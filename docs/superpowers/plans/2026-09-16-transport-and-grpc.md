# 统一错误语义与 gRPC 双协议暴露 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让同一个 application 层 Service 同时以 HTTP 和 gRPC 暴露，且两边的错误语义完全一致。

**Architecture:** 新增 `transport` 包定义与协议无关的错误类型和 HTTP 统一响应；新增 `component/grpcserver` 与 `component/grpcclient` 两个生命周期组件，各自负责把 `transport` 的错误与 gRPC status 互相翻译。示例模块的 `greeter` 增加 proto 定义与 gRPC 接口层，两条协议委托同一个 `application.Service`。

**Tech Stack:** `google.golang.org/grpc`、`google.golang.org/protobuf`、protoc 与 protoc-gen-go / protoc-gen-go-grpc、标准库 `net/http`。

对应设计文档：`docs/superpowers/specs/2026-09-15-gokit-framework-design.md` 第 12 节的第 4 步与第 5 步。前一份计划 `docs/superpowers/plans/2026-09-15-gokit-kernel-and-core-components.md` 已完成并合并。

---

## Global Constraints

这一节的约束适用于**每一个** Task，不再逐条重复。

1. **Go 版本下限 1.22**（库模块 `go.mod` 的 `go` 指令就是 1.22，不要改动）。工具链在 WSL 的 `/home/gaore/sdk/go/bin/go`；示例模块的下限是 1.25.0，由 `modernc.org/sqlite` 决定。
2. **模块路径** `github.com/Kline-x/gokit`。仓库本地路径 `E:\code\AI\vibCoding\gokit`，WSL 内为 `/mnt/e/code/AI/vibCoding/gokit`。
3. **所有 go / make / protoc / wire 命令在 WSL 中执行**，模板：

   ```bash
   wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./... -race'
   ```

   单引号包住整条命令。命令跑完后打印的 `chdir(...) failed` 是中继工具的产物，不是失败，看退出码。**不要在 WSL 命令里用裸的 `$HOME` 之类 shell 变量，会被静默吞掉**，一律写字面路径。
4. **依赖边界**：
   - `app` 与 `transport` 只允许标准库。
   - `config` 只允许 `gopkg.in/yaml.v3`。
   - 各组件包只允许其直接对应的客户端库，加 `github.com/google/wire`。`grpcserver` 与 `grpcclient` 可以用 `google.golang.org/grpc` 与 `google.golang.org/protobuf`。
   - 任何组件都不得 import `app`，按方法集结构性地满足生命周期契约。
5. **组件之间互不依赖**，例外有两条：`component/log` 是横切关注点，谁都可以 import；本计划新增第二条例外 —— `component/grpcserver` 与 `component/grpcclient` 可以 import `transport`，因为错误语义本身就是通信契约的一部分。
6. **文档、目录名、注释、提交信息中不出现 "DDD" 字样**，统一说「分层」「领域模型」。
7. **注释与提交信息用中文**。提交用 `git -c user.name=xuyang -c user.email=xuyang@89you.com commit`，信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。**这行是字面文本，执行者不要换成自己的模型名。**
8. **配置结构体只用值类型字段，不用指针字段**，且第一个字段是 `Name string \`yaml:"name"\``，默认值取组件名。`config.Validate` 会在 `Load` 时拒绝指针字段与展开后无可寻址叶子的结构体字段。
9. **组件的生命周期方法集**是 `Name() string`、`Start(context.Context) error`、`Stop(context.Context) error`；可选实现 `Health(context.Context) error`（`App.Health` 会用类型断言探测）与 `DependsOn() []string`。`Start` 必须快速返回，阻塞型服务自己起 goroutine，运行期异常通过组件的 fatal 回调上报。
10. **每个 Task 结束时提交一次**，提交前两个模块的 `go vet` 与 `go test ./... -race` 都必须通过。

### 相对设计文档的一处修正

设计文档第 8 节说 `transport` 提供「与 gRPC status 与 HTTP JSON 的双向转换」。但全局约束第 4 条要求 `transport` 只用标准库，两者矛盾。

本计划的处理：`transport` 只管与协议无关的错误类型和 HTTP 侧（HTTP 属于标准库），**gRPC 侧的转换放在 `component/grpcserver` 与 `component/grpcclient` 里**。这样约束不破，错误语义也仍然只有一个来源。

### 相对设计文档的另一处细化

设计文档第 6 节说 `grpcclient` 是「按目标名建连接池」。本计划改成**一个 `Client` 组件对应一条到一个下游的连接**，要连多个下游就注册多个组件，靠 `Config.Name` 区分。

理由是这样与前一版已经定下的组件模型一致：名字在配置里、由 `App` 统一托管生命周期、每条连接各自探活。把多条连接塞进一个组件反而让 `Health` 的语义含糊——一条断了算不算不健康。

---

## 现有 API（写代码时按这个来，不要凭记忆）

前一份计划已交付并合并，当前可用的导出面：

```go
// app
type Component interface { Name() string; Start(context.Context) error; Stop(context.Context) error }
type HealthChecker interface { Health(context.Context) error }
type Dependent interface { DependsOn() []string }
func New(opts ...Option) *App
func (a *App) Register(cs ...Component)          // Start 之后调用会 panic
func (a *App) Start(ctx context.Context) error   // 重复调用返回错误
func (a *App) Stop(ctx context.Context) error
func (a *App) Run(ctx context.Context) error
func (a *App) Health(ctx context.Context) error
func (a *App) Fatal(err error)
func (a *App) Done() <-chan error
func WithName(string) Option; func WithVersion(string) Option
func WithStopTimeout(time.Duration) Option; func WithLogger(*slog.Logger) Option
func WithSignals(...os.Signal) Option

// config
func New(opts ...Option) *Loader
func (l *Loader) Load(dst any) error
func WithFile(...string) Option; func WithOptionalFile(...string) Option
func WithEnvPrefix(string) Option; func WithOverride(map[string]string) Option
func Paths(dst any) []string; func EnvName(prefix, path string) string
func SetPath(dst any, path, value string) error; func Validate(dst any) error

// component/log
type Config struct { Name, Level, Format, Output string }
func DefaultConfig() Config
func New(cfg Config) (*Logger, error)   // *Logger 内嵌 *slog.Logger
func (l *Logger) SetLevel(level string) error
func NewContext(ctx context.Context, l *slog.Logger) context.Context
func FromContext(ctx context.Context) *slog.Logger

// component/httpserver
type Config struct { Name, Addr string; ReadTimeout, WriteTimeout, IdleTimeout, ShutdownTimeout time.Duration }
func DefaultConfig() Config
func New(cfg Config, h http.Handler, opts ...Option) *Server
func WithFatal(fn func(error)) Option
func (s *Server) Addr() net.Addr
func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler
func Recover(logger *slog.Logger) func(http.Handler) http.Handler
func RequestLog(logger *slog.Logger) func(http.Handler) http.Handler
func Timeout(d time.Duration) func(http.Handler) http.Handler
// 顺序约束：RequestLog 必须在最外层

// component/sqldb
type Config struct { Name, Driver, DSN string; MaxOpenConns, MaxIdleConns int; ConnMaxLifetime, ConnMaxIdleTime, PingTimeout time.Duration }
// 数值字段：0 表示用默认值，-1 表示用 database/sql 的零值语义
func DefaultConfig() Config
func New(cfg Config) (*DB, error)      // *DB 内嵌 *sql.DB
func (d *DB) Tx(ctx context.Context, fn func(ctx context.Context) error) error
func (d *DB) Executor(ctx context.Context) Executor
func TxFromContext(ctx context.Context) (*sql.Tx, bool)
```

---

## File Structure

| 文件 | 职责 |
|---|---|
| `transport/error.go` | `Error` 类型、`New`、`Is`/`As` 支持、`FromError` |
| `transport/code.go` | 预定义错误码与构造助手（`NotFound`、`InvalidArgument` 等） |
| `transport/http.go` | 统一响应体、`Render`、`RenderError`、错误码到 HTTP 状态码的映射 |
| `component/grpcserver/server.go` | `Config`、`Server` 组件 |
| `component/grpcserver/interceptor.go` | recover、请求日志、错误转 gRPC status 的一元拦截器 |
| `component/grpcserver/wire.go` | `ProviderSet` |
| `component/grpcclient/client.go` | `Config`、`Client` 组件（连接管理） |
| `component/grpcclient/interceptor.go` | gRPC status 转回 `transport.Error` 的一元拦截器 |
| `component/grpcclient/wire.go` | `ProviderSet` |
| `example/minimal/api/greeter/v1/greeter.proto` | 模块对外契约 |
| `example/minimal/internal/greeter/interfaces/grpc.go` | gRPC 接口层，委托同一个 `application.Service` |
| `Makefile` | 新增 `proto` 与 `tools` 目标 |

---

## Task 1: transport — 错误类型

**Files:**
- Create: `transport/error.go`, `transport/error_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `type Error struct { Code int; Reason string; Message string; Metadata map[string]string }`
  - `func New(code int, reason, message string) *Error`
  - `func (e *Error) Error() string`
  - `func (e *Error) Is(target error) bool`
  - `func (e *Error) WithMetadata(kv map[string]string) *Error`
  - `func (e *Error) WithCause(err error) *Error`
  - `func (e *Error) Unwrap() error`
  - `func FromError(err error) *Error`

- [ ] **Step 1: 写失败的测试**

`transport/error_test.go`：

```go
package transport

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorCarriesCodeReasonAndMessage(t *testing.T) {
	err := New(404, "USER_NOT_FOUND", "用户不存在")

	if err.Code != 404 {
		t.Errorf("Code = %d, want 404", err.Code)
	}
	if err.Reason != "USER_NOT_FOUND" {
		t.Errorf("Reason = %q, want %q", err.Reason, "USER_NOT_FOUND")
	}
	if got := err.Error(); got == "" {
		t.Error("Error() 返回空串")
	}
}

