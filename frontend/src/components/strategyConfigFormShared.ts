export type StrategyFormConfig = {
  symbol: string;
  symbols: string;
  leverage: number;
  order_amount_mode: string;
  trade_amount: number;
  order_amount_pct: number;
  max_initial_margin_usdt: number;
  take_profit_pct: number;
  stop_loss_pct: number;
  max_concurrent_positions: number;
  max_consecutive_entries_per_symbol: number;
  symbol_reentry_cooldown_minutes: number;
  max_trades_per_day: number;
  warmup_bars: number;
  auto_symbols: boolean;
  symbol_select_mode: string;
  min_price: number;
  max_price: number;
  min_precision: number;
  min_volatility: number;
  select_limit: number;
  non_natural_entry_enabled: boolean;
  non_natural_entry_sequence: string;
  entry_time_windows: string;
  auto_optimize_enabled: boolean;
  auto_optimize_model: string;
  auto_optimize_api_key: string;
  log_level: 'quiet' | 'normal' | 'verbose' | 'debug';
  // === 策略阈值（meme contract engine 内部参数）===
  min_confidence: number;     // 0-1，触发信号的最低置信度（默认 0.35）
  atr_tp_mult: number;        // 动态止盈 = ATR × 此倍数（默认 2.0）
  atr_sl_mult: number;        // 动态止损 = ATR × 此倍数（默认 1.0）
  volume_ratio_min: number;   // 量比阈值（默认 1.2）
  // === 信号方向 / 黑名单（Go backend isAllowedSide / isBlacklistedSymbol）===
  allowed_sides: 'both' | 'buy' | 'sell'; // 'both'=多空双开 / 'buy'=只多 / 'sell'=只空
  symbol_blacklist: string;   // 逗号分隔的 symbol 黑名单
  use_exchange_tpsl: boolean; // 是否强制在交易所挂 TP/SL（推荐 true）
};

export type StrategyConfigMarketSymbol = {
  symbol: string;
  base_asset: string;
  quote_asset: string;
  last_price: number;
  quote_volume_24h: number;
};

export type StrategyConfigTemplateOption = {
  id: number;
  name: string;
  description: string;
  is_enabled: boolean;
};

export type StrategyConfigTemplateUsage = {
  template_id?: number;
};

const getCfgString = (cfg: Record<string, unknown>, key: string, fallback: string) => {
  const v = cfg[key];
  return typeof v === 'string' ? v : fallback;
};

const getCfgNumber = (cfg: Record<string, unknown>, key: string, fallback: number) => {
  const v = cfg[key];
  return typeof v === 'number' ? v : fallback;
};

const getCfgBool = (cfg: Record<string, unknown>, key: string, fallback: boolean) => {
  const v = cfg[key];
  if (typeof v === 'boolean') return v;
  if (typeof v === 'number') return v !== 0;
  if (typeof v === 'string') {
    const normalized = v.trim().toLowerCase();
    if (['true', '1', 'yes', 'y', 'on'].includes(normalized)) return true;
    if (['false', '0', 'no', 'n', 'off'].includes(normalized)) return false;
  }
  return fallback;
};

export const parseFixedSymbols = (raw: string): string[] => {
  return (raw || '').split(',').map(s => s.trim()).filter(Boolean);
};

export const ratioToPercent = (value: number, fallback: number) => {
  if (!Number.isFinite(value)) return fallback;
  if (value > 0 && value <= 1) return value * 100;
  return value;
};

export const createDefaultStrategyConfig = (): StrategyFormConfig => ({
  symbol: 'BTCUSDT',
  symbols: '',
  leverage: 20,
  order_amount_mode: 'notional',
  trade_amount: 300,
  order_amount_pct: 10,
  max_initial_margin_usdt: 50,
  take_profit_pct: 30,
  stop_loss_pct: 10,
  max_concurrent_positions: 1,
  max_consecutive_entries_per_symbol: 0,
  symbol_reentry_cooldown_minutes: 0,
  max_trades_per_day: 3,
  warmup_bars: 100,
  auto_symbols: false,
  symbol_select_mode: 'manual',
  min_price: 0,
  max_price: 5,
  min_precision: 5,
  min_volatility: 5,
  select_limit: 20,
  non_natural_entry_enabled: false,
  non_natural_entry_sequence: '多,多,多,多,空',
  entry_time_windows: '',
  auto_optimize_enabled: false,
  auto_optimize_model: 'anthropic/claude-opus-4.8-fast',
  auto_optimize_api_key: '',
  log_level: 'normal',
  min_confidence: 0.35,
  atr_tp_mult: 2.0,
  atr_sl_mult: 1.0,
  volume_ratio_min: 1.2,
  allowed_sides: 'both',
  symbol_blacklist: '',
  use_exchange_tpsl: true,
});

