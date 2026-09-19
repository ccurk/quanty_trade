#!/usr/bin/env bash
# ============================================================================
# 启动 Majors 实例 e725e31a（owner 2026-09-19 直令「先启动 e725e31a 观察」）
#
# 为什么由你手动跑：对策略执行 start 被自动分类器拦死（我这边），
# 按纪律不改换形式重试 —— 交给你亲手执行。
#
# 这个实例是「关掉 hunger 通道」的现成反事实臂：
#   hunger_mode_enabled = False   ← 唯一停用软件市价平仓的实例
#   max_hold_minutes    = 720     ← 12 小时
# 主池 8eb182b6 是 True / 45 分钟。两者对照即可回答「关 hunger 是否变好」。
#
# —— 开跑前你必须知道的风险（我只说一次）——
#   take_profit_pct = 0 且 stop_loss_pct = 0，配合 hunger_mode_enabled=False
#   ⇒ 软件侧两条止盈止损【都是 0】，唯一在岗的保护是交易所那条
#      use_exchange_tpsl=True / atr_sl_mult=2 的 algo 腿，而持仓上限是 12 小时。
#   敞口：可用 × order_amount_pct 0.125 × leverage 10 = 195U 名义/笔
#         5 槽 ⇒ 975U 总名义 / 156U 权益 = 【6.25× 账户杠杆】
#   若交易所腿在 2×ATR≈0.5% 价格：单笔止损 ≈1.0U、5 笔 ≈4.9U（权益 3%）
#   若在 AKE 实测的 −3.0% 价格：  单笔止损 ≈5.9U、5 笔 ≈29U（权益 19%）
#   ⇒ 先跑 dry-run 看一眼，确认上面的数字你接受，再加 --apply。
#
# 用法：
#   bash qt_start_majors.sh            # 只看当前状态 + 打印将要发的请求（不写）
#   bash qt_start_majors.sh --apply    # 真启动
#
# 停止它（观察够了想收）：
#   curl -sS -X POST http://127.0.0.1:8080/api/strategies/e725e31a-0a10-4393-9ae7-1f4a82ca31ce/stop \
#        -H "Authorization: Bearer <ADMIN_PASSWORD>"
# ============================================================================
set -euo pipefail

SID="e725e31a-0a10-4393-9ae7-1f4a82ca31ce"
BASE="${BASE:-http://127.0.0.1:8080/api}"
APPLY=0
[[ "${1:-}" == "--apply" ]] && APPLY=1

read -r -s -p "ADMIN_PASSWORD: " ADMIN_PASSWORD; echo
AUTH=(-H "Authorization: Bearer ${ADMIN_PASSWORD}" -H "Content-Type: application/json")

show() {
  curl -sS "${BASE}/strategies" "${AUTH[@]}" | python3 -c "
import json,sys
d=json.load(sys.stdin)
items = d if isinstance(d,list) else (d.get('data') or d.get('items') or [])
for it in items:
    i=str(it.get('id',''))
    if i.startswith('e725e31a') or i.startswith('8eb182b6'):
        print('   %s  %-28s status=%s' % (i[:8], it.get('name'), it.get('status')))
"
}

echo "== 启动前 =="
show

echo
echo "== 将要发出的请求 =="
echo "   POST ${BASE}/strategies/${SID}/start"

if [[ "$APPLY" != "1" ]]; then
  echo
  echo "== DRY-RUN（未启动）。确认上面【风险】那段你接受后，加 --apply 重跑。=="
  exit 0
fi

echo
echo "== 启动 =="
code=$(curl -sS -o /tmp/_start_out.json -w '%{http_code}' -X POST \
        "${BASE}/strategies/${SID}/start" "${AUTH[@]}")
echo "   HTTP ${code}"
head -c 400 /tmp/_start_out.json; echo
echo
echo "== 启动后 =="
sleep 3
show
echo
echo "  追踪：确认出现开仓后，用同目录 qt_patch_exit_channel.sh 里那套口径对比"
echo "        主池 8eb182b6（hunger 开/45min） vs 本实例（hunger 关/720min）。"
