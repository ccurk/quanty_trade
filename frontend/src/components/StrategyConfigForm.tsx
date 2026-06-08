import { Info } from 'lucide-react';
import { parseFixedSymbols, type StrategyConfigMarketSymbol, type StrategyConfigTemplateOption, type StrategyConfigTemplateUsage, type StrategyFormConfig } from './strategyConfigFormShared';

type StrategyConfigFormProps = {
  isDarkMode: boolean;
  config: StrategyFormConfig;
  onChange: (next: StrategyFormConfig) => void;
  marketSymbols: StrategyConfigMarketSymbol[];
  isLoadingMarketSymbols: boolean;
  onRefreshMarketSymbols: () => void;
  symbolSearch: string;
  onSymbolSearchChange: (value: string) => void;
  showTemplateSelector?: boolean;
  selectedTemplate?: number;
  onSelectedTemplateChange?: (value: number) => void;
  templates?: StrategyConfigTemplateOption[];
  strategyTemplateUsages?: StrategyConfigTemplateUsage[];
};

export function StrategyConfigForm({
  isDarkMode,
  config,
  onChange,
  marketSymbols,
  isLoadingMarketSymbols,
  onRefreshMarketSymbols,
  symbolSearch,
  onSymbolSearchChange,
  showTemplateSelector = false,
  selectedTemplate = 0,
  onSelectedTemplateChange,
  templates = [],
  strategyTemplateUsages = [],
}: StrategyConfigFormProps) {
  const selectedSymbols = parseFixedSymbols(config.symbols || config.symbol || '');
  const selectedTemplateInfo = templates.find(t => t.id === selectedTemplate);
  const optimizeModelOptions = [
    { value: 'anthropic/claude-opus-4.8-fast', label: 'Claude Opus 4.8 Fast', desc: '默认推荐，固定通过 OpenRouter chat/completions 调用。' },
    { value: 'anthropic/claude-opus-4.7', label: 'Claude Opus 4.7', desc: 'OpenRouter 官方路由名，适合作为 4.8 的备用版本。' },
  ];
  const selectedModelInfo = optimizeModelOptions.find(item => item.value === config.auto_optimize_model) || optimizeModelOptions[0];

  return (
    <div className="space-y-4">
      <div>
        <label className="block text-sm font-medium text-gray-500 mb-2">交易对</label>
        <div className={`rounded-2xl border p-4 ${isDarkMode ? 'border-gray-800 bg-gray-950/30' : 'border-gray-200 bg-gray-50'}`}>
          <div className="flex items-center justify-between gap-4 mb-3">
            <div className="text-xs text-gray-500">可搜索多选（选中后会同时监控这些交易对）</div>
            <div className="flex gap-2">
              <button
                onClick={onRefreshMarketSymbols}
                className={`px-3 py-1.5 rounded-lg text-xs font-bold border ${isDarkMode ? 'bg-gray-900 border-gray-800 text-gray-200 hover:bg-gray-800' : 'bg-white border-gray-200 text-gray-700 hover:bg-gray-100'}`}
                disabled={isLoadingMarketSymbols}
                type="button"
              >
                {isLoadingMarketSymbols ? '加载中...' : '刷新列表'}
              </button>
              <button
                onClick={() => onChange({ ...config, symbols: '', symbol: '' })}
                className={`px-3 py-1.5 rounded-lg text-xs font-bold border ${isDarkMode ? 'bg-gray-900 border-gray-800 text-gray-200 hover:bg-gray-800' : 'bg-white border-gray-200 text-gray-700 hover:bg-gray-100'}`}
                type="button"
              >
                清空选择
              </button>
            </div>
          </div>

          {/* 推荐交易对快捷选择：基于 60 天历史数据复盘出的盈利子集（BILL/UB/PRL/TRUTH）。
              即使 /api/markets/symbols 返回的 500 个 binance 实时列表里因 24h
              交易量靠后被截掉，这里也能一键选上。点击会切换添加/移除。 */}
          <div className={`mb-3 p-3 rounded-xl border ${isDarkMode ? 'border-blue-900/40 bg-blue-950/20' : 'border-blue-200 bg-blue-50/60'}`}>
            <div className="flex items-center justify-between gap-2 mb-2">
              <div className={`text-xs font-bold ${isDarkMode ? 'text-blue-200' : 'text-blue-800'}`}>💡 推荐交易对（基于历史盈利复盘）</div>
              <button
                type="button"
                onClick={() => {
                  if (config.auto_symbols) return;
                  const recommended = ['BILLUSDT', 'UBUSDT', 'PRLUSDT', 'TRUTHUSDT'];
                  const merged = Array.from(new Set([...selectedSymbols, ...recommended]));
                  onChange({ ...config, symbols: merged.join(','), symbol: merged[0] || '' });
                }}
                disabled={config.auto_symbols}
                className={`px-2 py-1 rounded text-xs font-bold border ${config.auto_symbols ? 'opacity-50 cursor-not-allowed' : ''} ${isDarkMode ? 'bg-blue-900/60 border-blue-700 text-blue-100 hover:bg-blue-800' : 'bg-blue-100 border-blue-300 text-blue-800 hover:bg-blue-200'}`}
              >
                一键全选 4 个
              </button>
            </div>
            <div className="flex flex-wrap gap-2">
              {['BILLUSDT', 'UBUSDT', 'PRLUSDT', 'TRUTHUSDT'].map(sym => {
                const checked = selectedSymbols.includes(sym);
                return (
                  <button
                    type="button"
                    key={sym}
                    onClick={() => {
                      if (config.auto_symbols) return;
                      const next = checked ? selectedSymbols.filter(s => s !== sym) : [...selectedSymbols, sym];
                      onChange({ ...config, symbols: next.join(','), symbol: next[0] || '' });
                    }}
                    disabled={config.auto_symbols}
                    className={`px-3 py-1 rounded-lg text-xs font-mono font-bold border transition ${
                      config.auto_symbols ? 'opacity-50 cursor-not-allowed' : ''
                    } ${
                      checked
                        ? (isDarkMode ? 'bg-green-600 border-green-500 text-white' : 'bg-green-500 border-green-600 text-white')
                        : (isDarkMode ? 'bg-gray-900 border-gray-700 text-gray-200 hover:bg-gray-800' : 'bg-white border-gray-300 text-gray-700 hover:bg-gray-100')
                    }`}
                  >
                    {checked ? '✓ ' : '+ '}{sym}
                  </button>
                );
              })}
            </div>
          </div>

          <input
            type="text"
            value={symbolSearch}
            onChange={(e) => onSymbolSearchChange(e.target.value)}
            placeholder="搜索交易对，例如 DOGE 或 DOGE/USDT"
            className={`w-full px-4 py-2 rounded-xl border text-sm transition outline-none mb-3 ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
          />

          <div className={`max-h-56 overflow-auto rounded-xl border ${isDarkMode ? 'border-gray-800 bg-gray-950/40' : 'border-gray-200 bg-white'}`}>
            {marketSymbols
              .filter(ms => {
                const q = symbolSearch.trim().toLowerCase();
                if (!q) return true;
                return ms.symbol.toLowerCase().includes(q) || ms.base_asset.toLowerCase().includes(q) || ms.quote_asset.toLowerCase().includes(q);
              })
              .slice(0, 200)
              .map(ms => {
                const checked = selectedSymbols.includes(ms.symbol);
                return (
                  <label key={ms.symbol} className={`flex items-center justify-between gap-3 px-3 py-2 text-sm cursor-pointer ${isDarkMode ? 'hover:bg-gray-900/60' : 'hover:bg-gray-50'}`}>
                    <div className="flex items-center gap-3 min-w-0">
                      <input
                        type="checkbox"
                        checked={checked}
                        disabled={config.auto_symbols}
                        onChange={() => {
                          if (config.auto_symbols) return;
                          const next = checked ? selectedSymbols.filter(s => s !== ms.symbol) : [...selectedSymbols, ms.symbol];
                          onChange({ ...config, symbols: next.join(','), symbol: next[0] || '' });
                        }}
                      />
                      <div className="min-w-0">
                        <div className="font-mono truncate">{ms.symbol}</div>
                        <div className="text-xs text-gray-500 truncate">{ms.base_asset}/{ms.quote_asset} 价格:{ms.last_price}</div>
                      </div>
                    </div>
                    <div className="text-xs text-gray-500 font-mono">Vol:{Math.round(ms.quote_volume_24h)}</div>
                  </label>
                );
              })}
          </div>

          <div className="mt-3 text-xs text-gray-500">
            已选 {selectedSymbols.length} 个
          </div>
        </div>
      </div>

      <div>
        <label className="block text-sm font-medium text-gray-500 mb-2">选币筛选（可选）</label>
        <div className={`rounded-2xl border p-4 ${isDarkMode ? 'border-gray-800 bg-gray-950/30' : 'border-gray-200 bg-gray-50'}`}>
          <div className="flex items-center justify-between gap-4 mb-3">
            <div className="text-xs text-gray-500">开启后由后端按条件筛选交易对，并把筛选结果下发给策略</div>
            <button
              type="button"
              onClick={() => {
                const enabled = !config.auto_symbols;
                onChange({
                  ...config,
                  auto_symbols: enabled,
                  symbol_select_mode: enabled ? 'filter' : 'manual',
                  ...(enabled ? { symbol: '', symbols: '' } : {}),
                });
              }}
              className={`px-3 py-1.5 rounded-lg text-xs font-bold border ${isDarkMode ? 'bg-gray-900 border-gray-800 text-gray-200 hover:bg-gray-800' : 'bg-white border-gray-200 text-gray-700 hover:bg-gray-100'}`}
            >
              {config.auto_symbols ? '已开启' : '未开启'}
            </button>
          </div>

          <div className="grid grid-cols-2 md:grid-cols-5 gap-3">
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">最小价格</label>
              <input
                type="number"
                value={config.min_price}
                onChange={(e) => onChange({ ...config, min_price: Number(e.target.value) })}
                className={`w-full px-3 py-2 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">最大价格 <span className="text-orange-500 font-normal">(0=屏蔽所有)</span></label>
              <input
                type="number"
                value={config.max_price}
                onChange={(e) => onChange({ ...config, max_price: Number(e.target.value) })}
                className={`w-full px-3 py-2 rounded-xl border text-sm transition outline-none ${
                  config.max_price === 0
                    ? (isDarkMode ? 'bg-red-900/30 border-red-700 text-red-200' : 'bg-red-50 border-red-300 text-red-700')
                    : (isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900')
                }`}
              />
              <div className="mt-1 text-[10px] text-gray-500">
                {config.max_price === 0 ? '⚠️ 当前 0 = 任何 symbol 都被过滤掉，请改成大于 0 的值（例如 1）' : '价格 ≤ 此值的 symbol 才允许交易'}
              </div>
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">最小精度</label>
              <input
                type="number"
                value={config.min_precision}
                onChange={(e) => onChange({ ...config, min_precision: Number(e.target.value) })}
                className={`w-full px-3 py-2 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">最小波动%</label>
              <input
                type="number"
                value={config.min_volatility}
                onChange={(e) => onChange({ ...config, min_volatility: Number(e.target.value) })}
                className={`w-full px-3 py-2 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">最多数量</label>
              <input
                type="number"
                value={config.select_limit}
                onChange={(e) => onChange({ ...config, select_limit: Number(e.target.value) })}
                className={`w-full px-3 py-2 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
              />
            </div>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <div>
          <label className="block text-sm font-medium text-gray-500 mb-2">杠杆</label>
          <input type="number" value={config.leverage} onChange={(e) => onChange({ ...config, leverage: Number(e.target.value) })} className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`} />
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-500 mb-2">下单模式</label>
          <select value={config.order_amount_mode} onChange={(e) => onChange({ ...config, order_amount_mode: e.target.value })} className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}>
            <option value="notional">固定名义额 USDT</option>
            <option value="qty">固定数量</option>
            <option value="percent_balance">可用余额百分比</option>
          </select>
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-500 mb-2">{config.order_amount_mode === 'percent_balance' ? '默认百分比(%)' : '下单值'}</label>
          <input type="number" value={config.order_amount_mode === 'percent_balance' ? config.order_amount_pct : config.trade_amount} onChange={(e) => onChange({ ...config, [config.order_amount_mode === 'percent_balance' ? 'order_amount_pct' : 'trade_amount']: Number(e.target.value) })} className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`} />
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-500 mb-2">最大初始保证金 USDT</label>
          <input type="number" value={config.max_initial_margin_usdt} onChange={(e) => onChange({ ...config, max_initial_margin_usdt: Number(e.target.value) })} className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`} />
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-500 mb-2">止盈收益率(%)</label>
          <input type="number" value={config.take_profit_pct} onChange={(e) => onChange({ ...config, take_profit_pct: Number(e.target.value) })} className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`} />
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-500 mb-2">止损收益率(%)</label>
          <input type="number" value={config.stop_loss_pct} onChange={(e) => onChange({ ...config, stop_loss_pct: Number(e.target.value) })} className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`} />
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-500 mb-2">最大并发仓位</label>
          <input type="number" value={config.max_concurrent_positions} onChange={(e) => onChange({ ...config, max_concurrent_positions: Number(e.target.value) })} className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`} />
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-500 mb-2">单币最多连开</label>
          <input type="number" value={config.max_consecutive_entries_per_symbol} onChange={(e) => onChange({ ...config, max_consecutive_entries_per_symbol: Number(e.target.value) })} className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`} />
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-500 mb-2">单币冷却分钟</label>
          <input type="number" value={config.symbol_reentry_cooldown_minutes} onChange={(e) => onChange({ ...config, symbol_reentry_cooldown_minutes: Number(e.target.value) })} className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`} />
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-500 mb-2">每日最多交易</label>
          <input type="number" value={config.max_trades_per_day} onChange={(e) => onChange({ ...config, max_trades_per_day: Number(e.target.value) })} className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`} />
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-500 mb-2">预热 K 线</label>
          <input type="number" value={config.warmup_bars} onChange={(e) => onChange({ ...config, warmup_bars: Number(e.target.value) })} className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`} />
        </div>
      </div>

      <div>
        <label className="block text-sm font-medium text-gray-500 mb-2">AI 自动优化</label>
        <div className={`rounded-2xl border p-4 space-y-4 ${isDarkMode ? 'border-gray-800 bg-gray-950/30' : 'border-gray-200 bg-gray-50'}`}>
          <div className="flex items-center justify-between gap-4">
            <div>
              <div className="text-sm font-medium text-gray-400">每 3 小时自动分析并更新策略脚本</div>
              <div className="text-xs text-gray-500 mt-1">系统会自动回看最近 3 小时仓位与行情数据，用选定模型生成新版本策略并自动替换执行脚本。</div>
            </div>
            <button
              type="button"
              onClick={() => onChange({ ...config, auto_optimize_enabled: !config.auto_optimize_enabled })}
              className={`px-3 py-1.5 rounded-lg text-xs font-bold border ${isDarkMode ? 'bg-gray-900 border-gray-800 text-gray-200 hover:bg-gray-800' : 'bg-white border-gray-200 text-gray-700 hover:bg-gray-100'}`}
            >
              {config.auto_optimize_enabled ? '已开启' : '未开启'}
            </button>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">优化模型</label>
              <select
                value={config.auto_optimize_model}
                onChange={(e) => onChange({ ...config, auto_optimize_model: e.target.value })}
                className={`w-full px-4 py-2.5 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
              >
                {optimizeModelOptions.map((item) => (
                  <option key={item.value || 'default'} value={item.value}>{item.label}</option>
                ))}
              </select>
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">OpenRouter API Key</label>
              <input
                type="password"
                value={config.auto_optimize_api_key}
                onChange={(e) => onChange({ ...config, auto_optimize_api_key: e.target.value })}
                placeholder="填写 sk-or-v1-...，会随策略配置写入数据库"
                className={`w-full px-4 py-2.5 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
              />
            </div>
          </div>
          <div className={`rounded-xl border px-4 py-3 text-xs space-y-1 ${isDarkMode ? 'border-gray-800 bg-gray-900/50 text-gray-400' : 'border-gray-200 bg-white text-gray-600'}`}>
            <div><span className="font-semibold">模型说明：</span>{selectedModelInfo.desc}</div>
            <div><span className="font-semibold">调用方式：</span>后端固定按 OpenRouter `POST /api/v1/chat/completions`，并附带 `Authorization: Bearer ...`、`HTTP-Referer`、`X-Title` 请求头。</div>
            <div>提示：现在不再需要选择 provider 或 API URL；只保留模型和 key，两者写入策略配置后会直接用于自动优化。</div>
          </div>
        </div>
      </div>

      <div>
        <label className="block text-sm font-medium text-gray-500 mb-2">非自然开仓</label>
        <div className={`rounded-2xl border p-4 space-y-3 ${isDarkMode ? 'border-gray-800 bg-gray-950/30' : 'border-gray-200 bg-gray-50'}`}>
          <div className="flex items-center justify-between gap-4">
            <div>
              <div className="text-sm font-medium text-gray-400">按仓位序号强制开仓方向</div>
              <div className="text-xs text-gray-500 mt-1">开启后，第 1 到第 N 个仓位会优先按你配置的方向开仓，例如 `多,多,多,多,空` 或 `多多多多低`。</div>
            </div>
            <button
              type="button"
              onClick={() => onChange({ ...config, non_natural_entry_enabled: !config.non_natural_entry_enabled })}
              className={`px-3 py-1.5 rounded-lg text-xs font-bold border ${isDarkMode ? 'bg-gray-900 border-gray-800 text-gray-200 hover:bg-gray-800' : 'bg-white border-gray-200 text-gray-700 hover:bg-gray-100'}`}
            >
              {config.non_natural_entry_enabled ? '已开启' : '未开启'}
            </button>
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">方向序列</label>
            <input
              type="text"
              value={config.non_natural_entry_sequence}
              onChange={(e) => onChange({ ...config, non_natural_entry_sequence: e.target.value })}
              placeholder="多,多,多,多,空"
              className={`w-full px-4 py-2.5 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
            />
            <div className="mt-2 text-xs text-gray-500">
              支持 `多/空`、`buy/sell`、`long/short`，也兼容你现在这种连续写法；超出配置长度的仓位会继续按策略自然方向执行。
            </div>
          </div>
        </div>
      </div>

      <div>
        <label className="block text-sm font-medium text-gray-500 mb-2">有效时间范围</label>
        <div className={`rounded-2xl border p-4 space-y-3 ${isDarkMode ? 'border-gray-800 bg-gray-950/30' : 'border-gray-200 bg-gray-50'}`}>
          <div className="text-xs text-gray-500">
            仅在这个时间范围内允许开仓，其他时间继续接收行情但不会下单；留空表示全天有效。支持多个时间段和跨天时间。
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">时间段</label>
            <input
              type="text"
              value={config.entry_time_windows}
              onChange={(e) => onChange({ ...config, entry_time_windows: e.target.value })}
              placeholder="09:00-11:30,13:00-18:00 或 22:00-02:00"
              className={`w-full px-4 py-2.5 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
            />
            <div className="mt-2 text-xs text-gray-500">
              使用服务端本地时间判断，格式为 `HH:mm-HH:mm`，多个时间段用逗号分隔。
            </div>
          </div>
        </div>
      </div>

      <div>
        <label className="block text-sm font-medium text-gray-500 mb-2">信号过滤（Go backend）</label>
        <div className={`rounded-2xl border p-4 space-y-3 ${isDarkMode ? 'border-gray-800 bg-gray-950/30' : 'border-gray-200 bg-gray-50'}`}>
          <div className="text-xs text-gray-500">
            这些是 backend 在 Python 发信号之后的二次过滤，独立于策略代码本身。
          </div>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">允许方向</label>
              <select
                value={config.allowed_sides}
                onChange={(e) => onChange({ ...config, allowed_sides: e.target.value as 'both' | 'buy' | 'sell' })}
                className={`w-full px-3 py-2 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
              >
                <option value="both">双向（多空都开）</option>
                <option value="sell">仅做空（short-only）</option>
                <option value="buy">仅做多（long-only）</option>
              </select>
              <div className="mt-1 text-xs text-gray-500">基于历史数据，meme 币 short 胜率更高</div>
            </div>
            <div className="md:col-span-2">
              <label className="block text-xs font-medium text-gray-500 mb-1">Symbol 黑名单（逗号分隔）</label>
              <input
                type="text"
                value={config.symbol_blacklist}
                onChange={(e) => onChange({ ...config, symbol_blacklist: e.target.value })}
                placeholder="SAHARAUSDT,WIFUSDT,AIOUSDT,..."
                className={`w-full px-3 py-2 rounded-xl border text-sm font-mono transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
              />
              <div className="mt-1 text-xs text-gray-500">即使 symbols 白名单允许，黑名单里的也绝对不开仓</div>
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">交易所托管 TP/SL</label>
              <button
                type="button"
                onClick={() => onChange({ ...config, use_exchange_tpsl: !config.use_exchange_tpsl })}
                className={`w-full px-3 py-2 rounded-xl text-sm font-bold border transition ${
                  config.use_exchange_tpsl
                    ? (isDarkMode ? 'bg-green-900/30 border-green-700 text-green-200' : 'bg-green-50 border-green-300 text-green-800')
                    : (isDarkMode ? 'bg-gray-900 border-gray-800 text-gray-400' : 'bg-white border-gray-200 text-gray-600')
                }`}
              >
                {config.use_exchange_tpsl ? '✓ 已开启' : '关闭'}
              </button>
              <div className="mt-1 text-xs text-gray-500">推荐开启：TP/SL 挂到 binance 服务器，不依赖你 backend 在线</div>
            </div>
          </div>
        </div>
      </div>

      <div>
        <label className="block text-sm font-medium text-gray-500 mb-2">策略阈值（meme 引擎内部参数）</label>
        <div className={`rounded-2xl border p-4 space-y-3 ${isDarkMode ? 'border-gray-800 bg-gray-950/30' : 'border-gray-200 bg-gray-50'}`}>
          <div className="text-xs text-gray-500">
            这些是 Python 信号生成的核心阈值。不确定就保持默认。
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">最低置信度 min_confidence</label>
              <input
                type="number"
                step="0.01"
                min="0"
                max="1"
                value={config.min_confidence}
                onChange={(e) => onChange({ ...config, min_confidence: Number(e.target.value) || 0 })}
                className={`w-full px-4 py-2.5 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
              />
              <div className="mt-1 text-xs text-gray-500">默认 0.35。降到 0.30 = 信号更频繁但质量低；升到 0.45 = 极少但更稳。</div>
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">量比阈值 volume_ratio_min</label>
              <input
                type="number"
                step="0.1"
                min="0"
                value={config.volume_ratio_min}
                onChange={(e) => onChange({ ...config, volume_ratio_min: Number(e.target.value) || 0 })}
                className={`w-full px-4 py-2.5 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
              />
              <div className="mt-1 text-xs text-gray-500">默认 1.2。当前量需 ≥ 均量 × 此值才算放量。降低 = 量不放也能开仓。</div>
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">ATR 止盈倍数 atr_tp_mult</label>
              <input
                type="number"
                step="0.1"
                min="0"
                value={config.atr_tp_mult}
                onChange={(e) => onChange({ ...config, atr_tp_mult: Number(e.target.value) || 0 })}
                className={`w-full px-4 py-2.5 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
              />
              <div className="mt-1 text-xs text-gray-500">默认 2.0。TP = 入场价 ± ATR × 此倍数。</div>
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">ATR 止损倍数 atr_sl_mult</label>
              <input
                type="number"
                step="0.1"
                min="0"
                value={config.atr_sl_mult}
                onChange={(e) => onChange({ ...config, atr_sl_mult: Number(e.target.value) || 0 })}
                className={`w-full px-4 py-2.5 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
              />
              <div className="mt-1 text-xs text-gray-500">默认 1.0。SL = 入场价 ∓ ATR × 此倍数。TP/SL 比 = atr_tp_mult / atr_sl_mult。</div>
            </div>
          </div>
        </div>
      </div>

      <div>
        <label className="block text-sm font-medium text-gray-500 mb-2">日志详细程度</label>
        <div className={`rounded-2xl border p-4 ${isDarkMode ? 'border-gray-800 bg-gray-950/30' : 'border-gray-200 bg-gray-50'}`}>
          <select
            value={config.log_level}
            onChange={(e) => onChange({ ...config, log_level: e.target.value as 'quiet' | 'normal' | 'verbose' | 'debug' })}
            className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
          >
            <option value="quiet">Quiet — 只打错误，几乎不发决策日志</option>
            <option value="normal">Normal — 默认，每小时几条（log_every=60）</option>
            <option value="verbose">Verbose — 每 10 分钟一条评估日志</option>
            <option value="debug">Debug — 每根 K 线都打全部细节（排查问题用）</option>
          </select>
          <div className="mt-2 text-xs text-gray-500">
            "Debug" 等同于一次性开 <code>debug + log_every=1 + log_decisions + log_signal + log_rx</code>。
            生产长跑选 Normal 即可；排查"为什么不开仓"时切到 Debug。
          </div>
        </div>
      </div>

      {showTemplateSelector && (
        <div>
          <label className="block text-sm font-medium text-gray-500 mb-2">选择模板</label>
          <select
            value={selectedTemplate}
            onChange={(e) => onSelectedTemplateChange?.(Number(e.target.value))}
            className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
          >
            <option value={0}>请选择一个模板</option>
            {templates.filter(t => t.is_enabled).map(t => <option key={t.id} value={t.id}>{t.name || `未命名模板#${t.id}`}</option>)}
          </select>
          {selectedTemplate !== 0 && selectedTemplateInfo && (
            <div className={`mt-4 p-4 rounded-xl border text-sm ${isDarkMode ? 'bg-blue-900/20 border-blue-800 text-blue-200' : 'bg-blue-50 border-blue-100 text-blue-700'}`}>
              <p className="font-bold mb-1">策略说明:</p>
              <p className="leading-relaxed mb-2">{selectedTemplateInfo.description || '暂无详细说明'}</p>
              {strategyTemplateUsages.some(s => s.template_id === selectedTemplate) && (
                <p className="text-xs text-orange-400 font-bold border-t border-blue-800/30 pt-2 flex items-center gap-1">
                  <Info size={12} /> 该模板已有一个运行中的实例，请确保使用不同的名称。
                </p>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
