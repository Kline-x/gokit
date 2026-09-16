package grpcclient

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Kline-x/gokit/component/grpcserver"
	"github.com/Kline-x/gokit/transport"
)

// startTestServer 起一个只提供健康检查的 gRPC 服务，返回它的地址。
func startTestServer(t *testing.T) string {
	t.Helper()
	s := grpcserver.New(grpcserver.Config{Name: "grpcserver.test", Addr: "127.0.0.1:0"}, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("起测试服务失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s.Addr().String()
}

func TestClientConnectsAndReportsHealthy(t *testing.T) {
	addr := startTestServer(t)

	c, err := New(Config{Name: "grpcclient.test", Target: addr})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Stop(ctx) })

	if c.Conn() == nil {
		t.Fatal("Conn() = nil，Start 之后应当可用")
	}
	if err := c.Health(ctx); err != nil {
		t.Errorf("Health() error = %v", err)
	}
}

// TestClientHealthFailsAfterStop 验证 Stop 之后连接已真正关闭：
// 此时不是服务端消失，而是客户端自己主动停止了连接。
func TestClientHealthFailsAfterStop(t *testing.T) {
	addr := startTestServer(t)

	c, err := New(Config{Name: "grpcclient.test", Target: addr})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if err := c.Health(ctx); err == nil {
		t.Error("Stop 之后 Health 仍然成功，连接未真正关闭")
	}
}

func TestNameComesFromConfig(t *testing.T) {
	c, err := New(Config{Name: "grpcclient.user", Target: "127.0.0.1:1"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })

	if c.Name() != "grpcclient.user" {
		t.Errorf("Name() = %q, want %q", c.Name(), "grpcclient.user")
	}

	d, err := New(Config{Target: "127.0.0.1:1"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })
	if d.Name() != "grpcclient" {
		t.Errorf("Name() = %q, want 默认值 %q", d.Name(), "grpcclient")
	}
}

func TestNewRejectsEmptyTarget(t *testing.T) {
	if _, err := New(Config{Name: "grpcclient.test"}); err == nil {
		t.Fatal("New() error = nil, want 目标地址为空的错误")
	}
}

func TestErrorRestorerRebuildsTransportError(t *testing.T) {
	// 真实的 gokit 服务端（ErrorMapper）总是带上 ErrorInfo detail，
	// 这里照实模拟，验证 Code/Reason/Message 三者都能还原正确。
	st, detailErr := status.New(codes.NotFound, "USER_NOT_FOUND: 用户不存在").
		WithDetails(&errdetails.ErrorInfo{Reason: "USER_NOT_FOUND", Domain: "gokit"})
	if detailErr != nil {
		t.Fatalf("构造 detail 失败: %v", detailErr)
	}

	restorer := ErrorRestorer()
	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return st.Err()
	}

	err := restorer(context.Background(), "/greeter.v1.Greeter/Greet", nil, nil, nil, invoker)

	var te *transport.Error
	if !errors.As(err, &te) {
		t.Fatalf("未能还原成 *transport.Error: %v", err)
	}
	if te.Code != transport.CodeNotFound {
		t.Errorf("Code = %d, want %d", te.Code, transport.CodeNotFound)
	}
	if te.Reason != "USER_NOT_FOUND" {
		t.Errorf("Reason = %q, want %q", te.Reason, "USER_NOT_FOUND")
	}
	if te.Message != "用户不存在" {
		t.Errorf("Message = %q, want %q", te.Message, "用户不存在")
	}
}

func TestErrorRestorerHandlesMessageWithoutReason(t *testing.T) {
	// 没有 detail：不是 gokit 服务端产生的错误，Message 必须用安全的
	// 泛化描述，不能采用下游原文；Code 的翻译不受影响。
	restorer := ErrorRestorer()

	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return status.Error(codes.Unavailable, "连接被拒绝")
	}

	err := restorer(context.Background(), "/x/y", nil, nil, nil, invoker)

	var te *transport.Error
	if !errors.As(err, &te) {
		t.Fatalf("未能还原成 *transport.Error: %v", err)
	}
	if te.Code != transport.CodeUnavailable {
		t.Errorf("Code = %d, want %d", te.Code, transport.CodeUnavailable)
	}
	if te.Reason != "" {
		t.Errorf("Reason = %q，没有 detail 时不该有业务 Reason", te.Reason)
	}
	if te.Message == "连接被拒绝" {
		t.Errorf("Message = %q，不带 detail 的原文不该原样对外", te.Message)
	}
	if !strings.Contains(err.Error(), "连接被拒绝") {
		t.Error("原始错误应当仍挂在 cause 上，供排障使用")
	}
}

