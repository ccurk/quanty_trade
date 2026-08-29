# S31 regime动态方向偏置 · v1 草案（预制@08-29 17:1x，未上线）

状态: DRAFT-NOT-APPLIED。执行窗=main(8eb182b6) 当前 _exp(多头闸+S30)评期到点接棒
（08-31 15Z 或多头n≥10 先到），规格权威=台账§4 S31 行。

## 应用方法（评期轮执行）
1. 现拉 main current_code 存文件（禁用本草案期的旧快照做基底）。
2. `python3 ops/drafts/s31_apply_patch.py <现拉code文件> <输出候选>` ——
   patcher 全部替换点带唯一性断言，基底漂移=断言抛错拒绝（此时人工重做补丁）。
3. 常规改码纪律照走: py_compile+6符号+锚点grep（S31 谱系并入 main=S4..S23+S30+S31）
   +先 PATCH _exp 再 apply+字节级复检+diff全量config+烟雾。
4. 锚点日期: patcher 写死 `@2026-08-31`，若实际 apply 日不同→sed 改为实际日期。

## 设计（阈值内联，改=ROUTE/EXP 预注册）
- 宽度=池内 EMA-up 占比（live 评估路径逐bar上报，新鲜窗30m，样本<8→NEUTRAL）。
- RISK_OFF(<0.35): lcp+0.10 ｜ NEUTRAL(0.35-0.60): 0 ｜ RISK_ON(>0.60): lcp−0.05, scp+0.05。
- 有效premium下限0（门永不低于base MIN_CONFIDENCE）；S31_ENABLED=False 整层退场。
- detail 新增观测五键 s31_regime/breadth/fresh_n/long_adj/short_adj；
  long/short_threshold 上报改为真实gate值（E2 观测偏差教训）。
- 历史重放/backtest 不喂宽度store（from_history 在评估前 return）；重启冷启动=NEUTRAL。

## 验证记录（08-29 17:1x）
- py_compile PASS；6静态符号在位；+80行。
- 单元冒烟8场景: 冷启动thin✓ n7 thin✓ n8全up→risk_on(−0.05/+0.05)✓ 0.40→neutral✓
  0.20→risk_off(+0.10)✓ 1.0→risk_on✓ 陈旧(>30m)剔除✓ 极旧(>60m)清扫✓ guard关→disabled✓。
- 劣化线（评期轮预注册草案，台账§4）: S31段多头净≤−3U 或 组wr−be劣化≥5pp@n≥20 → guard关。
