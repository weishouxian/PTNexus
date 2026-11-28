package siteprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 本文件实现自研/官方 JSON API 站的账号数据抓取。
//
// 与 NexusPHP 网页解析不同，这批站点没有 userdetails.php：
//   - M-Team 走 /api/member/profile（x-api-key），memberCount 里有 uploaded/downloaded/bonus/shareRate；
//   - 好学 hxpt、可乐 colapt、YemaPT、朱雀、肉丝 PeerGo 各有自己的当前用户接口。
//
// 各站字段命名并不统一（uploaded / upload / uploadBytes…），因此除 M-Team 走精确字段外，
// 其余站点统一用「字段名归一化后按别名探测」的方式取值 —— 这也是 MoviePilot / PT-Plugin-Plus
// 处理同类站点的通用做法：只依赖语义，不依赖某个站的字段拼写。

const (
	jsonChannelMTeam   = "mteam"
	jsonChannelHxpt    = "hxpt"
	jsonChannelColapt  = "colapt"
	jsonChannelRousi   = "rousi"
	jsonChannelYemaPT  = "yemapt"
	jsonChannelZhuque  = "zhuque"
	requestTimeoutEach = 20 * time.Second
)

// jsonEndpoint 描述一个 JSON 通道的请求方式。
type jsonEndpoint struct {
	Channel  string
	Path     string
	Method   string
	NeedCSRF bool
	// StatsPath 是同一站点的补充统计端点（部分站把身份与流量拆成两个接口）。
	StatsPath string
	// StatsNote 说明「该站确实没有统计接口」，抓取成功但只有身份时写进备注。
	StatsNote string
}

// detectJSONChannel 判定站点是否走 JSON 通道，返回通道标识与请求描述。
// 判定口径与 publish/dupe 的 Cookie 校验一致（同样按站点代码/域名包含关键字匹配），
// 避免出现「校验走接口、取数据走网页」两套判定。
func detectJSONChannel(siteCode, baseURL string) (jsonEndpoint, bool) {
	haystack := strings.ToLower(strings.TrimSpace(siteCode) + " " + strings.TrimSpace(baseURL))
	switch {
	case strings.Contains(haystack, "m-team") || strings.Contains(haystack, "mteam") || strings.Contains(haystack, "馒头"):
		return jsonEndpoint{Channel: jsonChannelMTeam, Path: "/api/member/profile", Method: http.MethodPost}, true
	case strings.Contains(haystack, "hxpt") || strings.Contains(haystack, "haoxue") || strings.Contains(haystack, "好学"):
		return jsonEndpoint{Channel: jsonChannelHxpt, Path: "/api/v1/me", Method: http.MethodGet}, true
	case strings.Contains(haystack, "colapt") || strings.Contains(haystack, "可乐"):
		// /api/v1/me 只有身份，流量在 /api/v1/me/stats（raw_*/billed_* 字节、seeding、points_balance）。
		return jsonEndpoint{Channel: jsonChannelColapt, Path: "/api/v1/me", Method: http.MethodGet, StatsPath: "/api/v1/me/stats"}, true
	case strings.Contains(haystack, "rousi") || strings.Contains(haystack, "肉丝"):
		// 肉丝 PeerGo 的 /api/v1/session 只返回身份，实测未开放流量统计端点。
		// session 只回 csrf_token / expires_at / user{id,username,display_name}：
		// 既没有等级字段，也没有流量统计端点（实测 /api/v1/me、/api/v1/me/stats 均 404）。
		return jsonEndpoint{Channel: jsonChannelRousi, Path: "/api/v1/session", Method: http.MethodGet,
			StatsNote: "该站接口未提供等级与流量统计（仅同步账号身份）"}, true
	case strings.Contains(haystack, "yemapt") || strings.Contains(haystack, "yema"):
		return jsonEndpoint{Channel: jsonChannelYemaPT, Path: "/api/user/profile", Method: http.MethodGet}, true
	case strings.Contains(haystack, "zhuque") || strings.Contains(haystack, "朱雀"):
		// getSecurityInfo 只有身份；getInfo 才带 upload/download/seeding/bonus/seedBonus。
		return jsonEndpoint{Channel: jsonChannelZhuque, Path: "/api/user/getInfo", Method: http.MethodGet, NeedCSRF: true}, true
	}
	return jsonEndpoint{}, false
}

