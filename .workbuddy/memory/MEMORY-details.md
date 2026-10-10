# PTNexus 细节（MEMORY.md 的展开）

> 本文件不被自动注入，需要时手动 Read。索引见 `MEMORY.md`。

## 发种链路细则

- NexusPHP 提交顺序：PublishPublic → setField → ResolveBasicPublishMappings → ExtraFormFields → AdjustFormFields。
- `PreCheckError.Detail` 承载判定过程（`NewPreCheckErrorWithDetail`/`AsPreCheckErrorWithDetail`），`buildPreCheckFailure` 附在 message 后 → 拦截原因在「发布进度→日志」可见，不必翻服务端日志。
- 副标题整季合集补「第x季 全y集」（`seed_draft.go:CompleteAndMapTags` + `tagging/subtitle_season.go`），历史不回填。
- 定时发种：入队即 `total_published++`；非成功结局经 ContinueHook → `ReclassifyPublishedAsSkipped` 改判 published-1/skipped+1 并继续。
- `trigger=batch_live` 曾不建日志且 payload 缺 `queue_task_id`（已修，历史行需回填 SQL）。

### dupe 校验全链

- `sites` 表 `dupe_check_enabled`(默认 0) + `dupe_size_tolerance_bytes`（默认 `repository.DefaultDupeSizeToleranceBytes`）+ `dupe_rules`（JSON：`{"medium.remux":["size","team"], …}`，sites 表为唯一规则来源）。
- 规则按待发布种子的**标准媒介**命中（`SeedMediumFromPayload` 读 `uploadData.standardized_params.medium`，与 `ResolveBasicPublishMappings` 同源）；未配规则的媒介走「兜底规则」（保留键 `dupe.DupeFallbackMedium = "*"`，站点设置里可开关）。
- ⚠️ **规则键只有 6 类媒介**（`dupe/medium_groups.go`，`MatchRule` 三段匹配：精确键 → 组键 → 兜底）：
  | 展示名 | 规则键 | 归入该组的标准媒介键 |
  |---|---|---|
  | UHD Blu-ray | `medium.uhd_bluray` | `uhd_bluray`、`uhd_diy` |
  | Blu-ray | `medium.bluray` | `bluray`、`bluray_diy` |
  | HDTV | `medium.hdtv` | `hdtv`、`uhdtv`、`tvrip` |
  | Encode | `medium.encode` | `encode`、`encode_2160p/1080p/720p`、`bdrip` |
  | Remux | `medium.remux` | `remux`、`uhd_remux`、`bluray_remux`、`uhd_bluray_remux`、`remux_tv`、`uhd_remux_tv` |
  | WEB | `medium.webdl` | `webdl`、`webrip` |
  组外媒介（`medium.other`/`dvd`/`dvdr`/`cd`/电子书类 `iso`·`pdf`·`azw`…）不命中媒介规则，只能靠兜底。日志里规则键与种子媒介不一致时会写「媒介=medium.remux（种子 medium.uhd_remux 归入该组）」。
