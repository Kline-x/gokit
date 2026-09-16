// Package greeter 是问候模块的装配入口。
//
// 模块对外只暴露两样东西：application.Service 接口，以及这里的装配集合。
// 模块独立成服务时，把 internal/greeter 整个目录搬走即可。
package greeter

import (
	"github.com/google/wire"

	"github.com/Kline-x/gokit/component/sqldb"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/application"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/domain"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/infrastructure"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/interfaces"
)

// LocalSet 是单体部署下的装配集合：Service 绑定到进程内实现。
//
// 本模块拆成独立服务后，另写一个 RemoteSet 把 application.Service
// 绑定到 gRPC 客户端实现，调用方依赖的仍是同一个接口，代码无需改动。
var LocalSet = wire.NewSet(
	sqldb.New,

	infrastructure.NewGreetingRepo,
	wire.Bind(new(domain.Repository), new(*infrastructure.GreetingRepo)),

	wire.Bind(new(application.Transactor), new(*sqldb.DB)),

	application.NewLocalService,
	wire.Bind(new(application.Service), new(*application.LocalService)),

	interfaces.NewHTTPHandler,
)
