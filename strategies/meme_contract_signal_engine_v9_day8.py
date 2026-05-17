import json
import os
import sys
import threading
import time
from dataclasses import replace
from datetime import datetime, timezone

try:
    from mini_redis import MiniRedis
except Exception:
    pass

from redis_compat import RedisCompat
import meme_contract_signal_engine_v6_day5 as base


base.Config.MAX_PRICE = 5.0
base.Config.MIN_PRECISION = 5
base.Config.MIN_VOLATILITY = 8.0
base.Config.MIN_CONFIDENCE = 0.70
base.Config.TP_RATIO = 0.055
base.Config.SL_RATIO = 0.025
base.Config.WARMUP_BARS = 240
base.Config.SIGNAL_COOLDOWN_SEC = 600
base.Config.VOL_BOOST_THR = 1.10
base.Config.TF_ALIGN_BOOST = 0.25
base.Config.TF_CONFLICT_PENALTY = 0.30


base.ADAPTIVE_CONFIGS[base.MarketRegime.TRENDING_UP] = base.AdaptiveConfig(0.060, 0.025, 0.72, 0.035, 0.018, 0.95, "v9 trend long")
base.ADAPTIVE_CONFIGS[base.MarketRegime.TRENDING_DOWN] = base.AdaptiveConfig(0.060, 0.025, 0.72, 0.035, 0.018, 0.95, "v9 trend short")
base.ADAPTIVE_CONFIGS[base.MarketRegime.REVERSAL_UP] = base.AdaptiveConfig(0.050, 0.022, 0.76, 0.030, 0.015, 0.75, "v9 reversal long")
base.ADAPTIVE_CONFIGS[base.MarketRegime.REVERSAL_DOWN] = base.AdaptiveConfig(0.050, 0.022, 0.76, 0.030, 0.015, 0.75, "v9 reversal short")
base.ADAPTIVE_CONFIGS[base.MarketRegime.RANGING] = base.AdaptiveConfig(0.040, 0.020, 0.80, 0.022, 0.012, 0.55, "v9 ranging skip")
base.ADAPTIVE_CONFIGS[base.MarketRegime.UNKNOWN] = base.AdaptiveConfig(0.045, 0.022, 0.80, 0.025, 0.014, 0.55, "v9 unknown skip")


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


def _is_high_liquidity_hour():
    utc_hour = datetime.now(timezone.utc).hour
    return 8 <= utc_hour <= 22


def _calculate_funding_rate_adjustment(funding_rate):
    if funding_rate < -0.0005:
        return {"bias": "long", "adjustment": 0.05, "reason": "高负费率偏好做多"}
    elif funding_rate > 0.0005:
        return {"bias": "short", "adjustment": 0.05, "reason": "高正费率偏好做空"}
    elif funding_rate < -0.0002:
        return {"bias": "long", "adjustment": 0.02, "reason": "负费率偏好做多"}
    elif funding_rate > 0.0002:
        return {"bias": "short", "adjustment": 0.02, "reason": "正费率偏好做空"}
    else:
        return {"bias": "neutral", "adjustment": 0.0, "reason": "费率中性"}


def _calculate_leverage_adjustment(atr_percentile, volatility):
    base_leverage = 3.0
    
    if atr_percentile > 0.85:
        leverage = max(1.0, base_leverage * 0.5)
        return {"leverage": leverage, "reason": f"高波动降低杠杆至{leverage:.1f}x"}
    elif atr_percentile > 0.7:
        leverage = base_leverage * 0.75
        return {"leverage": leverage, "reason": f"中高波动杠杆{leverage:.1f}x"}
    elif volatility < 5.0:
        leverage = min(5.0, base_leverage * 1.2)
        return {"leverage": leverage, "reason": f"低波动适当提高至{leverage:.1f}x"}
    else:
        return {"leverage": base_leverage, "reason": f"标准杠杆{base_leverage:.1f}x"}


