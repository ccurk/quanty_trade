"""
fee_aware_breakout.py — 低频、扣费纪律优先的趋势突破策略

为什么是这个：60 天真实账户数据显示，旧策略毛盈亏≈0(+0.78 USDT)、手续费 −17.90 USDT
(毛利的 23 倍)、100% 吃单、换手 730×本金、出场太紧(MFE 远大于落袋)。结论是
"亏在纪律不在指标"。本脚本把纪律写进代码：

  1) 罕见入场     —— 唐奇安通道突破 + EMA 趋势过滤 + 每标的冷却期 → 直接砍换手
  2) 手续费门槛   —— TP 距离必须 ≥ min_tp_over_cost × 一个来回成本(费+点差) → TP 远大于费
  3) 出场放宽     —— TP 按 ATR 自适应、RR≥rr_min → 拿住有利波动，别再被磨

它与平台契约完全对齐(见 redis_signal_template.py)：订阅 {prefix}:candle:{id} 收 K 线，
向 {prefix}:signal:{id} 发 {action:"open", side, amount, take_profit, stop_loss}，
出场由后端 TP/SL 监控负责。

诚实声明：这不是"稳赚"策略。我没有、也不会假装有已验证的 edge。它的价值是结构性的——
不让手续费放干你。**上实盘前必须用扣费回测(把 taker×2+点差 算进去)验证它在你的标的上
扣费后仍是正期望**；若不是，说明这个频率/打法在这些微盘上数学上不成立，需要换思路。
"""

import json
import os
import sys
import time
import threading
from collections import deque
from datetime import datetime, timezone

from mini_redis import MiniRedis


def _f(v, d=0.0):
    try:
        return float(d) if v is None else float(v)
    except Exception:
        return float(d)


def _i(v, d=0):
    try:
        return int(d) if v is None else int(float(v))
    except Exception:
        return int(d)


def _now():
    return datetime.now(tz=timezone.utc).isoformat()


class _SymbolState:
    """每个标的的滚动行情与节流状态。"""

    def __init__(self, maxlen):
        self.highs = deque(maxlen=maxlen)
        self.lows = deque(maxlen=maxlen)
        self.closes = deque(maxlen=maxlen)
        self.prev_close = None
        self.ema_fast = None
        self.ema_slow = None
        self.atr = None
        self.bar_idx = 0
        self.last_entry_bar = -10 ** 9


