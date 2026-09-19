import json, hmac, hashlib, time, urllib.request, urllib.parse, pymysql
from datetime import datetime, timezone, timedelta
from collections import defaultdict

env = {}
for line in open("/etc/quanty/backend.env"):
    line = line.strip()
    if line and not line.startswith("#") and "=" in line:
        k, v = line.split("=", 1)
        env[k.strip()] = v.strip().strip('"').strip("'")

NOW = datetime.now(timezone.utc)
print("SERVER UTC NOW =", NOW.strftime("%Y-%m-%d %H:%M:%S"))

# ---- Binance 私有接口 ----
def bkeys():
    ak = env.get("BINANCE_API_KEY") or env.get("BINANCE_KEY")
    sk = env.get("BINANCE_API_SECRET") or env.get("BINANCE_SECRET")
    return ak, sk

AK, SK = bkeys()
BASE = "https://fapi.binance.com"


def signed(path, params):
    p = dict(params); p["timestamp"] = int(time.time() * 1000); p["recvWindow"] = 5000
    q = urllib.parse.urlencode(p)
    sig = hmac.new(SK.encode(), q.encode(), hashlib.sha256).hexdigest()
    r = urllib.request.Request(BASE + path + "?" + q + "&signature=" + sig,
                               headers={"X-MBX-APIKEY": AK, "User-Agent": "Mozilla/5.0"})
    with urllib.request.urlopen(r, timeout=30) as resp:
        return json.loads(resp.read())


DAY0 = datetime(2026, 9, 19, 0, 0, tzinfo=timezone.utc)
DAY1 = DAY0 + timedelta(days=1)


def income_window(a, b):
    # ★★ 2026-09-19 取数 bug（我自己踩的第三个静默坑，代价是报错数）。
    #
    # 旧写法一把梭：startTime=窗口起、endTime=窗口末、limit=1000，游标 `max(time)+1`。
    # Binance 在 limit 处截断 ⇒ 与页尾同一时刻的行被劈到下一页，而 +1 正好把它们跳过。
    # 实测：全天缺 **18 行，全部落在 05:40 同一批次**，少算 −1.2809U
    # （REALIZED −1.1563 + COMMISSION −0.1246）⇒ 报 −49.1529，真值 **−50.4337**。
    # 最阴的是：**逐小时求和自检照样通过**（缺的行不在自检覆盖里）⇒ 自检抓不到这个。
    #
    # 我也试过「游标不加 1 + tranId 去重」，结果**更差**（1189 行 / −39.74）——
    # 说明我根本没实测过 Binance 这个端点的返回顺序，靠猜改游标是错的。
    #
    # 唯一实证过的正确做法：**逐小时取**。每段行数远小于 1000（实测最忙的 05Z 也只有
    # 269 行，是 limit 的 27%），结构上不可能截断，且与「单小时窗口单独取」逐一对得上
    # （05Z 独立取 = −20.0359，逐小时法 05Z 也是 −20.0359；06Z 两边都是 −15.6643）。
    out = []
    t = a.replace(minute=0, second=0, microsecond=0)
    while t < b:
        s = int(t.timestamp() * 1000)
        e = min(int((t + timedelta(hours=1)).timestamp() * 1000) - 1, int(b.timestamp() * 1000))
        rows = signed("/fapi/v1/income", {"startTime": s, "endTime": e, "limit": 1000})
        if len(rows) >= 1000:
            print("  ★★ 警告：%s 这一小时取满 1000 行上限，可能被截断 —— 必须改成该小时内再分段"
                  % t.strftime("%H:%M"))
        out += rows
        t += timedelta(hours=1)
    return out


rows = income_window(DAY0, min(DAY1, NOW))
agg = defaultdict(float)
hourly = defaultdict(float)
for r in rows:
    t = r["incomeType"]
    v = float(r["income"])
    agg[t] += v
    if t != "TRANSFER":
        hourly[datetime.fromtimestamp(r["time"] / 1000, timezone.utc).hour] += v

rp, cm, ff, tr = agg["REALIZED_PNL"], agg["COMMISSION"], agg["FUNDING_FEE"], agg["TRANSFER"]
net = rp + cm + ff
print("\n=== 币安 income（UTC 09-19 00:00 → now，rows=%d）===" % len(rows))
print("  REALIZED_PNL %+10.4f   COMMISSION %+9.4f   FUNDING_FEE %+8.4f" % (rp, cm, ff))
print("  净(不含TRANSFER) %+9.4f   TRANSFER(单列,非交易) %+.4f" % (net, tr))
print("  毛/费比 = %.4f" % (abs(rp) / abs(cm) if cm else 0))
s = sum(hourly.values())
print("  逐小时求和自检: %.6f vs %.6f  diff=%.6f %s" %
      (s, net, s - net, "OK" if abs(s - net) < 1e-6 else "★不符"))
print("  超出全天量的子窗口: %s" % ([h for h, v in hourly.items() if abs(v) > abs(net) + 1e-9] or "无"))
print("  逐小时: " + " ".join("%02dZ%+.4f" % (h, hourly[h]) for h in sorted(hourly)))

# ---- 账户与敞口 ----
acct = signed("/fapi/v2/account", {})
eq = float(acct["totalWalletBalance"]) + float(acct["totalUnrealizedProfit"])
avail = float(acct["availableBalance"])
upnl = float(acct["totalUnrealizedProfit"])
mm = float(acct["totalMaintMargin"])
pos = signed("/fapi/v2/positionRisk", {})
live = [p for p in pos if abs(float(p["positionAmt"])) > 0]
notional = sum(abs(float(p["positionAmt"]) * float(p["markPrice"])) for p in live)
# ★ 2026-09-19 修：cross 持仓的 positionInitialMargin / isolatedMargin 都返回 0，
# 按它们求和会得到「保证金占用 0.00%」——一个看着正常、实则恒零的读数。
# 权威字段是 account.totalInitialMargin（实测 42.2055 ⇒ 26.85%）。
im = float(acct.get("totalInitialMargin", 0) or 0)
print("\n=== 敞口 ===")
print("  权益 %.4f  可用 %.4f  upnl %+.4f  维持保证金 %.4f (%.2f%%)" %
      (eq, avail, upnl, mm, 100 * mm / eq if eq else 0))
