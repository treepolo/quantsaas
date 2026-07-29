package ga

import (
	"math"
	"math/rand"
	"testing"

	"quantsaas/internal/quant"
)

func TestSearchCandidatesUsePointZeroFiveLattice(t *testing.T) {
	e := SigmoidDCAEvolvable{}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 100; i++ {
		sample := asChromosome(e.Sample(rng))
		assertSearchChromosomeLattice(t, sample)
		if sample.ForceFullThreshold < sample.ForceEmptyThreshold {
			t.Fatalf("sample has invalid force thresholds: %+v", sample)
		}
		mutated := asChromosome(e.Mutate(sample, 1, 3, rng))
		assertSearchChromosomeLattice(t, mutated)
		if mutated.ForceFullThreshold < mutated.ForceEmptyThreshold {
			t.Fatalf("mutation has invalid force thresholds: %+v", mutated)
		}
		crossed := asChromosome(e.Crossover(sample, mutated, rng))
		assertSearchChromosomeLattice(t, crossed)
		if crossed.ForceFullThreshold < crossed.ForceEmptyThreshold {
			t.Fatalf("crossover has invalid force thresholds: %+v", crossed)
		}
	}
}

func TestCandidateReservationConfigIgnoresTaskControlOptions(t *testing.T) {
	e := NewEvolutionEngine(SigmoidDCAEvolvable{}, nil)
	base := EpochConfig{Pair: "SOXL", InstrumentID: "SOXL", DataSource: "yahoo", Interval: "1d", ExecutionMode: "close_next_open", StartTimeMs: 1, EndTimeMs: 2, PopSize: 50, MaxGenerations: 10, SeedGeneID: 1, TraceMode: TraceModeFull}
	changed := base
	changed.PopSize = 500
	changed.MaxGenerations = 99
	changed.SeedGeneID = 999
	changed.TraceMode = TraceModeOff
	changed.RandomPopulation = true
	if string(e.candidateReservationConfig(base)) != string(e.candidateReservationConfig(changed)) {
		t.Fatal("task control options must not affect the cross-task reservation key")
	}
	fixed := base
	fixed.GeneOptions = GeneOptions{FixedParamKeys: []string{"beta"}, FixedGene: &quant.Chromosome{Beta: 1.25}}
	fixedChanged := fixed
	fixedChanged.GeneOptions.FixedGene = &quant.Chromosome{Beta: 1.30}
	if string(e.candidateReservationConfig(fixed)) == string(e.candidateReservationConfig(fixedChanged)) {
		t.Fatal("a fixed core parameter value must affect the cross-task reservation key")
	}
	multi := base
	multi.MultiMarkets = []MarketScope{
		{InstrumentID: "A", DataSource: "yahoo", StartTimeMs: 10, EndTimeMs: 20},
		{InstrumentID: "B", DataSource: "yahoo", StartTimeMs: 30, EndTimeMs: 40},
	}
	multiChanged := multi
	multiChanged.StartTimeMs = 999
	multiChanged.EndTimeMs = 1000
	if string(e.candidateReservationConfig(multi)) != string(e.candidateReservationConfig(multiChanged)) {
		t.Fatal("hidden single-market dates must not affect a multi-market reservation key")
	}
	dataChanged := base
	dataChanged.DatasetHash = "changed-bars"
	if string(e.candidateReservationConfig(base)) == string(e.candidateReservationConfig(dataChanged)) {
		t.Fatal("the evaluated dataset must affect the cross-task reservation key")
	}
}

func assertSearchChromosomeLattice(t *testing.T, c quant.Chromosome) {
	t.Helper()
	values := []float64{
		c.MicroReservePct, c.Beta, c.Gamma, c.WMean, c.WMomentum, c.WBreakout,
		c.DustUSD, c.RebalanceThreshold, c.ForceFullThreshold, c.ForceEmptyThreshold,
		c.WedgeDeltaThreshold, c.WedgeVolRatioThreshold, c.MacroBearMultiplier,
		c.MacroBullMultiplier, c.ExtraDeployPct, c.SoftReleasePct, c.HardReleaseMaxPct,
	}
	for _, value := range values {
		if math.Abs(value/searchParameterStep-math.Round(value/searchParameterStep)) > 1e-9 {
			t.Fatalf("%v is not on the 0.05 lattice", value)
		}
	}
}
