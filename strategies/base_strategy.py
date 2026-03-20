import json
import sys
import time
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
        self.trace = bool(config.get("trace", False)) or bool(config.get("debug", False))
        try:
            self.trace_candle_interval = max(1, int(config.get("trace_candle_interval", config.get("debug_interval_bars", 5))))
        except Exception:
            self.trace_candle_interval = 5
        self._trace_candle_count = 0

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
        if self.trace:
            try:
                self.log(f"TRACE send_order side={side} amount={float(amount)} price={float(price)}")
            except Exception:
                self.log(f"TRACE send_order side={side} amount={amount} price={price}")
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

    def send_order_for(self, symbol, side, amount, price=0):
        if not symbol:
            self.log("Cannot send order: symbol is empty")
            return
        if self.trace:
            try:
                self.log(f"TRACE send_order symbol={symbol} side={side} amount={float(amount)} price={float(price)}")
            except Exception:
                self.log(f"TRACE send_order symbol={symbol} side={side} amount={amount} price={price}")
        order_request = {
            "symbol": symbol,
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

    def buy_for(self, symbol, amount, price=0):
        self._mark_pending_for(symbol, "buy", amount, price)
        self.send_order_for(symbol, "buy", amount, price)

    def sell_for(self, symbol, amount, price=0):
        self._mark_pending_for(symbol, "sell", amount, price)
        self.send_order_for(symbol, "sell", amount, price)

    def close_position(self, price=0):
        pos = self.get_position()
        if not pos:
            return
        qty = float(pos.get("qty", 0) or 0)
        if qty <= 0:
            return
        self.sell(qty, price)

    def close_position_for(self, symbol, price=0):
        pos = self.get_position(symbol)
        if not pos:
            return
        qty = float(pos.get("qty", 0) or 0)
        if qty <= 0:
            return
        self.sell_for(symbol, qty, price)

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

    def _mark_pending_for(self, symbol, side, amount, price):
        sym = symbol or self.symbol
        if not sym:
            return
        self.pending_orders[sym] = {
            "side": side,
            "amount": amount,
            "price": price
        }
        if self.trace:
            self.log(f"TRACE pending set symbol={sym} side={side}")

    def _mark_pending(self, side, amount, price):
        self._mark_pending_for(self.symbol, side, amount, price)

    def _clear_pending(self, symbol=None):
        sym = symbol or self.symbol
        if not sym:
            return
        if sym in self.pending_orders:
            del self.pending_orders[sym]
            if self.trace:
                self.log(f"TRACE pending cleared symbol={sym}")

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
        if self.trace:
            try:
                self.log(f"TRACE position symbol={sym} status={pos.get('status')} qty={float(pos.get('qty', 0) or 0)} avg={float(pos.get('avg_price', 0) or 0)}")
            except Exception:
                self.log(f"TRACE position symbol={sym} status={pos.get('status')} qty={pos.get('qty')} avg={pos.get('avg_price')}")
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
                    if self.trace:
                        self._trace_candle_count += 1
                        if self._trace_candle_count % self.trace_candle_interval == 0:
                            ts = None
                            close = None
                            if isinstance(data, dict):
                                ts = data.get("timestamp")
                                close = data.get("close")
                            self.log(f"TRACE recv candle n={self._trace_candle_count} ts={ts} close={close} pending={1 if (self.symbol in self.pending_orders) else 0}")
                    t0 = time.time()
                    self.on_candle(data)
                    if self.trace:
                        dt_ms = (time.time() - t0) * 1000.0
                        self.log(f"TRACE on_candle done ms={dt_ms:.2f}")
                elif msg_type == "order":
                    if self.trace:
                        self.log(f"TRACE recv order keys={list(data.keys()) if isinstance(data, dict) else type(data)}")
                    self._record_order(data)
                    pos = self._update_position_from_order(data)
                    t0 = time.time()
                    self.on_order(data)
                    if self.trace:
                        dt_ms = (time.time() - t0) * 1000.0
                        self.log(f"TRACE on_order done ms={dt_ms:.2f}")
                    if pos is not None:
                        t0 = time.time()
                        self.on_position(pos)
                        if self.trace:
                            dt_ms = (time.time() - t0) * 1000.0
                            self.log(f"TRACE on_position(done from order) ms={dt_ms:.2f}")
                elif msg_type == "position":
                    if self.trace:
                        self.log(f"TRACE recv position keys={list(data.keys()) if isinstance(data, dict) else type(data)}")
                    t0 = time.time()
                    self.on_position(data)
                    if self.trace:
                        dt_ms = (time.time() - t0) * 1000.0
                        self.log(f"TRACE on_position done ms={dt_ms:.2f}")
                elif msg_type == "stop":
                    self.log("Stopping strategy...")
                    self.running = False
            except Exception as e:
                self.log(f"Error in strategy loop: {str(e)}\n{traceback.format_exc()}")

if __name__ == "__main__":
    pass
