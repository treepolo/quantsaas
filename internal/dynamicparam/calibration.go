package dynamicparam

import (
	"fmt"
	"math"
)

const CalibrationVersion = "p09-histogram-isotonic-v1"

type CalibrationBin struct {
	Lower        float64 `json:"lower"`
	Upper        float64 `json:"upper"`
	Count        int     `json:"count"`
	MeanForecast float64 `json:"mean_forecast"`
	ObservedRate float64 `json:"observed_rate"`
}

type ProbabilityCalibrator struct {
	SchemaVersion string    `json:"schema_version"`
	Boundaries    []float64 `json:"boundaries"`
	Values        []float64 `json:"values"`
}

func FitProbabilityCalibrator(probabilities, labels []float64, binCount int) (ProbabilityCalibrator, error) {
	if len(probabilities) != len(labels) || len(labels) < 8 {
		return ProbabilityCalibrator{}, fmt.Errorf("insufficient calibration samples")
	}
	if binCount < 2 {
		binCount = 10
	}
	counts := make([]float64, binCount)
	sums := make([]float64, binCount)
	prevalence := 0.0
	for index, probability := range probabilities {
		if !finite(probability) || probability < 0 || probability > 1 || (labels[index] != 0 && labels[index] != 1) {
			return ProbabilityCalibrator{}, fmt.Errorf("invalid calibration sample")
		}
		bin := int(math.Min(float64(binCount-1), math.Floor(probability*float64(binCount))))
		counts[bin]++
		sums[bin] += labels[index]
		prevalence += labels[index]
	}
	prevalence /= float64(len(labels))
	type isotonicBlock struct {
		start, end    int
		weight, value float64
	}
	blocks := make([]isotonicBlock, 0, binCount)
	for index := 0; index < binCount; index++ {
		// Two prevalence-weighted pseudo observations keep empty/small bins stable.
		weight := counts[index] + 2
		blocks = append(blocks, isotonicBlock{start: index, end: index, weight: weight, value: (sums[index] + 2*prevalence) / weight})
	}
	// Pool-adjacent-violators produces a deterministic monotone reliability map.
	for index := 0; index+1 < len(blocks); {
		if blocks[index].value <= blocks[index+1].value+1e-15 {
			index++
			continue
		}
		weight := blocks[index].weight + blocks[index+1].weight
		merged := isotonicBlock{start: blocks[index].start, end: blocks[index+1].end, weight: weight, value: (blocks[index].value*blocks[index].weight + blocks[index+1].value*blocks[index+1].weight) / weight}
		blocks = append(blocks[:index], append([]isotonicBlock{merged}, blocks[index+2:]...)...)
		if index > 0 {
			index--
		}
	}
	values := make([]float64, binCount)
	for _, block := range blocks {
		for index := block.start; index <= block.end; index++ {
			values[index] = block.value
		}
	}
	boundaries := make([]float64, binCount+1)
	for index := range boundaries {
		boundaries[index] = float64(index) / float64(binCount)
	}
	return ProbabilityCalibrator{SchemaVersion: CalibrationVersion, Boundaries: boundaries, Values: values}, nil
}

func (calibrator ProbabilityCalibrator) Predict(probability float64) (float64, error) {
	if calibrator.SchemaVersion != CalibrationVersion || len(calibrator.Boundaries) != len(calibrator.Values)+1 || len(calibrator.Values) == 0 || !finite(probability) {
		return 0, fmt.Errorf("invalid probability calibrator")
	}
	probability = math.Max(0, math.Min(1, probability))
	index := int(math.Min(float64(len(calibrator.Values)-1), math.Floor(probability*float64(len(calibrator.Values)))))
	return calibrator.Values[index], nil
}

func BuildReliabilityBins(probabilities, labels []float64, binCount int) []CalibrationBin {
	if len(probabilities) != len(labels) || len(labels) == 0 {
		return nil
	}
	if binCount < 2 {
		binCount = 10
	}
	result := make([]CalibrationBin, binCount)
	labelSums := make([]float64, binCount)
	for index := range result {
		result[index].Lower = float64(index) / float64(binCount)
		result[index].Upper = float64(index+1) / float64(binCount)
	}
	for index, probability := range probabilities {
		bin := int(math.Min(float64(binCount-1), math.Floor(math.Max(0, math.Min(1, probability))*float64(binCount))))
		result[bin].Count++
		result[bin].MeanForecast += probability
		labelSums[bin] += labels[index]
	}
	for index := range result {
		if result[index].Count > 0 {
			result[index].MeanForecast /= float64(result[index].Count)
			result[index].ObservedRate = labelSums[index] / float64(result[index].Count)
		}
	}
	return result
}
