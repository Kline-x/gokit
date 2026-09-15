// Package config 负责把 YAML 文件、环境变量与显式覆盖逐级合并进配置结构体。
//
// 合并顺序固定为：文件（按传入顺序）→ 环境变量 → 显式覆盖。
// 配置结构体的字段只能用值类型，不能用指针，否则路径覆盖无法定位字段。
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"

	"gopkg.in/yaml.v3"
)

type fileSource struct {
	path     string
	optional bool
}

// Loader 按既定顺序合并多个配置来源。
type Loader struct {
	files []fileSource
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
func (l *Loader) Load(dst any) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config: dst 必须是指向结构体的非空指针，得到 %T", dst)
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
	return nil
}
