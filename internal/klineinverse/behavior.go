package klineinverse

import (
	"fmt"
	"math"
	"sort"
)

var FeatureNames = [20]string{
	"w_mean_return", "w_trend_efficiency", "w_drawdown_shape", "w_runup_shape", "w_direction_reversal",
	"w_mean_tr", "w_activity_concentration", "w_gap_share", "w_wick_share", "w_wick_asymmetry",
	"h_mean_return", "h_trend_efficiency", "h_drawdown_shape", "h_runup_shape", "h_direction_reversal",
	"h_mean_tr", "h_activity_concentration", "h_gap_share", "h_wick_share", "h_wick_asymmetry",
}

func Features(path Path) (Behavior, error) {
	if err := validatePath(path); err != nil {
		return Behavior{}, err
	}
	return Behavior{
		Warmup:     segmentFeatures(path.Coordinates[:path.WarmupLength]),
		Evaluation: segmentFeatures(path.Coordinates[path.WarmupLength:]),
	}, nil
}

func FeatureVector(behavior Behavior) [20]float64 {
	w := segmentVector(behavior.Warmup)
	h := segmentVector(behavior.Evaluation)
	var result [20]float64
	copy(result[:10], w[:])
	copy(result[10:], h[:])
	return result
}

func BuildFeatureRange(behaviors []Behavior) (FeatureRange, error) {
	if len(behaviors) == 0 {
		return FeatureRange{}, fmt.Errorf("%w：CVT 校準特徵不可為空", ErrInvalidPath)
	}
	first := FeatureVector(behaviors[0])
	result := FeatureRange{Min: first, Max: first}
	for _, behavior := range behaviors[1:] {
		vector := FeatureVector(behavior)
		for index, value := range vector {
			if !finite(value) {
				return FeatureRange{}, fmt.Errorf("%w：特徵含非有限值", ErrInvalidPath)
			}
			result.Min[index] = math.Min(result.Min[index], value)
			result.Max[index] = math.Max(result.Max[index], value)
		}
	}
	return result, nil
}

func NormalizeFeatures(behavior Behavior, ranges FeatureRange) [20]float64 {
	vector := FeatureVector(behavior)
	for index := range vector {
		vector[index] = normalizeChannel(vector[index], ranges.Min[index], ranges.Max[index])
	}
	return vector
}

func PathDistance(a, b Path, bounds Bounds) (Distance, error) {
	if err := validatePath(a); err != nil {
		return Distance{}, err
	}
	if err := validatePath(b); err != nil {
		return Distance{}, err
	}
	if a.WarmupLength != b.WarmupLength || a.EvaluationLength != b.EvaluationLength || len(a.Dates) != len(b.Dates) {
		return Distance{}, fmt.Errorf("%w：比較路徑 W/H 不一致", ErrInvalidPath)
	}
	for index := range a.Dates {
		if a.Dates[index] != b.Dates[index] {
			return Distance{}, fmt.Errorf("%w：比較路徑日期不一致", ErrInvalidPath)
		}
	}
	normalA, normalB := make([]Coordinate, len(a.Coordinates)), make([]Coordinate, len(b.Coordinates))
	for index := range a.Coordinates {
		var err error
		if normalA[index], err = Normalize(a.Coordinates[index], bounds); err != nil {
			return Distance{}, err
		}
		if normalB[index], err = Normalize(b.Coordinates[index], bounds); err != nil {
			return Distance{}, err
		}
	}
	dw := segmentDistance(normalA[:a.WarmupLength], normalB[:b.WarmupLength])
	dh := segmentDistance(normalA[a.WarmupLength:], normalB[b.WarmupLength:])
	return Distance{Warmup: dw, Evaluation: dh, Total: math.Sqrt((dw*dw + dh*dh) / 2)}, nil
}

func Classify(strategyInitial, strategyFinal, dcaFinal float64) (Outcome, error) {
	if strategyInitial <= 0 || strategyFinal <= 0 || dcaFinal <= 0 || !finite(strategyInitial) || !finite(strategyFinal) || !finite(dcaFinal) {
		return Outcome{}, fmt.Errorf("%w：績效值必須為有限正數", ErrInvalidPath)
	}
	outcome := Outcome{QRelative: math.Log(strategyFinal / dcaFinal), QAbsolute: strategyFinal/strategyInitial - 1}
	outcome.PassA = outcome.QRelative > 0
	outcome.PassB = outcome.PassA && outcome.QAbsolute > 0
	switch {
	case outcome.PassB:
		outcome.State = StateAB
	case outcome.PassA:
		outcome.State = StateAOnly
	case outcome.QAbsolute > 0:
		outcome.State = StatePositiveButBelowDCA
	default:
		outcome.State = StateNeither
	}
	return outcome, nil
}

