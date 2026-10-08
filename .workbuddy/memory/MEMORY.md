# PTNexus 长期约定

> 细节查技能：`ptn-deploy` / `ptn-proxy-release` / `github-release` / `ptn-desc-clean` / `ptn-downloader-scope` / `ptn-publish-pacing` / `ptn-publish-progress` / `ptn-publish-field-diagnose` / `ptn-publish-site-setup`（含 `references/known_sites.md`）/ `ptn-manual-screenshots` / `ffmpeg-hdr-frame-capture`。站点映射真值 = `server/configs/<site>.yaml`。

## 环境/流程
- 主工作区直接改、不建 worktree；改完给 `git status`/`git diff`；提交/回滚/改历史须当轮明确要求。
- 工具链：Bash 先 export PortableGit PATH；Go `/d/go/go1.25.11/bin/go`、pnpm `~/AppData/Roaming/npm/pnpm`、pymysql venv `C:/Users/wind/.workbuddy/binaries/python/envs/default/Scripts/python.exe`。
- 校验：前端 `pnpm -C webui run type-check`（`lint` 有存量报错、改动不得新增）；后端 `go build ./...` + `go vet`。`core.autocrlf=true` → 工作区 CRLF 属正常，**gofmt 前先 `tr -d '\r'`**，改写保持原行尾。⚠️ **绝不对目录跑 `gofmt -w <dir>`**（已踩两次）：它会把目录内所有文件的行尾从 CRLF 改成 LF，凭空多出一批「只有行尾差异」的改动；只对本次真正改动的文件逐个执行。
- 临时测试写 `zz_tmp_*_test.go`，验完备份 `.workbuddy/tmp/test-backup/` 再删；`-mod=mod` 会污染 go.mod，用完还原。
- 通知走 `wsl-win-notify` skill；本机 toast 通道（powershell.exe）会挂起被 timeout 杀掉，须加 `--channel msg`。

## 发布/数据
- `5.135.178.15`（OVH，`/home/code/PTNexus`）是**构建推送机**、非生产：只 buildx push Docker Hub `fenglaile/ptnexus`（仅 latest）；动手前查 `ps -ef | grep '[b]uildx build'`，`ss` 看不到容器属正常。
- ⚠️ 容器 `/etc/localtime` 仍 UTC → DSN 必须显式 `loc=Asia/Shanghai`。
- 内网 DB `192.168.5.3:3306/pt_nexus`（root/111111，用 pymysql）。Release 走 skill `github-release`（publish.sh 末尾 trap 被拦 → 退出码 1 但已上传成功）。
- 进度/日志列表「大小」两表都无 size 列、查询时旁路关联 hash → `torrents.size`（日志页覆盖 ~63%）；`service/migrationflow/publish_size.go`，DTO `gorm:"-"`，`SizeByHashes` 刻意不过滤 is_hidden。

