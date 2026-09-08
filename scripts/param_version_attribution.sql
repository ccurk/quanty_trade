-- 按策略/参数版本的收益归因 —— 建表 + 回滚脚本 (MySQL)
--
-- 平台正常启动时 GORM AutoMigrate 会自动建这张表和两个新列，本文件是给
-- "想先在预发库手工验证" 和 "出事要撤" 两个场景用的。回滚段在文件末尾。
--
-- 设计前提（这是它能安全上线的原因）：
--   * 只 ADD，不 MODIFY、不 DROP、不改任何既有列的语义；
--   * 两个新列都可空，NULL 是合法值。老数据一行不动，查询侧按
--     open_time 落到版本的 [effective_from, effective_to) 窗口里做退化归因；
--   * 没有外键约束（本项目 DisableForeignKeyConstraintWhenMigrating），
--     所以删表/删列不会被引用关系卡住。

-- ---------------------------------------------------------------------------
-- 1) 参数版本登记表（append-only）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS strategy_param_versions (
    id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    strategy_id     VARCHAR(64)     DEFAULT NULL,
    strategy_name   VARCHAR(128)    DEFAULT NULL,
    owner_id        BIGINT UNSIGNED DEFAULT NULL,
    seq             BIGINT          DEFAULT NULL,
    label           VARCHAR(64)     DEFAULT NULL,
    note            VARCHAR(512)    DEFAULT NULL,
    config_hash     VARCHAR(64)     DEFAULT NULL,
    config_json     TEXT,
    changed_json    TEXT,
    source          VARCHAR(32)     DEFAULT NULL,
    actor           VARCHAR(64)     DEFAULT NULL,
    effective_from  DATETIME(3)     DEFAULT NULL,
    -- NULL = 这一版还在生效中。禁止写 '0000-00-00'：严格模式 MySQL 会拒。
    effective_to    DATETIME(3)     DEFAULT NULL,
    is_current      BOOLEAN         DEFAULT NULL,
    created_at      DATETIME(3)     DEFAULT NULL,
    updated_at      DATETIME(3)     DEFAULT NULL,
    PRIMARY KEY (id),
    KEY idx_param_ver_sid_from (strategy_id, effective_from),
    KEY idx_strategy_param_versions_owner_id (owner_id),
    KEY idx_strategy_param_versions_config_hash (config_hash),
    KEY idx_strategy_param_versions_source (source),
    KEY idx_strategy_param_versions_is_current (is_current)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ---------------------------------------------------------------------------
-- 2) 成交/仓位表上的可空外键列（不加 FK 约束，只加索引）
--    MySQL 8.0.29+ 支持 ADD COLUMN IF NOT EXISTS；低版本请先跑一遍
--    information_schema 判存在，或直接依赖 AutoMigrate。
-- ---------------------------------------------------------------------------
ALTER TABLE strategy_positions ADD COLUMN param_version_id BIGINT UNSIGNED DEFAULT NULL;
ALTER TABLE strategy_positions ADD KEY idx_strategy_positions_param_version_id (param_version_id);

ALTER TABLE strategy_orders ADD COLUMN param_version_id BIGINT UNSIGNED DEFAULT NULL;
ALTER TABLE strategy_orders ADD KEY idx_strategy_orders_param_version_id (param_version_id);

