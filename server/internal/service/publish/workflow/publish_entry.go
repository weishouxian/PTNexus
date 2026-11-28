package workflow

import (
	"fmt"
	neturl "net/url"
	"strings"
	"unicode"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/platform/logx"
	acquirefetch "github.com/pt-nexus/server/internal/service/acquire/fetch"
	processingtagging "github.com/pt-nexus/server/internal/service/processing/tagging"
	publishchecker "github.com/pt-nexus/server/internal/service/publish/checker"
	publishdownloader "github.com/pt-nexus/server/internal/service/publish/downloader"
	publishpublisher "github.com/pt-nexus/server/internal/service/publish/publisher"
	publishuploader "github.com/pt-nexus/server/internal/service/publish/uploader"
)

const publishDetailsLogModule = "发布-详情回写"

// PublishExecutionInput 定义单站发布执行输入。
type PublishExecutionInput struct {
	TargetSite string
	TargetInfo map[string]any
	UploadData map[string]any

	SourceSiteNickname string
	SourceTorrentHash  string
	SourceTorrentName  string

	TorrentPath string
	Payload     map[string]any

	FallbackSavePath     string
	FallbackDownloaderID string
	DefaultDownloaderID  string
	RootConfig           map[string]any
}

// PublishExecutionDeps 定义单站发布执行依赖。
type PublishExecutionDeps struct {
	AddToDownloader                   func(payload map[string]any) (map[string]any, int)
	FindSiteNicknameByGroup           func(releaseGroup string) (string, error)
	IsTransferForbiddenByOfficialSite func(officialSite, targetSite string) (bool, string, error)
	UpdateTorrentDetails              func(input PublishTorrentDetailsUpdateInput) (int64, error)
}

// PublishTorrentDetailsUpdateInput 定义发种成功后回写 torrents.details 所需的最小字段。
type PublishTorrentDetailsUpdateInput struct {
	Hashes       []string
	Name         string
	DownloaderID string
	SiteNickname string
	DetailsURL   string
}