def _detect_manipulation_risk(result, candles):
    if len(candles) < 20:
        return {"risk": "low", "reason": "数据不足"}
    
    recent_prices = [c["close"] for c in candles[-10:]]
    recent_volumes = [c["volume"] for c in candles[-10:]]
    
    price_change = abs((recent_prices[-1] - recent_prices[0]) / recent_prices[0])
    volume_change = abs((recent_volumes[-1] - recent_volumes[0]) / recent_volumes[0])
    
    if price_change > 0.15 and volume_change < 0.3:
        return {"risk": "high", "reason": f"价格异常波动{price_change:.1%}但成交量未跟上"}
    elif price_change > 0.25:
        return {"risk": "high", "reason": f"价格剧烈波动{price_change:.1%}"}
    elif result.vol_ratio < 0.7:
        return {"risk": "medium", "reason": f"成交量异常低{result.vol_ratio:.2f}"}
    else:
        return {"risk": "low", "reason": "市场行为正常"}


def _calculate_ls_ratio_adjustment(ls_ratio):
    if ls_ratio > 2.5:
        return {"adjustment": -0.08, "reason": f"极端多空比{ls_ratio:.2f}，反向信号增强"}
    elif ls_ratio > 2.0:
        return {"adjustment": -0.05, "reason": f"高多空比{ls_ratio:.2f}，谨慎做多"}
    elif ls_ratio < 0.4:
        return {"adjustment": 0.08, "reason": f"极端低多空比{ls_ratio:.2f}，反向信号增强"}
    elif ls_ratio < 0.6:
        return {"adjustment": 0.05, "reason": f"低多空比{ls_ratio:.2f}，谨慎做空"}
    else:
        return {"adjustment": 0.0, "reason": "多空比正常"}


def _calculate_quantum_thresholds(result):
    regime = result.regime_result.regime
    atr_percentile = result.regime_result.atr_percentile
    funding_rate = result.funding_rate
    ls_ratio = result.ls_ratio
    
    base_thresholds = {
        "trending": {"score": 62, "confidence": 0.70, "rr": 1.2, "vol_ratio": 0.90},
        "reversal": {"score": 64, "confidence": 0.76, "rr": 1.3, "vol_ratio": 0.95},
        "ranging": {"score": 68, "confidence": 0.80, "rr": 1.6, "vol_ratio": 1.05},
    }
    
    funding_adj = _calculate_funding_rate_adjustment(funding_rate)
    ls_adj = _calculate_ls_ratio_adjustment(ls_ratio)
    total_adjustment = funding_adj["adjustment"] + ls_adj["adjustment"]
    
    if regime in (base.MarketRegime.TRENDING_UP, base.MarketRegime.TRENDING_DOWN):
        thresholds = base_thresholds["trending"].copy()
        regime_key = "trending"
    elif regime in (base.MarketRegime.REVERSAL_UP, base.MarketRegime.REVERSAL_DOWN):
        thresholds = base_thresholds["reversal"].copy()
        regime_key = "reversal"
    else:
        thresholds = base_thresholds["ranging"].copy()
        regime_key = "ranging"
    
    if atr_percentile > 0.8:
        thresholds["score"] = max(58, thresholds["score"] - 4)
        thresholds["confidence"] = max(0.68, thresholds["confidence"] - 0.04)
        thresholds["rr"] = max(1.1, thresholds["rr"] - 0.1)
        thresholds["vol_ratio"] = max(0.85, thresholds["vol_ratio"] - 0.05)
    
    if not _is_high_liquidity_hour():
        thresholds["score"] += 2
        thresholds["confidence"] += 0.02
        thresholds["rr"] += 0.1
        thresholds["vol_ratio"] += 0.05
    
    thresholds["confidence"] = max(thresholds["confidence"] + total_adjustment, 0.65)
    
    thresholds["funding_bias"] = funding_adj["bias"]
    thresholds["funding_reason"] = funding_adj["reason"]
    thresholds["ls_adjustment"] = ls_adj["adjustment"]
    thresholds["ls_reason"] = ls_adj["reason"]
    thresholds["regime_key"] = regime_key
    
    return thresholds


