# gokit 内核与核心组件 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 gokit 的生命周期内核（`app`）、配置加载（`config`）与三个基础组件（`log`、`httpserver`、`sqldb`），并用一个分层示例把它们串成可运行的最小单体。

**Architecture:** `app` 包定义统一的 `Component` 生命周期接口和 `App` 编排器，负责按依赖拓扑启动、逆序停止、失败回滚、信号退出。基础设施各自成包实现 `Component`，互不依赖（`log` 作为横切关注点例外）。业务代码按 `domain / application / infrastructure / interfaces` 四层切分，用 google/wire 在编译期装配。

**Tech Stack:** Go（标准库优先：`log/slog`、`net/http`、`database/sql`）、`gopkg.in/yaml.v3`、`github.com/google/wire`、GitHub Actions。

对应设计文档：`docs/superpowers/specs/2026-09-15-gokit-framework-design.md`。本计划覆盖设计文档第 12 节的第 1 至 3 步。

---

## Global Constraints

这一节的约束适用于**每一个** Task，不再逐条重复。

1. **Go 版本下限 1.22**。新工具链装在 `$HOME/sdk/go`（WSL 内），不动 `/usr/local/go` 的 go1.21，避免影响其他项目。
2. **模块路径** `github.com/Kline-x/gokit`。仓库本地路径 `E:\code\AI\vibCoding\gokit`，WSL 内为 `/mnt/e/code/AI/vibCoding/gokit`。
3. **所有 go / make 命令在 WSL 中执行**，模板：

   ```bash
   wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && make test'
   ```

   后文步骤只写 `make test` 这样的命令体，执行时一律套上面这层模板。PATH 由 Makefile 内的 `GO` 变量解决，不依赖登录 PATH。
4. **禁止第三方重依赖**。`app`、`transport` 包只允许标准库；`config` 只允许 `gopkg.in/yaml.v3`；各组件包只允许其直接对应的客户端库加 `github.com/google/wire`。
5. **组件之间互不依赖**，唯一例外是 `component/log`：日志是横切关注点，其他组件可以 import 它，仅用于 `log.FromContext` / `log.NewContext`。这是对设计文档第 3 节的一处明确细化。
6. **文档、目录名、注释、提交信息中不出现 "DDD" 字样**，统一表述为「分层」「领域模型」。
7. **注释与提交信息用中文**。提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。
8. **配置结构体只用值类型字段，不用指针字段**。`config` 的路径覆盖依赖这一点。
9. **每个 Task 结束时提交一次**，提交前 `make test` 必须通过。

### 相对设计文档的两处范围说明

- 设计文档 5.3 节提到的 `config.Watch` 热更新钩子**不在本计划内**。本计划没有任何配置变更来源（没有配置中心、没有文件监听组件），先做钩子等于做一个无人调用的接口。`Logger.SetLevel` 已在 Task 8 落地，等脚手架计划引入配置来源时再补 `Watch`。
- 设计文档 5.1 节的 `HealthChecker` 接口在 Task 2 定义、Task 11 由 `sqldb.DB` 实现，但 `App` 暂不做类型断言探测——健康检查端点属于 `transport` 层，在设计文档第 12 节的第 5 步，不在本计划内。

---

## File Structure

本计划涉及的文件与各自职责：

| 文件 | 职责 |
|---|---|
| `go.mod` / `Makefile` / `.golangci.yml` / `.github/workflows/ci.yml` | 构建与检查基线 |
| `app/component.go` | `Component`、`HealthChecker`、`Dependent` 三个接口 |
| `app/options.go` | `App` 的构造选项 |
| `app/app.go` | `App` 结构、`Register` / `Start` / `Stop` / `Run` / `Fatal` |
| `app/sort.go` | 按 `DependsOn` 做确定性拓扑排序 |
| `app/app_test.go` / `app/sort_test.go` | 内核测试，用假组件驱动 |
| `config/config.go` | `Loader`、`Option`、`Load` |
| `config/path.go` | 反射实现「按点分路径读写结构体字段」 |
| `config/env.go` | 枚举结构体路径、映射到环境变量名 |
| `component/log/log.go` | `Config`、`Logger` 组件、级别热更新 |
| `component/log/context.go` | `NewContext` / `FromContext` |
| `component/log/wire.go` | `ProviderSet` |
| `component/httpserver/server.go` | `Config`、`Server` 组件 |
| `component/httpserver/middleware.go` | `Recover` / `RequestLog` / `Timeout` / `Chain` |
| `component/httpserver/wire.go` | `ProviderSet` |
| `component/sqldb/db.go` | `Config`、`DB` 组件 |
| `component/sqldb/tx.go` | `Tx` 事务助手、`Executor` |
| `component/sqldb/fakedriver_test.go` | 测试用假 SQL 驱动，零外部依赖 |
| `component/sqldb/wire.go` | `ProviderSet` |
| `example/minimal/` | 独立 go.mod 的分层示例，验证整套装配 |

---

## Task 1: 环境与仓库基线

**Files:**
- Create: `go.mod`, `Makefile`, `.golangci.yml`, `.github/workflows/ci.yml`, `doc.go`

**Interfaces:**
- Consumes: 无
- Produces: 可用的 `make build` / `make test` / `make lint`；模块路径 `github.com/Kline-x/gokit`

- [ ] **Step 1: 查出最新稳定版 Go 并装到 `$HOME/sdk`**

```bash
wsl -u gaore bash -lc 'cd ~ && VER=$(curl -sL "https://go.dev/VERSION?m=text" | head -1) && echo "latest=$VER" && mkdir -p ~/sdk && curl -fsSL -o /tmp/$VER.tar.gz "https://dl.google.com/go/$VER.linux-amd64.tar.gz" && rm -rf ~/sdk/$VER && mkdir -p ~/sdk/$VER && tar -C ~/sdk/$VER --strip-components=1 -xzf /tmp/$VER.tar.gz && ln -sfn ~/sdk/$VER ~/sdk/go && ~/sdk/go/bin/go version'
```

预期输出末行形如 `go version go1.2x.y linux/amd64`，且 `1.2x` 不低于 `1.22`。若低于 1.22，停下来报告，不要继续。

- [ ] **Step 2: 初始化 go.mod**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && ~/sdk/go/bin/go mod init github.com/Kline-x/gokit && cat go.mod'
```

预期 `go.mod` 含 `module github.com/Kline-x/gokit` 与一行 `go 1.2x`。

- [ ] **Step 3: 写 Makefile**

`Makefile`（缩进必须是 Tab，不是空格）：

```makefile
GO ?= $(HOME)/sdk/go/bin/go

.PHONY: build test lint tidy example-test all

all: build lint test

build:
	$(GO) build ./...

test:
	$(GO) test -race ./...

lint:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

example-test:
	cd example/minimal && $(GO) test -race ./...
```

- [ ] **Step 4: 写 doc.go 与 golangci 配置**

`doc.go`：

```go
// Package gokit 是一个分层清晰、基础设施组件化的 Go 应用框架。
//
// 内核见 app 包：统一的 Component 生命周期接口与 App 编排器。
// 基础设施见 component 下各子包，彼此独立，按需引用。
package gokit
```

`.golangci.yml`：

```yaml
version: "2"

linters:
  enable:
    - errcheck
    - govet
    - ineffassign
    - staticcheck
    - unused
    - bodyclose

formatters:
  enable:
    - gofmt

issues:
  max-issues-per-linter: 0
  max-same-issues: 0
```

- [ ] **Step 5: 写 CI**

`.github/workflows/ci.yml`：

```yaml
name: CI

on:
  push:
    branches: [master]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - run: go build ./...
      - run: go vet ./...
      - run: go test -race ./...

  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - uses: golangci/golangci-lint-action@v6
        with:
          version: latest
```

- [ ] **Step 6: 安装 wire 工具**

```bash
wsl -u gaore bash -lc 'cd ~ && ~/sdk/go/bin/go install github.com/google/wire/cmd/wire@latest && ls -l ~/go/bin/wire'
```

预期列出 `~/go/bin/wire`。

- [ ] **Step 7: 验证基线可跑**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && make build && make lint'
```

预期两条命令都无输出、退出码 0。`make test` 此时会输出 `no test files`，属正常。

注意 `make lint` 只跑 `go vet`，而 CI 另有一个 job 跑完整的 golangci-lint。本地不装 golangci-lint 也能推进，但 CI 报出 lint 问题时要按 `.golangci.yml` 的规则修，不要改规则绕过。

- [ ] **Step 8: 提交**

```bash
cd /mnt/e/code/AI/vibCoding/gokit
git add go.mod Makefile .golangci.yml .github doc.go
git commit -m "搭建仓库基线：go.mod、Makefile、lint 与 CI

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 2: Component 接口与 App 顺序启停

**Files:**
- Create: `app/component.go`, `app/options.go`, `app/app.go`, `app/app_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `type Component interface { Name() string; Start(context.Context) error; Stop(context.Context) error }`
  - `type HealthChecker interface { Health(context.Context) error }`
  - `type Dependent interface { DependsOn() []string }`
  - `func New(opts ...Option) *App`
  - `func (a *App) Register(cs ...Component)`
  - `func (a *App) Start(ctx context.Context) error`
  - `func (a *App) Stop(ctx context.Context) error`
  - `func WithName(string) Option`、`func WithVersion(string) Option`、`func WithStopTimeout(time.Duration) Option`、`func WithLogger(*slog.Logger) Option`、`func WithSignals(...os.Signal) Option`

- [ ] **Step 1: 写失败的测试**

`app/app_test.go`：

```go
package app

import (
	"context"
	"slices"
	"testing"
)

// fakeComponent 把每次 Start/Stop 记录到共享的 events 切片，用于断言调用顺序。
type fakeComponent struct {
	name      string
	startErr  error
	stopErr   error
	events    *[]string
	dependsOn []string
}

func (f *fakeComponent) Name() string { return f.name }

func (f *fakeComponent) Start(context.Context) error {
	*f.events = append(*f.events, "start:"+f.name)
	return f.startErr
}

func (f *fakeComponent) Stop(context.Context) error {
	*f.events = append(*f.events, "stop:"+f.name)
	return f.stopErr
}

func TestStartInRegistrationOrderAndStopInReverse(t *testing.T) {
	var events []string
	a := New()
	a.Register(
		&fakeComponent{name: "a", events: &events},
		&fakeComponent{name: "b", events: &events},
		&fakeComponent{name: "c", events: &events},
	)

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}

	want := []string{"start:a", "start:b", "start:c", "stop:c", "stop:b", "stop:a"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	var events []string
	a := New()
	a.Register(&fakeComponent{name: "a", events: &events})

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}

	want := []string{"start:a", "stop:a"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v（第二次 Stop 不应重复调用组件）", events, want)
	}
}

func TestRegisterRejectsDuplicateNames(t *testing.T) {
	var events []string
	a := New()
	a.Register(
		&fakeComponent{name: "dup", events: &events},
		&fakeComponent{name: "dup", events: &events},
	)

	err := a.Start(context.Background())
	if err == nil {
		t.Fatal("Start() error = nil, want 重名错误")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
make test
```

预期：编译失败，`undefined: New`。

- [ ] **Step 3: 写 app/component.go**

```go
package app

import "context"

// Component 是所有基础设施的统一生命周期契约。
//
// Start 必须快速返回：像 HTTP、gRPC 这类需要长期阻塞的组件，
// 应在 Start 内部自行起 goroutine，并通过 App.Fatal 上报运行期异常。
type Component interface {
	// Name 返回组件的唯一标识，用于日志与依赖声明。
	Name() string
	// Start 启动组件。返回错误时 App 会逆序停止已启动的组件。
	Start(ctx context.Context) error
	// Stop 停止组件。ctx 带超时，超时后 App 放弃等待。
	Stop(ctx context.Context) error
}

// HealthChecker 是组件的可选能力：对外暴露健康状态。
type HealthChecker interface {
	Health(ctx context.Context) error
}

// Dependent 是组件的可选能力：声明自己依赖哪些组件先启动。
// 返回的是被依赖组件的 Name()。
type Dependent interface {
	DependsOn() []string
}
```

