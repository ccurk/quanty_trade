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
}

func (p PairConfig) refresh() int {
	if p.RefreshMs <= 0 {
		return 1000
	}
	return p.RefreshMs
}
