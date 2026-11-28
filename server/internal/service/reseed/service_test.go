package reseed

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/repository"
	acquirefetch "github.com/pt-nexus/server/internal/service/acquire/fetch"
)

// TestFilterCredentialSites 校验目标站只保留已设置 Cookie 或 Passkey 的站点。
func TestFilterCredentialSites(t *testing.T) {
	sites := []repository.ReseedSiteRow{
		{Nickname: "有Cookie", HasCookie: true},
		{Nickname: "有Passkey", HasPasskey: true},
		{Nickname: "都有", HasCookie: true, HasPasskey: true},
		{Nickname: "都没有"},
	}
	kept, hidden := filterCredentialSites(sites)
	if hidden != 1 {
		t.Fatalf("期望剔除 1 个无凭据站点，实际 %d", hidden)
	}
	if len(kept) != 3 {
		t.Fatalf("期望保留 3 个站点，实际 %d: %+v", len(kept), kept)
	}
	for _, site := range kept {
		if !site.HasCookie && !site.HasPasskey {
			t.Fatalf("保留列表里不应出现无凭据站点: %+v", site)
		}
	}
}

// TestGroupCandidates 校验候选种子组的过滤与归并规则。
// 覆盖：体积门限、不存在状态、name+size 去重、真实做种行优先、hash 去重与上限。
func TestGroupCandidates(t *testing.T) {
	const bigSize = int64(5 * 1024 * 1024 * 1024)
	const smallSize = int64(1024)

	rows := []repository.TorrentRecord{
		// 占位行在前（路径为空、状态未做种），真实做种行在后，应覆盖代表字段
		{Name: "Movie.A", Size: bigSize, SavePath: "", State: "未做种", Sites: "馒头", Hash: "BBB", Downloader: "qb1"},
		{Name: "Movie.A", Size: bigSize, SavePath: "/data/a", State: "做种中", Sites: "天空", Hash: "AAA", Downloader: "qb1"},
		// 体积过小，忽略
		{Name: "Small.B", Size: smallSize, SavePath: "/data/b", State: "做种中", Hash: "CCC", Downloader: "qb1"},
		// 状态为不存在，忽略
		{Name: "Gone.C", Size: bigSize, SavePath: "/data/c", State: "不存在", Hash: "DDD", Downloader: "qb1"},
		// 空 hash，忽略
		{Name: "NoHash.E", Size: bigSize, SavePath: "/data/a", State: "做种中", Hash: "", Downloader: "qb1"},
	}

	groups := groupCandidates(rows, Params{Limit: 100})
	if len(groups) != 1 {
		t.Fatalf("期望 1 组，实际 %d 组: %+v", len(groups), groups)
	}
	group := groups[0]
	if group.Name != "Movie.A" {
		t.Fatalf("组名错误: %s", group.Name)
	}
	if group.SavePath != "/data/a" {
		t.Fatalf("保存路径应取真实做种行，实际 %s", group.SavePath)
	}
	if group.SourceSite != "天空" {
		t.Fatalf("站点应取真实做种行，实际 %s", group.SourceSite)
	}
	if len(group.Hashes) != 2 || group.Hashes[0] != "aaa" || group.Hashes[1] != "bbb" {
		t.Fatalf("hash 顺序应为真实行优先并转小写，实际 %+v", group.Hashes)
	}
}

// TestGroupCandidatesPathFilter 校验保存路径过滤。
func TestGroupCandidatesPathFilter(t *testing.T) {
	const bigSize = int64(5 * 1024 * 1024 * 1024)
	rows := []repository.TorrentRecord{
		{Name: "Keep.1", Size: bigSize, SavePath: "/data/a", State: "做种中", Hash: "H1", Downloader: "qb1"},
		{Name: "Drop.2", Size: bigSize, SavePath: "/data/other", State: "做种中", Hash: "H2", Downloader: "qb1"},
	}

	groups := groupCandidates(rows, Params{Paths: []string{"/data/a"}, Limit: 100})
	if len(groups) != 1 || groups[0].Name != "Keep.1" {
		t.Fatalf("期望只剩 Keep.1，实际 %+v", groups)
	}

	all := groupCandidates(rows, Params{Limit: 100})
	if len(all) != 2 {
		t.Fatalf("不传路径时应保留全部 2 组，实际 %d", len(all))
	}
}

