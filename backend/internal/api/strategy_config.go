package api

import (
	"encoding/json"
	"strings"
)

func normalizeStrategyConfigJSON(raw string) (string, map[string]interface{}, error) {
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return "", nil, err
	}
	norm := normalizeStrategyConfigMap(cfg)
	b, err := json.Marshal(norm)
	if err != nil {
		return "", nil, err
	}
	return string(b), norm, nil
}

func normalizeStrategyConfigMap(cfg map[string]interface{}) map[string]interface{} {
	if cfg == nil {
		cfg = map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(cfg))
	for k, v := range cfg {
		out[k] = v
	}
	trimString := func(key string) {
		if v, ok := out[key].(string); ok {
			out[key] = strings.TrimSpace(v)
		}
	}
	trimString("symbol")
	trimString("symbols")
	trimString("market")
	trimString("order_amount_mode")
	trimString("symbol_select_mode")
	trimString("allowed_symbols")
	for _, key := range []string{"take_profit_pct", "stop_loss_pct", "order_amount_pct"} {
		if v, ok := out[key].(float64); ok && v > 1 {
			out[key] = v / 100
		}
	}
	if mode, ok := out["order_amount_mode"].(string); ok && strings.TrimSpace(mode) == "" {
		out["order_amount_mode"] = "notional"
	}
	if _, ok := out["max_concurrent_positions"].(float64); !ok {
		out["max_concurrent_positions"] = 1.0
	}
	if _, ok := out["max_trades_per_day"].(float64); !ok {
		out["max_trades_per_day"] = 3.0
	}
	if _, ok := out["warmup_bars"].(float64); !ok {
		out["warmup_bars"] = 100.0
	}
	return out
}
