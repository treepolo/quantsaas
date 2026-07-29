package ga

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"time"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/quant"
)

type GenomeStore interface {
	LoadEliteGenes(ctx context.Context, scope GeneScope, limit int) ([][]byte, error)
	SaveChallenger(ctx context.Context, scope GeneScope, paramPack []byte, result FitnessResult, searchConfig []byte) (uint, error)
	LoadKLines(ctx context.Context, scope DatasetScope) ([]quant.Bar, error)
}

// CandidateReservationStore is optional so lightweight test stores do not
// need database support. The production store persists the reservation index
// across separate tasks with the same search configuration.
type CandidateReservationStore interface {
	LoadReservedFingerprints(ctx context.Context, scope GeneScope, searchConfig []byte) (map[uint64]bool, error)
	ReserveCandidates(ctx context.Context, scope GeneScope, searchConfig []byte, taskID uint, generation int, candidates []CandidateReservation) error
}

type CandidateResultStore interface {
	UpdateCandidateResults(ctx context.Context, searchConfig []byte, results []CandidateEvaluation) error
}

type CandidateBestStore interface {
	LoadBestEvaluatedCandidate(ctx context.Context, scope GeneScope, searchConfig []byte) (EvaluatedCandidate, bool, error)
}

type CandidateReservation struct {
	Fingerprint uint64
	ParamPack   []byte
}

type CandidateEvaluation struct {
	Fingerprint uint64
	Fitness     FitnessResult
}

