package klineinverse

import (
	"fmt"
	"math"
	"sort"
)

const (
	OperationGlobal      = "global"
	OperationLocal       = "local"
	OperationBlock       = "block"
	OperationCrossover   = "crossover"
	OperationDirectional = "directional"
)

var OperationOrder = []string{OperationGlobal, OperationLocal, OperationBlock, OperationCrossover, OperationDirectional}

type Center [20]float64

type Candidate struct {
	ID        string
	Hash      string
	Path      Path
	QRelative float64
	QAbsolute float64
}

type Variation struct {
	Operation       string   `json:"operation"`
	ActualOperation string   `json:"actual_operation"`
	ParentIDs       []string `json:"parent_ids,omitempty"`
	StartIndex      int      `json:"start_index"`
	Length          int      `json:"length"`
	Channels        []string `json:"channels,omitempty"`
	Amplitude       float64  `json:"amplitude"`
}

func AutoKP(n int) (int, int, error) {
	if n < 1 {
		return 0, 0, fmt.Errorf("搜尋預算必須大於 0")
	}
	k := int(math.Round(math.Sqrt(float64(n))))
	if k < 1 {
		k = 1
	}
	p := int(math.Round(math.Sqrt(float64(n) / float64(k))))
	if p < 4 {
		p = 4
	}
	return k, p, nil
}

func OperationQuotas(n int) map[string]int {
	result := make(map[string]int, len(OperationOrder))
	if n <= 0 {
		return result
	}
	base, remainder := n/len(OperationOrder), n%len(OperationOrder)
	for index, operation := range OperationOrder {
		result[operation] = base
		if index < remainder {
			result[operation]++
		}
	}
	return result
}

func GenerateGlobal(seed int64, sequence int, dates []int64, warmupLength int, bounds Bounds) (Path, error) {
	if warmupLength < 1 || warmupLength >= len(dates) {
		return Path{}, fmt.Errorf("%w：W/H 長度不合法", ErrInvalidPath)
	}
	coordinates := make([]Coordinate, len(dates))
	for index := range coordinates {
		normalized := Coordinate{G: unit(seed, sequence, index*4), B: unit(seed, sequence, index*4+1), U: unit(seed, sequence, index*4+2), D: unit(seed, sequence, index*4+3)}
		value, err := Denormalize(normalized, bounds)
		if err != nil {
			return Path{}, err
		}
		coordinates[index] = value
	}
	return Path{WarmupLength: warmupLength, EvaluationLength: len(dates) - warmupLength, Dates: append([]int64(nil), dates...), Coordinates: coordinates}, nil
}

