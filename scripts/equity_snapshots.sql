-- 权益快照落库 —— 建表 + 复核查询 + 回滚 (MySQL)   台账 #42
--
-- ⚠️ 本文件【不会】被程序自动执行。equity_snapshots 故意【没有】进
--    internal/database/database.go 的 AutoMigrate 名单，所以：
--
--      * 只部署代码 = 什么都不发生（不写库、不报错、不刷日志，余额照读照用）；
--      * 【所有者手工跑这个脚本】= 开关打开，下次重启后自动开始落库。
--
--    生产迁移是所有者拍板的事（台账 #8），一个会在部署时自己建表的功能
--    等于把这个决定拿走了。这条约定与 scripts/markout_persistence.sql 一致，
--    两个功能共用同一套形状，不各搞一套。
--
-- 它解决什么：系统一直在读自己的余额，只是从来不写下来。
--   internal/marketmaker/engine.go  ex.Balances()       每秒约 4 次（每 pair 每次报价刷新）
--   internal/exchange/binance.go    USDMAvailableUSDT() 每次开仓定量、每次 /optimize/context
-- 读了几万次，写进 DB 的次数是 0。缺的从来不是"读余额的能力"，是一条 INSERT。
-- 于是每个资金问题都只能靠成交名义额 ÷ 配置百分比【反解】
-- （state/strategy/equity-blindspot-2026-09-09.md §3）——那推出来的是【下界】，
-- 而且压在三个没验证过的假设上，不是测量值。
--
-- 设计前提（这是它能安全上线的原因）：
--   * 纯新增一张表。不 MODIFY、不 DROP、不碰任何既有表的任何列；
--   * append-only：一次观测一行，写完不再改。【没有"总权益"这种列】——
--     总权益是查询期的 SUM，落了库就会在汇率/折价口径一改时让历史行全废，
--     并造出第二个真相源。这正是 #42 诊断出的病根；
--   * venue 是硬要求。#18/#22 撞车的真因就是 $57.60(Polymarket) 和 $236(Binance)
--     被当成同一个量的两个估计去【三角定位】，而它们是两个场子的钱、该【相加】
--     （同上 §4）。没标场子的余额不是弱数据，是陷阱 —— equity.Emit 直接拒收；
--   * 不加任何接口、不加任何权限、不多打一次 API：balance / crossUnPnl / locked
--     三个字段本来就在【已经在收的同一份响应】里，以前解出来就丢了，现在接住；
--   * 写入完全在旁路：失败/变慢只丢快照，碰不到报价与下单
--     （internal/equity/equity.go，测试 TestSinkFailureDoesNotBlockTradingPath）。

