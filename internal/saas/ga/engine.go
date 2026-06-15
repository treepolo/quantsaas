package ga

import (
	"context"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"time"

	"quantsaas/internal/quant"
)

type GenomeStore interface {
	LoadEliteGenes(ctx context.Context, strategyID string, limit int) ([][]byte, error)
	SaveChallenger(ctx context.Context, strategyID string, paramPack []byte, result FitnessResult) (uint, error)
	LoadKLines(ctx context.Context, symbol string, interval string) ([]quant.Bar, error)
}

type EvolutionEngine struct {
	evolvable EvolvableStrategy
	store     GenomeStore

	PopSize                int
	MaxGenerations         int
	EliteCount             int
	MutationProbability    float64
	MutationScale          float64
	MutationProbabilityMax float64
	MutationScaleMax       float64
	MutationRampFactor     float64
	EarlyStopPatience      int
	EarlyStopMinDelta      float64
	TournamentSize         int
}

type EpochConfig struct {
	Pair               string
	Interval           string
	PopSize            int
	MaxGenerations     int
	LotStepSize        float64
	LotMinQty          float64
	OnProgress         func(EpochProgress)
	SpawnPointOverride *quant.SpawnPoint
}

type EpochProgress struct {
	Generation          int
	BestFitness         float64
	MutationProbability float64
	MutationScale       float64
}

type EpochResult struct {
	GeneRecordID uint
	BestGene     Gene
	Fitness      FitnessResult
	ParamPack    []byte
}

type individual struct {
	Gene    Gene
	Fitness FitnessResult
}

func NewEvolutionEngine(evolvable EvolvableStrategy, store GenomeStore) *EvolutionEngine {
	return &EvolutionEngine{
		evolvable:              evolvable,
		store:                  store,
		PopSize:                300,
		MaxGenerations:         25,
		EliteCount:             8,
		MutationProbability:    0.15,
		MutationScale:          1.0,
		MutationProbabilityMax: 0.55,
		MutationScaleMax:       3.0,
		MutationRampFactor:     1.25,
		EarlyStopPatience:      5,
		EarlyStopMinDelta:      0.001,
		TournamentSize:         3,
	}
}

func (e *EvolutionEngine) RunEpoch(ctx context.Context, cfg EpochConfig) (EpochResult, error) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	plan, err := e.buildEvaluablePlan(ctx, cfg)
	if err != nil {
		return EpochResult{}, err
	}

	population, err := e.initializePopulation(ctx, cfg, rng)
	if err != nil {
		return EpochResult{}, err
	}
	population = e.evaluatePopulation(ctx, population, plan)

	best := bestIndividual(population)
	bestScore := best.Fitness.ScoreTotal
	patience := 0
	mutProb := e.mutationProbability(cfg)
	mutScale := e.mutationScale()
	maxGenerations := e.maxGenerations(cfg)

	for generation := 0; generation < maxGenerations; generation++ {
		if err := ctx.Err(); err != nil {
			return EpochResult{}, err
		}
		sortPopulation(population)
		currentBest := population[0]
		if currentBest.Fitness.ScoreTotal-bestScore < e.EarlyStopMinDelta {
			patience++
		} else {
			best = currentBest
			bestScore = currentBest.Fitness.ScoreTotal
			patience = 0
		}

		if patience >= e.EarlyStopPatience {
			nextProb, nextScale := e.rampMutation(mutProb, mutScale)
			if nextProb == mutProb && nextScale == mutScale {
				break
			}
			mutProb = nextProb
			mutScale = nextScale
			patience = 0
		}

		if cfg.OnProgress != nil {
			cfg.OnProgress(EpochProgress{
				Generation:          generation,
				BestFitness:         bestScore,
				MutationProbability: mutProb,
				MutationScale:       mutScale,
			})
		}

		population = e.nextGeneration(population, mutProb, mutScale, rng)
		population = e.evaluatePopulation(ctx, population, plan)
	}

	sortPopulation(population)
	if population[0].Fitness.ScoreTotal > best.Fitness.ScoreTotal {
		best = population[0]
	}

	paramPack, err := e.evolvable.EncodeResult(best.Gene, plan.Spawn)
	if err != nil {
		return EpochResult{}, err
	}
	id, err := e.store.SaveChallenger(ctx, e.evolvable.StrategyID(), paramPack, best.Fitness)
	if err != nil {
		return EpochResult{}, err
	}
	return EpochResult{
		GeneRecordID: id,
		BestGene:     best.Gene,
		Fitness:      best.Fitness,
		ParamPack:    paramPack,
	}, nil
}

