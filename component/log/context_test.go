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
