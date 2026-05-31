#!/usr/bin/env python3
"""
v11策略基本功能测试
测试幻方量化策略理念的核心组件
"""

import sys
import os
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import numpy as np
import pandas as pd
from datetime import datetime, timedelta
import meme_contract_signal_engine_v11_day10 as v11


def test_ai_selection_model():
    """测试AI选股模型"""
    print("测试AI选股模型...")
    
    model = v11.AISelectionModel()
    
    # 测试预测功能
    features = {
        "momentum": 0.8,
        "volume": 0.7,
        "volatility": 0.3,
        "sentiment": 0.9,
        "structure": 0.6,
        "regime": 0.8,
        "funding_rate": -0.0005,
        "ls_ratio": 1.8
    }
    
    score = model.predict("TEST/USDT", features)
    print(f"  AI评分: {score:.3f}")
    
    assert 0 <= score <= 1, "AI评分应在0-1之间"
    
    # 测试权重更新
    model.update_weights(0.75, score)
    print("  权重更新测试通过")
    
    # 测试排名功能
    model.predict("SYMBOL1/USDT", {"momentum": 0.9, "volume": 0.8})
    model.predict("SYMBOL2/USDT", {"momentum": 0.7, "volume": 0.6})
    model.predict("SYMBOL3/USDT", {"momentum": 0.8, "volume": 0.7})
    
    top_symbols = model.get_top_symbols(2)
    print(f"  Top 2 标的: {top_symbols}")
    
    assert len(top_symbols) == 2, "应返回前2个标的"
    assert top_symbols[0][1] >= top_symbols[1][1], "评分应降序排列"
    
    print("  AI选股模型测试通过 ✓")


def test_multi_period_strategy():
    """测试多周期策略分析"""
    print("测试多周期策略分析...")
    
    strategy = v11.MultiPeriodStrategy()
    
    # 创建测试数据
    np.random.seed(42)
    dates = pd.date_range(start='2024-01-01', periods=200, freq='1min')
    prices = 100 + np.cumsum(np.random.randn(200) * 0.5)
    volumes = 1000 + np.random.randn(200) * 200
    
    df = pd.DataFrame({
        'timestamp': dates,
        'open': prices - np.random.randn(200) * 0.1,
        'high': prices + np.random.randn(200) * 0.2,
        'low': prices - np.random.randn(200) * 0.2,
        'close': prices,
        'volume': volumes
    })
    
    # 测试分析功能
    result = strategy.analyze(df)
    
    print(f"  短期评分: {result['short_term']['score']:.3f}")
    print(f"  中期评分: {result['medium_term']['score']:.3f}")
    print(f"  长期评分: {result['long_term']['score']:.3f}")
    print(f"  综合评分: {result['overall_score']:.3f}")
    print(f"  周期共识: {result['consensus']}")
    
    assert 'short_term' in result
    assert 'medium_term' in result
    assert 'long_term' in result
    assert 'overall_score' in result
    assert 'consensus' in result
    
    assert 0 <= result['overall_score'] <= 1, "综合评分应在0-1之间"
    
    print("  多周期策略分析测试通过 ✓")


def test_t0_strategy():
    """测试T0策略"""
    print("测试T0策略...")
    
    strategy = v11.T0Strategy()
    
    # 测试是否可以执行T0
    can_execute = strategy.can_execute_t0("TEST/USDT", 10000)
    print(f"  可执行T0: {can_execute}")
    
    # 创建测试数据
    np.random.seed(42)
    dates = pd.date_range(start='2024-01-01', periods=50, freq='1min')
    prices = 100 + np.cumsum(np.random.randn(50) * 0.2)
    
    df = pd.DataFrame({
        'timestamp': dates,
        'open': prices - np.random.randn(50) * 0.05,
        'high': prices + np.random.randn(50) * 0.1,
        'low': prices - np.random.randn(50) * 0.1,
        'close': prices,
        'volume': np.random.randn(50) * 1000 + 5000
    })
    
    # 测试生成T0信号
    signal = strategy.generate_t0_signal("TEST/USDT", df, "long", 100.0)
    
    if signal:
        print(f"  T0信号: {signal['action']} - {signal['reason']}")
        assert signal['action'] in ['buy', 'sell']
        assert 'reason' in signal
        assert 'price' in signal
        assert 'size_pct' in signal
    else:
        print("  无T0信号生成")
    
    print("  T0策略测试通过 ✓")


