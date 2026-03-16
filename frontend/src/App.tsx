import React, { useState, useEffect, useRef } from 'react';
import axios from 'axios';
import { Play, Square, RefreshCw, Activity, Terminal, List, LayoutDashboard, ShoppingBag, Users, LogOut, ShieldCheck, Share2, PlusCircle, Trash2 } from 'lucide-react';
import Login from './Login';
import Register from './Register';

interface User {
  id: number;
  username: string;
  role: 'admin' | 'user';
  configs?: string;
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
  author: { id: number, username: string };
}

interface Position {
  symbol: string;
  amount: number;
  price: number;
}

const App: React.FC = () => {
  const [user, setUser] = useState<User | null>(null);
  const [token, setToken] = useState<string | null>(localStorage.getItem('token'));
  const [isRegistering, setIsRegistering] = useState(false);
  const [strategies, setStrategies] = useState<Strategy[]>([]);
  const [templates, setTemplates] = useState<Template[]>([]);
  const [positions, setPositions] = useState<Position[]>([]);
  const [logs, setLogs] = useState<string[]>([]);
  const [activeTab, setActiveTab] = useState<'strategies' | 'positions' | 'logs' | 'square' | 'admin'>('strategies');
  const [isDarkMode, setIsDarkMode] = useState(true);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [showPublishConfirm, setShowPublishConfirm] = useState(false);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [showDeleteTemplateConfirm, setShowDeleteTemplateConfirm] = useState(false);
  const [strategyToPublish, setStrategyToPublish] = useState<Strategy | null>(null);
  const [strategyToDelete, setStrategyToDelete] = useState<Strategy | null>(null);
  const [templateToDelete, setTemplateToDelete] = useState<Template | null>(null);
  const [newStratName, setNewStratName] = useState('');
  const [selectedTemplate, setSelectedTemplate] = useState<number>(0);
  const ws = useRef<WebSocket | null>(null);

  useEffect(() => {
    if (isDarkMode) {
      document.documentElement.classList.add('dark');
    } else {
      document.documentElement.classList.remove('dark');
    }
  }, [isDarkMode]);

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
      fetchPositions();
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

  const fetchPositions = async () => {
    try {
      const res = await axios.get('/api/positions');
      setPositions(res.data);
    } catch (err) {
      console.error('Failed to fetch positions', err);
    }
  };

  const connectWS = () => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws.current = new WebSocket(`${protocol}//${window.location.host}/ws`);
    ws.current.onmessage = (event) => {
      const msg = JSON.parse(event.data);
      if (msg.type === 'order') {
        fetchPositions(); // Refresh positions on order
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

  const createStrategy = async () => {
    try {
      await axios.post('/api/strategies', {
        name: newStratName,
        template_id: selectedTemplate,
        config: JSON.stringify({ symbol: 'BTC/USDT', window: 20 })
      });
      fetchStrategies();
      setShowCreateModal(false);
      setNewStratName('');
    } catch (err) {
      console.error('Failed to create strategy', err);
    }
  };

  const deleteStrategy = async () => {
    if (!strategyToDelete) return;
    try {
      await axios.delete(`/api/strategies/${strategyToDelete.id}`);
      fetchStrategies();
      setShowDeleteConfirm(false);
      setStrategyToDelete(null);
    } catch (err) {
      console.error('Failed to delete strategy', err);
    }
  };

  const deleteTemplate = async () => {
    if (!templateToDelete) return;
    try {
      await axios.delete(`/api/templates/${templateToDelete.id}`);
      fetchTemplates();
      setShowDeleteTemplateConfirm(false);
      setTemplateToDelete(null);
    } catch (err) {
      console.error('Failed to delete template', err);
    }
  };

  const publishToSquare = async () => {
    if (!strategyToPublish) return;
    try {
      await axios.post('/api/templates/publish', {
        name: strategyToPublish.name,
        description: `由用户 ${user?.username} 发布的优质策略`,
        path: `../strategies/simple_trend.py`
      });
      fetchTemplates();
      setShowPublishConfirm(false);
      setStrategyToPublish(null);
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
    if (isRegistering) {
      return <Register onBackToLogin={() => setIsRegistering(false)} />;
    }
    return <Login onLogin={handleLogin} onGoToRegister={() => setIsRegistering(true)} />;
  }

  return (
    <div className={`min-h-screen flex ${isDarkMode ? 'bg-gray-950 text-gray-100' : 'bg-gray-50 text-gray-900'}`}>
      {/* Sidebar */}
      <aside className={`w-64 border-r flex flex-col ${isDarkMode ? 'bg-gray-900 border-gray-800' : 'bg-white border-gray-200'}`}>
        <div className="p-6">
          <h1 className="text-2xl font-bold flex items-center gap-2 text-blue-500">
            <Activity size={28} /> QuantyTrade
          </h1>
        </div>

        <nav className="flex-1 px-4 space-y-2">
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'strategies'} onClick={() => setActiveTab('strategies')} icon={<LayoutDashboard size={20} />} label="我的策略" />
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'square'} onClick={() => setActiveTab('square')} icon={<ShoppingBag size={20} />} label="策略广场" />
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'positions'} onClick={() => setActiveTab('positions')} icon={<List size={20} />} label="仓位管理" />
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'logs'} onClick={() => setActiveTab('logs')} icon={<Terminal size={20} />} label="系统日志" />
          {user.role === 'admin' && (
            <NavItem isDarkMode={isDarkMode} active={activeTab === 'admin'} onClick={() => setActiveTab('admin')} icon={<ShieldCheck size={20} />} label="系统管理" />
          )}
        </nav>

        <div className={`p-4 border-t ${isDarkMode ? 'border-gray-800' : 'border-gray-200'}`}>
          <div className="flex items-center gap-3 mb-4 px-2">
            <div className="w-10 h-10 bg-blue-600 rounded-full flex items-center justify-center font-bold text-white">
              {user.username[0].toUpperCase()}
            </div>
            <div>
              <p className="text-sm font-semibold">{user.username}</p>
              <p className="text-xs text-gray-500 capitalize">{user.role}</p>
            </div>
          </div>
          <button
            onClick={() => setIsDarkMode(!isDarkMode)}
            className={`w-full flex items-center gap-2 px-4 py-2 mb-2 rounded-lg transition ${isDarkMode ? 'hover:bg-gray-800 text-gray-300' : 'hover:bg-gray-100 text-gray-600'}`}
          >
            {isDarkMode ? '🌞 明亮模式' : '🌙 黑暗模式'}
          </button>
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
            {activeTab === 'positions' && '实时持仓'}
            {activeTab === 'logs' && '实时日志'}
            {activeTab === 'admin' && '用户管理'}
          </h2>
          <div className="flex gap-4">
            <button onClick={() => { fetchStrategies(); fetchTemplates(); fetchPositions(); }} className={`p-2 rounded-lg transition shadow-sm border ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 border-gray-700' : 'bg-white hover:bg-gray-50 border-gray-200'}`}>
              <RefreshCw size={20} />
            </button>
          </div>
        </header>

        {activeTab === 'strategies' && (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            {strategies.map(s => (
              <div key={s.id} className={`p-6 rounded-2xl border shadow-xl transition ${isDarkMode ? 'bg-gray-900/50 border-gray-800 hover:border-gray-700' : 'bg-white border-gray-200 hover:border-blue-200'}`}>
                <div className="flex justify-between items-start mb-4">
                  <h3 className="text-lg font-bold">{s.name}</h3>
                  <span className={`px-3 py-1 rounded-full text-[10px] font-black uppercase tracking-wider ${s.status === 'running' ? 'bg-green-900/30 text-green-400' : 'bg-red-900/30 text-red-400'}`}>
                    {s.status}
                  </span>
                </div>
                <div className="text-sm text-gray-500 mb-8 space-y-1">
                  <p className="flex justify-between"><span>交易对</span> <span className={isDarkMode ? 'text-gray-300' : 'text-gray-700'}>{s.config.symbol}</span></p>
                  <p className="flex justify-between"><span>窗口期</span> <span className={isDarkMode ? 'text-gray-300' : 'text-gray-700'}>{s.config.window}</span></p>
                </div>
                <div className="flex gap-2">
                  <button
                    onClick={() => toggleStrategy(s)}
                    className={`flex-1 flex items-center justify-center gap-2 py-2.5 rounded-xl font-bold transition shadow-lg ${s.status === 'running' ? 'bg-red-600 hover:bg-red-700 shadow-red-900/20 text-white' : 'bg-green-600 hover:bg-green-700 shadow-green-900/20 text-white'}`}
                  >
                    {s.status === 'running' ? <><Square size={16} /> 禁用</> : <><Play size={16} /> 启用</>}
                  </button>
                  <button
                    onClick={() => { setStrategyToPublish(s); setShowPublishConfirm(true); }}
                    className={`p-2.5 rounded-xl transition border text-blue-400 ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 border-gray-700' : 'bg-white hover:bg-gray-50 border-gray-200'}`}
                    title="发布到广场"
                  >
                    <Share2 size={20} />
                  </button>
                  <button
                    onClick={() => { setStrategyToDelete(s); setShowDeleteConfirm(true); }}
                    className={`p-2.5 rounded-xl transition border text-red-400 ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 border-gray-700' : 'bg-white hover:bg-gray-50 border-gray-200'}`}
                    title="删除策略"
                  >
                    <Trash2 size={20} />
                  </button>
                </div>

              </div>
            ))}
            <div 
              onClick={() => setShowCreateModal(true)}
              className={`p-6 rounded-2xl border-2 border-dashed flex flex-col items-center justify-center transition cursor-pointer group ${isDarkMode ? 'bg-gray-900/30 border-gray-800 text-gray-600 hover:border-gray-700 hover:text-gray-400' : 'bg-white border-gray-200 text-gray-400 hover:border-blue-200 hover:text-blue-400'}`}
            >
              <PlusCircle size={40} className="mb-2 group-hover:scale-110 transition" />
              <p className="font-bold">新建策略</p>
            </div>
          </div>
        )}

        {activeTab === 'square' && (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            {templates.map(t => (
              <div key={t.id} className={`p-6 rounded-2xl border shadow-xl transition group relative ${isDarkMode ? 'bg-gray-900/50 border-gray-800 hover:border-gray-700' : 'bg-white border-gray-200 hover:border-blue-200'}`}>
                <div className="mb-4">
                  <div className="flex justify-between items-start">
                    <div>
                      <h3 className="text-lg font-bold">{t.name}</h3>
                      <p className="text-xs text-blue-500 font-medium">by @{t.author?.username}</p>
                    </div>
                    {(user.role === 'admin' || t.author?.id === user.id) && (
                      <button 
                        onClick={() => { setTemplateToDelete(t); setShowDeleteTemplateConfirm(true); }}
                        className="text-red-500 hover:text-red-600 transition p-1"
                        title="下架策略"
                      >
                        <Trash2 size={18} />
                      </button>
                    )}
                  </div>
                </div>

                <div className="relative mb-8 h-10 group">
                  <p className="text-sm text-gray-500 line-clamp-2 cursor-help">
                    {t.description || '该策略没有详细描述'}
                  </p>
                  {/* Custom Tooltip */}
                  {t.description && (
                    <div className={`absolute bottom-full left-0 mb-2 w-64 p-3 rounded-xl shadow-2xl border text-xs leading-relaxed z-10 opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-gray-200' : 'bg-white border-gray-200 text-gray-700'}`}>
                      {t.description}
                    </div>
                  )}
                </div>
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


        {activeTab === 'positions' && (
          <div className={`rounded-2xl border overflow-hidden shadow-2xl ${isDarkMode ? 'bg-gray-900 border-gray-800' : 'bg-white border-gray-200'}`}>
            <table className="w-full text-left">
              <thead className={`text-xs uppercase tracking-wider ${isDarkMode ? 'bg-gray-800/50 text-gray-400' : 'bg-gray-50 text-gray-500'}`}>
                <tr>
                  <th className="px-6 py-4">交易对</th>
                  <th className="px-6 py-4">数量</th>
                  <th className="px-6 py-4">均价</th>
                  <th className="px-6 py-4">盈亏</th>
                </tr>
              </thead>
              <tbody className={`divide-y ${isDarkMode ? 'divide-gray-800' : 'divide-gray-100'}`}>
                {positions.map((p, i) => (
                  <tr key={i} className={`transition ${isDarkMode ? 'hover:bg-gray-800/30' : 'hover:bg-gray-50'}`}>
                    <td className="px-6 py-4 font-bold">{p.symbol}</td>
                    <td className="px-6 py-4 font-mono">{p.amount}</td>
                    <td className="px-6 py-4 font-mono">${p.price.toLocaleString(undefined, { minimumFractionDigits: 2 })}</td>
                    <td className="px-6 py-4">
                      <span className="font-bold text-green-500">+1.2%</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {activeTab === 'logs' && (
          <div className={`p-6 rounded-2xl font-mono text-sm h-[600px] overflow-y-auto border shadow-2xl custom-scrollbar ${isDarkMode ? 'bg-black/80 text-gray-300 border-gray-800' : 'bg-gray-100 text-gray-700 border-gray-200'}`}>
            {logs.map((log, i) => (
              <div key={i} className="mb-2 flex gap-4">
                <span className="text-gray-500 shrink-0">[{i}]</span>
                <span className="leading-relaxed">{log}</span>
              </div>
            ))}
          </div>
        )}
      </main>

      {/* Create Modal */}
      {showCreateModal && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
          <div className={`w-full max-w-md p-8 rounded-2xl shadow-2xl ${isDarkMode ? 'bg-gray-900 border border-gray-800' : 'bg-white border border-gray-200'}`}>
            <h3 className="text-2xl font-bold mb-6">新建交易策略</h3>
            <div className="space-y-4 mb-8">
              <div>
                <label className="block text-sm font-medium text-gray-500 mb-2">策略名称</label>
                <input 
                  type="text" 
                  value={newStratName}
                  onChange={(e) => setNewStratName(e.target.value)}
                  placeholder="例如: BTC 均线回归"
                  className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-500 mb-2">选择模板</label>
                <select 
                  value={selectedTemplate}
                  onChange={(e) => setSelectedTemplate(Number(e.target.value))}
                  className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                >
                  <option value={0}>请选择一个模板</option>
                  {templates.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
                </select>
              </div>
              {selectedTemplate !== 0 && (
                <div className={`p-4 rounded-xl border text-sm ${isDarkMode ? 'bg-blue-900/20 border-blue-800 text-blue-200' : 'bg-blue-50 border-blue-100 text-blue-700'}`}>
                  <p className="font-bold mb-1">策略说明:</p>
                  <p className="leading-relaxed">{templates.find(t => t.id === selectedTemplate)?.description || '暂无详细说明'}</p>
                </div>
              )}
            </div>

            <div className="flex gap-4">
              <button 
                onClick={() => setShowCreateModal(false)}
                className={`flex-1 py-3 rounded-xl font-bold transition ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700' : 'bg-gray-100 hover:bg-gray-200'}`}
              >
                取消
              </button>
              <button 
                onClick={createStrategy}
                className="flex-1 py-3 bg-blue-600 hover:bg-blue-700 text-white rounded-xl font-bold transition shadow-lg shadow-blue-900/20"
              >
                创建
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Publish Confirm Modal */}
      {showPublishConfirm && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
          <div className={`w-full max-w-md p-8 rounded-2xl shadow-2xl ${isDarkMode ? 'bg-gray-900 border border-gray-800' : 'bg-white border border-gray-200'}`}>
            <div className="flex items-center gap-3 text-blue-500 mb-4">
              <Share2 size={28} />
              <h3 className="text-2xl font-bold">发布策略到广场</h3>
            </div>
            <p className={`mb-8 leading-relaxed ${isDarkMode ? 'text-gray-400' : 'text-gray-600'}`}>
              您确定要将策略 <span className="font-bold text-blue-500">“{strategyToPublish?.name}”</span> 公开发布到策略广场吗？发布后其他交易员将可以引用并使用该策略。
            </p>
            <div className="flex gap-4">
              <button 
                onClick={() => { setShowPublishConfirm(false); setStrategyToPublish(null); }}
                className={`flex-1 py-3 rounded-xl font-bold transition ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700' : 'bg-gray-100 hover:bg-gray-200'}`}
              >
                取消
              </button>
              <button 
                onClick={publishToSquare}
                className="flex-1 py-3 bg-blue-600 hover:bg-blue-700 text-white rounded-xl font-bold transition shadow-lg shadow-blue-900/20"
              >
                确认发布
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Delete Strategy Confirm Modal */}
      {showDeleteConfirm && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
          <div className={`w-full max-w-md p-8 rounded-2xl shadow-2xl ${isDarkMode ? 'bg-gray-900 border border-gray-800' : 'bg-white border border-gray-200'}`}>
            <div className="flex items-center gap-3 text-red-500 mb-4">
              <Trash2 size={28} />
              <h3 className="text-2xl font-bold">删除策略</h3>
            </div>
            <p className={`mb-8 leading-relaxed ${isDarkMode ? 'text-gray-400' : 'text-gray-600'}`}>
              您确定要删除策略 <span className="font-bold text-red-500">“{strategyToDelete?.name}”</span> 吗？此操作将永久停止该策略并删除其配置。
            </p>
            <div className="flex gap-4">
              <button 
                onClick={() => { setShowDeleteConfirm(false); setStrategyToDelete(null); }}
                className={`flex-1 py-3 rounded-xl font-bold transition ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700' : 'bg-gray-100 hover:bg-gray-200'}`}
              >
                取消
              </button>
              <button 
                onClick={deleteStrategy}
                className="flex-1 py-3 bg-red-600 hover:bg-red-700 text-white rounded-xl font-bold transition shadow-lg shadow-red-900/20"
              >
                确认删除
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Delete Template Confirm Modal */}
      {showDeleteTemplateConfirm && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
          <div className={`w-full max-w-md p-8 rounded-2xl shadow-2xl ${isDarkMode ? 'bg-gray-900 border border-gray-800' : 'bg-white border border-gray-200'}`}>
            <div className="flex items-center gap-3 text-red-500 mb-4">
              <Trash2 size={28} />
              <h3 className="text-2xl font-bold">下架广场策略</h3>
            </div>
            <p className={`mb-8 leading-relaxed ${isDarkMode ? 'text-gray-400' : 'text-gray-600'}`}>
              您确定要从广场下架策略 <span className="font-bold text-red-500">“{templateToDelete?.name}”</span> 吗？下架后其他用户将无法再引用此策略。
            </p>
            <div className="flex gap-4">
              <button 
                onClick={() => { setShowDeleteTemplateConfirm(false); setTemplateToDelete(null); }}
                className={`flex-1 py-3 rounded-xl font-bold transition ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700' : 'bg-gray-100 hover:bg-gray-200'}`}
              >
                取消
              </button>
              <button 
                onClick={deleteTemplate}
                className="flex-1 py-3 bg-red-600 hover:bg-red-700 text-white rounded-xl font-bold transition shadow-lg shadow-red-900/20"
              >
                确认下架
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};



interface NavItemProps {
  active: boolean;
  onClick: () => void;
  icon: React.ReactNode;
  label: string;
  isDarkMode: boolean;
}

const NavItem: React.FC<NavItemProps> = ({ active, onClick, icon, label, isDarkMode }) => (
  <button
    onClick={onClick}
    className={`w-full flex items-center gap-3 px-4 py-3 rounded-xl transition-all duration-200 ${active ? 'bg-blue-600 text-white shadow-lg shadow-blue-900/40 translate-x-1' : isDarkMode ? 'text-gray-400 hover:bg-gray-800 hover:text-gray-200' : 'text-gray-500 hover:bg-gray-100 hover:text-gray-900'}`}
  >
    {icon}
    <span className="font-bold">{label}</span>
  </button>
);


export default App;
