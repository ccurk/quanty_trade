from base_strategy import BaseStrategy
import json
import sys
import time


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
        return int(v)
    except Exception:
        return int(d)


class LiveMARiskStrategy(BaseStrategy):
    def __init__(self, config):
        super().__init__(config)
        self.symbol = config.get("symbol", self.symbol)
        self.leverage = _i(config.get("leverage", 0), 0)

        self.fast_window = max(1, _i(config.get("fast_window", 10), 10))
        self.slow_window = max(self.fast_window, _i(config.get("slow_window", 30), 30))

        self.trade_amount = _f(config.get("trade_amount", 0.01), 0.01)

        self.take_profit_pct = max(0.0, _f(config.get("take_profit_pct", 0.02), 0.02))
        self.stop_loss_pct = max(0.0, _f(config.get("stop_loss_pct", 0.01), 0.01))
        self.trailing_stop_pct = max(0.0, _f(config.get("trailing_stop_pct", 0.0), 0.0))

        self.cooldown_bars = max(0, _i(config.get("cooldown_bars", 5), 5))
        self.max_hold_bars = max(0, _i(config.get("max_hold_bars", 0), 0))

        self.max_trades_per_day = max(0, _i(config.get("max_trades_per_day", 10), 10))
        self.daily_loss_limit_pct = max(0.0, _f(config.get("daily_loss_limit_pct", 0.02), 0.02))

        self.closes = []
        self.bar_index = 0
        self.cooldown_until_bar = 0

        self.entry_bar_index = None
        self.highest_price = None

        self.day_key = time.strftime("%Y-%m-%d", time.localtime())
        self.trades_today = 0
        self.day_start_equity = None
        self.realized_pnl_today = 0.0

    def on_candle(self, candle):
        close = _f(candle.get("close", 0), 0.0)
        if close <= 0:
            return

        self._roll_day_if_needed()

        self.bar_index += 1

        self.closes.append(close)
        if len(self.closes) > self.slow_window:
            self.closes.pop(0)

        if self.symbol in self.pending_orders:
            return

        qty = self.position_qty()
        avg = self.position_avg_price()

        if qty > 0:
            if self.highest_price is None or close > self.highest_price:
                self.highest_price = close

            if self._blocked_by_daily_loss(close, qty, avg):
                self.close_position()
                return

            if self._should_exit(close, avg):
                self.close_position()
                return

            if self.max_hold_bars > 0 and self.entry_bar_index is not None:
                if (self.bar_index - self.entry_bar_index) >= self.max_hold_bars:
                    self.close_position()
                    return

        if len(self.closes) < self.slow_window:
            return

        if self.bar_index < self.cooldown_until_bar:
            return

        fast_ma = sum(self.closes[-self.fast_window:]) / self.fast_window
        slow_ma = sum(self.closes) / self.slow_window

        if qty <= 0 and fast_ma > slow_ma:
            if self._can_open_new_trade():
                self.buy(self.trade_amount, 0)
                self.trades_today += 1
                self.entry_bar_index = self.bar_index
                self.highest_price = close
            return

        if qty > 0 and fast_ma < slow_ma:
            self.close_position()
            return

    def on_order(self, order):
        return

    def on_position(self, position):
        if not isinstance(position, dict):
            return
        sym = position.get("symbol")
        if sym and self.symbol and sym != self.symbol:
            return

        status = str(position.get("status") or "").lower()
        qty = _f(position.get("qty", 0), 0.0)
        avg = _f(position.get("avg_price", 0), 0.0)

        if status == "open" and qty > 0:
            if self.entry_bar_index is None:
                self.entry_bar_index = self.bar_index
            if self.highest_price is None and avg > 0:
                self.highest_price = avg
            return

        if status == "closed" or qty <= 0:
            if self.cooldown_bars > 0:
                self.cooldown_until_bar = self.bar_index + self.cooldown_bars
            self.entry_bar_index = None
            self.highest_price = None

    def _should_exit(self, close, entry):
        if entry <= 0:
            return False
        if self.take_profit_pct > 0 and close >= entry * (1.0 + self.take_profit_pct):
            return True
        if self.stop_loss_pct > 0 and close <= entry * (1.0 - self.stop_loss_pct):
            return True
        if self.trailing_stop_pct > 0 and self.highest_price is not None:
            stop_price = self.highest_price * (1.0 - self.trailing_stop_pct)
            if close <= stop_price:
                return True
        return False

    def _roll_day_if_needed(self):
        k = time.strftime("%Y-%m-%d", time.localtime())
        if k == self.day_key:
            return
        self.day_key = k
        self.trades_today = 0
        self.day_start_equity = None
        self.realized_pnl_today = 0.0

    def _can_open_new_trade(self):
        if self.trade_amount <= 0:
            return False
        if self.max_trades_per_day > 0 and self.trades_today >= self.max_trades_per_day:
            return False
        return True

    def _blocked_by_daily_loss(self, close, qty, avg):
        if self.daily_loss_limit_pct <= 0:
            return False
        if avg <= 0 or qty <= 0:
            return False
        pnl = (close - avg) * qty
        if self.day_start_equity is None:
            self.day_start_equity = max(avg * qty, 1.0)
        equity = self.day_start_equity + pnl + self.realized_pnl_today
        limit = self.day_start_equity * (1.0 - self.daily_loss_limit_pct)
        return equity <= limit


if __name__ == "__main__":
    config_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    config = json.loads(config_str)
    strategy = LiveMARiskStrategy(config)
    strategy.run()
