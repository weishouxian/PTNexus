package siteprofile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestExtractUserLevelFromHeaderClass 验证「资料页没有等级行、只有用户名 class」的站能取到等级。
//
// 蝶粉 / 杜比 / PTzone / 萝莉 都是这种皮肤：页头 `<a href="userdetails.php?id=..." class='VeteranUser_Name'>`。
// 城市 hdcity 的 `<font class="InsaneAngel_Name">mss</font>` 是邀请人，不是当前用户，不能取。
func TestExtractUserLevelFromHeaderClass(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "用户名链接带等级 class",
			doc: `<div class="bottom"><span class="nowrap"><a href="https://discfan.net/userdetails.php?id=50471" class='VeteranUser_Name'><b>fenglaile</b></a></span></div>
				  <div><a href="https://discfan.net/userdetails.php?id=47820" class='InsaneUser_Name'>other</a></div>`,
			want: "Veteran User",
		},
		{
			name: "NexusMaster 拆词",
			doc:  `<a href="userdetails.php?id=1" class="NexusMaster_Name">me</a>`,
			want: "Nexus Master",
		},
		{
			name: "彩虹名不是等级，回落到等级徽章",
			doc: `<a href="userdetails.php?id=30723" class='Rainbow_Name'>me</a>
				  <div class="ud-hero__badges"><span class="ud-class"><b class="VeteranUser_Name">(杰出公民)Veteran User</b></span></div>`,
			want: "Veteran User",
		},
		{
			name: "别人的 font class 不取",
			doc:  `<a href="userdetails?id=10102939" class="citizen"><font class="InsaneAngel_Name">mss</font></a>`,
			want: "",
		},
		{
			name: "第一个用户链接没有等级就停，不往后找别人的",
			doc: `<a href="userdetails.php?id=1" class="citizen">me</a>
				  <a href="userdetails.php?id=2" class="VeteranUser_Name">other</a>`,
			want: "",
		},
	}
	for _, testCase := range cases {
		got := extractUserLevel(testCase.doc, newCellSet(nil, nil, nil))
		if got != testCase.want {
			t.Errorf("%s：等级应为 %q，实际 %q", testCase.name, testCase.want, got)
		}
	}
}

// TestExtractUserLevelFromClassIcon 验证页头等级小图标（`pic/class/5.gif` 的 alt）也能取到等级。
// 城市 hdcity 的资料页既没有「等级」行，也没有用户名 class，只有这张图标。
func TestExtractUserLevelFromClassIcon(t *testing.T) {
	doc := `<td><img alt="中国" src="//static.leniter.org/pic/flag/china.gif" />
			<img alt="InsaneAngel" src="//static.leniter.org/pic/class/5.gif" /></td>`
	if got := extractUserLevel(doc, newCellSet(nil, nil, nil)); got != "Insane Angel" {
		t.Errorf("等级应取图标 alt（Insane Angel），实际 %q", got)
	}
}

// TestExtractUserLevelFromBadgeAttr 验证卡片皮肤把等级写在 data-* 属性里的情况（人人）。
func TestExtractUserLevelFromBadgeAttr(t *testing.T) {
	doc := `<div data-uploader-label="fenglaile" data-uploader-badge="(杰出公民)Veteran User" data-uploader-meta-primary="UID 30723"></div>`
	if got := extractUserLevel(doc, newCellSet(nil, nil, nil)); got != "(杰出公民)Veteran User" {
		t.Errorf("等级应取 data-uploader-badge，实际 %q", got)
	}
}

