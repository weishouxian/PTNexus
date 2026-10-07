package media

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// BDInfo 碟指纹正则：用于在「本体已确认是碟结构」的前提下推断是否 DIY（自制/精简）原盘。
var (
	reBDInfoDiscLabel     = regexp.MustCompile(`(?im)^\s*Disc\s+Label\s*:\s*(.+?)\s*$`)
	reBDInfoDiscSizeBytes = regexp.MustCompile(`(?im)^\s*Disc\s+Size\s*:\s*([\d,]+)\s*bytes`)
	reBDInfoProtection    = regexp.MustCompile(`(?im)^\s*Protection\s*:\s*(.+?)\s*$`)
	reBDInfoPlaylistName  = regexp.MustCompile(`(?im)^\s*Name\s*:\s*(\d{5}\.MPLS)\s*$`)
	reBDInfoPlaylistSize  = regexp.MustCompile(`(?im)^\s*Size\s*:\s*([\d,]+)\s*bytes`)

	// 工具生成的卷标（如 `iso-4207589629`）：商业碟不会用这种名字当 volume label。
	reBDInfoToolGeneratedLabel = regexp.MustCompile(`(?i)^(iso|bdmv|bdiso|disc|untitled|my)[-_ ]?\w{4,}$`)

	// 卷标里的发布参数标记（分辨率）：卷标带 1080p/2160p 这类字样说明它是发布名而非商业碟卷标。
	reDiscLabelReleaseToken = regexp.MustCompile(`(?i)\d{3,4}[pi]`)
)

const (
	// discDIYNoMenuPayloadToleranceBytes 无菜单判定容差：碟大小减去正片 playlist 后剩余不足该值，
	// 说明碟里没有菜单 / BD-J / 花絮 / 第二条 playlist，只剩正片流。
	discDIYNoMenuPayloadToleranceBytes = 1 << 20 // 1 MiB
	// discDIYLabelCompareMinRunes 卷标与发布名做「互相包含」比较时的最短长度门槛，避免短名误配。
	discDIYLabelCompareMinRunes = 12
)

// DiscDIYFingerprint 汇总 BDInfo 里可用于判定 DIY 的碟指纹读数。
type DiscDIYFingerprint struct {
	// HasDiscInfo 为真表示成功解析出 Disc Label 与 Disc Size（即文本确实是 BDInfo 报告）。
	HasDiscInfo bool
	// DiscLabel 为 BDInfo 的 Disc Label 原文。
	DiscLabel string
	// DiscSizeBytes 为 BDInfo 的 Disc Size（字节）。
	DiscSizeBytes int64
	// MainPlaylistBytes 为最大的 playlist 体积（字节），即正片流。
	MainPlaylistBytes int64
	// PlaylistCount 为 PLAYLIST REPORT 段里的 playlist 条数。
	PlaylistCount int
	// AACSRetained 为真表示碟内仍保留 AACS 目录。
	AACSRetained bool

	// LabelMatchesRelease 为真表示卷标与发布名（种子名）一致或互为包含。
	LabelMatchesRelease bool
	// LabelToolGenerated 为真表示卷标呈工具生成形态（如 iso-4207589629）。
	LabelToolGenerated bool
	// NoMenuPayload 为真表示只剩正片：单 playlist 且几乎没有碟结构开销。
	NoMenuPayload bool
}

// ParseBDInfoDiscFingerprint 解析 BDInfo 文本中的碟指纹。
// 参数/返回：mediaText 为媒体文本（BDInfo 报告）；releaseName 为发布名/种子名，用于比对卷标；返回指纹读数。
// 失败场景：文本为空、非 BDInfo（缺 Disc Label / Disc Size）或字节数解析失败时，
// 返回的 HasDiscInfo 为 false，调用方据此跳过判定。
// 副作用：无。
func ParseBDInfoDiscFingerprint(mediaText, releaseName string) DiscDIYFingerprint {
	fingerprint := DiscDIYFingerprint{}

	text := strings.TrimSpace(mediaText)
	if text == "" {
		return fingerprint
	}

	labelMatch := reBDInfoDiscLabel.FindStringSubmatch(text)
	sizeMatch := reBDInfoDiscSizeBytes.FindStringSubmatch(text)
	if len(labelMatch) < 2 || len(sizeMatch) < 2 {
		return fingerprint
	}
	discSize, err := parseBDInfoByteCount(sizeMatch[1])
	if err != nil || discSize <= 0 {
		return fingerprint
	}

	fingerprint.HasDiscInfo = true
	fingerprint.DiscLabel = strings.TrimSpace(labelMatch[1])
	fingerprint.DiscSizeBytes = discSize

	fingerprint.PlaylistCount = len(reBDInfoPlaylistName.FindAllStringSubmatch(text, -1))

	// FILES 段的数据行以 `00000.M2TS` 开头、没有 `Size:` 前缀，因此该正则只会命中 playlist 的体积行。
	for _, match := range reBDInfoPlaylistSize.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		value, parseErr := parseBDInfoByteCount(match[1])
		if parseErr != nil {
			continue
		}
		if value > fingerprint.MainPlaylistBytes {
			fingerprint.MainPlaylistBytes = value
		}
	}

	if protectionMatch := reBDInfoProtection.FindStringSubmatch(text); len(protectionMatch) >= 2 {
		fingerprint.AACSRetained = strings.Contains(strings.ToUpper(protectionMatch[1]), "AACS")
	}

	fingerprint.LabelMatchesRelease = discLabelMatchesRelease(fingerprint.DiscLabel, releaseName)
	fingerprint.LabelToolGenerated = reBDInfoToolGeneratedLabel.MatchString(fingerprint.DiscLabel)

	if fingerprint.PlaylistCount == 1 && fingerprint.MainPlaylistBytes > 0 {
		overhead := fingerprint.DiscSizeBytes - fingerprint.MainPlaylistBytes
		fingerprint.NoMenuPayload = overhead >= 0 && overhead < discDIYNoMenuPayloadToleranceBytes
	}

	return fingerprint
}

