import json
import os
import sys
import threading
import time
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from enum import Enum
from typing import Optional

import numpy as np
import pandas as pd

try:
    from mini_redis import MiniRedis
except Exception:
    pass

from redis_compat import RedisCompat


class MarketRegime(Enum):
    TRENDING_UP = "trending_up"
    TRENDING_DOWN = "trending_down"
    RANGING = "ranging"
    REVERSAL_UP = "reversal_up"
    REVERSAL_DOWN = "reversal_down"
    UNKNOWN = "unknown"


@dataclass
class MarketSnapshot:
    symbol: str
    price: float
    change_pct_24h: float
    funding_rate: float
    ls_ratio: float


@dataclass
class OHLCVData:
    symbol: str
    df: pd.DataFrame


@dataclass
class KellyParams:
    win_rate: float = 0.50
    avg_win_pct: float = 0.06
    avg_loss_pct: float = 0.03
    max_fraction: float = 0.25
    min_fraction: float = 0.05


@dataclass
class RegimeResult:
    regime: MarketRegime
    adx: float
    adx_plus_di: float
    adx_minus_di: float
    atr_percentile: float
    price_position: float
    regime_label: str
    strategy_hint: str


@dataclass
class AdaptiveConfig:
    tp_ratio: float
    sl_ratio: float
    min_confidence: float
    trail_activate: float
    trail_retrace: float
    kelly_multiplier: float
    label: str


@dataclass
class ScoreCard:
    symbol: str
    direction: Optional[str]
    confidence: float
    momentum_score: float
    volume_score: float
    structure_score: float
    sentiment_score: float
    trend_score: float
    overall_score: float
    strengths: list[str] = field(default_factory=list)
    risks: list[str] = field(default_factory=list)
    regime: MarketRegime = MarketRegime.UNKNOWN
    regime_config: Optional[AdaptiveConfig] = None
    verdict: str = "🚫 建议跳过"


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
    rsi: float
    macd_diff: float
    bb_position: str
    atr_pct: float
    funding_rate: float
    ls_ratio: float
    vol_ratio: float
    ema_trend: str
    vol_boost: bool
    trend_align: bool
    cvd_trend: str
    nearest_support: float
    nearest_resistance: float
    dist_to_support: float
    dist_to_resistance: float
    sl_dynamic: bool
    obv_trend: str
    obv_divergence: str
    tf4h_direction: Optional[str]
    tf_aligned: bool
    tf_boost: float
    position_sizing: PositionSizing
    trailing_stop: TrailingStopState
    regime_result: RegimeResult
    score_card: ScoreCard
    adaptive_cfg: AdaptiveConfig
    passed_filter: bool
    filter_reason: str


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

    BASE_TRADE_USDT = 10.0
    TOTAL_CAPITAL = 200.0
    KELLY_HALF = True
    TRAIL_ACTIVATE_PCT = 0.025
    TRAIL_RETRACE_PCT = 0.015

    ADX_TREND_THR = 25
    ADX_RANGE_THR = 20
    ADX_PERIOD = 14
    ATR_LOOKBACK = 100
    REVERSAL_RSI_LOW = 30
    REVERSAL_RSI_HIGH = 70

    MAX_BARS = 720
    WARMUP_BARS = 180
    HIGHER_TF_GROUP = 4
    SIGNAL_COOLDOWN_SEC = 600


ADAPTIVE_CONFIGS: dict[MarketRegime, AdaptiveConfig] = {
    MarketRegime.TRENDING_UP: AdaptiveConfig(0.10, 0.04, 0.72, 0.04, 0.02, 1.2, "上升趋势"),
    MarketRegime.TRENDING_DOWN: AdaptiveConfig(0.10, 0.04, 0.72, 0.04, 0.02, 1.2, "下降趋势"),
    MarketRegime.RANGING: AdaptiveConfig(0.04, 0.02, 0.75, 0.015, 0.01, 0.8, "震荡盘整"),
    MarketRegime.REVERSAL_UP: AdaptiveConfig(0.08, 0.025, 0.80, 0.03, 0.015, 0.9, "底部反转"),
    MarketRegime.REVERSAL_DOWN: AdaptiveConfig(0.08, 0.025, 0.80, 0.03, 0.015, 0.9, "顶部反转"),
    MarketRegime.UNKNOWN: AdaptiveConfig(0.06, 0.03, 0.75, 0.025, 0.015, 0.7, "状态未知"),
}


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


def _normalize_df(df: pd.DataFrame) -> pd.DataFrame:
    cols = ["ts", "open", "high", "low", "close", "vol"]
    sub = df.copy()
    for c in cols:
        if c not in sub.columns:
            sub[c] = 0.0
    sub = sub[cols]
    for c in ["open", "high", "low", "close", "vol"]:
        sub[c] = pd.to_numeric(sub[c], errors="coerce")
    sub["ts"] = pd.to_numeric(sub["ts"], errors="coerce")
    sub = sub.dropna(subset=["close"]).reset_index(drop=True)
    return sub


