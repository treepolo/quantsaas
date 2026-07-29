package ga

import (
	"encoding/json"
	"fmt"
	"math/rand"
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

func TestThresholdCombinationUsesExactRawValuesWithoutTransform(t *testing.T) {
	values := []float64{0.011, 0.027, 0.043, 0.089}
	seen := map[string]bool{}
	for index := 0; index < 6; index++ {
		got := thresholdValuesAtRanks(values, []int{0, 1}, index)
		if len(got) != 2 {
			t.Fatalf("combination %d has %d values", index, len(got))
		}
		for _, value := range got {
			found := false
			for _, raw := range values {
				found = found || value == raw
			}
			if !found {
				t.Fatalf("threshold %v is not an original market observation", value)
			}
		}
		seen[fmt.Sprintf("%v", got)] = true
	}
	if len(seen) != 6 {
		t.Fatalf("enumerated %d combinations, want 6", len(seen))
	}
}

func TestRebuildMarketRegionPacksDoesNotAllocateTheoreticalCombinations(t *testing.T) {
	features := make([]MarketRegionFeature, 0, len(MarketRegionFeatureIDs))
	for _, id := range MarketRegionFeatureIDs {
		features = append(features, MarketRegionFeature{ID: id, Window: 2})
	}
	oldKey := marketRegionKeys(features)[0]
	features[0].Thresholds = []float64{0.05}
	region := MarketRegionGene{
		SchemaVersion: MarketRegionSchemaVersion,
		Global:        quant.DefaultSeedChromosome,
		Features:      features,
		Packs: []MarketRegionPack{{
			Key:        oldKey,
			Chromosome: quant.DefaultSeedChromosome,
		}},
	}
	rebuilt := rebuildMarketRegionPacks(region, rand.New(rand.NewSource(1)))
	if len(rebuilt.Packs) != 1 || rebuilt.Packs[0].Key != oldKey {
		t.Fatalf("layout mutation must retain only existing sparse packs, got %#v", rebuilt.Packs)
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
	base.Gamma = 0.8
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
	if params.Chromosome.Beta != 0 {
		t.Fatalf("market-region beta must be disabled, got %v", params.Chromosome.Beta)
	}
	if params.Chromosome.Gamma != quant.ClampChromosome(base).Gamma {
		t.Fatalf("expected selected pack gamma %v, got %v", base.Gamma, params.Chromosome.Gamma)
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

func TestMarketRegionFingerprintIgnoresInactiveFeatureWindows(t *testing.T) {
	featuresA := make([]MarketRegionFeature, 0, len(MarketRegionFeatureIDs))
	featuresB := make([]MarketRegionFeature, 0, len(MarketRegionFeatureIDs))
	for index, id := range MarketRegionFeatureIDs {
		featuresA = append(featuresA, MarketRegionFeature{ID: id, Window: 2})
		featuresB = append(featuresB, MarketRegionFeature{ID: id, Window: 9 + index})
	}
	a := MarketRegionGene{
		SchemaVersion: MarketRegionSchemaVersion,
		Global:        quant.DefaultSeedChromosome,
		DefaultState:  marketRegionStateChromosome(quant.DefaultSeedChromosome),
		Features:      featuresA,
	}
	b := a
	b.Features = featuresB
	if marketRegionFingerprint(a) != marketRegionFingerprint(b) {
		t.Fatal("inactive feature windows must not create distinct candidates")
	}
}
