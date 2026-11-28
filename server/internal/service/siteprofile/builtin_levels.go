package siteprofile

import "strings"

// builtinLevelSpec 描述「站点抓不到等级表」时内置的一行等级。
type builtinLevelSpec struct {
	Name         string
	Requirements string
	Privileges   string
}

// qingwapLevelSpecs 是 青蛙（qingwapt / www.qingwapt.com）的等级体系。
//
// 为什么要内置：该站的 faq.php 已 302 到 wiki.qingwapt.org/faq/…，正文是 Nuxt 的
// 内容数据库（doc body 里等级名只有 `["user-level",{"level":"power"}]` 这类英文键，
// 中文名与门槛由前端组件渲染），因此 FAQ 抓取既拿不到中文等级名也拿不到门槛，
// 之前只能回落到 NexusPHP 内置 10 级标准表，等级、升级条件、保号全都不对。
// 门槛原文由用户 2026-11-27 提供（青蛙 Wiki「不同的用户等级代表了什么含义？」）。
//
// 保号：该站没有任何「永远保留账号」的表述，因此全部 KeepAlive=false
// （不能沿用标准表的 Nexus Master 保号，那会把保号线标到错误等级）。
var qingwapLevelSpecs = []builtinLevelSpec{
	{
		Name: "Peasant(青蛙卵)",
		Requirements: "当以下情况时将被自动降至本级：已下载超过 10GB 且分享率小于 0.2；" +
			"已下载超过 50GB 且分享率小于 0.3；已下载超过 100GB 且分享率小于 0.4；" +
			"已下载超过 200GB 且分享率小于 0.6；已下载超过 400GB 且分享率小于 0.6。" +
			"若不尽快改善分享率，你的账号将在一段时间后被禁用。",
	},
	{
		Name:         "User(蝌蚪)",
		Requirements: "若你未能达成其他任何用户组的条件，则会变成此用户组。",
	},
	{
		Name: "Power User(幼蛙)",
		Requirements: "必须注册已至少 35 天，分享率在 1.0 以上，并满足以下任意 1 个条件：" +
			"条件A-偷天蛙：发种大于 50 个，做种积分大于 40000。 " +
			"条件B-传火蛙：下载至少 150G，分享率小于 4.0，并且做种积分大于 40000。",
	},
	{
		Name: "Elite User(成蛙)",
		Requirements: "必须注册已至少 112 天，分享率在 1.0 以上，并满足以下任意 1 个条件：" +
			"条件A-偷天蛙：发种大于 120 个，做种积分大于 70000。 " +
			"条件B-传火蛙：下载至少 300G，分享率小于 4.5，并且做种积分大于 80000。",
	},
	{
		Name: "Crazy User(妙蛙种子)",
		Requirements: "必须注册已至少 175 天，分享率在 1.0 以上，并满足以下任意 1 个条件：" +
			"条件A-偷天蛙：发种大于 200 个，做种积分大于 90000。 " +
			"条件B-传火蛙：下载至少 1024G，分享率小于 5.0，并且做种积分大于 160000。",
	},
	{
		Name: "Insane User(妙蛙草)",
		Requirements: "必须注册已至少 252 天，分享率在 1.0 以上，并满足以下任意 1 个条件：" +
			"条件A-偷天蛙：发种大于 300 个，做种积分大于 150000。 " +
			"条件B-传火蛙：下载至少 2048G，分享率小于 5.5，并且做种积分大于 320000。",
	},
	{
		Name: "Veteran User(妙蛙花)",
		Requirements: "必须注册已至少 364 天，分享率在 1.0 以上，并满足以下任意 1 个条件：" +
			"条件A-偷天蛙：发种大于 500 个，做种积分大于 200000。 " +
			"条件B-传火蛙：下载至少 4096G，分享率小于 6.0，并且做种积分大于 640000。",
	},
	{
		Name: "Extreme User(mega妙蛙花)",
		Requirements: "必须注册已至少 420 天，分享率在 1.0 以上，并满足以下任意 1 个条件：" +
			"条件A-偷天蛙：发种大于 650 个，做种积分大于 280000。 " +
			"条件B-传火蛙：下载至少 8192G，分享率小于 6.5，并且做种积分大于 1280000。",
	},
	{
		Name: "Ultimate User(极巨化妙蛙花)",
		Requirements: "必须注册已至少 700 天，分享率在 1.0 以上，并满足以下任意 1 个条件：" +
			"条件A-偷天蛙：发种大于 800 个，做种积分大于 400000。 " +
			"条件B-传火蛙：下载至少 16384G，分享率小于 7.0，并且做种积分大于 2560000。",
	},
	{
		Name:         "SVIP(闪光妙蛙花)",
		Requirements: "已绝版，无法以任何方式获取。",
	},
	{
		Name:         "VIP(贵宾)",
		Requirements: "可通过向公益组织捐赠，或者用蝌蚪兑换获取；到期后会降级至对应等级。",
	},
	{
		Name:         "Retiree(养老族)",
		Requirements: "工作组退休待遇，由管理员授予。",
	},
	{
		Name:         "Uploader(发布员)",
		Requirements: "论坛提交工作组申请，由管理员审批之后分配。",
	},
	{
		Name:         "Moderator(总版主)",
		Requirements: "如果条件满足，我们会主动联系。",
	},
}

