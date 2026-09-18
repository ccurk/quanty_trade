package conf

import (
	"strings"
	"testing"

	yaml "github.com/goccy/go-yaml"
)

// defect #271: defaultConfig() 曾经把 Exchange.Binance.Market 缺省成 "spot"。
// Load() 是 cfg = defaultConfig() 之后再 yaml.Unmarshal 合并——yaml 缺失
// exchange.binance.market 字段时 Unmarshal 不会清空已有值，所以旧代码里
// "yaml 完全不写 market" 这条路径从来到不了空字符串，exchange 包 #270 加的
// fail-fast 也就永远触发不到。这里直接钉住 defaultConfig()+Unmarshal 合并
// 这条真实机制，不依赖 Load() 的全局单例状态（loaded 一旦为 true 整个测试
// 二进制内不会重跑，没法在同一进程里反复摆弄）。
func TestDefaultConfigMarketEmptyWhenYAMLOmitsIt(t *testing.T) {
	yamlMissingMarket := `
exchange:
  name: binance
  binance:
    api_key: "x"
    api_secret: "y"
`
	cfg := defaultConfig()
	if err := yaml.Unmarshal([]byte(yamlMissingMarket), &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal 失败: %v", err)
	}

	got := strings.TrimSpace(cfg.Exchange.Binance.Market)
	if got != "" {
		t.Fatalf("yaml 缺 market 字段时 defaultConfig()+Unmarshal 合并后 Market 应为空串，实际 got %q —— "+
			"说明又把某个非空默认值塞回去了，exchange 包的 fail-fast 会被绕过", got)
	}
}

// yaml 显式写 market 时必须原样生效，与线上 conf_pro.yaml/conf_dev.yaml 的
// market: "usdm" 路径保持零行为影响。
func TestDefaultConfigMarketExplicitYAMLWins(t *testing.T) {
	yamlWithMarket := `
exchange:
  name: binance
  binance:
    market: "usdm"
`
	cfg := defaultConfig()
	if err := yaml.Unmarshal([]byte(yamlWithMarket), &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal 失败: %v", err)
	}
	if cfg.Exchange.Binance.Market != "usdm" {
		t.Fatalf("yaml 显式配置 market=usdm 时应原样生效, got %q", cfg.Exchange.Binance.Market)
	}
}
