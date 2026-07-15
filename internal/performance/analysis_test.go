package performance

import (
	"math"
	"testing"
	"time"
)

func TestAnalyzeProducesRelativeDistributionAccumulationAndExposureMetrics(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	path := []Point{
		{TimeMs: start, NAV: 100, BenchmarkNAV: 100, ActualExposure: 0},
		{TimeMs: start + dayMilliseconds, NAV: 110, BenchmarkNAV: 105, ActualExposure: 0.5},
		{TimeMs: start + 2*dayMilliseconds, NAV: 99, BenchmarkNAV: 105, ActualExposure: 0.5},
		{TimeMs: start + 3*dayMilliseconds, NAV: 121, BenchmarkNAV: 110, ActualExposure: 1},
	}
	result, err := Analyze(path, path, nil, Config{HistogramBins: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, "final ratio", result.Summary.Relative.FinalNAVRatio, 1.1)
	assertNear(t, "log ratio", result.Summary.Relative.LogFinalNAVRatio, math.Log(1.1))
	if got := result.Summary.Distributions[PeriodDaily].Count; got != 3 {
		t.Fatalf("daily observations = %d, want 3", got)
	}
	if got := len(result.Charts.DailyDistribution.Bins); got != 3 {
		t.Fatalf("daily histogram bins = %d, want 3", got)
	}
	counts := 0
	for _, bin := range result.Charts.DailyDistribution.Bins {
		counts += bin.Count
	}
	if counts != 3 {
		t.Fatalf("daily histogram count = %d, want 3", counts)
	}
	if got := len(result.Charts.Accumulation.Points); got != 3 {
		t.Fatalf("accumulation points = %d, want 3", got)
	}
	last := result.Charts.Accumulation.Points[2]
	assertNear(t, "arithmetic sum", last.ArithmeticSum, 0.1-0.1+121.0/99.0-1)
	assertNear(t, "compound return", last.CompoundedReturn, 0.21)
	assertNear(t, "longest underwater days", result.Summary.LongestUnderwater.LongestDays, 1)
	if !result.Summary.LongestUnderwater.RecoveryCompleted {
		t.Fatal("expected the underwater period to recover")
	}
	assertNear(t, "exposure days ratio", result.Summary.Exposure.ExposureDaysRatio, 0.75)
	assertNear(t, "average exposure", result.Summary.Exposure.AverageActualExposure, 0.5)
	if result.Summary.Exposure.ExposureAdjustedReturn == nil {
		t.Fatal("expected readable exposure-adjusted return")
	}
	assertNear(t, "exposure-adjusted return", *result.Summary.Exposure.ExposureAdjustedReturn, 0.42)
}

func TestAnalyzeAnnualizationUsesExplicitNoCashFlowPath(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	end := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	source := []Point{
		{TimeMs: start, NAV: 100, BenchmarkNAV: 100, ActualExposure: 0.5},
		{TimeMs: end, NAV: 1000, BenchmarkNAV: 900, ActualExposure: 0.5},
	}
	noCashFlow := []Point{
		{TimeMs: start, NAV: 100, BenchmarkNAV: 100, ActualExposure: 0.5},
		{TimeMs: end, NAV: 110, BenchmarkNAV: 105, ActualExposure: 0.5},
	}
	result, err := Analyze(source, noCashFlow, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Relative.StrategyNoCashFlowAnnualized == nil || result.Summary.Relative.BenchmarkNoCashFlowAnnualized == nil {
		t.Fatal("expected no-cash-flow annualization metrics")
	}
	days := float64(end-start) / float64(dayMilliseconds)
	assertNear(t, "strategy CAGR", *result.Summary.Relative.StrategyNoCashFlowAnnualized, math.Pow(1.1, averageYearDays/days)-1)
	assertNear(t, "benchmark CAGR", *result.Summary.Relative.BenchmarkNoCashFlowAnnualized, math.Pow(1.05, averageYearDays/days)-1)
}

func TestAnalyzeBetaUsesAlignedDailyReturns(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	benchmark := []SeriesPoint{
		{TimeMs: start, Value: 100},
		{TimeMs: start + dayMilliseconds, Value: 110},
		{TimeMs: start + 2*dayMilliseconds, Value: 99},
		{TimeMs: start + 3*dayMilliseconds, Value: 108.9},
	}
	path := []Point{
		{TimeMs: start, NAV: 100, BenchmarkNAV: 100, ActualExposure: 1},
		{TimeMs: start + dayMilliseconds, NAV: 120, BenchmarkNAV: 110, ActualExposure: 1},
		{TimeMs: start + 2*dayMilliseconds, NAV: 96, BenchmarkNAV: 99, ActualExposure: 1},
		{TimeMs: start + 3*dayMilliseconds, NAV: 115.2, BenchmarkNAV: 108.9, ActualExposure: 1},
	}
	result, err := Analyze(path, path, benchmark, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Beta.Value == nil {
		t.Fatalf("beta unavailable: %s", result.Summary.Beta.UnavailableReason)
	}
	assertNear(t, "beta", *result.Summary.Beta.Value, 2)
}

func TestAnalyzeBetaUsesReturnsBetweenCommonCalendarEndpoints(t *testing.T) {
	start := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC).UnixMilli()
	benchmark := []SeriesPoint{
		{TimeMs: start, Value: 100},
		{TimeMs: start + 3*dayMilliseconds, Value: 110},
		{TimeMs: start + 4*dayMilliseconds, Value: 99},
	}
	path := []Point{
		{TimeMs: start, NAV: 100, BenchmarkNAV: 100, ActualExposure: 1},
		{TimeMs: start + dayMilliseconds, NAV: 110, BenchmarkNAV: 101, ActualExposure: 1},
		{TimeMs: start + 2*dayMilliseconds, NAV: 115, BenchmarkNAV: 102, ActualExposure: 1},
		{TimeMs: start + 3*dayMilliseconds, NAV: 120, BenchmarkNAV: 110, ActualExposure: 1},
		{TimeMs: start + 4*dayMilliseconds, NAV: 96, BenchmarkNAV: 99, ActualExposure: 1},
	}
	result, err := Analyze(path, path, benchmark, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Beta.Value == nil {
		t.Fatalf("beta unavailable: %s", result.Summary.Beta.UnavailableReason)
	}
	assertNear(t, "calendar-aligned beta", *result.Summary.Beta.Value, 2)
}

func TestAnalyzeAggregatesIntradayAccumulationAndExposureByUTCDay(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	path := []Point{
		{TimeMs: start, NAV: 100, BenchmarkNAV: 100, ActualExposure: 0},
		{TimeMs: start + 12*60*60*1000, NAV: 105, BenchmarkNAV: 102, ActualExposure: 1},
		{TimeMs: start + dayMilliseconds + 12*60*60*1000, NAV: 110, BenchmarkNAV: 104, ActualExposure: 0.5},
		{TimeMs: start + dayMilliseconds + 18*60*60*1000, NAV: 121, BenchmarkNAV: 110, ActualExposure: 0.25},
	}
	result, err := Analyze(path, path, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.Charts.Accumulation.Points); got != 1 {
		t.Fatalf("daily accumulation points = %d, want 1", got)
	}
	if got := len(result.Charts.Exposure.Points); got != 2 {
		t.Fatalf("daily exposure points = %d, want 2", got)
	}
	assertNear(t, "daily average exposure", result.Summary.Exposure.AverageActualExposure, 0.625)
	if result.Summary.Exposure.ExposureAdjustedReturn == nil {
		t.Fatal("expected exposure-adjusted return")
	}
	assertNear(t, "full-path exposure-adjusted return", *result.Summary.Exposure.ExposureAdjustedReturn, 0.21/0.625)
}

func TestAnalyzeMarksZeroExposureAndNoDownsideAsUnreadable(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	path := []Point{
		{TimeMs: start, NAV: 100, BenchmarkNAV: 100},
		{TimeMs: start + dayMilliseconds, NAV: 101, BenchmarkNAV: 101},
		{TimeMs: start + 2*dayMilliseconds, NAV: 102, BenchmarkNAV: 102},
		{TimeMs: start + 3*dayMilliseconds, NAV: 103, BenchmarkNAV: 103},
	}
	result, err := Analyze(path, path, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Exposure.ExposureAdjustedReadable || result.Summary.Exposure.ExposureAdjustedReturn != nil {
		t.Fatalf("zero exposure should be unreadable: %+v", result.Summary.Exposure)
	}
	if result.Summary.Sortino.Value != nil || result.Summary.Sortino.UnavailableReason != "no_downside_deviation" {
		t.Fatalf("unexpected Sortino state: %+v", result.Summary.Sortino)
	}
	if result.Summary.Beta.Value != nil || result.Summary.Beta.UnavailableReason != "benchmark_not_selected" {
		t.Fatalf("unexpected Beta state: %+v", result.Summary.Beta)
	}
}

func TestDistributionUsesUTCISOWeekAndLinearQuantiles(t *testing.T) {
	start := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC).UnixMilli()
	path := []Point{
		{TimeMs: start, NAV: 100, BenchmarkNAV: 100, ActualExposure: 0.5},
		{TimeMs: start + dayMilliseconds, NAV: 110, BenchmarkNAV: 105, ActualExposure: 0.5},
		{TimeMs: start + 2*dayMilliseconds, NAV: 121, BenchmarkNAV: 110, ActualExposure: 0.5},
		{TimeMs: start + 9*dayMilliseconds, NAV: 133.1, BenchmarkNAV: 115, ActualExposure: 0.5},
	}
	result, err := Analyze(path, path, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Summary.Distributions[PeriodWeekly].Count; got != 2 {
		t.Fatalf("weekly return count = %d, want 2", got)
	}
	daily := result.Summary.Distributions[PeriodDaily]
	assertNear(t, "daily median", daily.Median, 0.1)
	assertNear(t, "daily p25", daily.Quantiles["p25"], 0.1)
}

func assertNear(t *testing.T, name string, got float64, want float64) {
	t.Helper()
	scale := math.Max(1, math.Max(math.Abs(got), math.Abs(want)))
	if math.Abs(got-want) > 1e-10*scale {
		t.Fatalf("%s = %.12f, want %.12f", name, got, want)
	}
}
