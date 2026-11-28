package siteprofile

import (
	"strconv"
	"strings"
	"testing"
)

// 青蛙的 FAQ 已 302 到 Nuxt Wiki，抓不到等级表，只能按内置表直出。
// 这里锁住「等级数、顺序、无保号、两条路线」这几件容易回归的事。
func TestBuiltinSiteLevelsQingwap(t *testing.T) {
	levels, ok := builtinSiteLevels("qingwapt")
	if !ok {
		t.Fatalf("qingwapt 应命中内置等级表")
	}
	if len(levels) != 14 {
		t.Fatalf("等级数 = %d，期望 14（Peasant..Ultimate + SVIP/VIP/Retiree/Uploader/Moderator）", len(levels))
	}
	for index, level := range levels {
		if level.Order != index {
			t.Fatalf("第 %d 级 order = %d", index, level.Order)
		}
		if level.KeepAlive {
			t.Fatalf("青蛙没有「永远保留账号」表述，%s 不应标保号", level.Name)
		}
	}
	if levels[0].Name != "Peasant(青蛙卵)" || levels[8].Name != "Ultimate User(极巨化妙蛙花)" {
		t.Fatalf("等级顺序不对：first=%s ninth=%s", levels[0].Name, levels[8].Name)
	}
	power := levels[2]
	if !strings.Contains(power.Requirements, "条件A-偷天蛙") || !strings.Contains(power.Requirements, "条件B-传火蛙") {
		t.Fatalf("Power User 应带两条路线，实际：%s", power.Requirements)
	}
	if !strings.Contains(power.Requirements, "35 天") {
		t.Fatalf("Power User 应带注册天数门槛，实际：%s", power.Requirements)
	}

	// 其它站点不应命中内置表（仍走 FAQ 解析）
	if _, ok := builtinSiteLevels("hdsky"); ok {
		t.Fatalf("非青蛙站点不应命中内置等级表")
	}
}

// 朱雀是自研接口站，getInfo 只给当前等级名，等级门槛写在站点规则页，
// 只能按内置表直出。这里锁住「8 级顺序、无保号、每级三项门槛」。
func TestBuiltinSiteLevelsZhuque(t *testing.T) {
	levels, ok := builtinSiteLevels("zhuque")
	if !ok {
		t.Fatalf("zhuque 应命中内置等级表")
	}
	want := []string{"筑基", "结丹", "元婴", "出窍", "炼虚", "合体", "大乘", "真仙"}
	if len(levels) != len(want) {
		t.Fatalf("等级数 = %d，期望 %d（筑基..真仙）", len(levels), len(want))
	}
	for index, level := range levels {
		if level.Order != index || level.Name != want[index] {
			t.Fatalf("第 %d 级 = %s(order %d)，期望 %s", index, level.Name, level.Order, want[index])
		}
		if level.KeepAlive {
			t.Fatalf("朱雀没有「永远保留账号」表述，%s 不应标保号", level.Name)
		}
	}
	// 结丹：5 周 / 10 GiB / 分享率 2
	first := levels[1]
	if !strings.Contains(first.Requirements, "5 周") || !strings.Contains(first.Requirements, "10 GiB") ||
		!strings.Contains(first.Requirements, "分享率大于 2") {
		t.Fatalf("结丹门槛不对：%s", first.Requirements)
	}
	// 真仙：35 周 / 400 GiB / 分享率 8
	last := levels[len(levels)-1]
	if !strings.Contains(last.Requirements, "35 周") || !strings.Contains(last.Requirements, "400 GiB") ||
		!strings.Contains(last.Requirements, "分享率大于 8") {
		t.Fatalf("真仙门槛不对：%s", last.Requirements)
	}
}

