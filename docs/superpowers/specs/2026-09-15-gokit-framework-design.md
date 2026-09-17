# gokit 框架设计文档

- 日期：2026-09-15
- 模块路径：`github.com/Kline-x/gokit`
- 本地路径：`E:\code\AI\vibCoding\gokit`
- 状态：设计已确认，待转实施计划

---

## 1. 目标

设计一个 Go 应用框架库 + 项目脚手架，满足四条目标：

1. **分层明确、职责清晰、依赖解耦、拆分简单**：按分层架构组织业务代码，层与层之间的依赖方向由静态检查强制。
2. **单体可平滑演进到微服务**：业务模块之间只通过 Go 接口调用，单体注入本地实现、拆分后注入远程实现，调用方代码零改动。
3. **基础设施全部组件化**：web server、gRPC server、Redis、MySQL、日志等都实现同一套生命周期接口，统一注册、统一启停、按需增删。
4. **场景无关**：同一套内核适用于 CLI、Web 服务、gRPC 服务，也能嵌入 Wails 这类自带事件循环的宿主框架。

### 非目标（首版明确不做）

服务注册发现、配置中心、链路追踪、熔断限流、多租户。这些后续都能以组件形式加入，内核不为它们预留抽象，避免过度设计。

---

## 2. 关键决策

| 决策点 | 选择 | 理由 |
|---|---|---|
| 产出形态 | 框架库 + 脚手架 | 多个业务项目共用一份框架代码，升级只改依赖版本 |
| 依赖注入 | `google/wire`（编译期生成） | 无反射、编译期报错、装配代码可读可调试 |
| 模块间调用 | Go 接口抽象 + 本地/远程双实现 | 拆分时调用方零改动 |
| 默认技术选型 | 标准库优先：`net/http` + `log/slog` + `database/sql` | 内核零重依赖，便于配合其他框架与 Wails |
| 内核形态 | 统一 `Component` 生命周期接口 + `App` 编排 | 同时满足「组件统一启停拔插」与「显式依赖可追溯」 |

### 内核方案选型过程

考虑过三个方案：

- **A. Component 生命周期接口 + App 编排 + wire 装配**（选中）。内核只定义 `Component` 与 `App`，所有基础设施实现同一接口。
- **B. kratos 式，App 只管 transport.Server**。更薄，但数据库、缓存等没有统一的启停语义，健康检查与按序关闭要各写各的，不满足目标 3。
- **C. 插件自注册（`init()` + blank import）**。拔插最省事，但依赖关系隐式不可见，组件间依赖要靠全局注册表解决，与 wire 的显式装配理念冲突，可测试性差。

选 A 的原因：唯一同时满足「组件统一启停拔插」「显式依赖可追溯」「场景无关」三条的方案。

### 术语约定

文档、目录名、注释中不使用 "DDD" 字样，统一表述为「分层」与「领域模型」。框架中承载领域基础类型的包命名为 `domain`。

---

## 3. 框架库结构

```
gokit/
├── app/              内核：App、Component 接口、生命周期编排、信号处理、优雅退出
├── config/           配置加载（yaml → env → flag 逐级覆盖）
├── domain/           领域基础类型：Entity / AggregateRoot / ValueObject
│                     / DomainEvent / Repository 标记接口 / UnitOfWork
├── transport/        通信契约层（非实现）：错误码、统一响应、中间件接口
├── component/        官方组件，每个子包独立，只依赖 app + config + 标准库
│   ├── log/          slog 封装：Logger 组件 + 从 ctx 取 logger
│   ├── httpserver/   net/http Server 组件
│   ├── grpcserver/   gRPC Server 组件
│   ├── grpcclient/   gRPC 客户端连接池组件
│   ├── sqldb/        database/sql 连接池组件
│   ├── redis/        go-redis 客户端组件
│   ├── cron/         定时任务组件
│   └── eventbus/     进程内事件总线
├── cmd/gokit/        脚手架 CLI
└── template/         脚手架模板（embed 进二进制）
```

组件包之间互不依赖。业务项目可以只引用其中任意几个子包，未引用的组件不进 `go.mod`、不参与编译。

---

## 4. 业务项目结构与分层

脚手架生成的业务项目按**模块垂直切片**组织，每个模块内部分四层：

