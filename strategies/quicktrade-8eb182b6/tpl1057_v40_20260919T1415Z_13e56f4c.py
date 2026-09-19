"""
crypto_perp_engine_v40 —— 加密永续原生信号引擎

替代 Meme_合约信号计算引擎_1（模板 998）的【评分核心】。接口契约完全不变
（同一个 Redis candle/signal 频道、同一个信号 JSON 形状、同一个 argv 配置）。

======================================================================
为什么重写 —— 三条已核证据（2026-09-19 实测，非推测）
======================================================================

1) 旧引擎的"加密货币原生输入"是【钉死的常量】，不是市场数据。
   源模板 `strategies/Meme_合约信号计算引擎_1.py` 第 1246-1247 行：

       fr = _f(extra.get("funding_rate"), 0.0)     # 默认 0.0
       lr = _f(extra.get("ls_ratio"), 1.0)         # 默认 1.0

   而实盘 config（strategy_instances.config）里 `funding_rate` / `ls_ratio`
   这两个键【根本不存在】（实测返回"不存在"）⇒ 永远是 0.0 与 1.0。

   于是模板里所有分支全部失效（score_confidence 561-569、score_confidence_detail 646-657）：

       if funding_rate < -0.0003:  ...   # 0.0 < -0.0003  → False
       elif funding_rate > 0.0003: ...   # 0.0 >  0.0003  → False   ⇒ 资金费率调整恒为 0
       if ls_ratio < 0.8:  ...           # 1.0 < 0.8       → False
       elif ls_ratio > 1.5: ...          # 1.0 > 1.5       → False   ⇒ 多空比调整恒为 0

2) 整个模板对 Binance 的【外部行情调用 0 处】
   （grep `fundingRate|openInterest|longShortRatio|premiumIndex|requests.|urllib` 无命中）
   ⇒ 旧引擎实际只吃 K 线。它是把股票式 TA 套在永续合约上跑。

3) 结构性多头偏置：`LONG_BIAS_FACTOR=1.15` / `SHORT_RESTRICTION_FACTOR=0.7`，
   且空头门槛更严（MIN_SHORT_CONFIDENCE=0.68 > MIN_LONG_CONFIDENCE=0.60）。
   实盘后果与方向数据一致：48h 多头 −144.017U vs 空头 −57.860U。

======================================================================
本引擎做什么
======================================================================

A. 真的去取 Binance 永续公开数据（免费、无需鉴权、无需 API key）：
   - 资金费率 + 基差（markPrice/indexPrice）—— /fapi/v1/premiumIndex
   - 持仓量变化 —— /futures/data/openInterestHist
   - 散户多空账户比 —— /futures/data/globalLongShortAccountRatio
   - 大户持仓比 —— /futures/data/topLongShortPositionRatio
   - 主动买卖量比 —— /futures/data/takerlongshortRatio

B. 成本感知闸门（这是实测缺口的直接对症）：
   已实测：逐笔毛期望 ≈ 0，来回 taker 费 0.10% 是唯一确定的负项。
   ⇒ 任何信号必须证明 `预期波动 >= COST_MULT × 来回手续费`，否则直接丢。
   旧引擎完全不知道手续费的存在。

C. 对称：多空同一个门槛、同一个权重，无偏置。

======================================================================
速率预算（关键设计，别改成"每 tick 全员取数"）
======================================================================
- /fapi/v1/premiumIndex 不带 symbol = 【一次拿到全宇宙】，weight 10。默认 30s 刷一次。
- /futures/data/* 这些端点本身是【5 分钟粒度】的 ⇒ 缓存 300s 不是妥协，是正确的。
- 只对通过 K 线初筛的少数候选逐个取数（默认上限 12 个）。
- 实测坑：突发 >160 请求/20s 会触发 WAF 封；默认 urllib UA 会被 403 ⇒ 必须带浏览器 UA。

======================================================================
自检
======================================================================
    python3 crypto_perp_engine_v40.py --selftest
        → 只读，验证 Binance 数据通路 + 打印若干真实数值。不下单、不写库。
"""

import json
import os
import socket
import sys
import threading
import time
import types
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from typing import Dict, List, Optional, Tuple


# ======================================================================
# 0. Redis 垫片
#    生产环境由平台在前面注入 miniRedisRuntimeShim()，这里只在本地裸跑时兜底。
# ======================================================================
try:
    MiniRedis  # noqa: B018  运行时由平台注入
