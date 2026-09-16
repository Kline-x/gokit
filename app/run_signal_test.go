//go:build !windows

package app

import (
	"context"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"testing"
	"time"
)

// TestRunStopsOnSignal 验证 Run 收到订阅的信号后会优雅退出。
//
// 用 SIGUSR1 而不是 SIGINT/SIGTERM：后两者若未被订阅会直接终止测试进程。
// SIGUSR1 在 Windows 上不存在，故本文件带 !windows 构建标签。
func TestRunStopsOnSignal(t *testing.T) {
	// 先在测试进程里订阅 SIGUSR1，把它的默认处置（终止进程）换成投递到通道。
	// 这样即使信号早于 Run 完成订阅到达，也只会落进这里被丢弃，不会打死测试进程。
	//
	// 这个订阅刻意不注销：os/signal 按信号做引用计数，一旦计数归零就会恢复
	// 致命默认处置。若在收尾时注销，循环里可能还有一个在途的 SIGUSR1，
	// 正好赶上默认处置恢复而打死整个测试进程。
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGUSR1)

	var events []string
	a := New(WithSignals(syscall.SIGUSR1))
	a.Register(&fakeComponent{name: "a", events: &events})

	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background()) }()

	// 无法确知 Run 何时完成 signal.Notify，与其靠固定等待赌时序，
	// 不如反复发信号直到它真正订阅上并退出。
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	for {
		if err := syscall.Kill(syscall.Getpid(), syscall.SIGUSR1); err != nil {
			t.Fatalf("发送 SIGUSR1 失败: %v", err)
		}

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Run() error = %v, want nil", err)
			}
			want := []string{"start:a", "stop:a"}
			if !slices.Equal(events, want) {
				t.Errorf("events = %v, want %v", events, want)
			}
			return
		case <-tick.C:
		case <-deadline:
			t.Fatal("Run() 未在收到信号后返回")
		}
	}
}
