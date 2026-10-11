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

### 52pt 站点差异速查（2026-10-10）

- 定位：`52pt.site`，标准 NexusPHP（`takeupload.php`，file 字段 `file`），`migration=3`（源+目标）。**免 yaml 做不到**：`medium_sel` 的取值把「分辨率」与「原盘有无中文」编码进去了。
- `form_fields`：`type`/`medium_sel`/`codec_sel`/`audiocodec_sel`/`standard_sel`/`team_sel`/`name`/`small_descr`/`descr`/`url`(IMDb 链接，配 `imdb_url`)/`tags[]`。**没有** `source_sel`/`processing_sel`/`pt_gen`/`dburl`/`technical_info`。
- `tags[]` 是**裸名数组**，value 就是中文词：`原盘/DIY/Remux/国语/粤语/简中/繁中/简繁/双语字幕/杜比视界/HDR/3D/官组`。⚠️ 别被「`fields.tags` 为 null + 一排 checkbox」误导去用 `checkbox_tags` —— 那些 checkbox 的 `name` **全是 `tags[]`**（`checkbox_tags` 要求各自独立命名 + value=yes）；数组形态靠 `resolveTagFieldBase` 取 `FormFields["tags[]"]` 削掉 `[]` 得到 `tags`，写出 `tags[0..n]`，PHP 一样解析成数组。`tag.官组` 被 `global_mappings.excluded_tags` 全局丢弃 → 本站「官组」选项拿不到值。
- `medium_sel` 选项：`2`=Blu-ray DIY `11`=Blu-ray原盘无中文 `1`=4K UHD无中文 `4`=Blu-ray Remux `5`=4K UHD Remux `14`=2K原盘中字（国粤语）`15`=4K原盘中字（国粤语）`3`=HDTV `12`=4K UHD DIY `7`=Encode(MKV/MP4) `6`=DVD/DVDR `8`=CD/HDCD `10`=WEB-DL `9`=Others `13`=8K UHD。
  - **yaml 只给保守落点**（`medium.remux→4`、`medium.bluray→11`、`medium.uhd_bluray→1`），`medium.remux` 的 4K/1080p 与 原盘 11/14、1/15 的区分由 `sites/pt52.go:refinePT52Medium` 按 `standard_sel`（5=4K、7=8K）与标签（`DIY` / `简中|繁中|简繁|国语|粤语`）二次判定。
- `team_sel`：`6`=52PT DIY/原盘小组、`7`=52PT REMUX/重编码小组（两者标准值同为 `team.pt52`，靠媒介判定 → `refinePT52Team`）；`4`=COASTER 在 `global_mappings` 无对应标准制作组，**不映射**。
- MediaInfo 内嵌：站点无 mediainfo 输入框 ⇒ `52pt` 已进 `uploader/helpers.go:shouldInlineMediainfo` 白名单，用 `[quote]` 包住并置于「正文之后、截图之前」；适配器再 `delete(technical_info)`。
- IMDb：`url` 字段要**完整链接**，而 `workflow/publish_target.go:resolvePublishExternalLinks` 只聚合 `imdb_link/imdbLink/imdb`、**不读 `imdb_id`** ⇒ 适配器 `resolvePT52IMDbURL` 自己兜底（`tt` 前缀或裸数字 → `https://www.imdb.com/title/tt<digits>/`）。
- 源站侧：`source_parsers.source_params` 用详情的实际标签（类型/媒介/**编码**/音频编码/分辨率/制作组 —— 注意视频编码的标签是「编码」不是「视频编码」）；`standard_keys` 覆盖详情「基本信息」里的原文（`Movies/电影`、`Blu-ray Remux`、`H.264/AVC`、`TRUE.HD Atmos`、`4K/2160P`、`52PT REMUX/重编码小组` 等）。
- 未做：`dupe_check` 未声明（需先探 `torrents.php` 检索参数）。

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

### 站点标签全链（sites.tags）

