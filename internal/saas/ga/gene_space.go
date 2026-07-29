package ga

import (
	"encoding/binary"
	"hash/fnv"
	"math"

	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

const (
	CoreCandidateSchemaVersion = "ga-core-grid-v1"
	CoreSearchGridStep         = 0.05
)

type ParameterState string

const (
	ParameterStateEvolving ParameterState = "evolving"
	ParameterStateFixed    ParameterState = "fixed"
	ParameterStateDisabled ParameterState = "disabled"
)

type ParameterAxis struct {
	Key      string         `json:"key"`
	Label    string         `json:"label"`
	Kind     string         `json:"kind"`
	Minimum  float64        `json:"minimum"`
	Maximum  float64        `json:"maximum"`
	Step     float64        `json:"step"`
	State    ParameterState `json:"state"`
	Value    float64        `json:"value"`
	GridSize int            `json:"grid_size"`
}

type coreField struct {
	key   string
	label string
	kind  string
}

var coreFields = []coreField{
	{key: "micro_reserve_pct", label: "資金保留比例", kind: "float"},
	{key: "beta", label: "訊號反應係數", kind: "float"},
	{key: "gamma", label: "倉位回饋係數", kind: "float"},
	{key: "w_mean", label: "均值訊號權重", kind: "float"},
	{key: "w_momentum", label: "動能訊號權重", kind: "float"},
	{key: "w_breakout", label: "突破訊號權重", kind: "float"},
	{key: "dust_usd", label: "最小交易金額", kind: "float"},
	{key: "rebalance_threshold", label: "調倉門檻", kind: "float"},
	{key: "force_full_threshold", label: "強制滿倉門檻", kind: "float"},
	{key: "force_empty_threshold", label: "強制空倉門檻", kind: "float"},
	{key: "wedge_delta_threshold", label: "幾何變化門檻", kind: "float"},
	{key: "wedge_vol_ratio_threshold", label: "幾何活動門檻", kind: "float"},
	{key: "macro_bear_multiplier", label: "偏空投入倍率", kind: "float"},
	{key: "macro_bull_multiplier", label: "偏多投入倍率", kind: "float"},
	{key: "extra_deploy_pct", label: "額外投入比例", kind: "float"},
	{key: "soft_release_months", label: "緩釋月數", kind: "int"},
	{key: "soft_release_pct", label: "緩釋比例", kind: "float"},
	{key: "hard_release_max_pct", label: "硬釋放上限", kind: "float"},
}

var floatingOnlyDisabledFields = map[string]bool{
	"micro_reserve_pct":         true,
	"dust_usd":                  true,
	"wedge_delta_threshold":     true,
	"wedge_vol_ratio_threshold": true,
	"macro_bear_multiplier":     true,
	"macro_bull_multiplier":     true,
	"extra_deploy_pct":          true,
	"soft_release_months":       true,
	"soft_release_pct":          true,
	"hard_release_max_pct":      true,
}

func ParameterAxes(options GeneOptions) []ParameterAxis {
	options = NormalizeGeneOptions(options)
	base := quant.DefaultSeedChromosome
	if options.FixedGene != nil {
		base = *options.FixedGene
	}
	base = normalizeChromosomeForOptions(base, options)
	axes := make([]ParameterAxis, 0, len(coreFields))
	for _, field := range coreFields {
		bound := quant.HardBounds[field.key]
		step := CoreSearchGridStep
		if field.kind == "int" {
			step = 1
		}
		state := parameterState(field.key, options)
		gridSize := legalGridSize(field.key, field.kind)
		if state != ParameterStateEvolving {
			gridSize = 1
		}
		axes = append(axes, ParameterAxis{
			Key:      field.key,
			Label:    field.label,
			Kind:     field.kind,
			Minimum:  bound.Min,
			Maximum:  bound.Max,
			Step:     step,
			State:    state,
			Value:    chromosomeValue(base, field.key),
			GridSize: gridSize,
		})
	}
	return axes
}

func parameterState(key string, options GeneOptions) ParameterState {
	options = NormalizeGeneOptions(options)
	if options.PositionStructure == sigmoiddca.PositionStructureFloatingOnly && floatingOnlyDisabledFields[key] {
		return ParameterStateDisabled
	}
	if key == "dust_usd" && options.DisableMinimumTrade {
		return ParameterStateDisabled
	}
	if containsString(options.FixedParamKeys, key) {
		return ParameterStateFixed
	}
	switch key {
	case "gamma":
		if !options.EvolveGamma {
			return ParameterStateFixed
		}
	case "rebalance_threshold":
		if !options.EvolveRebalanceThreshold {
			return ParameterStateFixed
		}
	case "force_full_threshold":
		if !options.EvolveForceFullThreshold {
			return ParameterStateFixed
		}
	case "force_empty_threshold":
		if !options.EvolveForceEmptyThreshold {
			return ParameterStateFixed
		}
	case "w_mean":
		if !options.EnableWMean {
			return ParameterStateDisabled
		}
	case "w_momentum":
		if !options.EnableWMomentum {
			return ParameterStateDisabled
		}
	case "w_breakout":
		if !options.EnableWBreakout {
			return ParameterStateDisabled
		}
	}
	return ParameterStateEvolving
}

func normalizeChromosomeForOptions(input quant.Chromosome, options GeneOptions) quant.Chromosome {
	options = NormalizeGeneOptions(options)
	c := quant.ClampChromosome(input)
	neutral := quant.DefaultSeedChromosome

	for _, field := range coreFields {
		if parameterState(field.key, options) == ParameterStateDisabled {
			setChromosomeValue(&c, field.key, chromosomeValue(neutral, field.key))
		}
	}
	if !options.EnableWMean {
		c.WMean = 0
	}
	if !options.EnableWMomentum {
		c.WMomentum = 0
	}
	if !options.EnableWBreakout {
		c.WBreakout = 0
	}
	if !options.EvolveGamma {
		c.Gamma = 0
	}
	if options.PositionStructure == sigmoiddca.PositionStructureFloatingOnly {
		c.MacroBearMultiplier = 1
		c.MacroBullMultiplier = 1
		c.ExtraDeployPct = 0
		c.SoftReleaseMonths = int(quant.HardBounds["soft_release_months"].Max)
		c.SoftReleasePct = 0
		c.HardReleaseMaxPct = 0
	}
	if options.DisableMinimumTrade || options.PositionStructure == sigmoiddca.PositionStructureFloatingOnly {
		c.DustUSD = neutral.DustUSD
		c.WedgeDeltaThreshold = neutral.WedgeDeltaThreshold
		c.WedgeVolRatioThreshold = neutral.WedgeVolRatioThreshold
	}
	if options.FixedGene != nil {
		for _, key := range options.FixedParamKeys {
			if parameterState(key, options) == ParameterStateFixed {
				setChromosomeValue(&c, key, chromosomeValue(*options.FixedGene, key))
			}
		}
	}

	return quant.ClampChromosome(c)
}

func sampleChromosome(rng RandomSource, options GeneOptions) quant.Chromosome {
	options = NormalizeGeneOptions(options)
	c := normalizeChromosomeForOptions(quant.DefaultSeedChromosome, options)
	for _, field := range coreFields {
		if parameterState(field.key, options) != ParameterStateEvolving {
			continue
		}
		switch field.key {
		case "force_full_threshold", "force_empty_threshold", "macro_bear_multiplier", "macro_bull_multiplier":
			continue
		}
		setChromosomeValue(&c, field.key, sampleGridValue(rng, field.key, field.kind))
	}
	sampleOrderedPair(rng, &c, options, "force_empty_threshold", "force_full_threshold")
	sampleOrderedPair(rng, &c, options, "macro_bull_multiplier", "macro_bear_multiplier")
	return normalizeChromosomeForOptions(c, options)
}

func mutateChromosome(input quant.Chromosome, prob float64, scale float64, rng RandomSource, options GeneOptions) quant.Chromosome {
	options = NormalizeGeneOptions(options)
	c := normalizeChromosomeForOptions(input, options)
	for _, field := range coreFields {
		if parameterState(field.key, options) != ParameterStateEvolving {
			continue
		}
		switch field.key {
		case "force_full_threshold", "force_empty_threshold", "macro_bear_multiplier", "macro_bull_multiplier":
			continue
		}
		if rng.Float64() < prob {
			setChromosomeValue(&c, field.key, mutateGridValue(chromosomeValue(c, field.key), field.key, field.kind, scale, rng))
		}
	}
	mutateOrderedPair(&c, options, "force_empty_threshold", "force_full_threshold", prob, scale, rng)
	mutateOrderedPair(&c, options, "macro_bull_multiplier", "macro_bear_multiplier", prob, scale, rng)
	return normalizeChromosomeForOptions(c, options)
}

func crossoverChromosome(a quant.Chromosome, b quant.Chromosome, rng RandomSource, options GeneOptions) quant.Chromosome {
	options = NormalizeGeneOptions(options)
	a = normalizeChromosomeForOptions(a, options)
	b = normalizeChromosomeForOptions(b, options)
	c := a
	for _, field := range coreFields {
		if parameterState(field.key, options) != ParameterStateEvolving {
			continue
		}
		switch field.key {
		case "force_full_threshold", "force_empty_threshold", "macro_bear_multiplier", "macro_bull_multiplier":
			continue
		}
		if rng.Float64() < 0.5 {
			setChromosomeValue(&c, field.key, chromosomeValue(b, field.key))
		}
	}
	if rng.Float64() < 0.5 {
		copyOrderedPair(&c, b, options, "force_empty_threshold", "force_full_threshold")
	}
	if rng.Float64() < 0.5 {
		copyOrderedPair(&c, b, options, "macro_bull_multiplier", "macro_bear_multiplier")
	}
	return normalizeChromosomeForOptions(c, options)
}

func fingerprintChromosome(c quant.Chromosome, options GeneOptions) uint64 {
	options = NormalizeGeneOptions(options)
	c = normalizeChromosomeForOptions(c, options)
	h := fnv.New64a()
	_, _ = h.Write([]byte(CoreCandidateSchemaVersion))
	for _, field := range coreFields {
		state := parameterState(field.key, options)
		if state == ParameterStateDisabled {
			continue
		}
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(field.key))
		var buf [8]byte
		value := chromosomeValue(c, field.key)
		if state == ParameterStateEvolving {
			binary.LittleEndian.PutUint64(buf[:], uint64(gridCoordinate(field.key, field.kind, value)))
		} else {
			binary.LittleEndian.PutUint64(buf[:], math.Float64bits(value))
		}
		_, _ = h.Write(buf[:])
	}
	return h.Sum64()
}

