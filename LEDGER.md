# QuantyTrade 改动台账（LEDGER）

> 权威分支：`quanty-ledger`（2026-07-22 12:19Z 由种子 `claude/jolly-bardeen-sz6d04` 引导上线）。
> 由 cron 运行维护，每轮必写。**瘦身协议 @08-01 17:35Z（用户直令：省 token）**：本文件保持精简工作集，全量历史永在 git（压缩前快照=29eb4c5）。纪律：§7 只保最近 10 行（新行≤800字符）；§6 每计数项只保最新读数；§5 只保 open 项+最近 2 条维护注；关闭候选/已落待落项直接删行；超长叙述以〔压缩〕标记截断。人工编辑请只增不删原则对 cron 瘦身豁免。
> 由 cron 运行维护，每轮必写；人工编辑请只增不删。协议：v12 七节制（2026-07-22 16:16Z 起升级；此前见 `docs/cron_prompt_v11_addendum.md` Step 7）。
> 策略：8eb182b6-ee74-4125-a602-f0a91f376432（tpl447 @2026-07-22）

## 1. 用户常备直令（权威登记处；prompt 内快照与此冲突时以本节为准）

- **prompt v3.1 写入@08-15 16:0x(owner令'评估prompt是否需要更新')**: 11处手术式对账——roster删brk/对账器CLI改v1.3真签名+arg4形态/互斥闸✓/logs limit&q✓/M部署清单/stop沉降10s纪律/长窗丢行纪律/幻影行部署注记/08-15足额单仓直令入快照。脱敏全文=ops/prompts/prompt_v3.1_20260815_redacted.txt;trigger=quote_optimize(trig_018CLR8XkCWmrgCfDarPeLBi);回滚=git上一版+update_trigger回写。

- **多仓位=足额单仓直令(2026-08-15 15:5x)**: owner原话要义——多仓位不是小仓,每仓按需足额(vol由引擎判),余额不足依次递减,无余额跳过等下次评估。**禁以缩pct换仓位数**(否决cron提出的0.08→0.05广度方案)。机制对应=conf_sizing(mult0.6-1.4按置信度)×percent_balance(逐仓自余额递减)×min_notional$21跳过——三段已实证在位,零改动;广度增长路径=信号门逐档(conf/长门)+池宽+载具数,仍按费覆红线门控。

1. **单笔名义 ≥20U**（2026-07-22 重校）：口径 = 余额×pct，不含杠杆。执行基准采保守口径 = 开仓时可用余额×pct，两槽最劣须 ≥20U（07-22 16:16Z 实测二槽 110.6×0.19=21.0 合规）。任何档位公式与此冲突以 20U 为准 ceil2 向上取整。
2. **全天开仓**（2026-07-19）：`entry_time_windows` 保持 `""`；恢复窗口属 🔴 提案须用户批准。
3. **max_concurrent_positions 基线 2**（2026-07-19）：升 3 只经 E4；降回 2 随时允许。
4. **引擎下单语义含杠杆**（2026-07-20）：notional = avail×pct×lev×mult，与币安滑杆一致；20U 度量口径不含杠杆，两口径并存。
5. **按置信度动态下单量**（2026-07-20）：引擎 conf_sizing 已实现（mult∈[0.6,1.4]、交易所名义地板 `conf_sizing_min_notional_usdt=21`）——策略代码勿双重实现（07-20 HANA 53.12U、07-22 AKE/MIRA mult≈1.4 实证）。
6. **充值信号常备令**（2026-07-20，长期有效）：触发=修复见效+12h/24h net双正+12h wr−be≥6pp → TG建议充至钱包250-300U。入金检测用钱包总值口径(balance_usdt+Σ持仓名义/lev),avail跳升≠入金;用户提前充值→当轮按新余额重算pct不视违规。两次到账已闭环(08-02 +148.14/08-08 +74.4→钱包295.7=区间顶),20U复算已随08-03直令退役;常备逻辑继续有效。全史(07-22校准/08-05首发3/3)git 766c7d9

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

- **08-11 02:38Z(交互)停机直令·全组owner-gated**: owner令全停(后端报错窗,Cloudflare 522/000);gating基础已被08-12两直令逐步取代;全文git 766c7d9

- **08-12 13:4x-14:0xZ(交互)部分复飞直令·单载具canary**: owner UI start×4+会话裁决trend单跑;canary保险(trend 6h≤−5U stop)后转§1.5常驻;被08-12 22:19全组复飞取代;全文git 766c7d9

- **08-12 22:19Z(交互)全组复飞直令**: owner令"启动全部策略"→start main/fade/brk(trend在跑),22:22四running;#43门执行前复核FAIL→main带sides=[buy]latch复飞;钱包252.88空仓启动。同令双投递并发会话(rv5cwj)补齐:trend未扰实证/main重播种ROUTE热移11→feed89/**对账器v1.2**(add排除他池币=硬边界#9生成层)/评期锚入§1.5/#45扩四载具。全文git cd4b082+766c7d9系

