package siteprofile

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pt-nexus/server/internal/repository"
)

// TestBuildUserLevelsFromSite 用 NexusPHP 标准 FAQ + rules 文案验证等级体系解析。
func TestBuildUserLevelsFromSite(t *testing.T) {
	faq := `不同的用户等级代表了什么含义?
Peasant 被降级的用户,他们有7天时间来提升分享率,否则他们会被踢。不能发表趣味盒内容;不能申请友情链接;不能上传字幕。
User 新用户的默认级别;可以上传的字幕。
Power User 可以查看NFO文档;可以请求续种; 可以购买/发送邀请;可以删除自己上传的字幕。可以申请友情链接;可以使用个性条。
Elite User 可以查看种子结构;可以更新外部信息。Elite User or above would never be deleted if parked.
Crazy User 可以在做种/下载/发布的时候选择匿名模式。
Insane User 可以查看排行榜。
Veteran User 可以查看其它用户种子历史。Veteran User or above would never be deleted whether parked or not.
Extreme User 可以更新过期的外部信息。
Ultimate User 首次到达此等级得到1个邀请名额。
Nexus Master 首次到达此等级得到1个邀请名额。Nexus Master及以上用户会永远保留账号。`

	rules := `用户升级和降级规则
升级至Power User等级别名:生员 注册时间大于4周,做种积分大于14888,下载量大于50GB, 且分享率大于1.05的用户,将升级为Power User。
升级至Elite User等级别名:秀才 注册时间大于8周,做种积分大于29888,下载量大于120GB, 且分享率大于1.55的用户,将升级为Elite User。
升级至Crazy User等级别名:举人 注册时间大于15周,做种积分大于59888,下载量大于300GB, 且分享率大于2.05的用户,将升级为Crazy User。`

	levels := buildUserLevels(faq, rules)
	if len(levels) != 10 {
		t.Fatalf("等级数量应为 10，实际 %d", len(levels))
	}

	byName := map[string]UserLevel{}
	for _, level := range levels {
		byName[level.Name] = level
	}

	if got := byName["Power User"].Privileges; !strings.Contains(got, "NFO") {
		t.Errorf("Power User 权限未从 FAQ 覆盖: %q", got)
	}
	if got := byName["Power User"].Requirements; !strings.Contains(got, "14888") {
		t.Errorf("Power User 升级条件未从 rules 覆盖: %q", got)
	}
	if got := byName["Elite User"].Requirements; !strings.Contains(got, "29888") {
		t.Errorf("Elite User 升级条件未从 rules 覆盖: %q", got)
	}
	// 保号线只看「永远保留」：Elite User 的 "never be deleted if parked" 是封存保号，
	// 只有 Veteran User 的 "never be deleted whether parked or not" 才是永久保留。
	if !byName["Veteran User"].KeepAlive {
		t.Errorf("保号等级应为 Veteran User（永久保留），实际未标记")
	}
	if byName["Elite User"].KeepAlive {
		t.Errorf("Elite User 是封存后才不删除，不应标为保号等级")
	}
	keepCount := 0
	for _, level := range levels {
		if level.KeepAlive {
			keepCount++
		}
	}
	if keepCount != 1 {
		t.Errorf("保号等级应只有一个，实际 %d", keepCount)
	}
	// User 的描述不能被 "Power User" 里的 User 截断。
	if got := byName["User"].Privileges; strings.Contains(got, "NFO") || strings.Contains(got, "Power") {
		t.Errorf("User 等级描述被误切: %q", got)
	}
}

// TestBuildUserLevelsFallback 验证 FAQ/rules 都拿不到时退回内置标准表。
// 内置表里只有 Nexus Master 写了「账号永久保留」，所以保号等级就落在它身上；
// 其它等级一律不标（不能猜 NexusPHP 的 Elite User 标准值，否则用户会误以为低一级已保号）。
func TestBuildUserLevelsFallback(t *testing.T) {
	levels := buildUserLevels("", "")
	if len(levels) != 10 {
		t.Fatalf("内置等级数量应为 10，实际 %d", len(levels))
	}
	for _, level := range levels {
		if level.KeepAlive && level.Name != "Nexus Master" {
			t.Errorf("内置表只有 Nexus Master 写了账号永久保留，不应标 %q", level.Name)
		}
		if level.Name == "Nexus Master" && !level.KeepAlive {
			t.Errorf("Nexus Master 写了「账号永久保留」，应标为保号等级")
		}
	}
	if levels[2].Requirements == "" || levels[9].Privileges == "" {
		t.Errorf("内置表不应有空字段")
	}
}

