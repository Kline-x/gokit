package infrastructure

import (
	"context"

	"github.com/Kline-x/gokit/component/sqldb"
)

// Migrator 把建表这件事变成一个受 App 托管的组件。
//
// 它声明依赖数据库组件，因此 App 会保证数据库先启动、探活通过之后
// 才轮到它建表；停止时它也会先于数据库被停掉。示例用，
// 真实项目应当走正式的迁移工具。
type Migrator struct {
	db *sqldb.DB
}

// NewMigrator 构造迁移组件。
func NewMigrator(db *sqldb.DB) *Migrator {
	return &Migrator{db: db}
}

// Name 实现 app.Component。
func (m *Migrator) Name() string { return "greeter.migrator" }

// DependsOn 实现 app.Dependent：建表必须发生在数据库可用之后。
// 这里取数据库组件自己配置的名字，而不是写死字符串。
func (m *Migrator) DependsOn() []string { return []string{m.db.Name()} }

// Start 实现 app.Component，建表。
func (m *Migrator) Start(ctx context.Context) error { return Migrate(ctx, m.db) }

// Stop 实现 app.Component。迁移没有需要释放的东西。
func (m *Migrator) Stop(context.Context) error { return nil }
