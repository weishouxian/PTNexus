package siteprofile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
)

const (
	logModule        = "站点数据"
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:128.0) Gecko/20100101 Firefox/128.0"
	maxResponseBytes = 4 << 20
	// 单站抓取超时：首页 + 资料页 + 做种列表兜底最多 4 次请求。
	fetchTimeoutEach = 45 * time.Second
	// 并发抓取数：站点普遍 30~90 个，取 6 既能压住总耗时，也不至于被站点限频。
	refreshConcurrency = 6
)

// SiteTarget 是一次抓取所需的站点快照（凭据来自 sites 表，不落日志）。
type SiteTarget struct {
	SiteCode string
	Nickname string
	BaseURL  string
	Cookie   string
	Passkey  string
	UserID   string
}

// Profile 是抓取结果（未落库的中间结构）。
type Profile struct {
	UserID     string
	Username   string
	UserClass  string
	Uploaded   int64
	Downloaded int64
	Ratio      float64
	Bonus      float64
	// SeedBonus 为「做种积分」（做种时长/体积累积的积分，部分站叫「做种得分」），与魔力值是两套体系。
	SeedBonus float64
	Seeding   int64
	Leeching  int64
	Invites   int64
	JoinDate  string
	Source    string
	// Notice 是抓取成功但数据不完整时的说明（如站点未开放统计接口），落库后作为 Message 展示。
	Notice string
	// Levels 为该站完整等级体系（含权限、升级条件与保号等级标记）。
	Levels []UserLevel
}

// Service 负责从各 PT 站点抓取账号数据并落库。
type Service struct {
	siteRepo    *repository.SiteRepository
	profileRepo *repository.SiteProfileRepository
	// dailyRepo 存按天快照，用于计算当天增量（上传/下载/做种积分/魔力值）。
	dailyRepo *repository.SiteProfileDailyRepository
}

// NewService 创建站点数据服务。
func NewService(siteRepo *repository.SiteRepository, profileRepo *repository.SiteProfileRepository, dailyRepo *repository.SiteProfileDailyRepository) *Service {
	return &Service{siteRepo: siteRepo, profileRepo: profileRepo, dailyRepo: dailyRepo}
}

// SiteData 是接口输出结构：站点账号数据 + 展开后的等级体系。
// 嵌入了落库行（LevelsJSON 不参与序列化），Levels 直接给前端渲染等级阶梯。
type SiteData struct {
	repository.SiteProfile
	Levels []UserLevel `json:"levels"`
	// KeepAliveAuto 是按站点权限**自动识别**出来的保号等级名（不受手动设置影响）。
	// 前端用它展示「自动识别：XXX」，用户把手动值清空时也能立刻看到会回到哪一级。
	KeepAliveAuto string `json:"keep_alive_auto"`
	// Tags 是站点在「站点管理」里打的标签（源 true 值是 sites.tags），
	// 只用于列表页按标签筛选，不参与账号数据抓取。
	Tags []string `json:"tags"`
}

var (
	// ErrProfileMissing 表示该站点还没有抓取过数据（没有等级表，无法指定保号等级）。
	ErrProfileMissing = errors.New("该站点还没有等级数据，请先刷新后再设置")
	// ErrLevelUnknown 表示提交的等级名不在该站等级表里（多半是前端传了别的站的名字）。
	ErrLevelUnknown = errors.New("该等级不在站点等级表中")
)

// applyKeepAliveOverride 用人工指定的保号等级覆盖自动识别结果。
//
// 传空名（或等级表里找不到同名等级）时原样返回：用户清空设置就回到站点权限的自动判定。
// 名称先精确比、再按 normalizeLevelName 归一化比（容忍站点重抓后等级别名里的空格/括号变化）。
func applyKeepAliveOverride(levels []UserLevel, manual string) []UserLevel {
	name := strings.TrimSpace(manual)
	if name == "" || len(levels) == 0 {
		return levels
	}
	target := -1
	for index, level := range levels {
		if level.Name == name {
			target = index
			break
		}
	}
	if target < 0 {
		key := keepAliveNameKey(name)
		for index, level := range levels {
			if keepAliveNameKey(level.Name) == key {
				target = index
				break
			}
		}
	}
	if target < 0 {
		return levels
	}
	for index := range levels {
		levels[index].KeepAlive = index == target
	}
	return levels
}