func segmentFeatures(coordinates []Coordinate) SegmentFeatures {
	if len(coordinates) == 0 {
		return SegmentFeatures{}
	}
	trueRanges := make([]float64, len(coordinates))
	var sumReturn, totalMovement, sumTR, sumGap, sumBody, sumWicks, sumWickDenominator, sumWickDifference float64
	nonzeroSigns := make([]int, 0, len(coordinates))
	cumulative, peak, trough, maxDrawdown, maxRunup := 0.0, 0.0, 0.0, 0.0, 0.0
	for index, coordinate := range coordinates {
		r := coordinate.G + coordinate.B
		highFromPrevious := coordinate.G + math.Max(0, coordinate.B) + coordinate.U
		lowFromPrevious := coordinate.G + math.Min(0, coordinate.B) - coordinate.D
		tr := math.Max(math.Abs(coordinate.B)+coordinate.U+coordinate.D, math.Max(math.Abs(highFromPrevious), math.Abs(lowFromPrevious)))
		trueRanges[index] = tr
		sumReturn += r
		totalMovement += math.Abs(r)
		sumTR += tr
		sumGap += math.Abs(coordinate.G)
		sumBody += math.Abs(coordinate.B)
		sumWicks += coordinate.U + coordinate.D
		sumWickDenominator += math.Abs(coordinate.B) + coordinate.U + coordinate.D
		sumWickDifference += coordinate.U - coordinate.D
		if r > 0 {
			nonzeroSigns = append(nonzeroSigns, 1)
		} else if r < 0 {
			nonzeroSigns = append(nonzeroSigns, -1)
		}
		cumulative += r
		peak = math.Max(peak, cumulative)
		trough = math.Min(trough, cumulative)
		maxDrawdown = math.Max(maxDrawdown, peak-cumulative)
		maxRunup = math.Max(maxRunup, cumulative-trough)
	}
	result := SegmentFeatures{MeanReturn: sumReturn / float64(len(coordinates)), MeanTR: sumTR / float64(len(coordinates))}
	if totalMovement > 0 {
		result.TrendEfficiency = math.Abs(sumReturn) / totalMovement
		result.DrawdownShape = maxDrawdown / totalMovement
		result.RunupShape = maxRunup / totalMovement
	}
	if len(nonzeroSigns) >= 2 {
		changes := 0
		for index := 1; index < len(nonzeroSigns); index++ {
			if nonzeroSigns[index] != nonzeroSigns[index-1] {
				changes++
			}
		}
		result.DirectionReversal = float64(changes) / float64(len(nonzeroSigns)-1)
	}
	if sumTR > 0 && len(coordinates) > 1 {
		squared := 0.0
		for _, value := range trueRanges {
			weight := value / sumTR
			squared += weight * weight
		}
		n := float64(len(coordinates))
		result.ActivityConcentration = (n*squared - 1) / (n - 1)
	}
	if sumGap+sumBody > 0 {
		result.GapShare = sumGap / (sumGap + sumBody)
	}
	if sumWickDenominator > 0 {
		result.WickShare = sumWicks / sumWickDenominator
	}
	if sumWicks > 0 {
		result.WickAsymmetry = sumWickDifference / sumWicks
	}
	return result
}

func segmentVector(value SegmentFeatures) [10]float64 {
	return [10]float64{value.MeanReturn, value.TrendEfficiency, value.DrawdownShape, value.RunupShape, value.DirectionReversal, value.MeanTR, value.ActivityConcentration, value.GapShare, value.WickShare, value.WickAsymmetry}
}

func segmentDistance(a, b []Coordinate) float64 {
	if len(a) == 0 {
		return 0
	}
	scales := []int{}
	for scale := 1; scale < len(a); scale *= 2 {
		scales = append(scales, scale)
	}
	if len(scales) == 0 || scales[len(scales)-1] != len(a) {
		scales = append(scales, len(a))
	}
	var squared float64
	count := 0
	for _, partitions := range scales {
		for partition := 0; partition < partitions; partition++ {
			start, end := partition*len(a)/partitions, (partition+1)*len(a)/partitions
			meanA, meanB := meanCoordinates(a[start:end]), meanCoordinates(b[start:end])
			for _, difference := range []float64{meanA.G - meanB.G, meanA.B - meanB.B, meanA.U - meanB.U, meanA.D - meanB.D} {
				squared += difference * difference
				count++
			}
		}
	}
	return math.Sqrt(squared / float64(count))
}

func meanCoordinates(values []Coordinate) Coordinate {
	result := Coordinate{}
	for _, value := range values {
		result.G += value.G
		result.B += value.B
		result.U += value.U
		result.D += value.D
	}
	n := float64(len(values))
	result.G /= n
	result.B /= n
	result.U /= n
	result.D /= n
	return result
}

func Quantiles(values []float64) map[string]float64 {
	if len(values) == 0 {
		return map[string]float64{}
	}
	sortedValues := append([]float64(nil), values...)
	sort.Float64s(sortedValues)
	return map[string]float64{"min": sortedValues[0], "p10": linearQuantile(sortedValues, .1), "median": linearQuantile(sortedValues, .5), "p90": linearQuantile(sortedValues, .9), "max": sortedValues[len(sortedValues)-1]}
}

func linearQuantile(values []float64, p float64) float64 {
	position := p * float64(len(values)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return values[lower]
	}
	weight := position - float64(lower)
	return values[lower]*(1-weight) + values[upper]*weight
}
