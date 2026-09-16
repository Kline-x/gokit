// Package grpcclient 提供 gRPC 客户端连接组件。
//
// 它把一条 grpc.ClientConn 的生命周期交给 App 管理：Start 建连、
// Health 探活、Stop 关闭。业务模块的远程实现拿着 Conn() 去构造
// 生成的客户端桩，因而不必各自管理连接。
package grpcclient

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Config 是 gRPC 客户端组件的配置。
type Config struct {
	// Name 是组件在 App 中的唯一标识。留空时取默认值。
	// 连多个下游服务时，必须给出互不相同的名字。
	Name string `yaml:"name"`
	// Target 是下游地址，形如 127.0.0.1:9000 或 dns:///user-service:9000。
	Target string `yaml:"target"`
	// DialTimeout 是建连与探活的超时。
	// 0 表示使用默认值；本组件不支持 sqldb 那样的 -1 哨兵。
	DialTimeout time.Duration `yaml:"dial_timeout"`
	// Block 决定 Start 是否等待连接真正就绪。
	// 置假时 Start 立刻返回，首次调用才会触发建连，适合下游可能晚于本服务启动的场景。
	Block bool `yaml:"block"`
}

// DefaultConfig 返回一组可直接使用的默认值。
func DefaultConfig() Config {
	return Config{
		Name:        "grpcclient",
		DialTimeout: 5 * time.Second,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.Name == "" {
		c.Name = d.Name
	}
	if c.DialTimeout == 0 {
		c.DialTimeout = d.DialTimeout
	}
	return c
}

// Option 用于定制 Client。
type Option func(*Client)

// WithUnaryInterceptor 追加一元拦截器，按传入顺序由外向内生效。
func WithUnaryInterceptor(is ...grpc.UnaryClientInterceptor) Option {
	return func(c *Client) { c.unary = append(c.unary, is...) }
}

// Client 是实现了 app.Component 方法集的 gRPC 连接。
type Client struct {
	cfg   Config
	unary []grpc.UnaryClientInterceptor

	mu   sync.Mutex
	conn *grpc.ClientConn
}

// New 创建客户端组件。此时并不建连，建连发生在 Start。
func New(cfg Config, opts ...Option) (*Client, error) {
	cfg = cfg.withDefaults()
	if cfg.Target == "" {
		return nil, fmt.Errorf("grpcclient: %s 的 target 不能为空", cfg.Name)
	}

	c := &Client{cfg: cfg}
	for _, fn := range opts {
		fn(c)
	}
	return c, nil
}

// Name 实现 app.Component。
func (c *Client) Name() string { return c.cfg.Name }

// Start 实现 app.Component，建立连接。
//
// Block 为真时会等到连接就绪或超时；为假时只创建连接对象，
// 真正的建连推迟到首次调用。
func (c *Client) Start(ctx context.Context) error {
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	if len(c.unary) > 0 {
		dialOpts = append(dialOpts, grpc.WithChainUnaryInterceptor(c.unary...))
	}

	conn, err := grpc.NewClient(c.cfg.Target, dialOpts...)
	if err != nil {
		return fmt.Errorf("grpcclient: %s 连接 %s 失败: %w", c.cfg.Name, c.cfg.Target, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	if !c.cfg.Block {
		return nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, c.cfg.DialTimeout)
	defer cancel()

	conn.Connect()
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return nil
		}
		if !conn.WaitForStateChange(waitCtx, state) {
			return fmt.Errorf("grpcclient: %s 建连 %s 超时: %w",
				c.cfg.Name, c.cfg.Target, waitCtx.Err())
		}
	}
}

// Stop 实现 app.Component，关闭连接。重复调用是安全的空操作。
func (c *Client) Stop(context.Context) error {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if conn == nil {
		return nil
	}
	if err := conn.Close(); err != nil {
		return fmt.Errorf("grpcclient: %s 关闭连接失败: %w", c.cfg.Name, err)
	}
	return nil
}

// Health 实现 app.HealthChecker，调用下游的标准健康检查服务。
func (c *Client) Health(ctx context.Context) error {
	conn := c.Conn()
	if conn == nil {
		return fmt.Errorf("grpcclient: %s 没有可用连接（尚未 Start，或已经 Stop）", c.cfg.Name)
	}

	ctx, cancel := context.WithTimeout(ctx, c.cfg.DialTimeout)
	defer cancel()

	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("grpcclient: %s 探活失败: %w", c.cfg.Name, err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("grpcclient: %s 下游状态为 %s", c.cfg.Name, resp.GetStatus())
	}
	return nil
}

// Conn 返回底层连接。Start 之前或 Stop 之后返回 nil。
func (c *Client) Conn() *grpc.ClientConn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}
