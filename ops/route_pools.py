#!/usr/bin/env python3
# 策略组·动态币池路由对账器 (owner 直令 2026-08-09: 币池不写死,动态增删)
#
# 模式=期望态对账: 每 cron 轮跑一次 → 用逐笔证据算【期望池】→ GET 各载具
# live feed 拿【实际池】→ 输出 rotate 差分计划(add/remove per 载具)。
# 执行权在 cron agent(核对 _exp/持仓后 POST /symbols/rotate)。
# rotate 是内存态,重启后从 config.symbols 种子重建 → 漂移由下轮对账自愈;
# 各载具自然重启窗顺手把当期期望池写回 config.symbols(种子刷新)。
#
# 生命周期: main(auto100=发现漏斗) →晋升→ 专家池 →降级→ main →失血→ 隔离区 →rehab→ main
# 阈值 v1(机械,预注册;改阈值=ROUTE 预注册动作):
#   隔离进: 48h 币净≤-4U 且 n≥4(不论方向/在哪个池) —— 不限流,立即执行
#   隔离出: 7天无交易 且 (vision画像翻转 或 owner令) → rehab 回 main
#   trend进(自main): 48h L净≥+1.5 且 nL≥3 且 wrL≥50%; 或 trending∩池 且 L净>0 且 nL≥2. 池容量8(按L净取top)
#   trend出: 在池期间滚动 nL≥6 且 L净<0 → 回main
#   fade进: 48h S净≥+1.5 且 nS≥3. 容量6 | fade出: 滚动 nS≥6 且 S净<0 → 回main
#   breakout进(载具live后): 48h max|mv|≥3.5% 且未入他池. 容量6
#   lowvol进(载具live后): feed ATR%≤1.3 持续 (待画像通道,暂 manual)
#   撞池优先级: 隔离 > fade/trend(方向净额大者) > breakout > main
#   churn限流: 每轮membership变更≤4(隔离动作不计——止血不限流)
#   出池语义 v1.1 (2026-08-10): remove 只由【出池规则或隔离】触发;失去入池资格=aging_watch
#   advisory(保留在池)。breakout/lowvol 出池阈值未注册(#42)前无自动出口。
#   CLI: route_pools.py <closed48.json> <token_file> [trending.json] [registry_quarantine.json]
#        arg4=台账§1.5隔离区数组(持久注册表,防窗口滚动放虎归山)
import json, sys, os, datetime, subprocess
from collections import defaultdict

BACKEND = os.environ.get("BACKEND", "https://quanty.qxyz.xyz")
TOKEN = os.environ.get("QT_TOKEN") or (open(sys.argv[2]).read().strip() if len(sys.argv) > 2 else "")
ROSTER = {  # id → (原型, live判定由/api/strategies现拉; 此表只定义原型归属)
    "8eb182b6-ee74-4125-a602-f0a91f376432": "main",
    "827ffe8c-64a9-428a-b876-9d28b711d224": "trend",
    "21519f1b-af98-4156-b1f6-9721f3f8f6c4": "fade",
    "2111f5f9-d1fa-48c7-a018-2249483615c4": "breakout",
    "ad37d337-17c7-4f9c-803f-59ede2f18db8": "lowvol",
}
CAP = {"trend": 8, "fade": 6, "breakout": 6, "lowvol": 6}
CHURN_LIMIT = 4

def api(path):  # curl 走容器代理链(CA/HTTPS_PROXY 由环境处理),与 cron 轮其余工具同构
    out = subprocess.run(["curl", "-s", "--max-time", "30", BACKEND + path,
                          "-H", "Authorization: Bearer " + TOKEN],
                         capture_output=True, text=True, timeout=40).stdout
    return json.loads(out)

def norm(s):  # TUT/USDT → TUTUSDT
    return s.replace("/", "").upper()

