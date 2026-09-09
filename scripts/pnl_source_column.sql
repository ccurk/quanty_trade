-- strategy_positions.pnl_source —— 把「不知道」和「真的是 0」分开 (MySQL) —— 台账 #122
--
-- ⚠️ 这个文件**没有在任何库上执行过**。写它的会话只有只读权限,而线上库此刻
--    还开在公网 3306 上(台账 #114),要不要执行、什么时候执行由所有者决定。
--
-- ---------------------------------------------------------------------------
-- 它解决什么
-- ---------------------------------------------------------------------------
-- fetchStaleRealizedPnL(strategy/manager.go)对任何失败一律返回 (0, false):
-- 非 Binance、openTime 为零、income 接口报错、窗口里查不到该 symbol 的
-- REALIZED_PNL 事件 —— 全都静默走这条路;行照样被置 status='closed',
-- realized_pn_l 停在 0。交易所侧 TP/SL 触发、强平、用户在币安手工平仓,走的都是
-- 这条路。
--
-- 于是库里出现两种长得完全一样的 0:
--   (a) 这笔真的打平    → 应该进胜率分母,是一条有效样本;
--   (b) 我们没查到      → 不该进任何统计,它是缺失值不是观测值。
-- 现在两者都是 `realized_pn_l = 0`,SQL 分不开。后果是「不知道」被当成「持平」
-- 计入分母,胜率被系统性稀释 —— 而这些数正是对外口径(#32)和 AI 调参器
-- (strategy_autotune.go)的输入。
--
-- 只加一列、可空、不改任何既有列的语义。老数据一行不动(全部落在 NULL =
-- 「这行写入时还没有来源标记」),所以这个 ALTER 本身不会改变任何现有查询的结果。
--
-- ---------------------------------------------------------------------------
-- 取值约定(写入侧要照这个来)
-- ---------------------------------------------------------------------------
--   'fill'             realized_pn_l 由本系统自己的成交腿算出(开仓均价 vs 平仓
--                      均价 × closed_qty)。可信。
--   'exchange_income'  取自交易所 income / userTrades 的 REALIZED_PNL。最可信,
--                      因为是交易所自己结的数。
--   'unknown'          查失败或压根没查 —— 就是上面的 (b)。**汇总时必须排除或
--                      单列**,绝不能当成 0 计入分母。
--   NULL               这一列上线前写入的历史行,来源不可考。等同 'unknown' 处理,
--                      除非另有回填(见台账 #91 的 scripts/realized_pnl_backfill_audit.sql)。
--
-- ---------------------------------------------------------------------------
-- 这个 ALTER 本身不够 —— 还差两步,都是独立改动
-- ---------------------------------------------------------------------------
--   1) 写入侧:manager.go 三处 Updates(...) 和 fetchStaleRealizedPnL 的失败分支
--      要开始写这一列。列建好但没人写 = 一列永远 NULL 的死列。
--   2) 查询侧:五处对外聚合(api/handlers.go、api/dashboard_builder.go、
--      api/modules_pnl.go、api/strategy_attribution.go、strategy/strategy_autotune.go)
--      要把 unknown 排除或单列展示。
--   在 (1) 落地前不要改 (2) —— 那样会把全部历史行(NULL)一次性排除掉,对外数字
--   会毫无预告地跳一次。

-- ---------------------------------------------------------------------------
-- 1) 加列
-- ---------------------------------------------------------------------------
ALTER TABLE strategy_positions
    ADD COLUMN pnl_source VARCHAR(16) DEFAULT NULL COMMENT 'fill|exchange_income|unknown; NULL=历史行,来源不可考';

-- 2) 索引:对外聚合会长期带 `pnl_source <> 'unknown'`(或 IS NULL 的变体),
--    和现有的 status/close_time 过滤一起走。
ALTER TABLE strategy_positions
    ADD KEY idx_strategy_positions_pnl_source (pnl_source);

-- ---------------------------------------------------------------------------
-- 执行后的自检(只读,先跑这个再改任何查询)
-- ---------------------------------------------------------------------------
-- SELECT COALESCE(pnl_source,'(NULL)') src, COUNT(*) rows_,
--        SUM(realized_pn_l = 0) zero_pnl,
--        ROUND(SUM(realized_pn_l), 4) sum_pnl
-- FROM strategy_positions
-- WHERE status = 'closed' AND closed_qty > 0
-- GROUP BY src;
--   刚 ALTER 完的预期:只有一行 '(NULL)',行数等于当前已平仓样本数。
--   写入侧上线后,'unknown' 那行的 zero_pnl 应该等于它的 rows_ —— 如果不等,
--   说明有 realized_pn_l 非 0 的行被标成了 unknown,那是写入侧的 bug。

-- ---------------------------------------------------------------------------
-- 回滚
-- ---------------------------------------------------------------------------
-- 只在写入侧和查询侧都还没引用这一列时才是无损的。
-- ALTER TABLE strategy_positions DROP KEY idx_strategy_positions_pnl_source;
-- ALTER TABLE strategy_positions DROP COLUMN pnl_source;
