package media

import (
	"regexp"
	"strings"
)

// audioTitleFamilyRule 描述一个音频编码「家族」在标题/组件文本里的识别与替换规则。
// 检测顺序 = 从具体到一般（TrueHD → DTS:X → DTS-HD MA → … → 裸 DTS），
// 避免裸 DTS 抢先命中 DTS-HD MA、AC-3 抢先命中 E-AC-3。
// Go RE2 不支持前瞻：detect/replace 统一用「前置非字母数字边界捕获组 + 尾部粘连声道捕获组」表达。
type audioTitleFamilyRule struct {
	standard   string         // 家族主标准键
	atmos      string         // Atmos/JOC 细分标准键（可为空）
	detect     *regexp.Regexp // 家族检测（含前置边界捕获组）
	replace    *regexp.Regexp // 替换（$1=前置边界，$2=粘连声道数字）
	token      string         // 展示 token
	tokenAtmos string         // Atmos 展示 token（可为空）
}

var audioTitleFamilyRules = []audioTitleFamilyRule{
	{standard: "audio.truehd", atmos: "audio.truehd_atmos",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])True[-\s.]?HD\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])True[-\s.]?HD(\d*)`),
		token:   "TrueHD", tokenAtmos: "TrueHD Atmos"},
	{standard: "audio.dtsx",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])DTS[:\-\s.]?X\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])DTS[:\-\s.]?X(\d*)`),
		token:   "DTS:X"},
	{standard: "audio.dts_hd_ma",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])DTS[-\s.]?HD[-\s.]?MA\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])DTS[-\s.]?HD[-\s.]?MA(\d*)`),
		token:   "DTS-HD MA"},
	{standard: "audio.dts_hd_hr",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])DTS[-\s.]?HD[-\s.]?HR\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])DTS[-\s.]?HD[-\s.]?HR(\d*)`),
		token:   "DTS-HD HR"},
	{standard: "audio.ddp", atmos: "audio.ddp_atmos",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])(?:E[-\s.]?AC[-\s.]?3|EAC3|DD\s*[\+＋]|DDP)`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])(?:E[-\s.]?AC[-\s.]?3|EAC3|DD\s*[\+＋]|DDP)(\d*)`),
		token:   "DDP", tokenAtmos: "DDP Atmos"},
	{standard: "audio.ac3",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])AC[-\s.]?3\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])AC[-\s.]?3(\d*)`),
		token:   "DD"},
	{standard: "audio.flac",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])FLAC\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])FLAC(\d*)`),
		token:   "FLAC"},
	{standard: "audio.av3a",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])(?:AV3A|Audio[\s.]?Vivid)\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])(?:AV3A|Audio[\s.]?Vivid)(\d*)`),
		token:   "AV3A"},
	{standard: "audio.alac",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])ALAC\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])ALAC(\d*)`),
		token:   "ALAC"},
	{standard: "audio.ape",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])APE\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])APE(\d*)`),
		token:   "APE"},
	{standard: "audio.wav",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])WAV\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])WAV(\d*)`),
		token:   "WAV"},
	{standard: "audio.ogg",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])(?:OGG|VORBIS)\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])(?:OGG|VORBIS)(\d*)`),
		token:   "OGG"},
	{standard: "audio.dsd",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])DSD\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])DSD(\d*)`),
		token:   "DSD"},
	{standard: "audio.aac",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])AAC\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])AAC(\d*)`),
		token:   "AAC"},
	{standard: "audio.lpcm",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])(?:LPCM|PCM)\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])(?:LPCM|PCM)(\d*)`),
		token:   "LPCM"},
	{standard: "audio.opus",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])OPUS\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])OPUS(\d*)`),
		token:   "Opus"},
	{standard: "audio.mp3",
		detect:  regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])MP[23]\b`),
		replace: regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])MP[23](\d*)`),
		token:   "MP3"},
}

// audioStandardDisplayTokens 标准音频键 → 标题组件里的展示写法。
// 取值对齐 title/simple_components.go extractAudioFromTitle 的产出习惯（AC-3 显示 DD、Opus 驼峰）。
var audioStandardDisplayTokens = map[string]string{
	"audio.truehd":       "TrueHD",
	"audio.truehd_atmos": "TrueHD Atmos",
	"audio.dtsx":         "DTS:X",
	"audio.dts_hd_ma":    "DTS-HD MA",
	"audio.dts_hd_hr":    "DTS-HD HR",
	"audio.dts":          "DTS",
	"audio.ddp":          "DDP",
	"audio.ddp_atmos":    "DDP Atmos",
	"audio.ac3":          "DD",
	"audio.flac":         "FLAC",
	"audio.av3a":         "AV3A",
	"audio.alac":         "ALAC",
	"audio.ape":          "APE",
	"audio.wav":          "WAV",
	"audio.ogg":          "OGG",
	"audio.dsd":          "DSD",
	"audio.aac":          "AAC",
	"audio.lpcm":         "LPCM",
	"audio.opus":         "Opus",
	"audio.mp3":          "MP3",
}

// TitleAudioCodecFamily 判断文本中的音频编码 token 属于哪个标准键家族（含 Atmos/JOC 细分）。
// 参数/返回：text 为标题或标题组件值；未命中任何家族时返回空串。
// 副作用：无。
func TitleAudioCodecFamily(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	for _, rule := range audioTitleFamilyRules {
		if !rule.detect.MatchString(trimmed) {
			continue
		}
		if rule.atmos != "" && audioTitleHasAtmosMarker(trimmed) {
			return rule.atmos
		}
		return rule.standard
	}
	return ""
}

// ReplaceTitleAudioCodecToken 把文本中旧家族的音频编码 token 替换为标准键对应的展示写法。
// 参数/返回：text 为标题或标题组件值；standardAudioCodec 为标准音频键（audio.*）。
// 文本未命中音频 token、与标准键同家族、或标准键无展示写法时原样返回；粘连声道数字（DDP2.0）会保留。
// 失败场景：不返回错误。
// 副作用：无。
//
// 背景：标题组件「音频编码」由标题文本推导（extractAudioFromTitle），标准 audio_codec 改以
// MediaInfo 第一条音轨为准后，两者可能矛盾（标题 DDP2.0、首音轨 AAC）——与「媒介」格的
// Remux 残留同类，发种标题也会带上与标准键矛盾的编码声明。
func ReplaceTitleAudioCodecToken(text, standardAudioCodec string) string {
	trimmed := strings.TrimSpace(text)
	standard := strings.TrimSpace(standardAudioCodec)
	if trimmed == "" || !strings.HasPrefix(standard, "audio.") {
		return trimmed
	}
	newToken := audioStandardDisplayTokens[standard]
	if newToken == "" {
		return trimmed
	}
	family := TitleAudioCodecFamily(trimmed)
	if family == "" || family == standard {
		return trimmed
	}
	var matched *audioTitleFamilyRule
	for idx, rule := range audioTitleFamilyRules {
		if rule.standard == family {
			matched = &audioTitleFamilyRules[idx]
			break
		}
	}
	if matched == nil {
		return trimmed
	}
	next := matched.replace.ReplaceAllString(trimmed, "${1}"+newToken+"${2}")
	if strings.TrimSpace(next) == "" {
		return trimmed
	}
	return strings.TrimSpace(next)
}

// audioTitleHasAtmosMarker 判断文本是否带 Atmos/JOC 标记（用于 TrueHD/DDP 的细分标准键）。
func audioTitleHasAtmosMarker(text string) bool {
	upper := strings.ToUpper(text)
	return strings.Contains(upper, "ATMOS") || strings.Contains(upper, "JOC")
}
