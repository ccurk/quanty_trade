package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func GenerateUUID() string {
	return uuid.New().String()
}

type UserRole string

const (
	RoleAdmin UserRole = "admin"
	RoleUser  UserRole = "user"
)

type User struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Username  string         `gorm:"unique;not null" json:"username"`
	Password  string         `gorm:"not null" json:"-"`
	Role      UserRole       `gorm:"default:'user'" json:"role"`
	Configs   string         `gorm:"type:text" json:"-"` // JSON string for exchange API keys
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type StrategyTemplate struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Name        string         `gorm:"unique;not null" json:"name"`
	Description string         `json:"description"`
	Path        string         `gorm:"not null" json:"path"` // Python file path
	AuthorID    uint           `json:"author_id"`
	Author      User           `gorm:"foreignKey:AuthorID" json:"author"`
	IsPublic    bool           `gorm:"default:false" json:"is_public"`
	IsDraft     bool           `gorm:"default:false" json:"is_draft"`
	IsEnabled   bool           `gorm:"default:true" json:"is_enabled"`
	Code        string         `gorm:"type:text" json:"code"` // Store source code for editing
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

type StrategyInstance struct {
	ID         string           `gorm:"primaryKey" json:"id"`
	Name       string           `json:"name"`
	TemplateID uint             `json:"template_id"`
	Template   StrategyTemplate `gorm:"foreignKey:TemplateID" json:"template"`
	OwnerID    uint             `json:"owner_id"`
	Owner      User             `gorm:"foreignKey:OwnerID" json:"owner"`
	Config     string           `json:"config"` // JSON string
	Status     string           `json:"status"` // running, stopped, error
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

type StrategyLog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	StrategyID string    `gorm:"index" json:"strategy_id"`
	Level      string    `json:"level"` // info, error
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

type Backtest struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	StrategyID     string    `gorm:"index" json:"strategy_id"`
	StartTime      time.Time `json:"start_time"`
	EndTime        time.Time `json:"end_time"`
	InitialBalance float64   `json:"initial_balance"`
	FinalBalance   float64   `json:"final_balance"`
	TotalTrades    int       `json:"total_trades"`
	TotalProfit    float64   `json:"total_profit"`
	ReturnRate     float64   `json:"return_rate"`
	Status         string    `json:"status"`                  // pending, running, completed, failed
	Result         string    `gorm:"type:text" json:"result"` // Full JSON result
	UserID         uint      `gorm:"index" json:"user_id"`
	CreatedAt      time.Time `json:"created_at"`
}

type APILog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	StatusCode int       `json:"status_code"`
	Latency    int64     `json:"latency"` // nanoseconds
	ClientIP   string    `json:"client_ip"`
	UserID     uint      `json:"user_id"`
	Username   string    `json:"username"`
	CreatedAt  time.Time `json:"created_at"`
}

type ExchangeOrderEvent struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	OwnerID       uint      `gorm:"index" json:"owner_id"`
	Exchange      string    `gorm:"index" json:"exchange"`
	Symbol        string    `gorm:"index" json:"symbol"`
	OrderID       string    `gorm:"index" json:"order_id"`
	ClientOrderID string    `gorm:"index" json:"client_order_id"`
	Side          string    `json:"side"`
	OrderType     string    `json:"order_type"`
	Status        string    `gorm:"index" json:"status"`
	Price         float64   `json:"price"`
	OrigQty       float64   `json:"orig_qty"`
	ExecutedQty   float64   `json:"executed_qty"`
	LastQty       float64   `json:"last_qty"`
	LastPrice     float64   `json:"last_price"`
	EventTime     time.Time `gorm:"index" json:"event_time"`
	Raw           string    `gorm:"type:text" json:"raw"`
	CreatedAt     time.Time `json:"created_at"`
}
