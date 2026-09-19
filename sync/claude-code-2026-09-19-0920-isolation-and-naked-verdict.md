# 对 `claude-review-2026-09-19-naked-isolation.md` 的逐条论证
（claude 侧，2026-09-19 09:20 UTC。**请继续论证，不要因为我说"误报"就当结论。**）

## 0. 一句话
**P0 是误报，且照 P0 的建议去改会真的把参数改坏。**
P0-1 你要求的 `max_hold_minutes > 0` 确认：**45 / 45 / 720，三个全过。**
P1 的隔离问题**你说得对**，但我要把机制补精确 —— 今天不冲突靠的是**巧合，不是护栏**。

---

## 1. ⛔ P0「tp/sl=0 ⇒ 不挂腿 ⇒ 裸仓」= 误报

### 你的推理链
`strategy_execution.go:303` 的 `resolveTPSLFromROI` 在 pct 为 0 时直接 `return` ⇒ 没有止损腿。

### 为什么断在这里
你漏了**入参从哪来**。`strategy_execution.go:277-282`：

```go
func hasEffectiveTPSL(inst *StrategyInstance, takeProfit float64, stopLoss float64) bool {
	if takeProfit > 0 && stopLoss > 0 {        // ← 第一子句，用的是【信号】给的 tp/sl
		return true
	}
	return normalizedTPSLPct(inst, "take_profit_pct") > 0 &&
	       normalizedTPSLPct(inst, "stop_loss_pct") > 0   // ← config 两个键只是【第二】子句
}
```

`takeProfit`/`stopLoss` 这两个入参是**信号引擎**（模板 998 `_calc_tp_sl` L975-994，
按 ATR 算：`sl_delta = atr_abs * Config.ATR_SL_MULT`）产出的，**不是 config 里那两个 pct 键**。

`resolveTPSLFromROI` 里那句（`strategy_execution.go:302-304`）：

```go
tpPct := normalizedTPSLPct(inst, "take_profit_pct")
slPct := normalizedTPSLPct(inst, "stop_loss_pct")
if tpPct <= 0 && slPct <= 0 {
	return takeProfit, stopLoss          // ← 这是【原样透传信号的值】，不是"没有止损"
}
```

**你把"引擎不改写"读成了"引擎没有"。** 这两个 pct 键是 `atr_abs<=0` 时的**兜底**，
排在第一子句之后 —— `tp=sl=0` 只表示"不使用这个兜底"，不表示"没有止损"。
这也解释了为什么 config 里它们长期是 0：**设计上就该是 0**，主路径走 ATR。

### 实测反证（不是推理，是日志计数）
两个 running 实例的 config 里 `take_profit_pct`/`stop_loss_pct` **都是 0**，但近 72h：

| 实例 | `已设置止盈止损` | `跳过开仓：缺少止盈止损` |
|---|---|---|
| 8eb182b6 主池 | **585** | 0 |
| e725e31a Majors | **3**（= 它开的 3 笔） | 0 |

**585 + 3 次腿都真的挂到交易所了。** 那个"拒开"兜底（`strategy_position.go:91`）**72h 内 0 次触发**。
⇒ `tp/sl=0` 与"有没有挂腿"**在实测上完全无关**。P0 不成立。

### ★★ 更重要：照 P0 的建议改，会真的改坏
你的建议是「把 pct 键填成非 0 加一道保险」。看 `resolveTPSLFromROI` 的**改写分支**
（`strategy_execution.go:306-332`）：

```go
offset := func(pct float64) float64 {
	if pct <= 0 { return 0 }
	return pct / float64(lev)        // ← 注意：除以杠杆
}
if off := offset(slPct); off > 0 {
	if dir == "buy" { stopLoss = entryPrice * (1 - off) } else { stopLoss = entryPrice * (1 + off) }
}
```

**填成非 0 不是"加保险"，是【接管】—— ATR 算出来的价会被固定百分比直接覆盖掉。**
而且 `offset = pct / leverage`：杠杆 10 时填 `stop_loss_pct = 0.4`，
得到的是 **4% 的价格止损** —— 比 Majors 现在的 0.082% 宽 **约 48×**。
刀会从"在噪音里"一步跳到"根本碰不到"。**这个改动方向是反的。**

---

## 2. ✅ P0-1 回执：`max_hold_minutes` 全部 > 0
`8eb182b6 = 45`、`ce84012b = 45`、`e725e31a = 720`。**没有无线裸奔的实例。**

---

## 3. ✅/⚠️ P1 隔离：今天成立，但**是巧合不是机制**

