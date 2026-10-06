package desktopapp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/bootstrap"
)

// EmbeddedServerAddr 是桌面端进程内 server 的监听地址。
// 与外部 sidecar 模式保持一致，前端代理层无需区分两种模式。
const EmbeddedServerAddr = "127.0.0.1:5275"

// embeddedServerStartTimeout 覆盖首次启动的建表/迁移耗时（SQLite 首建 < 5s，留足余量）。
const embeddedServerStartTimeout = 90 * time.Second

// StartEmbeddedServer 在桌面进程内直接启动 server 的 Gin 引擎，不再依赖外部 server.exe，
// 这是「单文件 exe」分发的关键：前端（Wails 内嵌 dist）与后端（进程内 Gin）同一进程。
//
// 与外部 sidecar 的关键差异：
//   - 不走 server/cmd/server 的 main()，因此不会加载仓库或安装目录里的 server/.env，
//     桌面端数据库一律以 %APPDATA%/pt-nexus/data/database.json 为准；
//   - 生命周期跟随桌面进程，无需 Stop()（Sidecar.cmd 为 nil，ProcessID 返回 0）。
//
// 失败场景：bootstrap 初始化失败或健康检查超时，调用方应回退到外部 server.exe。
func StartEmbeddedServer(env DesktopRuntimeEnv) (*Sidecar, error) {
	baseURL := "http://" + EmbeddedServerAddr

	// 已有人监听（用户同机开着 sidecar 版桌面端或本地调试实例）时直接复用，避免端口冲突。
	if isHTTPReady(EmbeddedServerAddr) {
		return &Sidecar{Name: "server", BaseURL: baseURL}, nil
	}

	applyEmbeddedServerEnv(env)

	app, err := bootstrap.NewApp()
	if err != nil {
		return nil, fmt.Errorf("embedded server init failed: %w", err)
	}

	go func() {
		// Engine.Run 仅在监听失败时返回；错误交由健康检查兜底提示。
		_ = app.Engine.Run(EmbeddedServerAddr)
	}()

	if err := waitHTTPReady(EmbeddedServerAddr, embeddedServerStartTimeout); err != nil {
		return nil, fmt.Errorf("embedded server health check failed: %w", err)
	}

	return &Sidecar{Name: "server", BaseURL: baseURL}, nil
}

// applyEmbeddedServerEnv 注入桌面端运行时环境变量。
func applyEmbeddedServerEnv(env DesktopRuntimeEnv) {
	os.Setenv("PTNEXUS_RUNTIME", "desktop")
	os.Setenv("SERVER_HOST", "127.0.0.1")
	os.Setenv("SERVER_PORT", "5275")

	setEnvIfEmpty("PTNEXUS_BASE_DIR", env.ResourceDir)
	setEnvIfEmpty("PTNEXUS_DATA_DIR", env.DataDir)
	setEnvIfEmpty("PTNEXUS_DB_CONFIG_FILE", env.DBConfigFile)
	setEnvIfEmpty("PTNEXUS_LOG_DIR", env.LogDir)
	setEnvIfEmpty("PTNEXUS_STATIC_DIR", env.StaticDir)
	setEnvIfEmpty("PTNEXUS_SITES_DATA_FILE", env.SitesDataFile)
	setEnvIfEmpty("PTNEXUS_GLOBAL_MAPPINGS", env.GlobalMapYML)

	isolateDatabaseEnv(env.DBConfigFile)
}

// isolateDatabaseEnv 在 database.json 有效时清除外部数据库环境变量。
//
// 原因：repository.NewStore 中 DB_TYPE / MYSQL_* 的优先级高于 database.json，
// 桌面端一旦继承到 shell 或 server/.env 的残留值（典型：DB_TYPE=mysql 指向内网实例），
// 就会连错库甚至启动阻塞到健康检查超时。桌面端语义明确：数据库只看 database.json。
func isolateDatabaseEnv(dbConfigFile string) {
	if !desktopDatabaseConfigReady(dbConfigFile) {
		return
	}
	for _, key := range []string{
		"DB_TYPE",
		"MYSQL_HOST", "MYSQL_PORT", "MYSQL_USER", "MYSQL_PASSWORD", "MYSQL_DATABASE",
		"POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DATABASE",
	} {
		_ = os.Unsetenv(key)
	}
}

// desktopDatabaseConfigReady 判断 database.json 是否已写明可用数据库类型。
func desktopDatabaseConfigReady(path string) bool {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return false
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return false
	}
	var cfg struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return false
	}
	return strings.TrimSpace(cfg.Type) != ""
}

// setEnvIfEmpty 仅在环境变量为空时写入，保留调用方显式设置的值。
func setEnvIfEmpty(key, value string) {
	if strings.TrimSpace(os.Getenv(key)) != "" {
		return
	}
	if strings.TrimSpace(value) == "" {
		return
	}
	_ = os.Setenv(key, value)
}