// TestBuildUserLevelsCNFAQ 验证中文标点下的保号识别：只认「会永远保留账号」那一级。
func TestBuildUserLevelsCNFAQ(t *testing.T) {
	faq := `不同的用户等级代表了什么含义？
Peasant：被降级的用户，须在7天内改善分享率。
User：新用户的默认级别。
Power User：可以查看NFO文档，可以发送邀请。
Elite User：可以查看种子结构。
Crazy User：可以匿名。
Veteran User：可以查看其它用户种子历史。Veteran User及以上用户封存账号后不会被删除。
Extreme User：可以更新过期的外部信息。
Ultimate User：首次到达得到1个邀请名额。
Nexus Master：Nexus Master及以上用户会永远保留账号。`

	levels := buildUserLevels(faq, "")
	var keepAlive string
	for _, level := range levels {
		if level.KeepAlive {
			keepAlive = level.Name
		}
	}
	// FAQ 里 Veteran User 只写了「封存账号后不会被删除」，永久保留的是 Nexus Master。
	if keepAlive != "Nexus Master" {
		t.Errorf("保号等级应为 Nexus Master，实际 %q", keepAlive)
	}
	for _, level := range levels {
		if level.Name == "Power User" && !strings.Contains(level.Privileges, "NFO") {
			t.Errorf("中文 FAQ 权限解析失败: %q", level.Privileges)
		}
	}
}

// TestBuildUserLevelsFromFAQTable 验证按 FAQ 页面 HTML 的等级表解析：
// 站点自定义等级（贵宾/发布员/版主）必须出现，不能再被内置 10 级标准表吃掉。
func TestBuildUserLevelsFromFAQTable(t *testing.T) {
	faqHTML := `<html><body>
<ul><li><a href="#id22" class="faqlink">不同的用户等级代表了什么含义？</a></li></ul>
<h2>站点信息</h2><span id="id22"><b>不同的用户等级代表了什么含义？</b></span><br />
<table cellspacing="3" cellpadding="0">
<tr><td class="embedded">&nbsp; <b class="Peasant_Name">(冰人)Peasant</b></td><td>&nbsp;</td><td class="embedded">被降级的用户，7天内提升分享率。</td></tr>
<tr><td class="embedded">&nbsp; <b class="User_Name">(未烧)User</b></td><td>&nbsp;</td><td class="embedded">新用户的默认级别。</td></tr>
<tr><td class="embedded">&nbsp; <b class="PowerUser_Name">(感冒)Power User</b></td><td>&nbsp;</td><td class="embedded">可以查看NFO文档；可以请求续种。</td></tr>
<tr><td class="embedded">&nbsp; <b class="EliteUser_Name">(发热)Elite User</b></td><td>&nbsp;</td><td class="embedded">可以查看种子结构。</td></tr>
<tr><td class="embedded">&nbsp; <b class="CrazyUser_Name">(低烧)Crazy User</b></td><td>&nbsp;</td><td class="embedded">可以做种时匿名。</td></tr>
<tr><td class="embedded">&nbsp; <b class="InsaneUser_Name">(中烧)Insane User</b></td><td>&nbsp;</td><td class="embedded">可以查看普通日志。</td></tr>
<tr><td class="embedded">&nbsp; <b class="VeteranUser_Name">(高烧)Veteran User</b></td><td>&nbsp;</td><td class="embedded">(高烧)Veteran User及以上用户封存账号后不会被删除。</td></tr>
<tr><td class="embedded">&nbsp; <b class="ExtremeUser_Name">(烧糊涂)Extreme User</b></td><td>&nbsp;</td><td class="embedded">可以查看投票区。</td></tr>
<tr><td class="embedded">&nbsp; <b class="UltimateUser_Name">(走火入魔)Ultimate User</b></td><td>&nbsp;</td><td class="embedded">保留帐号。</td></tr>
<tr><td class="embedded">&nbsp; <b class="NexusMaster_Name">(骨灰)Nexus Master</b></td><td>&nbsp;</td><td class="embedded">永久保留账号。</td></tr>
<tr><td class="embedded">&nbsp; <img class="star" src="pic/trans.gif" alt="Star" /></td><td>&nbsp;</td><td class="embedded">为网站捐款的主。</td></tr>
<tr><td class="embedded">&nbsp; <b class="VIP_Name">贵宾(VIP)</b></td><td>&nbsp;</td><td class="embedded">和Nexus Master拥有相同权限。</td></tr>
<tr><td class="embedded">&nbsp; <b class="User_Name">其它</b></td><td>&nbsp;</td><td class="embedded">自定义等级。</td></tr>
<tr><td class="embedded">&nbsp; <b class="Uploader_Name">发布员(Uploader)</b></td><td>&nbsp;</td><td class="embedded">专注的发布者。</td></tr>
<tr><td class="embedded">&nbsp; <b class="Retiree_Name">版主(Retiree)</b></td><td>&nbsp;</td><td class="embedded">退休后的管理组成员。</td></tr>
</table>
</body></html>`

	rules := `升级至 Ultimate User 等级：注册时间大于80周，做种积分大于668888，下载量大于1536GB。
升级至 贵宾(VIP) 等级：捐赠后由管理员授予。`

	levels := buildUserLevelsFromFAQ(faqHTML, rules)
	if len(levels) != 13 {
		names := make([]string, 0, len(levels))
		for _, level := range levels {
			names = append(names, level.Name)
		}
		t.Fatalf("应解析出 13 个等级（过滤「其它」，保留贵宾/发布员/版主），实际 %d: %s", len(levels), strings.Join(names, ","))
	}

	byName := map[string]UserLevel{}
	for _, level := range levels {
		byName[level.Name] = level
	}
	for _, expected := range []string{"贵宾(VIP)", "发布员(Uploader)", "版主(Retiree)", "(走火入魔)Ultimate User"} {
		if _, ok := byName[expected]; !ok {
			t.Errorf("等级 %q 未解析出来", expected)
		}
	}
	if _, ok := byName["其它"]; ok {
		t.Errorf("占位行「其它」不应作为等级展示")
	}
	if got := byName["(感冒)Power User"].Privileges; !strings.Contains(got, "NFO") {
		t.Errorf("Power User 权限未取到: %q", got)
	}
	// 保号（口径：以权限列写的「永远保留账号」为准）：
	//   - (高烧)Veteran User 行只写「封存账号后不会被删除」，属封存保号，不算；
	//   - (走火入魔)Ultimate User 行「保留帐号」、(骨灰)Nexus Master 行「永久保留账号」都算，
	//     取最低的那一级 ⇒ Ultimate User。
	if !byName["(走火入魔)Ultimate User"].KeepAlive {
		t.Errorf("保号等级应为 (走火入魔)Ultimate User（权限列写了保留帐号）")
	}
	if byName["(高烧)Veteran User"].KeepAlive {
		t.Errorf("封存保号（封存账号后不会被删除）不应标为保号等级，实际标了 %q", "(高烧)Veteran User")
	}
	keepCount := 0
	for _, level := range levels {
		if level.KeepAlive {
			keepCount++
		}
	}
	if keepCount != 1 {
		t.Errorf("保号等级应只有一个，实际 %d", keepCount)
	}
	// 升级条件要能对上带中文别名的等级名。
	if got := byName["(走火入魔)Ultimate User"].Requirements; !strings.Contains(got, "668888") {
		t.Errorf("Ultimate User 升级条件未从 rules 匹配: %q", got)
	}
	if got := byName["贵宾(VIP)"].Requirements; !strings.Contains(got, "捐赠") {
		t.Errorf("贵宾(VIP) 升级条件未从 rules 匹配: %q", got)
	}
}

