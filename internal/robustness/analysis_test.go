package robustness

import (
	"math"
	"reflect"
	"testing"

	"quantsaas/internal/strategies/sigmoiddca"
)

func TestComputeRelativeMetricsUsesConfirmedFormulas(t *testing.T) {
	metrics, err := ComputeRelativeMetrics(RelativeMetricInput{
		StrategyFinalNAV: 120, BenchmarkFinalNAV: 100,
		StrategyMaxDrawdown: 0.20, BenchmarkMaxDrawdown: 0.30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !metrics.Qualified {
		t.Fatal("expected point to beat the benchmark in both required dimensions")
	}
	wantResidual := 0.8 / 0.7
	if math.Abs(metrics.LogFinalNAVRatio-math.Log(1.2)) > 1e-12 || math.Abs(metrics.DrawdownResidualRatio-wantResidual) > 1e-12 ||
		math.Abs(metrics.LogDrawdownResidualRatio-math.Log(wantResidual)) > 1e-12 || math.Abs(metrics.PerformanceDrawdown-math.Log(1.2)*wantResidual) > 1e-12 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
}

func TestBuildLocalSpaceUsesTwoDecimalDefaultAndExcludesIllegalCrossConstraint(t *testing.T) {
	params := sigmoiddca.DefaultParams()
	params.Chromosome.ForceFullThreshold = 0.05
	params.Chromosome.ForceEmptyThreshold = 0.05
	space, err := BuildLocalSpace(params, []string{"force_full_threshold", "force_empty_threshold"}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if space.Axes[0].Step != 0.05 || space.Axes[1].Step != 0.05 {
		t.Fatalf("default scan precision changed: %+v", space.Axes)
	}
	points, err := Enumerate(space)
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range points {
		if point.Parameters["force_full_threshold"] < point.Parameters["force_empty_threshold"] {
			t.Fatalf("illegal cross-constraint point was enumerated: %+v", point)
		}
	}
}

func TestQualificationRequiresReturnAndDrawdown(t *testing.T) {
	for _, input := range []RelativeMetricInput{
		{StrategyFinalNAV: 99, BenchmarkFinalNAV: 100, StrategyMaxDrawdown: 0.1, BenchmarkMaxDrawdown: 0.2},
		{StrategyFinalNAV: 101, BenchmarkFinalNAV: 100, StrategyMaxDrawdown: 0.2, BenchmarkMaxDrawdown: 0.2},
	} {
		metrics, err := ComputeRelativeMetrics(input)
		if err != nil {
			t.Fatal(err)
		}
		if metrics.Qualified {
			t.Fatalf("input should not qualify: %+v", input)
		}
	}
}

func TestGridEnumeratesOnlyResearchDimensionsAndSamplingIsDeterministic(t *testing.T) {
	space := testSpace2D()
	points, err := Enumerate(space)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 9 {
		t.Fatalf("grid point count = %d, want 9", len(points))
	}
	if points[0].Parameters["fixed"] != 7 {
		t.Fatal("fixed parameter was not retained")
	}
	first, err := SampleNeighborhood(space, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := SampleNeighborhood(space, 5, 0)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("deterministic sampler changed for identical input")
	}
}

func TestAnalyzeSeparatesUnknownAndUsesAxisConnectivity(t *testing.T) {
	space := testSpace2D()
	points, _ := Enumerate(space)
	for index := range points {
		coordinate := points[index].Coordinates
		if coordinate[0] == 1 && coordinate[1] == 1 {
			continue // unknown center gap keeps diagonal lobes disconnected
		}
		qualified := (coordinate[0] == 0 && coordinate[1] == 0) || (coordinate[0] == 2 && coordinate[1] == 2)
		metrics := metricFor(qualified)
		points[index].Metrics = &metrics
	}
	result, err := Analyze(space, points, "0:0", []int{1, 2}, MetricLogFinalNAVRatio)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.MissingCoordinates) != 1 || !reflect.DeepEqual(result.MissingCoordinates[0], []int{1, 1}) {
		t.Fatalf("missing coordinates = %v", result.MissingCoordinates)
	}
	if len(result.Regions) != 2 {
		t.Fatalf("axis-connected region count = %d, want 2", len(result.Regions))
	}
	if result.Scales[1].UnknownPoints != 1 || result.Scales[1].Complete {
		t.Fatalf("unknown point was treated as failure or complete: %+v", result.Scales[1])
	}
}

func TestCenterUsesBoxDepthThenMedoidAndKeepsTies(t *testing.T) {
	space := ParameterSpace{SchemaVersion: GridVersion, Axes: []ParameterAxis{{
		Name: "x", Label: "X", Type: ParameterFloat, Values: []float64{0, 0.05, 0.1, 0.15, 0.2},
		LegalMin: 0, LegalMax: 0.2, Step: 0.05, StudyStart: 0, StudyEnd: 4,
	}}, Fixed: map[string]float64{"fixed": 1}}
	points, _ := Enumerate(space)
	for index := range points {
		qualified := index >= 1 && index <= 3
		metrics := metricFor(qualified)
		metrics.LogFinalNAVRatio += float64(index) * 0.001
		points[index].Metrics = &metrics
	}
	result, err := Analyze(space, points, "2", []int{1, 2}, MetricLogFinalNAVRatio)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Regions) != 1 || len(result.Regions[0].CenterIDs) != 1 || result.Regions[0].CenterIDs[0] != "2" {
		t.Fatalf("unexpected center selection: %+v", result.Regions)
	}
	geometry := result.Regions[0].Geometries
	var center PointGeometry
	for _, item := range geometry {
		if item.PointID == "2" {
			center = item
		}
	}
	if center.GuaranteedBoxRadius != 1 || center.AxisFailureDepth != 2 || center.MedoidCost != 2 {
		t.Fatalf("unexpected center geometry: %+v", center)
	}
	if center.NeighborhoodQuality <= 0 || center.NeighborhoodStability <= 0 || center.NeighborhoodDispersion <= 0 {
		t.Fatalf("formal frontier neighborhood objectives were not calculated: %+v", center)
	}
}

func TestAnalyzeRejectsUnknownMetricInsteadOfSilentlyChangingZAxis(t *testing.T) {
	space := testSpace2D()
	points, _ := Enumerate(space)
	for index := range points {
		metrics := metricFor(true)
		points[index].Metrics = &metrics
	}
	if _, err := Analyze(space, points, "0:0", []int{1}, MetricName("not-a-metric")); err == nil {
		t.Fatal("unknown metric should be rejected")
	}
}

func testSpace2D() ParameterSpace {
	return ParameterSpace{SchemaVersion: GridVersion, Axes: []ParameterAxis{
		{Name: "x", Label: "X", Type: ParameterFloat, Values: []float64{0, 0.05, 0.1}, LegalMin: 0, LegalMax: 0.1, Step: 0.05, StudyStart: 0, StudyEnd: 2},
		{Name: "y", Label: "Y", Type: ParameterFloat, Values: []float64{1, 2, 3}, LegalMin: 1, LegalMax: 3, Step: 1, StudyStart: 0, StudyEnd: 2},
	}, Fixed: map[string]float64{"fixed": 7}}
}

func metricFor(qualified bool) RelativeMetrics {
	if qualified {
		metrics, _ := ComputeRelativeMetrics(RelativeMetricInput{StrategyFinalNAV: 110, BenchmarkFinalNAV: 100, StrategyMaxDrawdown: 0.1, BenchmarkMaxDrawdown: 0.2})
		return metrics
	}
	metrics, _ := ComputeRelativeMetrics(RelativeMetricInput{StrategyFinalNAV: 90, BenchmarkFinalNAV: 100, StrategyMaxDrawdown: 0.3, BenchmarkMaxDrawdown: 0.2})
	return metrics
}