- ⚠️ **兜底关闭时，未配规则的媒介直接跳过校验**（不留拦截），发布日志留一行「该媒介未配置规则且未开启兜底」+ 站点列表用红色标签「已开（未配置规则）」提醒 —— 开关开着 ≠ 已生效。`MatchRule` 返回命中的规则键，走兜底时日志写「兜底规则（媒介 X 未单独配置）」以便区分。
- ⚠️ 选项结构体必须带 `json` tag（`value`/`needs_site_filter`/`site_value`/`fallback_medium`…），否则按 Go 字段名输出、前端读到 undefined。
- 维度常量（`dupe/rules.go`，前后端共用，改名必须同步 `SitesSettings.vue`）：`size` 文件大小 / `team` 制作组 由**客户端比对**（`Query.MatchSize`/`MatchTeam`）；`medium`/`resolution`/`video_codec`/`audio_codec` 由**站点检索筛选**承担（`BuildSearchFilters(site, formFields, dims)` 只拼勾选的维度，「类型」始终参与；勾了站点没声明的维度 → 发布日志告警 + 界面置灰，见 `FilterDimensionsForSite`/`unsupportedFilterDimensions`）。
- ⚠️ **结果行只有标题+体积**，所以媒介/分辨率/编码只能靠筛选，不能靠解析。
- ⚠️ **媒介规则（非兜底）恒含「媒介」维度**：规则本身按媒介区分，`MatchRule` 用 `ensureMediumDimension` 统一补上（存量/手改数据也照样生效），界面里该勾选框是「已勾选 + disabled」（`DUPE_LOCKED_DIMENSION`）；**兜底规则不补**，媒介是否参与由用户决定。
- ⚠️ **只靠筛选的规则不得据「去掉筛选重试」的结果判重复**（`runSearchPlans`：无客户端维度时该结果只留痕不判定，因为无法确认维度真的一致）；勾了 size/team 时该重试仍是防「筛选参数写错→0 条→漏检」的兜底。
- 选项接口 `GET /api/sites/dupe_options?site=<code>`（`dupe/options.go:BuildSiteDupeOptions`）：`mediums` 返回上面那 **6 类媒介组**（固定文案 + 固定顺序；站点完全没映射的组会被略去，`site_value` 取组内首个命中成员的站点取值，仅作展示参考），外加各维度可用性 + `fallback_medium` 保留键（由后端下发，前端不硬编码）；前端是扁平 6 项下拉（不再按 site_value 分组），可「添加全部媒介」一键把 6 类按「文件大小+制作组」铺满。⚠️ 这些选项结构体**必须带 json tag**（`value`/`label`/`needs_site_filter`/`supported`/`medium`/`site_value`/`fallback_medium`/`enabled`/`mediums`/`dimensions`），不加 tag 会按 Go 字段名输出、前端全读到 `undefined`。
- **配置入口是站点管理表格「操作」列的「Dupe 设置」独立弹窗**（`SitesSettings.vue`：`handleOpenDupeDialog`/`handleSaveDupe`，操作列宽度 260，按钮用 `@click.stop` 避免触发行点击开编辑弹窗）。保存走 **`POST /api/sites/update_dupe`** → `SiteRepository.UpdateSiteDupeSettings`（只更新 `dupe_check_enabled`/`dupe_size_tolerance_bytes`/`dupe_rules` 三列，值未变化时靠回查主键判存在）。⚠️ **不能复用 `/api/sites/update`**：那是全字段覆盖，dupe 弹窗只带自己那几项，走它会把昵称/cookie/passkey 清空。⚠️ 站点编辑弹窗保存时 `dupe_rules` 必须**原样透传**（`{...siteForm.value.dupe_rules}`），不能拿 dupe 编辑器的状态重建，否则「只改站点信息」会把规则覆盖成空。
- ⚠️ 显示/录入单位是 **MB（1024² 字节，二分口径）**，存库仍是字节；**0 是合法值**（体积须完全一致），前后端都要把「0」与「字段缺失/NULL」区分开（`resolveSizeTolerance`），提示文案统一走 `FormatSizeMB`。
- 逻辑在 `publish/dupe/`，`CheckBySite` 分发站点；**站点是否参与由 `configs/<site>.yaml` 的 `dupe_check` 声明，钩子由 `engine.Publish` 入口的 `AttachSiteDupeCheck` 统一挂载 —— 新增站点只写 YAML**。
- 骨架 `search.go`（`searchPlan` + `runSearchPlans` + `ensureSearchResultPage`）；NexusPHP 结果页走 `nexusphp.go`（**用 x/net/html DOM 解析**，行内嵌套 table，正则切 `<tr>` 会错位）。
- ⚠️ **带筛选 0 候选必须去掉筛选重试**（hdhome/chdbits 对不认识的参数返回 0 条而非忽略 → 会把「查不到」当「不重复」放行）。
- ⚠️ `BuildSearchFilters` **跳过 `@index:` 占位值**（hdhome 的 medium/编码要到抓上传页才解析）。
- ⚠️ Go RE2 **不支持前瞻 `(?=)`**，切行用 `FindAllStringIndex` 或 DOM。
- 前端展示：`PreCheckError.Meta` 携 `dupe_blocked`/`dupe_search_url`/`dupe_torrent_url`（常量在 `publisher/precheck.go`），`publish_entry.go:mergePreCheckMeta` 并入响应 → 面板卡片显示「dupe 发布失败」+「仍要发布」（`skip_dupe_check` 单次放行，只跳 dupe）。

### dupe 站点差异速查

- **人人** `search_area=2`、行带 `data-size-bytes`。
- **幸运** 0/1/3/4 无豆瓣、行 `data-label`、GB=GiB。
- **家园** 0/1/3/4 无豆瓣、筛选平铺名、**唯一用 `@index:N`**（需配 `dupe_check.upload_path` 抓上传页换算；索引≠值：medium_sel[1]=UHD 值 10、[2]=Blu-ray 值 1，audiocodec_sel 更乱）。
- **猫站** 0/2/3/4/5 有豆瓣、平铺名、medium→`source{值}`，**表单只有 type/source_sel/team_sel** 故只两维可筛。
- **我堡** 0/3/4/5 有豆瓣、筛选数组名 `medium[]`/`standard[]`。
- **彩虹岛** 0/1/3/4 无豆瓣、平铺名。
- 后四站体积只有展示文本、按 1024³ 换算。除家园外**维度映射均为固定值**，五维可直接进检索。

