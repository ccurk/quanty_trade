#!/usr/bin/env bash
set -euo pipefail

COMPONENT="${1:-all}"

DOCKER_HUB_ID="zhaoxianxinclimber108"
BACKEND_IMAGE="${DOCKER_HUB_ID}/quanty_trade-backend"
FRONTEND_IMAGE="${DOCKER_HUB_ID}/quanty_trade-frontend"
BACKEND_VERSION="REPLACE_BACKEND_TAG"
FRONTEND_VERSION="REPLACE_FRONTEND_TAG"

NETWORK_NAME="quanty_net"

BACKEND_CONTAINER="backend"
BACKEND_PORT="8080"

FRONTEND_CONTAINER="frontend"
FRONTEND_PORT="80"

DB_TYPE="mysql"
DB_HOST="REPLACE_DB_HOST"
DB_PORT="3306"
DB_USER="REPLACE_DB_USER"
DB_PASS="REPLACE_DB_PASS"
DB_NAME="quanty_trade"

docker version >/dev/null

if [ "$COMPONENT" != "backend" ] && [ "$COMPONENT" != "frontend" ] && [ "$COMPONENT" != "all" ]; then
  echo "参数不合法：$COMPONENT"
  echo "用法："
  echo "  ./server_deploy_docker.sh backend"
  echo "  ./server_deploy_docker.sh frontend"
  echo "  ./server_deploy_docker.sh all"
  exit 1
fi

docker network create "$NETWORK_NAME" >/dev/null 2>&1 || true

if [ "$COMPONENT" = "backend" ] || [ "$COMPONENT" = "all" ]; then
  if [ "$DB_TYPE" = "mysql" ]; then
    if [ "$DB_HOST" = "REPLACE_DB_HOST" ] || [ "$DB_USER" = "REPLACE_DB_USER" ] || [ "$DB_PASS" = "REPLACE_DB_PASS" ]; then
      echo "请先在脚本顶部填写数据库配置：DB_HOST DB_USER DB_PASS"
      exit 1
    fi
    if [ -z "$DB_HOST" ] || [ -z "$DB_PORT" ] || [ -z "$DB_USER" ] || [ -z "$DB_PASS" ] || [ -z "$DB_NAME" ]; then
      echo "DB_TYPE=mysql 时必须填写：DB_HOST DB_PORT DB_USER DB_PASS DB_NAME"
      exit 1
    fi
  fi
  if [ "$BACKEND_VERSION" = "REPLACE_BACKEND_TAG" ] || [ -z "$BACKEND_VERSION" ]; then
    echo "请先在脚本顶部填写 BACKEND_VERSION（后端镜像 tag）"
    exit 1
  fi

  docker rm -f "$BACKEND_CONTAINER" >/dev/null 2>&1 || true
  docker pull "${BACKEND_IMAGE}:${BACKEND_VERSION}" >/dev/null
  docker run -d \
    --name "$BACKEND_CONTAINER" \
    --restart always \
    --network "$NETWORK_NAME" \
    --network-alias backend \
    -p "${BACKEND_PORT}:8080" \
    -e PORT=8080 \
    -e DB_TYPE="${DB_TYPE}" \
    -e DB_USER="${DB_USER}" \
    -e DB_PASS="${DB_PASS}" \
    -e DB_HOST="${DB_HOST}" \
    -e DB_PORT="${DB_PORT}" \
    -e DB_NAME="${DB_NAME}" \
    "${BACKEND_IMAGE}:${BACKEND_VERSION}" >/dev/null
fi

if [ "$COMPONENT" = "frontend" ] || [ "$COMPONENT" = "all" ]; then
  if [ "$FRONTEND_VERSION" = "REPLACE_FRONTEND_TAG" ] || [ -z "$FRONTEND_VERSION" ]; then
    echo "请先在脚本顶部填写 FRONTEND_VERSION（前端镜像 tag）"
    exit 1
  fi
  docker rm -f "$FRONTEND_CONTAINER" >/dev/null 2>&1 || true
  docker pull "${FRONTEND_IMAGE}:${FRONTEND_VERSION}" >/dev/null
  docker run -d \
    --name "$FRONTEND_CONTAINER" \
    --restart always \
    --network "$NETWORK_NAME" \
    -p "${FRONTEND_PORT}:80" \
    "${FRONTEND_IMAGE}:${FRONTEND_VERSION}" >/dev/null
fi

echo "部署完成"
echo "前端: http://<server-ip>:${FRONTEND_PORT}"
echo "后端: http://<server-ip>:${BACKEND_PORT}"
