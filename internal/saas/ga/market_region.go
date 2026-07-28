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

const maxMarketRegionPacks = 512

// marketRegionProviderUnits is a stable work-unit estimate for materialising
// causal activity and geometry series. Each distinct window needs one activity
// pass and one geometry pass; the per-bar selection itself is linear in the
// number of approved market values.
func marketRegionProviderUnits(g MarketRegionGene, bars []quant.Bar) int64 {
	uniqueWindows := map[int]bool{}
	for _, feature := range g.Features {
		uniqueWindows[feature.Window] = true
	}
	barCount := int64(len(bars))
	units := barCount * int64(len(g.Features))
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
}

type MarketRegionPack struct {
	Key        string           `json:"key"`
	Chromosome quant.Chromosome `json:"chromosome"`
}

type MarketRegionGene struct {
	SchemaVersion string                `json:"schema_version"`
	Features      []MarketRegionFeature `json:"features"`
	Packs         []MarketRegionPack    `json:"packs"`
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
		feature.Thresholds = append([]float64(nil), feature.Thresholds...)
		for _, value := range feature.Thresholds {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return MarketRegionGene{}, fmt.Errorf("feature %s has invalid threshold", feature.ID)
			}
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
	keys := marketRegionKeys(g.Features)
	if len(keys) > maxMarketRegionPacks {
		return MarketRegionGene{}, fmt.Errorf("market region creates %d parameter packs; maximum is %d", len(keys), maxMarketRegionPacks)
	}
	packByKey := make(map[string]MarketRegionPack, len(g.Packs))
	for _, pack := range g.Packs {
		if _, exists := packByKey[pack.Key]; exists {
			return MarketRegionGene{}, fmt.Errorf("duplicated market region pack %q", pack.Key)
		}
		pack.Chromosome = normalizeChromosome(pack.Chromosome, options)
		if err := quant.ValidateChromosome(pack.Chromosome); err != nil {
			return MarketRegionGene{}, err
		}
		packByKey[pack.Key] = pack
	}
	g.Packs = make([]MarketRegionPack, 0, len(keys))
	for _, key := range keys {
		pack, exists := packByKey[key]
		if !exists {
			return MarketRegionGene{}, fmt.Errorf("missing market region pack %q", key)
		}
		g.Packs = append(g.Packs, pack)
	}
	return g, nil
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
	result := make([]map[string]float64, len(bars))
	for i := range result {
		result[i] = map[string]float64{}
	}
	byWindow := map[int][]MarketRegionFeature{}
	for _, feature := range features {
		byWindow[feature.Window] = append(byWindow[feature.Window], feature)
	}
	for window, group := range byWindow {
		activity, err := dynamicparam.BuildFeaturePoints(bars, window)
		if err != nil {
			return nil, err
		}
		geometry, err := dynamicparam.BuildGeometryFeatures(bars, window)
		if err != nil {
			return nil, err
		}
		for index := range bars {
			for _, feature := range group {
				if value, ok := marketRegionFeatureValue(feature.ID, activity[index], geometry[index]); ok {
					result[index][feature.ID] = value
				}
			}
		}
	}
	return result, nil
}

func marketRegionFeatureRanges(bars []quant.Bar, window int) (map[string][2]float64, error) {
	features := make([]MarketRegionFeature, 0, len(MarketRegionFeatureIDs))
	for _, id := range MarketRegionFeatureIDs {
		features = append(features, MarketRegionFeature{ID: id, Window: window})
	}
	values, err := marketRegionValues(bars, features)
	if err != nil {
		return nil, err
	}
	ranges := make(map[string][2]float64, len(features))
	for _, id := range MarketRegionFeatureIDs {
		minimum, maximum, found := 0.0, 0.0, false
		for _, row := range values {
			value, ok := row[id]
			if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			if !found || value < minimum {
				minimum = value
			}
			if !found || value > maximum {
				maximum = value
			}
			found = true
		}
		if found && maximum > minimum {
			ranges[id] = [2]float64{minimum, maximum}
		}
	}
	return ranges, nil
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
	values, err := marketRegionValues(bars, g.Features)
	if err != nil {
		return nil, err
	}
	packs := make(map[string]quant.Chromosome, len(g.Packs))
	for _, pack := range g.Packs {
		packs[pack.Key] = pack.Chromosome
	}
	return func(context backtestcore.ParameterContext) (backtestcore.EffectiveParameters, error) {
		if context.Index < 0 || context.Index >= len(values) {
			return backtestcore.EffectiveParameters{}, fmt.Errorf("market region index %d is unavailable", context.Index)
		}
		key, ok := marketRegionKey(g.Features, values[context.Index])
		if !ok {
			key = marketRegionKeys(g.Features)[0]
		}
		chromosome, ok := packs[key]
		if !ok {
			return backtestcore.EffectiveParameters{}, fmt.Errorf("market region pack %q is missing", key)
		}
		return backtestcore.EffectiveParameters{Chromosome: chromosome, Metadata: backtestcore.ParameterMetadata{StructureState: key, ModelVersion: MarketRegionSchemaVersion}}, nil
	}, nil
}

// MarketRegionParameterProvider exposes the same causal interval selection
// used by GA evaluation to ordinary backtests that receive a stored JSON pack.
func MarketRegionParameterProvider(raw []byte, bars []quant.Bar) (backtestcore.ParameterProvider, bool, error) {
	pack, ok := decodeMarketRegionParamPack(raw)
	if !ok {
		return nil, false, nil
	}
	provider, err := newMarketRegionProvider(pack.MarketRegion, bars)
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
		key = marketRegionKeys(g.Features)[0]
	}
	for _, pack := range g.Packs {
		if pack.Key == key {
			return pack.Chromosome, key, nil
		}
	}
	return quant.Chromosome{}, "", fmt.Errorf("market region pack %q is missing", key)
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
	raw, _ := jsonMarshalMarketRegion(g)
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
