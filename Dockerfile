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

# Debian 软件源回退：境外构建机（OVH）直连官方源最快；国内机器（如 fnOS）访问
# deb.debian.org 会被超时，导致 apt 索引全空、所有包 "Unable to locate"。
# 这里先试官方源，失败自动切清华镜像；也可用 --build-arg DEBIAN_MIRROR=xxx 显式指定。
# 注意两点：
#   1) bookworm 起官方镜像用 deb822 格式的 debian.sources，老镜像才有 sources.list，两个都处理；
#   2) apt-get update 遇到「部分源失败」退出码仍是 0，所以用输出里的 Err:/Failed to fetch 判定。
ARG DEBIAN_MIRROR=""

# 编译工具链按「构建机架构 → 目标架构」选择：
#   同架构          → 本机 gcc（无需任何交叉包）
#   amd64 → arm64  → gcc-aarch64-linux-gnu
#   arm64 → amd64  → gcc-x86-64-linux-gnu
RUN set -eux; \
    if [ -n "${DEBIAN_MIRROR}" ]; then \
      for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
        if [ -f "$f" ]; then sed -i "s|deb.debian.org|${DEBIAN_MIRROR}|g" "$f"; fi; \
      done; \
    fi; \
    if apt-get update -o Acquire::Retries=0 2>&1 | grep -qE "^Err:|W: Failed to fetch"; then \
      echo "官方 Debian 源不可达，切换为 mirrors.tuna.tsinghua.edu.cn"; \
      for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
        if [ -f "$f" ]; then sed -i "s|deb.debian.org|mirrors.tuna.tsinghua.edu.cn|g" "$f"; fi; \
      done; \
      apt-get update; \
    fi; \
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

# BUILD_STAMP 用于强制 server 编译层失效：BuildKit 对 COPY 目录的缓存 key
# 在某些 NAS（fnOS 的 docker 0.25.2）上无法正确反映目录内容变化，导致改了
# server/internal 下的 .go 后 COPY 层仍 CACHED、go build 复用旧二进制。
# 通过每次 build 传入不同的 --build-arg BUILD_STAMP，可稳定触发重编译。
ARG BUILD_STAMP=""

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
      go build -ldflags="-s -w -X main.buildStamp=${BUILD_STAMP}" -o /out/server ./cmd/server


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

# Debian 软件源回退，逻辑同 server-builder 阶段：官方源不通时自动切清华镜像。
ARG DEBIAN_MIRROR=""

RUN set -eux; \
    if [ -n "${DEBIAN_MIRROR}" ]; then \
      for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
        if [ -f "$f" ]; then sed -i "s|deb.debian.org|${DEBIAN_MIRROR}|g" "$f"; fi; \
      done; \
    fi; \
    if apt-get update -o Acquire::Retries=0 2>&1 | grep -qE "^Err:|W: Failed to fetch"; then \
      echo "官方 Debian 源不可达，切换为 mirrors.tuna.tsinghua.edu.cn"; \
      for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
        if [ -f "$f" ]; then sed -i "s|deb.debian.org|mirrors.tuna.tsinghua.edu.cn|g" "$f"; fi; \
      done; \
      apt-get update; \
    fi; \
    apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    xz-utils \
    mpv \
    mediainfo \
    util-linux \
    fonts-noto-cjk \
    libicu-dev \
    supervisor \
    libvulkan1 \
    mesa-vulkan-drivers \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# --- ffmpeg 9.0（静态构建，替换 Debian 源的 5.1）---
# Debian bookworm 源的 ffmpeg 仍是 5.1，对 HEVC m2ts 快速 seek 会报
# "Could not find ref with POC" 产出花帧；且 HDR10/HLG 截图需 libplacebo 做色调映射，
# 5.1 的 libplacebo 过老。这里下载 BtbN 的静态 ffmpeg 9.0（含 libplacebo），
# 软链 ffmpeg/ffprobe 到 /usr/local/bin，使 server 的 resolveBinary("ffmpeg", ...)
# 走 PATH 即可命中 9.0，无需额外环境变量。
# 架构命名：amd64 -> linux64，arm64 -> linuxarm64。
ARG TARGETARCH
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) FF_ARCH="linux64" ;; \
      arm64) FF_ARCH="linuxarm64" ;; \
      *) echo "Unsupported TARGETARCH for ffmpeg: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    FF_VER="n9.0-latest"; \
    FF_DIR="ffmpeg-${FF_VER}-${FF_ARCH}-gpl-9.0"; \
    FF_TARBALL="${FF_DIR}.tar.xz"; \
    FF_URL="https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/${FF_TARBALL}"; \
    mkdir -p /opt/ffmpeg-static; \
    cd /opt/ffmpeg-static; \
    if ! curl -fsSL --retry 3 -o "${FF_TARBALL}" "${FF_URL}"; then \
      echo "GitHub 直连失败，尝试 gh-proxy.com 镜像"; \
      curl -fsSL --retry 3 -o "${FF_TARBALL}" "https://gh-proxy.com/${FF_URL}"; \
    fi; \
    tar -xJf "${FF_TARBALL}"; \
    ln -sf "/opt/ffmpeg-static/${FF_DIR}/bin/ffmpeg" /usr/local/bin/ffmpeg; \
    ln -sf "/opt/ffmpeg-static/${FF_DIR}/bin/ffprobe" /usr/local/bin/ffprobe; \
    rm -f "${FF_TARBALL}"; \
    ffmpeg -version | head -1; \
    ffprobe -version | head -1

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
