package klineinverse

import (
	"math"
	"reflect"
	"testing"
)

func fixturePath() Path {
	return Path{WarmupLength: 2, EvaluationLength: 3, Dates: []int64{1, 2, 3, 4, 5}, Coordinates: []Coordinate{
		{G: 0, B: .01, U: .002, D: .003}, {G: -.005, B: -.01, U: 0, D: .004},
		{G: .002, B: .02, U: .003, D: 0}, {G: 0, B: -.015, U: .002, D: .002}, {G: .001, B: .005, U: .001, D: .001},
	}}
}

func fixtureBounds() Bounds {
	return Bounds{GMin: -.1, GMax: .1, BMin: -.1, BMax: .1, UMin: 0, UMax: .1, DMin: 0, DMax: .1}
}

func TestCoordinatesRoundTripAndScaleInvariance(t *testing.T) {
	path := fixturePath()
	for _, initial := range []float64{100, 123450} {
		bars, err := Reconstruct(path, initial)
		if err != nil {
			t.Fatal(err)
		}
		coordinates, err := Coordinates(bars, initial)
		if err != nil {
			t.Fatal(err)
		}
		for index := range coordinates {
			assertCoordinate(t, coordinates[index], path.Coordinates[index], 1e-12)
		}
	}
}

func TestReflectHandlesMultipleCrossings(t *testing.T) {
	cases := map[float64]float64{-3.25: .75, -1.2: .8, -.2: .2, 1.2: .8, 2.2: .2, 5.25: .75}
	for input, expected := range cases {
		actual, err := Reflect(input)
		if err != nil || math.Abs(actual-expected) > 1e-12 {
			t.Fatalf("Reflect(%g)=%g,%v want %g", input, actual, err, expected)
		}
	}
}

func TestFeaturesAndDistanceSeparateWarmupEvaluation(t *testing.T) {
	path := fixturePath()
	features, err := Features(path)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(features.Warmup.MeanReturn-(-.0025)) > 1e-12 {
		t.Fatalf("warmup mean=%g", features.Warmup.MeanReturn)
	}
	if math.Abs(features.Evaluation.MeanReturn-.013/3) > 1e-12 {
		t.Fatalf("evaluation mean=%g", features.Evaluation.MeanReturn)
	}
	same, err := PathDistance(path, path, fixtureBounds())
	if err != nil || same.Total != 0 {
		t.Fatalf("same distance=%+v,%v", same, err)
	}
	warmupChanged := path
	warmupChanged.Coordinates = append([]Coordinate(nil), path.Coordinates...)
	warmupChanged.Coordinates[0].B += .02
	distance, err := PathDistance(path, warmupChanged, fixtureBounds())
	if err != nil {
		t.Fatal(err)
	}
	if distance.Warmup <= 0 || distance.Evaluation != 0 || distance.Total <= 0 {
		t.Fatalf("distance=%+v", distance)
	}
	reverse, _ := PathDistance(warmupChanged, path, fixtureBounds())
	if math.Abs(distance.Total-reverse.Total) > 1e-15 {
		t.Fatalf("distance not symmetric")
	}
}

func TestHashNormalizesNegativeZeroAndRejectsNonFinite(t *testing.T) {
	a, b := fixturePath(), fixturePath()
	b.Coordinates = append([]Coordinate(nil), b.Coordinates...)
	b.Coordinates[0].G = math.Copysign(0, -1)
	ha, err := Hash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := Hash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("negative zero changed hash")
	}
	b.Coordinates[0].G = math.NaN()
	if _, err := Hash(b); err == nil {
		t.Fatal("expected non-finite hash rejection")
	}
}

func TestDeterministicGenerationCVTAndOperationQuotas(t *testing.T) {
	dates := []int64{1, 2, 3, 4, 5, 6}
	a, err := GenerateGlobal(42, 7, dates, 3, fixtureBounds())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateGlobal(42, 7, dates, 3, fixtureBounds())
	ha, _ := Hash(a)
	hb, _ := Hash(b)
	if ha != hb {
		t.Fatal("generation is not deterministic")
	}
	behaviors := make([]Behavior, 20)
	features := make([][20]float64, 20)
	for i := range behaviors {
		path, _ := GenerateGlobal(91, i, dates, 3, fixtureBounds())
		behaviors[i], _ = Features(path)
	}
	ranges, _ := BuildFeatureRange(behaviors)
	for i := range features {
		features[i] = NormalizeFeatures(behaviors[i], ranges)
	}
	centersA, err := CalibrateCVT(features, 4, 11)
	if err != nil {
		t.Fatal(err)
	}
	centersB, _ := CalibrateCVT(features, 4, 11)
	if centersA != nil && centersA[0] != centersB[0] {
		t.Fatal("CVT is not deterministic")
	}
	quotas := OperationQuotas(12)
	for _, operation := range OperationOrder {
		if quotas[operation] == 0 {
			t.Fatalf("operation %s has zero quota", operation)
		}
	}
}