except NameError:  # pragma: no cover - 本地测试路径
    class MiniRedis:
        """只实现本引擎用到的最小 RESP 子集：AUTH / SELECT / SUBSCRIBE / PUBLISH。"""

        def __init__(self, host="127.0.0.1", port=6379, password="", db=0, timeout=30):
            self.host, self.port = host, int(port)
            self.password, self.db = password or "", int(db or 0)
            self.timeout = timeout
            self.sock, self.buf = None, b""

        def connect(self):
            self.sock = socket.create_connection((self.host, self.port), timeout=self.timeout or None)
            if self.timeout:
                self.sock.settimeout(self.timeout)
            if self.password:
                self.execute("AUTH", self.password)
            if self.db:
                self.execute("SELECT", str(self.db))
            return self

        def _enc(self, *parts):
            out = [("*%d\r\n" % len(parts)).encode()]
            for p in parts:
                b = p if isinstance(p, (bytes, bytearray)) else str("" if p is None else p).encode()
                out += [("$%d\r\n" % len(b)).encode(), bytes(b), b"\r\n"]
            return b"".join(out)

        def _fill(self, n):
            while len(self.buf) < n:
                ch = self.sock.recv(4096)
                if not ch:
                    raise ConnectionError("redis closed")
                self.buf += ch
            out, self.buf = self.buf[:n], self.buf[n:]
            return out

        def _line(self):
            while b"\r\n" not in self.buf:
                ch = self.sock.recv(4096)
                if not ch:
                    raise ConnectionError("redis closed")
                self.buf += ch
            ln, self.buf = self.buf.split(b"\r\n", 1)
            return ln

        def _resp(self):
            t = self._fill(1)
            if t == b"+":
                return self._line().decode()
            if t == b"-":
                raise RuntimeError(self._line().decode())
            if t == b":":
                return int(self._line())
            if t == b"$":
                n = int(self._line())
                if n < 0:
                    return None
                d = self._fill(n + 2)[:-2]
                return d.decode("utf-8", "replace")
            if t == b"*":
                n = int(self._line())
                return None if n < 0 else [self._resp() for _ in range(n)]
            raise RuntimeError("bad RESP type %r" % t)

        def execute(self, *args):
            self.sock.sendall(self._enc(*args))
            return self._resp()

        def publish(self, ch, payload):
            return self.execute("PUBLISH", ch, payload)

        def read_pubsub_message(self, timeout=1.0):
            self.sock.settimeout(timeout)
            try:
                while True:
                    m = self._resp()
                    if isinstance(m, list) and len(m) >= 3 and str(m[0]).lower() in ("message", "pmessage"):
                        return {"channel": m[1] if len(m) == 3 else m[2],
                                "data": m[2] if len(m) == 3 else m[3]}
            except socket.timeout:
                return None
            except Exception:
                return None


def _now() -> str:
    return datetime.now(tz=timezone.utc).isoformat()


def _f(v, d=0.0) -> float:
    try:
        x = float(v)
        return x if x == x else d  # 挡 NaN
    except (TypeError, ValueError):
        return d


def _i(v, d=0) -> int:
    try:
        return int(float(v))
    except (TypeError, ValueError):
        return d


def _s(v, d="") -> str:
    return v if isinstance(v, str) else (d if v is None else str(v))


# ======================================================================
# 1. 配置
# ======================================================================
class Config:
    # —— 与旧引擎兼容的键（Go 侧 / 平台会读） ——
    MIN_CONFIDENCE = 0.55
    TP_RATIO = 0.012
    SL_RATIO = 0.006
    ATR_TP_MULT = 2.5
    ATR_SL_MULT = 1.2
    COOLDOWN_SEC = 900
    WARMUP_BARS = 120
    MAX_BARS = 600

    # —— 成本模型（本引擎新增；单位=价格比例，非 ROI） ——
    FEE_ROUND_TRIP = 0.0010   # taker 0.05%/边 × 2
    COST_MULT = 2.0           # 预期波动必须 ≥ 2× 来回费，否则不开
    FEE_SL_FLOOR_MULT = 1.5   # 止损不得比 1.5× 来回费还窄（否则被噪声打掉净亏手续费）

    # —— 资金费率（8 小时一档） ——
    FUNDING_STRONG = 0.0003   # 0.03%/8h：拥挤开始有意义
    FUNDING_EXTREME = 0.0005  # 0.05%/8h：极端拥挤，均值回归概率高
    FUNDING_MAX_USABLE = 0.0030  # 0.30%/8h 以上视为数据异常/交割切换，不交易

    # —— 多空比（散户账户比，逆向用） ——
    LS_RETAIL_HIGH = 2.0
    LS_RETAIL_LOW = 0.70

    # —— 大户持仓比（同向用，不逆向） ——
    LS_TOP_HIGH = 1.2
    LS_TOP_LOW = 0.85

    # —— 主动买卖量比 ——
    TAKER_HOT = 0.55
    TAKER_COLD = 0.45

    # —— 持仓量变化（窗口内比例） ——
    OI_SURGE = 0.03

    # —— 偏置：对称。旧引擎这里是 1.15 / 0.7，那是被实测证伪的。 ——
    LONG_BIAS = 1.0
    SHORT_BIAS = 1.0

    # —— 网络 ——
    FAPI = "https://fapi.binance.com"
    UA = "Mozilla/5.0 (compatible; quanty-v40/1.0)"
    PREMIUM_TTL = 30.0        # premiumIndex 刷新周期
    DATA_TTL = 300.0          # /futures/data/* 是 5 分钟粒度，缓存 300s 正确
    HTTP_TIMEOUT = 8.0
    MAX_CANDIDATES = 12       # 每 tick 最多对几个候选逐个取数（速率预算）


