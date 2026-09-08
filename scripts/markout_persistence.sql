-- markout 逐笔落库 —— 建表 + 复核查询 + 回滚 (MySQL)
--
-- ⚠️ 本文件【不会】被程序自动执行。markout_fills 故意【没有】进
--    internal/database/database.go 的 AutoMigrate 名单,所以：
--
--      * 只部署代码 = 什么都不发生（不写库、不报错、不刷日志）；
--      * 【所有者手工跑这个脚本】= 开关打开，下次重启后引擎自动开始落库。
--
--    这样做是因为生产迁移是所有者拍板的事（台账 #8），
--    一个会在部署时自己建表的功能等于把这个决定拿走了。
--    落库开启后的自检见文件末尾 §5。
--
-- 它解决什么：markout 一直只活在进程内存里（MarkoutTracker.done，5000 条滚动窗口，
-- 重启即失）。于是每次要用 markout 下判断，都得拿 server.log 离线按公式重算一遍
-- ——见 state/strategy/quote-anchor-decision-2026-09-09.md §6：
-- "本报告里所有 markout 都是我用 markout.go 的公式在离线数据上重算的，不是它跑出来的"。
-- 两份公式一旦分叉没人会发现。这张表存的是【线上那份代码真的算出来的数】。
--
-- 设计前提（这是它能安全上线的原因）：
--   * 纯新增一张表。不 MODIFY、不 DROP、不碰任何既有表的任何列；
--   * append-only：一笔成交一行，写完不再改。写入用 INSERT IGNORE 语义
--     （GORM 侧 ON CONFLICT DO NOTHING），引擎重拉成交时重复写是 no-op，
--     不会覆盖已有行 —— 没有"当前值"这种会被后写抹掉的列（台账 #42 的病根）；
--   * 没有外键约束（本项目 DisableForeignKeyConstraintWhenMigrating），
--     param_version_id 只是可空的软引用，删版本行不会卡住这张表；
--   * 写入完全在旁路：失败/变慢只丢度量，碰不到报价与下单
--     （marketmaker/markout_sink.go，测试 TestMarkoutSinkFailureDoesNotBlockTradingPath）。

