import React, { useState, useEffect, useRef } from 'react';
import axios from 'axios';
import { Play, Square, RefreshCw, Activity, Terminal, List, LayoutDashboard, ShoppingBag, Users, LogOut, ShieldCheck, Share2, PlusCircle, Trash2, Menu, X, Sun, Moon, Settings, Code, Search } from 'lucide-react';
import Editor from '@monaco-editor/react';
import Login from './Login';
import Register from './Register';
import LandingPage from './LandingPage';

interface User {
  id: number;
  username: string;
  role: 'admin' | 'user';
  configs?: string;
  created_at?: string;
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
  code?: string;
  is_public: boolean;
  is_draft: boolean;
  author: { id: number, username: string };
}

interface Position {
  symbol: string;
  amount: number;
  price: number;
  strategy_name: string;
  exchange_name: string;
  status: string;
  open_time: string;
  close_time?: string;
}

const DEFAULT_STRATEGY_CODE = `from base_strategy import BaseStrategy
import json

class MyStrategy(BaseStrategy):
    def __init__(self, config):
        super().__init__(config)
        self.window = config.get("window", 20)

    def on_candle(self, candle):
        self.log(f"Received candle: {candle['close']}")
        # Add your logic here

    def on_order(self, order):
        self.log(f"Order updated: {order['id']}")

    def on_position(self, position):
        pass

if __name__ == "__main__":
    import sys
    config = json.loads(sys.argv[1])
    strategy = MyStrategy(config)
    strategy.run()
`;