// TestParseFAQLevelRowsNonNexusPHP 验证页面里没有等级表时不会误把其它表格当成等级。
func TestParseFAQLevelRowsNonNexusPHP(t *testing.T) {
	page := `<html><body><h2>FAQ</h2><table><tr><td>常见问题</td><td>答案</td></tr></table></body></html>`
	rows := parseLevelTable(page, reFAQPrivilegeHeading)
	if len(rows) != 0 {
		t.Fatalf("非等级表不应解析出等级，实际 %d", len(rows))
	}
	// 没有 FAQ 可用时 buildUserLevelsFromFAQ 必须退回内置标准表。
	levels := buildUserLevelsFromFAQ(page, "")
	if len(levels) != 10 {
		t.Fatalf("无 FAQ 等级表时应退回 10 级标准表，实际 %d", len(levels))
	}
}

// TestBuildUserLevelsFromMarkdownTable 验证自研皮肤的四列等级表（憨憨 / PeerGo 规则页）：
// 等级名写成「脸憨皮厚 Peasant」（没有 _Name class）、升级条件在第 2 列、权限在第 4 列。
func TestBuildUserLevelsFromMarkdownTable(t *testing.T) {
	faqHTML := `<div><h2 class="toc-header" id="账号等级规则"><a href="#账号等级规则" class="toc-anchor">¶</a> <font color="#2277aa">账号等级规则</font></h2>
<div class="table-container"><table>
<thead><tr><th>等级</th><th>升级条件</th><th>降级条件</th><th>权限</th></tr></thead>
<tbody>
<tr><td><font>脸憨皮厚 Peasant </font></td><td>-</td><td>详见分享率规则</td><td>被降级的用户，7 天内提升分享率。</td></tr>
<tr><td><font>憨头憨脑 User</font></td><td>新注册用户的默认等级</td><td>新注册用户的默认等级</td><td>新用户的默认级别。</td></tr>
<tr><td><font>憨声憨气 Power User</font></td><td>做种积分大于80000，并且下载至少50G，分享率大于1.05</td><td>分享率低于0.95</td><td>访问音乐区、论坛邀请区</td></tr>
<tr><td><font>憨态可掬 Elite User</font></td><td>必须注册至少8周</td><td>分享率低于1.45</td><td>-</td></tr>
<tr><td><font>明姿憨憨 Crazy User</font></td><td>必须注册至少15周</td><td>分享率低于1.95</td><td>可以做种时匿名。</td></tr>
<tr><td><font>裝憨打呆 Insane User</font></td><td>必须注册至少25周</td><td>分享率低于2.45</td><td>可以查看普通日志。</td></tr>
<tr><td><font>憨状可掬 Veteran User</font></td><td>必须注册至少40周</td><td>分享率低于2.95</td><td>Veteran User及以上用户封存账号后不会被删除。</td></tr>
<tr><td><font>憨痴可人 Extreme User</font></td><td>必须注册至少60周</td><td>分享率低于3.45</td><td>可以更新过期的外部信息。</td></tr>
<tr><td><font>憨笑如花 Ultimate User</font></td><td>必须注册至少80周</td><td>分享率低于3.95</td><td>Ultimate User及以上用户会永远保留账号。</td></tr>
<tr><td><font>满面娇憨 Nexus Master</font></td><td>必须注册至少100周</td><td>分享率低于4.45</td><td>得到两个邀请名额。</td></tr>
<tr><td><font>憨厚可爱 贵宾(VIP)</font></td><td>用户使用魔力值兑换</td><td>-</td><td>免除自动降级。</td></tr>
<tr><td><font>装憨卖傻 养老族(Retiree)</font></td><td>由管理员授予</td><td>-</td><td>退休后的管理组成员。</td></tr>
<tr><td><font>其他</font></td><td>用户使用魔力值兑换</td><td>-</td><td>占位说明。</td></tr>
<tr><td><font>红憨满面 发布员(Uploader)</font></td><td>由管理员分配</td><td>-</td><td>专注的发布者。</td></tr>
<tr><td><font>憨直率真 总版主(Moderator)</font></td><td>无需多问</td><td>-</td><td>管理论坛。</td></tr>
<tr><td><font>憨憨与世 管理员(Administrator)</font></td><td>-</td><td>-</td><td>管理站点。</td></tr>
<tr><td><font>浩瀚憨憨 维护开发员(Sysop)</font></td><td>-</td><td>-</td><td>维护站点。</td></tr>
</tbody></table></div></div>`

	levels := buildUserLevelsFromFAQ(faqHTML, "")
	if len(levels) != 16 {
		names := make([]string, 0, len(levels))
		for _, level := range levels {
			names = append(names, level.Name)
		}
		t.Fatalf("应解析出 16 个等级（过滤「其他」），实际 %d: %s", len(levels), strings.Join(names, ","))
	}
	byName := map[string]UserLevel{}
	for _, level := range levels {
		byName[level.Name] = level
	}
	if got := byName["憨声憨气 Power User"].Requirements; !strings.Contains(got, "80000") {
		t.Errorf("四列表格的升级条件未取到: %q", got)
	}
	if got := byName["憨声憨气 Power User"].Privileges; !strings.Contains(got, "音乐区") {
		t.Errorf("四列表格的权限未取到: %q", got)
	}
	// 憨憨的 Veteran User 行只写封存保号，永久保留写在 Ultimate User 行。
	if !byName["憨笑如花 Ultimate User"].KeepAlive {
		t.Errorf("保号等级应为 憨笑如花 Ultimate User（永久保留）")
	}
	if byName["憨状可掬 Veteran User"].KeepAlive {
		t.Errorf("Veteran User 是封存后才不删除，不应标为保号等级")
	}
	if _, ok := byName["其他"]; ok {
		t.Errorf("占位行「其他」不应作为等级展示")
	}
	for _, top := range []string{"贵宾(VIP)", "养老族(Retiree)", "发布员(Uploader)", "总版主(Moderator)"} {
		found := false
		for name := range byName {
			if strings.Contains(name, top) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("特殊等级 %q 未解析出来", top)
		}
	}
}

