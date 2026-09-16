package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
)

// errStoppedDuringStart 表示启动过程中应用已被并发停止，剩余组件不再启动。
var errStoppedDuringStart = errors.New("app: 应用在启动过程中已被停止")

// App 编排一组 Component 的生命周期：按依赖顺序启动、逆序停止。
//
// App 不是服务定位器：组件之间的依赖一律由构造时注入解决，
// App 只负责启停顺序，不提供按名字查找组件的能力。
type App struct {
	opts    options
	fatalCh chan error

	mu          sync.Mutex
	components  []Component
	started     []Component
	stopped     bool
	startCalled bool
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
//
// 必须在 Start 之前调用。启动之后再注册是编程错误，会直接 panic——
// 静默忽略只会让人以为组件已经在跑了。
func (a *App) Register(cs ...Component) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.startCalled {
		panic("app: 不能在 Start 之后注册组件")
	}
	a.components = append(a.components, cs...)
}

// Start 依次启动全部组件。任一组件启动失败时，
// 已启动的组件会被逆序停止，错误原样返回。
//
// 注意：回滚只覆盖已成功启动的组件。构造好但没轮到启动的组件、
// 以及启动失败的那一个，它们在构造期占用的资源需要调用方自行释放。
func (a *App) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.startCalled {
		a.mu.Unlock()
		return errors.New("app: Start 已经调用过，不能重复启动")
	}
	a.startCalled = true
	components := make([]Component, len(a.components))
	copy(components, a.components)
	a.mu.Unlock()

	components, err := sortComponents(components)
	if err != nil {
		return err
	}

	for _, c := range components {
		a.mu.Lock()
		stopped := a.stopped
		a.mu.Unlock()
		if stopped {
			// 已经被并发停止，剩下的组件不必再启动。
			return errStoppedDuringStart
		}

		if err := c.Start(ctx); err != nil {
			startErr := fmt.Errorf("app: 启动组件 %s 失败: %w", c.Name(), err)
			if stopErr := a.Stop(context.WithoutCancel(ctx)); stopErr != nil {
				return errors.Join(startErr, stopErr)
			}
			return startErr
		}
		a.mu.Lock()
		if a.stopped {
			a.mu.Unlock()
			// Stop 已并发执行过，且它看不到这个刚启动的组件。
			// 立刻回收它并中止剩余启动，避免组件被永久遗留。
			if stopErr := c.Stop(context.WithoutCancel(ctx)); stopErr != nil {
				return errors.Join(errStoppedDuringStart,
					fmt.Errorf("app: 回收组件 %s 失败: %w", c.Name(), stopErr))
			}
			return errStoppedDuringStart
		}
		a.started = append(a.started, c)
		a.mu.Unlock()
		a.opts.logger.InfoContext(ctx, "组件已启动", slog.String("component", c.Name()))
	}
	return nil
}

// Stop 逆序停止已启动的组件，并汇总所有错误。重复调用是安全的空操作。
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
	a.stopped = true
	started := a.started
	a.started = nil
	a.mu.Unlock()

	var errs []error
	for i := len(started) - 1; i >= 0; i-- {
		c := started[i]
		// 先记录再停止：日志组件通常最后才停，一旦它关掉输出，
		// 之后写的任何日志都会静默丢失。
		a.opts.logger.InfoContext(ctx, "正在停止组件", slog.String("component", c.Name()))
		if err := c.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("app: 停止组件 %s 失败: %w", c.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// Health 对所有已启动且实现了 HealthChecker 的组件做一次探活，并汇总错误。
// 未实现该接口的组件视为健康。尚未启动或已停止的组件不参与探活。
func (a *App) Health(ctx context.Context) error {
	a.mu.Lock()
	started := make([]Component, len(a.started))
	copy(started, a.started)
	a.mu.Unlock()

	var errs []error
	for _, c := range started {
		hc, ok := c.(HealthChecker)
		if !ok {
			continue
		}
		if err := hc.Health(ctx); err != nil {
			errs = append(errs, fmt.Errorf("app: 组件 %s 探活失败: %w", c.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// Run 启动全部组件并阻塞，直到 ctx 取消、收到退出信号，或有组件通过 Fatal 上报致命错误。
// 返回前会执行一次优雅停止，停止阶段的错误与致命错误一并返回。
func (a *App) Run(ctx context.Context) error {
	if err := a.Start(ctx); err != nil {
		return err
	}

	a.opts.logger.InfoContext(ctx, "应用已启动",
		slog.String("name", a.opts.name),
		slog.String("version", a.opts.version))

	// 始终准备好信号通道，只有配置了信号时才真正订阅。
	// 未订阅的通道永远不会触发，因此一个 select 就能覆盖两种情形。
	sigCh := make(chan os.Signal, 1)
	if len(a.opts.signals) > 0 {
		signal.Notify(sigCh, a.opts.signals...)
		defer signal.Stop(sigCh)
	}

	var runErr error
	select {
	case <-ctx.Done():
	case sig := <-sigCh:
		a.opts.logger.InfoContext(ctx, "收到退出信号", slog.String("signal", sig.String()))
	case runErr = <-a.Done():
	}

	a.opts.logger.InfoContext(ctx, "应用开始退出", slog.String("name", a.opts.name))
	stopErr := a.Stop(context.WithoutCancel(ctx))
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

// Done 返回致命错误通道。组件通过 Fatal 上报的错误从这里送出。
//
// Run 内部等的就是它。当宿主自带事件循环（例如 Wails 桌面端），
// 调用方用 Start/Stop 自行管理生命周期时，应当自己 select 这个通道，
// 否则组件在运行期崩溃将无从感知。通道容量为 1，只保留第一个错误。
func (a *App) Done() <-chan error { return a.fatalCh }
