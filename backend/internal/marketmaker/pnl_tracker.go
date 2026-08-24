package marketmaker

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// pnl_tracker.go tracks live market-making PnL from actual gate fills, so risk
// controls (single-day stop-loss) and the dashboard have a REAL number, not a
// guess. Accounting is simple spot cash-flow: buy = cash out + inventory in, sell =
// cash in + inventory out; mark-to-market PnL = cash + inventory×mid. Fees are
// subtracted. Only gate is wired (the live venue); the account is the user's own
// gate key (single-owner) so my_trades = exactly our fills.

var tradeHTTP = &http.Client{Timeout: 10 * time.Second}

type gateFill struct {
	ID     string
	Side   string // buy/sell
	Amount float64
	Price  float64
	Fee    float64 // in quote (USDT) terms after normalization; here we store raw + currency
	FeeCur string
}

// gateMyTrades fetches recent fills for a pair (GET /spot/my_trades, HMAC-SHA512).
func gateMyTrades(symbol string, limit int) ([]gateFill, error) {
	key, secret := os.Getenv("MM_GATE_API_KEY"), os.Getenv("MM_GATE_API_SECRET")
	if key == "" || secret == "" {
		return nil, fmt.Errorf("gate key 未配置")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	const host = "https://api.gateio.ws"
	const path = "/spot/my_trades"
	query := "currency_pair=" + symbol + "&limit=" + strconv.Itoa(limit)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	bh := sha512.Sum512([]byte(""))
	payload := "GET\n/api/v4" + path + "\n" + query + "\n" + hex.EncodeToString(bh[:]) + "\n" + ts
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write([]byte(payload))
	sign := hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest(http.MethodGet, host+"/api/v4"+path+"?"+query, nil)
	req.Header.Set("KEY", key)
	req.Header.Set("Timestamp", ts)
	req.Header.Set("SIGN", sign)
	req.Header.Set("Accept", "application/json")

	resp, err := tradeHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("my_trades HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var rows []struct {
		ID          string `json:"id"`
		Side        string `json:"side"`
		Amount      string `json:"amount"`
		Price       string `json:"price"`
		Fee         string `json:"fee"`
		FeeCurrency string `json:"fee_currency"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	out := make([]gateFill, 0, len(rows))
	for _, r := range rows {
		out = append(out, gateFill{
			ID: r.ID, Side: strings.ToLower(r.Side),
			Amount: atofP(r.Amount), Price: atofP(r.Price),
			Fee: atofP(r.Fee), FeeCur: strings.ToUpper(r.FeeCurrency),
		})
	}
	return out, nil
}

// pnlTracker accumulates one pair's live cash-flow PnL from its fills.
type pnlTracker struct {
	mu        sync.Mutex
	cash      float64         // 累计现金流(卖入-买出-费),USDT
	inventory float64         // 当前基础币净持仓
	seen      map[string]bool // 已计入的成交 id(去重)
	day       string          // UTC 日,跨日清零
}

func newPnLTracker() *pnlTracker { return &pnlTracker{seen: map[string]bool{}} }

// apply folds new fills into cash/inventory. quoteAsset = USDT; baseAsset = the coin.
func (t *pnlTracker) apply(fills []gateFill, baseAsset string) {
	today := time.Now().UTC().Format("2006-01-02")
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.day != today { // 跨 UTC 日清零(单日止损按当日)
		t.day = today
		t.cash, t.inventory = 0, 0
		t.seen = map[string]bool{}
	}
	for _, f := range fills {
		if f.ID == "" || t.seen[f.ID] {
			continue
		}
		t.seen[f.ID] = true
		notional := f.Price * f.Amount
		if f.Side == "buy" {
			t.cash -= notional
			t.inventory += f.Amount
		} else {
			t.cash += notional
			t.inventory -= f.Amount
		}
		// 费:USDT 计价的费直接减;基础币计价的费按成交价折 USDT 减(近似)。
		switch f.FeeCur {
		case "USDT", "":
			t.cash -= f.Fee
		case baseAsset:
			t.inventory -= f.Fee
		default:
			t.cash -= f.Fee // 其它计价币近似当 USDT 减(保守)
		}
	}
}

// mtmPnL = 现金流 + 存货按当前中价折算(已实现+浮动)。
func (t *pnlTracker) mtmPnL(mid float64) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cash + t.inventory*mid
}

func atofP(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// 实时做市 PnL(盯市)按对存一份,供仪表盘做市卡显示。
var (
	mmPnLMu sync.RWMutex
	mmPnL   = map[string]float64{}
)

func recordMMPnL(exchange, symbol string, pnl float64) {
	mmPnLMu.Lock()
	mmPnL[exchange+"|"+symbol] = pnl
	mmPnLMu.Unlock()
}

// LiveMMPnL sums live mark-to-market PnL across all managed pairs (USDT).
func LiveMMPnL() float64 {
	mmPnLMu.RLock()
	defer mmPnLMu.RUnlock()
	var s float64
	for _, v := range mmPnL {
		s += v
	}
	return s
}

func nextUTCMidnight() time.Time {
	n := time.Now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day()+1, 0, 0, 0, 0, time.UTC)
}
