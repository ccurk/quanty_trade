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
	Symbol string  `json:"symbol"`
	Amount float64 `json:"amount"`
	Price  float64 `json:"price"`
}

type Exchange interface {
	GetName() string
	FetchCandles(symbol string, timeframe string, limit int) ([]Candle, error)
	PlaceOrder(symbol string, side string, amount float64, price float64) (*Order, error)
	FetchOrders(symbol string) ([]Order, error)
	FetchPositions() ([]Position, error)
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

func (m *MockExchange) FetchPositions() ([]Position, error) {
	return []Position{}, nil
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
