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
	TemplateName   string
	Spawn          *quant.SpawnPoint
	LotStep        float64
	LotMin         float64
	Windows        []quant.CrucibleWindow
	DCABaselines   []DCABaseline
	AggregateCache map[string]any
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
}

type EvolvableStrategy interface {
	StrategyID() string
	Sample(rng RandomSource) Gene
	Mutate(c Gene, prob float64, scale float64, rng RandomSource) Gene
	Crossover(p1 Gene, p2 Gene, rng RandomSource) Gene
	Fingerprint(c Gene) uint64
	Evaluate(ctx context.Context, c Gene, plan EvaluablePlan) (FitnessResult, error)
	DecodeElite(raw []byte) Gene
	EncodeResult(c Gene, spawn *quant.SpawnPoint) ([]byte, error)
	Verify(ctx context.Context, c Gene, spawn *quant.SpawnPoint, bars []quant.Bar, lotStep float64, lotMin float64) (BacktestMetrics, error)
}

type RandomSource interface {
	Float64() float64
	NormFloat64() float64
	Intn(n int) int
}