func Vary(seed int64, sequence int, requested string, parents []Candidate, bounds Bounds, amplitude float64) (Path, Variation, error) {
	if len(parents) == 0 {
		return Path{}, Variation{}, fmt.Errorf("%w：變異缺少父代", ErrInvalidPath)
	}
	base := parents[0].Path
	if err := validatePath(base); err != nil {
		return Path{}, Variation{}, err
	}
	actual := requested
	needsTwo := requested == OperationCrossover || requested == OperationDirectional
	if needsTwo && len(parents) < 2 {
		actual = OperationGlobal
	}
	if actual == OperationGlobal {
		path, err := GenerateGlobal(seed, sequence, base.Dates, base.WarmupLength, bounds)
		return path, Variation{Operation: requested, ActualOperation: OperationGlobal, Amplitude: amplitude}, err
	}
	normalized := make([]Coordinate, len(base.Coordinates))
	for index, coordinate := range base.Coordinates {
		var err error
		normalized[index], err = Normalize(coordinate, bounds)
		if err != nil {
			return Path{}, Variation{}, err
		}
	}
	variation := Variation{Operation: requested, ActualOperation: actual, ParentIDs: []string{parents[0].ID}, Amplitude: amplitude}
	switch actual {
	case OperationLocal:
		variation.Length = 1 + int(unit(seed, sequence, 0)*float64(minInt(3, len(normalized))))
		variation.StartIndex = chooseStart(seed, sequence, 1, len(normalized), variation.Length)
		channel := int(unit(seed, sequence, 2) * 4)
		variation.Channels = []string{[]string{"g", "b", "u", "d"}[channel]}
		for index := variation.StartIndex; index < variation.StartIndex+variation.Length; index++ {
			applyDelta(&normalized[index], channel, signed(seed, sequence, 10+index)*amplitude)
		}
	case OperationBlock:
		scales := blockScales(len(normalized))
		variation.Length = scales[int(unit(seed, sequence, 0)*float64(len(scales)))]
		variation.StartIndex = chooseStart(seed, sequence, 1, len(normalized), variation.Length)
		variation.Channels = []string{"g", "b", "u", "d"}
		for index := variation.StartIndex; index < variation.StartIndex+variation.Length; index++ {
			for channel := 0; channel < 4; channel++ {
				applyDelta(&normalized[index], channel, signed(seed, sequence, 10+index*4+channel)*amplitude)
			}
		}
	case OperationCrossover:
		other := parents[1].Path
		if other.WarmupLength != base.WarmupLength || len(other.Coordinates) != len(base.Coordinates) {
			return Path{}, Variation{}, fmt.Errorf("%w：交叉父代結構不一致", ErrInvalidPath)
		}
		variation.ParentIDs = append(variation.ParentIDs, parents[1].ID)
		scales := blockScales(len(normalized))
		variation.Length = scales[int(unit(seed, sequence, 0)*float64(len(scales)))]
		variation.StartIndex = chooseStart(seed, sequence, 1, len(normalized), variation.Length)
		for index := variation.StartIndex; index < variation.StartIndex+variation.Length; index++ {
			value, err := Normalize(other.Coordinates[index], bounds)
			if err != nil {
				return Path{}, Variation{}, err
			}
			normalized[index] = value
		}
	case OperationDirectional:
		other := parents[1].Path
		if other.WarmupLength != base.WarmupLength || len(other.Coordinates) != len(base.Coordinates) {
			return Path{}, Variation{}, fmt.Errorf("%w：方向父代結構不一致", ErrInvalidPath)
		}
		variation.ParentIDs = append(variation.ParentIDs, parents[1].ID)
		variation.StartIndex, variation.Length = 0, len(normalized)
		for index := range normalized {
			otherValue, err := Normalize(other.Coordinates[index], bounds)
			if err != nil {
				return Path{}, Variation{}, err
			}
			values, others := coordinateValues(normalized[index]), coordinateValues(otherValue)
			for channel := range values {
				values[channel] += amplitude * (values[channel] - others[channel])
			}
			normalized[index] = coordinateFromValues(values)
		}
	default:
		return Path{}, Variation{}, fmt.Errorf("%w：未知搜尋操作 %s", ErrInvalidPath, requested)
	}
	coordinates := make([]Coordinate, len(normalized))
	for index, value := range normalized {
		values := coordinateValues(value)
		for channel := range values {
			reflected, err := Reflect(values[channel])
			if err != nil {
				return Path{}, Variation{}, err
			}
			values[channel] = reflected
		}
		var err error
		coordinates[index], err = Denormalize(coordinateFromValues(values), bounds)
		if err != nil {
			return Path{}, Variation{}, err
		}
	}
	return Path{WarmupLength: base.WarmupLength, EvaluationLength: base.EvaluationLength, Dates: append([]int64(nil), base.Dates...), Coordinates: coordinates}, variation, nil
}

