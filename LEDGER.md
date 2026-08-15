# QuantyTrade 改动台账（LEDGER）

> 权威分支：`quanty-ledger`（2026-07-22 12:19Z 由种子 `claude/jolly-bardeen-sz6d04` 引导上线）。
> 由 cron 运行维护，每轮必写。**瘦身协议 @08-01 17:35Z（用户直令：省 token）**：本文件保持精简工作集，全量历史永在 git（压缩前快照=29eb4c5）。纪律：§7 只保最近 10 行（新行≤800字符）；§6 每计数项只保最新读数；§5 只保 open 项+最近 2 条维护注；关闭候选/已落待落项直接删行；超长叙述以〔压缩〕标记截断。人工编辑请只增不删原则对 cron 瘦身豁免。
> 由 cron 运行维护，每轮必写；人工编辑请只增不删。协议：v12 七节制（2026-07-22 16:16Z 起升级；此前见 `docs/cron_prompt_v11_addendum.md` Step 7）。
> 策略：8eb182b6-ee74-4125-a602-f0a91f376432（tpl447 @2026-07-22）

## 1. 用户常备直令（权威登记处；prompt 内快照与此冲突时以本节为准）

1. **单笔名义 ≥20U**（2026-07-22 重校）：口径 = 余额×pct，不含杠杆。执行基准采保守口径 = 开仓时可用余额×pct，两槽最劣须 ≥20U（07-22 16:16Z 实测二槽 110.6×0.19=21.0 合规）。任何档位公式与此冲突以 20U 为准 ceil2 向上取整。
2. **全天开仓**（2026-07-19）：`entry_time_windows` 保持 `""`；恢复窗口属 🔴 提案须用户批准。
3. **max_concurrent_positions 基线 2**（2026-07-19）：升 3 只经 E4；降回 2 随时允许。
4. **引擎下单语义含杠杆**（2026-07-20）：notional = avail×pct×lev×mult，与币安滑杆一致；20U 度量口径不含杠杆，两口径并存。
5. **按置信度动态下单量**（2026-07-20）：引擎 conf_sizing 已实现（mult∈[0.6,1.4]、交易所名义地板 `conf_sizing_min_notional_usdt=21`）——策略代码勿双重实现（07-20 HANA 53.12U、07-22 AKE/MIRA mult≈1.4 实证）。
6. **充值信号常备令**（2026-07-20，长期有效；自 config._exp.user_directive_20260720 迁入 @07-22 16:16Z，原键已移除 @07-23 04:18Z ✓）：触发条件 = L2→L1 升档成立（数据条件满足且**无冻结**）且 12h wr−be≥6pp 且 12h/24h net 双正 → 立即 TG 发【📥充值信号: 建议充至钱包总额 250-300U（登记时≈157U，即约 +100-150U）；到账后升档/维护 PATCH 按新余额重算 pct】。次序纪律：用户等 TG 信号再充值；若发现余额突增 >20% 且非升档所致（用户提前充值）→ 当轮 LADDER-MAINT 按新余额重算 pct 压回 L2 约 20U 规格并 TG 说明，不视为违规但必须压回。⚠️ 07-22 16:16Z 校准：入金检测必须用**钱包总值口径**（balance_usdt + Σ(持仓名义/lev)）；`balance_usdt` 仅为可用余额，平仓释放保证金引起的 avail 跳升不是入金（07-22 120.7→151 即此类假信号，已证伪）。 **✅充值已到账 @08-02 15:11Z 发现**：income TRANSFER +148.14U（钱包 85→227.28），早于正式信号触发（12h/24h 未双正）＝用户提前充值情形 → 当轮 LADDER-MAINT pct 0.27→0.11 压回 L2 已执行（两槽 25.0/21.15≥20U），照直令不视为违规。 📥**信号首发@08-05 16:19Z**(3/3:S18 keep解冻+当档[pct0.11自08-02 15:11Z]33笔净+12.90+24h wr−be+25.5pp+12h/24h双正[12h n2轶事如实注记];钱包239.85→建议充至250-300U)。 **✅二次到账@08-08(16:12-17:41Z窗)TRANSFER+74.4U**:钱包221.4→295.7=建议区间顶部;20U复算已退役(08-03直令)→零pct动作,单槽名义随avail自然放大(现74-103U);本次信号闭环,常备逻辑继续有效。

- **08-02 17:0xZ(交互)**: 用户明确短线高频定位+主动问 cd 降档 → 恢复扩张阶梯次序调整为【①cd 1800→900 ②allowed_sides 回双向 ③仓位/并发】,cd 优先于 sides;触发门不变(6h/24h 双转正,且 FIX 结案或≥35样本指标同向);当轮拉数 6h 0胜8负−11.82 明确否决即时降档,用户知情。

- **08-03(交互)教练模式常备直令**: 用户明示"不想被慢慢替换"→所有输出加教学层: ①TG 报告尾固定加一行 🎓(本轮一个概念或一个开放判断题,≤200字,用当轮真实数据讲) ②交互回答先给"怎么想"(判断框架/我用什么证据/你可以自查的命令或指标)再给结论 ③非紧急决策改为"呈现取舍+我的推荐+留一晚给用户拍板"(紧急刹车与硬边界内动作照旧先斩后奏) ④用户提出的质疑必须正面回应证据而非以权威压过。此直令与瘦身纪律并行,🎓行不计入800字符限制。

- **08-03(交互)并发阶梯直令**: 用户改并发硬边界 ≤3→**≤10(阶梯终点)**,基线仍2。逐档升门(每档全部满足): ①业绩门=当档≥30笔且净额为正、wr−be≥0 ②结构门=首次升档(2→3)须 S18穿刺修复上线+FIX结案keep+6h/24h双转正 ③资金门=20U地板数学解存在(pct0.11口径: mcp3≈255U/mcp5≈355U/mcp10≈800U;或与用户另议pct) ④空仓窗+预注册_exp(劣化回滚上一档)。mcp≥5 前须先落地相关性控制(meme池同涨同跌,并发=同暴露,08-01七币同窗爆亏实锤)。逐档只升1,不跳档。

