-- ============================================================
-- P0 策略优化 SQL Patch
-- 基于 60 天 Binance 实盘数据分析，应用以下变更：
--
-- 1. symbols 白名单：只保留 4 个证明赚钱的标的
--    BILLUSDT / UBUSDT / PRLUSDT / TRUTHUSDT
--
-- 2. symbol_blacklist：永久禁掉历史亏损最严重的标的
--    SAHARAUSDT / WIFUSDT / AIOUSDT / DODOXUSDT / 1000PEPEUSDT
--    （即使 symbols 白名单变了也不会碰这些）
--
-- 3. allowed_sides = ["sell"]：关掉 long 信号
--    60 天 long 净亏 -60.78，short 净赚 +22.92
--
-- 4. entry_time_windows：禁掉 UTC 18:00-23:00（北京 02-07 点）
--    该时段累亏 -23 USDT
--
-- 5. trade_amount：从默认值调到 8 USDT
--    历史平均仓位 $37，账户 $35，相当于 35x 杠杆。
--    SAHARA 那笔 $1224 notional 的灾难就是这么来的。
--
-- 6. hunger_after_minutes 至少 5 分钟、stop_loss_pct >= 1.5%
--    避免噪声触发；前 3 分钟桶胜率仅 13.3%
--
-- 使用方法：
--   1. 把 <STRATEGY_ID> 换成你要改的 instance.id
--   2. 在 MySQL 客户端执行整个文件，或者按单条复制
--   3. 通过 API 调 /api/strategies/<ID>/stop 然后 start 重启实例
--      （配置改完不重启不生效）
--
-- 回滚：每条 UPDATE 前都有对应的 SELECT 备份；执行前先把当前 config
-- 复制到笔记本里，万一改坏可以原样写回。
-- ============================================================

-- 备份：先记下当前 config（执行前手动复制结果）
SELECT id, name, status, config
FROM strategy_instances
WHERE id = '<STRATEGY_ID>';

-- ============================================================
-- 应用优化：把多个 JSON 字段合并进 config
-- ============================================================
-- MySQL 8+ 支持 JSON_MERGE_PATCH。如果是 SQLite，下面有 SQLite 版本。

-- ---- MySQL 8+ ----
UPDATE strategy_instances
SET config = JSON_MERGE_PATCH(
    CAST(config AS JSON),
    JSON_OBJECT(
        'symbols',              JSON_ARRAY('BILLUSDT', 'UBUSDT', 'PRLUSDT', 'TRUTHUSDT'),
        'symbol_blacklist',     JSON_ARRAY('SAHARAUSDT', 'WIFUSDT', 'AIOUSDT', 'DODOXUSDT', '1000PEPEUSDT', 'PLAYUSDT', 'MAGMAUSDT'),
        'allowed_sides',        JSON_ARRAY('sell'),
        'entry_time_windows',   '00:00-18:00',
        'trade_amount',         8,
        'hunger_after_minutes', 5,
        'stop_loss_pct',        1.5,
        'symbol_reentry_cooldown_minutes', 10,
        'max_concurrent_positions',        2
    )
),
updated_at = NOW()
WHERE id = '<STRATEGY_ID>';

-- ---- SQLite 版本（手动构造 JSON）----
-- 注意：SQLite 的 json_patch / json_set 行为略有不同。如果你的库是 sqlite，用这条：
--
-- UPDATE strategy_instances
-- SET config = json_patch(
--     config,
--     json_object(
--         'symbols', json_array('BILLUSDT','UBUSDT','PRLUSDT','TRUTHUSDT'),
--         'symbol_blacklist', json_array('SAHARAUSDT','WIFUSDT','AIOUSDT','DODOXUSDT','1000PEPEUSDT','PLAYUSDT','MAGMAUSDT'),
--         'allowed_sides', json_array('sell'),
--         'entry_time_windows', '00:00-18:00',
--         'trade_amount', 8,
--         'hunger_after_minutes', 5,
--         'stop_loss_pct', 1.5,
--         'symbol_reentry_cooldown_minutes', 10,
--         'max_concurrent_positions', 2
--     )
-- ),
-- updated_at = datetime('now')
-- WHERE id = '<STRATEGY_ID>';

-- 验证：看一下结果
SELECT id, name, status,
       JSON_EXTRACT(config, '$.symbols')              AS new_symbols,
       JSON_EXTRACT(config, '$.allowed_sides')        AS new_sides,
       JSON_EXTRACT(config, '$.symbol_blacklist')     AS new_blacklist,
       JSON_EXTRACT(config, '$.trade_amount')         AS new_trade_amt,
       JSON_EXTRACT(config, '$.stop_loss_pct')        AS new_sl_pct,
       JSON_EXTRACT(config, '$.entry_time_windows')   AS new_time_window
FROM strategy_instances
WHERE id = '<STRATEGY_ID>';

-- ============================================================
-- 应用后必须做的事
-- ============================================================
-- 1. 调用：POST /api/strategies/<STRATEGY_ID>/stop
-- 2. 调用：POST /api/strategies/<STRATEGY_ID>/start
-- 3. 看日志：应该出现 "跳过信号：方向不在 allowed_sides" 类的 INFO 日志
--    （证明 long 信号确实被过滤了）
-- 4. 24h 后看一下 daily PnL，对比之前。
