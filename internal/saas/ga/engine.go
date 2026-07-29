package ga

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/quant"
)

type GenomeStore interface {
	LoadEliteGenes(ctx context.Context, scope GeneScope, limit int) ([][]byte, error)
	SaveChallenger(ctx context.Context, scope GeneScope, paramPack []byte, result FitnessResult, searchConfig []byte) (uint, error)
	LoadKLines(ctx context.Context, scope DatasetScope) ([]quant.Bar, error)
}

type CandidateReservationStore interface {
	ReserveCandidates(ctx context.Context, scope GeneScope, taskID uint, generation int, candidates []CandidateReservation) ([]CandidateReservationOutcome, error)
	CompleteCandidateEvaluations(ctx context.Context, evaluations []CandidateEvaluation) error
	RecordGridCoverage(ctx context.Context, scope GeneScope, taskID uint, generation int, axes []ParameterAxis, candidates []CandidateReservation) error
}

type GeneScope struct {
	StrategyID    string
	InstrumentID  string
	DataSource    string
	Interval      string
	ExecutionMode string
	SearchHash    string
}

type DatasetScope struct {
	InstrumentID string
	DataSource   string
	Symbol       string
	Interval     string
	StartTimeMs  int64
	EndTimeMs    int64
}

type MarketScope struct {
	InstrumentID string `json:"instrument_id"`
	DataSource   string `json:"data_source"`
	Pair         string `json:"pair"`
	Interval     string `json:"interval"`
	StartTimeMs  int64  `json:"start_time_ms"`
	EndTimeMs    int64  `json:"end_time_ms"`
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
	TaskID              uint
	Pair                string
	InstrumentID        string
	DataSource          string
	ExecutionMode       string
	StartTimeMs         int64
	EndTimeMs           int64
	Interval            string
	PopSize             int
	MaxGenerations      int
	SpawnMode           string
	LotStepSize         float64
	LotMinQty           float64
	InitialCapital      float64
	MonthlyDCA          float64
	GeneOptions         GeneOptions
	Costs               quant.ExecutionCostConfig
	TradePenalty        float64
	LongTermFilter      backtestcore.LongTermFilterConfig
	OnProgress          func(EpochProgress)
	OnTrace             func(TraceEvent)
	OnComputePlan       func(ComputePlan)
	OnComputeStep       func(int64)
	OnSearchPrepared    func(SearchIdentity, string)
	TraceMode           TraceMode
	TraceModeFunc       func() TraceMode
	SpawnPointOverride  *quant.SpawnPoint
	SeedGeneID          uint
	SeedParamPack       []byte
	RandomPopulation    bool
	MultiMarkets        []MarketScope
	GridCoverageEnabled bool
}

type EpochProgress struct {
	Generation          int
	BestFitness         float64
	BestMaxDrawdown     float64
	BestWindows         []quant.CrucibleResult
	BestMarkets         []MarketPerformance
	BestParamPack       []byte
	MutationProbability float64
	MutationScale       float64
	EvaluatedCount      int64
	ValidCount          int64
	SkippedCount        int64
	FailedCount         int64
}

type EpochResult struct {
	GeneRecordID   uint
	BestGene       Gene
	Fitness        FitnessResult
	ParamPack      []byte
	EvaluatedCount int64
	ValidCount     int64
	SkippedCount   int64
	FailedCount    int64
}

type individual struct {
	Gene          Gene
	Fitness       FitnessResult
	Fingerprint   uint64
	ReservationID uint
	Evaluated     bool
	Failed        bool
	ErrorMessage  string
}

