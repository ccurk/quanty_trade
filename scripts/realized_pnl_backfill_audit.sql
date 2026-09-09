-- strategy_positions.realized_pn_l / direction 存量审计与回填 (MySQL) —— 台账 #91
--
-- 状态:**本文件的每一段都未在生产库上跑过**。写它的这一轮拿不到实盘库口令
-- (conf/conf_pro.yaml 的 db.pass 恒为空,真口令走部署期环境变量),所以下面的
-- 数字全是"应该这么算",不是"实测是这样"。谁先跑,请把 §0/§1 的输出贴回台账 #91。
--
-- 用法纪律:
--   * §0 §1 §2 §3 全是 SELECT,只读,随便跑。
--   * §4 是回填 UPDATE,**默认注释掉,不要直接执行**。必须先满足 §1 的三个前提,
--     再按 §3 逐行核对过 id=1897 / id=1966,再由所有者拍板。
--   * 回填前先做一次 scripts/db-backup.sh。
--
-- 为什么能回填:平仓盈亏不需要重算。exchange_fills 是 Binance /fapi/v1/userTrades
-- 的原样镜像,realized_pn_l 是交易所自己算好的每笔平仓成交盈亏。所以台账 #92 那个
-- "244 笔 filled entry vs 2,824 行 position" 的开仓腿缺口**挡不住这条路** —— 它挡住
-- 的是"拿开仓价 × 数量重算"那条路。这里一次都不需要开仓价。
--
-- 为什么不是全部能回填:见 §1 的三道闸。

-- ---------------------------------------------------------------------------
-- §0 先看清坏成什么样(只读)
-- ---------------------------------------------------------------------------

-- 0.1 realized_pn_l = 0 的已平仓行有多少、占多少
SELECT strategy_id,
       COUNT(*)                                            AS closed_rows,
       SUM(CASE WHEN realized_pn_l = 0 THEN 1 ELSE 0 END)  AS zero_pnl_rows,
       SUM(CASE WHEN closed_qty  = 0 THEN 1 ELSE 0 END)    AS zero_closed_qty_rows,
       ROUND(SUM(realized_pn_l), 4)                        AS sum_pn_l
FROM strategy_positions
WHERE owner_id = @owner AND status = 'closed'
GROUP BY strategy_id
ORDER BY zero_pnl_rows DESC;

-- 0.2 direction 与平仓单方向矛盾的行(买入平仓 ⇒ 该仓只可能是空头)
--     这就是台账 #91 里"direction 记 long 而平仓是 buy"那批。
SELECT p.id, p.strategy_id, p.symbol,
       p.direction                                                   AS stored_direction,
       CASE WHEN LOWER(o.side) = 'buy' THEN 'short' ELSE 'long' END   AS implied_direction,
       o.side, o.executed_qty, o.avg_price, p.realized_pn_l, p.close_time
FROM strategy_positions p
JOIN strategy_orders   o ON o.position_id = p.id
                        AND o.purpose = 'close'
                        AND LOWER(o.status) = 'filled'
WHERE p.owner_id = @owner
  AND p.status = 'closed'
  AND LOWER(p.direction) <> CASE WHEN LOWER(o.side) = 'buy' THEN 'short' ELSE 'long' END
ORDER BY p.id;

-- ---------------------------------------------------------------------------
-- §1 回填的三道前提闸 —— 任何一道不过,对应那部分行就是"不能回填",别硬来
-- ---------------------------------------------------------------------------

-- 1.1 exchange_fills 的时间覆盖是否包住了 strategy_positions
--     覆盖不到的时间段一律不能回填(exchange_fills 由 daily_pnl_job 拉,限频时
--     会 return 部分结果,历史不保证拉全)。
SELECT (SELECT MIN(trade_time) FROM exchange_fills)  AS fills_from,
       (SELECT MAX(trade_time) FROM exchange_fills)  AS fills_to,
       (SELECT COUNT(*)        FROM exchange_fills)  AS fills_rows,
       (SELECT MIN(open_time)  FROM strategy_positions WHERE owner_id = @owner) AS pos_from,
       (SELECT MAX(close_time) FROM strategy_positions WHERE owner_id = @owner AND status = 'closed') AS pos_to;

-- 1.2 平仓单能不能 join 上 fills。join 键是 strategy_orders.exchange_order_id
--     ↔ exchange_fills.order_id —— 这个 join 已经在 attribution 的手续费子查询里
--     用着(strategy_attribution.go:196),不是新发明的。
SELECT o.purpose,
       COUNT(*)                                            AS filled_orders,
       SUM(CASE WHEN x.order_id IS NULL THEN 1 ELSE 0 END) AS orders_without_fill