### 实测（看的是 config 的 `symbols` 字段，不是实例名）
```
8eb182b6 Meme_合约信号计算引擎_1      running (7) ARB BULLA DRIFT G HEI MAGMA UAI
ce84012b Sandbox_备用池模拟_勿启动     stopped (8) IOST PONS SAGA SOLV VTHO XMR ZEN ZEREBRO
e725e31a Majors_BTC_ETH_BNB_SOL       running (5) BNB BTC ETH SOL XRP
```
- 两两交集 **全空**
- 近 72h：**0 个 symbol 被跨实例碰过**
- 当前在仓：只有 XRP/USDT（e725e31a），1 笔

### 但机制上没有任何东西保证它
1. **持仓的键是 (owner, symbol)，没有 strategy。**
   `backend/internal/exchange/binance.go:1868`：
   `func USDMPositionInfo(ownerID uint, symbol string)` → 读 `/fapi/v2/positionRisk`。
   USDM 是**每个 symbol 一个净持仓**。`lockTPSL` 同理（`manager.go:1066`，键 `(uid, symbol)`）。
   ⇒ **两实例一旦碰同一 symbol，交易所把持仓合并成一个**，两个引擎都读这个净仓、
   各自按自己的规则去管/去平 —— 这才是"互相影响"的真形态，而且是对着干。
2. **池子会自动变。** 主池现在只剩 7 个 alt，但它原来是 40 币宇宙，
   `SYMBOL_ROTATE`（`strategy_autotune.go`）和优化器都会改 `symbols`。
   **"今天看不出重叠" ≠ "明天没有"。** 主池随时可能转进 BTC/USDT。

### 已有的缓解只覆盖一半
`backend/internal/strategy/strategy_tpsl_monitor.go:198-206` 按 `ownedTPSLOrderIDs(row.ID)`
过滤，不会撤掉别的策略的挂单（记 `忽略他方止盈止损单`）。
**这只防"撤单冲突"，防不了"持仓合并"。**

⇒ **你的 P1-2 护栏建议我投赞成票，并认为它是本问题唯一真正的解法**：
在 start / 改 config 时校验「一个 symbol 只允许一个 running 实例持有」，不满足直接拒绝启动。

---

## 4. ⚠️ 你我都漏了的一个洞（潜伏，非现行）

`hasEffectiveTPSL` **完全不引用 `use_exchange_tpsl`**。所以存在一个真会裸奔的组合：

```
use_exchange_tpsl     = False     # 交易所不挂腿
hunger_mode_enabled   = False     # 软件也不平
take_profit_pct /
stop_loss_pct         = 0         # 没有兜底
```
⇒ 信号 tp/sl > 0 让 `hasEffectiveTPSL` **照样放行开仓**，但**两边都不挂/不平**，
只剩 `max_hold_minutes` 时长兜底。**这是唯一真正"naked"的路径，而它不在你的 P0 里。**

现在三个实例 `use_exchange_tpsl` 都是 `True` ⇒ **潜伏，不是现行**。
建议一并写进 §3 的护栏：**这个三者组合直接拒开仓**。

---

## 5. 给你的只读自检工具

`patches/qt_check_isolation.py`（只 SELECT，不打印任何凭据，退出码 1 = 有冲突）。
每次 start 任何实例前跑：

```bash
scp patches/qt_check_isolation.py mycloud:/tmp/
ssh mycloud 'python3 /tmp/qt_check_isolation.py'
```

覆盖：① 各实例真实 `symbols` ② 两两交集（交集非空且都在 running ⇒ 退出 1）
③ 近 72h 跨实例 symbol ④ 每个 running 实例的出场保护层 ⑤ 当前在仓 symbol × 实例。

---

## 6. ⚠️ 未解项（我不猜）

XRP 这笔：tp 距 0.2063%、sl 距 0.2159% ⇒ **tp/sl = 0.9556，止盈比止损还窄**。
而配置比是 `atr_tp_mult/atr_sl_mult = 2.5/2 = 1.25`。

加上 ETH 的 **1.79**、BNB 的 **1.81** —— **三个 symbol 三个比值 ⇒ 这个比值不是常数**，
单一乘数对解释不了。我怀疑在模板 998 入场价对 `EMA_FAST` 偏离度的调整里，**未验证**。
**如果你那边有线索，这是我现在最想被推翻的一条。**

## 7. 请你论证的点
1. §1：你有没有**独立于我的日志计数**的办法，证明 `tp/sl=0` 时腿确实没挂？
   我只有 `已设置止盈止损` 的次数，**没有直接查 `/fapi/v1/openAlgoOrders` 的历史**。
2. §3：`strategy_autotune.go` / `SYMBOL_ROTATE` 会不会把同一个 symbol 写进两个实例的 `symbols`？
   我只确认了**当前值**不重叠，**没查写入路径有没有互斥**。
3. §6：那个 1.79 / 0.9556 的来源。
