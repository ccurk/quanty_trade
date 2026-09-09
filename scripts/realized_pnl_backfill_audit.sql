-- strategy_positions.realized_pn_l / direction 存量审计与回填 (MySQL) —— 台账 #91
--
-- 状态:**已在生产库上跑过,只读部分**(2026-09-09,quanty-mysql,只 SELECT)。
-- 下面标了「实测」的数字都是真跑出来的;§6 的 UPDATE 一行都没执行过。
-- 上一版这个文件写着"每一段都未跑过、数字全是应该这么算" —— 本版把它替换掉。
--
-- 用法纪律:
--   * §0 §1 §2 §3 §5 全是 SELECT,只读,随便跑。
--   * §6 是回填 UPDATE,**默认注释掉**。执行前:先 scripts/db-backup.sh,
--     再逐行核对 §3,再由所有者拍板。此刻线上库还开在公网 3306(台账 #114)。
--   * 写入侧要先落地(见 §7),否则回填完的行还会被新的成交再写坏一次。

-- ###########################################################################
-- §0 先说清楚:上一版的两个前提在生产库上都不成立
-- ###########################################################################
--
-- (a) exchange_fills 表在生产库【不存在】。
--     SHOW TABLES 实测只有 18 张,没有 exchange_fills,也没有
--     strategy_param_versions —— 线上跑的镜像是 d933d91(2026-09-06),这两张表
--     的模型是它之后才进 main 的,AutoMigrate 要等下一次部署才会建。
--     后果:上一版 §1.1/§1.2/§2/§4.2/§5 全部依赖 exchange_fills,现在一句都跑不了。
--     「从交易所 REALIZED_PNL 回填」这条路**今天不通**,不是"数字待测",是没有数据源。
--
-- (b) 因此今天唯一能回填的来源是本系统自己的 strategy_orders 平仓腿。
--     它只覆盖 6 行(见 §2)。剩下的 2,000 行没有任何本地数据源。

-- ###########################################################################
-- §1 坏成什么样(只读)—— 实测结果直接写在每段下面
-- ###########################################################################

-- 1.1 全表口径
SELECT COUNT(*)                                                    AS total,
       SUM(closed_qty > 0)                                         AS booked,
       SUM(closed_qty IS NULL OR closed_qty = 0)                   AS shells,
       SUM(status = 'closed')                                      AS closed_rows
FROM strategy_positions;
-- 实测 2026-09-09:total=2830  booked=824  shells=2006  closed_rows=2830
--   (全表 2,830 行【每一行】status 都是 'closed';库里此刻没有 open 行。)
--   2,006 行是「已平仓但没有平仓腿」的空壳:closed_qty=0 且 realized_pn_l=0,
--   而且这 2,006 行的 realized_pn_l 无一例外全是 0 —— 它们的 0 是"没查到",
--   不是"打平"。这正是 #122 加 pnl_source 列要分开的两种 0。

-- 1.2 direction 与平仓单方向矛盾的行(买入平仓 ⇒ 该仓只可能是空头)
SELECT p.direction, o.side AS close_side, COUNT(*) AS positions,
       SUM(p.closed_qty > 0) AS with_closed_qty
FROM strategy_positions p
JOIN (SELECT DISTINCT position_id, side FROM strategy_orders
      WHERE purpose = 'close' AND status = 'filled' AND executed_qty > 0
        AND position_id IS NOT NULL) o ON o.position_id = p.id
GROUP BY p.direction, o.side ORDER BY p.direction, o.side;
-- 实测 2026-09-09:
--   direction=long  close_side=sell   208 行   (207 有 closed_qty)   ← 自洽
--   direction=short close_side=buy    193 行   (193 有 closed_qty)   ← 自洽
--   direction=long  close_side=buy      4 行   (  0 有 closed_qty)   ← 矛盾
--   direction=''    close_side=sell     1 行   (  0 有 closed_qty)   ← 缺失
--
-- **定性结论(台账 #91 问的就是这个):direction 是「个别行写错」,不是「语义理解错」。**
-- 401/406 行方向与平仓单自洽,读侧和写侧用的是同一套约定(long 由 sell 平、
-- short 由 buy 平)。所以修法是【改写入侧 + 定点回填这几行】,不是去改所有读它
-- 的地方。矛盾的 4 行恰好就是 closed_qty=0 的那批 —— 方向错和盈亏丢是同一件事。

-- ###########################################################################
-- §2 能回填多少 / 永远补不回多少(只读)—— 这是台账 #91 要的那两个数
-- ###########################################################################

SELECT CASE WHEN o.position_id IS NULL THEN 'no_filled_close_order'
            ELSE 'has_filled_close_order' END AS bucket,
       COUNT(*) AS n
FROM strategy_positions p
LEFT JOIN (SELECT DISTINCT position_id FROM strategy_orders
           WHERE purpose = 'close' AND status = 'filled' AND executed_qty > 0
             AND position_id IS NOT NULL) o ON o.position_id = p.id
WHERE p.status = 'closed' AND (p.closed_qty IS NULL OR p.closed_qty = 0)
GROUP BY bucket;
-- 实测:has_filled_close_order = 6      ← 可回填
--       no_filled_close_order  = 2000   ← 本地无数据源

-- 2,000 行再拆一刀:其中有多少只是「同一个净仓的重复行」?
SELECT CASE WHEN EXISTS (SELECT 1 FROM strategy_positions q
                         WHERE q.id <> p.id AND q.symbol = p.symbol AND q.closed_qty > 0
                           AND ABS(TIMESTAMPDIFF(SECOND, q.open_time, p.open_time)) <= 60)
            THEN 'dup_of_booked_row' ELSE 'truly_unknown' END AS bucket,
       COUNT(*) AS n, MIN(p.open_time) AS first_open, MAX(p.open_time) AS last_open
FROM strategy_positions p
LEFT JOIN (SELECT DISTINCT position_id FROM strategy_orders
           WHERE purpose = 'close' AND status = 'filled' AND executed_qty > 0
             AND position_id IS NOT NULL) o ON o.position_id = p.id
WHERE p.status = 'closed' AND (p.closed_qty IS NULL OR p.closed_qty = 0)
  AND o.position_id IS NULL
GROUP BY bucket;
-- 实测:dup_of_booked_row = 596   (2026-07-19 → 2026-08-23)
--       truly_unknown     = 1404  (2026-07-19 → 2026-08-29)
--
-- ---------------------------------------------------------------------------
-- 所以三句话回答「回填能覆盖多少」:
--
--   可回填            6 行   有自己的 filled 平仓单,盈亏能从库里算出来(§3)。
--   不该回填        596 行   同一个净仓的重复行,兄弟行已经把这笔记过账了。
--                            给它们补一份盈亏 = 账户级重复记账。它们要的是
--                            被标成重复行、从分母里拿掉,不是被补数。
--   永远补不回     1404 行   交易所侧平仓(TP/SL 在币安成交、强平、手工平),
--                            平台从头到尾没记过平仓价,本地也没有 exchange_fills。
--
-- 「永远补不回的那部分怎么办」—— 这正是 #122 的 pnl_source 列:
--   把它们标成 pnl_source='unknown',对外聚合一律排除或单列,**绝不当 0 计入分母**。
--   补不回来就写"补不回来",不要为了让报表好看去猜一个数。
--   ⚠ 就算将来 exchange_fills 拉起来了,这 1,404 行里也只有一部分能救:共享账户 +
--     单向持仓下,一个净仓在 DB 里对应 2-3 行(§4),交易所的 income 事件是
--     per-symbol per-account 的,拆不到 (owner, strategy) 这一层。596 那批就是
--     被实证出来的重复行,剩下 1,404 里还有多少是同类,库里分辨不出来。
-- ---------------------------------------------------------------------------

-- ###########################################################################
-- §3 那 6 行逐行摊开 + 真实开仓价的还原(只读)
-- ###########################################################################
--
-- 关键:4 行矛盾行的 avg_price 【本身已经被这个 bug 改坏了】,不能直接拿来算盈亏。
--
-- 坏法(在 manager.go applyOrderFillToPosition 里):行上 direction 写成 long,
-- 来了一笔 buy 平仓 → isIncrease = (long && buy) 判真 → 走加仓分支 → 量翻倍、
-- avg_price 被重算成 (真实开仓价 + 平仓价) / 2,closed_qty 与 realized_pn_l
-- 一个字不写。随后 stale-close 把行置成 closed,空壳成型。
--
-- 于是留下一个可验证的指纹:  真实开仓价 = 2 * 存量 avg_price - 平仓价
-- 实测四行全部对得上,而且每一行都能被同一个净仓的兄弟行独立印证:
--   id=1798 BLUAI  2*0.014871138912354805 - 0.0148693  = 0.0148729778
--                  = 兄弟行 1799 的 avg_price,也 = 开仓单 18092 的成交价 0.014873
--   id=1805 BLUAI  → 0.015323183009963291 = 兄弟行 1803/1804 的 avg_price
--   id=1897 STAR   → 0.08075              = 兄弟行 1895/1896 的 avg_price
--                                          (也 = 开仓单 18461 sell 676 @ 0.08075)
--   id=1966 AKE    → 0.010954799999999997 = 兄弟行 1964/1965 的 avg_price
-- id=60 / id=258 方向自洽、没走过加仓分支,它们的 avg_price 就是真实开仓价。

SELECT p.id, p.strategy_name, p.symbol,
       p.direction                                                   AS stored_dir,
       CASE WHEN o.side = 'buy' THEN 'short' ELSE 'long' END         AS true_dir,
       CASE WHEN p.direction = CASE WHEN o.side = 'buy' THEN 'short' ELSE 'long' END
            THEN p.avg_price ELSE 2 * p.avg_price - o.px END         AS true_entry,
       o.px AS close_px, o.q AS qty,
       ROUND(CASE WHEN o.side = 'buy' THEN o.q * ((2 * p.avg_price - o.px) - o.px)
                  ELSE o.q * (o.px - p.avg_price) END, 6)            AS pnl_gross
FROM strategy_positions p
JOIN (SELECT position_id, SUM(executed_qty) AS q,
             SUM(executed_qty * avg_price) / NULLIF(SUM(executed_qty), 0) AS px,
             MIN(side) AS side
      FROM strategy_orders
      WHERE purpose = 'close' AND status = 'filled' AND executed_qty > 0
      GROUP BY position_id) o ON o.position_id = p.id
WHERE p.status = 'closed' AND (p.closed_qty IS NULL OR p.closed_qty = 0)
ORDER BY p.id;
-- 实测 2026-09-09(pnl_gross 为毛盈亏,不含手续费 —— 库里没有手续费,见 §0(a)):
--   id=  60 Meme               ROBO   long  9763 @0.01227  → 0.01173   =  -2.636010
--   id= 258 Meme               TLM    long 18075 @0.001826 → 0.001911  =  +1.536375
--   id=1798 qt-fade-short      BLUAI  short 1894 @0.0148730→ 0.0148693 =  +0.006966
--   id=1805 qt-fade-short      BLUAI  short 1907 @0.0153232→ 0.015206  =  +0.223468
--   id=1897 qt-breakout-follow STAR   short  676 @0.08075  → 0.08014   =  +0.412360
--   id=1966 qt-breakout-follow AKE    short 2874 @0.0109548→ 0.0102008 =  +2.166996

-- ---------------------------------------------------------------------------
-- ⚠ 更正台账 #91 记的那个数
-- ---------------------------------------------------------------------------
-- 台账写「按 buy 平仓 ⇒ 实为空头补回 +1.2897,qt-breakout-follow 从 -1.2208
-- 翻成 +0.069」。**+1.2897 少算了一半**,因为它用的是【已经被加仓分支改坏的】
-- avg_price(0.080445 / 0.0105778),而不是真实开仓价。
-- 坏掉的 avg_price 恰好是 (真实开仓价 + 平仓价)/2,所以按它算出来的差价恰好是
-- 真实差价的一半 —— 0.20618 + 1.083498 = 1.289678,正是真值 2.579356 的 1/2。
--
-- 用真实开仓价重算(实测 sum_now 来自 SELECT SUM(realized_pn_l) GROUP BY 策略):
--   qt-breakout-follow   -1.220834 + 0.412360 + 2.166996 = +1.358522  (符号仍翻转)
--   qt-fade-short        +0.972108 + 0.006966 + 0.223468 = +1.202542
--   Meme_合约信号计算引擎_1 +42.940017 - 2.636010 + 1.536375 = +41.840382
--
-- ⚠ 归属是个取舍,不是事实:1897 的开仓腿是 owner1 / Meme 下的
--    (单 18461 sell 676 @0.08075),平仓腿才是 owner2 / qt-breakout-follow 下的
--    (单 18490 buy 676 @0.08014)。共享账户下这是【一个净仓】。
--    "+0.41236 算 qt-breakout-follow 的" 是一种记法,不是唯一正确答案。
--    账户级 +0.41236 是硬的;拆到策略头上要所有者定口径。1966/1798/1805 同理
--    (1798/1805 的开仓单 18092/18131 倒是 qt-fade-short 自己的,归属没有歧义)。

-- ###########################################################################
-- §4 为什么会有 2-3 行对一个净仓(给回填的人看,别把重复行当独立样本)
-- ###########################################################################
-- 实测 STAR 2026-08-14 04:32:34.300 这一个瞬间落了三行,open_time 精确到毫秒相同:
--   id=1895 owner=1 Meme               direction=short  avg=0.08075
--   id=1896 owner=1 Meme               direction=''     avg=0.08075
--   id=1897 owner=2 qt-breakout-follow direction=long   avg=0.080445(被改坏)
-- 三行对应交易所上的同一个净空仓。AKE 同日 16:44 是同样的五行结构(1964/1965/1966)。
-- 这三行里没有任何一行记了盈亏(全是 0),所以给 1897 补一次不会重复记账 ——
-- 但**前提是别再给 1895/1896 补第二次**。§6 的 UPDATE 只按 position_id 认平仓单,
-- 天然只会命中 1897,不会碰到兄弟行。

-- ###########################################################################
-- §5 「1,404 行永远补不回」的正面证据(只读)—— 别只当结论看,这段是可复核的
-- ###########################################################################
--
-- 先看个吓人的数,再看它其实救不了我们。
SELECT COUNT(*) AS close_orders,
       SUM(CASE WHEN position_id = 0 OR position_id IS NULL THEN 1 ELSE 0 END) AS orphan_close_orders,
       MIN(CASE WHEN position_id = 0 OR position_id IS NULL THEN requested_at END) AS orphan_first,
       MAX(CASE WHEN position_id = 0 OR position_id IS NULL THEN requested_at END) AS orphan_last
FROM strategy_orders
WHERE purpose = 'close' AND LOWER(status) = 'filled';
-- 实测:close_orders=3430  orphan_close_orders=3019 (88%)
--       orphan_first=2026-03-29 18:26  orphan_last=2026-07-19 17:36
--
-- position_id=0 的平仓单是账本缺口自己制造的孤儿:closeUSDMPosition 先
-- First(&pos) 查开仓行,查不到时 pos 是零值,紧接着写平仓单 PositionID 就落 0
-- (strategy_execution.go:424 的 `PositionID: pos.ID`)。88% 听起来像"平仓价其实
-- 都在库里,只是没挂上",于是很自然会想:能不能按 (owner, strategy, symbol,
-- 时间窗) 把它们认回去?

-- 试一次。对 §2 那 1,404 行 truly_unknown,数一数各有几张候选孤儿平仓单。
SELECT CASE WHEN c.n = 0 THEN 'no_candidate'
            WHEN c.n = 1 THEN 'unique_match' ELSE 'ambiguous' END AS bucket,
       COUNT(*) AS rows_
FROM strategy_positions p
LEFT JOIN (SELECT DISTINCT position_id FROM strategy_orders
           WHERE purpose = 'close' AND status = 'filled' AND executed_qty > 0
             AND position_id IS NOT NULL) o ON o.position_id = p.id
JOIN LATERAL (
  SELECT COUNT(*) AS n FROM strategy_orders x
  WHERE x.purpose = 'close' AND x.status = 'filled' AND x.executed_qty > 0
    AND (x.position_id = 0 OR x.position_id IS NULL)
    AND x.owner_id = p.owner_id AND x.strategy_id = p.strategy_id AND x.symbol = p.symbol
    AND x.requested_at >= p.open_time
    AND x.requested_at <= p.close_time + INTERVAL 5 SECOND
) c ON 1 = 1
WHERE p.status = 'closed' AND (p.closed_qty IS NULL OR p.closed_qty = 0)
  AND o.position_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM strategy_positions q
                  WHERE q.id <> p.id AND q.symbol = p.symbol AND q.closed_qty > 0
                    AND ABS(TIMESTAMPDIFF(SECOND, q.open_time, p.open_time)) <= 60)