## 配置/下载器/代理
- ⚠️ **DB 读出来的开关值类型是 int8**：MySQL `TINYINT(1)` 经 GORM `Scan(&map)` 返回 **`int8`**（SQLite 给 int64、PG 可能 int32、原始 SQL 扫描给 `[]byte`）。**任何 `switch v.(type)` 式转换都必须覆盖全部整型宽度 + bool + string + []byte** —— dupe 的 `toBool` 早期只写 int/int64/float64/string，导致「开关开着也被读成 false、dupe 校验整体静默失效」，而日志只留「未开启」一句，极难察觉。`repository/value_helpers.go:toInt64` 是完整实现，可作参照。
- 配置存 DB `app_settings`（DB 优先）；Docker 下改 `server/data/config.json` 无效，要改文件即生效用环境变量。
- ⚠️ **MySQL DSN 未开 `clientFoundRows`**（`repository/db.go`）→ `UPDATE` 提交值与库内完全相同时 `RowsAffected=0`。**因此所有「用 `RowsAffected>0` 判断记录是否存在」的 UPDATE 都是错的**：会把「没改任何配置直接保存」误报成「站点不存在」。已修 `site_repository.go` 的 `UpdateSiteDetails` / `UpdateSiteCookie` / `UpdateSiteCookieBySite`（改为按主键/nickname 回查 `siteExists`）；`DeleteSite` 用 RowsAffected 是对的（删 0 行确实不存在）。同理 `sites_handler.go:UpdateSite` 已把 `err` 与 `!updated` 拆开，DB 错误回 500 如实报，不再与「未找到站点」混成同一条 404 文案。
- 菜单显隐 `PTNEXUS_SHOW_*` → `menu_visibility.go` → 前端 `App.vue` `v-if`（只隐藏入口不拦路由）。
- 顶部「全局下载器」是全站下载器筛选唯一来源（页面内入口已移除、`onMounted` 无条件覆盖）。
- 站点角色 `sites_data.json:migration`（0 不参与/1 仅源/2 仅目标/3 双向，bit0 源 bit1 目标）；唯一写入是启动 `SyncSitesFromJSON`，门禁在 `source_site.go` / `publish_with_context.go`（位不对 → 403）。⚠️ 缺 `migration` 会被写成 0；该 JSON 不能含注释。
- 下载器配置字段名是 **`host`**（写 `url` → Host 空 → 首页「连接失败」）。
- **代理路径探测**：候选真源 `downloaderclient/proxy_paths.go:BuildProxyPathCandidates`，调用方必须传两个根 `[下载器原始路径, 映射后路径]`；四条链路 refresh_flow / screenshot(_preview) / bdinfo_task_ops / episode_count。⚠️ 候选循环**只在 400 continue、其余 break** → 代理的 stat 失败必须经 `proxy_error.go:NormalizeProxyCandidateStatus` + `newProxyHTTPError/newProxyResponseFailure` 归一为 400，新增代理接口必走这两个构造器；代理不可达仍 500 → 立即 break。
- 本地回退门禁 `shouldSkipLocalMediaFallback` / `shouldSkipLocalScreenshotFallback`（use_proxy=true 且映射后本地根不存在 → 报「本地路径不可访问」）。

