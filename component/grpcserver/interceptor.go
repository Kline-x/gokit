package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/transport"
)

// codeByTransport 把框架错误码映射到 gRPC status code。
//
// 两套码没有一一对应关系，这张表是唯一的翻译依据；
// 客户端侧的反向翻译在 component/grpcclient 里，两边必须对称。
var codeByTransport = map[int]codes.Code{
	transport.CodeOK:                 codes.OK,
	transport.CodeInvalidArgument:    codes.InvalidArgument,
	transport.CodeUnauthenticated:    codes.Unauthenticated,
	transport.CodePermissionDenied:   codes.PermissionDenied,
	transport.CodeNotFound:           codes.NotFound,
	transport.CodeAlreadyExists:      codes.AlreadyExists,
	transport.CodeFailedPrecondition: codes.FailedPrecondition,
	transport.CodeRateLimited:        codes.ResourceExhausted,
	transport.CodeInternal:           codes.Internal,
	transport.CodeUnavailable:        codes.Unavailable,
	transport.CodeTimeout:            codes.DeadlineExceeded,
}

// GRPCCode 返回框架错误码对应的 gRPC status code。表外的取值一律按 Internal 处理。
func GRPCCode(code int) codes.Code {
	if c, ok := codeByTransport[code]; ok {
		return c
	}
	return codes.Internal
}

// ErrorMapper 把业务返回的 transport.Error 翻译成 gRPC status。
//
// status 的 message 里带上 Reason，形如 "USER_NOT_FOUND: 用户不存在"，
// 这样客户端侧的拦截器能把它还原回 transport.Error，跨进程后语义不丢。
// 已经是 gRPC status 的错误原样放行，不做二次包装。
func ErrorMapper() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}

		// 先问「是不是框架错误」，再问「是不是已经成型的 status」。顺序不能反：
		// grpc 的 status.FromError 会用 errors.As 匹配任何「包装了 status」的错误，
		// 而中转下游调用时，grpcclient 还原出来的 transport.Error 正好把原始 status
		// 挂在 cause 上。若先问 status.FromError，这类错误会被原样放行，
		// 于是 Reason 丢失、Error() 的完整内容（连同 cause）被发给外部客户端。
		var te *transport.Error
		if !errors.As(err, &te) {
			// 不是框架错误：已经是 status 的原样放行，其余归一成内部错误。
			if _, ok := status.FromError(err); ok {
				return resp, err
			}
			te = transport.FromError(err)
		}

		return resp, status.Error(
			GRPCCode(te.StatusCode()),
			fmt.Sprintf("%s: %s", te.Reason, te.Message),
		)
	}
}

// Recover 捕获处理链中的 panic，记录堆栈并返回 Internal，避免整个进程崩溃。
func Recover(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			logger.ErrorContext(ctx, "grpc 处理过程中发生 panic",
				slog.Any("panic", rec),
				slog.String("method", info.FullMethod),
				slog.String("stack", string(debug.Stack())),
			)
			err = status.Error(codes.Internal, "内部错误")
		}()
		return handler(ctx, req)
	}
}

// RequestLog 记录每次调用的方法、状态码与耗时，
// 同时把 logger 放进 ctx，供业务代码用 log.FromContext 取用。
//
// 顺序要求：本拦截器必须排在 ErrorMapper 之外（更靠外的一层）。
// 它靠 status.Code(err) 取状态码，而业务错误要经 ErrorMapper 翻译之后才是 status；
// 若排在里面，日志会把业务错误一律记成 Unknown，而客户端收到的状态码却是对的 ——
// 这种不一致最难排查。推荐顺序：RequestLog、Recover、ErrorMapper。
func RequestLog(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		begin := time.Now()
		ctx = log.NewContext(ctx, logger)

		resp, err := handler(ctx, req)

		attrs := []any{
			slog.String("method", info.FullMethod),
			slog.String("code", status.Code(err).String()),
			slog.Duration("latency", time.Since(begin)),
		}
		if err != nil {
			// 这里记的是未经翻译的原始错误，包含 transport.Error 挂在 cause 上的
			// 底层原因。客户端只会收到泛化描述，排障要靠这一行。
			attrs = append(attrs, slog.Any("error", err))
		}
		logger.InfoContext(ctx, "grpc 调用", attrs...)
		return resp, err
	}
}
