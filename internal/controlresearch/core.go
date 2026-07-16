package controlresearch

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"

	robust "quantsaas/internal/robustness"
)

const (
	RandomGeneratorVersion = "p11-discrete-uniform-v1"
	ShuffleVersion         = "p11-fisher-yates-v1"
	StatisticsVersion      = "p11-empirical-midrank-v1"
)

var ErrInvalidInput = errors.New("對照研究輸入無效")

type Sample struct {
	Index       int                `json:"index"`
	Coordinates []int              `json:"coordinates"`
	Parameters  map[string]float64 `json:"parameters"`
}

type Batch struct {
	Version        string         `json:"version"`
	Seed           int64          `json:"seed"`
	Samples        []Sample       `json:"samples"`
	AttemptCount   int            `json:"attempt_count"`
	RejectionCount int            `json:"rejection_count"`
	RejectReasons  map[string]int `json:"reject_reasons"`
}

type Distribution struct {
	Count  int     `json:"count"`
	Min    float64 `json:"min"`
	P05    float64 `json:"p05"`
	P25    float64 `json:"p25"`
	Median float64 `json:"median"`
	P75    float64 `json:"p75"`
	P95    float64 `json:"p95"`
	Max    float64 `json:"max"`
}

// GenerateDiscrete samples canonical legal grid coordinates without
// replacement. The validator is part of the pure domain boundary and must not
// perform I/O. Re-running with the same inputs preserves every existing prefix.
func GenerateDiscrete(space robust.ParameterSpace, count int, seed int64, validator func(map[string]float64) error) (Batch, error) {
	if count < 1 || robust.ValidateSpace(space) != nil || len(space.Axes) == 0 {
		return Batch{}, ErrInvalidInput
	}
	capacity := uint64(1)
	saturated := false
	for _, axis := range space.Axes {
		if len(axis.Values) == 0 {
			return Batch{}, ErrInvalidInput
		}
		if capacity > math.MaxUint64/uint64(len(axis.Values)) {
			saturated = true
			capacity = math.MaxUint64
		} else if !saturated {
			capacity *= uint64(len(axis.Values))
		}
	}
	if !saturated && uint64(len(space.ExcludedCoordinates)) <= capacity && uint64(count) > capacity-uint64(len(space.ExcludedCoordinates)) {
		return Batch{}, fmt.Errorf("%w：要求數量超過合法離散空間", ErrInvalidInput)
	}
	excluded := make(map[string]bool, len(space.ExcludedCoordinates))
	for _, coordinate := range space.ExcludedCoordinates {
		excluded[robust.CoordinateKey(coordinate)] = true
	}
	rng := newRNG(uint64(seed))
	seen := map[string]bool{}
	batch := Batch{Version: RandomGeneratorVersion, Seed: seed, Samples: make([]Sample, 0, count), RejectReasons: map[string]int{}}
	maxAttempts := count * 1000
	if !saturated && capacity < uint64(maxAttempts) {
		maxAttempts = int(capacity) * 10
	}
	for len(batch.Samples) < count && batch.AttemptCount < maxAttempts {
		batch.AttemptCount++
		coordinate := make([]int, len(space.Axes))
		parameters := make(map[string]float64, len(space.Fixed)+len(space.Axes))
		for name, value := range space.Fixed {
			parameters[name] = value
		}
		for i, axis := range space.Axes {
			coordinate[i] = int(rng.next() % uint64(len(axis.Values)))
			parameters[axis.Name] = axis.Values[coordinate[i]]
		}
		key := robust.CoordinateKey(coordinate)
		reason := ""
		switch {
		case seen[key]:
			reason = "duplicate"
		case excluded[key]:
			reason = "excluded_coordinate"
		case validator != nil:
			if err := validator(parameters); err != nil {
				reason = "structural_constraint"
			}
		}
		if reason != "" {
			batch.RejectionCount++
			batch.RejectReasons[reason]++
			continue
		}
		seen[key] = true
		batch.Samples = append(batch.Samples, Sample{Index: len(batch.Samples), Coordinates: coordinate, Parameters: parameters})
	}
	if len(batch.Samples) != count {
		return Batch{}, fmt.Errorf("%w：在 %d 次嘗試後只取得 %d 組合法參數", ErrInvalidInput, batch.AttemptCount, len(batch.Samples))
	}
	return batch, nil
}

// Shuffle returns a deterministic Fisher-Yates permutation for one sequence
// index. It preserves the exact multiset, including repeated exposure values.
func Shuffle(values []float64, seed int64, sequence int) ([]float64, error) {
	if len(values) == 0 || sequence < 0 {
		return nil, ErrInvalidInput
	}
	result := append([]float64(nil), values...)
	for _, value := range result {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return nil, ErrInvalidInput
		}
	}
	rng := newRNG(mixSeed(seed, sequence))
	for i := len(result) - 1; i > 0; i-- {
		j := int(rng.next() % uint64(i+1))
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

// Percentile uses empirical mid-rank. higherIsBetter=false reverses the rank
// for risk metrics such as drawdown and underwater duration.
func Percentile(value float64, sample []float64, higherIsBetter bool) (float64, error) {
	if !finite(value) || len(sample) == 0 {
		return 0, ErrInvalidInput
	}
	less, equal, greater := 0, 0, 0
	for _, candidate := range sample {
		if !finite(candidate) {
			return 0, ErrInvalidInput
		}
		if candidate < value-1e-12 {
			less++
		} else if candidate > value+1e-12 {
			greater++
		} else {
			equal++
		}
	}
	better := float64(less) + 0.5*float64(equal)
	if !higherIsBetter {
		better = float64(greater) + 0.5*float64(equal)
	}
	return 100 * better / float64(len(sample)), nil
}

func Summarize(values []float64) (Distribution, error) {
	if len(values) == 0 {
		return Distribution{}, ErrInvalidInput
	}
	sorted := append([]float64(nil), values...)
	for _, value := range sorted {
		if !finite(value) {
			return Distribution{}, ErrInvalidInput
		}
	}
	sort.Float64s(sorted)
	return Distribution{Count: len(sorted), Min: sorted[0], P05: quantile(sorted, .05), P25: quantile(sorted, .25), Median: quantile(sorted, .5), P75: quantile(sorted, .75), P95: quantile(sorted, .95), Max: sorted[len(sorted)-1]}, nil
}

func quantile(sorted []float64, probability float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	position := probability * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

type rng64 struct{ state uint64 }

func newRNG(seed uint64) *rng64 {
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return &rng64{state: seed}
}

func (r *rng64) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func mixSeed(seed int64, sequence int) uint64 {
	var raw [16]byte
	binary.LittleEndian.PutUint64(raw[:8], uint64(seed))
	binary.LittleEndian.PutUint64(raw[8:], uint64(sequence)+0x9e3779b97f4a7c15)
	x := binary.LittleEndian.Uint64(raw[:8]) ^ binary.LittleEndian.Uint64(raw[8:])
	rng := newRNG(x)
	return rng.next()
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
