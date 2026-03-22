import os
import sys
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

"""
Meme 合约信号计算引擎 — 系统可用版（Redis 模式）
职责：只做指标计算 + 置信度评分 + 信号生成
无任何网络请求、无交易所接口调用
所有行情与历史数据均从 Go 后端经 Redis PubSub 推送
"""

import json
import os
import sys
import threading
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Optional

from mini_redis import MiniRedis

try:
    import pandas as pd

    _HAS_PANDAS = True
except Exception:
    pd = None
    _HAS_PANDAS = False


def _now():
    return datetime.now(tz=timezone.utc).isoformat()


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


def _s(v, d=""):
    try:
        if v is None:
            return d
        return str(v)
    except Exception:
        return d


def get_decimal_precision(price: float) -> int:
    s = f"{price:.10f}".rstrip("0")
    return len(s.split(".")[1]) if "." in s else 0


def sma(xs, n: int) -> Optional[float]:
    if n <= 0 or len(xs) < n:
        return None
    s = 0.0
    for v in xs[-n:]:
        s += float(v)
    return s / float(n)


def stddev(xs, n: int) -> Optional[float]:
    if n <= 1 or len(xs) < n:
        return None
    m = sma(xs, n)
    if m is None:
        return None
    v = 0.0
    for x in xs[-n:]:
        d = float(x) - float(m)
        v += d * d
    return (v / float(n)) ** 0.5


def ema_series(xs, span: int) -> list[float]:
    if span <= 0:
        return []
    alpha = 2.0 / float(span + 1)
    out: list[float] = []
    prev: Optional[float] = None
    for x in xs:
        xv = float(x)
        if prev is None:
            prev = xv
        else:
            prev = alpha * xv + (1.0 - alpha) * prev
        out.append(prev)
    return out


def calc_rsi(closes: list[float], period: int = 14) -> float:
    if len(closes) < period + 1:
        return 0.0
    gains: list[float] = []
    losses: list[float] = []
    for i in range(1, len(closes)):
        d = float(closes[i]) - float(closes[i - 1])
        gains.append(d if d > 0 else 0.0)
        losses.append(-d if d < 0 else 0.0)
    avg_gain = sma(gains, period)
    avg_loss = sma(losses, period)
    if avg_gain is None or avg_loss is None:
        return 0.0
    if avg_loss == 0:
        return 100.0
    rs = float(avg_gain) / float(avg_loss)
    return float(100.0 - 100.0 / (1.0 + rs))


def calc_rsi_pd(closes: list[float], period: int = 14) -> float:
    if not _HAS_PANDAS:
        return calc_rsi(closes, period)
    if len(closes) < period + 1:
        return 0.0
    s = pd.Series(closes, dtype="float64")
    delta = s.diff()
    gain = delta.clip(lower=0).rolling(period).mean()
    loss = (-delta.clip(upper=0)).rolling(period).mean()
    rs = gain / loss
    v = 100.0 - 100.0 / (1.0 + rs.iloc[-1])
    if pd.isna(v):
        return 0.0
    return float(v)


def calc_macd(closes: list[float]) -> tuple[float, float]:
    if len(closes) < 35:
        return 0.0, 0.0
    ema12 = ema_series(closes, 12)
    ema26 = ema_series(closes, 26)
    line = [a - b for a, b in zip(ema12, ema26)]
    sig = ema_series(line, 9)
    return float(line[-1]), float(sig[-1])


def calc_macd_pd(closes: list[float]) -> tuple[float, float]:
    if not _HAS_PANDAS:
        return calc_macd(closes)
    if len(closes) < 35:
        return 0.0, 0.0
    s = pd.Series(closes, dtype="float64")
    ema12 = s.ewm(span=12, adjust=False).mean()
    ema26 = s.ewm(span=26, adjust=False).mean()
    line = ema12 - ema26
    sig = line.ewm(span=9, adjust=False).mean()
    a = line.iloc[-1]
    b = sig.iloc[-1]
    if pd.isna(a) or pd.isna(b):
        return 0.0, 0.0
    return float(a), float(b)


