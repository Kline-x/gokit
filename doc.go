// Package gokit 是一个分层清晰、基础设施组件化的 Go 应用框架。
//
// 内核见 app 包：统一的 Component 生命周期接口与 App 编排器。
// 基础设施见 component 下各子包，彼此独立，按需引用。
// 与协议无关的错误类型及其到 HTTP/gRPC 的映射见 transport 包。
package gokit
