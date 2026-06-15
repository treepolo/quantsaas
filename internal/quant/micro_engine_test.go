package quant

import (
	"math"
	"testing"
)

func TestMicroSignalPositiveTargetsBelowHalf(t *testing.T) {
	out := ComputeMicroDecisionV4(baseMicroInput(1))
	if out.TargetWeight >= 0.5 {
		t.Fatalf("target weight = %f, want < 0.5", out.TargetWeight)
	}
}

func TestMicroSignalNegativeTargetsAboveHalf(t *testing.T) {
	out := ComputeMicroDecisionV4(baseMicroInput(-1))
	if out.TargetWeight <= 0.5 {
		t.Fatalf("target weight = %f, want > 0.5", out.TargetWeight)
	}
}

func TestMicroNeutralInventoryBias(t *testing.T) {
	input := baseMicroInput(0)
	input.Gamma = 3
	out := ComputeMicroDecisionV4(input)
	if math.Abs(out.TargetWeight-0.5) > 1e-12 {
		t.Fatalf("target weight = %.12f, want 0.5", out.TargetWeight)
	}
}

func TestMicroDeterministic(t *testing.T) {
	input := baseMicroInput(0.25)
	a := ComputeMicroDecisionV4(input)
	b := ComputeMicroDecisionV4(input)
	if a != b {
		t.Fatalf("expected deterministic output\nfirst=%+v\nsecond=%+v", a, b)
	}
}

func TestMicroQuietDustIsZero(t *testing.T) {
	input := baseMicroInput(0.01)
	input.IsQuiet = true
	out := ComputeMicroDecisionV4(input)
	if math.Abs(out.TheoreticalUSD) >= 10.1 {
		t.Fatalf("test setup expected dust order, got theoretical %f", out.TheoreticalUSD)
	}
	if out.OrderUSDT != 0 {
		t.Fatalf("quiet dust order = %f, want 0", out.OrderUSDT)
	}
}

func TestMicroWedgeForcesMinimumOrder(t *testing.T) {
	input := baseMicroInput(0.01)
	input.IsQuiet = false
	input.WedgeDeltaThreshold = 0.0001
	out := ComputeMicroDecisionV4(input)
	if math.Abs(out.TheoreticalUSD) >= 10.1 {
		t.Fatalf("test setup expected dust order, got theoretical %f", out.TheoreticalUSD)
	}
	if math.Abs(math.Abs(out.OrderUSDT)-10.1) > 1e-12 {
		t.Fatalf("wedge order = %f, want ±10.1", out.OrderUSDT)
	}
}

func baseMicroInput(signal float64) MicroDecisionInput {
	return MicroDecisionInput{
		Price:                  100,
		TotalEquity:            1000,
		FloatBTC:               5,
		SpendableUSDT:          500,
		Signal:                 signal,
		Beta:                   1,
		Gamma:                  1,
		MarketBetaMultiplier:   1,
		VolatilityRatio:        1,
		DustUSD:                10.1,
		WedgeDeltaThreshold:    0.04,
		WedgeVolRatioThreshold: 1.6,
	}
}