// mteamAPIBase 是 M-Team 的接口域名。网页域 kp.m-team.cc 不代理 /api/*（直接回 403），
// 发种、查重也都走这个域名。
const mteamAPIBase = "https://api.m-team.cc"

// mteamRoleKeepAlive 是保号等级对应的 role（府尹 / Extreme User）。
// 府丞的「封存时永久保号」只是封存后的豁免（与 NexusPHP 同口径），不算保号线。
const mteamRoleKeepAlive = 7

// mteamLevelSpec 描述 M-Team 的一个等级。
// 接口只返回 `role`（1-9）这个数字，没有等级名、门槛与权限字段，站点也没有等级表接口，
// 因此等级体系按站点官方说明内置（用户 2026-11-26 提供）。
type mteamLevelSpec struct {
	Role         int
	Name         string
	Requirements string
	Privileges   string
}

var mteamLevelSpecs = []mteamLevelSpec{
	{Role: 1, Name: "小卒(User)", Privileges: "可以發候選、發佈趣味盒、兌換魔力"},
	{Role: 2, Name: "捕頭(Power User)", Requirements: "註冊 > 4 周；下載 > 200 GB；分享率 > 2（低於 1.9 自動降級）", Privileges: "同小卒(User)"},
	{Role: 3, Name: "知縣(Elite User)", Requirements: "註冊 > 8 周；下載 > 400 GB；分享率 > 3（低於 2.9 自動降級）", Privileges: "可以發送邀請"},
	{Role: 4, Name: "通判(Crazy User)", Requirements: "註冊 > 12 周；下載 > 500 GB；分享率 > 4（低於 3.9 自動降級）", Privileges: "同知縣(Elite User)"},
	{Role: 5, Name: "知州(Insane User)", Requirements: "註冊 > 16 周；下載 > 800 GB；分享率 > 5（低於 4.9 自動降級）", Privileges: "同知縣(Elite User)"},
	{Role: 6, Name: "府丞(Veteran User)", Requirements: "註冊 > 20 周；下載 > 1000 GB；分享率 > 6（低於 5.9 自動降級）", Privileges: "封存時永久保號"},
	{Role: 7, Name: "府尹(Extreme User)", Requirements: "註冊 > 24 周；下載 > 2000 GB；分享率 > 7（低於 6.9 自動降級）", Privileges: "永久保號"},
	{Role: 8, Name: "總督(Ultimate User)", Requirements: "註冊 > 28 周；下載 > 2500 GB；分享率 > 8（低於 7.9 自動降級）", Privileges: "同府尹(Extreme User)"},
	{Role: 9, Name: "大臣(mTorrent Master)", Requirements: "註冊 > 32 周；下載 > 3000 GB；分享率 > 9（低於 8.9 自動降級）", Privileges: "同府尹(Extreme User)"},
}

// mteamLevels 返回 M-Team 的 9 级等级体系（标出保号线）。
func mteamLevels() []UserLevel {
	levels := make([]UserLevel, 0, len(mteamLevelSpecs))
	for index, spec := range mteamLevelSpecs {
		levels = append(levels, UserLevel{
			Name:         spec.Name,
			Order:        index,
			Requirements: spec.Requirements,
			Privileges:   spec.Privileges,
			KeepAlive:    spec.Role == mteamRoleKeepAlive,
		})
	}
	return levels
}

// mteamRoleName 把接口的 role 数字翻成等级名（`9` → `大臣(mTorrent Master)`）；未知 role 返回空。
func mteamRoleName(role string) string {
	value, err := strconv.Atoi(strings.TrimSpace(role))
	if err != nil {
		return ""
	}
	for _, spec := range mteamLevelSpecs {
		if spec.Role == value {
			return spec.Name
		}
	}
	return ""
}

// mteamRoleFromRoot 从 /api/member/profile 的响应里取 `data.role`（接口给的是字符串 "9"）。
func mteamRoleFromRoot(root any) string {
	payload, ok := root.(map[string]any)
	if !ok {
		return ""
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		return ""
	}
	switch value := data["role"].(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		return strconv.Itoa(int(value))
	}
	return ""
}

// jsonRequestBase 返回该 JSON 通道实际请求的域名。
// 绝大多数站点的接口就在网页域下；M-Team 例外，网页域对 /api/* 直接 403。
func jsonRequestBase(channel, webBase string) string {
	if channel == jsonChannelMTeam {
		return mteamAPIBase
	}
	return webBase
}