func TestErrorRestorerLeavesSuccessAlone(t *testing.T) {
	restorer := ErrorRestorer()
	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return nil
	}
	if err := restorer(context.Background(), "/x/y", nil, nil, nil, invoker); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestTransportCodeIsInverseOfGRPCCode(t *testing.T) {
	// 两侧的映射必须对称，否则跨进程后错误码会漂移。
	codesToCheck := []int{
		transport.CodeInvalidArgument,
		transport.CodeUnauthenticated,
		transport.CodePermissionDenied,
		transport.CodeNotFound,
		transport.CodeAlreadyExists,
		transport.CodeFailedPrecondition,
		transport.CodeRateLimited,
		transport.CodeInternal,
		transport.CodeUnavailable,
		transport.CodeTimeout,
	}

	for _, code := range codesToCheck {
		grpcCode := grpcserver.GRPCCode(code)
		if got := TransportCode(grpcCode); got != code {
			t.Errorf("往返不一致: transport %d -> grpc %v -> transport %d", code, grpcCode, got)
		}
	}
}

func TestDialTimeoutIsRespected(t *testing.T) {
	// 连一个不存在的地址，Start 应当在超时内返回错误而不是一直卡着。
	c, err := New(Config{Name: "grpcclient.test", Target: "127.0.0.1:1", Block: true, DialTimeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })

	begin := time.Now()
	startErr := c.Start(context.Background())
	elapsed := time.Since(begin)

	if startErr == nil {
		t.Fatal("Start() error = nil, want 建连超时错误")
	}
	if elapsed > 3*time.Second {
		t.Errorf("Start() 耗时 %v，应当在 DialTimeout 附近返回", elapsed)
	}
}

func TestSplitReasonIgnoresGRPCProseWithColons(t *testing.T) {
	// gRPC 自己的错误文本里带冒号是常态，不能把前半段当成业务 Reason。
	cases := []struct {
		name        string
		msg         string
		wantReason  string
		wantMessage string
	}{
		{
			name:        "grpc 建连失败的文本",
			msg:         "last connection error: connection refused",
			wantReason:  "",
			wantMessage: "last connection error: connection refused",
		},
		{
			name:        "服务端产生的业务错误",
			msg:         "USER_NOT_FOUND: 用户不存在",
			wantReason:  "USER_NOT_FOUND",
			wantMessage: "用户不存在",
		},
		{
			name:        "小写前缀不算 Reason",
			msg:         "something went wrong: 详情",
			wantReason:  "",
			wantMessage: "something went wrong: 详情",
		},
		{
			name:        "没有分隔符",
			msg:         "连接被拒绝",
			wantReason:  "",
			wantMessage: "连接被拒绝",
		},
		{
			name:        "带数字与下划线的 Reason",
			msg:         "ERR_42_BAD_STATE: 状态不对",
			wantReason:  "ERR_42_BAD_STATE",
			wantMessage: "状态不对",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, message := splitReason(tc.msg)
			if reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", reason, tc.wantReason)
			}
			if message != tc.wantMessage {
				t.Errorf("message = %q, want %q", message, tc.wantMessage)
			}
		})
	}
}

func TestErrorRestorerPrefersDetailOverMessageSplitting(t *testing.T) {
	st, detailErr := status.New(codes.InvalidArgument, "随便一段不含冒号的文本").
		WithDetails(&errdetails.ErrorInfo{
			Reason:   "NAME_REQUIRED",
			Domain:   "gokit",
			Metadata: map[string]string{"field": "name"},
		})
	if detailErr != nil {
		t.Fatalf("构造 detail 失败: %v", detailErr)
	}

	restorer := ErrorRestorer()
	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return st.Err()
	}

	err := restorer(context.Background(), "/x/y", nil, nil, nil, invoker)

	var te *transport.Error
	if !errors.As(err, &te) {
		t.Fatalf("未能还原成 *transport.Error: %v", err)
	}
	if te.Reason != "NAME_REQUIRED" {
		t.Errorf("Reason = %q，应当取自 detail 而不是拆文本", te.Reason)
	}
	if te.Metadata["field"] != "name" {
		t.Errorf("Metadata = %v，detail 里的结构化信息应当还原出来", te.Metadata)
	}
}