// ProbeVary creates a finite, explicitly scoped direct child of an anchor.
// Scope is W, H, or both; min/max length are part of the immutable probe manifest.
func ProbeVary(seed int64, sequence int, requested string, parents []Candidate, bounds Bounds, amplitude float64, scope string, minLength, maxLength int) (Path, Variation, error) {
	if len(parents) == 0 || amplitude <= 0 || amplitude > 1 {
		return Path{}, Variation{}, fmt.Errorf("%w：探測缺少 anchor 或幅度不合法", ErrInvalidPath)
	}
	base := parents[0].Path
	if err := validatePath(base); err != nil {
		return Path{}, Variation{}, err
	}
	scopeStart, scopeLength := 0, len(base.Coordinates)
	switch scope {
	case "W":
		scopeLength = base.WarmupLength
	case "H":
		scopeStart, scopeLength = base.WarmupLength, base.EvaluationLength
	case "both":
	default:
		return Path{}, Variation{}, fmt.Errorf("%w：探測範圍不合法", ErrInvalidPath)
	}
	if minLength < 1 || maxLength < minLength || minLength > scopeLength {
		return Path{}, Variation{}, fmt.Errorf("%w：探測區塊長度不合法", ErrInvalidPath)
	}
	if maxLength > scopeLength {
		maxLength = scopeLength
	}
	length := minLength
	if maxLength > minLength {
		length += int(unit(seed, sequence, 600000) * float64(maxLength-minLength+1))
	}
	start := scopeStart + chooseStart(seed, sequence, 600001, scopeLength, length)
	normalized := make([]Coordinate, len(base.Coordinates))
	for index, coordinate := range base.Coordinates {
		value, err := Normalize(coordinate, bounds)
		if err != nil {
			return Path{}, Variation{}, err
		}
		normalized[index] = value
	}
	variation := Variation{Operation: requested, ActualOperation: requested, ParentIDs: []string{parents[0].ID}, StartIndex: start, Length: length, Amplitude: amplitude}
	actual := requested
	if (requested == OperationCrossover || requested == OperationDirectional) && len(parents) < 2 {
		actual = OperationBlock
		variation.ActualOperation = actual
	}
	switch actual {
	case OperationLocal:
		channel := int(unit(seed, sequence, 600002) * 4)
		variation.Channels = []string{[]string{"g", "b", "u", "d"}[channel]}
		for index := start; index < start+length; index++ {
			applyDelta(&normalized[index], channel, signed(seed, sequence, 600010+index)*amplitude)
		}
	case OperationBlock:
		variation.Channels = []string{"g", "b", "u", "d"}
		for index := start; index < start+length; index++ {
			for channel := 0; channel < 4; channel++ {
				applyDelta(&normalized[index], channel, signed(seed, sequence, 600010+index*4+channel)*amplitude)
			}
		}
	case OperationCrossover, OperationDirectional:
		other := parents[1].Path
		if other.WarmupLength != base.WarmupLength || other.EvaluationLength != base.EvaluationLength {
			return Path{}, Variation{}, fmt.Errorf("%w：探測父代結構不一致", ErrInvalidPath)
		}
		variation.ParentIDs = append(variation.ParentIDs, parents[1].ID)
		for index := start; index < start+length; index++ {
			otherValue, err := Normalize(other.Coordinates[index], bounds)
			if err != nil {
				return Path{}, Variation{}, err
			}
			if actual == OperationCrossover {
				normalized[index] = otherValue
				continue
			}
			values, others := coordinateValues(normalized[index]), coordinateValues(otherValue)
			for channel := range values {
				values[channel] += amplitude * (values[channel] - others[channel])
			}
			normalized[index] = coordinateFromValues(values)
		}
	default:
		return Path{}, Variation{}, fmt.Errorf("%w：探測操作不合法", ErrInvalidPath)
	}
	coordinates := append([]Coordinate(nil), base.Coordinates...)
	for index := start; index < start+length; index++ {
		coordinate := normalized[index]
		values := coordinateValues(coordinate)
		for channel := range values {
			reflected, err := Reflect(values[channel])
			if err != nil {
				return Path{}, Variation{}, err
			}
			values[channel] = reflected
		}
		value, err := Denormalize(coordinateFromValues(values), bounds)
		if err != nil {
			return Path{}, Variation{}, err
		}
		coordinates[index] = value
	}
	return Path{WarmupLength: base.WarmupLength, EvaluationLength: base.EvaluationLength, Dates: append([]int64(nil), base.Dates...), Coordinates: coordinates}, variation, nil
}

func CalibrateCVT(features [][20]float64, k int, seed int64) ([]Center, error) {
	if len(features) == 0 || k < 1 || k > len(features) {
		return nil, fmt.Errorf("%w：CVT K 或校準特徵數不合法", ErrInvalidPath)
	}
	centers := make([]Center, 0, k)
	first := int(unit(seed, 0, 0) * float64(len(features)))
	centers = append(centers, Center(features[first]))
	for len(centers) < k {
		bestIndex, bestDistance := 0, -1.0
		for index, feature := range features {
			distance := nearestCenterDistance(feature, centers)
			if distance > bestDistance || (distance == bestDistance && index < bestIndex) {
				bestIndex, bestDistance = index, distance
			}
		}
		centers = append(centers, Center(features[bestIndex]))
	}
	for iteration := 0; iteration < 100; iteration++ {
		sums := make([]Center, k)
		counts := make([]int, k)
		for _, feature := range features {
			cell := AssignCell(feature, centers)
			counts[cell]++
			for dimension, value := range feature {
				sums[cell][dimension] += value
			}
		}
		changed := false
		for cell := range centers {
			if counts[cell] == 0 {
				continue
			}
			for dimension := range centers[cell] {
				next := sums[cell][dimension] / float64(counts[cell])
				if math.Abs(next-centers[cell][dimension]) > 1e-15 {
					changed = true
				}
				centers[cell][dimension] = next
			}
		}
		if !changed {
			break
		}
	}
	return centers, nil
}

func AssignCell(feature [20]float64, centers []Center) int {
	best, bestDistance := 0, math.Inf(1)
	for index, center := range centers {
		distance := featureDistance(feature, [20]float64(center))
		if distance < bestDistance {
			best, bestDistance = index, distance
		}
	}
	return best
}

