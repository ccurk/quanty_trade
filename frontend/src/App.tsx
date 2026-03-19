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
  config: Record<string, unknown>;
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
  author_id: number;
  author: { id: number, username: string };
}

interface Position {
  symbol: string;
  amount: number;
  price: number;
  current_price?: number;
  unrealized_pnl?: number;
  return_rate?: number;
  strategy_name: string;
  exchange_name: string;
  status: string;
  open_time: string;
  close_time?: string;
}

interface PnLPeriodSummary {
  start_time: string;
  gross_profit: number;
  gross_loss: number;
  realized_pnl: number;
  realized_notional: number;
  realized_return_pct: number;
  unrealized_pnl: number;
  total_pnl: number;
}

interface PnLSummaryResponse {
  updated_at: string;
  unrealized_pnl: number;
  day: PnLPeriodSummary;
  week: PnLPeriodSummary;
  month: PnLPeriodSummary;
}

interface EquityPoint {
  timestamp: string;
  equity: number;
}

interface BacktestResult {
  total_trades: number;
  total_profit: number;
  return_rate: number;
  initial_balance: number;
  final_balance: number;
  equity_curve: EquityPoint[];
}

interface BacktestRow {
  id: number;
  strategy_id: string;
  start_time: string;
  end_time: string;
  initial_balance: number;
  final_balance: number;
  total_trades: number;
  total_profit: number;
  return_rate: number;
  status: string;
  created_at: string;
  result?: string;
}

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null;

const getCfgString = (cfg: Record<string, unknown>, key: string, fallback: string) => {
  const v = cfg[key];
  return typeof v === 'string' ? v : fallback;
};

const getCfgNumber = (cfg: Record<string, unknown>, key: string, fallback: number) => {
  const v = cfg[key];
  return typeof v === 'number' ? v : fallback;
};

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
import sys


class MyStrategy(BaseStrategy):
    def __init__(self, config):
        super().__init__(config)
        self.symbol = config.get("symbol", "BTC/USDT")
        self.fast_window = int(config.get("fast_window", 10))
        self.slow_window = int(config.get("slow_window", 30))
        self.trade_amount = float(config.get("trade_amount", 0.01))
        self.take_profit_pct = float(config.get("take_profit_pct", 0.03))
        self.stop_loss_pct = float(config.get("stop_loss_pct", 0.01))

        self.closes = []
        self.in_position = False
        self.entry_price = None

    def on_candle(self, candle):
        close = float(candle.get("close", 0))
        if close <= 0:
            return

        self.closes.append(close)
        if len(self.closes) > self.slow_window:
            self.closes.pop(0)

        if self.in_position and self.entry_price:
            if close >= self.entry_price * (1.0 + self.take_profit_pct):
                self.log(f"TAKE PROFIT close={close}")
                self.send_order("sell", self.trade_amount, 0)
                self.in_position = False
                self.entry_price = None
                return
            if close <= self.entry_price * (1.0 - self.stop_loss_pct):
                self.log(f"STOP LOSS close={close}")
                self.send_order("sell", self.trade_amount, 0)
                self.in_position = False
                self.entry_price = None
                return

        if len(self.closes) < self.slow_window:
            return

        fast_ma = sum(self.closes[-self.fast_window:]) / self.fast_window
        slow_ma = sum(self.closes) / self.slow_window

        if fast_ma > slow_ma and not self.in_position:
            self.log(f"BUY signal fast={fast_ma:.4f} slow={slow_ma:.4f} close={close:.4f}")
            self.send_order("buy", self.trade_amount, 0)
            self.in_position = True
            self.entry_price = close
            return

        if fast_ma < slow_ma and self.in_position:
            self.log(f"SELL signal fast={fast_ma:.4f} slow={slow_ma:.4f} close={close:.4f}")
            self.send_order("sell", self.trade_amount, 0)
            self.in_position = False
            self.entry_price = None

    def on_order(self, order):
        return

    def on_position(self, position):
        return


