package ga

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/dynamicparam"
	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

// MarketRegionSchemaVersion identifies the parameter-search candidate that
// selects a complete chromosome from causal market-data intervals.
const MarketRegionSchemaVersion = "market-region-parameter-search-v1"

// MarketRegionFeatureIDs is deliberately fixed: this mode uses all approved
// market-internal values, rather than a prediction output or state label.
var MarketRegionFeatureIDs = []string{
	"tr_mean", "tr_std_dev", "high_low_mean", "high_low_std_dev", "parkinson", "yang_zhang",
	"tr_mean_history_ratio", "tr_std_dev_history_ratio", "high_low_mean_history_ratio", "high_low_std_dev_history_ratio", "parkinson_history_ratio", "yang_zhang_history_ratio",
	"geometry_coverage_area", "close_trend_slope",
}

// marketRegionProviderUnits is a stable work-unit estimate for materialising
// causal activity and geometry series. Each distinct window needs one activity
// pass and one geometry pass; the per-bar selection itself is linear in the
// number of approved market values.
func marketRegionProviderUnits(g MarketRegionGene, bars []quant.Bar) int64 {
	uniqueWindows := map[int]bool{}
	activeFeatures := 0
	for _, feature := range g.Features {
		if len(feature.Thresholds) == 0 && len(feature.ThresholdRanks) == 0 {
			continue
		}
		activeFeatures++
		uniqueWindows[feature.Window] = true
	}
	barCount := int64(len(bars))
	units := barCount * int64(activeFeatures)
	for window := range uniqueWindows {
		units += 2 * barCount * int64(window)
	}
	return units
}

func marketRegionMaximumProviderUnits(maxWindow int, barCount int) int64 {
	if maxWindow < 2 || barCount <= 0 {
		return 0
	}
	return int64(barCount*len(MarketRegionFeatureIDs)) + 2*int64(barCount)*int64(maxWindow)*int64(len(MarketRegionFeatureIDs))
}

type MarketRegionFeature struct {
	ID         string    `json:"id"`
	Window     int       `json:"window"`
	Thresholds []float64 `json:"thresholds"`
	// ThresholdRanks are the searched coordinates before the exact raw values
	// are materialised.  This lets one candidate use the raw values calculated
	// from its own Window without precomputing every window's full feature
	// sequence and retaining it in memory.
	ThresholdRanks      []int `json:"threshold_ranks,omitempty"`
	ThresholdRankOffset int   `json:"threshold_rank_offset,omitempty"`
}

type MarketRegionPack struct {
	Key        string           `json:"key"`
	Chromosome quant.Chromosome `json:"chromosome"`
}

type MarketRegionGene struct {
	SchemaVersion string `json:"schema_version"`
	// Global holds the non-state-specific candidate dimensions. State packs only
	// override the six values selected by the market-region decision.
	Global quant.Chromosome `json:"global"`
	// DefaultState is used only when a previously unseen state occurs after a
	// candidate has been saved.  Search-time materialisation creates explicit
	// packs for every state actually present in the training data; no Cartesian
	// product is preallocated just because it is theoretically possible.
	DefaultState quant.Chromosome      `json:"default_state"`
	Features     []MarketRegionFeature `json:"features"`
	Packs        []MarketRegionPack    `json:"packs"`
}

// marketRegionParamPack keeps the normal execution settings beside the
// interval candidate. A promoted gene can therefore use the same execution
// path as a static parameter pack.
type marketRegionParamPack struct {
	SchemaVersion     string           `json:"schema_version"`
	Chromosome        quant.Chromosome `json:"sigmoid_dca_config"`
	Spawn             quant.SpawnPoint `json:"spawn_point"`
	PositionStructure string           `json:"position_structure,omitempty"`
	MarketRegion      MarketRegionGene `json:"market_region"`
}

func decodeMarketRegionParamPack(raw []byte) (marketRegionParamPack, bool) {
	var pack marketRegionParamPack
	if json.Unmarshal(raw, &pack) != nil || pack.SchemaVersion != MarketRegionSchemaVersion || pack.MarketRegion.SchemaVersion != MarketRegionSchemaVersion {
		return marketRegionParamPack{}, false
	}
	return pack, true
}

