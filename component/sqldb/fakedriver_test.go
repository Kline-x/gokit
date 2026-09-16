package sqldb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

var driverSeq atomic.Int64

// fakeDriver 是测试专用的 database/sql 驱动，记录打开次数与事务的提交、回滚次数。
type fakeDriver struct {
	mu        sync.Mutex
	opened    int
	openErr   error
	pingErr   error
	commits   atomic.Int32
	rollbacks atomic.Int32
}

func (d *fakeDriver) Open(string) (driver.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.openErr != nil {
		return nil, d.openErr
	}
	d.opened++
	return &fakeConn{drv: d}, nil
}

func (d *fakeDriver) setPingErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pingErr = err
}

func (d *fakeDriver) currentPingErr() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pingErr
}

type fakeConn struct{ drv *fakeDriver }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return &fakeStmt{}, nil }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return &fakeTx{drv: c.drv}, nil }
func (c *fakeConn) Ping(context.Context) error          { return c.drv.currentPingErr() }

type fakeTx struct{ drv *fakeDriver }

func (t *fakeTx) Commit() error {
	t.drv.commits.Add(1)
	return nil
}

func (t *fakeTx) Rollback() error {
	t.drv.rollbacks.Add(1)
	return nil
}

type fakeStmt struct{}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }
func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) { return &fakeRows{}, nil }

type fakeRows struct{ done bool }

func (r *fakeRows) Columns() []string { return []string{"n"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = int64(1)
	return nil
}

// registerFakeDriver 注册一个独占的假驱动，返回驱动名与驱动实例。
// 每次调用用不同的名字，避免 sql.Register 重名 panic。
func registerFakeDriver(t *testing.T) (string, *fakeDriver) {
	t.Helper()
	drv := &fakeDriver{}
	name := fmt.Sprintf("gokit-fake-%d", driverSeq.Add(1))
	sql.Register(name, drv)
	return name, drv
}