// TestGroupCandidatesDownloaderFilter 校验下载器筛选与数量上限。
func TestGroupCandidatesDownloaderFilter(t *testing.T) {
	const bigSize = int64(5 * 1024 * 1024 * 1024)
	rows := []repository.TorrentRecord{
		{Name: "A.1", Size: bigSize, SavePath: "/data/a", State: "做种中", Hash: "H1", Downloader: "qb1"},
		{Name: "A.2", Size: bigSize, SavePath: "/data/a", State: "做种中", Hash: "H2", Downloader: "qb2"},
		{Name: "A.3", Size: bigSize, SavePath: "/data/a", State: "做种中", Hash: "H3", Downloader: "qb1"},
	}

	groups := groupCandidates(rows, Params{DownloaderID: "qb1", Limit: 100})
	if len(groups) != 2 {
		t.Fatalf("期望按下载器筛出 2 组，实际 %d", len(groups))
	}

	limited := groupCandidates(rows, Params{Limit: 1})
	if len(limited) != 1 {
		t.Fatalf("期望被 limit 截断为 1 组，实际 %d", len(limited))
	}
}

// TestGroupCandidatesPinnedHashes 校验「指定种子试跑」：只取命中 hash，并放开体积下限与数量上限。
func TestGroupCandidatesPinnedHashes(t *testing.T) {
	const bigSize = int64(5 * 1024 * 1024 * 1024)
	const tinySize = int64(20 * 1024 * 1024)
	rows := []repository.TorrentRecord{
		{Name: "Big.1", Size: bigSize, SavePath: "/data/a", State: "做种中", Hash: "AAAA", Downloader: "qb1"},
		{Name: "Big.2", Size: bigSize, SavePath: "/data/a", State: "做种中", Hash: "BBBB", Downloader: "qb1"},
		// 体积低于 198MB 的种子显式指定时也要能跑
		{Name: "Tiny.3", Size: tinySize, SavePath: "/data/a", State: "做种中", Hash: "CCCC", Downloader: "qb1"},
	}

	pinned := groupCandidates(rows, Params{Hashes: []string{"bbbb"}, Limit: 1})
	if len(pinned) != 1 || pinned[0].Name != "Big.2" {
		t.Fatalf("期望只保留指定 hash 的 Big.2，实际 %+v", pinned)
	}

	tiny := groupCandidates(rows, Params{Hashes: []string{"CCCC"}, Limit: 1})
	if len(tiny) != 1 || tiny[0].Name != "Tiny.3" {
		t.Fatalf("指定小体积种子时应忽略体积下限，实际 %+v", tiny)
	}

	none := groupCandidates(rows, Params{Hashes: []string{"ZZZZ"}, Limit: 1})
	if len(none) != 0 {
		t.Fatalf("未命中的 hash 应返回空，实际 %+v", none)
	}
}

// TestEffectiveTags 校验「按目标站名打标签」：与手动标签去重叠加，未开启时不加站名。
// 站点标签口径与设置中心一致，必须是「站点/<站名>」而不是裸站名。
func TestEffectiveTags(t *testing.T) {
	manual := Params{Tags: []string{"辅种", " "}}
	if tags := effectiveTags(manual, "财神"); len(tags) != 1 || tags[0] != "辅种" {
		t.Fatalf("未开启站名标签时应只有手动标签，实际 %+v", tags)
	}

	bySite := Params{Tags: []string{"辅种", "站点/财神"}, TagBySite: true}
	tags := effectiveTags(bySite, "财神")
	if len(tags) != 2 || tags[0] != "辅种" || tags[1] != "站点/财神" {
		t.Fatalf("站名标签应与手动标签去重叠加为「站点/财神」，实际 %+v", tags)
	}

	onlySite := Params{TagBySite: true}
	if got := effectiveTags(onlySite, "天空"); len(got) != 1 || got[0] != "站点/天空" {
		t.Fatalf("只开站名标签时应只有「站点/天空」，实际 %+v", got)
	}

	// 空站名不应产生「站点/」这种废标签
	if got := effectiveTags(onlySite, "  "); len(got) != 0 {
		t.Fatalf("站名为空时不应加标签，实际 %+v", got)
	}
}

// TestEffectiveTagsSiteNamePlaceholder 校验标签里的「站点/{站点名称}」占位符按目标站展开，
// 这样默认标签也能存「站点/站名」这种动态标签。
func TestEffectiveTagsSiteNamePlaceholder(t *testing.T) {
	params := Params{Tags: []string{siteNameTagTemplate, "辅种", siteNameTagTemplate}}
	tags := effectiveTags(params, " 财神 ")
	if len(tags) != 2 || tags[0] != "站点/财神" || tags[1] != "辅种" {
		t.Fatalf("占位符应展开为「站点/财神」并与手动标签去重，实际 %+v", tags)
	}

	// 占位符与勾选框同时开启时不能重复写入同一个站名标签。
	both := Params{Tags: []string{siteNameTagTemplate}, TagBySite: true}
	if got := effectiveTags(both, "天空"); len(got) != 1 || got[0] != "站点/天空" {
		t.Fatalf("占位符与勾选框应合并为一条「站点/天空」，实际 %+v", got)
	}

	// 站名未知时丢弃占位符标签，但保留同一组里的静态标签。
	if got := effectiveTags(params, ""); len(got) != 1 || got[0] != "辅种" {
		t.Fatalf("站名为空时应丢弃占位符标签、保留静态标签，实际 %+v", got)
	}

	// 前后缀混写也能替换，例如「辅种-{站点名称}」。
	mixed := Params{Tags: []string{"辅种-{站点名称}"}}
	if got := effectiveTags(mixed, "财神"); len(got) != 1 || got[0] != "辅种-财神" {
		t.Fatalf("前缀混写的占位符应被替换，实际 %+v", got)
	}
}

