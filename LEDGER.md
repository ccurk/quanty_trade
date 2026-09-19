# QuantyTrade 改动台账（LEDGER）

> 权威分支：`quanty-ledger`（2026-07-22 12:19Z 由种子 `claude/jolly-bardeen-sz6d04` 引导上线）。
> 由 cron 运行维护，每轮必写。**瘦身协议 @08-01 17:35Z（用户直令：省 token）**：本文件保持精简工作集，全量历史永在 git（压缩前快照=29eb4c5）。纪律：§7 只保最近 10 行（新行≤800字符）；§6 每计数项只保最新读数；§5 只保 open 项+最近 2 条维护注；关闭候选/已落待落项直接删行；超长叙述以〔压缩〕标记截断。人工编辑请只增不删原则对 cron 瘦身豁免。
> 由 cron 运行维护，每轮必写；人工编辑请只增不删。协议：v12 七节制（2026-07-22 16:16Z 起升级；此前见 `docs/cron_prompt_v11_addendum.md` Step 7）。
> 策略：8eb182b6-ee74-4125-a602-f0a91f376432（tpl447 @2026-07-22）

## 1. 用户常备直令（权威登记处；prompt 内快照与此冲突时以本节为准）

- **prompt v3.1 写入@08-15**: 11处手术对账;脱敏档ops/prompts/prompt_v3.1_20260815_redacted.txt;已被v3.2/v4/v5.1取代;全文git 7ee5ca0版§1。

- **多仓位=足额单仓直令(2026-08-15 15:5x)**: 多仓位不是小仓, 每仓按需足额, 余额不足依次递减; **禁以缩pct换仓位数**; 机制=conf_sizing×percent_balance×min_notional 21 已在位; 全文=git 7ee5ca0 版§1。

1. **单笔名义≥20U(07-22)**: 已被08-03费覆红线废除,后费覆降级为评判指标(08-29);全文git 7ee5ca0版§1。
2. **全天开仓**（2026-07-19）：`entry_time_windows` 保持 `""`；恢复窗口属 🔴 提案须用户批准。
3. **mcp基线2(07-19)**: 已被08-05 mcp10/09-14 mcp10直令取代。
4. **引擎下单语义含杠杆**（2026-07-20）：notional = avail×pct×lev×mult，与币安滑杆一致；20U 度量口径不含杠杆，两口径并存。
5. **按置信度动态下单量**（2026-07-20）：引擎 conf_sizing 已实现（mult∈[0.6,1.4]、交易所名义地板 `conf_sizing_min_notional_usdt=21`）——策略代码勿双重实现（07-20 HANA 53.12U、07-22 AKE/MIRA mult≈1.4 实证）。
6. **充值信号常备令**（2026-07-20，长期有效）：触发=修复见效+12h/24h net双正+12h wr−be≥6pp → TG建议充至钱包250-300U。入金检测用钱包总值口径(balance_usdt+Σ持仓名义/lev),avail跳升≠入金;用户提前充值→当轮按新余额重算pct不视违规。两次到账已闭环(08-02 +148.14/08-08 +74.4→钱包295.7=区间顶),20U复算已随08-03直令退役;常备逻辑继续有效。全史(07-22校准/08-05首发3/3)git 766c7d9

- **08-02 17:0xZ(交互)恢复阶梯cd优先**: ①cd ②sides ③仓位次序,6h/24h双转正门;后续cd180地板(08-06)/sides双向(08-05)已落地;全文git 7ee5ca0版§1。

- **08-03(交互)教练模式常备直令**: 用户明示"不想被慢慢替换"→所有输出加教学层: ①TG 报告尾固定加一行 🎓(本轮一个概念或一个开放判断题,≤200字,用当轮真实数据讲) ②交互回答先给"怎么想"(判断框架/我用什么证据/你可以自查的命令或指标)再给结论 ③非紧急决策改为"呈现取舍+我的推荐+留一晚给用户拍板"(紧急刹车与硬边界内动作照旧先斩后奏) ④用户提出的质疑必须正面回应证据而非以权威压过。此直令与瘦身纪律并行,🎓行不计入800字符限制。

- **08-03(交互)并发阶梯直令**: 硬边界mcp≤10语义存续;pacing条款已废;全文git 766c7d9。

- **08-03(交互)核心使命+20U废除直令**: 使命=稳定×高频×快速周转;20U底线废除→费覆红线(后08-29降级为评判/回滚指标);并发资金门作废;全文git 7ee5ca0版§1。

- **08-03 16:5xZ(交互)mcp=3直令+平台事实**: mcp3 owner UI改禁回滚(已被08-05 mcp10/09-14 mcp10取代);**平台UI改config不写audit**(实锤,存续);全文git 7ee5ca0版§1。

- **08-03 17:0xZ(交互)杠杆上限直令**: 硬边界 leverage [2,5]→**[2,20]**;阶梯 2→3→5→8→12→20 逐档,每档门=≥30笔正净额+费覆+预注册;物理护栏已内置(§3 SL 钳 0.3/lev);全文git 91e4749。

- **08-05 17:1xZ(交互)币池直令**: select_limit 50→**100** owner UI改,禁回滚;select_limit仅启动时读(strategy_start.go:230);#22系已结案;全文git 766c7d9。

- **08-05 17:4xZ(交互)三解锁直令**: sides双向+cd1800→900+mcp3→10当轮落地(S17 gate移除tpl534);取代08-03并发pacing;风险呈报存档;全文git 7ee5ca0版§1。

- **08-05 17:4xZ(交互)代码入库+模板保留3版直令**: 策略代码不再只存DB——每次apply后上线代码推git仓库(confident-fermi分支 strategies/ 快照目录,git=全史档案);DB StrategyTemplate 只保留最新3版(=当前+2步rollback深度;retention patch M通道候部署)。owner索要此流程prompt块已交付(见08-05交互记录)。

