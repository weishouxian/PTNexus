package repair

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/service/downloaderclient"
	processingmedia "github.com/pt-nexus/server/internal/service/processing/media"
)

const (
	defaultScreenshotCount = 3
	maxScreenshotCount     = 10
)

// ScreenshotCountFromConfig 读取截图默认数量配置，并将结果限制在允许范围内。
// 参数/返回：rootConfig 为应用根配置；返回 1-10 之间的截图数量，配置缺失时返回 3。
// 失败场景：配置结构或数值无法解析时回退默认值。
// 副作用：仅读取内存中的配置，不会写入配置或执行外部请求。
func ScreenshotCountFromConfig(rootConfig map[string]any) int {
	if rootConfig != nil {
		if crossSeed, ok := rootConfig["cross_seed"].(map[string]any); ok {
			if value, ok := parseIntAny(crossSeed["screenshot_count"]); ok {
				return normalizeScreenshotCount(value)
			}
		}
	}
	return defaultScreenshotCount
}

func screenshotCountFromInput(input ScreenshotGenerateInput) int {
	if value, ok := parseIntAny(input.Payload["screenshot_count"]); ok {
		return normalizeScreenshotCount(value)
	}
	return ScreenshotCountFromConfig(input.RootConfig)
}

func normalizeScreenshotCount(value int) int {
	if value < 1 {
		return defaultScreenshotCount
	}
	if value > maxScreenshotCount {
		return maxScreenshotCount
	}
	return value
}