# ======================================================================
# 2. Binance 公开行情
# ======================================================================
class MarketDataFeed:
    """只读 Binance 永续公开端点。全部免鉴权。

    线程安全：后台线程刷新 premiumIndex；/futures/data/* 按需拉 + 带 TTL 缓存。
    任何网络失败都只是"这条信号少一个输入"，绝不抛到主循环。
    """

    def __init__(self, log=None):
        self._log = log or (lambda m: None)
        self._lock = threading.Lock()
        self._premium: Dict[str, dict] = {}     # sym(无斜杠) -> {mark, index, funding, next_funding}
        self._premium_at = 0.0
        self._cache: Dict[str, Tuple[float, dict]] = {}
        self._http_errors = 0
        self._ok_calls = 0

    # ---------- 底层 HTTP ----------
    def _get(self, path: str, params: Optional[dict] = None):
        url = Config.FAPI + path
        if params:
            url += "?" + urllib.parse.urlencode(params)
        req = urllib.request.Request(url)
        req.add_header("User-Agent", Config.UA)   # 默认 UA 会被 403
        with urllib.request.urlopen(req, timeout=Config.HTTP_TIMEOUT) as resp:
            raw = resp.read().decode("utf-8")
        self._ok_calls += 1
        return json.loads(raw) if raw else None

    def _get_soft(self, path, params=None):
        try:
            return self._get(path, params)
        except Exception as e:
            self._http_errors += 1
            self._log("[v40] 行情拉取失败 path=%s err=%r" % (path, e))
            return None

    # ---------- 全宇宙：资金费率 + 基差（一次调用） ----------
    def refresh_premium(self, force: bool = False) -> int:
        now = time.time()
        with self._lock:
            if not force and now - self._premium_at < Config.PREMIUM_TTL and self._premium:
                return len(self._premium)
        data = self._get_soft("/fapi/v1/premiumIndex")   # 不带 symbol = 全宇宙
        if not isinstance(data, list):
            return len(self._premium)
        fresh = {}
        for row in data:
            if not isinstance(row, dict):
                continue
            sym = _s(row.get("symbol"))
            if not sym:
                continue
            fresh[sym] = {
                "mark": _f(row.get("markPrice")),
                "index": _f(row.get("indexPrice")),
                "funding": _f(row.get("lastFundingRate")),
                "next_funding": _i(row.get("nextFundingTime")),
            }
        if fresh:
            with self._lock:
                self._premium = fresh
                self._premium_at = time.time()
        return len(fresh)

    def premium(self, symbol: str) -> dict:
        """symbol 允许带斜杠（BR/USDT 或 BRUSDT 都收）。"""
        key = symbol.replace("/", "").upper()
        with self._lock:
            return dict(self._premium.get(key) or {})

    def funding(self, symbol: str) -> Optional[float]:
        p = self.premium(symbol)
        return p.get("funding") if p else None

    def basis(self, symbol: str) -> Optional[float]:
        """mark/index - 1。正=永续贵于现货（多头拥挤）。"""
        p = self.premium(symbol)
        if not p:
            return None
        mk, ix = p.get("mark") or 0.0, p.get("index") or 0.0
        if mk <= 0 or ix <= 0:
            return None
        return mk / ix - 1.0

    # ---------- 单币端点（带 TTL 缓存） ----------
    def _cached(self, key: str, path: str, params: dict):
        now = time.time()
        hit = self._cache.get(key)
        if hit and now - hit[0] < Config.DATA_TTL:
            return hit[1]
        data = self._get_soft(path, params)
        out = data if data is not None else (hit[1] if hit else None)
        self._cache[key] = (now, out)
        return out

    def oi_change(self, symbol: str, period: str = "5m", limit: int = 12) -> Optional[float]:
        """窗口内持仓量变化比例。OI 升 + 价跌 = 空头在建（挤空燃料）。"""
        sym = symbol.replace("/", "").upper()
        data = self._cached("oi:%s:%s" % (sym, period), "/futures/data/openInterestHist",
                            {"symbol": sym, "period": period, "limit": limit})
        if not isinstance(data, list) or len(data) < 3:
            return None
        first = _f((data[0] or {}).get("sumOpenInterest"))
        last = _f((data[-1] or {}).get("sumOpenInterest"))
        if first <= 0 or last <= 0:
            return None
        return last / first - 1.0

    def retail_ls(self, symbol: str, period: str = "5m") -> Optional[float]:
        sym = symbol.replace("/", "").upper()
        data = self._cached("ls:%s:%s" % (sym, period), "/futures/data/globalLongShortAccountRatio",
                            {"symbol": sym, "period": period, "limit": 1})
        if isinstance(data, list) and data:
            v = _f((data[0] or {}).get("longShortRatio"))
            return v if v > 0 else None
        return None

    def top_ls(self, symbol: str, period: str = "5m") -> Optional[float]:
        sym = symbol.replace("/", "").upper()
        data = self._cached("tls:%s:%s" % (sym, period), "/futures/data/topLongShortPositionRatio",
                            {"symbol": sym, "period": period, "limit": 1})
        if isinstance(data, list) and data:
            v = _f((data[0] or {}).get("longShortRatio"))
            return v if v > 0 else None
        return None

    def taker_ratio(self, symbol: str, period: str = "5m") -> Optional[float]:
        sym = symbol.replace("/", "").upper()
        data = self._cached("tk:%s:%s" % (sym, period), "/futures/data/takerlongshortRatio",
                            {"symbol": sym, "period": period, "limit": 1})
        if isinstance(data, list) and data:
            v = _f((data[0] or {}).get("buySellRatio"))
            return v if v > 0 else None
        return None

    # ---------- 后台刷新 ----------
    def start(self):
        def loop():
            while True:
                try:
                    n = self.refresh_premium(force=True)
                    self._log("[v40] 资金费率全宇宙刷新 n=%d ok=%d err=%d" % (n, self._ok_calls, self._http_errors))
                except Exception as e:
                    self._log("[v40] premium 刷新异常 err=%r" % (e,))
                time.sleep(Config.PREMIUM_TTL)

        t = threading.Thread(target=loop, daemon=True)
        t.start()
        return t


# ======================================================================
# 3. 纯技术面（保留旧引擎里经得起检验的部分，但更简、对称）
# ======================================================================
def ema(xs: List[float], span: int) -> Optional[float]:
    if len(xs) < span or span <= 0:
        return None
    k = 2.0 / (span + 1.0)
    v = sum(xs[:span]) / span
    for x in xs[span:]:
        v = x * k + v * (1 - k)
    return v


