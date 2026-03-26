import json
import math
import time

try:
    from mini_redis import MiniRedis  # optional external module
except Exception:
    pass


class Config:
    MAX_PRICE = 5.0
    MIN_PRECISION = 5
    MIN_VOLATILITY = 5.0
    MIN_CONFIDENCE = 0.70
    TP_RATIO = 0.06
    SL_RATIO = 0.03
    ATR_DISCOUNT_THR = 1.5
    ATR_DISCOUNT = 0.7

    VOL_MA_PERIOD = 20
    VOL_BOOST_THR = 1.5
    VOL_BOOST_WEIGHT = 0.10
    EMA_FAST = 21
    EMA_SLOW = 55
    TREND_BOOST = 0.10
    TREND_PENALTY = 0.15

    SR_WINDOW = 5
    SR_LOOKBACK = 60
    CVD_PERIOD = 20
    CVD_BOOST = 0.10
    CVD_PENALTY = 0.10
    NEAR_SR_DIST = 0.015
    NEAR_SR_BOOST = 0.08
    DYNAMIC_SL_BUFFER = 0.002

    CHANGE_LOOKBACK_BARS = 200
    VOL_LOOKBACK_BARS = 200
    COOLDOWN_SEC = 0
    MAX_BARS = 300


def _precision_of_price(px: float) -> int:
    s = f"{px:.12f}".rstrip("0").rstrip(".")
    if "." not in s:
        return 0
    return len(s.split(".", 1)[1])


def _sma(xs, n: int):
    if n <= 0 or len(xs) < n:
        return None
    return sum(xs[-n:]) / n


def _ema(xs, n: int):
    if n <= 0 or not xs:
        return None
    k = 2 / (n + 1)
    v = xs[0]
    for x in xs[1:]:
        v = x * k + v * (1 - k)
    return v


def _stddev(xs, n: int):
    if n <= 1 or len(xs) < n:
        return None
    w = xs[-n:]
    m = sum(w) / n
    var = sum((x - m) ** 2 for x in w) / (n - 1)
    return math.sqrt(var)


def calc_rsi(closes, period: int = 14) -> float:
    if len(closes) < period + 1:
        return 50.0
    gains = []
    losses = []
    for i in range(-period, 0):
        d = closes[i] - closes[i - 1]
        if d >= 0:
            gains.append(d)
            losses.append(0.0)
        else:
            gains.append(0.0)
            losses.append(-d)
    avg_gain = sum(gains) / period
    avg_loss = sum(losses) / period
    if avg_loss == 0:
        return 100.0
    rs = avg_gain / avg_loss
    return 100 - 100 / (1 + rs)


def calc_macd(closes):
    if len(closes) < 35:
        return 0.0, 0.0
    ema12 = _ema(closes, 12)
    ema26 = _ema(closes, 26)
    if ema12 is None or ema26 is None:
        return 0.0, 0.0
    macd_line = ema12 - ema26
    series = []
    if len(closes) >= 35:
        for i in range(-35, 0):
            sub = closes[:i] if i != 0 else closes
            e12 = _ema(sub, 12)
            e26 = _ema(sub, 26)
            if e12 is None or e26 is None:
                continue
            series.append(e12 - e26)
    signal = _ema(series, 9) if series else 0.0
    return macd_line, signal or 0.0


def calc_bollinger(closes, period: int = 20, k: float = 2.0):
    m = _sma(closes, period)
    s = _stddev(closes, period)
    if m is None or s is None:
        return None, None, None
    return m, m + k * s, m - k * s


def calc_atr_pct(ohlcv, period: int = 14) -> float:
    if len(ohlcv) < period + 1:
        return 0.0
    trs = []
    for i in range(-period, 0):
        hi = ohlcv[i]["high"]
        lo = ohlcv[i]["low"]
        prev_close = ohlcv[i - 1]["close"]
        tr = max(hi - lo, abs(hi - prev_close), abs(lo - prev_close))
        trs.append(tr)
    atr = sum(trs) / period
    px = ohlcv[-1]["close"]
    if px <= 0:
        return 0.0
    return (atr / px) * 100


def calc_volume_ratio(vols, ma_period: int):
    base = _sma(vols, ma_period)
    if base is None or base <= 0:
        return 1.0
    return vols[-1] / base


def calc_ema_trend(closes, fast: int, slow: int) -> str:
    ef = _ema(closes, fast)
    es = _ema(closes, slow)
    if ef is None or es is None:
        return "flat"
    if ef > es:
        return "up"
    if ef < es:
        return "down"
    return "flat"


