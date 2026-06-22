package backtest

import (
	"testing"

	"quantsaas/internal/quant"
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