// fetchJSONProfile 走 JSON 接口抓取站点账号数据。
// 参数/返回：target 为站点凭据快照；返回 Profile（未填充站点元信息）与错误。
// 失败场景：凭据缺失、网络错误、HTTP 非 2xx、响应非 JSON、接口返回未登录。
// 副作用：向目标站点发起 1~2 次 HTTP 请求。
func fetchJSONProfile(ctx context.Context, target SiteTarget, endpoint jsonEndpoint) (Profile, error) {
	webBase := normalizeBaseURL(target.BaseURL)
	if webBase == "" {
		return Profile{}, fmt.Errorf("站点缺少域名（base_url）")
	}
	// M-Team 的网页域（kp.m-team.cc）不代理接口：在网页域上请求 /api/member/profile
	// 直接被 nginx 回 403（此前「接口返回 HTTP 403（Cookie/令牌可能已失效）」就是这么来的，
	// 令牌本身是好的），接口必须走 api.m-team.cc —— 与发种/查重用的同一个 API 域名。
	base := jsonRequestBase(endpoint.Channel, webBase)

	headers := map[string]string{
		"Accept": "application/json, text/plain, */*",
	}
	cookie := strings.TrimSpace(target.Cookie)
	passkey := strings.TrimSpace(target.Passkey)

	switch endpoint.Channel {
	case jsonChannelMTeam:
		// 令牌口径与发种/校验一致：Passkey 栏优先，Cookie 栏兜底。
		token := passkey
		if token == "" {
			token = cookie
		}
		if token == "" {
			return Profile{}, fmt.Errorf("未填写 M-Team 存取令牌")
		}
		headers["x-api-key"] = token
		headers["Content-Type"] = "application/x-www-form-urlencoded"
		headers["Referer"] = webBase + "/"
		headers["Origin"] = webBase
		headers["Accept-Language"] = "zh-CN,zh;q=0.9,en;q=0.8"
	case jsonChannelRousi:
		if cookie == "" {
			cookie = passkey
		}
		if cookie == "" {
			return Profile{}, fmt.Errorf("未填写肉丝会话 Cookie")
		}
		if !strings.Contains(strings.ToLower(cookie), "peergo_session=") {
			cookie = "__Host-peergo_session=" + cookie
		}
		headers["Cookie"] = cookie
	default:
		if cookie == "" {
			return Profile{}, fmt.Errorf("未填写 Cookie")
		}
		headers["Cookie"] = cookie
	}

	if endpoint.NeedCSRF {
		token, err := fetchCSRFToken(ctx, base, cookie)
		if err != nil {
			return Profile{}, err
		}
		headers["x-csrf-token"] = token
		headers["X-Requested-With"] = "XMLHttpRequest"
		headers["Referer"] = base + "/user/rss"
		headers["Origin"] = base
	}

	root, err := requestJSON(ctx, endpoint.Method, base+endpoint.Path, headers)
	if err != nil {
		return Profile{}, err
	}
	roots := []any{root}
	if endpoint.StatsPath != "" {
		// 统计端点出错不阻断整体：身份已拿到就算抓取成功，缺的流量由 Notice 说明。
		if statsRoot, statsErr := requestJSON(ctx, http.MethodGet, base+endpoint.StatsPath, headers); statsErr == nil {
			roots = append(roots, statsRoot)
		}
	}

	profile := Profile{Source: "json", Notice: endpoint.StatsNote}
	applyJSONPayload(&profile, roots...)
	// YemaPT 的等级就是裸数字，站点里也只有数字（前端提示「等级0，即将被ban」
	// 「等级需要达到 Level 3」，没有等级名）；applyJSONPayload 会把纯数字当成 ID 跳过，
	// 这里补成「Level 4」，否则整站等级是空的。
	if endpoint.Channel == jsonChannelYemaPT && profile.UserClass == "" {
		profile.UserClass = jsonNumericLevelName(root)
	}
	// YemaPT 的等级门槛只在站点规则页里（level 0 ~ level 11），接口没有任何等级体系来源，
	// 按内置表直出；否则等级阶梯与升级进度全空（只有一条「仅同步当前等级」的占位行）。
	if endpoint.Channel == jsonChannelYemaPT {
		if builtin, ok := builtinSiteLevels("yemapt"); ok {
			profile.Levels = builtin
		}
	}
	// M-Team 的等级只有 `role` 这个数字（1-9），没有等级名也没有权限字段；
	// 站点没开放等级表接口，只能按站点官方说明内置 9 级（含每级门槛与保号线），
	// 再把当前 role 翻成对应等级名，这样「等级」列与等级阶梯都能对上。
	if endpoint.Channel == jsonChannelMTeam {
		if role := mteamRoleFromRoot(root); role != "" {
			if name := mteamRoleName(role); name != "" {
				profile.UserClass = name
				profile.Levels = mteamLevels()
			}
		}
	}
	// 朱雀的接口只返回当前等级名（`getInfo` 里的等级字段），没有任何等级体系接口，
	// 规则写成「筑基 → 结丹 → … → 真仙」8 级且只在站点规则页里，所以按内置表直出，
	// 否则等级阶梯与升级进度全空（之前只有一条「仅同步当前等级」的占位行）。
	if endpoint.Channel == jsonChannelZhuque {
		if builtin, ok := builtinSiteLevels("zhuque"); ok {
			profile.Levels = builtin
		}
	}
	profile.UserID = firstNonEmpty(profile.UserID, target.UserID)
	if profile.Username == "" && profile.UserID == "" {
		return profile, fmt.Errorf("接口未返回用户信息（可能未登录）")
	}
	// 接口只给绝对量、不给分享率是常态（好学/可乐/朱雀都是），这里统一按上传/下载推算；
	// 下载为 0 且有上传量时站点显示 ∞，用 -1 表示。
	if profile.Ratio == 0 {
		switch {
		case profile.Downloaded > 0:
			profile.Ratio = float64(profile.Uploaded) / float64(profile.Downloaded)
		case profile.Uploaded > 0:
			profile.Ratio = -1
		}
	}
	// 非 NexusPHP 站点没有 faq.php 等级表：把接口返回的当前等级作为唯一一条记录带出去，
	// 否则页面「等级」点开是空的（朱雀「真仙」、可乐「经典罐」这类自定义等级名更无从对照）。
	if len(profile.Levels) == 0 {
		profile.Levels = singleLevelFallback(profile.UserClass)
	}
	return profile, nil
}

