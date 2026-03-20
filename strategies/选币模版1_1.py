from base_strategy import BaseStrategy
import json
import sys

def _f(v, d=0.0):
    try:
        if v is None:
            return float(d)
        return float(v)
    except Exception:
        return float(d)

def _i(v, d=0):
    try:
        if v is None:
            return int(d)
        return int(float(v))
    except Exception:
        return int(d)

def _sma(xs, n):
    if n <= 0 or len(xs) < n:
        return None
    s = 0.0
    for v in xs[-n:]:
        s += float(v)
    return s / float(n)

class SelectorTrendMA(BaseStrategy):
    def __init__(self, config):
        super().__init__(config)
        self.symbol = (config.get("symbol") or self.symbol or "").strip()
        self.fast_window = max(1, _i(config.get("fast_window", 10), 10))
        self.slow_window = max(2, _i(config.get("slow_window", 30), 30))
        if self.fast_window >= self.slow_window:
            self.fast_window = max(1, self.slow_window // 2)
        self.trade_amount = _f(config.get("trade_amount", 0.01), 0.01)
        self.take_profit_pct = max(0.0, _f(config.get("take_profit_pct", 0.03), 0.03))
        self.stop_loss_pct = max(0.0, _f(config.get("stop_loss_pct", 0.01), 0.01))
        self.trailing_stop_pct = max(0.0, _f(config.get("trailing_stop_pct", 0.0), 0.0))
        self.confirm_bars = max(1, _i(config.get("confirm_bars", 1), 1))
        self.cooldown_bars = max(0, _i(config.get("cooldown_bars", 0), 0))
        self.entry_mode = str(config.get("entry_mode", "trend") or "trend").strip().lower()
        if self.entry_mode not in ("trend", "crossover"):
            self.entry_mode = "trend"
        self.closes = []
        self.high_water = 0.0
        self.trend_up_count = 0
        self.confirm_left = 0
        self.bar = 0
        self.cooldown_until = 0
        self.log(f"INIT symbol={self.symbol} fast={self.fast_window} slow={self.slow_window} qty={self.trade_amount} tp={self.take_profit_pct} sl={self.stop_loss_pct} ts={self.trailing_stop_pct} confirm={self.confirm_bars} cooldown={self.cooldown_bars}")

    def on_candle(self, candle):
        if self.symbol:
            sym = str(candle.get("symbol") or "").strip()
            if sym and sym != self.symbol:
                return
        close = _f(candle.get("close", 0), 0.0)
        if close <= 0:
            return
        self.bar += 1
        self.closes.append(close)
        if len(self.closes) > self.slow_window + 5:
            self.closes = self.closes[-(self.slow_window + 5):]

        in_pos = self.in_position()
        if in_pos:
            if close > self.high_water:
                self.high_water = close
            ep = self.position_avg_price()
            if ep > 0:
                tp = ep * (1.0 + self.take_profit_pct)
                sl = ep * (1.0 - self.stop_loss_pct)
                ts = 0.0
                if self.trailing_stop_pct > 0 and self.high_water > 0:
                    ts = self.high_water * (1.0 - self.trailing_stop_pct)
                stop_level = max(sl, ts) if ts > 0 else sl
                if close >= tp:
                    self.log(f"EXIT take_profit close={close} entry={ep}")
                    self.close_position(0)
                    self.high_water = 0.0
                    self.cooldown_until = self.bar + self.cooldown_bars if self.cooldown_bars > 0 else 0
                    return
                if close <= stop_level:
                    self.log(f"EXIT stop close={close} entry={ep} stop={stop_level}")
                    self.close_position(0)
                    self.high_water = 0.0
                    self.cooldown_until = self.bar + self.cooldown_bars if self.cooldown_bars > 0 else 0
                    return

        if len(self.closes) < self.slow_window:
            return
        fast = _sma(self.closes, self.fast_window)
        slow = _sma(self.closes, self.slow_window)
        if fast is None or slow is None:
            return

        if self.bar < self.cooldown_until:
            return

        if in_pos:
            if fast < slow:
                self.log(f"EXIT trend_reverse fast={fast:.6f} slow={slow:.6f} close={close:.6f}")
                self.close_position(0)
                self.high_water = 0.0
                self.cooldown_until = self.bar + self.cooldown_bars if self.cooldown_bars > 0 else 0
            return

        if self.trade_amount <= 0:
            return

        if self.entry_mode == "trend":
            if fast > slow:
                self.trend_up_count += 1
            else:
                self.trend_up_count = 0
            if self.trend_up_count < self.confirm_bars:
                return
            self.log(f"ENTRY long(trend) close={close:.6f} fast={fast:.6f} slow={slow:.6f} qty={self.trade_amount}")
            self.buy(self.trade_amount, 0)
            self.high_water = close
            self.trend_up_count = 0
            return

        crossed_up = len(self.closes) >= 2 and self.closes[-2] < self.closes[-1]
        if crossed_up:
            self.confirm_left = self.confirm_bars
        if self.confirm_left > 0:
            self.confirm_left -= 1
            if fast <= slow:
                self.confirm_left = 0
                return
            if self.confirm_left != 0:
                return
            self.log(f"ENTRY long(crossover) close={close:.6f} fast={fast:.6f} slow={slow:.6f} qty={self.trade_amount}")
            self.buy(self.trade_amount, 0)
            self.high_water = close
            return

    def on_order(self, order):
        if not isinstance(order, dict):
            return
        status = str(order.get("status") or "").lower()
        side = str(order.get("side") or "").lower()
        qty = _f(order.get("amount", 0), 0.0)
        price = _f(order.get("price", 0), 0.0)
        self.log(f"ORDER side={side} status={status} qty={qty} price={price}")
        if status != "filled":
            return
        if side == "buy" and qty > 0:
            ep = price if price > 0 else _f(self.position_avg_price(), 0.0)
            self.high_water = max(self.high_water, ep)
            return
        if side == "sell":
            self.high_water = 0.0
            self.cooldown_until = self.bar + self.cooldown_bars if self.cooldown_bars > 0 else 0

    def on_position(self, position):
        if not isinstance(position, dict):
            return
        status = str(position.get("status") or "").lower()
        qty = _f(position.get("qty", 0), 0.0)
        self.log(f"POSITION status={status} qty={qty}")

if __name__ == "__main__":
    config_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    cfg = json.loads(config_str)
    SelectorTrendMA(cfg).run()