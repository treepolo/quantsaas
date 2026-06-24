package ga

import (
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"time"

	"quantsaas/internal/quant"
)

type GenomeStore interface {
	LoadEliteGenes(ctx context.Context, scope GeneScope, limit int) ([][]byte, error)
	SaveChallenger(ctx context.Context, scope GeneScope, paramPack []byte, result FitnessResult, searchConfig []byte) (uint, error)
	LoadObservedFingerprints(ctx context.Context, scope GeneScope, searchConfig []byte) (map[uint64]bool, error)
	SaveObservedGenes(ctx context.Context, scope GeneScope, searchConfig []byte, observations []GeneObservation) error
	LoadKLines(ctx context.Context, scope DatasetScope) ([]quant.Bar, error)
}

type GeneScope struct {
	StrategyID    string
	InstrumentID  string
	DataSource    string
	Interval      string
	ExecutionMode string
}

type DatasetScope struct {
	InstrumentID string
	DataSource   string
	Symbol       string
	Interval     string
	StartTimeMs  int64
	EndTimeMs    int64
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
	TaskID             uint
	Pair               string
	InstrumentID       string
	DataSource         string
	ExecutionMode      string
	StartTimeMs        int64
	EndTimeMs          int64
	Interval           string
	PopSize            int
	MaxGenerations     int
	SpawnMode          string
	LotStepSize        float64
	LotMinQty          float64
	InitialCapital     float64
	MonthlyDCA         float64
	GeneOptions        GeneOptions
	Costs              quant.ExecutionCostConfig
	TradePenalty       float64
	OnProgress         func(EpochProgress)
	OnTrace            func(TraceEvent)
	TraceMode          TraceMode
	TraceModeFunc      func() TraceMode
	SpawnPointOverride *quant.SpawnPoint
	SeedParamPack      []byte
	RandomPopulation   bool
}

type EpochProgress struct {
	Generation          int
	BestFitness         float64
	BestMaxDrawdown     float64
	BestWindows         []quant.CrucibleResult
	BestParamPack       []byte
	MutationProbability float64
	MutationScale       float64
}

