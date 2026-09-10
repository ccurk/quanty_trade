package marketmaker

// Gate.io WebSocket 交易通道 (wss://api.gateio.ws/ws/v4/)。动机: REST 下单每笔吃
// 一次 HTTP 请求开销(空闲逐出后还要重付 TLS 握手), 做市 requote 高频撤/挂对时延敏感;
// WS 保持一条已登录长连接, 下单=一帧一 ack。协议 (Gate APIv4 WS, 与官方 SDK 逐字段核对):
//
//   请求信封: {"time":<unix秒>,"channel":"spot.order_place","event":"api",
//              "payload":{"req_id":..,"api_key":..,"req_param":<obj>,
//                         "timestamp":"<unix秒>","signature":<hex>}}
//   签名串:   "api\n{channel}\n{JSON(req_param)}\n{time}" 的 HMAC-SHA512 hex。
//   ⚠️ 签名里的 req_param JSON 字节必须与信封中发送的字节完全一致——这里 Marshal 一次
//   得 RawMessage, 签名与信封共用同一份字节; login 的 req_param 是空字符串 "" (含引号)。
//   应答信封: {"request_id":..,"header":{"status":"200",...},"data":{"result":..,"errs":..}}
//   status!="200" 即失败; 关联键=request_id。心跳=spot.ping(无 ack 要求)。
//
// 回退语义 (幂等安全, 这是本文件最重要的不变式):
//   - PlaceLimit: 仅在【发送前】失败(未连接/登录失败/写失败前)才回退 REST;
//     发送成功但 ack 超时 → 返回错误, 绝不盲目 REST 重试——ack 丢失时订单可能已被
//     交易所接受, 重试=双挂单。engine 对下单失败的处置本就是跳过本轮, 下个
//     refresh 周期会重新对账挂单 (OpenOrders + requote), 天然自愈。
//   - CancelOrder: 撤单幂等, WS 失败(任何阶段)一律 REST 兜底重试。
//   - ws_trade=false(默认) 整个通道不启用, 行为与旧版逐字节相同 = 回滚路径。

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"quanty_trade/internal/logger"
)

const (
	gateWSTradeURL     = "wss://api.gateio.ws/ws/v4/"
	gateWSDialTimeout  = 5 * time.Second
	gateWSAckTimeout   = 5 * time.Second
	gateWSPingEvery    = 15 * time.Second
	gateWSReadDeadline = 60 * time.Second
	gateWSRedialMinGap = 2 * time.Second
)

type gateWSPayload struct {
	ReqID     string          `json:"req_id"`
	APIKey    string          `json:"api_key"`
	ReqParam  json.RawMessage `json:"req_param"`
	Timestamp string          `json:"timestamp"`
	Signature string          `json:"signature"`
}

type gateWSRequest struct {
	Time    int64         `json:"time"`
	Channel string        `json:"channel"`
	Event   string        `json:"event"`
	Payload gateWSPayload `json:"payload"`
}

// gateWSAckHeader 是应答信封的 header。
//
// 【为什么多了三个限流字段(台账 #197)】原来这里只解 status/channel/event,把
// gate 每一条应答都带着的限流三件套整个丢掉了。后果是"我们现在是哪一档限流"这个
// 问题在本地【无法回答】—— 只能靠翻两万多条日志考古反推。它们回来之后:
//   - 当前档位是 grep 一行的事;
//   - 限流器可以按交易所报的【真实剩余】自适应,不再靠我们猜的常量
//     (gate_ratelimit.go observeRemote)。
//
// ⚠️ 字段名以官方 WS 文档为准:下划线、全小写(不是 HTTP 头那种连字符)。
// reset 那个字段官方文档里写作 x_gat_ratelimit_reset_timestamp(少一个 e,
// 看着像官方的笔误),所以两种拼法都收 —— 它们不会同时出现,谁有用谁。
// 【未核实】我没有在实盘应答上亲眼验证过这三个字段真的会出现;解不到就是零值,
// hint.OK=false,限流器原样退回本地配置常量,不会因此变坏。
type gateWSAckHeader struct {
	Status  string `json:"status"`
	Channel string `json:"channel"`
	Event   string `json:"event"`

	RateLimitLimit  string `json:"x_gate_ratelimit_limit"`
	RequestsRemain  string `json:"x_gate_ratelimit_requests_remain"`
	ResetTimestamp  string `json:"x_gate_ratelimit_reset_timestamp"`
	ResetTimestamp2 string `json:"x_gat_ratelimit_reset_timestamp"`
}

