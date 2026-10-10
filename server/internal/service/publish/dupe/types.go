package dupe

// Query 描述一次 dupe 检索所需的全部输入。
//
// 判定维度由站点设置里「该媒介的规则」决定（见 rules.go）：
//   - 媒介 / 分辨率 / 视频编码 / 音频编码由 Filters 承担（站点用自己的元数据筛掉不一致的条目）；
//   - 文件大小与制作组由 MatchSize / MatchTeam 声明，对返回的候选逐条比对。
type Query struct {
	// BaseURL / Cookie / UserAgent 为站点检索所需的会话信息。
	BaseURL   string
	Cookie    string
	UserAgent string

	// DoubanID / IMDbID / TMDbID 为外部条目 ID。站点搜索页对多种 ID 的匹配能力不同，
	// 因此由各站点实现自行决定使用哪个字段、按什么形式拼接。
	DoubanID string
	IMDbID   string
	TMDbID   string

	// Filters 为站点搜索 URL 的筛选参数（已换算为站点取值），键为参数名、值为参数值。
	// 值为空字符串的参数会被忽略，避免提交空筛选导致站点忽略该维度。
	Filters map[string]string

	// Title 为待发布种子的主标题，用于提取制作组。
	Title string

	// TorrentSizeBytes 为待发布种子的总体积（字节），用于与检索结果的体积做容差比较。
	TorrentSizeBytes int64

	// SizeToleranceBytes 为体积容差（字节）。小于 0 时视为 0，即要求体积完全一致。
	SizeToleranceBytes int64

	// MatchSize 表示本次判定需要比对体积（规则勾选了「文件大小」）。
	MatchSize bool
	// MatchTeam 表示本次判定需要比对制作组（规则勾选了「制作组」）。
	MatchTeam bool
	// Dimensions 为本次生效的判定维度，仅用于日志说明与命中说明的生成。
	Dimensions []string
}

// Candidate 是从站点搜索结果中解析出的一条候选种子。
type Candidate struct {
	// TorrentID 为站点种子 ID（用于拼接详情页链接）。
	TorrentID string
	// Title 为站点上的主标题。
	Title string
	// SizeBytes 为站点记录的种子总体积（字节）。
	SizeBytes int64
}

// Result 表示一次 dupe 检索的结论。
type Result struct {
	// Matched 为命中的 dupe 候选；nil 表示未发现重复。
	Matched *Candidate
	// Reason 为人类可读的判定说明，用于发布日志与拦截提示。
	Reason string
	// Detail 为过程日志（检索 URL、候选数量等），便于排查。
	Detail string
	// SearchURL 为真正命中的那一次检索地址，供前端「查重地址」跳转。
	SearchURL string
	// MatchedURL 为命中重复种子的详情页地址，供前端「重复种子」跳转。
	MatchedURL string
}

// IsDupe 判定本次检索是否命中 dupe。
func (r Result) IsDupe() bool { return r.Matched != nil }
