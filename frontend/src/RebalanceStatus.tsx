import { useEffect, useState } from 'react';
import axios from 'axios';
import { ArrowRight, AlertTriangle } from 'lucide-react';

// 再平衡只读状态:实时余额 + 失衡搬运建议。数据来自 GET /api/rebalance/status,
// 每 10s 刷新。这里只展示,不触发任何搬运。

interface Balance { exchange: string; asset: string; free: number; locked: number; }
interface Plan { asset: string; from_exchange: string; to_exchange: string; amount: number; network: string; to_address: string; reason: string; }
interface Snapshot { balances: Balance[]; plans: Plan[]; last_update: string; error: string; running: boolean; }
interface StatusResp { enabled: boolean; message?: string; snapshot?: Snapshot; }

const fmt = (v: number) => v.toLocaleString(undefined, { maximumFractionDigits: 6 });

export default function RebalanceStatus({ isDarkMode }: { isDarkMode: boolean }) {
  const [data, setData] = useState<StatusResp | null>(null);

  const load = async () => {
    try { const r = await axios.get('/api/rebalance/status'); setData(r.data); } catch { /* 忽略瞬时错误 */ }
  };
  useEffect(() => { load(); const t = setInterval(load, 10000); return () => clearInterval(t); }, []);

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
                <th className="text-left py-2 pr-3">交易所</th>
                <th className="text-left pr-3">币</th>
                <th className="text-right pr-3">可用</th>
                <th className="text-right pr-3">冻结</th>
                <th className="text-right">合计</th>
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
              {balances.length === 0 && <tr><td colSpan={5} className="py-6 text-center text-gray-500">暂无余额数据(检查 key 权限)</td></tr>}
            </tbody>
          </table>
        </div>
      </div>

      {/* 搬运建议 */}
      <div className={`p-5 rounded-2xl border shadow-xl ${card}`}>
        <div className="font-bold mb-3">搬运建议({snap.plans?.length || 0})<span className="text-xs font-normal text-gray-500 ml-2">仅建议,不自动执行</span></div>
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
                  </div>
                  <div className="text-xs text-gray-500 mt-1">{p.reason}</div>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </>
  );
}
