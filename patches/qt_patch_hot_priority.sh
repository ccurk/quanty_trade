#!/usr/bin/env bash
# ============================================================================
# S36 择优加权 —— owner 直令 2026-09-19
#   「meme 开仓机会需要先看我们最近盈利的仓位，看看能不能继续开仓，然后在看近3h盈利的，
#     看看能不能开。然后在通用看。」
#   「上次盈利的仓位，下一次再开盈利的概率大一些。」
#   「那就改代码啊，非要纠结配置干啥。」「你不需要验证，按照我说的，直接修改。」
#
# 【改哪】strategy_templates id=998 (Meme_合约信号计算引擎_1_auto_v39)
#   共 4 处，全部在择优(best-pick)路径上：
#     1. __init__       加 hot_t1/hot_t2/_hot_refresh_ts 三个内存态
#     2. 新增两个方法    _refresh_hot() / _hot_weight()
#     3. _offer_best_pick 比较键  confidence → confidence * _hot_weight(symbol)
#     4. run() 主循环    每轮 _flush_best_pick() 前调 _refresh_hot()
#
# 【为什么是这个位置】实盘每批只有 1 个候选到达 Go 侧（全历史 782 条排序日志 100% 候选=1）
#   ⇒ Go 的 score 排序恒惰性。真正决定"同一批里谁被发出去"的是模板的 _offer_best_pick
#   （template code:1890 `if float(cand["confidence"]) > float(cur["confidence"])`）。
#   近24h 该判据实际发生竞争 102 次（落选98+替换4）对 44 个批首 ⇒ 平均每批 2.3 个候选在抢。
#
# 【权重】T1(最近盈利) 1.30  /  T2(近3h盈利) 1.15  /  其他 1.00
#   数据依据(全历史 1160 对「上一笔→下一笔」, 已去重):
#     上笔盈利→下笔 胜率 51.2% 均值 +0.0211U  vs  上笔亏损→下笔 49.8% / -0.0400U
#   方向与 owner 假设一致，但幅度小(+1.3pp，约 0.47 SE)。本改动兑现的是【少亏】不是【转正】。
#
# 【安全边界】加权只影响"同批里选谁"，不改 confidence 本身、不改 TP/SL、不改仓位。
#   名单缺失/redis 故障 ⇒ 权重全 1.0 ⇒ 完全退化为原有行为。
#
# 用法: bash qt_patch_hot_priority.sh            # 备份+改+语法验证+写回
#       bash qt_patch_hot_priority.sh --rollback # 从备份还原
# ============================================================================
set -euo pipefail
MODE="${1:-apply}"
python3 - "$MODE" <<'PY'
import json, sys, os, pymysql

mode = sys.argv[1]
TMPL = 998
BAK = "/root/qt_tmpl998_hot_priority.bak"

env = {}
for line in open("/etc/quanty/backend.env"):
    line = line.strip()
    if line and not line.startswith("#") and "=" in line:
        k, v = line.split("=", 1); env[k.strip()] = v.strip().strip('"').strip("'")
cn = pymysql.connect(host="127.0.0.1", user=env["DB_USER"], password=env["DB_PASS"],
                     database="quanty_trade", charset="utf8mb4")
c = cn.cursor()

if mode == "--rollback":
    if not os.path.exists(BAK):
        print("⛔ 无备份 %s" % BAK); sys.exit(1)
    old = open(BAK).read()
    c.execute("UPDATE strategy_templates SET code=%s WHERE id=%s", (old, TMPL))
    cn.commit(); cn.close()
    print("✅ 已从备份还原模板 %d（%d 字节）" % (TMPL, len(old)))
    sys.exit(0)

c.execute("SELECT code FROM strategy_templates WHERE id=%s", (TMPL,))
row = c.fetchone()
if not row:
    print("⛔ 找不到模板 %d" % TMPL); sys.exit(1)
code = row[0]
orig_len = len(code)