type GeneObservation struct {
	TaskID      uint
	Generation  int
	Individual  int
	Fingerprint uint64
	ParamPack   []byte
	Fitness     FitnessResult
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
	cfg.TraceMode = NormalizeTraceMode(cfg.TraceMode)
	cfg.GeneOptions = NormalizeGeneOptions(cfg.GeneOptions)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	e.trace(cfg, TraceModeSummary, "evolution", "epoch.start", "epoch started", map[string]any{
		"pair":            cfg.Pair,
		"instrument_id":   cfg.InstrumentID,
		"data_source":     cfg.DataSource,
		"interval":        cfg.Interval,
		"execution_mode":  cfg.ExecutionMode,
		"population":      e.popSize(cfg),
		"max_generations": e.maxGenerations(cfg),
		"initial_capital": e.initialCapital(cfg),
		"monthly_dca":     e.monthlyDCA(cfg),
		"gene_options":    cfg.GeneOptions,
		"fee_rate":        cfg.Costs.FeeRate,
		"spread_rate":     cfg.Costs.SpreadRate,
		"trade_penalty":   cfg.TradePenalty,
		"trace_mode":      cfg.TraceMode,
	})
	plan, err := e.buildEvaluablePlan(ctx, cfg)
	if err != nil {
		e.trace(cfg, TraceModeSummary, "evolution", "epoch.failed", "failed to build evaluable plan", map[string]any{
			"error": err.Error(),
		})
		return EpochResult{}, err
	}
	plan.Trace = cfg.OnTrace
	plan.TraceMode = cfg.TraceMode
	plan.TraceModeFunc = cfg.TraceModeFunc
	searchConfig := e.searchConfig(cfg)
	scope := GeneScope{
		StrategyID:    e.evolvable.StrategyID(),
		InstrumentID:  cfg.InstrumentID,
		DataSource:    cfg.DataSource,
		Interval:      cfg.Interval,
		ExecutionMode: cfg.ExecutionMode,
	}
	knownFingerprints, err := e.store.LoadObservedFingerprints(ctx, scope, searchConfig)
	if err != nil {
		return EpochResult{}, err
	}

	population, err := e.initializePopulation(ctx, cfg, rng)
	if err != nil {
		e.trace(cfg, TraceModeSummary, "evolution", "epoch.failed", "failed to initialize population", map[string]any{
			"error": err.Error(),
		})
		return EpochResult{}, err
	}
	population = e.deduplicatePopulation(population, knownFingerprints, rng, true, cfg)
	population = e.evaluatePopulation(ctx, population, plan, 0, cfg)
	e.saveObservedPopulation(ctx, scope, searchConfig, population, plan, 0, cfg, knownFingerprints)

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
				e.trace(cfg, TraceModeSummary, "evolution", "epoch.early_stop", "mutation ramp reached limit", map[string]any{
					"generation":           generation,
					"mutation_probability": mutProb,
					"mutation_scale":       mutScale,
				})
				break
			}
			mutProb = nextProb
			mutScale = nextScale
			patience = 0
			e.trace(cfg, TraceModeSummary, "evolution", "mutation.ramp", "mutation parameters ramped", map[string]any{
				"generation":           generation,
				"mutation_probability": mutProb,
				"mutation_scale":       mutScale,
			})
		}

		if cfg.OnProgress != nil {
			paramPack, _ := e.evolvable.EncodeResult(best.Gene, plan.Spawn, cfg.GeneOptions)
			cfg.OnProgress(EpochProgress{
				Generation:          generation,
				BestFitness:         bestScore,
				BestMaxDrawdown:     best.Fitness.MaxDrawdown,
				BestWindows:         best.Fitness.Windows,
				BestParamPack:       paramPack,
				MutationProbability: mutProb,
				MutationScale:       mutScale,
			})
		}
		e.trace(cfg, TraceModeSummary, "evolution", "generation.completed", "generation completed", map[string]any{
			"generation":           generation + 1,
			"best_score":           bestScore,
			"max_drawdown":         best.Fitness.MaxDrawdown,
			"mutation_probability": mutProb,
			"mutation_scale":       mutScale,
		})

		population = e.nextGeneration(population, mutProb, mutScale, rng, cfg, generation+1, knownFingerprints)
		population = e.evaluatePopulation(ctx, population, plan, generation+1, cfg)
		e.saveObservedPopulation(ctx, scope, searchConfig, population, plan, generation+1, cfg, knownFingerprints)
	}

	sortPopulation(population)
	if population[0].Fitness.ScoreTotal > best.Fitness.ScoreTotal {
		best = population[0]
	}

	paramPack, err := e.evolvable.EncodeResult(best.Gene, plan.Spawn, cfg.GeneOptions)
	if err != nil {
		return EpochResult{}, err
	}
	id, err := e.store.SaveChallenger(ctx, scope, paramPack, best.Fitness, searchConfig)
	if err != nil {
		return EpochResult{}, err
	}
	e.trace(cfg, TraceModeSummary, "evolution", "epoch.completed", "epoch completed", map[string]any{
		"gene_record_id": id,
		"best_score":     best.Fitness.ScoreTotal,
		"max_drawdown":   best.Fitness.MaxDrawdown,
	})
	return EpochResult{
		GeneRecordID: id,
		BestGene:     best.Gene,
		Fitness:      best.Fitness,
		ParamPack:    paramPack,
	}, nil
}

