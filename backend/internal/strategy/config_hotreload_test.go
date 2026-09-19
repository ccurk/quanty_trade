package strategy

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// 2026-09-16 回归测试：配置热更改造。
//
// 背景（实测数据，不是推测）：审计表 strategy_audit_logs 里，
// action='patch_config' AND success=0 共 1589 行，全部同一个错误
// "cannot update config while strategy is running"，actor 是 claude_cron，
// 时间跨度 2026-07-17 → 2026-09-16，同期成功仅 219 次。
// 也就是说自动调参链路两个月里 88% 的改动被那道 guard 弹掉。

// 直接回归：running 状态下改配置必须成功（旧实现这里返回错误）。
func TestUpdateStrategyConfigWhileRunning(t *testing.T) {
	m := &Manager{instances: map[string]*StrategyInstance{}}
	inst := &StrategyInstance{ID: "s1", Status: StatusRunning}
	inst.setConfig(map[string]interface{}{"leverage": 2})
	m.instances["s1"] = inst

	if err := m.UpdateStrategyConfig("s1", map[string]interface{}{"leverage": 10}); err != nil {
		t.Fatalf("running 状态下改配置必须成功，却返回: %v", err)
	}
	if got := getNumber(inst.Config()["leverage"]); got != 10 {
		t.Fatalf("leverage 未生效: want 10, got %v", got)
	}
	if inst.Status != StatusRunning {
		t.Fatalf("改配置不应改变运行状态: got %v", inst.Status)
	}
}

// 未知策略仍要报错 —— 移除 guard 不等于移除存在性校验。
func TestUpdateStrategyConfigUnknownStillErrors(t *testing.T) {
	m := &Manager{instances: map[string]*StrategyInstance{}}
	if err := m.UpdateStrategyConfig("nope", map[string]interface{}{"leverage": 10}); err == nil {
		t.Fatal("未知策略必须报错")
	}
}

// 并发替换 + 并发读，必须在 -race 下干净。
// 这是移除 guard 的前提：读方无锁，写方只换指针。
func TestConfigSnapshotConcurrentReplaceAndRead(t *testing.T) {
	inst := &StrategyInstance{ID: "s1", Status: StatusRunning}
	inst.setConfig(map[string]interface{}{"leverage": 2})

	const readers = 8
	const writes = 500
	var wg sync.WaitGroup

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < writes; j++ {
				cfg := inst.Config()
				if cfg == nil {
					continue
				}
				_ = getNumber(cfg["leverage"])
				_ = getString(cfg["order_amount_mode"])
			}
		}()
	}
	for i := 0; i < writes; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			inst.setConfig(map[string]interface{}{
				"leverage":          n,
				"order_amount_mode": "notional",
			})
		}(i)
	}
	wg.Wait()

	if inst.Config() == nil {
		t.Fatal("并发替换后 Config() 不应为 nil")
	}
}

// 读方拿到的永远是**完整一致**的快照，不会看到两个字段来自不同版本。
// 裸 map 原地改会破坏这条；整体换指针不会。
func TestConfigSnapshotIsConsistentPair(t *testing.T) {
	inst := &StrategyInstance{ID: "s1"}
	inst.setConfig(map[string]interface{}{"leverage": 2, "tag": "v2"})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var bad int
	var mu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			cfg := inst.Config()
			if cfg == nil {
				continue
			}
			lev := int(getNumber(cfg["leverage"]))
			tag, _ := cfg["tag"].(string)
			if want := fmt.Sprintf("v%d", lev); tag != want {
				mu.Lock()
				bad++
				mu.Unlock()
			}
		}
	}()

	for i := 3; i < 300; i++ {
		inst.setConfig(map[string]interface{}{"leverage": i, "tag": fmt.Sprintf("v%d", i)})
	}
	close(stop)
	wg.Wait()

	if bad != 0 {
		t.Fatalf("观察到 %d 次跨版本撕裂读（两个字段来自不同快照）", bad)
	}
}

// API 契约回归：Config 改成原子存储后，"config" 字段必须仍出现在 JSON 里。
// 前端和 ListStrategies 的调用方都按这个字段名取值，丢了是静默故障。
func TestMarshalJSONKeepsConfigField(t *testing.T) {
	inst := &StrategyInstance{ID: "s1", Name: "n1", Status: StatusRunning}
	inst.setConfig(map[string]interface{}{"leverage": 10})

	b, err := json.Marshal(inst)
	if err != nil {
		t.Fatalf("marshal 失败: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal 失败: %v", err)
	}
	cfg, ok := got["config"]
	if !ok {
		t.Fatalf("JSON 里丢了 config 字段: %s", string(b))
	}
	cm, ok := cfg.(map[string]interface{})
	if !ok {
		t.Fatalf("config 不是对象: %T", cfg)
	}
	if v := getNumber(cm["leverage"]); v != 10 {
		t.Fatalf("config.leverage 丢了: %v", cm["leverage"])
	}
	// 其它导出字段不能被 MarshalJSON 吃掉
	for _, k := range []string{"id", "name", "status"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("MarshalJSON 丢了字段 %q: %s", k, string(b))
		}
	}
	// 必须**恰好出现一次**：将来若有人在 StrategyInstance 上再加一个
	// json:"config" 标签，encoding/json 会输出两个同名 key，前端解析静默取到
	// 后者 —— 那不是报错，是无声的错值，最难查。
	if n := strings.Count(string(b), `"config"`); n != 1 {
		t.Fatalf("JSON 里 config key 出现 %d 次（必须恰好 1 次）: %s", n, string(b))
	}
}

// setConfig 后旧快照不被原地修改：拿过旧引用的读者不应看到新值。
// 这是"整体替换"与"原地改"的语义分界。
func TestSetConfigDoesNotMutatePreviousSnapshot(t *testing.T) {
	inst := &StrategyInstance{ID: "s1"}
	old := map[string]interface{}{"leverage": 2}
	inst.setConfig(old)

	inst.setConfig(map[string]interface{}{"leverage": 10})

	if v := getNumber(old["leverage"]); v != 2 {
		t.Fatalf("旧快照被原地改了: want 2, got %v", v)
	}
	if v := getNumber(inst.Config()["leverage"]); v != 10 {
		t.Fatalf("新快照未生效: got %v", v)
	}
}
