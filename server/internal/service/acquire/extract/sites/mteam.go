package sites

import (
	"fmt"
	"strings"

	"github.com/pt-nexus/server/internal/service/mteamapi"
)

// ExtractMTeam 通过馒头（M-Team）的 JSON API 提取种子参数。
//
// 站点是纯 SPA：详情页 /detail/{id} 的 HTML 里只有站点骨架，没有任何可解析字段，
// 因此这里完全忽略 input.PageHTML，改为调 /api/torrent/detail（x-api-key 鉴权）读详情。
//
// 参数/返回：input.Passkey 填站点控制台「存取令牌」（x-api-key，与发种同一套鉴权，
// 已实测 code=0），网页 Cookie 与 32 位站点 Passkey 都无效；input.TorrentID 为数字种子 ID。
// 失败场景：缺少令牌、种子 ID 非法、站点拒绝或接口异常。
// 副作用：发起一次 HTTP 请求。
func ExtractMTeam(input Input, runtime Runtime) (SeedData, error) {
	token := mteamapi.ResolveToken([]mteamapi.Credential{
		{Source: "passkey", Value: input.Passkey},
		{Source: "cookie", Value: input.Cookie},
	})
	if token.Value == "" {
		return SeedData{}, fmt.Errorf("馒头缺少 API 令牌：请在站点配置的 Passkey 栏填写控制台生成的存取令牌（网页 Cookie 与 32 位站点 Passkey 无效）")
	}

	torrentID := mteamapi.ExtractTorrentID(input.TorrentID)
	if torrentID == "" {
		return SeedData{}, fmt.Errorf("馒头种子 ID 无效：%s", strings.TrimSpace(input.TorrentID))
	}

	apiBase := mteamapi.APIBaseFor(input.BaseURL)
	detail, err := mteamapi.Detail(apiBase, token.Value, torrentID, 0)
	if err != nil {
		return SeedData{}, err
	}

	return buildMTeamSeedData(detail, runtime), nil
}

// buildMTeamSeedData 把站点种子详情映射为统一提取结构。
//
// 口径说明：
//   - name 是站点规范化的英文标题（含分辨率/媒介/编码），直接作为 Title；
//   - smallDescr 是「译名 / 别名 / 字幕语言」这类副标题文本，作为 Subtitle；
//   - descr 是完整 BBCode（海报 + 影片参数 + 简介 + 截图），交给公共分段器拆出海报/正文/截图/声明；
//   - mediainfo 字段已由站点单独给出，直接作为 MediaInfo；
//   - category/standard/videoCodec/audioCodec/processing 是站点侧数字 ID，
//     反查 configs/mteam.yaml 的字典得到标准化键（详见 mteamIDReverse）。
func buildMTeamSeedData(detail mteamapi.DetailData, runtime Runtime) SeedData {
	data := SeedData{
		Title:    strings.TrimSpace(detail.Name),
		Subtitle: strings.TrimSpace(detail.SmallDescr),
		Team:     strings.TrimSpace(detail.Team),
	}

	// descr 是纯 BBCode（无 HTML），公共分段器能识别海报、截图、声明与影片参数。
	if runtime.ExtractDescriptionSections != nil {
		statement, poster, body, screens, _, tags, removed := runtime.ExtractDescriptionSections(
			"", strings.TrimSpace(detail.Descr), "",
		)
		data.Intro.Statement = statement
		data.Intro.Poster = poster
		data.Intro.Body = body
		data.Intro.Screenshots = screens
		data.Intro.RemovedARDTUDeclarations = removed
		if len(tags) > 0 {
			data.Tags = append(data.Tags, tags...)
		}
	} else {
		data.Intro.Body = strings.TrimSpace(detail.Descr)
	}

	// 海报兜底：descr 里没解析出海报时，用站点 imageList 首图。
	if strings.TrimSpace(data.Intro.Poster) == "" && len(detail.ImageList) > 0 {
		if poster := strings.TrimSpace(detail.ImageList[0]); poster != "" {
			data.Intro.Poster = "[img]" + poster + "[/img]"
		}
	}

	data.MediaInfo = strings.TrimSpace(detail.MediaInfo)

	// 外链直接取自站点结构化字段。
	data.IMDbLink = strings.TrimSpace(detail.IMDb)
	data.DoubanLink = strings.TrimSpace(detail.Douban)
	if strings.TrimSpace(detail.Bangumi) != "" {
		// bangumi 字段可能是裸数字 ID，也可能已是链接；仅当是链接时写入 TMDbLink 兜底位，
		// 避免与标准外链语义冲突。这里暂不写入外链，留给动漫标签流程另行处理。
		_ = detail.Bangumi
	}

	// 站点侧数字 ID → 标准化键。
	data.Type = mteamCategoryKey(detail.Category)
	data.Resolution = mteamStandardValue(detail.Standard)
	data.VideoCodec = mteamVideoCodecValue(detail.VideoCodec)
	data.AudioCodec = mteamAudioCodecValue(detail.AudioCodec)
	data.Medium = mteamMediumValue(detail.Source)
	data.Source = mteamProcessingValue(detail.Processing)

	// 标签：labelsNew 是站点标签数组（中字/4k/hdr10/DoVi 等），合并进 Tags 并去重。
	data.Tags = appendUniqueTags(data.Tags, detail.LabelsNew...)

	return data
}