// PTFans 的 FAQ 等级表能解析出 17 个等级，但「提升和降级」正文只写
// 「看置顶种子链接PU升级计划相关说明」，门槛文本为空；要用内置值把那 8 级补齐。
// 这里锁住「门槛被覆盖、权限与保号仍来自 FAQ、其它站点不受影响」。
func TestApplyBuiltinRequirementOverridesPtFans(t *testing.T) {
	faq := `<b>不同的用户等级代表了什么含义？</b><br />` + `<table>
<tr><td class="embedded"><b class="Peasant_Name">Peasant</b></td><td class="embedded">&nbsp;</td><td class="embedded">被降级的用户，他们有30天时间来提升分享率。</td></tr>
<tr><td class="embedded"><b class="User_Name">User</b></td><td class="embedded">&nbsp;</td><td class="embedded">新用户的默认级别。</td></tr>
<tr><td class="embedded"><b class="PowerUser_Name">Power User</b></td><td class="embedded">&nbsp;</td><td class="embedded">得到一个邀请名额。</td></tr>
<tr><td class="embedded"><b class="EliteUser_Name">Elite User</b></td><td class="embedded">&nbsp;</td><td class="embedded">Elite User 及以上用户封存账号后不会被删除。</td></tr>
<tr><td class="embedded"><b class="CrazyUser_Name">Crazy User</b></td><td class="embedded">&nbsp;</td><td class="embedded">得到两个邀请名额。</td></tr>
<tr><td class="embedded"><b class="InsaneUser_Name">Insane User</b></td><td class="embedded">&nbsp;</td><td class="embedded">可以查看普通日志。</td></tr>
<tr><td class="embedded"><b class="VeteranUser_Name">Veteran User</b></td><td class="embedded">&nbsp;</td><td class="embedded">Veteran User 及以上用户会永远保留账号。</td></tr>
<tr><td class="embedded"><b class="ExtremeUser_Name">Extreme User</b></td><td class="embedded">&nbsp;</td><td class="embedded">可以更新过期的外部信息。</td></tr>
<tr><td class="embedded"><b class="UltimateUser_Name">Ultimate User</b></td><td class="embedded">&nbsp;</td><td class="embedded">得到五个邀请名额。</td></tr>
<tr><td class="embedded"><b class="NexusMaster_Name">Nexus Master</b></td><td class="embedded">&nbsp;</td><td class="embedded">得到十个邀请名额。</td></tr>
</table>`

	levels := buildUserLevelsFromFAQ(faq, "")
	for _, level := range levels {
		if level.Requirements != "" {
			t.Fatalf("PTFans 的 FAQ 本身没有门槛文本，%s 不应解析出 requirements：%s", level.Name, level.Requirements)
		}
	}

	applyBuiltinRequirementOverrides("ptfans", levels)

	byName := map[string]UserLevel{}
	for _, level := range levels {
		byName[level.Name] = level
	}
	power := byName["Power User"]
	if !strings.Contains(power.Requirements, "4 周") || !strings.Contains(power.Requirements, "40000") ||
		!strings.Contains(power.Requirements, "50 GB") || !strings.Contains(power.Requirements, "1.05") {
		t.Fatalf("Power User 门槛不对：%s", power.Requirements)
	}
	// 站点值不是 NexusPHP 标准值（做种积分 14888 / 下载量 1024GB），必须用站点自己的。
	if strings.Contains(power.Requirements, "14888") {
		t.Fatalf("Power User 用了内置标准值：%s", power.Requirements)
	}
	master := byName["Nexus Master"]
	for _, want := range []string{"100 周", "1000000", "3072 GB", "4.55"} {
		if !strings.Contains(master.Requirements, want) {
			t.Fatalf("Nexus Master 门槛缺少 %q：%s", want, master.Requirements)
		}
	}
	// 权限文案与保号来自 FAQ，覆盖门槛不能把它们清掉。
	if !strings.Contains(power.Privileges, "邀请名额") {
		t.Fatalf("覆盖门槛后权限文案丢了：%q", power.Privileges)
	}
	if !byName["Veteran User"].KeepAlive {
		t.Fatalf("保号仍应来自 FAQ 的「Veteran User 及以上用户会永远保留账号」")
	}

	// 同一份等级表换别的站点不能被改写。
	other := buildUserLevelsFromFAQ(faq, "")
	applyBuiltinRequirementOverrides("hdsky", other)
	for _, level := range other {
		if level.Requirements != "" {
			t.Fatalf("非 PTFans 站点不应被内置门槛覆盖：%s → %s", level.Name, level.Requirements)
		}
	}
}

