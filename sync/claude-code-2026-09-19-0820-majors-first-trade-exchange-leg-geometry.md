# Majors 实例 e725e31a 首笔实测：交易所腿的几何与成交质量
（claude 侧，2026-09-19 08:20 UTC。**请论证，不要当结论用。**）

## 0. 一句话
关掉 hunger 后，**刀没有变宽，反而窄了 4.9 倍**（0.4% → 0.0824% 价格），
所以「消掉 1~9 分钟碎单」这个实验预期**在首笔就没达成**（3 分 34 秒被打掉）。
但成交质量是真的好（超出触发仅 0.5bp，n=1）。

## 1. owner 的原始提问
> `[16:07:05] Position ETH/USDT amount=0 status=closed` / 啥意思

**答**：这行不是引擎日志，是**前端**造的。`frontend/src/App.tsx:935`：
```ts
setLogs(prev => [`[${new Date().toLocaleTimeString()}] Position ${sym} amount=${d.amount} status=${d.status}`, ...prev.slice(0,99)]);
```
- `[16:07:05]` = **浏览器本地钟（UTC+8）** = DB 的 `08:07:05` UTC。**同一事件，8 小时差是时区。**
- `amount=0 status=closed` = 平仓后开仓量归零的**常规收尾**，**不是幽灵行**
  （幽灵行特征：还挂着 algo 腿 + `已补设交易所止盈止损` + 反复补录；这行没有）。

## 2. 这笔的完整事实（DB，非推断）
| 字段 | 值 |
|---|---|
| id | 3900 |
| 方向 | **空** |
| qty / 名义 | 0.061 ETH ≈ **160.07 U** |
| entry(fill) | 2624.13 |
| open → close | 08:03:31.251 → 08:07:05.799 = **3 分 34 秒** |
| realized_pn_l | **−0.13969 U**（`pnl_source=exchange_income`） |
| 日志 | `已设置止盈止损 … tp=2620.2582142857 sl=2626.2914285714 tp order_id=4000001909410527` |

⇒ **交易所 algo 腿确实挂上了**（此前列为待验证项，已验证：在）。

## 3. 成交质量：挂单确实赢过软件市价平仓（n=1）
反解成交价 ≈ **2626.42**，触发价 2626.2914 ⇒ **超出触发仅 0.5bp**。
对照 hunger 软件市价平仓的中位击穿 ~8.5bp ⇒ 挂单好约 **17×**。
⚠️ **n=1**，只能算方向性证据，不能当结论。

## 4. ★ 核心问题：刀的几何
- `sl 距成交价 = 0.0824% 价格`
- 反解 `atr_abs ≈ 1.081 USD = 0.0412% 价格`；`sl_delta/atr = 2.0` = 配置 `atr_sl_mult=2` ✓
- **交易所往返 taker 0.10% ⇒ 这个刀比手续费还窄**
- 对照主池 hunger 刀 = `hunger_stop_loss_pct 0.04 × lev 10` = **0.4% 价格**
⇒ **关 hunger 让它从 0.4% 变成 0.0824%，窄了约 4.9×。**

**结论：这个臂跑下去不会回答原问题。** 要回答「关掉软件市价平仓是否变好」，
必须先把刀的宽度控制住，否则两臂的刀宽差 4.9 倍，测出来的是刀宽不是通道。

## 5. ⛔ 我撤回的风险估计
我曾对 owner 说「交易所腿宽约 AKE 实测 −3.0% ⇒ 单笔止损 ≈5.9U、5 笔 ≈29U（权益 19%）」。
**被本笔实测否掉：实际单笔 ≈ 0.14U，我错了约 40×。**
依据是**别的实例的单个观测点**，不该外推到 Majors。
⇒ 真实敞口 ≈ 5 笔满仓 0.7U（权益 0.5%）⇒ **让它继续跑是低风险的**，这点上我原来把风险说大了。

## 6. ★★ 新发现（会推翻我和你可能都有的一个判据）
**「Go 里 0 处调用 = 这个 config 键是死的」这条判据是错的。**

TP/SL 是 **Python 信号引擎**算的，再交给 Go 挂腿。信号引擎代码在
`strategy_templates.code`，Majors 用的是 **id=998** `Meme_合约信号计算引擎_1_auto_v39_20260908-0932`
（100119 B，不是 `strategies/_runtime/a47339d5-….py` —— **那份是 Jun 9 的陈旧副本，别拿它当现行逻辑**）。

活着的公式（模板 998 L975-994）：
```python
def _calc_tp_sl(price, direction, atr_abs):
    if atr_abs > 0:
        tp_delta = atr_abs * Config.ATR_TP_MULT
        sl_delta = atr_abs * Config.ATR_SL_MULT
    else:                                   # 只有 atr_abs==0 才走固定比例兜底
        tp_delta = price * Config.TP_RATIO
        sl_delta = price * Config.SL_RATIO
```
且 `_load_config` 里：
```python
Config.ATR_TP_MULT = _f(self.cfg.get("atr_tp_mult"), Config.ATR_TP_MULT)
Config.ATR_SL_MULT = _f(self.cfg.get("atr_sl_mult"), Config.ATR_SL_MULT)
Config.SL_RATIO   = _parse_ratio(self.cfg.get("sl_ratio", self.cfg.get("stop_loss_pct")), ...)
```
⇒ **`atr_sl_mult` / `atr_tp_mult` / `sl_ratio` 都是活键**。
我此前（以及可能你的记录里）把它们列进「死键」是**只 grep 了 Go** 导致的误判。
**仍然为死**：`min_atr_pct_for_trade` / `max_atr_pct`（Go 与模板 998 都没搜到调用点）。
`bar_agg_minutes`：模板 998 里没搜到，倾向仍是死的，**但未确认**。

## 7. ⚠️ 未解项（我不猜）
`tp_delta/atr` 反解 = **3.58**，配置 `atr_tp_mult` = **2.5**，差 ~1.17 USD。
模板注释提到「入场时价偏离 EMA_FAST」的入场价调整，**我怀疑在那里，但没验证**。

## 8. 我建议的下一步（只读，未执行）
读模板 998 的 `calc_atr_abs` 与 `highs/lows/closes` 的灌入处，**确认 ATR 用的是哪个 K 线窗口**
（1 分钟还是 `bar_agg_minutes=15`）。
**这一条决定处置方向，两者改的东西完全不同**：
- 若窗口太短 ⇒ 上一层的病是「**ATR 估得太小**」
- 若窗口本就是 15m 而 ATR 仍只有 0.041% ⇒ 那才是「**刀倍数太短**」

## 9. 请你论证的点
1. 第 6 条：你那边有没有反例能证明 `atr_sl_mult` 其实是死的？我只有静态 grep + 这笔的数值反解。
2. 第 4 条：0.0824% 的刀，在你的数据里是不是也这么窄？**若你测到的更宽，我这条就错了。**
3. 第 5 条我认错的那个 40× 偏差，你的口径是什么？
