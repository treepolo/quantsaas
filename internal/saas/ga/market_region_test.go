package ga

import (
	"encoding/json"
	"testing"

	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

func TestMarketRegionKeysUsesEachFeatureInterval(t *testing.T) {
	features := []MarketRegionFeature{
		{ID: "tr_mean", Window: 2, Thresholds: []float64{0.1}},
		{ID: "parkinson", Window: 2, Thresholds: []float64{0.01, 0.02}},
	}
	keys := marketRegionKeys(features)
	if len(keys) != 6 {
		t.Fatalf("expected 6 Cartesian region keys, got %d", len(keys))
	}
	key, available := marketRegionKey(features, map[string]float64{"tr_mean": 0.11, "parkinson": 0.015})
	if !available || key != "tr_mean=1;parkinson=1" {
		t.Fatalf("unexpected region key %q, available=%v", key, available)
	}
}

func TestResolveMarketRegionParamsUsesLatestCompletedBar(t *testing.T) {
	features := make([]MarketRegionFeature, 0, len(MarketRegionFeatureIDs))
	for _, id := range MarketRegionFeatureIDs {
		features = append(features, MarketRegionFeature{ID: id, Window: 2})
	}
	keys := marketRegionKeys(features)
	base := quant.DefaultSeedChromosome
	base.Beta = 0.8
	gene := MarketRegionGene{SchemaVersion: MarketRegionSchemaVersion, Features: features, Packs: []MarketRegionPack{{Key: keys[0], Chromosome: base}}}
	gene, err := normalizeMarketRegionGene(gene, GeneOptions{MarketRegionEnabled: true, MarketRegionMaxThresholds: 1, MarketRegionMaxWindow: 2})
	if err != nil {
		t.Fatalf("normalize gene: %v", err)
	}
	params := sigmoiddca.DefaultParams()
	raw, err := json.Marshal(marketRegionParamPack{SchemaVersion: MarketRegionSchemaVersion, Chromosome: params.Chromosome, Spawn: params.Spawn, PositionStructure: params.PositionStructure, MarketRegion: gene})
	if err != nil {
		t.Fatalf("marshal parameter pack: %v", err)
	}
	legacyParams := sigmoiddca.ParseParamsFromParamPack(raw)
	if legacyParams.PositionStructure != params.PositionStructure || legacyParams.Spawn.Policy.MonthlyInjectUSDT != params.Spawn.Policy.MonthlyInjectUSDT {
		t.Fatal("market region pack must retain the normal parameter-pack fields")
	}
	params, handled, err := ResolveMarketRegionParams(raw, marketRegionTestBars())
	if err != nil || !handled {
		t.Fatalf("resolve market region params: handled=%v err=%v", handled, err)
	}
	if params.Chromosome.Beta != quant.ClampChromosome(base).Beta {
		t.Fatalf("expected selected pack beta %v, got %v", base.Beta, params.Chromosome.Beta)
	}
}

func marketRegionTestBars() []quant.Bar {
	return []quant.Bar{
		{OpenTime: 1, Open: 100, High: 102, Low: 99, Close: 101},
		{OpenTime: 2, Open: 101, High: 104, Low: 100, Close: 103},
		{OpenTime: 3, Open: 103, High: 105, Low: 101, Close: 102},
		{OpenTime: 4, Open: 102, High: 106, Low: 101, Close: 105},
	}
}