func (e *EvolutionEngine) buildEvaluablePlan(ctx context.Context, cfg EpochConfig) (EvaluablePlan, error) {
	bars, err := e.store.LoadKLines(ctx, cfg.Pair, cfg.Interval)
	if err != nil {
		return EvaluablePlan{}, err
	}
	windows := quant.BuildCrucibleWindows(bars, 1200)
	spawn := cfg.SpawnPointOverride
	if spawn == nil {
		defaultSpawn := quant.SpawnPoint{
			Policy: quant.CapitalPolicy{
				InitialUSDT:       1000,
				MonthlyInjectUSDT: 100,
			},
			Risk: quant.RiskBounds{
				MaxDrawdownPct: 0.88,
				LotStep:        cfg.LotStepSize,
				LotMin:         cfg.LotMinQty,
			},
		}
		spawn = &defaultSpawn
	}

	baselines := make([]DCABaseline, 0, len(windows))
	for _, window := range windows {
		dca := quant.SimulateGhostDCAFrom(window.Bars, window.EvalStartMs, quant.GhostDCAConfig{
			InitialUSDT:       spawn.Policy.InitialUSDT,
			MonthlyInjectUSDT: spawn.Policy.MonthlyInjectUSDT,
		})
		baselines = append(baselines, DCABaseline{
			FinalEquity:   dca.FinalEquity,
			TotalInjected: dca.TotalInjected,
			MaxDrawdown:   dca.MaxDrawdown,
			ROI:           dca.ROI,
		})
	}

	return EvaluablePlan{
		Pair:           cfg.Pair,
		Interval:       cfg.Interval,
		TemplateName:   e.evolvable.StrategyID(),
		Spawn:          spawn,
		LotStep:        cfg.LotStepSize,
		LotMin:         cfg.LotMinQty,
		Windows:        windows,
		DCABaselines:   baselines,
		AggregateCache: map[string]any{},
	}, nil
}

func (e *EvolutionEngine) initializePopulation(ctx context.Context, cfg EpochConfig, rng RandomSource) ([]individual, error) {
	popSize := e.popSize(cfg)
	elitesRaw, err := e.store.LoadEliteGenes(ctx, e.evolvable.StrategyID(), popSize)
	if err != nil {
		return nil, err
	}

	population := make([]individual, 0, popSize)
	if len(elitesRaw) > 0 {
		seed := e.evolvable.DecodeElite(elitesRaw[0])
		population = append(population, individual{Gene: seed})
		remaining := popSize - 1
		copyCount := int(math.Round(float64(remaining) * 0.10))
		mutateCount := int(math.Round(float64(remaining) * 0.40))

		for i := 0; i < copyCount && len(population) < popSize; i++ {
			population = append(population, individual{Gene: e.evolvable.DecodeElite(elitesRaw[i%len(elitesRaw)])})
		}
		for i := 0; i < mutateCount && len(population) < popSize; i++ {
			base := e.evolvable.DecodeElite(elitesRaw[i%len(elitesRaw)])
			population = append(population, individual{Gene: e.evolvable.Mutate(base, 0.15, 1.5, rng)})
		}
	} else {
		population = append(population, individual{Gene: quant.DefaultSeedChromosome})
	}

	for len(population) < popSize {
		population = append(population, individual{Gene: e.evolvable.Sample(rng)})
	}
	return population, nil
}