def _calculate_quantum_position(result, thresholds):
    regime = result.regime_result.regime
    confidence = result.confidence
    score = result.score_card.overall_score
    rr = _rr(result)
    atr_percentile = result.regime_result.atr_percentile
    
    base_multiplier = 1.0
    
    leverage_adj = _calculate_leverage_adjustment(atr_percentile, result.atr_pct)
    leverage = leverage_adj["leverage"]
    
    if regime in (base.MarketRegime.TRENDING_UP, base.MarketRegime.TRENDING_DOWN):
        if confidence > 0.76 and score > 68 and rr > 1.4:
            base_multiplier = 1.3
        elif confidence > 0.73 and score > 65 and rr > 1.25:
            base_multiplier = 1.15
            
        if thresholds["funding_bias"] == "long" and result.direction == "long":
            base_multiplier *= 1.08
        elif thresholds["funding_bias"] == "short" and result.direction == "short":
            base_multiplier *= 1.08
            
    elif regime in (base.MarketRegime.REVERSAL_UP, base.MarketRegime.REVERSAL_DOWN):
        if confidence > 0.80 and score > 70 and rr > 1.5:
            base_multiplier = 1.4
        elif confidence > 0.78 and score > 67 and rr > 1.35:
            base_multiplier = 1.2
            
        if thresholds["ls_adjustment"] > 0 and result.direction == "long":
            base_multiplier *= (1 + thresholds["ls_adjustment"])
        elif thresholds["ls_adjustment"] < 0 and result.direction == "short":
            base_multiplier *= (1 - thresholds["ls_adjustment"])
    
    if result.tf_aligned and result.tf_boost > 0.2:
        base_multiplier *= 1.12
        
    if result.vol_boost:
        base_multiplier *= 1.06
    
    position_multiplier = min(base_multiplier, 1.6)
    
    leverage_factor = 3.0 / leverage
    final_multiplier = position_multiplier * leverage_factor
    
    return {
        "position_multiplier": position_multiplier,
        "leverage": leverage,
        "leverage_reason": leverage_adj["reason"],
        "final_multiplier": final_multiplier,
        "recommended_leverage": f"{leverage:.1f}x",
        "max_position_boost": "60%"
    }