// 杜比的 FAQ 门槛是旧值（16 周/380000 积分），且把「做种积分」写成「种积分」，
// 前端匹配不到；要用站点当前规则表覆盖。这里锁住「周数与积分被抬高、保号仍在 Extreme User」。
func TestApplyBuiltinRequirementOverridesHddolby(t *testing.T) {
	levels := []UserLevel{
		{Name: "Peasant", Order: 0},
		{Name: "User", Order: 1},
		{Name: "Power User", Order: 2, Requirements: "必须注册至少2周，并且下载至少120G，分享率大于2.0，种积分大于48000积分。"},
		{Name: "Elite User", Order: 3},
		{Name: "Crazy User", Order: 4},
		{Name: "Insane User", Order: 5},
		{Name: "Veteran User", Order: 6, Requirements: "必须注册至少16周，并且下载至少1TB，分享率大于4.0，种积分大于380000积分。"},
		{Name: "Extreme User", Order: 7, KeepAlive: true},
		{Name: "Ultimate User", Order: 8},
		{Name: "Nexus Master", Order: 9},
		{Name: "贵宾(VIP)", Order: 10},
	}
	applyBuiltinRequirementOverrides("hddolby", levels)

	byName := map[string]UserLevel{}
	for _, level := range levels {
		byName[level.Name] = level
	}
	power := byName["Power User"].Requirements
	for _, want := range []string{"2 周", "60000", "120 GB", "2.0"} {
		if !strings.Contains(power, want) {
			t.Fatalf("Power User 门槛缺少 %q：%s", want, power)
		}
	}
	// 旧值是 48000 积分，站点现在要 60000。
	if strings.Contains(power, "48000") {
		t.Fatalf("Power User 仍是 FAQ 旧值：%s", power)
	}
	// 门槛文本必须写成「做种积分」，前端才认得出这一项。
	if !strings.Contains(power, "做种积分") {
		t.Fatalf("门槛应写「做种积分」而不是「种积分」：%s", power)
	}
	if !strings.Contains(byName["Veteran User"].Requirements, "20 周") {
		t.Fatalf("Veteran User 应为 20 周（FAQ 旧值是 16 周）：%s", byName["Veteran User"].Requirements)
	}
	master := byName["Nexus Master"].Requirements
	for _, want := range []string{"56 周", "1680000", "8192 GB", "5.5"} {
		if !strings.Contains(master, want) {
			t.Fatalf("Nexus Master 门槛缺少 %q：%s", want, master)
		}
	}
	if !byName["Extreme User"].KeepAlive {
		t.Fatalf("「Extreme User 及以上等级用户会永远保留」⇒ 保号应在 Extreme User")
	}
}

