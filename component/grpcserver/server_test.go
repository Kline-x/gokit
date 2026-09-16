package grpcserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/Kline-x/gokit/transport"
)

// registrarFunc 让测试可以用一个函数字面量充当 ServiceRegistrar，
// 不必依赖 protoc 生成代码。
type registrarFunc func(*grpc.Server)

func (f registrarFunc) Register(s *grpc.Server) { f(s) }

// failingServiceDesc 手写一个只有一个方法的 gRPC 服务：直接返回一个
// 带 cause 的业务错误。用它可以在不依赖任何 .proto 生成代码的前提下，
// 验证默认拦截器链是否真的被装上——请求消息复用 healthpb 里已有的类型即可，
// 因为这里根本不关心消息内容。
func failingServiceDesc(businessErr error) *grpc.ServiceDesc {
	return &grpc.ServiceDesc{
		ServiceName: "grpcserver.test.Failing",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "Fail",
				Handler: func(_ any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
					req := new(healthpb.HealthCheckRequest)
					if err := dec(req); err != nil {
						return nil, err
					}
					handler := func(ctx context.Context, _ any) (any, error) {
						return nil, businessErr
					}
					if interceptor == nil {
						return handler(ctx, req)
					}
					info := &grpc.UnaryServerInfo{FullMethod: "/grpcserver.test.Failing/Fail"}
					return interceptor(ctx, req, info, handler)
				},
			},
		},
		Streams:  []grpc.StreamDesc{},
		Metadata: "grpcserver_test",
	}
}

func newTestServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	return New(Config{Name: "grpcserver.test", Addr: "127.0.0.1:0"}, nil, opts...)
}

func TestServerStartsAndServesHealth(t *testing.T) {
	s := newTestServer(t)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	if s.Addr() == nil {
		t.Fatal("Addr() = nil，Start 之后应能拿到真实监听地址")
	}

	conn, err := grpc.NewClient(s.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("建连失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("健康检查失败: %v", err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("status = %v, want SERVING", resp.GetStatus())
	}
}

func TestStopMakesServerUnreachable(t *testing.T) {
	s := newTestServer(t)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	addr := s.Addr().String()

	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("建连对象创建失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err == nil {
		t.Error("Stop 之后请求仍然成功，服务未真正关闭")
	}
}

func TestStartFailsOnOccupiedAddress(t *testing.T) {
	first := newTestServer(t)
	if err := first.Start(context.Background()); err != nil {
		t.Fatalf("第一个 Start() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Stop(context.Background()) })

	second := New(Config{Name: "grpcserver.second", Addr: first.Addr().String()}, nil)
	if err := second.Start(context.Background()); err == nil {
		_ = second.Stop(context.Background())
		t.Fatal("Start() error = nil, want 端口占用错误")
	}
}

func TestStopBeforeStartIsSafe(t *testing.T) {
	s := newTestServer(t)
	if err := s.Stop(context.Background()); err != nil {
		t.Errorf("Stop() error = %v, want nil（从未 Start 过也应能安全 Stop）", err)
	}
}

func TestNameComesFromConfig(t *testing.T) {
	if got := newTestServer(t).Name(); got != "grpcserver.test" {
		t.Errorf("Name() = %q, want %q", got, "grpcserver.test")
	}
	if got := New(Config{Addr: "127.0.0.1:0"}, nil).Name(); got != "grpcserver" {
		t.Errorf("Name() = %q, want 默认值 %q", got, "grpcserver")
	}
}

func TestDefaultInterceptorsTranslateBusinessError(t *testing.T) {
	// 使用方没传任何拦截器时，业务错误也必须被翻译成正确的 status code，
	// 而不是带着 Error() 的完整内容（连同 cause 里的底层原因）以 Unknown 抵达客户端。
	businessErr := transport.Internal("DB_FAIL", "内部错误").
		WithCause(errors.New("dial tcp 10.0.0.1:3306: connect: connection refused"))

	registrar := registrarFunc(func(s *grpc.Server) {
		s.RegisterService(failingServiceDesc(businessErr), struct{}{})
	})

	s := New(Config{Name: "grpcserver.test", Addr: "127.0.0.1:0"}, []ServiceRegistrar{registrar})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	conn, err := grpc.NewClient(s.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("建连失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	callErr := conn.Invoke(ctx, "/grpcserver.test.Failing/Fail",
		&healthpb.HealthCheckRequest{}, &healthpb.HealthCheckResponse{})

	st, ok := status.FromError(callErr)
	if !ok {
		t.Fatalf("返回的不是 gRPC status: %v", callErr)
	}
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal（说明默认拦截器没有生效，客户端会看到 Unknown）", st.Code())
	}
	if strings.Contains(st.Message(), "connect: connection refused") {
		t.Errorf("message = %q，cause 里的底层原因不该发给外部客户端", st.Message())
	}
}

func TestInnerInterceptorErrorsAreTranslated(t *testing.T) {
	// 在 handler 之前就拒绝请求的拦截器（鉴权、配额之类）返回的框架错误，
	// 也必须被 ErrorMapper 翻译，而不是绕过翻译以 Unknown 加完整 Error()
	// 内容（含 cause）抵达客户端。
	auth := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo,
		_ grpc.UnaryHandler) (any, error) {
		return nil, transport.Unauthenticated("NO_TOKEN", "缺少凭证").
			WithCause(errors.New("jwt: 内部细节 secret=s3cr3t"))
	}

	s := New(Config{Name: "grpcserver.test", Addr: "127.0.0.1:0"}, nil,
		WithInnerUnaryInterceptor(auth))
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	conn, err := grpc.NewClient(s.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("建连失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, callErr := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})

	st, ok := status.FromError(callErr)
	if !ok {
		t.Fatalf("返回的不是 gRPC status: %v", callErr)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated（内层拦截器的错误没有被翻译）", st.Code())
	}
	if st.Message() != "NO_TOKEN: 缺少凭证" {
		t.Errorf("message = %q, want %q", st.Message(), "NO_TOKEN: 缺少凭证")
	}
	if strings.Contains(st.Message(), "secret=s3cr3t") {
		t.Errorf("message = %q，cause 里的敏感信息不该发给外部客户端", st.Message())
	}
}