// GenerateAndUploadScreenshots 从目标媒体自动截帧并上传到 Pixhost，返回可用图片链接列表。
// 参数/返回：输入包含 payload/source_info/content_name/config，返回去重后的截图 URL。
// 失败场景：路径定位失败、mpv/ffmpeg/ffprobe 不可用、上传失败时返回错误。
// 副作用：读取本地媒体、执行外部命令、向 Pixhost 发起网络请求；会输出叙事式纯文本日志。
func GenerateAndUploadScreenshots(input ScreenshotGenerateInput) ([]string, error) {
	payload := input.Payload
	sourceInfo := input.SourceInfo
	selectedSubtitleSID, selectedSubtitleProvided := parseSelectedSubtitleSIDAny(payload["selected_subtitle_sid"])
	screenshotCount := screenshotCountFromInput(input)

	logx.PlainInfof("开始执行截图和上传任务 (智能 HDR/SDR + 自动中文字幕)...")
	uploadCtx := PrepareScreenshotUploadContext(input.RootConfig)
	logx.PlainInfof("已选择图床服务: %s, 截图数量: %d", uploadCtx.Hoster, screenshotCount)

	savePath := strings.TrimSpace(toStringAny(payload["savePath"], toStringAny(payload["save_path"], "")))
	if savePath == "" {
		savePath = strings.TrimSpace(toStringAny(sourceInfo["save_path"], ""))
	}
	downloaderID := strings.TrimSpace(toStringAny(payload["downloaderId"], toStringAny(payload["downloader_id"], "")))
	torrentName := strings.TrimSpace(toStringAny(payload["torrentName"], toStringAny(payload["torrent_name"], "")))
	if torrentName == "" {
		torrentName = strings.TrimSpace(toStringAny(payload["name"], ""))
	}
	if torrentName == "" {
		torrentName = strings.TrimSpace(toStringAny(sourceInfo["main_title"], ""))
	}
	contentName := strings.TrimSpace(input.ContentName)
	preferExactRemotePath := false
	savePath, torrentName, preferExactRemotePath = enrichScreenshotSourceFromDownloader(input.RootConfig, payload, downloaderID, savePath, torrentName, contentName)

	// 对齐 MediaInfo：当 downloader.use_proxy=true 且本机不挂载媒体目录时，优先通过盒子代理远程截图。
	downloader, decision, dErr := downloaderclient.DecideProxy(input.RootConfig, downloaderID)
	logx.Infof(screenshotValidateLogModule, "截图代理判定 downloader_id=%s enabled=%t reason=%s proxy_host=%s proxy_port=%d err=%v", downloaderID, decision.Enabled, decision.Reason, downloader.Host, downloader.ProxyPort, dErr)
	if decision.Enabled {
		remoteCandidates := buildRemotePathCandidatesForProxy(savePath, torrentName, contentName, preferExactRemotePath)
		var lastErr error
		for candidateIndex, remoteCandidate := range remoteCandidates {
			logx.Infof(screenshotValidateLogModule, "截图代理尝试 scene=自动截图 candidate=%d/%d remote_path=%s", candidateIndex+1, len(remoteCandidates), remoteCandidate)
			bbcode, err := downloader.FetchScreenshotsByProxy(
				remoteCandidate,
				contentName,
				screenshotCount,
				buildSelectedSubtitleSIDPointer(selectedSubtitleSID, selectedSubtitleProvided),
			)
			if err == nil && strings.TrimSpace(bbcode) != "" {
				urls := ExtractImageURLsFromText(bbcode)
				logx.Infof(screenshotValidateLogModule, "截图代理响应 scene=自动截图 remote_path=%s bbcode_len=%d image_urls=%d", remoteCandidate, len([]rune(strings.TrimSpace(bbcode))), len(urls))
				if len(urls) > 0 {
					logx.PlainInfof("已通过盒子代理生成截图 remote_path=%s count=%d", remoteCandidate, len(urls))
					return urls, nil
				}
				lastErr = fmt.Errorf("代理返回的截图 BBCode 未包含可用图片链接")
				break
			}

			if apiErr, ok := err.(*downloaderclient.ProxyAPIError); ok && apiErr != nil {
				lastErr = err
				// 400 通常表示路径不存在/未找到视频文件，继续尝试下一个候选；其他错误回退本地逻辑。
				if apiErr.StatusCode == 400 {
					continue
				}
				break
			}
			lastErr = err
			break
		}
		if lastErr != nil {
			logx.PlainWarnf("盒子代理截图失败，回退本地截图 err=%v", lastErr)
		} else {
			logx.PlainWarnf("盒子代理截图未命中有效路径，回退本地截图 remote_candidates=%v", remoteCandidates)
		}
	} else if dErr != nil && strings.TrimSpace(decision.Reason) == "config_error" {
		logx.PlainWarnf("盒子代理截图跳过：读取下载器配置失败 downloader_id=%s err=%v", downloaderID, dErr)
	}

	translatedSavePath := TranslateDownloaderPath(input.RootConfig, downloaderID, savePath)
	if translatedSavePath != savePath && savePath != "" && translatedSavePath != "" {
		logx.PlainInfof("路径映射: %s -> %s", savePath, translatedSavePath)
	}
	if shouldSkipLocalScreenshotFallback(input.RootConfig, downloaderID, savePath, translatedSavePath, decision) {
		return nil, fmt.Errorf("下载器已启用远程模式，代理未能生成截图，且未配置本地路径映射，已停止本地扫描")
	}

	fullVideoPath := translatedSavePath
	if strings.TrimSpace(torrentName) != "" {
		fullVideoPath = filepath.Join(translatedSavePath, torrentName)
	}
	logx.PlainInfof("处理视频路径: %s", fullVideoPath)

	logx.PlainInfof("开始在路径 '%s' 中查找目标视频文件...", fullVideoPath)
	targetResult, err := resolveLocalMediaTargetResult(input.RootConfig, downloaderID, savePath, torrentName, contentName, "截图生成")
	if err != nil {
		logx.PlainWarnf("错误：在指定路径中未找到视频文件: %v", err)
		return nil, err
	}
	defer func() {
		if closeErr := targetResult.Close(); closeErr != nil {
			logx.Warnf(screenshotValidateLogModule, "关闭本地媒体访问会话失败 scene=%s source_path=%s err=%v", "截图生成", targetResult.SourcePath, closeErr)
		}
	}()
	targetVideoFile := targetResult.TargetFile
	logx.PlainInfof("找到目标媒体文件: source=%s target=%s", targetResult.SourcePath, targetVideoFile)

	mpvPath, err := resolveBinary("mpv", "PTNEXUS_MPV_PATH")
	if err != nil {
		logx.PlainWarnf("错误：找不到 mpv。请安装 mpv 或设置 PTNEXUS_MPV_PATH。")
		return nil, err
	}
	ffmpegPath, err := resolveBinary("ffmpeg", "PTNEXUS_FFMPEG_PATH")
	if err != nil {
		logx.PlainWarnf("错误：找不到 ffmpeg。请安装 ffmpeg 或设置 PTNEXUS_FFMPEG_PATH。")
		return nil, err
	}
	ffprobePath, err := resolveBinary("ffprobe", "PTNEXUS_FFPROBE_PATH")
	if err != nil {
		logx.PlainWarnf("错误：找不到 ffprobe。请安装 ffprobe 或设置 PTNEXUS_FFPROBE_PATH。")
		return nil, err
	}

	// 获取截图时间点：先智能分析，失败则回退百分比。
	points := getSmartScreenshotPoints(ffprobePath, targetVideoFile, screenshotCount)
	if len(points) < screenshotCount {
		logx.PlainWarnf("警告: 智能分析失败，回退到按百分比截图。")
		duration, err := probeDurationSeconds(targetVideoFile)
		if err != nil || duration <= 0 {
			logx.PlainWarnf("错误: 获取视频时长失败: %v", err)
			return nil, fmt.Errorf("读取视频时长失败: %w", err)
		}
		points = buildPreviewFallbackPoints(duration, screenshotCount)
	}
	sort.Float64s(points)

	// 自动检测中文字幕轨道（mpv sid）。
	logx.PlainInfof("正在分析字幕流...")
	inspection, selectedCandidate, hasSelectedCandidate, err := resolveLocalSubtitleCandidate(ffprobePath, targetVideoFile, selectedSubtitleSID)
	if err != nil {
		return nil, err
	}
	subtitleSID := inspection.CurrentSubtitleSID
	if selectedSubtitleProvided {
		subtitleSID = selectedSubtitleSID
		if selectedSubtitleSID > 0 && !hasSelectedCandidate {
			subtitleSID = inspection.CurrentSubtitleSID
		}
	}
	switch {
	case subtitleSID <= 0:
		logx.PlainInfof("   当前选择为无字幕，将截取无字幕画面。")
	case selectedSubtitleProvided && hasSelectedCandidate:
		logx.PlainInfof("   已按用户选择的字幕流截图 sid=%d title=%s", subtitleSID, strings.TrimSpace(selectedCandidate.Title))
	case inspection.State == ScreenshotSubtitleStateConfirmedChinese:
		logx.PlainInfof("   已检测到明确中文字幕，将自动挂载字幕截图 sid=%d", subtitleSID)
	default:
		logx.PlainInfof("   将使用当前预览字幕流截图 sid=%d", subtitleSID)
	}

	tmpDir, err := os.MkdirTemp("", "ptnexus-screens-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	type uploadJob struct {
		Index    int
		TimeStr  string
		FilePath string
	}
	type uploadResult struct {
		Index    int
		URL      string
		OK       bool
		LogBlock string
	}

	const uploadWorkers = 5
	jobs := make(chan uploadJob, len(points))
	results := make(chan uploadResult, len(points))
	var wg sync.WaitGroup
	wg.Add(uploadWorkers)
	for w := 0; w < uploadWorkers; w++ {
		go func() {
			defer wg.Done()
			for job := range jobs {
				var buf bytes.Buffer
				logLine := func(format string, args ...any) {
					buf.WriteString(fmt.Sprintf(format, args...))
					buf.WriteByte('\n')
				}
				showURL, err := uploadCtx.UploadScreenshot(job.FilePath, logLine)
				if err != nil || strings.TrimSpace(showURL) == "" {
					results <- uploadResult{Index: job.Index, OK: false, LogBlock: buf.String()}
					continue
				}
				logLine("   🚀 上传成功: %s", showURL)

				finalURL := uploadCtx.NormalizeScreenshotURL(showURL)
				results <- uploadResult{Index: job.Index, OK: true, URL: finalURL, LogBlock: buf.String()}
			}
		}()
	}

	uploadLogs := make([]string, len(points))
	uploadedURLs := make([]string, len(points))
	uploadedOK := make([]bool, len(points))

	for i, point := range points {
		timeStr := formatSecondHMS(point)
		fileStem := fmt.Sprintf("s%d_%s", i+1, timeStr)
		rawPNG := filepath.Join(tmpDir, "raw_"+fileStem+".png")

		logx.PlainInfof("")
		logx.PlainInfof("--- 处理第 %d/%d 张截图 (%s) ---", i+1, len(points), timeStr)

		if err := captureRawPNGWithMPV(mpvPath, targetVideoFile, point, rawPNG, subtitleSID); err != nil {
			logx.PlainInfof("❌ mpv 截图失败: %s", sanitizeCommandErrForLog(err))
			continue
		}
		if stat, statErr := os.Stat(rawPNG); statErr != nil || stat.Size() == 0 {
			logx.PlainInfof("❌ mpv 未生成文件: %s", rawPNG)
			continue
		}

		keywordHDR := hasScreenshotHDRKeyword(contentName) || hasScreenshotHDRKeyword(torrentName)
		metadataHDR := false
		if hdr, hdrErr := detectHDRFromPNG(ffprobePath, rawPNG); hdrErr == nil {
			metadataHDR = hdr
		} else {
			logx.PlainInfof("   ⚠️ 检测 HDR 信息失败，假定为 SDR: %v", hdrErr)
		}
		isHDR := keywordHDR || metadataHDR
		finalExt := ".png"
		if isHDR {
			finalExt = ".jpg"
		}
		finalImagePath := filepath.Join(tmpDir, fileStem+finalExt)
		logx.Infof(screenshotValidateLogModule, "截图 HDR 判定 scene=自动截图 index=%d keyword_hdr=%t metadata_hdr=%t hdr=%t output=%s", i+1, keywordHDR, metadataHDR, isHDR, finalImagePath)

		if isHDR {
			logx.PlainInfof("   🎨 检测到 HDR 原始内容，应用色调映射...")
		} else {
			logx.PlainInfof("   🎨 检测到 SDR 内容，应用标准 RGB 转换...")
		}

		startCompress := time.Now()
		if err := compressPNGWithFFmpeg(ffmpegPath, rawPNG, finalImagePath, isHDR); err != nil {
			logx.PlainInfof("❌ ffmpeg 压缩失败: %s", sanitizeCommandErrForLog(err))
			continue
		}
		compressTime := time.Since(startCompress).Seconds()

		srcSize := fileSizeBytes(rawPNG)
		dstSize := fileSizeBytes(finalImagePath)
		ratio := 0.0
		if srcSize > 0 {
			ratio = float64(dstSize) / float64(srcSize) * 100
		}
		logx.PlainInfof("   ✅ 压缩完成: %.2f MB (压缩率 %.1f%%) | 耗时 %.2fs | HDR: %v", float64(dstSize)/1024.0/1024.0, ratio, compressTime, isHDR)

		// 上传不并发截图，但上传并发。
		jobs <- uploadJob{Index: i, TimeStr: timeStr, FilePath: finalImagePath}
	}

	close(jobs)
	wg.Wait()
	close(results)

	for res := range results {
		if res.Index < 0 || res.Index >= len(points) {
			continue
		}
		uploadLogs[res.Index] = res.LogBlock
		if res.OK {
			uploadedOK[res.Index] = true
			uploadedURLs[res.Index] = res.URL
		}
	}

	logx.PlainInfof("")
	logx.PlainInfof("开始并发上传图片... 并发数: %d, 总数: %d", uploadWorkers, len(points))
	successCount := 0
	for i := 0; i < len(points); i++ {
		logx.PlainInfof("")
		logx.PlainInfof("--- 上传第 %d/%d 张截图 (%s) ---", i+1, len(points), formatSecondHMS(points[i]))
		if block := strings.TrimSpace(uploadLogs[i]); block != "" {
			for _, line := range strings.Split(block, "\n") {
				line = strings.TrimRight(line, "\r")
				if strings.TrimSpace(line) == "" {
					continue
				}
				logx.PlainInfof("%s", line)
			}
		}
		if uploadedOK[i] {
			successCount++
		} else {
			logx.PlainInfof("   ❌ 第 %d 张图片上传失败", i+1)
		}
	}

	finalList := make([]string, 0, successCount)
	for i := 0; i < len(points); i++ {
		if uploadedOK[i] && strings.TrimSpace(uploadedURLs[i]) != "" {
			finalList = append(finalList, uploadedURLs[i])
		}
	}
	if len(finalList) == 0 {
		return nil, fmt.Errorf("未生成可用截图")
	}
	return finalList, nil
}