export const strategyConfigFromExisting = (cfg: Record<string, unknown>): StrategyFormConfig => {
  const symbolsRaw = cfg?.symbols;
  return {
    symbol: getCfgString(cfg, 'symbol', 'BTCUSDT'),
    symbols: Array.isArray(symbolsRaw) ? (symbolsRaw as unknown[]).map(String).join(',') : (typeof symbolsRaw === 'string' ? symbolsRaw : ''),
    leverage: getCfgNumber(cfg, 'leverage', 20),
    order_amount_mode: getCfgString(cfg, 'order_amount_mode', 'notional'),
    trade_amount: getCfgNumber(cfg, 'trade_amount', 300),
    order_amount_pct: ratioToPercent(getCfgNumber(cfg, 'order_amount_pct', 0.1), 10),
    max_initial_margin_usdt: getCfgNumber(cfg, 'max_initial_margin_usdt', 50),
    take_profit_pct: ratioToPercent(getCfgNumber(cfg, 'take_profit_pct', 0.3), 30),
    stop_loss_pct: ratioToPercent(getCfgNumber(cfg, 'stop_loss_pct', 0.1), 10),
    max_concurrent_positions: getCfgNumber(cfg, 'max_concurrent_positions', 1),
    max_consecutive_entries_per_symbol: getCfgNumber(cfg, 'max_consecutive_entries_per_symbol', 0),
    symbol_reentry_cooldown_minutes: getCfgNumber(cfg, 'symbol_reentry_cooldown_minutes', 0),
    max_trades_per_day: getCfgNumber(cfg, 'max_trades_per_day', 3),
    warmup_bars: getCfgNumber(cfg, 'warmup_bars', 100),
    auto_symbols: getCfgBool(cfg, 'auto_symbols', false),
    symbol_select_mode: getCfgString(cfg, 'symbol_select_mode', 'manual'),
    min_price: getCfgNumber(cfg, 'min_price', 0),
    max_price: getCfgNumber(cfg, 'max_price', 5),
    min_precision: getCfgNumber(cfg, 'min_precision', 5),
    min_volatility: getCfgNumber(cfg, 'min_volatility', 5),
    select_limit: getCfgNumber(cfg, 'select_limit', 20),
    non_natural_entry_enabled: getCfgBool(cfg, 'non_natural_entry_enabled', false),
    non_natural_entry_sequence: getCfgString(cfg, 'non_natural_entry_sequence', '多,多,多,多,空'),
    entry_time_windows: getCfgString(cfg, 'entry_time_windows', ''),
    auto_optimize_enabled: getCfgBool(cfg, 'auto_optimize_enabled', false),
    auto_optimize_model: getCfgString(cfg, 'auto_optimize_model', ''),
    auto_optimize_api_key: getCfgString(cfg, 'auto_optimize_api_key', ''),
    log_level: ((): 'quiet' | 'normal' | 'verbose' | 'debug' => {
      const raw = String(cfg?.log_level ?? '').toLowerCase().trim();
      return (raw === 'quiet' || raw === 'verbose' || raw === 'debug') ? raw : 'normal';
    })(),
    min_confidence: getCfgNumber(cfg, 'min_confidence', 0.35),
    atr_tp_mult: getCfgNumber(cfg, 'atr_tp_mult', 2.0),
    atr_sl_mult: getCfgNumber(cfg, 'atr_sl_mult', 1.0),
    volume_ratio_min: getCfgNumber(cfg, 'volume_ratio_min', 1.2),
    allowed_sides: ((): 'both' | 'buy' | 'sell' => {
      const raw = cfg?.allowed_sides;
      if (Array.isArray(raw)) {
        const sides = raw.map(s => String(s).toLowerCase().trim());
        if (sides.includes('buy') && sides.includes('sell')) return 'both';
        if (sides.includes('sell') && !sides.includes('buy')) return 'sell';
        if (sides.includes('buy') && !sides.includes('sell')) return 'buy';
      }
      if (typeof raw === 'string') {
        const v = raw.toLowerCase().trim();
        if (v === 'buy' || v === 'sell' || v === 'both') return v;
      }
      return 'both'; // 默认双向
    })(),
    symbol_blacklist: ((): string => {
      const raw = cfg?.symbol_blacklist;
      if (Array.isArray(raw)) return raw.map(s => String(s).trim()).filter(Boolean).join(',');
      if (typeof raw === 'string') return raw.trim();
      return '';
    })(),
    use_exchange_tpsl: getCfgBool(cfg, 'use_exchange_tpsl', true),
  };
};