// levelNameExists 判断等级名是否在该站等级表里（规则同 applyKeepAliveOverride）。
func levelNameExists(levels []UserLevel, name string) bool {
	key := keepAliveNameKey(name)
	for _, level := range levels {
		if level.Name == name || keepAliveNameKey(level.Name) == key {
			return true
		}
	}
	return false
}

// keepAliveNameKey 是手动保号等级匹配用的宽松键：小写并去掉空白/连字符/下划线/各种括号。
//
// 比 normalizeLevelName 更狠一点点：后者只裁掉两端的括号（给「(新)User」这类修饰用），
// 而手动设置时用户/浏览器可能传「影帝（Ultimate User）」这种全角括号、括号在中间，
// 必须能对上「影帝(Ultimate User)」，否则保存会被误判成「等级不在表里」。前端 normalizeLevel 同口径。
var keepAliveNameKeyReplacer = strings.NewReplacer(
	" ", "", "　", "", "\t", "", "-", "", "_", "",
	"(", "", ")", "", "（", "", "）", "", "[", "", "]", "", "【", "", "】", "",
)

func keepAliveNameKey(value string) string {
	return keepAliveNameKeyReplacer.Replace(strings.ToLower(strings.TrimSpace(value)))
}

// List 返回站点 + 已抓取数据的合并视图。
// 参数/返回：无参数；返回按昵称排序的记录，未抓取过的站点以 pending 状态占位。
// 失败场景：站点或数据表查询失败。
// 副作用：无（只读）。
//
// 合并的意义：页面首次打开、或某站刚配置凭据还没刷新时，用户需要看到「有这个站、但还没取」，
// 而不是整行消失。
func (s *Service) List() ([]SiteData, error) {
	if s == nil || s.siteRepo == nil {
		return nil, nil
	}
	sites, err := s.siteRepo.ListSites("all")
	if err != nil {
		return nil, err
	}
	stored := map[string]repository.SiteProfile{}
	if s.profileRepo != nil {
		profiles, err := s.profileRepo.List()
		if err != nil {
			return nil, err
		}
		for _, profile := range profiles {
			stored[profile.SiteCode] = profile
		}
	}

	result := make([]SiteData, 0, len(sites))
	for _, site := range sites {
		code := strings.TrimSpace(toString(site["site"]))
		nickname := strings.TrimSpace(toString(site["nickname"]))
		if code == "" && nickname == "" {
			continue
		}
		profile, ok := stored[code]
		if !ok {
			profile = repository.SiteProfile{
				SiteCode: code,
				Nickname: nickname,
				BaseURL:  strings.TrimSpace(toString(site["base_url"])),
				Status:   "pending",
				Message:  "尚未获取",
			}
		}
		// 站点改名/换域名后，以 sites 表为真源刷新展示字段。
		profile.Nickname = firstNonEmpty(nickname, profile.Nickname)
		profile.BaseURL = firstNonEmpty(strings.TrimSpace(toString(site["base_url"])), profile.BaseURL)
		// 没配 Cookie / Passkey 的站点抓不到任何数据，列表里直接不展示（用户要求）：
		// 显示成「未配置凭据」只是噪音，去站点管理补上凭据后自然会出现在这里。
		if !hasCredential(site) {
			continue
		}
		// 先记下自动识别结果，再应用手动覆盖（applyKeepAliveOverride 会就地改 levels 的标记）。
		levels := parseLevelsJSON(profile.LevelsJSON)
		autoName := keepAliveName(levels)
		result = append(result, SiteData{
			SiteProfile:   profile,
			Levels:        applyKeepAliveOverride(levels, profile.KeepAliveLevel),
			KeepAliveAuto: autoName,
			Tags:          siteTagList(site["tags"]),
		})
	}
	return result, nil
}

