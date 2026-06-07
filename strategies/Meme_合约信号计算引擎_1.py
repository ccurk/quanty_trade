"""
Meme 合约信号计算引擎 — 系统可用版（Redis 模式）
职责：只做指标计算 + 置信度评分 + 信号生成
无任何网络请求、无交易所接口调用
所有行情与历史数据均从 Go 后端经 Redis PubSub 推送

优化记录：
- 增加 EMA20/EMA60 趋势方向过滤，避免逆势开仓
- 增加成交量确认（当前量 > 近期均量才触发）
- ATR 动态止盈止损（TP=2×ATR，SL=1×ATR），替代固定比例
- 高波动保护（ATR 过高时降低信号强度）
- 调整评分权重：趋势一致性 +0.20，MACD 降至 +0.15
- 冷却默认 300 秒，避免频繁开仓
"""

import json
import os
import socket
import sys
import threading
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Optional

class MiniRedis:
    def __init__(self, host="127.0.0.1", port=6379, password="", db=0, timeout=30):
        self.host = host
        self.port = int(port)
        self.password = password or ""
        self.db = int(db or 0)
        self.timeout = timeout
        self.sock = None
        self.buf = b""

    def connect(self):
        self.sock = socket.create_connection((self.host, self.port), timeout=self.timeout if self.timeout else None)
        if self.timeout:
            self.sock.settimeout(self.timeout)
        if self.password:
            self.execute("AUTH", self.password)
        if self.db:
            self.execute("SELECT", str(self.db))
        return self

    def close(self):
        try:
            if self.sock:
                self.sock.close()
        finally:
            self.sock = None
            self.buf = b""

    def _encode(self, *parts):
        out = [f"*{len(parts)}\r\n".encode("utf-8")]
        for p in parts:
            if p is None:
                p = ""
            if not isinstance(p, (bytes, bytearray)):
                p = str(p).encode("utf-8")
            out.append(f"${len(p)}\r\n".encode("utf-8"))
            out.append(p)
            out.append(b"\r\n")
        return b"".join(out)

    def _read_exact(self, n):
        while len(self.buf) < n:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise ConnectionError("redis connection closed")
            self.buf += chunk
        out, self.buf = self.buf[:n], self.buf[n:]
        return out

    def _read_line(self):
        while b"\r\n" not in self.buf:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise ConnectionError("redis connection closed")
            self.buf += chunk
        i = self.buf.index(b"\r\n")
        line, self.buf = self.buf[:i], self.buf[i + 2 :]
        return line

    def _read_resp(self):
        prefix = self._read_exact(1)
        if prefix == b"+":
            return self._read_line().decode("utf-8", errors="replace")
        if prefix == b"-":
            raise RuntimeError(self._read_line().decode("utf-8", errors="replace"))
        if prefix == b":":
            return int(self._read_line())
        if prefix == b"$":
            n = int(self._read_line())
            if n == -1:
                return None
            data = self._read_exact(n)
            _ = self._read_exact(2)
            return data.decode("utf-8", errors="replace")
        if prefix == b"*":
            n = int(self._read_line())
            if n == -1:
                return None
            return [self._read_resp() for _ in range(n)]
        raise RuntimeError(f"unknown RESP prefix: {prefix!r}")

    def execute(self, *args):
        if not self.sock:
            self.connect()
        self.sock.sendall(self._encode(*args))
        return self._read_resp()

    def publish(self, channel, payload):
        return self.execute("PUBLISH", channel, payload)

    def subscribe(self, channel):
        return self.execute("SUBSCRIBE", channel)

    def read_pubsub_message(self):
        try:
            msg = self._read_resp()
        except (TimeoutError, socket.timeout):
            return None
        if not isinstance(msg, list) or len(msg) < 3:
            return None
        if msg[0] != "message":
            return None
        return {"channel": msg[1], "data": msg[2]}

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


def _parse_ratio(v, d=0.0):
    x = _f(v, d)
    if x > 1:
        x = x / 100.0
    return max(x, 0.0)


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


def calc_atr_abs(highs: list[float], lows: list[float], closes: list[float], period: int = 14) -> float:
    """返回 ATR 绝对值（用于动态止盈止损计算）"""
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
    return float(atr) if atr is not None else 0.0


