#!/usr/bin/env python3
"""
P0 优化回测：用过去 60 天 Binance userTrades 真实数据，
模拟"如果当时就应用了 P0 过滤"会发生什么。

输入：data/binance_analysis/userTrades_60d.json
输出：terminal 表格 + data/binance_analysis/backtest_p0_result.json

注意：这不是策略回测（无法重新生成信号），是"应用过滤后剩下哪些
trade 还会成交"的反事实分析。给出的是上限估计——假设过滤掉的 trade
不会以任何形式发生，留下的 trade 完全保持原样。

按 BTC 持续涨/跌的 60 天判断这个估算的可信度：60 天里 BILL+UB 的 short
吃掉了 +28 净盈利。如果这个 60 天换成 BTC 大涨的环境，short-only 反过来
可能亏。所以这个回测结果只对"60 天内的实际市场"成立。
"""

import json
import os
from collections import defaultdict
from datetime import datetime, timezone

BASE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DATA = os.path.join(BASE, "data", "binance_analysis", "userTrades_60d.json")

# P0 优化规则
WHITELIST = {"BILLUSDT", "UBUSDT", "PRLUSDT", "TRUTHUSDT"}
BLACKLIST = {"SAHARAUSDT", "WIFUSDT", "AIOUSDT", "DODOXUSDT", "1000PEPEUSDT", "PLAYUSDT", "MAGMAUSDT"}
ALLOWED_SIDES = {"sell"}   # 只做空（USDM 里 SELL 表示开 short / 关 long）
BAN_HOURS_UTC = set(range(18, 24))  # UTC 18:00-23:59 禁止开仓
MIN_HOLD_MIN = 5   # 假设我们改了 hunger 之后，开仓后 5 分钟内不会被砍


def load_trades():
    with open(DATA) as f:
        return json.load(f)


def group_by_order(trades):
    per_order = defaultdict(lambda: {"qty":0.0,"rp":0.0,"comm":0.0,"side":"","sym":"",
                                      "ts_first":1e18,"ts_last":0,
                                      "vwap_num":0.0,"vwap_den":0.0,"n":0})
    for t in trades:
        k = (t["symbol"], t["orderId"])
        o = per_order[k]
        o["qty"] += float(t["qty"])
        o["rp"] += float(t["realizedPnl"])
        if t["commissionAsset"] == "USDT":
            o["comm"] += float(t["commission"])
        o["side"] = t["side"]; o["sym"] = t["symbol"]
        o["ts_first"] = min(o["ts_first"], t["time"])
        o["ts_last"] = max(o["ts_last"], t["time"])
        o["vwap_num"] += float(t["price"]) * float(t["qty"])
        o["vwap_den"] += float(t["qty"])
        o["n"] += 1
    return per_order


def pair_open_close(per_order):
    """返回每个 close 事件配上它对应的 open，得到 trade 元组列表"""
    sym_opens = defaultdict(list)
    events = []
    for o in sorted(per_order.values(), key=lambda x: x["ts_first"]):
        if o["rp"] == 0:
            sym_opens[o["sym"]].append(o)
        else:
            opens = sym_opens[o["sym"]]
            matched = None
            for op in reversed(opens):
                if op["ts_last"] <= o["ts_first"]:
                    matched = op
                    break
            if not matched:
                continue
            entry_vwap = matched["vwap_num"] / matched["vwap_den"] if matched["vwap_den"] else 0
            exit_vwap = o["vwap_num"] / o["vwap_den"] if o["vwap_den"] else 0
            direction = "long" if matched["side"] == "BUY" else "short"
            entry_side_filter = "buy" if direction == "long" else "sell"  # 对应 allowed_sides 配置语义
            hold_min = (o["ts_first"] - matched["ts_first"]) / 60000
            events.append({
                "sym": o["sym"],
                "direction": direction,
                "entry_side": entry_side_filter,
                "entry_ts": matched["ts_first"],
                "exit_ts": o["ts_first"],
                "entry_vwap": entry_vwap,
                "exit_vwap": exit_vwap,
                "qty": matched["qty"],
                "hold_min": hold_min,
                "realized_pnl": o["rp"],
                "open_comm": matched["comm"],
                "close_comm": o["comm"],
                "notional": matched["qty"] * entry_vwap,
            })
    return events


def evaluate(events, label, predicate):
    """对 events 应用过滤器 predicate(e) -> bool（True=保留），输出汇总"""
    kept = [e for e in events if predicate(e)]
    gross = sum(e["realized_pnl"] for e in kept)
    comm = sum(e["open_comm"] + e["close_comm"] for e in kept)
    net = gross - comm
    wins = sum(1 for e in kept if e["realized_pnl"] > 0)
    n = len(kept)
    wr = wins / n * 100 if n else 0
    print(f"  {label:<50} n={n:>4} win%={wr:>5.1f}  gross={gross:>+9.4f}  comm={comm:>7.4f}  NET={net:>+9.4f}")
    return {"label": label, "n": n, "wins": wins, "gross": gross, "comm": comm, "net": net}