// siteTagList 取出站点的自定义标签。
// sites 列表查询已把 tags 统一解析成 []string，这里只做类型兜底（[]any / JSON 串 / 空值），
// 并去空、按大小写不敏感去重，避免前端下拉里出现重复选项。
func siteTagList(raw any) []string {
	items := make([]string, 0, 4)
	switch typed := raw.(type) {
	case []string:
		items = append(items, typed...)
	case []any:
		for _, item := range typed {
			items = append(items, toString(item))
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil
		}
		if strings.HasPrefix(trimmed, "[") {
			var parsed []string
			if json.Unmarshal([]byte(trimmed), &parsed) == nil {
				items = append(items, parsed...)
			}
		} else {
			items = append(items, trimmed)
		}
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

// keepAliveName 返回等级表里带保号标记的等级名（没有则空串）。
// ⚠️ 必须在 applyKeepAliveOverride 之前调用，否则拿到的就是被手动值覆盖后的结果。
func keepAliveName(levels []UserLevel) string {
	for _, level := range levels {
		if level.KeepAlive {
			return level.Name
		}
	}
	return ""
}

// SetKeepAliveLevel 保存用户手动指定的保号等级（levelName 为空表示恢复站点权限自动识别）。
// 参数/返回：siteCode 支持站点代码或昵称；levelName 必须是该站等级表里的等级名；返回 error。
// 失败场景：站点没有抓取数据（ErrProfileMissing）、等级名不在等级表里（ErrLevelUnknown）、写库失败。
// 副作用：只更新 site_profiles.keep_alive_level 一列。
//
// 为什么需要手动指定：站点 FAQ 的写法千奇百怪（有的只在权限里写「保留帐号」、有的写「Y 及以上封存后
// 不删除」、有的干脆不写），自动识别不可能覆盖全部；用户比解析规则更清楚自己站点到底哪一级保号。
func (s *Service) SetKeepAliveLevel(siteCode, levelName string) error {
	if s == nil || s.profileRepo == nil {
		return fmt.Errorf("站点数据服务未初始化")
	}
	code := strings.TrimSpace(siteCode)
	if code == "" {
		return ErrProfileMissing
	}
	profile, err := s.profileRepo.GetBySiteCode(code)
	if err != nil {
		return err
	}
	if profile.ID == 0 {
		return ErrProfileMissing
	}
	levels := parseLevelsJSON(profile.LevelsJSON)
	name := strings.TrimSpace(levelName)
	if name != "" && !levelNameExists(levels, name) {
		return ErrLevelUnknown
	}
	return s.profileRepo.UpdateKeepAliveLevel(code, name)
}

// Refresh 抓取指定站点（为空表示全部）的账号数据并落库。
// 参数/返回：ctx 控制整体超时；siteCodes 支持站点代码或站点昵称；返回刷新后的合并视图。
// 失败场景：站点列表读取失败、写入失败；单站抓取失败只记在该站 Status/Message 上，不影响整体。
// 副作用：向多个站点发起 HTTP 请求，并写入 site_profiles 表。
func (s *Service) Refresh(ctx context.Context, siteCodes []string) ([]SiteData, error) {
	if s == nil || s.siteRepo == nil {
		return nil, fmt.Errorf("站点数据服务未初始化")
	}
	sites, err := s.siteRepo.ListSites("all")
	if err != nil {
		return nil, err
	}

	wanted := map[string]struct{}{}
	for _, code := range siteCodes {
		code = strings.TrimSpace(code)
		if code != "" {
			wanted[strings.ToLower(code)] = struct{}{}
		}
	}

	// 上次抓到的 uid 直接带上：资料页只有 `?id=` 地址才是完整版（TTG / 蝶粉 / 杜比 等皮肤
	// 不带 id 只渲染页头，等级与资料表全缺），带上能省掉每站 1~2 次试探请求。
	knownUserIDs := map[string]string{}
	if s.profileRepo != nil {
		if profiles, listErr := s.profileRepo.List(); listErr == nil {
			for _, item := range profiles {
				if id := strings.TrimSpace(item.UserID); id != "" {
					knownUserIDs[strings.ToLower(strings.TrimSpace(item.SiteCode))] = id
				}
			}
		}
	}

	targets := make([]SiteTarget, 0, len(sites))
	for _, site := range sites {
		code := strings.TrimSpace(toString(site["site"]))
		nickname := strings.TrimSpace(toString(site["nickname"]))
		if len(wanted) > 0 {
			if _, ok := wanted[strings.ToLower(code)]; !ok {
				if _, ok := wanted[strings.ToLower(nickname)]; !ok {
					continue
				}
			}
		}
		target := SiteTarget{
			SiteCode: code,
			Nickname: nickname,
			BaseURL:  strings.TrimSpace(toString(site["base_url"])),
			Cookie:   strings.TrimSpace(toString(site["cookie"])),
			Passkey:  strings.TrimSpace(toString(site["passkey"])),
			UserID:   knownUserIDs[strings.ToLower(code)],
		}
		if !hasCredential(site) {
			// 无凭据站点不发起请求，也不落库：这类站点已不再出现在列表里，
			// 再写一条 skip 记录只会让库和页面看到的状态不一致。
			continue
		}
		targets = append(targets, target)
	}

	start := time.Now()
	logx.Infof(logModule, "开始刷新站点数据 count=%d", len(targets))

	// 等级体系变化很慢（FAQ/rules 页），7 天内抓到的直接复用，避免每次刷新都多打两次请求。
	cachedLevels := s.reusableLevels()

	sem := make(chan struct{}, refreshConcurrency)
	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		go func(target SiteTarget) {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					logx.Warnf(logModule, "抓取站点数据 panic site=%s err=%v", target.Nickname, recovered)
					s.save(target, Profile{}, "fail", fmt.Sprintf("内部错误: %v", recovered))
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()

			siteCtx, cancel := context.WithTimeout(ctx, fetchTimeoutEach)
			defer cancel()
			profile, err := fetchProfile(siteCtx, target, cachedLevels[target.SiteCode])
			if err != nil {
				logx.Warnf(logModule, "站点数据获取失败 site=%s err=%v", target.Nickname, err)
				s.save(target, Profile{Levels: cachedLevels[target.SiteCode]}, "fail", err.Error())
				return
			}
			s.save(target, profile, "ok", "")
		}(target)
	}
	wg.Wait()

	logx.Infof(logModule, "站点数据刷新完成 count=%d cost=%s", len(targets), time.Since(start).Round(time.Millisecond))
	return s.List()
}

// fetchProfile 按站点类型选择抓取通道。
func fetchProfile(ctx context.Context, target SiteTarget, cachedLevels []UserLevel) (Profile, error) {
	if endpoint, ok := detectJSONChannel(target.SiteCode, target.BaseURL); ok {
		return fetchJSONProfile(ctx, target, endpoint)
	}
	return fetchNexusPHPProfile(ctx, target, cachedLevels)
}

// fetchNexusPHPProfile 抓取 NexusPHP 站点（userdetails.php + 做种列表兜底）。
//
// 流程：
//  1. 先访问 index.php 拿当前登录用户 ID（资料页需要 ?id=，部分站不带 id 会 302 到首页）；
//  2. 访问 userdetails.php?id=<uid>，解析资料表键值对与等级图标；
//  3. 资料页没有做种/下载中数量时，退回 getusertorrentlistajax.php 计数。
//
// 失败场景：Cookie 失效（首页无用户链接）、资料页打不开、上传/下载量都解析不到。
// 副作用：向站点发起 2~4 次 HTTP 请求。
func fetchNexusPHPProfile(ctx context.Context, target SiteTarget, cachedLevels []UserLevel) (Profile, error) {
	base := normalizeBaseURL(target.BaseURL)
	if base == "" {
		return Profile{}, fmt.Errorf("站点缺少域名（base_url）")
	}
	cookie := strings.TrimSpace(target.Cookie)
	if cookie == "" {
		cookie = strings.TrimSpace(target.Passkey)
	}
	if cookie == "" {
		return Profile{}, fmt.Errorf("未填写 Cookie")
	}
	headers := map[string]string{
		"Cookie": cookie,
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	}

	userID := strings.TrimSpace(target.UserID)
	username := ""
	if userID == "" {
		indexBody, status, err := doRequest(ctx, http.MethodGet, base+"/index.php", headers, nil)
		if err != nil {
			return Profile{}, err
		}
		if status >= 400 {
			return Profile{}, fmt.Errorf("站点首页返回 HTTP %d", status)
		}
		userID, username = extractUserFromIndex(string(indexBody))
	}

	// 资料页：优先带 id；部分站（如城市 hdcity）用 URL 重写、首页里拿不到自己的 id，
	// 但允许不带 id 直接打开（NexusPHP 会渲染当前登录用户），因此逐个候选地址试到有数据为止。
	detail, err := fetchDetailDocument(ctx, base, headers, userID, target.SiteCode)
	if err != nil {
		return Profile{}, err
	}
	profile := detail.Profile
	profile.UserID = userID
	profile.Username = firstNonEmpty(username, profile.Username)
	cells := detail.Cells

	if !detail.hasAccountData() {
		return profile, fmt.Errorf("资料页未解析到账号数据（Cookie 可能已失效，或页面结构已变更）")
	}
	// 流量两项都为空但其它字段有值：说明站点又换了展示结构，日志里带上实际标签便于定位。
	if profile.Uploaded == 0 && profile.Downloaded == 0 {
		logx.Warnf(logModule, "资料页未解析出流量 site=%s cells=%d labels=%s", target.Nickname, cells.size(), cellLabels(cells))
	}

	// 资料页没给做种/下载中数量时，用站点内置的种子列表接口计数。
	// 接口必须带 userid，取不到自己 uid 的站点（如城市）会跳过，避免传空 id 拉到别人的列表。
	if userID != "" {
		if !detail.SeedingParsed {
			profile.Seeding = countUserTorrents(ctx, base, headers, userID, "seeding")
		}
		if !detail.LeechingParsed {
			profile.Leeching = countUserTorrents(ctx, base, headers, userID, "leeching")
		}
	}
	// 等级体系：FAQ 给权限、rules 给门槛，7 天内抓过的直接复用缓存。
	if len(cachedLevels) > 0 {
		profile.Levels = cachedLevels
	} else {
		profile.Levels = fetchSiteLevels(ctx, target.SiteCode, base, headers)
	}
	return profile, nil
}

// fetchDetailDocument 依次尝试几种资料页地址，返回第一个能解析出账号数据的页面。
// 参数/返回：base 为站点根地址，headers 为带 Cookie 的请求头，userID 可为空，
// siteCode 仅用于排查时给落盘文件命名；返回解析结果。
// 失败场景：所有候选地址都请求失败时返回错误；能打开但解析不到数据时返回空结果（由调用方判失败）。
// 副作用：向站点发起 1~3 次 GET 请求；若开启了页面落盘开关还会写文件。
func fetchDetailDocument(ctx context.Context, base string, headers map[string]string, userID, siteCode string) (parsedDetail, error) {
	candidates := make([]string, 0, 4)
	if userID != "" {
		candidates = append(candidates, "/userdetails.php?id="+url.QueryEscape(userID))
	}
	// 不带 id 的两种写法：URL 重写的站点只有前者，老皮肤只有后者。
	candidates = append(candidates, "/userdetails.php", "/userdetails")

	tried := make(map[string]struct{}, len(candidates)+1)
	best := parsedDetail{}
	bestScore := -1
	var lastErr error

	// 逐个候选试到「信息够全」为止（不是第一个有数据的就收工）：
	// TTG 这类皮肤不带 id 只渲染页头，流量能解析出来但资料表整块缺失（等级、加入日期都没有），
	// 早退会把它当成正常结果，等级就一直是空的。
	for index := 0; index < len(candidates); index++ {
		path := candidates[index]
		if _, visited := tried[path]; visited {
			continue
		}
		tried[path] = struct{}{}

		body, status, err := doRequest(ctx, http.MethodGet, base+path, headers, nil)
		if err != nil {
			lastErr = err
			continue
		}
		dumpDebugBody(siteCode, path, body)
		if status >= 400 {
			lastErr = fmt.Errorf("资料页返回 HTTP %d", status)
			continue
		}
		parsed := parseDetailDoc(string(body))
		if score := detailScore(parsed); score > bestScore {
			best = parsed
			bestScore = score
		}
		// 首页没给出 uid 时，从不带 id 的页面里补一个：拿到后立刻带 id 再抓一次，
		// 完整资料表（等级、做种数）都在那个地址上。
		if !strings.Contains(path, "?id=") {
			if discovered, _ := extractUserFromIndex(string(body)); discovered != "" {
				idPath := "/userdetails.php?id=" + url.QueryEscape(discovered)
				if _, visited := tried[idPath]; !visited {
					candidates = append(candidates, idPath)
				}
			}
		}
		if bestScore >= detailCompleteScore {
			break
		}
	}
	if bestScore >= 0 {
		// 页面能打开，即使没解析出数据也交回调用方：按「Cookie 失效 / 结构变更」报错，
		// 保留解析细节（资料表标签）便于排错。
		return best, nil
	}
	if lastErr != nil {
		return parsedDetail{}, lastErr
	}
	return parsedDetail{}, fmt.Errorf("资料页请求失败")
}

// detailCompleteScore 是「这份资料页信息够全」的阈值：有等级 + 有流量就算够。
const detailCompleteScore = 120

// detailScore 粗略衡量资料页的信息完整度，用于在多个候选地址里挑最好的那份。
// 等级权重最高 —— 它只出现在完整资料表里，是「带 id 页面」最可靠的标志。
// 参数/返回：parsed 为解析结果；返回分数（越大越完整）。
// 失败场景：无。
// 副作用：无。
func detailScore(parsed parsedDetail) int {
	score := 0
	if parsed.Profile.UserClass != "" {
		score += 100
	}
	if parsed.Profile.Uploaded > 0 || parsed.Profile.Downloaded > 0 {
		score += 20
	}
	if parsed.SeedingParsed {
		score += 5
	}
	if parsed.Profile.JoinDate != "" {
		score += 3
	}
	score += min(parsed.Cells.size(), 20)
	return score
}

// fetchSiteLevels 抓取站点的等级体系（faq.php 等级表 + rules.php 升级条件）。
// 参数/返回：siteCode 用于识别是否有内置等级表（如青蛙），base 为站点根地址，
// headers 为带 Cookie 的请求头；返回等级列表（至少是内置标准表）。
// 失败场景：页面打不开或结构不符时退回内置表，不阻断账号数据。
// 副作用：向站点发起最多 2 次 GET 请求（内置等级表的站点不发请求）。
func fetchSiteLevels(ctx context.Context, siteCode, base string, headers map[string]string) []UserLevel {
	// 有些站的 FAQ 已经不是 HTML 表格（青蛙直接 302 到 Nuxt Wiki，正文由前端组件渲染），
	// 抓下来只能回落到标准 10 级，等级/保号全错，这类站点按内置表直出。
	if builtin, ok := builtinSiteLevels(siteCode); ok {
		return builtin
	}
	// 新版 NexusPHP 把等级表搬到独立的 classes.php（北洋园 tjupt 的页头「等级」就指向它），
	// 那里逐级列出「当前值/目标值」的完整门槛；FAQ 里的「提升和降级」还是它自己标注的
	// 「之前的规则」，且表格只剩权限列，解析出来 requirements 全空。所以先试 classes.php，
	// 解不出等级（多数站没有这个页面，直接 404）再走原来的 FAQ 解析。
	if classesHTML := fetchPageHTML(ctx, base+"/classes.php", headers); classesHTML != "" {
		if levels := parseClassesPage(classesHTML); len(levels) >= 3 {
			return levels
		}
	}
	// FAQ 必须按 HTML 解析：等级表是 <table> 三列结构，站点自定义等级（VIP/发布员/版主等）
	// 只存在于表格的 class 名里，转成纯文本后只剩标准 10 级能对上。
	faqHTML := fetchPageHTML(ctx, base+"/faq.php", headers)
	rulesText := fetchPageText(ctx, base+"/rules.php", headers)
	levels := buildUserLevelsFromFAQ(faqHTML, rulesText)
	// 等级表能抓、门槛抓不到的站点（PTFans 的门槛贴在置顶种子的截图里）用内置值补齐。
	applyBuiltinRequirementOverrides(siteCode, levels)
	return levels
}

// fetchPageHTML 抓取一个页面并返回原始 HTML；失败返回空串。
func fetchPageHTML(ctx context.Context, rawURL string, headers map[string]string) string {
	body, status, err := doRequest(ctx, http.MethodGet, rawURL, headers, nil)
	if err != nil || status < 200 || status >= 300 {
		return ""
	}
	return string(body)
}

// fetchPageText 抓取一个页面并转成保留换行的纯文本；失败返回空串。
func fetchPageText(ctx context.Context, rawURL string, headers map[string]string) string {
	body, status, err := doRequest(ctx, http.MethodGet, rawURL, headers, nil)
	if err != nil || status < 200 || status >= 300 {
		return ""
	}
	return htmlToTextWithBreaks(string(body))
}

// levelCacheDays 为等级体系的复用天数（FAQ/rules 属于低频变动内容）。
const levelCacheDays = 7

// reusableLevels 返回 7 天内抓到的等级体系（按站点代码索引），供刷新时跳过重复抓取。
func (s *Service) reusableLevels() map[string][]UserLevel {
	if s == nil || s.profileRepo == nil {
		return nil
	}
	profiles, err := s.profileRepo.List()
	if err != nil {
		return nil
	}
	result := map[string][]UserLevel{}
	for _, profile := range profiles {
		if strings.TrimSpace(profile.LevelsJSON) == "" || !levelCacheFresh(profile.FetchedAt) {
			continue
		}
		// 只复用当前版本的缓存：解析口径升级过（10 级标准表 → 站点真实等级表）后，
		// 旧缓存必须失效重抓，否则用户刷新后看到的还是那 10 个标准等级。
		levels, version := decodeLevelsCache(profile.LevelsJSON)
		if version == levelsSchemaVersion && len(levels) > 0 {
			result[profile.SiteCode] = levels
		}
	}
	return result
}

// levelCacheFresh 判断上次抓取时间是否仍在缓存窗口内。
func levelCacheFresh(fetchedAt string) bool {
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(fetchedAt), time.Local)
	if err != nil {
		return false
	}
	return time.Since(parsed) < levelCacheDays*24*time.Hour
}

