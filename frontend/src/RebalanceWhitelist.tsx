import { useEffect, useState } from 'react';
import axios from 'axios';
import { PlusCircle, Trash2, AlertCircle } from 'lucide-react';

// 跨所再平衡提现白名单管理界面。这是 App 侧(第二道)守卫 —— 真正的硬保证是
// 交易所自身"仅允许提现到白名单地址"设置。这里只让运营方增删/启停允许的目标地址。

interface WhitelistItem {
  id: number;
  exchange: string;
  asset: string;
  network: string;
  address: string;
  memo: string;
  label: string;
  enabled: boolean;
  created_at: string;
}

const EMPTY = { exchange: '', asset: '', network: '', address: '', memo: '', label: '' };

export default function RebalanceWhitelist({ isDarkMode }: { isDarkMode: boolean }) {
  const [items, setItems] = useState<WhitelistItem[]>([]);
  const [form, setForm] = useState({ ...EMPTY });
  const [err, setErr] = useState('');
  const [loading, setLoading] = useState(false);

  const load = async () => {
    try {
      const r = await axios.get('/api/rebalance/whitelist');
      setItems(r.data.items || []);
    } catch (e: any) {
      setErr(e?.response?.data?.error || '加载失败');
    }
  };
  useEffect(() => { load(); }, []);

  const add = async () => {
    setErr('');
    if (!form.exchange || !form.asset || !form.network || !form.address) {
      setErr('目标所 / 币种 / 链 / 地址 均必填');
      return;
    }
    setLoading(true);
    try {
      await axios.post('/api/rebalance/whitelist', form);
      setForm({ ...EMPTY });
      await load();
    } catch (e: any) {
      setErr(e?.response?.data?.error || '创建失败(写操作需要管理员权限)');
    } finally {
      setLoading(false);
    }
  };

  const toggle = async (it: WhitelistItem) => {
    try {
      await axios.patch(`/api/rebalance/whitelist/${it.id}`, { enabled: !it.enabled });
      await load();
    } catch (e: any) {
      setErr(e?.response?.data?.error || '更新失败(需要管理员权限)');
    }
  };

  const del = async (it: WhitelistItem) => {
    if (!window.confirm(`删除白名单地址 ${it.exchange} / ${it.asset} / ${it.network}?`)) return;
    try {
      await axios.delete(`/api/rebalance/whitelist/${it.id}`);
      await load();
    } catch (e: any) {
      setErr(e?.response?.data?.error || '删除失败(需要管理员权限)');
    }
  };

  const card = isDarkMode ? 'bg-gray-900 border-gray-800' : 'bg-white border-gray-200';
  const input = `px-3 py-2 rounded-lg border text-sm outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-gray-100' : 'bg-white border-gray-300 text-gray-900'}`;

  return (
    <div className="space-y-4">
      {/* 安全提示 —— 必须让人看到:交易所侧白名单才是硬保证 */}
      <div className={`p-4 rounded-xl border flex gap-3 ${isDarkMode ? 'bg-amber-950/40 border-amber-800 text-amber-200' : 'bg-amber-50 border-amber-200 text-amber-800'}`}>
        <AlertCircle size={18} className="shrink-0 mt-0.5" />
        <div className="text-sm leading-relaxed">
          <b>这是第二道守卫。</b> 真正保证钱不丢的是<b>交易所侧</b>的"仅允许提现到白名单地址"设置(受交易所登录 + 2FA 保护)。
          这里的地址只是 App 侧再校验一遍 —— 请<b>务必先在 gate / binance 各自后台</b>把对方的充值地址加进白名单。
          <b className="text-red-500"> 链(network)填错会永久丢钱</b>,USDT 建议 TRC20 且两端一致。
        </div>
      </div>

      {/* 新增表单 */}
      <div className={`p-5 rounded-2xl border shadow-xl ${card}`}>
        <div className="font-bold mb-3">新增提现白名单地址</div>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
          <select value={form.exchange} onChange={e => setForm({ ...form, exchange: e.target.value })} className={input}>
            <option value="">目标所…</option>
            <option value="gate">gate</option>
            <option value="binance">binance</option>
            <option value="kucoin">kucoin</option>
            <option value="coinsph">coinsph</option>
          </select>
          <input placeholder="币种 如 USDT" value={form.asset} onChange={e => setForm({ ...form, asset: e.target.value })} className={input} />
          <input placeholder="链 如 TRC20(错链丢钱)" value={form.network} onChange={e => setForm({ ...form, network: e.target.value })} className={input} />
          <input placeholder="目标所的充值地址" value={form.address} onChange={e => setForm({ ...form, address: e.target.value })} className={`${input} md:col-span-2`} />
          <input placeholder="Memo/Tag(需要的链才填)" value={form.memo} onChange={e => setForm({ ...form, memo: e.target.value })} className={input} />
          <input placeholder="备注(可选)" value={form.label} onChange={e => setForm({ ...form, label: e.target.value })} className={`${input} md:col-span-2`} />
          <button onClick={add} disabled={loading} className="flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-700 text-white text-sm font-semibold disabled:opacity-50">
            <PlusCircle size={16} /> 添加
          </button>
        </div>
        {err && <div className="text-red-500 text-sm mt-3">{err}</div>}
      </div>

      {/* 列表 */}
      <div className={`p-5 rounded-2xl border shadow-xl ${card}`}>
        <div className="font-bold mb-3">白名单地址({items.length})</div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className={isDarkMode ? 'text-gray-400' : 'text-gray-500'}>
                <th className="text-left py-2 pr-3">目标所</th>
                <th className="text-left pr-3">币</th>
                <th className="text-left pr-3">链</th>
                <th className="text-left pr-3">地址</th>
                <th className="text-left pr-3">备注</th>
                <th className="text-left pr-3">状态</th>
                <th className="text-right">操作</th>
              </tr>
            </thead>
            <tbody>
              {items.map(it => (
                <tr key={it.id} className={`border-t ${isDarkMode ? 'border-gray-800' : 'border-gray-100'}`}>
                  <td className="py-2 pr-3 font-medium">{it.exchange}</td>
                  <td className="pr-3">{it.asset}</td>
                  <td className="pr-3">{it.network}</td>
                  <td className="pr-3 font-mono text-xs break-all max-w-[240px]">
                    {it.address}{it.memo && <span className="text-gray-500"> · memo:{it.memo}</span>}
                  </td>
                  <td className="pr-3 text-gray-500">{it.label}</td>
                  <td className="pr-3">
                    <button onClick={() => toggle(it)} className={`px-2 py-0.5 rounded text-xs font-semibold ${it.enabled ? 'bg-green-500/20 text-green-500' : 'bg-gray-500/20 text-gray-400'}`}>
                      {it.enabled ? '启用' : '停用'}
                    </button>
                  </td>
                  <td className="text-right">
                    <button onClick={() => del(it)} className="text-red-500 hover:text-red-400" title="删除"><Trash2 size={16} /></button>
                  </td>
                </tr>
              ))}
              {items.length === 0 && (
                <tr><td colSpan={7} className="py-6 text-center text-gray-500">暂无白名单地址</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