// TestKeepAliveOrderNotStolenByLowerLevels 回归真实踩过的坑：低等级行不能抢走
// **后面等级行**里的「X 及以上用户会永远保留账号」——早期实现给每个等级名配 320 字节窗口，
// 取最低命中，结果 1ptba 判成 Elite User、天空判成 Veteran User、彩虹岛判成 Extreme User。
func TestKeepAliveOrderNotStolenByLowerLevels(t *testing.T) {
	faqHTML := `<html><body>
<span id="id22"><b>不同的用户等级代表了什么含义？</b></span><br />
<table cellspacing="3">
<tr><td><b class="Peasant_Name">Peasant</b></td><td>被降级的用户。</td></tr>
<tr><td><b class="User_Name">User</b></td><td>新用户的默认级别。</td></tr>
<tr><td><b class="PowerUser_Name">Power User</b></td><td>可以查看NFO文档。</td></tr>
<tr><td><b class="EliteUser_Name">Elite User</b></td><td>可以查看种子结构。</td></tr>
<tr><td><b class="CrazyUser_Name">Crazy User</b></td><td>可以做种时匿名。</td></tr>
<tr><td><b class="VeteranUser_Name">Veteran User</b></td><td>Veteran User 及以上用户会永远保留账号。</td></tr>
<tr><td><b class="ExtremeUser_Name">Extreme User</b></td><td>可以更新过期的外部信息。</td></tr>
<tr><td><b class="UltimateUser_Name">Ultimate User</b></td><td>首次到达得到1个邀请名额。</td></tr>
<tr><td><b class="NexusMaster_Name">Nexus Master</b></td><td>Nexus Master 及以上用户会永远保留账号。</td></tr>
</table></body></html>`

	levels := buildUserLevelsFromFAQ(faqHTML, "")
	byName := map[string]UserLevel{}
	for _, level := range levels {
		byName[level.Name] = level
	}
	if !byName["Veteran User"].KeepAlive {
		t.Errorf("保号等级应为 Veteran User（权限列写了「Veteran User 及以上用户会永远保留账号」）")
	}
	for _, level := range levels {
		if level.KeepAlive && level.Name != "Veteran User" {
			t.Errorf("只有 Veteran User 应标保号，实际多标了 %q", level.Name)
		}
	}
}