### 添加下载器失败的重试入口（2026-10-10）

- 失败信息存 `publish_logs.auto_add_result`（JSON：success/message/downloader_id/downloader_name），**发布状态仍是 success**（发布与添加下载器分开判定）。发种日志页「下载器」列读的就是它。
- 重试链：`POST /api/publish_logs/re_add_downloader {id, downloader_id?, save_path?}` → `migrationflow/publish_log_readd.go:ReAddPublishLogToDownloader` → 复用 `MigrateService.AddToDownloader`（用日志的 `result_url` 当 url + publishURL，走详情页反查站点下载种子）；结果 marshal 回写同一行的 `auto_add_result`（**不回写就会刷新后仍显示旧失败**）。
- 仓储 `PublishLogRepository.GetByID` / `UpdateDownloaderResultByID(id, downloaderID, autoAddResult)`；后者 downloaderID 传空则保留原值。
- 前端 `PublishLogsView.vue`：`canReAddToDownloader` = 状态∈{success,exists,edited} 且 `result_url` 非空 且 `auto_add_result.success !== true`；弹窗可换下载器（默认日志记录的 > 顶部全局 > 第一个启用项）。
- 转种面板入口（`CrossSeedStepPublishResults.vue`）：顶部工具条「一键添加到下载器」+ 失败卡片内联「重新添加」→ `publishFlow.ts:reAddSiteToDownloader/reAddFailedSitesToDownloader`（串行批量），**同一个接口**改用 `task_id + target_site` 定位（找不到日志行也照常添加，只是不回写；响应 `log_record_found`）。可重试判定 = `success===true && auto_add_result.success===false && !dupe_blocked` —— dupe 拦截/发布前限制/发布本身失败都不算加种失败。⚠️ 重试成功后必须 `rebuildFinalResultsList()`：卡片走的是 `finalResultsList` 重建的 Map。

## 媒体参数全链

- 标准值真源 `acquire/extract/review_extract.go:InferStandardizedValues`；产地只认简介「◎产　　地」行、年份只认「◎年　　代」。
- 语种只在 MediaInfo **Audio 段**读（BDInfo 全文扫会被字幕段 Chinese 误命中）；分辨率只在视频段内找。
- 蓝光 ISO 直读只出 General，须 `mount -o loop,ro` 读 mpls；`medium` 把所有 Remux 收敛为 `medium.remux`，判断用 `strings.Contains(medium,"remux")`。
- 判「载荷是不是整碟」的免文件列表法：`torrents.size` 与详情页 BDInfo 的 `Disc Size` **逐字节相等即整碟**（Remux mkv 会小掉 M2TS 封装约 2%）。

### 碟结构媒介纠偏 `processing/media/disc_structure.go`

- 收敛口径 `ConvergeMediumByConfirmedDiscStructure`（DVD 系/SD/分辨率缺失不收敛，其余 → `bluray`/`uhd_bluray`）；`OverrideMediumByDiscStructure` = 文件列表判定 + 前者。
- 两条**物理信号源**：①抓取期 `SeedDraft.TorrentFileNames`（`.iso` 或 `index.bdmv`+`.mpls/.clpi`；**未落库**）②刷新期本机/盒子对本体成功跑出 BDInfo。
- ⚠️ `NormalizeMediumByMediaType` 的 `isBDInfo` 分支**刻意不覆盖 `medium.remux`**（防源站贴源盘 BDInfo 误判），必须先有物理确认。
- ⚠️ 反向不成立：详情页 BDInfo 只说明源盘，不能据此判本体原盘。
- ⚠️ **抓取期纠偏不得挂在 `if mediainfoValid` 之后**（`fetch_finalize.go` 里媒体文本缺失是常态，会把 `.iso` 证据整条跳过 → 媒介停在 `remux`）→ 用 `SeedDraft.CorrectMediumByDiscStructureOnly()`。
- ⚠️ 前端 `applySeedUpdates` 对 tags 是**并集**（只增不减）→ 后端后来剔除的标签不会消失，媒介类标签需按回传 `medium` 单独剔除（`isRemuxTagValue`）。
- ⚠️ 面板「媒介」格 = 标题文本组件 ≠ 标准键 `medium`。
- ⚠️ **`is_reviewed=1` 阻断刷新侧纠偏**（`migrationflow/bdinfo_callbacks.go` 直接 return）→ 已复核行只能手改或重抓。

