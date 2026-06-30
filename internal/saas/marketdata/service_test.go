package marketdata

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	saasstore "quantsaas/internal/saas/store"
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

func nearlyEqual(a float64, b float64) bool {
	return math.Abs(a-b) < 1e-9
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
	normalized := normalizeYahooRowsForStorage(ImportRequest{InstrumentID: "SOXL", DataSource: DataSourceYahoo, Symbol: "SOXL", Interval: "1d"}, rows, time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC))
	if len(normalized) != 1 {
		t.Fatalf("normalized len = %d, want 1", len(normalized))
	}
	if normalized[0].Close != 279.29 {
		t.Fatalf("kept close = %f, want regular daily close", normalized[0].Close)
	}
}

func TestNormalizeYahooDailyRowsKeepsTaiwanETFOpen(t *testing.T) {
	rows := []BinanceKLine{
		{OpenTime: time.Date(2026, 6, 18, 1, 0, 0, 0, time.UTC).UnixMilli(), Close: 189.75},
		{OpenTime: time.Date(2026, 6, 18, 5, 33, 15, 0, time.UTC).UnixMilli(), Close: 190.10},
	}
	normalized := normalizeYahooRowsForStorage(ImportRequest{InstrumentID: "0050.TW", DataSource: DataSourceYahoo, Symbol: "0050.TW", Interval: "1d"}, rows, time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC))
	if len(normalized) != 1 {
		t.Fatalf("normalized len = %d, want 1", len(normalized))
	}
	if normalized[0].Close != 189.75 {
		t.Fatalf("kept close = %f, want Taiwan regular daily close", normalized[0].Close)
	}
}