func sampleOrderedPair(rng RandomSource, c *quant.Chromosome, options GeneOptions, lowerKey, upperKey string) {
	lowerState := parameterState(lowerKey, options)
	upperState := parameterState(upperKey, options)
	lower := chromosomeValue(*c, lowerKey)
	upper := chromosomeValue(*c, upperKey)
	switch {
	case lowerState == ParameterStateEvolving && upperState == ParameterStateEvolving:
		lower = sampleGridValue(rng, lowerKey, "float")
		upper = sampleGridValueBetween(rng, upperKey, lower, quant.HardBounds[upperKey].Max)
	case lowerState == ParameterStateEvolving:
		lower = sampleGridValueBetween(rng, lowerKey, quant.HardBounds[lowerKey].Min, upper)
	case upperState == ParameterStateEvolving:
		upper = sampleGridValueBetween(rng, upperKey, lower, quant.HardBounds[upperKey].Max)
	}
	setChromosomeValue(c, lowerKey, lower)
	setChromosomeValue(c, upperKey, upper)
}

func mutateOrderedPair(c *quant.Chromosome, options GeneOptions, lowerKey, upperKey string, prob, scale float64, rng RandomSource) {
	lower := chromosomeValue(*c, lowerKey)
	upper := chromosomeValue(*c, upperKey)
	if parameterState(lowerKey, options) == ParameterStateEvolving && rng.Float64() < prob {
		lower = mutateGridValueBetween(lower, lowerKey, "float", quant.HardBounds[lowerKey].Min, upper, scale, rng)
	}
	if parameterState(upperKey, options) == ParameterStateEvolving && rng.Float64() < prob {
		upper = mutateGridValueBetween(upper, upperKey, "float", lower, quant.HardBounds[upperKey].Max, scale, rng)
	}
	setChromosomeValue(c, lowerKey, lower)
	setChromosomeValue(c, upperKey, upper)
}

