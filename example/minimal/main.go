// Command minimal 是 gokit 的最小可用单体示例：
// 一个分层的问候模块，通过 HTTP 暴露，数据落在 SQLite。
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	_ "modernc.org/sqlite" // 注册 sqlite 驱动，框架本身不绑定任何驱动

	"github.com/Kline-x/gokit/app"
	"github.com/Kline-x/gokit/component/httpserver"
	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/component/sqldb"
	"github.com/Kline-x/gokit/config"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/infrastructure"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/interfaces"
)

// Config 是本应用的聚合配置，按组件名挂子节点。
type Config struct {
	Log  log.Config        `yaml:"log"`
	HTTP httpserver.Config `yaml:"http"`
	DB   sqldb.Config      `yaml:"db"`
}

func defaultConfig() Config {
	dbCfg := sqldb.DefaultConfig()
	dbCfg.Driver = "sqlite"
	dbCfg.DSN = "file:minimal.db"

	return Config{
		Log:  log.DefaultConfig(),
		HTTP: httpserver.DefaultConfig(),
		DB:   dbCfg,
	}
}

// Bundle 汇总一次装配产出的全部对象，由 wire 填充。
type Bundle struct {
	App    *app.App
	Logger *log.Logger
	DB     *sqldb.DB
	HTTP   *httpserver.Server
}

// Register 把各组件按启动顺序注册进 App。
func (b *Bundle) Register() *app.App {
	b.App.Register(b.Logger, b.DB, b.HTTP)
	return b.App
}

func provideLogConfig(cfg Config) log.Config         { return cfg.Log }
func provideHTTPConfig(cfg Config) httpserver.Config { return cfg.HTTP }
func provideDBConfig(cfg Config) sqldb.Config        { return cfg.DB }

func provideApp(logger *log.Logger) *app.App {
	return app.New(app.WithName("minimal"), app.WithLogger(logger.Logger))
}

// provideHandler 组装路由与中间件。各业务模块在这里挂自己的路由。
func provideHandler(logger *log.Logger, greeter *interfaces.HTTPHandler) http.Handler {
	mux := http.NewServeMux()
	greeter.Register(mux)
	return httpserver.Chain(mux,
		httpserver.RequestLog(logger.Logger),
		httpserver.Recover(logger.Logger),
	)
}

// provideHTTPServer 把 App.Fatal 接给服务，让运行期异常触发整体优雅退出。
func provideHTTPServer(cfg httpserver.Config, h http.Handler, a *app.App) *httpserver.Server {
	return httpserver.New(cfg, h, httpserver.WithFatal(a.Fatal))
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.yaml", "配置文件路径")
	flag.Parse()

	cfg := defaultConfig()
	loader := config.New(
		config.WithOptionalFile(configPath),
		config.WithEnvPrefix("MINIMAL"),
	)
	if err := loader.Load(&cfg); err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}

	b, err := initApp(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "装配失败:", err)
		os.Exit(1)
	}

	ctx := context.Background()
	if err := infrastructure.Migrate(ctx, b.DB); err != nil {
		fmt.Fprintln(os.Stderr, "初始化数据库失败:", err)
		os.Exit(1)
	}

	if err := b.Register().Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "运行失败:", err)
		os.Exit(1)
	}
}
