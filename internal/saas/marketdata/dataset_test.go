package marketdata

import (
	"testing"

	"quantsaas/internal/quant"
	saasstore "quantsaas/internal/saas/store"
)

func TestAssembleDatasetUsesOnlyAvailableIndicatorValues(t *testing.T) {
	req := DatasetBuildRequest{
		Interval:    "1d",
		StartTimeMs: 1000,
		EndTimeMs:   3000,
	}
	primary := datasetSeriesData{
		Series: ResearchSeries{ID: "SOXL", SeriesType: SeriesTypeTradableAsset, DisplayName: "SOXL", DataSource: DataSourceYahoo, Tradable: true},
		Role:   "primary_tradable",
		Values: []DatasetValue{
			{SeriesID: "SOXL", ObservedAtMs: 1000, AvailableAtMs: 1500, Close: 10, Value: 10},
			{SeriesID: "SOXL", ObservedAtMs: 2000, AvailableAtMs: 2500, Close: 11, Value: 11},
			{SeriesID: "SOXL", ObservedAtMs: 3000, AvailableAtMs: 3500, Close: 12, Value: 12},
		},
	}
	indicator := datasetSeriesData{
		Series: ResearchSeries{ID: "CREDIT_SPREAD", SeriesType: SeriesTypeIndicator, DisplayName: "Credit Spread", DataSource: "manual"},
		Role:   "indicator",
		Values: []DatasetValue{
			{SeriesID: "CREDIT_SPREAD", ObservedAtMs: 1000, AvailableAtMs: 2400, Value: 1.2},
			{SeriesID: "CREDIT_SPREAD", ObservedAtMs: 3000, AvailableAtMs: 3600, Value: 9.9},
		},
	}

	result := assembleDataset(req, []datasetSeriesData{primary, indicator})

	if len(result.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(result.Rows))
	}
	if _, ok := result.Rows[0].Values["CREDIT_SPREAD"]; ok {
		t.Fatalf("first row used indicator before available_at")
	}
	gotSecond := result.Rows[1].Values["CREDIT_SPREAD"]
	if gotSecond.Value != 1.2 {
		t.Fatalf("second row indicator = %.2f, want 1.2", gotSecond.Value)
	}
	gotThird := result.Rows[2].Values["CREDIT_SPREAD"]
	if gotThird.Value != 1.2 {
		t.Fatalf("third row indicator = %.2f, want previous 1.2 because newer value is unavailable", gotThird.Value)
	}
}

func TestAssembleDatasetDoesNotUseFutureObservedIndicator(t *testing.T) {
	req := DatasetBuildRequest{Interval: "1d", StartTimeMs: 1000, EndTimeMs: 2000}
	primary := datasetSeriesData{
		Series: ResearchSeries{ID: "SOXL", SeriesType: SeriesTypeTradableAsset, DisplayName: "SOXL", DataSource: DataSourceYahoo, Tradable: true},
		Role:   "primary_tradable",
		Values: []DatasetValue{
			{SeriesID: "SOXL", ObservedAtMs: 1000, AvailableAtMs: 1500, Close: 10, Value: 10},
		},
	}
	indicator := datasetSeriesData{
		Series: ResearchSeries{ID: "BAD_FUTURE", SeriesType: SeriesTypeIndicator, DisplayName: "Bad Future", DataSource: "manual"},
		Role:   "indicator",
		Values: []DatasetValue{
			{SeriesID: "BAD_FUTURE", ObservedAtMs: 5000, AvailableAtMs: 1200, Value: 99},
		},
	}

	result := assembleDataset(req, []datasetSeriesData{primary, indicator})

	if _, ok := result.Rows[0].Values["BAD_FUTURE"]; ok {
		t.Fatalf("row used indicator with future observed_at")
	}
}

