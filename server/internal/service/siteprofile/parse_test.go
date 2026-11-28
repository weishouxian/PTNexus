package siteprofile

import (
	"encoding/json"
	"strings"
	"testing"
)

// wantBytes 用解析器自身换算期望值，避免测试里手写大数字算错。
func wantBytes(text string) int64 {
	value, ok := parseSizeBytes(text)
	if !ok {
		panic("测试期望值无法解析: " + text)
	}
	return value
}

// TestParseDetailDocLegacyTable 覆盖老式 NexusPHP 皮肤：资料表全是 rowhead/rowfollow 键值格。
func TestParseDetailDocLegacyTable(t *testing.T) {
	doc := `
<table>
<tr><td class="rowhead">用户名</td><td class="rowfollow"><a href="userdetails.php?id=42">tester</a></td></tr>
<tr><td class="rowhead">上传量</td><td class="rowfollow">1.234 TB</td></tr>
<tr><td class="rowhead">下载量</td><td class="rowfollow">22.53<br />GB</td></tr>
<tr><td class="rowhead">分享率</td><td class="rowfollow">∞</td></tr>
<tr><td class="rowhead">等级</td><td class="rowfollow"><img class="userlevel" src="pic/user.gif" alt="Power User" title="Power User" /></td></tr>
<tr><td class="rowhead">魔力值</td><td class="rowfollow">12,345.6</td></tr>
<tr><td class="rowhead">在做种</td><td class="rowfollow"><a href="getusertorrentlistajax.php?userid=42&type=seeding">42</a></td></tr>
<tr><td class="rowhead">在下载</td><td class="rowfollow">3</td></tr>
<tr><td class="rowhead">邀请</td><td class="rowfollow">5</td></tr>
</table>`

	detail := parseDetailDoc(doc)
	profile := detail.Profile

	if profile.Uploaded != wantBytes("1.234 TB") {
		t.Errorf("上传量解析错误: %d", profile.Uploaded)
	}
	if profile.Downloaded != wantBytes("22.53 GB") {
		t.Errorf("下载量解析错误: %d", profile.Downloaded)
	}
	if profile.Ratio != -1 {
		t.Errorf("∞ 分享率应解析为 -1，实际 %v", profile.Ratio)
	}
	if profile.UserClass != "Power User" {
		t.Errorf("等级解析错误: %q", profile.UserClass)
	}
	if profile.Bonus != 12345.6 {
		t.Errorf("魔力值解析错误: %v", profile.Bonus)
	}
	if profile.Seeding != 42 || !detail.SeedingParsed {
		t.Errorf("做种数解析错误: %d parsed=%v", profile.Seeding, detail.SeedingParsed)
	}
	if profile.Leeching != 3 || !detail.LeechingParsed {
		t.Errorf("下载中解析错误: %d parsed=%v", profile.Leeching, detail.LeechingParsed)
	}
	if profile.Invites != 5 {
		t.Errorf("邀请数解析错误: %d", profile.Invites)
	}
}

// TestParseDetailDocPageHeader 覆盖页头状态条写法（彩虹岛 / 我堡）：
// 上传/下载/分享率不在资料表里，而是「<font>分享率：</font> 17.889 上传量： 160.389 TB」。
func TestParseDetailDocPageHeader(t *testing.T) {
	doc := `<table><tr><td class="bottom" align="right">
<a href='hnr.php?id=1'><font class = 'color_bonus'>H&amp;R:</font></a>0<br />
<font class="color_ratio">分享率：</font> 17.889  <font class='color_uploaded'>上传量：</font> 160.389 TB<font class='color_downloaded'> 下载量：</font> 8.966 TB  <font class='color_active'>当前活动：</font> <img class="arrowup" alt="Torrents seeding" title="当前做种" src="pic/trans.gif" />340  <img class="arrowdown" alt="Torrents leeching" title="当前下载" src="pic/trans.gif" />0
<font class = 'color_bonus'>魔力值 </font>[<a href="mybonus.php">使用</a>]: 1,593,841.1  <font class="color_ratio">做种积分: </font>1014461.1
</td></tr></table>`

	detail := parseDetailDoc(doc)
	profile := detail.Profile

	if profile.Uploaded != wantBytes("160.389 TB") {
		t.Errorf("页头上传量解析错误: %d", profile.Uploaded)
	}
	if profile.Downloaded != wantBytes("8.966 TB") {
		t.Errorf("页头下载量解析错误: %d", profile.Downloaded)
	}
	if profile.Ratio < 17.88 || profile.Ratio > 17.89 {
		t.Errorf("页头分享率解析错误: %v", profile.Ratio)
	}
	if profile.Seeding != 340 || !detail.SeedingParsed {
		t.Errorf("页头当前做种解析错误: %d parsed=%v", profile.Seeding, detail.SeedingParsed)
	}
	if profile.Bonus != 1593841.1 {
		t.Errorf("魔力值解析错误: %v", profile.Bonus)
	}
	if profile.SeedBonus != 1014461.1 {
		t.Errorf("做种积分解析错误: %v", profile.SeedBonus)
	}
}