class FeeAwareBreakout:
    def __init__(self, config):
        self.cfg = config or {}
        self.strategy_id = str(self.cfg.get("strategy_id") or "").strip()
        self.owner_id = int(self.cfg.get("owner_id") or 0)
        self.prefix = str(self.cfg.get("redis_prefix") or os.getenv("REDIS_PREFIX") or "qt").strip() or "qt"
        self.redis_addr = str(self.cfg.get("redis_addr") or os.getenv("REDIS_ADDR") or "127.0.0.1:6379").strip()
        self.redis_password = str(self.cfg.get("redis_password") or os.getenv("REDIS_PASSWORD") or "")
        self.redis_db = _i(self.cfg.get("redis_db") if self.cfg.get("redis_db") is not None else os.getenv("REDIS_DB"), 0)

        # ---- 策略参数(全部可被平台 config 覆盖)----
        self.channel_window = max(5, _i(self.cfg.get("channel_window", 30), 30))   # 唐奇安回看
        self.ema_fast_n = max(2, _i(self.cfg.get("ema_fast", 12), 12))
        self.ema_slow_n = max(self.ema_fast_n + 1, _i(self.cfg.get("ema_slow", 48), 48))
        self.atr_window = max(2, _i(self.cfg.get("atr_window", 20), 20))

        # ---- 扣费纪律参数 ----
        self.taker_fee = _f(self.cfg.get("taker_fee", 0.00035), 0.00035)          # 实测单边 ~0.033%
        self.est_spread = _f(self.cfg.get("est_spread", 0.0015), 0.0015)          # 微盘点差估计，宁可高估
        self.min_tp_over_cost = _f(self.cfg.get("min_tp_over_cost", 6.0), 6.0)    # TP 至少是一个来回成本的几倍
        self.rr_min = max(1.0, _f(self.cfg.get("rr_min", 1.8), 1.8))             # 风险回报比下限
        self.atr_tp_mult = _f(self.cfg.get("atr_tp_mult", 2.5), 2.5)            # TP = atr_tp_mult × ATR(下限受手续费门槛约束)
        self.cooldown_bars = max(0, _i(self.cfg.get("cooldown_bars", 24), 24))  # 同标的两次入场最小间隔(根)
        self.allow_short = bool(self.cfg.get("allow_short", True))

        self.trade_amount = _f(self.cfg.get("trade_amount", 0.0), 0.0)           # 0 = 用名义额换算
        self.trade_notional = _f(self.cfg.get("trade_notional", 10.0), 10.0)     # 单笔目标名义额(USDT)

        # 一个来回的总成本(fraction)：进+出各一次 taker，再加一次有效点差
        self.round_trip_cost = 2.0 * self.taker_fee + self.est_spread

        self.warmup = max(self.channel_window, self.ema_slow_n, self.atr_window) + 2

        self.symbols = []
        raw = self.cfg.get("symbols")
        if isinstance(raw, list):
            self.symbols = [s.strip() for s in raw if isinstance(s, str) and s.strip()]
        if not self.symbols:
            sym = str(self.cfg.get("symbol") or "").strip()
            if sym:
                self.symbols = [sym]
        self.state = {s: _SymbolState(self.warmup + 5) for s in self.symbols}

        host, port = (self.redis_addr.split(":") + ["6379"])[:2]
        self.r = MiniRedis(host=host, port=int(port), password=self.redis_password, db=self.redis_db).connect()
        self.pub = MiniRedis(host=host, port=int(port), password=self.redis_password, db=self.redis_db).connect()
        self.boot_id = f"{int(time.time() * 1000)}-{os.getpid()}"
        self.healthcheck = self.cfg.get("healthcheck") or {}

    # ---- 通道 ----
    def _candle_ch(self):
        return f"{self.prefix}:candle:{self.strategy_id}"

    def _signal_ch(self):
        return f"{self.prefix}:signal:{self.strategy_id}"

    def _state_ch(self):
        return f"{self.prefix}:state:{self.strategy_id}"

    def _log(self, msg):
        sys.stdout.write(json.dumps({"type": "log", "data": msg}) + "\n")
        sys.stdout.flush()

    def _heartbeat_loop(self):
        interval = 5
        if isinstance(self.healthcheck, dict):
            interval = max(1, _i(self.healthcheck.get("interval_sec"), 5))
        while True:
            try:
                self.pub.publish(self._state_ch(), json.dumps({"type": "heartbeat", "strategy_id": self.strategy_id, "boot_id": self.boot_id, "created_at": _now()}))
            except Exception:
                pass
            time.sleep(interval)

    def _emit_signal(self, symbol, side, amount, take_profit, stop_loss):
        msg = {
            "strategy_id": self.strategy_id,
            "owner_id": self.owner_id,
            "symbol": symbol,
            "action": "open",
            "side": side,
            "amount": float(amount),
            "take_profit": float(take_profit),
            "stop_loss": float(stop_loss),
            "signal_id": f"{self.strategy_id}:{symbol}:{int(time.time() * 1000)}",
            "generated_at": _now(),
        }
        self.r.publish(self._signal_ch(), json.dumps(msg))
        self._log(f"OPEN {side} {symbol} qty={amount} tp={take_profit:.8f} sl={stop_loss:.8f} cost_rt={self.round_trip_cost:.4%}")

    @staticmethod
    def _ema(prev, price, n):
        if prev is None:
            return price
        k = 2.0 / (n + 1.0)
        return price * k + prev * (1.0 - k)

    def on_candle(self, candle):
        if not isinstance(candle, dict):
            return
        if candle.get("type") == "history":
            for it in (candle.get("candles") or []):
                self.on_candle(it)
            return

        symbol = str(candle.get("symbol") or "").strip()
        st = self.state.get(symbol)
        if st is None:
            return
        close = _f(candle.get("close", 0), 0.0)
        high = _f(candle.get("high", close), close)
        low = _f(candle.get("low", close), close)
        if close <= 0:
            return

        st.bar_idx += 1
        # ATR(真实波幅，含跨根跳空)
        if st.prev_close is not None:
            tr = max(high - low, abs(high - st.prev_close), abs(low - st.prev_close))
            st.atr = tr if st.atr is None else (st.atr * (self.atr_window - 1) + tr) / self.atr_window
        st.ema_fast = self._ema(st.ema_fast, close, self.ema_fast_n)
        st.ema_slow = self._ema(st.ema_slow, close, self.ema_slow_n)

        # 用"上一根之前"的通道做突破判定，避免用当根自身造成前视
        prior_high = max(st.highs) if len(st.highs) >= self.channel_window else None
        prior_low = min(st.lows) if len(st.lows) >= self.channel_window else None

        st.highs.append(high)
        st.lows.append(low)
        st.closes.append(close)
        st.prev_close = close

        if st.bar_idx < self.warmup or st.atr is None or prior_high is None or prior_low is None:
            return
        if st.bar_idx - st.last_entry_bar < self.cooldown_bars:
            return  # 冷却：直接砍换手
        if st.atr <= 0:
            return

        # TP 距离(fraction)：手续费门槛与 ATR 自适应取较大者
        atr_frac = st.atr / close
        min_tp_frac = self.min_tp_over_cost * self.round_trip_cost
        tp_frac = max(min_tp_frac, self.atr_tp_mult * atr_frac)
        sl_frac = tp_frac / self.rr_min

        side = None
        if close > prior_high and st.ema_fast > st.ema_slow:
            side = "buy"
        elif self.allow_short and close < prior_low and st.ema_fast < st.ema_slow:
            side = "sell"
        if side is None:
            return

        amount = self.trade_amount if self.trade_amount > 0 else max(0.0, self.trade_notional / close)
        if amount <= 0:
            return

        if side == "buy":
            tp = close * (1.0 + tp_frac)
            sl = close * (1.0 - sl_frac)
        else:
            tp = close * (1.0 - tp_frac)
            sl = close * (1.0 + sl_frac)

        st.last_entry_bar = st.bar_idx
        self._emit_signal(symbol, side, amount, tp, sl)

    def run(self):
        if not self.strategy_id:
            raise RuntimeError("missing strategy_id")
        self.r.subscribe(self._candle_ch())
        self.pub.publish(self._state_ch(), json.dumps({"type": "ready", "strategy_id": self.strategy_id, "boot_id": self.boot_id, "created_at": _now()}))
        threading.Thread(target=self._heartbeat_loop, daemon=True).start()
        self._log(f"FEE_AWARE_BREAKOUT started id={self.strategy_id} symbols={len(self.symbols)} round_trip_cost={self.round_trip_cost:.4%} min_tp={self.min_tp_over_cost * self.round_trip_cost:.4%} cooldown={self.cooldown_bars}")
        while True:
            item = self.r.read_pubsub_message()
            if not item:
                continue
            payload = item.get("data")
            if not payload:
                continue
            try:
                msg = json.loads(payload)
            except Exception:
                continue
            try:
                self.on_candle(msg)
            except Exception as e:
                self._log(f"on_candle error: {e}")


if __name__ == "__main__":
    cfg_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    FeeAwareBreakout(json.loads(cfg_str)).run()
