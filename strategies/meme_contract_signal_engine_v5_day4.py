import json
import math
import os
import sys
import threading
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Optional

try:
    from mini_redis import MiniRedis
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

    OBV_MA_PERIOD = 20
    OBV_TREND_BOOST = 0.10
    OBV_TREND_PENALTY = 0.10
    OBV_DIV_BOOST = 0.12
    OBV_DIV_PENALTY = 0.12
    TF_ALIGN_BOOST = 0.15
    TF_CONFLICT_PENALTY = 0.20
    TF_DIV_LOOKBACK = 20

    MAX_BARS = 720
    WARMUP_BARS = 180
    HIGHER_TF_GROUP = 15

    BASE_TRADE_USDT = 10.0
    TOTAL_CAPITAL = 200.0
    KELLY_HALF = True
    TRAIL_ACTIVATE_PCT = 0.025
    TRAIL_RETRACE_PCT = 0.015
    SIGNAL_COOLDOWN_SEC = 600


@dataclass
class KellyParams:
    win_rate: float = 0.50
    avg_win_pct: float = 0.06
    avg_loss_pct: float = 0.03
    max_fraction: float = 0.25
    min_fraction: float = 0.05


@dataclass
class PositionSizing:
    kelly_fraction: float
    used_fraction: float
    usdt_amount: float
    rationale: str


@dataclass
class TrailingStopState:
    direction: str
    entry_price: float
    trail_pct: float
    highest_price: float
    lowest_price: float
    current_stop: float
    activated: bool
    activate_pct: float


@dataclass
class SignalResult:
    symbol: str
    direction: Optional[str]
    entry_price: float
    tp_price: float
    sl_price: float
    confidence: float
    position_sizing: PositionSizing
    trailing_stop: TrailingStopState
    passed_filter: bool
    filter_reason: str
    rsi: float = 0.0
    macd_diff: float = 0.0
    bb_position: str = "—"
    atr_pct: float = 0.0
    funding_rate: float = 0.0
    ls_ratio: float = 1.0
    vol_ratio: float = 0.0
    ema_trend: str = "flat"
    vol_boost: bool = False
    trend_align: bool = False
    cvd_trend: str = "flat"
    nearest_support: float = 0.0
    nearest_resistance: float = 0.0
    dist_to_support: float = 0.0
    dist_to_resistance: float = 0.0
    sl_dynamic: bool = False
    obv_trend: str = "flat"
    obv_divergence: str = "none"
    tf4h_direction: Optional[str] = None
    tf_aligned: bool = False
    tf_boost: float = 0.0


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


def _now_iso():
    return datetime.now(timezone.utc).isoformat()


def _parse_ratio(v, d):
    x = _f(v, d)
    if x > 1:
        x = x / 100.0
    return max(x, 0.0)


def _sma(xs, n):
    if n <= 0 or len(xs) < n:
        return None
    return sum(xs[-n:]) / float(n)


def _ema(xs, n):
    if n <= 0 or not xs:
        return None
    k = 2.0 / float(n + 1)
    v = float(xs[0])
    for x in xs[1:]:
        v = float(x) * k + v * (1.0 - k)
    return v


def _stddev(xs, n):
    if n <= 1 or len(xs) < n:
        return None
    w = xs[-n:]
    m = sum(w) / float(n)
    var = sum((x - m) ** 2 for x in w) / float(n - 1)
    return math.sqrt(var)


def get_decimal_precision(price: float) -> int:
    s = f"{price:.12f}".rstrip("0").rstrip(".")
    if "." not in s:
        return 0
    return len(s.split(".", 1)[1])


def calc_rsi(closes, period=14):
    if len(closes) < period + 1:
        return 50.0
    gains = []
    losses = []
    for i in range(-period, 0):
        d = float(closes[i]) - float(closes[i - 1])
        if d >= 0:
            gains.append(d)
            losses.append(0.0)
        else:
            gains.append(0.0)
            losses.append(-d)
    avg_gain = sum(gains) / float(period)
    avg_loss = sum(losses) / float(period)
    if avg_loss <= 0:
        return 100.0
    rs = avg_gain / avg_loss
    return 100.0 - 100.0 / (1.0 + rs)


