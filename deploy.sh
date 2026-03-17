#!/bin/bash

# ==========================================
# QuantyTrade 一键部署推送脚本
# ==========================================

# 1. 配置您的 Docker Hub ID
DOCKER_HUB_ID="zhaoxianxinclimber108"

if [ "$DOCKER_HUB_ID" == "<YOUR_DOCKERHUB_ID>" ]; then
    echo "❌ 错误: 请先在 deploy.sh 中配置您的 DOCKER_HUB_ID"
    exit 1
fi

echo "🚀 开始构建最新镜像..."

# 2. 构建并推送后端镜像
echo "📦 构建后端镜像..."
docker build -t $DOCKER_HUB_ID/quanty-backend:latest -f backend/Dockerfile .
echo "📤 推送后端镜像到 Docker Hub..."
docker push $DOCKER_HUB_ID/quanty-backend:latest

# 3. 构建并推送前端镜像
echo "📦 构建前端镜像..."
docker build -t $DOCKER_HUB_ID/quanty-frontend:latest -f frontend/Dockerfile .
echo "📤 推送前端镜像到 Docker Hub..."
docker push $DOCKER_HUB_ID/quanty-frontend:latest

echo "✅ 镜像发布成功！版本: latest"
echo "------------------------------------------"
echo "🌐 服务器更新命令:"
echo "export DOCKER_HUB_ID=$DOCKER_HUB_ID"
echo "docker-compose -f docker-compose.prod.yml pull"
echo "docker-compose -f docker-compose.prod.yml up -d"
echo "------------------------------------------"