func TestErrorIsMatchesByCodeAndReason(t *testing.T) {
	sentinel := New(404, "USER_NOT_FOUND", "")
	actual := New(404, "USER_NOT_FOUND", "id=7 的用户不存在")

	if !errors.Is(actual, sentinel) {
		t.Error("errors.Is 应当按 Code 与 Reason 匹配，与 Message 无关")
	}

	other := New(404, "ORDER_NOT_FOUND", "")
	if errors.Is(actual, other) {
		t.Error("Reason 不同的错误不应互相匹配")
	}

	wrongCode := New(500, "USER_NOT_FOUND", "")
	if errors.Is(actual, wrongCode) {
		t.Error("Code 不同的错误不应互相匹配")
	}
}

func TestErrorIsWorksThroughWrapping(t *testing.T) {
	sentinel := New(404, "USER_NOT_FOUND", "")
	wrapped := fmt.Errorf("查询用户失败: %w", New(404, "USER_NOT_FOUND", "id=7"))

	if !errors.Is(wrapped, sentinel) {
		t.Error("被 fmt.Errorf 包装之后 errors.Is 仍应匹配")
	}
}

func TestErrorAsExtractsConcreteType(t *testing.T) {
	wrapped := fmt.Errorf("外层: %w", New(400, "BAD_INPUT", "name 不能为空"))

	var target *Error
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As 未能取出 *Error")
	}
	if target.Reason != "BAD_INPUT" {
		t.Errorf("Reason = %q, want %q", target.Reason, "BAD_INPUT")
	}
}

func TestWithMetadataDoesNotMutateOriginal(t *testing.T) {
	base := New(400, "BAD_INPUT", "参数有误")
	derived := base.WithMetadata(map[string]string{"field": "name"})

	if base.Metadata != nil {
		t.Error("WithMetadata 不应改动原错误")
	}
	if derived.Metadata["field"] != "name" {
		t.Errorf("Metadata = %v, want field=name", derived.Metadata)
	}
	if derived.Code != base.Code || derived.Reason != base.Reason {
		t.Error("WithMetadata 丢失了 Code 或 Reason")
	}
}

func TestWithCauseKeepsUnderlyingErrorReachable(t *testing.T) {
	cause := errors.New("连接被拒绝")
	err := New(500, "DB_UNAVAILABLE", "数据库不可用").WithCause(cause)

	if !errors.Is(err, cause) {
		t.Error("errors.Is 应能穿透到 WithCause 记录的原始错误")
	}
}

func TestFromErrorWrapsUnknownError(t *testing.T) {
	plain := errors.New("某个底层错误")
	got := FromError(plain)

	if got.Code != CodeInternal {
		t.Errorf("Code = %d, want %d（未知错误应归为内部错误）", got.Code, CodeInternal)
	}
	if !errors.Is(got, plain) {
		t.Error("FromError 应保留原始错误可被 errors.Is 找到")
	}
}

func TestFromErrorPassesThroughTransportError(t *testing.T) {
	original := New(404, "USER_NOT_FOUND", "用户不存在")
	if got := FromError(original); got != original {
		t.Error("FromError 对已经是 *Error 的输入应原样返回")
	}
}

func TestFromErrorReturnsNilForNil(t *testing.T) {
	if got := FromError(nil); got != nil {
		t.Errorf("FromError(nil) = %v, want nil", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./transport -v'
```

预期编译失败：`undefined: New`、`undefined: CodeInternal`。`CodeInternal` 由 Task 2 定义，本任务先让 `error.go` 编译不过是正常的 —— 为了让本任务的测试能跑，在 `error.go` 里**先不要**定义它；Step 3 会说明怎么处理。

- [ ] **Step 3: 写 transport/error.go**

本文件需要 `CodeInternal`，但错误码表属于 Task 2。为了让本任务自成闭环，在 `error.go` 里定义它，Task 2 再把它连同其他码一起移到 `code.go`：

```go
// Package transport 定义与具体协议无关的错误语义与 HTTP 侧的统一响应。
//
// 这里的 Error 是整个框架的错误载体：业务层返回它，HTTP 与 gRPC 两侧
// 各自把它翻译成本协议的表达，因此同一个错误在两条协议上语义一致。
//
// 本包只依赖标准库。gRPC 侧的转换在 component/grpcserver 与
// component/grpcclient 里，避免把 gRPC 拖进这个最底层的包。
package transport

import (
	"errors"
	"fmt"
	"maps"
)

// CodeInternal 是未知错误的兜底码。完整的错误码表见 code.go。
const CodeInternal = 500

// Error 是框架统一的错误类型。
//
// Code 决定协议层的状态（HTTP 状态码、gRPC status code），
// Reason 是稳定的机器可读标识，调用方按它做分支判断，
// Message 是给人看的描述，可以随时改而不影响调用方。
type Error struct {
	// Code 是错误的分类码，取值见 code.go 中的常量。
	Code int
	// Reason 是稳定的机器可读标识，例如 USER_NOT_FOUND。
	Reason string
	// Message 是面向人的描述，不参与相等性判断。
	Message string
	// Metadata 携带结构化的补充信息，例如出错的字段名。
	Metadata map[string]string

	// cause 是底层原因，只用于 errors.Is / errors.As 穿透，不跨进程传递。
	cause error
}

// New 构造一个错误。
func New(code int, reason, message string) *Error {
	return &Error{Code: code, Reason: reason, Message: message}
}

// Error 实现 error。
func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("transport: code=%d reason=%s message=%s cause=%v",
			e.Code, e.Reason, e.Message, e.cause)
	}
	return fmt.Sprintf("transport: code=%d reason=%s message=%s", e.Code, e.Reason, e.Message)
}

// Is 让 errors.Is 按 Code 与 Reason 匹配，与 Message、Metadata 无关。
//
// 这样业务可以定义一个不带描述的哨兵错误，用它去匹配任何同类错误。
//
// 这里对 target 做直接类型断言而不是 errors.As：拆解调用方那条错误链
// 是 errors.Is 自己的职责，如果这里再去拆 target，那么「target 只是
// 包装了一个 Error」也会被误判成匹配。
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.Code == t.Code && e.Reason == t.Reason
}

// Unwrap 让 errors.Is / errors.As 能穿透到底层原因。
func (e *Error) Unwrap() error { return e.cause }

// WithMetadata 返回一个带上补充信息的副本，不改动原错误。
func (e *Error) WithMetadata(kv map[string]string) *Error {
	out := e.clone()
	if out.Metadata == nil {
		out.Metadata = make(map[string]string, len(kv))
	}
	maps.Copy(out.Metadata, kv)
	return out
}

// WithCause 返回一个记录了底层原因的副本，不改动原错误。
func (e *Error) WithCause(err error) *Error {
	out := e.clone()
	out.cause = err
	return out
}

func (e *Error) clone() *Error {
	out := &Error{
		Code:    e.Code,
		Reason:  e.Reason,
		Message: e.Message,
		cause:   e.cause,
	}
	if e.Metadata != nil {
		out.Metadata = maps.Clone(e.Metadata)
	}
	return out
}

// FromError 把任意 error 归一成 *Error。
//
// 已经是 *Error 的原样返回；其余一律归为内部错误，并把原始错误挂在
// cause 上，好让调用方仍能用 errors.Is 找到它。nil 返回 nil。
func FromError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return New(CodeInternal, "INTERNAL", err.Error()).WithCause(err)
}
```

`maps` 是 Go 1.21 起的标准库包，符合 1.22 的版本下限。

- [ ] **Step 4: 跑测试确认通过**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./transport -v -race -count=1'
```

预期 9 个测试全过。

- [ ] **Step 5: 跑全量并提交**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

```bash
git add transport
git commit -m "通信：与协议无关的统一错误类型"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 2: transport — 错误码表与构造助手

**Files:**
- Create: `transport/code.go`, `transport/code_test.go`
- Modify: `transport/error.go`（把 `CodeInternal` 的定义搬走）

**Interfaces:**
- Consumes: Task 1 的 `Error`、`New`
- Produces:
  - 常量 `CodeOK`、`CodeInvalidArgument`、`CodeUnauthenticated`、`CodePermissionDenied`、`CodeNotFound`、`CodeAlreadyExists`、`CodeFailedPrecondition`、`CodeRateLimited`、`CodeInternal`、`CodeUnavailable`、`CodeTimeout`
  - 助手 `func InvalidArgument(reason, message string) *Error` 等同名一族
  - `func Code(err error) int`

- [ ] **Step 1: 写失败的测试**

`transport/code_test.go`：

```go
package transport

import (
	"errors"
	"testing"
)

func TestHelpersProduceExpectedCodes(t *testing.T) {
	cases := []struct {
		name string
		err  *Error
		want int
	}{
		{"InvalidArgument", InvalidArgument("BAD_INPUT", "参数有误"), CodeInvalidArgument},
		{"Unauthenticated", Unauthenticated("NO_TOKEN", "缺少凭证"), CodeUnauthenticated},
		{"PermissionDenied", PermissionDenied("FORBIDDEN", "无权访问"), CodePermissionDenied},
		{"NotFound", NotFound("USER_NOT_FOUND", "用户不存在"), CodeNotFound},
		{"AlreadyExists", AlreadyExists("DUPLICATE", "已存在"), CodeAlreadyExists},
		{"FailedPrecondition", FailedPrecondition("NOT_READY", "状态不满足"), CodeFailedPrecondition},
		{"RateLimited", RateLimited("TOO_MANY", "请求过快"), CodeRateLimited},
		{"Internal", Internal("BOOM", "内部错误"), CodeInternal},
		{"Unavailable", Unavailable("DOWN", "依赖不可用"), CodeUnavailable},
		{"Timeout", Timeout("SLOW", "超时"), CodeTimeout},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Code != tc.want {
				t.Errorf("Code = %d, want %d", tc.err.Code, tc.want)
			}
			if tc.err.Reason == "" {
				t.Error("Reason 不应为空")
			}
		})
	}
}

func TestCodeReadsThroughWrapping(t *testing.T) {
	err := NotFound("USER_NOT_FOUND", "用户不存在")
	if got := Code(err); got != CodeNotFound {
		t.Errorf("Code() = %d, want %d", got, CodeNotFound)
	}

	wrapped := errors.Join(errors.New("外层"), err)
	if got := Code(wrapped); got != CodeNotFound {
		t.Errorf("包装后 Code() = %d, want %d", got, CodeNotFound)
	}
}

