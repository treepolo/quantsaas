package ga

import (
	"testing"

	"quantsaas/internal/quant"
)

func TestAggregateGridPointsUsesCompactCoreLattice(t *testing.T) {
	pack, err := SigmoidDCAEvolvable{}.EncodeResult(quant.DefaultSeedChromosome, nil, GeneOptions{})
	if err != nil {
		t.Fatalf("encode candidate: %v", err)
	}
	points := aggregateGridPoints(42, []CandidateReservation{{Fingerprint: 1, ParamPack: pack}, {Fingerprint: 2, ParamPack: pack}})
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
