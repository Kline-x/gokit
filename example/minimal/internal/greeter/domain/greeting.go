// Package domain 是问候模块的领域层。
//
// 这一层只描述业务概念，除标准库外不 import 任何包，
// 也不感知数据库、HTTP 等任何技术细节。
package domain

import (
	"context"
	"errors"
)

// ErrNotFound 表示指定名字还没有对应的问候语。
var ErrNotFound = errors.New("问候语不存在")

// Greeting 是一条问候语。
type Greeting struct {
	Name string
	Text string
}

// NewGreeting 按业务规则生成一条问候语。
func NewGreeting(name string) Greeting {
	return Greeting{Name: name, Text: "你好，" + name}
}

// Repository 是问候语的仓储契约，实现放在 infrastructure 层。
type Repository interface {
	Save(ctx context.Context, g Greeting) error
	FindByName(ctx context.Context, name string) (Greeting, error)
}