type epochCounters struct {
	evaluated atomic.Int64
	valid     atomic.Int64
	skipped   atomic.Int64
	failed    atomic.Int64
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
		"pair":                     cfg.Pair,
		"instrument_id":            cfg.InstrumentID,
		"data_source":              cfg.DataSource,
		"interval":                 cfg.Interval,
		"execution_mode":           cfg.ExecutionMode,
		"population":               e.popSize(cfg),
		"max_generations":          e.maxGenerations(cfg),
		"initial_capital":          e.initialCapital(cfg),
		"monthly_dca":              e.monthlyDCA(cfg),
		"gene_options":             cfg.GeneOptions,
		"fee_rate":                 cfg.Costs.FeeRate,
		"spread_rate":              cfg.Costs.SpreadRate,
		"trade_penalty":            cfg.TradePenalty,
		"long_term_filter_enabled": cfg.LongTermFilter.Enabled,
		"long_term_filter_months":  cfg.LongTermFilter.Months,
		"long_term_filter_version": cfg.LongTermFilter.Version,
		"trace_mode":               cfg.TraceMode,
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
	plan.ComputeStep = cfg.OnComputeStep
	if cfg.OnComputePlan != nil {
		unitsPerIndividual := planComputeUnits(plan)
		cfg.OnComputePlan(ComputePlan{
			UnitsPerIndividual: unitsPerIndividual,
			PlannedUnits:       unitsPerIndividual * int64(e.popSize(cfg)) * int64(e.maxGenerations(cfg)+1),
		})
	}
	identity := BuildSearchIdentity(e.evolvable.StrategyID(), plan)
	searchHash := identity.Hash()
	searchConfig := e.searchConfig(cfg, identity)
	if cfg.OnSearchPrepared != nil {
		cfg.OnSearchPrepared(identity, searchHash)
	}
	scope := GeneScope{
		StrategyID:    e.evolvable.StrategyID(),
		InstrumentID:  cfg.InstrumentID,
		DataSource:    cfg.DataSource,
		Interval:      cfg.Interval,
		ExecutionMode: cfg.ExecutionMode,
		SearchHash:    searchHash,
	}
	if len(plan.MultiMarkets) > 0 {
		scope.InstrumentID = "multi-market"
		scope.DataSource = "multiple"
	}
	e.trace(cfg, TraceModeSummary, "evolution", "search.identity", "resolved search identity", map[string]any{
		"schema_version": CoreCandidateSchemaVersion,
		"search_hash":    searchHash,
		"datasets":       identity.Datasets,
	})
	// P14 retired the historical observation table. De-duplicate only within
	// this run; durable research point identity now belongs to M.
	knownFingerprints := make(map[uint64]bool)
	counters := &epochCounters{}

	population, err := e.initializePopulation(ctx, cfg, rng, scope)
	if err != nil {
		e.trace(cfg, TraceModeSummary, "evolution", "epoch.failed", "failed to initialize population", map[string]any{
			"error": err.Error(),
		})
		return EpochResult{}, err
	}
	population = e.deduplicatePopulation(population, knownFingerprints, rng, true, cfg)
	population, err = e.preparePopulation(ctx, population, plan, scope, 0, cfg, knownFingerprints, counters, true, rng)
	if err != nil {
		return EpochResult{}, err
	}
	population, err = e.evaluatePopulation(ctx, population, plan, 0, cfg, counters)
	if err != nil {
		return EpochResult{}, err
	}

	best, ok := bestValidIndividual(population)
	if !ok {
		return EpochResult{}, errors.New("初始族群沒有完成評估的有效候選")
	}
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
				BestMarkets:         best.Fitness.Markets,
				BestParamPack:       paramPack,
				MutationProbability: mutProb,
				MutationScale:       mutScale,
				EvaluatedCount:      counters.evaluated.Load(),
				ValidCount:          counters.valid.Load(),
				SkippedCount:        counters.skipped.Load(),
				FailedCount:         counters.failed.Load(),
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
		population, err = e.preparePopulation(ctx, population, plan, scope, generation+1, cfg, knownFingerprints, counters, false, rng)
		if err != nil {
			return EpochResult{}, err
		}
		population, err = e.evaluatePopulation(ctx, population, plan, generation+1, cfg, counters)
		if err != nil {
			return EpochResult{}, err
		}
	}

	sortPopulation(population)
	if current, found := bestValidIndividual(population); found && current.Fitness.ScoreTotal > best.Fitness.ScoreTotal {
		best = current
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
		GeneRecordID:   id,
		BestGene:       best.Gene,
		Fitness:        best.Fitness,
		ParamPack:      paramPack,
		EvaluatedCount: counters.evaluated.Load(),
		ValidCount:     counters.valid.Load(),
		SkippedCount:   counters.skipped.Load(),
		FailedCount:    counters.failed.Load(),
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
	plan.ComputeStep = cfg.OnComputeStep
	gene := e.normalizeGene(cfg, e.evolvable.DecodeElite(paramPack))
	return e.evolvable.Evaluate(ctx, gene, plan)
}

