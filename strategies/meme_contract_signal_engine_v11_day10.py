import os
import time
import uuid
from dataclasses import replace
from datetime import datetime, timezone
import numpy as np
import pandas as pd
from typing import Dict, List, Optional, Tuple

from redis_compat import RedisCompat
import meme_contract_signal_engine_v6_day5 as base


base.Config.MAX_PRICE = 5.0
base.Config.MIN_PRECISION = 5
base.Config.MIN_VOLATILITY = 6.0
base.Config.MIN_CONFIDENCE = 0.62
base.Config.TP_RATIO = 0.06
base.Config.SL_RATIO = 0.028
base.Config.WARMUP_BARS = 180
base.Config.SIGNAL_COOLDOWN_SEC = 300
base.Config.VOL_BOOST_THR = 1.05
base.Config.TF_ALIGN_BOOST = 0.30
base.Config.TF_CONFLICT_PENALTY = 0.25


base.ADAPTIVE_CONFIGS[base.MarketRegime.TRENDING_UP] = base.AdaptiveConfig(0.065, 0.028, 0.66, 0.04, 0.02, 1.0, "v11 trend long")
base.ADAPTIVE_CONFIGS[base.MarketRegime.TRENDING_DOWN] = base.AdaptiveConfig(0.065, 0.028, 0.66, 0.04, 0.02, 1.0, "v11 trend short")
base.ADAPTIVE_CONFIGS[base.MarketRegime.REVERSAL_UP] = base.AdaptiveConfig(0.055, 0.025, 0.69, 0.035, 0.018, 0.8, "v11 reversal long")
base.ADAPTIVE_CONFIGS[base.MarketRegime.REVERSAL_DOWN] = base.AdaptiveConfig(0.055, 0.025, 0.69, 0.035, 0.018, 0.8, "v11 reversal short")
base.ADAPTIVE_CONFIGS[base.MarketRegime.RANGING] = base.AdaptiveConfig(0.045, 0.022, 0.70, 0.025, 0.015, 0.6, "v11 ranging relaxed")
base.ADAPTIVE_CONFIGS[base.MarketRegime.UNKNOWN] = base.AdaptiveConfig(0.050, 0.025, 0.70, 0.028, 0.016, 0.6, "v11 unknown relaxed")


class V11Config:
    LONG_BIAS_FACTOR = 1.15
    SHORT_RESTRICTION_FACTOR = 0.7
    MIN_LONG_CONFIDENCE = 0.60
    MIN_SHORT_CONFIDENCE = 0.68
    AI_SELECTION_TOP_N = 5
    MULTI_PERIOD_WEIGHTS = {
        "short_term": 0.4,
        "medium_term": 0.35,
        "long_term": 0.25
    }
    T0_ENABLED = True
    T0_MAX_POSITION_RATIO = 0.3
    CAPACITY_AWARE = True
    MAX_POSITIONS = 15
    MIN_CAPITAL_ALLOCATION = 500
    MAX_CAPITAL_ALLOCATION = 5000


class AISelectionModel:
    def __init__(self):
        self.feature_weights = {
            "momentum": 0.25,
            "volume": 0.20,
            "volatility": 0.15,
            "sentiment": 0.15,
            "structure": 0.15,
            "regime": 0.10
        }
        self.learning_rate = 0.01
        self.recent_performance = []
        self.symbol_scores = {}
        
    def predict(self, symbol: str, features: Dict) -> float:
        score = 0.0
        for feature_name, weight in self.feature_weights.items():
            if feature_name in features:
                score += features[feature_name] * weight
        
        if "funding_rate" in features and features["funding_rate"] < -0.0003:
            score *= 1.1
        
        if "ls_ratio" in features and features["ls_ratio"] > 1.5:
            score *= 1.05
            
        final_score = min(max(score, 0.0), 1.0)
        self.symbol_scores[symbol] = final_score
        return final_score
    
    def update_weights(self, actual_performance: float, predicted_score: float):
        error = actual_performance - predicted_score
        for feature_name in self.feature_weights:
            self.feature_weights[feature_name] += self.learning_rate * error
    
    def get_top_symbols(self, n: int = 5) -> List[Tuple[str, float]]:
        sorted_symbols = sorted(
            self.symbol_scores.items(),
            key=lambda x: x[1],
            reverse=True
        )
        return sorted_symbols[:n]


