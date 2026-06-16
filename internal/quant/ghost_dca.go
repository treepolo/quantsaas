package quant

import (
	"math"
	"time"
)

type GhostDCAConfig struct {
	InitialUSDT       float64
	MonthlyInjectUSDT float64
	UseOpenExecution  bool
}

type GhostDCAResult struct {
	FinalEquity   float64
	TotalInjected float64
	MaxDrawdown   float64
	ROI           float64
	Times         []int64
	NAV           []float64
}

type TimedCashFlow struct {
	TimeMs int64
	Amount float64
}

func SimulateGhostDCA(bars []Bar, cfg GhostDCAConfig) GhostDCAResult {
	if len(bars) == 0 {
		return GhostDCAResult{}
	}
	return SimulateGhostDCAFrom(bars, bars[0].OpenTime, cfg)
}

func SimulateGhostDCAFrom(bars []Bar, evalStartMs int64, cfg GhostDCAConfig) GhostDCAResult {
	if len(bars) == 0 || bars[0].Close <= 0 || cfg.InitialUSDT <= 0 {
		return GhostDCAResult{}
	}

	initialPrice := ghostDCAExecutionPrice(bars[0], cfg.UseOpenExecution)
	if initialPrice <= 0 {
		return GhostDCAResult{}
	}
	btc := cfg.InitialUSDT / initialPrice
	evalInjected := 0.0
	flows := make([]TimedCashFlow, 0)
	times := make([]int64, 0, len(bars))
	nav := make([]float64, 0, len(bars))
	lastYear, lastMonth := yearMonth(bars[0].OpenTime)
	evalInitial := 0.0
	actualEvalStart := int64(0)

	for i, bar := range bars {
		if bar.Close <= 0 {
			continue
		}

		year, month := yearMonth(bar.OpenTime)
		if i > 0 && (year != lastYear || month != lastMonth) && cfg.MonthlyInjectUSDT > 0 {
			executionPrice := ghostDCAExecutionPrice(bar, cfg.UseOpenExecution)
			if executionPrice <= 0 {
				return GhostDCAResult{}
			}
			btc += cfg.MonthlyInjectUSDT / executionPrice
			if bar.OpenTime > evalStartMs {
				evalInjected += cfg.MonthlyInjectUSDT
				flows = append(flows, TimedCashFlow{TimeMs: bar.OpenTime, Amount: cfg.MonthlyInjectUSDT})
			}
			lastYear, lastMonth = year, month
		}

		equity := btc * bar.Close
		if bar.OpenTime >= evalStartMs {
			if len(nav) == 0 {
				evalInitial = equity
				actualEvalStart = bar.OpenTime
			}
			times = append(times, bar.OpenTime)
			nav = append(nav, equity)
		}
	}

	finalEquity := 0.0
	if len(nav) > 0 {
		finalEquity = nav[len(nav)-1]
	}

	return GhostDCAResult{
		FinalEquity:   finalEquity,
		TotalInjected: evalInitial + evalInjected,
		MaxDrawdown:   MaxDrawdown(nav),
		ROI:           ModifiedDietzROI(evalInitial, finalEquity, flows, actualEvalStart, bars[len(bars)-1].OpenTime),
		Times:         times,
		NAV:           nav,
	}
}

func ghostDCAExecutionPrice(bar Bar, useOpenExecution bool) float64 {
	if useOpenExecution {
		return bar.Open
	}
	return bar.Close
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

func ModifiedDietzROI(initial, final float64, flows []TimedCashFlow, startMs, endMs int64) float64 {
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
