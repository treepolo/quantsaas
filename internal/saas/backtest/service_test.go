package backtest

import (
	"encoding/json"
	"strings"
	"testing"

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
