package models

import "time"

type StrategySelector struct {
	ID               string    `gorm:"primaryKey" json:"id"`
	Name             string    `json:"name"`
	OwnerID          uint      `json:"owner_id"`
	ExecutorTemplate uint      `json:"executor_template_id"`
	Config           string    `json:"config"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type StrategySelectorChild struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	SelectorID string    `gorm:"index" json:"selector_id"`
	StrategyID string    `gorm:"index" json:"strategy_id"`
	Symbol     string    `gorm:"index" json:"symbol"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
