#!/usr/bin/env bash
set -euo pipefail

DOCKER_HUB_ID="zhaoxianxinclimber108"
BACKEND_IMAGE="${DOCKER_HUB_ID}/quanty_trade-backend"
BACKEND_VERSION="${BACKEND_VERSION}"

CONTAINER_NAME="quanty-backend"
HOST_PORT="8080"

DB_TYPE="mysql"
DB_HOST="137.220.219.172"
DB_PORT="3306"
DB_USER="root"
DB_PASS="work@..."
DB_NAME="quanty_trade"

STRATEGIES_DIR="/root/quanty_trade/strategies"

REDIS_ENABLED="true"
REDIS_ADDR="137.220.219.172" # e.g. 127.0.0.1:6379 or <redis-ip>:6379
REDIS_PASSWORD="work@..."
REDIS_DB="0"
REDIS_PREFIX="qt"

EXCHANGE="binance"
BINANCE_MARKET="usdm"

docker version >/dev/null

if [ "$BACKEND_VERSION" = "REPLACE_BACKEND_TAG" ] || [ -z "$BACKEND_VERSION" ]; then
  echo "请先在脚本顶部填写 BACKEND_VERSION（后端镜像 tag）"
  exit 1
fi

if [ "$DB_TYPE" = "mysql" ]; then
  if [ "$DB_HOST" = "REPLACE_DB_HOST" ] || [ "$DB_USER" = "REPLACE_DB_USER" ] || [ "$DB_PASS" = "REPLACE_DB_PASS" ]; then
    echo "请先在脚本顶部填写数据库配置：DB_HOST DB_USER DB_PASS"
    exit 1
  fi
fi

if [ "${REDIS_ENABLED}" = "true" ]; then
  if [ "$REDIS_ADDR" = "REPLACE_REDIS_ADDR" ] || [ -z "$REDIS_ADDR" ]; then
    echo "请先在脚本顶部填写 REDIS_ADDR"
    exit 1
  fi
  if [ "$REDIS_PASSWORD" = "REPLACE_REDIS_PASSWORD" ]; then
    echo "请先在脚本顶部填写 REDIS_PASSWORD（如无密码可置空）"
    exit 1
  fi

  if [[ "$REDIS_ADDR" != *:* ]]; then
    REDIS_ADDR="${REDIS_ADDR}:6379"
  fi
fi

docker pull "${BACKEND_IMAGE}:${BACKEND_VERSION}"

docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true

mkdir -p "${STRATEGIES_DIR}"

docker run -d \
  --name "${CONTAINER_NAME}" \
  --restart always \
  -p "${HOST_PORT}:8080" \
  -v "${STRATEGIES_DIR}:/app/strategies" \
  -e PORT=8080 \
  -e SERVER_PORT=8080 \
  -e GIN_MODE=release \
  -e DB_TYPE="${DB_TYPE}" \
  -e DB_USER="${DB_USER}" \
  -e DB_PASS="${DB_PASS}" \
  -e DB_HOST="${DB_HOST}" \
  -e DB_PORT="${DB_PORT}" \
  -e DB_NAME="${DB_NAME}" \
  -e STRATEGIES_DIR="/app/strategies" \
  -e REDIS_ENABLED="${REDIS_ENABLED}" \
  -e REDIS_ADDR="${REDIS_ADDR}" \
  -e REDIS_PASSWORD="${REDIS_PASSWORD}" \
  -e REDIS_DB="${REDIS_DB}" \
  -e REDIS_PREFIX="${REDIS_PREFIX}" \
  -e EXCHANGE="${EXCHANGE}" \
  -e BINANCE_MARKET="${BINANCE_MARKET}" \
  -e EXCHANGE=binance \
  -e BINANCE_MARKET=usdm \ 
  -e BINANCE_API_KEY="2g60CgD0iZYf1ysqcI5HVXMvZKs7Pv5HnCzyrFMrEvH2NbAORjSwbTH4CQyjlRS9" \
  -e BINANCE_API_SECRET="OSFfTOgErRNRfY5mq17JN5VlQqc4w61CFcx6Y6BNDwVr6PD79SYdCPWlzrZy1yF4" \ 
  "${BACKEND_IMAGE}:${BACKEND_VERSION}" >/dev/null

echo "后端部署完成"
echo "Backend: http://<server-ip>:${HOST_PORT}"
