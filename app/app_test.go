package app

import (
	"context"
	"slices"
	"testing"
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
