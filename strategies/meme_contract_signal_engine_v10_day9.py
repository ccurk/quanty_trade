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
base.Config.MIN_VOLATILITY = 6.0
base.Config.MIN_CONFIDENCE = 0.62
base.Config.TP_RATIO = 0.06
base.Config.SL_RATIO = 0.028
base.Config.WARMUP_BARS = 180
base.Config.SIGNAL_COOLDOWN_SEC = 300
base.Config.VOL_BOOST_THR = 1.05
base.Config.TF_ALIGN_BOOST = 0.30
base.Config.TF_CONFLICT_PENALTY = 0.25


base.ADAPTIVE_CONFIGS[base.MarketRegime.TRENDING_UP] = base.AdaptiveConfig(0.065, 0.028, 0.66, 0.04, 0.02, 1.0, "v10 trend long")
base.ADAPTIVE_CONFIGS[base.MarketRegime.TRENDING_DOWN] = base.AdaptiveConfig(0.065, 0.028, 0.66, 0.04, 0.02, 1.0, "v10 trend short")
base.ADAPTIVE_CONFIGS[base.MarketRegime.REVERSAL_UP] = base.AdaptiveConfig(0.055, 0.025, 0.69, 0.035, 0.018, 0.8, "v10 reversal long")
base.ADAPTIVE_CONFIGS[base.MarketRegime.REVERSAL_DOWN] = base.AdaptiveConfig(0.055, 0.025, 0.69, 0.035, 0.018, 0.8, "v10 reversal short")
base.ADAPTIVE_CONFIGS[base.MarketRegime.RANGING] = base.AdaptiveConfig(0.045, 0.022, 0.70, 0.025, 0.015, 0.6, "v10 ranging relaxed")
base.ADAPTIVE_CONFIGS[base.MarketRegime.UNKNOWN] = base.AdaptiveConfig(0.050, 0.025, 0.70, 0.028, 0.016, 0.6, "v10 unknown relaxed")


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
    if funding_rate < -0.0008:
        return {"bias": "long", "adjustment": 0.08, "reason": "极高负费率强烈偏好做多"}
    elif funding_rate > 0.0008:
        return {"bias": "short", "adjustment": 0.08, "reason": "极高正费率强烈偏好做空"}
    elif funding_rate < -0.0003:
        return {"bias": "long", "adjustment": 0.04, "reason": "负费率偏好做多"}
    elif funding_rate > 0.0003:
        return {"bias": "short", "adjustment": 0.04, "reason": "正费率偏好做空"}
    else:
        return {"bias": "neutral", "adjustment": 0.0, "reason": "费率中性"}


def _calculate_leverage_adjustment(atr_percentile, volatility):
    base_leverage = 4.0
    
    if atr_percentile > 0.9:
        leverage = max(1.5, base_leverage * 0.4)
        return {"leverage": leverage, "reason": f"极高波动大幅降低杠杆至{leverage:.1f}x"}
    elif atr_percentile > 0.8:
        leverage = base_leverage * 0.6
        return {"leverage": leverage, "reason": f"高波动降低杠杆至{leverage:.1f}x"}
    elif volatility < 4.0:
        leverage = min(6.0, base_leverage * 1.3)
        return {"leverage": leverage, "reason": f"极低波动适当提高至{leverage:.1f}x"}
    elif volatility < 6.0:
        leverage = min(5.0, base_leverage * 1.15)
        return {"leverage": leverage, "reason": f"低波动适当提高至{leverage:.1f}x"}
    else:
        return {"leverage": base_leverage, "reason": f"标准杠杆{base_leverage:.1f}x"}


def _detect_manipulation_risk(result, candles):
    if len(candles) < 15:
        return {"risk": "low", "reason": "数据不足"}
    
    recent_prices = [c["close"] for c in candles[-8:]]
    recent_volumes = [c["volume"] for c in candles[-8:]]
    
    price_change = abs((recent_prices[-1] - recent_prices[0]) / recent_prices[0])
    volume_change = abs((recent_volumes[-1] - recent_volumes[0]) / recent_volumes[0])
    
    if price_change > 0.20 and volume_change < 0.25:
        return {"risk": "high", "reason": f"价格异常波动{price_change:.1%}但成交量未跟上"}
    elif price_change > 0.30:
        return {"risk": "high", "reason": f"价格剧烈波动{price_change:.1%}"}
    elif result.vol_ratio < 0.6:
        return {"risk": "medium", "reason": f"成交量极低{result.vol_ratio:.2f}"}
    elif result.vol_ratio < 0.8:
        return {"risk": "low", "reason": f"成交量较低{result.vol_ratio:.2f}"}
    else:
        return {"risk": "low", "reason": "市场行为正常"}


