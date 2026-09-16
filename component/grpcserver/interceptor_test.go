package grpcserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

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

func TestGRPCCodeFallsBackToInternal(t *testing.T) {
	if got := GRPCCode(9999); got != codes.Internal {
		t.Errorf("GRPCCode(9999) = %v, want Internal", got)
	}
	if got := GRPCCode(transport.CodeOK); got != codes.OK {
		t.Errorf("GRPCCode(CodeOK) = %v, want OK", got)
	}
}
