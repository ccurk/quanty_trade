package exchange

import (
	"testing"
	"time"
)

// USDMPositionAmtCached must (1) return the signed amount for a held symbol from a
// fresh cache, and (2) return 0 for a symbol ABSENT from a fresh cache WITHOUT
// hitting REST — the whole point of the ①/(b) hardening. If the absent path fell
// through to FetchPositions it would attempt a real signed request (no
// httpClient/network in the test) and error/panic, which this test would catch.
func TestUSDMPositionAmtCachedFreshHitAndAbsent(t *testing.T) {
	b := &BinanceExchange{
		credsByID:         map[uint]binanceCred{1: {APIKey: "AAA", APISecret: "s"}},
		acctKeyByOwner:    map[uint]string{},
		positionsCache:    map[string][]Position{},
		positionsCacheExp: map[string]time.Time{},
	}
	acct := b.positionsAccountKey(1)
	b.positionsCache[acct] = []Position{
		{Symbol: "ALICE/USDT", Direction: "long", Amount: 397, Price: 0.139, CurrentPrice: 0.14},
		{Symbol: "H/USDT", Direction: "short", Amount: 100, Price: 1.0, CurrentPrice: 0.9},
	}
	b.positionsCacheExp[acct] = time.Now().Add(5 * time.Second)

	if amt, entry, mark, err := b.USDMPositionAmtCached(1, "ALICE/USDT"); err != nil || amt != 397 || entry != 0.139 || mark != 0.14 {
		t.Fatalf("long hit wrong: amt=%v entry=%v mark=%v err=%v", amt, entry, mark, err)
	}
	if amt, _, _, err := b.USDMPositionAmtCached(1, "H/USDT"); err != nil || amt != -100 {
		t.Fatalf("short hit must be negative: amt=%v err=%v", amt, err)
	}
	// absent in a FRESH cache → 0, and must NOT fall through to REST.
	if amt, entry, mark, err := b.USDMPositionAmtCached(1, "NOTHELD/USDT"); err != nil || amt != 0 || entry != 0 || mark != 0 {
		t.Fatalf("absent must return 0 without REST: amt=%v entry=%v mark=%v err=%v", amt, entry, mark, err)
	}
}
