// Package config 负责把 YAML 文件、环境变量与显式覆盖逐级合并进配置结构体。
//
// 合并顺序固定为：文件（按传入顺序）→ 环境变量 → 显式覆盖。
// 配置结构体的字段只能用值类型，不能用指针，否则路径覆盖无法定位字段；
// 带指针字段、或展开后不含任何可寻址叶子的结构体字段（如 time.Time）的
// 配置结构体，会被 Load 直接拒绝，而不是静默失效。
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"sort"

	"gopkg.in/yaml.v3"
)

type fileSource struct {
	path     string
	optional bool
}

// Loader 按既定顺序合并多个配置来源。
type Loader struct {
	files      []fileSource
	envPrefix  string
	envEnabled bool
	overrides  map[string]string
}

// Option 用于定制 Loader。
type Option func(*Loader)

// WithFile 追加必需的 YAML 文件。文件不存在时 Load 返回错误。
func WithFile(paths ...string) Option {
	return func(l *Loader) {
		for _, p := range paths {
			l.files = append(l.files, fileSource{path: p})
		}
	}
}

// WithOptionalFile 追加可选的 YAML 文件。文件不存在时静默跳过。
func WithOptionalFile(paths ...string) Option {
	return func(l *Loader) {
		for _, p := range paths {
			l.files = append(l.files, fileSource{path: p, optional: true})
		}
	}
}

// WithEnvPrefix 开启环境变量覆盖。变量名由前缀与配置路径拼成，
// 例如前缀 APP、路径 server.http.addr 对应 APP_SERVER_HTTP_ADDR。
//
// 传空前缀表示不加前缀，直接用路径本身作变量名（server.http.addr 对应
// SERVER_HTTP_ADDR）。只要调用了本选项就会启用环境变量覆盖，
// 空前缀与「从未调用」是两回事。
func WithEnvPrefix(prefix string) Option {
	return func(l *Loader) {
		l.envPrefix = prefix
		l.envEnabled = true
	}
}

// WithOverride 追加显式覆盖，键为配置路径。通常来自命令行 --set key=value。
func WithOverride(kv map[string]string) Option {
	return func(l *Loader) {
		if l.overrides == nil {
			l.overrides = make(map[string]string, len(kv))
		}
		for k, v := range kv {
			l.overrides[k] = v
		}
	}
}

// New 创建一个 Loader。
func New(opts ...Option) *Loader {
	l := &Loader{}
	for _, fn := range opts {
		fn(l)
	}
	return l
}

// Load 把各配置来源依次合并进 dst。dst 必须是指向结构体的非空指针。
// 某个来源未提供的字段保持 dst 的原值，因此调用方可以先填好默认值再 Load。
//
// dst 会先经过 Validate 校验：带指针字段、或展开后不含任何可寻址叶子的
// 结构体字段（如 time.Time），会被直接拒绝，不会等到读文件、读环境变量
// 才发现覆盖不生效。
func (l *Loader) Load(dst any) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config: dst 必须是指向结构体的非空指针，得到 %T", dst)
	}

	if err := Validate(dst); err != nil {
		return err
	}

	for _, f := range l.files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			if f.optional && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("config: 读取 %s 失败: %w", f.path, err)
		}
		if err := yaml.Unmarshal(data, dst); err != nil {
			return fmt.Errorf("config: 解析 %s 失败: %w", f.path, err)
		}
	}

	if l.envEnabled {
		if err := applyEnv(dst, l.envPrefix); err != nil {
			return err
		}
	}

	// 按路径排序，保证多个覆盖出错时的报错顺序稳定。
	paths := make([]string, 0, len(l.overrides))
	for p := range l.overrides {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := SetPath(dst, p, l.overrides[p]); err != nil {
			return err
		}
	}
	return nil
}
