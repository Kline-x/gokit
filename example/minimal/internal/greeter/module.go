// Package greeter 是问候模块的装配入口。
//
// 模块对外只暴露两样东西：application.Service 接口，以及这里的装配集合。
// 模块独立成服务时，把 internal/greeter 整个目录搬走即可。
package greeter

import (
	"github.com/google/wire"

	"github.com/Kline-x/gokit/example/minimal/internal/greeter/application"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/domain"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/infrastructure"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/interfaces"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/remote"
)

// LocalSet 是单体部署下的装配集合：Service 绑定到进程内实现。
//
// 本模块拆成独立服务后，另写一个 RemoteSet 把 application.Service
// 绑定到 gRPC 客户端实现，调用方依赖的仍是同一个接口，代码无需改动。
//
// 注意这里**不**提供 *sqldb.DB：数据库是整个应用共享的基础设施，
// 由组合根 wire.go 统一提供。业务模块只声明自己需要什么，
// 不负责创建共享资源——否则第二个模块也需要数据库时就会与本模块冲突。
var LocalSet = wire.NewSet(
	infrastructure.NewGreetingRepo,
	wire.Bind(new(domain.Repository), new(*infrastructure.GreetingRepo)),

	infrastructure.NewMigrator,

	application.NewLocalService,
	wire.Bind(new(application.Service), new(*application.LocalService)),

	interfaces.NewHTTPHandler,
	interfaces.NewGRPCHandler,
)

// RemoteSet 是模块拆成独立服务后，**调用方**使用的装配集合。
//
// 它把 application.Service 绑到 gRPC 客户端实现上。与 LocalSet 相比：
// 不需要仓储、不需要迁移、不需要数据库，因为那些都跟着服务走了；
// 也不需要 gRPC 接口层，因为调用方不对外提供这个服务。
// 需要的只有一条到下游的连接，由调用方在组合根里提供。
//
// 换掉 LocalSet 这件事，对 domain、application、interfaces 三层完全不可见——
// 这正是这套分层想换来的东西。
var RemoteSet = wire.NewSet(
	remote.NewService,
	wire.Bind(new(application.Service), new(*remote.Service)),

	interfaces.NewHTTPHandler,
)
