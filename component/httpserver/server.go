// Package httpserver 提供基于标准库 net/http 的 HTTP 服务组件。
//
// 路由由使用方以 http.Handler 的形式传入，本包不绑定任何路由框架。
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Config 是 HTTP 服务组件的配置。
type Config struct {
	// Name 是组件在 App 中的唯一标识。留空时取默认值。
	// 同一个 App 里注册多个同类组件时，必须给出互不相同的名字。
	Name string `yaml:"name"`
	// Addr 是监听地址，形如 :8080。测试中可用 127.0.0.1:0 让系统分配端口。
	Addr string `yaml:"addr"`
	// ReadTimeout 是读取整个请求（含 body）的超时。
	// 0 表示使用默认值；本组件不支持 sqldb 那样的 -1 哨兵。
	ReadTimeout time.Duration `yaml:"read_timeout"`
	// WriteTimeout 是写响应的超时。
	// 0 表示使用默认值；本组件不支持 sqldb 那样的 -1 哨兵。
	WriteTimeout time.Duration `yaml:"write_timeout"`
	// IdleTimeout 是 keep-alive 连接的空闲超时。
	// 0 表示使用默认值；本组件不支持 sqldb 那样的 -1 哨兵。
	IdleTimeout time.Duration `yaml:"idle_timeout"`
	// ShutdownTimeout 是优雅关闭时等待在途请求的上限。
	// 0 表示使用默认值；本组件不支持 sqldb 那样的 -1 哨兵。
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// DefaultConfig 返回一组可直接用于生产的保守默认值。
func DefaultConfig() Config {
	return Config{
		Name:            "httpserver",
		Addr:            ":8080",
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    15 * time.Second,
		IdleTimeout:     60 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.Name == "" {
		c.Name = d.Name
	}
	if c.Addr == "" {
		c.Addr = d.Addr
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = d.ReadTimeout
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = d.WriteTimeout
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = d.IdleTimeout
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = d.ShutdownTimeout
	}
	return c
}

// Option 用于定制 Server。
type Option func(*Server)

// WithFatal 注册运行期异常回调。Serve 意外退出时会被调用，
// 典型用法是传入 app.App 的 Fatal 方法，触发整体优雅退出。
func WithFatal(fn func(error)) Option {
	return func(s *Server) { s.onFatal = fn }
}

// Server 是实现了 app.Component 方法集的 HTTP 服务。
type Server struct {
	cfg     Config
	srv     *http.Server
	onFatal func(error)

	mu sync.Mutex
	ln net.Listener
}

// New 创建 HTTP 服务组件。h 为空时使用 http.DefaultServeMux。
func New(cfg Config, h http.Handler, opts ...Option) *Server {
	cfg = cfg.withDefaults()
	s := &Server{
		cfg: cfg,
		srv: &http.Server{
			Handler:      h,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
			IdleTimeout:  cfg.IdleTimeout,
		},
	}
	for _, fn := range opts {
		fn(s)
	}
	return s
}

// Name 实现 app.Component。
func (s *Server) Name() string { return s.cfg.Name }

// Start 实现 app.Component。
// 监听动作是同步的，因此 Start 返回后 Addr() 即可用；Serve 在后台 goroutine 中运行。
func (s *Server) Start(context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("httpserver: %s 监听 %s 失败: %w", s.cfg.Name, s.cfg.Addr, err)
	}

	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()

	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			if s.onFatal != nil {
				s.onFatal(fmt.Errorf("httpserver: %s 服务异常退出: %w", s.cfg.Name, err))
			}
		}
	}()
	return nil
}

// Stop 实现 app.Component，优雅关闭并等待在途请求。
func (s *Server) Stop(ctx context.Context) error {
	if s.cfg.ShutdownTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.ShutdownTimeout)
		defer cancel()
	}
	if err := s.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("httpserver: 关闭 %s 失败: %w", s.cfg.Name, err)
	}
	return nil
}

// Addr 返回真实监听地址。Start 之前返回 nil。
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}
