# 系统调用链路（QuantyTrade 当前架构）

本文档描述当前代码状态下的关键调用链路（后端 Go + Redis + Python 策略 + 前端 WebSocket），用于快速理解“数据从哪里来、怎么流转、在哪里落库/广播”。

## 0. 全局组件与入口

**后端进程入口**

- 入口： [main.go](file:///Users/black/basis/quanty_trade/backend/cmd/main.go#L69-L191)
- 启动顺序（简化）：
  - `conf.MustLoad()`：加载配置 + 环境变量覆盖
  - `database.InitDB()`：连接 DB + AutoMigrate + 初始化管理员
  - 初始化 Gin 中间件（Trace/APILogger/CORS/Auth）
  - `hub := ws.NewHub(); go hub.Run()`：启动后端的 WebSocket 广播中心
  - `ex := exchange.NewBinanceExchange()` 或 `MockExchange`
  - `mgr := strategy.NewManager(hub, ex)`
  - 如果 `redis.enabled=true`：`rb := bus.NewRedisBusFromConfig(); mgr.SetRedisBus(rb)`
  - `mgr.SyncFromDB(database.DB)`：从 DB 同步策略实例到内存管理器
  - 注册 API 路由 + `/ws` 升级 WebSocket

**WebSocket Hub**

- 实现：[hub.go](file:///Users/black/basis/quanty_trade/backend/internal/ws/hub.go#L16-L105)
- 语义：后端任意模块调用 `hub.BroadcastJSON(...)`，会广播给所有已连接的前端 `/ws` 客户端。

**Redis Bus（策略消息总线）**

- 实现：[redis_bus.go](file:///Users/black/basis/quanty_trade/backend/internal/bus/redis_bus.go#L14-L192)
- 通道（以 `REDIS_PREFIX=qt` 为例）：
  - 行情：`qt:candle:{strategy_id}`（Go -> Redis -> Python）
  - 信号：`qt:signal:{strategy_id}`（Python -> Redis -> Go）
  - 状态：`qt:state:{strategy_id}`（Python -> Redis -> Go）

## 1. 用户访问后端（HTTP API）

HTTP API 路由集中注册在：
- [main.go](file:///Users/black/basis/quanty_trade/backend/cmd/main.go#L120-L170)

典型链路：

1) 前端登录
- `POST /api/login` → [handlers.go](file:///Users/black/basis/quanty_trade/backend/internal/api/handlers.go)
- 登录成功后前端持有 JWT，后续请求通过 `AuthMiddleware()` 校验。

2) 策略生命周期
- `POST /api/strategies/:id/start` → `api.StartStrategy` → `mgr.StartStrategy(id)`
- `POST /api/strategies/:id/stop` → `api.StopStrategy` → `mgr.StopStrategy(id, force)`

## 2. 启动策略（Go 管理 Python 子进程）

入口函数：
- [StartStrategy](file:///Users/black/basis/quanty_trade/backend/internal/strategy/manager.go#L315-L545)

### 2.1 子进程启动参数

后端会把策略配置整理成一个 JSON（`runCfg`），并以命令行参数形式传给 Python：

```text
python3 <abs_path_to_strategy.py> "<json_config>"
```

其中会自动注入：
- `strategy_id` / `owner_id`
- `redis_addr` / `redis_password` / `redis_db` / `redis_prefix`
- `use_redis=true`
- `healthcheck`（默认开启，interval=5s，timeout=20s，ready_grace=30s）

### 2.2 订阅 Redis：信号与状态

StartStrategy 成功后，会在 Go 里对该 `strategy_id` 启动两个订阅：

- 订阅信号：`SubscribeSignals(ctx, strategy_id, handler)`
  - Redis 通道：`{prefix}:signal:{strategy_id}`
  - handler：`m.handleRedisSignal(inst, s)`
  - 代码位置：[handleRedisSignal](file:///Users/black/basis/quanty_trade/backend/internal/strategy/manager.go#L939-L1004)

- 订阅状态：`SubscribeState(ctx, strategy_id, handler)`
  - Redis 通道：`{prefix}:state:{strategy_id}`
  - 主要处理两类消息：
    - `ready`：记录 `boot_id`，触发历史同步（全量 200 根）
    - `heartbeat`：刷新 `lastHB` 用于健康检查

### 2.3 行情订阅（交易所 WS → Go → Redis）

当策略配置里包含 `symbol/symbols` 时：

1) Go 对每个交易对调用 `exchange.SubscribeCandles(sym, callback)`
2) callback 每次只接收“收盘K线”（以 Binance 为例，kline `x=true` 才触发）
3) callback 内会做两件事：
   - `redisBus.PublishCandle(...)`：推送增量 candle 给 Python
   - `hub.BroadcastJSON(...)`：推送给前端实时展示

代码位置（策略侧订阅入口）：
- [StartStrategy 行情订阅循环](file:///Users/black/basis/quanty_trade/backend/internal/strategy/manager.go#L458-L544)

### 2.4 首次全量历史同步（Go → Redis）

触发条件：
- Python 启动后发布 `ready`（带 `boot_id`），Go 在订阅 state 里收到后会将 `inst.resync=true`
- `historySyncLoop` 看到 `resync=true && boot_id!=empty` 时，拉取并发布历史

实现：
- 拉取历史：`inst.exchange.FetchCandles(sym, "1m", 200)`
- 发布历史：`redisBus.PublishHistory(...)`（消息类型 `history`，发到 candle channel）

代码位置：
- [historySyncLoop](file:///Users/black/basis/quanty_trade/backend/internal/strategy/manager.go#L755-L807)

## 3. Binance 行情 WS 如何“推送过来”，在哪里接收

### 3.1 接口层（Exchange 抽象）

- 接口定义： [Exchange](file:///Users/black/basis/quanty_trade/backend/internal/exchange/exchange.go#L86-L106)
- 行情订阅函数：`SubscribeCandles(symbol, callback) (stop func(), err error)`

### 3.2 Binance 实现（Kline 1m）

- 实现位置： [binance.go:SubscribeCandles](file:///Users/black/basis/quanty_trade/backend/internal/exchange/binance.go#L1091-L1165)

链路：
- Go 侧 Dial：`websocket.DefaultDialer.Dial(wsURL, nil)`
- Go 侧接收：循环 `conn.ReadMessage()` 获取 payload
- 解析 `payload.K`：
  - `payload.K.X == true` 表示该分钟 K线已收盘
  - 当前实现只在收盘时调用 callback（避免未收盘中间数据频繁波动）
- callback 交给 Strategy Manager 做 Redis 推送与前端广播（见 2.3）

### 3.3 stop（取消订阅）

`SubscribeCandles` 会返回 `stop()`，StopStrategy 时会调用 stop，确保行情 WS 连接被关闭，避免策略多次启动导致重复订阅。

相关代码：
- stop 返回：[binance.go](file:///Users/black/basis/quanty_trade/backend/internal/exchange/binance.go#L1091-L1165)
- stop 调用：[StopStrategy](file:///Users/black/basis/quanty_trade/backend/internal/strategy/manager.go#L664-L753)

## 4. Python 策略进程如何消费数据、如何发信号

模板策略：
- [redis_signal_template.py](file:///Users/black/basis/quanty_trade/strategies/redis_signal_template.py)

### 4.1 Python 启动与状态上报

启动后：
- `subscribe(candle_channel)`
- `publish(state_channel, {"type":"ready", "boot_id":...})`
- 后台线程定时 `publish(state_channel, {"type":"heartbeat", ...})`

### 4.2 消费行情（history + candle）

Python 在 `on_candle` 里：
- 如果 `type=="history"`：遍历 `candles[]` 并递归按单条 candle 处理（用于重建记忆）
- 如果 `type=="candle"`：按单条增量更新 closes/指标

### 4.3 发布交易信号（Python → Redis）

Python 通过 `publish(signal_channel, msg)` 发出：
- `action=open`
- `side=buy/sell`
- `amount`
- `take_profit` / `stop_loss`

Go 订阅到信号后进入 `handleRedisSignal`（见 2.2），最终进入下单逻辑。

## 5. 下单与仓位监控（Go）

链路：

1) Python 发布信号 → Redis `signal` channel
2) Go `SubscribeSignals` 收到 → `handleRedisSignal`
3) 过滤：
   - symbol 是否属于该策略允许范围（`symbol/symbols`）
   - action/side 合法性
   - amount clamp（min/max 等配置）
4) `placeOrderForInstance` 下单并落库（StrategyOrder/Position 等）
5) 如携带 TP/SL：启动 `monitorPositionTPStop` 协程轮询触发平仓

代码位置：
- [handleRedisSignal](file:///Users/black/basis/quanty_trade/backend/internal/strategy/manager.go#L939-L1004)
- [monitorPositionTPStop](file:///Users/black/basis/quanty_trade/backend/internal/strategy/manager.go#L914-L1018)

## 6. Binance 账户/订单回报 WS（User Data Stream）

这条链路用于接收 `executionReport` 等事件（主要影响 UI 展示与事件落库），与行情 kline 是两套 WS。

入口：
- Strategy 启动时，如果 exchange 实现了 `EnsureUserDataStream`，会调用一次
  - 调用点：[StartStrategy](file:///Users/black/basis/quanty_trade/backend/internal/strategy/manager.go#L452-L457)

实现：
- [binance_user_stream.go](file:///Users/black/basis/quanty_trade/backend/internal/exchange/binance_user_stream.go#L30-L220)
- 关键步骤：
  - REST 创建 listenKey：`POST /api/v3/userDataStream`
  - WS 连接：`/ws/{listenKey}`
  - `readUserStream` 循环 `conn.ReadMessage()`，解析 `e` 字段并处理 `executionReport`

## 7. 健康检查与自动重启（每个 Python 进程默认开启）

目标：每个策略进程独立健康检查，默认开启。

Go 侧：
- 监听 `state` 通道的 `ready/heartbeat`，维护 `lastHB/bootID/startedAt`
- 健康检查规则：
  - 启动后 30s 仍未收到 `ready` → 重启
  - 收到 `ready` 后心跳超过 20s 未更新 → 重启
  - 进程退出（cmd.Wait 返回）→ 重启

代码位置：
- [healthMonitorLoop / waitProcessLoop / requestRestart](file:///Users/black/basis/quanty_trade/backend/internal/strategy/manager.go#L812-L938)

Python 侧：
- 模板默认发送 `ready` + 周期 `heartbeat`
- 代码位置：[redis_signal_template.py](file:///Users/black/basis/quanty_trade/strategies/redis_signal_template.py#L90-L167)