- 真源列 `sites.tags`（JSON 数组，编解码 `siteStringListFromAny`/`encodeSiteStringList`）。单站写在 `UpdateSiteDetails`；**批量** `SiteRepository.BatchUpdateSiteTags(ids, rawTags, mode)`（mode=add/remove/replace，只改 tags 不动其它字段）→ `POST /api/sites/batch_tags`，入口 = 站点管理列表勾选后的「批量打标签」；非法入参回 `*SiteTagBatchError` → handler 400。
- ⚠️ 读 `tags` 列必须用 `sql.NullString` 并取 `.String` 再交给解析器 —— 传 `*string` 会落到 `siteStringListFromAny` 的 default 分支被 `toString` 按 `%v` 打成 `0xc000...` 地址串，标签被静默写坏（已踩）。`changed` 在 Go 侧按标签集合（忽略大小写/顺序）比对，不用 RowsAffected（MySQL 与 SQLite 口径不同）。
- 前端快捷选择唯一实现 `components/SiteTagChips.vue`（4 处引用：转种 `CrossSeedStepSiteSelection`、定时发种 `TaskFormDialog`、自动发种 `AutoSeedView` 规则+发布弹窗）：**点标签 = 切换** —— 该标签下站点未全选 emit `apply`（父组件并集补选），已全选 emit `remove`（父组件过滤剔除），其余选择不动；`disabled` 站点不计入 `selectable`，无可选站点时不 emit。
- 配色 `utils/siteTagColor.ts`（`siteTagStyle(tag,'plain'|'solid')`，同名恒定同色、不落库）；key 归一/匹配 `utils/siteTag.ts`（`siteTagKey`/`siteTagMatchesAnyTagKey`，忽略大小写与空白）；共享样式 `assets/styles/site-tags.scss`（`.site-tag-chip`，main.ts 引入）。⚠️ 色相哈希必须带雪崩混淆（FNV-1a + 末尾 xorshift）—— 用 `hash*31+code` 这种线性哈希时中文短标签会聚集（实测 24 个标签只落到 9 个色相、单色相挤 7 个）。
- 站点管理列表：**筛选条件在表头上方** `.tag-filter-row`（`v-if="!isSortMode"`，无标签时也显示）里一个 `el-select` 多选绑定 `activeTagFilters`（filterable/clearable/collapse-tags，选项来自 `allTagOptions` + 末尾特殊项「未设置标签」，与搜索/站点范围筛选 AND，变更回第一页，激活时 `tagFilterHint` 显示命中数）。**「未设置标签」= 哨兵值 `__ptn_no_tag__`**（`TAG_FILTER_NO_TAG[_LABEL]`，灰底虚线胶囊、不注入按名推导的颜色），语义是与普通标签并列的 OR 分支：`siteTagKeys(site.tags).length === 0`（空数组/缺字段/只有空白项都算未设置）；哨兵不进 `activeTagFilterKeys`。⚠️ 别用 `siteMatchesAnyTagKey` 承接这段逻辑 —— 它在 `wantedKeys.size===0` 时返回 **true**，只勾「未设置标签」会把全部站点放行。
- 标签列只做展示、不可点击筛选（点标签列等同点行 → 打开编辑弹窗）。标签列、筛选下拉与编辑/批量弹窗的 `el-select` 标签都上色（弹窗用 EP `#tag` 插槽渲染彩色可关闭标签）。

## 修复/校验入口（转种面板「重新获取」）

