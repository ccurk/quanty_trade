#!/usr/bin/env bash
# ============================================================================
# owner 直令落地（2026-09-19 16:5xZ）：连亏熔断的配置键显式化
#
#   「如果一个 symbol 连续 3次亏损，直接拉黑 48h 。」（owner 选 Go 侧做）
#
# 【这个脚本在做什么】把三个键**显式写进两个实例的 config**：
#     loss_streak_blacklist_enabled = true
#     loss_streak_threshold         = 3
#     loss_streak_quarantine_hours  = 48
#
# 【注意】这三个值就是代码里的**默认值**（见
# backend/internal/strategy/strategy_loss_streak.go 的 lossStreakDefault* 常量，
# 缺键时默认 开/3/48h）⇒ 功能在镜像部署那一刻就已经生效，
# 本脚本**不改变任何行为**，只是把键写出来，让人能在配置里看见和改。
#
# 【为什么单独写出来还有意义】缺键时走上限/下限 clamp 是"看不见的默认"，
# 谁都不知道这个功能存在、阈值是多少；显式写出来之后 owner 可以直接改阈值，
# 不用回来读源码。
#
# 【热生效，不需要重启】代码只读 inst.Config() 的原子快照（symbolLossStreakBan），
# 不经过 argv ⇒ PATCH 后下一个 tick 就按新值判定。
#
# 用法：
#   bash qt_patch_loss_streak.sh              # 落
#   bash qt_patch_loss_streak.sh --rollback   # 按备份还原
#
# 备份：/root/qt_patch_loss_streak.bak  (600)
# ============================================================================
set -euo pipefail
MODE="${1:-apply}"
python3 - "$MODE" <<'PY'
import json, os, sys, urllib.request, urllib.error, pymysql

mode = sys.argv[1]
BAK = "/root/qt_patch_loss_streak.bak"
BASE = "http://127.0.0.1:8080"
LOGIN_USER = "admin"
DESIRED = {
    "loss_streak_blacklist_enabled": True,
    "loss_streak_threshold": 3,
    "loss_streak_quarantine_hours": 48,
}
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
    if tok:
        r.add_header("Authorization", "Bearer " + tok)
    try:
        with urllib.request.urlopen(r, timeout=30) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        return e.code, e.read()[:400].decode("utf8", "replace")


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
        out[r["id"]] = (r["status"], {k: cfg.get(k, "<缺键>") for k in DESIRED})
    c.execute("SELECT COUNT(*) n FROM strategy_positions WHERE status='open' AND strategy_id IN (%s,%s)",
              tuple(TARGETS.keys()))
    out["_open"] = c.fetchone()["n"]
    cn.close()
    return out


st, tok = call("POST", "/api/login", {"username": LOGIN_USER, "password": env["ADMIN_PASSWORD"]})
if st != 200:
    sys.exit("login failed: %s %s" % (st, tok))
tok = tok.get("token") or tok.get("access_token") or tok.get("data", {}).get("token")
print("login ok")

if mode == "--rollback":
    bak = json.load(open(BAK))
    for sid, vals in bak.items():
        st, r = call("PATCH", "/api/strategies/%s/config" % sid, vals, tok)
        print("  rollback %s -> %r  HTTP %s" % (TARGETS.get(sid, sid), vals, st))
else:
    cur = read_live()
    print("PATCH 前: 在仓=%d" % cur["_open"])
    json.dump({sid: cur[sid][1] for sid in TARGETS}, open(BAK, "w"))
    os.chmod(BAK, 0o600)
    print("备份 -> %s" % BAK)
    for sid, name in TARGETS.items():
        print("  %-26s 现值 = %r" % (name, cur[sid][1]))
        st, r = call("PATCH", "/api/strategies/%s/config" % sid, DESIRED, tok)
        print("     PATCH -> %r  HTTP %s  %s" % (DESIRED, st, str(r)[:160]))

after = read_live()
print("\nPATCH 后（新连接回读）: 在仓=%d" % after["_open"])
for sid, name in TARGETS.items():
    print("  %-26s status=%-8s %r" % (name, after[sid][0], after[sid][1]))
PY
