from base_strategy import BaseStrategy
import json
import sys
from datetime import datetime, timezone


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


def _b(v, d=False):
    try:
        if isinstance(v, bool):
            return v
        if v is None:
            return bool(d)
        if isinstance(v, (int, float)):
            return v != 0
        s = str(v).strip().lower()
        if s in ("1", "true", "yes", "y", "on"):
            return True
        if s in ("0", "false", "no", "n", "off"):
            return False
        return bool(d)
    except Exception:
        return bool(d)


def _ts_to_date(ts):
    if isinstance(ts, (int, float)):
        try:
            return datetime.fromtimestamp(float(ts) / 1000.0, tz=timezone.utc).date()
        except Exception:
            return datetime.now(tz=timezone.utc).date()
    if isinstance(ts, str):
        try:
            return datetime.fromisoformat(ts.replace("Z", "+00:00")).date()
        except Exception:
            return datetime.now(tz=timezone.utc).date()
    return datetime.now(tz=timezone.utc).date()


def _sma(xs, n):
    if n <= 0 or len(xs) < n:
        return None
    s = 0.0
    for v in xs[-n:]:
        s += float(v)
    return s / float(n)


class ProductionTrendMAStrategy(BaseStrategy):
    def __init__(self, config):
        super().__init__(config)
        self.symbol = config.get("symbol", self.symbol)
        self.leverage = _i(config.get("leverage", 0), 0)
        self.trade_amount = _f(config.get("trade_amount", 0), 0.0)

        self.fast_window = _i(config.get("fast_window", 10), 10)
        self.slow_window = _i(config.get("slow_window", 30), 30)
        if self.fast_window <= 0:
            self.fast_window = 10
        if self.slow_window <= 0:
            self.slow_window = 30
        if self.fast_window >= self.slow_window:
            self.fast_window = max(1, self.slow_window // 2)

        self.warmup_bars = _i(config.get("warmup_bars", 0), 0)
        if self.warmup_bars <= 0:
            self.warmup_bars = max(50, self.slow_window + 5)

        self.take_profit_pct = _f(config.get("take_profit_pct", 0.03), 0.03)
        self.stop_loss_pct = _f(config.get("stop_loss_pct", 0.01), 0.01)
        self.trailing_stop_pct = _f(config.get("trailing_stop_pct", 0.0), 0.0)
        self.max_hold_bars = _i(config.get("max_hold_bars", 0), 0)
        self.cooldown_bars = _i(config.get("cooldown_bars", 0), 0)

        self.max_trades_per_day = _i(config.get("max_trades_per_day", 3), 3)
        if self.max_trades_per_day <= 0:
            self.max_trades_per_day = 3

        self.allow_reentry = _b(config.get("repeat_on_flat", False), False)
        self.confirm_bars = _i(config.get("confirm_bars", 1), 1)
        if self.confirm_bars < 1:
            self.confirm_bars = 1
        if self.confirm_bars > 5:
            self.confirm_bars = 5

        self.debug = _b(config.get("debug", False), False)
        self.debug_interval_bars = _i(config.get("debug_interval_bars", 10), 10)
        if self.debug_interval_bars <= 0:
            self.debug_interval_bars = 10

        self.closes = []
        self.highs = []
        self.lows = []
        self._bar = 0

        self.entry_price = 0.0
        self.entry_bar = 0
        self.high_water = 0.0
        self.cooldown_until = 0

        self.trades_today = 0
        self.current_day = None

        self._last_fast = None
        self._last_slow = None
        self._confirm_left = 0

        self._log_config()

    def _log_config(self):
        self.log(
            "CONFIG "
            f"symbol={self.symbol} leverage={self.leverage} qty={self.trade_amount} "
            f"fast={self.fast_window} slow={self.slow_window} warmup={self.warmup_bars} "
            f"tp={self.take_profit_pct} sl={self.stop_loss_pct} ts={self.trailing_stop_pct} "
            f"max_hold={self.max_hold_bars} cooldown={self.cooldown_bars} "
            f"max_trades_day={self.max_trades_per_day} reentry={self.allow_reentry} "
            f"confirm_bars={self.confirm_bars} debug={self.debug} debug_int={self.debug_interval_bars}"
        )

    def _on_new_day(self, day):
        if self.current_day is None:
            self.current_day = day
            self.trades_today = 0
            return
        if day != self.current_day:
            self.current_day = day
            self.trades_today = 0
            self.log(f"DAY_RESET day={self.current_day}")

    def _ready(self):
        return len(self.closes) >= max(self.warmup_bars, self.slow_window + 2)

    def _in_cooldown(self):
        return self._bar < self.cooldown_until

    def _can_open(self):
        if self.trade_amount <= 0:
            return False, "trade_amount<=0"
        if self.trades_today >= self.max_trades_per_day:
            return False, "max_trades_per_day"
        if self._in_cooldown():
            return False, f"cooldown_left={self.cooldown_until - self._bar}"
        if self.symbol in self.pending_orders:
            return False, "pending_orders"
        if self.in_position():
            return False, "already_in_position"
        return True, ""

    def _risk_check(self, close_price):
        if not self.in_position():
            return
        if self.symbol in self.pending_orders:
            return
        if self.entry_price <= 0:
            return
        if close_price <= 0:
            return

        if close_price > self.high_water:
            self.high_water = close_price

        stop_from_sl = self.entry_price * (1.0 - max(0.0, self.stop_loss_pct))
        stop_from_ts = 0.0
        if self.trailing_stop_pct > 0 and self.high_water > 0:
            stop_from_ts = self.high_water * (1.0 - self.trailing_stop_pct)
        stop_level = max(stop_from_sl, stop_from_ts)
        tp_level = self.entry_price * (1.0 + max(0.0, self.take_profit_pct))

        if self.max_hold_bars > 0 and (self._bar - self.entry_bar) >= self.max_hold_bars:
            self.log(f"EXIT time_stop bar={self._bar} hold={self._bar - self.entry_bar}")
            self.close_position(0)
            return

        if stop_level > 0 and close_price <= stop_level:
            self.log(f"EXIT stop close={close_price} stop={stop_level} entry={self.entry_price} high={self.high_water}")
            self.close_position(0)
            return

        if tp_level > 0 and close_price >= tp_level:
            self.log(f"EXIT take_profit close={close_price} tp={tp_level} entry={self.entry_price}")
            self.close_position(0)
            return

    def _signal_update(self):
        fast = _sma(self.closes, self.fast_window)
        slow = _sma(self.closes, self.slow_window)
        if fast is None or slow is None:
            return None, None, None

        crossed_up = False
        if self._last_fast is not None and self._last_slow is not None:
            crossed_up = (self._last_fast <= self._last_slow) and (fast > slow)

        self._last_fast = fast
        self._last_slow = slow
        return fast, slow, crossed_up

    def on_candle(self, candle):
        self._bar += 1
        close_price = _f(candle.get("close") if isinstance(candle, dict) else None, 0.0)
        high_price = _f(candle.get("high") if isinstance(candle, dict) else None, close_price)
        low_price = _f(candle.get("low") if isinstance(candle, dict) else None, close_price)
        ts = candle.get("timestamp") if isinstance(candle, dict) else None

        self._on_new_day(_ts_to_date(ts))

        if close_price <= 0:
            if self.debug and (self._bar % self.debug_interval_bars == 0):
                self.log(f"DEBUG bar={self._bar} skip reason=invalid_close close={close_price}")
            return

        self.closes.append(close_price)
        self.highs.append(high_price)
        self.lows.append(low_price)
        max_keep = max(self.warmup_bars, self.slow_window) + 200
        if len(self.closes) > max_keep:
            self.closes = self.closes[-max_keep:]
            self.highs = self.highs[-max_keep:]
            self.lows = self.lows[-max_keep:]

        if not self._ready():
            if self.debug and (self._bar % self.debug_interval_bars == 0):
                self.log(f"DEBUG bar={self._bar} warmup len={len(self.closes)} need={max(self.warmup_bars, self.slow_window + 2)}")
            return

        self._risk_check(close_price)

        fast, slow, crossed_up = self._signal_update()
        if fast is None or slow is None:
            return

        if self.debug and (self._bar % self.debug_interval_bars == 0):
            self.log(
                f"DEBUG bar={self._bar} close={close_price} fast={fast:.6f} slow={slow:.6f} "
                f"in_pos={1 if self.in_position() else 0} pending={1 if (self.symbol in self.pending_orders) else 0} "
                f"trades_today={self.trades_today} cooldown_left={max(0, self.cooldown_until - self._bar)}"
            )

        if self.in_position():
            return

        if crossed_up:
            self._confirm_left = self.confirm_bars

        if self._confirm_left > 0:
            self._confirm_left -= 1
            if fast <= slow:
                self._confirm_left = 0
                return
            if self._confirm_left != 0:
                return

            ok, reason = self._can_open()
            if not ok:
                if self.debug:
                    self.log(f"ENTRY blocked bar={self._bar} reason={reason}")
                return

            self.log(f"ENTRY long bar={self._bar} close={close_price} qty={self.trade_amount}")
            self.buy(self.trade_amount, 0)
            self.trades_today += 1
            return

    def on_order(self, order):
        if not isinstance(order, dict):
            return
        status = str(order.get("status") or "").lower()
        side = str(order.get("side") or "").lower()
        qty = _f(order.get("amount", 0), 0.0)
        price = _f(order.get("price", 0), 0.0)
        self.log(f"ORDER sym={order.get('symbol') or self.symbol} side={side} status={status} qty={qty} price={price}")

        if status != "filled":
            return

        if side == "buy" and qty > 0:
            if price > 0:
                self.entry_price = price
            else:
                self.entry_price = _f(self.position_avg_price(), 0.0)
            self.entry_bar = self._bar
            self.high_water = max(self.high_water, self.entry_price)
            if self.debug:
                self.log(f"STATE entry_price={self.entry_price} entry_bar={self.entry_bar}")
            return

        if side == "sell":
            self.entry_price = 0.0
            self.entry_bar = 0
            self.high_water = 0.0
            if self.cooldown_bars > 0:
                self.cooldown_until = self._bar + self.cooldown_bars
            if self.allow_reentry:
                self._confirm_left = 0
            if self.debug:
                self.log(f"STATE flat bar={self._bar} cooldown_until={self.cooldown_until}")

    def on_position(self, position):
        if not isinstance(position, dict):
            return
        status = str(position.get("status") or "").lower()
        qty = _f(position.get("qty", 0), 0.0)
        self.log(f"POSITION sym={position.get('symbol') or self.symbol} status={status} qty={qty}")


if __name__ == "__main__":
    config_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    config = json.loads(config_str)
    strategy = ProductionTrendMAStrategy(config)
    strategy.run()