// zhuqueLevelSpecs 是 朱雀（zhuque）的等级体系。
//
// 为什么要内置：朱雀是自研接口站，`/api/user/getInfo` 只返回当前等级名（如「真仙」），
// 没有任何等级体系接口，之前只能沿用一个「仅同步当前等级」的占位行，等级阶梯和升级进度全空。
// 门槛原文由用户 2026-11-27 提供（站点规则页：筑基 → 结丹 → 元婴 → 出窍 → 炼虚 → 合体 → 大乘 → 真仙）。
//
// 保号：站点规则里「分享率低于 X 自动降级」是降级规则，没有「永远保留账号」的表述，
// 因此全部 KeepAlive=false。
var zhuqueLevelSpecs = []builtinLevelSpec{
	{
		Name:         "筑基",
		Requirements: "欢迎进入朱雀。",
	},
	{
		Name:         "结丹",
		Requirements: "注册时间大于 5 周，下载量大于 10 GiB，且分享率大于 2；如果你的分享率低于 1.95，你将自动降级。",
	},
	{
		Name:         "元婴",
		Requirements: "注册时间大于 10 周，下载量大于 25 GiB，且分享率大于 2；如果你的分享率低于 1.95，你将自动降级。",
	},
	{
		Name:         "出窍",
		Requirements: "注册时间大于 15 周，下载量大于 50 GiB，且分享率大于 2；如果你的分享率低于 1.95，你将自动降级。",
	},
	{
		Name:         "炼虚",
		Requirements: "注册时间大于 20 周，下载量大于 100 GiB，且分享率大于 2；如果你的分享率低于 1.95，你将自动降级。",
	},
	{
		Name:         "合体",
		Requirements: "注册时间大于 25 周，下载量大于 200 GiB，且分享率大于 4；如果你的分享率低于 3.95，你将自动降级。",
	},
	{
		Name:         "大乘",
		Requirements: "注册时间大于 30 周，下载量大于 300 GiB，且分享率大于 6；如果你的分享率低于 5.95，你将自动降级。",
	},
	{
		Name:         "真仙",
		Requirements: "注册时间大于 35 周，下载量大于 400 GiB，且分享率大于 8；如果你的分享率低于 7.95，你将自动降级。",
	},
}

