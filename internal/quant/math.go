package quant

import "math"

func EMA(values []float64, period int) float64 {
	if len(values) == 0 {
		return 0
	}
	if period <= 1 {
		return values[len(values)-1]
	}

	alpha := 2.0 / float64(period+1)
	ema := values[0]
	for _, v := range values[1:] {
		ema = alpha*v + (1-alpha)*ema
	}
	return ema
}

func StdDev(values []float64, period int) float64 {
	window := tail(values, period)
	if len(window) < 2 {
		return 0
	}

	var sum float64
	for _, v := range window {
		sum += v
	}
	mean := sum / float64(len(window))

	var variance float64
	for _, v := range window {
		diff := v - mean
		variance += diff * diff
	}
	return math.Sqrt(variance / float64(len(window)-1))
}

func MAVAbsChange(closes []float64, period int) float64 {
	window := tail(closes, period)
	if len(window) < 2 {
		return 0
	}

	var sum float64
	for i := 1; i < len(window); i++ {
		sum += math.Abs(window[i] - window[i-1])
	}
	return sum / float64(len(window)-1)
}

func ClipFloat64(v, lo, hi float64) float64 {
	if lo > hi {
		lo, hi = hi, lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func RoundToUSDT(v float64) float64 {
	return math.Round(v*100) / 100
}

func tail(values []float64, period int) []float64 {
	if len(values) == 0 {
		return nil
	}
	if period <= 0 || period >= len(values) {
		return values
	}
	return values[len(values)-period:]
}
