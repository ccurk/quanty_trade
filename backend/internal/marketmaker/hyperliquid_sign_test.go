package marketmaker

import "testing"

// 官方测试向量,取自 hyperliquid-python-sdk/tests/signing_test.py。
// 私钥是 SDK 测试里公开的固定 key(非真实资金账户)。
// 这些向量把 msgpack 字节序 → keccak → EIP-712 → secp256k1 整条链钉死:
// 任何一环差一个字节,r/s/v 都对不上。文档没写签名方案,只有 SDK 能背书。
const hlTestKey = "0x0123456789012345678901234567890123456789012345678901234567890123"

func checkSig(t *testing.T, label string, got hlSignature, wantR, wantS string, wantV int) {
	t.Helper()
	if got.R != wantR || got.S != wantS || got.V != wantV {
		t.Errorf("%s:\n  got  r=%s s=%s v=%d\n  want r=%s s=%s v=%d",
			label, got.R, got.S, got.V, wantR, wantS, wantV)
	}
}

// Test 1: {"type":"dummy","num":100000000000} nonce=0 无 vault。
// num 超过 uint32,顺带验证 msgpack 的 uint64 分支。
func TestHLSignDummyAction(t *testing.T) {
	action := newMap().setStr("type", "dummy").setInt("num", 100000000000)

	got, err := hlSignAction(hlTestKey, action, 0, "", true)
	if err != nil {
		t.Fatal(err)
	}
	checkSig(t, "dummy mainnet", got,
		"0x53749d5b30552aeb2fca34b530185976545bb22d0b3ce6f62e31be961a59298",
		"0x755c40ba9bf05223521753995abb2f73ab3229be8ec921f350cb447e384d8ed8", 27)

	got, err = hlSignAction(hlTestKey, action, 0, "", false)
	if err != nil {
		t.Fatal(err)
	}
	checkSig(t, "dummy testnet", got,
		"0x542af61ef1f429707e3c76c5293c80d01f74ef853e34b76efffcb57e574f9510",
		"0x17b8b32f086e8cdede991f1e2c529f5dd5297cbe8128500e00cbaf766204a613", 28)
}

// Test 4: 同一 action 带 vault_address,验证 0x01||addr 分支。
func TestHLSignWithVault(t *testing.T) {
	action := newMap().setStr("type", "dummy").setInt("num", 100000000000)
	vault := "0x1719884eb866cb12b2287399b15f7db5e7d775ea"

	got, err := hlSignAction(hlTestKey, action, 0, vault, true)
	if err != nil {
		t.Fatal(err)
	}
	checkSig(t, "vault mainnet", got,
		"0x3c548db75e479f8012acf3000ca3a6b05606bc2ec0c29c50c515066a326239",
		"0x4d402be7396ce74fbba3795769cda45aec00dc3125a984f2a9f23177b190da2c", 28)
}

// Test 2: 真实下单 action —— 这条最关键,它就是引擎每秒要发的东西。
// order_request_to_order_wire({coin ETH, buy, sz 100, px 100, GTC}, asset=1)
// → {"a":1,"b":true,"p":"100","s":"100","r":false,"t":{"limit":{"tif":"Gtc"}}}
// order_wires_to_order_action → {"type":"order","orders":[...],"grouping":"na"}
func TestHLSignOrderAction(t *testing.T) {
	wire := newMap().
		setInt("a", 1).
		setBool("b", true).
		setStr("p", hlFloatToWire(100)).
		setStr("s", hlFloatToWire(100)).
		setBool("r", false).
		set("t", newMap().set("limit", newMap().setStr("tif", "Gtc")))
	action := newMap().
		setStr("type", "order").
		set("orders", mpArr{wire}).
		setStr("grouping", "na")

	got, err := hlSignAction(hlTestKey, action, 0, "", true)
	if err != nil {
		t.Fatal(err)
	}
	checkSig(t, "order mainnet", got,
		"0xd65369825a9df5d80099e513cce430311d7d26ddf477f5b3a33d2806b100d78e",
		"0x2b54116ff64054968aa237c20ca9ff68000f977c93289157748a3162b6ea940e", 28)

	got, err = hlSignAction(hlTestKey, action, 0, "", false)
	if err != nil {
		t.Fatal(err)
	}
	checkSig(t, "order testnet", got,
		"0x82b2ba28e76b3d761093aaded1b1cdad4960b3af30212b343fb2e6cdfa4e3d54",
		"0x6b53878fc99d26047f4d7e8c90eb98955a109f44209163f52d8dc4278cbbd9f5", 27)
}

// Test 9: {"type":"scheduleCancel","time":123456789} —— 验证 uint32 分支。
func TestHLSignScheduleCancel(t *testing.T) {
	action := newMap().setStr("type", "scheduleCancel").setInt("time", 123456789)
	got, err := hlSignAction(hlTestKey, action, 0, "", true)
	if err != nil {
		t.Fatal(err)
	}
	checkSig(t, "scheduleCancel mainnet", got,
		"0x609cb20c737945d070716dcc696ba030e9976fcf5edad87afa7d877493109d55",
		"0x16c685d63b5c7a04512d73f183b3d7a00da5406ff1f8aad33f8ae2163bab758b", 28)
}

// Test 6: 字符串字段 action,验证 fixstr 分支。
func TestHLSignCreateSubAccount(t *testing.T) {
	action := newMap().setStr("type", "createSubAccount").setStr("name", "example")
	got, err := hlSignAction(hlTestKey, action, 0, "", true)
	if err != nil {
		t.Fatal(err)
	}
	checkSig(t, "createSubAccount mainnet", got,
		"0x51096fe3239421d16b671e192f574ae24ae14329099b6db28e479b86cdd6caa7",
		"0xb71f7d293af92d3772572afb8b102d167a7cef7473388286bc01f52a5c5b423", 27)
}

func TestHLFloatToWire(t *testing.T) {
	cases := map[float64]string{
		100: "100", 0.5: "0.5", 1.23456789: "1.23456789",
		0: "0", 1000000: "1000000", 0.00000001: "0.00000001",
	}
	for in, want := range cases {
		if got := hlFloatToWire(in); got != want {
			t.Errorf("hlFloatToWire(%v)=%q want %q", in, got, want)
		}
	}
}