/**
 * 构造发给 backend 的 config payload。
 *
 * rawConfig: 编辑现有实例时传入"原 config"，用于**保留所有 form 不认识的
 * 自定义字段**（比如 allowed_sides / symbol_blacklist / 任何用户/SQL 加的字段）。
 * 不传或传 undefined → 仅 form 字段（用于新建实例）。
 *
 * 合并语义：rawConfig 先铺底，再用 form 字段覆盖。form 没动的自定义字段保留。
 */
export const buildStrategyConfigPayload = (
  cfg: StrategyFormConfig,
  rawConfig?: Record<string, unknown>,
): Record<string, unknown> => {
  const formFields: Record<string, unknown> = {
    symbol: cfg.symbol.trim(),
    symbols: cfg.symbols.trim(),
    leverage: Number(cfg.leverage) || 20,
    order_amount_mode: cfg.order_amount_mode,
    trade_amount: Number(cfg.trade_amount) || 0,
    order_amount_pct: (Number(cfg.order_amount_pct) || 0) / 100,
    max_initial_margin_usdt: Number(cfg.max_initial_margin_usdt) || 0,
    take_profit_pct: (Number(cfg.take_profit_pct) || 0) / 100,
    stop_loss_pct: (Number(cfg.stop_loss_pct) || 0) / 100,
    max_concurrent_positions: Number(cfg.max_concurrent_positions) || 1,
    max_consecutive_entries_per_symbol: Number(cfg.max_consecutive_entries_per_symbol) || 0,
    symbol_reentry_cooldown_minutes: Number(cfg.symbol_reentry_cooldown_minutes) || 0,
    max_trades_per_day: Number(cfg.max_trades_per_day) || 0,
    warmup_bars: Number(cfg.warmup_bars) || 0,
    auto_symbols: cfg.auto_symbols,
    symbol_select_mode: cfg.symbol_select_mode,
    min_price: Number(cfg.min_price) || 0,
    max_price: Number(cfg.max_price) || 0,
    min_precision: Number(cfg.min_precision) || 0,
    min_volatility: Number(cfg.min_volatility) || 0,
    select_limit: Number(cfg.select_limit) || 20,
    non_natural_entry_enabled: cfg.non_natural_entry_enabled,
    non_natural_entry_sequence: cfg.non_natural_entry_sequence.trim(),
    entry_time_windows: cfg.entry_time_windows.trim(),
    auto_optimize_enabled: cfg.auto_optimize_enabled,
    auto_optimize_model: cfg.auto_optimize_model.trim(),
    auto_optimize_api_key: cfg.auto_optimize_api_key.trim(),
    log_level: cfg.log_level || 'normal',
    min_confidence: Number(cfg.min_confidence) || 0.35,
    atr_tp_mult: Number(cfg.atr_tp_mult) || 2.0,
    atr_sl_mult: Number(cfg.atr_sl_mult) || 1.0,
    volume_ratio_min: Number(cfg.volume_ratio_min) || 1.2,
    // allowed_sides 序列化：UI 是 'both' | 'buy' | 'sell'，写到 config 用数组
    // 这样和 Go backend 的 isAllowedSide 解析逻辑兼容
    allowed_sides:
      cfg.allowed_sides === 'both'
        ? ['buy', 'sell']
        : cfg.allowed_sides === 'sell'
          ? ['sell']
          : ['buy'],
    symbol_blacklist: cfg.symbol_blacklist
      .split(',')
      .map(s => s.trim().toUpperCase())
      .filter(Boolean),
    use_exchange_tpsl: cfg.use_exchange_tpsl,
  };
  if (rawConfig && typeof rawConfig === 'object') {
    // 关键修复：合并 rawConfig 先（保留所有未知字段如 allowed_sides /
    // symbol_blacklist），再用 form 字段覆盖。这样用户通过 SQL 加的字段
    // 不会因为 form 保存而丢失。
    return { ...rawConfig, ...formFields };
  }
  return formFields;
};