class MultiPeriodStrategy:
    def __init__(self):
        self.short_term_window = 20
        self.medium_term_window = 60
        self.long_term_window = 180
        
    def analyze(self, df: pd.DataFrame) -> Dict:
        if len(df) < self.long_term_window:
            return {"error": "Insufficient data"}
            
        results = {}
        
        short_df = df.iloc[-self.short_term_window:]
        medium_df = df.iloc[-self.medium_term_window:]
        long_df = df.iloc[-self.long_term_window:]
        
        results["short_term"] = self._analyze_period(short_df, "short")
        results["medium_term"] = self._analyze_period(medium_df, "medium")
        results["long_term"] = self._analyze_period(long_df, "long")
        
        overall_score = (
            results["short_term"]["score"] * V11Config.MULTI_PERIOD_WEIGHTS["short_term"] +
            results["medium_term"]["score"] * V11Config.MULTI_PERIOD_WEIGHTS["medium_term"] +
            results["long_term"]["score"] * V11Config.MULTI_PERIOD_WEIGHTS["long_term"]
        )
        
        results["overall_score"] = overall_score
        results["consensus"] = self._get_consensus(results)
        
        return results
    
    def _analyze_period(self, df: pd.DataFrame, period: str) -> Dict:
        returns = df["close"].pct_change().dropna()
        volatility = returns.std() * np.sqrt(252) if len(returns) > 0 else 0
        
        if len(df) >= 10:
            sma_short = df["close"].rolling(window=5).mean().iloc[-1]
            sma_long = df["close"].rolling(window=10).mean().iloc[-1]
            momentum = 1.0 if sma_short > sma_long else -1.0
        else:
            momentum = 0.0
            
        volume_trend = 1.0 if df["volume"].iloc[-5:].mean() > df["volume"].iloc[-10:-5].mean() else -1.0
        
        score = 0.5 + (momentum * 0.2 + volume_trend * 0.1 - volatility * 0.05)
        
        return {
            "score": min(max(score, 0.0), 1.0),
            "momentum": momentum,
            "volume_trend": volume_trend,
            "volatility": volatility,
            "period": period
        }
    
    def _get_consensus(self, results: Dict) -> str:
        periods = ["short_term", "medium_term", "long_term"]
        bullish_count = 0
        total_periods = len(periods)
        
        for period in periods:
            if results[period]["score"] > 0.6:
                bullish_count += 1
                
        if bullish_count == total_periods:
            return "strong_bull"
        elif bullish_count >= 2:
            return "bull"
        elif bullish_count == 1:
            return "neutral"
        else:
            return "bear"


