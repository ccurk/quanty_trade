package marketmaker

// Config is the top-level market-making config. It ships BLANK — the owner fills
// credentials and pairs (loaded from yaml/env by the caller). Nothing runs unless
// Enabled is true, and even then ObserveOnly (the default posture) means the
// engine only MEASURES exec-vs-feed spread and never places an order — so you
// data-validate opportunity per exchange before enabling live quoting.
type Config struct {
	Enabled     bool         `yaml:"enabled" json:"enabled"`
	ObserveOnly bool         `yaml:"observe_only" json:"observe_only"`
	Feed        string       `yaml:"feed" json:"feed"` // feed source name, e.g. "binance"
	Exec        []ExecConfig `yaml:"exec" json:"exec"` // one entry per execution exchange
	Pairs       []PairConfig `yaml:"pairs" json:"pairs"`
	// MaxDailyLossUSD: 单日盯市亏损(USDT)达到此值即撤单并停报价至次日 UTC。0=不启用。
	MaxDailyLossUSD float64 `yaml:"max_daily_loss_usd" json:"max_daily_loss_usd"`
}

// ExecConfig holds one execution exchange's endpoint + credentials (OWNER fills).
// BaseURL may be left empty to use the adapter's built-in default.
type ExecConfig struct {
	Name      string `yaml:"name" json:"name"` // "coinsph" | "gate" | "kucoin" | ...
	BaseURL   string `yaml:"base_url" json:"base_url"`
	APIKey    string `yaml:"api_key" json:"api_key"`
	APISecret string `yaml:"api_secret" json:"api_secret"`
	// Passphrase is a 3rd credential some exchanges require (e.g. KuCoin). Empty otherwise.
	Passphrase string `yaml:"passphrase" json:"passphrase"`
	// WSTrade routes order place/cancel over the exchange's WebSocket trade API
	// where the adapter supports it (today: gate). Default false = REST only —
	// flipping this back off is the rollback path for the WS channel.
	WSTrade bool `yaml:"ws_trade" json:"ws_trade"`
}

// PairConfig defines one market-making pair: which feed symbol to reference, and
// where/how to quote it on which execution exchange.
type PairConfig struct {
	FeedSymbol  string  `yaml:"feed_symbol" json:"feed_symbol"`   // reference symbol on the feed, e.g. BTCUSDT
	Exec        string  `yaml:"exec" json:"exec"`                 // exec exchange name (matches ExecConfig.Name)
	ExecSymbol  string  `yaml:"exec_symbol" json:"exec_symbol"`   // symbol on the exec exchange
	SpreadBps   float64 `yaml:"spread_bps" json:"spread_bps"`     // half-spread each side around reference mid (bps)
	OrderQty    float64 `yaml:"order_qty" json:"order_qty"`       // base-asset size per quote
	MaxPosition float64 `yaml:"max_position" json:"max_position"` // inventory cap (base asset)
	RefreshMs   int     `yaml:"refresh_ms" json:"refresh_ms"`     // requote / observe cadence (default 1000)

	// QuoteAnchor 决定报价中心锚在哪:
	//   ""/"ref"  参考所(binance)中价 —— 历史默认,保持不变
	//   "exec"    执行所(gate)自身中价
	//
	// 为什么要能切:2026-09-08 实测 ONG_USDT 中位基差 +45.2bps 而中位价差仅 23.0bps
	// (3572 个样本)。锚在 binance 时,两条腿同时落在 gate 盘口的错误一侧 ——
	// 卖单扎进买盘被秒吃、买单挂在天上永不成交,结构性只卖不买。
	//
	// 但"改锚 exec"是策略变更不是 bug 修复:锚 exec 等于放弃跨所信息,退化成
	// 单所做市。哪个更好不该拍脑袋 —— markout 度量已上线,逐品种切换后用
	// 成交后 1s/5s/30s 的 markout 对比,让数据判。所以默认不变,显式配置才切。
	QuoteAnchor string `yaml:"quote_anchor" json:"quote_anchor"`
}

// anchorMid 按配置选报价中心。参考所中价用于方向与风控,不一定用于定价。
func (p PairConfig) anchorMid(refMid, execMid float64) float64 {
	if p.QuoteAnchor == "exec" && execMid > 0 {
		return execMid
	}
	return refMid
}

func (p PairConfig) refresh() int {
	if p.RefreshMs <= 0 {
		return 1000
	}
	return p.RefreshMs
}
