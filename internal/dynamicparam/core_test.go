package dynamicparam

import (
	"math"
	"testing"

	"quantsaas/internal/quant"
)

func TestTargetsUseCompleteFuturePathAndCausalMedian(t *testing.T) {
	bars := testBars(30)
	bars[1].High = bars[0].Close * math.Exp(0.20)
	bars[1].Low = bars[0].Close * math.Exp(-0.10)
	targets, err := BuildTargets(bars, HorizonOneDay)
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, targets[0].UpSpace, 0.20)
	assertNear(t, targets[0].DownSpace, 0.10)
	if targets[0].Normalized {
		t.Fatal("first target cannot have a completed causal baseline")
	}
	if !targets[1].Normalized {
		t.Fatal("second target should use the first completed one-day target")
	}
	assertNear(t, targets[1].CausalMedian, targets[0].TotalSpace)

	original := targets[5]
	changed := append([]quant.Bar(nil), bars...)
	changed[20].High *= 10
	rebuilt, err := BuildTargets(changed, HorizonOneDay)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt[5] != original {
		t.Fatal("a later bar changed an earlier one-day target or its causal baseline")
	}
}

func TestFeaturePointsAreCausalAndDimensionless(t *testing.T) {
	bars := testBars(40)
	features, err := BuildFeaturePoints(bars, 10)
	if err != nil {
		t.Fatal(err)
	}
	if features[9].Available {
		t.Fatal("first activity window cannot yet have an expanding history median")
	}
	if !features[10].Available || len(features[10].RawSequence) != 9 {
		t.Fatalf("unexpected causal feature point: %+v", features[10])
	}
	changed := append([]quant.Bar(nil), bars...)
	changed[30].Open *= 1.5
	changed[30].High *= 1.5
	changed[30].Low *= 1.5
	changed[30].Close *= 1.5
	rebuilt, err := BuildFeaturePoints(changed, 10)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt[20].Activity != features[20].Activity || rebuilt[20].HistoryRatio != features[20].HistoryRatio {
		t.Fatal("future OHLC changed an earlier feature point")
	}
}

func TestCalibrationIsMonotoneAndSpaceUncertaintyIsDistinctFromBalance(t *testing.T) {
	calibrator, err := FitProbabilityCalibrator([]float64{0.05, 0.1, 0.2, 0.3, 0.4, 0.6, 0.7, 0.8, 0.9, 0.95}, []float64{0, 0, 1, 0, 1, 0, 1, 1, 1, 1}, 5)
	if err != nil {
		t.Fatal(err)
	}
	previous := -1.0
	for index := 0; index <= 100; index++ {
		value, err := calibrator.Predict(float64(index) / 100)
		if err != nil {
			t.Fatal(err)
		}
		if value+1e-12 < previous {
			t.Fatal("isotonic calibration is not monotone")
		}
		previous = value
	}
	rules := SpaceStateRules{SchemaVersion: SpaceStateVersion, DirectionConfidence: 0.6, DirectionGap: 0.15, DirectionHysteresis: 0.5, MagnitudeConfidence: 0.65, MagnitudeHysteresis: 0.5, MinimumSupport: 1}
	engine, err := NewSpaceStateEngine(rules)
	if err != nil {
		t.Fatal(err)
	}
	uncertain := engine.Step(SixRegionProbabilities{UpSmall: 0.2, UpLarge: 0.15, BalancedSmall: 0.18, BalancedLarge: 0.17, DownSmall: 0.15, DownLarge: 0.15})
	if uncertain.Direction != SpaceDirectionUncertain {
		t.Fatalf("low confidence should be uncertain: %+v", uncertain)
	}
	balanced := engine.Step(SixRegionProbabilities{UpSmall: 0.05, UpLarge: 0.05, BalancedSmall: 0.45, BalancedLarge: 0.25, DownSmall: 0.1, DownLarge: 0.1})
	if balanced.Direction != SpaceDirectionBalanced {
		t.Fatalf("confident balance was treated as uncertainty: %+v", balanced)
	}
}

