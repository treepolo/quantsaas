package marketdata

import "testing"

func TestSeriesFromInstrumentCreatesTradableAssetSeries(t *testing.T) {
	instrument := ResearchInstrument{
		ID:                 "SOXL",
		Symbol:             "SOXL",
		DisplayName:        "SOXL 三倍做多費半 ETF",
		DataSource:         DataSourceYahoo,
		SupportedIntervals: []string{"1d", "1w"},
		Market:             "us",
		SortOrder:          30,
		Enabled:            true,
	}

	series := seriesFromInstrument(instrument)

	if series.ID != "SOXL" {
		t.Fatalf("series id = %s, want SOXL", series.ID)
	}
	if series.SeriesType != SeriesTypeTradableAsset {
		t.Fatalf("series type = %s, want %s", series.SeriesType, SeriesTypeTradableAsset)
	}
	if !series.Tradable {
		t.Fatalf("tradable = false, want true")
	}
	if series.SourceInstrumentID != "SOXL" {
		t.Fatalf("source instrument id = %s, want SOXL", series.SourceInstrumentID)
	}
	if series.Currency != "USD" {
		t.Fatalf("currency = %s, want USD", series.Currency)
	}
	if len(series.SupportedIntervals) != 2 {
		t.Fatalf("supported intervals length = %d, want 2", len(series.SupportedIntervals))
	}
}

func TestNormalizeUpsertSeriesDefaultsIndicatorAsNonTradable(t *testing.T) {
	series, err := normalizeUpsertSeries(UpsertSeriesRequest{
		ID:          "credit_spread",
		DisplayName: "Credit Spread",
		DataSource:  "manual",
	})
	if err != nil {
		t.Fatalf("normalize upsert series: %v", err)
	}
	if series.SeriesType != SeriesTypeIndicator {
		t.Fatalf("series type = %s, want %s", series.SeriesType, SeriesTypeIndicator)
	}
	if series.Tradable {
		t.Fatalf("tradable = true, want false")
	}
	if series.Frequency != "1d" {
		t.Fatalf("frequency = %s, want 1d", series.Frequency)
	}
	if series.Unit != "value" {
		t.Fatalf("unit = %s, want value", series.Unit)
	}
	if series.RevisionPolicy != RevisionPolicyCurrentHistorical {
		t.Fatalf("revision policy = %s, want %s", series.RevisionPolicy, RevisionPolicyCurrentHistorical)
	}
}

func TestNormalizeUpsertSeriesRejectsUnsupportedType(t *testing.T) {
	_, err := normalizeUpsertSeries(UpsertSeriesRequest{
		ID:         "BAD",
		SeriesType: "magic",
	})
	if err == nil {
		t.Fatalf("expected unsupported type error")
	}
}
