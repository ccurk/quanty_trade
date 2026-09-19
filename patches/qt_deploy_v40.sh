#!/usr/bin/env bash
# ============================================================================
# 部署 v40 信号引擎到 Meme 实例 8eb182b6（owner 直令 2026-09-19「那就部署 v40」）
#
# 做法：**不改 998**，新建一个模板行装 v40 代码，把实例的 template_id 指过去。
#   ⇒ 回滚 = 把 template_id 指回 998 + 重启，一行命令（见文件尾）。
#
# 为什么必须重启：源码裁决 manager.go:1238-1251 —— 引擎在【启动路径】把模板代码
#   写成 _runtime/<instID>_<ts>.py 再跑，改模板不重启不会生效。
#   （策略配置那些键是每 tick 读 inst.Config，实时生效；模板代码不是。两者语义不同。）
#
# 部署前已核（2026-09-19 13:28Z 实测，非推测）：
#   - 契约五项与旧模板一致：频道名 / 配置键 / 信号 JSON 形状 / 币种解析 / 历史回灌去重
#   - 服务器侧 --selftest 全绿：908 个永续、四个 /futures/data/* 端点有真数、HTTP 9/0
#   - Meme 的 symbols 已恢复为空串（选币范围=全市场，select_limit=300 生效）
#
# 用法:  bash qt_deploy_v40.sh            # 部署
#        bash qt_deploy_v40.sh --rollback # 回滚到 998
# ============================================================================
set -euo pipefail

SRV=mycloud
OPS=/root/qt_ops
PKG=/Users/black/basis/quanty_trade
LOCAL_ENGINE="$PKG/strategies/crypto_perp_engine_v40.py"
INST=8eb182b6
OLD_TPL=998

if [[ "${1:-}" == "--rollback" ]]; then
  echo "═══ 回滚：Meme 实例 template_id → $OLD_TPL ═══"
  ssh $SRV 'python3 -' <<PY
import json, urllib.request, time, pymysql
env = {}
for L in open("/etc/quanty/backend.env"):
    L = L.strip()
    if L and not L.startswith("#") and "=" in L:
        k, v = L.split("=", 1); env[k.strip()] = v.strip().strip('"').strip("'")
cn = pymysql.connect(host="127.0.0.1", user=env["DB_USER"], password=env["DB_PASS"],
                     database="quanty_trade", charset="utf8mb4")
c = cn.cursor()
c.execute("UPDATE strategy_instances SET template_id=$OLD_TPL, updated_at=NOW(3) WHERE id LIKE '$INST%'")
cn.commit()
c.execute("SELECT id, template_id, status FROM strategy_instances WHERE id LIKE '$INST%'")
print("   已写回:", c.fetchone())
c.execute("SELECT id FROM strategy_instances WHERE id LIKE '$INST%'"); sid = c.fetchone()[0]
cn.close()
def call(path, token=None, body=None):
    req = urllib.request.Request("http://127.0.0.1:8080/api" + path,
                                 data=json.dumps(body).encode() if body is not None else None,
                                 method="POST" if body is not None else "GET")
    req.add_header("Content-Type", "application/json")
    if token: req.add_header("Authorization", "Bearer " + token)
    try:
        r = urllib.request.urlopen(req, timeout=20); return r.status, r.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()
st, body = call("/login", body={"username": "admin", "password": env["ADMIN_PASSWORD"]})
d = json.loads(body); tok = d.get("token") or d.get("access_token") or d.get("data", {}).get("token")
print("   stop  →", call("/strategies/%s/stop" % sid, tok, {})[0]); time.sleep(4)
print("   start →", call("/strategies/%s/start" % sid, tok, {})[0])
PY
  echo "✅ 已回滚到模板 $OLD_TPL（模板代码无改动，随时可再切回）"
  exit 0
fi

echo "═══ [1/6] 上传引擎 ═══"
ssh $SRV "mkdir -p $OPS"
scp -q "$LOCAL_ENGINE" "$SRV:$OPS/crypto_perp_engine_v40.py"
ssh $SRV "chmod 700 $OPS/crypto_perp_engine_v40.py; md5sum $OPS/crypto_perp_engine_v40.py"
echo -n "   本地 md5: "; md5 -q "$LOCAL_ENGINE"

echo
echo "═══ [2/6] 语法 + 自检（真连 Binance；不过就中止，一个字节都不写 DB）═══"
ssh $SRV 'python3 -c "import ast; ast.parse(open(\"/root/qt_ops/crypto_perp_engine_v40.py\").read()); print(\"   语法 OK\")"'
ssh $SRV "cd $OPS && timeout 120 python3 crypto_perp_engine_v40.py --selftest" | tail -8

