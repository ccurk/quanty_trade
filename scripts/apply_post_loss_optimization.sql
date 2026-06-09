-- ============================================================================
-- Apply post-loss optimization based on 7-day trade data
-- 数据：57 笔配对，胜率 17.5%，毛 PnL -$6.99，手续费 -$2.46，净 -$9.45
-- ============================================================================
-- 关键发现：
--   1. SHORT (-$7.95) 损失是 LONG (-$1.50) 的 5.3 倍 → 当前市场偏多
--   2. <1min 持仓 0% 胜率（12 笔，-$5.13） → whipsaw 主导
--   3. 5-15min 区间是唯一盈利（+$2.36）→ 信号需要"喘息"时间
--   4. ALLO/BILL/MOVE 是亏损主力
-- ----------------------------------------------------------------------------
-- 应用方式：
--   SET @strategy_id = '8eb182b6-ee74-4125-a602-f0a91f376432';
--   然后运行下面整段
-- ============================================================================

SET @strategy_id = '8eb182b6-ee74-4125-a602-f0a91f376432';

-- ============================================================================
-- 改动 A：阈值/方向/冷却（影响最大）
-- ============================================================================
UPDATE strategy_instances
SET config = JSON_MERGE_PATCH(
    CAST(config AS JSON),
    JSON_OBJECT(
        -- 改动 A1：放开两边，但当前数据说明 LONG 更佳；保守起见用双向
        'allowed_sides', JSON_ARRAY('buy', 'sell'),

        -- 改动 A2：置信度门槛 0.30 → 0.40（17.5% wr 说明噪音太多）
        'min_confidence', 0.40,

        -- 改动 A3：放量倍数 1.2 → 1.5（更严格的放量确认）
        'volume_ratio_min', 1.5,

        -- 改动 A4：同方向冷却 300s → 900s（15 分钟），避免重复抓同一波动
        'cooldown_sec', 900,

        -- 改动 A5：拉宽 R:R — ATR_TP×2 → ×2.5；ATR_SL×1 → ×1.2
        -- 当前 R:R=2.46 但 wr 太低；新比例 ~3:1，breakeven wr 降到 25%
        'atr_tp_mult', 2.5,
        'atr_sl_mult', 1.2,

        -- 改动 A6：新增的硬过滤旋钮（在新脚本里生效）
        'trend_confirm_bars', 3,    -- EMA 方向必须稳定 3 根 K 线
        'chop_lookback', 6,         -- 6 根回看判定震荡
        'reject_on_chop', true,     -- 震荡时不开仓
        'max_atr_pct', 8.0,         -- ATR%>8 极端波动拒
        'min_atr_pct_for_trade', 0.15,  -- ATR%<0.15 死水拒

        -- 改动 A7：symbol 黑名单（数据里最亏的）
        'symbol_blacklist', JSON_ARRAY(
            'ALLOUSDT',    -- -$6.70（38 次）
            'MOVEUSDT',    -- -$4.29（16 次）
            'BILLUSDT',    -- -$3.02（52 次，过度交易）
            'UBUSDT',      -- -$2.67（20 次）
            'GWEIUSDT',    -- -$1.96（13 次）
            'EPICUSDT',    -- -$1.76（11 次）
            'UAIUSDT',     -- -$1.44（7 次）
            'BLESSUSDT',   -- -$1.51（28 次，过度交易）
            'PIPPINUSDT'   -- -$1.28（9 次）
        )
    )
),
updated_at = NOW()
WHERE id = @strategy_id;

-- ============================================================================
-- 改动 B：仓位大小（按余额比例的护栏）
-- ============================================================================
-- 当前账户 $12，按 100% 1 笔下，单笔 notional = $12 × 5 = $60
-- 极端不利下一笔可亏 6-15%，连亏 3 次直接清零。
--
-- 建议：先降到 30%，存活下来再加。同时把 max_init=0 改回 20，
-- 防止账户大涨后又意外重仓。
UPDATE strategy_instances
SET config = JSON_MERGE_PATCH(
    CAST(config AS JSON),
    JSON_OBJECT(
        'order_amount_pct', 0.30,           -- 100% → 30%
        'max_initial_margin_usdt', 20.0,    -- 重新启用保护
        'leverage', 5                       -- 不变，确认是 5x
    )
),
updated_at = NOW()
WHERE id = @strategy_id;

-- ============================================================================
-- 验证最终 config
-- ============================================================================
SELECT
    id,
    name,
    JSON_EXTRACT(config, '$.order_amount_mode')          AS mode,
    JSON_EXTRACT(config, '$.order_amount_pct')           AS pct,
    JSON_EXTRACT(config, '$.max_initial_margin_usdt')    AS max_init,
    JSON_EXTRACT(config, '$.leverage')                   AS lev,
    JSON_EXTRACT(config, '$.allowed_sides')              AS sides,
    JSON_EXTRACT(config, '$.min_confidence')             AS min_conf,
    JSON_EXTRACT(config, '$.atr_tp_mult')                AS tp_mult,
    JSON_EXTRACT(config, '$.atr_sl_mult')                AS sl_mult,
    JSON_EXTRACT(config, '$.cooldown_sec')               AS cooldown,
    JSON_EXTRACT(config, '$.volume_ratio_min')           AS vol_min,
    JSON_EXTRACT(config, '$.trend_confirm_bars')         AS trend_bars,
    JSON_EXTRACT(config, '$.reject_on_chop')             AS reject_chop,
    JSON_EXTRACT(config, '$.symbol_blacklist')           AS blacklist
FROM strategy_instances
WHERE id = @strategy_id\G

-- ============================================================================
-- 预期效果（粗估）
-- ============================================================================
-- A1-A4：减少 50-70% 信号数（放掉的多是噪音）
-- A5：R:R 拉到 2.92:1，breakeven wr 从 28.9% 降到 25.5%
-- A6：屏蔽 whipsaw / 震荡，应该消灭 <1min 持仓的 0% wr 灾区
-- A7：直接排除净亏 -$22.62 的 9 个 symbol
-- B：单笔最大风险 $4 → $1.2，连亏 5 次还有 50% 余地翻盘
--
-- 预期胜率从 17.5% → 25-35%（不保证，需观察 24-48h）
