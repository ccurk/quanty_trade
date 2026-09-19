# Sandbox_备用池模拟_勿启动 (ce84012b-c356-4e6a-a53c-1342fcf4262a)

- 用途: owner 09-19 直令"备用池不参与交易, 用策略模拟下单"的模拟载具; 永不 start, 只跑 POST /backtest。
- tpl1055.py = main tpl998 + S34 sim-clock 锚点(回测时 cooldown/急再入veto/影子/CB/S31/S32/择优窗改用 K 线时间; live 模式零差异)。
- 验证: 09-19 07:2x 对 G/BULLA/MAGMA 18h 1m 回测(sim_clock=true) vs 无 S34 基线(每币仅 1 笔)。
