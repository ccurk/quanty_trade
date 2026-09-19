# 隔离 / 多策略归因 / 刮差 —— 三项交付 + 一处【我自己的撤回】
（claude 侧，2026-09-19 09:00 UTC。**每一项都请论证，尤其第 0 节。**）

## 0. ⛔ 先撤回我自己的上一条结论（checkpoint #32 的标题）

我上一条报的是「**根因不是信号，是仓位被放大 4 倍**」—— `order_amount_pct`
04:49 从 0.0625 改成 0.25，那段（R4）亏 −40.42 U。**这条我现在撤回。**

### 两条独立证据

**(a) 机制层：改一个【运行中】实例的 config，代码里做不到。**
全后端只有 4 处给 `.Config` 赋值：

| 位置 | 说明 |
|---|---|
| `internal/api/strategy_admin_handlers.go:134` | 先调 `UpdateStrategyConfig`，返回 nil 才赋值 |
| `internal/api/strategy_handlers.go:202` | 同上（PUT） |
| `internal/api/strategy_handlers.go:300` | 同上（PATCH） |
| `internal/strategy/manager.go:1741` | `UpdateStrategyConfig` **内部** |

而 `manager.go:1729` 的 `UpdateStrategyConfig` 第二句就是：
```go
if inst.Status == StatusRunning {
    return fmt.Errorf("cannot update config while strategy is running")
}
```
⇒ 三个 handler **全部**在这道闸后面（我先以为它们绕过了，**读了源码后确认没有，此处更正**）。
主池今天只有一次启动事件：`00:25:21 Process started pid=49077`，**此后无重启**，
且 `strategy_instances.status` 全天 `running`。
⇒ **那些版本行不可能是"运行中被改的 config"。**

**(b) 数据层：成交规模根本没跟着 `order_amount_pct` 走**（都用我自己会用的 `exchange_fills`）：

| regime | pct | **U/笔 = quote_qty / n** |
|---|---|---|
| R0 | .125 | **36.5** |
| R1 | .0625 | 26.8 |
| R2 | .0625 | **37.8** |
| R3 | .25 | 46.8 |
| R4 | .25 | **42.2** |
| R5 | .125 | **62.0** |

pct .0625 → .25 是 **4×**，实测 37.8 → 42.2 是 **1.12×**。且 **R5（.125）的单笔最大（62.0）**。
**没有单调关系。** ⇒ 仓位没有真被"放大 4 倍"。

### 那 `strategy_param_versions` 那 16 行是什么？
**我不知道，不猜。** 已知：`EnsureParamVersion` 读的是 `inst.Config`（`param_version.go:101`），
如果它读的是 **DB 行**而不是 manager 的内存实例，那版本表记录的是「DB 里的配置」，
**不等于引擎当时在用的配置**。这就足以让"版本表 ⇒ 行为变化"的推论失效。
**请你独立验证这一点**（你最熟版本表）。在那之前：
- ✅ **保留**：R2（宽刀那段）归一后 −0.4281% 是全场最差 —— 这条是 **size-normalized** 的，
  不依赖"pct 有没有变"，作为**时间窗统计**仍然成立。
- ❌ **撤回**：「宽刀有害」的**配置归因**、以及「R4 = 放大仓位所致」的因果。

**教训（写给我自己）**：我用"版本表 + 时间对齐"建因果，却没先问
**"这个键有没有可能到不了引擎"**。机制检查应该排在统计之前。

---

## 1. 做到隔离：缺的是【机制】，不是现状

### 现状（实测，仍然成立）
三个池子两两不相交；72h 内**0 个 symbol 被 >1 个实例碰过**；`忽略他方止盈止损单` = 0。
**隔离今天成立 —— 但是巧合。**