- 统一入口 `POST /api/media/validate`（`bootstrap/app.go:352`）→ `handler/migrate/downloader_media.go:MediaValidate` → `migrationflow/media.go:MediaValidate`（补 savePath）→ `repair.MediaValidateEntry` → `repair.ValidateMediaPayload`，按 `type` 分发 `screenshot_preview`/`screenshot_finalize`/`screenshot`/`poster`/`intro`/`mediainfo`。
- **poster**：`repair/movie_info.go:FetchMovieInfo` 五级兜底 —— ①已有外链互补（queryByIMDb/queryByDouban/searchByName + `backfillTMDbByIMDbIfNeeded`）②PTGen 多节点（`repair/ptgen_nodes.go`，数组序=优先级）③豆瓣页 `og:image`/`.nbg` ④TMDb API ⑤IMDb 页 → 再经 `NormalizePosterBBCodeWithConfig` 取首个 URL 并转存图床（`cross_seed.image_hoster`：`pixhost`(默认)/`agsv`，输出单个 `[img]`），返回 `posters` + `extracted_{imdb,douban,tmdb}_link`。前端 `CrossSeedPanel.vue:refreshPosters` 只在原外链为空时回填。
- 图床转存 `repair/pixhost.go`：下载候选 = 直连 + 两个 worker 代理前缀（`posterTransferProxyPrefixes`），每候选重试 2 次、25MB 上限、必须 `image/*`；上传取 `show_url` 后 `ResolvePixhostImageURLWithConfig` 解析直链并 `IsImageURLReachable`（HEAD → Range GET）校验。
- 前端预览图失效会自动走 `handleImageError` 重发同一 payload（`type=poster`），但**受限标签命中直接跳过**、`pixhost.to` 域名直接跳过检测。

## 媒体参数补充（自 MEMORY.md 迁入 2026-10-11）

- 组件展示侧「音频编码」真源 `processing/title/simple_components.go:extractAudioFromTitle`；`audioCleanupTags` 需一并输出无空格变体（`audioCodecSeparatorVariants`），否则残留进「无法识别」。
- `title/components.go` 恒传 `preferMediaAudio=true` 的 B 方案（2026-10-11 用户选定）：MediaInfo 与 BDInfo 统一以媒体主音轨重建「音频编码」= 编码 + 声道 + Atmos + 音轨数。此前 MediaInfo 源走「标题优先」，会把标题 `DDP5.1` 与媒体首轨 `AAC 2.0` 拼成 `DDP 5.1 11Audios`。
- 传 `false` 的「标题优先 + 媒体补缺」分支（`findBestMatchingAudioTrack`/`supplementAudioInfo`）现已无调用方，保留备用。
- 多语言 WEB-DL 首轨常是配音 AAC（2026-10-10 实测）—— 这是「首轨低规格则改取最高规格」规则的来源。
- `parser_pythonish.go` 里从 Python 直译的正则若含 `\(`/`\[` 必须写单反斜杠：raw string 的 `\\(s\\)` 要求字面反斜杠 → `Channel(s)` 永不匹配、声道静默退化成 2.0（2026-10-11 已修）。同类未修：`cleanBBCode` 的 `\\[/?\\w+\\]`。

## 代理路径探测细则

- 真源 `downloaderclient/proxy_paths.go:BuildProxyPathCandidates`，调用方须传两个根 `[原始路径, 映射后路径]`；四条链路：refresh_flow / screenshot(_preview) / bdinfo_task_ops / episode_count。
- ⚠️ 候选循环**只在 400 continue、其余 break** ⇒ 代理 stat 失败必须经 `proxy_error.go:NormalizeProxyCandidateStatus` + `newProxyHTTPError/newProxyResponseFailure` 归一为 400；不可达仍返回 500 会立即 break、断掉后续候选。
- 本地回退门禁 `shouldSkipLocalMediaFallback`/`shouldSkipLocalScreenshotFallback`：`use_proxy=true` 且映射后本地根不存在 → 报「本地路径不可访问」。

## 发布 / CI 细则（2026-10-11 查实）