## 发种链路
- 唯一入口 `workflow/publish_entry.go` → `publish_target.go:PublishTorrentToTarget`；NexusPHP 顺序 PublishPublic → setField → ResolveBasicPublishMappings → ExtraFormFields → AdjustFormFields。日志 `region=<字段>:<值>` 是排查第一现场。
- ⚠️ `mappings.<类目>` 直接提交站点 select，须对齐 upload.php 实测 value；`default` 兜底会掩盖错值、`@index:N` 越界静默退化。站点不存在的字段被静默丢弃 → 在 `AdjustFormFields` 回填/清理。
- ⚠️ 三条铁律：① 站点硬性拒绝用 `*publisher.PreCheckError`（`publish_entry.go` 识别后返回 `pre_check+limit_reached` 让队列改判跳过；用 `errors.New` 会变 500 污染统计）② 语种以 MediaInfo 音轨为准（`tagging/completion.go:ReconcileAudioLanguageTagsWithMediaText`），**不与源站标签取并集** ③ 动画标签优先于类型字段。`PreCheckError` 带 `Detail` 字段承载判定过程（`NewPreCheckErrorWithDetail` / `AsPreCheckErrorWithDetail`），`publish_entry.go:buildPreCheckFailure` 会把 detail 附在 message 后 → 拦截原因在「发布进度 → 日志」里可见，不必翻服务端日志。
- ⚠️ 标题默认沿用源标题，站点冲突在适配器 `normalizeXxxTitle` 纠偏；别改全局解析或 `global_mappings.yaml`，别在 Go 里重复 yaml 能表达的映射。
- 副标题整季合集补「第x季 全y集」（`seed_draft.go:CompleteAndMapTags` + `tagging/subtitle_season.go`），历史不回填。
- `IsExistingTorrent` → 日志 `exists` 不重试；非 200 / success=false 必判 failed；torrent_id ≠ infohash。
- 定时发种：入队即 `total_published++`；非成功结局经 ContinueHook → `ReclassifyPublishedAsSkipped` 改判 published-1/skipped+1 并继续。
- ⚠️ 队列行状态与日志状态是两套判定（日志靠 `queue_task_id` 反查，另有 `task_id`+`target_site` 兜底）；`trigger=batch_live` 曾不建日志且 payload 缺 `queue_task_id`（已修，历史行需回填 SQL）。
- **dupe 校验**（发布前查重）：`sites` 表 `dupe_check_enabled`(默认 0) + `dupe_size_tolerance_bytes`(默认 1 GiB，`repository.DefaultDupeSizeToleranceBytes` 为默认真源)；逻辑在 `publish/dupe/`，站点差异由 `CheckBySite` 分发（已接入 6 站：`audiences`/`luckpt`/`hdhome`/`pterclub`/`ourbits`/`chdbits`）；**站点是否参与由 `configs/<site>.yaml` 的 `dupe_check` 声明，钩子由 `engine.Publish` 入口的 `AttachSiteDupeCheck` 统一挂载 —— 新增站点只需写 YAML，不必碰 Go**。共用骨架 `search.go`（`searchPlan` + `runSearchPlans` 多段检索 + `rejectUnexpectedPage` 兜底）；标准 NexusPHP 结果页走 `nexusphp.go`（**用 x/net/html 做 DOM 解析**，因行内嵌套 table，正则切 `<tr>` 会错位）。⚠️ **带筛选检索 0 候选时必须去掉筛选重试**：hdhome/chdbits 对不认识的参数会返回 0 条而非忽略，配错就会把「查不到」当「不重复」放行。⚠️ `BuildSearchFilters` **跳过 `@index:` 占位值**（hdhome 的 medium/编码映射是 `@index:N`，要到抓上传页阶段才解析，dupe 时拿不到）。
- dupe 站点差异速查：**人人** `search_area=2` 豆瓣/IMDb 都命中、行带 `data-size-bytes` 精确字节；**幸运** 0/1/3/4 无豆瓣、行 `data-label`、体积只有文本但 GB=GiB；**家园** 0/1/3/4 无豆瓣、筛选**平铺名**（`medium[]` 会返回 0）、medium/编码是 @index 故只筛 type；**猫站** 0/2/3/4/5 有豆瓣、筛选平铺名且 medium→`source{值}`（映射到上传的 source_sel）；**我堡** 0/3/4/5 有豆瓣、筛选**数组名** `medium[]`/`standard[]`；**彩虹岛** 0/1/3/4 无豆瓣、筛选平铺名。四站（家园/猫站/我堡/彩虹岛）体积都只有展示文本、按 1024³ 换算。⚠️ Go RE2 **不支持前瞻 `(?=`）**，切行要用 `FindAllStringIndex` 或 DOM。前端展示：`PreCheckError.Meta` 携带 `dupe_blocked`/`dupe_search_url`/`dupe_torrent_url`（键名常量在 `publisher/precheck.go`），`publish_entry.go:mergePreCheckMeta` 并入响应体 → 转种面板卡片显示「dupe 发布失败」+「查重地址」「重复种子」+「仍要发布」（`skip_dupe_check` 单次放行，只跳 dupe、不影响其它硬性限制）。

## 站点差异
- 各站差异查 skill `ptn-publish-site-setup/references/known_sites.md`。
- hdhome 六维度独立 select、无地区字段；我堡 Remux 硬禁、无粤语标签；青蛙标题特化 + HDR 统一写「HDR」（正则别用 `\b`）；北洋园无六维度、无 `name`、简介只有 `descr`、动漫整类拒发。
- **m-team**：`team` 字段一律不提交（站点校验组成员，非成员整条被拒）；`source` 仅电影大类；站点配置「Passkey 栏」放**存取令牌（36 位 UUID）**，32 位站点 Passkey / 网页 Cookie 会被 `mteamapi.ExtractToken` 本地拒绝（否则只回 code=1「key無效」）；`labelsNew` 本地按 mediainfo 规则补 `4k`/`8k`/`hdr` 并丢弃与「菁彩HDR」冲突的 HDR10/HDR10+；`api.m-team.cc` 与 `api2.m-team.cc` 同一后端（504 直接重试，别换域名）。

