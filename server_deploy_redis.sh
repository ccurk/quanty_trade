#!/usr/bin/env bash
set -euo pipefail

CONTAINER_NAME="quanty-redis"
REDIS_IMAGE="redis:7"
HOST_PORT="6379"

DATA_DIR="/root/quanty_trade/redis"

REDIS_PASSWORD="work@..."

docker version >/dev/null

if [ "$REDIS_PASSWORD" = "REPLACE_REDIS_PASSWORD" ] || [ -z "$REDIS_PASSWORD" ]; then
  echo "请先在脚本顶部填写 REDIS_PASSWORD"
  exit 1
fi

mkdir -p "$DATA_DIR"

docker pull "$REDIS_IMAGE"

docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true

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
  if docker exec "$CONTAINER_NAME" redis-cli -a "$REDIS_PASSWORD" ping >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! docker exec "$CONTAINER_NAME" redis-cli -a "$REDIS_PASSWORD" ping >/dev/null 2>&1; then
  echo "Redis 未在预期时间内就绪"
  exit 1
fi

echo "Redis 部署完成"
echo "Addr: <server-ip>:${HOST_PORT}"
echo "Data dir: ${DATA_DIR} (已持久化)"