- [ ] **Step 4: 写 app/options.go**

```go
package app

import (
	"log/slog"
	"os"
	"syscall"
	"time"
)

type options struct {
	name        string
	version     string
	stopTimeout time.Duration
	logger      *slog.Logger
	signals     []os.Signal
}

// Option 用于定制 App 的行为。
type Option func(*options)

// WithName 设置应用名，写入启动与退出日志。
func WithName(name string) Option { return func(o *options) { o.name = name } }

// WithVersion 设置应用版本，写入启动日志。
func WithVersion(v string) Option { return func(o *options) { o.version = v } }

// WithStopTimeout 设置整体停止超时。当传给 Stop 的 ctx 没有 deadline 时生效。
func WithStopTimeout(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.stopTimeout = d
		}
	}
}

// WithLogger 替换 App 自身使用的日志器。
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithSignals 替换触发优雅退出的信号集合。传空表示不监听信号。
func WithSignals(sigs ...os.Signal) Option {
	return func(o *options) { o.signals = sigs }
}

func defaultOptions() options {
	return options{
		name:        "gokit-app",
		stopTimeout: 10 * time.Second,
		logger:      slog.Default(),
		signals:     []os.Signal{os.Interrupt, syscall.SIGTERM},
	}
}
```

- [ ] **Step 5: 写 app/app.go**

```go
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// App 编排一组 Component 的生命周期：按依赖顺序启动、逆序停止。
//
// App 不是服务定位器：组件之间的依赖一律由构造时注入解决，
// App 只负责启停顺序，不提供按名字查找组件的能力。
type App struct {
	opts    options
	fatalCh chan error

	mu         sync.Mutex
	components []Component
	started    []Component
	stopped    bool
}

// New 创建一个 App。
func New(opts ...Option) *App {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}
	return &App{opts: o, fatalCh: make(chan error, 1)}
}

// Register 注册组件。未声明依赖时，启动顺序即注册顺序。
func (a *App) Register(cs ...Component) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.components = append(a.components, cs...)
}

// Start 依次启动全部组件。任一组件启动失败时，
// 已启动的组件会被逆序停止，错误原样返回。
func (a *App) Start(ctx context.Context) error {
	a.mu.Lock()
	components := make([]Component, len(a.components))
	copy(components, a.components)
	a.mu.Unlock()

	if err := checkDuplicateNames(components); err != nil {
		return err
	}

	for _, c := range components {
		if err := c.Start(ctx); err != nil {
			startErr := fmt.Errorf("启动组件 %s 失败: %w", c.Name(), err)
			if stopErr := a.Stop(context.WithoutCancel(ctx)); stopErr != nil {
				return errors.Join(startErr, stopErr)
			}
			return startErr
		}
		a.mu.Lock()
		a.started = append(a.started, c)
		a.mu.Unlock()
		a.opts.logger.InfoContext(ctx, "组件已启动", slog.String("component", c.Name()))
	}
	return nil
}

// Stop 逆序停止已启动的组件，并汇总所有错误。重复调用是安全的空操作。
func (a *App) Stop(ctx context.Context) error {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return nil
	}
	a.stopped = true
	started := a.started
	a.started = nil
	a.mu.Unlock()

	var errs []error
	for i := len(started) - 1; i >= 0; i-- {
		c := started[i]
		if err := c.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("停止组件 %s 失败: %w", c.Name(), err))
			continue
		}
		a.opts.logger.InfoContext(ctx, "组件已停止", slog.String("component", c.Name()))
	}
	return errors.Join(errs...)
}

func checkDuplicateNames(cs []Component) error {
	seen := make(map[string]struct{}, len(cs))
	for _, c := range cs {
		if _, dup := seen[c.Name()]; dup {
			return fmt.Errorf("组件名重复: %q", c.Name())
		}
		seen[c.Name()] = struct{}{}
	}
	return nil
}
```

- [ ] **Step 6: 跑测试确认通过**

```bash
make test
```

预期 `ok github.com/Kline-x/gokit/app`，三个测试全过。

- [ ] **Step 7: 提交**

```bash
cd /mnt/e/code/AI/vibCoding/gokit
git add app
git commit -m "内核：Component 接口与 App 顺序启停

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 3: 启动失败回滚与停止超时

**Files:**
- Modify: `app/app.go`（`Stop` 应用 stopTimeout）
- Modify: `app/app_test.go`（追加两个测试与一个假组件）

**Interfaces:**
- Consumes: Task 2 的 `App`、`Component`、`WithStopTimeout`
- Produces: `Stop` 在 ctx 无 deadline 时自动套用 `opts.stopTimeout`

- [ ] **Step 1: 写失败的测试**

在 `app/app_test.go` 末尾追加：

```go
// blockingComponent 的 Stop 会一直阻塞到 ctx 结束，用来验证停止超时。
type blockingComponent struct{ name string }

func (b *blockingComponent) Name() string                { return b.name }
func (b *blockingComponent) Start(context.Context) error { return nil }
func (b *blockingComponent) Stop(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestStartRollsBackAlreadyStartedComponents(t *testing.T) {
	var events []string
	boom := errors.New("boom")
	a := New()
	a.Register(
		&fakeComponent{name: "a", events: &events},
		&fakeComponent{name: "b", events: &events, startErr: boom},
		&fakeComponent{name: "c", events: &events},
	)

	err := a.Start(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("Start() error = %v, want 包装了 boom 的错误", err)
	}

	// b 自身启动失败，不应被 Stop；c 从未启动，也不应被 Stop。
	want := []string{"start:a", "start:b", "stop:a"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
}

func TestStopAppliesStopTimeoutWhenContextHasNoDeadline(t *testing.T) {
	a := New(WithStopTimeout(50 * time.Millisecond))
	a.Register(&blockingComponent{name: "blocker"})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	begin := time.Now()
	err := a.Stop(context.Background())
	elapsed := time.Since(begin)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Errorf("Stop() 耗时 %v，应在 stopTimeout 附近返回", elapsed)
	}
}
```

同时把 `app/app_test.go` 的 import 补成：

```go
import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)
```

- [ ] **Step 2: 跑测试确认失败**

```bash
make test
```

预期 `TestStopAppliesStopTimeoutWhenContextHasNoDeadline` 超时挂起或失败（`Stop` 目前不套超时，会永远阻塞，`go test` 最终以 panic 形式超时）。`TestStartRollsBackAlreadyStartedComponents` 应已通过，因为 Task 2 的 `Start` 已含回滚。

- [ ] **Step 3: 让 Stop 套用 stopTimeout**

把 `app/app.go` 的 `Stop` 开头改成：

```go
func (a *App) Stop(ctx context.Context) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && a.opts.stopTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.opts.stopTimeout)
		defer cancel()
	}

	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return nil
	}
	// 以下保持不变
```

- [ ] **Step 4: 跑测试确认通过**

```bash
make test
```

预期 `ok`，五个测试全过，`TestStopAppliesStopTimeoutWhenContextHasNoDeadline` 在 50ms 量级返回。

- [ ] **Step 5: 提交**

```bash
cd /mnt/e/code/AI/vibCoding/gokit
git add app
git commit -m "内核：启动失败回滚与停止超时

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 4: 依赖拓扑排序

**Files:**
- Create: `app/sort.go`, `app/sort_test.go`
- Modify: `app/app.go`（`Start` 改为按排序结果启动；重名检查移入 `sort.go`）

**Interfaces:**
- Consumes: Task 2 的 `Component`、`Dependent`
- Produces: `func sortComponents(cs []Component) ([]Component, error)`（包内私有）

- [ ] **Step 1: 写失败的测试**

`app/sort_test.go`：

```go
package app

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func (f *fakeComponent) DependsOn() []string { return f.dependsOn }

func names(cs []Component) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name())
	}
	return out
}

func TestSortPutsDependenciesFirst(t *testing.T) {
	var events []string
	// 注册顺序故意反着写：server 依赖 db，db 依赖 log。
	in := []Component{
		&fakeComponent{name: "server", events: &events, dependsOn: []string{"db"}},
		&fakeComponent{name: "db", events: &events, dependsOn: []string{"log"}},
		&fakeComponent{name: "log", events: &events},
	}

	got, err := sortComponents(in)
	if err != nil {
		t.Fatalf("sortComponents() error = %v", err)
	}

	want := []string{"log", "db", "server"}
	if !slices.Equal(names(got), want) {
		t.Errorf("顺序 = %v, want %v", names(got), want)
	}
}

func TestSortKeepsRegistrationOrderAmongIndependentComponents(t *testing.T) {
	var events []string
	in := []Component{
		&fakeComponent{name: "a", events: &events},
		&fakeComponent{name: "b", events: &events},
		&fakeComponent{name: "c", events: &events},
	}

	got, err := sortComponents(in)
	if err != nil {
		t.Fatalf("sortComponents() error = %v", err)
	}
	want := []string{"a", "b", "c"}
	if !slices.Equal(names(got), want) {
		t.Errorf("顺序 = %v, want %v", names(got), want)
	}
}

func TestSortRejectsUnknownDependency(t *testing.T) {
	var events []string
	in := []Component{
		&fakeComponent{name: "a", events: &events, dependsOn: []string{"nope"}},
	}

	_, err := sortComponents(in)
	if err == nil {
		t.Fatal("sortComponents() error = nil, want 未知依赖错误")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("错误信息 = %q, 应指出未知依赖 nope", err.Error())
	}
}

func TestSortRejectsCycle(t *testing.T) {
	var events []string
	in := []Component{
		&fakeComponent{name: "a", events: &events, dependsOn: []string{"b"}},
		&fakeComponent{name: "b", events: &events, dependsOn: []string{"a"}},
	}

	_, err := sortComponents(in)
	if err == nil {
		t.Fatal("sortComponents() error = nil, want 循环依赖错误")
	}
}

func TestAppStartFollowsDependencyOrder(t *testing.T) {
	var events []string
	a := New()
	a.Register(
		&fakeComponent{name: "server", events: &events, dependsOn: []string{"db"}},
		&fakeComponent{name: "db", events: &events},
	)

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	want := []string{"start:db", "start:server", "stop:server", "stop:db"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
}
```

注意：`fakeComponent` 现在实现了 `DependsOn`，`dependsOn` 为 nil 时返回 nil，等价于无依赖。

- [ ] **Step 2: 跑测试确认失败**

```bash
make test
```

预期编译失败，`undefined: sortComponents`。

- [ ] **Step 3: 写 app/sort.go**

```go
package app

import "fmt"

// sortComponents 按 Dependent 声明的依赖做拓扑排序。
//
// 排序是确定性的：每一轮都从头扫描，挑出第一个依赖已全部就绪的组件，
// 因此互不依赖的组件保持注册顺序。
func sortComponents(cs []Component) ([]Component, error) {
	index := make(map[string]int, len(cs))
	for i, c := range cs {
		if _, dup := index[c.Name()]; dup {
			return nil, fmt.Errorf("组件名重复: %q", c.Name())
		}
		index[c.Name()] = i
	}

	indegree := make([]int, len(cs))
	dependents := make([][]int, len(cs)) // dependents[j] 记录依赖 j 的组件下标
	for i, c := range cs {
		d, ok := c.(Dependent)
		if !ok {
			continue
		}
		for _, dep := range d.DependsOn() {
			j, known := index[dep]
			if !known {
				return nil, fmt.Errorf("组件 %q 依赖了未注册的组件 %q", c.Name(), dep)
			}
			if j == i {
				return nil, fmt.Errorf("组件 %q 依赖了自己", c.Name())
			}
			indegree[i]++
			dependents[j] = append(dependents[j], i)
		}
	}

	out := make([]Component, 0, len(cs))
	done := make([]bool, len(cs))
	for len(out) < len(cs) {
		picked := -1
		for i := range cs {
			if !done[i] && indegree[i] == 0 {
				picked = i
				break
			}
		}
		if picked < 0 {
			return nil, fmt.Errorf("组件依赖存在循环，无法确定启动顺序")
		}
		done[picked] = true
		out = append(out, cs[picked])
		for _, k := range dependents[picked] {
			indegree[k]--
		}
	}
	return out, nil
}
```

