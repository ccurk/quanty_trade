-- 开仓订单账本(台账 #92)体检 + 存量修复 —— strategy_orders / strategy_positions (MySQL)
--
-- 状态:**只读部分已在生产库上跑过**(2026-09-09 07:15~07:47 UTC,quanty-mysql,全程 SELECT)。
-- 标了「实测」的数字都是真跑出来的;§4/§5 的 UPDATE **一行都没执行过**。
--
-- ###########################################################################
-- §0 台账 #92 原话与本次复核结论
-- ###########################################################################
--
-- 台账 #92 写的是:
--   「全窗口只有 244 笔 filled entry order,而 strategy_positions 有 2,824 行;
--     HANA 08-25、BTR 08-26/08-28 任何策略都没有 entry 订单。」
--
-- 复核结论:**两句话都不成立,但底下确实有病,而且比"漏记"更难看。**
--
--   (1) 244 复现不出来。今天 purpose='entry' AND status='filled' 全表 = 3,204 笔;
--       限定在持仓表存在的窗口(>= 2026-07-19 17:32:17)= 752 笔。换过 6 种窗口
--       和过滤组合都到不了 244,原始 SQL 也没留档,**所以这个数不采信**。
--
--   (2) HANA 08-25 / BTR 08-26,08-28 有 entry 订单,是 qt-fade-short-v2 自己下的,
--       sell 方向,executed_qty 和持仓行的 closed_qty 逐笔相等(HANA 1020/1022/1044,
--       BTR 161/129/128/128/131/131)。之所以"查不到",是因为它们的
--       **status 停在 'new'**,按 status='filled' 过滤就一条都不剩。
--
--   (3) 真正的病:**订单行不是没写,是状态没回写。** 见 §2。
--
-- 今日两个数(实测 2026-09-09 07:47 UTC):
--   strategy_positions            2,834 行(其中 2,007 行 closed_qty=0,是空壳)
--   purpose='entry' 且非 failed    4,634 笔(filled 3,204 / new 1,418 / partial 12)
--   持仓表窗口内的 entry 腿        1,469 笔(filled 752 + new 708 + 其它 9)
--   窗口内 owner+策略+symbol 净仓  1,672 个(去掉重复行前是 2,833 行)
--   共享账户口径的净仓            1,473 个,其中 **1,469 个查得到开仓单,只有 4 个查不到**

-- ###########################################################################
-- §1 两个头条数字(照抄可跑)
-- ###########################################################################

-- 1a. 持仓行数 / 空壳行数
SELECT COUNT(*) rows_, SUM(closed_qty > 0) with_close_leg,
       SUM(closed_qty = 0 OR closed_qty IS NULL) empty_shell
FROM strategy_positions;
-- 实测 2026-09-09 07:47 UTC: 2834 / 827 / 2007

-- 1b. entry 订单按状态 —— 注意 new 这一行
SELECT status, order_type, COUNT(*) c,
       SUM(executed_qty > 0) exq_gt0, SUM(avg_price > 0) avgpx_gt0
FROM strategy_orders WHERE purpose = 'entry' GROUP BY 1, 2 ORDER BY c DESC;
-- 实测:
--   failed  market 3247 | exq_gt0    0 | avgpx_gt0    0
--   filled  market 3204 | exq_gt0 3204 | avgpx_gt0 3204
--   new     market 1418 | exq_gt0 1418 | avgpx_gt0    0   <-- 1,418 笔"有量、无价、状态 new"
--   rolled_back    900  | exq_gt0  900 | avgpx_gt0  777
--   partially_filled 12 | exq_gt0   12 | avgpx_gt0   12

-- ###########################################################################
-- §2 病灶:状态回写在下单那一刻就丢了,而且此后没有任何东西会来纠正
-- ###########################################################################
--
-- 代码链条(全部在已部署镜像 ...-d933d91 里,不是新引入的):
--
--   backend/internal/exchange/binance.go:1443
--       if status != "filled" || avgPx == 0 || executedQty == 0 {
--           if refreshed, err := b.waitUSDMOrderFinal(...); err == nil && refreshed != nil { ... }
--       }
--     ← `err == nil &&` 把错误**整个丢掉,且不打日志**。刷新失败 = 什么都没发生。
--
--   backend/internal/exchange/binance.go:1451-1456
--       aq := origQty
--       if executedQty > 0 { aq = executedQty }
--       if avgPx > 0 { px = avgPx }
--     ← 刷新失败时 avgPx 仍是 0,于是 px = resp.Price = 0(市价单回包的 price 就是 "0")。
--       而 aq 退回 origQty —— 订单行于是写着"成交了 129 张,成交价 0"。
--
--   backend/internal/strategy/strategy_position.go:351-358
--       Updates({"status": order.Status, "executed_qty": order.Amount, "avg_price": order.Price})
--     ← 原样落库:status='new',executed_qty=下单量,avg_price=0。
--
--   backend/internal/exchange/binance_user_stream.go:~75
--     唯一会事后把 status 改成 filled 的东西是 User Data Stream 的
--     ORDER_TRADE_UPDATE 回写(binance_user_stream.go:417 / :545),
--     而它对 usdm 长期是 `return nil` 的死代码(已在提交 197e8e1 修好,**尚未部署**)。
--     生产库 exchange_order_events **今天仍是 0 行** —— 这条流一次都没跑过。
--
-- 为什么确定是"刷新失败"而不是"订单真的没成交":
--   waitUSDMOrderFinal(binance.go:1556-1581)最多轮询 30×200ms = 6s。
--   如果它跑满 6s 才放弃,订单行的 updated_at - requested_at 会 ≈ 6s。实测:
SELECT status, COUNT(*) c,
       ROUND(MIN(TIMESTAMPDIFF(MICROSECOND, requested_at, updated_at)) / 1e6, 3) min_s,
       ROUND(AVG(TIMESTAMPDIFF(MICROSECOND, requested_at, updated_at)) / 1e6, 3) avg_s,
       ROUND(MAX(TIMESTAMPDIFF(MICROSECOND, requested_at, updated_at)) / 1e6, 3) max_s,
       SUM(TIMESTAMPDIFF(MICROSECOND, requested_at, updated_at) / 1e6 BETWEEN 5.5 AND 8) around_6s
FROM strategy_orders WHERE purpose = 'entry' AND status IN ('new', 'filled') GROUP BY 1;
-- 实测: new 1418 笔 min 0.021s / avg 0.036s / max 2.956s / **落在 6s 附近的 0 笔**
--       filled 3204 笔 min 0.016s / avg 0.030s / max 0.666s
--   → 6s 轮询从来没跑到头,只可能是 signedRequest 立刻报错、被 `err == nil` 吞掉。
--   具体是哪个 Binance 错误码**查不出来,因为那行错误压根没记**——这本身是要修的。
--
-- 另一处**真·不写单**(与上面不同,是一行订单都没有):
--   backend/internal/strategy/strategy_pyramid.go:96  PlaceOrder 前后都不建 StrategyOrder,
--   直接在 :119-121 改 strategy_positions.amount / avg_price。
--   金字塔加仓因此在订单账本里完全不可见。库里 client_order_id LIKE 'pyr%' = 0 行,
--   但这**证明不了它没跑过**(它本来就不写),而 strategy_logs 只留到 2026-09-07,
--   历史日志已被清。定性为**潜在缺口,量测不了**,不编数字。

-- ###########################################################################
-- §3 「漏记」和「本来就不该有自己的开仓单」拆开
-- ###########################################################################
--
-- 口径:一个"净仓"= 同 owner+策略+symbol、开仓时刻 60s 内的一组持仓行(重复行折成一个)。
-- 窗口 = 持仓表存在的全部时间(2026-07-19 17:32 起)。实测 1,672 个净仓 / 2,833 行,
-- 即 **1,161 行是同一个净仓的第 2/3/4 行**(2-4 行开仓时刻毫秒级相同、avg_price 相同,
-- 只有一行带 closed_qty/realized_pn_l,其余是空壳)。
--
--   A  自己有 filled 开仓单                752 个净仓 / 1,335 行 / |PnL| 51.8%
--   B  自己有开仓单但状态没回写(new 等)   717 个净仓 / 1,277 行 / |PnL| 41.4%   ← **漏记**
--   C  同一时刻的 filled 开仓单挂在别的     130 个净仓 /   142 行 / |PnL|  4.6%   ← **本来就不该有**
--      owner/策略名下(共享账户同住人)
--   D  我们账本里任何 owner 都没有开仓单     73 个净仓 /    79 行 / |PnL|  2.1%
--
-- B 是漏记,证据是硬的:708 个 B 净仓的开仓行在对应订单 requested_at 之后
-- **11ms ~ 1.7s** 被建出来(p50 = 17ms),且 272/277 个能比数量的案例里
-- executed_qty 与持仓 closed_qty 相差 <1%。同一笔单,不是两笔。
--
-- C/D 不是漏记:共享同一个 Binance 账户,同一个净仓被多个 owner 的对账循环各铸一行
-- (manager.go:717 收养、strategy_roi_monitor.go:224 补录、strategy_position.go:437
-- TPSL 兜底、api/positions_handlers.go:301/635)。这些行**本来就没有属于自己的开仓单**,
-- 按 #31 的裁决是**退役,不是改挂** —— 开仓方早就为同一净仓建了自己的行。
--
-- 换成共享账户口径(同一 symbol 上只有一个净仓,不分 owner):
--   1,473 个净仓,**1,469 个查得到唯一开仓方,4 个查不到**;开仓方有歧义(两条策略
--   同时下单)的 **0 个**。那 4 个全是 Meme 名下 closed_qty=0 / realized_pn_l=0 的空壳,
--   合计 PnL 恰好 0.0000 —— 对任何决策都不产生影响。
--
-- 复核 C/D 的查询:
SELECT
  COUNT(*) nets,
  SUM(own_filled) a_own_filled,
  SUM(own_filled = 0 AND own_any = 1) b_own_status_lost,
  SUM(own_filled = 0 AND own_any = 0 AND other_any = 1) c_other_owner,
  SUM(own_filled = 0 AND own_any = 0 AND other_any = 0) d_nobody
FROM (
  SELECT p.id,
    EXISTS(SELECT 1 FROM strategy_orders o WHERE o.purpose = 'entry' AND o.status = 'filled'
       AND o.owner_id = p.owner_id AND o.strategy_id = p.strategy_id AND o.symbol = p.symbol
       AND o.requested_at BETWEEN p.open_time - INTERVAL 60 SECOND AND p.open_time + INTERVAL 60 SECOND) own_filled,
    EXISTS(SELECT 1 FROM strategy_orders o WHERE o.purpose = 'entry' AND o.status <> 'failed'
       AND o.owner_id = p.owner_id AND o.strategy_id = p.strategy_id AND o.symbol = p.symbol
       AND o.requested_at BETWEEN p.open_time - INTERVAL 60 SECOND AND p.open_time + INTERVAL 60 SECOND) own_any,
    EXISTS(SELECT 1 FROM strategy_orders o WHERE o.purpose = 'entry' AND o.status <> 'failed'
       AND o.symbol = p.symbol
       AND o.requested_at BETWEEN p.open_time - INTERVAL 60 SECOND AND p.open_time + INTERVAL 60 SECOND) other_any
  FROM strategy_positions p) t;
-- 实测 2026-09-09 07:52 UTC: nets(其实是行数) 2834 | A 1336 | B 1277 | C 217 | D 4
--
-- 注意这条 SQL 是**按行**统计,不是按净仓,而且 C 列里的 other_any 放宽到了
-- "任何 owner 的任何非 failed 开仓单",所以 C 偏大、D 偏小(4 行)。
-- 上面 A/B/C/D 那张表是**按净仓**的口径(重复行折成一个、C 只认 filled),
-- 折叠那一步在 Python 里做:同 owner+策略+symbol、开仓时刻 60s 内聚成一组。
-- 两个口径都对,别混着引用。

-- ###########################################################################
-- §4 存量修复(一):把 708 笔状态丢失的开仓单改回 filled —— **未执行**
-- ###########################################################################
--
-- 能修多少、需不需要交易所:
--   1,418 笔 status='new' 的 entry 单,按 requested_at 切得**干干净净**:
SELECT (requested_at >= '2026-07-19 17:32:17') in_position_window,
       EXISTS(SELECT 1 FROM strategy_positions p
          WHERE p.owner_id = o.owner_id AND p.strategy_id = o.strategy_id AND p.symbol = o.symbol
            AND p.open_time BETWEEN o.requested_at AND o.requested_at + INTERVAL 2 SECOND
            AND p.avg_price > 0) corroborated,
       COUNT(*) c, MIN(requested_at) mn, MAX(requested_at) mx
FROM strategy_orders o WHERE o.purpose = 'entry' AND o.status = 'new' GROUP BY 1, 2;
-- 实测:
--   窗口外 / 无佐证  710 笔  2026-03-29 20:09 ~ 2026-07-19 16:55
--   窗口内 / 有佐证  708 笔  2026-07-19 18:15 ~ 2026-09-09 01:07
-- 也就是说:**持仓表开始记账之后的每一笔状态丢失的开仓单,都能在库内自证**,
-- 一次交易所调用都不需要。窗口外那 710 笔早于第一行持仓(2026-07-19 17:32),
-- 没有可对照的对象,**放弃,不猜**。
--
-- 价格取自那一刻被铸出来的持仓行(它的 avg_price 来自交易所快照,是真成交价)。
-- 歧义检查:708 笔里 707 笔候选价唯一,1 笔有 2 个候选价 —— 下面的 HAVING 把它排掉。
--
-- BEGIN;
-- UPDATE strategy_orders o
--   JOIN (
--     SELECT o2.id, MIN(p.avg_price) px
--     FROM strategy_orders o2
--     JOIN strategy_positions p
--       ON p.owner_id = o2.owner_id AND p.strategy_id = o2.strategy_id AND p.symbol = o2.symbol
--      AND p.open_time BETWEEN o2.requested_at AND o2.requested_at + INTERVAL 2 SECOND
--      AND p.avg_price > 0
--     WHERE o2.purpose = 'entry' AND o2.status = 'new'
--     GROUP BY o2.id
--     HAVING COUNT(DISTINCT ROUND(p.avg_price, 10)) = 1     -- 候选价不唯一的那 1 笔不动
--   ) m ON m.id = o.id
--   SET o.status = 'filled', o.avg_price = m.px, o.updated_at = NOW()
--   WHERE o.purpose = 'entry' AND o.status = 'new';
-- -- 期望 Rows matched: 707
-- ROLLBACK;   -- 确认行数无误后再改成 COMMIT
--
-- **executed_qty 一个字都不要动。** 它写的是 origQty(下单取整后的量),
-- 实测 272/277 个可比案例里与持仓 closed_qty 相差 <1%,是可用的;
-- 而"改得更准"没有第二个数据源可依据,改了就是编。

-- ###########################################################################
-- §5 存量修复(二):给开仓单补上 position_id —— **未执行**
-- ###########################################################################
--
-- 生产库 purpose='entry' 的 8,781 行 **position_id 全是 0**(平仓单反而有:
-- close 单 964 行有值)。根因是开仓单行在 PlaceOrder 之前就建好了
-- (strategy_position.go:259-282),那时持仓行还不存在;
-- applyOrderFillToPosition(manager.go:1340)明明 return 了持仓 id,
-- 调用方 strategy_position.go:373 却把它丢了。**写入侧本轮已修。**
--
-- 存量能补多少(窗口内 1,469 笔 entry 单,只认"2s 内恰好一行带平仓腿"的):
SELECT n_leg, COUNT(*) orders_ FROM (
  SELECT o.id, (SELECT COUNT(*) FROM strategy_positions p
     WHERE p.owner_id = o.owner_id AND p.strategy_id = o.strategy_id AND p.symbol = o.symbol
       AND p.open_time BETWEEN o.requested_at AND o.requested_at + INTERVAL 2 SECOND
       AND p.closed_qty > 0) n_leg
  FROM strategy_orders o
  WHERE o.purpose = 'entry' AND o.status IN ('filled', 'new', 'partially_filled')
    AND o.requested_at >= '2026-07-19 17:32:17') t
GROUP BY n_leg ORDER BY n_leg;
-- 实测: n_leg=0 → 789 笔(对应持仓行全是 closed_qty=0 的空壳,#122 的 unknown population)
--       n_leg=1 → 653 笔  ← 可补
--       n_leg=2 →  27 笔(重复行没退役之前认不准,不补)
--
-- BEGIN;
-- UPDATE strategy_orders o
--   JOIN (
--     SELECT o2.id, MIN(p.id) pid
--     FROM strategy_orders o2
--     JOIN strategy_positions p
--       ON p.owner_id = o2.owner_id AND p.strategy_id = o2.strategy_id AND p.symbol = o2.symbol
--      AND p.open_time BETWEEN o2.requested_at AND o2.requested_at + INTERVAL 2 SECOND
--      AND p.closed_qty > 0
--     WHERE o2.purpose = 'entry' AND o2.status IN ('filled', 'new', 'partially_filled')
--       AND o2.requested_at >= '2026-07-19 17:32:17'
--     GROUP BY o2.id HAVING COUNT(*) = 1
--   ) m ON m.id = o.id
--   SET o.position_id = m.pid, o.updated_at = NOW()
--   WHERE o.position_id = 0;
-- -- 期望 Rows matched: 653
-- ROLLBACK;
--
-- 顺序要求:§5 必须在 #31 的重复行退役**之后**跑,否则那 27 笔歧义会变多不会变少。

-- ###########################################################################
-- §6 跑完之后的验收
-- ###########################################################################

-- 6a. 窗口内不应再有 status='new' 的 entry 单
SELECT COUNT(*) should_be_711 FROM strategy_orders
WHERE purpose = 'entry' AND status = 'new';
-- 期望 1418 - 707 = 711(全部是 2026-07-19 之前、无佐证的)

-- 6b. status='filled' 却没有成交价的行数必须是 0
SELECT COUNT(*) must_be_0 FROM strategy_orders
WHERE purpose = 'entry' AND status = 'filled' AND (avg_price IS NULL OR avg_price = 0);

-- 6c. 改前/改后逐笔比对:被改的行只能动 status / avg_price / updated_at
--     跑 §4 之前先存一份:
-- CREATE TABLE _bak_entry_status_20260909 AS
--   SELECT id, status, avg_price, executed_qty, position_id, updated_at
--   FROM strategy_orders WHERE purpose = 'entry' AND status = 'new';

-- 6d. 账面总盈亏不许变(本次只改订单表,持仓表一个字不动)
SELECT ROUND(SUM(realized_pn_l), 6) total_pnl FROM strategy_positions;
-- 实测改前: 40.607794

-- ###########################################################################
-- §7 补不回来的部分,按 #122 三态处理
-- ###########################################################################
--
-- 今天有没有交易所侧数据源:**没有。**
--   exchange_fills            表在生产库不存在(#91 已证)
--   exchange_order_events     0 行(今天复测仍是 0;这条流对 usdm 从没跑过)
--   Binance REST userTrades / income  需要用生产 API key 签名。那把 key 正是
--     所有者待办 #9 里标 P0、等着吊销的同一把(指纹 bd5ee4cd1225),
--     **本轮没有调用,也不建议在吊销/轮换之前调用。**
--
-- 好消息是:**需要交易所的部分极小。**
--   · §4 的 708 笔、§5 的 653 笔,数据源都在库内,零外部依赖。
--   · 共享账户口径下真正无从归属的净仓只有 4 个,合计 PnL 恰好 0.0000。
--   · 剩下真正拿不回来的是**平仓腿**不是开仓腿:2,007 行 closed_qty=0 的空壳
--     (#122 的 unknown population),那是另一张卡,处置办法已定 ——
--     pnl_source='unknown',per-strategy 聚合排除、account 级保留,不当 0 进分母。
--     scripts/pnl_source_column.sql 是那一步的 migration(同样未执行)。
