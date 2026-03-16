package exchange

import (
	"time"
)

type Candle struct {
	Timestamp time.Time `json:"timestamp"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Close     float64   `json:"close"`
	Volume    float64   `json:"volume"`
}

type Order struct {
	ID        string    `json:"id"`
	Symbol    string    `json:"symbol"`
	Side      string    `json:"side"` // "buy", "sell"
	Amount    float64   `json:"amount"`
	Price     float64   `json:"price"`
	Status    string    `json:"status"` // "open", "filled", "canceled"
	Timestamp time.Time `json:"timestamp"`
}

type Position struct {
	Symbol       string    `json:"symbol"`
	Amount       float64   `json:"amount"`
	Price        float64   `json:"price"`
	StrategyName string    `json:"strategy_name"`
	ExchangeName string    `json:"exchange_name"`
	Status       string    `json:"status"` // "active", "closed"
	OwnerID      uint      `json:"owner_id"`
	OpenTime     time.Time `json:"open_time"`
	CloseTime    time.Time `json:"close_time,omitempty"`
}

type Exchange interface {
	GetName() string
	FetchCandles(symbol string, timeframe string, limit int) ([]Candle, error)
	PlaceOrder(symbol string, side string, amount float64, price float64) (*Order, error)
	FetchOrders(symbol string) ([]Order, error)
	FetchPositions(status string) ([]Position, error) // status: "active" or "closed"
	SubscribeCandles(symbol string, callback func(Candle)) error
}

type MockExchange struct {
	Name string
}

func (m *MockExchange) GetName() string { return m.Name }

func (m *MockExchange) FetchCandles(symbol string, timeframe string, limit int) ([]Candle, error) {
	// Mock implementation
	return []Candle{}, nil
}

func (m *MockExchange) PlaceOrder(symbol string, side string, amount float64, price float64) (*Order, error) {
	return &Order{
		ID:        "mock-id",
		Symbol:    symbol,
		Side:      side,
		Amount:    amount,
		Price:     price,
		Status:    "filled",
		Timestamp: time.Now(),
	}, nil
}

func (m *MockExchange) FetchOrders(symbol string) ([]Order, error) {
	return []Order{}, nil
}

func (m *MockExchange) FetchPositions(status string) ([]Position, error) {
	if status == "active" {
		return []Position{
			{
				Symbol:       "BTC/USDT",
				Amount:       0.05,
				Price:        62000.0,
				StrategyName: "均线趋势策略",
				ExchangeName: m.Name,
				Status:       "active",
				OwnerID:      1, // admin
				OpenTime:     time.Now().Add(-2 * time.Hour),
			},
			{
				Symbol:       "ETH/USDT",
				Amount:       1.5,
				Price:        2500.0,
				StrategyName: "用户A策略",
				ExchangeName: m.Name,
				Status:       "active",
				OwnerID:      2, // user A
				OpenTime:     time.Now().Add(-1 * time.Hour),
			},
		}, nil
	} else {
		return []Position{
			{
				Symbol:       "ETH/USDT",
				Amount:       1.2,
				Price:        2450.0,
				StrategyName: "网格套利",
				ExchangeName: m.Name,
				Status:       "closed",
				OwnerID:      1, // admin
				OpenTime:     time.Now().Add(-24 * time.Hour),
				CloseTime:    time.Now().Add(-22 * time.Hour),
			},
		}, nil
	}
}

func (m *MockExchange) SubscribeCandles(symbol string, callback func(Candle)) error {
	// Simulate data every 1 second
	go func() {
		price := 60000.0
		for {
			price += float64((time.Now().UnixNano() % 100) - 50)
			callback(Candle{
				Timestamp: time.Now(),
				Open:      price,
				High:      price + 10,
				Low:       price - 10,
				Close:     price,
				Volume:    1.0,
			})
			time.Sleep(1 * time.Second)
		}
	}()
	return nil
}