def rsi(closes: List[float], period: int = 14) -> Optional[float]:
    if len(closes) < period + 1:
        return None
    gains = losses = 0.0
    for i in range(-period, 0):
        d = closes[i] - closes[i - 1]
        gains += max(d, 0.0)
        losses += max(-d, 0.0)
    if losses == 0:
        return 100.0
    rs = (gains / period) / (losses / period)
    return 100.0 - 100.0 / (1.0 + rs)


def atr_pct(highs: List[float], lows: List[float], closes: List[float], period: int = 14) -> Optional[float]:
    if len(closes) < period + 1:
        return None
    trs = []
    for i in range(-period, 0):
        trs.append(max(highs[i] - lows[i],
                       abs(highs[i] - closes[i - 1]),
                       abs(lows[i] - closes[i - 1])))
    a = sum(trs) / period
    c = closes[-1]
    return (a / c) if c > 0 else None


def volume_ratio(volumes: List[float], period: int = 20) -> Optional[float]:
    if len(volumes) < period + 1:
        return None
    base = sum(volumes[-period - 1:-1]) / period
    return (volumes[-1] / base) if base > 0 else None


def trend_dir(closes: List[float], fast: int = 20, slow: int = 60, confirm: int = 3) -> str:
    """'up' / 'down' / 'flat'，要求连续 confirm 根同向，抗单根噪声。"""
    ef, es = ema(closes, fast), ema(closes, slow)
    if ef is None or es is None:
        return "flat"
    if confirm > 0 and len(closes) > confirm + slow:
        ok_up = ok_dn = True
        for i in range(1, confirm + 1):
            sub = closes[:len(closes) - i]
            a, b = ema(sub, fast), ema(sub, slow)
            if a is None or b is None:
                return "flat"
            ok_up &= a > b
            ok_dn &= a < b
        if ok_up:
            return "up"
        if ok_dn:
            return "down"
        return "flat"
    return "up" if ef > es else ("down" if ef < es else "flat")


# ======================================================================
# 4. 加密原生评分
# ======================================================================
def crypto_adjust(symbol: str, direction: str, md: MarketDataFeed) -> Tuple[float, List[str], List[str]]:
    """返回 (乘数, 支持理由, 反对理由)。

    设计原则（每条都能说出机制）：
      资金费率  极端正 ⇒ 多头拥挤 ⇒ 利于 SHORT；极端负 ⇒ 利于 LONG。逆向。
      基差      永续显著贵于现货 ⇒ 同上，同向加强。
      散户多空  极端多 ⇒ 逆向（散户是错的）；极端空 ⇒ 利于 LONG。
      大户持仓  同向（大户是聪明的）：大户偏多 ⇒ 利于 LONG。
      主动买卖  买量占优 ⇒ 动量方向确认。
      持仓量    OI 升 + 价跌 ⇒ 空头在建 ⇒ 挤空利于 LONG；OI 升 + 价涨 ⇒ 利于 LONG。
    """
    sup: List[str] = []
    opp: List[str] = []
    mult = 1.0
    want_long = (direction == "long")

    # --- 资金费率 ---
    fr = md.funding(symbol)
    if fr is not None:
        if abs(fr) > Config.FUNDING_MAX_USABLE:
            opp.append("资金费率异常%.5f" % fr)
            mult *= 0.5
        elif fr >= Config.FUNDING_EXTREME:          # 多头极度拥挤
            if want_long:
                opp.append("资金费率极端正%.5f" % fr); mult *= 0.55
            else:
                sup.append("资金费率极端正%.5f→挤多" % fr); mult *= 1.35
        elif fr >= Config.FUNDING_STRONG:
            if want_long:
                opp.append("资金费率偏高%.5f" % fr); mult *= 0.80
            else:
                sup.append("资金费率偏高%.5f" % fr); mult *= 1.15
        elif fr <= -Config.FUNDING_EXTREME:         # 空头极度拥挤
            if want_long:
                sup.append("资金费率极端负%.5f→挤空" % fr); mult *= 1.35
            else:
                opp.append("资金费率极端负%.5f" % fr); mult *= 0.55
        elif fr <= -Config.FUNDING_STRONG:
            if want_long:
                sup.append("资金费率偏低%.5f" % fr); mult *= 1.15
            else:
                opp.append("资金费率偏低%.5f" % fr); mult *= 0.80
        else:
            sup.append("资金费率中性%.5f" % fr)

    # --- 基差 ---
    basis = md.basis(symbol)
    if basis is not None and abs(basis) > 0.0002:
        if basis > 0:
            if want_long:
                opp.append("基差正%.4f%%" % (basis * 100)); mult *= 0.90
            else:
                sup.append("基差正%.4f%%" % (basis * 100)); mult *= 1.08
        else:
            if want_long:
                sup.append("基差负%.4f%%" % (basis * 100)); mult *= 1.08
            else:
                opp.append("基差负%.4f%%" % (basis * 100)); mult *= 0.90

    # --- 散户多空比（逆向） ---
    ls = md.retail_ls(symbol)
    if ls is not None:
        if ls >= Config.LS_RETAIL_HIGH:
            if want_long:
                opp.append("散户过度做多%.2f" % ls); mult *= 0.70
            else:
                sup.append("散户过度做多%.2f→逆向" % ls); mult *= 1.20
        elif ls <= Config.LS_RETAIL_LOW:
            if want_long:
                sup.append("散户过度做空%.2f→逆向" % ls); mult *= 1.20
            else:
                opp.append("散户过度做空%.2f" % ls); mult *= 0.70

    # --- 大户持仓比（同向） ---
    tls = md.top_ls(symbol)
    if tls is not None:
        if tls >= Config.LS_TOP_HIGH:
            if want_long:
                sup.append("大户偏多%.2f" % tls); mult *= 1.12
            else:
                opp.append("大户偏多%.2f" % tls); mult *= 0.85
        elif tls <= Config.LS_TOP_LOW:
            if want_long:
                opp.append("大户偏空%.2f" % tls); mult *= 0.85
            else:
                sup.append("大户偏空%.2f" % tls); mult *= 1.12

    # --- 主动买卖比 ---
    tk = md.taker_ratio(symbol)
    if tk is not None:
        if tk >= Config.TAKER_HOT:
            if want_long:
                sup.append("主动买占优%.2f" % tk); mult *= 1.10
            else:
                opp.append("主动买占优%.2f" % tk); mult *= 0.88
        elif tk <= Config.TAKER_COLD:
            if want_long:
                opp.append("主动卖占优%.2f" % tk); mult *= 0.88
            else:
                sup.append("主动卖占优%.2f" % tk); mult *= 1.10

    return mult, sup, opp


