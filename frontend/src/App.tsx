import React, { useState, useEffect, useRef } from 'react';
import axios from 'axios';
import { Play, Square, RefreshCw, Activity, Terminal, List, LayoutDashboard, ShoppingBag, Users, LogOut, ShieldCheck, Share2, PlusCircle, Trash2, Menu, X, Sun, Moon, Settings, Code, Search, Info, CheckCircle, AlertCircle } from 'lucide-react';
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
  template_id?: number;
}

interface Template {
  id: number;
  name: string;
  description: string;
  path: string;
  code?: string;
  is_public: boolean;
  is_draft: boolean;
  is_enabled: boolean;
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

interface Toast {
  id: number;
  message: string;
  type: 'success' | 'error' | 'info' | 'warning';
}

interface ConfirmOptions {
  show: boolean;
  title: string;
  message: string;
  onConfirm: () => void;
  onCancel?: () => void;
  confirmText?: string;
  cancelText?: string;
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
  const [activeTab, setActiveTab] = useState<'strategies' | 'templates' | 'positions' | 'logs' | 'square' | 'admin' | 'develop'>('strategies');
  
  // Search States
  const [stratSearch, setStratSearch] = useState('');
  const [templateSearch, setTemplateSearch] = useState('');
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
  const [showPositionDetailModal, setShowPositionDetailModal] = useState(false);
  const [selectedPosition, setSelectedPosition] = useState<Position | null>(null);
  const [strategyToPublish, setStrategyToPublish] = useState<Strategy | null>(null);
  const [strategyToDelete, setStrategyToDelete] = useState<Strategy | null>(null);
  const [templateToDelete, setTemplateToDelete] = useState<Template | null>(null);
  const [strategyToEdit, setStrategyToEdit] = useState<Strategy | null>(null);
  const [editConfigJson, setEditConfigJson] = useState('');
  const [newStratName, setNewStratName] = useState('');
  const [selectedTemplate, setSelectedTemplate] = useState<number>(0);
  
  // UI Enhancements
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [confirm, setConfirm] = useState<ConfirmOptions>({
    show: false,
    title: '',
    message: '',
    onConfirm: () => {},
  });

  const showToast = (message: string, type: 'success' | 'error' | 'info' | 'warning' = 'info') => {
    const id = Date.now();
    setToasts(prev => [...prev, { id, message, type }]);
    setTimeout(() => {
      setToasts(prev => prev.filter(t => t.id !== id));
    }, 3000);
  };

  const customConfirm = (title: string, message: string, onConfirm: () => void, confirmText = '确定', cancelText = '取消') => {
    setConfirm({
      show: true,
      title,
      message,
      onConfirm: () => {
        onConfirm();
        setConfirm(prev => ({ ...prev, show: false }));
      },
      onCancel: () => setConfirm(prev => ({ ...prev, show: false })),
      confirmText,
      cancelText,
    });
  };
  
  // Develop Tab State
  const [devCode, setDevCode] = useState(() => localStorage.getItem('dev_code') || DEFAULT_STRATEGY_CODE);
  const [devName, setDevCodeName] = useState(() => localStorage.getItem('dev_name') || '');
  const [devDesc, setDevCodeDesc] = useState(() => localStorage.getItem('dev_desc') || '');
  const [isTestingCode, setIsTestingCode] = useState(false);
  const [testResult, setDevTestResult] = useState<{valid: boolean, error?: string} | null>(null);
  
  const ws = useRef<WebSocket | null>(null);

