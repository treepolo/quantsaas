package controlresearch

import (
	"reflect"
	"testing"

	robust "quantsaas/internal/robustness"
)

func TestGenerateDiscreteIsDeterministicAppendSafeAndRejectsConstraints(t *testing.T) {
	space := robust.ParameterSpace{SchemaVersion: robust.GridVersion, Axes: []robust.ParameterAxis{
		{Name: "a", Type: robust.ParameterFloat, Values: []float64{0, 1, 2}, LegalMin: 0, LegalMax: 2, Step: 1, StudyStart: 0, StudyEnd: 2},
		{Name: "b", Type: robust.ParameterFloat, Values: []float64{0, 1, 2}, LegalMin: 0, LegalMax: 2, Step: 1, StudyStart: 0, StudyEnd: 2},
	}}
	validator := func(values map[string]float64) error {
		if values["a"] < values["b"] {
			return ErrInvalidInput
		}
		return nil
	}
	first, err := GenerateDiscrete(space, 3, 42, validator)
	if err != nil {
		t.Fatal(err)
	}
	extended, err := GenerateDiscrete(space, 6, 42, validator)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Samples, extended.Samples[:3]) {
		t.Fatalf("existing random prefix changed after append")
	}
	again, _ := GenerateDiscrete(space, 6, 42, validator)
	if !reflect.DeepEqual(extended, again) {
		t.Fatalf("same seed and schema produced a different batch")
	}
	for _, sample := range extended.Samples {
		if sample.Parameters["a"] < sample.Parameters["b"] {
			t.Fatalf("invalid sample escaped validation: %#v", sample)
		}
	}
}

func TestShufflePreservesMultisetAndAppendSequence(t *testing.T) {
	input := []float64{0, .25, .5, .5, 1}
	one, err := Shuffle(input, 9, 3)
	if err != nil {
		t.Fatal(err)
	}
	two, _ := Shuffle(input, 9, 3)
	if !reflect.DeepEqual(one, two) {
		t.Fatal("shuffle is not deterministic")
	}
	counts := func(values []float64) map[float64]int {
		out := map[float64]int{}
		for _, value := range values {
			out[value]++
		}
		return out
	}
	if !reflect.DeepEqual(counts(input), counts(one)) {
		t.Fatal("shuffle changed the exposure multiset")
	}
}

func TestPercentileAndLinearDistribution(t *testing.T) {
	p, err := Percentile(2, []float64{1, 2, 3}, true)
	if err != nil || p != 50 {
		t.Fatalf("higher percentile = %v, %v", p, err)
	}
	risk, _ := Percentile(2, []float64{1, 2, 3}, false)
	if risk != 50 {
		t.Fatalf("lower-is-better percentile = %v", risk)
	}
	distribution, err := Summarize([]float64{4, 1, 3, 2})
	if err != nil || distribution.Median != 2.5 || distribution.Min != 1 || distribution.Max != 4 {
		t.Fatalf("unexpected distribution: %#v, %v", distribution, err)
	}
}