def calc_cvd_trend(ohlcv, period: int) -> str:
    if len(ohlcv) < period + 2:
        return "flat"
    cvd = 0.0
    hist = []
    for bar in ohlcv[-(period * 2 + 2) :]:
        hi = bar["high"]
        lo = bar["low"]
        cl = bar["close"]
        vol = bar["vol"]
        if hi <= lo:
            clv = 0.0
        else:
            clv = (2 * cl - hi - lo) / (hi - lo)
        cvd += clv * vol
        hist.append(cvd)
    if len(hist) < period + 1:
        return "flat"
    d = hist[-1] - hist[-1 - period]
    if abs(d) < 1e-9:
        return "flat"
    return "up" if d > 0 else "down"


def calc_support_resistance(ohlcv, current_price: float, window: int, lookback: int):
    if not ohlcv:
        return current_price * 0.95, current_price * 1.05, 0.05, 0.05
    data = ohlcv[-lookback:]
    lows = [b["low"] for b in data]
    highs = [b["high"] for b in data]
    supports = []
    resistances = []
    w = max(2, int(window))
    half = w // 2
    for i in range(half, len(data) - half):
        seg_l = lows[i - half : i + half + 1]
        seg_h = highs[i - half : i + half + 1]
        if lows[i] == min(seg_l):
            supports.append(lows[i])
        if highs[i] == max(seg_h):
            resistances.append(highs[i])
    supports = [s for s in supports if s < current_price]
    resistances = [r for r in resistances if r > current_price]
    nearest_sup = max(supports) if supports else current_price * 0.95
    nearest_res = min(resistances) if resistances else current_price * 1.05
    dist_sup = (current_price - nearest_sup) / current_price if current_price > 0 else 0.0
    dist_res = (nearest_res - current_price) / current_price if current_price > 0 else 0.0
    return nearest_sup, nearest_res, dist_sup, dist_res


def calc_dynamic_sl(direction: str, entry: float, nearest_sup: float, nearest_res: float, default_sl: float):
    buf = Config.DYNAMIC_SL_BUFFER
    if direction == "long":
        dynamic = nearest_sup * (1 - buf)
        if dynamic > default_sl:
            return dynamic, True
        return default_sl, False
    dynamic = nearest_res * (1 + buf)
    if dynamic < default_sl:
        return dynamic, True
    return default_sl, False


def score_confidence(
    rsi_val: float,
    macd_val: float,
    macd_sig: float,
    price: float,
    bb_upper: float,
    bb_lower: float,
    funding_rate: float,
    ls_ratio: float,
    atr_pct: float,
    vol_ratio: float,
    ema_trend: str,
    cvd_trend: str,
    dist_sup: float,
    dist_res: float,
):
    ls = 0.0
    ss = 0.0

    if rsi_val < 35:
        ls += 0.25
    elif rsi_val > 65:
        ss += 0.25

    if macd_val > macd_sig:
        ls += 0.25
    else:
        ss += 0.25

    if bb_lower is not None and price <= bb_lower:
        ls += 0.15
    elif bb_upper is not None and price >= bb_upper:
        ss += 0.15

    if funding_rate < -0.0003:
        ls += 0.20
    elif funding_rate > 0.0003:
        ss += 0.20

    if ls_ratio < 0.8:
        ls += 0.15
    elif ls_ratio > 1.5:
        ss += 0.15

    if atr_pct < Config.ATR_DISCOUNT_THR:
        ls *= Config.ATR_DISCOUNT
        ss *= Config.ATR_DISCOUNT

    vol_boost = vol_ratio >= Config.VOL_BOOST_THR
    if vol_boost:
        ls += Config.VOL_BOOST_WEIGHT
        ss += Config.VOL_BOOST_WEIGHT

    if ema_trend == "up":
        ls += Config.TREND_BOOST
        ss -= Config.TREND_PENALTY
    elif ema_trend == "down":
        ss += Config.TREND_BOOST
        ls -= Config.TREND_PENALTY

    if cvd_trend == "up":
        ls += Config.CVD_BOOST
        ss -= Config.CVD_PENALTY
    elif cvd_trend == "down":
        ss += Config.CVD_BOOST
        ls -= Config.CVD_PENALTY

    if dist_sup <= Config.NEAR_SR_DIST:
        ls += Config.NEAR_SR_BOOST
    if dist_res <= Config.NEAR_SR_DIST:
        ss += Config.NEAR_SR_BOOST

    ls = max(ls, 0.0)
    ss = max(ss, 0.0)

    if ls >= Config.MIN_CONFIDENCE and ls >= ss:
        return ls, "long", vol_boost, ema_trend == "up"
    if ss >= Config.MIN_CONFIDENCE and ss > ls:
        return ss, "short", vol_boost, ema_trend == "down"
    dominant = "long" if ls >= ss else "short"
    align = (ema_trend == "up") if dominant == "long" else (ema_trend == "down")
    return max(ls, ss), None, vol_boost, align


