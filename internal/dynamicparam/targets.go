package dynamicparam

import (
	"fmt"
	"math"
	"sort"

	"quantsaas/internal/quant"
)

func BuildTargets(bars []quant.Bar, horizon int) ([]TargetPoint, error) {
	if horizon != HorizonOneDay && horizon != HorizonTwentyDay {
		return nil, fmt.Errorf("unsupported prediction horizon %d", horizon)
	}
	if len(bars) <= horizon {
		return nil, fmt.Errorf("prediction horizon %d requires more bars", horizon)
	}
	if err := validateBars(bars); err != nil {
		return nil, err
	}
	points := make([]TargetPoint, len(bars)-horizon)
	completedTotals := make([]float64, 0, len(points))
	for index := range points {
		completedIndex := index - horizon
		if completedIndex >= 0 {
			completedTotals = append(completedTotals, points[completedIndex].TotalSpace)
		}
		anchor := bars[index].Close
		up, down := 0.0, 0.0
		for future := index + 1; future <= index+horizon; future++ {
			up = math.Max(up, math.Log(bars[future].High/anchor))
			down = math.Max(down, math.Log(anchor/bars[future].Low))
		}
		up = math.Max(0, up)
		down = math.Max(0, down)
		point := TargetPoint{
			SchemaVersion: TargetSchemaVersion, Index: index, TimeMs: bars[index].OpenTime, Horizon: horizon,
			DirectionUp: bars[index+horizon].Close > anchor, UpSpace: up, DownSpace: down, TotalSpace: up + down,
		}
		point.FutureActivity = futureActivity(anchor, bars[index+1:index+horizon+1])
		if median, ok := positiveMedian(completedTotals); ok {
			point.CausalMedian = median
			point.NormalizedUp = up / median
			point.NormalizedDown = down / median
			point.Normalized = finite(point.NormalizedUp) && finite(point.NormalizedDown)
		}
		points[index] = point
	}
	return points, nil
}

func futureActivity(anchorClose float64, bars []quant.Bar) ActivityVector {
	tr := make([]float64, 0, len(bars))
	hl := make([]float64, 0, len(bars))
	parkTerms := make([]float64, 0, len(bars))
	gaps := make([]float64, 0, len(bars))
	closeOpen := make([]float64, 0, len(bars))
	rogersSatchell := 0.0
	previousClose := anchorClose
	for _, bar := range bars {
		trueRange := math.Max(bar.High-bar.Low, math.Max(math.Abs(bar.High-previousClose), math.Abs(bar.Low-previousClose))) / previousClose
		tr = append(tr, trueRange)
		highLow := math.Log(bar.High / bar.Low)
		hl = append(hl, highLow)
		parkTerms = append(parkTerms, highLow*highLow)
		gaps = append(gaps, math.Log(bar.Open/previousClose))
		closeOpen = append(closeOpen, math.Log(bar.Close/bar.Open))
		rogersSatchell += math.Log(bar.High/bar.Close)*math.Log(bar.High/bar.Open) + math.Log(bar.Low/bar.Close)*math.Log(bar.Low/bar.Open)
		previousClose = bar.Close
	}
	trMean, trStd := meanStd(tr)
	hlMean, hlStd := meanStd(hl)
	_, gapVariance := meanVariance(gaps)
	_, closeVariance := meanVariance(closeOpen)
	n := float64(len(bars))
	k := 0.34 / (1.34 + (n+1)/math.Max(1, n-1))
	yangZhang := math.Sqrt(math.Max(0, gapVariance+k*closeVariance+(1-k)*rogersSatchell/n))
	parkMean, _ := meanVariance(parkTerms)
	return ActivityVector{
		TRMean: trMean, TRStdDev: trStd, HighLowMean: hlMean, HighLowStdDev: hlStd,
		Parkinson: math.Sqrt(math.Max(0, parkMean/(4*math.Log(2)))), YangZhang: yangZhang,
	}
}

func BuildFeaturePoints(bars []quant.Bar, lookback int) ([]FeaturePoint, error) {
	return buildFeaturePoints(bars, lookback, true)
}