func TestNormalizeDatasetRequestDeduplicatesSeriesIDs(t *testing.T) {
	req := normalizeDatasetRequest(DatasetBuildRequest{
		TradableSeriesIDs:  []string{"soxl", "SOXL", " tqqq "},
		IndicatorSeriesIDs: []string{"credit_spread", "CREDIT_SPREAD"},
	})

	if len(req.TradableSeriesIDs) != 2 {
		t.Fatalf("tradable ids = %#v, want 2 unique ids", req.TradableSeriesIDs)
	}
	if req.TradableSeriesIDs[0] != "SOXL" || req.TradableSeriesIDs[1] != "TQQQ" {
		t.Fatalf("tradable ids = %#v", req.TradableSeriesIDs)
	}
	if len(req.IndicatorSeriesIDs) != 1 || req.IndicatorSeriesIDs[0] != "CREDIT_SPREAD" {
		t.Fatalf("indicator ids = %#v", req.IndicatorSeriesIDs)
	}
	if req.Interval != "1d" {
		t.Fatalf("interval = %s, want 1d", req.Interval)
	}
}

func TestPrimaryBarsFromDatasetUsesPrimaryTradableOHLCV(t *testing.T) {
	dataset := ResearchDataset{
		Series: []DatasetSeriesInfo{
			{ID: "SOXL", Role: "primary_tradable", SeriesType: SeriesTypeTradableAsset},
			{ID: "CREDIT_SPREAD", Role: "indicator", SeriesType: SeriesTypeIndicator},
		},
		Rows: []DatasetRow{
			{
				ObservedAtMs:   1000,
				DecisionTimeMs: 2000,
				Values: map[string]DatasetValue{
					"SOXL":          {SeriesID: "SOXL", Open: 10, High: 12, Low: 9, Close: 11, Value: 11, Volume: 100},
					"CREDIT_SPREAD": {SeriesID: "CREDIT_SPREAD", Value: 1.5},
				},
			},
			{
				ObservedAtMs:   3000,
				DecisionTimeMs: 4000,
				Values: map[string]DatasetValue{
					"SOXL": {SeriesID: "SOXL", Open: 11, High: 13, Low: 10, Close: 12, Value: 12, Volume: 120},
				},
			},
		},
	}

	bars, err := PrimaryBarsFromDataset(dataset)
	if err != nil {
		t.Fatal(err)
	}
	if len(bars) != 2 {
		t.Fatalf("bars = %d, want 2", len(bars))
	}
	if bars[0].OpenTime != 1000 || bars[0].Open != 10 || bars[0].High != 12 || bars[0].Low != 9 || bars[0].Close != 11 || bars[0].Volume != 100 {
		t.Fatalf("first bar = %#v", bars[0])
	}
	if bars[1].OpenTime != 3000 || bars[1].Close != 12 {
		t.Fatalf("second bar = %#v", bars[1])
	}
}

func TestPrimaryBarsFromDatasetRejectsMissingPrimaryValue(t *testing.T) {
	dataset := ResearchDataset{
		Series: []DatasetSeriesInfo{
			{ID: "SOXL", Role: "primary_tradable", SeriesType: SeriesTypeTradableAsset},
		},
		Rows: []DatasetRow{
			{
				ObservedAtMs:   1000,
				DecisionTimeMs: 2000,
				Values:         map[string]DatasetValue{},
			},
		},
	}

	if _, err := PrimaryBarsFromDataset(dataset); err == nil {
		t.Fatal("expected missing primary value to fail")
	}
}

func TestPrimaryBarsFromDatasetFallsBackToValueForCloseOnlySeries(t *testing.T) {
	dataset := ResearchDataset{
		Series: []DatasetSeriesInfo{
			{ID: "SOXL", Role: "primary_tradable", SeriesType: SeriesTypeTradableAsset},
		},
		Rows: []DatasetRow{
			{
				ObservedAtMs:   1000,
				DecisionTimeMs: 2000,
				Values: map[string]DatasetValue{
					"SOXL": {SeriesID: "SOXL", Value: 11},
				},
			},
		},
	}

	bars, err := PrimaryBarsFromDataset(dataset)
	if err != nil {
		t.Fatal(err)
	}
	if bars[0].Open != 11 || bars[0].High != 11 || bars[0].Low != 11 || bars[0].Close != 11 {
		t.Fatalf("bar = %#v, want close-only fallback", bars[0])
	}
}

