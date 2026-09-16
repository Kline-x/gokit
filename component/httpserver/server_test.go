package httpserver

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func newTestServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	mux := http.NewServeMux()
	// 方法路由是 Go 1.22 起的标准库能力，这里顺带验证工具链版本。
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "pong")
	})
	return New(Config{Addr: "127.0.0.1:0"}, mux, opts...)
}

func TestServerServesRequests(t *testing.T) {
	s := newTestServer(t)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	if s.Addr() == nil {
		t.Fatal("Addr() = nil，Start 之后应能拿到真实监听地址")
	}

	resp, err := http.Get("http://" + s.Addr().String() + "/ping")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "pong" {
		t.Errorf("body = %q, want %q", string(body), "pong")
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

	client := &http.Client{Timeout: time.Second}
	if _, err := client.Get("http://" + addr + "/ping"); err == nil {
		t.Error("Stop 之后请求仍然成功，服务未真正关闭")
	}
}

func TestStartFailsOnOccupiedAddress(t *testing.T) {
	first := newTestServer(t)
	if err := first.Start(context.Background()); err != nil {
		t.Fatalf("第一个 Start() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Stop(context.Background()) })

	mux := http.NewServeMux()
	second := New(Config{Addr: first.Addr().String()}, mux)
	if err := second.Start(context.Background()); err == nil {
		_ = second.Stop(context.Background())
		t.Fatal("Start() error = nil, want 端口占用错误")
	}
}

func TestServerName(t *testing.T) {
	if got := newTestServer(t).Name(); got != "httpserver" {
		t.Errorf("Name() = %q, want %q", got, "httpserver")
	}
}

// TestTwoServersCanCarryDifferentNames 证明同一进程里可以用不同的 Name
// 区分两个 httpserver 实例（例如业务 API 与 metrics/pprof 各占一个端口），
// 从而能在 App 中共存而不触发内核的重名校验。内核层的校验本身不在本包职责内，
// 这里只断言 Name() 本身可配置且互不相同。
func TestTwoServersCanCarryDifferentNames(t *testing.T) {
	api := New(Config{Name: "httpserver.api", Addr: "127.0.0.1:0"}, http.NewServeMux())
	metrics := New(Config{Name: "httpserver.metrics", Addr: "127.0.0.1:0"}, http.NewServeMux())

	if api.Name() == metrics.Name() {
		t.Fatalf("两个实例的 Name 相同：%q", api.Name())
	}
	if api.Name() != "httpserver.api" {
		t.Errorf("Name() = %q, want %q", api.Name(), "httpserver.api")
	}
}

func TestDefaultNameIsUsedWhenEmpty(t *testing.T) {
	s := New(Config{Addr: "127.0.0.1:0"}, http.NewServeMux())
	if s.Name() != "httpserver" {
		t.Errorf("Name() = %q, want %q", s.Name(), "httpserver")
	}
}