if __name__ == "__main__":
    config_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    config = json.loads(config_str)
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
  const [activeTab, setActiveTab] = useState<'strategies' | 'templates' | 'positions' | 'stats' | 'logs' | 'square' | 'admin' | 'develop'>('strategies');
  
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
  const [newStratName, setNewStratName] = useState('');
  const [newStratConfig, setNewStratConfig] = useState({
    symbol: 'BTC/USDT',
    leverage: 20,
    fast_window: 10,
    slow_window: 30,
    trade_amount: 0.01,
    take_profit_pct: 0.03,
    stop_loss_pct: 0.01,
    trailing_stop_pct: 0.005,
    max_hold_bars: 0,
    cooldown_bars: 0,
    max_trades_per_day: 3,
    max_concurrent_positions: 1,
    warmup_bars: 100,
    position_pct: 10,
    take_profit: 5,
    stop_loss: 2,
    close_yield: 10,
  });
  const [selectedTemplate, setSelectedTemplate] = useState<number>(0);
  const [pnlSummary, setPnlSummary] = useState<PnLSummaryResponse | null>(null);
  
  // Backtest State
  const [showBacktestModal, setShowBacktestModal] = useState(false);
  const [strategyToBacktest, setStrategyToBacktest] = useState<Strategy | null>(null);
  const [isBacktesting, setIsBacktesting] = useState(false);
  const [backtestResult, setBacktestResult] = useState<BacktestResult | null>(null);
  const [isAsyncBacktest, setIsAsyncBacktest] = useState(false);
  const [backtestHistory, setBacktestHistory] = useState<BacktestRow[]>([]);
  const [showHistoryModal, setShowHistoryModal] = useState(false);
  const [backtestConfig, setBacktestConfig] = useState({
    start_time: new Date(Date.now() - 7 * 24 * 60 * 60 * 1000).toISOString().split('T')[0],
    end_time: new Date().toISOString().split('T')[0],
    initial_balance: 10000
  });

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

  const getAxiosErrorMessage = (err: unknown) => {
    if (!axios.isAxiosError(err)) return null;
    const data = err.response?.data;
    if (isObject(data) && typeof data.error === 'string') return data.error;
    return null;
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
    let pnlTimer: number | undefined;
    if (user && token) {
      fetchStrategies();
      fetchTemplates();
      fetchPositions(positionStatus);
      fetchPnLSummary();
      if (user.role === 'admin') fetchUsers();
      connectWS();
      pnlTimer = window.setInterval(() => {
        fetchPnLSummary();
      }, 5000);
    }
    return () => {
      if (ws.current) ws.current.close();
      if (pnlTimer) window.clearInterval(pnlTimer);
    };
  }, [user, token, positionStatus]);

  useEffect(() => {
    let posTimer: number | undefined;
    if (user && token && activeTab === 'positions' && positionStatus === 'active') {
      fetchPositions('active');
      posTimer = window.setInterval(() => {
        fetchPositions('active');
      }, 3000);
    }
    return () => {
      if (posTimer) window.clearInterval(posTimer);
    };
  }, [user, token, activeTab, positionStatus]);

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
        config: JSON.stringify(newStratConfig)
      });
      fetchStrategies();
      setShowEditConfigModal(false);
      setStrategyToEdit(null);
      showToast('配置更新成功', 'success');
    } catch (err: unknown) {
      showToast(getAxiosErrorMessage(err) || '更新失败', 'error');
    }
  };

  const runBacktest = async () => {
    if (!strategyToBacktest) return;
    setIsBacktesting(true);
    setBacktestResult(null);
    try {
      const res = await axios.post(`/api/strategies/${strategyToBacktest.id}/backtest${isAsyncBacktest ? '?async=true' : ''}`, {
        start_time: new Date(backtestConfig.start_time).toISOString(),
        end_time: new Date(backtestConfig.end_time).toISOString(),
        initial_balance: backtestConfig.initial_balance
      });
      if (isAsyncBacktest) {
        showToast('回测任务已加入后台队列', 'info');
        setShowBacktestModal(false);
      } else {
        setBacktestResult(res.data as BacktestResult);
        showToast('回测完成', 'success');
      }
    } catch (err: unknown) {
      showToast(getAxiosErrorMessage(err) || '回测失败', 'error');
    } finally {
      setIsBacktesting(false);
    }
  };

  const fetchBacktestHistory = async (strategyId?: string) => {
    try {
      const res = await axios.get(`/api/backtests${strategyId ? `?strategy_id=${strategyId}` : ''}`);
      setBacktestHistory(Array.isArray(res.data) ? (res.data as BacktestRow[]) : []);
    } catch (err) {
      console.error('Failed to fetch backtest history', err);
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
    } catch (err: unknown) {
      showToast(getAxiosErrorMessage(err) || '保存失败', 'error');
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

  const fetchPnLSummary = async () => {
    try {
      const res = await axios.get('/api/stats/pnl');
      setPnlSummary(res.data);
    } catch (err) {
      console.error('Failed to fetch pnl summary', err);
    }
  };

  const closePosition = async (symbol: string) => {
    customConfirm('手动平仓', `确定要手动平仓 ${symbol} 吗？此操作将立即在交易所下单。`, async () => {
      try {
        await axios.post(`/api/positions/close?symbol=${symbol}`);
        showToast(`已请求平仓 ${symbol}`, 'success');
        fetchPositions(positionStatus);
        fetchPnLSummary();
      } catch (err: unknown) {
        showToast(getAxiosErrorMessage(err) || '平仓失败', 'error');
      }
    });
  };


  const connectWS = () => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws.current = new WebSocket(`${protocol}//${window.location.host}/ws`);
    ws.current.onmessage = (event) => {
      const parsed = JSON.parse(event.data) as unknown;
      if (!isObject(parsed)) return;
      const type = parsed.type;
      if (typeof type !== 'string') return;

      if (type === 'order') {
        fetchPositions(positionStatus); // Refresh positions on order
        fetchPnLSummary();
      } else if (type === 'log') {
        const data = parsed.data;
        setLogs(prev => [`[${new Date().toLocaleTimeString()}] ${typeof data === 'string' ? data : ''}`, ...prev.slice(0, 99)]);
      } else if (type === 'execution_report') {
        const d = isObject(parsed.data) ? parsed.data : {};
        const symbol = typeof d.symbol === 'string' ? d.symbol : '';
        const side = typeof d.side === 'string' ? d.side : '';
        const status = typeof d.status === 'string' ? d.status : '';
        const executedQty = typeof d.executed_qty === 'number' ? d.executed_qty : '';
        const lastPrice = typeof d.last_price === 'number' ? d.last_price : '';
        setLogs(prev => [`[${new Date().toLocaleTimeString()}] EXEC ${symbol} ${side} ${status} qty=${executedQty} price=${lastPrice}`, ...prev.slice(0, 99)]);
        fetchPositions(positionStatus);
        fetchPnLSummary();
      } else if (type === 'backtest_progress') {
        const msgUserID = parsed.user_id;
        if (user && typeof msgUserID === 'number' && msgUserID === user.id) {
          const equity = typeof parsed.equity === 'number' ? parsed.equity.toFixed(2) : '';
          const trades = typeof parsed.trades === 'number' ? parsed.trades : '';
          setLogs(prev => [`[${new Date().toLocaleTimeString()}] Backtest progress equity=${equity} trades=${trades}`, ...prev.slice(0, 99)]);
        }
      } else if (type === 'backtest_status') {
        const msgUserID = parsed.user_id;
        if (user && typeof msgUserID === 'number' && msgUserID === user.id) {
          if (parsed.status === 'completed') {
            showToast('回测完成', 'success');
            const strategyID = parsed.strategy_id;
            const result = parsed.result;
            if (typeof strategyID === 'string' && strategyToBacktest?.id === strategyID && isObject(result)) {
              setBacktestResult(result as unknown as BacktestResult);
              setShowBacktestModal(true);
            }
            fetchBacktestHistory();
          } else if (parsed.status === 'failed') {
            showToast(typeof parsed.error === 'string' ? parsed.error : '回测失败', 'error');
            fetchBacktestHistory();
          }
        }
      } else if (type === 'candle') {
        const msgOwnerID = parsed.owner_id;
        if (user && typeof msgOwnerID === 'number' && msgOwnerID === user.id) {
          const c = isObject(parsed.data) ? parsed.data : {};
          const ts = c.timestamp ? new Date(String(c.timestamp)).toLocaleString() : '';
          const close = typeof c.close === 'number' ? c.close : '';
          setLogs(prev => [`[${new Date().toLocaleTimeString()}] Candle ${ts} close=${close}`, ...prev.slice(0, 99)]);
        }
      } else if (type === 'position') {
        const d = isObject(parsed.data) ? parsed.data : {};
        const sym = typeof d.symbol === 'string' ? d.symbol : '';
        const amt = typeof d.amount === 'number' ? d.amount : '';
        const status = typeof d.status === 'string' ? d.status : '';
        setLogs(prev => [`[${new Date().toLocaleTimeString()}] Position ${sym} amount=${amt} status=${status}`, ...prev.slice(0, 99)]);
        fetchPositions(positionStatus);
        fetchPnLSummary();
      } else if (type === 'error') {
        const msg = typeof parsed.error === 'string' ? parsed.error : '发生错误';
        setLogs(prev => [`[${new Date().toLocaleTimeString()}] ERROR ${msg}`, ...prev.slice(0, 99)]);
        showToast(msg, 'error');
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
    } catch (err: unknown) {
      showToast(getAxiosErrorMessage(err) || '操作失败', 'error');
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
        config: JSON.stringify(newStratConfig)
      });
      fetchStrategies();
      setShowCreateModal(false);
      setNewStratName('');
      setNewStratConfig({
        symbol: 'BTC/USDT',
        leverage: 20,
        fast_window: 10,
        slow_window: 30,
        trade_amount: 0.01,
        take_profit_pct: 0.03,
        stop_loss_pct: 0.01,
        trailing_stop_pct: 0.005,
        max_hold_bars: 0,
        cooldown_bars: 0,
        max_trades_per_day: 3,
        max_concurrent_positions: 1,
        warmup_bars: 100,
        position_pct: 10,
        take_profit: 5,
        stop_loss: 2,
        close_yield: 10,
      });
      setSelectedTemplate(0);
      setActiveTab('strategies');
      showToast('策略创建成功', 'success');
    } catch (err: unknown) {
      showToast(getAxiosErrorMessage(err) || '创建失败', 'error');
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
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'templates'} onClick={() => { setActiveTab('templates'); setIsSidebarOpen(false); }} icon={<List size={20} />} label="模板列表" />
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'develop'} onClick={() => { setActiveTab('develop'); setIsSidebarOpen(false); }} icon={<Code size={20} />} label="代码开发" />
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'square'} onClick={() => { setActiveTab('square'); setIsSidebarOpen(false); }} icon={<ShoppingBag size={20} />} label="策略广场" />
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'positions'} onClick={() => { setActiveTab('positions'); setIsSidebarOpen(false); }} icon={<List size={20} />} label="仓位管理" />
          <NavItem isDarkMode={isDarkMode} active={activeTab === 'stats'} onClick={() => { setActiveTab('stats'); setIsSidebarOpen(false); }} icon={<Activity size={20} />} label="统计" />
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
              {activeTab === 'templates' && '模板列表'}
              {activeTab === 'develop' && '策略代码开发'}
              {activeTab === 'square' && '策略广场'}
              {activeTab === 'positions' && '实时持仓'}
              {activeTab === 'stats' && '收益统计'}
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
              onClick={() => { fetchStrategies(); fetchTemplates(); fetchPositions(positionStatus); fetchPnLSummary(); if (user.role === 'admin') fetchUsers(); }} 
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
                   <PlusCircle size={18} /> 保存到我的模版
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
              onClick={() => {
                setSelectedTemplate(0);
                setNewStratName('');
                setNewStratConfig({
                  symbol: 'BTC/USDT',
                  leverage: 20,
                  fast_window: 10,
                  slow_window: 30,
                  trade_amount: 0.01,
                  take_profit_pct: 0.03,
                  stop_loss_pct: 0.01,
                  trailing_stop_pct: 0.005,
                  max_hold_bars: 0,
                  cooldown_bars: 0,
                  max_trades_per_day: 3,
                  max_concurrent_positions: 1,
                  warmup_bars: 100,
                  position_pct: 10,
                  take_profit: 5,
                  stop_loss: 2,
                  close_yield: 10,
                });
                setShowCreateModal(true);
              }}
              className={`p-6 rounded-2xl border-2 border-dashed flex flex-col items-center justify-center transition cursor-pointer group order-first ${isDarkMode ? 'bg-gray-900/30 border-gray-800 text-gray-600 hover:border-gray-700 hover:text-gray-400' : 'bg-white border-gray-200 text-gray-400 hover:border-blue-200 hover:text-blue-400'}`}
              title="新建策略：从已启用的模板中选择并部署一个新的交易实例"
            >
              <PlusCircle size={40} className="mb-2 group-hover:scale-110 transition" />
              <p className="font-bold">新建策略</p>
            </div>

            {/* Combined List: Instances */}
            {strategies.map(s => ({ ...s, type: 'instance' as const }))
            .filter(item => {
              const search = stratSearch.toLowerCase();
              return item.name.toLowerCase().includes(search) || JSON.stringify(item.config).toLowerCase().includes(search);
            })
            .sort((a, b) => {
              const idA = typeof a.id === 'string' ? parseInt(a.id) || 0 : a.id;
              const idB = typeof b.id === 'string' ? parseInt(b.id) || 0 : b.id;
              return idB - idA;
            })
            .map(s => (
              <div key={`instance-${s.id}`} className={`p-6 rounded-2xl border shadow-xl transition ${isDarkMode ? 'bg-gray-900/50 border-gray-800 hover:border-gray-700' : 'bg-white border-gray-200 hover:border-blue-200'}`}>
                <div className="flex justify-between items-start mb-4">
                  <h3 className="text-lg font-bold">{s.name}</h3>
                  <span className={`px-3 py-1 rounded-full text-[10px] font-black uppercase tracking-wider ${s.status === 'running' ? 'bg-green-900/30 text-green-400' : 'bg-red-900/30 text-red-400'}`}>
                    {s.status}
                  </span>
                </div>
                <div className="text-sm text-gray-500 mb-8 space-y-1">
                  <p className="flex justify-between"><span>交易对</span> <span className={isDarkMode ? 'text-gray-300' : 'text-gray-700'}>{getCfgString(s.config, 'symbol', '')}</span></p>
                  <p className="flex justify-between"><span>均线</span> <span className={isDarkMode ? 'text-gray-300' : 'text-gray-700'}>{getCfgNumber(s.config, 'fast_window', 0)} / {getCfgNumber(s.config, 'slow_window', 0)}</span></p>
                  <p className="flex justify-between"><span>下单数量</span> <span className={isDarkMode ? 'text-gray-300' : 'text-gray-700'}>{getCfgNumber(s.config, 'trade_amount', 0)}</span></p>
                  <p className="flex justify-between"><span>止盈/止损/追踪</span> <span className={isDarkMode ? 'text-gray-300' : 'text-gray-700'}>{getCfgNumber(s.config, 'take_profit_pct', 0)} / {getCfgNumber(s.config, 'stop_loss_pct', 0)} / {getCfgNumber(s.config, 'trailing_stop_pct', 0)}</span></p>
                  <p className="flex justify-between"><span>最大并发仓位</span> <span className={isDarkMode ? 'text-gray-300' : 'text-gray-700'}>{getCfgNumber(s.config, 'max_concurrent_positions', 1)}</span></p>
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
                          setStrategyToBacktest(s);
                          setBacktestResult(null);
                          setShowBacktestModal(true);
                        }}
                        className={`p-2.5 rounded-xl transition border text-green-400 ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 border-gray-700' : 'bg-white hover:bg-gray-50 border-gray-200'}`}
                        title="回测：模拟历史数据运行并计算盈亏"
                      >
                        <Activity size={20} />
                      </button>
                      <button
                        onClick={() => { 
                          if (s.status === 'running') {
                            showToast('策略正在运行中，无法修改配置。请先停止策略。', 'warning');
                            return;
                          }
                          const hasActivePositions = positions.some(p => p.strategy_name === s.name && p.status === 'active');
                          if (hasActivePositions) {
                            showToast('该策略尚有未平仓位，请先在“仓位管理”中手动平仓后再修改配置。', 'warning');
                            return;
                          }

                          setStrategyToEdit(s); 
                          setNewStratConfig({
                            symbol: getCfgString(s.config, 'symbol', 'BTC/USDT'),
                            leverage: getCfgNumber(s.config, 'leverage', 20),
                            fast_window: getCfgNumber(s.config, 'fast_window', 10),
                            slow_window: getCfgNumber(s.config, 'slow_window', 30),
                            trade_amount: getCfgNumber(s.config, 'trade_amount', 0.01),
                            take_profit_pct: getCfgNumber(s.config, 'take_profit_pct', 0.03),
                            stop_loss_pct: getCfgNumber(s.config, 'stop_loss_pct', 0.01),
                            trailing_stop_pct: getCfgNumber(s.config, 'trailing_stop_pct', 0.005),
                            max_hold_bars: getCfgNumber(s.config, 'max_hold_bars', 0),
                            cooldown_bars: getCfgNumber(s.config, 'cooldown_bars', 0),
                            max_trades_per_day: getCfgNumber(s.config, 'max_trades_per_day', 3),
                            max_concurrent_positions: getCfgNumber(s.config, 'max_concurrent_positions', 1),
                            warmup_bars: getCfgNumber(s.config, 'warmup_bars', 100),
                            position_pct: getCfgNumber(s.config, 'position_pct', 10),
                            take_profit: getCfgNumber(s.config, 'take_profit', 5),
                            stop_loss: getCfgNumber(s.config, 'stop_loss', 2),
                            close_yield: getCfgNumber(s.config, 'close_yield', 10),
                          });
                          setShowEditConfigModal(true); 
                        }}
                        className={`p-2.5 rounded-xl transition border ${s.status === 'running' ? 'opacity-50 cursor-not-allowed text-gray-600' : 'text-gray-400 hover:bg-gray-50 border-gray-200'} ${isDarkMode ? (s.status === 'running' ? 'bg-gray-900 border-gray-800' : 'bg-gray-800 hover:bg-gray-700 border-gray-700') : ''}`}
                        title={s.status === 'running' ? "运行中的策略无法修改配置" : "配置参数：修改交易对、仓位比例等运行参数"}
                      >
                        <Settings size={20} />
                      </button>
                  <button
                    onClick={() => { 
                      const hasActivePositions = positions.some(p => p.strategy_name === s.name && p.status === 'active');
                      if (hasActivePositions) {
                        showToast('该策略尚有未平仓位，请先在“仓位管理”中手动平仓后再删除。', 'warning');
                        return;
                      }
                      setStrategyToDelete(s); 
                      setShowDeleteConfirm(true); 
                    }}
                    className={`p-2.5 rounded-xl transition border text-red-400 ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 border-gray-700' : 'bg-white hover:bg-gray-50 border-gray-200'}`}
                    title="删除策略：停止运行并永久删除此策略实例"
                  >
                    <Trash2 size={20} />
                  </button>
                </div>
              </div>
            ))}
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
                  (t.author_id === user?.id || user?.role === 'admin') && 
                  !t.is_draft &&
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
                        {t.is_public && (
                          <span className="px-2 py-0.5 rounded-full text-[10px] font-black uppercase tracking-wider bg-purple-900/30 text-purple-400 w-fit">
                            已发布
                          </span>
                        )}
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
                {/* Deployment button removed as per user instruction. All deployments should happen in My Strategies. */}
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
                    <th className="px-4 md:px-6 py-4">最新价</th>
                    <th className="px-4 md:px-6 py-4">未实现盈亏</th>
                    <th className="px-4 md:px-6 py-4">回报率</th>
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
                      <td className="px-4 md:px-6 py-4 font-mono">
                        {typeof p.current_price === 'number' && p.current_price > 0 ? `$${p.current_price.toLocaleString(undefined, { minimumFractionDigits: 4 })}` : '--'}
                      </td>
                      <td className={`px-4 md:px-6 py-4 font-mono ${typeof p.unrealized_pnl === 'number' ? (p.unrealized_pnl >= 0 ? 'text-green-500' : 'text-red-500') : ''}`}>
                        {typeof p.unrealized_pnl === 'number' ? `$${p.unrealized_pnl.toFixed(2)}` : '--'}
                      </td>
                      <td className={`px-4 md:px-6 py-4 font-mono ${typeof p.return_rate === 'number' ? (p.return_rate >= 0 ? 'text-green-500' : 'text-red-500') : ''}`}>
                        {typeof p.return_rate === 'number' ? `${p.return_rate.toFixed(2)}%` : '--'}
                      </td>
                      <td className="px-4 md:px-6 py-4 text-xs text-gray-500 font-mono">
                        {new Date(positionStatus === 'active' ? p.open_time : (p.close_time || '')).toLocaleString()}
                      </td>
                      <td className="px-4 md:px-6 py-4">
                        <span className={`px-2 py-1 rounded text-[10px] font-black uppercase ${p.status === 'active' ? 'bg-green-900/30 text-green-500' : 'bg-gray-800 text-gray-400'}`}>
                          {p.status}
                        </span>
                      </td>
                      <td className="px-4 md:px-6 py-4 text-right">
                        <div className="flex justify-end gap-2">
                          <button 
                            onClick={() => { setSelectedPosition(p); setShowPositionDetailModal(true); }}
                            className={`p-2 rounded-lg transition ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 text-blue-400' : 'bg-gray-100 hover:bg-gray-200 text-blue-600'}`}
                            title="查看详情：打开模态框查看此持仓的完整入场、价值及时间明细"
                          >
                            <Info size={18} />
                          </button>
                          {p.status === 'active' && (
                            <button 
                              onClick={() => closePosition(p.symbol)}
                              className={`p-2 rounded-lg transition ${isDarkMode ? 'bg-red-900/20 hover:bg-red-900/40 text-red-400' : 'bg-red-50 hover:bg-red-100 text-red-600'}`}
                              title="手动平仓：立即在交易所下对冲单平掉此持仓"
                            >
                              <Square size={18} />
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {activeTab === 'stats' && (
          <div className="space-y-6">
            <div className="flex items-center justify-between">
              <div className="text-sm text-gray-500">
                更新时间：{pnlSummary ? new Date(pnlSummary.updated_at).toLocaleString() : '--'}
              </div>
              <button
                onClick={fetchPnLSummary}
                className={`px-4 py-2 rounded-xl font-bold transition shadow-sm border ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 border-gray-700' : 'bg-white hover:bg-gray-50 border-gray-200'}`}
              >
                刷新统计
              </button>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              {(['day', 'week', 'month'] as const).map((k) => {
                const p = pnlSummary ? pnlSummary[k] : null;
                const title = k === 'day' ? '今日统计' : k === 'week' ? '本周统计' : '本月统计';
                const total = p ? p.total_pnl : 0;
                return (
                  <div key={k} className={`p-5 rounded-2xl border shadow-xl ${isDarkMode ? 'bg-gray-900 border-gray-800' : 'bg-white border-gray-200'}`}>
                    <div className="flex justify-between items-center mb-3">
                      <div className="font-bold">{title}</div>
                      <div className={`font-mono font-bold ${total >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                        {p ? `$${total.toFixed(2)}` : '--'}
                      </div>
                    </div>
                    <div className="grid grid-cols-2 gap-3 text-sm">
                      <div className="text-gray-500">总收入</div>
                      <div className="font-mono">{p ? `$${p.gross_profit.toFixed(2)}` : '--'}</div>
                      <div className="text-gray-500">总盈亏</div>
                      <div className={`font-mono ${p && p.realized_pnl >= 0 ? 'text-green-500' : 'text-red-500'}`}>{p ? `$${p.realized_pnl.toFixed(2)}` : '--'}</div>
                      <div className="text-gray-500">未实现</div>
                      <div className={`font-mono ${p && p.unrealized_pnl >= 0 ? 'text-green-500' : 'text-red-500'}`}>{p ? `$${p.unrealized_pnl.toFixed(2)}` : '--'}</div>
                      <div className="text-gray-500">回报率</div>
                      <div className={`font-mono ${p && p.realized_return_pct >= 0 ? 'text-green-500' : 'text-red-500'}`}>{p ? `${p.realized_return_pct.toFixed(2)}%` : '--'}</div>
                    </div>
                  </div>
                );
              })}
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
          <div className={`w-full max-w-xl p-8 rounded-2xl shadow-2xl ${isDarkMode ? 'bg-gray-900 border border-gray-800' : 'bg-white border border-gray-200'}`}>
            <h3 className="text-2xl font-bold mb-6">新建交易策略</h3>
            <div className="space-y-4 mb-8">
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
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
                  <label className="block text-sm font-medium text-gray-500 mb-2">交易对</label>
                  <input 
                    type="text" 
                    value={newStratConfig.symbol}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, symbol: e.target.value })}
                    placeholder="例如: BTC/USDT"
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">快线窗口</label>
                  <input
                    type="number"
                    value={newStratConfig.fast_window}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, fast_window: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">慢线窗口</label>
                  <input
                    type="number"
                    value={newStratConfig.slow_window}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, slow_window: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">下单数量</label>
                  <input
                    type="number"
                    value={newStratConfig.trade_amount}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, trade_amount: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">预热 K 线</label>
                  <input
                    type="number"
                    value={newStratConfig.warmup_bars}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, warmup_bars: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">止盈 (0-1)</label>
                  <input
                    type="number"
                    value={newStratConfig.take_profit_pct}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, take_profit_pct: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">止损 (0-1)</label>
                  <input
                    type="number"
                    value={newStratConfig.stop_loss_pct}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, stop_loss_pct: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">追踪止损 (0-1)</label>
                  <input
                    type="number"
                    value={newStratConfig.trailing_stop_pct}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, trailing_stop_pct: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">最大并发仓位</label>
                  <input
                    type="number"
                    value={newStratConfig.max_concurrent_positions}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, max_concurrent_positions: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">最多持仓根数</label>
                  <input
                    type="number"
                    value={newStratConfig.max_hold_bars}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, max_hold_bars: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">冷却根数</label>
                  <input
                    type="number"
                    value={newStratConfig.cooldown_bars}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, cooldown_bars: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
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
          <div className={`w-full max-w-xl p-8 rounded-2xl shadow-2xl ${isDarkMode ? 'bg-gray-900 border border-gray-800' : 'bg-white border border-gray-200'}`}>
            <div className="flex items-center gap-3 text-gray-400 mb-6">
              <Settings size={28} />
              <h3 className="text-2xl font-bold">编辑策略配置</h3>
            </div>
            <div className="space-y-4 mb-8">
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">交易对</label>
                  <input
                    type="text"
                    value={newStratConfig.symbol}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, symbol: e.target.value })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">下单数量</label>
                  <input
                    type="number"
                    value={newStratConfig.trade_amount}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, trade_amount: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">快线窗口</label>
                  <input
                    type="number"
                    value={newStratConfig.fast_window}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, fast_window: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">慢线窗口</label>
                  <input
                    type="number"
                    value={newStratConfig.slow_window}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, slow_window: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">杠杆</label>
                  <input
                    type="number"
                    value={newStratConfig.leverage}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, leverage: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">预热 K 线</label>
                  <input
                    type="number"
                    value={newStratConfig.warmup_bars}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, warmup_bars: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">最大并发仓位</label>
                  <input
                    type="number"
                    value={newStratConfig.max_concurrent_positions}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, max_concurrent_positions: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">止盈 (0-1)</label>
                  <input
                    type="number"
                    value={newStratConfig.take_profit_pct}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, take_profit_pct: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">止损 (0-1)</label>
                  <input
                    type="number"
                    value={newStratConfig.stop_loss_pct}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, stop_loss_pct: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">追踪止损 (0-1)</label>
                  <input
                    type="number"
                    value={newStratConfig.trailing_stop_pct}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, trailing_stop_pct: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">最多持仓根数</label>
                  <input
                    type="number"
                    value={newStratConfig.max_hold_bars}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, max_hold_bars: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">冷却根数</label>
                  <input
                    type="number"
                    value={newStratConfig.cooldown_bars}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, cooldown_bars: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-500 mb-2">每日最多交易</label>
                  <input
                    type="number"
                    value={newStratConfig.max_trades_per_day}
                    onChange={(e) => setNewStratConfig({ ...newStratConfig, max_trades_per_day: Number(e.target.value) })}
                    className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                  />
                </div>
              </div>
              <p className="mt-2 text-xs text-gray-500 italic">提示: 只有在策略停止状态下修改配置才会生效。</p>
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

      {/* Backtest History Modal */}
      {showHistoryModal && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-[60] p-4">
          <div className={`w-full max-w-5xl p-8 rounded-2xl shadow-2xl overflow-y-auto max-h-[90vh] ${isDarkMode ? 'bg-gray-900 border border-gray-800' : 'bg-white border border-gray-200'}`}>
            <div className="flex items-center justify-between mb-8">
              <div className="flex items-center gap-3 text-purple-500">
                <List size={28} />
                <h3 className="text-2xl font-bold">回测历史记录</h3>
              </div>
              <button 
                onClick={() => setShowHistoryModal(false)}
                className={`p-2 rounded-lg transition ${isDarkMode ? 'hover:bg-gray-800' : 'hover:bg-gray-100'}`}
              >
                <X size={24} />
              </button>
            </div>

            <div className="space-y-4">
              {backtestHistory.length === 0 ? (
                <div className="text-center py-12 text-gray-500">暂无回测历史</div>
              ) : (
                <div className={`rounded-2xl border overflow-hidden ${isDarkMode ? 'border-gray-800' : 'border-gray-100'}`}>
                  <table className="w-full text-left">
                    <thead className={`text-xs uppercase tracking-wider ${isDarkMode ? 'bg-gray-800/50 text-gray-400' : 'bg-gray-50 text-gray-500'}`}>
                      <tr>
                        <th className="px-6 py-4">时间范围</th>
                        <th className="px-6 py-4">状态</th>
                        <th className="px-6 py-4">盈亏 / 收益率</th>
                        <th className="px-6 py-4">交易次数</th>
                        <th className="px-6 py-4 text-right">操作</th>
                      </tr>
                    </thead>
                    <tbody className={`divide-y ${isDarkMode ? 'divide-gray-800' : 'divide-gray-100'}`}>
                      {backtestHistory.map((bt) => (
                        <tr key={bt.id} className={`transition ${isDarkMode ? 'hover:bg-gray-800/30' : 'hover:bg-gray-50'}`}>
                          <td className="px-6 py-4 text-sm">
                            <div className="font-medium">{new Date(bt.start_time).toLocaleDateString()} - {new Date(bt.end_time).toLocaleDateString()}</div>
                            <div className="text-[10px] text-gray-500">创建于 {new Date(bt.created_at).toLocaleString()}</div>
                          </td>
                          <td className="px-6 py-4">
                            <span className={`px-2 py-0.5 rounded text-[10px] font-black uppercase ${
                              bt.status === 'completed' ? 'bg-green-900/30 text-green-400' :
                              bt.status === 'running' ? 'bg-blue-900/30 text-blue-400' :
                              bt.status === 'failed' ? 'bg-red-900/30 text-red-400' : 'bg-gray-800 text-gray-400'
                            }`}>
                              {bt.status}
                            </span>
                          </td>
                          <td className="px-6 py-4 font-mono text-sm">
                            {bt.status === 'completed' ? (
                              <div className={bt.total_profit >= 0 ? 'text-green-500' : 'text-red-500'}>
                                ${bt.total_profit.toLocaleString()} ({bt.return_rate.toFixed(2)}%)
                              </div>
                            ) : '-'}
                          </td>
                          <td className="px-6 py-4 text-sm font-mono">{bt.status === 'completed' ? bt.total_trades : '-'}</td>
                          <td className="px-6 py-4 text-right">
                            {bt.status === 'completed' && (
                              <button 
                                onClick={() => {
                                  const raw = bt.result;
                                  if (typeof raw !== 'string') return;
                                  const result = JSON.parse(raw) as BacktestResult;
                                  setBacktestResult(result);
                                  setShowHistoryModal(false);
                                }}
                                className={`p-2 rounded-lg transition ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700 text-blue-400' : 'bg-gray-100 hover:bg-gray-200 text-blue-600'}`}
                                title="查看详细结果"
                              >
                                <Activity size={18} />
                              </button>
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      {/* Backtest Modal */}
      {showBacktestModal && strategyToBacktest && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
          <div className={`w-full max-w-4xl p-8 rounded-2xl shadow-2xl overflow-y-auto max-h-[90vh] ${isDarkMode ? 'bg-gray-900 border border-gray-800' : 'bg-white border border-gray-200'}`}>
            <div className="flex items-center justify-between mb-8">
              <div className="flex items-center gap-3 text-green-500">
                <Activity size={28} />
                <h3 className="text-2xl font-bold">策略回测: {strategyToBacktest.name}</h3>
              </div>
              <button 
                onClick={() => { setShowBacktestModal(false); setBacktestResult(null); }}
                className={`p-2 rounded-lg transition ${isDarkMode ? 'hover:bg-gray-800' : 'hover:bg-gray-100'}`}
              >
                <X size={24} />
              </button>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-3 gap-6 mb-8">
              <div>
                <label className="block text-sm font-medium text-gray-500 mb-2">开始时间</label>
                <input 
                  type="date" 
                  value={backtestConfig.start_time}
                  onChange={(e) => setBacktestConfig({ ...backtestConfig, start_time: e.target.value })}
                  className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-500 mb-2">结束时间</label>
                <input 
                  type="date" 
                  value={backtestConfig.end_time}
                  onChange={(e) => setBacktestConfig({ ...backtestConfig, end_time: e.target.value })}
                  className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-500 mb-2">初始本金 (USDT)</label>
                <input 
                  type="number" 
                  value={backtestConfig.initial_balance}
                  onChange={(e) => setBacktestConfig({ ...backtestConfig, initial_balance: Number(e.target.value) })}
                  className={`w-full px-4 py-2.5 rounded-xl border transition focus:ring-2 focus:ring-blue-500 outline-none ${isDarkMode ? 'bg-gray-800 border-gray-700 text-white' : 'bg-gray-50 border-gray-200'}`}
                />
              </div>
            </div>

            <div className="flex gap-4 mb-8">
              <button 
                onClick={runBacktest}
                disabled={isBacktesting}
                className="flex-[2] py-3 bg-green-600 hover:bg-green-700 text-white rounded-xl font-bold transition shadow-lg shadow-green-900/20 disabled:opacity-50 flex items-center justify-center gap-2"
              >
                {isBacktesting ? <><RefreshCw size={20} className="animate-spin" /> 回测运行中...</> : '开始回测'}
              </button>
              <button 
                onClick={() => {
                  fetchBacktestHistory(strategyToBacktest.id);
                  setShowHistoryModal(true);
                }}
                className={`flex-1 py-3 rounded-xl font-bold transition flex items-center justify-center gap-2 ${isDarkMode ? 'bg-gray-800 hover:bg-gray-700' : 'bg-gray-100 hover:bg-gray-200'}`}
              >
                <List size={20} /> 历史记录
              </button>
            </div>

            <div className="flex items-center gap-3 mb-8">
              <input 
                type="checkbox" 
                id="asyncBacktest" 
                checked={isAsyncBacktest}
                onChange={(e) => setIsAsyncBacktest(e.target.checked)}
                className="w-5 h-5 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
              />
              <label htmlFor="asyncBacktest" className={`text-sm font-medium ${isDarkMode ? 'text-gray-300' : 'text-gray-700'}`}>
                后台运行 (任务将在完成后自动保存，您可以随时关闭此窗口)
              </label>
            </div>

            {backtestResult && (
              <div className="space-y-8 animate-in fade-in slide-in-from-bottom-4 duration-500">
                <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                  <div className={`p-4 rounded-xl border ${isDarkMode ? 'bg-black/20 border-gray-800' : 'bg-gray-50 border-gray-100'}`}>
                    <p className="text-xs text-gray-500 uppercase font-bold mb-1">总交易次数</p>
                    <p className="text-2xl font-mono font-bold">{backtestResult.total_trades}</p>
                  </div>
                  <div className={`p-4 rounded-xl border ${isDarkMode ? 'bg-black/20 border-gray-800' : 'bg-gray-50 border-gray-100'}`}>
                    <p className="text-xs text-gray-500 uppercase font-bold mb-1">净利润</p>
                    <p className={`text-2xl font-mono font-bold ${backtestResult.total_profit >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                      ${backtestResult.total_profit.toLocaleString(undefined, { minimumFractionDigits: 2 })}
                    </p>
                  </div>
                  <div className={`p-4 rounded-xl border ${isDarkMode ? 'bg-black/20 border-gray-800' : 'bg-gray-50 border-gray-100'}`}>
                    <p className="text-xs text-gray-500 uppercase font-bold mb-1">收益率</p>
                    <p className={`text-2xl font-mono font-bold ${backtestResult.return_rate >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                      {backtestResult.return_rate.toFixed(2)}%
                    </p>
                  </div>
                  <div className={`p-4 rounded-xl border ${isDarkMode ? 'bg-black/20 border-gray-800' : 'bg-gray-50 border-gray-100'}`}>
                    <p className="text-xs text-gray-500 uppercase font-bold mb-1">最终余额</p>
                    <p className="text-2xl font-mono font-bold">${backtestResult.final_balance.toLocaleString(undefined, { minimumFractionDigits: 2 })}</p>
                  </div>
                </div>

                <div className={`p-6 rounded-2xl border ${isDarkMode ? 'bg-black/40 border-gray-800' : 'bg-gray-50 border-gray-200'}`}>
                  <h4 className="text-sm font-bold text-gray-500 uppercase mb-6">资产净值曲线 (Equity Curve)</h4>
                  <div className="h-64 w-full relative">
                    {/* Simple SVG Line Chart */}
                    <svg className="w-full h-full" preserveAspectRatio="none">
                      <polyline
                        fill="none"
                        stroke="#3b82f6"
                        strokeWidth="2"
                        points={backtestResult.equity_curve.map((p: EquityPoint, i: number) => {
                          const denom = Math.max(backtestResult.equity_curve.length - 1, 1);
                          const x = (i / denom) * 100;
                          const minEquity = Math.min(...backtestResult.equity_curve.map((ep: EquityPoint) => ep.equity));
                          const maxEquity = Math.max(...backtestResult.equity_curve.map((ep: EquityPoint) => ep.equity));
                          const range = maxEquity - minEquity || 1;
                          const y = 100 - ((p.equity - minEquity) / range) * 100;
                          return `${x},${y}`;
                        }).join(' ')}
                        style={{ vectorEffect: 'non-scaling-stroke' }}
                      />
                    </svg>
                    <div className="flex justify-between mt-2 text-[10px] text-gray-500 font-mono">
                      <span>{new Date(backtestResult.equity_curve[0].timestamp).toLocaleDateString()}</span>
                      <span>{new Date(backtestResult.equity_curve[backtestResult.equity_curve.length - 1].timestamp).toLocaleDateString()}</span>
                    </div>
                  </div>
                </div>
              </div>
            )}
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