func SelectPareto(candidates []Candidate, capacity int, bounds Bounds) ([]Candidate, error) {
	if capacity < 1 {
		return nil, fmt.Errorf("Pareto 容量必須大於 0")
	}
	nondominated := make([]Candidate, 0, len(candidates))
	for index, candidate := range candidates {
		dominated := false
		for otherIndex, other := range candidates {
			if index != otherIndex && dominates(other, candidate) {
				dominated = true
				break
			}
		}
		if !dominated {
			nondominated = append(nondominated, candidate)
		}
	}
	sort.Slice(nondominated, func(i, j int) bool { return nondominated[i].Hash < nondominated[j].Hash })
	if len(nondominated) <= capacity {
		return nondominated, nil
	}
	selected := []Candidate{}
	addUnique := func(candidate Candidate) {
		for _, existing := range selected {
			if existing.Hash == candidate.Hash {
				return
			}
		}
		selected = append(selected, candidate)
	}
	bestRelative, bestAbsolute := nondominated[0], nondominated[0]
	for _, candidate := range nondominated[1:] {
		if candidate.QRelative > bestRelative.QRelative || (candidate.QRelative == bestRelative.QRelative && candidate.Hash < bestRelative.Hash) {
			bestRelative = candidate
		}
		if candidate.QAbsolute > bestAbsolute.QAbsolute || (candidate.QAbsolute == bestAbsolute.QAbsolute && candidate.Hash < bestAbsolute.Hash) {
			bestAbsolute = candidate
		}
	}
	addUnique(bestRelative)
	if len(selected) < capacity {
		addUnique(bestAbsolute)
	}
	for len(selected) < capacity {
		bestIndex, bestCrowding := -1, -1.0
		for index, candidate := range nondominated {
			already := false
			for _, existing := range selected {
				if existing.Hash == candidate.Hash {
					already = true
					break
				}
			}
			if already {
				continue
			}
			nearest := math.Inf(1)
			for _, existing := range selected {
				distance, err := PathDistance(candidate.Path, existing.Path, bounds)
				if err != nil {
					return nil, err
				}
				nearest = math.Min(nearest, distance.Total)
			}
			if nearest > bestCrowding || (nearest == bestCrowding && (bestIndex < 0 || candidate.Hash < nondominated[bestIndex].Hash)) {
				bestIndex, bestCrowding = index, nearest
			}
		}
		if bestIndex < 0 {
			break
		}
		selected = append(selected, nondominated[bestIndex])
	}
	return selected, nil
}

func dominates(a, b Candidate) bool {
	return a.QRelative >= b.QRelative && a.QAbsolute >= b.QAbsolute && (a.QRelative > b.QRelative || a.QAbsolute > b.QAbsolute)
}

func featureDistance(a, b [20]float64) float64 {
	var warmup, evaluation float64
	for index := 0; index < 10; index++ {
		difference := a[index] - b[index]
		warmup += difference * difference
	}
	for index := 10; index < 20; index++ {
		difference := a[index] - b[index]
		evaluation += difference * difference
	}
	return .5*warmup/10 + .5*evaluation/10
}

func nearestCenterDistance(feature [20]float64, centers []Center) float64 {
	best := math.Inf(1)
	for _, center := range centers {
		best = math.Min(best, featureDistance(feature, [20]float64(center)))
	}
	return best
}

func unit(seed int64, sequence, dimension int) float64 {
	value := uint64(seed) ^ uint64(sequence+1)*0x9e3779b97f4a7c15 ^ uint64(dimension+1)*0xbf58476d1ce4e5b9
	value += 0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	value ^= value >> 31
	return float64(value>>11) / float64(uint64(1)<<53)
}

// DeterministicUnit exposes the versioned counter RNG for orchestration code.
// It has no mutable state, so resume and retry can reproduce an exact sequence.
func DeterministicUnit(seed int64, sequence, dimension int) float64 {
	return unit(seed, sequence, dimension)
}

func signed(seed int64, sequence, dimension int) float64 {
	return unit(seed, sequence, dimension)*2 - 1
}

func chooseStart(seed int64, sequence, dimension, total, length int) int {
	if length >= total {
		return 0
	}
	return int(unit(seed, sequence, dimension) * float64(total-length+1))
}

func applyDelta(coordinate *Coordinate, channel int, delta float64) {
	values := coordinateValues(*coordinate)
	values[channel] += delta
	*coordinate = coordinateFromValues(values)
}

func coordinateValues(value Coordinate) [4]float64 {
	return [4]float64{value.G, value.B, value.U, value.D}
}
func coordinateFromValues(value [4]float64) Coordinate {
	return Coordinate{G: value[0], B: value[1], U: value[2], D: value[3]}
}

func blockScales(length int) []int {
	result := []int{}
	for scale := 1; scale < length; scale *= 2 {
		result = append(result, scale)
	}
	if len(result) == 0 || result[len(result)-1] != length {
		result = append(result, length)
	}
	return result
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
