# PTNexus 长期约定（精简索引）

> 本文件有注入长度上限，保持精简。**深度细节读 `.workbuddy/memory/MEMORY-details.md`**（dupe 查重全链、站点标签全链、媒体参数/媒介纠偏全链、发种链路细则、52pt 差异、修复与校验入口）。
> 技能：`ptn-deploy`/`ptn-proxy-release`/`github-release`/`ptn-desc-clean`/`ptn-downloader-scope`/`ptn-publish-pacing`/`ptn-publish-progress`/`ptn-publish-field-diagnose`/`ptn-publish-site-setup`(含 `references/known_sites.md`)/`ptn-manual-screenshots`/`ffmpeg-hdr-frame-capture`。站点映射真值 = `server/configs/<site>.yaml`。

## 环境/流程
- 主工作区直接改、不建 worktree；改完给 `git status`/`git diff`；提交/回滚/改历史须当轮明确要求。
- 工具链：Bash 先 export PortableGit PATH；Go `/d/go/go1.25.11/bin/go`、pnpm `~/AppData/Roaming/npm/pnpm`、pymysql venv `C:/Users/wind/.workbuddy/binaries/python/envs/default/Scripts/python.exe`。
- 校验：前端 `pnpm -C webui run type-check`（`lint` 存量报错、不得新增）；后端 `go build ./...` + `go vet`。
- ⚠️ `core.autocrlf=true`：工作区 CRLF 正常，**gofmt 前先 `tr -d '\r'`**；**绝不对目录跑 `gofmt -w <dir>`**（会把整目录行尾改成 LF、凭空多出纯行尾差异，已踩两次），只逐个文件执行。
- 临时测试写 `zz_tmp_*_test.go`，验完备份 `.workbuddy/tmp/test-backup/` 再删（`server/` 下另有 4 个 git 跟踪的正式测试，勿删）；`-mod=mod` 污染 go.mod，用完还原。
- 通知走 `wsl-win-notify`；本机 toast（powershell.exe）会挂起，须加 `--channel msg`。

## 发布/数据
- `5.135.178.15`（OVH，`/home/code/PTNexus`）是**构建推送机**非生产：只 buildx push Docker Hub `fenglaile/ptnexus`（仅 latest）；动手前查 `ps -ef | grep '[b]uildx build'`。
- ⚠️ 容器 `/etc/localtime` 仍 UTC → DSN 必须显式 `loc=Asia/Shanghai`。
- 内网 DB `192.168.5.3:3306/pt_nexus`（root/111111，pymysql）。Release 走 `github-release`（publish.sh 末尾 trap 被拦 → 退出码 1 但已上传成功）。
- ⚠️ 无外键。删除某颗种子的库内数据 = 按 hash 删 **`torrents` / `seed_parameters` / `torrent_upload_stats`**（seed_parameters 存解析结果 audio_codec/title_components，重测必须一起删）。应用内「一种多站」删除只删 torrents + torrent_upload_stats（不碰 seed_parameters，也不删下载器文件）。
- 进度/日志列表「大小」两表都无 size 列 → 旁路关联 hash 取 `torrents.size`（日志页覆盖 ~63%）；`migrationflow/publish_size.go`，DTO `gorm:"-"`，`SizeByHashes` 刻意不过滤 is_hidden。

