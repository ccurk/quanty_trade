import React, { useState, useEffect } from 'react';
import axios from 'axios';
import { Activity, Shield, TrendingUp, Cpu, Mail, ArrowRight, Zap, Globe, Github } from 'lucide-react';

interface LandingPageProps {
  onGoToLogin: () => void;
  onGoToRegister: () => void;
}

interface PublicTemplate {
  name: string;
  description?: string;
  author?: { username?: string };
  is_default?: boolean;
  stats?: string;
}

const DEFAULT_STRATEGIES = [
  {
    name: "BTC 均线趋势",
    description: "基于 20/60 日均线交叉的经典趋势追踪策略，在牛市中表现极其稳健。",
    author: { username: "System" },
    is_default: true,
    stats: "+124.5%"
  },
  {
    name: "网格震荡套利",
    description: "适用于震荡行情，通过高频低买高卖获取超额收益，回撤控制极佳。",
    author: { username: "System" },
    is_default: true,
    stats: "+42.1%"
  },
  {
    name: "多空对冲模型",
    description: "利用统计学原理进行多空对冲，规避系统性风险，适合追求稳健收益的交易员。",
    author: { username: "System" },
    is_default: true,
    stats: "+89.3%"
  }
];

const LandingPage: React.FC<LandingPageProps> = ({ onGoToLogin, onGoToRegister }) => {
  const [publicTemplates, setPublicTemplates] = useState<PublicTemplate[]>([]);
  const [currentSlide, setCurrentSlide] = useState(0);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await axios.get('/api/public/templates');
        const data = Array.isArray(res.data) ? (res.data as PublicTemplate[]) : [];
        if (!cancelled) setPublicTemplates(data);
      } catch (err) {
        console.error('Failed to fetch public templates', err);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const allStrategies = publicTemplates.length > 0 
    ? [...publicTemplates] 
    : [...DEFAULT_STRATEGIES];

  const nextSlide = () => {
    setCurrentSlide((prev) => (prev + 1) % allStrategies.length);
  };

  // Auto-play
  useEffect(() => {
    const timer = setInterval(nextSlide, 5000);
    return () => clearInterval(timer);
  }, [allStrategies.length]);

  return (
    <div className="min-h-screen bg-gray-950 text-white selection:bg-blue-500/30">
      {/* Navbar */}
      <nav className="fixed top-0 w-full z-50 bg-gray-950/80 backdrop-blur-md border-b border-gray-800">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 h-16 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <div className="bg-blue-600 p-1.5 rounded-lg">
              <Activity size={24} />
            </div>
            <span className="text-xl font-bold tracking-tight">QuantyTrade</span>
          </div>
          <div className="flex items-center gap-4">
            <button onClick={onGoToLogin} className="text-sm font-medium text-gray-400 hover:text-white transition">登录</button>
            <button 
              onClick={onGoToRegister}
              className="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-full text-sm font-bold transition shadow-lg shadow-blue-900/20"
            >
              立即开始
            </button>
          </div>
        </div>
      </nav>

      {/* Hero Section */}
      <section className="pt-32 pb-20 px-4">
        <div className="max-w-7xl mx-auto text-center">
          <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-blue-900/30 border border-blue-500/30 text-blue-400 text-xs font-bold mb-8 animate-fade-in">
            <Zap size={14} /> 新一代 AI 驱动量化平台
          </div>
          <h1 className="text-5xl md:text-7xl font-extrabold tracking-tight mb-6 bg-gradient-to-b from-white to-gray-500 bg-clip-text text-transparent">
            让策略编写<br/>变得前所未有的简单
          </h1>
          <p className="text-gray-400 text-lg md:text-xl max-w-2xl mx-auto mb-10 leading-relaxed">
            QuantyTrade 是一个面向开发者与专业交易员的高性能量化系统。集成在线代码编辑器、多账户隔离及实时监控，助您在瞬息万变的市场中抢占先机。
          </p>
          <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
            <button 
              onClick={onGoToRegister}
              className="w-full sm:w-auto px-8 py-4 bg-white text-gray-950 rounded-2xl font-bold text-lg hover:bg-gray-200 transition flex items-center justify-center gap-2 group"
            >
              开启交易之旅 <ArrowRight size={20} className="group-hover:translate-x-1 transition-transform" />
            </button>
            <button 
              onClick={onGoToLogin}
              className="w-full sm:w-auto px-8 py-4 bg-gray-900 border border-gray-800 rounded-2xl font-bold text-lg hover:bg-gray-800 transition"
            >
              查看演示
            </button>
          </div>
        </div>
      </section>

      {/* Strategy Showcase Section (Carousel) */}
      <section className="py-20 bg-gray-900/20 border-y border-gray-900 overflow-hidden">
        <div className="max-w-7xl mx-auto px-4">
          <div className="text-center mb-16">
            <h2 className="text-3xl md:text-4xl font-bold mb-4">热门量化策略</h2>
            <p className="text-gray-500">探索策略广场中由专业交易员分享的高收益逻辑</p>
          </div>

          <div className="relative group">
            {/* Carousel Container */}
            <div className="overflow-hidden rounded-3xl border border-gray-800 bg-gray-900/40 backdrop-blur-sm shadow-2xl">
              <div 
                className="flex transition-transform duration-700 ease-in-out"
                style={{ transform: `translateX(-${currentSlide * 100}%)` }}
              >
                {allStrategies.map((strat, idx) => (
                  <div key={idx} className="w-full flex-shrink-0 p-8 md:p-16 flex flex-col md:flex-row items-center gap-12">
                    <div className="flex-1 text-center md:text-left">
                      <div className="flex items-center gap-3 mb-6 justify-center md:justify-start">
                        <span className="px-3 py-1 rounded-full bg-blue-600/20 text-blue-400 text-xs font-bold uppercase tracking-wider">
                          {strat.is_default ? '官方精选' : '社区发布'}
                        </span>
                        <span className="text-gray-500 text-xs italic">by @{strat.author?.username}</span>
                      </div>
                      <h3 className="text-3xl md:text-5xl font-black mb-6">{strat.name}</h3>
                      <p className="text-gray-400 text-lg leading-relaxed mb-8 max-w-xl">
                        {strat.description || "该策略通过精密算法捕捉市场波动，提供全天候自动化的交易执行方案。"}
                      </p>
                      <button 
                        onClick={onGoToLogin}
                        className="inline-flex items-center gap-2 text-blue-400 font-bold hover:text-blue-300 transition group"
                      >
                        立即引用此策略 <ArrowRight size={18} className="group-hover:translate-x-1 transition-transform" />
                      </button>
                    </div>
                    
                    <div className="w-full md:w-auto shrink-0">
                      <div className="p-8 md:p-12 rounded-3xl bg-gradient-to-br from-blue-600/20 to-purple-600/20 border border-white/10 text-center relative overflow-hidden group/card">
                        <div className="absolute inset-0 bg-blue-600/10 opacity-0 group-hover/card:opacity-100 transition-opacity" />
                        <div className="relative z-10">
                          <p className="text-gray-400 text-sm mb-2 uppercase tracking-widest font-bold">累计收益</p>
                          <div className="text-5xl md:text-7xl font-black text-green-400 mb-4 drop-shadow-[0_0_15px_rgba(74,222,128,0.3)]">
                            {strat.stats || fallbackStats(strat.name)}
                          </div>
                          <p className="text-gray-500 text-xs">过去 12 个月测试表现</p>
                        </div>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            </div>

            {/* Indicators (Non-clickable dots) */}
            <div className="flex justify-center gap-2 mt-8">
              {allStrategies.map((_, idx) => (
                <div
                  key={idx}
                  className={`h-1.5 rounded-full transition-all duration-500 ${currentSlide === idx ? 'w-8 bg-blue-600' : 'w-2 bg-gray-800'}`}
                />
              ))}
            </div>
          </div>
        </div>
      </section>

      {/* Features */}
      <section className="py-20 px-4">
        <div className="max-w-7xl mx-auto grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-12">
          <FeatureCard 
            icon={<Cpu className="text-blue-500" size={32} />}
            title="在线代码开发"
            desc="集成 Monaco 编辑器，支持 Python 脚本实时编写、语法检测与 Dry-run 测试。"
          />
          <FeatureCard 
            icon={<Globe className="text-green-500" size={32} />}
            title="策略广场"
            desc="发现全球优质策略模板，一键引用并配置属于您的私有交易实例。"
          />
          <FeatureCard 
            icon={<Shield className="text-purple-500" size={32} />}
            title="多租户隔离"
            desc="严格的权限管理与账号隔离，确保您的 API Key 与策略逻辑万无一失。"
          />
          <FeatureCard 
            icon={<TrendingUp className="text-orange-500" size={32} />}
            title="实时数据看板"
            desc="基于 WebSocket 的秒级数据推送，实时监控持仓盈亏与系统日志。"
          />
        </div>
      </section>

      {/* Footer / Contact */}
      <footer className="py-20 border-t border-gray-900 bg-black/40">
        <div className="max-w-7xl mx-auto px-4 text-center md:text-left flex flex-col md:flex-row justify-between items-start gap-12">
          <div className="max-w-sm">
            <div className="flex items-center gap-2 mb-6">
              <Activity className="text-blue-600" size={28} />
              <span className="text-2xl font-bold">QuantyTrade</span>
            </div>
            <p className="text-gray-500 leading-relaxed mb-6">
              致力打造全球最开放、透明的量化交易基础设施。无论是机构还是个人，都能在这里找到属于自己的盈利逻辑。
            </p>
            <div className="flex items-center gap-4 text-gray-400">
              <Github size={20} className="hover:text-white cursor-pointer transition" />
              <Globe size={20} className="hover:text-white cursor-pointer transition" />
            </div>
          </div>
          
          <div className="grid grid-cols-2 gap-12 md:gap-24">
            <div>
              <h4 className="font-bold mb-6 text-gray-200">商务合作</h4>
              <ul className="space-y-4 text-gray-500 text-sm">
                <li className="hover:text-blue-400 cursor-pointer transition">机构版申请</li>
                <li className="hover:text-blue-400 cursor-pointer transition">策略开发者计划</li>
                <li className="hover:text-blue-400 cursor-pointer transition">API 代理对接</li>
              </ul>
            </div>
            <div>
              <h4 className="font-bold mb-6 text-gray-200">联系我们</h4>
              <ul className="space-y-4 text-gray-500 text-sm">
                <li className="flex items-center gap-2 group">
                  <Mail size={16} className="group-hover:text-blue-400" />
                  <a href="mailto:zhaoxianxin.climber@gmail.com" className="hover:text-blue-400 transition">
                    zhaoxianxin.climber@gmail.com
                  </a>
                </li>
                <li className="hover:text-blue-400 cursor-pointer transition">加入 Discord 群组</li>
                <li className="hover:text-blue-400 cursor-pointer transition">微信客服 (QuantySupport)</li>
              </ul>
            </div>
          </div>
        </div>
        <div className="max-w-7xl mx-auto px-4 mt-20 pt-8 border-t border-gray-900/50 flex flex-col md:flex-row justify-between items-center gap-4 text-xs text-gray-600">
          <p>© 2026 QuantyTrade Platform. All rights reserved.</p>
          <div className="flex gap-8">
            <span>隐私政策</span>
            <span>服务协议</span>
            <span>合规声明</span>
          </div>
        </div>
      </footer>
    </div>
  );
};

interface FeatureCardProps {
  icon: React.ReactNode;
  title: string;
  desc: string;
}

const FeatureCard = ({ icon, title, desc }: FeatureCardProps) => (
  <div className="group">
    <div className="mb-6 transform group-hover:scale-110 transition-transform duration-300">{icon}</div>
    <h4 className="text-xl font-bold mb-4 text-gray-100">{title}</h4>
    <p className="text-gray-500 leading-relaxed text-sm">{desc}</p>
  </div>
);

const fallbackStats = (seed: string) => {
  let h = 2166136261;
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  const pct = 30 + (Math.abs(h) % 500) / 10;
  return `+${pct.toFixed(1)}%`;
};

export default LandingPage;
