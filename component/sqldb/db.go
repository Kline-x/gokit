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
//
// 数值字段一律遵循同一个约定：0 表示「采用默认值」，-1 表示「显式采用
// database/sql 的零值语义」。之所以需要这个哨兵，是因为配置结构体只能用
// 值类型字段，YAML 里「没写这个字段」和「写了 0」无法区分。
type Config struct {
	// Name 是组件在 App 中的唯一标识。留空时取默认值。
	// 同一个 App 里注册多个同类组件时，必须给出互不相同的名字。
	Name string `yaml:"name"`
	// Driver 是已注册的 database/sql 驱动名，例如 mysql、pgx、sqlite。
	Driver string `yaml:"driver"`
	// DSN 是驱动自己的连接串。
	DSN string `yaml:"dsn"`
	// MaxOpenConns 是连接池上限。0 表示使用默认值，-1 表示不限连接数。
	MaxOpenConns int `yaml:"max_open_conns"`
	// MaxIdleConns 是空闲连接上限。0 表示使用默认值，-1 表示不保留空闲连接。
	MaxIdleConns int `yaml:"max_idle_conns"`
	// ConnMaxLifetime 是单条连接的最长存活时间。0 表示使用默认值，-1 表示永不过期。
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
	// ConnMaxIdleTime 是单条连接的最长空闲时间。0 表示使用默认值，-1 表示不因空闲而回收。
	ConnMaxIdleTime time.Duration `yaml:"conn_max_idle_time"`
	// PingTimeout 是 Start 阶段探活的超时。0 表示使用默认值，-1 表示不设超时。
	PingTimeout time.Duration `yaml:"ping_timeout"`
}

// DefaultConfig 返回一组保守的默认值。
func DefaultConfig() Config {
	return Config{
		Name:            "sqldb",
		MaxOpenConns:    50,
		MaxIdleConns:    10,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: 10 * time.Minute,
		PingTimeout:     5 * time.Second,
	}
}

// resolveInt 把配置里的哨兵值翻译成 database/sql 需要的实际值：
// 0 取默认值，-1 取 database/sql 的零值语义，其余原样返回。
func resolveInt(v, def int) int {
	switch {
	case v == 0:
		return def
	case v < 0:
		return 0
	default:
		return v
	}
}

// resolveDuration 与 resolveInt 同理，用于时长字段。
func resolveDuration(v, def time.Duration) time.Duration {
	switch {
	case v == 0:
		return def
	case v < 0:
		return 0
	default:
		return v
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.Name == "" {
		c.Name = d.Name
	}
	c.MaxOpenConns = resolveInt(c.MaxOpenConns, d.MaxOpenConns)
	c.MaxIdleConns = resolveInt(c.MaxIdleConns, d.MaxIdleConns)
	c.ConnMaxLifetime = resolveDuration(c.ConnMaxLifetime, d.ConnMaxLifetime)
	c.ConnMaxIdleTime = resolveDuration(c.ConnMaxIdleTime, d.ConnMaxIdleTime)
	c.PingTimeout = resolveDuration(c.PingTimeout, d.PingTimeout)
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
		return nil, fmt.Errorf("sqldb: %s 打开驱动 %s 失败: %w", cfg.Name, cfg.Driver, err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	return &DB{DB: db, cfg: cfg}, nil
}

// Name 实现 app.Component。
func (d *DB) Name() string { return d.cfg.Name }

// Start 实现 app.Component，通过一次 Ping 确认连接可用。
func (d *DB) Start(ctx context.Context) error {
	return d.Health(ctx)
}

// Stop 实现 app.Component，关闭连接池。
func (d *DB) Stop(context.Context) error {
	if err := d.DB.Close(); err != nil {
		return fmt.Errorf("sqldb: %s 关闭连接池失败: %w", d.cfg.Name, err)
	}
	return nil
}

// Health 实现 app.HealthChecker。
func (d *DB) Health(ctx context.Context) error {
	if d.cfg.PingTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.cfg.PingTimeout)
		defer cancel()
	}
	if err := d.DB.PingContext(ctx); err != nil {
		return fmt.Errorf("sqldb: %s 探活失败: %w", d.cfg.Name, err)
	}
	return nil
}
