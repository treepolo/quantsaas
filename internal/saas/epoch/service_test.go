package epoch

import (
	"context"
	"testing"

	"quantsaas/internal/saas/ga"
	"quantsaas/internal/strategies/sigmoiddca"
)

func TestNormalizeRequestKeepsRebalanceForFloatingOnly(t *testing.T) {
	service := &Service{}
	req := service.normalizeRequest(context.Background(), CreateTaskRequest{
		PositionStructure:        sigmoiddca.PositionStructureFloatingOnly,
		EvolveRebalanceThreshold: true,
		MultiMarketEnabled:       true,
	})
	if !req.EvolveRebalanceThreshold {
		t.Fatal("floating-only request must preserve rebalance evolution")
	}
	if !searchGeneOptions(req).EvolveRebalanceThreshold {
		t.Fatal("floating-only gene options must evolve rebalance threshold when requested")
	}
}

func TestSearchGeneOptionsDisableIrrelevantMinimumTradeFields(t *testing.T) {
	zero := 0.0
	options := searchGeneOptions(CreateTaskRequest{
		PositionStructure: sigmoiddca.PositionStructureDualLayer,
		FeeRate:           &zero,
		SpreadRate:        &zero,
	})
	if !options.DisableMinimumTrade {
		t.Fatal("zero fee and spread must disable minimum-trade candidate semantics")
	}

	fee := 0.001
	options = searchGeneOptions(CreateTaskRequest{
		PositionStructure: sigmoiddca.PositionStructureFloatingOnly,
		FeeRate:           &fee,
		SpreadRate:        &zero,
	})
	if !options.DisableMinimumTrade {
		t.Fatal("floating-only structure must disable minimum-trade candidate semantics")
	}
}

func TestSearchMarketScopesIgnoreSingleMarketFields(t *testing.T) {
	req := CreateTaskRequest{
		Pair:               "IGNORED",
		InstrumentID:       "IGNORED",
		DataSource:         "ignored",
		Interval:           "1h",
		TrainStartMs:       999,
		TrainEndMs:         1000,
		MultiMarketEnabled: true,
		MultiMarketSelections: []MultiMarketSelection{
			{InstrumentID: "BTCUSDT", Pair: "BTCUSDT", DataSource: "binance", Interval: "1d", StartTimeMs: 1, EndTimeMs: 2},
			{InstrumentID: "SOXL", Pair: "SOXL", DataSource: "yahoo", Interval: "1d", StartTimeMs: 3, EndTimeMs: 4},
		},
	}
	scopes := searchMarketScopes(req)
	if len(scopes) != 2 {
		t.Fatalf("market scopes = %d, want 2", len(scopes))
	}
	if scopes[0].InstrumentID != "BTCUSDT" || scopes[0].StartTimeMs != 1 || scopes[1].InstrumentID != "SOXL" || scopes[1].EndTimeMs != 4 {
		t.Fatalf("multi-market scopes did not come exclusively from selections: %+v", scopes)
	}
	for _, scope := range scopes {
		if scope.Pair == "IGNORED" || scope.Interval == "1h" || scope.StartTimeMs == 999 {
			t.Fatalf("single-market field leaked into multi-market scope: %+v", scope)
		}
	}
}

func TestStandardizedMarketScopesKeepFullMultiMarketSet(t *testing.T) {
	req := CreateTaskRequest{
		MultiMarketEnabled: true,
		StandardStartMs:    100,
		StandardEndMs:      200,
		MultiMarketSelections: []MultiMarketSelection{
			{InstrumentID: "BTCUSDT", Pair: "BTCUSDT", DataSource: "binance", Interval: "1d", StartTimeMs: 1, EndTimeMs: 2},
			{InstrumentID: "SOXL", Pair: "SOXL", DataSource: "yahoo", Interval: "1d", StartTimeMs: 3, EndTimeMs: 4},
		},
	}
	scopes := standardizedMarketScopes(req)
	if len(scopes) != 2 {
		t.Fatalf("standardized market scopes = %d, want 2", len(scopes))
	}
	for _, scope := range scopes {
		if scope.StartTimeMs != 100 || scope.EndTimeMs != 200 {
			t.Fatalf("standardized range not applied to %s: %+v", scope.InstrumentID, scope)
		}
	}
	if scopes[0].InstrumentID != "BTCUSDT" || scopes[1].InstrumentID != "SOXL" {
		t.Fatalf("standardized evaluation changed the selected market set: %+v", scopes)
	}
}

func TestSameSettingsBestUsesInitialSeedOnlyForFirstIteration(t *testing.T) {
	initialSeed := []byte(`{"seed":"initial"}`)
	req := CreateTaskRequest{
		ContinuousMode: ContinuousModeSameSettingsBest,
		seedParamPack:  initialSeed,
	}

	first := ga.EpochConfig{SeedParamPack: req.seedParamPack}
	applyContinuousIteration(&first, req, 1, nil)
	if string(first.SeedParamPack) != string(initialSeed) {
		t.Fatal("first iteration must preserve the explicitly selected initial seed")
	}
	if first.RandomPopulation {
		t.Fatal("same-settings best must not use a random-only population")
	}

	second := ga.EpochConfig{SeedParamPack: req.seedParamPack}
	applyContinuousIteration(&second, req, 2, nil)
	if len(second.SeedParamPack) != 0 {
		t.Fatal("later iterations must let the highest-scoring same-identity elite become the primary seed")
	}
	if second.RandomPopulation {
		t.Fatal("same-settings best must continue loading historical elites")
	}
}

func TestStandardizedBestKeepsItsExplicitChampionSeed(t *testing.T) {
	req := CreateTaskRequest{ContinuousMode: ContinuousModeStandardizedBest}
	champion := &standardizedChampion{ParamPack: []byte(`{"seed":"standardized"}`)}
	cfg := ga.EpochConfig{}
	applyContinuousIteration(&cfg, req, 2, champion)
	if string(cfg.SeedParamPack) != string(champion.ParamPack) {
		t.Fatal("standardized-best mode must continue using the standardized champion")
	}
}
