package app

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func (f *fakeComponent) DependsOn() []string { return f.dependsOn }

func names(cs []Component) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name())
	}
	return out
}

func TestSortPutsDependenciesFirst(t *testing.T) {
	var events []string
	// 注册顺序故意反着写：server 依赖 db，db 依赖 log。
	in := []Component{
		&fakeComponent{name: "server", events: &events, dependsOn: []string{"db"}},
		&fakeComponent{name: "db", events: &events, dependsOn: []string{"log"}},
		&fakeComponent{name: "log", events: &events},
	}

	got, err := sortComponents(in)
	if err != nil {
		t.Fatalf("sortComponents() error = %v", err)
	}

	want := []string{"log", "db", "server"}
	if !slices.Equal(names(got), want) {
		t.Errorf("顺序 = %v, want %v", names(got), want)
	}
}

func TestSortKeepsRegistrationOrderAmongIndependentComponents(t *testing.T) {
	var events []string
	in := []Component{
		&fakeComponent{name: "a", events: &events},
		&fakeComponent{name: "b", events: &events},
		&fakeComponent{name: "c", events: &events},
	}

	got, err := sortComponents(in)
	if err != nil {
		t.Fatalf("sortComponents() error = %v", err)
	}
	want := []string{"a", "b", "c"}
	if !slices.Equal(names(got), want) {
		t.Errorf("顺序 = %v, want %v", names(got), want)
	}
}

func TestSortRejectsUnknownDependency(t *testing.T) {
	var events []string
	in := []Component{
		&fakeComponent{name: "a", events: &events, dependsOn: []string{"nope"}},
	}

	_, err := sortComponents(in)
	if err == nil {
		t.Fatal("sortComponents() error = nil, want 未知依赖错误")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("错误信息 = %q, 应指出未知依赖 nope", err.Error())
	}
}

func TestSortRejectsCycle(t *testing.T) {
	var events []string
	in := []Component{
		&fakeComponent{name: "a", events: &events, dependsOn: []string{"b"}},
		&fakeComponent{name: "b", events: &events, dependsOn: []string{"a"}},
	}

	_, err := sortComponents(in)
	if err == nil {
		t.Fatal("sortComponents() error = nil, want 循环依赖错误")
	}
}

func TestAppStartFollowsDependencyOrder(t *testing.T) {
	var events []string
	a := New()
	a.Register(
		&fakeComponent{name: "server", events: &events, dependsOn: []string{"db"}},
		&fakeComponent{name: "db", events: &events},
	)

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	want := []string{"start:db", "start:server", "stop:server", "stop:db"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
}