- **08-22 04:1x(交互)低波解锁优化直令**: owner质疑"感觉现在已经不开仓了,这不对啊"(7.4h零成交实证)→教练呈三层诊断(市场ATR萎缩×绝对阈值栈交集×费覆死循环)+分批方案→owner裁示**"现在开始优化"**=第一批双载具三值包当轮落地(费覆红线owner短路,08-05/08-06先例系)。落地: ①trend=连开闸off(null)+atr_discount_thr 1.0→0.5+long_conf_premium 0.149[#65修复] ②v2=min_atr 0.5→0.35+atr_discount_thr 0.5+short_conf_premium 0.049[#65修复],FLEET评期提前结案(管道PASS/盈利不可判饥饿)转低波适配EXP。**不动清单**: main零字段(0.90关门=#61c终裁保护)、无pct/lev/cd/mcp改动、旧fade保持gated。两EXP各自预注册劣化线n≥8∧净≤−4U回滚+载具保险继承(trend6h−5U/v2 6h−6U)。第二批候: S28-fade衰竭签名入场重写(08-24前后;S27已让渡#47突破复活)+评期最低样本流原则。

- **08-26 17:07Z(交互)提频直令**: owner live终端令**"增加频率"**。教练框架: 组瓶颈=main空头信号供给(cd180地板/mcp200/择优在位,门=唯一活杠杆);普查7.17h=0.600档411线/195episode/84币被浮点误杀(0.55+0.05=0.6000000000000001>canonical 0.6,历史零conf0.600成交) vs 0.56档=低波折扣档(tpl806 n16−1.00已证伪不重走)。落地: ①main scp 0.05→**0.049**(门0.599收0.600档;v2 08-22同款config epsilon先例→后tpl823入码永久化)+_exp预注册(评08-27 18Z或档n≥15;劣化线=档累净≤−3U或档n≥20且净<0立即回滚) ②ROUTE#52同窗执行(VELVET移v2)。**08-19'main两侧epsilon未修=批准现状'就此部分更新**: 短侧按本直令收档,多侧关闸(lcp0.35=#61c终裁)不动。0.56档不收=频率不以质量换。

## 1.5 策略组注册表（08-09 起;池归属与载具状态权威节;roster 有变当轮必更）

| 载具 | id | 原型 | 池 | 状态 | 门/备注 |
|---|---|---|---|---|---|
| main 通才 | 8eb182b6 | 通才(S18-S23栈) | auto·**feed83@08-26 18:2x(17:26全局重启再泄漏同14→热清14/14;两重启法医学见§6;#46史git)** | running | pct0.08 sides=[buy,sell] **bl19@08-26 17:1x(+VELVET ROUTE#52)** **scp=0.049@08-26(提频直令收0.600档)** **long_conf_premium=0.35(#61c rollback@08-19,门0.90=多头事实关闸)** tpl567;**trailing(act1.0/cb1.2%)+BE(1.0)=基线几何(#49KEEP)**;**_exp=open·EXP提频0.600档@08-26 17:1x(评08-27 18Z或档n≥15;劣化线档净≤−3U即滚回scp0.05;档n1=ARIA空18:10 conf0.6000→SL26m毛−0.83@21:1x读,距线2.17U;#61c史git 0940c62)**;RECOVER刹线6h空≤−8U继承;#11降级观察(§4) |
| qt-trend-long | 827ffe8c | 趋势动量多 | **TUT单币**(COTI出池@08-24 15:3x ROUTE#36触头注线;ACE隔离@08-22;史git f43756c/040d80d) | running | **tpl575(S24)** pct0.05 mcp3 buy trailing on;**_exp=closed·ROLLBACK#69终裁@08-25 09:24Z**(评期09:18Z先到段n11<16,段毛−0.083净≈−0.35不>0→按预注册回滚adt1.0/lcp0.15/mces3,stop-PATCH-start全链✓复读三值+symbols[TUT]+feed91/1/4零漂移✓;子段记录=COTI n6毛−0.52[拖累主源,已出池]/TUT-only n5毛+0.44净≈+0.34,金标准整段不受子段改判;**三次申请=churn禁区→trend低波解锁线关闭**,重开须新regime证据[TUT ATR%中位持续≥1.0或新机制维度]从零预注册;连带#65退场见§4#65;verdict全文config._exp+git);**常驻保险=滚动6h净≤−5U→stop(继承)**;aging_watch[TUT]advisory按对账器头注线(滚动nL≥6∧L净<0)独立看护;三值包全史git 8a1d995/f71d825/5df52d2/492779a |
| qt-fade-short-v2 | 7583727a | 冲高回落空(**tpl887/S28+S29**;谱系tpl576/S25→823) | HANA/COLLECT/MELANIA/BTR/**VELVET**(feed5=种子5✓@08-26 17:3x ROUTE#52入池+空仓窗种子写回+_exp按币拆分条款扩展;**MELANIA出池线触发但deferred@15:3x ROUTE#36**:nS7净−0.53证据全pre-S28[tpl806/823时代,S28段n0]=#57病理证据不可移植+移出=撤走S28/S29主标的;defer至_exp终裁重跑对账,风险界=保险6h−6U+劣化线;BTR晋升#35;MELANIA晋升08-22) | running | **S28衰竭签名直入上线@08-23 21:24Z**(#66修复,机制全文§2 S28行+git;附随#65 epsilon永久化;apply三复检✓);**_exp=open·EXP:S28·B延期已执行@08-25 21:18Z**(21Z评期到点按预注册预案B[owner三次TG无回复]:评期延至段n≥16或**08-28 21Z**先到,metric补终裁按入场路径拆分[签名域/常规],到期签名域仍n0自动转C停v2[FLEET预注册];checkpoint=段n4全常规路径[HANA×3 epsilon恰0.600:−0.173超时/−0.542饥饿/+0.773 TP+trail;**+BTR空20:57@08-26 conf0.65常规6m快赢+0.269**(RSI38布林中部=非签名;前1min RSI31.5超卖硬拦正常)],段毛+0.327段净≈+0.17,**签名域~72h n0→08-28 21Z auto-C倒计时~48h**;stop-poll1-PATCH-start全链✓复读eval_after+checkpoint+symbols4✓;二元/劣化线n≥8∧−4U/保险6h−6U不变;三笔epsilon详§6;预案史+n1/n2时刻git 830a46c/91b3276;段n2时代预案叙事(三选项/两次修正案/epsilon判死推理)全文git 91b3276/830a46c;**烟雾债清偿@08-25 00:2x**(task36显式18h窗completed 3min;根因=漏start_time默认7d窗,§3条+git 48ca3ce/12699f5));低波三值包ROLLBACK@08-23(组级知识:低波解锁不付费;git a2e845f);保险=6h净≤−6U停(继承) |
| ~~qt-fade-short~~ | 21519f1b | 停机@08-16劣化线 | — | stopped(gated,勿auto-start)·退役归档@08-21 | v2壳接任;**symbols=RETIRED/USDT占位@08-26 09:30(audit1773)封API层收养向量,bl5留作引擎层皮带**;**_exp=open·FIX v2评08-28 18Z**(**首例实弹pass@08-26 15:16Z**:main再开VELVET空→sid空未被收养,post-fix新行0;前FIX bl5终裁**FAIL**@08-26:09:08Z VELVET复发[main开仓sid被抢,活仓案],真向量=API层收养§3新条;#72修复=claude/dev-adoption-attribution@4385fc3候部署;bl5 FIX史audit1770+git);复活=新壳走FLEET(tpl576+S25存git);全史git 766c7d9/f43756c
| ~~qt-breakout-follow~~ | 2111f5f9 | owner删除@08-15 15:5x(评判FAIL史+S26+tpl577存git;#47复活=新壳FLEET) | — | deleted | ⚖️翻案@08-18:幽灵可验证成交=0;#53修复候部署=卫生项;详git 766c7d9+95e4c9f |

- 隔离区(规则≤−4U∧n≥4)【13】: 4/CYS/TST/龙虾/BMT/BTW/H/APR/AIO/BICO/BEAT/XNY/**ACE**(+ACE@08-22 12:22 n4/−4.40;详git 8a797ec)(+BEAT[n9/−4.19混合血统,main era仅−0.76 rehab须知]+XNY[n6/−4.23全main空]@08-17 13:0x;+BICO[n8/−4.05资金费磁铁§6结案]@08-17 03:2x;+AIO/APR/H等史git;逐条全文git bb3b883)。**池列值=快照,权威=每轮对账器输出**。**对账器现版v1.4权威副本=quanty-ledger分支ops/route_pools.py@08-22**(v1.4@08-22=roster遮蔽修复[running壳优先,阈值零改动];谱系v1.3←61b1bb4←377c567←f970704;v1.0默认分支禁跑;演进史git bb2e)。**arg4输入纪律(08-15实证):隔离表必须X/USDT形态**(裸名norm不match feed键→漏移)。出池只认头注规则或隔离,失格=aging_watch advisory;brk/lowvol出池阈值未注册(#42)前无自动出口。最近跑@08-26 21:1x(v1.4)=**ROUTE#54零差分(连2;83/5/1无泄漏免热清);#53热清史+#52执行史(VELVET晋升fade全链)见git 188af33/66048a9**(隔离/降级零触发;aging=trend[TUT]+fade4[BTR/COLLECT/HANA/MELANIA] advisory;bl_sync desired19=现bl19✓;#47门=费覆🟢连2形式但两轮同靠VELVET单笔[剥之+0.016回🔴]+brk篮子[BAS,HANA,KOMA,STAR,TAC,WTML]除VELVET外48h净≈0→判未熟,门判据补强BOOK见§7 15:1x行;bl_sync=desired19[+VELVET]随移池落地;#51零差分(连6)及#46热清史git 9957041/1ae847c/91b3276/830a46c)
- 保证金: Σ_running=main0.08×5+trend0.05×3+fadev2 0.04×2=**0.63≤0.75✓**(08-21 16:5x FLEET建壳重算;前史0.55见git)
- 互斥不变式: feed层@08-25 12:3x复核=main91/trend1(TUT)/fadev2 4**两两∩∅**(v2计数991→1356=365bar连续零重启;隔离13币零在feed✓);种子层bl=18(desired达成@#70);**退役壳自bl5=收养向量封闭**;重播种法医学详§3(签名=top回落~202;bl闸零违例);#20=bl币可直漏feed(交易闸级非feed级);#24/#23及#8-#19史git;引擎互斥闸✓live;**fade壳seed5与main auto理论重叠=停机态无违例**(不变式限定running载具;若owner UI复活fade须先重划池,互斥闸+TG示警兜底);隔离币零成交✓(复核@08-21 09:2x,48h窗隔离12币零平仓行)
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
| S22 | 2026-08-07 | live | **gate7d断供免疫补强(emit口径副闸+仪表修复)**：①同币同向距上次实际emit<45m即拒(last_signal_ts/dir盖戳,与bar投递无关;反向不限;择优落选不盖戳无误伤) ②统计行补fresh_reentry_block(影子)/fresh_reentry_block_emit双计数(原S21 metric③只写不读+600s清零=不可观测)；依据=AIOT 21:49空SL17秒→21:53:34同向再开gap4m漏拦实锤(两腿名义31.8/31.2U=avail×0.055×2×1.4策略单;源码演绎:引擎用信号绝对SL[resolveTPSLFromROI无pct原样返回]→实盘SL触发⇒bar高点必穿影子SL⇒bar若到达veto必拦;唯一可断前提=21:49-21:52 AIOT bar未被处理);tpl566(e525db4e,归档81d6e12);S21 _exp内s22_amend修订非新实验,段注记pre/postS22；**随S21裁决KEEP保留@08-08 14:1x**(postS22段31笔+9.25均+0.299) | `S22` |
| S24 | 2026-08-09 | live@trend fork | **trend定制(tpl575)**: 空头出口封闭(elif False档案化)+续势加分ls+0.10当3bar累涨∈[0.2,1.0]×ATR%(中继漏斗;0.70锚不动);S19/S20在场不可达 | `S24 ` |
| S25 | 2026-08-09 | live@fade fork | **fade定制(tpl576)**: 多头出口封闭(if False档案化)+RSI>75衰竭第二档ss+0.10(与S19/S20正交);S18在场不可达;0.60锚不动 | `S25 ` |
| S26 | 2026-08-09 | live@breakout fork | **breakout新内核(tpl577)**: score_breakout_detail整体替换均值回归核;30bar收盘破位base0.40+量能0.15+3bar动量0.15+EMA对齐0.15+余量0.10,blowoff帽1.2×ATR;趋稳/震荡/RSI极端/追高四gate False-guard档案化;detail键契约兼容 | `S26` |
| S28 | 2026-08-23 | live@fade-v2 fork | **衰竭签名直入(gate2旁路;#66修复;tpl823)**: short∧RSI>75∧价≥布林上轨∧末完成bar涨<0.3×ATR%时仅旁路"EMA非down+趋稳"一门;S19 3bar条款/S20/RSI<32/量能地板/ATR带/追空ext全保留;失速要件0.3×严于S19首条款0.6×=不接飞刀;评分门0.60另需≥1项独立空头确认(MACD死叉/资金费/多空比);附随#65 epsilon永久化(ss≥short_thr−1e-9);评判随v2 _exp二元 | `S28 ` |
| S29 | 2026-08-24 | live@fade-v2 fork | **S28签名域ATR地板降档(gate1;tpl887)**: 完整签名bar(short∧RSI>75∧价≥上轨∧末bar涨<0.3×ATR%)的gate1地板0.5→0.30,其余通道地板不动;依据=08-24 08:44-47Z MELANIA放量冲顶ss0.80/RSI79.8-88.4全灭于ATR%过低(0.30-0.35<0.5)+census 48h ATR%过低=ss≥0.60第一杀手378/791;机制=ATR回看滞后垂直拉升,gate1在S28旁路(gate2)之前=签名域机制性不可达;非低波解锁(HANA/COLLECT 0.11-0.26仍拒;tpl806 n16−1.00证伪之路不重走);内联不接config;评判并入v2 _exp二元 | `S29 ` |

下一个新锚点编号：**S30**(S23-S26/S28/S29已用;S27预留#47突破复活版)。⚠️锚点自S24起分fork谱系: main=S4..S23;trend=+S24;fade-v2=+S25+S28;breakout=+S26(其均值回归核档案化)。apply前grep按该载具fork谱系核验。原注记:(历史上 S10 曾存在于注释引述，编号不复用)。
静态必留 6 符号（v10 既有，与上表取并集）：`on_market_message` `_emit_signal` `_append_bar` `self.pub.publish` `_init_symbol_state` `_purge_idle_symbols`

## 3. 已确认机制
- **[新增 08-26 21:2x] -4411 TradFi-Perps 协议类币不可交易(平台事实)**: SNXX/USDT 08-25 14:31 触发信号→下单被binance -4411拒(需owner在币安签TradFi-Perps协议)且烧掉当批择优(候选失败=本批无标的)。SNXX现已随重播种出feed=零现患;含义: auto选币可能再选入此类币,再现→bl该币或TG owner签协议;择优失败烧批=频率隐性损耗。
- **[新增 08-26 03:2x] balance_usdt=availableBalance(源码定案)**: optimize_handlers.go L417-418 余额只暴露可用(注释原文'钱包=可用+冻结');binance.go L266-285=/fapi/v2/balance availableBalance→有仓时初始保证金被冻结不在此数。**含义: 刹车钱包基数=balance_usdt+Σ(名义/杠杆)±unrealized**;跨轮钱包对比必须补回保证金(08-26实证:169.20→154.86非亏损,=WTML空仓29.67/lev2冻结14.84)。
- **[新增 08-26 00:3x] bar计数器≠重启时钟(观察注)**: IDLE行top计数跨轮算术与已知stop/start事件对不上(main 519@00:26回推15:47Z启动 vs 19:10Z重启实锤;v2 387回推17:59Z vs 21:18Z预案B stop/start实锤;trend 517 vs 09:24 #69重启预期~901)——三载具推算互相矛盾⇒计数语义未明(疑hub级非订阅级),跨轮降数只证发生过某种reset,不可定位重启时刻;轮内+1/min连续性用法仍有效。重启法医学以feed宽度+audit+config为准。
- **回测默认7天窗陷阱(08-25 00:2x源码+双实证)**: POST /backtest 漏传 start_time→默认 now−7d(strategy_handlers.go:113-114);1m×7d 磨不完呈僵尸样。烟雾窗≤18h 必须显式传 start_time/end_time(task31=18h窗10min完 vs task34/35=7d窗数小时未完)。

- **[新增 08-24 12:4x] apply重启作用域=载具级(n=2定案)**: 08-23 tpl823与08-24 tpl887两次apply后,他载具feed(main89→89/90→90,trend2→2)与IDLE计数均连续=只重启被apply载具;prompt v3.1"疑全局级"废除;08-09全局重播种例归部署级路径。推论: apply不再制造main feed漂移,漂移主源=crash loop/部署重启。

- **[新增 08-20 06:3x] 引擎连开限制=同币同向连续开仓≤max_consecutive_entries_per_symbol(默认3;strategy_signal.go);错失/避损审计计数在§6;全文git 0940c62**
- **[新增 08-18 15:3x·压缩@08-20] main评分天花板0.76=门0.80不可达(#61空臂根因,日志+源码双证)**: 7因子加权×低波折扣0.8→打印分0.04网格顶档0.76;≥0.78 36h零出现⇒**prem上限≈0.21**;高波币(×0.6)永不过多门。**#61c浮点陷阱**: 原生档打印0.750内部=0.7499…<门0.55+0.20精确0.75被拒=多头主流量档挡死;**铁律=调门避开与档位打印值精确相等,留≥0.01余量**(prem取0.19非0.20先例)。全文+HOME更正史git 0940c62+95e4c9f。
- **[速记·压缩@08-15 19:2x] 架构升级f4848f8部署清单(08-15 14:xx owner通告,双实证)**: 跨策略同币互斥闸/WS标记价守护(TP-SL反应~1s)/赢家金字塔(roi≤0硬拒)/收养去重/一开仓一行/SL棘轮=✓live;#37 logs-limit未并入(limit=300仍返100);#15①引擎侧已实现;部署分支=main(owner自部署)〔全文git 45e2f36〕

- **[压缩@08-20] 收养竞态→一仓多行双守护互搏(#57,BEAT全证据链08-17)**: 账户级对账器把交易所仓回声收养到**非开仓载具**名下(fade停机壳3例实证=收养错标磁铁)→同仓两行两守护互搏(TP/SL重复cancel/replace)。**修复a451d32=收养归因按开仓者(候owner部署=#58候)**;部署前缓解=fade壳seed5+auto_symbols=false硬停;逐笔归因纪律=closed行sid存疑时按开仓者日志链裁决(§3归因方法论条)〔全证据链git bb3b883〕
- **[速记·压缩v2@08-19 09:3x] DELETE不杀进程竞态→幽灵载具(brk 2111f5f9)**: DeleteStrategy先删DB行,RemoveStrategy仅Status==running才Kill→竞态窗进程存活成幽灵(API 404不可控,仍收feed;三层出场+互斥闸仍罩);杀灭唯一通路=后端重启;#53修复fa93736候部署;**⚖️翻案@08-18 18:2x:幽灵可验证成交=0**("STAR空n5"实为main开,5/5开仓信号在main日志流+brk buy-only铁判据+trend日志对照)→降级卫生项;机制源码行号+时间线+翻案三支链=git 766c7d9系+95e4c9f

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

- **API层收养归属向量(08-26源码闭环)**: GET /api/positions?status=active 同步收养(positions_handlers.go)用findStrategyInstanceForSymbol静态config.symbols扫描定归属,无bl/无running检查,且优先于orderMeta(开仓订单真归属);entry行落库竞态窗(~1s)内前端/cron轮询先行→row sid=错壳。VELVET双案(08-24 18:12/08-26 09:08)实证;bl自拉黑只护引擎层findGuardStrategyInstance(UpdateStrategyConfig同步内存+SyncFromDB全量装载,停机壳bl在场却不经此路)→config级唯一有效封堵=symbols占位(清空不可行:isAllowedSymbol空值回退return true=匹配全域)。副作用:错归因行TP/SL被错壳config的ROI公式派生;entry流按本sid update打空→linked单簿记缺失;错归因活仓仍被quick_trade_monitor托管(按行sid解内存实例不查running,用错壳config的hunger/max_hold执行)=风险有界(08-24案3.5m即平/-0.24佐证)。修复=#72(4385fc3候部署)。

- **closed视图sid=owner域限定(源码+行为双证@08-21 18:1x)**: a451d32归因两路(order_id主径+±5min DB兜底,positions_binance.go)均按 `owner_id=uid` 过滤;main归owner1、专家载具归owner2=cron(uid2)查询域→**main行恒EMPTY=结构性非回归**(部署后新行FARTCOIN空17:31空sid,而main日志17:17开仓全链实证)。专家行#56 order_id精确归因healthy(COTI→trend sid实证)→fade-v2伪行检测器=锐利(带fade sid的行必真为其所开)。归因纪律不变:EMPTY行按池归属拆(池互斥⇒唯一);历史'幽灵sid'=兜底匹配到owner2已删载具DB行所致,同根因。
- **[新增 08-24 21:2x] closed(binance_only)行归属=两道回填,双空手⇒sid空串(源码定位)**: positions_binance.go L262-274 orderid匹配→L310-332 symbol+close_time±5min DB行兜底;48h实证main名下sid行=0、main池12行全空sid(trend/v2正常)=main DB行在crash loop下丢失或close_time分叉>5min→读侧归属缺口;交易/守护/income/池互斥归因全不受影响(VELVET 20:33开→20:37重启→21:10 trailing平+1.63全程受管);#72 post-fix空sid=修复生效预期签名(壳行不复存在→无从匹配);根治候=M通道close-sync收敛,优先级让位crash loop根因


- **logs ?q 大表超时形态＋EXIT_AUDIT 可用 (08-15 16:1x)**: main 日志≈344行/分,`?q&limit=2000` 在 main 上超时→返回**空体0字节**,极易误读为零匹配(trend 小表同查询正常返回`[]`或命中)。纪律: q 查 main 用 limit≤50;**空体=超时,`[]`才=零条**。EXIT_AUDIT 结构化出场审计已实证部署(reason∈breakeven_moved/trailing_moved/guard_sl/hunger_tp/hunger_sl/max_hold_timeout),死法分类可自 hold 启发式升级为 ground-truth 标签;#49金丝雀即以此实证 BE→trailing 全链(VELVET 15:37-39: BE@1.2ATR new_sl=entry−费缓冲→棘轮×8→回撤锁盈+0.65,atr=0.0129)。

- **平仓撤单-2011竞态+补设竞态=无害自愈**(order does not exist=已成交/已撤;引擎撤单失败继续平仓流,重复保护单被交易所-4046拒=幂等;全文git bb3b883)
- **[压缩@08-24] 账户级position行sid=陈旧symbol→strategy映射伪影(08-14定案)**: closed行sid可挂错载具(STAR空挂brk名,brk buy-only物理不可能=铁判据)→归因禁用行sid,主口径=池归属(§1.5)+方向可行性;08-15收养去重部署后新行盖真sid,存量旧行仍伪。全文git 2ce0fb4系
- **main(87币大池)stop→stopped状态过渡异步>30s,期间PATCH被manager拒(实证@08-14 09:41-09:44三连试)**: stop API即时返回stopped但strategies列表持续running≥30s;trend(4币池)同窗stop→PATCH→start 15s成功=池大小相关。教训:大池载具PATCH窗脚本=stop→轮询90s→PATCH→start,单试不连发(连发致排队start互相吞);3次重启全有主已计成本(CB清零)。
- **DELETE /strategies/:id/blacklist/:symbol路由对含斜杠币名404(gin UseRawPath未开,%2F不解码)**: 全币种皆含/USDT=接口整体不可达;黑名单改动唯一通路=stopped窗PATCH symbol_blacklist全量。候dev分支修复(非紧急)。
- **main stop被账户级在途持仓卡死(行为实证@08-22)**: stop判空仓以【账户级】现拉持仓为准,他载具在途仓可卡本载具stop→PATCH窗须全账户真空仓;全文git 8a797ec

- **[升格v2@08-23;再燃@08-24 06:3x] 平台级crash loop(非apply非start路径;修复权=owner硬边界#6)**: 检测=recv回落法(main IDLE top计数跌回~202=重启+回填200bar签名)。影响=清内存态(CB/统计/rotate态/择优攒批窗)不清DB(config/种子/持仓/LastEntryAt/挂单TP/SL);跨重启完好实证=hunger DB open_time锚(COLLECT精锚45m收割)+bl交易闸零违例。残余=①main feed泄漏(热清寿命≈≤1次restart) ②重启后≤2min突发开仓n=2(§6) ③评期内存计数断链。频率史:08-23 17次→**08-24全日≈16次(05:58-08:32×9峰值/14:29无端/16:30-17:18×5[启动完成实录16:30,16:49,16:50,17:17,17:18]/20:37×1[v2序号401@20:38锚])**;昼夜相关假设候证(20:37例落owner夜间段=首反例);TG连6轮报owner;逐例史git a2e845f/b505652/17251a9

**[v2 增补 @08-01 16:40Z]**
- **逐笔平仓 API 已验证存在**：`GET /api/positions?status=closed&hours=N&source=binance_only` → 入场/出场价、realized_pnl、open/close_time 全量（本轮 48h 拉到 72 笔）。旧结论"无逐笔明细"**作废**。归因主武器。
- **[压缩@08-24] 镜像行双记账(08-09实锤)**: 专家载具开仓且symbol同在main live feed→DB给main写同qty镜像open行(卡stop/rotate+归因幻影+反向净额合并险)。根修=引擎同币互斥闸✓部署08-15;缓解=live feed严格不相交(ROUTE每轮)。全文git 95e4c9f系
- **[新增 08-09 18:4xZ] stop 语义=入队异步**：POST /stop 回执 {"status":"stopped"} 仅=入队成功;真执行在单 stopWorker,失败只写策略 error 日志(如"has open positions"),API 无感。**stop 后必须 GET /api/strategies 轮询确认**,失败诊断=安静秒窗(:34-:59)发 stop 后 2s 拉 logs 抓 error 行(18:46 实证有效)。
- **[新增 08-09 18:4xZ] start 中途不感知 stop**：start 亦入队(startCh 单 worker),boot 完成无条件写回 running(lifecycle 源码+4 连复活实测);对 running 实例发 start=纯 no-op(不重启进程,S23 分段无害)。窗口操作纪律:先静置排干队列→单发 stop→轮询确认→PATCH→单发 start。
- **[速记·压缩@08-16 15:5x] stop=乐观回执+校验双洞(main实测不粘)**: 回执≠沉降轮询才真;sid=""仓隐形+auto池跳过交易所侧校验→main可带仓过闸(硬边界5被绕1次无损);stop后~40s可自动回running(机制未定=#52);**程序v2=自查空仓→stop→1-2s紧循环PATCH竞速→start→验bl**;全文+18:27全录git ffebcb4系
- **出场三层语义（源码核实）**：①策略 TP/SL(atr_tp/sl_mult×ATR，平台执行) ②饥饿模式(quick_trade_monitor.go，10s tick，持仓≥hunger_after_minutes 后首检 |roi|≥hunger_tp/sl_pct×100 即市价收割，roi=价格变动%×杠杆) ③max_hold_minutes 无条件平仓。②与①不匹配=已确认病灶（见亏损侧 08-01 条目）。
- **hold_distribution 只覆盖部分仓位**——死法分析以逐笔 API 为准。
- **backtest 接口可用**：`POST /api/strategies/:id/backtest`（async=true），大改 apply 后烟雾测试用。
- **[新增 08-02 15:11Z] ctx API 结构改版**：`paired_trades`→`trades_window`（键：count/win_count/win_rate_pct/realized_pnl/long_count/long_pnl/short_count/short_pnl/by_symbol；口径=fills 非配对，24h n58 vs 逐笔 24 对）；`.binance` 下 balance/available_balance/income_totals（含 **TRANSFER=入金检测直读字段**）/income_counts/open_positions。旧键名脚本全部需改。逐笔 API 不受影响仍为归因主武器。
- [速记] 监控盲区DB↔币安失同步: monitor只扫DB open行,行缺失→实仓脱管漂移(KOMA 29h/-15.13 n=1);#15 sweeper候选;US'第二例'证伪〔全文git dba6d19前史〕
- **[新增 08-02 06:11Z] max_hold 计时锚 = 币安 pos.OpenTime(updateTime)，饥饿模式计时锚 = 本地 open_time**（quick_trade_monitor.go L85-89 vs L103 源码核实）：币安 updateTime 会被仓位变动刷新 → 实际 hold 可超 max_hold_minutes（post-FIX 实例 PROM 89.8m/120.3m，均盈利良性）。hold>60m 非故障；死法分类时 60m+ 桶不可武断归为硬超时。
见 v10 附录C（stop 需空仓、apply 模板泄漏、Binance 直连 451、日志窗 ~100 条/几秒、`daily_pnl_7d` 停更等），不在此重复。
- **[新增 07-22 16:16Z] `balance_usdt` = 可用余额（不含持仓保证金）**：16:11Z 空仓读 151.00 → 16:18Z 双仓在持读 81.82，差额 69.2 ≈ 两仓保证金 70.3 − 浮盈 1.15，精确吻合。钱包总值 = balance_usdt + Σ(名义/lev)，07-22 全日恒 ~152U。推论：①此前各轮报告的"余额 118-120.7"均为可用余额，钱包总值一直 ~150-157U；②入金检测/充值信号进度必须用钱包总值口径；③20U 硬边界执行基数沿用保守口径（开仓时可用余额）不受影响。

- **[结案压缩] logs端点慢查询→索引+保留清扫已部署验证@08-03(实测1.44s;保活体系齐备;首启建索引期HTTP不监听数分钟=非故障)**。全文git 0980231前史。
- **[压缩@08-20] closed·binance_only重建=窗口边界相位移洞(08-03源码+双窗实证)**: FIFO配对不播种窗口起点前在持仓→跨窗起点仓整链错位(平当开/方向翻),可吞真单(US −2.36实证;168h漂移链=纯伪影)。**纪律: income by_symbol=pnl真相源,逐笔死法每轮by_symbol↔positions交叉核对;分析侧拉hours+2再滤末48h;根修=#18**〔全文git 0980231前〕
- **[新增 08-03 18:2xZ] 代码 hash 双口径**：ctx `current_code_hash`=sha256(TrimSpace(code))；apply 返回 new_code_hash=sha256(原始请求串)——发送含尾 LF 时两值不同=正常非漂移（本轮 e4c7ab vs 8dcedd 实锤，取回代码字节级一致）。baseline_hash 用 ctx 口径 ✓（apply 侧同走 TrimSpace）。
- **[新增 08-03 03:5xZ] DB StrategyPosition 行=空壳+重复**：近期行 amt=0/avg_close=null/pnl 多 null，且每仓 1 真行+1-2 条开仓后 1-2s 即闭伪行（direction 有时空）——DB 口径禁用于归因，仅作 strategy_id 溯源；closed?source=db 无 hours 过滤=全史返回。
- **[新增 08-03 21:5xZ] data.binance.vision 日档=历史1m K线新数据源**：容器 451 仅封 api/fapi 主机，vision CDN 可达（08-01/08-02 futures/um/daily/klines 实测 HTTP200，9币18档全下齐）；日档 T+1 发布（当日 404）。用途=逐笔反事实差分重放（真实入场固定，只重放出场变体）；ATR 注意：vision 完整 1m 序列算出的 ATR ≠ 实盘策略缓存 ATR（缓存含 WS Fallback 缺口→TR 偏大→括号更宽），全路径模拟 5/16 笔幻影 SL/TP 实锤，故只可做差分（基线=实际出场零模型误差）。
- **[压缩] 回测通道两缺陷(08-03)**: ①window≥24h挂死(后经08-12定案条揭机制) ②模拟入场饥饿=冷启动零缓存+喂线仅OHLCV→系统性低估入场;烟雾标准=完成不崩非成交数;修复候选#21。全文git 0980231前史。
- **[压缩] 回测v1事故三平台事实(08-02)**: 演化策略socket版MiniRedis直连生产redis事故+信号过滤无boot_id校验+backtest无看门狗;原则=凡spawn策略子进程先审redis_addr注入。全文git 0980231前史。

- **[速记·压缩@08-15 15:4x] 部署链核验(08-05)**: 回测v2/A/B/logs/保活≥fadc14a 在产;用户部署分支=main(cron严禁推);62cb2fd klineHub 已生效(0 fallback);单符号回测0成交限制维持(冷启动+缺跨币因子,#21);三角套利Phase1只读=owner新产品线与本策略资金无交互〔全文见git 541a2bc〕
- **[新增 08-05 17:5xZ] PATCH /strategies/:id/config = 浅合并语义（源码 PatchStrategyConfig 核实+实锤事故）**：请求体=直接字段 map（`{"cooldown_sec":900,...}`），逐键覆盖 current，**值 null=删键**；发 `{"config":...}` 包裹体=垃圾键静默失败(首锤08-05已修)。PUT /config 才是整体替换。一律 PATCH 平铺字段。**二次实锤@08-22 21:2x(v2 _exp延评PATCH)**: 嵌套`{"config":{...}}`同样成垃圾键且返回patched无警告=目标键未更新的静默失败;平铺体+null删键修复,空仓窗零影响;纪律追加=①动手PATCH前先查本条②patched≠落库,复读必须验目标键新值非仅status。
- **[新增 08-05 18:0xZ] 代码入库直令首轮执行**：strategies/quicktrade-8eb182b6/{tpl477,tpl534}.py+README(归档协议) 已 push cron 分支(85b6921,基于 main 36ffc50)；retention patch(apply 后 DB 每策略只留最新3版 auto 模板,四重护栏,TEMPLATE_RETENTION_KEEP 可调)已 push `claude/dev-template-retention` 候 owner merge+部署；owner 所索流程 prompt 已交付(见交互记录)。
- **[新增 08-06 02:4xZ] 冷却双层机制(源码裁决)**: cd_sec=Python侧per-symbol信号冷却(仅启动载入);引擎真闸=symbol_reentry_cooldown_minutes(strategy_signal.go L337,LastEntryAt DB锚重启存活);同币再入受max(两层)→re150在位时改cd_sec零效;改cd_sec需stop→start。详git 0980231前史。
- **[新增 08-05 18:2xZ] cron 触发器实测=每2h非3h**：本账户唯一 Routine=`quote_optimize`，cron `9 */2 * * *`（prompt 文本"每 3h"陈旧；owner 17:54:15Z 曾更新 trigger）；槽位分钟级投递延迟正常（18:09 槽实投 18:14）。首例同槽双覆盖实锤：owner 交互会话收尾以"cron轮18:0x"写台账（commit 18:09:42Z）＋真 cron 槽 18:14 投递（=18:2x 轮）。处置先例：双覆盖轮=轻量核验+HOLD+简版 TG，不重复长报；调整 cron/文本的决定权留 owner。下轮起槽位预期 20:09/22:09/…Z。
- **[新增 08-06 04:3xZ] apply=DB换绑+自带async restart（绕持仓保护）**：ApplyOptimization 不查运行态，事务换 template_id 后返回 `needs_restart:true,restart:"scheduled async"`——平台内部重启**有持仓也执行**（3仓在持实录），restart后自动回running载新码；stopped窗<30s，PATCH抢窗两拒（"cannot update config while strategy is running"=PATCH需stopped实证2次）。含义：①大改可先apply后候窗PATCH，代码上线不被持仓阻塞 ②restart清策略内存态照旧 ③ctx hash字段仍双口径（7c8c314d vs sha256=b64fb6ce，TrimSpace已知事实，字节比对为准）。


- **平台事实@08-06 14:4xZ**: ①`ctx.current_code_hash`≠sha256(current_code)（tpl563:ctx 7c8c314d vs apply b64fb6ce;tpl564:ctx 0f70eff5 vs apply b4e8c650;两代绑定代码经直diff=提交逐字节一致）→代码验证一律用current_code直diff,勿用ctx hash字段。②回测执行与live共享`/logs`流（[backtest strategy]前缀+fake redis顺序喂线,单币24h/1m≈10min+,回测期live日志窗被稀释——观测铁律窗内未见≠零加倍适用）。
- **[新增 08-06 20:3xZ] start历史回灌=200根/币(manager.go:1429,rotate-in resync同路径)**: S20注释"~400根即时全功率"有误(MAX_BARS=400仅缓存上限);300m支gate需再攒101根活bar≈100min盲窗,180m支即时在线;gate放行不留日志→事后不可复盘;定案需币安期货1m K线(vision T+1)。HFT应拦未拦案全文git 0980231前史。

- **[新增 08-07 22:5xZ] gate7d影子路径=bar投递依赖(首例漏拦AIOT裁决)**：影子平仓判定只在bar到达时执行(_update_shadow仅live bar调用)→断供/延迟分钟恰覆盖SL穿越价位时影子挂死,close口径veto静默失效。AIOT 21:49:34空开→21:49:51 SL(17s,mv−3.66%,wick型暴涨分钟内)→21:53:34同向再开gap4m未拦。排除法钉死(策略单尺寸双吻合/引擎absoluteSL/实盘SL已触发⇒bar到达则必拦)。S22补强=emit口径副闸(投递无关)。残余脆弱点登记:反向flip-flop链两口径均可绕(L→S→L第二个L,lsc与last_dir均被反向腿覆盖;无逐笔证据暂不行动)。
- **[新增 08-07 22:5xZ] apply baseline_hash=TrimSpace口径**：resolveCodeForOptimize对模板code做strings.TrimSpace后sha256=ctx.current_code原文hash(81b5cf37族)≠存储模板hash(尾换行,2cab9833族)。apply 409 baseline_race时先按TrimSpace口径重算再重试,勿盲目省略baseline_hash。

- **max_hold 时钟=币安 updateTime,可被资金费结算等事件重置(08-08 02:1x 源码+逐笔实锤)**: binance.go L1540-42 映射 UpdateTime→OpenTime,quick_trade_monitor.go L84-89 优先币安钟→updateTime 刷新即重置 60m;饥饿层用本地钟不受累(亏仓 45m 照割),滞留域仅(−5%,+8%)roi 带,现净影响+2.43 良性。判据: 亏损腿 hold>75m ≥3例或单笔≥5U→M 修复(取 min 钟);亏腿计数 1/3(KAITO 08-08)。证据链全文 git 9d0dc94。
- **[速记·压缩@08-23] 双会话抢窗双写(08-08首例)**: PATCH=_exp全量替换last-writer-wins+stop/start幂等→双会话互不感知各自"成功";同源载荷无害,**异源载荷同窗竞写静默丢先写**→互斥靠§5认领行+后启会话先探audit;全文git 766c7d9系

- **[新增 08-09 06:4xZ] ctx 两口径（源码核实 optimize_handlers.go）**: trades_window(data_source=binance)=buildTradesWindowFromBinance 打包，count=成交腿数（24h 194腿 vs 逐笔配对61笔，分批平仓一笔多腿），long_pnl+short_pnl≠realized_pnl 属口径差非bug；paired_trades 键仅 DB 源变体出现。avail 权威读径=ctx.binance.balance_usdt（ctx.account 无 balance 字段）。逐笔归因一律以 A 武器 closed48 配对行（滤无 realized_pnl 幻影行）为准。
- **[新增 08-09 09:3xZ] positions.realized_pnl=税前毛额（不含佣金/资金费,实锤）**: 24币逐一与 income 原始 REALIZED_PNL 比对 diff=0.000 精确吻合（CYS raw−12.371=closed−12.371,佣金−0.565 另在 by_symbol.commission;KAITO raw+9.539 vs net+10.536=资金费差）。含义: 历史费覆读数（均净 vs 2×来回费）实为【毛额比】,真实净额=毛额−佣金,字面'净额≥2×费'需毛额≥3×费。跨轮趋势可比性不受影响；今后 TG 双口径并报（毛额比+扣佣净额比）,红线判定从严=毛额<3×费即🔴。

- **[新增 08-10 00:4xZ] stop迟滞≈池规模（两次实测）**: main(85币池)stop回执后15s/44s两次不落地(status持续running,start-back吸收);同窗brk(3币池)stop 2s confirmed,5s原子完成。#39先例main 2s落地=非稳态。候选机制=控制事件在策略循环检查点消费,大池评估爆发饿死检查点。影响:main空仓PATCH窗不可靠,种子同步走#40多轮重试;不判stop API故障。

- **[压缩v2@08-19 09:3x] closed行strategy_id归因方法论**: 专家载具closed行盖sid始于08-09 19:05,main行至今全空→主口径=sid,空且属专家池币=按池回填,其余归main。三陷阱(全实锤@08-11~08-17): ①空sid≠必main(断网重启窗重建的专家行sid丢失,按池归属) ②段起点污染——段评判=sid∧close_time≥_exp.ts双滤(TUT/fade两案) ③非空sid≠该载具开仓——停机壳收养错标(#57 BEAT/BLUAI/VELVET)与删除壳盖章(STAR翻案)两族,账按开仓者日志归属;例证细节git 26eaf24+766c7d9系

- **[新增 08-10 18:2xZ] 日志置信度=折扣后值(四例精确复算)**: 未触发信号/评估行的置信度=加分项和×低波动折扣0.8(0.65→0.52/0.75→0.60/0.95→0.76/0.70→0.56全吻合);过0.55基础线仍可被后级门拦(长锚0.70/ATR%<0.5硬滤/sides)。读日志勿把置信度当原始分;S24/25/26谱系继承同口径。

- **[压缩@08-20] 08-11停机事故定案**: 根因=宿主机DNS故障(Tailscale MagicDNS解析binance域全i/o timeout)→WS+REST双断刷错,非引擎bug(四载具零Python异常);既有TCP连接不需DNS(部分live流仍推送);**交易所侧条件单兜底实证**(COOKIE断网中TP在币安侧成交+0.35,恢复后从成交史重建sid空行);**stop/start不写audit行**;服务器处置权owner(硬边界#6)〔四实锤全文git 0980231前〕
- **[新增 08-12 06:3xZ] 回测挂死机制定案(bt29+源码)**: 取数FetchHistoricalCandles先于watchdog装载→取数挂起=running永久无守护;API无cancel端点;不阻塞实盘;修复候选=fetch前置deadline或cancel端点(低优先)。案详git bb2e前史。

- **owner直令双投递→并发会话双执行(08-12 22:19-22:5x实证)**: "启动全部策略"同时到达两会话——A会话start×3+#43门复核+台账先推(cd4b082),B会话(rv5cwj)独立核验后补齐ROUTE−11+对账器v1.2,push被non-ff拒→fetch重放合并(本条即合并产物)。防护三层=①动作幂等(start对running=no-op;rotate remove不存在币=skip)②台账push非ff必重放禁force③**执行动作前fetch台账**自此升为必须步(A会话基线事故+B会话non-ff=同型双证)。
- **[压缩@08-20] stop异步+回声行挡停(08-12定案)**: stop回执仅=入队(真停runStopWorker,失败只写策略error日志行);validateStrategyCanStop数DB open行,而账户级对账器把交易所仓按最近订单回声补建到各owner名下→**任何载具持仓期间main必有open回声行⇒main空仓窗=全组空仓窗,普通stop必拒;出口=stop?force=true仅跳该校验teardown同,无撤单不碰他载具仓(force停main实证成功)**;回声行=幻影行同源〔全文git 0980231前〕
- **BOOT RESTORE开机自恢复(08-12源码定案)**: 后端重启自动拉起DB态running/starting策略(lifecycle.go RestoreRunningStrategies延5s);人为stop(DB=stopped)不触碰⇒trend单跑期间owner重启后端=trend自动恢复非无主start;挂死回测行(bt29类)重启无启动对账不自动清。?q=/limit logs过滤未部署实证@08-12(dev-logs-limit候部署;main日志100行窗≈1秒宽,error行需停机后或安静窗抓)。

- **对账器版本管理(08-14确认)**: 新cron容器工作树=默认分支→ops/route_pools.py只有v1.0初版,直接跑=错误plan(实证2例:08-13容器v1.0误提议拆trend/fade池;08-14误提议清空trend池+COTI越brk直入trend)。修根@08-14:权威副本入quanty-ledger分支ops/route_pools.py,每轮fetch台账即得现版;改对账器=ROUTE预注册,改后同步台账分支副本+§1.5谱系行。

## 4. 假设库·候选队列（v2 迁移注记 @08-01 16:40Z：本节与 §6 观察计数合并为【假设库】，内容全量保留；prompt v2 起执行门槛=逐笔证据标准[≥20 笔同型死法或机制落到源码行为]，旧 v12 Step 4.6c 门槛作历史参照）

| # | 类型 | 内容 | 依据 | 复现计数 | 状态 |
|---|---|---|---|---|---|
| 72 | 引擎候修(M通道;#57族亚型) | **收养错归属:停机壳可抢归属**——findGuardStrategyInstance按实例序取首个"未拉黑∧isAllowedSymbol"壳,不排除stopped(strategy_roi_monitor.go L187收养/L338-352遍历+manager.go L287 isAllowedSymbol读config.symbols);竞态窗=开仓行落库慢于收养tick(dupCount现查DB=0);修法=遍历改running优先两趟(保留stopped兜底供已存行守护) | 18:12Z VELVET实锤:main开仓链完整(conf0.75/mult1.4/1per200槽)而行sid=21519f1b;守护正常管理(BE+trailing+guard平仓)实害≈0=08-21几何对齐保险起效;a451d32幂等闸挡"双行"不挡"单行错主"亚型 | n=1 | 候修;**缓解已落@08-24 18:4x=退役壳自bl5种子;post-fix首复核@21:2x=sid=21519f1b新行0✓**(18:12案行系pre-fix开仓;post-fix VELVET行转空sid=§3回填双空手预期签名,≠#72复发);FIX评08-27判据不变=自bl后再现sid错归属行→升部署主诉求 |
| 71 | M通道候部署(做市模块,非策略组) | **Gate做市WS下单通道**(claude/dev-gate-ws-trade已推):marketmaker/gate.go下单/撤单优先走WS长连接省REST建连时延,开关ws_trade默认false=纯REST回滚路径;幂等回退(下单仅发送前失败回退REST防双挂单,撤单一律REST兜底);协议与Gate官方SDK逐字段核对+签名跨语言双证;整仓build✓单测3/3✓race✓ | owner交互直令@08-24 15:xx'接入ws下单省建连时间';现状实证=策略组三载具Binance走REST /fapi/v1/order(binance.go:1250)+做市Gate走REST /spot/orders,全库无WS下单路径 | n/a | 候部署(部署权owner;TG已报备msg7608三选项A只gate/B也做Binance合约WS/C搁置);**注:未动策略组Binance路径=当前交易零影响** |
| 69 | E7类(trend重申请;队首) | **EXP:trend低波解锁三值包重申请(adt0.5+lcp0.149+mces off)**;理据=原rollback亏损全源ACE(已隔离),ex-ACE n14净+4.11 wr86%+双机制落源码(连开闸死结strategy_signal.go:262/adt抬门) | 终裁全文config._exp;逐笔git 5df52d2 | n14 ex-ACE | **closed=rollback@08-25 09:24Z**(段n11净≈−0.35;verdict存config._exp+§1.5;churn禁区生效=不再申请,低波解锁trend线关闭) |
| 35 | M通道 | S14 CB状态持久化(restart清计数株连修复;候选=引擎计数DB落库或cron快照种子) | 方案+先例链git(08-14轮) | n=2币/−7.66 | 候选(候部署;缓解=restart节流纪律) |
| 37 | M通道 | logs端点?limit(≤2000)+?q过滤 | 读端Limit(100)硬编码堵S23评判读口;详史git 45e2f36前 | n/a | **已部署实证@08-15 15:37**(limit500✓/?q✓;统计行标签新引擎='IDLE';#34回补通道就绪)〔全文git 26eaf24〕 |
| 2 | E8 类 | `max_consecutive_entries_per_symbol` 3→2 | pct 升档后单币堆叠上限变肥（07-22 06:43Z 登记）；16:16Z 无亏损侧稳定分化支撑 | 0/2 | 候选 |
| 18 | M通道 | closed窗口边界播种修复(幻影行族根因) | 幻影三形态全史+标本链见git 26eaf24 #18行(BTW隙5幻影/镜像行/ROBO整60m例) | n/a | **修复部署@08-15**(f4848f8封边界播种;隙型另路径未封,形态链git bb3b883);观察1-15史git 0940c62/044907d系(观察15@08-19=1/22隙型回归BEAT旧窗行,破连清零);**观察16-32(至08-22 03:1x连17轮全零,最新0/23)**;行集随重拉波动=懒生成方向;滤行纪律无限期保留;结案重置=隙型机制源码查明或#56部署后复核 |
| 60 | 归因观察 | closed行sid空串形态 | 结案@08-21=owner域结构性形态非缺陷(main行在cron视图EMPTY;§3 BOOK) | n=15史 | closed;全史git |
| 61 | E7类观察(main) | 多头溢价墙#61实验族closed rollback@08-19(prem三代全负,门回0.90=多头事实关闸) | 族全史git 0940c62+95e4c9f+9d0dc94 | 终态 | closed |
| 62 | 观察(S21域) | **S21急再入veto机会成本反例首例**:03:11Z轮登记(n=1)——轮内细节随该轮空提交丢失,存证仅commit ceb2d31 message原文"S21机会成本反例首登记§4#62(n=1)";同轮新笔=AIOT S+0.09/CLO S+0.86 | commit ceb2d31(0 diff空提交=写入失败事故,06:18轮发现补记);S21本体KEEP判据在§6 | n=1 | 观察;判据:S21域机会成本累计≥5例或段净影响≤−5U→复核S21窗参;新例登记须带veto行时间戳防再丢;登记纪律注记=push前须验commit非空(git show --numstat);**+09:2x轮:emit veto2例(08-19 02:27/04:29统计行急再入veto=0/1)**,反例性未证(影子结算未见),暂不计入n——n仍1,veto事件≠机会成本反例,须影子胜方可计 |
| 63 | S18域 | S18 veto×0.750档影子结算n18/wr61%/−2.38%转负,接刀通道废弃 | 全文git ef6d02e/f43756c | 终态 | 结案=S18 KEEP@08-19;EDEN负funding毒亚型=重议首排除项 |
| 21 | M通道 | backtest可用性修复包:①看门狗+cancel端点+挂死根因 ②preroll预热 ③缺因子降级注记 | §3 08-03源码级机制+差分重放实锤;08-06 +6例挂死复现;运维规则=烟雾只发≤18h窗;全史git d19c710 | n/a | 候选(候部署窗);**task30-33四连死于重启@08-24(基建非代码);task33曾误用7d窗,task34起回归12h窗规程** |
| 3 | E8 类 | per-symbol 重入 gate（代码级，需新锚点 S17） | churn 残留；引擎不执行重入字段（见已确认机制）。16:16Z 注：AKE（churn 代表币）24h +5.06/12 已转盈利，紧迫性降 | 0/2 | 候选 |
| 5 | 研究 | TP/SL/max_hold 与 15-60m 桶关系 | 桶已翻正，优先级降 | n/a | 低优 |
| 6 | E7 类 | 热点∩池内维度加权（择优批中 trending 币） | 热点∩池∩盈利连续8轮链+反例COTI/AKE热而亏³=榜首逆信号〔压缩@08-09 15:2x,全文见git e93a79e〕;§6计数行为准 | 0/2 | 观察（依据弱化第4轮） |
| 7 | 观察 | CB 重犯加时（同币第 2 次隔离 ×2，S17 类） | CB throttle-not-eradicator 多币多轮观察;cb_consec_losses/cb_quarantine_min 均 config 可设(代码 line557-558);历史 RIF/BANK/BEAT/BULLA 全零新增归档 | 0/2 | 观察;判据=同币CB隔离期满重入再血达4.6c→升级;逐轮历史链〔压缩@08-07 16:1x,全文见git b1bd082〕 |
| 9 | E7 类 | short盈利机制固化(白名单小改) | 判据链git 121e975 #9行 | 0/2·过时@08-13 | short主战场归fade(S25);main短侧复议前提=#43 unlatch后新证据 |

| 11 | E8 类 | short跨币修补:`breadth_max_for_short` null→0.50设计定稿@08-15(config-only;宽度门L1713 veto;回滚=null;设计全文git d19c710前史) | 依据链git+§1.5#49终数;短侧n26/+0.04已打平 | 2/2达成→病灶消退 | **降级观察@08-16 18:2x**:08-09依据(24h空−15.20 burst)在#49基线几何下未再现→撤出候选队列;**重升门=main空侧24h≤−8U复现**(届时以#49段为基线重算keep线再上_exp);watch@08-21 03:1x:main空24h−2.41修复中(末3空连赢+2.65),距门5.6U;ONG多−1.92不计空侧归#61族 |
| 30 | S21类 | 追跌空反弹veto(gate7e)设计预审计FAIL不上线(签名不分离赢亏) | 全史git 044907d | 终态n=20/−43.49 | closed;重开须新分离维+从零计数 |
| 47 | FLEET候选 | **S27突破追动量复活版**:入场核S26保留,出场重构=让赢家跑(trail回调≥0.5×池ATR%或trail激活后免45m硬帽);出池阈值同包注册 | S26段n15/−3.23=出场结构性倒挂非信号伪;全链git 95e4c9f/17251a9 | n=15段档案 | 候选;门=组费覆🟢+新_exp预注册+ROUTE灌池;载具已删→复活=POST建新壳(FLEET预注册+TG报备,tpl577存git);**追加门@08-18=候#56/#57部署后再启**(sid伪影三案:新壳=归因再污染;且候选池过薄) |
| 55 | E7类(trend) | **EXP:trend prem降档候选(gate 0.70→0.65)——watch结案@08-21 03:2x不改**:真凶=atr_discount×0.8(ATR%<1.0锁分)非S24硬过滤;解锁双径=市场波动回归(已自证@08-21 12:18 COTI破荒)或EXP降门;费覆红线内只登记不动〔分带演化+ACE三条件全史git 044907d/92fb9c3〕 | 机制级(gate) | 0/2 | 候选(需求已减:破荒后活性恢复) |
| 64 | E9类(v2) | min_atr 0.5→0.35——closed@08-23随低波三值包ROLLBACK(低波解锁不付费=组级知识) | 全史git a2e845f+f71d825 | n=11拦截史 | closed;重开须池ATR中位持续≥0.5;S28质量通道正交 |
| 65 | 代码缺陷(组共病;v2/trend活性) | 信号门浮点边界:score与门槛精确等值被静默丢弃 | 活体证据08-22 COLLECT六行;机制=浮点算术直证 | n=6行/1夜 | **修复executed@08-22(config:v2 scp0.049+trend lcp0.149)→epsilon代码级永久化@08-23 21:24(v2域随S28 apply,ss≥thr−1e-9);trend域曾靠lcp0.149→**回退@08-25(lcp0.15随#69回滚,trend域epsilon回未修态=score恰0.70被丢的已知小漏,与main现状对齐;重修=未来trend config动窗顺手0.149或代码级,须自带预注册)**;main两侧维持不修(短侧意外保护+长侧0.90反向安全,owner'先这样'批准);全文git a2e845f系** |
| 66 | 代码候选(v2原型正确性;批2) | **S28-fade衰竭签名直入重写**(顶点开空:RSI>75+失速<0.3×ATR+触上轨,不等EMA确认;SL/S22/保险全保留) | 双标本同构+7月反向血账链全文git e49b3b7/f43756c | n=2载具标本 | **executed@08-23 21:24Z=tpl823 apply✓**(评判随§1.5 v2行_exp二元;设计全文tpl823.py头注+git 7d6a27f) |
| 58 | E9类(main扩仓) | **main pct 0.08→0.10**(config-only;Σ=0.10×5+0.05×3=0.65≤0.75✓;回滚=回0.08) | main载具费覆首次12h/24h双🟢@08-17 09:2x(24h n12/+4.74 avg+0.395=3.8×费 wr83全空侧;12h+0.167=1.6×);硬边界#1按载具判=允许,组口径🔴不锁单载具扩仓但#57在飞收养竞态=fade config接管main赢家仓(BEAT/BLUAI实证)→扩仓入缺陷不洁 | n=12段 | 候选@09:2x;**门=①a451d32部署+#57双行归零观察2轮 ②main 24h费覆🟢维持 ③执行窗现拉复核净为正**;执行走EXP _exp预注册(main槽空闲);门①a451d32已部署@08-21✓→改锁'#57双行归零观察2轮'进行中;门②n薄高频摆动(3次翻转史存git f3c6469)=按'执行窗现拉'裁,不逐轮追写;**激活门补注@08-21 12:3x=24h∧48h费覆双绿再动**(12:3x评估:48h首绿系伪影→拒);**新阻@08-24 21:2x=main行零归属**(空sid12/12,§3 closed回填缺口):'扩仓入缺陷不洁'原则续锁;本轮main 24h avg+0.226/48h avg+0.165双绿实录但3h前尚红=n薄摆动,且绿翻转全系21:10 VELVET+1.63单笔;解锁=crash loop根因修复或main行归属恢复+双绿续持 |
| 52 | 引擎候修(M通道) | **stop校验双洞+复活路径**:①validateStrategyCanStop把sid=""仓当不存在+auto池跳过交易所侧校验→带仓stop可过闸(修法=exchange侧校验在auto池用全量active仓判);②stop后≤40s自动回running复现机制未定(requestRestart候选);顺手项=stop回执改真沉降或带async标注 | 18:27全录grab50.log+源码strategy_lifecycle.go L233-270/strategy_runtime.go L135;XNY sid=""行实证 | n=1(main) | 候选;dev分支claude/dev-*走M通道,部署权owner;先复现②再动手 |
| 53 | 引擎候修 | DeleteStrategy斩草除根(无条件Kill) | 修复已推fa93736@08-16候owner部署;翻案@08-18幽灵成交=0→降卫生级 | n=1 | 候部署(卫生级);全史git |
| 57 | 引擎候修 | 收养幂等闸(账户级)+守护唯一化 | a451d32部署@08-21+obs2轮双行0∧收养0 | n=3史 | **结案closed@08-21 21:2x**;v2新壳=根除实证域;全史git |
| 56 | 引擎候修 | closed视图归因order_id精确反查 | fa93736部署@08-21+obs2轮达成;owner域限定语义BOOK§3 | n=4笔史 | **结案closed@08-21 21:2x**;全史git |
| 49 | 组共病(出场几何) | trailing(act1.0/cb1.2%)+BE(1.0)重构 | 72h n96基线赔率0.88;verdict全文config._exp+git 191b049 | n=30段 | **结案KEEP@08-16**=main基线几何 |
| 57 | ROUTE病理(阈值候选) | **晋升证据不可移植**:跨模板方向净额不认,专家进池须机制同型证据(自身样本或该模板回测烟雾) | VELVET/BULLA晋升入fade后反亏实证链+债务激活史(v2建壳/MELANIA活测第3例)全文git f71d825/17251a9 | 病理级 | 登记@08-16;**S28批1已交付@08-23,阈值重设计=S28段证据到手后另行ROUTE预注册**('机制同型证据'现定义=衰竭签名适配性,须live段校准) |





## 5. 待落队列（已决定、仅被持仓/平台锁阻塞的动作；空仓窗按 Step 2.5 逐项落地）

| # | 类型 | 内容（含完整意图） | 登记轮 | 状态 |
|---|---|---|---|---|
| — | — | (空) | — | — |

> 维护注 08-24 18:4xZ：#70 **落地结案删行**——18:37全组真空仓窗:对账器v1.4先跑(desired17+MELANIA defer=18与#70一致),main stop[poll1即stopped]-PATCH bl18-start✓running✓;重启泄漏9热清9/9→feed91✓config复检bl18无泄漏✓COTI入feed释放✓。
> 维护注 08-22 18:2xZ：#68 **落地结案删行**——18:1x全账户仅COTI在trend仓,v2与main双空仓:①v2 stop[poll1]-PATCH symbols3币-start✓running✓feed3✓_exp完好✓ ②main stop[poll1]-PATCH bl18(+MELANIA)-start✓running✓;重播种#24泄漏11(隔离8+专家3)热清11/11零挡→feed89泄漏0;config关键字段复检无泄漏。
> 维护注 08-22 15:1xZ：#67 **落地结案删行**——15:11全账户真空仓窗抢到:trend stop→poll1即stopped→PATCH(种子写回COTI/TUT+_exp延评文本+verdict_n8中间记录)→start;后验symbols2✓eval_after落库✓三值包完好(adt0.5/lcp0.149/mces null)✓running✓feed2✓。
> 维护注 08-17：#59落地(程序v2全序先例)+#54/#51/#50/#46/#42/#45归档,全文git bb3b883/2ce0fb4。

## 6. 假设库·观察计数（v2 迁移注记 @08-01 16:40Z：并入假设库，与 §4 合称；跨轮累计；9 秒日志窗单次未观测 ≠ 零，以本节跨轮增量为准）

| 计数项 | 读数 | 更新轮 | 备注 |
| 幻影行post-fix观察 | +1@08-25 00:2x(SCRT空回声指纹);**窗内0行@08-26 15:2x(连观,累计n=1)** | 08-26 15:2x | 再现累计;n≥3或含实亏→升§4 |
| epsilon边界放行观测(v2域;#65永久化伴生) | **n=3/类净+0.016@08-26 15:2x(v2零新笔连6轮)**(+HANA空#3 20:48开conf恰0.600[资费0.20+多空比0.15+EMA0.30+量0.10=0.75×折0.8],RSI40.3布林中部,资费0.00032双边界=与#2同构;**吃到8.9%瀑布TP+trail 12.5m毛+0.773→类翻正**;#1超时−0.173/#2饥饿−0.542详史91b3276) | 08-25 18:2x | 边界类累计n≥5∧类净≤−2U→复议(现n3/+0.016远离门,方向观察:边界笔≈掷硬币而非系统性漏损);**⚠️premium加距已源码判死@18:2x:tpl887 L806 short_thr为常规+S28共用门,S28最小组合(衰竭0.30+上轨0.15+死叉0.15=0.60)与常规0.75×0.8简并于0.600,任何加距同杀S28主通道→类修复须代码层路径感知(S30候选,门=本计数达标)**;**main短侧0.600档已收@08-26 17:1x(scp0.049,owner提频直令;本计数v2域继续)** |
|---|---|---|---|
| main断流观测(三闸交集关门) | 本轮main笔数=9/24h@08-27 00:2x(亏侧全集中ONG磁铁×2+ARIA档笔;feed83 K线序号542→613连续=零新重启) | 08-27 00:2x | 判据不变:再现6h+零笔且池ATR中位≥0.5→查管道;每轮记main笔数 |
| rotate has_open_position判定条件观测 | n=2矛盾@08-23(15:16 COLLECT持仓中remove未被拒)〔全文git 0940c62〕 | 08-23 | 再现1例→定性(疑判定=本载具视角);影响=互斥窗口期 |
| apply重启作用域观测 | **定案@08-24 12:4x n=2→升§3**(tpl823+tpl887双证,他载具feed/IDLE计数连续) | 08-24 12:4x | 已定案;反例(他载具feed跳回种子全集)即回§6重开 |
| 重启后速开仓观测(crash loop伴生) | **n=5@08-24 18:2x**(+TUT开17:19:34≈17:18重启后~75s平−0.758=**首亏例**;史4例全非亏:06:24窗内/07:39~60-90s/14:44 63s/15:07 106s);机制候选=重启清cd_sec态+回填后立即重评 | 08-24 18:2x | ≥5例且亏单≥3→议重启后静默期;现5/1未达 |
| 收养错归属亚型(#57族;§4#72) | **n=1维持**(post-fix新增0,复核08-26 15:2x✓+**首例实弹pass15:16:34Z**[main开VELVET空sid=空未被收养=占位符封API层向量首次实证];窗内21519f1b行2条均pre-fix[08-24 18:12案+08-26 09:08复发案]) | 08-24 21:2x | 判据:自bl后再现sid错归属行→#72升部署主诉求;每轮扫closed行sid=21519f1b计数 |
| main行零归属(空sid;§3 closed回填缺口) | **12/17@08-26 18:2x**(非空5=收养案2[21519f1b]+v2正常3;18:10 ARIA活仓行sid亦空;始08-23 18:24持续;sid真归因纠偏BOOK见§7 06:3x行) | 08-25 21:2x | 无P&L实害(income/守护/池互斥归因全旁路);判据:crash loop根因修复后仍空sid→升M通道close-sync候修;每轮扫main池closed行空sid计数 |
| closed平仓行消失观测(≤48h短窗) | 计数与形态史git 17251a9/eaf1bcd(08-24 06:2x掉行再证=16h短窗+income双拉纪律) | 08-24 12:5x | 纪律:窗内行少≠没交易,income n为准;长窗>120h禁用作逐笔 |
| 已结案·终态归档集(18项瘦身@08-26 12:4x) | UTC早晨段(bce1b1d)/pick_lose(全史git)/信号转化0(bce1b1d)/WS断连(3dd071b)/单币失血11币(add4230)/long双窗(bce1b1d)/15-60m桶(b1bd082)/穿刺亚型c(2873d28)/S20拉升拒空(541a2bc)/#20hunger_tp(数字§3)/S21急再入(d1400b2)/fade0.60墙(766c7d9)/追跌空→#30/长侧穿刺→#34/统计汇总→#37取代/S23影子(8897832,重开=#37后)/连开拦截(f71d825)/CB熔断→#35 | 08-26 | 各项终态读数与重开条件见git指针 |
| 平仓撤单竞态error观测(-2011/补设) | **3条@08-14 18:02-18:04(US×2+CAP×1,单一波动窗18:01-07三连SL,全自愈)** | 08-14 18:1x | 判据:复现≥3个独立窗或单窗≥5条→查撤单路径系统性延迟;机制定案§3;零处置 |
| main无端重启重播种观测 | 已升§3平台事实@08-15;**新例@08-26 17:26:35Z全局级(三载具IDLE计数同步重置~203回填bar;v2/trend种子写回免疫symbols5/1完好,main auto重播种97泄漏14→18:2x热清;直令轮17:2x热清feed83验证为真但8min后被此重启推翻=快照时效教训);前例08-25 19:10单次泄漏11;史08-24约4连** | 08-24 18:2x | feed数每轮复核照旧;crash loop已报owner(08-23),部署权owner |
| S19顺涨拒空veto | KEEP@08-06终态;非应拦例累计n=2(ONG@08-21,VELVET案)〔全文git 17251a9前史〕 | 08-21 | 非应拦≥4且净损≥2U→复评veto阈值 |
| 热点∩池内∧盈利连续轮数 | **重启1@08-10 18:2x(TUT回trending榜∩trend池,24h+6.52组最佳币;CYS/BEAT榜上但一在押一孤儿不动)** | 08-10 18:2x | 判据同前;n<30轶事级;隔离币上trending≠rehab信号(需7天无交易+画像翻转) |
| BICO资金费磁铁长侧 | 结案=已隔离@08-17 03:2x(48h n8/−4.05全多头;磁铁income直证;全文git bb3b883系) | 08-17 | 机制病留档:他币再现[funding<−0.0003入场bonus推门+价亏]→重开候选引用本条;rehab按头注 |
| funding磁铁·他币再现watch(重开@08-26;BICO条引用) | **n=2 era/累毛−2.83: +ONG多22:56Z conf0.9500(RSI24.7+0.20/资费−0.01548触+0.20/多空比+0.15/EMA up+0.30/放量+0.10)→4m快SL毛−1.442,与19:04案−1.386同构同日**;资费轨迹−0.872%→−1.5%→−2.0%(bonus幅度盲:−0.04%与−2%同给+0.20);围栏实证=SL后23:00/01/03三次0.950再入全被S22 emit veto拦+CB连败ONG:2/3(第3亏自动隔离240m)+00:16 EMA转down组合自然解体;前例ONG−1.92@08-21(#61族);史BICO n8/−4.05、ACE n4/−4.40〔git 8a797ec/bb3b883〕 | 08-27 00:2x | 判据不变: **funding<−0.003多单亏损再现n≥3或era累净≤−4U→S30候选**(设计定稿=资费多头bonus幅度分段:<−0.003减半/<−0.008归零或反转−0.10;校准集=跨era BICO8+ACE4+ONG3≈15笔−11U);era距门一步;ONG币级48h n2/−2.83未及隔离线(n≥4);⚠️main _exp槽被EXP提频占至08-27 18Z评,S30最早=该评后新_exp |
| short双窗负 | 0维持(逐笔配对为主口径)〔git 6a93e93〕 | 08-06 | 新增空头亏损事件逼近4.6c门→评估 |

## 7. 运行日志（每轮一行，新行追加在表首）

| 时间(UTC) | 档位 | 五窗净额 1h/3h/6h/12h/24h | 世界 | 决策 | 备注 |
| 08-27 00:1x-00:4xZ | 例行轮(max) | income:1h−0.79/3h−0.70/6h−2.71/12h−2.59/24h净−0.79(REAL−1.03佣−0.32资费+0.56;rows n12 wr50.0/be54.1=**−4.1pp**;毛均−0.086<0组🔴连2) | FGI71↑Greed/BTC78830+0.5%/mcap−1.75%无降压/trending∩池=BTR(fade)+**ONG(main auto,榜上+资费−2%=磁铁画像互证)** | **0原子包HOLD;ROUTE#55零差分(连3;bl_sync desired19=现19✓);磁铁era n1→n2:ONG多22:56 conf0.9500(资费−0.0155推+0.20,RSI24.7接刀)4m SL毛−1.442,era累−2.83距门(n≥3∨−4U)一步不跳(§6判据08-26预注册);围栏实证=S22拦23:00/01/03三连0.950再入+CB ONG:2/3+EMA转down自然解体;S30设计定稿挂§6待门** | 死法24h:TP6+5.77/SL6−6.80(ONG×2磁铁−2.83=最大失血源,饥饿0超时0);载具拆分:main n9毛−1.54(ex-ONG为+1.29)/v2 n3毛+0.52(BTR 22:24常规3m快赢+1.014、23:41常规SL−0.768;**段n6毛+0.573全常规,签名域n0,auto-C倒计时~45h**)/trend零笔(TUT ATR%0.53<1.0,churn禁区维持);EXP提频档n1无新增(0.600档S19/S20 veto~11次/6.5h=veto层重工作属设计,评今18Z);audit×3巡检零无主改动;钱包168.41(0持仓,21:1x 169.11→Δ−0.70对账一致);保证金0.63✓;充值线未触;待落0 |
| 08-26 21:1x-21:3xZ | 例行轮(max) | 24h净−1.67(rows n10 wr50/be56.3;毛均−0.137组🔴连3🟢断) | FGI65 | **HOLD;ROUTE#54零差分(连2);登记×2=磁铁watch重开(ONG 19:04案)+TradFi平台事实** | stub@08-27 00:3x;全文git 86e771d(死法/EXP档n1细节/旧壳FIX再证) |
| 08-26 18:1x-18:5xZ | 例行轮(max) | 24h净+2.52(组🟢连3,首次剥后仍🟢) | FGI65 | **HOLD;热清17:26全局重启泄漏14/14;ROUTE#53零差分;#47补强门首轮全格达标呈owner;EXP档首笔ARIA在押** | stub@08-26 21:3x;全文git 188af33(直令轮两重启法医学/我踏马watch/档首笔细节) |
| 08-26 17:0x-17:4xZ | 直令轮(owner live) | 间奏窗+0.15(15:38Z VELVET空trailing平;17:10起全空仓;五窗详15:1x行) | 距上轮2h未重拉,FGI65系 | **owner直令'增加频率'→①main提频EXP:scp0.05→0.049收0.600档(普查7.17h=411线/195ep/84币float误杀,历史零conf0.600成交;0.56低波档不收);预注册评08-27 18Z或档n≥15,劣化线档净≤−3U即回滚;②ROUTE#52执行:VELVET移v2(bl19重播种+泄漏14热清→feed83零泄漏;v2热add+种子写回symbols5+_exp拆分条款)** | stop-poll1-PATCH-start×2全链✓复读三值✓(scp/bl19/symbols5/_exp×2落库);互斥83/5/1∩∅✓;保证金0.63✓;§1直令登记;频率预期=档ep~27/h×转化率待实测,_exp metric=触发线conf0.600逐笔 |
| 08-26 15:1x-15:4xZ | 例行轮(max) | 24h净+1.83(组🟢连2形式,VELVET同笔撑两轮) | FGI65 | **HOLD;ROUTE#52首现差分VELVET晋升fade deferred(活仓);收养FIX首例实弹pass;#47门判未熟+判据补强BOOK** | stub@08-26 21:3x;全文git b8ab4bf/188af33 |
| 08-26 12:4x-13:0xZ | 例行轮(max) | 24h净+1.67(组🟢首轮,VELVET单笔依赖) | FGI65 | **HOLD;ROUTE#51零差分(连6);充值线形式触发判未熟呈owner** | stub@08-26 17:4x;全文git 8e7b8fd |
| 08-26 09:0x-09:5xZ | 例行轮(max) | 24h净−1.56(组🔴连4) | FGI65 | **原子包①旧壳FIX终裁FAIL+symbols占位止血;②#72修复push候部署;ROUTE#50零差分** | stub@08-26 18:x;VELVET复发案09:08Z全文git b8ab4bf/66048a9链 |
| 08-26 06:3x-07:0xZ | 例行轮(max) | 24h净−1.07(组🔴连3) | FGI65 | **HOLD;ROUTE#49零差分;BOOK:sid真归因纠偏——closed行sid非空=开仓时刻归属权威(COTI遗留4行归trend案)** | stub@08-26;全文git e00be39 |
| 08-26 03:1x-03:4xZ | 例行轮(max) | 24h净−0.10 | FGI65 | **0原子包HOLD;ROUTE#48零差分;钱包之谜=保证金冻结(§3)** | stub@08-26;全文git 1ae847c |
| 08-26 00:1x-00:4xZ | 例行轮(max) | 24h净−0.94(费覆回🔴) | FGI65↓ | **0原子包HOLD;ROUTE#47零差分** | stub@08-26;全文git 4fc0468 |
|---|---|---|---|---|---|
> 瘦身注(合并@08-25 21:3x): 08-25 21:1x行裁@08-27(全文git 5da0604); 08-24 18:1x行裁@08-26(全文git 17251a9); 08-24 15:1x行裁(全文git eaf1bcd)+08-24 09:1x行裁(全文git 040d80d^链+12699f5)+08-24 06:2x行裁(全文git b505652)+08-24 00:1x行裁(全文git 02d3fb5/c1f6f24史); 08-25 06:2x行裁+08-26 06:3x行stub@08-26 12:4x(全文git 12699f5/e00be39);原注与08-23前压缩史见git(a2e845f/cb2fc25等,链在git历史)。
