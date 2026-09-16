# 拆分演示：把模块从单体切到独立进程 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `example/minimal` 的 `greeter` 模块从单体里切出去，跑在独立进程上，并用测试证明调用方的业务代码一行没改。

**Architecture:** 示例长出三个二进制：`monolith` 保持今天的形态，`greeter` 只跑该模块并以 gRPC 暴露，`gateway` 通过 gRPC 调用它。模块新增一个远程实现与 `RemoteSet`，与 `LocalSet` 并列。一组断言在本地实现与远程实现上各跑一遍，两边必须一致。

**Tech Stack:** 已有的 `app`、`config`、`transport`、`component/*`，加 `google/wire`。不引入任何新依赖。

对应设计文档：`docs/superpowers/specs/2026-09-15-gokit-framework-design.md` 第 12 节第 7 步，以及第 7.2 节拆分四步里的第 2 步「多进程同仓」。
前两份计划已完成并合并：`2026-09-15-gokit-kernel-and-core-components.md`、`2026-09-16-transport-and-grpc.md`。

---

## 为什么这一步值得单独做

上一轮整分支评审查出的最严重缺陷，是「本服务把下游返回的错误往上抛」时错误翻译被绕过：`Reason` 丢失、调用方的 `errors.Is` 返回 false、内部细节泄漏给外部客户端。它只在评审者手写探针时才暴露，因为仓库里当时没有任何真实的跨进程场景。

这份计划要补上的正是那个场景。**Task 5 的对等测试是本计划的全部意义所在**，其余五个任务都是为它铺路。

---

## Global Constraints

这一节适用于**每一个** Task。

1. **Go 版本**：库模块 `go.mod` 是 `go 1.22`，**不得改动**；示例模块是 `go 1.25.0`。依赖锁死：`grpc v1.65.0`、`protobuf v1.35.2`。**本计划不引入任何新依赖**，若发现需要，停下来报告。
2. **模块路径**：库 `github.com/Kline-x/gokit`，示例 `github.com/Kline-x/gokit/example/minimal`。仓库在 `E:\code\AI\vibCoding\gokit`，WSL 内 `/mnt/e/code/AI/vibCoding/gokit`。
3. **所有 go / wire 命令在 WSL 中执行**：

   ```bash
   wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
   ```

   Go 在 `/home/gaore/sdk/go/bin/go`，`wire` 在 `/home/gaore/go/bin/wire`。**不要在 WSL 命令里用裸的 `$HOME`**，会被静默吞掉，一律写字面路径；`PATH=...:$PATH` 赋值里的 `$PATH` 是唯一例外。命令跑完后打印的 `chdir(...) failed` 是中继产物，看退出码。若 Git Bash 改写了 `/home/gaore/...` 这类路径，命令前加 `MSYS_NO_PATHCONV=1`。
4. **只动示例模块**。框架的 `app`、`config`、`transport`、`component/*` 一律不改。若你认为必须改框架才能完成任务，**停下来报告**——那说明框架缺了东西，是个值得单独决定的发现。
5. **分层规则不变**：`domain` 只依赖标准库；`application` 只 import 本模块 `domain`，拥有 `Service` 接口这个唯一对外契约；`infrastructure` 与 `remote` 是两个并列的出站适配器；`interfaces` 下的 HTTP 与 gRPC 适配器只做协议转换、都依赖 `Service` 接口。
6. **文档、目录名、注释、提交信息中不出现 "DDD" 字样**。
7. **注释与提交信息用中文**。提交用 `git -c user.name=xuyang -c user.email=xuyang@89you.com commit`，信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。**这行是字面文本，执行者不要换成自己的模型名。**
8. **`wire_gen.go` 是工具产物，绝不手写。** wire 报缺 provider 就补 `provideXxx`，绝不改框架组件。
9. **每个 Task 结束时提交一次**，提交前两个模块的 `go vet` 与 `go test ./... -race` 都必须通过。

---

## 现状（写代码时按这个来）

