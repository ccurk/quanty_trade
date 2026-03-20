from base_strategy import BaseStrategy
import json
import sys
from datetime import datetime, timezone
 
 
def _f(v, d=0.0):
    try:
        if v is None:
            return float(d)
        return float(v)
    except Exception:
        return float(d)
 
 
def _i(v, d=0):
    try:
        if v is None:
            return int(d)
        return int(float(v))
    except Exception:
        return int(d)
 
 
def _b(v, d=False):
    try:
        if isinstance(v, bool):
            return v
        if v is None:
            return bool(d)
        if isinstance(v, (int, float)):
            return v != 0
        s = str(v).strip().lower()
        if s in ("1", "true", "yes", "y", "on"):
            return True
        if s in ("0", "false", "no", "n", "off"):
            return False
        return bool(d)
    except Exception:
        return bool(d)
 
 
def _ts_to_date(ts):
    if isinstance(ts, (int, float)):
        try:
            return datetime.fromtimestamp(float(ts) / 1000.0, tz=timezone.utc).date()
        except Exception:
            return datetime.now(tz=timezone.utc).date()
    if isinstance(ts, str):
        try:
            return datetime.fromisoformat(ts.replace("Z", "+00:00")).date()
        except Exception:
            return datetime.now(tz=timezone.utc).date()
    return datetime.now(tz=timezone.utc).date()
 
 
def _sma(xs, n):
    if n <= 0 or len(xs) < n:
        return None
    s = 0.0
    for v in xs[-n:]:
        s += float(v)
    return s / float(n)
 
 
