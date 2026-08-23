package rebalance

import (
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

// Config is the rebalance band/strategy config, loaded from conf/rebalance.yaml.
// The withdrawal WHITELIST is deliberately NOT here — it's managed in the UI
// (DB-backed) because destinations are edited operationally. Bands and safety
// limits are strategy tuning, so they live in yaml the owner controls.
type Config struct {
	Enable            bool          `yaml:"enable"`
	ExecExchange      string        `yaml:"execExchange"`      // 被管理库存的所, e.g. gate
	ReservoirExchange string        `yaml:"reservoirExchange"` // 资金水库(补/收), e.g. binance
	DryRun            bool          `yaml:"dryRun"`            // true=只算+日志, 不真提现
	Mode              string        `yaml:"mode"`              // recommend | semi | auto
	CooldownMinutes   int           `yaml:"cooldownMinutes"`   // 同一资产两次搬运的最小间隔(链上到账慢)
	MaxPerTransferUSD float64       `yaml:"maxPerTransferUsdt"` // 单笔上限(USDT计), 硬 safety
	MaxPerDayUSD      float64       `yaml:"maxPerDayUsdt"`      // 单日累计上限, 硬 safety
	Assets            []AssetConfig `yaml:"assets"`
}

// AssetConfig is one asset's inventory band on the exec exchange.
type AssetConfig struct {
	Asset       string  `yaml:"asset"`       // e.g. USDT
	Network     string  `yaml:"network"`     // 链, e.g. TRC20 —— 必填, 错链丢钱
	Opt         float64 `yaml:"opt"`         // 目标库存(回到这个值)
	Min         float64 `yaml:"min"`         // 下限, 低于→补
	Max         float64 `yaml:"max"`         // 上限, 高于→回落
	MinTransfer float64 `yaml:"minTransfer"` // dust: 缺口小于此不搬(免白付提现费)
}

// Rebalance execution modes, least→most dangerous.
const (
	ModeRecommend = "recommend" // 只给建议, 从不提现
	ModeSemi      = "semi"      // 逐笔人工确认后提现
	ModeAuto      = "auto"      // 达阈值自动提现
)

// LoadConfig reads + validates conf/rebalance.yaml. A nil error means the config
// is internally consistent enough that the planner can't be fed nonsense.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("rebalance.yaml 解析失败: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate catches the mistakes that would move the wrong amount, the wrong
// direction, or to the wrong chain — before any of that can touch real funds.
func (c *Config) Validate() error {
	if !c.Enable {
		return nil // 关闭 = 整模块 no-op, 不细校验
	}
	if strings.TrimSpace(c.ExecExchange) == "" || strings.TrimSpace(c.ReservoirExchange) == "" {
		return fmt.Errorf("execExchange / reservoirExchange 必填")
	}
	if eqFold(c.ExecExchange, c.ReservoirExchange) {
		return fmt.Errorf("execExchange 和 reservoirExchange 不能相同")
	}
	switch c.Mode {
	case ModeRecommend, ModeSemi, ModeAuto:
	case "":
		c.Mode = ModeRecommend // 缺省取最安全档
	default:
		return fmt.Errorf("mode 只能是 recommend|semi|auto, 收到 %q", c.Mode)
	}
	if c.Mode != ModeRecommend { // 真会动钱: 安全上限必须 > 0
		if c.MaxPerTransferUSD <= 0 || c.MaxPerDayUSD <= 0 {
			return fmt.Errorf("mode=%s 时 maxPerTransferUsdt / maxPerDayUsdt 必须 > 0(硬上限不可缺)", c.Mode)
		}
	}
	if len(c.Assets) == 0 {
		return fmt.Errorf("assets 至少配置一个")
	}
	seen := map[string]bool{}
	for _, a := range c.Assets {
		as := strings.ToUpper(strings.TrimSpace(a.Asset))
		if as == "" {
			return fmt.Errorf("asset 名不能为空")
		}
		if seen[as] {
			return fmt.Errorf("asset %s 重复配置", as)
		}
		seen[as] = true
		if strings.TrimSpace(a.Network) == "" {
			return fmt.Errorf("asset %s 的 network(链) 必填 —— 错链会永久丢钱", as)
		}
		if !(a.Min < a.Opt && a.Opt < a.Max) {
			return fmt.Errorf("asset %s 需满足 min < opt < max, 收到 min=%.6f opt=%.6f max=%.6f", as, a.Min, a.Opt, a.Max)
		}
		if a.Min < 0 {
			return fmt.Errorf("asset %s 的 min 不能为负", as)
		}
	}
	return nil
}

// BuildPlanner turns the config + a UI/DB-sourced whitelist into a Planner.
func (c *Config) BuildPlanner(wl *Whitelist) *Planner {
	bands := make([]AssetBand, 0, len(c.Assets))
	nets := map[string]string{}
	dust := map[string]float64{}
	for _, a := range c.Assets {
		as := strings.ToUpper(a.Asset)
		bands = append(bands, AssetBand{Asset: as, Target: a.Opt, Min: a.Min, Max: a.Max})
		nets[as] = a.Network
		dust[as] = a.MinTransfer
	}
	return &Planner{
		Exec:        c.ExecExchange,
		Reservoir:   c.ReservoirExchange,
		Networks:    nets,
		Bands:       bands,
		Whitelist:   wl,
		MinTransfer: dust,
	}
}