## 媒体参数
- 标准值真源 `acquire/extract/review_extract.go:InferStandardizedValues`；产地只认简介「◎产　　地」行、年份只认「◎年　　代」。
- 语种只在 MediaInfo **Audio 段**读（BDInfo 全文扫会被字幕段 Chinese 误命中）；分辨率只在视频段内找。
- 蓝光 ISO 直读只出 General，须 `mount -o loop,ro` 读 mpls；`medium` 把所有 Remux 收敛为 `medium.remux`，判断用 `strings.Contains(medium,"remux")`。
- 碟结构媒介纠偏：`processing/media/disc_structure.go`。收敛口径 `ConvergeMediumByConfirmedDiscStructure`（DVD 系 / SD / 分辨率缺失一律不收敛，其余 → `medium.bluray` / `medium.uhd_bluray`）；`OverrideMediumByDiscStructure` = 文件列表判定 + 前者。两条**物理信号源**：①抓取期 `SeedDraft.TorrentFileNames`（`.iso`，或 `index.bdmv`+`.mpls/.clpi`；**未落库**，只在内存）②刷新期「本机/盒子对本体成功跑出 BDInfo」（本地取 `ResolveAndExtractForBDInfo.UsedBDInfo`，代理完成即成立）→ 入口 `RewriteSeedTitleComponentsByMediaInfo(..., discStructure bool)`。⚠️ `NormalizeMediumByMediaType` 的 `isBDInfo` 分支**刻意不覆盖 `medium.remux`**（防源站贴源盘 BDInfo 的误判），所以必须先有物理确认才越过它。⚠️ 反向不成立：详情页贴的 BDInfo 只说明源盘，不能据此判本体是原盘。⚠️ **抓取期的碟结构纠偏不得挂在 `if mediainfoValid` 之后**：`fetch_finalize.go` 里媒体文本缺失是常态（缺它才去跑 BDInfo），挂了门禁就会把 `TorrentFileNames` 的 `.iso` 证据整条跳过 → 媒介停在 `medium.remux`、`tag.Remux` 留库。走 `SeedDraft.CorrectMediumByDiscStructureOnly()`（只按文件列表纠偏）。⚠️ 前端 `CrossSeedPanel.vue:applySeedUpdates` 对 tags 是**并集**（只增不减）→ 后端后来剔除的标签不会从前端消失，媒介类标签需按回传 `medium` 单独剔除（`isRemuxTagValue`，与 `ShouldDropRemuxTag` 同口径）。⚠️ 面板「媒介」格 = 标题文本组件，≠ 标准键 `medium`（标题写 Remux 就显示 `Blu-ray Remux`），别当 bug。⚠️ **`is_reviewed=1` 会阻断刷新侧纠偏**：`migrationflow/bdinfo_callbacks.go` 在 `BoolFromAny(row["is_reviewed"])` 为真时直接 return，跳过回写 → 已复核的行不会自纠，只能手改或重抓。
- 判「载荷是不是整碟」的免文件列表法：拿 `torrents.size` 比对详情页 BDInfo 的 `Disc Size`，**逐字节相等即载荷为整碟**（Remux mkv 会小掉 M2TS 封装开销，约 2%）。
- 标签跟随媒介（`tagging/recompute.go`）：`ReconcileMediumTagsWithStandardMedium` 在媒介非 Remux（`ShouldDropRemuxTag`，**空值不删**）时剔除 `tag.Remux`；`ShouldAddDiscDIYRawTag` 在**媒介属原盘档**且 BDInfo 碟指纹命中 DIY 时补原始标签 `"DIY"`（映射前注入，站点无映射自然丢弃）。落点：抓取 `SeedDraft.CompleteAndMapTags`、刷新 `RecomputeStandardTags`（入参 `medium`）。⚠️ DIY 指纹在 `media/disc_diy.go`：卷标==发布名 / 卷标工具名(`iso-<数字>`) / 单playlist+无菜单载荷+AACS；「媒介属原盘档」门禁是防「源站贴源盘 BDInfo」误标的关键，故标签侧无需另穿参数。
- ⚠️ **标题 Remux 标记必须跟随标准媒介**（`media/title_medium.go:StripRemuxMediumToken`）：标题组件「媒介」由**标题文本**推导（`title/simple_components.go:extractMediumPythonish`，遇到 `REMUX` 就拼上 “Remux”），所以媒介被纠偏成原盘档后若标题仍写 “…Blu-ray Remux…”，面板「媒介」会一直显示 “Blu-ray Remux”，用户只能手改。门禁 = `ShouldDropRemuxTag(medium)`（与标签同口径：媒介非空且不含 remux）。落点两处：抓取 `SeedDraft.dropRemuxTitleTokenIfMediumNotRemux`（`CorrectMediumByDiscStructureOnly` / `CorrectMediumAndTitleByMediaType` 末尾，组件在其后构建，故自动跟着变）；刷新 `RewriteSeedTitleComponentsByMediaInfo`（**必须先算媒介 → 摘标题 Remux → 再据最终标题造组件并连带回写 `title`**，否则组件用的是旧标题）。⚠️ 面板保存时标题是**由组件重建**的（`manual_update.go:BuildPreviewTitleFromTitleComponents`），所以手改「媒介」格子会连带改标题——这正是以前需要人工补的缺口。
- 「一种多站」列表（TorrentsView，`GET /api/data`，聚合键 name+size）类型/媒介/地区取自 `seed_parameters`（择优 is_reviewed > 非空多 > updated_at 晚），中文靠 `reverse_mappings`；`columns_version=2`。

