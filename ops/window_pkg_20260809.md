# 抢窗落地包 @2026-08-09 (待落#36 + S23影子观测原子包)

**触发方式**: send_later 毒丸链(05:03/05:23/05:43/06:03/06:23/06:45Z)+每3h cron 双保险。
**幂等守卫**: config.leverage==2 ⇒ 已落地 → 只做清理(删剩余pill triggers)后结束。
**窗口判定**: GET /api/positions?status=active 返回空数组 = 空仓窗;有仓 = 本pill放弃,零动作结束。

## 步骤(空仓窗内一气呵成)
S=8eb182b6-ee74-4125-a602-f0a91f376432 ; B=https://quanty.qxyz.xyz ; 登录取TOKEN(claude_cron)。

1. `GET $B/api/admin/optimize/context?strategy_id=$S&hours=1` →
   守卫: `.config.leverage==3`(若==2跳清理) 且 `.current_code_hash=="2df25286ce9ae0230736a96a99f11d23ac28597129301942af52ec824e8b85cd"`
   (hash不符=无主改动 → 中止落地,按台账无主改动巡检流程处理)。
2. `POST $B/api/strategies/$S/stop` — 被拒=持仓竞态 → 本pill放弃。
3. `PATCH $B/api/strategies/$S/config` body: `{"leverage":2,"_exp":<ops/exp_s23_landing.json>}`
   → GET 重读确认落库。
4. `POST $B/api/admin/optimize/apply` body:
   `{"strategy_id":$S,"code":<ops/tpl_next_s23.py 全文>,"baseline_hash":"2df25286…(同上)","description":"S23 逆bar入场影子观测(纯计数零交易路径)+同窗lev3→2回滚落地"}`
   - 失败 ⇒ PATCH `_exp`=<ops/exp_closure_only_fallback.json> → 跳6(仅#36落地,S23下窗再试)。
   - 成功 ⇒ 记 new_template_id。
5. `GET ctx` 复检: current_code 含 `S23`锚点≥6处 + 全部live锚点(S4/S8/S12/S13/S14/S15/S16/S18/S19/S20/S21/S22) + 静态6符号;
   config 全字段 diff vs `ops/expected_config_post_landing.json`(_exp/balance_usdt 除外) → 泄漏字段纠正PATCH(豁免限制)。
6. `POST $B/api/strategies/$S/start` → GET 验证 running(此刻=lev2段起点,记时间戳)。
7. 归档: ops/tpl_next_s23.py → `strategies/quicktrade-8eb182b6/tpl<新id>.py` @ claude/confident-fermi-wx3cei 推送
   (commit 注明 tpl编号+S23锚点+_exp.action)。
8. 台账: §5 #36 落地结案(维护注格式,含start时刻=lev2段起点) + §7 运行日志行 + push origin HEAD:quanty-ledger。
9. TG 报告(含🎓行) + 删除剩余 pill triggers(IDs 见本文件尾)。

## 中止规则
- 步2 stop 被拒 → 放弃本pill,等下一枚(不重试,不消耗stop限额)。
- 步4 apply 失败 → fallback _exp 后照常 start(**#36必须落地,S23可让路**)。
- 步6 start 失败 → 立即重试1次;再失败=TG🆘(策略停机>硬边界#5)。
- 任何步骤后发现持仓出现(开仓竞态) → 已 stop 的前提下平台不会开新仓,忽略幻影;
  若 stop 前竞态见步2。

## 裁决依据快照(2026-08-09 04:1x cron 轮)
- lev3段终(开仓≥08-08 16:39:53): n=25 净-1.84 均净-0.074 快SL 11/25=44.0%
  → 预注册回滚线 [段n≥20 且 (净<0 ∨ 均净<+0.15)] 触发,三读恶化一致(9/-8.47→15/-10.90→24/-1.98→25/-1.84)。
- S23证据: 48h首腿快SL L 20/-37.62 + S 14/-26.46;批内节流候选证伪(聚簇0.24=0.24,误杀45-60%);
  bar向闸不可退化审计(无1m bar史) → 影子计数先行(#30教训)。

## pill trigger IDs(创建后回填)
- trig_01GGwSYAfan2mYjqo774geUB @05:03Z
- trig_01BT9MjprTewZhgFM59zL2NP @05:23Z
- trig_011nDBhsPduw4Y7LQtdozzJn @05:43Z
- trig_015k4DMs1gJM2KK2CsrrNwnN @06:03Z
- trig_01RHKacFtMKK2vRzcQhR97YX @06:23Z
- trig_01TvgSSqLM7i1pSXeTQEEFrN @06:45Z
