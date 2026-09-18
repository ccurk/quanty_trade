package exchange

import "testing"

// defect #270: market 配置缺失时不许静默兜底成 spot（那会把实盘 USD-M 合约密钥
// #39 无声指向 spot 现货接口），必须明确失败。resolveBinanceMarket 是
// NewBinanceExchange 实际读取 conf 后调用的规整函数，这里直接钉住它的行为，
// 不依赖 conf 包的全局单例状态。
func TestResolveBinanceMarketEmptyFails(t *testing.T) {
	if _, err := resolveBinanceMarket(""); err == nil {
		t.Fatal("market 为空必须返回 error，不能静默通过")
	}
	if _, err := resolveBinanceMarket("   "); err == nil {
		t.Fatal("market 全是空白也必须返回 error")
	}
}

func TestResolveBinanceMarketUSDMUnchanged(t *testing.T) {
	m, err := resolveBinanceMarket("usdm")
	if err != nil {
		t.Fatalf("market=usdm 不应报错: %v", err)
	}
	if m != "usdm" {
		t.Fatalf("market=usdm 应原样返回 usdm，got %q", m)
	}
	// 大小写/首尾空白规整前后一致，验证对现有 usdm 配置零行为影响。
	if m2, err := resolveBinanceMarket("  USDM  "); err != nil || m2 != "usdm" {
		t.Fatalf("大小写/空白规整应仍解析为 usdm, got %q err=%v", m2, err)
	}
}

func TestResolveBinanceMarketSpotUnchanged(t *testing.T) {
	m, err := resolveBinanceMarket("spot")
	if err != nil || m != "spot" {
		t.Fatalf("market=spot 显式配置时应原样通过, got %q err=%v", m, err)
	}
}

// NewBinanceExchange 在 conf.C() 未被本测试二进制 Load 过时，Exchange.Binance.Market
// 为 Go 零值空字符串——正好复现"环境变量和 yaml 都没配 market"的真实故障场景。
// exchange 包内没有任何测试调用 conf.Load/MustLoad（已用 grep 确认），所以这个零值
// 前提在同一个 test 二进制内是可控的。
func TestNewBinanceExchangePanicsOnUnconfiguredMarket(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("market 未配置时 NewBinanceExchange 必须 panic 拒启，而不是静默选 spot")
		}
	}()
	NewBinanceExchange()
}
