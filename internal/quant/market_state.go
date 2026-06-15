package quant

import "math"

const (
	MarketStateBullTrend = "BULL_TREND"
	MarketStateBearTrend = "BEAR_TREND"
	MarketStateQuiet     = "QUIET"
	MarketStateShock     = "SHOCK"
)

type MarketState struct {
	State                  string
	TimeDilationMultiplier float64
	BetaMultiplier         float64
	IsQuiet                bool
	VolatilityRatio        float64
	TrendSlope             float64
	DrawdownRatio          float64
}

func ComputeMarketState(closes []float64) MarketState {
	if len(closes) < MicroSignalEMABars+1 {
		return MarketState{
			State:                  MarketStateQuiet,
			TimeDilationMultiplier: 1.0,
			BetaMultiplier:         0.65,
			IsQuiet:                true,
			VolatilityRatio:        1.0,
		}
	}

	price := closes[len(closes)-1]
	emaFast := EMA(closes, MicroSignalEMABars)
	emaSlow := EMA(closes, MicroVolRatioLongBars)
	trendSlope := 0.0
	if emaSlow > 0 {
		trendSlope = emaFast/emaSlow - 1
	}

	logReturnN := 0.0
	if len(closes) > MicroSignalEMABars && closes[len(closes)-1-MicroSignalEMABars] > 0 {
		logReturnN = math.Log(price / closes[len(closes)-1-MicroSignalEMABars])
	}

	emaDeviation := 0.0
	if emaFast > 0 {
		emaDeviation = price/emaFast - 1
	}

	volatilityRatio := ComputeVolatilityRatio(closes)
	high := rollingHigh(closes, MicroVolRatioLongBars)
	drawdownRatio := 0.0
	if high > 0 {
		drawdownRatio = price/high - 1
	}

	if isShock(closes, volatilityRatio) {
		return MarketState{
			State:                  MarketStateShock,
			TimeDilationMultiplier: 1.5,
			BetaMultiplier:         1.4,
			IsQuiet:                false,
			VolatilityRatio:        volatilityRatio,
			TrendSlope:             trendSlope,
			DrawdownRatio:          drawdownRatio,
		}
	}

	if trendSlope < -0.03 || drawdownRatio < -0.20 {
		return MarketState{
			State:                  MarketStateBearTrend,
			TimeDilationMultiplier: 1.25,
			BetaMultiplier:         1.15,
			IsQuiet:                false,
			VolatilityRatio:        volatilityRatio,
			TrendSlope:             trendSlope,
			DrawdownRatio:          drawdownRatio,
		}
	}

	if trendSlope > 0.03 && logReturnN > 0 {
		return MarketState{
			State:                  MarketStateBullTrend,
			TimeDilationMultiplier: 0.85,
			BetaMultiplier:         1.0,
			IsQuiet:                false,
			VolatilityRatio:        volatilityRatio,
			TrendSlope:             trendSlope,
			DrawdownRatio:          drawdownRatio,
		}
	}

	isQuiet := volatilityRatio < 0.75 && math.Abs(emaDeviation) < 0.015
	if isQuiet {
		return MarketState{
			State:                  MarketStateQuiet,
			TimeDilationMultiplier: 1.0,
			BetaMultiplier:         0.65,
			IsQuiet:                true,
			VolatilityRatio:        volatilityRatio,
			TrendSlope:             trendSlope,
			DrawdownRatio:          drawdownRatio,
		}
	}

	return MarketState{
		State:                  MarketStateQuiet,
		TimeDilationMultiplier: 1.0,
		BetaMultiplier:         1.0,
		IsQuiet:                false,
		VolatilityRatio:        volatilityRatio,
		TrendSlope:             trendSlope,
		DrawdownRatio:          drawdownRatio,
	}
}

func isShock(closes []float64, volatilityRatio float64) bool {
	if volatilityRatio > 1.8 {
		return true
	}
	if len(closes) < MicroSignalStdDevBars+2 {
		return false
	}

	logReturns := make([]float64, 0, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		if closes[i-1] <= 0 || closes[i] <= 0 {
			continue
		}
		logReturns = append(logReturns, math.Log(closes[i]/closes[i-1]))
	}
	if len(logReturns) < 2 {
		return false
	}

	latest := math.Abs(logReturns[len(logReturns)-1])
	sigma := StdDev(logReturns, MicroSignalStdDevBars)
	return sigma > 0 && latest > 2.5*sigma
}
