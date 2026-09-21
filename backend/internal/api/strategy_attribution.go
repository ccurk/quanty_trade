package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"quanty_trade/internal/database"
	"quanty_trade/internal/logger"
	"quanty_trade/internal/models"
	"quanty_trade/internal/strategy"

	"github.com/gin-gonic/gin"
)

// recordParamVersion opens (or confirms) the strategy's parameter version after
// a config write and returns the label for the HTTP response. It NEVER fails the
// request: the config is already saved, and an attribution row is bookkeeping.
func recordParamVersion(c *gin.Context, strategyID, source string) string {
	actor := ""
	if u, _ := c.Get("username"); u != nil {
		if s, ok := u.(string); ok {
			actor = s
		}
	}
	v, err := strategy.EnsureParamVersion(strategyID, source, actor)
	if err != nil {
		logger.Errorf("[PARAM_VERSION] strategy=%s source=%s err=%v", strategyID, source, err)
		return ""
	}
	if v == nil {
		return ""
	}
	return v.Label
}

// AttributionRow is one (strategy, parameter version) bucket of realized results.
type AttributionRow struct {
	StrategyID   string `json:"strategy_id"`
	StrategyName string `json:"strategy_name"`

	// ParamVersionID is 0 for the "unversioned" bucket: closed positions that
	// carry no stamp AND whose OpenTime falls into no version window (i.e. they
	// predate the first recorded version of that strategy).
	ParamVersionID uint   `json:"param_version_id"`
	VersionLabel   string `json:"version_label"`
	VersionNote    string `json:"version_note,omitempty"`
	ConfigHash     string `json:"config_hash,omitempty"`
	ChangedJSON    string `json:"changed_json,omitempty"`

	EffectiveFrom *time.Time `json:"effective_from,omitempty"`
	EffectiveTo   *time.Time `json:"effective_to,omitempty"`

	// Trades is the number of CLOSED positions attributed to this bucket.
	Trades int64 `json:"trades"`
	Wins   int64 `json:"wins"`
	Losses int64 `json:"losses"`
	// Fails is the 「仓位损失」count: positions that closed at or below the failure
	// line, INCLUDING the ones that closed at a small profit. It is an independent
	// third number, not a subset of Losses — see the 口径 note above attributionSQL.
	Fails int64 `json:"fails"`

	// GrossPnL is realized PnL BEFORE fees. This is the number the Telegram card
	// labels 已实现收益（毛）and the number the position ledger stores; it is not
	// comparable with a backtest, which deducts taker fee.
	GrossPnL    float64 `json:"gross_pnl"`
	GrossProfit float64 `json:"gross_profit"`
	GrossLoss   float64 `json:"gross_loss"`

	// FeeUSDT is the commission actually charged by the exchange on this
	// bucket's orders, summed over USDT-denominated fills only. Fills paid in
	// another asset are NOT converted here — the rate is a query-time decision,
	// so they are excluded from the sum and counted in FeeOtherAssetFills so the
	// gap is visible rather than silently folded in at a guessed rate.
	FeeUSDT float64 `json:"fee_usdt"`
	// FeeFills is how many exchange fills backed FeeUSDT. It is the coverage
	// indicator: fee is meaningless without knowing how much of the bucket it
	// covers, and 0 here means "not measured", not "no fee was paid".
	FeeFills           int64 `json:"fee_fills"`
	FeeOtherAssetFills int64 `json:"fee_other_asset_fills"`
	MakerFills         int64 `json:"maker_fills"`

	// NetPnL is GrossPnL - FeeUSDT, computed here and never stored. Persisting a
	// net column would create a second source of truth that silently goes stale
	// the moment a fee is corrected or a non-USDT fill becomes convertible.
	NetPnL float64 `json:"net_pnl"`
	// Notional is the accumulated entry notional, the denominator for ReturnPct.
	Notional float64 `json:"notional"`
	// AvgPnL / NetAvgPnL are the per-trade expectancy — the number "did this
	// change help?" actually turns on. Total alone is confounded by trade count.
	// NetAvgPnL is the one to judge on: gross expectancy rewards raising turnover
	// even when every extra trade loses money after fees.
	AvgPnL    float64 `json:"avg_pnl"`
	NetAvgPnL float64 `json:"net_avg_pnl"`
	WinRate   float64 `json:"win_rate"`
	// FailRate shares WinRate's denominator (Trades), so the two are directly
	// comparable: FailRate - (1 - WinRate) is exactly the share of trades that
	// closed positive but did not clear the failure line.
	FailRate  float64 `json:"fail_rate"`
	ReturnPct float64 `json:"return_pct"`
}

