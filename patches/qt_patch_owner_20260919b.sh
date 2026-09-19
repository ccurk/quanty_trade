#!/usr/bin/env bash
# ============================================================================
# owner 直令落地（2026-09-19 11:0xZ）
#
#   A. 关掉 Meme 的 hunger          →  Meme   hunger_mode_enabled: true → false
#   B. 主流币并发变成 2              →  Majors max_concurrent_positions: 5 → 2
#   C. （前次直令「把池子恢复回去，落地」仍未落）Meme symbols → ''
#
# 【生效时机 —— 三笔不同，这点必须分清】
#   A. `hunger_mode_enabled` **实时生效，不需要重启**。
#      证据：backend/internal/strategy/quick_trade_monitor.go:102
#        enabled, after, hungerTPPct, hungerSLPct := resolveHungerMode(inst)
#      这一行在 quick-trade 监视器的 tick 循环体内，每轮读 inst.Config。
#   B. `max_concurrent_positions` 见脚本运行后打印的判定（同法已查/待查）。
#   C. `symbols` **只在启动路径解析**，必须重启 Meme 才生效。
#      证据：strategy_start.go:241 resolveFeedSymbols；全仓库唯一调用点 :152（buildStrategyStartPlan）
#
# 【为什么关 Meme 的 hunger】
#   Majors 已关（seq=6, hunger=False），持仓从 3 分钟变成 134 分钟；
#   Meme 仍开（seq=92, hunger=True, sl=4%, tp=30%, hunger_after_minutes=1）。
#   本轮实证连续两笔在开仓后 ~2.5 分钟被 hunger 砍掉：
#     DRIFT 10:38:32→10:40:59  持 2m27s  roi −4.0354%
#     G     10:46:31→10:49:19  持 2m48s  roi −4.2083%   ← 这笔是 short，不是方向问题
#
# 用法：
#   bash qt_patch_owner_20260919b.sh              # 三笔全落
#   bash qt_patch_owner_20260919b.sh --rollback   # 按备份还原
#
# 备份：/root/qt_patch_owner_20260919b.bak  (600)
# ============================================================================
set -euo pipefail
MODE="${1:-apply}"
python3 - "$MODE" <<'PY'
import json, sys, os, urllib.request, urllib.error, pymysql

mode = sys.argv[1]
BAK = "/root/qt_patch_owner_20260919b.bak"
BASE = "http://127.0.0.1:8080"
# 登录要 username+password 两个字段，且 username 是 users 表里的账号（bcrypt 比对），
# 不是 backend.env 里的值。users 表实测只有两个账号：admin(id=1,role=admin)、claude_cron(id=2)。
# 第一版脚本只传 password ⇒ HTTP 400 "LoginRequest.Username failed on the 'required' tag"。
LOGIN_USER = "admin"

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

st, body = call("POST", "/api/login", {"username": LOGIN_USER, "password": env["ADMIN_PASSWORD"]})
tok = (body.get("token") or body.get("data", {}).get("token")) if isinstance(body, dict) else None
if not tok:
    print("⛔ 登录失败 HTTP %s: %s" % (st, body)); sys.exit(1)
print("✅ 已登录\n")

cn = pymysql.connect(host="127.0.0.1", user=env["DB_USER"], password=env["DB_PASS"],
                     database="quanty_trade", charset="utf8mb4", cursorclass=pymysql.cursors.DictCursor)
c = cn.cursor()
c.execute("""SELECT strategy_id, strategy_name, config_json FROM strategy_param_versions WHERE is_current=1""")
sid, cfgcur = {}, {}
for r in c.fetchall():
    sid[r["strategy_name"]] = r["strategy_id"]
    try: cfgcur[r["strategy_name"]] = json.loads(r["config_json"]) if isinstance(r["config_json"], str) else r["config_json"]
    except Exception: cfgcur[r["strategy_name"]] = {}