-- ---------------------------------------------------------------------------
-- 1) 建表
--    列名与 models.EquitySnapshot 的 gorm tag 逐一对应，
--    由 equitydb 的 TestDDLMatchesModel 钉死（表不在 AutoMigrate 里 =
--    没有任何运行时机制会发现两边对不上，只会在生产上 INSERT 报 unknown column）。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS equity_snapshots (
    id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,

    -- 场子。binance_usdm | binance_spot | gate_spot | polymarket
    -- （常量在 internal/equity/equity.go）。这一列没有默认值也不该有：
    -- 一行不知道自己是哪个场子的钱，就不该存在。
    venue       VARCHAR(32)     DEFAULT NULL,
    asset       VARCHAR(32)     DEFAULT NULL,
    -- 观测时刻（交易所报出这个数的时候），不是插入时刻。两者之差 = sink 队列延迟，
    -- 见 created_at。
    taken_at    DATETIME(3)     DEFAULT NULL,

    -- free  = 现在就能动的（Binance availableBalance / Gate available）
    -- total = free + 已占用但仍是我们的（保证金、挂单锁仓；
    --         Binance balance 钱包 / Gate available+locked）
    -- 两个都存而不是只存一个：(total − free) 就是"账上有钱为什么开不了仓"的答案。
    free        DOUBLE          DEFAULT NULL,
    total       DOUBLE          DEFAULT NULL,
    -- 持仓浮盈浮亏，只有衍生品场子有意义（Binance crossUnPnl）。
    -- 现货场子结构性为 0 —— 是 venue 这一列在告诉读者这个 0 是"不适用"
    -- 而不是"没读到"。这里没有任何 null-as-zero。
    unrealized  DOUBLE          DEFAULT NULL,

    -- 这一行是从哪个端点解出来的，如 "GET /fapi/v2/balance"。
    -- 存端点而不是含糊的 "api"：第二个人几个月后能照着它重发一次请求做对照，
    -- 不需要读这个程序。
    source      VARCHAR(64)     DEFAULT NULL,

    -- 插入时刻。与 taken_at 的差 = 落库延迟，行少了/来晚了先看这个。
    created_at  DATETIME(3)     DEFAULT NULL,

    PRIMARY KEY (id),
    -- 同一场子同一币种同一时刻只可能有一个真值。唯一键 + INSERT IGNORE
    -- （GORM 侧 ON CONFLICT DO NOTHING）= 重放/重复投递是 no-op，
    -- 而不是把那一瞬间在任何均值里重复加权。
    UNIQUE KEY idx_equity_snap_uniq (venue, asset, taken_at),
    KEY idx_equity_snapshots_taken_at (taken_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ---------------------------------------------------------------------------
-- 2) 容量估算（算式在此，别只记结论）
-- ---------------------------------------------------------------------------
-- ❶ 不降频会是多少行 —— 这是必须先算的那个数：
--    gate：engine.go 每个 pair 每次报价刷新读一次全账户余额，
--          生产配置 4 个 pair × refresh_ms=1000 → 【4 次/秒】
--          （台账 #42 的口径；仓库里的 marketmaker.example.json 只有 3 个 pair，
--            真实配置在服务器 $MARKETMAKER_CONFIG 上，不在库里）。
--          每次返回【整个账户】的资产，设有 A 个非零资产：
--             4 × 86,400 × A = 345,600 × A 行/天
--          A ≈ 5（SOL/ONG/MOVE/USDT + 零头，⚠️【未实测】，量级估计）
--             → 约 1,728,000 行/天
--    binance 永续：USDMAvailableUSDT 有 5 秒缓存，即使被连续调用也 ≤ 17,280 次/天，
--          单一资产 USDT → ≤ 17,280 行/天
--    合计不降频 ≈ 【175 万行/天】。绝大多数是逐字相同的重复行 —— 不可接受。
--
-- ❷ 降频规则（internal/equity/equity.go，三个都是必需的）：
--    a) 速率上限 minWriteInterval = 60s：每个 (venue, asset) 每分钟最多一行。
--       没有它，报价循环会直接把 DB 冲垮。
--    b) 变化才写：过了 60s 但值没动（相对 1e-9 以内）就不写。
--       账户安静时几乎不产生行。
--    c) 心跳 heartbeatInterval = 15min：即使没动，也强制写一行。
--       这条最容易被省掉，但【不能省】—— 没有它，"余额一直没变"和
--       "我们不再观测了"会产生一模一样的空白区间，而审计表里的空洞
--       必须和平线分得开。
--
-- ❸ 降频后：
--       最忙（每个资产每分钟都在动）：(A + 1) × 1440 = 6 × 1440 ≈ 8,640 行/天
--       最闲（全靠心跳）：            (A + 1) ×   96 = 6 ×   96 ≈   576 行/天
--    → 相对不降频约 【200 倍】的削减。
--
-- ❹ 单行字节 ≈
--       定长：id 8 + free/total/unrealized 3×8 + taken_at/created_at 2×7 ≈ 46 B
--       变长（utf8mb4，按实际内容）：venue 13 + asset 5 + source 22    ≈ 40 B
--       数据行（含 InnoDB 行头）                                       ≈ 106 B
--       二级索引 2 个：唯一键 (venue,asset,taken_at)+主键 ≈ 48、taken_at ≈ 30 ≈ 78 B
--       合计 ≈ 184 B；按页填充率与 B 树分裂留白 1.4× → 规划值 ≈ 【260 B/行】
--
--    一年：最忙 8,640 × 365 ≈ 3.15M 行 ≈ 820 MB
--          最闲   576 × 365 ≈ 210k 行 ≈  55 MB
--    结论：不需要分区、不需要 TTL。落库一两周后拿 §4.1 的自检把"行/天"
--          换成实测值再复算一次；真到了 GB 量级，按 taken_at 归档即可。
--    参照：strategy_logs.ibd 现在是 2.9 GB，这张表最忙的一年也只有它的 1/4。

-- ---------------------------------------------------------------------------
-- 3) 主查询：任何资金结论都从这里出，别再各用各的数
-- ---------------------------------------------------------------------------
-- 3.1 每个场子每种资产的最新一行（这才是"我们现在有多少钱"的正确形状：
--     一张【按场子分行】的表，而不是一个数）。
SELECT s.venue, s.asset, s.taken_at, s.free, s.total, s.unrealized, s.source
FROM equity_snapshots s
JOIN (
    SELECT venue, asset, MAX(taken_at) AS mx
    FROM equity_snapshots
    GROUP BY venue, asset
) t ON t.venue = s.venue AND t.asset = s.asset AND t.mx = s.taken_at
ORDER BY s.venue, s.asset;

