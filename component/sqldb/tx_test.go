package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func newFakeDB(t *testing.T) (*DB, *fakeDriver) {
	t.Helper()
	name, drv := registerFakeDriver(t)
	db, err := New(Config{Driver: name, DSN: "fake"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Stop(context.Background()) })
	return db, drv
}

func TestTxCommitsWhenFuncSucceeds(t *testing.T) {
	db, drv := newFakeDB(t)

	err := db.Tx(context.Background(), func(ctx context.Context) error {
		if _, ok := TxFromContext(ctx); !ok {
			t.Error("事务未放进 ctx")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Tx() error = %v", err)
	}

	if got := drv.commits.Load(); got != 1 {
		t.Errorf("提交次数 = %d, want 1", got)
	}
	if got := drv.rollbacks.Load(); got != 0 {
		t.Errorf("回滚次数 = %d, want 0", got)
	}
}

func TestTxRollsBackAndReturnsOriginalError(t *testing.T) {
	db, drv := newFakeDB(t)
	boom := errors.New("业务失败")

	err := db.Tx(context.Background(), func(context.Context) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("Tx() error = %v, want %v", err, boom)
	}
	if got := drv.rollbacks.Load(); got != 1 {
		t.Errorf("回滚次数 = %d, want 1", got)
	}
	if got := drv.commits.Load(); got != 0 {
		t.Errorf("提交次数 = %d, want 0", got)
	}
}

func TestTxRollsBackOnPanicAndRepanics(t *testing.T) {
	db, drv := newFakeDB(t)

	defer func() {
		if rec := recover(); rec == nil {
			t.Error("panic 未向上传播")
		}
		if got := drv.rollbacks.Load(); got != 1 {
			t.Errorf("回滚次数 = %d, want 1", got)
		}
	}()

	_ = db.Tx(context.Background(), func(context.Context) error { panic("炸了") })
}

func TestNestedTxReusesOuterTransaction(t *testing.T) {
	db, drv := newFakeDB(t)

	err := db.Tx(context.Background(), func(outer context.Context) error {
		outerTx, _ := TxFromContext(outer)
		return db.Tx(outer, func(inner context.Context) error {
			innerTx, _ := TxFromContext(inner)
			if innerTx != outerTx {
				t.Error("内层 Tx 另起了事务，应复用外层事务")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("Tx() error = %v", err)
	}
	if got := drv.commits.Load(); got != 1 {
		t.Errorf("提交次数 = %d, want 1（嵌套调用只应提交一次）", got)
	}
}

func TestExecutorSwitchesBetweenTxAndPool(t *testing.T) {
	db, _ := newFakeDB(t)

	ctx := context.Background()
	if got := db.Executor(ctx); got != db.DB {
		t.Error("事务外 Executor 应返回连接池本身")
	}

	err := db.Tx(ctx, func(inner context.Context) error {
		tx, _ := TxFromContext(inner)
		if got := db.Executor(inner); got != tx {
			t.Error("事务内 Executor 应返回当前事务")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Tx() error = %v", err)
	}
}

func TestTxKeepsOriginalErrorWhenRollbackAlsoFails(t *testing.T) {
	db, drv := newFakeDB(t)
	bizErr := errors.New("业务失败")
	rbErr := errors.New("回滚也失败")
	drv.setRollbackErr(rbErr)

	err := db.Tx(context.Background(), func(context.Context) error { return bizErr })

	if !errors.Is(err, bizErr) {
		t.Errorf("Tx() error 丢了原始业务错误: %v", err)
	}
	if !errors.Is(err, rbErr) {
		t.Errorf("Tx() error 丢了回滚错误: %v", err)
	}
	if got := drv.rollbacks.Load(); got != 1 {
		t.Errorf("回滚次数 = %d, want 1", got)
	}
}

func TestTxToleratesErrTxDoneOnRollback(t *testing.T) {
	db, drv := newFakeDB(t)
	bizErr := errors.New("业务失败")
	// 驱动可能已经自行结束了事务，这时回滚会返回 sql.ErrTxDone，
	// 它不该盖过真正的业务错误，也不该被当成新的失败报出来。
	drv.setRollbackErr(sql.ErrTxDone)

	err := db.Tx(context.Background(), func(context.Context) error { return bizErr })

	if !errors.Is(err, bizErr) {
		t.Fatalf("Tx() error = %v, want 原始业务错误", err)
	}
	if errors.Is(err, sql.ErrTxDone) {
		t.Errorf("Tx() error 里混进了 sql.ErrTxDone: %v", err)
	}
}

func TestTxReportsCommitFailure(t *testing.T) {
	db, drv := newFakeDB(t)
	commitErr := errors.New("提交失败")
	drv.setCommitErr(commitErr)

	err := db.Tx(context.Background(), func(context.Context) error { return nil })

	if !errors.Is(err, commitErr) {
		t.Fatalf("Tx() error = %v, want 包含提交错误", err)
	}
	if got := drv.commits.Load(); got != 1 {
		t.Errorf("提交次数 = %d, want 1", got)
	}
}
