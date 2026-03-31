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
  return typeof v === 'boolean' ? v : fallback;
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
  };
};

export const buildStrategyConfigPayload = (cfg: StrategyFormConfig) => ({
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
});
