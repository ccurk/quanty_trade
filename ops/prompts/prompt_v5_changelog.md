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

# prompt v5.2 变更清单（vs v5.1）@2026-09-18 06:2xZ — owner 直令"策略不对…不要不下单…要积极适应市场改变策略"＋"这个加到 prompt 中"
1. 核心使命新增【不下单不是解决方案】条：刹车动作只允许改变"怎么交易"（出场几何/杠杆档/入场规则/币池/regime 化方向偏置）；禁方向、缩 mcp、cd 拉满、抬 min_confidence 不是刹车杠杆；频率硬地板＝笔/日 ≥ 基线 50%（19/日）任何状态适用；亏损轮必须换一根打在病根上的杠杆并预注册；唯一缩表例外＝6h≥20% 或 avail<1U 紧急止血且同轮带策略改动、下轮复飞。教训原文：09-17 07:52→09-18 06:14 刹车链 lev10→2→禁多→mcp2，笔/日 256→112→26→20。
2. 协同协议 4 新增"进程有效值 ground truth＝START 行"（logs q=cooldown%3D）：config≠进程＝未生效，评判以进程为准，未生效的 Python 侧键须钉回进程值（09-18 教训：DeepSeek 写的 cd1800/min_conf0.45 从未生效却可被任何重启静默激活）；协议 5/7 把"禁方向/缩 mcp/压频率到地板以下"列为越界（当轮纠回）。
3. 频率阶梯：刹车态不再"阶梯暂停"，改为"先修质量"的杠杆清单（①出场 ATR 口径 ②S35 高分延伸 veto 候选 ③conf_sizing_max_mult 1.4→1.0 升档时启用 ④48h 规则隔离）；RECOVER 门只管杠杆升档，不再管 sides/mcp；劣化反应永远是"换杠杆"不是"缩表"。
4. 引擎语义速查重写出场层次：默认 ATR 口径（take_profit_pct=stop_loss_pct=0 → resolveTPSLFromROI 原样用信号 tp/sl＝2.5×ATR SL / 4.5×ATR TP），SL 价距随波动不随杠杆；ROI 口径 pct 写回 >0＝当轮回滚除非预注册；单笔风险公式改为 名义×atr_sl_mult×ATR%。
5. 硬边界 #2 加"allowed_sides 双向为默认、tp/sl pct 默认 0"；#5 加"严禁以不交易止血：笔/日地板 19"；#11 注明现货 canary 是唯一允许"停"的载具。
6. 每轮节奏 2 加读 START 行；4 改为"刹车＝换策略杠杆"；5 加置信度桶 join；7 优先级加"频率地板"。
7. 能力清单 A 加置信度×逐笔 join 标准工具；E 加 q=cooldown%3D；H 加 resolveTPSLFromROI 语义；J 出场能力改默认 ATR 口径；O 注明持仓中币 remove 被拒。
8. 用户常备直令快照加 09-18 条；环境节凭证占位符（脱敏）。
同轮已落地（Go 热 PATCH v34/v35，均复读✓）：allowed_sides [sell]→[buy,sell]；mcp 2→10；take_profit_pct 0.25→0；stop_loss_pct 0.12→0；预防性钉回 min_confidence 0.35 / cooldown_sec 180（=进程现值）。预注册 _exp_cc OWNER-DIRECTIVE P1/P2，eval 09-18 12:00Z 或段 n≥30。
9.（同日 08:2x 增补）owner 直令"下单数量和杠杆太谨慎了。增大 10x"：核心使命加"杠杆"条（lev10 常态档；ROI 口径 hunger_* 键随杠杆换算；conf_sizing_max_mult 1.0；pct 目标 0.075；防御线 24h≥20%→lev2；物理护栏 max_atr_pct≤2.67 重启窗落）；快照加 09-18 08:0x 条。同轮落地 PATCH v37：leverage 2→10 / mcp 4→10（纠回 admin 06:30 裸改）/ hunger_stop_loss_pct 0.125 / hunger_take_profit_pct 0.40 / conf_sizing_max_mult 1.0。
10.（同日 13:2x 增补）owner UI 自改 pct 0.25（滑杆 25% 语义）/ mcp 20 / max_consecutive_entries_per_symbol 100 / max_trades_per_day 5000 / warmup_bars 50 并重启：硬边界 #2 的 mcp=10 与 #8 的 pct×mcp≤0.75 改为"owner 直令现值，CC 不改，DS 改＝越界纠回"；总占用由引擎递减机制兜底；刹车半步 pct→0.15 为唯一例外。风险陈述与 DS breaker 阈值冲突见台账 §1 13:0x 行。
11.（同日 13:4x 增补）owner "硬边界#8 是多余的"+"去掉了"：删除全部自动缩表条款——核心使命④改为"不自动缩表"；协同协议 5 改为只报警＋DS 砍值即恢复；每轮节奏 4 改为报警＋换策略杠杆；硬边界 #2 去掉半步例外、#8 改为无 CC 侧上限；TG 报告刹车行改报警行。保留：6h≥8%/24h≥20% TG 报警、24h≥30% TG🆘、avail<1U cancel-orders。