```
example/minimal/
├── api/greeter/v1/greeter.proto  (+ 生成的 .pb.go)
├── config.yaml
├── go.mod                        go 1.25.0
├── main.go                       package main：Config、Bundle、各 provideXxx
├── wire.go                       //go:build wireinject
├── wire_gen.go
├── main_test.go                  三个端到端测试
└── internal/greeter/
    ├── domain/greeting.go        Greeting、NewGreeting、Repository、ErrNotFound
    ├── application/service.go    GreetRequest、GreetReply、Service、Transactor、LocalService
    ├── infrastructure/repo.go    GreetingRepo、Migrate
    ├── infrastructure/migrator.go Migrator（组件，DependsOn 数据库）
    ├── interfaces/http.go        HTTPHandler
    ├── interfaces/grpc.go        GRPCHandler
    └── module.go                 LocalSet
```

`Bundle` 现在是 `{App, HTTP, GRPC, Components}`，`provideComponents(logger, db, migrator, httpSrv, grpcSrv) []app.Component`。

`application.Service` 的方法是 `Greet(ctx, GreetRequest) (GreetReply, error)`，用的是**普通 Go 结构体**，不是 proto 类型。远程实现要自己在 `GreetRequest` 与 `greeterv1.GreetRequest` 之间转换。

---

## File Structure

| 文件 | 职责 |
|---|---|
| `cmd/monolith/{main,wire,wire_gen}.go` | 今天的单体，整体搬迁过来 |
| `cmd/monolith/main_test.go` | 今天的三个端到端测试，跟着搬 |
| `cmd/greeter/{main,wire,wire_gen}.go` | 只跑 greeter 模块，gRPC 暴露 |
| `cmd/gateway/{main,wire,wire_gen}.go` | HTTP 入口，greeter 走远程 |
| `configs/{monolith,greeter,gateway}.yaml` | 三份配置 |
| `internal/greeter/remote/client.go` | `application.Service` 的 gRPC 客户端实现 |
| `internal/greeter/module.go` | `LocalSet` 旁边加 `RemoteSet` |
| `internal/parity/parity_test.go` | 本地与远程实现的对等测试 |
| `docs/superpowers/notes/splitting-a-module.md` | 拆分过程的文字记录 |

---

## Task 1: 重构为多二进制骨架

纯搬迁，行为不变。先把架子搭好，后面三个二进制才有地方放。

**Files:**
- Move: `main.go` → `cmd/monolith/main.go`；`wire.go` → `cmd/monolith/wire.go`；`wire_gen.go` → `cmd/monolith/wire_gen.go`；`main_test.go` → `cmd/monolith/main_test.go`
- Move: `config.yaml` → `configs/monolith.yaml`
- Modify: `cmd/monolith/main.go`（默认配置路径）

**Interfaces:**
- Consumes: 无
- Produces: `go run ./cmd/monolith` 等价于今天的 `go run .`

- [ ] **Step 1: 搬文件**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && mkdir -p cmd/monolith configs && git mv main.go wire.go wire_gen.go main_test.go cmd/monolith/ && git mv config.yaml configs/monolith.yaml && ls cmd/monolith configs'
```

用 `git mv` 而不是 `mv`，好让历史能追下去。

- [ ] **Step 2: 改默认配置路径**

`cmd/monolith/main.go` 里那行 `flag.StringVar(&configPath, "config", "config.yaml", ...)` 改成：

```go
	flag.StringVar(&configPath, "config", "configs/monolith.yaml", "配置文件路径")
```

读一下真实代码再改——变量名与说明文字可能与这里的写法不同。

- [ ] **Step 3: 重新生成 wire**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && PATH=/home/gaore/sdk/go/bin:/home/gaore/go/bin:$PATH wire ./...'
```

搬过目录之后 `wire_gen.go` 的包声明与 import 路径都不用变（还是 `package main`），但重新跑一次能确认工具认得出新位置。若 wire 报错，停下来报告。

- [ ] **Step 4: 验证**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

三个端到端测试必须照旧全过。它们不该需要任何改动——如果需要，说明搬迁改变了行为，停下来报告是哪一条。

再跑一次真实二进制确认路径对了：

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && timeout 8 /home/gaore/sdk/go/bin/go run ./cmd/monolith > /tmp/mono.log 2>&1 & sleep 5; curl -s http://127.0.0.1:8080/greet/gokit; echo; sleep 4; head -20 /tmp/mono.log'
```

预期返回统一信封。若后台进程在这套工具下跑不稳，如实说明并以测试结果为准。

- [ ] **Step 5: 更新根 Makefile 的示例目标**

仓库根 `Makefile` 有一个 `example-test` 目标。读一下它现在怎么写的——它跑的是 `cd example/minimal && $(GO) test ./...`，搬迁之后仍然有效，不需要改。若它写死了别的路径，改成能覆盖整个示例模块。在报告里说明你有没有改。

- [ ] **Step 6: 提交**

```bash
git add -A
git commit -m "示例：重构为多二进制骨架，单体搬进 cmd/monolith"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 2: 模块的远程实现与 RemoteSet