// TestParseDetailDocTransportRow 覆盖资料表「传输」行（天空）：
// 一个格子里塞「上传量 : 21.374 TB / 下载量 : 0.00 KB」，做种行是 [显示/隐藏] 折叠开关。
func TestParseDetailDocTransportRow(t *testing.T) {
	doc := `<table>
<tr><td class="rowhead nowrap">传输</td><td class="rowfollow" align="left">上传量 : <b>21.374 TB</b><br />下载量 : <b>0.00 KB</b><br />上传量(本月) : <b>2.467 TB</b><br />下载量(本月) : <b>0.00 KB</b></td></tr>
<tr><td class="rowhead nowrap">做种率</td><td class="rowfollow">做种/下载时间比率 : 27.893</td></tr>
<tr><td class="rowhead nowrap">魔力值</td><td class="rowfollow">672460.0</td></tr>
<tr><td class="rowhead nowrap">当前做种</td><td class="rowfollow">[显示/隐藏]</td></tr>
<tr><td class="rowhead nowrap">当前下载</td><td class="rowfollow">[显示/隐藏]</td></tr>
</table>`

	detail := parseDetailDoc(doc)
	profile := detail.Profile

	if profile.Uploaded != wantBytes("21.374 TB") {
		t.Errorf("「传输」行上传量解析错误: %d", profile.Uploaded)
	}
	if profile.Downloaded != 0 {
		t.Errorf("下载量应为 0，实际 %d", profile.Downloaded)
	}
	// 下载为 0 且有上传量 ⇒ 站点显示 ∞，用 -1 表示。
	if profile.Ratio != -1 {
		t.Errorf("无下载量时分享率应为 -1（∞），实际 %v", profile.Ratio)
	}
	if profile.Bonus != 672460 {
		t.Errorf("魔力值解析错误: %v", profile.Bonus)
	}
	// [显示/隐藏] 取不到数字，必须标记为「未解析」，让上层继续走种子列表计数兜底。
	if detail.SeedingParsed || detail.LeechingParsed {
		t.Errorf("[显示/隐藏] 不应被当成做种/下载中数字: seeding=%v leeching=%v", detail.SeedingParsed, detail.LeechingParsed)
	}
	if !detail.hasAccountData() {
		t.Errorf("有上传量时应判定为解析成功")
	}
}

// TestParseDetailDocTailwindSkin 覆盖 Tailwind 自定义皮肤（憨憨）：标签和值分处两个 span。
func TestParseDetailDocTailwindSkin(t *testing.T) {
	doc := `<div class="grid grid-cols-3 gap-4">
<div><span class="font-bold">上传量：</span><span>28.329 TB</span></div>
<div><span class="font-bold">下载量：</span><span>6.826 TB</span></div>
<div><span class="font-bold">分享率：</span><span>4.15</span></div>
<div><span class="font-bold">做种积分：</span><span>952,422.5</span></div>
</div>`

	profile := parseDetailDoc(doc).Profile

	if profile.Uploaded != wantBytes("28.329 TB") {
		t.Errorf("Tailwind 皮肤上传量解析错误: %d", profile.Uploaded)
	}
	if profile.Downloaded != wantBytes("6.826 TB") {
		t.Errorf("Tailwind 皮肤下载量解析错误: %d", profile.Downloaded)
	}
	if profile.Ratio != 4.15 {
		t.Errorf("Tailwind 皮肤分享率解析错误: %v", profile.Ratio)
	}
	if profile.SeedBonus != 952422.5 {
		t.Errorf("Tailwind 皮肤做种积分解析错误: %v", profile.SeedBonus)
	}
}

