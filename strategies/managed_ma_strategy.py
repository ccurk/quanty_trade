from base_strategy import BaseStrategy
import json
import sys


def _to_float(v, default=0.0) -> float:
    try:
        if v is None:
            return float(default)
        return float(v)
    except Exception:
        return float(default)


def _to_int(v, default=0) -> int:
    try:
        if v is None:
            return int(default)
        return int(v)
    except Exception:
        return int(default)


class ManagedMACrossoverStrategy(BaseStrategy):
    def __init__(self, config):
        super().__init__(config)
        self.symbol = config.get("symbol", self.symbol)

        self.fast_window = max(1, _to_int(config.get("fast_window", 10), 10))
        self.slow_window = max(self.fast_window, _to_int(config.get("slow_window", 30), 30))

        self.trade_amount = _to_float(config.get("trade_amount", 0.01), 0.01)

        self.take_profit_pct = max(0.0, _to_float(config.get("take_profit_pct", 0.03), 0.03))
        self.stop_loss_pct = max(0.0, _to_float(config.get("stop_loss_pct", 0.01), 0.01))
        self.trailing_stop_pct = max(0.0, _to_float(config.get("trailing_stop_pct", 0.0), 0.0))

        self.max_hold_bars = max(0, _to_int(config.get("max_hold_bars", 0), 0))
        self.cooldown_bars = max(0, _to_int(config.get("cooldown_bars", 0), 0))

        self.closes = []

        self.highest_price = None
        self.entry_bar_index = None
        self.bar_index = 0
        self.cooldown_until_bar = 0

    def on_candle(self, candle):
        close = _to_float(candle.get("close", 0), 0.0)
        if close <= 0:
            return

        self.bar_index += 1

        self.closes.append(close)
        if len(self.closes) > self.slow_window:
            self.closes.pop(0)

        qty = self.position_qty()
        if qty > 0:
            if self.highest_price is None or close > self.highest_price:
                self.highest_price = close

        if self.get_position() and self.symbol in self.pending_orders:
            return

        if qty > 0:
            if self._should_exit(close):
                self.close_position()
                return

        if len(self.closes) < self.slow_window:
            return

        if self.bar_index < self.cooldown_until_bar:
            return

        fast_ma = sum(self.closes[-self.fast_window:]) / self.fast_window
        slow_ma = sum(self.closes) / self.slow_window

        if qty <= 0 and fast_ma > slow_ma:
            self.log(f"BUY signal fast={fast_ma:.4f} slow={slow_ma:.4f} close={close:.4f}")
            self.buy(self.trade_amount, 0)
            return

        if qty > 0 and fast_ma < slow_ma:
            self.log(f"SELL signal fast={fast_ma:.4f} slow={slow_ma:.4f} close={close:.4f}")
            self.close_position()

    def on_order(self, order):
        return

    def on_position(self, position):
        if not isinstance(position, dict):
            return
        sym = position.get("symbol")
        if sym and self.symbol and sym != self.symbol:
            return
        status = str(position.get("status") or "").lower()
        qty = _to_float(position.get("qty", 0), 0.0)
        avg_price = _to_float(position.get("avg_price", 0), 0.0)
        if status in ("open", "flat") and qty > 0:
            if self.entry_bar_index is None:
                self.entry_bar_index = self.bar_index
            if self.highest_price is None and avg_price > 0:
                self.highest_price = avg_price
        if status == "closed" or qty <= 0:
            self.highest_price = None
            self.entry_bar_index = None
            if self.cooldown_bars > 0:
                self.cooldown_until_bar = self.bar_index + self.cooldown_bars

    def _should_exit(self, close: float) -> bool:
        entry_price = self.position_avg_price()
        if entry_price <= 0:
            return False

        if self.take_profit_pct > 0 and close >= entry_price * (1.0 + self.take_profit_pct):
            return True

        if self.stop_loss_pct > 0 and close <= entry_price * (1.0 - self.stop_loss_pct):
            return True

        if self.trailing_stop_pct > 0 and self.highest_price is not None:
            stop_price = self.highest_price * (1.0 - self.trailing_stop_pct)
            if close <= stop_price:
                return True

        if self.max_hold_bars > 0 and self.entry_bar_index is not None:
            if (self.bar_index - self.entry_bar_index) >= self.max_hold_bars:
                return True

        return False


if __name__ == "__main__":
    config_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    config = json.loads(config_str)
    strategy = ManagedMACrossoverStrategy(config)
    strategy.run()