func (e *EvolutionEngine) EstimateComputePlan(ctx context.Context, cfg EpochConfig) (ComputePlan, error) {
	plan, err := e.buildEvaluablePlan(ctx, cfg)
	if err != nil {
		return ComputePlan{}, err
	}
	unitsPerIndividual := planComputeUnits(plan)
	return ComputePlan{
		UnitsPerIndividual: unitsPerIndividual,
		PlannedUnits:       unitsPerIndividual * int64(e.popSize(cfg)) * int64(e.maxGenerations(cfg)+1),
	}, nil
}

func (e *EvolutionEngine) searchConfig(cfg EpochConfig, identity SearchIdentity) []byte {
	cfg.GeneOptions = NormalizeGeneOptions(cfg.GeneOptions)
	spawnMode := cfg.SpawnMode
	if spawnMode == "" {
		spawnMode = "inherit"
	}
	raw, err := json.Marshal(map[string]any{
		"candidate_schema_version": CoreCandidateSchemaVersion,
		"search_hash":              identity.Hash(),
		"search_identity":          identity,
		"strategy_id":              e.evolvable.StrategyID(),
		"symbol":                   cfg.Pair,
		"instrument_id":            cfg.InstrumentID,
		"data_source":              cfg.DataSource,
		"interval":                 cfg.Interval,
		"execution_mode":           cfg.ExecutionMode,
		"train_start_ms":           cfg.StartTimeMs,
		"train_end_ms":             cfg.EndTimeMs,
		"initial_capital":          e.initialCapital(cfg),
		"monthly_dca":              e.monthlyDCA(cfg),
		"gene_options":             cfg.GeneOptions,
		"fee_rate":                 quant.NormalizeExecutionCosts(cfg.Costs).FeeRate,
		"spread_rate":              quant.NormalizeExecutionCosts(cfg.Costs).SpreadRate,
		"trade_penalty":            cfg.TradePenalty,
		"spawn_mode":               spawnMode,
		"population":               e.popSize(cfg),
		"generations":              e.maxGenerations(cfg),
		"seed_gene_id":             cfg.SeedGeneID,
		"fixed_param_keys":         cfg.GeneOptions.FixedParamKeys,
		"long_term_filter_enabled": cfg.LongTermFilter.Enabled,
		"long_term_filter_months":  cfg.LongTermFilter.Months,
		"long_term_filter_version": cfg.LongTermFilter.Version,
	})
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}

func (e *EvolutionEngine) buildEvaluablePlan(ctx context.Context, cfg EpochConfig) (EvaluablePlan, error) {
	if len(cfg.MultiMarkets) > 0 {
		return e.buildMultiMarketEvaluablePlan(ctx, cfg)
	}
	return e.buildSingleMarketEvaluablePlan(ctx, cfg)
}

func (e *EvolutionEngine) buildSingleMarketEvaluablePlan(ctx context.Context, cfg EpochConfig) (EvaluablePlan, error) {
	cfg.GeneOptions = NormalizeGeneOptions(cfg.GeneOptions)
	e.trace(cfg, TraceModeSummary, "market_data", "klines.load", "loading historical bars", map[string]any{
		"pair":          cfg.Pair,
		"instrument_id": cfg.InstrumentID,
		"data_source":   cfg.DataSource,
		"interval":      cfg.Interval,
		"start_time_ms": cfg.StartTimeMs,
		"end_time_ms":   cfg.EndTimeMs,
	})
	datasetScope := DatasetScope{
		InstrumentID: cfg.InstrumentID,
		DataSource:   cfg.DataSource,
		Symbol:       cfg.Pair,
		Interval:     cfg.Interval,
		StartTimeMs:  cfg.StartTimeMs,
		EndTimeMs:    cfg.EndTimeMs,
	}
	bars, err := e.store.LoadKLines(ctx, datasetScope)
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
		LongTermFilter: cfg.LongTermFilter,
		GeneOptions:    cfg.GeneOptions,
		LotStep:        cfg.LotStepSize,
		LotMin:         cfg.LotMinQty,
		Windows:        windows,
		DCABaselines:   baselines,
		Datasets:       []DatasetIdentity{BuildDatasetIdentity(datasetScope, bars)},
		AggregateCache: map[string]any{},
	}, nil
}

