import json
import os
import sys
import time
import threading
from datetime import datetime, timezone

from mini_redis import MiniRedis


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


def _sma(xs, n):
    if n <= 0 or len(xs) < n:
        return None
    s = 0.0
    for v in xs[-n:]:
        s += float(v)
    return s / float(n)


def _now():
    return datetime.now(tz=timezone.utc).isoformat()


class RedisSignalStrategy:
    def __init__(self, config):
        self.cfg = config or {}
        self.strategy_id = str(self.cfg.get("strategy_id") or "").strip()
        self.owner_id = int(self.cfg.get("owner_id") or 0)
        self.prefix = str(self.cfg.get("redis_prefix") or os.getenv("REDIS_PREFIX") or "qt").strip() or "qt"
        self.redis_addr = str(self.cfg.get("redis_addr") or os.getenv("REDIS_ADDR") or "127.0.0.1:6379").strip()
        self.redis_password = str(self.cfg.get("redis_password") or os.getenv("REDIS_PASSWORD") or "")
        self.redis_db = _i(self.cfg.get("redis_db") if self.cfg.get("redis_db") is not None else os.getenv("REDIS_DB"), 0)

        self.fast_window = max(1, _i(self.cfg.get("fast_window", 10), 10))
        self.slow_window = max(2, _i(self.cfg.get("slow_window", 30), 30))
        if self.fast_window >= self.slow_window:
            self.fast_window = max(1, self.slow_window // 2)

        self.trade_amount = _f(self.cfg.get("trade_amount", 0.01), 0.01)
        self.take_profit_pct = max(0.0, _f(self.cfg.get("take_profit_pct", 0.03), 0.03))
        self.stop_loss_pct = max(0.0, _f(self.cfg.get("stop_loss_pct", 0.01), 0.01))

        self.symbols = []
        raw_syms = self.cfg.get("symbols")
        if isinstance(raw_syms, list):
            for s in raw_syms:
                if isinstance(s, str) and s.strip():
                    self.symbols.append(s.strip())
        if not self.symbols:
            sym = str(self.cfg.get("symbol") or "").strip()
            if sym:
                self.symbols = [sym]

        self.closes = {s: [] for s in self.symbols}
        self.inflight = set()

        host, port = (self.redis_addr.split(":") + ["6379"])[:2]
        self.r = MiniRedis(host=host, port=int(port), password=self.redis_password, db=self.redis_db).connect()
        self.pub = MiniRedis(host=host, port=int(port), password=self.redis_password, db=self.redis_db).connect()
        self.boot_id = f"{int(time.time() * 1000)}-{os.getpid()}"
        self.healthcheck = self.cfg.get("healthcheck") or {}

    def _candle_ch(self):
        return f"{self.prefix}:candle:{self.strategy_id}"

    def _signal_ch(self):
        return f"{self.prefix}:signal:{self.strategy_id}"

    def _state_ch(self):
        return f"{self.prefix}:state:{self.strategy_id}"

    def _heartbeat_loop(self):
        interval = 5
        try:
            if isinstance(self.healthcheck, dict):
                interval = int(self.healthcheck.get("interval_sec") or 5)
        except Exception:
            interval = 5
        if interval <= 0:
            interval = 5
        while True:
            try:
                self.pub.publish(self._state_ch(), json.dumps({"type": "heartbeat", "strategy_id": self.strategy_id, "boot_id": self.boot_id, "created_at": _now()}))
            except Exception:
                pass
            time.sleep(interval)

    def _emit_signal(self, symbol, side, amount, take_profit, stop_loss):
        msg = {
            "strategy_id": self.strategy_id,
            "owner_id": self.owner_id,
            "symbol": symbol,
            "action": "open",
            "side": side,
            "amount": float(amount),
            "take_profit": float(take_profit) if take_profit else 0.0,
            "stop_loss": float(stop_loss) if stop_loss else 0.0,
            "signal_id": f"{self.strategy_id}:{symbol}:{int(time.time() * 1000)}",
            "generated_at": datetime.now(tz=timezone.utc).isoformat(),
        }
        self.r.publish(self._signal_ch(), json.dumps(msg))
        sys.stdout.write(json.dumps({"type": "log", "data": f"SIGNAL open sym={symbol} side={side} qty={amount} tp={take_profit} sl={stop_loss} ts={_now()}"}) + "\n")
        sys.stdout.flush()

    def on_candle(self, candle):
        if isinstance(candle, dict) and candle.get("type") == "history":
            candles = candle.get("candles") or []
            if isinstance(candles, list):
                for it in candles:
                    if isinstance(it, dict):
                        self.on_candle(it)
            return

        symbol = str(candle.get("symbol") or "").strip()
        if not symbol or symbol not in self.closes:
            return
        close = _f(candle.get("close", 0), 0.0)
        if close <= 0:
            return

        xs = self.closes[symbol]
        xs.append(close)
        if len(xs) > self.slow_window + 5:
            self.closes[symbol] = xs[-(self.slow_window + 5):]
            xs = self.closes[symbol]

        fast = _sma(xs, self.fast_window)
        slow = _sma(xs, self.slow_window)
        if fast is None or slow is None:
            return

        if symbol in self.inflight:
            return

        if fast > slow:
            tp = close * (1.0 + self.take_profit_pct) if self.take_profit_pct > 0 else 0.0
            sl = close * (1.0 - self.stop_loss_pct) if self.stop_loss_pct > 0 else 0.0
            if self.trade_amount > 0:
                self.inflight.add(symbol)
                self._emit_signal(symbol, "buy", self.trade_amount, tp, sl)

    def run(self):
        if not self.strategy_id:
            raise RuntimeError("missing strategy_id")
        self.r.subscribe(self._candle_ch())
        self.pub.publish(self._state_ch(), json.dumps({"type": "ready", "strategy_id": self.strategy_id, "boot_id": self.boot_id, "created_at": _now()}))
        t = threading.Thread(target=self._heartbeat_loop, daemon=True)
        t.start()
        sys.stdout.write(json.dumps({"type": "log", "data": f"REDIS started strategy_id={self.strategy_id} ch={self._candle_ch()} ts={_now()}"}) + "\n")
        sys.stdout.flush()
        while True:
            item = self.r.read_pubsub_message()
            if not item:
                continue
            payload = item.get("data")
            if not payload:
                continue
            try:
                msg = json.loads(payload)
            except Exception:
                continue
            self.on_candle(msg)


if __name__ == "__main__":
    cfg_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    cfg = json.loads(cfg_str)
    RedisSignalStrategy(cfg).run()
