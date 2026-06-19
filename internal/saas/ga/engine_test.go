package ga

import (
	"context"
	"testing"

	"quantsaas/internal/quant"
)

func TestNextGenerationKeepsElites(t *testing.T) {
	engine := NewEvolutionEngine(fakeEvolvable{}, nil)
	engine.EliteCount = 2
	pop := []individual{
		{Gene: "best", Fitness: FitnessResult{ScoreTotal: 10}},
		{Gene: "second", Fitness: FitnessResult{ScoreTotal: 9}},
		{Gene: "third", Fitness: FitnessResult{ScoreTotal: 1}},
		{Gene: "fourth", Fitness: FitnessResult{ScoreTotal: 0}},
	}

	rng := cyclingRNG{}
	next := engine.nextGeneration(pop, 0, 0, &rng, EpochConfig{}, 1, nil)
	if next[0].Gene != "best" || next[1].Gene != "second" {
		t.Fatalf("elites not preserved: %+v", next[:2])
	}
}

func TestMutationRamp(t *testing.T) {
	engine := NewEvolutionEngine(fakeEvolvable{}, nil)
	prob, scale := engine.rampMutation(0.15, 1.0)
	if prob != 0.1875 {
		t.Fatalf("prob = %f, want 0.1875", prob)
	}
	if scale != 1.25 {
		t.Fatalf("scale = %f, want 1.25", scale)
	}
}

func TestTournamentRarelySelectsFatal(t *testing.T) {
	engine := NewEvolutionEngine(fakeEvolvable{}, nil)
	engine.TournamentSize = 3
	pop := []individual{{Gene: "fatal", Fitness: FitnessResult{ScoreTotal: FatalFitnessScore}}}
	for i := 0; i < 99; i++ {
		pop = append(pop, individual{Gene: i, Fitness: FitnessResult{ScoreTotal: float64(i)}})
	}

	var fatalCount int
	rng := cyclingRNG{}
	for i := 0; i < 1000; i++ {
		if engine.tournamentSelect(pop, &rng).Gene == "fatal" {
			fatalCount++
		}
	}
	if fatalCount >= 50 {
		t.Fatalf("fatal selected %d times, want < 50", fatalCount)
	}
}

type fakeEvolvable struct{}

func (fakeEvolvable) StrategyID() string                                       { return "fake" }
func (fakeEvolvable) Sample(RandomSource) Gene                                 { return "sample" }
func (fakeEvolvable) Mutate(c Gene, _ float64, _ float64, _ RandomSource) Gene { return c }
func (fakeEvolvable) Crossover(p1 Gene, _ Gene, _ RandomSource) Gene           { return p1 }
func (fakeEvolvable) Fingerprint(Gene) uint64                                  { return 0 }
func (fakeEvolvable) Evaluate(context.Context, Gene, EvaluablePlan) (FitnessResult, error) {
	return FitnessResult{}, nil
}
func (fakeEvolvable) DecodeElite([]byte) Gene                              { return "elite" }
func (fakeEvolvable) EncodeResult(Gene, *quant.SpawnPoint) ([]byte, error) { return nil, nil }
func (fakeEvolvable) Verify(context.Context, Gene, *quant.SpawnPoint, []quant.Bar, float64, float64) (BacktestMetrics, error) {
	return BacktestMetrics{}, nil
}

type cyclingRNG struct{ n int }

func (r *cyclingRNG) Float64() float64     { return 0.5 }
func (r *cyclingRNG) NormFloat64() float64 { return 0 }
func (r *cyclingRNG) Intn(n int) int {
	out := r.n % n
	r.n++
	return out
}