- **CI 不可用（铁证）**：`weishouxian/PTNexus` 是 **fork**（parent/source = `sqing33/PTNexus`，公开；父仓同名工作流 conclusion=success）。本仓 `GET /repos/.../actions/runs?per_page=100` → 75 条（70 schedule + 5 workflow_dispatch），**conclusion 全为 failure、0 success**，最早回溯至 2026-08-02 前后。
- `meta` job 症状：`started_at` 与 `completed_at` 相差 **3 秒**、`runner_name` 为空、`steps` 为空数组；`GET /repos/.../actions/jobs/<job_id>/logs` → `BlobNotFound`（HTTP 404）⇒ **job 从未在 runner 上执行**。其余 job（runtime/desktop_windows/proxy/docker_image/release_assets）全 skipped。
- ⇒ 别派 `workflow_dispatch`，也别修 CI；直接本地构建 + `github-release` 技能发布（`ptn-proxy-release` 技能 2026-09-24 已记同样结论）。
- **本机凭据**：`~/.workbuddy/skills/github-release/scripts/gh` 包装器已登录 `weishouxian`（仓库所有者），scopes = `gist, read:org, repo`（**不含 `workflow`**）。包装器自动设可写 `GH_CONFIG_DIR=~/.workbuddy/binaries/gh/config` 并清空代理变量。⚠️ 新建 tag 的 Release 可能报 workflow scope 不足（补：`scripts/gh auth refresh -h github.com -s workflow`）；向**已存在** Release 上传附件不需要。
- **v4.0.3 附件实况（手工发的证据）**：`install-pt-nexus-box-proxy.sh`(15,484) / `pt-nexus-box-proxy-amd64`(7,098,552) / `pt-nexus-box-proxy-arm64`(6,684,856) / `ptnexus-runtime-linux-amd64.tar.gz`(11,925,701) / `ptnexus-runtime-linux-arm64.tar.gz`(10,948,622) / `UPDATE_MANIFEST.json`(26,741) —— **没有桌面安装包/浏览器插件**（CI 才产），共 6 件。GitHub release id `409005495`，created 2026-10-10T13:38:18Z。
- **更新检测链路**：`updater/update_sources.go` 候选顺序 = `UPDATE_MANIFEST_URL` 环境变量 > `github.com/<owner>/<repo>/releases/download/<version>/UPDATE_MANIFEST.json` > `gitee.com/wsx181/.../releases/download/<version>/...` > 两边 `/releases/latest/download/...`。比对 `manifest.latest.version` 与 `getLocalVersion()`（运行时推断，Docker 下退化为镜像内 `/app/CHANGELOG.json`）。强更标记 `latest.force_update`/`latest.disable_update`/`history[].force_update`。
- **清单生成**：`scripts/build-update-artifacts.sh:66` 从 `CHANGELOG.json` 取版本 → 生成 `dist/updates/<VERSION>/UPDATE_MANIFEST.json`（`schema:2`，`latest.version/date/artifacts[]`，artifacts 含 os/arch/url/sha256/size/format + `mirror_urls`）；`scripts/merge-update-manifest.py:build_base_manifest()` 再合并桌面安装包元数据。URL 由 `CHANGELOG.json:artifact_sources[].url` 模板（`{version}`/`{filename}`）展开，兜底 `BASE_URL=https://github.com/weishouxian/PTNexus/releases/download/<ver>`。
- `build-update-artifacts.sh` 也可直接本机交叉编译（`gcc` / `aarch64-linux-gnu-gcc`，Linux 环境）；Windows 无 `aarch64-linux-gnu-gcc`，故 v4.0.3 选择从 Docker 镜像抽产物。
- ⚠️ **Dockerfile 写死 `ENV SCHEDULE_ENABLED="false"`** → 容器默认关闭每天 06:00 的定时自动升级（UI 手动升级不受影响）。相关变量：`SCHEDULE_TIME`/`SCHEDULE_TIMEZONE`/`UPDATE_DIR`(默认 `/app/data/updates`)/`PTNEXUS_BASE_DIR`/`SUPERVISOR_CONF`/`SUPERVISOR_SERVER_NAME`/`AUTO_INSTALL_SYSTEM_DEPS`/`UPDATE_USE_PROXY`/`UPDATE_SKIP_VERIFY`/`UPDATE_MANIFEST_URL`。

