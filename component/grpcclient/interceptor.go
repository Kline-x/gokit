package grpcclient

import (
	"context"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Kline-x/gokit/transport"
)

// transportByCode 是服务端映射表的逆向。两边必须对称，
// 否则同一个错误跨进程一个来回之后错误码会漂移。
var transportByCode = map[codes.Code]int{
	codes.OK:                 transport.CodeOK,
	codes.InvalidArgument:    transport.CodeInvalidArgument,
	codes.Unauthenticated:    transport.CodeUnauthenticated,
	codes.PermissionDenied:   transport.CodePermissionDenied,
	codes.NotFound:           transport.CodeNotFound,
	codes.AlreadyExists:      transport.CodeAlreadyExists,
	codes.FailedPrecondition: transport.CodeFailedPrecondition,
	codes.ResourceExhausted:  transport.CodeRateLimited,
	codes.Internal:           transport.CodeInternal,
	codes.Unavailable:        transport.CodeUnavailable,
	codes.DeadlineExceeded:   transport.CodeTimeout,
}

// TransportCode 返回 gRPC status code 对应的框架错误码。
// 表外的取值一律按内部错误处理。
func TransportCode(c codes.Code) int {
	if code, ok := transportByCode[c]; ok {
		return code
	}
	return transport.CodeInternal
}

// ErrorRestorer 把下游返回的 gRPC status 还原成 transport.Error。
//
// 优先用 status detail 里的 errdetails.ErrorInfo 取 Reason 与 Metadata；
// 没有 detail（例如下游不是 gokit 的服务）时，退回按 "REASON: 描述"
// 拆 message 的老办法，拆不出来时整段当描述，Reason 留空。还原之后，
// 调用方用 errors.Is 判断错误类型的写法在本地实现与远程实现下完全一致 ——
// 这正是模块从单体拆成服务时调用方代码不用改的原因。
func ErrorRestorer() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err == nil {
			return nil
		}

		st := status.Convert(err)
		reason, message := splitReason(st.Message())
		var metadata map[string]string

		// detail 是权威来源：只要下游带了 ErrorInfo，就用它的 Reason 与 Metadata，
		// 不必再去猜测文本里的冒号。拆文本只是对不带 detail 的下游的兜底。
		for _, d := range st.Details() {
			info, ok := d.(*errdetails.ErrorInfo)
			if !ok {
				continue
			}
			if info.GetReason() != "" {
				reason = info.GetReason()
			}
			if len(info.GetMetadata()) > 0 {
				metadata = info.GetMetadata()
			}
			break
		}

		restored := transport.New(TransportCode(st.Code()), reason, message)
		if metadata != nil {
			restored = restored.WithMetadata(metadata)
		}
		return restored.WithCause(err)
	}
}

// looksLikeReason 判断一段文本像不像机器可读的 Reason。
//
// 按约定 Reason 由大写字母、数字与下划线组成，例如 USER_NOT_FOUND。
// 之所以要这道闸：gRPC 自己产生的错误文本里也常带冒号，
// 比如建连失败时的「last connection error: connection refused」。
// 不加限制地按第一个冒号拆，就会把「last connection error」当成 Reason
// 交给调用方去分支判断，而那根本不是业务语义。
func looksLikeReason(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// splitReason 从 "REASON: 描述" 中拆出两段。
// 拆不出、或前半段不像 Reason 时，整段都当描述，Reason 留空。
func splitReason(msg string) (reason, message string) {
	before, after, found := strings.Cut(msg, ": ")
	if !found || !looksLikeReason(before) {
		return "", msg
	}
	return before, after
}
