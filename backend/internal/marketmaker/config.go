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
	// RateLimit configures the client-side UID rate limiter + 429 breaker
	// (today: gate only, see gate_ratelimit.go). All-zero = built-in defaults,
	// which encode 台账 #84 的实测惩罚档。
	RateLimit RateLimitConfig `yaml:"rate_limit" json:"rate_limit"`
}

// RateLimitConfig 是 UID 级限流器 + 429 熔断的参数面。
//
// 它【必须可配】而不是写死:限流档位是交易所按成交率(FR)动态调的惩罚档,
// 台账 #84 实测当前是 10 请求/10 秒,但解限后会回到常规档、FR 再掉又会降回来。
// 写死就等于每次调档改一次代码、发一次版。零值 = 走 defaults(),即 #84 那一档。
type RateLimitConfig struct {
	// Requests/WindowMs 是【下单池】的窗口预算:默认 10 请求 / 10000 毫秒。
	// 出处是 Gate 公告 40657,它点名的端点【只有】POST /spot/orders 与
	// PATCH /spot/orders/{id};撤单和查询各有自己的池,见下面两项。
	// 字段名保持 requests/window_ms 不变 = 已在跑的 yaml/json 不用改。
	Requests int `yaml:"requests" json:"requests"`
	WindowMs int `yaml:"window_ms" json:"window_ms"`
	// CancelRequests 是【撤单池】每个窗口的名额(默认 500,即 50r/s)。
	// 官方基线是 5000r/s,这里刻意取得保守得多:撤单虽然不吃下单的 10 个名额,
	// 却进成交率公式的分母(公告 40657),撤太猛会把自己压进更深的惩罚档。
	// 500/窗口 相对引擎最坏需求(8 腿×10s=80)仍有 6 倍余量。
	CancelRequests int `yaml:"cancel_requests" json:"cancel_requests"`
	// QueryRequests 是【查询池】每个窗口的名额(默认 200,即 20r/s)。
	// 官方表格写的是 900r/s;取 200 是因为"查询走另一套池"这件事有据可查,
	// 而"另一套池到底多大"我没拿到能引的原文 —— 保守 45 倍,且引擎实际只要 80。
	QueryRequests int `yaml:"query_requests" json:"query_requests"`
	// ReservedCancel 是【查询池】里划给撤单路径专用的名额,报价类的读看不到它们。
	// (下单池不预留:今天没有 critical 类的下单路径,留了就是白扔报价预算。)
	// 见 gate_ratelimit.go 顶部"预留名额现在护的是哪一格"。默认 3。
	ReservedCancel int `yaml:"reserved_cancel" json:"reserved_cancel"`
	// MaxWaitMs 是撤单类请求为了等一个名额最多阻塞多久(默认 3000)。
	// 报价类【从不等待】,恒为 0:见 gate_ratelimit.go。
	MaxWaitMs int `yaml:"max_wait_ms" json:"max_wait_ms"`
	// MaxRetries/MaxBackoffMs 限住撤单类的 429 退避重试。退避必须有上限,
	// 否则一条撤不掉的撤单会把后面所有撤单堵在队里(默认 3 次 / 封顶 2000ms)。
	MaxRetries   int `yaml:"max_retries" json:"max_retries"`
	MaxBackoffMs int `yaml:"max_backoff_ms" json:"max_backoff_ms"`
	// BreakerFails 连续多少次 429 打开熔断(默认 5);BreakerCoolMs 是交易所没给
	// Retry-After 时的默认冷却(默认 30000)。
	//
	// 为什么不照抄 binance 的 2 分钟(binance.go:888):那是 IP 级封禁的量级,
	// gate 这里是 10 秒窗口的惩罚档,停 2 分钟等于自己把策略关掉。
	BreakerFails  int `yaml:"breaker_fails" json:"breaker_fails"`
	BreakerCoolMs int `yaml:"breaker_cool_ms" json:"breaker_cool_ms"`
}