def candles_to_ohlcv(symbol: str, candles: list[dict]) -> OHLCVData:
    if not candles:
        return OHLCVData(symbol, pd.DataFrame(columns=["ts", "open", "high", "low", "close", "vol"]))
    rows = []
    for c in candles:
        rows.append({
            "ts": _f(c.get("ts", 0), 0),
            "open": _f(c.get("open", 0), 0),
            "high": _f(c.get("high", 0), 0),
            "low": _f(c.get("low", 0), 0),
            "close": _f(c.get("close", 0), 0),
            "vol": _f(c.get("vol", 0), 0),
        })
    return OHLCVData(symbol, _normalize_df(pd.DataFrame(rows)))


def aggregate_ohlcv(ohlcv: OHLCVData, group: int) -> Optional[OHLCVData]:
    if ohlcv is None or ohlcv.df.empty or group <= 1:
        return ohlcv
    df = ohlcv.df.copy().reset_index(drop=True)
    if len(df) < group * 20:
        return None
    bucket = np.arange(len(df)) // group
    agg = df.groupby(bucket).agg({
        "ts": "last",
        "open": "first",
        "high": "max",
        "low": "min",
        "close": "last",
        "vol": "sum",
    }).reset_index(drop=True)
    return OHLCVData(ohlcv.symbol, agg)


def calc_rsi(closes: pd.Series, period: int = 14) -> float:
    delta = closes.diff()
    gain = delta.clip(lower=0).rolling(period).mean()
    loss = (-delta.clip(upper=0)).rolling(period).mean()
    rs = gain / loss.replace(0, np.nan)
    val = 100 - 100 / (1 + rs.iloc[-1])
    if pd.isna(val):
        return 50.0
    return float(val)


def calc_macd(closes: pd.Series) -> tuple[float, float]:
    line = closes.ewm(span=12, adjust=False).mean() - closes.ewm(span=26, adjust=False).mean()
    signal = line.ewm(span=9, adjust=False).mean()
    return float(line.iloc[-1]), float(signal.iloc[-1])


def calc_bollinger(closes: pd.Series, period: int = 20) -> tuple[float, float, float]:
    ma = closes.rolling(period).mean()
    std = closes.rolling(period).std()
    return float(ma.iloc[-1]), float(ma.iloc[-1] + 2 * std.iloc[-1]), float(ma.iloc[-1] - 2 * std.iloc[-1])


def calc_atr(df: pd.DataFrame, period: int = 14) -> pd.Series:
    high, low, close = df["high"], df["low"], df["close"]
    tr = pd.concat([
        high - low,
        (high - close.shift()).abs(),
        (low - close.shift()).abs(),
    ], axis=1).max(axis=1)
    return tr.rolling(period).mean()


def calc_atr_pct(df: pd.DataFrame, period: int = 14) -> float:
    atr = calc_atr(df, period)
    px = float(df["close"].iloc[-1])
    if px <= 0 or pd.isna(float(atr.iloc[-1])):
        return 0.0
    return float(atr.iloc[-1]) / px * 100


def calc_volume_ratio(vol: pd.Series) -> float:
    ma = vol.rolling(Config.VOL_MA_PERIOD).mean()
    base = float(ma.iloc[-1]) if len(ma) else 0.0
    return float(vol.iloc[-1]) / base if base > 0 else 1.0


def calc_ema_trend(closes: pd.Series) -> str:
    ef = closes.ewm(span=Config.EMA_FAST, adjust=False).mean()
    es = closes.ewm(span=Config.EMA_SLOW, adjust=False).mean()
    diff = float(ef.iloc[-1]) - float(es.iloc[-1])
    thr = float(closes.iloc[-1]) * 0.002
    return "up" if diff > thr else ("down" if diff < -thr else "flat")


def calc_cvd(df: pd.DataFrame) -> str:
    high, low, close, vol = df["high"], df["low"], df["close"], df["vol"]
    hl = (high - low).replace(0, np.nan)
    buy_flow = vol * (close - low) / hl
    sell_flow = vol * (high - close) / hl
    cvd = (buy_flow - sell_flow).fillna(0).cumsum()
    r = cvd.iloc[-Config.CVD_PERIOD:].values
    if len(r) < 2:
        return "flat"
    slope = np.polyfit(np.arange(len(r)), r, 1)[0]
    norm = slope / (float(np.mean(np.abs(r))) or 1.0)
    return "up" if norm > 0.01 else ("down" if norm < -0.01 else "flat")


def calc_support_resistance(df: pd.DataFrame, price: float) -> tuple[float, float, float, float]:
    sub = df.iloc[-min(Config.SR_LOOKBACK, len(df)):].reset_index(drop=True)
    w = Config.SR_WINDOW
    hs, ls = sub["high"].values, sub["low"].values
    res, sup = [], []
    for i in range(w, len(sub) - w):
        if hs[i] == max(hs[i - w:i + w + 1]):
            res.append(hs[i])
        if ls[i] == min(ls[i - w:i + w + 1]):
            sup.append(ls[i])
    ns = max([s for s in sup if s < price], default=price * 0.95)
    nr = min([r for r in res if r > price], default=price * 1.05)
    return ns, nr, (price - ns) / price, (nr - price) / price


