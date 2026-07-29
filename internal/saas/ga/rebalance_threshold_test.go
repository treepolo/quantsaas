package ga

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

func TestApplyRebalanceThresholdFiltersOnlyMicroOrders(t *testing.T) {
	output := quant.StrategyOutput{
		Intents: []quant.TradeIntent{
			{Action: quant.ActionBuy, Engine: quant.EngineMacro, AmountUSDT: 100, LotType: quant.LotTypeDeadStack},
			{Action: quant.ActionBuy, Engine: quant.EngineMicro, AmountUSDT: 50, LotType: quant.LotTypeFloating},
		},
		LotTransfers: []quant.LotTransfer{
			{FromLotType: quant.LotTypeDeadStack, ToLotType: quant.LotTypeFloating, Amount: 1},
		},
		Diagnostics: map[string]float64{"target_weight": 0.05},
	}
	portfolio := quant.PortfolioSnapshot{
		USDTBalance: 1000,
		FloatBTC:    0.04,
		TotalEquity: 1000,
	}

	filtered := applyRebalanceThreshold(output, portfolio, 1000, 0.02)

	if len(filtered.Intents) != 1 {
		t.Fatalf("intent count = %d, want 1", len(filtered.Intents))
	}
	if filtered.Intents[0].Engine != quant.EngineMacro {
		t.Fatalf("remaining intent engine = %s, want macro", filtered.Intents[0].Engine)
	}
	if len(filtered.LotTransfers) != 0 {
		t.Fatalf("lot transfers = %d, want 0", len(filtered.LotTransfers))
	}
}

func TestApplyRebalanceThresholdKeepsMicroOrderWhenGapPasses(t *testing.T) {
	output := quant.StrategyOutput{
		Intents:     []quant.TradeIntent{{Action: quant.ActionSell, Engine: quant.EngineMicro, QtyAsset: 0.02, LotType: quant.LotTypeFloating}},
		Diagnostics: map[string]float64{"target_weight": 0.05},
	}
	portfolio := quant.PortfolioSnapshot{
		USDTBalance: 1000,
		FloatBTC:    0.20,
		TotalEquity: 1000,
	}

	filtered := applyRebalanceThreshold(output, portfolio, 1000, 0.02)

	if len(filtered.Intents) != 1 {
		t.Fatalf("intent count = %d, want 1", len(filtered.Intents))
	}
	if filtered.Intents[0].Engine != quant.EngineMicro {
		t.Fatalf("remaining intent engine = %s, want micro", filtered.Intents[0].Engine)
	}
}

func TestApplyRebalanceThresholdAllowsHighThreshold(t *testing.T) {
	output := quant.StrategyOutput{
		Intents:     []quant.TradeIntent{{Action: quant.ActionBuy, Engine: quant.EngineMicro, AmountUSDT: 600, LotType: quant.LotTypeFloating}},
		Diagnostics: map[string]float64{"target_weight": 0.62},
	}
	portfolio := quant.PortfolioSnapshot{
		USDTBalance: 1000,
		TotalEquity: 1000,
	}

	filtered := applyRebalanceThreshold(output, portfolio, 100, 0.75)

	if len(filtered.Intents) != 0 {
		t.Fatalf("intent count = %d, want 0", len(filtered.Intents))
	}
}

func TestForceTargetThresholdsOnlyAffectPracticalPath(t *testing.T) {
	params := sigmoiddca.DefaultParams()
	params.Spawn.Policy.InitialUSDT = 1000
	params.Spawn.Policy.MonthlyInjectUSDT = 0
	params.PositionStructure = sigmoiddca.PositionStructureFloatingOnly
	params.Chromosome.ForceFullThreshold = 0.60
	params.Chromosome.ForceEmptyThreshold = 0
	params.Chromosome.RebalanceThreshold = 0
	params.Chromosome.DustUSD = 1

	bars := flatTestBars(115)
	path := RunSigmoidDCAPathBacktestWithModeCostsAndStructure(
		bars,
		bars[112].OpenTime,
		"1d",
		executionModeCloseSameBar,
		params.Chromosome,
		&params.Spawn,
		quant.ExecutionCostConfig{},
		params.PositionStructure,
	)
	if len(path.NAV) == 0 {
		t.Fatal("expected path points")
	}

	first := path.NAV[0]
	rawTarget := 1 / (1 + math.Exp(-0.5))
	if math.Abs(first.ModelTargetWeight-rawTarget) > 1e-12 {
		t.Fatalf("model target weight = %.12f, want raw baseline %.12f", first.ModelTargetWeight, rawTarget)
	}
	if math.Abs(first.EmptyReferenceTargetWeight-rawTarget) > 1e-12 {
		t.Fatalf("empty reference target weight = %.12f, want raw reference %.12f", first.EmptyReferenceTargetWeight, rawTarget)
	}
	if math.Abs(first.PracticalTargetWeight-1) > 1e-12 {
		t.Fatalf("practical target weight = %.12f, want forced 1", first.PracticalTargetWeight)
	}
}