def calc_ema_trend(closes: list[float], fast: int = 20, slow: int = 60) -> Optional[str]:
    """
    计算 EMA 趋势方向。
    返回 'up'（EMA fast > EMA slow）、'down'（EMA fast < EMA slow）或 None（数据不足）。
    """
    if len(closes) < slow:
        return None
    fast_series = ema_series(closes, fast)
    slow_series = ema_series(closes, slow)
    if not fast_series or not slow_series:
        return None
    ema_fast = fast_series[-1]
    ema_slow = slow_series[-1]
    if ema_fast > ema_slow:
        return "up"
    elif ema_fast < ema_slow:
        return "down"
    return None


def calc_volume_ratio(volumes: list[float], period: int = 20) -> float:
    """
    计算当前成交量与近期均量的比值。
    > 1.0 表示放量，< 1.0 表示缩量。
    数据不足时返回 1.0（中性）。
    """
    if len(volumes) < period + 1:
        return 1.0
    avg = sma(volumes[:-1], period)
    if avg is None or avg <= 0:
        return 1.0
    return float(volumes[-1]) / float(avg)


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
    MIN_CONFIDENCE = 0.35          # 提高最低置信度，减少噪音信号
    TP_RATIO = 0.06                # 固定止盈兜底（ATR 不足时使用）
    SL_RATIO = 0.03                # 固定止损兜底（ATR 不足时使用）
    ATR_TP_MULT = 2.0              # 动态止盈 = ATR × 倍数
    ATR_SL_MULT = 1.0              # 动态止损 = ATR × 倍数
    ATR_DISCOUNT_THR = 1.5         # ATR% 低于此值：低波动折扣
    ATR_DISCOUNT = 0.7             # 低波动折扣系数
    ATR_HIGH_THR = 5.0             # ATR% 高于此值：高波动保护
    ATR_HIGH_DISCOUNT = 0.6        # 高波动折扣系数
    VOLUME_RATIO_MIN = 1.2         # 成交量确认：当前量需 > 均量 × 此倍数
    EMA_FAST = 20                  # EMA 趋势快线周期
    EMA_SLOW = 60                  # EMA 趋势慢线周期
    CHANGE_LOOKBACK_BARS = 200
    VOL_LOOKBACK_BARS = 200
    COOLDOWN_SEC = 300             # 默认冷却 300 秒，避免频繁开仓
    MAX_BARS = 400                 # 增加缓存上限，保证 EMA60 计算充分
    WARMUP_BARS = 80               # 提高预热要求，保证 EMA60 稳定


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
    ema_trend: str          # 'up' / 'down' / 'flat'
    volume_ratio: float     # 当前量 / 近期均量


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
    ema_trend: Optional[str],
    volume_ratio: float,
) -> tuple[float, Optional[str]]:
    ls = 0.0
    ss = 0.0

    # RSI 超卖/超买（权重 0.20）
    if rsi_val < 35:
        ls += 0.20
    elif rsi_val > 65:
        ss += 0.20

    # MACD 方向（权重降至 0.15，避免单因子主导）
    if macd_val > macd_sig:
        ls += 0.15
    else:
        ss += 0.15

    # 布林带位置（权重 0.15）
    if bb_lower > 0 and price <= bb_lower:
        ls += 0.15
    elif bb_upper > 0 and price >= bb_upper:
        ss += 0.15

    # 资金费率（权重 0.20）
    if funding_rate < -0.0003:
        ls += 0.20
    elif funding_rate > 0.0003:
        ss += 0.20

    # 多空比（权重 0.15）
    if ls_ratio < 0.8:
        ls += 0.15
    elif ls_ratio > 1.5:
        ss += 0.15

    # EMA 趋势一致性（权重 0.20，新增）
    if ema_trend == "up":
        ls += 0.20
    elif ema_trend == "down":
        ss += 0.20

    # 成交量确认：放量加分（权重 0.10，新增）
    if volume_ratio >= Config.VOLUME_RATIO_MIN:
        # 放量时，多空方向各自加分（哪边分高就加哪边）
        if ls >= ss:
            ls += 0.10
        else:
            ss += 0.10

    # 低波动折扣
    if atr_pct < Config.ATR_DISCOUNT_THR:
        ls *= Config.ATR_DISCOUNT
        ss *= Config.ATR_DISCOUNT

    # 高波动保护（新增）
    if atr_pct > Config.ATR_HIGH_THR:
        ls *= Config.ATR_HIGH_DISCOUNT
        ss *= Config.ATR_HIGH_DISCOUNT

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
    ema_trend: Optional[str],
    volume_ratio: float,
) -> tuple[float, Optional[str], dict]:
    ls = 0.0
    ss = 0.0
    ls_parts: list[str] = []
    ss_parts: list[str] = []

    # RSI（权重 0.20）
    if rsi_val < 35:
        ls += 0.20
        ls_parts.append("RSI<35:+0.20")
    elif rsi_val > 65:
        ss += 0.20
        ss_parts.append("RSI>65:+0.20")

    # MACD（权重 0.15）
    if macd_val > macd_sig:
        ls += 0.15
        ls_parts.append("MACD>信号:+0.15")
    else:
        ss += 0.15
        ss_parts.append("MACD<=信号:+0.15")

    # 布林带（权重 0.15）
    if bb_lower > 0 and price <= bb_lower:
        ls += 0.15
        ls_parts.append("布林<=下轨:+0.15")
    elif bb_upper > 0 and price >= bb_upper:
        ss += 0.15
        ss_parts.append("布林>=上轨:+0.15")

    # 资金费率（权重 0.20）
    if funding_rate < -0.0003:
        ls += 0.20
        ls_parts.append("资金费率<-0.0003:+0.20")
    elif funding_rate > 0.0003:
        ss += 0.20
        ss_parts.append("资金费率>0.0003:+0.20")

    # 多空比（权重 0.15）
    if ls_ratio < 0.8:
        ls += 0.15
        ls_parts.append("多空比<0.8:+0.15")
    elif ls_ratio > 1.5:
        ss += 0.15
        ss_parts.append("多空比>1.5:+0.15")

    # EMA 趋势一致性（权重 0.20，新增）
    if ema_trend == "up":
        ls += 0.20
        ls_parts.append("EMA趋势向上:+0.20")
    elif ema_trend == "down":
        ss += 0.20
        ss_parts.append("EMA趋势向下:+0.20")

    # 成交量确认（权重 0.10，新增）
    vol_bonus_applied = False
    if volume_ratio >= Config.VOLUME_RATIO_MIN:
        vol_bonus_applied = True
        if ls >= ss:
            ls += 0.10
            ls_parts.append(f"放量确认(×{volume_ratio:.2f}):+0.10")
        else:
            ss += 0.10
            ss_parts.append(f"放量确认(×{volume_ratio:.2f}):+0.10")

    # 低波动折扣
    low_vol_discounted = False
    if atr_pct < Config.ATR_DISCOUNT_THR:
        low_vol_discounted = True
        ls *= Config.ATR_DISCOUNT
        ss *= Config.ATR_DISCOUNT

    # 高波动保护（新增）
    high_vol_discounted = False
    if atr_pct > Config.ATR_HIGH_THR:
        high_vol_discounted = True
        ls *= Config.ATR_HIGH_DISCOUNT
        ss *= Config.ATR_HIGH_DISCOUNT

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
        "atr_discounted": low_vol_discounted,
        "atr_high_discounted": high_vol_discounted,
        "atr_discount_thr": float(Config.ATR_DISCOUNT_THR),
        "atr_high_thr": float(Config.ATR_HIGH_THR),
        "atr_discount": float(Config.ATR_DISCOUNT),
        "atr_high_discount": float(Config.ATR_HIGH_DISCOUNT),
        "ema_trend": _s(ema_trend, "flat"),
        "volume_ratio": float(volume_ratio),
        "volume_ratio_min": float(Config.VOLUME_RATIO_MIN),
        "vol_bonus_applied": vol_bonus_applied,
        "min_confidence": float(Config.MIN_CONFIDENCE),
    }
    return float(conf), direction, detail


