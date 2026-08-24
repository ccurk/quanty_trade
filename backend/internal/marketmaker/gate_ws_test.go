package marketmaker

import (
	"encoding/json"
	"testing"
)

// 签名向量与 python hmac/hashlib 独立计算结果钉死(跨语言双证, 防串接顺序/编码回归):
//
//	HMAC_SHA512("test-secret", "api\nspot.order_place\n{\"currency_pair\":\"BTC_USDT\"}\n1700000000")
//	HMAC_SHA512("test-secret", "api\nspot.login\n\"\"\n1700000000")
func TestGateSignWSVectors(t *testing.T) {
	got := gateSignWS("test-secret", "spot.order_place", []byte(`{"currency_pair":"BTC_USDT"}`), 1700000000)
	want := "a0e3cd6601fb76b835c1026178ad1300f44dfcab1c9daa92b88e38c477a2adaf3e0b3abb689e269b7d6faac8fdcdcd77b5d967490875893ea827a953009b9696"
	if got != want {
		t.Fatalf("order_place sign mismatch:\n got %s\nwant %s", got, want)
	}
	got = gateSignWS("test-secret", "spot.login", []byte(`""`), 1700000000)
	want = "c3d99ced73bae47b7b0062bfc01c24a764abe7bfc908352250606a83735cc371f8bf9d0de45e0a54fad98926301912c612c4eda1553dc31bea4e37ec4e5a61bb"
	if got != want {
		t.Fatalf("login sign mismatch:\n got %s\nwant %s", got, want)
	}
}

// 不变式: 信封里发送的 req_param 字节必须与参与签名的字节完全一致
// (json.RawMessage 原样内嵌, Marshal 不得改写它)。
func TestGateWSEnvelopeReqParamBytesStable(t *testing.T) {
	raw := json.RawMessage(`{"currency_pair":"BTC_USDT","side":"buy"}`)
	req := gateWSRequest{
		Time: 1700000000, Channel: "spot.order_place", Event: "api",
		Payload: gateWSPayload{ReqID: "r1", APIKey: "k", ReqParam: raw, Timestamp: "1700000000", Signature: "s"},
	}
	frame, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Payload struct {
			ReqParam json.RawMessage `json:"req_param"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(frame, &back); err != nil {
		t.Fatal(err)
	}
	if string(back.Payload.ReqParam) != string(raw) {
		t.Fatalf("req_param bytes rewritten in envelope:\n sent %s\n got  %s", raw, back.Payload.ReqParam)
	}
}

// 向量取自 2026-08-25 线上探针的真实帧(截断无关 header 字段):order_place 是两段式,
// 帧1=请求回显(必须跳过),帧2=真结果;order_cancel/login 单帧(不得跳过)。
func TestIsPlaceEchoFrame(t *testing.T) {
	echo := []byte(`{"header":{"status":"200","channel":"spot.order_place","event":"api"},"data":{"result":{"req_id":"qt-1","timestamp":"1787590984","signature":"ab","req_param":{"side":"buy","amount":"0.0001"}}},"request_id":"qt-1"}`)
	var ack gateWSAck
	if err := json.Unmarshal(echo, &ack); err != nil {
		t.Fatal(err)
	}
	if !isPlaceEchoFrame(ack) {
		t.Fatalf("place 回显帧必须被识别为 echo")
	}

	final := []byte(`{"header":{"status":"400","channel":"spot.order_place","event":"api"},"data":{"errs":{"label":"INVALID_PARAM_VALUE","message":"too small"}},"request_id":"qt-1"}`)
	ack = gateWSAck{}
	if err := json.Unmarshal(final, &ack); err != nil {
		t.Fatal(err)
	}
	if isPlaceEchoFrame(ack) {
		t.Fatalf("place 真结果帧(errs)不得被当成 echo")
	}

	success := []byte(`{"header":{"status":"200","channel":"spot.order_place","event":"api"},"data":{"result":{"id":"1119","text":"apiv4-ws","amount":"48"}},"request_id":"qt-2"}`)
	ack = gateWSAck{}
	if err := json.Unmarshal(success, &ack); err != nil {
		t.Fatal(err)
	}
	if isPlaceEchoFrame(ack) {
		t.Fatalf("place 成功帧(订单对象)不得被当成 echo")
	}

	// login 单帧应答同样含 req_param 回显 —— 但 channel 不同,绝不能跳过(否则登录死等超时)。
	login := []byte(`{"header":{"status":"200","channel":"spot.login","event":"api"},"data":{"result":{"api_key":"947f","req_param":""}},"request_id":"login-1"}`)
	ack = gateWSAck{}
	if err := json.Unmarshal(login, &ack); err != nil {
		t.Fatal(err)
	}
	if isPlaceEchoFrame(ack) {
		t.Fatalf("login 应答不得被当成 echo(会导致登录超时)")
	}
}

func TestGateWSAckParsing(t *testing.T) {
	ok := []byte(`{"request_id":"r1","header":{"response_time":"1","status":"200","channel":"spot.order_place","event":"api"},"data":{"result":{"id":"1700664343","text":"t-abc"}}}`)
	var ack gateWSAck
	if err := json.Unmarshal(ok, &ack); err != nil {
		t.Fatal(err)
	}
	if ack.RequestID != "r1" || ack.Header.Status != "200" {
		t.Fatalf("ack parse: %+v", ack)
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(ack.Data.Result, &r); err != nil || r.ID != "1700664343" {
		t.Fatalf("result parse: %v %+v", err, r)
	}

	bad := []byte(`{"request_id":"r2","header":{"status":"401","channel":"spot.login","event":"api"},"data":{"errs":{"label":"INVALID_KEY","message":"..."}}}`)
	ack = gateWSAck{}
	if err := json.Unmarshal(bad, &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Header.Status == "200" || ack.Data.Errs == nil || ack.Data.Errs.Label != "INVALID_KEY" {
		t.Fatalf("error ack parse: %+v", ack)
	}
}
