package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/Kline-x/gokit/component/grpcclient"
	"github.com/Kline-x/gokit/component/sqldb"
	greeterv1 "github.com/Kline-x/gokit/example/minimal/api/greeter/v1"
	"github.com/Kline-x/gokit/transport"
)

// 端到端验证：配置 → wire 装配 → App 启停 → HTTP 请求 → 分层调用 → SQLite 落库。
func TestGreetEndToEnd(t *testing.T) {
	cfg := defaultConfig()
	cfg.HTTP.Addr = "127.0.0.1:0"
	// 内存库靠 cache=shared 在连接之间共享，只要连接池里还有活连接就不会消失。
	// 这依赖 sqldb 默认的 MaxIdleConns 大于 0——若把空闲连接数调成 0，
	// migrator 用完的连接会被立刻关掉，后续请求将看不到这张表。
	cfg.DB.DSN = "file:e2e?mode=memory&cache=shared"
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

	url := "http://" + b.HTTP.Addr().String() + "/greet/gokit"

	// 第一次请求会生成并落库，第二次应命中已有记录，两次结果必须一致。
	for i := 0; i < 2; i++ {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("第 %d 次请求失败: %v", i+1, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("读取响应失败: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("第 %d 次 status = %d, want 200, body=%s", i+1, resp.StatusCode, body)
		}

		// 响应现在是统一信封 {"code":0,"data":{"text":"..."}}，而不是裸的
		// {"text":"..."}——这是 Step 4 改用 transport.Render 之后的预期变化。
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
	}

	db := findDB(t, b)

	var count int
	row := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM greetings WHERE name = ?`, "gokit")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 1 {
		t.Errorf("greetings 行数 = %d, want 1（第二次请求不应重复写入）", count)
	}
}

// findDB 从托管组件列表里按类型取出数据库组件。
// Bundle 不再单列 DB 字段（哪些基础设施组件存在由 provideComponents 决定），
// 这是各测试共用的查找方式。
func findDB(t *testing.T, b *Bundle) *sqldb.DB {
	t.Helper()
	for _, c := range b.Components {
		if d, ok := c.(*sqldb.DB); ok {
			return d
		}
	}
	t.Fatalf("Components 中未找到 *sqldb.DB")
	return nil
}

// 双协议端到端验证：同一个 application.Service 分别以 gRPC 与 HTTP 调用，
// 必须返回同一份数据，且只在数据库里落一行。
func TestGreetOverGRPC(t *testing.T) {
	cfg := defaultConfig()
	cfg.HTTP.Addr = "127.0.0.1:0"
	cfg.GRPC.Addr = "127.0.0.1:0"
	cfg.DB.DSN = "file:e2egrpc?mode=memory&cache=shared"
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

	conn, err := grpc.NewClient(b.GRPC.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcclient.ErrorRestorer()))
	if err != nil {
		t.Fatalf("建连失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := greeterv1.NewGreeterClient(conn)

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	reply, err := client.Greet(callCtx, &greeterv1.GreetRequest{Name: "gokit"})
	if err != nil {
		t.Fatalf("Greet() error = %v", err)
	}
	if reply.GetText() != "你好，gokit" {
		t.Errorf("text = %q, want %q", reply.GetText(), "你好，gokit")
	}

	// 同一个名字再走一次 HTTP，两条协议必须返回同一份数据。
	resp, err := http.Get("http://" + b.HTTP.Addr().String() + "/greet/gokit")
	if err != nil {
		t.Fatalf("HTTP 请求失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	var envelope struct {
		Code int               `json:"code"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("HTTP 响应不是合法 JSON: %v, 内容=%s", err, body)
	}
	if envelope.Data["text"] != reply.GetText() {
		t.Errorf("两条协议返回不一致: HTTP=%q gRPC=%q", envelope.Data["text"], reply.GetText())
	}

	// 只应落一行库，说明两次调用走的是同一套业务逻辑与同一张表。
	var count int
	db := findDB(t, b)
	row := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM greetings WHERE name = ?`, "gokit")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 1 {
		t.Errorf("greetings 行数 = %d, want 1", count)
	}
}

// 错误语义一致性验证：空名字在 HTTP 与 gRPC 两条协议上都必须是「参数不合法」，
// 而不是内部错误。Go 1.22 的 ServeMux 通配符段不匹配空路径段，
// HTTP 侧无法构造出空名字请求，这是路由层面的事实，本测试只在 gRPC 侧验证。
func TestGreetErrorSemanticsMatchAcrossProtocols(t *testing.T) {
	cfg := defaultConfig()
	cfg.HTTP.Addr = "127.0.0.1:0"
	cfg.GRPC.Addr = "127.0.0.1:0"
	cfg.DB.DSN = "file:e2eerr?mode=memory&cache=shared"
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

	// 空名字在两条协议上都应当是「参数不合法」，而不是内部错误。
	conn, err := grpc.NewClient(b.GRPC.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcclient.ErrorRestorer()))
	if err != nil {
		t.Fatalf("建连失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, grpcErr := greeterv1.NewGreeterClient(conn).Greet(callCtx, &greeterv1.GreetRequest{Name: ""})
	if grpcErr == nil {
		t.Fatal("空名字的 gRPC 调用应当失败")
	}

	var te *transport.Error
	if !errors.As(grpcErr, &te) {
		t.Fatalf("客户端拦截器未把错误还原成 transport.Error: %v", grpcErr)
	}
	if te.Code != transport.CodeInvalidArgument {
		t.Errorf("gRPC 侧 Code = %d, want %d", te.Code, transport.CodeInvalidArgument)
	}
	if te.Reason != "NAME_REQUIRED" {
		t.Errorf("gRPC 侧 Reason = %q, want %q", te.Reason, "NAME_REQUIRED")
	}
}