// GenerateAndUploadRandomScreenshots 按随机时间点生成指定数量的正式截图并上传图床。
// 参数/返回：screenshotCount 为需要生成的截图数量；返回去重后的截图 URL 列表。
// 失败场景：目标媒体无法定位、无法读取时长、截图/上传失败时返回错误。
// 副作用：会读取本地媒体文件、执行外部命令，并向图床或盒子代理发起请求。
func GenerateAndUploadRandomScreenshots(input ScreenshotGenerateInput, screenshotCount int) ([]string, error) {
	if urls, handled, err := generateRandomScreenshotsByProxy(input, screenshotCount); handled {
		return urls, err
	}
	points, err := buildRandomScreenshotPoints(input, screenshotCount)
	if err != nil {
		return nil, err
	}
	return generateAndUploadScreenshotsWithPoints(input, points, true)
}

// generateRandomScreenshotsByProxy 优先通过盒子代理生成随机正式截图。
// 参数/返回：input 为截图上下文，screenshotCount 为截图数量；handled 表示代理链路已决定结果。
// 失败场景：代理失败且不允许本地兜底时返回错误；未启用代理时 handled=false。
// 副作用：可能请求下载器补齐保存路径，并向盒子代理发起截图上传请求。
func generateRandomScreenshotsByProxy(input ScreenshotGenerateInput, screenshotCount int) ([]string, bool, error) {
	screenshotCount = normalizeRandomScreenshotCount(screenshotCount)
	payload := input.Payload
	sourceInfo := input.SourceInfo
	selectedSubtitleSID, selectedSubtitleProvided := parseSelectedSubtitleSIDAny(payload["selected_subtitle_sid"])
	savePath, downloaderID, torrentName, contentName, preferExactRemotePath := parseScreenshotSourceParams(input.RootConfig, payload, sourceInfo, input.ContentName)

	downloader, decision, dErr := downloaderclient.DecideProxy(input.RootConfig, downloaderID)
	logx.Infof(screenshotValidateLogModule, "随机正式截图代理判定 downloader_id=%s enabled=%t reason=%s proxy_host=%s proxy_port=%d err=%v", downloaderID, decision.Enabled, decision.Reason, downloader.Host, downloader.ProxyPort, dErr)
	if !decision.Enabled {
		if dErr != nil && strings.TrimSpace(decision.Reason) == "config_error" {
			logx.PlainWarnf("随机正式截图代理跳过：读取下载器配置失败 downloader_id=%s err=%v", downloaderID, dErr)
		}
		return nil, false, nil
	}

	remoteCandidates := buildRemotePathCandidatesForProxy(savePath, torrentName, contentName, preferExactRemotePath)
	var lastErr error
	for candidateIndex, remoteCandidate := range remoteCandidates {
		logx.Infof(screenshotValidateLogModule, "随机正式截图代理尝试 candidate=%d/%d remote_path=%s count=%d", candidateIndex+1, len(remoteCandidates), remoteCandidate, screenshotCount)
		bbcode, err := downloader.FetchRandomScreenshotsByProxy(
			remoteCandidate,
			contentName,
			screenshotCount,
			buildSelectedSubtitleSIDPointer(selectedSubtitleSID, selectedSubtitleProvided),
		)
		if err == nil && strings.TrimSpace(bbcode) != "" {
			urls := ExtractImageURLsFromText(bbcode)
			logx.Infof(screenshotValidateLogModule, "随机正式截图代理响应 remote_path=%s bbcode_len=%d image_urls=%d", remoteCandidate, len([]rune(strings.TrimSpace(bbcode))), len(urls))
			if len(urls) > 0 {
				logx.PlainInfof("已通过盒子代理生成随机正式截图 remote_path=%s count=%d", remoteCandidate, len(urls))
				return urls, true, nil
			}
			lastErr = fmt.Errorf("代理返回的截图 BBCode 未包含可用图片链接")
			break
		}
		if apiErr, ok := err.(*downloaderclient.ProxyAPIError); ok && apiErr != nil {
			lastErr = err
			if apiErr.StatusCode == 400 {
				continue
			}
			break
		}
		lastErr = err
		break
	}

	translatedSavePath := TranslateDownloaderPath(input.RootConfig, downloaderID, savePath)
	if shouldSkipLocalScreenshotFallback(input.RootConfig, downloaderID, savePath, translatedSavePath, decision) {
		if lastErr == nil {
			lastErr = fmt.Errorf("盒子代理随机正式截图未命中有效路径: %v", remoteCandidates)
		}
		return nil, true, fmt.Errorf("盒子代理随机正式截图失败: %w", lastErr)
	}
	if lastErr != nil {
		logx.PlainWarnf("盒子代理随机正式截图失败，回退本地截图 err=%v", lastErr)
	} else {
		logx.PlainWarnf("盒子代理随机正式截图未命中有效路径，回退本地截图 remote_candidates=%v", remoteCandidates)
	}
	return nil, false, nil
}