func TestCodeOfNilIsOK(t *testing.T) {
	if got := Code(nil); got != CodeOK {
		t.Errorf("Code(nil) = %d, want %d", got, CodeOK)
	}
}

func TestCodeOfUnknownErrorIsInternal(t *testing.T) {
	if got := Code(errors.New("随便一个错误")); got != CodeInternal {
		t.Errorf("Code() = %d, want %d", got, CodeInternal)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./transport -run TestHelpers -v'
```

预期编译失败：`undefined: InvalidArgument` 等。

- [ ] **Step 3: 写 transport/code.go**

```go
package transport

import "errors"

// 框架的错误码表。取值大体沿用 HTTP 状态码，方便一眼看懂；
// 映射到 gRPC status 的规则在 component/grpcserver 里。
const (
	// CodeOK 表示没有错误。
	CodeOK = 0
	// CodeInvalidArgument 表示入参不合法。
	CodeInvalidArgument = 400
	// CodeUnauthenticated 表示缺少或无效的身份凭证。
	CodeUnauthenticated = 401
	// CodePermissionDenied 表示身份有效但无权执行。
	CodePermissionDenied = 403
	// CodeNotFound 表示目标资源不存在。
	CodeNotFound = 404
	// CodeAlreadyExists 表示资源已存在，通常出现在创建场景。
	CodeAlreadyExists = 409
	// CodeFailedPrecondition 表示当前状态不允许该操作。
	CodeFailedPrecondition = 422
	// CodeRateLimited 表示触发了限流。
	CodeRateLimited = 429
	// CodeInternal 是未归类错误的兜底。
	CodeInternal = 500
	// CodeUnavailable 表示依赖暂时不可用，调用方可以重试。
	CodeUnavailable = 503
	// CodeTimeout 表示处理超时。
	CodeTimeout = 504
)

// InvalidArgument 构造一个入参不合法的错误。
func InvalidArgument(reason, message string) *Error {
	return New(CodeInvalidArgument, reason, message)
}

// Unauthenticated 构造一个缺少或无效凭证的错误。
func Unauthenticated(reason, message string) *Error {
	return New(CodeUnauthenticated, reason, message)
}

// PermissionDenied 构造一个无权执行的错误。
func PermissionDenied(reason, message string) *Error {
	return New(CodePermissionDenied, reason, message)
}

// NotFound 构造一个资源不存在的错误。
func NotFound(reason, message string) *Error {
	return New(CodeNotFound, reason, message)
}

// AlreadyExists 构造一个资源已存在的错误。
func AlreadyExists(reason, message string) *Error {
	return New(CodeAlreadyExists, reason, message)
}

// FailedPrecondition 构造一个状态不满足的错误。
func FailedPrecondition(reason, message string) *Error {
	return New(CodeFailedPrecondition, reason, message)
}

// RateLimited 构造一个触发限流的错误。
func RateLimited(reason, message string) *Error {
	return New(CodeRateLimited, reason, message)
}

// Internal 构造一个内部错误。
func Internal(reason, message string) *Error {
	return New(CodeInternal, reason, message)
}

// Unavailable 构造一个依赖不可用的错误。
func Unavailable(reason, message string) *Error {
	return New(CodeUnavailable, reason, message)
}

// Timeout 构造一个处理超时的错误。
func Timeout(reason, message string) *Error {
	return New(CodeTimeout, reason, message)
}

// Code 取出任意 error 的错误码。nil 返回 CodeOK，未归类的错误返回 CodeInternal。
func Code(err error) int {
	if err == nil {
		return CodeOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}
```

- [ ] **Step 4: 把 CodeInternal 的定义从 error.go 搬走**

删除 `transport/error.go` 里这两行：

```go
// CodeInternal 是未知错误的兜底码。完整的错误码表见 code.go。
const CodeInternal = 500
```

`error.go` 仍然会用到 `CodeInternal`，但它现在由同包的 `code.go` 提供，编译没问题。

- [ ] **Step 5: 跑测试确认通过**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./transport -v -race -count=1'
```

预期 Task 1 与 Task 2 的测试全过，且没有重复定义的编译错误。

- [ ] **Step 6: 跑全量并提交**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

```bash
git add transport
git commit -m "通信：错误码表与构造助手"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 3: transport — HTTP 统一响应

**Files:**
- Create: `transport/http.go`, `transport/http_test.go`

**Interfaces:**
- Consumes: Task 1 与 Task 2 的 `Error`、`FromError`、错误码常量
- Produces:
  - `type Response struct { Code int; Reason string; Message string; Metadata map[string]string; Data any }`
  - `func Render(w http.ResponseWriter, data any) error`
  - `func RenderError(w http.ResponseWriter, err error) error`
  - `func HTTPStatus(code int) int`

- [ ] **Step 1: 写失败的测试**

`transport/http_test.go`：

```go
package transport

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderWritesSuccessEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := Render(rec, map[string]string{"text": "你好"}); err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v, 内容=%q", err, rec.Body.String())
	}
	if body["code"] != float64(CodeOK) {
		t.Errorf("code = %v, want %d", body["code"], CodeOK)
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("data 不是对象: %v", body["data"])
	}
	if data["text"] != "你好" {
		t.Errorf("data.text = %v, want 你好", data["text"])
	}
}

func TestRenderErrorMapsCodeToStatus(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   int
	}{
		{"not found", NotFound("USER_NOT_FOUND", "用户不存在"), http.StatusNotFound, CodeNotFound},
		{"invalid", InvalidArgument("BAD_INPUT", "参数有误"), http.StatusBadRequest, CodeInvalidArgument},
		{"rate limited", RateLimited("TOO_MANY", "请求过快"), http.StatusTooManyRequests, CodeRateLimited},
		{"unknown", errors.New("随便一个错误"), http.StatusInternalServerError, CodeInternal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			if err := RenderError(rec, tc.err); err != nil {
				t.Fatalf("RenderError() error = %v", err)
			}
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应不是合法 JSON: %v", err)
			}
			if body["code"] != float64(tc.wantCode) {
				t.Errorf("code = %v, want %d", body["code"], tc.wantCode)
			}
			if _, has := body["data"]; has {
				t.Errorf("出错时不应带 data 字段，实际 = %v", body)
			}
		})
	}
}

func TestRenderErrorIncludesReasonAndMetadata(t *testing.T) {
	err := InvalidArgument("BAD_INPUT", "name 不能为空").
		WithMetadata(map[string]string{"field": "name"})

	rec := httptest.NewRecorder()
	if renderErr := RenderError(rec, err); renderErr != nil {
		t.Fatalf("RenderError() error = %v", renderErr)
	}

	var body map[string]any
	if jsonErr := json.Unmarshal(rec.Body.Bytes(), &body); jsonErr != nil {
		t.Fatalf("响应不是合法 JSON: %v", jsonErr)
	}
	if body["reason"] != "BAD_INPUT" {
		t.Errorf("reason = %v, want BAD_INPUT", body["reason"])
	}
	meta, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata 不是对象: %v", body["metadata"])
	}
	if meta["field"] != "name" {
		t.Errorf("metadata.field = %v, want name", meta["field"])
	}
}

func TestRenderErrorHidesInternalCause(t *testing.T) {
	// cause 只服务于进程内的 errors.Is，不应泄漏到响应体里。
	err := Internal("BOOM", "内部错误").WithCause(errors.New("数据库密码错误"))

	rec := httptest.NewRecorder()
	if renderErr := RenderError(rec, err); renderErr != nil {
		t.Fatalf("RenderError() error = %v", renderErr)
	}
	if body := rec.Body.String(); strings.Contains(body, "数据库密码错误") {
		t.Errorf("响应体泄漏了内部原因: %s", body)
	}
}

