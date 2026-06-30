package marketdata

import "testing"

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