open(BAK, "w").write(code); os.chmod(BAK, 0o600)
print("备份 → %s (%d 字节)" % (BAK, orig_len))

# ---- 幂等护栏：已改过就别重复改 ----
if "_hot_weight" in code:
    print("ℹ️  模板里已存在 _hot_weight —— 已改过，退出（如需重来先 --rollback）")
    sys.exit(0)

# ---- 前置依赖：_refresh_hot 靠模板自己的 _signal_ch() 反推 redis 键 ----
if "def _signal_ch" not in code:
    print("⛔ 模板里找不到 `def _signal_ch` ⇒ 无法反推 redis 键，中止（未做任何修改）")
    sys.exit(1)

# ============ 改动 1: 新增 _refresh_hot / _hot_weight（插在 _offer_best_pick 之前）============
# 刻意不碰 __init__：状态用 getattr 惰性初始化，少一个锚点 = 少一处改错的机会。
A1 = "    def _offer_best_pick(self, symbol: str, direction: str, entry_price: float, tp: float, sl: float, confidence: float):\n"
A1_NEW = (
    "    def _refresh_hot(self):\n"
    "        # S36 @2026-09-19 owner直令: \"先看最近盈利的仓位, 再看近3h盈利的, 然后再通用\"。\n"
    "        # 名单由 ops/hot_symbols.py 写入 redis <prefix>:hot:<sid>。30s 节流。\n"
    "        # 局部 import + 全包异常: redis 抖动/回测 shim 缺 execute 时静默退化(权重全 1.0),\n"
    "        # 完全等价于改动前的行为。\n"
    "        import time as _t\n"
    "        _now = _t.time()\n"
    "        if _now - getattr(self, \"_hot_refresh_ts\", 0.0) < 30.0:\n"
    "            return\n"
    "        self._hot_refresh_ts = _now\n"
    "        try:\n"
    "            import json as _j\n"
    "            # 复用模板自身的通道命名反推 hot 键: <prefix>:signal:<sid> → <prefix>:hot:<sid>\n"
    "            _ch = self._signal_ch()\n"
    "            _key = _ch.split(\":signal:\")[0] + \":hot:\" + _ch.rsplit(\":\", 1)[-1]\n"
    "            # 不猜连接属性名: 遍历实例属性找带 execute 的那个\n"
    "            _ex = None\n"
    "            for _v in list(vars(self).values()):\n"
    "                _e = getattr(_v, \"execute\", None)\n"
    "                if _e is not None:\n"
    "                    _ex = _e\n"
    "                    break\n"
    "            if _ex is None:\n"
    "                return\n"
    "            _raw = _ex(\"GET\", _key)\n"
    "            if not _raw:\n"
    "                return\n"
    "            _d = _j.loads(_raw)\n"
    "            self.hot_t1 = {str(_x).replace(\"/\", \"\").upper() for _x in (_d.get(\"t1\") or [])}\n"
    "            self.hot_t2 = {str(_x).replace(\"/\", \"\").upper() for _x in (_d.get(\"t2\") or [])}\n"
    "        except Exception:\n"
    "            pass\n"
    "\n"
    "    def _hot_weight(self, symbol) -> float:\n"
    "        # S36: T1(最近盈利)=1.30 > T2(近3h盈利)=1.15 > 其他=1.00。\n"
    "        # 只用于同批择优的比较键, 不改 confidence 本身、不改 TP/SL、不改仓位。\n"
    "        try:\n"
    "            _s = str(symbol).replace(\"/\", \"\").upper()\n"
    "            _t1 = getattr(self, \"hot_t1\", None)\n"
    "            if _t1 and _s in _t1:\n"
    "                return 1.30\n"
    "            _t2 = getattr(self, \"hot_t2\", None)\n"
    "            if _t2 and _s in _t2:\n"
    "                return 1.15\n"
    "        except Exception:\n"
    "            pass\n"
    "        return 1.0\n"
    "\n"
) + A1