func isMarketRegionGene(g Gene) (MarketRegionGene, bool) {
	v, ok := g.(MarketRegionGene)
	return v, ok
}

func normalizeMarketRegionGene(g MarketRegionGene, options GeneOptions) (MarketRegionGene, error) {
	options = NormalizeGeneOptions(options)
	if g.SchemaVersion == "" {
		g.SchemaVersion = MarketRegionSchemaVersion
	}
	if g.SchemaVersion != MarketRegionSchemaVersion {
		return MarketRegionGene{}, fmt.Errorf("unsupported market region schema %q", g.SchemaVersion)
	}
	if options.MarketRegionMaxThresholds < 0 {
		return MarketRegionGene{}, fmt.Errorf("market region threshold maximum must not be negative")
	}
	if options.MarketRegionMaxWindow < 2 {
		return MarketRegionGene{}, fmt.Errorf("market region window maximum must be at least 2")
	}
	byID := make(map[string]MarketRegionFeature, len(g.Features))
	for _, feature := range g.Features {
		if !marketRegionFeatureKnown(feature.ID) {
			return MarketRegionGene{}, fmt.Errorf("unknown market region feature %q", feature.ID)
		}
		if _, exists := byID[feature.ID]; exists {
			return MarketRegionGene{}, fmt.Errorf("duplicated market region feature %q", feature.ID)
		}
		if feature.Window < 2 || feature.Window > options.MarketRegionMaxWindow {
			return MarketRegionGene{}, fmt.Errorf("feature %s window %d is outside 2..%d", feature.ID, feature.Window, options.MarketRegionMaxWindow)
		}
		if len(feature.Thresholds) > options.MarketRegionMaxThresholds {
			return MarketRegionGene{}, fmt.Errorf("feature %s exceeds global threshold maximum", feature.ID)
		}
		if len(feature.ThresholdRanks) > options.MarketRegionMaxThresholds {
			return MarketRegionGene{}, fmt.Errorf("feature %s exceeds global searched threshold maximum", feature.ID)
		}
		feature.Thresholds = append([]float64(nil), feature.Thresholds...)
		for index, value := range feature.Thresholds {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return MarketRegionGene{}, fmt.Errorf("feature %s has invalid threshold", feature.ID)
			}
			// Market values are raw causal measurements.  They are never rounded
			// to the core-parameter lattice.
			feature.Thresholds[index] = value
		}
		sort.Float64s(feature.Thresholds)
		for i := 1; i < len(feature.Thresholds); i++ {
			if feature.Thresholds[i] <= feature.Thresholds[i-1] {
				return MarketRegionGene{}, fmt.Errorf("feature %s thresholds must be strictly increasing", feature.ID)
			}
		}
		byID[feature.ID] = feature
	}
	g.Features = make([]MarketRegionFeature, 0, len(MarketRegionFeatureIDs))
	for _, id := range MarketRegionFeatureIDs {
		feature, exists := byID[id]
		if !exists {
			return MarketRegionGene{}, fmt.Errorf("missing market region feature %q", id)
		}
		g.Features = append(g.Features, feature)
	}
	// Older saved interval candidates stored a complete chromosome in every
	// pack.  Retain their non-state values as the global part when loading them.
	if g.Global == (quant.Chromosome{}) && len(g.Packs) > 0 {
		g.Global = g.Packs[0].Chromosome
	}
	g.Global = normalizeChromosome(g.Global, marketRegionGlobalOptions(options))
	if err := quant.ValidateChromosome(g.Global); err != nil {
		return MarketRegionGene{}, err
	}

	if g.DefaultState == (quant.Chromosome{}) {
		g.DefaultState = marketRegionStateChromosome(g.Global)
	}
	packByKey := make(map[string]MarketRegionPack, len(g.Packs))
	for _, pack := range g.Packs {
		if _, exists := packByKey[pack.Key]; exists {
			return MarketRegionGene{}, fmt.Errorf("duplicated market region pack %q", pack.Key)
		}
		// A region pack owns only the six state-controlled dimensions. Combine it
		// with the global chromosome before validation so Beta and every disabled
		// dimension cannot leak in from a per-region legacy value.
		combined := normalizeChromosome(combineMarketRegionChromosome(g.Global, pack.Chromosome), options)
		if err := quant.ValidateChromosome(combined); err != nil {
			return MarketRegionGene{}, err
		}
		pack.Chromosome = marketRegionStateChromosome(combined)
		packByKey[pack.Key] = pack
	}
	// Packs are intentionally sparse.  Only state combinations observed in
	// the actual training bars are materialised before evaluation.
	g.Packs = make([]MarketRegionPack, 0, len(packByKey))
	for _, pack := range packByKey {
		g.Packs = append(g.Packs, pack)
	}
	sort.Slice(g.Packs, func(i, j int) bool { return g.Packs[i].Key < g.Packs[j].Key })
	return g, nil
}