-- ---------------------------------------------------------------------------
-- 1) 建表
--    列名与 models.MarkoutFill 的 gorm tag 逐一对应，已实测比对过：
--    GORM 默认命名会把 Mid1s 写成 "mid1s"、Lag1sMs 写成 "lag1s_ms"（数字前不加下划线），
--    所以模型里每个 horizon 列都显式 column: 钉死了，别改任何一边的名字。
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS markout_fills (
    id                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,

    -- 成交标识（去重键）。fill_id 存成字符串：交易所 id 的整数格式歧义不该有机会毁掉唯一键。
    exchange            VARCHAR(32)     DEFAULT NULL,
    symbol              VARCHAR(64)     DEFAULT NULL,
    fill_id             VARCHAR(64)     DEFAULT NULL,

    side                VARCHAR(8)      DEFAULT NULL,
    fill_px             DOUBLE          DEFAULT NULL,
    amount              DOUBLE          DEFAULT NULL,
    -- 测这笔时【假设】的单腿 maker 费。逐行存是因为它是假设不是事实
    -- （MakerFeeBps 取不到实时费率时会回落到默认值）：以后查到账户真实档位，
    -- 不能反过来改写历史行当时是按什么判的。
    fee_bps             DOUBLE          DEFAULT NULL,
    -- 交易所给的成交时刻（create_time），不是我们轮询到它的时刻。
    -- 引擎每 ~10s 才拉一次成交，用轮询时刻会把 1s horizon 直接测成噪声。
    fill_ts             DATETIME(3)     DEFAULT NULL,

    -- 成交【当时】的执行所中价（取 fill_ts 之前最后一个样本；0 = 无样本覆盖）。
    -- (mid_at_fill − fill_px) 是这笔【拿到的边】，下面的 markout 是【成交之后的漂移】。
    -- 少了它，"报价挂得好但市场随后跑赢"和"报价本身就吃亏"是同一个数。
    mid_at_fill         DOUBLE          DEFAULT NULL,
    mid_at_fill_ts      DATETIME(3)     DEFAULT NULL,
    mid_at_fill_lag_ms  BIGINT          DEFAULT NULL,

    -- 每个 horizon 的原始证据：用到的中价 + 由它算出的 markout + 采样滞后 + 陈旧标记。
    -- mid_* 必须来自【成交发生的那个所】；跨所中价会把基差整段折进 markout
    -- （台账 #7 的头号陷阱：ONG 实测中位基差 +37.9bps，足以淹掉整个信号）。
    -- lag_*_ms = 样本时刻 −（成交时刻 + horizon），恒 ≥ 0。
    --
    -- 【样本陈旧度上限】marketmaker.maxSampleLag = max(2s, horizon/4)
    --   → 1s 和 5s 卡 2000ms，30s 卡 7500ms。
    -- 为什么要有：resolveLocked 取"target 之后的第一个样本"，原本对它有多晚不设上限。
    -- 行情断流 5 分钟后恢复的第一个中价同时 ≥ 三个 target，会把 1s/5s/30s 用【同一个
    -- 5 分钟后的价】一次性结算 —— 三个数完全相同、却分别叫 1s/5s/30s markout，
    -- 和真样本躺在同一列里，事后分不出来。数字没算错，错在没标它是什么条件下产生的。
    -- 下限 2s = 两个 refresh_ms(默认 1000)，即容忍恰好一次漏采；
    -- 上限 h/4 = 长 horizon 最多被拉长 25%（30s 实测窗口 ≤ 37.5s）。
    --
    -- 超上限的那一档：证据照落（mid_*、lag_*_ms 有值，stale_* = 1），
    -- 但 markout_bps_* 写 NULL。三个后果都是有意的：
    --   * AVG(markout_bps_5s) —— 所有人打的第一条查询 —— 默认就是对的（AVG 跳过 NULL）；
    --   * SUM(stale_5s) 数得出【扔掉了多少】。静默丢弃就数不出来了，而
    --     "这个品种 5s 样本少"和"5s 大半被断流吃掉"是相反的结论；
    --   * lag_*_ms 仍在行上，想更严就在查询期再切一刀（WHERE lag_1s_ms < 500）。
    --     代码里那条线是【可信底线】，不是最终口径。
    mid_1s              DOUBLE          DEFAULT NULL,
    markout_bps_1s      DOUBLE          DEFAULT NULL,
    lag_1s_ms           BIGINT          DEFAULT NULL,
    stale_1s            TINYINT(1)      DEFAULT NULL,
    mid_5s              DOUBLE          DEFAULT NULL,
    markout_bps_5s      DOUBLE          DEFAULT NULL,
    lag_5s_ms           BIGINT          DEFAULT NULL,
    stale_5s            TINYINT(1)      DEFAULT NULL,
    mid_30s             DOUBLE          DEFAULT NULL,
    markout_bps_30s     DOUBLE          DEFAULT NULL,
    lag_30s_ms          BIGINT          DEFAULT NULL,
    stale_30s           TINYINT(1)      DEFAULT NULL,

    -- complete=1：三个 horizon 【全都产出了可用的 markout】。所以干净样本 = complete=1，
    -- 一句话就够。complete=0 有两种成因，落库后分得开、且结论相反：
    --   * 那个 horizon 从头到尾没样本（行情断了不回来）→ mid_* = 0；
    --   * 有样本但太晚（超过上限）                     → mid_* > 0 且 stale_* = 1。
    -- horizons_done = 【可用】的 horizon 数（0..3），不是采到样本的数量。
    -- 这类行【保留】而不是丢弃 ——
    -- "样本少"和"我们没记上"会导出相反的结论，只有落了行才分得开。
    complete            TINYINT(1)      DEFAULT NULL,
    horizons_done       BIGINT          DEFAULT NULL,

    -- 算这行时用的做市参数。param_hash 恒有值（sha256，本地算的，不含任何凭据）；
    -- param_version_id 指向 strategy_param_versions 里那一版的完整配置，
    -- 登记失败时留 NULL —— 少一个戳可以退化成按 param_hash 归组，
    -- 填一个错的戳则无法补救，所以绝不猜。
    param_hash          VARCHAR(64)     DEFAULT NULL,
    param_version_id    BIGINT UNSIGNED DEFAULT NULL,

    resolved_at         DATETIME(3)     DEFAULT NULL,
    created_at          DATETIME(3)     DEFAULT NULL,

    PRIMARY KEY (id),
    -- 引擎每 ~10s 重拉最近 100 笔成交，同一笔会被反复看到。
    -- 唯一键 + INSERT IGNORE = 重复写是 no-op，而不是把这笔在每个均值里重复加权。
    UNIQUE KEY idx_markout_fill_uniq (exchange, symbol, fill_id),
    KEY idx_markout_fills_side (side),
    KEY idx_markout_fills_fill_ts (fill_ts),
    KEY idx_markout_fills_complete (complete),
    KEY idx_markout_fills_param_hash (param_hash),
    KEY idx_markout_fills_param_version_id (param_version_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ---------------------------------------------------------------------------
-- 2) 容量估算（算式在此，别只记结论）
-- ---------------------------------------------------------------------------
-- 单行字节 ≈ 定长部分 + 变长部分 + 二级索引：
--   定长：id 8 + (fill_px,amount,fee_bps) 3×8 + (mid/markout ×3 horizon) 6×8
--         + (lag ×3) 3×8 + mid_at_fill 8 + mid_at_fill_lag_ms 8
--         + DATETIME(3) ×5 (fill_ts, mid_at_fill_ts, resolved_at, created_at) 4×7
--         + complete 1 + stale_* 3×1 + horizons_done 8 + param_version_id 8 ≈ 168 B
--   变长（utf8mb4，按实际内容）：exchange 5 + symbol 9 + fill_id 11
--         + side 5 + param_hash 65                                  ≈  95 B
--   数据行 ≈ 265 B（含 InnoDB 行头；三个 stale_* 只加 3 B，不建索引）
--   二级索引 6 个，每条 = 索引列 + 主键 8 + 开销：唯一键 ~48、param_hash ~88、
--   其余 4 个各 ~25~30                                              ≈ 250 B
--   合计 ≈ 515 B/行；InnoDB 页填充率与 B 树分裂留白按 1.4× 计
--   → 规划值 **≈ 700 B/行**
--
-- 一天多少行 = 一天多少笔成交（一笔成交一行，买卖两条腿都算）。
-- ⚠️ 真实成交笔数【从来没有被记录过】—— 那正是这张表要解决的问题。
--    目前唯一的依据是台账 #7 用 top-of-book 推断出的上下界
--    （state/strategy/quote-anchor-decision-2026-09-09.md §2.2，
--     4 个 symbol × 2 条腿，窗口 09-05 17:56 → 09-08 17:55 UTC ≈ 72h）：
--      悲观（through，市场穿过我们才算成交）：8 条腿合计   701 笔 / 72h ≈   234 笔/天
--      乐观（attouch，成为盘口最优档就算成交）：8 条腿合计 12,703 笔 / 72h ≈ 4,234 笔/天
--
--   90 天 = 笔/天 × 90 × 700 B：
--      悲观    234 × 90 =  21,060 行 ≈  14 MB
--      乐观  4,234 × 90 = 381,060 行 ≈ 254 MB
--
--   结论：两端都不构成容量问题，不需要分区、不需要 TTL。
--   落库一两周后拿 §5 的自检查询把"笔/天"换成实测值，再复算一次即可。

-- ---------------------------------------------------------------------------
-- 3) 复核查询：每一行都能被独立重算（不需要跑那个程序、不需要凭据）
-- ---------------------------------------------------------------------------
-- 这是"离线复算不会跟线上漂移"的最终形态：markout_bps_* 只是 (side, fill_px, mid_*)
-- 的函数，复核方可以完全不信那一列，就地重算。
-- 与之等价的 Go 侧入口是 marketmaker.MarkoutBps / MarkoutRecord.Verify，
-- 二者共用 markout.go 里那一个 markoutBps 实现，不存在第二份公式。
-- 下面这段与 markoutdb 的 TestPersistedRowIsRecheckableInPureSQL 是同一条，那里跑过。
--
-- 期望结果：0 行。任何非 0 都说明那些行本身坏了，不要拿去下判断。
-- markout_bps_1s IS NOT NULL 是必须的：陈旧的那一档故意写 NULL（mid_1s 仍有值），
-- 那不是"对不上"，是"这一档没有可用的 markout"。
SELECT COUNT(*) AS mismatched_rows
FROM markout_fills
WHERE mid_1s > 0
  AND markout_bps_1s IS NOT NULL
  AND ABS((CASE WHEN side = 'sell' THEN -1 ELSE 1 END)
          * (mid_1s - fill_px) / fill_px * 10000 - markout_bps_1s) > 1e-6;