def calc_macd(closes):
    if len(closes) < 35:
        return 0.0, 0.0
    ema12 = _ema(closes, 12)
    ema26 = _ema(closes, 26)
    if ema12 is None or ema26 is None:
        return 0.0, 0.0
    macd_line = ema12 - ema26
    series = []
    start = max(35, len(closes) - 60)
    for end in range(start, len(closes) + 1):
        sub = closes[:end]
        e12 = _ema(sub, 12)
        e26 = _ema(sub, 26)
        if e12 is None or e26 is None:
            continue
        series.append(e12 - e26)
    signal = _ema(series, 9) if series else 0.0
    return macd_line, signal or 0.0


def calc_bollinger(closes, period=20, k=2.0):
    m = _sma(closes, period)
    s = _stddev(closes, period)
    if m is None or s is None:
        return None, None, None
    return m, m + k * s, m - k * s


def calc_atr_pct(candles, period=14):
    if len(candles) < period + 1:
        return 0.0
    trs = []
    for i in range(-period, 0):
        hi = float(candles[i]["high"])
        lo = float(candles[i]["low"])
        prev_close = float(candles[i - 1]["close"])
        tr = max(hi - lo, abs(hi - prev_close), abs(lo - prev_close))
        trs.append(tr)
    atr = sum(trs) / float(period)
    px = float(candles[-1]["close"])
    if px <= 0:
        return 0.0
    return atr / px * 100.0


def calc_volume_ratio(vols):
    base = _sma(vols, Config.VOL_MA_PERIOD)
    if base is None or base <= 0:
        return 1.0
    return float(vols[-1]) / float(base)


def calc_ema_trend(closes):
    ef = _ema(closes, Config.EMA_FAST)
    es = _ema(closes, Config.EMA_SLOW)
    if ef is None or es is None:
        return "flat"
    diff = ef - es
    thr = float(closes[-1]) * 0.002
    if diff > thr:
        return "up"
    if diff < -thr:
        return "down"
    return "flat"


def calc_cvd_trend(candles):
    if len(candles) < Config.CVD_PERIOD + 2:
        return "flat"
    cvd = 0.0
    hist = []
    for bar in candles[-(Config.CVD_PERIOD * 2 + 2) :]:
        hi = float(bar["high"])
        lo = float(bar["low"])
        cl = float(bar["close"])
        vol = float(bar["vol"])
        if hi <= lo:
            clv = 0.0
        else:
            clv = ((cl - lo) - (hi - cl)) / (hi - lo)
        cvd += clv * vol
        hist.append(cvd)
    if len(hist) < Config.CVD_PERIOD + 1:
        return "flat"
    d = hist[-1] - hist[-1 - Config.CVD_PERIOD]
    if abs(d) < 1e-9:
        return "flat"
    return "up" if d > 0 else "down"


def calc_support_resistance(candles, price):
    if not candles:
        return price * 0.95, price * 1.05, 0.05, 0.05
    data = candles[-min(Config.SR_LOOKBACK, len(candles)) :]
    lows = [float(b["low"]) for b in data]
    highs = [float(b["high"]) for b in data]
    supports = []
    resistances = []
    w = max(2, int(Config.SR_WINDOW))
    half = w // 2
    for i in range(half, len(data) - half):
        if lows[i] == min(lows[i - half : i + half + 1]):
            supports.append(lows[i])
        if highs[i] == max(highs[i - half : i + half + 1]):
            resistances.append(highs[i])
    supports = [s for s in supports if s < price]
    resistances = [r for r in resistances if r > price]
    nearest_sup = max(supports) if supports else price * 0.95
    nearest_res = min(resistances) if resistances else price * 1.05
    dist_sup = (price - nearest_sup) / price if price > 0 else 0.0
    dist_res = (nearest_res - price) / price if price > 0 else 0.0
    return nearest_sup, nearest_res, dist_sup, dist_res


