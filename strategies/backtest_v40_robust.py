#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""v40 回测的稳健性修正 —— 回答「刚才那个 t=5.54 是真的还是假的」。

为什么必须做这一步：
  第一版把每个 (币, 时刻) 当成独立观测，得到 n=27960、t=5.54。
  但同一时刻 40 个币的收益高度相关（加密是一个因子）⇒ 有效样本量是
  ~792 个【时间点】，不是 27960。SE 被低估约 sqrt(40)≈6.3 倍。
  不修正就报 t=5.54，等于把 β 当成 α。

三个修正：
  D1 按时间戳聚类：先对每个时间点求跨币均值，再对 792 个时间点做 t 检验。
     —— 这同时是【组合视角】：同一时刻的多个信号本就该合并成一个 P&L。
  D2 横截面超额（β 中性）：r − 同一时刻全体币的均值。只问「它选的币有没有
     跑赢同时刻的其他币」。若超额 ≈0 而绝对收益 >0，那只是踩上了大盘方向。
  D3 多空拆分：若信号在上涨窗口里系统偏多，D1 的正数就是 beta。

用法：python3 backtest_v40_robust.py --cache /tmp/bt_cache.json --v40 /tmp/v40.py
"""
import argparse
import importlib.util
import json
import math
import os
import sys

HORIZONS = [1, 2, 4, 8, 12]


def load_v40(path):
    spec = importlib.util.spec_from_file_location("v40", path)
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


def load_bt(path):
    """复用第一版的取数/Feed 实现，保证口径一致。"""
    spec = importlib.util.spec_from_file_location("bt", path)
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


def build_grid(data):
    """统一时间网格 + 每个符号的 ts->close。"""
    allts = set()
    arr = {}
    for d in data:
        sym = d["symbol"]
        m = {}
        for row, ts in zip(d["rows"], d["ts"]):
            m[ts] = row["close"]
        arr[sym] = m
        allts.update(m)
    ts_list = sorted(allts)
    pos = {t: i for i, t in enumerate(ts_list)}
    return ts_list, pos, arr


def fwd(arr, sym, ts, ts_list, pos, h):
    k = pos.get(ts)
    if k is None or k + h >= len(ts_list):
        return None
    c0 = arr.get(sym, {}).get(ts)
    c1 = arr.get(sym, {}).get(ts_list[k + h])
    if not c0 or not c1:
        return None
    return c1 / c0 - 1.0


_XS = {}


def xs_mean(arr, syms, ts, ts_list, pos, h, tag="ALL"):
    """同一时刻全体币的等权远期收益（当作 β 基准）。tag 区分不同的宇宙。"""
    key = (tag, ts, h)
    if key in _XS:
        return _XS[key]
    vals = []
    for s in syms:
        r = fwd(arr, s, ts, ts_list, pos, h)
        if r is not None:
            vals.append(r)
    v = sum(vals) / len(vals) if vals else None
    _XS[key] = v
    return v


def per_ts(box, arr, ts_list, pos, h, syms, beta, tag):
    """先对同一时刻的信号取均值（组合视角），返回 {ts: mean_edge}。"""
    by = {}
    for ts, sym, sg in box:
        r = fwd(arr, sym, ts, ts_list, pos, h)
        if r is None:
            continue
        if beta:
            m = xs_mean(arr, syms, ts, ts_list, pos, h, tag)
            if m is None:
                continue
            r -= m
        by.setdefault(ts, []).append(sg * r)
    return {t: sum(v) / len(v) for t, v in by.items()}


def _ttest(per, label, beta):
    n = len(per)
    if n < 20:
        return None
    mean = sum(per) / n
    var = sum((x - mean) ** 2 for x in per) / (n - 1)
    se = math.sqrt(var / n)
    return {"label": label, "n_ts": n, "mean": mean, "se": se,
            "t": mean / se if se else 0.0, "beta_neutral": beta}


def clustered(pairs, arr, ts_list, pos, h, syms, beta_neutral, label, tag="ALL"):
    """pairs = [(ts, sym, sign)]。先按时刻聚合，再对时刻做 t 检验。"""
    d = per_ts(pairs, arr, ts_list, pos, h, syms, beta_neutral, tag)
    s = _ttest(list(d.values()), label, beta_neutral)
    if s:
        s["h"] = h
        s["n_obs"] = len(pairs)
    return s


def paired(boxA, boxB, arr, ts_list, pos, h, syms, tag, beta=True):
    """B−C 的【配对】检验：同一时刻两个账本各自的均值之差，再对时刻做 t。
    这才是「加密乘数有没有加东西」的答案 —— 单独看 B 的 t 值回答不了，
    因为 B 和 C 高度相关，差值必须用配对口径。"""
    a = per_ts(boxA, arr, ts_list, pos, h, syms, beta, tag)
    b = per_ts(boxB, arr, ts_list, pos, h, syms, beta, tag)
    common = sorted(set(a) & set(b))
    diffs = [a[t] - b[t] for t in common]
    return _ttest(diffs, "B-C", beta)


def classify(syms):
    """symbol -> underlyingType。币安 exchangeInfo 自带，不用猜。
    COIN / COMMODITY / EQUITY / CN_EQUITY / HK_EQUITY / KR_EQUITY / FX /
    PREMARKET / INDEX —— contractType=TRADIFI_PERPETUAL 的那批是股票/商品/外汇。"""
    import urllib.request
    req = urllib.request.Request(
        "https://fapi.binance.com/fapi/v1/exchangeInfo",
        headers={"User-Agent": "Mozilla/5.0"})
    d = json.load(urllib.request.urlopen(req, timeout=15))
    want = set(syms)
    out = {}
    for s in d["symbols"]:
        if s["symbol"] in want:
            out[s["symbol"]] = s.get("underlyingType") or s.get("contractType") or "?"
    return out


def show(s):
    if not s:
        print("  (样本不足)")
        return
    tag = "超额(β中性)" if s["beta_neutral"] else "绝对  "
    print("  h=%-3d %s 时刻数=%-5d 观测=%-6d 均%+.4f%%  t=%+.2f"
          % (s["h"], tag, s["n_ts"], s["n_obs"], s["mean"] * 100, s["t"]))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--cache", default="/tmp/bt_cache.json")
    ap.add_argument("--v40", default="/tmp/v40.py")
    ap.add_argument("--bt", default="/tmp/bt_v40.py")
    ap.add_argument("--lag", type=int, default=1)
    args = ap.parse_args()

    v40 = load_v40(args.v40)
    bt = load_bt(args.bt)
    with open(args.cache, encoding="utf-8") as fh:
        data = json.load(fh)
    print("数据: %d 个符号" % len(data))

    ts_list, pos, arr = build_grid(data)
    syms = [d["symbol"] for d in data]
    print("统一时间网格: %d 个小时桶 (%s -> %s)"
          % (len(ts_list), ts_list[0], ts_list[-1]))
    print("平均每桶有价的符号数: %.1f" % (
        sum(1 for t in ts_list if sum(1 for s in syms if t in arr[s])) / len(ts_list)))

    # ---- 生成信号（与第一版 Test A/B/C 完全同口径）----
    sigA, sigB, sigC = [], [], []
    for d in data:
        sym, rows = d["symbol"], d["rows"]
        # A: 只用乘数
        feedA = bt.HistFeed(rows, args.lag)
        # C: 加密全关
        stC = v40.Strategy.__new__(v40.Strategy)
        stC.closes = {sym: []}; stC.highs = {sym: []}
        stC.lows = {sym: []}; stC.volumes = {sym: []}
        stC.feed = bt.HistFeed(rows, args.lag, only="none")
        # B: 全开
        stB = v40.Strategy.__new__(v40.Strategy)
        stB.closes = {sym: []}; stB.highs = {sym: []}
        stB.lows = {sym: []}; stB.volumes = {sym: []}
        stB.feed = bt.HistFeed(rows, args.lag)
        for i in range(len(rows)):
            ts = d["ts"][i]
            for st in (stB, stC):
                st.closes[sym].append(rows[i]["close"])
                st.highs[sym].append(rows[i]["high"])
                st.lows[sym].append(rows[i]["low"])
                st.volumes[sym].append(rows[i]["volume"])
                for a2 in (st.closes, st.highs, st.lows, st.volumes):
                    if len(a2[sym]) > v40.Config.MAX_BARS:
                        del a2[sym][:-v40.Config.MAX_BARS]
            if i < 80:
                continue
            feedA.at(i)
            ml = v40.crypto_adjust(sym, "long", feedA)[0] * v40.oi_adjust(sym, "long", feedA, 0.0)[0]
            ms = v40.crypto_adjust(sym, "short", feedA)[0] * v40.oi_adjust(sym, "short", feedA, 0.0)[0]
            if ml > 0 and ms > 0:
                s = math.log(ml) - math.log(ms)
                if abs(s) > 1e-9:
                    sigA.append((ts, sym, 1.0 if s > 0 else -1.0))
            for st, box in ((stB, sigB), (stC, sigC)):
                st.feed.at(i)
                out = st.evaluate(sym)
                if out:
                    box.append((ts, sym, 1.0 if out[0] == "long" else -1.0))
        print("  信号: %-12s A=%-6d B=%-6d C=%-6d" % (
            sym, len(sigA), len(sigB), len(sigC)))

    print("\n信号方向构成（多/空）:")
    for nm, box in (("A(乘数)", sigA), ("B(全开)", sigB), ("C(纯tech)", sigC)):
        nl = sum(1 for x in box if x[2] > 0)
        print("  %-10s 多 %-7d 空 %-7d  多头占比 %.1f%%" % (nm, nl, len(box) - nl, 100.0 * nl / max(len(box), 1)))

    # 同期宇宙平均收益（判断窗口是不是单边行情）
    print("\n窗口行情（等权宇宙）:")
    for h in HORIZONS:
        vs = [xs_mean(arr, syms, t, ts_list, pos, h) for t in ts_list]
        vs = [v for v in vs if v is not None]
        if vs:
            print("  h=%-3d 全期平均 %+.4f%%  (中位 %+.4f%%)"
                  % (h, 100 * sum(vs) / len(vs), 100 * sorted(vs)[len(vs) // 2]))

    for nm, box in (("Test A 隔离乘数", sigA), ("Test B 全引擎(加密开)", sigB),
                    ("Test C 纯 tech(加密关)", sigC)):
        print("\n=== %s ===" % nm)
        for h in HORIZONS:
            show(clustered(box, arr, ts_list, pos, h, syms, False, nm))
        print("  -- β 中性（剔除同时刻大盘）--")
        for h in HORIZONS:
            show(clustered(box, arr, ts_list, pos, h, syms, True, nm))

    print("\n=== 决定性对照 B − C ===")
    print("   绝对口径（含大盘）/ β中性（纯 α）/ ★配对差值 —— 配对才是答案")
    for h in HORIZONS:
        b = clustered(sigB, arr, ts_list, pos, h, syms, False, "B")
        bb = clustered(sigB, arr, ts_list, pos, h, syms, True, "B")
        c = clustered(sigC, arr, ts_list, pos, h, syms, False, "C")
        cc = clustered(sigC, arr, ts_list, pos, h, syms, True, "C")
        p = paired(sigB, sigC, arr, ts_list, pos, h, syms, "ALL", True)
        if not (b and bb and c and cc and p):
            continue
        print("  h=%-2d B %+.4f%%(t%+.2f) | β中性 B %+.4f%%(t%+.2f) C %+.4f%%(t%+.2f)"
              " | ★配对差 %+.4f%% t=%+.2f"
              % (h, b["mean"] * 100, b["t"], bb["mean"] * 100, bb["t"],
                 cc["mean"] * 100, cc["t"], p["mean"] * 100, p["t"]))

    print("\n=== 扣费后的净超额（taker 0.05%%/边 ⇒ 来回 0.10%%）===")
    print("   β中性超额 − 0.10%%；只有为正才值得开真钱")
    for h in HORIZONS:
        for nm, box in (("B 全开", sigB), ("C 纯tech", sigC)):
            s = clustered(box, arr, ts_list, pos, h, syms, True, nm)
            if s:
                net = s["mean"] - 0.0010
                flag = "✅" if net > 0 else "❌"
                print("  h=%-2d %-8s 超额%+.4f%%  净%+.4f%%  %s"
                      % (h, nm, s["mean"] * 100, net * 100, flag))

    print("\n⚠️ 判据：β 中性列才是 α。|t|<2 一律不报为发现。")
    print("⚠️ 30 天 = 单一市场状态，不可外推。")

    # ---- 按资产类别拆（owner 2026-09-19：不同类不能用同一套策略）----
    print("\n" + "=" * 62)
    print("按 underlyingType 分组 —— 「这个池子值不值得单独建一个策略」")
    print("=" * 62)
    try:
        cls = classify(syms)
    except Exception as e:
        print("分类取数失败: %r" % e)
        return
    groups = {}
    for s in syms:
        groups.setdefault(cls.get(s, "?"), []).append(s)
    for k in sorted(groups):
        print("  %-14s %d 个: %s" % (k, len(groups[k]),
                                     ",".join(x.replace("USDT", "") for x in groups[k])))
    print()
    for k in sorted(groups):
        members = set(groups[k])
        if len(members) < 3:
            print("=== %s (%d 个) —— 样本太小，跳过 ===" % (k, len(members)))
            continue
        msyms = groups[k]
        print("=== %s (%d 个) ===" % (k, len(msyms)))
        for h in (1, 4, 8):
            for nm, box in (("B 全开", sigB), ("C 纯tech", sigC)):
                sub = [p for p in box if p[1] in members]
                s2 = clustered(sub, arr, ts_list, pos, h, msyms, True, nm, tag=k)
                if s2:
                    net = s2["mean"] - 0.0010
                    print("  h=%-2d %-8s 时刻=%-4d 观测=%-5d 超额%+.4f%% t=%+.2f  净%+.4f%% %s"
                          % (h, nm, s2["n_ts"], s2["n_obs"], s2["mean"] * 100, s2["t"],
                             net * 100, "✅" if net > 0 else "❌"))
        print()

    # 每类的成本/波动比 —— 手续费固定 0.10% 来回，波动不固定
    print("=== 成本/波动比（1h 平均绝对波幅 vs 0.10%% 来回费）===")
    for k in sorted(groups):
        msyms = groups[k]
        mv = []
        for t in ts_list:
            for s in msyms:
                r = fwd(arr, s, t, ts_list, pos, 1)
                if r is not None:
                    mv.append(abs(r))
        if mv:
            m = sum(mv) / len(mv)
            print("  %-14s 1h 平均|波幅| %.4f%%   费占波幅 %.1f%%"
                  % (k, m * 100, 0.10 / (m * 100) * 100))


if __name__ == "__main__":
    main()
