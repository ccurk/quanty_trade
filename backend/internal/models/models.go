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
	Path string `gorm:"not null" json:"path"`
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
	Code string `gorm:"type:text" json:"code"`
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
type StrategyLog struct {
	// ID is the auto-increment primary key.
	ID uint `gorm:"primaryKey" json:"id"`
	// StrategyID is the StrategyInstance ID.
	StrategyID string `gorm:"index" json:"strategy_id"`
	// Level indicates severity (info/error).
	Level string `json:"level"`
	// Message is the raw log message string.
	Message string `json:"message"`
	// CreatedAt is the log creation time.
	CreatedAt time.Time `json:"created_at"`
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
	// CreatedAt is the request timestamp.
	CreatedAt time.Time `json:"created_at"`
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
	ID uint `gorm:"primaryKey" json:"id"`
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
	Side string `gorm:"type:varchar(8)" json:"side"`
	// OrderType is market/limit/... (platform normalized values).
	OrderType string `gorm:"type:varchar(16)" json:"order_type"`
	// ClientOrderID is generated by platform and used for correlation.
	ClientOrderID string `gorm:"type:varchar(64);uniqueIndex" json:"client_order_id"`
	// ExchangeOrderID is the exchange-generated id once accepted.
	ExchangeOrderID string `gorm:"type:varchar(64);index" json:"exchange_order_id"`
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
	// Amount is the current position size in base asset.
	Amount float64 `json:"amount"`
	// AvgPrice is the volume-weighted average entry price.
	AvgPrice float64 `json:"avg_price"`
	// RealizedPnL is the realized profit/loss accumulated on closes.
	RealizedPnL float64 `json:"realized_pnl"`
	// RealizedNotional is the accumulated entry notional used for return calculation.
	RealizedNotional float64 `json:"realized_notional"`
	// Status is open/closed.
	Status string `gorm:"type:varchar(16);index" json:"status"`
	// OpenTime is when the position was first opened.
	OpenTime time.Time `json:"open_time"`
	// CloseTime is set when position becomes closed.
	CloseTime time.Time `json:"close_time,omitempty"`
	// UpdatedAt is when we last updated this row.
	UpdatedAt time.Time `json:"updated_at"`
}
