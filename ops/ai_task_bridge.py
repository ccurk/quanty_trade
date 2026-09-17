#!/usr/bin/env python3
"""ai_task_bridge.py — 宿主机 /tmp/ai_task ↔ 策略 config 留言板键 的双向桥接。

安装位置：DeepSeek 侧（宿主机，与 ops/deepseek_optimize.py 同机）。owner 09-17 直令：
"在服务上我创建了一个文件 你们通过这个交流 /tmp/ai_task 只保留最近几天就好"。
Claude cron 跑在云端容器，没有宿主文件系统，只能经 config 键读写，所以由本脚本把两边接起来。

协议
  文件 /tmp/ai_task：条目以 "### <UTC ISO 时间> | <from>" 起头，正文到下一个 "### " 之前；
                      只保留最近 KEEP_DAYS 天；owner 直接 cat 就能看两边的对话。
  config._ai_task_cc：Claude → DeepSeek（Claude 写；本脚本只读，同步进文件）。
  config._ai_task_ds：DeepSeek → Claude（本脚本写；同时写进文件）。
  两个键各是数组 [{"ts","from","state","msg"}]，写时裁到 KEEP_DAYS 天且 ≤ MAX_BYTES。

用法
  环境变量：QT_BACKEND（默认 https://quanty.qxyz.xyz）QT_USER / QT_PASS（admin 账号）
            QT_STRATEGY_ID（默认 main）AI_TASK_FILE（默认 /tmp/ai_task）KEEP_DAYS（默认 3）
  python3 ai_task_bridge.py sync                     # 双向同步（DeepSeek 脚本每次跑前跑后各调一次，或独立 cron 每 10 分钟）
  python3 ai_task_bridge.py post "本轮 cd 1200→1800，理由…" [state]   # DeepSeek 留言：写文件 + config._ai_task_ds
  python3 ai_task_bridge.py show                     # 打印文件内容（最近 KEEP_DAYS 天）

只用标准库；任何一步失败只打印错误、不抛出，不影响 DeepSeek 主流程。
"""
import json
import os
import sys
import urllib.request
from datetime import datetime, timedelta, timezone

BACKEND = os.environ.get("QT_BACKEND", "https://quanty.qxyz.xyz").rstrip("/")
USER = os.environ.get("QT_USER", "")
PASS = os.environ.get("QT_PASS", "")
STRATEGY_ID = os.environ.get("QT_STRATEGY_ID", "8eb182b6-ee74-4125-a602-f0a91f376432")
FILE = os.environ.get("AI_TASK_FILE", "/tmp/ai_task")
KEEP_DAYS = int(os.environ.get("KEEP_DAYS", "3"))
MAX_BYTES = 8 * 1024
KEY_CC = "_ai_task_cc"   # Claude -> DeepSeek
KEY_DS = "_ai_task_ds"   # DeepSeek -> Claude
HDR = "### "