// requestJSON 发起一次请求并把响应解析成任意 JSON 结构。
// 参数/返回：method/url/headers 描述请求；返回解码后的根节点。
// 失败场景：网络错误、HTTP 非 2xx、空响应、非法 JSON。
// 副作用：发起一次 HTTP 请求。
func requestJSON(ctx context.Context, method, rawURL string, headers map[string]string) (any, error) {
	body, status, err := doRequest(ctx, method, rawURL, headers, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return nil, fmt.Errorf("接口返回 HTTP %d（Cookie/令牌可能已失效）", status)
		}
		return nil, fmt.Errorf("接口返回 HTTP %d: %s", status, snippet(body))
	}

	if status == http.StatusNoContent || len(strings.TrimSpace(string(body))) == 0 {
		// 肉丝 PeerGo 把「未登录」改成了 204 空响应（前端 session.mutations 里 204 即 null），
		// 这里必须单独判空，否则会落到 JSON 解析分支报「不是合法 JSON」，把失效误导成接口改版。
		return nil, fmt.Errorf("接口返回空响应（HTTP %d，Cookie/令牌可能已失效）", status)
	}

	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("响应不是合法 JSON: %s", snippet(body))
	}
	return root, nil
}

// fetchCSRFToken 访问上传页，从 meta 标签提取 x-csrf-token（朱雀 TNode 站点要求）。
func fetchCSRFToken(ctx context.Context, base, cookie string) (string, error) {
	body, status, err := doRequest(ctx, http.MethodGet, base+"/torrent/upload", map[string]string{"Cookie": cookie}, nil)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("获取 CSRF Token 失败（HTTP %d）", status)
	}
	if match := reCSRFMeta.FindStringSubmatch(string(body)); len(match) > 1 {
		if token := strings.TrimSpace(match[1]); token != "" {
			return token, nil
		}
	}
	return "", fmt.Errorf("上传页未找到 x-csrf-token")
}

