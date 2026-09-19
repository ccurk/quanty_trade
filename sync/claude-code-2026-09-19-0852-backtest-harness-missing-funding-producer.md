# 回测 harness 没喂 funding_rate / ls_ratio —— 你的 S35 实验目前测的是「没接上行情」的引擎
（claude 侧，2026-09-19 08:52 UTC。**结论请你复核，尤其第 3 节。**）

## 1. 先说结论

你今天 08:39:38 上线了 `Majors_S35_bar_agg15_20260919-0839`（template id=1056），
Majors 实例 08:39:40 重启接上了它。**但那条链上缺一个生产者**：

模板 1056 的 code（107,928 B / 2,115 行）里，`funding_rate` 与 `ls_ratio` **只有消费者，没有生产者**。

```
extra = msg.get("extra") if isinstance(msg.get("extra"), dict) else {}
fr = _f(extra.get("funding_rate"), 0.0)
lr = _f(extra.get("ls_ratio"), 1.0)
```

**这两个默认值 `0.0` / `1.0`，恰好就是旧引擎里被钉死的常量。**
而整份 code 里：

| 关键串 | 出现次数 |
|---|---|
| `premiumIndex` | **0** |
| `/futures/data` | **0** |
| `longShortRatio` | **0** |
| `lastFundingRate` | **0** |
| `def fetch` / `def _fetch` | **0** |

唯一的网络调用是 `fapi/v1/klines`（和它的 `data-api.binance.vision` 镜像），用于「S35 种子预热」。

## 2. 运行时证据（这个是决定性的）

模板自报行 `资金费率={r.funding_rate:+.5f} 多空比={r.ls_ratio:.2f}`，今日共 11 条，**全部**：

```
资金费率=+0.00000  多空比=1.00
```

样本里含 **BTC/USDT（价格 81076.7）和 SOL/USDT（113.38）** —— BTC 的当期资金费在现实中不可能正好是 0.00000。

## 3. ⚠️ 口径边界（请你就这一条反驳我，如果我对了就不用）

**这 11 条全部带前缀 `[backtest strategy]`** —— 是**回测路径**打的。

所以我**能确定**的只有：**回测 harness 没有把这两个字段喂进去**。
⇒ 你的 S35 实验（sandbox `ce84012b` 带 `sim_clock`、以及回测对比）**测的仍是「crypto 原生输入未生效」的引擎**，
不能用来声称「新引擎的原生输入有效」或「无效」。

我**不能确定**的是实盘路径。反而有证据说实盘**可能**是通的：

- `backend/internal/exchange/binance.go:1093 refreshFunding()` → `GET /fapi/v1/premiumIndex`，取 `lastFundingRate`
- `:1111 refreshLS()` → `GET /futures/data/globalLongShortAccountRatio`（period=5m）
- `:1070 refreshOI()` → `GET /futures/data/openInterestHist`
- `backend/internal/strategy/strategy_dataflow.go:478-480` 把 `oi_change_pct` / `funding_rate` / `ls_ratio`
  塞进 **live candle 的 `Extra`**，再 `redisBus.PublishCandle(...)`
- 这三个 URL 字符串在**部署中的二进制 `/app/main`**（Sep 16 14:41）里**都存在**
  （`globalLongShortAccountRatio` ×1、`premiumIndex` ×1、`lastFundingRate` ×2）

⇒ **实盘链路可能已经通了，是回测 harness 单独缺这一环。** 我没有实盘样本，不下结论。

**判别实验（一条就够）**：在**实盘**信号路径上用同一个模板打一行自报（或直接读 `marketFloat` 的 `fr:`/`ls:` 缓存），
对 BTC/ETH 各取一次：
- 非 0 / 非 1 ⇒ 实盘已修，只需补回测 harness
- 仍是 0 / 1 ⇒ 两条路径都没接，`funding_rate`/`ls_ratio` 在 S35 里是**装饰**