print("  持仓 %d  名义 %.2f (%.1f%%权益)  保证金占用 %.4f (%.2f%%)" %
      (len(live), notional, 100 * notional / eq if eq else 0, im, 100 * im / eq if eq else 0))
for p in live:
    print("     %-12s amt=%s entry=%s mark=%s uPnL=%s" %
          (p["symbol"], p["positionAmt"], p["entryPrice"], p["markPrice"], p["unRealizedProfit"]))
# ★ 2026-09-19 修：openAlgoOrders 返回的是**扁平 list**（每个元素就是一条腿，
# 键是 algoType/orderType/triggerPrice/quantity），不是嵌套的 {"algoOrders": [...]}。
# 我原来按嵌套解析 ⇒ 恒为 0 张，配上「持仓 3」看起来就是"三个裸仓"的假警报。
# 每仓应有 2 条腿（TAKE_PROFIT + STOP）⇒ 腿数 == 2×持仓数 才是零裸仓。
try:
    algos = signed("/fapi/v1/openAlgoOrders", {})
    legs = algos if isinstance(algos, list) else []
    per = defaultdict(lambda: defaultdict(int))
    for a in legs:
        per[a.get("symbol")][a.get("orderType")] += 1
    print("  algo 腿 %d 张（持仓 %d ⇒ 应为 %d 张才是零裸仓）" % (len(legs), len(live), 2 * len(live)))
    for sym in sorted(p["symbol"] for p in live):   # live 是 dict 列表，不能直接 sorted(live)
        d = per.get(sym, {})
        ok = "OK" if (d.get("STOP", 0) >= 1 and d.get("TAKE_PROFIT", 0) >= 1) else "★缺腿"
        print("     %-12s TP=%d SL=%d  %s" % (sym, d.get("TAKE_PROFIT", 0), d.get("STOP", 0), ok))
    # live 是 dict 列表，必须先把 symbol 抽成集合再比对。
    # 写成 `s not in live` 时 s 是字符串、live 里全是 dict ⇒ 恒为真，
    # 会把每个有腿的币都报成孤儿腿（我上一版就是这么错的）。
    live_syms = {p["symbol"] for p in live}
    orphan = [s for s in per if s not in live_syms]
    if orphan:
        print("     ★ 孤儿腿（有腿无仓）: %s" % orphan)
    oo = signed("/fapi/v1/openOrders", {})
    print("  普通挂单 %d 张  orderType=%s" %
          (len(oo), sorted({o["type"] for o in oo}) or "无"))
except Exception as e:
    print("  algo/挂单查询失败: %s" % e)

# ---- DB 侧 ----
cn = pymysql.connect(host="127.0.0.1", user=env["DB_USER"], password=env["DB_PASS"],
                     database="quanty_trade", charset="utf8mb4",
                     cursorclass=pymysql.cursors.DictCursor)
c = cn.cursor()
INST = {"8eb182b6-ee74-4125-a602-f0a91f376432": "Meme",
        "e725e31a-0a10-4393-9ae7-1f4a82ca31ce": "Majors"}
print("\n=== 实例 ===")
for sid, nm in INST.items():
    c.execute("SELECT status FROM strategy_instances WHERE id=%s", (sid,))
    st = c.fetchone()
    c.execute("SELECT COUNT(*) n FROM strategy_positions WHERE strategy_id=%s AND status='open'", (sid,))
    op = c.fetchone()["n"]
    c.execute("""SELECT COUNT(*) n FROM strategies WHERE id=%s""" if False else
               "SELECT COUNT(*) n FROM strategy_positions WHERE strategy_id=%s AND status='closed'", (sid,))
    print("  %-8s status=%-8s open=%d" % (nm, st["status"] if st else "?", op))
    del st

print("\n=== 近 3h EXIT_AUDIT (action, reason) ===")
c.execute("""SELECT message FROM strategy_logs
             WHERE message LIKE '[EXIT_AUDIT]%%' AND created_at >= NOW() - INTERVAL 3 HOUR""")
ec = defaultdict(int)
for r in c.fetchall():
    a = rr = "?"
    for part in r["message"].split():
        if part.startswith("action="):
            a = part.split("=", 1)[1]
        elif part.startswith("reason="):
            rr = part.split("=", 1)[1]
    ec[(a, rr)] += 1
for k, v in sorted(ec.items(), key=lambda x: -x[1]):
    print("   %-28s %d" % (str(k), v))
if not ec:
    print("   无")

print("\n=== 连亏熔断/黑名单拦截（近 1h）===")
c.execute("""SELECT COUNT(*) n FROM strategy_logs
             WHERE message LIKE '%%连亏熔断%%' AND created_at >= NOW() - INTERVAL 1 HOUR""")
print("   连亏熔断拦截 = %d" % c.fetchone()["n"])
c.execute("""SELECT LEFT(message,34) p, COUNT(*) n FROM strategy_logs
             WHERE message LIKE '跳过信号%%' AND created_at >= NOW() - INTERVAL 3 HOUR
             GROUP BY p ORDER BY n DESC LIMIT 6""")
for r in c.fetchall():
    print("   %-36s %d" % (r["p"], r["n"]))
cn.close()
