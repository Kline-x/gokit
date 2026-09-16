package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Kline-x/gokit/component/sqldb"
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

		var reply map[string]string
		if err := json.Unmarshal(body, &reply); err != nil {
			t.Fatalf("响应不是合法 JSON: %v, 内容=%s", err, body)
		}
		if reply["text"] != "你好，gokit" {
			t.Errorf("text = %q, want %q", reply["text"], "你好，gokit")
		}
	}

	// Bundle 不再单列 DB 字段（哪些基础设施组件存在由 provideComponents 决定），
	// 从托管组件列表里按类型取出数据库组件来做断言。
	var db *sqldb.DB
	for _, c := range b.Components {
		if d, ok := c.(*sqldb.DB); ok {
			db = d
			break
		}
	}
	if db == nil {
		t.Fatalf("Components 中未找到 *sqldb.DB")
	}

	var count int
	row := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM greetings WHERE name = ?`, "gokit")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 1 {
		t.Errorf("greetings 行数 = %d, want 1（第二次请求不应重复写入）", count)
	}
}
