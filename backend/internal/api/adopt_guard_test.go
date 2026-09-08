package api

import (
	"testing"

	"quanty_trade/internal/models"
)

// The positions GET handler is the second place that can hand an unclaimed
// exchange net position to a strategy. Its guard must match the reconcile
// loop's (strategy.mayAdoptUnclaimedPosition): only running/starting adopt.
func TestInstanceRowMayAdopt(t *testing.T) {
	cases := []struct {
		name string
		si   *models.StrategyInstance
		want bool
	}{
		{"running", &models.StrategyInstance{ID: "a", Status: "running"}, true},
		{"starting", &models.StrategyInstance{ID: "a", Status: "starting"}, true},
		{"mixed case running", &models.StrategyInstance{ID: "a", Status: " Running "}, true},
		{"stopped", &models.StrategyInstance{ID: "a", Status: "stopped"}, false},
		{"error", &models.StrategyInstance{ID: "a", Status: "error"}, false},
		{"empty status", &models.StrategyInstance{ID: "a"}, false},
		{"deleted instance", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := instanceRowMayAdopt(tc.si); got != tc.want {
				t.Errorf("instanceRowMayAdopt(%v) = %v, want %v", tc.si, got, tc.want)
			}
		})
	}
}

func TestFindStrategyInstanceByID(t *testing.T) {
	rows := []models.StrategyInstance{
		{ID: "sid-1", Name: "qt-breakout-follow-v2", Status: "stopped"},
		{ID: "sid-2", Name: "Meme_合约信号计算引擎_1", Status: "running"},
	}
	if got := findStrategyInstanceByID(rows, "sid-2"); got == nil || got.Name != "Meme_合约信号计算引擎_1" {
		t.Errorf("findStrategyInstanceByID(sid-2) = %v, want the running instance", got)
	}
	if got := findStrategyInstanceByID(rows, "sid-missing"); got != nil {
		t.Errorf("missing id returned %v, want nil", got)
	}
	if got := findStrategyInstanceByID(rows, "  "); got != nil {
		t.Errorf("blank id returned %v, want nil", got)
	}
}

// A retired strategy whose last orders are still inside the owner's recent-500
// window must not be able to adopt through the orderMeta fallback.
func TestRetiredStrategyCannotAdoptViaOrderMeta(t *testing.T) {
	rows := []models.StrategyInstance{
		{ID: "sid-1", Name: "qt-fade-short-v2", Status: "stopped",
			Config: `{"symbols":["RETIRED/USDT"]}`},
	}
	if findStrategyInstanceForSymbol(rows, "COLLECT/USDT") != nil {
		t.Fatal("retired config must not match a live symbol")
	}
	if instanceRowMayAdopt(findStrategyInstanceByID(rows, "sid-1")) {
		t.Error("stopped instance adopted through the orderMeta fallback")
	}
}
