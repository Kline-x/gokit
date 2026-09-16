package grpcserver

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

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