## 配置/下载器/代理
- ⚠️ **DB 读出的开关值类型是 int8**（MySQL TINYINT(1) 经 GORM `Scan(&map)`；SQLite int64、PG int32、原生 SQL `[]byte`）。**任何 `switch v.(type)` 必须覆盖全部整型宽度 + bool + string + []byte** —— dupe 的 `toBool` 只写 int/int64/float64/string → 开关开着被读成 false、校验静默失效。参照 `repository/value_helpers.go:toInt64`。
- ⚠️ **MySQL DSN 未开 `clientFoundRows`** → 值未变时 `RowsAffected=0`。**凡「用 `RowsAffected>0` 判存在」的 UPDATE 都错**（没改配置直接保存会误报「站点不存在」）。已修 `site_repository.go` 的 `UpdateSiteDetails`/`UpdateSiteCookie`/`UpdateSiteCookieBySite`（改为按主键/nickname 回查 `siteExists`）；`DeleteSite` 用 RowsAffected 正确；`sites_handler.go:UpdateSite` 已拆开 `err` 与 `!updated`，DB 错误回 500。
- 配置存 DB `app_settings`（DB 优先）；Docker 下改 `server/data/config.json` 无效，改文件即生效用环境变量。
- 菜单显隐 `PTNEXUS_SHOW_*` → `menu_visibility.go` → 前端 `App.vue` `v-if`（只隐入口不拦路由）。
- 顶部「全局下载器」是全站下载器筛选唯一来源（页面内入口已移除，`onMounted` 无条件覆盖）。
- 站点角色 `sites_data.json:migration`（0 不参与/1 仅源/2 仅目标/3 双向）；唯一写入是启动 `SyncSitesFromJSON`，门禁在 `source_site.go`/`publish_with_context.go`（位不对 → 403）。⚠️ 缺该字段会被写成 0；JSON 不能含注释。
- 下载器配置字段名是 **`host`**（写 `url` → 首页「连接失败」）。
- **代理路径探测**真源 `downloaderclient/proxy_paths.go:BuildProxyPathCandidates`，调用方须传两个根 `[原始路径, 映射后路径]`；四条链路 refresh_flow / screenshot(_preview) / bdinfo_task_ops / episode_count。⚠️ 候选循环**只在 400 continue、其余 break** → 代理 stat 失败必须经 `proxy_error.go:NormalizeProxyCandidateStatus` + `newProxyHTTPError/newProxyResponseFailure` 归一为 400；不可达仍 500 → 立即 break。
- 本地回退门禁 `shouldSkipLocalMediaFallback`/`shouldSkipLocalScreenshotFallback`（use_proxy=true 且映射后本地根不存在 → 报「本地路径不可访问」）。
- 站点导入/导出真源 `repository/sites_backup.go`：`GET /api/sites/export`（全量、含 cookie/passkey）+ `POST /api/sites/import`（**仅补空**＝库内为默认值/空才填、**只更新不新增**、匹配 site→nickname→base_url、**cookie/passkey 为一组：任一有值则整组不动**）；入口=站点管理工具栏。
- **站点标签**：真源列 `sites.tags`（JSON 数组）；批量 `POST /api/sites/batch_tags`（mode=add/remove/replace），入口=站点管理列表勾选后「批量打标签」；前端快捷选择唯一实现 `components/SiteTagChips.vue`（点标签=切换）；配色 `utils/siteTagColor.ts` + 归一 `utils/siteTag.ts` + 样式 `assets/styles/site-tags.scss`。⚠️ 读 `tags` 列必须 `sql.NullString` 取 `.String`（传 `*string` 会被 `%v` 打成 `0xc000...`）；色相哈希须雪崩混淆（FNV-1a+xorshift，线性 `hash*31+code` 会让中文短标签聚集）。全链（筛选栏、`__ptn_no_tag__` 哨兵、弹窗上色）见 details。

## 发种链路（要点）
- 唯一入口 `workflow/publish_entry.go` → `publish_target.go:PublishTorrentToTarget`；日志 `region=<字段>:<值>` 是排查第一现场。
- ⚠️ 三条铁律：① 站点硬性拒绝必须用 `*publisher.PreCheckError`（`publish_entry.go` 识别后返回 `pre_check+limit_reached` 让队列改判跳过；用 `errors.New` 会变 500 污染统计）② 语种以 MediaInfo 音轨为准，**不与源站标签取并集** ③ 动画标签优先于类型字段。
- ⚠️ `mappings.<类目>` 直接提交站点 select，须对齐 upload.php 实测 value；`default` 兜底会掩盖错值、`@index:N` 越界静默退化；站点不存在的字段被静默丢弃 → 在 `AdjustFormFields` 回填/清理。
- ⚠️ 标题默认沿用源标题，站点冲突在适配器 `normalizeXxxTitle` 纠偏；别改全局解析或 `global_mappings.yaml`。
- `IsExistingTorrent` → 日志 `exists` 不重试；非 200 / success=false 必判 failed；torrent_id ≠ infohash。
- ⚠️ 队列行状态与日志状态是两套判定（日志靠 `queue_task_id` 反查，另有 `task_id`+`target_site` 兜底）。
- **dupe 查重**：`sites` 表 `dupe_check_enabled` + `dupe_size_tolerance_bytes` + `dupe_rules`(JSON，唯一规则来源，只 6 类媒介组键 `medium.uhd_bluray`/`bluray`/`hdtv`/`encode`/`remux`/`webdl`，兜底键 `"*"`)。未配走兜底，兜底关闭 → **直接跳过校验**（列表红标签「已开（未配置规则）」）。**站点是否参与由 `configs/<site>.yaml` 的 `dupe_check` 声明，新增站点只写 YAML。** 配置入口 = 站点管理「操作」列「Dupe 设置」独立弹窗，保存走 `POST /api/sites/update_dupe`（只更新 dupe 三列，**不可复用全量 `/sites/update`**）。单位 MB(1024²) 存库为字节，**0 合法**。全链见 details。