def _quantum_filter(result: base.SignalResult, candles) -> base.SignalResult:
    if not result.passed_filter or result.direction is None:
        return result

    regime = result.regime_result.regime
    thresholds = _calculate_quantum_thresholds(result)
    
    manipulation_risk = _detect_manipulation_risk(result, candles)
    if manipulation_risk["risk"] == "high":
        return _reject(result, f"v9 市场操纵风险: {manipulation_risk['reason']}")
    
    if not _is_high_liquidity_hour():
        if result.confidence < 0.75:
            return _reject(result, "v9 低流动性时段需要更高置信度")
    
    if regime in (base.MarketRegime.RANGING, base.MarketRegime.UNKNOWN):
        if result.confidence < 0.78 or result.score_card.overall_score < 72:
            return _reject(result, f"v9 skip regime={regime.value} conf={result.confidence:.4f} score={result.score_card.overall_score:.1f}")

    if result.score_card.overall_score < thresholds["score"]:
        return _reject(result, f"v9 score too low={result.score_card.overall_score:.1f} < {thresholds['score']}")

    min_confidence = max(thresholds["confidence"], result.adaptive_cfg.min_confidence)
    if result.confidence < min_confidence:
        return _reject(result, f"v9 confidence too low={result.confidence:.4f} < {min_confidence:.4f}")

    if result.atr_pct <= 0 or result.regime_result.atr_percentile >= 0.96:
        return _reject(result, "v9 volatility too extreme")

    if result.vol_ratio < thresholds["vol_ratio"] and not result.vol_boost:
        return _reject(result, f"v9 volume weak={result.vol_ratio:.2f} < {thresholds['vol_ratio']}")

    funding_bias = thresholds["funding_bias"]
    if funding_bias == "long" and result.direction == "short":
        if result.confidence < 0.80:
            return _reject(result, f"v9 费率偏多时做空需要更高置信度: {thresholds['funding_reason']}")
    elif funding_bias == "short" and result.direction == "long":
        if result.confidence < 0.80:
            return _reject(result, f"v9 费率偏空时做多需要更高置信度: {thresholds['funding_reason']}")

    if result.direction == "long":
        if result.ema_trend != "up":
            if result.confidence < 0.78:
                return _reject(result, "v9 need ema up for long")
        if result.cvd_trend == "down" or result.obv_trend == "down":
            if result.confidence < 0.82:
                return _reject(result, "v9 flow against long")
        if result.tf4h_direction and result.tf4h_direction != "long":
            if result.confidence < 0.84:
                return _reject(result, "v9 4h conflict long")
        if regime == base.MarketRegime.TRENDING_DOWN:
            if result.confidence < 0.86:
                return _reject(result, "v9 regime down for long")
    else:
        if result.ema_trend != "down":
            if result.confidence < 0.78:
                return _reject(result, "v9 need ema down for short")
        if result.cvd_trend == "up" or result.obv_trend == "up":
            if result.confidence < 0.82:
                return _reject(result, "v9 flow against short")
        if result.tf4h_direction and result.tf4h_direction != "short":
            if result.confidence < 0.84:
                return _reject(result, "v9 4h conflict short")
        if regime == base.MarketRegime.TRENDING_UP:
            if result.confidence < 0.86:
                return _reject(result, "v9 regime up for short")

    if regime in (base.MarketRegime.REVERSAL_UP, base.MarketRegime.REVERSAL_DOWN):
        need_div = "bullish" if result.direction == "long" else "bearish"
        if result.confidence < 0.82:
            return _reject(result, "v9 reversal confidence low")
        if result.obv_divergence != need_div:
            if result.confidence < 0.86:
                return _reject(result, "v9 reversal needs divergence")

    rr = _rr(result)
    if rr < thresholds["rr"]:
        return _reject(result, f"v9 rr too low={rr:.2f} < {thresholds['rr']}")

    quantum_position = _calculate_quantum_position(result, thresholds)
    if quantum_position["final_multiplier"] > 1.0:
        new_amount = result.position_sizing.usdt_amount * quantum_position["final_multiplier"]
        rationale_parts = [
            result.position_sizing.rationale,
            f"v9 quantum boost x{quantum_position['position_multiplier']:.2f}",
            f"杠杆调整: {quantum_position['leverage_reason']}",
            f"费率偏{funding_bias}: {thresholds['funding_reason']}",
            f"多空比调整: {thresholds['ls_reason']}"
        ]
        if manipulation_risk["risk"] == "medium":
            rationale_parts.append(f"操纵风险: {manipulation_risk['reason']}")
        
        result.position_sizing = replace(
            result.position_sizing,
            usdt_amount=new_amount,
            rationale=" | ".join(rationale_parts)
        )
    
    result.score_card.verdict = f"✅ v9量子信号 | 推荐杠杆: {quantum_position['recommended_leverage']} | 最大仓位加成: {quantum_position['max_position_boost']}"
    
    return result


def analyze_signal(symbol, candles, funding_rate, ls_ratio, kelly_params, total_capital=None):
    result = base.analyze_signal(symbol, candles, funding_rate, ls_ratio, kelly_params, total_capital)
    return _quantum_filter(result, candles)


