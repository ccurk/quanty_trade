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
              <label className="block text-xs font-medium text-gray-500 mb-1">最大价格</label>
              <input
                type="number"
                value={config.max_price}
                onChange={(e) => onChange({ ...config, max_price: Number(e.target.value) })}
                className={`w-full px-3 py-2 rounded-xl border text-sm transition outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
              />
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
