// Command greeter 是拆分出去之后的问候服务。
//
// 它只装这个模块自己需要的东西：日志、数据库、建表、gRPC 服务。
// 没有 HTTP —— 对外只提供 gRPC 契约，谁要用就按 api/greeter/v1 生成客户端。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	_ "modernc.org/sqlite" // 注册 sqlite 驱动，框架本身不绑定任何驱动

	"github.com/Kline-x/gokit/app"
	"github.com/Kline-x/gokit/component/grpcserver"
	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/component/sqldb"
	"github.com/Kline-x/gokit/config"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/infrastructure"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/interfaces"
)

// Config 是本服务的聚合配置。
type Config struct {
	Log  log.Config        `yaml:"log"`
	GRPC grpcserver.Config `yaml:"grpc"`
	DB   sqldb.Config      `yaml:"db"`
}

func defaultConfig() Config {
	dbCfg := sqldb.DefaultConfig()
	dbCfg.Driver = "sqlite"
	dbCfg.DSN = "file:greeter.db"

	grpcCfg := grpcserver.DefaultConfig()
	grpcCfg.Addr = ":9000"

	return Config{
		Log:  log.DefaultConfig(),
		GRPC: grpcCfg,
		DB:   dbCfg,
	}
}

// Bundle 汇总一次装配产出的对象。
type Bundle struct {
	App        *app.App
	GRPC       *grpcserver.Server
	Components []app.Component
}

// Register 把组件交给 App。先后由各组件的 DependsOn 决定。
func (b *Bundle) Register() *app.App {
	b.App.Register(b.Components...)
	return b.App
}

func provideLogConfig(cfg Config) log.Config         { return cfg.Log }
func provideGRPCConfig(cfg Config) grpcserver.Config { return cfg.GRPC }
func provideDBConfig(cfg Config) sqldb.Config        { return cfg.DB }

func provideApp(logger *log.Logger) *app.App {
	return app.New(app.WithName("greeter"), app.WithLogger(logger.Logger))
}

// provideServiceRegistrars 列出要挂到 gRPC 服务器上的服务。
func provideServiceRegistrars(h *interfaces.GRPCHandler) []grpcserver.ServiceRegistrar {
	return []grpcserver.ServiceRegistrar{h}
}

// provideGRPCServer 只传 RequestLog：Recover 与 ErrorMapper 由 grpcserver
// 默认装在最内层，因此这里传进去的天然包在它们外面。
func provideGRPCServer(
	cfg grpcserver.Config,
	services []grpcserver.ServiceRegistrar,
	logger *log.Logger,
	a *app.App,
) *grpcserver.Server {
	return grpcserver.New(cfg, services,
		grpcserver.WithFatal(a.Fatal),
		grpcserver.WithLogger(logger.Logger),
		grpcserver.WithUnaryInterceptor(grpcserver.RequestLog(logger.Logger)),
	)
}

// provideComponents 列出本次装配要交给 App 托管的组件。
func provideComponents(
	logger *log.Logger,
	db *sqldb.DB,
	migrator *infrastructure.Migrator,
	grpcSrv *grpcserver.Server,
) []app.Component {
	return []app.Component{logger, db, migrator, grpcSrv}
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "configs/greeter.yaml", "配置文件路径")
	flag.Parse()

	cfg := defaultConfig()
	loader := config.New(
		config.WithOptionalFile(configPath),
		config.WithEnvPrefix("GREETER"),
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
