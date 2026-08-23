package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func GenerateUUID() string {
	return uuid.New().String()
}

// UserRole represents an authorization role in the system.
type UserRole string

const (
	RoleAdmin UserRole = "admin"
	RoleUser  UserRole = "user"
)

type User struct {
	// ID is the auto-increment primary key.
	ID uint `gorm:"primaryKey" json:"id"`
	// Username is the unique login name.
	Username string `gorm:"unique;not null" json:"username"`
	// Password stores the bcrypt hash; never serialized to clients.
	Password string `gorm:"not null" json:"-"`
	// Role determines permissions (admin/user).
	Role UserRole `gorm:"default:'user'" json:"role"`
	// Configs stores exchange credentials and user-level integration settings.
	// It is stored as a JSON string and never exposed via API responses.
	Configs string `gorm:"type:text" json:"-"`
	// CreatedAt is the row creation time.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the last update time.
	UpdatedAt time.Time `json:"updated_at"`
	// DeletedAt enables soft-deletion.
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// StrategyTemplate is a reusable Python strategy source/template.
type StrategyTemplate struct {
	// ID is the auto-increment primary key.
	ID uint `gorm:"primaryKey" json:"id"`
	// Name is the unique template name.
	Name string `gorm:"unique;not null" json:"name"`
	// Description is a human-readable summary shown in UI.
	Description string `json:"description"`
	// Path is the Python file path (absolute or relative, depending on deployment).
	Path         string `gorm:"not null" json:"path"`
	TemplateType string `gorm:"default:'strategy'" json:"template_type"`
	// AuthorID references the user who created/published this template.
	AuthorID uint `json:"author_id"`
	// Author is the joined author record (used in list endpoints).
	Author User `gorm:"foreignKey:AuthorID" json:"author"`
	// IsPublic controls whether the template appears in the public square.
	IsPublic bool `gorm:"default:false" json:"is_public"`
	// IsDraft indicates a work-in-progress template.
	IsDraft bool `gorm:"default:false" json:"is_draft"`
	// IsEnabled allows admins/authors to disable a template without deleting it.
	IsEnabled bool `gorm:"default:true" json:"is_enabled"`
	// Code stores the template source for in-browser editing.
	Code string `gorm:"type:mediumtext" json:"code"`
	// CreatedAt is the row creation time.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the last update time.
	UpdatedAt time.Time `json:"updated_at"`
	// DeletedAt enables soft-deletion.
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// StrategyInstance is a user-owned deployed strategy created from a template.
type StrategyInstance struct {
	// ID is a UUID string used as primary key (stable across restarts).
	ID string `gorm:"primaryKey" json:"id"`
	// Name is the user-facing instance name.
	Name string `json:"name"`
	// TemplateID links to the template used to create this instance.
	TemplateID uint `json:"template_id"`
	// Template is the joined template record.
	Template StrategyTemplate `gorm:"foreignKey:TemplateID" json:"template"`
	// StrategyVersionID points to the immutable version currently bound to this instance.
	StrategyVersionID *uint `gorm:"index" json:"strategy_version_id,omitempty"`
	// StrategyVersion is the joined bound version record.
	StrategyVersion *StrategyVersion `gorm:"foreignKey:StrategyVersionID" json:"strategy_version,omitempty"`
	// OwnerID is the user who owns this instance.
	OwnerID uint `json:"owner_id"`
	// Owner is the joined owner record.
	Owner User `gorm:"foreignKey:OwnerID" json:"owner"`
	// Config is the strategy runtime config JSON string (e.g. symbol, risk params).
	Config string `json:"config"`
	// Status is the runtime state reported by the manager (running/stopped/error).
	Status string `json:"status"`
	// CreatedAt is the row creation time.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the last update time.
	UpdatedAt time.Time `json:"updated_at"`
}

// StrategyLog stores user-visible logs emitted by the running Python strategy.
// 复合索引 (strategy_id, created_at) 服务 GetStrategyLogs 的
// WHERE strategy_id=? ORDER BY created_at DESC LIMIT 100——表在 debug 档以
// ~15 行/秒增长，缺该索引时这条查询要对单策略全部行做 filesort（实测 33s）。
// created_at 单列索引服务保留清扫（log_retention_job）的过期行定位。
type StrategyLog struct {
	// ID is the auto-increment primary key.
	ID uint `gorm:"primaryKey" json:"id"`
	// StrategyID is the StrategyInstance ID.
	StrategyID string `gorm:"index;index:idx_strategy_logs_sid_created,priority:1" json:"strategy_id"`
	// Level indicates severity (info/error).
	Level string `json:"level"`
	// Message is the raw log message string.
	Message string `json:"message"`
	// CreatedAt is the log creation time.
	CreatedAt time.Time `gorm:"index;index:idx_strategy_logs_sid_created,priority:2" json:"created_at"`
}

// Backtest records a historical simulation run for a strategy instance.
type Backtest struct {
	// ID is the auto-increment primary key.
	ID uint `gorm:"primaryKey" json:"id"`
	// StrategyID is the StrategyInstance ID.
	StrategyID string `gorm:"index" json:"strategy_id"`
	// StartTime and EndTime define the simulation window.
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	// InitialBalance is the starting cash balance for simulation (e.g. USDT).
	InitialBalance float64 `json:"initial_balance"`
	// FinalBalance is the ending simulated equity/cash (implementation-dependent).
	FinalBalance float64 `json:"final_balance"`
	// TotalTrades is the number of executed trades in the simulation.
	TotalTrades int `json:"total_trades"`
	// TotalProfit is FinalBalance - InitialBalance.
	TotalProfit float64 `json:"total_profit"`
	// ReturnRate is profit percentage of InitialBalance.
	ReturnRate float64 `json:"return_rate"`
	// Status tracks lifecycle: pending/running/completed/failed.
	Status string `json:"status"`
	// Symbol is the market the simulation ran on. Auto-symbol strategies have no
	// config symbol, so the request supplies one and it is recorded here.
	Symbol string `json:"symbol"`
	// Timeframe is the candle interval fed to the strategy (default 1m, the same
	// interval the live engine pushes).
	Timeframe string `json:"timeframe"`
	// Overrides records the simulation-only config_overrides the run used, so
	// A/B experiment rows are self-describing.
	Overrides string `gorm:"type:text" json:"overrides"`
	// Error preserves the failure reason. Failures used to be broadcast on the
	// websocket only, leaving failed rows with an empty Result and no way to
	// diagnose them afterwards through the REST API.
	Error string `gorm:"type:text" json:"error"`
	// Result stores the full JSON payload of the backtest result (equity curve, etc).
	Result string `gorm:"type:text" json:"result"`
	// UserID is the owner who initiated the backtest.
	UserID uint `gorm:"index" json:"user_id"`
	// CreatedAt is the backtest request time.
	CreatedAt time.Time `json:"created_at"`
}

// APILog records every API request for audit/debug purposes.
type APILog struct {
	// ID is the auto-increment primary key.
	ID uint `gorm:"primaryKey" json:"id"`
	// Method is the HTTP method (GET/POST/...).
	Method string `json:"method"`
	// Path is the request URL path.
	Path string `json:"path"`
	// StatusCode is the HTTP response status code.
	StatusCode int `json:"status_code"`
	// Latency is request duration in nanoseconds.
	Latency int64 `json:"latency"`
	// ClientIP is the request IP as seen by Gin.
	ClientIP string `json:"client_ip"`
	// UserID is the authenticated user (0 if unauthenticated).
	UserID uint `json:"user_id"`
	// Username is denormalized for easier querying.
	Username string `json:"username"`
	TraceID  string `gorm:"index" json:"trace_id"`
	// CreatedAt is the request timestamp. Indexed for retention pruning
	// (log_retention_job) —— 每请求一行，无索引时过期行定位退化为全表扫。
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

// ExchangeOrderEvent is an append-only raw stream of exchange order events.
// For Binance this is populated from User Data Stream "executionReport".
type ExchangeOrderEvent struct {
	// ID is the auto-increment primary key.
	ID uint `gorm:"primaryKey" json:"id"`
	// OwnerID is the user owning the exchange account.
	OwnerID uint `gorm:"index" json:"owner_id"`
	// Exchange is the exchange name, e.g. "Binance".
	Exchange string `gorm:"index" json:"exchange"`
	// Symbol is the exchange symbol, e.g. BTCUSDT.
	Symbol string `gorm:"index" json:"symbol"`
	// OrderID is the exchange-generated order id.
	OrderID string `gorm:"index" json:"order_id"`
	// ClientOrderID is the client id used to correlate local orders and events.
	ClientOrderID string `gorm:"index" json:"client_order_id"`
	// Side is buy/sell.
	Side string `json:"side"`
	// OrderType is market/limit/... (exchange-specific values).
	OrderType string `json:"order_type"`
	// Status is the exchange order status (new/partially_filled/filled/canceled/...).
	Status string `gorm:"index" json:"status"`
	// Price is the order price (0 for market orders in some events).
	Price float64 `json:"price"`
	// OrigQty is the order original quantity.
	OrigQty float64 `json:"orig_qty"`
	// ExecutedQty is cumulative filled quantity.
	ExecutedQty float64 `json:"executed_qty"`
	// LastQty is the last filled quantity of this event.
	LastQty float64 `json:"last_qty"`
	// LastPrice is the last fill price of this event.
	LastPrice float64 `json:"last_price"`
	// EventTime is the exchange event timestamp.
	EventTime time.Time `gorm:"index" json:"event_time"`
	// Raw is the raw JSON payload for auditing and future parsing.
	Raw string `gorm:"type:text" json:"raw"`
	// CreatedAt is the time we stored this event.
	CreatedAt time.Time `json:"created_at"`
}

// StrategyOrder is the platform-level order ledger for one strategy instance.
// It is created when a strategy requests an order and is updated as exchange
// events arrive (execution report).
type StrategyOrder struct {
	// ID is the auto-increment primary key.
	ID         uint `gorm:"primaryKey" json:"id"`
	PositionID uint `gorm:"index" json:"position_id"`
	// StrategyID identifies the strategy instance that created this order.
	StrategyID string `gorm:"type:varchar(64);index" json:"strategy_id"`
	// StrategyName is denormalized for UI display and debugging.
	StrategyName string `gorm:"type:varchar(128)" json:"strategy_name"`
	// OwnerID identifies the user who owns the strategy/exchange account.
	OwnerID uint `gorm:"index" json:"owner_id"`
	// Exchange is the exchange name, e.g. "Binance".
	Exchange string `gorm:"type:varchar(32);index" json:"exchange"`
	// Symbol is the trading symbol, normalized for display (e.g. BTC/USDT).
	Symbol string `gorm:"type:varchar(64);index" json:"symbol"`
	// Side is buy/sell (normalized to lower-case).
	Side    string `gorm:"type:varchar(8)" json:"side"`
	Purpose string `gorm:"type:varchar(32);index" json:"purpose"`
	// OrderType is market/limit/... (platform normalized values).
	OrderType           string `gorm:"type:varchar(32)" json:"order_type"`
	ParentClientOrderID string `gorm:"type:varchar(64);index" json:"parent_client_order_id"`
	// ClientOrderID is generated by platform and used for correlation.
	ClientOrderID string `gorm:"type:varchar(64);uniqueIndex" json:"client_order_id"`
	// ExchangeOrderID is the exchange-generated id once accepted.
	ExchangeOrderID string  `gorm:"type:varchar(64);index" json:"exchange_order_id"`
	IsAlgo          bool    `gorm:"index" json:"is_algo"`
	TriggerPrice    float64 `json:"trigger_price"`
	// Status is the current order state in platform terms.
	Status string `gorm:"type:varchar(32);index" json:"status"`
	// RequestedQty is the requested order quantity.
	RequestedQty float64 `json:"requested_qty"`
	// Price is the requested price (0 for market).
	Price float64 `json:"price"`
	// ExecutedQty is the filled quantity so far.
	ExecutedQty float64 `json:"executed_qty"`
	// AvgPrice is the volume-weighted average fill price.
	AvgPrice float64 `json:"avg_price"`
	// RequestedAt is when the strategy requested this order.
	RequestedAt time.Time `json:"requested_at"`
	// UpdatedAt is when we last updated this row.
	UpdatedAt time.Time `json:"updated_at"`
}

// StrategyPosition is the platform-level position ledger per strategy instance.
// A position is opened by filled buy orders and closed by filled sell orders.
type StrategyPosition struct {
	// ID is the auto-increment primary key.
	ID uint `gorm:"primaryKey" json:"id"`
	// StrategyID identifies the strategy instance that owns this position.
	StrategyID string `gorm:"type:varchar(64);index" json:"strategy_id"`
	// StrategyName is denormalized for UI display.
	StrategyName string `gorm:"type:varchar(128)" json:"strategy_name"`
	// OwnerID identifies the user who owns the strategy/exchange account.
	OwnerID uint `gorm:"index" json:"owner_id"`
	// Exchange is the exchange name, e.g. "Binance".
	Exchange string `gorm:"type:varchar(32);index" json:"exchange"`
	// Symbol is the trading symbol for this position (e.g. BTC/USDT).
	Symbol string `gorm:"type:varchar(64);index" json:"symbol"`
	// Direction is long/short for futures or long for spot.
	Direction string `gorm:"type:varchar(16);index" json:"direction"`
	// Amount is the current position size in base asset.
	Amount float64 `json:"amount"`
	// AvgPrice is the volume-weighted average entry price.
	AvgPrice   float64 `json:"avg_price"`
	TakeProfit float64 `json:"take_profit"`
	StopLoss   float64 `json:"stop_loss"`
	// AtrAbs is the absolute ATR captured at entry, used by exit engineering
	// (breakeven/trailing). 0 when unknown (falls back to |entry-sl|/atr_sl_mult).
	AtrAbs float64 `json:"atr_abs"`
	// BreakevenMoved marks that the stop has already been moved to breakeven, so
	// the exit engine only moves it once per position (durable across restarts).
	BreakevenMoved bool `json:"breakeven_moved"`
	// ClosedQty is the cumulative closed quantity across partial closes.
	ClosedQty float64 `json:"closed_qty"`
	// AvgClosePrice is the volume-weighted average close price.
	AvgClosePrice float64 `json:"avg_close_price"`
	// RealizedPnL is the realized profit/loss accumulated on closes.
	RealizedPnL float64 `json:"realized_pnl"`
	// RealizedNotional is the accumulated entry notional used for return calculation.
	RealizedNotional float64 `json:"realized_notional"`
	// Status is open/closed.
	Status string `gorm:"type:varchar(16);index" json:"status"`
	// OpenTime is when the position was first opened.
	OpenTime time.Time `json:"open_time"`
	// CloseTime is set when position becomes closed.
	// default:null 让 GORM 在零值时省略该列（落 NULL）：严格模式 MySQL
	// (NO_ZERO_DATE) 会拒绝零值 time.Time 生成的 '0000-00-00'（Error 1292）。
	CloseTime time.Time `gorm:"default:null" json:"close_time,omitempty"`
	// UpdatedAt is when we last updated this row.
	UpdatedAt time.Time `json:"updated_at"`
}

// StrategyOptimizationRun records one automatic AI optimization attempt for a strategy.
type StrategyOptimizationRun struct {
	ID uint `gorm:"primaryKey" json:"id"`

	StrategyID string `gorm:"type:varchar(64);index" json:"strategy_id"`
	OwnerID    uint   `gorm:"index" json:"owner_id"`

	Status  string `gorm:"type:varchar(32);index" json:"status"`
	Trigger string `gorm:"type:varchar(32)" json:"trigger"`
	Model   string `gorm:"type:varchar(128)" json:"model"`

	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`

	BaseCodeHash      string `gorm:"type:varchar(64)" json:"base_code_hash"`
	CandidateCodeHash string `gorm:"type:varchar(64)" json:"candidate_code_hash"`
	Applied           bool   `json:"applied"`
	// PreviousTemplateID 记录这次 apply 之前 instance 绑定的 template_id，
	// 用于 POST /strategies/:id/rollback 回滚。
	PreviousTemplateID uint `gorm:"index" json:"previous_template_id"`
	// NewTemplateID 记录这次 apply 新建的 template_id（== instance 切到的目标）。
	// 跟 PreviousTemplateID 一起使 rollback 不依赖 hash 字符串匹配。
	NewTemplateID uint `gorm:"index" json:"new_template_id"`

	Summary      string `gorm:"type:text" json:"summary"`
	ErrorMessage string `gorm:"type:text" json:"error_message"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// StrategyAuditLog 是给"bot/admin 改策略"系列动作的统一审计流。
// 任何修改性 endpoint（PUT/PATCH/POST 改动 config 或代码、平仓、取消订单、rollback）
// 都自动 INSERT 一行，以便事后追溯"是谁、什么时候、改了什么"。
type StrategyAuditLog struct {
	ID uint `gorm:"primaryKey" json:"id"`

	StrategyID string `gorm:"type:varchar(64);index" json:"strategy_id"`
	OwnerID    uint   `gorm:"index" json:"owner_id"`

	// Actor 是触发动作的用户名（claude_cron / admin / ...）
	Actor string `gorm:"type:varchar(64);index" json:"actor"`
	// ActorID 是上面 Actor 的 user_id
	ActorID uint `gorm:"index" json:"actor_id"`

	// Action 是高层动作分类：patch_config / put_config / apply_code / rollback /
	// blacklist_remove / cancel_orders / close_position / patch_meta / ...
	Action string `gorm:"type:varchar(48);index" json:"action"`

	// Endpoint 是 HTTP 路径（含 method 前缀），用于精准定位走的哪条接口
	Endpoint string `gorm:"type:varchar(160)" json:"endpoint"`

	// BeforeJSON / AfterJSON 是动作前后的关键字段快照（不必全量 config，只放变化部分）
	BeforeJSON string `gorm:"type:text" json:"before_json"`
	AfterJSON  string `gorm:"type:text" json:"after_json"`

	// Summary 是给人类看的一句话总结
	Summary string `gorm:"type:varchar(512)" json:"summary"`

	// Success 标识动作是否成功（4xx/5xx 都算失败）
	Success bool `gorm:"index" json:"success"`
	// HTTPStatus 实际返回的状态码
	HTTPStatus int `json:"http_status"`
	// ErrorMessage 失败时的错误说明
	ErrorMessage string `gorm:"type:text" json:"error_message"`

	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

// StrategyVersion stores one versioned snapshot of a strategy template/code for a strategy instance.
type StrategyVersion struct {
	ID uint `gorm:"primaryKey" json:"id"`

	StrategyID string `gorm:"type:varchar(64);index" json:"strategy_id"`
	TemplateID uint   `gorm:"index" json:"template_id"`
	OwnerID    uint   `gorm:"index" json:"owner_id"`

	VersionHash string `gorm:"type:varchar(64);index" json:"version_hash"`
	CodeHash    string `gorm:"type:varchar(64);index" json:"code_hash"`
	CodeSize    int    `json:"code_size"`
	Code        string `gorm:"type:mediumtext" json:"code"`

	Source    string `gorm:"type:varchar(32);index" json:"source"`
	Trigger   string `gorm:"type:varchar(32)" json:"trigger"`
	Path      string `gorm:"type:text" json:"path"`
	Summary   string `gorm:"type:text" json:"summary"`
	IsCurrent bool   `gorm:"index" json:"is_current"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// StrategyPublishRecord records a version switch applied to a strategy.
type StrategyPublishRecord struct {
	ID uint `gorm:"primaryKey" json:"id"`

	StrategyID string `gorm:"type:varchar(64);index" json:"strategy_id"`
	TemplateID uint   `gorm:"index" json:"template_id"`
	OwnerID    uint   `gorm:"index" json:"owner_id"`

	RunID         *uint `gorm:"index" json:"run_id,omitempty"`
	FromVersionID *uint `gorm:"index" json:"from_version_id,omitempty"`
	ToVersionID   *uint `gorm:"index" json:"to_version_id,omitempty"`

	Status      string `gorm:"type:varchar(32);index" json:"status"`
	Trigger     string `gorm:"type:varchar(32)" json:"trigger"`
	AppliedPath string `gorm:"type:text" json:"applied_path"`
	Summary     string `gorm:"type:text" json:"summary"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DailyPnL is a daily realized PnL snapshot for the user.
// It is computed by a scheduled job (00:05 local time) for the previous day.
type DailyPnL struct {
	ID uint `gorm:"primaryKey" json:"id"`

	OwnerID uint   `gorm:"index:idx_daily_pnl_owner_day,unique" json:"owner_id"`
	Day     string `gorm:"type:varchar(10);index:idx_daily_pnl_owner_day,unique" json:"day"` // YYYY-MM-DD (local)

	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`

	GrossProfit      float64 `json:"gross_profit"`
	GrossLoss        float64 `json:"gross_loss"`
	RealizedPnL      float64 `gorm:"column:realized_pn_l" json:"realized_pnl"`
	RealizedNotional float64 `json:"realized_notional"`
	Trades           int     `json:"trades"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TelegramSubscriber struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ChatID    int64     `gorm:"uniqueIndex" json:"chat_id"`
	Username  string    `json:"username"`
	FirstName string    `json:"first_name"`
	Enabled   bool      `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TelegramBotState struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	LastUpdateID int64     `json:"last_update_id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// RebalanceWhitelist is one approved cross-exchange withdrawal destination, edited
// in the UI. It is the app-side (SECOND) guard: the rebalancer only moves funds to
// an address that has an Enabled row here. The FIRST, hard guard is the exchange's
// own "withdraw to whitelisted addresses only" setting — this table does NOT
// replace it. Address is the destination exchange's deposit address for
// (Asset, Network); a wrong chain permanently loses funds, so Network is required.
type RebalanceWhitelist struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	Exchange string `gorm:"size:32;not null;index:uniq_rebalance_wl,unique" json:"exchange"`  // 目标所: gate/binance
	Asset    string `gorm:"size:32;not null;index:uniq_rebalance_wl,unique" json:"asset"`     // 币种: USDT...
	Network  string `gorm:"size:32;not null;index:uniq_rebalance_wl,unique" json:"network"`   // 链: TRC20...
	Address  string `gorm:"size:128;not null;index:uniq_rebalance_wl,unique" json:"address"`  // 目标所的充值地址
	Memo     string `gorm:"size:128" json:"memo"`  // tag/memo, 需要的链才填
	Label    string `gorm:"size:128" json:"label"` // 人工备注
	Enabled  bool   `gorm:"default:true" json:"enabled"`
	// CreatedBy records which user added the row — a fund-movement audit trail.
	CreatedBy uint           `json:"created_by"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// RebalanceTransfer is the audit log of every rebalance transfer OUTCOME — real
// submissions, dry-runs, skips, and failures alike. Only Status=="submitted" rows
// count toward the daily USD cap and the per-asset cooldown, so the caps survive
// restarts (they are derived from this table, not in-memory).
type RebalanceTransfer struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Asset        string    `gorm:"size:32;index" json:"asset"`
	FromExchange string    `gorm:"size:32" json:"from_exchange"`
	ToExchange   string    `gorm:"size:32" json:"to_exchange"`
	Network      string    `gorm:"size:32" json:"network"`
	Amount       float64   `json:"amount"`
	AmountUSD    float64   `json:"amount_usd"`
	ToAddress    string    `gorm:"size:128" json:"to_address"`
	Status       string    `gorm:"size:24;index" json:"status"` // submitted|dryrun|skipped|failed
	TxID         string    `gorm:"size:191" json:"tx_id"`
	Detail       string    `gorm:"size:255" json:"detail"` // skip reason or error
	Mode         string    `gorm:"size:16" json:"mode"`
	CreatedBy    uint      `json:"created_by"`
	CreatedAt    time.Time `gorm:"index" json:"created_at"`
}
