package repository

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pt-nexus/server/internal/config"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Store struct {
	DB     *gorm.DB
	DBType string
}

// DatabaseTimeZone 是所有数据库连接统一使用的会话时区（东八区）。
// 参数/返回：无参数；返回固定为 Asia/Shanghai 的位置对象。
// 失败场景：时区数据缺失时退回固定 UTC+8 偏移，保证时间仍是北京时间。
// 副作用：无。
// 说明：队列时间字段以 "2006-01-02 15:04:05" 文本形式落库，不带时区信息，
// 若各驱动解析出的会话时区不一致（例如容器内 /etc/localtime 为 Etc/UTC 时
// MySQL 的 loc=Local 会解析成 UTC），同一列就会出现两种时刻的混用。
// 统一钉死为东八区后，写入与读出都按北京时间解释。
var DatabaseTimeZone = resolveDatabaseTimeZone()

// resolveDatabaseTimeZone 解析数据库会话时区，优先 Asia/Shanghai，失败时退回固定 UTC+8。
// 参数/返回：无参数；返回 time.Location 指针（非 nil）。
// 失败场景：运行环境缺少 tzdata 时无法加载 Asia/Shanghai，此时退回 FixedZone("CST", 8*3600)。
// 副作用：无。
func resolveDatabaseTimeZone() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil && loc != nil {
		return loc
	}
	return time.FixedZone("CST", 8*3600)
}

func NewStore(paths config.RuntimePaths) (*Store, error) {
	dbType := strings.ToLower(strings.TrimSpace(os.Getenv("DB_TYPE")))
	desktopCfg := config.DatabaseConfig{}
	if dbType == "" {
		loadedCfg, found, loadErr := config.LoadDesktopDatabaseConfig(paths)
		if loadErr != nil {
			return nil, loadErr
		}
		if found {
			desktopCfg = loadedCfg
			if loadedCfg.Type != "" {
				dbType = loadedCfg.Type
			}
		}
	}
	if dbType == "" {
		dbType = "sqlite"
	}

	var (
		db  *gorm.DB
		err error
	)

	switch dbType {
	case "mysql":
		host := firstNonEmpty(os.Getenv("MYSQL_HOST"), desktopCfg.MySQL.Host)
		user := firstNonEmpty(os.Getenv("MYSQL_USER"), desktopCfg.MySQL.User)
		password := firstNonEmpty(os.Getenv("MYSQL_PASSWORD"), desktopCfg.MySQL.Password)
		database := firstNonEmpty(os.Getenv("MYSQL_DATABASE"), desktopCfg.MySQL.Database)
		port := firstNonEmpty(os.Getenv("MYSQL_PORT"), intToString(desktopCfg.MySQL.Port), "3306")
		// loc 必须显式写成 URL 转义后的时区名，不能用 loc=Local：
		// 容器内 /etc/localtime 常常是 Etc/UTC（宿主机时区由 TZ 环境变量提供），
		// 此时 Local 会解析成 UTC，导致读出的时间比实际早 8 小时。
		dsn := fmt.Sprintf(
			"%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=%s",
			user, password, host, port, database, url.QueryEscape("Asia/Shanghai"),
		)
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	case "postgresql":
		host := firstNonEmpty(os.Getenv("POSTGRES_HOST"), desktopCfg.PostgreSQL.Host)
		user := firstNonEmpty(os.Getenv("POSTGRES_USER"), desktopCfg.PostgreSQL.User)
		password := firstNonEmpty(os.Getenv("POSTGRES_PASSWORD"), desktopCfg.PostgreSQL.Password)
		database := firstNonEmpty(os.Getenv("POSTGRES_DATABASE"), desktopCfg.PostgreSQL.Database)
		port := firstNonEmpty(os.Getenv("POSTGRES_PORT"), intToString(desktopCfg.PostgreSQL.Port), "5432")
		sslMode := firstNonEmpty(os.Getenv("POSTGRES_SSLMODE"), desktopCfg.PostgreSQL.SSLMode, "disable")
		// TimeZone 与 MySQL 的 loc 保持同一个时区（东八区），避免两种驱动下同一列解释不一致。
		dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s TimeZone=%s", host, user, password, database, port, sslMode, DatabaseTimeZone.String())
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	case "sqlite":
		fallthrough
	default:
		dbType = "sqlite"
		dbPath := filepath.Join(paths.DataDir, "pt_stats.db")
		if value := strings.TrimSpace(os.Getenv("SQLITE_PATH")); value != "" {
			dbPath = value
		} else if value := strings.TrimSpace(desktopCfg.SQLitePath); value != "" {
			dbPath = value
		}
		// SQLite DSN：开启 WAL 模式（读写不互斥）+ busy_timeout 5s（锁冲突时等待而非立即失败）+ synchronous=NORMAL（WAL 下安全且更快）
		dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
		db, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	}

	if err != nil {
		return nil, fmt.Errorf("open database failed: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB failed: %w", err)
	}
	if dbType == "sqlite" {
		// SQLite 只支持单写入者，MaxOpenConns=1 序列化所有 DB 访问，
		// 避免连接池复用已在事务中的连接导致 "cannot start a transaction within a transaction"
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	} else {
		sqlDB.SetMaxOpenConns(20)
		sqlDB.SetMaxIdleConns(10)
	}

	return &Store{DB: db, DBType: dbType}, nil
}

func (s *Store) GroupColumn() string {
	if s.DBType == "postgresql" {
		return `"group"`
	}
	return "`group`"
}

func getEnvOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func intToString(value int) string {
	if value <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", value)
}