// materializeMarketRegionPacks expands only state keys that actually occur in
// the supplied training bars.  This removes the artificial 512 cap without
// allocating the impossible full Cartesian state space.
func materializeMarketRegionPacks(g MarketRegionGene, barSets [][]quant.Bar, cache *MarketRegionFeatureCache) (MarketRegionGene, error) {
	var err error
	g, err = materializeMarketRegionThresholds(g, barSets, cache)
	if err != nil {
		return g, err
	}
	previous := make(map[string]MarketRegionPack, len(g.Packs))
	for _, pack := range g.Packs {
		previous[pack.Key] = pack
	}
	byKey := make(map[string]MarketRegionPack)
	for _, bars := range barSets {
		values, err := marketRegionValuesWithCache(bars, g.Features, cache)
		if err != nil {
			return g, err
		}
		for _, row := range values {
			key, ok := marketRegionKey(g.Features, row)
			if !ok {
				continue
			}
			if _, exists := byKey[key]; exists {
				continue
			}
			// The candidate's state values are the values being searched.  A
			// missing observed state therefore inherits this candidate's current
			// DefaultState, never an unrelated deterministic sample.
			if pack, exists := previous[key]; exists {
				byKey[key] = pack
			} else {
				byKey[key] = MarketRegionPack{Key: key, Chromosome: g.DefaultState}
			}
		}
	}
	g.Packs = g.Packs[:0]
	for _, pack := range byKey {
		g.Packs = append(g.Packs, pack)
	}
	sort.Slice(g.Packs, func(i, j int) bool { return g.Packs[i].Key < g.Packs[j].Key })
	return g, nil
}

// materializeMarketRegionThresholds resolves lattice ranks to exact causal
// feature values from the feature's own selected window.  Raw values are only
// retained for the duration of this candidate materialisation; the bounded LRU
// cache owns any reusable feature series.
func materializeMarketRegionThresholds(g MarketRegionGene, barSets [][]quant.Bar, cache *MarketRegionFeatureCache) (MarketRegionGene, error) {
	for index := range g.Features {
		feature := &g.Features[index]
		if len(feature.ThresholdRanks) == 0 {
			continue
		}
		seen := map[uint64]bool{}
		values := make([]float64, 0)
		for _, bars := range barSets {
			series, err := cache.series(bars, feature.Window)
			if err != nil {
				return g, err
			}
			for row := range bars {
				value, ok := marketRegionFeatureValue(feature.ID, series.activity[row], series.geometry[row])
				if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
					continue
				}
				bits := math.Float64bits(value)
				if !seen[bits] {
					seen[bits] = true
					values = append(values, value)
				}
			}
		}
		sort.Float64s(values)
		feature.Thresholds = thresholdValuesAtRanks(values, feature.ThresholdRanks, feature.ThresholdRankOffset)
	}
	return g, nil
}

func thresholdValuesAtRanks(values []float64, ranks []int, offset int) []float64 {
	if len(values) == 0 || len(ranks) == 0 {
		return nil
	}
	count := len(ranks)
	if count > len(values) {
		count = len(values)
	}
	// ThresholdRankOffset is a deterministic combination index, not a numeric
	// transform of the raw market value. Unranking chooses one exact k-of-n
	// combination, so every decision value remains an original calculated
	// observation and every combination has a stable search coordinate.
	total := binomialSaturated(len(values), count)
	index := uint64(max(0, offset))
	if total > 0 {
		index %= total
	}
	positions := unrankCombination(len(values), count, index)
	result := make([]float64, 0, len(positions))
	for _, position := range positions {
		result = append(result, values[position])
	}
	return result
}

