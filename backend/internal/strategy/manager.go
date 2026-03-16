package strategy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"quanty_trade/internal/exchange"

	"quanty_trade/internal/models"
	"quanty_trade/internal/ws"
	"sync"

	"gorm.io/gorm"
)

type StrategyStatus string

const (
	StatusRunning StrategyStatus = "running"
	StatusStopped StrategyStatus = "stopped"
	StatusError   StrategyStatus = "error"
)

type StrategyInstance struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Path     string                 `json:"path"`
	Config   map[string]interface{} `json:"config"`
	Status   StrategyStatus         `json:"status"`
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	mu       sync.Mutex
	hub      *ws.Hub
	exchange exchange.Exchange
}

type Manager struct {
	instances map[string]*StrategyInstance
	mu        sync.RWMutex
	hub       *ws.Hub
	exchange  exchange.Exchange
}

func NewManager(hub *ws.Hub, ex exchange.Exchange) *Manager {
	return &Manager{
		instances: make(map[string]*StrategyInstance),
		hub:       hub,
		exchange:  ex,
	}
}

func (m *Manager) AddStrategy(id, name, path string, config map[string]interface{}) *StrategyInstance {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := &StrategyInstance{
		ID:       id,
		Name:     name,
		Path:     path,
		Config:   config,
		Status:   StatusStopped,
		hub:      m.hub,
		exchange: m.exchange,
	}
	m.instances[id] = inst
	return inst
}

func (m *Manager) StartStrategy(id string) error {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("strategy %s not found", id)
	}

	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.Status == StatusRunning {
		return nil
	}

	configJSON, _ := json.Marshal(inst.Config)

	absPath, _ := filepath.Abs(inst.Path)
	cmd := exec.Command("python3", absPath, string(configJSON))
	cmd.Dir = filepath.Dir(absPath)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	inst.cmd = cmd
	inst.stdin = stdin
	inst.stdout = stdout
	inst.Status = StatusRunning

	// Start data feed
	symbol, _ := inst.Config["symbol"].(string)
	if symbol != "" {
		go inst.exchange.SubscribeCandles(symbol, func(candle exchange.Candle) {
			inst.SendData("candle", candle)
		})
	}

	go inst.readStdout()
	go inst.readStderr(stderr)
	return nil
}

func (inst *StrategyInstance) readStderr(stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		fmt.Printf("[%s ERROR] %s\n", inst.Name, scanner.Text())
	}
}

func (m *Manager) StopStrategy(id string) error {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("strategy %s not found", id)
	}

	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.Status != StatusRunning {
		return nil
	}

	// Send stop command
	stopMsg := map[string]interface{}{"type": "stop"}
	json.NewEncoder(inst.stdin).Encode(stopMsg)

	if err := inst.cmd.Process.Kill(); err != nil {
		return err
	}

	inst.Status = StatusStopped
	return nil
}

func (inst *StrategyInstance) SendData(dataType string, data interface{}) error {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.Status != StatusRunning {
		return fmt.Errorf("strategy not running")
	}

	msg := map[string]interface{}{
		"type": dataType,
		"data": data,
	}
	return json.NewEncoder(inst.stdin).Encode(msg)
}

func (inst *StrategyInstance) readStdout() {
	scanner := bufio.NewScanner(inst.stdout)
	for scanner.Scan() {
		var msg map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			fmt.Printf("Error decoding strategy output: %v\n", err)
			continue
		}

		msgType, _ := msg["type"].(string)
		data := msg["data"]

		switch msgType {
		case "order":
			orderReq, _ := data.(map[string]interface{})
			symbol, _ := orderReq["symbol"].(string)
			side, _ := orderReq["side"].(string)
			amount, _ := orderReq["amount"].(float64)
			price, _ := orderReq["price"].(float64)

			order, err := inst.exchange.PlaceOrder(symbol, side, amount, price)
			if err != nil {
				inst.hub.BroadcastJSON(map[string]interface{}{
					"type":  "error",
					"error": fmt.Sprintf("Failed to place order: %v", err),
				})
			} else {
				inst.hub.BroadcastJSON(map[string]interface{}{
					"type": "order",
					"data": order,
				})
				inst.SendData("order", order)
			}
		case "log":
			fmt.Printf("[%s LOG] %v\n", inst.Name, data)
			inst.hub.BroadcastJSON(map[string]interface{}{
				"type": "log",
				"data": data,
				"id":   inst.ID,
			})

		}
	}

	inst.mu.Lock()
	inst.Status = StatusStopped
	inst.mu.Unlock()
}

func (m *Manager) SyncFromDB(db *gorm.DB) error {
	var instances []models.StrategyInstance
	if err := db.Preload("Template").Find(&instances).Error; err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, inst := range instances {
		if _, ok := m.instances[inst.ID]; !ok {
			var config map[string]interface{}
			json.Unmarshal([]byte(inst.Config), &config)

			m.instances[inst.ID] = &StrategyInstance{
				ID:       inst.ID,
				Name:     inst.Name,
				Path:     inst.Template.Path,
				Config:   config,
				Status:   StatusStopped,
				hub:      m.hub,
				exchange: m.exchange,
			}
		}
	}
	return nil
}

func (m *Manager) ListStrategies() []*StrategyInstance {

	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]*StrategyInstance, 0, len(m.instances))
	for _, inst := range m.instances {
		list = append(list, inst)
	}
	return list
}

func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range m.instances {
		if inst.Status == StatusRunning {
			inst.cmd.Process.Kill()
		}
	}
	m.instances = make(map[string]*StrategyInstance)
}