func TestHTTPStatusFallsBackToInternal(t *testing.T) {
	if got := HTTPStatus(9999); got != http.StatusInternalServerError {
		t.Errorf("HTTPStatus(9999) = %d, want 500", got)
	}
	if got := HTTPStatus(CodeOK); got != http.StatusOK {
		t.Errorf("HTTPStatus(CodeOK) = %d, want 200", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./transport -run TestRender -v'
```

预期编译失败：`undefined: Render`。

- [ ] **Step 3: 写 transport/http.go**

```go
package transport

import (
	"encoding/json"
	"net/http"
)

// Response 是 HTTP 侧的统一响应体。
//
// 成功时 Code 为 CodeOK、Data 携带业务数据；失败时 Code 为错误码、
// Reason 与 Message 说明原因，Data 缺席。业务代码不手写 JSON，
// 一律走 Render 与 RenderError，好让所有接口的形状一致。
type Response struct {
	// Code 是业务错误码，成功为 CodeOK。
	Code int `json:"code"`
	// Reason 是稳定的机器可读标识，成功时缺席。
	Reason string `json:"reason,omitempty"`
	// Message 是面向人的描述，成功时缺席。
	Message string `json:"message,omitempty"`
	// Metadata 是结构化的补充信息，成功时缺席。
	Metadata map[string]string `json:"metadata,omitempty"`
	// Data 是业务数据，失败时缺席。
	Data any `json:"data,omitempty"`
}

// statusByCode 把框架错误码映射到 HTTP 状态码。
// 错误码本身就按 HTTP 的直觉取值，这里只做一次显式确认，
// 顺便挡住表外的取值。
var statusByCode = map[int]int{
	CodeOK:                 http.StatusOK,
	CodeInvalidArgument:    http.StatusBadRequest,
	CodeUnauthenticated:    http.StatusUnauthorized,
	CodePermissionDenied:   http.StatusForbidden,
	CodeNotFound:           http.StatusNotFound,
	CodeAlreadyExists:      http.StatusConflict,
	CodeFailedPrecondition: http.StatusUnprocessableEntity,
	CodeRateLimited:        http.StatusTooManyRequests,
	CodeInternal:           http.StatusInternalServerError,
	CodeUnavailable:        http.StatusServiceUnavailable,
	CodeTimeout:            http.StatusGatewayTimeout,
}

// HTTPStatus 返回错误码对应的 HTTP 状态码。表外的取值一律按 500 处理。
func HTTPStatus(code int) int {
	if status, ok := statusByCode[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// Render 写出一个成功响应。
func Render(w http.ResponseWriter, data any) error {
	return write(w, http.StatusOK, Response{Code: CodeOK, Data: data})
}

// RenderError 写出一个错误响应。
//
// 任何 error 都能传进来：不是 *Error 的会被归一成内部错误。
// 错误的 cause 只服务于进程内的 errors.Is，不会出现在响应体里。
func RenderError(w http.ResponseWriter, err error) error {
	e := FromError(err)
	if e == nil {
		return Render(w, nil)
	}
	return write(w, HTTPStatus(e.Code), Response{
		Code:     e.Code,
		Reason:   e.Reason,
		Message:  e.Message,
		Metadata: e.Metadata,
	})
}

func write(w http.ResponseWriter, status int, body Response) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(body)
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./transport -v -race -count=1'
```

预期 `transport` 包全部测试通过。

- [ ] **Step 5: 跑全量并提交**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

```bash
git add transport
git commit -m "通信：HTTP 统一响应与错误码到状态码的映射"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 4: proto 工具链

**Files:**
- Modify: `Makefile`（新增 `tools` 与 `proto` 目标）
- Modify: `go.mod` / `go.sum`（引入 grpc 与 protobuf）

**Interfaces:**
- Consumes: 无
- Produces: `make tools` 能装好两个 protoc 插件；`make proto` 能把示例的 proto 生成到位

- [ ] **Step 1: 引入依赖（版本必须锁定，不要用 @latest）**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go get google.golang.org/grpc@v1.65.0 && /home/gaore/sdk/go/bin/go get google.golang.org/protobuf@v1.35.2 && /home/gaore/sdk/go/bin/go mod tidy'
```

**为什么锁版本**：`grpc@latest`（v1.83 一线）自身的 `go` 指令是 1.25，一旦引入就会把本模块的 `go 1.22` 顶上去，而降低版本下限正是上一版评审专门修过的事。v1.65.0 与 v1.35.2 只要求 go1.21，且已包含本计划用到的全部 API（`grpc.NewClient` 自 v1.63 起提供）。

这只是**下限**：消费者的项目想用更新的 grpc，自己 require 即可，Go 的最小版本选择会选高的那个。锁低反而保住了兼容面。后续任何任务再引入这两个依赖时，一律带上同样的版本号。

- [ ] **Step 2: 安装 protoc 插件**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && /home/gaore/sdk/go/bin/go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest && ls -l /home/gaore/go/bin/protoc-gen-go /home/gaore/go/bin/protoc-gen-go-grpc'
```

预期两个二进制都列出来。protoc 本身已经装在 `/home/gaore/bin/protoc`。

- [ ] **Step 3: 给 Makefile 加目标**

在 `Makefile` 末尾追加（缩进必须是 Tab）：

```makefile
# PROTOC 与插件的位置。默认取 PATH 上的，可覆盖。
PROTOC ?= protoc
PROTO_DIR ?= example/minimal/api

.PHONY: tools proto

# tools 安装代码生成需要的 protoc 插件。
tools:
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# proto 根据 idl 生成 Go 代码。生成物与 .proto 同目录。
proto:
	$(PROTOC) --proto_path=$(PROTO_DIR) \
		--go_out=$(PROTO_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_DIR) --go-grpc_opt=paths=source_relative \
		$(shell find $(PROTO_DIR) -name '*.proto')
```

- [ ] **Step 4: 确认依赖没污染示例模块之外的东西**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && cat go.mod && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

预期 `go.mod` 的 require 里出现 `google.golang.org/grpc` 与 `google.golang.org/protobuf`，测试仍然全绿。此时还没有任何代码用到它们，`go mod tidy` 可能把它们标成 indirect —— 那是正常的，Task 5 用上之后会变回直接依赖。若 tidy 直接把它们删掉了，先跳过 tidy，等 Task 5 写完代码再跑。

- [ ] **Step 5: 提交**

```bash
git add Makefile go.mod go.sum
git commit -m "构建：引入 grpc 与 protobuf，补上代码生成目标"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 5: component/grpcserver — 服务生命周期

**Files:**
- Create: `component/grpcserver/server.go`, `component/grpcserver/wire.go`, `component/grpcserver/server_test.go`

**Interfaces:**
- Consumes: 无（本包不 import `app`，按方法集实现）
- Produces:
  - `type Config struct { Name, Addr string; ShutdownTimeout time.Duration; EnableReflection bool }`
  - `func DefaultConfig() Config`
  - `type ServiceRegistrar interface { Register(*grpc.Server) }`
  - `func New(cfg Config, services []ServiceRegistrar, opts ...Option) *Server`
  - `func WithFatal(fn func(error)) Option`
  - `func WithUnaryInterceptor(is ...grpc.UnaryServerInterceptor) Option`
  - `func (s *Server) Name() string`、`Start(context.Context) error`、`Stop(context.Context) error`、`Addr() net.Addr`
  - `var ProviderSet`

- [ ] **Step 1: 写失败的测试**

`component/grpcserver/server_test.go`：

```go
package grpcserver

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func newTestServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	return New(Config{Name: "grpcserver.test", Addr: "127.0.0.1:0"}, nil, opts...)
}

func TestServerStartsAndServesHealth(t *testing.T) {
	s := newTestServer(t)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	if s.Addr() == nil {
		t.Fatal("Addr() = nil，Start 之后应能拿到真实监听地址")
	}

	conn, err := grpc.NewClient(s.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("建连失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("健康检查失败: %v", err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("status = %v, want SERVING", resp.GetStatus())
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

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("建连对象创建失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err == nil {
		t.Error("Stop 之后请求仍然成功，服务未真正关闭")
	}
}

func TestStartFailsOnOccupiedAddress(t *testing.T) {
	first := newTestServer(t)
	if err := first.Start(context.Background()); err != nil {
		t.Fatalf("第一个 Start() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Stop(context.Background()) })

	second := New(Config{Name: "grpcserver.second", Addr: first.Addr().String()}, nil)
	if err := second.Start(context.Background()); err == nil {
		_ = second.Stop(context.Background())
		t.Fatal("Start() error = nil, want 端口占用错误")
	}
}

func TestStopBeforeStartIsSafe(t *testing.T) {
	s := newTestServer(t)
	if err := s.Stop(context.Background()); err != nil {
		t.Errorf("Stop() error = %v, want nil（从未 Start 过也应能安全 Stop）", err)
	}
}

func TestNameComesFromConfig(t *testing.T) {
	if got := newTestServer(t).Name(); got != "grpcserver.test" {
		t.Errorf("Name() = %q, want %q", got, "grpcserver.test")
	}
	if got := New(Config{Addr: "127.0.0.1:0"}, nil).Name(); got != "grpcserver" {
		t.Errorf("Name() = %q, want 默认值 %q", got, "grpcserver")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./component/grpcserver -v'
```

预期编译失败：`undefined: New`。

- [ ] **Step 3: 写 component/grpcserver/server.go**

```go
// Package grpcserver 提供 gRPC 服务组件。
//
// 具体的服务由使用方以 ServiceRegistrar 的形式传入，本包不关心它们是什么。
// 组件自带健康检查服务，因此不注册任何业务服务时也能起得来。
package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Config 是 gRPC 服务组件的配置。
type Config struct {
	// Name 是组件在 App 中的唯一标识。留空时取默认值。
	// 同一个 App 里注册多个同类组件时，必须给出互不相同的名字。
	Name string `yaml:"name"`
	// Addr 是监听地址，形如 :9000。测试中可用 127.0.0.1:0 让系统分配端口。
	Addr string `yaml:"addr"`
	// ShutdownTimeout 是优雅关闭时等待在途调用的上限，超时后强制停止。
	// 0 表示使用默认值；本组件不支持 sqldb 那样的 -1 哨兵。
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	// EnableReflection 决定是否开启反射服务，便于用 grpcurl 之类的工具调试。
	EnableReflection bool `yaml:"enable_reflection"`
}

// DefaultConfig 返回一组可直接使用的默认值。
func DefaultConfig() Config {
	return Config{
		Name:             "grpcserver",
		Addr:             ":9000",
		ShutdownTimeout:  10 * time.Second,
		EnableReflection: true,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.Name == "" {
		c.Name = d.Name
	}
	if c.Addr == "" {
		c.Addr = d.Addr
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = d.ShutdownTimeout
	}
	return c
}

// ServiceRegistrar 是业务服务把自己挂到 gRPC 服务器上的方式。
//
// 生成的 gRPC 服务端代码通常提供 RegisterXxxServer(s, impl)，
// 在接口层写一个薄薄的适配器实现本接口即可。
type ServiceRegistrar interface {
	Register(s *grpc.Server)
}

// Option 用于定制 Server。
type Option func(*Server)

// WithFatal 注册运行期异常回调。Serve 意外退出时会被调用，
// 典型用法是传入 app.App 的 Fatal 方法，触发整体优雅退出。
func WithFatal(fn func(error)) Option {
	return func(s *Server) { s.onFatal = fn }
}

// WithUnaryInterceptor 追加一元拦截器，按传入顺序由外向内生效。
func WithUnaryInterceptor(is ...grpc.UnaryServerInterceptor) Option {
	return func(s *Server) { s.unary = append(s.unary, is...) }
}

// Server 是实现了 app.Component 方法集的 gRPC 服务。
type Server struct {
	cfg      Config
	services []ServiceRegistrar
	unary    []grpc.UnaryServerInterceptor
	onFatal  func(error)

	health *health.Server

	mu  sync.Mutex
	srv *grpc.Server
	ln  net.Listener
}

// New 创建 gRPC 服务组件。services 可以为 nil，此时只提供健康检查。
func New(cfg Config, services []ServiceRegistrar, opts ...Option) *Server {
	s := &Server{
		cfg:      cfg.withDefaults(),
		services: services,
		health:   health.NewServer(),
	}
	for _, fn := range opts {
		fn(s)
	}
	return s
}

// Name 实现 app.Component。
func (s *Server) Name() string { return s.cfg.Name }

// Start 实现 app.Component。
// 监听动作是同步的，因此 Start 返回后 Addr() 即可用；Serve 在后台 goroutine 中运行。
func (s *Server) Start(context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("grpcserver: %s 监听 %s 失败: %w", s.cfg.Name, s.cfg.Addr, err)
	}

	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(s.unary...))
	for _, svc := range s.services {
		svc.Register(srv)
	}
	healthpb.RegisterHealthServer(srv, s.health)
	s.health.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	if s.cfg.EnableReflection {
		reflection.Register(srv)
	}

	s.mu.Lock()
	s.srv = srv
	s.ln = ln
	s.mu.Unlock()

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			if s.onFatal != nil {
				s.onFatal(fmt.Errorf("grpcserver: %s 服务异常退出: %w", s.cfg.Name, err))
			}
		}
	}()
	return nil
}

// Stop 实现 app.Component，优雅关闭并等待在途调用；超时后强制停止。
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	srv := s.srv
	s.srv = nil
	s.ln = nil
	s.mu.Unlock()

	if srv == nil {
		return nil
	}

	s.health.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)

	if s.cfg.ShutdownTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.ShutdownTimeout)
		defer cancel()
	}

	done := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		// 还有在途调用没排空，强制停止，避免拖住整个应用的关停预算。
		srv.Stop()
		<-done
		return fmt.Errorf("grpcserver: %s 优雅关闭超时，已强制停止: %w", s.cfg.Name, ctx.Err())
	}
}

// Addr 返回真实监听地址。Start 之前或 Stop 之后返回 nil。
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}
```

- [ ] **Step 4: 写 component/grpcserver/wire.go**

```go
package grpcserver

import "github.com/google/wire"

// ProviderSet 供 wire 装配 gRPC 服务组件。
// 使用方需自行提供 Config 与 []ServiceRegistrar。
var ProviderSet = wire.NewSet(New)
```

- [ ] **Step 5: 跑测试确认通过**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go mod tidy && /home/gaore/sdk/go/bin/go test ./component/grpcserver -v -race -count=1'
```

预期 5 个测试全过。

- [ ] **Step 6: 跑全量并提交**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

```bash
git add component/grpcserver go.mod go.sum
git commit -m "组件：gRPC 服务生命周期与健康检查"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 6: component/grpcserver — 拦截器与错误映射

**Files:**
- Create: `component/grpcserver/interceptor.go`, `component/grpcserver/interceptor_test.go`

**Interfaces:**
- Consumes: Task 1 至 3 的 `transport.Error`、`transport.FromError`、错误码常量；Task 5 的 `Option`
- Produces:
  - `func Recover(logger *slog.Logger) grpc.UnaryServerInterceptor`
  - `func RequestLog(logger *slog.Logger) grpc.UnaryServerInterceptor`
  - `func ErrorMapper() grpc.UnaryServerInterceptor`
  - `func GRPCCode(code int) codes.Code`

- [ ] **Step 1: 写失败的测试**

`component/grpcserver/interceptor_test.go`：

```go
package grpcserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Kline-x/gokit/transport"
)