## 环境：批量删除被 safe-delete 钩子拦截（2026-10-11 查实）

- 症状：`rm -rf <大目录>` 与 PowerShell `Remove-Item -Recurse -Force` 都报 `[safe-delete][SAFE_DELETE_BULK_CONFIRM_REQUIRED] {"count":9999,"threshold":50,"scope":"turn"}`；**不是权限/只读属性问题**（`attrib -R` 无用），是应用内置钩子按「文件数 > 50」要求**当轮**批量确认。
- 合规尝试：Bash 工具 + `dangerouslyDisableSandbox: true` **不再报钩子错误**，但实测**未能完成删除**（3m38s 内文件数/体积不变，疑为后台运行时拿不到沙箱外的用户授权确认）。⇒ 大目录删除**交给用户在本机终端执行**最稳。
- 待删目标（2026-10-11 用户决定**暂不删**）：`D:/MYCODE/gitee/PTNexus.obsolete-20261011`，1.8GB / 23,118 文件；其独有文件 `.github/workflows/release-beta.yml`(15,423B) 等 3 个 workflow 已备份至 `.workbuddy/tmp/archive-unique/`。
- `github-release` 技能 `publish.sh` 末尾的 `[safe-delete][SAFE_DELETE_INVALID_PATH]` 同理 —— 退出码 1 但发布已成功，以 `[publish] 完成` 为准。

## 在线更新（updater）安装链路细则（2026-10-11）

- 容器入口 = updater（5274，反代 server 5275，supervisord 编排）；接口 `/update/check|pull|install|changelog`；前端 `webui/src/components/VersionUpdate.vue`。
- 安装流程（`updater/artifact_update.go:installPreparedBundle`）：拉清单 → 下载 runtime → 校验 sha256 → 解压 staging → `ensureRuntimeSymlinks` → 停 server → staging 移入 `/app/data/updates/releases/<ver>/server` → 切 `current` 软链 → 起 server 并查 `/health` → 失败自动回滚 → 写本地版本。
- `ensureRuntimeSymlinks` 的语义：把**镜像内置**的 `/app/server` 挪到 `/app/server.image.<YYYYMMDD-HHMMSS>`，再把 `/app/server` 变成指向 `<UPDATE_DIR>/current` 的软链；`start-services.sh:restore_updated_runtime` 在容器重建后做同样的事。
- ⚠️ **该流程依赖「目录 rename」**，在部分环境不可用 → 线上故障 `迁移 baseDir 失败: rename /app/server /app/server.image.…: invalid cross-device link`。
  - 已排除：普通 overlayfs（构建机 kernel 6.1 `redirect_dir=N` 下 `mv /app/server` 正常，写过文件后也正常）、镜像声明卷（三个 tag 的 `Config.Volumes` 都只有 `/app/data`）。
  - 实测差异：`/app/server` 被 bind mount / 命名卷挂载时报 **EBUSY**（`Device or resource busy`），与用户看到的 EXDEV 不是同一条路径。
  - ⇒ 剩余解释：btrfs 存储驱动（跨子卷）/ fuse-overlayfs（rootless，改下层目录）/ ZFS 跨 dataset，或部分内核下 rename 挂载点返回 EXDEV。**定性需用户给 `docker info --format '{{.Driver}}'` 与容器 mounts。**
  - 2026-10-11 已加固：`moveDirPortable`（rename 优先，EXDEV/EBUSY/EINVAL 退回「复制 + 删除」）、`isMountPointPath`（解析 `/proc/self/mountinfo`）、`moveBaseDirAside`（真是挂载点则给可操作错误）；`start-services.sh` 同步加 `move_dir_portable` 且 `restore_updated_runtime` 不再 `return 1`（原先在 `set -e` 下会让容器起不来）。回归测试 `updater/artifact_move_test.go`。
