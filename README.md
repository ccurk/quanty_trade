# QuantyTrade 量化交易系统

这是一个基于 **Go (后端)** + **Redis (消息总线)** + **Python (策略进程)** + **React (前端)** 的全栈量化交易系统。

## 系统架构

- **后端 (Go)**: 负责用户认证、策略生命周期管理、行情订阅、风控与下单、WebSocket 实时分发。
- **消息总线 (Redis PubSub)**: 后端与策略进程之间的事件通道（行情、状态、交易信号）。
- **策略 (Python)**: 启动即独立进程运行，通过 Redis 订阅行情并发布交易信号；脚本自身维护记忆（历史K线、指标状态等）。
- **前端 (React + Tailwind)**: 提供策略管理、仓位管理、模板市场、实时日志与行情展示。

### 数据推送模型（全量 + 增量）

- **首次全量**：策略进程启动后会发布 `ready`（带 `boot_id`）。后端收到 `ready` 后，针对该策略的每个交易对推送一次 `history`（固定 200 根 1m K线，包含最新一根）。
- **后续增量**：后端每当收到交易所最新收盘K线（kline closed）时，推送单条 `candle` 追加更新。
- **脚本重启识别**：如果策略进程重启，`boot_id` 会变化；后端检测到变化后会再次推送 `history(200)`，保证脚本在“无记忆”场景也能恢复。

## 目录结构

- `backend/`: Go 后端核心代码。
- `strategies/`: Python 策略模板（Redis 模式）及最小 Redis 客户端。
- `frontend/`: React 前端应用。
- `scripts/`: 自动化测试脚本。
- `quanty.db`: SQLite 数据库（存储用户信息及策略配置）。

## 快速开始

### 0. 启动 Redis

本项目策略通信依赖 Redis PubSub，启动策略前必须先启动 Redis。

```bash
docker run -d --name quanty-redis -p 6379:6379 redis:7
```

### 1. 运行后端 (Go)

```bash
cd backend
go mod tidy
# 默认读取 conf/config.yaml（若不存在则读取 conf/config.example.yaml）
go run ./cmd
```

默认管理员账号：`admin` / `admin123`

#### Redis 配置

支持 `conf/config.yaml` 或环境变量覆盖（优先级：环境变量 > 配置文件）。

```bash
export REDIS_ENABLED=true
export REDIS_ADDR=127.0.0.1:6379
export REDIS_PASSWORD=
export REDIS_DB=0
export REDIS_PREFIX=qt
```

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

### 2. 运行前端 (React)

```bash
cd frontend
npm install
npm run dev
```

### 3. 执行测试
```bash
python3 scripts/test_api.py
```

## 策略与协议（Redis）

### 交易对配置

- 不再提供“选币器/选币模式”，策略直接通过配置指定交易对：
  - 单交易对：`symbol: "BTC/USDT"`
  - 多交易对：`symbols: "BTC/USDT,ETH/USDT"`（或前端多选填充）

### Redis 通道

以 `REDIS_PREFIX=qt` 为例：

- 行情通道：`qt:candle:{strategy_id}`
- 信号通道：`qt:signal:{strategy_id}`
- 状态通道：`qt:state:{strategy_id}`

### 消息格式

#### 策略状态（Python -> Redis）

```json
{"type":"ready","strategy_id":"...","boot_id":"...","created_at":"2026-03-22T12:34:56Z"}
```

#### 行情全量（Go -> Redis）

```json
{"type":"history","strategy_id":"...","symbol":"BTC/USDT","candles":[{"type":"candle","strategy_id":"...","symbol":"BTC/USDT","timestamp":"...","open":0,"high":0,"low":0,"close":0,"volume":0}]}
```

#### 行情增量（Go -> Redis）

```json
{"type":"candle","strategy_id":"...","symbol":"BTC/USDT","timestamp":"...","open":0,"high":0,"low":0,"close":0,"volume":0}
```

#### 交易信号（Python -> Redis）

```json
{"strategy_id":"...","owner_id":1,"symbol":"BTC/USDT","action":"open","side":"buy","amount":0.01,"take_profit":0,"stop_loss":0,"signal_id":"...","timestamp":"2026-03-22T12:34:56Z"}
```

## 风控与下单（后端统一执行）

- `max_order_amount` / `min_order_amount`：后端统一裁剪/过滤下单数量
- `max_concurrent_positions`：限制同一策略同时持仓数量（按 open + inflight buy 统计）
- `take_profit` / `stop_loss`：后端下单后启动监控协程，触发则市价平仓

## 核心功能

- [x] **用户系统**: 支持 JWT 认证及管理员权限管理。
- [x] **策略引擎**: Python 进程动态启动与停止，支持多策略并行（Redis 通信）。
- [x] **策略广场**: 支持策略的发布、浏览及一键引用。
- [x] **实时监控**: 通过 WebSocket 实时推送 K 线数据、订单状态及策略日志。
- [x] **平台适配**: 统一的 Exchange 接口，支持快速接入不同交易平台（目前内置 Mock 模拟平台）。

## 技术栈

- **Go**: Gin, GORM, Gorilla WebSocket
- **Python**: Redis PubSub（策略运行无需第三方 redis pip 包）
- **Frontend**: Vite, React, TypeScript, Tailwind CSS, Lucide Icons
- **Database**: SQLite (可扩展至 MySQL)

## 启动

生产部署建议将 Redis 纳入编排，并为后端配置：

```bash
export REDIS_ENABLED=true
export REDIS_ADDR=redis:6379
export REDIS_PREFIX=qt
```

现有 `docker-compose*.yml` 未内置 Redis，需要你在编排里补一个 Redis service（或使用外部 Redis）。
