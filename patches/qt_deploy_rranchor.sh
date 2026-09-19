#!/bin/bash
# =============================================================================
# 把「TP/SL 锚到成交价」上线（owner 2026-09-19 直令：「改，把 TP/SL 锚到成交价」）
#
# 为什么需要你亲自跑：构建那一步（docker buildx build --push）被 Claude Code 的
# 权限分类器判为 [CI Bypass] 拒了。按纪律我不换形式重试，把剩余动作原样落成本脚本。
#
# 现状（2026-09-19 10:25Z 实测）：
#   · 代码改动已完成：v40 模板 4 个 Go 文件 + crypto_perp_engine_v40.py
#   · v40 模板【已写入线上 DB】（模板 1057，md5 4f55ca36…，回读校验通过）
#     —— 这是潜伏写入：旧 Go 不认识新增的 price 字段（encoding/json 静默忽略）
#        ⇒ 在镜像上线前，行为与改动前完全一致，安全。
#   · backend 镜像【尚未重建】 ← 本脚本第 1 步
#
# 爆炸半径：重建 backend 会连带重启 Meme / Majors 两个策略实例和前端（同一容器）。
#   在仓（部署前实测 4 个）的交易所 algo 条件单挂在交易所侧，容器重启不会撤掉它们。
#   容器重建后 backend 的 RestoreRunningStrategies 会在 5 秒后自动拉起期望状态为
#   running 的策略 ⇒ 无需人工 start。
#
# 【2026-09-19 15:5xZ 追加】本次构建额外带上 owner 直令「不需要冷却」的代码层改动：
#   strategy_signal.go —— 排序从 penalty=1+近期*0.35+连开*0.5 改为「上一笔平仓盈利
#   的 symbol 优先(rr*1.25)」，并删掉三条本身就是冷却的平局规则。
#   ⚠️ 配置层(symbol_reentry_cooldown_minutes 3/15 → 0)已单独生效，不在本脚本里。
#   ⚠️ 本镜像【不含】止损腿 LIMIT 降级(binance.go:2745-2748)的修复 —— 仍未修。
#
# 用法：
#   bash patches/qt_deploy_rranchor.sh build     # 只在构建机跑：构建+推镜像
#   bash patches/qt_deploy_rranchor.sh deploy    # 只在服务器跑：拉取+重建容器
#   bash patches/qt_deploy_rranchor.sh verify    # 只在服务器跑：验收
# =============================================================================
set -euo pipefail

TAG="20260919222512-7cb93e0-6d98d0-backend"
OLD_TAG="20260916103742-7f14374-31c3-backend"
IID="8eb182b6-ee74-4125-a602-f0a91f376432"
HUB="zhaoxianxinclimber108"
NEW_MD5="4f55ca36b31d2ada3a21743bd68725a3"
STEP="${1:-}"

case "$STEP" in
build)
  echo "▶ 构建并推送 $HUB/quanty_trade-backend:$TAG （多架构，约 5~15 分钟）"
  echo "  构建源 = 当前工作树。本次相对线上镜像的 Go 改动固化在："
  echo "  .deploy_patch_${TAG}.diff （md5 f7ee96a0c9f2e33c0f0553c158c416d9，372 行，6 个文件）"
  cd "$(dirname "$0")/.."
  ./deploy.sh backend "$TAG"
  echo "✅ 已推送。接着在服务器上跑： bash patches/qt_deploy_rranchor.sh deploy"
  ;;

deploy)
  cd /root/work/quanty_trade
  echo "▶ 部署前检"
  BACKEND_VERSION="$TAG" bash server_deploy_backend.sh --check
  echo
  echo "▶ 回滚点（部署前确认旧镜像仍在本地）"
  docker images | grep -F "$OLD_TAG" || echo "  ⚠️ 本地没有旧 tag！回滚只能靠 /root/qt_ops/rollback_backend_${OLD_TAG%%-*}.tar.gz"
  ls -la /root/qt_ops/rollback_backend_*.tar.gz 2>/dev/null || true
  echo
  read -r -p "确认部署？会重建 quanty-backend 容器（策略实例会自动恢复）。输入 yes 继续: " ok
  [ "$ok" = "yes" ] || { echo "已取消"; exit 1; }
  BACKEND_VERSION="$TAG" bash server_deploy_backend.sh
  echo
  echo "▶ 等 25 秒看容器与自恢复"
  sleep 25
  docker ps --filter "name=quanty-backend" --format "{{.Names}}  {{.Image}}  {{.Status}}"
  docker logs --tail 40 quanty-backend 2>&1 | grep -E "BOOT RESTORE|开机自恢复|listen|panic" || true
  echo "✅ 部署完成。接着： bash patches/qt_deploy_rranchor.sh verify"
  ;;

