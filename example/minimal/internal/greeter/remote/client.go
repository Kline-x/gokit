// Package remote 是问候模块的出站适配器：用 gRPC 调用同名的远程服务。
//
// 它与 infrastructure 并列——两者都是出站适配器，区别在于 infrastructure
// 连的是本进程管得着的存储，remote 连的是另一个进程。
// 模块真正拆出去时，infrastructure 跟着服务走，remote 留在调用方这边。
//
// 它实现的是 application.Service，与 application.LocalService 同一个接口。
// 调用方拿到的是接口，因此换实现这件事对它不可见。
package remote

import (
	"context"

	"github.com/Kline-x/gokit/component/grpcclient"
	greeterv1 "github.com/Kline-x/gokit/example/minimal/api/greeter/v1"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/application"
	"github.com/Kline-x/gokit/transport"
)

// Service 通过 gRPC 调用远程的问候服务。
type Service struct {
	conn *grpcclient.Client
}

// NewService 构造远程实现。
//
// 这里只存下连接组件本身，不提前取它的 Conn：wire 的装配发生在 App.Start
// 之前，那时连接还没建立，Conn() 返回 nil，拿它造出来的桩一调用就会 panic。
func NewService(c *grpcclient.Client) *Service {
	return &Service{conn: c}
}

// Greet 实现 application.Service。
//
// 只做两件事：把应用层的入参翻成 proto、把 proto 的出参翻回来。
// 错误原样返回——grpcclient 的拦截器已经把 status 还原成 transport.Error，
// 所以调用方用 errors.Is 判断错误的写法与本地实现下完全一致。
//
// 每次调用都现取连接、现造桩。造桩很便宜，它只是把 conn 包一层，没有握手。
func (s *Service) Greet(ctx context.Context, req application.GreetRequest) (application.GreetReply, error) {
	conn := s.conn.Conn()
	if conn == nil {
		return application.GreetReply{}, transport.Unavailable(
			"GREETER_NOT_CONNECTED", "问候服务尚未建立连接")
	}

	reply, err := greeterv1.NewGreeterClient(conn).Greet(ctx, &greeterv1.GreetRequest{Name: req.Name})
	if err != nil {
		return application.GreetReply{}, err
	}
	return application.GreetReply{Text: reply.GetText()}, nil
}
