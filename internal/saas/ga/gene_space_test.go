package ga

import (
	"math"
	"testing"

	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

func TestDirectGridOperatorsProduceLegalCandidates(t *testing.T) {
	evolvable := NewSigmoidDCAEvolvable()
	options := GeneOptions{
		EvolveRebalanceThreshold:  true,
		EvolveForceFullThreshold:  true,
		EvolveForceEmptyThreshold: true,
		EvolveGamma:               true,
		EnableWMean:               true,
		EnableWMomentum:           true,
		EnableWBreakout:           true,
		PositionStructure:         sigmoiddca.PositionStructureDualLayer,
	}
	rng := &gridTestRNG{state: 7}
	first := evolvable.SampleWithOptions(rng, options).(quant.Chromosome)
	second := evolvable.SampleWithOptions(rng, options).(quant.Chromosome)
	for index := 0; index < 500; index++ {
		first = evolvable.MutateWithOptions(first, 0.8, 2, rng, options).(quant.Chromosome)
		child := evolvable.CrossoverWithOptions(first, second, rng, options).(quant.Chromosome)
		assertLegalGridChromosome(t, child, options)
		if child.ForceFullThreshold < child.ForceEmptyThreshold {
			t.Fatalf("candidate %d has illegal force thresholds: %+v", index, child)
		}
		if child.MacroBearMultiplier < child.MacroBullMultiplier {
			t.Fatalf("candidate %d has illegal macro multipliers: %+v", index, child)
		}
		second = child
	}
}

func TestFloatingOnlyDisablesNonEffectiveFieldsAndFingerprint(t *testing.T) {
	evolvable := NewSigmoidDCAEvolvable()
	options := GeneOptions{
		EvolveRebalanceThreshold: true,
		EnableWMean:              true,
		EnableWMomentum:          true,
		EnableWBreakout:          true,
		PositionStructure:        sigmoiddca.PositionStructureFloatingOnly,
	}
	first := quant.DefaultSeedChromosome
	second := first
	second.MicroReservePct = 0.45
	second.DustUSD = 25
	second.WedgeDeltaThreshold = 0.15
	second.WedgeVolRatioThreshold = 2.5
	second.MacroBearMultiplier = 2.5
	second.MacroBullMultiplier = 0.2
	second.ExtraDeployPct = 0.6
	second.SoftReleaseMonths = 3
	second.SoftReleasePct = 0.25
	second.HardReleaseMaxPct = 0.5

	firstNormalized := evolvable.NormalizeGene(first, options).(quant.Chromosome)
	secondNormalized := evolvable.NormalizeGene(second, options).(quant.Chromosome)
	if firstNormalized != secondNormalized {
		t.Fatalf("disabled fields changed normalized candidate:\nfirst=%+v\nsecond=%+v", firstNormalized, secondNormalized)
	}
	if evolvable.FingerprintWithOptions(first, options) != evolvable.FingerprintWithOptions(second, options) {
		t.Fatal("disabled floating-only fields changed candidate fingerprint")
	}
	for _, axis := range ParameterAxes(options) {
		if floatingOnlyDisabledFields[axis.Key] && axis.State != ParameterStateDisabled {
			t.Fatalf("axis %s state = %s, want disabled", axis.Key, axis.State)
		}
		if floatingOnlyDisabledFields[axis.Key] && axis.GridSize != 1 {
			t.Fatalf("disabled axis %s grid size = %d, want 1", axis.Key, axis.GridSize)
		}
		if axis.Key == "rebalance_threshold" && axis.State != ParameterStateEvolving {
			t.Fatalf("rebalance threshold state = %s, want evolving for floating-only", axis.State)
		}
		if axis.Key == "rebalance_threshold" && axis.GridSize <= 1 {
			t.Fatalf("rebalance threshold grid size = %d, want evolving grid", axis.GridSize)
		}
	}

	second.RebalanceThreshold = 0.75
	if evolvable.FingerprintWithOptions(first, options) == evolvable.FingerprintWithOptions(second, options) {
		t.Fatal("floating-only rebalance threshold must change candidate fingerprint")
	}
}

func TestZeroCostsDisableMinimumTradeIdentityOnly(t *testing.T) {
	evolvable := NewSigmoidDCAEvolvable()
	options := GeneOptions{
		EnableWMean:         true,
		EnableWMomentum:     true,
		EnableWBreakout:     true,
		PositionStructure:   sigmoiddca.PositionStructureDualLayer,
		DisableMinimumTrade: true,
	}
	first := quant.DefaultSeedChromosome
	second := first
	second.DustUSD = 25
	if evolvable.FingerprintWithOptions(first, options) != evolvable.FingerprintWithOptions(second, options) {
		t.Fatal("zero-cost disabled minimum trade changed candidate fingerprint")
	}
	for _, axis := range ParameterAxes(options) {
		if axis.Key == "dust_usd" && axis.State != ParameterStateDisabled {
			t.Fatalf("dust axis state = %s, want disabled", axis.State)
		}
	}
}

func TestFixedFieldPreservesExactNonGridValue(t *testing.T) {
	evolvable := NewSigmoidDCAEvolvable()
	base := quant.DefaultSeedChromosome
	base.Beta = 1.23456789
	options := GeneOptions{
		EnableWMean:       true,
		EnableWMomentum:   true,
		EnableWBreakout:   true,
		PositionStructure: sigmoiddca.PositionStructureDualLayer,
		FixedParamKeys:    []string{"beta"},
		FixedGene:         &base,
	}
	rng := &gridTestRNG{state: 11}
	for index := 0; index < 100; index++ {
		candidate := evolvable.SampleWithOptions(rng, options).(quant.Chromosome)
		if candidate.Beta != base.Beta {
			t.Fatalf("fixed beta = %.10f, want %.10f", candidate.Beta, base.Beta)
		}
	}
}

func assertLegalGridChromosome(t *testing.T, candidate quant.Chromosome, options GeneOptions) {
	t.Helper()
	if err := quant.ValidateChromosome(candidate); err != nil {
		t.Fatalf("invalid candidate: %v", err)
	}
	for _, axis := range ParameterAxes(options) {
		if axis.State != ParameterStateEvolving {
			continue
		}
		value := chromosomeValue(candidate, axis.Key)
		if axis.Kind == "int" {
			if value != math.Round(value) {
				t.Fatalf("%s = %v, want integer", axis.Key, value)
			}
			continue
		}
		coordinate := value / CoreSearchGridStep
		if math.Abs(coordinate-math.Round(coordinate)) > 1e-9 {
			t.Fatalf("%s = %.12f, not on 0.05 grid", axis.Key, value)
		}
	}
}

type gridTestRNG struct {
	state uint64
}

func (r *gridTestRNG) next() uint64 {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	return r.state
}

func (r *gridTestRNG) Float64() float64 {
	return float64(r.next()>>11) / float64(uint64(1)<<53)
}

func (r *gridTestRNG) NormFloat64() float64 {
	return (r.Float64() + r.Float64() + r.Float64() + r.Float64()) - 2
}

func (r *gridTestRNG) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}