// rateLimitHint 把 header 里那三个字段解成限流器能吃的形状;没带就是 OK=false。
func (h gateWSAckHeader) rateLimitHint() gateRateLimitHint {
	reset := h.ResetTimestamp
	if reset == "" {
		reset = h.ResetTimestamp2
	}
	return parseGateRateLimitHint(h.RateLimitLimit, h.RequestsRemain, reset)
}

type gateWSAck struct {
	RequestID string          `json:"request_id"`
	Header    gateWSAckHeader `json:"header"`
	Data      struct {
		Result json.RawMessage `json:"result"`
		Errs   *struct {
			Label   string `json:"label"`
			Message string `json:"message"`
		} `json:"errs"`
	} `json:"data"`
}

// gateSignWS 构造 "api\n{channel}\n{reqParamJSON}\n{time}" 的 HMAC-SHA512 hex 签名。
// 独立成包级函数以便单测钉死跨语言向量(见 gate_ws_test.go)。
func gateSignWS(secret, channel string, reqParamJSON []byte, ts int64) string {
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write([]byte("api\n" + channel + "\n" + string(reqParamJSON) + "\n" + strconv.FormatInt(ts, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

type gateWSTrader struct {
	wsURL  string
	apiKey string
	secret string

	mu        sync.Mutex // 守护 conn/loggedIn/lastDial/pending 的建立与失效
	conn      *websocket.Conn
	loggedIn  bool
	lastDial  time.Time
	writeMu   sync.Mutex    // gorilla 单写者约束
	reqSeq    atomic.Uint64 // sendLocked 会被无 t.mu 的 request 路径并发调用,必须原子
	pendingMu sync.Mutex
	pending   map[string]chan gateWSAck
	closed    chan struct{} // 当前连接的 reader 存活信号(每次重连换新)
}

func newGateWSTrader(wsURL, apiKey, secret string) *gateWSTrader {
	if wsURL == "" {
		wsURL = gateWSTradeURL
	}
	return &gateWSTrader{wsURL: wsURL, apiKey: apiKey, secret: secret, pending: map[string]chan gateWSAck{}}
}

// ensureConn 惰性建连+登录; 返回错误时保证【没有任何请求被发出】(回退 REST 安全)。
func (t *gateWSTrader) ensureConn() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conn != nil && t.loggedIn {
		return nil
	}
	if since := time.Since(t.lastDial); since < gateWSRedialMinGap {
		return fmt.Errorf("gatews: redial suppressed (%.1fs since last attempt)", since.Seconds())
	}
	t.lastDial = time.Now()
	t.teardownLocked()

	u, err := url.Parse(t.wsURL)
	if err != nil {
		return fmt.Errorf("gatews: bad url: %w", err)
	}
	dialer := websocket.Dialer{HandshakeTimeout: gateWSDialTimeout}
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("gatews: dial: %w", err)
	}
	t.conn = conn
	t.closed = make(chan struct{})
	go t.readLoop(conn, t.closed)
	go t.pingLoop(conn, t.closed)

	// 登录 (req_param = 空字符串 "", 签名串里也是带引号的 "" —— 与信封字节一致)
	ackCh, reqID, err := t.sendLocked(conn, "spot.login", json.RawMessage(`""`))
	if err != nil {
		t.teardownLocked()
		return fmt.Errorf("gatews: login send: %w", err)
	}
	select {
	case ack := <-ackCh:
		if ack.Header.Status != "200" {
			t.teardownLocked()
			return fmt.Errorf("gatews: login rejected status=%s errs=%v", ack.Header.Status, ack.Data.Errs)
		}
	case <-time.After(gateWSAckTimeout):
		t.dropPending(reqID)
		t.teardownLocked()
		return fmt.Errorf("gatews: login ack timeout")
	}
	t.loggedIn = true
	logger.Infof("[mm-gatews] connected+logged in %s", t.wsURL)
	return nil
}

// teardownLocked 关闭当前连接并让所有 pending 立刻失败。调用方须持有 t.mu。
func (t *gateWSTrader) teardownLocked() {
	if t.conn != nil {
		_ = t.conn.Close()
		t.conn = nil
	}
	t.loggedIn = false
	if t.closed != nil {
		select {
		case <-t.closed: // reader 已退出
		default:
			close(t.closed)
		}
		t.closed = nil
	}
	t.pendingMu.Lock()
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
	t.pendingMu.Unlock()
}

func (t *gateWSTrader) dropPending(reqID string) {
	t.pendingMu.Lock()
	delete(t.pending, reqID)
	t.pendingMu.Unlock()
}

// sendLocked 组帧+签名+写出一个 api 请求, 返回 ack 通道。调用方须已持有 t.mu
// (登录路径)或保证 conn 有效(request 路径经 mu 取得快照)。
func (t *gateWSTrader) sendLocked(conn *websocket.Conn, channel string, reqParam json.RawMessage) (chan gateWSAck, string, error) {
	now := time.Now().Unix()
	reqID := "qt-" + strconv.FormatInt(now, 10) + "-" + strconv.FormatUint(t.reqSeq.Add(1), 10)
	req := gateWSRequest{
		Time:    now,
		Channel: channel,
		Event:   "api",
		Payload: gateWSPayload{
			ReqID:     reqID,
			APIKey:    t.apiKey,
			ReqParam:  reqParam,
			Timestamp: strconv.FormatInt(now, 10),
			Signature: gateSignWS(t.secret, channel, reqParam, now),
		},
	}
	frame, err := json.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	ackCh := make(chan gateWSAck, 1)
	t.pendingMu.Lock()
	t.pending[reqID] = ackCh
	t.pendingMu.Unlock()

	t.writeMu.Lock()
	_ = conn.SetWriteDeadline(time.Now().Add(gateWSDialTimeout))
	err = conn.WriteMessage(websocket.TextMessage, frame)
	t.writeMu.Unlock()
	if err != nil {
		t.dropPending(reqID)
		return nil, "", err
	}
	return ackCh, reqID, nil
}

// request 在已登录连接上发一个 api 请求并等 ack。
// sentOut=true 表示帧已写出(此后失败不可回退 REST——订单可能已在交易所)。
func (t *gateWSTrader) request(channel string, reqParam json.RawMessage) (ack gateWSAck, sentOut bool, err error) {
	if err := t.ensureConn(); err != nil {
		return gateWSAck{}, false, err
	}
	t.mu.Lock()
	conn, ok := t.conn, t.loggedIn
	t.mu.Unlock()
	if conn == nil || !ok {
		return gateWSAck{}, false, fmt.Errorf("gatews: connection lost before send")
	}
	ackCh, reqID, err := t.sendLocked(conn, channel, reqParam)
	if err != nil {
		// 写失败: 帧未完整送达对端的概率极高, 但保守起见按"已可能送达"处理仍过僵——
		// gorilla WriteMessage 报错=本地写故障, 帧未被确认写入内核, 判定未发出。
		t.markBroken(conn)
		return gateWSAck{}, false, err
	}
	select {
	case got, chOK := <-ackCh:
		if !chOK {
			return gateWSAck{}, true, fmt.Errorf("gatews: connection closed awaiting ack")
		}
		return got, true, nil
	case <-time.After(gateWSAckTimeout):
		t.dropPending(reqID)
		return gateWSAck{}, true, fmt.Errorf("gatews: ack timeout on %s", channel)
	}
}

// markBroken 使指定连接失效(若仍是当前连接), 下次调用触发惰性重连。
func (t *gateWSTrader) markBroken(conn *websocket.Conn) {
	t.mu.Lock()
	if t.conn == conn {
		t.teardownLocked()
	}
	t.mu.Unlock()
}

// isPlaceEchoFrame 判定 spot.order_place 的两段式应答中的第一帧(回执):status=200
// 且 data.result 为【请求回显】(带 req_param 字段)。实测(2026-08-25 探针):下单先回
// 一帧请求回显,+3ms 后第二帧才是真结果(订单对象或 errs);撤单/登录则单帧直返。
// 判别必须限定 channel:login 的单帧应答同样含回显字段,不能被跳过。
func isPlaceEchoFrame(ack gateWSAck) bool {
	if ack.Header.Channel != "spot.order_place" || len(ack.Data.Result) == 0 {
		return false
	}
	var probe struct {
		ReqParam json.RawMessage `json:"req_param"`
	}
	return json.Unmarshal(ack.Data.Result, &probe) == nil && probe.ReqParam != nil
}

func (t *gateWSTrader) readLoop(conn *websocket.Conn, closed chan struct{}) {
	defer t.markBroken(conn)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(gateWSReadDeadline))
		_, data, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-closed:
			default:
				logger.Warnf("[mm-gatews] read loop exit: %v", err)
			}
			return
		}
		var ack gateWSAck
		if err := json.Unmarshal(data, &ack); err != nil || ack.RequestID == "" {
			continue // 非 api-ack 帧(ping 回显/订阅推送等)直接略过
		}
		if isPlaceEchoFrame(ack) {
			continue // order_place 的回执帧(请求回显),真结果在下一帧 —— pending 保留继续等
		}
		t.pendingMu.Lock()
		ch, ok := t.pending[ack.RequestID]
		if ok {
			delete(t.pending, ack.RequestID)
		}
		t.pendingMu.Unlock()
		if ok {
			ch <- ack
		}
	}
}

