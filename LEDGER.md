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

## 1.5 策略组注册表（08-09 起;池归属与载具状态权威节;roster 有变当轮必更）

| 载具 | id | 原型 | 池 | 状态 | 门/备注 |
|---|---|---|---|---|---|
| main 通才 | 8eb182b6 | 通才(S18-S23栈) | auto·**feed91@08-24 21:2x复核(含20:37重启后零泄漏;COTI在feed可交易;#37热清史见git)** | running | pct0.08 sides=[buy,sell] **bl18@08-24 18:4x(#70落地移COTI)** **long_conf_premium=0.35(#61c rollback@08-19,门0.90=多头事实关闸)** tpl567;**trailing(act1.0/cb1.2%)+BE(1.0)=基线几何(#49KEEP)**;**_exp=closed rollback(#61c;史§4#61/#63+git 0940c62)**;RECOVER刹线6h空≤−8U继承;#11降级观察(§4) |
| qt-trend-long | 827ffe8c | 趋势动量多 | **TUT单币**(COTI出池@08-24 15:3x ROUTE#36:48h nL14净−1.06触头注线[窗滚动08-22赢单出窗];种子同步stop-PATCH-start@15:35✓;COTI回main孤儿态待bl释放;ACE隔离@08-22;史git f43756c) | running | **tpl575(S24)** pct0.05 mcp3 buy trailing on;**_exp=open·#69三值包重申请@08-23 09:18Z**(adt0.5+lcp0.149+mces off;终裁二元=新段净>0→keep否则回滚[adt1.0/lcp0.15/mces3];eval=n≥16或08-25 09:18Z;劣化线n≥8∧≤−4U;**进度@08-24 21:2x段n11净≈−0.35无新笔**(评期明晨09:18Z或n16,仍负→按预注册回滚[adt1.0/lcp0.15/mces3];评判注记:段负全系COTI拖累[段内COTI毛−0.92已出池@#36]vs TUT-only子段n5毛+0.44净≈+0.34——金标准仍预注册段净,子段仅作verdict记录,三值包三次申请=实验churn禁区);否决窗空置+程序史git 492779a);原三值段终裁rollback+ex-ACE归因全文git 5df52d2;**常驻保险=滚动6h净≤−5U→stop(继承)**;三值包史git 8a1d995/f71d825 |
| qt-fade-short-v2 | 7583727a | 冲高回落空(**tpl887/S28+S29**;谱系tpl576/S25→823) | HANA/COLLECT/MELANIA/BTR(feed4=种子4✓@08-24 15:2x;**MELANIA出池线触发但deferred@15:3x ROUTE#36**:nS7净−0.53证据全pre-S28[tpl806/823时代,S28段n0]=#57病理证据不可移植+移出=撤走S28/S29主标的;defer至_exp终裁重跑对账,风险界=保险6h−6U+劣化线;BTR晋升#35;MELANIA晋升08-22) | running | **S28衰竭签名直入上线@08-23 21:24Z**(#66修复,机制全文§2 S28行+git;附随#65 epsilon永久化;apply三复检✓);**_exp=open·EXP:S28**(二元:段净[Σpnl−名义×0.1%]>0→keep否则rollback回tpl576;劣化线段n≥8∧≤−4U即回;评期段n≥16或08-25 21Z;崩溃立即回滚;**S29 amend@08-24 12:40Z(tpl887)**;段n0@21:2x·24h(池4币live全布林中部RSI<75低ATR=签名域未现,非闸误杀);评期/劣化线/二元不变;终裁rollback=rollback×2或apply归档tpl576码;**明晚21Z评期n0边界预案**:预注册字面(段净0不>0)→rollback tpl576,但tpl576自身末段n16净−1.00=回到已证负机制——评判轮应并置第三选项(FLEET stop v2攒证据/汰弱)呈盘;**烟雾根因改判@08-25 00:2x:task33=interrupted by backend restart(录因);task34/35漏start_time→默认7d窗(§3新条;task31基准=显式18h窗10min完);task36修根重发@00:24显式18h窗=**completed✓@00:27**(3min,0笔=标准内)→S29烟雾债清偿,评期前路径已验**);低波三值包ROLLBACK@08-23(组级知识:低波解锁不付费;git a2e845f);保险=6h净≤−6U停(继承) |
| ~~qt-fade-short~~ | 21519f1b | 停机@08-16劣化线 | — | stopped(gated,勿auto-start)·退役归档@08-21 | v2壳接任;**自bl5种子@08-24 18:4x(ARC/1000CAT/BEAT/VELVET/BULLA)封收养错归属向量,_exp=open FIX评08-27;前moot verdict存audit id1754+git**;复活=新壳走FLEET(tpl576+S25存git);全史git 766c7d9/f43756c |
| ~~qt-breakout-follow~~ | 2111f5f9 | owner删除@08-15 15:5x(评判FAIL史+S26+tpl577存git;#47复活=新壳FLEET) | — | deleted | ⚖️翻案@08-18:幽灵可验证成交=0;#53修复候部署=卫生项;详git 766c7d9+95e4c9f |

- 隔离区(规则≤−4U∧n≥4)【13】: 4/CYS/TST/龙虾/BMT/BTW/H/APR/AIO/BICO/BEAT/XNY/**ACE**(+ACE@08-22 12:22 n4/−4.40;详git 8a797ec)(+BEAT[n9/−4.19混合血统,main era仅−0.76 rehab须知]+XNY[n6/−4.23全main空]@08-17 13:0x;+BICO[n8/−4.05资金费磁铁§6结案]@08-17 03:2x;+AIO/APR/H等史git;逐条全文git bb3b883)。**池列值=快照,权威=每轮对账器输出**。**对账器现版v1.4权威副本=quanty-ledger分支ops/route_pools.py@08-22**(v1.4@08-22=roster遮蔽修复[running壳优先,阈值零改动];谱系v1.3←61b1bb4←377c567←f970704;v1.0默认分支禁跑;演进史git bb2e)。**arg4输入纪律(08-15实证):隔离表必须X/USDT形态**(裸名norm不match feed键→漏移)。出池只认头注规则或隔离,失格=aging_watch advisory;brk/lowvol出池阈值未注册(#42)前无自动出口。最近跑@08-25 00:2x(v1.4)=**ROUTE#39零差分**(plan={};无新隔离晋降;VELVET 48h n5净−1.30离线远;aging=trend[TUT]+fade[4币全]advisory;want.brk[TAC,TUT,VELVET]#47门关;bl_sync=18全同);#38零差分+MELANIA defer链结案史git 95cdc9a;#37执行史git 17251a9
- 保证金: Σ_running=main0.08×5+trend0.05×3+fadev2 0.04×2=**0.63≤0.75✓**(08-21 16:5x FLEET建壳重算;前史0.55见git)
- 互斥不变式: feed层@08-24 21:2x复核=main91/trend1(TUT)/fadev2 4**两两∩∅**(20:37重启后零泄漏——首例重启不漏,机制未证[或auto-select当刻候选恰不含bl币],#20机制仍视为有效下轮续核);种子层bl=18(desired达成@#70);**退役壳自bl5=收养向量封闭**;重播种法医学详§3(签名=top回落~202;bl闸零违例);#20=bl币可直漏feed(交易闸级非feed级);#24/#23及#8-#19史git;引擎互斥闸✓live;**fade壳seed5与main auto理论重叠=停机态无违例**(不变式限定running载具;若owner UI复活fade须先重划池,互斥闸+TG示警兜底);隔离币零成交✓(复核@08-21 09:2x,48h窗隔离12币零平仓行)
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
| 69 | E7类(trend重申请;队首) | **EXP:trend低波解锁三值包重申请(adt0.5+lcp0.149+mces off)**;理据=原rollback亏损全源ACE(已隔离),ex-ACE n14净+4.11 wr86%+双机制落源码(连开闸死结strategy_signal.go:262/adt抬门) | 终裁全文config._exp;逐笔git 5df52d2 | n14 ex-ACE | **executed@08-23 09:18Z**;跟踪权威=§1.5 trend行;三次申请=churn禁区(评判轮铁律) |
| 35 | M通道 | S14 CB状态持久化(restart清计数株连修复;候选=引擎计数DB落库或cron快照种子) | 方案+先例链git(08-14轮) | n=2币/−7.66 | 候选(候部署;缓解=restart节流纪律) |
| 37 | M通道 | logs端点?limit(≤2000)+?q过滤 | 读端Limit(100)硬编码堵S23评判读口;详史git 45e2f36前 | n/a | **已部署实证@08-15 15:37**(limit500✓/?q✓;统计行标签新引擎='IDLE';#34回补通道就绪)〔全文git 26eaf24〕 |
| 2 | E8 类 | `max_consecutive_entries_per_symbol` 3→2 | pct 升档后单币堆叠上限变肥（07-22 06:43Z 登记）；16:16Z 无亏损侧稳定分化支撑 | 0/2 | 候选 |
| 18 | M通道 | closed窗口边界播种修复(幻影行族根因) | 幻影三形态全史+标本链见git 26eaf24 #18行(BTW隙5幻影/镜像行/ROBO整60m例) | n/a | **修复部署@08-15**(f4848f8封边界播种;隙型另路径未封,形态链git bb3b883);观察1-15史git 0940c62/044907d系(观察15@08-19=1/22隙型回归BEAT旧窗行,破连清零);**观察16-32(至08-22 03:1x连17轮全零,最新0/23)**;行集随重拉波动=懒生成方向;滤行纪律无限期保留;结案重置=隙型机制源码查明或#56部署后复核 |
| 60 | 归因观察(M通道族) | **closed行sid空串形态** | 08-18 03:1x首拉;与#57同族 | n=15行史 | **结案closed@08-21 21:2x=owner域破案吸收**(§3 BOOK@18:1x:空串/EMPTY=main[owner1]行在cron[uid2]视图的结构性形态非缺陷;专家行部署后order_id精确3/3✓;forensics闭合=EMPTY∧非专家池⇒main排除法;原候修判据撤销);全史git |
| 61 | E7类观察(main) | **main多头溢价墙→#61实验族:closed rollback@08-19 15:3x**(prem0.35→0.25→0.20→0.19三代实验全负,多头n5/−4.77 wr20;门回0.90=多头事实关闸;'该放的没有'=放进来的都是顶点追涨)〔族全史+浮点边界破案git 0940c62+95e4c9f+9d0dc94〕 | 终态 | n/a | closed |
| 62 | 观察(S21域) | **S21急再入veto机会成本反例首例**:03:11Z轮登记(n=1)——轮内细节随该轮空提交丢失,存证仅commit ceb2d31 message原文"S21机会成本反例首登记§4#62(n=1)";同轮新笔=AIOT S+0.09/CLO S+0.86 | commit ceb2d31(0 diff空提交=写入失败事故,06:18轮发现补记);S21本体KEEP判据在§6 | n=1 | 观察;判据:S21域机会成本累计≥5例或段净影响≤−5U→复核S21窗参;新例登记须带veto行时间戳防再丢;登记纪律注记=push前须验commit非空(git show --numstat);**+09:2x轮:emit veto2例(08-19 02:27/04:29统计行急再入veto=0/1)**,反例性未证(影子结算未见),暂不计入n——n仍1,veto事件≠机会成本反例,须影子胜方可计 |
| 63 | S18域(veto机会成本) | S18闪跌veto×0.750档结构死锁——影子结算v1累计n18/wr61%/−2.38%转负,接刀通道废弃 | 重放器+判据全文git ef6d02e/f43756c前 | **结案=S18 KEEP@08-19 15:3x** | 反向判据触发结案;重开=新预注册从零累计;#61c同步rollback;EDEN负funding毒亚型=未来重议首排除项 |
| 21 | M通道 | backtest可用性修复包:①看门狗+cancel端点+挂死根因 ②preroll预热 ③缺因子降级注记 | §3 08-03源码级机制+差分重放实锤;08-06 +6例挂死复现;运维规则=烟雾只发≤18h窗;全史git d19c710 | n/a | 候选(候部署窗);**task30-33四连死于重启@08-24(基建非代码);task33曾误用7d窗,task34起回归12h窗规程** |
| 3 | E8 类 | per-symbol 重入 gate（代码级，需新锚点 S17） | churn 残留；引擎不执行重入字段（见已确认机制）。16:16Z 注：AKE（churn 代表币）24h +5.06/12 已转盈利，紧迫性降 | 0/2 | 候选 |
| 5 | 研究 | TP/SL/max_hold 与 15-60m 桶关系 | 桶已翻正，优先级降 | n/a | 低优 |
| 6 | E7 类 | 热点∩池内维度加权（择优批中 trending 币） | 热点∩池∩盈利连续8轮链+反例COTI/AKE热而亏³=榜首逆信号〔压缩@08-09 15:2x,全文见git e93a79e〕;§6计数行为准 | 0/2 | 观察（依据弱化第4轮） |
| 7 | 观察 | CB 重犯加时（同币第 2 次隔离 ×2，S17 类） | CB throttle-not-eradicator 多币多轮观察;cb_consec_losses/cb_quarantine_min 均 config 可设(代码 line557-558);历史 RIF/BANK/BEAT/BULLA 全零新增归档 | 0/2 | 观察;判据=同币CB隔离期满重入再血达4.6c→升级;逐轮历史链〔压缩@08-07 16:1x,全文见git b1bd082〕 |
| 9 | E7 类 | short盈利机制固化(白名单小改) | 判据链git 121e975 #9行 | 0/2·过时@08-13 | short主战场归fade(S25);main短侧复议前提=#43 unlatch后新证据 |

| 11 | E8 类 | short跨币修补:`breadth_max_for_short` null→0.50设计定稿@08-15(config-only;宽度门L1713 veto;回滚=null;设计全文git d19c710前史) | 依据链git+§1.5#49终数;短侧n26/+0.04已打平 | 2/2达成→病灶消退 | **降级观察@08-16 18:2x**:08-09依据(24h空−15.20 burst)在#49基线几何下未再现→撤出候选队列;**重升门=main空侧24h≤−8U复现**(届时以#49段为基线重算keep线再上_exp);watch@08-21 03:1x:main空24h−2.41修复中(末3空连赢+2.65),距门5.6U;ONG多−1.92不计空侧归#61族 |
| 30 | S21类(代码gate) | 追跌空反弹veto(gate7e)——结案@08-21 09:2x=设计预审计FAIL不上线(两要件签名不分离赢亏,维度内无阈值可分) | 三周全史+vision四查链git 044907d | 终态n=20/−43.49 | closed;重开须新分离维+从零计数 |
| 47 | FLEET候选 | **S27突破追动量复活版**:入场核S26保留,出场重构=让赢家跑(trail回调≥0.5×池ATR%或trail激活后免45m硬帽);出池阈值同包注册 | S26段n15/−3.23=出场结构性倒挂非信号伪;全链git 95e4c9f/17251a9 | n=15段档案 | 候选;门=组费覆🟢+新_exp预注册+ROUTE灌池;载具已删→复活=POST建新壳(FLEET预注册+TG报备,tpl577存git);**追加门@08-18=候#56/#57部署后再启**(sid伪影三案:新壳=归因再污染;且候选池过薄) |
| 55 | E7类(trend) | **EXP:trend prem降档候选(gate 0.70→0.65)——watch结案@08-21 03:2x不改**:真凶=atr_discount×0.8(ATR%<1.0锁分)非S24硬过滤;解锁双径=市场波动回归(已自证@08-21 12:18 COTI破荒)或EXP降门;费覆红线内只登记不动〔分带演化+ACE三条件全史git 044907d/92fb9c3〕 | 机制级(gate) | 0/2 | 候选(需求已减:破荒后活性恢复) |
| 64 | E9类(fade-v2提频候选) | EXP:fade-v2 min_atr 0.5→0.35——**closed@08-23 18:2x=随低波三值包EXP终裁ROLLBACK**(地板带产出16笔/41h但60m超时慢磨≈半数=无行情税;低波解锁不付费=组级知识) | 全史git a2e845f+f71d825 | n=11拦截史 | closed;重开须新regime证据(池ATR中位持续≥0.5后另议);S28走质量通道非地板通道=正交 |
| 65 | 代码缺陷(组共病;v2/trend活性) | 信号门浮点边界:score与门槛精确等值被静默丢弃 | 活体证据08-22 COLLECT六行;机制=浮点算术直证 | n=6行/1夜 | **修复executed@08-22(config:v2 scp0.049+trend lcp0.149)→epsilon代码级永久化@08-23 21:24(v2域随S28 apply,ss≥thr−1e-9);trend域仍靠lcp0.149;main两侧维持不修(短侧意外保护+长侧0.90反向安全,owner'先这样'批准);全文git a2e845f系** |
| 66 | 代码候选(v2原型正确性;批2) | **S28-fade衰竭签名直入重写**(顶点开空:RSI>75+失速<0.3×ATR+触上轨,不等EMA确认;SL/S22/保险全保留) | 双标本同构+7月反向血账链全文git e49b3b7/f43756c | n=2载具标本 | **executed@08-23 21:24Z=tpl823 apply✓**(评判随§1.5 v2行_exp二元;设计全文tpl823.py头注+git 7d6a27f) |
| 58 | E9类(main扩仓) | **main pct 0.08→0.10**(config-only;Σ=0.10×5+0.05×3=0.65≤0.75✓;回滚=回0.08) | main载具费覆首次12h/24h双🟢@08-17 09:2x(24h n12/+4.74 avg+0.395=3.8×费 wr83全空侧;12h+0.167=1.6×);硬边界#1按载具判=允许,组口径🔴不锁单载具扩仓但#57在飞收养竞态=fade config接管main赢家仓(BEAT/BLUAI实证)→扩仓入缺陷不洁 | n=12段 | 候选@09:2x;**门=①a451d32部署+#57双行归零观察2轮 ②main 24h费覆🟢维持 ③执行窗现拉复核净为正**;执行走EXP _exp预注册(main槽空闲);门①a451d32已部署@08-21✓→改锁'#57双行归零观察2轮'进行中;门②n薄高频摆动(3次翻转史存git f3c6469)=按'执行窗现拉'裁,不逐轮追写;**激活门补注@08-21 12:3x=24h∧48h费覆双绿再动**(12:3x评估:48h首绿系伪影→拒);**新阻@08-24 21:2x=main行零归属**(空sid12/12,§3 closed回填缺口):'扩仓入缺陷不洁'原则续锁;本轮main 24h avg+0.226/48h avg+0.165双绿实录但3h前尚红=n薄摆动,且绿翻转全系21:10 VELVET+1.63单笔;解锁=crash loop根因修复或main行归属恢复+双绿续持 |
| 52 | 引擎候修(M通道) | **stop校验双洞+复活路径**:①validateStrategyCanStop把sid=""仓当不存在+auto池跳过交易所侧校验→带仓stop可过闸(修法=exchange侧校验在auto池用全量active仓判);②stop后≤40s自动回running复现机制未定(requestRestart候选);顺手项=stop回执改真沉降或带async标注 | 18:27全录grab50.log+源码strategy_lifecycle.go L233-270/strategy_runtime.go L135;XNY sid=""行实证 | n=1(main) | 候选;dev分支claude/dev-*走M通道,部署权owner;先复现②再动手 |
| 53 | 引擎候修(M通道) | **DeleteStrategy斩草除根**:RemoveStrategy去Status==running守卫改无条件Kill;与#52同族(生命周期竞态) | §3 08-16幽灵条(manager.go L1496守卫+行为实证23:11Z);zombie 48h n15/−1.56 | n=1(brk) | **修复已推@08-16 09:5x**=claude/dev-closed-attrib-delete-kill(fa93736):无条件Kill+置stopping压制复活;候owner部署;**翻案注@08-18:幽灵删除后可验证成交=0(§3),本项降卫生级**(runtime悬空仍应灭,但无成交危害);同commit的#56归因反查因sid伪影三案(BICO/收养/幽灵)升为部署主诉求;#52仍独立候修 |
| 57 | 引擎候修(M通道) | **收养幂等闸(账户级)+守护唯一化** | §3#57条(BEAT双行互搏全链git) | n=3史 | **结案closed@08-21 21:2x**:a451d32部署@08-21 16:4x→obs1@18:1x双行0/22+obs2@21:1x双行0/26∧收养日志0∧重复止盈止损0(末例08-17 06:39,部署后零新增=logs直证)=观察2轮达成;fade-v1壳FIX-ALIGN同步moot转archive;v2新壳=部署后建=根除实证域;全史git |
| 56 | 引擎候修(M通道) | **closed视图归因order_id精确反查** | §3归因条陷阱③(BICO双笔抢归因案全链git) | n=4笔史 | **结案closed@08-21 21:2x**:fa93736部署@08-21 16:4x→obs1@18:1x(COTI→trend精确✓幽灵sid消失✓)+obs2@21:1x(专家行3/3精确[COTI×2→trend,HANA→v2开仓日志对证],镜像抢归因0)=观察2轮达成;**owner域限定语义已BOOK§3@18:1x**(main=owner1行在cron uid2视图恒EMPTY=结构性,main归因=排除法);全史git |
| 49 | 组共病(出场几何) | **出场几何倒挂重构**:main trailing(act1.0/cb1.2%)+BE(1.0);目标=实现赔率0.88→≥1.3 | 72h审计n=96基线:SL率40%/均亏−1.84/TP仅12%均+0.64/实现0.88:1 vs 设计1.8:1〔全文git 191b049前〕 | n=30段 | **结案KEEP@08-16 18:2x n=30门先触**(赔率0.43=微赢灌稀统计失真裁keep,三无失真维度全同向改善;verdict全文config._exp.verdict+git;trailing+BE转main基线几何) |
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
| 幻影行post-fix观察 | +1@08-25 00:2x(SCRT空08-24T01:43→01:54,入=出0.01744,无realized_pnl空sid=回声指纹) | 08-25 | 再现累计;n≥3或含实亏→升§4 |
|---|---|---|---|
| main断流观测(三闸交集关门) | 断流结束@08-23 18:24(23.0h;定性=三闸交集设计性关门,全文git a2e845f);本轮main笔数=11/24h(活跃) | 08-24 18:4x | 判据不变:再现6h+零笔且池ATR中位≥0.5→查管道;每轮记main笔数 |
| rotate has_open_position判定条件观测 | n=2矛盾@08-23(15:16 COLLECT持仓中remove未被拒)〔全文git 0940c62〕 | 08-23 | 再现1例→定性(疑判定=本载具视角);影响=互斥窗口期 |
| apply重启作用域观测 | **定案@08-24 12:4x n=2→升§3**(tpl823+tpl887双证,他载具feed/IDLE计数连续) | 08-24 12:4x | 已定案;反例(他载具feed跳回种子全集)即回§6重开 |
| 重启后速开仓观测(crash loop伴生) | **n=5@08-24 18:2x**(+TUT开17:19:34≈17:18重启后~75s平−0.758=**首亏例**;史4例全非亏:06:24窗内/07:39~60-90s/14:44 63s/15:07 106s);机制候选=重启清cd_sec态+回填后立即重评 | 08-24 18:2x | ≥5例且亏单≥3→议重启后静默期;现5/1未达 |
| 收养错归属亚型(#57族;§4#72) | **n=1维持**(post-fix新增0@08-24 21:2x首复核✓) | 08-24 21:2x | 判据:自bl后再现sid错归属行→#72升部署主诉求;每轮扫closed行sid=21519f1b计数 |
| main行零归属(空sid;§3 closed回填缺口) | **12/12@08-24 21:2x**(48h窗main池成交行全空sid,始08-23 18:24 UAI;trend/v2行归属正常=区分变量系main DB行丢失非API通病) | 08-24 21:2x | 无P&L实害(income/守护/池互斥归因全旁路);判据:crash loop根因修复后仍空sid→升M通道close-sync候修;每轮扫main池closed行空sid计数 |
| closed平仓行消失观测(≤48h短窗) | 计数与形态史git 17251a9/eaf1bcd(08-24 06:2x掉行再证=16h短窗+income双拉纪律) | 08-24 12:5x | 纪律:窗内行少≠没交易,income n为准;长窗>120h禁用作逐笔 |
| 引擎连开拦截(错失/避损审计) | 终局2例@08-22:1避损1错失对冲≈中性,结案归档〔全文git f71d825前史〕 | 08-22 | 新增例≥3且净损益单边≥2U→复评 |
| fade 0.60墙(空头分顶死门槛) | 结案@08-16(墙随载具moot;反证与读数史git 766c7d9) | 08-16 | 复活S25类原型时=门槛设计输入 |
| 平仓撤单竞态error观测(-2011/补设) | **3条@08-14 18:02-18:04(US×2+CAP×1,单一波动窗18:01-07三连SL,全自愈)** | 08-14 18:1x | 判据:复现≥3个独立窗或单窗≥5条→查撤单路径系统性延迟;机制定案§3;零处置 |
| S23逆bar影子观测 | 证据不足结案@08-13(n19<20门;全文git 8897832) | 08-13 | 重开判窗=#37部署后;逆bar族(#30/#34)冻结至可观测 |
| main无端重启重播种观测 | 已升§3平台事实@08-15;**新例@08-24:16:31/16:49/16:51/17:18约4连(main logs历史K线burst法医学),17:18后静默;15:24另有部分burst;每次重播种100抹purge→#37热清自愈** | 08-24 18:2x | feed数每轮复核照旧;crash loop已报owner(08-23),部署权owner |
| S19顺涨拒空veto | KEEP@08-06终态;非应拦例累计n=2(ONG@08-21,VELVET案)〔全文git 17251a9前史〕 | 08-21 | 非应拦≥4且净损≥2U→复评veto阈值 |
| S20拉升语境拒空 | 裁决KEEP+候审清零@08-07终态〔git 541a2bc〕 | 08-07 | 误杀审计(被拒币2h实跌>2×ATR%)≥3例→回滚tpl563;新应拦型报警 |
| #20 hunger_tp0.08段 | 裁决KEEP@08-07终态(数字存§3+_exp.fix20_verdict) | 08-07 | hunger域健康度随S21段一体跟踪 |
| S21急再入veto | 裁决KEEP@08-08终态(存§2 S21行)〔git d1400b2〕 | 08-09 | 急再入类段净≤−5U重启复核 |
| 重启清CB熔断计数成本 | 速记:累计3笔/−6.93(HFT),CYS第二币复现✓→**已候选化#35@08-08 20:1x**,计数与推进在§4#35;restart节流纪律维持〔压缩@08-10,全文见git 2873d28〕 | 08-08 20:1x | 判据与逐例链见git |
| 追跌空反弹型快SL观测 | 已候选化#30@08-07(计数移§4#30;史git) | 08-07 | 读数在#30行 |
| 长侧快SL穿刺观测 | 已候选化#34@08-08(计数移§4#34;史git) | 08-08 | 读数在#34行 |
| 统计汇总行未捕获连续轮数 | **被#37部署取代@08-15**(q=IDLE&limit=2000可回溯;结构性不可捕结论史git 26eaf24) | 08-15 | 跨段求和纪律不变;#34回补用新通道 |
| 热点∩池内∧盈利连续轮数 | **重启1@08-10 18:2x(TUT回trending榜∩trend池,24h+6.52组最佳币;CYS/BEAT榜上但一在押一孤儿不动)** | 08-10 18:2x | 判据同前;n<30轶事级;隔离币上trending≠rehab信号(需7天无交易+画像翻转) |
| UTC早晨段主盈观测 | 5+反例2=时段非因子终态〔git bce1b1d〕 | 08-03 | 达4.6c亏损门才议时段premium |
| pick_lose/槽满拒绝 | 不可判终态〔全史git〕 | 07-23 | 新议题需此计数时重建 |
| 信号→成交转化失败累计 | 0终态〔git bce1b1d〕 | 07-30 | 历轮全为门槛拦截非转化事件 |
| WS 断连/重连观测轮数 | ✅fallback归零终态(62cb2fd后13清洁样本)〔git 3dd071b〕 | 08-05 10:1xZ | 复现fallback即报警 |
| BICO资金费磁铁长侧 | 结案=已隔离@08-17 03:2x(48h n8/−4.05全多头;磁铁income直证;全文git bb3b883系) | 08-17 | 机制病留档:他币再现[funding<−0.0003入场bonus推门+价亏]→重开候选引用本条;rehab按头注 |
| funding磁铁扩展watch(ACE) | ACE已隔离@08-22(n4/−4.40)=watch结案〔全文git 8a797ec〕 | 08-22 | 隔离态零成交维持;rehab评估时重开watch |
| 单币失血观测·已归档集合(11币) | 全0归档终态〔git add4230〕 | 08-08 02:1x | 判据(BTW先例)=6h新增亏损再现→重起算;per-symbol=CB域不拉黑 |
| 穿刺亚型c(冲顶回落) | S18 keep@08-05;strict穿刺2/−4.95零新增;HOME c族n=1〔git 2873d28〕 | 08-06 | 新增strict穿刺或c族≥3例再议 |
| long双窗负共现 | E8关账,随FIX/PREM一体跟踪〔git bce1b1d〕 | 08-01 | 不再单列 |
| 15-60m桶双窗负 | 4(hold覆盖不全=降权)〔git b1bd082〕 | 08-01 | 复现且n≥30按4.6c评估;回正清零 |
| short双窗负 | 0维持(逐笔配对为主口径)〔git 6a93e93〕 | 08-06 | 新增空头亏损事件逼近4.6c门→评估 |

## 7. 运行日志（每轮一行，新行追加在表首）

| 时间(UTC) | 档位 | 五窗净额 1h/3h/6h/12h/24h | 世界 | 决策 | 备注 |
| 08-25 00:1x-00:3xZ | 例行轮(max) | income:1h0/3h0/6h+1.33/12h−0.90/24h+0.95(n38,毛均0.039<3×费0.045组🔴禁提频) | FGI74/BTC+1.7%/市值−0.7%无降压;trending∩main=VELVET,PUMP,AERO | **0原子包HOLD;ROUTE#39零差分;烟雾根因改判=task34/35非(仅)重启僵尸而是漏start_time默认7d窗(源码strategy_handlers.go:113,§3新条),task36修根重发18h窗=completed✓(3min,0笔=烟雾标准内)→S29烟雾债清偿;task33录因=interrupted by backend restart;两_exp未到门(trend段n11净−0.35无新笔评09:18Z;v2段n0评21Z,00:22实时conf≤0.44签名域未现);幻影行+1(SCRT回声§6)** | 钱包170.14 avail全额0持仓;48h sid归因:main+2.40(n12,wr75)/fadev2+0.22(n6)/trend−0.19(n17,COTI拖−0.96已出池)/壳−0.24(已封);24h:main+1.82(n10,wr80)独扛,trend−0.10(n9),v2 n0;死法48h:TP/trail18/+8.03,fastSL5/−4.58,hunger4/−1.14,timeout9/−0.13;feed91/1/4两两∩∅夜间零漂移;充值条件破(12h负) |
| 08-24 21:1x-21:4xZ | 例行轮(max) | income:1h+1.59/3h+1.33/6h+0.30/12h−0.57/24h+2.04(n41,wr61.0/be51.5=+9.5pp;毛均0.064≥3×费0.041组🟢) | FGI73/BTC+1.3%/市值−0.7%无降压;trending∩main=PUMP,LIT,AERO(VELVET出榜后反+1.63) | **0原子包HOLD;ROUTE#38零差分(MELANIA defer窗滚解除;VELVET机械判n5净−1.30不隔离);发现+源码定位main行零归属(空sid12/12,§3新条;#72 post-fix复核新增0✓);#58新阻登记;20:37重启新例(当日≈16,首例重启后feed零泄漏);task34僵尸→task35重发running;两_exp未到门(trend明晨09:18Z仍负→预注册回滚+COTI拖累注记/v2明晚21Z n0边界预案,均§1.5)** | 钱包≈170.1(avail全额0持仓);死法24h:TP/trail W10+6.09/fastSL4−3.77(VELVET×2+TUT+壳行)/hunger_L1−0.76/timeout小;VELVET盘中−3.0后21:10 trailing+1.63拉回;多空劈叉:24h空net+2.30 wr78 vs 多net−0.31 wr39(be51.5);充值三条件破(12h净−0.57) |
| 08-24 18:1x-19:0xZ | 例行轮(max) | income 24h+0.20(rows n21 wr62/be63) | FGI73 | **原子包①=退役壳自bl5封收养错归属(#72;VELVET 18:12案);ROUTE#37 bl18落地+热清9→feed91;费覆🔴;充值条件破** | stub@21:3x;全文git 17251a9 |
| 08-24 15:1x-15:4xZ | 例行轮(max) | 24h毛+1.87净+1.43(18,wr66.7/be57.0) | FGI73无降压 | **0原子包;ROUTE#36=trend出COTI(nL14净−1.06触线)+main purge7+fade MELANIA defer;费覆连3轮🟢;充值三条件连3轮成立;无端重启新例14:29:50** | stub@18:4x;全文git eaf1bcd |
| 08-24 12:2x-12:5xZ | 例行轮(max) | 24h净+2.60(26,wr80.8/be64.0) | FGI73无降压 | **原子包1=S29@fade-v2(tpl887,详§2/§1.5);ROUTE#35=BTR晋升fade+bl_sync19;apply作用域定案§3;费覆连2轮🟢** | stub@18:4x;掉行再证纪律=16h短窗+income双拉;全文git eaf1bcd^ |
| 08-24 09:1x-09:3xZ | 例行轮(max) | 24h净+1.52(13,wr62/be47=+15pp)｜12h+2.77(6,wr83) | crash loop 9重启05:58-08:32Z后43min静 | **0原子包HOLD;ROUTE#34 purge9→feed91;trend#69段n6净+1.23由负转正(TUT 06:55 mv+6.2% trailing骑赢);v2段n0@11.9h;费覆首🟢毛均+0.137;充值信号三条件全过→TG建议250-300U** | stub@12:5x;全文git HEAD^ |
| 08-24 06:2x-06:4xZ | 例行轮(max) | 24h毛−1.42净−1.63(income20笔wr50/be66🔴;closed48仅10行=窗掉行) | FGI73 | **HOLD 0原子包;ROUTE#33 remove8(post-restart重播种purge);两_exp未到门;crash loop再燃05:58-06:24连2重启;费覆🔴;TRANSFER−50U=owner提款已录** | stub@12:5x;全文git b505652 |
| 08-24 03:1x-03:4xZ | 例行轮(max) | 24h毛−0.68净−0.95(15,wr53/be59🔴) | FGI73无降压 | **HOLD 0原子包;ROUTE=#32 plan={}零差分连3;两_exp未到门;刹车绿;费覆🔴** | stub(SCRT幻影破案=真实成交零盈亏BE→§6;24h慢衰亡快SL0/饥饿−2.27/超时−1.18;钱包168.38);全文git |
| 08-24 00:1x-00:3xZ | 例行轮(max) | 24h毛+0.70净+0.27(16,wr62/be57) | FGI73 | **HOLD 0原子包;ROUTE=#31零差分连2;两_exp未到门;crash loop降温8.2h;费覆🔴** | stub;全文git 02d3fb5 |
| 08-23 21:1x-21:4xZ | 例行轮(max) | 24h毛−1.30净−1.53(17,wr53/be64) | FGI66 | **S28衰竭签名直入apply@v2=唯一原子包(_exp二元评期n≥16或08-25 21Z)+ROUTE=#30零差分;main断流23h结束** | stub;全文git 396fd76/e49b3b7 |
|---|---|---|---|---|---|
> 瘦身注(合并@08-24 00:3x): 18:1x行stub+#66依据截断+§1.5法医学去重(本轮);08-23 15:1x行裁+§6 BICO压缩+§4#64结案压缩+§1.5法医学时刻表转指针(本轮,全文git a2e845f);08-22及以前各轮行裁与压缩史见git(cb2fc25/f71d825/8a797ec/492779a/6283811/9e896ed/f623b98/7e2b925/bf6c431/9d0dc94/82e6cf6/0980231);旧注链在git历史。