// parseLevelsJSON 把库里的等级体系 JSON 展开成结构体；解析失败返回 nil。
// 兼容旧格式（裸数组）：旧数据只是展示用，换代时不需要再回写。
func parseLevelsJSON(raw string) []UserLevel {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	levels, _ := decodeLevelsCache(raw)
	return levels
}

// countUserTorrents 统计当前账号「做种中 / 下载中」的种子数。
//
// 两个接口名都要试：标准 NexusPHP 是 `getusertorrentlistajax.php`（无下划线），
// 梓喵（azusa）这类改过皮肤的站用 `getusertorrentlist_ajax.php`，前者直接 404。
// 后者返回的是摘要（`<b>100</b> 条记录 | 总大小：300.19 GB`）而不是种子行，
// 所以两种格式都解析；「没有记录」视为明确的 0 并结束探测。
//
// 失败时返回 0（不阻断整体结果）。
func countUserTorrents(ctx context.Context, base string, headers map[string]string, userID, kind string) int64 {
	paths := []string{"/getusertorrentlistajax.php", "/getusertorrentlist_ajax.php"}
	for _, path := range paths {
		listURL := base + path + "?userid=" + url.QueryEscape(userID) + "&type=" + url.QueryEscape(kind)
		body, status, err := doRequest(ctx, http.MethodGet, listURL, headers, nil)
		if err != nil || status < 200 || status >= 300 {
			continue
		}
		text := string(body)
		if count := countTorrentRows(text); count > 0 {
			return count
		}
		if count, ok := parseTorrentListSummary(text); ok {
			return count
		}
		if strings.Contains(text, "没有记录") || strings.Contains(text, "沒有記錄") {
			return 0
		}
	}
	return 0
}