# ============ 改动 2: 择优比较键加权 ============
A2 = (
    "        # 同批竞争: 严格更高才换, 平手保先到者(其K线更早收盘, 数据更新鲜)。窗口锚定批首不延长。\n"
    "        if float(cand[\"confidence\"]) > float(cur[\"confidence\"]):\n"
)
A2_NEW = (
    "        # 同批竞争: 严格更高才换, 平手保先到者(其K线更早收盘, 数据更新鲜)。窗口锚定批首不延长。\n"
    "        # S36 @2026-09-19: 比较键加热度权重 —— 让\"上一笔赚过的币\"在同批竞争中更容易胜出。\n"
    "        # 权重 1.0 = 完全原有行为。cur 侧单独 try: 取不到 symbol 就不加权, 绝不让它抛。\n"
    "        _nk = float(cand[\"confidence\"]) * self._hot_weight(symbol)\n"
    "        _ck = float(cur[\"confidence\"])\n"
    "        try:\n"
    "            _ck = _ck * self._hot_weight(cur.get(\"symbol\"))\n"
    "        except Exception:\n"
    "            pass\n"
    "        if _nk > _ck:\n"
)

# ============ 改动 3: 主循环刷新名单 ============
A3 = "            self._flush_best_pick()\n"
A3_NEW = "            self._refresh_hot()\n            self._flush_best_pick()\n"

ok = True
for name, anchor, repl in (("1.新增_refresh_hot/_hot_weight", A1, A1_NEW),
                           ("2.择优比较键加权", A2, A2_NEW),
                           ("3.主循环刷新名单", A3, A3_NEW)):
    n = code.count(anchor)
    if n != 1:
        print("⛔ 改动 %s 锚点匹配 %d 次（应为 1）⇒ 中止，未做任何修改" % (name, n))
        if n == 0:
            _hint = [L for L in code.split("\n")
                     if ("confidence" in L and "float(cand" in L) or "_flush_best_pick" in L
                     or "def _offer_best_pick" in L]
            print("   该锚点的近似候选行（供修正锚点用）:")
            for L in _hint[:12]:
                print("     | %s" % L)
        ok = False
        break
    code = code.replace(anchor, repl, 1)
    print("   ✅ %s" % name)

if not ok:
    sys.exit(1)

# ---- 语法验证：编译不过绝不写回 ----
try:
    compile(code, "<tmpl998>", "exec")
    print("   ✅ Python 语法验证通过")
except SyntaxError as e:
    print("⛔ 语法错误 line %s: %s ⇒ 中止，未写回" % (e.lineno, e.msg))
    sys.exit(1)

c.execute("UPDATE strategy_templates SET code=%s WHERE id=%s", (code, TMPL))
cn.commit()
print("\n✅ 模板 %d 已更新: %d → %d 字节 (+%d)" % (TMPL, orig_len, len(code), len(code)-orig_len))

# ---- 回读校验 ----
c.execute("SELECT code FROM strategy_templates WHERE id=%s", (TMPL,))
back = c.fetchone()[0]
print("   回读校验: %s" % ("✅ 一致" if back == code else "⛔ 不一致！"))
print("   含 _hot_weight = %s / 含 _refresh_hot = %s / 保留 confidence 比较 = %s" % (
    "_hot_weight" in back, "_refresh_hot" in back, "float(cand[\"confidence\"])" in back))
cn.close()

print("""
────────────────────────────────────────────────────────────────
⚠️ 模板改动【需重启 Meme 策略】才生效（子进程重新落盘执行新 code）。

重启：
  curl -s -X POST http://127.0.0.1:8080/api/strategies/8eb182b6-ee74-4125-a602-f0a91f376432/stop \\
       -H "Authorization: Bearer <token>"
  ... 再 start

回滚：bash qt_patch_hot_priority.sh --rollback
────────────────────────────────────────────────────────────────""")
PY