func (e *EvolutionEngine) EvaluateParamPack(ctx context.Context, cfg EpochConfig, paramPack []byte) (FitnessResult, error) {
	cfg.TraceMode = NormalizeTraceMode(cfg.TraceMode)
	cfg.GeneOptions = NormalizeGeneOptions(cfg.GeneOptions)
	plan, err := e.buildEvaluablePlan(ctx, cfg)
	if err != nil {
		return FitnessResult{}, err
	}
	plan.Trace = cfg.OnTrace
	plan.TraceMode = cfg.TraceMode
	plan.TraceModeFunc = cfg.TraceModeFunc
	gene := e.normalizeGene(cfg, e.evolvable.DecodeElite(paramPack))
	return e.evolvable.Evaluate(ctx, gene, plan)
}

func (e *EvolutionEngine) searchConfig(cfg EpochConfig) []byte {
	cfg.GeneOptions = NormalizeGeneOptions(cfg.GeneOptions)
	spawnMode := cfg.SpawnMode
	if spawnMode == "" {
		spawnMode = "inherit"
	}
	raw, err := json.Marshal(map[string]any{
		"strategy_id":     e.evolvable.StrategyID(),
		"symbol":          cfg.Pair,
		"instrument_id":   cfg.InstrumentID,
		"data_source":     cfg.DataSource,
		"interval":        cfg.Interval,
		"execution_mode":  cfg.ExecutionMode,
		"train_start_ms":  cfg.StartTimeMs,
		"train_end_ms":    cfg.EndTimeMs,
		"initial_capital": e.initialCapital(cfg),
		"monthly_dca":     e.monthlyDCA(cfg),
		"gene_options":    cfg.GeneOptions,
		"fee_rate":        quant.NormalizeExecutionCosts(cfg.Costs).FeeRate,
		"spread_rate":     quant.NormalizeExecutionCosts(cfg.Costs).SpreadRate,
		"trade_penalty":   cfg.TradePenalty,
		"spawn_mode":      spawnMode,
		"population":      e.popSize(cfg),
		"generations":     e.maxGenerations(cfg),
	})
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}

func (e *EvolutionEngine) buildEvaluablePlan(ctx context.Context, cfg EpochConfig) (EvaluablePlan, error) {
	cfg.GeneOptions = NormalizeGeneOptions(cfg.GeneOptions)
	e.trace(cfg, TraceModeSummary, "market_data", "klines.load", "loading historical bars", map[string]any{
		"pair":          cfg.Pair,
		"instrument_id": cfg.InstrumentID,
		"data_source":   cfg.DataSource,
		"interval":      cfg.Interval,
		"start_time_ms": cfg.StartTimeMs,
		"end_time_ms":   cfg.EndTimeMs,
	})
	bars, err := e.store.LoadKLines(ctx, DatasetScope{
		InstrumentID: cfg.InstrumentID,
		DataSource:   cfg.DataSource,
		Symbol:       cfg.Pair,
		Interval:     cfg.Interval,
		StartTimeMs:  cfg.StartTimeMs,
		EndTimeMs:    cfg.EndTimeMs,
	})
	if err != nil {
		return EvaluablePlan{}, err
	}
	e.trace(cfg, TraceModeSummary, "market_data", "klines.loaded", "historical bars loaded", map[string]any{
		"pair":     cfg.Pair,
		"interval": cfg.Interval,
		"bars":     len(bars),
	})
	windows := quant.BuildCrucibleWindows(bars, 1200)
	costs := quant.NormalizeExecutionCosts(cfg.Costs)
	spawn := cfg.SpawnPointOverride
	if spawn == nil {
		defaultSpawn := quant.SpawnPoint{
			Policy: quant.CapitalPolicy{
				InitialUSDT:       e.initialCapital(cfg),
				MonthlyInjectUSDT: e.monthlyDCA(cfg),
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
			UseOpenExecution:  usesNextOpenExecution(cfg.ExecutionMode),
			Costs:             costs,
		})
		baselines = append(baselines, DCABaseline{
			FinalEquity:   dca.FinalEquity,
			TotalInjected: dca.TotalInjected,
			MaxDrawdown:   dca.MaxDrawdown,
			ROI:           dca.ROI,
		})
		e.trace(cfg, TraceModeDetailed, "evolution", "window.prepared", "evaluation window prepared", map[string]any{
			"window":                window.Label,
			"bars":                  len(window.Bars),
			"weight":                window.Weight,
			"baseline_roi":          dca.ROI,
			"baseline_max_drawdown": dca.MaxDrawdown,
			"baseline_final_equity": dca.FinalEquity,
			"baseline_total_inject": dca.TotalInjected,
		})
	}

	return EvaluablePlan{
		Pair:           cfg.Pair,
		Interval:       cfg.Interval,
		ExecutionMode:  cfg.ExecutionMode,
		TemplateName:   e.evolvable.StrategyID(),
		Spawn:          spawn,
		Costs:          costs,
		TradePenalty:   math.Max(0, cfg.TradePenalty),
		GeneOptions:    cfg.GeneOptions,
		LotStep:        cfg.LotStepSize,
		LotMin:         cfg.LotMinQty,
		Windows:        windows,
		DCABaselines:   baselines,
		AggregateCache: map[string]any{},
	}, nil
}