class T0Strategy:
    def __init__(self):
        self.max_positions = 3
        self.max_holding_time = 300
        self.min_profit_pct = 0.005
        self.positions = {}
        
    def can_execute_t0(self, symbol: str, base_position: float) -> bool:
        if not V11Config.T0_ENABLED:
            return False
            
        if len(self.positions) >= self.max_positions:
            return False
            
        max_t0_size = base_position * V11Config.T0_MAX_POSITION_RATIO
        if max_t0_size < V11Config.MIN_CAPITAL_ALLOCATION:
            return False
            
        return True
    
    def generate_t0_signal(self, symbol: str, df: pd.DataFrame, 
                          base_direction: str, base_entry: float) -> Optional[Dict]:
        if len(df) < 20:
            return None
            
        recent_volatility = df["close"].pct_change().std() * np.sqrt(252)
        if recent_volatility > 100:
            return None
            
        short_ma = df["close"].rolling(window=3).mean().iloc[-1]
        medium_ma = df["close"].rolling(window=10).mean().iloc[-1]
        
        if base_direction == "long":
            if short_ma > medium_ma * 1.002:
                return {
                    "action": "sell",
                    "price": short_ma,
                    "size_pct": 0.3,
                    "reason": "短期超买T0卖出"
                }
            elif short_ma < medium_ma * 0.998:
                return {
                    "action": "buy",
                    "price": short_ma,
                    "size_pct": 0.3,
                    "reason": "短期回调T0买入"
                }
        elif base_direction == "short":
            if short_ma < medium_ma * 0.998:
                return {
                    "action": "buy",
                    "price": short_ma,
                    "size_pct": 0.3,
                    "reason": "短期超卖T0买入"
                }
            elif short_ma > medium_ma * 1.002:
                return {
                    "action": "sell",
                    "price": short_ma,
                    "size_pct": 0.3,
                    "reason": "短期反弹T0卖出"
                }
                
        return None


class CapacityAwareAllocator:
    def __init__(self, total_capital: float = 100000):
        self.total_capital = total_capital
        self.allocated_capital = 0
        self.positions = {}
        
    def can_allocate(self, symbol: str, requested_amount: float) -> Tuple[bool, float]:
        if len(self.positions) >= V11Config.MAX_POSITIONS:
            return False, 0.0
            
        if requested_amount < V11Config.MIN_CAPITAL_ALLOCATION:
            return False, 0.0
            
        max_per_position = min(
            V11Config.MAX_CAPITAL_ALLOCATION,
            self.total_capital * 0.05
        )
        
        allocated_so_far = sum(self.positions.values())
        available = self.total_capital - allocated_so_far
        
        if available < V11Config.MIN_CAPITAL_ALLOCATION:
            return False, 0.0
            
        allocated_amount = min(requested_amount, max_per_position, available)
        
        if allocated_amount >= V11Config.MIN_CAPITAL_ALLOCATION:
            self.positions[symbol] = allocated_amount
            self.allocated_capital += allocated_amount
            return True, allocated_amount
        else:
            return False, 0.0
    
    def release_allocation(self, symbol: str):
        if symbol in self.positions:
            self.allocated_capital -= self.positions[symbol]
            del self.positions[symbol]


ai_model = AISelectionModel()
multi_period = MultiPeriodStrategy()
t0_strategy = T0Strategy()
allocator = CapacityAwareAllocator()


def _reject(result: base.SignalResult, reason: str) -> base.SignalResult:
    return replace(
        result,
        direction=None,
        tp_price=0.0,
        sl_price=0.0,
        passed_filter=False,
        filter_reason=reason,
        position_sizing=replace(result.position_sizing, usdt_amount=0.0, rationale=reason),
        score_card=replace(result.score_card, direction=None, verdict="SKIP"),
    )


def _rr(result: base.SignalResult) -> float:
    if result.direction == "long" and result.entry_price > result.sl_price and result.tp_price > result.entry_price:
        return (result.tp_price - result.entry_price) / max(result.entry_price - result.sl_price, 1e-12)
    if result.direction == "short" and result.sl_price > result.entry_price and result.tp_price < result.entry_price:
        return (result.entry_price - result.tp_price) / max(result.sl_price - result.entry_price, 1e-12)
    return 0.0


def _calculate_ai_features(result: base.SignalResult, candles: List[Dict]) -> Dict:
    df = pd.DataFrame(candles)
    
    features = {
        "momentum": result.confidence,
        "volume": result.vol_ratio,
        "volatility": result.atr_pct,
        "sentiment": 1.0 if result.ls_ratio > 1.0 else 0.5,
        "structure": 0.7 if result.ema_trend == "bullish" else 0.3,
        "regime": 0.8 if result.regime_result.regime == base.MarketRegime.TRENDING_UP else 0.5,
        "funding_rate": result.funding_rate,
        "ls_ratio": result.ls_ratio
    }
    
    return features


