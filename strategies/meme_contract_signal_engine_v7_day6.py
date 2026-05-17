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
base.Config.MIN_CONFIDENCE = 0.78
base.Config.TP_RATIO = 0.05
base.Config.SL_RATIO = 0.022
base.Config.WARMUP_BARS = 240
base.Config.SIGNAL_COOLDOWN_SEC = 900
base.Config.VOL_BOOST_THR = 1.2
base.Config.TF_ALIGN_BOOST = 0.20
base.Config.TF_CONFLICT_PENALTY = 0.28

base.ADAPTIVE_CONFIGS[base.MarketRegime.TRENDING_UP] = base.AdaptiveConfig(0.055, 0.022, 0.79, 0.03, 0.015, 0.9, "v7 trend long")
base.ADAPTIVE_CONFIGS[base.MarketRegime.TRENDING_DOWN] = base.AdaptiveConfig(0.055, 0.022, 0.79, 0.03, 0.015, 0.9, "v7 trend short")
base.ADAPTIVE_CONFIGS[base.MarketRegime.REVERSAL_UP] = base.AdaptiveConfig(0.045, 0.020, 0.84, 0.025, 0.012, 0.7, "v7 reversal long")
base.ADAPTIVE_CONFIGS[base.MarketRegime.REVERSAL_DOWN] = base.AdaptiveConfig(0.045, 0.020, 0.84, 0.025, 0.012, 0.7, "v7 reversal short")
base.ADAPTIVE_CONFIGS[base.MarketRegime.RANGING] = base.AdaptiveConfig(0.035, 0.018, 0.86, 0.018, 0.010, 0.5, "v7 ranging skip")
base.ADAPTIVE_CONFIGS[base.MarketRegime.UNKNOWN] = base.AdaptiveConfig(0.040, 0.020, 0.86, 0.020, 0.010, 0.5, "v7 unknown skip")


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


def _strict_filter(result: base.SignalResult) -> base.SignalResult:
    if not result.passed_filter or result.direction is None:
        return result

    regime = result.regime_result.regime
    if regime in (base.MarketRegime.RANGING, base.MarketRegime.UNKNOWN):
        return _reject(result, f"v7 skip regime={regime.value}")

    if result.score_card.overall_score < 68:
        return _reject(result, f"v7 score too low={result.score_card.overall_score:.1f}")

    if result.confidence < max(0.78, result.adaptive_cfg.min_confidence):
        return _reject(result, f"v7 confidence too low={result.confidence:.4f}")

    if result.atr_pct <= 0 or result.regime_result.atr_percentile >= 0.93:
        return _reject(result, "v7 volatility too extreme")

    if result.vol_ratio < 1.05 and not result.vol_boost:
        return _reject(result, f"v7 volume weak={result.vol_ratio:.2f}")

    if result.direction == "long":
        if result.ema_trend != "up":
            return _reject(result, "v7 need ema up")
        if result.cvd_trend == "down" or result.obv_trend == "down":
            return _reject(result, "v7 flow against long")
        if result.tf4h_direction and result.tf4h_direction != "long":
            return _reject(result, "v7 4h conflict long")
        if regime == base.MarketRegime.TRENDING_DOWN:
            return _reject(result, "v7 regime down")
    else:
        if result.ema_trend != "down":
            return _reject(result, "v7 need ema down")
        if result.cvd_trend == "up" or result.obv_trend == "up":
            return _reject(result, "v7 flow against short")
        if result.tf4h_direction and result.tf4h_direction != "short":
            return _reject(result, "v7 4h conflict short")
        if regime == base.MarketRegime.TRENDING_UP:
            return _reject(result, "v7 regime up")

    if regime in (base.MarketRegime.REVERSAL_UP, base.MarketRegime.REVERSAL_DOWN):
        need_div = "bullish" if result.direction == "long" else "bearish"
        if result.confidence < 0.84:
            return _reject(result, "v7 reversal confidence low")
        if result.obv_divergence != need_div:
            return _reject(result, "v7 reversal needs divergence")

    if _rr(result) < 1.6:
        return _reject(result, "v7 rr too low")

    return result


def analyze_signal(symbol, candles, funding_rate, ls_ratio, kelly_params, total_capital=None):
    result = base.analyze_signal(symbol, candles, funding_rate, ls_ratio, kelly_params, total_capital)
    return _strict_filter(result)


class MemeSignalEngineV7(base.MemeSignalEngineV6):
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
            f"v7 strategy start strategy_id={self.strategy_id} owner_id={self.owner_id} "
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
                        f"v7-skip:{symbol}",
                        f"v7 skip symbol={symbol} reason={reason} bars={len(candles)} regime={result.regime_result.regime.value} score={result.score_card.overall_score:.1f}",
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
    strategy = MemeSignalEngineV7(cfg)
    strategy.run()