def now_iso():
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def parse_ts(s):
    try:
        return datetime.strptime(s.strip(), "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
    except Exception:
        return None


def _req(method, path, body=None, token=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BACKEND + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read().decode() or "null")


def login():
    if not USER or not PASS:
        raise RuntimeError("QT_USER / QT_PASS 未设置")
    return _req("POST", "/api/login", {"username": USER, "password": PASS})["token"]


def get_config(token):
    for s in _req("GET", "/api/strategies", token=token):
        if s.get("id") == STRATEGY_ID:
            return s.get("config") or {}
    raise RuntimeError("strategy not found: " + STRATEGY_ID)


def patch_config(token, patch):
    return _req("PATCH", "/api/strategies/%s/config" % STRATEGY_ID, patch, token=token)


# ---------- 文件侧 ----------
def read_entries():
    """文件 -> [{ts, from, msg}]；没有可解析头的段落原样保留（ts=None，永不被裁）。"""
    if not os.path.exists(FILE):
        return []
    entries, cur = [], None
    with open(FILE, encoding="utf-8", errors="replace") as f:
        for line in f:
            if line.startswith(HDR) and "|" in line:
                ts, _, frm = line[len(HDR):].partition("|")
                cur = {"ts": ts.strip(), "from": frm.strip(), "msg": ""}
                entries.append(cur)
            elif cur is not None:
                cur["msg"] += line
            else:
                cur = {"ts": None, "from": "", "msg": line}
                entries.append(cur)
    for e in entries:
        e["msg"] = e["msg"].rstrip("\n")
    return entries


def trim(entries):
    cutoff = datetime.now(timezone.utc) - timedelta(days=KEEP_DAYS)
    keep = []
    for e in entries:
        t = parse_ts(e.get("ts") or "")
        if t is None or t >= cutoff:
            keep.append(e)
    return keep


def write_entries(entries):
    tmp = FILE + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        for e in entries:
            if e.get("ts"):
                f.write("%s%s | %s\n" % (HDR, e["ts"], e["from"]))
            f.write((e.get("msg") or "") + "\n\n")
    os.replace(tmp, FILE)


def merge_into_file(new_items, frm):
    entries = read_entries()
    seen = {(e.get("ts"), e.get("from")) for e in entries}
    for it in new_items:
        key = (it.get("ts"), frm)
        if key in seen or not it.get("ts"):
            continue
        state = it.get("state")
        msg = ("[%s] " % state if state else "") + (it.get("msg") or "")
        entries.append({"ts": it["ts"], "from": frm, "msg": msg})
    entries.sort(key=lambda e: e.get("ts") or "")
    write_entries(trim(entries))


# ---------- config 侧 ----------
def cap(items):
    items = [i for i in items if parse_ts(i.get("ts") or "") and
             parse_ts(i["ts"]) >= datetime.now(timezone.utc) - timedelta(days=KEEP_DAYS)]
    items.sort(key=lambda i: i["ts"])
    while items and len(json.dumps(items, ensure_ascii=False).encode()) > MAX_BYTES:
        items.pop(0)
    return items


def sync():
    token = login()
    cfg = get_config(token)
    cc = cfg.get(KEY_CC) or []
    ds = cfg.get(KEY_DS) or []
    merge_into_file(cc, "claude_cron")
    merge_into_file(ds, "deepseek")
    # 文件里 DeepSeek 手写/脚本写的条目补进 config._ai_task_ds（让 Claude 能读到）
    have = {(i.get("ts"), i.get("from", "deepseek")) for i in ds}
    added = 0
    for e in read_entries():
        if e.get("from") == "deepseek" and (e.get("ts"), "deepseek") not in have:
            ds.append({"ts": e["ts"], "from": "deepseek", "state": "", "msg": e["msg"]})
            added += 1
    new_ds = cap(ds)
    if added or new_ds != ds:
        patch_config(token, {KEY_DS: new_ds})
    print("sync ok: cc=%d ds=%d file=%s" % (len(cc), len(new_ds), FILE))


def post(msg, state=""):
    ts = now_iso()
    item = {"ts": ts, "from": "deepseek", "state": state, "msg": msg}
    merge_into_file([item], "deepseek")
    token = login()
    cfg = get_config(token)
    ds = cfg.get(KEY_DS) or []
    ds.append(item)
    patch_config(token, {KEY_DS: cap(ds)})
    print("posted", ts)


def show():
    for e in trim(read_entries()):
        print("%s%s | %s\n%s\n" % (HDR, e.get("ts") or "-", e.get("from") or "-", e.get("msg") or ""))


if __name__ == "__main__":
    try:
        cmd = sys.argv[1] if len(sys.argv) > 1 else "sync"
        if cmd == "sync":
            sync()
        elif cmd == "post":
            post(sys.argv[2], sys.argv[3] if len(sys.argv) > 3 else "")
        elif cmd == "show":
            show()
        else:
            print("usage: ai_task_bridge.py sync|post <msg> [state]|show")
    except Exception as exc:  # 桥接失败不许拖垮 DeepSeek 主流程
        print("ai_task_bridge error:", exc, file=sys.stderr)