def _apply_long_bias(result: base.SignalResult) -> base.SignalResult:
    if result.direction == "long":
        result.confidence *= V11Config.LONG_BIAS_FACTOR
        result.score_card.confidence = result.confidence
    elif result.direction == "short":
        result.confidence *= V11Config.SHORT_RESTRICTION_FACTOR
        result.score_card.confidence = result.confidence
        
    return result


def _apply_multi_period_analysis(result: base.SignalResult, candles: List[Dict]) -> base.SignalResult:
    df = pd.DataFrame(candles)
    multi_period_result = multi_period.analyze(df)
    
    if "error" in multi_period_result:
        return result
        
    result.score_card.overall_score = multi_period_result["overall_score"]
    result.score_card.strengths.append(f"多周期共识: {multi_period_result['consensus']}")
    
    if multi_period_result["consensus"] in ["strong_bull", "bull"]:
        result.confidence *= 1.1
        result.score_card.confidence = result.confidence
        
    return result


def _check_capacity_allocation(result: base.SignalResult) -> base.SignalResult:
    if not V11Config.CAPACITY_AWARE:
        return result
        
    requested_amount = result.position_sizing.usdt_amount
    can_allocate, allocated_amount = allocator.can_allocate(result.symbol, requested_amount)
    
    if not can_allocate:
        return _reject(result, f"容量限制: 无法分配资金, 已持仓{len(allocator.positions)}个")
        
    if allocated_amount < requested_amount:
        result.position_sizing.usdt_amount = allocated_amount
        result.position_sizing.rationale = f"容量调整: 从{requested_amount:.0f}调整至{allocated_amount:.0f} USDT"
        
    return result


def _generate_t0_signal_if_applicable(result: base.SignalResult, candles: List[Dict]):
    if result.direction is None or not result.passed_filter:
        return None
        
    if not t0_strategy.can_execute_t0(result.symbol, result.position_sizing.usdt_amount):
        return None
        
    df = pd.DataFrame(candles)
    t0_signal = t0_strategy.generate_t0_signal(
        result.symbol, df, result.direction, result.entry_price
    )
    
    return t0_signal


