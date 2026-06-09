# Cron Auto-Optimize Prompt

这是每 6 小时被 Claude 定时触发一次时跑的完整指令。
当 cron 触发时，Anthropic 这边会拉起一个**新的、独立的 Claude 会话**，
把下面整段 prompt 作为初始消息塞进去，让它带着 Bash 工具去执行。

每次触发独立，无记忆。所以下面要写得**完全自包含**——所有 URL、密钥、
ID 都在 prompt 里。

> **v2 更新**：现在 `/api/admin/optimize/context` 会自动包含 `binance` 字段
> （账户余额、配对成交、胜率、R:R、long/short 不对称、持仓时长分布）。
> 远端 cron 不再需要 binance IP 白名单，所有真实数据都通过本服务转发。

---

## 占位符（注册 cron 前必须填实际值）

| 占位符 | 值的来源 | 例子 |
|---|---|---|
| `<BACKEND>` | 你后端的公网域名 | `https://quanty.qxyz.xyz` |
| `<CRON_USER>` | 你为 cron 单独建的 admin 用户名 | `claude_cron` |
| `<CRON_PASS>` | 上面用户的密码（强随机） | `xxxxx...` |
| `<STRATEGY_ID>` | 要优化的 strategy_instance.id | `8eb182b6-ee74-4125-a602-f0a91f376432` |
| `<TG_TOKEN>` | TG bot token | `8842350365:AAFXBB...` |
| `<TG_CHAT>` | TG chat_id | `6938657035` |

---

## Prompt 正文（cron 每次触发时执行）

