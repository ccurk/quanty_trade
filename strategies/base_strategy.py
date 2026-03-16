import json
import sys
import traceback
from abc import ABC, abstractmethod

class BaseStrategy(ABC):
    def __init__(self, config):
        self.config = config
        self.running = True
        self.symbol = config.get("symbol")
        self.positions = {}
        self.orders = {}

    @abstractmethod
    def on_candle(self, candle):
        """Handle incoming candle/k-line data"""
        pass

    @abstractmethod
    def on_order(self, order):
        """Handle order status updates"""
        pass

    @abstractmethod
    def on_position(self, position):
        """Handle position updates"""
        pass

    def send_order(self, side, amount, price=0):
        """Send a new order to the backend"""
        order_request = {
            "symbol": self.symbol,
            "side": side,
            "amount": amount,
            "price": price
        }
        self._send_to_backend("order", order_request)

    def log(self, message):
        """Send a log message to the backend/frontend"""
        self._send_to_backend("log", message)

    def _send_to_backend(self, msg_type, data):
        print(json.dumps({"type": msg_type, "data": data}), flush=True)

    def run(self):
        self.log(f"Strategy started for {self.symbol}")
        while self.running:
            line = sys.stdin.readline()
            if not line:
                break
            try:
                msg = json.loads(line)
                msg_type = msg.get("type")
                data = msg.get("data")

                if msg_type == "candle":
                    self.on_candle(data)
                elif msg_type == "order":
                    self.on_order(data)
                elif msg_type == "position":
                    self.on_position(data)
                elif msg_type == "stop":
                    self.log("Stopping strategy...")
                    self.running = False
            except Exception as e:
                self.log(f"Error in strategy loop: {str(e)}\n{traceback.format_exc()}")

if __name__ == "__main__":
    pass