def analyze_signal_v11(symbol: str, candles: List[Dict], 
                      market_snapshot: base.MarketSnapshot,
                      regime_result: base.RegimeResult) -> base.SignalResult:
    
    if len(candles) < base.Config.WARMUP_BARS:
        bars = len(candles)
        needed = base.Config.WARMUP_BARS
        base._log(f"v11等待样本 symbol={symbol} bars={bars}/{needed}")
        return base.SignalResult(
            symbol=symbol,
            direction=None,
            entry_price=0.0,
            tp_price=0.0,
            sl_price=0.0,
            confidence=0.0,
            rsi=0.0,
            macd_diff=0.0,
            bb_position="",
            atr_pct=0.0,
            funding_rate=0.0,
            ls_ratio=0.0,
            vol_ratio=0.0,
            ema_trend="",
            vol_boost=False,
            trend_align=False,
            cvd_trend="",
            nearest_support=0.0,
            nearest_resistance=0.0,
            dist_to_support=0.0,
            dist_to_resistance=0.0,
            sl_dynamic=False,
            obv_trend="",
            obv_divergence="",
            tf4h_direction=None,
            tf_aligned=False,
            tf_boost=0.0,
            position_sizing=base.PositionSizing(
                kelly_fraction=0.0,
                used_fraction=0.0,
                usdt_amount=0.0,
                rationale=f"等待样本: {bars}/{needed}"
            ),
            trailing_stop=base.TrailingStopState(
                direction="",
                entry_price=0.0,
                trail_pct=0.0,
                highest_price=0.0,
                lowest_price=0.0,
                current_stop=0.0,
                activated=False,
                activate_pct=0.0
            ),
            regime_result=regime_result,
            score_card=base.ScoreCard(
                symbol=symbol,
                direction=None,
                confidence=0.0,
                momentum_score=0.0,
                volume_score=0.0,
                structure_score=0.0,
                sentiment_score=0.0,
                trend_score=0.0,
                overall_score=0.0,
                strengths=[],
                risks=["数据不足"],
                regime=regime_result.regime,
                verdict="等待样本"
            ),
            adaptive_cfg=base.ADAPTIVE_CONFIGS[regime_result.regime],
            passed_filter=False,
            filter_reason=f"等待样本: {bars}/{needed}"
        )
    
    base_result = base.analyze_signal(symbol, candles, market_snapshot, regime_result)
    
    if not base_result.passed_filter:
        base._log(f"v11激进跳过 symbol={symbol} reason={base_result.filter_reason}")
        return base_result
    
    ai_features = _calculate_ai_features(base_result, candles)
    ai_score = ai_model.predict(symbol, ai_features)
    
    if ai_score < 0.6:
        return _reject(base_result, f"AI选股评分不足: {ai_score:.2f}")
    
    base_result = _apply_long_bias(base_result)
    base_result = _apply_multi_period_analysis(base_result, candles)
    
    if base_result.direction == "long" and base_result.confidence < V11Config.MIN_LONG_CONFIDENCE:
        return _reject(base_result, f"多头信心不足: {base_result.confidence:.2f} < {V11Config.MIN_LONG_CONFIDENCE}")
    
    if base_result.direction == "short" and base_result.confidence < V11Config.MIN_SHORT_CONFIDENCE:
        return _reject(base_result, f"空头信心不足: {base_result.confidence:.2f} < {V11Config.MIN_SHORT_CONFIDENCE}")
    
    base_result = _check_capacity_allocation(base_result)
    
    t0_signal = _generate_t0_signal_if_applicable(base_result, candles)
    if t0_signal:
        base._log(f"v11 T0信号 symbol={symbol} action={t0_signal['action']} reason={t0_signal['reason']}")
    
    rr_value = _rr(base_result)
    if rr_value < 1.5:
        return _reject(base_result, f"风险回报比不足: {rr_value:.2f}")
    
    base_result.score_card.verdict = "✅ 建议开仓"
    base_result.score_card.strengths.append(f"AI评分: {ai_score:.2f}")
    base_result.score_card.strengths.append(f"风险回报比: {rr_value:.2f}")
    
    base._log(f"SIGNAL open sym={symbol} dir={base_result.direction} conf={base_result.confidence:.2f} ai={ai_score:.2f} rr={rr_value:.2f}")
    
    return base_result


def batch_ai_selection(symbols: List[str], all_candles: Dict[str, List[Dict]]) -> List[Tuple[str, float]]:
    """
    批量AI选股，返回排名前N的标的
    模仿幻方量化的AI选股系统
    """
    scores = []
    
    for symbol in symbols:
        if symbol not in all_candles:
            continue
            
        candles = all_candles[symbol]
        if len(candles) < base.Config.WARMUP_BARS:
            continue
            
        market_snapshot = base.MarketSnapshot(
            symbol=symbol,
            price=candles[-1]["close"] if candles else 0.0,
            change_pct_24h=0.0,
            funding_rate=0.0,
            ls_ratio=1.0
        )
        
        regime_result = base.detect_market_regime(candles)
        base_result = base.analyze_signal(symbol, candles, market_snapshot, regime_result)
        
        if not base_result.passed_filter:
            continue
            
        ai_features = _calculate_ai_features(base_result, candles)
        ai_score = ai_model.predict(symbol, ai_features)
        
        if ai_score < 0.6:
            continue
            
        scores.append((symbol, ai_score))
    
    scores.sort(key=lambda x: x[1], reverse=True)
    return scores[:V11Config.AI_SELECTION_TOP_N]


