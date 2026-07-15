package performancereport

import (
	"testing"
	"time"

	performancecore "quantsaas/internal/performance"
)

func TestBuildArtifactsCreatesIndependentChartBlocks(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	path := []performancecore.Point{
		{TimeMs: start, NAV: 100, BenchmarkNAV: 100, ActualExposure: 0},
		{TimeMs: start + 86_400_000, NAV: 105, BenchmarkNAV: 102, ActualExposure: 0.5},
		{TimeMs: start + 2*86_400_000, NAV: 103, BenchmarkNAV: 101, ActualExposure: 0.4},
	}
	analysis, err := performancecore.Analyze(path, path, nil, performancecore.Config{})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := BuildIdentity(IdentitySnapshot{
		BacktestResultID: 1, BacktestResultVersion: "p03-result-v1", BacktestResultContentHash: "sha256:source",
		AnnualizationBacktestResultID: 1, AnnualizationBacktestResultVersion: "p03-result-v1", AnnualizationResultContentHash: "sha256:source",
		Settings: ResolvedSettings{},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := BuildArtifacts(identity, analysis)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts.Charts) != 6 || artifacts.Manifest.BlockCount != 6 {
		t.Fatalf("chart block count = %d/%d, want 6", len(artifacts.Charts), artifacts.Manifest.BlockCount)
	}
	seen := map[string]bool{}
	for _, chart := range artifacts.Charts {
		if chart.ContentHash == "" || len(chart.PayloadJSON) == 0 || seen[chart.Kind] {
			t.Fatalf("invalid chart artifact: %+v", chart)
		}
		seen[chart.Kind] = true
	}
	if artifacts.SummaryHash == "" || artifacts.ManifestHash == "" || artifacts.ReportContentHash == "" {
		t.Fatalf("incomplete report hashes: %+v", artifacts)
	}
}