### 标签跟随媒介 `tagging/recompute.go`

- `ReconcileMediumTagsWithStandardMedium` 在媒介非 Remux（`ShouldDropRemuxTag`，**空值不删**）时剔除 `tag.Remux`。
- `ShouldAddDiscDIYRawTag` 在媒介属原盘档且 BDInfo 碟指纹命中 DIY 时补原始标签 `"DIY"`（映射前注入）。DIY 指纹在 `media/disc_diy.go`：卷标==发布名 / 卷标工具名(`iso-<数字>`) / 单 playlist+无菜单载荷+AACS。
- 落点：抓取 `CompleteAndMapTags`、刷新 `RecomputeStandardTags(medium)`。

### 标签映射两条入口

- ⚠️ 删 yaml 映射堵不住直通分支：`MapTagsToStandard` 对已是 `tag.*` 的输入走 `directStandard` **绕过映射表**，来源 = `RecomputeStandardTags` 的 `existingTags`（历史行）+ `persist/manual_update.go:80`（面板保存）。
- 要彻底禁用某标准标签：①`global_standard_keys.tag.<键>` 置 `null`（同表已有 `"default": null` 约定，遇 `!!null` 直接 skip）②`global_standard_keys.excluded_tags` 黑名单（`loadGlobalExcludedTags`，两条分支都过滤、大小写不敏感、进程内不热更新）。当前黑名单 = `tag.官组`。
- ⚠️ `matchPartial` 是**双向** `strings.Contains`，删精确键前先核有没有别的键会被撞上。发布侧映射（chdbits/ptsbao/yemapt 的官组）是另一套，不受黑名单影响。

### 标题 Remux 标记

- ⚠️ 必须跟随标准媒介（`media/title_medium.go:StripRemuxMediumToken`，门禁 = `ShouldDropRemuxTag(medium)`）：组件「媒介」由标题文本推导（`title/simple_components.go:extractMediumPythonish`），媒介纠正后标题仍写 Remux 就只能手改。
- 落点：抓取 `SeedDraft.dropRemuxTitleTokenIfMediumNotRemux`；刷新 `RewriteSeedTitleComponentsByMediaInfo`（**先算媒介 → 摘标题 Remux → 再造组件并连带回写 title**）。面板保存时标题由组件重建（`manual_update.go:BuildPreviewTitleFromTitleComponents`），手改「媒介」格会连带改标题。

### 「一种多站」列表

- TorrentsView，`GET /api/data`，聚合键 name+size；类型/媒介/地区取自 `seed_parameters`（择优 is_reviewed > 非空多 > updated_at 晚），中文靠 `reverse_mappings`；`columns_version=2`。

## 修复/校验入口（转种面板「重新获取」）

- 统一入口 `POST /api/media/validate`（`bootstrap/app.go:352`）→ `handler/migrate/downloader_media.go:MediaValidate` → `migrationflow/media.go:MediaValidate`（补 savePath）→ `repair.MediaValidateEntry` → `repair.ValidateMediaPayload`，按 `type` 分发 `screenshot_preview`/`screenshot_finalize`/`screenshot`/`poster`/`intro`/`mediainfo`。
- **poster**：`repair/movie_info.go:FetchMovieInfo` 五级兜底 —— ①已有外链互补（queryByIMDb/queryByDouban/searchByName + `backfillTMDbByIMDbIfNeeded`）②PTGen 多节点（`repair/ptgen_nodes.go`，数组序=优先级）③豆瓣页 `og:image`/`.nbg` ④TMDb API ⑤IMDb 页 → 再经 `NormalizePosterBBCodeWithConfig` 取首个 URL 并转存图床（`cross_seed.image_hoster`：`pixhost`(默认)/`agsv`，输出单个 `[img]`），返回 `posters` + `extracted_{imdb,douban,tmdb}_link`。前端 `CrossSeedPanel.vue:refreshPosters` 只在原外链为空时回填。
- 图床转存 `repair/pixhost.go`：下载候选 = 直连 + 两个 worker 代理前缀（`posterTransferProxyPrefixes`），每候选重试 2 次、25MB 上限、必须 `image/*`；上传取 `show_url` 后 `ResolvePixhostImageURLWithConfig` 解析直链并 `IsImageURLReachable`（HEAD → Range GET）校验。
- 前端预览图失效会自动走 `handleImageError` 重发同一 payload（`type=poster`），但**受限标签命中直接跳过**、`pixhost.to` 域名直接跳过检测。
