package backtest

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

func TestNormalizeSpawnPointKeepsZeroMonthlyDCA(t *testing.T) {
	spawn := quant.SpawnPoint{
		Policy: quant.CapitalPolicy{
			InitialUSDT:       10000,
			MonthlyInjectUSDT: 0,
		},
		Risk: quant.RiskBounds{
			MaxDrawdownPct: 0.88,
			LotStep:        0.000001,
			LotMin:         0.00001,
		},
	}

	if err := normalizeSpawnPoint(&spawn); err != nil {
		t.Fatalf("normalizeSpawnPoint returned error: %v", err)
	}
	if spawn.Policy.MonthlyInjectUSDT != 0 {
		t.Fatalf("monthly DCA = %v, want 0", spawn.Policy.MonthlyInjectUSDT)
	}
}

func TestScoreWindowsParallelMatchesIndependentWindowRuns(t *testing.T) {
	bars := make([]quant.Bar, 0, 2500)
	for index := 0; index < 2500; index++ {
		open := 100.0 + float64(index)*0.03
		close := open + 0.1 + float64(index%5)*0.01
		bars = append(bars, quant.Bar{OpenTime: int64(index+1) * 86_400_000, Open: open, High: close + 0.2, Low: open - 0.2, Close: close})
	}
	spawn := sigmoiddca.DefaultParams().Spawn
	costs := quant.ExecutionCostConfig{}
	windows := quant.BuildCrucibleWindows(bars, 1200)
	want := make([]WindowResult, 0, len(windows))
	for _, window := range windows {
		outcome := scoreWindow(context.Background(), window, "BTCUSDT", "1d", "close_same_bar", quant.DefaultSeedChromosome, &spawn, costs, sigmoiddca.PositionStructureFloatingOnly, backtestcore.LongTermFilterConfig{}, nil, nil)
		if outcome.err != nil {
			t.Fatalf("independent window %s: %v", window.Label, outcome.err)
		}
		want = append(want, outcome.detail)
	}
	_, got, err := scoreWindows(context.Background(), bars, "BTCUSDT", "1d", "close_same_bar", quant.DefaultSeedChromosome, &spawn, costs, sigmoiddca.PositionStructureFloatingOnly, backtestcore.LongTermFilterConfig{}, nil, nil)
	if err != nil {
		t.Fatalf("parallel window scoring: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatal("parallel window scoring changed an independent window result")
	}
}

func TestBacktestCostsUsesRequestValues(t *testing.T) {
	fee := 0.001
	spread := 0.0005
	costs := backtestCosts(CreateRequest{FeeRate: &fee, SpreadRate: &spread})

	if costs.FeeRate != fee {
		t.Fatalf("fee rate = %v, want %v", costs.FeeRate, fee)
	}
	if costs.SpreadRate != spread {
		t.Fatalf("spread rate = %v, want %v", costs.SpreadRate, spread)
	}
}

func TestValidateCostRateRejectsInvalidValues(t *testing.T) {
	negative := -0.001
	if err := validateCostRate("fee_rate", &negative); err == nil {
		t.Fatal("expected negative cost rate to fail")
	}

	tooLarge := 0.21
	if err := validateCostRate("spread_rate", &tooLarge); err == nil {
		t.Fatal("expected oversized cost rate to fail")
	}
}

func TestParseCustomParamsDefaultsToFloatingOnly(t *testing.T) {
	raw := json.RawMessage(`{"rebalance_threshold":0.75}`)

	params, err := parseCustomParams(raw)
	if err != nil {
		t.Fatal(err)
	}

	if params.PositionStructure != sigmoiddca.PositionStructureFloatingOnly {
		t.Fatalf("position structure = %s, want %s", params.PositionStructure, sigmoiddca.PositionStructureFloatingOnly)
	}
	if params.Chromosome.RebalanceThreshold != 0.75 {
		t.Fatalf("rebalance threshold = %.4f, want 0.75", params.Chromosome.RebalanceThreshold)
	}
}

func TestParseCustomParamEnvelopeDefaultsToFloatingOnly(t *testing.T) {
	raw := json.RawMessage(`{"sigmoid_dca_config":{"rebalance_threshold":0.75}}`)

	params, err := parseCustomParams(raw)
	if err != nil {
		t.Fatal(err)
	}

	if params.PositionStructure != sigmoiddca.PositionStructureFloatingOnly {
		t.Fatalf("position structure = %s, want %s", params.PositionStructure, sigmoiddca.PositionStructureFloatingOnly)
	}
	if params.Chromosome.RebalanceThreshold != 0.75 {
		t.Fatalf("rebalance threshold = %.4f, want 0.75", params.Chromosome.RebalanceThreshold)
	}
}

func TestParseCustomParamsRejectsInvalidForceThresholdOrder(t *testing.T) {
	raw := json.RawMessage(`{"force_full_threshold":0.40,"force_empty_threshold":0.60}`)

	_, err := parseCustomParams(raw)
	if err == nil {
		t.Fatal("expected invalid force threshold order to fail")
	}
	if !strings.Contains(err.Error(), "滿倉閾值低於空倉閾值") {
		t.Fatalf("error = %q, want force threshold message", err.Error())
	}
}
