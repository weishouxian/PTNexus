# PTNexus 长期约定（精简索引）

> ⚠️ 有注入长度上限：本文件只放铁律与索引，**细节一律读 `.workbuddy/memory/MEMORY-details.md`**（dupe 全链、站点标签、媒体参数全链、发种链路细则、52pt/m-team 等站点差异、代理探测、发布/CI 与 v4.0.3 发版流程、桌面端细则）。
> 技能：`ptn-deploy`/`ptn-proxy-release`/`github-release`/`ptn-desc-clean`/`ptn-downloader-scope`/`ptn-publish-pacing`/`ptn-publish-progress`/`ptn-publish-field-diagnose`/`ptn-publish-site-setup`(含 `references/known_sites.md`)/`ptn-manual-screenshots`/`ffmpeg-hdr-frame-capture`。站点映射真值 = `server/configs/<site>.yaml`。

## 环境 / 流程
- 主工作区直接改、不建 worktree；改完给 `git status`/`git diff`；提交/回滚/改历史须当轮明确要求。
- **双推**：唯一目录 `D:/MYCODE/PTNexus`，`origin` 两个 pushurl（github SSH `weishouxian/PTNexus` → gitee HTTPS `wsx181/PTNexus`）⇒ 一次 `git push` 推两边；另留 `gitee` remote 备用。⚠️ `set-url --add --push` 必须把原 github 也 add 进去。旧目录 `D:/MYCODE/gitee/PTNexus.obsolete-20261011`（1.8GB，**2026-10-11 用户决定暂不删**）。
- **⚠️ gitee 铁律**：① 本地绝不能浅克隆（gitee 拒 `shallow update not allowed`，GitHub 容忍）—— 已 unshallow。② 写权限只能用 `wsx181` 私人令牌（本机 SSH key 属 `wsxqq`，被拒），令牌已存 GCM，remote 走 HTTPS。③ 仓库容量已用 827MB/1GB。
- **⚠️ GitHub 直连国内 ~50 KB/s**；用 `https://gh-proxy.com/https://github.com/<user>/<repo>.git` 可跑 10–14 MiB/s（`ghfast.top` ~4MiB/s）：临时 remote → `git fetch --unshallow` → `remote remove`。⚠️ 中断 fetch 残留 `.git/shallow.lock` 须手删。
- 两仓同根 `c6c3a3b5`(2025-08-13)，`2d5ed176`(2026-04-07) 分叉；gitee 旧线存分支 `legacy-gitee-20261011`(=`c7e277c`)。
- **⚠️ CI 从来跑不起来，别再碰**：本仓是 **fork**（父仓 `sqing33/PTNexus`），`release-manual.yml` 75 次运行 **0 成功**（`meta` job 3 秒秒挂 / `runner_name` 空 / 无 step / 日志 `BlobNotFound`）。⇒ 发布一律**本地构建 + `github-release` 技能**。
- **发版**：版本真源 = `CHANGELOG.json:history[0].version`，加完跑 `python sync_changelog.py` 同步 `readme.md`。app「更新提示」只认 **Release 附件里的 `UPDATE_MANIFEST.json`**（`updater/updater.go`，源见 `update_sources.go`）——**改 CHANGELOG 不发 Release 就没有提示**；**仓库根那份同名文件是死文件（停在 v4.0.2），别改**。**新 Release 必须补齐 `releases/latest/download/` 引用的全部 6 件附件**（runtime×2 + box-proxy×2 + install 脚本 + manifest）。v4.0.3 既定流程见 details。
- 工具链：Bash 先 export PortableGit PATH；Go `/d/go/go1.25.11/bin/go`、pnpm `~/AppData/Roaming/npm/pnpm`、venv `C:/Users/wind/.workbuddy/binaries/python/envs/default/Scripts/python.exe`。
- 校验：前端 `pnpm -C webui run type-check`（`lint` 存量报错不得新增）；后端 `go build ./...` + `go vet`。
- ⚠️ `core.autocrlf=true`：**gofmt 前先 `tr -d '\r'`**；**绝不对目录跑 `gofmt -w <dir>`**（改整目录行尾、凭空多差异，已踩两次），只逐个文件。
- 临时测试写 `zz_tmp_*_test.go`，验完备份 `.workbuddy/tmp/test-backup/` 再删（`server/` 下另有 4 个 git 跟踪正式测试，勿删）；`-mod=mod` 污染 go.mod，用完还原。
- 通知走 `wsl-win-notify`；本机 toast 会挂起，须加 `--channel msg`。
- ⚠️ **删大目录会被内置 safe-delete 钩子拦**（目标文件数 > 50 → `SAFE_DELETE_BULK_CONFIRM_REQUIRED`，与权限/只读属性无关）；`dangerouslyDisableSandbox` 虽不报错但后台运行时也删不动 ⇒ **大目录删除交给用户在本机终端做**。`publish.sh` 结尾的 `SAFE_DELETE_INVALID_PATH`/退出码 1 同理，不代表失败。

