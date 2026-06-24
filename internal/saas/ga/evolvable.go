package ga

import (
	"context"

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
	Pair           string
	Interval       string
	ExecutionMode  string
	TemplateName   string
	Spawn          *quant.SpawnPoint
	Costs          quant.ExecutionCostConfig
	TradePenalty   float64
	GeneOptions    GeneOptions
	LotStep        float64
	LotMin         float64
	Windows        []quant.CrucibleWindow
	DCABaselines   []DCABaseline
	AggregateCache map[string]any
	Trace          func(TraceEvent)
	TraceMode      TraceMode
	TraceModeFunc  func() TraceMode
	Generation     int
	Individual     int
	Worker         int
}

type FitnessResult struct {
	ScoreTotal  float64
	MaxDrawdown float64
	Windows     []quant.CrucibleResult
	Fatal       bool
}

type BacktestMetrics struct {
	ROI           float64
	MaxDrawdown   float64
	FinalEquity   float64
	TotalInjected float64
	TradeCount    int
}

type GeneOptions struct {
	EvolveRebalanceThreshold  bool
	EvolveForceFullThreshold  bool
	EvolveForceEmptyThreshold bool
	EnableWMean               bool
	EnableWMomentum           bool
	EnableWBreakout           bool
	PositionStructure         string
}

type GeneNormalizer interface {
	NormalizeGene(c Gene, options GeneOptions) Gene
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