type EvaluatedCandidate struct {
	ParamPack []byte
	Fitness   FitnessResult
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

type MarketScope struct {
	InstrumentID string `json:"instrument_id"`
	Pair         string `json:"pair"`
	DataSource   string `json:"data_source"`
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
	TaskID         uint
	Pair           string
	InstrumentID   string
	DataSource     string
	ExecutionMode  string
	StartTimeMs    int64
	EndTimeMs      int64
	Interval       string
	PopSize        int
	MaxGenerations int
	// SearchAlgorithm selects candidate generation only.  "layered_grid" is
	// the deterministic default; "genetic" retains the legacy GA behaviour.
	SearchAlgorithm string
	// LayeredLocalPercent is the visible share of each layered batch allocated
	// to systematic expansion around the current centre.  The remainder is
	// allocated to the deterministic global frontier.
	LayeredLocalPercent int
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
	OnSearchCheckpoint  func([]byte)
	OnSearchIdentity    func(string)
	TraceMode           TraceMode
	TraceModeFunc       func() TraceMode
	SpawnPointOverride  *quant.SpawnPoint
	SeedGeneID          uint
	SeedParamPack       []byte
	SeedIsAutomatic     bool
	RandomPopulation    bool
	// UseInitialSeedOnly keeps every continuous epoch anchored to the task's
	// original seed and bypasses the shared historical elite pool.
	UseInitialSeedOnly bool
	// ReservedFingerprints belongs to one user task. Continuous runs pass the
	// same map into every epoch so a candidate is never sent to evaluation twice.
	ReservedFingerprints map[uint64]bool `json:"-"`
	SearchCheckpoint     []byte          `json:"-"`
	MultiMarketEnabled   bool            `json:"multi_market_enabled"`
	MultiMarkets         []MarketScope   `json:"multi_markets,omitempty"`
	DatasetHash          string          `json:"-"`
}

const candidateEvaluationSemanticsVersion = "sigmoid-dca-search-evaluation-v2"

const (
	SearchAlgorithmLayeredGrid = "layered_grid"
	SearchAlgorithmGenetic     = "genetic"
)

func normalizeSearchAlgorithm(value string) string {
	if value == SearchAlgorithmGenetic {
		return SearchAlgorithmGenetic
	}
	return SearchAlgorithmLayeredGrid
}

func normalizeLayeredLocalPercent(value int) int {
	if value == 0 {
		return 70
	}
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
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
	SearchAxes          []LayeredAxisStatus
	SearchStatus        *LayeredSearchStatus
}

type EpochResult struct {
	GeneRecordID uint
	BestGene     Gene
	Fitness      FitnessResult
	ParamPack    []byte
}

type individual struct {
	Gene      Gene
	Fitness   FitnessResult
	Evaluated bool
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
	cfg.SearchAlgorithm = normalizeSearchAlgorithm(cfg.SearchAlgorithm)
	cfg.LayeredLocalPercent = normalizeLayeredLocalPercent(cfg.LayeredLocalPercent)
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
		"search_algorithm":         cfg.SearchAlgorithm,
		"layered_local_percent":    cfg.LayeredLocalPercent,
	})
	plan, err := e.buildEvaluablePlan(ctx, cfg)
	if err != nil {
		e.trace(cfg, TraceModeSummary, "evolution", "epoch.failed", "failed to build evaluable plan", map[string]any{
			"error": err.Error(),
		})
		return EpochResult{}, err
	}
	// The upper window bound is derived from the actual evaluation data, not a
	// fixed menu. It becomes part of every candidate created in this run.
	cfg.GeneOptions = plan.GeneOptions
	plan.Trace = cfg.OnTrace
	plan.TraceMode = cfg.TraceMode
	plan.TraceModeFunc = cfg.TraceModeFunc
	plan.ComputeStep = cfg.OnComputeStep
	cfg.DatasetHash = evaluablePlanDatasetHash(plan)
	searchConfig := e.searchConfig(cfg)
	reservationConfig := e.candidateReservationConfig(cfg)
	if cfg.OnSearchIdentity != nil {
		cfg.OnSearchIdentity(candidateSearchHash(reservationConfig))
	}
	scope := GeneScope{
		StrategyID:    e.evolvable.StrategyID(),
		InstrumentID:  cfg.InstrumentID,
		DataSource:    cfg.DataSource,
		Interval:      cfg.Interval,
		ExecutionMode: cfg.ExecutionMode,
	}
	reservedFingerprints := cfg.ReservedFingerprints
	if reservedFingerprints == nil {
		reservedFingerprints = make(map[uint64]bool)
	}
	if reservations, ok := e.store.(CandidateReservationStore); ok {
		persisted, loadErr := reservations.LoadReservedFingerprints(ctx, scope, reservationConfig)
		if loadErr != nil {
			return EpochResult{}, loadErr
		}
		for fingerprint := range persisted {
			reservedFingerprints[fingerprint] = true
		}
	}
	var inheritedBest *individual
	if bestStore, ok := e.store.(CandidateBestStore); ok {
		candidate, found, loadErr := bestStore.LoadBestEvaluatedCandidate(ctx, scope, reservationConfig)
		if loadErr != nil {
			return EpochResult{}, loadErr
		}
		if found {
			gene := e.normalizeGene(cfg, e.evolvable.DecodeElite(candidate.ParamPack))
			inheritedBest = &individual{Gene: gene, Fitness: candidate.Fitness, Evaluated: true}
			if cfg.SearchAlgorithm == SearchAlgorithmLayeredGrid && (cfg.SeedGeneID == 0 || cfg.SeedIsAutomatic) && !cfg.UseInitialSeedOnly && !cfg.RandomPopulation {
				cfg.SeedParamPack = append([]byte(nil), candidate.ParamPack...)
			}
		}
	}

	var scheduler *layeredGridScheduler
	var population []individual
	if cfg.SearchAlgorithm == SearchAlgorithmLayeredGrid {
		population, scheduler, err = e.initializeLayeredPopulation(ctx, cfg, rng)
	} else {
		population, err = e.initializePopulation(ctx, cfg, rng)
	}
	if err != nil {
		e.trace(cfg, TraceModeSummary, "evolution", "epoch.failed", "failed to initialize population", map[string]any{
			"error": err.Error(),
		})
		return EpochResult{}, err
	}
	if scheduler != nil {
		population, err = e.deduplicateLayeredPopulation(population, reservedFingerprints, scheduler, cfg, plan)
	} else {
		population, err = e.deduplicatePopulation(population, reservedFingerprints, rng, cfg)
		if err == nil {
			err = materializePopulationMarketRegionPacks(population, plan)
		}
	}
	if err != nil {
		return EpochResult{}, err
	}
	if cfg.OnComputePlan != nil {
		var units int64
		for _, item := range population {
			units += candidateComputeUnits(plan, item.Gene)
		}
		unitsPerIndividual := units / int64(max(1, len(population)))
		cfg.OnComputePlan(ComputePlan{
			UnitsPerIndividual: unitsPerIndividual,
			PlannedUnits:       plan.InitializationUnits + unitsPerIndividual*int64(e.popSize(cfg))*int64(e.maxGenerations(cfg)+1),
		})
	}
	population, err = e.evaluatePopulation(ctx, population, plan, 0, cfg, scope, reservationConfig)
	if err != nil {
		return EpochResult{}, err
	}
	e.persistLayeredCheckpoint(cfg, scheduler)

	best := bestIndividual(population)
	if inheritedBest != nil && inheritedBest.Fitness.ScoreTotal > best.Fitness.ScoreTotal {
		best = *inheritedBest
	}
	if math.IsInf(best.Fitness.ScoreTotal, -1) {
		return EpochResult{}, fmt.Errorf("所有候選參數在至少一張行情的現金流調整後報酬皆不大於 -100%%，沒有可用候選參數")
	}
	bestScore := best.Fitness.ScoreTotal
	if scheduler != nil {
		scheduler.ObserveMaterializedStatePackages(best.Gene)
		scheduler.Recenter(best.Gene)
	}
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
		improvement := currentBest.Fitness.ScoreTotal - bestScore
		if improvement <= 0 {
			patience++
		} else {
			best = currentBest
			bestScore = currentBest.Fitness.ScoreTotal
			if scheduler != nil {
				scheduler.ObserveMaterializedStatePackages(best.Gene)
				scheduler.Recenter(best.Gene)
			}
			if improvement >= e.EarlyStopMinDelta {
				patience = 0
			} else {
				patience++
			}
		}
		e.persistLayeredCheckpoint(cfg, scheduler)

		if scheduler == nil && patience >= e.EarlyStopPatience {
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
				SearchAxes:          layeredAxisStatuses(scheduler),
				SearchStatus:        layeredSearchStatus(scheduler),
			})
		}
		e.trace(cfg, TraceModeSummary, "evolution", "generation.completed", "generation completed", map[string]any{
			"generation":           generation + 1,
			"best_score":           bestScore,
			"max_drawdown":         best.Fitness.MaxDrawdown,
			"mutation_probability": mutProb,
			"mutation_scale":       mutScale,
		})

		if scheduler != nil {
			population, err = e.nextLayeredPopulation(e.popSize(cfg), reservedFingerprints, scheduler, cfg, plan)
		} else {
			population, err = e.nextGeneration(population, mutProb, mutScale, rng, cfg, generation+1, reservedFingerprints)
		}
		if err != nil {
			return EpochResult{}, err
		}
		if scheduler == nil {
			if err := materializePopulationMarketRegionPacks(population, plan); err != nil {
				return EpochResult{}, err
			}
		}
		population, err = e.evaluatePopulation(ctx, population, plan, generation+1, cfg, scope, reservationConfig)
		if err != nil {
			return EpochResult{}, err
		}
		e.persistLayeredCheckpoint(cfg, scheduler)
	}

	sortPopulation(population)
	if population[0].Fitness.ScoreTotal > best.Fitness.ScoreTotal {
		best = population[0]
		if scheduler != nil {
			scheduler.ObserveMaterializedStatePackages(best.Gene)
			scheduler.Recenter(best.Gene)
			e.persistLayeredCheckpoint(cfg, scheduler)
		}
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

func materializePopulationMarketRegionPacks(population []individual, plan EvaluablePlan) error {
	barSets := make([][]quant.Bar, 0, len(plan.Windows)+len(plan.MultiMarkets))
	for _, window := range plan.Windows {
		barSets = append(barSets, window.Bars)
	}
	for _, market := range plan.MultiMarkets {
		barSets = append(barSets, market.Window.Bars)
	}
	for index := range population {
		region, ok := isMarketRegionGene(population[index].Gene)
		if !ok {
			continue
		}
		materialized, err := materializeMarketRegionPacks(region, barSets, plan.MarketRegionCache)
		if err != nil {
			return err
		}
		population[index].Gene = materialized
	}
	return nil
}

func (e *EvolutionEngine) EvaluateParamPack(ctx context.Context, cfg EpochConfig, paramPack []byte) (FitnessResult, error) {
	cfg.TraceMode = NormalizeTraceMode(cfg.TraceMode)
	cfg.GeneOptions = NormalizeGeneOptions(cfg.GeneOptions)
	plan, err := e.buildEvaluablePlan(ctx, cfg)
	if err != nil {
		return FitnessResult{}, err
	}
	cfg.GeneOptions = plan.GeneOptions
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

func (e *EvolutionEngine) searchConfig(cfg EpochConfig) []byte {
	cfg.GeneOptions = NormalizeGeneOptions(cfg.GeneOptions)
	spawnMode := cfg.SpawnMode
	if spawnMode == "" {
		spawnMode = "inherit"
	}
	raw, err := json.Marshal(map[string]any{
		"evaluation_version":       candidateEvaluationSemanticsVersion,
		"dataset_hash":             cfg.DatasetHash,
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
		"search_algorithm":         normalizeSearchAlgorithm(cfg.SearchAlgorithm),
		"layered_local_percent":    normalizeLayeredLocalPercent(cfg.LayeredLocalPercent),
		"seed_gene_id":             cfg.SeedGeneID,
		"fixed_param_keys":         cfg.GeneOptions.FixedParamKeys,
		"long_term_filter_enabled": cfg.LongTermFilter.Enabled,
		"long_term_filter_months":  cfg.LongTermFilter.Months,
		"long_term_filter_version": cfg.LongTermFilter.Version,
		"multi_market_enabled":     cfg.MultiMarketEnabled,
		"multi_markets":            cfg.MultiMarkets,
	})
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}

// candidateReservationConfig contains only inputs that can change the result
// of evaluating a candidate. Task-control options (population, generations,
// seed source, trace, and continuous-mode controls) deliberately do not enter
// this key, so separate runs of the same actual search share reservations.
func (e *EvolutionEngine) candidateReservationConfig(cfg EpochConfig) []byte {
	cfg.GeneOptions = NormalizeGeneOptions(cfg.GeneOptions)
	startTimeMs, endTimeMs := cfg.StartTimeMs, cfg.EndTimeMs
	if len(cfg.MultiMarkets) > 0 {
		// Multi-market evaluation uses only the explicit per-market ranges.
		// Hidden single-market form dates must not split an otherwise identical
		// reservation identity.
		startTimeMs, endTimeMs = 0, 0
	}
	raw, err := json.Marshal(struct {
		EvaluationVersion string                            `json:"evaluation_version"`
		DatasetHash       string                            `json:"dataset_hash"`
		StrategyID        string                            `json:"strategy_id"`
		Pair              string                            `json:"pair"`
		InstrumentID      string                            `json:"instrument_id"`
		DataSource        string                            `json:"data_source"`
		Interval          string                            `json:"interval"`
		ExecutionMode     string                            `json:"execution_mode"`
		StartTimeMs       int64                             `json:"start_time_ms"`
		EndTimeMs         int64                             `json:"end_time_ms"`
		LotStepSize       float64                           `json:"lot_step_size"`
		LotMinQty         float64                           `json:"lot_min_qty"`
		InitialCapital    float64                           `json:"initial_capital"`
		MonthlyDCA        float64                           `json:"monthly_dca"`
		GeneOptions       GeneOptions                       `json:"gene_options"`
		FixedGene         *quant.Chromosome                 `json:"fixed_gene,omitempty"`
		Costs             quant.ExecutionCostConfig         `json:"costs"`
		TradePenalty      float64                           `json:"trade_penalty"`
		LongTermFilter    backtestcore.LongTermFilterConfig `json:"long_term_filter"`
		SpawnPoint        *quant.SpawnPoint                 `json:"spawn_point"`
		MultiMarkets      []MarketScope                     `json:"multi_markets,omitempty"`
	}{
		EvaluationVersion: candidateEvaluationSemanticsVersion,
		DatasetHash:       cfg.DatasetHash,
		StrategyID:        e.evolvable.StrategyID(), Pair: cfg.Pair, InstrumentID: cfg.InstrumentID,
		DataSource: cfg.DataSource, Interval: cfg.Interval, ExecutionMode: cfg.ExecutionMode,
		StartTimeMs: startTimeMs, EndTimeMs: endTimeMs, LotStepSize: cfg.LotStepSize,
		LotMinQty: cfg.LotMinQty, InitialCapital: e.initialCapital(cfg), MonthlyDCA: e.monthlyDCA(cfg),
		GeneOptions: cfg.GeneOptions, FixedGene: reservationFixedGene(cfg.GeneOptions), Costs: quant.NormalizeExecutionCosts(cfg.Costs),
		TradePenalty: cfg.TradePenalty, LongTermFilter: cfg.LongTermFilter, SpawnPoint: cfg.SpawnPointOverride,
		MultiMarkets: cfg.MultiMarkets,
	})
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}

func evaluablePlanDatasetHash(plan EvaluablePlan) string {
	type marketBars struct {
		InstrumentID string      `json:"instrument_id,omitempty"`
		Pair         string      `json:"pair"`
		Bars         []quant.Bar `json:"bars"`
	}
	payload := make([]marketBars, 0, max(1, len(plan.MultiMarkets)))
	if len(plan.MultiMarkets) > 0 {
		for _, market := range plan.MultiMarkets {
			payload = append(payload, marketBars{InstrumentID: market.InstrumentID, Pair: market.Pair, Bars: market.Window.Bars})
		}
	} else if len(plan.Windows) > 0 {
		full := plan.Windows[len(plan.Windows)-1]
		payload = append(payload, marketBars{Pair: plan.Pair, Bars: full.Bars})
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:])
}

func reservationFixedGene(options GeneOptions) *quant.Chromosome {
	if len(options.FixedParamKeys) == 0 {
		return nil
	}
	return options.FixedGene
}

func (e *EvolutionEngine) buildEvaluablePlan(ctx context.Context, cfg EpochConfig) (EvaluablePlan, error) {
	if cfg.MultiMarketEnabled {
		return e.buildMultiMarketEvaluablePlan(ctx, cfg)
	}
	return e.buildSingleEvaluablePlan(ctx, cfg)
}

func (e *EvolutionEngine) buildSingleEvaluablePlan(ctx context.Context, cfg EpochConfig) (EvaluablePlan, error) {
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
	initializationUnits := int64(0)
	if cfg.GeneOptions.MarketRegionEnabled {
		maxWindow := len(bars)
		for _, window := range windows {
			if len(window.Bars) < maxWindow {
				maxWindow = len(window.Bars)
			}
		}
		// A feature window needs observations after its own lookback period.
		// Using the entire shortest evaluation period left no usable row to
		// discover threshold ranges, which silently produced window-only genes.
		maxWindow /= 2
		if maxWindow < 2 {
			return EvaluablePlan{}, fmt.Errorf("market region search requires at least two bars in every evaluation window")
		}
		cfg.GeneOptions.MarketRegionMaxWindow = maxWindow
		// Threshold values are resolved lazily from the candidate's own feature
		// window during materialisation.  Precomputing a value pool at the
		// largest window was both memory-heavy and semantically wrong for every
		// other searched window.
	}
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
		Pair:                cfg.Pair,
		Interval:            cfg.Interval,
		ExecutionMode:       cfg.ExecutionMode,
		TemplateName:        e.evolvable.StrategyID(),
		Spawn:               spawn,
		Costs:               costs,
		TradePenalty:        math.Max(0, cfg.TradePenalty),
		LongTermFilter:      cfg.LongTermFilter,
		GeneOptions:         cfg.GeneOptions,
		LotStep:             cfg.LotStepSize,
		LotMin:              cfg.LotMinQty,
		Windows:             windows,
		DCABaselines:        baselines,
		AggregateCache:      map[string]any{},
		MarketRegionCache:   NewMarketRegionFeatureCache(),
		InitializationUnits: initializationUnits,
	}, nil
}