def calc_bollinger(closes: list[float], period: int = 20) -> tuple[float, float, float]:
    m = sma(closes, period)
    sd = stddev(closes, period)
    if m is None or sd is None:
        return 0.0, 0.0, 0.0
    upper = float(m + 2.0 * sd)
    lower = float(m - 2.0 * sd)
    return float(m), upper, lower


def calc_bollinger_pd(closes: list[float], period: int = 20) -> tuple[float, float, float]:
    if not _HAS_PANDAS:
        return calc_bollinger(closes, period)
    if len(closes) < period:
        return 0.0, 0.0, 0.0
    s = pd.Series(closes, dtype="float64")
    ma = s.rolling(period).mean()
    sd = s.rolling(period).std()
    mid = ma.iloc[-1]
    stdv = sd.iloc[-1]
    if pd.isna(mid) or pd.isna(stdv):
        return 0.0, 0.0, 0.0
    upper = float(mid + 2.0 * stdv)
    lower = float(mid - 2.0 * stdv)
    return float(mid), upper, lower


def calc_atr_pct(highs: list[float], lows: list[float], closes: list[float], period: int = 14) -> float:
    if len(highs) < period + 1 or len(lows) < period + 1 or len(closes) < period + 1:
        return 0.0
    trs: list[float] = []
    for i in range(1, len(closes)):
        h = float(highs[i])
        l = float(lows[i])
        pc = float(closes[i - 1])
        tr = max(h - l, abs(h - pc), abs(l - pc))
        trs.append(tr)
    atr = sma(trs, period)
    if atr is None:
        return 0.0
    price = float(closes[-1])
    if price <= 0:
        return 0.0
    return float(atr) / price * 100.0


def calc_atr_pct_pd(highs: list[float], lows: list[float], closes: list[float], period: int = 14) -> float:
    if not _HAS_PANDAS:
        return calc_atr_pct(highs, lows, closes, period)
    if len(highs) < period + 1 or len(lows) < period + 1 or len(closes) < period + 1:
        return 0.0
    df = pd.DataFrame({"high": highs, "low": lows, "close": closes}, dtype="float64")
    high = df["high"]
    low = df["low"]
    close = df["close"]
    tr = pd.concat([high - low, (high - close.shift()).abs(), (low - close.shift()).abs()], axis=1).max(axis=1)
    atr = tr.rolling(period).mean().iloc[-1]
    price = close.iloc[-1]
    if pd.isna(atr) or pd.isna(price) or float(price) <= 0:
        return 0.0
    return float(atr) / float(price) * 100.0


def estimate_change_pct(closes: list[float], lookback: int) -> float:
    if len(closes) < 2:
        return 0.0
    n = min(max(1, int(lookback)), len(closes) - 1)
    base = float(closes[-(n + 1)])
    last = float(closes[-1])
    if base == 0:
        return 0.0
    return (last - base) / base * 100.0


def estimate_volatility_pct(highs: list[float], lows: list[float], lookback: int) -> float:
    if not highs or not lows:
        return 0.0
    n = min(max(1, int(lookback)), len(highs))
    hs = highs[-n:]
    ls = lows[-n:]
    hi = max(float(x) for x in hs)
    lo = min(float(x) for x in ls)
    if lo <= 0:
        return 0.0
    return (hi - lo) / lo * 100.0


class Config:
    MAX_PRICE = 5.0
    MIN_PRECISION = 5
    MIN_VOLATILITY = 5.0
    MIN_CONFIDENCE = 0.10
    TP_RATIO = 0.06
    SL_RATIO = 0.03
    ATR_DISCOUNT_THR = 1.5
    ATR_DISCOUNT = 0.7
    CHANGE_LOOKBACK_BARS = 200
    VOL_LOOKBACK_BARS = 200
    COOLDOWN_SEC = 300
    MAX_BARS = 300


@dataclass
class MarketSnapshot:
    symbol: str
    price: float
    change_pct_24h: float
    funding_rate: float
    ls_ratio: float