- [ ] **Step 4: 让 Start 用排序结果**

把 `app/app.go` 中 `Start` 的重名检查换成排序调用：

```go
	components, err := sortComponents(components)
	if err != nil {
		return err
	}
```

并删除 `checkDuplicateNames` 函数及其调用（重名检查已并入 `sortComponents`）。

- [ ] **Step 5: 跑测试确认通过**

```bash
make test
```

预期 `ok`，`app` 包全部测试通过，包含 Task 2 的 `TestRegisterRejectsDuplicateNames`。

- [ ] **Step 6: 提交**

```bash
cd /mnt/e/code/AI/vibCoding/gokit
git add app
git commit -m "内核：按 DependsOn 做确定性拓扑排序

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 5: App.Run — 信号、上下文取消与 Fatal

**Files:**
- Modify: `app/app.go`（新增 `Run`、`Fatal`）
- Create: `app/run_test.go`

**Interfaces:**
- Consumes: Task 2 至 4 的 `App.Start` / `App.Stop`
- Produces:
  - `func (a *App) Run(ctx context.Context) error`
  - `func (a *App) Fatal(err error)`

- [ ] **Step 1: 写失败的测试**

`app/run_test.go`：

```go
package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestRunReturnsWhenContextCancelled(t *testing.T) {
	var events []string
	a := New(WithSignals())
	a.Register(&fakeComponent{name: "a", events: &events})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() 未在 ctx 取消后返回")
	}

	want := []string{"start:a", "stop:a"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
}