// TestKeepAliveThisLevelPhrase 验证自指写法「此级别及以上用户会永远保留账号」（ptcafe 这类
// 不重复等级名的站点）也能定位到本行。
func TestKeepAliveThisLevelPhrase(t *testing.T) {
	faqHTML := `<html><body>
<span id="id22"><b>不同的用户等级代表了什么含义？</b></span><br />
<table cellspacing="3">
<tr><td><b class="Peasant_Name">Peasant</b></td><td>被降级的用户。</td></tr>
<tr><td><b class="User_Name">User</b></td><td>新用户的默认级别。</td></tr>
<tr><td><b class="PowerUser_Name">Power User</b></td><td>可以查看NFO文档。</td></tr>
<tr><td><b class="EliteUser_Name">Elite User</b></td><td>可以查看种子结构。</td></tr>
<tr><td><b class="CrazyUser_Name">Crazy User</b></td><td>可以做种时匿名。</td></tr>
<tr><td><b class="VeteranUser_Name">Veteran User</b></td><td>得到三个邀请名额；此级别及以上用户会永远保留账号。</td></tr>
<tr><td><b class="ExtremeUser_Name">Extreme User</b></td><td>可以更新过期的外部信息。</td></tr>
<tr><td><b class="UltimateUser_Name">Ultimate User</b></td><td>首次到达得到1个邀请名额。</td></tr>
<tr><td><b class="NexusMaster_Name">Nexus Master</b></td><td>获得 10 个邀请名额。</td></tr>
</table></body></html>`

	levels := buildUserLevelsFromFAQ(faqHTML, "")
	keepAlive := ""
	for _, level := range levels {
		if level.KeepAlive {
			if keepAlive != "" {
				t.Fatalf("保号等级应唯一，实际多个: %q / %q", keepAlive, level.Name)
			}
			keepAlive = level.Name
		}
	}
	if keepAlive != "Veteran User" {
		t.Errorf("「此级别及以上」应定位到本行 Veteran User，实际 %q", keepAlive)
	}
}

// TestSingleLevelFallback 验证接口站没有等级体系时用当前等级兜底（否则等级弹层是空的）。
func TestSingleLevelFallback(t *testing.T) {
	if levels := singleLevelFallback("  "); levels != nil {
		t.Errorf("空等级名不应生成兜底等级，实际 %+v", levels)
	}
	levels := singleLevelFallback(" 真仙 ")
	if len(levels) != 1 {
		t.Fatalf("应生成 1 条兜底等级，实际 %d", len(levels))
	}
	if levels[0].Name != "真仙" || levels[0].KeepAlive {
		t.Errorf("兜底等级名称未 trim 或被误标保号: %+v", levels[0])
	}
	if !strings.Contains(levels[0].Privileges, "未提供等级体系") {
		t.Errorf("兜底等级缺少说明: %q", levels[0].Privileges)
	}
}

// TestApplyKeepAliveOverride 验证手动保号等级覆盖自动识别结果。
func TestApplyKeepAliveOverride(t *testing.T) {
	base := []UserLevel{
		{Name: "Veteran User", Order: 6, KeepAlive: true},
		{Name: "影帝(Ultimate User)", Order: 8},
		{Name: "终身影帝(Nexus Master)", Order: 9},
	}
	copyLevels := func() []UserLevel {
		out := make([]UserLevel, len(base))
		copy(out, base)
		return out
	}

	// 1) 精确名覆盖：保号标记搬到指定等级，且只有一个。
	levels := applyKeepAliveOverride(copyLevels(), "影帝(Ultimate User)")
	if levels[1].KeepAlive != true || levels[0].KeepAlive || levels[2].KeepAlive {
		t.Errorf("精确覆盖失败: %+v", levels)
	}
	// 2) 名称带不同空格/括号写法也能对上（站点重抓后别名可能变）。
	levels = applyKeepAliveOverride(copyLevels(), "影帝（UltimateUser）")
	if !levels[1].KeepAlive {
		t.Errorf("归一化匹配失败: %+v", levels)
	}
	// 3) 空值 = 保留站点自动识别结果。
	levels = applyKeepAliveOverride(copyLevels(), "  ")
	if !levels[0].KeepAlive || levels[1].KeepAlive {
		t.Errorf("空值不应改动自动结果: %+v", levels)
	}
	// 4) 表里没有这个等级（站点改名）→ 不覆盖，也不把保号标记抹掉。
	levels = applyKeepAliveOverride(copyLevels(), "不存在的等级")
	if !levels[0].KeepAlive {
		t.Errorf("未知等级不应清空自动结果: %+v", levels)
	}
}

