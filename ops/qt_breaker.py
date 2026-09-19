#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""独立熔断器 —— 与优化器解耦：优化器管「更好」，本脚本只管「别再流血」。

为什么必须有它（2026-09-17 实测）：
  _exp 里写着 rollback_if 「净利 < -4U → 立即回滚」，但 grep 全仓库 **零代码引用**。
  代码里只有 experiment_hold()（冻结），eval_after 到点做的事是**解冻**、不是回滚。
  于是 09-16 22:00 到点后：冻结静默解除，实验值默认转正，没有任何人做这个决定。
  实测那 48h 净利 -59.53U，是那条 -4U 阈值的 15 倍。
  本脚本把「挂在提示词里的一句话」变成「可执行的判定」。

与优化器的分工（**不要合并**）：
  * 优化器受三重约束：BOUNDS 白名单 + MAX_DRIFT(2×) + 实验冻结。它**改不动**出场几何 ——
    breakeven_trigger_atr / trailing_activation_atr / atr_sl_mult 都不在 BOUNDS 里，
    validate() 直接回「不在白名单内」。熔断器不能被这些约束绑住，所以独立成进程。
  * 熔断器只做**单向**动作：触发 → 降敞口。不抬敞口、不碰出场几何、不做任何「优化」。

判定口径全部来自 exchange_fills（权威账本，从交易所拉的真成交），
**不用 strategy_positions** —— 那是派生视图，实测有重复行（48h 内 389 raw → 352 去重）。
净利必须扣手续费：实测 5bps/笔、两腿合共 10bps/来回，不扣会把亏的算成平的。

