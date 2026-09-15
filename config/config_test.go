package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type httpConfig struct {
	Addr        string        `yaml:"addr"`
	ReadTimeout time.Duration `yaml:"read_timeout"`
}

type serverConfig struct {
	HTTP httpConfig `yaml:"http"`
}

type testConfig struct {
	Name    string       `yaml:"name"`
	Debug   bool         `yaml:"debug"`
	Workers int          `yaml:"workers"`
	Tags    []string     `yaml:"tags"`
	Server  serverConfig `yaml:"server"`
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
	return path
}

func TestLoadReadsYAMLAndKeepsUnsetDefaults(t *testing.T) {
	path := writeFile(t, "config.yaml", "name: demo\ntags: [a, b]\nserver:\n  http:\n    addr: \":9000\"\n    read_timeout: 3s\n")

	cfg := testConfig{Workers: 4, Debug: true}
	if err := New(WithFile(path)).Load(&cfg); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Name != "demo" {
		t.Errorf("Name = %q, want %q", cfg.Name, "demo")
	}
	if cfg.Server.HTTP.Addr != ":9000" {
		t.Errorf("Server.HTTP.Addr = %q, want %q", cfg.Server.HTTP.Addr, ":9000")
	}
	if cfg.Server.HTTP.ReadTimeout != 3*time.Second {
		t.Errorf("Server.HTTP.ReadTimeout = %v, want 3s", cfg.Server.HTTP.ReadTimeout)
	}
	if len(cfg.Tags) != 2 || cfg.Tags[0] != "a" || cfg.Tags[1] != "b" {
		t.Errorf("Tags = %v, want [a b]", cfg.Tags)
	}
	if cfg.Workers != 4 {
		t.Errorf("Workers = %d, want 4（yaml 未提供的字段应保留默认值）", cfg.Workers)
	}
	if !cfg.Debug {
		t.Error("Debug = false, want true（yaml 未提供的字段应保留默认值）")
	}
}

func TestLoadAppliesFilesInOrder(t *testing.T) {
	base := writeFile(t, "base.yaml", "name: base\nworkers: 1\n")
	over := writeFile(t, "over.yaml", "workers: 9\n")

	var cfg testConfig
	if err := New(WithFile(base, over)).Load(&cfg); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Name != "base" {
		t.Errorf("Name = %q, want %q", cfg.Name, "base")
	}
	if cfg.Workers != 9 {
		t.Errorf("Workers = %d, want 9（后一个文件应覆盖前一个）", cfg.Workers)
	}
}

func TestLoadFailsOnMissingRequiredFile(t *testing.T) {
	var cfg testConfig
	err := New(WithFile(filepath.Join(t.TempDir(), "nope.yaml"))).Load(&cfg)
	if err == nil {
		t.Fatal("Load() error = nil, want 文件缺失错误")
	}
}

func TestLoadSkipsMissingOptionalFile(t *testing.T) {
	var cfg testConfig
	err := New(WithOptionalFile(filepath.Join(t.TempDir(), "nope.yaml"))).Load(&cfg)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil（可选文件缺失应跳过）", err)
	}
}

func TestLoadRejectsNonPointerDestination(t *testing.T) {
	var cfg testConfig
	if err := New().Load(cfg); err == nil {
		t.Fatal("Load() error = nil, want 非指针错误")
	}
}
