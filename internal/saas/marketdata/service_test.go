package marketdata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
	svc := NewService(nil, nil)
	err := svc.validateImportRequest(context.Background(), ImportRequest{
		Symbol:      "BTCUSDT",
		Interval:    "2m",
		StartTimeMs: 1,
		EndTimeMs:   2,
	})
	if err != ErrUnsupportedInterval {
		t.Fatalf("expected ErrUnsupportedInterval, got %v", err)
	}
}

func TestYahooChartIntervalMapsWeeklyAndMonthly(t *testing.T) {
	if got := yahooChartInterval("1w"); got != "1wk" {
		t.Fatalf("weekly interval = %s", got)
	}
	if got := yahooChartInterval("1M"); got != "1mo" {
		t.Fatalf("monthly interval = %s", got)
	}
}

func TestNormalizeYahooDailyRowsDropsRealtimeRows(t *testing.T) {
	rows := []BinanceKLine{
		{OpenTime: time.Date(2026, 6, 18, 13, 30, 0, 0, time.UTC).UnixMilli(), Close: 279.29},
		{OpenTime: time.Date(2026, 6, 18, 13, 34, 41, 0, time.UTC).UnixMilli(), Close: 268.65},
	}
	normalized := normalizeYahooRowsForStorage(ImportRequest{InstrumentID: "SOXL", DataSource: DataSourceYahoo, Symbol: "SOXL", Interval: "1d"}, rows)
	if len(normalized) != 1 {
		t.Fatalf("normalized len = %d, want 1", len(normalized))
	}
	if normalized[0].Close != 279.29 {
		t.Fatalf("kept close = %f, want regular daily close", normalized[0].Close)
	}
}

func TestAutoUpdateIntervalsOnlyUsesHighTimeframes(t *testing.T) {
	got := autoUpdateIntervals([]string{"1d", "1h", "1m", "1w", "1M", "1d"})
	want := []string{"1d", "1w", "1M"}
	if len(got) != len(want) {
		t.Fatalf("intervals = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("intervals = %+v, want %+v", got, want)
		}
	}
}

func TestYahooClientRetries429AndParsesChartResponse(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.Header.Get("User-Agent") == "" || r.Header.Get("Referer") == "" {
			t.Fatalf("missing browser-like yahoo headers")
		}
		if got := r.URL.Query().Get("interval"); got != "1d" {
			t.Fatalf("interval = %s", got)
		}
		if attempts == 1 {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"chart": {
				"result": [{
					"timestamp": [1719878400],
					"indicators": {
						"quote": [{
							"open": [23000.0],
							"high": [23100.0],
							"low": [22900.0],
							"close": [23050.0],
							"volume": [123456]
						}]
					}
				}],
				"error": null
			}
		}`))
	}))
	defer server.Close()

	client := NewYahooClient(server.URL)
	client.lastAt = time.Now().Add(-yahooMinRequestInterval)
	rows, err := client.FetchKLines(context.Background(), "^TWII", "1d", 1719878400000, 1719964800000)
	if err != nil {
		t.Fatalf("FetchKLines failed: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if len(rows) != 1 || rows[0].Close != 23050.0 || rows[0].Volume != 123456 {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}
