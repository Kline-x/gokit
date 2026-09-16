//go:build wireinject

package main

import (
	"github.com/google/wire"

	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/component/sqldb"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/application"
)

// initApp 由 wire 在编译期生成实现：按依赖关系把各组件与业务模块装配起来。
//
// 本文件与 provideComponents 一起构成组合根。把 greeter.LocalSet 换成将来的
// RemoteSet，就能把该模块切成远程调用；同时要改的还有 provideComponents 的入参，
// 以及 wire.Build 里那些只为这个模块服务、换成远程后不再需要的共享基础设施。
// 业务代码——domain、application、interfaces 三层——一行都不用动，
// 这才是这套分层想换来的东西。
func initApp(cfg Config) (*Bundle, error) {
	panic(wire.Build(
		provideLogConfig,
		provideHTTPConfig,
		provideGRPCConfig,
		provideDBConfig,
		log.New,
		provideApp,
		provideHandler,
		provideHTTPServer,
		provideServiceRegistrars,
		provideGRPCServer,
		provideComponents,
		sqldb.New,
		// 事务能力由共享的数据库组件提供。这个绑定放在组合根而不是业务模块里，
		// 因为它把应用层的接口接到了全应用共享的基础设施上。
		wire.Bind(new(application.Transactor), new(*sqldb.DB)),
		greeter.LocalSet,
		wire.Struct(new(Bundle), "*"),
	))
}
