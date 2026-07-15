package robustness

import (
	"math"
)

func ComputeRelativeMetrics(input RelativeMetricInput) (RelativeMetrics, error) {
	if !finitePositive(input.StrategyFinalNAV) || !finitePositive(input.BenchmarkFinalNAV) ||
		!finiteDrawdown(input.StrategyMaxDrawdown) || !finiteDrawdown(input.BenchmarkMaxDrawdown) {
		return RelativeMetrics{}, ErrInvalidMetricInput
	}
	finalRatio := input.StrategyFinalNAV / input.BenchmarkFinalNAV
	drawdownRatio := (1 - input.StrategyMaxDrawdown) / (1 - input.BenchmarkMaxDrawdown)
	if !finitePositive(finalRatio) || !finitePositive(drawdownRatio) {
		return RelativeMetrics{}, ErrInvalidMetricInput
	}
	logFinal := math.Log(finalRatio)
	logDrawdown := math.Log(drawdownRatio)
	return RelativeMetrics{
		Version:                  MetricsVersion,
		FinalNAVRatio:            finalRatio,
		LogFinalNAVRatio:         logFinal,
		DrawdownResidualRatio:    drawdownRatio,
		LogDrawdownResidualRatio: logDrawdown,
		PerformanceDrawdown:      logFinal * drawdownRatio,
		Qualified:                input.StrategyFinalNAV > input.BenchmarkFinalNAV && input.StrategyMaxDrawdown < input.BenchmarkMaxDrawdown,
	}, nil
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finiteDrawdown(value float64) bool {
	return value >= 0 && value < 1 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
