#!/bin/bash

# 获取当前绝对路径
PROJECT_ROOT=$(pwd)

# 颜色定义
GREEN='\033[0;32m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}--- 🚀 正在启动 QuantyTrade 量化交易系统 ---${NC}"

# 0. 清理残留进程 (可选)
echo -e "${BLUE}[0/2] 正在清理残留进程...${NC}"
lsof -ti:8080 | xargs kill -9 2>/dev/null
lsof -ti:5173 | xargs kill -9 2>/dev/null

# 1. 检查后端依赖
echo -e "${BLUE}[1/2] 正在启动后端服务器 (Go)...${NC}"
cd $PROJECT_ROOT/backend
go mod tidy
go run cmd/main.go &
BACKEND_PID=$!

# 2. 检查前端依赖并启动
echo -e "${BLUE}[2/2] 正在启动前端界面 (React)...${NC}"
cd $PROJECT_ROOT/frontend
# 检查 node_modules 是否存在，不存在则安装
if [ ! -d "node_modules" ]; then
    echo -e "${BLUE}首次启动，正在安装前端依赖...${NC}"
    npm install
fi
npm run dev &
FRONTEND_PID=$!

# 捕获退出信号，同时关闭前后端
trap "kill $BACKEND_PID $FRONTEND_PID; echo -e '${RED}\n已停止所有服务${NC}'; exit" SIGINT SIGTERM

echo -e "${GREEN}--- ✨ 系统启动成功! ---${NC}"
echo -e "${GREEN}后端 API: http://localhost:8080${NC}"
echo -e "${GREEN}前端 UI: http://localhost:5173${NC}"
echo -e "${BLUE}按 Ctrl+C 停止所有服务...${NC}"

# 保持脚本运行，实时查看日志
wait