func TestNormalizeYahooDailyRowsDropsRegularOpenBeforeMarketClose(t *testing.T) {
	rows := []BinanceKLine{
		{OpenTime: time.Date(2026, 6, 18, 13, 30, 0, 0, time.UTC).UnixMilli(), Close: 268.65},
	}
	normalized := normalizeYahooRowsForStorage(ImportRequest{InstrumentID: "SOXL", DataSource: DataSourceYahoo, Symbol: "SOXL", Interval: "1d"}, rows, time.Date(2026, 6, 18, 18, 0, 0, 0, time.UTC))
	if len(normalized) != 0 {
		t.Fatalf("normalized len = %d, want 0 before US market close", len(normalized))
	}
	normalized = normalizeYahooRowsForStorage(ImportRequest{InstrumentID: "SOXL", DataSource: DataSourceYahoo, Symbol: "SOXL", Interval: "1d"}, rows, time.Date(2026, 6, 18, 21, 0, 0, 0, time.UTC))
	if len(normalized) != 0 {
		t.Fatalf("normalized len = %d, want 0 before Yahoo ready delay", len(normalized))
	}
	normalized = normalizeYahooRowsForStorage(ImportRequest{InstrumentID: "SOXL", DataSource: DataSourceYahoo, Symbol: "SOXL", Interval: "1d"}, rows, time.Date(2026, 6, 18, 22, 0, 0, 0, time.UTC))
	if len(normalized) != 1 {
		t.Fatalf("normalized len after ready delay = %d, want 1", len(normalized))
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

func TestYahooClientAdjustsOHLCWithAdjustedClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"chart": {
				"result": [{
					"timestamp": [1719878400],
					"indicators": {
						"quote": [{
							"open": [90.0],
							"high": [110.0],
							"low": [80.0],
							"close": [100.0],
							"volume": [123456]
						}],
						"adjclose": [{
							"adjclose": [50.0]
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
	rows, err := client.FetchKLines(context.Background(), "0050.TW", "1d", 1719878400000, 1719964800000)
	if err != nil {
		t.Fatalf("FetchKLines failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows len = %d", len(rows))
	}
	row := rows[0]
	if row.Open != 45.0 || row.High != 55.0 || row.Low != 40.0 || row.Close != 50.0 || row.Volume != 123456 {
		t.Fatalf("unexpected adjusted row: %+v", row)
	}
}

func TestDetectYahooAvailableStartUsesFirstReturnedRow(t *testing.T) {
	first := time.Date(2010, 3, 11, 14, 30, 0, 0, time.UTC).Unix()
	second := time.Date(2010, 3, 12, 14, 30, 0, 0, time.UTC).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("period1"); got != "0" {
			t.Fatalf("period1 = %s, want 0", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"chart": {
				"result": [{
					"timestamp": [` + strconv.FormatInt(first, 10) + `,` + strconv.FormatInt(second, 10) + `],
					"indicators": {
						"quote": [{
							"open": [10.0, 10.5],
							"high": [11.0, 11.5],
							"low": [9.0, 9.5],
							"close": [10.5, 11.0],
							"volume": [100, 200]
						}]
					}
				}],
				"error": null
			}
		}`))
	}))
	defer server.Close()

	svc := NewService(nil, nil)
	svc.yahooClient = NewYahooClient(server.URL)
	svc.yahooClient.lastAt = time.Now().Add(-yahooMinRequestInterval)
	svc.now = func() time.Time { return time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC) }

	start, err := svc.detectAvailableStart(context.Background(), ResearchInstrument{
		ID:                 "SOXL",
		Symbol:             "SOXL",
		DataSource:         DataSourceYahoo,
		SupportedIntervals: []string{"1d"},
		Market:             "us",
	}, "1d")
	if err != nil {
		t.Fatalf("detectAvailableStart failed: %v", err)
	}
	if start != first*1000 {
		t.Fatalf("start = %d, want %d", start, first*1000)
	}
}

func TestDetectYahooAvailableStartFallsBackForIntradayRange(t *testing.T) {
	first := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC).Unix()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			http.Error(w, "range rejected", http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"chart": {
				"result": [{
					"timestamp": [` + strconv.FormatInt(first, 10) + `],
					"indicators": {
						"quote": [{
							"open": [10.0],
							"high": [11.0],
							"low": [9.0],
							"close": [10.5],
							"volume": [100]
						}]
					}
				}],
				"error": null
			}
		}`))
	}))
	defer server.Close()

	svc := NewService(nil, nil)
	svc.yahooClient = NewYahooClient(server.URL)
	svc.yahooClient.lastAt = time.Now().Add(-yahooMinRequestInterval)
	svc.now = func() time.Time { return time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC) }

	start, err := svc.detectAvailableStart(context.Background(), ResearchInstrument{
		ID:                 "SOXL",
		Symbol:             "SOXL",
		DataSource:         DataSourceYahoo,
		SupportedIntervals: []string{"1h"},
		Market:             "us",
	}, "1h")
	if err != nil {
		t.Fatalf("detectAvailableStart failed: %v", err)
	}
	if requests < 2 {
		t.Fatalf("fallback was not attempted")
	}
	if start != first*1000 {
		t.Fatalf("start = %d, want %d", start, first*1000)
	}
}

func TestInstrumentRecordRoundTripsAvailableStartMs(t *testing.T) {
	record := saasstore.ResearchInstrument{
		ID:                 "SOXL",
		Symbol:             "SOXL",
		DisplayName:        "SOXL",
		DataSource:         DataSourceYahoo,
		SupportedIntervals: saasstore.JSONB([]byte(`["1d","1h"]`)),
		AvailableStartMs:   saasstore.JSONB([]byte(`{"1d":1268327400000,"1h":1719705600000}`)),
		Market:             "us",
		Enabled:            true,
	}
	instrument, err := recordToInstrument(record)
	if err != nil {
		t.Fatalf("recordToInstrument failed: %v", err)
	}
	if instrument.AvailableStartMs["1d"] != 1268327400000 || instrument.AvailableStartMs["1h"] != 1719705600000 {
		t.Fatalf("available starts = %+v", instrument.AvailableStartMs)
	}
	out, err := instrumentToRecord(instrument)
	if err != nil {
		t.Fatalf("instrumentToRecord failed: %v", err)
	}
	if string(out.AvailableStartMs) == "{}" {
		t.Fatalf("available starts were not preserved")
	}
}

func TestRepairYahooOHLCFillsZeroOpen(t *testing.T) {
	open, high, low, closePrice := repairYahooOHLC(0, 10, 8, 9)
	if open != 9 || high != 10 || low != 8 || closePrice != 9 {
		t.Fatalf("unexpected repaired OHLC: %f %f %f %f", open, high, low, closePrice)
	}
}

func TestAggregateYahooDailyRowsBuildsWeeklyFromDailyScale(t *testing.T) {
	rows := []BinanceKLine{
		{OpenTime: time.Date(2013, 12, 23, 1, 0, 0, 0, time.UTC).UnixMilli(), Open: 9.10, High: 9.20, Low: 9.05, Close: 9.15, Volume: 1},
		{OpenTime: time.Date(2013, 12, 24, 1, 0, 0, 0, time.UTC).UnixMilli(), Open: 9.15, High: 9.25, Low: 9.10, Close: 9.20, Volume: 2},
		{OpenTime: time.Date(2013, 12, 25, 1, 0, 0, 0, time.UTC).UnixMilli(), Open: 9.20, High: 9.30, Low: 9.18, Close: 9.24, Volume: 3},
	}
	weekly := aggregateYahooDailyRows("0050.TW", rows, "1w", time.Date(2013, 12, 30, 0, 0, 0, 0, time.UTC).UnixMilli())
	if len(weekly) != 1 {
		t.Fatalf("weekly len = %d, want 1", len(weekly))
	}
	row := weekly[0]
	wantOpenTime := time.Date(2013, 12, 22, 16, 0, 0, 0, time.UTC).UnixMilli()
	if row.OpenTime != wantOpenTime {
		t.Fatalf("weekly open time = %s, want %s", time.UnixMilli(row.OpenTime).UTC(), time.UnixMilli(wantOpenTime).UTC())
	}
	if row.Open != 9.10 || row.High != 9.30 || row.Low != 9.05 || row.Close != 9.24 || row.Volume != 6 {
		t.Fatalf("unexpected weekly row: %+v", row)
	}
}

func TestAggregateYahooDailyRowsDropsIncompleteWeeklyBucket(t *testing.T) {
	rows := []BinanceKLine{
		{OpenTime: time.Date(2026, 6, 15, 13, 30, 0, 0, time.UTC).UnixMilli(), Open: 100, High: 110, Low: 90, Close: 105, Volume: 1},
		{OpenTime: time.Date(2026, 6, 16, 13, 30, 0, 0, time.UTC).UnixMilli(), Open: 105, High: 112, Low: 95, Close: 108, Volume: 1},
	}
	weekly := aggregateYahooDailyRows("SOXL", rows, "1w", time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC).UnixMilli())
	if len(weekly) != 0 {
		t.Fatalf("weekly len = %d, want 0 for incomplete week: %+v", len(weekly), weekly)
	}
}

func TestAggregateYahooDailyRowsBuildsCompletedMonthlyBucket(t *testing.T) {
	rows := []BinanceKLine{
		{OpenTime: time.Date(2026, 5, 1, 13, 30, 0, 0, time.UTC).UnixMilli(), Open: 10, High: 12, Low: 9, Close: 11, Volume: 1},
		{OpenTime: time.Date(2026, 5, 29, 13, 30, 0, 0, time.UTC).UnixMilli(), Open: 11, High: 13, Low: 10, Close: 12, Volume: 2},
		{OpenTime: time.Date(2026, 6, 1, 13, 30, 0, 0, time.UTC).UnixMilli(), Open: 12, High: 14, Low: 11, Close: 13, Volume: 3},
	}
	monthly := aggregateYahooDailyRows("SOXL", rows, "1M", time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC).UnixMilli())
	if len(monthly) != 1 {
		t.Fatalf("monthly len = %d, want 1 completed month: %+v", len(monthly), monthly)
	}
	row := monthly[0]
	wantOpenTime := time.Date(2026, 5, 1, 4, 0, 0, 0, time.UTC).UnixMilli()
	if row.OpenTime != wantOpenTime {
		t.Fatalf("monthly open time = %s, want %s", time.UnixMilli(row.OpenTime).UTC(), time.UnixMilli(wantOpenTime).UTC())
	}
	if row.Open != 10 || row.High != 13 || row.Low != 9 || row.Close != 12 || row.Volume != 3 {
		t.Fatalf("unexpected monthly row: %+v", row)
	}
}

func TestBackAdjustLargeYahooDiscontinuities(t *testing.T) {
	rows := []BinanceKLine{
		{Open: 3.8, High: 4.4, Low: 3.6, Close: 4},
		{Open: 0.9, High: 1.1, Low: 0.8, Close: 1},
		{Open: 1.1, High: 1.3, Low: 1.0, Close: 1.2},
	}
	backAdjustLargeYahooDiscontinuities(rows)
	if !nearlyEqual(rows[0].Open, 0.95) || !nearlyEqual(rows[0].High, 1.1) || !nearlyEqual(rows[0].Low, 0.9) || !nearlyEqual(rows[0].Close, 1) {
		t.Fatalf("unexpected first adjusted row: %+v", rows[0])
	}
	if rows[1].Close != 1 || rows[2].Close != 1.2 {
		t.Fatalf("unexpected later rows: %+v", rows)
	}
}
