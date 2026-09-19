#!/usr/bin/env python3
"""平掉单个 symbol 的仓位（走平台 API，会一并撤 TP/SL 与挂单）。

用法:  python3 close_position.py HEIUSDT
注意 symbol 用【交易所原式】（HEIUSDT），不是 HEI/USDT。
只在服务器上跑 —— 它读 /etc/quanty/backend.env 并调 127.0.0.1:8080。
"""
import json, sys, time, urllib.request, urllib.error, pymysql

if len(sys.argv) < 2:
    raise SystemExit("用法: python3 close_position.py <交易所原式SYMBOL>   例: HEIUSDT")
SYM = sys.argv[1].upper()

env = {}
for L in open("/etc/quanty/backend.env"):
    L = L.strip()
    if L and not L.startswith("#") and "=" in L:
        k, v = L.split("=", 1); env[k.strip()] = v.strip().strip('"').strip("'")

cn = pymysql.connect(host="127.0.0.1", user=env["DB_USER"], password=env["DB_PASS"],
                     database="quanty_trade", charset="utf8mb4", cursorclass=pymysql.cursors.DictCursor)
c = cn.cursor()

print("=== 平仓前 ===")
c.execute("SELECT symbol, direction, amount, avg_price FROM strategy_positions "
          "WHERE symbol LIKE %s AND status='open'", (SYM[:-4] + "/%",))
rows = c.fetchall()
for r in rows:
    print("   %s %s amt=%s @%s" % (r["symbol"], r["direction"], r["amount"], r["avg_price"]))
if not rows:
    print("   (DB 里没有该 symbol 的 open 行 —— 仓位可能刚平/未记录)")

def call(path, token=None, body=None, method=None):
    m = method or ("POST" if body is not None else "GET")
    req = urllib.request.Request("http://127.0.0.1:8080/api" + path,
                                 data=json.dumps(body).encode() if body is not None else None, method=m)
    req.add_header("Content-Type", "application/json")
    if token: req.add_header("Authorization", "Bearer " + token)
    try:
        r = urllib.request.urlopen(req, timeout=30); return r.status, r.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()

st, body = call("/login", body={"username": "admin", "password": env["ADMIN_PASSWORD"]})
if st != 200:
    print("⛔ 登录失败 HTTP %d" % st); raise SystemExit(1)
d = json.loads(body)
tok = d.get("token") or d.get("access_token") or d.get("data", {}).get("token")
print("\n=== 平仓 ===")
st, body = call("/positions/close?symbol=" + SYM, tok, None, method="POST")
print("   POST /api/positions/close?symbol=%s → HTTP %d" % (SYM, st))
print("   %s" % body[:400])

print("\n=== 20 秒后回读（DB 是异步跟进的，必须等）===")
time.sleep(20)
c.execute("SELECT symbol, status, close_time, closed_qty, realized_pn_l FROM strategy_positions "
          "WHERE symbol LIKE %s ORDER BY open_time DESC LIMIT 3", (SYM[:-4] + "/%",))
for r in c.fetchall():
    print("   %s %s close_time=%s qty=%s pnl=%s" % (
        r["symbol"], r["status"], r["close_time"], r["closed_qty"], r["realized_pn_l"]))
c.execute("SELECT COUNT(*) n FROM strategy_positions WHERE strategy_id LIKE '8eb182b6%' AND status='open'")
n = c.fetchone()["n"]
print("\n   Meme 实例在仓 = %d %s" % (n, "✅ 已 flat，可以重启换引擎" if n == 0 else "⛔ 还有在仓"))
cn.close()
