# gokit

gokit 是一个 Go 应用框架库：基础设施组件化，每个组件（日志、HTTP 服务、数据库……）都实现同一套生命周期契约，由一个 `App` 统一负责启动与停止的编排。业务方引入 gokit 后，只需实现或复用组件、把它们注册进 `App`，不需要自己管理启动顺序、优雅退出和信号处理。

## 版本状态

当前是 **v0.1.0**，0.x 意味着 API 尚未稳定：组件的选项、配置字段与 `transport` 的错误契约都还可能变。已有的每一处变动都由测试与评审盯着，但升级小版本时请看一眼变更，不要假定平滑。

引入方式：

```bash
go get github.com/Kline-x/gokit@v0.1.0
```

Go 版本下限是 1.22。

## 快速开始

以下片段基于 `example/minimal/main.go` 精简而来，展示构造一个日志组件、一个 HTTP 组件和一个 `App`，注册后运行的最小流程：

```go
package main

import (
	"context"
	"net/http"

	"github.com/Kline-x/gokit/app"
	"github.com/Kline-x/gokit/component/httpserver"
	"github.com/Kline-x/gokit/component/log"
)

func main() {
	logger, err := log.New(log.DefaultConfig())
	if err != nil {
		panic(err)
	}

	a := app.New(app.WithName("demo"), app.WithLogger(logger.Logger))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("pong"))
	})

	httpCfg := httpserver.DefaultConfig()
	srv := httpserver.New(httpCfg, mux, httpserver.WithFatal(a.Fatal))

	a.Register(logger, srv)

	if err := a.Run(context.Background()); err != nil {
		panic(err)
	}
}
```

`app.Run` 会依次启动已注册组件、阻塞等待，直到 `ctx` 取消、收到退出信号（默认监听 `SIGINT`/`SIGTERM`），或某个组件通过 `Fatal` 回调上报运行期错误，然后逆序停止所有组件。

真实项目通常还需要加载配置（`config` 包）、接入数据库（`component/sqldb`）、用 `wire` 做依赖注入，完整写法见 `example/minimal`。

## 写自己的组件

组件只需实现 `app.Component` 接口（`Name() string`、`Start(ctx) error`、`Stop(ctx) error`），有三条规则必须遵守：

1. **`Name()` 在同一个 `App` 内必须唯一**——`App` 用它做启动顺序、依赖声明（`Dependent` 接口）和日志标识，重名会互相覆盖。
2. **`Start` 必须快速返回**：像 HTTP、gRPC 这类需要长期运行、阻塞监听的组件，要在 `Start` 内部自己起一个 goroutine 去跑，`Start` 本身不能被服务本体的阻塞逻辑占住。
3. **运行期失败通过回调上报，而不是阻塞在 `Start` 里等错误发生**：组件在构造时接收一个"致命错误"回调（通常是组件自带的 `WithFatal` 选项），运行中出现无法恢复的问题时调用它；`App` 收到后会触发整体优雅退出。不要用阻塞、重试等方式在 `Start` 里死等这类错误。

## 包一览

| 包 | 说明 |
|---|---|
| `app` | 组件生命周期编排：注册、按依赖排序启动、逆序停止、信号处理、致命错误汇聚。 |
| `config` | 把 YAML 文件、环境变量、显式覆盖逐级合并进配置结构体。 |
| `component/log` | 基于标准库 `log/slog` 的日志组件，支持从 `context` 取当前请求的 logger。 |
| `component/httpserver` | 基于标准库 `net/http` 的 HTTP 服务组件，含请求日志、异常恢复等中间件。 |
| `component/sqldb` | 基于标准库 `database/sql` 的关系库组件，驱动由业务方自行引入注册。 |
| `transport` | 与协议无关的错误类型与错误码表，以及 HTTP 侧的统一响应。 |
| `component/grpcserver` | gRPC 服务组件，自带健康检查，默认装上 recover 与错误映射两个拦截器；反射默认关闭，按需在配置里打开。 |
| `component/grpcclient` | gRPC 客户端连接组件，把下游返回的 status 还原成框架错误。 |

## 统一错误语义

业务层只返回 `transport.Error`：一个错误码、一个稳定的机器可读 `Reason`、一句给人看的 `Message`。两条协议各自把它翻译成自己的表达：HTTP 侧由 `transport.RenderError` 翻成状态码，配上统一信封 `{code, reason, message, data}`；gRPC 侧由服务端拦截器翻成 status，客户端拦截器再把它还原回 `transport.Error`。

结果是调用方判断错误的写法（`errors.Is` 比对一个哨兵错误）在本地实现与远程实现下完全一致，这正是模块能从单体拆出去而调用方不用改代码的原因。

两点约定：`Reason` 只用大写字母、数字与下划线，gRPC 侧靠这个形状把它从 status 文本里认出来；未归类的错误只会把泛化描述发给客户端，底层原因留在服务端日志里。

## 更多

- 完整可运行示例：`example/minimal`（分层的问候模块，同一套服务同时通过 HTTP 与 gRPC 对外提供，数据落 SQLite）。示例里同一套业务代码有两种部署形态：`cmd/monolith` 是单体，所有模块本地实现；`cmd/greeter` 与 `cmd/gateway` 是拆成两个进程后的形态，`greeter` 独占数据库、只对外提供 gRPC，`gateway` 对外提供 HTTP、通过 gRPC 调用 `greeter`。两种形态之间只是换了一个 ProviderSet（`LocalSet` 换成 `RemoteSet`），`domain`、`application`、`interfaces` 三层代码完全不动。拆分的实际过程见 `docs/superpowers/notes/splitting-a-module.md`。
- 设计文档：`docs/superpowers/specs/2026-09-15-gokit-framework-design.md`。

## 本版不包含

- 脚手架 CLI
- 服务注册发现
- 链路追踪
- 熔断限流
