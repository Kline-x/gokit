//go:build wireinject

package main

import (
	"github.com/google/wire"

	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/component/sqldb"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/application"
)

// initApp 装配问候服务。
//
// 用的是 greeter.LocalSet —— 本进程就是这个模块的家，
// 仓储、建表、业务实现全在这里。
func initApp(cfg Config) (*Bundle, error) {
	panic(wire.Build(
		provideLogConfig,
		provideGRPCConfig,
		provideDBConfig,
		log.New,
		sqldb.New,
		wire.Bind(new(application.Transactor), new(*sqldb.DB)),
		provideApp,
		provideServiceRegistrars,
		provideGRPCServer,
		provideComponents,
		greeter.LocalSet,
		wire.Struct(new(Bundle), "*"),
	))
}