class MemeSignalEngineV3:
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
        self.owner_id = self.cfg.get("owner_id", 0)
        self.symbols = self._parse_symbols(self.cfg.get("symbols", ""))
        self.cooldown_sec = int(self.cfg.get("cooldown_sec", Config.COOLDOWN_SEC))
        self.last_signal_at = 0.0
        self.market = str(self.cfg.get("market", "usdm")).lower()
        self.trade_amount = float(self.cfg.get("order_amount", 100.0))

        self.candles = {}

    def _parse_symbols(self, s: str):
        if not s:
            return []
        out = []
        for part in s.split(","):
            p = part.strip()
            if p:
                out.append(p)
        return out

    def _push_candle(self, sym: str, c: dict):
        arr = self.candles.get(sym)
        if arr is None:
            arr = []
            self.candles[sym] = arr
        arr.append(c)
        if len(arr) > Config.MAX_BARS:
            del arr[0 : len(arr) - Config.MAX_BARS]

    def _compute(self, sym: str, candle_list):
        if len(candle_list) < 80:
            return None
        price = candle_list[-1]["close"]
        if price <= 0 or price > Config.MAX_PRICE:
            return None
        if _precision_of_price(price) < Config.MIN_PRECISION:
            return None
        closes = [b["close"] for b in candle_list]
        vols = [b["vol"] for b in candle_list]
        look = min(Config.CHANGE_LOOKBACK_BARS, len(closes) - 1)
        change_pct = ((closes[-1] / closes[-1 - look]) - 1) * 100 if closes[-1 - look] > 0 else 0.0
        if abs(change_pct) < Config.MIN_VOLATILITY:
            return None
        funding = float(self.cfg.get("funding_rate", 0.0))
        ls_ratio = float(self.cfg.get("ls_ratio", 1.0))

        rsi_val = calc_rsi(closes)
        macd_val, macd_sig = calc_macd(closes)
        _, bb_upper, bb_lower = calc_bollinger(closes)
        atr_pct = calc_atr_pct(candle_list)
        vol_ratio = calc_volume_ratio(vols, Config.VOL_MA_PERIOD)
        ema_trend = calc_ema_trend(closes, Config.EMA_FAST, Config.EMA_SLOW)
        cvd_trend = calc_cvd_trend(candle_list, Config.CVD_PERIOD)
        nearest_sup, nearest_res, dist_sup, dist_res = calc_support_resistance(
            candle_list, price, Config.SR_WINDOW, Config.SR_LOOKBACK
        )

        confidence, direction, _, _ = score_confidence(
            rsi_val,
            macd_val,
            macd_sig,
            price,
            bb_upper,
            bb_lower,
            funding,
            ls_ratio,
            atr_pct,
            vol_ratio,
            ema_trend,
            cvd_trend,
            dist_sup,
            dist_res,
        )
        if direction is None:
            return None

        if direction == "long":
            default_tp = price * (1 + Config.TP_RATIO)
            default_sl = price * (1 - Config.SL_RATIO)
            sl, _ = calc_dynamic_sl("long", price, nearest_sup, nearest_res, default_sl)
            tp = default_tp
            side = "buy"
        else:
            default_tp = price * (1 - Config.TP_RATIO)
            default_sl = price * (1 + Config.SL_RATIO)
            sl, _ = calc_dynamic_sl("short", price, nearest_sup, nearest_res, default_sl)
            tp = default_tp
            side = "sell"

        return {
            "symbol": sym,
            "side": side,
            "entry": price,
            "tp": tp,
            "sl": sl,
            "confidence": confidence,
        }

    def _emit_signal(self, r: MiniRedis, sig: dict):
        msg = {
            "type": "signal",
            "strategy_id": self.strategy_id,
            "owner_id": self.owner_id,
            "action": "open",
            "symbol": sig["symbol"],
            "side": sig["side"],
            "amount": self.trade_amount,
            "price": 0,
            "take_profit": sig["tp"],
            "stop_loss": sig["sl"],
            "confidence": sig["confidence"],
            "generated_at": time.time(),
        }
        ch = f"{self.redis_prefix}:signal:{self.strategy_id}"
        r.publish(ch, json.dumps(msg, ensure_ascii=False))
        if self.redis_prefix != "qt":
            r.publish(f"qt:signal:{self.strategy_id}", json.dumps(msg, ensure_ascii=False))

    def _emit_ready(self, r: MiniRedis):
        msg = {
            "type": "ready",
            "strategy_id": self.strategy_id,
            "owner_id": self.owner_id,
        }
        ch = f"{self.redis_prefix}:state:{self.strategy_id}"
        r.publish(ch, json.dumps(msg, ensure_ascii=False))
        if self.redis_prefix != "qt":
            r.publish(f"qt:state:{self.strategy_id}", json.dumps(msg, ensure_ascii=False))

    def _log(self, text: str):
        try:
            print(json.dumps({"type": "log", "data": text}, ensure_ascii=False), flush=True)
        except Exception:
            pass

    def run(self):
        use_redis = True
        v = self.cfg.get("use_redis")
        if isinstance(v, bool):
            use_redis = v
        if use_redis:
            try:
                MiniRedis
            except NameError:
                use_redis = False
        if use_redis:
            try:
                if not self.strategy_id:
                    raise RuntimeError("strategy_id required")
                candidates = [(self.redis_host, self.redis_port)]
                if self.redis_host in ("127.0.0.1", "localhost"):
                    candidates.append(("host.docker.internal", self.redis_port))
                r = None
                last_err = None
                for h, p in candidates:
                    try:
                        self._log(f"Connecting Redis host={h} port={p} db={self.redis_db} prefix={self.redis_prefix}")
                        r = MiniRedis(h, p, self.redis_password, self.redis_db).connect()
                        self._log("Redis connected")
                        break
                    except Exception as e:
                        last_err = e
                        self._log(f"Redis connect failed host={h}:{p} err={e}")
                        time.sleep(0.5)
                if r is None:
                    raise last_err or RuntimeError("Redis connect failed")
                ps = r.pubsub()
                for sym in self.symbols:
                    ch1 = f"{self.redis_prefix}:candle:{self.strategy_id}:{sym}"
                    ps.psubscribe(ch1)
                    if self.redis_prefix != "qt":
                        ps.psubscribe(f"qt:candle:{self.strategy_id}:{sym}")
                self._emit_ready(r)
                self._log("Ready published")
                while True:
                    msg = ps.get_message(timeout=1.0)
                    if not msg:
                        continue
                    if msg.get("type") not in ("pmessage", "message"):
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
                    if self.cooldown_sec > 0 and time.time() - self.last_signal_at < self.cooldown_sec:
                        continue
                    sig = self._compute(sym, self.candles.get(sym, []))
                    if not sig:
                        continue
                    self._emit_signal(r, sig)
                    self.last_signal_at = time.time()
            except Exception:
                self._base_run()
        else:
            self._base_run()

    def _base_send_log(self, text: str):
        line = {"type": "log", "data": text}
        print(json.dumps(line, ensure_ascii=False), flush=True)

    def _base_send_order(self, sym: str, side: str, qty: float, price: float = 0.0):
        data = {"symbol": sym, "side": side, "amount": qty, "price": price}
        line = {"type": "order", "data": data}
        print(json.dumps(line, ensure_ascii=False), flush=True)

    def on_order(self, order):
        return

    def on_candle(self, sym: str):
        if self.cooldown_sec > 0 and time.time() - self.last_signal_at < self.cooldown_sec:
            return
        sig = self._compute(sym, self.candles.get(sym, []))
        if not sig:
            return
        self._base_send_order(sig["symbol"], sig["side"], self.trade_amount, 0.0)
        self.last_signal_at = time.time()

    def _base_run(self):
        import sys
        cfg_text = ""
        if len(sys.argv) >= 2:
            cfg_text = sys.argv[1]
        if cfg_text:
            try:
                c = json.loads(cfg_text)
                if isinstance(c, dict):
                    self.cfg.update(c)
            except Exception:
                pass
        while True:
            line = sys.stdin.readline()
            if not line:
                time.sleep(0.05)
                continue
            try:
                msg = json.loads(line.strip())
            except Exception:
                continue
            if msg.get("type") != "candle":
                continue
            d = msg.get("data") or {}
            sym = d.get("symbol") or d.get("s") or ""
            if not sym:
                continue
            c = {
                "open": float(d.get("open", 0) or d.get("o", 0) or 0),
                "high": float(d.get("high", 0) or d.get("h", 0) or 0),
                "low": float(d.get("low", 0) or d.get("l", 0) or 0),
                "close": float(d.get("close", 0) or d.get("c", 0) or 0),
                "vol": float(d.get("volume", 0) or d.get("vol", 0) or 0),
                "ts": d.get("timestamp") or d.get("ts") or 0,
            }
            self._push_candle(sym, c)
            self.on_candle(sym)


if __name__ == "__main__":
    import os

    cfg = {}
    if os.environ.get("STRATEGY_CONFIG_JSON"):
        try:
            cfg = json.loads(os.environ.get("STRATEGY_CONFIG_JSON"))
        except Exception:
            cfg = {}
    MemeSignalEngineV3(cfg).run()
