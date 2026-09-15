package config

import "testing"

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