-- ---------------------------------------------------------------------------
-- 2b) 交易所成交流水镜像表（手续费的唯一来源）
--
-- 为什么要新建一张表，而不是给 strategy_orders 加 fee_amount / fee_asset：
--   下单应答里没有手续费 —— exchange.Order 结构体只有 id/side/amount/price/status，
--   没有任何 fee 字段；而 USDM 期货**根本没有成交回报流**：
--   EnsureUserDataStream 在 market == "usdm" 时直接 return nil，
--   handleExecutionReport 对期货一次都不会触发。
--   所以给 strategy_orders 加两列，结果是两列永远为 NULL 的死列。
--   手续费实际进入本系统的唯一入口是 /fapi/v1/userTrades，
--   它返回的 commission / commissionAsset / maker 三个字段本来就已经解析出来了，
--   只是在日结 job 里被丢掉。本表就是把它们接住。
--
-- 去重键 (exchange, symbol, trade_id) + 写入侧 ON CONFLICT DO NOTHING：
--   同一笔成交被两个 owner 各拉一次时收敛成一行，而不是像 daily_pn_ls 那样
--   存成两份完整副本（那正是跨 owner 求和会翻倍的根因）。
--
-- ⚠️ 单账户前提：币安 trade_id 是「每账户每 symbol」唯一。当前全平台所有 owner
--   共用同一个交易所账户，所以这个键是安全的。**一旦接入第二个真实交易所账户，
--   必须先给本表加 account_id 并并入唯一键**，否则两个账户上同 symbol 的同号成交
--   会被误并成一行。加列前不要接第二个账户。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS exchange_fills (
    id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    exchange            VARCHAR(32)     DEFAULT NULL,
    symbol              VARCHAR(64)     DEFAULT NULL,
    trade_id            VARCHAR(64)     DEFAULT NULL,
    -- VARCHAR 是为了和 strategy_orders.exchange_order_id 同类型直接 JOIN；
    -- 任何一侧加 CAST 都会让索引失效。
    order_id            VARCHAR(64)     DEFAULT NULL,
    side                VARCHAR(8)      DEFAULT NULL,
    position_side       VARCHAR(16)     DEFAULT NULL,
    qty                 DOUBLE          DEFAULT NULL,
    price               DOUBLE          DEFAULT NULL,
    quote_qty           DOUBLE          DEFAULT NULL,
    realized_pn_l       DOUBLE          DEFAULT NULL,
    commission          DOUBLE          DEFAULT NULL,
    -- 原币种原数量，不折算。折算率是查询期的事，写死进来历史行就没法用新汇率重算。
    commission_asset    VARCHAR(16)     DEFAULT NULL,
    is_maker            BOOLEAN         DEFAULT NULL,
    trade_time          DATETIME(3)     DEFAULT NULL,
    -- 仅记录「谁的密钥拉到的」，是溯源信息。**禁止 GROUP BY / SUM 这一列**：
    -- 账户是共享的，把它当归属维度就会复现 daily_pn_ls 的翻倍。
    fetched_by_owner_id BIGINT UNSIGNED DEFAULT NULL,
    created_at          DATETIME(3)     DEFAULT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY idx_exchange_fill_uniq (exchange, symbol, trade_id),
    KEY idx_exchange_fills_order_id (order_id),
    KEY idx_exchange_fills_trade_time (trade_time),
    KEY idx_exchange_fills_commission_asset (commission_asset),
    KEY idx_exchange_fills_is_maker (is_maker),
    KEY idx_exchange_fills_fetched_by_owner_id (fetched_by_owner_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ---------------------------------------------------------------------------
-- 3) 归因查询（后端 GET /stats/strategy-attribution 用的就是这一条）
--    :owner 换成 user_id。两级归因：先认行上盖的章，没盖章的按 open_time
--    落窗口；两者都落空的归到 param_version_id = 0 的 "unversioned" 桶。
-- ---------------------------------------------------------------------------
-- SELECT
--     t.strategy_id,
--     MAX(t.strategy_name)                                  AS strategy_name,
--     COALESCE(t.version_id, 0)                             AS param_version_id,
--     COUNT(*)                                              AS trades,
--     SUM(CASE WHEN t.realized_pn_l > 0 THEN 1 ELSE 0 END)  AS wins,
--     COALESCE(SUM(t.realized_pn_l), 0)                     AS net_pn_l,
--     COALESCE(SUM(t.realized_pn_l), 0) / COUNT(*)          AS avg_pn_l
-- FROM (
--     SELECT p.strategy_id, p.strategy_name, p.realized_pn_l,
--         COALESCE(p.param_version_id, (
--             SELECT v.id FROM strategy_param_versions v
--             WHERE v.strategy_id = p.strategy_id
--               AND v.effective_from <= p.open_time
--               AND (v.effective_to IS NULL OR v.effective_to > p.open_time)
--             ORDER BY v.effective_from DESC LIMIT 1)) AS version_id
--     FROM strategy_positions p
--     WHERE p.owner_id = :owner AND p.status = 'closed'
-- ) t
-- GROUP BY t.strategy_id, COALESCE(t.version_id, 0);

-- ---------------------------------------------------------------------------
-- 3b) 验收查询：净期望值必须不为 NULL，且 fee 不全为 0
--
-- 跑之前先确认 exchange_fills 已经有数据。它由日结 job 的 userTrades 循环写入，
-- 所以 **新部署后要先触发一次回填** 才会有行：
--     POST /api/admin/daily-pnl/backfill?days=400   （BackfillDailyPnL，后台异步）
-- 表为空时下面的 fee 一列会全是 0 —— 那表示「还没采」，不表示「没交过手续费」。
-- ---------------------------------------------------------------------------
-- SELECT 'fill 采集情况' AS k, COUNT(*) fills,
--        MIN(trade_time) first_fill, MAX(trade_time) last_fill,
--        SUM(commission_asset='USDT') usdt_fills,
--        SUM(commission_asset<>'USDT') other_asset_fills,
--        SUM(is_maker) maker_fills,
--        ROUND(SUM(CASE WHEN commission_asset='USDT' THEN commission ELSE 0 END),6) fee_usdt
-- FROM exchange_fills;
--
-- 实测真实费率（不再靠猜 VIP 档位）：
-- SELECT ROUND(SUM(CASE WHEN commission_asset='USDT' THEN commission ELSE 0 END)
--              / NULLIF(SUM(quote_qty),0) * 10000, 4) AS real_fee_bps,
--        SUM(is_maker)/COUNT(*) AS maker_ratio
-- FROM exchange_fills;
--
-- 归因表的验收（对应后端 /stats/strategy-attribution 的 net_avg_pnl）：
-- SELECT o.strategy_id,
--        COUNT(DISTINCT o.exchange_order_id)                                   AS orders,
--        ROUND(SUM(CASE WHEN x.commission_asset='USDT' THEN x.commission ELSE 0 END),6) AS fee_usdt,
--        SUM(x.is_maker)                                                       AS maker_fills
-- FROM exchange_fills x
-- JOIN strategy_orders o ON o.exchange_order_id = x.order_id
-- GROUP BY o.strategy_id;
--
-- §7.8 的口子（交易所成交了、本平台没下过的单）现在也能直接列出来了：
-- SELECT COUNT(*) foreign_fills, ROUND(SUM(x.realized_pn_l),4) foreign_pnl
-- FROM exchange_fills x
-- LEFT JOIN strategy_orders o ON o.exchange_order_id = x.order_id
-- WHERE o.id IS NULL;