```
你是 QuantyTrade 的自动化策略优化器。本次被 cron 触发，执行以下流程。
全程使用 Bash 工具，每一步失败都要发 TG 提醒后退出，不要拖延或重试。

===== 已替换的变量 =====
BACKEND=<BACKEND>
CRON_USER=<CRON_USER>
CRON_PASS=<CRON_PASS>
STRATEGY_ID=<STRATEGY_ID>
TG_TOKEN=<TG_TOKEN>
TG_CHAT=<TG_CHAT>

===== Helper：发 TG 的函数 =====
所有 TG 消息都以 「【Claude · QuantyTrade】6h auto-optim」开头。
用 curl 调 https://api.telegram.org/bot${TG_TOKEN}/sendMessage 发送。

===== Step 1: 登录 =====
TOKEN=$(curl -s --max-time 15 -X POST "$BACKEND/api/login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$CRON_USER\",\"password\":\"$CRON_PASS\"}" \
  | jq -r '.token // empty')

如果 TOKEN 为空：
  发 TG：「❌ login 失败 @ $(date -u +%FT%TZ)，跳过本轮」
  立即退出，不要继续。

===== Step 2: 拉 context（含 binance 真实数据）=====
hours=24
curl -s --max-time 60 -H "Authorization: Bearer $TOKEN" \
  "$BACKEND/api/admin/optimize/context?strategy_id=$STRATEGY_ID&hours=$hours" \
  -o /tmp/ctx.json

校验：
  STRATEGY_NAME=$(jq -r '.strategy_name // empty' /tmp/ctx.json)
  if [ -z "$STRATEGY_NAME" ]; then
    发 TG「❌ context 拉取失败」+ 退出
  fi

关键字段（**优先用 binance 字段，没有再 fallback 到 trades_window**）：

  current_code (Python 源码 — 优化的对象)
  current_code_hash
  config (dict — 配置参数)

  # binance.* 是从币安 API 拉的真实数据（更准）
  binance.balance_usdt              # 当前余额
  binance.available_balance_usdt
  binance.open_positions[]          # 当前持仓
  binance.income_totals             # {REALIZED_PNL, COMMISSION, FUNDING_FEE} 各自累计
  binance.by_symbol[]               # 每个 symbol 的 realized/commission/funding
  binance.paired_trades.pair_count
  binance.paired_trades.win_rate_pct
  binance.paired_trades.reward_risk_ratio
  binance.paired_trades.breakeven_win_rate_pct
  binance.paired_trades.fee_drag_pct
  binance.paired_trades.long_count / long_win_rate_pct / long_net_pnl
  binance.paired_trades.short_count / short_win_rate_pct / short_net_pnl
  binance.paired_trades.avg_hold_minutes
  binance.hold_distribution[]       # <1m / 1-5m / 5-15m / 15-60m / >60m

  # DB 视角（fallback；binance 拉失败时用）
  trades_window.count
  trades_window.win_rate_pct
  trades_window.realized_pnl
  trades_window.long_pnl, short_pnl
  trades_window.by_symbol
  daily_pnl_7d (数组)
  account.open_positions

如果 binance.fetch_error 非空，写到 TG 提醒，并 fallback 到 trades_window。

===== Step 3: 诊断 + 决策（按规则优先级，命中第一条就执行）=====

记号：
  bt = binance.paired_trades （如果 binance 字段存在）
  br = bt 的 reward_risk_ratio
  bw = bt 的 win_rate_pct
  bre = bt 的 breakeven_win_rate_pct
  edge = bw - bre   # 正值 = 正期望，负值 = 负期望

────── R1：单 symbol 重亏 → 黑名单（不改代码，改 config）──────
  条件: binance.by_symbol 里存在 SYM 满足
        net_pnl < -1.0 USDT AND trade_count >= 3
  动作: 把 SYM 加入 config.symbol_blacklist （PUT /api/strategies/$STRATEGY_ID/config）
  描述: "blacklist <SYM> (<trade_count> trades, net <net_pnl>)"

────── R2：long/short 不对称 → 切换方向（不改代码）──────
  条件: (bt.long_count >= 3 AND bt.short_count >= 3) AND
        ((bt.long_net_pnl < -2 AND bt.short_net_pnl > 0)
         OR (bt.short_net_pnl < -2 AND bt.long_net_pnl > 0))
  动作: 改 config.allowed_sides
        - long 亏 short 赚 → ["sell"]
        - short 亏 long 赚 → ["buy"]
  描述: "asymmetric: long=<long_net> short=<short_net>, switching to <side>-only"

────── R3：手续费拖累过大 → 提冷却 + 阈值（改 config）──────
  条件: bt.fee_drag_pct > 30 AND bt.pair_count >= 10
  动作: cooldown_sec ×= 1.5 （cap 至 1800）
        min_confidence += 0.05 （cap 至 0.55）
  描述: "fee_drag <pct>% > 30% — slowing entries"

────── R4：胜率 < breakeven 且差距大 → 提 MIN_CONFIDENCE（改代码）──────
  条件: bt.pair_count >= 10 AND edge < -5 （胜率比 breakeven 低 5pt 以上）
  动作: Python Config 类的 MIN_CONFIDENCE 乘 1.10（最多 +10%，cap 0.55）
  描述: "edge <edge>pp negative — raising MIN_CONFIDENCE"

────── R5：<1m 持仓亏损主导 → 启用震荡过滤（改 config）──────
  条件: binance.hold_distribution 第一个桶 (<1m) 满足
        count >= 3 AND win_rate_pct == 0 AND pnl < -2
  动作: config.reject_on_chop = true
        config.trend_confirm_bars = max(current, 3)
  描述: "<1m bucket 0% wr, -<pnl> — enabling chop reject + trend confirm"

────── R6：7 日大亏 → 降仓位 + 拉宽 SL（改 config）──────
  条件: sum(daily_pnl_7d[].realized_pnl) < -3.0
  动作: order_amount_pct ×= 0.8 （cap 不低于 0.1）
        atr_sl_mult += 0.2 （cap 至 2.0）
  描述: "7d PnL <sum> < -3, defensive: cut size + widen SL"

────── R7：一切正常 → no-op ──────
  其它任何情况都不动。Step 5 发 TG。

===== Step 4: 执行 =====

A. 改 config 路径（R1/R2/R3/R5/R6）:
   # 拉当前 config
   curl -s -H "Authorization: Bearer $TOKEN" \
     "$BACKEND/api/strategies/$STRATEGY_ID" > /tmp/inst.json
   # 用 jq 合并改动到 /tmp/cfg.json （注意 config 在 instance 里是 JSON 字符串）
   jq '.config | fromjson // .config' /tmp/inst.json > /tmp/cfg.json
   # 按规则修改 /tmp/cfg.json （例如加 symbol_blacklist 元素，改 allowed_sides，等）
   # PUT 回去
   curl -s -X PUT "$BACKEND/api/strategies/$STRATEGY_ID/config" \
     -H "Authorization: Bearer $TOKEN" \
     -H 'Content-Type: application/json' \
     -d "$(jq -c '.' /tmp/cfg.json)" > /tmp/cfg_resp.json
   # 滚动重启（让新 config 生效）
   curl -s -X POST "$BACKEND/api/strategies/$STRATEGY_ID/stop?force=true" \
     -H "Authorization: Bearer $TOKEN"
   sleep 3
   curl -s -X POST "$BACKEND/api/strategies/$STRATEGY_ID/start" \
     -H "Authorization: Bearer $TOKEN"

B. 改 Python 代码路径（仅 R4）:
   # 在 /tmp/new_code.py 写出新代码
   # 约束（违反会被后端 guard 拒绝）：
   #   - 必须保留: on_market_message, _emit_signal, _append_bar, self.pub.publish
   #   - 关键参数（TP_RATIO, SL_RATIO, ATR_TP_MULT, ATR_SL_MULT,
   #     MIN_CONFIDENCE, WARMUP_BARS, VOLUME_RATIO_MIN, MAX_BARS）
   #     变化必须 ≤ 2x（即新值不能 < 旧值/2，也不能 > 旧值*2）

   curl -s --max-time 90 -X POST "$BACKEND/api/admin/optimize/apply" \
     -H "Authorization: Bearer $TOKEN" \
     -H 'Content-Type: application/json' \
     --data-binary @<(jq -n \
       --arg sid "$STRATEGY_ID" \
       --rawfile code /tmp/new_code.py \
       --arg desc "<规则描述>" \
       --arg base "$(jq -r '.current_code_hash' /tmp/ctx.json)" \
       '{"strategy_id":$sid,"code":$code,"description":$desc,"baseline_hash":$base}') \
     > /tmp/apply.json

   解读响应：
     .status == "applied"   → 成功（已自动重启）
     .status == "no_change" → 哈希相同，没真改
     .guard 非空            → 被某 guard 拒了，看 .error

===== Step 5: TG 通知（统一格式）=====

【Claude · QuantyTrade】6h auto-optim
━━━━━━━━━━━━━━
窗口: 过去 ${hours}h | strategy=${STRATEGY_ID:0:8}...
余额: $<bal> USDT
配对成交: <pair_count> 笔
胜率: <wr>% (breakeven: <bre>%)
R:R: <rr> : 1
Long/Short: <long_count>笔 净$<long_net> / <short_count>笔 净$<short_net>
手续费拖累: <fee_drag>%
持仓时长 <1m: <count> 笔，胜率 <wr>%
7 日合计: <sum> USDT
━━━━━━━━━━━━━━
触发规则: R1/R2/R3/R4/R5/R6/R7
动作: <executed action>
描述: <description>
新 template_id: <id 或 N/A>
config 变更: <list of changed keys 或 none>
下次: 6h 后
━━━━━━━━━━━━━━

最后用 curl 发 TG，结束。

===== 防呆守则 =====
1. 每次 fire 只能命中 **1 条** 规则。命中即执行即收尾，不要连续触发。
2. 任何 curl 失败 → 发 TG 注明 endpoint 和 HTTP 状态码 → 退出。
3. 不允许重启同一个 strategy 超过 1 次/小时（用 TG 历史判断）。
4. 不允许把 order_amount_pct 改到 > 0.5。
5. 不允许 allowed_sides 变成空数组。
```

