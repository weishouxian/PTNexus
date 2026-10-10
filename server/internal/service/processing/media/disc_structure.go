package media

import "strings"

// discStructureMarkerNames BDMV 目录独有的标志性文件名（小写比较）。
var discStructureMarkerNames = map[string]struct{}{
	"index.bdmv":       {},
	"movieobject.bdmv": {},
}

// discStructureMarkerExts BDMV 目录独有的扩展名（小写比较）。
var discStructureMarkerExts = map[string]struct{}{
	".mpls": {},
	".clpi": {},
}

// IsDiscStructureFileNames 判断种子文件列表是否呈现蓝光原盘结构（ISO 整盘镜像或 BDMV 目录）。
// 参数/返回：fileNames 为种子内文件名（可含路径，只取路径末段参与判断）；返回是否为碟结构。
// 失败场景：空列表或全为空串时返回 false。
// 副作用：无。
// 判定口径：
//   - 出现 .iso 文件：视为整盘镜像（ISO 单文件发布形态）。
//   - 出现 BDMV 标志文件（index.bdmv / MovieObject.bdmv）且同时存在 .mpls / .clpi：
//     普通 Remux/Encode 单文件发布不会带这些扩展名，故此判定不会误伤 mkv/ts 种。
//
// 注意：本函数只回答「内容是不是碟结构」，不区分蓝光与 DVD（DVD 原盘同样以 ISO 形态发布），
// 碟规格的收敛由 OverrideMediumByDiscStructure 负责。
func IsDiscStructureFileNames(fileNames []string) bool {
	hasBDMVMarker := false
	hasDiscPayload := false

	for _, raw := range fileNames {
		name := normalizeTorrentFileBaseName(raw)
		if name == "" {
			continue
		}

		ext := torrentFileExt(name)
		if ext == ".iso" {
			return true
		}
		if _, ok := discStructureMarkerExts[ext]; ok {
			hasDiscPayload = true
			continue
		}
		if _, ok := discStructureMarkerNames[strings.TrimSpace(name)]; ok {
			hasBDMVMarker = true
		}
	}

	return hasBDMVMarker && hasDiscPayload
}

// OverrideMediumByDiscStructure 在「种子文件列表」证实本体是蓝光碟结构时，把媒介收敛回蓝光原盘档。
// 参数/返回：currentMedium 为当前标准媒介键；resolution 为标准分辨率键（resolution.r1080p 等）；fileNames 为种子内文件名；返回纠偏后的媒介键。
// 失败场景：非碟结构、DVD 系媒介、分辨率缺失或无法归入蓝光档时原样返回 currentMedium。
// 副作用：无。
// 背景：标题写 “…BluRay.Remux…” 但种子本体发的是 ISO/BDMV 的转种很常见，标题侧推断会把媒介定成
// medium.remux，发布到站点就会选错分类（如馒头 movie_remux 439 而不是原盘 movie_bluray 421）。
// 文件列表是物理事实，优先于标题声明。
//
// 说明：本函数只负责「文件列表 → 是否碟结构」的判定，真正的收敛口径在
// ConvergeMediumByConfirmedDiscStructure，供拿不到文件列表但有其他物理证据的链路复用。
func OverrideMediumByDiscStructure(currentMedium, resolution string, fileNames []string) string {
	medium := strings.TrimSpace(currentMedium)
	if !IsDiscStructureFileNames(fileNames) {
		return medium
	}
	return ConvergeMediumByConfirmedDiscStructure(medium, resolution)
}

// ConvergeMediumByConfirmedDiscStructure 在碟结构已被任一物理信号确认时，把媒介收敛回蓝光原盘档。
// 参数/返回：currentMedium 为当前标准媒介键；resolution 为标准分辨率键；返回纠偏后的媒介键。
// 失败场景：DVD 系媒介、分辨率缺失或无法归入蓝光档时原样返回 currentMedium。
// 副作用：无。
// 调用方职责：必须自行确认「本体确实是碟结构」后再调用（如抓取链路的文件列表、刷新链路对本体成功跑出 BDInfo）。
// 绝不可凭「源站详情页贴的 BDInfo 文本」调用——那描述的是源盘，不代表种子本体。
//
// 收口语义：只做「非原盘档 → 原盘档」的单向覆盖，已是原盘档时返回原值（幂等）。
func ConvergeMediumByConfirmedDiscStructure(currentMedium, resolution string) string {
	medium := strings.TrimSpace(currentMedium)

	// DVD 原盘同样以 ISO 形态发布，不能并入蓝光原盘档。
	if strings.Contains(strings.ToUpper(medium), "DVD") {
		return medium
	}

	if strings.Contains(strings.ToUpper(medium), "UHD") ||
		resolution == "resolution.r2160p" || resolution == "resolution.r8k" {
		return "medium.uhd_bluray"
	}

	switch resolution {
	case "resolution.r1080p", "resolution.r1080i", "resolution.r720p":
		return "medium.bluray"
	default:
		// 分辨率缺失或 SD 档：可能是不带蓝光标记的 DVD 原盘 ISO，保持原值不做收敛。
		return medium
	}
}

// normalizeTorrentFileBaseName 取种内文件名的路径末段并转小写。
// 说明：抓取链路的 TorrentFileNames 多数已是纯文件名，此处兼容带目录的形态。
func normalizeTorrentFileBaseName(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "\\", "/")
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.ToLower(strings.TrimSpace(name))
}

// torrentFileExt 返回小写扩展名（含点），无扩展名时返回空串。
func torrentFileExt(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx <= 0 || idx == len(name)-1 {
		return ""
	}
	return name[idx:]
}
