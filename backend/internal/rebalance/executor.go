package rebalance

import (
	"fmt"
	"time"
)

// executor.go is the ONLY place a transfer can be authorized. It sits behind every
// guard: a plan must be whitelisted (Executable), the mode must permit execution,
// the cooldown must have elapsed, and the amount must fit the single-transfer and
// daily USD caps. In dry-run it computes and returns the intended transfer WITHOUT
// calling the withdrawal function. The signed withdrawal call itself is injected
// (WithdrawFn) so this logic is fully unit-testable without moving real money.

// WithdrawFn submits a real withdrawal and returns the exchange's withdrawal id.
// The concrete implementations live in withdraw.go (gate / binance).
type WithdrawFn func(fromExchange, asset, network, address, memo string, amount float64) (txID string, err error)

// PriceUSDFn returns the USD value of one unit of asset, or 0 if unknown. Caps are
// denominated in USD, so an unknown price means "cannot verify the cap" → refuse.
type PriceUSDFn func(asset string) float64

// Executor applies the guards and (when permitted) performs a transfer.
type Executor struct {
	cfg      *Config
	withdraw WithdrawFn
	priceUSD PriceUSDFn
}

func NewExecutor(cfg *Config, withdraw WithdrawFn, priceUSD PriceUSDFn) *Executor {
	return &Executor{cfg: cfg, withdraw: withdraw, priceUSD: priceUSD}
}

// TransferResult is the outcome of trying to act on one plan.
type TransferResult struct {
	Plan     Plan    `json:"plan"`
	Executed bool    `json:"executed"` // a real withdrawal was submitted
	DryRun   bool    `json:"dry_run"`  // would have executed, but dry-run
	Amount   float64 `json:"amount"`   // possibly capped below plan.Amount
	TxID     string  `json:"tx_id"`
	Skipped  string  `json:"skipped"` // non-empty ⇒ not executed, this is why
	Error    string  `json:"error"`
}

// Execute applies all guards to one plan and, if permitted and not dry-run, submits
// the withdrawal. spentTodayUSD and lastTransferAt come from the persisted transfer
// log (so the caps survive restarts); now is injected for deterministic tests.
func (e *Executor) Execute(p Plan, spentTodayUSD float64, lastTransferAt, now time.Time) TransferResult {
	res := TransferResult{Plan: p, Amount: p.Amount}

	// 1. 必须白名单可解析(planner 已判定;这里再挡一次,纵深防御)。
	if !p.Executable() {
		res.Skipped = "目标地址不在白名单,拒绝"
		return res
	}
	// 2. recommend 模式永不执行。
	if e.cfg.Mode == ModeRecommend {
		res.Skipped = "recommend 模式:只建议不执行"
		return res
	}
	// 3. 冷却:同资产两次搬运的最小间隔(链上到账慢,防重复在途)。
	if cd := time.Duration(e.cfg.CooldownMinutes) * time.Minute; cd > 0 && !lastTransferAt.IsZero() {
		if elapsed := now.Sub(lastTransferAt); elapsed < cd {
			res.Skipped = fmt.Sprintf("冷却中(距上次 %s < %s)", elapsed.Truncate(time.Second), cd)
			return res
		}
	}
	// 4. 估值:USD 计价的上限校验必须有价格,估不出就拒绝(安全默认)。
	px := e.priceUSD(p.Asset)
	if px <= 0 {
		res.Skipped = fmt.Sprintf("%s 无法估值(USD),拒绝以免绕过上限", p.Asset)
		return res
	}
	amount := p.Amount
	// 5. 单笔上限:超了就压到上限(让自动搬运仍能推进,但绝不超限)。
	if maxUSD := e.cfg.MaxPerTransferUSD; maxUSD > 0 && amount*px > maxUSD {
		amount = maxUSD / px
	}
	// 6. 单日上限:压到今日剩余额度。
	if maxDay := e.cfg.MaxPerDayUSD; maxDay > 0 {
		remainUSD := maxDay - spentTodayUSD
		if remainUSD <= 0 {
			res.Skipped = fmt.Sprintf("已达单日上限(%.0f USD)", maxDay)
			return res
		}
		if amount*px > remainUSD {
			amount = remainUSD / px
		}
	}
	// 7. dust:压完后若低于最小搬运额,不值当一次提现费。
	if amount < e.cfg.minTransfer(p.Asset) {
		res.Skipped = fmt.Sprintf("压限后 %.6f < 最小搬运额,跳过", amount)
		return res
	}
	res.Amount = amount

	// 8. dry-run:算到这里就停,不真提。
	if e.cfg.DryRun {
		res.DryRun = true
		return res
	}
	// 9. 真提现(唯一动钱处)。
	txID, err := e.withdraw(p.FromExchange, p.Asset, p.Network, p.ToAddress, p.Memo, amount)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Executed = true
	res.TxID = txID
	return res
}