```
myapp/
├── cmd/
│   ├── server/         main.go + wire.go     web + gRPC 装配
│   └── cli/            main.go + wire.go     只装 config/log/sqldb 的命令行装配
├── api/                                      模块对外契约，拆分时随模块走
│   └── user/v1/user.proto
├── internal/
│   ├── user/                                 一个业务模块 = 一个未来可独立的服务
│   │   ├── domain/            实体、值对象、领域服务、仓储接口、领域事件
│   │   ├── application/       用例编排、DTO、事务边界；对外暴露 UserService 接口
│   │   ├── infrastructure/    仓储实现（sql/redis）、外部服务适配器、事件发布实现
│   │   ├── interfaces/        http handler / grpc server / cli 命令，只做协议转换
│   │   └── module.go          本模块的 wire.ProviderSet，唯一装配入口
│   └── order/ ...
├── pkg/                                      跨模块共享的纯工具（无业务语义）
├── configs/config.yaml
└── .golangci.yml                             依赖方向静态检查规则
```

### 依赖方向硬规则

1. `domain` 不 import 任何非标准库包，也不 import 兄弟模块。
2. `application` 只 import 本模块 `domain`，以及其他模块 `application` 层的**接口**；不得 import 其他模块的 `infrastructure`。
3. `infrastructure` 与 `interfaces` 可以 import gokit 组件；二者互不依赖。
4. 跨模块调用只能走 `application` 层接口，入参出参使用 `api/` 下 proto 生成的类型或纯 DTO。

这四条由项目模板内置的 `.golangci.yml` 中 `depguard` 规则强制，CI 可拦截；`gokit doctor` 在本地跑同一套检查。

每个模块的 `module.go` 是它的边界。拆分时把 `internal/<module>` 与 `api/<module>` 整目录搬走即可。

---

## 5. 内核契约

### 5.1 Component

所有基础设施统一实现：

```go
type Component interface {
    Name() string
    // 阻塞型组件（http/grpc server）内部自行起 goroutine，Start 必须快速返回
    Start(ctx context.Context) error
    // ctx 带超时，超时后 App 强制退出
    Stop(ctx context.Context) error
}

// 可选能力，按需实现，App 用类型断言探测
type HealthChecker interface{ Health(ctx context.Context) error }
type Dependent    interface{ DependsOn() []string }  // 按 Name 声明启动顺序依赖
```

### 5.2 App

```go
type App struct { /* ... */ }

func New(opts ...Option) *App
// Option: WithName / WithVersion / WithStopTimeout / WithLogger / WithSignals

func (a *App) Register(c ...Component)
func (a *App) Run(ctx context.Context) error   // Start 全部 → 阻塞 → 逆序 Stop
func (a *App) Start(ctx context.Context) error // 拆开供 Wails 等宿主场景使用
func (a *App) Stop(ctx context.Context) error
func (a *App) Fatal(err error)                 // 运行期组件异常上报，触发整体优雅退出
```

行为约定：

- **启动顺序**：先按 `DependsOn` 做拓扑排序；未声明依赖的组件按注册顺序启动。
- **停止顺序**：严格逆序。
- **启动失败**：任一组件 `Start` 返回错误时，已启动的组件逆序 `Stop`，`Run` 返回该错误。
- **运行期异常**：组件通过 `app.Fatal(err)` 上报（例如 `ListenAndServe` 返回非 `ErrServerClosed`），触发整体优雅退出。
- **退出触发**：`ctx` 取消、进程信号（默认 SIGINT/SIGTERM）、`Fatal` 三者之一。
- **App 不是服务定位器**：组件之间不通过 App 互相查找，依赖全部由 wire 在构造时注入。App 只负责生命周期。

### 5.3 配置

`gokit/config` 提供一个 `Loader`：文件（yaml）→ 环境变量覆盖 → 命令行 flag 覆盖，产出配置树。

每个组件自带 `Config` 结构与 `DefaultConfig()`。业务项目的聚合配置按组件名挂子节点（`server.http`、`store.sql`、`log`）。组件的 Provider 只接收自己那一段 Config，与总配置结构解耦。

支持 `Watch` 钩子用于热更新。首版只落地日志级别一项。

### 5.4 日志

`gokit/component/log` 基于标准库 `log/slog`。提供 `log.FromContext(ctx)`，其他组件一律从 ctx 取 logger，框架内不持有全局单例。

---

## 6. 官方组件清单（首版）

每个组件独立子包，各自提供 `Config`、`ProviderSet`，并实现 `Component`。

