package dynamicparam

import (
	"fmt"
	"math"
	"math/bits"
	"sort"
)

var activityMetricNames = []string{"tr_mean", "tr_std_dev", "high_low_mean", "high_low_std_dev", "parkinson", "yang_zhang"}

type activityScaleRow struct {
	values [6]float64
	target float64
}

type ActivityScaleModel struct {
	SchemaVersion          string             `json:"schema_version"`
	Constant               float64            `json:"constant"`
	Weights                map[string]float64 `json:"weights"`
	MeanAbsoluteLogRatio   float64            `json:"mean_absolute_log_ratio"`
	MedianAbsoluteLogRatio float64            `json:"median_absolute_log_ratio"`
	P90AbsoluteLogRatio    float64            `json:"p90_absolute_log_ratio"`
}

func TrainActivityScale(features []FeaturePoint, targets []TargetPoint) (ActivityScaleModel, error) {
	rows := make([]activityScaleRow, 0, len(targets))
	for _, target := range targets {
		if target.Index < 0 || target.Index >= len(features) || !features[target.Index].Available || target.TotalSpace <= 0 {
			continue
		}
		activity := features[target.Index].Activity
		values := [6]float64{activity.TRMean, activity.TRStdDev, activity.HighLowMean, activity.HighLowStdDev, activity.Parkinson, activity.YangZhang}
		valid := false
		for _, value := range values {
			if value < 0 || !finite(value) {
				valid = false
				break
			}
			if value > 0 {
				valid = true
			}
		}
		if valid {
			rows = append(rows, activityScaleRow{values: values, target: target.TotalSpace})
		}
	}
	if len(rows) < 20 {
		return ActivityScaleModel{}, fmt.Errorf("insufficient samples for normal activity scale")
	}
	bestMask, bestLoss, bestSE := 1, math.Inf(1), 0.0
	losses := map[int]float64{}
	for mask := 1; mask < 1<<len(activityMetricNames); mask++ {
		validRows := activityRowsForMask(rows, mask)
		if len(validRows) < 20 {
			continue
		}
		trainEnd := len(validRows) * 4 / 5
		model := fitActivityScale(validRows[:trainEnd], mask)
		errors := activityScaleErrors(model, validRows[trainEnd:])
		mean, variance := meanVariance(errors)
		losses[mask] = mean
		if mean < bestLoss {
			bestMask, bestLoss = mask, mean
			bestSE = math.Sqrt(variance / math.Max(1, float64(len(errors))))
		}
	}
	if len(losses) == 0 {
		return ActivityScaleModel{}, fmt.Errorf("no activity-scale subset has sufficient positive support")
	}
	selected := bestMask
	for mask, loss := range losses {
		if loss <= bestLoss+bestSE && (bits.OnesCount(uint(mask)) < bits.OnesCount(uint(selected)) || (bits.OnesCount(uint(mask)) == bits.OnesCount(uint(selected)) && mask < selected)) {
			selected = mask
		}
	}
	selectedRows := activityRowsForMask(rows, selected)
	model := fitActivityScale(selectedRows, selected)
	errors := activityScaleErrors(model, selectedRows)
	sort.Float64s(errors)
	model.MeanAbsoluteLogRatio, _ = meanVariance(errors)
	model.MedianAbsoluteLogRatio = quantileSorted(errors, 0.5)
	model.P90AbsoluteLogRatio = quantileSorted(errors, 0.9)
	return model, nil
}

func activityRowsForMask(rows []activityScaleRow, mask int) []activityScaleRow {
	result := make([]activityScaleRow, 0, len(rows))
	for _, row := range rows {
		valid := true
		for index, value := range row.values {
			if mask&(1<<index) != 0 && (value <= 0 || !finite(value)) {
				valid = false
				break
			}
		}
		if valid {
			result = append(result, row)
		}
	}
	return result
}

func (model ActivityScaleModel) Predict(feature FeaturePoint) (float64, error) {
	if model.SchemaVersion != ActivityScaleVersion || !feature.Available {
		return 0, fmt.Errorf("activity scale model or feature is unavailable")
	}
	return model.PredictActivity(feature.Activity)
}