**Files:**
- Create: `internal/greeter/remote/client.go`
- Modify: `internal/greeter/module.go`

**Interfaces:**
- Consumes: `application.Service`、`application.GreetRequest`、`application.GreetReply`；`greeterv1.GreeterClient`；`grpcclient.Client`
- Produces:
  - `func NewService(c *grpcclient.Client) *Service`（`remote` 包内）
  - `func (s *Service) Greet(ctx context.Context, req application.GreetRequest) (application.GreetReply, error)`
  - `var greeter.RemoteSet`

- [ ] **Step 1: 写远程实现**

有个时序陷阱要先说清楚：**不能在构造时就取连接**。wire 的装配发生在 `App.Start` 之前，那时 `grpcclient.Client` 还没建连，`Conn()` 返回 nil，拿 nil 造出来的桩一调用就 panic。所以要每次调用时再取。

`example/minimal/internal/greeter/remote/client.go`：

```go
// Package remote 是问候模块的出站适配器：用 gRPC 调用同名的远程服务。
//
// 它与 infrastructure 并列——两者都是出站适配器，区别在于 infrastructure
// 连的是本进程管得着的存储，remote 连的是另一个进程。
// 模块真正拆出去时，infrastructure 跟着服务走，remote 留在调用方这边。
//
// 它实现的是 application.Service，与 application.LocalService 同一个接口。
// 调用方拿到的是接口，因此换实现这件事对它不可见。
package remote

import (
	"context"

	"github.com/Kline-x/gokit/component/grpcclient"
	greeterv1 "github.com/Kline-x/gokit/example/minimal/api/greeter/v1"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/application"
	"github.com/Kline-x/gokit/transport"
)

// Service 通过 gRPC 调用远程的问候服务。
type Service struct {
	conn *grpcclient.Client
}

// NewService 构造远程实现。
//
// 这里只存下连接组件本身，不提前取它的 Conn：wire 的装配发生在 App.Start
// 之前，那时连接还没建立，Conn() 返回 nil，拿它造出来的桩一调用就会 panic。
func NewService(c *grpcclient.Client) *Service {
	return &Service{conn: c}
}

// Greet 实现 application.Service。
//
// 只做两件事：把应用层的入参翻成 proto、把 proto 的出参翻回来。
// 错误原样返回——grpcclient 的拦截器已经把 status 还原成 transport.Error，
// 所以调用方用 errors.Is 判断错误的写法与本地实现下完全一致。
//
// 每次调用都现取连接、现造桩。造桩很便宜，它只是把 conn 包一层，没有握手。
func (s *Service) Greet(ctx context.Context, req application.GreetRequest) (application.GreetReply, error) {
	conn := s.conn.Conn()
	if conn == nil {
		return application.GreetReply{}, transport.Unavailable(
			"GREETER_NOT_CONNECTED", "问候服务尚未建立连接")
	}

	reply, err := greeterv1.NewGreeterClient(conn).Greet(ctx, &greeterv1.GreetRequest{Name: req.Name})
	if err != nil {
		return application.GreetReply{}, err
	}
	return application.GreetReply{Text: reply.GetText()}, nil
}
```

- [ ] **Step 2: 加 RemoteSet**

在 `internal/greeter/module.go` 的 `LocalSet` 之后追加：

```go
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
```

补上 `remote` 包的 import。注意 `RemoteSet` 仍然提供 `interfaces.NewHTTPHandler`——调用方要把这个模块的能力以 HTTP 暴露给自己的用户。

- [ ] **Step 3: 验证编译**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

此时还没有任何二进制用 `RemoteSet`，但它必须能编译。三个既有测试照旧全过。

- [ ] **Step 4: 提交**