- **08-03(交互)核心使命+20U废除直令**: 核心使命=稳定收益×高频×快速周转。**20U 单笔名义底线(07-22)废除**,代之以【费用覆盖红线】=单笔均净额(逐笔口径)≥2×来回手续费才允许提频/扩仓,未覆盖只修不扩(入 prompt 硬边界#1)。连锁: 并发阶梯资金门(255/355/800U)作废,升档门=业绩(30笔正净额)+结构(首升须S18+FIX keep+双转正)+费用覆盖;物理下限=交易所最小名义+引擎 conf_sizing_min_notional_usdt(现21,自主调);频率阶梯 cd 1800→900→600→300 同规则。旧 LADDER-MAINT 的 20U 复算逻辑随之退役。

- **08-03 16:5xZ(交互)mcp=3 用户直令确认+新平台事实**: 16:46Z 巡检发现 mcp 2→3 无 audit 变更;用户确认"是我改的,保持3"(平台UI手动)。→ ①mcp=3 为用户越阶直令,**18:1x 起各轮禁回滚**;并发阶梯自 3 起算,升 4 门槛照旧(S18上线+FIX结案keep+双转正+当档30笔正净额+费用覆盖) ②⚠️新平台事实(实锤): **平台 UI 改 config 不写 strategy audit**(15:1x轮核验mcp2→16:46Z mcp3,audit 00:18Z后零条目)——今后无主改动巡检先经 TG/交互问所有者,再评估回滚 ③风险知情注记: S18 穿刺修复未上线期间三槽并发=闪跌多一记穿刺敞口,用户已知情选择 | 权威登记 |

- **08-03 17:0xZ(交互)杠杆上限直令**: 硬边界 leverage [2,5]→**[2,20]**(用户直令;prompt 硬边界#2 待用户同步改行,改行前 cron 仍按 prompt 现值执行)。基线2不变;杠杆阶梯 2→3→5→8→12→20 逐档,每档门=当档≥30笔正净额+费用覆盖+预注册。**物理护栏(任何档强制)**: 清算距离≥1.5×SL距离 ⇒ lev ≤ 100/(3.75×池内max ATR%) ⇒ 升杠杆档必须同步收紧 max_atr_pct_for_trade(例: lev20→ATR%≤1.33/lev8→≤3.3/lev5→≤5.3);违反即该档不可用。风险知情: 20x 清算距≈5%,与 2.5×ATR 止损带重叠,高杠杆档=低波动币专用,用户已获物理冲突说明。

- **08-05 17:1xZ(交互)币池直令**: 用户平台UI改 `select_limit` 50→**100** 并已生效(GET /symbols live_feed=100;期间有重启,#22a cd150经LastEntryAt DB播种存活[源码L315-316]),config diff确认为唯一改动、_exp完好。按mcp=3先例=用户直令**禁回滚**。机制注记: select_limit仅启动时读(strategy_start.go:230);风险上限不变(mcp3/cd1800/pct0.11/lev2钉死敞口),扩的是S16择优候选集=提频不提险;新增尾部暴露(榜尾50币更高ATR%)由S18+#22a+max_atr门看护。**#22 _exp护栏②预注册修正(见数据前登记@17:1x)**: 池扩使基线+23.06/15笔失锚→主口径改**费后单笔均净≥+0.60(=0.75×80%)∧分段总净>0**,绝对额+18.4降为参考;①<150m再入=0不变(机械、与池无关)。监控三点:①klineHub 100路fallback(首2s样本0✓)②费覆单笔口径(榜尾币若拉穿<0.18硬边界#1只修不扩)③日志窗33s→~3s观测密度再降(铁律窗内未见≠零)。

- **08-05 17:4xZ(交互)三解锁直令**: owner连发"方向和冷却解锁"+"不要限制下单数量" → sides=[buy,sell]+cd1800→900+mcp3→**10(硬边界顶格)**当轮落地(S17代码gate同步apply移除,tpl477→534)。**取代**08-03并发阶梯逐档pacing与"mcp≥5前相关性控制"前置条款(owner自令自废);硬边界mcp≤10仍不可越。风险已书面呈报:逐币cd900×meme池相关性→burst可同窗多仓同向(08-01七币同窗爆亏同型),极端1h估≈−10%钱包;看护=6h/24h刹车线+_exp short单侧回刹线(-11.8U)+宽度门0.70拒逆势空+S14 CB,事后接管,owner知情。cd下一档600候owner再令或30笔档门。

- **08-05 17:4xZ(交互)代码入库+模板保留3版直令**: 策略代码不再只存DB——每次apply后上线代码推git仓库(confident-fermi分支 strategies/ 快照目录,git=全史档案);DB StrategyTemplate 只保留最新3版(=当前+2步rollback深度;retention patch M通道候部署)。owner索要此流程prompt块已交付(见08-05交互记录)。

- **08-06 02:4xZ(交互)冷却处置直令**: owner问"冷却可以去掉,在哪里改"→教练呈源码裁决(cooldown_sec=策略Python侧per-symbol信号冷却[码L1195读入/L1634闸,仅启动时载入],引擎Go无此字段;真闸=re150引擎门+置信度门0.70/0.60;昨晚7次同币再入gap 169-334m全贴150m栅栏=re150 binding实证)+推荐"先修后扩:S19与#22评判先行,cd随后一并动"→owner裁示**"按照你说的来"**。登记: ①cd900维持至条件窗 ②**cd→180预授权**(硬边界#2下限;owner原意"去掉",0越界不采),执行窗=S19上线∧#22评判(08-07~16:30Z)完∧费覆🟢的首空仓窗;届时费覆仍🔴→TG呈报owner再确认(先修后扩次序为本次裁示核心,预授权不自动豁免硬边界#1) ③取代08-05"cd下一档600候owner再令或30笔档门"条款 ④owner若UI自改仍属直令但请知会(平台UI不写audit)。→待落#24

- **08-06 09:2x-09:3xZ(交互)二次解锁直令**: owner令**"解锁"**+告知已UI自改策略内容("冷却时间等")→现拉diff实锤自改=**mcp 3→200 + re150→3**(cd1800/sides[buy]未动);**停机之谜解=owner编辑窗**(08:22-09:17 UI改配置,非平台异常;09:17硬边界#5 start无碍,start恰载入owner值生效)。登记:①mcp200/re3=owner直令禁回滚(mcp3先例);**mcp200>硬边界#2≤10张力**——物理上限≈14-15仓(avail×pct递减+21U名义地板),软件上限失效常态化,硬边界行待owner改文本,风险=同向批相关性×15仓,刹车线+S18/S19+批量连亏触发器看护②re3=owner直令**终止#22a实验**(T+17h机械0违规/软红,无数据裁决;族内再入病灶回归敞口由S18/S19部分围栏)③解锁包=sides[buy,sell]+cd180(去冷却直令下限;#24被吸收结案)——09:3x两次窗口尝试:第1次PATCH竞态拒(stop返回stopped但PATCH视角running=状态传播延迟,新平台事实候证),第2次MMT多单09:31:34抢窗→**转待落#26+本会话抢窗器+10:09Z cron双保险**④费覆红线🔴被owner短路(08-05先例);RECOVER2 _exp全文存cron会话scratchpad patch_recover2.json,规格见#26行。


- **08-09 15:2x-16:4xZ(交互)策略组+WS实时管理直令**: owner 四连令 ①"策略组模式,每策略适配一些币种;cron入口不变,变成优化多个策略+币种适配路由" ②"WS实时监控仓位,实时调整止盈止损委托/加仓/加杠杆"(本次交互豁免教练模式) ③"不只2个,要多个适配策略,按币种指标抽象几个原型,数量你定" ④"代码修改之后,再给我一个新的prompt"。当轮落地: 5载具编队+引擎补丁 claude/dev-strategy-group-ws@6dd3cf0(跨策略同币互斥闸+WS标记价加速[5s→1s]+赢家金字塔[roi≤0硬拒=马丁禁区],候部署)+v3 prompt交付 ops/prompt_v3_group.md。**裁决要点**: 加仓=只赢家金字塔(硬边界#4不动);中途加杠杆=不做(清算距离物理护栏冲突,金字塔替代);trailing/BE系7月已部署引擎能力config即生效(strategy_exit.go核实)。**追令'选币不能固定,要动态增删'→ops/route_pools.py 期望态对账器上线**(逐笔→期望池→实际池→rotate差分;生命周期发现→晋升→降级→隔离→rehab;阈值预注册于头注,改阈值走ROUTE _exp;churn≤4/轮隔离不限;互斥三层)。**owner拍板@17:0x**: ①fade启(越双转正门,自动保险=段6h净≤−6U停) ②lowvol删除('编制≠全员出动'owner知情)。**再追令@17:1x'每一个都要对不同的模板'→S24(trend)/S25(fade)/S26(breakout新内核)三连发当轮上线**(全部py_compile+6符号+字节diff+烟雾过;归档5ae2d15;弃用机制False-guard在场)。⚠️候证:apply期间main feed 86→100重播种(apply疑全局restart)〔rotate实战数字与apply细节见git 121e975本行〕

- **08-11 02:38Z(交互)停机直令·全组owner-gated**: owner令"先停止所有策略,目前已知在报错"→四载具全部stop并转**owner-gated**(硬边界#5的stopped→start自动恢复对全组悬停,恢复须owner明令)。执行实况:02:38首轮stop三空响应+fade522;02:40重试全线522(Cloudflare origin timeout);02:42-02:44连000=后端进程级不可达——"报错"本体疑似=后端宕/挂死,stop落地未确认,后台重试器在跑(结果见§7)。后端服务器处置权在owner(硬边界#6)。#42/#43随停机直令冻结。

- **08-12 13:4x-14:0xZ(交互)部分复飞直令·单载具canary**: owner UI start×4(≈13:40-41Z,无audit=平台UI事实;非BOOT RESTORE[bt29仍挂+四DB态原stopped])+会话直令"看起来读个策略有一些问题,先用一个策略跑"→裁决=**qt-trend-long单跑**(段真值n5/+1.06组内唯一正净+保证金0.15最小+3币池可观测+恰持BICO仓),main/fade/brk当轮停回。三停载具gating基础更新=本直令(勿auto-start,恢复须owner令;取代08-11停机直令的gating基础)。canary自动保险预注册:trend复飞段(自13:41 start)6h净≤−5U→立即stop本载具,组刹车照常叠加。trend _exp(FIX S24)评期重锚=累计运行时口径(段前~31h,余~17h或段n≥15先到)。owner所见"读个策略有问题"最可能=stop不生效(回声行挡停,已定案入§3)或bt29挂死观感;已TG请owner给报错原文。

- **08-12 22:19Z(交互)全组复飞直令**: owner令"启动全部策略"→config零无主改动复核+保证金0.71✓→start main/fade/brk(trend在跑),22:22四running+K线回流实证(池归属正确,main历史回放预热)。#43门执行前复核(第7读)=FAIL:24h组净−3.19<0→main带sides=[buy]latch复飞,门候双转正重来。四_exp评期以22:20重锚续跑累计口径。钱包252.88全组空仓启动。BOOT RESTORE提示:四DB态=running,owner重启后端将全组自动恢复。**同令并发会话(rv5cwj)补录@22:3x-22:5x**: 本直令双投递至两会话(§3新平台事实),另会话核验四running后补齐: ①trend未被start触及实证(22:17-22:24日志零预热行)=canary段13:41锚无扰 ②main复飞重播种feed100含11应隔离币(08-09同型)→**ROUTE热移除11→feed89 overlap=0**(信号层blacklist闸全程在防,零实仓风险) ③**对账器v1.2**(add路径排除他池在池币=硬边界#9生成层封死;起因=TUT闪崩mv6.34%命中brk入池条但TUT在trend池,v1.1自提名撞池) ④评期具体锚已写入§1.5各行(main 00:33Z最先到点) ⑤#45扩至四载具(running锁,本条+§1.5=重锚权威)。

## 1.5 策略组注册表（08-09 起;池归属与载具状态权威节;roster 有变当轮必更）

| 载具 | id | 原型 | 池 | 状态 | 门/备注 |
|---|---|---|---|---|---|
| main 通才 | 8eb182b6 | 通才(S18-S23栈) | auto·feed87@15:4x(bl15种子层含APR/BEAT=重启免疫达成@15:32;竞态v2后我方15:36补start再重播种[第6例]→热清v2全绿,详§7例行轮行;当日6次重播种全自愈) | running | pct0.08 sides=[buy,sell] long_premium=0.35 tpl567;**FIX闭卷@15:32**(臂1✓长侧−1.44零新亏20h/臂2✗组净恶化但按侧拆臂=空侧因,premium留0.35,劣化条款'回0.15'之诊断被侧拆证伪不机械执行,owner在场);_exp=**FIX-GEO(#49)@15:32:13Z**: trailing(act1.0ATR/cb1.2%)+BE(1.0ATR),TP4.5/SL2.5/premium不动;基线=72h实现赔率0.88:1/SL率40%/均净−0.73;keep=n≥30或48h先到时赔率≥1.2∧段均净改善;回滚=4键回现值(te:false/be:0/ta:2/cb:1.5);eval 08-16 15:30Z;RECOVER刹线6h空≤−8U继承;#11空侧gate=下一槽 |
| qt-trend-long | 827ffe8c | 趋势动量多 | ACE/TUT/BICO/COTI(feed4=种子4✓;aging_watch=TUT/BICO advisory@15:2x对账,COTI回want) | running | **tpl575(S24)** pct0.05 mcp3 buy trailing on;_exp v3常驻:**累计n32/+0.93转正**(15:12轮+5:ACE×3+BICO+0.27/−0.68净+1.05;6h n6/+1.32;48h窗ACE n12/+1.62扛旗∧trending);早结门=净>0✓但毛均净0.029<3×费0.084未达→continue;劣化−5U/canary(6h+1.32)未触;**终裁08-16 06:35Z**当值:净>0=keep(现值+0.93落此格,守住即keep);全史git+config._exp |
| qt-fade-short | 21519f1b | 冲高回落空 | 1000CAT/ARC/**BEAT**(+BEAT@12:26 ROUTE补灌达入池条;**种子3币写回✓@14:5x空仓窗stop→PATCH→start,重启免疫达成**;14:1x部署重启曾致BEAT掉池→main抢开14:27空单=归属main非fade首笔) | running@08-15 14:5x | **tpl576(S25)** pct0.04 mcp2 sell;_exp v2续:累计n8/−2.63(池含BEAT起可产样本;前12轮零成交根因=want.fade空,非载具故障);评期=累计n≥15或**08-16 12:50Z**(池补灌已先行,到点若仍n<15按证据不足格裁,停驶取舍届时议);自动保险6h≤−6U(0✓) |
| qt-breakout-follow | 2111f5f9 | 突破追动量(S26) | 无(孤儿已释:AKE/BTR/COOKIE/EDEN回main feed✓@15:5x;DYM未被当前选集选中=场外自由身) | **stopped@08-14 06:50Z(verdict-gated)·复活事件@08-15 15:36:发现running(audit无start条目=UI手点或新keepalive开机自恢复误复活DB-stopped载具,待owner证言),feed曾播回种子3币,零成交,15:38停机复位✓;若系keepalive→每次部署均将复活gated载具=边界候修** | _exp closed=段n15/−3.23≤−3U宽容线;诊断=trail0.8%回调帽截赢payoff倒挂(全文config._exp+git);复活=S27新原型周期(#47,门=组费覆🟢+新预注册+重ROUTE灌池;种子3币旧值勿直启);⚠️STAR空3闭+1飞行挂brk名=sid伪影(brk buy-only物理不可能开空=铁判据,实为main空侧;§3) |

- 隔离区(规则≤−4U∧n≥4): 4/CYS/TST/龙虾/BMT/BTW/H/**APR**(+APR@08-15 12:2x,48h n5/−7.54,#30确认1+候审1在列;热移除✓种子候重启窗;H隔离史存git)。**池列值=快照,权威=每轮对账器输出**。**对账器现版v1.3权威副本=quanty-ledger分支ops/route_pools.py@08-14(=334dae1内容;谱系61b1bb4←377c567←f970704;默认分支工作树只有v1.0初版——禁直接跑,实证2例错误plan)**:出池只认头注规则或隔离(失格币aging_watch);隔离表arg4;add互斥只排他专家池(v1.2误把main feed入排除集=晋升封死,COTI案实证修复);晋升币同轮移出main(封v1.1单轮重叠窗,执行序=先main remove后专家add);brk/lowvol出池阈值未注册前无自动出口(#42)。**arg4输入纪律(08-15 12:2x实证):隔离表必须传X/USDT形态——裸名norm后不match feed键,5隔离币漏出remove计划,当轮补刀rotate清除;代码零改动,属输入格式纪律**
- 保证金: Σ_running=main0.08×5+trend0.05×3+fade0.04×2=**0.63≤0.75✓**(08-14 14:4x复核,rotate不动pct/mcp)
- 互斥不变式: feed层@08-15 15:4x=main87/trend4/fade3,**overlap0全绿**(BICO镜像remove 15:32落地+热清v2去bl15全集)>种子层=期望态✓(main bl15@15:32/trend4币/fade3币,三载具重启免疫齐备)>引擎互斥闸✓live;aging=trend2(TUT/BICO)/fade2(1000CAT/ARC) advisory;重播种规律第6例(15:36我方start触发,自愈同型)〔前5例与伪影注记压缩,全文git 541a2bc〕
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
| S19 | 2026-08-06 | live | **短侧顺涨开空veto（S18镜像 gate7b）**：short 且[上一完成bar涨>0.6×ATR% 或 3bar累涨>1.0×ATR%]即拒；阈值内联不接config；依据=解锁段短穿刺簇9例/−16.7（hold6-31m,mv−3.5~−4.2%≈2.5×ATR快SL），ex穿刺段+6.5正；tpl563(hash b64fb6ce)；**裁决KEEP @08-06 16:4xZ**（15短达标[10:31→16:15]：应拦型0/15；全型快SL空4/−7.76 vs 基线9/−16.7 收窄53.5%≥50%过；诚实注记=时率口径仅−19%[基线10.5h vs 段6h]，但残余4例全属拉升语境型[3例CoinGecko 300m≥+17.6%实证/ON不可证]=设计外已由S20围栏[分型口径14:2x轮预登记非事后]；S19锚点永久保留） | `S19` |
| S20 | 2026-08-06 | live | **拉升语境拒空(gate7c,S19盲区补层)**：short且[300m累涨≥12% 或 180m累涨≥9%]即拒(绝对%阈值不×ATR——拉升是小时级绝对现象)；机制=7b只读closes[-1..-4]对3-5h拉升物理盲区；依据=postR2快SL空3例CoinGecko 5m实证入场300m全≥+17.6%(CTSI+37.7/TAKE1+52.4/ZBT+17.6),可证对照(慢亏BTW/TAKE2)≤+2.3%分离7.7×,TAKE1案180m=−3.81(顶部回撤仍死续腿)⇒主窗300m,ZBT案60m+0.02%=bar级全盲实证；tpl564(apply hash b4e8c650,归档c4656e5),预注册apply前commit见§4瘦身注；裁决=+24h初评/48h正式(对齐RECOVER2窗08-08 10:30Z):拉升型快SL空(hold<45m∧mv≤−2.8%∧入场300m≥12%)应=0,误杀审计=被拒币2h后走势+回测id20-25 A/B,劣化=re-apply tpl563归档；**16:4x初读T+1.7h**：应拦型0✓,postS20 6平+10.13(短流未断=无 blanket 拦截:TRADOOR/VIC正常过gate),回测护航6发全挂死(24h窗缺陷复现)→护航改由live逐笔独任 | `S20` |
| S21 | 2026-08-07 | live | **同向急再入veto(gate7d)**：影子平仓后<45m同向再入拒(反向反手保留)；依据=#29 postS20 20例/−20.34 wr20%(盈后腿8/−7.73与亏后腿12/−12.62双负→全veto,窄版不采)；执行门=(#20评判完∧#29 20/20)预注册达成@18:2x空仓窗；gate7e追跌拒空同draft**误杀审计未过暂缓**(见§4#30)；tpl565(apply hash 2cab9833/ctx 81b5cf37,归档999177d)；_exp在飞 eval 08-09 18:30Z或段+30笔；已知噪声=幻影影子假veto窗+restart清last_shadow_close(冷启动首影子平仓前无veto窗)；**裁决KEEP@08-08 14:1x 段n=37先到门**(五维:机械违规0/5[1漏拦S22修+3skew+1冷启动]/段均净+0.296 vs 基线−0.319/簇−20.34→+8.33/快SL占比40.3→35.1↓/反例AIOT+2.97在册类级不翻案;全文_exp.s21_verdict)；S21永久锚点 | `S21` |
| S22 | 2026-08-07 | live | **gate7d断供免疫补强(emit口径副闸+仪表修复)**：①同币同向距上次实际emit<45m即拒(last_signal_ts/dir盖戳,与bar投递无关;反向不限;择优落选不盖戳无误伤) ②统计行补fresh_reentry_block(影子)/fresh_reentry_block_emit双计数(原S21 metric③只写不读+600s清零=不可观测)；依据=AIOT 21:49空SL17秒→21:53:34同向再开gap4m漏拦实锤(两腿名义31.8/31.2U=avail×0.055×2×1.4策略单;源码演绎:引擎用信号绝对SL[resolveTPSLFromROI无pct原样返回]→实盘SL触发⇒bar高点必穿影子SL⇒bar若到达veto必拦;唯一可断前提=21:49-21:52 AIOT bar未被处理);tpl566(e525db4e,归档81d6e12);S21 _exp内s22_amend修订非新实验,段注记pre/postS22；**随S21裁决KEEP保留@08-08 14:1x**(postS22段31笔+9.25均+0.299) | `S22` |
| S24 | 2026-08-09 | live@trend fork | **trend定制(tpl575)**: 空头出口封闭(elif False档案化)+续势加分ls+0.10当3bar累涨∈[0.2,1.0]×ATR%(中继漏斗;0.70锚不动);S19/S20在场不可达 | `S24 ` |
| S25 | 2026-08-09 | live@fade fork | **fade定制(tpl576)**: 多头出口封闭(if False档案化)+RSI>75衰竭第二档ss+0.10(与S19/S20正交);S18在场不可达;0.60锚不动 | `S25 ` |
| S26 | 2026-08-09 | live@breakout fork | **breakout新内核(tpl577)**: score_breakout_detail整体替换均值回归核;30bar收盘破位base0.40+量能0.15+3bar动量0.15+EMA对齐0.15+余量0.10,blowoff帽1.2×ATR;趋稳/震荡/RSI极端/追高四gate False-guard档案化;detail键契约兼容 | `S26` |

下一个新锚点编号：**S27**(S23-S26已用)。⚠️锚点自S24起分fork谱系: main=S4..S23;trend=+S24;fade=+S25;breakout=+S26(其均值回归核档案化)。apply前grep按该载具fork谱系核验。原注记:(历史上 S10 曾存在于注释引述，编号不复用)。
静态必留 6 符号（v10 既有，与上表取并集）：`on_market_message` `_emit_signal` `_append_bar` `self.pub.publish` `_init_symbol_state` `_purge_idle_symbols`

## 3. 已确认机制

- **架构升级 v08-15 部署清单(owner 14:xx 通告,行为+源码双实证@14:4x)**: main分支87d88e6→f4848f8并入且已部署——①**引擎互斥闸live**(strategy_signal.go L581一仓一主,§1.5三层互斥第三层⚠️转✓)②**WS标记价守护live**(strategy_ws_guard.go+binance_markprice_hub.go,TP/SL反应5s→~1s)③**赢家金字塔live**(strategy_pyramid.go,默认关;roi≤0硬拒/SL重锚棘轮/TP原价;config键pyramid_enabled/trigger_roi默认6.0/add_frac0.5/max_adds1)④**DB一开仓一行**(open_key生成列+唯一索引8ba8754)+**账户级持仓收养去重**(③a/③b)→#18幻影行族预期归零(候观察)、#15脱管仓假设部署实证(FOLKS粉尘仓14:xx无closed行消失=收养清理)⑤**allowed_sides下单口复检**(latch类动作引擎级强化)+止损棘轮护栏+tpsl兜底+markprice复位⑥active行开始带真实strategy_id(BICO@trend实证;sid伪影修复中,跨轮确认后归因纪律可简化)。**未并入**: #37 logs?limit/?q(行为实证limit=300仍返100行)、#21 backtest看门狗、#35 CB持久化——dev分支仍候部署。**部署副作用**: 重启重播种(当日第3次,部署型)——main feed回100致12:2x热清作废,APR/BEAT无bl保护窗口暴露,main 14:27开BEAT空(路由违规残留,互斥闸使fade被让开无双仓事故);处置见08-15 14:4x事件轮。

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

- **[已确认 @08-06 04:1xZ · RECOVER verdict=rollback] 短侧穿刺簇=双向解锁段唯一净失血机制**：解锁段(08-05 17:45Z起)32平毛−10.17，其中严格短穿刺9例/−16.70（BTW/CYS/INX/HFT/CYS²/VIC/PTB/SYN/HEI，hold6-31m，mv−3.5~−4.2%≈2.5×ATR快SL=顺涨开空被上涨延续穿刺），ex穿刺段**+6.5正**；短侧非穿刺单wr健康（TAKE+2.56/ON+3.29/HEI+2.13）。RECOVER expect破2/5（24h总净−6.08未维持正+费覆均净−0.32🔴），且"short批量连亏"即回防触发器击发（1h 13短0胜/3h 19短wr16%）→预注册一步回防执行＋S19镜像veto同窗上线（tpl563）。short 24h−9.96未破−11.8线（84%耗用）；#22机械0违规；并发峰5/10。裁决=双向本身未证伪，缺的是S18短侧镜像；恢复阶梯=6h/24h双转正后cd优先于sides（§1 08-02直令）。
- **[已确认 @08-07 18:2xZ · #20 verdict=KEEP] hunger_tp 0.06→0.08 放大赢单腿成立**：段n=44主腿hunger域赢均2.552raw/2.696折(note20) payoff2.42 vs 基线2.262/1.411双超回滚线(2.0/1.2)且达标(2.60折/1.55)；赢单mv>4%九笔顶10.88%=旧3%mv封顶解除直证；段面−0.336/笔miss全源hold<45m快SL 17/−31.14(预登记隔离路径,hunger物理正交)→转S21依据；新代价=超时微赢6/+3.48(6-8%roi不再45m收割的衰变尾),ex-fast段+16.34/27=+0.605/笔仍强正；hunger 45/0.05/0.08=current基线；裁决全文存_exp.fix20_verdict。

### 平台事实

- **平仓撤单-2011竞态+补设竞态=无害自愈类(08-14 18:02定案)**: closeUSDMPosition流程=①撤关联TP/SL(失败仅记error**继续**)②全量撤该币全部委托(普通+条件)③市价平仓④order=nil再扫一遍(strategy_execution.go L379-417)⇒孤儿挂单被双重扫尾结构性兜死;-2011"Unknown order"=止损单已被交易所同秒触发消费;"补设失败sl would immediately trigger"=tpsl_monitor在飞迭代与平仓流竞态(strategy_tpsl_monitor.go L137),价已越线拒单正确。实证:US空18:02:28两error仍平−2.94✓、CAP多18:04:18一error仍平−1.10✓,零裸仓;CAP行挂main名=sid代管伪影已知类(实际trend开,closed行sid=827ffe8c)。计数看§6。
- **账户级position行sid=陈旧symbol→strategy映射伪影(已确认@08-14 09:xx)**: STAR空3笔挂brk名(brk buy-only物理不可能开空=铁判据)+BLUAI空挂fade名(fade feed仅2币无此币);实为main空侧(两币∈main feed87,45m hunger签名吻合,上轮空侧+2.04已含STAR=跨轮口径一致)。**归因权威=池归属纪律**,行sid仅参考。monitors(quick_trade/roi)DB驱动按行sid解析instance→错标仓由被标载具代管TP/SL(BLUAI由fade代管平仓−1.83@09:37实证=无害);风险注记=若载具间hunger/hold/tp/sl参数分叉,错标仓将按错参数被管——参数分叉改动前先核在飞仓sid。**第3例@08-14 21:02**: AKE多7m快SL−1.82挂brk名——brk stopped 14h且停机前必空仓=停机载具物理不可开新仓(铁判据第2形态),实为main长侧(AKE∈main feed since 15:5x)。
- **main(87币大池)stop→stopped状态过渡异步>30s,期间PATCH被manager拒(实证@08-14 09:41-09:44三连试)**: stop API即时返回stopped但strategies列表持续running≥30s;trend(4币池)同窗stop→PATCH→start 15s成功=池大小相关。教训:大池载具PATCH窗脚本=stop→轮询90s→PATCH→start,单试不连发(连发致排队start互相吞);3次重启全有主已计成本(CB清零)。
- **DELETE /strategies/:id/blacklist/:symbol路由对含斜杠币名404(gin UseRawPath未开,%2F不解码)**: 全币种皆含/USDT=接口整体不可达;黑名单改动唯一通路=stopped窗PATCH symbol_blacklist全量。候dev分支修复(非紧急)。
- **main stop被账户级在途持仓卡死(行为实锤3例,08-10 A/B定案;'stop迟滞≈池规模'假设证伪撤销)**: 同轮对照=06:52 fade持BTW时111s+轮询不落地 vs 06:59 BTW平仓账户全空后**2s落地**;前两例=08-09 00:2x两拒(BTW在飞)+#39镜像行(BMT平仓后8s过)。机制候证=DB侧validateStrategyCanStop镜像行计数把任一载具在途仓算到main(main config.symbols空串→交易所侧检查被跳过,源码strategy_lifecycle.go L233-268);**运维铁律=main重启窗必须全账户空仓;专家载具stop只看自身持仓**。

- **[升格@08-15 06:2x] 平台级无端全局重启重复发生(非apply非start API路径)**: 第2例=08-15 03:30-06:16窗(main feed89→100重播种;audit零API行;config完好含long_conf_premium=0.35);第1例=08-13 22:5x-00:18(机制排除法:feedSymbols仅rotate/start改写→必经进程重启;领先假设=owner后端重启/部署)。影响=清内存态(CB计数#35域/统计计数/rotate态),不清DB(config/种子/持仓);防御已固化=种子层同步(专家池重启免疫)+main漂移对账器每轮自愈;残余=评期内存计数断链(#34/#37域)。

**[v2 增补 @08-01 16:40Z]**
- **逐笔平仓 API 已验证存在**：`GET /api/positions?status=closed&hours=N&source=binance_only` → 入场/出场价、realized_pnl、open/close_time 全量（本轮 48h 拉到 72 笔）。旧结论"无逐笔明细"**作废**。归因主武器。
- **[新增 08-09 18:4xZ·实锤] 镜像行双记账**：专家载具开仓时,若该 symbol 同时在 main 的 live feed 内,DB 会给 main 也写一条同 qty 的 StrategyPosition open 行(fade BLUAI 1359 与 brk BMT 832 双双复现;`?status=open` 原始视图可见,`?status=active` 合并视图不可见)。实仓归属以"谁设置 TP/SL 日志"裁定(BMT=brk 18:46:31 实锤)。危害:①镜像 open 行卡死 main 的 stop(validateStrategyCanStop 按 DB 行计数)与 rotate remove(has_open_position)②归因/前端幻影(=#18 家族)③若两载具反向,单向持仓模式下真实净额合并事故。根修=引擎同币互斥闸(dev-strategy-group-ws 候部署,升急)。缓解=live feed 严格不相交(rotate 已修复)。
- **[新增 08-09 18:4xZ] stop 语义=入队异步**：POST /stop 回执 {"status":"stopped"} 仅=入队成功;真执行在单 stopWorker,失败只写策略 error 日志(如"has open positions"),API 无感。**stop 后必须 GET /api/strategies 轮询确认**,失败诊断=安静秒窗(:34-:59)发 stop 后 2s 拉 logs 抓 error 行(18:46 实证有效)。
- **[新增 08-09 18:4xZ] start 中途不感知 stop**：start 亦入队(startCh 单 worker),boot 完成无条件写回 running(lifecycle 源码+4 连复活实测);对 running 实例发 start=纯 no-op(不重启进程,S23 分段无害)。窗口操作纪律:先静置排干队列→单发 stop→轮询确认→PATCH→单发 start。
- **出场三层语义（源码核实）**：①策略 TP/SL(atr_tp/sl_mult×ATR，平台执行) ②饥饿模式(quick_trade_monitor.go，10s tick，持仓≥hunger_after_minutes 后首检 |roi|≥hunger_tp/sl_pct×100 即市价收割，roi=价格变动%×杠杆) ③max_hold_minutes 无条件平仓。②与①不匹配=已确认病灶（见亏损侧 08-01 条目）。
- **hold_distribution 只覆盖部分仓位**——死法分析以逐笔 API 为准。
- **backtest 接口可用**：`POST /api/strategies/:id/backtest`（async=true），大改 apply 后烟雾测试用。
- **[新增 08-02 15:11Z] ctx API 结构改版**：`paired_trades`→`trades_window`（键：count/win_count/win_rate_pct/realized_pnl/long_count/long_pnl/short_count/short_pnl/by_symbol；口径=fills 非配对，24h n58 vs 逐笔 24 对）；`.binance` 下 balance/available_balance/income_totals（含 **TRANSFER=入金检测直读字段**）/income_counts/open_positions。旧键名脚本全部需改。逐笔 API 不受影响仍为归因主武器。
- [速记] 监控盲区DB↔币安失同步: monitor只扫DB open行,行缺失→实仓脱管漂移(KOMA 29h/-15.13 n=1);#15 sweeper候选;US'第二例'证伪〔全文git dba6d19前史〕
- **[新增 08-02 06:11Z] max_hold 计时锚 = 币安 pos.OpenTime(updateTime)，饥饿模式计时锚 = 本地 open_time**（quick_trade_monitor.go L85-89 vs L103 源码核实）：币安 updateTime 会被仓位变动刷新 → 实际 hold 可超 max_hold_minutes（post-FIX 实例 PROM 89.8m/120.3m，均盈利良性）。hold>60m 非故障；死法分类时 60m+ 桶不可武断归为硬超时。
见 v10 附录C（stop 需空仓、apply 模板泄漏、Binance 直连 451、日志窗 ~100 条/几秒、`daily_pnl_7d` 停更等），不在此重复。
- **[新增 07-22 16:16Z] `balance_usdt` = 可用余额（不含持仓保证金）**：16:11Z 空仓读 151.00 → 16:18Z 双仓在持读 81.82，差额 69.2 ≈ 两仓保证金 70.3 − 浮盈 1.15，精确吻合。钱包总值 = balance_usdt + Σ(名义/lev)，07-22 全日恒 ~152U。推论：①此前各轮报告的"余额 118-120.7"均为可用余额，钱包总值一直 ~150-157U；②入金检测/充值信号进度必须用钱包总值口径；③20U 硬边界执行基数沿用保守口径（开仓时可用余额）不受影响。

- **[新增 08-02 21:10Z] logs 端点=慢查询非死亡**：33s 完成@90s 超时（25s 超时侧观测为 HTTP000×4 轮）；根因=strategy_logs 缺 (strategy_id,created_at) 复合索引（WHERE+ORDER BY 走 filesort）＋debug 档 ~15 行/秒≈130 万行/天无清扫无限增长。M 通道修复 d1af9d8 推 `claude/dev-logs-retention` 候部署（复合索引+created_at 索引+api_logs 同防护+每小时批量保留清扫默认 7 天 LOG_RETENTION_DAYS 可调）。**✓e60acc8 部署验证结案@08-03 19:0xZ**：/logs 实测 1.44s；保活体系齐备（崩溃自愈+心跳+开机自恢复+cron 3h 兜底），重启后策略免人工自动回 running；⚠️首启建索引期 HTTP 不监听数分钟=启动未完非故障（#17/#21 结论迁入）。
- **[新增 08-03 03:5xZ] closed·binance_only 重建=窗口边界相位移洞（源码裁决+双窗实证）**：closedPositionsFromBinance（positions_binance.go）FIFO 配对不播种窗口起点前的在持仓态 → 仓位跨窗口起点时其平仓 fill 被误当开仓，该 symbol 后续整链错位（平当开/方向翻/跨窗 pnl 错归），可致真实交易被吞。实证 US：48h 窗吞掉 02:09→02:19 真实 SL 单 −2.36（income by_symbol 有/positions API 无，当轮 10 币交叉核对唯一破例；US 08-01 03:00-03:30 空单恰跨 48h 窗起点 03:11=触发条件吻合）；168h 窗生成"57h/71h 空头漂移链"与 #25-33 轮 positionRisk 连续 open_pos=0 直接矛盾=纯伪影。裁决：**income by_symbol=pnl 真相源；逐笔死法分析每轮必须 by_symbol↔positions 交叉核对**（本轮 9/10 币 fee 级吻合）；分析侧缓解=拉 hours+2 再过滤末 48h；根修=候选#18。
- **[新增 08-03 18:2xZ] 代码 hash 双口径**：ctx `current_code_hash`=sha256(TrimSpace(code))；apply 返回 new_code_hash=sha256(原始请求串)——发送含尾 LF 时两值不同=正常非漂移（本轮 e4c7ab vs 8dcedd 实锤，取回代码字节级一致）。baseline_hash 用 ctx 口径 ✓（apply 侧同走 TrimSpace）。
- **[新增 08-03 03:5xZ] DB StrategyPosition 行=空壳+重复**：近期行 amt=0/avg_close=null/pnl 多 null，且每仓 1 真行+1-2 条开仓后 1-2s 即闭伪行（direction 有时空）——DB 口径禁用于归因，仅作 strategy_id 溯源；closed?source=db 无 hours 过滤=全史返回。
- **[新增 08-03 21:5xZ] data.binance.vision 日档=历史1m K线新数据源**：容器 451 仅封 api/fapi 主机，vision CDN 可达（08-01/08-02 futures/um/daily/klines 实测 HTTP200，9币18档全下齐）；日档 T+1 发布（当日 404）。用途=逐笔反事实差分重放（真实入场固定，只重放出场变体）；ATR 注意：vision 完整 1m 序列算出的 ATR ≠ 实盘策略缓存 ATR（缓存含 WS Fallback 缺口→TR 偏大→括号更宽），全路径模拟 5/16 笔幻影 SL/TP 实锤，故只可做差分（基线=实际出场零模型误差）。
- **[新增 08-03 21:5xZ] 引擎回测通道两缺陷（#20②矩阵为此转轨重放）**：①**window≥24h 挂死**=status running 永不完成不触看门狗（task13[5d]/task14[1d] 40min+ 零错误零成交；≤18h 秒级完成 n=4；看门狗只罩喂线循环，拉取与阻塞写不罩；无 cancel API，僵尸 python 进程候 backend 重启清[v2 启动清扫标 failed]）②**模拟入场饥饿**=冷启动零缓存（EMA/MACD/ATR 预热棒不足）+喂线仅 OHLCV（缺资金费率/多空比/多空加分项）→ 含实盘 EUL 14:18 开仓的 3h 高活跃窗模拟 0 成交=对入场路径系统性低估；回测仅可测出场机制（且需≤18h 窗），不可测入场频率/质量。修复=候选#21。
- **回测v1事故与三平台事实(08-02 16:0xZ 交互轮)**: ①演化策略自带socket版MiniRedis(live_code L26),从config redis_addr直连,模板stdin shim被类定义覆盖=无效; ②引擎信号过滤仅strategy_id无boot_id校验(strategy_dataflow.go L30-37)→任何持实盘id的进程可注入真信号; ③backtest任务行无错误落库+无看门狗→永久running(v1已修error列,v2加看门狗+启动清扫)。事故: v1回测3克隆连生产redis,15:56Z重启清除,实盘零损失零异常单。台账原则强化: 凡spawn策略子进程的新通道,先审redis_addr注入。 |

- **[速记·压缩@08-15 15:4x] 部署链核验(08-05)**: 回测v2/A/B/logs/保活≥fadc14a 在产;用户部署分支=main(cron严禁推);62cb2fd klineHub 已生效(0 fallback);单符号回测0成交限制维持(冷启动+缺跨币因子,#21);三角套利Phase1只读=owner新产品线与本策略资金无交互〔全文见git 541a2bc〕
- **[新增 08-05 17:5xZ] PATCH /strategies/:id/config = 浅合并语义（源码 PatchStrategyConfig 核实+实锤事故）**：请求体=直接字段 map（`{"cooldown_sec":900,...}`），逐键覆盖 current，**值 null=删键**；发 `{"config":"<json串>"}` 会被当成新增一个名叫 config 的垃圾键（本轮实锤一次并已 null 删除修复，期间三解锁字段未生效约 3min，空仓窗内零影响）。PUT /config 才是 UpdateConfigRequest{config:string} 整体替换语义。今后一律 PATCH 直接字段。
- **[新增 08-05 18:0xZ] 代码入库直令首轮执行**：strategies/quicktrade-8eb182b6/{tpl477,tpl534}.py+README(归档协议) 已 push cron 分支(85b6921,基于 main 36ffc50)；retention patch(apply 后 DB 每策略只留最新3版 auto 模板,四重护栏,TEMPLATE_RETENTION_KEEP 可调)已 push `claude/dev-template-retention` 候 owner merge+部署；owner 所索流程 prompt 已交付(见交互记录)。
- **[新增 08-06 02:4xZ] 冷却双层机制(源码裁决)**: config `cooldown_sec`=策略Python侧per-symbol信号冷却(L1195读入/L1634闸,仅启动时载入,择优落选不烧);引擎Go无此字段。引擎侧真闸=`symbol_reentry_cooldown_minutes`(canOpenSymbolByCooldown strategy_signal.go L337,双入场路径L652/strategy_position.go L68,LastEntryAt DB锚重启存活)。同币再入受max(两层)约束→re150在位时改cd_sec对再入零效,cd_sec仅在"发信号未成交"角落烧900s;跨币枪速卡在置信度门。改cd_sec需stop→start重载(同select_limit)。
- **[新增 08-05 18:2xZ] cron 触发器实测=每2h非3h**：本账户唯一 Routine=`quote_optimize`，cron `9 */2 * * *`（prompt 文本"每 3h"陈旧；owner 17:54:15Z 曾更新 trigger）；槽位分钟级投递延迟正常（18:09 槽实投 18:14）。首例同槽双覆盖实锤：owner 交互会话收尾以"cron轮18:0x"写台账（commit 18:09:42Z）＋真 cron 槽 18:14 投递（=18:2x 轮）。处置先例：双覆盖轮=轻量核验+HOLD+简版 TG，不重复长报；调整 cron/文本的决定权留 owner。下轮起槽位预期 20:09/22:09/…Z。
- **[新增 08-06 04:3xZ] apply=DB换绑+自带async restart（绕持仓保护）**：ApplyOptimization 不查运行态，事务换 template_id 后返回 `needs_restart:true,restart:"scheduled async"`——平台内部重启**有持仓也执行**（3仓在持实录），restart后自动回running载新码；stopped窗<30s，PATCH抢窗两拒（"cannot update config while strategy is running"=PATCH需stopped实证2次）。含义：①大改可先apply后候窗PATCH，代码上线不被持仓阻塞 ②restart清策略内存态照旧 ③ctx hash字段仍双口径（7c8c314d vs sha256=b64fb6ce，TrimSpace已知事实，字节比对为准）。


- **平台事实@08-06 14:4xZ**: ①`ctx.current_code_hash`≠sha256(current_code)（tpl563:ctx 7c8c314d vs apply b64fb6ce;tpl564:ctx 0f70eff5 vs apply b4e8c650;两代绑定代码经直diff=提交逐字节一致）→代码验证一律用current_code直diff,勿用ctx hash字段。②回测执行与live共享`/logs`流（[backtest strategy]前缀+fake redis顺序喂线,单币24h/1m≈10min+,回测期live日志窗被稀释——观测铁律窗内未见≠零加倍适用）。
- **[新增 08-06 20:3xZ] start历史回灌=200根/币（manager.go:1429 historyBars=200,historySyncLoop;rotate-in resync同路径）**：S20代码注释"~400根⇒重启后即时全功率"**有误**（MAX_BARS=400仅策略侧缓存上限）；重启/换池后300m支gate7c需再攒101根活bar（200+101=301）≈100min盲窗，180m支（需181根≤200）即时在线。实证=HFT 08-06 17:57空CG口径300m+16.55%≥12%应拦未拦-2.07（14:47重启+101min=16:28理论已复明→本案三机制候证：期现口径差[CG现货vs币安期货1m]/HFT晚rotate-in回灌不足/回灌失败,n=1不可裁）。gate放行不留日志→事后不可从策略侧复盘；定案需币安期货1m K线（vision日档T+1可取）。

- **[新增 08-07 22:5xZ] gate7d影子路径=bar投递依赖(首例漏拦AIOT裁决)**：影子平仓判定只在bar到达时执行(_update_shadow仅live bar调用)→断供/延迟分钟恰覆盖SL穿越价位时影子挂死,close口径veto静默失效。AIOT 21:49:34空开→21:49:51 SL(17s,mv−3.66%,wick型暴涨分钟内)→21:53:34同向再开gap4m未拦。排除法钉死(策略单尺寸双吻合/引擎absoluteSL/实盘SL已触发⇒bar到达则必拦)。S22补强=emit口径副闸(投递无关)。残余脆弱点登记:反向flip-flop链两口径均可绕(L→S→L第二个L,lsc与last_dir均被反向腿覆盖;无逐笔证据暂不行动)。
- **[新增 08-07 22:5xZ] apply baseline_hash=TrimSpace口径**：resolveCodeForOptimize对模板code做strings.TrimSpace后sha256=ctx.current_code原文hash(81b5cf37族)≠存储模板hash(尾换行,2cab9833族)。apply 409 baseline_race时先按TrimSpace口径重算再重试,勿盲目省略baseline_hash。

- **max_hold 时钟=币安 updateTime，可被资金费结算等事件重置（08-08 02:1x 源码+逐笔实锤）**: 适配层把 positionRisk.UpdateTime 映射为 pos.OpenTime（binance.go L1540-1542），quick_trade_monitor.go L84-89 计 max_hold(60m) 优先用币安钟，本地 open_time 仅在币安钟为零时兜底 → updateTime 被刷新（资金费结算/保证金变动）即重置 60m 时钟。证据: LA 84.7m(+0.98)/RIVER 120.2m(+1.45) 赢单超 60m 仍持（ACE 64.9m 边际=tick 延迟域）。风险有界: 饥饿层按源码注释仍用本地 open_time 计时，亏损仓 45m 后照常被 −5%roi 收割，滞留域仅 (−5%,+8%)roi 带。现净影响 +2.43 良性（赢单多跑）。判据: 亏损腿 hold>75m ≥3 例或单笔 ≥5U → M 通道修复（binanceOpen=min(币安,本地)）；赢单跑长=良性不动。KAITO 08-08 120m/−0.16=亏腿首例(1/3例·0.16/5U),伴粉尘挡窗自愈(14:54部分强平余0.1塞slot→15:54全清,M粉尘sweeper暂不候选)。
- **[新增 08-08 10:5xZ] 双会话抢窗双写首例（#32 落地 audit 归属注记）**：10:54 空仓窗被两会话抢窗器同窗双擒——audit 10:54:45.961Z=08:1x 会话 v3（stop 10:54:44→start 10:54:54）,10:54:50.895Z=10:1x 会话 v3b（§7 10:1x 行所记,后写者胜=终态 recover/carry 措辞）。两载荷语义同源（均执行 §5#32）,S21/S22 十二键双方保全,终态零冲突。机制：①PATCH=_exp 全量替换 last-writer-wins ②stop 对已停/start 对启动中幂等→双会话可各自"成功"互不感知 ③audit 两条均有主,此注即归属。风险边界：同源载荷无害;**异源载荷同窗竞写会静默丢先写改动**→跨会话互斥靠 §5 认领行声明抢窗器 armed 时限,后启会话见在飞认领先探 audit 再动手。

- **[新增 08-09 06:4xZ] ctx 两口径（源码核实 optimize_handlers.go）**: trades_window(data_source=binance)=buildTradesWindowFromBinance 打包，count=成交腿数（24h 194腿 vs 逐笔配对61笔，分批平仓一笔多腿），long_pnl+short_pnl≠realized_pnl 属口径差非bug；paired_trades 键仅 DB 源变体出现。avail 权威读径=ctx.binance.balance_usdt（ctx.account 无 balance 字段）。逐笔归因一律以 A 武器 closed48 配对行（滤无 realized_pnl 幻影行）为准。
- **[新增 08-09 09:3xZ] positions.realized_pnl=税前毛额（不含佣金/资金费,实锤）**: 24币逐一与 income 原始 REALIZED_PNL 比对 diff=0.000 精确吻合（CYS raw−12.371=closed−12.371,佣金−0.565 另在 by_symbol.commission;KAITO raw+9.539 vs net+10.536=资金费差）。含义: 历史费覆读数（均净 vs 2×来回费）实为【毛额比】,真实净额=毛额−佣金,字面'净额≥2×费'需毛额≥3×费。跨轮趋势可比性不受影响；今后 TG 双口径并报（毛额比+扣佣净额比）,红线判定从严=毛额<3×费即🔴。

- **[新增 08-10 00:4xZ] stop迟滞≈池规模（两次实测）**: main(85币池)stop回执后15s/44s两次不落地(status持续running,start-back吸收);同窗brk(3币池)stop 2s confirmed,5s原子完成。#39先例main 2s落地=非稳态。候选机制=控制事件在策略循环检查点消费,大池评估爆发饿死检查点。影响:main空仓PATCH窗不可靠,种子同步走#40多轮重试;不判stop API故障。

- **[新增 08-10 18:2xZ,证据升级 08-11 06:4xZ] closed行strategy_id归因**: 专家载具closed行盖sid始于**08-09 19:05**(72h窗127行实证:19:05前全空串);main行至今全空(CTSI 08-09 20:59/MUBARAK 08-10 01:53均空)→主口径=sid,空且属专家池币=按池归属回填,其余=main。⚠️两陷阱实锤@08-11: ①**空sid≠必main**——后端断网重启窗重建的专家行sid丢失(COOKIE 08-11 01:16 +0.35,交易所侧TP成交+重建,按池归brk) ②**段起点污染**——trend段曾误含段前行TUT 19:05+5.13(S24 _exp ts=19:30前)致读数+7.48虚高,真值n5/+1.06;fade曾n9混1笔段前;段评判=sid∧close_time≥_exp.ts双滤。

- **[新增 08-10 18:2xZ] 日志置信度=折扣后值(四例精确复算)**: 未触发信号/评估行的置信度=加分项和×低波动折扣0.8(0.65→0.52/0.75→0.60/0.95→0.76/0.70→0.56全吻合);过0.55基础线仍可被后级门拦(长锚0.70/ATR%<0.5硬滤/sides)。读日志勿把置信度当原始分;S24/25/26谱系继承同口径。

- **[新增 08-11 06:4xZ] 08-11停机事故定案(根因+四实锤)**: ①根因=宿主机DNS故障(容器172.17.0.6经100.100.100.100:53[Tailscale MagicDNS]解析fapi/fstream.binance.com全i/o timeout)→WS重连+REST fallback双断刷错("报错"本体);同窗Cloudflare 522→000=同一主机网络故障,非引擎bug——四载具日志除连接错外**零Python异常/panic** ②既有TCP连接不需DNS:klineHub部分live流断网中仍推送(main 02:52仍收K线) ③**交易所侧条件单兜底实证**:COOKIE多单00:57开,断网中01:16 TP在币安侧成交+0.35;后端恢复后从成交史重建该行(sid空) ④02:51 DNS自愈(fallback取数成功+live回流)→02:52:08-13 stop×4被上会话重试器打进,四日志窗同秒级终止=落地实证;**stop/start不写audit行**(audit最新仍为08-10 12:4x patch_config)。服务器处置权在owner(硬边界#6)。

- **[新增 08-12 06:3xZ] 回测挂死机制定案(bt29+源码)**: strategy_backtest.go 取数FetchHistoricalCandles(L~186)先于watchdog装载(L~360,预算=90s+30ms/根,7d窗≈6.6min)→取数阶段挂起=status=running永久无守护(bt7/8/10同型,源码注释自证"tasks 7/8/10 did exactly that");API无cancel端点(main.go仅POST创建/GET列表+详情)。bt29(7d COOKIE探针@08-11 06:33)running>24h定性挂死;不阻塞实盘/策略CRUD;owner下次重启后端清goroutine,DB行料残留running僵尸行。修复候选=fetch前置deadline或cancel端点(dev通道,低优先,列复飞之后)。

- **owner直令双投递→并发会话双执行(08-12 22:19-22:5x实证)**: "启动全部策略"同时到达两会话——A会话start×3+#43门复核+台账先推(cd4b082),B会话(rv5cwj)独立核验后补齐ROUTE−11+对账器v1.2,push被non-ff拒→fetch重放合并(本条即合并产物)。防护三层=①动作幂等(start对running=no-op;rotate remove不存在币=skip)②台账push非ff必重放禁force③**执行动作前fetch台账**自此升为必须步(A会话基线事故+B会话non-ff=同型双证)。
- **stop异步+回声行挡停(08-12源码+实证定案)**: stop API返回{"status":"stopped"}仅=请求入队(lifecycle.go StopStrategy→stopCh),真停由runStopWorker执行,失败只写策略error日志行(实证14:05:15"Strategy stop failed: strategy has open positions")。validateStrategyCanStop数DB StrategyPosition(strategy_id=self∧status=open),而账户级对账器(manager.go:791)把交易所仓位按最近订单归因回声补建到各platform-owner名下(main=owner1,专家=owner2,同一Binance账户)→任何载具持仓期间main必有open回声行→**main空仓窗=全组空仓窗**,普通stop必拒。出口=stop?force=true:仅跳过该校验,teardown完全相同(kill python+断feed+置stopped),无撤单不碰他载具仓(实证14:07:26 force停main成功,trend/BICO无扰)。回声行=幻影行同源(closed48滤realized_pnl历史原因);修正08-06"状态传播延迟"认知=当时更可能同为回声行挡停。
- **BOOT RESTORE开机自恢复(08-12源码定案)**: 后端重启自动拉起DB态running/starting策略(lifecycle.go RestoreRunningStrategies延5s);人为stop(DB=stopped)不触碰⇒trend单跑期间owner重启后端=trend自动恢复非无主start;挂死回测行(bt29类)重启无启动对账不自动清。?q=/limit logs过滤未部署实证@08-12(dev-logs-limit候部署;main日志100行窗≈1秒宽,error行需停机后或安静窗抓)。

- **对账器版本管理(08-14确认)**: 新cron容器工作树=默认分支→ops/route_pools.py只有v1.0初版,直接跑=错误plan(实证2例:08-13容器v1.0误提议拆trend/fade池;08-14误提议清空trend池+COTI越brk直入trend)。修根@08-14:权威副本入quanty-ledger分支ops/route_pools.py,每轮fetch台账即得现版;改对账器=ROUTE预注册,改后同步台账分支副本+§1.5谱系行。

## 4. 假设库·候选队列（v2 迁移注记 @08-01 16:40Z：本节与 §6 观察计数合并为【假设库】，内容全量保留；prompt v2 起执行门槛=逐笔证据标准[≥20 笔同型死法或机制落到源码行为]，旧 v12 Step 4.6c 门槛作历史参照）

| # | 类型 | 内容 | 依据 | 复现计数 | 状态 |
|---|---|---|---|---|---|
| 34 | S23候选 | 长侧首腿穿刺节流→S23影子观测版(逆bar cohort审计;enforcement门=A组n≥20且AL/WL wr分离≥15pp) | 过20门:48h首腿快SL L20/-37.62;批内同向节流证伪@08-09;跨段终账@08-13裁决轮: **A组19/20**=seg1 15(AL1/3 AS4/7 WL0/3 WS7/14@08-09 12:30)+seg2 3(@15:50)+08-13段1(AS0/1);AL vs WL=1/4 vs 0/3噪声方向暂反〔中检1-5全史与seg链见git 121e975 #34行〕 | 19/20·**冻结@08-13**(S23 _exp证据不足结案) | **解锁达成@08-15(#37已部署)**→下轮起q=IDLE&limit=2000回补跨段真值,A组补满20再裁;WS 21@wr33%线索归#30 |
| 35 | M通道 | **S14 CB状态持久化**（restart清内存计数株连修复；方案候选=①引擎侧enforcement+计数DB落库[LastEntryAt播种先例L315-316] ②cron落地前CB快照→config种子字段策略init读回） | §6 CB行判据履行:第二币种复现✓(CYS案:16:39restart清2连败→18:37第3败未触隔离→反事实窗[19:01-23:01]内再开2腿:20:23**−3.55**+23:11**+2.82**双向抵扣)+HFT前史3笔/−6.93;00:1x首现CB会拦住赢腿的反例 | n=2币/实现成本−7.66 | 候选(候部署窗,deploy队列积压4支;缓解=restart节流纪律在用;反事实双向入账纪律自此适用;经owner部署后生效) |
| 37 | M通道 | **logs端点 ?limit=(≤2000)+?q=消息子串过滤**（GetStrategyLogs 硬编码 Limit(100)→读端截断；DB 留存7d 数据齐全，统计行每60s在写——部署后可回溯拉全量 S23 计数史，`?q=统计汇总&limit=100`=100分钟计数历史） | 本轮实测14次轮询0命中统计行(10×8s+4散点)=整点评估爆发把行冲出百行窗<8s；S23评判(08-11)唯一 metric 读口被堵；只读端点零交易路径 | n/a | **已部署实证@08-15 15:37**(0aa150a并入;limit=500返500行✓;?q功能✓但统计行标签已变——新引擎为'IDLE'非'统计汇总',查询用q=IDLE;#34观测债回补通道就绪,下轮可拉limit=2000回溯) |
| 2 | E8 类 | `max_consecutive_entries_per_symbol` 3→2 | pct 升档后单币堆叠上限变肥（07-22 06:43Z 登记）；16:16Z 无亏损侧稳定分化支撑 | 0/2 | 候选 |
| 15 | M通道 | 脱管仓 sweeper：monitor 增"币安实仓∩DB 无 open 行→重收养/TG 告警"扫描 | KOMA 29h 漂移 −15.13（n=1）+源码 L48 机制已裁决+HEAD 1292 修复疑同根因（部署态未知） | 0/2·**FOLKS watch@08-15 12:1x**(active行amount0.1≈$0.2名义空,sid="",open11:24Z,48h无FOLKS closed行=来源不明非正常下单尺寸;|upnl|<0.01经济nil;下轮复核在管/消失/漂移再定性) | **部署实证@08-15**(③a/③b收养去重并入f4848f8;FOLKS粉尘仓无closed行消失=收养清理佐证;假设方向①引擎侧已由owner自行实现)→观察2-3轮零新漂移即结案 |
| 18 | M通道 | closedPositionsFromBinance 窗口边界播种修复：拉 fills 前先取窗口起点在持仓态（或多拉 2h 缓冲），链起点=平仓 fill 时正确接续而非误开新链 | §3 08-03 重建洞条目（机制已落源码行为=可行动级）；US 双窗实证；影响=归因主武器完整性+前端 closed 历史正确性；**08-08 18:1x清晰标本**:BTW 6真短链间5幻影long填隙(无realized_pnl字段/qty198/487/入场价=前链出场价);48h文件10幻影行,24h窗3行以0额混入(51→真48);分析纪律=逐笔先滤无pnl字段行;**08-09 18:4x 镜像行新形态**(open期双记账,详§3新条;幻影家族第3形态);**+1镜像例@08-15 12:2x**(ROBO 09:17→10:17整60m,entry=exit=0.01784,qty2101,无pnl字段,滤除✓) | n/a | **修复部署@08-15**(open_key唯一索引+entry去重并入f4848f8,窗口边界播种根因的DB层兜底)→幻影行族预期归零,滤行纪律保留观察2-3轮,归零即撤销简化分析 |
| 21 | M通道 | backtest 可用性修复包：①拉取/阻塞写纳入看门狗+cancel 端点+≥24h 挂死根因 ②喂线预热 preroll（窗前 N 根静默灌注）③缺失因子降级注记或占位推送 | §3 08-03 两缺陷条目（机制=源码级：看门狗 L361 只罩循环/喂线 payload 仅 OHLCV/模拟冷启动）；矩阵评测被迫转轨差分重放实锤；**08-06 16:3x +6例**：S20护航id20-25(24h窗)T+2h全running零成交零日志线=挂死签名v2下复现,①根因仍在;运维规则=烟雾测试只发≤18h窗 | n/a | 候选（候部署窗；分析侧 vision 重放替代不阻塞） |
| 3 | E8 类 | per-symbol 重入 gate（代码级，需新锚点 S17） | churn 残留；引擎不执行重入字段（见已确认机制）。16:16Z 注：AKE（churn 代表币）24h +5.06/12 已转盈利，紧迫性降 | 0/2 | 候选 |
| 5 | 研究 | TP/SL/max_hold 与 15-60m 桶关系 | 桶已翻正，优先级降 | n/a | 低优 |
| 6 | E7 类 | 热点∩池内维度加权（择优批中 trending 币） | 热点∩池∩盈利连续8轮链+反例COTI/AKE热而亏³=榜首逆信号〔压缩@08-09 15:2x,全文见git e93a79e〕;§6计数行为准 | 0/2 | 观察（依据弱化第4轮） |
| 7 | 观察 | CB 重犯加时（同币第 2 次隔离 ×2，S17 类） | CB throttle-not-eradicator 多币多轮观察;cb_consec_losses/cb_quarantine_min 均 config 可设(代码 line557-558);历史 RIF/BANK/BEAT/BULLA 全零新增归档 | 0/2 | 观察;判据=同币CB隔离期满重入再血达4.6c→升级;逐轮历史链〔压缩@08-07 16:1x,全文见git b1bd082〕 |
| 8 | 观测性 | `stats_log_interval_sec` 600→60（config-only，零交易路径影响） | 附录E 第 8 轮升级评估副产品（07-23 00:10Z）：统计行 600s 发射间隔 vs 16-42s 日志窗 → 单轮捕获率 ~3-7%，结构性不可观测，E4 槽满/S16 落选/CB 心跳/置信度带评判全被阻塞；改 60s 后单轮捕获率 ~25-50%，跨轮累计差分可用〔让过链15轮+压缩@08-07 07:1x,全文见git e767b9b〕 | n/a | 候选（占预算；候空仓窗+非 World B 轮+预算窗）  〔续延链压缩@08-07 07:1x,全文见git e767b9b;要点=stats盲23轮+,候flat窗∧非WorldB∧预算窗优先落〕
| 9 | E7 类 | short方向盈利机制固化(白名单小改:短侧TP/SL微调/S16 tie-break优先短侧) | 07-29两次登记两次跌出(4.6c复现协议0/2×2;07-31双读门槛不在场)〔判据链全史见git 121e975 #9行〕 | 0/2·**事实性过时@08-13注记** | 08-09策略组改制后short主战场归fade载具(S25);main短侧复议前提=#43 unlatch后新证据;复现协议条款保留 |

| 11 | E8 类 | short 方向跨币修补·**设计定稿@08-15 15:3x**: `breadth_max_for_short` null(引擎默认0.70)→**0.50**(config-only零代码;S13宽度门L1713既有veto路径:池内EMA上行占比>0.50拒空;与长侧breadth_min_for_long=0.50对称=regime分区;回滚=set null;_exp草案metric=main空侧24h n/net/wr+对照长侧,keep线=空侧24h净≥−8U∧均净改善于−0.541,劣化=≤−12U或n<5/24h窒息→回null) | 08-01登记0/2→**2/2@08-09**(24h short−15.20 n51跨16币同窗burst;门槛链git);15:12轮增证:FIX段空n23/−18.51 wr22跨8币在0.70门下全数放行=门未起regime保护作用(main统计行结构性不可观测,拦截计数不可直证;0.50收紧效果即_exp所测) | 2/2达成+段增证 | **下一槽候执行**(owner拍板#49几何包先行@15:3x;执行窗=#49评后[08-16 15:30Z或n≥30]主槽释放,或owner令提前;包已ready零设计欠账;注:#49若显著改善空侧亏损幅度,#11部署前须重算keep/劣化线基线) |
| 30 | S21类(代码gate) | 追跌空反弹拒空veto候选(gate7e,暂缓中): short且跌势陈旧拒;draft单维300m阈值族证伪@08-07(误杀24%>10%门);下一迭代=加第二判别维(距60m低点反弹幅/自低点时间) | 追跌型累计10例/−24.28+晨午簇候审〔审计全史git 121e975/42e4fc8/541a2bc〕 | **n=17/−38.94**+候审16·暂缓(最新APR#2确认@08-15 12:3x;APR#1源分辨率存疑不强标;跨币同型US/AIO/ROBO/VELVET在列=系统性;逐例链git 541a2bc) | 执行门=确认型≥20或owner核准;#11 regime gate若上线将覆盖池级同窗burst亚型,#30残余=个币新鲜度维,定标继续 |
| 47 | FLEET候选 | **S27突破追动量复活版**:入场核S26保留(30bar破位+量能推力+blowoff帽,wr60%段实证),出场重构=让赢家跑(trail回调≥0.5×池ATR%或trail激活后免45m硬帽);出池阈值同包注册(承#42:滚动n≥6∧净<0或在池7天零成交→回main) | S26段n15/−3.23评判@08-14:赢单被0.8%固定回调帽截(池ATR3.5-4.4%的1/5),亏单走满2.5ATR,EV/笔−0.22=出场结构性倒挂非信号伪;want.breakout候选池实时=APR/BEAT/COOKIE/EDEN/STORJ等 | n=15段档案 | 候选;门=组费覆🟢+新_exp预注册+ROUTE重灌池;壳/tpl577/git在位重启成本低 |
| 49 | 组共病(出场几何) | **出场几何倒挂重构**:main trailing_enabled+breakeven_trigger_atr(WS守护1s执行引擎已部署=配置即生效)±TP收进可达域;目标=实现赔率0.88→≥1.3,打平wr 53%→~43% | 72h审计n=96(15:2x轮):main(trail OFF)SL率40%/−49.67均−1.84,TP仅12%均+0.64(设计应+3.2),时间出场48%合计≈0,实现0.88:1 vs 设计1.8:1,wr34缺口−19pp;对照trend(trail ON)SL率14%/wr68 vs 打平58=+10pp组内唯一正缺口(注:trend兼有选币优势,非纯trailing归因);brk死因(trail帽截赢)同族 | n=96机制级 | **live@15:32:13Z**(内联全序落地;抢窗器v1教训=stop异步~10s沉降,PATCH须先验stopped再发——b32w1vc32曾1s后PATCH被拒空跑,已修流程;bl15同窗落地#48结案;评期08-16 15:30Z或n≥30) |


> 瘦身注 @08-07 18:3xZ：#29(同向急再入veto)已执行升级为live锚点**S21**删行——20/20到门+#20关账同轮达成执行门；规格与依据存§2 S21行+_exp+git 999177d系(分型前史git b1bd082)。下一候选编号 **#32**(#31已被刹车占用)。
> 瘦身注 @08-06 14:4xZ：#27(S20拉升语境拒空)已执行升级为live锚点删行——预注册门②同轮双中(第3例ZBT+累计−6.09≤−6),_exp全文与定标依据存apply前commit(git log quanty-ledger 08-06 14:2x「S20预注册」)+§2 S20行。下一候选编号 **#28**→已被§5待落占用,再下一编号 **#29**。



## 5. 待落队列（已决定、仅被持仓/平台锁阻塞的动作；空仓窗按 Step 2.5 逐项落地）

| # | 类型 | 内容（含完整意图） | 登记轮 | 状态 |
|---|---|---|---|---|


> 维护注 08-14 15:0xZ：#46 **落地结案删行**——HEI SL−1.99@15:02:39平仓开真空仓窗,后台抢窗器15:02:52-15:03:16全序完成(stop 10s落地→PATCH bl15→13[释COOKIE/DYM/BTR+锁CAP]→验证CAP入/长13/_exp完好→start running);重启重播种feed100(trend5币播回,信号层bl在防)→15:0x热rotate−5自愈feed95 overlap0;AKE/BTR/COOKIE/EDEN随释放回feed。竞态教训:前台哨兵与未死后台抢窗器同窗并行,多付1次start(15:05,无害)——**立规:一窗一执行器,启动前TaskList核对在跑任务**。待落队列清空。
> 维护注 08-14 07:0xZ：#42+#45 **结案删行**——#42:brk评判停机使breakout出池阈值失去对象(lowvol已删),阈值设计并入#47复活包必含项;#45:brk部分被评判closure PATCH取代(_exp verdict含revival警示+种子勿直启注记),main/trend/fade三部分前轮已✓。

## 6. 假设库·观察计数（v2 迁移注记 @08-01 16:40Z：并入假设库，与 §4 合称；跨轮累计；9 秒日志窗单次未观测 ≠ 零，以本节跨轮增量为准）

| 计数项 | 读数 | 更新轮 | 备注 |
|---|---|---|---|
| 平仓撤单竞态error观测(-2011/补设) | **3条@08-14 18:02-18:04(US×2+CAP×1,单一波动窗18:01-07三连SL,全自愈)** | 08-14 18:1x | 判据:复现≥3个独立窗或单窗≥5条→查撤单路径系统性延迟;机制定案§3;零处置 |
| S23逆bar影子观测(main _exp 08-08起) | **裁决=证据不足·结案@08-13 00:33Z**(A组n19<20门,AL1/3 vs WL0/3纯噪声不可裁,零事后改指标;S23纯观测零交易路径原样在场)〔压缩@08-14,全文见git 8897832〕 | 08-13 00:3x | 重开判窗=#37部署后重启观测段;逆bar族(#30/#34)冻结至可观测 |
| main无端重启重播种观测 | **2@08-15(03:30-06:16窗)→已升§3平台事实(预登记判据「复现→升」履行)** | 08-15 06:2x | 升格转速记;后续feed数每轮复核照旧,新例§3计数;第1例全文git |
| S19顺涨拒空veto累计 | **裁决KEEP@08-06 16:4x转速记**(15短达标:应拦型0/15;全型4/−7.76 vs 9/−16.7收窄53.5%过;残余全拉升型=S20域;数字与诚实注记存§2 S19行);后续新增"应拦型"(顺涨bar可见+快SL)即报警复核 | 08-06 16:4x | 裁决口径分型14:2x轮预登记非事后;劣化回滚未触发(apply回tpl534路径归档) |
| S20拉升语境拒空累计(post-apply) | **裁决KEEP+候审清零结案@08-07 10:1x转速记**(应拦确认0/44终;HFT候审=vision期货口径未拦=正确;期现分离实锤CG现货vs期货Δ≈4.8pp=S20用期货K线口径正确)〔全文git 541a2bc〕 | 08-07 10:1x | 后续:误杀审计(被拒币2h后实跌>2×ATR%)≥3例→回滚tpl563;新增应拦型即报警复核 |
| #20 hunger_tp0.08段观测(在飞_exp影子行) | **裁决KEEP@08-07 18:2x转速记**(n=44：主腿hunger域赢均2.552raw/2.696折 payoff2.42双超线；段负全源快SL簇17/−31.14=预登记隔离路径零事后改口；数字存§3新条+_exp.fix20_verdict；hunger_tp=0.08基线) | 08-07 18:2x | 后续hunger域健康度随S21段观测一体跟踪,不再单列 |
| S21急再入veto(post-apply)观测 | **裁决KEEP@08-08 14:1x转速记**(段n=37五维全过,存§2 S21行+_exp.s21_closure);时差孔2例(ACE)+滑漏类8腿净+9.90仍正=无失血不翻案〔压缩@08-09 12:xx,全文见git d1400b2〕 | 08-09 12:xx | 后续:急再入类段净转负≤−5U重启复核;12:30统计行急再入veto(影子/emit)=0/0在读✓ |
| 重启清CB熔断计数成本 | 速记:累计3笔/−6.93(HFT),CYS第二币复现✓→**已候选化#35@08-08 20:1x**,计数与推进在§4#35;restart节流纪律维持〔压缩@08-10,全文见git 2873d28〕 | 08-08 20:1x | 判据与逐例链见git |
| 追跌空反弹型快SL观测(三gate域外) | **已候选化#30@08-07 04:2x转速记**(7例/−17.30+RIVER亚型1达≥6门;新增BSB02:42+BTW03:24定标;新鲜度分离器实证见§4#30行;计数移#30) | 08-07 04:2x | 判据履行:≥6例→候选化✓;后续计数与设计推进在§4 #30行 |
| 长侧快SL穿刺(momentum彩票成本)观测 | **预登记判据成立→候选化#34@08-08 16:1x**(6h L净−1.95∧首腿L快SL7/−12.00≥6,CYS×3;计数与设计推进移§4#34行,本行转速记;前史存git) | 08-08 16:1x | 升级判据已履行;后续读数在#34;镜像拒多维持不采 |
| 统计汇总行未捕获连续轮数 | **08-13 00:1x轮罕见2拍成功**(00:18:01/00:26:03安静分钟捕获=当前段读数;评估爆发期100行窗仍仅覆~2s,跨段累计依旧不可得;**?q&limit第7验未部署**[limit300返100行,q过滤无效];08-14 18:2x第8验同(q=S23返未过滤100行));前史结构性不可捕结论维持〔压缩@08-13,全文见git 121e975〕 | 08-13 00:2x | 跨段求和纪律不变;#37七催(S23判窗重开的解锁条件) |
| 热点∩池内∧盈利连续轮数 | **重启1@08-10 18:2x(TUT回trending榜∩trend池,24h+6.52组最佳币;CYS/BEAT榜上但一在押一孤儿不动)** | 08-10 18:2x | 判据同前;n<30轶事级;隔离币上trending≠rehab信号(需7天无交易+画像翻转) |
| UTC 早晨段（~02-10Z）当日主盈段观测次数 | **5 维持＋反例2扩展（08-03 09:1xZ:02-09Z全段12笔−5.39,内穿刺7笔−16.16 vs 饥饿TP胜5笔+10.78=同段两机制对冲,时段本身非因子,穿刺归#19机制域）** | 08-03 09:1xZ | 单段n<30轶事级;晚间弱形态×2曾同型;判据=达4.6c亏损侧门槛才议时段premium〔压缩@08-03,全文见git bce1b1d〕 |
| pick_lose/槽满拒绝累计(合并压缩@08-12) | 不可判终态(07-23起零可判事件;E4/S16评判域已随mcp200/池改制退役) | 07-23 20:10Z | 复活判据=新升档议题需该计数时重建;全史git |
| 信号→成交转化失败累计 | **0（08-01 15:13Z:8s窗0触发=无可判事件;累计0）** | 07-30 06:03Z | 历轮全为门槛/硬过滤拦截(非转化事件),gate在线直证多次〔压缩@08-03,全文见git bce1b1d〕 |
| WS 断连/重连观测轮数 | **✅fallback归零确认(08-05 10:1xZ klineHub 62cb2fd部署生效;至08-07 20:16窗累计13清洁样本连续0 fallback,最新=20:16窗0/100条96/96币live;逐样本链存git 3dd071b)** | 08-05 10:1xZ | 全史自愈零评分流受阻,未升级;回归监控,复现fallback即报警〔压缩@08-07 20:2x,全文见git 3dd071b〕 |
| 单币失血观测·已归档集合(BTW/BULLA/TLM/BANK/SYN/BEAT/RIF/AKE/SOON/COTI/KOMA) | 全部 0 归档终态（各币逐轮链与幅度见 git 历史，压缩@08-08 02:1x，前快照 add4230） | 08-08 02:1x | 通用判据(BTW先例)=6h 新增(非滚存)亏损再现→该币重起算并核 4.6c 幅度;per-symbol=CB 域(S14)不拉黑;E2 long_thr0.70 看护多头再入;恢复升档旧计数已随 08-05 三解锁归档 |
| 穿刺亚型c(冲顶回落)post-S18观测 | 速记:S18裁决keep@08-05(预登记路径c型→升v2分支,零事后改口);strict穿刺2/−4.95零新增维持;#22a已由owner re3直令终止@08-06;HOME−1.98(08-06 12:01)c族候观察n=1 | 08-06 12:2x | 后续:新增strict穿刺或c族≥3例再议;判据与全史〔压缩@08-10,全文见git 2873d28〕 |
| long 方向双窗负共现轮数 | **E8跟踪T+42h读数后随FIX _exp主口径吸收（08-01 15:13Z:24h long −20.24/33 vs基线−37.07收窄45.4%;12h wr55.6%修复但payoff不对称）** | 07-29 02:16Z | E8 achieved已关账breadth0.50保留;long侧健康度随FIX评判与PREM释放观察一体跟踪,不再单列累计〔压缩@08-03,全文见git bce1b1d〕 |
| 15-60m 桶双窗负观测轮数 | **4(08-01 15:13Z双窗负共现;hold仅覆盖部分对=伪影降权)** | 07-27 02:11Z | 判据=跨轮复现且组n≥30按4.6c评估,双窗回正清零;n<30嵌套同源维持观察;逐轮历史链〔压缩@08-07 16:1x,全文见git b1bd082〕 |
| short 方向双窗负观测轮数 | **0维持·口径分裂注记(08-06 02:1xZ:fills 12h−2.59∧24h−2.59双负但逐笔配对24h short+0.31正[21笔wr48]=衡平不计共现;主失血已单列#23穿刺簇5例−9.78;回刹−11.8余量97%;幅度距4.6c −11.86远)** 前史:🔓解除@08-05 17:5xZ三解锁#11解冻;冻结期注记〔压缩@08-05,全文见git 6a93e93〕 | 08-06 02:1xZ | 判据:**新增**(非滚存)空头亏损事件再现且逼近4.6c门槛→评估;逐笔配对为主口径,fills口径负共现单独不起计 |

## 7. 运行日志（每轮一行，新行追加在表首）

| 时间(UTC) | 档位 | 五窗净额 1h/3h/6h/12h/24h | 世界 | 决策 | 备注 |
|---|---|---|---|---|---|
| 08-15 15:12-15:4xZ | 例行轮(max) | close@15:12快照: 1h+0.21(4)/3h−0.88(8)/6h−3.53(14,wr50)/12h−8.70(21,wr48 be69)/24h−20.37(45,wr42 be61=−19pp);15:30三平改观:VELVET空+8.39/ENSO−0.19/BICO−0.68→24h≈−12.9,组6h≈+4.0转正 | FGI34/BTC+0.54%/mcap+0.12%无降压;trending∩池=ACE·COTI(trend双热点)+WAL/COW(main) | ①例行0原子包(#49由交互会话在本轮中段上线,main槽被占;#11设计定稿转下一槽[§4]) ②ROUTE:对账器v1.3→BICO镜像remove落地15:32;**竞态v2**=我方stop/rotate与交互会话start/热清同窗互踩(main暗~4-6min,15:36我方补start)→start再触发重播种第6例→热清v2 feed100→87 overlap0全绿;**规则升级:开窗执行前先fetch台账最新commit时间戳,±40min内有交互会话活动则cron只做只读+rotate,不碰stop/start/PATCH** ③trend累计n32/+0.93转正 ④19:05Z send_later(trig_014KQE)重定向=#49金丝雀(trailing乱触发/下单报错→4键回滚不等评期) | 钱包≈225;死法@24h:中段SL9/−18.51+快SL6/−10.83主刀=main空侧(#49赔率重构+#11 regime gate双管候);RECOVER 6h空−5.54(VELVET后转正)未触;费覆组🔴−0.453@15:12口径;fade n0(BEAT RSI66-70空头分0.35未及0.55);audit=15:30:33/15:32:14两patch皆交互会话#49包,我方no_change不留痕,零无主;全文git |
| 08-15 15:2x-15:4xZ | 执行轮(owner直令'现在就修改') | — | — | **#49几何包live@15:32:13**(trailing act1.0/cb1.2%+BE1.0;FIX闭卷premium留0.35;bl15种子落地#48结案;主槽_exp=FIX-GEO评期明15:30Z);抢窗器v1停透竞态教训入#49行;post-restart热清feed100→85 | owner新令'高频多仓位稳定盈利'对质=费覆红线先行,提频阶梯候#49评过+费覆🟢,详对话与#49 |
| 08-15 15:1x-15:4xZ | 质疑对质轮(owner:'几个策略不如原先单策略强') | 台账git逐日采样07-28→08-15+closed168(近期行有丢=#18长窗形态,弃用) | — | **对质结论(教练规则4)**:①'未达预期'=证实(两时代皆净亏,主账不回避)②'单策略更强'=证伪——单策略段07-28..08-09日均≈−12.9U(含−39.04/−49.05/−27.42三重创日;+14.78强日次日即−39;08-04/05正日=禁空+pct0.11防御态所挣),组段08-10..08-15日均≈−6.7U(约一半;且08-11/12=后端宕机+owner全停令非组因,今日−22.67=组段最差)③专家载具6天总成本trend−0.12+fade−2.63+brk−3.23≈−6.0U=失血小头,main空侧=大头且病根早于组(#11注册于08-01单策略时代12h空−24.78)④组结构本身是治法:fade专业化空/brk自保险止损−3.23/#11今晚上膛 | 7d拉取实证hours=168丢近期行(08-14仅19行vs实测56)→era对比只用台账git采样;质疑登记完毕,owner决策点=A按管线打#11(推荐)/B收紧main空侧敞口/C回单载具(数据不支持) |
| 08-15 14:3x-15:0xZ | 事件轮(owner通告架构升级) | 未拉五窗(事件驱动非例行) | — | **部署实证**:f4848f8已上(互斥闸/WS守护/金字塔/收养去重/一开仓一行=✓live;#37未并入);**善后**:main热清11(APR优先,feed→89,BEAT/BICO持仓拒)+fade种子写回3币✓+BEAT空单归属修正=main抢开+§5#48登记;详§3§4§5+git 11a2546 | 升级解锁队列:#11>trend金字塔(门=费覆🟢+终裁后)>main trailing>sid归因简化;滤行/watch纪律保留观察期 |
| 08-15 12:18-12:4xZ | 例行轮(max) | close: 1h−0.17(3)/3h−1.88(7)/6h−6.45(11,wr36)/12h−6.87(18,wr44 be59=−15pp)/24h−22.67(42,wr38 be59=−21pp) | FGI34/BTC≈平/mcap+0.38%无降压;trending∩池=ACE(trend)+AKE/COW/LAB/ROBO(main) | ①ROUTE三动作:**APR隔离**(48h n5/−7.54达线)+重播种第5例自愈(main热清8+补5 feed100→87 overlap0)+**fade补灌BEAT**(48h nS3/+2.98wr67达入池条,want.fade 13轮首非空,12:26预热160bar实证) ②对账器arg4裸名教训登记(§1.5) ③#30定标+1=**17/−38.94**(APR#2确认/APR#1候审不强标,CG源矛盾) ④0原子包(三_exp槽占:FIX今19:00Z/trend明06:35Z/fade明12:50Z;#11队首候FIX评后窗释放main槽) | 钱包≈224.3;FIX段长n2/−1.44零增vs空n20/−15.13主刀;RECOVER−6.25未触;trend累n27/−0.12;死法快SL8/−14.44+中段SL8/−17.02主刀;费覆组🔴−0.540;audit零无主;全文git 1d92d72 |

> 瘦身注 08-15(15:0x事件轮)：更老运行日志行随轮裁剪,被裁轮要点均已存§1.5/§3/§4/_exp,全史见 git 历史。