// yemaptLevelSpecs 是 YemaPT（yemapt）的等级体系。
//
// 为什么要内置：YemaPT 的接口只给一个裸数字（`level: 2`），站点里也没有等级名，
// 之前只能补一条「Level 2」的占位行，等级阶梯与升级进度全空。门槛写在站点规则页，
// 由用户 2026-11-27 提供（level 0 ~ level 11）。
//
// level 0 / level 1 写的是「被降级」的条件（低分享率惩罚），按青蛙同口径加上
// 「当以下情况时将被自动降至本级」前缀，让前端识别成降级规则、只展示原文，
// 不会把「分享率低于 0.6」解析成虚假的升级进度。
//
// 保号：站点没有任何「永远保留账号」的表述，因此全部 KeepAlive=false。
var yemaptLevelSpecs = []builtinLevelSpec{
	{
		Name:         "Level 0",
		Requirements: "当以下情况时将被自动降至本级：下载量超过 10 GB，且分享率（上传量/下载量）低于 0.3。",
	},
	{
		Name: "Level 1",
		Requirements: "新注册后默认为本级。当以下情况时将被自动降至本级：" +
			"下载量超过 10 GB 且分享率低于 0.6；下载量超过 50 GB 且分享率低于 0.7；" +
			"下载量超过 100 GB 且分享率低于 0.8；下载量超过 200 GB 且分享率低于 0.9；" +
			"下载量超过 500 GB 且分享率低于 1.0。",
	},
	{
		Name:         "Level 2",
		Requirements: "不符合其他规则时，默认为本级。",
	},
	{
		Name:         "Level 3",
		Requirements: "下载量大于 100 GB，分享率大于 1。",
	},
	{
		Name:         "Level 4",
		Requirements: "下载量大于 200 GB，分享率大于 2，注册超过 30 天，且月均下载量超过 5 GB。",
	},
	{
		Name:         "Level 5",
		Requirements: "下载量大于 400 GB，分享率大于 3，注册超过 60 天，且月均下载量超过 10 GB。",
	},
	{
		Name:         "Level 6",
		Requirements: "下载量大于 500 GB，分享率大于 4，注册超过 90 天，且月均下载量超过 15 GB。",
	},
	{
		Name:         "Level 7",
		Requirements: "下载量大于 800 GB，分享率大于 5，注册超过 120 天，且月均下载量超过 20 GB。",
	},
	{
		Name:         "Level 8",
		Requirements: "下载量大于 1000 GB，分享率大于 6，注册超过 150 天，且月均下载量超过 25 GB。",
	},
	{
		Name:         "Level 9",
		Requirements: "下载量大于 2000 GB，分享率大于 7，注册超过 180 天，且月均下载量超过 50 GB。",
	},
	{
		Name:         "Level 10",
		Requirements: "下载量大于 2500 GB，分享率大于 8，注册超过 210 天，且月均下载量超过 60 GB。",
	},
	{
		Name:         "Level 11",
		Requirements: "下载量大于 3000 GB，分享率大于 9，注册超过 240 天，且月均下载量超过 70 GB。",
	},
}

// ptfansRequirementSpecs 是 PTFans（ptfans.cc）的升级门槛。
//
// 为什么要内置：PTFans 的 FAQ 等级表（17 级，含 VIP/发布员/版主等）能正常解析出等级名与权限，
// 但「提升和降级又是怎样被执行的呢？」那一段正文只有一句
// 「看置顶种子链接PU升级计划相关说明，details.php?id=68」——真正的门槛贴在那条种子的简介里
// （站点后台「等级别名 + 注册时间/做种积分/下载量/分享率」的设置截图），页面没有任何可解析文本。
// 于是 17 个等级全解析出来了，requirements 却全空，等级弹窗只剩等级名、看不到升级进度。
// 数值由用户 2026-11-27 提供（PU 升级计划：Power User → Nexus Master 八级）。
//
// 与 NexusPHP 标准值的差别：分享率门槛与本级一致，但做种积分（40000/80000/…/1000000）
// 与下载量（50/120/300/500/750/1024/1536/3072 GB）都是站点自己调高的，
// 不能沿用内置标准表的 14888/29888/… 与 1024GB 那一套。
//
// 保号：站点 FAQ 写明「Veteran User 及以上用户会永远保留账号」，
// 由 markKeepAliveLevelsFromRows 从 FAQ 权限列解析，这里不覆盖。
var ptfansRequirementSpecs = []builtinLevelSpec{
	{Name: "Power User", Requirements: "注册时间大于 4 周，做种积分大于 40000，下载量大于 50 GB，且分享率大于 1.05"},
	{Name: "Elite User", Requirements: "注册时间大于 8 周，做种积分大于 80000，下载量大于 120 GB，且分享率大于 1.55"},
	{Name: "Crazy User", Requirements: "注册时间大于 15 周，做种积分大于 150000，下载量大于 300 GB，且分享率大于 2.05"},
	{Name: "Insane User", Requirements: "注册时间大于 25 周，做种积分大于 250000，下载量大于 500 GB，且分享率大于 2.55"},
	{Name: "Veteran User", Requirements: "注册时间大于 40 周，做种积分大于 400000，下载量大于 750 GB，且分享率大于 3.05"},
	{Name: "Extreme User", Requirements: "注册时间大于 60 周，做种积分大于 600000，下载量大于 1024 GB，且分享率大于 3.55"},
	{Name: "Ultimate User", Requirements: "注册时间大于 80 周，做种积分大于 800000，下载量大于 1536 GB，且分享率大于 4.05"},
	{Name: "Nexus Master", Requirements: "注册时间大于 100 周，做种积分大于 1000000，下载量大于 3072 GB，且分享率大于 4.55"},
}

