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
//
// 返回的 error 只够用来记日志，不足以补救：状态码与响应头在编码之前
// 就已经写出去了，此刻没有任何办法改写它们。调用方拿到它应当记一条日志，
// 而不是试图再渲染一次。
func Render(w http.ResponseWriter, data any) error {
	return write(w, http.StatusOK, Response{Code: CodeOK, Data: data})
}

// RenderError 写出一个错误响应。
//
// 任何 error 都能传进来：不是 *Error 的会被归一成内部错误。
// 错误的 cause 只服务于进程内的 errors.Is，不会出现在响应体里。
//
// 返回的 error 只够用来记日志，不足以补救：状态码与响应头在编码之前
// 就已经写出去了，此刻没有任何办法改写它们。调用方拿到它应当记一条日志，
// 而不是试图再渲染一次。
func RenderError(w http.ResponseWriter, err error) error {
	e := FromError(err)
	if e == nil {
		return Render(w, nil)
	}
	code := e.StatusCode()
	return write(w, HTTPStatus(code), Response{
		Code:     code,
		Reason:   e.Reason,
		Message:  e.Message,
		Metadata: e.Metadata,
	})
}

// write 落盘一个响应。注意写出顺序：先定头、再写状态码、最后编码正文，
// 因此编码阶段的失败已经无法回退状态码，只能原样返回给调用方去记日志。
func write(w http.ResponseWriter, status int, body Response) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(body)
}