func (e *EvolutionEngine) buildMultiMarketEvaluablePlan(ctx context.Context, cfg EpochConfig) (EvaluablePlan, error) {
	if len(cfg.MultiMarkets) < 2 {
		return EvaluablePlan{}, fmt.Errorf("多行情搜尋至少要選擇兩張行情")
	}
	var root EvaluablePlan
	for index, market := range cfg.MultiMarkets {
		marketCfg := cfg
		marketCfg.MultiMarkets = nil
		marketCfg.Pair = market.Pair
		marketCfg.InstrumentID = market.InstrumentID
		marketCfg.DataSource = market.DataSource
		marketCfg.Interval = market.Interval
		marketCfg.StartTimeMs = market.StartTimeMs
		marketCfg.EndTimeMs = market.EndTimeMs
		plan, err := e.buildSingleMarketEvaluablePlan(ctx, marketCfg)
		if err != nil {
			return EvaluablePlan{}, fmt.Errorf("讀取行情 %s 失敗：%w", market.InstrumentID, err)
		}
		var full quant.CrucibleWindow
		for _, window := range plan.Windows {
			if window.Label == "10y" {
				full = window
				break
			}
		}
		if len(full.Bars) < 2 {
			return EvaluablePlan{}, fmt.Errorf("行情 %s 無法形成完整回測", market.InstrumentID)
		}
		if index == 0 {
			root = plan
			root.Windows = nil
			root.DCABaselines = nil
			root.Datasets = nil
			root.MultiMarkets = nil
		}
		dataset := plan.Datasets[0]
		root.Datasets = append(root.Datasets, dataset)
		root.MultiMarkets = append(root.MultiMarkets, MultiMarketPlan{
			Pair:         market.Pair,
			InstrumentID: market.InstrumentID,
			DataSource:   market.DataSource,
			Interval:     market.Interval,
			Window:       full,
			Dataset:      dataset,
		})
	}
	root.Pair = ""
	return root, nil
}

func planComputeUnits(plan EvaluablePlan) int64 {
	var total int64
	for _, market := range plan.MultiMarkets {
		total += int64(len(market.Window.Bars))
	}
	if len(plan.MultiMarkets) > 0 {
		return total
	}
	for _, window := range plan.Windows {
		total += int64(len(window.Bars))
	}
	return total
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

func (e *EvolutionEngine) sampleGene(rng RandomSource, options GeneOptions) Gene {
	if aware, ok := e.evolvable.(OptionAwareGeneSpace); ok {
		return aware.SampleWithOptions(rng, options)
	}
	return e.evolvable.Sample(rng)
}

func (e *EvolutionEngine) mutateGene(gene Gene, probability float64, scale float64, rng RandomSource, options GeneOptions) Gene {
	if aware, ok := e.evolvable.(OptionAwareGeneSpace); ok {
		return aware.MutateWithOptions(gene, probability, scale, rng, options)
	}
	return e.evolvable.Mutate(gene, probability, scale, rng)
}

func (e *EvolutionEngine) crossoverGene(first Gene, second Gene, rng RandomSource, options GeneOptions) Gene {
	if aware, ok := e.evolvable.(OptionAwareGeneSpace); ok {
		return aware.CrossoverWithOptions(first, second, rng, options)
	}
	return e.evolvable.Crossover(first, second, rng)
}

func (e *EvolutionEngine) fingerprintGene(gene Gene, options GeneOptions) uint64 {
	if aware, ok := e.evolvable.(OptionAwareGeneSpace); ok {
		return aware.FingerprintWithOptions(gene, options)
	}
	return e.evolvable.Fingerprint(gene)
}

func (e *EvolutionEngine) initializePopulation(ctx context.Context, cfg EpochConfig, rng RandomSource, scope GeneScope) ([]individual, error) {
	popSize := e.popSize(cfg)
	elitesRaw := [][]byte{}
	if len(cfg.SeedParamPack) > 0 {
		elitesRaw = append(elitesRaw, cfg.SeedParamPack)
	}
	if !cfg.RandomPopulation {
		loaded, err := e.store.LoadEliteGenes(ctx, scope, popSize)
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
			population = append(population, individual{Gene: e.normalizeGene(cfg, e.mutateGene(base, 0.15, 1.5, rng, cfg.GeneOptions))})
		}
	} else if !cfg.RandomPopulation {
		population = append(population, individual{Gene: e.normalizeGene(cfg, quant.DefaultSeedChromosome)})
	}

	for len(population) < popSize {
		population = append(population, individual{Gene: e.normalizeGene(cfg, e.sampleGene(rng, cfg.GeneOptions))})
	}
	e.trace(cfg, TraceModeSummary, "evolution", "population.initialized", "initial population initialized", map[string]any{
		"population":  len(population),
		"elite_count": len(elitesRaw),
	})
	return population, nil
}

