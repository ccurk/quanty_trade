# 策略组模式 + WS 实时仓位管理（2026-08-09 owner 直令）

## 一、已上线（不依赖部署，当前生产引擎即生效）

**5 载具编队**（一仓一主、币池互不相交）：

| 载具 | id | 状态 | 池 | 关键配置 |
|---|---|---|---|---|
| main 通才 | 8eb182b6 | running | auto 100 − blacklist15 | pct0.08, sides=[buy](刹车), S18-S23 栈 |
| qt-trend-long | 827ffe8c | **running** | TUT/KAITO/BICO/DODOX/ACE | pct0.05 mcp3, trailing(act2.0/cb1.2)+BE1.2 |
| qt-fade-short | 21519f1b | stopped | BTW/1000CAT/BLUAI/AIOT | 解锁门=6h/24h 双转正（与刹车恢复同门） |
| qt-breakout-follow | 2111f5f9 | stopped | ARC/BTR/SAGA/STAR/AIO/BMT | 待新模板代码（见 activation_gate） |
| qt-lowvol-lev | ad37d337 | stopped | GUA(占位) | 待 feed ATR% 画像填池 |

- 池互斥 v1 靠 config：main `symbol_blacklist` 拉黑 专家池9币 + 隔离区6币
  (CYS/TST/龙虾/4/ON/COOKIE = 48h 六大稳定失血源，任何载具不得交易，
  除非未来某原型明确以其行为设计并预注册)。
- trailing/breakeven 是 7 月已部署的引擎能力（strategy_exit.go），配置即生效。
- 专家启动窗checklist：预注册 _exp → 把该池币加入 main blacklist（同窗）→ start。

## 二、引擎补丁（本分支 claude/dev-strategy-group-ws，候 owner 部署）

1. **跨策略同币互斥闸**（strategy_signal.go）：候选循环里查同 owner 其他策略
   的 open 行，命中即拒（"组内其他策略持有"）。config 配错时的兜底，
   单向持仓模式下防交易所净额合并搅乱出场三层语义。
2. **无主仓收养尊重 blacklist**（strategy_roi_monitor.go findGuardStrategyInstance）：
   防 main 把专家池仓位错认收养。
3. **WS 标记价加速守护**（binance_markprice_hub.go + strategy_ws_guard.go）：
   `!markPrice@arr@1s` 单连接全市场标记价 → 在持 symbol 位移≥0.15% 即触发
   一次完整 ROI 守护扫描（决策路径不变，仍是 scanROILimits）。
   TP/SL/保本/追踪反应延迟 5s→~1s。限流：单飞+700ms 最小间隔。
   关闭开关：env `WS_GUARD_DISABLED=1`。
4. **赢家金字塔加仓**（strategy_pyramid.go，默认关）：config
   `pyramid_enabled/pyramid_trigger_roi(6.0)/pyramid_add_frac(0.5)/pyramid_max_adds(1)`。
   **代码级硬规则：roi≤0 一律拒绝——亏损仓加仓（马丁/摊平）是禁区，先于任何配置。**
   加仓后 SL 按新均价平移重锚（只紧不松），TP 保持原绝对价（更早锁利）。
   计数在内存，重启最坏=对仍盈利仓多加一次，受 max_adds+roi 门再约束。

**中途加杠杆：不实现。** 理由：持仓中提杠杆会压缩清算距离，与 2.5×ATR 止损带
+ 物理护栏 lev≤100/(3.75×ATR%) 直接冲突；"赢家加码"的需求由金字塔满足
（不动清算距离）。如 owner 仍要，单独出补丁另议。

## 部署

```bash
git fetch origin claude/dev-strategy-group-ws
git checkout claude/dev-strategy-group-ws   # 或 cherry-pick 进 main
cd backend && go build ./... && ./deploy.sh  # 按现有部署流程
```

回滚：`WS_GUARD_DISABLED=1` 关 WS 加速；pyramid 默认关；互斥闸无行为面
（池不相交时永不触发）。整包回滚=部署上一版。

## 三、保证金纪律

引擎 sizing = 开仓时可用余额×pct×lev×mult，全组共享同一 avail（自阻尼）。
组约束：Σ(pct_i × mcp_i) ≤ 0.75。当前：main 0.08×5槽 + trend 0.05×3 = 0.55 ✓。
新载具启动前重算此式并写入预注册。
