from base_strategy import BaseStrategy
import json

class MyStrategy(BaseStrategy):
    def __init__(self, config):
        super().__init__(config)
        self.window = config.get("window", 20)

    def on_candle(self, candle):
        self.log(f"Received candle: {candle['close']}")
        # Add your logic here

    def on_order(self, order):
        self.log(f"Order updated: {order['id']}")

    def on_position(self, position):
        pass

if __name__ == "__main__":
    import sys
    config = json.loads(sys.argv[1])
    strategy = MyStrategy(config)
    strategy.run()