def main():
    closed_path = sys.argv[1]  # 本轮已拉的 closed48 json(滤幻影后逐笔)
    rows = [r for r in json.load(open(closed_path)) if r.get("realized_pnl") is not None]
    now = datetime.datetime.now(datetime.timezone.utc)
    stat = defaultdict(lambda: {"n": 0, "net": 0.0, "nL": 0, "L": 0.0, "wL": 0,
                                "nS": 0, "S": 0.0, "wS": 0, "maxmv": 0.0, "last_ct": None})
    for r in rows:
        e, x = float(r["price"]), float(r["avg_close_price"])
        mv = (x - e) / e * 100 * (1 if r["direction"] == "long" else -1)
        pnl = float(r["realized_pnl"]); s = stat[norm(r["symbol"])]
        s["n"] += 1; s["net"] += pnl; s["maxmv"] = max(s["maxmv"], abs(mv))
        ct = r["close_time"]; s["last_ct"] = max(s["last_ct"] or ct, ct)
        if r["direction"] == "long": s["nL"] += 1; s["L"] += pnl; s["wL"] += pnl > 0
        else: s["nS"] += 1; s["S"] += pnl; s["wS"] += pnl > 0

    strategies = api("/api/strategies")
    live = {}   # 原型 → live feed set (仅 running 载具)
    running = {}
    for st in strategies:
        role = ROSTER.get(st["id"]);  status = st.get("status")
        if not role: continue
        running[role] = (status == "running", st["id"])
        if status == "running":
            try: live[role] = {norm(x) for x in api(f"/api/strategies/{st['id']}/symbols")["symbols"]}
            except Exception: live[role] = None  # 拉不到=本轮跳过该载具

    trending = set()  # cron agent 可注入 CoinGecko trending∩池(第3参 json 数组)
    if len(sys.argv) > 3: trending = {norm(x) for x in json.load(open(sys.argv[3]))}

    quarantine, want = set(), {k: set() for k in CAP}
    for sym, s in stat.items():
        if s["net"] <= -4 and s["n"] >= 4: quarantine.add(sym); continue
        fitT = (s["L"] >= 1.5 and s["nL"] >= 3 and s["wL"] / max(s["nL"], 1) >= 0.5) or \
               (sym in trending and s["L"] > 0 and s["nL"] >= 2)
        fitF = s["S"] >= 1.5 and s["nS"] >= 3
        if fitT and fitF: (want["fade"] if s["S"] > s["L"] else want["trend"]).add(sym)
        elif fitT: want["trend"].add(sym)
        elif fitF: want["fade"].add(sym)
        elif s["maxmv"] >= 3.5: want["breakout"].add(sym)
    for role in want:  # 容量裁剪: 按该原型主方向净额排序
        keyf = {"trend": lambda x: -stat[x]["L"], "fade": lambda x: -stat[x]["S"]}.get(role, lambda x: -stat[x]["maxmv"])
        want[role] = set(sorted(want[role], key=keyf)[:CAP[role]])

    # v1.1 持久隔离注册表(arg4=json数组,取台账§1.5隔离区): 48h窗看不见老隔离币的历史证据
    # (如 龙虾 证据老化后窗内净≈0),会把注册表在押币放回 want——隔离出必须走头注
    # "7天无交易∧画像翻转∨owner令",不是窗口滚动。arg4 并入后从一切 want 剔除。
    if len(sys.argv) > 4:
        quarantine |= {norm(x) for x in json.load(open(sys.argv[4]))}
    for role in want:
        want[role] -= quarantine

    # v1.1 remove 语义对齐头注: 出池只认头注"出池规则"(trend: nL≥6∧L净<0; fade: nS≥6∧S净<0)
    # 或隔离命中。失去入池资格(典型=赢单老化出窗)≠出池——此类币保留在池并列入 aging_watch
    # advisory(08-10 实证: ACE窗内0笔/BICO 2胜+0.96/ARC nS1 被旧版按入池差集误列移除,
    # 会把无病币打成孤儿态=可交易宇宙单向棘轮收缩)。breakout/lowvol 头注无出池规则→
    # 永不自动移除,其失格币全走 aging_watch,阈值注册候 ROUTE _exp(#42)。头注阈值零改动。
    def out_rule(role, s):
        st = stat.get(s)
        if st is None: return False  # 窗内零交易=无出池证据(挂 aging_watch)
        if role == "trend": return st["nL"] >= 6 and st["L"] < 0
        if role == "fade":  return st["nS"] >= 6 and st["S"] < 0
        return False  # breakout/lowvol: 出池阈值未注册(#42)
    plan, churn, aging_watch = {}, 0, {}
    for role in ["trend", "fade", "breakout", "lowvol"]:
        is_run, sid = running.get(role, (False, None))
        if not is_run or live.get(role) is None: continue  # gated/停机载具不路由(启动窗才灌池)
        cur = live[role]
        add = [s for s in want[role] - cur if s not in quarantine]
        stale = sorted(cur - want[role])  # 在池但已不满足入池条的币
        rem = [s for s in stale if s in quarantine or out_rule(role, s)]  # 隔离(降级回main由auto漏斗接住)或出池规则命中
        aw = [s for s in stale if s not in rem]
        if aw: aging_watch[role] = aw
        add2, rem2 = [], []
        for s in rem:
            if s in quarantine or churn < CHURN_LIMIT:
                rem2.append(s); churn += s not in quarantine
        for s in add:
            if churn < CHURN_LIMIT: add2.append(s); churn += 1
        if add2 or rem2: plan[role] = {"strategy_id": sid, "add": sorted(add2), "remove": sorted(rem2)}
    # main: feed 期望 = auto选集 − (各running专家live池 ∪ 隔离区) → 只输出应移除项
    if live.get("main"):
        occupied = set().union(*[live[r] for r in live if r != "main" and live[r]], quarantine)
        rm = sorted(live["main"] & occupied)
        if rm: plan["main"] = {"strategy_id": running["main"][1], "add": [], "remove": rm}
    # blacklist_sync: main config.symbol_blacklist 的期望值(种子层,重启窗批量 PATCH 同步;
    # 日常互斥靠 live feed rotate + 引擎互斥闸,此处只维护重启后的静态兜底)
    post = {}
    for role in ("trend", "fade", "breakout", "lowvol"):
        if live.get(role):
            p = plan.get(role, {})
            post[role] = (live[role] - set(p.get("remove", []))) | set(p.get("add", []))
    desired_bl = sorted(quarantine | set().union(*post.values()) if post else quarantine)
    print(json.dumps({"generated_at": now.isoformat(), "quarantine": sorted(quarantine),
                      "want": {k: sorted(v) for k, v in want.items()}, "plan": plan,
                      "aging_watch": aging_watch,
                      "blacklist_sync": {"desired_main_blacklist": desired_bl,
                                         "note": "重启窗才 PATCH;离池未隔离币=孤儿态(安全不交易)等此同步释放回main"},
                      "note": "执行=cron agent核对_exp与持仓后逐载具POST /symbols/rotate;remove被has_open_position拒→下轮重试;隔离新增须同步登台账§1.5"},
                     ensure_ascii=False, indent=1))

if __name__ == "__main__":
    main()
