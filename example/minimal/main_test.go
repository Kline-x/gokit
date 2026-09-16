package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Kline-x/gokit/example/minimal/internal/greeter/infrastructure"
)

// 端到端验证：配置 → wire 装配 → App 启停 → HTTP 请求 → 分层调用 → SQLite 落库。
func TestGreetEndToEnd(t *testing.T) {
	cfg := defaultConfig()
	cfg.HTTP.Addr = "127.0.0.1:0"
	cfg.DB.DSN = "file:e2e?mode=memory&cache=shared"
	cfg.Log.Output = filepath.Join(t.TempDir(), "app.log")

	b, err := initApp(cfg)
	if err != nil {
		t.Fatalf("initApp() error = %v", err)
	}

	ctx := context.Background()
	if err := infrastructure.Migrate(ctx, b.DB); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

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

		var reply map[string]string
		if err := json.Unmarshal(body, &reply); err != nil {
			t.Fatalf("响应不是合法 JSON: %v, 内容=%s", err, body)
		}
		if reply["text"] != "你好，gokit" {
			t.Errorf("text = %q, want %q", reply["text"], "你好，gokit")
		}
	}

	var count int
	row := b.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM greetings WHERE name = ?`, "gokit")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 1 {
		t.Errorf("greetings 行数 = %d, want 1（第二次请求不应重复写入）", count)
	}
}