verify)
  echo "▶ 1) 镜像是否为预期 tag"
  docker inspect quanty-backend --format '{{.Config.Image}}'
  echo
  echo "▶ 2) 两个策略实例状态 + 模板 md5"
  ssh_self=1
  export ADMIN_PASSWORD="$(grep '^ADMIN_PASSWORD=' /etc/quanty/backend.env | cut -d= -f2-)"
  export DB_USER="$(grep '^DB_USER=' /etc/quanty/backend.env | cut -d= -f2-)"
  export DB_PASS="$(grep '^DB_PASS=' /etc/quanty/backend.env | cut -d= -f2-)"
  python3 - <<'PY'
import os, hashlib, pymysql
cn = pymysql.connect(host="127.0.0.1", user=os.environ["DB_USER"], password=os.environ["DB_PASS"],
                     database="quanty_trade", charset="utf8mb4", cursorclass=pymysql.cursors.DictCursor)
c = cn.cursor()
c.execute("SELECT id,name,status FROM strategy_instances")
for r in c.fetchall():
    print("   实例 %-40s %s" % (r["name"][:40], r["status"]))
c.execute("SELECT code FROM strategy_templates WHERE id=1057")
print("   模板1057 md5 =", hashlib.md5(c.fetchone()["code"].encode()).hexdigest(), "(应为 4f55ca36b31d2ada3a21743bd68725a3)")
c.execute("SELECT symbol,direction,amount,avg_price,take_profit,stop_loss,open_time FROM strategy_positions "
          "WHERE strategy_id=%s AND status='open'", ("8eb182b6-ee74-4125-a602-f0a91f376432",))
rows = c.fetchall()
print("   在仓 %d 笔" % len(rows))
for r in rows:
    e, tp, sl = float(r["avg_price"] or 0), float(r["take_profit"] or 0), float(r["stop_loss"] or 0)
    rr = ""
    if e > 0 and tp > 0 and sl > 0:
        rr = "  R:R=%.3f" % (abs(tp - e) / abs(e - sl))
    print("     %-12s %-5s %s @ %s  tp=%s sl=%s%s" % (r["symbol"], r["direction"], r["amount"], e, tp, sl, rr))
print("   ↑ 新开的仓 R:R 应 == 1.250（重锚生效）；改动前会在 0.44~1.29 之间散开")
cn.close()
PY
  echo
  echo "▶ 3) 关键日志：应出现「止盈止损已锚到成交价 … 滑点=…」"
  docker logs --tail 400 quanty-backend 2>&1 | grep -E "止盈止损已锚到成交价|开仓下单成功" | tail -10 || echo "  (暂无新开仓，等下一笔)"
  ;;

*)
  echo "用法: $0 {build|deploy|verify}"; exit 1;;
esac

# ---------------------------------------------------------------------------
# 回滚（任一步出问题）
#   模板回滚: ssh mycloud 'cp /root/qt_ops/tpl1057_backup_20260919-102555.py <写回 DB>'
#             （或用 /tmp/rollback_v40_history.py 的骨架，把 code 换成该备份文件）
#   镜像回滚: cd /root/work/quanty_trade &&
#             BACKEND_VERSION=20260916103742-7f14374-31c3-backend bash server_deploy_backend.sh
#   若 Docker Hub 上旧 tag 已不在：
#             docker load < /root/qt_ops/rollback_backend_20260916103742.tar.gz
#             然后手工 docker run（参数见 server_deploy_backend.sh 的 docker run 段）
# ---------------------------------------------------------------------------