func buildRandomScreenshotPoints(input ScreenshotGenerateInput, screenshotCount int) ([]float64, error) {
	screenshotCount = normalizeRandomScreenshotCount(screenshotCount)
	payload := input.Payload
	sourceInfo := input.SourceInfo
	savePath := strings.TrimSpace(toStringAny(payload["savePath"], toStringAny(payload["save_path"], "")))
	if savePath == "" {
		savePath = strings.TrimSpace(toStringAny(sourceInfo["save_path"], ""))
	}
	downloaderID := strings.TrimSpace(toStringAny(payload["downloaderId"], toStringAny(payload["downloader_id"], "")))
	torrentName := strings.TrimSpace(toStringAny(payload["torrentName"], toStringAny(payload["torrent_name"], "")))
	if torrentName == "" {
		torrentName = strings.TrimSpace(toStringAny(payload["name"], ""))
	}
	if torrentName == "" {
		torrentName = strings.TrimSpace(toStringAny(sourceInfo["main_title"], ""))
	}
	contentName := strings.TrimSpace(input.ContentName)
	savePath, torrentName, _ = enrichScreenshotSourceFromDownloader(input.RootConfig, payload, downloaderID, savePath, torrentName, contentName)

	targetResult, err := resolveLocalMediaTargetResult(input.RootConfig, downloaderID, savePath, torrentName, contentName, "随机截图生成")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = targetResult.Close()
	}()

	duration, err := probeDurationSeconds(targetResult.TargetFile)
	if err != nil || duration <= 0 {
		return nil, fmt.Errorf("读取视频时长失败: %w", err)
	}
	points := buildRandomScreenshotPointsByDuration(duration, screenshotCount)
	if len(points) < screenshotCount {
		return nil, fmt.Errorf("未能生成足够的随机截图时间点")
	}
	return points, nil
}