GROUP BY bucket;
-- 实测:no_candidate = 1404,unique_match = 0,ambiguous = 0。
--
-- **全军覆没,而且干净利落**:不是"候选太多分不清",是【一张候选都没有】。
-- 原因在时间轴上一眼可见 —— 孤儿平仓单最后一张停在 2026-07-19 17:36,而 1,404 行
-- 的 open_time 从 2026-07-19 17:45 才开始,两段完全不重叠:
--   SELECT COUNT(*) FROM strategy_orders WHERE purpose='close' AND status='filled'
--     AND (position_id=0 OR position_id IS NULL) AND requested_at >= '2026-07-19 17:45:00';
--   实测 = 0。
-- 那 3,019 张孤儿是更早一代策略(V1/V5/V6/M1_new_2/V10 等,2026-03~05)和 Meme
-- 早期留下的,和这 1,404 行不是同一批交易。
--
-- 所以「1,404 行永远补不回」是**证出来的,不是估出来的**:平台从头到尾没有为
-- 它们记过任何平仓单,本地也没有 exchange_fills(§0a)。补不回来就是补不回来,
-- 别拿"按时间窗猜一个"去凑 —— 共享账户下同 symbol 并发交易必然串账,
-- manager.go fetchStaleRealizedPnL 的注释已经承认了这个取舍。