// BuildFeaturePointsWithoutRawSequence preserves the exact causal activity and
// history-ratio values from BuildFeaturePoints, but omits RawSequence. Consumers
// that only need the numerical market values must use this form so a long-lived
// cache does not retain one OHLC slice per bar.
func BuildFeaturePointsWithoutRawSequence(bars []quant.Bar, lookback int) ([]FeaturePoint, error) {
	return buildFeaturePoints(bars, lookback, false)
}

func buildFeaturePoints(bars []quant.Bar, lookback int, includeRawSequence bool) ([]FeaturePoint, error) {
	if lookback < 2 {
		return nil, fmt.Errorf("lookback must be at least 2")
	}
	if err := validateBars(bars); err != nil {
		return nil, err
	}
	result := make([]FeaturePoint, len(bars))
	history := make([]ActivityVector, 0, len(bars))
	for index := range bars {
		point := FeaturePoint{SchemaVersion: FeatureSchemaVersion, Index: index, TimeMs: bars[index].OpenTime, Lookback: lookback}
		if index+1 < lookback {
			point.UnavailableReason = "insufficient_lookback"
			result[index] = point
			continue
		}
		activity, err := computeActivity(bars[index-lookback+1 : index+1])
		if err != nil {
			point.UnavailableReason = err.Error()
			result[index] = point
			continue
		}
		point.Activity = activity
		if includeRawSequence {
			point.RawSequence = normalizedOHLC(bars[index-lookback+1 : index+1])
		}
		point.HistoryRatio = activityRatios(activity, history)
		point.Available = activityVectorValid(activity) && activityVectorValid(point.HistoryRatio)
		if !point.Available {
			point.UnavailableReason = "causal_history_median_unavailable"
		}
		result[index] = point
		history = append(history, activity)
	}
	return result, nil
}

func computeActivity(bars []quant.Bar) (ActivityVector, error) {
	if len(bars) < 2 {
		return ActivityVector{}, fmt.Errorf("insufficient activity window")
	}
	tr := make([]float64, 0, len(bars)-1)
	hl := make([]float64, 0, len(bars))
	parkTerms := make([]float64, 0, len(bars))
	openClose := make([]float64, 0, len(bars))
	closeOpen := make([]float64, 0, len(bars))
	for index, bar := range bars {
		if bar.Open <= 0 || bar.High <= 0 || bar.Low <= 0 || bar.Close <= 0 || bar.High < math.Max(bar.Open, bar.Close) || bar.Low > math.Min(bar.Open, bar.Close) {
			return ActivityVector{}, fmt.Errorf("invalid OHLC bar")
		}
		hlValue := math.Log(bar.High / bar.Low)
		hl = append(hl, hlValue)
		parkTerms = append(parkTerms, hlValue*hlValue)
		closeOpen = append(closeOpen, math.Log(bar.Close/bar.Open))
		if index > 0 {
			previousClose := bars[index-1].Close
			trueRange := math.Max(bar.High-bar.Low, math.Max(math.Abs(bar.High-previousClose), math.Abs(bar.Low-previousClose))) / previousClose
			tr = append(tr, trueRange)
			openClose = append(openClose, math.Log(bar.Open/previousClose))
		}
	}
	trMean, trStd := meanStd(tr)
	hlMean, hlStd := meanStd(hl)
	_, openVariance := meanVariance(openClose)
	_, closeVariance := meanVariance(closeOpen)
	rogersSatchell := 0.0
	for _, bar := range bars {
		rogersSatchell += math.Log(bar.High/bar.Close)*math.Log(bar.High/bar.Open) + math.Log(bar.Low/bar.Close)*math.Log(bar.Low/bar.Open)
	}
	rogersSatchell /= float64(len(bars))
	n := float64(len(bars))
	k := 0.34 / (1.34 + (n+1)/(n-1))
	yangZhang := math.Sqrt(math.Max(0, openVariance+k*closeVariance+(1-k)*rogersSatchell))
	parkMean, _ := meanVariance(parkTerms)
	parkinson := math.Sqrt(math.Max(0, parkMean/(4*math.Log(2))))
	return ActivityVector{TRMean: trMean, TRStdDev: trStd, HighLowMean: hlMean, HighLowStdDev: hlStd, Parkinson: parkinson, YangZhang: yangZhang}, nil
}

