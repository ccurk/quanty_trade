package app

import (
	"context"
	"fmt"

	"quanty_trade/internal/api"
	"quanty_trade/internal/bus"
	"quanty_trade/internal/conf"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/lark"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/marketmaker"
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
	if mm, err := marketmaker.Start(marketmaker.LoadConfigFromEnv()); err != nil {
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
	// 做市全市场扫描:只扫用户能实际交易的所(kucoin/mexc 因 +86 无法注册已剔除)。
	marketmaker.StartUniverseScanner([]string{"gate", "coinsph"}, 10)
}
