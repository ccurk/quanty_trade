#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""v40 信号边际回测 —— 只读、不下单、不写库。

要回答的唯一问题：v40 新增的"加密原生乘数"在这个宇宙上有没有方向边际？

为什么这么设计：
  · 不重写评分逻辑 —— import v40 本体，用历史数组伪装成 MarketDataFeed，
    调真实的 crypto_adjust / oi_adjust / Strategy.evaluate。抄一份就等于测别的东西。
  · 分两层测。evaluate() 里 tech 部分（EMA/RSI/momentum）是已证 ≈0 边际的旧 TA；
    只测"v40 会开的单赚不赚"会被旧 TA 稀释掉、什么都说明不了。
      A) 隔离测：只有乘数，没有 tech。
      B) 全引擎：跑真实 evaluate()。
  · lag=1：决策时点 i 只用 i-1 及更早的数据。彻底排除前视。
  · 三个纪律：非重叠子样本 t（保守）、逐笔成本 0.10%、随机方向负对照。

用法：python3 backtest_v40_signals.py [--symbols N] [--days 30]
"""
import argparse
import json
import math
import os
import random
import sys
import time
import urllib.request
from datetime import datetime, timezone

UA = {"User-Agent": "Mozilla/5.0 (compatible; quanty-v40/1.0)"}
FAPI = "https://fapi.binance.com"
FEE_ROUND_TRIP = 0.0010          # 与 Config.FEE_ROUND_TRIP 一致
HOUR = 3600 * 1000


def http(path, params, tries=3):
    q = "&".join("%s=%s" % (k, v) for k, v in params.items())
    url = FAPI + path + "?" + q
    for a in range(tries):
        try:
            req = urllib.request.Request(url, headers=UA)
            with urllib.request.urlopen(req, timeout=12) as r:
                return json.loads(r.read().decode())
        except Exception as e:
            if a == tries - 1:
                sys.stderr.write("HTTP FAIL %s %r\n" % (path, e))
                return None
            time.sleep(1.0 + a)
    return None


# ----------------------------------------------------------------------
# 1. 取数
# ----------------------------------------------------------------------
def top_symbols(n):
    d = http("/fapi/v1/ticker/24hr", {})
    if not isinstance(d, list):
        return []
    rows = []
    for t in d:
        s = t.get("symbol", "")
        if not s.endswith("USDT"):
            continue
        if "_" in s:                      # 交割合约
            continue
        try:
            qv = float(t.get("quoteVolume") or 0)
        except Exception:
            continue
        rows.append((qv, s))
    rows.sort(reverse=True)
    return [s for _, s in rows[:n]]


def paged(path, base, span_days, limit=500, klines=False):
    """往前翻页取满 span_days。返回 [(bucket_start_ms, row), ...] 升序去重。

    klines=True 时行是数组（[openTime, o, h, l, c, ...]），时间戳在 [0]；
    其余 /futures/data/* 返回 dict，时间戳在 ["timestamp"]。
    """
    end = int(time.time() * 1000)
    start = end - span_days * 86400 * 1000
    out, seen = [], set()
    cur_end = end
    for _ in range(8):
        p = dict(base)
        p["limit"] = limit
        p["endTime"] = cur_end
        p["startTime"] = start
        r = http(path, p)
        if not isinstance(r, list) or not r:
            break
        added = 0
        page_min = None
        for row in r:
            raw = row[0] if klines else (row or {}).get("timestamp")
            if raw is None:
                continue
            ts = (int(raw) // HOUR) * HOUR
            if page_min is None or ts < page_min:
                page_min = ts
            if ts in seen:
                continue
            seen.add(ts)
            out.append((ts, row))
            added += 1
        if added == 0 or page_min is None:
            break
        # 第一页就够到区间起点时，下一页会请求 [start, start-1] 这个倒置区间 ⇒ 400。
        if page_min <= start:
            break
        cur_end = page_min - 1
        time.sleep(0.30)
    out.sort(key=lambda x: x[0])
    return out


def fetch_symbol(sym, days):
    kl = paged("/fapi/v1/klines", {"symbol": sym, "interval": "1h"},
               days + 3, limit=1000, klines=True)
    if not kl:
        return None
    bars = {}
    for ts, k in kl:
        try:
            bars[ts] = {
                "open": float(k[1]), "high": float(k[2]),
                "low": float(k[3]), "close": float(k[4]),
                "volume": float(k[5]),
            }
        except Exception:
            continue
    for path, key, field in [
        ("/futures/data/openInterestHist", "oi", "sumOpenInterest"),
        ("/futures/data/globalLongShortAccountRatio", "gls", "longShortRatio"),
        ("/futures/data/topLongShortPositionRatio", "tls", "longShortRatio"),
        ("/futures/data/takerlongshortRatio", "taker", "buySellRatio"),
    ]:
        for ts, r in paged(path, {"symbol": sym, "period": "1h"}, days):
            if ts not in bars:
                continue
            try:
                bars[ts][key] = float((r or {}).get(field))
            except Exception:
                pass
        time.sleep(0.20)

    # 资金费率：结算在 00/08/16 UTC，把最近一次已结算的费率铺到之后每个小时
    fr = http("/fapi/v1/fundingRate", {"symbol": sym, "limit": 1000})
    fund = []
    if isinstance(fr, list):
        for f in fr:
            try:
                fund.append((int(f["fundingTime"]), float(f["fundingRate"])))
            except Exception:
                pass
        fund.sort()
    fi = 0
    cur = None
    for ts in sorted(bars):
        while fi < len(fund) and fund[fi][0] <= ts:
            cur = fund[fi][1]
            fi += 1
        if cur is not None:
            bars[ts]["funding"] = cur

    rows = [bars[t] for t in sorted(bars) if bars[t].get("close", 0) > 0]
    if len(rows) < 200:
        return None
    return {"symbol": sym, "rows": rows, "ts": sorted(bars)}


# ----------------------------------------------------------------------
# 2. 历史 feed —— 伪装成 v40.MarketDataFeed 的接口
# ----------------------------------------------------------------------
class HistFeed:
    """决策时点 i 只用 i-lag 及更早的数据。lag=1 ⇒ 彻底排除前视。

    only: None    = 全部输入
          "none"  = 全部屏蔽（跑纯 tech，做决定性对照）
          其余     = 只用该分量（隔离测）
    """

    def __init__(self, rows, lag=1, only=None):
        self.rows = rows
        self.lag = lag
        self.only = only
        self.i = 0
        self.n_hit = {}          # 分量 -> 有值次数，用于算触发率

    def at(self, i):
        self.i = i
        return self

    def _v(self, key):
        j = self.i - self.lag
        if j < 0 or j >= len(self.rows):
            return None
        v = self.rows[j].get(key)
        if v is not None:
            self.n_hit[key] = self.n_hit.get(key, 0) + 1
        return v

    def _use(self, key):
        if self.only == "none":
            return False
        return self.only in (None, key)

    def funding(self, symbol):
        return self._v("funding") if self._use("funding") else None

    def basis(self, symbol):
        # 历史基差在本回测里不取（premiumIndex 只有实时值）。
        # 资金费率本身就是基差的函数 ⇒ 信息不丢，但 basis 这一条未被检验。
        return None

    def retail_ls(self, symbol):
        return self._v("gls") if self._use("gls") else None

    def top_ls(self, symbol):
        return self._v("tls") if self._use("tls") else None

    def taker_ratio(self, symbol):
        return self._v("taker") if self._use("taker") else None

    def oi_change(self, symbol, period="5m", limit=12):
        if not self._use("oi"):
            return None
        j = self.i - self.lag
        k = j - 1
        if k < 0:
            return None
        a, b = self.rows[k].get("oi"), self.rows[j].get("oi")
        if not a or not b or a <= 0:
            return None
        self.n_hit["oi"] = self.n_hit.get("oi", 0) + 1
        return b / a - 1.0


# ----------------------------------------------------------------------
# 3. 统计 —— 非重叠子样本是headline，重叠样本只做参考
# ----------------------------------------------------------------------
def stats(xs, horizon, label):
    n = len(xs)
    if n < 20:
        return None
    mean = sum(xs) / n
    var = sum((x - mean) ** 2 for x in xs) / max(n - 1, 1)
    sd = math.sqrt(var)
    se_naive = sd / math.sqrt(n)
    # 非重叠：每 horizon 抽一个
    no = xs[::horizon]
    nn = len(no)
    mean_no = sum(no) / nn if nn else 0.0
    var_no = sum((x - mean_no) ** 2 for x in no) / max(nn - 1, 1)
    se_no = math.sqrt(var_no / nn) if nn > 1 else float("inf")
    t_no = mean_no / se_no if se_no > 0 else 0.0
    srt = sorted(xs)
    med = srt[n // 2]
    # block bootstrap：块长 = horizon，保住自相关结构
    rnd = random.Random(12345)
    nb = max(horizon * 2, 20)
    boots = []
    for _ in range(400):
        acc = []
        while len(acc) < n:
            st = rnd.randrange(n)
            acc.extend(xs[st:st + horizon])
        boots.append(sum(acc[:n]) / n)
    boots.sort()
    lo, hi = boots[int(0.05 * len(boots))], boots[int(0.95 * len(boots))]
    net = mean_no - FEE_ROUND_TRIP
    return {
        "label": label, "h": horizon, "n": n, "n_no": nn,
        "mean": mean, "mean_no": mean_no, "net_no": net, "med": med,
        "se_naive": se_naive, "t_naive": mean / se_naive if se_naive else 0,
        "t_no": t_no, "ci90": (lo, hi),
        "win": sum(1 for x in xs if x > 0) / n,
    }


def show(s):
    if not s:
        return
    print("  h=%-3d n=%-6d 重叠n=%-5d 均%+.4f%% 中位%+.4f%% | 非重叠 均%+.4f%% t=%+.2f"
          " | 扣费%+.4f%% | 90%%CI[%+.4f%%,%+.4f%%] 胜率%.1f%%"
          % (s["h"], s["n"], s["n_no"], s["mean"] * 100, s["med"] * 100,
             s["mean_no"] * 100, s["t_no"], s["net_no"] * 100,
             s["ci90"][0] * 100, s["ci90"][1] * 100, s["win"] * 100))


# ----------------------------------------------------------------------
# 4. 主流程
# ----------------------------------------------------------------------
HORIZONS = [1, 2, 4, 8, 12]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--symbols", type=int, default=40)
    ap.add_argument("--days", type=int, default=30)
    ap.add_argument("--lag", type=int, default=1)
    ap.add_argument("--v40", default="/tmp/v40.py")
    ap.add_argument("--cache", default="/tmp/bt_cache.json",
                    help="取数缓存；存在则直接读，省掉 185s 的取数")
    args = ap.parse_args()

    sys.path.insert(0, os.path.dirname(os.path.abspath(args.v40)))
    import importlib.util
    spec = importlib.util.spec_from_file_location("v40", args.v40)
    v40 = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(v40)
    print("已载入 v40 本体: %s" % args.v40)

    syms = top_symbols(args.symbols)
    print("宇宙: %d 个 USDT 永续（按 24h 名义额取前 %d）" % (len(syms), args.symbols))

    data = None
    if args.cache and os.path.exists(args.cache):
        try:
            with open(args.cache, encoding="utf-8") as fh:
                data = json.load(fh)
            print("取数缓存命中: %s (%d 个符号)" % (args.cache, len(data)))
        except Exception as e:
            print("缓存读取失败，重新取数: %r" % e)
            data = None
    if not data:
        data = []
        t0 = time.time()
        for i, s in enumerate(syms):
            d = fetch_symbol(s, args.days)
            if d:
                data.append(d)
            if (i + 1) % 5 == 0:
                print("  取数 %d/%d  已用 %.0fs  成功 %d" % (i + 1, len(syms), time.time() - t0, len(data)))
        print("取数完成: %d 个符号可用, 用时 %.0fs\n" % (len(data), time.time() - t0))
        if args.cache:
            with open(args.cache, "w", encoding="utf-8") as fh:
                json.dump(data, fh, ensure_ascii=False)
            print("已写缓存 %s\n" % args.cache)
    if not data:
        return

    for d in data:
        r = d["rows"]
        cov = [k for k in ("oi", "gls", "tls", "taker", "funding") if sum(1 for x in r if k in x) > 0.5 * len(r)]
        print("  %-12s bars=%-5d 覆盖: %s" % (d["symbol"], len(r), ",".join(cov)))
    print()

    # ---- 负对照：随机方向 ----
    rnd = random.Random(7)
    neg = {h: [] for h in HORIZONS}
    for d in data:
        rows = d["rows"]
        for i in range(80, len(rows) - max(HORIZONS) - 1):
            c0 = rows[i]["close"]
            for h in HORIZONS:
                r = rows[i + h]["close"] / c0 - 1.0
                neg[h].append(r if rnd.random() < 0.5 else -r)
    print("=== 负对照：随机方向（应当 ≈0，用来验证机器本身没偏）===")
    for h in HORIZONS:
        show(stats(neg[h], h, "random"))
    print()

    # ---- Test A：隔离测乘数 ----
    print("=== Test A：加密原生乘数（无 tech 稀释）===")
    print("    s = log(M_long) - log(M_short)；按 s 的方向持有 h 小时。")
    for h in HORIZONS:
        edges = []
        for d in data:
            rows = d["rows"]
            feed = HistFeed(rows, args.lag)
            for i in range(80, len(rows) - max(HORIZONS) - 1):
                feed.at(i)
                ml = v40.crypto_adjust(d["symbol"], "long", feed)[0] * \
                    v40.oi_adjust(d["symbol"], "long", feed, 0.0)[0]
                ms = v40.crypto_adjust(d["symbol"], "short", feed)[0] * \
                    v40.oi_adjust(d["symbol"], "short", feed, 0.0)[0]
                if ml <= 0 or ms <= 0:
                    continue
                s = math.log(ml) - math.log(ms)
                if abs(s) < 1e-9:
                    continue
                r = rows[i + h]["close"] / rows[i]["close"] - 1.0
                edges.append(r if s > 0 else -r)
        show(stats(edges, h, "A-all"))

    # ---- Test A2：逐分量隔离 ----
    print("\n=== Test A2：单个分量（其余屏蔽）h=4 ===")
    for comp in ["funding", "gls", "tls", "taker", "oi"]:
        edges = []
        for d in data:
            rows = d["rows"]
            feed = HistFeed(rows, args.lag, only=comp)
            for i in range(80, len(rows) - max(HORIZONS) - 1):
                feed.at(i)
                ml = v40.crypto_adjust(d["symbol"], "long", feed)[0] * \
                    v40.oi_adjust(d["symbol"], "long", feed, 0.0)[0]
                ms = v40.crypto_adjust(d["symbol"], "short", feed)[0] * \
                    v40.oi_adjust(d["symbol"], "short", feed, 0.0)[0]
                if ml <= 0 or ms <= 0:
                    continue
                s = math.log(ml) - math.log(ms)
                if abs(s) < 1e-9:
                    continue
                r = rows[i + 4]["close"] / rows[i]["close"] - 1.0
                edges.append(r if s > 0 else -r)
        st = stats(edges, 4, comp)
        if st:
            print("  %-8s n=%-6d 非重叠 n=%-5d 均%+.4f%% t=%+.2f 扣费%+.4f%%"
                  % (comp, st["n"], st["n_no"], st["mean_no"] * 100, st["t_no"], st["net_no"] * 100))
        else:
            print("  %-8s 样本不足" % comp)

    # ---- Test B / C：全引擎 evaluate()，加密输入 开 vs 关 ----
    def run_engine(mode, title):
        sig = {h: [] for h in HORIZONS}
        n_eval = n_fired = 0
        hits = {}
        for d in data:
            rows = d["rows"]
            st = v40.Strategy.__new__(v40.Strategy)
            st.closes = {d["symbol"]: []}
            st.highs = {d["symbol"]: []}
            st.lows = {d["symbol"]: []}
            st.volumes = {d["symbol"]: []}
            st.feed = HistFeed(rows, args.lag, only=mode)
            for i in range(len(rows)):
                st.closes[d["symbol"]].append(rows[i]["close"])
                st.highs[d["symbol"]].append(rows[i]["high"])
                st.lows[d["symbol"]].append(rows[i]["low"])
                st.volumes[d["symbol"]].append(rows[i]["volume"])
                # 与真实引擎一致：_append_bar 把历史截到 Config.MAX_BARS
                for arr in (st.closes, st.highs, st.lows, st.volumes):
                    if len(arr[d["symbol"]]) > v40.Config.MAX_BARS:
                        del arr[d["symbol"]][:-v40.Config.MAX_BARS]
                if i < 80 or i >= len(rows) - max(HORIZONS) - 1:
                    continue
                st.feed.at(i)
                n_eval += 1
                out = st.evaluate(d["symbol"])
                if not out:
                    continue
                n_fired += 1
                sign = 1.0 if out[0] == "long" else -1.0
                for h in HORIZONS:
                    r = rows[i + h]["close"] / rows[i]["close"] - 1.0
                    sig[h].append(sign * r)
            for k, v in st.feed.n_hit.items():
                hits[k] = hits.get(k, 0) + v
        print("\n=== %s ===" % title)
        print("  evaluate 调用 %d 次, 出信号 %d 次 (%.2f%%)"
              % (n_eval, n_fired, 100.0 * n_fired / max(n_eval, 1)))
        if mode != "none":
            print("  输入有值率: " + "  ".join(
                "%s=%.0f%%" % (k, 100.0 * v / max(n_eval, 1)) for k, v in sorted(hits.items())))
        for h in HORIZONS:
            show(stats(sig[h], h, "engine"))
        return {h: stats(sig[h], h, "engine") for h in HORIZONS}

    b = run_engine(None, "Test B：真实 v40.evaluate()（加密输入 开）")
    c = run_engine("none", "Test C【决定性对照】：同一 evaluate()，加密输入 全关（纯 tech）")

    print("\n=== B − C：加密数据到底加了多少边际？ ===")
    print("  （这是本次回测的核心问题；若差值的 |t| < 2，则新数据没有可测贡献）")
    for h in HORIZONS:
        if not b[h] or not c[h]:
            continue
        d_mean = b[h]["mean_no"] - c[h]["mean_no"]
        print("  h=%-3d  B %+.4f%%   C %+.4f%%   差 %+.4f%%   (B n=%d / C n=%d)"
              % (h, b[h]["mean_no"] * 100, c[h]["mean_no"] * 100, d_mean * 100,
                 b[h]["n_no"], c[h]["n_no"]))

    print("\n⚠️ 纪律提醒：非重叠 t 是 headline；|t|<2 一律不报为发现。")
    print("⚠️ 30 天窗口 = 单一市场状态，不可外推到其他行情。")


if __name__ == "__main__":
    main()