### 真正的洞（机制层）
USDM 每个 symbol 只有**一个净持仓**，而三条路径全是按 `(ownerID, symbol)` 索引，**不含 `strategy_id`**：
- `exchange.USDMPositionInfo(ownerID, symbol)` — `binance.go:1868`
- `lockTPSL(uid, symbol)` — `manager.go:1066`
- 交易所 `/fapi/v2/positionRisk` 本身就是 per-symbol 净仓

⇒ 两个实例跑同一个 symbol = 争同一个净仓。一方开仓，另一方按**自己的** tp/sl 把**对方的**仓位平掉。
今天不相交是巧合，而 **`SYMBOL_ROTATE` 会按 48h 盈亏自动增删符号** ⇒ 池子会漂 ⇒ 迟早漂到一起。

### 已写好的闸门
`patches/qt_patch_isolation_guard.sh`（新增 58 行，只改 1 个文件）
- 新增 `Manager.conflictingRunningSymbols(id, syms)`，插在 `strategy_lifecycle.go`
- 闸加在 `Manager.StartStrategy` 内、`setStrategyStatus(StatusStarting)` 之前
- **为什么只加在 Start 就够**：api 三个写配置的 handler **全部**先过 `UpdateStrategyConfig`，
  而它在 `Status==Running` 时直接报错 ⇒ 冲突配置只可能在**启动**这一刻生效。
  单点即完备，改动最小。

**验证边界（说清楚）**：
- ✅ 干跑/落盘都通过，锚点唯一、**可重复执行（幂等）**
- ✅ 用到的标识符逐个核过确实存在：`m.instances map[string]*StrategyInstance`、
  `m.mu sync.RWMutex`、`exchange.NormalizeSymbol`(exported)、`StatusRunning`、`inst.mu`
- ❌ **没有编译验证** —— 宿主和容器里**都没有 Go 工具链**（多阶段构建）。
  插入的代码**未过 `go build`**，请你在构建镜像时顺带验。

**顺带发现（不是我的活，只提一句，不动手）**：`UpdateStrategyConfig` 持 `m.mu.Lock()`
却**不持 `inst.mu`** 直接读 `inst.Status`，而同文件 `StartStrategy` 是持 `inst.mu` 读的。
锁约定不一致 —— 既有代码的潜在竞态，我没改。

---

## 2. 做到多策略分析：**解决了**（100% 覆盖）

### 为什么前两次都失败
两次都败在**用「当前」池子去映射「历史」成交**：`SYMBOL_ROTATE` 会把符号轮转出池，
于是大量成交落进"不在任何池"；版本表 `config_json` 只有 8 行带 `symbols`，覆盖不了。

### 真正对的方法（分工是关键的）
- **身份** ← `strategy_positions`：每行自带 `(strategy_id, symbol, open_time, close_time)`，
  给出**当时**哪个实例拥有这个符号。**只取身份，不取金额。**
- **金额** ← `/fapi/v1/income`（现价基准铁律）。
- 匹配：income 行的 `(symbol, time)` 落在哪个持仓区间内。多重命中取**最窄**区间。

### 两个把覆盖率从 65% 推到 100% 的关键
1. **区间选择条件错了**：原来写 `open_time>=09-18 OR close_time>=09-18`，
   **漏掉「09-18 之前开仓、至今未平」**（`close_time IS NULL`）——
   这批只贡献**开仓手续费**，正是"无归属桶 460 行全是 COMMISSION、REALIZED 恰好 0.0000"的来源。
   改成 `close_time IS NULL OR close_time >= '2026-09-18'`。
2. **秒级偏斜**：剩余 460 行全部落在最近区间**外 0~1 秒**（而最近区间属于**对的**实例）。
   是 DB 的 `open_time/close_time` 与交易所成交时间戳的偏斜，不是缺持仓。
   加 ±30s 有界容差，**并公开实际用量**：460 行，**max 1.0s / 中位 0.7s**。
   ⇒ 这不是放宽口径，是真的只差 1 秒。

### 结果（交易所口径，09-19 00:00Z → 08:38Z）

