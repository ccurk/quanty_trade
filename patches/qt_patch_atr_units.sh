#!/usr/bin/env bash
# ============================================================================
# 【修单位，不是调参数】Majors 观察实例的 ATR 量纲校正（2026-09-19）
#
# —— 病是什么 ——
# 引擎全流程跑的是 **1 分钟 K 线**，硬编码在
#   backend/internal/exchange/binance_kline_hub.go:213/237/255
#     s.enqueueCtrl(wsCtrl{method:"SUBSCRIBE", stream: sym + "@kline_1m"})
# 而 config 里的 `bar_agg_minutes=15`（前端还给了输入框）**Go 0 处调用、
# 模板 998 里也没搜到** ⇒ 死键。**有人按 15 分钟 K 线配的参数，引擎给的是 1 分钟。**
#
# —— 证据 ——
# ① 实盘两笔反解：ETH 刀=0.0824%(atr=0.0412%)、BNB 刀=0.0845%(atr=0.0423%)
#    —— 两笔几乎同宽 ⇒ 不是冷启动 artifact，是稳定态。
# ② 独立实测（最近 24h，5 币，1m vs 15m 各算 ATR14）：
#      BTC 4.78 / ETH 4.63 / BNB 4.91 / SOL 5.92 / XRP 7.75  ⇒ 平均 **5.60×**
# ③ 回测侧的 `atr_sl_mult=2.0` 对应 ≈0.80% 刀宽（≈15m ATR 口径）
#    ⇒ 回测与实盘的刀宽差约 10×。**"止损宽度已测 ≥2 轮全负"那条结论，
#      测的是 0.8% 的刀，对实盘 0.082% 的刀不适用。**
#
# —— 改什么 ——
# 把两个 ATR 乘数同乘 5.6（保持 atr_tp_mult/atr_sl_mult 的原设计比 1.25）：
#     atr_sl_mult   2   → 11     (2  × 5.6 = 11.2 → 11)
#     atr_tp_mult   2.5 → 14     (2.5× 5.6 = 14)
# 效果：刀宽回到**回测研究过的那个几何**，这个臂才第一次测的是它对标的东西。
#   ETH 08:03 那根 ATR(1m)=0.0412% ⇒ 刀 0.45%
#   ETH 24h 均 ATR(1m)=0.0605%   ⇒ 刀 0.67%
#
# —— 明确【不改】的东西 ——
#   ⛔ order_amount_pct 不动。旧补丁里的 0.125→0.025 是基于"AKE 实测 3.0% 刀宽"
#      的估算，**那个估算已被实测否掉**（真实单笔止损 0.14U，我错了约 40×）。
#      按新几何：单笔止损 ≈160U × 0.45% ≈ **0.72U**，5 槽满仓 ≈3.6U ≈ 权益 2.3%
#      ⇒ **不需要缩仓**。缩仓会把这个臂变成"小仓位测宽刀"，又偏离对标。
#   ⛔ hunger_mode_enabled 不动（本实例已是 False，这是本臂的立足点）
#   ⛔ max_hold_minutes 不动（本实例已是 720）
#   ⛔ 主池 8eb182b6 一律不动 —— 它跑的是 0.082% 的窄刀 + hunger，是**另一个**几何，
#      要不要一起改是独立决策，本补丁不碰。
#
# —— 这是 workaround，不是真修 ——
# 真修是让引擎认 `bar_agg_minutes`（Go 订阅改周期，或在信号引擎里把 1m 聚成 15m）。
# 那要动 Go / 共享模板 998，影响全部实例，我不单方面做。
# **本补丁只动 Majors 一个实例、两个键、可秒回滚。**
# ⚠️ 一旦真修落地，这两个键必须**同时还原成 2 / 2.5**，否则刀会宽 5.6× —— 记在这里。
#
# —— 仍未解 ——
# 实测 tp/sl 比 = 1.79，而配置比是 2.5/2 = 1.25。本补丁按**配置比**校正（14/11=1.27），
# 但那个 1.79 还没找到来源（怀疑是模板里 EMA_FAST 偏离度的入场价调整）。**未经证实。**
#
# 用法：
#   bash qt_patch_atr_units.sh            # dry-run，只打印将要写的 body
#   bash qt_patch_atr_units.sh --apply    # 真写
#
# 回滚（改回原值即可）：
#   SID=e725e31a bash qt_patch_atr_units.sh   # 把下面 WANT 里的值改回 2 / 2.5 再 --apply
# ============================================================================
set -euo pipefail

SID="${SID:-e725e31a-0a10-4393-9ae7-1f4a82ca31ce}"   # Majors 观察实例，只此一个
BASE="${BASE:-http://127.0.0.1:8080}"
APPLY=0
[[ "${1:-}" == "--apply" ]] && APPLY=1

declare -A WANT=(
  [atr_sl_mult]=11    # 2   → 11   量纲校正 ×5.6（不是加宽止损）
  [atr_tp_mult]=14    # 2.5 → 14   同比校正，保持原设计比 1.25
)

read -r -s -p "ADMIN_PASSWORD: " ADMIN_PASSWORD; echo
AUTH=(-H "Authorization: Bearer ${ADMIN_PASSWORD}" -H "Content-Type: application/json")

# 注意：没有 GET /strategies/:id 这条路由，只能列出来再按 id 匹配
api() {
  local m="$1" p="$2" b="${3:-}" out code
  for pre in "/api" ""; do
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
CUR=$(api GET "/strategies") || { echo "读取失败（最后 HTTP ${BASE_LAST_CODE:-?}）—— 检查 BASE/密码"; exit 1; }
BODY=""
for k in "${!WANT[@]}"; do
  v=$(printf '%s' "$CUR" | python3 -c "
import json,sys
d=json.loads(sys.stdin.read())
items = d if isinstance(d,list) else (d.get('data') or d.get('items') or [])
sid='${SID}'
for it in items:
    if str(it.get('id',''))==sid:
        cfg=it.get('config') or {}
        if isinstance(cfg,str):
            import json as j; cfg=j.loads(cfg)
        print(cfg.get('$k','未设置')); break
else: print('未找到实例')
" 2>/dev/null || echo '?')
  printf '   %-16s 现在=%-8s → 改为=%s\n' "$k" "$v" "${WANT[$k]}"
  BODY="${BODY}\"$k\":${WANT[$k]},"
done
BODY="{${BODY%,}}"

echo
echo "== 2. 将发出的 PATCH body =="
echo "   $BODY"
echo "   （SID=${SID}）"

if [[ "$APPLY" != "1" ]]; then
  echo
  echo "== DRY-RUN（未写库）。确认后加 --apply 重跑。=="
  exit 0
fi

echo
echo "== 3. 写入 =="
api PATCH "/strategies/${SID}/config" "$BODY" >/dev/null \
  && echo "   ✅ 已写入" || { echo "   ❌ 写入失败 HTTP ${BASE_LAST_CODE:-?}"; exit 1; }

echo "   atr_sl_mult / atr_tp_mult 由 **Python 信号引擎**读取（模板 998 L975-994 的 _calc_tp_sl），"
echo "   引擎重启或下一根 K 线评估时生效；已在仓的仓位不受影响（它的 tp/sl 已挂在交易所）。"
echo
echo "== 4. 验收（下一笔开仓后）=="
echo "   看日志 '已设置止盈止损 symbol=… tp=… sl=…' 里的 sl，距 entry 应落在 0.4%~0.7%，"
echo "   而不是现在的 0.08%。若仍是 0.08%，说明 atr_sl_mult 没被读到，回来找我。"
