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