func binomialSaturated(n, k int) uint64 {
	if k < 0 || k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	result := uint64(1)
	for i := 1; i <= k; i++ {
		numerator := uint64(n - k + i)
		if result > math.MaxUint64/numerator {
			return math.MaxUint64
		}
		result = result * numerator / uint64(i)
	}
	return result
}

func unrankCombination(n, k int, rank uint64) []int {
	out := make([]int, 0, k)
	next := 0
	for remaining := k; remaining > 0; remaining-- {
		for candidate := next; candidate <= n-remaining; candidate++ {
			block := binomialSaturated(n-candidate-1, remaining-1)
			if rank < block {
				out = append(out, candidate)
				next = candidate + 1
				break
			}
			rank -= block
		}
	}
	return out
}

func marketRegionFeatureKnown(id string) bool {
	for _, known := range MarketRegionFeatureIDs {
		if id == known {
			return true
		}
	}
	return false
}

func marketRegionKeys(features []MarketRegionFeature) []string {
	keys := []string{""}
	for _, feature := range features {
		next := make([]string, 0, len(keys)*(len(feature.Thresholds)+1))
		for _, prefix := range keys {
			for region := 0; region <= len(feature.Thresholds); region++ {
				part := fmt.Sprintf("%s=%d", feature.ID, region)
				if prefix != "" {
					part = prefix + ";" + part
				}
				next = append(next, part)
			}
		}
		keys = next
	}
	return keys
}

func marketRegionKey(features []MarketRegionFeature, values map[string]float64) (string, bool) {
	parts := make([]string, 0, len(features))
	for _, feature := range features {
		if len(feature.Thresholds) == 0 {
			parts = append(parts, fmt.Sprintf("%s=0", feature.ID))
			continue
		}
		value, available := values[feature.ID]
		if !available || math.IsNaN(value) || math.IsInf(value, 0) {
			return "", false
		}
		region := sort.SearchFloat64s(feature.Thresholds, value)
		parts = append(parts, fmt.Sprintf("%s=%d", feature.ID, region))
	}
	return strings.Join(parts, ";"), true
}

func marketRegionValues(bars []quant.Bar, features []MarketRegionFeature) ([]map[string]float64, error) {
	return marketRegionValuesWithCache(bars, features, nil)
}

func marketRegionValuesWithCache(bars []quant.Bar, features []MarketRegionFeature, cache *MarketRegionFeatureCache) ([]map[string]float64, error) {
	result := make([]map[string]float64, len(bars))
	for i := range result {
		result[i] = map[string]float64{}
	}
	byWindow := map[int][]MarketRegionFeature{}
	for _, feature := range features {
		if len(feature.Thresholds) == 0 {
			continue
		}
		byWindow[feature.Window] = append(byWindow[feature.Window], feature)
	}
	for window, group := range byWindow {
		series, err := cache.series(bars, window)
		if err != nil {
			return nil, err
		}
		for index := range bars {
			for _, feature := range group {
				if value, ok := marketRegionFeatureValue(feature.ID, series.activity[index], series.geometry[index]); ok {
					result[index][feature.ID] = value
				}
			}
		}
	}
	return result, nil
}

func marketRegionFeatureRanges(bars []quant.Bar, window int) (map[string][2]float64, error) {
	return marketRegionFeatureRangesWithProgress(bars, window, nil)
}

func marketRegionFeatureRangesWithProgress(bars []quant.Bar, window int, progress func(int64)) (map[string][2]float64, error) {
	ranges, _, err := marketRegionFeatureRangesAndValuesWithProgress(bars, window, progress)
	return ranges, err
}

// marketRegionFeatureValuesWithProgress returns every distinct raw calculated
// value in ascending order. The search uses these exact values as threshold
// candidates; it never replaces them with normalised scores or synthetic
// endpoints.
func marketRegionFeatureValuesWithProgress(bars []quant.Bar, window int, progress func(int64)) (map[string][]float64, error) {
	_, values, err := marketRegionFeatureRangesAndValuesWithProgress(bars, window, progress)
	return values, err
}

