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

// newLocalService 造一个本地实现，连一个独立的内存库。
func newLocalService(t *testing.T, dsn string) (application.Service, *sqldb.DB) {
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
	return application.NewLocalService(repo, db), db
}

// newRemoteService 起一个真的 gRPC 服务，再造一个连过去的远程实现。
func newRemoteService(t *testing.T, dsn string) application.Service {
	t.Helper()

	local, _ := newLocalService(t, dsn)

	srv := grpcserver.New(
		grpcserver.Config{Name: "grpcserver.parity", Addr: "127.0.0.1:0"},
		[]grpcserver.ServiceRegistrar{interfaces.NewGRPCHandler(local)},
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

	return remote.NewService(client)
}

// TestServiceBehavesIdenticallyLocalAndRemote 是本计划存在的理由。
//
// 同一组断言跑两遍：一遍打本地实现，一遍打跨进程的远程实现。
// 断言里没有任何一句在区分「这是本地还是远程」——调用方本来就不该知道。
func TestServiceBehavesIdenticallyLocalAndRemote(t *testing.T) {
	cases := []struct {
		name string
		make func(t *testing.T, dsn string) application.Service
	}{
		{
			name: "local",
			make: func(t *testing.T, dsn string) application.Service {
				svc, _ := newLocalService(t, dsn)
				return svc
			},
		},
		{
			name: "remote",
			make: newRemoteService,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := tc.make(t, "file:parity_"+tc.name+"?mode=memory&cache=shared")
			ctx := context.Background()

			// 一、正常路径：两次调用应当返回同一份结果。
			first, err := svc.Greet(ctx, application.GreetRequest{Name: "gokit"})
			if err != nil {
				t.Fatalf("Greet() error = %v", err)
			}
			if first.Text != "你好，gokit" {
				t.Errorf("Text = %q, want %q", first.Text, "你好，gokit")
			}

			second, err := svc.Greet(ctx, application.GreetRequest{Name: "gokit"})
			if err != nil {
				t.Fatalf("第二次 Greet() error = %v", err)
			}
			if second.Text != first.Text {
				t.Errorf("两次结果不一致: %q vs %q", first.Text, second.Text)
			}

			// 二、错误路径：判断错误的写法两边必须完全一样。
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

			// 三、错误里的结构化信息也要过得去。
			var te *transport.Error
			if !errors.As(err, &te) {
				t.Fatalf("取不出 *transport.Error: %v", err)
			}
			if te.Metadata["field"] != "name" {
				t.Errorf("Metadata = %v, want field=name", te.Metadata)
			}
		})
	}
}
