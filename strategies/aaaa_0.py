from base_strategy import BaseStrategy
import json
import sys


class MyStrategy(BaseStrategy):
    def __init__(self, config):
        super().__init__(config)
        self.symbol = config.get("symbol", "BTC/USDT")
        self.fast_window = int(config.get("fast_window", 10))
        self.slow_window = int(config.get("slow_window", 30))
        self.trade_amount = float(config.get("trade_amount", 0.01))
        self.take_profit_pct = float(config.get("take_profit_pct", 0.03))
        self.stop_loss_pct = float(config.get("stop_loss_pct", 0.01))

        self.closes = []
        self.in_position = False
        self.entry_price = None

    def on_candle(self, candle):
        close = float(candle.get("close", 0))
        if close <= 0:
            return

        self.closes.append(close)
        if len(self.closes) > self.slow_window:
            self.closes.pop(0)

        if self.in_position and self.entry_price:
            if close >= self.entry_price * (1.0 + self.take_profit_pct):
                self.log(f"TAKE PROFIT close={close}")
                self.send_order("sell", self.trade_amount, 0)
                self.in_position = False
                self.entry_price = None
                return
            if close <= self.entry_price * (1.0 - self.stop_loss_pct):
                self.log(f"STOP LOSS close={close}")
                self.send_order("sell", self.trade_amount, 0)
                self.in_position = False
                self.entry_price = None
                return

        if len(self.closes) < self.slow_window:
            return

        fast_ma = sum(self.closes[-self.fast_window:]) / self.fast_window
        slow_ma = sum(self.closes) / self.slow_window

        if fast_ma > slow_ma and not self.in_position:
            self.log(f"BUY signal fast={fast_ma:.4f} slow={slow_ma:.4f} close={close:.4f}")
            self.send_order("buy", self.trade_amount, 0)
            self.in_position = True
            self.entry_price = close
            return

        if fast_ma < slow_ma and self.in_position:
            self.log(f"SELL signal fast={fast_ma:.4f} slow={slow_ma:.4f} close={close:.4f}")
            self.send_order("sell", self.trade_amount, 0)
            self.in_position = False
            self.entry_price = None

    def on_order(self, order):
        return

    def on_position(self, position):
        return


if __name__ == "__main__":
    config_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    config = json.loads(config_str)
    strategy = MyStrategy(config)
    strategy.run()
