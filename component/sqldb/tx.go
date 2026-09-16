package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Executor 是 *sql.DB 与 *sql.Tx 的公共子集。
// 仓储实现依赖它而不是具体类型，从而在事务内外都能工作。
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type txKey struct{}

// TxFromContext 取出 ctx 中正在进行的事务。
func TxFromContext(ctx context.Context) (*sql.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(*sql.Tx)
	return tx, ok && tx != nil
}

// Executor 返回当前上下文应当使用的执行器：
// 处于事务中时返回该事务，否则返回连接池。
func (d *DB) Executor(ctx context.Context) Executor {
	if tx, ok := TxFromContext(ctx); ok {
		return tx
	}
	return d.DB
}

// Tx 在事务中执行 fn：fn 返回 nil 则提交，返回错误则回滚并原样返回该错误。
//
// 若 ctx 中已有事务，则直接复用，不再嵌套开启，
// 因此 application 层可以放心地在一个事务里组合多个用例。
// fn 内发生 panic 时先回滚再把 panic 继续向上抛。
func (d *DB) Tx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := TxFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqldb: 开启事务失败: %w", err)
	}

	// finished 表示事务已由正常路径终结（已提交或已回滚），
	// 此时 defer 无需再做任何事；只有 panic 路径会让它保持 false。
	finished := false
	defer func() {
		if finished {
			return
		}
		if rec := recover(); rec != nil {
			_ = tx.Rollback()
			panic(rec)
		}
	}()

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		finished = true
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return errors.Join(err, fmt.Errorf("sqldb: 回滚失败: %w", rbErr))
		}
		return err
	}

	finished = true
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqldb: 提交失败: %w", err)
	}
	return nil
}
