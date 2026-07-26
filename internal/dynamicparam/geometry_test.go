package dynamicparam

import (
	"math"
	"testing"

	"quantsaas/internal/quant"
)

func geometryBars(values ...[4]float64) []quant.Bar {
	result := make([]quant.Bar, len(values))
	for index, value := range values {
		result[index] = quant.Bar{OpenTime: int64(index + 1), Open: value[0], High: value[1], Low: value[2], Close: value[3]}
	}
	return result
}

func TestConvexHullAreaDropsInteriorAndCollinearPoints(t *testing.T) {
	bars := geometryBars([4]float64{2, 4, 1, 3}, [4]float64{3, 5, 2, 4}, [4]float64{4, 6, 3, 5}, [4]float64{5, 7, 4, 6})
	area, err := ConvexHullArea(bars)
	if err != nil {
		t.Fatal(err)
	}
	if area != 9 {
		t.Fatalf("area = %v, want 9", area)
	}
}

func TestTrendSlopeUsesRawCloseScale(t *testing.T) {
	bars := geometryBars([4]float64{10, 11, 9, 10}, [4]float64{20, 21, 19, 20}, [4]float64{30, 31, 29, 30})
	slope, err := TrendSlope(bars)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(slope-10) > 1e-12 {
		t.Fatalf("slope = %v, want 10", slope)
	}
}

func TestGeometrySamplesAreCausalAndUseFutureWindow(t *testing.T) {
	bars := geometryBars(
		[4]float64{10, 11, 9, 10}, [4]float64{11, 13, 10, 12}, [4]float64{12, 14, 11, 13},
		[4]float64{13, 18, 12, 14}, [4]float64{14, 16, 13, 15}, [4]float64{15, 17, 14, 16},
	)
	features, err := BuildGeometryFeatures(bars, 3)
	if err != nil {
		t.Fatal(err)
	}
	if features[2].CoverageArea == features[3].CoverageArea {
		t.Fatal("feature did not move with the causal window")
	}
	targets, err := BuildGeometryTargets(bars, 1)
	if err != nil {
		t.Fatal(err)
	}
	if targets[0].Index != 0 || targets[0].TimeMs != bars[0].OpenTime {
		t.Fatalf("unexpected target identity: %+v", targets[0])
	}
	if !targets[0].Available || targets[0].CoverageArea <= 0 {
		t.Fatal("one-day geometry must use the decision bar as the connecting anchor")
	}
}

func TestGeometryRejectsInvalidBars(t *testing.T) {
	bars := geometryBars([4]float64{10, 9, 8, 10})
	if _, err := ConvexHullArea(bars); err == nil {
		t.Fatal("expected invalid OHLC error")
	}
}
