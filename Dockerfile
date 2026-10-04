# 阶段 1: 构建 Vue 前端（Go 版本）
# 前端产物是纯静态文件（与目标架构无关），固定跑在构建机平台，
# 避免多平台构建时在 QEMU 模拟的 arm64 下执行 pnpm install / vite build（极慢）。
FROM --platform=$BUILDPLATFORM node:20-alpine AS webui-builder

WORKDIR /app/webui

RUN corepack enable

COPY ./webui/package.json ./
RUN pnpm install --no-frozen-lockfile

COPY ./webui ./
RUN pnpm run build


# 阶段 2: 构建 Go 更新服务
# updater 是纯 Go（CGO_ENABLED=0），固定构建机平台 + 交叉产出目标架构二进制。
FROM --platform=$BUILDPLATFORM golang:1.23-bookworm AS updater-builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src/updater

ENV GOPROXY=https://goproxy.cn,direct

COPY ./updater/go.mod ./updater/go.sum ./
RUN go mod download

COPY ./updater/*.go ./
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /out/updater .


# 阶段 3: 构建 Go 后端（server）
# 注意：server 走 glebarez/sqlite（modernc.org 纯 Go 实现），依赖树里没有任何第三方 CGO 库；
# 这里沿用 CGO_ENABLED=1（标准库 net/os/user 会因此启用 cgo → 动态链接），
# 所以仍然需要「目标架构」的 C 编译器，故固定构建机平台 + 交叉编译，避免在 QEMU 下编译。
FROM --platform=$BUILDPLATFORM golang:1.23-bookworm AS server-builder

ARG TARGETOS
ARG TARGETARCH
ARG BUILDARCH

WORKDIR /src/server

ENV GOPROXY=https://goproxy.cn,direct

# 编译工具链按「构建机架构 → 目标架构」选择：
#   同架构          → 本机 gcc（无需任何交叉包）
#   amd64 → arm64  → gcc-aarch64-linux-gnu
#   arm64 → amd64  → gcc-x86-64-linux-gnu
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends gcc g++ libc6-dev; \
    if [ "${TARGETARCH}" != "${BUILDARCH}" ]; then \
      case "${TARGETARCH}" in \
        arm64) apt-get install -y --no-install-recommends gcc-aarch64-linux-gnu libc6-dev-arm64-cross ;; \
        amd64) apt-get install -y --no-install-recommends gcc-x86-64-linux-gnu libc6-dev-amd64-cross ;; \
        *) echo "Unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
      esac; \
    fi; \
    rm -rf /var/lib/apt/lists/*

COPY ./server/go.mod ./server/go.sum ./
RUN go mod download

COPY ./server/cmd ./cmd
COPY ./server/internal ./internal

RUN set -eux; \
    if [ "${TARGETARCH}" = "${BUILDARCH}" ]; then \
      cc=gcc; \
    else \
      case "${TARGETARCH}" in \
        arm64) cc=aarch64-linux-gnu-gcc ;; \
        amd64) cc=x86_64-linux-gnu-gcc ;; \
        *) echo "Unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
      esac; \
    fi; \
    CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH="${TARGETARCH}" CC="$cc" \
      go build -ldflags="-s -w" -o /out/server ./cmd/server


# 阶段 4: 最终运行环境（纯 Go 运行时：updater 作为入口，反代到 server）
FROM debian:bookworm-slim

WORKDIR /app

# 确保容器内对 localhost 和 127.0.0.1 的请求直接连接，不通过代理
ENV no_proxy="localhost,127.0.0.1,::1"
ENV NO_PROXY="localhost,127.0.0.1,::1"

# 禁用 updater 的定时自动更新
ENV SCHEDULE_ENABLED="false"

# server 运行目录与数据目录（对齐 updater/batch 使用的 /app/data）
ENV PTNEXUS_BASE_DIR="/app/server"
ENV PTNEXUS_DATA_DIR="/app/data"
ENV PTNEXUS_BDINFO_DIR="/app/bdinfo/linux"

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    ffmpeg \
    mpv \
    mediainfo \
    util-linux \
    fonts-noto-cjk \
    libicu-dev \
    supervisor \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# --- Go 版后端文件 ---
RUN mkdir -p /app/server
COPY --from=server-builder /out/server /app/server/server
RUN chmod +x /app/server/server

# 配置与站点数据（server 默认从 baseDir 下读取）
COPY ./server/configs /app/server/configs
COPY ./server/sites_data.json /app/server/sites_data.json

# --- Go 版前端产物：放入 server 的静态目录 ---
COPY --from=webui-builder /app/webui/dist /app/server/dist

# --- BDInfo（按目标架构选择对应的 Linux 工具，避免打入 Windows 版本）---
# TARGETARCH 由 BuildKit/buildx 自动注入（amd64 / arm64），对应 server/bdinfo/linux-<arch>/
# ⚠️ 这里必须写 `ARG TARGETARCH`（不带默认值）：一旦写成 `ARG TARGETARCH=amd64`，
#    该默认值会覆盖 buildx 注入的目标架构，导致 arm64 镜像里被塞进 amd64 的 BDInfo。
ARG TARGETARCH
RUN mkdir -p /app/bdinfo/linux
COPY ./server/bdinfo/linux-${TARGETARCH}/ /app/bdinfo/linux/
RUN chmod +x /app/bdinfo/linux/BDInfo /app/bdinfo/linux/BDInfoDataSubstractor

# --- updater ---
COPY --from=updater-builder /out/updater /app/updater
RUN chmod +x /app/updater

# 复制版本文件（updater 默认读取 /app/CHANGELOG.json）
COPY ./CHANGELOG.json /app/CHANGELOG.json

# Supervisor + 启动脚本（Go 版）
COPY ./supervisord.conf /app/supervisord.conf
COPY ./start-services.sh /app/start-services.sh
RUN chmod +x /app/start-services.sh

# 创建数据目录，用于持久化存储（对齐原版镜像路径）
RUN mkdir -p /app/data /app/data/tmp
VOLUME /app/data

# 对外只暴露 updater 端口（入口反代）
EXPOSE 5274

CMD ["./start-services.sh"]
