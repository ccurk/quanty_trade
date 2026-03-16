from base_strategy import BaseStrategy
import json
import sys

class SimpleTrendStrategy(BaseStrategy):
    def __init__(self, config):
        super().__init__(config)
        self.candles = []
        self.window = config.get("window", 20)
        self.in_position = False

    def on_candle(self, candle):
        self.candles.append(candle)
        if len(self.candles) > self.window:
            self.candles.pop(0)

        if len(self.candles) == self.window:
            avg_price = sum([c['close'] for c in self.candles]) / self.window
            current_price = candle['close']

            if current_price > avg_price and not self.in_position:
                self.log(f"Trend UP: current({current_price:.2f}) > avg({avg_price:.2f}). Buying...")
                self.send_order("buy", 0.01, current_price)
                self.in_position = True
            elif current_price < avg_price and self.in_position:
                self.log(f"Trend DOWN: current({current_price:.2f}) < avg({avg_price:.2f}). Selling...")
                self.send_order("sell", 0.01, current_price)
                self.in_position = False

    def on_order(self, order):
        self.log(f"Order confirmed: {order['id']} - {order['status']}")

    def on_position(self, position):
        pass

if __name__ == "__main__":
    config_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    try:
        config = json.loads(config_str)
        strategy = SimpleTrendStrategy(config)
        strategy.run()
    except Exception as e:
        print(json.dumps({"type": "log", "data": f"Fatal error: {str(e)}"}), flush=True)