// TestRecordTags 校验记录里只在真正写入下载器时才带标签。
func TestRecordTags(t *testing.T) {
	params := Params{Tags: []string{"辅种"}, TagBySite: true}
	if got := recordTags(StatusAdded, params, "财神"); got != "辅种,站点/财神" {
		t.Fatalf("新增记录应带完整标签，实际 %q", got)
	}
	if got := recordTags(StatusAuthFailed, params, "财神"); got != "" {
		t.Fatalf("失败记录不应带标签，实际 %q", got)
	}
}

// TestClassifyDownloadError 校验 Cookie/Passkey 问题能被单独识别成「凭据失效」。
func TestClassifyDownloadError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus string
	}{
		{"cookie 失效", acquirefetch.ErrSourceCookieExpired, StatusAuthFailed},
		{"passkey 无效", errors.New("下载内容不是 torrent 文件（站点返回：Passkey 无效，请更新站点配置中的 Passkey）"), StatusAuthFailed},
		{"未登录", errors.New("站点返回登录页，请先登录"), StatusAuthFailed},
		{"普通失败", errors.New("dial tcp: i/o timeout"), StatusFailed},
	}
	for _, item := range cases {
		status, message := classifyDownloadError(item.err)
		if status != item.wantStatus {
			t.Fatalf("%s：期望状态 %s，实际 %s（%s）", item.name, item.wantStatus, status, message)
		}
		if strings.TrimSpace(message) == "" {
			t.Fatalf("%s：失败说明不能为空", item.name)
		}
	}
}

// TestNormalizeTagList 校验默认标签清洗：去空白、忽略大小写去重、超长截断、超量截断。
func TestNormalizeTagList(t *testing.T) {
	tags := normalizeTagList([]any{" 辅种 ", "", "PT", "pt", "站点/财神", "站点/财神"})
	want := []string{"辅种", "PT", "站点/财神"}
	if len(tags) != len(want) {
		t.Fatalf("期望 %d 个标签，实际 %d 个：%+v", len(want), len(tags), tags)
	}
	for i := range want {
		if tags[i] != want[i] {
			t.Fatalf("第 %d 个标签期望 %q，实际 %q", i, want[i], tags[i])
		}
	}

	long := strings.Repeat("长", maxDefaultTagRunes+5)
	got := normalizeTagList([]string{long})
	if len(got) != 1 || len([]rune(got[0])) != maxDefaultTagRunes {
		t.Fatalf("超长标签应截断到 %d 个字符，实际 %+v", maxDefaultTagRunes, got)
	}

	many := make([]any, 0, maxDefaultTags+5)
	for i := 0; i < maxDefaultTags+5; i++ {
		many = append(many, "标签"+strconv.Itoa(i))
	}
	if got := normalizeTagList(many); len(got) != maxDefaultTags {
		t.Fatalf("标签数量应上限为 %d，实际 %d", maxDefaultTags, len(got))
	}

	if got := normalizeTagList(nil); len(got) != 0 {
		t.Fatalf("空值应返回空列表，实际 %+v", got)
	}
}

// TestDefaultTagsRoundTrip 校验默认标签的保存与读取（走真实配置管理器，落临时配置文件）。
func TestDefaultTagsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	manager, err := config.NewManager(config.RuntimePaths{ConfigFile: filepath.Join(dir, "config.json")})
	if err != nil {
		t.Fatalf("创建配置管理器失败：%v", err)
	}
	service := &Service{cfg: manager}

	if got := service.DefaultTags(); len(got) != 0 {
		t.Fatalf("初始默认标签应为空，实际 %+v", got)
	}
	if err := service.SetDefaultTags([]string{" 辅种 ", "站点/财神", "辅种"}); err != nil {
		t.Fatalf("保存默认标签失败：%v", err)
	}
	got := service.DefaultTags()
	if len(got) != 2 || got[0] != "辅种" || got[1] != "站点/财神" {
		t.Fatalf("默认标签读取结果不符合预期：%+v", got)
	}

	if err := service.SetDefaultTags(nil); err != nil {
		t.Fatalf("清空默认标签失败：%v", err)
	}
	if got := service.DefaultTags(); len(got) != 0 {
		t.Fatalf("清空后应为空，实际 %+v", got)
	}
}
