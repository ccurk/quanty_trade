import json
import os
import sys
import threading
import time
from dataclasses import replace

try:
    from mini_redis import MiniRedis
except Exception:
    pass

from redis_compat import RedisCompat
import meme_contract_signal_engine_v6_day5 as base


base.Config.MAX_PRICE = 5.0
base.Config.MIN_PRECISION = 5
base.Config.MIN_VOLATILITY = 7.0
base.Config.MIN_CONFIDENCE = 0.72
base.Config.TP_RATIO = 0.05
base.Config.SL_RATIO = 0.022
base.Config.WARMUP_BARS = 240
base.Config.SIGNAL_COOLDOWN_SEC = 900
base.Config.VOL_BOOST_THR = 1.15
base.Config.TF_ALIGN_BOOST = 0.20
base.Config.TF_CONFLICT_PENALTY = 0.28


base.ADAPTIVE_CONFIGS[base.MarketRegime.TRENDING_UP] = base.AdaptiveConfig(0.055, 0.022, 0.74, 0.03, 0.015, 0.9, "v8 trend long")
base.ADAPTIVE_CONFIGS[base.MarketRegime.TRENDING_DOWN] = base.AdaptiveConfig(0.055, 0.022, 0.74, 0.03, 0.015, 0.9, "v8 trend short")
base.ADAPTIVE_CONFIGS[base.MarketRegime.REVERSAL_UP] = base.AdaptiveConfig(0.045, 0.020, 0.78, 0.025, 0.012, 0.7, "v8 reversal long")
base.ADAPTIVE_CONFIGS[base.MarketRegime.REVERSAL_DOWN] = base.AdaptiveConfig(0.045, 0.020, 0.78, 0.025, 0.012, 0.7, "v8 reversal short")
base.ADAPTIVE_CONFIGS[base.MarketRegime.RANGING] = base.AdaptiveConfig(0.035, 0.018, 0.82, 0.018, 0.010, 0.5, "v8 ranging skip")
base.ADAPTIVE_CONFIGS[base.MarketRegime.UNKNOWN] = base.AdaptiveConfig(0.040, 0.020, 0.82, 0.020, 0.010, 0.5, "v8 unknown skip")


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


def _calculate_dynamic_thresholds(result: base.SignalResult):
    regime = result.regime_result.regime
    atr_percentile = result.regime_result.atr_percentile
    
    if regime in (base.MarketRegime.TRENDING_UP, base.MarketRegime.TRENDING_DOWN):
        min_score = 64
        min_confidence = 0.72
        min_rr = 1.3
        min_vol_ratio = 0.95
        
        if atr_percentile > 0.8:
            min_score = 62
            min_confidence = 0.70
            min_rr = 1.2
            min_vol_ratio = 0.90
            
    elif regime in (base.MarketRegime.REVERSAL_UP, base.MarketRegime.REVERSAL_DOWN):
        min_score = 66
        min_confidence = 0.78
        min_rr = 1.4
        min_vol_ratio = 1.0
        
        if atr_percentile > 0.8:
            min_score = 64
            min_confidence = 0.76
            min_rr = 1.3
            min_vol_ratio = 0.95
            
    else:
        min_score = 68
        min_confidence = 0.82
        min_rr = 1.6
        min_vol_ratio = 1.05
        
    return {
        "min_score": min_score,
        "min_confidence": min_confidence,
        "min_rr": min_rr,
        "min_vol_ratio": min_vol_ratio
    }


def _calculate_position_multiplier(result: base.SignalResult):
    regime = result.regime_result.regime
    confidence = result.confidence
    score = result.score_card.overall_score
    rr = _rr(result)
    
    base_multiplier = 1.0
    
    if regime in (base.MarketRegime.TRENDING_UP, base.MarketRegime.TRENDING_DOWN):
        if confidence > 0.78 and score > 70 and rr > 1.5:
            base_multiplier = 1.2
        elif confidence > 0.75 and score > 66 and rr > 1.3:
            base_multiplier = 1.1
            
    elif regime in (base.MarketRegime.REVERSAL_UP, base.MarketRegime.REVERSAL_DOWN):
        if confidence > 0.82 and score > 72 and rr > 1.6:
            base_multiplier = 1.3
        elif confidence > 0.80 and score > 68 and rr > 1.4:
            base_multiplier = 1.15
            
    if result.tf_aligned and result.tf_boost > 0.15:
        base_multiplier *= 1.1
        
    if result.vol_boost:
        base_multiplier *= 1.05
        
    return min(base_multiplier, 1.5)