func (e *EvolutionEngine) preparePopulation(
	ctx context.Context,
	population []individual,
	plan EvaluablePlan,
	scope GeneScope,
	generation int,
	cfg EpochConfig,
	knownFingerprints map[uint64]bool,
	counters *epochCounters,
	preserveFirst bool,
	rng RandomSource,
) ([]individual, error) {
	store, durable := e.store.(CandidateReservationStore)
	for index := range population {
		if population[index].Evaluated {
			continue
		}
		population[index].Fingerprint = e.fingerprintGene(population[index].Gene, cfg.GeneOptions)
	}
	if !durable {
		return population, nil
	}

	newlyReserved := make([]CandidateReservation, 0, len(population))
	pending := make([]int, 0, len(population))
	for index := range population {
		if !population[index].Evaluated {
			pending = append(pending, index)
		}
	}
	for round := 0; len(pending) > 0 && round < 128; round++ {
		reservations := make([]CandidateReservation, 0, len(pending))
		for _, index := range pending {
			paramPack, err := e.evolvable.EncodeResult(population[index].Gene, plan.Spawn, cfg.GeneOptions)
			if err != nil {
				return nil, fmt.Errorf("候選 %d 無法編碼：%w", index, err)
			}
			reservations = append(reservations, CandidateReservation{
				Fingerprint: population[index].Fingerprint,
				Identity:    canonicalCandidateIdentity(paramPack),
				Individual:  index,
				ParamPack:   paramPack,
			})
		}
		outcomes, err := store.ReserveCandidates(ctx, scope, cfg.TaskID, generation, reservations)
		if err != nil {
			return nil, err
		}
		byIdentity := make(map[string]CandidateReservationOutcome, len(outcomes))
		for _, outcome := range outcomes {
			byIdentity[outcome.Identity] = outcome
		}
		nextPending := make([]int, 0)
		for reservationIndex, index := range pending {
			item := &population[index]
			identity := reservations[reservationIndex].Identity
			outcome, ok := byIdentity[identity]
			if !ok {
				return nil, fmt.Errorf("候選 %016x 沒有取得預約結果", item.Fingerprint)
			}
			switch {
			case outcome.Reserved:
				item.ReservationID = outcome.ReservationID
				for _, reservation := range reservations {
					if reservation.Identity == identity {
						newlyReserved = append(newlyReserved, reservation)
						break
					}
				}
			case outcome.Completed && preserveFirst && index == 0:
				item.Fitness = outcome.Fitness
				item.Evaluated = true
				counters.skipped.Add(1)
			default:
				if outcome.Completed {
					counters.skipped.Add(1)
				}
				replacement, fingerprint, found := e.newGlobalUniqueGene(rng, cfg, knownFingerprints)
				if !found {
					return nil, errors.New("合法候選空間已全部評估，無法再建立新候選")
				}
				item.Gene = replacement
				item.Fingerprint = fingerprint
				item.ReservationID = 0
				item.Evaluated = false
				item.Failed = false
				item.ErrorMessage = ""
				nextPending = append(nextPending, index)
			}
		}
		pending = nextPending
	}
	if len(pending) > 0 {
		return nil, errors.New("無法在未評估區域取得原子候選預約")
	}
	if cfg.GridCoverageEnabled && len(newlyReserved) > 0 {
		if err := store.RecordGridCoverage(ctx, scope, cfg.TaskID, generation, ParameterAxes(cfg.GeneOptions), newlyReserved); err != nil {
			return nil, err
		}
	}
	return population, nil
}