// agsvRequirementSpecs 是 末日（agsv / pt.agsvpt.cn）的升级门槛。
//
// 为什么要内置：末日的 faq.php 里「提升和降级」还是 NexusPHP 的老文案（「必须注册至少 4 周，
// 并且下载至少 50G，分享率大于 1.05」），而站点实际生效的是自定义的「熊」等级体系 ——
// 门槛里加了做种积分、注册周数与分享率整体调高，等级还带别名（User 小熊 / Power User 北冰珍珠熊 /
// Elite User 深渊蔚蓝熊 / Crazy User 翡翠森林熊 / Insane User 神秘紫晶熊 / Veteran User 寒冰白金熊 /
// Extreme User 皇家金辉熊 / Ultimate User 永恒铂金熊 / Nexus Master 钻石之冠北极熊）。
// 这套配置只出现在站点后台的等级设置里，faq.php / rules.php 都抓不到（两页都没有「做种积分」「等级别名」），
// 不加内置值就会一直显示老文案，等级弹窗的进度全按 NexusPHP 默认值算，做出错误的达成判断。
// 数值由用户 2026-11-27 提供（末日 等级规则）。
//
// ⚠️ 与 NexusPHP 标准规则的差别（别拿标准表套）：
//   - Power User 没有注册周数要求，只考做种积分 40000 / 下载量 50GB / 分享率 1.05；
//   - 注册周数 8/12/20/28/40/52/70（标准是 8/15/25/40/60/80/100）；
//   - 做种积分 40000/80000/150000/400000/800000/1200000/2000000/3000000（标准表是 14888 起）；
//   - 分享率 1.05/1.55/2.05/2.55/4.05/5.05/6.05/7.05（Veteran 起就跳到 4.05）。
//
// 保号：站点 FAQ 写明「Veteran User 及以上用户会永远保留账号」，由 FAQ 权限列解析，这里不覆盖。
var agsvRequirementSpecs = []builtinLevelSpec{
	{Name: "Power User", Requirements: "做种积分大于 40000，下载量大于 50 GB，且分享率大于 1.05"},
	{Name: "Elite User", Requirements: "注册时间大于 8 周，做种积分大于 80000，下载量大于 120 GB，且分享率大于 1.55"},
	{Name: "Crazy User", Requirements: "注册时间大于 12 周，做种积分大于 150000，下载量大于 300 GB，且分享率大于 2.05"},
	{Name: "Insane User", Requirements: "注册时间大于 20 周，做种积分大于 400000，下载量大于 500 GB，且分享率大于 2.55"},
	{Name: "Veteran User", Requirements: "注册时间大于 28 周，做种积分大于 800000，下载量大于 750 GB，且分享率大于 4.05"},
	{Name: "Extreme User", Requirements: "注册时间大于 40 周，做种积分大于 1200000，下载量大于 1024 GB，且分享率大于 5.05"},
	{Name: "Ultimate User", Requirements: "注册时间大于 52 周，做种积分大于 2000000，下载量大于 1536 GB，且分享率大于 6.05"},
	{Name: "Nexus Master", Requirements: "注册时间大于 70 周，做种积分大于 3000000，下载量大于 3072 GB，且分享率大于 7.05"},
}

