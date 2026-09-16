//go:build wireinject

package main

import (
	"github.com/google/wire"

	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter"
)

// initApp 装配入口服务。
//
// 与 cmd/monolith 的装配比一比，差别只有两处：
// 这里用 greeter.RemoteSet 而不是 LocalSet，以及多提供一条 gRPC 连接。
// domain、application、interfaces 三层的代码一行没动。
func initApp(cfg Config) (*Bundle, error) {
	panic(wire.Build(
		provideLogConfig,
		provideHTTPConfig,
		provideGreeterConfig,
		log.New,
		provideGreeterClient,
		provideApp,
		provideHandler,
		provideHTTPServer,
		provideComponents,
		greeter.RemoteSet,
		wire.Struct(new(Bundle), "*"),
	))
}
