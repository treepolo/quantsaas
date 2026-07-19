package parameterresearch

import (
	"fmt"
	"math"
	"testing"

	robust "quantsaas/internal/robustness"
)

func testSpace() robust.ParameterSpace {
	return robust.ParameterSpace{SchemaVersion: robust.GridVersion, Axes: []robust.ParameterAxis{
		{Name: "beta", Label: "Beta", Type: robust.ParameterFloat, Values: []float64{0, .05, .1}, LegalMin: 0, LegalMax: .1, Step: .05, StudyStart: 0, StudyEnd: 2},
		{Name: "gamma", Label: "Gamma", Type: robust.ParameterFloat, Values: []float64{0, .05, .1}, LegalMin: 0, LegalMax: .1, Step: .05, StudyStart: 0, StudyEnd: 2},
	}, Fixed: map[string]float64{"w_mean": .5}}
}

func TestInitialPlanSwitchesToFullEnumerationAndDeduplicatesAnchors(t *testing.T) {
	space := testSpace()
	plan, err := PlanGlobal(space, []int{1, 1}, InitialSobolCount(2), 0, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != "full_enumeration" || plan.UniquePointCount != 9 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	seen := map[string]bool{}
	for _, point := range plan.Points {
		if seen[point.VectorHash] {
			t.Fatal("duplicate vector")
		}
		seen[point.VectorHash] = true
	}
}

func TestSobolContinuationIsDeterministicAndInUnitCube(t *testing.T) {
	first, err := SobolPoint(17, 4)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SobolPoint(17, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if first[i] != second[i] || first[i] < 0 || first[i] >= 1 {
			t.Fatalf("invalid Sobol coordinate %v", first)
		}
	}
	if first[0] == first[1] && first[1] == first[2] {
		t.Fatalf("dimensions unexpectedly collapsed: %v", first)
	}
}

func TestGlobalPlanSupportsAllStaticStrategyDimensions(t *testing.T) {
	space := robust.ParameterSpace{SchemaVersion: robust.GridVersion, Fixed: map[string]float64{}}
	base := make([]int, 18)
	for dimension := 0; dimension < 18; dimension++ {
		space.Axes = append(space.Axes, robust.ParameterAxis{
			Name: fmt.Sprintf("parameter_%d", dimension), Label: "參數", Type: robust.ParameterFloat,
			Values: []float64{0, 1, 2}, LegalMin: 0, LegalMax: 2, Step: 1, StudyStart: 0, StudyEnd: 2,
		})
		base[dimension] = 1
	}
	requested := InitialSobolCount(len(space.Axes))
	plan, err := PlanGlobal(space, base, requested, 0, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RequestedSobol != 512 || plan.UniquePointCount < requested {
		t.Fatalf("unexpected all-parameter plan: %+v", plan)
	}
}

func TestLocalRefinementNeverRepeatsExistingPoint(t *testing.T) {
	space := testSpace()
	centerPoint, _ := plannedPoint(space, []int{1, 1}, "manual", "manual", nil)
	points, err := PlanLocalRefinement(space, []int{1, 1}, 1, map[string]bool{centerPoint.VectorHash: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 8 {
		t.Fatalf("expected 8 missing points, got %d", len(points))
	}
}

func TestLocalRefinementRejectsOversizedHighDimensionalGridBeforeAllocation(t *testing.T) {
	space := robust.ParameterSpace{SchemaVersion: robust.GridVersion, Fixed: map[string]float64{}}
	center := make([]int, 10)
	for dimension := 0; dimension < 10; dimension++ {
		space.Axes = append(space.Axes, robust.ParameterAxis{
			Name: fmt.Sprintf("parameter_%d", dimension), Label: "參數", Type: robust.ParameterFloat,
			Values: []float64{0, 1, 2, 3, 4, 5, 6}, LegalMin: 0, LegalMax: 6, Step: 1, StudyStart: 0, StudyEnd: 6,
		})
		center[dimension] = 3
	}
	if _, err := PlanLocalRefinementLimited(space, center, 3, nil, 300000); err == nil {
		t.Fatal("expected oversized local refinement to be rejected")
	}
}

func TestSurrogateRequiresBatchOOFAndKeepsTargetsIndependent(t *testing.T) {
	examples := make([]SurrogateExample, 0, 128)
	for batch := 0; batch < 2; batch++ {
		for i := 0; i < 64; i++ {
			x := []int{i % 8, (i / 8) % 8}
			y := float64(x[0]) + float64(x[1])*.2
			examples = append(examples, SurrogateExample{Coordinates: x, Batch: []string{"batch-1", "batch-2"}[batch], LogFinalNAVRatio: y, LogDrawdownRatio: -math.Abs(float64(x[0]-4)) + .1*float64(x[1])})
		}
	}
	artifact, err := TrainSurrogate(examples, 64, DefaultForestSettings(42))
	if err != nil {
		t.Fatal(err)
	}
	if len(artifact.ReturnModel.Trees) != 64 || len(artifact.DrawdownModel.Trees) != 64 {
		t.Fatal("missing independent forests")
	}
	if artifact.ReturnOOF.BaselineMAE <= 0 || artifact.DrawdownOOF.BaselineMAE <= 0 {
		t.Fatalf("missing OOF baseline: %+v", artifact)
	}
	scored := ScoreProposalPool(artifact, [][]int{{2, 2}, {6, 6}, {4, 4}})
	if _, err := SelectProposals("pure_coverage", scored, 2, artifact); err != nil {
		t.Fatal(err)
	}
}

func TestComparisonEligibilityFourLevels(t *testing.T) {
	base := ComparisonContext{InstrumentID: "BTC", DatasetHash: "d", Interval: "1d", StartTimeMs: 1, EndTimeMs: 2, StrategyVersion: "s", BacktestCoreVersion: "b", ExecutionMode: "close", InitialCapitalHash: "i", CashFlowHash: "c", CostHash: "fee", BenchmarkVersion: "dca", MetricVersion: "m", ParameterSchemaHash: "p", ResultSchemaVersion: "r", PointSetHash: "points"}
	same := base
	if got := CompareEligibility([]ComparisonContext{base, same}).Level; got != "paired_direct" {
		t.Fatal(got)
	}
	same.PointSetHash = "other"
	if got := CompareEligibility([]ComparisonContext{base, same}).Level; got != "context_matched_unpaired" {
		t.Fatal(got)
	}
	same = base
	same.DatasetHash = "other"
	if got := CompareEligibility([]ComparisonContext{base, same}).Level; got != "descriptive_only" {
		t.Fatal(got)
	}
	same = base
	same.MetricVersion = "other"
	if got := CompareEligibility([]ComparisonContext{base, same}).Level; got != "incompatible" {
		t.Fatal(got)
	}
}
