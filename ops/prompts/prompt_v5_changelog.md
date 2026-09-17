# prompt v5.0 变更清单（vs v4.0）@2026-09-17 — owner 直令"我增加了一个 deepseek 的定时模型优化，你们配合着来；/tmp/ai_task 交流，只保留最近几天"

取证基础（当轮实拉，全部可自查）：
- DeepSeek 调参器＝宿主机 cron 脚本 ops/deepseek_optimize.py（experiment_hold 冻结键）＋ ops/qt_breaker.py（熔断器），不在 git 仓库；以 admin#1 身份 PATCH config（audit 1812–1827，09-16 13:33 起）；只动 config 不动策略代码（current_code 100KB，S11..S33 锚点全在）。
- Go 侧 backend/internal/strategy/strategy_autotune.go 是另一套"LLM 重写整份策略代码＋重启"的定时器：auto_optimize_enabled=false 未跑，但 apply=true/dry_run=false 上膛。与 DeepSeek 脚本无关。
- 探针实证：running 态 PATCH 被部署后端接受（http 200，param_version v17→v18），仓库 manager.go:1737 的拒绝守卫线上不存在；但 strategy_start.go:306-317 显示 config 只在进程启动时注入 Python → Python 侧键热 PATCH 不生效。
- 09-16~17 DeepSeek 改动史：lev10（×2）/pct0.15（越硬边界#8 87 分钟后自纠 0.075）/sl0.12+tp0.25（owner 令）/cd 300→600→1200→1800/mcp10→6/min_conf0.45/pct0.05（熔断）。结果：24h 934 腿 wr43.9% 净≈−66.9U；6h −41.5U；日志见 conf 0.40/0.44 成交。

变更：
1. 核心使命：新增"双执行体共管"条（DeepSeek 提议并落参数，Claude 评判并守边界；降敞口不问、升敞口预注册）；进攻直令补北极星前提（单笔均净≥0；笔数高于基线但均净<0＝无效频率）；mcp 条改为"owner 令 10，现值 6 待裁，裁前两边不动"。
2. 授权声明：新增对 DeepSeek 改动的评判权/回滚权（越界纠回不需 owner 批）；明确不改其脚本（无 ssh）。
3. 新增【协同协议】9 条：身份识别（actor）/命名空间（_exp 归 DS 只读、_exp_cc 归 CC、留言板 _ai_task_cc↔_ai_task_ds↔宿主 /tmp/ai_task 3 天保留）/写冲突（重读→最小 diff→尊重 frozen_keys→复读）/生效语义（Go 热 vs Python 重启，键归属表首轮建立，有效门槛反推）/刹车优先级（两套取更严，单向）/评判权（裸改补 watch_ds）/越界纠回清单/Go 侧重写器保持关闭＋dry_run 建议/留言板每轮必写。
4. 频率阶梯：加"刹车态"段（防御档在位；Step0 质量门恢复候 owner；RECOVER 阶梯 lev 2→…→20 与 max_atr_pct 联动表；再 pct 0.05→0.075）；常态阶梯从 lcp 0.10 起（v4 一档已被清零覆盖）；频率仪表加北极星。
5. 能力清单：A/D/E 补返回结构与限额（logs>400 截断、audit 空响应重试）；B 补 income_totals/TRANSFER/current_code；H 加 strategy_tpsl_monitor.go/strategy_start.go/strategy_autotune.go 并注明部署版≠仓库版；I 补 baseline_race 语义；J 改为热/重启双语义＋探针；L 补 config 回滚口径；新增 R【DeepSeek 对账】、S【留言板写入】；M 增可选 ai-task 端点。
6. 引擎语义速查：出场第一层改为交易所侧 TP/SL 委托（ROI 口径，lev 影响价距）；新增"单笔风险恒等式"（SL 亏损＝余额×pct×sl_roi，杠杆约掉）；config 生效语义改写。
7. 每轮节奏：新增 2.5【DeepSeek 对账】；步骤 2 加读 _exp/_exp_cc/_ai_task_ds；步骤 4 恢复加"先 lev 后 pct"＋冻结键同步；步骤 5 加有效门槛反推；步骤 7 优先级插入"DS 越界纠回"；步骤 9 加留言板。
8. 预注册：写入位置 _exp→_exp_cc；schema 加 watch_ds；前缀加 DS-ROLLBACK。
9. 改代码纪律：加第 4 条 baseline_race 处理＋apply 重启告知留言板。
10. 硬边界：#2 mcp 行加现值待裁；#6 明确 auto_optimize_enabled 指 Go 侧重写器；#7 加"改 DeepSeek 脚本行为"；新增 #10 命名空间与纠回权边界。
11. 台账：§1.5 改单行 main（退役壳已从平台删除）；§3 加"双执行体"小节；§7 每行含 DS 段。
12. TG 报告：新增 🤝 DeepSeek 对账行；📈 加入金另列；🔪 加有效门槛反推；⚡ 加北极星。
13. 环境节不变（归档版 5 个凭证为占位符；可粘贴版经会话交付 owner）。
14. 同轮执行（非 prompt 变更但入台账）：BRAKE leverage 10→2 @07:52Z（24h≥30% 防御档）；leverage append 进 _exp.frozen_keys；_exp_cc 预注册 eval 14:00Z；TG🆘 #9632。

未做/待 owner：mcp 6 vs 10 裁决；Step0 质量门恢复（需重启）；ops/ai_task_bridge.py 安装到 DeepSeek 侧；auto_optimize_dry_run=true 保险；可选 /api/admin/ai-task 端点（需 docker 卷挂载宿主 /tmp/ai_task）。
