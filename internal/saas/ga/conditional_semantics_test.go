package ga

import (
	"reflect"
	"testing"

	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

func TestFloatingOnlyIgnoresDisabledChromosomeFieldsInPath(t *testing.T) {
	bars := flatTestBars(180)
	params := sigmoiddca.DefaultParams()
	params.Spawn.Policy.InitialUSDT = 1000
	base := params.Chromosome
	changed := base
	changed.MicroReservePct = 0.45
	changed.DustUSD = 25
	changed.WedgeDeltaThreshold = 0.15
	changed.WedgeVolRatioThreshold = 2.5
	changed.MacroBearMultiplier = 2.5
	changed.MacroBullMultiplier = 0.2
	changed.ExtraDeployPct = 0.6
	changed.SoftReleaseMonths = 36
	changed.SoftReleasePct = 0.25
	changed.HardReleaseMaxPct = 0.5
	costs := quant.ExecutionCostConfig{FeeRate: 0.001, SpreadRate: 0.0005}

	first := RunSigmoidDCAPathBacktestWithModeCostsAndStructure(
		bars, bars[100].OpenTime, "1d", executionModeCloseNextOpen,
		base, &params.Spawn, costs, sigmoiddca.PositionStructureFloatingOnly,
	)
	second := RunSigmoidDCAPathBacktestWithModeCostsAndStructure(
		bars, bars[100].OpenTime, "1d", executionModeCloseNextOpen,
		changed, &params.Spawn, costs, sigmoiddca.PositionStructureFloatingOnly,
	)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("floating-only path changed when only disabled fields changed\nfirst=%+v\nsecond=%+v", first.Metrics, second.Metrics)
	}
}

func TestZeroCostsIgnoreDustThresholdInPath(t *testing.T) {
	bars := flatTestBars(180)
	params := sigmoiddca.DefaultParams()
	params.Spawn.Policy.InitialUSDT = 1000
	firstGene := params.Chromosome
	secondGene := firstGene
	firstGene.DustUSD = 5
	secondGene.DustUSD = 25

	first := RunSigmoidDCAPathBacktestWithModeCostsAndStructure(
		bars, bars[100].OpenTime, "1d", executionModeCloseSameBar,
		firstGene, &params.Spawn, quant.ExecutionCostConfig{}, sigmoiddca.PositionStructureDualLayer,
	)
	second := RunSigmoidDCAPathBacktestWithModeCostsAndStructure(
		bars, bars[100].OpenTime, "1d", executionModeCloseSameBar,
		secondGene, &params.Spawn, quant.ExecutionCostConfig{}, sigmoiddca.PositionStructureDualLayer,
	)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("zero-cost path changed when only dust threshold changed\nfirst=%+v\nsecond=%+v", first.Metrics, second.Metrics)
	}
}