func (e *EvolutionEngine) initialCapital(cfg EpochConfig) float64 {
	if cfg.InitialCapital > 0 {
		return cfg.InitialCapital
	}
	return 1000000
}

func (e *EvolutionEngine) monthlyDCA(cfg EpochConfig) float64 {
	if cfg.MonthlyDCA > 0 {
		return cfg.MonthlyDCA
	}
	return 0
}

func (e *EvolutionEngine) normalizeGene(cfg EpochConfig, gene Gene) Gene {
	if normalizer, ok := e.evolvable.(GeneNormalizer); ok {
		return normalizer.NormalizeGene(gene, cfg.GeneOptions)
	}
	return gene
}

func (e *EvolutionEngine) initializePopulation(ctx context.Context, cfg EpochConfig, rng RandomSource) ([]individual, error) {
	popSize := e.popSize(cfg)
	elitesRaw := [][]byte{}
	if len(cfg.SeedParamPack) > 0 {
		elitesRaw = append(elitesRaw, cfg.SeedParamPack)
	}
	if !cfg.RandomPopulation {
		loaded, err := e.store.LoadEliteGenes(ctx, GeneScope{
			StrategyID:    e.evolvable.StrategyID(),
			InstrumentID:  cfg.InstrumentID,
			DataSource:    cfg.DataSource,
			Interval:      cfg.Interval,
			ExecutionMode: cfg.ExecutionMode,
		}, popSize)
		if err != nil {
			return nil, err
		}
		elitesRaw = append(elitesRaw, loaded...)
	}

	population := make([]individual, 0, popSize)
	if len(elitesRaw) > 0 {
		seed := e.normalizeGene(cfg, e.evolvable.DecodeElite(elitesRaw[0]))
		population = append(population, individual{Gene: seed})
		remaining := popSize - 1
		copyCount := int(math.Round(float64(remaining) * 0.10))
		mutateCount := int(math.Round(float64(remaining) * 0.40))

		for i := 0; i < copyCount && len(population) < popSize; i++ {
			population = append(population, individual{Gene: e.normalizeGene(cfg, e.evolvable.DecodeElite(elitesRaw[i%len(elitesRaw)]))})
		}
		for i := 0; i < mutateCount && len(population) < popSize; i++ {
			base := e.normalizeGene(cfg, e.evolvable.DecodeElite(elitesRaw[i%len(elitesRaw)]))
			population = append(population, individual{Gene: e.normalizeGene(cfg, e.evolvable.Mutate(base, 0.15, 1.5, rng))})
		}
	} else if !cfg.RandomPopulation {
		population = append(population, individual{Gene: e.normalizeGene(cfg, quant.DefaultSeedChromosome)})
	}

	for len(population) < popSize {
		population = append(population, individual{Gene: e.normalizeGene(cfg, e.evolvable.Sample(rng))})
	}
	e.trace(cfg, TraceModeSummary, "evolution", "population.initialized", "initial population initialized", map[string]any{
		"population":  len(population),
		"elite_count": len(elitesRaw),
	})
	return population, nil
}