| 组件 | 包 | 外部依赖 | 说明 |
|---|---|---|---|
| 日志 | `component/log` | 无（slog） | json/text 输出，级别热更新 |
| HTTP 服务 | `component/httpserver` | 无（net/http） | 接收 `http.Handler`；内置 recover / 请求日志 / 超时中间件；路由用标准库 `ServeMux`（Go 1.22+ 方法路由） |
| gRPC 服务 | `component/grpcserver` | `google.golang.org/grpc` | 接收 `[]ServiceRegistrar`；内置 recover / 日志拦截器、健康检查、reflection |
| gRPC 客户端 | `component/grpcclient` | `google.golang.org/grpc` | 按目标名管理连接，注入拦截器；模块远程实现基于它 |
| SQL | `component/sqldb` | 无（database/sql） | 连接池 + 事务助手 `Tx(ctx, fn)`；驱动由业务 blank import |
| Redis | `component/redis` | `go-redis/v9` | 客户端 + 分布式锁小工具 |
| 定时任务 | `component/cron` | `robfig/cron/v3` | 注册 job，跟随 App 启停 |
| 事件总线 | `component/eventbus` | 无 | 进程内发布/订阅，接口预留 MQ 实现 |

**Go 版本要求**：≥ 1.22（依赖标准库方法路由）。当前环境 Windows 为 go1.19、WSL 为 go1.21，需要先升级。

---

## 7. 模块间调用与拆分路径

### 7.1 接口契约

```go
// internal/user/application/service.go
type UserService interface {
    GetUser(ctx context.Context, req *v1.GetUserRequest) (*v1.GetUserReply, error)
}
```

入参出参直接使用 `api/user/v1` 的 proto 生成类型，使本地实现与 gRPC 实现的签名天然一致。

```go
// internal/user/module.go
var LocalSet = wire.NewSet(
    NewUserService,
    wire.Bind(new(UserService), new(*userService)),
    // 仓储实现、handler ...
)

var RemoteSet = wire.NewSet(
    NewUserGRPCClient,
    wire.Bind(new(UserService), new(*userGRPCClient)),
)
```

`order` 模块需要用 `user` 时，只 import `internal/user/application` 的接口；在 `wire.go` 里选择 `user.LocalSet` 或 `user.RemoteSet`。

`interfaces/grpc` 层的 server 实现直接委托给 `UserService`，因此远程实现等于本地实现加一层 gRPC 透传，无业务逻辑重复。

### 7.2 拆分四步

每一步都可独立上线：

1. **单体**：一个 `cmd/server`，所有模块使用 `LocalSet`。
2. **多进程同仓**：新增 `cmd/user-server`，只装 user 模块 + grpcserver；原 server 把 user 换成 `RemoteSet`。仓库结构与业务代码不动，只改两个 `wire.go`。
3. **独立仓库**：把 `internal/user` 与 `api/user` 搬到新仓库，原仓库 `go get` 新仓库的 `api/user` 包，`RemoteSet` 继续可用。
4. **异步解耦**：模块间领域事件从 `eventbus` 内存实现换成 MQ 实现，同样只动 wire。

---

## 8. 错误与响应

`gokit/transport` 提供统一错误类型：

```go
err := transport.New(code int, reason string, msg string)
// 带 Code / Reason / Metadata，支持 errors.Is / errors.As
```

同时提供与 gRPC `status` 和 HTTP JSON 的双向转换，保证跨进程后错误语义不丢失。

HTTP 统一响应体 `{code, msg, data}`，由 httpserver 的 `Render` 助手输出，业务 handler 不手写 JSON 序列化。

---

## 9. 多场景适配

| 场景 | 装配方式 |
|---|---|
| Web 服务 | 注册 log + httpserver + sqldb（+ redis），`app.Run` |
| gRPC 服务 | 注册 log + grpcserver + sqldb，`app.Run` |
| 双协议 | 同时注册 httpserver 与 grpcserver，两者委托同一个 application 层 Service |
| CLI | 注册 log + sqldb；命令用 `cobra`，子命令内部 `app.Start` → 执行 → `app.Stop` |
| Wails 桌面端 | 不装 httpserver。在 Wails 的 `OnStartup(ctx)` 调 `app.Start(ctx)`，`OnShutdown` 调 `app.Stop`；application 层 Service 直接 `Bind` 给前端 |

把 `Run` 拆成 `Start` / `Stop` 两个导出方法，正是为了支持 Wails 这类宿主已有事件循环的场景。

---

