package grpcclient

import (
	"context"
	"errors"
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
//
// 表外的 gRPC 码一律归为内部错误，其中有几个值得留意：codes.Canceled
// （调用方自己挂断）、codes.Unimplemented（方法不存在）与 codes.Aborted
// 都会被算成内部错误，因此以错误码为维度的监控会把它们和真正的服务端故障
// 混在一起。需要区分时，用 status.Code(errors.Unwrap(err)) 取回原始的 gRPC 码 ——
// ErrorRestorer 把它挂在 cause 上了。
func TransportCode(c codes.Code) int {
	if code, ok := transportByCode[c]; ok {
		return code
	}
	return transport.CodeInternal
}

// ErrorRestorer 把下游返回的 gRPC status 还原成 transport.Error。
//
// 用 status detail 里的 errdetails.ErrorInfo 是否存在来判断这条错误有没有
// 被脱敏过：带 detail 的，是 gokit 服务端的 ErrorMapper 处理过的业务错误，
// Reason、Metadata、文本都可以放心采用；不带 detail 的，要么是非 gokit 的
// 下游，要么是 gRPC 自己产生的传输层故障（建连失败、超时、取消），这些
// 从没经过脱敏，st.Message() 里常带着下游地址之类的内情，绝不能原样
// 塞进面向客户端的 Message ——这种情况一律换成一句安全的泛化描述，
// 原文只挂在 cause 上，供服务端日志排障。还原之后，调用方用 errors.Is
// 判断错误类型的写法在本地实现与远程实现下完全一致 —— 这正是模块从
// 单体拆成服务时调用方代码不用改的原因。
func ErrorRestorer() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any,
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err == nil {
			return nil
		}

		st := status.Convert(err)

		var (
			reason   string
			message  string
			metadata map[string]string
		)

		if info := errorInfoFrom(st); info != nil {
			// 下游是 gokit 服务：它的 ErrorMapper 已经在服务端脱过敏，
			// detail 是 Reason 与 Metadata 的权威来源，文本也可以放心采用。
			reason = info.GetReason()
			metadata = info.GetMetadata()
			_, message = splitReason(st.Message())
		} else {
			// 没有 detail。这条错误要么来自非 gokit 的下游，要么压根是 gRPC
			// 自己产生的传输层故障——建连失败、超时、取消。这几种情况下
			// st.Message() 从没经过任何脱敏，里头常带着下游地址之类的内情，
			// 而 Message 是直接发给客户端的字段。
			//
			// 所以按 transport.FromError 对未归类错误的一贯做法办：
			// 给一句泛化描述，原文留在 cause 上，由服务端的日志中间件记录。
			reason, message = fallbackReasonAndMessage(st.Code())
		}

		restored := transport.New(TransportCode(st.Code()), reason, message)
		if len(metadata) > 0 {
			restored = restored.WithMetadata(metadata)
		}
		return restored.WithCause(cause(st.Code(), err))
	}
}

// errorInfoFrom 取出 status 里的 ErrorInfo detail，没有则返回 nil。
func errorInfoFrom(st *status.Status) *errdetails.ErrorInfo {
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			return info
		}
	}
	return nil
}

// fallbackReasonAndMessage 给没有 detail 的错误一组安全的对外说法。
//
// 取消与超时给出自己的标识，是为了和本地实现对上——transport.FromError
// 对 context 的两个哨兵也给同样的 Reason 与 Code。其余一律归为一句泛化描述。
func fallbackReasonAndMessage(c codes.Code) (reason, message string) {
	switch c {
	case codes.Canceled:
		return "CANCELED", "调用已取消"
	case codes.DeadlineExceeded:
		return "DEADLINE_EXCEEDED", "处理超时"
	default:
		return "", "调用下游服务失败"
	}
}

// cause 决定挂在 transport.Error 上的底层原因。
//
// 取消与超时额外挂上 context 的哨兵，好让调用方
// errors.Is(err, context.Canceled) 这类在本地实现下成立的写法，
// 跨进程之后照样成立。
func cause(c codes.Code, err error) error {
	switch c {
	case codes.Canceled:
		return errors.Join(err, context.Canceled)
	case codes.DeadlineExceeded:
		return errors.Join(err, context.DeadlineExceeded)
	default:
		return err
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
