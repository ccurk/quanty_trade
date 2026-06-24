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
	mgr.StartWorkers()
	return mgr
}

func StartBackgroundJobs(ctx context.Context, mgr *strategy.Manager) {
	api.SetManager(mgr)
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
}
