// Command gateway 是把问候能力以 HTTP 暴露给用户的入口服务。
//
// 它自己不实现任何业务：问候模块已经拆到 greeter 服务上了，
// 这里装的是 greeter.RemoteSet，走 gRPC 过去。
// 注意它没有数据库——存储跟着模块一起搬走了。
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/Kline-x/gokit/app"
	"github.com/Kline-x/gokit/component/grpcclient"
	"github.com/Kline-x/gokit/component/httpserver"
	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/config"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/interfaces"
)

// Config 是本服务的聚合配置。
type Config struct {
	Log     log.Config        `yaml:"log"`
	HTTP    httpserver.Config `yaml:"http"`
	Greeter grpcclient.Config `yaml:"greeter"`
}

func defaultConfig() Config {
	greeterCfg := grpcclient.DefaultConfig()
	greeterCfg.Name = "grpcclient.greeter"
	greeterCfg.Target = "127.0.0.1:9000"

	return Config{
		Log:     log.DefaultConfig(),
		HTTP:    httpserver.DefaultConfig(),
		Greeter: greeterCfg,
	}
}

// Bundle 汇总一次装配产出的对象。
type Bundle struct {
	App        *app.App
	HTTP       *httpserver.Server
	Components []app.Component
}

// Register 把组件交给 App。
func (b *Bundle) Register() *app.App {
	b.App.Register(b.Components...)
	return b.App
}

func provideLogConfig(cfg Config) log.Config            { return cfg.Log }
func provideHTTPConfig(cfg Config) httpserver.Config    { return cfg.HTTP }
func provideGreeterConfig(cfg Config) grpcclient.Config { return cfg.Greeter }

func provideApp(logger *log.Logger) *app.App {
	return app.New(app.WithName("gateway"), app.WithLogger(logger.Logger))
}

// provideGreeterClient 构造到问候服务的连接。
//
// 装上 ErrorRestorer：它把下游返回的 status 还原成 transport.Error，
// 因此调用方用 errors.Is 判断错误的写法与本地实现下完全一致。
func provideGreeterClient(cfg grpcclient.Config) (*grpcclient.Client, error) {
	return grpcclient.New(cfg,
		grpcclient.WithUnaryInterceptor(grpcclient.ErrorRestorer()),
	)
}

// provideHandler 组装路由与中间件。RequestLog 必须在最外层。
func provideHandler(logger *log.Logger, greeter *interfaces.HTTPHandler) http.Handler {
	mux := http.NewServeMux()
	greeter.Register(mux)
	return httpserver.Chain(mux,
		httpserver.RequestLog(logger.Logger),
		httpserver.Recover(logger.Logger),
	)
}

func provideHTTPServer(cfg httpserver.Config, h http.Handler, a *app.App) *httpserver.Server {
	return httpserver.New(cfg, h, httpserver.WithFatal(a.Fatal))
}

// provideComponents 列出本次装配要交给 App 托管的组件。
//
// 与单体相比这里少了数据库与建表，多了一条到下游的连接——
// 拆分带来的差异全部集中在这一处。
func provideComponents(
	logger *log.Logger,
	greeterConn *grpcclient.Client,
	httpSrv *httpserver.Server,
) []app.Component {
	return []app.Component{logger, greeterConn, httpSrv}
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "configs/gateway.yaml", "配置文件路径")
	flag.Parse()

	cfg := defaultConfig()
	loader := config.New(
		config.WithOptionalFile(configPath),
		config.WithEnvPrefix("GATEWAY"),
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

	if err := b.Register().Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "运行失败:", err)
		os.Exit(1)
	}
}
