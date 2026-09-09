#!/usr/bin/env bash
set -euo pipefail

# 凭据一律不写进本脚本（本仓库是公开的）。真实值放在服务器本地的 600 权限 env
# 文件里，默认 /etc/quanty/datastore.env，可用 QUANTY_DATASTORE_ENV 覆盖路径。
# 这个文件与后端的 /etc/quanty/backend.env 分开：root 口令只出现在这一份里，
# 应用容器永远拿不到（台账 #10「三把互不相同」）。已 export 的环境变量优先。
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

CONTAINER_NAME="quanty-mysql"
MYSQL_IMAGE="mysql:8"
HOST_PORT="3306"

DATA_DIR="/root/quanty_trade/mysql"

# 密码只从环境变量读取，脚本里不留明文。未设置直接退出，不使用默认值。
MYSQL_ROOT_PASSWORD="${MYSQL_ROOT_PASSWORD:-}"
MYSQL_PASSWORD="${MYSQL_PASSWORD:-}"
MYSQL_DATABASE="${MYSQL_DATABASE:-quanty_trade}"
MYSQL_USER="${MYSQL_USER:-quanty}"

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
      echo "环境变量(必填): MYSQL_ROOT_PASSWORD MYSQL_PASSWORD（两者必须不同）"
      echo "              可写进 \$QUANTY_DATASTORE_ENV（默认 /etc/quanty/datastore.env，权限 600）"
      exit 0
      ;;
    *) echo "未知参数: $arg（可用: --recreate --yes）"; exit 1 ;;
  esac
done

if [ -z "$MYSQL_ROOT_PASSWORD" ]; then
  echo "错误: 环境变量 MYSQL_ROOT_PASSWORD 未设置。请先 export，不要写进脚本。" >&2
  exit 1
fi

if [ -z "$MYSQL_PASSWORD" ]; then
  echo "错误: 环境变量 MYSQL_PASSWORD 未设置。请先 export，不要写进脚本。" >&2
  exit 1
fi

# 台账 #10：root 与业务账号曾是同一把 8 位口令，泄漏一处 = 同时丢掉两者。
# 这里只比较是否相等，不打印、不记录任何值。
if [ "$MYSQL_ROOT_PASSWORD" = "$MYSQL_PASSWORD" ]; then
  echo "错误: MYSQL_ROOT_PASSWORD 与 MYSQL_PASSWORD 相同。root 与业务账号必须用两把不同的口令。" >&2
  echo "      这正是 #10 要修的问题，别在重建容器时又把它们设回一样。" >&2
  exit 1
fi

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
  # 数据落在宿主机 bind mount（见下方 DATA_DIR），删容器不等于删数据；
  # 但这是实盘库，停机会中断在跑的交易写入，仍然要一次显式确认。
  echo "即将重建 ${CONTAINER_NAME}（当前状态: ${STATE}）。"
  echo "数据目录: ${DATA_DIR} -> /var/lib/mysql（宿主机 bind mount，删容器不删数据）"
  echo "当前实际挂载:"
  docker inspect -f '{{range .Mounts}}  {{.Type}} {{.Source}} -> {{.Destination}}{{println}}{{end}}' "$CONTAINER_NAME"
  echo "建议先备份: tar czf /root/quanty_trade/mysql-backup-\$(date +%F).tgz -C ${DATA_DIR} ."
  if [ "$ASSUME_YES" -ne 1 ]; then
    read -r -p "确认停机并重建？输入 recreate 继续: " reply
    if [ "$reply" != "recreate" ]; then
      echo "已取消，未做任何改动。"
      exit 1
    fi
  fi
fi

mkdir -p "$DATA_DIR"

docker pull "$MYSQL_IMAGE"

if [ "$STATE" != "absent" ]; then
  # 先优雅停（让 InnoDB 正常 flush），再删；避免 rm -f 直接 SIGKILL 造成崩溃恢复。
  docker stop -t 60 "$CONTAINER_NAME" >/dev/null
  docker rm "$CONTAINER_NAME" >/dev/null
fi

docker run -d \
  --name "$CONTAINER_NAME" \
  --restart always \
  -p "${HOST_PORT}:3306" \
  -e MYSQL_ROOT_PASSWORD="$MYSQL_ROOT_PASSWORD" \
  -e MYSQL_DATABASE="$MYSQL_DATABASE" \
  -e MYSQL_USER="$MYSQL_USER" \
  -e MYSQL_PASSWORD="$MYSQL_PASSWORD" \
  -v "${DATA_DIR}:/var/lib/mysql" \
  "$MYSQL_IMAGE" >/dev/null

for _ in $(seq 1 60); do
  if docker exec -e MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$CONTAINER_NAME" mysqladmin ping -uroot --silent >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

if ! docker exec -e MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$CONTAINER_NAME" mysqladmin ping -uroot --silent >/dev/null 2>&1; then
  echo "MySQL 未在预期时间内就绪"
  exit 1
fi

echo "MySQL 部署完成"
echo "Host: <server-ip>:${HOST_PORT}"
echo "Data dir: ${DATA_DIR} (已持久化)"
echo "DB: ${MYSQL_DATABASE}"
echo "User: ${MYSQL_USER}"