// hddolbyRequirementSpecs 是 杜比（hddolby）的升级门槛。
//
// 为什么要内置：杜比的 faq.php 里门槛是「必须注册至少16周 … 种积分大于380000积分」这套旧值，
// 与站点当前生效的规则不一致（做种积分被整体抬高、Veteran 起的注册周数也拉长了，
// 例如 Veteran 16→20 周 / Extreme 20→28 周 / Ultimate 24→40 周 / Nexus Master 48→56 周）。
// 另外 FAQ 把「做种积分」写成「种积分」（少了「做」字），前端指标正则匹配不到，
// 弹窗里会少掉最关键的一项门槛。数值由用户 2026-11-27 提供（杜比 等级规则表）。
//
// 降级分享率（1.9/2.4/2.9/3.4/3.9/4.4/4.9/5.4）是降级线，不算升级门槛，不写进 requirements。
//
// 保号：站点写明「Extreme User 及以上等级用户会永远保留」，由 FAQ 权限列解析（Extreme User 那一行），
// 这里只覆盖门槛、不动 keep_alive。注：「Veteran User 及以上…封存账号后不会被删除」是封存豁免，不算保号。
var hddolbyRequirementSpecs = []builtinLevelSpec{
	{Name: "Power User", Requirements: "注册时间大于 2 周，做种积分大于 60000，下载量大于 120 GB，且分享率大于 2.0"},
	{Name: "Elite User", Requirements: "注册时间大于 4 周，做种积分大于 120000，下载量大于 256 GB，且分享率大于 2.5"},
	{Name: "Crazy User", Requirements: "注册时间大于 8 周，做种积分大于 240000，下载量大于 512 GB，且分享率大于 3.0"},
	{Name: "Insane User", Requirements: "注册时间大于 12 周，做种积分大于 360000，下载量大于 768 GB，且分享率大于 3.5"},
	{Name: "Veteran User", Requirements: "注册时间大于 20 周，做种积分大于 600000，下载量大于 1024 GB，且分享率大于 4.0"},
	{Name: "Extreme User", Requirements: "注册时间大于 28 周，做种积分大于 720000，下载量大于 2048 GB，且分享率大于 4.5"},
	{Name: "Ultimate User", Requirements: "注册时间大于 40 周，做种积分大于 1200000，下载量大于 4096 GB，且分享率大于 5.0"},
	{Name: "Nexus Master", Requirements: "注册时间大于 56 周，做种积分大于 1680000，下载量大于 8192 GB，且分享率大于 5.5"},
}

// builtinRequirementSpecs 返回「等级表能抓到、门槛抓不到」的站点的内置升级条件。
// 参数/返回：siteCode 为站点代码或昵称；返回覆盖用的一行行等级门槛（没有则 nil）。
// 失败场景：无。
// 副作用：无。
func builtinRequirementSpecs(siteCode string) []builtinLevelSpec {
	code := strings.ToLower(strings.TrimSpace(siteCode))
	switch {
	case code == "ptfans" || strings.Contains(code, "ptfans"):
		return ptfansRequirementSpecs
	case code == "agsv" || strings.Contains(code, "末日"):
		return agsvRequirementSpecs
	case code == "hddolby" || strings.Contains(code, "杜比"):
		return hddolbyRequirementSpecs
	default:
		return nil
	}
}

// applyBuiltinRequirementOverrides 用内置的门槛覆盖 FAQ 解析出的空 requirements。
//
// 只覆盖 specs 里点名的那些等级，等级名/权限/顺序/保号标记全部保留 FAQ 的解析结果 ——
// 这些站点的 FAQ 等级表本身是准的，缺的只是那几行门槛文本。
func applyBuiltinRequirementOverrides(siteCode string, levels []UserLevel) {
	specs := builtinRequirementSpecs(siteCode)
	if len(specs) == 0 || len(levels) == 0 {
		return
	}
	byKey := indexLevelsByKey(levels)
	for _, spec := range specs {
		if index, ok := lookupLevelIndex(byKey, spec.Name); ok {
			levels[index].Requirements = spec.Requirements
		}
	}
}

// builtinSiteLevels 返回站点内置的等级体系（青蛙、朱雀、YemaPT 这类「站点自己抓不到等级表」的站）。
// 参数/返回：siteCode 为站点代码或昵称；返回等级列表与是否存在内置表。
// 失败场景：无。
// 副作用：无。
func builtinSiteLevels(siteCode string) ([]UserLevel, bool) {
	code := strings.ToLower(strings.TrimSpace(siteCode))
	var specs []builtinLevelSpec
	switch {
	case code == "qingwapt" || strings.Contains(code, "青蛙"):
		specs = qingwapLevelSpecs
	case code == "zhuque" || strings.Contains(code, "朱雀"):
		specs = zhuqueLevelSpecs
	case code == "yemapt" || strings.Contains(code, "yema"):
		specs = yemaptLevelSpecs
	default:
		return nil, false
	}
	levels := make([]UserLevel, 0, len(specs))
	for index, spec := range specs {
		levels = append(levels, UserLevel{
			Name:         spec.Name,
			Order:        index,
			Requirements: spec.Requirements,
			Privileges:   spec.Privileges,
			// 站点没有「永远保留账号」表述，统一不标保号，交给用户手动指定。
			KeepAlive: false,
		})
	}
	return levels, true
}