echo
echo "═══ [3/6] 建新模板行（998 原件不动）+ 指向 + 重启 ═══"
ssh $SRV 'python3 -' <<PY
import json, urllib.request, time, hashlib, pymysql, datetime

env = {}
for L in open("/etc/quanty/backend.env"):
    L = L.strip()
    if L and not L.startswith("#") and "=" in L:
        k, v = L.split("=", 1); env[k.strip()] = v.strip().strip('"').strip("'")

code = open("/root/qt_ops/crypto_perp_engine_v40.py", encoding="utf-8").read()
h = hashlib.md5(code.encode()).hexdigest()[:8]

cn = pymysql.connect(host="127.0.0.1", user=env["DB_USER"], password=env["DB_PASS"],
                     database="quanty_trade", charset="utf8mb4")
c = cn.cursor()

# ---- ★ 前置硬闸：持仓未平 ⇒ stop 会被拒（strategy_lifecycle.go:247 无 force 开关）----
# 2026-09-19 13:34 就是这么留下双面状态的：template_id 已写 1057，但运行脚本没重生，
# 实盘仍跑 v39。⇒ 先查在仓，非零就【一个字节都不写】，让状态始终单一。
# 注意：本行【不传 args】—— pymysql 只有传了 args 才处理 %，无 args 时 % 是字面量。
# 之前写成 '%%s%%' % 单值 ⇒ '%%' 已是字面 %，没有真占位符 ⇒ TypeError。别再加 %%。
c.execute("SELECT COUNT(*) FROM strategy_positions WHERE strategy_id LIKE '$INST%' AND status='open'")
n_open = c.fetchone()[0]
if n_open > 0:
    print("")
    print("⛔ 该实例有 %d 个在仓 —— 引擎拒绝 stop（strategy has open positions），" % n_open)
    print("   重启换引擎这一步做不了。DB 未做任何改动（template_id 保持原值）。")
    print("   等它自然平仓（交易所 algo 腿 / max_hold）后重跑本脚本即可；脚本是幂等的。")
    cn.close(); raise SystemExit(2)
print("   前置检查: 在仓 0 ✅")

# ---- 备份（幂等：已存在就覆盖，它只是回滚说明用）----
c.execute("SELECT id, name, path, author_id, template_type FROM strategy_templates WHERE id=$OLD_TPL")
r = c.fetchone()
c.execute("SELECT id, template_id, status FROM strategy_instances WHERE id LIKE '$INST%'")
inst = c.fetchone()
ts = datetime.datetime.utcnow().strftime("%Y%m%d-%H%M%S")
backup = {"ts_utc": ts, "old_template": r, "instance_before": inst,
          "rollback": "UPDATE strategy_instances SET template_id=$OLD_TPL WHERE id LIKE '$INST%'; 然后重启"}
open("/root/qt_ops/v40_backup_%s.json" % ts, "w").write(json.dumps(backup, ensure_ascii=False, default=str, indent=1))
print("   备份 → /root/qt_ops/v40_backup_%s.json" % ts)
print("   实例原状: id=%s template_id=%s status=%s" % inst)

# ---- 幂等：同一份代码已建过就复用 ----
name = "Meme_v40_crypto_perp_%s" % ts
c.execute("SELECT id, name, MD5(code) m FROM strategy_templates WHERE name LIKE 'Meme_v40_crypto_perp_%%' ORDER BY id DESC")
prev = c.fetchone()
if prev and prev[2] == hashlib.md5(code.encode()).hexdigest():
    new_id = prev[0]
    print("   已存在同内容模板 id=%s（%s），复用" % (new_id, prev[1]))
else:
    c.execute("SELECT COALESCE(MAX(id),0)+1 FROM strategy_templates"); new_id = c.fetchone()[0]
    c.execute("""INSERT INTO strategy_templates
                 (id, name, description, path, template_type, author_id,
                  is_public, is_draft, is_enabled, code, created_at, updated_at)
                 VALUES (%s,%s,%s,%s,%s,%s,0,0,1,%s,NOW(3),NOW(3))""",
              (new_id, name, "v40 加密永续原生信号引擎（真实取 premiumIndex + /futures/data/*，成本感知闸门，多空对称）",
               "db://template/" + name, r[4] or "strategy", r[3] or 1, code))
    cn.commit()
    print("   新模板 id=%s name=%s code=%d 字符 md5=%s" % (new_id, name, len(code), h))

