package transport

import "errors"

// 框架的错误码表。取值刻意与 HTTP 状态码对齐，方便直觉理解；
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
