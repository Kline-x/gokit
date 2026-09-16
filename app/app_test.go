package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeComponent 把每次 Start/Stop 记录到共享的 events 切片，用于断言调用顺序。
type fakeComponent struct {
	name      string
	startErr  error
	stopErr   error
	events    *[]string
	dependsOn []string
}

func (f *fakeComponent) Name() string { return f.name }

func (f *fakeComponent) Start(context.Context) error {
	*f.events = append(*f.events, "start:"+f.name)
	return f.startErr
}

func (f *fakeComponent) Stop(context.Context) error {
	*f.events = append(*f.events, "stop:"+f.name)
	return f.stopErr
}

// healthyComponent 在 fakeComponent 之上实现 HealthChecker，用于验证 App.Health。
type healthyComponent struct {
	fakeComponent
	healthErr error
}

func (h *healthyComponent) Health(context.Context) error { return h.healthErr }

func TestStartInRegistrationOrderAndStopInReverse(t *testing.T) {
	var events []string
	a := New()
	a.Register(
		&fakeComponent{name: "a", events: &events},
		&fakeComponent{name: "b", events: &events},
		&fakeComponent{name: "c", events: &events},
	)

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}

	want := []string{"start:a", "start:b", "start:c", "stop:c", "stop:b", "stop:a"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	var events []string
	a := New()
	a.Register(&fakeComponent{name: "a", events: &events})

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}

	want := []string{"start:a", "stop:a"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v（第二次 Stop 不应重复调用组件）", events, want)
	}
}

// stopTriggeringComponent 在自己 Start 期间触发一次 Stop，
// 用来确定性地复现「Start 与 Stop 交错」这一时序。
type stopTriggeringComponent struct {
	name   string
	events *[]string
	app    *App
}

func (s *stopTriggeringComponent) Name() string { return s.name }

func (s *stopTriggeringComponent) Start(ctx context.Context) error {
	*s.events = append(*s.events, "start:"+s.name)
	// 模拟启动尚未走完时，另一条路径（例如信号处理）调用了 Stop。
	return s.app.Stop(ctx)
}

func (s *stopTriggeringComponent) Stop(context.Context) error {
	*s.events = append(*s.events, "stop:"+s.name)
	return nil
}

func TestStartAbortsWhenStoppedConcurrently(t *testing.T) {
	var events []string
	a := New()
	a.Register(
		&fakeComponent{name: "a", events: &events},
		&stopTriggeringComponent{name: "b", events: &events, app: a},
		&fakeComponent{name: "c", events: &events},
	)

	err := a.Start(context.Background())
	if !errors.Is(err, errStoppedDuringStart) {
		t.Fatalf("Start() error = %v, want errStoppedDuringStart", err)
	}

	// b 启动后必须被回收，c 不应再被启动。
	// 注意这里不是严格逆序：b 是由 Start 自己回收的，而 a 是并发的 Stop 停的。
	// 「严格逆序」只在没有 Start/Stop 并发交错时成立，这是该契约的已知例外。
	want := []string{"start:a", "start:b", "stop:a", "stop:b"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
}

func TestRegisterRejectsDuplicateNames(t *testing.T) {
	var events []string
	a := New()
	a.Register(
		&fakeComponent{name: "dup", events: &events},
		&fakeComponent{name: "dup", events: &events},
	)

	err := a.Start(context.Background())
	if err == nil {
		t.Fatal("Start() error = nil, want 重名错误")
	}
}

// blockingComponent 的 Stop 会一直阻塞到 ctx 结束，用来验证停止超时。
type blockingComponent struct{ name string }

func (b *blockingComponent) Name() string                { return b.name }
func (b *blockingComponent) Start(context.Context) error { return nil }
func (b *blockingComponent) Stop(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestStartRollsBackAlreadyStartedComponents(t *testing.T) {
	var events []string
	boom := errors.New("boom")
	a := New()
	a.Register(
		&fakeComponent{name: "a", events: &events},
		&fakeComponent{name: "b", events: &events, startErr: boom},
		&fakeComponent{name: "c", events: &events},
	)

	err := a.Start(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("Start() error = %v, want 包装了 boom 的错误", err)
	}

	// b 自身启动失败，不应被 Stop；c 从未启动，也不应被 Stop。
	want := []string{"start:a", "start:b", "stop:a"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
}

func TestStopAppliesStopTimeoutWhenContextHasNoDeadline(t *testing.T) {
	a := New(WithStopTimeout(50 * time.Millisecond))
	a.Register(&blockingComponent{name: "blocker"})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	begin := time.Now()
	err := a.Stop(context.Background())
	elapsed := time.Since(begin)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Errorf("Stop() 耗时 %v，应在 stopTimeout 附近返回", elapsed)
	}
}

func TestHealthAggregatesUnhealthyComponents(t *testing.T) {
	var events []string
	boom := errors.New("挂了")
	a := New(WithSignals())
	a.Register(
		&fakeComponent{name: "plain", events: &events},
		&healthyComponent{fakeComponent: fakeComponent{name: "ok", events: &events}},
		&healthyComponent{fakeComponent: fakeComponent{name: "bad", events: &events}, healthErr: boom},
	)

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(ctx) })

	err := a.Health(ctx)
	if !errors.Is(err, boom) {
		t.Fatalf("Health() error = %v, want 包含 boom", err)
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("错误信息 = %q, 应指出是 bad 组件", err.Error())
	}
	if strings.Contains(err.Error(), "plain") {
		t.Errorf("错误信息 = %q, 未实现 HealthChecker 的组件不该出现", err.Error())
	}
}

func TestHealthReturnsNilWhenAllHealthy(t *testing.T) {
	var events []string
	a := New(WithSignals())
	a.Register(&healthyComponent{fakeComponent: fakeComponent{name: "ok", events: &events}})

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(ctx) })

	if err := a.Health(ctx); err != nil {
		t.Errorf("Health() error = %v, want nil", err)
	}
}

func TestDoneDeliversFatalError(t *testing.T) {
	boom := errors.New("运行期崩了")
	a := New(WithSignals())
	a.Fatal(boom)

	select {
	case err := <-a.Done():
		if !errors.Is(err, boom) {
			t.Fatalf("Done() 收到 %v, want boom", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Done() 没有送出 Fatal 上报的错误")
	}
}

func TestStartRejectsSecondCall(t *testing.T) {
	var events []string
	a := New(WithSignals())
	a.Register(&fakeComponent{name: "a", events: &events})

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(ctx) })

	if err := a.Start(ctx); err == nil {
		t.Fatal("second Start() error = nil, want 重复启动错误")
	}
	if len(events) != 1 {
		t.Errorf("events = %v, 第二次 Start 不应再启动任何组件", events)
	}
}

func TestRegisterAfterStartPanics(t *testing.T) {
	var events []string
	a := New(WithSignals())

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(ctx) })

	defer func() {
		if recover() == nil {
			t.Error("Start 之后 Register 应当 panic")
		}
	}()
	a.Register(&fakeComponent{name: "late", events: &events})
}