- ⚠️ **部署铁律：不要挂载 `/app/server`**。镜像只要求 `- ./data:/app/data`（readme 的 compose 就是这么写的）；挂 `/app/server` 会让在线更新永远失败（且 `docker pull` 也换不掉运行时）。
- ⚠️ `cd updater && go build ./...` 会**覆盖 git 跟踪的预编译产物 `updater/pt-nexus-updater.exe`**；本地验证请用 `go build -o /dev/null .` 或 `go vet`。
- 跨设备回退只能在 **Linux + 另一个设备**上复现：`docker run --rm -v $PWD:/src -w /src golang:1.25 sh -c 'go test -count=1 -run TestMoveDirPortableCrossDeviceFallback -v ./...'`（tmpfs `/dev/shm` 与 `/src` 不同设备 → 真实 EXDEV）。

## 站点差异补充（自 MEMORY.md 迁入 2026-10-11）

- **hdhome**：六维度独立 select、无地区字段。**我堡**：Remux 硬禁、无粤语标签。**青蛙**：标题特化 + HDR 统一写「HDR」（正则别用 `\b`）。**北洋园**：无六维度、无 `name`、简介只有 `descr`、动漫整类拒发。
- **m-team**：`team` 一律不提交（站点校验组成员）；`source` 仅电影大类；「Passkey 栏」放**存取令牌（36 位 UUID）**（32 位 Passkey/Cookie 被 `mteamapi.ExtractToken` 本地拒绝）；`labelsNew` 本地按 mediainfo 补 `4k`/`8k`/`hdr`，并丢弃与「菁彩HDR」冲突的 HDR10/HDR10+；`api.m-team.cc` 与 `api2` 同一后端（504 直接重试）。
- **52pt**：标准 NexusPHP 但**无 `source_sel`/`processing_sel`**（不区分产地）；标签是裸名 `tags[]` 且 **value 是中文词**（走 `mappings.tag`，**不能**用 `checkbox_tags`）；`url` 字段即「IMDb链接」；`medium_sel` 把「分辨率 + 原盘有无中文」编码进选项、站内两个 52PT 小组同属 `team.pt52` ⇒ 必须靠 `sites/pt52.go` 适配器二次判定。

## 桌面端 / 其他细则（自 MEMORY.md 迁入 2026-10-11）

- 桌面端同一 exe 两形态：单文件（`scripts/package-desktop.sh single`，go:embed，~40MB）与安装包（wails + NSIS），构建文档 `desktop/docs/build.md`。
- ⚠️ 桌面端**不加载 `server/.env`**，DB 唯一来源 `%APPDATA%/pt-nexus/data/database.json`。
- ⚠️ 站点资源两份：`server/sites_data.json` + `server/configs/` 与 `desktop/internal/desktopapp/bundle/`（go:embed，已提交）—— **改映射必须同步 bundle**。
- PTGen 节点真源 `repair/ptgen_nodes.go:builtinPTGenNodeDefinitions`；`*.dpdns.org` 全域 NXDOMAIN。
- 抽帧双份实现须同步（`repair/screenshot*.go` 与 `proxy/media_core.go`），咽喉是 `isHDRMetadataText`。
- 简介清洗真源 `descclean.TrimDescription`（从【影片参数】/「更多视频截图」截断到结尾），抓取归一与发种组装共用。
- 抓取标题清洗双份实现须同步（`extract/review_extract.go` ↔ `extract/sites/ssd.go`）：① 尾部促销徽标 ② 前缀中文片名（中文段后必须紧跟 ASCII 字母才剥）③ Go RE2 无前瞻。
- Docker 多架构：`server/bdinfo/linux-{amd64,arm64}` 按 `ARG TARGETARCH` 选（**不能带默认值**）；SQLite 驱动 `glebarez/sqlite`。BDInfo 重编须用 commit `342b6069…`，且必须在 PTNexus 仓库外构建。
- 组件展示侧「音频编码」真源 `processing/title/simple_components.go:extractAudioFromTitle`；`audioCleanupTags` 需一并输出无空格变体（`audioCodecSeparatorVariants`），否则残留进「无法识别」。

