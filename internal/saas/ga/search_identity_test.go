package ga

import (
	"testing"

	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

func TestSearchIdentityChangesOnlyWithEvaluationSemantics(t *testing.T) {
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
	spawn := quant.SpawnPoint{
		Policy: quant.CapitalPolicy{InitialUSDT: 1_000_000},
		Risk:   quant.RiskBounds{MaxDrawdownPct: 0.88, LotStep: 0.000001, LotMin: 0.00001},
	}
	base := EvaluablePlan{
		ExecutionMode: "close_next_open",
		Spawn:         &spawn,
		Costs:         quant.ExecutionCostConfig{FeeRate: 0.001, SpreadRate: 0.0005},
		GeneOptions:   options,
		Datasets: []DatasetIdentity{{
			InstrumentID: "BTCUSDT",
			DataSource:   "binance",
			Pair:         "BTCUSDT",
			Interval:     "1d",
			StartTimeMs:  1,
			EndTimeMs:    2,
			ContentHash:  "content-a",
			BarCount:     2,
		}},
	}
	first := BuildSearchIdentity("sigmoid-dca-btc", base)
	if first.Hash() != BuildSearchIdentity("sigmoid-dca-btc", base).Hash() {
		t.Fatal("same evaluation semantics produced different search hashes")
	}

	changedData := base
	changedData.Datasets = append([]DatasetIdentity(nil), base.Datasets...)
	changedData.Datasets[0].ContentHash = "content-b"
	if first.Hash() == BuildSearchIdentity("sigmoid-dca-btc", changedData).Hash() {
		t.Fatal("dataset content change did not change search hash")
	}

	changedCosts := base
	changedCosts.Costs.FeeRate = 0
	if first.Hash() == BuildSearchIdentity("sigmoid-dca-btc", changedCosts).Hash() {
		t.Fatal("execution-cost change did not change search hash")
	}

	fixed := quant.DefaultSeedChromosome
	fixed.Beta = 1.25
	changedFixed := base
	changedFixed.GeneOptions.FixedGene = &fixed
	changedFixed.GeneOptions.FixedParamKeys = []string{"beta"}
	if first.Hash() == BuildSearchIdentity("sigmoid-dca-btc", changedFixed).Hash() {
		t.Fatal("fixed effective parameter did not change search hash")
	}
}

func TestMultiMarketIdentityIsOrderIndependentAndDatasetComplete(t *testing.T) {
	spawn := quant.SpawnPoint{Policy: quant.CapitalPolicy{InitialUSDT: 1_000_000}}
	plan := EvaluablePlan{
		ExecutionMode: "close_next_open",
		Spawn:         &spawn,
		GeneOptions: GeneOptions{
			EnableWMean:       true,
			PositionStructure: sigmoiddca.PositionStructureFloatingOnly,
		},
		MultiMarkets: []MultiMarketPlan{{InstrumentID: "BTCUSDT"}, {InstrumentID: "SOXL"}},
		Datasets: []DatasetIdentity{
			{InstrumentID: "SOXL", DataSource: "yahoo", Pair: "SOXL", Interval: "1d", StartTimeMs: 10, EndTimeMs: 20, ContentHash: "soxl", BarCount: 11},
			{InstrumentID: "BTCUSDT", DataSource: "binance", Pair: "BTCUSDT", Interval: "1d", StartTimeMs: 1, EndTimeMs: 9, ContentHash: "btc", BarCount: 9},
		},
	}
	first := BuildSearchIdentity("sigmoid-dca-btc", plan)
	reordered := plan
	reordered.Datasets = []DatasetIdentity{plan.Datasets[1], plan.Datasets[0]}
	if first.Hash() != BuildSearchIdentity("sigmoid-dca-btc", reordered).Hash() {
		t.Fatal("multi-market dataset order changed search identity")
	}
	changedRange := plan
	changedRange.Datasets = append([]DatasetIdentity(nil), plan.Datasets...)
	changedRange.Datasets[0].EndTimeMs++
	if first.Hash() == BuildSearchIdentity("sigmoid-dca-btc", changedRange).Hash() {
		t.Fatal("per-market range change did not change search identity")
	}
}
