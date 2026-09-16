// Package infrastructure 是问候模块的基础设施层：把领域层的仓储契约
// 落到具体存储上。它可以依赖 gokit 组件，但不被领域层与应用层依赖。
package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Kline-x/gokit/component/sqldb"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/domain"
)

// GreetingRepo 用关系库实现 domain.Repository。
type GreetingRepo struct {
	db *sqldb.DB
}

// NewGreetingRepo 构造仓储。
func NewGreetingRepo(db *sqldb.DB) *GreetingRepo {
	return &GreetingRepo{db: db}
}

// Save 写入一条问候语。
//
// 这里取执行器用的是 db.Executor(ctx)：
// 处于事务中时自动落到当前事务，否则走连接池，同一份代码两种场景都适用。
func (r *GreetingRepo) Save(ctx context.Context, g domain.Greeting) error {
	_, err := r.db.Executor(ctx).ExecContext(ctx,
		`INSERT INTO greetings(name, text) VALUES(?, ?)`, g.Name, g.Text)
	if err != nil {
		return fmt.Errorf("写入 greetings 失败: %w", err)
	}
	return nil
}

// FindByName 按名字查询问候语，查不到时返回 domain.ErrNotFound。
func (r *GreetingRepo) FindByName(ctx context.Context, name string) (domain.Greeting, error) {
	row := r.db.Executor(ctx).QueryRowContext(ctx,
		`SELECT name, text FROM greetings WHERE name = ?`, name)

	var g domain.Greeting
	if err := row.Scan(&g.Name, &g.Text); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Greeting{}, domain.ErrNotFound
		}
		return domain.Greeting{}, fmt.Errorf("查询 greetings 失败: %w", err)
	}
	return g, nil
}

// Migrate 建表。示例用，真实项目应走迁移工具。
func Migrate(ctx context.Context, db *sqldb.DB) error {
	_, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS greetings (name TEXT PRIMARY KEY, text TEXT NOT NULL)`)
	if err != nil {
		return fmt.Errorf("建表失败: %w", err)
	}
	return nil
}
