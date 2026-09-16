package grpcserver

import (
	"context"
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

		// status.FromError 只对 nil、*status.Error，以及实现了 GRPCStatus()
		// 的类型返回 true。transport.Error 不实现它，普通 error 也不实现，
		// 所以这一句足以把「已经是 status」的错误挑出来原样放行。
		if _, ok := status.FromError(err); ok {
			return resp, err
		}

		e := transport.FromError(err)
		return resp, status.Error(GRPCCode(e.Code), fmt.Sprintf("%s: %s", e.Reason, e.Message))
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
func RequestLog(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		begin := time.Now()
		ctx = log.NewContext(ctx, logger)

		resp, err := handler(ctx, req)

		logger.InfoContext(ctx, "grpc 调用",
			slog.String("method", info.FullMethod),
			slog.String("code", status.Code(err).String()),
			slog.Duration("latency", time.Since(begin)),
		)
		return resp, err
	}
}
