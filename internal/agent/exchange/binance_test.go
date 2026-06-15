package exchange

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	agentconfig "quantsaas/internal/agent/config"
	"quantsaas/internal/protocol"
)

func TestBinancePlaceOrderUsesQuoteOrderQtyForBuy(t *testing.T) {
	const apiKey = "unit-test-key"
	const secret = "unit-test-secret"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/order" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if got := r.Header.Get("X-MBX-APIKEY"); got != apiKey {
			t.Fatalf("unexpected api key header %q", got)
		}
		if !validSignature(r.URL.RawQuery, secret) {
			t.Fatalf("request signature is invalid: %s", r.URL.RawQuery)
		}

		query := r.URL.Query()
		if got := query.Get("symbol"); got != "BTCUSDT" {
			t.Fatalf("symbol = %q", got)
		}
		if got := query.Get("side"); got != "BUY" {
			t.Fatalf("side = %q", got)
		}
		if got := query.Get("type"); got != "MARKET" {
			t.Fatalf("type = %q", got)
		}
		if got := query.Get("quoteOrderQty"); got != "15.5" {
			t.Fatalf("quoteOrderQty = %q", got)
		}
		if got := query.Get("quantity"); got != "" {
			t.Fatalf("quantity should be empty for quote buy, got %q", got)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"symbol":              "BTCUSDT",
			"clientOrderId":       "inst1-MACRO-1",
			"executedQty":         "0.00025000",
			"cummulativeQuoteQty": "15.50000000",
			"status":              "FILLED",
			"fills": []map[string]string{
				{"price": "62000.00000000", "qty": "0.00025000", "commission": "0.00000025", "commissionAsset": "BTC"},
			},
		})
	}))
	defer server.Close()

	client := NewBinanceClient(agentconfig.ExchangeConfig{
		Name:      "binance",
		APIKey:    apiKey,
		SecretKey: secret,
		BaseURL:   server.URL,
	})

	execution, err := client.PlaceOrder(context.Background(), protocol.TradeCommand{
		ClientOrderID: "inst1-MACRO-1",
		Action:        "BUY",
		Symbol:        "BTCUSDT",
		AmountUSDT:    "15.5",
	})
	if err != nil {
		t.Fatalf("PlaceOrder error: %v", err)
	}
	if execution.Status != "filled" {
		t.Fatalf("status = %q", execution.Status)
	}
	if execution.FilledQty != "0.00025000" {
		t.Fatalf("filled qty = %q", execution.FilledQty)
	}
	if execution.FilledPrice != "62000" {
		t.Fatalf("filled price = %q", execution.FilledPrice)
	}
}

func TestBinanceGetBalancesMapsFreeAndLocked(t *testing.T) {
	const secret = "unit-test-secret"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/account" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if !validSignature(r.URL.RawQuery, secret) {
			t.Fatalf("request signature is invalid: %s", r.URL.RawQuery)
		}
		if got := r.URL.Query().Get("omitZeroBalances"); got != "true" {
			t.Fatalf("omitZeroBalances = %q", got)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"balances": []map[string]string{
				{"asset": "BTC", "free": "0.10000000", "locked": "0.01000000"},
				{"asset": "USDT", "free": "100.50000000", "locked": "0.00000000"},
			},
		})
	}))
	defer server.Close()

	client := NewBinanceClient(agentconfig.ExchangeConfig{
		Name:      "binance",
		SecretKey: secret,
		BaseURL:   server.URL,
	})

	balances, err := client.GetBalances(context.Background())
	if err != nil {
		t.Fatalf("GetBalances error: %v", err)
	}
	if len(balances) != 2 {
		t.Fatalf("len(balances) = %d", len(balances))
	}
	if balances[0] != (protocol.Balance{Asset: "BTC", Available: "0.10000000", Frozen: "0.01000000"}) {
		t.Fatalf("unexpected BTC balance: %+v", balances[0])
	}
	if balances[1] != (protocol.Balance{Asset: "USDT", Available: "100.50000000", Frozen: "0.00000000"}) {
		t.Fatalf("unexpected USDT balance: %+v", balances[1])
	}
}

func validSignature(rawQuery string, secret string) bool {
	parts := strings.Split(rawQuery, "&signature=")
	if len(parts) != 2 {
		return false
	}
	payload, err := url.QueryUnescape(parts[0])
	if err != nil {
		return false
	}
	encoded := url.Values{}
	for _, pair := range strings.Split(payload, "&") {
		keyValue := strings.SplitN(pair, "=", 2)
		if len(keyValue) != 2 {
			return false
		}
		encoded.Set(keyValue[0], keyValue[1])
	}
	return parts[1] == signHMACSHA256(encoded.Encode(), secret)
}