-- 5s / 30s 同理，把 mid_1s/markout_bps_1s 换成对应列即可。
--
-- 配套的第二条：NULL 和 stale 必须是同一件事的两面（写入端由 markoutdb 保证）。
-- 期望结果：0 行。非 0 说明落库端把标记和值写岔了。
SELECT COUNT(*) AS flag_value_disagreements
FROM markout_fills
WHERE (stale_1s  = 1 AND markout_bps_1s  IS NOT NULL)
   OR (stale_5s  = 1 AND markout_bps_5s  IS NOT NULL)
   OR (stale_30s = 1 AND markout_bps_30s IS NOT NULL)
   OR (stale_1s  = 0 AND mid_1s  > 0 AND markout_bps_1s  IS NULL)
   OR (stale_5s  = 0 AND mid_5s  > 0 AND markout_bps_5s  IS NULL)
   OR (stale_30s = 0 AND mid_30s > 0 AND markout_bps_30s IS NULL);

-- ---------------------------------------------------------------------------
-- 4) 主查询：逐 symbol × 腿 × 参数版本的 markout（这就是台账 #7 那张表的线上版）
-- ---------------------------------------------------------------------------
-- 四个刻意的写法：
--   * 【不再需要】手写 lag 阈值：陈旧样本的 markout_bps_* 已经是 NULL，AVG 天然跳过它。
--     原来这里写 `AND lag_30s_ms < 2000` 是因为代码不设上限，只能在查询期补救 ——
--     那等于把口径交给每个写查询的人，谁忘了写谁的数就是脏的。现在口径在写入端。
--   * COUNT(*) 与 COUNT(markout_bps_*) 并列：前者是这段时间有多少笔，
--     后者是每个尺度上真正能用的有多少笔。两个数差很多 = 行情喂得不够密，
--     此时哪怕均值好看也别下结论（分母不同，跨 horizon 不可直接比较）。
--   * 净值在查询里现算（markout − fee_bps），不落库 ——
--     以后费率认知变了，改这一句就行，不会出现第二个"真相来源"；
--   * 按 param_version_id 分组：换了参数就是另一组样本，混在一起平均没有意义。
--
-- 这里【不】加 complete = 1：那会把"30s 陈旧但 1s 好"的行整行扔掉，
-- 而那一行的 1s 是好数据。按列各自取用比按行一刀切留下的样本多，且同样干净。
SELECT
    m.symbol,
    m.side,
    m.param_version_id,
    v.label                              AS param_label,
    COUNT(*)                             AS fills,
    COUNT(m.markout_bps_1s)              AS n_1s,      -- 各尺度真正可用的笔数
    COUNT(m.markout_bps_5s)              AS n_5s,
    COUNT(m.markout_bps_30s)             AS n_30s,
    AVG(m.markout_bps_1s)                AS mk_1s,
    AVG(m.markout_bps_5s)                AS mk_5s,
    AVG(m.markout_bps_30s)               AS mk_30s,
    AVG(m.markout_bps_30s - m.fee_bps)   AS net_30s,   -- 单腿扣费后
    AVG((m.mid_at_fill - m.fill_px) / m.fill_px * 10000
        * (CASE WHEN m.side = 'sell' THEN -1 ELSE 1 END)) AS edge_at_fill_bps
