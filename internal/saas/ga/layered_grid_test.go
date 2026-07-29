package ga

import (
	"math"
	"testing"

	"quantsaas/internal/quant"
)

func TestLayeredGridMarketRegionProducesDistinctStateCandidates(t *testing.T) {
	options := GeneOptions{MarketRegionEnabled: true, MarketRegionMaxThresholds: 1, MarketRegionMaxWindow: 20, PositionStructure: "floating_only"}
	scheduler := newLayeredGridScheduler(quant.DefaultSeedChromosome, options, 100)
	evolvable := NewSigmoidDCAEvolvable()
	seen := map[uint64]bool{}
	for i := 0; i < 20; i++ {
		gene := evolvable.NormalizeGene(scheduler.Next(), options)
		fingerprint := evolvable.Fingerprint(gene)
		seen[fingerprint] = true
	}
	if len(seen) < 10 {
		t.Fatalf("distinct market-region candidates = %d, want at least 10", len(seen))
	}
}

func TestLayeredGridCreatesDirectCoreLatticeAndLegalForcePairs(t *testing.T) {
	options := GeneOptions{
		EvolveRebalanceThreshold: true, EvolveForceFullThreshold: true,
		EvolveForceEmptyThreshold: true, EvolveGamma: true,
		EnableWMean: true, EnableWMomentum: true, EnableWBreakout: true,
	}
	scheduler := newLayeredGridScheduler(quant.DefaultSeedChromosome, options, 70)
	for index := 0; index < 500; index++ {
		chromosome := asChromosome(scheduler.Next())
		for _, value := range []float64{
			chromosome.MicroReservePct, chromosome.Beta, chromosome.Gamma,
			chromosome.WMean, chromosome.WMomentum, chromosome.WBreakout,
			chromosome.DustUSD, chromosome.RebalanceThreshold,
			chromosome.ForceEmptyThreshold, chromosome.ForceFullThreshold,
		} {
			if math.Abs(value/coreSearchStep-math.Round(value/coreSearchStep)) > 1e-8 {
				t.Fatalf("candidate value %v was not created on the 0.05 lattice", value)
			}
		}
		if chromosome.ForceFullThreshold < chromosome.ForceEmptyThreshold {
			t.Fatalf("illegal force pair was generated: full=%v empty=%v", chromosome.ForceFullThreshold, chromosome.ForceEmptyThreshold)
		}
	}
}

func TestLayeredGridRetainsAndSeparatelyAddressesObservedPacks(t *testing.T) {
	features := make([]MarketRegionFeature, 0, len(MarketRegionFeatureIDs))
	for _, id := range MarketRegionFeatureIDs {
		features = append(features, MarketRegionFeature{ID: id, Window: 2})
	}
	first := marketRegionStateChromosome(quant.DefaultSeedChromosome)
	second := first
	second.Gamma = first.Gamma + coreSearchStep
	region := MarketRegionGene{
		SchemaVersion: MarketRegionSchemaVersion,
		Global:        quant.DefaultSeedChromosome, DefaultState: first, Features: features,
		Packs: []MarketRegionPack{{Key: "a", Chromosome: first}, {Key: "b", Chromosome: second}},
	}
	options := GeneOptions{MarketRegionEnabled: true, MarketRegionMaxThresholds: 6, MarketRegionMaxWindow: 20, PositionStructure: "floating_only"}
	scheduler := newLayeredGridScheduler(region, options, 100)
	gene, ok := isMarketRegionGene(scheduler.Next())
	if !ok || len(gene.Packs) != 2 {
		t.Fatalf("observed state packages were discarded: %#v", gene.Packs)
	}
	if gene.Packs[0].Chromosome.Gamma == gene.Packs[1].Chromosome.Gamma {
		t.Fatal("independent state package values were collapsed into one shared chromosome")
	}
}

func TestLayeredGridCheckpointRestoresExactFrontier(t *testing.T) {
	options := GeneOptions{
		EvolveForceFullThreshold: true, EvolveForceEmptyThreshold: true,
		EvolveGamma: true, EnableWMean: true, EnableWMomentum: true, EnableWBreakout: true,
	}
	scheduler := newLayeredGridScheduler(quant.DefaultSeedChromosome, options, 70)
	for index := 0; index < 37; index++ {
		_ = scheduler.Next()
	}
	raw, err := scheduler.Checkpoint()
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	restored, err := restoreLayeredGridScheduler(raw, options)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	evolvable := NewSigmoidDCAEvolvable()
	for index := 0; index < 20; index++ {
		want := evolvable.Fingerprint(scheduler.Next())
		got := evolvable.Fingerprint(restored.Next())
		if got != want {
			t.Fatalf("restored frontier differs at %d: got %x want %x", index, got, want)
		}
	}
}

func TestLayeredThresholdRanksEnumerateAllAllowedCounts(t *testing.T) {
	for cursor := uint64(0); cursor < 6; cursor++ {
		got, offset := layeredThresholdRanks(6, cursor)
		if len(got) != int(cursor)+1 {
			t.Fatalf("cursor %d produced %d ranks, want %d", cursor, len(got), cursor+1)
		}
		if offset != 0 {
			t.Fatalf("first cycle offset = %d, want 0", offset)
		}
	}
	if got, _ := layeredThresholdRanks(0, 0); got != nil {
		t.Fatalf("zero maximum produced %#v", got)
	}
}

func TestLayeredGridRecenterWaitsForCurrentSlice(t *testing.T) {
	scheduler := &layeredGridScheduler{
		centre: quant.DefaultSeedChromosome,
		axes: []layeredAxis{{
			Key: "core:w_mean", Minimum: -60, Maximum: 60,
			Centre: 0, Lower: 0, Upper: 1,
		}},
		axisIndex:    map[string]int{"core:w_mean": 0},
		localPercent: 100,
		slice:        layeredSlice{Axis: 0, Value: 1, Cursor: 0, Total: 2},
	}
	nextCentre := scheduler.centre
	nextCentre.WMean = 2
	scheduler.Recenter(nextCentre)
	if scheduler.centre.WMean == nextCentre.WMean {
		t.Fatal("recenter changed the centre before the current slice was exhausted")
	}
	_, _ = scheduler.nextLocal()
	if scheduler.centre.WMean == nextCentre.WMean {
		t.Fatal("recenter changed the centre while the current slice still had work")
	}
	_, _ = scheduler.nextLocal()
	if scheduler.centre.WMean == nextCentre.WMean {
		t.Fatal("recenter changed the centre before the final point was returned")
	}
	_, _ = scheduler.nextLocal()
	if scheduler.centre.WMean != nextCentre.WMean {
		t.Fatal("recenter was not applied after the current slice was exhausted")
	}
	if scheduler.slice.Axis != -1 {
		t.Fatalf("recentered scheduler must enumerate the shifted local box first, axis=%d", scheduler.slice.Axis)
	}
}
