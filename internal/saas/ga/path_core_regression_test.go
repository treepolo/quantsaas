package ga

import (
	"math"
	"testing"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

func TestSharedCoreMatchesLegacySigmoidDCAPath(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := make([]quant.Bar, 180)
	price := 100.0
	for i := range bars {
		price *= 1 + 0.004*math.Sin(float64(i)/7) - 0.001
		bars[i] = quant.Bar{
			OpenTime: start.AddDate(0, 0, i).UnixMilli(),
			Open:     price * (1 - 0.002*math.Cos(float64(i)/5)),
			High:     price * 1.01,
			Low:      price * 0.99,
			Close:    price,
			Volume:   1000 + float64(i),
		}
	}
	params := sigmoiddca.DefaultParams()
	params.Spawn.Policy.InitialUSDT = 1234
	params.Spawn.Policy.MonthlyInjectUSDT = 77
	params.PositionStructure = sigmoiddca.PositionStructureFloatingOnly
	params.Chromosome.RebalanceThreshold = 0.015
	params.Chromosome.ForceFullThreshold = 0.82
	params.Chromosome.ForceEmptyThreshold = 0.18
	costs := quant.ExecutionCostConfig{FeeRate: 0.001, SpreadRate: 0.0005}

	tests := []struct {
		mode        string
		roi         float64
		maxDrawdown float64
		finalEquity float64
		tradeCount  int
	}{
		{
			mode:        executionModeCloseSameBar,
			roi:         -0.12205202287406713,
			maxDrawdown: 0.06973231707688078,
			finalEquity: 1429.8440839422337,
			tradeCount:  9,
		},
		{
			mode:        executionModeCloseNextOpen,
			roi:         -0.11084077288102258,
			maxDrawdown: 0.0650770237423821,
			finalEquity: 1447.2192507985428,
			tradeCount:  8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			path := RunSigmoidDCAPathBacktestWithModeCostsAndStructure(
				bars,
				bars[100].OpenTime,
				"1d",
				tt.mode,
				params.Chromosome,
				&params.Spawn,
				costs,
				params.PositionStructure,
			)
			if len(path.NAV) != 80 {
				t.Fatalf("NAV points = %d, want 80", len(path.NAV))
			}
			assertNear(t, "ROI", path.Metrics.ROI, tt.roi)
			assertNear(t, "max drawdown", path.Metrics.MaxDrawdown, tt.maxDrawdown)
			assertNear(t, "final equity", path.Metrics.FinalEquity, tt.finalEquity)
			assertNear(t, "total injected", path.Metrics.TotalInjected, 1619)
			if path.Metrics.TradeCount != tt.tradeCount {
				t.Fatalf("trade count = %d, want %d", path.Metrics.TradeCount, tt.tradeCount)
			}
			first := path.NAV[0]
			last := path.NAV[len(path.NAV)-1]
			if first.TimeMs != 1744329600000 || last.TimeMs != 1751155200000 {
				t.Fatalf("NAV range = %d..%d, want legacy range", first.TimeMs, last.TimeMs)
			}
			assertNear(t, "last practical target", last.PracticalTargetWeight, 1)
			assertNear(t, "last model target", last.ModelTargetWeight, 0.9999999999876648)
			assertNear(t, "last empty reference", last.EmptyReferenceTargetWeight, 0.9999999999954621)
			assertNear(t, "last point equity", last.TotalEquity, tt.finalEquity)
			if last.Cash < -1e-9 || last.AssetQuantity < 0 || last.ActualExposureWeight < 0 || last.ActualExposureWeight > 1 {
				t.Fatalf("invalid standardized post-trade state: %+v", last)
			}
		})
	}
}

func assertNear(t *testing.T, name string, got float64, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %.15f, want %.15f", name, got, want)
	}
}