FROM markout_fills m
LEFT JOIN strategy_param_versions v ON v.id = m.param_version_id
WHERE m.fill_ts >= NOW() - INTERVAL 7 DAY
GROUP BY m.symbol, m.side, m.param_version_id, v.label
ORDER BY m.symbol, m.side;

-- ---------------------------------------------------------------------------
-- 5) 落库自检（建完表、重启之后跑这几条确认链路真的通了）
-- ---------------------------------------------------------------------------
-- 5.1 有没有在写、写了多少、缺口多大：
SELECT DATE(fill_ts)                                   AS day,
       COUNT(*)                                        AS rows_written,
       SUM(complete = 0)                               AS incomplete_rows,
       -- 采到样本但太晚，被判不可用的（逐尺度分开数：它们的阈值不同）
       SUM(stale_1s  = 1)                              AS stale_1s_rows,
       SUM(stale_5s  = 1)                              AS stale_5s_rows,
       SUM(stale_30s = 1)                              AS stale_30s_rows,
       -- 从头到尾就没有样本的（和上面三个是不同的缺口，别混着看）
       SUM(mid_30s = 0 OR mid_30s IS NULL)             AS unsampled_30s_rows,
       SUM(param_version_id IS NULL)                   AS unversioned_rows
FROM markout_fills
GROUP BY DATE(fill_ts)
ORDER BY day DESC;
-- rows_written 恒为 0 → 引擎没在落（表名/重启/enabled 三选一没到位）。
-- incomplete_rows 或 stale_*_rows 占比高 → 行情样本喂得不够密，
--   markout 的时间尺度不可信，先修行情再谈结论。
-- ⚠️ 如果 stale_1s_rows / stale_5s_rows 突然接近 100%，按顺序查这两件事：
--    a) refresh_ms —— 陈旧下限 2s 是按 refresh_ms=1000（config.go:120 的默认值）定的，
--       调到 2000 以上就会大面积误杀这两档；
--    b) 本机时钟 —— lag = 样本时刻(【我们的】时钟) − (fill_ts(【交易所的】时钟) + horizon)。
--       两边的钟差会原封不动进到 lag 里：本机慢 2s，所有 lag 就整体 +2s，
--       1s/5s 两档会集体判陈旧，而行情其实一直好好的。
--       下限取 2s 而不是贴着 refresh_ms 取 1.2s，一半就是为了吃掉这点钟差。
--    这两条是本表设计里仅有的隐藏耦合，所以它们被做成"数得出来"的——而不是靠人记得。
--    自查：SELECT AVG(mid_at_fill_lag_ms) FROM markout_fills WHERE fill_ts >= NOW() - INTERVAL 1 HOUR;
--    它衡量的是同一个钟差（成交时刻 − 成交前最后一个样本），正常应在 0~refresh_ms 量级；
--    若它也整体偏大，问题在时钟/轮询而不在行情。
-- unversioned_rows > 0 → 参数版本登记失败，仍可按 param_hash 归组。
--
-- 5.2 参数版本长什么样（做市的版本挂在合成 strategy_id 上，形如 mm:gate:ONG_USDT）：
SELECT id, strategy_id, label, seq, config_hash, effective_from, effective_to, is_current
FROM strategy_param_versions
WHERE strategy_id LIKE 'mm:%'
ORDER BY strategy_id, seq;
-- 这些行由 markoutdb 在启动时登记，配置没变不开新版、变了才开新版并给旧版关窗。
-- config_json 里【不含任何凭据】（只有 PairConfig + feed/observe_only），
-- 由 TestParamVersionIsAppendOnlyAndSecretFree 锁死。
--
-- 5.3 进程内的落库计数（写入端有没有在丢/在失败）：
--     marketmaker.MarkoutSinkCounters() —— enqueued / written / failed / dropped。
--     dropped 持续增长 = 缓冲一直满，写入端跟不上；
--     failed 增长 = 表不存在或 DB 出错（这两种情况下交易路径都不受影响）。

-- ---------------------------------------------------------------------------
-- 6) 回滚
-- ---------------------------------------------------------------------------
-- 关掉落库有两条路，都不需要改代码：
--   a) 只想停写、保留已有数据 → 把表改名，进程下次重启时 HasTable 判 false 即停：
--        RENAME TABLE markout_fills TO markout_fills_disabled;
--   b) 彻底撤掉 → 直接删（没有外键指向它，删了不影响任何既有功能）：
--        DROP TABLE IF EXISTS markout_fills;
--
-- 顺带：markoutdb 在 strategy_param_versions 里登记的做市版本行是 append-only 的，
-- 删表不会连带清掉它们。要一并清（只影响做市，不碰任何真实策略的版本）：
--   DELETE FROM strategy_param_versions WHERE strategy_id LIKE 'mm:%';