func (t *gateWSTrader) pingLoop(conn *websocket.Conn, closed chan struct{}) {
	tick := time.NewTicker(gateWSPingEvery)
	defer tick.Stop()
	for {
		select {
		case <-closed:
			return
		case <-tick.C:
			frame := []byte(fmt.Sprintf(`{"time":%d,"channel":"spot.ping"}`, time.Now().Unix()))
			t.writeMu.Lock()
			_ = conn.SetWriteDeadline(time.Now().Add(gateWSDialTimeout))
			err := conn.WriteMessage(websocket.TextMessage, frame)
			t.writeMu.Unlock()
			if err != nil {
				t.markBroken(conn)
				return
			}
		}
	}
}

// PlaceLimit 走 spot.order_place。返回 (orderID, hint, sentOut, err):
// sentOut=false 且 err!=nil ⇒ 调用方可安全回退 REST。
// hint 是交易所报的限流档位/剩余,【失败路径也回】—— 被拒的那一发带的档位信息
// 恰恰最值钱(它就是"你现在被限到哪一档"的直接答案)。
func (t *gateWSTrader) PlaceLimit(body map[string]string) (string, gateRateLimitHint, bool, error) {
	var noHint gateRateLimitHint
	reqParam, err := json.Marshal(body)
	if err != nil {
		return "", noHint, false, err
	}
	start := time.Now()
	ack, sentOut, err := t.request("spot.order_place", reqParam)
	if err != nil {
		return "", noHint, sentOut, err
	}
	hint := ack.Header.rateLimitHint()
	if ack.Header.Status != "200" {
		if ack.Data.Errs != nil {
			return "", hint, true, fmt.Errorf("gatews order_place %s: %s", ack.Data.Errs.Label, ack.Data.Errs.Message)
		}
		return "", hint, true, fmt.Errorf("gatews order_place status=%s", ack.Header.Status)
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(ack.Data.Result, &r); err != nil || r.ID == "" {
		return "", hint, true, fmt.Errorf("gatews order_place: ack ok but no order id (%s)", string(ack.Data.Result))
	}
	// 档位/剩余进日志:这一行就是"当前是哪一档"的可 grep 答案,
	// 不必再从两万多条日志里反推(台账 #197)。
	logger.Infof("[mm-gatews] order_place %s %s ok id=%s rtt=%dms ratelimit=%s/%s reset=%s",
		body["currency_pair"], body["side"], r.ID, time.Since(start).Milliseconds(),
		nz(ack.Header.RequestsRemain), nz(ack.Header.RateLimitLimit), nz(ack.Header.ResetTimestamp))
	return r.ID, hint, true, nil
}

// nz 把空字段打成 "-",免得日志里出现 "ratelimit=/" 这种看不出是"没带"还是"是 0"的行。
func nz(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// CancelOrder 走 spot.order_cancel; 任何失败调用方都可 REST 兜底(撤单幂等)。
// 同样把限流 hint 交回去 —— 撤单花的是撤单池,它报的档位属于那个池。
func (t *gateWSTrader) CancelOrder(orderID, currencyPair string) (gateRateLimitHint, error) {
	var noHint gateRateLimitHint
	reqParam, err := json.Marshal(map[string]string{"order_id": orderID, "currency_pair": currencyPair})
	if err != nil {
		return noHint, err
	}
	ack, _, err := t.request("spot.order_cancel", reqParam)
	if err != nil {
		return noHint, err
	}
	hint := ack.Header.rateLimitHint()
	if ack.Header.Status != "200" {
		if ack.Data.Errs != nil {
			return hint, fmt.Errorf("gatews order_cancel %s: %s", ack.Data.Errs.Label, ack.Data.Errs.Message)
		}
		return hint, fmt.Errorf("gatews order_cancel status=%s", ack.Header.Status)
	}
	return hint, nil
}