def test_capacity_allocator():
    """测试容量感知分配器"""
    print("测试容量感知分配器...")
    
    allocator = v11.CapacityAwareAllocator(total_capital=100000)
    
    # 测试分配
    can_allocate1, amount1 = allocator.can_allocate("SYMBOL1", 8000)
    print(f"  分配SYMBOL1 8000USDT: {can_allocate1}, {amount1}")
    
    can_allocate2, amount2 = allocator.can_allocate("SYMBOL2", 300)
    print(f"  分配SYMBOL2 300USDT: {can_allocate2}, {amount2}")
    
    can_allocate3, amount3 = allocator.can_allocate("SYMBOL3", 6000)
    print(f"  分配SYMBOL3 6000USDT: {can_allocate3}, {amount3}")
    
    assert can_allocate1, "应能分配8000USDT"
    assert not can_allocate2, "不应分配低于最小分配额"
    assert can_allocate3, "应能分配6000USDT"
    
    # 测试释放
    allocator.release_allocation("SYMBOL1")
    print("  释放SYMBOL1分配")
    
    # 再次分配
    can_allocate4, amount4 = allocator.can_allocate("SYMBOL4", 7000)
    print(f"  再次分配SYMBOL4 7000USDT: {can_allocate4}, {amount4}")
    
    assert can_allocate4, "释放后应能再次分配"
    
    print("  容量感知分配器测试通过 ✓")


def test_config_values():
    """测试配置值"""
    print("测试配置值...")
    
    print(f"  多头偏好因子: {v11.V11Config.LONG_BIAS_FACTOR}")
    print(f"  空头限制因子: {v11.V11Config.SHORT_RESTRICTION_FACTOR}")
    print(f"  最小多头信心: {v11.V11Config.MIN_LONG_CONFIDENCE}")
    print(f"  最小空头信心: {v11.V11Config.MIN_SHORT_CONFIDENCE}")
    print(f"  AI选股TopN: {v11.V11Config.AI_SELECTION_TOP_N}")
    print(f"  最大持仓数: {v11.V11Config.MAX_POSITIONS}")
    print(f"  T0策略启用: {v11.V11Config.T0_ENABLED}")
    
    assert v11.V11Config.LONG_BIAS_FACTOR > 1.0, "多头偏好因子应大于1"
    assert v11.V11Config.SHORT_RESTRICTION_FACTOR < 1.0, "空头限制因子应小于1"
    assert v11.V11Config.MIN_LONG_CONFIDENCE < v11.V11Config.MIN_SHORT_CONFIDENCE, "多头门槛应低于空头"
    
    print("  配置值测试通过 ✓")


def main():
    """主测试函数"""
    print("=" * 60)
    print("v11幻方量化策略基本功能测试")
    print("=" * 60)
    
    tests = [
        test_ai_selection_model,
        test_multi_period_strategy,
        test_t0_strategy,
        test_capacity_allocator,
        test_config_values
    ]
    
    passed = 0
    total = len(tests)
    
    for test_func in tests:
        try:
            test_func()
            passed += 1
            print()
        except Exception as e:
            print(f"  ❌ 测试失败: {e}")
            print()
    
    print("=" * 60)
    print(f"测试结果: {passed}/{total} 通过")
    
    if passed == total:
        print("✅ 所有测试通过！v11策略基本功能正常")
        return 0
    else:
        print("⚠️  部分测试失败，请检查策略实现")
        return 1


if __name__ == "__main__":
    sys.exit(main())