func TestErrorRestorerFallsBackToMessageWhenNoDetail(t *testing.T) {
	// 没有 detail 意味着这条错误不是经过 gokit ErrorMapper 脱敏的，
	// 哪怕文本看起来像 "REASON: 描述"，也不能再当成业务 Reason 采信——
	// 那正是漏洞所在：一条精心构造的下游文本能借着这条路径把 Reason
	// 伪造出来。没有 detail 就该没有 Reason、没有 Metadata。
	restorer := ErrorRestorer()
	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return status.Error(codes.NotFound, "USER_NOT_FOUND: 用户不存在")
	}

	err := restorer(context.Background(), "/x/y", nil, nil, nil, invoker)

	var te *transport.Error
	if !errors.As(err, &te) {
		t.Fatalf("未能还原成 *transport.Error: %v", err)
	}
	if te.Reason != "" {
		t.Errorf("Reason = %q，没有 detail 时不该有业务 Reason", te.Reason)
	}
	if len(te.Metadata) != 0 {
		t.Errorf("Metadata = %v，没有 detail 时应当为空", te.Metadata)
	}
}

func TestErrorRestorerDoesNotFabricateReasonFromGRPCProse(t *testing.T) {
	restorer := ErrorRestorer()

	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return status.Error(codes.Unavailable, "last connection error: connection refused")
	}

	err := restorer(context.Background(), "/x/y", nil, nil, nil, invoker)

	var te *transport.Error
	if !errors.As(err, &te) {
		t.Fatalf("未能还原成 *transport.Error: %v", err)
	}
	if te.Reason != "" {
		t.Errorf("Reason = %q，gRPC 自己的错误文本不该被当成业务 Reason", te.Reason)
	}
	// 没有 ErrorInfo detail：这条错误没有经过任何脱敏，Message 不能采用原文，
	// 应当换成一句泛化描述，原文只挂在 cause 上。
	if te.Message == "last connection error: connection refused" {
		t.Errorf("Message = %q，不带 detail 的原文不该原样对外", te.Message)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Error("原始错误应当仍挂在 cause 上，供排障使用")
	}
}

func TestErrorRestorerDoesNotLeakUnsanitisedStatusText(t *testing.T) {
	// gRPC 自己产生的传输层故障没经过任何脱敏，文本里常带下游地址。
	// Message 是直接发给客户端的字段，不能采用这段原文。
	restorer := ErrorRestorer()
	raw := `connection error: desc = "transport: Error while dialing: dial tcp 10.1.2.3:9000: connect: connection refused"`

	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return status.Error(codes.Unavailable, raw)
	}

	err := restorer(context.Background(), "/x/y", nil, nil, nil, invoker)

	var te *transport.Error
	if !errors.As(err, &te) {
		t.Fatalf("未能还原成 *transport.Error: %v", err)
	}
	for _, leak := range []string{"10.1.2.3", "dial tcp", "transport: Error"} {
		if strings.Contains(te.Message, leak) {
			t.Errorf("Message 里漏出了 %q: %q", leak, te.Message)
		}
	}
	// 原文不能丢，它要进服务端日志。
	if !strings.Contains(err.Error(), "10.1.2.3") {
		t.Error("原始错误应当仍挂在 cause 上，供排障使用")
	}
}

func TestErrorRestorerCarriesContextSentinels(t *testing.T) {
	cases := []struct {
		name     string
		code     codes.Code
		sentinel error
		reason   string
	}{
		{"canceled", codes.Canceled, context.Canceled, "CANCELED"},
		{"deadline", codes.DeadlineExceeded, context.DeadlineExceeded, "DEADLINE_EXCEEDED"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restorer := ErrorRestorer()
			invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				return status.Error(tc.code, "随便一段下游文本")
			}

			err := restorer(context.Background(), "/x/y", nil, nil, nil, invoker)

			if !errors.Is(err, tc.sentinel) {
				t.Errorf("errors.Is 认不出 %v：%v", tc.sentinel, err)
			}
			var te *transport.Error
			if !errors.As(err, &te) {
				t.Fatalf("未能还原成 *transport.Error: %v", err)
			}
			if te.Reason != tc.reason {
				t.Errorf("Reason = %q, want %q", te.Reason, tc.reason)
			}
		})
	}
}
