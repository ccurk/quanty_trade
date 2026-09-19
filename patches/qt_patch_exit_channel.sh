#!/usr/bin/env bash
# ============================================================================
# 主池「regime 闸门 + 4h 持仓」的落地补丁（2026-09-19）
#
# —— 先说他问的两件事为什么不能按原样落 ——
# ① regime 闸门：配置层做不到。min_atr_pct_for_trade / max_atr_pct / sl_ratio
#    三个键在三个实例的 config 里都躺着，但 Go 里各 0 处调用（唯一变量 key 读取
#    路径是 normalizedTPSLPct，只用于 TP/SL）⇒ 和 bar_agg_minutes 同类死键。
#    ⇒ 必须改代码，见 §3。
# ② hunger_stop_loss_pct / hunger_after_minutes：不许再动。
#    止损宽度 0.6%~6% 全在 1 SE 内（已测 ≥2 轮全负）；after_minutes 45 比 15/30
#    更差（arm45−arm15 = −0.487% 价，t = −3.09）⇒ 现在的 1 已在紧端。
#    本 session 独立复测 12 格（刀宽 0.4/1.0/2.0/3.5% × horizon 1h/4h/8h）
#    全负且彼此差 < 2bp —— 第 5 次确认「别再调止损宽度」。
#
# —— 本补丁改的是【通道】，不是参数 ——
# 近 7 天三个软件出场通道的真实现金流（行内自带 pnl，n=245，不 join）：
#   max_hold_timeout  47 笔  +1.0194 U  均 +0.0217  负笔 51%  均ROI +0.38%
#   hunger           127 笔 −190.7044 U  均 −1.5016  负笔 93%  均ROI −5.77%
#   guard             69 笔  −24.5612 U  均 −0.3560  负笔 51%  均ROI −3.33%
# ⇒ 亏损的 92% 来自 hunger 的 120 笔 sl 臂（−197.37 U）。而「时间到无条件平仓」
#   是唯一正现金流的通道。⇒ 关掉 hunger 这个【软件市价平仓触发器】，
#   让仓位走「交易所 ATR 挂单 + 时间到平仓」。
#
# 第二个独立理由：交易所 algo 挂单的成交质量更好。实测击穿（软止损按 10 秒 tick
# 采样后再发市价单）中位 −0.85pp ROI、尾部 ~1.1% 价格；AKE 单笔设计亏 0.87U /
# 实亏 3.21U = 3.7× 超额。挂单不吃这个。
#
# ⚠️⚠️ 2026-09-19 订正：本节原来的核心假设【已被实测否掉】。
#    原文写的是「交易所 ATR 腿在 AKE 实测 −3.0% 价格 ⇒ 单笔止损 ≈5.9U ⇒ 必须缩仓」。
#    Majors 实例首两笔实测（2026-09-19 08:03/08:1x）：
#      ETH 刀 = 0.0824% 价、BNB 刀 = 0.0845% 价 —— 我错了约 **40×**。
#    ⇒ **`order_amount_pct` 的缩仓理由不成立，已从 WANT 里删除。**
#    真因见 patches/qt_patch_atr_units.sh：引擎跑的是**硬编码 1m K 线**
#    （binance_kline_hub.go:213 `@kline_1m`），而 `bar_agg_minutes=15` 是死键
#    ⇒ 所有 ATR 距离比设计意图窄 **5.60×**（5 币 24h 实测）。
#
# ⚠️ 未做也做不到的验证：hunger 关掉后的表现没有事前回测（回测与实盘不是同一条
#    平仓逻辑）。这是「换层」不是「调参」，风险自担。建议先只对 Sandbox 实例试。
# ============================================================================
set -euo pipefail

SID="${SID:-8eb182b6}"          # 主池；建议先 SID=ce84012b（Sandbox）试
BASE="${BASE:-http://127.0.0.1:8080}"
APPLY=0
[[ "${1:-}" == "--apply" ]] && APPLY=1

declare -A WANT=(
  [hunger_mode_enabled]=false   # True → False  关掉软市价平仓通道（占亏损 92%）
  [max_hold_minutes]=240        # 45 → 240      关掉 hunger 后它才成为真正的出场闸
)
# order_amount_pct 曾列入，**理由已被实测否掉，故不写**（见上）。
# hunger_stop_loss_pct / hunger_after_minutes 一律不动（已测 ≥2 轮全负）

read -r -s -p "ADMIN_PASSWORD: " ADMIN_PASSWORD; echo
AUTH=(-H "Authorization: Bearer ${ADMIN_PASSWORD}" -H "Content-Type: application/json")

api() {
  local m="$1" p="$2" b="${3:-}" out code
  for pre in "" "/api"; do
    if [[ -n "$b" ]]; then
      out=$(curl -sS -X "$m" "${BASE}${pre}${p}" "${AUTH[@]}" -d "$b" -w $'\n%{http_code}' 2>/dev/null) || continue
    else
      out=$(curl -sS -X "$m" "${BASE}${pre}${p}" "${AUTH[@]}" -w $'\n%{http_code}' 2>/dev/null) || continue
    fi
    code="${out##*$'\n'}"
    [[ "$code" == "200" || "$code" == "201" ]] && { echo "${out%$'\n'*}"; return 0; }
    BASE_LAST_CODE="$code"
  done
  return 1
}

echo "== 1. 读当前值（只打印白名单键，绝不整体输出 config）=="
CUR=$(api GET "/strategies/${SID}") || { echo "读取失败（最后 HTTP $BASE_LAST_CODE）—— 检查 SID/BASE/密码"; exit 1; }
BODY=""
for k in "${!WANT[@]}"; do
  v=$(printf '%s' "$CUR" | python3 -c "
import json,sys
try: d=json.loads(sys.stdin.read())
except Exception: print('?'); raise SystemExit
def find(o):
    if isinstance(o,dict):
        if '$k' in o: return o['$k']
        for v in o.values():
            r=find(v)
            if r is not None: return r
    return None
r=find(d); print('未设置' if r is None else r)
" 2>/dev/null || echo '?')
  printf '   %-24s 现在=%-10s → 建议=%s\n' "$k" "$v" "${WANT[$k]}"
  case "${WANT[$k]}" in true|false) BODY="${BODY}\"$k\":${WANT[$k]}," ;; *) BODY="${BODY}\"$k\":${WANT[$k]}," ;; esac
done
BODY="{${BODY%,}}"

echo
echo "== 2. 将发出的 PATCH body =="
echo "   $BODY"

if [[ "$APPLY" != "1" ]]; then
  echo
  echo "== DRY-RUN（未写库）。确认无误后加 --apply 重跑。=="
  exit 0
fi

echo
echo "== 3. 写入 =="
api PATCH "/strategies/${SID}/config" "$BODY" >/dev/null && echo "   ✅ 已写入" || { echo "   ❌ 写入失败 HTTP $BASE_LAST_CODE"; exit 1; }
echo "   hunger_mode_enabled / max_hold_minutes 都是 Go 热键（quick_trade_monitor.go 每 10 秒读一次 inst.Config），实时生效、不用重启容器。"
echo "   ⚠️ 已在仓的仓位会立刻改用新规则判定。"
