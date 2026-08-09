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


def calc_trend_confirm_bars(closes: list[float], fast: int, slow: int, confirm_bars: int) -> int:
    """
    返回最近多少根 K 线的 EMA fast/slow 关系与"现在方向"一致。
    用于过滤刚刚翻转的 whipsaw 信号（17.5% 历史胜率主要来自这种短命反向）。

    返回值越大越可靠：
    - 0：刚反转 / 数据不足
    - >= confirm_bars：方向已经持续稳定，允许放行
    """
    if confirm_bars <= 0 or len(closes) < slow + confirm_bars:
        return 0
    fast_series = ema_series(closes, fast)
    slow_series = ema_series(closes, slow)
    if not fast_series or not slow_series:
        return 0
    n = min(len(fast_series), len(slow_series), confirm_bars + 5)
    # 当前方向
    last_dir = 1 if fast_series[-1] > slow_series[-1] else (-1 if fast_series[-1] < slow_series[-1] else 0)
    if last_dir == 0:
        return 0
    count = 0
    for i in range(1, n + 1):
        f = fast_series[-i]; s = slow_series[-i]
        if last_dir == 1 and f > s:
            count += 1
        elif last_dir == -1 and f < s:
            count += 1
        else:
            break
    return count


def is_choppy(closes: list[float], lookback: int = 6) -> bool:
    """
    检测最近 N 根 K 线是不是震荡走势（来回穿越中位）。
    7 天回测显示：<1min 持仓 0% 胜率、12 笔都被秒止损 — 主因之一是开仓时正在震荡的 noise bar。
    """
    if len(closes) < lookback + 1:
        return False
    sub = closes[-lookback - 1:]
    sign_changes = 0
    for i in range(2, len(sub)):
        d1 = sub[i] - sub[i-1]
        d2 = sub[i-1] - sub[i-2]
        if d1 * d2 < 0:
            sign_changes += 1
    # 6 根 K 线里 >= 3 次方向变化 = 震荡
    return sign_changes >= max(3, lookback // 2)


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
    # —— 多空不对称门槛（新增 @2026-06-20）：做空需比做多更高置信度才放行 ——
    # 诊断：两窗口一致显示空头结构性弱于多头——7d long 34单44.1%胜 vs short 51单35.3%胜；
    # 24h long 26单46.2%胜 vs short 11单36.4%胜。空头胜率贴着/低于盈亏平衡，多头明显在其上。
    # 且全部空头早已被 EMA-down + TREND_CONFIRM_BARS=5 + RSI<32拒 三重确认过，仍 35%胜——
    # meme 标的空头被逼空(squeeze)秒反弹是 <1m whipsaw 桶(7d 15单13%胜 -$2.58)的记录在案根因。
    # 对策：不一刀切禁空(allowed_sides 钝且两天前刚 toggle 过、会丢掉真跌段利润)，而是给空头
    # 加一道置信度溢价——空头需 ss >= MIN_CONFIDENCE + SHORT_CONF_PREMIUM 才放行，多头不变。
    # 砍掉贴门槛的边际空单(churn/fee_drag 主因)，保留强信念空单(funding/RSI 0.20权重确认的真跌段)。
    # 经 _load_config 接 config short_conf_premium，便于后续微调。
    SHORT_CONF_PREMIUM = 0.08      # 做空置信度溢价：做空门槛 = MIN_CONFIDENCE + 此值
    # —— 多头置信度溢价（新增 @2026-06-20 晚）：对称地给多头也加一道门槛 ——
    # 诊断（live PnL，三窗口一致）：多头在每一个时间窗都净亏——6h long 4单全亏 -$0.92；
    # 24h long 25单 wr40% -$0.53；7d long 38单 wr39.5% -$0.60。而空头自上午加了
    # SHORT_CONF_PREMIUM 后已被收敛成盈利侧——7d short 53单 wr41.5% +$3.40（远好于当初
    # 诊断写的 35.3%胜，说明溢价砍掉弱空单后留下的强空单是赚的，机制有效）。
    # 问题：唯一的方向纪律(溢价)加在了 live 上盈利的空头侧，而持续亏损的多头侧零纪律放任。
    # 多头 wr(39.5%)≈空头(41.5%)但净亏，说明多头亏单幅度>赢单=贴门槛的弱多单 churn 拉低 EV，
    # 也是 24h fee_drag 170% 的推手之一。对策：对称给多头加溢价，多头门槛=MIN_CONFIDENCE+此值，
    # 砍掉 0.51~0.57 区间的边际弱多单，只留 EMA-up+多因子确认的强多单。用 premium 而非禁多
    # (allowed_sides)，保持 regime 反转(转涨)时强多单仍可放行，避免一刀切的方向豪赌。
    # 取 0.06 < 空头 0.08：本轮先温和验证，6h 复盘看 long net/trade_count 再决定是否加码。
    # 经 _load_config 接 config long_conf_premium，便于后续微调。
    LONG_CONF_PREMIUM = 0.14       # 0.06→0.14 @2026-07-16 S12: 做多门槛=MIN_CONFIDENCE+此值(空头不加)。三窗口多头持续净亏: 168h 15单wr26.7% -$4.57 / 24h wr28.6% -$4.21 / 6h 7单0胜 -$3.70; 空头全盈利(+$6.24/+$5.85/+$1.83)。0.14使long_thr=0.32+0.14=0.46, 精准砍0.45分的「EMA翻多0.30+MACD0.15」裸熊市反弹单(trend_confirm_bars被config降至3后EMA短暂翻多即触发), 保留0.55+放量/多因子强多单, regime真转涨仍可放行。接config long_conf_premium
    TP_RATIO = 0.06                # 固定止盈兜底（ATR 不足时使用）
    SL_RATIO = 0.03                # 固定止损兜底（ATR 不足时使用）
    ATR_TP_MULT = 3.0              # 动态止盈 = ATR × 倍数（2.0→3.0：24h 42单 胜率30.95% vs 盈亏平衡38.83% edge -7.87pp，fee_drag 44%——赢单太小被手续费吃光。持仓分布显示 15-60m 是唯一盈利区(+$1.13)，2×ATR 止盈过早封顶让赢单跑不到该区间。放大止盈到3×ATR 放大赢单、摊薄手续费占比、把名义R:R 1.33→2.0 拉低盈亏平衡门槛，止损仍守1.5×ATR 容忍亏）
    ATR_SL_MULT = 1.5              # 动态止损 = ATR × 倍数（1.0→1.5：1-5m持仓 12单0胜 -$3.42 是最大出血，1×ATR止损在初始噪音中过早被打，放宽至1.5×ATR 让单子活到唯一盈利的15-60m持仓区间）
    ATR_DISCOUNT_THR = 1.5         # ATR% 低于此值：低波动折扣
    ATR_DISCOUNT = 0.7             # 低波动折扣系数
    ATR_HIGH_THR = 5.0             # ATR% 高于此值：高波动保护
    ATR_HIGH_DISCOUNT = 0.6        # 高波动折扣系数
    VOLUME_RATIO_MIN = 1.2         # 成交量确认：当前量需 > 均量 × 此倍数（软加分门槛）
    HARD_VOLUME_FLOOR = 1.0        # 硬性放量地板(入场量需≥20根均量×此值否则拒)。演化: 1.0→1.2(06-13 S4)→1.4(06-25 S6, 压<5m whipsaw/fee_drag)→1.2(07-03 S9, 修复4日零成交饥饿)→1.0(07-06 S10, S9后仅2单仍饥饿; 1.0~1.2边际单由后加的RSI极端拒/MAX_ENTRY_EXT/MIN_ATR地板/2.5×ATR SL接住)。故意不接config防回退覆盖。若<1m桶/fee_drag复燃→回1.2
    # —— 反过度延伸地板（新增 @2026-06-13 S4）：拒绝在 RSI 极端区逆冲入场 ——
    # 诊断：18单全是 SHORT，<1m 持仓桶 8单仅12.5%胜 -$3.10 独占全部净亏，而 15-60m 桶
    # 75%胜 +$1.45 —— 边际可见 edge 出现在“活过第一分钟”之后。空头分(ss)可由
    # EMA向下0.30+MACD0.15+资金费率0.20+多空比0.15 凑到 0.80 过门槛，全程不需要任何
    # 超买确认 → 极易在 RSI 已深度超卖(价格已砸穿)时做空，随即均值回归反弹秒打止损=<1m whipsaw。
    # 前序周期已把 量地板/趋势确认/EMA权重 拉满仍挡不住，因为它们管“趋势成型”不管“入场位置过度延伸”。
    # 对策：新增极端区硬拒——RSI<32 不准做空(砸到底反弹)，RSI>68 不准做多(冲到顶回落)。
    # 只卡明确的衰竭/反转区，趋势中段(RSI~40-60)的盈利单不受影响，故不会误杀 15-60m 赢单。
    # 故意不接入 apply_config，防止 config 覆盖回退（与 HARD_VOLUME_FLOOR 同策略）。
    RSI_SHORT_REJECT_BELOW = 32.0  # RSI 低于此值时拒绝做空（已超卖，反弹 whipsaw 高发区）
    RSI_LONG_REJECT_ABOVE = 68.0   # RSI 高于此值时拒绝做多（已超买，回落 whipsaw 高发区）
    EMA_FAST = 20                  # EMA 趋势快线周期
    EMA_SLOW = 60                  # EMA 趋势慢线周期
    CHANGE_LOOKBACK_BARS = 200
    VOL_LOOKBACK_BARS = 200
    COOLDOWN_SEC = 300             # 默认冷却 300 秒，避免频繁开仓
    MAX_BARS = 400                 # 增加缓存上限，保证 EMA60 计算充分
    WARMUP_BARS = 80               # 提高预热要求，保证 EMA60 稳定
    # —— 基于 7 天实盘回测（胜率 17.5%）新增的硬过滤 ——
    TREND_CONFIRM_BARS = 5         # EMA fast/slow 方向必须已稳定的 K 线数，过滤 whipsaw（3→5：<1m秒反转单 0胜 是最大出血点，要求趋势成型更久再进场）
    CHOP_LOOKBACK = 6              # 震荡判定回看的 K 线数
    REJECT_ON_CHOP = True          # 震荡时不开仓
    MAX_ATR_PCT = 8.0              # ATR% > 此值时拒绝开仓（极端波动）
    MIN_ATR_PCT_FOR_TRADE = 0.9    # ATR%低于此值拒绝开仓(死水行情SL距离过窄被噪音秒打=<1m whipsaw)。演化: 0.15→0.5(06-14, <1m+1-5m桶占7d净亏70%)→0.9(06-27 S7, 0.5~0.9死水带仍coin-flip, 只压fee零机会成本)。接config min_atr_pct_for_trade(当前config已设0.2覆盖此默认)
    # —— 反追高/追空地板（新增 @2026-06-23）：入场价离 EMA_FAST 过远即拒 ——
    # 诊断（7d n=179，可靠样本）：hold_distribution 单调揭示 <1m 桶 26单wr11.5% + 1-5m 桶
    # 38单wr23.7% = 64单(占成交36%)wr~18% 是结构性出血源，而 >5m 的单 wr 47-66% 盈利。
    # 若剔除 <5m whipsaw，剩余单 wr 升至 ~46% > 盈亏平衡 42% → EV 翻正。前序周期已叠加
    # MIN_ATR地板/趋势确认5根/量地板/RSI极端拒/震荡拒/双向溢价 六道入场滤，仍挡不住该桶——
    # 因为没有一道检查"入场价相对趋势的位置"。机制：动量信号(EMA+MACD+量)全在拉升确认后
    # 才齐备 → 入场点聚集在局部顶/底(衰竭区)，价随即均值回归秒打 SL=1.9×ATR 止损=<1m whipsaw。
    # 关键不等式：止损距离=ATR_SL_MULT×ATR=1.9×ATR；若入场时价已偏离 EMA_FAST > 1.9×ATR，
    # 则仅仅回归到均线就足以击穿止损=结构性必损单。对策：拒绝顺势方向上偏离 EMA_FAST
    # 超过此倍数的"追单"，只卡过度延伸，回调入场(价在均线附近/穿过均线)不受影响→不误杀盈利单。
    # 取 2.0(略高于 SL 倍数 1.9)：只砍"回归即爆仓"的极端追高/追空。故意不接 apply_config 防回退覆盖。
    MAX_ENTRY_EXT_ATR = 2.0        # 1.2→2.0 @2026-07-02 S8: 该门自述不变式=入场偏离<SL倍数(回归至EMA不至单独打穿SL)。当初写此门时SL=1.9×ATR故取1.2；但 atr_sl_mult 已扩至2.5×ATR(config)，而本门从冻在1.2未随SL同步→与自述不变式脱钩、过度从紧。病灶：06-30起3天零0单(daily_pnl_7d),连续4次config放松(min_conf 0.47→0.42/max_atr 6→7)均未重启成交——因本门不接config无法被那些旋钮触及。动量入场在TREND_CONFIRM_BARS=5确认+放量后价已自然偏离EMA_FAST>1.2×ATR，1.2几乎拒掉所有顺势入场。重设2.0=恢复不变式：入场最多偏离2.0×ATR，回归至EMA后距SL(2.5×ATR)仍留0.5×ATR缓冲，既重开顺势漏斗又保留"回归单独不爆仓"保护。仍故意不接 apply_config 防回退覆盖
    # —— 市场宽度regime门 @2026-07-17 S13: 现有7道硬过滤全看单币, 无一看大盘。meme与大盘beta极高,
    # 全池齐跌时单币"EMA翻多+放量"=下跌中继接刀。breadth=池内EMA_FAST>EMA_SLOW币占比(现有feed免费算),
    # <0.30拒多/>0.70拒空, 中间带不干预(是veto非信号源)。可评估币<5时门失效; 60s缓存防burst重算。
    # 三值接config(breadth_min_for_long/breadth_max_for_short/breadth_min_symbols), min=0或max=1停用。
    BREADTH_MIN_FOR_LONG = 0.30    # 宽度低于此拒多; 0=停用
    BREADTH_MAX_FOR_SHORT = 0.70   # 宽度高于此拒空; 1=停用
    BREADTH_MIN_SYMBOLS = 5        # 可评估币数低于此门失效
    # —— per-symbol 亏损熔断器(影子跟单) @2026-07-18 S14 ——
    # 病灶: 动态轮换池不断送入新亏损币(24h TOSHI 5笔-$12全败/HOME -$6.6/DODOX -$5.6),
    # 静态blacklist永远在追杀昨天的亏损币, 轮换每天送进新的。执行在平台闭源侧,
    # 策略进程收不到成交回报 → 影子跟单: _emit_signal后自记(entry/tp/sl/ts),
    # 用该币后续K线自判虚拟胜负(_update_shadow)。全局仅1个仓位槽, 发出的信号未必
    # 真实成交 → 这是信号质量熔断而非真实PnL熔断, 偏差已知且接受, 勿试图"修复"。
    # 连败N次 → 隔离M分钟不再对该币发信号(熔断gate在方向确认后、宽度门之前)。
    CB_ENABLED = True              # 熔断总开关; 接config cb_enabled
    CB_CONSEC_LOSSES = 3           # 连续N次影子亏损触发熔断; 接config cb_consec_losses
    CB_QUARANTINE_MIN = 240        # 熔断隔离分钟数; 接config cb_quarantine_min
    MAX_HOLD_MINUTES = 60          # 影子单超时(分), 对齐平台强平时长; 接config max_hold_minutes
    # —— 跨币择优开仓(batch best-pick) @2026-07-18 S16 ——
    # 病灶: 信号先到先得。同一分钟多币过gate时谁占唯一仓位槽由平台推流顺序(≈字母序)
    # 决定, 与信号质量无关; 且落选币在被平台槽位丢弃前已被盖冷却戳, 高置信信号
    # 被"槽位+冷却"双重锁死30min。机制: 过gate候选先入单元素缓冲, 自首个候选起
    # BEST_PICK_WINDOW_SEC内攒批(1m K线同批到达通常<2s), 到点只对最高置信度者
    # 盖冷却戳+发信号; 落选者不盖冷却戳, 下一根K线可再竞争。flush挂主循环每次
    # 迭代(IDLE poll也触发), 最坏发出延迟≈窗口+一次poll间隔, 对1m级信号无实质影响。
    BEST_PICK_ENABLED = True       # 择优总开关; 接config best_pick_enabled, false=回到先到先得
    BEST_PICK_WINDOW_SEC = 5.0     # 攒批窗口秒; 接config best_pick_window_sec, clamp[1,30]


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

    # EMA 趋势一致性（权重 0.20→0.30：放大趋势对齐，滤掉逆势whipsaw噪音）
    if ema_trend == "up":
        ls += 0.30
    elif ema_trend == "down":
        ss += 0.30

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

    # EMA 趋势一致性（权重 0.20→0.30：放大趋势对齐。实盘 <1m 持仓 12单胜率16.7% -$2.11 是逆势whipsaw噪音占半数全亏，而存活>1m 的趋势单 1-15m 胜率37.5-40% 盈利。提高EMA权重让趋势对齐信号更易过MIN_CONFIDENCE门槛、逆势噪音信号跌破门槛被滤掉）
    if ema_trend == "up":
        ls += 0.30
        ls_parts.append("EMA趋势向上:+0.30")
    elif ema_trend == "down":
        ss += 0.30
        ss_parts.append("EMA趋势向下:+0.30")

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

    # —— 2026-07-13 S11：撤销双向置信度溢价，入场门槛还原到 base MIN_CONFIDENCE ——
    # 病灶：daily_pnl_7d 显示 07-08~07-13 连续6日 trades=0，策略 status=running 但完全休眠；
    # paired_trades 168h 仅3配对全部聚集在 07-06/07（已 stale），24h/6h 零成交零 income 事件。
    # 关键推断：前序周期反复只调「下游硬 gate」——HARD_VOLUME_FLOOR 1.4→1.2→1.0(S9/S10)、
    # MAX_ENTRY_EXT 1.2→2.0(S8)、MIN_ATR/max_atr/min_conf/symbols——均未durably重启成交
    # (S10 后仅 07-07 冒 2 单又归零)。原因：得分需先过 score≥MIN_CONFIDENCE+premium 这道
    # 「上游」门槛才轮到那些下游硬 gate；信号在上游就被拒，调下游 gate 自然无效。
    # 而上游门槛是唯一从未被放松的轴：双向溢价(LONG0.06/SHORT0.08，06-20 新增只增不减)叠加在
    # 已近地板的 MIN_CONFIDENCE(0.35，floor0.30 仅剩0.05空间)之上，再乘低波折扣×0.8(07-12 刚收紧
    # atr_discount_thr→1.0)——典型低波alt有效门槛=(0.35+0.06)/0.8≈0.51，EMA对齐+单一确认的
    # 干净趋势单(0.45)恒被拒。撤销溢价把 long/short 门槛统一还原到 0.35：低波下 EMA(0.30)+MACD(0.15)
    # =0.45×0.8=0.36≥0.35 由「拒」翻「过」，直接重开被误杀的趋势单漏斗。
    # 安全性：溢价是「上游」加分门槛，撤销它不碰任何「下游」反whipsaw硬 gate——ATR带[0.5,7]/趋势稳3根/
    # 放量地板1.0/RSI极端拒(32/68)/反追高2.0×ATR 全部在岗，接住弱信念秒反转单，防 fee-bleed 复燃。
    # 单变量、code-only(config 无法回填此比较逻辑)、易回滚。6h 复核：trade_count>0？若仍0→墙在更上游
    # (信号生成/数据/plumbing)而非门槛，应转查决策日志/行情feed 而非继续放松；若成交但 <1m桶/fee_drag
    # 复燃→按方向证据非对称重加溢价(优先只对亏损侧)。
    # —— 2026-07-16 S12: 按S11预案「按方向证据非对称重加溢价(优先只对亏损侧)」执行。
    # S11撤双溢价已成功重启成交(24h 38配对), 但出血100%集中多头(见LONG_CONF_PREMIUM注释)。
    # 只抬多头门槛, 空头保持base不动, 不碰下游硬gate, 单变量易回滚。
    # —— 2026-07-21 E2: S12的「空头免溢价」是当时空盈多亏的产物, regime已反转——
    # 空头五窗全负(1h -0.89/3h -0.77/6h -1.53 wr20%/12h -1.47 wr30.4%/24h -5.99 wr34.1%)
    # 而多头12h wr56.3%净正, 亏损侧门槛(0.55)反而低于盈利侧(0.60)。修复: short_thr
    # 恢复吃 SHORT_CONF_PREMIUM(接config short_conf_premium, 现值0.05→生效0.60, 与long对齐)。
    # detail 上报的 short_threshold 一直按 base+premium 计算, 此修复同时消除
    # 「上报门槛≠真实gate」的观测偏差。单变量, 回滚=本行退回 base。
    long_thr = Config.MIN_CONFIDENCE + Config.LONG_CONF_PREMIUM
    short_thr = Config.MIN_CONFIDENCE + Config.SHORT_CONF_PREMIUM
    direction: Optional[str] = None
    if ls >= long_thr:
        direction = "long"
        conf = ls
    elif ss >= short_thr:
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
        "short_conf_premium": float(Config.SHORT_CONF_PREMIUM),
        "short_threshold": float(Config.MIN_CONFIDENCE + Config.SHORT_CONF_PREMIUM),
        "long_conf_premium": float(Config.LONG_CONF_PREMIUM),
        "long_threshold": float(Config.MIN_CONFIDENCE + Config.LONG_CONF_PREMIUM),
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
        "short_conf_premium": float(Config.SHORT_CONF_PREMIUM),
        "short_threshold": float(Config.MIN_CONFIDENCE + Config.SHORT_CONF_PREMIUM),
        "long_conf_premium": float(Config.LONG_CONF_PREMIUM),
        "long_threshold": float(Config.MIN_CONFIDENCE + Config.LONG_CONF_PREMIUM),
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

    # —— 基于历史回测加的硬过滤（在得分通过 MIN_CONFIDENCE 后还要再守一道）——
    reject_reason: Optional[str] = None
    if direction is not None:
        # 1) 极端波动两端：太死或太疯都拒
        if atr_pct > Config.MAX_ATR_PCT:
            reject_reason = f"ATR%过高({atr_pct:.2f}>{Config.MAX_ATR_PCT})"
        elif atr_pct < Config.MIN_ATR_PCT_FOR_TRADE:
            reject_reason = f"ATR%过低({atr_pct:.2f}<{Config.MIN_ATR_PCT_FOR_TRADE})"
        # 2) EMA fast/slow 关系刚翻转：whipsaw 主要来源
        elif Config.TREND_CONFIRM_BARS > 0:
            stable = calc_trend_confirm_bars(closes, Config.EMA_FAST, Config.EMA_SLOW, Config.TREND_CONFIRM_BARS)
            if stable < Config.TREND_CONFIRM_BARS:
                reject_reason = f"趋势未稳定({stable}<{Config.TREND_CONFIRM_BARS})"
            else:
                # 方向必须和当下 EMA 趋势一致（不允许逆趋势）
                if direction == "long" and ema_trend != "up":
                    reject_reason = "做多但EMA非up"
                elif direction == "short" and ema_trend != "down":
                    reject_reason = "做空但EMA非down"
        # 3) 震荡过滤
        if reject_reason is None and Config.REJECT_ON_CHOP and is_choppy(closes, Config.CHOP_LOOKBACK):
            reject_reason = "震荡走势"
        # 4) 硬性放量地板：成交量不足均量×1.2 的入场是 <1m 假突破 whipsaw 噪音主因，直接拒
        #    守卫 (volumes and len>=21)：仅在有足量样本算出真实 volume_ratio 时才拒，
        #    避免量数据缺失/不足时 calc_volume_ratio 兜底=1.0 在 1.2 地板下被误杀
        if reject_reason is None and volumes and len(volumes) >= 21 and volume_ratio < Config.HARD_VOLUME_FLOOR:
            reject_reason = f"放量不足({volume_ratio:.2f}<{Config.HARD_VOLUME_FLOOR})"
        # 5) 反过度延伸地板：极端 RSI 区逆冲入场是 <1m 均值回归反弹 whipsaw 主因，直接拒
        #    只卡衰竭/反转极端区(RSI<32 做空 / RSI>68 做多)，趋势中段盈利单不受影响
        if reject_reason is None and direction == "short" and rsi_val < Config.RSI_SHORT_REJECT_BELOW:
            reject_reason = f"做空但RSI已超卖({rsi_val:.1f}<{Config.RSI_SHORT_REJECT_BELOW})"
        elif reject_reason is None and direction == "long" and rsi_val > Config.RSI_LONG_REJECT_ABOVE:
            reject_reason = f"做多但RSI已超买({rsi_val:.1f}>{Config.RSI_LONG_REJECT_ABOVE})"
        # 6) 反追高/追空地板：入场价离 EMA_FAST 超过 MAX_ENTRY_EXT_ATR×ATR=追单，回归即击穿1.9×ATR止损
        #    只卡顺势方向的过度延伸(long且价高于均线 / short且价低于均线)；回调入场(价穿过均线侧)放行
        if reject_reason is None and Config.MAX_ENTRY_EXT_ATR > 0 and atr_abs > 0:
            _ema_fast_series = ema_series(closes, Config.EMA_FAST)
            _ema_fast_val = _ema_fast_series[-1] if _ema_fast_series else 0.0
            if _ema_fast_val > 0:
                _ext = abs(price - _ema_fast_val) / atr_abs
                if direction == "long" and price > _ema_fast_val and _ext > Config.MAX_ENTRY_EXT_ATR:
                    reject_reason = f"追高入场(偏离EMA{Config.EMA_FAST} {_ext:.2f}×ATR>{Config.MAX_ENTRY_EXT_ATR})"
                elif direction == "short" and price < _ema_fast_val and _ext > Config.MAX_ENTRY_EXT_ATR:
                    reject_reason = f"追空入场(偏离EMA{Config.EMA_FAST} {_ext:.2f}×ATR>{Config.MAX_ENTRY_EXT_ATR})"
        # 7) 闪跌穿刺入场veto  # S18 @2026-08-03 闪跌穿刺veto
        #    机制: 微型币放量冲顶后闪跌, 高置信long在短程下行动量中入场——S8不变式仅留0.5×ATR缓冲
        #    (入场偏EMA≤2.0 vs SL2.5), 且量能地板把砸盘量计为放量, 跌势起步反而过量能门 →
        #    入场即被2.5×ATR SL穿刺(<20m)。48h实证 n=8 net−18.79 全高置信mult1.19-1.45(conf零判别力)。
        #    以已完成bar短程下行动量拒long: (a)上一完成bar单bar跌幅>0.6×ATR% (b)3bar累计跌幅>1.0×ATR%。
        #    冲顶亚型(c: ext>1.5×ATR∧末bar红)首发故意不含, 防误杀顺势胜单(ex-穿刺wr65.6%)。
        if reject_reason is None and direction == "long" and atr_pct > 0 and len(closes) >= 4:
            _c0, _c1, _c3 = closes[-1], closes[-2], closes[-4]
            _drop1 = (_c1 - _c0) / _c1 * 100.0 if _c1 > 0 else 0.0
            _drop3 = (_c3 - _c0) / _c3 * 100.0 if _c3 > 0 else 0.0
            if _drop1 > 0.6 * atr_pct:
                reject_reason = f"闪跌veto(1bar跌{_drop1:.2f}%>0.6×ATR{atr_pct:.2f}%)"
            elif _drop3 > 1.0 * atr_pct:
                reject_reason = f"闪跌veto(3bar累跌{_drop3:.2f}%>1.0×ATR{atr_pct:.2f}%)"
        # 7b) 顺涨开空veto  # S19 @2026-08-06 短侧顺涨开空veto(S18镜像)
        #    机制: S18的短侧镜像——meme反弹/拉升续航中short在短程上行动量中入场(逆势空),
        #    上涨延续即被2.5×ATR SL向上穿刺(hold 6-31m)。解锁段(08-05 17:45Z起)实证 n=9
        #    net−16.7(mv−3.5~−4.2%≈2.5×ATR快SL), ex穿刺段毛+6.5=病灶特异性强。
        #    以已完成bar短程上行动量拒short: (a)上一完成bar单bar涨幅>0.6×ATR%
        #    (b)3bar累计涨幅>1.0×ATR%。阈值与S18对称、内联不接config防回退覆盖;
        #    慢磨型短亏(hold≥45m小额)非本veto目标, 由饥饿/硬超时层处置。
        if reject_reason is None and direction == "short" and atr_pct > 0 and len(closes) >= 4:
            _c0b, _c1b, _c3b = closes[-1], closes[-2], closes[-4]
            _rise1 = (_c0b - _c1b) / _c1b * 100.0 if _c1b > 0 else 0.0
            _rise3 = (_c0b - _c3b) / _c3b * 100.0 if _c3b > 0 else 0.0
            if _rise1 > 0.6 * atr_pct:
                reject_reason = f"顺涨拒空(1bar涨{_rise1:.2f}%>0.6×ATR{atr_pct:.2f}%)"
            elif _rise3 > 1.0 * atr_pct:
                reject_reason = f"顺涨拒空(3bar累涨{_rise3:.2f}%>1.0×ATR{atr_pct:.2f}%)"
        # 7c) 拉升语境拒空  # S20 @2026-08-06 多小时拉升语境拒空(S19盲区补层)
        #    机制: 7b只读closes[-1..-4], 对3-5h尺度meme拉升的中继横盘物理盲区——
        #    数小时+17~+52%拉升的整理段里bar级动量静默(ZBT案60m仅+0.02%), 而短评分
        #    (RSI>65/资金费率/多空比/EMA down)恰在此际打高空分, 入场后拉升续腿即被
        #    2.5×ATR SL快速穿刺(hold 3-32m)。postR2实证(CoinGecko 5m重建@08-06):
        #    快SL空单3例入场时300m涨幅全≥+17.6%(CTSI+37.7/TAKE+52.4/ZBT+17.6),
        #    可证对照组(慢亏型BTW/TAKE二入)≤+2.3%, 分离度~7.7×。TAKE案180m=−3.81%
        #    (顶部回撤2h仍死于续腿)⇒主窗必须300m; 180m辅窗抓更快的3h尺度拉升。
        #    绝对%阈值(不×ATR): 拉升语境是小时级绝对现象, 与1m bar ATR尺度无关。
        #    平台start回灌history(~400根,MAX_BARS=400)⇒重启后即时全功率; 缓冲不足跳过。
        if reject_reason is None and direction == "short":
            _nb = len(closes)
            _c300 = closes[-301] if _nb >= 301 else 0.0
            _c180 = closes[-181] if _nb >= 181 else 0.0
            _chg300 = (closes[-1] / _c300 - 1.0) * 100.0 if _c300 > 0 else None
            _chg180 = (closes[-1] / _c180 - 1.0) * 100.0 if _c180 > 0 else None
            if _chg300 is not None and _chg300 >= 12.0:
                reject_reason = f"拉升语境拒空(300m涨{_chg300:.1f}%≥12%)"
            elif _chg180 is not None and _chg180 >= 9.0:
                reject_reason = f"拉升语境拒空(180m涨{_chg180:.1f}%≥9%)"

    if reject_reason is not None:
        # 触发了硬过滤：放弃方向，置 confidence 为 0 让上层日志能看到
        detail = dict(detail)
        detail["hard_filter_reject"] = reject_reason
        direction = None
        confidence = 0.0

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
        elif isinstance(raw_syms, str):
            # 修复(2026-07-13)：config 里 symbols 是逗号分隔字符串(非 JSON 数组)，旧代码只认 list，
            # 于是 self.symbols 恒为空 → 回退到单一 self.symbol=BTC/USDT → BTC 价被 max_price 挡下 →
            # 07-08 那次 PATCH 触发 stop+start 重载 config 后连日零成交。此处按逗号/分号切分，
            # 兼容 "A/USDT,B/USDT" 写法，恢复既定的多标的 alt 池。
            for s in raw_syms.replace(";", ",").split(","):
                if s.strip():
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
        self._breadth_cache: tuple[float, float, int] = (0.0, 1.0, 0)  # (ts, 宽度, 可评估币数)

        self.closes: dict[str, list[float]] = {s: [] for s in self.symbols}
        self.highs: dict[str, list[float]] = {s: [] for s in self.symbols}
        self.lows: dict[str, list[float]] = {s: [] for s in self.symbols}
        self.volumes: dict[str, list[float]] = {s: [] for s in self.symbols}
        # 动态币池(2026-07-18)：config.symbols 仅作初始种子集，运行中以平台实际推流为准。
        # 平台热移除某币时只是停止推流，不另行通知，靠 last_seen 超时清理释放状态。
        self.last_seen_ts: dict[str, float] = {s: time.time() for s in self.symbols}
        self._last_purge_ts: float = time.time()
        # 熔断器状态: shadow_open 随 purge 清理; consec_loss/quarantine_until 跨轮换
        # 保留(同 last_signal_ts 语义: 币被移除又换回时隔离必须延续, 否则轮换绕过熔断)。
        self.shadow_open: dict[str, dict] = {}
        self.consec_loss: dict[str, int] = {}
        self.quarantine_until: dict[str, float] = {}
        # S21 @2026-08-07 影子最近平仓记忆(急再入veto用): 特意不入purge元组, 跨purge保留(同consec_loss语义)。
        self.last_shadow_close: dict[str, dict] = {}
        # 择优攒批(S16): 单元素缓冲只留当前批最高置信度候选; 内存态不跨重启,
        # 重启丢一个未flush候选最多少发一单, 可接受。
        self.best_pick: Optional[dict] = None
        self.best_pick_deadline: float = 0.0
        # —— 观测计数器(S15 @2026-07-18): 纯观测, 不碰任何交易路径 ——
        # 日志接口只回最近100条(评估噪音下约1分钟), 逐事件行等于不可观测。
        # 自维护窗口计数器+周期一行"统计汇总", cron 只 grep 汇总行。
        # 影子胜负按方向细分: 平台在闭源侧丢弃 sell 信号(allowed_sides=buy-only),
        # 空头影子胜负 = 被丢弃空单的假想表现, 是 E5(恢复双向)的唯一数据源。
        self.stats: dict[str, int] = {}
        self._last_stats_ts: float = time.time()
        # S23 @2026-08-09 逆bar入场影子观测(enforcement前置审计, #30误杀审计先行教训)。
        # 病灶: 48h首腿快SL L20/-37.62 + S14/-26.46 = 主血; 候选机制"入场bar动量确认"
        # 无法退化审计(容器无1m bar历史), 批内同向节流已被数据证伪(死亡组前10m同向聚簇0.24
        # =盈利组0.24零分离, 节流静态审计误杀45-60%远超10%设计门)。
        # 本锚点纯计数零交易路径: emit时标bar向(A=逆bar/W=顺bar), 影子平仓按 方向×胜负 累计,
        # 统计行输出累计值(不随窗清零; restart归零=干净分段)。A组n≥20且wr分离足够才升enforcement。
        self.s23c: dict[str, int] = {}

        host, port = (self.redis_addr.split(":") + ["6379"])[:2]
        self.sub = MiniRedis(host=host, port=int(port), password=self.redis_password, db=self.redis_db).connect()
        self.pub = MiniRedis(host=host, port=int(port), password=self.redis_password, db=self.redis_db).connect()

        self._load_config()

    def _load_config(self):
        Config.MIN_CONFIDENCE = _parse_ratio(self.cfg.get("min_confidence"), Config.MIN_CONFIDENCE)
        Config.SHORT_CONF_PREMIUM = max(0.0, _f(self.cfg.get("short_conf_premium"), Config.SHORT_CONF_PREMIUM))
        Config.LONG_CONF_PREMIUM = max(0.0, _f(self.cfg.get("long_conf_premium"), Config.LONG_CONF_PREMIUM))
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
        # —— 历史回测新增的硬过滤旋钮 ——
        Config.TREND_CONFIRM_BARS = max(0, _i(self.cfg.get("trend_confirm_bars"), Config.TREND_CONFIRM_BARS))
        Config.CHOP_LOOKBACK = max(2, _i(self.cfg.get("chop_lookback"), Config.CHOP_LOOKBACK))
        Config.REJECT_ON_CHOP = bool(self.cfg.get("reject_on_chop", Config.REJECT_ON_CHOP))
        Config.MAX_ATR_PCT = max(0.0, _f(self.cfg.get("max_atr_pct"), Config.MAX_ATR_PCT))
        Config.MIN_ATR_PCT_FOR_TRADE = max(0.0, _f(self.cfg.get("min_atr_pct_for_trade"), Config.MIN_ATR_PCT_FOR_TRADE))
        Config.MAX_PRICE = max(0.0, _f(self.cfg.get("max_price"), Config.MAX_PRICE))
        Config.MIN_PRECISION = max(0, _i(self.cfg.get("min_precision"), Config.MIN_PRECISION))
        Config.MIN_VOLATILITY = max(0.0, _f(self.cfg.get("min_volatility"), Config.MIN_VOLATILITY))
        Config.BREADTH_MIN_FOR_LONG = min(1.0, max(0.0, _f(self.cfg.get("breadth_min_for_long"), Config.BREADTH_MIN_FOR_LONG)))
        Config.BREADTH_MAX_FOR_SHORT = min(1.0, max(0.0, _f(self.cfg.get("breadth_max_for_short"), Config.BREADTH_MAX_FOR_SHORT)))
        Config.BREADTH_MIN_SYMBOLS = max(2, _i(self.cfg.get("breadth_min_symbols"), Config.BREADTH_MIN_SYMBOLS))
        Config.CB_ENABLED = bool(self.cfg.get("cb_enabled", Config.CB_ENABLED))
        Config.CB_CONSEC_LOSSES = max(2, _i(self.cfg.get("cb_consec_losses"), Config.CB_CONSEC_LOSSES))
        Config.CB_QUARANTINE_MIN = max(30, _i(self.cfg.get("cb_quarantine_min"), Config.CB_QUARANTINE_MIN))
        Config.MAX_HOLD_MINUTES = max(5, _i(self.cfg.get("max_hold_minutes"), Config.MAX_HOLD_MINUTES))
        Config.BEST_PICK_ENABLED = bool(self.cfg.get("best_pick_enabled", Config.BEST_PICK_ENABLED))
        Config.BEST_PICK_WINDOW_SEC = min(30.0, max(1.0, _f(self.cfg.get("best_pick_window_sec"), Config.BEST_PICK_WINDOW_SEC)))
        # symbol_blacklist 在策略层生效（见 _symbol_denied）；接受 list 或逗号分隔字符串。
        raw_bl = self.cfg.get("symbol_blacklist") or []
        if isinstance(raw_bl, str):
            raw_bl = raw_bl.replace(";", ",").split(",")
        try:
            self.symbol_denylist = {str(x).replace("/", "").strip().upper() for x in raw_bl if str(x).strip()}
        except Exception:
            self.symbol_denylist = set()
        self.trace = bool(self.cfg.get("log_trace") or self.cfg.get("debug"))
        self.log_rx = bool(self.cfg.get("log_rx", True))
        self.log_decisions = bool(self.cfg.get("log_decisions", True))
        self.log_every = max(1, _i(self.cfg.get("log_every"), 60))
        self.log_idle_sec = max(5, _i(self.cfg.get("log_idle_sec"), 30))
        # 动态币池：某币连续 N 秒无 K 线则清理其行情状态，0=不清理；下限 300 防误配置抖动。
        _purge = _i(self.cfg.get("symbol_idle_purge_sec"), 1800)
        self.symbol_idle_purge_sec = 0 if _purge <= 0 else max(300, _purge)
        # 统计汇总间隔(秒), 0=关, 下限60; 接config stats_log_interval_sec。
        _si = _i(self.cfg.get("stats_log_interval_sec"), 600)
        self.stats_log_interval_sec = 0 if _si <= 0 else max(60, _si)
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

    def _stat(self, key: str, n: int = 1):
        self.stats[key] = int(self.stats.get(key) or 0) + n

    def _log_stats_summary(self):
        # 窗口计数打完即清零, 每行自含义; 仅主循环调用, 无并发写。
        if self.stats_log_interval_sec <= 0:
            return
        now = time.time()
        if now - self._last_stats_ts < float(self.stats_log_interval_sec):
            return
        win_sec = int(now - self._last_stats_ts)
        self._last_stats_ts = now
        g = self.stats.get
        c23 = self.s23c.get  # S23 @2026-08-09 累计口径(不清零)
        quarantined = sorted(s for s, u in self.quarantine_until.items() if float(u or 0) > now)
        streaks = sorted(f"{s}:{n}" for s, n in self.consec_loss.items() if int(n or 0) >= 2)
        self._log(
            f"统计汇总 窗口={win_sec}s 发信号(多/空)={g('emit_long', 0)}/{g('emit_short', 0)} "
            f"影子多(胜/负)={g('shadow_win_long', 0)}/{g('shadow_loss_long', 0)} "
            f"影子空(胜/负)={g('shadow_win_short', 0)}/{g('shadow_loss_short', 0)} "
            f"熔断触发={g('cb_trip', 0)} 熔断拦截={g('cb_block', 0)} 隔离中={','.join(quarantined) or '-'} "
            f"连败≥2={','.join(streaks) or '-'} 黑名单拦截={g('bl_block', 0)} 宽度拦截={g('breadth_block', 0)} "
            f"冷却跳过={g('cooldown_skip', 0)} 急再入veto(影子/emit)={g('fresh_reentry_block', 0)}/{g('fresh_reentry_block_emit', 0)} "  # S22 @2026-08-07 S21计数器补进统计行(原只写不读)
            f"S23bar向累计(胜/负) AL={c23('AL_w', 0)}/{c23('AL_l', 0)} AS={c23('AS_w', 0)}/{c23('AS_l', 0)} "  # S23 @2026-08-09
            f"WL={c23('WL_w', 0)}/{c23('WL_l', 0)} WS={c23('WS_w', 0)}/{c23('WS_l', 0)} "  # S23 A=逆bar W=顺bar
            f"择优批={g('pick_batch', 0)} 择优发出={g('pick_emit', 0)} 择优落选={g('pick_lose', 0)} "
            f"动态注册={g('dyn_reg', 0)} 清理停推={g('dyn_purge', 0)} "
            f"当前池={len(self.closes)} 影子在途={len(self.shadow_open)} 时间={_now()}"
        )
        self.stats = {}

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

    def _symbol_denied(self, symbol: str) -> bool:
        # 平台侧限制：auto_symbols=filter 模式的选币器不读 config.symbol_blacklist，
        # 黑名单币会照常混入自选池并开仓（实证：UB/AVAAI/1000XEC 24h 合计 -$19）。
        # 策略进程是黑名单唯一可靠的执行点，_emit_signal 是信号唯一出口，故在此拦截。
        # 归一化：去 "/"、大写，兼容 config 里 "UBUSDT" 与 "UB/USDT" 两种写法。
        deny = getattr(self, "symbol_denylist", None) or set()
        if not deny:
            return False
        return symbol.replace("/", "").strip().upper() in deny

    def _emit_signal(self, symbol: str, direction: str, entry_price: float, tp: float, sl: float, confidence: float):
        if self._symbol_denied(symbol):
            self._log(f"黑名单拦截 sym={symbol} 方向={direction} 置信度={confidence:.4f} 信号已丢弃 时间={_now()}")
            self._stat("bl_block")
            return
        # S17 removed @2026-08-05: BRAKE解除(owner直令 方向解锁),恢复双向;后续单闸=config allowed_sides。原文见台账§2与git历史。
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
        # S23 @2026-08-09 emit时点bar向标记: closes[-1]vs[-2]=上一完成bar方向(S18同口径);
        # 与交易方向反向=A(against), 顺向/平bar/数据不足=W。仅标记不拦截, 标记随影子单归档。
        _cl23 = self.closes.get(symbol) or []
        _t23 = "W"
        if len(_cl23) >= 2 and float(_cl23[-2]) > 0:
            _chg23 = float(_cl23[-1]) - float(_cl23[-2])
            if (direction == "long" and _chg23 < 0) or (direction == "short" and _chg23 > 0):
                _t23 = "A"
        self._log(f"触发开仓信号 sym={symbol} 方向={direction} side={side} 数量={amount} 入场价={entry_price} 止盈={tp} 止损={sl} 置信度={confidence:.4f} bar向={_t23} 时间={_now()}")
        self._stat("emit_long" if direction == "long" else "emit_short")
        if symbol in self.shadow_open:
            self._log(f"影子单覆盖 sym={symbol} 旧单未决被新信号覆盖 时间={_now()}")
        self.shadow_open[symbol] = {"entry": float(entry_price), "tp": float(tp) if tp else 0.0, "sl": float(sl) if sl else 0.0, "direction": direction, "ts": time.time(), "s23": _t23}

    def _update_shadow(self, symbol: str, high: float, low: float, close: float):
        # 影子跟单判定(熔断器数据源): 同一根K线同时触及TP与SL时保守判负;
        # 超时单(超 MAX_HOLD_MINUTES)按 close vs entry 结算。仅主消息循环调用, 无并发写。
        st = self.shadow_open.get(symbol)
        if not st:
            return
        direction = _s(st.get("direction")).strip().lower()
        entry = _f(st.get("entry"), 0.0)
        tp = _f(st.get("tp"), 0.0)
        sl = _f(st.get("sl"), 0.0)
        opened_ts = _f(st.get("ts"), 0.0)
        outcome = ""
        if direction == "long":
            if sl > 0 and low <= sl:
                outcome = "loss"
            elif tp > 0 and high >= tp:
                outcome = "win"
        else:
            if sl > 0 and high >= sl:
                outcome = "loss"
            elif tp > 0 and low <= tp:
                outcome = "win"
        basis = "触及"
        if not outcome and opened_ts > 0 and time.time() - opened_ts > Config.MAX_HOLD_MINUTES * 60.0:
            if direction == "long":
                outcome = "win" if close > entry else "loss"
            else:
                outcome = "win" if close < entry else "loss"
            basis = "超时"
        if not outcome:
            return
        self.shadow_open.pop(symbol, None)
        # S21 @2026-08-07 记录影子平仓(方向/时刻/结果), 供同向急再入veto读取。
        self.last_shadow_close[symbol] = {"ts": time.time(), "direction": direction, "outcome": outcome}
        # S23 @2026-08-09 影子平仓按bar向cohort累计(A=逆bar入场/W=顺bar), enforcement审计数据源。
        _t23c = str(st.get("s23") or "")
        if _t23c in ("A", "W"):
            _k23 = f"{_t23c}{'L' if direction == 'long' else 'S'}_{'w' if outcome == 'win' else 'l'}"
            self.s23c[_k23] = int(self.s23c.get(_k23) or 0) + 1
        if outcome == "win":
            self.consec_loss[symbol] = 0
            self._log(f"影子单平仓 sym={symbol} 结果=胜 依据={basis} 方向={direction} 入场={entry} 时间={_now()}")
            self._stat("shadow_win_long" if direction == "long" else "shadow_win_short")
            return
        n = int(self.consec_loss.get(symbol) or 0) + 1
        self.consec_loss[symbol] = n
        self._log(f"影子单平仓 sym={symbol} 结果=负 依据={basis} 方向={direction} 入场={entry} 连败={n} 时间={_now()}")
        self._stat("shadow_loss_long" if direction == "long" else "shadow_loss_short")
        if Config.CB_ENABLED and n >= Config.CB_CONSEC_LOSSES:
            until = time.time() + Config.CB_QUARANTINE_MIN * 60.0
            self.quarantine_until[symbol] = until
            self.consec_loss[symbol] = 0
            until_iso = datetime.fromtimestamp(until, tz=timezone.utc).isoformat()
            self._log(f"熔断触发 sym={symbol} 连败={n} 隔离={Config.CB_QUARANTINE_MIN}min 隔离至={until_iso} 时间={_now()}")
            self._stat("cb_trip")

    def _init_symbol_state(self, symbol: str):
        # 动态币池：未知 symbol 的 K 线到达时惰性注册一套空行情状态。
        # last_signal_ts/last_signal_dir 不在此初始化也不随清理删除——
        # 币被平台移除又换回时冷却计时必须延续，否则轮换会绕过 cooldown。
        self.closes.setdefault(symbol, [])
        self.highs.setdefault(symbol, [])
        self.lows.setdefault(symbol, [])
        self.volumes.setdefault(symbol, [])
        self.recv_count.setdefault(symbol, 0)
        self.last_bar_ts.setdefault(symbol, "")
        self.last_seen_ts[symbol] = time.time()

    def _purge_idle_symbols(self):
        # 仅从主消息循环调用（心跳线程不碰 per-symbol 字典，无并发写）。
        if self.symbol_idle_purge_sec <= 0:
            return
        now = time.time()
        if now - self._last_purge_ts < 60.0:
            return
        self._last_purge_ts = now
        stale = [s for s, ts in self.last_seen_ts.items() if now - ts > float(self.symbol_idle_purge_sec)]
        for s in stale:
            # shadow_open 随行情状态清理; consec_loss/quarantine_until 特意保留(同 last_signal_ts 语义)。
            for d in (self.closes, self.highs, self.lows, self.volumes, self.recv_count, self.last_bar_ts, self.last_seen_ts, self.shadow_open):
                d.pop(s, None)
        if stale:
            self._stat("dyn_purge", len(stale))
            self._log(f"清理停推币种 count={len(stale)} syms={','.join(sorted(stale))} 阈值={self.symbol_idle_purge_sec}s 当前池={len(self.closes)} 时间={_now()}")

    def _append_bar(self, symbol: str, h: float, l: float, c: float, v: float = 0.0):
        if symbol not in self.closes:
            self._init_symbol_state(symbol)
        self.closes[symbol].append(float(c))
        self.highs[symbol].append(float(h))
        self.lows[symbol].append(float(l))
        self.volumes[symbol].append(float(v))
        if len(self.closes[symbol]) > Config.MAX_BARS:
            self.closes[symbol] = self.closes[symbol][-Config.MAX_BARS :]
            self.highs[symbol] = self.highs[symbol][-Config.MAX_BARS :]
            self.lows[symbol] = self.lows[symbol][-Config.MAX_BARS :]
            self.volumes[symbol] = self.volumes[symbol][-Config.MAX_BARS :]

    def _market_breadth(self) -> tuple[float, int]:
        """池内EMA趋势向上币占比。返回(breadth, 可评估币数); 60s缓存。"""
        now = time.time()
        cache_ts, cache_val, cache_n = self._breadth_cache
        if cache_n > 0 and now - cache_ts < 60.0:
            return cache_val, cache_n
        ups = 0
        n_eval = 0
        for _bsym, _bcloses in self.closes.items():
            if len(_bcloses) < Config.EMA_SLOW:
                continue
            tr = calc_ema_trend(_bcloses, Config.EMA_FAST, Config.EMA_SLOW)
            if tr is None:
                continue
            n_eval += 1
            if tr == "up":
                ups += 1
        val = (ups / n_eval) if n_eval > 0 else 1.0
        self._breadth_cache = (now, val, n_eval)
        return val, n_eval

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
        if not symbol:
            return
        if symbol not in self.closes:
            # 动态币池：平台热增币时直接开始推流（新币会先回灌 history），此处惰性注册。
            self._init_symbol_state(symbol)
            src = "history" if from_history else "live"
            self._log(f"动态注册新币 sym={symbol} 来源={src} 当前池={len(self.closes)} 时间={_now()}")
            self._stat("dyn_reg")
        self.last_seen_ts[symbol] = time.time()

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
        if not from_history:
            self._update_shadow(symbol, h, l, c)
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
                hard_reject = _s(sc.get("hard_filter_reject"))
                if hard_reject:
                    self._log(
                        f"硬过滤拦截 sym={symbol} 原因={hard_reject} 置信度={r.confidence:.3f} "
                        f"多头分={_f(sc.get('ls'), 0.0):.3f} 空头分={_f(sc.get('ss'), 0.0):.3f} 时间={_now()}"
                    )
                else:
                    self._log(
                        f"未触发信号 sym={symbol} 置信度={r.confidence:.3f} 多头分={_f(sc.get('ls'), 0.0):.3f} "
                        f"空头分={_f(sc.get('ss'), 0.0):.3f} 最低置信度={_f(sc.get('min_confidence'), Config.MIN_CONFIDENCE):.3f} 时间={_now()}"
                    )
            return

        # 熔断隔离gate: 连败熔断中的币直接闭嘴(方向确认后最先查, 省掉宽度计算)
        _q_until = float(self.quarantine_until.get(symbol) or 0.0)
        if Config.CB_ENABLED and _q_until > 0:
            _q_now = time.time()
            if _q_now < _q_until:
                self._stat("cb_block")
                if self.log_decisions:
                    self._log(f"熔断隔离中 sym={symbol} 方向={_s(r.direction).strip().lower()} 剩余={_q_until - _q_now:.0f}s 置信度={r.confidence:.3f} 时间={_now()}")
                return
            self.quarantine_until.pop(symbol, None)

        # 同向急再入veto(gate7d)  # S21 @2026-08-07 信号新鲜度闸: 影子平仓后<45m同向再入拒。
        # 病灶: re3引擎闸放行+信号路径无前腿结果记忆, postS20急再入次腿20例/-20.34 wr20%
        # (盈后腿8/-7.73与亏后腿12/-12.62双负=全veto, 窄版不采); 反向快速反手保留(无同型失血)。
        # 影子close≈实盘close(S14同源近似, 幻影影子假veto窗为已知噪声成本, fresh_reentry_block计数观测)。
        _lsc = self.last_shadow_close.get(symbol)
        if _lsc:
            _lsc_gap = time.time() - _f(_lsc.get("ts"), 0.0)
            if 0.0 <= _lsc_gap < 45 * 60.0 and _s(_lsc.get("direction")).strip().lower() == _s(r.direction).strip().lower():
                self._stat("fresh_reentry_block")
                if self.log_decisions:
                    self._log(f"急再入veto sym={symbol} 方向={_s(r.direction).strip().lower()} 距影子平仓={_lsc_gap/60:.0f}m<45m 前腿={_s(_lsc.get('outcome'))} 置信度={r.confidence:.3f} 时间={_now()}")
                return
        # S22 @2026-08-07 gate7d补强(emit口径副闸): 影子平仓依赖bar连续投递, 断供分钟恰覆盖
        # SL穿越时影子挂死→close口径veto静默失效(AIOT 21:49 SL→21:53同向再入4m漏拦实锤)。
        # last_signal_ts/dir在emit时点盖戳(直发/择优胜出两路径), 与bar投递无关=断供免疫地板;
        # 反向反手仍不限(同7d语义); 落选不盖戳故无误伤; restart清内存态=已知冷启动窗。
        _els = float(self.last_signal_ts.get(symbol) or 0.0)
        if _els > 0:
            _els_gap = time.time() - _els
            if 0.0 <= _els_gap < 45 * 60.0 and _s(self.last_signal_dir.get(symbol)).strip().lower() == _s(r.direction).strip().lower():
                self._stat("fresh_reentry_block_emit")
                if self.log_decisions:
                    self._log(f"急再入veto(emit口径) sym={symbol} 方向={_s(r.direction).strip().lower()} 距上次同向信号={_els_gap/60:.0f}m<45m 置信度={r.confidence:.3f} 时间={_now()}")
                return

        # 市场宽度regime门: 方向确认后、冷却前做全池否决(见Config注释)
        if Config.BREADTH_MIN_FOR_LONG > 0.0 or Config.BREADTH_MAX_FOR_SHORT < 1.0:
            _breadth, _breadth_n = self._market_breadth()
            if _breadth_n >= Config.BREADTH_MIN_SYMBOLS:
                _dir = _s(r.direction).strip().lower()
                if _dir == "long" and _breadth < Config.BREADTH_MIN_FOR_LONG:
                    self._stat("breadth_block")
                    if self.log_decisions:
                        self._log(f"宽度门拦截 sym={symbol} 方向=long 宽度={_breadth:.2f}<{Config.BREADTH_MIN_FOR_LONG:.2f} 样本={_breadth_n} 置信度={r.confidence:.3f} 时间={_now()}")
                    return
                if _dir == "short" and _breadth > Config.BREADTH_MAX_FOR_SHORT:
                    self._stat("breadth_block")
                    if self.log_decisions:
                        self._log(f"宽度门拦截 sym={symbol} 方向=short 宽度={_breadth:.2f}>{Config.BREADTH_MAX_FOR_SHORT:.2f} 样本={_breadth_n} 置信度={r.confidence:.3f} 时间={_now()}")
                    return

        now = time.time()
        last_ts = float(self.last_signal_ts.get(symbol) or 0.0)
        last_dir = _s(self.last_signal_dir.get(symbol)).strip().lower()
        if Config.COOLDOWN_SEC > 0 and last_ts > 0 and now - last_ts < float(Config.COOLDOWN_SEC):
            self._stat("cooldown_skip")
            remaining = max(0.0, float(Config.COOLDOWN_SEC) - (now - last_ts))
            if self.log_decisions:
                self._log(
                    f"跳过-冷却中 sym={symbol} 上次方向={last_dir or '-'} 本次方向={_s(r.direction).strip().lower()} remaining={remaining:.1f}s 时间={_now()}"
                )
            return
        if Config.BEST_PICK_ENABLED:
            # 择优路径: 冷却检查已过, 但冷却戳推迟到flush胜出时才盖——落选不烧冷却。
            self._offer_best_pick(symbol, _s(r.direction).strip().lower(), r.entry_price, r.tp_price, r.sl_price, r.confidence)
            return
        self.last_signal_ts[symbol] = now
        self.last_signal_dir[symbol] = _s(r.direction).strip().lower()
        self._emit_signal(symbol, _s(r.direction).strip().lower(), r.entry_price, r.tp_price, r.sl_price, r.confidence)

    def _offer_best_pick(self, symbol: str, direction: str, entry_price: float, tp: float, sl: float, confidence: float):
        # 黑名单在入批口先拦: 若等到emit口才拦, 黑名单币可能先挤掉真候选再被丢弃, 整批作废。
        if self._symbol_denied(symbol):
            self._stat("bl_block")
            self._log(f"黑名单拦截 sym={symbol} 方向={direction} 置信度={confidence:.4f} 信号已丢弃 时间={_now()}")
            return
        cand = {"symbol": symbol, "direction": direction, "entry": float(entry_price),
                "tp": float(tp) if tp else 0.0, "sl": float(sl) if sl else 0.0, "confidence": float(confidence)}
        cur = self.best_pick
        if cur is None:
            self.best_pick = cand
            self.best_pick_deadline = time.time() + float(Config.BEST_PICK_WINDOW_SEC)
            self._stat("pick_batch")
            if self.log_decisions:
                self._log(f"择优攒批开始 sym={symbol} 方向={direction} 置信度={confidence:.4f} 窗口={Config.BEST_PICK_WINDOW_SEC:.0f}s 时间={_now()}")
            return
        # 同批竞争: 严格更高才换, 平手保先到者(其K线更早收盘, 数据更新鲜)。窗口锚定批首不延长。
        if float(cand["confidence"]) > float(cur["confidence"]):
            self._stat("pick_lose")
            if self.log_decisions:
                self._log(f"择优替换 新={symbol}({confidence:.4f}) 旧={cur['symbol']}({float(cur['confidence']):.4f}) 时间={_now()}")
            self.best_pick = cand
        else:
            self._stat("pick_lose")
            if self.log_decisions:
                self._log(f"择优落选 sym={symbol} 置信度={confidence:.4f} 当前最高={cur['symbol']}({float(cur['confidence']):.4f}) 时间={_now()}")

    def _flush_best_pick(self):
        cur = self.best_pick
        if cur is None or time.time() < self.best_pick_deadline:
            return
        self.best_pick = None
        symbol = _s(cur.get("symbol"))
        # 攒批窗口内该币影子单可能判负触发熔断, flush前复查一次隔离态
        _q = float(self.quarantine_until.get(symbol) or 0.0)
        if Config.CB_ENABLED and _q > time.time():
            self._stat("cb_block")
            self._log(f"择优胜者被熔断拦截 sym={symbol} 时间={_now()}")
            return
        direction = _s(cur.get("direction")).strip().lower()
        self.last_signal_ts[symbol] = time.time()
        self.last_signal_dir[symbol] = direction
        self._stat("pick_emit")
        self._log(f"择优胜出 sym={symbol} 方向={direction} 置信度={_f(cur.get('confidence'), 0.0):.4f} 时间={_now()}")
        self._emit_signal(symbol, direction, _f(cur.get("entry"), 0.0), _f(cur.get("tp"), 0.0), _f(cur.get("sl"), 0.0), _f(cur.get("confidence"), 0.0))

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
            self._purge_idle_symbols()
            self._log_stats_summary()
            self._flush_best_pick()
            item = self.sub.read_pubsub_message()
            if not item:
                if self.log_rx and time.time() - last_idle >= float(self.log_idle_sec):
                    last_idle = time.time()
                    self._log(f"IDLE waiting candle ch={self._candle_ch()} symbols={len(self.closes)} recv={self._recv_summary()} ts={_now()}")
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

# runtime-regen nudge 2026-07-11: re-apply known-good code to rebuild stuck runtime (status=error, runtime_generated=false, runtime_path=empty). Logic unchanged from 07-07 last-trading version; goal is materializing a runnable runtime so start no longer errors.
