package sqldb

import "github.com/google/wire"

// ProviderSet 供 wire 装配关系库组件。使用方需自行提供 Config。
var ProviderSet = wire.NewSet(New)