// TestExtractLabeledCellsBareValueCell 验证老皮肤（TTG）`<td align=left>` 没 class 的值格也能取到。
// 只认 rowfollow 会让 TTG 的资料表只剩标签，等级、加入日期整块丢失。
func TestExtractLabeledCellsBareValueCell(t *testing.T) {
	doc := `<table>
		<tr><td class=rowhead>等级</td><td align=left>BrontoByte</td></tr>
		<tr><td class=rowhead>邀请</td><td align=left><a href="/invite.php">6</a></td></tr>
		<tr><td class=rowhead>空值</td><td align=left>&nbsp;</td></tr>
		<tr><td class=rowhead>分组</td><td colspan=2 align=center>分隔行</td></tr>
	</table>`
	cells := newCellSet(extractLabeledCells(doc), nil, nil)
	if _, value := cells.pick([]string{"等级"}); !strings.Contains(value, "BrontoByte") {
		t.Errorf("老皮肤的裸值格没取到，实际 %q", value)
	}
	if _, value := cells.pick([]string{"分组"}); value != "" {
		t.Errorf("colspan 合并格不应当成值，实际 %q", value)
	}
}

// TestJSONNumericLevelName 验证「等级就是裸数字」的站点能补出 Level N（YemaPT）。
func TestJSONNumericLevelName(t *testing.T) {
	root := map[string]any{
		"success": true,
		"data": map[string]any{
			"name":  "fenglaile",
			"level": float64(2),
			"bonus": float64(419350),
		},
	}
	if got := jsonNumericLevelName(root); got != "Level 2" {
		t.Errorf("应补成 Level 2，实际 %q", got)
	}
	if got := jsonNumericLevelName(map[string]any{"data": map[string]any{"name": "x"}}); got != "" {
		t.Errorf("没有 level 字段时应返回空，实际 %q", got)
	}
}

// TestFetchDetailDocumentPrefersIDPage 验证资料页挑选逻辑：
// 不带 id 的页面只有页头（有流量、没有等级）时，必须用页面里发现的 uid 再抓一次带 id 的页面。
// TTG 就是这样：不带 id 只渲染页头，等级（BrontoByte）只在 `?id=` 页面里。
func TestFetchDetailDocumentPrefersIDPage(t *testing.T) {
	var bareHits, idHits int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/userdetails.php" {
			http.NotFound(writer, request)
			return
		}
		if request.URL.Query().Get("id") == "" {
			bareHits++
			// 页头只有流量 + 自己的用户链接，没有资料表（没有等级）。
			_, _ = writer.Write([]byte(`<html><body>
				<div>欢迎回来，<a href="/userdetails.php?id=171805">fenglaile</a></div>
				<div>分享率: 28.7 上传量: 100 TB 下载量: 3.5 TB</div>
			</body></html>`))
			return
		}
		idHits++
		_, _ = writer.Write([]byte(`<html><body>
			<div>欢迎回来，<a href="/userdetails.php?id=171805">fenglaile</a></div>
			<table>
				<tr><td class=rowhead>等级</td><td align=left>BrontoByte</td></tr>
				<tr><td class=rowhead>加入日期</td><td align=left>2025-11-13</td></tr>
			</table>
		</body></html>`))
	}))
	defer server.Close()

	parsed, err := fetchDetailDocument(context.Background(), server.URL, nil, "", "ttg")
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	if parsed.Profile.UserClass != "BrontoByte" {
		t.Errorf("应取带 id 页面里的等级，实际 %q", parsed.Profile.UserClass)
	}
	if bareHits == 0 || idHits == 0 {
		t.Errorf("应先试不带 id 再用页面里的 uid 抓带 id 页面，实际 bare=%d id=%d", bareHits, idHits)
	}
}

// TestDetailScorePrefersLevel 验证完整度打分：有等级的页面优先于只有流量的页面。
func TestDetailScorePrefersLevel(t *testing.T) {
	withLevel := parsedDetail{Profile: Profile{UserClass: "Veteran User", Uploaded: 1024}}
	onlyTraffic := parsedDetail{Profile: Profile{Uploaded: 1 << 40}}
	if detailScore(withLevel) <= detailScore(onlyTraffic) {
		t.Errorf("有等级的页面应得分更高：with=%d only=%d", detailScore(withLevel), detailScore(onlyTraffic))
	}
}