func unaryInfo() *grpc.UnaryServerInfo {
	return &grpc.UnaryServerInfo{FullMethod: "/greeter.v1.Greeter/Greet"}
}

func TestErrorMapperTranslatesTransportError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"not found", transport.NotFound("USER_NOT_FOUND", "用户不存在"), codes.NotFound},
		{"invalid", transport.InvalidArgument("BAD_INPUT", "参数有误"), codes.InvalidArgument},
		{"denied", transport.PermissionDenied("FORBIDDEN", "无权访问"), codes.PermissionDenied},
		{"unavailable", transport.Unavailable("DOWN", "依赖不可用"), codes.Unavailable},
		{"unknown", errors.New("随便一个错误"), codes.Internal},
	}

	mapper := ErrorMapper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mapper(context.Background(), nil, unaryInfo(),
				func(context.Context, any) (any, error) { return nil, tc.err })

			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("返回的不是 gRPC status: %v", err)
			}
			if st.Code() != tc.want {
				t.Errorf("code = %v, want %v", st.Code(), tc.want)
			}
		})
	}
}

func TestErrorMapperCarriesReasonInMessage(t *testing.T) {
	mapper := ErrorMapper()
	_, err := mapper(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) {
			return nil, transport.NotFound("USER_NOT_FOUND", "用户不存在")
		})

	st, _ := status.FromError(err)
	if !strings.Contains(st.Message(), "USER_NOT_FOUND") {
		t.Errorf("status message = %q，应当带上 Reason 以便客户端还原", st.Message())
	}
}

func TestErrorMapperLeavesSuccessAlone(t *testing.T) {
	mapper := ErrorMapper()
	got, err := mapper(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) { return "ok", nil })

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != "ok" {
		t.Errorf("resp = %v, want ok", got)
	}
}

func TestRecoverTurnsPanicIntoInternalStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	_, err := Recover(logger)(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) { panic("boom") })

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("返回的不是 gRPC status: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal", st.Code())
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("日志未记录 panic 内容，实际为 %q", buf.String())
	}
}

func TestRequestLogRecordsMethodAndCode(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	_, err := RequestLog(logger)(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) {
			return nil, status.Error(codes.NotFound, "没找到")
		})
	if err == nil {
		t.Fatal("handler 的错误应当原样返回")
	}

	var entry map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); jsonErr != nil {
		t.Fatalf("日志不是合法 JSON: %v, 内容=%q", jsonErr, buf.String())
	}
	if entry["method"] != "/greeter.v1.Greeter/Greet" {
		t.Errorf("method = %v", entry["method"])
	}
	if entry["code"] != codes.NotFound.String() {
		t.Errorf("code = %v, want %v", entry["code"], codes.NotFound.String())
	}
}

func TestRequestLogInjectsLoggerIntoContext(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	var injected bool
	_, err := RequestLog(logger)(context.Background(), nil, unaryInfo(),
		func(ctx context.Context, _ any) (any, error) {
			injected = log.FromContext(ctx) == logger
			return nil, nil
		})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !injected {
		t.Error("处理函数未能从 ctx 取到注入的 logger")
	}
}

func TestGRPCCodeFallsBackToInternal(t *testing.T) {
	if got := GRPCCode(9999); got != codes.Internal {
		t.Errorf("GRPCCode(9999) = %v, want Internal", got)
	}
	if got := GRPCCode(transport.CodeOK); got != codes.OK {
		t.Errorf("GRPCCode(CodeOK) = %v, want OK", got)
	}
}
```

测试文件需要 import `"github.com/Kline-x/gokit/component/log"`，供 `TestRequestLogInjectsLoggerIntoContext` 使用。

- [ ] **Step 2: 跑测试确认失败**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./component/grpcserver -run TestErrorMapper -v'
```

预期编译失败：`undefined: ErrorMapper`。

- [ ] **Step 3: 写 component/grpcserver/interceptor.go**

```go
package grpcserver

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/transport"
)

// codeByTransport 把框架错误码映射到 gRPC status code。
//
// 两套码没有一一对应关系，这张表是唯一的翻译依据；
// 客户端侧的反向翻译在 component/grpcclient 里，两边必须对称。
var codeByTransport = map[int]codes.Code{
	transport.CodeOK:                 codes.OK,
	transport.CodeInvalidArgument:    codes.InvalidArgument,
	transport.CodeUnauthenticated:    codes.Unauthenticated,
	transport.CodePermissionDenied:   codes.PermissionDenied,
	transport.CodeNotFound:           codes.NotFound,
	transport.CodeAlreadyExists:      codes.AlreadyExists,
	transport.CodeFailedPrecondition: codes.FailedPrecondition,
	transport.CodeRateLimited:        codes.ResourceExhausted,
	transport.CodeInternal:           codes.Internal,
	transport.CodeUnavailable:        codes.Unavailable,
	transport.CodeTimeout:            codes.DeadlineExceeded,
}

// GRPCCode 返回框架错误码对应的 gRPC status code。表外的取值一律按 Internal 处理。
func GRPCCode(code int) codes.Code {
	if c, ok := codeByTransport[code]; ok {
		return c
	}
	return codes.Internal
}

// ErrorMapper 把业务返回的 transport.Error 翻译成 gRPC status。
//
// status 的 message 里带上 Reason，形如 "USER_NOT_FOUND: 用户不存在"，
// 这样客户端侧的拦截器能把它还原回 transport.Error，跨进程后语义不丢。
// 已经是 gRPC status 的错误原样放行，不做二次包装。
func ErrorMapper() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}

		// status.FromError 只对 nil、*status.Error，以及实现了 GRPCStatus()
		// 的类型返回 true。transport.Error 不实现它，普通 error 也不实现，
		// 所以这一句足以把「已经是 status」的错误挑出来原样放行。
		if _, ok := status.FromError(err); ok {
			return resp, err
		}

		e := transport.FromError(err)
		return resp, status.Error(GRPCCode(e.Code), fmt.Sprintf("%s: %s", e.Reason, e.Message))
	}
}

// Recover 捕获处理链中的 panic，记录堆栈并返回 Internal，避免整个进程崩溃。
func Recover(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			logger.ErrorContext(ctx, "grpc 处理过程中发生 panic",
				slog.Any("panic", rec),
				slog.String("method", info.FullMethod),
				slog.String("stack", string(debug.Stack())),
			)
			err = status.Error(codes.Internal, "内部错误")
		}()
		return handler(ctx, req)
	}
}

// RequestLog 记录每次调用的方法、状态码与耗时，
// 同时把 logger 放进 ctx，供业务代码用 log.FromContext 取用。
func RequestLog(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		begin := time.Now()
		ctx = log.NewContext(ctx, logger)

		resp, err := handler(ctx, req)

		logger.InfoContext(ctx, "grpc 调用",
			slog.String("method", info.FullMethod),
			slog.String("code", status.Code(err).String()),
			slog.Duration("latency", time.Since(begin)),
		)
		return resp, err
	}
}
```

注意 `ErrorMapper` 里的判断：`status.FromError` 对任何 error 都会返回一个 status（未知的归为 Unknown），所以不能只靠它判断「已经是 status」。这里的写法是：若错误本身不是 `*transport.Error`，且能被 `status.FromError` 识别，就原样放行；否则走翻译。