func TestRunReturnsFatalError(t *testing.T) {
	var events []string
	boom := errors.New("组件运行期崩了")
	a := New(WithSignals())
	a.Register(&fakeComponent{name: "a", events: &events})

	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background()) }()

	time.Sleep(50 * time.Millisecond)
	a.Fatal(boom)

	select {
	case err := <-done:
		if !errors.Is(err, boom) {
			t.Fatalf("Run() error = %v, want 包含 boom", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() 未在 Fatal 后返回")
	}

	want := []string{"start:a", "stop:a"}
	if !slices.Equal(events, want) {
		t.Errorf("events = %v, want %v（Fatal 后仍应优雅停止）", events, want)
	}
}

func TestRunReturnsStartErrorWithoutBlocking(t *testing.T) {
	var events []string
	boom := errors.New("启动失败")
	a := New(WithSignals())
	a.Register(&fakeComponent{name: "a", events: &events, startErr: boom})

	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background()) }()

	select {
	case err := <-done:
		if !errors.Is(err, boom) {
			t.Fatalf("Run() error = %v, want 包含 boom", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() 在启动失败后应立即返回")
	}
}

func TestFatalDoesNotBlockWhenCalledTwice(t *testing.T) {
	a := New(WithSignals())
	a.Fatal(errors.New("第一个"))

	done := make(chan struct{})
	go func() {
		a.Fatal(errors.New("第二个"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("第二次 Fatal 阻塞了，应当直接丢弃")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
make test
```

预期编译失败，`a.Run undefined` 与 `a.Fatal undefined`。

- [ ] **Step 3: 在 app/app.go 追加 Run 与 Fatal**

先补 import：`"os/signal"`。然后追加：

```go
// Run 启动全部组件并阻塞，直到 ctx 取消、收到退出信号，或有组件通过 Fatal 上报致命错误。
// 返回前会执行一次优雅停止，停止阶段的错误与致命错误一并返回。
func (a *App) Run(ctx context.Context) error {
	if err := a.Start(ctx); err != nil {
		return err
	}

	a.opts.logger.InfoContext(ctx, "应用已启动",
		slog.String("name", a.opts.name),
		slog.String("version", a.opts.version))

	var runErr error
	if len(a.opts.signals) > 0 {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, a.opts.signals...)
		defer signal.Stop(sigCh)

		select {
		case <-ctx.Done():
		case sig := <-sigCh:
			a.opts.logger.InfoContext(ctx, "收到退出信号", slog.String("signal", sig.String()))
		case runErr = <-a.fatalCh:
		}
	} else {
		select {
		case <-ctx.Done():
		case runErr = <-a.fatalCh:
		}
	}

	stopErr := a.Stop(context.WithoutCancel(ctx))
	a.opts.logger.Info("应用已退出", slog.String("name", a.opts.name))
	return errors.Join(runErr, stopErr)
}

// Fatal 供组件在运行期上报致命错误，触发 Run 优雅退出。
// 只保留第一个错误，后续调用直接丢弃，绝不阻塞调用方。
func (a *App) Fatal(err error) {
	if err == nil {
		return
	}
	select {
	case a.fatalCh <- err:
	default:
	}
}
```

同时把 `app/app.go` 的 import 补成：

```go
import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
)
```

- [ ] **Step 4: 跑测试确认通过**

```bash
make test
```

预期 `ok github.com/Kline-x/gokit/app`，全部测试通过。

- [ ] **Step 5: 提交**

```bash
cd /mnt/e/code/AI/vibCoding/gokit
git add app
git commit -m "内核：Run 支持信号、上下文取消与 Fatal 上报

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 6: config — YAML 文件加载

**Files:**
- Create: `config/config.go`, `config/config_test.go`
- Modify: `go.mod`（引入 `gopkg.in/yaml.v3`）

**Interfaces:**
- Consumes: 无
- Produces:
  - `func New(opts ...Option) *Loader`
  - `func (l *Loader) Load(dst any) error`
  - `func WithFile(paths ...string) Option`
  - `func WithOptionalFile(paths ...string) Option`

- [ ] **Step 1: 写失败的测试**

`config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type httpConfig struct {
	Addr        string        `yaml:"addr"`
	ReadTimeout time.Duration `yaml:"read_timeout"`
}

type serverConfig struct {
	HTTP httpConfig `yaml:"http"`
}

type testConfig struct {
	Name    string       `yaml:"name"`
	Debug   bool         `yaml:"debug"`
	Workers int          `yaml:"workers"`
	Tags    []string     `yaml:"tags"`
	Server  serverConfig `yaml:"server"`
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
	return path
}

func TestLoadReadsYAMLAndKeepsUnsetDefaults(t *testing.T) {
	path := writeFile(t, "config.yaml", "name: demo\ntags: [a, b]\nserver:\n  http:\n    addr: \":9000\"\n    read_timeout: 3s\n")

	cfg := testConfig{Workers: 4, Debug: true}
	if err := New(WithFile(path)).Load(&cfg); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Name != "demo" {
		t.Errorf("Name = %q, want %q", cfg.Name, "demo")
	}
	if cfg.Server.HTTP.Addr != ":9000" {
		t.Errorf("Server.HTTP.Addr = %q, want %q", cfg.Server.HTTP.Addr, ":9000")
	}
	if cfg.Server.HTTP.ReadTimeout != 3*time.Second {
		t.Errorf("Server.HTTP.ReadTimeout = %v, want 3s", cfg.Server.HTTP.ReadTimeout)
	}
	if len(cfg.Tags) != 2 || cfg.Tags[0] != "a" || cfg.Tags[1] != "b" {
		t.Errorf("Tags = %v, want [a b]", cfg.Tags)
	}
	if cfg.Workers != 4 {
		t.Errorf("Workers = %d, want 4（yaml 未提供的字段应保留默认值）", cfg.Workers)
	}
	if !cfg.Debug {
		t.Error("Debug = false, want true（yaml 未提供的字段应保留默认值）")
	}
}

func TestLoadAppliesFilesInOrder(t *testing.T) {
	base := writeFile(t, "base.yaml", "name: base\nworkers: 1\n")
	over := writeFile(t, "over.yaml", "workers: 9\n")

	var cfg testConfig
	if err := New(WithFile(base, over)).Load(&cfg); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Name != "base" {
		t.Errorf("Name = %q, want %q", cfg.Name, "base")
	}
	if cfg.Workers != 9 {
		t.Errorf("Workers = %d, want 9（后一个文件应覆盖前一个）", cfg.Workers)
	}
}

func TestLoadFailsOnMissingRequiredFile(t *testing.T) {
	var cfg testConfig
	err := New(WithFile(filepath.Join(t.TempDir(), "nope.yaml"))).Load(&cfg)
	if err == nil {
		t.Fatal("Load() error = nil, want 文件缺失错误")
	}
}

func TestLoadSkipsMissingOptionalFile(t *testing.T) {
	var cfg testConfig
	err := New(WithOptionalFile(filepath.Join(t.TempDir(), "nope.yaml"))).Load(&cfg)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil（可选文件缺失应跳过）", err)
	}
}

func TestLoadRejectsNonPointerDestination(t *testing.T) {
	var cfg testConfig
	if err := New().Load(cfg); err == nil {
		t.Fatal("Load() error = nil, want 非指针错误")
	}
}
```

- [ ] **Step 2: 引入 yaml 依赖并跑测试确认失败**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && ~/sdk/go/bin/go get gopkg.in/yaml.v3'
```

然后 `make test`。预期编译失败，`undefined: New`。

- [ ] **Step 3: 写 config/config.go**

```go
// Package config 负责把 YAML 文件、环境变量与显式覆盖逐级合并进配置结构体。
//
// 合并顺序固定为：文件（按传入顺序）→ 环境变量 → 显式覆盖。
// 配置结构体的字段只能用值类型，不能用指针，否则路径覆盖无法定位字段。
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"

	"gopkg.in/yaml.v3"
)

type fileSource struct {
	path     string
	optional bool
}

// Loader 按既定顺序合并多个配置来源。
type Loader struct {
	files []fileSource
}

// Option 用于定制 Loader。
type Option func(*Loader)

// WithFile 追加必需的 YAML 文件。文件不存在时 Load 返回错误。
func WithFile(paths ...string) Option {
	return func(l *Loader) {
		for _, p := range paths {
			l.files = append(l.files, fileSource{path: p})
		}
	}
}

// WithOptionalFile 追加可选的 YAML 文件。文件不存在时静默跳过。
func WithOptionalFile(paths ...string) Option {
	return func(l *Loader) {
		for _, p := range paths {
			l.files = append(l.files, fileSource{path: p, optional: true})
		}
	}
}

// New 创建一个 Loader。
func New(opts ...Option) *Loader {
	l := &Loader{}
	for _, fn := range opts {
		fn(l)
	}
	return l
}

// Load 把各配置来源依次合并进 dst。dst 必须是指向结构体的非空指针。
// 某个来源未提供的字段保持 dst 的原值，因此调用方可以先填好默认值再 Load。
func (l *Loader) Load(dst any) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config: dst 必须是指向结构体的非空指针，得到 %T", dst)
	}

	for _, f := range l.files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			if f.optional && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("config: 读取 %s 失败: %w", f.path, err)
		}
		if err := yaml.Unmarshal(data, dst); err != nil {
			return fmt.Errorf("config: 解析 %s 失败: %w", f.path, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

`make tidy && make test`。预期 `ok github.com/Kline-x/gokit/config`，五个测试全过。

- [ ] **Step 5: 提交**

```bash
git add config go.mod go.sum
git commit -m "配置：YAML 文件按序加载"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 7: config — 路径覆盖与环境变量

**Files:**
- Create: `config/path.go`, `config/env.go`, `config/path_test.go`, `config/env_test.go`
- Modify: `config/config.go`（新增 `WithEnvPrefix`、`WithOverride`，并在 `Load` 中应用）

**Interfaces:**
- Consumes: Task 6 的 `Loader`、`Option`、`Load`、测试里的 `testConfig` 与 `writeFile`
- Produces:
  - `func SetPath(dst any, path, value string) error`
  - `func Paths(dst any) []string`
  - `func EnvName(prefix, path string) string`
  - `func WithEnvPrefix(prefix string) Option`
  - `func WithOverride(kv map[string]string) Option`

- [ ] **Step 1: 写失败的测试**

`config/path_test.go`:

```go
package config

import (
	"testing"
	"time"
)

func TestSetPathSetsNestedScalars(t *testing.T) {
	cases := []struct {
		path  string
		value string
		check func(*testing.T, testConfig)
	}{
		{"name", "abc", func(t *testing.T, c testConfig) {
			if c.Name != "abc" {
				t.Errorf("Name = %q, want %q", c.Name, "abc")
			}
		}},
		{"debug", "true", func(t *testing.T, c testConfig) {
			if !c.Debug {
				t.Error("Debug = false, want true")
			}
		}},
		{"workers", "12", func(t *testing.T, c testConfig) {
			if c.Workers != 12 {
				t.Errorf("Workers = %d, want 12", c.Workers)
			}
		}},
		{"tags", "x, y ,z", func(t *testing.T, c testConfig) {
			if len(c.Tags) != 3 || c.Tags[0] != "x" || c.Tags[2] != "z" {
				t.Errorf("Tags = %v, want [x y z]", c.Tags)
			}
		}},
		{"server.http.addr", "0.0.0.0:80", func(t *testing.T, c testConfig) {
			if c.Server.HTTP.Addr != "0.0.0.0:80" {
				t.Errorf("Addr = %q, want %q", c.Server.HTTP.Addr, "0.0.0.0:80")
			}
		}},
		{"server.http.read_timeout", "1500ms", func(t *testing.T, c testConfig) {
			if c.Server.HTTP.ReadTimeout != 1500*time.Millisecond {
				t.Errorf("ReadTimeout = %v, want 1.5s", c.Server.HTTP.ReadTimeout)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			var got testConfig
			if err := SetPath(&got, tc.path, tc.value); err != nil {
				t.Fatalf("SetPath(%q, %q) error = %v", tc.path, tc.value, err)
			}
			tc.check(t, got)
		})
	}
}

func TestSetPathRejectsUnknownPath(t *testing.T) {
	var cfg testConfig
	if err := SetPath(&cfg, "server.http.nope", "1"); err == nil {
		t.Fatal("SetPath() error = nil, want 未知路径错误")
	}
}

func TestSetPathRejectsBadValue(t *testing.T) {
	var cfg testConfig
	if err := SetPath(&cfg, "workers", "not-a-number"); err == nil {
		t.Fatal("SetPath() error = nil, want 数值解析错误")
	}
}

func TestPathsEnumeratesLeafFieldsInOrder(t *testing.T) {
	got := Paths(&testConfig{})
	want := []string{
		"name", "debug", "workers", "tags",
		"server.http.addr", "server.http.read_timeout",
	}
	if len(got) != len(want) {
		t.Fatalf("Paths() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Paths() = %v, want %v", got, want)
		}
	}
}
```

`config/env_test.go`:

```go
package config

import "testing"

func TestEnvNameMapsDottedPath(t *testing.T) {
	if got := EnvName("APP", "server.http.addr"); got != "APP_SERVER_HTTP_ADDR" {
		t.Errorf("EnvName() = %q, want %q", got, "APP_SERVER_HTTP_ADDR")
	}
	if got := EnvName("", "name"); got != "NAME" {
		t.Errorf("EnvName() = %q, want %q", got, "NAME")
	}
}

func TestLoadAppliesEnvOverFile(t *testing.T) {
	path := writeFile(t, "config.yaml", "name: from-file\nworkers: 1\n")
	t.Setenv("APP_WORKERS", "7")

	var cfg testConfig
	if err := New(WithFile(path), WithEnvPrefix("APP")).Load(&cfg); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Name != "from-file" {
		t.Errorf("Name = %q, want %q", cfg.Name, "from-file")
	}
	if cfg.Workers != 7 {
		t.Errorf("Workers = %d, want 7（环境变量应覆盖文件）", cfg.Workers)
	}
}

func TestLoadAppliesOverrideOverEnv(t *testing.T) {
	t.Setenv("APP_WORKERS", "7")

	var cfg testConfig
	err := New(
		WithEnvPrefix("APP"),
		WithOverride(map[string]string{"workers": "99"}),
	).Load(&cfg)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Workers != 99 {
		t.Errorf("Workers = %d, want 99（显式覆盖应压过环境变量）", cfg.Workers)
	}
}

func TestLoadFailsOnUnknownOverridePath(t *testing.T) {
	var cfg testConfig
	err := New(WithOverride(map[string]string{"nope": "1"})).Load(&cfg)
	if err == nil {
		t.Fatal("Load() error = nil, want 未知路径错误")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

`make test`。预期编译失败：`undefined: SetPath`、`undefined: Paths`、`undefined: EnvName`、`undefined: WithEnvPrefix`、`undefined: WithOverride`。

- [ ] **Step 3: 写 config/path.go**

```go
package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var durationType = reflect.TypeOf(time.Duration(0))

// SetPath 按点分路径设置结构体字段，例如 SetPath(&cfg, "server.http.addr", ":80")。
// 路径的每一段对应字段的 yaml 标签名，无标签时取字段名的小写形式。
func SetPath(dst any, path, value string) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("config: dst 必须是非空指针，得到 %T", dst)
	}
	v = v.Elem()

	for _, part := range strings.Split(path, ".") {
		if v.Kind() != reflect.Struct {
			return fmt.Errorf("config: 路径 %q 中的 %q 不是结构体字段", path, part)
		}
		f, ok := fieldByConfigName(v, part)
		if !ok {
			return fmt.Errorf("config: 未知配置路径 %q", path)
		}
		v = f
	}
	if err := setScalar(v, value); err != nil {
		return fmt.Errorf("config: 设置 %q 失败: %w", path, err)
	}
	return nil
}

func fieldByConfigName(v reflect.Value, name string) (reflect.Value, bool) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" {
			continue
		}
		cn := configName(sf)
		if cn == "-" || cn != name {
			continue
		}
		return v.Field(i), true
	}
	return reflect.Value{}, false
}

func configName(sf reflect.StructField) string {
	tag := sf.Tag.Get("yaml")
	if tag == "" {
		return strings.ToLower(sf.Name)
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		return strings.ToLower(sf.Name)
	}
	return name
}

func setScalar(v reflect.Value, s string) error {
	if !v.CanSet() {
		return fmt.Errorf("字段不可写")
	}
	if v.Type() == durationType {
		d, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("无法解析时长 %q: %w", s, err)
		}
		v.SetInt(int64(d))
		return nil
	}

	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("无法解析布尔值 %q: %w", s, err)
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("无法解析整数 %q: %w", s, err)
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("无法解析无符号整数 %q: %w", s, err)
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(s, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("无法解析浮点数 %q: %w", s, err)
		}
		v.SetFloat(f)
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("只支持字符串切片，得到 %s", v.Type())
		}
		if strings.TrimSpace(s) == "" {
			v.Set(reflect.MakeSlice(v.Type(), 0, 0))
			return nil
		}
		parts := strings.Split(s, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		v.Set(reflect.ValueOf(parts))
	default:
		return fmt.Errorf("不支持的字段类型 %s", v.Type())
	}
	return nil
}
```

- [ ] **Step 4: 写 config/env.go**

```go
package config

import (
	"os"
	"reflect"
	"strings"
)

// Paths 按声明顺序枚举结构体的全部叶子字段路径。
// 嵌套结构体会被递归展开；time.Duration 视为叶子。
func Paths(dst any) []string {
	v := reflect.ValueOf(dst)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	var out []string
	collectPaths(v, "", &out)
	return out
}

func collectPaths(v reflect.Value, prefix string, out *[]string) {
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" {
			continue
		}
		name := configName(sf)
		if name == "-" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		fv := v.Field(i)
		if fv.Kind() == reflect.Struct && fv.Type() != durationType {
			collectPaths(fv, path, out)
			continue
		}
		*out = append(*out, path)
	}
}

// EnvName 把配置路径映射成环境变量名，
// 例如 ("APP", "server.http.addr") 得到 APP_SERVER_HTTP_ADDR。
func EnvName(prefix, path string) string {
	name := strings.ToUpper(strings.ReplaceAll(path, ".", "_"))
	if prefix == "" {
		return name
	}
	return strings.ToUpper(prefix) + "_" + name
}

func applyEnv(dst any, prefix string) error {
	for _, path := range Paths(dst) {
		raw, ok := os.LookupEnv(EnvName(prefix, path))
		if !ok {
			continue
		}
		if err := SetPath(dst, path, raw); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 5: 把两个新来源接进 Load**

`config/config.go` 的 `Loader` 补两个字段：

```go
type Loader struct {
	files     []fileSource
	envPrefix string
	overrides map[string]string
}
```

追加两个 Option：

```go
// WithEnvPrefix 开启环境变量覆盖。变量名由前缀与配置路径拼成，
// 例如前缀 APP、路径 server.http.addr 对应 APP_SERVER_HTTP_ADDR。
func WithEnvPrefix(prefix string) Option {
	return func(l *Loader) { l.envPrefix = prefix }
}

// WithOverride 追加显式覆盖，键为配置路径。通常来自命令行 --set key=value。
func WithOverride(kv map[string]string) Option {
	return func(l *Loader) {
		if l.overrides == nil {
			l.overrides = make(map[string]string, len(kv))
		}
		for k, v := range kv {
			l.overrides[k] = v
		}
	}
}
```

在 `Load` 的文件循环之后、`return nil` 之前插入：

```go
	if l.envPrefix != "" {
		if err := applyEnv(dst, l.envPrefix); err != nil {
			return err
		}
	}

	// 按路径排序，保证多个覆盖出错时的报错顺序稳定。
	paths := make([]string, 0, len(l.overrides))
	for p := range l.overrides {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := SetPath(dst, p, l.overrides[p]); err != nil {
			return err
		}
	}
```

把 `"sort"` 加进 `config/config.go` 的 import。

- [ ] **Step 6: 跑测试确认通过**

`make test`。预期 `ok github.com/Kline-x/gokit/config`，全部测试通过。

- [ ] **Step 7: 提交**

```bash
git add config
git commit -m "配置：路径覆盖与环境变量注入"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 8: component/log — 日志组件

**Files:**
- Create: `component/log/log.go`, `component/log/context.go`, `component/log/wire.go`, `component/log/log_test.go`, `component/log/context_test.go`
- Modify: `go.mod`（引入 `github.com/google/wire`）

**Interfaces:**
- Consumes: Task 2 的 `app.Component` 契约（本包不 import `app`，仅按其方法集实现）
- Produces:
  - `type Config struct { Level string; Format string; Output string }`
  - `func DefaultConfig() Config`
  - `func New(cfg Config) (*Logger, error)`
  - `type Logger struct { *slog.Logger; ... }`，含 `Name() string`、`Start(context.Context) error`、`Stop(context.Context) error`
  - `func (l *Logger) SetLevel(level string) error`
  - `func NewContext(ctx context.Context, l *slog.Logger) context.Context`
  - `func FromContext(ctx context.Context) *slog.Logger`
  - `var ProviderSet` （`wire.ProviderSet`）

- [ ] **Step 1: 写失败的测试**

`component/log/log_test.go`:

```go
package log

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewWritesJSONToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	l, err := New(Config{Level: "info", Format: "json", Output: path})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	l.Info("你好", "k", "v")
	if err := l.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &rec); err != nil {
		t.Fatalf("日志不是合法 JSON: %v, 内容=%q", err, string(data))
	}
	if rec["msg"] != "你好" {
		t.Errorf("msg = %v, want 你好", rec["msg"])
	}
	if rec["k"] != "v" {
		t.Errorf("k = %v, want v", rec["k"])
	}
}

func TestLevelFiltersAndSetLevelTakesEffect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	l, err := New(Config{Level: "warn", Format: "json", Output: path})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = l.Stop(context.Background()) }()

	l.Info("被过滤掉")
	if err := l.SetLevel("debug"); err != nil {
		t.Fatalf("SetLevel() error = %v", err)
	}
	l.Debug("应当写出")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "被过滤掉") {
		t.Error("warn 级别下 Info 日志不应写出")
	}
	if !strings.Contains(content, "应当写出") {
		t.Error("SetLevel(debug) 后 Debug 日志应写出")
	}
}

func TestNewRejectsUnknownLevelAndFormat(t *testing.T) {
	if _, err := New(Config{Level: "nope"}); err == nil {
		t.Error("New() 未知级别应报错")
	}
	if _, err := New(Config{Format: "xml"}); err == nil {
		t.Error("New() 未知格式应报错")
	}
}

func TestLoggerSatisfiesComponentMethodSet(t *testing.T) {
	l, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if l.Name() != "log" {
		t.Errorf("Name() = %q, want %q", l.Name(), "log")
	}
	if err := l.Start(context.Background()); err != nil {
		t.Errorf("Start() error = %v", err)
	}
	if err := l.Stop(context.Background()); err != nil {
		t.Errorf("Stop() error = %v", err)
	}
}
```

`component/log/context_test.go`:

```go
package log

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestFromContextReturnsDefaultWhenAbsent(t *testing.T) {
	if got := FromContext(context.Background()); got == nil {
		t.Fatal("FromContext() = nil, want 默认 logger")
	}
}

func TestFromContextRoundTrip(t *testing.T) {
	want := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := NewContext(context.Background(), want)
	if got := FromContext(ctx); got != want {
		t.Errorf("FromContext() 返回的不是放进去的那个 logger")
	}
}

func TestNewContextIgnoresNilLogger(t *testing.T) {
	ctx := NewContext(context.Background(), nil)
	if got := FromContext(ctx); got == nil {
		t.Fatal("FromContext() = nil, want 默认 logger")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

`make test`。预期编译失败：`undefined: New`、`undefined: FromContext`。

- [ ] **Step 3: 写 component/log/log.go**

```go
// Package log 提供基于标准库 log/slog 的日志组件。
//
// 日志是横切关注点：其他组件可以 import 本包，用 FromContext 取出
// 当前请求的 logger。反过来，本包不 import 任何其他组件。
package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config 是日志组件的配置。
type Config struct {
	// Level 取 debug / info / warn / error，默认 info。
	Level string `yaml:"level"`
	// Format 取 json / text，默认 json。
	Format string `yaml:"format"`
	// Output 取 stdout / stderr 或文件路径，默认 stdout。
	Output string `yaml:"output"`
}

// DefaultConfig 返回可直接使用的默认配置。
func DefaultConfig() Config {
	return Config{Level: "info", Format: "json", Output: "stdout"}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.Level == "" {
		c.Level = d.Level
	}
	if c.Format == "" {
		c.Format = d.Format
	}
	if c.Output == "" {
		c.Output = d.Output
	}
	return c
}

// Logger 是实现了 app.Component 方法集的日志器。
type Logger struct {
	*slog.Logger
	level  *slog.LevelVar
	closer io.Closer
}

// New 按配置创建日志组件。Output 指向文件时会创建或追加该文件。
func New(cfg Config) (*Logger, error) {
	cfg = cfg.withDefaults()

	lv := new(slog.LevelVar)
	if err := parseLevel(cfg.Level, lv); err != nil {
		return nil, err
	}

	w, closer, err := openOutput(cfg.Output)
	if err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{Level: lv}
	var h slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "json":
		h = slog.NewJSONHandler(w, opts)
	case "text":
		h = slog.NewTextHandler(w, opts)
	default:
		if closer != nil {
			_ = closer.Close()
		}
		return nil, fmt.Errorf("log: 未知日志格式 %q，仅支持 json 与 text", cfg.Format)
	}

	return &Logger{Logger: slog.New(h), level: lv, closer: closer}, nil
}

// Name 实现 app.Component。
func (l *Logger) Name() string { return "log" }

// Start 实现 app.Component。日志器在 New 时已就绪，这里是空操作。
func (l *Logger) Start(context.Context) error { return nil }

// Stop 实现 app.Component。输出为文件时关闭文件句柄。
func (l *Logger) Stop(context.Context) error {
	if l.closer == nil {
		return nil
	}
	if err := l.closer.Close(); err != nil {
		return fmt.Errorf("log: 关闭日志文件失败: %w", err)
	}
	return nil
}

// SetLevel 在运行期调整日志级别，用于配置热更新。
func (l *Logger) SetLevel(level string) error { return parseLevel(level, l.level) }

func parseLevel(s string, lv *slog.LevelVar) error {
	switch strings.ToLower(s) {
	case "debug":
		lv.Set(slog.LevelDebug)
	case "info", "":
		lv.Set(slog.LevelInfo)
	case "warn", "warning":
		lv.Set(slog.LevelWarn)
	case "error":
		lv.Set(slog.LevelError)
	default:
		return fmt.Errorf("log: 未知日志级别 %q，仅支持 debug/info/warn/error", s)
	}
	return nil
}

func openOutput(out string) (io.Writer, io.Closer, error) {
	switch strings.ToLower(out) {
	case "stdout":
		return os.Stdout, nil, nil
	case "stderr":
		return os.Stderr, nil, nil
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("log: 打开日志文件 %s 失败: %w", out, err)
	}
	return f, f, nil
}
```

- [ ] **Step 4: 写 component/log/context.go**

```go
package log

import (
	"context"
	"log/slog"
)

type ctxKey struct{}

// NewContext 把 logger 放进 ctx，供下游用 FromContext 取出。传 nil 时原样返回 ctx。
func NewContext(ctx context.Context, l *slog.Logger) context.Context {
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext 取出 ctx 中的 logger，没有时回退到 slog.Default()。
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
```

- [ ] **Step 5: 写 component/log/wire.go**

```go
package log

import "github.com/google/wire"

// ProviderSet 供 wire 装配日志组件。
// 使用方需自行提供 Config，并把 *Logger 注册进 app.App。
var ProviderSet = wire.NewSet(New)
```

- [ ] **Step 6: 引入 wire 依赖并跑测试确认通过**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && ~/sdk/go/bin/go get github.com/google/wire'
```

然后 `make tidy && make test`。预期 `ok github.com/Kline-x/gokit/component/log`，七个测试全过。

- [ ] **Step 7: 提交**

```bash
git add component go.mod go.sum
git commit -m "组件：基于 slog 的日志组件与上下文透传"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 9: component/httpserver — 服务生命周期

**Files:**
- Create: `component/httpserver/server.go`, `component/httpserver/wire.go`, `component/httpserver/server_test.go`

**Interfaces:**
- Consumes: 无（本包不 import `app`，仅按 `app.Component` 的方法集实现）
- Produces:
  - `type Config struct { Addr string; ReadTimeout, WriteTimeout, IdleTimeout, ShutdownTimeout time.Duration }`
  - `func DefaultConfig() Config`
  - `func New(cfg Config, h http.Handler, opts ...Option) *Server`
  - `func WithFatal(fn func(error)) Option`
  - `func (s *Server) Name() string`、`Start(context.Context) error`、`Stop(context.Context) error`、`Addr() net.Addr`
  - `var ProviderSet`

- [ ] **Step 1: 写失败的测试**

`component/httpserver/server_test.go`:

```go
package httpserver

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func newTestServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	mux := http.NewServeMux()
	// 方法路由是 Go 1.22 起的标准库能力，这里顺带验证工具链版本。
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "pong")
	})
	return New(Config{Addr: "127.0.0.1:0"}, mux, opts...)
}