```bash
git add -A
git commit -m "示例：问候模块的远程实现与 RemoteSet"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 3: cmd/greeter — 独立的问候服务

**Files:**
- Create: `cmd/greeter/main.go`, `cmd/greeter/wire.go`
- Generate: `cmd/greeter/wire_gen.go`
- Create: `configs/greeter.yaml`

**Interfaces:**
- Consumes: `greeter.LocalSet`、`log`、`sqldb`、`grpcserver`
- Produces: 一个只跑问候模块、以 gRPC 暴露的二进制

- [ ] **Step 1: 写 main.go**

`example/minimal/cmd/greeter/main.go`：

```go
// Command greeter 是拆分出去之后的问候服务。
//
// 它只装这个模块自己需要的东西：日志、数据库、建表、gRPC 服务。
// 没有 HTTP —— 对外只提供 gRPC 契约，谁要用就按 api/greeter/v1 生成客户端。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	_ "modernc.org/sqlite" // 注册 sqlite 驱动，框架本身不绑定任何驱动

	"github.com/Kline-x/gokit/app"
	"github.com/Kline-x/gokit/component/grpcserver"
	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/component/sqldb"
	"github.com/Kline-x/gokit/config"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/infrastructure"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/interfaces"
)

// Config 是本服务的聚合配置。
type Config struct {
	Log  log.Config        `yaml:"log"`
	GRPC grpcserver.Config `yaml:"grpc"`
	DB   sqldb.Config      `yaml:"db"`
}

func defaultConfig() Config {
	dbCfg := sqldb.DefaultConfig()
	dbCfg.Driver = "sqlite"
	dbCfg.DSN = "file:greeter.db"

	grpcCfg := grpcserver.DefaultConfig()
	grpcCfg.Addr = ":9000"

	return Config{
		Log:  log.DefaultConfig(),
		GRPC: grpcCfg,
		DB:   dbCfg,
	}
}

// Bundle 汇总一次装配产出的对象。
type Bundle struct {
	App        *app.App
	GRPC       *grpcserver.Server
	Components []app.Component
}

// Register 把组件交给 App。先后由各组件的 DependsOn 决定。
func (b *Bundle) Register() *app.App {
	b.App.Register(b.Components...)
	return b.App
}

func provideLogConfig(cfg Config) log.Config          { return cfg.Log }
func provideGRPCConfig(cfg Config) grpcserver.Config  { return cfg.GRPC }
func provideDBConfig(cfg Config) sqldb.Config         { return cfg.DB }

func provideApp(logger *log.Logger) *app.App {
	return app.New(app.WithName("greeter"), app.WithLogger(logger.Logger))
}

// provideServiceRegistrars 列出要挂到 gRPC 服务器上的服务。
func provideServiceRegistrars(h *interfaces.GRPCHandler) []grpcserver.ServiceRegistrar {
	return []grpcserver.ServiceRegistrar{h}
}

// provideGRPCServer 只传 RequestLog：Recover 与 ErrorMapper 由 grpcserver
// 默认装在最内层，因此这里传进去的天然包在它们外面。
func provideGRPCServer(
	cfg grpcserver.Config,
	services []grpcserver.ServiceRegistrar,
	logger *log.Logger,
	a *app.App,
) *grpcserver.Server {
	return grpcserver.New(cfg, services,
		grpcserver.WithFatal(a.Fatal),
		grpcserver.WithLogger(logger.Logger),
		grpcserver.WithUnaryInterceptor(grpcserver.RequestLog(logger.Logger)),
	)
}

// provideComponents 列出本次装配要交给 App 托管的组件。
func provideComponents(
	logger *log.Logger,
	db *sqldb.DB,
	migrator *infrastructure.Migrator,
	grpcSrv *grpcserver.Server,
) []app.Component {
	return []app.Component{logger, db, migrator, grpcSrv}
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "configs/greeter.yaml", "配置文件路径")
	flag.Parse()

	cfg := defaultConfig()
	loader := config.New(
		config.WithOptionalFile(configPath),
		config.WithEnvPrefix("GREETER"),
	)
	if err := loader.Load(&cfg); err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}

	b, err := initApp(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "装配失败:", err)
		os.Exit(1)
	}

	if err := b.Register().Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "运行失败:", err)
		os.Exit(1)
	}
}
```

读一下 `cmd/monolith/main.go` 再动手，把 `grpcserver.WithLogger` 之类的选项名与真实签名对齐——它们在上一份计划的修复轮里变过。

- [ ] **Step 2: 写 wire.go**

`example/minimal/cmd/greeter/wire.go`：

```go
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
```

`LocalSet` 会带进 `interfaces.NewHTTPHandler`，但本二进制没有 HTTP 服务器，wire 不会去构造用不到的东西，所以不成问题。若 wire 报「未使用的 provider」，读它的报错再决定怎么办，不要凭猜改 `LocalSet`。

- [ ] **Step 3: 写配置**

`example/minimal/configs/greeter.yaml`：

```yaml
log:
  level: info
  format: json
  output: stdout

