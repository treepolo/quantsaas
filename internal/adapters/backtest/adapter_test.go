package backtest

import (
	"reflect"
	"testing"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/saas/ga"
)

func TestSigmoidBacktestDeterministic(t *testing.T) {
	bars := syntheticBars(240)
	chromosome := quant.DefaultSeedChromosome
	spawn := &quant.SpawnPoint{
		Policy: quant.CapitalPolicy{
			InitialUSDT:       1000,
			MonthlyInjectUSDT: 100,
		},
		Risk: quant.RiskBounds{
			MaxDrawdownPct: 0.88,
			LotStep:        0.000001,
			LotMin:         0.00001,
		},
	}

	a := ga.RunSigmoidDCASingleBacktest(bars, bars[120].OpenTime, chromosome, spawn)
	b := ga.RunSigmoidDCASingleBacktest(bars, bars[120].OpenTime, chromosome, spawn)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("backtest should be deterministic\nfirst=%+v\nsecond=%+v", a, b)
	}
}

func syntheticBars(n int) []quant.Bar {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := make([]quant.Bar, 0, n)
	price := 100.0
	for i := 0; i < n; i++ {
		price *= 1 + 0.001
		if i%17 == 0 {
			price *= 0.985
		}
		bars = append(bars, quant.Bar{
			OpenTime: start.AddDate(0, 0, i).UnixMilli(),
			Open:     price * 0.99,
			High:     price * 1.01,
			Low:      price * 0.98,
			Close:    price,
			Volume:   1000,
		})
	}
	return bars
}
