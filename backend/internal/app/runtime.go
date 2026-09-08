package app

import (
	"context"
	"fmt"

	"quanty_trade/internal/api"
	"quanty_trade/internal/bus"
	"quanty_trade/internal/conf"
	"quanty_trade/internal/database"
	"quanty_trade/internal/equity/equitydb"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/lark"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/marketmaker"
	"quanty_trade/internal/marketmaker/markoutdb"
	"quanty_trade/internal/strategy"
	"quanty_trade/internal/telegram"
	"quanty_trade/internal/ws"
)

func BuildExchange() exchange.Exchange {
	switch conf.C().Exchange.Name {
	case "binance":
		return exchange.NewBinanceExchange()
	default:
		return &exchange.MockExchange{Name: "Mock"}
	}
}

func BuildStrategyManager(ctx context.Context, hub *ws.Hub) *strategy.Manager {
	mgr := strategy.NewManager(hub, BuildExchange())
	if conf.C().Redis.Enabled {
		if rb, err := bus.NewRedisBusFromConfig(); err == nil {
			mgr.SetRedisBus(rb)
		} else {
			logger.Errorf("Redis bus init failed err=%v", err)
		}
	}
	mgr.SyncFromDB(database.DB)
	go mgr.SyncRedisOpenCountsFromExchange(ctx)
	mgr.StartQuickTradeMonitor(ctx)
	mgr.StartROIGuardMonitor(ctx)
	mgr.StartROISLScanMonitor(ctx)
	mgr.StartTPSLGuardMonitor(ctx)
	mgr.StartWSPositionGuard(ctx)
	mgr.StartWorkers()
	go mgr.RestoreRunningStrategies(ctx)
	return mgr
}

func StartBackgroundJobs(ctx context.Context, mgr *strategy.Manager) {
	api.SetManager(mgr)
	// 跨所做市模块(独立于策略引擎)。默认禁用:仅当 $MARKETMAKER_CONFIG 指向配置文件
	// 且其中 enabled=true 才启动;observe_only 模式只测价差不下单。
	mmCfg := marketmaker.LoadConfigFromEnv()
	// markout 落库。在 Start 之前装,免得刚起的引擎头几笔成交没人接。
	// markout_fills 表不存在时是彻底的 no-op(表不在 AutoMigrate 名单里,
	// 所有者跑 scripts/markout_persistence.sql 才算开启)。写库全程在独立
	// goroutine 上,失败/变慢都碰不到报价与下单路径。
	markoutdb.Install(mmCfg)
	// 权益快照落库(台账 #42)。同样是"表不存在就彻底 no-op"的形状:
	// equity_snapshots 不在 AutoMigrate 名单里,所有者跑 scripts/equity_snapshots.sql
	// 才算开启。装在余额读取之前,免得引擎起来后头几次读数没人接。
	// 写库全程在独立 goroutine 上,失败/变慢都碰不到报价与下单路径。
	equitydb.Install()
	if mm, err := marketmaker.Start(mmCfg); err != nil {
		logger.Errorf("[mm] start failed: %v", err)
	} else if mm != nil {
		go func() { <-ctx.Done(); mm.Stop() }()
	}
	// Lark 群机器人 ERROR 告警：注册 logger error sink，ERROR 日志实时推送。
	if ln := lark.Start(lark.Config{
		Enabled:            conf.C().Lark.Enabled,
		WebhookURL:         conf.C().Lark.WebhookURL,
		Secret:             conf.C().Lark.Secret,
		MinIntervalSeconds: conf.C().Lark.MinIntervalSeconds,
		MaxPerMinute:       conf.C().Lark.MaxPerMinute,
	}); ln != nil {
		lark.SendInfo(fmt.Sprintf("后端启动 · 端口 %d · 交易所 %s · ERROR 告警已接入",
			conf.C().Server.Port, conf.C().Exchange.Name))
	}
	if svc := telegram.Start(ctx, mgr); svc != nil {
		telegram.NotifySystemEvent(
			"后端服务重启",
			"服务：QuantyTrade Backend",
			fmt.Sprintf("端口：%d", conf.C().Server.Port),
			fmt.Sprintf("交易所：%s", conf.C().Exchange.Name),
		)
	}
	api.StartDashboardSnapshotJob(ctx)
	api.StartDailyPnLJob(ctx)
	api.StartLogRetentionJob(ctx)
	api.StartTriArbDetectorJob(ctx)
	api.StartRebalanceMonitor(ctx)
	// 做市全市场扫描:只读观测(gate+coinsph),按净边排序 Top-100,绝不下单。
	marketmaker.StartUniverseScanner([]string{"gate", "coinsph"}, 10)
	// gate 永续 × binance 永续的只读盘口观测,逐行 JSONL 落到 logs/gate-futures-scan.jsonl。
	// 实测约 77MB/天,自带 50MB×5 轮转(gzip 备份)封顶,保留约 3.3 天,详见该文件常量注释。
	// 台账 #14(跨所价差套利)卡在没有持续数据,这条就是补那份数据;同样绝不下单。
	// 日志目录的取法与 cmd/main.go initLogging() 一致,落在 server.log 旁边。
	mmLogDir := conf.C().Paths.LogsDir
	if mmLogDir == "" {
		mmLogDir = conf.Path("logs")
	}
	marketmaker.StartGateFuturesScanner(mmLogDir, 0)
}