func (model ActivityScaleModel) PredictActivity(activity ActivityVector) (float64, error) {
	if model.SchemaVersion != ActivityScaleVersion {
		return 0, fmt.Errorf("activity scale model is unavailable")
	}
	values := map[string]float64{"tr_mean": activity.TRMean, "tr_std_dev": activity.TRStdDev, "high_low_mean": activity.HighLowMean, "high_low_std_dev": activity.HighLowStdDev, "parkinson": activity.Parkinson, "yang_zhang": activity.YangZhang}
	logValue := math.Log(model.Constant)
	sum := 0.0
	for name, weight := range model.Weights {
		value := values[name]
		if value <= 0 || weight < 0 {
			return 0, fmt.Errorf("invalid activity scale input")
		}
		logValue += weight * math.Log(value)
		sum += weight
	}
	if math.Abs(sum-1) > 1e-8 {
		return 0, fmt.Errorf("activity scale weights do not sum to one")
	}
	result := math.Exp(logValue)
	if result <= 0 || !finite(result) {
		return 0, fmt.Errorf("invalid activity scale prediction")
	}
	return result, nil
}

func fitActivityScale(rows []activityScaleRow, mask int) ActivityScaleModel {
	active := make([]int, 0)
	for index := 0; index < 6; index++ {
		if mask&(1<<index) != 0 {
			active = append(active, index)
		}
	}
	weights := make([]float64, len(active))
	for index := range weights {
		weights[index] = 1 / float64(len(weights))
	}
	logConstant := 0.0
	for iteration := 0; iteration < 300; iteration++ {
		gradientConstant := 0.0
		gradient := make([]float64, len(weights))
		for _, row := range rows {
			prediction := logConstant
			for position, index := range active {
				prediction += weights[position] * math.Log(row.values[index])
			}
			residual := prediction - math.Log(row.target)
			gradientConstant += residual
			for position, index := range active {
				gradient[position] += residual * math.Log(row.values[index])
			}
		}
		rate := 0.02 / float64(len(rows))
		logConstant -= rate * gradientConstant
		for index := range weights {
			weights[index] -= rate * gradient[index]
		}
		weights = projectSimplex(weights)
	}
	result := ActivityScaleModel{SchemaVersion: ActivityScaleVersion, Constant: math.Exp(logConstant), Weights: map[string]float64{}}
	for position, index := range active {
		result.Weights[activityMetricNames[index]] = weights[position]
	}
	return result
}

func activityScaleErrors(model ActivityScaleModel, rows []activityScaleRow) []float64 {
	result := make([]float64, 0, len(rows))
	for _, row := range rows {
		logPrediction := math.Log(model.Constant)
		for name, weight := range model.Weights {
			logPrediction += weight * math.Log(row.values[activityMetricIndex(name)])
		}
		result = append(result, math.Abs(logPrediction-math.Log(row.target)))
	}
	return result
}

func activityMetricIndex(name string) int {
	for index, candidate := range activityMetricNames {
		if candidate == name {
			return index
		}
	}
	return -1
}

func projectSimplex(values []float64) []float64 {
	sorted := append([]float64(nil), values...)
	sort.Sort(sort.Reverse(sort.Float64Slice(sorted)))
	cumulative, threshold := 0.0, 0.0
	for index, value := range sorted {
		cumulative += value
		candidate := (cumulative - 1) / float64(index+1)
		if index == len(sorted)-1 || sorted[index+1] <= candidate {
			threshold = candidate
			break
		}
	}
	projected := make([]float64, len(values))
	for index, value := range values {
		projected[index] = math.Max(0, value-threshold)
	}
	return projected
}

func quantileSorted(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	position := quantile * float64(len(values)-1)
	lower, upper := int(math.Floor(position)), int(math.Ceil(position))
	if lower == upper {
		return values[lower]
	}
	fraction := position - float64(lower)
	return values[lower]*(1-fraction) + values[upper]*fraction
}