def calc_dynamic_sl(direction, entry, nearest_sup, nearest_res, default_sl):
    _ = entry
    buf = Config.DYNAMIC_SL_BUFFER
    if direction == "long":
        dynamic = nearest_sup * (1.0 - buf)
        if dynamic > default_sl:
            return dynamic, True
        return default_sl, False
    dynamic = nearest_res * (1.0 + buf)
    if dynamic < default_sl:
        return dynamic, True
    return default_sl, False


def calc_obv(candles):
    if not candles:
        return []
    out = [0.0]
    for i in range(1, len(candles)):
        prev = float(candles[i - 1]["close"])
        cur = float(candles[i]["close"])
        vol = float(candles[i]["vol"])
        if cur > prev:
            out.append(out[-1] + vol)
        elif cur < prev:
            out.append(out[-1] - vol)
        else:
            out.append(out[-1])
    return out


def calc_obv_trend(candles):
    obv = calc_obv(candles)
    ma = _sma(obv, Config.OBV_MA_PERIOD)
    if ma is None:
        return "flat"
    diff = (obv[-1] - ma) / (abs(ma) or 1.0)
    if diff > 0.02:
        return "up"
    if diff < -0.02:
        return "down"
    return "flat"


def calc_obv_divergence(candles):
    lb = Config.TF_DIV_LOOKBACK
    if len(candles) < lb + 5:
        return "none"
    sub = candles[-lb:]
    obv = calc_obv(sub)
    prices = [float(x["close"]) for x in sub]
    if prices[-1] > max(prices[:-5]) * 1.005 and obv[-1] < max(obv[:-5]) * 0.995:
        return "bearish"
    if prices[-1] < min(prices[:-5]) * 0.995 and obv[-1] > min(obv[:-5]) * 1.005:
        return "bullish"
    return "none"


def aggregate_candles(candles, group_size):
    if group_size <= 1 or len(candles) < group_size:
        return []
    out = []
    start = len(candles) % group_size
    if start != 0:
        candles = candles[start:]
    for i in range(0, len(candles), group_size):
        chunk = candles[i : i + group_size]
        if len(chunk) < group_size:
            continue
        out.append(
            {
                "symbol": chunk[-1]["symbol"],
                "open": float(chunk[0]["open"]),
                "high": max(float(x["high"]) for x in chunk),
                "low": min(float(x["low"]) for x in chunk),
                "close": float(chunk[-1]["close"]),
                "vol": sum(float(x["vol"]) for x in chunk),
                "ts": chunk[-1]["ts"],
            }
        )
    return out


def calc_tf4h_direction(candles):
    if len(candles) < 60:
        return None
    closes = [float(x["close"]) for x in candles]
    ef = _ema(closes, 21)
    es = _ema(closes, 55)
    if ef is None or es is None:
        return None
    rsi = calc_rsi(closes)
    thr = float(closes[-1]) * 0.003
    if ef - es > thr and rsi > 45:
        return "long"
    if es - ef > thr and rsi < 55:
        return "short"
    return None


def calc_kelly_fraction(params):
    p = max(0.0, min(1.0, params.win_rate))
    q = 1.0 - p
    b = params.avg_win_pct / params.avg_loss_pct if params.avg_loss_pct > 0 else 1.0
    kelly = (b * p - q) / b if b > 0 else 0.0
    if Config.KELLY_HALF:
        kelly /= 2.0
    kelly = max(params.min_fraction, min(params.max_fraction, kelly))
    return round(kelly, 4)