## 10. 测试策略

| 层 | 方式 |
|---|---|
| `domain` | 纯单元测试，零 mock |
| `application` | 用仓储接口的内存实现驱动测试 |
| 组件包 | 各自集成测试；sqldb / redis 用 testcontainers 或 build tag 跳过 |
| App 内核 | 用假组件验证启停顺序、失败回滚、停止超时 |

---

## 11. 脚手架

```bash
go install github.com/Kline-x/gokit/cmd/gokit@latest
```

| 命令 | 作用 |
|---|---|
| `gokit new myapp --mod github.com/xxx/myapp` | 生成项目骨架 |
| `gokit new module user` | 在现有项目中新增业务模块（四层目录 + module.go + proto） |
| `gokit wire` | 封装 `wire gen`，生成后顺带 `go build` 验证 |
| `gokit doctor` | 检查 Go 版本、protoc、wire 是否就绪，以及依赖方向是否违规 |

`gokit new` 只做一次交互提问：**装哪些组件**（多选 http / grpc / sql / redis / cron）。未选中的组件不写进 `wire.go`、不进 `go.mod`。生成的项目 `go build` 即通过、`go run` 即启动。

模板附带一个可运行的 `user` 示例模块：一条 HTTP 接口与一条 gRPC 接口打到同一个 `UserService`，仓储用 SQLite 实现，附各层示例测试。示例模块作为活文档，比 README 更能说明分层写法。

**实际实现与上述设想的出入**（`cmd/gokit`，见 `docs/superpowers/plans/2026-09-16-scaffold-cli.md`）：

- `gokit new` 没有做交互式提问，改成了参数开关：`--sql`（默认开）、`--grpc`（默认关）控制要不要生成对应组件；http 恒定生成，不在开关之列；redis、cron 这两个可选组件本身还没实现（见第 12 步第 8 步），自然也没有对应开关。
- `gokit new module`（往现有项目里新增业务模块）**没有做**，留到后续单独立计划：往既有项目里加模块要解析并改写现成的 `wire.go`，风险与工作量都独立于「生成一个全新项目」，值得单独评估。
- 示例模块默认名是 `hello`（可用 `--module` 改名），不叫 `user`；结构与设想一致——一条 HTTP 路由，加 `--grpc` 时同一个用例再挂一条 gRPC 方法，`--sql` 打开时仓储用 SQLite 实现。
- 设想里"各层附带示例测试"这条没有做：模板目录下没有任何 `_test.go` 模板，生成的项目各层只有实现代码，没有测试。
- `gokit wire`、`gokit doctor` 的行为与上表一致：前者是 `wire ./...` 加一次 `go build ./...` 验证；后者除工具链检查外，也会检查 `internal/<模块>/<层>` 的分层依赖方向，违反时返回非零。

---

## 12. 实施顺序

每一步产出可运行成果：

1. 升级 Go 到 1.22+（Windows 现 1.19、WSL 现 1.21），建仓库，配置 CI。（已完成，见 `docs/superpowers/plans/2026-09-15-gokit-kernel-and-core-components.md`）
2. 内核：`app`（Component / App / 生命周期）+ `config` + `component/log`，配完整单测。（已完成，见 `docs/superpowers/plans/2026-09-15-gokit-kernel-and-core-components.md`）
3. 组件：`httpserver`、`sqldb`，跑通最小可用单体。（已完成，见 `docs/superpowers/plans/2026-09-15-gokit-kernel-and-core-components.md`）
4. 组件：`grpcserver`、`grpcclient`，跑通同一个 Service 双协议暴露。（已完成，见 `docs/superpowers/plans/2026-09-16-transport-and-grpc.md`）
5. `transport`：错误码 + 统一响应。（已完成，见 `docs/superpowers/plans/2026-09-16-transport-and-grpc.md`）
6. 脚手架 CLI + 模板 + 示例模块。（已完成，见 `docs/superpowers/plans/2026-09-16-scaffold-cli.md`）
7. 拆分演示：把示例模块从单体切到独立进程，只改 `wire.go`，过程写成文档。（已完成，见 `docs/superpowers/plans/2026-09-16-split-demo.md`）
8. 可选组件：`redis`、`cron`、`eventbus`。

规模说明：以上八步不适合塞进同一份实施计划。第一份实施计划覆盖第 1 至 3 步，产出「内核 + 日志 + HTTP + SQL 的最小可用单体」；其余各步在其完成后各自立计划。
