// Package transport 定义与具体协议无关的错误语义与 HTTP 侧的统一响应。
//
// 这里的 Error 是整个框架的错误载体：业务层返回它，HTTP 与 gRPC 两侧
// 各自把它翻译成本协议的表达，因此同一个错误在两条协议上语义一致。
//
// 本包只依赖标准库。gRPC 侧的转换在 component/grpcserver 与
// component/grpcclient 里，避免把 gRPC 拖进这个最底层的包。
package transport

import (
	"context"
	"errors"
	"fmt"
	"maps"
)

// Error 是框架统一的错误类型。
//
// Code 决定协议层的状态（HTTP 状态码、gRPC status code），
// Reason 是稳定的机器可读标识，调用方按它做分支判断，
// Message 是给人看的描述，可以随时改而不影响调用方。
type Error struct {
	// Code 是错误的分类码，取值见 code.go 中的常量。
	Code int
	// Reason 是稳定的机器可读标识，例如 USER_NOT_FOUND。
	// 按约定只用大写字母、数字与下划线：gRPC 侧靠这个形状把 Reason 从
	// status 的文本里认出来，掺入小写或空格会让它跨进程后还原不回来。
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

// StatusCode 返回在协议边界上应当使用的错误码。
//
// 它与 Code 字段几乎总是一致，唯一的例外是 Code 为 CodeOK：一个走到错误路径
// 的错误却带着「成功」码，多半是用复合字面量构造时漏填了 Code。若原样发出去，
// HTTP 会回 200、gRPC 会把错误吞成 nil，调用方收到一个「成功但空」的响应，
// 比直接报错难查得多，所以这里强制归为内部错误。
func (e *Error) StatusCode() int {
	if e.Code == CodeOK {
		return CodeInternal
	}
	return e.Code
}

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
// 已经是 *Error 的原样返回；其余一律归为内部错误，Message 给一句泛化
// 描述而不是原始错误文本——Message 是客户端可见的，原文可能带表名、
// 驱动细节等内情。原始错误只挂在 cause 上，好让调用方仍能用 errors.Is
// 找到它，但不会跨进程传递。nil 返回 nil。
func FromError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	// 上下文的两个哨兵单独归类，好让同一次取消或超时在单体与拆分之后
	// 给出同样的 Code 与 Reason —— gRPC 侧的 ErrorRestorer 对它们给的正是这一组。
	if errors.Is(err, context.DeadlineExceeded) {
		return New(CodeTimeout, "DEADLINE_EXCEEDED", "处理超时").WithCause(err)
	}
	if errors.Is(err, context.Canceled) {
		return New(CodeInternal, "CANCELED", "调用已取消").WithCause(err)
	}
	// 未归类的错误一律给一句泛化描述：Message 是客户端可见的，
	// 把底层错误原文塞进去会泄漏表名、驱动细节之类的内情。
	// 真正的原因挂在 cause 上，只在进程内可达，由服务端的日志中间件记录。
	return New(CodeInternal, "INTERNAL", "内部错误").WithCause(err)
}