// save 把抓取结果写入 site_profiles（失败只记日志，不影响其它站点）。
func (s *Service) save(target SiteTarget, profile Profile, status, message string) {
	if s == nil || s.profileRepo == nil {
		return
	}
	levelsJSON := encodeLevelsCache(profile.Levels)
	record := repository.SiteProfile{
		SiteCode:   target.SiteCode,
		Nickname:   target.Nickname,
		BaseURL:    target.BaseURL,
		UserID:     firstNonEmpty(profile.UserID, target.UserID),
		Username:   profile.Username,
		UserClass:  profile.UserClass,
		Uploaded:   profile.Uploaded,
		Downloaded: profile.Downloaded,
		Ratio:      profile.Ratio,
		Bonus:      profile.Bonus,
		SeedBonus:  profile.SeedBonus,
		Seeding:    profile.Seeding,
		Leeching:   profile.Leeching,
		Invites:    profile.Invites,
		JoinDate:   profile.JoinDate,
		Source:     profile.Source,
		Status:     status,
		Message:    firstNonEmpty(message, profile.Notice),
		LevelsJSON: levelsJSON,
		FetchedAt:  time.Now().Format("2006-01-02 15:04:05"),
	}
	if err := s.profileRepo.Upsert(record); err != nil {
		logx.Warnf(logModule, "写入站点数据失败 site=%s err=%v", target.Nickname, err)
		return
	}
	// 只有抓取成功才写当天快照：失败时各项都是空值，写进去会把增量算成负数。
	if status == "ok" {
		s.recordDailySample(target, profile)
	}
}