func buildRandomScreenshotPointsByDuration(duration float64, screenshotCount int) []float64 {
	screenshotCount = normalizeRandomScreenshotCount(screenshotCount)
	if duration <= 0 || screenshotCount <= 0 {
		return []float64{}
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	bucketDuration := duration / float64(screenshotCount)
	points := make([]float64, 0, screenshotCount)
	for i := 0; i < screenshotCount; i++ {
		start := bucketDuration * float64(i)
		end := bucketDuration * float64(i+1)
		margin := bucketDuration * 0.2
		if margin < 1 {
			margin = bucketDuration * 0.1
		}
		lower := start + margin
		upper := end - margin
		point := (start + end) / 2
		if upper > lower {
			point = lower + rng.Float64()*(upper-lower)
		}
		if point < 0.5 {
			point = 0.5
		}
		if point > duration-0.5 {
			point = duration - 0.5
		}
		points = append(points, point)
	}
	sort.Float64s(points)
	return points
}

func normalizeRandomScreenshotCount(value int) int {
	if value <= 0 {
		return 3
	}
	if value > 10 {
		return 10
	}
	return value
}

func buildRemotePathCandidatesForProxy(savePath, torrentName, contentName string, preferExactPath bool) []string {
	trimmedSavePath := strings.TrimSpace(savePath)
	trimmedTorrentName := strings.TrimSpace(torrentName)
	trimmedContentName := strings.TrimSpace(contentName)

	candidates := make([]string, 0, 3)
	appendCandidate := func(candidate string) {
		normalized := normalizeProxyRemotePath(candidate)
		if normalized == "" {
			return
		}
		for _, existing := range candidates {
			if normalizeScreenshotPathForCompare(existing) == normalizeScreenshotPathForCompare(normalized) {
				return
			}
		}
		candidates = append(candidates, normalized)
	}
	if preferExactPath && trimmedSavePath != "" {
		appendCandidate(trimmedSavePath)
	}
	if trimmedSavePath != "" && trimmedTorrentName != "" {
		appendCandidate(joinProxyRemotePath(trimmedSavePath, trimmedTorrentName))
	}
	if trimmedSavePath != "" && trimmedContentName != "" && !strings.EqualFold(trimmedContentName, trimmedTorrentName) {
		appendCandidate(joinProxyRemotePath(trimmedSavePath, trimmedContentName))
	}
	if trimmedSavePath != "" {
		appendCandidate(trimmedSavePath)
	}
	return candidates
}

func enrichScreenshotSourceFromDownloader(rootConfig map[string]any, payload map[string]any, downloaderID, savePath, torrentName, contentName string) (string, string, bool) {
	if strings.TrimSpace(downloaderID) == "" {
		return savePath, torrentName, false
	}
	seedHash := firstNonEmptyScreenshotString(
		toStringAny(payload["downloader_hash"], ""),
		toStringAny(payload["downloaderHash"], ""),
	)
	if seedHash == "" && strings.TrimSpace(torrentName) == "" && strings.TrimSpace(contentName) == "" && strings.TrimSpace(savePath) == "" {
		return savePath, torrentName, false
	}
	downloader, err := downloaderclient.FromConfig(rootConfig, downloaderID)
	if err != nil {
		logx.Warnf(screenshotValidateLogModule, "截图路径回填跳过：读取下载器失败 downloader_id=%s err=%v", downloaderID, err)
		return savePath, torrentName, false
	}
	snapshots, err := downloader.FetchTorrents()
	if err != nil {
		logx.Warnf(screenshotValidateLogModule, "截图路径回填跳过：拉取下载器任务失败 downloader_id=%s err=%v", downloaderID, err)
		return savePath, torrentName, false
	}
	for _, snapshot := range snapshots {
		bestPath := strings.TrimSpace(snapshot.ContentPath)
		preferExactPath := bestPath != ""
		if bestPath == "" {
			bestPath = strings.TrimSpace(snapshot.SavePath)
		}
		if bestPath == "" {
			continue
		}
		matched := false
		if seedHash != "" && strings.EqualFold(seedHash, strings.TrimSpace(snapshot.Hash)) {
			matched = true
		}
		if !matched && strings.TrimSpace(snapshot.Name) != "" {
			for _, candidateName := range []string{torrentName, contentName} {
				if strings.TrimSpace(candidateName) != "" && strings.TrimSpace(snapshot.Name) == strings.TrimSpace(candidateName) {
					matched = true
					break
				}
			}
		}
		if !matched && strings.TrimSpace(savePath) != "" && normalizeScreenshotPathForCompare(snapshot.ContentPath) == normalizeScreenshotPathForCompare(savePath) {
			matched = true
		}
		if !matched {
			continue
		}
		if strings.TrimSpace(snapshot.Name) != "" {
			torrentName = strings.TrimSpace(snapshot.Name)
		}
		logx.Infof(
			screenshotValidateLogModule,
			"截图路径回填完成 downloader_id=%s hash=%s torrent_name=%s save_path=%s content_path=%s used_path=%s prefer_exact=%t",
			downloaderID,
			snapshot.Hash,
			snapshot.Name,
			snapshot.SavePath,
			snapshot.ContentPath,
			bestPath,
			preferExactPath,
		)
		return bestPath, torrentName, preferExactPath
	}
	logx.Warnf(screenshotValidateLogModule, "截图路径回填未命中 downloader_id=%s seed_hash=%s torrent_name=%s", downloaderID, seedHash, torrentName)
	return savePath, torrentName, false
}

func firstNonEmptyScreenshotString(items ...string) string {
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			return strings.TrimSpace(item)
		}
	}
	return ""
}

func joinProxyRemotePath(base, name string) string {
	normalizedBase := normalizeProxyRemotePath(base)
	normalizedName := strings.Trim(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"), "/")
	if normalizedBase == "" {
		return normalizedName
	}
	if normalizedName == "" {
		return normalizedBase
	}
	return pathpkg.Join(normalizedBase, normalizedName)
}

func normalizeProxyRemotePath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return strings.ReplaceAll(trimmed, "\\", "/")
}

func fileSizeBytes(path string) int64 {
	stat, err := os.Stat(path)
	if err != nil || stat == nil {
		return 0
	}
	return stat.Size()
}

func detectHDRFromPNG(ffprobePath string, pngPath string) (bool, error) {
	cmd := exec.Command(ffprobePath, "-v", "error", "-show_streams", pngPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			text = err.Error()
		}
		return false, fmt.Errorf("ffprobe 执行失败: %s", text)
	}
	return isHDRMetadataText(string(out)), nil
}

