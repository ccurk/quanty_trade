package exchange

import "testing"

// Owners that share one Binance API key must resolve to a single account key so
// the guard treats their shared net position as ONE tracked row (the ③a
// adoption-dedup foundation). Owners on distinct keys must stay isolated.
func TestAccountKeyGroupsSharedAccount(t *testing.T) {
	b := &BinanceExchange{
		credsByID:      map[uint]binanceCred{},
		acctKeyByOwner: map[uint]string{},
	}
	b.credsByID[1] = binanceCred{APIKey: "AAA", APISecret: "s1"}
	b.credsByID[2] = binanceCred{APIKey: "AAA", APISecret: "s1"} // same account as owner 1
	b.credsByID[3] = binanceCred{APIKey: "BBB", APISecret: "s2"} // different account

	if k1, k2 := b.AccountKey(1), b.AccountKey(2); k1 != k2 {
		t.Fatalf("owners sharing an API key must map to one account key: %q vs %q", k1, k2)
	}
	if k1, k3 := b.AccountKey(1), b.AccountKey(3); k1 == k3 {
		t.Fatalf("owners with different API keys must map to different account keys, both %q", k1)
	}
}

// The account key is memoized per owner and stays stable for the process
// lifetime even if the underlying cred is later swapped — cache/guard buckets
// must not silently re-partition mid-run.
func TestAccountKeyMemoizedStable(t *testing.T) {
	b := &BinanceExchange{
		credsByID:      map[uint]binanceCred{},
		acctKeyByOwner: map[uint]string{},
	}
	b.credsByID[1] = binanceCred{APIKey: "AAA", APISecret: "s"}
	first := b.AccountKey(1)
	b.credsByID[1] = binanceCred{APIKey: "CCC", APISecret: "s"}
	if second := b.AccountKey(1); second != first {
		t.Fatalf("memoized account key must be stable: %q -> %q", first, second)
	}
}
