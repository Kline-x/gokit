// Package grpcserver 提供 gRPC 服务组件。
//
// 具体的服务由使用方以 ServiceRegistrar 的形式传入，本包不关心它们是什么。
// 组件自带健康检查服务，因此不注册任何业务服务时也能起得来。
package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Config 是 gRPC 服务组件的配置。
type Config struct {
	// Name 是组件在 App 中的唯一标识。留空时取默认值。
	// 同一个 App 里注册多个同类组件时，必须给出互不相同的名字。
	Name string `yaml:"name"`
	// Addr 是监听地址，形如 :9000。测试中可用 127.0.0.1:0 让系统分配端口。
	Addr string `yaml:"addr"`
	// ShutdownTimeout 是优雅关闭时等待在途调用的上限，超时后强制停止。
	// 0 表示使用默认值；本组件不支持 sqldb 那样的 -1 哨兵。
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	// EnableReflection 决定是否开启反射服务，便于用 grpcurl 之类的工具调试。
	EnableReflection bool `yaml:"enable_reflection"`
}

// DefaultConfig 返回一组可直接使用的默认值。
func DefaultConfig() Config {
	return Config{
		Name:             "grpcserver",
		Addr:             ":9000",
		ShutdownTimeout:  10 * time.Second,
		EnableReflection: true,
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
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = d.ShutdownTimeout
	}
	return c
}

// ServiceRegistrar 是业务服务把自己挂到 gRPC 服务器上的方式。
//
// 生成的 gRPC 服务端代码通常提供 RegisterXxxServer(s, impl)，
// 在接口层写一个薄薄的适配器实现本接口即可。
type ServiceRegistrar interface {
	Register(s *grpc.Server)
}

// Option 用于定制 Server。
type Option func(*Server)

// WithFatal 注册运行期异常回调。Serve 意外退出时会被调用，
// 典型用法是传入 app.App 的 Fatal 方法，触发整体优雅退出。
func WithFatal(fn func(error)) Option {
	return func(s *Server) { s.onFatal = fn }
}

// WithUnaryInterceptor 追加一元拦截器，按传入顺序由外向内生效。
//
// 默认装上的 Recover 与 ErrorMapper 始终在最内层，这里传入的拦截器
// 只会包在它们外面。
func WithUnaryInterceptor(is ...grpc.UnaryServerInterceptor) Option {
	return func(s *Server) { s.unary = append(s.unary, is...) }
}

// WithLogger 替换本组件默认拦截器使用的日志器。不设时取 slog.Default()。
func WithLogger(l *slog.Logger) Option {
	return func(s *Server) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithoutDefaultInterceptors 关掉默认装上的 Recover 与 ErrorMapper。
//
// 默认装它们是因为两者都关乎安全：漏了 Recover，一次 panic 会掀掉整个进程；
// 漏了 ErrorMapper，业务错误会以 Unknown 抵达客户端，且 Error() 的完整内容
// （连同 cause 记录的底层原因）会被当成 status 描述发出去。
// 只有在你确定要自己完整接管这两件事时才关掉它。
func WithoutDefaultInterceptors() Option {
	return func(s *Server) { s.withoutDefaults = true }
}

// Server 是实现了 app.Component 方法集的 gRPC 服务。
type Server struct {
	cfg      Config
	services []ServiceRegistrar
	unary    []grpc.UnaryServerInterceptor
	onFatal  func(error)

	logger          *slog.Logger
	withoutDefaults bool

	health *health.Server

	mu  sync.Mutex
	srv *grpc.Server
	ln  net.Listener
}

// New 创建 gRPC 服务组件。services 可以为 nil，此时只提供健康检查。
//
// 默认会在拦截器链最内层装上 Recover 与 ErrorMapper：前者兜住 panic
// 避免进程被掀翻，后者把业务错误翻译成正确的 gRPC status 而不是泛化的
// Unknown。WithUnaryInterceptor 传入的拦截器只会包在它们外面。
// 需要自己完整接管这两件事时，用 WithoutDefaultInterceptors 关掉。
func New(cfg Config, services []ServiceRegistrar, opts ...Option) *Server {
	s := &Server{
		cfg:      cfg.withDefaults(),
		services: services,
		health:   health.NewServer(),
		logger:   slog.Default(),
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
		return fmt.Errorf("grpcserver: %s 监听 %s 失败: %w", s.cfg.Name, s.cfg.Addr, err)
	}

	// 默认拦截器放在最内层：使用方通过 WithUnaryInterceptor 传进来的
	// 只会包在外面，因此 RequestLog 这类需要看到已翻译状态码的拦截器
	// 天然处在正确的位置。
	unary := s.unary
	if !s.withoutDefaults {
		unary = append(append([]grpc.UnaryServerInterceptor{}, s.unary...),
			Recover(s.logger), ErrorMapper())
	}
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(unary...))
	for _, svc := range s.services {
		svc.Register(srv)
	}
	healthpb.RegisterHealthServer(srv, s.health)
	s.health.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	if s.cfg.EnableReflection {
		reflection.Register(srv)
	}

	s.mu.Lock()
	s.srv = srv
	s.ln = ln
	s.mu.Unlock()

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			if s.onFatal != nil {
				s.onFatal(fmt.Errorf("grpcserver: %s 服务异常退出: %w", s.cfg.Name, err))
			}
		}
	}()
	return nil
}

// Stop 实现 app.Component，优雅关闭并等待在途调用；超时后强制停止。
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	srv := s.srv
	s.srv = nil
	s.ln = nil
	s.mu.Unlock()

	if srv == nil {
		return nil
	}

	s.health.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)

	if s.cfg.ShutdownTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.ShutdownTimeout)
		defer cancel()
	}

	done := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		// 还有在途调用没排空，强制停止，避免拖住整个应用的关停预算。
		srv.Stop()
		<-done
		return fmt.Errorf("grpcserver: %s 优雅关闭超时，已强制停止: %w", s.cfg.Name, ctx.Err())
	}
}

// Addr 返回真实监听地址。Start 之前或 Stop 之后返回 nil。
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}
