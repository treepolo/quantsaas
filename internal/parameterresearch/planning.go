package parameterresearch

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	compute "quantsaas/internal/compute"
	robust "quantsaas/internal/robustness"
)

const (
	SchemaVersion           = "p10-parameter-research-v1"
	SamplerVersion          = "p10-sobol-joe-kuo-v1"
	MappingVersion          = "p10-legal-index-map-v1"
	SurrogateVersion        = "p10-random-forest-v1"
	SeriesVersion           = "p10-research-series-v1"
	CandidateVersion        = "p10-robust-candidate-v1"
	AnalysisLinkVersion     = "p10-candidate-analysis-link-v1"
	ComparisonVersion       = "p10-comparison-v1"
	AnalysisSnapshotVersion = "p10-analysis-snapshot-v1"
)

var ErrInvalidPlan = errors.New("參數研究計畫無效")

type PlannedPoint struct {
	Coordinates []int              `json:"coordinates"`
	Parameters  map[string]float64 `json:"parameters"`
	VectorHash  string             `json:"vector_hash"`
	OriginType  string             `json:"origin_type"`
	OriginKey   string             `json:"origin_key"`
	SobolIndex  *int64             `json:"sobol_index,omitempty"`
}

type GlobalPlan struct {
	SchemaVersion      string         `json:"schema_version"`
	SamplerVersion     string         `json:"sampler_version"`
	MappingVersion     string         `json:"mapping_version"`
	Mode               string         `json:"mode"`
	RequestedSobol     int            `json:"requested_sobol"`
	BaseAndAnchorCount int            `json:"base_and_anchor_count"`
	UniquePointCount   int            `json:"unique_point_count"`
	DuplicateCount     int            `json:"duplicate_count"`
	RejectedCount      int            `json:"rejected_count"`
	TotalCombinations  uint64         `json:"total_combinations"`
	SobolStartIndex    int64          `json:"sobol_start_index"`
	SobolEndIndex      int64          `json:"sobol_end_index"`
	NextSobolIndex     int64          `json:"next_sobol_index"`
	Points             []PlannedPoint `json:"points"`
}

func InitialSobolCount(dimensions int) int {
	if dimensions < 1 {
		return 0
	}
	n := 16 * dimensions
	if n < 64 {
		n = 64
	}
	result := 1
	for result < n {
		result <<= 1
	}
	return result
}

func CombinationCount(space robust.ParameterSpace) (uint64, error) {
	if err := robust.ValidateSpace(space); err != nil {
		return 0, err
	}
	total := uint64(1)
	for _, axis := range space.Axes {
		count := uint64(axis.StudyEnd - axis.StudyStart + 1)
		if count == 0 || total > math.MaxUint64/count {
			return math.MaxUint64, nil
		}
		total *= count
	}
	if len(space.ExcludedCoordinates) > 0 {
		validExcluded := uint64(0)
		for _, coordinate := range space.ExcludedCoordinates {
			if coordinateValid(space, coordinate) {
				validExcluded++
			}
		}
		if validExcluded < total {
			total -= validExcluded
		}
	}
	return total, nil
}

func PlanGlobal(space robust.ParameterSpace, base []int, requestedSobol int, startIndex int64, existing map[string]bool, includeAnchors bool) (GlobalPlan, error) {
	return PlanGlobalValidated(space, base, requestedSobol, startIndex, existing, includeAnchors, nil)
}

