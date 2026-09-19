#!/usr/bin/env python3
"""交易所口径盈亏读取器 —— 币安 /fapi/v1/income 的唯一正确用法。

为什么单独成文（2026-09-19）：同一天里这个取数被写错了三次，每次都能算出
「看起来合理」的错数：
  ① 按 tranId 去重 —— 交易所【一条 tranId 同时产生 REALIZED_PNL 行和 COMMISSION 行】，
     去重会把佣金行整个丢掉（实测 4h 窗口 118 条佣金被砍成 48 条，佣金 -1.9218 -> -0.9355）。
  ② 单次请求 —— limit 静默截断到 1000，窗口一大就只拿到最早一段。
  ③ 窗口边界 —— startTime/endTime 是闭区间，链式翻页会在边界重复计数。

正确做法（本文件）：自适应窗口 + 只在【整窗一行不差】时收下，满 1000 行就对半劈。
不去重 tranId；只对【全字段完全相同】的行去重（真的重复响应）。

用法：
    from exchange_pnl import fetch, summarize, trading_net
    rows = fetch(lo_ms, hi_ms)
    agg = summarize(rows)
"""
import hashlib
import hmac
import json
import time
import urllib.error
import urllib.parse
import urllib.request

BASE = "https://fapi.binance.com"
ENV = "/etc/quanty/backend.env"


def load_env(path=ENV):
    out = {}
    for line in open(path, encoding="utf-8"):
        line = line.strip()
        if line and not line.startswith("#") and "=" in line:
            k, v = line.split("=", 1)
            out[k.strip()] = v.strip().strip('"').strip("'")
    return out


def _get(path, params, key, secret, tries=6):
    for i in range(tries):
        p = dict(params)
        p["timestamp"] = int(time.time() * 1000)
        p["recvWindow"] = 5000
        q = urllib.parse.urlencode(p)
        sig = hmac.new(secret.encode(), q.encode(), hashlib.sha256).hexdigest()
        req = urllib.request.Request("%s%s?%s&signature=%s" % (BASE, path, q, sig),
                                     headers={"X-MBX-APIKEY": key, "User-Agent": "Mozilla/5.0"})
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                return json.load(r)
        except urllib.error.HTTPError as e:
            if e.code in (429, 418) and i < tries - 1:
                time.sleep(5 * (i + 1))      # 触发过 WAF：退避要够长，短退避等于继续打
                continue
            raise
    raise RuntimeError("重试超限")


def _fetch_one(lo, hi, key, secret):
    return _get("/fapi/v1/income", {"startTime": lo, "endTime": hi, "limit": 1000}, key, secret)


def fetch(lo_ms, hi_ms, key=None, secret=None, env=None, min_window_ms=1000, verbose=False):
    """返回 [lo_ms, hi_ms] 内的全部 income 行（含 TRANSFER，调用方自己剔）。

    自适应窗口：一次请求拿到 <1000 行 => 该窗完整，收下；恰好 1000 行 => 可能被截断，
    对半劈开递归。这是唯一能同时避免【静默截断】和【边界重复】的做法。
    """
    e = env or load_env()
    key = key or e["BINANCE_API_KEY"]
    secret = secret or e["BINANCE_API_SECRET"]
    out = []
    stack = [(lo_ms, hi_ms)]
    reqs = 0
    while stack:
        lo, hi = stack.pop()
        rows = _fetch_one(lo, hi, key, secret)
        reqs += 1
        time.sleep(0.35)                     # 限速：突发 >160 请求/20s 会触发 WAF 429
        if len(rows) < 1000:
            out.extend(rows)
            continue
        if hi - lo <= min_window_ms:
            out.extend(rows)                 # 1ms 窗口还满 1000 行，无法再分，只能收
            continue
        mid = (lo + hi) // 2
        stack.append((lo, mid))
        stack.append((mid + 1, hi))
    # 只去【整行完全相同】的重复响应；绝不去 tranId
    seen = set()
    uniq = []
    for r in out:
        k = (r.get("tranId"), r.get("time"), r.get("incomeType"), r.get("income"), r.get("symbol"))
        if k in seen:
            continue
        seen.add(k)
        uniq.append(r)
    if verbose:
        print("[exchange_pnl] %d 请求 -> %d 行（去整行重复后 %d）" % (reqs, len(out), len(uniq)))
    return uniq


def summarize(rows, drop_transfer=False):
    agg = {}
    for r in rows:
        agg[r["incomeType"]] = agg.get(r["incomeType"], 0.0) + float(r["income"])
    if drop_transfer:
        agg.pop("TRANSFER", None)
    return agg


def trading_net(agg):
    """交易净额 = 除 TRANSFER（入金/划转）外的全部。"""
    return sum(v for k, v in agg.items() if k != "TRANSFER")


if __name__ == "__main__":
    import datetime
    import sys
    days = int(sys.argv[1]) if len(sys.argv) > 1 else 1
    hi = int(time.time() * 1000)
    lo = hi - days * 86400000
    print("窗口 %s -> %s" % (
        datetime.datetime.utcfromtimestamp(lo / 1000).strftime("%Y-%m-%d %H:%M:%SZ"),
        datetime.datetime.utcfromtimestamp(hi / 1000).strftime("%Y-%m-%d %H:%M:%SZ")))
    rows = fetch(lo, hi, verbose=True)
    agg = summarize(rows)
    for k in sorted(agg):
        print("   %-14s %+12.4f" % (k, agg[k]))
    print("   交易净额 %+.4f" % trading_net(agg))
    c = agg.get("COMMISSION", 0.0)
    g = agg.get("REALIZED_PNL", 0.0)
    if c:
        print("   费/毛 = %.3f" % (abs(c) / abs(g) if g else float("inf")))
