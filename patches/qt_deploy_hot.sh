#!/usr/bin/env bash
# ============================================================================
# S36 部署: 热点币名单 → redis → 模板 998 择优加权
# owner 直令 2026-09-19「你不需要验证，按照我说的，直接修改。」
#
# 顺序: 上传 → 改模板(自带语法验证,失败即中止) → 重启 Meme → 装 cron → 首跑 → 回读
# 幂等: patch 自带护栏(已改过则跳过); cron 文件覆盖写
# 回滚: bash /root/qt_ops/qt_patch_hot_priority.sh --rollback  然后重启 Meme
# ============================================================================
set -euo pipefail

SRV=mycloud
OPS=/root/qt_ops
PKG=/Users/black/basis/quanty_trade

echo "═══ [1/6] 上传 ═══"
ssh $SRV "mkdir -p $OPS"
scp -q "$PKG/ops/hot_symbols.py"                  "$SRV:$OPS/hot_symbols.py"
scp -q "$PKG/patches/qt_patch_hot_priority.sh"    "$SRV:$OPS/qt_patch_hot_priority.sh"
ssh $SRV "chmod 700 $OPS/hot_symbols.py $OPS/qt_patch_hot_priority.sh; ls -l $OPS"

echo
echo "═══ [2/6] 改模板 998 (含语法验证, 不通过则中止且不写回) ═══"
ssh $SRV "bash $OPS/qt_patch_hot_priority.sh"

echo
echo "═══ [3/6] dry-run 名单 (只看不写) ═══"
ssh $SRV "python3 $OPS/hot_symbols.py --dry-run"

echo
echo "═══ [4/6] 重启 Meme 实例 (restart 不平仓, 已源码裁决) ═══"
ssh $SRV 'python3 -' <<'PY'
import json, urllib.request, time, pymysql

env = {}
for L in open("/etc/quanty/backend.env"):
    L = L.strip()
    if L and not L.startswith("#") and "=" in L:
        k, v = L.split("=", 1); env[k.strip()] = v.strip().strip('"').strip("'")

cn = pymysql.connect(host="127.0.0.1", user=env["DB_USER"], password=env["DB_PASS"],
                     database="quanty_trade", charset="utf8mb4")
c = cn.cursor()
c.execute("SELECT id, name, status FROM strategy_instances WHERE id LIKE '8eb182b6%'")
row = c.fetchone()
if not row:
    print("⛔ 找不到 Meme 实例 8eb182b6*"); raise SystemExit(1)
sid, sname, sstatus = row
print("   实例: %s  status=%s  id=%s" % (sname, sstatus, sid))

c.execute("""SELECT COUNT(*) n, COALESCE(SUM(symbol IS NOT NULL),0) FROM strategy_positions
             WHERE strategy_name LIKE 'Meme%%' AND status='open'""")
print("   当前在仓 = %d （重启不平仓，仅重新加载模板）" % c.fetchone()[0])
cn.close()

def call(path, token=None, body=None):
    req = urllib.request.Request("http://127.0.0.1:8080/api" + path,
                                 data=json.dumps(body).encode() if body is not None else None,
                                 method="POST" if body is not None else "GET")
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        r = urllib.request.urlopen(req, timeout=20)
        return r.status, r.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()

st, body = call("/login", body={"username": "admin", "password": env["ADMIN_PASSWORD"]})
if st != 200:
    print("⛔ 登录失败 HTTP %d: %s" % (st, body[:300])); raise SystemExit(1)
d = json.loads(body)
print("   登录 OK，响应字段: %s" % list(d.keys()))
tok = d.get("token") or d.get("access_token") or d.get("data", {}).get("token")
if not tok:
    print("⛔ 响应里找不到 token 字段"); raise SystemExit(1)

st, body = call("/strategies/%s/stop" % sid, tok, {})
print("   stop  → HTTP %d %s" % (st, body[:160]))
time.sleep(4)
st, body = call("/strategies/%s/start" % sid, tok, {})
print("   start → HTTP %d %s" % (st, body[:160]))
time.sleep(3)
st, body = call("/strategies/%s" % sid, tok)
if st == 200:
    try:
        print("   重启后 status = %s" % json.loads(body).get("status"))
    except Exception:
        print("   重启后响应 = %s" % body[:200])
PY

echo
echo "═══ [5/6] 装 cron (每分钟) + 首跑 ═══"
# 注意: 不要 import redis —— 实测服务器系统 python3 的 pip list 只有 PyMySQL。
#        hot_symbols.py 已改用内置 socket 直发 RESP，零第三方依赖。
ssh $SRV "python3 -c 'import pymysql, socket; print(\" 依赖 OK: pymysql + socket(内置)\")'"
PY3=$(ssh $SRV 'command -v python3')   # cron 的 PATH 只有 /usr/bin:/bin, 必须写绝对路径
echo "   解释器: $PY3"
ssh $SRV "cat > /etc/cron.d/qt-hot-symbols <<CRON
# S36 热点币名单 → redis qt:hot:{sid}  (owner 直令 2026-09-19)
# T1=最近盈利的仓位  T2=近3h盈利的仓位  TTL 900s
* * * * * root $PY3 /root/qt_ops/hot_symbols.py >> /var/log/qt_hot_symbols.log 2>&1
CRON
chmod 644 /etc/cron.d/qt-hot-symbols; echo '  cron 已装:'; tail -2 /etc/cron.d/qt-hot-symbols"
ssh $SRV "python3 $OPS/hot_symbols.py"

echo
echo "═══ [6/6] 回读验证 ═══"
ssh $SRV 'python3 -' <<'PY'
import json, pymysql
env = {}
for L in open("/etc/quanty/backend.env"):
    L = L.strip()
    if L and not L.startswith("#") and "=" in L:
        k, v = L.split("=", 1); env[k.strip()] = v.strip().strip('"').strip("'")

cn = pymysql.connect(host="127.0.0.1", user=env["DB_USER"], password=env["DB_PASS"],
                     database="quanty_trade", charset="utf8mb4")
c = cn.cursor()
c.execute("SELECT length(code), code LIKE '%%_hot_weight%%', code LIKE '%%_refresh_hot%%', "
          "code LIKE '%%self._hot_weight(symbol)%%' FROM strategy_templates WHERE id=998")
n, w, r, u = c.fetchone()
print("   模板 998: %d 字节 | _hot_weight=%s | _refresh_hot=%s | 比较键已加权=%s" % (n, bool(w), bool(r), bool(u)))
c.execute("SELECT COUNT(*) FROM strategy_templates WHERE id=998 AND code LIKE '%%float(cand[\\\"confidence\\\"]) > float(cur[\\\"confidence\\\"])%%'")
print("   旧比较键残留 = %d (应为 0)" % c.fetchone()[0])
cn.close()

import redis as rm
r = rm.Redis(host="127.0.0.1", port=6379, db=0, password=env.get("REDIS_PASSWORD") or None, socket_timeout=5)
for k in sorted(r.scan_iter("qt:hot:*")):
    print("   redis %s = %s" % (k.decode(), r.get(k).decode()))
PY
echo
echo "✅ 部署完成"