func TestServerServesRequests(t *testing.T) {
	s := newTestServer(t)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	if s.Addr() == nil {
		t.Fatal("Addr() = nil，Start 之后应能拿到真实监听地址")
	}

	resp, err := http.Get("http://" + s.Addr().String() + "/ping")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "pong" {
		t.Errorf("body = %q, want %q", string(body), "pong")
	}
}

func TestStopMakesServerUnreachable(t *testing.T) {
	s := newTestServer(t)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	addr := s.Addr().String()

	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	client := &http.Client{Timeout: time.Second}
	if _, err := client.Get("http://" + addr + "/ping"); err == nil {
		t.Error("Stop 之后请求仍然成功，服务未真正关闭")
	}
}

func TestStartFailsOnOccupiedAddress(t *testing.T) {
	first := newTestServer(t)
	if err := first.Start(context.Background()); err != nil {
		t.Fatalf("第一个 Start() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Stop(context.Background()) })

	mux := http.NewServeMux()
	second := New(Config{Addr: first.Addr().String()}, mux)
	if err := second.Start(context.Background()); err == nil {
		_ = second.Stop(context.Background())
		t.Fatal("Start() error = nil, want 端口占用错误")
	}
}

func TestServerName(t *testing.T) {
	if got := newTestServer(t).Name(); got != "httpserver" {
		t.Errorf("Name() = %q, want %q", got, "httpserver")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

`make test`。预期编译失败，`undefined: New`。

- [ ] **Step 3: 写 component/httpserver/server.go**

```go
// Package httpserver 提供基于标准库 net/http 的 HTTP 服务组件。
//
// 路由由使用方以 http.Handler 的形式传入，本包不绑定任何路由框架。
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Config 是 HTTP 服务组件的配置。
type Config struct {
	// Addr 是监听地址，形如 :8080。测试中可用 127.0.0.1:0 让系统分配端口。
	Addr string `yaml:"addr"`
	// ReadTimeout 是读取整个请求（含 body）的超时。
	ReadTimeout time.Duration `yaml:"read_timeout"`
	// WriteTimeout 是写响应的超时。
	WriteTimeout time.Duration `yaml:"write_timeout"`
	// IdleTimeout 是 keep-alive 连接的空闲超时。
	IdleTimeout time.Duration `yaml:"idle_timeout"`
	// ShutdownTimeout 是优雅关闭时等待在途请求的上限。
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// DefaultConfig 返回一组可直接用于生产的保守默认值。
func DefaultConfig() Config {
	return Config{
		Addr:            ":8080",
		ReadTimeout:     15 * time.Second,
		WriteTimeout:    15 * time.Second,
		IdleTimeout:     60 * time.Second,
		ShutdownTimeout: 10 * time.Second,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.Addr == "" {
		c.Addr = d.Addr
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = d.ReadTimeout
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = d.WriteTimeout
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = d.IdleTimeout
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = d.ShutdownTimeout
	}
	return c
}

// Option 用于定制 Server。
type Option func(*Server)

// WithFatal 注册运行期异常回调。Serve 意外退出时会被调用，
// 典型用法是传入 app.App 的 Fatal 方法，触发整体优雅退出。
func WithFatal(fn func(error)) Option {
	return func(s *Server) { s.onFatal = fn }
}

// Server 是实现了 app.Component 方法集的 HTTP 服务。
type Server struct {
	cfg     Config
	srv     *http.Server
	onFatal func(error)

	mu sync.Mutex
	ln net.Listener
}

// New 创建 HTTP 服务组件。h 为空时使用 http.DefaultServeMux。
func New(cfg Config, h http.Handler, opts ...Option) *Server {
	cfg = cfg.withDefaults()
	s := &Server{
		cfg: cfg,
		srv: &http.Server{
			Handler:      h,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
			IdleTimeout:  cfg.IdleTimeout,
		},
	}
	for _, fn := range opts {
		fn(s)
	}
	return s
}

// Name 实现 app.Component。
func (s *Server) Name() string { return "httpserver" }

// Start 实现 app.Component。
// 监听动作是同步的，因此 Start 返回后 Addr() 即可用；Serve 在后台 goroutine 中运行。
func (s *Server) Start(context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("httpserver: 监听 %s 失败: %w", s.cfg.Addr, err)
	}

	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()

	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			if s.onFatal != nil {
				s.onFatal(fmt.Errorf("httpserver: 服务异常退出: %w", err))
			}
		}
	}()
	return nil
}

// Stop 实现 app.Component，优雅关闭并等待在途请求。
func (s *Server) Stop(ctx context.Context) error {
	if s.cfg.ShutdownTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.ShutdownTimeout)
		defer cancel()
	}
	if err := s.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("httpserver: 关闭失败: %w", err)
	}
	return nil
}

// Addr 返回真实监听地址。Start 之前返回 nil。
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}
```

- [ ] **Step 4: 写 component/httpserver/wire.go**

```go
package httpserver

import "github.com/google/wire"

// ProviderSet 供 wire 装配 HTTP 服务组件。
// 使用方需自行提供 Config 与 http.Handler。
var ProviderSet = wire.NewSet(New)
```

- [ ] **Step 5: 跑测试确认通过**

`make test`。预期 `ok github.com/Kline-x/gokit/component/httpserver`，四个测试全过。

若 `TestServerServesRequests` 报 `invalid pattern "GET /ping"`，说明工具链低于 1.22，回到 Task 1 检查 `$HOME/sdk/go` 的版本。

- [ ] **Step 6: 提交**

```bash
git add component
git commit -m "组件：HTTP 服务生命周期"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 10: component/httpserver — 中间件

**Files:**
- Create: `component/httpserver/middleware.go`, `component/httpserver/middleware_test.go`

**Interfaces:**
- Consumes: Task 8 的 `log.NewContext` / `log.FromContext`
- Produces:
  - `func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler`
  - `func Recover(logger *slog.Logger) func(http.Handler) http.Handler`
  - `func RequestLog(logger *slog.Logger) func(http.Handler) http.Handler`
  - `func Timeout(d time.Duration) func(http.Handler) http.Handler`

- [ ] **Step 1: 写失败的测试**

`component/httpserver/middleware_test.go`:

```go
package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Kline-x/gokit/component/log"
)

func TestChainAppliesMiddlewareOutsideIn(t *testing.T) {
	var order []string
	mark := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	h := Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			order = append(order, "handler")
		}),
		mark("first"), mark("second"),
	)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"first", "second", "handler"}
	if !slices.Equal(order, want) {
		t.Errorf("执行顺序 = %v, want %v", order, want)
	}
}

