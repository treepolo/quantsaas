package ga

import (
	"context"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/quant"
)

type Gene = any

const FatalFitnessScore = -99999.0

type DCABaseline struct {
	FinalEquity   float64
	TotalInjected float64
	MaxDrawdown   float64
	ROI           float64
}

type EvaluablePlan struct {
	Pair                string
	Interval            string
	ExecutionMode       string
	TemplateName        string
	Spawn               *quant.SpawnPoint
	Costs               quant.ExecutionCostConfig
	TradePenalty        float64
	LongTermFilter      backtestcore.LongTermFilterConfig
	GeneOptions         GeneOptions
	LotStep             float64
	LotMin              float64
	Windows             []quant.CrucibleWindow
	DCABaselines        []DCABaseline
	AggregateCache      map[string]any
	MarketRegionCache   *MarketRegionFeatureCache
	InitializationUnits int64
	Trace               func(TraceEvent)
	TraceMode           TraceMode
	TraceModeFunc       func() TraceMode
	ComputeStep         func(int64)
	Generation          int
	Individual          int
	Worker              int
	// MultiMarkets is populated only for the multi-market search mode.  It is
	// deliberately separate from Windows: multi-market evaluation uses one
	// complete training period per market and never applies crucible weights.
	MultiMarkets []MultiMarketPlan
}

type MultiMarketPlan struct {
	Pair              string
	InstrumentID      string
	Window            quant.CrucibleWindow
	MarketRegionCache *MarketRegionFeatureCache
}

type FitnessResult struct {
	ScoreTotal  float64
	MaxDrawdown float64
	Windows     []quant.CrucibleResult
	Markets     []MarketPerformance
	Fatal       bool
}

// MarketPerformance is a complete-period result for one selected market in a
// multi-market search. It is retained only for the currently best candidate
// and saved challenger; it never changes the direct summed score formula.
type MarketPerformance struct {
	InstrumentID     string  `json:"instrument_id"`
	Pair             string  `json:"pair"`
	TotalReturn      float64 `json:"total_return"`
	AnnualizedReturn float64 `json:"annualized_return"`
	MaxDrawdown      float64 `json:"max_drawdown"`
}

type BacktestMetrics struct {
	ROI           float64
	MaxDrawdown   float64
	FinalEquity   float64
	TotalInjected float64
	TradeCount    int
}

type ComputePlan struct {
	UnitsPerIndividual int64
	PlannedUnits       int64
}

type GeneOptions struct {
	EvolveRebalanceThreshold  bool
	EvolveForceFullThreshold  bool
	EvolveForceEmptyThreshold bool
	EvolveGamma               bool
	EnableWMean               bool
	EnableWMomentum           bool
	EnableWBreakout           bool
	PositionStructure         string
	FixedParamKeys            []string
	FixedGene                 *quant.Chromosome     `json:"-"`
	MarketRegionEnabled       bool                  `json:"market_region_enabled"`
	MarketRegionMaxThresholds int                   `json:"market_region_max_thresholds"`
	MarketRegionMaxWindow     int                   `json:"market_region_max_window"`
	MarketRegionFeatureRanges map[string][2]float64 `json:"market_region_feature_ranges,omitempty"`
	// MarketRegionFeatureValues is an internal, sorted pool of the exact raw
	// calculated market values. It is intentionally not persisted in task
	// settings: it is rebuilt from the task's selected training data.
	MarketRegionFeatureValues map[string][]float64 `json:"-"`
	// These switches remove a legacy mechanism from both the candidate space and
	// the strategy calculation for the current search mode.
	DisableBeta         bool `json:"disable_beta"`
	DisableDustFilter   bool `json:"disable_dust_filter"`
	DisableWedgeMinimum bool `json:"disable_wedge_minimum"`
}

type GeneOptionSampler interface {
	SampleWithOptions(rng RandomSource, options GeneOptions) Gene
}

type GeneNormalizer interface {
	NormalizeGene(c Gene, options GeneOptions) Gene
}

// Optional strategy-side operations let a candidate space exclude inactive
// dimensions before crossover or mutation happens.
type GeneOptionMutator interface {
	MutateWithOptions(c Gene, prob float64, scale float64, rng RandomSource, options GeneOptions) Gene
}

type GeneOptionCrossover interface {
	CrossoverWithOptions(p1 Gene, p2 Gene, rng RandomSource, options GeneOptions) Gene
}

type EvolvableStrategy interface {
	StrategyID() string
	Sample(rng RandomSource) Gene
	Mutate(c Gene, prob float64, scale float64, rng RandomSource) Gene
	Crossover(p1 Gene, p2 Gene, rng RandomSource) Gene
	Fingerprint(c Gene) uint64
	Evaluate(ctx context.Context, c Gene, plan EvaluablePlan) (FitnessResult, error)
	DecodeElite(raw []byte) Gene
	EncodeResult(c Gene, spawn *quant.SpawnPoint, options GeneOptions) ([]byte, error)
	Verify(ctx context.Context, c Gene, spawn *quant.SpawnPoint, bars []quant.Bar, lotStep float64, lotMin float64) (BacktestMetrics, error)
}

type RandomSource interface {
	Float64() float64
	NormFloat64() float64
	Intn(n int) int
}

type TraceMode string

const (
	TraceModeOff      TraceMode = "off"
	TraceModeSummary  TraceMode = "summary"
	TraceModeDetailed TraceMode = "detailed"
	TraceModeFull     TraceMode = "full"
)

type TraceEvent struct {
	RequiredMode TraceMode      `json:"-"`
	Level        string         `json:"level"`
	Source       string         `json:"source"`
	Scope        string         `json:"scope"`
	Message      string         `json:"message"`
	Fields       map[string]any `json:"fields,omitempty"`
}

func NormalizeTraceMode(mode TraceMode) TraceMode {
	switch mode {
	case TraceModeSummary, TraceModeDetailed, TraceModeFull:
		return mode
	default:
		return TraceModeOff
	}
}

func TraceEnabled(active TraceMode, required TraceMode) bool {
	active = NormalizeTraceMode(active)
	required = NormalizeTraceMode(required)
	if active == TraceModeOff || required == TraceModeOff {
		return false
	}
	rank := map[TraceMode]int{
		TraceModeSummary:  1,
		TraceModeDetailed: 2,
		TraceModeFull:     3,
	}
	return rank[active] >= rank[required]
}