# prompt v5.3 变更清单（vs v5.2 848966c）@2026-09-18 13:4xZ — owner"止损止盈收紧…高频开仓只抓能看到的收益"＋"给我一个最新的 prompt 我直接粘贴"
1. 核心使命重排为 09-18 直令集：owner 值优先条（pct/mcp/lev/sides/select_limit/max_price 由 owner 界面设定，CC 不改，DS 改＝纠回）；不下单不是解决方案；不自动缩表；新增【出场哲学】条（紧出场高周转包为默认：信号 TP 2.0×ATR/SL 1.5×ATR、trailing 0.5×ATR/0.6%、保本 0.5×ATR、饥饿 20m 10%/5% ROI、max_hold 60）。删除"防御线/双转正/pct 目标 0.075/物理护栏 max_atr_pct≤2.67"（review A1、C10）。
2. 授权声明：apply 内部 force stop（源码 optimize_handlers.go:843-847/strategy_lifecycle.go:199），代码与 Python 侧键改动不再等空仓窗（review C8 按推荐落定"准"）。
3. 协同协议 7 越界清单：删"pct×mcp>0.75"，改为"pct/mcp/lev/sides/select_limit/max_price ≠ owner 现值"＋"tp/sl pct 写回>0"＋"出场包 9 键偏离且无预注册"（review A2）。协议 4 增"进程有效值推断法＝最近 Symbol select start 时间＋当时 config"。
4. 【频率阶梯】节改为【质量与频率杠杆】：删 RECOVER 杠杆阶梯与 v4 常态阶梯的过时数字（review A3/B6），保留一轮 ≤2 原子包（review C9 按推荐保留）、地板 19。
5. 引擎语义速查：出场层次与单笔风险公式按紧出场包更新（review B5）；下单公式补 max_initial_margin_usdt=500 与递减机制。
6. 每轮节奏：2 加 owner 界面漂移巡检（无 audit 变化＝owner 值，登记不纠回）；3 改为不等空仓窗＋10 分钟自查点；4 改名"报警"。
7. 硬边界：#2 改 owner 值条；#4 改"严禁手动平掉策略持仓"；#5 改"重启只为 Python 侧键/代码 apply"；#7 删"改 mcp"；#8 无 CC 侧上限；#11 canary 劣化到线＝TG 报 owner 拍板（不自动停）。
8. 现货节：canary 起跑门去掉"双转正"，改 owner 明示；atr 倍数对齐 2.0/1.5；标注参数为 09-17 稿待部署时复核（review D11）。
9. 能力清单 E 加 q=Symbol%20select%20start / q=EXIT_AUDIT；B 加 429 瞬时；H 加 resolveUSDMOrderAmount/lifecycle force；J 改重启路径；O 加重启重播种复核。
10. 快照加 09-18 六条直令；环境节凭证占位符（脱敏）；可粘贴版经会话文件交付 owner。
同轮已落地（PATCH v49 复读✓）：atr_tp_mult 4.5→2.0 / atr_sl_mult 2.5→1.5（Python 侧，owner 重启生效）；trailing_activation_atr 1→0.5 / trailing_callback_pct 1.2→0.6 / breakeven_trigger_atr 1→0.5 / hunger_after_minutes 45→20 / hunger_take_profit_pct 0.40→0.10 / hunger_stop_loss_pct 0.125→0.05 / max_hold_minutes 240→60（Go 热）。预注册 _exp_cc.p6_exit_tight，eval 09-19 00:00Z 或段 n≥40。
11.（同日 13:5x 增补）owner "60 变成 45分钟"＋键表（"我这边分类器拦死，只能你落"）：max_hold 45；hunger_after 表写 0→引擎 ≤0 回退 30，按意图落 1（自首分钟起用饥饿区间）；hunger_take_profit_pct 0.30（3% 价@10x）、hunger_stop_loss_pct 0.075（0.75% 价；owner："0.5% 太噪声"）、trailing_callback_pct 0.5、max_concurrent_positions 20→10（owner 表）。PATCH v50/v51 复读✓。核心使命出场哲学条、引擎语义速查③、质量杠杆①、快照同步更新。风险注记：0.75% 固定止损自第 1 分钟起优先于 ATR SL，评判以穿刺率为主。