func TestPrimaryBarsFromDatasetMatchesLegacyKLineConversion(t *testing.T) {
	rows := []saasstore.KLine{
		{OpenTime: 1000, Open: 10, High: 12, Low: 9, Close: 11, Volume: 100},
		{OpenTime: 2000, Open: 11, High: 13, Low: 10, Close: 12, Volume: 120},
	}
	values := make([]DatasetValue, 0, len(rows))
	for _, row := range rows {
		values = append(values, DatasetValue{
			SeriesID:     "SOXL",
			ObservedAtMs: row.OpenTime,
			Open:         row.Open,
			High:         row.High,
			Low:          row.Low,
			Close:        row.Close,
			Value:        row.Close,
			Volume:       row.Volume,
		})
	}
	dataset := assembleDataset(DatasetBuildRequest{Interval: "1d", StartTimeMs: 1000, EndTimeMs: 2000}, []datasetSeriesData{
		{
			Series: ResearchSeries{ID: "SOXL", SeriesType: SeriesTypeTradableAsset, DisplayName: "SOXL", DataSource: DataSourceYahoo, Tradable: true},
			Role:   "primary_tradable",
			Values: values,
		},
	})

	got, err := PrimaryBarsFromDataset(dataset)
	if err != nil {
		t.Fatal(err)
	}
	want := []quant.Bar{
		{OpenTime: 1000, Open: 10, High: 12, Low: 9, Close: 11, Volume: 100},
		{OpenTime: 2000, Open: 11, High: 13, Low: 10, Close: 12, Volume: 120},
	}
	if len(got) != len(want) {
		t.Fatalf("bars = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bar[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestExternalSignalByTimeBuildsRollingIndicatorSignal(t *testing.T) {
	req := DatasetBuildRequest{Interval: "1d", StartTimeMs: 1000, EndTimeMs: 3000}
	primary := datasetSeriesData{
		Series: ResearchSeries{ID: "SOXL", SeriesType: SeriesTypeTradableAsset, DisplayName: "SOXL", DataSource: DataSourceYahoo, Tradable: true},
		Role:   "primary_tradable",
		Values: []DatasetValue{
			{SeriesID: "SOXL", ObservedAtMs: 1000, AvailableAtMs: 1000, Close: 10, Value: 10},
			{SeriesID: "SOXL", ObservedAtMs: 2000, AvailableAtMs: 2000, Close: 11, Value: 11},
			{SeriesID: "SOXL", ObservedAtMs: 3000, AvailableAtMs: 3000, Close: 12, Value: 12},
		},
	}
	indicator := datasetSeriesData{
		Series: ResearchSeries{ID: "CREDIT_SPREAD", SeriesType: SeriesTypeIndicator, DisplayName: "Credit Spread", DataSource: "manual"},
		Role:   "indicator",
		Values: []DatasetValue{
			{SeriesID: "CREDIT_SPREAD", ObservedAtMs: 1000, AvailableAtMs: 1000, Value: 1},
			{SeriesID: "CREDIT_SPREAD", ObservedAtMs: 2000, AvailableAtMs: 2000, Value: 2},
			{SeriesID: "CREDIT_SPREAD", ObservedAtMs: 3000, AvailableAtMs: 3000, Value: 4},
		},
	}
	dataset := assembleDataset(req, []datasetSeriesData{primary, indicator})
	signals := ExternalSignalByTime(dataset)

	if _, ok := signals[1000]; ok {
		t.Fatal("first point should not have a z-score before any variance exists")
	}
	if signals[2000] <= 0 {
		t.Fatalf("second signal = %.4f, want positive z-score", signals[2000])
	}
	if signals[3000] <= signals[2000] {
		t.Fatalf("third signal = %.4f, want greater than second %.4f", signals[3000], signals[2000])
	}
}