def calc_dynamic_sl(direction, entry, ns, nr, default_sl):
    buf = Config.DYNAMIC_SL_BUFFER
    if direction == "long":
        d = ns * (1 - buf)
        return (d, True) if d > default_sl else (default_sl, False)
    d = nr * (1 + buf)
    return (d, True) if d < default_sl else (default_sl, False)


def calc_obv(df: pd.DataFrame) -> pd.Series:
    return (np.sign(df["close"].diff().fillna(0)) * df["vol"]).cumsum()


def calc_obv_trend(df: pd.DataFrame) -> str:
    obv = calc_obv(df)
    ma = obv.rolling(Config.OBV_MA_PERIOD).mean()
    if pd.isna(float(ma.iloc[-1])):
        return "flat"
    diff = (float(obv.iloc[-1]) - float(ma.iloc[-1])) / (abs(float(ma.iloc[-1])) or 1.0)
    return "up" if diff > 0.02 else ("down" if diff < -0.02 else "flat")


def calc_obv_divergence(df: pd.DataFrame) -> str:
    lb = Config.TF_DIV_LOOKBACK
    if len(df) < lb + 5:
        return "none"
    sub = df.iloc[-lb:].reset_index(drop=True)
    obv = calc_obv(sub).values
    price = sub["close"].values
    if price[-1] > max(price[:-5]) * 1.005 and obv[-1] < max(obv[:-5]) * 0.995:
        return "bearish"
    if price[-1] < min(price[:-5]) * 0.995 and obv[-1] > min(obv[:-5]) * 1.005:
        return "bullish"
    return "none"


def calc_tf4h_direction(ohlcv_4h: OHLCVData) -> Optional[str]:
    closes = ohlcv_4h.df["close"]
    if len(closes) < 60:
        return None
    ef = float(closes.ewm(span=21, adjust=False).mean().iloc[-1])
    es = float(closes.ewm(span=55, adjust=False).mean().iloc[-1])
    rsi = calc_rsi(closes)
    thr = float(closes.iloc[-1]) * 0.003
    if ef - es > thr and rsi > 45:
        return "long"
    if es - ef > thr and rsi < 55:
        return "short"
    return None


def get_decimal_precision(price: float) -> int:
    s = f"{price:.10f}".rstrip("0").rstrip(".")
    return len(s.split(".")[1]) if "." in s else 0


def calc_kelly_fraction(params: KellyParams) -> float:
    p = params.win_rate
    q = 1 - p
    b = params.avg_win_pct / params.avg_loss_pct if params.avg_loss_pct > 0 else 1.0
    k = (b * p - q) / b
    if Config.KELLY_HALF:
        k /= 2.0
    return max(params.min_fraction, min(params.max_fraction, round(k, 4)))


def calc_position_sizing(confidence, kp, regime_cfg=None, total_capital=None):
    if total_capital is None:
        total_capital = Config.TOTAL_CAPITAL
    base = calc_kelly_fraction(kp)
    mult = getattr(regime_cfg, "kelly_multiplier", 1.0) if regime_cfg else 1.0
    if confidence <= 0.85:
        cf = 0.7 + max(0.0, confidence - 0.70) / max(0.15, 0.85 - 0.70) * 0.3
    else:
        cf = 1.0 + min(0.15, confidence - 0.85) / 0.15 * 0.2
    adj = max(kp.min_fraction, min(kp.max_fraction, base * cf * mult))
    return PositionSizing(
        kelly_fraction=base,
        used_fraction=round(adj, 4),
        usdt_amount=round(total_capital * adj, 2),
        rationale=f"Kelly={base:.1%} × 置信度系数{cf:.2f} × Regime系数{mult:.1f} = {adj:.1%} → {round(total_capital * adj, 2)}U",
    )


def init_trailing_stop(direction, entry, activate_pct=None, trail_pct=None):
    if activate_pct is None:
        activate_pct = Config.TRAIL_ACTIVATE_PCT
    if trail_pct is None:
        trail_pct = Config.TRAIL_RETRACE_PCT
    init_stop = entry * (1 - trail_pct) if direction == "long" else entry * (1 + trail_pct)
    return TrailingStopState(direction, entry, trail_pct, entry, entry, init_stop, False, activate_pct)