func canonicalCandidateIdentity(paramPack []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(paramPack))
}

func (e *EvolutionEngine) newGlobalUniqueGene(rng RandomSource, cfg EpochConfig, knownFingerprints map[uint64]bool) (Gene, uint64, bool) {
	for attempt := 0; attempt < 4096; attempt++ {
		gene := e.normalizeGene(cfg, e.sampleGene(rng, cfg.GeneOptions))
		fingerprint := e.fingerprintGene(gene, cfg.GeneOptions)
		if knownFingerprints[fingerprint] {
			continue
		}
		knownFingerprints[fingerprint] = true
		return gene, fingerprint, true
	}
	return nil, 0, false
}

func (e *EvolutionEngine) evaluatePopulation(ctx context.Context, population []individual, plan EvaluablePlan, generation int, cfg EpochConfig, counters *epochCounters) ([]individual, error) {
	workers := runtime.NumCPU()
	pendingCount := 0
	for index := range population {
		if !population[index].Evaluated && !population[index].Failed {
			pendingCount++
		}
	}
	if pendingCount == 0 {
		return population, nil
	}
	if workers > pendingCount {
		workers = pendingCount
	}
	if workers < 1 {
		workers = 1
	}
	e.trace(cfg, TraceModeSummary, "evolution", "population.evaluate", "population evaluation started", map[string]any{
		"generation": generation,
		"population": pendingCount,
		"workers":    workers,
	})

	type task struct {
		index int
		gene  Gene
	}
	tasks := make(chan task, len(population))
	var wg sync.WaitGroup
	var evaluationsMu sync.Mutex
	evaluations := make([]CandidateEvaluation, 0, pendingCount)

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range tasks {
				fingerprint := population[task.index].Fingerprint
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
				fitness, err := e.evaluateGeneSafely(ctx, task.gene, evalPlan)
				if err != nil {
					population[task.index].Failed = true
					population[task.index].ErrorMessage = err.Error()
					counters.failed.Add(1)
				} else {
					population[task.index].Fitness = fitness
					population[task.index].Evaluated = true
					counters.evaluated.Add(1)
					if !fitness.Fatal {
						counters.valid.Add(1)
					}
				}
				evaluationsMu.Lock()
				evaluations = append(evaluations, CandidateEvaluation{
					ReservationID: population[task.index].ReservationID,
					Fingerprint:   fingerprint,
					Fitness:       fitness,
					Error:         population[task.index].ErrorMessage,
				})
				evaluationsMu.Unlock()
				e.trace(cfg, TraceModeDetailed, "evolution", "individual.evaluate.done", "individual evaluation completed", map[string]any{
					"generation":   generation,
					"individual":   task.index,
					"worker":       workerID,
					"fingerprint":  fingerprint,
					"score":        fitness.ScoreTotal,
					"max_drawdown": fitness.MaxDrawdown,
					"fatal":        fitness.Fatal,
					"error":        population[task.index].ErrorMessage,
				})
			}
		}(worker)
	}

	for i, item := range population {
		if item.Evaluated || item.Failed {
			continue
		}
		tasks <- task{index: i, gene: item.Gene}
	}
	close(tasks)
	wg.Wait()
	if durable, ok := e.store.(CandidateReservationStore); ok {
		if err := durable.CompleteCandidateEvaluations(context.WithoutCancel(ctx), evaluations); err != nil {
			return nil, err
		}
	}
	e.trace(cfg, TraceModeSummary, "evolution", "population.evaluated", "population evaluation completed", map[string]any{
		"generation": generation,
		"population": pendingCount,
	})
	if err := ctx.Err(); err != nil {
		return population, err
	}
	return population, nil
}

