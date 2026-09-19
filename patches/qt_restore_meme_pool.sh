#!/usr/bin/env bash
# ============================================================================
# 恢复 Meme 主池的选币范围 —— 把 config 的 symbols 从硬编码 7 币写回【空串】
#
# 【为什么是这一个键】
# 2026-09-19 07:13:23（seq=80, actor=claude_cron）一次性改了 5 个键：
#     symbols:            ''      →  ['G/USDT','BULLA/USDT','MAGMA/USDT','UAI/USDT',
#                                     'HEI/USDT','DRIFT/USDT','ARB/USDT']   ← 元凶
#     auto_symbols:       True    →  False
#     symbol_select_mode: 'filter'→  'manual'
#     min_volatility:     1       →  0
#     max_price:          1e12    →  0        （seq=81 已把它还原回 1e12）
#
# 引擎 backend/internal/strategy/strategy_start.go:241 的分支：
#     useFilter := fixedSymbol=="" && (selectMode=="filter" || autoSymbols
#                  || minPrice>0 || maxPrice>0 || minPrecision>0 || minVolatility>0)
#     if !useFilter { return validateFeedSymbolsForExchange(inst, feedSymbols, ...) }
#     ... SelectSymbolsDetailed({OnlySymbols: feedSymbols, Limit: select_limit=300})
#
# ⇒ **symbols 非空时，它的值被当作选中器的 OnlySymbols 白名单**，
#   把 limit=300 的全市场选币箍死成那 7 个币。
#   （useFilter 至今仍为真 —— 靠的是 max_price=1e12>0，seq=81 还原的那次。）
#
# 【实测代价】
#   收窄前 09-18 12:00→09-19 07:13（19.22h）  235 笔 = 12.22 笔/时，93 个币
#   收窄后 07:13→10:40（3.44h）                  8 笔 =  2.32 笔/时， 3 个币
#   昨日同时段 07:13→12:00（4.78h）             40 笔 =  8.37 笔/时，22 个币
#
# 【为什么只改 symbols，不用还原另外 4 个键】
#   max_price=1e12>0 已经让 useFilter 为真；min_volatility=0 只会让候选更宽。
#   还原它们等于顺手改行为 —— 外科手术式改动只动必须动的。最小改动 = 最小风险。
#
# 【⚠️ 生效时机 —— 这点重要】
#   resolveFeedSymbols 全仓库只有【一个】调用点（strategy_start.go:152，在
#   buildStrategyStartPlan 里）⇒ **纯启动路径**。
#   ⇒ 只改配置【不会】改变运行中的 feed，**必须重启 Meme 策略才生效**。
#   本脚本默认【不】重启，只写配置 + 打印重启指引；重启与否由你决定。
#
# 用法：
#   bash qt_restore_meme_pool.sh              # 只写配置 + 校验（安全）
#   bash qt_restore_meme_pool.sh --rollback   # 还原成改前的值
#
# 回滚备份：/root/qt_restore_meme_pool.bak（改前 symbols 的 JSON 原文）
# ============================================================================
set -euo pipefail

SID="8eb182b6-ee74-4125-a602-f0a91f376432"     # Meme 主池
BAK="/root/qt_restore_meme_pool.bak"
BASE="http://127.0.0.1:8080"
MODE="${1:-apply}"

python3 - "$SID" "$BAK" "$BASE" "$MODE" <<'PY'
import json, sys, urllib.request, urllib.error, os

sid, bak, base, mode = sys.argv[1:5]

# 凭据只在进程内读，绝不进 argv / 绝不打印
env = {}
for line in open("/etc/quanty/backend.env"):
    line = line.strip()
    if line and not line.startswith("#") and "=" in line:
        k, v = line.split("=", 1)
        env[k.strip()] = v.strip().strip('"').strip("'")

def call(method, path, body=None, tok=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(base + path, data=data, method=method)
    r.add_header("Content-Type", "application/json")
    if tok:
        r.add_header("Authorization", "Bearer " + tok)
    try:
        with urllib.request.urlopen(r, timeout=30) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        return e.code, e.read()[:300].decode("utf8", "replace")

# ⚠️ 本脚本已被 qt_patch_owner_20260919b.sh 取代（后者把 symbols 恢复并进三笔改动）。
# 登录必须带 username 字段 —— 只传 password 会 400 "LoginRequest.Username ... required"。
st, body = call("POST", "/api/login", {"username": "admin", "password": env["ADMIN_PASSWORD"]})
tok = (body.get("token") or body.get("data", {}).get("token")) if isinstance(body, dict) else None
if not tok:
    print("⛔ 登录失败 HTTP %s，响应键: %s" % (st, list(body.keys()) if isinstance(body, dict) else body))
    sys.exit(1)
print("✅ 已登录")

if mode == "--rollback":
    if not os.path.exists(bak):
        print("⛔ 没有备份文件 %s，无法回滚" % bak); sys.exit(1)
    old = json.load(open(bak))["symbols"]
    st, r = call("PATCH", "/api/strategies/%s/config" % sid, {"symbols": old}, tok=tok)
    print("回滚 PATCH -> %s  %s" % (st, r))
    sys.exit(0)

st, cfg = call("GET", "/api/strategies/%s/config" % sid, tok=tok)
if not isinstance(cfg, dict):
    print("⛔ 读配置失败 HTTP %s: %s" % (st, cfg)); sys.exit(1)
cur = cfg.get("symbols")
print("改前 symbols = %r" % (cur,))

json.dump({"symbols": cur}, open(bak, "w"))
os.chmod(bak, 0o600)
print("已备份到 %s" % bak)

if cur in ("", [], None):
    print("ℹ️  symbols 已经是空 —— 无需修改（幂等）")
else:
    st, r = call("PATCH", "/api/strategies/%s/config" % sid, {"symbols": ""}, tok=tok)
    print("PATCH symbols='' -> HTTP %s  %s" % (st, r))

st, after = call("GET", "/api/strategies/%s/config" % sid, tok=tok)
print("改后 symbols = %r" % (after.get("symbols") if isinstance(after, dict) else after,))
ok = isinstance(after, dict) and after.get("symbols") in ("", [], None)
print("\n%s" % ("✅ 配置已还原为空串。" if ok else "⛔ 回读不是空串，请人工核对！"))
print("""
────────────────────────────────────────────────────────────────
⚠️ 还没生效。resolveFeedSymbols 只在【启动】路径被调用
   （strategy_start.go:152，全仓库唯一调用点）
   ⇒ 必须【重启 Meme 主池策略】才会用全市场选币（limit=300）。

   重启后 15 分钟内可这样验证池子确实回来了（只看订单广度）：
     mysql> SELECT COUNT(DISTINCT symbol), COUNT(*) FROM strategy_orders
            WHERE strategy_id LIKE '8eb182b6%' AND purpose='entry'
              AND requested_at >= NOW() - INTERVAL 15 MINUTE;
   期望：币数从 1~3 个 回到 10+ 个。

   回滚（把 symbols 写回 7 币）：
     bash qt_restore_meme_pool.sh --rollback
────────────────────────────────────────────────────────────────""")
PY
