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
