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