# 不关连接：回读要用。（注意：API 里 **没有** GET /api/strategies/:id/config 这个路由，
# 实测 404 ⇒ 第一版脚本拿它做回读，三行都印成 `回读=None`，是误报，不是写入失败。）

MEME = "Meme_合约信号计算引擎_1"
MAJ  = "Majors_BTC_ETH_BNB_SOL"
for n in (MEME, MAJ):
    if n not in sid: print("⛔ 找不到实例 %s" % n); sys.exit(1)

# 目标：三笔
TARGETS = [
    (MEME, "hunger_mode_enabled",      False, "关 hunger（owner 直令 A）"),
    (MAJ,  "max_concurrent_positions", 2,     "主流币并发 5→2（owner 直令 B）"),
    (MEME, "symbols",                  "",    "池子恢复（前次直令 C，需重启才生效）"),
]

if mode == "--rollback":
    if not os.path.exists(BAK): print("⛔ 无备份 %s" % BAK); sys.exit(1)
    old = json.load(open(BAK))
    for name, key, _v, _d in TARGETS:
        if name in old and key in old[name]:
            st, r = call("PATCH", "/api/strategies/%s/config" % sid[name], {key: old[name][key]}, tok)
            print("回滚 %-28s %-24s -> HTTP %s %s" % (name[:28], key, st, str(r)[:120]))
    sys.exit(0)

bak = {}
print("=== 改前值 ===")
for name, key, _v, desc in TARGETS:
    cur = cfgcur.get(name, {}).get(key, "<键不存在>")
    if isinstance(cur, list): cur_disp = "%d 币 %s" % (len(cur), cur[:3])
    else: cur_disp = repr(cur)
    bak.setdefault(name, {})[key] = cfgcur.get(name, {}).get(key)
    print("   %-28s %-24s = %s" % (name[:28], key, cur_disp))
    print("        → %s" % desc)
json.dump(bak, open(BAK, "w")); os.chmod(BAK, 0o600)
print("\n备份 → %s (600)\n" % BAK)

print("=== 逐笔写入 + 回读校验 ===")
allok = True
for name, key, val, desc in TARGETS:
    st, r = call("PATCH", "/api/strategies/%s/config" % sid[name], {key: val}, tok)
    # 回读改走 DB（strategy_param_versions.config_json 的 is_current 行）—— API 无 GET /config 路由
    c.execute("""SELECT config_json FROM strategy_param_versions
                 WHERE strategy_id = %s AND is_current = 1 ORDER BY seq DESC LIMIT 1""", (sid[name],))
    row = c.fetchone()
    after = {}
    if row:
        try: after = json.loads(row["config_json"]) if isinstance(row["config_json"], str) else row["config_json"]
        except Exception: after = {}
    got = after.get(key, "<键不存在>")
    ok = (got == val) or (val == "" and got in ("", None, []))
    allok = allok and ok
    gd = ("%d 币 %s" % (len(got), got[:3])) if isinstance(got, list) else repr(got)
    print("   %s %-28s %-24s PATCH=%s  回读=%s" % ("✅" if ok else "⛔", name[:28], key, st, gd))
    if not ok and isinstance(r, str): print("        响应: %s" % r[:200])

print("\n%s" % ("✅ 三笔全部写入并回读一致。" if allok else "⛔ 有写入未生效，见上。"))
print("""
────────────────────────────────────────────────────────────────
生效时机（三笔不同）：
  A. hunger_mode_enabled  → **已实时生效，不用重启**（quick_trade_monitor.go:102 每 tick 读）
  B. max_concurrent_positions → 同上按 tick 读则已生效；否则下次重启生效
  C. symbols              → **必须重启 Meme 才生效**（resolveFeedSymbols 只在启动路径）

验证 Meme hunger 已关（重启与否都该看到）：
  之后不应再出现新的 `饥饿模式触发` 行；持仓不再在 ~2.5 分钟被砍。

回滚：bash qt_patch_owner_20260919b.sh --rollback
────────────────────────────────────────────────────────────────""")
PY