// TestParseDetailDocAttributeStats 覆盖把数字只写在 data-* 属性里的皮肤（人人）。
func TestParseDetailDocAttributeStats(t *testing.T) {
	doc := `<div class="ta-uploader" data-uploader-stats="[{&quot;label&quot;:&quot;上传量：&quot;,&quot;value&quot;:&quot;101.552 TB&quot;,&quot;icon&quot;:&quot;fal fa-upload&quot;},{&quot;label&quot;:&quot;下载量：&quot;,&quot;value&quot;:&quot;3.558 TB&quot;,&quot;icon&quot;:&quot;fal fa-download&quot;},{&quot;label&quot;:&quot;做种积分：&quot;,&quot;value&quot;:&quot;980,961.4&quot;}]"></div>`

	profile := parseDetailDoc(doc).Profile

	if profile.Uploaded != wantBytes("101.552 TB") {
		t.Errorf("属性内嵌上传量解析错误: %d", profile.Uploaded)
	}
	if profile.Downloaded != wantBytes("3.558 TB") {
		t.Errorf("属性内嵌下载量解析错误: %d", profile.Downloaded)
	}
	if profile.SeedBonus != 980961.4 {
		t.Errorf("属性内嵌做种积分解析错误: %v", profile.SeedBonus)
	}
	// 站点没直接给分享率时按上传/下载推算。
	want := float64(profile.Uploaded) / float64(profile.Downloaded)
	if profile.Ratio < want-0.01 || profile.Ratio > want+0.01 {
		t.Errorf("分享率推算错误: %v 期望约 %v", profile.Ratio, want)
	}
}

// TestSecondaryLabelNotUsedAsTotal 验证「本月/今日」这类增量标签不会被当成账号总量。
func TestSecondaryLabelNotUsedAsTotal(t *testing.T) {
	doc := `<div>本月上传量： 2.467 TB</div>
<div>上传量： 21.374 TB</div>
<div>今日下载量： 12.5 GB</div>
<div>下载量： 0.50 TB</div>`

	profile := parseDetailDoc(doc).Profile

	if profile.Uploaded != wantBytes("21.374 TB") {
		t.Errorf("上传量取到了本月增量: %d", profile.Uploaded)
	}
	if profile.Downloaded != wantBytes("0.50 TB") {
		t.Errorf("下载量取到了今日增量: %d", profile.Downloaded)
	}
}

// TestSeedingBonusNotStolenByBonus 验证「做种积分」与「魔力值 / 做种数」不会互相抢字段。
// 关键风险：英文站的标签叫 Seed Bonus / Seeding Bonus，既包含 bonus（魔力值别名）也包含 seeding（做种数别名），
// 若不先取走并标记已用，做种积分会被记成魔力值甚至做种数。
func TestSeedingBonusNotStolenByBonus(t *testing.T) {
	// 中文站：两个字段同时存在，各归各位。
	zhDoc := `<table>
<tr><td class="rowhead">魔力值</td><td class="rowfollow">12,345.6</td></tr>
<tr><td class="rowhead">做种积分</td><td class="rowfollow">8,888.5</td></tr>
<tr><td class="rowhead">在做种</td><td class="rowfollow">42</td></tr>
</table>`
	zh := parseDetailDoc(zhDoc).Profile
	if zh.SeedBonus != 8888.5 {
		t.Errorf("中文站做种积分解析错误: %v", zh.SeedBonus)
	}
	if zh.Bonus != 12345.6 {
		t.Errorf("魔力值被做种积分污染: %v", zh.Bonus)
	}
	if zh.Seeding != 42 {
		t.Errorf("做种数解析错误: %d", zh.Seeding)
	}

	// 英文站：只有 Seed Bonus（做种积分），不能被当成魔力值或做种数。
	enDoc := `<table>
<tr><td class="rowhead">Seed Bonus</td><td class="rowfollow">3,200.75</td></tr>
<tr><td class="rowhead">Seeding</td><td class="rowfollow">17</td></tr>
</table>`
	en := parseDetailDoc(enDoc).Profile
	if en.SeedBonus != 3200.75 {
		t.Errorf("英文站做种积分解析错误: %v", en.SeedBonus)
	}
	if en.Bonus != 0 {
		t.Errorf("Seed Bonus 不应被当成魔力值: %v", en.Bonus)
	}
	if en.Seeding != 17 {
		t.Errorf("做种数不应被 Seed Bonus 干扰: %d", en.Seeding)
	}
}