// isHDRMetadataText 根据 ffprobe 输出文本判断源是否为 HDR。
// 参数/返回：text 为 ffprobe 的输出；返回是否按 HDR 处理。
// 失败场景：无。
// 副作用：无。
// 判据说明：优先看标准色彩标记（bt2020 / smpte2084）与 Dolby Vision 关键词；
// 若两者都没有，但源是 10bit 及以上的 HEVC/VP9/AV1 且色彩三元组全部未标注，
// 则按 HDR 处理 —— Dolby Vision Profile 5 正是这种形态（IPT 编码，元数据缺失），
// 否则会被误判为 SDR 而绕过色彩转换链，产出偏色的截图。
func isHDRMetadataText(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "smpte2084") || strings.Contains(lower, "bt2020") ||
		strings.Contains(lower, "dovi") || strings.Contains(lower, "dolby vision") ||
		strings.Contains(lower, "dolbyvision") || containsDolbyVisionToken(lower) {
		return true
	}
	if !hasTenBitOrHigherPixelFormat(lower) {
		return false
	}
	if !hasAnyToken(lower, "hevc", "h265", "vp9", "av1") {
		return false
	}
	return !hasKnownColorMetadata(lower)
}

// hasTenBitOrHigherPixelFormat 判断 ffprobe 文本中是否出现 10bit 及以上的像素格式。
// 参数/返回：text 为已小写的 ffprobe 输出；返回是否命中。
// 失败场景：无。
// 副作用：无。
func hasTenBitOrHigherPixelFormat(text string) bool {
	for _, marker := range []string{"pix_fmt=yuv420p10", "pix_fmt=yuv422p10", "pix_fmt=yuv444p10",
		"pix_fmt=yuv420p12", "pix_fmt=yuv422p12", "pix_fmt=yuv444p12", "pix_fmt=p010", "pix_fmt=p012"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// hasAnyToken 判断文本中是否包含任一关键词。
// 参数/返回：text 为已小写的文本；tokens 为候选关键词；返回是否命中。
// 失败场景：无。
// 副作用：无。
func hasAnyToken(text string, tokens ...string) bool {
	for _, token := range tokens {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

// hasKnownColorMetadata 判断 ffprobe 文本里的色彩三元组是否被明确标注。
// 参数/返回：text 为已小写的 ffprobe 输出；返回是否存在已知取值。
// 失败场景：无。
// 副作用：无。
// 说明：ffprobe 对未标注的色彩字段会输出 unknown / unspecified / reserved，这些视为“无元数据”。
func hasKnownColorMetadata(text string) bool {
	for _, key := range []string{"color_space=", "color_transfer=", "color_primaries="} {
		index := strings.Index(text, key)
		if index < 0 {
			continue
		}
		value := text[index+len(key):]
		if end := strings.IndexAny(value, "\r\n"); end >= 0 {
			value = value[:end]
		}
		switch strings.TrimSpace(value) {
		case "", "unknown", "unspecified", "reserved":
			continue
		}
		return true
	}
	return false
}

// containsDolbyVisionToken 判断文本中是否存在独立的“dv”标记（大小写不敏感）。
// 参数/返回：value 为待判断文本；返回是否存在。
// 失败场景：无。
// 副作用：无。
// 说明：资源名里 Dolby Vision 常写成 .DV. / [DV] / DV- 这类形式，单纯 Contains("dv ") 会漏判，
// 而 Contains("dv") 又会误伤 dvdr 之类的词，因此按分隔符判定独立 token。
func containsDolbyVisionToken(value string) bool {
	lower := strings.ToLower(value)
	for offset := 0; offset+2 <= len(lower); {
		index := strings.Index(lower[offset:], "dv")
		if index < 0 {
			return false
		}
		position := offset + index
		beforeOK := position == 0 || isTokenSeparator(lower[position-1])
		after := position + 2
		afterOK := after >= len(lower) || isTokenSeparator(lower[after])
		if beforeOK && afterOK {
			return true
		}
		offset = position + 2
	}
	return false
}

// isTokenSeparator 判断字节是否为分隔 token 的字符。
// 参数/返回：b 为待判断字节；返回是否为分隔符。
// 失败场景：无。
// 副作用：无。
func isTokenSeparator(b byte) bool {
	switch b {
	case ' ', '.', '_', '-', '+', '[', ']', '(', ')', '/', '\\', ',', ':', '~':
		return true
	}
	return false
}

// hasScreenshotHDRKeyword 根据下载任务名称补充 HDR 判断，避免截图文件缺少色彩元数据时被误判为 SDR。
func hasScreenshotHDRKeyword(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(lower, "hdr") ||
		strings.Contains(lower, "dovi") ||
		strings.Contains(lower, "dolby vision") ||
		containsDolbyVisionToken(lower)
}

func captureRawPNGWithMPV(mpvPath string, videoPath string, second float64, outputPath string, subtitleSID int) error {
	cmd := []string{
		mpvPath,
		"--no-audio",
		fmt.Sprintf("--start=%.2f", second),
		"--frames=1",
		"--screenshot-high-bit-depth=yes",
		"--screenshot-png-compression=0",
		"--screenshot-tag-colorspace=yes",
		fmt.Sprintf("--o=%s", outputPath),
	}
	if subtitleSID > 0 {
		cmd = append(cmd, fmt.Sprintf("--sid=%d", subtitleSID), "--sub-visibility=yes")
	} else {
		cmd = append(cmd, "--sid=no")
	}
	cmd = append(cmd, videoPath)

	proc := exec.Command(cmd[0], cmd[1:]...)
	out, err := proc.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			text = err.Error()
		}
		return fmt.Errorf("mpv 执行失败: %s", text)
	}
	return nil
}

// toneMapFilterCandidate 描述一条色彩转换滤镜链候选。
type toneMapFilterCandidate struct {
	// Name 为候选来源标识，用于日志定位实际生效的滤镜链。
	Name string
	// Filter 为可直接传给 ffmpeg -vf 的完整滤镜链。
	Filter string
	// PreArgs 为执行该候选时需要前置的额外 ffmpeg 选项，例如显式创建 Vulkan 设备。
	PreArgs []string
	// Degraded 为 true 表示该候选不做色调映射，输出颜色可能偏暗或偏灰。
	Degraded bool
}

// toneMapChainOptions 描述一次色彩转换任务的滤镜链选项。
type toneMapChainOptions struct {
	// SDRFilter 为源为 SDR 时直接使用的滤镜链。
	SDRFilter string
	// HDRTail 为 HDR 色调映射链的链尾（缩放与输出像素格式）。
	HDRTail string
	// FallbackFilter 为色调映射全部失败时的兜底链，不做色彩转换。
	FallbackFilter string
	// OutRange 为色调映射链的输出 range，取值 tv 或 pc。
	OutRange string
}

var (
	// pngToneMapChainOptions 为正式截图（PNG/JPEG）链路使用的色彩转换选项。
	pngToneMapChainOptions = toneMapChainOptions{
		SDRFilter:      "format=rgb24",
		HDRTail:        "format=rgb24",
		FallbackFilter: "scale='min(3840,iw)':-2:flags=lanczos,unsharp=5:5:0.30:3:3:0.15,format=yuv420p",
		OutRange:       "pc",
	}
	// previewToneMapChainOptions 为预览截图（640 宽 JPEG）链路使用的色彩转换选项。
	previewToneMapChainOptions = toneMapChainOptions{
		SDRFilter:      "scale='min(640,iw)':-2,format=yuv420p",
		HDRTail:        "scale='min(640,iw)':-2,format=yuv420p",
		FallbackFilter: "scale='min(640,iw)':-2,format=yuv420p",
		OutRange:       "tv",
	}
)

// libplaceboHwDeviceArgs 让 ffmpeg 先用自身的 Vulkan hwcontext 建好设备，再交给 libplacebo 使用。
// 纯 CPU 服务器（只装了 mesa-vulkan-drivers/lavapipe、无独显）下，libplacebo 自己的设备选择会把
// CPU 类型设备判为不可用并直接报 “Found no suitable device”；由 hwcontext 显式创建后即可正常出图。
var libplaceboHwDeviceArgs = []string{"-init_hw_device", "vulkan=vk", "-filter_hw_device", "vk"}

// buildToneMapFilterCandidates 按优先级构造色彩转换滤镜链候选。
// 参数/返回：isHDR 标记源是否为 HDR；options 定义链尾与兜底链；返回按优先级排序的候选列表。
// 失败场景：无。
// 副作用：仅构造字符串，不执行外部命令。
// 优先级说明：libplacebo 能正确处理 Dolby Vision Profile 5 等非标准色彩空间（需要可用的 Vulkan 设备，
// 无独显时用 libplacebo-hwdevice 借助软件 Vulkan）；常规 zscale 依赖帧内色彩元数据；
// 显式 zscale 在元数据缺失时补 BT.2020/PQ 输入参数；兜底链只保证出图。
func buildToneMapFilterCandidates(isHDR bool, options toneMapChainOptions) []toneMapFilterCandidate {
	if !isHDR {
		return []toneMapFilterCandidate{{Name: "sdr", Filter: options.SDRFilter}}
	}
	libplaceboFilter := "libplacebo=tonemapping=hable:colorspace=bt709:color_primaries=bt709:color_trc=bt709:range=" + options.OutRange + "," + options.HDRTail
	return []toneMapFilterCandidate{
		{
			Name:   "libplacebo",
			Filter: libplaceboFilter,
		},
		{
			Name:    "libplacebo-hwdevice",
			Filter:  libplaceboFilter,
			PreArgs: libplaceboHwDeviceArgs,
		},
		{
			Name:   "zscale",
			Filter: "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=" + options.OutRange + "," + options.HDRTail,
		},
		{
			Name:   "zscale-explicit",
			Filter: "zscale=pin=bt2020:tin=smpte2084:min=bt2020nc:rin=pc:t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=" + options.OutRange + "," + options.HDRTail,
		},
		{
			Name:     "passthrough",
			Filter:   options.FallbackFilter,
			Degraded: true,
		},
	}
}

// prependToneMapPreArgs 把候选自带的额外 ffmpeg 选项放到命令参数最前面。
// 参数/返回：candidate 提供 PreArgs；args 为调用方构造的参数；返回可直接执行的参数切片。
// 失败场景：无。
// 副作用：返回新切片，不修改入参。
func prependToneMapPreArgs(candidate toneMapFilterCandidate, args []string) []string {
	if len(candidate.PreArgs) == 0 {
		return args
	}
	merged := make([]string, 0, len(candidate.PreArgs)+len(args))
	merged = append(merged, candidate.PreArgs...)
	return append(merged, args...)
}

// runToneMapFilterAttempts 依次尝试候选滤镜链，返回首个产出有效文件的结果。
// 参数/返回：outputPath 为输出文件路径；candidates 为候选列表；run 负责按候选执行一次转换并返回命令输出。
// 失败场景：全部候选失败或均未产出有效文件时返回最后一次错误。
// 副作用：删除失败尝试留下的输出文件；命中兜底链时输出降级警告。
func runToneMapFilterAttempts(outputPath string, candidates []toneMapFilterCandidate, run func(toneMapFilterCandidate) ([]byte, error)) (toneMapFilterCandidate, string, error) {
	var lastOutput []byte
	var lastErr error
	for _, candidate := range candidates {
		output, err := run(candidate)
		if err == nil {
			if stat, statErr := os.Stat(outputPath); statErr == nil && stat.Size() > 0 {
				if candidate.Degraded {
					logx.PlainWarnf("⚠️ 色调映射滤镜不可用，已按原样输出，颜色可能偏暗或偏灰（filter=%s）", candidate.Name)
				} else {
					logx.Infof(screenshotValidateLogModule, "色彩转换滤镜链命中 name=%s output=%s", candidate.Name, outputPath)
				}
				return candidate, string(output), nil
			}
			err = fmt.Errorf("输出文件未生成")
		}
		logx.Infof(screenshotValidateLogModule, "色彩转换滤镜链未生效，尝试下一个候选 name=%s err=%v", candidate.Name, err)
		lastOutput = output
		lastErr = err
		_ = os.Remove(outputPath)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的色彩转换滤镜链")
	}
	return toneMapFilterCandidate{}, string(lastOutput), lastErr
}

// formatFFmpegFailureOutput 整理 ffmpeg 失败输出，便于写入日志。
// 参数/返回：output 为命令输出；err 为命令错误；返回非空文本。
// 失败场景：无。
// 副作用：无。
func formatFFmpegFailureOutput(output string, err error) string {
	if text := strings.TrimSpace(output); text != "" {
		return text
	}
	if err != nil {
		return err.Error()
	}
	return "未知错误"
}

// compressPNGWithFFmpeg 把 mpv 导出的原始 PNG 转换为可上传的 PNG/JPEG，并处理 HDR 色调映射。
// 参数/返回：ffmpegPath 为 ffmpeg 路径；srcPNG 为源图；dstPNG 为输出路径（扩展名决定编码格式）；isHDR 标记源是否为 HDR。
// 失败场景：所有滤镜链候选均失败或输出文件未生成时返回错误。
// 副作用：执行 ffmpeg 命令；色调映射不可用时降级为不做色彩转换的直出链并输出警告日志。
func compressPNGWithFFmpeg(ffmpegPath string, srcPNG string, dstPNG string, isHDR bool) error {
	isJPEG := strings.EqualFold(filepath.Ext(dstPNG), ".jpg") || strings.EqualFold(filepath.Ext(dstPNG), ".jpeg")
	candidates := buildToneMapFilterCandidates(isHDR, pngToneMapChainOptions)
	_, output, err := runToneMapFilterAttempts(dstPNG, candidates, func(candidate toneMapFilterCandidate) ([]byte, error) {
		args := prependToneMapPreArgs(candidate, []string{
			"-y", "-v", "error", "-i", srcPNG, "-frames:v", "1", "-vf", candidate.Filter,
		})
		if isJPEG {
			args = append(args, "-q:v", "2")
		} else {
			args = append(args, "-compression_level", "4", "-pred", "mixed")
		}
		args = append(args, dstPNG)
		return exec.Command(ffmpegPath, args...).CombinedOutput()
	})
	if err != nil {
		return fmt.Errorf("ffmpeg 执行失败: %s", formatFFmpegFailureOutput(output, err))
	}
	return nil
}

func resolveBinary(binName, envKey string) (string, error) {
	if envKey != "" {
		if configured := strings.TrimSpace(os.Getenv(envKey)); configured != "" {
			if _, err := os.Stat(configured); err == nil {
				return configured, nil
			}
			return "", fmt.Errorf("%s 指向的可执行文件不存在: %s", envKey, configured)
		}
	}
	if found, err := exec.LookPath(binName); err == nil {
		return found, nil
	}
	if envKey != "" {
		return "", fmt.Errorf("未找到 %s，可安装后重试，或设置 %s", binName, envKey)
	}
	return "", fmt.Errorf("未找到 %s，可安装后重试", binName)
}

func probeDurationSeconds(targetFile string) (float64, error) {
	ffprobePath, err := resolveBinary("ffprobe", "PTNEXUS_FFPROBE_PATH")
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(
		ffprobePath,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		targetFile,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text == "" {
			text = err.Error()
		}
		return 0, fmt.Errorf("ffprobe 执行失败: %s", text)
	}
	value := strings.TrimSpace(string(output))
	duration, parseErr := strconv.ParseFloat(value, 64)
	if parseErr != nil || duration <= 0 {
		return 0, fmt.Errorf("无法解析视频时长: %s", value)
	}
	return duration, nil
}

func formatSecondHMS(second float64) string {
	totalSeconds := int(second)
	minutes, sec := divmod(totalSeconds, 60)
	hour, min := divmod(minutes, 60)
	return fmt.Sprintf("%02dh%02dm%02ds", hour, min, sec)
}

func divmod(a, b int) (int, int) {
	if b == 0 {
		return 0, a
	}
	return a / b, a % b
}

func resolveLocalMediaTargetResult(rootConfig map[string]any, downloaderID, savePath, torrentName, contentName, scene string) (*processingmedia.ResolvedMediaTarget, error) {
	translatedSavePath := TranslateDownloaderPath(rootConfig, downloaderID, savePath)
	return processingmedia.ResolveMediaTargetByCandidates(translatedSavePath, torrentName, contentName, scene)
}

func sanitizeCommandErrForLog(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	if text == "" {
		return ""
	}
	switch {
	case strings.Contains(text, "ffprobe 执行失败"):
		return "ffprobe 执行失败"
	case strings.Contains(text, "ffmpeg 执行失败"):
		return "ffmpeg 执行失败"
	case strings.Contains(text, "mpv 执行失败"):
		return "mpv 执行失败"
	default:
		return text
	}
}

// TranslateDownloaderPath 按下载器路径映射把远端保存路径转换为本地路径。
func TranslateDownloaderPath(rootConfig map[string]any, downloaderID, remotePath string) string {
	return downloaderclient.TranslateDownloaderPath(rootConfig, downloaderID, remotePath)
}

func shouldSkipLocalScreenshotFallback(rootConfig map[string]any, downloaderID, savePath, translatedSavePath string, decision downloaderclient.ProxyDecision) bool {
	if !decision.Enabled {
		return false
	}
	if strings.TrimSpace(translatedSavePath) == "" {
		translatedSavePath = TranslateDownloaderPath(rootConfig, downloaderID, savePath)
	}
	return normalizeScreenshotPathForCompare(savePath) == normalizeScreenshotPathForCompare(translatedSavePath)
}

func normalizeScreenshotPathForCompare(value string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	for strings.Contains(normalized, "//") {
		normalized = strings.ReplaceAll(normalized, "//", "/")
	}
	return strings.TrimRight(normalized, "/")
}