## 4. 我欠的一处更正（与我上次发你的文件冲突）

9:00 那份文件里，我为了撤回 #32 的「仓位被放大 4 倍」，写了：

> 主池今天只有一次启动事件 00:25:21 pid=49077，此后无重启，status 全天 running。

**这句是错的。** 今日 `Process started` 有 **6 次**：

| 时刻 | 实例 | symbols | 说明 |
|---|---|---|---|
| 00:25:21 | 8eb182b6 | **300** | |
| 03:41:10 | 8eb182b6 | **300** | |
| 07:13:27 | 8eb182b6 | **7** | G/BULLA/MAGMA/UAI/HEI/DRIFT/ARB |
| 07:21:21 | 8eb182b6 | 7 | |
| 08:01:47 | e725e31a | 5 | BTC/ETH/BNB/SOL/XRP |
| 08:39:40 | e725e31a | 5 | 接新模板 1056 |

（注意：这行日志是 **Python 信号子进程**的启动 —— `candle_ch`/`signal_ch` 是它的 Redis 频道 ——
不等于 Go 实例 stop/start。语义上要分清。）

**撤回的结论本身我认为仍成立**（成交规模没跟着 pct 跳：pct 0.0625→0.25 是 4×，实测 U/笔 37.8→42.2 只 1.12×），
但现在多了一个更可能的解释：04:49 把 `order_amount_pct` 写成 0.25，**07:08 又改回 0.125**，而下次启动是 07:13
⇒ **0.25 从未上线**。这是**竞争假设，未证**。

## 5. 一处我裁决不了、请你帮忙的矛盾

- **源码**：`manager.go:1729 UpdateStrategyConfig` 第一句就是
  `if inst.Status == StatusRunning { return fmt.Errorf("cannot update config while strategy is running") }`
- **审计**：今日 22 条 `patch_config` **全部 HTTP 200 成功**，其中 `08:42:49`(e725e31a)、`08:43:43`(8eb182b6)
  发生时两个实例都在 running；03:22 / 04:49 / 07:08 由 `admin` 改的还是**行为键**（`order_amount_pct` 等）
- **结果**：主池 `symbols` **300 → 7** 确实到了引擎

⇒ 至少某些配置变更**确实能到运行中的实例**。

但审计本身有局限：**22 条里 `before_json` 全是空的** ⇒ 它记的是**请求**，不是**状态 diff**，
HTTP 200 ≠ 引擎采纳。

**请你看一眼 `backend/internal/api/strategy_handlers.go:293` 与 `:308` 那个 handler 本体**：
它是不是第 4 条绕过 `UpdateStrategyConfig` 的赋值路径？还是实例当时其实不在 `running`？
这条决定我该不该把 #32 的机制论据彻底作废。

## 6. 顺手记下的观察（不用回）

- Majors 现在：`max_hold_minutes=720`(12h) + `hunger_mode_enabled=false` + `bar_agg_minutes` 键存在 + `hunger_stop_loss_pct=0.3`
- 主池现在：`symbols` 固定 7 个 meme + `auto_symbols=True→False`
- ⇒ 我此前测出来「边际全在 h≥4h，45min 在死区」和「hunger 通道占软件平仓总亏 92%」两条，
  **第一次有了真实检验**。Majors 目前 0 在仓、n=17 笔 —— 样本还不够，我先不动结论。

## 7. 我这边今日的口径（对齐用）

- 交易所 `/fapi/v1/income`，09-19 00:00Z→08:48Z：净 **−47.0861**（REALIZED −29.4155 / COMMISSION −17.7084 / FUNDING +0.0378）
- 毛 −29.3777，**费占 |毛| 60.3%**
- 按实例归因（身份←`strategy_positions` 区间，金额←`income`，±30s 容差，覆盖 100%）：
  主池 `8eb182b6` −45.86 / n=1331；Majors `e725e31a` −1.26 / n=17
- 账户当前 **全平**（equity 160.5609，持仓 0）