func marketRegionFeatureRangesAndValuesWithProgress(bars []quant.Bar, window int, progress func(int64)) (map[string][2]float64, map[string][]float64, error) {
	activity, err := dynamicparam.BuildFeaturePointsWithoutRawSequence(bars, window)
	if err != nil {
		return nil, nil, err
	}
	if progress != nil {
		progress(int64(len(bars) * window))
	}
	geometry, err := dynamicparam.BuildGeometryFeaturesWithProgress(bars, window, progress)
	if err != nil {
		return nil, nil, err
	}
	ranges := make(map[string][2]float64, len(MarketRegionFeatureIDs))
	valuesByID := make(map[string][]float64, len(MarketRegionFeatureIDs))
	seen := make(map[string]map[uint64]bool, len(MarketRegionFeatureIDs))
	for _, id := range MarketRegionFeatureIDs {
		seen[id] = map[uint64]bool{}
		minimum, maximum, found := 0.0, 0.0, false
		for index := range bars {
			value, ok := marketRegionFeatureValue(id, activity[index], geometry[index])
			if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			if !found || value < minimum {
				minimum = value
			}
			if !found || value > maximum {
				maximum = value
			}
			bits := math.Float64bits(value)
			if !seen[id][bits] {
				seen[id][bits] = true
				valuesByID[id] = append(valuesByID[id], value)
			}
			found = true
		}
		if found && maximum > minimum {
			ranges[id] = [2]float64{minimum, maximum}
		}
		sort.Float64s(valuesByID[id])
	}
	return ranges, valuesByID, nil
}

func marketRegionFeatureValue(id string, activity dynamicparam.FeaturePoint, geometry dynamicparam.GeometryPoint) (float64, bool) {
	if id == "geometry_coverage_area" {
		return geometry.CoverageArea, geometry.Available
	}
	if id == "close_trend_slope" {
		return geometry.TrendSlope, geometry.Available
	}
	if !activity.Available {
		return 0, false
	}
	v := activity.Activity
	if strings.HasSuffix(id, "_history_ratio") {
		v = activity.HistoryRatio
		id = strings.TrimSuffix(id, "_history_ratio")
	}
	switch id {
	case "tr_mean":
		return v.TRMean, true
	case "tr_std_dev":
		return v.TRStdDev, true
	case "high_low_mean":
		return v.HighLowMean, true
	case "high_low_std_dev":
		return v.HighLowStdDev, true
	case "parkinson":
		return v.Parkinson, true
	case "yang_zhang":
		return v.YangZhang, true
	}
	return 0, false
}

func newMarketRegionProvider(g MarketRegionGene, bars []quant.Bar) (backtestcore.ParameterProvider, error) {
	return newMarketRegionProviderWithCache(g, bars, nil)
}

func newMarketRegionProviderWithCache(g MarketRegionGene, bars []quant.Bar, cache *MarketRegionFeatureCache) (backtestcore.ParameterProvider, error) {
	values, err := marketRegionValuesWithCache(bars, g.Features, cache)
	if err != nil {
		return nil, err
	}
	global := marketRegionGlobalChromosome(g)
	packs := make(map[string]quant.Chromosome, len(g.Packs))
	for _, pack := range g.Packs {
		packs[pack.Key] = combineMarketRegionChromosome(global, pack.Chromosome)
	}
	selected := make([]quant.Chromosome, len(values))
	selectedStates := make([]string, len(values))
	fallbackKey := marketRegionFallbackKey(g.Features)
	for index, row := range values {
		key, ok := marketRegionKey(g.Features, row)
		if !ok {
			key = fallbackKey
		}
		chromosome, exists := packs[key]
		if !exists {
			chromosome = combineMarketRegionChromosome(global, g.DefaultState)
		}
		selected[index] = chromosome
		selectedStates[index] = key
	}
	return func(context backtestcore.ParameterContext) (backtestcore.EffectiveParameters, error) {
		if context.Index < 0 || context.Index >= len(values) {
			return backtestcore.EffectiveParameters{}, fmt.Errorf("market region index %d is unavailable", context.Index)
		}
		return backtestcore.EffectiveParameters{Chromosome: selected[context.Index], Metadata: backtestcore.ParameterMetadata{StructureState: selectedStates[context.Index], ModelVersion: MarketRegionSchemaVersion}}, nil
	}, nil
}

// MarketRegionParameterProvider exposes the same causal interval selection
// used by GA evaluation to ordinary backtests that receive a stored JSON pack.
func MarketRegionParameterProvider(raw []byte, bars []quant.Bar) (backtestcore.ParameterProvider, bool, error) {
	return MarketRegionParameterProviderWithCache(raw, bars, nil)
}

