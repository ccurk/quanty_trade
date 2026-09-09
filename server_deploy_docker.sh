#!/usr/bin/env bash
set -euo pipefail

COMPONENT="${1:-all}"

# 凭据一律不写进本脚本（本仓库是公开的）。真实值放在服务器本地的 600 权限 env
# 文件里，默认 /etc/quanty/backend.env，可用 QUANTY_ENV_FILE 覆盖路径。
# 与 server_deploy_backend.sh 用同一份文件、同一套口径。
QUANTY_ENV_FILE="${QUANTY_ENV_FILE:-/etc/quanty/backend.env}"
if [ -f "$QUANTY_ENV_FILE" ]; then
  perm="$(stat -c '%a' "$QUANTY_ENV_FILE" 2>/dev/null || stat -f '%Lp' "$QUANTY_ENV_FILE" 2>/dev/null || echo '')"
  if [ -n "$perm" ] && [ "$perm" != "600" ]; then
    echo "错误: $QUANTY_ENV_FILE 权限是 $perm，必须是 600。执行: chmod 600 $QUANTY_ENV_FILE" >&2
    exit 1
  fi
  set -a
  # shellcheck disable=SC1090
  . "$QUANTY_ENV_FILE"
  set +a
fi

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
# 下面这几个是凭据，只从 env 读（$QUANTY_ENV_FILE 或已 export 的变量）。
# 原来它们是 REPLACE_ 占位符，等于叫下一个人把明文填进这个**被 git 跟踪的**文件——
# #54 那把公网可下载的 key 就是这么来的。占位符留给非凭据字段（HOST/ADDR/TAG）。
DB_USER="${DB_USER:-quanty}"
DB_PASS="${DB_PASS:-}"
DB_NAME="quanty_trade"

REDIS_ENABLED="true"
REDIS_ADDR="REPLACE_REDIS_ADDR"
REDIS_PASSWORD="${REDIS_PASSWORD:-}"
REDIS_DB="0"
REDIS_PREFIX="qt"

EXCHANGE="binance"
BINANCE_MARKET="usdm"
BINANCE_API_KEY="${BINANCE_API_KEY:-}"
BINANCE_API_SECRET="${BINANCE_API_SECRET:-}"

TELEGRAM_ENABLED="true"
TELEGRAM_BOT_TOKEN="${TELEGRAM_BOT_TOKEN:-}"
TELEGRAM_POLL_TIMEOUT_SECONDS="30"

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
    if [ "$DB_HOST" = "REPLACE_DB_HOST" ]; then
      echo "请先在脚本顶部填写 DB_HOST"
      exit 1
    fi
    if [ -z "$DB_PASS" ]; then
      echo "错误: DB_PASS 未设置。写进 $QUANTY_ENV_FILE (chmod 600) 或先 export，别写进本脚本。" >&2
      exit 1
    fi
    if [ "$DB_USER" = "root" ]; then
      echo "错误: DB_USER=root。后端必须用业务账号连库，root 只留给运维/备份（台账 #10）。" >&2
      exit 1
    fi
    if [ -z "$DB_HOST" ] || [ -z "$DB_PORT" ] || [ -z "$DB_USER" ] || [ -z "$DB_PASS" ] || [ -z "$DB_NAME" ]; then
      echo "DB_TYPE=mysql 时必须填写：DB_HOST DB_PORT DB_USER DB_PASS DB_NAME"
      exit 1
    fi
  fi
  if [ "$EXCHANGE" = "binance" ]; then
    if [ "$BINANCE_API_KEY" = "REPLACE_BINANCE_API_KEY" ] || [ "$BINANCE_API_SECRET" = "REPLACE_BINANCE_API_SECRET" ]; then
      echo "请先在脚本顶部填写币安配置：BINANCE_API_KEY BINANCE_API_SECRET"
      exit 1
    fi
  fi
  if [ "$REDIS_ENABLED" = "true" ] && [ "$REDIS_ADDR" = "REPLACE_REDIS_ADDR" ]; then
    echo "请先在脚本顶部填写 Redis 配置：REDIS_ADDR"
    exit 1
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
    -e REDIS_ENABLED="${REDIS_ENABLED}" \
    -e REDIS_ADDR="${REDIS_ADDR}" \
    -e REDIS_PASSWORD="${REDIS_PASSWORD}" \
    -e REDIS_DB="${REDIS_DB}" \
    -e REDIS_PREFIX="${REDIS_PREFIX}" \
    -e EXCHANGE="${EXCHANGE}" \
    -e BINANCE_MARKET="${BINANCE_MARKET}" \
    -e BINANCE_API_KEY="${BINANCE_API_KEY}" \
    -e BINANCE_API_SECRET="${BINANCE_API_SECRET}" \
    -e TELEGRAM_ENABLED="${TELEGRAM_ENABLED}" \
    -e TELEGRAM_BOT_TOKEN="${TELEGRAM_BOT_TOKEN}" \
    -e TELEGRAM_POLL_TIMEOUT_SECONDS="${TELEGRAM_POLL_TIMEOUT_SECONDS}" \
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
