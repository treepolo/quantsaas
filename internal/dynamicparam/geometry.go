package dynamicparam

import (
	"fmt"
	"math"
	"sort"

	"quantsaas/internal/quant"
)

// GeometrySchemaVersion identifies the causal, raw-scale geometry contract.
// It is intentionally separate from the existing P09 feature schema.
const GeometrySchemaVersion = "geometry-v1"

const RouteTrendGeometry = "trend_geometry"

type GeometryPoint struct {
	SchemaVersion     string  `json:"schema_version"`
	Index             int     `json:"index"`
	TimeMs            int64   `json:"time_ms"`
	Lookback          int     `json:"lookback"`
	CoverageArea      float64 `json:"coverage_area"`
	TrendSlope        float64 `json:"trend_slope"`
	Available         bool    `json:"available"`
	UnavailableReason string  `json:"unavailable_reason,omitempty"`
}

type GeometryTarget struct {
	SchemaVersion     string  `json:"schema_version"`
	Index             int     `json:"index"`
	TimeMs            int64   `json:"time_ms"`
	Horizon           int     `json:"horizon"`
	CoverageArea      float64 `json:"coverage_area"`
	TrendSlope        float64 `json:"trend_slope"`
	Available         bool    `json:"available"`
	UnavailableReason string  `json:"unavailable_reason,omitempty"`
}

type GeometrySample struct {
	Feature GeometryPoint  `json:"feature"`
	Target  GeometryTarget `json:"target"`
}

// ConvexHullArea returns the area of the outer, non-crossing OHLC envelope.
// Points with the same x coordinate are reduced to the lowest and highest y;
// the monotonic chain removes collinear interior vertices.
func ConvexHullArea(bars []quant.Bar) (float64, error) {
	if err := validateGeometryBars(bars); err != nil {
		return 0, err
	}
	if len(bars) < 2 {
		return 0, fmt.Errorf("geometry window requires at least two bars")
	}
	points := make([]geometryXY, 0, len(bars)*2)
	for index, bar := range bars {
		points = append(points, geometryXY{X: float64(index), Y: bar.Low})
		points = append(points, geometryXY{X: float64(index), Y: bar.High})
	}
	hull := geometryHull(points)
	if len(hull) < 3 {
		return 0, nil
	}
	area := 0.0
	for index, point := range hull {
		next := hull[(index+1)%len(hull)]
		area += point.X*next.Y - next.X*point.Y
	}
	return math.Abs(area) / 2, nil
}

// TrendSlope is the raw-price least-squares slope of Close[i] on i.
func TrendSlope(bars []quant.Bar) (float64, error) {
	if err := validateGeometryBars(bars); err != nil {
		return 0, err
	}
	if len(bars) < 2 {
		return 0, fmt.Errorf("trend window requires at least two bars")
	}
	n := float64(len(bars))
	meanX := (n - 1) / 2
	meanY := 0.0
	for _, bar := range bars {
		meanY += bar.Close
	}
	meanY /= n
	denominator, numerator := 0.0, 0.0
	for index, bar := range bars {
		dx := float64(index) - meanX
		numerator += dx * (bar.Close - meanY)
		denominator += dx * dx
	}
	return numerator / denominator, nil
}

// BuildGeometryFeatures computes each point using only bars ending at index.
func BuildGeometryFeatures(bars []quant.Bar, lookback int) ([]GeometryPoint, error) {
	return buildGeometryFeatures(bars, lookback, nil)
}

// BuildGeometryFeaturesWithProgress has the same output contract as
// BuildGeometryFeatures and reports completed causal geometry work in batches.
func BuildGeometryFeaturesWithProgress(bars []quant.Bar, lookback int, progress func(int64)) ([]GeometryPoint, error) {
	return buildGeometryFeatures(bars, lookback, progress)
}

