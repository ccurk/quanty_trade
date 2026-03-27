package strategy

import (
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/models"
)

func (m *Manager) setStrategyStatus(inst *StrategyInstance, status StrategyStatus) {
	if inst == nil {
		return
	}
	inst.mu.Lock()
	inst.Status = status
	inst.mu.Unlock()
	if database.DB != nil {
		_ = database.DB.Model(&models.StrategyInstance{}).Where("id = ?", inst.ID).
			Updates(map[string]interface{}{"status": string(status), "updated_at": time.Now()}).Error
	}
	if m != nil && m.hub != nil {
		m.hub.BroadcastJSON(map[string]interface{}{
			"type":        "strategy_status",
			"strategy_id": inst.ID,
			"owner_id":    inst.OwnerID,
			"status":      status,
		})
	}
}