func TestPracticalTargetDoesNotAdoptUnexecutableForcedTarget(t *testing.T) {
	params := sigmoiddca.DefaultParams()
	params.Spawn.Policy.InitialUSDT = 10
	params.Spawn.Policy.MonthlyInjectUSDT = 0
	params.PositionStructure = sigmoiddca.PositionStructureFloatingOnly
	params.Chromosome.ForceFullThreshold = 0.60
	params.Chromosome.ForceEmptyThreshold = 0
	params.Chromosome.RebalanceThreshold = 0
	params.Chromosome.DustUSD = 14.5

	bars := flatTestBars(115)
	path := RunSigmoidDCAPathBacktestWithModeCostsAndStructure(
		bars,
		bars[112].OpenTime,
		"1d",
		executionModeCloseSameBar,
		params.Chromosome,
		&params.Spawn,
		quant.ExecutionCostConfig{},
		params.PositionStructure,
	)
	if len(path.NAV) == 0 {
		t.Fatal("expected path points")
	}

	first := path.NAV[0]
	if math.Abs(first.ModelTargetWeight-(1/(1+math.Exp(-0.5)))) > 1e-12 {
		t.Fatalf("model target weight = %.12f, want raw baseline target", first.ModelTargetWeight)
	}
	if math.Abs(first.PracticalTargetWeight) > 1e-12 {
		t.Fatalf("practical target weight = %.12f, want actual cash position when order is below dust", first.PracticalTargetWeight)
	}
}

