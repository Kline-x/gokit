// Package log 提供基于标准库 log/slog 的日志组件。
//
// 日志是横切关注点：其他组件可以 import 本包，用 FromContext 取出
// 当前请求的 logger。反过来，本包不 import 任何其他组件。
package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config 是日志组件的配置。
type Config struct {
	// Level 取 debug / info / warn / error，默认 info。
	Level string `yaml:"level"`
	// Format 取 json / text，默认 json。
	Format string `yaml:"format"`
	// Output 取 stdout / stderr 或文件路径，默认 stdout。
	Output string `yaml:"output"`
}

// DefaultConfig 返回可直接使用的默认配置。
func DefaultConfig() Config {
	return Config{Level: "info", Format: "json", Output: "stdout"}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.Level == "" {
		c.Level = d.Level
	}
	if c.Format == "" {
		c.Format = d.Format
	}
	if c.Output == "" {
		c.Output = d.Output
	}
	return c
}

// Logger 是实现了 app.Component 方法集的日志器。
type Logger struct {
	*slog.Logger
	level  *slog.LevelVar
	closer io.Closer
}

// New 按配置创建日志组件。Output 指向文件时会创建或追加该文件。
func New(cfg Config) (*Logger, error) {
	cfg = cfg.withDefaults()

	lv := new(slog.LevelVar)
	if err := parseLevel(cfg.Level, lv); err != nil {
		return nil, err
	}

	w, closer, err := openOutput(cfg.Output)
	if err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{Level: lv}
	var h slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "json":
		h = slog.NewJSONHandler(w, opts)
	case "text":
		h = slog.NewTextHandler(w, opts)
	default:
		if closer != nil {
			_ = closer.Close()
		}
		return nil, fmt.Errorf("log: 未知日志格式 %q，仅支持 json 与 text", cfg.Format)
	}

	return &Logger{Logger: slog.New(h), level: lv, closer: closer}, nil
}

// Name 实现 app.Component。
func (l *Logger) Name() string { return "log" }

// Start 实现 app.Component。日志器在 New 时已就绪，这里是空操作。
func (l *Logger) Start(context.Context) error { return nil }

// Stop 实现 app.Component。输出为文件时关闭文件句柄。
func (l *Logger) Stop(context.Context) error {
	if l.closer == nil {
		return nil
	}
	if err := l.closer.Close(); err != nil {
		return fmt.Errorf("log: 关闭日志文件失败: %w", err)
	}
	return nil
}

// SetLevel 在运行期调整日志级别，用于配置热更新。
func (l *Logger) SetLevel(level string) error { return parseLevel(level, l.level) }

func parseLevel(s string, lv *slog.LevelVar) error {
	switch strings.ToLower(s) {
	case "debug":
		lv.Set(slog.LevelDebug)
	case "info", "":
		lv.Set(slog.LevelInfo)
	case "warn", "warning":
		lv.Set(slog.LevelWarn)
	case "error":
		lv.Set(slog.LevelError)
	default:
		return fmt.Errorf("log: 未知日志级别 %q，仅支持 debug/info/warn/error", s)
	}
	return nil
}

func openOutput(out string) (io.Writer, io.Closer, error) {
	switch strings.ToLower(out) {
	case "stdout":
		return os.Stdout, nil, nil
	case "stderr":
		return os.Stderr, nil, nil
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("log: 打开日志文件 %s 失败: %w", out, err)
	}
	return f, f, nil
}
