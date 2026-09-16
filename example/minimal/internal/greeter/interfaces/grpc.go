package interfaces

import (
	"context"

	"google.golang.org/grpc"

	greeterv1 "github.com/Kline-x/gokit/example/minimal/api/greeter/v1"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/application"
)

// GRPCHandler 把问候用例暴露成 gRPC 接口。
//
// 它和 HTTPHandler 是并列的两个接口层实现，委托的是同一个
// application.Service —— 业务逻辑只有一份，协议有两种。
type GRPCHandler struct {
	greeterv1.UnimplementedGreeterServer
	svc application.Service
}

// NewGRPCHandler 构造 gRPC 接口层处理器。
func NewGRPCHandler(svc application.Service) *GRPCHandler {
	return &GRPCHandler{svc: svc}
}

// Register 实现 grpcserver.ServiceRegistrar，把本服务挂到 gRPC 服务器上。
func (h *GRPCHandler) Register(s *grpc.Server) {
	greeterv1.RegisterGreeterServer(s, h)
}

// Greet 实现 greeterv1.GreeterServer，只做协议转换。
// 业务错误原样返回，由服务端拦截器翻译成 gRPC status。
func (h *GRPCHandler) Greet(ctx context.Context, req *greeterv1.GreetRequest) (*greeterv1.GreetReply, error) {
	reply, err := h.svc.Greet(ctx, application.GreetRequest{Name: req.GetName()})
	if err != nil {
		return nil, err
	}
	return &greeterv1.GreetReply{Text: reply.Text}, nil
}
