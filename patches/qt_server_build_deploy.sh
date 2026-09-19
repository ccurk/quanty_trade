#!/usr/bin/env bash
# =============================================================================
# 服务器本地构建 + 推送 + 部署
# owner 2026-09-19 直令：
#   「你是在本地编译么？？不要这样，在本地改完代码推送到 git。然后去服务器把最新的
#     代码拉下来，在服务器构建，启动。」
#
# 链路（已按直令改）：
#   Mac: git add → git commit（点名路径）→ git push
#   服务器: git pull --ff-only → docker build（原生 x86_64）→ docker push → 部署
#
# 【为什么还要 push】server_deploy_backend.sh 第 161 行是 `docker pull`，
#   对"只存在于本地"的 tag 会失败。推回 Docker Hub 就能保持部署脚本链路不变。
#   服务器已登录 Docker Hub（/root/.docker/config.json 有 index.docker.io auths）。
#
# 【前置已完成，本脚本不重复做】
#   · 守卫 scripts/guard-conf-secrets.sh 退出码 0（conf/ 里活密钥已抄进 backend.env）
#   · conf/ 已备份 /root/qt_ops/conf_backup_20260919-154521.tar.gz (600)
#   · git pull --ff-only 已完成，HEAD=e07f3d7，工作树干净，conf/ 未被本次 pull 触碰
#
# 【爆炸半径】重建 quanty-backend 会重启容器 ⇒ Meme / Majors 两策略实例与前端一起重启。
#   在仓的交易所 algo 条件单挂在交易所侧，容器重启不会撤掉。容器起来后
#   RestoreRunningStrategies 会在约 5 秒后自动拉回 running 实例，无需人工 start。
#
# 回滚：BACKEND_VERSION=20260916103742-7f14374-31c3-backend bash server_deploy_backend.sh
#       若旧 tag 已被 Hub 清掉：docker load < /root/qt_ops/rollback_backend_20260916103742.tar.gz
# =============================================================================
set -euo pipefail
cd /root/work/quanty_trade

HUB="zhaoxianxinclimber108"
IMG="$HUB/quanty_trade-backend"
OLD_TAG="20260916103742-7f14374-31c3-backend"
TAG="$(date -u +%Y%m%d%H%M%S)-$(git rev-parse --short=7 HEAD)-$(openssl rand -hex 3)-backend"
LOG="/root/qt_ops/build_${TAG}.log"
mkdir -p /root/qt_ops

echo "=========================================================="
echo " HEAD  = $(git rev-parse --short HEAD)  $(git log -1 --format=%s | cut -c1-40)"
echo " TAG   = $TAG"
echo " 日志  = $LOG"
echo "=========================================================="

echo
echo "▶ 0) 工作树应干净（不干净则构建源含未提交内容，先停下来）"
if [ -n "$(git status --porcelain)" ]; then
  echo "  ⚠️ 工作树不干净："; git status --porcelain | head -10
  read -r -p "  仍要继续？输入 yes: " k; [ "$k" = yes ] || exit 1
else
  echo "  ✅ 干净"
fi

echo
echo "▶ 1) 构建（服务器原生 x86_64，不需要 buildx）"
docker build -f backend/Dockerfile -t "$IMG:$TAG" . 2>&1 | tee "$LOG" | tail -25
docker images --format '{{.Repository}}:{{.Tag}}  {{.Size}}  {{.CreatedSince}}' | grep -F "$TAG" \
  || { echo "  ❌ 镜像未产出，构建失败，见 $LOG"; exit 1; }
echo "  ✅ 镜像已产出"

echo
echo "▶ 2) 推送（部署脚本靠 docker pull 取像）"
docker push "$IMG:$TAG" 2>&1 | tail -5

echo
echo "▶ 3) 部署前检"
BACKEND_VERSION="$TAG" bash server_deploy_backend.sh --check

echo
echo "▶ 4) 回滚点确认"
docker images --format '{{.Repository}}:{{.Tag}}' | grep -F "$OLD_TAG" \
  || echo "  ⚠️ 本地无旧 tag，回滚只能靠 /root/qt_ops/rollback_backend_${OLD_TAG%%-*}.tar.gz"
ls -la /root/qt_ops/rollback_backend_*.tar.gz 2>/dev/null || true

echo
echo "▶ 5) 部署（重建 quanty-backend）"
BACKEND_VERSION="$TAG" bash server_deploy_backend.sh
echo "$TAG" > /root/qt_ops/deployed_backend_tag

echo
echo "▶ 6) 等 25 秒看容器与自恢复"
sleep 25
docker ps --filter "name=quanty-backend" --format "{{.Names}}  {{.Image}}  {{.Status}}"
docker logs --tail 60 quanty-backend 2>&1 \
  | grep -E "BOOT RESTORE|开机自恢复|listen|panic|error" | tail -12 || true

echo
echo "=========================================================="
echo " ✅ 完成。TAG=$TAG（已写入 /root/qt_ops/deployed_backend_tag）"
echo "=========================================================="