grpc:
  name: "grpcserver"
  addr: ":9000"
  shutdown_timeout: 10s
  enable_reflection: true

db:
  driver: sqlite
  dsn: "file:greeter.db"
  max_open_conns: 10
```

- [ ] **Step 4: 生成 wire 并验证**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && PATH=/home/gaore/sdk/go/bin:/home/gaore/go/bin:$PATH wire ./... && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

- [ ] **Step 5: 提交**

```bash
git add -A
git commit -m "示例：拆出独立的问候服务二进制"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 4: cmd/gateway — 通过 gRPC 调用的入口

**Files:**
- Create: `cmd/gateway/main.go`, `cmd/gateway/wire.go`
- Generate: `cmd/gateway/wire_gen.go`
- Create: `configs/gateway.yaml`

**Interfaces:**
- Consumes: `greeter.RemoteSet`、`log`、`grpcclient`、`httpserver`
- Produces: 一个不碰数据库、把问候能力以 HTTP 暴露的二进制

- [ ] **Step 1: 写 main.go**

`example/minimal/cmd/gateway/main.go`：

```go
// Command gateway 是把问候能力以 HTTP 暴露给用户的入口服务。
//
// 它自己不实现任何业务：问候模块已经拆到 greeter 服务上了，
// 这里装的是 greeter.RemoteSet，走 gRPC 过去。
// 注意它没有数据库——存储跟着模块一起搬走了。
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/Kline-x/gokit/app"
	"github.com/Kline-x/gokit/component/grpcclient"
	"github.com/Kline-x/gokit/component/httpserver"
	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/config"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/interfaces"
)

// Config 是本服务的聚合配置。
type Config struct {
	Log     log.Config        `yaml:"log"`
	HTTP    httpserver.Config `yaml:"http"`
	Greeter grpcclient.Config `yaml:"greeter"`
}

func defaultConfig() Config {
	greeterCfg := grpcclient.DefaultConfig()
	greeterCfg.Name = "grpcclient.greeter"
	greeterCfg.Target = "127.0.0.1:9000"

	return Config{
		Log:     log.DefaultConfig(),
		HTTP:    httpserver.DefaultConfig(),
		Greeter: greeterCfg,
	}
}

// Bundle 汇总一次装配产出的对象。
type Bundle struct {
	App        *app.App
	HTTP       *httpserver.Server
	Components []app.Component
}

// Register 把组件交给 App。
func (b *Bundle) Register() *app.App {
	b.App.Register(b.Components...)
	return b.App
}

func provideLogConfig(cfg Config) log.Config             { return cfg.Log }
func provideHTTPConfig(cfg Config) httpserver.Config     { return cfg.HTTP }
func provideGreeterConfig(cfg Config) grpcclient.Config  { return cfg.Greeter }

func provideApp(logger *log.Logger) *app.App {
	return app.New(app.WithName("gateway"), app.WithLogger(logger.Logger))
}

// provideGreeterClient 构造到问候服务的连接。
//
// 装上 ErrorRestorer：它把下游返回的 status 还原成 transport.Error，
// 因此调用方用 errors.Is 判断错误的写法与本地实现下完全一致。
func provideGreeterClient(cfg grpcclient.Config) (*grpcclient.Client, error) {
	return grpcclient.New(cfg,
		grpcclient.WithUnaryInterceptor(grpcclient.ErrorRestorer()),
	)
}

// provideHandler 组装路由与中间件。RequestLog 必须在最外层。
func provideHandler(logger *log.Logger, greeter *interfaces.HTTPHandler) http.Handler {
	mux := http.NewServeMux()
	greeter.Register(mux)
	return httpserver.Chain(mux,
		httpserver.RequestLog(logger.Logger),
		httpserver.Recover(logger.Logger),
	)
}

func provideHTTPServer(cfg httpserver.Config, h http.Handler, a *app.App) *httpserver.Server {
	return httpserver.New(cfg, h, httpserver.WithFatal(a.Fatal))
}

// provideComponents 列出本次装配要交给 App 托管的组件。
//
// 与单体相比这里少了数据库与建表，多了一条到下游的连接——
// 拆分带来的差异全部集中在这一处。
func provideComponents(
	logger *log.Logger,
	greeterConn *grpcclient.Client,
	httpSrv *httpserver.Server,
) []app.Component {
	return []app.Component{logger, greeterConn, httpSrv}
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "configs/gateway.yaml", "配置文件路径")
	flag.Parse()

	cfg := defaultConfig()
	loader := config.New(
		config.WithOptionalFile(configPath),
		config.WithEnvPrefix("GATEWAY"),
	)
	if err := loader.Load(&cfg); err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}

	b, err := initApp(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "装配失败:", err)
		os.Exit(1)
	}

	if err := b.Register().Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "运行失败:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: 写 wire.go**

`example/minimal/cmd/gateway/wire.go`：

```go
//go:build wireinject