// TestLevelNameExists 验证保存前的等级名校验（防止前端传别的站的名字）。
func TestLevelNameExists(t *testing.T) {
	levels := []UserLevel{{Name: "Veteran User"}, {Name: "影帝(Ultimate User)"}}
	if !levelNameExists(levels, "Veteran User") {
		t.Errorf("精确名应存在")
	}
	if !levelNameExists(levels, "影帝（Ultimate User）") {
		t.Errorf("归一化后应存在")
	}
	if levelNameExists(levels, "Nexus Master") {
		t.Errorf("不存在的等级不应通过校验")
	}
	if order := keepAliveName(levels); order != "" {
		t.Errorf("未标保号时应返回空串，实际 %q", order)
	}
	levels[1].KeepAlive = true
	if got := keepAliveName(levels); got != "影帝(Ultimate User)" {
		t.Errorf("保号名取错: %q", got)
	}
}

// TestLevelsCacheVersion 验证等级缓存带版本号：旧版（裸数组）缓存不被复用，避免刷新后
// 还显示缺失等级的旧结果。
func TestLevelsCacheVersion(t *testing.T) {
	levels := []UserLevel{{Name: "贵宾(VIP)", Order: 10}}
	raw := encodeLevelsCache(levels)
	if raw == "" {
		t.Fatalf("编码结果为空")
	}
	decoded, version := decodeLevelsCache(raw)
	if version != levelsSchemaVersion || len(decoded) != 1 || decoded[0].Name != "贵宾(VIP)" {
		t.Errorf("解码结果不符: version=%d levels=%+v", version, decoded)
	}
	legacy := `[{"name":"Peasant","order":0}]`
	decodedLegacy, legacyVersion := decodeLevelsCache(legacy)
	if legacyVersion != 0 || len(decodedLegacy) != 1 {
		t.Errorf("旧格式应解析为版本 0，实际 version=%d levels=%+v", legacyVersion, decodedLegacy)
	}
}

