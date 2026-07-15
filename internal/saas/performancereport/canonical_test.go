package performancereport

import "testing"

func TestAnalysisIdentityIsCanonicalAndSeparatesSettings(t *testing.T) {
	base := IdentitySnapshot{
		BacktestResultID: 10, BacktestResultVersion: "p03-result-v1", BacktestResultContentHash: "sha256:source",
		AnnualizationBacktestResultID: 11, AnnualizationBacktestResultVersion: "p03-result-v1", AnnualizationResultContentHash: "sha256:annual",
		Settings: ResolvedSettings{RiskFreeAnnualRate: 0.02, HistogramBins: 20},
	}
	first, err := BuildIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	if first.AnalysisKey != second.AnalysisKey || first.Snapshot.SettingsHash != second.Snapshot.SettingsHash {
		t.Fatal("identical settings did not produce a stable identity")
	}
	changed := base
	changed.Settings.RiskFreeAnnualRate = 0.03
	third, err := BuildIdentity(changed)
	if err != nil {
		t.Fatal(err)
	}
	if first.AnalysisKey == third.AnalysisKey {
		t.Fatal("risk-free rate did not change the analysis key")
	}
	changed = base
	changed.Settings.BetaBenchmark = &BetaBenchmarkSettings{
		InstrumentID: "SP500", Symbol: "^GSPC", DataSource: "yahoo", Interval: "1d",
		StartTimeMs: 1, EndTimeMs: 2, DatasetVersion: "ohlcv-v1", DatasetHash: "sha256:beta",
	}
	fourth, err := BuildIdentity(changed)
	if err != nil {
		t.Fatal(err)
	}
	if first.AnalysisKey == fourth.AnalysisKey {
		t.Fatal("Beta benchmark did not change the analysis key")
	}
	decoded, err := DecodeIdentity(first.SnapshotJSON)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.AnalysisKey != first.AnalysisKey {
		t.Fatalf("decoded analysis key = %s, want %s", decoded.AnalysisKey, first.AnalysisKey)
	}
}
