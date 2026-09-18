# QuantyTrade 改动台账（LEDGER）

> 权威分支：`quanty-ledger`（2026-07-22 12:19Z 由种子 `claude/jolly-bardeen-sz6d04` 引导上线）。
> 由 cron 运行维护，每轮必写。**瘦身协议 @08-01 17:35Z（用户直令：省 token）**：本文件保持精简工作集，全量历史永在 git（压缩前快照=29eb4c5）。纪律：§7 只保最近 10 行（新行≤800字符）；§6 每计数项只保最新读数；§5 只保 open 项+最近 2 条维护注；关闭候选/已落待落项直接删行；超长叙述以〔压缩〕标记截断。人工编辑请只增不删原则对 cron 瘦身豁免。
> 由 cron 运行维护，每轮必写；人工编辑请只增不删。协议：v12 七节制（2026-07-22 16:16Z 起升级；此前见 `docs/cron_prompt_v11_addendum.md` Step 7）。
> 策略：8eb182b6-ee74-4125-a602-f0a91f376432（tpl447 @2026-07-22）

## 1. 用户常备直令（权威登记处；prompt 内快照与此冲突时以本节为准）

- **prompt v3.1 写入@08-15**: 11处手术对账;脱敏档ops/prompts/prompt_v3.1_20260815_redacted.txt;已被v3.2/v4/v5.1取代;全文git 7ee5ca0版§1。

- **多仓位=足额单仓直令(2026-08-15 15:5x)**: owner原话要义——多仓位不是小仓,每仓按需足额(vol由引擎判),余额不足依次递减,无余额跳过等下次评估。**禁以缩pct换仓位数**(否决cron提出的0.08→0.05广度方案)。机制对应=conf_sizing(mult0.6-1.4按置信度)×percent_balance(逐仓自余额递减)×min_notional$21跳过——三段已实证在位,零改动;广度增长路径=信号门逐档(conf/长门)+池宽+载具数,仍按费覆红线门控。

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