// defaults 把零值字段填成台账 #84 那一档。逐字段填(而不是"全零才用默认"),
// 这样只想改窗口预算的人不必把 7 个字段全抄一遍。
func (c RateLimitConfig) defaults() RateLimitConfig {
	if c.Requests <= 0 {
		c.Requests = 10
	}
	if c.WindowMs <= 0 {
		c.WindowMs = 10000
	}
	if c.CancelRequests <= 0 {
		c.CancelRequests = 500
	}
	if c.QueryRequests <= 0 {
		c.QueryRequests = 200
	}
	if c.ReservedCancel < 0 {
		c.ReservedCancel = 0
	}
	if c.ReservedCancel == 0 {
		c.ReservedCancel = 3
	}
	// 预留不能吃光它所在的那个池(查询池),否则报价类的读恒为 0 名额 = 永远不报价。
	if c.ReservedCancel >= c.QueryRequests {
		c.ReservedCancel = c.QueryRequests - 1
	}
	if c.MaxWaitMs <= 0 {
		c.MaxWaitMs = 3000
	}
	if c.MaxRetries <= 0 {
		c.MaxRetries = 3
	}
	if c.MaxBackoffMs <= 0 {
		c.MaxBackoffMs = 2000
	}
	if c.BreakerFails <= 0 {
		c.BreakerFails = 5
	}
	if c.BreakerCoolMs <= 0 {
		c.BreakerCoolMs = 30000
	}
	return c
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
	// 裁决(2026-09-09,台账 #7 收口):【不切】,保持 "ref"。依据是 66,083 张【真实挂单】
	// (server.log 的 [mm-quote])join 949,534 条 mm-observe 重算的每腿 markout,
	// 窗口 09-05 17:56 → 09-08 17:55 UTC。全文见
	// state/strategy/quote-anchor-decision-2026-09-09.md。
	//
	// 上一版注释这里写的两件事按证据改掉:
	//   - "中位基差 +45.2bps / 中位价差 23.0bps(3572 样本)"不是行情数,它出自
	//     795a348 那 101 条【合成】单测样本。ONG_USDT 实测(237,462 条):中位基差
	//     +37.9bps、中位价差 14.8bps,|b|>s/2 占 88.5%。关系成立,数字得换。
	//   - "卖单扎进买盘被秒吃"不可能发生:rideToBook(engine.go:365-386)把 post-only
	//     严格钳在盘口内。实测 ONG 卖单中位落在 gate 卖一【内侧】1.0bps(58.7% 恰好
	//     1 tick);真正错位的是另一条腿 —— 买单中位挂在 gate 买一【下方】35.5bps,
	//     是太便宜而不成交,不是"挂在天上"。
	//
	// 为什么不切:锚点决定哪条腿能成交,决定不了这门生意成不成立。实测每腿 30s
	// markout 折算的保本 maker 费只有 4.5~15.5bps/腿(悲观/乐观两种成交判定夹逼),
	// 而 gate 现货 maker 是 20bps/腿 —— 4 个 symbol × 2 条腿扣费后【八条全负】,
	// 取乐观上界也一样。切锚补不上 25~39bps 的费率缺口,且实测会让 SOL 反而差 22~28bps。
	// 该修的是费率(转永续 maker −1~+2bps),不是锚。
	QuoteAnchor string `yaml:"quote_anchor" json:"quote_anchor"`

	// BasisHalfLifeS 打开【每 symbol 基差修正】(台账 #7 的修复形态,秒;0=关闭,默认):
	// 报价中心 = 参考所中价 ×(1 + b̂/1e4),b̂ 是 exec-vs-ref 基差的 EWMA。
	//
	// 它是 quote_anchor 的连续推广:半衰期→0 等价于锚执行所、→∞ 等价于锚参考所。
	// 好处是只抹掉基差里【持续】的那部分(gate 的价格水位),把零均值的那部分
	// 留给参考所解释 —— 即保住跨所价格发现。实测依据与回放结果见 basis.go 顶部。
	//
	// quote_anchor:"exec" 时本项被忽略(锚在执行所,基差按定义已是 0)。
	// 推荐起步值 60;档位最终该由 markout A/B 定,不该由这行注释定。
	BasisHalfLifeS float64 `yaml:"basis_half_life_s" json:"basis_half_life_s"`
	// BasisCapBps 限制修正项绝对值(bps),0 = 用默认 200。见 defaultBasisCapBps。
	BasisCapBps float64 `yaml:"basis_cap_bps" json:"basis_cap_bps"`

	// AllowShort 放开卖侧的"只能卖已持有库存"这条现货假设。默认 false =
	// 历史行为【逐位不变】:卖量恒等于 min(order_qty, 已持有量)。
	//
	// 要修的缺陷(2026-09-09 用单测量出来,不是推的,见 short_gate_test.go):
	// 引擎里 askQty 钳在 baseHeld 上,而永续适配器的 Balances() 返回的是【有符号持仓】。
	// 于是空仓的永续账户 baseHeld=0 → askQty=0 → wantAsk=false → 引擎在永续上
	// 【一张卖单都挂不出来】,只能先买成多头再卖回去 —— 一个只能做多的做市商。
	// hyperliquid.go:17-22 写着选永续就是为了"双边对称报价",这个目标在引擎层
	// 从来没落地过:SupportsShort() 全仓库零调用点。
	//
	// 为什么现在才要它:台账 #7 收口(quote-anchor-decision-2026-09-09.md)证明
	// gate 现货做市整条不成立 —— 保本 maker 费只有 4.5~15.5bps/腿而现货是 20bps/腿,
	// 八条腿扣费后全负。出路是转永续(maker 量级 −1~+2bps),而永续上"只能挂买单"
	// 是硬阻塞:做市本来就是两边都要挂。
	//
	// 它【不是打开就能用】的开关。开了以后还要同时满足两条,否则整个模块拒绝启动
	// 而不是静默退回单边(见 engine.go shortSideBlockers):
	//  1. 场馆 SupportsShort() == true;
	//  2. 适配器实现 PerpRiskControls —— 永续风控四件套,今天没有任何适配器实现。
	// 也就是说:本字段现在配上去只会让后端拒绝启动并报 ERROR。这是刻意的。
	AllowShort bool `yaml:"allow_short" json:"allow_short"`
}

// anchorMid 按配置选报价中心。参考所中价用于方向与风控,不一定用于定价。
func (p PairConfig) anchorMid(refMid, execMid float64) float64 {
	return p.basisAdjustedMid(refMid, execMid, 0)
}

// basisAdjustedMid 在 anchorMid 之上叠加基差修正 corrBps(bps)。
// corrBps=0 时与 anchorMid 逐位相同,所以未开启修正的路径行为完全不变。
func (p PairConfig) basisAdjustedMid(refMid, execMid, corrBps float64) float64 {
	if p.QuoteAnchor == "exec" && execMid > 0 {
		// 锚执行所时基差按定义为 0;再叠加修正等于把同一个量减两次。
		return execMid
	}
	return refMid * (1 + corrBps/10000)
}

func (p PairConfig) refresh() int {
	if p.RefreshMs <= 0 {
		return 1000
	}
	return p.RefreshMs
}