- **08-06 02:4xZ(交互)冷却处置直令**: cd→180预授权(owner"按照你说的来";0越界不采,硬边界#2下限);已执行cd180全组现值;全文git 766c7d9。

- **08-06 09:2xZ(交互)二次解锁直令**: owner UI mcp200+re3禁回滚(已被09-14 mcp10取代);全文git 7ee5ca0版§1。


- **08-09 策略组+WS实时管理直令(交互)**: 已被09-14单策略直令取代(5载具/S24-S26/route_pools对账器/金字塔只赢家/owner拍板fade启·lowvol删);全文git 7ee5ca0版§1。

- **08-11停机/08-12复飞三直令**: 全组停机(后端522窗)→单载具canary→全组复飞;全部被09-14单策略直令取代;全文git 766c7d9/cd4b082。



- **08-22 04:1x(交互)低波解锁优化直令**: owner"现在开始优化"→双载具三值包(trend/v2低波解锁);后续=trend包#69回滚@08-25、v2退役@08-28,低波解锁线关闭;全文git 766c7d9+a2e845f。

- **08-26 17:07Z(交互)提频直令**: "增加频率"→main scp0.05→0.049收0.600档+ROUTE#52;后被08-28终裁rollback;全文git 7ee5ca0版§1。

| 2026-08-29 | 全文=git 848966c版 | | |
| 2026-09-03 | 全文=git 848966c版 | | |
| 2026-09-14 | 全文=git 848966c版 | | |
| 2026-09-17 | **DeepSeek 双执行体协同直令(live,07:3x-08:0x)**: owner "我增加了一个 deepseek 的定时模型优化 你们配合着来"+"/tmp/ai_task 只保留最近几天"; DS=宿主 cron ops/deepseek_optimize.py+qt_breaker.py(admin#1 只动 config); _exp 归 DS; 交付 prompt v5.0(协同协议 9 条)+ops/ai_task_bridge.py; 全文=git 7f578a7 版§1 |
| 2026-09-17 | **现货载具直令(live会话,08:2x)**: owner "增加现货策略。下单，写到 prompt 中"; 规格=prompt v5.1【现货载具】节/git b341694 版§1; 状态=候部署(DS-6) |

| 2026-09-18 | **09-18 直令归并指针**: lev10(08:0x)/单笔上限(10:2x)/币池300+不限价+保证金口径(12:5x)/仓位 owner UI 自改 pct0.25(13:0x)/出场收紧(13:2x)/max_hold45+键表(13:5x); 要点=lev10; mcp/pct=owner 值取代硬边界#2/#8; max_initial_margin 500; hunger_after1/tp0.30; 全文=git 70822bb 版§1 |
| 2026-09-18 | **不下单不是解决方案直令(live 06:1x)**: owner "不要不下单。要积极适应市场改变策略"。**常备规则: 刹车＝换策略杠杆(出场几何→杠杆档→入场规则→币池)不＝停交易; 频率地板=基线50%(19/日); 禁方向/缩mcp/cd拉满/抬min_conf不是刹车杠杆**(缩表例外已被 13:4x 直令删除); prompt v5.2; 全文=git d19a237版§1 |





| 2026-09-18 | **去掉自动缩表刹车直令(live 13:4x)**: owner "硬边界第 8 条…傻逼"→"去掉了"。**自此 CC 任何轮次不得自动降 pct/lev/mcp/sides**; 保留=TG 报警(6h≥8%/24h≥20%)+24h≥30% TG🆘+avail<1U cancel-orders; DS breaker 砍 pct/lev→按 owner 值恢复并 TG; 唯一硬刹车=交易所强平; 全文=git d19a237版§1 |


| 2026-09-18 | **止损 ROI 4% 直令(live 17:5x-18:0x)**: owner 三改口→**hunger_stop_loss_pct=0.04(ROI 口径=UI 显示值, 10x 下 0.4% 价; owner 17:59:41Z 自落 v59)**; CC 误读价格距离已更正; 三条路 ①ROI 4% ②pct 减半 ③看 30 笔; 全文=git f48d043 版§1 |

| 2026-09-19 | **金额止损直令(live 03:3x)**: owner 原话"止损设置为亏损金额不能超过 3-5u, 止盈还是百分比 15～20, 最大持仓时间不变"→引擎无金额止损键, 03:34 临时 tp0.15/sl0.30 随即被第一波直令取代; 金额帽转仓位规则 名义≤4U/(2×ATR)(CC-22 Go 风险定量 sizing 候); 全文=git f48d043版§1 |
| 2026-09-19 | **第一波直令+优化知识登记(live 03:3x)**: owner "止盈止损经不起第一波波动…找到一个合理的数字, 记在优化知识里"→206 笔路径反事实定数=ATR 缩放(atr_sl 2.0×/atr_tp 2.5×/45m, 交易所 pct 归 0, hunger 0.30/0.30 兜底, trailing/BE 关, max_atr_pct 6; 03:41 force 重启). **常备知识: 止损距离以 ATR 为单位设计; 固定%/固定金额刀在多币池上必然被第一波打掉**; owner 的 15~20% 止盈/3-5U 金额帽同批数据为负已呈报; 全文=git 6b44db7 版§1 |
| 2026-09-19 | **/tmp/ai_task 双向论证直令(live 05:3x)**: owner "优化前看 /tmp/ai_task, 优化后同步, 不要把他的结论当作对的, 要论证"+"你的意见也写进去让他读取". **常备规则: 每轮优化前读 _ai_task_ds, 优化后写 _ai_task_cc(状态条+意见条), 对 DS 结论逐条论证不照单**; 容器无宿主 /tmp, 桥接 ops/ai_task_bridge.py 未在宿主运行(_ai_task_ds 始终空, DS-3); 全文=git 6b44db7 版§1 |

| 2026-09-19 | **多策略化直令(live 06:5x)**: owner"不能用一套策略盯所有 meme…变成多个策略: 一个池子专门做 btc eth bnb sol, 一个策略专门做 meme 固定的几个币"+备用池(不交易, 模拟下单, 好则加入)。执行: main→7 币 manual 池(G/BULLA/MAGMA/UAI/HEI/DRIFT/ARB; 72h 费后/笔≥−0.07∧n≥7) 07:13/07:21 重启(max_price 0 陷阱→1e12); 新建 Majors e725e31a(stopped; 1m 结构性负)+Sandbox ce84012b(备用池模拟, 永不 start, S34 tpl1055); _standby_cc 8 币。**08:01Z Majors 被 UI 启动(5 币含 XRP, mcp5)→首 3 单止损距 0.10~0.19%=费, CC-25 watch。** owner 07:2x"我让 deepseek 在做, 一会你检查一下"→08:2x 检查(§7)。 |
## 1.5 策略组注册表（08-09 起;池归属与载具状态权威节;roster 有变当轮必更）

| 载具 | id | 原型 | 池 | 状态 | 门/备注 |
|---|---|---|---|---|---|

> 维护注 08-29 17:4xZ：#76 **落地结案删行**——17:36自查点retry#2逢全账户空仓窗:brk stop[poll1即stopped无幻影卡]-PATCH六键+_exp尾注FIX伴随-start✓running✓复读mode=percent_balance/cs_en=true/floor21/0.6-1.4-0.55全落库;→rotate add BAS,NIL(斜杠)brk feed4→6✓;main remove BTR残留✓专家残留0互斥恢复;分界前brk段n8/−0.56全5U。
| main 通才(平台名 Meme_合约信号计算引擎_1) | 8eb182b6 | 通才(S4..S23+S30+S31+S33;S32关guard;tpl998 hash 3e311e8b) | **owner 11:06Z 重置: symbols ""+max_price 1e12 ⇒ filter 路径 300 池(select_limit 300)**; feed 265(CC 11:4x rotate remove 35: bl∩feed=∅, ∩majors=∅) | running(pid 58521 START 11:08:43Z) | **09-19 11:4xZ 现值: 注入 min_conf0.35/cd180/atr 2.0/2.5/warmup50; pct0.125(DS 07:08, owner 值 0.25, lock-1789810634 至 09-20 09:37Z) lev5(DS 10:09 裸改, owner 值 10, watch_ds) mcp3(DS 09:37 未声明, owner 令 10; 11:16 起绑定) sides 双向 hunger OFF(owner 11:06) tp,sl pct0(交易所腿=信号 ATR 括号, 钳 0.3/lev) trailing/BE 关 max_hold45 sl_ratio0.03/tp_ratio0.06 bl45 conf_sizing 0.6-1.0 auto_optimize enabled=false(apply=true/dry_run=false 上膛)**; 7 币段(07:21→11:08) 终读 n12 +17.34 wr50% |
| majors(平台名 Majors_BTC_ETH_BNB_SOL) | e725e31a | main fork → **tpl1056 = tpl998 + S35 bar聚合 15m**(CC apply 09-19 08:39:38Z, hash 766b576a; 11:08 owner 重启后模板保持) | manual BTC/ETH/BNB/SOL/XRP(bl0; main 侧已 bl+rotate remove SOL/XRP 防撞) | **running@08:01:47Z(owner UI start); 11:08:41Z owner 重启(mcp 5→2 admin 11:06)** | 11:4x 现值: lev10 pct0.125 mcp2 cd900 max_hold720 hunger off atr 2.0/2.5 min_atr 0.03 reentry 15m bar_agg 15 sl_ratio0.03 auto_optimize dry_run=true; 1m 段(08:03-08:20) 3 空 SL −0.78; **S35 段: SOL/BNB/BTC 多 08:46/09:01/10:16 开, 11:08 重启时 'manual' 路径平仓 −0.63/−0.15/+0.03(段 n3 −0.75, 未经 TP/SL 检验); XRP 多 11:16 在飞(腿✓ SL 距 0.84%, 浮盈 +0.25)**; CC-28 eval 09-20 08:40Z/n≥15 |
| sandbox(平台名 Sandbox_备用池模拟_勿启动) | ce84012b | main fork tpl1055 = tpl998 + S34 sim-clock(live 会话 09-19 07:19 建; 永不 start) | 备用池 8 币 IOST/PONS/SAGA/SOLV/ZEREBRO/VTHO/XMR/ZEN(_standby_cc 于 main config) | stopped(只跑 POST /backtest) | 用途=owner 直令"备用池用策略模拟下单, 好则加入"; 门=连续 2 窗 1m 18h 回测 费后>0∧n≥5→晋升 core(CC-26); **回测引擎同窗结果不一致(§3 08:4x 条)→晋升前须 sim-clock+同窗复现一致**; 归档 confident-fermi df31d32 strategies/sandbox-ce84012b/ |
| qt-spot-long(现货) | 未建 | main fork+S34(spot) | auto select_limit100 现货USDT对;bl=隔离区 | **候部署@09-17(owner:第二进程 quanty-spot+划转;cron:建壳→S34→烟雾→canary)** | 规格=prompt v5.1【现货载具】节/git c06ab72版§1.5(buy-only/mcp3/cd300/mc0.60/atr_tp3.0/atr_sl1.5; 劣化线段净≤−3U∨n≥10∧wr<35%→stop; 现货刹车6h≥5%; 超时缺口=DS-7) |
| ~~退役壳×4~~ trend 827ffe8c/breakout-v2 3b646bf4/fade-v2 7583727a/fade 21519f1b | — | — | — | **已从平台删除**(09-15~16 owner/迁移;/api/strategies仅main 1行@09-17 07:3x实证) | 谱系tpl/verdict/遗仓收养全史=git 7ee5ca0版§1.5;复活=新壳FLEET预注册+owner令;qt-breakout-follow 2111f5f9 owner删@08-15同上 |

- 隔离区【bl42@09-19 07:2x】=规则类(≤−4U∧n≥4): 4/CYS/TST/龙虾/BMT/BTW/H/APR/AIO/BICO/BEAT/XNY/ACE(血统git bb3b883/8a797ec/bb2e系)＋BR/AVA/ONE@09-18 05:19Z(48h BR n14/−14.89·AVA n6/−9.54·ONE n5/−4.49;全文git 5d2c77a)＋**哈基米@09-18 10:1xZ(48h n8/−4.01;附证 09:18 -4028 "Leverage 10 is not valid"→引擎按现有杠杆继续下单=该币杠杆不可控;bl+rotate remove同轮复读✓)**＋**XTZ/AR/CROSS@09-19 07:2xZ(48h XTZ n4/−7.44·AR n9/−7.12·CROSS n14/−7.06, 亏损集中在 04:53 后 0.4% 刀段; bl39→42; manual 7 币 feed ∩=∅)**＋TradFi类【7】@09-10/09-17: GPRO/KODEX200/SOXS/CSOPSAMSUNG2L/CSOPSKHYNIX2L/HK0992/FLNC(-4411直证;签约闸+crypto模板未验证资产类;解禁须owner签约∧专项原型预注册;全文git 5d2c77a)。**池列值=快照,权威=每轮对账器输出**;对账器v1.6=quanty-ledger分支ops/route_pools.py(谱系与污染事故见§3);**arg4隔离表必须X/USDT形态**;出池只认头注规则或隔离;币级watch集/对账史/#51-#58史=git 5d2c77a版行。
- 保证金: owner 13:0x 起 pct0.25×mcp10 递减序列(引擎按剩余 avail 逐仓扣减)理论峰≈94% 余额;实测并发峰 5仓/1147U名义/保证金≈115U(56% 钱包)@09-18 13:26;硬边界#8 已被 owner 值取代(§1 13:0x/13:4x 行)
- 互斥不变式: 单载具后退化为 隔离∩feed=∅ + bl=隔离区;多载具时代不变式与违例史git 7ee5ca0版§1.5。
- lowvol(ad37d337)已删@08-09 17:0x owner拍板(无交易史,原型设计存git f970704 create_low.json可重建)
- 载具归档目录: strategies/quicktrade-<id前8位>/ 一载具一目录(协议不变)

## 2. 锚点登记（代码活性改动；apply 前必须逐一 grep 到）

| 锚点 | 日期 | 状态 | 一句话 | grep 签名 |
|---|---|---|---|---|
| S4 | 2026-06-13 | live | 反过度延伸地板：RSI 极端区拒逆冲入场（HARD_VOLUME_FLOOR 1.0 演化基线；故意不接 config 防回退覆盖） | `@2026-06-13 S4` |
| S8 | 2026-07-02 | live | MAX_ENTRY_EXT_ATR 1.2→2.0，恢复"入场偏离<SL"不变式，重开顺势漏斗 | `S8` |
| S12 | 2026-07-16 | live | LONG_CONF_PREMIUM 0.06→0.14，砍裸熊市反弹多单 | `S12` |
| S13 | 2026-07-17 | live | 市场宽度 regime 门（大盘 beta 过滤） | `S13` |
| S14 | 2026-07-18 | live | per-symbol 亏损熔断器（影子跟单，3 连败→240min 隔离；config cb_*=null 走代码默认） | `S14` |
| S15 | 2026-07-18 | live | 观测计数器统计行（影子多空/熔断/择优三计数） | `S15` |
| S16 | 2026-07-18 | live | 跨币择优开仓（5s 攒批取最高置信度，落选不烧冷却） | `S16` |
| E2fix | 2026-07-21 | live | short_thr 恢复吃 SHORT_CONF_PREMIUM（生效空头门槛 0.55→0.60，与多头对齐） | `short_thr = Config.MIN_CONFIDENCE + Config.SHORT_CONF_PREMIUM` |
| S17 | 2026-08-01 | **removed @08-05 17:4xZ tpl534** | BRAKE禁short gate 已按设计经 apply 移除（owner方向解锁直令;tombstone注释在码;short单闸回归 config allowed_sides） | `S17 removed` |
| S18 | 2026-08-03 | live | **闪跌穿刺入场veto**（硬过滤 gate7：long 且[上一完成bar跌>0.6×ATR% 或 3bar累跌>1.0×ATR%]即拒；阈值内联不接 config；冲顶亚型(c)首发不含防误杀；**评判keep@08-05 16:19Z** 穿刺2/−4.95 vs 基线8/−18.79，详§3） | `S18` |
| S19 | 2026-08-06 | live | **短侧顺涨开空veto（S18镜像 gate7b）**：short 且[上一完成bar涨>0.6×ATR% 或 3bar累涨>1.0×ATR%]即拒；阈值内联不接config；tpl563；**裁决KEEP@08-06 16:4xZ**(应拦型0/15,快SL空收窄53.5%≥50%过;残余拉升语境型由S20围栏;依据与全链git)；永久锚点 | `S19` |
| S20 | 2026-08-06 | live | **拉升语境拒空(gate7c,S19盲区补层)**：short且[300m累涨≥12% 或 180m累涨≥9%]即拒(绝对%阈值不×ATR)；机制=7b只读closes[-1..-4]对3-5h拉升物理盲区；tpl564；**裁决KEEP+候审清零@08-07**(应拦型0;误杀审计判据=被拒币2h实跌>2×ATR%≥3例→回滚tpl563;依据与全链git 541a2bc) | `S20` |
| S21 | 2026-08-07 | live | **同向急再入veto(gate7d)**：影子平仓后<45m同向再入拒(反向反手保留)；依据=#29 postS20 20例/−20.34 wr20%双负腿全veto；tpl565；已知噪声=幻影影子假veto窗+restart清last_shadow_close；**裁决KEEP@08-08 14:1x段n=37五维过**(全文_exp.s21_verdict+git)；永久锚点 | `S21` |
| S22 | 2026-08-07 | live | **gate7d断供免疫补强(emit口径副闸+仪表修复)**: 同币同向距上次实际emit<45m即拒(反向不限;择优落选不盖戳)+统计行fresh_reentry_block(影子)/_emit双计数;依据AIOT 21:49空SL→21:53同向再开漏拦实锤;tpl566;随S21裁决KEEP@08-08(postS22段31笔+9.25);全文git 7f578a7版§2 | `S22` |
| S24 | 2026-08-09 | live@trend fork(壳已删) | trend定制(tpl575): 空头出口封闭+续势加分; 全文=git c06ab72版§2 | `S24 ` |
| S25 | 2026-08-09 | live@fade fork(壳已删) | fade定制(tpl576): 多头出口封闭+RSI>75衰竭加分; 全文=git c06ab72版§2 | `S25 ` |
| S26 | 2026-08-09 | live@breakout fork(壳已删) | breakout新内核(tpl577): 30bar破位+量能+动量+EMA对齐; 全文=git c06ab72版§2 | `S26` |
| S28 | 2026-08-23 | live@fade-v2 fork(壳已删) | 衰竭签名直入(gate2旁路; tpl823); 全文=git c06ab72版§2 | `S28 ` |
| S29 | 2026-08-24 | live@fade-v2 fork(壳已删) | S28签名域ATR地板 0.5→0.30(gate1; tpl887); 全文=git c06ab72版§2 | `S29 ` |

| S27 | 2026-08-29 | live@breakout-v2 fork(壳已删) | #47复活标记锚(tpl973); 全文=git c06ab72版§2 | `S27 ` |
| S30 | 2026-08-29 | live@main(tpl972) | **资费磁铁疫苗**:多头funding bonus分段(温和(−0.003,−0.0003)+0.20/深(−0.008,−0.003]+0.10/极深≤−0.008→−0.10),score_confidence+detail两位点同步;空头侧不动;内联不接config;伴随lcp0.35→0.15开多头闸(EXP评判);校准集=BICO8+ACE4+ONG3≈15笔−11U | `S30 ` |

| S31 | 2026-09-01 | live@main(tpl988) | **regime动态方向偏置**:池内EMA-up宽度三档(<0.35 OFF:lcp+0.10/0.35-0.60中性/>0.60 ON:lcp−0.05,scp+0.05),有效premium下限0,阈值内联,S31_ENABLED False-guard;live路径逐bar上报宽度store(新鲜窗30m,样本<8→NEUTRAL,历史重放/backtest不喂);detail新增s31五键+threshold上报改真实gate | `S31 ` |
| S32 | 2026-09-03 | **rolled-back@09-03 18:2x(tpl990 guard=False在场,复活须新预注册)** | **接刀冷却(不稳定窗veto记忆)**: 近10m内 S18 veto 或 RSI>70 ∧ A-bar 仍跌→拒多, W-bar 放行; 阈值内联(600s/RSI70) S32_ENABLED False-guard; 域/反证全文=git d19a237版§2 | `S32 ` |
| S33 | 2026-09-08 | live@main(tpl998) | **资费疫苗下修(S30参数段下修)**:温和+0.20→+0.10,深+0.10→+0.05,极深−0.10不动;score+detail两位点同步,detail标签换S33前缀(决胜扫描口径随更);触发=§6预注册门(多头决胜cohort n10/净−5.82,owner无否决);阈值内联;回滚=一步rollback回tpl990 | `S33 ` |

| S34 | 2026-09-19 | live@sandbox tpl1055(main 未装) | **sim-clock**: sim_clock=true 时 cooldown/急再入veto/影子/CB/S31/S32/择优窗改用 K 线时间(_ct/_cm), live 零差异; 依据=回测把小时压成秒→每币仅 1 笔; 验证 #56 MAGMA 10 笔 | `S34 ` |
| S35 | 2026-09-19 | live@majors e725e31a(tpl1056) | **bar聚合**: config bar_agg_minutes(默认 1=关; majors 15)→1m K线合成 N 分钟 bar 后再算指标/信号(桶=floor(open_ms/N·60s) 与交易所 K 线对齐, 桶换号封口), 影子跟单仍 1m 判 TP/SL, 启动直拉 N 分钟 K 线 200 根预热(fapi→data-api.binance.vision 兜底; backtest=true 不喂; 失败退化慢预热 WARMUP×N), 统计行 S35聚合(封口bar/种子)+START 行 bar_agg=; 病灶=majors 1m SL 距 0.08-0.22%≤来回费 0.1%; 离线 harness 6/6(1000×1m→66 bar OHLCV 精确/ISO Z+08:00 ms ns 秒同桶/种子桶去重+乱序丢弃/bar_agg=1 零差异/vision 兜底 199 根 2.4s/backtest 跳种); 实证 08:46:31 首个封口即发信号 SL 距 0.88%; 回滚=rollback→998 或 bar_agg_minutes=1 | `S35` |

下一个新锚点编号：**S36**(S23-S35已用; S34=sandbox sim-clock, S35=majors bar聚合)。⚠️锚点自S24起分fork谱系: main=S4..S23+S30+S31+S33(S32关guard在场);**majors=main谱系+S35(tpl1056);sandbox=main谱系+S34(tpl1055)**;trend=+S24;fade-v2=+S25+S28;breakout=+S26(其均值回归核档案化)。apply前grep按该载具fork谱系核验。原注记:(历史上 S10 曾存在于注释引述，编号不复用)。
静态必留 6 符号（v10 既有，与上表取并集）：`on_market_message` `_emit_signal` `_append_bar` `self.pub.publish` `_init_symbol_state` `_purge_idle_symbols`

## 3. 已确认机制
- **logs q参数LIKE通配透传(09-03实证)**: GetStrategyLogs的q直拼`LIKE %q%`,q内嵌`%`可透传→`ZKC/USDT%时间=2026-09-02T14:1`即按币×分钟窗打捞DB全量日志,绕开limit2000近期窗冲刷;历史取证(veto链/入场链回验)自此不再受'日志窗流失'限制(源码strategy_handlers.go L338-340)。

- **[新增 08-29 15:2x] rotate符号形态+新壳sizing缺省(双源码定案)**: ①rotate add/remove必须X/USDT斜杠形态——裸名EqualFold不归一化=静默no-op且响应仍报removed;执行后必GET /symbols复核feed数 ②新壳config缺order_amount_mode=percent_balance+conf_sizing五键→引擎走notional默认路径→5U粉尘单;建壳checklist⑥为此设,首单必验名义≥21U。全文+源码行号git 6093c9f版行(§3本条09-01瘦身压缩)
- **[新增 08-28 03:4x] max_consecutive_entries_per_symbol=连开cap语义(源码定案strategy_signal.go L237-289)**: config字段(main/v2=模板默认3;trend=#69回滚值3,重开披露见§1.5行);计数=entry订单DB尾(requested/new/partial/filled)从最新往回数同币连续,遇他币即断,**无时间衰减**→小池/单币池=终身笔数…〔全文git 91e4749〕
- **[新增 08-27 04:4x] 后端DB层周期性全局阻塞(平台事实,n=2)**: 签名=进程活/鉴权秒回/一切触DB端点无限挂(MySQL 40连接池耗尽,Go池等待无超时);止血=#73驱动级超时补丁已部署@08-27;探测口径 /api/health/db 2s定判;拖死源未定位=复发候;全文=git 955cb46版§3。
- **[新增 08-26 21:2x] -4411 TradFi-Perps 协议类币不可交易(平台事实)**: SNXX/USDT 08-25 14:31 触发信号→下单被binance -4411拒(需owner在币安签TradFi-Perps协议)且烧掉当批择优(候选失败=本批无标的)。SNXX现已随重播种出feed=零现患;含义: auto选币可能再选入此类币,再现→bl该币或TG owner签协议。
- **[新增 08-26 03:2x] balance_usdt=availableBalance(源码定案)**: optimize_handlers.go L417-418 余额只暴露可用(注释原文'钱包=可用+冻结');binance.go L266-285=/fapi/v2/balance availableBalance→有仓时初始保证金被冻结不在此数。**含义: 刹车钱包基数=balance_usdt+Σ(名义/杠杆)±unrealized**;跨轮钱包对比必须补回保证金(08-26实证:169.20→154.86非亏损,=WTML空仓29.67/lev2冻结14.84)。
- **[压缩@08-28] bar计数器≠重启时钟**: IDLE top计数非重启钟,重启判定用feed漂移+行为证据〔全文git 4fc0468〕<!-- 压缩尾巴: -->
- **回测默认7天窗陷阱(08-25 00:2x源码+双实证)**: POST /backtest 漏传 start_time→默认 now−7d(strategy_handlers.go:113-114);1m×7d 磨不完呈僵尸样。烟雾窗≤18h 必须显式传 start_time/end_time(task31=18h窗10min完 vs task34/35=7d窗数小时未完)。

- **[新增 08-24 12:4x] apply重启作用域=载具级(n=2定案)**: 08-23 tpl823与08-24 tpl887两次apply后,他载具feed(main89→89/90→90,trend2→2)与IDLE计数均连续=只重启被apply载具;prompt v3.1"疑全局级"废除;08-09全局重播种例归部署级路径。推论: apply不再制造main feed漂移,漂移主源=crash loop/部署重启。

- **[新增 08-20 06:3x] 引擎连开限制=同币同向连续开仓≤max_consecutive_entries_per_symbol(默认3;strategy_signal.go);错失/避损审计计数在§6;全文git 0940c62**
- **[压缩@08-28] main评分天花板0.76=门0.80不可达(#61空臂根因)**: 7因子加权低波折扣后长侧上限0.76;门0.90=事实关闸〔全文git〕
- **[速记·压缩@08-15 19:2x] 架构升级f4848f8部署清单(08-15 14:xx owner通告,双实证)**: 跨策略同币互斥闸/WS标记价守护(TP-SL反应~1s)/赢家金字塔(roi≤0硬拒)/收养去重/一开仓一行/SL棘轮=✓live;#37 logs-limit未并入(limit=300仍返100);#15①引擎侧已实现;部署分支=main(owner自部署)〔全文git 45e2f36〕

- **[压缩@08-20] 收养竞态→一仓多行双守护互搏(#57,BEAT全证据链08-17)**: 账户级对账器把交易所仓回声收养到**非开仓载具**名下(fade停机壳3例实证=收养错标磁铁)→同仓两行两守护互搏(TP/SL重复cancel/replace)。**修复a451d32=收养归因按开仓者(候owner部署=#58候)**;部署前缓解=fade壳seed5+auto_symbols=false硬停;逐笔归因纪律=closed行sid存疑时按开仓者日志链裁决(§3归因方法论条)〔全证据链git bb3b883〕
- **[压缩@08-28] DELETE不杀进程竞态→幽灵载具(brk案)**: 删行不停runtime;⚖️翻案08-18可验证成交=0;#53卫生项候部署〔全文git 95e4c9f〕

### 盈利侧
- [已确认·速记] E2 空头门槛0.60对齐→空头转正(+11.27摆动);关账07-23✓〔全文git 29eb4c5〕
- [已确认·速记] E2-long long_thr0.70修复→长侧转正(+30.65摆动);0.70/0.60=台账锚定双门槛,变更须🔴〔全文git 29eb4c5〕
- [已确认·速记] RECOVER L2→L1升档一评achieved后regime三翻,eval窗敏感性教训在案〔全文git 29eb4c5系〕
- [已确认·速记] LADDER S2降档止血achieved,L2保留〔全文git 29eb4c5系〕
- **[已实证] 引擎侧置信度动态仓位**：名义 = avail×pct×lev×mult，mult∈[0.6,1.4]，21U 名义地板（`conf_sizing_min_notional_usdt=21`）在位。用户"按置信度动态下单量"直令由引擎满足，**模板代码勿双重实现**（07-20 HANA 53.12U 精确命中实证；07-22 复证）。16:16Z 三证：AKE 80.83U=151×0.19×2×1.41、MIRA 59.73U=110.6×0.19×2×1.42，双 mult≈1.4 高置信，且反证引擎 sizing 基数=开仓时可用余额。

### 亏损侧
- **[新增 09-19 03: 赢单到 +1.5% 前 MAE 中位 0.6×ATR/p90 2.4×ATR(n206), 0.4% 刀杀 56%; 常备知识=止损距离以 ATR 为单位设计, 固定%/固定金额刀在多币池必被第一波打掉; 全文 git 0e0195f版§3
- [已确认·速记] 出场体系错配: hunger30m首检批量收割未成熟仓(30-35m桶50%集中死亡/-51.39簇)→hunger45修复achieved@08-03(30-40m簇归零,45-58m wr73.7%);hunger45/0.05/0.08=现基线(#20 keep)〔全文git bbf8046前史〕
- **[速记·压缩@08-15 15:4x] SL穿刺两亚型→S18定案(08-03~08-05)**: 亚型A高ATR零复现;亚型B低ATR闪跌(n8/−18.79全long高置信带,机制=S8偏EMA2.0×ATR vs SL2.5×ATR仅0.5缓冲+砸盘量计为放量)→S18上线keep@08-05(穿刺2/−4.95收窄73.7%,ex穿刺+28.01,a/b型根除;残余c型→#22a已终止@owner直令);S18锚点永久保留〔全文见git 541a2bc〕
- **[已确认→已修复(E2 achieved @07-22 20:11Z，结案)] 边际空单带 0.55-0.60 曾是唯一五窗全负方向的主要失血源**（E2 hypothesis @07-21：24h short −5.99 wr34.1% n44，而多头 12h wr56.3% 净正）→ 修复经 21.8h/新增 ~25-26 对独立样本评判达成，见盈利侧 E2 已确认条目。
- **[已实证] 引擎不执行 `symbol_reentry_cooldown_minutes`**（07-21 06:19Z 快速档回滚 90→45 实锤：改动后 AKE 10 对/6h ≈36min 节奏，违反 90min 上限 ≥2x）。churn 治理只剩代码级 per-symbol 重入 gate。
- [观察中·速记] CB节流不根除重犯币(RIF/ONE/BANK/SYN 07-22~23多轮观察;CB在线双实证;重犯加时候选=§4#7,持久化=#35)〔全文见git 6b6a4bf前史〕
- [已结案·速记] 15-60m桶失血主体→随E2修复翻正为主体盈利桶(07-21→22七读链;桶健康度并入常规归因扫描)〔全文见git 6b6a4bf前史〕

- [已确认·速记·压缩@08-20] 短侧穿刺簇RECOVER=rollback(08-06): 双向解锁段严格短穿刺9例/−16.70(顺涨开空被延续穿刺),ex穿刺段+6.5;一步回防+S19镜像veto同窗上线(tpl563);裁决=双向未证伪,缺S18短侧镜像;恢复阶梯cd优先(§1 08-02直令)〔全文git 0980231前〕
- **[已确认 @08-07 #20 KEEP] hunger_tp 0.06→0.08 放大赢单腿成立**: 段n44 payoff2.42>回滚线2.0; 快SL 17/−31.14 转S21依据; 全文=git d19a237版§3。

### 平台事实
- **TradFi永续类@09-10**: 币安2026中推股票/ETF USDM永续(KODEX200/SOXS/三星/SK海力士系官宣),未签TradFi-Perps协议=-4411拒开仓(GPRO直证);select_limit auto按量选池会捞入该类(09-10捞6只);处置=TradFi类隔离(§1.5)+嫌疑watch(§6);签约决策权owner。
- **closed48拉取可夹带跨月陈旧行(隔离/晋升门污染源)@09-10直证**: hours=48 吐2847行仅~50在真48h窗; 纪律=按close_time预过滤; 对账器v1.6内置防御; 全文=git d19a237版§3。

- **平台宕机窗 08-30 ~08-10Z → 09-01 ~09Z(≈49h,无端重启族最长)** @09-01定谳: 证据=双载具日志探针(main 时间=T07/T08有行,T10后至09-01早全空;trend ts=同型0行)+closed48h=0行+钱包164.17→161.09全程冻结(差额=宕机前…〔全文git f0b0c0a〕
- **logs ?q 中文子串=0字节空响应**(08-28 06:2x实证:q=触发开仓 两种编码均空,q=VELVET/USDT等ASCII正常;检索开仓事件用 q=<SYM>/USDT 再grep,勿用中文关键词)。


- **API层收养归属向量(08-26源码闭环)**: GET active 同步收养按静态config.symbols定归属(无bl/running检查,优先于orderMeta)→entry落库竞态窗内row sid可错壳(VELVET双案);错归因活仓仍被quick_trade_monitor托管=风险有界;修复=#72(4385fc3候部署);全文=git 955cb46版§3。

- **closed视图sid=owner域限定(源码+行为双证@08-21 18:1x)**: a451d32归因两路(order_id主径+±5min DB兜底,positions_binance.go)均按 `owner_id=uid` 过滤;main归owner1、专家载具归owner2=cron(uid2)查询域→**main行恒EMPTY=结构性非回归**(部署后新行FARTCOIN空17:31空sid,而main日志17:17开仓全链实证)。…〔全文git 91e4749〕
- **[新增 08-24 21:2x] closed(binance_only)行归属=两道回填,双空手⇒sid空串(源码定位)**: positions_binance.go L262-274 orderid匹配→L310-332 symbol+close_time±5min DB行兜底;48h实证main名下sid行=0、main池12行全空sid(trend/v2…〔全文git f0b0c0a〕


- **[压缩@08-28] logs?q超时形态**: 稀有子串大回看可>30s/空响应,重试或缩limit;EXIT_AUDIT标签可用〔全文git;新LIKE短路条在下〕

- **平仓撤单-2011竞态+补设竞态=无害自愈**(order does not exist=已成交/已撤;引擎撤单失败继续平仓流,重复保护单被交易所-4046拒=幂等;全文git bb3b883)
- **[压缩@08-24] 账户级position行sid=陈旧symbol→strategy映射伪影(08-14定案)**: closed行sid可挂错载具(STAR空挂brk名,brk buy-only物理不可能=铁判据)→归因禁用行sid,主口径=池归属(§1.5)+方向可行性;08-15收养去重部署后新行盖真sid,存量旧行仍伪。全文git 2ce0fb4系
- **[压缩@08-28·合并三条] stop异步语义**: stop回执=入队非落地,迟滞≈池规模(main 15-44s,小池~1-10s),期间PATCH被'while running'拒→必须轮询stopped;回声行可挡停〔全文git:08-10/08-12/08-14三条〕
- **DELETE /strategies/:id/blacklist/:symbol路由对含斜杠币名404(gin UseRawPath未开,%2F不解码)**: 全币种皆含/USDT=接口整体不可达;黑名单改动唯一通路=stopped窗PATCH symbol_blacklist全量。候dev分支修复(非紧急)。
- **main stop被账户级在途持仓卡死(行为实证@08-22)**: stop判空仓以【账户级】现拉持仓为准,他载具在途仓可卡本载具stop→PATCH窗须全账户真空仓;全文git 8a797ec

- **[升格v2@08-23;再燃@08-24 06:3x] 平台级crash loop(非apply非start路径;修复权=owner硬边界#6)**: 检测=recv回落法(main IDLE top计数跌回~202=重启+回填200bar签名)。影响=清内存态(CB/统计/rotate态/择优攒批窗)不清DB(c…〔全文git f0b0c0a〕

**[v2 增补 @08-01 16:40Z]**
- **逐笔平仓 API 已验证存在**：`GET /api/positions?status=closed&hours=N&source=binance_only` → 入场/出场价、realized_pnl、open/close_time 全量（本轮 48h 拉到 72 笔）。旧结论"无逐笔明细"**作废**。归因主武器。
- **[压缩@08-24] 镜像行双记账(08-09实锤)**: 专家载具开仓且symbol同在main live feed→DB给main写同qty镜像open行(卡stop/rotate+归因幻影+反向净额合并险)。根修=引擎同币互斥闸✓部署08-15;缓解=live feed严格不相交(ROUTE每轮)。全文git 95e4c9f系
- **[新增 08-09 18:4xZ] stop 语义=入队异步**：POST /stop 回执 {"status":"stopped"} 仅=入队成功;真执行在单 stopWorker,失败只写策略 error 日志(如"has open positions"),API 无感。**stop 后必须 GET /api/strategies 轮询确认**,失败诊断=安静秒窗(:34-:59)发 stop 后 2s 拉 logs 抓 error 行(18:46 实证有效)。
- **[新增 08-09 18:4xZ] start 中途不感知 stop**：start 亦入队(startCh 单 worker),boot 完成无条件写回 running(lifecycle 源码+4 连复活实测);对 running 实例发 start=纯 no-op(不重启进程,S23 分段无害)。窗口操作纪律:先静置排干队列→单发 stop→轮询确认→PATCH→单发 start。
- **[速记·压缩@08-16 15:5x] stop=乐观回执+校验双洞(main实测不粘)**: 回执≠沉降轮询才真;sid=""仓隐形+auto池跳过交易所侧校验→main可带仓过闸(硬边界5被绕1次无损);stop后~40s可自动回running(机制未定=#52);**程序v2=自查空仓→stop→1-2s紧循环PATCH竞速→start→验bl**;全文+18:27全录git ffebcb4系
- **出场三层语义（源码核实）**：①策略 TP/SL(atr_tp/sl_mult×ATR，平台执行) ②饥饿模式(quick_trade_monitor.go，10s tick，持仓≥hunger_after_minutes 后首检 |roi|≥hunger_tp/sl_pct×100 即市价收割，roi=价格变动%×杠杆) ③max_hold_minutes 无条件平仓。②与①不匹配=已确认病灶（见亏损侧 08-01 条目）。
- **hold_distribution 只覆盖部分仓位**——死法分析以逐笔 API 为准。
- **backtest 接口可用**：`POST /api/strategies/:id/backtest`（async=true），大改 apply 后烟雾测试用。
- **[压缩@08-28] ctx结构**: paired_trades→trades_window(count/wins/losses/net_pnl等);币安侧by_symbol并行在〔全文git〕
- [速记] 监控盲区DB↔币安失同步: monitor只扫DB open行,行缺失→实仓脱管漂移(KOMA 29h/-15.13 n=1);#15 sweeper候选;US'第二例'证伪〔全文git dba6d19前史〕
- **[新增 08-02 06:11Z] max_hold 计时锚 = 币安 pos.OpenTime(updateTime)，饥饿模式计时锚 = 本地 open_time**（quick_trade_monitor.go L85-89 vs L103 源码核实）：币安 updateTime 会被仓位变动刷新 → 实际 hold 可超 max_hold_minutes（post-FIX 实例 PROM 89.8m/120.3m，均盈利良性）。hold>60m 非故障；死法分类时 60m+ 桶不可武断归为硬超时。
见 v10 附录C（stop 需空仓、apply 模板泄漏、Binance 直连 451、日志窗 ~100 条/几秒、`daily_pnl_7d` 停更等），不在此重复。

- **[结案压缩] logs端点慢查询→索引+保留清扫已部署验证@08-03(实测1.44s;保活体系齐备;首启建索引期HTTP不监听数分钟=非故障)**。全文git 0980231前史。
- **[压缩@08-20] closed·binance_only重建=窗口边界相位移洞(08-03源码+双窗实证)**: FIFO配对不播种窗口起点前在持仓→跨窗起点仓整链错位(平当开/方向翻),可吞真单(US −2.36实证;168h漂移链=纯伪影)。**纪律: income by_symbol=pnl真相源,逐笔死法每轮by_symbol↔positions交叉核对;分析侧拉hours+2再滤末48h;根修=#18**〔全文git 0980231前〕
- **[新增 08-03 18:2xZ] 代码 hash 双口径**：ctx `current_code_hash`=sha256(TrimSpace(code))；apply 返回 new_code_hash=sha256(原始请求串)——发送含尾 LF 时两值不同=正常非漂移（本轮 e4c7ab vs 8dcedd 实锤，取回代码字节级一致）。baseline_hash 用 ctx 口径 ✓（apply 侧同走 TrimSpace）。
- **[新增 08-03 03:5xZ] DB StrategyPosition 行=空壳+重复**：近期行 amt=0/avg_close=null/pnl 多 null，且每仓 1 真行+1-2 条开仓后 1-2s 即闭伪行（direction 有时空）——DB 口径禁用于归因，仅作 strategy_id 溯源；closed?source=db 无 hours 过滤=全史返回。
- **[压缩@08-28] vision日档=1m K线源(T+1)**: 容器451只封api/fapi;https://data.binance.vision/data/futures/um/daily/klines/<SYM>/1m/ 可curl〔全文git〕
- **[压缩] 回测通道两缺陷(08-03)**: ①window≥24h挂死(后经08-12定案条揭机制) ②模拟入场饥饿=冷启动零缓存+喂线仅OHLCV→系统性低估入场;烟雾标准=完成不崩非成交数;修复候选#21。全文git 0980231前史。
- **[压缩] 回测v1事故三平台事实(08-02)**: 演化策略socket版MiniRedis直连生产redis事故+信号过滤无boot_id校验+backtest无看门狗;原则=凡spawn策略子进程先审redis_addr注入。全文git 0980231前史。

- **[速记·压缩@08-15 15:4x] 部署链核验(08-05)**: 回测v2/A/B/logs/保活≥fadc14a 在产;用户部署分支=main(cron严禁推);62cb2fd klineHub 已生效(0 fallback);单符号回测0成交限制维持(冷启动+缺跨币因子,#21);三角套利Phase1只读=owner新产品线与本策略资金无交互〔全文见git 541a2bc〕
- **[新增 08-05 17:5xZ] PATCH /strategies/:id/config = 浅合并语义（源码 PatchStrategyConfig 核实+实锤事故）**：请求体=直接字段 map（`{"cooldown_sec":900,...}`），逐键覆盖 current，**值 null=删键**；发 `{"config":...}` 包…〔全文git f0b0c0a〕
- **[压缩@08-28] 代码入库直令首轮@08-05**: 归档协议自此运行〔全文git〕
- **[新增 08-06 02:4xZ] 冷却双层机制(源码裁决)**: cd_sec=Python侧per-symbol信号冷却(仅启动载入);引擎真闸=symbol_reentry_cooldown_minutes(strategy_signal.go L337,LastEntryAt DB锚重启存活);同币再入受max(两层)→re150在位时改cd_sec零效;改cd_sec需stop→start。详git 0980231前史。
- **[压缩@08-28] cron节奏**: Routine=quote_optimize;近期实测≈每3h(00:15/03:12型);以触发时刻为准不预设〔全文git〕
- **[新增 08-06 04:3xZ] apply=DB换绑+自带async restart（绕持仓保护）**：ApplyOptimization 不查运行态，事务换 template_id 后返回 `needs_restart:true,restart:"scheduled async"`——平台内部重启**有持仓也执行**（3仓在持…〔全文git f0b0c0a〕


- **平台事实@08-06 14:4xZ**: ①`ctx.current_code_hash`≠sha256(current_code)（tpl563:ctx 7c8c314d vs apply b64fb6ce;tpl564:ctx 0f70eff5 vs apply b4e8c650;两代绑定代码经直diff=提交逐字节一致）→代码验证一律用current_code直diff,勿用ctx hash字段。②回测执行与live共享`/logs`流（[backtest strategy]前缀+fake redis顺序喂线,单币24h/1m≈10min+,回测期live日志窗被稀释——观测铁律窗内未见≠零加倍适用）。
- **[新增 08-06 20:3xZ] start历史回灌=200根/币(manager.go:1429,rotate-in resync同路径)**: S20注释"~400根即时全功率"有误(MAX_BARS=400仅缓存上限);300m支gate需再攒101根活bar≈100min盲窗,180m支即时在线;gate放行不留日志→事后不可复盘;定案需币安期货1m K线(vision T+1)。HFT应拦未拦案全文git 0980231前史。

- **[压缩@08-28] gate7d影子=bar投递依赖**: 断供窗漏拦→S22 emit副闸已补(AIOT案)〔全文git d1400b2〕
- **[新增 08-07 22:5xZ] apply baseline_hash=TrimSpace口径**：resolveCodeForOptimize对模板code做strings.TrimSpace后sha256=ctx.current_code原文hash(81b5cf37族)≠存储模板hash(尾换行,2cab9833族)。apply 409 baseline_race时先按TrimSpace口径重算再重试,勿盲目省略baseline_hash。

- **max_hold 时钟=币安 updateTime,可被资金费结算等事件重置(08-08 02:1x 源码+逐笔实锤)**: binance.go L1540-42 映射 UpdateTime→OpenTime,quick_trade_monitor.go L84-89 优先币安钟→updateTime 刷新即重置 60m;饥饿层用本地钟不受累(亏仓 45m 照割),滞留域仅(−5%,+8%)roi 带,现净影响+2.43 良性。判据: 亏损腿 hold>75m ≥3例或单笔≥5U→M 修复(取 min 钟);亏腿计数 1/3(KAITO 08-08)。证据链全文 git 9d0dc94。
- **[速记·压缩@08-23] 双会话抢窗双写(08-08首例)**: PATCH=_exp全量替换last-writer-wins+stop/start幂等→双会话互不感知各自"成功";同源载荷无害,**异源载荷同窗竞写静默丢先写**→互斥靠§5认领行+后启会话先探audit;全文git 766c7d9系

- **[新增 08-09 06:4xZ] ctx 两口径（源码核实 optimize_handlers.go）**: trades_window(data_source=binance)=buildTradesWindowFromBinance 打包，count=成交腿数（24h 194腿 vs 逐笔配对61笔，分批平仓一笔多腿），long_pnl+short_pnl≠realized_pnl 属口径差非bug；paired_trades 键仅 DB 源变体出现。avail 权威读径=ctx.binance.balance_usdt（ctx.account 无 balance 字段）。逐笔归因一律以 A 武器 closed48 配对行（滤无 realized_pnl 幻影行）为准。
- **[新增 08-09 09:3xZ] positions.realized_pnl=税前毛额(不含佣金/资金费)**: 全文=git c06ab72版§3。


- **[压缩@08-28] closed行sid归因史**: 专家sid始08-09 19:05,更早行回退池归属法;owner域限定+双回填条在下〔全文git〕

- **[新增 08-10 18:2xZ] 日志置信度=折扣后值(四例精确复算)**: 未触发信号/评估行的置信度=加分项和×低波动折扣0.8(0.65→0.52/0.75→0.60/0.95→0.76/0.70→0.56全吻合);过0.55基础线仍可被后级门拦(长锚0.70/ATR%<0.5硬滤/sides)。读日志勿把置信度当原始分;S24/25/26谱系继承同口径。

- **[升级@09-12 18:1x] 宿主DNS故障(Tailscale MagicDNS SERVFAIL)=复发性平台向量 n=2**: 08-11停机〔全文git〕+09-09 16:12Z起≥74h交易全停(episode细目§6行);特征=全域名解析死而既有WS连接存活→REST/下单全灭+重订阅危殆;修复在宿主机;工程对策=#89 DNS韧性补丁(§4)候部署;断连窗纪律=禁stop/start
- **[新增 08-12 06:3xZ] 回测挂死机制定案(bt29+源码)**: 取数FetchHistoricalCandles先于watchdog装载→取数挂起=running永久无守护;API无cancel端点;不阻塞实盘;修复候选=fetch前置deadline或cancel端点(低优先)。案详git bb2e前史。

- **[压缩@08-28] owner直令可双投递并发会话**: 执行前查audit最新态防重复动作〔全文git〕
- **[压缩@08-28] BOOT RESTORE**: 后端重启自动拉起DB态running/starting策略(lifecycle.go);gated壳靠DB态stopped免疫〔全文git〕

- **对账器版本管理(08-14确认)**: 新cron容器工作树=默认分支→ops/route_pools.py只有v1.0初版,直接跑=错误plan(实证2例:08-13容器v1.0误提议拆trend/fade池;08-14误提议清空trend池+COTI越brk直入trend)。修根@08-14:权威副本入quanty-ledger分支ops/route_pools.py,每轮fetch台账即得现版;改对账器=ROUTE预注册,改后同步台账分支副本+§1.5谱系行。

- **logs?q=检索选择性=LIKE短路(08-27 21:2x实证)**: q子串扫描按limit短路——高频子串(0.600/IDLE/币名)秒回;稀有子串(触发开仓/32.48/时间戳前缀)=全表扫>30s超时code000,与中英文编码无关(ASCII稀有串同样挂)。回溯稀有行正解=拉高频伴生子串宽窗后本地grep(触发行含conf数值故q=0.600可达);重启后每币~200预热行会吃掉币名q的limit窗。
- **[新增 08-28 00:3xZ] 高价币最小手数静默拒单=MVLL型定谳(#75;源码闭环+双案)**: 单枚价>sizing名义的币,最小手数凑整后名义超sizing上限→引擎静默拒单零日志;候部署#75加日志行;验证轨=部署后grep最小手数凑整;池内高价币(>20U/枚)受影响。全文+源码链git 9957041版行(§3本条09-01瘦身压缩)

- **[新增 09-18 20:2x] 饥饿刀首查时点+DB 漏行+CYPH TradFi**: 全文=git c06ab72版§3(要点: 饥饿首查=hunger_after 整点 10s 轮询→过冲; closed 表漏行以 income 为准; CYPH -4411 隔离)。

### 双执行体(09-17 起)
- **[新增 09-19 11:4x] owner 11:06Z 批签名 + symbols "" 语义 + 跨实例槽位干扰(源码)**: ①11:06:38Z admin 三 PATCH(main hunger_mode_enabled=false / main symbols "" / majors mcp 2)+双壳 11:08 重启, 无 _exp、非 DS 3h 节拍、symbols 在 ops/deepseek_optimize.py FORBIDDEN_EXACT、重启非 DS 脚本能力 ⇒ 归属 owner(API 走 admin 号; 候认领). ②strategy_start.go:224-241 resolveFeedSymbols: symbols 空 ∧ (mode=filter ∨ auto_symbols ∨ min_price>0 ∨ **max_price>0** ∨ …) ⇒ useFilter=true → 按成交额选 select_limit 300 ⇒ 07:21 的 max_price 1e12(治 0=全拒)使 symbols "" 直接回到 300 池; manual 模式名存实亡. ③manager.go:287-313 isAllowedSymbol: config.symbols 空 ∧ symbol 空 ⇒ **恒 true**(:312) → strategy_signal.go:611-627 把交易所全账户持仓(含 majors XRP)计入 main openCount ⇒ 11:19/11:23/11:30 '已持仓4个≥3'(main 自持 3/2 仓); 修复路径=main symbols 显式列表(重启键)或 mcp 上调(owner). ④DB 同仓双写(#77 候部署)在 hours 参数失效的全量 closed 读径下放大 1 倍(1752→去重 1433): 去重键=symbol+direction+close 分钟+pnl 2dp; 'manual' 行 hold≈0 = 重启/手动平仓记录. ⑤429→'设置止盈止损失败,触发事务回滚'→回滚也 429 → 仓位裸奔 28s 后 tpsl_monitor '已补设交易所止盈止损'自愈(ZAMA 11:11:36→11:12:04); '仓位缺少有效止盈止损配置' 20 行/19h 每币 1 行且与平仓/重启同刻=瞬态, 无持续裸奔证据.
- **[新增 09-19 10:3x] tp/sl pct=0 ≠ 裸奔(源码+日志实证; 纠正 prompt v5.1 协同协议 11①/硬边界 11/引擎速查的前提)**: strategy_execution.go resolveTPSLFromROI :305-307 当 take_profit_pct/stop_loss_pct 均 ≤0 时**原样返回信号自带 tp/sl**(非 0/0); hasEffectiveTPSL :278-283 信号 tp>0∧sl>0 即 true → 交易所 algo 腿照挂. 实证: main 07:21 后 7/7 开仓均有 '已设置止盈止损 … sl order_id'(SL 距 1.28-3.0% 价=2×ATR, 钳 0.3/lev), majors 08:46/09:01/10:16 同. 真裸奔条件=信号 tp/sl 也为 0(Python 兜底 sl_ratio0.03/tp_ratio0.06>0 在位即不可能, 见 09-18 陷阱条). 5.5 核对口径改为: 每 running 实例最近开仓有 '已设置止盈止损' 行 ∧ (sl_ratio,tp_ratio>0 ∨ pct>0) ∧ max_hold>0.
- **[新增 09-19 10:3x] DS 改动簿 #9**: 09:37 breaker lock-1789810634 mcp 6→3 未声明(协议7#3 第 7 次) + 10:09 optimizer lev 10→5 裸改 + majors 10:03/10:06 atr 净零; 裁决=不硬抢, watch_ds; 全文=git 29d8478 版§3。
- **[09-19 07:3x] DS 改动簿 #8 + 熔断器副本漂移 + 优化器禁改缺口 + max_price=0 语义**: 要点=07:08 pct 0.25→0.125 无锁(宿主副本≠仓库 087a6dd floor0.25)→CC-24⑦; FORBIDDEN_EXACT 缺 symbol_select_mode/max_price/_exp_cc/_ai_task_cc/_standby_cc/hunger_*/min_confidence→CC-24⑧; max_price 必须 1e12 非 0(0=全拒); 全文=git c75a1c3 版§3。
- **[09-19 06:3x] REST 轮询地图(源码)+本地 TP/SL 陈旧价假触发**: 每仓 2s positionRisk 权重 5; 补丁 claude/dev-rest-budget 61631b0 候部署; 全文=git 6b44db7 版§3。
- **[新增 09-19 08:4x] Python stdout 日志不出 API(部署版)**: `_log`(stdout JSON) 行——START/统计汇总/IDLE/收到首根/S35 种子——在 /logs 中 main/majors 均 n=0(09-16 起统计汇总缺失同因) ⇒ Python 侧只能由 Go 侧动作行(候选/置信度动态仓位/开仓/已设置止盈止损)反推; S35 预热成功判据=首信号出现在首个 N 分钟封口(实证 08:46:31), 慢预热=空窗≥WARMUP×N。
- **[新增 09-19 08:4x] 无 audit 写路径**: 平台 UI 改 config 走 PUT /strategies/:id/config(UpdateStrategyConfig)且 start/stop 端点均不写 audit(audit 50 行全 patch_config) ⇒ majors 08:01:47 start + 6 键改动(mcp2→5/+XRP/hunger off/bar_agg_minutes 15/min_atr 0.03/symbol_reentry 15)零 audit=owner UI 归属; 无主改动巡检遇此签名→先 TG 认领再动 config(代码 apply 权在 CC)。
- **[新增 09-19 08:4x] apply 契约(optimize_handlers.go)**: 4 符号 guard→py_compile→baseline_hash(sha256 原字节)→param_drift(>2x 拒)→建模板换绑+stop(force)+start; 全文=git 29d8478 版§3。
- **[新增 09-19 09:2x] 回测引擎非确定性实证**: 同请求 3 次 10/12/12 笔 −12.3/+3.6/+7.6, 首分歧=首入场时刻(根因 CC-29); 单次回测符号不可信; 全文=git 29d8478 版§3。
- **[新增 09-19 09:2x] 饥饿触发价≠成交价亚型**: 触发 roi 与实 roi 差≥2pp=陈旧价/插针族(G 08:39 −4.30%→实 −1.8%); 全文=git 29d8478 版§3。
- **[新增 09-19 08:4x] 回测引擎事实**: POST /backtest 支持 timeframe+config_overrides; 结果暂不可作晋升门; 全文=git 29d8478 版§3。
- **DeepSeek管线事实**: 宿主cron ops/deepseek_optimize.py(experiment_hold冻结键)+ops/qt_breaker.py(熔断: 净≤−4U 或 wr<45%∧净<0→单向砍pct,锁24h续期自到期);admin#1;节拍≈3h+熔断+探针;只动config不动代码;_exp schema id(lock-*/exp-*)/status/changed/frozen_keys/eval_after/prior_exp/mechanism/why。改动史09-16 13:33→09-18 10:27(lev10/pct0.15越界/sl-tp/cd阶梯/mcp三连裸改/be_atr/max_hold/max_initial_margin)全文=git 7f578a7版§3。
- **[09-19 05:5x] DS 改动簿 #7**: 04:49 _exp owner-unlock+pct 0.25 / 04:53 hunger_stop_loss_pct 0.30→0.04(同 actor, 按 owner 值候认领; 覆盖 p9 2×ATR SL 腿 quick_trade_monitor.go:112); 全文=git d19a237 版§3。
- **[新增 09-19 05:5x] 桥接事实**: _ai_task_ds 自 09-17 始终 null ⇒ ops/ai_task_bridge.py sync 从未在宿主成功运行(未装或凭据错); 容器不可读 /tmp/ai_task; 留言板=唯一通道。
- **[新增 09-19 05:5x] 空头无正期望取证(方法可复用)**: 现货 1m 对 24h 空单 n45 算入场前 r1..r30 与 ATR: 赢/输中位无差, BTC 平, 括号反事实 −16.2 vs 实际 −17.5 ⇒ 空头亏损=信号本身; 全文=git d19a237版§3。
- **[新增 09-19 00:1x] DS 改动簿 #5**: 21:22:13Z breaker lock-1789766533(至 09-19 21:22) pct 0.25→0.125(声明)+mcp 10→6(未声明);CC 不硬抢(CC-9);**breaker 只要 24h 净<−4U 或 wr<45%∧净<0 即每次节拍再砍并续锁**→owner 侧改阈值前任何恢复都是乒乓。
- **[新增 09-19 00:1x] 入场单类型=MARKET(taker)**: strategy_signal.go:745 price=0→binance.go:1372 仅 price>0 走 LIMIT GTC(无 GTX);双边 taker≈0.1% 来回(24h 手续费 40.59U/名义 40.5k 实证)→费率病根 M 候选 CC-19。
- **[09-19 00:1x] 入场 ATR% 反推法 / 硬过滤拒绝不落后端日志 / Python 进程有效值(00:25Z)**: 全文=git 6b44db7 版§3(要点: tpsl 日志 sl 距%/atr_sl_mult=Python ATR%; code:1155 reject_reason 只进 signal detail, 门是否生效以成交 ATR 分布为准; START 行=ground truth 但部署版 Python 日志不出 API→以注入 config 现值为准)。
- **热PATCH实证@09-17 07:4x(CC探针 _probe_cc_running_check 写+null删 http200 param_version v17→v18,策略running不变)**: 部署后端接受running PATCH;仓库main manager.go:1737 "cannot update config while strategy is running"线上不存在=**部…〔全文git f0b0c0a〕
- **有效门槛反推法**: 日志"置信度动态仓位 symbol=… conf=… mult=… pct=…"=逐单成交置信度;09-17 07:xx见conf 0.40/0.44成交→有效门槛≤0.40(config min_conf0.45,lcp/scp 0)。
- **Go侧LLM重写器(strategy_autotune.go)**: enabled=false不跑; apply=true/dry_run=false上膛(main; majors 已 dry_run); 会让模型重写全码并 stop/start, 只校验 def run+py_compile, 不校验 S 锚点 ⇒ 若被打开=S 谱系风险(TG🆘+重拉 grep 锚点); 全文=git 6b44db7 版§3。
- **命名空间**: _exp=DS(CC只读,刹车frozen_keys append例外);_exp_cc=CC预注册(+watch_ds);_ai_task_cc(CC→DS)/_ai_task_ds(DS→CC)各保3天≤8KB;宿主/tmp/ai_task=owner可读合并日志,经ops/ai_task_bridge.py桥接(owner装DS侧)。
- **现货引擎事实@09-17(源码)**: 全文=prompt v5.1【现货载具】节◆引擎事实/git b341694版§3(market进程级→第二进程;可用=市价买卖+本地TP/SL;不可用=饥饿/max_hold/ROI/交易所TPSL/追踪/保本/收养/余额sizing;amount静态→S34改名义/现价;funding非usdm=0→S30/S33失活)。
- **单笔风险恒等式**: SL亏损=余额×pct×sl_roi(杠杆约掉;09-17 114×0.05×0.12≈0.68U/次);杠杆只改SL价距(噪声穿刺频率)与名义/手续费规模。
- **键归属表(DS-5结案@09-17 10:2x;grep backend/internal/strategy Config["…"] vs current_code self.cfg.get)**: Go热(热PATCH即生效)=leverage/order_amount_pct/order_amount_mode/max_concurrent_positions/allowed_sides/entry_time_windows/symbol_blacklist(交易级)/use_exchange_tpsl/take_profit_pct/stop_loss_pct(normalizedTPSLPct)/hunger_mode_enabled/hunger_after_minutes/hunger_take_profit_pct/hunger_stop_loss_pct/max_hold_minutes/trailing_enabled/trailing_callback_pct/trailing_activation_atr/breakeven_trigger_atr/conf_sizing_*(5键+conf_lo)/pyramid_*/symbol_reentry_cooldown_minutes/max_consecutive_entries_per_symbol/min_hold_seconds/auto_optimize_*;启动时读(重启生效)=select_limit/symbols/auto_symbols/symbol_select_mode;**Python侧(须重启)**=min_confidence/long_conf_premium/short_conf_premium/**cooldown_sec(Go零引用,归属定案)**/best_pick_enabled/best_pick_window_sec/warmup_bars/volume_ratio_min/breadth_min_for_long/breadth_max_for_short/max_atr_pct/min_atr_pct_for_trade/atr_tp_mult/atr_sl_mult(信号tp/sl;Go另用于exitATR)/atr_discount(_thr)/reject_on_chop/trend_confirm_bars/min_volatility/min_precision/max_price/trade_amount/cb_*/chop_lookback/vol_lookback_bars/stats_log_interval_sec;双侧=max_hold_minutes/symbol_blacklist/take_profit_pct/stop_loss_pct(Python作信号tp/sl兜底,Go作出场)。
- **[新增 09-18 12:4x] max_initial_margin_usdt 语义**: strategy_execution.go:127-150 percent_balance 下 保证金/仓=min(avail×pct×mult, cap)(>0 生效), Go 热键; 现值 500=不触; 全文=git c06ab72版§3。
- **[新增 09-18 10:2x] Go侧SL距离clamp**: strategy_position.go:486-498/:885-898 交易所 SL 钳在 entry×(1∓0.3/lev)=lev10 时 ≤3% 价(−30% ROI 上限), 2×ATR>3% 即被钳; lev20 时 1.5%; 全文=git c06ab72版§3。
- **[新增 09-18 10:2x] Python兜底比例重启陷阱**: config stop_loss_pct=0/take_profit_pct=0 重启后令 Config.SL_RATIO/TP_RATIO=0→Go 拒开; 修复=config sl_ratio0.03/tp_ratio0.06 在位(v39), 任何重启窗前复核; -4028 杠杆无效币按规则隔离; 全文=git c06ab72版§3。
- **Python进程有效值(START行=ground truth;logs q=cooldown%3D)**: 最近START 09-16 04:33:40Z(owner重启/部署,非audit动作)注入 cooldown=180s min_confidence=0.35 atr_tp4.5/sl2.5;成交conf最低0.36且折扣先于门(code 674-676→816-823)→有效门槛≈0.35(lcp/scp≈0)。**DS 09-16 16:12~09-17 04:09 的 cooldow…〔全文git f0b0c0a〕
- **breakeven_trigger_atr语义**: strategy_exit.go:171 trig<=0→maybeMoveBreakeven no-op(保本关闭);admin 08:30 1.0→0裸改(无_exp;DS或owner手改待认),watch_ds登记。**饥饿数字**: hunger_take_profit_pct0.08/hunger_stop_loss_pct0.05=ROI口径(lev2→价格±4%/±2.5%),45m后|roi|≥阈即市价平(日志"饥饿模式触发");lev2态死法结构=初始SL6%价格(几乎不触)→45m饥饿SL2.5%价格→60m硬超时。
- **手动平仓端点**: POST /api/positions/close?symbol=X(positions_handlers.go:517)仅usdm分支(撤联动TPSL+市价平),spot无路径→DS-7现货超时缺口部署前=TG报owner手平。GET /api/stats/dashboard无余额字段;ctx balance_usdt=availableBalance(binance.go:263),钱包≈avail+Σ名义/lev。
- **反事实数据源@09-18 00:2x**: data-api.binance.vision(现货 REST 公共镜像)本容器 http200(fapi/api.binance.com 451)→/api/v3/klines 1m 现货作永续代理, 归一化以入场前一根现货收盘为 p0; 09-19 又证 Bitget/Gate 永续 1m 可用(206 笔反事实); ONE/USDT 类深度差币不可信; 方法 cf2.py/cf3.py(scratch); 全文=git f48d043版§3 |

## 4. 假设库·候选队列（v2 迁移注记 @08-01 16:40Z：本节与 §6 观察计数合并为【假设库】，内容全量保留；prompt v2 起执行门槛=逐笔证据标准[≥20 笔同型死法或机制落到源码行为]，旧 v12 Step 4.6c 门槛作历史参照）
- **[候选·连开cap全组重校 @08-28 03:4x]** 结论=多币池载具cap3可自愈非死锁,仅单币池致死(trend已修),main改mces必要性下调,候选降权窗;依据链与全文=git f90d9dd版§4。

| # | 类型 | 内容 | 依据 | 复现计数 | 状态 |
| **H-0919e bar聚合封口延迟/滑点(S36 候选)** | 09-19 09:2x | majors S35 信号时刻 08:46:31/09:01:31=15m 边界后 ≈91s(桶换号在新桶首根 1m 收盘时发生 +≈30s 管道; main 1m 信号亦落在 :31-:36s); 滑点=成交−信号价(信号价=SL+2×ATR, ATR=(TP−SL)/4.5): SOL +0.036%(0.08ATR)/BNB +0.128%(0.47ATR); S36 方案=新桶首个 tick 即封口(省 60s), 回滚=S35 原样 | tpsl 日志反推 | n2 | watch(门: n≥10 且滑点中位≥0.3×ATR→预注册 S36; CC-28 在飞期不动码) |
| 85 | 观察(组死法·regime病理) | SL穿刺簇=当期唯一主失血道但无可行动修复族@09-06 21:1x(48h n19/−17.55多空对称;16/19深穿刺gap-through)全文=git 8705b00版§4 | 逐笔法医学 | n19 | 观察(lev2态后穿刺归零,见§6刹车基线读数) |
| **conf≥0.60桶=延伸段入场(追高/追跌)** | 09-17 10:2x | 09-16 16:00→09-17 07:52 conf≥0.60 n26 wr31% 净−28.79(穿刺69%<3m,mult1.4放大)= S35 入场延伸veto候选(§5 CC-4;需1m K线量化 (entry−EMA20)/ATR ≥20笔同型);cs_mult 1.0 已落地@09-18 08:12;全文git 7f578a7版§4 | n26 | open(候S35) |
| **lev2态饥饿45m收割=主血(候反事实)** | 09-17 15:2x | 已被 owner lev10 直令(09-18 08:12)超越;FIX hunger_sl 0.025 等价并入 P3(0.125@lev10);全文git 5d2c77a版 | — | 结案@09-18 10:2x |
| **lev×ROI口径出场耦合(候反事实)** | 09-18 05:2x | **结案@09-18 10:2x=P1/P2 KEEP 3/3**: tp/sl pct=0(ATR口径)后 lev2 段 n12 均+0.010 → lev10 段 n20 均+0.213(名义21→53U),SL 价距不随杠杆变(中位1.46%,Go clamp 3%)=耦合已解;全文git 5d2c77a版 | 段n32 | 结案 |
| 80 | 全文=git 9f6379d版§4 | | |
| 89 | 全文=git 9d56e10版 | | |
| 77 | 全文=git 9d56e10版 | | |
<!-- 瘦身@08-27 03:2x: closed/终态行删除(#9过时/#30/#37/#56/#57幂等闸/#60/#61/#63/#64/#69);§5维护注裁至2条(#67/#59指针在git),全文永在git c767c07^链 -->
| 73 | M通道(平台稳定性) | db驱动级超时+健康探针=claude/dev-db-timeouts | **已部署@08-27≈18:55Z(ed0c160,行为实证)**;§3挂死条 | n=2事故 | 部署✓;复发判据§3 |
|---|---|---|---|---|---|
| 72 | 全文=git 9f6379d版§4 | | |
| 71 | M通道候部署(做市模块,非策略组) | Gate做市WS下单通道(claude/dev-gate-ws-trade已推) | 全文git | n/a | 候部署(低优,非策略组) |
| 35 | M通道 | S14 CB状态持久化(restart清计数株连修复;候选=引擎计数DB落库或cron快照种子) | 方案+先例链git(08-14轮) | n=2币/−7.66 | 候选(候部署;缓解=restart节流纪律) |
| 2 | E8 类 | `max_consecutive_entries_per_symbol` 3→2 | pct 升档后单币堆叠上限变肥（07-22 06:43Z 登记）；16:16Z 无亏损侧稳定分化支撑 | 0/2 | 候选 |
| 18 | 全文=git b32ba54版 | | |
| 62 | 全文=git b32ba54版 | | |
| 21 | 全文=git b32ba54版 | | |
| 3 | E8 类 | per-symbol 重入 gate（代码级，需新锚点 S17） | churn 残留；引擎不执行重入字段（见已确认机制）。16:16Z 注：AKE（churn 代表币）24h +5.06/12 已转盈利，紧迫性降 | 0/2 | 候选 |
| 5 | 研究 | TP/SL/max_hold 与 15-60m 桶关系 | 桶已翻正，优先级降 | n/a | 低优 |
| 6 | E7 类 | 热点∩池内维度加权（择优批中 trending 币） | 热点∩池∩盈利连续8轮链+反例COTI/AKE热而亏³=榜首逆信号〔压缩@08-09 15:2x,全文见git e93a79e〕;§6计数行为准 | 0/2 | 观察（依据弱化第4轮） |
| 7 | 全文=git b32ba54版 | | |

| 11 | 全文=git 9d56e10版 | | |
| 47 | FLEET候选 | S27突破追动量复活版 | **已兑现@08-29=FLEET复活brk-v2(tpl973=S26内核+S27),本行让渡§1.5 brk行与_exp** | — | 结案→§1.5 |
| 55 | E7类(trend) | **EXP:trend prem降档候选——watch结案@08-21不改**:真凶=atr_discount非S24;全文git(已自证@08-21 12:18 COTI破荒)或EXP降门;费覆红线内只登记不动〔分带演化+ACE三条件全史git 044907d/92fb9c3〕 | 机制级(gate) | 0/2 | 候选(需求已减:破荒后活性恢复) |
| 65 | 全文=git e39ddee版 | | |
| 66 | 代码候选(v2原型正确性;批2) | S28-fade衰竭签名直入重写 | v2壳已退役@08-28,S28随葬;复活=新壳FLEET时重估;设计全文git | n/a | 冻结(宿主退役) |
| 58 | E9类(main扩仓) | E9扩仓rider(0.08→0.10)兑现@09-03 | 直令+rider全文config._exp史;门史git f3c6469 | n=12段(史) | **结案;E9裁决=ROLLBACK@09-03 21:2x(24h−9.03∧段n21/−9.42;pct回0.08);全文git 64ede74版行** |
| 52 | 全文=git 9d56e10版 | | |
| 53 | 引擎候修 | DeleteStrategy斩草除根(无条件Kill) | 修复已推fa93736@08-16候owner部署;翻案@08-18幽灵成交=0→降卫生级 | n=1 | 候部署(卫生级);全史git |
| 49 | 组共病(出场几何) | trailing(act1.0/cb1.2%)+BE(1.0)重构 | 72h n96基线赔率0.88;verdict全文config._exp+git 191b049 | n=30段 | **结案KEEP@08-16**=main基线几何 |
| 57 | 全文=git 848966c版 | | |

| H-0919a | 追踪变体未测: 反事实 +54 为无追踪纯括号; 候测 activation 2×ATR/callback 1×ATR 是否优于固定 TP 2.5×ATR(MFE 中位 1.65%≈2.6 ATR) | 09-19 | 门=段 n≥40 后用 cf3.py 重跑 |
| H-0919b | 空头偏爱紧刀: 网格 SL2×/TP2× 多 n136 +36.4 vs 空 n70 −22.0; 紧刀 0.4/1.5 空 −9.7 优于多 −33.9 → 空头可能需独立(更紧)括号; 小样本 | 09-19 | 门=空头 n≥60 再分方向网格 |
| H-0919c | 空头信号本窗口无正期望且机制未定位(6h 空 164 腿 −31.5 wr28%; 动量/BTC/括号反事实均无改善); 候选机制 a-d 与 06:3x 实证(病根桶=空 conf≥0.45 n9 −18.3; S31 OFF 偏置只解释笔数)全文=git d19a237版§4 | 09-19 05:5x | 门=组件级归因 n≥30 后再写门; owner 裁 sides/短侧溢价(CC-24②) |
| H-0919d | CC-4 高分多头延伸假说 两窗矛盾: 12h DB 联接 conf≥0.45 多 n27 −16.7 wr33% vs 24h 现货可核子集 conf≥0.45 n27 +19.6; 延伸多单 ext20>1×ATR n50 +15.75 wr58% 反是赢家(r60>1% n69 +8.4) ⇒ 不写 S34; 差异来源候选=已隔离 meme 币(无现货)集中在 12h 联接 | 09-19 05:5x | 门=隔离后新段 n≥30 同型再测; 未过门不写 |
## 5. 待落队列（已决定、仅被持仓/平台锁阻塞的动作；空仓窗按 Step 2.5 逐项落地）

| # | 类型 | 内容（含完整意图） | 登记轮 | 状态 |
|---|---|---|---|---|
| DS-2 | owner裁决(需重启) | 全文=git 6b44db7版§5(要点不变) | 09-17 | open |
| DS-3 | owner安装 | 全文=git 6b44db7版§5(要点不变) | 09-17 | open **@09-19 05:5x owner 两条 live 直令重申 /tmp/ai_task 双向; 事实 _ai_task_ds 始终 null=sync 从未成功; 已 TG 安装行: `*/10 * * * * cd <repo> && QT_USER=admin QT_PASS=*** python3 ops/ai_task_bridge.py sync`(脚本在 quanty-ledger 分支 ops/); DS 侧发言用 `post`** |
| DS-4 | owner保险 | 全文=git 6b44db7版§5(要点不变) | 09-17 | open |
| DS-6 | owner部署(现货) | 全文=git 6b44db7版§5(要点不变) | 09-17 | open |
| DS-7 | M候选(现货超时) | 全文=git 6b44db7版§5(要点不变) | 09-17 | open |
| DS-8 | owner确认 | 全文=git 6b44db7版§5(要点不变) | 09-17 | open |
| DS-9 | owner确认 | 全文=git 6b44db7版§5(要点不变) | 09-17 | open(**裁决@15:20Z: watch线破(open≥08:30 n27 毛−6.26U≤−4U)→DS-ROLLBACK be_atr 0→1.0已执行复读✓;仍候owner认领:若为owner手改→告知即恢复0**) |
| CC-1 | CC反事实全集 | 全文=git 6b44db7版§5(要点不变) | 09-18 | open |
| CC-2 | RECOVER门跟踪 | 全文=git 6b44db7版§5(要点不变) | 09-18 | open |
| CC-4 | 策略代码(S35候选) | 全文=git 6b44db7版§5(要点不变) | 09-18 06:2x | open **@09-19 05:5x 两窗矛盾(§4 H-0919d)→HOLD 不写门** |
| CC-11 | 巡检 | 全文=git 6b44db7版§5(要点不变) | 09-18 13:1x | open(读数史 git 2d9c837/29d8478 版; 最新读数见 §6 11:4x 行) |
| CC-15 | owner裁决 | p6 出场几何 D 方案(赢单侧放宽 trailing/BE);候 4 轮 | 09-18 15:2x | **closed@09-19 03:4x: 被 owner 第一波直令+反事实取代——trailing/BE 直接关闭(反事实 +54 为无追踪括号), 追踪变体入 §4 H-0919a** |
| CC-9 | DS越界簿 | admin mcp 未声明改动=协议7越界 #1~#5(09-16 22:10/09-17 13:15/09-18 06:30/09-18 15:22/09-18 21:22, 全文 git df539c9版)+#6 09-19 03:22 pct→0.0625; 裁决=不硬抢(RECOVER 门关/mcp6 不绑定/乒乓), owner 04:49 自行解锁 pct0.25(=A 路径), mcp 仍 6; owner 令恢复即恢复+TG | 09-18 08:1x | open(mcp 6→10 候 owner; breaker 阈值改钱包比例候 owner; 读数史 06:3x/07:3x/08:4x=git 2d9c837版; **@09:2x 07:08 后 DS 0 PATCH; 10:0x 节拍预警: 6h 净 −37≤−4U→宿主副本可能再砍 pct 0.125→0.0625, 届时只 TG 不硬抢(owner 13:4x"按 owner 值恢复"vs CC-9 不硬抢仍候一字令)**) **@10:3x #7: 09:37 breaker mcp 6→3 未声明 + 10:09 optimizer lev 10→5 裸改; 均不硬抢, watch_ds 登记(lev 劣化线 post 段 n≥10, eval 18:00Z); owner 一字令(恢复 lev10/mcp10 or 认可 DS 值)仍候** **@11:4x: mcp3 绑定实证(11:16/11:19/11:23/11:30 跳过 G/COTI/B2/ZAMA; majors XRP 计入 main 计数 manager.go:312); 仍不硬抢——理由=两壳共用 IP 配额, 5 仓已触 429→tpsl_setup_failed 裸奔窗; TG owner 三选一: 留 3 至 REST 预算补丁部署 / 恢复 10 / symbols 显式列表重启** |
| CC-19 | M候选(费率病根) | 全文=git 6b44db7版§5(要点不变) | 09-19 00:1x | open |
| CC-20 | 评判 | FIX max_atr_pct 6→0.8 | 09-19 00:2x | **closed FAIL@03:3x→ROLLBACK 6; 全文=git c75a1c3 版§5** |
| CC-21 | 评判 | p9 ATR 括号(03:41) | 09-19 03:4x | **closed@09-19 07:3x(段被 07:13 池切换封口; p9b n20 −32.95 饥饿 0.4% 刀 16/20; 括号未被公平检验); 全文=git c75a1c3 版§5** |
| CC-22 | M候选(金额帽精确实现) | 全文=git 6b44db7版§5(要点不变) | 09-19 03:4x | open |
| CC-23 | 管道(owner/M) | 全文=git 6b44db7版§5(要点不变) | 09-19 05:5x | open(候 owner; **@09:2x 429 复现 3 次: 08:56:49/09:10:49 positionRisk 守护跳过 + 09:13:34 查 TPSL 失败, main(owner=1)与 majors(owner=2)同秒=同账户配额, 3 仓即触; 61631b0 部署优先级↑**) **@11:4x 429 簇再现(11:08-11:25): FetchCandles history/ZAMA tpsl_setup_failed(裸奔 28s 自愈)/查仓失败→B/USDT 保证金≤0 跳过=直接丢单; 300 池重启预热+两壳=峰值; 61631b0 部署=第一优先** |
| CC-24 | owner 裁决包 | ①hunger_sl 0.04 取舍(**@11:06 owner 关 hunger_mode_enabled=false ⇒ ①事实结案, 刀退场**) ②空头 sides/scp ③mcp10 ④breaker 阈值改钱包比例 ⑤FORBIDDEN_EXACT 补键 ⑥宿主脚本同步 ⑦majors 08:01 start/+XRP 认领; 全文=git 29d8478 版§5 | 09-19 05:5x | open(①closed@11:06) |
| CC-25 | watch_ds(裸改) | **Majors 被启动**(08:01:47Z UI 无 audit, 5 币 mcp5): 1m 止损距=来回费(ETH 0.082/BNB 0.085/XRP 0.216% vs 0.10%), 首 3 单 −0.78 全 SL | 09-19 08:2x | **closed@08:39Z: CC 不停壳, 改为 apply S35 bar聚合 15m(tpl1056)→评判转 CC-28; 08:01 start/+XRP/mcp5 认领仍候 owner(CC-24⑨)** |
| CC-26 | 备用池机制 | _standby_cc 8 币; 每轮 POST /backtest(sandbox, 1m 18h, 现行 config+sim_clock+taker_fee 0.0005); 连续 2 窗费后>0∧≥5 笔→晋升; core 48h ≤−4U∧n≥4→降回; 前提 S34 验证✓(#56); 阻塞=回测器 #54/55/57 挂起>1h 需重跑 | 09-19 07:1x | open(**@09:2x 确定性探针 #59/#60(与 #56 同请求)=10/12/12 笔 −12.3/+3.6/+7.6 不可复现(§3 09:2x); #51/#53 0 笔=缺 max_price 已解释; 本轮未排 8 币回测(单次符号无意义); 临时门候 owner=同窗 3 次符号一致∧中位>0; 根修=CC-29**) |
| CC-28 | 评判(在飞) | **EXP S35 bar聚合 15m on majors**(tpl1056 @09-19 08:39:38Z; 预注册在 majors 壳 _exp_cc; metric=段 n/费后均净/wr/tpsl 日志 SL 距中位/笔日/首单时刻; expect n≥15 费后均净≥0∧SL 距中位≥0.3%∧wr≥45%; worsen 段净≤−4U ∨ n≥10∧wr<35% ∨ SL 距中位<0.2%→ROLLBACK(rollback→998); 24h 零信号无异常→只报) | 09-19 08:4x | open(**@09:2x 平仓 n0; 在飞 SOL 08:46 多/BNB 09:01 多, SL 距 0.88/0.67% 中位 0.77%≥0.3 ✓聚合生效; worsen 未触; 滑点 watch=§4 H-0919e**; eval 09-20 08:40Z 或 n≥15) **@10:3x 平仓仍 0; 在飞 3(+BTC 10:16 SL 距 0.33%); SL 距中位 0.67%≥0.3 ✓; worsen 未触; admin 10:03/10:06 atr 改回净零未重启=段未污染** **@11:4x: S35 段 SOL/BNB/BTC 多 11:08 被 owner 重启 'manual' 平仓 −0.75(n3, 未经 TP/SL 检验, 不计 worsen); XRP 多 11:16 腿✓ SL 距 0.84%≥0.3% ✓; mcp 5→2(owner); 继续至 09-20 08:40Z/n≥15** |
| CC-29 | M候选(回测确定性) | 根因 strategy_backtest.go:426 开环 10ms 喂送+:473-479 非阻塞 select ⇒ 信号错位; 修复=lockstep ack; 全文=git 29d8478 版§5 | 09-19 09:3x | open |
| CC-30 | owner 认领+裁决 | **11:06Z admin 批(main hunger off + symbols ""→300 池 + majors mcp2 + 双壳重启)**: 请 owner 认领(若非 owner=未知 admin 写路径🆘); 若有意回 300 池: 建议 symbols 写显式 265 币列表(重启键)使 isAllowedSymbol 不再把 majors 仓计入 main 槽位; 若无意: 回 7 币 symbols 列表+重启(CC 可执行, 空仓窗) | 09-19 11:4x | open |
| CC-27 | DS 脚本论证 | 仓库 087a6dd@05:14Z 入库 ops/qt_breaker.py(6h/−4U/PCT_FLOOR 0.25/冷却 6h/锁 24h)+deepseek_optimize.py(**第 246 行仍执行已废 #8 Σ(pct×mcp)≤0.75**→0.25×6 必被砍到 0.125; 07:08 砍 pct 无锁且距上次 3h46m<6h=宿主副本≠仓库); 请 owner ①同步宿主脚本 ②删 246 行约束 ③阈值改钱包比例 | 09-19 08:2x | open |
> 结案集@09-19 03:4x: CC-20(FAIL→回滚 6)/CC-15(被第一波直令取代) 终态在行内; 结案集@09-19 00:2x: CC-12(owner atr 2.0/1.5 经 CC force 重启 00:25:26Z 生效)/CC-13(p6 封段 17:56Z n71 −34.75)/CC-17(p7 裁决见 §6)/CC-18(ROUTE 7 币落地 20:2x) 终态全文=git 5096ca1版§5;更早结案集=git 2fcc0da版§5。
> 维护注指针集: #87/#86=git 805a115;#84=git dd4326e;#83=git 09-05 06:2x版;#82=09-04 07:3x版;#81/#78=git 4be520c;#74=2aa13b1;#70=17251a9;#68=0940c62。
> 维护注(落地结案@09-14 14:52Z): #88全绿(bl22落地+feed卫生remove15)全文=git 6c1bd6a版§5。

## 6. 假设库·观察计数（v2 迁移注记 @08-01 16:40Z：并入假设库，与 §4 合称；跨轮累计；9 秒日志窗单次未观测 ≠ 零，以本节跨轮增量为准）

| 计数项 | 读数 | 更新轮 | 备注 |
| **09-19 11:4x 读数(DB 去重; income 括号)** | 1h n7 −0.85 / 3h n11 +4.48(income +3.78) / 6h n37 −13.17 wr24%(income −11.9: 毛 −4.8 费 −7.1) 多 −1.65 空 −11.52 / 12h n111 −37.2 空 −41.85 / 24h n258 −65.95 wr41%(income −101; 毛 −44 费 −57) 多 −37.8 空 −28.1; 24h 死法: <2m 亏 n53 −100.9, 2-5m 亏 n47 −81.6, 5-15m 赢 n40 +93.6, 15-44m 赢 n13 +22.4, 超时 n4 +1.1 ⇒ 病根=hold<5m 刀(饥饿 0.04, 11:06 已关); **7 币段终读(07:21→11:08) n12 毛 +17.34 wr50% 多 11/12: 赢 6/6 hold≥6m +20.25, 亏 6/6 hold<6m −2.90**; 300 池段(≥11:08) n1 HEI 空 +1.49(交易所 TP 10.1m, 无引擎日志); 成交 conf: 24h n248 min0.36 med0.44(有效门槛 0.35=config✓), 11:08 后 n3 0.44-0.50; 48h 币级规则命中新增 PROM n4 −11.34(已隔离), M n3 −9.05(n<4 候) | 09-19 11:4x | 10:3x 读数全文=git 29d8478 版§6; 下轮: 300 池段 n≥15 复读 hold<5m 亏单占比(刀关后应<30%) |
| **09-19 07:3x 饥饿 0.4% 刀死法计数(DB 24h n260)** | 刀 107/260 毛 −215.9 vs 赢单 115/+186.5, 超时 8/−6.0, TP 5/+25.6; 3h 输单中位价距 −0.53%(过冲 0.13pp)/hold 四分位 1.9/4.4/9.2m; 1h wr0%(n12) 3h wr15%(n27); 24h 多 −74.0/空 −35.1(income) | 09-19 07:3x | 行动=候 CC-24①(owner 值不自改); 新池段复读线: n≥8∧刀占比≥60%∧段净≤−4U→TG 再荐 B(hunger_sl 0.30) |
| **09-19 06:3x 空头置信度反向(03:41 后)** | 空 conf<0.45 n10 +1.10 vs ≥0.45 n9 −18.30; 全文=git 29d8478 版§6 | 09-19 06:3x | 行动=不抬 scp; 门=n≥30 再议 |
| 本地 TP/SL 监控陈旧价假触发 | n=2(ROBO 09-19 05:58:38 429 挡下无实害; G 09-19 08:39:39 饥饿触发 roi −4.30% 实 −1.8%=触发价偏差 0.25%, 亚型见§3 09:2x) | 09-19 09:2x | 补丁 61631b0 候部署; 再现 1 例(成功平仓∧hold<60s∧pnl≈0)→催部署 |
| 池宽度离线读数(1m EMA20/60, data-api.binance.vision 现货可核子集) | **0.31**(48/157 up; 272 池中 115 币无现货=样本偏向主流币)@09-19 05:4x | 09-19 05:5x | 引擎 S31/S13 读数不可见(统计汇总行自 09-16 12:24 缺失而 stats_log_interval_sec=60 在位→日志缺口候查);用途=校准 breadth_max_for_short/S31 阈值;判据: 连续 2 轮 ≥0.70 而空头仍开=S13 门失效→查源 |
| backend→Binance REST DNS断连 | episode结案@09-14 14:4xZ(118.6h零成交;owner docker重启修复);全文=git 6b41621版§6 | 09-14 14:5x | 复发判据=fapi SERVFAIL再现→重开计episode n+1;#89(c49c555)根修推荐不变 |
|---|---|---|---|
| **09-18 P3 lev10 段终裁=KEEP 4/4(RECOVER)** | 全文=git e39ddee版 | | |
| **09-18 p5/p6/p7 终读** | p5 KEEP 3/3; p6 封段 n71 −34.75; p7(hunger_sl 0.04) 终读 n46 179笔/日 毛+4.26 wr52% 饥饿SL 带 41% 费后 −0.23/笔 🔴 多35/−2.61 空11/+6.87; 全文=git c06ab72版§6 | 09-19 00:1x | |
| **09-19 ATR×固定价格刀 交互(四段)** | 全文=git c06ab72/6b44db7 版§6(机制=固定刀<0.5×ATR=噪声穿刺; 四段 ATR>0.8% 子集均劣) | 09-19 00:1x | 行动史=max_atr_pct 0.8(CC-20 FAIL→回滚 6) |
| **09-19 05:5x 空头入场前动量 vs 结果(24h 现货可核 n45)** | 全文=git 6b44db7 版§6(要点: 赢/输入场前 r1..r30 中位无差, veto 扫描无效; 多头 ext20>1×ATR 反是赢家) | 09-19 05:5x | 行动=不写动量门; 复测门=隔离后新段 n≥30 |
| **09-19 03:5x 止损×止盈×45m 反事实网格(n206, 费后 0.1%, 1m 永续代理)** | SL2×/TP2.5× **+54.0**(wr49% sl87/tp94/time25) ‖ 2×/3× +37 ‖ 2.5×/2.5× +19.6 ‖ 固定% 最好 1.0/3.0 +11.7 ‖ 现刀 0.4/1.5 −43.6 ‖ 3.0/1.5 −73.9 ‖ 金额帽 3/4/5U −59.6/−66.3/−39.8 ‖ 实际 −54; 多 n136 +36.4 vs 空 n70 −22.0; 全表 git 0e0195f版§6 | 09-19 03:5x | 行动=p9(CC-21); +54 为上界(无滑点), 排序稳健 |
| main断流观测(三闸交集关门) | main笔数0/24h@09-10 21:1x=断连首全零轮(30.2h零成交;史2@12:2x/16@04:1x;微差双胞纪律留档git 2596a9a) | 09-08 18:1x | 判据不变:再现6h+零笔且池ATR中位≥0.5→查管道;每轮记main笔数 |
| 已结案·终态归档集(18项瘦身@08-26 12:4x) | 18项终态读数与重开条件全文=git 8705b00版§6;触发即复活行 | | |
| 终态归档集2(4项瘦身@09-17) | 全文=git f90d9dd版§6(含粉尘残量亚型 n=1@09-17 AVA);判据不变,触发即复活行 | | |
| 终态归档集3(8项瘦身@09-19 08:4x) | 重启后速开仓/closed行消失(≤48h)/main行零归属/收养错归属(#57族)/rotate has_open_position/apply重启作用域/无端重启重播种/#79 sid旧绑定 全文=git 6b44db7 版§6; 判据不变, 触发即复活行(重启后速开仓判据已达=候议 warmup 静默 5~10m) | 09-19 08:4x ‖ 再折 6 项@08:5x: TradFi嫌疑币watch / 幻影行post-fix观察 / epsilon边界放行观测(v2域) / 硬超时磨损类 / BICO资金费磁铁长侧 / funding磁铁·他币再现watch(TradFi 规则"任一 -4411 即隔离"存续; 全文=git 2ce79f1 版§6) | 09-19 08:5x | |
| S31·regime动态方向偏置(结案KEEP@09-02) | 上线tpl988@09-01;KEEP;verdict=ops/exp_archive/s31_verdict_20260902.json;全文git e9d1b17版§6 | | |

## 7. 运行日志（每轮一行，新行追加在表首）
| 2026-09-19 11:1x-11:4x | 定时轮: 管道活(REST 200; 429 簇 11:08-11:25: FetchCandles/ tpsl_setup_failed ZAMA→28s 后自愈补设/ 查仓失败→B 保证金≤0 跳过); 钱包≈167(avail 123.7+3 仓保证金≈42+浮盈 +1.6); income 1h −0.85 / 3h +3.78 / 6h 净 −11.9(毛 −4.8 费 −7.1, ≈−7%) / 12h −43.3 / 24h 净 −101(毛 −44.2 费 −56.7; 入金 +142 另列)🆘; DB 去重 24h n258 −66.0 wr41%, <5m 亏 n100 −182.5=病根(饥饿刀); **owner 11:06Z 批(admin, 无 _exp; symbols 在 DS FORBIDDEN_EXACT ⇒ owner 归属候认): main hunger_mode_enabled=false + symbols ""(→filter 路径 300 池, strategy_start.go:240) + majors mcp 5→2 + 双壳 11:08 重启(main pid 58512→58521 单活)**; 7 币段终读(07:21→11:08) n12 毛 +17.34 wr50%: 亏 6/6 hold<6m 刀, 赢 6/6 ≥6m; 300 池段 n1 HEI 空 +1.49; DS: 10:30 后 0 PATCH, _exp lock-1789810634(pct 冻至 09-20 09:37), _ai_task_ds null; mcp3 绑定(11:16-11:30 跳过 4 次)且 majors XRP 计入 main 计数(manager.go:312)→不硬抢 TG 三选一; 止损完整性: main HEI 11:15 腿✓/majors XRP 11:16 腿✓(SL 距 0.84%), sl_ratio0.03 在位, max_hold 45/720; **改动: ROUTE bl42→45(+SOL/XRP 跨实例隔离+PROM 48h n4 −11.34) + rotate remove 35(feed 300→265, 隔离∩feed=∅, main∩majors=∅)**; _exp_cc 换为 OBS 300 池段(eval 18:00Z/n≥30; 旧文 ops/exp_archive/exp_cc_20260919T1030Z.json); 留言板 11:4x(7 条 7.7KB); 宏观 FGI71 BTC +4.2% ETH +5.5% XRP +8.0% 市值 +0.9% trending∩feed=ZAMA(在飞多); 下轮=12:15 定时, DS 13:09 节拍对账 |
| 2026-09-19 10:1x-10:3x / 09:1x-09:3x / 08:1x-08:5x / 08:2x(检查轮) / 07:1x-07:3x / 06:1x-06:4x / 05:3x-05:5x / 05:1x-05:5x / 03:3x-03:5x / 00:1x-00:4x | 全文=git 29d8478(10:1x-10:3x 行)/c75a1c3(09:1x-09:3x 行)/2d9c837(08:1x-08:5x/08:2x/07:1x-07:3x 三行)/6b44db7 版§7(逐行 git d19a237/4c56565/4c56565/f48d043/c06ab72) |
> 瘦身注(09-19 05:5x 归并): 2026-09-18 17:5x-18:1x/15:1x-15:3x=git 2fcc0da版; 14:1x-14:3x/13:5x-14:0x=git 58e5e5c版; 13:2x-13:5x/13:1x-13:3x/13:0x-13:2x=git fa4cd1d版。
> 瘦身注(09-18 归并): 2026-09-18 12:5x-13:0x; 2026-09-18 10:2x-10:3x; 2026-09-18 12:4x-12:5x; 2026-09-18 10:1x-10:3x; 2026-09-18 08:0x-08:2x 全文=git e7f1c65版§7(08:0x 行=git 5d2c77a)。
> 瘦身注指针集: 0x归并): 断连#35(09:1x)全文=git 0f076c6版行。; 断连#34+probe(09-14 09:5x-11:5x)=git 9137f71;#33+probe(06:5x-08:5x)=19130f2;#32+probe(03:5x-05:5x)=e9b12ca。; 1x归并): 断连#31(21:1x)全文+#32后probe#1-3注记(00:5x/01:5x/02:5x)=git 3f6d7a6版行。
<!-- §7瘦身史指针集v2=git e9b12ca版§7注原文(09-14 00:1x及更早全部归并注逐hash在内,含前v1集8729d22) -->