func copyOrderedPair(target *quant.Chromosome, source quant.Chromosome, options GeneOptions, lowerKey, upperKey string) {
	if parameterState(lowerKey, options) == ParameterStateEvolving {
		setChromosomeValue(target, lowerKey, chromosomeValue(source, lowerKey))
	}
	if parameterState(upperKey, options) == ParameterStateEvolving {
		setChromosomeValue(target, upperKey, chromosomeValue(source, upperKey))
	}
}

func sampleGridValue(rng RandomSource, key, kind string) float64 {
	bound := quant.HardBounds[key]
	return sampleGridValueBetween(rng, key, bound.Min, bound.Max)
}

func sampleGridValueBetween(rng RandomSource, key string, minimum, maximum float64) float64 {
	kind := fieldKind(key)
	minimumIndex, maximumIndex := legalGridIndexRange(minimum, maximum, kind)
	if maximumIndex < minimumIndex {
		return minimum
	}
	return gridValue(kind, minimumIndex+int64(rng.Intn(int(maximumIndex-minimumIndex+1))))
}

func mutateGridValue(value float64, key, kind string, scale float64, rng RandomSource) float64 {
	bound := quant.HardBounds[key]
	return mutateGridValueBetween(value, key, kind, bound.Min, bound.Max, scale, rng)
}

