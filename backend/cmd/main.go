package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"quanty_trade/internal/api"
	"quanty_trade/internal/app"
	"quanty_trade/internal/conf"
	"quanty_trade/internal/database"
	"quanty_trade/internal/lark"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/ws"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// customRecovery 捕获任意 HTTP handler 的 panic，记 ERROR（→ 进 Lark 告警），返回 500。
// 替代 gin.Recovery()（后者只写 stderr，不进我们的 logger，因此不告警）。
func customRecovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		stack := debug.Stack()
		const maxStack = 2000
		st := string(stack)
		if len(st) > maxStack {
			st = st[:maxStack] + " …(truncated)"
		}
		logger.Errorf("[HTTP PANIC] %s %s panic=%v\n%s",
			c.Request.Method, c.Request.URL.Path, recovered, st)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	})
}

func initLogging() {
	c := conf.C()
	logDir := c.Paths.LogsDir
	if logDir == "" {
		logDir = conf.Path("logs")
	}
	_ = os.MkdirAll(logDir, 0o755)

	serverPath := filepath.Join(logDir, "server.log")
	serverFile, err := os.OpenFile(serverPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.SetOutput(os.Stdout)
		gin.DefaultWriter = os.Stdout
		gin.DefaultErrorWriter = os.Stderr
		return
	}

	gatewayPath := filepath.Join(logDir, "gateway.log")
	gatewayFile, err := os.OpenFile(gatewayPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		mw := io.MultiWriter(os.Stdout, serverFile)
		log.SetOutput(mw)
		log.SetFlags(log.LstdFlags | log.Lmicroseconds)
		gin.DefaultWriter = os.Stdout
		gin.DefaultErrorWriter = mw
		return
	}

	businessWriter := io.MultiWriter(os.Stdout, serverFile)
	gatewayWriter := io.MultiWriter(os.Stdout, gatewayFile)

	log.SetOutput(businessWriter)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	gin.DefaultWriter = gatewayWriter
	gin.DefaultErrorWriter = businessWriter

	if strings.ToLower(strings.TrimSpace(c.Server.Mode)) == "debug" {
		logger.SetLevel("debug")
	} else {
		logger.SetLevel("info")
	}
	logger.SetLevelFromEnv()
}

