package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestRunReturnsWhenContextCancelled(t *testing.T) {
	var events []string
	a := New(WithSignals())
	a.Register(&fakeComponent{name: "a", events: &events})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() 未在 ctx 取消后返回")
	}

	want := []string{"start:a", "stop:a"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
}

func TestRunReturnsFatalError(t *testing.T) {
	var events []string
	boom := errors.New("组件运行期崩了")
	a := New(WithSignals())
	a.Register(&fakeComponent{name: "a", events: &events})

	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background()) }()

	time.Sleep(50 * time.Millisecond)
	a.Fatal(boom)

	select {
	case err := <-done:
		if !errors.Is(err, boom) {
			t.Fatalf("Run() error = %v, want 包含 boom", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() 未在 Fatal 后返回")
	}

	want := []string{"start:a", "stop:a"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v（Fatal 后仍应优雅停止）", events, want)
	}
}

func TestRunReturnsStartErrorWithoutBlocking(t *testing.T) {
	var events []string
	boom := errors.New("启动失败")
	a := New(WithSignals())
	a.Register(&fakeComponent{name: "a", events: &events, startErr: boom})

	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background()) }()

	select {
	case err := <-done:
		if !errors.Is(err, boom) {
			t.Fatalf("Run() error = %v, want 包含 boom", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() 在启动失败后应立即返回")
	}
}

func TestFatalDoesNotBlockWhenCalledTwice(t *testing.T) {
	a := New(WithSignals())
	a.Fatal(errors.New("第一个"))

	done := make(chan struct{})
	go func() {
		a.Fatal(errors.New("第二个"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("第二次 Fatal 阻塞了，应当直接丢弃")
	}
}