def calc_adx(df: pd.DataFrame, period: int = None) -> tuple[float, float, float]:
    if period is None:
        period = Config.ADX_PERIOD
    if len(df) < period * 2 + 5:
        return 0.0, 0.0, 0.0
    high = df["high"].values
    low = df["low"].values
    close = df["close"].values
    n = len(close)
    tr_arr = np.zeros(n)
    plus_dm_arr = np.zeros(n)
    minus_dm_arr = np.zeros(n)
    for i in range(1, n):
        tr_arr[i] = max(high[i] - low[i], abs(high[i] - close[i - 1]), abs(low[i] - close[i - 1]))
        move_up = high[i] - high[i - 1]
        move_down = low[i - 1] - low[i]
        plus_dm_arr[i] = move_up if move_up > move_down and move_up > 0 else 0.0
        minus_dm_arr[i] = move_down if move_down > move_up and move_down > 0 else 0.0

    def wilder_smooth(arr, p):
        result = np.zeros(len(arr))
        result[p] = np.sum(arr[1:p + 1])
        for i in range(p + 1, len(arr)):
            result[i] = result[i - 1] - result[i - 1] / p + arr[i]
        return result

    atr_s = wilder_smooth(tr_arr, period)
    plus_s = wilder_smooth(plus_dm_arr, period)
    minus_s = wilder_smooth(minus_dm_arr, period)
    atr_s = np.nan_to_num(atr_s, nan=0.0, posinf=0.0, neginf=0.0)
    safe_atr = np.where(atr_s == 0, 1e-10, atr_s)
    plus_di = np.divide(100 * plus_s, safe_atr, out=np.zeros_like(safe_atr), where=safe_atr != 0)
    minus_di = np.divide(100 * minus_s, safe_atr, out=np.zeros_like(safe_atr), where=safe_atr != 0)
    plus_di = np.nan_to_num(plus_di, nan=0.0, posinf=0.0, neginf=0.0)
    minus_di = np.nan_to_num(minus_di, nan=0.0, posinf=0.0, neginf=0.0)
    di_sum = plus_di + minus_di
    with np.errstate(divide="ignore", invalid="ignore"):
        dx = np.divide(
            100 * np.abs(plus_di - minus_di),
            di_sum,
            out=np.zeros_like(di_sum),
            where=di_sum != 0,
        )
    dx = np.nan_to_num(dx, nan=0.0, posinf=0.0, neginf=0.0)
    adx_arr = np.zeros(n)
    if n > period * 2:
        adx_arr[period * 2] = np.mean(dx[period:period * 2 + 1])
        for i in range(period * 2 + 1, n):
            adx_arr[i] = (adx_arr[i - 1] * (period - 1) + dx[i]) / period
    return float(adx_arr[-1]), float(plus_di[-1]), float(minus_di[-1])


def detect_regime(df: pd.DataFrame, rsi_val: float) -> RegimeResult:
    closes = df["close"]
    price = float(closes.iloc[-1])
    adx, plus_di, minus_di = calc_adx(df)
    atr_series = calc_atr(df)
    valid_atr = atr_series.dropna()
    lb = min(Config.ATR_LOOKBACK, len(valid_atr))
    atr_hist = valid_atr.iloc[-lb:].values
    curr_atr = float(atr_series.iloc[-1]) if len(atr_series) else 0.0
    atr_pctile = float(np.mean(atr_hist <= curr_atr)) if len(atr_hist) > 0 else 0.5
    lookback = min(60, len(df))
    sub = df.iloc[-lookback:]
    hi_range = float(sub["high"].max())
    lo_range = float(sub["low"].min())
    price_pos = (price - lo_range) / (hi_range - lo_range) if hi_range > lo_range else 0.5
    if adx > Config.ADX_TREND_THR:
        if plus_di >= minus_di:
            regime = MarketRegime.TRENDING_UP
            hint = "顺势做多，宽止损跟趋势"
        else:
            regime = MarketRegime.TRENDING_DOWN
            hint = "顺势做空，宽止损跟趋势"
    elif adx < Config.ADX_RANGE_THR:
        if rsi_val < Config.REVERSAL_RSI_LOW and price_pos < 0.25:
            regime = MarketRegime.REVERSAL_UP
            hint = "底部反转信号，谨慎做多，高置信度才入场"
        elif rsi_val > Config.REVERSAL_RSI_HIGH and price_pos > 0.75:
            regime = MarketRegime.REVERSAL_DOWN
            hint = "顶部反转信号，谨慎做空，高置信度才入场"
        else:
            regime = MarketRegime.RANGING
            hint = "震荡行情，快进快出，紧止损，忌追涨杀跌"
    else:
        regime = MarketRegime.UNKNOWN
        hint = "趋势不明，降仓观望或等ADX方向确认"
    label_map = {
        MarketRegime.TRENDING_UP: "📈 上升趋势",
        MarketRegime.TRENDING_DOWN: "📉 下降趋势",
        MarketRegime.RANGING: "↔️ 震荡盘整",
        MarketRegime.REVERSAL_UP: "🔄 底部反转",
        MarketRegime.REVERSAL_DOWN: "🔄 顶部反转",
        MarketRegime.UNKNOWN: "❓ 趋势不明",
    }
    return RegimeResult(regime, round(adx, 2), round(plus_di, 2), round(minus_di, 2), round(atr_pctile, 2), round(price_pos, 2), label_map[regime], hint)


