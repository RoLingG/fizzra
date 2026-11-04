package store

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/rolingg/fizzra/backend/internal/config"
)

// DB 包住 GORM 的句柄，调用方因此依赖我们自己的类型而不是直接依赖 GORM
// 日后更换存储引擎只需改这个包和各个仓储
type DB struct {
	*gorm.DB
	log *slog.Logger
}

// Open 连接配置好的数据库并执行迁移
func Open(cfg *config.Config, log *slog.Logger) (*DB, error) {
	dsn := cfg.Database.DSN
	if dsn != ":memory:" && !isURI(dsn) {
		dir := filepath.Dir(dsn)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create database directory %s: %w", dir, err)
		}
	}

	// 调试模式以下把 GORM 自己的日志静音，「record not found」在这里是预期结果
	logLevel := gormlogger.Silent
	if cfg.Server.Mode == "debug" {
		logLevel = gormlogger.Warn
	}

	gdb, err := gorm.Open(sqlite.Open(sqliteDSN(dsn)), &gorm.Config{
		Logger: gormlogger.Default.LogMode(logLevel),

		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if dsn != ":memory:" {
		if err := applySQLitePragmas(gdb); err != nil {
			return nil, err
		}
	}

	return &DB{DB: gdb, log: log}, nil
}

// isURI 报告该 DSN 是否为完整的连接 URI（例如 "file::memory:"）
func isURI(dsn string) bool {
	return len(dsn) > 5 && dsn[:5] == "file:"
}

// sqliteDSN 加上我们希望在每条连接上生效的 pragma
func sqliteDSN(dsn string) string {
	if isURI(dsn) {
		return dsn
	}
	// _busy_timeout 避免接口和任务 worker 并发写入时出现 "database is locked"，
	// WAL 通过 PRAGMA 单独设置
	return dsn + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
}

func applySQLitePragmas(db *gorm.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	}
	for _, p := range pragmas {
		if err := db.Exec(p).Error; err != nil {
			return fmt.Errorf("apply %q: %w", p, err)
		}
	}
	return nil
}

// Close 释放底层的连接池
func (d *DB) Close() error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