type AttributionResponse struct {
	// AttributedBy documents the join rule, so nobody has to guess whether a
	// trade counts against the params that opened it or the params live at close.
	AttributedBy string `json:"attributed_by"`
	// Scope is the literal WHERE clause behind every number in Rows. It ships
	// with the payload on purpose: the last headline figure this system produced
	// was repeated four times without anyone recording which filter produced it,
	// and nobody could tell afterwards whether it was gross or net.
	Scope string     `json:"scope"`
	From  *time.Time `json:"from,omitempty"`
	To    *time.Time `json:"to,omitempty"`
	// FailureThresholdUSDT is the line every row's `fails` used. It ships with the
	// payload because `fails` is unreadable without it, and it is NOT always the
	// package default: a query scoped to one strategy uses that strategy's own
	// override (config key failure_pnl_threshold_usdt).
	FailureThresholdUSDT float64          `json:"failure_threshold_usdt"`
	Rows                 []AttributionRow `json:"rows"`
}

// attributionSQL buckets every closed position by parameter version and attaches
// the fee actually charged by the exchange.
//
// Version resolution is two-tier and that is the whole point of the design:
//  1. param_version_id stamped on the row at open time — authoritative, and
//     immune to anyone later editing a version's effective window;
//  2. NULL stamp (rows written before the column existed, or opened by the
//     exchange-adoption / reconcile paths) falls back to matching open_time into
//     [effective_from, effective_to). Without this the whole history reads as one
//     undifferentiated blob and the table would be useless on day one.
//
// Positions are attributed by OPEN time, not close time: the parameters that
// were live when the trade was entered are the ones being judged.
//
// 口径 (every number reported must carry its WHERE clause):
// closed positions are `status='closed' AND closed_qty>0`. The closed_qty filter
// is NOT cosmetic — roughly three quarters of rows marked 'closed' carry
// closed_qty=0 and are empty shells, so counting them would inflate the trade
// count several fold and divide expectancy down by the same factor.
//
// 「仓位损失」(fails) 是**独立于** wins/losses 的第三个数，不是它们的子集调整:
// pnl <= 阈值(默认 0.5U，见 strategy/failure_threshold.go) 即失败，含不赚钱的与只赚了
// 0.几U 的。亏损单 wins 不进、losses 进、fails 也进；+0.30U 的单旧口径进 wins，
// 新口径进 fails。金额列(gross_pn_l/gross_profit/gross_loss)一律仍是**符号**口径 ——
// 把 +0.30U 塞进 gross_loss 会让「亏损总额」变成假的，所以钱一分没动。
//
// 阈值是绑进来的标量(`?`)，不是从 config 里读的:这条 SQL 要在 SQLite(测试)与 MySQL
// 上同样能跑，用不了 JSON_EXTRACT 这类库专有函数；而 GROUP BY 里一个 `?` 只能绑一个值，
// 跨策略聚合时无法逐组取各自的覆盖值。所以处理函数在 Go 侧解析:查单策略时用该策略的
// 覆盖值，跨策略时用包默认值，并把实际用的线回写在响应的 failure_threshold_usdt 里。
//
// WHY FEE IS JOINED THROUGH ORDERS AND NOT THROUGH position_id:
// the obvious join, strategy_orders.position_id -> strategy_positions.id, is a
// trap here. Measured on the live database: of the filled orders, every single
// `purpose='entry'` row has position_id = 0 (the open leg is never linked back
// to its position), and only about half of closed positions have even one linked
// filled order. Attributing fee that way would silently capture close-leg fees
// for half the positions — roughly a quarter of the true round-trip cost — and
// present it as the whole fee. A fee that is quietly 4x too small is worse than
// no fee column at all, because it looks answered. So fee is aggregated on the
// order ledger itself, which carries strategy_id and param_version_id directly
// and joins to fills on exchange_order_id with full coverage.
const attributionSQL = `
SELECT
    t.strategy_id                                                AS strategy_id,
    MAX(t.strategy_name)                                         AS strategy_name,
    COALESCE(t.version_id, 0)                                    AS param_version_id,
    COUNT(*)                                                     AS trades,
    SUM(CASE WHEN t.realized_pn_l > 0 THEN 1 ELSE 0 END)         AS wins,
    SUM(CASE WHEN t.realized_pn_l < 0 THEN 1 ELSE 0 END)         AS losses,
    SUM(CASE WHEN t.realized_pn_l <= ? THEN 1 ELSE 0 END)        AS fails,
    COALESCE(SUM(t.realized_pn_l), 0)                            AS gross_pn_l,
    COALESCE(SUM(CASE WHEN t.realized_pn_l > 0 THEN t.realized_pn_l ELSE 0 END), 0) AS gross_profit,
    COALESCE(SUM(CASE WHEN t.realized_pn_l < 0 THEN t.realized_pn_l ELSE 0 END), 0) AS gross_loss,
    COALESCE(SUM(t.realized_notional), 0)                        AS notional,
    COALESCE(MAX(f.fee_usdt), 0)                                 AS fee_usdt,
    COALESCE(MAX(f.fee_fills), 0)                                AS fee_fills,
    COALESCE(MAX(f.fee_other_fills), 0)                          AS fee_other_asset_fills,
    COALESCE(MAX(f.maker_fills), 0)                              AS maker_fills
FROM (
    SELECT
        p.strategy_id,
        p.strategy_name,
        p.realized_pn_l,
        p.realized_notional,
        COALESCE(p.param_version_id, (
            SELECT v.id FROM strategy_param_versions v
            WHERE v.strategy_id = p.strategy_id
              AND v.effective_from <= p.open_time
              AND (v.effective_to IS NULL OR v.effective_to > p.open_time)
            ORDER BY v.effective_from DESC
            LIMIT 1
        )) AS version_id
    FROM strategy_positions p
    WHERE p.owner_id = ?
      AND p.status = 'closed'
      AND p.closed_qty > 0
      AND (? = 0 OR p.close_time >= ?)
      AND (? = 0 OR p.close_time < ?)
      AND (? = '' OR p.strategy_id = ?)
) t
LEFT JOIN (
    SELECT
        o.strategy_id AS strategy_id,
        COALESCE(o.param_version_id, (
            SELECT v.id FROM strategy_param_versions v
            WHERE v.strategy_id = o.strategy_id
              AND v.effective_from <= o.requested_at
              AND (v.effective_to IS NULL OR v.effective_to > o.requested_at)
            ORDER BY v.effective_from DESC
            LIMIT 1
        ), 0) AS version_id,
        SUM(CASE WHEN x.commission_asset = 'USDT' THEN x.commission ELSE 0 END) AS fee_usdt,
        COUNT(*)                                                                AS fee_fills,
        SUM(CASE WHEN x.commission_asset <> 'USDT' AND x.commission > 0
                 THEN 1 ELSE 0 END)                                             AS fee_other_fills,
        SUM(CASE WHEN x.is_maker THEN 1 ELSE 0 END)                             AS maker_fills
    FROM exchange_fills x
    JOIN strategy_orders o ON o.exchange_order_id = x.order_id
    WHERE o.owner_id = ?
      AND (? = 0 OR x.trade_time >= ?)
      AND (? = 0 OR x.trade_time < ?)
      AND (? = '' OR o.strategy_id = ?)
    GROUP BY o.strategy_id, version_id
) f
    ON f.strategy_id = t.strategy_id
   AND f.version_id  = COALESCE(t.version_id, 0)
GROUP BY t.strategy_id, COALESCE(t.version_id, 0)
ORDER BY t.strategy_id, COALESCE(t.version_id, 0)
`