func buildGeometryFeatures(bars []quant.Bar, lookback int, progress func(int64)) ([]GeometryPoint, error) {
	if lookback < 2 {
		return nil, fmt.Errorf("geometry lookback must be at least two")
	}
	if err := validateGeometryBars(bars); err != nil {
		return nil, err
	}
	result := make([]GeometryPoint, len(bars))
	workspace := geometryWorkspace{
		points: make([]geometryXY, 0, lookback*2), unique: make([]geometryXY, 0, lookback*2),
		lower: make([]geometryXY, 0, lookback*2), upper: make([]geometryXY, 0, lookback*2), hull: make([]geometryXY, 0, lookback*2),
	}
	for index, bar := range bars {
		point := GeometryPoint{SchemaVersion: GeometrySchemaVersion, Index: index, TimeMs: bar.OpenTime, Lookback: lookback}
		if index+1 < lookback {
			point.UnavailableReason = "insufficient_lookback"
			result[index] = point
			continue
		}
		window := bars[index-lookback+1 : index+1]
		point.CoverageArea = convexHullAreaOrdered(window, &workspace)
		point.TrendSlope = trendSlopeValidated(window)
		point.Available = true
		result[index] = point
		if progress != nil && (index+1)%64 == 0 {
			progress(int64(64 * lookback))
		}
	}
	if progress != nil {
		remaining := len(bars) % 64
		if remaining > 0 {
			progress(int64(remaining * lookback))
		}
	}
	return result, nil
}

type geometryWorkspace struct {
	points, unique, lower, upper, hull []geometryXY
}

// convexHullAreaOrdered is equivalent to ConvexHullArea for validated OHLC
// windows. Its input order is already the exact X/Y order that geometryHull
// would create, so it reuses workspace buffers instead of allocating several
// large slices for every causal bar.
func convexHullAreaOrdered(bars []quant.Bar, workspace *geometryWorkspace) float64 {
	workspace.points = workspace.points[:0]
	for index, bar := range bars {
		workspace.points = append(workspace.points, geometryXY{X: float64(index), Y: bar.Low}, geometryXY{X: float64(index), Y: bar.High})
	}
	workspace.unique = workspace.unique[:0]
	for _, point := range workspace.points {
		if len(workspace.unique) == 0 || point != workspace.unique[len(workspace.unique)-1] {
			workspace.unique = append(workspace.unique, point)
		}
	}
	workspace.lower = workspace.lower[:0]
	for _, point := range workspace.unique {
		for len(workspace.lower) >= 2 && cross(workspace.lower[len(workspace.lower)-2], workspace.lower[len(workspace.lower)-1], point) <= 0 {
			workspace.lower = workspace.lower[:len(workspace.lower)-1]
		}
		workspace.lower = append(workspace.lower, point)
	}
	workspace.upper = workspace.upper[:0]
	for index := len(workspace.unique) - 1; index >= 0; index-- {
		point := workspace.unique[index]
		for len(workspace.upper) >= 2 && cross(workspace.upper[len(workspace.upper)-2], workspace.upper[len(workspace.upper)-1], point) <= 0 {
			workspace.upper = workspace.upper[:len(workspace.upper)-1]
		}
		workspace.upper = append(workspace.upper, point)
	}
	workspace.hull = append(workspace.hull[:0], workspace.lower[:len(workspace.lower)-1]...)
	workspace.hull = append(workspace.hull, workspace.upper[:len(workspace.upper)-1]...)
	if len(workspace.hull) < 3 {
		return 0
	}
	area := 0.0
	for index, point := range workspace.hull {
		next := workspace.hull[(index+1)%len(workspace.hull)]
		area += point.X*next.Y - next.X*point.Y
	}
	return math.Abs(area) / 2
}

func trendSlopeValidated(bars []quant.Bar) float64 {
	n := float64(len(bars))
	meanX := (n - 1) / 2
	meanY := 0.0
	for _, bar := range bars {
		meanY += bar.Close
	}
	meanY /= n
	denominator, numerator := 0.0, 0.0
	for index, bar := range bars {
		dx := float64(index) - meanX
		numerator += dx * (bar.Close - meanY)
		denominator += dx * dx
	}
	return numerator / denominator
}

