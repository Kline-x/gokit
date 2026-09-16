# 把一个模块拆成独立进程：实际发生了什么

这份记录基于 `develop/xuyang/gokit-split-demo` 分支的真实改动：把
`example/minimal` 里的问候模块从单体进程切出去，变成两个协作的进程。
以下内容全部对照 `git log` / `git diff` 核实过，不描述计划里设想、但实际没发生的步骤。

## 改了什么

- 单体二进制搬进 `example/minimal/cmd/monolith`（原来的 `main.go` /
  `wire.go` / `wire_gen.go` 平移过去，只改了包内一处相对路径引用）。
- 新增 `example/minimal/internal/greeter/remote/client.go`：问候模块的
  出站适配器，用 gRPC 调远程的问候服务。
- 新增 `RemoteSet`（`internal/greeter/module.go`），与既有的 `LocalSet`
  并列。
- 两个新二进制：
  - `example/minimal/cmd/greeter`：只装问候模块自己的东西——日志、
    数据库、建表、gRPC 服务，没有 HTTP。
  - `example/minimal/cmd/gateway`：对外的 HTTP 入口，自己不实现业务，
    装 `greeter.RemoteSet` 走 gRPC 调 `greeter`。
- 对应的配置文件：`configs/greeter.yaml`（gRPC 监听 `:9000`、SQLite
  DSN）、`configs/gateway.yaml`（HTTP 监听 `:8080`、下游目标
  `127.0.0.1:9000`）；原来的 `config.yaml` 改名进
  `configs/monolith.yaml`。
- 新增 `internal/parity/parity_test.go`，见下文「怎么验证拆对了」。
- `.gitignore` 加了一行 `*.db`（起本地服务会在工作区留下 SQLite 文件）。

## 没改什么

`domain`、`application`、`interfaces` 三层一行没动——这是这次拆分要证明
的核心。核实方式：

```bash
git diff --stat origin/master..HEAD -- "*/domain/*" "*/application/*" "*/interfaces/*"
```

输出为空。整个分支的改动集中在装配层（`module.go` 新增 `RemoteSet`）、
新的出站适配器（`remote/`）、两个新二进制的组合根，以及为验证拆分新写的
测试。三层业务代码本身没有一次提交碰过。

## `LocalSet` 与 `RemoteSet` 的差异

两个 ProviderSet 并排贴出来（均取自 `internal/greeter/module.go`）：

```go
// LocalSet 是单体部署下的装配集合：Service 绑定到进程内实现。
var LocalSet = wire.NewSet(
	infrastructure.NewGreetingRepo,
	wire.Bind(new(domain.Repository), new(*infrastructure.GreetingRepo)),

	infrastructure.NewMigrator,

	application.NewLocalService,
	wire.Bind(new(application.Service), new(*application.LocalService)),

	interfaces.NewHTTPHandler,
	interfaces.NewGRPCHandler,
)

// RemoteSet 是模块拆成独立服务后，调用方使用的装配集合。
var RemoteSet = wire.NewSet(
	remote.NewService,
	wire.Bind(new(application.Service), new(*remote.Service)),

	interfaces.NewHTTPHandler,
)
```

`RemoteSet` 不需要仓储、不需要迁移、不需要数据库——那些都跟着服务走了；
也不需要 `interfaces.NewGRPCHandler`，因为调用方不对外提供这个 gRPC 服务。
它需要的只是一条到下游的连接，由调用方（`cmd/gateway`）在组合根里提供。
换掉 `LocalSet` 这件事，对 `domain`、`application`、`interfaces` 三层完全
不可见。

## 一个真实的坑：连接不能在构造时就取

`internal/greeter/remote/client.go` 里的 `Service` 起初的写法是在
`NewService` 里就调用 `c.Conn()` 存成员变量。这在测试里立刻炸了：wire
把所有对象装配完是在 `App.Start` 之前，而 `grpcclient.Client` 这时还没
拨号，`Conn()` 返回 `nil`，拿它造出来的 gRPC 桩一调用就 panic。

修复方式是不在构造时取连接，只存 `*grpcclient.Client` 本身，每次调用时
现取：

```go
func (s *Service) Greet(ctx context.Context, req application.GreetRequest) (application.GreetReply, error) {
	conn := s.conn.Conn()
	if conn == nil {
		return application.GreetReply{}, transport.Unavailable(
			"GREETER_NOT_CONNECTED", "问候服务尚未建立连接")
	}
	reply, err := greeterv1.NewGreeterClient(conn).Greet(ctx, &greeterv1.GreetRequest{Name: req.Name})
	...
}
```

造桩很便宜（只是包一层 `conn`，没有握手），所以每次调用现造不是问题。
教训是：任何依赖“组件已经 Start 过”的资源，都不能在 wire 装配阶段
（构造函数里）就去取，只能在真正调用的那一刻取。

## 怎么验证拆对了

`internal/parity/parity_test.go` 只有一个测试，同一组断言分别打到本地
实现和跨进程的远程实现上，两边都必须通过：正常问候的文案、直接读库验证
读的是存储而不是现算、写两次验证不重复插入、参数错误时 `errors.Is` 与
`transport.Code` 的判断方式一致。

这个测试在开发过程中还纠正过一次自己：最初的写法是断言数据库关闭后
`err.Error()` 里不包含内部细节，这在本地实现下失败——本地拿到的错误是
`sqldb: 开启事务失败: sql: database is closed` 这样的完整错误链。这不是
框架的问题，是断言本身想错了：同一进程内没有跨越信任边界，完整的错误链
正是 `cause`（wrap 链）存在的意义，脱敏本就该发生在协议边界上，也就是
`transport.RenderError`（HTTP）与 `grpcserver.ErrorMapper`（gRPC）已经在
做的事。测试后来改成断言 `transport.FromError(err).Message`——也就是真正
会发给客户端的那句话——本地和远程都能过。

由此得到的契约要写清楚：**`Code`、`Reason`、`Metadata` 与 `errors.Is` 的
判断方式在本地实现和远程实现下完全一致；错误字符串（`err.Error()`）不
一致，调用方不应该去解析它。**

取消与超时也算数——`ErrorRestorer` 会把 context 的哨兵挂回 cause 上，
`FromError` 对本地那侧给出同样的 Code 与 Reason，所以
`errors.Is(err, context.Canceled)` 这类写法两边都成立。对等测试盯着这一条。

网关原本会把下游的地址与建连失败原文发给客户端——gRPC 自己产生的传输层
错误没有 ErrorInfo detail、也从没脱过敏，而 `ErrorRestorer` 当时把 status
文本直接当成了对客户端可见的 Message。现在没有 detail 时一律给泛化描述，
原文留在 cause 上进日志。

## 还差什么

设计文档拆分四步里，这次只做了第 2 步：多进程、同一个仓库。第 3 步
（搬到独立仓库）和第 4 步（模块间事件改走消息队列而不是进程内调用）都
还没做，本记录不覆盖。
