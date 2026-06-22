package quant

import "testing"

func TestGhostDCAUsesOpenExecutionWhenRequested(t *testing.T) {
	bars := []Bar{
		{OpenTime: 1, Open: 100, Close: 200},
		{OpenTime: 2, Open: 100, Close: 200},
	}

	closeResult := SimulateGhostDCAFrom(bars, bars[0].OpenTime, GhostDCAConfig{
		InitialUSDT:      1000,
		UseOpenExecution: false,
	})
	openResult := SimulateGhostDCAFrom(bars, bars[0].OpenTime, GhostDCAConfig{
		InitialUSDT:      1000,
		UseOpenExecution: true,
	})

	if closeResult.FinalEquity != 1000 {
		t.Fatalf("close execution final equity = %f, want 1000", closeResult.FinalEquity)
	}
	if openResult.FinalEquity != 2000 {
		t.Fatalf("open execution final equity = %f, want 2000", openResult.FinalEquity)
	}
}

func TestGhostDCAAppliesExecutionCosts(t *testing.T) {
	bars := []Bar{
		{OpenTime: 1, Open: 100, Close: 100},
		{OpenTime: 2, Open: 100, Close: 100},
	}

	noCost := SimulateGhostDCAFrom(bars, bars[0].OpenTime, GhostDCAConfig{
		InitialUSDT: 1000,
	})
	withCost := SimulateGhostDCAFrom(bars, bars[0].OpenTime, GhostDCAConfig{
		InitialUSDT: 1000,
		Costs: ExecutionCostConfig{
			FeeRate:    0.01,
			SpreadRate: 0.01,
		},
	})

	if noCost.FinalEquity != 1000 {
		t.Fatalf("no-cost final equity = %f, want 1000", noCost.FinalEquity)
	}
	if withCost.FinalEquity >= noCost.FinalEquity {
		t.Fatalf("costed final equity = %f, want below %f", withCost.FinalEquity, noCost.FinalEquity)
	}
}