func (e *EvolutionEngine) buildMultiMarketEvaluablePlan(ctx context.Context, cfg EpochConfig) (EvaluablePlan, error) {
	if len(cfg.MultiMarkets) < 2 {
		return EvaluablePlan{}, fmt.Errorf("多行情搜尋至少要選擇兩張行情")
	}
	var root EvaluablePlan
	minimumRegionWindow := 0
	for i, market := range cfg.MultiMarkets {
		marketCfg := cfg
		marketCfg.MultiMarketEnabled = false
		marketCfg.MultiMarkets = nil
		marketCfg.Pair, marketCfg.InstrumentID, marketCfg.DataSource = market.Pair, market.InstrumentID, market.DataSource
		marketCfg.StartTimeMs, marketCfg.EndTimeMs = market.StartTimeMs, market.EndTimeMs
		// Range preparation on every market is still required in region mode;
		// the UI monitor remains attached to the aggregate task.
		marketPlan, err := e.buildSingleEvaluablePlan(ctx, marketCfg)
		if err != nil {
			return EvaluablePlan{}, fmt.Errorf("讀取行情 %s 失敗：%w", market.InstrumentID, err)
		}
		full := marketPlan.Windows[len(marketPlan.Windows)-1]
		if i == 0 {
			root = marketPlan
			root.Windows = nil
			root.DCABaselines = nil
			root.InitializationUnits = 0
		}
		if cfg.GeneOptions.MarketRegionEnabled {
		}
		if cfg.GeneOptions.MarketRegionEnabled && (minimumRegionWindow == 0 || marketPlan.GeneOptions.MarketRegionMaxWindow < minimumRegionWindow) {
			minimumRegionWindow = marketPlan.GeneOptions.MarketRegionMaxWindow
		}
		root.InitializationUnits += marketPlan.InitializationUnits
		root.MultiMarkets = append(root.MultiMarkets, MultiMarketPlan{Pair: market.Pair, InstrumentID: market.InstrumentID, Window: full, MarketRegionCache: marketPlan.MarketRegionCache})
	}
	if minimumRegionWindow > 0 {
		// One shared gene must be valid for every selected market.
		root.GeneOptions.MarketRegionMaxWindow = minimumRegionWindow
	}
	if cfg.GeneOptions.MarketRegionEnabled {
	}
	return root, nil
}