func (e *EvolutionEngine) evaluateGeneSafely(ctx context.Context, gene Gene, plan EvaluablePlan) (fitness FitnessResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("candidate evaluation panic: %v", recovered)
		}
	}()
	return e.evolvable.Evaluate(ctx, gene, plan)
}

func (e *EvolutionEngine) deduplicatePopulation(population []individual, knownFingerprints map[uint64]bool, rng RandomSource, preserveFirst bool, cfg EpochConfig) []individual {
	if len(population) == 0 || knownFingerprints == nil {
		return population
	}
	out := make([]individual, len(population))
	copy(out, population)
	for i := range out {
		if preserveFirst && i == 0 {
			knownFingerprints[e.fingerprintGene(out[i].Gene, cfg.GeneOptions)] = true
			continue
		}
		out[i].Gene = e.uniqueGene(out[i].Gene, knownFingerprints, rng, func() Gene {
			return e.normalizeGene(cfg, e.sampleGene(rng, cfg.GeneOptions))
		}, cfg.GeneOptions)
		out[i].Gene = e.normalizeGene(cfg, out[i].Gene)
		knownFingerprints[e.fingerprintGene(out[i].Gene, cfg.GeneOptions)] = true
	}
	return out
}

func (e *EvolutionEngine) uniqueGene(g Gene, knownFingerprints map[uint64]bool, rng RandomSource, create func() Gene, options GeneOptions) Gene {
	if knownFingerprints == nil {
		return g
	}
	fingerprint := e.fingerprintGene(g, options)
	if !knownFingerprints[fingerprint] {
		return g
	}
	for i := 0; i < 25; i++ {
		candidate := create()
		candidateFingerprint := e.fingerprintGene(candidate, options)
		if !knownFingerprints[candidateFingerprint] {
			return candidate
		}
	}
	return e.mutateGene(g, 0.55, 3.0, rng, options)
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
		child := e.crossoverGene(p1.Gene, p2.Gene, rng, cfg.GeneOptions)
		child = e.mutateGene(child, mutProb, mutScale, rng, cfg.GeneOptions)
		child = e.normalizeGene(cfg, child)
		child = e.uniqueGene(child, knownFingerprints, rng, func() Gene {
			next := e.crossoverGene(p1.Gene, p2.Gene, rng, cfg.GeneOptions)
			return e.normalizeGene(cfg, e.mutateGene(next, mutProb, mutScale, rng, cfg.GeneOptions))
		}, cfg.GeneOptions)
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
			knownFingerprints[e.fingerprintGene(child, cfg.GeneOptions)] = true
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
		if betterIndividual(population[idx], best) {
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
		return betterIndividual(population[i], population[j])
	})
}

func bestIndividual(population []individual) individual {
	sortPopulation(population)
	if len(population) == 0 {
		return individual{Fitness: FitnessResult{ScoreTotal: FatalFitnessScore, Fatal: true}}
	}
	return population[0]
}

func bestValidIndividual(population []individual) (individual, bool) {
	sortPopulation(population)
	for _, candidate := range population {
		if candidate.Evaluated && !candidate.Failed && !candidate.Fitness.Fatal {
			return candidate, true
		}
	}
	return individual{}, false
}

func betterIndividual(first, second individual) bool {
	firstRank := individualRank(first)
	secondRank := individualRank(second)
	if firstRank != secondRank {
		return firstRank > secondRank
	}
	return first.Fitness.ScoreTotal > second.Fitness.ScoreTotal
}

func individualRank(candidate individual) int {
	switch {
	case candidate.Evaluated && !candidate.Failed && !candidate.Fitness.Fatal:
		return 3
	case candidate.Evaluated && !candidate.Failed:
		return 2
	case candidate.Failed:
		return 1
	default:
		return 0
	}
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
