import React, { useState, useEffect, useRef } from 'react';
import axios from 'axios';
import { Play, Square, RefreshCw, Activity, Terminal, List, LayoutDashboard, ShoppingBag, Users, LogOut, ShieldCheck, Share2, PlusCircle } from 'lucide-react';
import Login from './Login';

interface User {
  id: number;
  username: string;
  role: 'admin' | 'user';
}

interface Strategy {
  id: string;
  name: string;
  status: 'running' | 'stopped' | 'error';
  config: any;
}

interface Template {
  id: number;
  name: string;
  description: string;
  path: string;
  author: { username: string };
}

interface Order {
  id: string;
  symbol: string;
  side: string;
  amount: number;
  price: number;
  status: string;
  timestamp: string;
}

const App: React.FC = () => {
  const [user, setUser] = useState<User | null>(null);
  const [token, setToken] = useState<string | null>(localStorage.getItem('token'));
  const [strategies, setStrategies] = useState<Strategy[]>([]);
  const [templates, setTemplates] = useState<Template[]>([]);
  const [orders, setOrders] = useState<Order[]>([]);
  const [logs, setLogs] = useState<string[]>([]);
  const [activeTab, setActiveTab] = useState<'strategies' | 'orders' | 'logs' | 'square' | 'admin'>('strategies');
  const ws = useRef<WebSocket | null>(null);

  useEffect(() => {
    const savedUser = localStorage.getItem('user');
    if (savedUser && token) {
      setUser(JSON.parse(savedUser));
      axios.defaults.headers.common['Authorization'] = `Bearer ${token}`;
    }
  }, [token]);

  useEffect(() => {
    if (user && token) {
      fetchStrategies();
      fetchTemplates();
      connectWS();
    }
    return () => {
      if (ws.current) ws.current.close();
    };
  }, [user, token]);

  const fetchStrategies = async () => {
    try {
      const res = await axios.get('/api/strategies');
      setStrategies(res.data);
    } catch (err) {
      console.error('Failed to fetch strategies', err);
    }
  };

  const fetchTemplates = async () => {
    try {
      const res = await axios.get('/api/templates');
      setTemplates(res.data);
    } catch (err) {
      console.error('Failed to fetch templates', err);
    }
  };

  const connectWS = () => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws.current = new WebSocket(`${protocol}//${window.location.host}/ws`);
    ws.current.onmessage = (event) => {
      const msg = JSON.parse(event.data);
      if (msg.type === 'order') {
        setOrders(prev => [msg.data, ...prev]);
      } else if (msg.type === 'log') {
        setLogs(prev => [`[${new Date().toLocaleTimeString()}] ${msg.data}`, ...prev.slice(0, 99)]);
      }
    };
    ws.current.onclose = () => {
      setTimeout(connectWS, 3000);
    };
  };

  const handleLogin = (newToken: string, newUser: User) => {
    setToken(newToken);
    setUser(newUser);
    localStorage.setItem('token', newToken);
    localStorage.setItem('user', JSON.stringify(newUser));
    axios.defaults.headers.common['Authorization'] = `Bearer ${newToken}`;
  };

  const handleLogout = () => {
    setToken(null);
    setUser(null);
    localStorage.removeItem('token');
    localStorage.removeItem('user');
    delete axios.defaults.headers.common['Authorization'];
  };

  const toggleStrategy = async (s: Strategy) => {
    try {
      if (s.status === 'running') {
        await axios.post(`/api/strategies/${s.id}/stop`);
      } else {
        await axios.post(`/api/strategies/${s.id}/start`);
      }
      fetchStrategies();
    } catch (err) {
      console.error('Failed to toggle strategy', err);
    }
  };

  const publishToSquare = async (s: Strategy) => {
    try {
      await axios.post('/api/templates/publish', {
        name: s.name,
        description: `由用户 ${user?.username} 发布`,
        path: `../strategies/simple_trend.py` // Hardcoded for demo, in reality should be part of strategy metadata
      });
      fetchTemplates();
      alert('发布成功！');
    } catch (err) {
      console.error('Failed to publish', err);
    }
  };

  const referenceFromSquare = async (t: Template) => {
    try {
      await axios.post('/api/templates/reference', {
        template_id: t.id,
        name: `${t.name} (来自广场)`,
        config: JSON.stringify({ symbol: 'BTC/USDT', window: 20 })
      });
      fetchStrategies();
      setActiveTab('strategies');
      alert('已成功引用到我的策略！');
    } catch (err) {
      console.error('Failed to reference', err);
    }
  };

  if (!token || !user) {
    return <Login onLogin={handleLogin} />;
  }

  return (
    <div className="min-h-screen bg-gray-950 text-gray-100 flex">
      {/* Sidebar */}
      <aside className="w-64 bg-gray-900 border-r border-gray-800 flex flex-col">
        <div className="p-6">
          <h1 className="text-2xl font-bold flex items-center gap-2 text-blue-500">
            <Activity size={28} /> QuantyTrade
          </h1>
        </div>

        <nav className="flex-1 px-4 space-y-2">
          <NavItem active={activeTab === 'strategies'} onClick={() => setActiveTab('strategies')} icon={<LayoutDashboard size={20} />} label="我的策略" />
          <NavItem active={activeTab === 'square'} onClick={() => setActiveTab('square')} icon={<ShoppingBag size={20} />} label="策略广场" />
          <NavItem active={activeTab === 'orders'} onClick={() => setActiveTab('orders')} icon={<List size={20} />} label="订单管理" />
          <NavItem active={activeTab === 'logs'} onClick={() => setActiveTab('logs')} icon={<Terminal size={20} />} label="系统日志" />
          {user.role === 'admin' && (
            <NavItem active={activeTab === 'admin'} onClick={() => setActiveTab('admin')} icon={<ShieldCheck size={20} />} label="系统管理" />
          )}
        </nav>

        <div className="p-4 border-t border-gray-800">
          <div className="flex items-center gap-3 mb-4 px-2">
            <div className="w-10 h-10 bg-blue-600 rounded-full flex items-center justify-center font-bold">
              {user.username[0].toUpperCase()}
            </div>
            <div>
              <p className="text-sm font-semibold">{user.username}</p>
              <p className="text-xs text-gray-500 capitalize">{user.role}</p>
            </div>
          </div>
          <button
            onClick={handleLogout}
            className="w-full flex items-center gap-2 px-4 py-2 text-red-400 hover:bg-red-900/20 rounded-lg transition"
          >
            <LogOut size={18} /> 退出登录
          </button>
        </div>
      </aside>

      {/* Main Content */}
      <main className="flex-1 p-8 overflow-y-auto">
        <header className="flex justify-between items-center mb-8">
          <h2 className="text-2xl font-bold">
            {activeTab === 'strategies' && '我的策略'}
            {activeTab === 'square' && '策略广场'}
            {activeTab === 'orders' && '订单记录'}
            {activeTab === 'logs' && '实时日志'}
            {activeTab === 'admin' && '用户管理'}
          </h2>
          <div className="flex gap-4">
            <button onClick={() => { fetchStrategies(); fetchTemplates(); }} className="p-2 bg-gray-800 hover:bg-gray-700 rounded-lg transition shadow-sm border border-gray-700">
              <RefreshCw size={20} />
            </button>
          </div>
        </header>

        {activeTab === 'strategies' && (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            {strategies.map(s => (
              <div key={s.id} className="bg-gray-900/50 p-6 rounded-2xl border border-gray-800 shadow-xl hover:border-gray-700 transition">
                <div className="flex justify-between items-start mb-4">
                  <h3 className="text-lg font-bold text-white">{s.name}</h3>
                  <span className={`px-3 py-1 rounded-full text-[10px] font-black uppercase tracking-wider ${s.status === 'running' ? 'bg-green-900/30 text-green-400' : 'bg-red-900/30 text-red-400'}`}>
                    {s.status}
                  </span>
                </div>
                <div className="text-sm text-gray-500 mb-8 space-y-1">
                  <p className="flex justify-between"><span>交易对</span> <span className="text-gray-300">{s.config.symbol}</span></p>
                  <p className="flex justify-between"><span>窗口期</span> <span className="text-gray-300">{s.config.window}</span></p>
                </div>
                <div className="flex gap-2">
                  <button
                    onClick={() => toggleStrategy(s)}
                    className={`flex-1 flex items-center justify-center gap-2 py-2.5 rounded-xl font-bold transition ${s.status === 'running' ? 'bg-red-600 hover:bg-red-700 shadow-red-900/20' : 'bg-green-600 hover:bg-green-700 shadow-green-900/20'} shadow-lg`}
                  >
                    {s.status === 'running' ? <><Square size={16} /> 禁用</> : <><Play size={16} /> 启用</>}
                  </button>
                  <button
                    onClick={() => publishToSquare(s)}
                    className="p-2.5 bg-gray-800 hover:bg-gray-700 rounded-xl transition border border-gray-700 text-blue-400"
                    title="发布到广场"
                  >
                    <Share2 size={20} />
                  </button>
                </div>
              </div>
            ))}
            <div className="bg-gray-900/30 p-6 rounded-2xl border-2 border-dashed border-gray-800 flex flex-col items-center justify-center text-gray-600 hover:border-gray-700 hover:text-gray-400 transition cursor-pointer group">
              <PlusCircle size={40} className="mb-2 group-hover:scale-110 transition" />
              <p className="font-bold">新建策略</p>
            </div>
          </div>
        )}

        {activeTab === 'square' && (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            {templates.map(t => (
              <div key={t.id} className="bg-gray-900/50 p-6 rounded-2xl border border-gray-800 shadow-xl hover:border-gray-700 transition">
                <div className="mb-4">
                  <h3 className="text-lg font-bold text-white">{t.name}</h3>
                  <p className="text-xs text-blue-400 font-medium">by @{t.author.username}</p>
                </div>
                <p className="text-sm text-gray-400 mb-8 h-10 overflow-hidden line-clamp-2">
                  {t.description || '该策略没有详细描述'}
                </p>
                <button
                  onClick={() => referenceFromSquare(t)}
                  className="w-full flex items-center justify-center gap-2 py-2.5 bg-blue-600 hover:bg-blue-700 text-white rounded-xl font-bold transition shadow-lg shadow-blue-900/20"
                >
                  <PlusCircle size={18} /> 引用此策略
                </button>
              </div>
            ))}
          </div>
        )}

        {activeTab === 'orders' && (
          <div className="bg-gray-900 rounded-2xl border border-gray-800 overflow-hidden shadow-2xl">
            <table className="w-full text-left">
              <thead className="bg-gray-800/50 text-gray-400 text-xs uppercase tracking-wider">
                <tr>
                  <th className="px-6 py-4">时间</th>
                  <th className="px-6 py-4">交易对</th>
                  <th className="px-6 py-4">方向</th>
                  <th className="px-6 py-4">数量</th>
                  <th className="px-6 py-4">成交价</th>
                  <th className="px-6 py-4">状态</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-800">
                {orders.map(o => (
                  <tr key={o.id} className="hover:bg-gray-800/30 transition">
                    <td className="px-6 py-4 text-xs text-gray-500 font-mono">{new Date(o.timestamp).toLocaleString()}</td>
                    <td className="px-6 py-4 font-bold">{o.symbol}</td>
                    <td className="px-6 py-4">
                      <span className={`font-black text-xs px-2 py-0.5 rounded ${o.side === 'buy' ? 'bg-green-900/30 text-green-500' : 'bg-red-900/30 text-red-500'}`}>
                        {o.side.toUpperCase()}
                      </span>
                    </td>
                    <td className="px-6 py-4 font-mono">{o.amount}</td>
                    <td className="px-6 py-4 font-mono text-blue-400">${o.price.toLocaleString(undefined, { minimumFractionDigits: 2 })}</td>
                    <td className="px-6 py-4">
                      <span className="px-2 py-1 bg-gray-800 text-gray-400 text-[10px] rounded font-black uppercase tracking-tighter">{o.status}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {activeTab === 'logs' && (
          <div className="bg-black/80 backdrop-blur-md p-6 rounded-2xl font-mono text-sm h-[600px] overflow-y-auto border border-gray-800 shadow-2xl custom-scrollbar">
            {logs.map((log, i) => (
              <div key={i} className="mb-2 flex gap-4">
                <span className="text-gray-600 shrink-0">[{i}]</span>
                <span className="text-gray-300 leading-relaxed">{log}</span>
              </div>
            ))}
          </div>
        )}

        {activeTab === 'admin' && (
          <div className="bg-gray-900 p-8 rounded-2xl border border-gray-800 shadow-xl">
            <h3 className="text-xl font-bold mb-6 flex items-center gap-2"><Users className="text-blue-500" /> 用户管理面板</h3>
            <p className="text-gray-400">在此处可以创建新的交易员账号并分配权限。</p>
            {/* Admin user management list would go here */}
          </div>
        )}
      </main>
    </div>
  );
};

interface NavItemProps {
  active: boolean;
  onClick: () => void;
  icon: React.ReactNode;
  label: string;
}

const NavItem: React.FC<NavItemProps> = ({ active, onClick, icon, label }) => (
  <button
    onClick={onClick}
    className={`w-full flex items-center gap-3 px-4 py-3 rounded-xl transition-all duration-200 ${active ? 'bg-blue-600 text-white shadow-lg shadow-blue-900/40 translate-x-1' : 'text-gray-400 hover:bg-gray-800 hover:text-gray-200'}`}
  >
    {icon}
    <span className="font-bold">{label}</span>
  </button>
);

export default App;