// PlanGlobalValidated keeps sampling until it has the requested number of
// unique points that satisfy both the generic parameter space and the
// strategy-specific structural rules.
func PlanGlobalValidated(space robust.ParameterSpace, base []int, requestedSobol int, startIndex int64, existing map[string]bool, includeAnchors bool, validator func(map[string]float64) error) (GlobalPlan, error) {
	if err := robust.ValidateSpace(space); err != nil || len(base) != len(space.Axes) || requestedSobol < 0 || startIndex < 0 {
		return GlobalPlan{}, ErrInvalidPlan
	}
	for i, value := range base {
		if value < space.Axes[i].StudyStart || value > space.Axes[i].StudyEnd {
			return GlobalPlan{}, ErrInvalidPlan
		}
	}
	total, err := CombinationCount(space)
	if err != nil {
		return GlobalPlan{}, err
	}
	plan := GlobalPlan{SchemaVersion: SchemaVersion, SamplerVersion: SamplerVersion, MappingVersion: MappingVersion, Mode: "sobol", RequestedSobol: requestedSobol, TotalCombinations: total, SobolStartIndex: startIndex, SobolEndIndex: startIndex - 1, NextSobolIndex: startIndex}
	seen := make(map[string]bool, len(existing)+requestedSobol+1+2*len(space.Axes))
	for key, value := range existing {
		seen[key] = value
	}
	appendCoordinate := func(coordinate []int, origin, originKey string, sobolIndex *int64) {
		point, pointErr := plannedPoint(space, coordinate, origin, originKey, sobolIndex)
		if pointErr != nil {
			plan.RejectedCount++
			return
		}
		if validator != nil && validator(point.Parameters) != nil {
			plan.RejectedCount++
			return
		}
		if seen[point.VectorHash] {
			plan.DuplicateCount++
			return
		}
		seen[point.VectorHash] = true
		plan.Points = append(plan.Points, point)
	}
	if includeAnchors {
		before := len(plan.Points)
		appendCoordinate(append([]int(nil), base...), "baseline", "baseline", nil)
		for dimension, axis := range space.Axes {
			minimum := append([]int(nil), base...)
			minimum[dimension] = axis.StudyStart
			appendCoordinate(minimum, "axis_anchor", axis.Name+":minimum", nil)
			maximum := append([]int(nil), base...)
			maximum[dimension] = axis.StudyEnd
			appendCoordinate(maximum, "axis_anchor", axis.Name+":maximum", nil)
		}
		plan.BaseAndAnchorCount = len(plan.Points) - before
	}
	if total <= uint64(requestedSobol) && startIndex == 0 {
		plan.Mode = "full_enumeration"
		coordinates, enumerateErr := enumerateCoordinates(space, nil, 0)
		if enumerateErr != nil {
			return GlobalPlan{}, enumerateErr
		}
		for _, coordinate := range coordinates {
			appendCoordinate(coordinate, "full_enumeration", coordinateKey(coordinate), nil)
		}
		plan.UniquePointCount = len(plan.Points)
		return plan, nil
	}
	if requestedSobol == 0 {
		plan.UniquePointCount = len(plan.Points)
		return plan, nil
	}
	target := requestedSobol
	addedSobol := 0
	index := startIndex
	maxAttempts := int64(requestedSobol*1024 + 65536)
	for addedSobol < target && index-startIndex < maxAttempts && uint64(len(seen)) < total {
		sequence, sequenceErr := SobolPoint(uint64(index), len(space.Axes))
		if sequenceErr != nil {
			return GlobalPlan{}, sequenceErr
		}
		coordinate := make([]int, len(space.Axes))
		for dimension, axis := range space.Axes {
			count := axis.StudyEnd - axis.StudyStart + 1
			mapped := int(math.Floor(sequence[dimension] * float64(count)))
			if mapped >= count {
				mapped = count - 1
			}
			coordinate[dimension] = axis.StudyStart + mapped
		}
		before := len(plan.Points)
		copyIndex := index
		appendCoordinate(coordinate, "sobol", fmt.Sprintf("sobol:%d", index), &copyIndex)
		if len(plan.Points) > before {
			addedSobol++
		}
		plan.SobolEndIndex = index
		index++
	}
	plan.NextSobolIndex = index
	plan.UniquePointCount = len(plan.Points)
	if addedSobol < target && uint64(len(seen)) < total {
		return GlobalPlan{}, fmt.Errorf("%w: Sobol 量化後無法產生足夠唯一合法點", ErrInvalidPlan)
	}
	return plan, nil
}

func PlanLocalRefinement(space robust.ParameterSpace, center []int, radius int, existing map[string]bool) ([]PlannedPoint, error) {
	return PlanLocalRefinementLimited(space, center, radius, existing, 0)
}

