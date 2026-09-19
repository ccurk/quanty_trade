#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""门 ①：Majors 池（BTC/ETH/BNB/SOL）在 15m bar 上，费后是否 ≥2 币为正。

★ v2 修了 v1 的三个错（每个都会静默改答案）：
  1. paged 同时发 startTime+endTime ⇒ 币安从【最早】返回 limit 根 ⇒ 第一页就 break
     ⇒ 拿到的是最旧那 15.6 天、且隔了 17 天。改为【只发 endTime，向后走】。
  2. tstat() 传了 dict ⇒ sum(dict) 求的是【键】(毫秒时间戳) ⇒ 逐币格印出 1.78e14%。
     改为 list(d.values())。
  3. ATR 与引擎的 0.398% 对比是 mean vs median ⇒ 不可比。改为 mean/median 都印，
     并在【同一窗口】抓 1m 做同方法对照，直接量 √15 缩放。
"""
import argparse, importlib.util, json, math, os, time, urllib.error, urllib.parse, urllib.request

BUCKET = 15 * 60 * 1000
FEE_RT = 0.0010
SYMS = ["BTCUSDT", "ETHUSDT", "BNBUSDT", "SOLUSDT"]
HORIZONS = [1, 4, 16, 32]
FAPI = "https://fapi.binance.com"


def load(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


def http(path, params):
    q = urllib.parse.urlencode(params)
    req = urllib.request.Request(FAPI + path + "?" + q, headers={"User-Agent": "Mozilla/5.0"})
    for attempt in range(3):
        try:
            return json.load(urllib.request.urlopen(req, timeout=25))
        except urllib.error.HTTPError as e:
            body = ""
            try:
                body = e.read().decode("utf-8", "replace")[:300]
            except Exception:
                pass
            if 400 <= e.code < 500:      # 4xx 重试无意义，把 body 报出来
                raise RuntimeError("HTTP %d %s :: %s" % (e.code, path, body)) from e
            if attempt == 2:
                raise
            time.sleep(1.0)
        except Exception:
            if attempt == 2:
                raise
            time.sleep(1.0)


def walk_back(path, base, days, bucket_ms, limit, klines):
    """只发 endTime，每次往前挪一页（★ 发 startTime 会让币安从最早返回并立刻 break）。"""
    floor = int(time.time() * 1000) - days * 86400 * 1000
    cur_end, out, seen = int(time.time() * 1000), [], set()
    for _ in range(24):
        p = dict(base); p.update({"limit": limit, "endTime": cur_end})
        r = http(path, p)
        if not isinstance(r, list) or not r:
            break
        page_min, added = None, 0
        for row in r:
            raw = row[0] if klines else (row or {}).get("timestamp")
            if raw is None:
                continue
            ts = (int(raw) // bucket_ms) * bucket_ms
            if page_min is None or ts < page_min:
                page_min = ts
            if ts in seen:
                continue
            seen.add(ts); out.append((ts, row)); added += 1
        if added == 0 or page_min is None or page_min <= floor:
            break
        cur_end = page_min - 1
        time.sleep(0.12)
    out.sort(key=lambda x: x[0])
    return out


def fetch(sym, days, interval="15m", bucket=BUCKET, klimit=1500, want_crypto=True):
    kl = walk_back("/fapi/v1/klines", {"symbol": sym, "interval": interval},
                  days, bucket, klimit, True)
    if not kl:
        return None
    bars, order = {}, []
    for ts, k in kl:
        try:
            if ts not in bars:
                order.append(ts)
            bars[ts] = {"open": float(k[1]), "high": float(k[2]), "low": float(k[3]),
                        "close": float(k[4]), "volume": float(k[5])}
        except Exception:
            continue
    if want_crypto:
        for path, key, field in [
            ("/futures/data/openInterestHist", "oi", "sumOpenInterest"),
            ("/futures/data/globalLongShortAccountRatio", "gls", "longShortRatio"),
            ("/futures/data/topLongShortPositionRatio", "tls", "longShortRatio"),
            ("/futures/data/takerlongshortRatio", "taker", "buySellRatio"),
        ]:
            for ts, r in walk_back(path, {"symbol": sym, "period": interval},
                                   min(days, 29), bucket, 1000, False):
                if ts in bars:
                    try:
                        bars[ts][key] = float((r or {}).get(field))
                    except Exception:
                        pass
            time.sleep(0.20)
    fr = http("/fapi/v1/fundingRate", {"symbol": sym, "limit": 1000})
    fund = []
    if isinstance(fr, list):
        for f in fr:
            try:
                fund.append((int(f["fundingTime"]), float(f["fundingRate"])))
            except Exception:
                pass
        fund.sort()
    fi, cur = 0, None
    for ts in order:
        while fi < len(fund) and fund[fi][0] <= ts:
            cur = fund[fi][1]; fi += 1
        if cur is not None:
            bars[ts]["funding"] = cur
    keep = [t for t in order if bars[t].get("close", 0) > 0]
    if len(keep) < 300:
        return None
    return {"symbol": sym, "rows": [bars[t] for t in keep], "ts": keep}


def tr_stats(rows):
    """TR/close 的 均值 与 中位 —— 引擎报的 0.398% 是【中位】，必须同口径比。"""
    v = []
    for i in range(1, len(rows)):
        h, l, pc = rows[i]["high"], rows[i]["low"], rows[i - 1]["close"]
        if pc > 0:
            v.append(max(h - l, abs(h - pc), abs(l - pc)) / pc)
    if len(v) < 20:
        return None
    v.sort()
    return sum(v) / len(v), v[len(v) // 2], len(v)


def tstat(d):
    n = len(d)
    if n < 20:
        return None
    mu = sum(d) / n
    var = sum((x - mu) ** 2 for x in d) / (n - 1)
    se = math.sqrt(var / n)
    return n, mu, se, (mu / se if se else 0.0)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--days", type=int, default=30)
    ap.add_argument("--cache", default="/tmp/bt_15m_cache.json")
    ap.add_argument("--v40", default="/tmp/v40.py")
    ap.add_argument("--bt", default="/tmp/bt_v40.py")
    args = ap.parse_args()
    v40, bt = load(args.v40, "v40"), load(args.bt, "bt")

    if os.path.exists(args.cache):
        data = json.load(open(args.cache, encoding="utf-8"))
        print("缓存命中 (%d 符号)" % len(data))
    else:
        data = []
        for s in SYMS:
            d = fetch(s, args.days)
            if d:
                cov = {k: sum(1 for x in d["rows"] if k in x)
                       for k in ("oi", "gls", "tls", "taker", "funding")}
                print("  %-9s %4d 根15m  %s" % (s, len(d["rows"]),
                      " ".join("%s=%d" % kv for kv in cov.items())))
                data.append(d)
            time.sleep(0.3)
        json.dump(data, open(args.cache, "w", encoding="utf-8"), ensure_ascii=False)
    if not data:
        print("取数失败"); return

    syms = [d["symbol"] for d in data]
    t0, t1 = data[0]["ts"][0] // 1000, data[0]["ts"][-1] // 1000
    print("\n窗口 %s -> %s UTC   %d 根15m = %.1f 天"
          % (time.strftime("%m-%d %H:%M", time.gmtime(t0)),
             time.strftime("%m-%d %H:%M", time.gmtime(t1)),
             len(data[0]["ts"]), len(data[0]["ts"]) * 0.25 / 24))

    # 按 (ts, sym) 建网格
    arr = {s: {} for s in syms}; tsset = set()
    for d in data:
        for t, r in zip(d["ts"], d["rows"]):
            arr[d["symbol"]][t] = r["close"]; tsset.add(t)
    ts_list = sorted(tsset); pos = {t: i for i, t in enumerate(ts_list)}

    def fwd(sym, ts, h):
        k = pos.get(ts)
        if k is None or k + h >= len(ts_list):
            return None
        c0, c1 = arr[sym].get(ts), arr[sym].get(ts_list[k + h])
        return (c1 / c0 - 1.0) if (c0 and c1) else None

    _xs = {}
    def xs(ts, h):
        key = (ts, h)
        if key not in _xs:
            v = [x for x in (fwd(s, ts, h) for s in syms) if x is not None]
            _xs[key] = sum(v) / len(v) if v else None
        return _xs[key]

    # ★ 窗口行情：判断多头账本是不是踩上涨
    print("\n窗口行情（4 币等权，首→尾）:")
    for s in syms:
        f, l = arr[s].get(ts_list[0]), arr[s].get(ts_list[-1])
        if f and l:
            print("  %-9s %+.3f%%" % (s, 100 * (l / f - 1)))
    print("  等权均值 %+.3f%%" % (100 * sum(arr[s][ts_list[-1]] / arr[s][ts_list[0]] - 1
                                          for s in syms if arr[s].get(ts_list[0]) and arr[s].get(ts_list[-1])) / len(syms)))

    # ---- 机制：ATR 缩放（15m vs 同窗口 1m）----
    print("\n" + "=" * 66)
    print("机制：15m 到底有没有把 ATR 放大？（同窗口 1m 同方法对照）")
    print("=" * 66)
    tot = []
    for d in data[:2]:
        s = d["symbol"]; a15 = tr_stats(d["rows"])
        m1 = fetch(s, min(args.days, 7), interval="1m", bucket=60 * 1000,
                   klimit=1500, want_crypto=False)
        a1 = tr_stats(m1["rows"]) if m1 else None
        if a15:

            if a1:
                print("  %-9s 15m: 均值%.4f%% 中位%.4f%%  |  1m(7d): 均值%.4f%% 中位%.4f%%  ⇒ 中位比 %.2f×"
                      % (s, a15[0] * 100, a15[1] * 100, a1[0] * 100, a1[1] * 100,
                         a15[1] / a1[1] if a1[1] else 0))
            else:
                print("  %-9s 15m: 均值%.4f%% 中位%.4f%%  (1m 取数失败)"
                      % (s, a15[0] * 100, a15[1] * 100))
            tot.append(a15[1])
    if tot:
        md = sum(tot) / len(tot)
        print("  ⇒ 15m ATR 中位 %.4f%% ⇒ 2×ATR 止损 %.4f%% = 来回费的 %.1f×"
              % (md * 100, 2 * md * 100, 2 * md / FEE_RT))
    print("  参照（上一轮实测，40 币宇宙 1h bar 的引擎 ATR 中位）：0.398%% ⇒ 2×ATR=0.796%%=7.96×")

    # ---- 引擎 ----
    sigB, sigC = [], []
    for d in data:
        sym, rows = d["symbol"], d["rows"]
        sts = []
        for only in (None, "none"):
            st = v40.Strategy.__new__(v40.Strategy)
            st.closes, st.highs, st.lows, st.volumes = {sym: []}, {sym: []}, {sym: []}, {sym: []}
            st.feed = bt.HistFeed(rows, 1, only=only)
            sts.append(st)
        stB, stC = sts
        for i in range(len(rows)):
            for st in sts:
                st.closes[sym].append(rows[i]["close"]); st.highs[sym].append(rows[i]["high"])
                st.lows[sym].append(rows[i]["low"]);     st.volumes[sym].append(rows[i]["volume"])
                for a2 in (st.closes, st.highs, st.lows, st.volumes):
                    if len(a2[sym]) > v40.Config.MAX_BARS:
                        del a2[sym][:-v40.Config.MAX_BARS]
            if i < 100:
                continue
            for st, box in ((stB, sigB), (stC, sigC)):
                st.feed.at(i)
                out = st.evaluate(sym)
                if out:
                    box.append((d["ts"][i], sym, 1.0 if out[0] == "long" else -1.0))
    for nm, box in (("B 全开", sigB), ("C 纯tech", sigC)):
        nl = sum(1 for x in box if x[2] > 0)
        print("  %-9s n=%-5d 多 %-5d 空 %-5d  多头占比 %.1f%%"
              % (nm, len(box), nl, len(box) - nl, 100.0 * nl / max(len(box), 1)))

    def edge_by_ts(box, h, sym=None, beta=True):
        per = {}
        for ts, s, sg in box:
            if sym and s != sym:
                continue
            r = fwd(s, ts, h)
            if r is None:
                continue
            if beta:
                m = xs(ts, h)
                if m is None:
                    continue
                r -= m
            per.setdefault(ts, []).append(sg * r)
        return [sum(v) / len(v) for v in per.values()]

    print("\n" + "=" * 66)
    print("★ 门 ①：逐币 费后净超额（β中性 − 0.10%）  判据 ≥2/4 为正")
    print("=" * 66)
    for h in HORIZONS:
        wins = 0; line = []
        for sym in syms:
            r = tstat(edge_by_ts(sigB, h, sym))
            if not r:
                line.append("%s:样本不足" % sym); continue
            n, mu, se, t = r
            net = mu - FEE_RT
            wins += 1 if net > 0 else 0
            line.append("%s %+.4f%%(t%+.2f)%s" % (sym.replace("USDT", ""), net * 100, t,
                                                  "✅" if net > 0 else "❌"))
        print("  持 %2d 根(%4.1fh) → %d/4  %s   %s"
              % (h, h * 0.25, wins, "★门过★" if wins >= 2 else "门不过", "  ".join(line)))

    print("\n  对照：全 4 币合并（组合视角，这是最可信的一格）")
    for h in HORIZONS:
        r = tstat(edge_by_ts(sigB, h))
        if r:
            n, mu, se, t = r
            print("    h=%-3d(%4.1fh) n=%-4d 超额%+.4f%% 净%+.4f%% t=%+.2f %s"
                  % (h, h * 0.25, n, mu * 100, (mu - FEE_RT) * 100, t,
                     "✅" if mu - FEE_RT > 0 else "❌"))
    print("\n⚠️ β 基准只有 4 个币（全是主流，共同因子占主导）⇒ β中性剔除得比 40 币宇宙狠，")
    print("   绝对值偏低 = 保守口径。但【符号】是可靠的：它回答「选这 4 个币有没有 α」。")
    print("⚠️ 固定持有、无止损 ⇒ 不等于实盘三层出场。")


if __name__ == "__main__":
    main()