def calc_position_sizing(confidence, kelly_params, total_capital=None):
    if total_capital is None:
        total_capital = Config.TOTAL_CAPITAL
    base_kelly = calc_kelly_fraction(kelly_params)
    if confidence <= 0.85:
        conf_multiplier = 0.7 + max(0.0, confidence - 0.70) / 0.15 * 0.3
    else:
        conf_multiplier = 1.0 + max(0.0, confidence - 0.85) / 0.15 * 0.2
    adj_fraction = base_kelly * conf_multiplier
    adj_fraction = max(kelly_params.min_fraction, min(kelly_params.max_fraction, adj_fraction))
    usdt_amount = round(total_capital * adj_fraction, 2)
    return PositionSizing(
        kelly_fraction=base_kelly,
        used_fraction=round(adj_fraction, 4),
        usdt_amount=usdt_amount,
        rationale=(
            f"Kelly基础={base_kelly:.1%} | "
            f"置信度系数×{conf_multiplier:.2f} | "
            f"实际仓位={adj_fraction:.1%} | "
            f"建议金额={usdt_amount}U"
        ),
    )


def init_trailing_stop(direction, entry_price, activate_pct=None, trail_pct=None):
    if activate_pct is None:
        activate_pct = Config.TRAIL_ACTIVATE_PCT
    if trail_pct is None:
        trail_pct = Config.TRAIL_RETRACE_PCT
    if direction == "long":
        initial_stop = entry_price * (1.0 - trail_pct)
    else:
        initial_stop = entry_price * (1.0 + trail_pct)
    return TrailingStopState(
        direction=direction,
        entry_price=entry_price,
        trail_pct=trail_pct,
        highest_price=entry_price,
        lowest_price=entry_price,
        current_stop=initial_stop,
        activated=False,
        activate_pct=activate_pct,
    )