def oi_adjust(symbol: str, direction: str, md: MarketDataFeed, price_change: float) -> Tuple[float, List[str]]:
    """持仓量 × 价格 四象限，只在方向明确时给分。"""
    oi = md.oi_change(symbol)
    if oi is None or abs(oi) < Config.OI_SURGE:
        return 1.0, []
    want_long = (direction == "long")
    if oi > 0 and price_change < 0:
        # OI 升 + 价跌 = 空头在建 ⇒ 挤空燃料，利于 LONG
        return (1.15, ["OI+%.1f%%而价跌→空头在建" % (oi * 100)]) if want_long \
            else (0.85, ["OI+%.1f%%而价跌→逆势" % (oi * 100)])
    if oi > 0 and price_change > 0:
        # OI 升 + 价涨 = 新多进场，趋势延续（利于多的方向）
        return (1.12, ["OI+%.1f%%而价涨→增仓上行" % (oi * 100)]) if want_long \
            else (0.88, ["OI+%.1f%%而价涨→逆势" % (oi * 100)])
    if oi < 0 and price_change > 0:
        # OI 降 + 价涨 = 空头回补，动力可能耗尽
        return (0.88, ["OI%.1f%%而价涨→回补盘" % (oi * 100)]) if want_long \
            else (1.10, ["OI%.1f%%而价涨→多头回补中" % (oi * 100)])
    # OI 降 + 价跌 = 多头平仓，跌势可能延续
    return (0.85, ["OI%.1f%%而价跌→多杀多" % (oi * 100)]) if want_long \
        else (1.12, ["OI%.1f%%而价跌→多头平仓" % (oi * 100)])