func mergeMarketRegionFeatureRanges(destination, source map[string][2]float64) {
	for id, candidate := range source {
		if current, exists := destination[id]; exists {
			if candidate[0] < current[0] {
				current[0] = candidate[0]
			}
			if candidate[1] > current[1] {
				current[1] = candidate[1]
			}
			destination[id] = current
			continue
		}
		destination[id] = candidate
	}
}

func mergeMarketRegionFeatureValues(destination, source map[string][]float64) {
	seen := make(map[string]map[uint64]bool, len(source))
	for id, values := range destination {
		seen[id] = make(map[uint64]bool, len(values))
		for _, value := range values {
			seen[id][math.Float64bits(value)] = true
		}
	}
	for id, values := range source {
		if seen[id] == nil {
			seen[id] = map[uint64]bool{}
		}
		for _, value := range values {
			bits := math.Float64bits(value)
			if !seen[id][bits] {
				seen[id][bits] = true
				destination[id] = append(destination[id], value)
			}
		}
		sort.Float64s(destination[id])
	}
}

func planComputeUnits(plan EvaluablePlan) int64 {
	total := plan.InitializationUnits
	if len(plan.MultiMarkets) > 0 {
		for _, market := range plan.MultiMarkets {
			total += int64(len(market.Window.Bars))
			if plan.GeneOptions.MarketRegionEnabled {
				total += marketRegionMaximumProviderUnits(plan.GeneOptions.MarketRegionMaxWindow, len(market.Window.Bars))
			}
		}
		return total
	}
	for _, window := range plan.Windows {
		total += int64(len(window.Bars))
		if plan.GeneOptions.MarketRegionEnabled {
			total += marketRegionMaximumProviderUnits(plan.GeneOptions.MarketRegionMaxWindow, len(window.Bars))
		}
	}
	return total
}

