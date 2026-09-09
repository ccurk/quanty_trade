#!/usr/bin/env bash
set -euo pipefail

# 凭据一律不写进本脚本（本仓库是公开的）。真实值放在服务器本地的 600 权限 env
# 文件里，默认 /etc/quanty/datastore.env（与 server_deploy_mysql.sh 同一份），
# 可用 QUANTY_DATASTORE_ENV 覆盖路径。已 export 的环境变量优先。
QUANTY_DATASTORE_ENV="${QUANTY_DATASTORE_ENV:-/etc/quanty/datastore.env}"
if [ -f "$QUANTY_DATASTORE_ENV" ]; then
  perm="$(stat -c '%a' "$QUANTY_DATASTORE_ENV" 2>/dev/null || stat -f '%Lp' "$QUANTY_DATASTORE_ENV" 2>/dev/null || echo '')"
  if [ -n "$perm" ] && [ "$perm" != "600" ]; then
    echo "错误: $QUANTY_DATASTORE_ENV 权限是 $perm，必须是 600。执行: chmod 600 $QUANTY_DATASTORE_ENV" >&2
    exit 1
  fi
  set -a
  # shellcheck disable=SC1090
  . "$QUANTY_DATASTORE_ENV"
  set +a
fi

CONTAINER_NAME="quanty-redis"
REDIS_IMAGE="redis:7"
HOST_PORT="6379"

DATA_DIR="/root/quanty_trade/redis"

# 密码只从环境变量读取，脚本里不留明文。未设置直接退出，不使用默认值。
REDIS_PASSWORD="${REDIS_PASSWORD:-}"

RECREATE=0
ASSUME_YES=0
for arg in "$@"; do
  case "$arg" in
    --recreate) RECREATE=1 ;;
    --yes) ASSUME_YES=1 ;;
    -h|--help)
      echo "用法: $0 [--recreate] [--yes]"
      echo "  不带参数: 容器已存在则不做任何事；不存在才创建。"
      echo "  --recreate: 允许先停掉并删除已存在的容器再重建（需确认）。"
      echo "  --yes     : 跳过交互确认（供非交互场景使用）。"
      echo "环境变量(必填): REDIS_PASSWORD（第三把，必须与两把 MySQL 口令都不同）"
      echo "              可写进 \$QUANTY_DATASTORE_ENV（默认 /etc/quanty/datastore.env，权限 600）"
      exit 0
      ;;
    *) echo "未知参数: $arg（可用: --recreate --yes）"; exit 1 ;;
  esac
done

if [ -z "$REDIS_PASSWORD" ]; then
  echo "错误: 环境变量 REDIS_PASSWORD 未设置。请先 export，不要写进脚本。" >&2
  exit 1
fi

# 台账 #10：Redis 曾与 MySQL root / 业务账号共用同一把 8 位口令。只在同一份
# datastore.env 里能看到另外两把时才比得了，比不了就跳过（不打印任何值）。
for _other in MYSQL_ROOT_PASSWORD MYSQL_PASSWORD DB_PASS; do
  eval "_v=\${$_other:-}"
  if [ -n "$_v" ] && [ "$_v" = "$REDIS_PASSWORD" ]; then
    echo "错误: REDIS_PASSWORD 与 $_other 相同。三把口令必须互不相同（台账 #10）。" >&2
    exit 1
  fi
done
unset _other _v

docker version >/dev/null

container_state() {
  docker inspect -f '{{.State.Status}}' "$1" 2>/dev/null || echo absent
}

STATE="$(container_state "$CONTAINER_NAME")"

if [ "$STATE" != "absent" ] && [ "$RECREATE" -ne 1 ]; then
  echo "容器 ${CONTAINER_NAME} 已存在（状态: ${STATE}），本脚本不会动它。"
  echo "确实需要重建请显式加 --recreate。"
  exit 0
fi

if [ "$STATE" != "absent" ]; then
  echo "即将重建 ${CONTAINER_NAME}（当前状态: ${STATE}）。"
  echo "数据目录: ${DATA_DIR} -> /data（宿主机 bind mount，删容器不删数据）"
  echo "当前实际挂载:"
  docker inspect -f '{{range .Mounts}}  {{.Type}} {{.Source}} -> {{.Destination}}{{println}}{{end}}' "$CONTAINER_NAME"
  if [ "$ASSUME_YES" -ne 1 ]; then
    read -r -p "确认停机并重建？输入 recreate 继续: " reply
    if [ "$reply" != "recreate" ]; then
      echo "已取消，未做任何改动。"
      exit 1
    fi
  fi
fi

mkdir -p "$DATA_DIR"

docker pull "$REDIS_IMAGE"

if [ "$STATE" != "absent" ]; then
  # 先优雅停（让 AOF/RDB 落盘），再删；避免 rm -f 直接 SIGKILL 丢掉最后一段写入。
  docker stop -t 30 "$CONTAINER_NAME" >/dev/null
  docker rm "$CONTAINER_NAME" >/dev/null
fi

docker run -d \
  --name "$CONTAINER_NAME" \
  --restart always \
  -p "${HOST_PORT}:6379" \
  -v "${DATA_DIR}:/data" \
  "$REDIS_IMAGE" \
  redis-server \
  --appendonly yes \
  --requirepass "$REDIS_PASSWORD" >/dev/null

for _ in $(seq 1 60); do
  if docker exec -e REDISCLI_AUTH="$REDIS_PASSWORD" "$CONTAINER_NAME" redis-cli ping >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! docker exec -e REDISCLI_AUTH="$REDIS_PASSWORD" "$CONTAINER_NAME" redis-cli ping >/dev/null 2>&1; then
  echo "Redis 未在预期时间内就绪"
  exit 1
fi

echo "Redis 部署完成"
echo "Addr: <server-ip>:${HOST_PORT}"
echo "Data dir: ${DATA_DIR} (已持久化)"