var reCSRFMeta = regexp.MustCompile(`(?is)<meta[^>]*\bname\s*=\s*["']?x-csrf-token["']?[^>]*\bcontent\s*=\s*["']([^"']+)["']`)

// jsonHit 表示 JSON 树里命中的一处标量值，depth 越小层级越浅（浅层优先）。
type jsonHit struct {
	Key string
	// Path 为归一化后的点分路径（如 data.class.name），用于取嵌套对象里的字段。
	Path  string
	Value string
	Depth int
}

// collectJSONHits 广度优先遍历 JSON 树，收集所有标量键值对。
// 参数/返回：root 为 Unmarshal 后的任意结构；返回按层级浅到深排列的命中列表。
// 失败场景：无。
// 副作用：无。
func collectJSONHits(root any) []jsonHit {
	hits := make([]jsonHit, 0, 32)
	type queueItem struct {
		node  any
		depth int
		path  string
	}
	queue := []queueItem{{node: root, depth: 0}}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		if item.depth > 4 {
			continue
		}
		switch typed := item.node.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			// map 遍历无序，排序保证同一响应每次解析结果一致。
			sort.Strings(keys)
			for _, key := range keys {
				value := typed[key]
				path := joinJSONPath(item.path, key)
				if text, ok := scalarText(value); ok {
					hits = append(hits, jsonHit{Key: normalizeJSONKey(key), Path: path, Value: text, Depth: item.depth})
					continue
				}
				queue = append(queue, queueItem{node: value, depth: item.depth + 1, path: path})
			}
		case []any:
			for _, value := range typed {
				if _, ok := scalarText(value); ok {
					continue
				}
				queue = append(queue, queueItem{node: value, depth: item.depth + 1, path: item.path})
			}
		}
	}
	return hits
}

// joinJSONPath 拼接归一化的点分路径。
func joinJSONPath(parent, key string) string {
	normalized := normalizeJSONKey(key)
	if parent == "" {
		return normalized
	}
	return parent + "." + normalized
}

// scalarText 把 JSON 标量转成字符串；对象/数组返回 false。
func scalarText(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(typed), true
	case json.Number:
		return typed.String(), true
	case nil:
		return "", false
	default:
		return "", false
	}
}

// normalizeJSONKey 归一化 JSON 字段名：小写并去掉下划线、连字符与空格。
func normalizeJSONKey(key string) string {
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	return replacer.Replace(strings.ToLower(strings.TrimSpace(key)))
}

// 字段别名表（均已归一化）。取第一个命中的键。
var (
	jsonAliasUserID    = []string{"id", "userid", "uid", "mid"}
	jsonAliasUsername  = []string{"username", "nickname", "displayname", "name", "account"}
	jsonAliasUserClass = []string{"classname", "userclass", "userlevel", "memberlevel", "level", "class", "rank", "role", "grouptitle"}
	// jsonAliasClassName 走点分路径：不少站把等级做成对象（class:{name:"真仙"}），键名匹配拿不到。
	jsonAliasClassName = []string{"class.name", "userclass.name", "level.name", "rank.name", "group.name", "role.name"}
	// 流量字段：先取「原始/已上传」这类明确语义，再退到 upload/download（部分站另有 billed_* 计费口径）。
	jsonAliasUploaded   = []string{"uploaded", "rawuploadedbytes", "uploadedbytes", "totaluploaded", "upload", "uploadsize", "realupload", "billeduploadedbytes"}
	jsonAliasDownloaded = []string{"downloaded", "rawdownloadedbytes", "downloadedbytes", "totaldownloaded", "download", "downloadsize", "realdownload", "billeddownloadedbytes"}
	jsonAliasRatio      = []string{"ratio", "sharerate", "shareratio", "uploadratio"}
	jsonAliasBonus      = []string{"bonus", "bonuspoint", "bonuspoints", "bonusbalance", "pointsbalance", "karma", "points"}
	jsonAliasSeedBonus  = []string{"seedbonus", "seedingbonus", "seedpoints", "seedpoint", "seedscore", "bonusseed"}
	// 只认「我的做种/下载中」语义，不收录 seeders/leechers —— 那是种子详情里的（下载者）数量。
	jsonAliasSeeding  = []string{"seeding", "seedingcount", "seedcount", "torrentsseeding", "activeseeding", "seedingtorrents"}
	jsonAliasLeeching = []string{"leeching", "leechingcount", "leechcount", "torrentsleeching", "leechingtorrents", "downloading", "downloadingcount"}
	jsonAliasInvites  = []string{"invites", "invitecount", "invitenum", "availableinvitenum", "availableinvites", "invitation", "invitedcount"}
	jsonAliasJoinDate = []string{"joindate", "joinedat", "createdat", "createddate", "registerdate", "registertime", "regdate", "regtime", "createdtime"}
)

