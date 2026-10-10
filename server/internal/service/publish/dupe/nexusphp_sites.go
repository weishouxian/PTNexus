package dupe

import "strings"

// 本文件汇集「标准 NexusPHP 结果页」站点的 dupe 校验入口。
//
// 这四个站点的搜索结果页结构经实测完全一致（标题在 a[title]、体积在 td.rowfollow），
// 差异只在两处，且都已下沉到各站 YAML 的 dupe_check 配置：
//   - search_areas：哪些检索范围可用（决定检索分几段）
//   - param_templates：筛选参数的命名格式（平铺名 medium12 还是数组名 medium[]，各站不同）
//
// 实测记录（供后续排查对照）：
//   - hdhome  ：search_area 0/1/3/4（无豆瓣）→ IMDb + 标题兜底；筛选为平铺名（medium1 生效，medium[] 返回 0）。
//     另有隐患已规避：该站 medium/分辨率/编码等映射是 @index:N 动态值，
//     发布前拿不到真实值，故这些维度一律不参与检索（见 BuildSearchFilters）。
//   - pterclub：search_area 0/2/3/4/5（有豆瓣，5=Douban ID）→ 豆瓣 + IMDb + 标题；
//     筛选 cat{值} 与 source{值}（该站 medium 映射到上传的 source_sel）。
//   - ourbits ：search_area 0/3/4/5（有豆瓣，5=豆瓣链接）→ 豆瓣 + IMDb + 标题；筛选为数组名 medium[]/standard[]。
//   - chdbits ：search_area 0/1/3/4（无豆瓣）→ IMDb + 标题兜底；筛选为平铺名 medium1/standard1/audiocodec7。
const (
	hdhomeDupeLogModule   = "发布-dupe校验-家园"
	pterclubDupeLogModule = "发布-dupe校验-猫站"
	ourbitsDupeLogModule  = "发布-dupe校验-我堡"
	chdbitsDupeLogModule  = "发布-dupe校验-彩虹岛"
)

// CheckHDHomeDupe 在 HDHome（家园）检索 dupe 候选。
// 参数/返回：query 为检索输入；返回命中结论、过程日志与错误。
// 副作用：向站点发起 1~3 次 GET 请求。
func CheckHDHomeDupe(query Query) (Result, string, error) {
	return checkNexusPHPSiteDupe("hdhome", query, hdhomeDupeLogModule)
}

// CheckPterclubDupe 在 PTerClub（猫站）检索 dupe 候选。
// 参数/返回：query 为检索输入；返回命中结论、过程日志与错误。
// 副作用：向站点发起 1~3 次 GET 请求。
func CheckPterclubDupe(query Query) (Result, string, error) {
	return checkNexusPHPSiteDupe("pterclub", query, pterclubDupeLogModule)
}

// CheckOurbitsDupe 在 OurBits（我堡）检索 dupe 候选。
// 参数/返回：query 为检索输入；返回命中结论、过程日志与错误。
// 副作用：向站点发起 1~3 次 GET 请求。
func CheckOurbitsDupe(query Query) (Result, string, error) {
	return checkNexusPHPSiteDupe("ourbits", query, ourbitsDupeLogModule)
}

// CheckChdbitsDupe 在 CHDBits（彩虹岛）检索 dupe 候选。
// 参数/返回：query 为检索输入；返回命中结论、过程日志与错误。
// 副作用：向站点发起 1~3 次 GET 请求。
func CheckChdbitsDupe(query Query) (Result, string, error) {
	return checkNexusPHPSiteDupe("chdbits", query, chdbitsDupeLogModule)
}

// siteDupeLogModules 汇总各站点 dupe 校验的日志模块名。
// 放在 dupe 包内作为唯一来源：站点适配器与通用钩子都从这里取，避免同一个名字在两处各写一遍。
var siteDupeLogModules = map[string]string{
	"audiences": "发布-dupe校验-人人",
	"luckpt":    "发布-dupe校验-幸运",
	"hdhome":    hdhomeDupeLogModule,
	"pterclub":  pterclubDupeLogModule,
	"ourbits":   ourbitsDupeLogModule,
	"chdbits":   chdbitsDupeLogModule,
}

// LogModuleForSite 返回站点 dupe 校验的日志模块名（未接入的站点返回空串）。
// 参数/返回：siteCode 为站点标识；返回日志模块名。
// 副作用：无。
func LogModuleForSite(siteCode string) string {
	return siteDupeLogModules[strings.ToLower(strings.TrimSpace(siteCode))]
}