def empty_score_detail(reason: str = "") -> dict:
    return {
        "ls": 0.0,
        "ss": 0.0,
        "ls_parts": [],
        "ss_parts": [],
        "atr_discounted": False,
        "atr_high_discounted": False,
        "atr_discount_thr": float(Config.ATR_DISCOUNT_THR),
        "atr_high_thr": float(Config.ATR_HIGH_THR),
        "atr_discount": float(Config.ATR_DISCOUNT),
        "atr_high_discount": float(Config.ATR_HIGH_DISCOUNT),
        "ema_trend": "flat",
        "volume_ratio": 1.0,
        "volume_ratio_min": float(Config.VOLUME_RATIO_MIN),
        "vol_bonus_applied": False,
        "min_confidence": float(Config.MIN_CONFIDENCE),
        "reason": _s(reason),
    }


def _calc_tp_sl(price: float, direction: str, atr_abs: float) -> tuple[float, float]:
    """
    动态止盈止损：优先使用 ATR 倍数，ATR 为 0 时回退到固定比例。
    多头：TP = price + ATR×2，SL = price - ATR×1
    空头：TP = price - ATR×2，SL = price + ATR×1
    """
    if atr_abs > 0:
        tp_delta = atr_abs * Config.ATR_TP_MULT
        sl_delta = atr_abs * Config.ATR_SL_MULT
    else:
        tp_delta = price * Config.TP_RATIO
        sl_delta = price * Config.SL_RATIO

    if direction == "long":
        tp = round(price + tp_delta, 10)
        sl = round(price - sl_delta, 10)
    else:
        tp = round(price - tp_delta, 10)
        sl = round(price + sl_delta, 10)
    return tp, sl


