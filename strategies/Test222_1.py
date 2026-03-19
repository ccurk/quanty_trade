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
 
 
class BasicMarketOrderStrategy(BaseStrategy):
    def __init__(self, config):
        super().__init__(config)
        self.symbol = config.get("symbol", self.symbol)
        self.trade_amount = _f(config.get("trade_amount", 0), 0.0)
        self.side = str(config.get("side", "buy")).lower().strip()
        self.fire_once = bool(config.get("fire_once", True))
        self.order_sent = False
        self.bar_index = 0
        self.hold_bars = int(_f(config.get("hold_bars", 0), 0))
        self.close_sent = False
 
    def on_candle(self, candle):
        self.bar_index += 1
 
        if self.symbol in self.pending_orders:
            return
 
        if (not self.order_sent) or (not self.fire_once):
            if self.trade_amount <= 0:
                self.log("ORDER blocked: trade_amount <= 0")
                self.order_sent = True
                return
            if self.side not in ("buy", "sell"):
                self.log(f"ORDER blocked: invalid side={self.side}")
                self.order_sent = True
                return
            self.log(f"ORDER send side={self.side} amount={self.trade_amount}")
            if self.side == "buy":
                self.buy(self.trade_amount, 0)
            else:
                self.sell(self.trade_amount, 0)
            self.order_sent = True
            return
 
        if self.hold_bars > 0 and (not self.close_sent) and self.in_position():
            if self.bar_index >= self.hold_bars:
                qty = self.position_qty()
                if qty > 0:
                    self.log(f"ORDER auto_close qty={qty}")
                    self.sell(qty, 0)
                    self.close_sent = True
 
    def on_order(self, order):
        if not isinstance(order, dict):
            return
        sym = order.get("symbol") or self.symbol
        status = str(order.get("status") or "").lower()
        side = str(order.get("side") or "").lower()
        qty = _f(order.get("amount", 0), 0.0)
        price = _f(order.get("price", 0), 0.0)
        self.log(f"ORDER_UPDATE sym={sym} side={side} status={status} qty={qty} price={price}")
 
 
if __name__ == "__main__":
    config_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    config = json.loads(config_str)
    strategy = BasicMarketOrderStrategy(config)
    strategy.run()