def main():
    trades = load_trades()
    per_order = group_by_order(trades)
    events = pair_open_close(per_order)

    print(f"全部配对成功的 close 事件: {len(events)}\n")
    print("=== 各种过滤场景的反事实回测 ===\n")

    # baseline
    base = evaluate(events, "BASELINE (无过滤,等价于历史实际)",
                    lambda e: True)
    print()

    # 单维度过滤
    sym_only = evaluate(events, "仅 symbols=白名单(BILL/UB/PRL/TRUTH)",
                        lambda e: e["sym"] in WHITELIST)
    blk_only = evaluate(events, "仅 symbol_blacklist (7个黑名单)",
                        lambda e: e["sym"] not in BLACKLIST)
    side_only = evaluate(events, "仅 allowed_sides=[sell] (关 long)",
                         lambda e: e["entry_side"] in ALLOWED_SIDES)
    time_only = evaluate(events, "仅 entry_time_windows 00:00-18:00 UTC",
                         lambda e: datetime.fromtimestamp(e["entry_ts"]/1000, tz=timezone.utc).hour not in BAN_HOURS_UTC)
    hold_only = evaluate(events, "仅 hunger>=5min (假设没在 0-5min 被砍)",
                         lambda e: e["hold_min"] >= MIN_HOLD_MIN)
    print()

    # 组合：白名单 + 黑名单 + 短 + 时段
    print("=== 组合过滤（P0 优化的完整效果） ===\n")
    combo1 = evaluate(events, "白名单 + 黑名单",
                      lambda e: e["sym"] in WHITELIST and e["sym"] not in BLACKLIST)
    combo2 = evaluate(events, "白名单 + 黑名单 + short-only",
                      lambda e: e["sym"] in WHITELIST and e["sym"] not in BLACKLIST and e["entry_side"] in ALLOWED_SIDES)
    combo3 = evaluate(events, "白名单 + 黑名单 + short-only + 时段过滤",
                      lambda e: e["sym"] in WHITELIST and e["sym"] not in BLACKLIST and e["entry_side"] in ALLOWED_SIDES and datetime.fromtimestamp(e["entry_ts"]/1000, tz=timezone.utc).hour not in BAN_HOURS_UTC)
    combo4 = evaluate(events, "P0 全集 (上面 + 持仓>=5min)",
                      lambda e: e["sym"] in WHITELIST and e["sym"] not in BLACKLIST and e["entry_side"] in ALLOWED_SIDES and datetime.fromtimestamp(e["entry_ts"]/1000, tz=timezone.utc).hour not in BAN_HOURS_UTC and e["hold_min"] >= MIN_HOLD_MIN)
    print()

    # 极端对比
    print("=== 极端情景做对照 ===\n")
    long_only = evaluate(events, "long-only", lambda e: e["entry_side"] == "buy")
    short_only = evaluate(events, "short-only", lambda e: e["entry_side"] == "sell")
    bill_only = evaluate(events, "BILLUSDT only", lambda e: e["sym"] == "BILLUSDT")
    ub_only = evaluate(events, "UBUSDT only", lambda e: e["sym"] == "UBUSDT")
    bill_ub_short = evaluate(events, "BILL+UB short-only", lambda e: e["sym"] in {"BILLUSDT","UBUSDT"} and e["entry_side"]=="sell")

    print("\n=== 结论 ===\n")
    print(f"  历史实际    : NET {base['net']:+.4f} USDT  ({base['n']} 笔)")
    print(f"  P0 全集     : NET {combo4['net']:+.4f} USDT  ({combo4['n']} 笔)")
    delta = combo4['net'] - base['net']
    print(f"  → P0 优化预计能改善 {delta:+.4f} USDT")
    if combo4['net'] > 0:
        print(f"  → P0 优化下该 60 天本来能从亏 {base['net']:+.2f} 变成赚 {combo4['net']:+.2f}")
    else:
        print(f"  → 即使应用 P0 全集仍是亏的，说明策略本身需要更深度改造")

    out = {
        "baseline": base, "sym_only": sym_only, "blk_only": blk_only,
        "side_only": side_only, "time_only": time_only, "hold_only": hold_only,
        "combo_full": combo4, "long_only": long_only, "short_only": short_only,
        "bill_only": bill_only, "ub_only": ub_only, "bill_ub_short": bill_ub_short,
    }
    out_path = os.path.join(BASE, "data", "binance_analysis", "backtest_p0_result.json")
    with open(out_path, "w") as f:
        json.dump(out, f, indent=2)
    print(f"\n详细结果已保存: {out_path}")


if __name__ == "__main__":
    main()
