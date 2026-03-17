#!/bin/bash

# ==========================================
# QuantyTrade 一键部署推送脚本
# ==========================================

# 1. 配置您的 Docker Hub ID
DOCKER_HUB_ID="zhaoxianxinclimber108"
PROJECT_NAMESPACE="quanty_trade"
VERSION_INPUT="$1"

if [ -z "$DOCKER_HUB_ID" ]; then
    echo "❌ 错误: 请先在 deploy.sh 中配置您的 DOCKER_HUB_ID"
    exit 1
fi

generate_version() {
    if [ -n "$VERSION_INPUT" ]; then
        echo "$VERSION_INPUT"
        return
    fi

    TS=$(date +%Y%m%d%H%M%S)
    if command -v git >/dev/null 2>&1 && git rev-parse --git-dir >/dev/null 2>&1; then
        GIT_SHA=$(git rev-parse --short HEAD 2>/dev/null)
        echo "${TS}-${GIT_SHA}"
        return
    fi

    RAND=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n' | head -c 8)
    echo "${TS}-${RAND}"
}

VERSION="$(generate_version)"

echo "🚀 开始构建镜像版本: $VERSION"

# 2. 构建并推送后端镜像
echo "📦 构建后端镜像..."
docker build -t $DOCKER_HUB_ID/$PROJECT_NAMESPACE/quanty-backend:$VERSION -f backend/Dockerfile .
echo "📤 推送后端镜像到 Docker Hub..."
docker push $DOCKER_HUB_ID/$PROJECT_NAMESPACE/quanty-backend:$VERSION

# 3. 构建并推送前端镜像
echo "📦 构建前端镜像..."
docker build -t $DOCKER_HUB_ID/$PROJECT_NAMESPACE/quanty-frontend:$VERSION -f frontend/Dockerfile .
echo "📤 推送前端镜像到 Docker Hub..."
docker push $DOCKER_HUB_ID/$PROJECT_NAMESPACE/quanty-frontend:$VERSION

echo "✅ 镜像发布成功！版本: $VERSION"
echo "------------------------------------------"
echo "🌐 服务器更新命令:"
echo "export DOCKER_HUB_ID=$DOCKER_HUB_ID"
echo "export PROJECT_NAMESPACE=$PROJECT_NAMESPACE"
echo "export APP_VERSION=$VERSION"
echo "docker-compose -f docker-compose.prod.yml pull"
echo "docker-compose -f docker-compose.prod.yml up -d"
echo "------------------------------------------"