func TestRecoverTurnsPanicInto500(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("日志未记录 panic 内容，实际为 %q", buf.String())
	}
}

func TestRequestLogRecordsResultAndInjectsLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	var gotInjected bool
	h := RequestLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotInjected = log.FromContext(r.Context()) == logger
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if !gotInjected {
		t.Error("处理函数未能从请求 ctx 中取到注入的 logger")
	}

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("日志不是合法 JSON: %v, 内容=%q", err, buf.String())
	}
	if entry["method"] != http.MethodGet {
		t.Errorf("method = %v, want GET", entry["method"])
	}
	if entry["path"] != "/x" {
		t.Errorf("path = %v, want /x", entry["path"])
	}
	if entry["status"] != float64(http.StatusTeapot) {
		t.Errorf("status = %v, want %d", entry["status"], http.StatusTeapot)
	}
}

func TestRequestLogDefaultsStatusTo200(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := RequestLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/y", nil))

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("日志不是合法 JSON: %v", err)
	}
	if entry["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200（处理函数未显式 WriteHeader 时）", entry["status"])
	}
}

func TestTimeoutReturns503ForSlowHandler(t *testing.T) {
	h := Timeout(20 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

`make test`。预期编译失败，`undefined: Chain`、`undefined: Recover`、`undefined: RequestLog`、`undefined: Timeout`。

- [ ] **Step 3: 写 component/httpserver/middleware.go**

```go
package httpserver

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/Kline-x/gokit/component/log"
)

// Chain 把中间件按传入顺序由外向内套在 h 上：
// Chain(h, a, b) 的执行顺序是 a → b → h。
func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// Recover 捕获处理链中的 panic，记录堆栈并返回 500，避免整个进程崩溃。
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				logger.ErrorContext(r.Context(), "http 处理过程中发生 panic",
					slog.Any("panic", rec),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(debug.Stack())),
				)
				w.WriteHeader(http.StatusInternalServerError)
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder 记录真实写出的状态码，供 RequestLog 使用。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// RequestLog 记录每个请求的方法、路径、状态码与耗时，
// 同时把 logger 放进请求 ctx，供业务代码用 log.FromContext 取用。
func RequestLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			begin := time.Now()
			ctx := log.NewContext(r.Context(), logger)
			rec := &statusRecorder{ResponseWriter: w}

			next.ServeHTTP(rec, r.WithContext(ctx))

			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			logger.InfoContext(ctx, "http 请求",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("bytes", rec.bytes),
				slog.Duration("latency", time.Since(begin)),
			)
		})
	}
}

// Timeout 给处理链加上整体超时，超时返回 503。
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, d, "请求处理超时")
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

`make test`。预期 `ok github.com/Kline-x/gokit/component/httpserver`，全部测试通过。

- [ ] **Step 5: 提交**

```bash
git add component
git commit -m "组件：HTTP recover、请求日志与超时中间件"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 11: component/sqldb — 连接池与生命周期

**Files:**
- Create: `component/sqldb/db.go`, `component/sqldb/wire.go`, `component/sqldb/fakedriver_test.go`, `component/sqldb/db_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `type Config struct { Driver, DSN string; MaxOpenConns, MaxIdleConns int; ConnMaxLifetime, ConnMaxIdleTime, PingTimeout time.Duration }`
  - `func DefaultConfig() Config`
  - `func New(cfg Config) (*DB, error)`
  - `type DB struct { *sql.DB; ... }`，含 `Name()`、`Start(ctx)`、`Stop(ctx)`、`Health(ctx)`
  - `var ProviderSet`
- 测试内部产出：`func registerFakeDriver(t *testing.T) (string, *fakeDriver)`，供 Task 12 复用

- [ ] **Step 1: 写测试用假驱动**

`component/sqldb/fakedriver_test.go`：一个零外部依赖的 `database/sql` 驱动，用来在没有真实数据库的前提下验证连接、Ping 与事务语义。

```go
package sqldb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

var driverSeq atomic.Int64

// fakeDriver 是测试专用的 database/sql 驱动，记录打开次数与事务的提交、回滚次数。
type fakeDriver struct {
	mu        sync.Mutex
	opened    int
	openErr   error
	pingErr   error
	commits   atomic.Int32
	rollbacks atomic.Int32
}

func (d *fakeDriver) Open(string) (driver.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.openErr != nil {
		return nil, d.openErr
	}
	d.opened++
	return &fakeConn{drv: d}, nil
}

func (d *fakeDriver) setPingErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pingErr = err
}

func (d *fakeDriver) currentPingErr() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pingErr
}

type fakeConn struct{ drv *fakeDriver }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return &fakeStmt{}, nil }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return &fakeTx{drv: c.drv}, nil }
func (c *fakeConn) Ping(context.Context) error          { return c.drv.currentPingErr() }

type fakeTx struct{ drv *fakeDriver }

func (t *fakeTx) Commit() error {
	t.drv.commits.Add(1)
	return nil
}

func (t *fakeTx) Rollback() error {
	t.drv.rollbacks.Add(1)
	return nil
}

type fakeStmt struct{}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }
func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) { return &fakeRows{}, nil }

type fakeRows struct{ done bool }

