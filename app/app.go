package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// errStoppedDuringStart 表示启动过程中应用已被并发停止，剩余组件不再启动。
var errStoppedDuringStart = errors.New("应用在启动过程中已被停止")

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

	components, err := sortComponents(components)
	if err != nil {
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
		if a.stopped {
			a.mu.Unlock()
			// Stop 已并发执行过，且它看不到这个刚启动的组件。
			// 立刻回收它并中止剩余启动，避免组件被永久遗留。
			if stopErr := c.Stop(context.WithoutCancel(ctx)); stopErr != nil {
				return errors.Join(errStoppedDuringStart,
					fmt.Errorf("回收组件 %s 失败: %w", c.Name(), stopErr))
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
		if err := c.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("停止组件 %s 失败: %w", c.Name(), err))
			continue
		}
		a.opts.logger.InfoContext(ctx, "组件已停止", slog.String("component", c.Name()))
	}
	return errors.Join(errs...)
}