def build_score_card(symbol, direction, confidence, rsi_val, macd_diff, bb_pos, vol_ratio, vol_boost, cvd_trend, obv_trend, obv_div, dist_sup, dist_res, funding_rate, ls_ratio, ema_trend, tf_aligned, tf_boost, regime, adaptive_cfg):
    is_long = direction == "long"
    rsi_score = 100 if (is_long and rsi_val < 30) or ((not is_long) and rsi_val > 70) else 70 if (is_long and rsi_val < 40) or ((not is_long) and rsi_val > 60) else 40 if (is_long and rsi_val < 50) or ((not is_long) and rsi_val > 50) else 10
    macd_score = 80 if (is_long and macd_diff > 0) or ((not is_long) and macd_diff < 0) else 20
    momentum_score = round(rsi_score * 0.6 + macd_score * 0.4, 1)
    obv_score = 80 if (is_long and obv_trend == "up") or ((not is_long) and obv_trend == "down") else 20 if (is_long and obv_trend == "down") or ((not is_long) and obv_trend == "up") else 50
    cvd_score = 80 if (is_long and cvd_trend == "up") or ((not is_long) and cvd_trend == "down") else 20 if (is_long and cvd_trend == "down") or ((not is_long) and cvd_trend == "up") else 50
    div_score = 90 if (is_long and obv_div == "bullish") or ((not is_long) and obv_div == "bearish") else 20 if (is_long and obv_div == "bearish") or ((not is_long) and obv_div == "bullish") else 50
    vol_extra = 15 if vol_boost else 0
    volume_score = min(100.0, round(obv_score * 0.35 + cvd_score * 0.35 + div_score * 0.2 + min(20.0, vol_ratio * 5) + vol_extra, 1))
    bb_score = 90 if (is_long and bb_pos == "下轨") or ((not is_long) and bb_pos == "上轨") else 20 if (is_long and bb_pos == "上轨") or ((not is_long) and bb_pos == "下轨") else 50
    sr_score = 80 if (is_long and dist_sup < 0.01) or ((not is_long) and dist_res < 0.01) else 60 if (is_long and dist_sup < 0.03) or ((not is_long) and dist_res < 0.03) else 30
    structure_score = round(bb_score * 0.5 + sr_score * 0.5, 1)
    if is_long:
        fr_score = 100 if funding_rate < -0.0005 else 75 if funding_rate < -0.0002 else 55 if funding_rate < 0 else 20
        ls_score = 80 if ls_ratio < 0.8 else 50 if ls_ratio < 1.0 else 20
    else:
        fr_score = 100 if funding_rate > 0.0005 else 75 if funding_rate > 0.0002 else 55 if funding_rate > 0 else 20
        ls_score = 80 if ls_ratio > 1.5 else 50 if ls_ratio > 1.2 else 20
    sentiment_score = round(fr_score * 0.6 + ls_score * 0.4, 1)
    ema_score = 80 if (is_long and ema_trend == "up") or ((not is_long) and ema_trend == "down") else 20 if (is_long and ema_trend == "down") or ((not is_long) and ema_trend == "up") else 50
    tf_score = 90 if tf_aligned else (30 if tf_boost < 0 else 50)
    adx_score = min(100.0, regime.adx * 2.5)
    trend_score = round(ema_score * 0.4 + tf_score * 0.35 + adx_score * 0.25, 1)
    overall = round(momentum_score * 0.30 + volume_score * 0.25 + structure_score * 0.20 + sentiment_score * 0.15 + trend_score * 0.10, 1)
    strengths, risks = [], []
    if momentum_score >= 70:
        strengths.append(f"动量强劲({momentum_score:.0f}/100)")
    if volume_score >= 70:
        strengths.append(f"量能支撑({volume_score:.0f}/100)")
    if structure_score >= 70:
        strengths.append(f"结构良好({structure_score:.0f}/100)")
    if sentiment_score >= 70:
        strengths.append(f"情绪顺向({sentiment_score:.0f}/100)")
    if trend_score >= 70:
        strengths.append(f"趋势一致({trend_score:.0f}/100)")
    if obv_div == ("bullish" if is_long else "bearish"):
        strengths.append("OBV背离确认✅")
    if tf_aligned:
        strengths.append("多时间框架对齐✅")
    if momentum_score < 40:
        risks.append(f"动量偏弱({momentum_score:.0f}/100)")
    if volume_score < 40:
        risks.append(f"量能不足({volume_score:.0f}/100)")
    if not tf_aligned and tf_boost < 0:
        risks.append("4h方向冲突⚠️")
    if regime.regime == MarketRegime.TRENDING_DOWN and is_long:
        risks.append("逆下降趋势做多⚠️")
    if regime.regime == MarketRegime.TRENDING_UP and not is_long:
        risks.append("逆上升趋势做空⚠️")
    if regime.atr_percentile > 0.8:
        risks.append(f"波动率处于历史高位({regime.atr_percentile:.0%})⚠️")
    verdict = "⭐⭐⭐ 强烈推荐" if overall >= 75 and len(risks) <= 1 else "⭐⭐ 谨慎推荐" if overall >= 65 and len(risks) <= 2 else "⭐ 条件观望" if overall >= 55 else "🚫 建议跳过"
    return ScoreCard(symbol, direction, confidence, momentum_score, volume_score, structure_score, sentiment_score, trend_score, overall, strengths, risks, regime.regime, adaptive_cfg, verdict)