func (e *EvolutionEngine) evaluatePopulation(ctx context.Context, population []individual, plan EvaluablePlan) []individual {
	workers := runtime.NumCPU()
	if workers > len(population) {
		workers = len(population)
	}
	if workers < 1 {
		workers = 1
	}

	type task struct {
		index int
		gene  Gene
	}
	tasks := make(chan task, len(population))
	var cache sync.Map
	var wg sync.WaitGroup

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range tasks {
				fingerprint := e.evolvable.Fingerprint(task.gene)
				if cached, ok := cache.Load(fingerprint); ok {
					population[task.index].Fitness = cached.(FitnessResult)
					continue
				}
				fitness, err := e.evolvable.Evaluate(ctx, task.gene, plan)
				if err != nil {
					fitness = FitnessResult{ScoreTotal: FatalFitnessScore, Fatal: true}
				}
				cache.Store(fingerprint, fitness)
				population[task.index].Fitness = fitness
			}
		}()
	}

	for i, item := range population {
		tasks <- task{index: i, gene: item.Gene}
	}
	close(tasks)
	wg.Wait()
	return population
}

func (e *EvolutionEngine) nextGeneration(population []individual, mutProb float64, mutScale float64, rng RandomSource) []individual {
	sortPopulation(population)
	popSize := len(population)
	eliteCount := e.EliteCount
	if eliteCount > popSize {
		eliteCount = popSize
	}
	next := make([]individual, 0, popSize)
	next = append(next, population[:eliteCount]...)
	for len(next) < popSize {
		p1 := e.tournamentSelect(population, rng)
		p2 := e.tournamentSelect(population, rng)
		child := e.evolvable.Crossover(p1.Gene, p2.Gene, rng)
		child = e.evolvable.Mutate(child, mutProb, mutScale, rng)
		next = append(next, individual{Gene: child})
	}
	return next
}

func (e *EvolutionEngine) tournamentSelect(population []individual, rng RandomSource) individual {
	size := e.TournamentSize
	if size <= 0 {
		size = 3
	}
	if size > len(population) {
		size = len(population)
	}
	best := population[rng.Intn(len(population))]
	seen := map[int]bool{}
	for len(seen) < size {
		idx := rng.Intn(len(population))
		if seen[idx] {
			continue
		}
		seen[idx] = true
		if population[idx].Fitness.ScoreTotal > best.Fitness.ScoreTotal {
			best = population[idx]
		}
	}
	return best
}

func (e *EvolutionEngine) popSize(cfg EpochConfig) int {
	if cfg.PopSize > 0 {
		return cfg.PopSize
	}
	return e.PopSize
}

func (e *EvolutionEngine) maxGenerations(cfg EpochConfig) int {
	if cfg.MaxGenerations > 0 {
		return cfg.MaxGenerations
	}
	return e.MaxGenerations
}

func (e *EvolutionEngine) mutationProbability(_ EpochConfig) float64 {
	if e.MutationProbability <= 0 {
		return 0.15
	}
	return e.MutationProbability
}

func (e *EvolutionEngine) mutationScale() float64 {
	if e.MutationScale <= 0 {
		return 1.0
	}
	return e.MutationScale
}

func (e *EvolutionEngine) rampMutation(prob float64, scale float64) (float64, float64) {
	factor := e.MutationRampFactor
	if factor <= 0 {
		factor = 1.25
	}
	probMax := e.MutationProbabilityMax
	if probMax <= 0 {
		probMax = 0.55
	}
	scaleMax := e.MutationScaleMax
	if scaleMax <= 0 {
		scaleMax = 3.0
	}
	return math.Min(probMax, prob*factor), math.Min(scaleMax, scale*factor)
}

func sortPopulation(population []individual) {
	sort.Slice(population, func(i, j int) bool {
		return population[i].Fitness.ScoreTotal > population[j].Fitness.ScoreTotal
	})
}

func bestIndividual(population []individual) individual {
	sortPopulation(population)
	if len(population) == 0 {
		return individual{Fitness: FitnessResult{ScoreTotal: FatalFitnessScore, Fatal: true}}
	}
	return population[0]
}