// PlanLocalRefinementLimited rejects an oversized Cartesian neighborhood before
// allocating its coordinate list. A zero limit keeps the original core API
// behavior for small, trusted callers.
func PlanLocalRefinementLimited(space robust.ParameterSpace, center []int, radius int, existing map[string]bool, maximumPoints int) ([]PlannedPoint, error) {
	if err := robust.ValidateSpace(space); err != nil || len(center) != len(space.Axes) || radius < 1 {
		return nil, ErrInvalidPlan
	}
	ranges := make([][]int, len(space.Axes))
	candidateCount := 1
	for i, axis := range space.Axes {
		start, end := center[i]-radius, center[i]+radius
		if start < axis.StudyStart {
			start = axis.StudyStart
		}
		if end > axis.StudyEnd {
			end = axis.StudyEnd
		}
		for value := start; value <= end; value++ {
			ranges[i] = append(ranges[i], value)
		}
		if maximumPoints > 0 && (len(ranges[i]) == 0 || candidateCount > maximumPoints/len(ranges[i])) {
			return nil, fmt.Errorf("%w: 局部細化預計超過 %d 個參數組合，請減少細化格數或改用追加全域探索", ErrInvalidPlan, maximumPoints)
		}
		candidateCount *= len(ranges[i])
	}
	coordinates, err := enumerateCoordinates(space, ranges, 0)
	if err != nil {
		return nil, err
	}
	points := make([]PlannedPoint, 0, len(coordinates))
	for _, coordinate := range coordinates {
		point, err := plannedPoint(space, coordinate, "local_refinement", "center:"+coordinateKey(center)+":radius:"+strconv.Itoa(radius), nil)
		if err != nil || existing[point.VectorHash] {
			continue
		}
		points = append(points, point)
	}
	return points, nil
}

func plannedPoint(space robust.ParameterSpace, coordinate []int, origin, originKey string, sobolIndex *int64) (PlannedPoint, error) {
	if !coordinateValid(space, coordinate) {
		return PlannedPoint{}, ErrInvalidPlan
	}
	parameters := make(map[string]float64, len(space.Fixed)+len(space.Axes))
	for name, value := range space.Fixed {
		parameters[name] = value
	}
	for dimension, axis := range space.Axes {
		parameters[axis.Name] = axis.Values[coordinate[dimension]]
	}
	raw, err := compute.CanonicalJSON(coordinate)
	if err != nil {
		return PlannedPoint{}, err
	}
	return PlannedPoint{Coordinates: append([]int(nil), coordinate...), Parameters: parameters, VectorHash: compute.HashBytes(raw), OriginType: origin, OriginKey: originKey, SobolIndex: sobolIndex}, nil
}

func enumerateCoordinates(space robust.ParameterSpace, ranges [][]int, dimension int) ([][]int, error) {
	if dimension == len(space.Axes) {
		return [][]int{{}}, nil
	}
	values := []int{}
	if len(ranges) > dimension && len(ranges[dimension]) > 0 {
		values = ranges[dimension]
	} else {
		for value := space.Axes[dimension].StudyStart; value <= space.Axes[dimension].StudyEnd; value++ {
			values = append(values, value)
		}
	}
	tail, err := enumerateCoordinates(space, ranges, dimension+1)
	if err != nil {
		return nil, err
	}
	result := make([][]int, 0, len(values)*len(tail))
	for _, value := range values {
		for _, suffix := range tail {
			coordinate := append([]int{value}, suffix...)
			if dimension == 0 && !coordinateValid(space, coordinate) {
				continue
			}
			result = append(result, coordinate)
		}
	}
	return result, nil
}

func coordinateValid(space robust.ParameterSpace, coordinate []int) bool {
	if len(coordinate) != len(space.Axes) {
		return false
	}
	for i, value := range coordinate {
		if value < space.Axes[i].StudyStart || value > space.Axes[i].StudyEnd || value < 0 || value >= len(space.Axes[i].Values) {
			return false
		}
	}
	for _, excluded := range space.ExcludedCoordinates {
		if coordinateKey(excluded) == coordinateKey(coordinate) {
			return false
		}
	}
	return true
}

func coordinateKey(coordinate []int) string {
	parts := make([]string, len(coordinate))
	for i, value := range coordinate {
		parts[i] = strconv.Itoa(value)
	}
	return strings.Join(parts, ":")
}

func StablePointSetHash(points []PlannedPoint) string {
	hashes := make([]string, 0, len(points))
	for _, point := range points {
		hashes = append(hashes, point.VectorHash)
	}
	sort.Strings(hashes)
	raw, _ := compute.CanonicalJSON(hashes)
	return compute.HashBytes(raw)
}
