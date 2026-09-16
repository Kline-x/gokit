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

			// 四、取消与超时在两边必须给出同样的判断依据。
			// 调用方写 errors.Is(err, context.Canceled) 是很常见的做法，
			// 拆分之后它不能悄悄失效。
			canceledCtx, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := svc.Greet(canceledCtx, application.GreetRequest{Name: "canceled"}); err == nil {
				t.Error("上下文已取消，Greet 不该成功")
			} else if !errors.Is(err, context.Canceled) {
				t.Errorf("errors.Is 认不出 context.Canceled: %v", err)
			}

			expiredCtx, stop := context.WithTimeout(ctx, time.Nanosecond)
			defer stop()
			time.Sleep(time.Millisecond)
			if _, err := svc.Greet(expiredCtx, application.GreetRequest{Name: "expired"}); err == nil {
				t.Error("上下文已超时，Greet 不该成功")
			} else {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("errors.Is 认不出 context.DeadlineExceeded: %v", err)
				}
				if got := transport.Code(err); got != transport.CodeTimeout {
					t.Errorf("超时的 Code = %d, want %d", got, transport.CodeTimeout)
				}
			}

			// 五、内部故障不能把底层细节带给调用方。
			// 关掉库，再问一次。
			//
			// 检查的是「假如要发给客户端，会发出去什么」，也就是 transport.FromError
			// 归一之后的 Message —— HTTP 的 RenderError 与 gRPC 的 ErrorMapper
			// 都是经它产出对外内容的。
			//
			// 刻意不去查 err.Error()：那两边本来就不一样，而且是应该不一样的。
			// 本地拿到的是完整的错误链（同一个进程内，没跨信任边界，留着好排障），
			// 远程拿到的是已经在服务端脱过敏、又在客户端还原出来的那一个。
			// 框架承诺的是 Code、Reason、Metadata 与 errors.Is 一致，
			// 不是错误字符串一致 —— 调用方本来就不该去解析 Error()。
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

			// 这条断言在 local 一栏是空转的：sqldb 在这条路径上返回的是
			// fmt.Errorf 包出来的普通 error，FromError 对它给出的 Message
			// 是写死的字面量，编译期就能确定不含下面任何一个关键词，测不出
			// 任何回归。它真正有意义的是 remote 一栏——那里的 Message 经过
			// 服务端 ErrorMapper 脱敏、又被客户端 ErrorRestorer 还原，能捕住
			// ErrorMapper 哪天不小心把底层细节漏出去。留着它只是为了让两栏
			// 跑同一段断言，不要从「local 也过了」里读出它验证了什么对称性。
			outward := transport.FromError(err).Message
			for _, leak := range []string{"sql", "database", "greetings", "SELECT", "INSERT"} {
				if strings.Contains(outward, leak) {
					t.Errorf("要发给客户端的描述里漏出了底层细节 %q: %q", leak, outward)
				}
			}
		})
	}
}