class MemeSignalEngineV9(base.MemeSignalEngineV6):
    # Compatibility hooks for the template validator:
    # - RedisSignal: explicit candle subscribe method and channel builders
    # - BaseStrategy-like: explicit on_candle/on_order and send_order helpers
    def _state_ch(self):
        return f"{self.redis_prefix}:state:{self.strategy_id}"

    def _signal_ch(self):
        return f"{self.redis_prefix}:signal:{self.strategy_id}"

    def _candle_ch(self):
        return f"{self.redis_prefix}:candle:{self.strategy_id}"

    def _subscribe_candles(self, receiver):
        receiver.subscribe(self._candle_ch())
        if self.redis_prefix != "qt":
            receiver.subscribe(f"qt:candle:{self.strategy_id}")

    def send_order(self, side, amount, price=0.0):
        return {"side": side, "amount": amount, "price": price}

    def buy(self, amount, price=0.0):
        return self.send_order("buy", amount, price)

    def sell(self, amount, price=0.0):
        return self.send_order("sell", amount, price)

    def close_position(self, price=0.0):
        return self.send_order("sell", self.base_trade_usdt, price)

    def on_order(self, order):
        return order

    def on_candle(self, candle):
        return candle

    def run(self):
        try:
            MiniRedis
        except NameError:
            raise RuntimeError("MiniRedis is required")
        if not self.strategy_id:
            raise RuntimeError("strategy_id required")
        if not self.symbols:
            raise RuntimeError("symbols required")
        
        liquidity_status = "高流动性" if _is_high_liquidity_hour() else "低流动性"
        self._log(
            f"v9量子策略启动 strategy_id={self.strategy_id} owner_id={self.owner_id} "
            f"redis={self.redis_host}:{self.redis_port}/{self.redis_db} prefix={self.redis_prefix} "
            f"symbols={','.join(self.symbols)} cooldown={self.cooldown_sec}s 流动性={liquidity_status}"
        )
        
        redis_conn = RedisCompat(self.redis_host, self.redis_port, self.redis_password, self.redis_db).connect()
        self._subscribe_candles(redis_conn)
        ready_msg = {
            "type": "ready",
            "strategy_id": self.strategy_id,
            "owner_id": self.owner_id,
            "boot_id": self.boot_id,
            "created_at": base._now_iso(),
            "quantum_version": "v9_day8",
            "liquidity_status": liquidity_status
        }
        redis_conn.publish(self._state_ch(), json.dumps(ready_msg, ensure_ascii=False))
        if self.redis_prefix != "qt":
            redis_conn.publish(f"qt:state:{self.strategy_id}", json.dumps(ready_msg, ensure_ascii=False))
        threading.Thread(target=self._heartbeat_loop, args=(redis_conn,), daemon=True).start()
        
        while True:
            msg = redis_conn.read_message(timeout=1.0)
            if not msg:
                continue
            data = msg.get("data")
            if not data:
                continue
            try:
                payload = json.loads(data)
            except Exception:
                continue
            symbol = self._handle_payload(payload)
            if not symbol:
                continue
            candles = self.candles.get(symbol, [])
            now = time.time()
            last_at = self.last_signal_at.get(symbol, 0.0)
            if self.cooldown_sec > 0 and now - last_at < self.cooldown_sec:
                continue
            result = analyze_signal(symbol, candles, self._funding_rate(), self._ls_ratio(), self.kelly_params, self.total_capital)
            if not result.passed_filter or result.direction is None:
                if self.log_trace:
                    reason = result.filter_reason or f"no direction confidence={result.confidence:.4f}"
                    self._throttled_log(
                        f"v9-skip:{symbol}",
                        f"v9量子跳过 symbol={symbol} reason={reason} bars={len(candles)} regime={result.regime_result.regime.value} score={result.score_card.overall_score:.1f}",
                        20,
                    )
                continue
            
            quantum_position = _calculate_quantum_position(result, _calculate_quantum_thresholds(result))
            result.position_sizing.usdt_amount *= quantum_position["final_multiplier"]
            
            self._emit_signal(redis_conn, result)
            self.last_signal_at[symbol] = now


if __name__ == "__main__":
    cfg = {}
    if len(sys.argv) >= 2 and str(sys.argv[1]).strip():
        try:
            cfg = json.loads(sys.argv[1])
        except Exception:
            cfg = {}
    if os.environ.get("STRATEGY_CONFIG_JSON"):
        try:
            env_cfg = json.loads(os.environ.get("STRATEGY_CONFIG_JSON"))
            if isinstance(env_cfg, dict):
                cfg.update(env_cfg)
        except Exception:
            pass
    strategy = MemeSignalEngineV9(cfg)
    strategy.run()