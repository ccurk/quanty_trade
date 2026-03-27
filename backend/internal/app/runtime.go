package app

import (
	"context"

	"quanty_trade/internal/api"
	"quanty_trade/internal/bus"
	"quanty_trade/internal/conf"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/strategy"
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
	mgr.StartWorkers()
	return mgr
}

func StartBackgroundJobs(ctx context.Context, mgr *strategy.Manager) {
	api.SetManager(mgr)
	api.StartDashboardSnapshotJob(ctx)
	api.StartDailyPnLJob(ctx)
}