func TestEvaluateRejectsInvalidForceThresholdOrder(t *testing.T) {
	params := sigmoiddca.DefaultParams()
	params.Chromosome.ForceFullThreshold = 0.40
	params.Chromosome.ForceEmptyThreshold = 0.60

	result, err := NewSigmoidDCAEvolvable().Evaluate(context.Background(), params.Chromosome, EvaluablePlan{
		Spawn: &params.Spawn,
		GeneOptions: GeneOptions{
			EvolveForceFullThreshold:  true,
			EvolveForceEmptyThreshold: true,
			PositionStructure:         sigmoiddca.PositionStructureFloatingOnly,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !result.Fatal || result.ScoreTotal != FatalFitnessScore {
		t.Fatalf("result = %+v, want fatal fitness", result)
	}
}

func TestEncodeResultRejectsInvalidForceThresholdOrder(t *testing.T) {
	params := sigmoiddca.DefaultParams()
	params.Chromosome.ForceFullThreshold = 0.40
	params.Chromosome.ForceEmptyThreshold = 0.60

	_, err := NewSigmoidDCAEvolvable().EncodeResult(params.Chromosome, &params.Spawn, GeneOptions{
		EvolveForceFullThreshold:  true,
		EvolveForceEmptyThreshold: true,
		PositionStructure:         sigmoiddca.PositionStructureFloatingOnly,
	})
	if err == nil {
		t.Fatal("expected invalid force threshold order error")
	}
	if !strings.Contains(err.Error(), "\u6eff\u5009\u95be\u503c\u4f4e\u65bc\u7a7a\u5009\u95be\u503c") {
		t.Fatalf("error = %v", err)
	}
}

func TestSampleProducesValidForceThresholdOrder(t *testing.T) {
	rng := cyclingRNG{}
	gene := NewSigmoidDCAEvolvable().Sample(&rng).(quant.Chromosome)
	if gene.ForceFullThreshold < gene.ForceEmptyThreshold {
		t.Fatalf("force full threshold %.4f < force empty threshold %.4f", gene.ForceFullThreshold, gene.ForceEmptyThreshold)
	}
}

func TestMutationAndCrossoverOnlyProduceValidForceThresholdPairs(t *testing.T) {
	rng := &cyclingRNG{}
	base := quant.DefaultSeedChromosome
	base.ForceFullThreshold = 0.82
	base.ForceEmptyThreshold = 0.18
	for index := 0; index < 200; index++ {
		mutated := NewSigmoidDCAEvolvable().Mutate(base, 1, 25, rng).(quant.Chromosome)
		if mutated.ForceFullThreshold < mutated.ForceEmptyThreshold {
			t.Fatalf("mutation %d produced full %.4f below empty %.4f", index, mutated.ForceFullThreshold, mutated.ForceEmptyThreshold)
		}
		other := base
		other.ForceFullThreshold = 0.35
		other.ForceEmptyThreshold = 0.30
		child := NewSigmoidDCAEvolvable().Crossover(base, other, rng).(quant.Chromosome)
		if child.ForceFullThreshold < child.ForceEmptyThreshold {
			t.Fatalf("crossover %d produced full %.4f below empty %.4f", index, child.ForceFullThreshold, child.ForceEmptyThreshold)
		}
	}
}

func TestNormalizeFixedForceThresholdKeepsGeneratedCounterpartLegal(t *testing.T) {
	base := quant.DefaultSeedChromosome
	base.ForceFullThreshold = 0.40
	candidate := quant.DefaultSeedChromosome
	candidate.ForceEmptyThreshold = 0.80
	normalized := NewSigmoidDCAEvolvable().NormalizeGene(candidate, GeneOptions{
		EvolveForceEmptyThreshold: true,
		FixedParamKeys:            []string{"force_full_threshold"},
		FixedGene:                 &base,
	}).(quant.Chromosome)
	if normalized.ForceFullThreshold < normalized.ForceEmptyThreshold {
		t.Fatalf("fixed full %.4f below generated empty %.4f", normalized.ForceFullThreshold, normalized.ForceEmptyThreshold)
	}
}

func TestNormalizeGeneDisablesGammaByDefault(t *testing.T) {
	params := sigmoiddca.DefaultParams()
	params.Chromosome.Gamma = 4.2

	normalized := NewSigmoidDCAEvolvable().NormalizeGene(params.Chromosome, GeneOptions{
		PositionStructure: sigmoiddca.PositionStructureFloatingOnly,
	}).(quant.Chromosome)
	if normalized.Gamma != 0 {
		t.Fatalf("gamma = %.4f, want 0 when gamma evolution is disabled", normalized.Gamma)
	}
}

func TestNormalizeGeneKeepsGammaWhenEnabled(t *testing.T) {
	params := sigmoiddca.DefaultParams()
	params.Chromosome.Gamma = 4.2

	normalized := NewSigmoidDCAEvolvable().NormalizeGene(params.Chromosome, GeneOptions{
		EvolveGamma:       true,
		PositionStructure: sigmoiddca.PositionStructureFloatingOnly,
	}).(quant.Chromosome)
	if math.Abs(normalized.Gamma-4.2) > 1e-12 {
		t.Fatalf("gamma = %.4f, want 4.2 when gamma evolution is enabled", normalized.Gamma)
	}
}

func TestMarketRegionModeDisablesGlobalReserveAndRebalance(t *testing.T) {
	params := sigmoiddca.DefaultParams()
	params.Chromosome.MicroReservePct = 0.35
	params.Chromosome.RebalanceThreshold = 0.70

	normalized := NewSigmoidDCAEvolvable().NormalizeGene(params.Chromosome, GeneOptions{
		MarketRegionEnabled:       true,
		EvolveRebalanceThreshold:  true,
		MarketRegionMaxThresholds: 1,
		MarketRegionMaxWindow:     2,
	}).(quant.Chromosome)
	if normalized.MicroReservePct != 0 || normalized.RebalanceThreshold != 0 {
		t.Fatalf("market region must disable global reserve/rebalance, got reserve=%.2f rebalance=%.2f", normalized.MicroReservePct, normalized.RebalanceThreshold)
	}
}

func TestNormalizeGeneAppliesFixedFields(t *testing.T) {
	params := sigmoiddca.DefaultParams()
	params.Chromosome.Beta = 7.5
	params.Chromosome.Gamma = 4.2
	base := quant.DefaultSeedChromosome
	base.Beta = 1.25
	base.Gamma = 3.5

	normalized := NewSigmoidDCAEvolvable().NormalizeGene(params.Chromosome, GeneOptions{
		PositionStructure: sigmoiddca.PositionStructureFloatingOnly,
		FixedParamKeys:    []string{"beta", "gamma"},
		FixedGene:         &base,
	}).(quant.Chromosome)
	if math.Abs(normalized.Beta-1.25) > 1e-12 {
		t.Fatalf("beta = %.4f, want fixed 1.25", normalized.Beta)
	}
	if math.Abs(normalized.Gamma-3.5) > 1e-12 {
		t.Fatalf("gamma = %.4f, want fixed 3.5", normalized.Gamma)
	}
}

func TestNormalizeFixedParamKeysFiltersUnknownAndDuplicates(t *testing.T) {
	keys := NormalizeFixedParamKeys([]string{"beta", "unknown", "gamma", "beta"})
	if len(keys) != 2 || keys[0] != "beta" || keys[1] != "gamma" {
		t.Fatalf("keys = %#v, want beta/gamma", keys)
	}
}

func flatTestBars(n int) []quant.Bar {
	start := time.Date(2026, 1, 2, 14, 30, 0, 0, time.UTC)
	bars := make([]quant.Bar, 0, n)
	for i := 0; i < n; i++ {
		ts := start.AddDate(0, 0, i).UnixMilli()
		bars = append(bars, quant.Bar{
			OpenTime: ts,
			Open:     100,
			High:     100,
			Low:      100,
			Close:    100,
		})
	}
	return bars
}
