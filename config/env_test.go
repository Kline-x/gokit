package config

import (
	"strings"
	"testing"
	"time"
)

func TestEnvNameMapsDottedPath(t *testing.T) {
	if got := EnvName("APP", "server.http.addr"); got != "APP_SERVER_HTTP_ADDR" {
		t.Errorf("EnvName() = %q, want %q", got, "APP_SERVER_HTTP_ADDR")
	}
	if got := EnvName("", "name"); got != "NAME" {
		t.Errorf("EnvName() = %q, want %q", got, "NAME")
	}
}

func TestLoadAppliesEnvOverFile(t *testing.T) {
	path := writeFile(t, "config.yaml", "name: from-file\nworkers: 1\n")
	t.Setenv("APP_WORKERS", "7")

	var cfg testConfig
	if err := New(WithFile(path), WithEnvPrefix("APP")).Load(&cfg); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Name != "from-file" {
		t.Errorf("Name = %q, want %q", cfg.Name, "from-file")
	}
	if cfg.Workers != 7 {
		t.Errorf("Workers = %d, want 7（环境变量应覆盖文件）", cfg.Workers)
	}
}

func TestLoadAppliesOverrideOverEnv(t *testing.T) {
	t.Setenv("APP_WORKERS", "7")

	var cfg testConfig
	err := New(
		WithEnvPrefix("APP"),
		WithOverride(map[string]string{"workers": "99"}),
	).Load(&cfg)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Workers != 99 {
		t.Errorf("Workers = %d, want 99（显式覆盖应压过环境变量）", cfg.Workers)
	}
}

func TestLoadFailsOnUnknownOverridePath(t *testing.T) {
	var cfg testConfig
	err := New(WithOverride(map[string]string{"nope": "1"})).Load(&cfg)
	if err == nil {
		t.Fatal("Load() error = nil, want 未知路径错误")
	}
}

func TestLoadWithEmptyEnvPrefixStillApplies(t *testing.T) {
	t.Setenv("WORKERS", "5")

	var cfg testConfig
	if err := New(WithEnvPrefix("")).Load(&cfg); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Workers != 5 {
		t.Errorf("Workers = %d, want 5（显式传空前缀应启用无前缀的环境变量覆盖）", cfg.Workers)
	}
}

func TestLoadWithoutEnvPrefixIgnoresEnvironment(t *testing.T) {
	t.Setenv("WORKERS", "5")

	cfg := testConfig{Workers: 1}
	if err := New().Load(&cfg); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Workers != 1 {
		t.Errorf("Workers = %d, want 1（未调用 WithEnvPrefix 时不应读环境变量）", cfg.Workers)
	}
}

type ptrSectionConfig struct {
	TLS  *httpConfig `yaml:"tls"`
	Name string      `yaml:"name"`
}

type timeFieldConfig struct {
	At   time.Time `yaml:"at"`
	Name string    `yaml:"name"`
}

func TestLoadRejectsPointerField(t *testing.T) {
	var cfg ptrSectionConfig
	err := New().Load(&cfg)
	if err == nil {
		t.Fatal("Load() error = nil，指针字段应被拒绝而不是静默失效")
	}
	if !strings.Contains(err.Error(), "tls") {
		t.Errorf("错误信息 = %q，应指出是 tls 字段", err.Error())
	}
}

func TestLoadRejectsUnaddressableStructField(t *testing.T) {
	var cfg timeFieldConfig
	err := New().Load(&cfg)
	if err == nil {
		t.Fatal("Load() error = nil，展开后无可寻址字段的结构体应被拒绝")
	}
	if !strings.Contains(err.Error(), "at") {
		t.Errorf("错误信息 = %q，应指出是 at 字段", err.Error())
	}
}

func TestLoadAcceptsOrdinaryConfig(t *testing.T) {
	var cfg testConfig
	if err := New().Load(&cfg); err != nil {
		t.Fatalf("Load() error = %v，普通配置结构体不应被校验拒绝", err)
	}
}

func TestLoadRejectsNilPointerDestination(t *testing.T) {
	var cfg *testConfig
	if err := New().Load(cfg); err == nil {
		t.Fatal("Load() error = nil，空指针目标应被拒绝")
	}
}

func TestLoadRejectsPointerToNonStruct(t *testing.T) {
	n := 0
	if err := New().Load(&n); err == nil {
		t.Fatal("Load() error = nil，指向非结构体的指针应被拒绝")
	}
}

func TestLoadSurfacesUnreadableFile(t *testing.T) {
	// 传一个目录当配置文件：它存在，但读取会失败，
	// 不能被当成「可选文件不存在」而静默跳过。
	dir := t.TempDir()
	var cfg testConfig
	if err := New(WithOptionalFile(dir)).Load(&cfg); err == nil {
		t.Fatal("Load() error = nil，存在但读不了的可选文件应当报错")
	}
}