@dataclass
class SignalResult:
    symbol: str
    direction: Optional[str]
    entry_price: float
    tp_price: float
    sl_price: float
    confidence: float
    rsi: float
    macd_diff: float
    bb_position: str
    atr_pct: float
    funding_rate: float
    ls_ratio: float
    passed_filter: bool
    filter_reason: str


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
) -> tuple[float, Optional[str]]:
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

    if bb_lower > 0 and price <= bb_lower:
        ls += 0.15
    elif bb_upper > 0 and price >= bb_upper:
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

    if ls >= Config.MIN_CONFIDENCE:
        return ls, "long"
    if ss >= Config.MIN_CONFIDENCE:
        return ss, "short"
    return max(ls, ss), None


def score_confidence_detail(
    rsi_val: float,
    macd_val: float,
    macd_sig: float,
    price: float,
    bb_upper: float,
    bb_lower: float,
    funding_rate: float,
    ls_ratio: float,
    atr_pct: float,
) -> tuple[float, Optional[str], dict]:
    ls = 0.0
    ss = 0.0
    ls_parts: list[str] = []
    ss_parts: list[str] = []

    if rsi_val < 35:
        ls += 0.25
        ls_parts.append("RSI<35:+0.25")
    elif rsi_val > 65:
        ss += 0.25
        ss_parts.append("RSI>65:+0.25")

    if macd_val > macd_sig:
        ls += 0.25
        ls_parts.append("MACD>信号:+0.25")
    else:
        ss += 0.25
        ss_parts.append("MACD<=信号:+0.25")

    if bb_lower > 0 and price <= bb_lower:
        ls += 0.15
        ls_parts.append("布林<=下轨:+0.15")
    elif bb_upper > 0 and price >= bb_upper:
        ss += 0.15
        ss_parts.append("布林>=上轨:+0.15")

    if funding_rate < -0.0003:
        ls += 0.20
        ls_parts.append("资金费率<-0.0003:+0.20")
    elif funding_rate > 0.0003:
        ss += 0.20
        ss_parts.append("资金费率>0.0003:+0.20")

    if ls_ratio < 0.8:
        ls += 0.15
        ls_parts.append("多空比<0.8:+0.15")
    elif ls_ratio > 1.5:
        ss += 0.15
        ss_parts.append("多空比>1.5:+0.15")

    discounted = False
    if atr_pct < Config.ATR_DISCOUNT_THR:
        discounted = True
        ls *= Config.ATR_DISCOUNT
        ss *= Config.ATR_DISCOUNT

    direction: Optional[str] = None
    if ls >= Config.MIN_CONFIDENCE:
        direction = "long"
        conf = ls
    elif ss >= Config.MIN_CONFIDENCE:
        direction = "short"
        conf = ss
    else:
        conf = max(ls, ss)

    detail = {
        "ls": float(ls),
        "ss": float(ss),
        "ls_parts": ls_parts,
        "ss_parts": ss_parts,
        "atr_discounted": discounted,
        "atr_discount_thr": float(Config.ATR_DISCOUNT_THR),
        "atr_discount": float(Config.ATR_DISCOUNT),
        "min_confidence": float(Config.MIN_CONFIDENCE),
    }
    return float(conf), direction, detail