func main() {
	conf.MustLoad()
	if v := conf.C().Network.HTTPProxy; v != "" {
		_ = os.Setenv("HTTP_PROXY", v)
		_ = os.Setenv("http_proxy", v)
	}
	if v := conf.C().Network.HTTPSProxy; v != "" {
		_ = os.Setenv("HTTPS_PROXY", v)
		_ = os.Setenv("https_proxy", v)
	}
	if v := conf.C().Network.NoProxy; v != "" {
		_ = os.Setenv("NO_PROXY", v)
		_ = os.Setenv("no_proxy", v)
	}
	initLogging()
	logger.Infof("Runtime config db_type=%s db_host=%s db_name=%s exchange=%s binance_market=%s redis_enabled=%t redis_addr=%s telegram_enabled=%t", conf.C().DB.Type, conf.C().DB.Host, conf.C().DB.Name, conf.C().Exchange.Name, conf.C().Exchange.Binance.Market, conf.C().Redis.Enabled, conf.C().Redis.Addr, conf.C().Telegram.Enabled)

	// Fail-fast: 没有 JWT_SECRET / CONFIG_ENCRYPTION_KEY 时拒绝启动。
	// release 模式下还要求 ALLOWED_ORIGINS 非空。
	conf.MustValidateSecurity()

	// 先配置 Lark 同步告警通道，使启动期（DB 初始化）的致命错误也能推送。
	// 异步告警 loop 在 app.StartBackgroundJobs 里另行启动。
	lark.Configure(lark.Config{
		Enabled:            conf.C().Lark.Enabled,
		WebhookURL:         conf.C().Lark.WebhookURL,
		Secret:             conf.C().Lark.Secret,
		MinIntervalSeconds: conf.C().Lark.MinIntervalSeconds,
		MaxPerMinute:       conf.C().Lark.MaxPerMinute,
	})

	// Initialize Database
	database.InitDB()

	if mode := conf.C().Server.Mode; mode != "" {
		gin.SetMode(mode)
	}

	r := gin.New()
	r.Use(gin.Logger())
	r.Use(customRecovery()) // HTTP handler panic → logger.Errorf → Lark 告警
	r.Use(api.TraceMiddleware())
	r.Use(api.APILogger()) // Global API Logging
	r.Use(api.CORSMiddleware())

	hub := ws.NewHub()
	go hub.Run()

	runtimeCtx := context.Background()
	mgr := app.BuildStrategyManager(runtimeCtx, hub)
	app.StartBackgroundJobs(runtimeCtx, mgr)

	// Auth Routes
	r.POST("/api/login", api.Login)
	r.POST("/api/register", api.Register)
	r.GET("/api/public/templates", api.ListPublicTemplates)

	// Protected Routes
	protected := r.Group("/api")
	protected.Use(api.AuthMiddleware())
	{
		// Strategies
		protected.GET("/strategies", api.ListStrategies)
		protected.POST("/strategies", api.CreateStrategy)
		protected.POST("/strategies/:id/start", api.StartStrategy)
		protected.POST("/strategies/:id/stop", api.StopStrategy)
		protected.POST("/strategies/:id/backtest", api.BacktestStrategy)
		protected.GET("/backtests", api.ListBacktests)
		protected.GET("/backtests/:id", api.GetBacktest)
		protected.PUT("/strategies/:id/config", api.UpdateStrategyConfig)
		// 单字段（部分）更新：body 直接是要 merge 的 object，自动 routine / cron 用。
		// 例：PATCH /strategies/<id>/config  body: {"order_amount_pct": 0.3}
		protected.PATCH("/strategies/:id/config", api.PatchStrategyConfig)
		protected.GET("/strategies/:id/logs", api.GetStrategyLogs)
		protected.DELETE("/strategies/:id", api.DeleteStrategy)
		// 解除实例与 StrategyVersion 的绑定，让它回到跟随 Template.Code 的状态。
		// 用于"AI 优化过后想用回手动模板"的场景。
		protected.POST("/strategies/:id/unbind-version", api.UnbindStrategyVersion)
		// Bot/admin 用：精细化操控 strategy。每个动作都自动写 strategy_audit_logs。
		protected.PATCH("/strategies/:id", api.PatchStrategyMeta)                            // 改 name 等顶层字段
		protected.DELETE("/strategies/:id/blacklist/:symbol", api.RemoveSymbolFromBlacklist) // 释放单个黑名单
		protected.POST("/strategies/:id/rollback", api.RollbackStrategyTemplate)             // 回滚到上一版 template
		protected.POST("/strategies/:id/cancel-orders", api.CancelStrategyOrders)            // 紧急取消委托
		protected.GET("/strategies/:id/symbols", api.GetRunningSymbols)                      // 运行中币种(动态选币)
		protected.POST("/strategies/:id/symbols/rotate", api.RotateRunningSymbols)           // 热轮换币种(cron 驱动)

		// Positions
		protected.GET("/positions", api.ListPositions)
		protected.POST("/positions/close", api.ClosePosition)

		// Markets
		protected.GET("/markets/symbols", api.ListMarketSymbols)

		// Stats
		protected.GET("/stats/pnl", api.GetPnLSummary)
		protected.GET("/stats/dashboard", api.GetDashboard)

		// Strategy Square
		protected.GET("/templates", api.ListTemplates)
		protected.POST("/templates", api.SaveTemplate)
		protected.PUT("/templates/:id", api.UpdateTemplate)
		protected.POST("/templates/test", api.TestCode)
		protected.POST("/templates/:id/toggle", api.ToggleTemplateEnabled)
		protected.DELETE("/templates/:id", api.DeleteTemplate)
		protected.POST("/templates/publish", api.PublishTemplate)
		protected.POST("/templates/reference", api.ReferenceTemplate)

		// Admin only
		admin := protected.Group("/admin")
		admin.Use(api.AdminOnly())
		{
			admin.GET("/users", api.ListUsers)
			admin.POST("/users", api.CreateUser)
			admin.DELETE("/users/:id", api.DeleteUser)
			// 强制回填 daily_pn_ls 表（默认 400 天）。在 daily PnL job 还没跑
			// 完、或者首次部署、或者想立刻刷新历史数据时调一次。
			admin.POST("/daily-pnl/backfill", api.BackfillDailyPnL)
			// Cron-driven auto-optimize：context 给外部 LLM 用、apply 落地新
			// template + rebind + restart。详见 internal/api/optimize_handlers.go
			admin.GET("/optimize/context", api.GetOptimizeContext)
			admin.POST("/optimize/apply", api.ApplyOptimization)
			// Audit log 读接口（写是自动的）
			admin.GET("/strategies/:id/audit", api.ListStrategyAudit)
			// 一键迁移：export 出 JSON bundle，import 创建新 template + 新 instance
			admin.POST("/strategies/:id/export", api.ExportStrategy)
			admin.POST("/strategies/import", api.ImportStrategy)
		}
	}

	// CheckOrigin 校验：release 模式下严格白名单（MustValidateSecurity
	// 已保证非空）。非 release 模式且白名单空 → allow-all（开发友好）。
	// 白名单匹配方式：精确字符串相等，大小写敏感（按 RFC 6454 Origin 序列化）。
	// 通配项 "*" 视为允许所有（便于本地开发环境显式声明）。
	allowedOrigins := conf.C().Security.AllowedOrigins
	releaseMode := strings.ToLower(strings.TrimSpace(conf.C().Server.Mode)) == "release"
	originAllowAll := false
	for _, o := range allowedOrigins {
		if strings.TrimSpace(o) == "*" {
			originAllowAll = true
			break
		}
	}
	if !releaseMode && len(allowedOrigins) == 0 {
		originAllowAll = true
		log.Println("[WS] non-release mode + no ALLOWED_ORIGINS configured → allow-all (do NOT use in production)")
	}
	originSet := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		t := strings.TrimSpace(o)
		if t != "" && t != "*" {
			originSet[t] = struct{}{}
		}
	}
	var upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			if originAllowAll {
				return true
			}
			origin := r.Header.Get("Origin")
			if origin == "" {
				// 无 Origin 头：通常是非浏览器客户端（curl、桌面 App）。
				// 同时配合 Authorization 校验时是可以接受的；这里保守地
				// 仅在 allow-all 时放行，否则拒绝。
				return false
			}
			_, ok := originSet[origin]
			return ok
		},
	}

	// WebSocket for real-time updates
	r.GET("/ws", func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("Failed to upgrade websocket: %v", err)
			return
		}
		ws.HandleConnection(hub, conn)
	})

	log.Println("Backend starting on :8080")
	addr := ":" + strconv.Itoa(conf.C().Server.Port)
	r.Run(addr)
}