func (e *EvolutionEngine) evaluatePopulation(ctx context.Context, population []individual, plan EvaluablePlan, generation int, cfg EpochConfig) []individual {
	workers := runtime.NumCPU()
	if workers > len(population) {
		workers = len(population)
	}
	if workers < 1 {
		workers = 1
	}
	e.trace(cfg, TraceModeSummary, "evolution", "population.evaluate", "population evaluation started", map[string]any{
		"generation": generation,
		"population": len(population),
		"workers":    workers,
	})

	type task struct {
		index int
		gene  Gene
	}
	tasks := make(chan task, len(population))
	var cache sync.Map
	var wg sync.WaitGroup

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range tasks {
				fingerprint := e.evolvable.Fingerprint(task.gene)
				if cached, ok := cache.Load(fingerprint); ok {
					population[task.index].Fitness = cached.(FitnessResult)
					e.trace(cfg, TraceModeDetailed, "evolution", "individual.cache_hit", "cached fitness reused", map[string]any{
						"generation":  generation,
						"individual":  task.index,
						"worker":      workerID,
						"fingerprint": fingerprint,
					})
					continue
				}
				e.trace(cfg, TraceModeDetailed, "evolution", "individual.evaluate.start", "individual evaluation started", map[string]any{
					"generation":  generation,
					"individual":  task.index,
					"worker":      workerID,
					"fingerprint": fingerprint,
				})
				evalPlan := plan
				evalPlan.Generation = generation
				evalPlan.Individual = task.index
				evalPlan.Worker = workerID
				fitness, err := e.evolvable.Evaluate(ctx, task.gene, evalPlan)
				if err != nil {
					fitness = FitnessResult{ScoreTotal: FatalFitnessScore, Fatal: true}
				}
				cache.Store(fingerprint, fitness)
				population[task.index].Fitness = fitness
				e.trace(cfg, TraceModeDetailed, "evolution", "individual.evaluate.done", "individual evaluation completed", map[string]any{
					"generation":   generation,
					"individual":   task.index,
					"worker":       workerID,
					"fingerprint":  fingerprint,
					"score":        fitness.ScoreTotal,
					"max_drawdown": fitness.MaxDrawdown,
					"fatal":        fitness.Fatal,
				})
			}
		}(worker)
	}

	for i, item := range population {
		tasks <- task{index: i, gene: item.Gene}
	}
	close(tasks)
	wg.Wait()
	e.trace(cfg, TraceModeSummary, "evolution", "population.evaluated", "population evaluation completed", map[string]any{
		"generation": generation,
		"population": len(population),
	})
	return population
}

func (e *EvolutionEngine) saveObservedPopulation(ctx context.Context, scope GeneScope, searchConfig []byte, population []individual, plan EvaluablePlan, generation int, cfg EpochConfig, knownFingerprints map[uint64]bool) {
	if len(population) == 0 {
		return
	}
	observations := make([]GeneObservation, 0, len(population))
	for i, item := range population {
		fingerprint := e.evolvable.Fingerprint(item.Gene)
		paramPack, err := e.evolvable.EncodeResult(item.Gene, plan.Spawn, cfg.GeneOptions)
		if err != nil {
			continue
		}
		observations = append(observations, GeneObservation{
			TaskID:      cfg.TaskID,
			Generation:  generation,
			Individual:  i,
			Fingerprint: fingerprint,
			ParamPack:   paramPack,
			Fitness:     item.Fitness,
		})
		if knownFingerprints != nil {
			knownFingerprints[fingerprint] = true
		}
	}
	if len(observations) == 0 {
		return
	}
	if err := e.store.SaveObservedGenes(ctx, scope, searchConfig, observations); err != nil {
		e.trace(cfg, TraceModeSummary, "evolution", "gene_observations.save_failed", "failed to save gene observations", map[string]any{
			"generation": generation,
			"error":      err.Error(),
		})
	}
}