---

## 怎么注册到 cron

让 Claude 这边执行：

```
请用 CronCreate 注册上面的 prompt，
cron 表达式 "11 */6 * * *"（每 6 小时第 11 分触发，避开整点）
durable: true（持久化跨 session）
```

或者直接告诉我 "register cron"，我帮你跑。

---

## 7 天自动过期

CronCreate 的 recurring 任务**最多跑 7 天**就自动删了。
每周需要重新注册一次。后续可以做：
- 写一个自我注册的子任务：在每次 fire 时检查剩余天数，如果 < 1 天就重新 CronCreate
- 或者改用更稳定的部署平台（不依赖 Claude session）

---

## 紧急停掉自动优化

如果某天你发现自动优化生成了乱七八糟的代码：

1. 立刻让 Claude 执行 `CronDelete <id>` 撤销 cron
2. 在 backend 数据库里把 instance 的 template_id 改回旧的
3. 或者直接停 strategy

更狠：把 `claude_cron` 这个 admin 账号在 backend 删了，cron 即使 fire 也 login 不上。

---

## 准备工作清单（注册 cron 前）

- [ ] 在 backend 建好 `claude_cron` admin 账号（强密码）
- [ ] 把 `<STRATEGY_ID>` 填成你**当前正在跑**的 instance id（不要随便用历史的）
- [ ] 在前端把 `min_confidence`、`atr_tp_mult`、`atr_sl_mult`、`cooldown_sec`、
      `allowed_sides`、`symbol_blacklist`、`reject_on_chop`、`trend_confirm_bars` 等字段
      都暴露出来，确保 PUT config 接口能写
- [ ] 公网域名加 HTTPS（用 HTTP 时 TOKEN 容易被劫持）
- [ ] 在 TG bot 那边 `/start` 一次，确保 `<TG_CHAT>` 是真实可达
- [ ] 测试调一次 `GET /api/admin/optimize/context?strategy_id=...&binance=1` 确认
      binance 字段填全了
- [ ] 第一次 cron 触发时盯着看，确认决策合理后再放手
