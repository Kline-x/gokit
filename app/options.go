package app

import (
	"log/slog"
	"os"
	"syscall"
	"time"
)

type options struct {
	name        string
	version     string
	stopTimeout time.Duration
	logger      *slog.Logger
	signals     []os.Signal
}

// Option 用于定制 App 的行为。
type Option func(*options)

// WithName 设置应用名，写入启动与退出日志。
func WithName(name string) Option { return func(o *options) { o.name = name } }

// WithVersion 设置应用版本，写入启动日志。
func WithVersion(v string) Option { return func(o *options) { o.version = v } }

// WithStopTimeout 设置整体停止超时。当传给 Stop 的 ctx 没有 deadline 时生效。
func WithStopTimeout(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.stopTimeout = d
		}
	}
}

// WithLogger 替换 App 自身使用的日志器。
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithSignals 替换触发优雅退出的信号集合。传空表示不监听信号。
func WithSignals(sigs ...os.Signal) Option {
	return func(o *options) { o.signals = sigs }
}

func defaultOptions() options {
	return options{
		name:        "gokit-app",
		stopTimeout: 10 * time.Second,
		logger:      slog.Default(),
		signals:     []os.Signal{os.Interrupt, syscall.SIGTERM},
	}
}
