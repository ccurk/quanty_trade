#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
策略实例隔离自检（只读，无副作用）。任何实例 start 之前跑一次。

为什么需要它：币安 USDM 是【每账户每 symbol 一个净持仓】，
而引擎取持仓用的是 `USDMPositionInfo(ownerID, symbol)`
（backend/internal/exchange/binance.go:1868，读 /fapi/v2/positionRisk）
—— **键里没有 strategy_id**。所以两个实例一旦碰同一个 symbol：
  · 交易所把两边持仓【合并】成一个净仓；
  · 两个引擎读到的都是这个净仓，各自按自己的规则去管/去平；
  · 池子本身是【自动变动】的（SYMBOL_ROTATE / 优化器会改 symbols），
    所以"今天不重叠"不等于"明天不重叠"。
⇒ 隔离必须【每次启动前查】，不能靠一次性的观察。

判定：任一两实例 symbols 交集非空 ⇒ 🔴 不允许同时 running。

凭据只从 /etc/quanty/backend.env 读进进程内存，绝不打印、绝不进 argv。
"""
import json
import sys

import pymysql

ENV = "/etc/quanty/backend.env"


def load_env():
    env = {}
    with open(ENV) as fh:
        for line in fh:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            env[k.strip()] = v.strip().strip('"').strip("'")
    return env


def symbols_of(cfg):
    s = cfg.get("symbols")
    if s is None:
        s = cfg.get("symbol")
    if s is None:
        return []
    if isinstance(s, str):
        s = s.split(",")
    return sorted({x.strip() for x in s if str(x).strip()})


def main():
    env = load_env()
    cn = pymysql.connect(host="127.0.0.1", user=env["DB_USER"], password=env["DB_PASS"],
                         database="quanty_trade", charset="utf8mb4",
                         cursorclass=pymysql.cursors.DictCursor)
    c = cn.cursor()

    c.execute("SELECT id, name, status, config FROM strategy_instances ORDER BY id")
    insts = []
    for r in c.fetchall():
        cfg = json.loads(r["config"])
        insts.append({"id": r["id"], "name": r["name"], "status": r["status"],
                      "syms": symbols_of(cfg), "cfg": cfg})

    print("=" * 70)
    print("1) 各实例【实际 symbols 字段】（不是实例名 —— 名字会骗人）")
    print("=" * 70)
    for i in insts:
        print("  %s %-30s %-8s (%d) %s"
              % (i["id"][:8], i["name"], i["status"], len(i["syms"]), ",".join(i["syms"])))

    print()
    print("=" * 70)
    print("2) ★ 两两交集 —— 非空即冲突（USDM 净持仓会合并）")
    print("=" * 70)
    bad = []
    for a in range(len(insts)):
        for b in range(a + 1, len(insts)):
            A, B = insts[a], insts[b]
            inter = sorted(set(A["syms"]) & set(B["syms"]))
            both_run = (A["status"] == "running" and B["status"] == "running")
            if not inter:
                print("  ✅ %s ∩ %s = 空" % (A["id"][:8], B["id"][:8]))
                continue
            tag = "🔴 冲突(两个都在跑)" if both_run else "🟡 重叠(未同时跑)"
            print("  %s %s ∩ %s = %d 个: %s"
                  % (tag, A["id"][:8], B["id"][:8], len(inter), ",".join(inter)))
            if both_run:
                bad.append((A["id"], B["id"], inter))

    print()
    print("=" * 70)
    print("3) 近 72h 实盘：有没有 symbol 被 >1 个实例碰过")
    print("=" * 70)
    c.execute("""SELECT symbol, COUNT(DISTINCT strategy_id) ns,
                        GROUP_CONCAT(DISTINCT LEFT(strategy_id,8)) sids
                 FROM strategy_positions
                 WHERE open_time >= NOW() - INTERVAL 72 HOUR
                 GROUP BY symbol HAVING ns > 1""")
    rows = c.fetchall()
    if not rows:
        print("  ✅ 0 个 symbol 被跨实例碰过")
    for r in rows:
        print("  🔴 %-14s 被 %d 个实例碰过: %s" % (r["symbol"], r["ns"], r["sids"]))

    print()
    print("=" * 70)
    print("4) 裸奔兜底检查：每个 running 实例是否至少有一层出场保护")
    print("=" * 70)
    for i in insts:
        if i["status"] != "running":
            continue
        cfg = i["cfg"]
        mh = cfg.get("max_hold_minutes") or 0
        hunger = bool(cfg.get("hunger_mode_enabled"))
        exleg = cfg.get("use_exchange_tpsl", True)
        # 交易所腿是否真的挂上，看日志实据，不看配置推断
        c.execute("""SELECT COUNT(*) n FROM strategy_logs
                     WHERE strategy_id LIKE %s AND message LIKE '已设置止盈止损%%'
                       AND created_at >= NOW() - INTERVAL 72 HOUR""", (i["id"][:8] + "%",))
        n_leg = c.fetchone()["n"]
        risks = []
        if mh <= 0:
            risks.append("max_hold<=0")
        if not hunger and not exleg:
            risks.append("hunger关+交易所腿关=真裸奔")
        if n_leg == 0:
            risks.append("近72h无挂腿记录(可能只是没开仓)")
        print("  %s %-30s max_hold=%-5s hunger=%-5s 交易所腿=%-5s 近72h挂腿=%-4d %s"
              % (i["id"][:8], i["name"], mh, hunger, exleg, n_leg,
                 "⚠️ " + "; ".join(risks) if risks else "✅"))
        if not hunger and not exleg:
            print("      ↑ 【代码事实】hasEffectiveTPSL(strategy_position.go:90) 只看信号 tp/sl 是否>0，")
            print("        与 use_exchange_tpsl 无关 ⇒ 这两项同为关时，会【开仓但没有任何挂单止损】，")
            print("        只剩 max_hold 时长兜底。这是一个没有护栏的组合。")

    print()
    print("=" * 70)
    print("5) 当前 open 持仓行：symbol × 实例")
    print("=" * 70)
    c.execute("""SELECT symbol, LEFT(strategy_id,8) sid, direction, amount, avg_price,
                        take_profit, stop_loss, open_time
                 FROM strategy_positions WHERE status='open' ORDER BY symbol, sid""")
    rows = c.fetchall()
    if not rows:
        print("  （无）")
    for r in rows:
        e = float(r["avg_price"] or 0)
        tp = float(r["take_profit"] or 0)
        sl = float(r["stop_loss"] or 0)
        k = "刀=%.4f%%" % (abs(sl - e) / e * 100) if e > 0 and sl > 0 else "刀=? (无止损价)"
        print("  %-12s %s %-5s amt=%-10s entry=%-12s %s open=%s"
              % (r["symbol"], r["sid"], r["direction"], r["amount"], r["avg_price"], k, r["open_time"]))

    cn.close()
    print()
    if bad:
        print("🔴 结论：存在【两个 running 实例共用 symbol】—— 先停一个再继续。")
        return 1
    print("✅ 结论：当前无两个 running 实例共用 symbol。")
    print("   但这是【查出来的】不是【保证的】：池子会被 SYMBOL_ROTATE / 优化器自动改，")
    print("   所以每次 start 之前都要重跑本脚本。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