// MarketRegionParameterProviderWithCache is identical to
// MarketRegionParameterProvider but reuses immutable exact feature series in a
// caller-owned task cache.
func MarketRegionParameterProviderWithCache(raw []byte, bars []quant.Bar, cache *MarketRegionFeatureCache) (backtestcore.ParameterProvider, bool, error) {
	pack, ok := decodeMarketRegionParamPack(raw)
	if !ok {
		return nil, false, nil
	}
	provider, err := newMarketRegionProviderWithCache(pack.MarketRegion, bars, cache)
	if err != nil {
		return nil, true, err
	}
	return provider, true, nil
}

func marketRegionChromosome(g MarketRegionGene, bars []quant.Bar, index int) (quant.Chromosome, string, error) {
	values, err := marketRegionValues(bars, g.Features)
	if err != nil {
		return quant.Chromosome{}, "", err
	}
	if index < 0 || index >= len(values) {
		return quant.Chromosome{}, "", fmt.Errorf("market region index %d is unavailable", index)
	}
	key, ok := marketRegionKey(g.Features, values[index])
	if !ok {
		key = marketRegionFallbackKey(g.Features)
	}
	for _, pack := range g.Packs {
		if pack.Key == key {
			return combineMarketRegionChromosome(marketRegionGlobalChromosome(g), pack.Chromosome), key, nil
		}
	}
	return combineMarketRegionChromosome(marketRegionGlobalChromosome(g), g.DefaultState), key, nil
}

func marketRegionFallbackKey(features []MarketRegionFeature) string {
	parts := make([]string, 0, len(features))
	for _, feature := range features {
		parts = append(parts, fmt.Sprintf("%s=0", feature.ID))
	}
	return strings.Join(parts, ";")
}

func marketRegionGlobalChromosome(g MarketRegionGene) quant.Chromosome {
	if g.Global != (quant.Chromosome{}) {
		return g.Global
	}
	if len(g.Packs) > 0 {
		return g.Packs[0].Chromosome
	}
	return quant.DefaultSeedChromosome
}

// ResolveMarketRegionParams converts a stored candidate into the parameters
// for the most recently completed bar. The caller supplies completed bars, so
// this function cannot inspect future data.
func ResolveMarketRegionParams(raw []byte, bars []quant.Bar) (sigmoiddca.Params, bool, error) {
	pack, ok := decodeMarketRegionParamPack(raw)
	if !ok {
		return sigmoiddca.Params{}, false, nil
	}
	chromosome, _, err := marketRegionChromosome(pack.MarketRegion, bars, len(bars)-1)
	if err != nil {
		return sigmoiddca.Params{}, true, err
	}
	params := sigmoiddca.ParseParamsFromParamPack(raw)
	params.Chromosome = quant.ClampChromosome(chromosome)
	return params, true, nil
}

func marketRegionFingerprint(g MarketRegionGene) uint64 {
	canonical := cloneMarketRegionGene(g)
	for index := range canonical.Features {
		// Ranks and the combination cursor are scheduler coordinates only. Once
		// exact raw thresholds are materialised, two coordinates resolving to
		// the same thresholds are the same executable parameter candidate.
		canonical.Features[index].ThresholdRanks = nil
		canonical.Features[index].ThresholdRankOffset = 0
		if len(canonical.Features[index].Thresholds) == 0 {
			canonical.Features[index].Window = 2
		}
	}
	raw, _ := jsonMarshalMarketRegion(canonical)
	sum := sha256.Sum256(raw)
	return binaryLittleEndianUint64(sum[:8])
}

func jsonMarshalMarketRegion(g MarketRegionGene) ([]byte, error) { return json.Marshal(g) }
func binaryLittleEndianUint64(raw []byte) uint64 {
	return uint64(raw[0]) | uint64(raw[1])<<8 | uint64(raw[2])<<16 | uint64(raw[3])<<24 | uint64(raw[4])<<32 | uint64(raw[5])<<40 | uint64(raw[6])<<48 | uint64(raw[7])<<56
}
func marketRegionHash(g MarketRegionGene) string {
	raw, _ := jsonMarshalMarketRegion(g)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
