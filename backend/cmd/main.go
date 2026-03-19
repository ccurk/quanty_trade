package main

import (
	"log"
	"net/http"
	"os"
	"quanty_trade/internal/api"
	"quanty_trade/internal/database"
	"quanty_trade/internal/exchange"
	"quanty_trade/internal/strategy"
	"quanty_trade/internal/ws"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func main() {
	// Initialize Database
	database.InitDB()

	r := gin.Default()
	r.Use(api.APILogger()) // Global API Logging
	r.Use(api.CORSMiddleware())

	hub := ws.NewHub()
	go hub.Run()

	var ex exchange.Exchange
	switch os.Getenv("EXCHANGE") {
	case "binance":
		ex = exchange.NewBinanceExchange()
	default:
		ex = &exchange.MockExchange{Name: "Mock"}
	}
	mgr := strategy.NewManager(hub, ex)
	mgr.SyncFromDB(database.DB)
	api.SetManager(mgr)

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

		// Strategy Square
		protected.GET("/templates", api.ListTemplates)
		protected.POST("/templates", api.SaveTemplate)
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
	r.Run(":8080")
}