func (e *EvolutionEngine) deduplicatePopulation(population []individual, knownFingerprints map[uint64]bool, rng RandomSource, preserveFirst bool, cfg EpochConfig) []individual {
	if len(population) == 0 || knownFingerprints == nil {
		return population
	}
	out := make([]individual, len(population))
	copy(out, population)
	for i := range out {
		if preserveFirst && i == 0 {
			knownFingerprints[e.evolvable.Fingerprint(out[i].Gene)] = true
			continue
		}
		out[i].Gene = e.uniqueGene(out[i].Gene, knownFingerprints, rng, func() Gene {
			return e.normalizeGene(cfg, e.evolvable.Sample(rng))
		})
		out[i].Gene = e.normalizeGene(cfg, out[i].Gene)
		knownFingerprints[e.evolvable.Fingerprint(out[i].Gene)] = true
	}
	return out
}

func (e *EvolutionEngine) uniqueGene(g Gene, knownFingerprints map[uint64]bool, rng RandomSource, create func() Gene) Gene {
	if knownFingerprints == nil {
		return g
	}
	fingerprint := e.evolvable.Fingerprint(g)
	if !knownFingerprints[fingerprint] {
		return g
	}
	for i := 0; i < 25; i++ {
		candidate := create()
		candidateFingerprint := e.evolvable.Fingerprint(candidate)
		if !knownFingerprints[candidateFingerprint] {
			return candidate
		}
	}
	return e.evolvable.Mutate(g, 0.55, 3.0, rng)
}

func (e *EvolutionEngine) nextGeneration(population []individual, mutProb float64, mutScale float64, rng RandomSource, cfg EpochConfig, generation int, knownFingerprints map[uint64]bool) []individual {
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
		child = e.normalizeGene(cfg, child)
		child = e.uniqueGene(child, knownFingerprints, rng, func() Gene {
			next := e.evolvable.Crossover(p1.Gene, p2.Gene, rng)
			return e.normalizeGene(cfg, e.evolvable.Mutate(next, mutProb, mutScale, rng))
		})
		child = e.normalizeGene(cfg, child)
		e.trace(cfg, TraceModeDetailed, "evolution", "offspring.created", "offspring generated", map[string]any{
			"generation":           generation,
			"child":                len(next),
			"parent_a_score":       p1.Fitness.ScoreTotal,
			"parent_b_score":       p2.Fitness.ScoreTotal,
			"mutation_probability": mutProb,
			"mutation_scale":       mutScale,
		})
		if knownFingerprints != nil {
			knownFingerprints[e.evolvable.Fingerprint(child)] = true
		}
		next = append(next, individual{Gene: child})
	}
	e.trace(cfg, TraceModeSummary, "evolution", "generation.spawned", "next generation spawned", map[string]any{
		"generation":  generation,
		"population":  len(next),
		"elite_count": eliteCount,
	})
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

func (e *EvolutionEngine) trace(cfg EpochConfig, required TraceMode, source string, scope string, message string, fields map[string]any) {
	if cfg.OnTrace == nil || !TraceEnabled(activeTraceMode(cfg.TraceMode, cfg.TraceModeFunc), required) {
		return
	}
	cfg.OnTrace(TraceEvent{
		RequiredMode: required,
		Level:        "trace",
		Source:       source,
		Scope:        scope,
		Message:      message,
		Fields:       fields,
	})
}

func activeTraceMode(fallback TraceMode, fn func() TraceMode) TraceMode {
	if fn != nil {
		return NormalizeTraceMode(fn())
	}
	return NormalizeTraceMode(fallback)
}
