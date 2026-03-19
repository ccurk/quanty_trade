from base_strategy import BaseStrategy
import json
import sys


class MovingAverageCrossoverStrategy(BaseStrategy):
    def __init__(self, config):
        super().__init__(config)
        self.fast_window = int(config.get("fast_window", 10))
        self.slow_window = int(config.get("slow_window", 30))
        self.trade_amount = float(config.get("trade_amount", 0.01))
        self.take_profit_pct = float(config.get("take_profit_pct", 0.03))
        self.stop_loss_pct = float(config.get("stop_loss_pct", 0.01))
        self.trailing_stop_pct = float(config.get("trailing_stop_pct", 0.0))

        self.closes = []
        self.in_position = False
        self.last_signal = None
        self.entry_price = None
        self.highest_price = None

    def on_candle(self, candle):
        close = float(candle.get("close", 0))
        if close <= 0:
            return

        self.closes.append(close)
        if len(self.closes) > self.slow_window:
            self.closes.pop(0)

        if self.in_position:
            self._check_risk(close)

        if len(self.closes) < self.slow_window:
            return

        fast_ma = sum(self.closes[-self.fast_window:]) / self.fast_window
        slow_ma = sum(self.closes) / self.slow_window

        if fast_ma > slow_ma and not self.in_position:
            if self.last_signal != "buy":
                self.log(f"MA crossover BUY: fast={fast_ma:.4f} slow={slow_ma:.4f} close={close:.4f}")
            self.send_order("buy", self.trade_amount, 0)
            self.in_position = True
            self.entry_price = close
            self.highest_price = close
            self.last_signal = "buy"
            return

        if fast_ma < slow_ma and self.in_position:
            if self.last_signal != "sell":
                self.log(f"MA crossover SELL: fast={fast_ma:.4f} slow={slow_ma:.4f} close={close:.4f}")
            self.send_order("sell", self.trade_amount, 0)
            self.in_position = False
            self.entry_price = None
            self.highest_price = None
            self.last_signal = "sell"

    def _check_risk(self, close):
        if self.entry_price is None:
            self.entry_price = close
        if self.highest_price is None or close > self.highest_price:
            self.highest_price = close

        if self.take_profit_pct > 0 and close >= self.entry_price * (1.0 + self.take_profit_pct):
            self.log(f"TAKE PROFIT: entry={self.entry_price:.4f} close={close:.4f}")
            self.send_order("sell", self.trade_amount, 0)
            self.in_position = False
            self.entry_price = None
            self.highest_price = None
            self.last_signal = "sell"
            return

        if self.stop_loss_pct > 0 and close <= self.entry_price * (1.0 - self.stop_loss_pct):
            self.log(f"STOP LOSS: entry={self.entry_price:.4f} close={close:.4f}")
            self.send_order("sell", self.trade_amount, 0)
            self.in_position = False
            self.entry_price = None
            self.highest_price = None
            self.last_signal = "sell"
            return

        if self.trailing_stop_pct > 0 and self.highest_price is not None:
            stop_price = self.highest_price * (1.0 - self.trailing_stop_pct)
            if close <= stop_price:
                self.log(f"TRAILING STOP: high={self.highest_price:.4f} stop={stop_price:.4f} close={close:.4f}")
                self.send_order("sell", self.trade_amount, 0)
                self.in_position = False
                self.entry_price = None
                self.highest_price = None
                self.last_signal = "sell"

    def on_order(self, order):
        return

    def on_position(self, position):
        return


if __name__ == "__main__":
    config_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    try:
        config = json.loads(config_str)
        strategy = MovingAverageCrossoverStrategy(config)
        strategy.run()
    except Exception as e:
        print(json.dumps({"type": "log", "data": f"Fatal error: {str(e)}"}), flush=True)