# ---- 指向 ----
c.execute("UPDATE strategy_instances SET template_id=%s, updated_at=NOW(3) WHERE id LIKE '%s%%'" % (new_id, "$INST"))
cn.commit()
c.execute("SELECT id, template_id, status FROM strategy_instances WHERE id LIKE '%s%%'" % "$INST")
print("   实例现状:", c.fetchone())

# ---- 重启：只换运行脚本，不动仓位（仓为 0 已由前置硬闸保证）----
c.execute("SELECT id FROM strategy_instances WHERE id LIKE '%s%%'" % "$INST"); sid = c.fetchone()[0]
cn.close()

def call(path, token=None, body=None):
    req = urllib.request.Request("http://127.0.0.1:8080/api" + path,
                                 data=json.dumps(body).encode() if body is not None else None,
                                 method="POST" if body is not None else "GET")
    req.add_header("Content-Type", "application/json")
    if token: req.add_header("Authorization", "Bearer " + token)
    try:
        rr = urllib.request.urlopen(req, timeout=20); return rr.status, rr.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()

st, body = call("/login", body={"username": "admin", "password": env["ADMIN_PASSWORD"]})
if st != 200:
    print("⛔ 登录失败 HTTP %d: %s" % (st, body[:300])); raise SystemExit(1)
d = json.loads(body)
tok = d.get("token") or d.get("access_token") or d.get("data", {}).get("token")
if not tok:
    print("⛔ 响应里没有 token 字段:", list(d.keys())); raise SystemExit(1)
print("   登录 OK")
print("   stop  → HTTP %d %s" % call("/strategies/%s/stop" % sid, tok, {}))
time.sleep(4)
print("   start → HTTP %d %s" % call("/strategies/%s/start" % sid, tok, {}))
PY

echo
echo "═══ [4/6] 等 25 秒让它起进程 + 拉第一轮行情 ═══"
sleep 25

echo
echo "═══ [5/6] 回读：实例 / 运行脚本 / 日志 ═══"
ssh $SRV 'python3 -' <<PY
import pymysql
env = {}
for L in open("/etc/quanty/backend.env"):
    L = L.strip()
    if L and not L.startswith("#") and "=" in L:
        k, v = L.split("=", 1); env[k.strip()] = v.strip().strip('"').strip("'")
cn = pymysql.connect(host="127.0.0.1", user=env["DB_USER"], password=env["DB_PASS"],
                     database="quanty_trade", charset="utf8mb4", cursorclass=pymysql.cursors.DictCursor)
c = cn.cursor()
c.execute("SELECT id, name, template_id, status, updated_at FROM strategy_instances WHERE id LIKE '$INST%'")
print("   实例:", c.fetchone())

c.execute("SELECT message, created_at FROM strategy_logs WHERE message LIKE '%%生成运行脚本%%' ORDER BY id DESC LIMIT 1")
r = c.fetchone()
print("   运行脚本行:", (r["message"][-110:] if r else "(无)"))
if r:
    import re
    m = re.search(r"runtime=(\S+)", r["message"])
    if m:
        p = m.group(1)
        try:
            body = open(p, encoding="utf-8").read()
            print("   文件 %s：%d 字符  含 v40 标记=%s  含旧引擎标记=%s" % (
                p, len(body), "crypto_perp_engine_v40" in body or "START v40" in body,
                "LONG_BIAS_FACTOR" in body))
        except Exception as e:
            print("   ⛔ 读不到运行脚本 %s: %r" % (p, e))

for pat in ("START v40", "[v40]", "★信号", "预热不足", "无信号", "IDLE 等待K线"):
    c.execute("SELECT COUNT(*) n, MAX(created_at) mx FROM strategy_logs WHERE message LIKE %s "
              "AND created_at >= NOW() - INTERVAL 10 MINUTE", ("%" + pat + "%",))
    r = c.fetchone()
    print("   %-14s n=%-5s 最后=%s" % (pat, r["n"], r["mx"]))

c.execute("SELECT created_at, LEFT(message,140) m FROM strategy_logs ORDER BY id DESC LIMIT 10")
print("   --- 全库最新 10 条 ---")
for r in c.fetchall():
    print("      %s %s" % (r["created_at"].strftime("%H:%M:%S"), r["m"].replace("\n", " ")))
cn.close()
PY

echo
echo "═══ [6/6] 完成 ═══"
echo "   回滚:  bash $PKG/patches/qt_deploy_v40.sh --rollback"
echo "   观察点: 前 30 分钟看『无信号 sym=... ATR%=... 预期...%% 需0.2000%% 资金费率=...』这行"
echo "           —— 若全部因『预期 < 需』被砍，说明成本闸门在这个 ATR 分布下太紧（可调 cost_mult）。"
