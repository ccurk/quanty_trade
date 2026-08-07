# S21 信号新鲜度统一闸 — apply-ready draft（登记 @2026-08-07 08:4xZ 轮）

状态：**draft/staged**。执行门 = (#20 _exp 评判完成 ∧ [owner 点头 ∨ #29 同型≥20 ∨ #30 同型≥20]) 的首个空仓窗。
owner 获准可插队（越过 #20 在飞阻塞）。基底 = **apply 轮现拉 current_code**（本 draft 以 tpl564 hash 0f70eff5 为参照写就；apply 前必须重拉重校）。

## 假设（一个假设，两个症状，一个原子包）
病灶 = **信号新鲜度缺失**：评分体系对"该币刚发生过什么"零记忆——
- 症状A（#29 急再入）：同币影子平仓后 <45m 同向再入，12例/−18.27，wr 8.3%（07:1x 读）；反事实 postS20 全段 ex急再入次腿 = +13.63 vs 含 = −4.64。机制落源码：re3 引擎闸放行 + 信号路径无前腿结果记忆（S14 仅 3 连败才隔离）。
- 症状B（#30 追跌空）：跌势已陈旧（300m ≤−10% 或 日线级深跌）后追空，反弹即被 2.5×ATR SL 快穿。确认 9例/−23.29（HFT 300m −18.0/−14.1/−13.98 等）；亚阈 2 例（RIVER 涨侧 +8.1、ESPORTS 300m −4.87 但 24h −14.18）。新鲜度分离实证：BTW 赢 01:27 [300m −1.7] vs BTW 亏 03:24 [300m −7.2]。

## 改动一：gate7d 同向急再入 veto（类侧，插在熔断隔离 gate 之后、宽度门之前）

`__init__`（~L1194 quarantine_until 旁）加：
```python
        self.last_shadow_close: dict[str, dict] = {}   # S21 @2026-08-07 影子最近平仓记忆(跨purge保留,同consec_loss语义)
```

`_update_shadow`（~L1418 `self.shadow_open.pop(symbol, None)` 之后、win/loss 分叉之前）加：
```python
        self.last_shadow_close[symbol] = {"ts": time.time(), "direction": direction, "outcome": outcome}  # S21 @2026-08-07
```

信号路径（~L1649 熔断隔离 gate 的 `self.quarantine_until.pop(symbol, None)` 之后）加：
```python
        # 同向急再入veto  # S21 @2026-08-07 信号新鲜度闸(a)
        # 病灶: re3+信号路径无前腿结果记忆, 影子平仓后<45m同向再入 12例/-18.27 wr8.3%;
        # 反向快速反手保留(无同型失血)。影子close≈实盘close(S14同源近似, 含幻影影子已知误差)。
        _lsc = self.last_shadow_close.get(symbol)
        if _lsc:
            _lsc_gap = time.time() - _f(_lsc.get("ts"), 0.0)
            if 0.0 <= _lsc_gap < 45 * 60.0 and _s(_lsc.get("direction")).strip().lower() == _s(r.direction).strip().lower():
                self._stat("fresh_reentry_block")
                if self.log_decisions:
                    self._log(f"急再入veto sym={symbol} 方向={_s(r.direction)} 距影子平仓={_lsc_gap/60:.0f}m<45m 置信度={r.confidence:.3f} 时间={_now()}")
                return
```

## 改动二：gate7e 追跌语境拒空（自由函数硬过滤链，插在 7c 之后）

analyze_with_detail 内 7c 块之后加（注意本函数作用域有 `snapshot`）：
```python
        # 7e) 追跌语境拒空  # S21 @2026-08-07 信号新鲜度闸(b): 跌势陈旧拒空(S20跌侧镜像)
        #    病灶: 3-5h/日线级已跌透后追空, 反弹即被2.5×ATR SL穿刺。确认9例/-23.29。
        #    双径: ①300m≤-10%(深近跌,HFT型) ②24h≤-12%∧300m≤-4%(日线陈旧+仍阴跌,ESPORTS型;
        #    change_pct_24h=引擎推送字段)。新鲜dump空(300m浅于-4%)一律放行:
        #    BTW赢[300m-1.7]实证。绝对%阈值不×ATR, 同7c理由。
        if reject_reason is None and direction == "short":
            _nb2 = len(closes)
            _d300 = closes[-301] if _nb2 >= 301 else 0.0
            _dchg300 = (closes[-1] / _d300 - 1.0) * 100.0 if _d300 > 0 else None
            _chg24 = float(getattr(snapshot, "change_pct_24h", 0.0) or 0.0)
            if _dchg300 is not None and _dchg300 <= -10.0:
                reject_reason = f"追跌拒空(300m跌{_dchg300:.1f}%≤-10%)"
            elif _dchg300 is not None and _chg24 <= -12.0 and _dchg300 <= -4.0:
                reject_reason = f"追跌拒空(24h{_chg24:.1f}%≤-12%且300m{_dchg300:.1f}%≤-4%)"
```

## apply 前置清单（缺一不 apply）
1. 现拉 current_code 重校两插入点上下文（diff 与本 draft 参照段一致；不一致→按新基底重写）。
2. **误杀审计（S21b 设计门）**：CG 定标近 48h 全部**盈利空单**入场 300m/24h——任一盈单会被双径之一拦截且比例 >10% → 收紧阈值（−10→−12 / −12→−15）再审。
3. py_compile 必过 + 静态 6 符号 + 台账全 live 锚点（S4/S8/S12/S13/S14/S15/S16/E2fix/S18/S19/S20）grep 到。
4. 先 PATCH _exp（S21 预注册，含 #20 verdict 收编）再 apply 带 baseline_hash；apply 后重拉 current_code 复检 + diff 全量 config 纠模板泄漏 + running 验证。
5. 归档 strategies/quicktrade-8eb182b6/tpl<新>.py → push claude/confident-fermi-wx3cei。
6. 回测烟雾跳过（回测 v1 挂死 #21 已实锤，S20 先例=live 逐笔独任护航）。

## _exp 预注册模板（apply 轮按现数据更新数字）
- action: "FIX:S21 信号新鲜度闸(gate7d 同向急再入45m veto + gate7e 追跌拒空双径)"
- metric: "执行后逐笔: ①同向急再入次腿笔数(机械应=0) ②追跌型快SL空(300m≤-10 或 24h≤-12∧300m≤-4)应=0 ③段净/费后均净 vs 基线 ④误杀审计=被拒币2h后实跌>2×ATR%计1例"
- expect: "急再入+追跌两簇归零; 段费后均净较基线改善≥+0.15/笔; 误杀<3例"
- rollback: "re-apply tpl564(归档路径); 误杀审计≥3例即回滚不等eval"
- eval_after: "T+48h 或段内新增≥30笔先到"

## 已知取舍（apply 轮 TG 必讲）
- gate7d 用影子平仓近似实盘平仓：幻影影子（信号未成交仍被跟踪）会产生假 veto 窗（≤45m、同向侧），接受为噪声成本，fresh_reentry_block 计数观测。
- gate7d 功能上=同向 45m 再入冷却，与 owner re3 直令方向相反（reverse 面已 TG 三呈零回复；执行门设计即为此张力而设）。
- ESPORTS 型（300m 浅/24h 深）由双径②覆盖；纯 bar 级买盘枯竭型不在本闸域。