-- ###########################################################################
-- §6 回填 —— 默认注释掉。跑之前:§3 手工核对一致 + 已备份 + 所有者点头 + 写入侧已上线
-- ###########################################################################
--
-- 顺序必须是 6.1 → 6.2:6.2 依赖 6.1 纠正后的 direction。
--
-- 6.1 direction 按平仓单方向纠正(只改与平仓单矛盾的 4 行 + 补 1 行空值)
-- UPDATE strategy_positions p
-- JOIN strategy_orders o ON o.position_id = p.id
--                       AND o.purpose = 'close'
--                       AND LOWER(o.status) = 'filled'
--                       AND o.executed_qty > 0
-- SET p.direction  = CASE WHEN LOWER(o.side) = 'buy' THEN 'short' ELSE 'long' END,
--     p.updated_at = NOW()
-- WHERE p.status = 'closed'
--   AND LOWER(COALESCE(p.direction, '')) <> CASE WHEN LOWER(o.side) = 'buy' THEN 'short' ELSE 'long' END;
-- 预期影响 5 行:60, 1798, 1805, 1897, 1966。
--
-- 6.2 avg_price 还原 + closed_qty / avg_close_price / realized_pn_l / pnl_source 回填
--     只动 §3 列出的 6 行(status=closed 且 closed_qty=0 且有 filled 平仓单)。
--     avg_price 的还原只对被加仓分支改坏的行做 —— 判据是「存量 direction 与平仓单
--     矛盾」,所以【必须在 6.1 之前取快照,或者用下面这样先算好的临时表】。
--     稳妥做法:先把 §3 的 SELECT 结果落成一张临时表 t(id, true_dir, true_entry,
--     close_px, qty, pnl_gross),再:
-- UPDATE strategy_positions p JOIN t ON t.id = p.id
-- SET p.direction       = t.true_dir,
--     p.avg_price       = t.true_entry,
--     p.closed_qty      = t.qty,
--     p.avg_close_price = t.close_px,
--     p.realized_pn_l   = t.pnl_gross,
--     p.realized_notional = t.qty * t.true_entry,
--     p.pnl_source      = 'fill',
--     p.updated_at      = NOW();
--
-- 6.3 把补不回来的那 1,404 行显式标出来(**这一段比 6.1/6.2 重要**)
--     没有这一步,它们的 0 会继续被当成"打平"计入分母。
-- UPDATE strategy_positions p
-- LEFT JOIN (SELECT DISTINCT position_id FROM strategy_orders
--            WHERE purpose='close' AND status='filled' AND executed_qty>0
--              AND position_id IS NOT NULL) o ON o.position_id = p.id
-- SET p.pnl_source = 'unknown', p.updated_at = NOW()
-- WHERE p.status = 'closed' AND (p.closed_qty IS NULL OR p.closed_qty = 0)
--   AND o.position_id IS NULL;
--     (596 行重复行也会被标成 unknown —— 对聚合来说结果正确:两类都不该进分母。
--      要把重复行单独成桶,得先有一个显式的来源标记,见 strategy_roi_monitor.go
--      里关于 is_adopted / origin='reconcile' 的那段注释。)

-- ###########################################################################
-- §7 写入侧(先做,否则回填完还会被新的成交再写坏一次)
-- ###########################################################################
--   [已落地] manager.go applyOrderFillToPosition:purpose='close' 时以平仓单
--            side 为准【覆盖】矛盾的存量 direction,不再只是补空值。这是 1897/1966
--            真正走的那条路 —— 见 position_fill_direction_test.go 的
--            TestCloseFillOverridesContradictingStoredDirection。
--   [已落地] manager.go:成交腿写 pnl_source='fill';stale-close 拉到 income 写
--            'exchange_income',拉不到写 'unknown'(以前这一支一个字不写)。
--   [已落地] strategy_autotune.go:closed_qty=0 的空壳不再计入 closed_positions,
--            改计入 unaccounted_closed_positions,让排除动作在 payload 里看得见。
--   [待做]   查询侧五处对外聚合排除 unknown —— **必须等写入侧部署、且 6.3 跑过
--            之后**,否则历史行全是 NULL/空,对外数字会毫无预告跳一次。
