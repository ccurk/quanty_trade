# Majors_BTC_ETH_BNB_SOL (e725e31a-0a10-4393-9ae7-1f4a82ca31ce)

- 载具: owner 09-19 06:5x 直令新建的主流币实例(BTC/ETH/BNB/SOL, 后 owner UI 加 XRP); 谱系 = main tpl998 fork。
- tpl1056.py (2026-09-19 08:39:38Z apply, hash 766b576a) = tpl998 + **S35 @2026-09-19 bar聚合**:
  config `bar_agg_minutes`(默认 1=关, 本壳 15) → 1m K线合成 15m bar 后再算指标/信号(桶=floor(open_ms/900s), 与交易所 15m K线对齐);
  影子跟单仍按 1m 判 TP/SL; 启动直拉 15m K线 200 根预热(fapi→data-api.binance.vision 兜底; 回测 backtest=true 不喂);
  预热失败退化为慢预热(WARMUP_BARS×15m)。统计行新增 `S35聚合(封口bar/种子)`; START 行新增 `bar_agg=`。
- 病灶实证: 08:03-08:20 三笔全 SL(ETH −0.14/BNB −0.15/XRP −0.49), 交易所 SL 距 0.082/0.085/0.216% vs taker 来回 0.1%。
- 回滚: POST /api/strategies/e725e31a/rollback → tpl998(previous_template_id) 或 config bar_agg_minutes=1 + 重启。
- 离线验证: scratch s35_harness.py 6/6 (1000×1m→66 bar OHLCV 精确; ISO Z/+08:00/ms/ns/秒 同桶; 种子桶去重+乱序丢弃; bar_agg=1 路径与旧码零差异; vision 兜底 199 根/2.4s; backtest 跳过种子)。