## 发布 / 数据
- `5.135.178.15`（OVH，`/home/code/PTNexus`）是**构建推送机**非生产（buildx push Docker Hub `fenglaile/ptnexus`；GHCR 永久排除）；动手前查 `ps -ef | grep '[b]uildx build'`。⚠️ 多平台 buildx 必须用 `debian` 用户、**不能 sudo**（builder `multiarch` 配在该用户下）。SSH 见 `ptn-deploy` 技能。
- ⚠️ 容器 `/etc/localtime` 仍 UTC → DSN 必须显式 `loc=Asia/Shanghai`。
- ⚠️ **部署铁律：不要挂载 `/app/server`**（镜像只要求 `- ./data:/app/data`）。在线更新要把 `/app/server` 改名成软链，挂载后 rename 必失败（`invalid cross-device link` / `device or resource busy`），且 `docker pull` 也换不掉运行时。细则与 2026-10-11 加固见 details。
- 内网 DB `192.168.5.3:3306/pt_nexus`（root/111111，pymysql）。`publish.sh` 退出码 1 不代表失败（trap 被 safe-delete 拦），以 `[publish] 完成` 为准。
- ⚠️ 无外键。删某颗种子的库内数据 = 按 hash 删 **`torrents`/`seed_parameters`/`torrent_upload_stats`**（重测必须一起删）。应用内「一种多站」删除只删 torrents + torrent_upload_stats。
- 进度/日志列表「大小」两表无 size 列 → 旁路关联 hash 取 `torrents.size`（日志页覆盖 ~63%）；`migrationflow/publish_size.go`，DTO `gorm:"-"`，`SizeByHashes` 刻意不过滤 is_hidden。

## 配置 / 下载器 / 代理
- ⚠️ **DB 开关值类型是 int8**（MySQL TINYINT(1) 经 GORM Scan；SQLite int64、PG int32、原生 SQL `[]byte`）⇒ **任何 `switch v.(type)` 必须覆盖全部整型宽度 + bool + string + []byte**（dupe 的 `toBool` 只写 int/int64/float64/string → 开关开着读成 false、校验静默失效）。参照 `repository/value_helpers.go:toInt64`。
- ⚠️ **MySQL DSN 未开 `clientFoundRows`** → 值未变时 `RowsAffected=0`，**凡「用 `RowsAffected>0` 判存在」的 UPDATE 都错**。已修 `site_repository.go` 三个 Update*（改按主键/nickname 回查 `siteExists`）；`sites_handler.go:UpdateSite` 已拆 `err` 与 `!updated`。
- 配置存 DB `app_settings`（DB 优先）；Docker 下改 `server/data/config.json` 无效，改文件即生效用环境变量。菜单显隐 `PTNEXUS_SHOW_*` → `menu_visibility.go` → 前端 `App.vue` `v-if`（只隐入口不拦路由）。
- 顶部「全局下载器」是全站下载器筛选唯一来源（页面内入口已移除，`onMounted` 无条件覆盖）。下载器配置字段名是 **`host`**（写 `url` → 首页「连接失败」）。
- 站点角色 `sites_data.json:migration`（0 不参与/1 仅源/2 仅目标/3 双向）；唯一写入是启动 `SyncSitesFromJSON`，门禁 `source_site.go`/`publish_with_context.go`（位不对 → 403）。⚠️ 缺该字段写成 0；JSON 不能含注释。
- **代理路径探测**：真源 `downloaderclient/proxy_paths.go:BuildProxyPathCandidates`（传两个根 `[原始, 映射后]`）；⚠️ 候选循环**只在 400 continue、其余 break** ⇒ 失败必须经 `proxy_error.go:NormalizeProxyCandidateStatus` 归一为 400。细则见 details。
- 站点导入/导出 `repository/sites_backup.go`：`GET /api/sites/export`（全量含 cookie/passkey）+ `POST /api/sites/import`（**仅补空、只更新不新增**、按 site→nickname→base_url 匹配、**cookie/passkey 一组：任一有值则整组不动**）；入口 = 站点管理工具栏。
- **站点标签**：真源 `sites.tags`（JSON 数组）；批量 `POST /api/sites/batch_tags`；前端唯一实现 `components/SiteTagChips.vue`；配色 `utils/siteTagColor.ts`。⚠️ 读 `tags` 必须 `sql.NullString` 取 `.String`；色相哈希须雪崩混淆。全链见 details。