const App: React.FC = () => {
  const [user, setUser] = useState<User | null>(null);
  const [token, setToken] = useState<string | null>(localStorage.getItem('token'));
  const [showLanding, setShowLanding] = useState(!localStorage.getItem('token'));
  const [isRegistering, setIsRegistering] = useState(false);
  const [strategies, setStrategies] = useState<Strategy[]>([]);
  const [templates, setTemplates] = useState<Template[]>([]);
  const [positions, setPositions] = useState<Position[]>([]);
  const [positionStatus, setPositionStatus] = useState<'active' | 'closed'>('active');
  const [logs, setLogs] = useState<string[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [activeTab, setActiveTab] = useState<'strategies' | 'positions' | 'logs' | 'square' | 'admin' | 'develop'>('strategies');
  
  // Search States
  const [stratSearch, setStratSearch] = useState('');
  const [squareSearch, setSquareSearch] = useState('');
  const [posSearch, setPosSearch] = useState('');
  const [userSearch, setUserSearch] = useState('');

  const [isDarkMode, setIsDarkMode] = useState(true);
  const [isSidebarOpen, setIsSidebarOpen] = useState(false);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [showPublishConfirm, setShowPublishConfirm] = useState(false);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [showDeleteTemplateConfirm, setShowDeleteTemplateConfirm] = useState(false);
  const [showEditConfigModal, setShowEditConfigModal] = useState(false);
  const [strategyToPublish, setStrategyToPublish] = useState<Strategy | null>(null);
  const [strategyToDelete, setStrategyToDelete] = useState<Strategy | null>(null);
  const [templateToDelete, setTemplateToDelete] = useState<Template | null>(null);
  const [strategyToEdit, setStrategyToEdit] = useState<Strategy | null>(null);
  const [editConfigJson, setEditConfigJson] = useState('');
  const [newStratName, setNewStratName] = useState('');
  const [selectedTemplate, setSelectedTemplate] = useState<number>(0);
  
  // Develop Tab State
  const [devCode, setDevCode] = useState(DEFAULT_STRATEGY_CODE);
  const [devName, setDevCodeName] = useState('');
  const [devDesc, setDevCodeDesc] = useState('');
  const [isTestingCode, setIsTestingCode] = useState(false);
  const [testResult, setDevTestResult] = useState<{valid: boolean, error?: string} | null>(null);
  
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
      fetchPositions(positionStatus);
      if (user.role === 'admin') fetchUsers();
      connectWS();
    }
    return () => {
      if (ws.current) ws.current.close();
    };
  }, [user, token, positionStatus]);

  const fetchUsers = async () => {
    try {
      const res = await axios.get('/api/admin/users');
      setUsers(res.data);
    } catch (err) {
      console.error('Failed to fetch users', err);
    }
  };

  const deleteUser = async (id: number) => {
    if (!window.confirm('确定要删除该用户吗？')) return;
    try {
      await axios.delete(`/api/admin/users/${id}`);
      fetchUsers();
    } catch (err) {
      console.error('Failed to delete user', err);
    }
  };

  const updateStrategyConfig = async () => {
    if (!strategyToEdit) return;
    try {
      await axios.put(`/api/strategies/${strategyToEdit.id}/config`, {
        config: editConfigJson
      });
      fetchStrategies();
      setShowEditConfigModal(false);
      setStrategyToEdit(null);
    } catch (err: any) {
      alert(err.response?.data?.error || '更新失败');
    }
  };

  const testStrategyCode = async () => {
    setIsTestingCode(true);
    setDevTestResult(null);
    try {
      const res = await axios.post('/api/templates/test', { code: devCode });
      setDevTestResult(res.data);
    } catch (err) {
      console.error('Failed to test code', err);
    } finally {
      setIsTestingCode(false);
    }
  };

  const saveStrategyTemplate = async (isDraft: boolean) => {
    if (!devName) {
      alert('请输入模板名称');
      return;
    }
    try {
      await axios.post('/api/templates', {
        name: devName,
        description: devDesc,
        code: devCode,
        is_draft: isDraft
      });
      fetchTemplates();
      if (!isDraft) {
        setActiveTab('strategies');
        alert('策略已保存到“我的策略”。');
      } else {
        alert('草稿已暂存。');
      }
    } catch (err: any) {
      alert(err.response?.data?.error || '保存失败');
    }
  };

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

  const fetchPositions = async (status: 'active' | 'closed') => {
    try {
      const res = await axios.get(`/api/positions?status=${status}`);
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
        fetchPositions(positionStatus); // Refresh positions on order
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
    setShowLanding(false);
    localStorage.setItem('token', newToken);
    localStorage.setItem('user', JSON.stringify(newUser));
    axios.defaults.headers.common['Authorization'] = `Bearer ${newToken}`;
  };

  const handleLogout = () => {
    setToken(null);
    setUser(null);
    setShowLanding(true);
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

  if (showLanding && !token) {
    return (
      <LandingPage 
        onGoToLogin={() => { setShowLanding(false); setIsRegistering(false); }} 
        onGoToRegister={() => { setShowLanding(false); setIsRegistering(true); }} 
      />
    );
  }

  if (!token || !user) {
    if (isRegistering) {
      return <Register onBackToLogin={() => setIsRegistering(false)} />;
    }
    return (
      <Login 
        onLogin={handleLogin} 
        onGoToRegister={() => setIsRegistering(true)} 
        onBackToLanding={() => setShowLanding(true)}
      />
    );
  }

  return (
    <div className={`min-h-screen flex flex-col md:flex-row ${isDarkMode ? 'bg-gray-950 text-gray-100' : 'bg-gray-50 text-gray-900'}`}>
      {/* Mobile Header */}
      <div className={`md:hidden flex items-center justify-between p-4 border-b ${isDarkMode ? 'bg-gray-900 border-gray-800' : 'bg-white border-gray-200'}`}>
        <h1 className="text-xl font-bold flex items-center gap-2 text-blue-500">
          <Activity size={24} /> QuantyTrade
        </h1>
        <button 
          onClick={() => setIsSidebarOpen(!isSidebarOpen)}
          className={`p-2 rounded-lg ${isDarkMode ? 'hover:bg-gray-800' : 'hover:bg-gray-100'}`}
        >
          {isSidebarOpen ? <X size={24} /> : <Menu size={24} />}
        </button>
      </div>

      {/* Sidebar Overlay (Mobile) */}
      {isSidebarOpen && (
        <div 
          className="fixed inset-0 bg-black/60 backdrop-blur-sm z-40 md:hidden" 
          onClick={() => setIsSidebarOpen(false)}
        />
      )}

      {/* Sidebar */}
      <aside className={`
        fixed md:static inset-y-0 left-0 z-50 w-64 border-r flex flex-col transition-transform duration-300 transform 
        ${isSidebarOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0'}
        ${isDarkMode ? 'bg-gray-900 border-gray-800' : 'bg-white border-gray-200'}
      `}>
        <div className="p-6 hidden md:block">
          <h1 className="text-2xl font-bold flex items-center gap-2 text-blue-500">
            <Activity size={28} /> QuantyTrade
          </h1>
        </div>

        <nav className="flex-1 px-4 py-4 md:py-0 space-y-2">
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'strategies'} onClick={() => { setActiveTab('strategies'); setIsSidebarOpen(false); }} icon={<LayoutDashboard size={20} />} label="我的策略" />
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'develop'} onClick={() => { setActiveTab('develop'); setIsSidebarOpen(false); }} icon={<Code size={20} />} label="代码开发" />
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'square'} onClick={() => { setActiveTab('square'); setIsSidebarOpen(false); }} icon={<ShoppingBag size={20} />} label="策略广场" />
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'positions'} onClick={() => { setActiveTab('positions'); setIsSidebarOpen(false); }} icon={<List size={20} />} label="仓位管理" />
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'logs'} onClick={() => { setActiveTab('logs'); setIsSidebarOpen(false); }} icon={<Terminal size={20} />} label="系统日志" />
          {user.role === 'admin' && (
            <NavItem isDarkMode={isDarkMode} active={activeTab === 'admin'} onClick={() => { setActiveTab('admin'); setIsSidebarOpen(false); }} icon={<ShieldCheck size={20} />} label="系统管理" />
          )}
        </nav>

        <div className={`p-4 border-t ${isDarkMode ? 'border-gray-800' : 'border-gray-200'}`}>
          <div className="flex items-center gap-3 mb-4 px-2">
            <div className="w-10 h-10 bg-blue-600 rounded-full flex items-center justify-center font-bold text-white shrink-0">
              {user.username[0].toUpperCase()}
            </div>
            <div className="min-w-0">
              <p className="text-sm font-semibold truncate">{user.username}</p>
              <p className="text-xs text-gray-500 capitalize">{user.role}</p>
            </div>
          </div>
          <button
            onClick={() => setIsDarkMode(!isDarkMode)}
            className={`w-full flex items-center gap-2 px-4 py-2 mb-2 rounded-lg transition ${isDarkMode ? 'hover:bg-gray-800 text-gray-300' : 'hover:bg-gray-100 text-gray-600'}`}
          >
            {isDarkMode ? <Sun size={18} /> : <Moon size={18} />}
            <span className="text-sm">{isDarkMode ? '明亮模式' : '黑暗模式'}</span>
          </button>
          <button
            onClick={handleLogout}
            className="w-full flex items-center gap-2 px-4 py-2 text-red-400 hover:bg-red-900/20 rounded-lg transition"
          >
            <LogOut size={18} /> <span className="text-sm">退出登录</span>
          </button>
        </div>
      </aside>

      {/* Main Content */}
      <main className="flex-1 p-4 md:p-8 overflow-y-auto">
        <header className="flex flex-col md:flex-row md:justify-between md:items-center gap-4 mb-6 md:mb-8">
          <div className="flex flex-col md:flex-row md:items-center gap-4 flex-1">
            {activeTab === 'strategies' && (
              <div className="relative w-full max-w-xs">
                <div className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-gray-500">
                  <Search size={16} />
                </div>
                <input
                  type="text"
                  placeholder="搜索名称、交易对或参数..."
                  value={stratSearch}
                  onChange={(e) => setStratSearch(e.target.value)}
                  className={`w-full pl-10 pr-4 py-2 rounded-xl border text-sm transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
                />
              </div>
            )}
            <h2 className="text-xl md:text-2xl font-bold min-w-fit">
              {activeTab === 'strategies' && '我的策略'}
              {activeTab === 'develop' && '策略代码开发'}
              {activeTab === 'square' && '策略广场'}
              {activeTab === 'positions' && '实时持仓'}
              {activeTab === 'logs' && '实时日志'}
              {activeTab === 'admin' && '用户管理'}
            </h2>
          </div>

          
          <div className="flex max-w-md gap-4 items-center">
            {['square', 'positions', 'admin'].includes(activeTab) && (
              <div className="relative flex-1">
                <div className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-gray-500">
                  <Search size={16} />
                </div>
                <input
                  type="text"
                  placeholder="搜索..."
                  value={
                    activeTab === 'square' ? squareSearch :
                    activeTab === 'positions' ? posSearch : userSearch
                  }
                  onChange={(e) => {
                    const val = e.target.value;
                    if (activeTab === 'square') setSquareSearch(val);
                    else if (activeTab === 'positions') setPosSearch(val);
                    else setUserSearch(val);
                  }}
                  className={`w-full pl-10 pr-4 py-2 rounded-xl border text-sm transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
                />
              </div>
            )}
            <button onClick={() => { fetchStrategies(); fetchTemplates(); fetchPositions(positionStatus); if (user.role === 'admin') fetchUsers(); }} className={`p-2 rounded-lg transition shadow-sm border ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 border-gray-700' : 'bg-white hover:bg-gray-50 border-gray-200'}`}>
              <RefreshCw size={18} className="md:w-5 md:h-5" />
            </button>
          </div>
        </header>

        {activeTab === 'develop' && (
          <div className="space-y-6">
            <div className={`p-6 rounded-2xl border shadow-xl ${isDarkMode ? 'bg-gray-900 border-gray-800' : 'bg-white border-gray-200'}`}>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-4">
                <input 
                  type="text" 
                  placeholder="模板名称 (必填)"
                  value={devName}
                  onChange={(e) => setDevCodeName(e.target.value)}
                  className={`px-4 py-2 rounded-xl border transition outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700' : 'bg-gray-50 border-gray-200'}`}
                />
                <input 
                  type="text" 
                  placeholder="策略描述"
                  value={devDesc}
                  onChange={(e) => setDevCodeDesc(e.target.value)}
                  className={`px-4 py-2 rounded-xl border transition outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700' : 'bg-gray-50 border-gray-200'}`}
                />
              </div>
              <div className="border rounded-xl overflow-hidden mb-4 h-[500px]">
                <Editor
                  height="100%"
                  defaultLanguage="python"
                  theme={isDarkMode ? 'vs-dark' : 'light'}
                  value={devCode}
                  onChange={(v) => setDevCode(v || '')}
                  options={{
                    minimap: { enabled: false },
                    fontSize: 14,
                    padding: { top: 16 },
                  }}
                />
              </div>
              <div className="flex gap-4">
                 <button 
                   onClick={testStrategyCode}
                   disabled={isTestingCode}
                   className="flex items-center gap-2 px-6 py-2.5 bg-gray-600 hover:bg-gray-700 text-white rounded-xl font-bold transition disabled:opacity-50"
                 >
                   <Code size={18} /> {isTestingCode ? '测试中...' : '语法检查'}
                 </button>
                 <button 
                   onClick={() => saveStrategyTemplate(true)}
                   className="flex items-center gap-2 px-6 py-2.5 bg-gray-800 border border-gray-700 hover:bg-gray-700 text-white rounded-xl font-bold transition"
                 >
                   暂存草稿
                 </button>
                 <button 
                   onClick={() => saveStrategyTemplate(false)}
                   className="flex items-center gap-2 px-6 py-2.5 bg-blue-600 hover:bg-blue-700 text-white rounded-xl font-bold transition shadow-lg shadow-blue-900/20"
                 >
                   <PlusCircle size={18} /> 保存到我的策略
                 </button>
               </div>
              {testResult && (
                <div className={`mt-4 p-4 rounded-xl border ${testResult.valid ? 'bg-green-900/20 border-green-800 text-green-400' : 'bg-red-900/20 border-red-800 text-red-400'}`}>
                  {testResult.valid ? '✅ 代码语法检查通过' : `❌ 错误: ${testResult.error}`}
                </div>
              )}
            </div>
          </div>
        )}

        {activeTab === 'strategies' && (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            {/* Deploy New Instance Button */}
            <div 
              onClick={() => setShowCreateModal(true)}
              className={`p-6 rounded-2xl border-2 border-dashed flex flex-col items-center justify-center transition cursor-pointer group order-first ${isDarkMode ? 'bg-gray-900/30 border-gray-800 text-gray-600 hover:border-gray-700 hover:text-gray-400' : 'bg-white border-gray-200 text-gray-400 hover:border-blue-200 hover:text-blue-400'}`}
            >
              <PlusCircle size={40} className="mb-2 group-hover:scale-110 transition" />
              <p className="font-bold">部署新实例</p>
            </div>

            {/* Combined List: Instances and Private Templates */}
            {[
              ...strategies.map(s => ({ ...s, type: 'instance' as const })),
              ...templates.filter(t => t.author?.id === user.id && !t.is_public).map(t => ({ ...t, type: 'template' as const }))
            ]
            .filter(item => {
              const search = stratSearch.toLowerCase();
              if (item.type === 'instance') {
                return item.name.toLowerCase().includes(search) || JSON.stringify(item.config).toLowerCase().includes(search);
              } else {
                return item.name.toLowerCase().includes(search) || item.description.toLowerCase().includes(search);
              }
            })
            .sort((a, b) => {
              const idA = typeof a.id === 'string' ? parseInt(a.id) || 0 : a.id;
              const idB = typeof b.id === 'string' ? parseInt(b.id) || 0 : b.id;
              return idB - idA;
            })
            .map(item => {
              if (item.type === 'instance') {
                const s = item as Strategy & { type: 'instance' };
                return (
                  <div key={`instance-${s.id}`} className={`p-6 rounded-2xl border shadow-xl transition ${isDarkMode ? 'bg-gray-900/50 border-gray-800 hover:border-gray-700' : 'bg-white border-gray-200 hover:border-blue-200'}`}>
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
                        onClick={() => { 
                          setStrategyToEdit(s); 
                          setEditConfigJson(JSON.stringify(s.config, null, 2)); 
                          setShowEditConfigModal(true); 
                        }}
                        className={`p-2.5 rounded-xl transition border text-gray-400 ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 border-gray-700' : 'bg-white hover:bg-gray-50 border-gray-200'}`}
                        title="修改配置"
                      >
                        <Settings size={20} />
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
                );
              } else {
                const t = item as Template & { type: 'template' };
                return (
                  <div key={`template-${t.id}`} className={`p-6 rounded-2xl border shadow-xl transition relative group ${isDarkMode ? 'bg-gray-900/50 border-gray-800 hover:border-gray-700' : 'bg-white border-gray-200 hover:border-purple-200'}`}>
                    <div className="flex justify-between items-start mb-4">
                      <div>
                        <h3 className="text-lg font-bold">{t.name}</h3>
                        <p className="text-xs text-purple-500 font-medium">{t.is_draft ? '📝 草稿' : '✅ 已就绪'}</p>
                      </div>
                      <button 
                        onClick={() => { setTemplateToDelete(t); setShowDeleteTemplateConfirm(true); }}
                        className="text-red-500 hover:text-red-600 transition p-1"
                      >
                        <Trash2 size={18} />
                      </button>
                    </div>
                    <p className="text-sm text-gray-500 mb-8 line-clamp-2">{t.description || '暂无描述'}</p>
                    <div className="flex gap-2">
                      <button
                        onClick={() => {
                          setDevCode(t.code || '');
                          setDevCodeName(t.name);
                          setDevCodeDesc(t.description);
                          setActiveTab('develop');
                        }}
                        className={`flex-1 py-2 rounded-xl font-bold border transition ${isDarkMode ? 'bg-gray-800 border-gray-700 hover:bg-gray-700' : 'bg-white border-gray-200 hover:bg-gray-100'}`}
                      >
                        继续开发
                      </button>
                      <button
                        onClick={() => referenceFromSquare(t)}
                        className="flex-1 py-2 bg-purple-600 hover:bg-purple-700 text-white rounded-xl font-bold transition shadow-lg shadow-purple-900/20"
                      >
                        部署运行
                      </button>
                    </div>
                  </div>
                );
              }
            })}
          </div>
        )}

        {activeTab === 'square' && (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            {templates.filter(t => 
              t.name.toLowerCase().includes(squareSearch.toLowerCase()) || 
              t.description.toLowerCase().includes(squareSearch.toLowerCase()) ||
              t.author?.username.toLowerCase().includes(squareSearch.toLowerCase())
            ).map(t => (
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
          <div className="space-y-6">
            <div className="flex gap-2 p-1 bg-gray-900/50 rounded-xl w-fit border border-gray-800">
              <button 
                onClick={() => setPositionStatus('active')}
                className={`px-6 py-2 rounded-lg font-bold transition ${positionStatus === 'active' ? 'bg-blue-600 text-white' : 'text-gray-500 hover:text-gray-300'}`}
              >
                当前持仓
              </button>
              <button 
                onClick={() => setPositionStatus('closed')}
                className={`px-6 py-2 rounded-lg font-bold transition ${positionStatus === 'closed' ? 'bg-blue-600 text-white' : 'text-gray-500 hover:text-gray-300'}`}
              >
                历史持仓
              </button>
            </div>

            <div className={`rounded-2xl border overflow-x-auto shadow-2xl ${isDarkMode ? 'bg-gray-900 border-gray-800' : 'bg-white border-gray-200'}`}>
              <table className="w-full text-left min-w-[800px]">
                <thead className={`text-xs uppercase tracking-wider ${isDarkMode ? 'bg-gray-800/50 text-gray-400' : 'bg-gray-50 text-gray-500'}`}>
                  <tr>
                    <th className="px-4 md:px-6 py-4">交易对</th>
                    <th className="px-4 md:px-6 py-4">引用策略</th>
                    <th className="px-4 md:px-6 py-4">交易所</th>
                    <th className="px-4 md:px-6 py-4">数量</th>
                    <th className="px-4 md:px-6 py-4">均价</th>
                    <th className="px-4 md:px-6 py-4">{positionStatus === 'active' ? '开仓时间' : '平仓时间'}</th>
                    <th className="px-4 md:px-6 py-4">状态</th>
                  </tr>
                </thead>
                <tbody className={`divide-y ${isDarkMode ? 'divide-gray-800' : 'divide-gray-100'}`}>
                  {positions.filter(p => 
                    p.symbol.toLowerCase().includes(posSearch.toLowerCase()) || 
                    p.strategy_name.toLowerCase().includes(posSearch.toLowerCase()) ||
                    p.exchange_name.toLowerCase().includes(posSearch.toLowerCase())
                  ).map((p, i) => (
                    <tr key={i} className={`transition ${isDarkMode ? 'hover:bg-gray-800/30' : 'hover:bg-gray-50'}`}>
                      <td className="px-4 md:px-6 py-4 font-bold">{p.symbol}</td>
                      <td className="px-4 md:px-6 py-4 text-sm text-blue-400 font-medium">{p.strategy_name}</td>
                      <td className="px-4 md:px-6 py-4 text-sm text-gray-500">{p.exchange_name}</td>
                      <td className="px-4 md:px-6 py-4 font-mono">{p.amount}</td>
                      <td className="px-4 md:px-6 py-4 font-mono">${p.price.toLocaleString(undefined, { minimumFractionDigits: 2 })}</td>
                      <td className="px-4 md:px-6 py-4 text-xs text-gray-500 font-mono">
                        {new Date(positionStatus === 'active' ? p.open_time : (p.close_time || '')).toLocaleString()}
                      </td>
                      <td className="px-4 md:px-6 py-4">
                        <span className={`px-2 py-1 rounded text-[10px] font-black uppercase ${p.status === 'active' ? 'bg-green-900/30 text-green-500' : 'bg-gray-800 text-gray-400'}`}>
                          {p.status}
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {activeTab === 'logs' && (
          <div className={`p-4 md:p-6 rounded-2xl font-mono text-xs md:text-sm h-[500px] md:h-[600px] overflow-y-auto border shadow-2xl custom-scrollbar ${isDarkMode ? 'bg-black/80 text-gray-300 border-gray-800' : 'bg-gray-100 text-gray-700 border-gray-200'}`}>
            {logs.map((log, i) => (
              <div key={i} className="mb-2 flex gap-2 md:gap-4">
                <span className="text-gray-500 shrink-0">[{i}]</span>
                <span className="leading-relaxed break-all md:break-normal">{log}</span>
              </div>
            ))}
          </div>
        )}

        {activeTab === 'admin' && (
          <div className={`rounded-2xl border overflow-hidden shadow-2xl ${isDarkMode ? 'bg-gray-900 border-gray-800' : 'bg-white border-gray-200'}`}>
            <div className="p-6 border-b border-gray-800 flex justify-between items-center">
              <h3 className="text-xl font-bold flex items-center gap-2"><Users className="text-blue-500" /> 用户管理</h3>
            </div>
            <table className="w-full text-left">
              <thead className={`text-xs uppercase tracking-wider ${isDarkMode ? 'bg-gray-800/50 text-gray-400' : 'bg-gray-50 text-gray-500'}`}>
                <tr>
                  <th className="px-6 py-4">ID</th>
                  <th className="px-6 py-4">用户名</th>
                  <th className="px-6 py-4">角色</th>
                  <th className="px-6 py-4">注册时间</th>
                  <th className="px-6 py-4 text-right">操作</th>
                </tr>
              </thead>
              <tbody className={`divide-y ${isDarkMode ? 'divide-gray-800' : 'divide-gray-100'}`}>
                {users.filter(u => 
                  u.username.toLowerCase().includes(userSearch.toLowerCase()) ||
                  u.role.toLowerCase().includes(userSearch.toLowerCase())
                ).map(u => (
                  <tr key={u.id} className={`transition ${isDarkMode ? 'hover:bg-gray-800/30' : 'hover:bg-gray-50'}`}>
                    <td className="px-6 py-4 font-mono text-sm text-gray-500">{u.id}</td>
                    <td className="px-6 py-4 font-bold">{u.username}</td>
                    <td className="px-6 py-4">
                      <span className={`px-2 py-0.5 rounded-full text-[10px] font-black uppercase ${u.role === 'admin' ? 'bg-purple-900/30 text-purple-400' : 'bg-blue-900/30 text-blue-400'}`}>
                        {u.role}
                      </span>
                    </td>
                    <td className="px-6 py-4 text-sm text-gray-500">
                      {new Date(u.created_at || '').toLocaleString()}
                    </td>
                    <td className="px-6 py-4 text-right">
                      {u.id !== 1 && (
                        <button 
                          onClick={() => deleteUser(u.id)}
                          className="text-red-500 hover:text-red-400 transition p-2"
                        >
                          <Trash2 size={18} />
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
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

      {/* Edit Config Modal */}
      {showEditConfigModal && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
          <div className={`w-full max-w-lg p-8 rounded-2xl shadow-2xl ${isDarkMode ? 'bg-gray-900 border border-gray-800' : 'bg-white border border-gray-200'}`}>
            <div className="flex items-center gap-3 text-gray-400 mb-6">
              <Settings size={28} />
              <h3 className="text-2xl font-bold">编辑策略配置</h3>
            </div>
            <div className="mb-6">
              <label className="block text-sm font-medium text-gray-500 mb-2">JSON 配置</label>
              <textarea 
                value={editConfigJson}
                onChange={(e) => setEditConfigJson(e.target.value)}
                rows={10}
                className={`w-full px-4 py-3 rounded-xl border font-mono text-sm transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-black/50 border-gray-700 text-green-400' : 'bg-gray-50 border-gray-200'}`}
                placeholder='{"symbol": "BTC/USDT", "window": 20}'
              />
              <p className="mt-2 text-xs text-gray-500">提示: 只有在策略停止状态下才能修改配置。</p>
            </div>
            <div className="flex gap-4">
              <button 
                onClick={() => { setShowEditConfigModal(false); setStrategyToEdit(null); }}
                className={`flex-1 py-3 rounded-xl font-bold transition ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700' : 'bg-gray-100 hover:bg-gray-200'}`}
              >
                取消
              </button>
              <button 
                onClick={updateStrategyConfig}
                className="flex-1 py-3 bg-blue-600 hover:bg-blue-700 text-white rounded-xl font-bold transition shadow-lg shadow-blue-900/20"
              >
                保存配置
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
