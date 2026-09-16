package grpcserver

import "github.com/google/wire"

// ProviderSet 供 wire 装配 gRPC 服务组件。
// 使用方需自行提供 Config 与 []ServiceRegistrar。
var ProviderSet = wire.NewSet(New)