func (r *fakeRows) Columns() []string { return []string{"n"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = int64(1)
	return nil
}

// registerFakeDriver 注册一个独占的假驱动，返回驱动名与驱动实例。
// 每次调用用不同的名字，避免 sql.Register 重名 panic。
func registerFakeDriver(t *testing.T) (string, *fakeDriver) {
	t.Helper()
	drv := &fakeDriver{}
	name := fmt.Sprintf("gokit-fake-%d", driverSeq.Add(1))
	sql.Register(name, drv)
	return name, drv
}
```

- [ ] **Step 2: 写失败的测试**

`component/sqldb/db_test.go`:

```go
package sqldb

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStartPingsAndStopCloses(t *testing.T) {
	name, _ := registerFakeDriver(t)

	db, err := New(Config{Driver: name, DSN: "fake"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if db.Name() != "sqldb" {
		t.Errorf("Name() = %q, want %q", db.Name(), "sqldb")
	}

	ctx := context.Background()
	if err := db.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := db.Health(ctx); err != nil {
		t.Errorf("Health() error = %v", err)
	}
	if err := db.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := db.Health(ctx); err == nil {
		t.Error("Stop 之后 Health 仍然成功，连接未真正关闭")
	}
}

func TestStartFailsWhenPingFails(t *testing.T) {
	name, drv := registerFakeDriver(t)
	boom := errors.New("连不上")
	drv.setPingErr(boom)

	db, err := New(Config{Driver: name, DSN: "fake", PingTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Stop(context.Background()) })

	if err := db.Start(context.Background()); err == nil {
		t.Fatal("Start() error = nil, want Ping 失败错误")
	}
}

func TestNewFailsOnUnknownDriver(t *testing.T) {
	if _, err := New(Config{Driver: "no-such-driver", DSN: "x"}); err == nil {
		t.Fatal("New() error = nil, want 未知驱动错误")
	}
}

func TestNewAppliesPoolSettings(t *testing.T) {
	name, _ := registerFakeDriver(t)

	db, err := New(Config{Driver: name, DSN: "fake", MaxOpenConns: 7})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Stop(context.Background()) })

	if got := db.Stats().MaxOpenConnections; got != 7 {
		t.Errorf("MaxOpenConnections = %d, want 7", got)
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

`make test`。预期编译失败，`undefined: New`。

- [ ] **Step 4: 写 component/sqldb/db.go**

```go
// Package sqldb 提供基于标准库 database/sql 的关系库组件。
//
// 本包不 import 任何数据库驱动：使用方按需 blank import，
// 例如 _ "github.com/go-sql-driver/mysql"，再把驱动名写进 Config.Driver。
package sqldb

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Config 是关系库组件的配置。
type Config struct {
	// Driver 是已注册的 database/sql 驱动名，例如 mysql、pgx、sqlite。
	Driver string `yaml:"driver"`
	// DSN 是驱动自己的连接串。
	DSN string `yaml:"dsn"`
	// MaxOpenConns 是连接池上限。
	MaxOpenConns int `yaml:"max_open_conns"`
	// MaxIdleConns 是空闲连接上限。
	MaxIdleConns int `yaml:"max_idle_conns"`
	// ConnMaxLifetime 是单条连接的最长存活时间。
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
	// ConnMaxIdleTime 是单条连接的最长空闲时间。
	ConnMaxIdleTime time.Duration `yaml:"conn_max_idle_time"`
	// PingTimeout 是 Start 阶段探活的超时。
	PingTimeout time.Duration `yaml:"ping_timeout"`
}

// DefaultConfig 返回一组保守的默认值。
func DefaultConfig() Config {
	return Config{
		MaxOpenConns:    50,
		MaxIdleConns:    10,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: 10 * time.Minute,
		PingTimeout:     5 * time.Second,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.MaxOpenConns == 0 {
		c.MaxOpenConns = d.MaxOpenConns
	}
	if c.MaxIdleConns == 0 {
		c.MaxIdleConns = d.MaxIdleConns
	}
	if c.ConnMaxLifetime == 0 {
		c.ConnMaxLifetime = d.ConnMaxLifetime
	}
	if c.ConnMaxIdleTime == 0 {
		c.ConnMaxIdleTime = d.ConnMaxIdleTime
	}
	if c.PingTimeout == 0 {
		c.PingTimeout = d.PingTimeout
	}
	return c
}

// DB 是实现了 app.Component 方法集的关系库句柄。
// 内嵌 *sql.DB，仓储实现可以直接使用标准库的全部方法。
type DB struct {
	*sql.DB
	cfg Config
}

// New 打开连接池。此时并不真正建连，探活发生在 Start。
func New(cfg Config) (*DB, error) {
	cfg = cfg.withDefaults()

	db, err := sql.Open(cfg.Driver, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("sqldb: 打开驱动 %s 失败: %w", cfg.Driver, err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	return &DB{DB: db, cfg: cfg}, nil
}

// Name 实现 app.Component。
func (d *DB) Name() string { return "sqldb" }

// Start 实现 app.Component，通过一次 Ping 确认连接可用。
func (d *DB) Start(ctx context.Context) error {
	if err := d.Health(ctx); err != nil {
		return fmt.Errorf("sqldb: 启动探活失败: %w", err)
	}
	return nil
}

// Stop 实现 app.Component，关闭连接池。
func (d *DB) Stop(context.Context) error {
	if err := d.DB.Close(); err != nil {
		return fmt.Errorf("sqldb: 关闭连接池失败: %w", err)
	}
	return nil
}

// Health 实现 app.HealthChecker。
func (d *DB) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, d.cfg.PingTimeout)
	defer cancel()
	return d.DB.PingContext(ctx)
}
```

- [ ] **Step 5: 写 component/sqldb/wire.go**

```go
package sqldb

import "github.com/google/wire"

// ProviderSet 供 wire 装配关系库组件。使用方需自行提供 Config。
var ProviderSet = wire.NewSet(New)
```

- [ ] **Step 6: 跑测试确认通过**

`make test`。预期 `ok github.com/Kline-x/gokit/component/sqldb`，四个测试全过。

- [ ] **Step 7: 提交**

```bash
git add component
git commit -m "组件：关系库连接池与探活"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 12: component/sqldb — 事务助手

**Files:**
- Create: `component/sqldb/tx.go`, `component/sqldb/tx_test.go`

**Interfaces:**
- Consumes: Task 11 的 `DB`、`registerFakeDriver`
- Produces:
  - `type Executor interface { ExecContext; QueryContext; QueryRowContext }`
  - `func (d *DB) Tx(ctx context.Context, fn func(ctx context.Context) error) error`
  - `func (d *DB) Executor(ctx context.Context) Executor`
  - `func TxFromContext(ctx context.Context) (*sql.Tx, bool)`

这是分层设计的关键一环：事务边界由 application 层用 `Tx` 划定，
infrastructure 层的仓储一律通过 `Executor(ctx)` 取执行器，
因而同一份仓储代码在事务内外都能用，且嵌套调用不会重复开启事务。

- [ ] **Step 1: 写失败的测试**

`component/sqldb/tx_test.go`:

```go
package sqldb

import (
	"context"
	"errors"
	"testing"
)

func newFakeDB(t *testing.T) (*DB, *fakeDriver) {
	t.Helper()
	name, drv := registerFakeDriver(t)
	db, err := New(Config{Driver: name, DSN: "fake"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Stop(context.Background()) })
	return db, drv
}

func TestTxCommitsWhenFuncSucceeds(t *testing.T) {
	db, drv := newFakeDB(t)

	err := db.Tx(context.Background(), func(ctx context.Context) error {
		if _, ok := TxFromContext(ctx); !ok {
			t.Error("事务未放进 ctx")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Tx() error = %v", err)
	}

	if got := drv.commits.Load(); got != 1 {
		t.Errorf("提交次数 = %d, want 1", got)
	}
	if got := drv.rollbacks.Load(); got != 0 {
		t.Errorf("回滚次数 = %d, want 0", got)
	}
}

func TestTxRollsBackAndReturnsOriginalError(t *testing.T) {
	db, drv := newFakeDB(t)
	boom := errors.New("业务失败")

	err := db.Tx(context.Background(), func(context.Context) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("Tx() error = %v, want %v", err, boom)
	}
	if got := drv.rollbacks.Load(); got != 1 {
		t.Errorf("回滚次数 = %d, want 1", got)
	}
	if got := drv.commits.Load(); got != 0 {
		t.Errorf("提交次数 = %d, want 0", got)
	}
}

func TestTxRollsBackOnPanicAndRepanics(t *testing.T) {
	db, drv := newFakeDB(t)

	defer func() {
		if rec := recover(); rec == nil {
			t.Error("panic 未向上传播")
		}
		if got := drv.rollbacks.Load(); got != 1 {
			t.Errorf("回滚次数 = %d, want 1", got)
		}
	}()

	_ = db.Tx(context.Background(), func(context.Context) error { panic("炸了") })
}

func TestNestedTxReusesOuterTransaction(t *testing.T) {
	db, drv := newFakeDB(t)

	err := db.Tx(context.Background(), func(outer context.Context) error {
		outerTx, _ := TxFromContext(outer)
		return db.Tx(outer, func(inner context.Context) error {
			innerTx, _ := TxFromContext(inner)
			if innerTx != outerTx {
				t.Error("内层 Tx 另起了事务，应复用外层事务")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("Tx() error = %v", err)
	}
	if got := drv.commits.Load(); got != 1 {
		t.Errorf("提交次数 = %d, want 1（嵌套调用只应提交一次）", got)
	}
}

func TestExecutorSwitchesBetweenTxAndPool(t *testing.T) {
	db, _ := newFakeDB(t)

	ctx := context.Background()
	if got := db.Executor(ctx); got != db.DB {
		t.Error("事务外 Executor 应返回连接池本身")
	}

	err := db.Tx(ctx, func(inner context.Context) error {
		tx, _ := TxFromContext(inner)
		if got := db.Executor(inner); got != tx {
			t.Error("事务内 Executor 应返回当前事务")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Tx() error = %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

`make test`。预期编译失败，`db.Tx undefined`、`undefined: TxFromContext`。

- [ ] **Step 3: 写 component/sqldb/tx.go**

```go
package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Executor 是 *sql.DB 与 *sql.Tx 的公共子集。
// 仓储实现依赖它而不是具体类型，从而在事务内外都能工作。
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type txKey struct{}

// TxFromContext 取出 ctx 中正在进行的事务。
func TxFromContext(ctx context.Context) (*sql.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(*sql.Tx)
	return tx, ok && tx != nil
}

// Executor 返回当前上下文应当使用的执行器：
// 处于事务中时返回该事务，否则返回连接池。
func (d *DB) Executor(ctx context.Context) Executor {
	if tx, ok := TxFromContext(ctx); ok {
		return tx
	}
	return d.DB
}

// Tx 在事务中执行 fn：fn 返回 nil 则提交，返回错误则回滚并原样返回该错误。
//
// 若 ctx 中已有事务，则直接复用，不再嵌套开启，
// 因此 application 层可以放心地在一个事务里组合多个用例。
// fn 内发生 panic 时先回滚再把 panic 继续向上抛。
func (d *DB) Tx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := TxFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqldb: 开启事务失败: %w", err)
	}

	// finished 表示事务已由正常路径终结（已提交或已回滚），
	// 此时 defer 无需再做任何事；只有 panic 路径会让它保持 false。
	finished := false
	defer func() {
		if finished {
			return
		}
		if rec := recover(); rec != nil {
			_ = tx.Rollback()
			panic(rec)
		}
	}()

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		finished = true
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return errors.Join(err, fmt.Errorf("sqldb: 回滚失败: %w", rbErr))
		}
		return err
	}

	finished = true
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqldb: 提交失败: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

`make test`。预期 `ok github.com/Kline-x/gokit/component/sqldb`，全部测试通过。

- [ ] **Step 5: 提交**

```bash
git add component
git commit -m "组件：关系库事务助手与上下文感知执行器"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 13: 最小可用单体示例

把内核、配置与三个组件串成一个能跑的分层应用，验证整套装配方式成立。
示例有独立的 `go.mod`，因此 SQLite 驱动不会污染框架库的依赖。

**Files:**
- Create: `example/minimal/go.mod`, `example/minimal/config.yaml`, `example/minimal/main.go`, `example/minimal/wire.go`, `example/minimal/main_test.go`
- Create: `example/minimal/internal/greeter/domain/greeting.go`
- Create: `example/minimal/internal/greeter/application/service.go`
- Create: `example/minimal/internal/greeter/infrastructure/repo.go`
- Create: `example/minimal/internal/greeter/interfaces/http.go`
- Create: `example/minimal/internal/greeter/module.go`
- Generate: `example/minimal/wire_gen.go`
- Modify: `.github/workflows/ci.yml`（把示例纳入 CI）

**Interfaces:**
- Consumes: `app.New/Register/Run/Fatal`、`config.New/WithOptionalFile/WithEnvPrefix/Load`、`log.New`、`httpserver.New/Chain/Recover/RequestLog`、`sqldb.New/Tx/Executor`
- Produces: `func initApp(cfg Config) (*Bundle, error)`（由 wire 生成实现）

- [ ] **Step 1: 建示例模块并接入框架**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && mkdir -p example/minimal/internal/greeter/{domain,application,infrastructure,interfaces} && cd example/minimal && ~/sdk/go/bin/go mod init github.com/Kline-x/gokit/example/minimal && ~/sdk/go/bin/go mod edit -replace github.com/Kline-x/gokit=../.. && ~/sdk/go/bin/go get github.com/Kline-x/gokit && ~/sdk/go/bin/go get modernc.org/sqlite'
```

- [ ] **Step 2: 写领域层**

`example/minimal/internal/greeter/domain/greeting.go`：

```go
// Package domain 是问候模块的领域层。
//
// 这一层只描述业务概念，除标准库外不 import 任何包，
// 也不感知数据库、HTTP 等任何技术细节。
package domain

import (
	"context"
	"errors"
)

// ErrNotFound 表示指定名字还没有对应的问候语。
var ErrNotFound = errors.New("问候语不存在")

// Greeting 是一条问候语。
type Greeting struct {
	Name string
	Text string
}

// NewGreeting 按业务规则生成一条问候语。
func NewGreeting(name string) Greeting {
	return Greeting{Name: name, Text: "你好，" + name}
}

// Repository 是问候语的仓储契约，实现放在 infrastructure 层。
type Repository interface {
	Save(ctx context.Context, g Greeting) error
	FindByName(ctx context.Context, name string) (Greeting, error)
}
```

- [ ] **Step 3: 写应用层**

`example/minimal/internal/greeter/application/service.go`：

```go
// Package application 是问候模块的应用层：编排用例、划定事务边界，
// 并以接口形式对外暴露能力。
//
// 跨模块调用只允许依赖本包的 Service 接口。单体部署时注入 LocalService，
// 本模块独立成服务后换成 gRPC 客户端实现，调用方代码一行都不用改。
package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/Kline-x/gokit/example/minimal/internal/greeter/domain"
)

// GreetRequest 是 Greet 用例的入参。
type GreetRequest struct {
	Name string
}

// GreetReply 是 Greet 用例的出参。
type GreetReply struct {
	Text string
}

// Service 是问候模块对外的唯一契约。
type Service interface {
	Greet(ctx context.Context, req GreetRequest) (GreetReply, error)
}

// Transactor 是应用层对事务能力的抽象。
// 应用层借此划定事务边界，而不必知道底层是哪种数据库。
type Transactor interface {
	Tx(ctx context.Context, fn func(ctx context.Context) error) error
}

// LocalService 是 Service 的进程内实现。
type LocalService struct {
	repo domain.Repository
	tx   Transactor
}

// NewLocalService 构造进程内实现。
func NewLocalService(repo domain.Repository, tx Transactor) *LocalService {
	return &LocalService{repo: repo, tx: tx}
}

// Greet 返回某个名字的问候语，不存在时先生成再落库。
// 查询与写入包在同一个事务里，事务边界由应用层决定。
func (s *LocalService) Greet(ctx context.Context, req GreetRequest) (GreetReply, error) {
	if req.Name == "" {
		return GreetReply{}, errors.New("name 不能为空")
	}

	var reply GreetReply
	err := s.tx.Tx(ctx, func(ctx context.Context) error {
		g, err := s.repo.FindByName(ctx, req.Name)
		switch {
		case err == nil:
			reply = GreetReply{Text: g.Text}
			return nil
		case errors.Is(err, domain.ErrNotFound):
			g = domain.NewGreeting(req.Name)
			if err := s.repo.Save(ctx, g); err != nil {
				return fmt.Errorf("保存问候语失败: %w", err)
			}
			reply = GreetReply{Text: g.Text}
			return nil
		default:
			return fmt.Errorf("查询问候语失败: %w", err)
		}
	})
	return reply, err
}
```

- [ ] **Step 4: 写基础设施层**

`example/minimal/internal/greeter/infrastructure/repo.go`：

```go
// Package infrastructure 是问候模块的基础设施层：把领域层的仓储契约
// 落到具体存储上。它可以依赖 gokit 组件，但不被领域层与应用层依赖。
package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Kline-x/gokit/component/sqldb"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/domain"
)

// GreetingRepo 用关系库实现 domain.Repository。
type GreetingRepo struct {
	db *sqldb.DB
}

// NewGreetingRepo 构造仓储。
func NewGreetingRepo(db *sqldb.DB) *GreetingRepo {
	return &GreetingRepo{db: db}
}

// Save 写入一条问候语。
//
// 这里取执行器用的是 db.Executor(ctx)：
// 处于事务中时自动落到当前事务，否则走连接池，同一份代码两种场景都适用。
func (r *GreetingRepo) Save(ctx context.Context, g domain.Greeting) error {
	_, err := r.db.Executor(ctx).ExecContext(ctx,
		`INSERT INTO greetings(name, text) VALUES(?, ?)`, g.Name, g.Text)
	if err != nil {
		return fmt.Errorf("写入 greetings 失败: %w", err)
	}
	return nil
}

// FindByName 按名字查询问候语，查不到时返回 domain.ErrNotFound。
func (r *GreetingRepo) FindByName(ctx context.Context, name string) (domain.Greeting, error) {
	row := r.db.Executor(ctx).QueryRowContext(ctx,
		`SELECT name, text FROM greetings WHERE name = ?`, name)

	var g domain.Greeting
	if err := row.Scan(&g.Name, &g.Text); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Greeting{}, domain.ErrNotFound
		}
		return domain.Greeting{}, fmt.Errorf("查询 greetings 失败: %w", err)
	}
	return g, nil
}

// Migrate 建表。示例用，真实项目应走迁移工具。
func Migrate(ctx context.Context, db *sqldb.DB) error {
	_, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS greetings (name TEXT PRIMARY KEY, text TEXT NOT NULL)`)
	if err != nil {
		return fmt.Errorf("建表失败: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: 写接口层**

`example/minimal/internal/greeter/interfaces/http.go`：

```go
// Package interfaces 是问候模块的接口层：只做协议转换，
// 把 HTTP 请求翻译成应用层用例调用，再把结果翻译回响应。
package interfaces

import (
	"encoding/json"
	"net/http"

	"github.com/Kline-x/gokit/example/minimal/internal/greeter/application"
)

// HTTPHandler 把问候用例暴露成 HTTP 接口。
type HTTPHandler struct {
	svc application.Service
}

// NewHTTPHandler 构造接口层处理器。注意它依赖的是 Service 接口，
// 因此本模块改成远程调用时，这一层完全不用动。
func NewHTTPHandler(svc application.Service) *HTTPHandler {
	return &HTTPHandler{svc: svc}
}

// Register 把本模块的路由挂到给定的 mux 上。
func (h *HTTPHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /greet/{name}", h.greet)
}

func (h *HTTPHandler) greet(w http.ResponseWriter, r *http.Request) {
	reply, err := h.svc.Greet(r.Context(), application.GreetRequest{
		Name: r.PathValue("name"),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(map[string]string{"text": reply.Text}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

- [ ] **Step 6: 写模块装配入口**

`example/minimal/internal/greeter/module.go`：

```go
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
	infrastructure.NewGreetingRepo,
	wire.Bind(new(domain.Repository), new(*infrastructure.GreetingRepo)),

	wire.Bind(new(application.Transactor), new(*sqldb.DB)),

	application.NewLocalService,
	wire.Bind(new(application.Service), new(*application.LocalService)),

	interfaces.NewHTTPHandler,
)
```

- [ ] **Step 7: 写 main.go**

`example/minimal/main.go`：

```go
// Command minimal 是 gokit 的最小可用单体示例：
// 一个分层的问候模块，通过 HTTP 暴露，数据落在 SQLite。
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	_ "modernc.org/sqlite" // 注册 sqlite 驱动，框架本身不绑定任何驱动

	"github.com/Kline-x/gokit/app"
	"github.com/Kline-x/gokit/component/httpserver"
	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/component/sqldb"
	"github.com/Kline-x/gokit/config"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/infrastructure"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/interfaces"
)

// Config 是本应用的聚合配置，按组件名挂子节点。
type Config struct {
	Log  log.Config        `yaml:"log"`
	HTTP httpserver.Config `yaml:"http"`
	DB   sqldb.Config      `yaml:"db"`
}

func defaultConfig() Config {
	dbCfg := sqldb.DefaultConfig()
	dbCfg.Driver = "sqlite"
	dbCfg.DSN = "file:minimal.db"

	return Config{
		Log:  log.DefaultConfig(),
		HTTP: httpserver.DefaultConfig(),
		DB:   dbCfg,
	}
}

// Bundle 汇总一次装配产出的全部对象，由 wire 填充。
type Bundle struct {
	App    *app.App
	Logger *log.Logger
	DB     *sqldb.DB
	HTTP   *httpserver.Server
}

// Register 把各组件按启动顺序注册进 App。
func (b *Bundle) Register() *app.App {
	b.App.Register(b.Logger, b.DB, b.HTTP)
	return b.App
}

func provideLogConfig(cfg Config) log.Config           { return cfg.Log }
func provideHTTPConfig(cfg Config) httpserver.Config   { return cfg.HTTP }
func provideDBConfig(cfg Config) sqldb.Config          { return cfg.DB }

func provideApp(logger *log.Logger) *app.App {
	return app.New(app.WithName("minimal"), app.WithLogger(logger.Logger))
}

// provideHandler 组装路由与中间件。各业务模块在这里挂自己的路由。
func provideHandler(logger *log.Logger, greeter *interfaces.HTTPHandler) http.Handler {
	mux := http.NewServeMux()
	greeter.Register(mux)
	return httpserver.Chain(mux,
		httpserver.Recover(logger.Logger),
		httpserver.RequestLog(logger.Logger),
	)
}

// provideHTTPServer 把 App.Fatal 接给服务，让运行期异常触发整体优雅退出。
func provideHTTPServer(cfg httpserver.Config, h http.Handler, a *app.App) *httpserver.Server {
	return httpserver.New(cfg, h, httpserver.WithFatal(a.Fatal))
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.yaml", "配置文件路径")
	flag.Parse()

	cfg := defaultConfig()
	loader := config.New(
		config.WithOptionalFile(configPath),
		config.WithEnvPrefix("MINIMAL"),
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

	ctx := context.Background()
	if err := infrastructure.Migrate(ctx, b.DB); err != nil {
		fmt.Fprintln(os.Stderr, "初始化数据库失败:", err)
		os.Exit(1)
	}

	if err := b.Register().Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "运行失败:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 8: 写 wire.go 并生成装配代码**

`example/minimal/wire.go`：

```go
//go:build wireinject

package main

import (
	"github.com/google/wire"

	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/component/sqldb"
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
		sqldb.New,
		provideApp,
		provideHandler,
		provideHTTPServer,
		greeter.LocalSet,
		wire.Struct(new(Bundle), "*"),
	))
}
```

生成：

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && PATH="$HOME/sdk/go/bin:$HOME/go/bin:$PATH" && wire ./... && head -20 wire_gen.go'
```

预期输出 `wire: github.com/Kline-x/gokit/example/minimal: wrote .../wire_gen.go`。

若 wire 报某个类型缺少 provider，对照报错补上对应的 `provideXxx` 函数，不要改动组件包。

- [ ] **Step 9: 写配置文件**

`example/minimal/config.yaml`：

```yaml
log:
  level: info
  format: json
  output: stdout

http:
  addr: ":8080"
  read_timeout: 15s
  write_timeout: 15s

db:
  driver: sqlite
  dsn: "file:minimal.db"
  max_open_conns: 10
```

- [ ] **Step 10: 写端到端测试**

`example/minimal/main_test.go`：

```go
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Kline-x/gokit/example/minimal/internal/greeter/infrastructure"
)

// 端到端验证：配置 → wire 装配 → App 启停 → HTTP 请求 → 分层调用 → SQLite 落库。
func TestGreetEndToEnd(t *testing.T) {
	cfg := defaultConfig()
	cfg.HTTP.Addr = "127.0.0.1:0"
	cfg.DB.DSN = "file:e2e?mode=memory&cache=shared"
	cfg.Log.Output = filepath.Join(t.TempDir(), "app.log")

	b, err := initApp(cfg)
	if err != nil {
		t.Fatalf("initApp() error = %v", err)
	}

	ctx := context.Background()
	if err := infrastructure.Migrate(ctx, b.DB); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	a := b.Register()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(ctx) })

	url := "http://" + b.HTTP.Addr().String() + "/greet/gokit"

	// 第一次请求会生成并落库，第二次应命中已有记录，两次结果必须一致。
	for i := 0; i < 2; i++ {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("第 %d 次请求失败: %v", i+1, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("读取响应失败: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("第 %d 次 status = %d, want 200, body=%s", i+1, resp.StatusCode, body)
		}

		var reply map[string]string
		if err := json.Unmarshal(body, &reply); err != nil {
			t.Fatalf("响应不是合法 JSON: %v, 内容=%s", err, body)
		}
		if reply["text"] != "你好，gokit" {
			t.Errorf("text = %q, want %q", reply["text"], "你好，gokit")
		}
	}

	var count int
	row := b.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM greetings WHERE name = ?`, "gokit")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 1 {
		t.Errorf("greetings 行数 = %d, want 1（第二次请求不应重复写入）", count)
	}
}
```

- [ ] **Step 11: 跑示例测试**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && ~/sdk/go/bin/go mod tidy'
```

然后在仓库根目录 `make example-test`。预期 `ok github.com/Kline-x/gokit/example/minimal`，端到端测试通过。

- [ ] **Step 12: 手工跑一遍真实服务**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && ~/sdk/go/bin/go run . &
sleep 3 && curl -s http://127.0.0.1:8080/greet/gokit && echo && kill %1'
```

预期看到一行 JSON `{"text":"你好，gokit"}`，以及 stdout 上的 JSON 启动日志与请求日志；`kill` 后应能看到逐个组件停止的日志。

- [ ] **Step 13: 把示例接进 CI**

在 `.github/workflows/ci.yml` 的 `test` job 末尾追加两步：

```yaml
      - run: go build ./...
        working-directory: example/minimal
      - run: go test -race ./...
        working-directory: example/minimal
```

- [ ] **Step 14: 提交**

```bash
git add example .github
git commit -m "示例：分层最小单体，串起内核、配置与三个组件"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

- [ ] **Step 15: 推送**

```bash
git push
```

---

## 完成标准

全部 13 个 Task 做完后，下列命令必须全部通过：

```bash
make build && make lint && make test && make example-test
```

以及手工验证：`example/minimal` 能启动、能响应请求、Ctrl+C 能看到组件逆序停止的日志。

此时设计文档第 12 节的第 1 至 3 步完成。后续步骤（gRPC 组件、错误码与统一响应、脚手架 CLI、拆分演示、可选组件）各自另立计划。