// GetStrategyAttribution answers "which change actually raised expectancy?".
// GET /stats/strategy-attribution?from=&to=&strategy_id=
//
// from/to are RFC3339 or YYYY-MM-DD and filter on position CLOSE time (that is
// when the PnL is booked); the version bucket is still decided by OPEN time.
func GetStrategyAttribution(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid := userID.(uint)

	from, err := parseAttributionTime(c.Query("from"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid from: " + err.Error()})
		return
	}
	to, err := parseAttributionTime(c.Query("to"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid to: " + err.Error()})
		return
	}
	strategyID := strings.TrimSpace(c.Query("strategy_id"))

	fromFlag, toFlag := 0, 0
	fromVal, toVal := time.Time{}, time.Time{}
	if from != nil {
		fromFlag, fromVal = 1, *from
	}
	if to != nil {
		toFlag, toVal = 1, *to
	}

	// 「仓位损失」线。查单策略时用该策略的覆盖值；跨策略聚合时用包默认值 ——
	// 理由见 attributionSQL 上方那段注释（SQL 要同时跑 SQLite/MySQL，且一个 `?`
	// 只能绑一个标量，做不到逐组取各自的值）。实际用了哪条线回写在响应里。
	failThreshold := strategy.FailurePnLThresholdUSDT
	if strategyID != "" {
		var inst models.StrategyInstance
		if err := database.DB.Where("id = ? AND owner_id = ?", strategyID, uid).First(&inst).Error; err == nil {
			var instCfg map[string]interface{}
			if strings.TrimSpace(inst.Config) != "" {
				_ = json.Unmarshal([]byte(inst.Config), &instCfg)
			}
			failThreshold = strategy.FailureThresholdUSDT(instCfg)
		}
	}

	var raw []struct {
		StrategyID         string
		StrategyName       string
		ParamVersionID     uint
		Trades             int64
		Wins               int64
		Losses             int64
		Fails              int64
		GrossPnL           float64 `gorm:"column:gross_pn_l"`
		GrossProfit        float64
		GrossLoss          float64
		Notional           float64
		FeeUSDT            float64 `gorm:"column:fee_usdt"`
		FeeFills           int64
		FeeOtherAssetFills int64
		MakerFills         int64
	}
	if err := database.DB.Raw(attributionSQL,
		failThreshold,
		uid,
		fromFlag, fromVal,
		toFlag, toVal,
		strategyID, strategyID,
		uid,
		fromFlag, fromVal,
		toFlag, toVal,
		strategyID, strategyID,
	).Scan(&raw).Error; err != nil {
		logger.Errorf("[ATTRIBUTION] query failed owner=%d err=%v", uid, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Second pass for the version metadata — a LEFT JOIN inside the aggregate
	// would have to survive both MySQL and SQLite; two plain queries do not.
	ids := make([]uint, 0, len(raw))
	for _, r := range raw {
		if r.ParamVersionID > 0 {
			ids = append(ids, r.ParamVersionID)
		}
	}
	versions := map[uint]models.StrategyParamVersion{}
	if len(ids) > 0 {
		var vs []models.StrategyParamVersion
		if err := database.DB.Where("id IN ?", ids).Find(&vs).Error; err == nil {
			for _, v := range vs {
				versions[v.ID] = v
			}
		}
	}

	rows := make([]AttributionRow, 0, len(raw))
	for _, r := range raw {
		row := AttributionRow{
			StrategyID:         r.StrategyID,
			StrategyName:       r.StrategyName,
			ParamVersionID:     r.ParamVersionID,
			VersionLabel:       "unversioned",
			Trades:             r.Trades,
			Wins:               r.Wins,
			Losses:             r.Losses,
			Fails:              r.Fails,
			GrossPnL:           r.GrossPnL,
			GrossProfit:        r.GrossProfit,
			GrossLoss:          r.GrossLoss,
			Notional:           r.Notional,
			FeeUSDT:            r.FeeUSDT,
			FeeFills:           r.FeeFills,
			FeeOtherAssetFills: r.FeeOtherAssetFills,
			MakerFills:         r.MakerFills,
			NetPnL:             r.GrossPnL - r.FeeUSDT,
		}
		if v, ok := versions[r.ParamVersionID]; ok {
			row.VersionLabel = v.Label
			row.VersionNote = v.Note
			row.ConfigHash = v.ConfigHash
			row.ChangedJSON = v.ChangedJSON
			ef := v.EffectiveFrom
			row.EffectiveFrom = &ef
			row.EffectiveTo = v.EffectiveTo
		}
		if r.Trades > 0 {
			row.AvgPnL = r.GrossPnL / float64(r.Trades)
			row.NetAvgPnL = row.NetPnL / float64(r.Trades)
			row.WinRate = float64(r.Wins) / float64(r.Trades)
			row.FailRate = float64(r.Fails) / float64(r.Trades)
		}
		if r.Notional > 0 {
			row.ReturnPct = row.NetPnL / r.Notional
		}
		rows = append(rows, row)
	}

	c.JSON(http.StatusOK, AttributionResponse{
		AttributedBy: "position.open_time -> param version window (stamped param_version_id wins when present); window filter applies to close_time",
		Scope: "positions: status='closed' AND closed_qty>0 AND owner_id=<caller>; " +
			"fee: SUM(exchange_fills.commission WHERE commission_asset='USDT') joined via " +
			"strategy_orders.exchange_order_id, bucketed by the order's own param version; " +
			"net_pnl = gross_pnl - fee_usdt, derived here and never stored; " +
			"fails = COUNT(realized_pn_l <= failure_threshold_usdt) — an independent third " +
			"count, wins/losses and every money column stay sign-based",
		From:                 from,
		To:                   to,
		FailureThresholdUSDT: failThreshold,
		Rows:                 rows,
	})
}

// ListStrategyParamVersions returns one strategy's parameter-version history,
// newest first. GET /strategies/:id/param-versions
func ListStrategyParamVersions(c *gin.Context) {
	id := c.Param("id")
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")

	var instance models.StrategyInstance
	if err := database.DB.Where("id = ?", id).First(&instance).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Strategy not found"})
		return
	}
	if instance.OwnerID != userID.(uint) && userRole != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	var rows []models.StrategyParamVersion
	if err := database.DB.Where("strategy_id = ?", id).
		Order("effective_from desc, id desc").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"versions": rows})
}

// AnnotateParamVersionRequest carries the two owner-supplied fields. Everything
// else on a version row is written by the platform and is not editable here —
// rewriting a hash or a window would silently re-attribute booked trades.
type AnnotateParamVersionRequest struct {
	Label string `json:"label"`
	Note  string `json:"note"`
}

// AnnotateStrategyParamVersion sets the human label/note on one version.
// PATCH /strategies/:id/param-versions/:vid
func AnnotateStrategyParamVersion(c *gin.Context) {
	id := c.Param("id")
	vid := c.Param("vid")
	userID, _ := c.Get("user_id")
	userRole, _ := c.Get("role")

	var req AnnotateParamVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	req.Note = strings.TrimSpace(req.Note)
	if req.Label == "" && req.Note == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "label 和 note 至少填一个"})
		return
	}
	if len(req.Label) > 64 || len(req.Note) > 512 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "label 最长 64 字符，note 最长 512 字符"})
		return
	}

	var row models.StrategyParamVersion
	if err := database.DB.Where("id = ? AND strategy_id = ?", vid, id).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "param version not found"})
		return
	}
	if row.OwnerID != userID.(uint) && userRole != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Permission denied"})
		return
	}

	upd := map[string]interface{}{"updated_at": time.Now()}
	if req.Label != "" {
		upd["label"] = req.Label
	}
	if req.Note != "" {
		upd["note"] = req.Note
	}
	if err := database.DB.Model(&models.StrategyParamVersion{}).Where("id = ?", row.ID).Updates(upd).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "annotated", "id": row.ID})
}

func parseAttributionTime(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return &t, nil
		}
	}
	return nil, errors.New("expected RFC3339, 'YYYY-MM-DD HH:MM:SS' or 'YYYY-MM-DD'")
}
