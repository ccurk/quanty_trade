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

# prompt v5.1 增量（vs v5.0）@2026-09-17 — owner 直令"增加现货策略。下单，写到 prompt 中"

取证基础（当轮源码，行号可自查）：
- market 是进程级：exchange/binance.go:129-141 从 conf/env BINANCE_MARKET 读一次（默认 spot，生产 usdm）；Manager.GetExchange() 单例（manager.go:1867），ctx/positions/dashboard 全走它 → 现货只能是第二个后端进程；同库会混账（positions/orders 无 market 列）。
- 现货可用：市价买入 /api/v3/order（binance.go:1339-1425）；市价卖出平仓 closeSpotPosition（strategy_execution.go:478-540）；本地逐仓 TP/SL 监控（strategy_position.go:859-973，2s 轮询 1m 收盘价）。
- 现货不可用：饥饿/max_hold（quick_trade_monitor.go:41）、ROI 监控（strategy_roi_monitor.go:72）、交易所侧 TP/SL·追踪·保本（strategy_tpsl_monitor.go:105）、WS 守卫（strategy_ws_guard.go:42）、收养（strategy_lifecycle.go:251）、余额比例/置信度 sizing（strategy_position.go:107-112）；非 usdm "数量<10 跳过"地板（:118-124）；信号路径无反转平仓且 allowed_sides 门在前（strategy_signal.go:821）→ 出场只有 TP/SL。
- 下单量：python _emit_signal 发 amount=cfg.trade_amount（静态 300）→ 现货会被当基础币数量下单 → 需 S34 改为名义/现价。
- 部署 env 映射：conf.go:522-530 支持 BINANCE_MARKET / BINANCE_BASE_URL / BINANCE_WS_BASE_URL 覆盖 conf_pro.yaml 的 fapi 硬编码；server_deploy_docker.sh 支持 BACKEND_PORT/DB_NAME/REDIS_DB/REDIS_PREFIX 覆盖。

变更：
1. 标题 v5.1；核心使命第 1 条改为"主载具 main（USDM）＋现货载具 qt-spot-long（候部署→canary；DeepSeek 未接入，config 由 Claude 独管）"。
2. 新增【现货载具】节：引擎事实（可用/不可用清单带行号）/部署配方（第二容器 env 覆盖、独立 DB/Redis、现货权限、划转、自检）/载具规格（FLEET 预注册 config 模板：buy-only、spot_notional_usdt=12、mcp=3、cd300、min_conf 0.60、atr_tp 3.0/atr_sl 1.5、hunger off、use_exchange_tpsl off）/S34 规格（SPOT_MODE、只发多头、名义/现价换算、数量<10 抬名义、funding/ls 固定 0）/超时缺口两条处置（M 候选 dev-spot-maxhold；部署前 cron 巡检持仓龄≥180m）/起跑条件、现货费覆 0.2% 来回、劣化线（段净≤−3U 或 n≥10 且 wr<35% → stop）、现货刹车 6h≥5% → stop、现货硬边界。
3. 环境节新增 BACKEND_SPOT / SPOT_USER / SPOT_PASS / SPOT_ID（空＝候部署跳过）。
4. 每轮节奏新增 1b 现货管道、5b 现货逐笔（两本账分开）。
5. 硬边界新增 #11 现货载具专属（只买不卖空、名义≤现货钱包 25%/仓、mcp≤3、不卖非本载具持仓、划转/部署只属 owner、禁混算钱包）。
6. TG 报告新增 🪙现货行。
7. 改代码纪律：S34 预留给 spot fork，main 下一个新锚点 S35；spot fork 谱系＝main 全锚点+S34。
8. 直令快照加 09-17 现货直令。

未做/待 owner：部署 quanty-spot 第二进程＋现货钱包划转＋填 BACKEND_SPOT/SPOT_ID；决定 canary 起跑是否等 main 双转正；M 候选 dev-spot-maxhold 是否要（无它则现货持仓只靠 TP/SL 出场）。
