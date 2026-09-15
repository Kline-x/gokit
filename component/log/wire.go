package log

import "github.com/google/wire"

// ProviderSet 供 wire 装配日志组件。
// 使用方需自行提供 Config，并把 *Logger 注册进 app.App。
var ProviderSet = wire.NewSet(New)
