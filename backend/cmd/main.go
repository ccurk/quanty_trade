package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"quanty_trade/internal/api"
	"quanty_trade/internal/bus"
	"quanty_trade/internal/conf"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/strategy"
	"quanty_trade/internal/ws"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

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

	// Initialize Database
	database.InitDB()

	if mode := conf.C().Server.Mode; mode != "" {
		gin.SetMode(mode)
	}

	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	r.Use(api.TraceMiddleware())
	r.Use(api.APILogger()) // Global API Logging
	r.Use(api.CORSMiddleware())

	hub := ws.NewHub()
	go hub.Run()

	var ex exchange.Exchange
	switch conf.C().Exchange.Name {
	case "binance":
		ex = exchange.NewBinanceExchange()
	default:
		ex = &exchange.MockExchange{Name: "Mock"}
	}
	mgr := strategy.NewManager(hub, ex)
	if conf.C().Redis.Enabled {
		if rb, err := bus.NewRedisBusFromConfig(); err == nil {
			mgr.SetRedisBus(rb)
		} else {
			logger.Errorf("Redis bus init failed err=%v", err)
		}
	}
	mgr.SyncFromDB(database.DB)
	go mgr.SyncRedisOpenCountsFromExchange(context.Background())
	api.SetManager(mgr)
	api.StartDashboardSnapshotJob(context.Background())
	api.StartDailyPnLJob(context.Background())

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
		protected.GET("/strategies/:id/logs", api.GetStrategyLogs)
		protected.DELETE("/strategies/:id", api.DeleteStrategy)

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
		}
	}

	var upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
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