package main

import (
	"github.com/google/wire"

	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter"
)

// initApp 装配入口服务。
//
// 与 cmd/monolith 的装配比一比，核心差异只有一处：这里用 greeter.RemoteSet
// 而不是 LocalSet，于是问候能力从「本进程实现」变成了「调远程」。
//
// 其余的出入都是这个选择的后果：既然实现不在本进程，就不需要数据库、
// 不需要事务能力的绑定，转而需要一条到下游的连接；本服务也不对外提供 gRPC，
// 所以 gRPC 服务端那套装配整个不在。逐行 diff 会看到七处不同，
// 但它们全是「换了一个 ProviderSet」推导出来的，不是七个独立决定。
//
// 真正的重点是没变的部分：domain、application、interfaces 三层一行没动。
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
```

- [ ] **Step 3: 写配置**

`example/minimal/configs/gateway.yaml`：

```yaml
log:
  level: info
  format: json
  output: stdout

http:
  name: "httpserver"
  addr: ":8080"
  read_timeout: 15s
  write_timeout: 15s

greeter:
  name: "grpcclient.greeter"
  target: "127.0.0.1:9000"
  dial_timeout: 5s
  block: false
```

`block: false` 是刻意的：下游可能比本服务晚起，不该因此卡住启动。

- [ ] **Step 4: 生成 wire 并验证**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && PATH=/home/gaore/sdk/go/bin:/home/gaore/go/bin:$PATH wire ./... && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

- [ ] **Step 5: 手工跑通两个进程**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && rm -f greeter.db && timeout 15 /home/gaore/sdk/go/bin/go run ./cmd/greeter > /tmp/greeter.log 2>&1 & sleep 6; timeout 12 /home/gaore/sdk/go/bin/go run ./cmd/gateway > /tmp/gateway.log 2>&1 & sleep 6; curl -s http://127.0.0.1:8080/greet/gokit; echo; sleep 6; echo "--- greeter ---"; head -20 /tmp/greeter.log; echo "--- gateway ---"; head -20 /tmp/gateway.log'
```

预期 curl 返回 `{"code":0,"data":{"text":"你好，gokit"}}`，两份日志各自显示自己那套组件的启停。

若后台进程在这套工具下跑不稳，如实说明并以 Task 5 的测试为准——不要反复跟 shell 较劲。跑完记得清掉 `greeter.db`。

- [ ] **Step 6: 提交**

```bash
git add -A
git commit -m "示例：入口服务通过 gRPC 调用问候服务"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 5: 对等测试 —— 本地与远程必须表现一致

**这是本计划的核心。** 前四个任务都是为它铺路。

**Files:**
- Create: `internal/parity/parity_test.go`

**Interfaces:**
- Consumes: 全部前序任务
- Produces: 一组断言，在本地实现与远程实现上各跑一遍

- [ ] **Step 1: 写对等测试**

`example/minimal/internal/parity/parity_test.go`：

```go
// Package parity 只有测试：它把同一组断言分别打到问候模块的本地实现
// 与远程实现上，两边都必须通过。
//
// 这是整套分层想换来的东西的字面验证 ——「模块拆出去，调用方代码不动」。
// 如果哪天这个测试在 remote 那一栏挂了，说明拆分的承诺破了。
package parity

