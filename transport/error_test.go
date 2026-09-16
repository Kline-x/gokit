package transport

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

func TestErrorIsRejectsTargetThatMerelyWrapsAnError(t *testing.T) {
	actual := New(404, "USER_NOT_FOUND", "id=7 的用户不存在")
	// target 自身不是 *Error，只是包了一个。拆解调用方的错误链是
	// errors.Is 的职责，Is 方法不该越过这层去匹配。
	target := fmt.Errorf("上下文: %w", New(404, "USER_NOT_FOUND", ""))

	if errors.Is(actual, target) {
		t.Error("target 只是包装了一个同类错误，不应判定为匹配")
	}
}

func TestErrorIsStillMatchesWhenReceiverIsWrapped(t *testing.T) {
	// 反过来：被包装的是接收方那条链时，errors.Is 会自己拆开，应当匹配。
	// 这条与上一条一起，把「谁负责拆链」这件事钉住。
	sentinel := New(404, "USER_NOT_FOUND", "")
	wrapped := fmt.Errorf("查询失败: %w", New(404, "USER_NOT_FOUND", "id=7"))

	if !errors.Is(wrapped, sentinel) {
		t.Error("接收方被包装时 errors.Is 仍应匹配")
	}
}

func TestStatusCodeCoercesOKButLeavesOthers(t *testing.T) {
	if got := (&Error{}).StatusCode(); got != CodeInternal {
		t.Errorf("零码的 StatusCode() = %d, want %d", got, CodeInternal)
	}
	if got := NotFound("X", "y").StatusCode(); got != CodeNotFound {
		t.Errorf("StatusCode() = %d, want %d（非零码应原样返回）", got, CodeNotFound)
	}
}

func TestFromErrorDoesNotLeakUnknownErrorText(t *testing.T) {
	// Message 是客户端可见的，底层错误原文不能进去。
	plain := errors.New("dial tcp 10.0.0.1:3306: connect: connection refused")
	got := FromError(plain)

	if strings.Contains(got.Message, "10.0.0.1") {
		t.Errorf("Message = %q，泄漏了底层错误原文", got.Message)
	}
	if got.Message == "" {
		t.Error("Message 不应为空，客户端需要一句能看的描述")
	}
	// 原因仍要在进程内可达，否则排障就断了。
	if !errors.Is(got, plain) {
		t.Error("原始错误应当仍可被 errors.Is 找到")
	}
}

func TestFromErrorClassifiesContextSentinels(t *testing.T) {
	deadline := FromError(fmt.Errorf("查询超时: %w", context.DeadlineExceeded))
	if deadline.Code != CodeTimeout {
		t.Errorf("超时的 Code = %d, want %d", deadline.Code, CodeTimeout)
	}
	if deadline.Reason != "DEADLINE_EXCEEDED" {
		t.Errorf("超时的 Reason = %q", deadline.Reason)
	}
	if !errors.Is(deadline, context.DeadlineExceeded) {
		t.Error("errors.Is 应当仍能认出 context.DeadlineExceeded")
	}

	canceled := FromError(fmt.Errorf("调用取消: %w", context.Canceled))
	if canceled.Code != CodeInternal {
		t.Errorf("取消的 Code = %d, want %d", canceled.Code, CodeInternal)
	}
	if canceled.Reason != "CANCELED" {
		t.Errorf("取消的 Reason = %q", canceled.Reason)
	}
	if !errors.Is(canceled, context.Canceled) {
		t.Error("errors.Is 应当仍能认出 context.Canceled")
	}
}