def analyze_with_detail(snapshot: MarketSnapshot, closes: list[float], highs: list[float], lows: list[float]) -> tuple[SignalResult, dict]:
    price = float(snapshot.price)

    rsi_val = calc_rsi_pd(closes)
    macd_val, macd_sig = calc_macd_pd(closes)
    _, bb_upper, bb_lower = calc_bollinger_pd(closes)
    atr_pct = calc_atr_pct_pd(highs, lows, closes)

    if bb_lower > 0 and price <= bb_lower:
        bb_pos = "下轨"
    elif bb_upper > 0 and price >= bb_upper:
        bb_pos = "上轨"
    else:
        bb_pos = "中部"

    confidence, direction, detail = score_confidence_detail(
        rsi_val,
        macd_val,
        macd_sig,
        price,
        bb_upper,
        bb_lower,
        snapshot.funding_rate,
        snapshot.ls_ratio,
        atr_pct,
    )

    if direction == "long":
        tp = round(price * (1.0 + Config.TP_RATIO), 10)
        sl = round(price * (1.0 - Config.SL_RATIO), 10)
    elif direction == "short":
        tp = round(price * (1.0 - Config.TP_RATIO), 10)
        sl = round(price * (1.0 + Config.SL_RATIO), 10)
    else:
        tp = 0.0
        sl = 0.0

    return (
        SignalResult(
            symbol=snapshot.symbol,
            direction=direction,
            entry_price=price,
            tp_price=tp,
            sl_price=sl,
            confidence=float(confidence),
            rsi=float(rsi_val),
            macd_diff=float(macd_val - macd_sig),
            bb_position=bb_pos,
            atr_pct=float(atr_pct),
            funding_rate=float(snapshot.funding_rate),
            ls_ratio=float(snapshot.ls_ratio),
            passed_filter=True,
            filter_reason="",
        ),
        detail,
    )


def _no_signal(snap: MarketSnapshot, reason: str) -> SignalResult:
    return SignalResult(
        symbol=snap.symbol,
        direction=None,
        entry_price=snap.price,
        tp_price=0.0,
        sl_price=0.0,
        confidence=0.0,
        rsi=0.0,
        macd_diff=0.0,
        bb_position="—",
        atr_pct=0.0,
        funding_rate=snap.funding_rate,
        ls_ratio=snap.ls_ratio,
        passed_filter=False,
        filter_reason=reason,
    )


def analyze(snapshot: MarketSnapshot, closes: list[float], highs: list[float], lows: list[float]) -> SignalResult:
    price = float(snapshot.price)

    rsi_val = calc_rsi_pd(closes)
    macd_val, macd_sig = calc_macd_pd(closes)
    _, bb_upper, bb_lower = calc_bollinger_pd(closes)
    atr_pct = calc_atr_pct_pd(highs, lows, closes)

    if bb_lower > 0 and price <= bb_lower:
        bb_pos = "下轨"
    elif bb_upper > 0 and price >= bb_upper:
        bb_pos = "上轨"
    else:
        bb_pos = "中部"

    confidence, direction = score_confidence(
        rsi_val,
        macd_val,
        macd_sig,
        price,
        bb_upper,
        bb_lower,
        snapshot.funding_rate,
        snapshot.ls_ratio,
        atr_pct,
    )

    if direction == "long":
        tp = round(price * (1.0 + Config.TP_RATIO), 10)
        sl = round(price * (1.0 - Config.SL_RATIO), 10)
    elif direction == "short":
        tp = round(price * (1.0 - Config.TP_RATIO), 10)
        sl = round(price * (1.0 + Config.SL_RATIO), 10)
    else:
        tp = 0.0
        sl = 0.0

    return SignalResult(
        symbol=snapshot.symbol,
        direction=direction,
        entry_price=price,
        tp_price=tp,
        sl_price=sl,
        confidence=float(confidence),
        rsi=float(rsi_val),
        macd_diff=float(macd_val - macd_sig),
        bb_position=bb_pos,
        atr_pct=float(atr_pct),
        funding_rate=float(snapshot.funding_rate),
        ls_ratio=float(snapshot.ls_ratio),
        passed_filter=True,
        filter_reason="",
    )