FROM strategy_orders o
LEFT JOIN (SELECT DISTINCT order_id FROM exchange_fills) x
       ON x.order_id = o.exchange_order_id
WHERE o.owner_id = @owner AND LOWER(o.status) = 'filled'
GROUP BY o.purpose;

-- 1.3 平仓单挂没挂上 position_id。position_id = 0 的那些正是本 bug 自己制造的
--     孤儿:closeUSDMPosition 先 First(&pos) 查开仓行,查不到 pos 是零值,紧接着
--     写平仓单时 PositionID 就落了 0(strategy_execution.go:424)。
SELECT COUNT(*)                                          AS close_orders,
       SUM(CASE WHEN position_id = 0 THEN 1 ELSE 0 END)  AS orphan_close_orders
FROM strategy_orders
WHERE owner_id = @owner AND purpose = 'close' AND LOWER(status) = 'filled';

-- ---------------------------------------------------------------------------
-- §2 重算(只读)。算出来先看 delta,别急着写回去
-- ---------------------------------------------------------------------------
--
-- 说明:SUM(x.realized_pn_l) 不需要过滤开仓腿 —— 开仓成交的 realizedPnl 恒为 0
-- (models.go ExchangeFill.RealizedPnL 的注释),加进来是加 0。
--
-- 已知取舍:账户是多 owner 共用的(台账:重复记账/限流根因)。这里靠
-- strategy_orders 做 owner 归属,如果两个 owner 的 strategy_orders 出现同一个
-- exchange_order_id,这条 join 会重复归属。跑 §2 时先确认下面这句返回 0 行:
--   SELECT exchange_order_id, COUNT(DISTINCT owner_id) c FROM strategy_orders
--   WHERE exchange_order_id <> '' GROUP BY exchange_order_id HAVING c > 1;

SELECT p.id,
       p.strategy_id, p.symbol, p.direction,
       p.closed_qty,
       p.realized_pn_l                                   AS stored_pn_l,
       COALESCE(r.exch_pn_l, 0)                          AS exchange_pn_l,
       ROUND(COALESCE(r.exch_pn_l, 0) - p.realized_pn_l, 8) AS delta,
       COALESCE(r.fills, 0)                              AS matched_fills
FROM strategy_positions p
LEFT JOIN (
    SELECT o.position_id        AS position_id,
           SUM(x.realized_pn_l) AS exch_pn_l,
           COUNT(*)             AS fills
    FROM exchange_fills x
    JOIN strategy_orders o ON o.exchange_order_id = x.order_id
    WHERE o.owner_id = @owner AND o.position_id > 0
    GROUP BY o.position_id
) r ON r.position_id = p.id
WHERE p.owner_id = @owner AND p.status = 'closed'
ORDER BY ABS(COALESCE(r.exch_pn_l, 0) - p.realized_pn_l) DESC;

-- 汇总口径会怎么变(这才是所有者关心的那个数)
SELECT ROUND(SUM(p.realized_pn_l), 4)              AS sum_stored,
       ROUND(SUM(COALESCE(r.exch_pn_l, 0)), 4)     AS sum_exchange,
       SUM(CASE WHEN r.position_id IS NULL THEN 1 ELSE 0 END) AS rows_not_recoverable
FROM strategy_positions p
LEFT JOIN (
    SELECT o.position_id AS position_id, SUM(x.realized_pn_l) AS exch_pn_l
    FROM exchange_fills x
    JOIN strategy_orders o ON o.exchange_order_id = x.order_id
    WHERE o.owner_id = @owner AND o.position_id > 0
    GROUP BY o.position_id
) r ON r.position_id = p.id
WHERE p.owner_id = @owner AND p.status = 'closed';

-- ---------------------------------------------------------------------------
-- §3 台账点名的那两行,逐笔摊开手工核对(只读)
-- ---------------------------------------------------------------------------
-- id=1897 STAR,平仓单 676 @ 0.08014;id=1966 AKE,平仓单 2874 @ 0.0102008。
-- 注意:如果这两行是被 bug 现造出来的假仓,o.position_id 就是 0,下面按
-- position_id 的 join 会是空 —— 那说明它们落在"不能按 position_id 回填"那一类,
-- 只能退到按 symbol + [open_time, close_time] 时间窗匹配(见 §5 的告警)。
SELECT p.id, p.symbol, p.direction, p.avg_price, p.avg_close_price,
       p.closed_qty, p.realized_pn_l, p.open_time, p.close_time,
       o.id AS order_id, o.side, o.purpose, o.status,
       o.executed_qty, o.avg_price AS order_avg_price, o.exchange_order_id,
       x.trade_id, x.side AS fill_side, x.qty, x.price,
       x.realized_pn_l AS fill_pn_l, x.commission, x.commission_asset, x.trade_time