# ======================================================================
# 5. 主策略
# ======================================================================
class Strategy:
    def __init__(self, config: dict):
        self.cfg = config or {}
        self.strategy_id = _s(self.cfg.get("strategy_id")).strip()
        self.owner_id = _i(self.cfg.get("owner_id"), 0)
        self.prefix = _s(self.cfg.get("redis_prefix") or os.getenv("REDIS_PREFIX") or "qt").strip() or "qt"
        self.redis_addr = _s(self.cfg.get("redis_addr") or os.getenv("REDIS_ADDR") or "127.0.0.1:6379").strip()
        self.redis_password = _s(self.cfg.get("redis_password") or os.getenv("REDIS_PASSWORD") or "")
        self.redis_db = _i(self.cfg.get("redis_db") if self.cfg.get("redis_db") is not None else os.getenv("REDIS_DB"), 0)
        self.boot_id = "%d-%d" % (int(time.time() * 1000), os.getpid())

        self.symbols: List[str] = []
        raw = self.cfg.get("symbols")
        if isinstance(raw, list):
            self.symbols = [s.strip() for s in raw if isinstance(s, str) and s.strip()]
        if not self.symbols and _s(self.cfg.get("symbol")).strip():
            self.symbols = [_s(self.cfg.get("symbol")).strip()]

        self.last_signal_ts: Dict[str, float] = {}
        self.last_bar_ts: Dict[str, str] = {}
        self.recv_count: Dict[str, int] = {s: 0 for s in self.symbols}
        self.closes: Dict[str, List[float]] = {s: [] for s in self.symbols}
        self.highs: Dict[str, List[float]] = {s: [] for s in self.symbols}
        self.lows: Dict[str, List[float]] = {s: [] for s in self.symbols}
        self.volumes: Dict[str, List[float]] = {s: [] for s in self.symbols}

        host, port = (self.redis_addr.split(":") + ["6379"])[:2]
        self.sub = MiniRedis(host=host, port=int(port), password=self.redis_password, db=self.redis_db).connect()
        self.pub = MiniRedis(host=host, port=int(port), password=self.redis_password, db=self.redis_db).connect()

        self._load_config()
        self.trace = bool(self.cfg.get("log_trace") or self.cfg.get("debug"))
        self.log_every = max(1, _i(self.cfg.get("log_every"), 60))
        self.feed = MarketDataFeed(log=self._log)
        self.feed.start()

    # ---------- 配置 ----------
    def _load_config(self):
        def ratio(v, d):
            x = _f(v, d)
            return x / 100.0 if x > 1.0 else x   # 兼容 0.55 与 55 两种写法

        Config.MIN_CONFIDENCE = ratio(self.cfg.get("min_confidence"), Config.MIN_CONFIDENCE)
        Config.TP_RATIO = ratio(self.cfg.get("tp_ratio"), Config.TP_RATIO)
        Config.SL_RATIO = ratio(self.cfg.get("sl_ratio"), Config.SL_RATIO)
        Config.ATR_TP_MULT = _f(self.cfg.get("atr_tp_mult"), Config.ATR_TP_MULT)
        Config.ATR_SL_MULT = _f(self.cfg.get("atr_sl_mult"), Config.ATR_SL_MULT)
        Config.COOLDOWN_SEC = max(0, _i(self.cfg.get("cooldown_sec"), Config.COOLDOWN_SEC))
        Config.WARMUP_BARS = max(60, _i(self.cfg.get("warmup_bars"), Config.WARMUP_BARS))
        Config.MAX_BARS = max(200, _i(self.cfg.get("max_bars"), Config.MAX_BARS))
        Config.COST_MULT = max(0.0, _f(self.cfg.get("cost_mult"), Config.COST_MULT))
        Config.FEE_ROUND_TRIP = max(0.0, _f(self.cfg.get("fee_round_trip"), Config.FEE_ROUND_TRIP))
        Config.LONG_BIAS = _f(self.cfg.get("long_bias"), Config.LONG_BIAS)
        Config.SHORT_BIAS = _f(self.cfg.get("short_bias"), Config.SHORT_BIAS)
        Config.MAX_CANDIDATES = max(1, _i(self.cfg.get("max_candidates"), Config.MAX_CANDIDATES))

    # ---------- Redis 频道 ----------
    def _candle_ch(self):
        return "%s:candle:%s" % (self.prefix, self.strategy_id)

    def _signal_ch(self):
        return "%s:signal:%s" % (self.prefix, self.strategy_id)

    def _state_ch(self):
        return "%s:state:%s" % (self.prefix, self.strategy_id)

    def _log(self, msg: str):
        print("[%s] %s" % (_now(), msg), flush=True)

    def _append_bar(self, symbol, h, l, c, v=0.0):
        if symbol not in self.closes:
            return
        self.closes[symbol].append(float(c))
        self.highs[symbol].append(float(h))
        self.lows[symbol].append(float(l))
        self.volumes[symbol].append(float(v))
        for d in (self.closes, self.highs, self.lows, self.volumes):
            if len(d[symbol]) > Config.MAX_BARS:
                del d[symbol][0:len(d[symbol]) - Config.MAX_BARS]

    # ---------- 决策 ----------
    def evaluate(self, symbol: str):
        """返回 (direction, confidence, tp, sl, reason) 或 None。"""
        closes = self.closes[symbol]
        highs, lows, vols = self.highs[symbol], self.lows[symbol], self.volumes[symbol]
        price = closes[-1]

        a = atr_pct(highs, lows, closes)
        if a is None or a <= 0:
            return None
        vr = volume_ratio(vols)
        r = rsi(closes)
        td = trend_dir(closes)
        look = min(20, len(closes) - 1)
        mom = (closes[-1] / closes[-1 - look] - 1.0) if look > 0 and closes[-1 - look] > 0 else 0.0

        # ==== 成本闸门 —— 先算，不过就直接丢，省掉所有后续网络调用 ====
        # 预期持有 60 分钟能走的幅度，用 ATR 的 sqrt(时间) 缩放粗略估。
        expected = a * (Config.ATR_TP_MULT * 0.5)
        required = Config.FEE_ROUND_TRIP * Config.COST_MULT
        if expected < required:
            return None

        # ==== 技术面打分（对称）====
        long_s = short_s = 0.0
        why: List[str] = []
        if td == "up":
            long_s += 0.30; why.append("EMA多头")
        elif td == "down":
            short_s += 0.30; why.append("EMA空头")
        if r is not None:
            if r < 32:
                long_s += 0.22; why.append("RSI超卖%.0f" % r)
            elif r > 68:
                short_s += 0.22; why.append("RSI超买%.0f" % r)
        if mom > 0.004:
            long_s += 0.18
        elif mom < -0.004:
            short_s += 0.18
        if vr and vr > 1.3:
            # 放量只放大已有方向，不自己造方向
            if long_s > short_s:
                long_s += 0.10
            elif short_s > long_s:
                short_s += 0.10
            why.append("放量%.2fx" % vr)
        # 基础分，让中性行情也有基线置信度可被乘数拉开
        long_s += 0.30
        short_s += 0.30

        long_s *= Config.LONG_BIAS
        short_s *= Config.SHORT_BIAS

        if abs(long_s - short_s) < 0.05:
            return None
        direction = "long" if long_s > short_s else "short"
        base = max(long_s, short_s)

        # ==== 加密原生乘数 ====
        cm, sup, opp = crypto_adjust(symbol, direction, self.feed)
        # OI 分量【暂不给投票权】：30 天回测里它是唯一显著为负的分量
        # （h=4 非重叠 n=204 −1.4739% t=−2.20；对照 taker +0.2762% t=+4.91 承载几乎全部边际）。
        # 理由照记进 reason 用于实盘重测，但不改置信度、也不发降级豁免券 —— 一个自认不利的分量
        # 却在 `len(sup) + len(oi_why)` 里当支持项，等于给自己开豁免。实盘重测为正再恢复投票权。
        _, oi_why = oi_adjust(symbol, direction, self.feed, mom)
        conf = base * cm
        conf = max(0.0, min(conf, 1.0))

        # 反对项超过支持项 ⇒ 不干净，降级
        if len(opp) > len(sup):
            conf *= 0.6

        if conf < Config.MIN_CONFIDENCE:
            return None
        if opp and len(opp) >= 2:
            return None

        # ==== 止损/止盈 —— 成本感知 ====
        sl_dist = max(a * Config.ATR_SL_MULT, Config.FEE_ROUND_TRIP * Config.FEE_SL_FLOOR_MULT)
        tp_dist = sl_dist * (Config.ATR_TP_MULT / max(Config.ATR_SL_MULT, 1e-9))
        # 再保证 TP 至少覆盖成本
        tp_dist = max(tp_dist, Config.FEE_ROUND_TRIP * Config.COST_MULT)
        if direction == "long":
            tp, sl = price * (1 + tp_dist), price * (1 - sl_dist)
        else:
            tp, sl = price * (1 - tp_dist), price * (1 + sl_dist)

        reason = "%s | 支持:%s | 反对:%s | %s" % (
            " ".join(why), ",".join(sup) or "-", ",".join(opp) or "-", ",".join(oi_why) or "-")
        return direction, conf, tp, sl, reason

    def _load_history(self, msg: dict):
        """吃掉 Go 的历史回放，把预热从 60 分钟压到 0。

        协议：manager.go `startStrategyProcess` 之后，Go 对每个 symbol 调
        FetchCandles(sym,"1m",200)，打包成【一条】{"type":"history","candles":[...]}
        发到本实例的 candle 频道（redis_bus.go PublishHistory）。
        而 on_market_message 的 type 白名单只认 ""/"candle" ⇒ 这条消息原本被整条丢弃，
        引擎只能靠实时 bar 自己攒到 Config.WARMUP_BARS(实盘=60) ⇒ 每次启动/重启后
        有 60 分钟不出任何评估（v40 上线至今 0 笔的真因之一）。

        必须清空再灌：Go 的 resync 会周期性重发，且历史尾根与实时首根可能同 ts。
        历史是权威重建 ⇒ 直接以它为准，避免交错出重复序列污染 ATR/EMA。
        灌完把 last_bar_ts 设成最后一根的 ts，让紧随其后的同 ts bar 被 on_market_message
        的去重挡掉、下一根新 bar 正常追加并触发首次评估。
        """
        symbol = _s(msg.get("symbol")).strip()
        if not symbol or symbol not in self.closes:
            return
        bars = msg.get("candles") or []
        if not bars:
            return
        for d in (self.closes, self.highs, self.lows, self.volumes):
            del d[symbol][:]
        last_ts = ""
        for b in bars:
            if not isinstance(b, dict):
                continue
            c = _f(b.get("close"))
            if c <= 0:
                continue
            h = _f(b.get("high")); l = _f(b.get("low"))
            self._append_bar(symbol, h if h > 0 else c, l if l > 0 else c, c, _f(b.get("volume")))
            ts = b.get("timestamp")
            last_ts = str(ts) if ts not in (None, "", 0) else ""
        self.last_bar_ts[symbol] = last_ts
        self.recv_count[symbol] = 0

    def on_market_message(self, msg: dict):
        if not isinstance(msg, dict):
            return
        mtype = _s(msg.get("type")).lower()
        if mtype == "history":
            self._load_history(msg)
            return
        if mtype not in ("", "candle"):
            return
        symbol = _s(msg.get("symbol")).strip()
        if not symbol or symbol not in self.closes:
            return
        from_history = bool(msg.get("from_history"))

        h = _f(msg.get("high")); l = _f(msg.get("low"))
        c = _f(msg.get("close")); v = _f(msg.get("volume"))
        if c <= 0:
            return
        h = h if h > 0 else c
        l = l if l > 0 else c

        ts_raw = msg.get("timestamp")
        ts_key = str(ts_raw) if ts_raw not in (None, "", 0) else ""
        if ts_key:
            if self.last_bar_ts.get(symbol, "") == ts_key:
                return
            self.last_bar_ts[symbol] = ts_key

        self._append_bar(symbol, h, l, c, v)
        self.recv_count[symbol] = int(self.recv_count.get(symbol) or 0) + 1
        n = self.recv_count[symbol]

        if len(self.closes[symbol]) < Config.WARMUP_BARS:
            if n % self.log_every == 0:
                self._log("预热不足 sym=%s %d/%d" % (symbol, len(self.closes[symbol]), Config.WARMUP_BARS))
            return
        if from_history:
            return

        out = self.evaluate(symbol)
        if out is None:
            if self.trace or n % self.log_every == 0:
                a = atr_pct(self.highs[symbol], self.lows[symbol], self.closes[symbol])
                exp = (a or 0.0) * (Config.ATR_TP_MULT * 0.5)
                fr = self.feed.funding(symbol)
                self._log("无信号 sym=%s 价=%.8g ATR%%=%.3f 预期%.4f%% 需%.4f%% 资金费率=%s" %
                          (symbol, c, (a or 0) * 100, exp * 100,
                           Config.FEE_ROUND_TRIP * Config.COST_MULT * 100,
                           ("%.5f" % fr) if fr is not None else "无"))
            return

        direction, conf, tp, sl, reason = out
        now = time.time()
        last = float(self.last_signal_ts.get(symbol) or 0.0)
        if Config.COOLDOWN_SEC > 0 and last > 0 and now - last < Config.COOLDOWN_SEC:
            return
        self.last_signal_ts[symbol] = now
        self._log("★信号 sym=%s 方向=%s 置信度=%.3f 价=%.8g tp=%.8g sl=%.8g 理由=%s" %
                  (symbol, direction, conf, c, tp, sl, reason))
        self._emit_signal(symbol, direction, c, tp, sl, conf)

    def _emit_signal(self, symbol, direction, entry_price, tp, sl, confidence):
        side = "buy" if direction == "long" else "sell"
        amount = _f(self.cfg.get("trade_amount", self.cfg.get("base_trade_usdt")), 0.01)
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
            "signal_id": "%s:%s:%s:%d" % (self.strategy_id, symbol, boot_short, time.time_ns()),
            "generated_at": datetime.now(tz=timezone.utc).isoformat(),
            "confidence": float(confidence),
        }
        try:
            self.pub.publish(self._signal_ch(), json.dumps(msg))
        except Exception as e:
            self._log("[ERROR] 信号 publish 失败 sym=%s err=%r" % (symbol, e))

    def run(self):
        if not self.strategy_id:
            raise RuntimeError("missing strategy_id")
        self.sub.subscribe(self._candle_ch())
        self.pub.publish(self._state_ch(), json.dumps(
            {"type": "ready", "strategy_id": self.strategy_id, "boot_id": self.boot_id, "engine": "v40"}))
        t = threading.Thread(target=self._heartbeat_loop, daemon=True)
        t.start()
        self._log("START v40 strategy_id=%s symbols=%d candle_ch=%s 成本闸门: 预期≥%.3f%% "
                  "(费%.3f%%×%.1f) min_conf=%.2f 无多空偏置" %
                  (self.strategy_id, len(self.symbols), self._candle_ch(),
                   Config.FEE_ROUND_TRIP * Config.COST_MULT * 100,
                   Config.FEE_ROUND_TRIP * 100, Config.COST_MULT, Config.MIN_CONFIDENCE))
        last_idle = time.time()
        while True:
            item = self.sub.read_pubsub_message()
            if not item:
                if time.time() - last_idle >= 30:
                    last_idle = time.time()
                    self._log("IDLE 等待K线 recv=%d 资金费率缓存=%d" %
                              (sum(self.recv_count.values()), len(self.feed._premium)))
                continue
            payload = item.get("data")
            if not payload:
                continue
            try:
                msg = json.loads(payload)
            except Exception:
                continue
            self.on_market_message(msg)

    def _heartbeat_loop(self):
        while True:
            try:
                self.pub.publish(self._state_ch(), json.dumps({
                    "type": "heartbeat", "strategy_id": self.strategy_id,
                    "boot_id": self.boot_id, "ts": _now(),
                    "symbols": len(self.symbols),
                    "premium_cached": len(self.feed._premium),
                    "http_ok": self.feed._ok_calls,
                    "http_err": self.feed._http_errors,
                }))
            except Exception:
                pass
            time.sleep(20)