// mteamCategoryKey 把站点分类 ID 反查为标准化类型键。
// 站点分类是「类型 + 介质」二维划分，这里按大类回落（与 mteam.yaml 的 category 映射对应）。
func mteamCategoryKey(categoryID string) string {
	switch strings.TrimSpace(categoryID) {
	case "401", "419", "420", "421", "439":
		// 电影：SD / HD / DVDiSo / BluRay / Remux
		return "category.movie"
	case "402", "403", "435", "438":
		// 影剧/综艺：SD / HD / DVDiSo / BluRay
		return "category.tv_series"
	case "405", "453":
		// 动画 / 动画 BluRay
		return "category.animation"
	case "404":
		return "category.documentary"
	case "434", "406", "407", "423", "422", "427", "442", "451":
		return ""
	default:
		return ""
	}
}

// mteamStandardValue 把站点分辨率 ID 反查为标准化分辨率值（"2160p" 等）。
func mteamStandardValue(standardID string) string {
	switch strings.TrimSpace(standardID) {
	case "1":
		return "1080p"
	case "2":
		return "1080i"
	case "3":
		return "720p"
	case "5":
		return "sd"
	case "6":
		return "2160p"
	case "7":
		return "4320p"
	default:
		return ""
	}
}

// mteamVideoCodecValue 把站点视频编码 ID 反查为标准化编码键。
func mteamVideoCodecValue(codecID string) string {
	switch strings.TrimSpace(codecID) {
	case "1":
		return "video.h264"
	case "16":
		return "video.h265"
	case "2":
		return "video.vc1"
	case "4":
		return "video.mpeg2"
	case "3":
		return "video.xvid"
	case "19":
		return "video.av1"
	case "21":
		return "video.vp9"
	default:
		return ""
	}
}

// mteamAudioCodecValue 把站点音频编码 ID 反查为标准化编码键。
func mteamAudioCodecValue(codecID string) string {
	switch strings.TrimSpace(codecID) {
	case "6":
		return "audio.aac"
	case "8":
		return "audio.ac3"
	case "3":
		return "audio.dts"
	case "11":
		return "audio.dts_hd_ma"
	case "12":
		return "audio.ddp"
	case "13":
		return "audio.ddp_atmos"
	case "9":
		return "audio.truehd"
	case "10":
		return "audio.truehd_atmos"
	case "14":
		return "audio.lpcm"
	case "15":
		return "audio.wav"
	case "1":
		return "audio.flac"
	case "2":
		return "audio.ape"
	case "4":
		return "audio.mp3"
	case "5":
		return "audio.ogg"
	default:
		return ""
	}
}

// mteamMediumValue 把站点 source ID 反查为标准化媒介键。
// 注意：馒头的 source 是「片源类型」（等价其它站的媒介），不是产地。
func mteamMediumValue(sourceID string) string {
	switch strings.TrimSpace(sourceID) {
	case "1":
		return "medium.bluray"
	case "4":
		return "medium.remux"
	case "8":
		return "medium.webdl"
	case "5":
		return "medium.hdtv"
	case "3":
		return "medium.dvd"
	case "10":
		return "medium.cd"
	case "6":
		return "medium.other"
	default:
		return ""
	}
}

// mteamProcessingValue 把站点地区 ID 反查为标准化产地键。
func mteamProcessingValue(processingID string) string {
	switch strings.TrimSpace(processingID) {
	case "1":
		return "source.china"
	case "2":
		return "source.western"
	case "3":
		return "source.hongkong_taiwan"
	case "4":
		return "source.japan"
	case "5":
		return "source.korea"
	default:
		return ""
	}
}
