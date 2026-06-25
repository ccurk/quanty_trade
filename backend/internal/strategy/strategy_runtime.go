package strategy

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
)

func (m *Manager) healthMonitorLoop(ctx context.Context, inst *StrategyInstance) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			inst.mu.Lock()
			if (inst.Status != StatusRunning && inst.Status != StatusStarting) || inst.stopping {
				inst.mu.Unlock()
				return
			}
			startedAt := inst.startedAt
			bootID := inst.bootID
			lastHB := inst.lastHB
			restarting := inst.restarting
			inst.mu.Unlock()

			if restarting {
				continue
			}

			now := time.Now()
			readyGrace := 30 * time.Second
			hbTimeout := 20 * time.Second

			if strings.TrimSpace(bootID) == "" {
				if !startedAt.IsZero() && now.Sub(startedAt) > readyGrace {
					m.requestRestart(inst, "no_ready")
				}
				continue
			}
			if lastHB.IsZero() {
				continue
			}
			if now.Sub(lastHB) > hbTimeout {
				m.requestRestart(inst, "heartbeat_timeout")
			}
		}
	}
}

func (m *Manager) waitProcessLoop(inst *StrategyInstance) {
	inst.mu.Lock()
	cmd := inst.cmd
	inst.mu.Unlock()
	if cmd == nil {
		return
	}
	err := cmd.Wait()

	inst.mu.Lock()
	runtimePath := inst.RuntimePath
	runtimeGenerated := inst.RuntimeGenerated
	runtimeKeep := inst.RuntimeKeep
	inst.RuntimePath = ""
	inst.RuntimeGenerated = false
	inst.RuntimeKeep = false
	// killedByUs / stopping = 我们主动停的（routine 改配置重启、健康检查重启、删除等），
	// 不是进程自己崩溃。读后清零 killedByUs，避免下一次真崩溃被误判为主动停。
	intentional := inst.killedByUs || inst.stopping
	inst.killedByUs = false
	inst.mu.Unlock()

	// 只有"真崩溃"才保留 runtime 脚本 + 记 error；主动停就正常清理、不告警。
	if !intentional && err != nil && runtimeGenerated && strings.TrimSpace(runtimePath) != "" {
		runtimeKeep = true
		emitStrategyLog(inst, "error", fmt.Sprintf("Strategy crashed; runtime script kept path=%s", runtimePath))
	}
	if runtimeGenerated && !runtimeKeep && strings.TrimSpace(runtimePath) != "" {
		_ = os.Remove(runtimePath)
	}

	inst.mu.Lock()
	stopping := inst.stopping
	active := inst.Status == StatusRunning || inst.Status == StatusStarting
	inst.mu.Unlock()

	if intentional || stopping {
		// 主动停/重启：状态置 Stopped（而非 Error），不告警、不自动重启。
		if active {
			m.setStrategyStatus(inst, StatusStopped)
		}
		return
	}

	// 到这里才是真正的意外退出 → 告警 + 自动重启
	if active {
		m.setStrategyStatus(inst, StatusError)
	}
	if err != nil {
		logger.Errorf("[STRATEGY EXIT] id=%s owner=%d err=%v", inst.ID, inst.OwnerID, err)
	} else {
		logger.Errorf("[STRATEGY EXIT] id=%s owner=%d", inst.ID, inst.OwnerID)
	}
	_ = database.DB.Create(&models.StrategyLog{
		StrategyID: inst.ID,
		Level:      "error",
		Message:    fmt.Sprintf("Strategy process exited: %v", err),
		CreatedAt:  time.Now(),
	}).Error
	inst.hub.BroadcastJSON(map[string]interface{}{
		"type":        "error",
		"strategy_id": inst.ID,
		"owner_id":    inst.OwnerID,
		"error":       "Strategy process exited",
	})
	m.requestRestart(inst, "process_exited")
}

func (m *Manager) requestRestart(inst *StrategyInstance, reason string) {
	if inst == nil {
		return
	}
	inst.mu.Lock()
	if inst.restarting || inst.stopping || (inst.Status != StatusRunning && inst.Status != StatusStarting) {
		inst.mu.Unlock()
		return
	}
	inst.restarting = true
	id := inst.ID
	ownerID := inst.OwnerID
	inst.mu.Unlock()

	logger.Errorf("[STRATEGY HEALTH] id=%s owner=%d action=restart reason=%s", id, ownerID, reason)
	_ = database.DB.Create(&models.StrategyLog{
		StrategyID: id,
		Level:      "error",
		Message:    fmt.Sprintf("Healthcheck restart: %s", reason),
		CreatedAt:  time.Now(),
	}).Error
	m.hub.BroadcastJSON(map[string]interface{}{
		"type":        "error",
		"strategy_id": id,
		"owner_id":    ownerID,
		"error":       fmt.Sprintf("Healthcheck restart: %s", reason),
	})

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[STRATEGY HEALTH] id=%s owner=%d action=restart_panic reason=%s panic=%v", id, ownerID, reason, r)
				inst.mu.Lock()
				inst.restarting = false
				inst.mu.Unlock()
			}
		}()
		_ = m.StopStrategy(id, true)
		time.Sleep(2 * time.Second)
		if err := m.StartStrategy(id); err != nil {
			logger.Errorf("[STRATEGY HEALTH] id=%s owner=%d action=restart_failed reason=%s err=%v", id, ownerID, reason, err)
			inst.mu.Lock()
			inst.restarting = false
			inst.mu.Unlock()
		}
	}()
}