// TestParseSizeBytesVariants 覆盖各站常见体积写法。
func TestParseSizeBytesVariants(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"1.5 GB", 1610612736},
		{"<span title=\"1024\">1</span> TB", 1099511627776},
		{"1,234.5 MiB", 1294467072},
		{"0 B", 0},
	}
	for _, c := range cases {
		got, ok := parseSizeBytes(c.input)
		if !ok {
			t.Errorf("%q 应解析成功", c.input)
			continue
		}
		if got != c.want {
			t.Errorf("%q 解析为 %d，期望 %d", c.input, got, c.want)
		}
	}
}

// TestJSONPayloadParsing 验证 JSON 通道的字段语义探测（不依赖具体站的字段拼写）。
func TestJSONPayloadParsing(t *testing.T) {
	raw := `{
		"data": {
			"id": "9",
			"username": "tester",
			"memberCount": {
				"uploaded": 1024,
				"downloaded": 512,
				"bonus": 8.5,
				"shareRate": 2.0,
				"seeding": 7,
				"leeching": 1
			}
		}
	}`
	var root any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		t.Fatalf("测试 JSON 不合法: %v", err)
	}
	profile := Profile{}
	applyJSONPayload(&profile, root)

	if profile.UserID != "9" || profile.Username != "tester" {
		t.Errorf("用户信息解析错误: id=%q name=%q", profile.UserID, profile.Username)
	}
	if profile.Uploaded != 1024 || profile.Downloaded != 512 {
		t.Errorf("流量解析错误: up=%d down=%d", profile.Uploaded, profile.Downloaded)
	}
	if profile.Ratio != 2 || profile.Bonus != 8.5 {
		t.Errorf("分享率/魔力解析错误: ratio=%v bonus=%v", profile.Ratio, profile.Bonus)
	}
	if profile.Seeding != 7 || profile.Leeching != 1 {
		t.Errorf("做种/下载中解析错误: seeding=%d leeching=%d", profile.Seeding, profile.Leeching)
	}
}

