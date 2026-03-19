# QuantyTrade 量化交易系统

这是一个基于 **Go (后端)** + **Python (策略)** + **React (前端)** 的全栈量化交易系统。

## 系统架构

- **后端 (Go)**: 负责用户认证、策略生命周期管理、WebSocket 实时数据分发以及与交易平台的 API 对接。
- **策略 (Python)**: 运行在独立的子进程中，通过标准输入输出 (stdin/stdout) 与 Go 后端进行高性能双向通信。
- **前端 (React + Tailwind)**: 提供现代化的管理仪表盘，支持策略监控、订单查看、广场引用及系统管理。

## 目录结构

- `backend/`: Go 后端核心代码。
- `strategies/`: Python 策略模板及基类。
- `frontend/`: React 前端应用。
- `scripts/`: 自动化测试脚本。
- `quanty.db`: SQLite 数据库（存储用户信息及策略配置）。

## 快速开始

### 1. 运行后端
```bash
cd backend
go mod tidy
# 默认读取 conf/config.yaml（若不存在则读取 conf/config.example.yaml）
go run ./cmd
```
*默认管理员账号: `admin` / `admin123`*

#### 使用 MySQL（推荐 docker）
1) 启动 MySQL
```bash
docker run -d --name quanty-mysql \
  -e MYSQL_ROOT_PASSWORD=rootpass \
  -e MYSQL_DATABASE=quanty_trade \
  -e MYSQL_USER=quanty \
  -e MYSQL_PASSWORD=quantypass \
  -p 3306:3306 \
  mysql:8.0
```

2) 修改 conf/config.yaml
```yaml
db:
  type: "mysql"
  host: "127.0.0.1"
  port: "3306"
  user: "quanty"
  pass: "quantypass"
  name: "quanty_trade"
```

#### Binance 连接方式
- 推荐：用户注册时提交 configs，后端加密保存到数据库（需要 security.config_encryption_key 或环境变量 CONFIG_ENCRYPTION_KEY）
- 可选：直接在 conf/config.yaml 的 exchange.binance.api_key/api_secret 配置（建议只在本地开发使用，文件已加入 .gitignore）
- 仍支持环境变量回退：BINANCE_API_KEY / BINANCE_API_SECRET / BINANCE_TESTNET

### 2. 运行前端
```bash
cd frontend
npm install
npm run dev
```

### 3. 执行测试
```bash
python3 scripts/test_api.py
```

## 核心功能

- [x] **用户系统**: 支持 JWT 认证及管理员权限管理。
- [x] **策略引擎**: Python 进程动态启动与停止，支持多策略并行。
- [x] **策略广场**: 支持策略的发布、浏览及一键引用。
- [x] **实时监控**: 通过 WebSocket 实时推送 K 线数据、订单状态及策略日志。
- [x] **平台适配**: 统一的 Exchange 接口，支持快速接入不同交易平台（目前内置 Mock 模拟平台）。

## 技术栈

- **Go**: Gin, GORM, Gorilla WebSocket
- **Python**: JSON-based IPC
- **Frontend**: Vite, React, TypeScript, Tailwind CSS, Lucide Icons
- **Database**: SQLite (可扩展至 MySQL)

## 启动
 - export DOCKER_HUB_ID=zhaoxianxinclimber108
 - export BACKEND_VERSION=20260317212705-0f0724e-backend
 - export FRONTEND_VERSION=20260317212705-0f0724e-frontend
 - docker-compose -f docker-compose.prod.yml pull
 - docker-compose -f docker-compose.prod.yml up -d