- **08-03 17:0xZ(交互)杠杆上限直令**: 硬边界 leverage [2,5]→**[2,20]**(用户直令;prompt 硬边界#2 待用户同步改行,改行前 cron 仍按 prompt 现值执行)。基线2不变;杠杆阶梯 2→3→5→8→12→20 逐档,每档门=当档≥30笔正净额+费用覆盖+预注册。**物理护栏(任何档强制)**: 清算距离≥1.5×SL距离 ⇒ lev ≤ 100/(3.75×池内max ATR%) ⇒ 升杠杆档必须同步收紧 max_atr_pct_for_trade(例: lev20→ATR%≤1.33/lev8→≤3.3/lev5→≤5.3);违反即该档不可用。风险知情: 20x 清算距≈5%,与 2.5×ATR 止损带重叠,高杠杆档=低波动币专用,用户已获物理冲突说明。

- **08-05 17:1xZ(交互)币池直令**: select_limit 50→**100** owner UI改,禁回滚;select_limit仅启动时读(strategy_start.go:230);#22系已结案;全文git 766c7d9。

- **08-05 17:4xZ(交互)三解锁直令**: sides双向+cd1800→900+mcp3→10当轮落地(S17 gate移除tpl534);取代08-03并发pacing;风险呈报存档;全文git 7ee5ca0版§1。

- **08-05 17:4xZ(交互)代码入库+模板保留3版直令**: 策略代码不再只存DB——每次apply后上线代码推git仓库(confident-fermi分支 strategies/ 快照目录,git=全史档案);DB StrategyTemplate 只保留最新3版(=当前+2步rollback深度;retention patch M通道候部署)。owner索要此流程prompt块已交付(见08-05交互记录)。

- **08-06 02:4xZ(交互)冷却处置直令**: cd→180预授权(owner"按照你说的来";0越界不采,硬边界#2下限);已执行cd180全组现值;全文git 766c7d9。

- **08-06 09:2xZ(交互)二次解锁直令**: owner UI mcp200+re3禁回滚(已被09-14 mcp10取代);全文git 7ee5ca0版§1。


- **08-09 策略组+WS实时管理直令(交互)**: 已被09-14单策略直令取代(5载具/S24-S26/route_pools对账器/金字塔只赢家/owner拍板fade启·lowvol删);全文git 7ee5ca0版§1。

- **08-11停机/08-12复飞三直令**: 全组停机(后端522窗)→单载具canary→全组复飞;全部被09-14单策略直令取代;全文git 766c7d9/cd4b082。



- **08-22 04:1x(交互)低波解锁优化直令**: owner"现在开始优化"→双载具三值包(trend/v2低波解锁);后续=trend包#69回滚@08-25、v2退役@08-28,低波解锁线关闭;全文git 766c7d9+a2e845f。

- **08-26 17:07Z(交互)提频直令**: "增加频率"→main scp0.05→0.049收0.600档+ROUTE#52;后被08-28终裁rollback;全文git 7ee5ca0版§1。

| 2026-08-29 | **提频直令(live会话)**: "不是不是,现在需要提升频率了"——现场覆盖08-03费覆红线"未覆盖=只修不扩"锁;红线降级为评判/回滚指标(非前置门)。同轮追问确认方向="不影响准确性的提频:改策略+按行情动态判多空"。执行=当轮2原子包(EXP多头闸重开+FLEET breakout复活);**追令@16:5x(live)"搞一个动态的"+"给我一个prompt我来更新"**=①动态化立为常备使命(关键门槛从静态数字→行情状态函数,S31为首件)②费覆红线降级入prompt硬边界#1改行=owner后示落定③prompt v3.2当轮交付owner自更(脱敏档ops/prompts/prompt_v3.2_20260829_redacted.txt;12处手术清单见§7行) |
| 2026-09-03 | **扩仓+提频直令(live,两连)**: ①"增加一下开仓数量。目前盈利太少，亏损单太多。这不合理。"→E9 rider(pct0.08→0.10;#58门被覆盖;回滚=24h组净≤−8U或费覆连🔴48h∧段净<0→0.08) ②"增加开仓数量和频率"→FREQ rider(select_limit100→150,feed89→138)+trend #65(lcp0.149);拒拧: 攒批窗(落选≈0)/cd<180(硬边界)/阈值下调(wr63<be68=垃圾档) |
| 2026-09-14 | **单策略回归+进攻直令(live,断连恢复后)**: owner原话要义"切回单策略/同时开10单/进攻是最好的武器,必须要交易/代码优化直接上线";教练对质: 零交易119h=DNS断连非策略;执行=FLEET stop trend+池并轨/mcp10+pct0.075/进攻一档lcp0.10带劣化线/prompt v4交付/部署与ssh仍属owner(硬边界#6);全文git 7f578a7版§1 |
| 2026-09-17 | **DeepSeek 双执行体协同直令(live,07:3x-08:0x)**: owner三连"我增加了一个 deepseek 的定时模型优化 你们配合着来…给我一个最新的 prompt"/"我切换了模型 重新分析"/"/tmp/ai_task 只保留最近几天"。取证: DS=宿主cron ops/deepseek_optimize.py+qt_breaker.py(admin#1只动config);Go侧autotune无关(enabled=false);_exp归DS。交付prompt v5.0(协同协议9条)+ops/ai_task_bridge.py;容器不可达/tmp→经config键桥接;全文git 7f578a7版§1 |
| 2026-09-17 | **现货载具直令(live会话,08:2x)**: owner原话"增加现货策略。下单，写到 promt 中。"。源码取证与规格全文=prompt v5.1【现货载具】节/git b341694版§1(market进程级→第二后端进程+独立DB/Redis;出场只有本地TP/SL;S34改amount=名义/现价;超时缺口M候选dev-spot-maxhold;劣化线段净≤−3U∨n≥10∧wr<35%→stop;现货刹车6h≥5%)。状态=候部署(owner部署+划转后由cron建壳canary)。 |

| 2026-09-18 | **不下单不是解决方案直令(live 06:1x)**: owner原话"策略不对 你现在遇到问题不去解决 而是不下单了，要解决，肯定是策略问题啊，不要不下单。要积极适应市场改变策略"+"这个加到 promt 中"。教练对质(证据): 刹车链 lev10→2(07:52)→禁多(15:20)→mcp2(DS 13:15) 使笔/日 256→112→26→20,12h 净+0.05=靠不交易止血;48h 亏损全在 lev10×ROI口径sl0.12(=1.2%价噪声带) SL/早亏115笔−136.6U;lev2 同键=6%不可达→死法转饥饿/超时。执行(Go热 v34/v35 复读✓): P1 sides→[buy,sell]+mcp 2→10(纠回owner令10) ‖ P2 tp/sl pct→0=引擎改用信号ATR口径(2.5×ATR SL/4.5×ATR TP,与杠杆解耦;resolveTPSLFromROI pct<=0 原样返回已核) ‖ 预防性钉回 min_conf0.35/cd180(=进程现值,DS写的0.45/1800从未生效)。新常备规则: 刹车＝换策略杠杆(出场几何→杠杆档→入场规则→币池)不＝停交易;频率地板=基线50%(19/日)任何状态适用;禁方向/缩mcp/cd拉满/抬min_conf不是刹车杠杆;缩表唯一例外=6h≥20%或avail<1U且同轮带策略改动、下轮复飞。交付 prompt v5.2(ops/prompts/prompt_v5.2_20260918.md 脱敏+changelog v5.2 节),安装权owner。 |

| 2026-09-18 | **杠杆 10x 直令(live 08:0x)**: owner原话"你这下单数量和杠杆太谨慎了。增大 10x"。执行(Go热 v37 复读✓): leverage 2→10 ‖ mcp 4→10(admin 06:30:06 裸改 10→4=协议7越界,纠回) ‖ hunger_stop_loss_pct 0.025→0.125 + hunger_take_profit_pct 0.08→0.40(ROI口径键随lev×5换算,价格距离不变1.25%/4%) ‖ conf_sizing_max_mult 1.4→1.0(CC-5预注册:首次升档启用)。名义 21U地板→105×0.05×10×[0.6,1.0]=32~53U(5×);单笔风险=名义×2.5×ATR%≈0.6~2.6U;出场ATR口径不随杠杆变(P2)。order_amount_pct 尊重DS锁至12:37Z→到期升0.075(顶格0.075×10)。物理护栏: lev10需max_atr_pct≤2.67(Python侧→重启窗,现6;全仓假设下清算为账户级)。防御线保留: 24h≥20%→lev回2(降敞口不问,TG);劣化反应=换策略杠杆不降频。教练对质: ROI口径键(hunger_*)与旧SL同病,升档必须同步换算,否则45m饥饿刀在10x下=0.25%价格。 |

| 2026-09-18 | **单笔上限直令(live 10:2x)**: owner原话"是不是用百分比比较好一些，如果百分比超过最大500u那就取500u下单数量"。答复: 下单本就是 percent_balance(保证金=avail×pct×mult 夹[0.05,0.75],名义=保证金×lev,地板21U);lev2 期全单21U是地板效应非固定额。owner 10:27:59Z 以 admin 自落 max_initial_margin_usdt=50(=单笔保证金上限50U⇒名义上限50×lev: lev10=500U/lev2=100U;strategy_execution.go:127),CC 10:28 同值 PATCH 撞车仅落 _exp_cc.p4_cap 注记;上限在 avail>1000U(pct0.05)/667U(0.075)才生效=护栏。要点: pct0.05 时引擎 pct 地板夹紧使 conf mult(0.6~1.0)无效(0.78×0.05=0.039→夹回0.05),全单=5%保证金×10=56U;mult 要起作用需 pct≥0.083>硬边界#8 顶格0.075(mcp10)→取舍交 owner。 |

## 1.5 策略组注册表（08-09 起;池归属与载具状态权威节;roster 有变当轮必更）

| 载具 | id | 原型 | 池 | 状态 | 门/备注 |
|---|---|---|---|---|---|

> 维护注 08-29 17:4xZ：#76 **落地结案删行**——17:36自查点retry#2逢全账户空仓窗:brk stop[poll1即stopped无幻影卡]-PATCH六键+_exp尾注FIX伴随-start✓running✓复读mode=percent_balance/cs_en=true/floor21/0.6-1.4-0.55全落库;→rotate add BAS,NIL(斜杠)brk feed4→6✓;main remove BTR残留✓专家残留0互斥恢复;分界前brk段n8/−0.56全5U。
| main 通才(平台名 Meme_合约信号计算引擎_1) | 8eb182b6 | 通才(S4..S23+S30+S31+S33;S32关guard) | auto·feed184@09-18 10:2x(隔离∩feed=∅) | running | **09-18 12:4xZ RECOVER 顶格(owner直令): sides=[buy,sell] mcp10 lev10 pct0.075(=0.75顶格,v42) tp/sl pct=0(ATR口径;Go clamp SL≤3%价) hunger45m 40%/12.5%ROI cs_mult[0.6,1.0] max_hold240(DS watch) max_initial_margin_usdt50(DS裸改,无效果watch) trailing1.0/1.2% BE1.0 cd180/min_conf0.35 sl_ratio0.03/tp_ratio0.06 bl24 feed184; leverage留frozen_keys(=DS勿动)** _exp_cc=cc8 pct0.075 in-flight(eval 18:45Z或n≥30;劣化线段净≤−6U或6h≤−8%→0.05) carry p3_lev10 KEEP 4/4@12:4x, prev P1/P2 KEEP 3/3@10:2x; 刹车链史=git e94a8b0版行; 10:2x态=git 56a5311版行 |
| qt-spot-long(现货) | 未建 | main fork+S34(spot) | auto select_limit100 现货USDT对;bl=隔离区19 | **候部署@09-17(owner:第二进程quanty-spot BINANCE_MARKET=spot+独立DB/Redis+划转;cron:建壳→S34→烟雾→canary)** | 规格=prompt v5.1【现货载具】节(buy-only/spot_notional_usdt12/mcp3/cd300/mc0.60/atr_tp3.0/atr_sl1.5/hunger off/use_exchange_tpsl off);起跑门=spot自检✓∧现货钱包≥30U∧(main双转正∨owner令);劣化线段净≤−3U∨n≥10∧wr<35%→stop;现货刹车6h≥5%→stop;超时缺口=M候选dev-spot-maxhold(quick_trade_monitor.go:41去usdm门)或cron巡检持仓龄≥180m;DeepSeek未接入 |
| ~~退役壳×4~~ trend 827ffe8c/breakout-v2 3b646bf4/fade-v2 7583727a/fade 21519f1b | — | — | — | **已从平台删除**(09-15~16 owner/迁移;/api/strategies仅main 1行@09-17 07:3x实证) | 谱系tpl/verdict/遗仓收养全史=git 7ee5ca0版§1.5;复活=新壳FLEET预注册+owner令;qt-breakout-follow 2111f5f9 owner删@08-15同上 |

- 隔离区【24】=规则类(≤−4U∧n≥4)【17】: 4/CYS/TST/龙虾/BMT/BTW/H/APR/AIO/BICO/BEAT/XNY/ACE(血统git bb3b883/8a797ec/bb2e系)＋BR/AVA/ONE@09-18 05:19Z(48h BR n14/−14.89·AVA n6/−9.54·ONE n5/−4.49;全文git 5d2c77a)＋**哈基米@09-18 10:1xZ(48h n8/−4.01;附证 09:18 -4028 "Leverage 10 is not valid"→引擎按现有杠杆继续下单=该币杠杆不可控;bl+rotate remove同轮复读✓)**＋TradFi类【7】@09-10/09-17: GPRO/KODEX200/SOXS/CSOPSAMSUNG2L/CSOPSKHYNIX2L/HK0992/FLNC(-4411直证;签约闸+crypto模板未验证资产类;解禁须owner签约∧专项原型预注册;全文git 5d2c77a)。**池列值=快照,权威=每轮对账器输出**;对账器v1.6=quanty-ledger分支ops/route_pools.py(谱系与污染事故见§3);**arg4隔离表必须X/USDT形态**;出池只认头注规则或隔离;币级watch集/对账史/#51-#58史=git 5d2c77a版行。
- 保证金: Σ_running=main **0.075×10=0.75 顶格**@09-18 12:4x(cc8;回滚线→0.05×10=0.50;史0.15×10=1.5越界87min@09-16 admin)
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
| S24 | 2026-08-09 | live@trend fork | **trend定制(tpl575)**: 空头出口封闭(elif False档案化)+续势加分ls+0.10当3bar累涨∈[0.2,1.0]×ATR%(中继漏斗;0.70锚不动);S19/S20在场不可达 | `S24 ` |
| S25 | 2026-08-09 | live@fade fork | **fade定制(tpl576)**: 多头出口封闭(if False档案化)+RSI>75衰竭第二档ss+0.10(与S19/S20正交);S18在场不可达;0.60锚不动 | `S25 ` |
| S26 | 2026-08-09 | live@breakout fork | **breakout新内核(tpl577)**: score_breakout_detail整体替换均值回归核;30bar收盘破位base0.40+量能0.15+3bar动量0.15+EMA对齐0.15+余量0.10,blowoff帽1.2×ATR;趋稳/震荡/RSI极端/追高四gate False-guard档案化;detail键契约兼容 | `S26` |
| S28 | 2026-08-23 | live@fade-v2 fork | **衰竭签名直入(gate2旁路;#66修复;tpl823)**: short∧RSI>75∧价≥布林上轨∧末完成bar涨<0.3×ATR%时仅旁路"EMA非down+趋稳"一门;S19 3bar条款/S20/RSI<32/量能地板/ATR带/追空ext全保留;失速要件0.3×严于S19首条款0.6×=不接飞刀;评分门0.60另需≥1项独立空头确认(MACD死叉/资金费/多空比);附随#65 epsilon永久化(ss≥short_thr−1e-9);评判随v2 _exp二元 | `S28 ` |
| S29 | 2026-08-24 | live@fade-v2 fork | **S28签名域ATR地板降档(gate1;tpl887)**: 完整签名bar(short∧RSI>75∧价≥上轨∧末bar涨<0.3×ATR%)的gate1地板0.5→0.30,其余通道地板不动;依据=08-24 08:44-47Z MELANIA放量冲顶ss0.80/RSI79.8-88.4全灭于ATR%过低(0.30-0.35<0.5)+census 48h ATR%过低=ss≥0.60第一杀手378/791;机制=ATR回看滞后垂直拉升,gate1在S28旁路(gate2)之前=签名域机制性不可达;非低波解锁(HANA/COLLECT 0.11-0.26仍拒;tpl806 n16−1.00证伪之路不重走);内联不接config;评判并入v2 _exp二元 | `S29 ` |

| S27 | 2026-08-29 | live@breakout-v2 fork(tpl973) | **#47复活标记锚**:S26内核零逻辑改动复活于新壳3b646bf4;依据=旧壳FAIL已翻案(幽灵行伪证)+want.breakout档4币48h毛≈−8.05池错配 | `S27 ` |
| S30 | 2026-08-29 | live@main(tpl972) | **资费磁铁疫苗**:多头funding bonus分段(温和(−0.003,−0.0003)+0.20/深(−0.008,−0.003]+0.10/极深≤−0.008→−0.10),score_confidence+detail两位点同步;空头侧不动;内联不接config;伴随lcp0.35→0.15开多头闸(EXP评判);校准集=BICO8+ACE4+ONG3≈15笔−11U | `S30 ` |

| S31 | 2026-09-01 | live@main(tpl988) | **regime动态方向偏置**:池内EMA-up宽度三档(<0.35 OFF:lcp+0.10/0.35-0.60中性/>0.60 ON:lcp−0.05,scp+0.05),有效premium下限0,阈值内联,S31_ENABLED False-guard;live路径逐bar上报宽度store(新鲜窗30m,样本<8→NEUTRAL,历史重放/backtest不喂);detail新增s31五键+threshold上报改真实gate | `S31 ` |
| S32 | 2026-09-03 | **rolled-back@09-03 18:2x(tpl990 guard=False在场,复活须新预注册)** | **接刀冷却(不稳定窗veto记忆)**:近10m内(S18闪跌veto开火 或 RSI>70读数)∧上一完成bar仍跌(A-bar,S23同口径)→拒多;W-bar反弹确认放行;域=#80升门4死(veto重置型SKR0902-0343/ZKC0902-1414+冲顶RSI冷却型FF0901-1549;SKR0902-0007=W-bar域外不宣称)反证2赢(BTR0902-1224/SKR0902-0232均W-bar)全正确分类;佐证S23cohort A 1胜6负vs W 4胜3负;阈值内联(600s/RSI70),S32_ENABLED False-guard;重启清记忆同S14/S31株 | `S32 ` |
| S33 | 2026-09-08 | live@main(tpl998) | **资费疫苗下修(S30参数段下修)**:温和+0.20→+0.10,深+0.10→+0.05,极深−0.10不动;score+detail两位点同步,detail标签换S33前缀(决胜扫描口径随更);触发=§6预注册门(多头决胜cohort n10/净−5.82,owner无否决);阈值内联;回滚=一步rollback回tpl990 | `S33 ` |

下一个新锚点编号：**S34**(S23-S33已用)。⚠️锚点自S24起分fork谱系: main=S4..S23+S30+S31+S33(S32关guard在场);trend=+S24;fade-v2=+S25+S28;breakout=+S26(其均值回归核档案化)。apply前grep按该载具fork谱系核验。原注记:(历史上 S10 曾存在于注释引述，编号不复用)。
静态必留 6 符号（v10 既有，与上表取并集）：`on_market_message` `_emit_signal` `_append_bar` `self.pub.publish` `_init_symbol_state` `_purge_idle_symbols`

## 3. 已确认机制
- **logs q参数LIKE通配透传(09-03实证)**: GetStrategyLogs的q直拼`LIKE %q%`,q内嵌`%`可透传→`ZKC/USDT%时间=2026-09-02T14:1`即按币×分钟窗打捞DB全量日志,绕开limit2000近期窗冲刷;历史取证(veto链/入场链回验)自此不再受'日志窗流失'限制(源码strategy_handlers.go L338-340)。

- **[新增 08-29 15:2x] rotate符号形态+新壳sizing缺省(双源码定案)**: ①rotate add/remove必须X/USDT斜杠形态——裸名EqualFold不归一化=静默no-op且响应仍报removed;执行后必GET /symbols复核feed数 ②新壳config缺order_amount_mode=percent_balance+conf_sizing五键→引擎走notional默认路径→5U粉尘单;建壳checklist⑥为此设,首单必验名义≥21U。全文+源码行号git 6093c9f版行(§3本条09-01瘦身压缩)
- **[新增 08-28 03:4x] max_consecutive_entries_per_symbol=连开cap语义(源码定案strategy_signal.go L237-289)**: config字段(main/v2=模板默认3;trend=#69回滚值3,重开披露见§1.5行);计数=entry订单DB尾(requested/new/partial/filled)从最新往回数同币连续,遇他币即断,**无时间衰减**→小池/单币池=终身笔数上限(TUT单币池数无可断=物理死锁)。拦截史:ACE@08-21/COTI@08-22(trend)/MELANIA@08-23/HANA×3@08-25-26/BTR×4@08-27-28(v2)/VELVET×5+TAC×1@08-24-25(main,恰为热币晋升前压制)。热币连开拦=对"骑热币"组架构的系统性手刹;亏损面已有CB(3连败240m)覆盖,cap独占保护仅混合结果连开。trend已解除(mces0 FIX@08-28);main/v2重校=§4候选
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
- [已确认·速记] 出场体系错配: hunger30m首检批量收割未成熟仓(30-35m桶50%集中死亡/-51.39簇)→hunger45修复achieved@08-03(30-40m簇归零,45-58m wr73.7%);hunger45/0.05/0.08=现基线(#20 keep)〔全文git bbf8046前史〕
- **[速记·压缩@08-15 15:4x] SL穿刺两亚型→S18定案(08-03~08-05)**: 亚型A高ATR零复现;亚型B低ATR闪跌(n8/−18.79全long高置信带,机制=S8偏EMA2.0×ATR vs SL2.5×ATR仅0.5缓冲+砸盘量计为放量)→S18上线keep@08-05(穿刺2/−4.95收窄73.7%,ex穿刺+28.01,a/b型根除;残余c型→#22a已终止@owner直令);S18锚点永久保留〔全文见git 541a2bc〕
- **[已确认→已修复(E2 achieved @07-22 20:11Z，结案)] 边际空单带 0.55-0.60 曾是唯一五窗全负方向的主要失血源**（E2 hypothesis @07-21：24h short −5.99 wr34.1% n44，而多头 12h wr56.3% 净正）→ 修复经 21.8h/新增 ~25-26 对独立样本评判达成，见盈利侧 E2 已确认条目。
- **[已实证] 引擎不执行 `symbol_reentry_cooldown_minutes`**（07-21 06:19Z 快速档回滚 90→45 实锤：改动后 AKE 10 对/6h ≈36min 节奏，违反 90min 上限 ≥2x）。churn 治理只剩代码级 per-symbol 重入 gate。
- [观察中·速记] CB节流不根除重犯币(RIF/ONE/BANK/SYN 07-22~23多轮观察;CB在线双实证;重犯加时候选=§4#7,持久化=#35)〔全文见git 6b6a4bf前史〕
- [已结案·速记] 15-60m桶失血主体→随E2修复翻正为主体盈利桶(07-21→22七读链;桶健康度并入常规归因扫描)〔全文见git 6b6a4bf前史〕

- [已确认·速记·压缩@08-20] 短侧穿刺簇RECOVER=rollback(08-06): 双向解锁段严格短穿刺9例/−16.70(顺涨开空被延续穿刺),ex穿刺段+6.5;一步回防+S19镜像veto同窗上线(tpl563);裁决=双向未证伪,缺S18短侧镜像;恢复阶梯cd优先(§1 08-02直令)〔全文git 0980231前〕
- **[已确认 @08-07 18:2xZ · #20 verdict=KEEP] hunger_tp 0.06→0.08 放大赢单腿成立**：段n=44主腿hunger域赢均2.552raw/2.696折(note20) payoff2.42 vs 基线2.262/1.411双超回滚线(2.0/1.2)且达标(2.60折/1.55)；赢单mv>4%九笔顶10.88%=旧3%mv封顶解除直证；段面−0.336/笔miss全源hold<45m快SL 17/−31.14(预登记隔离路径,hunger物理正交)→转S21依据；新代价=超时微赢6/+3.48(6-8%roi不再45m收割的衰变尾),ex-fast段+16.34/27=+0.605/笔仍强正；hunger 45/0.05/0.08=current基线；裁决全文存_exp.fix20_verdict。

### 平台事实
- **TradFi永续类@09-10**: 币安2026中推股票/ETF USDM永续(KODEX200/SOXS/三星/SK海力士系官宣),未签TradFi-Perps协议=-4411拒开仓(GPRO直证);select_limit auto按量选池会捞入该类(09-10捞6只);处置=TradFi类隔离(§1.5)+嫌疑watch(§6);签约决策权owner。
- **closed48拉取可夹带跨月陈旧行(隔离/晋升门污染源)@09-10直证**: hours=48参数下API吐2847行,有pnl行839中仅~50在真48h窗,最老close_time=07-19(53天);懒生成行集逐拉波动=有时近窗有时全史。危害直证=对账器v1.5直喂→假plan(隔离+7币/trend+3币,BULLA 32行/−6.73全陈旧行触发,14连零后突爆);纪律=喂对账器/窗口聚合/死法分类前必按close_time预过滤,**v1.6已内置防御过滤(免疫验证:污染输入产出与干净输入一致)**。

- **平台宕机窗 08-30 ~08-10Z → 09-01 ~09Z(≈49h,无端重启族最长)** @09-01定谳: 证据=双载具日志探针(main 时间=T07/T08有行,T10后至09-01早全空;trend ts=同型0行)+closed48h=0行+钱包164.17→161.09全程冻结(差额=宕机前02:04-06:38段6笔净−2.89)+期间~14次cron排程零台账写入(推定后端down→login失败中止,符合prompt中止条款=静默无报警,已TG owner)。复活≈09:0-4xZ:running载具自动resume✓stopped三壳未误活✓feed重播种泄漏7币(隔离6+TUT,#24模式)当轮对账器清扫。启示:宕机=cron整轮静默,监控盲区在后端可用性本身。
- **logs ?q 中文子串=0字节空响应**(08-28 06:2x实证:q=触发开仓 两种编码均空,q=VELVET/USDT等ASCII正常;检索开仓事件用 q=<SYM>/USDT 再grep,勿用中文关键词)。


- **API层收养归属向量(08-26源码闭环)**: GET active 同步收养按静态config.symbols定归属(无bl/running检查,优先于orderMeta)→entry落库竞态窗内row sid可错壳(VELVET双案);错归因活仓仍被quick_trade_monitor托管=风险有界;修复=#72(4385fc3候部署);全文=git 955cb46版§3。

- **closed视图sid=owner域限定(源码+行为双证@08-21 18:1x)**: a451d32归因两路(order_id主径+±5min DB兜底,positions_binance.go)均按 `owner_id=uid` 过滤;main归owner1、专家载具归owner2=cron(uid2)查询域→**main行恒EMPTY=结构性非回归**(部署后新行FARTCOIN空17:31空sid,而main日志17:17开仓全链实证)。专家行#56 order_id精确归因healthy(COTI→trend sid实证);⚠️'带专家sid行必真为其所开'检测器**08-29证伪**(§6亚型B BTR案:跨owner域陈旧orderMeta收养可给他域交易贴专家sid)→行sid永不单独定归属。归因纪律不变:EMPTY行按池归属拆(池互斥⇒唯一);历史'幽灵sid'=兜底匹配到owner2已删载具DB行所致,同根因。
- **[新增 08-24 21:2x] closed(binance_only)行归属=两道回填,双空手⇒sid空串(源码定位)**: positions_binance.go L262-274 orderid匹配→L310-332 symbol+close_time±5min DB行兜底;48h实证main名下sid行=0、main池12行全空sid(trend/v2正常)=main DB行在crash loop下丢失或close_time分叉>5min→读侧归属缺口;交易/守护/income/池互斥归因全不受影响(VELVET 20:33开→20:37重启→21:10 trailing平+1.63全程受管);#72 post-fix空sid=修复生效预期签名(壳行不复存在→无从匹配);根治候=M通道close-sync收敛,优先级让位crash loop根因


- **[压缩@08-28] logs?q超时形态**: 稀有子串大回看可>30s/空响应,重试或缩limit;EXIT_AUDIT标签可用〔全文git;新LIKE短路条在下〕

- **平仓撤单-2011竞态+补设竞态=无害自愈**(order does not exist=已成交/已撤;引擎撤单失败继续平仓流,重复保护单被交易所-4046拒=幂等;全文git bb3b883)
- **[压缩@08-24] 账户级position行sid=陈旧symbol→strategy映射伪影(08-14定案)**: closed行sid可挂错载具(STAR空挂brk名,brk buy-only物理不可能=铁判据)→归因禁用行sid,主口径=池归属(§1.5)+方向可行性;08-15收养去重部署后新行盖真sid,存量旧行仍伪。全文git 2ce0fb4系
- **[压缩@08-28·合并三条] stop异步语义**: stop回执=入队非落地,迟滞≈池规模(main 15-44s,小池~1-10s),期间PATCH被'while running'拒→必须轮询stopped;回声行可挡停〔全文git:08-10/08-12/08-14三条〕
- **DELETE /strategies/:id/blacklist/:symbol路由对含斜杠币名404(gin UseRawPath未开,%2F不解码)**: 全币种皆含/USDT=接口整体不可达;黑名单改动唯一通路=stopped窗PATCH symbol_blacklist全量。候dev分支修复(非紧急)。
- **main stop被账户级在途持仓卡死(行为实证@08-22)**: stop判空仓以【账户级】现拉持仓为准,他载具在途仓可卡本载具stop→PATCH窗须全账户真空仓;全文git 8a797ec

- **[升格v2@08-23;再燃@08-24 06:3x] 平台级crash loop(非apply非start路径;修复权=owner硬边界#6)**: 检测=recv回落法(main IDLE top计数跌回~202=重启+回填200bar签名)。影响=清内存态(CB/统计/rotate态/择优攒批窗)不清DB(config/种子/持仓/LastEntryAt/挂单TP/SL);跨重启完好实证=hunger DB open_time锚(COLLECT精锚45m收割)+bl交易闸零违例。残余=①main feed泄漏(热清寿命≈≤1次restart) ②重启后≤2min突发开仓n=2(§6) ③评期内存计数断链。频率史:08-23 17次/08-24≈16次(峰值09:00前×9);昼夜假设候证;TG连6轮报owner;逐例史git a2e845f/b505652/17251a9

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
- **[新增 08-05 17:5xZ] PATCH /strategies/:id/config = 浅合并语义（源码 PatchStrategyConfig 核实+实锤事故）**：请求体=直接字段 map（`{"cooldown_sec":900,...}`），逐键覆盖 current，**值 null=删键**；发 `{"config":...}` 包裹体=垃圾键静默失败(首锤08-05已修)。PUT /config 才是整体替换。一律 PATCH 平铺字段。**二次实锤@08-22 21:2x(v2 _exp延评PATCH)**: 嵌套`{"config":{...}}`同样成垃圾键且返回patched无警告=目标键未更新的静默失败;平铺体+null删键修复,空仓窗零影响;纪律追加=①动手PATCH前先查本条②patched≠落库,复读必须验目标键新值非仅status。
- **[压缩@08-28] 代码入库直令首轮@08-05**: 归档协议自此运行〔全文git〕
- **[新增 08-06 02:4xZ] 冷却双层机制(源码裁决)**: cd_sec=Python侧per-symbol信号冷却(仅启动载入);引擎真闸=symbol_reentry_cooldown_minutes(strategy_signal.go L337,LastEntryAt DB锚重启存活);同币再入受max(两层)→re150在位时改cd_sec零效;改cd_sec需stop→start。详git 0980231前史。
- **[压缩@08-28] cron节奏**: Routine=quote_optimize;近期实测≈每3h(00:15/03:12型);以触发时刻为准不预设〔全文git〕
- **[新增 08-06 04:3xZ] apply=DB换绑+自带async restart（绕持仓保护）**：ApplyOptimization 不查运行态，事务换 template_id 后返回 `needs_restart:true,restart:"scheduled async"`——平台内部重启**有持仓也执行**（3仓在持实录），restart后自动回running载新码；stopped窗<30s，PATCH抢窗两拒（"cannot update config while strategy is running"=PATCH需stopped实证2次）。含义：①大改可先apply后候窗PATCH，代码上线不被持仓阻塞 ②restart清策略内存态照旧 ③ctx hash字段仍双口径（7c8c314d vs sha256=b64fb6ce，TrimSpace已知事实，字节比对为准）。


- **平台事实@08-06 14:4xZ**: ①`ctx.current_code_hash`≠sha256(current_code)（tpl563:ctx 7c8c314d vs apply b64fb6ce;tpl564:ctx 0f70eff5 vs apply b4e8c650;两代绑定代码经直diff=提交逐字节一致）→代码验证一律用current_code直diff,勿用ctx hash字段。②回测执行与live共享`/logs`流（[backtest strategy]前缀+fake redis顺序喂线,单币24h/1m≈10min+,回测期live日志窗被稀释——观测铁律窗内未见≠零加倍适用）。
- **[新增 08-06 20:3xZ] start历史回灌=200根/币(manager.go:1429,rotate-in resync同路径)**: S20注释"~400根即时全功率"有误(MAX_BARS=400仅缓存上限);300m支gate需再攒101根活bar≈100min盲窗,180m支即时在线;gate放行不留日志→事后不可复盘;定案需币安期货1m K线(vision T+1)。HFT应拦未拦案全文git 0980231前史。

- **[压缩@08-28] gate7d影子=bar投递依赖**: 断供窗漏拦→S22 emit副闸已补(AIOT案)〔全文git d1400b2〕
- **[新增 08-07 22:5xZ] apply baseline_hash=TrimSpace口径**：resolveCodeForOptimize对模板code做strings.TrimSpace后sha256=ctx.current_code原文hash(81b5cf37族)≠存储模板hash(尾换行,2cab9833族)。apply 409 baseline_race时先按TrimSpace口径重算再重试,勿盲目省略baseline_hash。

- **max_hold 时钟=币安 updateTime,可被资金费结算等事件重置(08-08 02:1x 源码+逐笔实锤)**: binance.go L1540-42 映射 UpdateTime→OpenTime,quick_trade_monitor.go L84-89 优先币安钟→updateTime 刷新即重置 60m;饥饿层用本地钟不受累(亏仓 45m 照割),滞留域仅(−5%,+8%)roi 带,现净影响+2.43 良性。判据: 亏损腿 hold>75m ≥3例或单笔≥5U→M 修复(取 min 钟);亏腿计数 1/3(KAITO 08-08)。证据链全文 git 9d0dc94。
- **[速记·压缩@08-23] 双会话抢窗双写(08-08首例)**: PATCH=_exp全量替换last-writer-wins+stop/start幂等→双会话互不感知各自"成功";同源载荷无害,**异源载荷同窗竞写静默丢先写**→互斥靠§5认领行+后启会话先探audit;全文git 766c7d9系

- **[新增 08-09 06:4xZ] ctx 两口径（源码核实 optimize_handlers.go）**: trades_window(data_source=binance)=buildTradesWindowFromBinance 打包，count=成交腿数（24h 194腿 vs 逐笔配对61笔，分批平仓一笔多腿），long_pnl+short_pnl≠realized_pnl 属口径差非bug；paired_trades 键仅 DB 源变体出现。avail 权威读径=ctx.binance.balance_usdt（ctx.account 无 balance 字段）。逐笔归因一律以 A 武器 closed48 配对行（滤无 realized_pnl 幻影行）为准。
- **[新增 08-09 09:3xZ] positions.realized_pnl=税前毛额（不含佣金/资金费,实锤）**: 24币逐一与 income 原始 REALIZED_PNL 比对 diff=0.000 精确吻合（CYS raw−12.371=closed−12.371,佣金−0.565 另在 by_symbol.commission;KAITO raw+9.539 vs net+10.536=资金费差）。含义: 历史费覆读数（均净 vs 2×来回费）实为【毛额比】,真实净额=毛额−佣金,字面'净额≥2×费'需毛额≥3×费。跨轮趋势可比性不受影响；今后 TG 双口径并报（毛额比+扣佣净额比）,红线判定从严=毛额<3×费即🔴。


- **[压缩@08-28] closed行sid归因史**: 专家sid始08-09 19:05,更早行回退池归属法;owner域限定+双回填条在下〔全文git〕

- **[新增 08-10 18:2xZ] 日志置信度=折扣后值(四例精确复算)**: 未触发信号/评估行的置信度=加分项和×低波动折扣0.8(0.65→0.52/0.75→0.60/0.95→0.76/0.70→0.56全吻合);过0.55基础线仍可被后级门拦(长锚0.70/ATR%<0.5硬滤/sides)。读日志勿把置信度当原始分;S24/25/26谱系继承同口径。

- **[升级@09-12 18:1x] 宿主DNS故障(Tailscale MagicDNS SERVFAIL)=复发性平台向量 n=2**: 08-11停机〔全文git〕+09-09 16:12Z起≥74h交易全停(episode细目§6行);特征=全域名解析死而既有WS连接存活→REST/下单全灭+重订阅危殆;修复在宿主机;工程对策=#89 DNS韧性补丁(§4)候部署;断连窗纪律=禁stop/start
- **[新增 08-12 06:3xZ] 回测挂死机制定案(bt29+源码)**: 取数FetchHistoricalCandles先于watchdog装载→取数挂起=running永久无守护;API无cancel端点;不阻塞实盘;修复候选=fetch前置deadline或cancel端点(低优先)。案详git bb2e前史。

- **[压缩@08-28] owner直令可双投递并发会话**: 执行前查audit最新态防重复动作〔全文git〕
- **[压缩@08-28] BOOT RESTORE**: 后端重启自动拉起DB态running/starting策略(lifecycle.go);gated壳靠DB态stopped免疫〔全文git〕

- **对账器版本管理(08-14确认)**: 新cron容器工作树=默认分支→ops/route_pools.py只有v1.0初版,直接跑=错误plan(实证2例:08-13容器v1.0误提议拆trend/fade池;08-14误提议清空trend池+COTI越brk直入trend)。修根@08-14:权威副本入quanty-ledger分支ops/route_pools.py,每轮fetch台账即得现版;改对账器=ROUTE预注册,改后同步台账分支副本+§1.5谱系行。

- **logs?q=检索选择性=LIKE短路(08-27 21:2x实证)**: q子串扫描按limit短路——高频子串(0.600/IDLE/币名)秒回;稀有子串(触发开仓/32.48/时间戳前缀)=全表扫>30s超时code000,与中英文编码无关(ASCII稀有串同样挂)。回溯稀有行正解=拉高频伴生子串宽窗后本地grep(触发行含conf数值故q=0.600可达);重启后每币~200预热行会吃掉币名q的limit窗。
- **[新增 08-28 00:3xZ] 高价币最小手数静默拒单=MVLL型定谳(#75;源码闭环+双案)**: 单枚价>sizing名义的币,最小手数凑整后名义超sizing上限→引擎静默拒单零日志;候部署#75加日志行;验证轨=部署后grep最小手数凑整;池内高价币(>20U/枚)受影响。全文+源码链git 9957041版行(§3本条09-01瘦身压缩)

### 双执行体(09-17 起)
- **DeepSeek管线事实**: 宿主cron ops/deepseek_optimize.py(experiment_hold冻结键)+ops/qt_breaker.py(熔断: 净≤−4U 或 wr<45%∧净<0→单向砍pct,锁24h续期自到期);admin#1;节拍≈3h+熔断+探针;只动config不动代码;_exp schema id(lock-*/exp-*)/status/changed/frozen_keys/eval_after/prior_exp/mechanism/why。改动史09-16 13:33→09-18 10:27(lev10/pct0.15越界/sl-tp/cd阶梯/mcp三连裸改/be_atr/max_hold/max_initial_margin)全文=git 7f578a7版§3。
- **热PATCH实证@09-17 07:4x(CC探针 _probe_cc_running_check 写+null删 http200 param_version v17→v18,策略running不变)**: 部署后端接受running PATCH;仓库main manager.go:1737 "cannot update config while strategy is running"线上不存在=**部署版≠仓库版**。但strategy_start.go:306-317 config仅启动时argv+env STRATEGY_CONFIG_JSON注入Python,manager.UpdateStrategyConfig只改Go内存无push→**Python侧键须重启**(min_confidence/premium/best_pick/warmup/volume_ratio/breadth/atr门);Go侧键热生效(lev/pct/mcp/tp_pct/sl_pct/hunger/max_hold/trailing/BE/bl交易级);cooldown_sec归属未核实。键归属表待首轮建立(§5 DS-5)。
- **有效门槛反推法**: 日志"置信度动态仓位 symbol=… conf=… mult=… pct=…"=逐单成交置信度;09-17 07:xx见conf 0.40/0.44成交→有效门槛≤0.40(config min_conf0.45,lcp/scp 0)。
- **Go侧LLM重写器(strategy_autotune.go)**: enabled=false不跑;apply=true/dry_run=false上膛;模型链 config auto_optimize_model→conf AI.Optimizer→env AI_OPTIMIZER_MODEL(默认anthropic/claude-opus-4.8-fast via OpenRouter);会让模型重写全码→仅校验def run(+py_compile→发版换绑→StopStrategy/StartStrategy;不校验S锚点/6符号→若被打开=S谱系风险;runs表strategy_optimization_runs(末行06-03)。
- **命名空间**: _exp=DS(CC只读,刹车frozen_keys append例外);_exp_cc=CC预注册(+watch_ds);_ai_task_cc(CC→DS)/_ai_task_ds(DS→CC)各保3天≤8KB;宿主/tmp/ai_task=owner可读合并日志,经ops/ai_task_bridge.py桥接(owner装DS侧)。
- **现货引擎事实@09-17(源码)**: 全文=prompt v5.1【现货载具】节◆引擎事实/git b341694版§3(market进程级→第二进程;可用=市价买卖+本地TP/SL;不可用=饥饿/max_hold/ROI/交易所TPSL/追踪/保本/收养/余额sizing;amount静态→S34改名义/现价;funding非usdm=0→S30/S33失活)。
- **单笔风险恒等式**: SL亏损=余额×pct×sl_roi(杠杆约掉;09-17 114×0.05×0.12≈0.68U/次);杠杆只改SL价距(噪声穿刺频率)与名义/手续费规模。
- **键归属表(DS-5结案@09-17 10:2x;grep backend/internal/strategy Config["…"] vs current_code self.cfg.get)**: Go热(热PATCH即生效)=leverage/order_amount_pct/order_amount_mode/max_concurrent_positions/allowed_sides/entry_time_windows/symbol_blacklist(交易级)/use_exchange_tpsl/take_profit_pct/stop_loss_pct(normalizedTPSLPct)/hunger_mode_enabled/hunger_after_minutes/hunger_take_profit_pct/hunger_stop_loss_pct/max_hold_minutes/trailing_enabled/trailing_callback_pct/trailing_activation_atr/breakeven_trigger_atr/conf_sizing_*(5键+conf_lo)/pyramid_*/symbol_reentry_cooldown_minutes/max_consecutive_entries_per_symbol/min_hold_seconds/auto_optimize_*;启动时读(重启生效)=select_limit/symbols/auto_symbols/symbol_select_mode;**Python侧(须重启)**=min_confidence/long_conf_premium/short_conf_premium/**cooldown_sec(Go零引用,归属定案)**/best_pick_enabled/best_pick_window_sec/warmup_bars/volume_ratio_min/breadth_min_for_long/breadth_max_for_short/max_atr_pct/min_atr_pct_for_trade/atr_tp_mult/atr_sl_mult(信号tp/sl;Go另用于exitATR)/atr_discount(_thr)/reject_on_chop/trend_confirm_bars/min_volatility/min_precision/max_price/trade_amount/cb_*/chop_lookback/vol_lookback_bars/stats_log_interval_sec;双侧=max_hold_minutes/symbol_blacklist/take_profit_pct/stop_loss_pct(Python作信号tp/sl兜底,Go作出场)。
- **[新增 09-18 12:4x] max_initial_margin_usdt 语义(源码定案)**: strategy_execution.go:127-150 percent_balance 模式下 初始保证金/仓=min(avail×pct×mult, max_initial_margin_usdt)(>0 时生效),名义=保证金×lev;order_pct_exclude_leverage=true 时改钳名义≤cap×lev。Go 热键。DS 10:27:59Z 裸改 0→50: avail102×0.075×[0.6,1.0]=4.6~7.7U/仓 远低于 50→**无效果**(需 avail>667U 才触及);方向=降敞口→不纠回,watch-no-effect(§5 DS-11)。
- **[新增 09-18 10:2x] Go侧SL距离clamp=物理护栏已内置(源码+日志双证)**: strategy_position.go:486-498(开仓设TPSL)与:885-898(监控补设) 把交易所SL价钳在 entry×(1∓0.3/levUsed) 内 → lev10 时 SL 距离≤3%价(=−30%ROI 上限),ATR口径 2.5×ATR 超过 3% 即被钳(09-18 09:01 STRK/09:03 G 日志 sl=3.00% 实证;G tp12.77%/sl3.00%=R:R被钳偏向盈利侧);清算距(10x隔离≈9.5%)≥1.5×3%✓ ⇒ **CC-7 max_atr_pct 2.67 结案(无需重启)**;lev20 时钳=1.5%(升档再核)。
- **[新增 09-18 10:2x] Python兜底比例重启陷阱(源码定案)**: current_code:170 `_parse_ratio(v,d)`=`max(_f(v,d),0)`→config stop_loss_pct=0/take_profit_pct=0 在下次重启时令 Config.SL_RATIO/TP_RATIO=0(非默认0.03/0.06)→ATR=0 兜底路径 `_calc_tp_sl` 给出 sl=tp=price→Go 拒开(tp/sl须有效)。修复=config 新增 `sl_ratio`0.03/`tp_ratio`0.06(code:1361-1362 优先读此二键;backend/ 零引用=热PATCH无副作用)已落地 v39@10:2x。**-4028 Leverage invalid**(哈基米 09:18): 引擎日志"对齐交易所杠杆失败(按现有杠杆继续下单)"=该币按交易所现存杠杆成交(名义仍按 config lev 算)→杠杆不可控币按规则隔离。
- **Python进程有效值(START行=ground truth;logs q=cooldown%3D)**: 最近START 09-16 04:33:40Z(owner重启/部署,非audit动作)注入 cooldown=180s min_confidence=0.35 atr_tp4.5/sl2.5;成交conf最低0.36且折扣先于门(code 674-676→816-823)→有效门槛≈0.35(lcp/scp≈0)。**DS 09-16 16:12~09-17 04:09 的 cooldown_sec 300→1800 与 min_confidence 0.45 均未生效(config≠进程)**;下一次重启将令其一次性生效(cd1800=每币30m冷却,频率骤降)→Step0窗前须先定这两键终值。
- **breakeven_trigger_atr语义**: strategy_exit.go:171 trig<=0→maybeMoveBreakeven no-op(保本关闭);admin 08:30 1.0→0裸改(无_exp;DS或owner手改待认),watch_ds登记。**饥饿数字**: hunger_take_profit_pct0.08/hunger_stop_loss_pct0.05=ROI口径(lev2→价格±4%/±2.5%),45m后|roi|≥阈即市价平(日志"饥饿模式触发");lev2态死法结构=初始SL6%价格(几乎不触)→45m饥饿SL2.5%价格→60m硬超时。
- **手动平仓端点**: POST /api/positions/close?symbol=X(positions_handlers.go:517)仅usdm分支(撤联动TPSL+市价平),spot无路径→DS-7现货超时缺口部署前=TG报owner手平。GET /api/stats/dashboard无余额字段;ctx balance_usdt=availableBalance(binance.go:263),钱包≈avail+Σ名义/lev。
- **反事实数据源@09-18 00:2x**: data-api.binance.vision(币安现货REST公共镜像)本容器可达(http200;fapi.binance.com/api.binance.com 均451)→/api/v3/klines?interval=1m 现货收盘作永续代理;归一化=以入场分钟前一根现货收盘为p0消基差;验证=19笔末roi误差≈1-2pp(ONE/USDT例外偏差>10pp=不可信,现货深度差);futures vision日档T+1(09-17档00:2x仍404→§5 CC-1全集复核)。方法cf2.py(scratch,逻辑:45m后逐分钟roi≤−X即按−X×保证金记,否则记实际pnl)。

## 4. 假设库·候选队列（v2 迁移注记 @08-01 16:40Z：本节与 §6 观察计数合并为【假设库】，内容全量保留；prompt v2 起执行门槛=逐笔证据标准[≥20 笔同型死法或机制落到源码行为]，旧 v12 Step 4.6c 门槛作历史参照）
- **[候选·连开cap全组重校 @08-28 03:4x]** 结论=多币池载具cap3可自愈非死锁,仅单币池致死(trend已修),main改mces必要性下调,候选降权窗;依据链与全文=git f90d9dd版§4。

| # | 类型 | 内容 | 依据 | 复现计数 | 状态 |
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
| 18 | M通道 | closed窗口边界播种修复(幻影行族根因) | 全史git 26eaf24 | n/a | **修复部署@08-15**(f4848f8封边界播种;隙型另路径未封,形态链git bb3b883);观察1-32史:连17轮全零至08-22〔git 0940c62/044907d〕;行集随重拉波动=懒生成方向;滤行纪律无限期保留;结案重置=隙型机制源码查明或#56部署后复核 |
| 62 | 观察(S21域) | S21急再入veto机会成本反例(首例08-31 03:11Z轮;登记事故=空提交ceb2d31,06:18补记) | commit ceb2d31;S21本体KEEP判据§6 | n=1 | 观察;判据:累计≥5例或段净≤−5U→复核S21窗参;veto事件≠反例,须影子胜方可计(emit veto带戳例09-01两批不计,细目git 044907d链);登记纪律=push前git show --numstat验非空 |
| 21 | M通道 | backtest可用性修复包:①看门狗+cancel端点+挂死根因 ②preroll预热 ③缺因子降级注记 | §3 08-03源码级机制+差分重放实锤;08-06 +6例挂死复现;运维规则=烟雾只发≤18h窗;全史git d19c710 | n/a | 候选(候部署窗);task30-34史=重启株连+7d窗误用,规程=12h窗〔git〕 |
| 3 | E8 类 | per-symbol 重入 gate（代码级，需新锚点 S17） | churn 残留；引擎不执行重入字段（见已确认机制）。16:16Z 注：AKE（churn 代表币）24h +5.06/12 已转盈利，紧迫性降 | 0/2 | 候选 |
| 5 | 研究 | TP/SL/max_hold 与 15-60m 桶关系 | 桶已翻正，优先级降 | n/a | 低优 |
| 6 | E7 类 | 热点∩池内维度加权（择优批中 trending 币） | 热点∩池∩盈利连续8轮链+反例COTI/AKE热而亏³=榜首逆信号〔压缩@08-09 15:2x,全文见git e93a79e〕;§6计数行为准 | 0/2 | 观察（依据弱化第4轮） |
| 7 | 观察 | CB 重犯加时（同币第 2 次隔离 ×2，S17 类） | CB throttle-not-eradicator 多币多轮观察;cb_consec_losses/cb_quarantine_min 均 config 可设(代码 line557-558);历史 RIF/BANK/BEAT/BULLA 全零新增归档 | 0/2 | 观察;判据=同币CB隔离期满重入再血达4.6c→升级;逐轮历史链〔压缩@08-07 16:1x,全文见git b1bd082〕 |

| 11 | 全文=git 9d56e10版 | | |
| 47 | FLEET候选 | S27突破追动量复活版 | **已兑现@08-29=FLEET复活brk-v2(tpl973=S26内核+S27),本行让渡§1.5 brk行与_exp** | — | 结案→§1.5 |
| 55 | E7类(trend) | **EXP:trend prem降档候选——watch结案@08-21不改**:真凶=atr_discount非S24;全文git(已自证@08-21 12:18 COTI破荒)或EXP降门;费覆红线内只登记不动〔分带演化+ACE三条件全史git 044907d/92fb9c3〕 | 机制级(gate) | 0/2 | 候选(需求已减:破荒后活性恢复) |
| 65 | 代码缺陷(组共病;v2/trend活性) | 信号门浮点边界:score与门槛精确等值被静默丢弃 | 活体证据08-22 COLLECT六行;机制=浮点算术直证 | n=6行/1夜 | **修复executed@08-22(config:v2 scp0.049+trend lcp0.149)→epsilon代码级永久化@08-23 21:24(v2域随S28 apply,ss≥thr−1e-9);trend域曾靠lcp0.149→**回退@08-25→重修executed@09-03(owner频率直令窗顺手lcp0.149+_exp预注册,trend域恢复;main两侧维持不修)**;main两侧维持不修(短侧意外保护+长侧0.90反向安全,owner'先这样'批准);全文git a2e845f系** |
| 66 | 代码候选(v2原型正确性;批2) | S28-fade衰竭签名直入重写 | v2壳已退役@08-28,S28随葬;复活=新壳FLEET时重估;设计全文git | n/a | 冻结(宿主退役) |
| 58 | E9类(main扩仓) | E9扩仓rider(0.08→0.10)兑现@09-03 | 直令+rider全文config._exp史;门史git f3c6469 | n=12段(史) | **结案;E9裁决=ROLLBACK@09-03 21:2x(24h−9.03∧段n21/−9.42;pct回0.08);全文git 64ede74版行** |
| 52 | 全文=git 9d56e10版 | | |
| 53 | 引擎候修 | DeleteStrategy斩草除根(无条件Kill) | 修复已推fa93736@08-16候owner部署;翻案@08-18幽灵成交=0→降卫生级 | n=1 | 候部署(卫生级);全史git |
| 49 | 组共病(出场几何) | trailing(act1.0/cb1.2%)+BE(1.0)重构 | 72h n96基线赔率0.88;verdict全文config._exp+git 191b049 | n=30段 | **结案KEEP@08-16**=main基线几何 |
| 57 | ROUTE病理(阈值候选) | **晋升证据不可移植**:跨模板方向净额不认,专家进池须机制同型证据(自身样本或该模板回测烟雾) | VELVET/BULLA晋升入fade后反亏实证链+债务激活史(v2建壳/MELANIA活测第3例)全文git f71d825/17251a9 | 病理级 | 登记@08-16;**S28批1已交付@08-23,阈值重设计=S28段证据到手后另行ROUTE预注册**('机制同型证据'现定义=衰竭签名适配性,须live段校准);**+MARSCOIN案@09-06 12:2x=同族新证**(双向币提名long-only池,执行层promo_hold,条款§1.5) |

## 5. 待落队列（已决定、仅被持仓/平台锁阻塞的动作；空仓窗按 Step 2.5 逐项落地）

| # | 类型 | 内容（含完整意图） | 登记轮 | 状态 |
|---|---|---|---|---|
| DS-1 | owner裁决 | mcp——**结案@09-18 06:2x: owner直令"不要不下单"→纠回10(v34);DS再缩=越界当轮纠回** | 09-17 | closed(**13:15Z admin PATCH mcp 6→2 裸改无_exp;降敞口→CC未纠回(纠回=刹车态升敞口须RECOVER门);候owner一句话裁10/6/2;_exp_cc.watch_ds已记;20:2x仍2,TG提醒#2;**00:2x仍2,TG提醒#3;05:2x仍2,提醒#4**) |
| DS-2 | owner裁决(需重启) | 质量门恢复Step0: min_confidence 0.45→0.55+lcp 0→0.10+scp 0→0.05(Python侧键,stop→PATCH→start空仓窗一气呵成)。**证据更新@10:2x: 逐笔join显示提高min_conf不是止血杠杆(§4 conf≥0.60行),Step0原样不推荐;进程现值min_conf0.35/cd180s(§3),任何重启会令DS写的cd1800/min_conf0.45一并生效→重启窗前owner须定终值(建议cd180~300保留频率)** | 09-17 | open |
| DS-3 | owner安装 | ops/ai_task_bridge.py装进DeepSeek侧cron(桥接/tmp/ai_task↔_ai_task_cc/_ai_task_ds);装前留言板仅config键半通(CC已写首条) | 09-17 | open |
| DS-4 | owner保险 | auto_optimize_dry_run=true(Go侧重写器上膛保险);或owner明示保持现状 | 09-17 | open |
| DS-6 | owner部署(现货) | 第二进程quanty-spot(配方=prompt v5.1◆部署配方/git b341694版§5): env覆盖PORT8081/DB quanty_spot/REDIS_DB1/BINANCE_MARKET=spot+BASE_URL/WS_BASE_URL;API key现货权限;划转≤期货30%;反代;建claude_cron;自检market=spot;填BACKEND_SPOT/SPOT_ID | 09-17 | open |
| DS-7 | M候选(现货超时) | claude/dev-spot-maxhold: quick_trade_monitor.go:41去usdm门(closePositionForInstance已支持spot),使现货持仓有max_hold出场;未部署前cron巡检持仓龄≥180m→**端点核实@10:2x: /api/positions/close仅usdm,spot无路径→TG报owner手平** | 09-17 | open |
| DS-8 | owner确认 | 24h income TRANSFER −50U(12h窗无)是否=现货钱包划转;若是→回填BACKEND_SPOT/SPOT_ID起跑canary;钱包口径现≈121U估(avail78.8+保证金41.9);**@20:2x TRANSFER已滚出24h窗(入账∈09-16 15:2x-20:1x),仍待owner认** | 09-17 | open |
| DS-9 | owner确认 | 08:30Z admin PATCH breakeven_trigger_atr 1.0→0(无_exp登记,DS节拍外)是DS还是owner手改;已按裸改watch_ds(eval 20:30Z) | 09-17 | open(**裁决@15:20Z: watch线破(open≥08:30 n27 毛−6.26U≤−4U)→DS-ROLLBACK be_atr 0→1.0已执行复读✓;仍候owner认领:若为owner手改→告知即恢复0**) |
| DS-10 | owner确认 | 15:16:07Z admin PATCH max_hold_minutes 60→240(无_exp,DS节拍外)归属待认;watch_ds线: 段open≥15:16Z hold>60m子集n≥8∧子集净≤−2U 或 段净≤−4U→回60;eval 09-18 15:16Z或子集n≥8先到;**读数@12:4x: 段n66 +6.19;子集n7 +1.22→线未触,预读KEEP 240;14:15Z自查点终裁** | 09-17 | open |
| CC-1 | CC反事实全集 | 09-17 futures vision 1m日档(T+1;00:2x/05:2x仍404,09-16档200)到手后用cf2.py重跑post-BRAKE全45笔+禁多段: 复核 hunger_sl 0.025 结论(赢单误杀计数/净额差);误杀≥2笔→回滚0.05;同时算 hunger_after 30/45 与 trailing_callback 1.2%放宽反事实 | 09-18 | open |
| CC-2 | RECOVER门跟踪 | 门自09-18 06:2x只管lev升档;lev已按owner令10;**@10:2x 6h 钱包净+4.39(+3.9%) 24h −2.44(−2.2%)=半开;下一档(lev12/20)非owner令不推,须双转正+预注册+max_atr同步(lev20钳SL1.5%)** | 09-18 | open |
| CC-3 | 池卫生(CC) | BR rotate remove | 09-18 05:2x | **结案@10:1xZ: rotate remove BR+哈基米 → feed184,隔离∩feed=∅复读✓** |
| CC-4 | 策略代码(S35候选) | 入场侧高分延伸veto: conf≥0.60桶(09-16 16:00→07:52 n26 wr31% 净−28.79=段亏67%,穿刺69%<3m)=延伸段末端入场;设计门=先取1m K线(data-api.binance.vision现货代理或vision T+1)量化 (entry−EMA20)/ATR 与 bars-since-cross,≥20笔同型再写gate;apply需空仓重启窗(有持仓stop被拒,严禁造窗)→send_later探针≤3 | 09-18 06:2x | open |
| CC-7 | 物理护栏 | max_atr_pct 6→2.67(lev10 清算距≥1.5×SL距) | 09-18 08:1x | **结案@10:2x=引擎已内置**: Go clamp SL≤0.3/lev=3%价(§3 新条),无需重启;lev20 升档时再核(钳=1.5%) |
| CC-8 | 定时 | DS breaker锁到期→order_amount_pct 0.05→0.075 | 09-18 08:1x | **结案@12:4xZ 执行 v42 复读✓**(门: 6h钱包净+3.8%/24h+1.6%,锁12:37:15Z自到期未续;预注册见_exp_cc主条: expect 均净≥+0.10∧6h>−8%∧笔/日≥38.7∧SL均亏≤2×赢均;劣化线 段净≤−6U或6h≤−8%→回0.05;eval 18:45Z或n≥30;_exp.cc_note已补,frozen_keys未动) |
| CC-9 | DS越界簿 | admin 06:30:06 mcp 10→4(owner直令10当日两次重申后10分钟)=协议7越界#3(史: 09-16 22:10 10→6 / 09-17 13:15 6→2);已纠回;留言板已请求勿再动mcp/sides;再犯→TG owner+建议owner在DS脚本冻结mcp | 09-18 08:1x | open |
| DS-11 | DS裸改watch | 10:27:59Z admin PATCH max_initial_margin_usdt 0→50(无_exp;DS 10:2x节拍):语义§3新条=初始保证金/仓上限,现4.6~7.7U/仓不触及=无效果;降敞口方向不纠回;watch-no-effect(无metric);DS要其生效须带_exp;owner若为手改请告知 | 09-18 12:4x | open(watch-no-effect) |
| CC-10 | 已落地(FIX) | 重启兜底比例: config sl_ratio0.03/tp_ratio0.06(§3 新条;v39@10:2x 复读✓);任何重启窗前复核两键仍在;DS 若删此二键→当轮补回+留言板 | 09-18 10:2x | **落地@10:2x** |
| CC-6 | 评判 | OWNER-DIRECTIVE P1/P2 段评判 | 09-18 06:2x | **结案@10:2x=KEEP 3/3**(n32 193笔/日 均+0.137 wr56% SL均亏−0.595/−0.705≤2×赢均1.06;worsen未触)→tp/sl pct=0 永久化,P2回滚路径退役 |
> 维护注指针集: #87/#86=git 805a115;#84=git dd4326e;#83=git 09-05 06:2x版;#82=09-04 07:3x版;#81/#78=git 4be520c;#74=2aa13b1;#70=17251a9;#68=0940c62。
> 维护注(落地结案@09-14 14:52Z): #88全绿(bl22落地+feed卫生remove15)全文=git 6c1bd6a版§5。

## 6. 假设库·观察计数（v2 迁移注记 @08-01 16:40Z：并入假设库，与 §4 合称；跨轮累计；9 秒日志窗单次未观测 ≠ 零，以本节跨轮增量为准）

| 计数项 | 读数 | 更新轮 | 备注 |
| backend→Binance REST DNS断连 | episode结案@09-14 14:4xZ(118.6h零成交;owner docker重启修复);全文=git 6b41621版§6 | 09-14 14:5x | 复发判据=fapi SERVFAIL再现→重开计episode n+1;#89(c49c555)根修推荐不变 |
| TradFi嫌疑币watch | BYD/AXTI/INTW/MVLL/MEGA(零史+名形存疑,无直证);**FLNC@09-17 直证-4411×2→已隔离(bl20;规则"任一现-4411即隔离"首次生效)** | 09-17 15:2x | 任一现-4411或官宣名单确认→即隔离(不限流);MEGA大概率MegaETH(低嫌) |
| 幻影行post-fix观察 | 累计n=2(SCRT@08-25/TAC@08-29);全文=git 6b41621版§6 | 08-28 18:1x | n≥3或含实亏→升§4 |
| epsilon边界放行观测(v2域) | n=5/类净+0.027;全文=git 6b41621版§6 | 08-28 18:2x | 累计n≥5∧类净≤−2U→复议 |
|---|---|---|---|
| **09-18 P3 lev10 段终裁=KEEP 4/4(RECOVER)** | @12:4x 段(open≥08:12:09Z) n35 186笔/日 净+5.62 均+0.161 wr49% 赢均+0.750 亏均−0.396;expect 笔/日≥38.7✓ 均净≥0✓ 交易所SL均亏−0.811≤2×0.750✓ 6h+3.8%✓;死法: 交易所SL 7/−5.68(SL距≈1.2~2.4%价) 饥饿1/−0.76 其它亏9/−0.36 ‖ TP 4/+7.54(AKE/CROSS×2/UNI) trail 11/+3.56 guard_sl 2/+0.99 sl_crossed 1/+0.33;多14/+1.98 空21/+3.64;名义均53.8;费覆 0.161 vs 3×费0.161=临界🟡;24h tape n72 均+0.089 wr58%;income 6h净+3.92(+3.8%) 24h +1.59(+1.6%)。首读@10:2x(n20 +4.27)与09-17刹车基线史=git 56a5311/5d2c77a版§6 | 09-18 12:4x | 防御线保留(24h≤−20%→lev10→2整包);cc8段另计 |
| main断流观测(三闸交集关门) | main笔数0/24h@09-10 21:1x=断连首全零轮(30.2h零成交;史2@12:2x/16@04:1x;微差双胞纪律留档git 2596a9a) | 09-08 18:1x | 判据不变:再现6h+零笔且池ATR中位≥0.5→查管道;每轮记main笔数 |
| rotate has_open_position判定条件观测 | n=2矛盾@08-23(15:16 COLLECT持仓中remove未被拒)〔全文git 0940c62〕 | 08-23 | 再现1例→定性(疑判定=本载具视角);影响=互斥窗口期 |
| apply重启作用域观测 | **定案@08-24 12:4x n=2→升§3**(tpl823+tpl887双证,他载具feed/IDLE计数连续) | 08-24 12:4x | 已定案;反例(他载具feed跳回种子全集)即回§6重开 |
| 重启后速开仓观测 | n=6/亏2@08-28;全文=git 6b41621版§6 | 08-28 03:4x | ≥5例且亏单≥3→议重启后静默期 |
| 收养错归属亚型(#57族;§4#72) | n=2@08-29(亚型B首例BTR);#72b=553c8c3+4385fc3候部署;全文=git 6b41621版§6 | 08-29 06:2x | 判据: #72/#72b部署后再现任何载具sid错归属行→重开源码勘察 |
| main行零归属(空sid;§3 closed回填缺口) | 新行我踏马来了闭+BAS活跃行均空sid@08-28 12:3x=结构性签名维持(#58解锁条件未变) | 08-28 12:3x | 无P&L实害;判据:crash loop根因修复后仍空sid→升M通道close-sync候修;每轮扫main池closed行空sid计数 |
| closed平仓行消失观测(≤48h短窗) | +2再证@08-28(09:14的48h拉漏VELVET/MAGMA行,09:30的4h重拉均已现=懒生成方向铁证,行集随拉取波动非丢失);计数与形态史git 17251a9/eaf1bcd | 08-28 09:3x | 纪律:窗内行少≠没交易,income n为准;长窗>120h禁用作逐笔 |
| 已结案·终态归档集(18项瘦身@08-26 12:4x) | 18项终态读数与重开条件全文=git 8705b00版§6(UTC早晨段/pick_lose/信号转化0/WS断连/单币失血11币/long双窗/15-60m桶/穿刺亚型c/S20/hunger_tp/S21/fade0.60墙/追跌空/长侧穿刺/统计汇总/S23影子/连开拦截/CB熔断/DB挂死/0.600档rollback) | 08-26 | 触发即复活行 |
| 终态归档集2(4项瘦身@09-17) | 平仓撤单竞态error/热点∩池内盈利/S19顺涨拒空KEEP/short双窗负;**粉尘残量亚型n=1@09-17 19:52 AVA**(饥饿TP后残量二次归零,无实害;≥3例→升§4/M通道) | 09-17 | 全文=git f90d9dd版§6;判据不变,触发即复活行 |
| main无端重启重播种观测 | 已升§3平台事实@08-15;最新例08-26;逐例git 188af33系 | 08-24 | feed数每轮复核;部署权owner |
| BICO资金费磁铁长侧 | 结案=已隔离@08-17(48h n8/−4.05;全文git bb3b883系) | 08-17 | 机制病留档;rehab按头注 |
| #79·closed行sid旧绑定继承(#77族) | 双行/壳行裸奔形态与判别特征全文=git e9d1b17版§6;退役壳已从平台删除@09-15~16→向量关闭待复核 | 09-01 | 复发判据=新closed行strategy_id≠main∧symbol在main池 |
| S31·regime动态方向偏置(结案KEEP@09-02) | 上线tpl988@09-01;评判KEEP(段n22 ON档多n3均+0.73 vs N/OFF n5净−1.99);verdict=ops/exp_archive/s31_verdict_20260902.json;全文git e9d1b17版§6 | 09-02 | 锚点§2 S31 |
| funding磁铁·他币再现watch | S30上线@08-29/S33下修@09-08;era史n2/−2.83;全文git e9d1b17版§6 | 08-27 | watch=疫苗效果验证 |
| 硬超时磨损类 | 48h滚动n13/−5.79@09-10 21:1x;全文=git 6b41621版§6 | 09-09 15:2x | 行动门: hold≥40m类n≥20∧类净≤−3U→升§4(方案max_hold60→45或hunger45→30;先做T+1 vision反事实) |

## 7. 运行日志（每轮一行，新行追加在表首）
| 2026-09-18 10:2x-10:3x | 交互轮·owner问"百分比+500U上限": 已是percent_balance;owner自落 max_initial_margin_usdt=50@10:27:59(admin)=500U名义上限@lev10,CC同值撞车只落_exp_cc.p4_cap;P3段(open≥08:12)n23 244笔/日 wr52% 毛+3.94 均+0.171 avgW+0.68/avgL−0.38 名义中位54U 多+3.24/空+0.69(AKE +2.96 4m);杠杆对齐lev=10 逐单✓;pct0.05 夹紧⇒conf mult无效(登记§1);CC-8 12:37Z pct→0.075 不变;TG 简报 |
| 2026-09-18 12:4x-12:5x | **自查点 CC-8(send_later)**: 管道活(active 3 SYN多/STAR空/DRIFT多;avail102.5 钱包≈119);income净 1h+0.55/6h+3.92(+3.8%)/24h+1.59(+1.6%) 刹车无触;DS对账: 10:27:59 裸改 max_initial_margin_usdt 0→50(无效果→DS-11 watch-no-effect),之后0 PATCH,锁12:37:15Z自到期未续,_ai_task_ds空;**评判 P3 lev10 n35≥30先到→KEEP 4/4**(186笔/日 均+0.161 wr49% SL均亏−0.81≤2×赢均1.50;费覆临界🟡);**执行 cc8: order_amount_pct 0.05→0.075(v42 复读✓ pct×mcp=0.75顶格;_exp.cc_note补记,frozen_keys未动;预注册 expect/劣化线/eval 18:45Z或n≥30)**;max_hold watch 子集n7/+1.22未触;全键diff无未见漂移(v40/41 无audit=版本计数器非PATCH来源,config经全键diff核对一致);留言板#11(8条7783B);风险口径: 名义/仓46~77U,10仓同触SL典型−10U(8%)/clamp3%极端−20U(17%);首单0.075名义待14:15Z核;§5 CC-8结案+DS-11新增;§6 P3终裁行;下轮=14:15Z cc8首读+首单名义+max_hold终裁 |
| 2026-09-18 10:1x-10:3x | **RECOVER 首评轮(cron)**: 管道活(active 2: SYN多/STRK空;avail113.5 钱包≈119);五窗income净 1h+2.32/3h+2.28/6h+4.39(+3.9%)/12h+3.28/24h−2.44(−2.2%),刹车线无触;DS对账: 08:12后audit 0条(07:xx/10:0x节拍静默)/_ai_task_ds空/lock至12:37Z frozen[pct,lev]✓;**评判**: P1/P2(ATR口径)n≥30先到→KEEP 3/3(n32 193笔/日 均+0.137 SL均亏≤2×赢均);P3 lev10 首读 n20 +4.27 均+0.213 wr50%(交易所SL 3/−2.11·TP 2/+4.30·trail 7/+3.03)expect 4/4在轨;FIX hunger_sl 0.025 结案(等价并入P3);max_hold240 watch 子集n7/+1.22 未触;**改动(Go热 v38/v39 复读✓)**: ROUTE bl+哈基米(48h n8/−4.01∧-4028 lev invalid)→bl24 + rotate remove BR/哈基米→feed184 隔离∩feed=∅ ‖ FIX sl_ratio0.03/tp_ratio0.06(重启兜底陷阱 §3) ‖ _exp_cc 重写(P3顶层+cc8预注册+prev_verdict) ‖ 留言板#10(裁至7条6803B);**源码发现**: Go clamp SL≤0.3/lev(3%@lev10)=物理护栏内置→CC-7结案;有效门槛复核 conf min0.36=config0.35✓ cs_mult≤1.00✓;频率仪表 24h 66笔/日 均+0.027🔴 北极星+1.8 / 6h 136笔/日 均+0.155🟢 +21 / P3 226笔/日 均+0.213🟢 +48;宏观FGI56/BTC+2.2%/ETH+3.1%/trending∩池 ARB NEAR PONS PUMP RENDER UNI;现货候部署;send_later 12:40Z(CC-8 pct0.075) + 14:15Z(P3终裁+max_hold);§4两行结案,§5 CC-3/5/6/7结案 CC-10落地;下轮=12:40Z执行CC-8→14:15Z裁P3 |
| 2026-09-18 08:0x-08:2x | 交互轮·owner直令"增大 10x"(P3 v37 lev10/mcp10纠回/hunger ROI换算/cs_mult1.0;CC-8 12:37Z排期)全文=git 5d2c77a版行 |
| 2026-09-18 06:1x-06:3x | 交互轮·owner直令"不要不下单/改变策略"(P1 sides双向+mcp10 / P2 tp-sl pct→0 ATR口径 / 钉回min_conf0.35 cd180;prompt v5.2交付)全文=git 5d2c77a版行 |
| 2026-09-18 05:1x-05:2x | BRAKE step2续·池对账补跑轮(ROUTE隔离BR/AVA/ONE bl20→23;FIX段首读n3;lev×ROI耦合候选§4)全文=git e94a8b0版行 |
| 2026-09-18 00:1x-00:2x | 反事实轮 全文=git f90d9dd版行 |
| 2026-09-18 01:1x-01:3x | 自查点·禁多评判hold+FIX hunger_stop_loss_pct 0.05→0.025(01:19Z)全文=git 955cb46版行 |
| 2026-09-17 20:1x-20:3x | 禁多评判预读轮 全文=git b341694版行 |
| 2026-09-17 15:1x-15:3x | BRAKE第二步·禁多轮(worsen触发→sides=[sell];DS-ROLLBACK be_atr;FLNC隔离;mcp6→2裸改watch)全文=git 6c1bd6a版行 |
> 瘦身注指针集: 0x归并): 断连#35(09:1x)全文=git 0f076c6版行。; 断连#34+probe(09-14 09:5x-11:5x)=git 9137f71;#33+probe(06:5x-08:5x)=19130f2;#32+probe(03:5x-05:5x)=e9b12ca。; 1x归并): 断连#31(21:1x)全文+#32后probe#1-3注记(00:5x/01:5x/02:5x)=git 3f6d7a6版行。
<!-- §7瘦身史指针集v2=git e9b12ca版§7注原文(09-14 00:1x及更早全部归并注逐hash在内,含前v1集8729d22) -->
