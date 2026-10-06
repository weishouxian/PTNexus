# desktop

`desktop` 是 PT Nexus 的桌面壳工程（Wails），负责把 `webui` 前端与 `server` 后端组装成一个 Windows 桌面应用。

## 分发形态

同一个 exe 同时支持两种运行形态，启动时自动选择，无需额外开关：

| 形态 | 组成 | 何时使用 |
|---|---|---|
| **单文件**（推荐） | 只有 `pt-nexus.exe` | server 在进程内启动；前端由 Wails embed、站点资源由 `go:embed` 内嵌 |
| 多文件 / 安装包 | `pt-nexus.exe` + `server.exe` + `updater.exe` + `configs/` | 进程内 server 初始化失败时的自动回退；NSIS 安装包走这条 |

两种形态都监听 `127.0.0.1:5275`，前端代理层无感知差异。

## 关键路径

用户数据目录：`%APPDATA%/pt-nexus`

- `data/database.json` —— **桌面端数据库唯一来源**（首次生成，默认 sqlite）
- `data/logs/ptnexus.log` —— 运行日志
- `data/pt_stats.db` —— sqlite 数据文件
- `resources/` —— 内嵌站点资源（`sites_data.json` + `configs/`）的展开目录，**每次启动覆盖**

端口：`127.0.0.1:5275`（server）、`127.0.0.1:5274`（updater，可选，缺失时仅「更新」功能不可用）

## 环境变量隔离（重要）

桌面端**不会**加载 `server/.env`。进程内启动绕过了 `server/cmd/server` 的 `main()`，因此那段自动搜寻 `.env` 的逻辑不会执行。

另外，当 `database.json` 已写明 `type` 时，启动会主动清除继承来的 `DB_TYPE` / `MYSQL_*` / `POSTGRES_*`。原因：`repository.NewStore` 里环境变量优先级高于 `database.json`，一旦继承到 shell 或 `.env` 残留（典型：`DB_TYPE=mysql` 指向内网实例），桌面版就会连错库并阻塞到健康检查超时。

需要走自定义资源目录时，用 `PTNEXUS_RESOURCE_DIR` 显式覆盖。

## 构建

```bash
# 单文件 exe（不需要 wails CLI / NSIS / rsync）
bash scripts/package-desktop.sh single

# 产物
desktop/build/bin/pt-nexus.exe
```

安装包与开发模式见 `docs/build.md`。

## 目录说明

- `internal/desktopapp/` —— 路由分流、进程内 server 与外部 sidecar 生命周期、内嵌资源展开
- `internal/tray/` —— 系统托盘
- `bundle/` —— 内嵌站点资源，由 `scripts/package-desktop.sh` 在构建前从 `server/` 同步
- `build/` —— Wails 打包资源与产物（`build/bin` 为产物目录，已 gitignore）