def score_confidence(rsi_val, macd_val, macd_sig, price, bb_upper, bb_lower, funding_rate, ls_ratio, atr_pct, vol_ratio, ema_trend, cvd_trend, dist_sup, dist_res, obv_trend, obv_divergence, tf4h_dir, min_confidence=None):
    if min_confidence is None:
        min_confidence = Config.MIN_CONFIDENCE
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
    if price <= bb_lower:
        ls += 0.15
    elif price >= bb_upper:
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
    if ls >= min_confidence and ls >= ss:
        return ls, "long", vol_boost, ema_trend == "up", tf_boost
    if ss >= min_confidence and ss > ls:
        return ss, "short", vol_boost, ema_trend == "down", tf_boost
    dominant = "long" if ls >= ss else "short"
    align = ema_trend == "up" if dominant == "long" else ema_trend == "down"
    return max(ls, ss), None, vol_boost, align, tf_boost


def _no_signal(snap: MarketSnapshot, reason: str) -> SignalResult:
    dummy_regime = RegimeResult(MarketRegime.UNKNOWN, 0, 0, 0, 0.5, 0.5, "❓ 状态未知", "")
    dummy_cfg = ADAPTIVE_CONFIGS[MarketRegime.UNKNOWN]
    dummy_sc = ScoreCard(snap.symbol, None, 0, 0, 0, 0, 0, 0, 0, [], [], MarketRegime.UNKNOWN, dummy_cfg, "🚫 建议跳过")
    dummy_sz = PositionSizing(0, 0, 0, reason)
    dummy_ts = init_trailing_stop("long", snap.price if snap.price > 0 else 1.0)
    return SignalResult(snap.symbol, None, snap.price, 0, 0, 0, 0, 0, "—", 0, snap.funding_rate, snap.ls_ratio, 0, "flat", False, False, "flat", 0, 0, 0, 0, False, "flat", "none", None, False, 0.0, dummy_sz, dummy_ts, dummy_regime, dummy_sc, dummy_cfg, False, reason)


def analyze(snapshot: MarketSnapshot, ohlcv_1h: OHLCVData, ohlcv_4h: Optional[OHLCVData] = None, kelly_params: Optional[KellyParams] = None) -> SignalResult:
    price = snapshot.price
    if price > Config.MAX_PRICE:
        return _no_signal(snapshot, f"币价${price:.8f}超上限")
    if get_decimal_precision(price) < Config.MIN_PRECISION:
        return _no_signal(snapshot, "精度不足5位")
    if abs(snapshot.change_pct_24h) < Config.MIN_VOLATILITY:
        return _no_signal(snapshot, f"24h波动{snapshot.change_pct_24h:.1f}%不足")
    df = _normalize_df(ohlcv_1h.df)
    if len(df) < Config.WARMUP_BARS:
        return _no_signal(snapshot, "K线数量不足")
    closes = df["close"]
    rsi_val = calc_rsi(closes)
    macd_val, macd_sig = calc_macd(closes)
    _, bb_upper, bb_lower = calc_bollinger(closes)
    atr_pct = calc_atr_pct(df)
    vol_ratio = calc_volume_ratio(df["vol"])
    ema_trend = calc_ema_trend(closes)
    cvd_trend = calc_cvd(df)
    ns, nr, ds, dr = calc_support_resistance(df, price)
    obv_trend = calc_obv_trend(df)
    obv_div = calc_obv_divergence(df)
    tf4h_dir = calc_tf4h_direction(ohlcv_4h) if ohlcv_4h and not ohlcv_4h.df.empty else None
    regime_result = detect_regime(df, rsi_val)
    adaptive_cfg = ADAPTIVE_CONFIGS[regime_result.regime]
    bb_pos = "下轨" if price <= bb_lower else ("上轨" if price >= bb_upper else "中部")
    confidence, direction, vol_boost, trend_align, tf_boost = score_confidence(rsi_val, macd_val, macd_sig, price, bb_upper, bb_lower, snapshot.funding_rate, snapshot.ls_ratio, atr_pct, vol_ratio, ema_trend, cvd_trend, ds, dr, obv_trend, obv_div, tf4h_dir, adaptive_cfg.min_confidence)
    tp_ratio = adaptive_cfg.tp_ratio
    sl_ratio = adaptive_cfg.sl_ratio
    if direction == "long":
        default_tp = round(price * (1 + tp_ratio), 10)
        default_sl = round(price * (1 - sl_ratio), 10)
        sl, sl_dyn = calc_dynamic_sl("long", price, ns, nr, default_sl)
        tp = default_tp
    elif direction == "short":
        default_tp = round(price * (1 - tp_ratio), 10)
        default_sl = round(price * (1 + sl_ratio), 10)
        sl, sl_dyn = calc_dynamic_sl("short", price, ns, nr, default_sl)
        tp = default_tp
    else:
        tp = 0.0
        sl = 0.0
        sl_dyn = False
    kp = kelly_params or KellyParams()
    sizing = calc_position_sizing(confidence, kp, adaptive_cfg)
    trail = init_trailing_stop(direction or "long", price, adaptive_cfg.trail_activate, adaptive_cfg.trail_retrace)
    score_card = build_score_card(snapshot.symbol, direction, confidence, rsi_val, macd_val - macd_sig, bb_pos, vol_ratio, vol_boost, cvd_trend, obv_trend, obv_div, ds, dr, snapshot.funding_rate, snapshot.ls_ratio, ema_trend, tf4h_dir == direction, tf_boost, regime_result, adaptive_cfg)
    return SignalResult(snapshot.symbol, direction, price, tp, sl, confidence, rsi_val, macd_val - macd_sig, bb_pos, atr_pct, snapshot.funding_rate, snapshot.ls_ratio, vol_ratio, ema_trend, vol_boost, trend_align, cvd_trend, ns, nr, ds, dr, sl_dyn, obv_trend, obv_div, tf4h_dir, tf4h_dir == direction, tf_boost, sizing, trail, regime_result, score_card, adaptive_cfg, True, "")


