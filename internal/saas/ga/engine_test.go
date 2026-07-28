package ga

import (
	"context"
	"encoding/json"
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
	next, err := engine.nextGeneration(pop, 0, 0, &rng, EpochConfig{}, 1, nil)
	if err != nil {
		t.Fatalf("next generation: %v", err)
	}
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

func TestSearchConfigIncludesExecutionCostsAndCapitalPolicy(t *testing.T) {
	engine := NewEvolutionEngine(fakeEvolvable{}, nil)
	raw := engine.searchConfig(EpochConfig{
		Pair:           "SOXL",
		InstrumentID:   "SOXL",
		DataSource:     "yahoo",
		Interval:       "1d",
		ExecutionMode:  "close_next_open",
		InitialCapital: 1000000,
		MonthlyDCA:     250,
		GeneOptions: GeneOptions{
			EvolveRebalanceThreshold:  true,
			EvolveForceFullThreshold:  true,
			EvolveForceEmptyThreshold: true,
			EvolveGamma:               true,
			EnableWMean:               true,
			EnableWMomentum:           true,
			EnableWBreakout:           true,
			PositionStructure:         "floating_only",
			FixedParamKeys:            []string{"beta", "gamma"},
		},
		TradePenalty: 0.002,
		Costs: quant.ExecutionCostConfig{
			FeeRate:    0.001,
			SpreadRate: 0.0005,
		},
		SeedGeneID: 42,
	})

	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("search config json invalid: %v", err)
	}
	if cfg["fee_rate"] != 0.001 {
		t.Fatalf("fee_rate = %v, want 0.001", cfg["fee_rate"])
	}
	if cfg["spread_rate"] != 0.0005 {
		t.Fatalf("spread_rate = %v, want 0.0005", cfg["spread_rate"])
	}
	if cfg["initial_capital"] != float64(1000000) {
		t.Fatalf("initial_capital = %v, want 1000000", cfg["initial_capital"])
	}
	if cfg["monthly_dca"] != float64(250) {
		t.Fatalf("monthly_dca = %v, want 250", cfg["monthly_dca"])
	}
	geneOptions, ok := cfg["gene_options"].(map[string]any)
	if !ok {
		t.Fatalf("gene_options = %T, want object", cfg["gene_options"])
	}
	if geneOptions["EvolveRebalanceThreshold"] != true {
		t.Fatalf("EvolveRebalanceThreshold = %v, want true", geneOptions["EvolveRebalanceThreshold"])
	}
	if geneOptions["EvolveGamma"] != true {
		t.Fatalf("EvolveGamma = %v, want true", geneOptions["EvolveGamma"])
	}
	if geneOptions["PositionStructure"] != "floating_only" {
		t.Fatalf("PositionStructure = %v, want floating_only", geneOptions["PositionStructure"])
	}
	if cfg["seed_gene_id"] != float64(42) {
		t.Fatalf("seed_gene_id = %v, want 42", cfg["seed_gene_id"])
	}
	fixedKeys, ok := cfg["fixed_param_keys"].([]any)
	if !ok || len(fixedKeys) != 2 || fixedKeys[0] != "beta" || fixedKeys[1] != "gamma" {
		t.Fatalf("fixed_param_keys = %#v, want beta/gamma", cfg["fixed_param_keys"])
	}
	geneFixedKeys, ok := geneOptions["FixedParamKeys"].([]any)
	if !ok || len(geneFixedKeys) != 2 || geneFixedKeys[0] != "beta" || geneFixedKeys[1] != "gamma" {
		t.Fatalf("gene option FixedParamKeys = %#v, want beta/gamma", geneOptions["FixedParamKeys"])
	}
	if cfg["trade_penalty"] != 0.002 {
		t.Fatalf("trade_penalty = %v, want 0.002", cfg["trade_penalty"])
	}
}

func TestPlanComputeUnitsSumsWindowBars(t *testing.T) {
	plan := EvaluablePlan{
		Windows: []quant.CrucibleWindow{
			{Label: "a", Bars: make([]quant.Bar, 3)},
			{Label: "b", Bars: make([]quant.Bar, 5)},
		},
	}
	if got := planComputeUnits(plan); got != 8 {
		t.Fatalf("planComputeUnits = %d, want 8", got)
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
func (fakeEvolvable) DecodeElite([]byte) Gene { return "elite" }
func (fakeEvolvable) EncodeResult(Gene, *quant.SpawnPoint, GeneOptions) ([]byte, error) {
	return nil, nil
}
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