class ProductionSelectorTrendMAStrategy(BaseStrategy):
    def __init__(self, config):
        super().__init__(config)
        self.fast_window = _i(config.get("fast_window", 10), 10)
        self.slow_window = _i(config.get("slow_window", 30), 30)
        if self.fast_window <= 0:
            self.fast_window = 10
        if self.slow_window <= 0:
            self.slow_window = 30
        if self.fast_window >= self.slow_window:
            self.fast_window = max(1, self.slow_window // 2)
 
        self.trade_amount = _f(config.get("trade_amount", 0), 0.0)
        self.take_profit_pct = _f(config.get("take_profit_pct", 0.02), 0.02)
        self.stop_loss_pct = _f(config.get("stop_loss_pct", 0.01), 0.01)
        self.trailing_stop_pct = _f(config.get("trailing_stop_pct", 0.0), 0.0)
        self.cooldown_bars = _i(config.get("cooldown_bars", 0), 0)
        self.max_hold_bars = _i(config.get("max_hold_bars", 0), 0)
        self.max_concurrent_positions = _i(config.get("max_concurrent_positions", 1), 1)
        if self.max_concurrent_positions <= 0:
            self.max_concurrent_positions = 1
        self.max_trades_per_day = _i(config.get("max_trades_per_day", 10), 10)
        if self.max_trades_per_day <= 0:
            self.max_trades_per_day = 10
        self.allow_reentry = _b(config.get("repeat_on_flat", False), False)
 
        self.entry_mode = str(config.get("entry_mode", "trend") or "trend").strip().lower()
        if self.entry_mode not in ("crossover", "trend"):
            self.entry_mode = "trend"
        self.confirm_bars = _i(config.get("confirm_bars", 1), 1)
        if self.confirm_bars < 1:
            self.confirm_bars = 1
        if self.confirm_bars > 5:
            self.confirm_bars = 5
 
        self.status_interval_bars = _i(config.get("status_interval_bars", 10), 10)
        if self.status_interval_bars < 0:
            self.status_interval_bars = 0
 
        self.debug = _b(config.get("debug", False), False)
        self.debug_interval_bars = _i(config.get("debug_interval_bars", 10), 10)
        if self.debug_interval_bars <= 0:
            self.debug_interval_bars = 10
 
        self.symbols = []
        raw_syms = config.get("symbols")
        if isinstance(raw_syms, list):
            for s in raw_syms:
                if isinstance(s, str) and s.strip():
                    self.symbols.append(s.strip())
        if not self.symbols and isinstance(self.symbol, str) and self.symbol.strip():
            self.symbols = [self.symbol.strip()]
 
        self.series = {}
        self.state = {}
        self._global_bar = 0
        self.current_day = None
        self.trades_today = 0
 
        for s in self.symbols:
            self.series[s] = []
            self.state[s] = {
                "bar": 0,
                "entry_price": 0.0,
                "entry_bar": 0,
                "high_water": 0.0,
                "cooldown_until": 0,
                "last_fast": None,
                "last_slow": None,
                "confirm_left": 0,
                "trend_up_count": 0,
                "last_exit_reason": "",
            }
 
        self.log(
            "CONFIG "
            f"symbols={len(self.symbols)} fast={self.fast_window} slow={self.slow_window} "
            f"qty={self.trade_amount} tp={self.take_profit_pct} sl={self.stop_loss_pct} ts={self.trailing_stop_pct} "
            f"cooldown={self.cooldown_bars} max_hold={self.max_hold_bars} max_concurrent={self.max_concurrent_positions} max_trades_day={self.max_trades_per_day} "
            f"reentry={self.allow_reentry} entry_mode={self.entry_mode} confirm_bars={self.confirm_bars} "
            f"status_int={self.status_interval_bars} debug={self.debug} debug_int={self.debug_interval_bars}"
        )
 
    def _on_new_day(self, day):
        if self.current_day is None:
            self.current_day = day
            self.trades_today = 0
            return
        if day != self.current_day:
            self.current_day = day
            self.trades_today = 0
            self.log(f"DAY_RESET day={self.current_day}")
 
    def _open_positions_count(self):
        n = 0
        for sym in self.symbols:
            if self.in_position(sym):
                n += 1
        return n
 
    def _can_open(self, sym):
        st = self.state.get(sym)
        if st is None:
            return False, "unknown_symbol"
        if self._open_positions_count() >= self.max_concurrent_positions:
            return False, "max_concurrent_positions"
        if self.trade_amount <= 0:
            return False, "trade_amount<=0"
        if self.trades_today >= self.max_trades_per_day:
            return False, "max_trades_per_day"
        if st["bar"] < st["cooldown_until"]:
            return False, f"cooldown_left={st['cooldown_until'] - st['bar']}"
        if sym in self.pending_orders:
            return False, "pending_orders"
        if self.in_position(sym):
            return False, "already_in_position"
        return True, ""
 
    def _risk_check(self, sym, close_price):
        st = self.state.get(sym)
        if st is None:
            return
        if not self.in_position(sym):
            return
        if sym in self.pending_orders:
            return
        ep = float(st.get("entry_price") or 0.0)
        if ep <= 0 or close_price <= 0:
            return
 
        if close_price > st["high_water"]:
            st["high_water"] = close_price
 
        stop_from_sl = ep * (1.0 - max(0.0, self.stop_loss_pct))
        stop_from_ts = 0.0
        if self.trailing_stop_pct > 0 and st["high_water"] > 0:
            stop_from_ts = st["high_water"] * (1.0 - self.trailing_stop_pct)
        stop_level = max(stop_from_sl, stop_from_ts)
        tp_level = ep * (1.0 + max(0.0, self.take_profit_pct))
 
        if self.max_hold_bars > 0 and (st["bar"] - st["entry_bar"]) >= self.max_hold_bars:
            self.log(f"EXIT time_stop sym={sym} bar={st['bar']} hold={st['bar'] - st['entry_bar']}")
            st["last_exit_reason"] = "time_stop"
            self.close_position_for(sym, 0)
            return
 
        if stop_level > 0 and close_price <= stop_level:
            self.log(f"EXIT stop sym={sym} close={close_price} stop={stop_level} entry={ep} high={st['high_water']}")
            st["last_exit_reason"] = "stop"
            self.close_position_for(sym, 0)
            return
 
        if tp_level > 0 and close_price >= tp_level:
            self.log(f"EXIT take_profit sym={sym} close={close_price} tp={tp_level} entry={ep}")
            st["last_exit_reason"] = "take_profit"
            self.close_position_for(sym, 0)
            return
 
    def _signal(self, sym):
        xs = self.series.get(sym) or []
        fast = _sma(xs, self.fast_window)
        slow = _sma(xs, self.slow_window)
        if fast is None or slow is None:
            return None, None, None
        st = self.state[sym]
        crossed_up = False
        if st["last_fast"] is not None and st["last_slow"] is not None:
            crossed_up = (st["last_fast"] <= st["last_slow"]) and (fast > slow)
        st["last_fast"] = fast
        st["last_slow"] = slow
        return fast, slow, crossed_up
 
    def on_candle(self, candle):
        self._global_bar += 1
        if not isinstance(candle, dict):
            return
        sym = candle.get("symbol") or self.symbol
        if not isinstance(sym, str) or not sym.strip():
            return
        sym = sym.strip()
        if sym not in self.state:
            self.symbols.append(sym)
            self.series[sym] = []
            self.state[sym] = {
                "bar": 0,
                "entry_price": 0.0,
                "entry_bar": 0,
                "high_water": 0.0,
                "cooldown_until": 0,
                "last_fast": None,
                "last_slow": None,
                "confirm_left": 0,
                "trend_up_count": 0,
                "last_exit_reason": "",
            }
 
        st = self.state[sym]
        st["bar"] += 1
        close_price = _f(candle.get("close"), 0.0)
        ts = candle.get("timestamp")
        self._on_new_day(_ts_to_date(ts))
 
        if close_price <= 0:
            return
 
        xs = self.series[sym]
        xs.append(close_price)
        if len(xs) > max(self.slow_window, 50) + 300:
            self.series[sym] = xs[-(max(self.slow_window, 50) + 300):]
 
        fast, slow, crossed_up = self._signal(sym)
        if fast is None or slow is None:
            if self.debug and (st["bar"] % self.debug_interval_bars == 0):
                self.log(f"DEBUG sym={sym} bar={st['bar']} warmup len={len(self.series[sym])} need={self.slow_window}")
            return
 
        self._risk_check(sym, close_price)
 
        if self.status_interval_bars > 0 and (st["bar"] % self.status_interval_bars == 0):
            ok, reason = self._can_open(sym)
            in_pos = 1 if self.in_position(sym) else 0
            extra = ""
            if in_pos:
                ep = float(st.get("entry_price") or 0.0)
                hw = float(st.get("high_water") or 0.0)
                if ep > 0:
                    stop_from_sl = ep * (1.0 - max(0.0, self.stop_loss_pct))
                    stop_from_ts = 0.0
                    if self.trailing_stop_pct > 0 and hw > 0:
                        stop_from_ts = hw * (1.0 - self.trailing_stop_pct)
                    stop_level = max(stop_from_sl, stop_from_ts)
                    tp_level = ep * (1.0 + max(0.0, self.take_profit_pct))
                    extra = f" entry={ep} tp={tp_level} stop={stop_level} high={hw}"
            self.log(
                f"HEARTBEAT sym={sym} bar={st['bar']} close={close_price} fast={fast:.6f} slow={slow:.6f} "
                f"in_pos={in_pos} pending={1 if (sym in self.pending_orders) else 0} "
                f"can_open={1 if ok else 0} reason={reason} trades_today={self.trades_today}{extra}"
            )
 
        if self.in_position(sym):
            return
 
        if self.entry_mode == "trend":
            if fast > slow:
                st["trend_up_count"] += 1
            else:
                st["trend_up_count"] = 0
            if st["trend_up_count"] < self.confirm_bars:
                return
            ok, reason = self._can_open(sym)
            if not ok:
                if self.debug:
                    self.log(f"ENTRY blocked sym={sym} bar={st['bar']} reason={reason}")
                return
            self.log(f"ENTRY long(trend) sym={sym} bar={st['bar']} close={close_price} qty={self.trade_amount}")
            self.buy_for(sym, self.trade_amount, 0)
            self.trades_today += 1
            st["trend_up_count"] = 0
            return
 
        if crossed_up:
            st["confirm_left"] = self.confirm_bars
        if st["confirm_left"] > 0:
            st["confirm_left"] -= 1
            if fast <= slow:
                st["confirm_left"] = 0
                return
            if st["confirm_left"] != 0:
                return
            ok, reason = self._can_open(sym)
            if not ok:
                if self.debug:
                    self.log(f"ENTRY blocked sym={sym} bar={st['bar']} reason={reason}")
                return
            self.log(f"ENTRY long(crossover) sym={sym} bar={st['bar']} close={close_price} qty={self.trade_amount}")
            self.buy_for(sym, self.trade_amount, 0)
            self.trades_today += 1
            return
 
    def on_order(self, order):
        if not isinstance(order, dict):
            return
        sym = order.get("symbol") or self.symbol
        status = str(order.get("status") or "").lower()
        side = str(order.get("side") or "").lower()
        qty = _f(order.get("amount", 0), 0.0)
        price = _f(order.get("price", 0), 0.0)
        self.log(f"ORDER sym={sym} side={side} status={status} qty={qty} price={price}")
 
        if not isinstance(sym, str) or sym not in self.state:
            return
        st = self.state[sym]
 
        if status != "filled":
            return
 
        if side == "buy" and qty > 0:
            ep = price if price > 0 else _f(self.position_avg_price(sym), 0.0)
            st["entry_price"] = ep
            st["entry_bar"] = st["bar"]
            st["high_water"] = max(float(st.get("high_water") or 0.0), ep)
            st["last_exit_reason"] = ""
            if self.debug:
                self.log(f"STATE sym={sym} entry_price={st['entry_price']} entry_bar={st['entry_bar']}")
            return
 
        if side == "sell":
            exit_reason = st.get("last_exit_reason") or ""
            st["entry_price"] = 0.0
            st["entry_bar"] = 0
            st["high_water"] = 0.0
            if self.cooldown_bars > 0:
                st["cooldown_until"] = st["bar"] + self.cooldown_bars
            st["trend_up_count"] = 0
            st["confirm_left"] = 0
            st["last_exit_reason"] = ""
            if self.debug:
                self.log(f"STATE sym={sym} flat bar={st['bar']} cooldown_until={st['cooldown_until']} exit_reason={exit_reason or 'unknown'}")
 
    def on_position(self, position):
        if not isinstance(position, dict):
            return
        sym = position.get("symbol") or self.symbol
        status = str(position.get("status") or "").lower()
        qty = _f(position.get("qty", 0), 0.0)
        self.log(f"POSITION sym={sym} status={status} qty={qty}")
 
 
if __name__ == "__main__":
    config_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    config = json.loads(config_str)
    strategy = ProductionSelectorTrendMAStrategy(config)
    strategy.run()