# ======================================================================
# 6. 自检 —— 只读，不下单、不写库
# ======================================================================
def selftest():
    md = MarketDataFeed(log=lambda m: print(m))
    print("=== 1) premiumIndex 全宇宙 ===")
    n = md.refresh_premium(force=True)
    print("  拿到 %d 个永续合约" % n)
    if n == 0:
        print("  ❌ 拿不到数据，先查网络/UA")
        return 1

    print("\n=== 2) 抽样：资金费率 + 基差 ===")
    rows = sorted(md._premium.items(), key=lambda kv: -abs(kv[1].get("funding") or 0.0))[:8]
    print("  %-18s %12s %10s %12s" % ("symbol", "funding", "basis%", "mark"))
    for sym, d in rows:
        ix = d.get("index") or 0.0
        print("  %-18s %12.5f %10.4f %12.8g" %
              (sym, d.get("funding") or 0.0,
               ((d.get("mark") or 0.0) / ix - 1.0) * 100 if ix > 0 else 0.0,
               d.get("mark") or 0.0))

    print("\n=== 3) 抽样：/futures/data/* 四个端点（取资金费率最极端的 2 个）===")
    for sym, _ in rows[:2]:
        print("  %s: OI变化=%s  散户多空=%s  大户多空=%s  主动买卖=%s" % (
            sym,
            ("%+.2f%%" % (md.oi_change(sym) * 100)) if md.oi_change(sym) is not None else "n/a",
            md.retail_ls(sym), md.top_ls(sym), md.taker_ratio(sym)))

    print("\n=== 4) 成本闸门自检（纯算术，无网络）===")
    req = Config.FEE_ROUND_TRIP * Config.COST_MULT
    print("  来回费 %.3f%% × %.1f = 需要预期波动 ≥ %.3f%%" %
          (Config.FEE_ROUND_TRIP * 100, Config.COST_MULT, req * 100))
    print("  引擎实测 ATR%% 中位 0.3963%% ⇒ 预期 = ATR × %.2f = %.4f%%  %s" %
          (Config.ATR_TP_MULT * 0.5, 0.003963 * Config.ATR_TP_MULT * 0.5 * 100,
           "✅ 过闸" if 0.003963 * Config.ATR_TP_MULT * 0.5 >= req else "❌ 过不了，ATR 太低的会被全砍"))

    print("\n=== 5) 对称性自检 ===")
    print("  LONG_BIAS=%.2f  SHORT_BIAS=%.2f  ⇒ %s" %
          (Config.LONG_BIAS, Config.SHORT_BIAS,
           "✅ 对称" if Config.LONG_BIAS == Config.SHORT_BIAS else "❌ 仍有偏置"))
    print("  HTTP 成功=%d 失败=%d" % (md._ok_calls, md._http_errors))
    return 0


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--selftest":
        sys.exit(selftest())
    cfg = json.loads(sys.argv[1] if len(sys.argv) > 1 else "{}")
    Strategy(cfg).run()