// hasCredential 判断站点是否填了 Cookie 或 Passkey（switch 覆盖全类型，兼容 int8/bool）。
func hasCredential(site map[string]any) bool {
	if toIntAny(site["has_cookie"]) > 0 || toIntAny(site["has_passkey"]) > 0 {
		return true
	}
	return strings.TrimSpace(toString(site["cookie"])) != "" || strings.TrimSpace(toString(site["passkey"])) != ""
}

// toIntAny 把任意数值/布尔/字符串转成 int（0 表示假）。
func toIntAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int8:
		return int(typed)
	case int16:
		return int(typed)
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case uint:
		return int(typed)
	case uint8:
		return int(typed)
	case uint64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	case bool:
		if typed {
			return 1
		}
		return 0
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "1" || strings.EqualFold(trimmed, "true") {
			return 1
		}
		return 0
	default:
		return 0
	}
}

// toString 安全取字符串字段。
func toString(value any) string {
	if typed, ok := value.(string); ok {
		return typed
	}
	return ""
}

// normalizeBaseURL 归一化站点根地址：补协议、去尾部斜杠。
func normalizeBaseURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimRight(trimmed, "/")
	if !strings.HasPrefix(strings.ToLower(trimmed), "http://") && !strings.HasPrefix(strings.ToLower(trimmed), "https://") {
		trimmed = "https://" + trimmed
	}
	return trimmed
}