func candidateComputeUnits(plan EvaluablePlan, g Gene) int64 {
	var total int64
	region, regionMode := isMarketRegionGene(g)
	if len(plan.MultiMarkets) > 0 {
		for _, market := range plan.MultiMarkets {
			total += int64(len(market.Window.Bars))
			if regionMode {
				total += marketRegionProviderUnits(region, market.Window.Bars)
			}
		}
		return total
	}
	for _, window := range plan.Windows {
		total += int64(len(window.Bars))
		if regionMode {
			total += marketRegionProviderUnits(region, window.Bars)
		}
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

func (e *EvolutionEngine) sampleGene(cfg EpochConfig, rng RandomSource) Gene {
	if sampler, ok := e.evolvable.(GeneOptionSampler); ok {
		return sampler.SampleWithOptions(rng, cfg.GeneOptions)
	}
	return e.evolvable.Sample(rng)
}

func (e *EvolutionEngine) mutateGene(cfg EpochConfig, gene Gene, prob float64, scale float64, rng RandomSource) Gene {
	if mutator, ok := e.evolvable.(GeneOptionMutator); ok {
		return mutator.MutateWithOptions(gene, prob, scale, rng, cfg.GeneOptions)
	}
	return e.evolvable.Mutate(gene, prob, scale, rng)
}

func (e *EvolutionEngine) crossoverGene(cfg EpochConfig, left Gene, right Gene, rng RandomSource) Gene {
	if crossover, ok := e.evolvable.(GeneOptionCrossover); ok {
		return crossover.CrossoverWithOptions(left, right, rng, cfg.GeneOptions)
	}
	return e.evolvable.Crossover(left, right, rng)
}

func (e *EvolutionEngine) initializePopulation(ctx context.Context, cfg EpochConfig, rng RandomSource) ([]individual, error) {
	popSize := e.popSize(cfg)
	sourceGenes := make([]Gene, 0)
	if len(cfg.SeedParamPack) > 0 {
		sourceGenes = append(sourceGenes, e.normalizeGene(cfg, e.evolvable.DecodeElite(cfg.SeedParamPack)))
	}
	if cfg.UseInitialSeedOnly && len(sourceGenes) == 0 {
		sourceGenes = append(sourceGenes, e.normalizeGene(cfg, quant.DefaultSeedChromosome))
	}
	if !cfg.RandomPopulation && !cfg.UseInitialSeedOnly {
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
		for _, raw := range loaded {
			sourceGenes = append(sourceGenes, e.normalizeGene(cfg, e.evolvable.DecodeElite(raw)))
		}
	}

	population := make([]individual, 0, popSize)
	if len(sourceGenes) > 0 {
		seed := sourceGenes[0]
		population = append(population, individual{Gene: seed})
		remaining := popSize - 1
		copyCount := int(math.Round(float64(remaining) * 0.10))
		mutateCount := int(math.Round(float64(remaining) * 0.40))

		for i := 0; i < copyCount && len(population) < popSize; i++ {
			population = append(population, individual{Gene: sourceGenes[i%len(sourceGenes)]})
		}
		for i := 0; i < mutateCount && len(population) < popSize; i++ {
			base := sourceGenes[i%len(sourceGenes)]
			population = append(population, individual{Gene: e.normalizeGene(cfg, e.mutateGene(cfg, base, 0.15, 1.5, rng))})
		}
	} else if !cfg.RandomPopulation {
		population = append(population, individual{Gene: e.normalizeGene(cfg, quant.DefaultSeedChromosome)})
	}

	for len(population) < popSize {
		population = append(population, individual{Gene: e.normalizeGene(cfg, e.sampleGene(cfg, rng))})
	}
	e.trace(cfg, TraceModeSummary, "evolution", "population.initialized", "initial population initialized", map[string]any{
		"population":        len(population),
		"elite_count":       len(sourceGenes),
		"initial_seed_only": cfg.UseInitialSeedOnly,
	})
	return population, nil
}

// initializeLayeredPopulation chooses a single, explicit centre and then
// obtains every candidate from the deterministic lattice iterator.  Unlike the
// GA path it never fills a batch with random samples.
func (e *EvolutionEngine) initializeLayeredPopulation(ctx context.Context, cfg EpochConfig, rng RandomSource) ([]individual, *layeredGridScheduler, error) {
	var seed Gene
	if cfg.RandomPopulation {
		seed = e.normalizeGene(cfg, e.sampleGene(cfg, rng))
	} else if len(cfg.SeedParamPack) > 0 {
		seed = e.normalizeGene(cfg, e.evolvable.DecodeElite(cfg.SeedParamPack))
	}
	if seed == nil && !cfg.UseInitialSeedOnly {
		loaded, err := e.store.LoadEliteGenes(ctx, GeneScope{
			StrategyID: e.evolvable.StrategyID(), InstrumentID: cfg.InstrumentID,
			DataSource: cfg.DataSource, Interval: cfg.Interval, ExecutionMode: cfg.ExecutionMode,
		}, 1)
		if err != nil {
			return nil, nil, err
		}
		if len(loaded) > 0 {
			seed = e.normalizeGene(cfg, e.evolvable.DecodeElite(loaded[0]))
		}
	}
	if seed == nil {
		seed = e.normalizeGene(cfg, quant.DefaultSeedChromosome)
	}
	scheduler := newLayeredGridScheduler(seed, cfg.GeneOptions, cfg.LayeredLocalPercent)
	if len(cfg.SearchCheckpoint) > 0 {
		restored, err := restoreLayeredGridScheduler(cfg.SearchCheckpoint, cfg.GeneOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("layered grid checkpoint is incompatible: %w", err)
		}
		scheduler = restored
	}
	// Placeholder slots are deliberately filled only by
	// deduplicateLayeredPopulation, where reservation happens before an
	// evaluation candidate is admitted.  Do not consume grid points here.
	population := make([]individual, e.popSize(cfg))
	centreSource := "default"
	if cfg.RandomPopulation {
		centreSource = "random"
	} else if len(cfg.SeedParamPack) > 0 {
		centreSource = "seed_or_champion"
	}
	e.trace(cfg, TraceModeSummary, "evolution", "layered_grid.initialized", "layered grid population initialized", map[string]any{
		"population": len(population), "local_percent": cfg.LayeredLocalPercent,
		"centre_source": centreSource,
	})
	return population, scheduler, nil
}

func layeredAxisStatuses(scheduler *layeredGridScheduler) []LayeredAxisStatus {
	if scheduler == nil {
		return nil
	}
	return scheduler.Bounds()
}

func layeredSearchStatus(scheduler *layeredGridScheduler) *LayeredSearchStatus {
	if scheduler == nil {
		return nil
	}
	status := scheduler.Status()
	return &status
}

func (e *EvolutionEngine) persistLayeredCheckpoint(cfg EpochConfig, scheduler *layeredGridScheduler) {
	if scheduler == nil || cfg.OnSearchCheckpoint == nil {
		return
	}
	raw, err := scheduler.Checkpoint()
	if err == nil {
		cfg.OnSearchCheckpoint(raw)
	}
}

func (e *EvolutionEngine) deduplicateLayeredPopulation(population []individual, reserved map[uint64]bool, scheduler *layeredGridScheduler, cfg EpochConfig, plan EvaluablePlan) ([]individual, error) {
	out := make([]individual, 0, len(population))
	for range population {
		gene, err := e.nextNovelLayeredGene(reserved, scheduler, cfg, plan)
		if err != nil {
			return nil, err
		}
		out = append(out, individual{Gene: gene})
	}
	return out, nil
}

func (e *EvolutionEngine) nextLayeredPopulation(popSize int, reserved map[uint64]bool, scheduler *layeredGridScheduler, cfg EpochConfig, plan EvaluablePlan) ([]individual, error) {
	population := make([]individual, 0, popSize)
	for len(population) < popSize {
		gene, err := e.nextNovelLayeredGene(reserved, scheduler, cfg, plan)
		if err != nil {
			return nil, err
		}
		population = append(population, individual{Gene: gene})
	}
	return population, nil
}

// nextNovelLayeredGene does not use the GA retry limit.  A deterministic
// lattice may legitimately need to pass a long already-reserved prefix from a
// prior task before reaching its next unevaluated point; stopping after an
// arbitrary number would turn a valid search into the observed 0% failure.
func (e *EvolutionEngine) nextNovelLayeredGene(reserved map[uint64]bool, scheduler *layeredGridScheduler, cfg EpochConfig, plan EvaluablePlan) (Gene, error) {
	for {
		gene := e.normalizeGene(cfg, scheduler.Next())
		materialized := []individual{{Gene: gene}}
		if err := materializePopulationMarketRegionPacks(materialized, plan); err != nil {
			return nil, err
		}
		gene = materialized[0].Gene
		fingerprint := e.evolvable.Fingerprint(gene)
		if !reserved[fingerprint] {
			reserved[fingerprint] = true
			return gene, nil
		}
		scheduler.RecordDuplicate()
	}
}

func (e *EvolutionEngine) evaluatePopulation(ctx context.Context, population []individual, plan EvaluablePlan, generation int, cfg EpochConfig, scope GeneScope, searchConfig []byte) ([]individual, error) {
	unevaluated := 0
	for _, item := range population {
		if !item.Evaluated {
			unevaluated++
		}
	}
	if unevaluated == 0 {
		return population, nil
	}
	if reservations, ok := e.store.(CandidateReservationStore); ok {
		candidates := make([]CandidateReservation, 0, unevaluated)
		for _, item := range population {
			if !item.Evaluated {
				paramPack, err := e.evolvable.EncodeResult(item.Gene, plan.Spawn, cfg.GeneOptions)
				if err != nil {
					return nil, err
				}
				candidates = append(candidates, CandidateReservation{Fingerprint: e.evolvable.Fingerprint(item.Gene), ParamPack: paramPack})
			}
		}
		if err := reservations.ReserveCandidates(ctx, scope, searchConfig, cfg.TaskID, generation, candidates); err != nil {
			return nil, err
		}
	}
	workers := runtime.NumCPU()
	if workers > unevaluated {
		workers = unevaluated
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
					population[task.index].Evaluated = true
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
				population[task.index].Evaluated = true
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
		if !item.Evaluated {
			tasks <- task{index: i, gene: item.Gene}
		}
	}
	close(tasks)
	wg.Wait()
	if resultStore, ok := e.store.(CandidateResultStore); ok {
		results := make([]CandidateEvaluation, 0, len(population))
		for _, item := range population {
			if item.Evaluated {
				results = append(results, CandidateEvaluation{
					Fingerprint: e.evolvable.Fingerprint(item.Gene),
					Fitness:     item.Fitness,
				})
			}
		}
		if err := resultStore.UpdateCandidateResults(ctx, searchConfig, results); err != nil {
			return nil, err
		}
	}
	e.trace(cfg, TraceModeSummary, "evolution", "population.evaluated", "population evaluation completed", map[string]any{
		"generation": generation,
		"population": len(population),
	})
	return population, nil
}

func (e *EvolutionEngine) deduplicatePopulation(population []individual, reserved map[uint64]bool, rng RandomSource, cfg EpochConfig) ([]individual, error) {
	if len(population) == 0 {
		return population, nil
	}
	out := make([]individual, 0, len(population))
	for _, item := range population {
		gene, ok := e.reserveNovelGene(e.normalizeGene(cfg, item.Gene), reserved, func() Gene {
			return e.normalizeGene(cfg, e.sampleGene(cfg, rng))
		})
		if !ok {
			return nil, fmt.Errorf("候選參數空間已用盡，無法產生未搜尋過的參數組合")
		}
		out = append(out, individual{Gene: gene})
	}
	return out, nil
}

// reserveNovelGene reserves before a candidate enters the evaluation queue.
// The first proposal is kept when it is new; retries use the supplied creator.
// Therefore a duplicate is never evaluated and never becomes an offspring.
func (e *EvolutionEngine) reserveNovelGene(first Gene, reserved map[uint64]bool, create func() Gene) (Gene, bool) {
	if reserved == nil {
		return first, true
	}
	if fingerprint := e.evolvable.Fingerprint(first); !reserved[fingerprint] {
		reserved[fingerprint] = true
		return first, true
	}
	for i := 0; i < 1024; i++ {
		candidate := create()
		fingerprint := e.evolvable.Fingerprint(candidate)
		if !reserved[fingerprint] {
			reserved[fingerprint] = true
			return candidate, true
		}
	}
	return nil, false
}

func (e *EvolutionEngine) nextGeneration(population []individual, mutProb float64, mutScale float64, rng RandomSource, cfg EpochConfig, generation int, reserved map[uint64]bool) ([]individual, error) {
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
		nearby := func() Gene {
			child := e.crossoverGene(cfg, p1.Gene, p2.Gene, rng)
			return e.normalizeGene(cfg, e.mutateGene(cfg, child, mutProb, mutScale, rng))
		}
		nearbyAttempts := 0
		child, ok := e.reserveNovelGene(nearby(), reserved, func() Gene {
			nearbyAttempts++
			// Keep exploiting the stronger parents first. Once their nearby lattice
			// has been visited, broaden to a fresh global sample.
			if nearbyAttempts <= 64 {
				return nearby()
			}
			return e.normalizeGene(cfg, e.sampleGene(cfg, rng))
		})
		if !ok {
			return nil, fmt.Errorf("候選參數空間已用盡，無法產生未搜尋過的參數組合")
		}
		e.trace(cfg, TraceModeDetailed, "evolution", "offspring.created", "offspring generated", map[string]any{
			"generation":           generation,
			"child":                len(next),
			"parent_a_score":       p1.Fitness.ScoreTotal,
			"parent_b_score":       p2.Fitness.ScoreTotal,
			"mutation_probability": mutProb,
			"mutation_scale":       mutScale,
		})
		next = append(next, individual{Gene: child})
	}
	e.trace(cfg, TraceModeSummary, "evolution", "generation.spawned", "next generation spawned", map[string]any{
		"generation":  generation,
		"population":  len(next),
		"elite_count": eliteCount,
	})
	return next, nil
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
