package app

import (
	"context"
	"fmt"
	"time"

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

// redisBusReconnectInterval 15s:比人反应快,又不至于把一台连不上的 Redis 打爆。
const redisBusReconnectInterval = 15 * time.Second

func BuildStrategyManager(ctx context.Context, hub *ws.Hub) *strategy.Manager {
	mgr := strategy.NewManager(hub, BuildExchange())
	if conf.C().Redis.Enabled {
		if rb, err := bus.NewRedisBusFromConfig(); err == nil {
			mgr.SetRedisBus(rb)
		} else {
			// 台账 #115。原来这里【只有】下面那行 Errorf,然后若无其事地继续:
			// 容器 Up、网页 200、/api/health/db 200,而 Go↔Python 的总线是死的。
			//
			// 为什么不改成 log.Fatal 拒启:这个进程同时是【手动止血的唯一入口】——
			// 看仓位 /positions、平仓 /positions/close、撤单 /strategies/:id/cancel-orders
			// 全都走 DB + 交易所 REST,一步都不经 Redis。Redis 一断就退出,配上
			// docker restart:always 就是 crash-loop:所有者在最需要手动平仓的时刻
			// 连后台都打不开。做市模块(gate×binance)也完全不用 Redis,没理由陪葬。
			//
			// 所以选的是"起来,但认账、并且让外面看得见":
			//   1) 健康端点从此对 Redis 说实话(/api/health、/api/health/redis 回 503)。
			//      失败结果已由 bus.NewRedisBusFromConfig 登记进进程级状态,
			//      调用方吞不掉。这是"30 秒内被外面发现"的主信号。
			//   2) 尽力发一条外部告警。注意:Lark 通道当前在生产上是坏的
			//      (容器 DNS 解析 open.larksuite.com 失败,docker logs 里实测
			//      9,877 条 "[lark] send err="),所以【不能】把可发现性押在它身上——
			//      它只是补充,主信号是上面那个健康端点。
			//   3) 后台按固定间隔重连,恢复后自动接上,不需要人重启进程。
			//      这一条同时保证:Redis 口令/网络晚一步就位时,先起来的后端会自愈,
			//      不会把所有者的恢复流程(重登 Tailscale → 重启 backend)卡住。
			logger.Errorf("[REDIS BUS] 初始化失败,总线不可用,策略将无法启动(健康端点已转 503,后台每 %s 重连一次): %v"+
				" | 该配哪里: REDIS_ENABLED / REDIS_ADDR / REDIS_PASSWORD —— 生产上这三个由容器 env 注入,conf_pro.yaml 里 redis.password 是空串占位",
				redisBusReconnectInterval, err)
			lark.AlertSync("🚨 QuantyTrade · Redis 总线不可用,策略无法启动 · " + err.Error())
			go reconnectRedisBus(ctx, mgr)
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

// reconnectRedisBus 在启动期 Redis 不可用之后守着重连,直到接上为止。
//
// 不打失败日志:失败状态由 bus.Health() 持有,健康端点随时可查;每 15s 刷一条 ERROR
// 只会淹掉日志,还会把告警通道刷爆(台账 #184/#196 就是这个形状)。只在【恢复】时出声。
func reconnectRedisBus(ctx context.Context, mgr *strategy.Manager) {
	t := time.NewTicker(redisBusReconnectInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := mgr.EnsureRedisBus(); err != nil {
				continue
			}
			logger.Infof("[REDIS BUS] 重连成功,总线恢复;健康端点转回 200。开机自恢复只在启动时跑过一次,断线期间转为 error 的策略需要人工重新启动")
			lark.AlertSync("✅ QuantyTrade · Redis 总线已恢复(断线期间启动失败的策略需人工重启)")
			return
		}
	}
}

// StartBackgroundJobs 拉起所有后台模块,并把【做市引擎的优雅关闭】交回给调用方。
//
// 返回值必须由 main 在退出前【同步】调一次:撤单要花时间(关闭时大概率正被限流),
// 挂在 ctx.Done 的 goroutine 里做等于没做 —— main 早就 return 了,进程一没,
// 撤单跑到一半就断。台账 #117 的一半就是这个形状。另一半是根本没人 cancel 这个 ctx
// (原来是 context.Background),见 cmd/main.go 的信号接线。
func StartBackgroundJobs(ctx context.Context, mgr *strategy.Manager) (shutdownMM func()) {
	shutdownMM = func() {}
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
		// Stop() 自带两段硬上限(marketmaker/engine.go shutdownDrainBudget /
		// shutdownCancelBudget),所以这里不再叠第二个超时 —— 一件事一个主人。
		shutdownMM = mm.Stop
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
	return shutdownMM
}
