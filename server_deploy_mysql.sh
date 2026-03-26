#!/usr/bin/env bash
set -euo pipefail

CONTAINER_NAME="quanty-mysql"
MYSQL_IMAGE="mysql:8"
HOST_PORT="3306"

DATA_DIR="/root/quanty_trade/mysql"

MYSQL_ROOT_PASSWORD="work@..."
MYSQL_DATABASE="quanty_trade"
MYSQL_USER="quanty"
MYSQL_PASSWORD="work@..."

docker version >/dev/null

if [ "$MYSQL_ROOT_PASSWORD" = "REPLACE_MYSQL_ROOT_PASSWORD" ] || [ -z "$MYSQL_ROOT_PASSWORD" ]; then
  echo "请先在脚本顶部填写 MYSQL_ROOT_PASSWORD"
  exit 1
fi

if [ "$MYSQL_PASSWORD" = "REPLACE_MYSQL_PASSWORD" ] || [ -z "$MYSQL_PASSWORD" ]; then
  echo "请先在脚本顶部填写 MYSQL_PASSWORD"
  exit 1
fi

mkdir -p "$DATA_DIR"

docker pull "$MYSQL_IMAGE"

docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true

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
  if docker exec "$CONTAINER_NAME" mysqladmin ping -uroot "-p${MYSQL_ROOT_PASSWORD}" --silent >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

if ! docker exec "$CONTAINER_NAME" mysqladmin ping -uroot "-p${MYSQL_ROOT_PASSWORD}" --silent >/dev/null 2>&1; then
  echo "MySQL 未在预期时间内就绪"
  exit 1
fi

echo "MySQL 部署完成"
echo "Host: <server-ip>:${HOST_PORT}"
echo "Data dir: ${DATA_DIR} (已持久化)"
echo "DB: ${MYSQL_DATABASE}"
echo "User: ${MYSQL_USER}"