// ExecutePublish 执行单站发布主流程（上传、URL标准化、自动加下载器）。
// 参数/返回：输入需包含目标站配置、发布参数和种子路径；返回与原迁移接口一致的发布结果结构。
// 失败场景：标签限制命中、上传失败、自动加下载器参数不足时返回对应状态与提示。
// 副作用：会向目标站点发起上传请求，可选调用下载器添加任务，并在发布成功后回写 torrents.details。
func ExecutePublish(input PublishExecutionInput, deps PublishExecutionDeps) (map[string]any, int) {
	targetSite := strings.TrimSpace(input.TargetSite)
	targetInfo := input.TargetInfo
	// 发种方式（站点设置页 sites.publish_mode）：候选模式只提交候选申请、不提交种子文件。
	// 提前取值，供后面的发布前门禁与链接归一化共用（同一份目标站配置，避免两处判断口径不一致）。
	isOfferPublish := publishpublisher.IsOfferPublishMode(targetInfo)
	uploadData := input.UploadData
	payload := input.Payload
	if uploadData == nil {
		uploadData = map[string]any{}
	}
	if payload == nil {
		payload = map[string]any{}
	}

	targetNickname := resolveTargetSiteLabel(targetInfo, targetSite)
	officialSite := strings.TrimSpace(toStringAny(uploadData["official_site"], ""))
	if officialSite == "" {
		officialSite = strings.TrimSpace(toStringAny(payload["official_site"], ""))
	}
	if officialSite == "" {
		if standardized, ok := uploadData["standardized_params"].(map[string]any); ok {
			officialSite = strings.TrimSpace(toStringAny(standardized["official_site"], ""))
		}
	}
	if officialSite == "" && deps.FindSiteNicknameByGroup != nil {
		team := strings.TrimSpace(toStringAny(uploadData["team"], ""))
		if team == "" {
			if standardized, ok := uploadData["standardized_params"].(map[string]any); ok {
				team = strings.TrimSpace(toStringAny(standardized["team"], ""))
			}
		}
		if team != "" {
			if resolved, resolveErr := deps.FindSiteNicknameByGroup(team); resolveErr != nil {
				logx.Warnf("发布-禁转站点", "解析官种站失败 target=%s team=%s err=%v", targetNickname, team, resolveErr)
			} else {
				officialSite = strings.TrimSpace(resolved)
			}
		}
	}
	// 禁转限制约束的是「把种子转到该站」，候选申请不提交种子文件，因此对候选模式不适用。
	if officialSite != "" && !isOfferPublish && deps.IsTransferForbiddenByOfficialSite != nil {
		forbidden, matchedTarget, checkErr := deps.IsTransferForbiddenByOfficialSite(officialSite, targetNickname)
		if checkErr != nil {
			logx.Warnf("发布-禁转站点", "检查禁转站点失败 official_site=%s target=%s err=%v", officialSite, targetNickname, checkErr)
		} else if forbidden {
			if strings.TrimSpace(matchedTarget) == "" {
				matchedTarget = targetNickname
			}
			message := fmt.Sprintf("🚫 发布前禁转站点限制: 官种站 %s 禁止转种到 %s", officialSite, matchedTarget)
			logx.Infof("发布-禁转站点", "拦截发布 official_site=%s target=%s", officialSite, matchedTarget)
			return map[string]any{
				"success":       false,
				"logs":          message,
				"message":       message,
				"limit_reached": true,
				"pre_check":     true,
				"url":           nil,
			}, 200
		}
	}

	// 检查前端是否已确认跳过受限标签检查。
	// 注意：该旁路只对「禁转/限转」生效 —— 「分集」由站点 YAML 的 allow_episode_publish 裁决，
	// 不接受前端逐次放行，否则配置就形同虚设。
	skipRestrictedCheck := false
	if v, ok := uploadData["skip_restricted_check"]; ok {
		if b, isBool := v.(bool); isBool {
			skipRestrictedCheck = b
		}
	}

	// 用户在前端对 dupe 拦截结果点了「仍要发布」时的单次放行标记。
	// 只跳过 dupe 查重，不影响其它站点级硬性限制。
	skipDupeCheck := payloadBoolFlag(input.Payload, "skip_dupe_check") ||
		payloadBoolFlag(uploadData, "skip_dupe_check")

	// 禁转/限转/分集标签同样只约束正式发种：候选只是「申请上传」，由站点管理员裁决，不做前置拦截。
	if restricted := publishuploader.DetectRestrictedTags(uploadData); len(restricted) > 0 && !isOfferPublish {
		logx.Infof("发布-候选模式", "候选申请忽略受限标签检查 target=%s tags=%v", targetNickname, restricted)
		// 分集单独裁决：目标站点 YAML 写了 allow_episode_publish 才允许发布，默认拦截。
		episodeTags := make([]string, 0, 1)
		otherTags := make([]string, 0, len(restricted))
		for _, tag := range restricted {
			if processingtagging.IsEpisodeRestrictedTag(tag) {
				episodeTags = append(episodeTags, tag)
				continue
			}
			otherTags = append(otherTags, tag)
		}
		if len(episodeTags) > 0 {
			// 分集发布只由站点级开关裁决：
			//   sites_data.json 的 allow_episode_publish —— 目标站显式写 true 才允许；
			//   不配置 / false 一律拦截（fail-closed）。
			siteCode := strings.TrimSpace(toStringAny(targetInfo["site"], strings.ToLower(targetSite)))
			if !config.SiteAllowsEpisodePublish(siteCode) {
				message := "🚫 发布前标签限制: 检测到分集标签，目标站未开启分集发布"
				logx.Infof("发布-分集限制", "拦截发布 target=%s code=%s tags=%v 站点允许=false",
					targetNickname, siteCode, episodeTags)
				return map[string]any{
					"success":       false,
					"logs":          message,
					"message":       message,
					"limit_reached": true,
					"pre_check":     true,
					"url":           nil,
				}, 200
			}
			logx.Infof("发布-分集限制", "站点配置已开启分集发布，放行 target=%s code=%s", targetNickname, siteCode)
		}
		if len(otherTags) > 0 && !skipRestrictedCheck {
			message := fmt.Sprintf("🚫 发布前标签限制: 检测到禁转/限转标签 %v", otherTags)
			return map[string]any{
				"success": false,
				"logs":    message,
				// 前端对 pre_check 响应统一读 message（见 publishFlow 的 result.message.replace），
				// 缺字段会直接抛错，这里与分集分支保持一致。
				"message":       message,
				"limit_reached": true,
				"pre_check":     true,
				"url":           nil,
			}, 200
		}
	}

	resolvedSavePath, resolvedDownloaderID := publishdownloader.ResolveEffectiveTarget(
		payload,
		strings.TrimSpace(input.FallbackSavePath),
		strings.TrimSpace(input.FallbackDownloaderID),
		strings.TrimSpace(input.DefaultDownloaderID),
	)
	buildPreCheckFailure := func(reason string, detail ...string) (map[string]any, int) {
		trimmedReason := strings.TrimSpace(reason)
		if trimmedReason == "" {
			trimmedReason = "已触发限制"
		}
		message := fmt.Sprintf("🚫 发布前预检查触发限制: %s", trimmedReason)
		// 把站点级校验的判定过程（如 dupe 检索 URL、候选数）附在消息后，
		// 这样「为什么被拦」在发布日志里就能直接看到，不必去翻服务端日志。
		for _, item := range detail {
			trimmedDetail := strings.TrimSpace(item)
			if trimmedDetail != "" {
				message += "\n" + trimmedDetail
			}
		}
		payload := map[string]any{
			"success":       false,
			"logs":          message,
			"message":       message,
			"limit_reached": true,
			"pre_check":     true,
			"url":           nil,
		}
		return payload, 200
	}

	// 站点级校验可回传结构化信息（如 dupe 的查重地址与重复种子详情页），
	// 直接并进响应体，前端据此渲染富提示而不是只显示一段纯文本。
	buildPreCheckFailureWithMeta := func(err error, reason string, detail ...string) (map[string]any, int) {
		payload, status := buildPreCheckFailure(reason, detail...)
		mergePreCheckMeta(payload, err)
		return payload, status
	}
	if resolvedDownloaderID == "" {
		return buildPreCheckFailure("缺少有效 downloaderId，已停止发布")
	}

	// 标题字符集门禁：发布标题只接受英文，出现汉字/中文标点直接拦截。
	// 放在这里而不是各站点适配器里 —— 单站发布、批量转种、下载器队列与定时发种
	// 全部汇聚到 ExecutePublish，一处拦截即可覆盖所有入口；候选模式的标题同样要提交，
	// 一并按同一口径拦下，避免发布后被站点拒绝或判为垃圾种。
	if publishTitle := strings.TrimSpace(toStringAny(uploadData["title"], "")); publishTitle != "" {
		if hits := chineseTitleChars(publishTitle); len(hits) > 0 {
			reason := fmt.Sprintf("发布标题含中文字符「%s」，请改成纯英文标题后再发布", string(hits))
			logx.Infof("发布-标题字符集", "拦截发布 target=%s chars=%s", targetNickname, string(hits))
			return buildPreCheckFailure(reason)
		}
	}

	publishURL, directDownloadURL, logs, isExistingTorrent, uploadFormFields, publishErr := PublishTorrentToTarget(
		targetInfo,
		uploadData,
		strings.TrimSpace(input.TorrentPath),
		strings.TrimSpace(input.SourceSiteNickname),
		deps.FindSiteNicknameByGroup,
		input.RootConfig,
		skipDupeCheck,
	)
	if publishErr != nil {
		// 站点适配器在发起上传前的硬性拒绝（如北洋园不接收动漫、我堡禁止 Remux）属于确定性失败：
		// 站点上不会新增种子，且同样的参数重试必然同样失败，因此按「预检查限制」上报，
		// 让队列与定时发种调度器把它改判为「跳过」并立即处理下一个种子。
		if reason, detail, isPreCheck := publishpublisher.AsPreCheckErrorWithDetail(publishErr); isPreCheck {
			logx.Infof("发布-预检查限制", "站点级发布前校验拦截 target=%s reason=%s", targetNickname, reason)
			return buildPreCheckFailureWithMeta(publishErr, reason, detail)
		}
		failureLogs := strings.TrimSpace(logs)
		if failureLogs == "" {
			failureLogs = fmt.Sprintf("发布到 %s 失败: %v", targetNickname, publishErr)
		}
		return map[string]any{
			"success": false,
			"logs":    failureLogs,
			"url":     nil,
		}, 500
	}
	normalizedBaseURL := acquirefetch.NormalizeSiteBaseURL(toStringAny(targetInfo["base_url"], ""))
	// 候选（offer）模式的发布结果是「候选链接」，不是种子详情页：
	// 候选 id 与种子 id 不共享序列，既不能把 offers.php?id=N 改写成 details.php?id=N，
	// 也不能拿它拼 download/dl 直链，否则会指向别人的种子。
	if isOfferPublish {
		publishURL = publishuploader.NormalizePublishURL(normalizedBaseURL, publishURL)
	} else {
		publishURL = publishuploader.NormalizePublishURLWithOfferSupport(normalizedBaseURL, publishURL)
	}
	directDownloadURL = strings.TrimSpace(directDownloadURL)
	if directDownloadURL == "" && !isOfferPublish {
		directDownloadURL = publishuploader.BuildDirectDownloadURLForPublished(
			toStringAny(targetInfo["base_url"], ""),
			toStringAny(targetInfo["passkey"], ""),
			toStringAny(targetInfo["site"], ""),
			publishURL,
		)
	}
	trimmedLogs := strings.TrimSpace(logs)
	if trimmedLogs == "" {
		trimmedLogs = fmt.Sprintf("发布到 %s 成功", targetNickname)
	}
	if publishURL != "" && !strings.Contains(trimmedLogs, publishURL) {
		linkLabel := "详情页链接"
		if isOfferPublish {
			linkLabel = "候选链接（等待站点审核）"
		}
		trimmedLogs += "\n" + fmt.Sprintf("%s: %s", linkLabel, publishURL)
	}
	if directDownloadURL != "" && !strings.Contains(trimmedLogs, directDownloadURL) {
		trimmedLogs += "\n" + fmt.Sprintf("直链下载: %s", directDownloadURL)
	}

	autoEditExecuted := false
	autoEditResult := map[string]any(nil)
	autoUpdateExistingTorrent := false
	if _, exists := payload["auto_update_existing_torrent"]; exists {
		autoUpdateExistingTorrent = boolFromAny(payload["auto_update_existing_torrent"])
	} else if _, exists := payload["autoUpdateExistingTorrent"]; exists {
		autoUpdateExistingTorrent = boolFromAny(payload["autoUpdateExistingTorrent"])
	}
	if isExistingTorrent && autoUpdateExistingTorrent {
		torrentID := strings.TrimSpace(extractTorrentIDFromPublishURL(publishURL))
		if torrentID == "" {
			autoEditResult = map[string]any{
				"success": false,
				"skipped": true,
				"message": "跳过：仅支持 NexusPHP details.php?id=... 形式的详情页链接",
			}
		} else if strings.TrimSpace(normalizedBaseURL) == "" {
			autoEditResult = map[string]any{
				"success":    false,
				"skipped":    true,
				"torrent_id": torrentID,
				"message":    "跳过：目标站点缺少 base_url",
			}
		} else {
			cookie := strings.TrimSpace(toStringAny(targetInfo["cookie"], ""))
			if cookie == "" {
				autoEditResult = map[string]any{
					"success":    false,
					"skipped":    true,
					"torrent_id": torrentID,
					"message":    "跳过：目标站点缺少 cookie",
				}
			} else {
				trimmedLogs += "\n--- [自动更新已存在种子信息] ---"

				detailHTML, fetchDetail, fetchErr := publishuploader.TryFetchDetailHTML(publishURL, cookie)
				if strings.TrimSpace(fetchDetail) != "" {
					trimmedLogs += "\n" + fetchDetail
				}
				if fetchErr != nil {
					autoEditResult = map[string]any{
						"success":    false,
						"skipped":    true,
						"torrent_id": torrentID,
						"message":    fmt.Sprintf("跳过：获取详情页失败: %v", fetchErr),
					}
				} else {
					if permissionLog := strings.TrimSpace(publishchecker.BuildExistingTorrentEditPermissionLog(detailHTML, torrentID)); permissionLog != "" {
						trimmedLogs += "\n" + permissionLog
					}
					canEdit, denyReason := publishchecker.CheckExistingTorrentEditPermission(detailHTML, torrentID)
					if !canEdit {
						reason := strings.TrimSpace(denyReason)
						if reason == "" {
							reason = "详情页校验未通过"
						}
						autoEditResult = map[string]any{
							"success":    false,
							"skipped":    true,
							"torrent_id": torrentID,
							"message":    fmt.Sprintf("跳过：%s", reason),
						}
					} else {
						editFields := copyStringMap(uploadFormFields)
						editFields["id"] = torrentID
						takeEditURL := strings.TrimRight(normalizedBaseURL, "/") + "/takeedit.php"
						referer := strings.TrimRight(normalizedBaseURL, "/") + "/edit.php?id=" + torrentID

						editSuccess, editDetail, editErr := publishuploader.TryEditTorrent(takeEditURL, cookie, referer, editFields)
						autoEditExecuted = true
						if strings.TrimSpace(editDetail) != "" {
							trimmedLogs += "\n" + editDetail
						}

						if editErr != nil {
							autoEditResult = map[string]any{
								"success":    false,
								"skipped":    false,
								"torrent_id": torrentID,
								"message":    fmt.Sprintf("编辑失败: %v", editErr),
							}
						} else if editSuccess {
							autoEditResult = map[string]any{
								"success":    true,
								"skipped":    false,
								"torrent_id": torrentID,
								"message":    "编辑成功",
							}
						} else {
							autoEditResult = map[string]any{
								"success":    false,
								"skipped":    false,
								"torrent_id": torrentID,
								"message":    "编辑请求已提交，但未能确认是否成功（请自行打开详情页确认）",
							}
						}
					}
				}
			}
		}
	}

	autoAdd := boolFromAny(payload["autoAddToDownloader"]) || boolFromAny(payload["auto_add_to_downloader"]) || boolFromAny(payload["auto_add"])
	autoAddExistingToDownloader := true
	if _, exists := payload["auto_add_existing_to_downloader"]; exists {
		autoAddExistingToDownloader = boolFromAny(payload["auto_add_existing_to_downloader"])
	} else if _, exists := payload["autoAddExistingToDownloader"]; exists {
		autoAddExistingToDownloader = boolFromAny(payload["autoAddExistingToDownloader"])
	}
	useDefaultDownloader := boolFromAny(payload["useDefaultDownloader"]) || boolFromAny(payload["use_default_downloader"])
	autoAddResult := map[string]any{"success": false, "message": "未启用自动添加到下载器"}
	// 只收录「发布后实际加入下载器的目标种子 hash」。
	// 注意：不要默认把源站 hash 塞进来——否则下面的 torrents.details 回写会按源站 hash
	// 命中源站那一行，把它的详情链接覆盖成目标站地址（即“源站种子地址变了”的根因）。
	publishedHashCandidates := []string{}
	if isOfferPublish {
		// 候选申请不产生种子文件，没有可加进下载器的东西，直接跳过这一次自动添加。
		// success 必须为 true：否则前端会把「发布成功但添加失败」当失败提示出来，
		// 而实际候选已提交成功（站点返回 302），用户看到的只会是误导性的红字。
		autoAddResult = map[string]any{
			"success":    true,
			"skipped":    true,
			"offer_mode": true,
			"message":    "候选申请已提交（等待站点审核），候选不产生种子文件，已跳过自动添加下载器",
		}
	} else if autoAdd {
		if isExistingTorrent && !autoAddExistingToDownloader {
			autoAddResult = map[string]any{"success": false, "message": "检测到目标站点种子已存在，按设置跳过自动添加"}
		} else if resolvedDownloaderID == "" && !useDefaultDownloader {
			autoAddResult = map[string]any{"success": false, "message": "自动添加已跳过：缺少 downloaderId"}
		} else if deps.AddToDownloader == nil {
			autoAddResult = map[string]any{"success": false, "message": "自动添加已跳过：服务未注册 AddToDownloader 回调"}
		} else {
			downloadURLForDownloader := publishURL
			if strings.TrimSpace(directDownloadURL) != "" {
				downloadURLForDownloader = strings.TrimSpace(directDownloadURL)
			}
			addPayload := map[string]any{
				"url":          downloadURLForDownloader,
				"savePath":     resolvedSavePath,
				"targetSite":   targetSite,
				"siteNickname": targetNickname,
				"publishURL":   publishURL,
			}
			if resolvedDownloaderID != "" {
				addPayload["downloaderId"] = resolvedDownloaderID
			}
			if raw, exists := payload["useDefaultDownloader"]; exists {
				addPayload["useDefaultDownloader"] = raw
			} else if raw, exists := payload["use_default_downloader"]; exists {
				addPayload["useDefaultDownloader"] = raw
			}
			addResult, _ := deps.AddToDownloader(addPayload)
			autoAddResult = addResult
			if hash := strings.TrimSpace(toStringAny(addResult["hash"], "")); hash != "" {
				publishedHashCandidates = appendUniqueStrings(publishedHashCandidates, hash)
			}
		}
	}

	if strings.TrimSpace(publishURL) != "" && deps.UpdateTorrentDetails != nil {
		updateInput := PublishTorrentDetailsUpdateInput{
			Hashes:       publishedHashCandidates,
			Name:         strings.TrimSpace(firstNonEmpty(input.SourceTorrentName, toStringAny(payload["name"], ""), toStringAny(uploadData["name"], ""))),
			DownloaderID: resolvedDownloaderID,
			SiteNickname: targetNickname,
			DetailsURL:   publishURL,
		}
		if affected, updateErr := deps.UpdateTorrentDetails(updateInput); updateErr != nil {
			logx.Warnf(publishDetailsLogModule, "回写种子详情地址失败 downloader_id=%s site=%s hashes=%v err=%v", resolvedDownloaderID, targetNickname, publishedHashCandidates, updateErr)
			trimmedLogs += "\n" + fmt.Sprintf("详情地址回写失败: %v", updateErr)
		} else if affected > 0 {
			logx.Infof(publishDetailsLogModule, "已回写种子详情地址 downloader_id=%s site=%s affected=%d hashes=%v", resolvedDownloaderID, targetNickname, affected, publishedHashCandidates)
			trimmedLogs += "\n" + fmt.Sprintf("已回写种子详情地址: %s", publishURL)
		} else {
			logx.Infof(publishDetailsLogModule, "回写种子详情地址未命中 torrents downloader_id=%s site=%s hashes=%v name=%s", resolvedDownloaderID, targetNickname, publishedHashCandidates, updateInput.Name)
		}
	}

	return map[string]any{
		"success":             true,
		"logs":                trimmedLogs,
		"url":                 publishURL,
		"direct_download_url": directDownloadURL,
		"site_identifier":     strings.TrimSpace(toStringAny(targetInfo["site"], strings.ToLower(targetSite))),
		"auto_add_result":     autoAddResult,
		"auto_add_executed":   autoAdd,
		"auto_edit_executed":  autoEditExecuted,
		"auto_edit_result":    autoEditResult,
		"is_existing_torrent": isExistingTorrent,
		// offer_mode=true 表示本次是「候选申请」而不是正式发种，前端据此换文案与状态，
		// 不再把它当作「发布成功但加下载器失败」。
		"offer_mode": isOfferPublish,
	}, 200
}