func TestParetoAndClassification(t *testing.T) {
	path := fixturePath()
	candidates := []Candidate{{ID: "a", Hash: "a", Path: path, QRelative: 1, QAbsolute: 0}, {ID: "b", Hash: "b", Path: path, QRelative: 0, QAbsolute: 1}, {ID: "c", Hash: "c", Path: path, QRelative: .2, QAbsolute: .2}, {ID: "d", Hash: "d", Path: path, QRelative: -1, QAbsolute: -1}}
	selected, err := SelectPareto(candidates, 2, fixtureBounds())
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 {
		t.Fatalf("selected=%d", len(selected))
	}
	outcome, err := Classify(100, 110, 105)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.PassA || !outcome.PassB || outcome.State != StateAB {
		t.Fatalf("outcome=%+v", outcome)
	}
	outcome, _ = Classify(100, 90, 85)
	if !outcome.PassA || outcome.PassB || outcome.State != StateAOnly {
		t.Fatalf("outcome=%+v", outcome)
	}
}

func TestNormalizeRejectsOutOfBoundsAndProbeIsScoped(t *testing.T) {
	bounds := fixtureBounds()
	if _, err := Normalize(Coordinate{G: bounds.GMax + .01, B: 0, U: 0, D: 0}, bounds); err == nil {
		t.Fatal("expected out-of-bounds coordinate rejection")
	}
	anchor := Candidate{ID: "1", Hash: "anchor", Path: fixturePath()}
	child, variation, err := ProbeVary(19, 4, OperationBlock, []Candidate{anchor}, bounds, .1, "H", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if variation.StartIndex < anchor.Path.WarmupLength || variation.Length != 1 {
		t.Fatalf("variation=%+v", variation)
	}
	for index := 0; index < anchor.Path.WarmupLength; index++ {
		if child.Coordinates[index] != anchor.Path.Coordinates[index] {
			t.Fatalf("W coordinate %d changed in H-only probe", index)
		}
	}
	firstHash, _ := Hash(child)
	repeated, _, err := ProbeVary(19, 4, OperationBlock, []Candidate{anchor}, bounds, .1, "H", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, _ := Hash(repeated)
	if firstHash != secondHash {
		t.Fatalf("probe is not deterministic: %s != %s", firstHash, secondHash)
	}
}

func TestGeneratedPathsRemainLegalAcrossLongNonPowerOfTwoLengths(t *testing.T) {
	dates := make([]int64, 257)
	for index := range dates {
		dates[index] = int64(index + 1)
	}
	for sequence := 0; sequence < 64; sequence++ {
		path, err := GenerateGlobal(773, sequence, dates, 112, fixtureBounds())
		if err != nil {
			t.Fatal(err)
		}
		bars, err := Reconstruct(path, 100)
		if err != nil {
			t.Fatal(err)
		}
		for index, bar := range bars {
			if bar.Open <= 0 || bar.Close <= 0 || bar.Low <= 0 || bar.High < math.Max(bar.Open, bar.Close) || bar.Low > math.Min(bar.Open, bar.Close) {
				t.Fatalf("sequence %d bar %d is illegal: %+v", sequence, index, bar)
			}
		}
		coordinates, err := Coordinates(bars, 100)
		if err != nil {
			t.Fatal(err)
		}
		for index := range coordinates {
			assertCoordinate(t, coordinates[index], path.Coordinates[index], 1e-11)
		}
	}
}

func TestSegmentFeaturesMatchHandCalculatedFixture(t *testing.T) {
	segment := []Coordinate{
		{G: .1, B: .2, U: .3, D: .4},
		{G: -.1, B: -.2, U: .1, D: .2},
	}
	path := Path{WarmupLength: 2, EvaluationLength: 2, Dates: []int64{1, 2, 3, 4}, Coordinates: append(append([]Coordinate(nil), segment...), segment...)}
	behavior, err := Features(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := []float64{0, 0, .5, .5, 1, .7, 4.0 / 49.0, 1.0 / 3.0, 5.0 / 7.0, -.2}
	for part, actual := range map[string][10]float64{"W": segmentVector(behavior.Warmup), "H": segmentVector(behavior.Evaluation)} {
		for index := range expected {
			if math.Abs(actual[index]-expected[index]) > 1e-12 {
				t.Fatalf("%s feature %d=%g want %g", part, index, actual[index], expected[index])
			}
		}
	}
	zeros := segmentFeatures([]Coordinate{{}, {}})
	if zeros != (SegmentFeatures{}) {
		t.Fatalf("zero-denominator features=%+v", zeros)
	}
}

func TestEveryVariationOperationAndFallbackIsDeterministic(t *testing.T) {
	base := Candidate{ID: "1", Hash: "one", Path: fixturePath()}
	otherPath, err := GenerateGlobal(91, 3, base.Path.Dates, base.Path.WarmupLength, fixtureBounds())
	if err != nil {
		t.Fatal(err)
	}
	other := Candidate{ID: "2", Hash: "two", Path: otherPath}
	for _, operation := range OperationOrder {
		parents := []Candidate{base}
		if operation == OperationCrossover || operation == OperationDirectional {
			parents = append(parents, other)
		}
		first, variation, err := Vary(808, 14, operation, parents, fixtureBounds(), .2)
		if err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
		second, repeated, err := Vary(808, 14, operation, parents, fixtureBounds(), .2)
		if err != nil {
			t.Fatal(err)
		}
		firstHash, _ := Hash(first)
		secondHash, _ := Hash(second)
		if firstHash != secondHash || !reflect.DeepEqual(variation, repeated) || variation.ActualOperation != operation {
			t.Fatalf("%s is not deterministic: %+v / %+v", operation, variation, repeated)
		}
		if _, err := Reconstruct(first, 100); err != nil {
			t.Fatalf("%s generated illegal path: %v", operation, err)
		}
	}
	for _, operation := range []string{OperationCrossover, OperationDirectional} {
		_, variation, err := Vary(11, 2, operation, []Candidate{base}, fixtureBounds(), .1)
		if err != nil || variation.ActualOperation != OperationGlobal {
			t.Fatalf("%s fallback=%+v, %v", operation, variation, err)
		}
	}
}

func TestAutoKPQuantilesAndAllOutcomeStates(t *testing.T) {
	k, p, err := AutoKP(100)
	if err != nil || k != 10 || p != 4 {
		t.Fatalf("AutoKP(100)=%d,%d,%v", k, p, err)
	}
	k, p, err = AutoKP(10000)
	if err != nil || k != 100 || p != 10 {
		t.Fatalf("AutoKP(10000)=%d,%d,%v", k, p, err)
	}
	quantiles := Quantiles([]float64{4, 1, 3, 2})
	for key, expected := range map[string]float64{"min": 1, "p10": 1.3, "median": 2.5, "p90": 3.7, "max": 4} {
		if math.Abs(quantiles[key]-expected) > 1e-12 {
			t.Fatalf("quantile %s=%g want %g", key, quantiles[key], expected)
		}
	}
	cases := []struct {
		strategy, dca float64
		state         string
	}{
		{110, 105, StateAB},
		{90, 85, StateAOnly},
		{110, 120, StatePositiveButBelowDCA},
		{90, 95, StateNeither},
	}
	for _, test := range cases {
		outcome, err := Classify(100, test.strategy, test.dca)
		if err != nil || outcome.State != test.state {
			t.Fatalf("Classify(100,%g,%g)=%+v,%v", test.strategy, test.dca, outcome, err)
		}
	}
}

func BenchmarkFeaturesAndDistance(b *testing.B) {
	dates := make([]int64, 232)
	for index := range dates {
		dates[index] = int64(index + 1)
	}
	left, _ := GenerateGlobal(42, 1, dates, 112, fixtureBounds())
	right, _ := GenerateGlobal(42, 2, dates, 112, fixtureBounds())
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_, _ = Features(left)
		_, _ = PathDistance(left, right, fixtureBounds())
	}
}

func assertCoordinate(t *testing.T, actual, expected Coordinate, tolerance float64) {
	t.Helper()
	a, e := []float64{actual.G, actual.B, actual.U, actual.D}, []float64{expected.G, expected.B, expected.U, expected.D}
	for index := range a {
		if math.Abs(a[index]-e[index]) > tolerance {
			t.Fatalf("coordinate %d=%g want %g", index, a[index], e[index])
		}
	}
}