  // Auto-save Develop Tab State to localStorage
  useEffect(() => {
    localStorage.setItem('dev_code', devCode);
    localStorage.setItem('dev_name', devName);
    localStorage.setItem('dev_desc', devDesc);
  }, [devCode, devName, devDesc]);

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
    customConfirm('删除用户', '确定要删除该用户吗？', async () => {
      try {
        await axios.delete(`/api/admin/users/${id}`);
        fetchUsers();
        showToast('用户已删除', 'success');
      } catch (err) {
        console.error('Failed to delete user', err);
        showToast('删除失败', 'error');
      }
    });
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
      showToast('配置更新成功', 'success');
    } catch (err: any) {
      showToast(err.response?.data?.error || '更新失败', 'error');
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

  const saveStrategyTemplate = async () => {
    if (!devName) {
      showToast('请输入模板名称', 'warning');
      return;
    }
    try {
      await axios.post('/api/templates', {
        name: devName,
        description: devDesc,
        code: devCode,
        is_draft: false
      });
      await fetchTemplates();
      await fetchStrategies();
      setActiveTab('templates');
      showToast('策略模板已保存', 'success');
      // Clear draft after successful save
      setDevCodeName('');
      setDevCodeDesc('');
      setDevCode(DEFAULT_STRATEGY_CODE);
      localStorage.removeItem('dev_name');
      localStorage.removeItem('dev_desc');
      localStorage.removeItem('dev_code');
    } catch (err: any) {
      showToast(err.response?.data?.error || '保存失败', 'error');
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

  const fetchTemplates = async (onlyEnabled = false) => {
    try {
      const res = await axios.get(`/api/templates${onlyEnabled ? '?only_enabled=true' : ''}`);
      setTemplates(res.data);
    } catch (err) {
      console.error('Failed to fetch templates', err);
    }
  };

  const toggleTemplateEnabled = async (t: Template) => {
    try {
      await axios.post(`/api/templates/${t.id}/toggle`);
      fetchTemplates();
    } catch (err) {
      console.error('Failed to toggle template', err);
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
    localStorage.removeItem('dev_name');
    localStorage.removeItem('dev_desc');
    localStorage.removeItem('dev_code');
    setDevCodeName('');
    setDevCodeDesc('');
    setDevCode(DEFAULT_STRATEGY_CODE);
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
    if (!newStratName.trim()) {
      showToast('请输入策略名称', 'warning');
      return;
    }
    if (strategies.some(s => s.name === newStratName)) {
      showToast('策略名称已存在，请换一个名称', 'warning');
      return;
    }
    try {
      await axios.post('/api/strategies', {
        name: newStratName,
        template_id: selectedTemplate,
        config: JSON.stringify({ symbol: 'BTC/USDT', window: 20 })
      });
      fetchStrategies();
      setShowCreateModal(false);
      setNewStratName('');
      setSelectedTemplate(0);
      setActiveTab('strategies');
      showToast('策略创建成功', 'success');
    } catch (err: any) {
      showToast(err.response?.data?.error || '创建失败', 'error');
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
      showToast('发布成功！', 'success');
    } catch (err) {
      console.error('Failed to publish', err);
      showToast('发布失败', 'error');
    }
  };

  const referenceFromSquare = (t: Template) => {
    setSelectedTemplate(t.id);
    setNewStratName(`${t.name}_copy`);
    setShowCreateModal(true);
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
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'templates'} onClick={() => { setActiveTab('templates'); setIsSidebarOpen(false); }} icon={<ShoppingBag size={20} />} label="策略模板" />
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
            title={isDarkMode ? "切换到明亮模式" : "切换到黑暗模式"}
          >
            {isDarkMode ? <Sun size={18} /> : <Moon size={18} />}
            <span className="text-sm">{isDarkMode ? '明亮模式' : '黑暗模式'}</span>
          </button>
          <button
            onClick={handleLogout}
            className="w-full flex items-center gap-2 px-4 py-2 text-red-400 hover:bg-red-900/20 rounded-lg transition"
            title="退出登录：清除本地会话并返回登录界面"
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
              <>
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
                <div className="flex items-center gap-2 px-3 py-1.5 rounded-xl border border-gray-800 bg-gray-900/50">
                  <span className="text-xs text-gray-500 font-bold uppercase tracking-widest">显示已禁用脚本</span>
                  <button 
                    onClick={() => fetchTemplates(false)} 
                    className={`w-8 h-4 rounded-full transition relative ${templates.some(t => !t.is_enabled) ? 'bg-blue-600' : 'bg-gray-700'}`}
                  >
                    <div className={`absolute top-0.5 w-3 h-3 bg-white rounded-full transition-all ${templates.some(t => !t.is_enabled) ? 'left-4.5' : 'left-0.5'}`} />
                  </button>
                </div>
              </>
            )}
            <h2 className="text-xl md:text-2xl font-bold min-w-fit">
              {activeTab === 'strategies' && '我的策略'}
              {activeTab === 'templates' && '策略模板'}
              {activeTab === 'develop' && '策略代码开发'}
              {activeTab === 'square' && '策略广场'}
              {activeTab === 'positions' && '实时持仓'}
              {activeTab === 'logs' && '实时日志'}
              {activeTab === 'admin' && '用户管理'}
            </h2>
          </div>

          
          <div className="flex max-w-md gap-4 items-center">
            {['templates', 'square', 'positions', 'admin'].includes(activeTab) && (
              <div className="relative flex-1">
                <div className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-gray-500">
                  <Search size={16} />
                </div>
                <input
                  type="text"
                  placeholder="搜索..."
                  value={
                    activeTab === 'templates' ? templateSearch :
                    activeTab === 'square' ? squareSearch :
                    activeTab === 'positions' ? posSearch : userSearch
                  }
                  onChange={(e) => {
                    const val = e.target.value;
                    if (activeTab === 'templates') setTemplateSearch(val);
                    else if (activeTab === 'square') setSquareSearch(val);
                    else if (activeTab === 'positions') setPosSearch(val);
                    else setUserSearch(val);
                  }}
                  className={`w-full pl-10 pr-4 py-2 rounded-xl border text-sm transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-900 border-gray-800 text-white' : 'bg-white border-gray-200 text-gray-900'}`}
                />
              </div>
            )}
            <button 
              onClick={() => { fetchStrategies(); fetchTemplates(); fetchPositions(positionStatus); if (user.role === 'admin') fetchUsers(); }} 
              className={`p-2 rounded-lg transition shadow-sm border ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 border-gray-700' : 'bg-white hover:bg-gray-50 border-gray-200'}`}
              title="立即刷新：手动同步后端最新策略、持仓和模板数据"
            >
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
                   onClick={saveStrategyTemplate}
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
            {/* Combined List: Instances and Local Draft */}
            {[
              ...strategies.map(s => ({ ...s, type: 'instance' as const })),
              // Add Local Draft if it exists and is not the default state
              ...((devName || (devCode && devCode !== DEFAULT_STRATEGY_CODE)) ? [{
                id: 0,
                name: devName || '未命名草稿',
                description: devDesc || '本地自动保存的草稿',
                code: devCode,
                path: '',
                is_public: false,
                is_draft: true,
                is_enabled: true,
                author: { id: user?.id || 0, username: user?.username || '' },
                type: 'template' as const,
                is_local: true
              }] : [])
            ]
            .filter(item => {
              const search = stratSearch.toLowerCase();
              if (item.type === 'instance') {
                return item.name.toLowerCase().includes(search) || JSON.stringify(item.config).toLowerCase().includes(search);
              } else {
                return item.name.toLowerCase().includes(search) || (item.description || '').toLowerCase().includes(search);
              }
            })
            .sort((a, b) => {
              if ((a as any).is_local) return -1;
              if ((b as any).is_local) return 1;
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
                        title={s.status === 'running' ? "停止运行：立即中断此策略的自动化交易" : "启动运行：开始执行此策略的自动化交易逻辑"}
                      >
                        {s.status === 'running' ? <><Square size={16} /> 停止</> : <><Play size={16} /> 启动</>}
                      </button>
                      <button
                        onClick={() => { setStrategyToPublish(s); setShowPublishConfirm(true); }}
                        className={`p-2.5 rounded-xl transition border text-blue-400 ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 border-gray-700' : 'bg-white hover:bg-gray-50 border-gray-200'}`}
                        title="发布：将此策略共享到广场，供他人参考引用"
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
                        title="配置参数：修改交易对、窗口期等策略运行参数"
                      >
                        <Settings size={20} />
                      </button>
                      <button
                        onClick={() => { setStrategyToDelete(s); setShowDeleteConfirm(true); }}
                        className={`p-2.5 rounded-xl transition border text-red-400 ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 border-gray-700' : 'bg-white hover:bg-gray-50 border-gray-200'}`}
                        title="删除策略：停止运行并永久删除此策略实例"
                      >
                        <Trash2 size={20} />
                      </button>
                    </div>
                  </div>
                );
              } else {
                const t = item as Template & { type: 'template', is_local?: boolean };
                return (
                  <div key={t.is_local ? 'local-draft' : `template-${t.id}`} className={`p-6 rounded-2xl border shadow-xl transition relative group ${isDarkMode ? 'bg-gray-900/50 border-gray-800 hover:border-gray-700' : 'bg-white border-gray-200 hover:border-purple-200'} ${t.is_local ? 'border-blue-500/50 bg-blue-500/5' : ''}`}>
                    <div className="flex justify-between items-start mb-4">
                      <div>
                        <h3 className="text-lg font-bold">{t.name}</h3>
                        <div className="flex gap-2 items-center">
                          <p className={`text-xs font-medium ${t.is_local ? 'text-blue-500' : 'text-purple-500'}`}>
                            {t.is_local ? '✨ 本地草稿' : (t.is_draft ? '📝 草稿' : '✅ 已就绪')}
                          </p>
                        </div>
                      </div>
                      <div className="flex gap-1">
                        {t.is_local && (
                          <button 
                            onClick={() => {
                              customConfirm('清除草稿', '确定要放弃本地草稿吗？', () => {
                                setDevCodeName('');
                                setDevCodeDesc('');
                                setDevCode(DEFAULT_STRATEGY_CODE);
                                localStorage.removeItem('dev_name');
                                localStorage.removeItem('dev_desc');
                                localStorage.removeItem('dev_code');
                                showToast('草稿已清除', 'info');
                              });
                            }}
                            className="text-gray-500 hover:text-red-500 transition p-1.5"
                            title="清除草稿：永久放弃当前未保存的开发进度"
                          >
                            <Trash2 size={16} />
                          </button>
                        )}
                      </div>
                    </div>
                    <p className="text-sm text-gray-500 mb-8 line-clamp-2">{t.description || '暂无描述'}</p>
                    <div className="flex gap-2">
                      <button
                        onClick={() => {
                          setDevCode(t.code || '');
                          setDevCodeName(t.name);
                          setDevCodeDesc(t.description || '');
                          setActiveTab('develop');
                        }}
                        className={`flex-1 py-2 rounded-xl font-bold border transition ${isDarkMode ? 'bg-gray-800 border-gray-700 hover:bg-gray-700' : 'bg-white border-gray-200 hover:bg-gray-100'}`}
                        title="继续编辑：跳转到开发页面继续编写此草稿"
                      >
                        {t.is_local ? '继续编辑' : '继续开发'}
                      </button>
                    </div>
                  </div>
                );
              }
            })}
          </div>
        )}

        {activeTab === 'templates' && (
          <div className={`rounded-2xl border overflow-hidden shadow-2xl ${isDarkMode ? 'bg-gray-900 border-gray-800' : 'bg-white border-gray-200'}`}>
            <table className="w-full text-left">
              <thead className={`text-xs uppercase tracking-wider ${isDarkMode ? 'bg-gray-800/50 text-gray-400' : 'bg-gray-50 text-gray-500'}`}>
                <tr>
                  <th className="px-6 py-4">模板名称</th>
                  <th className="px-6 py-4">描述</th>
                  <th className="px-6 py-4">状态</th>
                  <th className="px-6 py-4 text-right">操作</th>
                </tr>
              </thead>
              <tbody className={`divide-y ${isDarkMode ? 'divide-gray-800' : 'divide-gray-100'}`}>
                {templates.filter(t => 
                  (t.author?.id === user?.id || user?.role === 'admin') && 
                  !t.is_public && !t.is_draft &&
                  (t.name.toLowerCase().includes(templateSearch.toLowerCase()) || 
                   (t.description || '').toLowerCase().includes(templateSearch.toLowerCase()))
                ).map(t => (
                  <tr key={t.id} className={`transition ${isDarkMode ? 'hover:bg-gray-800/30' : 'hover:bg-gray-50'}`}>
                    <td className="px-6 py-4 font-bold">{t.name}</td>
                    <td className="px-6 py-4 text-sm text-gray-500 truncate max-w-xs">{t.description || '暂无描述'}</td>
                    <td className="px-6 py-4">
                      <div className="flex flex-col gap-1">
                        <span className={`px-2 py-0.5 rounded-full text-[10px] font-black uppercase tracking-wider w-fit ${t.is_enabled ? 'bg-green-900/30 text-green-400' : 'bg-gray-800 text-gray-500'}`}>
                          {t.is_enabled ? '已启用' : '已禁用'}
                        </span>
                        {strategies.some(s => s.template_id === t.id) && (
                          <span className="px-2 py-0.5 rounded-full text-[10px] font-black uppercase tracking-wider bg-blue-900/30 text-blue-400 w-fit">
                            已在运行
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="px-6 py-4 text-right">
                      <div className="flex justify-end gap-2">
                        <button 
                          onClick={() => toggleTemplateEnabled(t)}
                          className={`p-1.5 rounded-lg transition ${t.is_enabled ? 'text-orange-400 hover:bg-orange-900/20' : 'text-green-400 hover:bg-green-900/20'}`}
                          title={t.is_enabled ? "禁用模板：此模板将无法用于部署新策略" : "启用模板：启用后可用于部署新策略"}
                        >
                          {t.is_enabled ? <Square size={16} /> : <Play size={16} />}
                        </button>
                        <button
                          onClick={() => {
                            setDevCode(t.code || '');
                            setDevCodeName(t.name);
                            setDevCodeDesc(t.description || '');
                            setActiveTab('develop');
                          }}
                          className="p-1.5 text-blue-400 hover:bg-blue-900/20 rounded-lg transition"
                          title="编辑代码：跳转到开发页面修改此模板源码"
                        >
                          <Code size={18} />
                        </button>
                        <button
                          onClick={() => referenceFromSquare(t)}
                          className="p-1.5 text-purple-400 hover:bg-purple-900/20 rounded-lg transition"
                          title="部署运行：基于此模板创建一个新的交易实例"
                        >
                          <PlusCircle size={18} />
                        </button>
                        <button 
                          onClick={() => { setTemplateToDelete(t); setShowDeleteTemplateConfirm(true); }}
                          className="p-1.5 text-red-500 hover:bg-red-900/20 rounded-lg transition"
                          title="删除模板：永久移除此模板文件及配置"
                        >
                          <Trash2 size={18} />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
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
                        title="下架策略：从公共广场移除此策略，但保留原作者的私有副本"
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
                  title="引用此策略：将此公开策略复制到我的模板并准备部署"
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
                    <th className="px-4 md:px-6 py-4 text-right">操作</th>
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
                      <td className="px-4 md:px-6 py-4 text-right">
                        <button 
                          onClick={() => { setSelectedPosition(p); setShowPositionDetailModal(true); }}
                          className={`p-2 rounded-lg transition ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 text-blue-400' : 'bg-gray-100 hover:bg-gray-200 text-blue-600'}`}
                          title="查看详情：打开模态框查看此持仓的完整入场、价值及时间明细"
                        >
                          <Info size={18} />
                        </button>
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
                  {templates.filter(t => t.is_enabled).map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
                </select>
              </div>
              {selectedTemplate !== 0 && (
                <div className={`p-4 rounded-xl border text-sm ${isDarkMode ? 'bg-blue-900/20 border-blue-800 text-blue-200' : 'bg-blue-50 border-blue-100 text-blue-700'}`}>
                  <p className="font-bold mb-1">策略说明:</p>
                  <p className="leading-relaxed mb-2">{templates.find(t => t.id === selectedTemplate)?.description || '暂无详细说明'}</p>
                  {strategies.some(s => s.template_id === selectedTemplate) && (
                    <p className="text-xs text-orange-400 font-bold border-t border-blue-800/30 pt-2 flex items-center gap-1">
                      <Info size={12} /> 该模板已有一个运行中的实例，请确保使用不同的名称。
                    </p>
                  )}
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

      {/* Position Detail Modal */}
      {showPositionDetailModal && selectedPosition && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
          <div className={`w-full max-w-2xl p-8 rounded-2xl shadow-2xl ${isDarkMode ? 'bg-gray-900 border border-gray-800' : 'bg-white border border-gray-200'}`}>
            <div className="flex items-center justify-between mb-8">
              <div className="flex items-center gap-3 text-blue-500">
                <Info size={28} />
                <h3 className="text-2xl font-bold">仓位详细信息</h3>
              </div>
              <button 
                onClick={() => { setShowPositionDetailModal(false); setSelectedPosition(null); }}
                className={`p-2 rounded-lg transition ${isDarkMode ? 'hover:bg-gray-800' : 'hover:bg-gray-100'}`}
              >
                <X size={24} />
              </button>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-6 mb-8">
              <DetailItem label="交易对" value={selectedPosition.symbol} isDarkMode={isDarkMode} highlight />
              <DetailItem label="策略名称" value={selectedPosition.strategy_name} isDarkMode={isDarkMode} />
              <DetailItem label="交易所" value={selectedPosition.exchange_name} isDarkMode={isDarkMode} />
              <DetailItem label="仓位状态" value={selectedPosition.status} isDarkMode={isDarkMode} isStatus status={selectedPosition.status} />
              <DetailItem label="持有数量" value={selectedPosition.amount.toString()} isDarkMode={isDarkMode} />
              <DetailItem label="入场均价" value={`$${selectedPosition.price.toLocaleString(undefined, { minimumFractionDigits: 2 })}`} isDarkMode={isDarkMode} />
              <DetailItem label="开仓时间" value={new Date(selectedPosition.open_time).toLocaleString()} isDarkMode={isDarkMode} />
              {selectedPosition.close_time && (
                <DetailItem label="平仓时间" value={new Date(selectedPosition.close_time).toLocaleString()} isDarkMode={isDarkMode} />
              )}
              <DetailItem 
                label="当前价值" 
                value={`$${(selectedPosition.amount * selectedPosition.price).toLocaleString(undefined, { minimumFractionDigits: 2 })}`} 
                isDarkMode={isDarkMode} 
                highlight 
              />
            </div>

            <div className="flex justify-end">
              <button 
                onClick={() => { setShowPositionDetailModal(false); setSelectedPosition(null); }}
                className={`px-8 py-3 rounded-xl font-bold transition ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700' : 'bg-gray-100 hover:bg-gray-200'}`}
              >
                关闭
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Custom Confirm Modal */}
      {confirm.show && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-[100] p-4">
          <div className={`w-full max-w-md p-8 rounded-2xl shadow-2xl animate-in fade-in zoom-in duration-200 ${isDarkMode ? 'bg-gray-900 border border-gray-800' : 'bg-white border border-gray-200'}`}>
            <h3 className="text-2xl font-bold mb-4">{confirm.title}</h3>
            <p className={`mb-8 leading-relaxed ${isDarkMode ? 'text-gray-400' : 'text-gray-600'}`}>
              {confirm.message}
            </p>
            <div className="flex gap-4">
              <button 
                onClick={confirm.onCancel}
                className={`flex-1 py-3 rounded-xl font-bold transition ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700' : 'bg-gray-100 hover:bg-gray-200'}`}
              >
                {confirm.cancelText || '取消'}
              </button>
              <button 
                onClick={confirm.onConfirm}
                className="flex-1 py-3 bg-blue-600 hover:bg-blue-700 text-white rounded-xl font-bold transition shadow-lg shadow-blue-900/20"
              >
                {confirm.confirmText || '确定'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Toasts */}
      <div className="fixed bottom-8 right-8 z-[110] flex flex-col gap-3 pointer-events-none">
        {toasts.map(toast => (
          <div 
            key={toast.id}
            className={`
              flex items-center gap-3 px-6 py-4 rounded-2xl shadow-2xl pointer-events-auto animate-in slide-in-from-right-full duration-300
              ${toast.type === 'success' ? (isDarkMode ? 'bg-green-900/90 text-green-100 border border-green-800' : 'bg-green-50 text-green-800 border border-green-100') : ''}
              ${toast.type === 'error' ? (isDarkMode ? 'bg-red-900/90 text-red-100 border border-red-800' : 'bg-red-50 text-red-800 border border-red-100') : ''}
              ${toast.type === 'warning' ? (isDarkMode ? 'bg-yellow-900/90 text-yellow-100 border border-yellow-800' : 'bg-yellow-50 text-yellow-800 border border-yellow-100') : ''}
              ${toast.type === 'info' ? (isDarkMode ? 'bg-blue-900/90 text-blue-100 border border-blue-800' : 'bg-blue-50 text-blue-800 border border-blue-100') : ''}
            `}
          >
            {toast.type === 'success' && <CheckCircle size={20} />}
            {toast.type === 'error' && <AlertCircle size={20} />}
            {toast.type === 'warning' && <AlertCircle size={20} />}
            {toast.type === 'info' && <Info size={20} />}
            <span className="font-bold text-sm">{toast.message}</span>
          </div>
        ))}
      </div>
    </div>
  );
};

const DetailItem = ({ label, value, isDarkMode, highlight, isStatus, status }: { label: string, value: string, isDarkMode: boolean, highlight?: boolean, isStatus?: boolean, status?: string }) => (
  <div className={`p-4 rounded-xl border ${isDarkMode ? 'bg-black/20 border-gray-800' : 'bg-gray-50 border-gray-100'}`}>
    <p className="text-xs text-gray-500 uppercase tracking-wider font-bold mb-1">{label}</p>
    {isStatus ? (
      <span className={`inline-block px-2 py-0.5 rounded text-[10px] font-black uppercase ${status === 'active' ? 'bg-green-900/30 text-green-500' : 'bg-gray-800 text-gray-400'}`}>
        {value}
      </span>
    ) : (
      <p className={`text-lg font-mono ${highlight ? 'text-blue-500 font-black' : isDarkMode ? 'text-gray-200' : 'text-gray-800'}`}>
        {value}
      </p>
    )}
  </div>
);



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