def score_confidence(
    rsi_val,
    macd_val,
    macd_sig,
    price,
    bb_upper,
    bb_lower,
    funding_rate,
    ls_ratio,
    atr_pct,
    vol_ratio,
    ema_trend,
    cvd_trend,
    dist_sup,
    dist_res,
    obv_trend,
    obv_divergence,
    tf4h_dir,
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

    if obv_trend == "up":
        ls += Config.OBV_TREND_BOOST
        ss -= Config.OBV_TREND_PENALTY
    elif obv_trend == "down":
        ss += Config.OBV_TREND_BOOST
        ls -= Config.OBV_TREND_PENALTY

    if obv_divergence == "bullish":
        ls += Config.OBV_DIV_BOOST
    elif obv_divergence == "bearish":
        ls -= Config.OBV_DIV_PENALTY
        ss += Config.OBV_DIV_BOOST

    ls = max(ls, 0.0)
    ss = max(ss, 0.0)

    tf_boost = 0.0
    if tf4h_dir:
        dominant = "long" if ls >= ss else "short"
        if dominant == tf4h_dir:
            if dominant == "long":
                ls += Config.TF_ALIGN_BOOST
            else:
                ss += Config.TF_ALIGN_BOOST
            tf_boost = Config.TF_ALIGN_BOOST
        else:
            if dominant == "long":
                ls -= Config.TF_CONFLICT_PENALTY
            else:
                ss -= Config.TF_CONFLICT_PENALTY
            tf_boost = -Config.TF_CONFLICT_PENALTY

    ls = max(ls, 0.0)
    ss = max(ss, 0.0)
    if ls >= Config.MIN_CONFIDENCE and ls >= ss:
        return ls, "long", vol_boost, ema_trend == "up", tf_boost
    if ss >= Config.MIN_CONFIDENCE and ss > ls:
        return ss, "short", vol_boost, ema_trend == "down", tf_boost
    dominant = "long" if ls >= ss else "short"
    align = ema_trend == "up" if dominant == "long" else ema_trend == "down"
    return max(ls, ss), None, vol_boost, align, tf_boost


def format_signal(result):
    if not result.passed_filter:
        return f"⚪ {result.symbol} — 跳过（{result.filter_reason}）"
    if result.direction is None:
        return f"⚪ {result.symbol} — 置信度{result.confidence:.0%}，无信号"
    label = "开多" if result.direction == "long" else "开空"
    trail = result.trailing_stop
    return (
        f"{result.symbol} | {label} | 置信度={result.confidence:.0%} | "
        f"开仓={result.entry_price:.8f} | 止盈={result.tp_price:.8f} | 止损={result.sl_price:.8f} | "
        f"建议仓位={result.position_sizing.usdt_amount}U({result.position_sizing.used_fraction:.1%}) | "
        f"移动止盈 激活={trail.activate_pct:.1%} 回撤={trail.trail_pct:.1%}"
    )


def _no_signal(symbol, price, funding_rate, ls_ratio, reason):
    sizing = PositionSizing(0.0, 0.0, 0.0, reason)
    trail = init_trailing_stop("long", price if price > 0 else 1.0)
    return SignalResult(
        symbol=symbol,
        direction=None,
        entry_price=price,
        tp_price=0.0,
        sl_price=0.0,
        confidence=0.0,
        position_sizing=sizing,
        trailing_stop=trail,
        passed_filter=False,
        filter_reason=reason,
        funding_rate=funding_rate,
        ls_ratio=ls_ratio,
    )


def analyze_signal(symbol, candles, funding_rate, ls_ratio, kelly_params):
    if len(candles) < Config.WARMUP_BARS:
        return _no_signal(symbol, candles[-1]["close"] if candles else 0.0, funding_rate, ls_ratio, "K线数量不足")
    price = float(candles[-1]["close"])
    if price > Config.MAX_PRICE:
        return _no_signal(symbol, price, funding_rate, ls_ratio, f"币价{price:.4f}超上限")
    if get_decimal_precision(price) < Config.MIN_PRECISION:
        return _no_signal(symbol, price, funding_rate, ls_ratio, "精度不足")
    lookback = min(120, len(candles) - 1)
    base = float(candles[-1 - lookback]["close"])
    change_pct_24h = (price / base - 1.0) * 100.0 if base > 0 else 0.0
    if abs(change_pct_24h) < Config.MIN_VOLATILITY:
        return _no_signal(symbol, price, funding_rate, ls_ratio, f"波动{change_pct_24h:.2f}%不足")

    closes = [float(x["close"]) for x in candles]
    vols = [float(x["vol"]) for x in candles]
    rsi_val = calc_rsi(closes)
    macd_val, macd_sig = calc_macd(closes)
    _, bb_upper, bb_lower = calc_bollinger(closes)
    atr_pct = calc_atr_pct(candles)
    vol_ratio = calc_volume_ratio(vols)
    ema_trend = calc_ema_trend(closes)
    cvd_trend = calc_cvd_trend(candles)
    nearest_sup, nearest_res, dist_sup, dist_res = calc_support_resistance(candles, price)
    obv_trend = calc_obv_trend(candles)
    obv_div = calc_obv_divergence(candles)
    higher_tf = aggregate_candles(candles, Config.HIGHER_TF_GROUP)
    tf4h_dir = calc_tf4h_direction(higher_tf) if higher_tf else None
    bb_pos = "下轨" if bb_lower is not None and price <= bb_lower else ("上轨" if bb_upper is not None and price >= bb_upper else "中部")

    confidence, direction, vol_boost, trend_align, tf_boost = score_confidence(
        rsi_val,
        macd_val,
        macd_sig,
        price,
        bb_upper,
        bb_lower,
        funding_rate,
        ls_ratio,
        atr_pct,
        vol_ratio,
        ema_trend,
        cvd_trend,
        dist_sup,
        dist_res,
        obv_trend,
        obv_div,
        tf4h_dir,
    )

    if direction == "long":
        default_tp = round(price * (1.0 + Config.TP_RATIO), 10)
        default_sl = round(price * (1.0 - Config.SL_RATIO), 10)
        sl, sl_dyn = calc_dynamic_sl("long", price, nearest_sup, nearest_res, default_sl)
        tp = default_tp
    elif direction == "short":
        default_tp = round(price * (1.0 - Config.TP_RATIO), 10)
        default_sl = round(price * (1.0 + Config.SL_RATIO), 10)
        sl, sl_dyn = calc_dynamic_sl("short", price, nearest_sup, nearest_res, default_sl)
        tp = default_tp
    else:
        tp = 0.0
        sl = 0.0
        sl_dyn = False

    sizing = calc_position_sizing(confidence, kelly_params)
    trail = init_trailing_stop(direction or "long", price)
    return SignalResult(
        symbol=symbol,
        direction=direction,
        entry_price=price,
        tp_price=tp,
        sl_price=sl,
        confidence=confidence,
        position_sizing=sizing,
        trailing_stop=trail,
        passed_filter=True,
        filter_reason="",
        rsi=rsi_val,
        macd_diff=macd_val - macd_sig,
        bb_position=bb_pos,
        atr_pct=atr_pct,
        funding_rate=funding_rate,
        ls_ratio=ls_ratio,
        vol_ratio=vol_ratio,
        ema_trend=ema_trend,
        vol_boost=vol_boost,
        trend_align=trend_align,
        cvd_trend=cvd_trend,
        nearest_support=nearest_sup,
        nearest_resistance=nearest_res,
        dist_to_support=dist_sup,
        dist_to_resistance=dist_res,
        sl_dynamic=sl_dyn,
        obv_trend=obv_trend,
        obv_divergence=obv_div,
        tf4h_direction=tf4h_dir,
        tf_aligned=tf4h_dir == direction,
        tf_boost=tf_boost,
    )


class MemeSignalEngineV5:
    def __init__(self, cfg=None):
        self.cfg = cfg or {}
        addr = str(self.cfg.get("redis_addr", "") or "").strip()
        self.redis_host = self.cfg.get("redis_host", "127.0.0.1")
        self.redis_port = int(self.cfg.get("redis_port", 6379))
        if addr and ":" in addr:
            try:
                host, port = addr.split(":", 1)
                self.redis_host = host.strip() or self.redis_host
                self.redis_port = int(port.strip())
            except Exception:
                pass
        self.redis_db = int(self.cfg.get("redis_db", 0) or 0)
        self.redis_password = str(self.cfg.get("redis_password", "") or "")
        self.redis_prefix = str(self.cfg.get("redis_prefix", "qt") or "qt")
        self.strategy_id = str(self.cfg.get("strategy_id", "") or "").strip()
        self.owner_id = int(self.cfg.get("owner_id", 0) or 0)
        self.symbols = self._parse_symbols(self.cfg.get("symbols", self.cfg.get("symbol", "")))
        self.cooldown_sec = max(0, _i(self.cfg.get("cooldown_sec", Config.SIGNAL_COOLDOWN_SEC), Config.SIGNAL_COOLDOWN_SEC))
        self.log_trace = bool(self.cfg.get("log_trace", False))
        self.boot_id = str(self.cfg.get("boot_id") or f"{self.strategy_id or 'strategy'}-{uuid.uuid4().hex[:12]}")
        self.healthcheck = self.cfg.get("healthcheck", {}) or {}
        self.base_trade_usdt = max(0.0, _f(self.cfg.get("base_trade_usdt", Config.BASE_TRADE_USDT), Config.BASE_TRADE_USDT))
        self.total_capital = max(self.base_trade_usdt, _f(self.cfg.get("total_capital", Config.TOTAL_CAPITAL), Config.TOTAL_CAPITAL))
        self.take_profit_pct = _parse_ratio(self.cfg.get("take_profit_pct", Config.TP_RATIO), Config.TP_RATIO)
        self.stop_loss_pct = _parse_ratio(self.cfg.get("stop_loss_pct", Config.SL_RATIO), Config.SL_RATIO)
        self.kelly_params = KellyParams(
            win_rate=max(0.01, min(0.99, _f(self.cfg.get("kelly_win_rate", 0.50), 0.50))),
            avg_win_pct=max(0.001, _parse_ratio(self.cfg.get("kelly_avg_win_pct", 0.06), 0.06)),
            avg_loss_pct=max(0.001, _parse_ratio(self.cfg.get("kelly_avg_loss_pct", 0.03), 0.03)),
            max_fraction=max(0.01, _parse_ratio(self.cfg.get("kelly_max_fraction", 0.25), 0.25)),
            min_fraction=max(0.001, _parse_ratio(self.cfg.get("kelly_min_fraction", 0.05), 0.05)),
        )
        self.candles = {}
        self.last_signal_at = {}

    def _parse_symbols(self, raw):
        if isinstance(raw, (list, tuple, set)):
            return [str(x).strip() for x in raw if str(x).strip()]
        s = str(raw or "").strip()
        if not s:
            return []
        return [part.strip() for part in s.split(",") if part.strip()]

    def _state_ch(self):
        return f"{self.redis_prefix}:state:{self.strategy_id}"

    def _signal_ch(self):
        return f"{self.redis_prefix}:signal:{self.strategy_id}"

    def _candle_ch(self):
        return f"{self.redis_prefix}:candle:{self.strategy_id}"

    def _log(self, text):
        try:
            print(json.dumps({"type": "log", "data": text}, ensure_ascii=False), flush=True)
        except Exception:
            pass

    def _publish_state(self, r, typ):
        msg = {
            "type": typ,
            "strategy_id": self.strategy_id,
            "owner_id": self.owner_id,
            "boot_id": self.boot_id,
            "created_at": _now_iso(),
        }
        r.publish(self._state_ch(), json.dumps(msg, ensure_ascii=False))
        if self.redis_prefix != "qt":
            r.publish(f"qt:state:{self.strategy_id}", json.dumps(msg, ensure_ascii=False))

    def _heartbeat_loop(self, r):
        interval = max(2, _i(self.healthcheck.get("interval_sec", 5), 5))
        while True:
            try:
                self._publish_state(r, "heartbeat")
            except Exception as e:
                self._log(f"心跳发送失败 err={e}")
            time.sleep(interval)

    def _emit_signal(self, r, result):
        amount = max(self.base_trade_usdt, result.position_sizing.usdt_amount)
        side = "buy" if result.direction == "long" else "sell"
        msg = {
            "type": "signal",
            "strategy_id": self.strategy_id,
            "owner_id": self.owner_id,
            "action": "open",
            "symbol": result.symbol,
            "side": side,
            "amount": amount,
            "price": 0,
            "take_profit": result.tp_price,
            "stop_loss": result.sl_price,
            "signal_id": f"{self.boot_id}:{result.symbol}:{int(time.time() * 1000)}",
            "generated_at": _now_iso(),
            "confidence": result.confidence,
        }
        r.publish(self._signal_ch(), json.dumps(msg, ensure_ascii=False))
        if self.redis_prefix != "qt":
            r.publish(f"qt:signal:{self.strategy_id}", json.dumps(msg, ensure_ascii=False))
        self._log(f"SIGNAL open sym={result.symbol} dir={result.direction} side={side} qty={amount} entry={result.entry_price:.8f} tp={result.tp_price:.8f} sl={result.sl_price:.8f} conf={result.confidence:.4f} ts={_now_iso()}")
        self._log(f"信号说明 {format_signal(result)}")

    def _push_candle(self, symbol, candle):
        arr = self.candles.get(symbol)
        if arr is None:
            arr = []
            self.candles[symbol] = arr
        arr.append(candle)
        if len(arr) > Config.MAX_BARS:
            del arr[0 : len(arr) - Config.MAX_BARS]

    def _funding_rate(self):
        return _f(self.cfg.get("funding_rate", 0.0), 0.0)

    def _ls_ratio(self):
        return max(0.01, _f(self.cfg.get("ls_ratio", 1.0), 1.0))

    def _subscribe_candles(self, receiver):
        receiver.subscribe(self._candle_ch())
        self._log(f"订阅K线通道 {self._candle_ch()}")
        if self.redis_prefix != "qt":
            receiver.subscribe(f"qt:candle:{self.strategy_id}")
            self._log(f"订阅K线通道 qt:candle:{self.strategy_id}")

    def _handle_payload(self, payload):
        kind = str(payload.get("type", "candle") or "candle").lower()
        if kind == "history":
            symbol = str(payload.get("symbol") or "").strip()
            if symbol and (not self.symbols or symbol in self.symbols):
                for c in payload.get("candles", []) or []:
                    self._handle_payload(c)
            return None

        symbol = str(payload.get("symbol") or payload.get("s") or "").strip()
        if not symbol:
            return None
        if self.symbols and symbol not in self.symbols:
            return None
        candle = {
            "symbol": symbol,
            "open": _f(payload.get("open", payload.get("o", 0)), 0.0),
            "high": _f(payload.get("high", payload.get("h", 0)), 0.0),
            "low": _f(payload.get("low", payload.get("l", 0)), 0.0),
            "close": _f(payload.get("close", payload.get("c", 0)), 0.0),
            "vol": _f(payload.get("volume", payload.get("vol", 0)), 0.0),
            "ts": payload.get("timestamp") or payload.get("ts") or 0,
        }
        if candle["close"] <= 0:
            return None
        self._push_candle(symbol, candle)
        return symbol

    def run(self):
        try:
            MiniRedis
        except NameError:
            raise RuntimeError("MiniRedis is required")
        if not self.strategy_id:
            raise RuntimeError("strategy_id required")
        if not self.symbols:
            raise RuntimeError("symbols required")

        receiver = MiniRedis(self.redis_host, self.redis_port, self.redis_password, self.redis_db).connect()
        sender = MiniRedis(self.redis_host, self.redis_port, self.redis_password, self.redis_db).connect()
        self._subscribe_candles(receiver)
        ready_msg = {
            "type": "ready",
            "strategy_id": self.strategy_id,
            "owner_id": self.owner_id,
            "boot_id": self.boot_id,
            "created_at": _now_iso(),
        }
        sender.publish(self._state_ch(), json.dumps(ready_msg, ensure_ascii=False))
        if self.redis_prefix != "qt":
            sender.publish(f"qt:state:{self.strategy_id}", json.dumps(ready_msg, ensure_ascii=False))
        threading.Thread(target=self._heartbeat_loop, args=(sender,), daemon=True).start()
        self._log(
            f"策略已就绪 strategy_id={self.strategy_id} boot_id={self.boot_id} "
            f"symbols={len(self.symbols)} total_capital={self.total_capital} base_trade={self.base_trade_usdt}"
        )

        while True:
            msg = receiver.read_pubsub_message()
            if not msg:
                continue
            data = msg.get("data")
            if not data:
                continue
            try:
                payload = json.loads(data)
            except Exception:
                continue
            symbol = self._handle_payload(payload)
            if not symbol:
                continue
            candles = self.candles.get(symbol, [])
            now = time.time()
            last_at = self.last_signal_at.get(symbol, 0.0)
            if self.cooldown_sec > 0 and now - last_at < self.cooldown_sec:
                continue
            result = analyze_signal(symbol, candles, self._funding_rate(), self._ls_ratio(), self.kelly_params)
            if not result.passed_filter or result.direction is None:
                if self.log_trace and result.filter_reason:
                    self._log(f"跳过信号 symbol={symbol} reason={result.filter_reason}")
                continue
            self._emit_signal(sender, result)
            self.last_signal_at[symbol] = now


if __name__ == "__main__":
    cfg = {}
    if len(sys.argv) >= 2 and str(sys.argv[1]).strip():
        try:
            cfg = json.loads(sys.argv[1])
        except Exception:
            cfg = {}
    if os.environ.get("STRATEGY_CONFIG_JSON"):
        try:
            env_cfg = json.loads(os.environ.get("STRATEGY_CONFIG_JSON"))
            if isinstance(env_cfg, dict):
                cfg.update(env_cfg)
        except Exception:
            pass
    strategy = MemeSignalEngineV5(cfg)
    strategy.run()
