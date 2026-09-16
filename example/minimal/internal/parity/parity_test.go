// Package parity 只有测试：它把同一组断言分别打到问候模块的本地实现
// 与远程实现上，两边都必须通过。
//
// 这是整套分层想换来的东西的字面验证 ——「模块拆出去，调用方代码不动」。
// 如果哪天这个测试在 remote 那一栏挂了，说明拆分的承诺破了。
package parity

import (
	"context"
	"errors"
	"io"
	"log/slog"
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

// fixture 是一次对等检查需要的全部东西：一个 Service，以及它背后的库。
//
// 拿着库是为了能验证「东西确实写进去了、确实是读出来的」——
// 没有它，断言只能看 Greet 的返回值，而返回值是名字的纯函数，
// 就算写库整个坏掉也看不出来。
type fixture struct {
	svc application.Service
	db  *sqldb.DB
}

// newLocalService 造一个本地实现，连一个独立的内存库。
func newLocalService(t *testing.T, dsn string) fixture {
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

	repo := infrastructure.NewGreetingRepo(db)
	return fixture{svc: application.NewLocalService(repo, db), db: db}
}

// newRemoteService 起一个真的 gRPC 服务，再造一个连过去的远程实现。
//
// db 是服务端那份库的句柄：对 remote 调用方来说库藏在另一个进程里，
// 但测试跑在同一个进程内，可以直接拿到它，用来验证写没写进去。
func newRemoteService(t *testing.T, dsn string) fixture {
	t.Helper()

	local := newLocalService(t, dsn)

	srv := grpcserver.New(
		grpcserver.Config{Name: "grpcserver.parity", Addr: "127.0.0.1:0"},
		[]grpcserver.ServiceRegistrar{interfaces.NewGRPCHandler(local.svc)},
		grpcserver.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("起 gRPC 服务失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(ctx) })

	client, err := grpcclient.New(
		grpcclient.Config{
			Name:        "grpcclient.parity",
			Target:      srv.Addr().String(),
			Block:       true,
			DialTimeout: 5 * time.Second,
		},
		grpcclient.WithUnaryInterceptor(grpcclient.ErrorRestorer()),
	)
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatalf("建连失败: %v", err)
	}
	t.Cleanup(func() { _ = client.Stop(ctx) })

	return fixture{svc: remote.NewService(client), db: local.db}
}

// TestServiceBehavesIdenticallyLocalAndRemote 是本计划存在的理由。
//
// 同一组断言跑两遍：一遍打本地实现，一遍打跨进程的远程实现。
// 断言里没有任何一句在区分「这是本地还是远程」——调用方本来就不该知道。
func TestServiceBehavesIdenticallyLocalAndRemote(t *testing.T) {
	cases := []struct {
		name string
		make func(t *testing.T, dsn string) fixture
	}{
		{
			name: "local",
			make: newLocalService,
		},
		{
			name: "remote",
			make: newRemoteService,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.make(t, "file:parity_"+tc.name+"?mode=memory&cache=shared")
			svc := f.svc
			ctx := context.Background()

			// 一、正常路径：按公式生成的问候语要对。
			first, err := svc.Greet(ctx, application.GreetRequest{Name: "gokit"})
			if err != nil {
				t.Fatalf("Greet() error = %v", err)
			}
			if first.Text != "你好，gokit" {
				t.Errorf("Text = %q, want %q", first.Text, "你好，gokit")
			}

			// 二、读路径：先直接往库里塞一条与公式不同的记录，再问同一个名字。
			// 必须拿到塞进去的那条，而不是按公式现算的 ——
			// 这才证明它真的去读了存储。
			const presetName = "preset"
			const presetText = "这是预置的问候语，不是算出来的"
			if _, execErr := f.db.ExecContext(ctx,
				`INSERT INTO greetings(name, text) VALUES(?, ?)`,
				presetName, presetText); execErr != nil {
				t.Fatalf("预置记录失败: %v", execErr)
			}

			preset, err := svc.Greet(ctx, application.GreetRequest{Name: presetName})
			if err != nil {
				t.Fatalf("Greet(preset) error = %v", err)
			}
			if preset.Text != presetText {
				t.Errorf("Text = %q, want %q（应当读存储里的那条，而不是按公式现算）",
					preset.Text, presetText)
			}

			// 三、写路径：换个新名字问两次，库里只该有一条。
			// 第一次写入、第二次命中已有记录，都要真的发生。
			const freshName = "fresh"
			for i := 0; i < 2; i++ {
				if _, err := svc.Greet(ctx, application.GreetRequest{Name: freshName}); err != nil {
					t.Fatalf("第 %d 次 Greet(fresh) error = %v", i+1, err)
				}
			}

			var count int
			row := f.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM greetings WHERE name = ?`, freshName)
			if err := row.Scan(&count); err != nil {
				t.Fatalf("统计失败: %v", err)
			}
			if count != 1 {
				t.Errorf("greetings 里 %q 有 %d 条, want 1（第一次要写进去，第二次不该重复写）",
					freshName, count)
			}

			// 四、错误路径：判断错误的写法两边必须完全一样。
			// 这里用的哨兵只带 Code 与 Reason，不带描述 ——
			// transport.Error 的 Is 正是按这两项匹配的。
			sentinel := transport.InvalidArgument("NAME_REQUIRED", "")

			_, err = svc.Greet(ctx, application.GreetRequest{Name: ""})
			if err == nil {
				t.Fatal("空名字应当报错")
			}
			if !errors.Is(err, sentinel) {
				t.Errorf("errors.Is 没认出这是参数不合法: %v", err)
			}
			if got := transport.Code(err); got != transport.CodeInvalidArgument {
				t.Errorf("Code = %d, want %d", got, transport.CodeInvalidArgument)
			}

			// 错误里的结构化信息也要过得去。
			var te *transport.Error
			if !errors.As(err, &te) {
				t.Fatalf("取不出 *transport.Error: %v", err)
			}
			if te.Metadata["field"] != "name" {
				t.Errorf("Metadata = %v, want field=name", te.Metadata)
			}

			// 五、内部故障不能把底层细节带给调用方。
			// 关掉库，再问一次：应当拿到内部错误码与一句泛化描述，
			// 而不是驱动或 SQL 的原文。这一条在本地与远程两边都必须成立 ——
			// 远程那边尤其要紧，因为泄漏出去的是给外部客户端看的。
			if err := f.db.Stop(ctx); err != nil {
				t.Fatalf("关闭数据库失败: %v", err)
			}

			_, err = svc.Greet(ctx, application.GreetRequest{Name: "afterclose"})
			if err == nil {
				t.Fatal("库已经关了，Greet 不该成功")
			}
			if got := transport.Code(err); got != transport.CodeInternal {
				t.Errorf("Code = %d, want %d", got, transport.CodeInternal)
			}
			for _, leak := range []string{"sql", "database", "greetings", "SELECT", "INSERT"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("错误里漏出了底层细节 %q: %v", leak, err)
				}
			}
		})
	}
}
