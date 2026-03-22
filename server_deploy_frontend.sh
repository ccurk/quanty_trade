#!/usr/bin/env bash
set -euo pipefail

DOCKER_HUB_ID="zhaoxianxinclimber108"
FRONTEND_IMAGE="${DOCKER_HUB_ID}/quanty_trade-frontend"
FRONTEND_VERSION="${FRONTEND_VERSION}"

CONTAINER_NAME="quanty-frontend"
HOST_PORT="81"

BACKEND_BASE_URL="http://137.220.219.172:8080"

CONF_DIR="/root/quanty_trade/nginx"
CONF_FILE="${CONF_DIR}/default.conf"

docker version >/dev/null

if [ "$FRONTEND_VERSION" = "REPLACE_FRONTEND_TAG" ] || [ -z "$FRONTEND_VERSION" ]; then
  echo "请先在脚本顶部填写 FRONTEND_VERSION（前端镜像 tag）"
  exit 1
fi

if command -v ss >/dev/null 2>&1; then
  if ss -lntp 2>/dev/null | grep -qE "LISTEN\\s+.*:${HOST_PORT}\\b"; then
    echo "端口 ${HOST_PORT} 已被占用，请先释放端口或修改 HOST_PORT"
    ss -lntp 2>/dev/null | grep -E ":${HOST_PORT}\\b" || true
    exit 1
  fi
elif command -v netstat >/dev/null 2>&1; then
  if netstat -lntp 2>/dev/null | grep -qE ":${HOST_PORT}\\b"; then
    echo "端口 ${HOST_PORT} 已被占用，请先释放端口或修改 HOST_PORT"
    netstat -lntp 2>/dev/null | grep -E ":${HOST_PORT}\\b" || true
    exit 1
  fi
fi

mkdir -p "$CONF_DIR"

cat > "$CONF_FILE" <<EOF
server {
    listen 80;
    location / {
        root /usr/share/nginx/html;
        index index.html;
        try_files \$uri \$uri/ /index.html;
    }
    location /api {
        proxy_pass ${BACKEND_BASE_URL};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
    }
    location /ws {
        proxy_pass ${BACKEND_BASE_URL};
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
EOF

docker pull "${FRONTEND_IMAGE}:${FRONTEND_VERSION}"

docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true

docker run -d \
  --name "${CONTAINER_NAME}" \
  --restart always \
  -p "${HOST_PORT}:81" \
  -v "${CONF_FILE}:/etc/nginx/conf.d/default.conf:ro" \
  "${FRONTEND_IMAGE}:${FRONTEND_VERSION}" >/dev/null

echo "前端部署完成"
echo "Frontend: http://<server-ip>:${HOST_PORT}"
echo "Proxy to backend: ${BACKEND_BASE_URL}"