- [ ] **Step 4: 跑测试确认通过**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./component/grpcserver -v -race -count=1'
```

预期本包全部测试通过。

若 `TestErrorMapperLeavesSuccessAlone` 或 `TestRequestLogRecordsMethodAndCode` 因为 `status.FromError` 的语义与上面的假设不符而失败，**停下来报告实际行为**，不要改测试去迁就实现 —— 这条判断逻辑是本任务的核心，写错了会让业务错误被吞成 Unknown。

- [ ] **Step 5: 跑全量并提交**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

```bash
git add component/grpcserver
git commit -m "组件：gRPC 拦截器与错误码映射"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 7: component/grpcclient — 连接组件与反向映射

**Files:**
- Create: `component/grpcclient/client.go`, `component/grpcclient/interceptor.go`, `component/grpcclient/wire.go`, `component/grpcclient/client_test.go`

**Interfaces:**
- Consumes: Task 1 至 3 的 `transport`；Task 5、6 的 `grpcserver`（仅测试里用来起一个真服务）
- Produces:
  - `type Config struct { Name, Target string; DialTimeout time.Duration; Block bool }`
  - `func DefaultConfig() Config`
  - `func New(cfg Config, opts ...Option) (*Client, error)`
  - `func WithUnaryInterceptor(is ...grpc.UnaryClientInterceptor) Option`
  - `func (c *Client) Name() string`、`Start(context.Context) error`、`Stop(context.Context) error`、`Health(context.Context) error`、`Conn() *grpc.ClientConn`
  - `func ErrorRestorer() grpc.UnaryClientInterceptor`
  - `func TransportCode(c codes.Code) int`
  - `var ProviderSet`

- [ ] **Step 1: 写失败的测试**

`component/grpcclient/client_test.go`：

```go
package grpcclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Kline-x/gokit/component/grpcserver"
	"github.com/Kline-x/gokit/transport"
)

// startTestServer 起一个只提供健康检查的 gRPC 服务，返回它的地址。
func startTestServer(t *testing.T) string {
	t.Helper()
	s := grpcserver.New(grpcserver.Config{Name: "grpcserver.test", Addr: "127.0.0.1:0"}, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("起测试服务失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s.Addr().String()
}

func TestClientConnectsAndReportsHealthy(t *testing.T) {
	addr := startTestServer(t)

	c, err := New(Config{Name: "grpcclient.test", Target: addr})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Stop(ctx) })

	if c.Conn() == nil {
		t.Fatal("Conn() = nil，Start 之后应当可用")
	}
	if err := c.Health(ctx); err != nil {
		t.Errorf("Health() error = %v", err)
	}
}

func TestClientHealthFailsWhenServerGone(t *testing.T) {
	addr := startTestServer(t)

	c, err := New(Config{Name: "grpcclient.test", Target: addr})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if err := c.Health(ctx); err == nil {
		t.Error("Stop 之后 Health 仍然成功，连接未真正关闭")
	}
}

func TestNameComesFromConfig(t *testing.T) {
	c, err := New(Config{Name: "grpcclient.user", Target: "127.0.0.1:1"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })

	if c.Name() != "grpcclient.user" {
		t.Errorf("Name() = %q, want %q", c.Name(), "grpcclient.user")
	}

	d, err := New(Config{Target: "127.0.0.1:1"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })
	if d.Name() != "grpcclient" {
		t.Errorf("Name() = %q, want 默认值 %q", d.Name(), "grpcclient")
	}
}

func TestNewRejectsEmptyTarget(t *testing.T) {
	if _, err := New(Config{Name: "grpcclient.test"}); err == nil {
		t.Fatal("New() error = nil, want 目标地址为空的错误")
	}
}

func TestErrorRestorerRebuildsTransportError(t *testing.T) {
	restorer := ErrorRestorer()

	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return status.Error(codes.NotFound, "USER_NOT_FOUND: 用户不存在")
	}

	err := restorer(context.Background(), "/greeter.v1.Greeter/Greet", nil, nil, nil, invoker)

	var te *transport.Error
	if !errors.As(err, &te) {
		t.Fatalf("未能还原成 *transport.Error: %v", err)
	}
	if te.Code != transport.CodeNotFound {
		t.Errorf("Code = %d, want %d", te.Code, transport.CodeNotFound)
	}
	if te.Reason != "USER_NOT_FOUND" {
		t.Errorf("Reason = %q, want %q", te.Reason, "USER_NOT_FOUND")
	}
	if te.Message != "用户不存在" {
		t.Errorf("Message = %q, want %q", te.Message, "用户不存在")
	}
}

func TestErrorRestorerHandlesMessageWithoutReason(t *testing.T) {
	restorer := ErrorRestorer()

	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return status.Error(codes.Unavailable, "连接被拒绝")
	}

	err := restorer(context.Background(), "/x/y", nil, nil, nil, invoker)

	var te *transport.Error
	if !errors.As(err, &te) {
		t.Fatalf("未能还原成 *transport.Error: %v", err)
	}
	if te.Code != transport.CodeUnavailable {
		t.Errorf("Code = %d, want %d", te.Code, transport.CodeUnavailable)
	}
	if te.Message != "连接被拒绝" {
		t.Errorf("Message = %q, want %q", te.Message, "连接被拒绝")
	}
}

func TestErrorRestorerLeavesSuccessAlone(t *testing.T) {
	restorer := ErrorRestorer()
	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return nil
	}
	if err := restorer(context.Background(), "/x/y", nil, nil, nil, invoker); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestTransportCodeIsInverseOfGRPCCode(t *testing.T) {
	// 两侧的映射必须对称，否则跨进程后错误码会漂移。
	codesToCheck := []int{
		transport.CodeInvalidArgument,
		transport.CodeUnauthenticated,
		transport.CodePermissionDenied,
		transport.CodeNotFound,
		transport.CodeAlreadyExists,
		transport.CodeFailedPrecondition,
		transport.CodeRateLimited,
		transport.CodeInternal,
		transport.CodeUnavailable,
		transport.CodeTimeout,
	}

	for _, code := range codesToCheck {
		grpcCode := grpcserver.GRPCCode(code)
		if got := TransportCode(grpcCode); got != code {
			t.Errorf("往返不一致: transport %d -> grpc %v -> transport %d", code, grpcCode, got)
		}
	}
}

func TestDialTimeoutIsRespected(t *testing.T) {
	// 连一个不存在的地址，Start 应当在超时内返回错误而不是一直卡着。
	c, err := New(Config{Name: "grpcclient.test", Target: "127.0.0.1:1", Block: true, DialTimeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })

	begin := time.Now()
	startErr := c.Start(context.Background())
	elapsed := time.Since(begin)

	if startErr == nil {
		t.Fatal("Start() error = nil, want 建连超时错误")
	}
	if elapsed > 3*time.Second {
		t.Errorf("Start() 耗时 %v，应当在 DialTimeout 附近返回", elapsed)
	}
}
```

测试文件需要 import `"google.golang.org/grpc"`，供 invoker 的签名使用。

- [ ] **Step 2: 跑测试确认失败**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./component/grpcclient -v'
```

预期编译失败：`undefined: New`。

- [ ] **Step 3: 写 component/grpcclient/client.go**

```go
// Package grpcclient 提供 gRPC 客户端连接组件。
//
// 它把一条 grpc.ClientConn 的生命周期交给 App 管理：Start 建连、
// Health 探活、Stop 关闭。业务模块的远程实现拿着 Conn() 去构造
// 生成的客户端桩，因而不必各自管理连接。
package grpcclient

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Config 是 gRPC 客户端组件的配置。
type Config struct {
	// Name 是组件在 App 中的唯一标识。留空时取默认值。
	// 连多个下游服务时，必须给出互不相同的名字。
	Name string `yaml:"name"`
	// Target 是下游地址，形如 127.0.0.1:9000 或 dns:///user-service:9000。
	Target string `yaml:"target"`
	// DialTimeout 是建连与探活的超时。
	// 0 表示使用默认值；本组件不支持 sqldb 那样的 -1 哨兵。
	DialTimeout time.Duration `yaml:"dial_timeout"`
	// Block 决定 Start 是否等待连接真正就绪。
	// 置假时 Start 立刻返回，首次调用才会触发建连，适合下游可能晚于本服务启动的场景。
	Block bool `yaml:"block"`
}

// DefaultConfig 返回一组可直接使用的默认值。
func DefaultConfig() Config {
	return Config{
		Name:        "grpcclient",
		DialTimeout: 5 * time.Second,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.Name == "" {
		c.Name = d.Name
	}
	if c.DialTimeout == 0 {
		c.DialTimeout = d.DialTimeout
	}
	return c
}

// Option 用于定制 Client。
type Option func(*Client)

// WithUnaryInterceptor 追加一元拦截器，按传入顺序由外向内生效。
func WithUnaryInterceptor(is ...grpc.UnaryClientInterceptor) Option {
	return func(c *Client) { c.unary = append(c.unary, is...) }
}

// Client 是实现了 app.Component 方法集的 gRPC 连接。
type Client struct {
	cfg   Config
	unary []grpc.UnaryClientInterceptor

	mu   sync.Mutex
	conn *grpc.ClientConn
}

// New 创建客户端组件。此时并不建连，建连发生在 Start。
func New(cfg Config, opts ...Option) (*Client, error) {
	cfg = cfg.withDefaults()
	if cfg.Target == "" {
		return nil, fmt.Errorf("grpcclient: %s 的 target 不能为空", cfg.Name)
	}

	c := &Client{cfg: cfg}
	for _, fn := range opts {
		fn(c)
	}
	return c, nil
}

// Name 实现 app.Component。
func (c *Client) Name() string { return c.cfg.Name }

// Start 实现 app.Component，建立连接。
//
// Block 为真时会等到连接就绪或超时；为假时只创建连接对象，
// 真正的建连推迟到首次调用。
func (c *Client) Start(ctx context.Context) error {
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	if len(c.unary) > 0 {
		dialOpts = append(dialOpts, grpc.WithChainUnaryInterceptor(c.unary...))
	}

	conn, err := grpc.NewClient(c.cfg.Target, dialOpts...)
	if err != nil {
		return fmt.Errorf("grpcclient: %s 连接 %s 失败: %w", c.cfg.Name, c.cfg.Target, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	if !c.cfg.Block {
		return nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, c.cfg.DialTimeout)
	defer cancel()

	conn.Connect()
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return nil
		}
		if !conn.WaitForStateChange(waitCtx, state) {
			return fmt.Errorf("grpcclient: %s 建连 %s 超时: %w",
				c.cfg.Name, c.cfg.Target, waitCtx.Err())
		}
	}
}

// Stop 实现 app.Component，关闭连接。重复调用是安全的空操作。
func (c *Client) Stop(context.Context) error {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if conn == nil {
		return nil
	}
	if err := conn.Close(); err != nil {
		return fmt.Errorf("grpcclient: %s 关闭连接失败: %w", c.cfg.Name, err)
	}
	return nil
}

// Health 实现 app.HealthChecker，调用下游的标准健康检查服务。
func (c *Client) Health(ctx context.Context) error {
	conn := c.Conn()
	if conn == nil {
		return errors.New("grpcclient: " + c.cfg.Name + " 尚未建连")
	}

	ctx, cancel := context.WithTimeout(ctx, c.cfg.DialTimeout)
	defer cancel()

	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("grpcclient: %s 探活失败: %w", c.cfg.Name, err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("grpcclient: %s 下游状态为 %s", c.cfg.Name, resp.GetStatus())
	}
	return nil
}

// Conn 返回底层连接。Start 之前或 Stop 之后返回 nil。
func (c *Client) Conn() *grpc.ClientConn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}
```

- [ ] **Step 4: 写 component/grpcclient/interceptor.go**

```go
package grpcclient

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Kline-x/gokit/transport"
)

