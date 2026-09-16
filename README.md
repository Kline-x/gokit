# gokit

gokit 是一个 Go 应用框架库：基础设施组件化，每个组件（日志、HTTP 服务、数据库……）都实现同一套生命周期契约，由一个 `App` 统一负责启动与停止的编排。业务方引入 gokit 后，只需实现或复用组件、把它们注册进 `App`，不需要自己管理启动顺序、优雅退出和信号处理。

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

## 更多

- 完整可运行示例：`example/minimal`（分层的问候模块，HTTP 暴露，数据落 SQLite）。
- 设计文档：`docs/superpowers/specs/2026-09-15-gokit-framework-design.md`。

## 本版不包含

- gRPC 组件
- 统一错误码与响应
- 脚手架 CLI
- 服务注册发现
- 链路追踪
- 熔断限流
