package marketmaker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// 2026-09-16 回归测试：做市「整晚零报价」的两个独立缺陷。
//
// 现场数据（20 分钟窗口，全部实测自容器日志，非推测）：
//   connected+logged in          1 次   → WS 从未重连过
//   cancel: WS failed            234 次 → 每一次撤单都 ack 超时
//   mm-quote 成功                0 次   → 一个 pair 都没挂上单
//   read loop exit               0 次   → 读循环从未退出（TCP/心跳是活的）
//
// 两个缺陷互为因果，缺一个都不会造成永久停摆：
//   缺陷 A（根因）ack 超时不拆连接 → 死连接被无限复用，故障永不收敛
//   缺陷 B（放大器）404「订单已不存在」被计成撤单失败 → pair 永久不恢复报价

// 缺陷 A：ack 永不到达时，连接必须被拆掉，好让下一次调用重连并重新登录。
//
// 修复前 request() 的超时分支只 dropPending 就返回，唯独漏了 markBroken ——
// 而写失败那条分支是有的。同一个语义（这一发没做成）两条分支待遇不一致，
// 于是「心跳活着但 api 已死」的连接被无限复用。
func TestGateWSAckTimeoutTearsDownConn(t *testing.T) {
	// 假 gate：login 正常应答，此后对任何 api 请求一律沉默。
	// 这精确复现现场 —— 连接在 TCP 与心跳层面都健康，只有 ack 停摆。
	var frames int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			var req gateWSRequest
			if json.Unmarshal(data, &req) != nil {
				continue
			}
			atomic.AddInt32(&frames, 1)
			if req.Channel == "spot.login" {
				// 手搓 JSON 而不是 marshal gateWSAck：不依赖应答体结构的精确形状，
				// 测的是超时语义，不是解析。
				if err := c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(
					`{"request_id":%q,"header":{"channel":"spot.login","status":"200"},"data":{"result":""}}`,
					req.Payload.ReqID))); err != nil {
					return
				}
			}
			// 非 login：一律不回，ack 永远不来
		}
	}))
	defer srv.Close()

	tr := newGateWSTrader("ws"+strings.TrimPrefix(srv.URL, "http"), "k", "s")

	start := time.Now()
	_, _, err := tr.request("spot.order_cancel", json.RawMessage(`{"id":"1"}`))
	elapsed := time.Since(start)

	// 先证伪「压根没发出去」这个替代解释，否则下面的超时断言毫无意义。
	if n := atomic.LoadInt32(&frames); n < 2 {
		t.Fatalf("假 gate 只收到 %d 帧，应至少 login + cancel 两帧 —— "+
			"请求没真正发出去，超时断言会失去意义", n)
	}

	if err == nil {
		t.Fatal("ack 永不到达时必须返回错误，不能假装成功")
	}
	if !strings.Contains(err.Error(), "ack timeout") {
		t.Fatalf("错误应是 ack timeout，得到: %v", err)
	}
	if elapsed < gateWSAckTimeout {
		t.Fatalf("提前返回了（%.1fs < %s），没走到超时分支", elapsed.Seconds(), gateWSAckTimeout)
	}

	tr.mu.Lock()
	conn, loggedIn := tr.conn, tr.loggedIn
	tr.mu.Unlock()
	if conn != nil || loggedIn {
		t.Fatalf("ack 超时后连接必须被拆掉（下次调用才会重连+重新登录）；"+
			"实际 conn!=nil=%v loggedIn=%v —— 这正是永久停摆的成因",
			conn != nil, loggedIn)
	}
}

// 缺陷 B：404 ORDER_NOT_FOUND 必须被判定为「订单已不存在」= 撤单成功。
func TestCancelIsAlreadyGone(t *testing.T) {
	notFound := &gateHTTPError{
		Status: http.StatusNotFound, Method: "DELETE",
		Path: "/spot/orders/1132521944761", Body: `{"label":"ORDER_NOT_FOUND"}`,
	}
	if !cancelIsAlreadyGone(notFound) {
		t.Fatal("404 必须被判定为「订单已不存在」")
	}
	if !cancelIsAlreadyGone(fmt.Errorf("上下文中转: %w", notFound)) {
		t.Fatal("经 fmt.Errorf %w 包装后仍须判定成功（signed 的调用链会包）")
	}

	// 这些都不是「已不存在」，绝不能被当成撤单成功 —— 否则就是真的把裸露挂单
	// 当成撤干净了，那是拿钱冒险的方向。
	for _, code := range []int{400, 401, 403, 429, 500, 502, 503} {
		if cancelIsAlreadyGone(&gateHTTPError{Status: code}) {
			t.Fatalf("HTTP %d 不是「已不存在」，不能被当成撤单成功", code)
		}
	}
	if cancelIsAlreadyGone(nil) {
		t.Fatal("nil 错误不能判定为已不存在")
	}

	// 只有带状态码的类型算证据。字符串匹配不算 —— 它会在格式一改时静默失效。
	if cancelIsAlreadyGone(fmt.Errorf("gate DELETE /spot/orders/1 -> 404: x")) {
		t.Fatal("普通字符串错误不能被误判；判据必须是类型里的状态码，不是错误串")
	}
}

// 错误串格式必须与改造前的 fmt.Errorf 逐字节一致 —— 日志、取证脚本、
// 运维 grep 全都依赖它。改了格式等于静默破坏取证链。
func TestGateHTTPErrorFormatUnchanged(t *testing.T) {
	e := &gateHTTPError{
		Status: 404, Method: "DELETE", Path: "/spot/orders/1132521944761",
		Body: `{"label":"ORDER_NOT_FOUND","message":"Order not found"}`,
	}
	want := `gate DELETE /spot/orders/1132521944761 -> 404: {"label":"ORDER_NOT_FOUND","message":"Order not found"}`
	if e.Error() != want {
		t.Fatalf("错误串格式变了：\n got: %s\nwant: %s", e.Error(), want)
	}
	// 旧实现里这个串是 fmt.Errorf 出来的纯字符串，没有任何可判别性。
	// 新类型必须能被 errors.As 取到状态码（TestCancelIsAlreadyGone 已钉死），
	// 这里再确认一次它确实实现了 error 接口而不是被内联成了别的形状。
	var _ error = e
}
