package grpcserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/transport"
)

func unaryInfo() *grpc.UnaryServerInfo {
	return &grpc.UnaryServerInfo{FullMethod: "/greeter.v1.Greeter/Greet"}
}

func TestErrorMapperTranslatesTransportError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"not found", transport.NotFound("USER_NOT_FOUND", "用户不存在"), codes.NotFound},
		{"invalid", transport.InvalidArgument("BAD_INPUT", "参数有误"), codes.InvalidArgument},
		{"denied", transport.PermissionDenied("FORBIDDEN", "无权访问"), codes.PermissionDenied},
		{"unavailable", transport.Unavailable("DOWN", "依赖不可用"), codes.Unavailable},
		{"unknown", errors.New("随便一个错误"), codes.Internal},
	}

	mapper := ErrorMapper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mapper(context.Background(), nil, unaryInfo(),
				func(context.Context, any) (any, error) { return nil, tc.err })

			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("返回的不是 gRPC status: %v", err)
			}
			if st.Code() != tc.want {
				t.Errorf("code = %v, want %v", st.Code(), tc.want)
			}
		})
	}
}

func TestErrorMapperCarriesReasonInMessage(t *testing.T) {
	mapper := ErrorMapper()
	_, err := mapper(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) {
			return nil, transport.NotFound("USER_NOT_FOUND", "用户不存在")
		})

	st, _ := status.FromError(err)
	if !strings.Contains(st.Message(), "USER_NOT_FOUND") {
		t.Errorf("status message = %q，应当带上 Reason 以便客户端还原", st.Message())
	}
}

func TestErrorMapperLeavesSuccessAlone(t *testing.T) {
	mapper := ErrorMapper()
	got, err := mapper(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) { return "ok", nil })

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != "ok" {
		t.Errorf("resp = %v, want ok", got)
	}
}

func TestRecoverTurnsPanicIntoInternalStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	_, err := Recover(logger)(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) { panic("boom") })

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("返回的不是 gRPC status: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal", st.Code())
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("日志未记录 panic 内容，实际为 %q", buf.String())
	}
}

func TestRequestLogRecordsMethodAndCode(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	_, err := RequestLog(logger)(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) {
			return nil, status.Error(codes.NotFound, "没找到")
		})
	if err == nil {
		t.Fatal("handler 的错误应当原样返回")
	}

	var entry map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); jsonErr != nil {
		t.Fatalf("日志不是合法 JSON: %v, 内容=%q", jsonErr, buf.String())
	}
	if entry["method"] != "/greeter.v1.Greeter/Greet" {
		t.Errorf("method = %v", entry["method"])
	}
	if entry["code"] != codes.NotFound.String() {
		t.Errorf("code = %v, want %v", entry["code"], codes.NotFound.String())
	}
}

func TestRequestLogInjectsLoggerIntoContext(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	var injected bool
	_, err := RequestLog(logger)(context.Background(), nil, unaryInfo(),
		func(ctx context.Context, _ any) (any, error) {
			injected = log.FromContext(ctx) == logger
			return nil, nil
		})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !injected {
		t.Error("处理函数未能从 ctx 取到注入的 logger")
	}
}

func TestErrorMapperTranslatesRelayedDownstreamError(t *testing.T) {
	// 中转场景：本服务把下游返回的错误原样往上抛。grpcclient 还原出来的
	// transport.Error 会把原始 status 挂在 cause 上，而 grpc 的 status.FromError
	// 会用 errors.As 认出它——必须先按框架错误翻译，否则 Reason 会丢、
	// 内部细节会漏给外部客户端。
	downstream := status.Error(codes.NotFound, "USER_NOT_FOUND: 用户不存在")
	relayed := transport.NotFound("USER_NOT_FOUND", "用户不存在").WithCause(downstream)

	_, err := ErrorMapper()(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) { return nil, relayed })

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("返回的不是 gRPC status: %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("code = %v, want NotFound", st.Code())
	}
	if st.Message() != "USER_NOT_FOUND: 用户不存在" {
		t.Errorf("message = %q，应当是重新翻译出来的，而不是原样放行的 Error() 内容", st.Message())
	}
	if strings.Contains(st.Message(), "cause=") {
		t.Errorf("message 里漏出了 cause：%q", st.Message())
	}
}