// transportByCode 是服务端映射表的逆向。两边必须对称，
// 否则同一个错误跨进程一个来回之后错误码会漂移。
var transportByCode = map[codes.Code]int{
	codes.OK:                 transport.CodeOK,
	codes.InvalidArgument:    transport.CodeInvalidArgument,
	codes.Unauthenticated:    transport.CodeUnauthenticated,
	codes.PermissionDenied:   transport.CodePermissionDenied,
	codes.NotFound:           transport.CodeNotFound,
	codes.AlreadyExists:      transport.CodeAlreadyExists,
	codes.FailedPrecondition: transport.CodeFailedPrecondition,
	codes.ResourceExhausted:  transport.CodeRateLimited,
	codes.Internal:           transport.CodeInternal,
	codes.Unavailable:        transport.CodeUnavailable,
	codes.DeadlineExceeded:   transport.CodeTimeout,
}

// TransportCode 返回 gRPC status code 对应的框架错误码。
// 表外的取值一律按内部错误处理。
func TransportCode(c codes.Code) int {
	if code, ok := transportByCode[c]; ok {
		return code
	}
	return transport.CodeInternal
}

// ErrorRestorer 把下游返回的 gRPC status 还原成 transport.Error。
//
// 服务端拦截器把 message 写成 "REASON: 描述"，这里按第一个冒号拆开；
// 拆不出来时整段当描述，Reason 留空。还原之后，调用方用 errors.Is
// 判断错误类型的写法在本地实现与远程实现下完全一致 ——
// 这正是模块从单体拆成服务时调用方代码不用改的原因。
func ErrorRestorer() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err == nil {
			return nil
		}

		st := status.Convert(err)
		reason, message := splitReason(st.Message())
		return transport.New(TransportCode(st.Code()), reason, message).WithCause(err)
	}
}

// splitReason 从 "REASON: 描述" 中拆出两段。没有冒号时整段都是描述。
func splitReason(msg string) (reason, message string) {
	before, after, found := strings.Cut(msg, ": ")
	if !found {
		return "", msg
	}
	return before, after
}
```

- [ ] **Step 5: 写 component/grpcclient/wire.go**

```go
package grpcclient

import "github.com/google/wire"

// ProviderSet 供 wire 装配 gRPC 客户端组件。使用方需自行提供 Config。
var ProviderSet = wire.NewSet(New)
```

- [ ] **Step 6: 跑测试确认通过**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go test ./component/grpcclient -v -race -count=1 -timeout 120s'
```

预期本包全部测试通过。注意 `TestTransportCodeIsInverseOfGRPCCode` 会 import `grpcserver` —— 这是测试文件里的依赖，不违反「组件互不依赖」，因为生产代码没有这条边。若 `go vet` 对此报循环依赖，停下来报告。

- [ ] **Step 7: 跑全量并提交**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

```bash
git add component/grpcclient
git commit -m "组件：gRPC 客户端连接与错误还原"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 8: 示例 — proto 契约与 gRPC 接口层

**Files:**
- Create: `example/minimal/api/greeter/v1/greeter.proto`
- Generate: `example/minimal/api/greeter/v1/greeter.pb.go`, `greeter_grpc.pb.go`
- Create: `example/minimal/internal/greeter/interfaces/grpc.go`
- Modify: `example/minimal/internal/greeter/application/service.go`（空名字改用 transport 错误）
- Modify: `example/minimal/internal/greeter/interfaces/http.go`（改用 transport 的统一响应）
- Modify: `example/minimal/internal/greeter/module.go`（新增 gRPC 接口层的 provider）
- Modify: `example/minimal/main.go`、`wire.go`、`config.yaml`
- Modify: `example/minimal/go.mod` / `go.sum`
- Modify: `example/minimal/main_test.go`

**Interfaces:**
- Consumes: 全部前序任务
- Produces: 同一个 `application.Service` 同时以 HTTP 与 gRPC 暴露

- [ ] **Step 1: 写 proto**

`example/minimal/api/greeter/v1/greeter.proto`：

```protobuf
syntax = "proto3";

package greeter.v1;

option go_package = "github.com/Kline-x/gokit/example/minimal/api/greeter/v1;greeterv1";

// Greeter 是问候模块对外的契约。
// 模块拆成独立服务后，这份 proto 随模块一起搬走，调用方按它生成客户端。
service Greeter {
  // Greet 返回某个名字的问候语。
  rpc Greet(GreetRequest) returns (GreetReply);
}

message GreetRequest {
  // name 是被问候者的名字，不能为空。
  string name = 1;
}

message GreetReply {
  // text 是问候语。
  string text = 1;
}
```

- [ ] **Step 2: 生成代码**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && PATH=/home/gaore/sdk/go/bin:/home/gaore/go/bin:/home/gaore/bin:$PATH make proto && ls example/minimal/api/greeter/v1/'
```

预期目录下出现 `greeter.pb.go` 与 `greeter_grpc.pb.go`。

若 `make` 因为 `GO` 变量没指向正确工具链而失败，用 `make GO=/home/gaore/sdk/go/bin/go proto`。

- [ ] **Step 3: 把应用层的错误改成 transport 错误**

`example/minimal/internal/greeter/application/service.go` 里，把名字为空的校验从 `errors.New` 改成框架错误，这样 HTTP 会返回 400、gRPC 会返回 InvalidArgument，而不是双双 500：

```go
	if req.Name == "" {
		return GreetReply{}, transport.InvalidArgument("NAME_REQUIRED", "name 不能为空").
			WithMetadata(map[string]string{"field": "name"})
	}
```

补上 import `"github.com/Kline-x/gokit/transport"`，并检查 `errors` 是否还被用到（`errors.Is(err, domain.ErrNotFound)` 那处还在用，应当保留）。

- [ ] **Step 4: 把 HTTP 接口层改成统一响应**

`example/minimal/internal/greeter/interfaces/http.go` 的 `greet` 方法改成：

```go
func (h *HTTPHandler) greet(w http.ResponseWriter, r *http.Request) {
	reply, err := h.svc.Greet(r.Context(), application.GreetRequest{
		Name: r.PathValue("name"),
	})
	if err != nil {
		if renderErr := transport.RenderError(w, err); renderErr != nil {
			log.FromContext(r.Context()).ErrorContext(r.Context(), "写出错误响应失败",
				slog.Any("error", renderErr))
		}
		return
	}

	if renderErr := transport.Render(w, map[string]string{"text": reply.Text}); renderErr != nil {
		log.FromContext(r.Context()).ErrorContext(r.Context(), "写出响应失败",
			slog.Any("error", renderErr))
	}
}
```

import 相应调整：去掉 `encoding/json`，加上 `log/slog`、`github.com/Kline-x/gokit/component/log`、`github.com/Kline-x/gokit/transport`。

- [ ] **Step 5: 写 gRPC 接口层**

`example/minimal/internal/greeter/interfaces/grpc.go`：

```go
package interfaces

import (
	"context"

	"google.golang.org/grpc"

	greeterv1 "github.com/Kline-x/gokit/example/minimal/api/greeter/v1"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/application"
)

// GRPCHandler 把问候用例暴露成 gRPC 接口。
//
// 它和 HTTPHandler 是并列的两个接口层实现，委托的是同一个
// application.Service —— 业务逻辑只有一份，协议有两种。
type GRPCHandler struct {
	greeterv1.UnimplementedGreeterServer
	svc application.Service
}

// NewGRPCHandler 构造 gRPC 接口层处理器。
func NewGRPCHandler(svc application.Service) *GRPCHandler {
	return &GRPCHandler{svc: svc}
}

// Register 实现 grpcserver.ServiceRegistrar，把本服务挂到 gRPC 服务器上。
func (h *GRPCHandler) Register(s *grpc.Server) {
	greeterv1.RegisterGreeterServer(s, h)
}

// Greet 实现 greeterv1.GreeterServer，只做协议转换。
// 业务错误原样返回，由服务端拦截器翻译成 gRPC status。
func (h *GRPCHandler) Greet(ctx context.Context, req *greeterv1.GreetRequest) (*greeterv1.GreetReply, error) {
	reply, err := h.svc.Greet(ctx, application.GreetRequest{Name: req.GetName()})
	if err != nil {
		return nil, err
	}
	return &greeterv1.GreetReply{Text: reply.Text}, nil
}
```

- [ ] **Step 6: 把 gRPC 接口层接进模块装配**

`example/minimal/internal/greeter/module.go` 的 `LocalSet` 里追加：

```go
	interfaces.NewGRPCHandler,
```

- [ ] **Step 7: 在组合根装上 gRPC 服务**

`example/minimal/main.go` 的聚合配置加一段：

```go
	GRPC grpcserver.Config `yaml:"grpc"`
```

`defaultConfig()` 里补 `GRPC: grpcserver.DefaultConfig()`。

新增三个 provider：

