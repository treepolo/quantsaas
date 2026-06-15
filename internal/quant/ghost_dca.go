package quant

import (
	"math"
	"time"
)

type GhostDCAConfig struct {
	InitialUSDT       float64
	MonthlyInjectUSDT float64
}

type GhostDCAResult struct {
	FinalEquity   float64
	TotalInjected float64
	MaxDrawdown   float64
	ROI           float64
	NAV           []float64
}

type cashFlow struct {
	TimeMs int64
	Amount float64
}

func SimulateGhostDCA(bars []Bar, cfg GhostDCAConfig) GhostDCAResult {
	if len(bars) == 0 || bars[0].Close <= 0 || cfg.InitialUSDT <= 0 {
		return GhostDCAResult{}
	}

	btc := cfg.InitialUSDT / bars[0].Close
	totalInjected := cfg.InitialUSDT
	flows := make([]cashFlow, 0)
	nav := make([]float64, 0, len(bars))
	lastYear, lastMonth := yearMonth(bars[0].OpenTime)

	for i, bar := range bars {
		if bar.Close <= 0 {
			continue
		}

		year, month := yearMonth(bar.OpenTime)
		if i > 0 && (year != lastYear || month != lastMonth) && cfg.MonthlyInjectUSDT > 0 {
			btc += cfg.MonthlyInjectUSDT / bar.Close
			totalInjected += cfg.MonthlyInjectUSDT
			flows = append(flows, cashFlow{TimeMs: bar.OpenTime, Amount: cfg.MonthlyInjectUSDT})
			lastYear, lastMonth = year, month
		}

		nav = append(nav, btc*bar.Close)
	}

	finalEquity := 0.0
	if len(nav) > 0 {
		finalEquity = nav[len(nav)-1]
	}

	return GhostDCAResult{
		FinalEquity:   finalEquity,
		TotalInjected: totalInjected,
		MaxDrawdown:   MaxDrawdown(nav),
		ROI:           modifiedDietzROI(cfg.InitialUSDT, finalEquity, flows, bars[0].OpenTime, bars[len(bars)-1].OpenTime),
		NAV:           nav,
	}
}

func MaxDrawdown(nav []float64) float64 {
	if len(nav) == 0 {
		return 0
	}

	peak := nav[0]
	var maxDD float64
	for _, v := range nav {
		if v > peak {
			peak = v
			continue
		}
		if peak <= 0 {
			continue
		}
		dd := (peak - v) / peak
		if dd > maxDD {
			maxDD = dd
		}
	}
	return maxDD
}

func modifiedDietzROI(initial, final float64, flows []cashFlow, startMs, endMs int64) float64 {
	if initial <= 0 || final <= 0 || endMs <= startMs {
		return 0
	}

	totalDays := math.Max(1, float64(endMs-startMs)/float64(24*time.Hour/time.Millisecond))
	var flowSum float64
	denominator := initial
	for _, flow := range flows {
		flowSum += flow.Amount
		day := math.Max(0, float64(flow.TimeMs-startMs)/float64(24*time.Hour/time.Millisecond))
		weight := (totalDays - day) / totalDays
		denominator += flow.Amount * weight
	}
	if denominator == 0 {
		return 0
	}
	return (final - initial - flowSum) / denominator
}

func yearMonth(ms int64) (int, time.Month) {
	t := time.UnixMilli(ms).UTC()
	return t.Year(), t.Month()
}
