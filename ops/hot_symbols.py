#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
热点币名单 → Redis (owner 直令 2026-09-19: "先看最近盈利的仓位，再看近3h盈利的，然后再通用")

口径:
  T1 = 最近 N 笔已平仓里【盈利】的那些 symbol   → owner 说的"最近盈利的仓位"
  T2 = 近 3 小时内【盈利】平仓的 symbol          → owner 说的"近3h盈利的"

写:  SET qt:hot:{strategy_id}  {"t1":[...],"t2":[...],"at":<epoch>}  EX 900
读方: strategy_templates id=998 的 _refresh_hot() / _hot_weight()  (择优加权)

⛔ 本脚本【只读 DB + 写 Redis】，不下任何单、不碰配置、不改交易状态。
   安全边界: 即使名单算错，最坏后果只是择优权重偏了一位，不会凭空开仓。

⚠️ 不依赖 redis-py —— 实测服务器系统 python3 的 pip list 只有 PyMySQL。
   这里用内置 socket 直接发 RESP（本脚本只需要 AUTH / SET 两条命令）。

用法: python3 hot_symbols.py [--dry-run]
"""
import json
import socket
import sys
import time

import pymysql


def load_env(path="/etc/quanty/backend.env"):
    env = {}
    with open(path) as fh:
        for line in fh:
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, v = line.split("=", 1)
                env[k.strip()] = v.strip().strip('"').strip("'")
    return env


class R:
    """最小 RESP 客户端: 只实现 AUTH / SET（本脚本全部所需）。"""

    def __init__(self, host="127.0.0.1", port=6379, password=None, timeout=5):
        self.s = socket.create_connection((host, port), timeout=timeout)
        self.f = self.s.makefile("rb")
        if password:
            try:
                self._cmd("AUTH", password)
            except RuntimeError as e:
                # 服务端没设密码时会拒绝 AUTH —— 那不是错误，照常继续
                if "no password is set" not in str(e):
                    raise

    def _cmd(self, *args):
        buf = bytearray(b"*%d\r\n" % len(args))
        for a in args:
            b = a if isinstance(a, bytes) else str(a).encode()
            buf += b"$%d\r\n" % len(b) + b + b"\r\n"
        self.s.sendall(bytes(buf))
        line = self.f.readline()
        if not line:
            raise RuntimeError("redis closed connection")
        t, rest = line[:1], line[1:].rstrip(b"\r\n")
        if t == b"+":
            return rest
        if t == b"-":
            raise RuntimeError(rest.decode("utf-8", "replace"))
        if t == b":":
            return int(rest)
        if t == b"$":
            n = int(rest)
            return None if n < 0 else self.f.read(n + 2)[:-2]
        raise RuntimeError("unexpected RESP type %r" % t)

    def set_ex(self, key, val, ex):
        return self._cmd("SET", key, val, "EX", ex)

    def close(self):
        try:
            self.f.close()
            self.s.close()
        except Exception:
            pass


RECENT_N = 6      # "最近盈利的仓位" 取最近 N 笔已平仓
T2_HOURS = 3      # owner 指定的 3 小时窗口
TTL_SEC = 900


def hot_for(cursor, strategy_id):
    """返回 (t1, t2) 两个 symbol 列表。"""
    # T1: 最近 N 笔已平仓中的盈利者
    cursor.execute(
        """SELECT symbol, realized_pn_l
             FROM strategy_positions
            WHERE strategy_id = %s AND status <> 'open' AND closed_qty > 0
              AND realized_pn_l IS NOT NULL
            ORDER BY close_time DESC
            LIMIT %s""",
        (strategy_id, RECENT_N),
    )
    t1 = []
    for r in cursor.fetchall():
        if float(r["realized_pn_l"]) > 0 and r["symbol"] not in t1:
            t1.append(r["symbol"])

    # T2: 近 T2_HOURS 小时内的盈利平仓
    cursor.execute(
        """SELECT DISTINCT symbol
             FROM strategy_positions
            WHERE strategy_id = %s AND status <> 'open' AND closed_qty > 0
              AND realized_pn_l > 0
              AND close_time >= NOW() - INTERVAL %s HOUR""",
        (strategy_id, T2_HOURS),
    )
    t2 = sorted({r["symbol"] for r in cursor.fetchall()})
    return t1, t2


def main():
    dry = "--dry-run" in sys.argv
    env = load_env()
    cn = pymysql.connect(
        host="127.0.0.1", user=env["DB_USER"], password=env["DB_PASS"],
        database="quanty_trade", charset="utf8mb4",
        cursorclass=pymysql.cursors.DictCursor,
    )
    c = cn.cursor()

    c.execute("SELECT id, name, status FROM strategy_instances WHERE status = 'running'")
    insts = c.fetchall()
    if not insts:
        print("没有 running 实例，退出")
        return

    r = None
    if not dry:
        r = R(password=env.get("REDIS_PASSWORD") or None)

    now = int(time.time())
    for inst in insts:
        t1, t2 = hot_for(c, inst["id"])
        payload = json.dumps({"t1": t1, "t2": t2, "at": now}, ensure_ascii=False)
        key = "qt:hot:%s" % inst["id"]
        if dry:
            print("[dry-run] %s (%s)\n    T1(最近盈利) = %s\n    T2(近3h盈利) = %s" %
                  (inst["name"], inst["id"][:8], t1 or "(空)", t2 or "(空)"))
        else:
            r.set_ex(key, payload, TTL_SEC)
            print("%s → %s  T1=%d T2=%d  %s" %
                  (inst["name"], key, len(t1), len(t2), payload))
    if r:
        r.close()
    cn.close()


if __name__ == "__main__":
    main()