def analyze_signal(symbol, candles, funding_rate, ls_ratio, kelly_params):
    if not candles:
        return _no_signal(MarketSnapshot(symbol, 0.0, 0.0, funding_rate, ls_ratio), "缺少K线")
    o1h = candles_to_ohlcv(symbol, candles)
    if o1h.df.empty:
        return _no_signal(MarketSnapshot(symbol, 0.0, 0.0, funding_rate, ls_ratio), "K线无效")
    price = float(o1h.df["close"].iloc[-1])
    lookback = min(120, len(o1h.df) - 1)
    base = float(o1h.df["close"].iloc[-1 - lookback]) if lookback > 0 else price
    change_pct_24h = (price / base - 1.0) * 100.0 if base > 0 else 0.0
    snap = MarketSnapshot(symbol, price, change_pct_24h, funding_rate, ls_ratio)
    o4h = aggregate_ohlcv(o1h, Config.HIGHER_TF_GROUP)
    return analyze(snap, o1h, o4h, kelly_params)


def format_signal(r: SignalResult) -> str:
    if not r.passed_filter:
        return f"⚪ {r.symbol} — 跳过（{r.filter_reason}）"
    if r.direction is None:
        return f"⚪ {r.symbol} — 置信度{r.confidence:.0%}，无信号"
    sc = r.score_card
    reg = r.regime_result
    cfg = r.adaptive_cfg
    sz = r.position_sizing
    ts = r.trailing_stop
    emoji = "🟢" if r.direction == "long" else "🔴"
    label = "开多" if r.direction == "long" else "开空"
    return "\n".join(filter(None, [
        f"{'═' * 50}",
        f"{emoji} {r.symbol}  |  {label}  |  置信度 {r.confidence:.0%}",
        f"{'─' * 50}",
        f"📋 综合评分: {sc.overall_score:.0f}/100  {sc.verdict}",
        f"   动量 {sc.momentum_score:.0f} | 量能 {sc.volume_score:.0f} | 结构 {sc.structure_score:.0f} | 情绪 {sc.sentiment_score:.0f} | 趋势 {sc.trend_score:.0f}",
        f"{'─' * 50}",
        f"🌐 市场状态: {reg.regime_label}",
        f"   ADX:{reg.adx:.1f}(+DI:{reg.adx_plus_di:.1f} -DI:{reg.adx_minus_di:.1f})  ATR分位:{reg.atr_percentile:.0%}  价格位置:{reg.price_position:.0%}",
        f"   💡 {reg.strategy_hint}",
        f"{'─' * 50}",
        f"📌 执行方案（{cfg.label}自适应参数）",
        f"   开仓: {r.entry_price}",
        f"   止盈: {r.tp_price}  ({cfg.tp_ratio:.0%})",
        f"   止损: {r.sl_price}  ({cfg.sl_ratio:.0%})  {'🎯动态' if r.sl_dynamic else '📌固定'}",
        f"   移动止盈: 盈利{ts.activate_pct:.1%}激活 → 回撤{ts.trail_pct:.1%}触发",
        f"{'─' * 50}",
        f"⚖️ 仓位: {sz.usdt_amount}U ({sz.used_fraction:.1%})  |  {sz.rationale}",
        f"{'─' * 50}",
        f"📊 指标明细",
        f"   RSI:{r.rsi:.1f}  MACD:{r.macd_diff:+.8f}  布林:{r.bb_position}",
        f"   资金费率:{r.funding_rate:+.5f}  多空比:{r.ls_ratio:.2f}  ATR:{r.atr_pct:.2f}%",
        f"   EMA:{r.ema_trend}  CVD:{r.cvd_trend}  OBV:{r.obv_trend}  {'🔔底背离' if r.obv_divergence == 'bullish' else '⚠️顶背离' if r.obv_divergence == 'bearish' else ''}",
        f"   4h方向:{r.tf4h_direction or '中性'}  {'✅对齐' if r.tf_aligned else '⚡冲突'}{r.tf_boost:+.0%}",
        f"{'─' * 50}",
        (f"✅ 优势: " + " | ".join(sc.strengths)) if sc.strengths else "",
        (f"⚠️ 风险: " + " | ".join(sc.risks)) if sc.risks else "",
        f"{'═' * 50}",
    ]))


