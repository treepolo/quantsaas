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
