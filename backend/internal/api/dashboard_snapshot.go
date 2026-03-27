package api

import (
	"context"
	"sync"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
)

var dashboardSnapOnce sync.Once
var dashboardSnapMu sync.RWMutex
var dashboardSnaps map[uint]DashboardResponse

func StartDashboardSnapshotJob(ctx context.Context) {
	dashboardSnapOnce.Do(func() {
		dashboardSnaps = make(map[uint]DashboardResponse)
		go runDashboardSnapshotLoop(ctx)
	})
}

func getDashboardSnapshot(uid uint) (DashboardResponse, bool) {
	dashboardSnapMu.RLock()
	defer dashboardSnapMu.RUnlock()
	v, ok := dashboardSnaps[uid]
	return v, ok
}

func runDashboardSnapshotLoop(ctx context.Context) {
	refreshAll := func() {
		if database.DB == nil || stratMgr == nil {
			return
		}
		var users []models.User
		if err := database.DB.Select("id").Find(&users).Error; err != nil {
			logger.Errorf("dashboard snapshot: list users failed err=%v", err)
			return
		}
		now := time.Now()
		for _, u := range users {
			resp, err := buildDashboardSnapshot(u.ID, now)
			if err != nil {
				continue
			}
			dashboardSnapMu.Lock()
			dashboardSnaps[u.ID] = resp
			dashboardSnapMu.Unlock()
		}
	}

	refreshAll()
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshAll()
		}
	}
}

func buildDashboardSnapshot(uid uint, now time.Time) (DashboardResponse, error) {
	return buildDashboardResponse(uid, now, dashboardBuildOptions{
		IncludeCalendar: true,
		CalendarDays:    60,
	})
}