class MemeSignalEngineV6:
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
        self.kelly_params = KellyParams(
            win_rate=max(0.01, min(0.99, _f(self.cfg.get("kelly_win_rate", 0.50), 0.50))),
            avg_win_pct=max(0.001, _parse_ratio(self.cfg.get("kelly_avg_win_pct", 0.06), 0.06)),
            avg_loss_pct=max(0.001, _parse_ratio(self.cfg.get("kelly_avg_loss_pct", 0.03), 0.03)),
            max_fraction=max(0.01, _parse_ratio(self.cfg.get("kelly_max_fraction", 0.25), 0.25)),
            min_fraction=max(0.001, _parse_ratio(self.cfg.get("kelly_min_fraction", 0.05), 0.05)),
        )
        self.candles = {}
        self.last_signal_at = {}
        self.last_log_at = {}
        self.message_count = 0
        self.history_count = {}
        self.realtime_count = {}
        self.heartbeat_count = 0

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

    def _throttled_log(self, key, text, interval_sec=30):
        now = time.time()
        last = self.last_log_at.get(key, 0.0)
        if now - last >= interval_sec:
            self.last_log_at[key] = now
            self._log(text)

    def _publish_state(self, r, typ):
        msg = {"type": typ, "strategy_id": self.strategy_id, "owner_id": self.owner_id, "boot_id": self.boot_id, "created_at": _now_iso()}
        r.publish(self._state_ch(), json.dumps(msg, ensure_ascii=False))
        if self.redis_prefix != "qt":
            r.publish(f"qt:state:{self.strategy_id}", json.dumps(msg, ensure_ascii=False))

    def _heartbeat_loop(self, r):
        interval = max(2, _i(self.healthcheck.get("interval_sec", 5), 5))
        while True:
            try:
                self._publish_state(r, "heartbeat")
                self.heartbeat_count += 1
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
        self._log(f"SIGNAL open sym={result.symbol} dir={result.direction} side={side} qty={amount} entry={result.entry_price:.8f} tp={result.tp_price:.8f} sl={result.sl_price:.8f} conf={result.confidence:.4f} score={result.score_card.overall_score:.1f}")
        self._log(f"信号说明 {format_signal(result)}")

    def _push_candle(self, symbol, candle):
        arr = self.candles.get(symbol)
        if arr is None:
            arr = []
            self.candles[symbol] = arr
        arr.append(candle)
        if len(arr) > Config.MAX_BARS:
            del arr[0:len(arr) - Config.MAX_BARS]

    def _funding_rate(self):
        return _f(self.cfg.get("funding_rate", 0.0), 0.0)

    def _ls_ratio(self):
        return max(0.01, _f(self.cfg.get("ls_ratio", 1.0), 1.0))

    def _subscribe_candles(self, receiver):
        receiver.subscribe(self._candle_ch())
        if self.redis_prefix != "qt":
            receiver.subscribe(f"qt:candle:{self.strategy_id}")

    def _handle_payload(self, payload):
        kind = str(payload.get("type", "candle") or "candle").lower()
        if kind == "history":
            symbol = str(payload.get("symbol") or "").strip()
            if symbol and (not self.symbols or symbol in self.symbols):
                candles = payload.get("candles", []) or []
                self.history_count[symbol] = self.history_count.get(symbol, 0) + len(candles)
                for c in candles:
                    self._handle_payload(c)
            return None
        symbol = str(payload.get("symbol") or payload.get("s") or "").strip()
        if not symbol or (self.symbols and symbol not in self.symbols):
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
        self.realtime_count[symbol] = self.realtime_count.get(symbol, 0) + 1
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
        self._log(f"策略启动 strategy_id={self.strategy_id} owner_id={self.owner_id} redis={self.redis_host}:{self.redis_port}/{self.redis_db} prefix={self.redis_prefix} symbols={','.join(self.symbols)} cooldown={self.cooldown_sec}s")
        redis_conn = RedisCompat(self.redis_host, self.redis_port, self.redis_password, self.redis_db).connect()
        self._subscribe_candles(redis_conn)
        ready_msg = {"type": "ready", "strategy_id": self.strategy_id, "owner_id": self.owner_id, "boot_id": self.boot_id, "created_at": _now_iso()}
        redis_conn.publish(self._state_ch(), json.dumps(ready_msg, ensure_ascii=False))
        if self.redis_prefix != "qt":
            redis_conn.publish(f"qt:state:{self.strategy_id}", json.dumps(ready_msg, ensure_ascii=False))
        threading.Thread(target=self._heartbeat_loop, args=(redis_conn,), daemon=True).start()
        while True:
            msg = redis_conn.read_message(timeout=1.0)
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
                if self.log_trace:
                    reason = result.filter_reason or f"无方向 confidence={result.confidence:.4f}"
                    self._throttled_log(f"skip:{symbol}", f"跳过信号 symbol={symbol} reason={reason} bars={len(candles)} regime={result.regime_result.regime.value} score={result.score_card.overall_score:.1f}", 20)
                continue
            self._emit_signal(redis_conn, result)
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
    strategy = MemeSignalEngineV6(cfg)
    strategy.run()
