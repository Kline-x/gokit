//go:build wireinject

package main

import (
	"github.com/google/wire"

	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter"
)

// initApp 由 wire 在编译期生成实现：按依赖关系把各组件与业务模块装配起来。
// 换掉 greeter.LocalSet 就能把该模块切成远程调用，这里是唯一需要改的地方。
func initApp(cfg Config) (*Bundle, error) {
	panic(wire.Build(
		provideLogConfig,
		provideHTTPConfig,
		provideDBConfig,
		log.New,
		provideApp,
		provideHandler,
		provideHTTPServer,
		greeter.LocalSet,
		wire.Struct(new(Bundle), "*"),
	))
}
