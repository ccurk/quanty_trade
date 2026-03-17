#!/bin/bash

# ==========================================
# QuantyTrade 一键部署推送脚本
# ==========================================

# 1. 配置您的 Docker Hub ID
DOCKER_HUB_ID="zhaoxianxinclimber108"
COMPONENT="$1"        # backend | frontend | all
VERSION_INPUT="$2"    # optional
BACKEND_VERSION_FILE=".deploy_version_backend"
FRONTEND_VERSION_FILE=".deploy_version_frontend"
PLATFORMS="linux/amd64,linux/arm64"

if [ -z "$DOCKER_HUB_ID" ]; then
    echo "❌ 错误: 请先在 deploy.sh 中配置您的 DOCKER_HUB_ID"
    exit 1
fi

if [ -z "$COMPONENT" ]; then
    COMPONENT="all"
fi

if [ "$COMPONENT" != "backend" ] && [ "$COMPONENT" != "frontend" ] && [ "$COMPONENT" != "all" ]; then
    echo "❌ 错误: 参数不合法。用法："
    echo "  ./deploy.sh all"
    echo "  ./deploy.sh backend"
    echo "  ./deploy.sh frontend"
    echo "可选：指定版本号：./deploy.sh backend 20260317010101-abc123"
    exit 1
fi

generate_version() {
    COMPONENT_SUFFIX="$1"
    VERSION_FILE="$2"

    if [ -n "$VERSION_INPUT" ]; then
        if [ "$COMPONENT" = "all" ]; then
            echo "${VERSION_INPUT}-${COMPONENT_SUFFIX}"
        else
            echo "$VERSION_INPUT"
        fi
        return
    fi

    if [ -f "$VERSION_FILE" ]; then
        cat "$VERSION_FILE"
        return
    fi

    TS=$(date +%Y%m%d%H%M%S)
    if command -v git >/dev/null 2>&1 && git rev-parse --git-dir >/dev/null 2>&1; then
        GIT_SHA=$(git rev-parse --short HEAD 2>/dev/null)
        echo "${TS}-${GIT_SHA}-${COMPONENT_SUFFIX}"
        return
    fi

    RAND=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n' | head -c 8)
    echo "${TS}-${RAND}-${COMPONENT_SUFFIX}"
}

BACKEND_VERSION="$(generate_version backend "$BACKEND_VERSION_FILE")"
FRONTEND_VERSION="$(generate_version frontend "$FRONTEND_VERSION_FILE")"

echo "$BACKEND_VERSION" > "$BACKEND_VERSION_FILE"
echo "$FRONTEND_VERSION" > "$FRONTEND_VERSION_FILE"

echo "🚀 开始构建并推送: $COMPONENT"
echo "  - backend:  $BACKEND_VERSION"
echo "  - frontend: $FRONTEND_VERSION"

docker buildx version >/dev/null
docker buildx inspect >/dev/null 2>&1 || docker buildx create --use >/dev/null

if [ "$COMPONENT" = "backend" ] || [ "$COMPONENT" = "all" ]; then
    echo "📦 构建后端镜像..."
    echo "📤 推送后端镜像到 Docker Hub..."
    docker buildx build \
      --platform "$PLATFORMS" \
      -t $DOCKER_HUB_ID/quanty_trade-backend:$BACKEND_VERSION \
      -f backend/Dockerfile \
      --push \
      .
fi

if [ "$COMPONENT" = "frontend" ] || [ "$COMPONENT" = "all" ]; then
    echo "📦 构建前端镜像..."
    echo "📤 推送前端镜像到 Docker Hub..."
    docker buildx build \
      --platform "$PLATFORMS" \
      -t $DOCKER_HUB_ID/quanty_trade-frontend:$FRONTEND_VERSION \
      -f frontend/Dockerfile \
      --push \
      .
fi

echo "✅ 镜像发布成功！"
echo "------------------------------------------"
echo "🌐 服务器更新命令:"
echo "export DOCKER_HUB_ID=$DOCKER_HUB_ID"
echo "export BACKEND_VERSION=$BACKEND_VERSION"
echo "export FRONTEND_VERSION=$FRONTEND_VERSION"
echo "docker compose -f docker-compose.prod.yml pull"
echo "docker compose -f docker-compose.prod.yml up -d"
echo "------------------------------------------"
