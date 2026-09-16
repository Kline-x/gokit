// Package sqldb 提供基于标准库 database/sql 的关系库组件。
//
// 本包不 import 任何数据库驱动：使用方按需 blank import，
// 例如 _ "github.com/go-sql-driver/mysql"，再把驱动名写进 Config.Driver。
package sqldb

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Config 是关系库组件的配置。
type Config struct {
	// Driver 是已注册的 database/sql 驱动名，例如 mysql、pgx、sqlite。
	Driver string `yaml:"driver"`
	// DSN 是驱动自己的连接串。
	DSN string `yaml:"dsn"`
	// MaxOpenConns 是连接池上限。
	MaxOpenConns int `yaml:"max_open_conns"`
	// MaxIdleConns 是空闲连接上限。
	MaxIdleConns int `yaml:"max_idle_conns"`
	// ConnMaxLifetime 是单条连接的最长存活时间。
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
	// ConnMaxIdleTime 是单条连接的最长空闲时间。
	ConnMaxIdleTime time.Duration `yaml:"conn_max_idle_time"`
	// PingTimeout 是 Start 阶段探活的超时。
	PingTimeout time.Duration `yaml:"ping_timeout"`
}

// DefaultConfig 返回一组保守的默认值。
func DefaultConfig() Config {
	return Config{
		MaxOpenConns:    50,
		MaxIdleConns:    10,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: 10 * time.Minute,
		PingTimeout:     5 * time.Second,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.MaxOpenConns == 0 {
		c.MaxOpenConns = d.MaxOpenConns
	}
	if c.MaxIdleConns == 0 {
		c.MaxIdleConns = d.MaxIdleConns
	}
	if c.ConnMaxLifetime == 0 {
		c.ConnMaxLifetime = d.ConnMaxLifetime
	}
	if c.ConnMaxIdleTime == 0 {
		c.ConnMaxIdleTime = d.ConnMaxIdleTime
	}
	if c.PingTimeout == 0 {
		c.PingTimeout = d.PingTimeout
	}
	return c
}

// DB 是实现了 app.Component 方法集的关系库句柄。
// 内嵌 *sql.DB，仓储实现可以直接使用标准库的全部方法。
type DB struct {
	*sql.DB
	cfg Config
}

// New 打开连接池。此时并不真正建连，探活发生在 Start。
func New(cfg Config) (*DB, error) {
	cfg = cfg.withDefaults()

	db, err := sql.Open(cfg.Driver, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("sqldb: 打开驱动 %s 失败: %w", cfg.Driver, err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	return &DB{DB: db, cfg: cfg}, nil
}

// Name 实现 app.Component。
func (d *DB) Name() string { return "sqldb" }

// Start 实现 app.Component，通过一次 Ping 确认连接可用。
func (d *DB) Start(ctx context.Context) error {
	if err := d.Health(ctx); err != nil {
		return fmt.Errorf("sqldb: 启动探活失败: %w", err)
	}
	return nil
}

// Stop 实现 app.Component，关闭连接池。
func (d *DB) Stop(context.Context) error {
	if err := d.DB.Close(); err != nil {
		return fmt.Errorf("sqldb: 关闭连接池失败: %w", err)
	}
	return nil
}

// Health 实现 app.HealthChecker。
func (d *DB) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, d.cfg.PingTimeout)
	defer cancel()
	return d.DB.PingContext(ctx)
}
