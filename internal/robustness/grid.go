package robustness

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

func ValidateSpace(space ParameterSpace) error {
	if strings.TrimSpace(space.SchemaVersion) == "" || len(space.Axes) == 0 {
		return ErrInvalidSchema
	}
	seen := map[string]bool{}
	for _, axis := range space.Axes {
		if axis.Name == "" || seen[axis.Name] || axis.Step <= 0 || len(axis.Values) == 0 || axis.StudyStart < 0 || axis.StudyEnd < axis.StudyStart || axis.StudyEnd >= len(axis.Values) {
			return ErrInvalidSchema
		}
		seen[axis.Name] = true
		previous := math.Inf(-1)
		for _, value := range axis.Values {
			if math.IsNaN(value) || math.IsInf(value, 0) || value <= previous || value < axis.LegalMin-1e-9 || value > axis.LegalMax+1e-9 {
				return ErrInvalidSchema
			}
			if axis.Type == ParameterInt && math.Abs(value-math.Round(value)) > 1e-9 {
				return ErrInvalidSchema
			}
			previous = value
		}
	}
	for _, coordinate := range space.ExcludedCoordinates {
		if len(coordinate) != len(space.Axes) {
			return ErrInvalidSchema
		}
		for dimension, value := range coordinate {
			axis := space.Axes[dimension]
			if value < axis.StudyStart || value > axis.StudyEnd {
				return ErrInvalidSchema
			}
		}
	}
	return nil
}

func AxisValues(center, minimum, maximum, step float64, radius int, kind ParameterType) []float64 {
	if radius < 0 || step <= 0 || maximum < minimum {
		return nil
	}
	if kind == ParameterInt {
		center, minimum, maximum, step = math.Round(center), math.Ceil(minimum), math.Floor(maximum), math.Max(1, math.Round(step))
	}
	values := make([]float64, 0, radius*2+1)
	for offset := -radius; offset <= radius; offset++ {
		value := center + float64(offset)*step
		if value < minimum-1e-9 || value > maximum+1e-9 {
			continue
		}
		if kind == ParameterInt {
			value = math.Round(value)
		} else {
			value = round(value, 8)
		}
		values = append(values, value)
	}
	sort.Float64s(values)
	return uniqueFloat(values)
}

func Enumerate(space ParameterSpace) ([]EvaluationPoint, error) {
	if err := ValidateSpace(space); err != nil {
		return nil, err
	}
	points := []EvaluationPoint{{Kind: PointActual, State: PointUnknown, Coordinates: make([]int, len(space.Axes)), Parameters: cloneMap(space.Fixed)}}
	for dimension, axis := range space.Axes {
		next := make([]EvaluationPoint, 0, len(points)*(axis.StudyEnd-axis.StudyStart+1))
		for _, base := range points {
			for index := axis.StudyStart; index <= axis.StudyEnd; index++ {
				point := EvaluationPoint{Kind: PointActual, State: PointUnknown, Coordinates: append([]int(nil), base.Coordinates...), Parameters: cloneMap(base.Parameters)}
				point.Coordinates[dimension] = index
				point.Parameters[axis.Name] = axis.Values[index]
				point.ID = CoordinateKey(point.Coordinates)
				next = append(next, point)
			}
		}
		points = next
	}
	result := points[:0]
	for _, point := range points {
		if !isExcluded(space, point.Coordinates) {
			result = append(result, point)
		}
	}
	return result, nil
}

// SampleNeighborhood uses a deterministic Halton sequence. Sparse samples are
// evidence points only; callers must not infer edges between non-adjacent samples.
func SampleNeighborhood(space ParameterSpace, count int, offset int) ([]EvaluationPoint, error) {
	if err := ValidateSpace(space); err != nil || count <= 0 || offset < 0 {
		return nil, ErrInvalidSchema
	}
	primes := []int{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47, 53, 59, 61, 67, 71}
	if len(space.Axes) > len(primes) {
		return nil, fmt.Errorf("%w: dimensions exceed sampler capacity", ErrInvalidSchema)
	}
	seen := map[string]bool{}
	result := make([]EvaluationPoint, 0, count)
	for sequence := offset + 1; len(result) < count && sequence < offset+count*100+100; sequence++ {
		coordinates := make([]int, len(space.Axes))
		params := cloneMap(space.Fixed)
		for dimension, axis := range space.Axes {
			width := axis.StudyEnd - axis.StudyStart + 1
			position := int(math.Floor(halton(sequence, primes[dimension]) * float64(width)))
			if position >= width {
				position = width - 1
			}
			coordinates[dimension] = axis.StudyStart + position
			params[axis.Name] = axis.Values[coordinates[dimension]]
		}
		key := CoordinateKey(coordinates)
		if seen[key] || isExcluded(space, coordinates) {
			continue
		}
		seen[key] = true
		result = append(result, EvaluationPoint{ID: key, Kind: PointActual, State: PointUnknown, Coordinates: coordinates, Parameters: params})
	}
	if len(result) < count {
		return nil, fmt.Errorf("%w: requested sample exceeds unique neighborhood points", ErrInvalidSchema)
	}
	return result, nil
}

func isExcluded(space ParameterSpace, coordinates []int) bool {
	for _, excluded := range space.ExcludedCoordinates {
		if len(excluded) != len(coordinates) {
			continue
		}
		equal := true
		for index := range coordinates {
			if coordinates[index] != excluded[index] {
				equal = false
				break
			}
		}
		if equal {
			return true
		}
	}
	return false
}

func CoordinateKey(coordinates []int) string {
	parts := make([]string, len(coordinates))
	for index, value := range coordinates {
		parts[index] = strconv.Itoa(value)
	}
	return strings.Join(parts, ":")
}

func halton(index, base int) float64 {
	result, fraction := 0.0, 1.0/float64(base)
	for index > 0 {
		result += fraction * float64(index%base)
		index /= base
		fraction /= float64(base)
	}
	return result
}

func uniqueFloat(values []float64) []float64 {
	result := make([]float64, 0, len(values))
	for _, value := range values {
		if len(result) == 0 || math.Abs(result[len(result)-1]-value) > 1e-9 {
			result = append(result, value)
		}
	}
	return result
}

func round(value float64, digits int) float64 {
	power := math.Pow10(digits)
	return math.Round(value*power) / power
}

func cloneMap(source map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