## 站点差异
- 查 `ptn-publish-site-setup/references/known_sites.md`。
- hdhome 六维度独立 select、无地区字段；我堡 Remux 硬禁、无粤语标签；青蛙标题特化 + HDR 统一写「HDR」（正则别用 `\b`）；北洋园无六维度、无 `name`、简介只有 `descr`、动漫整类拒发。
- **m-team**：`team` 一律不提交（站点校验组成员）；`source` 仅电影大类；「Passkey 栏」放**存取令牌（36 位 UUID）**，32 位 Passkey/Cookie 会被 `mteamapi.ExtractToken` 本地拒绝；`labelsNew` 本地按 mediainfo 补 `4k`/`8k`/`hdr` 并丢弃与「菁彩HDR」冲突的 HDR10/HDR10+；`api.m-team.cc` 与 `api2` 同一后端（504 直接重试）。
- **52pt**：标准 NexusPHP 但**无 `source_sel`/`processing_sel`**（不区分产地）；标签是裸名 `tags[]` 且 **value 是中文词**（走 `mappings.tag`，**不能**用 `checkbox_tags`）；`url` 字段即「IMDb链接」；`medium_sel` 把「分辨率 + 原盘有无中文」编码进选项、站内两个 52PT 小组同属 `team.pt52` ⇒ 必须靠 `sites/pt52.go` 适配器二次判定（详见 details 文件）。

## 媒体参数（要点）
- 标准值真源 `acquire/extract/review_extract.go:InferStandardizedValues`；产地只认简介「◎产　　地」、年份只认「◎年　　代」。
- 音频编码唯一入口 `acquire/extract/audio_codec_infer.go:InferAudioCodecKey`（MediaInfo/BDInfo 音轨 → 标题 token → 标题+媒体文本合并 token，再按 Atmos 同族升级）；标签侧 `tagging.ExtractRawTagsFromAudioCodec` 与之同源。⚠️ **音轨选取口径 = `mediainfo_codec.go:audioCodecKeyForInference`**：默认取第一条，但**首轨是「已识别的低规格配音轨」（AAC/MP3，rank ≤ `audio.aac`=12）且存在更高规格音轨时改取 `audioCodecRank` 最高者**（多语言 WEB-DL 首轨常是配音 AAC，2026-10-10 实测）；**首轨 rank=0（如 TrueHD 的 `Format : MLP FBA` 映射不到键）必须原样返回空串**，否则会把高规格音轨降级并掐掉「媒体识别不到 → 回退标题 token」的兜底。**DTS 细分只走 `mediainfo_codec.go:dtsCodecKeyFromText`** —— ⚠️ MediaInfo 把 **DTS-HD MA** 写作 `Format : DTS XLL`（DTS:X 才是 `DTS XLL X`），任何 `contains("DTS X")` 都会把它误判成 DTS:X。
- **同一套选轨规则有三处消费方（勿各写一份）**：① 标准值 `inferAudioCodecFromMediainfo`；② 组件展示 `media/parser_pythonish.go:buildMediaInfoAudioFromTracks` → `pickMainAudioTrack`，经导出件 `extract.SelectInferenceAudioTrack` + `extract.AudioCodecKeyFromDisplayName`（展示名 DDP/TrueHD/DD… → 标准键）复用；③ 站点策略 `selectAudioTrack`。⚠️ **`parser_pythonish.go` 里从 Python 直译的正则若含 `\(`/`\[` 必须写单反斜杠**：raw string 里的 `\\(s\\)` 会要求字面反斜杠 → `Channel(s)` 永远匹配不上、声道静默退化成 2.0（2026-10-11 已修；`cleanBBCode` 的 `\\[/?\\w+\\]` 同类问题**仍未修**）。
- ⚠️ **BDInfo 音轨解析**：`parseAudioTracksFromMediainfo` 的 MediaInfo 段切分只认 `^\s*Audio$`，BDInfo 段头是 `AUDIO:`（带冒号）切不到 → 已加 `mediainfo_codec.go:parseBDInfoAudioTracks` 回退（定位 `AUDIO:` 行逐行扫、首列编码到多空格为止、`Dolby Digital`→`audio.ac3` 分支）。组件展示侧 `media.ApplyMediaInfoOverrides(components, hdr, audio, preferMediaAudio)` 在 `title/components.go` **恒传 `true`** —— MediaInfo 与 BDInfo **统一以媒体主音轨重建「音频编码」= 编码 + 声道 + Atmos + 音轨数**（2026-10-11 用户选定的 B 方案；此前 MediaInfo 源走「标题优先」，标题 `DDP5.1` 与媒体首轨 `AAC 2.0` 被拼成 `DDP 5.1 11Audios`）。传 `false` 的「标题优先 + 媒体补缺」分支（`findBestMatchingAudioTrack`/`supplementAudioInfo`）现已无调用方，保留备用。
- 组件展示侧「音频编码」真源 `processing/title/simple_components.go:extractAudioFromTitle` —— **DTS 家族必须走 `extract.DTSAudioCodecKeySpan`**（返回标准键 + 命中区间），别再用 `contains("DTS-HD MA")`：`DTS-HDMA`/`DTS-HD.MA`/`DTSHDMA` 等无空格写法都匹配不到、会退化成裸 `DTS`（再被补全成 `DTS 5.1`）。⚠️ `splitTitleAndTeamPythonish` 的「最早无空格 `-`」切分会把 `DTS-HDMA5.1-52pt` 从 `DTS-` 处切断（音频丢、制作组变 `HDMA5.1-52pt`），已加 `isAudioDTSHDSplit` 护栏跳过 `DTS-HD` 里的 `-`；**`WEB-DL` 同类误切尚未修（保持 Python 对齐，改动面大，需用户确认）**。`audioCleanupTags` 需一并输出无空格变体（`audioCodecSeparatorVariants`），否则残留进「无法识别」。
- ⚠️ **`ReplaceTitleAudioCodecToken` 只换编码不换声道** → `DDP5.1`→`AAC` 会留下 `AAC 5.1`（凭空造出源里不存在的规格）；`AAC 5.1 11Audios` 即此类脏数据（声数字来自原标题、`Audios` 来自多音轨）。
- 语种只在 MediaInfo **Audio 段**读（BDInfo 全文扫会被字幕段 Chinese 误命中）；分辨率只在视频段内找。
- 蓝光 ISO 直读只出 General，须 `mount -o loop,ro` 读 mpls；`medium` 把所有 Remux 收敛为 `medium.remux`，判断用 `strings.Contains(medium,"remux")`。
- 碟结构媒介纠偏、标签跟随媒介、标题 Remux 标记、标签映射两条入口（`directStandard` 直通 + `excluded_tags` 黑名单）—— 全链见 details 文件。