// TestJSONKeyMatchingPrecision 验证字段名匹配的精度：
// 既不能漏掉 promotionUploadSize 这种真实流量写法，也不能把 maxUploadTorrentSize 当成上传量。
func TestJSONKeyMatchingPrecision(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		check func(t *testing.T, profile Profile)
	}{
		{
			name: "YemaPT 的 promotionUploadSize 才是上传量",
			raw: `{"success":true,"data":{"id":4073,"name":"fenglaile",
				"maxUploadTorrentSize":3,"promotionUploadSize":4153269390012,
				"promotionDownloadSize":87701399349,"bonus":418814,"level":2,
				"availableInviteNum":1,"registerTime":"2026-09-10 16:20:00"}}`,
			check: func(t *testing.T, profile Profile) {
				if profile.Uploaded != 4153269390012 {
					t.Errorf("上传量取错字段: %d", profile.Uploaded)
				}
				if profile.Downloaded != 87701399349 {
					t.Errorf("下载量取错字段: %d", profile.Downloaded)
				}
				if profile.Bonus != 418814 {
					t.Errorf("魔力值解析错误: %v", profile.Bonus)
				}
				if profile.Invites != 1 {
					t.Errorf("邀请数解析错误: %d", profile.Invites)
				}
				if profile.JoinDate == "" {
					t.Errorf("注册时间未解析")
				}
			},
		},
		{
			name: "朱雀把等级做成对象时取 class.name",
			raw: `{"status":200,"data":{"id":9038,"username":"fenglaile",
				"upload":153695234095,"download":6040000000,"seeding":1,"leeching":0,
				"bonus":50066550.15,"seedBonus":9132323.53,
				"class":{"id":8,"level":8,"name":"真仙"}}}`,
			check: func(t *testing.T, profile Profile) {
				if profile.Uploaded != 153695234095 {
					t.Errorf("上传量解析错误: %d", profile.Uploaded)
				}
				if profile.UserClass != "真仙" {
					t.Errorf("等级应取 class.name，实际 %q", profile.UserClass)
				}
				if profile.SeedBonus != 9132323.53 {
					t.Errorf("做种积分解析错误: %v", profile.SeedBonus)
				}
				if profile.Bonus != 50066550.15 {
					t.Errorf("魔力值解析错误: %v", profile.Bonus)
				}
			},
		},
		{
			name: "可乐的流量在独立的 stats 响应里，优先取 raw_* 而不是 billed_*",
			raw: `{"billed_uploaded_bytes":"7629009472410","billed_downloaded_bytes":"0",
				"raw_uploaded_bytes":"7013852529357","raw_downloaded_bytes":"1013017446773",
				"seeding":24,"leeching":0,"points_balance":"10294.8504","class_name":"经典罐"}`,
			check: func(t *testing.T, profile Profile) {
				if profile.Uploaded != 7013852529357 {
					t.Errorf("应取 raw_uploaded_bytes，实际 %d", profile.Uploaded)
				}
				if profile.Downloaded != 1013017446773 {
					t.Errorf("应取 raw_downloaded_bytes，实际 %d", profile.Downloaded)
				}
				if profile.Seeding != 24 {
					t.Errorf("做种数解析错误: %d", profile.Seeding)
				}
				if profile.Bonus != 10294.8504 {
					t.Errorf("魔力值应取 points_balance，实际 %v", profile.Bonus)
				}
				if profile.UserClass != "经典罐" {
					t.Errorf("等级解析错误: %q", profile.UserClass)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var root any
			if err := json.Unmarshal([]byte(c.raw), &root); err != nil {
				t.Fatalf("测试 JSON 不合法: %v", err)
			}
			profile := Profile{}
			applyJSONPayload(&profile, root)
			c.check(t, profile)
		})
	}
}