用法：默认 dry-run（只算不动）。加 --apply 才真落配置。与优化器同一 idiom。
"""

import json
import os
import sys
import time
import urllib.error
import urllib.request

import pymysql

BACKEND = "http://localhost:8080"
BACKEND_ENV = "/etc/quanty/backend.env"
STATE = "/root/work/quanty_trade/ops/.qt_breaker.state.json"

APPLY = "--apply" in sys.argv
# 与优化器同源同默认值（deepseek_optimize.py:68），避免两处各写一份 ID 漂移。
STRATEGY_ID = os.environ.get("QT_STRATEGY_ID", "8eb182b6-ee74-4125-a602-f0a91f376432")
DB_NAME = os.environ.get("QT_DB_NAME", "quanty_trade")
WINDOW_HOURS = int(os.environ.get("QT_BREAKER_WINDOW_H", "6"))
NET_TRIP = float(os.environ.get("QT_BREAKER_NET", "-4.0"))      # 来自 _exp.rollback_if 原文
WIN_TRIP = float(os.environ.get("QT_BREAKER_WIN", "45.0"))
PCT_FLOOR = 0.25          # owner order 2026-09-19: 0.25 is the floor (was 0.05)
EXPO_MAX = 0.75           # §8：Σ(pct × mcp) ≤ 0.75
TRIP_COOLDOWN_S = 6 * 3600
LOCK_HOURS = float(os.environ.get("QT_BREAKER_LOCK_H", "24"))   # 互锁时长，每次触发续期


def load_env(path):
    """读 env 文件成 dict。**不打印值** —— 和优化器同一约定。"""
    out = {}
    try:
        with open(path, encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                k, v = line.split("=", 1)
                out[k.strip()] = v.strip().strip('"').strip("'")
    except OSError:
        pass
    return out


BENV = load_env(BACKEND_ENV)
os.environ["_TG_TOKEN"] = BENV.get("TELEGRAM_BOT_TOKEN", "")
os.environ["_TG_CHAT"] = BENV.get("TELEGRAM_CHAT_ID", "")


def tg(msg):
    """推 Telegram。缺 token/chat_id 就静默跳过 —— 推送失败不能影响熔断主流程。
    QT_BREAKER_NO_TG=1 供干跑自测：否则一次 dry-run 会往 TG 推一条假的「熔断触发」。"""
    if os.environ.get("QT_BREAKER_NO_TG"):
        return
    tok, chat = os.environ.get("_TG_TOKEN"), os.environ.get("_TG_CHAT")
    if not tok or not chat:
        return
    try:
        req = urllib.request.Request(
            "https://api.telegram.org/bot%s/sendMessage" % tok,
            data=json.dumps({"chat_id": chat, "text": msg[:3900],
                             "disable_web_page_preview": True}).encode(),
            headers={"Content-Type": "application/json"})
        urllib.request.urlopen(req, timeout=20).read()
    except Exception:                           # noqa: BLE001 - 尽力而为
        pass


def http_json(url, method="GET", body=None, token=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=30) as r:
        txt = r.read().decode()
    return json.loads(txt) if txt else {}


def load_state():
    try:
        with open(STATE, encoding="utf-8") as f:
            return json.load(f)
    except (OSError, ValueError):
        return {}


def save_state(d):
    tmp = STATE + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(d, f, ensure_ascii=False, indent=2)
    os.replace(tmp, STATE)


def metrics(db):
    """窗口内权威账本指标。窗口用 SQL 侧 UTC_TIMESTAMP() 算，不碰 Python 时区
    （实测踩过：MySQL 是 UTC、Python 是 EST，dt.timestamp() 会整体偏 5 小时）。"""
    cur = db.cursor()
    cur.execute("""
        SELECT COUNT(*) n,
               COALESCE(SUM(realized_pn_l),0) gross,
               COALESCE(SUM(commission),0) fee,
               COALESCE(SUM(realized_pn_l),0)-COALESCE(SUM(commission),0) net,
               COALESCE(SUM(realized_pn_l > 0),0) wins,
               COALESCE(SUM(realized_pn_l < 0),0) losses
        FROM exchange_fills
        WHERE trade_time >= UTC_TIMESTAMP() - INTERVAL %s HOUR
    """, (WINDOW_HOURS,))
    n, gross, fee, net, wins, losses = cur.fetchone()
    # SUM(expr) 在 MySQL 侧回的是 Decimal，必须先转 int —— 先除后转会 TypeError。
    wins, losses = int(wins or 0), int(losses or 0)
    closed = wins + losses
    return {"fills": int(n or 0), "gross": float(gross or 0), "fee": float(fee or 0),
            "net": float(net or 0), "wins": wins, "losses": losses,
            "closed": closed,
            "win_rate": (100.0 * wins / closed) if closed else None}


def build_lock(cfg, cur_pct, new_pct, reasons):
    """把熔断结果写成 _exp 互锁 —— 优化器 FORBIDDEN_EXACT 里有 _exp（它改不动它），
    而 experiment_hold() 按 _exp.changed + frozen_keys 冻结键。两者一叠加，熔断器
    降下去的 order_amount_pct 就成了优化器**解不开的下限**。
    这是唯一不必改优化器代码就能建立的互锁。

    三个必须踩准的点：
      1. eval_after 必须在**将来**。experiment_hold() 先查 eval_after，过期就
         `return set()`，**根本不看 frozen_keys** —— 那是它「到点自动解冻」的实现方式。
         设成过去 = 锁根本没上。
      2. changed 只能含 order_amount_pct。旧 exit-bracket 实验把
         order_amount_pct / stop_loss_pct / take_profit_pct 一起写进了 changed，
         而 changed 的键**全部**会被冻结 —— 照搬就会连出场几何一起锁死，而那恰恰是
         最需要被修的地方（BOUNDS 不含那三个键，优化器本来也改不动，锁上只是把
         能动的路也堵死）。所以旧记录降级成 prior_exp 留档，不进 changed。
      3. prior_exp 是安全的：experiment_hold() 只读顶层的 changed / frozen_keys，不递归。
    """
    now = time.time()
    old = cfg.get("_exp")
    lock = {
        "id": "lock-%d-breaker" % int(now),
        "status": "open-breaker-lock",
        "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(now)),
        "eval_after": time.strftime("%Y-%m-%dT%H:%M:%SZ",
                                    time.gmtime(now + LOCK_HOURS * 3600)),
        "changed": {"order_amount_pct": "%s→%s（熔断器单向降敞口）" % (cur_pct, new_pct)},
        "frozen_keys": ["order_amount_pct"],
        "why": ("熔断命中：%s。锁 order_amount_pct 防优化器把敞口抬回去；"
                "每次触发续期 %gh。" % ("；".join(reasons), LOCK_HOURS)),
        "mechanism": ("写入 ops/qt_breaker.py；执行侧 ops/deepseek_optimize.py "
                      "experiment_hold()。锁自行到期，不需人工清理。"),
    }
    if isinstance(old, dict):
        lock["prior_exp"] = old
    return lock


def main():
    env = load_env(BACKEND_ENV)

    db = pymysql.connect(host="127.0.0.1", port=3306,
                         user=env.get("DB_USER"), password=env.get("DB_PASS"),
                         database=DB_NAME, charset="utf8mb4")
    # 直接从库里读现值：少一个未知端点，而且 PATCH 落的就是这份 config，读它最不出意外。
    cur = db.cursor()
    cur.execute("SELECT config FROM strategy_instances WHERE id=%s", (STRATEGY_ID,))
    row = cur.fetchone()
    if not row:
        print("[fatal] 库里找不到策略 %s" % STRATEGY_ID, file=sys.stderr)
        return 2
    cfg = json.loads(row[0]) if isinstance(row[0], str) else (row[0] or {})

    m = metrics(db)
    print("[info] window=%dh fills=%d closed=%d win_rate=%s net=%.3f gross=%.3f fee=%.3f"
          % (WINDOW_HOURS, m["fills"], m["closed"],
             ("%.1f%%" % m["win_rate"]) if m["win_rate"] is not None else "n/a",
             m["net"], m["gross"], m["fee"]))

    if m["closed"] < 20:
        print("[skip] 成交样本不足（closed=%d < 20），本轮不判定。" % m["closed"])
        return 0

    reasons = []
    if m["net"] < NET_TRIP:
        reasons.append("净利 %.2fU < 阈值 %.1fU" % (m["net"], NET_TRIP))
    if m["win_rate"] is not None and m["win_rate"] < WIN_TRIP and m["net"] < 0:
        reasons.append("胜率 %.1f%% < %.1f%% 且净利为负" % (m["win_rate"], WIN_TRIP))

    # 注：_exp.rollback_if 还有第三个子条件「0-3 分钟桶仍为负」。它要求逐笔持仓时长，
    # 只能从 strategy_positions（派生视图、有重复行）取。宁缺毋滥，暂未实现，
    # 在这里显式标注而不是假装判过了。
    if not reasons:
        print("[ok] 未触发：净利 %.2fU 胜率 %s"
              % (m["net"], ("%.1f%%" % m["win_rate"]) if m["win_rate"] is not None else "n/a"))
        return 0

    st = load_state()
    since = time.time() - float(st.get("last_trip_ts") or 0)
    if since < TRIP_COOLDOWN_S:
        print("[hold] 已触发但处于冷却（%.1fh 前触发过）" % (since / 3600.0))
        return 0

    cur_pct = float(cfg.get("order_amount_pct", 0) or 0)
    cur_mcp = int(float(cfg.get("max_concurrent_positions", 0) or 0))
    new_pct = max(PCT_FLOOR, round(cur_pct / 2.0, 4))
    new_mcp = cur_mcp
    if new_pct * new_mcp > EXPO_MAX:            # §8 组保证金：只降不升
        new_mcp = max(1, int(EXPO_MAX / new_pct))

    patch = {}
    if new_pct < cur_pct:
        patch["order_amount_pct"] = new_pct
    if new_mcp < cur_mcp:
        patch["max_concurrent_positions"] = new_mcp

    lock = build_lock(cfg, cur_pct, patch.get("order_amount_pct", cur_pct), reasons)

    lines = ["🛑 熔断触发 | %s" % time.strftime("%Y-%m-%d %H:%M UTC", time.gmtime()),
             "窗口 %dh | 成交 %d | 平仓 %d | 胜率 %s"
             % (WINDOW_HOURS, m["fills"], m["closed"],
                ("%.1f%%" % m["win_rate"]) if m["win_rate"] is not None else "n/a"),
             "净利 %.2fU（毛 %.2f − 费 %.2f）" % (m["net"], m["gross"], m["fee"]),
             "", "命中:"] + ["  • " + r for r in reasons] + [""]

    if patch:
        lines.append("动作（单向降敞口，均为即时生效键，不需重启）：")
        for k, v in patch.items():
            lines.append("  • %s: %s → %s" % (k, cfg.get(k), v))
        lines.append("Σ(pct×mcp) = %.3f → %.3f（§8 上限 %.2f）"
                     % (cur_pct * cur_mcp,
                        float(patch.get("order_amount_pct", cur_pct))
                        * float(patch.get("max_concurrent_positions", cur_mcp)),
                        EXPO_MAX))
    else:
        lines.append("⚠️ 已在硬边界地板（pct=%.3f, mcp=%d），无余量可降。" % (cur_pct, cur_mcp))
    lines.append("互锁：_exp 冻结 order_amount_pct 至 %s"
                 "（优化器 FORBIDDEN_EXACT 含 _exp，自己解不开）" % lock["eval_after"])
    lines.append("模式: %s" % ("APPLY(已落配置)" if APPLY else "dry-run(未落配置，加 --apply 才动)"))

    if APPLY:
        tok = (http_json(BACKEND + "/api/login", "POST",
                         {"username": "admin",
                          "password": env.get("ADMIN_PASSWORD", "")}) or {}).get("token")
        if not tok:
            lines.append("落配置失败: backend login failed")
        else:
            # 先动作、再互锁。动作失败也要把锁上上 —— 宁可锁着一个没降成的敞口，
            # 也不能让优化器在还在流血的时候把敞口抬回去。
            for label, body in (("动作", patch), ("互锁", {"_exp": lock})):
                if not body:
                    continue
                try:
                    r = http_json("%s/api/strategies/%s/config" % (BACKEND, STRATEGY_ID),
                                  "PATCH", body, token=tok)
                    lines.append("已落%s: %s" % (label, json.dumps(r, ensure_ascii=False)[:200]))
                    if label == "动作":
                        st["last_patch"] = patch
                except Exception as e:          # noqa: BLE001
                    lines.append("落%s失败: %s" % (label, e))
            st["last_trip_ts"] = time.time()
            st["last_lock_until"] = lock["eval_after"]
            save_state(st)

    out = "\n".join(lines)
    print(out)
    # dry-run 的推送必须自曝身份 —— 否则一条「🛑 熔断触发」发到 TG 会被当成真事。
    tg(("[dry-run 未落配置] " if not APPLY else "") + out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
