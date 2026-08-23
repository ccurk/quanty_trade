package rebalance

import "testing"

func validCfg() *Config {
	return &Config{
		Enable:            true,
		ExecExchange:      "gate",
		ReservoirExchange: "binance",
		DryRun:            true,
		Mode:              ModeRecommend,
		Assets: []AssetConfig{
			{Asset: "USDT", Network: "TRC20", Opt: 1000, Min: 500, Max: 1500, MinTransfer: 50},
		},
	}
}

func TestValidateOK(t *testing.T) {
	if err := validCfg().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateDisabledSkips(t *testing.T) {
	c := &Config{Enable: false} // 一片空白, 但关闭 → 应放行
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled config should no-op, got %v", err)
	}
}

func TestValidateBandOrder(t *testing.T) {
	c := validCfg()
	c.Assets[0].Min = 1200 // min > opt
	if err := c.Validate(); err == nil {
		t.Fatal("expected min<opt<max violation to fail")
	}
}

func TestValidateNetworkRequired(t *testing.T) {
	c := validCfg()
	c.Assets[0].Network = ""
	if err := c.Validate(); err == nil {
		t.Fatal("missing network(链) must fail — 错链丢钱")
	}
}

func TestValidateDupAsset(t *testing.T) {
	c := validCfg()
	c.Assets = append(c.Assets, c.Assets[0])
	if err := c.Validate(); err == nil {
		t.Fatal("duplicate asset must fail")
	}
}

func TestValidateExecEqReservoir(t *testing.T) {
	c := validCfg()
	c.ReservoirExchange = "GATE" // 同所(大小写不敏感)
	if err := c.Validate(); err == nil {
		t.Fatal("exec == reservoir must fail")
	}
}

func TestValidateAutoNeedsCaps(t *testing.T) {
	c := validCfg()
	c.Mode = ModeAuto // 会动钱, 但没设上限
	if err := c.Validate(); err == nil {
		t.Fatal("mode=auto without caps must fail")
	}
	c.MaxPerTransferUSD, c.MaxPerDayUSD = 2000, 5000
	if err := c.Validate(); err != nil {
		t.Fatalf("auto with caps should pass: %v", err)
	}
}

func TestValidateModeDefault(t *testing.T) {
	c := validCfg()
	c.Mode = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("empty mode should default, got %v", err)
	}
	if c.Mode != ModeRecommend {
		t.Errorf("empty mode should default to recommend, got %q", c.Mode)
	}
}

func TestBuildPlannerFromConfig(t *testing.T) {
	wl := NewWhitelist([]AllowedAddress{{Exchange: "gate", Asset: "USDT", Network: "TRC20", Address: "TGate"}})
	p := validCfg().BuildPlanner(wl)
	plans := p.Plan([]Balance{{Exchange: "gate", Asset: "USDT", Free: 100}}) // below min 500
	if len(plans) != 1 || plans[0].ToExchange != "gate" || !plans[0].Executable() {
		t.Fatalf("planner from config should propose an executable refill, got %+v", plans)
	}
}