## 桌面端
- 同一 exe 两形态：单文件（`scripts/package-desktop.sh single`，go:embed，约 40MB）与安装包（需 wails + NSIS）。见 `desktop/docs/build.md`。
- ⚠️ 桌面端**不加载 `server/.env`**，DB 唯一来源 `%APPDATA%/pt-nexus/data/database.json`（否则命中仓库 .env 连内网 MySQL → 5275 永不就绪）。
- ⚠️ 站点资源两份：`server/sites_data.json`+`configs/` 与 `desktop/internal/desktopapp/bundle/`（go:embed，已提交）——改映射必须同步 bundle，否则 exe 内仍旧映射。

## 其他
- PTGen 节点真源 `repair/ptgen_nodes.go:builtinPTGenNodeDefinitions`（数组序=优先级）；`*.dpdns.org` 全域 NXDOMAIN。
- 抽帧双份实现须同步（`repair/screenshot*.go` 与 `proxy/media_core.go`），咽喉是 `isHDRMetadataText`。
- 简介清洗真源 `descclean.TrimDescription`（从【影片参数】/「更多视频截图」截断到结尾），抓取归一与发种组装共用。
- 抓取标题清洗双份实现须同步（`extract/review_extract.go` ↔ `extract/sites/ssd.go`），三件事：① 尾部促销徽标 `[免费]`/`[优惠]`/`[Free]`…（`reTitleBadgeText`/`reTitleBadgeWord`，右括号 `\]?` 可选，由 `cleanTopTitleText` / `trimSSDTitleBadges` 循环剥离）② 前缀中文片名（`reLeadingChineseTitle` + `stripLeadingChineseTitle`，规则 `^([\p{Han}·・]+)[\s\x{3000}]*([A-Za-z].*)$` —— 中文段后**必须紧跟 ASCII 字母**才剥，故 `庆余年 2019 1080p` 与纯中文标题不受影响）③ Go RE2 无前瞻 `(?=)`，判"后接字母"只能用捕获组。
- Docker 多架构：`server/bdinfo/linux-{amd64,arm64}` 按 `ARG TARGETARCH` 选（**不能带默认值**）；SQLite 驱动 `glebarez/sqlite`（纯 Go）。BDInfo 重编须用 commit `342b6069ba3344f7ae41448e30d6a7368105e338`，且必须在 PTNexus 仓库外构建。