import (
	"context"
	"database/sql"
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
```

第三组断言（Metadata）是刻意加的：上一轮评审发现 `Metadata` 在 gRPC 侧会整个丢失，修复后没有任何跨进程的测试盯着它。这里补上。

- [ ] **Step 2: 跑测试**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && /home/gaore/sdk/go/bin/go test ./internal/parity -v -race -count=1 -timeout 120s'
```

两个子测试都必须过。

**若 remote 那一栏挂了，不要改测试去迁就实现。** 记下失败的具体断言并报告——那正是这个测试存在的意义，它发现了一处「拆分后调用方要改代码」的地方。

已知的几个坑，先想清楚再动手：
- `application.NewLocalService` 的第二个参数是 `Transactor`。`*sqldb.DB` 满足它，直接传即可。
- `interfaces.NewGRPCHandler` 收的是 `application.Service` 接口。
- `grpcserver.New` 默认已经装了 `Recover` 与 `ErrorMapper`，所以不必再传 —— 但 `ErrorMapper` 必须生效，否则错误到不了客户端那边。
- 两个子测试各用一个 DSN，互不干扰。remote 那一栏内部还会再造一个本地实现给服务端用，DSN 与 local 那栏不同。

- [ ] **Step 3: 跑全量并提交**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s && cd example/minimal && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

```bash
git add -A
git commit -m "示例：本地与远程实现的对等测试"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 6: 把拆分过程写下来

**Files:**
- Create: `docs/superpowers/notes/splitting-a-module.md`
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-09-15-gokit-framework-design.md`

**Interfaces:**
- Consumes: 全部前序任务
- Produces: 一份能照着做的拆分记录

- [ ] **Step 1: 写拆分记录**

`docs/superpowers/notes/splitting-a-module.md`，用中文，大约 80 到 120 行。内容按这个顺序，全部基于示例里**真实发生**的改动，不要写想当然的步骤：

1. 一段话说明这份记录是什么：把 `example/minimal` 的问候模块从单体切到独立进程，实际改了哪些文件。
2. **改了什么**：列出新增的 `internal/greeter/remote/`、`RemoteSet`、两个新二进制与它们的配置。
3. **没改什么**：`domain`、`application`、`interfaces` 三层一行没动。这是重点，要明确指出来。读 `git log` 或 `git diff` 确认这句话是真的再写——若实际上动了，如实写动了什么、为什么。
4. **`LocalSet` 与 `RemoteSet` 的差异**：远程那边不需要仓储、迁移、数据库、gRPC 接口层；需要一条连接。把两个 ProviderSet 并排贴出来。
5. **一个真实的坑**：远程实现不能在构造时就取 `grpcclient.Client` 的连接，因为 wire 的装配发生在 `App.Start` 之前，那时 `Conn()` 是 nil。要每次调用时再取。
6. **怎么验证拆对了**：指向 `internal/parity/parity_test.go`，说明它同一组断言跑两遍的用意。
7. **还差什么**：设计文档拆分四步里的第 3 步（搬到独立仓库）与第 4 步（事件改走 MQ）都还没做；本记录只覆盖第 2 步「多进程同仓」。

- [ ] **Step 2: 更新 README**

读一下 `README.md` 现在怎么描述示例的，把它改成反映三个二进制。要说清楚：

- `cmd/monolith` 是单体形态，所有模块本地实现
- `cmd/greeter` 与 `cmd/gateway` 是同一套业务代码拆成两个进程的形态
- 换掉一个 ProviderSet 就能切换，业务三层不动
- 指向 `docs/superpowers/notes/splitting-a-module.md`

把「本版不包含」里的「拆分演示」删掉（若有这一条）。读一下真实内容再改，只动陈旧的部分。

- [ ] **Step 3: 在设计文档里标注进度**

`docs/superpowers/specs/2026-09-15-gokit-framework-design.md` 第 12 节，给第 7 步加上完成标记，注明交付它的计划是 `docs/superpowers/plans/2026-09-16-split-demo.md`，与前面几步的标记方式保持一致。不要改动设计文档的其他内容。

- [ ] **Step 4: 提交**

```bash
git add -A
git commit -m "文档：记录模块拆分的实际过程"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## 完成标准

全部 6 个 Task 做完后，下列命令必须通过：

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

以及：`internal/parity` 的两个子测试都通过，三个二进制都能起得来，两进程形态下 curl 入口服务能拿到与单体一致的结果。

此时设计文档第 12 节的第 7 步完成。剩余的第 6 步（脚手架 CLI）与第 8 步（可选组件）各自另立计划。
