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
