package iyuu

// Site 表示 IYUU 支持的辅种站点。
// 参数/返回：字段与 /reseed/sites/index 返回一一对应。
// 失败场景：无。
// 副作用：无。
type Site struct {
	ID       int64  `json:"id"`
	Nickname string `json:"nickname"`
	BaseURL  string `json:"base_url"`
	Site     string `json:"site"`
}