// resolveTargetSiteLabel 解析发布目标站点的显示名。
// 参数/返回：targetInfo 为 sites 表记录，targetSite 为请求中的目标站标识；返回用于日志和下载器标签的站点名。
// 失败场景：所有候选字段为空时返回空字符串。
// 副作用：无。
func resolveTargetSiteLabel(targetInfo map[string]any, targetSite string) string {
	for _, value := range []string{
		toStringAny(targetInfo["nickname"], ""),
		toStringAny(targetInfo["site_name"], ""),
		toStringAny(targetInfo["name"], ""),
		strings.TrimSpace(targetSite),
		toStringAny(targetInfo["site"], ""),
	} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func extractTorrentIDFromPublishURL(publishURL string) string {
	trimmed := strings.TrimSpace(publishURL)
	if trimmed == "" {
		return ""
	}

	parsed, err := neturl.Parse(trimmed)
	if err != nil {
		return ""
	}

	query := parsed.Query()
	if id := strings.TrimSpace(query.Get("id")); id != "" {
		return id
	}
	return strings.TrimSpace(query.Get("torrent_id"))
}

func copyStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func appendUniqueStrings(values []string, candidate string) []string {
	trimmed := strings.ToLower(strings.TrimSpace(candidate))
	if trimmed == "" {
		return values
	}
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == trimmed {
			return values
		}
	}
	return append(values, trimmed)
}

func boolFromAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	case string:
		lower := strings.ToLower(strings.TrimSpace(typed))
		return lower == "1" || lower == "true" || lower == "yes"
	default:
		return false
	}
}

// mergePreCheckMeta 把站点级发布前校验的结构化附加信息并进响应体。
// 参数/返回：payload 为待返回给前端的响应体；err 为站点适配器返回的错误。
// 说明：仅在错误链中存在 *PreCheckError 且其 Meta 非空时写入；
// 其余情况保持响应体不变，不影响既有站点的拦截行为。
// 副作用：就地修改 payload。
func mergePreCheckMeta(payload map[string]any, err error) {
	if payload == nil || err == nil {
		return
	}
	preCheck, ok := publishpublisher.PreCheckErrorFrom(err)
	if !ok || len(preCheck.Meta) == 0 {
		return
	}
	for key, value := range preCheck.Meta {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		payload[trimmedKey] = value
	}
}

// payloadBoolFlag 从请求体/发布参数中读取布尔开关。
// 参数/返回：source 为待读取的 map；key 为字段名；返回该字段是否为真。
// 说明：兼容 bool 与常见字符串写法（前端一般传 bool，但配置类字段可能来自表单文本）。
// 副作用：无。
func payloadBoolFlag(source map[string]any, key string) bool {
	if source == nil || strings.TrimSpace(key) == "" {
		return false
	}
	value, ok := source[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes":
			return true
		}
		return false
	default:
		return false
	}
}