// BuildGeometryTargets creates targets from t+1 through t+horizon.
func BuildGeometryTargets(bars []quant.Bar, horizon int) ([]GeometryTarget, error) {
	if horizon != HorizonOneDay && horizon != HorizonTwentyDay {
		return nil, fmt.Errorf("unsupported geometry horizon %d", horizon)
	}
	if err := validateGeometryBars(bars); err != nil {
		return nil, err
	}
	if len(bars) <= horizon {
		return nil, fmt.Errorf("geometry horizon requires future bars")
	}
	result := make([]GeometryTarget, len(bars)-horizon)
	for index := range result {
		// The decision bar is the anchor that connects the future window.
		// This makes the one-day target a two-bar envelope: t and t+1.
		window := bars[index : index+horizon+1]
		area, areaErr := ConvexHullArea(window)
		slope, slopeErr := TrendSlope(window)
		target := GeometryTarget{SchemaVersion: GeometrySchemaVersion, Index: index, TimeMs: bars[index].OpenTime, Horizon: horizon}
		if areaErr != nil || slopeErr != nil {
			target.UnavailableReason = "degenerate_future_window"
		} else {
			target.CoverageArea, target.TrendSlope, target.Available = area, slope, true
		}
		result[index] = target
	}
	return result, nil
}

func BuildGeometrySamples(bars []quant.Bar, lookback, horizon int) ([]GeometrySample, error) {
	features, err := BuildGeometryFeatures(bars, lookback)
	if err != nil {
		return nil, err
	}
	targets, err := BuildGeometryTargets(bars, horizon)
	if err != nil {
		return nil, err
	}
	result := make([]GeometrySample, 0, len(targets))
	for _, target := range targets {
		if target.Index >= len(features) || !features[target.Index].Available || !target.Available {
			continue
		}
		result = append(result, GeometrySample{Feature: features[target.Index], Target: target})
	}
	return result, nil
}

type geometryXY struct{ X, Y float64 }

func geometryHull(points []geometryXY) []geometryXY {
	ordered := append([]geometryXY(nil), points...)
	// x is already ordered, but low/high pairs must be ordered by y.
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].X == ordered[right].X {
			return ordered[left].Y < ordered[right].Y
		}
		return ordered[left].X < ordered[right].X
	})
	unique := ordered[:0]
	for _, point := range ordered {
		if len(unique) == 0 || point != unique[len(unique)-1] {
			unique = append(unique, point)
		}
	}
	if len(unique) <= 1 {
		return unique
	}
	chain := func(input []geometryXY) []geometryXY {
		result := make([]geometryXY, 0, len(input))
		for _, point := range input {
			for len(result) >= 2 && cross(result[len(result)-2], result[len(result)-1], point) <= 0 {
				result = result[:len(result)-1]
			}
			result = append(result, point)
		}
		return result
	}
	lower := chain(unique)
	reverse := make([]geometryXY, len(unique))
	for i := range unique {
		reverse[i] = unique[len(unique)-1-i]
	}
	upper := chain(reverse)
	return append(lower[:len(lower)-1], upper[:len(upper)-1]...)
}

func cross(a, b, c geometryXY) float64 { return (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X) }

func validateGeometryBars(bars []quant.Bar) error {
	for index, bar := range bars {
		if bar.OpenTime <= 0 || bar.Open <= 0 || bar.High <= 0 || bar.Low <= 0 || bar.Close <= 0 || !finiteGeometryValue(bar.Open) || !finiteGeometryValue(bar.High) || !finiteGeometryValue(bar.Low) || !finiteGeometryValue(bar.Close) || bar.High < math.Max(bar.Open, bar.Close) || bar.Low > math.Min(bar.Open, bar.Close) {
			return fmt.Errorf("invalid OHLC bar %d", index)
		}
		if index > 0 && bar.OpenTime <= bars[index-1].OpenTime {
			return fmt.Errorf("bars are not strictly ordered")
		}
	}
	return nil
}

func finiteGeometryValue(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