func normalizedOHLC(bars []quant.Bar) [][]float64 {
	result := make([][]float64, 0, len(bars)-1)
	for index := 1; index < len(bars); index++ {
		previous := bars[index-1].Close
		bar := bars[index]
		result = append(result, []float64{math.Log(bar.Open / previous), math.Log(bar.High / previous), math.Log(bar.Low / previous), math.Log(bar.Close / previous)})
	}
	return result
}

func activityRatios(current ActivityVector, history []ActivityVector) ActivityVector {
	return ActivityVector{
		TRMean:        ratioToMedian(current.TRMean, history, func(value ActivityVector) float64 { return value.TRMean }),
		TRStdDev:      ratioToMedian(current.TRStdDev, history, func(value ActivityVector) float64 { return value.TRStdDev }),
		HighLowMean:   ratioToMedian(current.HighLowMean, history, func(value ActivityVector) float64 { return value.HighLowMean }),
		HighLowStdDev: ratioToMedian(current.HighLowStdDev, history, func(value ActivityVector) float64 { return value.HighLowStdDev }),
		Parkinson:     ratioToMedian(current.Parkinson, history, func(value ActivityVector) float64 { return value.Parkinson }),
		YangZhang:     ratioToMedian(current.YangZhang, history, func(value ActivityVector) float64 { return value.YangZhang }),
	}
}

func ratioToMedian(current float64, history []ActivityVector, pick func(ActivityVector) float64) float64 {
	values := make([]float64, 0, len(history))
	for _, item := range history {
		if value := pick(item); value > 0 && finite(value) {
			values = append(values, value)
		}
	}
	median, ok := positiveMedian(values)
	if !ok {
		return math.NaN()
	}
	return current / median
}

func positiveMedian(values []float64) (float64, bool) {
	valid := make([]float64, 0, len(values))
	for _, value := range values {
		if value > 0 && finite(value) {
			valid = append(valid, value)
		}
	}
	if len(valid) == 0 {
		return 0, false
	}
	sort.Float64s(valid)
	middle := len(valid) / 2
	if len(valid)%2 == 1 {
		return valid[middle], true
	}
	return (valid[middle-1] + valid[middle]) / 2, true
}

func activityVectorValid(value ActivityVector) bool {
	return value.TRMean > 0 && finite(value.TRMean) && value.TRStdDev >= 0 && finite(value.TRStdDev) && value.HighLowMean > 0 && finite(value.HighLowMean) && value.HighLowStdDev >= 0 && finite(value.HighLowStdDev) && value.Parkinson > 0 && finite(value.Parkinson) && value.YangZhang > 0 && finite(value.YangZhang)
}

func validateBars(bars []quant.Bar) error {
	for index, bar := range bars {
		if bar.OpenTime <= 0 || bar.Open <= 0 || bar.High <= 0 || bar.Low <= 0 || bar.Close <= 0 || !finite(bar.Open) || !finite(bar.High) || !finite(bar.Low) || !finite(bar.Close) {
			return fmt.Errorf("bar %d is invalid", index)
		}
		if index > 0 && bar.OpenTime <= bars[index-1].OpenTime {
			return fmt.Errorf("bars are not strictly ordered")
		}
	}
	return nil
}

func meanStd(values []float64) (float64, float64) {
	mean, variance := meanVariance(values)
	return mean, math.Sqrt(math.Max(0, variance))
}

func meanVariance(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	mean := 0.0
	for _, value := range values {
		mean += value
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, value := range values {
		delta := value - mean
		variance += delta * delta
	}
	if len(values) > 1 {
		variance /= float64(len(values) - 1)
	}
	return mean, variance
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