// jsonKeyPrefixes / jsonKeySuffixes 是字段名里常见的前后缀修饰，用于把 totalUploaded、
// raw_uploaded_bytes 这类写法归到 uploaded 语义上。
//
// 这里刻意不用宽松的「包含」匹配：yemapt 的 maxUploadTorrentSize 曾因包含 upload
// 被当成上传量，导致上传量显示 3 字节。
var (
	jsonKeyPrefixes = []string{"total", "user", "my", "account", "raw", "real", "billed", "current", "active", "self", "member", "promotion"}
	jsonKeySuffixes = []string{"bytes", "size", "total", "count", "num", "value", "point", "points", "balance", "amount"}
)

// jsonKeyMatches 判断归一化后的键名是否表达该别名的语义：完全相等，或带一个已知前后缀。
func jsonKeyMatches(key, alias string) bool {
	if key == alias {
		return true
	}
	for _, suffix := range jsonKeySuffixes {
		if key == alias+suffix {
			return true
		}
	}
	for _, prefix := range jsonKeyPrefixes {
		if key == prefix+alias {
			return true
		}
		for _, suffix := range jsonKeySuffixes {
			if key == prefix+alias+suffix {
				return true
			}
		}
	}
	return false
}

// pickJSON 按别名优先级取值，三档精度依次放宽：
//  1. 键名完全相等；
//  2. 点分路径后缀匹配（取嵌套对象字段）；
//  3. 带已知前后缀修饰的键名（totalUploaded / raw_uploaded_bytes）。
func pickJSON(hits []jsonHit, aliases []string) string {
	for _, alias := range aliases {
		for _, hit := range hits {
			if hit.Key == alias {
				return hit.Value
			}
		}
	}
	for _, alias := range aliases {
		if !strings.Contains(alias, ".") {
			continue
		}
		for _, hit := range hits {
			if strings.HasSuffix(hit.Path, alias) {
				return hit.Value
			}
		}
	}
	for _, alias := range aliases {
		if len(alias) < 4 {
			continue
		}
		for _, hit := range hits {
			if jsonKeyMatches(hit.Key, alias) {
				return hit.Value
			}
		}
	}
	return ""
}

// dropJSONHits 剔除键名命中指定别名的条目（用于把「做种积分」从魔力值的候选里摘出去）。
// 参数/返回：hits 为原命中列表，aliases 为归一化别名；返回过滤后的新切片（不修改原切片）。
// 失败场景：无。
// 副作用：无。
func dropJSONHits(hits []jsonHit, aliases []string) []jsonHit {
	result := make([]jsonHit, 0, len(hits))
	for _, hit := range hits {
		matched := false
		for _, alias := range aliases {
			if jsonKeyMatches(hit.Key, alias) {
				matched = true
				break
			}
		}
		if !matched {
			result = append(result, hit)
		}
	}
	return result
}