// TestSiteDataJSONShape 验证接口输出：账号字段平铺、levels 为数组、内部 levels_json 不外泄。
func TestSiteDataJSONShape(t *testing.T) {
	data := SiteData{
		SiteProfile: repository.SiteProfile{
			SiteCode:   "hdsky",
			Nickname:   "天空",
			UserClass:  "Power User",
			Uploaded:   1024,
			LevelsJSON: `[{"name":"should-not-appear"}]`,
		},
		Levels: []UserLevel{{Name: "Power User", Order: 2, Requirements: "注册 ≥ 4 周", Privileges: "查看 NFO"}},
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"user_class":"Power User"`) {
		t.Errorf("账号字段未平铺: %s", text)
	}
	if !strings.Contains(text, `"levels":[{"name":"Power User"`) {
		t.Errorf("levels 数组缺失: %s", text)
	}
	if strings.Contains(text, "levels_json") || strings.Contains(text, "should-not-appear") {
		t.Errorf("内部 levels_json 不应外泄: %s", text)
	}
	// 手动保号与自动识别结果都要平铺输出，前端靠这两个字段画「当前/可改」状态。
	if !strings.Contains(text, `"keep_alive_auto":""`) {
		t.Errorf("keep_alive_auto 字段缺失: %s", text)
	}
	if !strings.Contains(text, `"keep_alive_level":""`) {
		t.Errorf("keep_alive_level 字段缺失: %s", text)
	}
}

// TestKeepAliveAccountPermanentlyRetained 验证「账号永久保留」这种语序也算保号等级。
//
// 站点常见两种写法：①「X 及以上用户会永远保留账号」（取 X）；②某级权限直接写
// 「保留帐号 / 账号永久保留 / 永久保留账号」（取该级）。NexusPHP 的 Nexus Master
// 就是第 ② 种，早期实现只在高等级行里找「及以上」句式，导致这类站点的保号列是空的。
func TestKeepAliveAccountPermanentlyRetained(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		wantLevel string
	}{
		{name: "账号永久保留", text: "获得 10 个邀请名额；为站点捐款的用户；账号永久保留", wantLevel: "Nexus Master"},
		{name: "永久保留账号", text: "为站点捐款的用户，永久保留账号", wantLevel: "Nexus Master"},
		{name: "保留帐号", text: "首次升级赠送邀请 2 枚，保留帐号", wantLevel: "Nexus Master"},
		{name: "繁体帳號永久保留", text: "為站點捐款的用戶，帳號永久保留", wantLevel: "Nexus Master"},
		{name: "封存后不删除不算", text: "Nexus Master 及以上用户封存账号后不会被删除", wantLevel: ""},
	}
	for _, testCase := range cases {
		levels := []UserLevel{
			{Name: "Veteran User", Order: 0, Privileges: "可以查看其它用户种子历史。"},
			{Name: "Ultimate User", Order: 1, Privileges: "首次到达赠送邀请。"},
			{Name: "Nexus Master", Order: 2, Privileges: testCase.text},
		}
		markKeepAliveLevelsFromRows(levels, "", "")
		got := ""
		for _, level := range levels {
			if level.KeepAlive {
				got = level.Name
			}
		}
		if got != testCase.wantLevel {
			t.Errorf("%s：保号等级应为 %q，实际 %q", testCase.name, testCase.wantLevel, got)
		}
	}
}

// TestBuildUserLevelsFromMarkdownFAQ 覆盖 markdown 皮肤的 FAQ（梓喵 azusa）。
//
// 这类站点用 editormd：等级表在页面源码里是隐藏 div 中的 markdown 原文
// （`| 等级 | 权限说明 |`），渲染出来的表格是 JS 生成的，HTML 里没有 <table>。
// 只解析 HTML 表格会让整站退回内置 10 级，站点自定义的「黄星 / 贵宾(VIP) / 发布员」全部丢失。
func TestBuildUserLevelsFromMarkdownFAQ(t *testing.T) {
	markdown := `# 用户等级

| 等级 | 权限说明 |
|------|----------|
| **Peasant** | 被降级的用户，有 5 天时间来提升分享率，否则他们会被禁用。 |
| **User** | 新用户的默认级别。 |
| **Elite User** | Elite User 及以上用户封存账号后不会被删除。 |
| **Veteran User** | Veteran User 及以上用户会永远保留账号。 |
| **黄星** | 为网站捐款的主。 |
| **其它** | 自定义等级。 |

### 各等级提升条件：

| 等级 | 注册时间 | 做种积分 | 下载量 |
|------|----------|----------|--------|
| **Power User** | 至少 4 周 | &gt;40000 | 至少 50G |
| **Elite User** | 至少 8 周 | &gt;100000 | 至少 100G |
| **Veteran User** | 至少 40 周 | &gt;1000000 | 至少 250G |
`
	doc := `<div id="markdown-abc-content" style="display: none;">` + markdown + `</div>`

	levels := buildUserLevelsFromFAQ(doc, "")
	if len(levels) != 5 {
		t.Fatalf("应解析出 5 个等级（「其它」是占位行要丢掉），实际 %d：%+v", len(levels), levels)
	}
	wantNames := []string{"Peasant", "User", "Elite User", "Veteran User", "黄星"}
	for index, want := range wantNames {
		if levels[index].Name != want {
			t.Errorf("第 %d 级应为 %q，实际 %q", index, want, levels[index].Name)
		}
	}
	if levels[0].Privileges == "" {
		t.Error("Peasant 的权限说明不应为空")
	}
	if got := levels[2].Requirements; got != "注册时间 至少 8 周；做种积分 >100000；下载量 至少 100G" {
		t.Errorf("Elite User 升级条件解析错误：%q", got)
	}
	// 保号：只认「Veteran User 及以上用户会永远保留账号」，
	// Elite 行的「封存账号后不会被删除」不算。
	for _, level := range levels {
		if level.KeepAlive != (level.Name == "Veteran User") {
			t.Errorf("保号等级应为 Veteran User，实际 %q keep=%v", level.Name, level.KeepAlive)
		}
	}
}

// TTG 的 FAQ（`faq.php#user9`）用第一列加粗的短文本表示等级（Byte/KiloByte/…/DoggaByte），
// 没有 `_Name` class，也没有 NexusPHP 的标准等级名；标题写的是「TTG有哪些不同的用户类别？」。
// 升级条件以行内括号形式写在权限列里，表格末尾还有「以下为工作组：」分隔行。
func TestBuildUserLevelsFromTTGUserClasses(t *testing.T) {
	doc := `<p><b>TTG有哪些不同的用户类别？</b><a name="user9" id="user9"></a><br>
<table cellspacing=3 cellpadding=0>
<tr><td class=embedded><strong>Byte</strong></td><td class=embedded>&nbsp;</td>
<td class=embedded>此为入站最初始等级，接受新人考核制。</td></tr>
<tr><td class=embedded><strong>KiloByte</strong></td><td class=embedded>&nbsp;</td>
<td class=embedded>此等级才为正式会员。（升级条件：注册满 5 周，下载量在60G以上，分享率大于 1.1 会升级。）</td></tr>
<tr><td class=embedded><strong>MegaByte</strong></td><td class=embedded>&nbsp;</td>
<td class=embedded>此等级及以上会员12周不活动将被系统自动清除。（升级条件：注册满 8 周，下载量150G以上）</td></tr>
<tr><td class=embedded><strong>BrontoByte</strong></td><td class=embedded>&nbsp;</td>
<td class=embedded>此等级及以上等级用户会永远保留账号。（升级条件：注册满 32 周）</td></tr>
<tr><td class=embedded><strong></strong></td><td class=embedded>&nbsp;</td><td class=embedded><br></td></tr>
<tr><td class=embedded><strong>VIP</strong></td><td class=embedded>&nbsp;</td>
<td class=embedded>为TTG做出特殊重大贡献的用户或合作者等。</td></tr>
<tr><td class=embedded><strong>以下为工作组：</strong></td><td class=embedded>&nbsp;</td><td class=embedded></td></tr>
<tr><td class=embedded><strong>WiKi</strong></td><td class=embedded>&nbsp;</td><td class=embedded>官方高清影视压制小组</td></tr>
<tr><td class=embedded><strong>Admin</strong></td><td class=embedded>&nbsp;</td><td class=embedded>管理员</td></tr>
</table>`

	levels := buildUserLevelsFromFAQ(doc, "")
	wantNames := []string{"Byte", "KiloByte", "MegaByte", "BrontoByte", "VIP"}
	if len(levels) != len(wantNames) {
		t.Fatalf("应解析出 %d 个等级（工作组行要按分组说明截断），实际 %d：%+v", len(wantNames), len(levels), levels)
	}
	for index, want := range wantNames {
		if levels[index].Name != want {
			t.Errorf("第 %d 级应为 %q，实际 %q", index, want, levels[index].Name)
		}
	}
	if got := levels[1].Requirements; got != "注册满 5 周，下载量在60G以上，分享率大于 1.1 会升级" {
		t.Errorf("KiloByte 行内升级条件未拆出：%q", got)
	}
	if got := levels[1].Privileges; got != "此等级才为正式会员。" {
		t.Errorf("KiloByte 权限应去掉行内条件：%q", got)
	}
	for _, level := range levels {
		if level.KeepAlive != (level.Name == "BrontoByte") {
			t.Errorf("保号等级应为 BrontoByte，实际 %q keep=%v", level.Name, level.KeepAlive)
		}
	}
}

// 不可说（ssd）的双语 FAQ：中文等级表与英文等级表在同一个 <table> 里（32 行 = 中文 16 + 英文 16），
// 而且「提升和降级」标题与表格之间夹了一段两百多字的中文说明，超出旧的 400 字节窗口，
// 导致升级条件一直取的是后面英文段落那张表。
func TestBilingualLevelTableDedupeAndGap(t *testing.T) {
	explain := "等级的调整并非实时，系统每隔24小时执行一次用户等级的提升和降级。" +
		"用户等级的提升自第5周结束后开始。下述等级要求中的发布种子均指合规种子，" +
		"合规种子是指已审且非中性的活种。这里再补一些字数让标题到表格的间距超过四百字节，" +
		"以验证解析器不会因为中间夹了说明段落就跳到后面的英文表格去。"
	doc := `<span id="id22"><b>不同的用户等级代表了什么含义？ What are the different user classes?</b></span><br>
<table cellspacing="5" class="standard">
<tr><td class="rowfollow"><b class="Peasant_Name">吸血鬼(Peasant)</b></td><td class="rowfollow">被降级的用户，7天内不提升分享率会被踢。</td></tr>
<tr><td class="rowfollow"><b class="User_Name">新人(User)</b></td><td class="rowfollow">新用户的默认级别。</td></tr>
<tr><td class="rowfollow"><b class="Elite_Name">精英(Elite)</b></td><td class="rowfollow">可以查看排行榜。</td></tr>
<tr><td class="rowfollow"><b class="Peasant_Name">Peasant</b></td><td class="rowfollow">Demoted users.</td></tr>
<tr><td class="rowfollow"><b class="User_Name">User</b></td><td class="rowfollow">The default class.</td></tr>
<tr><td class="rowfollow"><b class="Elite_Name">Elite</b></td><td class="rowfollow">Can view Top 10.</td></tr>
</table>
<br /><span id="id23"><b>提升和降级又是怎样被执行的呢？ How does this promotion thing work anyway?</b></span><br>
<p>` + explain + `</p>
<table cellspacing="5" class="standard">
<tr><td class="rowfollow"><b class="Peasant_Name">吸血鬼(Peasant)</b></td><td class="rowfollow">下载超过 10GB 时分享率需大于 0.6。</td></tr>
<tr><td class="rowfollow"><b class="User_Name">新人(User)</b></td><td class="rowfollow">注册即得。</td></tr>
<tr><td class="rowfollow"><b class="Elite_Name">精英(Elite)</b></td><td class="rowfollow">分享率大于 1.2 且做种积分不少于 10 万。</td></tr>
</table>`

	levels := buildUserLevelsFromFAQ(doc, "")
	wantNames := []string{"吸血鬼(Peasant)", "新人(User)", "精英(Elite)"}
	if len(levels) != len(wantNames) {
		t.Fatalf("双语表格应去重成 %d 级，实际 %d：%+v", len(wantNames), len(levels), levels)
	}
	for index, want := range wantNames {
		if levels[index].Name != want {
			t.Errorf("第 %d 级应为 %q，实际 %q", index, want, levels[index].Name)
		}
	}
	if got := levels[2].Requirements; got != "分享率大于 1.2 且做种积分不少于 10 万" {
		t.Errorf("标题与表格间距超过 400 字节时应取中文条件表，实际 %q", got)
	}
}
