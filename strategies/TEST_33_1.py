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
        self.repeat_on_flat = bool(config.get("repeat_on_flat", False))
        self.cooldown_bars = int(_f(config.get("cooldown_bars", 0), 0))
        self.order_sent = False
        self.bar_index = 0
        self.hold_bars = int(_f(config.get("hold_bars", 0), 0))
        self.close_sent = False
        self.cooldown_until_bar = 0
 
    def on_candle(self, candle):
        self.bar_index += 1
 
        if self.bar_index < self.cooldown_until_bar:
            return
 
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

    def on_position(self, position):
        if not isinstance(position, dict):
            return
        sym = position.get("symbol")
        if sym and self.symbol and sym != self.symbol:
            return
        status = str(position.get("status") or "").lower()
        qty = _f(position.get("qty", 0), 0.0)
        self.log(f"POSITION_UPDATE sym={self.symbol} status={status} qty={qty}")
        if status == "closed" or qty <= 0:
            if not self.repeat_on_flat:
                self.log("ORDER reentry skipped: repeat_on_flat=false")
                return
            self.order_sent = False
            self.close_sent = False
            if self.cooldown_bars > 0:
                self.cooldown_until_bar = self.bar_index + self.cooldown_bars
            if self.bar_index < self.cooldown_until_bar:
                self.log(f"ORDER reentry delayed: cooldown_left={self.cooldown_until_bar - self.bar_index}")
                return
            if self.symbol in self.pending_orders:
                self.log("ORDER reentry delayed: pending_orders=1")
                return
            if self.trade_amount <= 0 or self.side not in ("buy", "sell"):
                self.log(f"ORDER reentry blocked: side={self.side} amount={self.trade_amount}")
                return
            self.log(f"ORDER reentry side={self.side} amount={self.trade_amount}")
            if self.side == "buy":
                self.buy(self.trade_amount, 0)
            else:
                self.sell(self.trade_amount, 0)
            self.order_sent = True
 
 
if __name__ == "__main__":
    config_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    config = json.loads(config_str)
    strategy = BasicMarketOrderStrategy(config)
    strategy.run()