// applyJSONPayload 把 JSON 响应填进 Profile（字段探测 + 数值规范化）。
// 参数/返回：roots 可传多个响应（身份接口 + 统计接口），命中按传入顺序优先。
// 失败场景：无。
// 副作用：修改 profile。
func applyJSONPayload(profile *Profile, roots ...any) {
	hits := make([]jsonHit, 0, 64)
	for _, root := range roots {
		hits = append(hits, collectJSONHits(root)...)
	}

	profile.UserID = firstNonEmpty(profile.UserID, pickJSON(hits, jsonAliasUserID))
	// 用户名优先取明确的 username/nickname，避免 id 类字段抢位。
	if name := pickJSON(hits, jsonAliasUsername); name != "" && !looksNumeric(name) {
		profile.Username = name
	}
	if class := pickJSON(hits, jsonAliasUserClass); class != "" && !looksNumeric(class) {
		profile.UserClass = class
	}
	// 等级是对象的情况（class:{level:8,name:"真仙"}）按路径补一次，数值型 level 会被上面跳过。
	if profile.UserClass == "" {
		if class := pickJSON(hits, jsonAliasClassName); class != "" && !looksNumeric(class) {
			profile.UserClass = class
		}
	}
	if value := pickJSON(hits, jsonAliasUploaded); value != "" {
		if bytes, ok := toBytes(value); ok {
			profile.Uploaded = bytes
		}
	}
	if value := pickJSON(hits, jsonAliasDownloaded); value != "" {
		if bytes, ok := toBytes(value); ok {
			profile.Downloaded = bytes
		}
	}
	if value := pickJSON(hits, jsonAliasRatio); value != "" {
		if ratio, ok := parseRatio(value); ok {
			profile.Ratio = ratio
		}
	}
	// 做种积分先取：字段名 seedBonus / seedingBonus 都包含 bonus（魔力值别名），
	// 先取走再把这些键从命中列表里剔除，避免同一份值被记成魔力值。
	if value := pickJSON(hits, jsonAliasSeedBonus); value != "" {
		if bonus, ok := parseLooseFloat(value); ok {
			profile.SeedBonus = bonus
			hits = dropJSONHits(hits, jsonAliasSeedBonus)
		}
	}
	if value := pickJSON(hits, jsonAliasBonus); value != "" {
		if bonus, ok := parseLooseFloat(value); ok {
			profile.Bonus = bonus
		}
	}
	if value := pickJSON(hits, jsonAliasSeeding); value != "" {
		if count, ok := parseInteger(value); ok {
			profile.Seeding = count
		}
	}
	if value := pickJSON(hits, jsonAliasLeeching); value != "" {
		if count, ok := parseInteger(value); ok {
			profile.Leeching = count
		}
	}
	if value := pickJSON(hits, jsonAliasInvites); value != "" {
		if count, ok := parseInteger(value); ok {
			profile.Invites = count
		}
	}
	profile.JoinDate = pickJSON(hits, jsonAliasJoinDate)
}

// toBytes 把 JSON 里的体积值转成字节数：纯数字直接当字节，带单位的走 parseSizeBytes。
func toBytes(value string) (int64, bool) {
	clean := strings.TrimSpace(value)
	if clean == "" {
		return 0, false
	}
	if looksNumeric(clean) {
		number, err := strconv.ParseFloat(clean, 64)
		if err != nil || number < 0 {
			return 0, false
		}
		return int64(number), true
	}
	return parseSizeBytes(clean)
}

// jsonNumericLevelName 给「等级就是裸数字」的站点补一个可读名（YemaPT 的 level: 2 → Level 2）。
//
// 为什么要单独处理：applyJSONPayload 会把纯数字当成 ID 跳过（很多站点的 class 字段存的是数字 ID），
// 于是这类站点的等级整列为空。YemaPT 站点里全程用数字表示等级（前端提示
// 「等级0，即将被ban」「等级需要达到 Level 3」），没有等级名可查。
func jsonNumericLevelName(root any) string {
	for _, hit := range collectJSONHits(root) {
		if strings.EqualFold(hit.Key, "level") && looksNumeric(hit.Value) {
			return "Level " + strings.TrimSpace(hit.Value)
		}
	}
	return ""
}

// looksNumeric 判断字符串是否为纯数字（允许符号与小数点）。
func looksNumeric(value string) bool {
	clean := strings.TrimSpace(value)
	if clean == "" {
		return false
	}
	if _, err := strconv.ParseFloat(clean, 64); err == nil {
		return true
	}
	return false
}

// doRequest 发起一次 HTTP 请求并读取响应体（限制大小，避免异常页面撑爆内存）。
// 参数/返回：method/url/headers 描述请求，body 可为 nil；返回响应体、状态码与错误。
// 失败场景：构造请求失败、网络错误、读取失败。
// 副作用：发起网络请求。
func doRequest(ctx context.Context, method, rawURL string, headers map[string]string, body []byte) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return nil, 0, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	client := &http.Client{Timeout: requestTimeoutEach}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("请求站点失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("读取响应失败: %w", err)
	}
	return data, resp.StatusCode, nil
}

// snippet 把响应体裁剪成一行短文本，用于错误信息。
func snippet(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	runes := []rune(text)
	if len(runes) > 180 {
		return string(runes[:180]) + "…"
	}
	if text == "" {
		return "(空响应)"
	}
	return text
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
