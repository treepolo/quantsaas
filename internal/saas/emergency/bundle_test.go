package emergency

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/saas/ga"
	"quantsaas/internal/saas/marketdata"
	"quantsaas/internal/strategies/sigmoiddca"
)

func TestCalculateUsesPathBacktestTargetWeight(t *testing.T) {
	params := sigmoiddca.DefaultParams()
	params.Spawn.Policy.InitialUSDT = 1000
	params.Spawn.Policy.MonthlyInjectUSDT = 0
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	bars := testBars(180)
	bundle := Bundle{
		Version:       BundleVersion,
		StrategyID:    sigmoiddca.StrategyID,
		ParameterID:   21,
		InstrumentID:  "SOXL",
		Symbol:        "SOXL",
		DataSource:    marketdata.DataSourceYahoo,
		Interval:      "1d",
		ExecutionMode: marketdata.ExecutionModeCloseNextOpen,
		ParamPack:     raw,
		Bars:          bars,
	}
	result, err := Calculate(bundle, nil)
	if err != nil {
		t.Fatal(err)
	}
	quantBars := make([]quant.Bar, 0, len(bars))
	for _, bar := range bars {
		quantBars = append(quantBars, quant.Bar{
			OpenTime: bar.OpenTimeMs,
			Open:     bar.Open,
			High:     bar.High,
			Low:      bar.Low,
			Close:    bar.Close,
		})
	}
	path := ga.RunSigmoidDCAPathBacktestWithMode(quantBars, quantBars[0].OpenTime, "1d", marketdata.ExecutionModeCloseNextOpen, params.Chromosome, &params.Spawn)
	want := path.NAV[len(path.NAV)-1].ModelTargetWeight
	if math.Abs(result.BaselineModelTargetWeight-want) > 1e-12 {
		t.Fatalf("target weight = %.12f, want %.12f", result.BaselineModelTargetWeight, want)
	}
}

func TestManualPriceReplacesSameDate(t *testing.T) {
	params := sigmoiddca.DefaultParams()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	bars := testBars(130)
	lastDate := bars[len(bars)-1].Date
	bundle := Bundle{
		Version:       BundleVersion,
		StrategyID:    sigmoiddca.StrategyID,
		ParameterID:   21,
		InstrumentID:  "SOXL",
		Symbol:        "SOXL",
		DataSource:    marketdata.DataSourceYahoo,
		Interval:      "1d",
		ExecutionMode: marketdata.ExecutionModeCloseSameBar,
		ParamPack:     raw,
		Bars:          bars,
	}
	result, err := Calculate(bundle, []ManualPrice{{Date: lastDate, Close: 222}})
	if err != nil {
		t.Fatal(err)
	}
	if result.LatestClose != 222 {
		t.Fatalf("latest close = %f, want 222", result.LatestClose)
	}
}

func TestReadManualPricesJSONL(t *testing.T) {
	input := strings.NewReader(`{"date":"2026-06-22","close":28.34}
{"date":"2026-06-23","close":29.01}
`)
	prices, err := readManualPrices(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != 2 || prices[1].Date != "2026-06-23" {
		t.Fatalf("unexpected prices: %#v", prices)
	}
}

func testBars(n int) []Bar {
	start := time.Date(2024, 1, 2, 14, 30, 0, 0, time.UTC)
	bars := make([]Bar, 0, n)
	price := 20.0
	for i := 0; i < n; i++ {
		price = price * (1 + 0.002*math.Sin(float64(i)/7))
		openTime := start.AddDate(0, 0, i)
		bars = append(bars, Bar{
			Date:       openTime.Format("2006-01-02"),
			OpenTimeMs: openTime.UnixMilli(),
			Open:       price * 0.99,
			High:       price * 1.02,
			Low:        price * 0.98,
			Close:      price,
		})
	}
	return bars
}