-- ===========================================================================
-- 回滚
--
-- 影响面：只丢归因/费用数据，不碰任何成交/仓位/PnL 既有字段。撤掉后
--   GET /stats/strategy-attribution 与 /strategies/:id/param-versions 会报错，
--   其余接口不受影响 —— 所以代码回滚（git revert 本次改动）和 DDL 回滚
--   要一起做，顺序是先回代码、再跑下面的 DDL，否则 AutoMigrate 会把表建回来。
--
-- 只想停掉手续费采集、不想回滚任何代码或表：
--   设环境变量 EXCHANGE_FILLS_PERSIST=0 然后重启后端。
--   日结 job 其余行为完全不变，已采集的行原样保留。
--   这是首选的止血手段 —— 比 DROP 表和 revert 代码都轻。
--
-- 注意：strategy_param_versions 是唯一存有 "哪一版参数在什么时间生效" 的地方，
--   DROP 之后无法从别处重建（strategy_instances.config 是原地覆盖的）。
--   exchange_fills 同理：它是交易所成交流水的唯一本地副本，DROP 之后只能靠
--   重新调 /fapi/v1/userTrades 重拉，而币安 userTrades 有时间窗和权重限制，
--   越久远的历史越难补。**两张表都先 RENAME 备份，别直接 DROP。**
-- ===========================================================================
-- RENAME TABLE strategy_param_versions TO strategy_param_versions_bak_20260908;
-- RENAME TABLE exchange_fills          TO exchange_fills_bak_20260908;
-- ALTER TABLE strategy_positions DROP COLUMN param_version_id;
-- ALTER TABLE strategy_orders    DROP COLUMN param_version_id;
-- 确认没问题之后再: DROP TABLE strategy_param_versions_bak_20260908;
--                   DROP TABLE exchange_fills_bak_20260908;

-- ===========================================================================
-- 给下一个人的两条纪律（别顺手改掉）
--
-- 1) 不要新增 net_pnl 列。净利 = gross - SUM(fee)，是查询期派生量。
--    存成列，费率一旦追溯调整（或非 USDT 手续费变得可折算），这一列就会变成
--    第二个真相源，而且是不会报错、只会悄悄对不上的那一种。
--    后端 AttributionRow.NetPnL 是在 Go 里现算的，没有对应的数据库列，这是刻意的。
--
-- 2) 收养/对账路径的 param_version_id 保持 NULL，不要盖章。
--    理由写在 backend/internal/strategy/strategy_roi_monitor.go 的 synthetic
--    仓位构造处：收养行不知道自己是在哪一版参数下开的，盖章只是把「扫到它的
--    那一刻」写进去。留 NULL 会退化成按 open_time 落窗口、落不进就进
--    unversioned 桶，两种结果在报表上都看得见、可被质疑；盖了错章之后它和
--    真货长得一模一样，没有字段能分辨，也就没人会去查。
--    盖错章比不盖章更难查。要给收养行归因，先加 is_adopted / origin 显式标记。
