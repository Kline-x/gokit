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
