package sqldb

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStartPingsAndStopCloses(t *testing.T) {
	name, _ := registerFakeDriver(t)

	db, err := New(Config{Driver: name, DSN: "fake"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if db.Name() != "sqldb" {
		t.Errorf("Name() = %q, want %q", db.Name(), "sqldb")
	}

	ctx := context.Background()
	if err := db.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := db.Health(ctx); err != nil {
		t.Errorf("Health() error = %v", err)
	}
	if err := db.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := db.Health(ctx); err == nil {
		t.Error("Stop 之后 Health 仍然成功，连接未真正关闭")
	}
}

func TestStartFailsWhenPingFails(t *testing.T) {
	name, drv := registerFakeDriver(t)
	boom := errors.New("连不上")
	drv.setPingErr(boom)

	db, err := New(Config{Driver: name, DSN: "fake", PingTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Stop(context.Background()) })

	if err := db.Start(context.Background()); err == nil {
		t.Fatal("Start() error = nil, want Ping 失败错误")
	}
}

func TestNewFailsOnUnknownDriver(t *testing.T) {
	if _, err := New(Config{Driver: "no-such-driver", DSN: "x"}); err == nil {
		t.Fatal("New() error = nil, want 未知驱动错误")
	}
}

func TestNewAppliesPoolSettings(t *testing.T) {
	name, _ := registerFakeDriver(t)

	db, err := New(Config{Driver: name, DSN: "fake", MaxOpenConns: 7})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Stop(context.Background()) })

	if got := db.Stats().MaxOpenConnections; got != 7 {
		t.Errorf("MaxOpenConnections = %d, want 7", got)
	}
}

func TestSentinelNegativeOneMeansDatabaseSQLZero(t *testing.T) {
	name, _ := registerFakeDriver(t)

	db, err := New(Config{
		Driver:       name,
		DSN:          "fake",
		MaxOpenConns: -1,
		MaxIdleConns: -1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Stop(context.Background()) })

	if got := db.Stats().MaxOpenConnections; got != 0 {
		t.Errorf("MaxOpenConnections = %d, want 0（-1 应表示不限连接数）", got)
	}
}

func TestZeroKeepsDefaults(t *testing.T) {
	name, _ := registerFakeDriver(t)

	db, err := New(Config{Driver: name, DSN: "fake"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Stop(context.Background()) })

	want := DefaultConfig().MaxOpenConns
	if got := db.Stats().MaxOpenConnections; got != want {
		t.Errorf("MaxOpenConnections = %d, want %d（0 应表示使用默认值）", got, want)
	}
}

// TestTwoDBsCanCarryDifferentNames 证明同一进程里可以用不同的 Name
// 区分两个数据库实例（例如主库与只读副本），从而能在 App 中共存而不触发内核的重名校验。
func TestTwoDBsCanCarryDifferentNames(t *testing.T) {
	name1, _ := registerFakeDriver(t)
	name2, _ := registerFakeDriver(t)

	primary, err := New(Config{Name: "sqldb.primary", Driver: name1, DSN: "fake"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = primary.Stop(context.Background()) })

	replica, err := New(Config{Name: "sqldb.replica", Driver: name2, DSN: "fake"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = replica.Stop(context.Background()) })

	if primary.Name() == replica.Name() {
		t.Fatalf("两个实例的 Name 相同：%q", primary.Name())
	}
	if primary.Name() != "sqldb.primary" {
		t.Errorf("Name() = %q, want %q", primary.Name(), "sqldb.primary")
	}
}

func TestDefaultDBNameIsUsedWhenEmpty(t *testing.T) {
	name, _ := registerFakeDriver(t)
	db, err := New(Config{Driver: name, DSN: "fake"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Stop(context.Background()) })
	if db.Name() != "sqldb" {
		t.Errorf("Name() = %q, want %q", db.Name(), "sqldb")
	}
}

func TestNegativePingTimeoutDisablesDeadline(t *testing.T) {
	name, _ := registerFakeDriver(t)

	db, err := New(Config{Driver: name, DSN: "fake", PingTimeout: -1})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Stop(context.Background()) })

	// PingTimeout 解析为 0 时不应派生已过期的上下文，探活必须照常成功。
	if err := db.Health(context.Background()); err != nil {
		t.Errorf("Health() error = %v, want nil", err)
	}
}
