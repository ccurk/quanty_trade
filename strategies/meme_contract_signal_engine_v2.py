import json
import time

try:
    from mini_redis import MiniRedis
except Exception:
    pass


class Config:
    TP_RATIO = 0.06
    SL_RATIO = 0.03
    MAX_BARS = 200


class MemeSignalEngineV2:
    def __init__(self, cfg=None):
        self.cfg = cfg or {}
        addr = str(self.cfg.get("redis_addr", "") or "").strip()
        self.redis_host = self.cfg.get("redis_host", "127.0.0.1")
        self.redis_port = int(self.cfg.get("redis_port", 6379))
        if addr:
            try:
                if ":" in addr:
                    h, p = addr.split(":", 1)
                    self.redis_host = h.strip() or self.redis_host
                    self.redis_port = int(p.strip())
            except Exception:
                pass
        self.redis_db = int(self.cfg.get("redis_db", 0))
        self.redis_password = self.cfg.get("redis_password", "")
        self.redis_prefix = str(self.cfg.get("redis_prefix", "qt") or "qt")
        self.strategy_id = self.cfg.get("strategy_id", "")
        self.owner_id = int(self.cfg.get("owner_id", 0))
        self.symbols = self._parse_symbols(self.cfg.get("symbols", ""))
        self.amount = float(self.cfg.get("order_amount", 100.0))
        self.candles = {}

    def _parse_symbols(self, s: str):
        if not s:
            return []
        return [p.strip() for p in s.split(",") if p.strip()]

    def _push_candle(self, sym: str, c: dict):
        arr = self.candles.get(sym)
        if arr is None:
            arr = []
            self.candles[sym] = arr
        arr.append(c)
        if len(arr) > Config.MAX_BARS:
            del arr[0 : len(arr) - Config.MAX_BARS]

    def _compute(self, sym: str, arr):
        if not arr:
            return None
        px = float(arr[-1]["close"])
        if px <= 0:
            return None
        tp = round(px * (1 + Config.TP_RATIO), 10)
        sl = round(px * (1 - Config.SL_RATIO), 10)
        return {"symbol": sym, "side": "buy", "entry": px, "tp": tp, "sl": sl}

    def _emit_ready(self, r: MiniRedis):
        msg = {"type": "ready", "strategy_id": self.strategy_id, "owner_id": self.owner_id}
        r.publish(f"{self.redis_prefix}:state:{self.strategy_id}", json.dumps(msg, ensure_ascii=False))

    def _emit_signal(self, r: MiniRedis, sig: dict):
        msg = {
            "type": "signal",
            "strategy_id": self.strategy_id,
            "owner_id": self.owner_id,
            "action": "open",
            "symbol": sig["symbol"],
            "side": sig["side"],
            "amount": self.amount,
            "price": 0,
            "take_profit": sig["tp"],
            "stop_loss": sig["sl"],
            "confidence": 0.5,
            "generated_at": time.time(),
        }
        r.publish(f"{self.redis_prefix}:signal:{self.strategy_id}", json.dumps(msg, ensure_ascii=False))

    def run(self):
        try:
            MiniRedis
        except NameError:
            raise RuntimeError("MiniRedis required")
        if not self.strategy_id:
            raise RuntimeError("strategy_id required")
        r = MiniRedis(self.redis_host, self.redis_port, self.redis_password, self.redis_db).connect()
        self._emit_ready(r)
        ps = r.pubsub()
        for sym in self.symbols:
            ps.psubscribe(f"{self.redis_prefix}:candle:{self.strategy_id}:{sym}")
        while True:
            msg = ps.get_message(timeout=1.0)
            if not msg or msg.get("type") not in ("pmessage", "message"):
                continue
            data = msg.get("data")
            if not data:
                continue
            try:
                payload = json.loads(data)
            except Exception:
                continue
            sym = payload.get("symbol") or payload.get("s") or ""
            if not sym:
                continue
            c = {
                "open": float(payload.get("open", 0) or 0),
                "high": float(payload.get("high", 0) or 0),
                "low": float(payload.get("low", 0) or 0),
                "close": float(payload.get("close", 0) or 0),
                "vol": float(payload.get("volume", 0) or payload.get("vol", 0) or 0),
                "ts": payload.get("timestamp") or payload.get("ts") or 0,
            }
            self._push_candle(sym, c)
            sig = self._compute(sym, self.candles.get(sym, []))
            if sig:
                self._emit_signal(r, sig)


if __name__ == "__main__":
    import os
    cfg = {}
    if os.environ.get("STRATEGY_CONFIG_JSON"):
        try:
            cfg = json.loads(os.environ.get("STRATEGY_CONFIG_JSON"))
        except Exception:
            cfg = {}
    MemeSignalEngineV2(cfg).run()
