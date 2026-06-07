# Cron Auto-Optimize Prompt

这是每 6 小时被 Claude 定时触发一次时跑的完整指令。
当 cron 触发时，Anthropic 这边会拉起一个**新的、独立的 Claude 会话**，
把下面整段 prompt 作为初始消息塞进去，让它带着 Bash 工具去执行。

每次触发独立，无记忆。所以下面要写得**完全自包含**——所有 URL、密钥、
ID 都在 prompt 里。

---

## 占位符（注册 cron 前必须填实际值）

| 占位符 | 值的来源 | 例子 |
|---|---|---|
| `<BACKEND>` | 你后端的公网域名 | `https://quanty.qxyz.xyz` |
| `<CRON_USER>` | 你为 cron 单独建的 admin 用户名 | `claude_cron` |
| `<CRON_PASS>` | 上面用户的密码（强随机） | `xxxxx...` |
| `<STRATEGY_ID>` | 要优化的 strategy_instance.id | `84648b05-54dd-4f30-9e4e-efd7fa84de22` |
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

===== Step 2: 拉 context =====
curl -s --max-time 30 -H "Authorization: Bearer $TOKEN" \
  "$BACKEND/api/admin/optimize/context?strategy_id=$STRATEGY_ID&hours=24" \
  -o /tmp/ctx.json

校验：
  STRATEGY_NAME=$(jq -r '.strategy_name // empty' /tmp/ctx.json)
  如果 STRATEGY_NAME 为空 → TG「❌ context 拉取失败」+ 退出。

读取关键字段：
  current_code (Python 源码)
  current_code_hash
  trades_window.count
  trades_window.win_rate_pct
  trades_window.realized_pnl
  trades_window.long_pnl, short_pnl
  trades_window.by_symbol
  account.open_positions
  daily_pnl_7d (数组)
  config

===== Step 3: 判定是否需要改动 =====
按以下规则依次检查，第一条命中的执行对应改动。
全部不命中 → 跳到 Step 6 发 TG no-op 后退出。

规则 R1 — 胜率过低 → 提 MIN_CONFIDENCE
  条件: trades_window.count >= 5 AND trades_window.win_rate_pct < 25
  改动: current_code 里 Config 类的 MIN_CONFIDENCE 乘 1.10（最多 +10%）
  描述: "win rate <X>% < 25%, raising MIN_CONFIDENCE by 10%"

规则 R2 — 单 symbol 亏损 → 加黑名单
  条件: trades_window.by_symbol 中存在 SYM 满足 count >= 3 AND pnl < -1.0
  改动: 这条不改 Python 代码，改 config——把 SYM 加入 config.symbol_blacklist
  执行方式: 调 /api/admin/optimize/apply 时传 current_code 本身（hash 相同会被
            后端识别为 no_change）。**改 config 走另一条路径**：用 PUT
            /api/strategies/<STRATEGY_ID>/config 更新 config，不动模板代码。
  描述: "blacklist <SYM> due to <count> trades, NET <pnl>"

规则 R3 — 7 日累计大亏 → 提 stop_loss
  条件: sum(daily_pnl_7d[].realized_pnl) < -3.0
  改动: Python Config 里 SL_RATIO 乘 1.3（最多+30%），或者改 config.stop_loss_pct
  描述: "7d PnL <X> < -3 USDT, defensive: tighten stop_loss"

规则 R4 — long 持续亏损但 short 还赚
  条件: trades_window.long_pnl < -2.0 AND trades_window.short_pnl > 0
  改动: 确认 config.allowed_sides 包含 ["sell"]。如果当前不是 sell-only，
        改成 ["sell"]。这条同样走 config 更新，不动模板代码。
  描述: "long bleed, switching to short-only"

规则 R5 — 啥都没满足
  no-op。Step 6。

===== Step 4: 生成新代码（仅当 R1 或 R3 命中需要改 Python 代码时）=====
基于 current_code 做最小化修改，输出完整文件。

约束（违反会被 backend 的 guard 拒绝）：
  - 必须保留: on_market_message, _emit_signal, _append_bar, self.pub.publish
  - 任何关键参数（TP_RATIO, SL_RATIO, ATR_TP_MULT, ATR_SL_MULT,
    MIN_CONFIDENCE, WARMUP_BARS, VOLUME_RATIO_MIN, MAX_BARS）
    变化必须 < 2x（即新值不能 < 旧值/2，也不能 > 旧值*2）

把改完的 Python 完整内容写入 /tmp/new_code.py

===== Step 5: Apply =====
A. 如果需要改 Python 代码（R1 或 R3）:
   curl -s --max-time 60 -X POST "$BACKEND/api/admin/optimize/apply" \
     -H "Authorization: Bearer $TOKEN" \
     -H 'Content-Type: application/json' \
     --data-binary @<(jq -n \
       --arg sid "$STRATEGY_ID" \
       --rawfile code /tmp/new_code.py \
       --arg desc "<上面规则的描述>" \
       '{"strategy_id":$sid, "code":$code, "description":$desc}') \
     > /tmp/apply.json

B. 如果只是改 config（R2 或 R4）:
   # 拉当前 config
   curl -s -H "Authorization: Bearer $TOKEN" \
     "$BACKEND/api/strategies/$STRATEGY_ID" > /tmp/inst.json
   # 用 jq 合并改动
   jq '.config | fromjson' /tmp/inst.json > /tmp/cfg.json
   # 改 /tmp/cfg.json 加 symbol_blacklist 或 allowed_sides
   # PUT 回去
   curl -s -X PUT "$BACKEND/api/strategies/$STRATEGY_ID/config" \
     -H "Authorization: Bearer $TOKEN" \
     -d "$(jq -c '.' /tmp/cfg.json)"
   # 然后 force restart
   curl -s -X POST "$BACKEND/api/strategies/$STRATEGY_ID/stop?force=true" \
     -H "Authorization: Bearer $TOKEN"
   sleep 3
   curl -s -X POST "$BACKEND/api/strategies/$STRATEGY_ID/start" \
     -H "Authorization: Bearer $TOKEN"

解读 apply 响应：
  - .status == "applied" → 成功
  - .status == "no_change" → 哈希相同（说明没真改）
  - .guard 字段存在 → 被某 guard 拒了

===== Step 6: TG 通知 =====
统一格式（不要省略字段，没值就写 N/A）：

【Claude · QuantyTrade】6h auto-optim
━━━━━━━━━━━━━━
窗口: 过去 24h
交易: <count> 笔 | 胜率 <win_rate>% | NET <realized_pnl> USDT
7 日合计: <sum daily_pnl_7d> USDT
持仓: <open_positions> 个
━━━━━━━━━━━━━━
触发规则: R1/R2/R3/R4/R5
动作: applied / no-op / rejected (guard=<name>)
描述: <上面 Step 3 决定的 description>
新 template_id: <id 或 N/A>
下次: 6h 后
━━━━━━━━━━━━━━

最后用 curl 发 TG，结束。
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