```go
func provideGRPCConfig(cfg Config) grpcserver.Config { return cfg.GRPC }

// provideServiceRegistrars 列出要挂到 gRPC 服务器上的服务。
// 各业务模块的 gRPC 接口层在这里汇总。
func provideServiceRegistrars(greeter *interfaces.GRPCHandler) []grpcserver.ServiceRegistrar {
	return []grpcserver.ServiceRegistrar{greeter}
}

// provideGRPCServer 把 App.Fatal 接给服务，并装上三个拦截器。
// 顺序与 HTTP 侧一致：RequestLog 在最外层，其次 Recover，最内层是错误映射。
func provideGRPCServer(
	cfg grpcserver.Config,
	services []grpcserver.ServiceRegistrar,
	logger *log.Logger,
	a *app.App,
) *grpcserver.Server {
	return grpcserver.New(cfg, services,
		grpcserver.WithFatal(a.Fatal),
		grpcserver.WithUnaryInterceptor(
			grpcserver.RequestLog(logger.Logger),
			grpcserver.Recover(logger.Logger),
			grpcserver.ErrorMapper(),
		),
	)
}
```

`Bundle` 加一个 `GRPC *grpcserver.Server` 字段（测试要读它的真实端口），`provideComponents` 的入参与返回值都加上它。

`wire.go` 的 `wire.Build` 里追加 `provideGRPCConfig`、`provideServiceRegistrars`、`provideGRPCServer`。

`example/minimal/config.yaml` 加一段：

```yaml
grpc:
  name: "grpcserver"
  addr: ":9000"
  shutdown_timeout: 10s
  enable_reflection: true
```

- [ ] **Step 8: 重新生成 wire**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && /home/gaore/sdk/go/bin/go mod tidy && PATH=/home/gaore/sdk/go/bin:/home/gaore/go/bin:$PATH wire ./...'
```

`wire_gen.go` 是工具产物，绝不手写。报缺 provider 就补 `provideXxx`，绝不改组件包。

- [ ] **Step 9: 写双协议端到端测试**

在 `example/minimal/main_test.go` 追加：

```go
func TestGreetOverGRPC(t *testing.T) {
	cfg := defaultConfig()
	cfg.HTTP.Addr = "127.0.0.1:0"
	cfg.GRPC.Addr = "127.0.0.1:0"
	cfg.DB.DSN = "file:e2egrpc?mode=memory&cache=shared"
	cfg.Log.Output = filepath.Join(t.TempDir(), "app.log")

	b, err := initApp(cfg)
	if err != nil {
		t.Fatalf("initApp() error = %v", err)
	}

	ctx := context.Background()
	a := b.Register()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(ctx) })

	conn, err := grpc.NewClient(b.GRPC.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcclient.ErrorRestorer()))
	if err != nil {
		t.Fatalf("建连失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	client := greeterv1.NewGreeterClient(conn)

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	reply, err := client.Greet(callCtx, &greeterv1.GreetRequest{Name: "gokit"})
	if err != nil {
		t.Fatalf("Greet() error = %v", err)
	}
	if reply.GetText() != "你好，gokit" {
		t.Errorf("text = %q, want %q", reply.GetText(), "你好，gokit")
	}

	// 同一个名字再走一次 HTTP，两条协议必须返回同一份数据。
	resp, err := http.Get("http://" + b.HTTP.Addr().String() + "/greet/gokit")
	if err != nil {
		t.Fatalf("HTTP 请求失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	var envelope struct {
		Code int               `json:"code"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("HTTP 响应不是合法 JSON: %v, 内容=%s", err, body)
	}
	if envelope.Data["text"] != reply.GetText() {
		t.Errorf("两条协议返回不一致: HTTP=%q gRPC=%q", envelope.Data["text"], reply.GetText())
	}

	// 只应落一行库，说明两次调用走的是同一套业务逻辑与同一张表。
	var count int
	db := findDB(t, b)
	row := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM greetings WHERE name = ?`, "gokit")
	if err := row.Scan(&count); err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 1 {
		t.Errorf("greetings 行数 = %d, want 1", count)
	}
}

func TestGreetErrorSemanticsMatchAcrossProtocols(t *testing.T) {
	cfg := defaultConfig()
	cfg.HTTP.Addr = "127.0.0.1:0"
	cfg.GRPC.Addr = "127.0.0.1:0"
	cfg.DB.DSN = "file:e2eerr?mode=memory&cache=shared"
	cfg.Log.Output = filepath.Join(t.TempDir(), "app.log")

	b, err := initApp(cfg)
	if err != nil {
		t.Fatalf("initApp() error = %v", err)
	}

	ctx := context.Background()
	a := b.Register()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(ctx) })

	// 空名字在两条协议上都应当是「参数不合法」，而不是内部错误。
	conn, err := grpc.NewClient(b.GRPC.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcclient.ErrorRestorer()))
	if err != nil {
		t.Fatalf("建连失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, grpcErr := greeterv1.NewGreeterClient(conn).Greet(callCtx, &greeterv1.GreetRequest{Name: ""})
	if grpcErr == nil {
		t.Fatal("空名字的 gRPC 调用应当失败")
	}

	var te *transport.Error
	if !errors.As(grpcErr, &te) {
		t.Fatalf("客户端拦截器未把错误还原成 transport.Error: %v", grpcErr)
	}
	if te.Code != transport.CodeInvalidArgument {
		t.Errorf("gRPC 侧 Code = %d, want %d", te.Code, transport.CodeInvalidArgument)
	}
	if te.Reason != "NAME_REQUIRED" {
		t.Errorf("gRPC 侧 Reason = %q, want %q", te.Reason, "NAME_REQUIRED")
	}
}
```

`findDB` 是现有测试里从 `b.Components` 按类型找出 `*sqldb.DB` 的写法 —— 读一下现有的 `TestGreetEndToEnd` 看它叫什么、怎么写的，复用它而不是重写一份。若它是内联的一段循环而不是函数，就把它提成 `findDB(t *testing.T, b *Bundle) *sqldb.DB` 再共用。

注意空名字这条路径：Go 1.22 的 `ServeMux` 通配符段不匹配空路径段，所以 HTTP 侧无法构造出空名字请求，本测试只在 gRPC 侧验证。若想覆盖 HTTP 侧，另加一条形如 `/greet/%20` 的请求也不会命中校验 —— 这是路由层面的事实，不是缺陷，不要为此改校验逻辑。

测试文件需要新增 import：`errors`、`google.golang.org/grpc`、`google.golang.org/grpc/credentials/insecure`、`github.com/Kline-x/gokit/component/grpcclient`、`github.com/Kline-x/gokit/transport`、`greeterv1 "github.com/Kline-x/gokit/example/minimal/api/greeter/v1"`。

- [ ] **Step 10: 跑测试**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

预期示例模块全部测试通过，包括原有的 `TestGreetEndToEnd`。

注意原有的 `TestGreetEndToEnd` 断言的是裸的 `{"text":"..."}`，而 Step 4 把 HTTP 响应改成了统一信封 `{"code":0,"data":{"text":"..."}}`。**这个测试必须跟着改**，改成解析信封后比对 `data.text`。这是预期内的变更，不是回归。

- [ ] **Step 11: 手工跑一遍真实服务**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && timeout 10 /home/gaore/sdk/go/bin/go run . > /tmp/minimal-grpc.log 2>&1 & sleep 5; curl -s http://127.0.0.1:8080/greet/gokit; echo; sleep 5; cat /tmp/minimal-grpc.log'
```

预期 HTTP 返回统一信封，日志里能看到 `log → sqldb → greeter.migrator → httpserver → grpcserver` 之类的启动顺序（httpserver 与 grpcserver 之间没有依赖关系，谁先谁后取决于注册顺序），以及停止时的逆序。

若后台进程在这套工具下跑不稳，如实说明并以测试结果为准，不要反复跟 shell 较劲。

- [ ] **Step 12: 跑全量并提交**

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

```bash
git add example Makefile
git commit -m "示例：同一个 Service 同时以 HTTP 与 gRPC 暴露"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## Task 9: README 与设计文档回填

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-09-15-gokit-framework-design.md`

**Interfaces:**
- Consumes: 全部前序任务
- Produces: 文档与代码一致

- [ ] **Step 1: 更新 README 的包表**

在 `README.md` 的包清单里加两行，并补一段说明统一错误语义。读一下现有 README 的结构再动手，保持行文一致。要加的内容：

- `transport` 一行：与协议无关的错误类型、错误码表、HTTP 统一响应
- `component/grpcserver` 一行：gRPC 服务组件，自带健康检查与反射
- `component/grpcclient` 一行：gRPC 客户端连接组件
- 一段短说明：业务层只返回 `transport.Error`，HTTP 侧由 `RenderError` 翻译成状态码加统一信封，gRPC 侧由服务端拦截器翻译成 status、客户端拦截器再还原回 `transport.Error`，因此同一个错误在两条协议上语义一致，调用方判断错误的写法在本地实现与远程实现下也一致。

把「本版不包含」里的「gRPC 组件」「统一错误码与响应」两条删掉，其余保留。

- [ ] **Step 2: 在设计文档里标注进度**

在 `docs/superpowers/specs/2026-09-15-gokit-framework-design.md` 第 12 节的第 4、5 步后面各加一个「已完成」标记，写明对应的计划文件是 `docs/superpowers/plans/2026-09-16-transport-and-grpc.md`。不要改动设计文档的其他内容。

- [ ] **Step 3: 提交**

```bash
git add README.md docs
git commit -m "文档：补上 transport 与两个 gRPC 组件"
```

提交信息末尾加一行 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

## 完成标准

全部 9 个 Task 做完后，下列命令必须全部通过：

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

```bash
wsl -u gaore bash -lc 'cd /mnt/e/code/AI/vibCoding/gokit/example/minimal && /home/gaore/sdk/go/bin/go vet ./... && /home/gaore/sdk/go/bin/go test ./... -race -count=1 -timeout 180s'
```

以及手工验证：`example/minimal` 能同时在 8080 提供 HTTP、在 9000 提供 gRPC，两条协议对同一个名字返回同一份数据，空名字在两边都是「参数不合法」而不是内部错误。

此时设计文档第 12 节的第 4、5 步完成。剩余的第 6 至 8 步（脚手架 CLI、拆分演示、可选组件）各自另立计划。