def _smart_filter(result: base.SignalResult) -> base.SignalResult:
    if not result.passed_filter or result.direction is None:
        return result

    regime = result.regime_result.regime
    thresholds = _calculate_dynamic_thresholds(result)
    
    if regime in (base.MarketRegime.RANGING, base.MarketRegime.UNKNOWN):
        if result.confidence < 0.80 or result.score_card.overall_score < 70:
            return _reject(result, f"v8 skip regime={regime.value} conf={result.confidence:.4f} score={result.score_card.overall_score:.1f}")

    if result.score_card.overall_score < thresholds["min_score"]:
        return _reject(result, f"v8 score too low={result.score_card.overall_score:.1f} < {thresholds['min_score']}")

    min_confidence = max(thresholds["min_confidence"], result.adaptive_cfg.min_confidence)
    if result.confidence < min_confidence:
        return _reject(result, f"v8 confidence too low={result.confidence:.4f} < {min_confidence:.4f}")

    if result.atr_pct <= 0 or result.regime_result.atr_percentile >= 0.95:
        return _reject(result, "v8 volatility too extreme")

    if result.vol_ratio < thresholds["min_vol_ratio"] and not result.vol_boost:
        return _reject(result, f"v8 volume weak={result.vol_ratio:.2f} < {thresholds['min_vol_ratio']}")

    if result.direction == "long":
        if result.ema_trend != "up":
            if result.confidence < 0.78:
                return _reject(result, "v8 need ema up for long")
        if result.cvd_trend == "down" or result.obv_trend == "down":
            if result.confidence < 0.80:
                return _reject(result, "v8 flow against long")
        if result.tf4h_direction and result.tf4h_direction != "long":
            if result.confidence < 0.82:
                return _reject(result, "v8 4h conflict long")
        if regime == base.MarketRegime.TRENDING_DOWN:
            if result.confidence < 0.85:
                return _reject(result, "v8 regime down for long")
    else:
        if result.ema_trend != "down":
            if result.confidence < 0.78:
                return _reject(result, "v8 need ema down for short")
        if result.cvd_trend == "up" or result.obv_trend == "up":
            if result.confidence < 0.80:
                return _reject(result, "v8 flow against short")
        if result.tf4h_direction and result.tf4h_direction != "short":
            if result.confidence < 0.82:
                return _reject(result, "v8 4h conflict short")
        if regime == base.MarketRegime.TRENDING_UP:
            if result.confidence < 0.85:
                return _reject(result, "v8 regime up for short")

    if regime in (base.MarketRegime.REVERSAL_UP, base.MarketRegime.REVERSAL_DOWN):
        need_div = "bullish" if result.direction == "long" else "bearish"
        if result.confidence < 0.82:
            return _reject(result, "v8 reversal confidence low")
        if result.obv_divergence != need_div:
            if result.confidence < 0.85:
                return _reject(result, "v8 reversal needs divergence")

    rr = _rr(result)
    if rr < thresholds["min_rr"]:
        return _reject(result, f"v8 rr too low={rr:.2f} < {thresholds['min_rr']}")

    position_multiplier = _calculate_position_multiplier(result)
    if position_multiplier > 1.0:
        result.position_sizing = replace(
            result.position_sizing,
            usdt_amount=result.position_sizing.usdt_amount * position_multiplier,
            rationale=f"{result.position_sizing.rationale} (v8 position boost x{position_multiplier:.2f})"
        )

    return result


def analyze_signal(symbol, candles, funding_rate, ls_ratio, kelly_params, total_capital=None):
    result = base.analyze_signal(symbol, candles, funding_rate, ls_ratio, kelly_params, total_capital)
    return _smart_filter(result)


class MemeSignalEngineV8(base.MemeSignalEngineV6):
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
        self._log(
            f"v8 strategy start strategy_id={self.strategy_id} owner_id={self.owner_id} "
            f"redis={self.redis_host}:{self.redis_port}/{self.redis_db} prefix={self.redis_prefix} "
            f"symbols={','.join(self.symbols)} cooldown={self.cooldown_sec}s"
        )
        redis_conn = RedisCompat(self.redis_host, self.redis_port, self.redis_password, self.redis_db).connect()
        self._subscribe_candles(redis_conn)
        ready_msg = {
            "type": "ready",
            "strategy_id": self.strategy_id,
            "owner_id": self.owner_id,
            "boot_id": self.boot_id,
            "created_at": base._now_iso(),
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
                        f"v8-skip:{symbol}",
                        f"v8 skip symbol={symbol} reason={reason} bars={len(candles)} regime={result.regime_result.regime.value} score={result.score_card.overall_score:.1f}",
                        20,
                    )
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
    strategy = MemeSignalEngineV8(cfg)
    strategy.run()