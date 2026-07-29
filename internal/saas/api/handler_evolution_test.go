package api

import (
	"testing"
	"time"

	"quantsaas/internal/saas/ga"
	saasstore "quantsaas/internal/saas/store"
)

func TestEvolutionTaskResponseDoesNotInventUncomputedMetrics(t *testing.T) {
	task := saasstore.EvolutionTask{
		ID:        7,
		CreatedAt: time.Unix(1, 0).UTC(),
		Status:    "running",
		Config:    saasstore.JSONB(`{"pop_size":300,"max_generations":25,"grid_coverage_enabled":true}`),
		Result:    saasstore.JSONB(`{"best_valid":false,"evaluated_count":4,"valid_count":0,"skipped_count":3,"failed_count":1}`),
	}
	response := evolutionTaskResponse(task)
	if response["best_score"] != nil || response["max_drawdown"] != nil {
		t.Fatalf("uncomputed metrics must be null, got score=%v drawdown=%v", response["best_score"], response["max_drawdown"])
	}
	if response["evaluated_count"] != int64(4) || response["valid_count"] != int64(0) || response["skipped_count"] != int64(3) || response["failed_count"] != int64(1) {
		t.Fatalf("exact progress counters were not preserved: %+v", response)
	}
	if response["evaluated_individuals"] != int64(4) {
		t.Fatalf("evaluated_individuals = %v, want exact counter 4", response["evaluated_individuals"])
	}
}

func TestEvolutionTaskResponsePreservesNegativeValidScoreAndMultiMarketResults(t *testing.T) {
	task := saasstore.EvolutionTask{
		ID:         8,
		CreatedAt:  time.Unix(1, 0).UTC(),
		Status:     "completed",
		SearchHash: "search-hash",
		Config: saasstore.JSONB(`{
			"pop_size":10,
			"max_generations":5,
			"multi_market_enabled":true,
			"multi_market_selections":[
				{"instrument_id":"BTCUSDT","use_all_data":true},
				{"instrument_id":"SOXL","use_all_data":true}
			]
		}`),
		Result: saasstore.JSONB(`{
			"best_valid":true,
			"best_score":-0.25,
			"max_drawdown":0.4,
			"market_performance":[
				{"instrument_id":"BTCUSDT","pair":"BTCUSDT","total_return":0.1,"annualized_return":0.05,"max_drawdown":0.2},
				{"instrument_id":"SOXL","pair":"SOXL","total_return":-0.2,"annualized_return":-0.1,"max_drawdown":0.4}
			]
		}`),
	}
	response := evolutionTaskResponse(task)
	if response["best_score"] != -0.25 {
		t.Fatalf("best_score = %v, want -0.25", response["best_score"])
	}
	if response["search_hash"] != "search-hash" || response["multi_market_enabled"] != true {
		t.Fatalf("multi-market identity missing: %+v", response)
	}
	markets, ok := response["market_performance"].([]ga.MarketPerformance)
	if !ok || len(markets) != 2 {
		t.Fatalf("market performance length = %d, want 2", len(markets))
	}
}
