package config

import (
	"testing"
	"time"
)

func TestSetPathSetsNestedScalars(t *testing.T) {
	cases := []struct {
		path  string
		value string
		check func(*testing.T, testConfig)
	}{
		{"name", "abc", func(t *testing.T, c testConfig) {
			if c.Name != "abc" {
				t.Errorf("Name = %q, want %q", c.Name, "abc")
			}
		}},
		{"debug", "true", func(t *testing.T, c testConfig) {
			if !c.Debug {
				t.Error("Debug = false, want true")
			}
		}},
		{"workers", "12", func(t *testing.T, c testConfig) {
			if c.Workers != 12 {
				t.Errorf("Workers = %d, want 12", c.Workers)
			}
		}},
		{"tags", "x, y ,z", func(t *testing.T, c testConfig) {
			if len(c.Tags) != 3 || c.Tags[0] != "x" || c.Tags[2] != "z" {
				t.Errorf("Tags = %v, want [x y z]", c.Tags)
			}
		}},
		{"server.http.addr", "0.0.0.0:80", func(t *testing.T, c testConfig) {
			if c.Server.HTTP.Addr != "0.0.0.0:80" {
				t.Errorf("Addr = %q, want %q", c.Server.HTTP.Addr, "0.0.0.0:80")
			}
		}},
		{"server.http.read_timeout", "1500ms", func(t *testing.T, c testConfig) {
			if c.Server.HTTP.ReadTimeout != 1500*time.Millisecond {
				t.Errorf("ReadTimeout = %v, want 1.5s", c.Server.HTTP.ReadTimeout)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			var got testConfig
			if err := SetPath(&got, tc.path, tc.value); err != nil {
				t.Fatalf("SetPath(%q, %q) error = %v", tc.path, tc.value, err)
			}
			tc.check(t, got)
		})
	}
}

func TestSetPathRejectsUnknownPath(t *testing.T) {
	var cfg testConfig
	if err := SetPath(&cfg, "server.http.nope", "1"); err == nil {
		t.Fatal("SetPath() error = nil, want 未知路径错误")
	}
}

func TestSetPathRejectsBadValue(t *testing.T) {
	var cfg testConfig
	if err := SetPath(&cfg, "workers", "not-a-number"); err == nil {
		t.Fatal("SetPath() error = nil, want 数值解析错误")
	}
}

func TestPathsEnumeratesLeafFieldsInOrder(t *testing.T) {
	got := Paths(&testConfig{})
	want := []string{
		"name", "debug", "workers", "tags",
		"server.http.addr", "server.http.read_timeout",
	}
	if len(got) != len(want) {
		t.Fatalf("Paths() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Paths() = %v, want %v", got, want)
		}
	}
}

type namedOrigin string

type namedSliceConfig struct {
	Origins []namedOrigin `yaml:"origins"`
}

func TestSetPathHandlesNamedSliceElementType(t *testing.T) {
	var cfg namedSliceConfig
	if err := SetPath(&cfg, "origins", "a, b ,c"); err != nil {
		t.Fatalf("SetPath() error = %v", err)
	}
	if len(cfg.Origins) != 3 || cfg.Origins[0] != "a" || cfg.Origins[2] != "c" {
		t.Errorf("Origins = %v, want [a b c]", cfg.Origins)
	}
}

type skippedFieldConfig struct {
	Kept    string `yaml:"kept"`
	Ignored string `yaml:"-"`
	Tagged  string `yaml:"tagged,omitempty"`
}

func TestConfigNameHonoursTagOptionsAndSkips(t *testing.T) {
	got := Paths(&skippedFieldConfig{})
	want := []string{"kept", "tagged"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Paths() = %v, want %v", got, want)
	}

	var cfg skippedFieldConfig
	if err := SetPath(&cfg, "tagged", "v"); err != nil {
		t.Fatalf("SetPath(tagged) error = %v（带选项的标签名应可寻址）", err)
	}
	if err := SetPath(&cfg, "ignored", "v"); err == nil {
		t.Error("SetPath(ignored) error = nil，yaml:\"-\" 的字段不应可寻址")
	}
}

type commonConfig struct {
	A string `yaml:"a"`
}

type inlineConfig struct {
	Common commonConfig `yaml:",inline"`
	B      string       `yaml:"b"`
}

func TestInlineFieldsFlattenIntoParentPaths(t *testing.T) {
	got := Paths(&inlineConfig{})
	want := []string{"a", "b"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Paths() = %v, want %v（内联字段的键应在父级展开）", got, want)
	}

	var cfg inlineConfig
	if err := SetPath(&cfg, "a", "x"); err != nil {
		t.Fatalf("SetPath(a) error = %v", err)
	}
	if cfg.Common.A != "x" {
		t.Errorf("Common.A = %q, want %q", cfg.Common.A, "x")
	}
}