class Strategy:
    def __init__(self, config: dict):
        self.cfg = config or {}
        self.strategy_id = _s(self.cfg.get("strategy_id")).strip()
        self.owner_id = _i(self.cfg.get("owner_id"), 0)
        self.prefix = _s(self.cfg.get("redis_prefix") or os.getenv("REDIS_PREFIX") or "qt").strip() or "qt"
        self.redis_addr = _s(self.cfg.get("redis_addr") or os.getenv("REDIS_ADDR") or "127.0.0.1:6379").strip()
        self.redis_password = _s(self.cfg.get("redis_password") or os.getenv("REDIS_PASSWORD") or "")
        self.redis_db = _i(self.cfg.get("redis_db") if self.cfg.get("redis_db") is not None else os.getenv("REDIS_DB"), 0)
        self.healthcheck = self.cfg.get("healthcheck") or {}

        self.boot_id = f"{int(time.time() * 1000)}-{os.getpid()}"

        self.symbols: list[str] = []
        raw_syms = self.cfg.get("symbols")
        if isinstance(raw_syms, list):
            for s in raw_syms:
                if isinstance(s, str) and s.strip():
                    self.symbols.append(s.strip())
        if not self.symbols:
            sym = _s(self.cfg.get("symbol")).strip()
            if sym:
                self.symbols = [sym]

        self.last_signal_ts: dict[str, float] = {}
        self.last_signal_dir: dict[str, str] = {}
        self.recv_count: dict[str, int] = {s: 0 for s in self.symbols}

        self.closes: dict[str, list[float]] = {s: [] for s in self.symbols}
        self.highs: dict[str, list[float]] = {s: [] for s in self.symbols}
        self.lows: dict[str, list[float]] = {s: [] for s in self.symbols}

        host, port = (self.redis_addr.split(":") + ["6379"])[:2]
        self.sub = MiniRedis(host=host, port=int(port), password=self.redis_password, db=self.redis_db).connect()
        self.pub = MiniRedis(host=host, port=int(port), password=self.redis_password, db=self.redis_db).connect()

        self._load_config()

    def _load_config(self):
        Config.MIN_CONFIDENCE = _f(self.cfg.get("min_confidence"), Config.MIN_CONFIDENCE)
        Config.TP_RATIO = _f(self.cfg.get("tp_ratio"), Config.TP_RATIO)
        Config.SL_RATIO = _f(self.cfg.get("sl_ratio"), Config.SL_RATIO)
        Config.ATR_DISCOUNT_THR = _f(self.cfg.get("atr_discount_thr"), Config.ATR_DISCOUNT_THR)
        Config.ATR_DISCOUNT = _f(self.cfg.get("atr_discount"), Config.ATR_DISCOUNT)
        Config.CHANGE_LOOKBACK_BARS = max(2, _i(self.cfg.get("change_lookback_bars"), Config.CHANGE_LOOKBACK_BARS))
        Config.VOL_LOOKBACK_BARS = max(2, _i(self.cfg.get("vol_lookback_bars"), Config.VOL_LOOKBACK_BARS))
        Config.COOLDOWN_SEC = max(0, _i(self.cfg.get("cooldown_sec"), Config.COOLDOWN_SEC))
        Config.MAX_BARS = max(50, _i(self.cfg.get("max_bars"), Config.MAX_BARS))
        self.trace = bool(self.cfg.get("log_trace") or self.cfg.get("debug"))
        self.log_rx = bool(self.cfg.get("log_rx", True))
        self.log_decisions = bool(self.cfg.get("log_decisions", True))
        self.log_every = max(1, _i(self.cfg.get("log_every"), 60))
        self.log_idle_sec = max(5, _i(self.cfg.get("log_idle_sec"), 30))
        if self.trace:
            self.log_rx = True
            self.log_decisions = True
            self.log_every = 1
            self.log_idle_sec = 5

    def _candle_ch(self):
        return f"{self.prefix}:candle:{self.strategy_id}"

    def _signal_ch(self):
        return f"{self.prefix}:signal:{self.strategy_id}"

    def _state_ch(self):
        return f"{self.prefix}:state:{self.strategy_id}"

    def _log(self, msg: str):
        sys.stdout.write(json.dumps({"type": "log", "data": msg}) + "\n")
        sys.stdout.flush()

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

    def _emit_signal(self, symbol: str, direction: str, entry_price: float, tp: float, sl: float, confidence: float):
        side = "buy" if direction == "long" else "sell"
        amount = _f(self.cfg.get("trade_amount"), 0.01)
        msg = {
            "strategy_id": self.strategy_id,
            "owner_id": self.owner_id,
            "symbol": symbol,
            "action": "open",
            "side": side,
            "amount": float(amount),
            "take_profit": float(tp) if tp else 0.0,
            "stop_loss": float(sl) if sl else 0.0,
            "signal_id": f"{self.strategy_id}:{symbol}:{int(time.time() * 1000)}",
            "generated_at": datetime.now(tz=timezone.utc).isoformat(),
            "confidence": float(confidence),
        }
        self.pub.publish(self._signal_ch(), json.dumps(msg))
        self._log(f"触发开仓信号 sym={symbol} 方向={direction} side={side} 数量={amount} 入场价={entry_price} 止盈={tp} 止损={sl} 置信度={confidence:.4f} 时间={_now()}")

    def _append_bar(self, symbol: str, o: float, h: float, l: float, c: float):
        if symbol not in self.closes:
            return
        self.closes[symbol].append(float(c))
        self.highs[symbol].append(float(h))
        self.lows[symbol].append(float(l))
        if len(self.closes[symbol]) > Config.MAX_BARS:
            self.closes[symbol] = self.closes[symbol][-Config.MAX_BARS :]
            self.highs[symbol] = self.highs[symbol][-Config.MAX_BARS :]
            self.lows[symbol] = self.lows[symbol][-Config.MAX_BARS :]

    def on_market_message(self, msg: dict):
        t = _s(msg.get("type")).strip().lower()
        if t == "history":
            sym = _s(msg.get("symbol")).strip()
            candles = msg.get("candles") or []
            if self.log_rx:
                n = len(candles) if isinstance(candles, list) else 0
                if sym:
                    self._log(f"收到历史K线 sym={sym} 根数={n} 频道={self._candle_ch()} 时间={_now()}")
                else:
                    self._log(f"收到历史K线 根数={n} 频道={self._candle_ch()} 时间={_now()}")
            if isinstance(candles, list):
                for it in candles:
                    if isinstance(it, dict):
                        self.on_market_message(it)
            return

        if t and t != "candle":
            return

        symbol = _s(msg.get("symbol")).strip()
        if not symbol or symbol not in self.closes:
            return

        o = _f(msg.get("open"), 0.0)
        h = _f(msg.get("high"), 0.0)
        l = _f(msg.get("low"), 0.0)
        c = _f(msg.get("close"), 0.0)
        if c <= 0:
            return
        if h <= 0:
            h = c
        if l <= 0:
            l = c

        self._append_bar(symbol, o, h, l, c)
        self.recv_count[symbol] = int(self.recv_count.get(symbol) or 0) + 1
        n_recv = self.recv_count[symbol]
        if self.log_rx and (self.trace or n_recv % self.log_every == 0):
            self._log(f"收到K线 sym={symbol} 序号={n_recv} 收盘={c} 缓存={len(self.closes[symbol])} 时间={_now()}")
        if self.log_rx and n_recv == 1:
            self._log(f"收到首根K线 sym={symbol} 收盘={c} 时间={_now()}")

        closes = self.closes[symbol]
        highs = self.highs[symbol]
        lows = self.lows[symbol]
        if len(closes) < 35:
            if self.log_decisions and (self.trace or n_recv % self.log_every == 0):
                self._log(f"跳过-预热不足 sym={symbol} 当前缓存={len(closes)}/35 时间={_now()}")
            return

        extra = msg.get("extra") if isinstance(msg.get("extra"), dict) else {}
        fr = _f(extra.get("funding_rate"), 0.0)
        lr = _f(extra.get("ls_ratio"), 1.0)
        change_pct = _f(extra.get("change_pct_24h"), 0.0)
        if change_pct == 0.0:
            change_pct = estimate_change_pct(closes, Config.CHANGE_LOOKBACK_BARS)
        vol_pct = estimate_volatility_pct(highs, lows, Config.VOL_LOOKBACK_BARS)
        if abs(change_pct) < abs(vol_pct):
            change_pct = vol_pct

        snap = MarketSnapshot(
            symbol=symbol,
            price=float(c),
            change_pct_24h=float(change_pct),
            funding_rate=float(fr),
            ls_ratio=float(lr),
        )
        r, sc = analyze_with_detail(snap, closes, highs, lows)
        tick_log = self.log_decisions and (self.trace or n_recv % self.log_every == 0)
        if tick_log:
            self._log(
                f"评估 sym={symbol} 价格={r.entry_price} 方向={r.direction} 置信度={r.confidence:.3f} RSI={r.rsi:.1f} MACD差={r.macd_diff:+.6f} 布林位置={r.bb_position} ATR%={r.atr_pct:.2f} 资金费率={r.funding_rate:+.5f} 多空比={r.ls_ratio:.2f} 时间={_now()}"
            )
            self._log(
                f"评分明细 sym={symbol} 多头分={sc['ls']:.3f} 空头分={sc['ss']:.3f} 最低置信度={sc['min_confidence']:.3f} 低波动折扣={sc['atr_discounted']} 多头加分项={','.join(sc['ls_parts'])} 空头加分项={','.join(sc['ss_parts'])} 时间={_now()}"
            )

        if not r.passed_filter:
            if tick_log:
                self._log(f"跳过-过滤未通过 sym={symbol} 原因={r.filter_reason} 价格={r.entry_price} 时间={_now()}")
            return
        if r.direction is None:
            if tick_log:
                self._log(
                    f"未触发信号 sym={symbol} 置信度={r.confidence:.3f} 多头分={sc['ls']:.3f} 空头分={sc['ss']:.3f} 最低置信度={sc['min_confidence']:.3f} 时间={_now()}"
                )
            return

        now = time.time()
        last_ts = float(self.last_signal_ts.get(symbol) or 0.0)
        if Config.COOLDOWN_SEC > 0 and now-last_ts < float(Config.COOLDOWN_SEC):
            if tick_log:
                self._log(f"跳过-冷却中 sym={symbol} 方向={r.direction} 剩余={int(Config.COOLDOWN_SEC-(now-last_ts))}s 时间={_now()}")
            return

        last_dir = _s(self.last_signal_dir.get(symbol)).strip().lower()
        if last_dir and last_dir == _s(r.direction).strip().lower():
            if tick_log:
                self._log(f"跳过-同向已发过 sym={symbol} 方向={r.direction} 时间={_now()}")
            return

        self.last_signal_ts[symbol] = now
        self.last_signal_dir[symbol] = _s(r.direction).strip().lower()
        self._emit_signal(symbol, _s(r.direction).strip().lower(), r.entry_price, r.tp_price, r.sl_price, r.confidence)

    def run(self):
        if not self.strategy_id:
            raise RuntimeError("missing strategy_id")
        self.sub.subscribe(self._candle_ch())
        self.pub.publish(self._state_ch(), json.dumps({"type": "ready", "strategy_id": self.strategy_id, "boot_id": self.boot_id, "created_at": _now()}))
        t = threading.Thread(target=self._heartbeat_loop, daemon=True)
        t.start()
        self._log(
            f"START strategy_id={self.strategy_id} symbols={','.join(self.symbols)} candle_ch={self._candle_ch()} signal_ch={self._signal_ch()} state_ch={self._state_ch()} log_every={self.log_every} ts={_now()}"
        )
        last_idle = time.time()
        while True:
            item = self.sub.read_pubsub_message()
            if not item:
                if self.log_rx and time.time() - last_idle >= float(self.log_idle_sec):
                    last_idle = time.time()
                    self._log(f"IDLE waiting candle ch={self._candle_ch()} symbols={len(self.symbols)} ts={_now()}")
                continue
            payload = item.get("data")
            if not payload:
                continue
            try:
                msg = json.loads(payload)
            except Exception:
                continue
            if isinstance(msg, dict):
                self.on_market_message(msg)


if __name__ == "__main__":
    cfg_str = sys.argv[1] if len(sys.argv) > 1 else "{}"
    cfg = json.loads(cfg_str)
    Strategy(cfg).run()