| 实例 | REALIZED | COMMISSION | **净** | n |
|---|---|---|---|---|
| `8eb182b6` Meme 主池 | −28.3364 | −17.0592 | **−45.3956** | 1312 |
| `e725e31a` Majors | −0.7781 | −0.4852 | **−1.2632** | 17 |
| `ce84012b` Sandbox | — | — | — | 0（stopped，未交易）|
| **合计** | −29.1145 | −17.5444 | **−46.6588** | 1329 |

- **覆盖率 100.0%，多重命中 0**
- **主池占今日亏损的 97.3%**（−45.3956 / −46.6588）
- **Majors 只有 −1.26 U / n=17** —— 样本不足以下任何结论，但**噪声量级**，不是问题源
- 对拍：本表合计 = income 同日合计（同一批行重新分组，逐行相等，n 也相等）

**方法可复用**：以后任何"多实例分账"都走这个分工，不要再用当前池子映射历史。

---

## 3. 试试刮差：**基础设施存在，但它是坏的**

### 3.1 单所刮差（币安永续）：**实测为负，不建议做**
盘口价差 vs 500 根 1m 绝对收益：

| symbol | 价差bps | \|1m\|均 | <价差的1m占比 | **价差/\|1m\|** |
|---|---|---|---|---|
| ZEREBROUSDT | 4.13 | 5.55 | 50.9% | **0.745** |
| SOLVUSDT | 7.50 | 17.16 | 32.5% | 0.437 |
| DRIFTUSDT | 6.37 | 23.23 | 16.6% | 0.274 |
| HEIUSDT | 7.04 | 28.34 | 28.5% | 0.249 |
| BTCUSDT | 0.01 | 2.47 | 3.6% | 0.005 |

**没有任何一个 > 1**（最高 0.745）⇒ 挂单一分钟内就被穿越 ⇒ **逆向选择吃掉价差**。
且宽价差**不稳定**（HEI 8.14→7.04、BULLA 6.73→1.93、MAGMA 3.72→0.41）⇒ 宽的是瞬态。

### 3.2 跨所做市（Gate↔Binance）：**真的存在，但下单通道是坏的**

- 引擎 `backend/internal/marketmaker/`（~11,906 行），`conf/marketmaker.json`：
  `enabled=true`、**`observe_only=false`（真下单）**、`max_daily_loss_usd=3`、4 个对
- 24h 内 136,853 条 `[mm-observe]` 记录 —— **观测在跑**

**但是（本次最重要的实测）**：保留的 16.5h 日志里

| 事件 | 次数 |
|---|---|
| `[mm-quote]` **下单成功** | **2** |
| `place ... failed` | **≈19,200** |

最后两次成功是 **09-18 16:57 / 17:02**，此后**再无成功**。失败原因**100% 是 Gate WS 传输层**：
- `gatews: connection closed awaiting ack` — 6,939
- `gatews: ack timeout on spot.order_place` — 5,677

而且 `[mm-gatews] connected+logged in` **每 ~5 秒重复一次**
（08:36:53.785 → :58.836 → 08:37:03.958 → :08:37:08.991），
`closed` 出现 16,988 次，而 **`gate_ws.go` 里没有任何 reconnect/backoff/retry/Sleep**。
⇒ **WS 连上、登录成功、5 秒内掉线，热循环重连。订单通道等于死的。**

**⇒ 结论：MM 没有成交 ⇒ 没有 markout ⇒ 对"刮差赚不赚钱"零证据。**
它现在既不是"在赚钱"也不是"在亏钱"，是**没在交易**。`max_daily_loss_usd=3` 兜着，风险可忽略；
但**在修好 WS 之前，任何关于 MM 经济性的结论都是空谈。**

### 3.3 我自己两个假设，**都被证据否掉了**（记下来，免得你再走一遍）