// TestJSONMultiRootMerge 验证「身份接口 + 统计接口」两份响应能合并解析（可乐就是这么拆的）。
func TestJSONMultiRootMerge(t *testing.T) {
	var identity, stats any
	if err := json.Unmarshal([]byte(`{"id":101,"username":"fenglaile","status":"active"}`), &identity); err != nil {
		t.Fatalf("身份 JSON 不合法: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"raw_uploaded_bytes":"7013852529357","raw_downloaded_bytes":"1013017446773","seeding":24}`), &stats); err != nil {
		t.Fatalf("统计 JSON 不合法: %v", err)
	}
	profile := Profile{}
	applyJSONPayload(&profile, identity, stats)

	if profile.UserID != "101" || profile.Username != "fenglaile" {
		t.Errorf("身份字段丢失: id=%q name=%q", profile.UserID, profile.Username)
	}
	if profile.Uploaded != 7013852529357 || profile.Seeding != 24 {
		t.Errorf("统计字段丢失: up=%d seeding=%d", profile.Uploaded, profile.Seeding)
	}
}

// TestParseDetailDocTraditionalLabels 覆盖港台繁体皮肤（蝶粉 discfan / PTZone 同款）。
//
// 这两站的资料表与页头用繁体写标签：「上傳量 / 下載量 / 做種積分」。
// 别名表里只有简体，逐字对不上就会让上传量、下载量、做种积分全部落成 0 ——
// 而分享率（繁简同形）、魔力值、做种数却能正常取到，所以现象特别像「只有流量没抓到」。
func TestParseDetailDocTraditionalLabels(t *testing.T) {
	doc := `
<div>
<font class='color_ratio'>分享率：</font> 5.462
<font class='color_uploaded'>上傳量：</font> 8.691 TB
<font class='color_downloaded'> 下載量：</font> 1.591 TB
</div>
<table>
<tr>
  <td width="1%" class="rowhead nowrap">做種積分</td>
  <td width="99%" class="rowfollow">938,154.1&nbsp;&nbsp;<span class='text-muted'>(更新時間: 2026-11-25 23:14:36)</span></td>
</tr>
<tr>
  <td width="1%" class="rowhead nowrap">目前做種</td>
  <td width="99%" class="rowfollow"><a href="javascript: klappe_news('a1')">[察看/隱藏]</a></td>
</tr>
<tr>
  <td width="1%" class="rowhead nowrap">加入日期</td>
  <td width="99%" class="rowfollow">2025-10-12 11:14:55 (<span title="2025-10-12 11:14:55">1年1月前</span>)</td>
</tr>
</table>
<img class="arrowup" alt="Torrents seeding" title="當前做種" src="pic/trans.gif" />58
`
	parsed := parseDetailDoc(doc)
	profile := parsed.Profile

	if profile.Uploaded != wantBytes("8.691 TB") {
		t.Errorf("上傳量 解析错误：期望 %d，实际 %d", wantBytes("8.691 TB"), profile.Uploaded)
	}
	if profile.Downloaded != wantBytes("1.591 TB") {
		t.Errorf("下載量 解析错误：期望 %d，实际 %d", wantBytes("1.591 TB"), profile.Downloaded)
	}
	if profile.SeedBonus != 938154.1 {
		t.Errorf("做種積分 解析错误：期望 938154.1，实际 %v", profile.SeedBonus)
	}
	// 做種積分不能被「做种」别名当成做种数取走（取走会污染 seeding）。
	if profile.Seeding != 58 || !parsed.SeedingParsed {
		t.Errorf("做种数应来自页头图标 58，实际 %d（parsed=%v）", profile.Seeding, parsed.SeedingParsed)
	}
	if profile.JoinDate == "" {
		t.Error("加入日期（繁简同形）应能解析")
	}
}

// TestNormalizeLabelTraditional 验证标签归一化的繁简转换不会伤到值。
func TestNormalizeLabelTraditional(t *testing.T) {
	cases := map[string]string{
		"上傳量：":    "上传量",
		" 下載量 :":  "下载量",
		"做種積分":    "做种积分",
		"等級":      "等级",
		"註冊時間":    "注册时间",
		"上傳量(本月)": "上传量(本月)",
	}
	for input, want := range cases {
		if got := normalizeLabel(input); got != want {
			t.Errorf("normalizeLabel(%q) = %q，期望 %q", input, got, want)
		}
	}
}

// TestParseTorrentListSummary 验证摘要式做种列表（梓喵 getusertorrentlist_ajax.php）能取到数量。
func TestParseTorrentListSummary(t *testing.T) {
	cases := map[string]int64{
		`<br /><b>100</b> 条记录 | 总大小：300.19 GB <a href='/getusertorrentlist.php'>查看列表</a><br /><br />`: 100,
		`<b>7</b> 筆記錄`: 7,
	}
	for text, want := range cases {
		got, ok := parseTorrentListSummary(text)
		if !ok || got != want {
			t.Errorf("parseTorrentListSummary(%q) = %d,%v，期望 %d", text, got, ok, want)
		}
	}
	if _, ok := parseTorrentListSummary(`<table><tr><td>details.php?id=1</td></tr></table>`); ok {
		t.Error("完整种子列表不应被当成摘要")
	}
}

// TestHeaderUserNameIgnoresIconGlyph 验证页头用户名不被排名图标的 onmouseover 参数污染。
//
// 梓喵（azusa）把排名信息塞进 `onmouseover="domTT_activate(… 'content', '综合排名: <b …>1264</b>' …)"`，
// 属性值里带 `>`；旧的 `<[^>]+>` 会在第一个 `>` 处断开，剩下的 JS 参数和图标字符
// （`&#xe902;`，私有区码位）会被当成用户名正文。
func TestHeaderUserNameIgnoresIconGlyph(t *testing.T) {
	doc := `<span class="nowrap"><a href="https://azusa.wiki/userdetails.php?id=10820" class='User_Name'>` +
		`<b>englaile<i class="icon-rank" onmouseover="domTT_activate(this, event, 'content', '综合排名: <b style=\'color:orangered;\'>1264</b>', 'trail', true, 'delay', 0);">&#xe902;</i></b>` +
		`</a></span>`

	if name := headerUserName(doc); name != "englaile" {
		t.Errorf("用户名应为 englaile，实际 %q", name)
	}
	if text := plainText(doc); strings.Contains(text, "trail") || strings.Contains(text, "\ue902") {
		t.Errorf("剥标签后不应残留 JS 参数或图标字符：%q", text)
	}
}

// TTG 页头的做种/下载计数不是「图标后直接跟数字」，而是 `&nbsp;<font><span>291</span></font>`，
// 严格正则取不到，整站做种数会一直是 0；宽松版只在严格版完全没命中时才启用。
func TestParseActivityCountNestedMarkup(t *testing.T) {
	strict := `<img alt="Torrents seeding" title="seeding" src="a.gif">340`
	if got, ok := extractActivityCount(strict, "seeding"); !ok || got != 340 {
		t.Fatalf("严格版解析失败: %d %v", got, ok)
	}
	nested := `<font>做种活动 :&nbsp;</font> <img alt="做种中" title="做种中" src="/pic/arrowup.gif">&nbsp;` +
		`<font color=black><span class="smallfont">291</span></font>&nbsp;&nbsp;` +
		`<img alt="下载中" title="下载中" src="/pic/arrowdown.gif">&nbsp;<font color=black><span class="smallfont">0</span></font>`
	if got, ok := extractActivityCount(nested, "seeding"); !ok || got != 291 {
		t.Fatalf("内联标签包裹的做种数解析失败: %d %v", got, ok)
	}
	if got, ok := extractActivityCount(nested, "leeching"); !ok || got != 0 {
		t.Fatalf("内联标签包裹的下载数解析失败: %d %v", got, ok)
	}
}

// 不可说（ssd）页头把做种积分/茉莉缩写成「606.7K」，精确值只写在 title 属性里；
// 只按正文解析会得到 606.7 / 329.5，比真实值小三个数量级。
func TestParseTitlePreciseNumbers(t *testing.T) {
	doc := `<div class="head">` +
		`<a href="mybonus.php" title="茉莉: 329,518.8">茉莉: 329.5K</a>` +
		`<font class="color_ratio">分享率: </font> 5.872` +
		`<font class='color_uploaded'>上传量: </font> 23.589 TB` +
		`<font class='color_downloaded'>下载量: </font> 4.017 TB` +
		`<font class="color_bonus">做种积分:</font> <span title="606,714.6">606.7K</span>` +
		`</div>`

	detail := parseDetailDoc(doc)
	if detail.Profile.SeedBonus != 606714.6 {
		t.Errorf("做种积分应取 title 里的精确值 606714.6，实际 %v", detail.Profile.SeedBonus)
	}
	if detail.Profile.Bonus != 329518.8 {
		t.Errorf("茉莉应取 title 里的精确值 329518.8，实际 %v", detail.Profile.Bonus)
	}
	if detail.Profile.Ratio != 5.872 {
		t.Errorf("分享率不应受影响，实际 %v", detail.Profile.Ratio)
	}
}

// 没有 title 精确值时，缩写后缀要乘回倍数（606.7K -> 606700）。
func TestParseLooseFloatAbbreviation(t *testing.T) {
	cases := []struct {
		raw  string
		want float64
	}{
		{"606.7K", 606700},
		{"1.2M", 1200000},
		{"3.4万", 34000},
		{"1,593,841.1", 1593841.1},
		{"952,422.5", 952422.5},
		{"5.872", 5.872},
	}
	for _, c := range cases {
		got, ok := parseLooseFloat(c.raw)
		if !ok || got != c.want {
			t.Errorf("parseLooseFloat(%q) = %v,%v 期望 %v", c.raw, got, ok, c.want)
		}
	}
	// 带单位的体积文本不能被当成倍数
	if got, _ := parseLooseFloat("160.389 TB"); got != 160.389 {
		t.Errorf("带单位文本不应套用缩写倍数，实际 %v", got)
	}
}