func mutateGridValueBetween(value float64, key, kind string, minimum, maximum, scale float64, rng RandomSource) float64 {
	minimumIndex, maximumIndex := legalGridIndexRange(minimum, maximum, kind)
	current := gridCoordinate(key, kind, value)
	baseStep := quant.GeneSteps[key]
	if kind == "int" {
		baseStep = math.Max(1, baseStep)
	} else {
		baseStep = math.Max(CoreSearchGridStep, baseStep)
	}
	unit := 1.0
	if kind == "int" {
		unit = 1
	} else {
		unit = CoreSearchGridStep
	}
	delta := int64(math.Round(rng.NormFloat64() * (baseStep / unit) * math.Max(scale, 0.1)))
	if delta == 0 {
		if rng.Float64() < 0.5 {
			delta = -1
		} else {
			delta = 1
		}
	}
	next := current + delta
	if next < minimumIndex {
		next = minimumIndex
	}
	if next > maximumIndex {
		next = maximumIndex
	}
	return gridValue(kind, next)
}

func gridCoordinate(key, kind string, value float64) int64 {
	if kind == "" {
		kind = fieldKind(key)
	}
	if kind == "int" {
		return int64(math.Round(value))
	}
	return int64(math.Round(value / CoreSearchGridStep))
}

func gridValue(kind string, coordinate int64) float64 {
	if kind == "int" {
		return float64(coordinate)
	}
	return float64(coordinate) * CoreSearchGridStep
}

func legalGridIndexRange(minimum, maximum float64, kind string) (int64, int64) {
	if kind == "int" {
		return int64(math.Ceil(minimum)), int64(math.Floor(maximum))
	}
	return int64(math.Ceil((minimum - 1e-12) / CoreSearchGridStep)),
		int64(math.Floor((maximum + 1e-12) / CoreSearchGridStep))
}

func legalGridSize(key, kind string) int {
	bound := quant.HardBounds[key]
	minimum, maximum := legalGridIndexRange(bound.Min, bound.Max, kind)
	if maximum < minimum {
		return 0
	}
	return int(maximum-minimum) + 1
}

func fieldKind(key string) string {
	for _, field := range coreFields {
		if field.key == key {
			return field.kind
		}
	}
	return "float"
}

func chromosomeValue(c quant.Chromosome, key string) float64 {
	switch key {
	case "micro_reserve_pct":
		return c.MicroReservePct
	case "beta":
		return c.Beta
	case "gamma":
		return c.Gamma
	case "w_mean":
		return c.WMean
	case "w_momentum":
		return c.WMomentum
	case "w_breakout":
		return c.WBreakout
	case "dust_usd":
		return c.DustUSD
	case "rebalance_threshold":
		return c.RebalanceThreshold
	case "force_full_threshold":
		return c.ForceFullThreshold
	case "force_empty_threshold":
		return c.ForceEmptyThreshold
	case "wedge_delta_threshold":
		return c.WedgeDeltaThreshold
	case "wedge_vol_ratio_threshold":
		return c.WedgeVolRatioThreshold
	case "macro_bear_multiplier":
		return c.MacroBearMultiplier
	case "macro_bull_multiplier":
		return c.MacroBullMultiplier
	case "extra_deploy_pct":
		return c.ExtraDeployPct
	case "soft_release_months":
		return float64(c.SoftReleaseMonths)
	case "soft_release_pct":
		return c.SoftReleasePct
	case "hard_release_max_pct":
		return c.HardReleaseMaxPct
	default:
		return 0
	}
}

func setChromosomeValue(c *quant.Chromosome, key string, value float64) {
	switch key {
	case "micro_reserve_pct":
		c.MicroReservePct = value
	case "beta":
		c.Beta = value
	case "gamma":
		c.Gamma = value
	case "w_mean":
		c.WMean = value
	case "w_momentum":
		c.WMomentum = value
	case "w_breakout":
		c.WBreakout = value
	case "dust_usd":
		c.DustUSD = value
	case "rebalance_threshold":
		c.RebalanceThreshold = value
	case "force_full_threshold":
		c.ForceFullThreshold = value
	case "force_empty_threshold":
		c.ForceEmptyThreshold = value
	case "wedge_delta_threshold":
		c.WedgeDeltaThreshold = value
	case "wedge_vol_ratio_threshold":
		c.WedgeVolRatioThreshold = value
	case "macro_bear_multiplier":
		c.MacroBearMultiplier = value
	case "macro_bull_multiplier":
		c.MacroBullMultiplier = value
	case "extra_deploy_pct":
		c.ExtraDeployPct = value
	case "soft_release_months":
		c.SoftReleaseMonths = int(math.Round(value))
	case "soft_release_pct":
		c.SoftReleasePct = value
	case "hard_release_max_pct":
		c.HardReleaseMaxPct = value
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