## 桌面端
- 同一 exe 两形态：单文件（`scripts/package-desktop.sh single`，go:embed，约 40MB）与安装包（wails + NSIS），见 `desktop/docs/build.md`。
- ⚠️ 桌面端**不加载 `server/.env`**，DB 唯一来源 `%APPDATA%/pt-nexus/data/database.json`。
- ⚠️ 站点资源两份：`server/sites_data.json`+`configs/` 与 `desktop/internal/desktopapp/bundle/`（go:embed，已提交）——改映射必须同步 bundle。

## 其他
- PTGen 节点真源 `repair/ptgen_nodes.go:builtinPTGenNodeDefinitions`；`*.dpdns.org` 全域 NXDOMAIN。
- 抽帧双份实现须同步（`repair/screenshot*.go` 与 `proxy/media_core.go`），咽喉是 `isHDRMetadataText`。
- 简介清洗真源 `descclean.TrimDescription`（从【影片参数】/「更多视频截图」截断到结尾），抓取归一与发种组装共用。
- 抓取标题清洗双份实现须同步（`extract/review_extract.go` ↔ `extract/sites/ssd.go`）：①尾部促销徽标 `[免费]`/`[优惠]`/`[Free]`…（右括号 `\]?` 可选）②前缀中文片名（`^([\p{Han}·・]+)[\s\x{3000}]*([A-Za-z].*)$` —— 中文段后**必须紧跟 ASCII 字母**才剥）③Go RE2 无前瞻。
- Docker 多架构：`server/bdinfo/linux-{amd64,arm64}` 按 `ARG TARGETARCH` 选（**不能带默认值**）；SQLite 驱动 `glebarez/sqlite`。BDInfo 重编须用 commit `342b6069ba3344f7ae41448e30d6a7368105e338`，且必须在 PTNexus 仓库外构建。
