package sigmoiddca

import (
	"testing"

	"quantsaas/internal/quant"
)

func TestExternalSignalWeightChangesTargetWeightOnlyWhenEnabled(t *testing.T) {
	closes := make([]float64, minStrategyBars)
	timestamps := make([]int64, minStrategyBars)
	for i := range closes {
		closes[i] = 100 + float64(i%7)
		timestamps[i] = int64(i+1) * 86400000
	}
	input := quant.StrategyInput{
		Symbol:     "SOXL",
		Interval:   "1d",
		Closes:     closes,
		Timestamps: timestamps,
		Portfolio: quant.PortfolioSnapshot{
			USDTBalance: 100000,
			TotalEquity: 100000,
		},
		AISignal: quant.AISignalVector{SMarket: 1},
	}

	baseParams := DefaultParams()
	baseParams.Chromosome = quant.DefaultSeedChromosome
	baseParams.Chromosome.ExternalSignalWeight = 0
	base := Step(input, baseParams)

	withSignal := baseParams
	withSignal.Chromosome.ExternalSignalWeight = 2
	changed := Step(input, withSignal)

	baseTarget := base.Diagnostics["target_weight"]
	changedTarget := changed.Diagnostics["target_weight"]
	if baseTarget == 0 || changedTarget == 0 {
		t.Fatalf("targets should be available, base %.4f changed %.4f", baseTarget, changedTarget)
	}
	if changedTarget >= baseTarget {
		t.Fatalf("positive external signal with positive weight should lower target: base %.4f changed %.4f", baseTarget, changedTarget)
	}
	if base.Diagnostics["external_signal"] != 1 {
		t.Fatalf("external signal = %.4f, want raw signal 1", base.Diagnostics["external_signal"])
	}
	if changed.Diagnostics["external_signal"] != 1 {
		t.Fatalf("external signal = %.4f, want 1", changed.Diagnostics["external_signal"])
	}
}