func TestErrorMapperTranslatesWrappedTransportError(t *testing.T) {
	// 业务层常用 fmt.Errorf 给错误加上下文，包装之后仍应被正确翻译。
	wrapped := fmt.Errorf("查询用户失败: %w", transport.NotFound("USER_NOT_FOUND", "用户不存在"))

	_, err := ErrorMapper()(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) { return nil, wrapped })

	st, _ := status.FromError(err)
	if st.Code() != codes.NotFound {
		t.Errorf("code = %v, want NotFound", st.Code())
	}
	if st.Message() != "USER_NOT_FOUND: 用户不存在" {
		t.Errorf("message = %q", st.Message())
	}
}

func TestErrorMapperTranslatesJoinedTransportError(t *testing.T) {
	joined := errors.Join(errors.New("上下文"), transport.NotFound("USER_NOT_FOUND", "用户不存在"))

	_, err := ErrorMapper()(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) { return nil, joined })

	st, _ := status.FromError(err)
	if st.Code() != codes.NotFound {
		t.Errorf("code = %v, want NotFound", st.Code())
	}
}

func TestErrorMapperTreatsZeroCodeAsInternal(t *testing.T) {
	// 带 CodeOK 的错误若原样映射，status.Error(codes.OK, ...) 会返回 nil，
	// 错误被整个吞掉，客户端收到一个空的成功响应。
	zero := &transport.Error{Reason: "USER_NOT_FOUND", Message: "用户不存在"}

	_, err := ErrorMapper()(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) { return nil, zero })

	if err == nil {
		t.Fatal("错误被吞成了 nil，客户端会以为调用成功")
	}
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("code = %v, want Internal", got)
	}
}

func TestGRPCCodeFallsBackToInternal(t *testing.T) {
	if got := GRPCCode(9999); got != codes.Internal {
		t.Errorf("GRPCCode(9999) = %v, want Internal", got)
	}
	if got := GRPCCode(transport.CodeOK); got != codes.OK {
		t.Errorf("GRPCCode(CodeOK) = %v, want OK", got)
	}
}

func TestRequestLogRecordsErrorDetail(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	boom := errors.New("dial tcp 10.0.0.1:3306: connect: connection refused")
	_, err := RequestLog(logger)(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) { return nil, boom })
	if err == nil {
		t.Fatal("handler 的错误应当原样返回")
	}

	if !strings.Contains(buf.String(), "10.0.0.1") {
		t.Errorf("日志里没有错误详情，排障会断线；实际日志=%q", buf.String())
	}
}

func TestErrorMapperCarriesReasonAndMetadataAsDetail(t *testing.T) {
	src := transport.InvalidArgument("NAME_REQUIRED", "name 不能为空").
		WithMetadata(map[string]string{"field": "name"})

	_, err := ErrorMapper()(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) { return nil, src })

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("返回的不是 gRPC status: %v", err)
	}

	var info *errdetails.ErrorInfo
	for _, d := range st.Details() {
		if got, isInfo := d.(*errdetails.ErrorInfo); isInfo {
			info = got
			break
		}
	}
	if info == nil {
		t.Fatal("status 里没有 ErrorInfo detail，Metadata 过不去")
	}
	if info.GetReason() != "NAME_REQUIRED" {
		t.Errorf("detail 的 Reason = %q", info.GetReason())
	}
	if info.GetMetadata()["field"] != "name" {
		t.Errorf("detail 的 Metadata = %v", info.GetMetadata())
	}

	// 文本仍要保持老格式，好让不认 detail 的客户端也能看懂。
	if st.Message() != "NAME_REQUIRED: name 不能为空" {
		t.Errorf("message = %q", st.Message())
	}
}

func TestRequestLogOmitsErrorFieldOnSuccess(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	_, err := RequestLog(logger)(context.Background(), nil, unaryInfo(),
		func(context.Context, any) (any, error) { return "ok", nil })
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}

	var entry map[string]any
	if jsonErr := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); jsonErr != nil {
		t.Fatalf("日志不是合法 JSON: %v, 内容=%q", jsonErr, buf.String())
	}
	if _, has := entry["error"]; has {
		t.Errorf("成功的调用不该带 error 字段，实际日志=%v", entry)
	}
}
