package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"quanty_trade/internal/database"
	"quanty_trade/internal/models"
)

// authorizeStrategy loads the instance and enforces owner/admin access, writing
// the error response and returning false on failure.
func authorizeStrategy(c *gin.Context, id string) bool {
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")
	var instance models.StrategyInstance
	if err := database.DB.Where("id = ?", id).First(&instance).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Strategy not found"})
		return false
	}
	if uid, ok := userID.(uint); !ok || (instance.OwnerID != uid && userRole != "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return false
	}
	return true
}

// GetRunningSymbols returns the running strategy's current live feed symbol set
// (in-memory, which differs from the persisted config when auto-selected).
// GET /strategies/:id/symbols
func GetRunningSymbols(c *gin.Context) {
	id := c.Param("id")
	if !authorizeStrategy(c, id) {
		return
	}
	syms, err := stratMgr.RunningSymbols(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"strategy_id": id, "symbols": syms})
}

// RotateRunningSymbols hot-rotates a running strategy's symbols without a
// restart: removes the given symbols (unless they hold an open position) and
// adds the given ones (with history backfill). This is the platform mechanism
// the phase-1 rotation cron drives; the cron decides which symbols to rotate.
// POST /strategies/:id/symbols/rotate  body: {"add":[...],"remove":[...],"reason":"..."}
func RotateRunningSymbols(c *gin.Context) {
	id := c.Param("id")
	if !authorizeStrategy(c, id) {
		return
	}
	var req struct {
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
		Reason string   `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
		return
	}
	res, err := stratMgr.RotateSymbols(id, req.Add, req.Remove, req.Reason)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}