def run():
    base._log("v11幻方量化策略启动 - 放弃中性对冲，全面拥抱多头增强 + AI选股")
    
    redis = RedisCompat(
        host=os.getenv("REDIS_HOST", "localhost"),
        port=int(os.getenv("REDIS_PORT", "6379")),
        db=int(os.getenv("REDIS_DB", "0")),
        password=os.getenv("REDIS_PASSWORD", "")
    )
    
    strategy_id = os.getenv("STRATEGY_ID", str(uuid.uuid4()))
    owner_id = int(os.getenv("OWNER_ID", "1"))
    prefix = os.getenv("REDIS_PREFIX", "qt")
    
    symbols_str = os.getenv("SYMBOLS", "")
    if symbols_str:
        symbols = [s.strip() for s in symbols_str.split(",") if s.strip()]
    else:
        symbols = []
    
    base._log(f"v11策略配置 strategy_id={strategy_id} owner_id={owner_id} symbols={len(symbols)}")
    base._log(f"核心逻辑: 1) 多头偏好因子={V11Config.LONG_BIAS_FACTOR} 2) AI选股Top{N}=V11Config.AI_SELECTION_TOP_N")
    base._log(f"容量管理: 最大持仓={V11Config.MAX_POSITIONS} T0策略={'启用' if V11Config.T0_ENABLED else '禁用'}")
    
    candle_cache = {}
    last_ai_report = time.time()
    ai_report_interval = 300
    
    cooldown_sec = base.Config.SIGNAL_COOLDOWN_SEC
    cooldown_map = {}
    
    def on_candle(payload):
        symbol = payload.get("symbol")
        if not symbol or symbol not in symbols:
            return
        
        now = time.time()
        if symbol in cooldown_map:
            remaining = cooldown_map[symbol] - now
            if remaining > 0:
                base._log(f"v11冷却中 symbol={symbol} remaining={remaining:.0f}s")
                return
            else:
                del cooldown_map[symbol]
        
        candles = base._get_candles(symbol)
        if not candles or len(candles) < base.Config.WARMUP_BARS:
            bars = len(candles) if candles else 0
            needed = base.Config.WARMUP_BARS
            base._log(f"v11等待样本 symbol={symbol} bars={bars}/{needed}")
            return
        
        candle_cache[symbol] = candles
        
        market_snapshot = base.MarketSnapshot(
            symbol=symbol,
            price=payload.get("close", 0.0),
            change_pct_24h=0.0,
            funding_rate=0.0,
            ls_ratio=1.0
        )
        
        regime_result = base.detect_market_regime(candles)
        
        result = analyze_signal_v11(symbol, candles, market_snapshot, regime_result)
        
        if result.passed_filter and result.direction:
            cooldown_map[symbol] = now + cooldown_sec
            base._emit_signal(result)
        elif result.filter_reason:
            base._log(f"v11跳过 symbol={symbol} reason={result.filter_reason}")
        
        if now - last_ai_report > ai_report_interval:
            if len(candle_cache) >= V11Config.AI_SELECTION_TOP_N:
                top_symbols = batch_ai_selection(symbols, candle_cache)
                if top_symbols:
                    report_lines = ["v11 AI选股排名 (Top {}):".format(V11Config.AI_SELECTION_TOP_N)]
                    for i, (sym, score) in enumerate(top_symbols, 1):
                        report_lines.append(f"{i}. {sym}: {score:.3f}")
                    base._log("\n".join(report_lines))
            
            last_ai_report = now
    
    base._run_event_loop(
        redis=redis,
        strategy_id=strategy_id,
        owner_id=owner_id,
        prefix=prefix,
        symbols=symbols,
        on_candle=on_candle,
        log_trace=True,
        log_decisions=True
    )


if __name__ == "__main__":
    run()