def analyze_with_detail(
    snapshot: MarketSnapshot,
    closes: list[float],
    highs: list[float],
    lows: list[float],
    volumes: Optional[list[float]] = None,
) -> tuple[SignalResult, dict]:
    price = float(snapshot.price)
    if price <= 0:
        return _no_signal(snapshot, "价格无效"), empty_score_detail("invalid_price")
    if price > Config.MAX_PRICE:
        return _no_signal(snapshot, f"币价${price:.8f}超上限"), empty_score_detail("max_price")
    if get_decimal_precision(price) < Config.MIN_PRECISION:
        return _no_signal(snapshot, f"精度不足{Config.MIN_PRECISION}位"), empty_score_detail("min_precision")
    if abs(float(snapshot.change_pct_24h)) < Config.MIN_VOLATILITY:
        return _no_signal(snapshot, f"波动不足{Config.MIN_VOLATILITY:.1f}%"), empty_score_detail("min_volatility")

    rsi_val = calc_rsi_pd(closes)
    macd_val, macd_sig = calc_macd_pd(closes)
    _, bb_upper, bb_lower = calc_bollinger_pd(closes)
    atr_pct = calc_atr_pct_pd(highs, lows, closes)
    atr_abs = calc_atr_abs(highs, lows, closes)
    ema_trend = calc_ema_trend(closes, Config.EMA_FAST, Config.EMA_SLOW)
    volume_ratio = calc_volume_ratio(volumes, 20) if volumes else 1.0

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
        ema_trend,
        volume_ratio,
    )

    tp, sl = (0.0, 0.0)
    if direction is not None:
        tp, sl = _calc_tp_sl(price, direction, atr_abs)

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
            ema_trend=_s(ema_trend, "flat"),
            volume_ratio=float(volume_ratio),
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
        ema_trend="flat",
        volume_ratio=1.0,
    )