FROM strategy_positions p
LEFT JOIN strategy_orders o ON o.position_id = p.id
LEFT JOIN exchange_fills  x ON x.order_id = o.exchange_order_id
WHERE p.id IN (1897, 1966)
ORDER BY p.id, o.id, x.trade_id;

-- 同两个 symbol 的全部平仓单(不经 position_id,用来找那两行真正对应的成交)
SELECT o.id, o.position_id, o.strategy_id, o.symbol, o.side, o.purpose, o.status,
       o.executed_qty, o.avg_price, o.exchange_order_id, o.requested_at
FROM strategy_orders o
WHERE o.owner_id = @owner
  AND o.symbol IN ('STAR/USDT', 'AKE/USDT')
  AND LOWER(o.status) = 'filled'
ORDER BY o.symbol, o.requested_at;

-- ---------------------------------------------------------------------------
-- §4 回填 —— 默认注释掉。跑之前:§1 三闸全过 + §3 手工核对一致 + 已备份 + 所有者点头
-- ---------------------------------------------------------------------------
--
-- 4.1 direction 按平仓单方向纠正(只改与平仓单矛盾的那些行)
-- UPDATE strategy_positions p
-- JOIN strategy_orders o ON o.position_id = p.id
--                       AND o.purpose = 'close'
--                       AND LOWER(o.status) = 'filled'
-- SET p.direction = CASE WHEN LOWER(o.side) = 'buy' THEN 'short' ELSE 'long' END,
--     p.updated_at = NOW()
-- WHERE p.owner_id = @owner
--   AND p.status = 'closed'
--   AND LOWER(p.direction) <> CASE WHEN LOWER(o.side) = 'buy' THEN 'short' ELSE 'long' END;
--
-- 4.2 realized_pn_l 用交易所的数覆盖(只动 §2 里 matched_fills > 0 的行)
-- UPDATE strategy_positions p
-- JOIN (
--     SELECT o.position_id AS position_id, SUM(x.realized_pn_l) AS exch_pn_l
--     FROM exchange_fills x
--     JOIN strategy_orders o ON o.exchange_order_id = x.order_id
--     WHERE o.owner_id = @owner AND o.position_id > 0
--     GROUP BY o.position_id
-- ) r ON r.position_id = p.id
-- SET p.realized_pn_l = r.exch_pn_l,
--     p.updated_at = NOW()
-- WHERE p.owner_id = @owner AND p.status = 'closed';

-- ---------------------------------------------------------------------------
-- §5 回填**不可能**覆盖的三类行 —— 这些行回填完仍然是错的,必须单独标注
-- ---------------------------------------------------------------------------
--   (a) 平仓单 position_id = 0 的孤儿(见 §1.3):平台侧下过单,但当时找不到持仓行。
--   (b) 根本没有 strategy_order 的平仓:交易所侧 TP/SL 触发、强平、用户在币安手工平。
--       这些走 manager.go SyncRedisOpenCountsFromExchange 的 stale-close 分支,
--       靠 fetchStaleRealizedPnL 拉 income;拉失败就一个字不写,行以 0 收尾。
--   (c) exchange_fills 未覆盖的时间段(见 §1.1)。
--
-- 对 (a)(b) 还剩一条退路:按 symbol + [open_time, close_time] 时间窗把 fills 归到
-- position。**这条只能当参考,不能当账** —— 共享账户下同 symbol 并发交易会串账,
-- manager.go fetchStaleRealizedPnL 的注释已经承认了这个取舍。下面这句只用来量
-- "还剩多少钱没归属",不要拿它去 UPDATE:
SELECT COUNT(*) AS unmatched_fills,
       ROUND(SUM(x.realized_pn_l), 4) AS unmatched_pn_l
FROM exchange_fills x
LEFT JOIN strategy_orders o ON o.exchange_order_id = x.order_id AND o.position_id > 0
WHERE x.realized_pn_l <> 0 AND o.id IS NULL;
