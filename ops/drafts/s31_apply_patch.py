import re, sys
SC = None  # portable: paths via argv
import sys
src = open(sys.argv[1]).read()  # argv[1]=当轮现拉的main current_code

BLOCK1 = '''
# ─────────────────────────────────────────────────────────────
# S31 @2026-08-31 regime动态方向偏置(owner 08-29动态化直令"搞一个动态的"首件)
# 病根: premium是轮间死数字——NIL案14:48 EMA-up入场14:55闪跌, 3h cron轮回看不见
# 盘面转折; 静态lcp/scp只能靠人轮调。机制: 池内EMA-up宽度(占比)做行情状态机,
# 三档regime→在config premium之上加减方向偏置:
#   RISK_OFF(宽度<0.35): 多头premium+0.10(弱盘反弹单抬门)
#   NEUTRAL(0.35~0.60): 零调整(=纯静态行为)
#   RISK_ON(宽度>0.60):  多头premium-0.05, 空头premium+0.05(顺风降门/逆风抬门)
# 有效premium下限0(门永不低于base MIN_CONFIDENCE); 阈值全部内联防config漂移;
# S31_ENABLED=False即整层退场(回滚开关, config premium静态路径原样保留)。
# 数据源: analyze_with_detail(live路径)逐bar上报各symbol EMA向; 历史重放/backtest
# 不喂(on_market_message的from_history在评估前return; 冷启动=NEUTRAL直到样本≥8)。
# 新鲜度: 30m未更新symbol不计宽度(断流/purge自愈); 新鲜样本<8强制NEUTRAL(薄池不动门)。
S31_ENABLED = True          # S31 False-guard: 关=完全回到静态premium行为
_S31_STALE_SEC = 1800.0     # S31 内联: 宽度样本新鲜窗30m
_S31_MIN_FRESH = 8          # S31 内联: 新鲜样本下限, 不足→NEUTRAL
_S31_OFF_THR = 0.35         # S31 内联: 宽度<此值=RISK_OFF
_S31_ON_THR = 0.60          # S31 内联: 宽度>此值=RISK_ON
_S31_OFF_LONG_ADD = 0.10    # S31 内联: RISK_OFF多头premium增量
_S31_ON_LONG_SUB = 0.05     # S31 内联: RISK_ON多头premium减量
_S31_ON_SHORT_ADD = 0.05    # S31 内联: RISK_ON空头premium增量
_s31_ema_state: dict = {}   # symbol -> (1|0 EMA-up票, time.monotonic上报时刻)


def _s31_note_ema(symbol: str, ema_trend) -> None:
    # S31: live评估路径逐bar上报该symbol当前EMA向(up=1, down/flat=0)。
    try:
        _s31_ema_state[str(symbol)] = (1 if ema_trend == "up" else 0, time.monotonic())
    except Exception:
        pass


def _s31_regime() -> tuple:
    # S31: 返回(regime, 宽度, 新鲜样本数, 多头premium增量, 空头premium增量)。
    # 任何异常→零调整(guard语义: 动态层失效=退回静态行为, 不是策略失效)。
    if not S31_ENABLED:
        return "disabled", -1.0, 0, 0.0, 0.0
    try:
        now = time.monotonic()
        votes = []
        drop = []
        for sym, (up, ts) in list(_s31_ema_state.items()):
            age = now - ts
            if age <= _S31_STALE_SEC:
                votes.append(up)
            elif age > 2 * _S31_STALE_SEC:
                drop.append(sym)
        for sym in drop:
            _s31_ema_state.pop(sym, None)
        n = len(votes)
        if n < _S31_MIN_FRESH:
            return "neutral_thin", -1.0, n, 0.0, 0.0
        breadth = sum(votes) / float(n)
        if breadth < _S31_OFF_THR:
            return "risk_off", breadth, n, _S31_OFF_LONG_ADD, 0.0
        if breadth > _S31_ON_THR:
            return "risk_on", breadth, n, -_S31_ON_LONG_SUB, _S31_ON_SHORT_ADD
        return "neutral", breadth, n, 0.0, 0.0
    except Exception:
        return "error", -1.0, 0, 0.0, 0.0


'''

anchor1 = 'def _calc_tp_sl(price: float, direction: str, atr_abs: float) -> tuple[float, float]:'
assert src.count(anchor1) == 1, "anchor1 not unique"
src = src.replace(anchor1, BLOCK1 + anchor1)

