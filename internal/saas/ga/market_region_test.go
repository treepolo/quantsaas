package ga

import (
	"encoding/json"
	"reflect"
	"testing"

	"quantsaas/internal/backtestcore"
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

func TestMarketRegionCachedValuesAndProviderMatchUncachedExactly(t *testing.T) {
	bars := marketRegionLongTestBars()
	features := make([]MarketRegionFeature, 0, len(MarketRegionFeatureIDs))
	for _, id := range MarketRegionFeatureIDs {
		feature := MarketRegionFeature{ID: id, Window: 3}
		if id == "tr_mean" {
			feature.Thresholds = []float64{0}
		}
		features = append(features, feature)
	}
	gene := MarketRegionGene{SchemaVersion: MarketRegionSchemaVersion, Features: features}
	for index, key := range marketRegionKeys(features) {
		chromosome := quant.DefaultSeedChromosome
		chromosome.Beta = 0.2 + float64(index)*0.1
		gene.Packs = append(gene.Packs, MarketRegionPack{Key: key, Chromosome: chromosome})
	}
	uncachedValues, err := marketRegionValues(bars, features)
	if err != nil {
		t.Fatalf("uncached values: %v", err)
	}
	cache := NewMarketRegionFeatureCache()
	cachedValues, err := marketRegionValuesWithCache(bars, features, cache)
	if err != nil {
		t.Fatalf("cached values: %v", err)
	}
	if !reflect.DeepEqual(uncachedValues, cachedValues) {
		t.Fatal("cached market-region values differ from the existing exact calculation")
	}
	if _, err := marketRegionValuesWithCache(bars, features, cache); err != nil {
		t.Fatalf("cached values reuse: %v", err)
	}
	if len(cache.entries) != 1 {
		t.Fatalf("expected one reusable feature series, got %d", len(cache.entries))
	}
	uncachedProvider, err := newMarketRegionProvider(gene, bars)
	if err != nil {
		t.Fatalf("uncached provider: %v", err)
	}
	cachedProvider, err := newMarketRegionProviderWithCache(gene, bars, cache)
	if err != nil {
		t.Fatalf("cached provider: %v", err)
	}
	for index := range bars {
		context := backtestcore.ParameterContext{Index: index}
		want, err := uncachedProvider(context)
		if err != nil {
			t.Fatalf("uncached provider index %d: %v", index, err)
		}
		got, err := cachedProvider(context)
		if err != nil {
			t.Fatalf("cached provider index %d: %v", index, err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("provider output differs at index %d", index)
		}
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

func marketRegionLongTestBars() []quant.Bar {
	bars := make([]quant.Bar, 0, 40)
	for index := 0; index < 40; index++ {
		open := 100.0 + float64(index)*0.7
		close := open + 0.3 + float64(index%3)*0.1
		bars = append(bars, quant.Bar{OpenTime: int64(index + 1), Open: open, High: close + 1.1, Low: open - 0.9, Close: close})
	}
	return bars
}