-- 3.2 总权益 —— 【故意写成查询，不落库】。
--     一旦某天要改折价/汇率口径，改这一句就行，不会出现第二个真相来源。
--     ⚠️ 这里直接 SUM 是把每种资产按 1 USD 记；只有 USDT/USDC 这类稳定币成立。
--        有 SOL/ONG 这种非稳定资产时必须先乘价格 —— 价格【不在这张表里】
--        （表里只有数量，这是刻意的：价格是另一个来源，混进来就没法复核了）。
--     跨场子是【相加】不是三角定位，这正是 #18/#22 撞车的真因（§4）。
SELECT venue, SUM(total + unrealized) AS venue_equity_usdt
FROM equity_snapshots s
JOIN (
    SELECT venue AS v, asset AS a, MAX(taken_at) AS mx
    FROM equity_snapshots GROUP BY venue, asset
) t ON t.v = s.venue AND t.a = s.asset AND t.mx = s.taken_at
WHERE s.asset IN ('USDT', 'USDC')
GROUP BY venue WITH ROLLUP;

-- 3.3 权益曲线（每场子每小时收盘值）。这是这张表真正的价值：
--     #42 之前【没有任何历史】可查，只有一个反解出来的当前下界。
SELECT DATE_FORMAT(taken_at, '%Y-%m-%d %H:00') AS hr,
       venue, asset,
       SUBSTRING_INDEX(GROUP_CONCAT(total + unrealized ORDER BY taken_at DESC), ',', 1) AS equity_end
FROM equity_snapshots
WHERE taken_at >= NOW() - INTERVAL 7 DAY
GROUP BY hr, venue, asset
ORDER BY hr DESC, venue, asset;

-- ---------------------------------------------------------------------------
-- 4) 落库自检（建完表、重启之后跑这几条确认链路真的通了）
-- ---------------------------------------------------------------------------
-- 4.1 在不在写、每天多少行、降频有没有生效：
SELECT DATE(taken_at)                        AS day,
       venue,
       COUNT(*)                              AS rows_written,
       COUNT(DISTINCT asset)                 AS assets,
       ROUND(COUNT(*) / NULLIF(COUNT(DISTINCT asset), 0), 1) AS rows_per_asset,
       MAX(TIMESTAMPDIFF(SECOND, taken_at, created_at))      AS max_sink_lag_s
FROM equity_snapshots
GROUP BY DATE(taken_at), venue
ORDER BY day DESC, venue;
-- rows_written 恒为 0 → 没在落（表名/重启/余额是否真的在读，三选一没到位）。
-- rows_per_asset 远超 1440 → 降频失效（不该发生，规则在进程内）。
-- rows_per_asset ≈ 96 → 一直在走心跳，即余额确实没动过。
-- max_sink_lag_s 持续变大 → 写入端跟不上，看 §4.3 的 dropped。
--
-- 4.2 有没有没标场子的行（应当恒为 0；Emit 和 WriteEquitySnapshot 双层拒收）：
SELECT COUNT(*) AS unlabelled_rows
FROM equity_snapshots
WHERE venue IS NULL OR venue = '' OR asset IS NULL OR asset = '';
--
-- 4.3 进程内计数：internal/equity.Counters() ——
--     observed / suppressed / enqueued / dropped / written / failed。
--     suppressed 占绝大多数是【正常的】，那正是降频在起作用；
--     dropped 持续增长 = 缓冲一直满，写入端跟不上；
--     failed 增长 = 表不存在或 DB 出错（这两种情况下交易路径都不受影响）。

-- ---------------------------------------------------------------------------
-- 5) 回滚
-- ---------------------------------------------------------------------------
-- 关掉落库有两条路，都不需要改代码、都不影响交易：
--   a) 只想停写、保留已有数据 → 改名，进程下次重启时 HasTable 判 false 即停：
--        RENAME TABLE equity_snapshots TO equity_snapshots_disabled;
--   b) 彻底撤掉 → 直接删（没有外键指向它，删了不影响任何既有功能）：
--        DROP TABLE IF EXISTS equity_snapshots;
--
-- 表建起来并且有了几天数据之后：internal/equity/baseline.go 里那套
-- "$355 / 区间 $300~420 / 硬地板 $75" 的临时口径就该【整体删除】，
-- 不是更新 —— 答案从此是上面 §3.1 的一条 SELECT。
