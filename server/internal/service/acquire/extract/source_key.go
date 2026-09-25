package extract

import (
	"regexp"
	"strings"
)

// 产地归一的唯一入口说明：
// 「产地 / 制片国家 / 地区」文本 → 标准化 source.* 键，全项目只在本文件维护这张表。
// 归并口径（与各站点 upload.php 的地区选项对齐）：
//
//	中国大陆                     → source.china
//	香港 / 台湾                  → source.hongkong / source.taiwan（站点可再收敛为 HK/TW 港台）
//	欧美：美国                   → source.western
//	      英国/法国/德国/意大利/西班牙/瑞典/丹麦/俄罗斯/加拿大/欧洲 → 各自细粒度 source.* 键
//	日本 / 韩国                  → source.japan / source.korea
//	其他地区（印度、泰国、新加坡、马来西亚、巴西、澳大利亚、其他） → source.india / … / source.other
//
// 细粒度值由站点映射收敛到站点实际选项：只提供「欧美」合并选项的站点（如我堡 US/EU(欧美)）
// 必须把上述欧美各国统一映射到同一个 value，否则会落 default 被静默判成「其他」。

// sourceKeyAliasGroup 描述一组「产地文本 → 标准化 source.* 键」的别名。
// cn 为中文关键词（子串匹配）；latin 为拉丁文关键词（按单词边界匹配）。
type sourceKeyAliasGroup struct {
	key   string
	cn    []string
	latin []*regexp.Regexp
}

// mustSourceLatinRegexp 编译「单词边界」形式的拉丁别名正则。
// 参数/返回：alias 为小写别名；返回可匹配独立单词（前后为非字母数字或串首尾）的正则。
// 失败场景：别名含非法正则字符时 panic（均为包内常量，不会触发）。
// 副作用：无。
func mustSourceLatinRegexp(alias string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^a-z0-9])` + regexp.QuoteMeta(alias) + `(?:[^a-z0-9]|$)`)
}

// newSourceKeyAliasGroup 构造一组产地别名。
// 参数/返回：key 为标准值；cn 为中文别名；latin 为小写拉丁别名；返回构造结果。
// 失败场景：无。
// 副作用：无。
func newSourceKeyAliasGroup(key string, cn []string, latin []string) sourceKeyAliasGroup {
	compiled := make([]*regexp.Regexp, 0, len(latin))
	for _, alias := range latin {
		if strings.TrimSpace(alias) == "" {
			continue
		}
		compiled = append(compiled, mustSourceLatinRegexp(alias))
	}
	return sourceKeyAliasGroup{key: key, cn: cn, latin: compiled}
}

// sourceKeyAliasGroups 是产地归一的判定表，顺序敏感。
// 港台必须排在「中国」之前，否则「中国香港 / 中国台湾」会被判成中国大陆。
// 拉丁别名一律使用三字母代码或完整英文名，避免 "us"/"it"/"de"/"ca" 等两字母代码在英文正文里误命中。
var sourceKeyAliasGroups = []sourceKeyAliasGroup{
	newSourceKeyAliasGroup("source.taiwan", []string{"台湾", "台剧"}, []string{"taiwan", "twn", "tw"}),
	newSourceKeyAliasGroup("source.hongkong", []string{"香港", "港剧"}, []string{"hong kong", "hongkong", "hkg", "hk"}),
	newSourceKeyAliasGroup("source.china", []string{"中国", "大陆", "内地", "国剧"}, []string{"china", "chn", "cn", "mainland"}),
	newSourceKeyAliasGroup("source.japan", []string{"日本", "日剧"}, []string{"japan", "jpn", "jp"}),
	newSourceKeyAliasGroup("source.korea", []string{"韩国", "韩剧"}, []string{"korea", "kor", "kr", "south korea"}),
	// 欧美组：站点若只提供合并选项（如我堡 US/EU(欧美)），由站点映射统一收敛。
	newSourceKeyAliasGroup("source.western", []string{"美国", "美剧", "欧美"}, []string{"usa", "united states", "america", "western"}),
	newSourceKeyAliasGroup("source.uk", []string{"英国", "英剧"}, []string{"united kingdom", "uk", "gbr", "england", "britain"}),
	newSourceKeyAliasGroup("source.france", []string{"法国"}, []string{"france", "fra"}),
	newSourceKeyAliasGroup("source.germany", []string{"德国"}, []string{"germany", "deu"}),
	newSourceKeyAliasGroup("source.italy", []string{"意大利"}, []string{"italy", "ita"}),
	newSourceKeyAliasGroup("source.spain", []string{"西班牙"}, []string{"spain", "esp"}),
	newSourceKeyAliasGroup("source.sweden", []string{"瑞典"}, []string{"sweden", "swe"}),
	newSourceKeyAliasGroup("source.denmark", []string{"丹麦"}, []string{"denmark", "dnk"}),
	newSourceKeyAliasGroup("source.russia", []string{"俄罗斯", "俄国"}, []string{"russia", "rus"}),
	newSourceKeyAliasGroup("source.canada", []string{"加拿大"}, []string{"canada"}),
	newSourceKeyAliasGroup("source.europe", []string{"欧洲"}, []string{"europe", "european"}),
	newSourceKeyAliasGroup("source.india", []string{"印度"}, []string{"india", "ind"}),
	newSourceKeyAliasGroup("source.thailand", []string{"泰国"}, []string{"thailand", "tha"}),
	newSourceKeyAliasGroup("source.singapore", []string{"新加坡"}, []string{"singapore", "sgp"}),
	newSourceKeyAliasGroup("source.malaysia", []string{"马来西亚"}, []string{"malaysia", "mys"}),
	newSourceKeyAliasGroup("source.brazil", []string{"巴西"}, []string{"brazil"}),
	newSourceKeyAliasGroup("source.australia", []string{"澳大利亚", "澳洲"}, []string{"australia", "aus"}),
	newSourceKeyAliasGroup("source.other", []string{"其他"}, []string{"other"}),
}

// NormalizeSourceKeyFromText 将产地/国家/地区文本归一为标准化 source.* 键。
// 参数/返回：text 为任意产地文本（可为多国并列，如「美国 / 英国」）；命中返回标准值，无法识别返回空字符串。
// 失败场景：text 为空或未命中任何别名时返回空字符串（由调用方决定兜底值）。
// 副作用：无。
func NormalizeSourceKeyFromText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	for _, group := range sourceKeyAliasGroups {
		for _, alias := range group.cn {
			if alias != "" && strings.Contains(trimmed, alias) {
				return group.key
			}
		}
		for _, re := range group.latin {
			if re.MatchString(lower) {
				return group.key
			}
		}
	}
	return ""
}