def _calculate_ls_ratio_adjustment(ls_ratio):
    if ls_ratio > 3.0:
        return {"adjustment": -0.12, "reason": f"极端多空比{ls_ratio:.2f}，强烈反向信号"}
    elif ls_ratio > 2.5:
        return {"adjustment": -0.08, "reason": f"极高多空比{ls_ratio:.2f}，反向信号增强"}
    elif ls_ratio > 2.0:
        return {"adjustment": -0.05, "reason": f"高多空比{ls_ratio:.2f}，谨慎做多"}
    elif ls_ratio < 0.3:
        return {"adjustment": 0.12, "reason": f"极端低多空比{ls_ratio:.2f}，强烈反向信号"}
    elif ls_ratio < 0.5:
        return {"adjustment": 0.08, "reason": f"极低多空比{ls_ratio:.2f}，反向信号增强"}
    elif ls_ratio < 0.7:
        return {"adjustment": 0.05, "reason": f"低多空比{ls_ratio:.2f}，谨慎做空"}
    else:
        return {"adjustment": 0.0, "reason": "多空比正常"}


def _calculate_aggressive_thresholds(result):
    regime = result.regime_result.regime
    atr_percentile = result.regime_result.atr_percentile
    funding_rate = result.funding_rate
    ls_ratio = result.ls_ratio
    
    base_thresholds = {
        "trending": {"score": 56, "confidence": 0.63, "rr": 1.05, "vol_ratio": 0.80},
        "reversal": {"score": 58, "confidence": 0.68, "rr": 1.10, "vol_ratio": 0.85},
        "ranging": {"score": 60, "confidence": 0.70, "rr": 1.20, "vol_ratio": 0.90},
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
    
    if atr_percentile > 0.75:
        thresholds["score"] = max(52, thresholds["score"] - 4)
        thresholds["confidence"] = max(0.60, thresholds["confidence"] - 0.04)
        thresholds["rr"] = max(1.0, thresholds["rr"] - 0.1)
        thresholds["vol_ratio"] = max(0.75, thresholds["vol_ratio"] - 0.05)
    
    if not _is_high_liquidity_hour():
        thresholds["score"] += 1
        thresholds["confidence"] += 0.0
        thresholds["rr"] += 0.03
        thresholds["vol_ratio"] += 0.02
    
    thresholds["confidence"] = max(thresholds["confidence"] + total_adjustment, 0.60)
    
    thresholds["funding_bias"] = funding_adj["bias"]
    thresholds["funding_reason"] = funding_adj["reason"]
    thresholds["ls_adjustment"] = ls_adj["adjustment"]
    thresholds["ls_reason"] = ls_adj["reason"]
    thresholds["regime_key"] = regime_key
    
    return thresholds


def _calculate_aggressive_position(result, thresholds):
    regime = result.regime_result.regime
    confidence = result.confidence
    score = result.score_card.overall_score
    rr = _rr(result)
    atr_percentile = result.regime_result.atr_percentile
    
    base_multiplier = 1.0
    
    leverage_adj = _calculate_leverage_adjustment(atr_percentile, result.atr_pct)
    leverage = leverage_adj["leverage"]
    
    if regime in (base.MarketRegime.TRENDING_UP, base.MarketRegime.TRENDING_DOWN):
        if confidence > 0.72 and score > 62 and rr > 1.3:
            base_multiplier = 1.4
        elif confidence > 0.68 and score > 58 and rr > 1.2:
            base_multiplier = 1.2
            
        if thresholds["funding_bias"] == "long" and result.direction == "long":
            base_multiplier *= 1.10
        elif thresholds["funding_bias"] == "short" and result.direction == "short":
            base_multiplier *= 1.10
            
    elif regime in (base.MarketRegime.REVERSAL_UP, base.MarketRegime.REVERSAL_DOWN):
        if confidence > 0.76 and score > 66 and rr > 1.5:
            base_multiplier = 1.5
        elif confidence > 0.74 and score > 62 and rr > 1.4:
            base_multiplier = 1.3
            
        if thresholds["ls_adjustment"] > 0 and result.direction == "long":
            base_multiplier *= (1 + thresholds["ls_adjustment"])
        elif thresholds["ls_adjustment"] < 0 and result.direction == "short":
            base_multiplier *= (1 - thresholds["ls_adjustment"])
    
    if result.tf_aligned and result.tf_boost > 0.15:
        base_multiplier *= 1.15
        
    if result.vol_boost:
        base_multiplier *= 1.08
    
    position_multiplier = min(base_multiplier, 1.8)
    
    leverage_factor = 4.0 / leverage
    final_multiplier = position_multiplier * leverage_factor
    
    return {
        "position_multiplier": position_multiplier,
        "leverage": leverage,
        "leverage_reason": leverage_adj["reason"],
        "final_multiplier": final_multiplier,
        "recommended_leverage": f"{leverage:.1f}x",
        "max_position_boost": "80%"
    }


def _aggressive_filter(result: base.SignalResult, candles) -> base.SignalResult:
    if not result.passed_filter or result.direction is None:
        return result

    regime = result.regime_result.regime
    confidence = result.confidence
    thresholds = _calculate_aggressive_thresholds(result)
    
    manipulation_risk = _detect_manipulation_risk(result, candles)
    if manipulation_risk["risk"] == "high":
        if confidence < 0.68:
            return _reject(result, f"v10 市场操纵风险: {manipulation_risk['reason']}")
    
    if not _is_high_liquidity_hour():
        if confidence < 0.66:
            return _reject(result, "v10 低流动性时段需要更高置信度")
    
    if regime in (base.MarketRegime.RANGING, base.MarketRegime.UNKNOWN):
        if confidence < 0.68 or result.score_card.overall_score < 60:
            return _reject(result, f"v10 skip regime={regime.value} conf={result.confidence:.4f} score={result.score_card.overall_score:.1f}")

    if result.score_card.overall_score < thresholds["score"]:
        return _reject(result, f"v10 score too low={result.score_card.overall_score:.1f} < {thresholds['score']}")

    min_confidence = max(thresholds["confidence"], result.adaptive_cfg.min_confidence)
    if result.confidence < min_confidence:
        return _reject(result, f"v10 confidence too low={result.confidence:.4f} < {min_confidence:.4f}")

    if result.atr_pct <= 0 or result.regime_result.atr_percentile >= 0.98:
        return _reject(result, "v10 volatility too extreme")

    if result.vol_ratio < thresholds["vol_ratio"] and not result.vol_boost:
        if confidence < 0.72:
            return _reject(result, f"v10 volume weak={result.vol_ratio:.2f} < {thresholds['vol_ratio']}")

    funding_bias = thresholds["funding_bias"]
    if funding_bias == "long" and result.direction == "short":
        if confidence < 0.74:
            return _reject(result, f"v10 费率偏多时做空需要更高置信度: {thresholds['funding_reason']}")
    elif funding_bias == "short" and result.direction == "long":
        if confidence < 0.74:
            return _reject(result, f"v10 费率偏空时做多需要更高置信度: {thresholds['funding_reason']}")

    if result.direction == "long":
        if result.ema_trend != "up":
            if confidence < 0.70:
                return _reject(result, "v10 need ema up for long")
        if result.cvd_trend == "down" or result.obv_trend == "down":
            if confidence < 0.73:
                return _reject(result, "v10 flow against long")
        if result.tf4h_direction and result.tf4h_direction != "long":
            if confidence < 0.76:
                return _reject(result, "v10 4h conflict long")
        if regime == base.MarketRegime.TRENDING_DOWN:
            if confidence < 0.78:
                return _reject(result, "v10 regime down for long")
    else:
        if result.ema_trend != "down":
            if confidence < 0.70:
                return _reject(result, "v10 need ema down for short")
        if result.cvd_trend == "up" or result.obv_trend == "up":
            if confidence < 0.73:
                return _reject(result, "v10 flow against short")
        if result.tf4h_direction and result.tf4h_direction != "short":
            if confidence < 0.76:
                return _reject(result, "v10 4h conflict short")
        if regime == base.MarketRegime.TRENDING_UP:
            if confidence < 0.78:
                return _reject(result, "v10 regime up for short")

    if regime in (base.MarketRegime.REVERSAL_UP, base.MarketRegime.REVERSAL_DOWN):
        need_div = "bullish" if result.direction == "long" else "bearish"
        if confidence < 0.72:
            return _reject(result, "v10 reversal confidence low")
        if result.obv_divergence != need_div:
            if confidence < 0.78:
                return _reject(result, "v10 reversal needs divergence")

    rr = _rr(result)
    if rr < thresholds["rr"]:
        if confidence < 0.76:
            return _reject(result, f"v10 rr too low={rr:.2f} < {thresholds['rr']}")

    aggressive_position = _calculate_aggressive_position(result, thresholds)
    if aggressive_position["final_multiplier"] > 1.0:
        new_amount = result.position_sizing.usdt_amount * aggressive_position["final_multiplier"]
        rationale_parts = [
            result.position_sizing.rationale,
            f"v10激进信号加成 x{aggressive_position['position_multiplier']:.2f}",
            f"杠杆调整: {aggressive_position['leverage_reason']}",
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
    
    result.score_card.verdict = f"🚀 v10激进信号 | 推荐杠杆: {aggressive_position['recommended_leverage']} | 最大仓位加成: {aggressive_position['max_position_boost']}"
    
    return result


def analyze_signal(symbol, candles, funding_rate, ls_ratio, kelly_params, total_capital=None):
    result = base.analyze_signal(symbol, candles, funding_rate, ls_ratio, kelly_params, total_capital)
    return _aggressive_filter(result, candles)


class MemeSignalEngineV10(base.MemeSignalEngineV6):
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

    def _warmup_log(self, symbol, candles):
        if not self.log_trace:
            return
        history = self.history_count.get(symbol, 0)
        realtime = self.realtime_count.get(symbol, 0)
        self._throttled_log(
            f"v10-warmup:{symbol}",
            f"v10等待样本 symbol={symbol} bars={len(candles)}/{base.Config.WARMUP_BARS} history={history} realtime={realtime}",
            20,
        )

    def _cooldown_log(self, symbol, candles, remaining_sec):
        if not self.log_trace:
            return
        self._throttled_log(
            f"v10-cooldown:{symbol}",
            f"v10冷却中 symbol={symbol} remaining={max(0.0, remaining_sec):.1f}s bars={len(candles)}",
            20,
        )

    def _skip_log(self, symbol, candles, result):
        if not self.log_trace:
            return
        reason = result.filter_reason or f"no direction confidence={result.confidence:.4f}"
        history = self.history_count.get(symbol, 0)
        realtime = self.realtime_count.get(symbol, 0)
        self._throttled_log(
            f"v10-skip:{symbol}",
            f"v10激进跳过 symbol={symbol} reason={reason} bars={len(candles)} history={history} realtime={realtime} regime={result.regime_result.regime.value} score={result.score_card.overall_score:.1f}",
            20,
        )

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
            f"v10激进策略启动 strategy_id={self.strategy_id} owner_id={self.owner_id} "
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
            "aggressive_version": "v10_day9",
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
            if len(candles) < base.Config.WARMUP_BARS:
                self._warmup_log(symbol, candles)
                continue
            now = time.time()
            last_at = self.last_signal_at.get(symbol, 0.0)
            if self.cooldown_sec > 0 and now - last_at < self.cooldown_sec:
                self._cooldown_log(symbol, candles, self.cooldown_sec - (now - last_at))
                continue
            result = analyze_signal(symbol, candles, self._funding_rate(), self._ls_ratio(), self.kelly_params, self.total_capital)
            if not result.passed_filter or result.direction is None:
                self._skip_log(symbol, candles, result)
                continue
            
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
    strategy = MemeSignalEngineV10(cfg)
    strategy.run()
