package grpcclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Kline-x/gokit/component/grpcserver"
	"github.com/Kline-x/gokit/transport"
)

// startTestServer 起一个只提供健康检查的 gRPC 服务，返回它的地址。
func startTestServer(t *testing.T) string {
	t.Helper()
	s := grpcserver.New(grpcserver.Config{Name: "grpcserver.test", Addr: "127.0.0.1:0"}, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("起测试服务失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s.Addr().String()
}

func TestClientConnectsAndReportsHealthy(t *testing.T) {
	addr := startTestServer(t)

	c, err := New(Config{Name: "grpcclient.test", Target: addr})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Stop(ctx) })

	if c.Conn() == nil {
		t.Fatal("Conn() = nil，Start 之后应当可用")
	}
	if err := c.Health(ctx); err != nil {
		t.Errorf("Health() error = %v", err)
	}
}

func TestClientHealthFailsWhenServerGone(t *testing.T) {
	addr := startTestServer(t)

	c, err := New(Config{Name: "grpcclient.test", Target: addr})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if err := c.Health(ctx); err == nil {
		t.Error("Stop 之后 Health 仍然成功，连接未真正关闭")
	}
}

func TestNameComesFromConfig(t *testing.T) {
	c, err := New(Config{Name: "grpcclient.user", Target: "127.0.0.1:1"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })

	if c.Name() != "grpcclient.user" {
		t.Errorf("Name() = %q, want %q", c.Name(), "grpcclient.user")
	}

	d, err := New(Config{Target: "127.0.0.1:1"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })
	if d.Name() != "grpcclient" {
		t.Errorf("Name() = %q, want 默认值 %q", d.Name(), "grpcclient")
	}
}

func TestNewRejectsEmptyTarget(t *testing.T) {
	if _, err := New(Config{Name: "grpcclient.test"}); err == nil {
		t.Fatal("New() error = nil, want 目标地址为空的错误")
	}
}

func TestErrorRestorerRebuildsTransportError(t *testing.T) {
	restorer := ErrorRestorer()

	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return status.Error(codes.NotFound, "USER_NOT_FOUND: 用户不存在")
	}

	err := restorer(context.Background(), "/greeter.v1.Greeter/Greet", nil, nil, nil, invoker)

	var te *transport.Error
	if !errors.As(err, &te) {
		t.Fatalf("未能还原成 *transport.Error: %v", err)
	}
	if te.Code != transport.CodeNotFound {
		t.Errorf("Code = %d, want %d", te.Code, transport.CodeNotFound)
	}
	if te.Reason != "USER_NOT_FOUND" {
		t.Errorf("Reason = %q, want %q", te.Reason, "USER_NOT_FOUND")
	}
	if te.Message != "用户不存在" {
		t.Errorf("Message = %q, want %q", te.Message, "用户不存在")
	}
}

func TestErrorRestorerHandlesMessageWithoutReason(t *testing.T) {
	restorer := ErrorRestorer()

	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return status.Error(codes.Unavailable, "连接被拒绝")
	}

	err := restorer(context.Background(), "/x/y", nil, nil, nil, invoker)

	var te *transport.Error
	if !errors.As(err, &te) {
		t.Fatalf("未能还原成 *transport.Error: %v", err)
	}
	if te.Code != transport.CodeUnavailable {
		t.Errorf("Code = %d, want %d", te.Code, transport.CodeUnavailable)
	}
	if te.Message != "连接被拒绝" {
		t.Errorf("Message = %q, want %q", te.Message, "连接被拒绝")
	}
}

func TestErrorRestorerLeavesSuccessAlone(t *testing.T) {
	restorer := ErrorRestorer()
	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return nil
	}
	if err := restorer(context.Background(), "/x/y", nil, nil, nil, invoker); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestTransportCodeIsInverseOfGRPCCode(t *testing.T) {
	// 两侧的映射必须对称，否则跨进程后错误码会漂移。
	codesToCheck := []int{
		transport.CodeInvalidArgument,
		transport.CodeUnauthenticated,
		transport.CodePermissionDenied,
		transport.CodeNotFound,
		transport.CodeAlreadyExists,
		transport.CodeFailedPrecondition,
		transport.CodeRateLimited,
		transport.CodeInternal,
		transport.CodeUnavailable,
		transport.CodeTimeout,
	}

	for _, code := range codesToCheck {
		grpcCode := grpcserver.GRPCCode(code)
		if got := TransportCode(grpcCode); got != code {
			t.Errorf("往返不一致: transport %d -> grpc %v -> transport %d", code, grpcCode, got)
		}
	}
}

func TestDialTimeoutIsRespected(t *testing.T) {
	// 连一个不存在的地址，Start 应当在超时内返回错误而不是一直卡着。
	c, err := New(Config{Name: "grpcclient.test", Target: "127.0.0.1:1", Block: true, DialTimeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })

	begin := time.Now()
	startErr := c.Start(context.Background())
	elapsed := time.Since(begin)

	if startErr == nil {
		t.Fatal("Start() error = nil, want 建连超时错误")
	}
	if elapsed > 3*time.Second {
		t.Errorf("Start() 耗时 %v，应当在 DialTimeout 附近返回", elapsed)
	}
}
