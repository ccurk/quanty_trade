import { useEffect, useState } from 'react';
import axios from 'axios';
import { ArrowRight, AlertTriangle } from 'lucide-react';

// 再平衡状态:实时余额 + 失衡搬运建议 + 执行(semi/auto)+ 转账审计。
// 执行时后端用最新余额+白名单重新计算,不信前端传的地址/金额;recommend/dryRun 不会真提。

interface Balance { exchange: string; asset: string; free: number; locked: number; }
interface Plan { asset: string; from_exchange: string; to_exchange: string; amount: number; network: string; to_address: string; reason: string; }
interface Snapshot { balances: Balance[]; plans: Plan[]; last_update: string; error: string; running: boolean; }
interface StatusResp { enabled: boolean; message?: string; snapshot?: Snapshot; }
interface Transfer { id: number; asset: string; from_exchange: string; to_exchange: string; amount: number; amount_usd: number; status: string; tx_id: string; detail: string; created_at: string; }

const fmt = (v: number) => v.toLocaleString(undefined, { maximumFractionDigits: 6 });

export default function RebalanceStatus({ isDarkMode }: { isDarkMode: boolean }) {
  const [data, setData] = useState<StatusResp | null>(null);
  const [transfers, setTransfers] = useState<Transfer[]>([]);
  const [busy, setBusy] = useState('');
  const [msg, setMsg] = useState('');

  const load = async () => {
    try { const r = await axios.get('/api/rebalance/status'); setData(r.data); } catch { /* ignore */ }
    try { const t = await axios.get('/api/rebalance/transfers'); setTransfers(t.data.items || []); } catch { /* ignore */ }
  };
  useEffect(() => { load(); const t = setInterval(load, 10000); return () => clearInterval(t); }, []);

  const exec = async (p: Plan) => {
    if (!window.confirm(`确认执行搬运?\n${p.asset}  ${fmt(p.amount)}  ${p.from_exchange} → ${p.to_exchange}\n(recommend / dryRun 模式不会真提现)`)) return;
    setBusy(p.asset); setMsg('');
    try {
      const r = await axios.post('/api/rebalance/execute', { asset: p.asset });
      const res = r.data.result;
      if (res.executed) setMsg(`✅ 已提交: ${fmt(res.amount)} ${p.asset}, tx=${res.tx_id}`);
      else if (res.dry_run) setMsg(`🧪 dry-run: 将搬 ${fmt(res.amount)} ${p.asset}(未真提)`);
      else setMsg(`⏭️ 未执行: ${res.skipped || res.error}`);
      await load();
    } catch (e: any) {
      setMsg('❌ ' + (e?.response?.data?.error || '执行失败(需管理员权限)'));
    } finally { setBusy(''); }
  };

  const card = isDarkMode ? 'bg-gray-900 border-gray-800' : 'bg-white border-gray-200';
  if (!data) return null;

  if (!data.enabled) {
    return (
      <div className={`p-5 rounded-2xl border ${card}`}>
        <div className="font-bold mb-1">再平衡监控</div>
        <div className="text-sm text-gray-500">{data.message || '未启动'}</div>
      </div>
    );
  }

  const snap = data.snapshot!;
  const balances = [...(snap.balances || [])].sort((a, b) => (a.exchange + a.asset).localeCompare(b.exchange + b.asset));
  const statusColor = (s: string) => s === 'submitted' ? 'text-green-500' : s === 'failed' ? 'text-red-500' : s === 'dryrun' ? 'text-blue-400' : 'text-gray-400';

  return (
    <>
      {/* 实时余额 */}
      <div className={`p-5 rounded-2xl border shadow-xl ${card}`}>
        <div className="flex items-center justify-between mb-3">
          <div className="font-bold">实时余额</div>
          <div className="text-xs text-gray-500">更新: {snap.last_update ? new Date(snap.last_update).toLocaleTimeString() : '--'}</div>
        </div>
        {snap.error && <div className="text-red-500 text-xs mb-2">拉取错误: {snap.error}</div>}
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className={isDarkMode ? 'text-gray-400' : 'text-gray-500'}>
                <th className="text-left py-2 pr-3">交易所</th><th className="text-left pr-3">币</th>
                <th className="text-right pr-3">可用</th><th className="text-right pr-3">冻结</th><th className="text-right">合计</th>
              </tr>
            </thead>
            <tbody>
              {balances.map((b, i) => (
                <tr key={i} className={`border-t ${isDarkMode ? 'border-gray-800' : 'border-gray-100'}`}>
                  <td className="py-2 pr-3 font-medium">{b.exchange}</td>
                  <td className="pr-3">{b.asset}</td>
                  <td className="pr-3 text-right font-mono">{fmt(b.free)}</td>
                  <td className="pr-3 text-right font-mono text-gray-500">{fmt(b.locked)}</td>
                  <td className="text-right font-mono font-semibold">{fmt(b.free + b.locked)}</td>
                </tr>
              ))}
              {balances.length === 0 && <tr><td colSpan={5} className="py-6 text-center text-gray-500">暂无余额数据(检查 key 权限 / 入金)</td></tr>}
            </tbody>
          </table>
        </div>
      </div>

      {/* 搬运建议 + 执行 */}
      <div className={`p-5 rounded-2xl border shadow-xl ${card}`}>
        <div className="font-bold mb-3">搬运建议({snap.plans?.length || 0})<span className="text-xs font-normal text-gray-500 ml-2">执行时后端重算,recommend/dryRun 不真提</span></div>
        {msg && <div className="text-sm mb-3">{msg}</div>}
        {(!snap.plans || snap.plans.length === 0) ? (
          <div className="text-sm text-gray-500">当前库存都在带内,无需搬运。</div>
        ) : (
          <div className="space-y-2">
            {snap.plans.map((p, i) => {
              const executable = !!p.to_address;
              return (
                <div key={i} className={`p-3 rounded-xl border ${isDarkMode ? 'border-gray-800 bg-gray-950/40' : 'border-gray-100 bg-gray-50'}`}>
                  <div className="flex items-center flex-wrap gap-2 text-sm">
                    <span className="font-bold">{p.asset}</span>
                    <span className="flex items-center gap-1 text-gray-500">{p.from_exchange} <ArrowRight size={14} /> {p.to_exchange}</span>
                    <span className="font-mono font-semibold">{fmt(p.amount)} {p.asset}</span>
                    <span className="text-xs text-gray-500">链 {p.network}</span>
                    {executable
                      ? <span className="px-2 py-0.5 rounded text-xs font-semibold bg-green-500/20 text-green-500">可执行</span>
                      : <span className="px-2 py-0.5 rounded text-xs font-semibold bg-red-500/20 text-red-500 flex items-center gap-1"><AlertTriangle size={12} /> 无法执行</span>}
                    {executable && (
                      <button onClick={() => exec(p)} disabled={busy === p.asset}
                        className="ml-auto px-3 py-1 rounded-lg bg-amber-600 hover:bg-amber-700 text-white text-xs font-semibold disabled:opacity-50">
                        {busy === p.asset ? '执行中…' : '执行搬运'}
                      </button>
                    )}
                  </div>
                  <div className="text-xs text-gray-500 mt-1">{p.reason}</div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* 转账审计 */}
      <div className={`p-5 rounded-2xl border shadow-xl ${card}`}>
        <div className="font-bold mb-3">转账记录({transfers.length})</div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className={isDarkMode ? 'text-gray-400' : 'text-gray-500'}>
                <th className="text-left py-2 pr-3">时间</th><th className="text-left pr-3">币</th><th className="text-left pr-3">方向</th>
                <th className="text-right pr-3">金额</th><th className="text-left pr-3">状态</th><th className="text-left">详情/Tx</th>
              </tr>
            </thead>
            <tbody>
              {transfers.map((t) => (
                <tr key={t.id} className={`border-t ${isDarkMode ? 'border-gray-800' : 'border-gray-100'}`}>
                  <td className="py-2 pr-3 text-gray-500 whitespace-nowrap">{new Date(t.created_at).toLocaleString()}</td>
                  <td className="pr-3">{t.asset}</td>
                  <td className="pr-3 text-gray-500">{t.from_exchange}→{t.to_exchange}</td>
                  <td className="pr-3 text-right font-mono">{fmt(t.amount)}</td>
                  <td className={`pr-3 font-semibold ${statusColor(t.status)}`}>{t.status}</td>
                  <td className="text-xs text-gray-500 break-all max-w-[240px]">{t.tx_id || t.detail}</td>
                </tr>
              ))}
              {transfers.length === 0 && <tr><td colSpan={6} className="py-6 text-center text-gray-500">暂无转账记录</td></tr>}
            </tbody>
          </table>
        </div>
      </div>
    </>
  );
}
