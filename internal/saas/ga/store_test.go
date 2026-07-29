package ga

import (
	"math"
	"testing"

	"quantsaas/internal/quant"
)

func TestAggregateGridPointsUsesCompactCoreLattice(t *testing.T) {
	pack, err := SigmoidDCAEvolvable{}.EncodeResult(quant.DefaultSeedChromosome, nil, GeneOptions{})
	if err != nil {
		t.Fatalf("encode candidate: %v", err)
	}
	points := aggregateGridPoints(42, "test-scope", []CandidateReservation{{Fingerprint: 1, ParamPack: pack}, {Fingerprint: 2, ParamPack: pack}})
	if len(points) != len(chromosomeGridValues(quant.DefaultSeedChromosome)) {
		t.Fatalf("point count = %d", len(points))
	}
	var betaFound bool
	for _, point := range points {
		if point.TaskID != 42 {
			t.Fatalf("task id = %d", point.TaskID)
		}
		if point.ParameterKey == "beta" && point.GridStep == 40 {
			betaFound = true
			if point.Count != 2 {
				t.Fatalf("beta count = %d, want 2", point.Count)
			}
		}
	}
	if !betaFound {
		t.Fatal("beta lattice point missing")
	}
}

func TestMarketThresholdGridKeepsRawSmallValue(t *testing.T) {
	key := "market_region.parkinson.threshold_1"
	value := 0.000013579
	step := gridStoredStep(key, value)
	if step == 0 {
		t.Fatal("raw market threshold was collapsed to zero")
	}
	if restored := GridStoredValue(key, step); math.Abs(restored-value) > 1e-12 {
		t.Fatalf("restored=%0.15f want=%0.15f", restored, value)
	}
}