# gate replacement in score_confidence_detail
old_gate = '''    long_thr = Config.MIN_CONFIDENCE + Config.LONG_CONF_PREMIUM
    short_thr = Config.MIN_CONFIDENCE + Config.SHORT_CONF_PREMIUM
    direction: Optional[str] = None'''
new_gate = '''    long_thr = Config.MIN_CONFIDENCE + Config.LONG_CONF_PREMIUM
    short_thr = Config.MIN_CONFIDENCE + Config.SHORT_CONF_PREMIUM
    # S31 @2026-08-31 regime动态偏置叠加于config premium之上(机制注释见模块级S31块);
    # 有效premium下限0=门永不低于base; disabled/neutral_thin/异常→零调整=原静态行为。
    _s31_reg, _s31_breadth, _s31_n, _s31_ladj, _s31_sadj = _s31_regime()
    if _s31_ladj != 0.0 or _s31_sadj != 0.0:
        long_thr = Config.MIN_CONFIDENCE + max(0.0, Config.LONG_CONF_PREMIUM + _s31_ladj)
        short_thr = Config.MIN_CONFIDENCE + max(0.0, Config.SHORT_CONF_PREMIUM + _s31_sadj)
    direction: Optional[str] = None'''
assert src.count(old_gate) == 1, "gate not unique"
src = src.replace(old_gate, new_gate)

# detail dict in score_confidence_detail (followed by "    }\n    return float(conf)")
old_d = '''        "short_conf_premium": float(Config.SHORT_CONF_PREMIUM),
        "short_threshold": float(Config.MIN_CONFIDENCE + Config.SHORT_CONF_PREMIUM),
        "long_conf_premium": float(Config.LONG_CONF_PREMIUM),
        "long_threshold": float(Config.MIN_CONFIDENCE + Config.LONG_CONF_PREMIUM),
    }
    return float(conf), direction, detail'''
new_d = '''        "short_conf_premium": float(Config.SHORT_CONF_PREMIUM),
        "short_threshold": float(short_thr),
        "long_conf_premium": float(Config.LONG_CONF_PREMIUM),
        "long_threshold": float(long_thr),
        "s31_regime": _s31_reg,
        "s31_breadth": float(_s31_breadth),
        "s31_fresh_n": int(_s31_n),
        "s31_long_adj": float(_s31_ladj),
        "s31_short_adj": float(_s31_sadj),
    }
    return float(conf), direction, detail'''
assert src.count(old_d) == 1, "detail dict not unique"
src = src.replace(old_d, new_d)
# note: S31后 short_threshold/long_threshold 上报=真实gate值(E2观测偏差教训)

# empty_score_detail: schema consistency keys
old_e = '''        "long_threshold": float(Config.MIN_CONFIDENCE + Config.LONG_CONF_PREMIUM),
        "reason": _s(reason),'''
new_e = '''        "long_threshold": float(Config.MIN_CONFIDENCE + Config.LONG_CONF_PREMIUM),
        "s31_regime": "n/a",
        "s31_breadth": -1.0,
        "s31_fresh_n": 0,
        "s31_long_adj": 0.0,
        "s31_short_adj": 0.0,
        "reason": _s(reason),'''
assert src.count(old_e) == 1, "empty detail not unique"
src = src.replace(old_e, new_e)

# updater call in analyze_with_detail (unique via min_volatility+empty_score_detail return above)
old_u = '''        return _no_signal(snapshot, f"波动不足{Config.MIN_VOLATILITY:.1f}%"), empty_score_detail("min_volatility")

    rsi_val = calc_rsi_pd(closes)
    macd_val, macd_sig = calc_macd_pd(closes)
    _, bb_upper, bb_lower = calc_bollinger_pd(closes)
    atr_pct = calc_atr_pct_pd(highs, lows, closes)
    atr_abs = calc_atr_abs(highs, lows, closes)
    ema_trend = calc_ema_trend(closes, Config.EMA_FAST, Config.EMA_SLOW)'''
new_u = old_u + '''
    _s31_note_ema(snapshot.symbol, ema_trend)  # S31: live路径上报EMA向(宽度regime数据源)'''
assert src.count(old_u) == 1, "updater site not unique"
src = src.replace(old_u, new_u)

open(sys.argv[2],"w").write(src)  # argv[2]=输出候选文件
print("patched OK, lines:", src.count(chr(10))+1)
