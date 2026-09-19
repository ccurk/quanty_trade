#!/usr/bin/env bash
# ============================================================================
# owner 直令落地（2026-09-19 15:4xZ）：去掉冷却
#
#   「修改一下，不需要冷却」
#
# 动作：两个 running 实例的 symbol_reentry_cooldown_minutes → 0
#   Meme_合约信号计算引擎_1    3 → 0
#   Majors_BTC_ETH_BNB_SOL    15 → 0
#
# 【为什么 0 就是"不冷却"】backend/internal/strategy/strategy_signal.go
#   func symbolReentryCooldown(inst) { minutes := ...; if minutes <= 0 { return 0 } }
#   func canOpenSymbolByCooldown(inst, stats) { if cooldown <= 0 { return true, ... } }
# 两个闸门都读这一个键：信号层排序用 loadRecentEntryStats，
# 下单口 strategy_position.go:86 复检同一函数 ⇒ 置 0 即两处同时失效。
#
# 【同步说明】代码侧（strategy_signal.go）已把排序里的
#   penalty = 1 + 近期*0.35 + 连开*0.5  →  改为「上一笔平仓盈利的 symbol 优先」
# 那条**必须重建镜像才生效**，不随本脚本生效。本脚本只落配置层。
#
# 用法：
#   bash qt_patch_no_cooldown.sh              # 落
#   bash qt_patch_no_cooldown.sh --rollback   # 按备份还原
#
# 备份：/root/qt_patch_no_cooldown.bak  (600)
# ============================================================================
set -euo pipefail
MODE="${1:-apply}"
python3 - "$MODE" <<'PY'
import json, sys, urllib.request, urllib.error, pymysql

mode = sys.argv[1]
BAK = "/root/qt_patch_no_cooldown.bak"
BASE = "http://127.0.0.1:8080"
LOGIN_USER = "admin"
KEY = "symbol_reentry_cooldown_minutes"
TARGETS = {
    "8eb182b6-ee74-4125-a602-f0a91f376432": "Meme_合约信号计算引擎_1",
    "e725e31a-0a10-4393-9ae7-1f4a82ca31ce": "Majors_BTC_ETH_BNB_SOL",
}

env = {}
for line in open("/etc/quanty/backend.env"):
    line = line.strip()
    if line and not line.startswith("#") and "=" in line:
        k, v = line.split("=", 1); env[k.strip()] = v.strip().strip('"').strip("'")

def call(method, path, body=None, tok=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(BASE + path, data=data, method=method)
    r.add_header("Content-Type", "application/json")
    if tok: r.add_header("Authorization", "Bearer " + tok)
    try:
        with urllib.request.urlopen(r, timeout=30) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        return e.code, e.read()[:400].decode("utf8", "replace")

st, tok = call("POST", "/api/login", {"username": LOGIN_USER, "password": env["ADMIN_PASSWORD"]})
if st != 200:
    sys.exit("login failed: %s %s" % (st, tok))
tok = tok.get("token") or tok.get("access_token") or tok.get("data", {}).get("token")
print("login ok")

def read_live():
    """新连接读（REPEATABLE READ：长连接首条 SELECT 定快照，必须新连接回读）。"""
    cn = pymysql.connect(host="127.0.0.1", user=env["DB_USER"], password=env["DB_PASS"],
                         database="quanty_trade", charset="utf8mb4",
                         cursorclass=pymysql.cursors.DictCursor)
    c = cn.cursor()
    c.execute("SELECT id,status,config FROM strategy_instances WHERE id IN (%s,%s)",
              tuple(TARGETS.keys()))
    out = {}
    for r in c.fetchall():
        cfg = json.loads(r["config"]) if r["config"] else {}
        out[r["id"]] = (r["status"], cfg.get(KEY))
    c.execute("SELECT COUNT(*) n FROM strategy_positions WHERE status='open' AND strategy_id IN (%s,%s)",
              tuple(TARGETS.keys()))
    out["_open"] = c.fetchone()["n"]
    cn.close()
    return out

if mode == "--rollback":
    bak = json.load(open(BAK))
    for sid, val in bak.items():
        st, r = call("PATCH", "/api/strategies/%s/config" % sid, {KEY: val}, tok)
        print("  rollback %s -> %r  HTTP %s" % (TARGETS.get(sid, sid), val, st))
else:
    cur = read_live()
    print("PATCH 前: 在仓=%d" % cur["_open"])
    json.dump({sid: cur[sid][1] for sid in TARGETS}, open(BAK, "w"))
    import os; os.chmod(BAK, 0o600)
    print("备份 -> %s" % BAK)
    for sid, name in TARGETS.items():
        before = cur[sid][1]
        print("  %-26s %s = %r" % (name, KEY, before))
        if before == 0:
            print("     已经是 0，跳过")
            continue
        st, r = call("PATCH", "/api/strategies/%s/config" % sid, {KEY: 0}, tok)
        print("     PATCH -> 0  HTTP %s  %s" % (st, str(r)[:160]))

after = read_live()
print("\nPATCH 后（新连接回读）: 在仓=%d" % after["_open"])
for sid, name in TARGETS.items():
    print("  %-26s status=%-8s %s = %r" % (name, after[sid][0], KEY, after[sid][1]))
PY