// chineseTitleChars 返回发布标题中出现的中文字符（去重，最多 8 个）。
//
// 口径：Unicode 汉字（含扩展 A 与兼容区）+ 常见中文标点，
// 与前端 webui/src/utils/titleCharset.ts 保持一致，避免前后端判定不同步。
// 参数：title 为待发布的标题；返回命中的字符，空切片表示标题可发布。
// 副作用：无。
func chineseTitleChars(title string) []rune {
	if strings.TrimSpace(title) == "" {
		return nil
	}
	hits := make([]rune, 0, 8)
	seen := make(map[rune]struct{}, 8)
	for _, r := range title {
		if !isChineseTitleRune(r) {
			continue
		}
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		hits = append(hits, r)
		if len(hits) >= 8 {
			break
		}
	}
	return hits
}

// isChineseTitleRune 判断单个字符是否属于「中文标题禁用字符」。
// 参数：r 为待判断字符。
// 返回：汉字或中文标点返回 true。
// 副作用：无。
func isChineseTitleRune(r rune) bool {
	switch r {
	case '\u3001', '\u3002', '\u30FB', // 、。・
		'\uFF01', '\uFF08', '\uFF09', '\uFF0C', '\uFF1A', '\uFF1B', '\uFF1F', // ！（），：；？
		'\u2014', '\u2026', '\u00B7': // —…·
		return true
	}
	switch {
	case r >= 0x3008 && r <= 0x3011: // 〈〉《》「」『』【】
		return true
	case r >= 0x3014 && r <= 0x3015: // 〔〕
		return true
	case r >= 0x301D && r <= 0x301F: // 〝〞〟
		return true
	}
	return unicode.Is(unicode.Han, r)
}
