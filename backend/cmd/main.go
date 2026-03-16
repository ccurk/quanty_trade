package main

import (
	"log"
	"net/http"
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

	hub := ws.NewHub()
	go hub.Run()

	ex := &exchange.MockExchange{Name: "Mock"}
	mgr := strategy.NewManager(hub, ex)
	mgr.SyncFromDB(database.DB)
	api.SetManager(mgr)

	// API Routes
	r.POST("/api/login", api.Login)

	// Protected Routes
	protected := r.Group("/api")
	protected.Use(api.AuthMiddleware())
	{
		// Strategies
		protected.GET("/strategies", api.ListStrategies)
		protected.POST("/strategies/:id/start", api.StartStrategy)
		protected.POST("/strategies/:id/stop", api.StopStrategy)

		// Strategy Square
		protected.GET("/templates", api.ListTemplates)
		protected.POST("/templates/publish", api.PublishTemplate)
		protected.POST("/templates/reference", api.ReferenceTemplate)

		// Admin only
		admin := protected.Group("/admin")
		admin.Use(api.AdminOnly())
		{
			admin.GET("/users", api.ListUsers)
			admin.POST("/users", api.CreateUser)
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
