//go:build !windows

package app

import (
	"context"
	"slices"
	"syscall"
	"testing"
	"time"
)

// TestRunStopsOnSignal 验证 Run 收到订阅的信号后会优雅退出。
//
// 用 SIGUSR1 而不是 SIGINT/SIGTERM：后两者在测试进程里未被订阅时会直接
// 终止测试运行。SIGUSR1 在 Windows 上不存在，故本文件带 !windows 构建标签。
func TestRunStopsOnSignal(t *testing.T) {
	var events []string
	a := New(WithSignals(syscall.SIGUSR1))
	a.Register(&fakeComponent{name: "a", events: &events})

	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background()) }()

	// 等 Run 完成 Start 并真正订阅信号，否则信号会落空。
	waitFor(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return len(a.started) == 1
	})
	// signal.Notify 的订阅发生在 Start 之后，这里再让出一下调度。
	time.Sleep(50 * time.Millisecond)

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatalf("发送 SIGUSR1 失败: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() 未在收到信号后返回")
	}

	want := []string{"start:a", "stop:a"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
}

// waitFor 轮询等待条件成立，最多等 2 秒。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}