// 末日的 FAQ 只有 NexusPHP 老文案（4 周/50G/1.05），实际生效的是自定义「熊」等级体系，
// 要用内置值覆盖成站点真实门槛。这里锁住「Power User 没有注册周数要求、周数/分享率抬高、
// 权限与保号仍来自 FAQ」。
func TestApplyBuiltinRequirementOverridesAgsv(t *testing.T) {
	// 模拟末日 FAQ 解析出来的 10 级（门槛是老文案）：覆盖后必须被替换。
	levels := []UserLevel{
		{Name: "Peasant", Order: 0},
		{Name: "User", Order: 1},
		{Name: "Power User", Order: 2, Requirements: "必须注册至少4周，并且下载至少50G，分享率大于1.05。"},
		{Name: "Elite User", Order: 3, Requirements: "必须注册至少8周，并且下载至少120G，分享率大于1.55。"},
		{Name: "Crazy User", Order: 4},
		{Name: "Insane User", Order: 5},
		{Name: "Veteran User", Order: 6, KeepAlive: true},
		{Name: "Extreme User", Order: 7},
		{Name: "Ultimate User", Order: 8},
		{Name: "Nexus Master", Order: 9},
		{Name: "贵宾(VIP)", Order: 10},
	}
	applyBuiltinRequirementOverrides("agsv", levels)

	byName := map[string]UserLevel{}
	for _, level := range levels {
		byName[level.Name] = level
	}
	power := byName["Power User"].Requirements
	if !strings.Contains(power, "40000") || !strings.Contains(power, "50 GB") || !strings.Contains(power, "1.05") {
		t.Fatalf("Power User 门槛不对：%s", power)
	}
	// 末日 Power User 只考做种积分/下载量/分享率，没有注册周数要求。
	if strings.Contains(power, "周") {
		t.Fatalf("Power User 不应有注册周数要求：%s", power)
	}
	if !strings.Contains(byName["Crazy User"].Requirements, "12 周") {
		t.Fatalf("Crazy User 应为 12 周（不是标准表的 15 周）：%s", byName["Crazy User"].Requirements)
	}
	if !strings.Contains(byName["Veteran User"].Requirements, "4.05") {
		t.Fatalf("Veteran User 分享率应为 4.05：%s", byName["Veteran User"].Requirements)
	}
	master := byName["Nexus Master"].Requirements
	for _, want := range []string{"70 周", "3000000", "3072 GB", "7.05"} {
		if !strings.Contains(master, want) {
			t.Fatalf("Nexus Master 门槛缺少 %q：%s", want, master)
		}
	}
	// 不在 specs 里的等级（Peasant / User / VIP）不应被写入门槛。
	for _, name := range []string{"Peasant", "User", "贵宾(VIP)"} {
		if byName[name].Requirements != "" {
			t.Fatalf("%s 不应被内置门槛覆盖：%s", name, byName[name].Requirements)
		}
	}
	if !byName["Veteran User"].KeepAlive {
		t.Fatalf("保号标记应保留 FAQ 的结果")
	}
}

// YemaPT 的等级只有一个裸数字（level: 2），门槛写在站点规则页，
// 按内置表直出 12 级。这里锁住「级数、Level N 命名、降级级不做门槛、月均下载量在要求里」。
func TestBuiltinSiteLevelsYemaPT(t *testing.T) {
	levels, ok := builtinSiteLevels("yemapt")
	if !ok {
		t.Fatalf("yemapt 应命中内置等级表")
	}
	if len(levels) != 12 {
		t.Fatalf("等级数 = %d，期望 12（Level 0 ~ Level 11）", len(levels))
	}
	for index, level := range levels {
		want := "Level " + strconv.Itoa(index)
		if level.Order != index || level.Name != want {
			t.Fatalf("第 %d 级 = %s(order %d)，期望 %s", index, level.Name, level.Order, want)
		}
		if level.KeepAlive {
			t.Fatalf("YemaPT 没有「永远保留账号」表述，%s 不应标保号", level.Name)
		}
	}
	// level 0 / level 1 是降级规则，必须带「自动降至本级」标记，否则前端会把
	// 「分享率低于 0.6」解析成虚假的升级进度。
	for _, index := range []int{0, 1} {
		if !strings.Contains(levels[index].Requirements, "自动降至本级") {
			t.Fatalf("%s 应标注为降级规则：%s", levels[index].Name, levels[index].Requirements)
		}
	}
	// level 11：3000 GB / 分享率 9 / 240 天 / 月均 70 GB
	last := levels[len(levels)-1]
	for _, want := range []string{"3000 GB", "分享率大于 9", "240 天", "月均下载量超过 70 GB"} {
		if !strings.Contains(last.Requirements, want) {
			t.Fatalf("Level 11 缺少 %q：%s", want, last.Requirements)
		}
	}
}
