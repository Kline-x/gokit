// Package application 是问候模块的应用层：编排用例、划定事务边界，
// 并以接口形式对外暴露能力。
//
// 跨模块调用只允许依赖本包的 Service 接口。单体部署时注入 LocalService，
// 本模块独立成服务后换成 gRPC 客户端实现，调用方代码一行都不用改。
package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/Kline-x/gokit/example/minimal/internal/greeter/domain"
)

// GreetRequest 是 Greet 用例的入参。
type GreetRequest struct {
	Name string
}

// GreetReply 是 Greet 用例的出参。
type GreetReply struct {
	Text string
}

// Service 是问候模块对外的唯一契约。
type Service interface {
	Greet(ctx context.Context, req GreetRequest) (GreetReply, error)
}

// Transactor 是应用层对事务能力的抽象。
// 应用层借此划定事务边界，而不必知道底层是哪种数据库。
type Transactor interface {
	Tx(ctx context.Context, fn func(ctx context.Context) error) error
}

// LocalService 是 Service 的进程内实现。
type LocalService struct {
	repo domain.Repository
	tx   Transactor
}

// NewLocalService 构造进程内实现。
func NewLocalService(repo domain.Repository, tx Transactor) *LocalService {
	return &LocalService{repo: repo, tx: tx}
}

// Greet 返回某个名字的问候语，不存在时先生成再落库。
// 查询与写入包在同一个事务里，事务边界由应用层决定。
func (s *LocalService) Greet(ctx context.Context, req GreetRequest) (GreetReply, error) {
	if req.Name == "" {
		return GreetReply{}, errors.New("name 不能为空")
	}

	var reply GreetReply
	err := s.tx.Tx(ctx, func(ctx context.Context) error {
		g, err := s.repo.FindByName(ctx, req.Name)
		switch {
		case err == nil:
			reply = GreetReply{Text: g.Text}
			return nil
		case errors.Is(err, domain.ErrNotFound):
			g = domain.NewGreeting(req.Name)
			if err := s.repo.Save(ctx, g); err != nil {
				return fmt.Errorf("保存问候语失败: %w", err)
			}
			reply = GreetReply{Text: g.Text}
			return nil
		default:
			return fmt.Errorf("查询问候语失败: %w", err)
		}
	})
	return reply, err
}
