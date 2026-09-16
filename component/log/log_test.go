package log

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewWritesJSONToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	l, err := New(Config{Level: "info", Format: "json", Output: path})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	l.Info("你好", "k", "v")
	if err := l.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &rec); err != nil {
		t.Fatalf("日志不是合法 JSON: %v, 内容=%q", err, string(data))
	}
	if rec["msg"] != "你好" {
		t.Errorf("msg = %v, want 你好", rec["msg"])
	}
	if rec["k"] != "v" {
		t.Errorf("k = %v, want v", rec["k"])
	}
}

func TestLevelFiltersAndSetLevelTakesEffect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	l, err := New(Config{Level: "warn", Format: "json", Output: path})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = l.Stop(context.Background()) }()

	l.Info("被过滤掉")
	if err := l.SetLevel("debug"); err != nil {
		t.Fatalf("SetLevel() error = %v", err)
	}
	l.Debug("应当写出")

	// 这里在 Stop（关闭文件）之前直接读取，之所以能读到内容，
	// 是因为 os.File 的 Write 不带用户态缓冲，写完立即可读。
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "被过滤掉") {
		t.Error("warn 级别下 Info 日志不应写出")
	}
	if !strings.Contains(content, "应当写出") {
		t.Error("SetLevel(debug) 后 Debug 日志应写出")
	}
}

func TestNewRejectsUnknownLevelAndFormat(t *testing.T) {
	if _, err := New(Config{Level: "nope"}); err == nil {
		t.Error("New() 未知级别应报错")
	}
	if _, err := New(Config{Format: "xml"}); err == nil {
		t.Error("New() 未知格式应报错")
	}
}

// TestTwoLoggersCanCarryDifferentNames 证明同一进程里可以用不同的 Name
// 区分两个日志实例，从而能在 App 中共存而不触发内核的重名校验。
func TestTwoLoggersCanCarryDifferentNames(t *testing.T) {
	primary, err := New(Config{Name: "log.primary", Output: "stdout"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	audit, err := New(Config{Name: "log.audit", Output: "stdout"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if primary.Name() == audit.Name() {
		t.Fatalf("两个实例的 Name 相同：%q", primary.Name())
	}
	if primary.Name() != "log.primary" {
		t.Errorf("Name() = %q, want %q", primary.Name(), "log.primary")
	}
}

func TestDefaultLogNameIsUsedWhenEmpty(t *testing.T) {
	l, err := New(Config{Output: "stdout"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if l.Name() != "log" {
		t.Errorf("Name() = %q, want %q", l.Name(), "log")
	}
}

func TestLoggerSatisfiesComponentMethodSet(t *testing.T) {
	l, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if l.Name() != "log" {
		t.Errorf("Name() = %q, want %q", l.Name(), "log")
	}
	if err := l.Start(context.Background()); err != nil {
		t.Errorf("Start() error = %v", err)
	}
	if err := l.Stop(context.Background()); err != nil {
		t.Errorf("Stop() error = %v", err)
	}
}