def analyze(
    snapshot: MarketSnapshot,
    closes: list[float],
    highs: list[float],
    lows: list[float],
    volumes: Optional[list[float]] = None,
) -> SignalResult:
    price = float(snapshot.price)
    if price <= 0:
        return _no_signal(snapshot, "价格无效")
    if price > Config.MAX_PRICE:
        return _no_signal(snapshot, f"币价${price:.8f}超上限")
    if get_decimal_precision(price) < Config.MIN_PRECISION:
        return _no_signal(snapshot, f"精度不足{Config.MIN_PRECISION}位")
    if abs(float(snapshot.change_pct_24h)) < Config.MIN_VOLATILITY:
        return _no_signal(snapshot, f"波动不足{Config.MIN_VOLATILITY:.1f}%")

    rsi_val = calc_rsi_pd(closes)
    macd_val, macd_sig = calc_macd_pd(closes)
    _, bb_upper, bb_lower = calc_bollinger_pd(closes)
    atr_pct = calc_atr_pct_pd(highs, lows, closes)
    atr_abs = calc_atr_abs(highs, lows, closes)
    ema_trend = calc_ema_trend(closes, Config.EMA_FAST, Config.EMA_SLOW)
    volume_ratio = calc_volume_ratio(volumes, 20) if volumes else 1.0

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
        ema_trend,
        volume_ratio,
    )

    tp, sl = (0.0, 0.0)
    if direction is not None:
        tp, sl = _calc_tp_sl(price, direction, atr_abs)

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
        ema_trend=_s(ema_trend, "flat"),
        volume_ratio=float(volume_ratio),
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
        # 已处理的最近一根 K 线时间戳（ISO 字符串或 ms），用于在 history 回灌
        # 与 live candle 之间去重，防止同一根 candle 被 _append_bar 重复入队。
        self.last_bar_ts: dict[str, str] = {s: "" for s in self.symbols}

        self.closes: dict[str, list[float]] = {s: [] for s in self.symbols}
        self.highs: dict[str, list[float]] = {s: [] for s in self.symbols}
        self.lows: dict[str, list[float]] = {s: [] for s in self.symbols}
        self.volumes: dict[str, list[float]] = {s: [] for s in self.symbols}

        host, port = (self.redis_addr.split(":") + ["6379"])[:2]
        self.sub = MiniRedis(host=host, port=int(port), password=self.redis_password, db=self.redis_db).connect()
        self.pub = MiniRedis(host=host, port=int(port), password=self.redis_password, db=self.redis_db).connect()

        self._load_config()

    def _load_config(self):
        Config.MIN_CONFIDENCE = _parse_ratio(self.cfg.get("min_confidence"), Config.MIN_CONFIDENCE)
        Config.TP_RATIO = _parse_ratio(self.cfg.get("tp_ratio", self.cfg.get("take_profit_pct")), Config.TP_RATIO)
        Config.SL_RATIO = _parse_ratio(self.cfg.get("sl_ratio", self.cfg.get("stop_loss_pct")), Config.SL_RATIO)
        Config.ATR_TP_MULT = _f(self.cfg.get("atr_tp_mult"), Config.ATR_TP_MULT)
        Config.ATR_SL_MULT = _f(self.cfg.get("atr_sl_mult"), Config.ATR_SL_MULT)
        Config.ATR_DISCOUNT_THR = _f(self.cfg.get("atr_discount_thr"), Config.ATR_DISCOUNT_THR)
        Config.ATR_DISCOUNT = _f(self.cfg.get("atr_discount"), Config.ATR_DISCOUNT)
        Config.ATR_HIGH_THR = _f(self.cfg.get("atr_high_thr"), Config.ATR_HIGH_THR)
        Config.ATR_HIGH_DISCOUNT = _f(self.cfg.get("atr_high_discount"), Config.ATR_HIGH_DISCOUNT)
        Config.VOLUME_RATIO_MIN = _f(self.cfg.get("volume_ratio_min"), Config.VOLUME_RATIO_MIN)
        Config.EMA_FAST = max(5, _i(self.cfg.get("ema_fast"), Config.EMA_FAST))
        Config.EMA_SLOW = max(10, _i(self.cfg.get("ema_slow"), Config.EMA_SLOW))
        Config.CHANGE_LOOKBACK_BARS = max(2, _i(self.cfg.get("change_lookback_bars"), Config.CHANGE_LOOKBACK_BARS))
        Config.VOL_LOOKBACK_BARS = max(2, _i(self.cfg.get("vol_lookback_bars"), Config.VOL_LOOKBACK_BARS))
        Config.COOLDOWN_SEC = max(0, _i(self.cfg.get("cooldown_sec"), Config.COOLDOWN_SEC))
        Config.MAX_BARS = max(100, _i(self.cfg.get("max_bars"), Config.MAX_BARS))
        Config.WARMUP_BARS = max(35, _i(self.cfg.get("warmup_bars"), Config.WARMUP_BARS))
        Config.MAX_PRICE = max(0.0, _f(self.cfg.get("max_price"), Config.MAX_PRICE))
        Config.MIN_PRECISION = max(0, _i(self.cfg.get("min_precision"), Config.MIN_PRECISION))
        Config.MIN_VOLATILITY = max(0.0, _f(self.cfg.get("min_volatility"), Config.MIN_VOLATILITY))
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

    def _recv_summary(self, limit: int = 5) -> str:
        pairs = sorted(self.recv_count.items(), key=lambda kv: (-int(kv[1]), kv[0]))
        if not pairs:
            return "-"
        top = [f"{sym}:{cnt}" for sym, cnt in pairs[: max(1, limit)]]
        active = sum(1 for _, cnt in pairs if int(cnt) > 0)
        return f"active={active}/{len(pairs)} top={';'.join(top)}"

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
        amount = _f(self.cfg.get("trade_amount", self.cfg.get("base_trade_usdt")), 0.01)
        # signal_id：用 boot_id 前 8 位 + 纳秒，避免同毫秒内重复触发时 ID 碰撞。
        # 原来用 int(time.time()*1000) 在 burst 时多条信号会撞同一 ID。
        boot_short = (self.boot_id or "").split("-", 1)[0][-8:]
        msg = {
            "strategy_id": self.strategy_id,
            "owner_id": self.owner_id,
            "symbol": symbol,
            "action": "open",
            "side": side,
            "amount": float(amount),
            "take_profit": float(tp) if tp else 0.0,
            "stop_loss": float(sl) if sl else 0.0,
            "signal_id": f"{self.strategy_id}:{symbol}:{boot_short}:{time.time_ns()}",
            "generated_at": datetime.now(tz=timezone.utc).isoformat(),
            "confidence": float(confidence),
        }
        # publish 必须包 try/except：Redis 抖一下不能让整个 Python 进程崩。
        # 心跳那边（_heartbeat_loop）已经包了，信号这条之前是裸 publish。
        try:
            self.pub.publish(self._signal_ch(), json.dumps(msg))
        except Exception as e:
            self._log(f"[ERROR] 信号 publish 失败 sym={symbol} side={side} err={e!r} 时间={_now()}")
            return
        self._log(f"触发开仓信号 sym={symbol} 方向={direction} side={side} 数量={amount} 入场价={entry_price} 止盈={tp} 止损={sl} 置信度={confidence:.4f} 时间={_now()}")

    def _append_bar(self, symbol: str, h: float, l: float, c: float, v: float = 0.0):
        if symbol not in self.closes:
            return
        self.closes[symbol].append(float(c))
        self.highs[symbol].append(float(h))
        self.lows[symbol].append(float(l))
        self.volumes[symbol].append(float(v))
        if len(self.closes[symbol]) > Config.MAX_BARS:
            self.closes[symbol] = self.closes[symbol][-Config.MAX_BARS :]
            self.highs[symbol] = self.highs[symbol][-Config.MAX_BARS :]
            self.lows[symbol] = self.lows[symbol][-Config.MAX_BARS :]
            self.volumes[symbol] = self.volumes[symbol][-Config.MAX_BARS :]

    def on_market_message(self, msg: dict, from_history: bool = False):
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
                        self.on_market_message(it, from_history=True)
            return

        if t and t != "candle":
            return

        symbol = _s(msg.get("symbol")).strip()
        if not symbol or symbol not in self.closes:
            return

        h = _f(msg.get("high"), 0.0)
        l = _f(msg.get("low"), 0.0)
        c = _f(msg.get("close"), 0.0)
        v = _f(msg.get("volume"), 0.0)
        if c <= 0:
            return
        if h <= 0:
            h = c
        if l <= 0:
            l = c

        # 按 timestamp 去重：history 回灌的最后一根 K 线 与 紧随其后的 live
        # candle 可能是同一根，Go 侧的 onExchangeCandle 只对 live 流内部去重，
        # 跨 history/live 边界不感知。直接拿 raw timestamp 做字符串比较即可。
        ts_raw = msg.get("timestamp")
        ts_key = str(ts_raw) if ts_raw not in (None, "", 0) else ""
        if ts_key:
            if self.last_bar_ts.get(symbol, "") == ts_key:
                if self.log_rx and self.trace:
                    self._log(f"跳过-重复K线 sym={symbol} ts={ts_raw} 时间={_now()}")
                return
            self.last_bar_ts[symbol] = ts_key

        self._append_bar(symbol, h, l, c, v)
        self.recv_count[symbol] = int(self.recv_count.get(symbol) or 0) + 1
        n_recv = self.recv_count[symbol]
        if self.log_rx and (self.trace or n_recv % self.log_every == 0):
            src = "history" if from_history else "live"
            self._log(f"收到K线 sym={symbol} 来源={src} 序号={n_recv} 收盘={c} 量={v} 缓存={len(self.closes[symbol])} 时间={_now()}")
        if self.log_rx and n_recv == 1:
            src = "history" if from_history else "live"
            self._log(f"收到首根K线 sym={symbol} 来源={src} 收盘={c} 时间={_now()}")

        closes = self.closes[symbol]
        highs = self.highs[symbol]
        lows = self.lows[symbol]
        volumes = self.volumes[symbol]
        if len(closes) < Config.WARMUP_BARS:
            if self.log_decisions and (self.trace or n_recv % self.log_every == 0):
                self._log(f"跳过-预热不足 sym={symbol} 当前缓存={len(closes)}/{Config.WARMUP_BARS} 时间={_now()}")
            return

        if from_history:
            if self.log_decisions and (self.trace or n_recv % self.log_every == 0):
                self._log(f"跳过-历史回放预热 sym={symbol} 缓存={len(closes)} 时间={_now()}")
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
        r, sc = analyze_with_detail(snap, closes, highs, lows, volumes)
        tick_log = self.log_decisions and (self.trace or n_recv % self.log_every == 0)
        if tick_log:
            ls = _f(sc.get("ls"), 0.0)
            ss = _f(sc.get("ss"), 0.0)
            min_conf = _f(sc.get("min_confidence"), Config.MIN_CONFIDENCE)
            atr_discounted = bool(sc.get("atr_discounted"))
            atr_high_discounted = bool(sc.get("atr_high_discounted"))
            ema_trend = _s(sc.get("ema_trend"), "flat")
            vol_ratio = _f(sc.get("volume_ratio"), 1.0)
            ls_parts = sc.get("ls_parts") if isinstance(sc.get("ls_parts"), list) else []
            ss_parts = sc.get("ss_parts") if isinstance(sc.get("ss_parts"), list) else []
            self._log(
                f"评估 sym={symbol} 价格={r.entry_price} 方向={r.direction} 置信度={r.confidence:.3f} "
                f"RSI={r.rsi:.1f} MACD差={r.macd_diff:+.6f} 布林位置={r.bb_position} "
                f"ATR%={r.atr_pct:.2f} EMA趋势={ema_trend} 量比={vol_ratio:.2f} "
                f"资金费率={r.funding_rate:+.5f} 多空比={r.ls_ratio:.2f} 时间={_now()}"
            )
            self._log(
                f"评分明细 sym={symbol} 多头分={ls:.3f} 空头分={ss:.3f} 最低置信度={min_conf:.3f} "
                f"低波动折扣={atr_discounted} 高波动折扣={atr_high_discounted} "
                f"多头加分项={','.join(ls_parts)} 空头加分项={','.join(ss_parts)} 时间={_now()}"
            )

        if not r.passed_filter:
            if tick_log:
                self._log(f"跳过-过滤未通过 sym={symbol} 原因={r.filter_reason} 价格={r.entry_price} 时间={_now()}")
            return
        if r.direction is None:
            if tick_log:
                self._log(
                    f"未触发信号 sym={symbol} 置信度={r.confidence:.3f} 多头分={_f(sc.get('ls'), 0.0):.3f} "
                    f"空头分={_f(sc.get('ss'), 0.0):.3f} 最低置信度={_f(sc.get('min_confidence'), Config.MIN_CONFIDENCE):.3f} 时间={_now()}"
                )
            return

        now = time.time()
        last_ts = float(self.last_signal_ts.get(symbol) or 0.0)
        last_dir = _s(self.last_signal_dir.get(symbol)).strip().lower()
        if Config.COOLDOWN_SEC > 0 and last_ts > 0 and now - last_ts < float(Config.COOLDOWN_SEC):
            remaining = max(0.0, float(Config.COOLDOWN_SEC) - (now - last_ts))
            if self.log_decisions:
                self._log(
                    f"跳过-冷却中 sym={symbol} 上次方向={last_dir or '-'} 本次方向={_s(r.direction).strip().lower()} remaining={remaining:.1f}s 时间={_now()}"
                )
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
            f"START strategy_id={self.strategy_id} symbols={','.join(self.symbols)} "
            f"candle_ch={self._candle_ch()} signal_ch={self._signal_ch()} state_ch={self._state_ch()} "
            f"log_every={self.log_every} ema_fast={Config.EMA_FAST} ema_slow={Config.EMA_SLOW} "
            f"atr_tp_mult={Config.ATR_TP_MULT} atr_sl_mult={Config.ATR_SL_MULT} "
            f"cooldown={Config.COOLDOWN_SEC}s min_confidence={Config.MIN_CONFIDENCE} ts={_now()}"
        )
        last_idle = time.time()
        while True:
            item = self.sub.read_pubsub_message()
            if not item:
                if self.log_rx and time.time() - last_idle >= float(self.log_idle_sec):
                    last_idle = time.time()
                    self._log(f"IDLE waiting candle ch={self._candle_ch()} symbols={len(self.symbols)} recv={self._recv_summary()} ts={_now()}")
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
