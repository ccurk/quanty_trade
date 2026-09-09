-- 幽灵收养(台账 #31)存量审计 —— strategy_positions (MySQL)
--
-- 状态:**已在生产库上跑过,只读部分**(2026-09-09,quanty-mysql,只 SELECT)。
-- 标了「实测」的数字都是真跑出来的;§4 的 UPDATE 一行都没执行过。
--
-- 病是什么:对账循环和持仓 API 都把「无人认领的交易所净仓」归给**最后动过这个
-- symbol 的策略**。共享交易所账户 + 单向持仓下,最后动过它的往往不是开的那个,
-- 于是别人开的仓被铸成自己名下的行。
--
-- 裁决(所有者 2026-09-09):**归属归开仓方,不归平仓方。**
--   盈亏属于仓位,仓位是开仓方建立的 —— 开仓方定了方向、规模、入场时机、风险敞口,
--   平仓方只是终结它。按"谁最后动过"归属,业绩数字会随执行顺序变化,不能用来决策。
--   查不出开仓方时不许猜:标 pnl_source='unknown',per-strategy 聚合排除、account 级保留。
--
-- 写入侧已落地(见 §5)。本文件回答的是:**存量有多少行、归谁、怎么办。**

-- ###########################################################################
-- §1 怎么把「幽灵行」认出来(库里没有 is_adopted 标记,只能证)
-- ###########################################################################
--
-- 判据两条,必须同时成立:
--   (a) 这一行自己没有开仓腿 —— 同 (strategy, symbol) 在 open_time ±120s 内
--       没有任何 purpose='entry' 的单。它不是这条策略开出来的。
--   (b) 它的 open_time 晚于这条策略**最后一次下开仓单**的时间 —— 那时它已经不再
--       建仓了(停机 / 被删)。这一条与 symbol 文本格式无关,是硬约束。
--
-- 只用 (a) 会高估:symbol 文本格式或时间窗错位都会让 join 落空。加上 (b) 之后
-- 剩下的都是「这条策略当时根本没在开仓,却长出一行」。

CREATE TEMPORARY TABLE ghost AS
SELECT p.id, p.strategy_id, p.strategy_name, p.owner_id, p.symbol, p.direction,
       p.open_time, p.close_time, p.amount, p.avg_price, p.closed_qty,
       p.realized_pn_l, p.status
FROM strategy_positions p
JOIN (SELECT strategy_id, MAX(requested_at) mx FROM strategy_orders
      WHERE purpose = 'entry' GROUP BY strategy_id) e ON e.strategy_id = p.strategy_id
LEFT JOIN strategy_orders o
  ON o.strategy_id = p.strategy_id AND o.symbol = p.symbol AND o.purpose = 'entry'
 AND o.requested_at BETWEEN p.open_time - INTERVAL 120 SECOND AND p.open_time + INTERVAL 120 SECOND
WHERE o.id IS NULL AND p.open_time > e.mx;

-- ###########################################################################
-- §2 影响面(只读)—— 实测 2026-09-09
-- ###########################################################################

SELECT strategy_name, owner_id, COUNT(*) n, SUM(closed_qty > 0) booked,
       ROUND(SUM(realized_pn_l), 6) pnl, MIN(open_time) f, MAX(open_time) l
FROM ghost GROUP BY strategy_name, owner_id ORDER BY n DESC;
-- 实测,共 **71 行**:
--   qt-fade-short-v2       owner2  23 行  booked 23  +1.078090  08-29 04:29 → 09-08 20:08
--   qt-breakout-follow-v2  owner2  19 行  booked 19  -1.808001  08-30 02:06 → 09-09 01:07
--   qt-breakout-follow     owner2  18 行  booked  2  -0.628916  08-14 07:17 → 08-19 15:56
--   qt-fade-short          owner2  10 行  booked  3  +0.972108  08-16 14:03 → 08-26 09:08
--   Meme_合约信号计算引擎_1 owner1   1 行  booked  1  +0.061370  09-09 07:06
--   合计 71 行,realized_pn_l 合计 -0.386718
--
-- 停机时间对照(strategy_instances.status/updated_at 实测):
--   qt-breakout-follow-v2  stopped 2026-08-29 22:21 → 仍长到 09-09 01:07(10 天)
--   qt-fade-short-v2       stopped 2026-08-28 21:20 → 仍长到 09-08 20:08(11 天)
--   qt-fade-short          stopped 2026-08-28 18:21
--   qt-breakout-follow     **在 strategy_instances 里已经不存在**(实例被删),
--                          仍有 18 行挂在它名下 —— 这就是 inst==nil 那一支。
-- **台账 #31 「status=stopped 却每天还在长新行,改配置改不掉」:属实,实测确认。**
--
-- ⚠ 最后一行 Meme(running,不是停机策略)是关键:它证明「只挡停机实例」不够。
--    那一仓由 qt-trend-long 开出,却被记到 Meme 名下 —— 活着的策略照样会
--    收养别人的仓,因为判据是"最后下单",不是"谁开的"。

-- ###########################################################################
-- §3 修完之后这些行归谁(只读)
-- ###########################################################################

-- 3.1 先按裁决找开仓方:同 symbol、方向一致(long⇐buy / short⇐sell)、
--     已成交的开仓单,且开仓方唯一。
CREATE TEMPORARY TABLE ghost2 AS
SELECT g.*, o.opener_id, o.opener_name
FROM ghost g
JOIN LATERAL (
  SELECT MIN(x.strategy_id) AS opener_id, MIN(x.strategy_name) AS opener_name,
         COUNT(DISTINCT x.strategy_id) AS n
  FROM strategy_orders x
  WHERE x.purpose = 'entry' AND LOWER(x.status) = 'filled' AND x.executed_qty > 0
    AND x.symbol = g.symbol
    AND x.requested_at BETWEEN g.open_time - INTERVAL 300 SECOND AND g.open_time + INTERVAL 60 SECOND
    AND LOWER(x.side) = CASE WHEN LOWER(g.direction) = 'short' THEN 'sell' ELSE 'buy' END
) o ON o.n = 1;

SELECT strategy_name AS ghost_owner, opener_name, COUNT(*) n,
       ROUND(SUM(realized_pn_l), 6) pnl
FROM ghost2 GROUP BY strategy_name, opener_name ORDER BY n DESC;
-- 实测:71 行里 **49 行**能查出唯一开仓方,**22 行**一张候选都没有。
--   qt-fade-short-v2      → Meme          20 行  +0.336230
--   qt-breakout-follow-v2 → Meme          12 行  -2.584018
--   qt-breakout-follow    → Meme           8 行  -0.470976
--   qt-fade-short         → Meme           8 行  +1.216308
--   Meme                  → qt-trend-long  1 行  +0.061370
--   (48/49 的开仓方是 owner1/Meme —— 跨 owner。这就是为什么收养侧的开仓腿查询
--    **不能**按 owner 过滤:按 owner 查根本看不见真正的开仓方。)

-- 3.2 ⚠⚠ 但**不能**把这 49 行改挂到开仓方名下 —— 那是重复记账。
SELECT CASE WHEN d.n > 0 THEN 'opener_already_has_a_row -> reattributing would DOUBLE COUNT'
            ELSE 'opener_has_no_row -> safe to reattribute' END AS verdict,
       COUNT(*) rows_, ROUND(SUM(g.realized_pn_l), 6) pnl_
FROM ghost2 g
JOIN LATERAL (
  SELECT COUNT(*) AS n FROM strategy_positions q
  WHERE q.strategy_id = g.opener_id AND q.symbol = g.symbol
    AND ABS(TIMESTAMPDIFF(SECOND, q.open_time, g.open_time)) <= 300
) d ON 1 = 1
GROUP BY verdict;
-- 实测:**49 行全部落在 opener_already_has_a_row**,0 行落在 safe_to_reattribute。
--
-- 也就是说:开仓方**早就为同一个净仓建了自己的行**(这正是台账 #91 §4 说的
-- 「一个净仓 2-3 行」)。把幽灵行再改挂过去,同一笔账就会在开仓方名下记两次。
-- 逐行样本(实测):ghost 1935 STAR/USDT 挂 qt-breakout-follow,同一时刻
-- Meme 名下已有 1934;ghost 2096 对应 Meme 的 2095/2102 两行。
--
-- ---------------------------------------------------------------------------
-- **所以「修完归谁」的答案是:一行都不改挂,71 行全部归 unknown。**
--   49 行:开仓方查得出,但它已经记过这笔账 → 改挂 = 重复记账 → 退成 unknown,
--          从 per-strategy 聚合里拿掉。账户级盈亏一分不变(开仓方那一份还在)。
--   22 行:连开仓方都查不出 → 按裁决"不许猜" → unknown。
-- 代码侧的修法和存量侧的修法是两件事:代码侧是**不再铸**这种行,
-- 存量侧是**退役**它们,不是搬家。
-- ---------------------------------------------------------------------------

-- 3.3 退役之后,各策略的对外数字变成什么(只读,#123 口径:只算 closed_qty>0)
SELECT p.strategy_name,
       COUNT(*) AS trades_now, ROUND(SUM(p.realized_pn_l), 6) AS pnl_now,
       COUNT(*) - SUM(g.id IS NOT NULL) AS trades_after,
       ROUND(SUM(p.realized_pn_l) - SUM(COALESCE(g.realized_pn_l, 0)), 6) AS pnl_after
FROM strategy_positions p LEFT JOIN ghost g ON g.id = p.id
WHERE p.closed_qty > 0 GROUP BY p.strategy_name ORDER BY trades_now DESC;
-- 实测 2026-09-09:
--   策略                    笔数 now→after      毛 PnL now→after
--   Meme_合约信号计算引擎_1   680 → 679        +42.992039 → +42.930669
--   qt-trend-long              55 →  55         +2.180675 →  +2.180675   (无幽灵行)
--   qt-fade-short-v2           46 →  23         -0.641514 →  -1.719604
--   qt-breakout-follow-v2      38 →  19         -3.674682 →  -1.866681
--   qt-breakout-follow          5 →   3         -1.220834 →  -0.591918
--   qt-fade-short               3 →   0         +0.972108 →   0
--
-- **两条停机策略的战绩恰好一半是幽灵**(46→23、38→19),
-- **qt-fade-short 的战绩 100% 是幽灵** —— 退役后它一笔可用样本都不剩。
-- 注意方向不一律:qt-fade-short-v2 退役后**更难看**(-0.64 → -1.72),
-- qt-breakout-follow-v2 反而**变好**(-3.67 → -1.87)。幽灵行不是单向偏置,
-- 它只是噪声,而这些策略的样本量小到被噪声主导。

-- 3.4 胜负比的变化(WinRatePct = Wins/(Wins+Losses),pnl 恰为 0 两边都不进)
SELECT p.strategy_name,
       SUM(p.realized_pn_l > 0) AS wins_now, SUM(p.realized_pn_l < 0) AS losses_now,
       SUM(p.realized_pn_l > 0 AND g.id IS NULL) AS wins_after,
       SUM(p.realized_pn_l < 0 AND g.id IS NULL) AS losses_after
FROM strategy_positions p LEFT JOIN ghost g ON g.id = p.id
WHERE p.closed_qty > 0 GROUP BY p.strategy_name;
-- 实测:qt-fade-short-v2      26胜20负 (56.5%) → 10胜13负 (43.5%)  由盈转亏侧
--       qt-breakout-follow-v2 14胜24负 (36.8%) →  6胜13负 (31.6%)
--       qt-fade-short          1胜 2负 (33.3%) →  0胜 0负 (无样本)
--       qt-breakout-follow     1胜 4负 (20.0%) →  1胜 2负 (33.3%)
--       Meme                 351胜327负(51.8%) → 350胜327负(51.7%)
--       qt-trend-long         35胜20负(63.6%) → 不变

-- ###########################################################################
-- §4 回填 —— 默认注释掉。跑之前:scripts/db-backup.sh + 所有者点头 + 写入侧已部署
-- ###########################################################################
--
-- 只做一件事:把这 71 行标成 unknown。**不改 strategy_id,不改 realized_pn_l。**
-- 不删行:它们是"曾经发生过什么"的记录,删掉就查不回来了;要的是从分母里拿掉。
--
-- CREATE TEMPORARY TABLE ghost ... (§1 那段,原样跑一遍)
-- UPDATE strategy_positions p JOIN ghost g ON g.id = p.id
--    SET p.pnl_source = 'unknown', p.updated_at = NOW();
-- 预期影响 71 行。
--
-- 与 realized_pnl_backfill_audit.sql §6.3 的关系:那一段按「没有平仓腿」标 unknown,
-- 本段按「不是这条策略开的」标 unknown。两批有重叠,都是幂等的 SET,先后无所谓。
-- ⚠ 两段合起来才够:幽灵行里有 48 行是 booked(closed_qty>0;23+19+2+3+1),
--   #6.3 的条件(closed_qty=0)一行都盖不到它们。

-- ###########################################################################
-- §5 写入侧(已落地本轮提交,**未部署**)
-- ###########################################################################
--   strategy/position_opener.go  新增 PositionOpener:按「方向一致的开仓腿 +
--       开仓方唯一」定归属;查不出 / 两条策略都开过 → 返回 false,调用方拒绝收养。
--       开仓腿查询**不按 owner 过滤**(实测 48/49 的开仓方在别的 owner 名下)。
--   strategy/manager.go  对账循环改用 PositionOpener;开仓方属于别的 owner 就不收养,
--       留给它自己那一轮;收养出来的行出生即 pnl_source='unknown'。
--   api/positions_handlers.go  第二个铸造点同口径改造 —— 原来先按 config 的 symbol
--       匹配实例、再退到「最近 500 单最后下单的策略」,两条都是"最后动过它的人"。
--   既有的收养守卫(只有 running/starting 能收养)保留:它挡停机策略,
--       PositionOpener 挡"活着但不是它开的"。两道都需要。
--
--   [未验证] 「停机策略不再长新行」要部署后观察 48h 才算数,本轮禁止部署。