// IsDIYDiscByBDInfo 依据 BDInfo 碟指纹推断种子本体是否为 DIY（自制/精简）原盘。
// 参数/返回：mediaText 为媒体文本；releaseName 为发布名/种子名；返回是否 DIY 与命中的证据说明（供日志追溯）。
// 失败场景：文本非 BDInfo、无任何指纹命中时返回 false 与空说明。
// 副作用：无。
//
// 判据（任一条成立即判 DIY）：
//  1. 卷标即发布名 —— 商业碟不会把 volume label 设成 `片名.年份.媒介.编码.音轨-发布组` 这种场景式发布名；
//  2. 卷标为工具生成名（如 `iso-4207589629`）；
//  3. 只剩正片 —— 单 playlist 且碟大小减去该 playlist 后不足 1 MiB（无菜单/BD-J/花絮/多版本），
//     同时仍保留 AACS 目录，说明是「拿商业碟删减后重新打包」。
//
// ⚠️ 本函数只看「碟长什么样」，不回答「种子本体是不是碟」。调用方必须先有物理证据
// （抓取期文件列表含 .iso/BDMV，或刷新期确认本体跑出了 BDInfo），
// 否则会把「源站详情页贴源盘 BDInfo」的 Remux 种子整批误标成 DIY。
func IsDIYDiscByBDInfo(mediaText, releaseName string) (bool, string) {
	fingerprint := ParseBDInfoDiscFingerprint(mediaText, releaseName)
	if !fingerprint.HasDiscInfo {
		return false, ""
	}

	reasons := make([]string, 0, 3)
	if fingerprint.LabelMatchesRelease {
		reasons = append(reasons, "卷标等于发布名:"+fingerprint.DiscLabel)
	}
	if fingerprint.LabelToolGenerated {
		reasons = append(reasons, "卷标为工具生成名:"+fingerprint.DiscLabel)
	}
	if fingerprint.NoMenuPayload && fingerprint.AACSRetained {
		reasons = append(reasons, "仅一条playlist且无菜单载荷:剩余"+
			strconv.FormatInt(fingerprint.DiscSizeBytes-fingerprint.MainPlaylistBytes, 10)+"B")
	}
	if len(reasons) == 0 {
		return false, ""
	}
	return true, strings.Join(reasons, "；")
}

// IsBlurayDiscMedium 判断标准媒介是否属于蓝光原盘档（含 DIY 子档）。
// 说明：媒介落到原盘档意味着碟结构已被物理证据确认过（抓取期文件列表或刷新期本体 BDInfo），
// 因此可作为 DIY 推断的前置门禁；仍留在 medium.remux / medium.encode 的种子不会被推断成 DIY。
func IsBlurayDiscMedium(medium string) bool {
	switch strings.ToLower(strings.TrimSpace(medium)) {
	case "medium.bluray", "medium.uhd_bluray", "medium.bluray_diy", "medium.uhd_diy":
		return true
	default:
		return false
	}
}

// discLabelMatchesRelease 判断卷标与发布名是否指向同一发布。
// 归一化只保留字母/数字/汉字并转小写。
// 两级判据：
//   - 完全相同 —— 商业碟不会把 volume label 设成场景式发布名；
//   - 互为包含 —— 额外要求卷标自身带发布参数（1080p/2160p 这类分辨率标记）且较短一方不少于
//     12 个字符。否则「卷标 `The Matrix 1999` 恰好是发布名 `The Matrix 1999 BluRay …` 的前缀」
//     这种商业碟情形会被误判成 DIY。
func discLabelMatchesRelease(discLabel, releaseName string) bool {
	label := normalizeDiscLabelForCompare(discLabel)
	release := normalizeDiscLabelForCompare(releaseName)
	if label == "" || release == "" {
		return false
	}
	if label == release {
		return true
	}
	if shorterRuneLen(label, release) < discDIYLabelCompareMinRunes {
		return false
	}
	if !reDiscLabelReleaseToken.MatchString(label) {
		return false
	}
	return strings.Contains(label, release) || strings.Contains(release, label)
}

// normalizeDiscLabelForCompare 归一化卷标/发布名：转小写并仅保留字母、数字与汉字。
func normalizeDiscLabelForCompare(text string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// shorterRuneLen 返回两个字符串中较短的字符数。
func shorterRuneLen(left, right string) int {
	leftLen := utf8.RuneCountInString(left)
	rightLen := utf8.RuneCountInString(right)
	if leftLen < rightLen {
		return leftLen
	}
	return rightLen
}

// parseBDInfoByteCount 解析 BDInfo 里的字节数（容忍千分位逗号与不间空格）。
func parseBDInfoByteCount(raw string) (int64, error) {
	cleaned := strings.NewReplacer(",", "", " ", "", "\u00a0", "", "\u202f", "").Replace(strings.TrimSpace(raw))
	return strconv.ParseInt(cleaned, 10, 64)
}
