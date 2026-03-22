#!/usr/bin/env bash
set -euo pipefail

DOCKER_HUB_ID="zhaoxianxinclimber108"
BACKEND_IMAGE="${DOCKER_HUB_ID}/quanty_trade-backend"
BACKEND_VERSION="${BACKEND_VERSION}"

CONTAINER_NAME="quanty-backend"
HOST_PORT="8080"

DB_TYPE="mysql"
DB_HOST="REPLACE_DB_HOST"
DB_PORT="3306"
DB_USER="REPLACE_DB_USER"
DB_PASS="REPLACE_DB_PASS"
DB_NAME="quanty_trade"

STRATEGIES_DIR="/root/quanty_trade/strategies"

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

docker pull "${BACKEND_IMAGE}:${BACKEND_VERSION}"

docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true

mkdir -p "${STRATEGIES_DIR}"

docker run -d \
  --name "${CONTAINER_NAME}" \
  --restart always \
  -p "${HOST_PORT}:8080" \
  -v "${STRATEGIES_DIR}:/app/strategies" \
  -e PORT=8080 \
  -e DB_TYPE="${DB_TYPE}" \
  -e DB_USER="${DB_USER}" \
  -e DB_PASS="${DB_PASS}" \
  -e DB_HOST="${DB_HOST}" \
  -e DB_PORT="${DB_PORT}" \
  -e DB_NAME="${DB_NAME}" \
  -e STRATEGIES_DIR="/app/strategies" \
  "${BACKEND_IMAGE}:${BACKEND_VERSION}" >/dev/null

echo "后端部署完成"
echo "Backend: http://<server-ip>:${HOST_PORT}"
