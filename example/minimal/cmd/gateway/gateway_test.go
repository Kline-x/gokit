package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Kline-x/gokit/component/grpcclient"
	"github.com/Kline-x/gokit/component/grpcserver"
	"github.com/Kline-x/gokit/component/sqldb"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/application"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/infrastructure"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/interfaces"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/remote"
	"github.com/Kline-x/gokit/transport"
)

// 本文件验证的是网关自己的组合根（initApp/wire_gen.go），而不是框架本身。
// internal/parity 已经证明了框架对本地与远程实现一视同仁；这里要证明的是
// cmd/gateway 真的把自己接到了那份契约上——曾经有人从 provideGreeterClient
// 里删掉 ErrorRestorer，parity 测试仍然全绿，只有真正跑通网关自己的装配
// 才会发现 Reason、Metadata、errors.Is 全部丢失。

// startGreeterBackend 起一个真的问候服务，供网关的 initApp 连接。
//
// 搭法照抄 internal/parity 的 newRemoteService：真实的 gRPC handler，
// 背后是真实的本地 Service 与一个内存 SQLite 库。不能从 _test.go 里
// import，所以这里最小重写一份，只留网关测试需要的部分。
func startGreeterBackend(t *testing.T, dsn string) string {
	t.Helper()

	cfg := sqldb.DefaultConfig()
	cfg.Driver = "sqlite"
	cfg.DSN = dsn

	db, err := sqldb.New(cfg)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Stop(context.Background()) })

	ctx := context.Background()
	if err := db.Start(ctx); err != nil {
		t.Fatalf("数据库探活失败: %v", err)
	}
	if err := infrastructure.Migrate(ctx, db); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	svc := application.NewLocalService(infrastructure.NewGreetingRepo(db), db)

	srv := grpcserver.New(
		grpcserver.Config{Name: "grpcserver.gatewaytest", Addr: "127.0.0.1:0"},
		[]grpcserver.ServiceRegistrar{interfaces.NewGRPCHandler(svc)},
		grpcserver.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("起 gRPC 服务失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(ctx) })

	return srv.Addr().String()
}

// findGreeterClient 从托管组件列表里按类型取出 gRPC 客户端组件。
// Bundle 不单列这个字段，用法与 monolith 测试里的 findDB 一致。
func findGreeterClient(t *testing.T, b *Bundle) *grpcclient.Client {
	t.Helper()
	for _, c := range b.Components {
		if gc, ok := c.(*grpcclient.Client); ok {
			return gc
		}
	}
	t.Fatalf("Components 中未找到 *grpcclient.Client")
	return nil
}

// TestGatewayGreetHappyPath 用网关自己的 initApp 走一遍正常路径，
// 并且直接调用网关 wire 出来的 Service，验证业务错误的身份
// （Code、Reason、Metadata、errors.Is）在真实装配下也保得住。
func TestGatewayGreetHappyPath(t *testing.T) {
	backendAddr := startGreeterBackend(t, "file:gatewaytest_happy?mode=memory&cache=shared")

	cfg := defaultConfig()
	cfg.HTTP.Addr = "127.0.0.1:0"
	cfg.Greeter.Target = backendAddr
	cfg.Log.Output = filepath.Join(t.TempDir(), "app.log")

	b, err := initApp(cfg)
	if err != nil {
		t.Fatalf("initApp() error = %v", err)
	}

	ctx := context.Background()
	a := b.Register()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(ctx) })

	httpClient := &http.Client{Timeout: 5 * time.Second}

	resp, err := httpClient.Get("http://" + b.HTTP.Addr().String() + "/greet/gokit")
	if err != nil {
		t.Fatalf("HTTP 请求失败: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, body)
	}

	var envelope struct {
		Code int               `json:"code"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("响应不是合法 JSON: %v, 内容=%s", err, body)
	}
	if envelope.Data["text"] != "你好，gokit" {
		t.Errorf("text = %q, want %q", envelope.Data["text"], "你好，gokit")
	}

	// 空名字在 HTTP 这条路由上造不出来（Go 1.22 的 ServeMux 通配符段不匹配
	// 空路径段），所以直接调用网关自己 wire 出来的 Service——从 Bundle 的
	// 托管组件里取出真实的 *grpcclient.Client，用它构造 remote.Service，
	// 这与 wire_gen.go 里实际装配出来的是同一条路径。
	client := findGreeterClient(t, b)
	svc := remote.NewService(client)

	_, err = svc.Greet(ctx, application.GreetRequest{Name: ""})
	if err == nil {
		t.Fatal("空名字应当报错")
	}
	sentinel := transport.InvalidArgument("NAME_REQUIRED", "")
	if !errors.Is(err, sentinel) {
		t.Errorf("errors.Is 没认出这是参数不合法: %v", err)
	}
	var te *transport.Error
	if !errors.As(err, &te) {
		t.Fatalf("取不出 *transport.Error: %v", err)
	}
	if te.Metadata["field"] != "name" {
		t.Errorf("Metadata = %v, want field=name", te.Metadata)
	}
}

// TestGatewayGreetColdStartDoesNotLeakTransportDetails 验证下游还没起来时
// （或者压根连不上）网关不会把地址、建连失败原文这类传输层内情带给客户端。
//
// 这是曾经的 Critical：gRPC 自己产生的传输层错误没有 ErrorInfo detail，
// 也从没脱过敏；ErrorRestorer 一度把 status 文本直接当成对客户端可见的
// Message。现在没有 detail 时一律给泛化描述，原文只留在 cause 上进日志。
func TestGatewayGreetColdStartDoesNotLeakTransportDetails(t *testing.T) {
	cfg := defaultConfig()
	cfg.HTTP.Addr = "127.0.0.1:0"
	// 这个地址上没有任何服务在监听：block:false，Start 不等它，
	// 请求会在真正发起 RPC 时才发现连不上。
	cfg.Greeter.Target = "127.0.0.1:1"
	cfg.Log.Output = filepath.Join(t.TempDir(), "app.log")

	b, err := initApp(cfg)
	if err != nil {
		t.Fatalf("initApp() error = %v", err)
	}

	ctx := context.Background()
	a := b.Register()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(ctx) })

	httpClient := &http.Client{Timeout: 10 * time.Second}

	resp, err := httpClient.Get("http://" + b.HTTP.Addr().String() + "/greet/x")
	if err != nil {
		t.Fatalf("HTTP 请求失败: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	bodyStr := string(body)
	for _, leak := range []string{"127.0.0.1", "dial tcp", "transport: Error"} {
		if strings.Contains(bodyStr, leak) {
			t.Errorf("响应里漏出了底层传输细节 %q: %q", leak, bodyStr)
		}
	}
}
