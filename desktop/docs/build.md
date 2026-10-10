# build.md

## 目标

记录 `desktop` 的开发、单文件打包与安装包打包流程，以及各方式的依赖差异。

## 一、单文件 exe（推荐，日常分发用）

```bash
bash scripts/package-desktop.sh single
```

产物：`desktop/build/bin/pt-nexus.exe`（约 40 MB）。

该命令内部完成三件事：

1. `prepare_frontend_stage`：`pnpm -C webui run build` 并把产物同步到 `desktop/frontend/dist`（供 `go:embed`）；
2. `sync_embedded_bundle`：把 `server/sites_data.json` 与 `server/configs/` 同步到 `desktop/internal/desktopapp/bundle/`（供 `go:embed`）；
3. `go build -tags desktop,production -ldflags "-w -s"`。

依赖：仅需要 `go` 与 `pnpm`。**不需要** `wails` CLI、NSIS、`rsync`。

产物形态：单个 exe。首启动会自行创建 `%APPDATA%/pt-nexus`，并把内嵌站点资源展开到 `resources/`，无需任何外挂文件。

> 说明：`gcc`/CGO 也不需要。sqlite 驱动是纯 Go 的 `glebarez/sqlite`。

## 二、安装包（多文件，走 NSIS）

```bash
bash scripts/package-desktop.sh
```

产物：`desktop/build/bin/` 下的 `pt-nexus.exe`、`pt-nexus-<版本>-amd64-installer.exe`、`pt-nexus-<版本>-amd64-update.exe`，另加 `desktop/build/windows/sidecar/` 里的 `server.exe`、`updater.exe`、`configs/`、`bdinfo/`。

依赖（三者缺一不可）：

1. `wails` CLI：`go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0`
2. NSIS：Ubuntu/WSL 用 `sudo apt install -y nsis`；Windows 装 NSIS 官方安装包
3. `rsync`：可选，缺失时脚本自动退回 `cp`（Windows Git Bash 通常没有 rsync）

## 三、开发模式

```bash
bash scripts/package-desktop.sh desktop-dev
```

以 `wails dev` 启动，前端热更新。需要 `wails` CLI。

## 增量缓存

`package` 与 `single` 都会使用 `desktop/build/.package-cache` 做阶段级缓存，重复执行会跳过未变化的前端、sidecar 与安装包步骤。

强制全量重建：

```bash
PTNEXUS_PACKAGE_FORCE=1 bash scripts/package-desktop.sh single
```

## 输出产物一览

| 命令 | 产物 |
|---|---|
| `single` | `desktop/build/bin/pt-nexus.exe`（单文件，可直接分发） |
| `package` | 单文件 exe + NSIS 安装包/更新包 + `build/windows/sidecar/` 全部运行时文件 |
| `desktop-dev` | Wails dev 运行时 |