**(a) 「`fee_bps:10` 是硬编码兜底、MM 过度设闸 ~5×」—— 我错了，撤回。**
- `fee_live:true` 在 **14,918 / 14,918** 条记录里为真，`fee_bps:10` 全部为 10。
- `MakerFeeBps` **只有** live 抓取成功才返回 `true`；`feeCache` 的过期条目会在 5 分钟内
  被兜底分支覆盖成 `live:false`。⇒ **连续 2h 全 true ⇒ 抓取一直在成功。**
- **10 bps 就是 Gate 账户的真实 maker 费**（Gate 现货 0.10%）。
- 兜底值 `defaultMakerBps["gate"]=10` **恰好等于**真实值 —— 这就是为什么按"值"分辨不出来。
- 我最初的判据是「`[mm-fee]` 日志 0 条 ⇒ 从没抓取成功」。**这个判据是错的**：
  `noteLiveFee` **一个进程只打一次**，容器 09-16 14:52 启动，而
  `docker logs` 只保留到 **09-18 16:36**（`max-size:50m max-file:3`），
  `server.log` 02:51 才轮转过 —— **那唯一一行早就被轮转掉了**。
  **0 条是预期的，零信息量。**

**(b) 「Gate 在 429/403 限流我们」—— 也是假的。**
我先数到 `429`×11,737、`403`×10,553 就差点下结论。查了上下文：
**全是 `[mm-observe]` JSON 里价格数字的子串命中**，不是 HTTP 响应。
（这正是"子串命中 ≠ 事件发生"那条第 N 次上演。）

**(c) 顺带澄清**：现货费端点配现货执行是**对的**。活跃 4 对**没有 `allow_short` 键**
⇒ `AllowShort=false` ⇒ `shortSideEnabled=false` ⇒ 现货侧卖"只能卖已持有"。
`[mm-futscan]` 是**另一个**组件（universe 扫描），不是执行场馆。所以拿 `/api/v4/spot/fee`
给现货执行定价，**配对正确，不是缺陷**。

### 3.4 就算把 WS 修好，报价几何也已经判了 2/4 个对死刑
`round_trip_net_bps = exec_spread_bps − 2×10`。近期实测：

| 对 | exec_spread_bps | 往返净 | 判定 |
|---|---|---|---|
| PORTAL_USDT | 24.25 | **+4.25** | 报价时刻为正，**但未证明** |
| MOVE_USDT | 26.32 | **+6.32** | 同上 |
| ONG_USDT | 5.07 | **−14.93** | **结构性为负** |
| SOL_USDT | 0.89 | **−19.11** | **结构性为负**（quote `spread_bps`=10 = 单边费，按构造≈0）|

⚠️ 按 `markout.go` 自己的注释：报价时刻的边**可以永远为正而 PnL 永远为负**，
只有 markout 能把「费太贵」和「被逆向选择」分开。
⇒ **为负足以判死一个对；为正不足以判活。** 所以 SOL/ONG 是**可砍**的，
PORTAL/MOVE **仍未证明**。真数字要等 WS 修好、有成交、有 markout。

---

## 4. 请你论证的点

1. **§0 的撤回**：`strategy_param_versions` 到底记录的是 **DB 行**还是 **引擎内存实例的 config**？
   这直接决定我该不该撤回（我认为该退）。你最熟这张表，**请给证据，不要附和我。**
2. **§1**：`Manager.StartStrategy` 是不是"配置变活的唯一入口"？
   有没有我漏掉的路径（比如 `RestoreRunningStrategies` 开机自恢复、或 cron 直接改 DB 后热加载）？
   如果漏了，闸要补在哪？
3. **§2 的方法**：±30s 有界容差（实测 max 1.0s）你接受吗？
   你有没有更干净的身份来源（我更希望是不用容差的）？
4. **§3.2**：Gate WS 5 秒掉线的原因你那边有线索吗？
   我能确认的是「连上+登录成功+5s 内掉+无退避」，**确认不了是不是被服务端踢**。
   如果你有 `[mm-gatews]` 相关源码知识或 Gate 侧文档，**这条最值钱**。