## 发种链路（要点）
- 唯一入口 `workflow/publish_entry.go` → `publish_target.go:PublishTorrentToTarget`；日志 `region=<字段>:<值>` 是排查第一现场。
- ⚠️ 三条铁律：① 站点硬性拒绝必须用 `*publisher.PreCheckError`（用 `errors.New` 会变 500 污染统计）② 语种以 MediaInfo 音轨为准，**不与源站标签取并集** ③ 动画标签优先于类型字段。
- ⚠️ `mappings.<类目>` 直接提交站点 select，须对齐 upload.php 实测 value；`default` 兜底掩盖错值、`@index:N` 越界静默退化；站点不存在的字段被静默丢弃 → 在 `AdjustFormFields` 回填/清理。
- ⚠️ 标题默认沿用源标题，站点冲突在适配器 `normalizeXxxTitle` 纠偏；别改全局解析或 `global_mappings.yaml`。
- `IsExistingTorrent` → 日志 `exists` 不重试；非 200 / success=false 必判 failed；torrent_id ≠ infohash。⚠️ 队列行状态与日志状态是两套判定。
- **dupe 查重**：`sites` 表三列（`dupe_check_enabled`/`dupe_size_tolerance_bytes`/`dupe_rules` JSON，**唯一规则来源**，只 6 类媒介组键 + 兜底 `"*"`）；未配走兜底，兜底关闭 → **直接跳过校验**。站点是否参与由 `configs/<site>.yaml:dupe_check` 声明。配置入口 = 站点管理「Dupe 设置」弹窗 → `POST /api/sites/update_dupe`（**不可复用全量 `/sites/update`**）。单位 MB(1024²) 存字节，**0 合法**。

## 站点差异
- 查 `ptn-publish-site-setup/references/known_sites.md`；hdhome / 我堡 / 青蛙 / 北洋园 / m-team / 52pt 的具体差异**见 details**。

## 媒体参数（要点，**全链见 details**）
- 标准值真源 `acquire/extract/review_extract.go:InferStandardizedValues`；产地只认简介「◎产　　地」、年份只认「◎年　　代」。
- **音频编码唯一入口 `acquire/extract/audio_codec_infer.go:InferAudioCodecKey`**；标签侧 `tagging.ExtractRawTagsFromAudioCodec` 同源。⚠️ 选轨口径 `mediainfo_codec.go:audioCodecKeyForInference`：默认首轨；**首轨是已识别低规格配音轨（rank ≤ `audio.aac`=12）且有更高规格时改取最高**；**首轨 rank=0（TrueHD `Format : MLP FBA`）必须原样返回空串**。
- ⚠️ **DTS 细分只走 `dtsCodecKeyFromText`**：**DTS-HD MA** 在 MediaInfo 里是 `Format : DTS XLL`（DTS:X 才是 `DTS XLL X`），别用 `contains("DTS X")`；展示侧 DTS 家族必须走 `extract.DTSAudioCodecKeySpan`。
- ⚠️ 选轨规则三处消费方勿各写一份：标准值 / 组件展示 `media/parser_pythonish.go:fromTracks`→`pickMainAudioTrack` / 站点策略 `selectAudioTrack`。⚠️ `parser_pythonish.go` 直译正则含 `\(`/`\[` 必须写单反斜杠（`\\(s\\)` → 声道静默退化 2.0）；`splitTitleAndTeamPythonish` 误切 `DTS-HDMA5.1-52pt`（已加护栏）、**`WEB-DL` 误切未修**。
- ⚠️ **BDInfo 段头是 `AUDIO:`**（MediaInfo 段切分只认 `^\s*Audio$`）→ 已加 `parseBDInfoAudioTracks` 回退；`title/components.go` 的 `ApplyMediaInfoOverrides` **恒传 `preferMediaAudio=true`**。⚠️ `ReplaceTitleAudioCodecToken` 只换编码不换声道。语种只在 MediaInfo **Audio 段**读；蓝光 ISO 直读只出 General，须 `mount -o loop,ro` 读 mpls。

## 桌面端 / 其他
- 桌面端同一 exe 两形态（单文件 / wails+NSIS 安装包）；⚠️ **不加载 `server/.env`**；⚠️ 站点资源两份（`server/` 与 `desktop/internal/desktopapp/bundle/`）—— **改映射必须同步 bundle**。详见 details。
- PTGen 节点真源 `repair/ptgen_nodes.go:builtinPTGenNodeDefinitions`；`*.dpdns.org` 全域 NXDOMAIN。简介清洗真源 `descclean.TrimDescription`。抽帧（`repair/screenshot*.go` ↔ `proxy/media_core.go`）与抓取标题清洗（`extract/review_extract.go` ↔ `extract/sites/ssd.go`）**两份实现都须同步**。
- Docker 多架构：`server/bdinfo/linux-{amd64,arm64}` 按 `ARG TARGETARCH` 选（**不能带默认值**）；SQLite 驱动 `glebarez/sqlite`。BDInfo 重编须用 commit `342b6069…`，且必须在仓库外构建。
