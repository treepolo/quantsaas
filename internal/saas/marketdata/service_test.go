package marketdata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientFetchKLinesParsesBinanceResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/klines" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("symbol"); got != "BTCUSDT" {
			t.Fatalf("symbol = %s", got)
		}
		if got := r.URL.Query().Get("interval"); got != "1m" {
			t.Fatalf("interval = %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[[1499040000000,"0.01634790","0.80000000","0.01575800","0.01577100","148976.11427815",1499644799999,"0",1,"0","0","0"]]`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	rows, err := client.FetchKLines(context.Background(), "BTCUSDT", "1m", 1499040000000, 1499040060000, 1000)
	if err != nil {
		t.Fatalf("FetchKLines failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows len = %d", len(rows))
	}
	row := rows[0]
	if row.OpenTime != 1499040000000 || row.Open != 0.01634790 || row.High != 0.80000000 || row.Low != 0.01575800 || row.Close != 0.01577100 || row.Volume != 148976.11427815 {
		t.Fatalf("unexpected row: %+v", row)
	}
}

func TestValidateImportRequestRejectsUnsupportedInterval(t *testing.T) {
	err := validateImportRequest(ImportRequest{
		Symbol:      "BTCUSDT",
		Interval:    "2m",
		StartTimeMs: 1,
		EndTimeMs:   2,
	})
	if err != ErrUnsupportedInterval {
		t.Fatalf("expected ErrUnsupportedInterval, got %v", err)
	}
}
