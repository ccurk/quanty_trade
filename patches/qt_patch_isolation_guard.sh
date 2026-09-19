#!/usr/bin/env bash
# 隔离闸：禁止【两个运行中的实例】的 symbols 相交。
#
# 为什么需要（机制，不是巧合）：
#   USDM 每个 symbol 只有【一个净持仓】。取仓与锁仓都是按 (ownerID, symbol) 索引：
#     - exchange.USDMPositionInfo(ownerID, symbol)      binance.go
#     - lockTPSL(uid, symbol)                           manager.go
#     - 交易所侧 /fapi/v2/positionRisk 本身就是 per-symbol 净仓
#   三者**都不含 strategy_id**。所以两个实例跑同一个 symbol = 争同一个净仓：
#   一方开仓，另一方的 tp/sl 会按自己的价位把【对方的】仓位平掉。
#   今天隔离成立是**巧合**：三个池子恰好不相交。而 SYMBOL_ROTATE 会按 48h 盈亏
#   自动增删符号，池子会漂 —— 漂到一起就是事故。本闸把它变成机制。
#
# 插在哪：Manager.StartStrategy（strategy_lifecycle.go）。
#   StartStrategy 是"一个配置变成活的"的唯一入口 —— 配置写入路径
#   （api 三个 handler）全都先过 UpdateStrategyConfig，而它在 Status==Running
#   时直接报错，所以冲突配置只可能在【启动】这一刻生效。只改这一处即完备。
#
# 用法：bash qt_patch_isolation_guard.sh            # 干跑，只打印
#       bash qt_patch_isolation_guard.sh --apply    # 落盘（自动备份 .bak-<ts>）
set -euo pipefail

REPO="${QT_REPO:-/root/work/quanty_trade/backend}"
F="$REPO/internal/strategy/strategy_lifecycle.go"
APPLY=0
[ "${1:-}" = "--apply" ] && APPLY=1

[ -f "$F" ] || { echo "找不到 $F（用 QT_REPO= 指定仓库根）"; exit 1; }

python3 - "$F" "$APPLY" <<'PY'
import sys, re, io, os, time
path, apply_ = sys.argv[1], sys.argv[2] == "1"
src = open(path, encoding="utf-8").read()

HELPER = '''
// conflictingRunningSymbols 返回【其它运行中实例】与 syms 重叠的符号（已归一）。
//
// 为什么需要这道闸：USDM 每个 symbol 只有一个净持仓，且取仓/锁仓都按
// (ownerID, symbol) 索引（exchange.USDMPositionInfo / lockTPSL），**不含 strategy_id**。
// 两个实例跑同一个 symbol，就是在争同一个净仓：一方开仓，另一方会按自己的 tp/sl
// 把【对方的】仓位平掉。今天隔离成立是巧合——池子恰好不相交，而 SYMBOL_ROTATE
// 会按 48h 盈亏自动换符号。闸门把它变成机制。
func (m *Manager) conflictingRunningSymbols(id string, syms []string) []string {
	want := map[string]struct{}{}
	for _, s := range syms {
		if n := exchange.NormalizeSymbol(s); n != "" {
			want[n] = struct{}{}
		}
	}
	if len(want) == 0 {
		return nil
	}
	// 锁序：本文件的约定是【先放 m.mu 再动 inst.mu】，不能持 m.mu 去拿 inst.mu。
	m.mu.RLock()
	others := make([]*StrategyInstance, 0, len(m.instances))
	for oid, o := range m.instances {
		if oid != id && o != nil {
			others = append(others, o)
		}
	}
	m.mu.RUnlock()

	seen := map[string]struct{}{}
	var hits []string
	for _, o := range others {
		o.mu.Lock()
		running := o.Status == StatusRunning || o.Status == StatusStarting
		o.mu.Unlock()
		if !running {
			continue
		}
		for _, s := range parseSymbolsValue(o.Config["symbols"]) {
			n := exchange.NormalizeSymbol(s)
			if n == "" {
				continue
			}
			if _, ok := want[n]; !ok {
				continue
			}
			if _, dup := seen[n]; dup {
				continue
			}
			seen[n] = struct{}{}
			hits = append(hits, n)
		}
	}
	return hits
}

'''

if "conflictingRunningSymbols" in src:
    print("已存在 conflictingRunningSymbols，跳过（幂等）")
    sys.exit(0)

anchor_fn = "func (m *Manager) StartStrategy(id string) error {"
if anchor_fn not in src:
    print("❌ 找不到 StartStrategy 锚点，未改动"); sys.exit(1)

# 1) 插 helper（放在 StartStrategy 前一行）
src = src.replace(anchor_fn, HELPER.lstrip("\n") + anchor_fn, 1)

# 2) 在 StartStrategy 里、状态置 Starting 之前插入闸门
old_call = """	inst.mu.Unlock()
	m.setStrategyStatus(inst, StatusStarting)"""
new_call = """	inst.mu.Unlock()
	if hits := m.conflictingRunningSymbols(id, parseSymbolsValue(inst.Config["symbols"])); len(hits) > 0 {
		return fmt.Errorf("符号 %s 已被其它运行中的策略占用：USDM 每 symbol 单一净仓，同跑会互相平仓（隔离闸）", strings.Join(hits, ","))
	}
	m.setStrategyStatus(inst, StatusStarting)"""
if old_call not in src:
    print("❌ 找不到 StartStrategy 内的插入点，未改动（请人工确认）"); sys.exit(1)
src = src.replace(old_call, new_call, 1)

if not apply_:
    print("=== 干跑：将写入 %s ===" % path)
    print("  + 新增 func (m *Manager) conflictingRunningSymbols")
    print("  + StartStrategy 内在 setStrategyStatus(StatusStarting) 前加一道闸")
    print("改动行数 ≈ %d" % (new_call.count(chr(10)) - old_call.count(chr(10)) + HELPER.count(chr(10))))
    print("用 --apply 落盘")
    sys.exit(0)

bak = path + ".bak-" + time.strftime("%Y%m%d-%H%M%S")
io.open(bak, "w", encoding="utf-8").write(io.open(path, encoding="utf-8").read())
io.open(path, "w", encoding="utf-8").write(src)
print("✅ 已写入 %s\n   备份 %s" % (path, bak))
PY

echo
echo "编译验证（在副本里做，不动生产二进制）："
echo "  cp -a $REPO /tmp/qt_build_test && cd /tmp/qt_build_test && go build ./... && echo BUILD_OK"
echo
echo "⚠️ 只改源码不会影响正在跑的容器 —— 需重新构建镜像 + 重启后端才生效。"
echo "⚠️ 生效前请先确认：三个实例的 symbols 当前两两不相交（跑 qt_check_isolation.py）。"