func TestStateHysteresisOccurrenceAndCausalTrend(t *testing.T) {
	rules := StateRules{
		SchemaVersion: StateSchemaVersion,
		Direction:     DirectionRule{EntryProbability: 0.7, HysteresisRatio: 0.5},
		Volatility:    VolatilityRule{HysteresisRatio: 0.5, Conditions: []VolatilityCondition{{Group: "overall_gap", Metric: "tr_mean", EntryThreshold: 1.5}}},
		ActivityKappa: 5,
	}
	engine, err := NewStateEngine(rules)
	if err != nil {
		t.Fatal(err)
	}
	ratio := ActivityVector{TRMean: 1, TRStdDev: 1, HighLowMean: 1, HighLowStdDev: 1, Parkinson: 1, YangZhang: 1}
	first, err := engine.Step(StateInput{TimeMs: 1, Close: 100, DirectionUpProbability: 0.8, HistoryActivityRatios: ratio, NormalActivityScale: 1, ObservedActivityScale: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, _ := engine.Step(StateInput{TimeMs: 2, Close: 101, DirectionUpProbability: 0.62, HistoryActivityRatios: ratio, NormalActivityScale: 1.1, ObservedActivityScale: 1.1})
	if second.Direction != DirectionUp || second.OccurrenceID != first.OccurrenceID {
		t.Fatal("direction hysteresis should keep the same occurrence")
	}
	third, _ := engine.Step(StateInput{TimeMs: 3, Close: 102, DirectionUpProbability: 0.5, HistoryActivityRatios: ratio, NormalActivityScale: 1.2, ObservedActivityScale: 1.2})
	if third.Direction != DirectionUncertain || third.OccurrenceID == first.OccurrenceID || third.TrendDeviation != nil {
		t.Fatal("state change must start a new occurrence without reusing prior trend data")
	}
	for index := 4; index <= 8; index++ {
		state, stepErr := engine.Step(StateInput{TimeMs: int64(index), Close: float64(99+index) + 0.08*math.Sin(float64(index)), DirectionUpProbability: 0.5, HistoryActivityRatios: ratio, NormalActivityScale: 1, ObservedActivityScale: 1})
		if stepErr != nil {
			t.Fatal(stepErr)
		}
		if index >= 7 && state.TrendDeviation == nil {
			t.Fatal("trend deviation should become available from prior occurrence observations")
		}
	}
}

func TestPolicyModesFallbackAndStructuralValidation(t *testing.T) {
	base := quant.DefaultSeedChromosome
	base.Gamma = 0
	bound := quant.HardBounds["beta"]
	signal := 0.5
	policy := DynamicPolicy{
		SchemaVersion: PolicySchemaVersion, Version: "policy-test-v1",
		Controls: []ParameterControl{
			{ParameterID: "beta", Mode: ControlContinuous, Lower: bound.Min, Upper: bound.Max, BaseValue: base.Beta, BaseLogit: 0, Terms: []ContinuousTerm{{Input: "direction_20d", Linear: 1, Quadratic: 0.5}}},
			{ParameterID: "gamma", Mode: ControlFixed, Lower: quant.HardBounds["gamma"].Min, Upper: quant.HardBounds["gamma"].Max, BaseValue: 0},
		},
	}
	state := StructureState{SchemaVersion: StateSchemaVersion, Direction: DirectionUp, Volatility: VolatilityLow, StateType: "up_low", OccurrenceID: "occ-1", Occurrence: 1}
	result, err := ApplyPolicy(base, policy, PolicyInput{Index: 1, TimeMs: 1, State: state, Signals: map[string]*float64{"direction_20d": &signal}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Chromosome.Beta <= (bound.Min+bound.Max)/2 || len(result.FallbackEvents) != 0 {
		t.Fatalf("continuous policy did not use the bounded signal: %+v", result)
	}
	fallback, err := ApplyPolicy(base, policy, PolicyInput{Index: 2, TimeMs: 2, State: state, Signals: map[string]*float64{}})
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, fallback.Chromosome.Beta, base.Beta)
	if len(fallback.FallbackEvents) != 1 {
		t.Fatalf("missing explicit fallback event: %+v", fallback)
	}

	invalid := policy
	invalid.Controls = append(invalid.Controls, ParameterControl{ParameterID: "force_full_threshold", Mode: ControlGlobal, Lower: 0, Upper: 1, BaseValue: 1, GlobalValue: 0})
	invalid.Controls = append(invalid.Controls, ParameterControl{ParameterID: "force_empty_threshold", Mode: ControlGlobal, Lower: 0, Upper: 1, BaseValue: 0, GlobalValue: 1})
	if _, err := ApplyPolicy(base, invalid, PolicyInput{State: state, Signals: map[string]*float64{"direction_20d": &signal}}); err == nil {
		t.Fatal("cross-parameter structural violation was accepted")
	}
}

func TestPurgedWalkForwardAndOOFTraining(t *testing.T) {
	folds, err := BuildPurgedFolds(100, 3, 40, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, fold := range folds {
		if fold.ValidationStart-fold.TrainEnd-1 < 20 {
			t.Fatalf("purge gap was not preserved: %+v", fold)
		}
	}
	bars := testBars(150)
	config := TrainingConfig{
		Route: RouteExplainable, Lookbacks: []int{5, 10}, Folds: 2, MinimumTrain: 30,
		Learner:    LearnerConfig{Route: RouteExplainable, GAM: GAMConfig{Interactions: true, L1Penalty: 0.0001, L2Penalty: 0.0001, Epochs: 12, LearningRate: 0.01}},
		RegionRule: RegionRule{DirectionBoundary: 0.2, MagnitudeBoundary: 1},
	}
	model, err := TrainHorizon(bars, HorizonOneDay, config)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.OOF) == 0 || model.Direction.Report.WalkForwardVersion != WalkForwardVersion {
		t.Fatalf("missing OOF predictions or report: %+v", model.Direction.Report)
	}
	for _, prediction := range model.OOF {
		total := prediction.SixRegions.UpSmall + prediction.SixRegions.UpLarge + prediction.SixRegions.BalancedSmall + prediction.SixRegions.BalancedLarge + prediction.SixRegions.DownSmall + prediction.SixRegions.DownLarge
		assertNear(t, total, 1)
	}
	predictions, err := model.Predict(bars)
	if err != nil {
		t.Fatal(err)
	}
	if len(predictions) == 0 {
		t.Fatal("frozen horizon model produced no predictions")
	}
	twenty, err := TrainHorizon(bars, HorizonTwentyDay, config)
	if err != nil {
		t.Fatal(err)
	}
	if twenty.StructuralRules == nil || twenty.StructuralReport == nil || twenty.StructuralReport.Volatility.BalancedAccuracy < 0 || twenty.SpaceReport.SchemaVersion != SpaceStateVersion {
		t.Fatalf("missing searched P09 structural rules: %+v", twenty)
	}
}

func TestCausalTCNRouteIsTrainableAndDeterministic(t *testing.T) {
	bars := testBars(55)
	features, err := BuildFeaturePoints(bars, 5)
	if err != nil {
		t.Fatal(err)
	}
	examples := make([]SupervisedExample, 0)
	for index := range features {
		if !features[index].Available {
			continue
		}
		target := 0.0
		if features[index].RawSequence[len(features[index].RawSequence)-1][3] > 0 {
			target = 1
		}
		examples = append(examples, SupervisedExample{Feature: features[index], Target: []float64{target}})
	}
	config := TCNConfig{Hidden: 3, KernelSize: 2, Dilations: []int{1, 2}, Epochs: 8, LearningRate: 0.01, L2Penalty: 0.0001}
	first, err := TrainTCN(examples, 1, LossBinary, config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := TrainTCN(examples, 1, LossBinary, config)
	if err != nil {
		t.Fatal(err)
	}
	left, err := first.Predict(examples[len(examples)-1].Feature)
	if err != nil {
		t.Fatal(err)
	}
	right, err := second.Predict(examples[len(examples)-1].Feature)
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, left[0], right[0])
	if left[0] <= 0 || left[0] >= 1 {
		t.Fatalf("invalid calibrated probability %v", left[0])
	}
}

func testBars(count int) []quant.Bar {
	result := make([]quant.Bar, count)
	for index := range result {
		previous := 100 + float64(index)*0.25
		open := previous * (1 + 0.002*math.Sin(float64(index)))
		close := previous * (1 + 0.003*math.Cos(float64(index)))
		result[index] = quant.Bar{OpenTime: int64(index+1) * 86_400_000, Open: open, High: math.Max(open, close) * 1.01, Low: math.Min(open, close) * 0.99, Close: close, Volume: 1000}
	}
	return result
}

func assertNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}
