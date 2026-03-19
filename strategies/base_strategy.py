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
        self.pending_orders = {}

    @abstractmethod
    def on_candle(self, candle):
        """Handle incoming candle/k-line data"""
        pass

    @abstractmethod
    def on_order(self, order):
        """Handle order status updates"""
        pass

    def on_position(self, position):
        """Handle position updates"""
        return

    def send_order(self, side, amount, price=0):
        """Send a new order to the backend"""
        if not self.symbol:
            self.log("Cannot send order: symbol is not set in config")
            return
        order_request = {
            "symbol": self.symbol,
            "side": side,
            "amount": amount,
            "price": price
        }
        lev = self.config.get("leverage")
        try:
            if lev is not None:
                lev_int = int(lev)
                if lev_int > 0:
                    order_request["leverage"] = lev_int
        except Exception:
            pass
        self._send_to_backend("order", order_request)

    def buy(self, amount, price=0):
        self._mark_pending("buy", amount, price)
        self.send_order("buy", amount, price)

    def sell(self, amount, price=0):
        self._mark_pending("sell", amount, price)
        self.send_order("sell", amount, price)

    def close_position(self, price=0):
        pos = self.get_position()
        if not pos:
            return
        qty = float(pos.get("qty", 0) or 0)
        if qty <= 0:
            return
        self.sell(qty, price)

    def get_position(self, symbol=None):
        sym = symbol or self.symbol
        if not sym:
            return None
        return self.positions.get(sym)

    def position_qty(self, symbol=None) -> float:
        pos = self.get_position(symbol)
        if not pos:
            return 0.0
        try:
            return float(pos.get("qty", 0) or 0)
        except Exception:
            return 0.0

    def in_position(self, symbol=None) -> bool:
        return self.position_qty(symbol) > 0

    def position_avg_price(self, symbol=None) -> float:
        pos = self.get_position(symbol)
        if not pos:
            return 0.0
        try:
            return float(pos.get("avg_price", 0) or 0)
        except Exception:
            return 0.0

    def log(self, message):
        """Send a log message to the backend/frontend"""
        self._send_to_backend("log", message)

    def _send_to_backend(self, msg_type, data):
        print(json.dumps({"type": msg_type, "data": data}), flush=True)

    def _mark_pending(self, side, amount, price):
        if not self.symbol:
            return
        self.pending_orders[self.symbol] = {
            "side": side,
            "amount": amount,
            "price": price
        }

    def _clear_pending(self, symbol=None):
        sym = symbol or self.symbol
        if not sym:
            return
        if sym in self.pending_orders:
            del self.pending_orders[sym]

    def _record_order(self, order):
        if not isinstance(order, dict):
            return
        oid = order.get("client_order_id") or order.get("id") or f"order_{len(self.orders)+1}"
        self.orders[str(oid)] = order

    def _update_position_from_order(self, order):
        if not isinstance(order, dict):
            return None

        sym = order.get("symbol") or self.symbol
        if not sym:
            return None

        status = str(order.get("status") or "").lower()
        side = str(order.get("side") or "").lower()
        qty = order.get("amount", 0)
        price = order.get("price", 0)

        try:
            qty = float(qty)
        except Exception:
            qty = 0.0
        try:
            price = float(price)
        except Exception:
            price = 0.0

        if status not in ("filled", "closed"):
            if status in ("canceled", "cancelled", "rejected", "failed", "error"):
                self._clear_pending(sym)
            return None

        pos = self.positions.get(sym)
        if not pos:
            pos = {
                "symbol": sym,
                "qty": 0.0,
                "avg_price": 0.0,
                "status": "flat"
            }

        current_qty = float(pos.get("qty", 0) or 0)
        current_avg = float(pos.get("avg_price", 0) or 0)

        if side == "buy":
            new_qty = current_qty + max(qty, 0.0)
            new_avg = current_avg
            if qty > 0 and price > 0:
                if new_qty > 0:
                    new_avg = ((current_avg * current_qty) + (price * qty)) / new_qty
                else:
                    new_avg = price
            pos["qty"] = new_qty
            pos["avg_price"] = new_avg
            pos["status"] = "open" if new_qty > 0 else "flat"
        elif side == "sell":
            new_qty = current_qty - max(qty, 0.0)
            if new_qty <= 0:
                pos["qty"] = 0.0
                pos["status"] = "closed"
            else:
                pos["qty"] = new_qty
                pos["status"] = "open"

        self.positions[sym] = pos
        self._clear_pending(sym)
        return pos

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
                    self._record_order(data)
                    pos = self._update_position_from_order(data)
                    self.on_order(data)
                    if pos is not None:
                        self.on_position(pos)
                elif msg_type == "position":
                    self.on_position(data)
                elif msg_type == "stop":
                    self.log("Stopping strategy...")
                    self.running = False
            except Exception as e:
                self.log(f"Error in strategy loop: {str(e)}\n{traceback.format_exc()}")

if __name__ == "__main__":
    pass
