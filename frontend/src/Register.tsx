import React, { useState } from 'react';
import axios from 'axios';
import { Lock, User, Activity, Key, Shield } from 'lucide-react';

interface RegisterProps {
  onBackToLogin: () => void;
}

const Register: React.FC<RegisterProps> = ({ onBackToLogin }) => {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [apiSecret, setApiSecret] = useState('');
  const [error, setError] = useState('');
  const [success, setSuccess] = useState(false);
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    try {
      await axios.post('/api/register', {
        username,
        password,
        configs: {
          binance: {
            apiKey,
            apiSecret
          }
        }
      });
      setSuccess(true);
      setTimeout(onBackToLogin, 2000);
    } catch (err: any) {
      setError(err.response?.data?.error || '注册失败，请稍后重试');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen bg-gray-950 flex items-center justify-center p-4">
      <div className="max-w-md w-full bg-gray-900 rounded-2xl shadow-2xl border border-gray-800 p-8">
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center p-3 bg-blue-600 rounded-xl mb-4">
            <Activity className="text-white" size={32} />
          </div>
          <h2 className="text-3xl font-bold text-white">创建账户</h2>
          <p className="text-gray-400 mt-2">加入 QuantyTrade 并配置交易所 API</p>
        </div>

        {error && (
          <div className="bg-red-900/50 border border-red-500 text-red-200 px-4 py-3 rounded-lg mb-6 text-sm">
            {error}
          </div>
        )}

        {success && (
          <div className="bg-green-900/50 border border-green-500 text-green-200 px-4 py-3 rounded-lg mb-6 text-sm text-center">
            注册成功！即将跳转到登录页面...
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-500 mb-2">用户名</label>
            <div className="relative">
              <div className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-gray-600">
                <User size={18} />
              </div>
              <input
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className="block w-full pl-10 pr-3 py-2.5 bg-gray-800 border border-gray-700 rounded-xl text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition"
                placeholder="请输入用户名"
                required
              />
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-500 mb-2">密码</label>
            <div className="relative">
              <div className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-gray-600">
                <Lock size={18} />
              </div>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="block w-full pl-10 pr-3 py-2.5 bg-gray-800 border border-gray-700 rounded-xl text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition"
                placeholder="请输入密码"
                required
              />
            </div>
          </div>

          <div className="pt-4 border-t border-gray-800 mt-4">
            <h3 className="text-sm font-bold text-blue-500 mb-4 flex items-center gap-2">
              <Shield size={16} /> Binance 交易所配置 (必填)
            </h3>
            
            <div className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-gray-500 mb-2">API Key</label>
                <div className="relative">
                  <div className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-gray-600">
                    <Key size={18} />
                  </div>
                  <input
                    type="text"
                    value={apiKey}
                    onChange={(e) => setApiKey(e.target.value)}
                    className="block w-full pl-10 pr-3 py-2.5 bg-gray-800 border border-gray-700 rounded-xl text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition"
                    placeholder="请输入 API Key"
                    required
                  />
                </div>
              </div>

              <div>
                <label className="block text-sm font-medium text-gray-500 mb-2">API Secret</label>
                <div className="relative">
                  <div className="absolute inset-y-0 left-0 pl-3 flex items-center pointer-events-none text-gray-600">
                    <Key size={18} />
                  </div>
                  <input
                    type="password"
                    value={apiSecret}
                    onChange={(e) => setApiSecret(e.target.value)}
                    className="block w-full pl-10 pr-3 py-2.5 bg-gray-800 border border-gray-700 rounded-xl text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition"
                    placeholder="请输入 API Secret"
                    required
                  />
                </div>
              </div>
            </div>
          </div>

          <button
            type="submit"
            disabled={loading || success}
            className="w-full py-3.5 px-4 bg-blue-600 hover:bg-blue-700 text-white font-bold rounded-xl shadow-lg shadow-blue-900/20 transition transform active:scale-95 disabled:opacity-50 mt-4"
          >
            {loading ? '正在注册...' : '立即注册'}
          </button>

          <button
            type="button"
            onClick={onBackToLogin}
            className="w-full py-2 text-sm text-gray-500 hover:text-gray-300 transition"
          >
            已有账号？返回登录
          </button>
        </form>
      </div>
    </div>
  );
};

export default Register;
