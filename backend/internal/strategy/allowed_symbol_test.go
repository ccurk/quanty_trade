package strategy

import "testing"

// Regression: signals for symbols hot-added by dynamic rotation (present in
// feedSymbols but absent from the static config seed list) were silently
// dropped by isAllowedSymbol — "择优胜出" in the strategy log but no order.
func TestIsAllowedSymbolAcceptsRotatedInSymbols(t *testing.T) {
	inst := &StrategyInstance{
		Config:      map[string]interface{}{"symbols": "BTCUSDT,ETHUSDT"},
		feedSymbols: []string{"BTCUSDT", "ETHUSDT", "ONUSDT"}, // ONUSDT rotated in at runtime
	}
	if !isAllowedSymbol(inst, "BTCUSDT") {
		t.Fatal("static seed symbol must be allowed")
	}
	if !isAllowedSymbol(inst, "ONUSDT") {
		t.Fatal("rotated-in symbol (in feedSymbols, not in config) must be allowed — silent signal drop regressed")
	}
	if isAllowedSymbol(inst, "XXXUSDT") {
		t.Fatal("symbol in neither config nor feed must be rejected")
	}

	// Singular-symbol config variant.
	inst2 := &StrategyInstance{
		Config:      map[string]interface{}{"symbol": "BTCUSDT"},
		feedSymbols: []string{"BTCUSDT", "ONUSDT"},
	}
	if !isAllowedSymbol(inst2, "ONUSDT") {
		t.Fatal("rotated-in symbol must be allowed under singular-symbol config too")
	}
	if isAllowedSymbol(inst2, "YYYUSDT") {
		t.Fatal("unknown symbol must still be rejected under singular-symbol config")
	}
}
