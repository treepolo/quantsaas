package ga

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

const (
	executionModeCloseSameBar  = "close_same_bar"
	executionModeCloseNextOpen = "close_next_open"
	executionModePreclose10m   = "preclose_10m"
	searchParameterStep        = 0.05
)

type SigmoidDCAEvolvable struct{}

type BacktestPoint = backtestcore.NAVPoint

type SigmoidDCAPathResult struct {
	Metrics BacktestMetrics
	NAV     []BacktestPoint
	Err     error
}

func NewSigmoidDCAEvolvable() SigmoidDCAEvolvable {
	return SigmoidDCAEvolvable{}
}

func NormalizeGeneOptions(options GeneOptions) GeneOptions {
	if options.PositionStructure == "" {
		options.PositionStructure = sigmoiddca.PositionStructureDualLayer
	}
	options.PositionStructure = sigmoiddca.NormalizePositionStructure(options.PositionStructure)
	if !options.EnableWMean && !options.EnableWMomentum && !options.EnableWBreakout {
		options.EnableWMean = true
		options.EnableWMomentum = true
		options.EnableWBreakout = true
	}
	options.FixedParamKeys = NormalizeFixedParamKeys(options.FixedParamKeys)
	if options.MarketRegionEnabled && options.MarketRegionMaxThresholds < 0 {
		options.MarketRegionMaxThresholds = 0
	}
	if options.MarketRegionEnabled {
		options.DisableBeta = true
		// Region selection owns the complete micro decision. A global cash reserve
		// or rebalance gate would otherwise change the strategy outside the
		// selected interval parameter pack.
		options.EvolveRebalanceThreshold = false
	}
	if options.PositionStructure == sigmoiddca.PositionStructureFloatingOnly {
		options.DisableWedgeMinimum = true
		options.EvolveRebalanceThreshold = false
	}
	return options
}

func NormalizeFixedParamKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if !isFixedParamKey(key) || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

func IsFixedParamKey(key string) bool {
	return isFixedParamKey(key)
}

func isFixedParamKey(key string) bool {
	_, ok := quant.HardBounds[key]
	return ok
}

func (SigmoidDCAEvolvable) StrategyID() string {
	return sigmoiddca.StrategyID
}

func (SigmoidDCAEvolvable) Sample(rng RandomSource) Gene {
	forceEmptyThreshold := sampleRange(rng, "force_empty_threshold")
	forceFullThreshold := sampleRangeAtLeast(rng, "force_full_threshold", forceEmptyThreshold)
	c := quant.Chromosome{
		MicroReservePct:        sampleRange(rng, "micro_reserve_pct"),
		Beta:                   sampleRange(rng, "beta"),
		Gamma:                  sampleRange(rng, "gamma"),
		WMean:                  sampleRange(rng, "w_mean"),
		WMomentum:              sampleRange(rng, "w_momentum"),
		WBreakout:              sampleRange(rng, "w_breakout"),
		DustUSD:                sampleRange(rng, "dust_usd"),
		RebalanceThreshold:     sampleRange(rng, "rebalance_threshold"),
		ForceFullThreshold:     forceFullThreshold,
		ForceEmptyThreshold:    forceEmptyThreshold,
		WedgeDeltaThreshold:    sampleRange(rng, "wedge_delta_threshold"),
		WedgeVolRatioThreshold: sampleRange(rng, "wedge_vol_ratio_threshold"),
		MacroBearMultiplier:    sampleRange(rng, "macro_bear_multiplier"),
		MacroBullMultiplier:    sampleRange(rng, "macro_bull_multiplier"),
		ExtraDeployPct:         sampleRange(rng, "extra_deploy_pct"),
		SoftReleaseMonths:      int(sampleRange(rng, "soft_release_months")),
		SoftReleasePct:         sampleRange(rng, "soft_release_pct"),
		HardReleaseMaxPct:      sampleRange(rng, "hard_release_max_pct"),
	}
	return quantizeSearchChromosome(c, nil)
}

func (e SigmoidDCAEvolvable) SampleWithOptions(rng RandomSource, options GeneOptions) Gene {
	options = NormalizeGeneOptions(options)
	if !options.MarketRegionEnabled {
		return sampleChromosomeWithOptions(rng, options)
	}
	maxWindow := options.MarketRegionMaxWindow
	if maxWindow < 2 {
		maxWindow = 2
	}
	features := make([]MarketRegionFeature, 0, len(MarketRegionFeatureIDs))
	for _, id := range MarketRegionFeatureIDs {
		feature := MarketRegionFeature{ID: id, Window: 2 + rng.Intn(maxWindow-1)}
		if options.MarketRegionMaxThresholds > 0 {
			if limits, ok := options.MarketRegionFeatureRanges[id]; ok && limits[1] > limits[0] {
				count := 1 + rng.Intn(options.MarketRegionMaxThresholds)
				for i := 0; i < count; i++ {
					if value, ok := sampleRegionThreshold(rng, limits); ok {
						feature.Thresholds = append(feature.Thresholds, value)
					}
				}
				feature.Thresholds = normalizeRegionThresholds(feature.Thresholds, limits, options.MarketRegionMaxThresholds)
			}
		}
		features = append(features, feature)
	}
	gene := MarketRegionGene{SchemaVersion: MarketRegionSchemaVersion, Global: sampleMarketRegionGlobal(rng, options), Features: features}
	gene.DefaultState = sampleMarketRegionState(rng, gene.Global)
	return gene
}

// MutateWithOptions never perturbs a dimension that the current task has
// removed from the candidate space.
func (SigmoidDCAEvolvable) MutateWithOptions(g Gene, prob float64, scale float64, rng RandomSource, options GeneOptions) Gene {
	options = NormalizeGeneOptions(options)
	if region, ok := isMarketRegionGene(g); ok {
		region.Global = mutateMarketRegionGlobal(region.Global, prob, scale, rng, options)
		for i := range region.Features {
			if rng.Float64() < prob {
				region.Features[i].Window += int(math.Round(rng.NormFloat64() * scale))
			}
			if limits, ok := options.MarketRegionFeatureRanges[region.Features[i].ID]; ok && rng.Float64() < prob {
				region.Features[i].Thresholds = mutateRegionThresholds(region.Features[i].Thresholds, limits, options.MarketRegionMaxThresholds, scale, rng)
			}
		}
		region = rebuildMarketRegionPacks(region, rng)
		region.DefaultState = mutateMarketRegionState(region.DefaultState, region.Global, prob, scale, rng)
		for i := range region.Packs {
			region.Packs[i].Chromosome = mutateMarketRegionState(region.Packs[i].Chromosome, region.Global, prob, scale, rng)
		}
		return region
	}
	return mutateChromosomeWithOptions(asChromosome(g), prob, scale, rng, options)
}

// rebuildMarketRegionPacks makes a changed interval layout executable before
// it can enter evaluation. Existing region keys retain their state parameters;
// only genuinely new interval combinations receive a fresh state package.
func rebuildMarketRegionPacks(region MarketRegionGene, rng RandomSource) MarketRegionGene {
	byKey := make(map[string]MarketRegionPack, len(region.Packs))
	for _, pack := range region.Packs {
		byKey[pack.Key] = pack
	}
	// State packs are sparse and materialised from observed bars immediately
	// before evaluation.  A layout mutation must preserve only packs whose
	// keys still belong to the new layout; it must never generate the full
	// Cartesian product in advance.
	packs := make([]MarketRegionPack, 0, len(byKey))
	for _, pack := range byKey {
		packs = append(packs, pack)
	}
	sort.Slice(packs, func(i, j int) bool { return packs[i].Key < packs[j].Key })
	region.Packs = packs
	return region
}

// CrossoverWithOptions never copies an inactive dimension from either parent.
func (SigmoidDCAEvolvable) CrossoverWithOptions(p1 Gene, p2 Gene, rng RandomSource, options GeneOptions) Gene {
	options = NormalizeGeneOptions(options)
	left, leftOK := isMarketRegionGene(p1)
	right, rightOK := isMarketRegionGene(p2)
	if leftOK && rightOK && len(left.Features) == len(right.Features) && len(left.Packs) == len(right.Packs) {
		child := left
		child.Global = crossoverMarketRegionGlobal(left.Global, right.Global, rng, options)
		for i := range child.Features {
			if rng.Float64() < 0.5 {
				child.Features[i].Window = right.Features[i].Window
			}
			if len(child.Features[i].Thresholds) == len(right.Features[i].Thresholds) {
				for j := range child.Features[i].Thresholds {
					child.Features[i].Thresholds[j] = pick(rng, child.Features[i].Thresholds[j], right.Features[i].Thresholds[j])
				}
			}
		}
		for i := range child.Packs {
			child.Packs[i].Chromosome = crossoverMarketRegionState(left.Packs[i].Chromosome, right.Packs[i].Chromosome, child.Global, rng)
		}
		return child
	}
	return crossoverChromosomeWithOptions(asChromosome(p1), asChromosome(p2), rng, options)
}

func (SigmoidDCAEvolvable) Mutate(g Gene, prob float64, scale float64, rng RandomSource) Gene {
	if region, ok := isMarketRegionGene(g); ok {
		for i := range region.Features {
			if rng.Float64() < prob {
				region.Features[i].Window += int(math.Round(rng.NormFloat64() * scale))
			}
		}
		for i := range region.Packs {
			region.Packs[i].Chromosome = asChromosome(SigmoidDCAEvolvable{}.Mutate(region.Packs[i].Chromosome, prob, scale, rng))
		}
		return region
	}
	c := asChromosome(g)
	c.MicroReservePct = mutateFloat(c.MicroReservePct, "micro_reserve_pct", prob, scale, rng)
	c.Beta = mutateFloat(c.Beta, "beta", prob, scale, rng)
	c.Gamma = mutateFloat(c.Gamma, "gamma", prob, scale, rng)
	c.WMean = mutateFloat(c.WMean, "w_mean", prob, scale, rng)
	c.WMomentum = mutateFloat(c.WMomentum, "w_momentum", prob, scale, rng)
	c.WBreakout = mutateFloat(c.WBreakout, "w_breakout", prob, scale, rng)
	c.DustUSD = mutateFloat(c.DustUSD, "dust_usd", prob, scale, rng)
	c.RebalanceThreshold = mutateFloat(c.RebalanceThreshold, "rebalance_threshold", prob, scale, rng)
	c.ForceFullThreshold, c.ForceEmptyThreshold = mutateForceThresholdPair(c.ForceFullThreshold, c.ForceEmptyThreshold, prob, scale, rng)
	c.WedgeDeltaThreshold = mutateFloat(c.WedgeDeltaThreshold, "wedge_delta_threshold", prob, scale, rng)
	c.WedgeVolRatioThreshold = mutateFloat(c.WedgeVolRatioThreshold, "wedge_vol_ratio_threshold", prob, scale, rng)
	c.MacroBearMultiplier = mutateFloat(c.MacroBearMultiplier, "macro_bear_multiplier", prob, scale, rng)
	c.MacroBullMultiplier = mutateFloat(c.MacroBullMultiplier, "macro_bull_multiplier", prob, scale, rng)
	c.ExtraDeployPct = mutateFloat(c.ExtraDeployPct, "extra_deploy_pct", prob, scale, rng)
	c.SoftReleaseMonths = int(math.Round(mutateFloat(float64(c.SoftReleaseMonths), "soft_release_months", prob, scale, rng)))
	c.SoftReleasePct = mutateFloat(c.SoftReleasePct, "soft_release_pct", prob, scale, rng)
	c.HardReleaseMaxPct = mutateFloat(c.HardReleaseMaxPct, "hard_release_max_pct", prob, scale, rng)
	return quantizeSearchChromosome(c, nil)
}

func (SigmoidDCAEvolvable) Crossover(p1 Gene, p2 Gene, rng RandomSource) Gene {
	left, leftOK := isMarketRegionGene(p1)
	right, rightOK := isMarketRegionGene(p2)
	if leftOK && rightOK && len(left.Features) == len(right.Features) && len(left.Packs) == len(right.Packs) {
		child := left
		// Keep the interval layout of one parent. Recombining a different number
		// of thresholds would change the Cartesian keys and detach packs from
		// their regions; threshold layouts evolve through fresh samples instead.
		for i := range child.Features {
			if rng.Float64() < 0.5 {
				child.Features[i].Window = right.Features[i].Window
			}
		}
		for i := range child.Packs {
			child.Packs[i].Chromosome = asChromosome(SigmoidDCAEvolvable{}.Crossover(left.Packs[i].Chromosome, right.Packs[i].Chromosome, rng))
		}
		return child
	}
	a := asChromosome(p1)
	b := asChromosome(p2)
	c := quant.Chromosome{}
	c.MicroReservePct = pick(rng, a.MicroReservePct, b.MicroReservePct)
	c.Beta = pick(rng, a.Beta, b.Beta)
	c.Gamma = pick(rng, a.Gamma, b.Gamma)
	c.WMean = pick(rng, a.WMean, b.WMean)
	c.WMomentum = pick(rng, a.WMomentum, b.WMomentum)
	c.WBreakout = pick(rng, a.WBreakout, b.WBreakout)
	c.DustUSD = pick(rng, a.DustUSD, b.DustUSD)
	c.RebalanceThreshold = pick(rng, a.RebalanceThreshold, b.RebalanceThreshold)
	c.ForceFullThreshold, c.ForceEmptyThreshold = crossoverForceThresholdPair(a.ForceFullThreshold, a.ForceEmptyThreshold, b.ForceFullThreshold, b.ForceEmptyThreshold, rng)
	c.WedgeDeltaThreshold = pick(rng, a.WedgeDeltaThreshold, b.WedgeDeltaThreshold)
	c.WedgeVolRatioThreshold = pick(rng, a.WedgeVolRatioThreshold, b.WedgeVolRatioThreshold)
	c.MacroBearMultiplier = pick(rng, a.MacroBearMultiplier, b.MacroBearMultiplier)
	c.MacroBullMultiplier = pick(rng, a.MacroBullMultiplier, b.MacroBullMultiplier)
	c.ExtraDeployPct = pick(rng, a.ExtraDeployPct, b.ExtraDeployPct)
	c.SoftReleaseMonths = int(pick(rng, float64(a.SoftReleaseMonths), float64(b.SoftReleaseMonths)))
	c.SoftReleasePct = pick(rng, a.SoftReleasePct, b.SoftReleasePct)
	c.HardReleaseMaxPct = pick(rng, a.HardReleaseMaxPct, b.HardReleaseMaxPct)
	return quantizeSearchChromosome(c, nil)
}

func (SigmoidDCAEvolvable) Fingerprint(g Gene) uint64 {
	if region, ok := isMarketRegionGene(g); ok {
		return marketRegionFingerprint(region)
	}
	c := asChromosome(g)
	h := fnv.New64a()
	writeQuantized(h, c.MicroReservePct)
	writeQuantized(h, c.Beta)
	writeQuantized(h, c.Gamma)
	writeQuantized(h, c.WMean)
	writeQuantized(h, c.WMomentum)
	writeQuantized(h, c.WBreakout)
	writeQuantized(h, c.DustUSD)
	writeQuantized(h, c.RebalanceThreshold)
	writeQuantized(h, c.ForceFullThreshold)
	writeQuantized(h, c.ForceEmptyThreshold)
	writeQuantized(h, c.WedgeDeltaThreshold)
	writeQuantized(h, c.WedgeVolRatioThreshold)
	writeQuantized(h, c.MacroBearMultiplier)
	writeQuantized(h, c.MacroBullMultiplier)
	writeQuantized(h, c.ExtraDeployPct)
	writeQuantized(h, float64(c.SoftReleaseMonths))
	writeQuantized(h, c.SoftReleasePct)
	writeQuantized(h, c.HardReleaseMaxPct)
	return h.Sum64()
}

func (SigmoidDCAEvolvable) NormalizeGene(g Gene, options GeneOptions) Gene {
	if region, ok := isMarketRegionGene(g); ok {
		for i := range region.Features {
			if region.Features[i].Window < 2 {
				region.Features[i].Window = 2
			}
			if options.MarketRegionMaxWindow >= 2 && region.Features[i].Window > options.MarketRegionMaxWindow {
				region.Features[i].Window = options.MarketRegionMaxWindow
			}
			if options.MarketRegionMaxThresholds >= 0 && len(region.Features[i].Thresholds) > options.MarketRegionMaxThresholds {
				region.Features[i].Thresholds = region.Features[i].Thresholds[:options.MarketRegionMaxThresholds]
			}
			if limits, ok := options.MarketRegionFeatureRanges[region.Features[i].ID]; ok {
				region.Features[i].Thresholds = normalizeRegionThresholds(region.Features[i].Thresholds, limits, options.MarketRegionMaxThresholds)
			}
		}
		normalized, err := normalizeMarketRegionGene(region, options)
		if err != nil {
			return region
		}
		return normalized
	}
	c := asChromosome(g)
	return normalizeChromosome(c, options)
}

func chromosomeFieldEvolves(key string, options GeneOptions) bool {
	options = NormalizeGeneOptions(options)
	for _, fixed := range options.FixedParamKeys {
		if fixed == key {
			return false
		}
	}
	if options.MarketRegionEnabled {
		switch key {
		case "micro_reserve_pct", "beta", "rebalance_threshold":
			return false
		case "gamma", "w_mean", "w_momentum", "w_breakout", "force_full_threshold", "force_empty_threshold":
			return true
		}
	}
	switch key {
	case "micro_reserve_pct":
		return options.PositionStructure != sigmoiddca.PositionStructureFloatingOnly
	case "beta":
		return !options.DisableBeta
	case "dust_usd":
		return !options.DisableDustFilter
	case "wedge_delta_threshold", "wedge_vol_ratio_threshold":
		return !options.DisableWedgeMinimum
	case "macro_bear_multiplier", "macro_bull_multiplier", "extra_deploy_pct", "soft_release_months", "soft_release_pct", "hard_release_max_pct":
		return options.PositionStructure != sigmoiddca.PositionStructureFloatingOnly
	case "rebalance_threshold":
		return options.EvolveRebalanceThreshold
	case "gamma":
		return options.EvolveGamma
	case "w_mean":
		return options.EnableWMean
	case "w_momentum":
		return options.EnableWMomentum
	case "w_breakout":
		return options.EnableWBreakout
	case "force_full_threshold":
		return options.EvolveForceFullThreshold
	case "force_empty_threshold":
		return options.EvolveForceEmptyThreshold
	default:
		return true
	}
}

func sampleChromosomeWithOptions(rng RandomSource, options GeneOptions) quant.Chromosome {
	c := normalizeChromosome(quant.DefaultSeedChromosome, options)
	if chromosomeFieldEvolves("micro_reserve_pct", options) {
		c.MicroReservePct = sampleRange(rng, "micro_reserve_pct")
	}
	if chromosomeFieldEvolves("beta", options) {
		c.Beta = sampleRange(rng, "beta")
	}
	if chromosomeFieldEvolves("gamma", options) {
		c.Gamma = sampleRange(rng, "gamma")
	}
	if chromosomeFieldEvolves("w_mean", options) {
		c.WMean = sampleRange(rng, "w_mean")
	}
	if chromosomeFieldEvolves("w_momentum", options) {
		c.WMomentum = sampleRange(rng, "w_momentum")
	}
	if chromosomeFieldEvolves("w_breakout", options) {
		c.WBreakout = sampleRange(rng, "w_breakout")
	}
	if chromosomeFieldEvolves("dust_usd", options) {
		c.DustUSD = sampleRange(rng, "dust_usd")
	}
	if chromosomeFieldEvolves("rebalance_threshold", options) {
		c.RebalanceThreshold = sampleRange(rng, "rebalance_threshold")
	}
	if chromosomeFieldEvolves("force_full_threshold", options) && chromosomeFieldEvolves("force_empty_threshold", options) {
		c.ForceEmptyThreshold = sampleRange(rng, "force_empty_threshold")
		c.ForceFullThreshold = sampleRangeAtLeast(rng, "force_full_threshold", c.ForceEmptyThreshold)
	} else if chromosomeFieldEvolves("force_full_threshold", options) {
		c.ForceFullThreshold = sampleRange(rng, "force_full_threshold")
	} else if chromosomeFieldEvolves("force_empty_threshold", options) {
		c.ForceEmptyThreshold = sampleRange(rng, "force_empty_threshold")
	}
	if chromosomeFieldEvolves("wedge_delta_threshold", options) {
		c.WedgeDeltaThreshold = sampleRange(rng, "wedge_delta_threshold")
	}
	if chromosomeFieldEvolves("wedge_vol_ratio_threshold", options) {
		c.WedgeVolRatioThreshold = sampleRange(rng, "wedge_vol_ratio_threshold")
	}
	if chromosomeFieldEvolves("macro_bear_multiplier", options) {
		c.MacroBearMultiplier = sampleRange(rng, "macro_bear_multiplier")
	}
	if chromosomeFieldEvolves("macro_bull_multiplier", options) {
		c.MacroBullMultiplier = sampleRange(rng, "macro_bull_multiplier")
	}
	if chromosomeFieldEvolves("extra_deploy_pct", options) {
		c.ExtraDeployPct = sampleRange(rng, "extra_deploy_pct")
	}
	if chromosomeFieldEvolves("soft_release_months", options) {
		c.SoftReleaseMonths = int(sampleRange(rng, "soft_release_months"))
	}
	if chromosomeFieldEvolves("soft_release_pct", options) {
		c.SoftReleasePct = sampleRange(rng, "soft_release_pct")
	}
	if chromosomeFieldEvolves("hard_release_max_pct", options) {
		c.HardReleaseMaxPct = sampleRange(rng, "hard_release_max_pct")
	}
	return normalizeChromosome(c, options)
}

func mutateChromosomeWithOptions(c quant.Chromosome, prob float64, scale float64, rng RandomSource, options GeneOptions) quant.Chromosome {
	if chromosomeFieldEvolves("micro_reserve_pct", options) {
		c.MicroReservePct = mutateFloat(c.MicroReservePct, "micro_reserve_pct", prob, scale, rng)
	}
	if chromosomeFieldEvolves("beta", options) {
		c.Beta = mutateFloat(c.Beta, "beta", prob, scale, rng)
	}
	if chromosomeFieldEvolves("gamma", options) {
		c.Gamma = mutateFloat(c.Gamma, "gamma", prob, scale, rng)
	}
	if chromosomeFieldEvolves("w_mean", options) {
		c.WMean = mutateFloat(c.WMean, "w_mean", prob, scale, rng)
	}
	if chromosomeFieldEvolves("w_momentum", options) {
		c.WMomentum = mutateFloat(c.WMomentum, "w_momentum", prob, scale, rng)
	}
	if chromosomeFieldEvolves("w_breakout", options) {
		c.WBreakout = mutateFloat(c.WBreakout, "w_breakout", prob, scale, rng)
	}
	if chromosomeFieldEvolves("dust_usd", options) {
		c.DustUSD = mutateFloat(c.DustUSD, "dust_usd", prob, scale, rng)
	}
	if chromosomeFieldEvolves("rebalance_threshold", options) {
		c.RebalanceThreshold = mutateFloat(c.RebalanceThreshold, "rebalance_threshold", prob, scale, rng)
	}
	if chromosomeFieldEvolves("force_full_threshold", options) || chromosomeFieldEvolves("force_empty_threshold", options) {
		c.ForceFullThreshold, c.ForceEmptyThreshold = mutateForceThresholdPair(c.ForceFullThreshold, c.ForceEmptyThreshold, prob, scale, rng)
	}
	if chromosomeFieldEvolves("wedge_delta_threshold", options) {
		c.WedgeDeltaThreshold = mutateFloat(c.WedgeDeltaThreshold, "wedge_delta_threshold", prob, scale, rng)
	}
	if chromosomeFieldEvolves("wedge_vol_ratio_threshold", options) {
		c.WedgeVolRatioThreshold = mutateFloat(c.WedgeVolRatioThreshold, "wedge_vol_ratio_threshold", prob, scale, rng)
	}
	if chromosomeFieldEvolves("macro_bear_multiplier", options) {
		c.MacroBearMultiplier = mutateFloat(c.MacroBearMultiplier, "macro_bear_multiplier", prob, scale, rng)
	}
	if chromosomeFieldEvolves("macro_bull_multiplier", options) {
		c.MacroBullMultiplier = mutateFloat(c.MacroBullMultiplier, "macro_bull_multiplier", prob, scale, rng)
	}
	if chromosomeFieldEvolves("extra_deploy_pct", options) {
		c.ExtraDeployPct = mutateFloat(c.ExtraDeployPct, "extra_deploy_pct", prob, scale, rng)
	}
	if chromosomeFieldEvolves("soft_release_months", options) {
		c.SoftReleaseMonths = int(math.Round(mutateFloat(float64(c.SoftReleaseMonths), "soft_release_months", prob, scale, rng)))
	}
	if chromosomeFieldEvolves("soft_release_pct", options) {
		c.SoftReleasePct = mutateFloat(c.SoftReleasePct, "soft_release_pct", prob, scale, rng)
	}
	if chromosomeFieldEvolves("hard_release_max_pct", options) {
		c.HardReleaseMaxPct = mutateFloat(c.HardReleaseMaxPct, "hard_release_max_pct", prob, scale, rng)
	}
	return normalizeChromosome(c, options)
}

func crossoverChromosomeWithOptions(a quant.Chromosome, b quant.Chromosome, rng RandomSource, options GeneOptions) quant.Chromosome {
	c := normalizeChromosome(quant.DefaultSeedChromosome, options)
	if chromosomeFieldEvolves("micro_reserve_pct", options) {
		c.MicroReservePct = pick(rng, a.MicroReservePct, b.MicroReservePct)
	}
	if chromosomeFieldEvolves("beta", options) {
		c.Beta = pick(rng, a.Beta, b.Beta)
	}
	if chromosomeFieldEvolves("gamma", options) {
		c.Gamma = pick(rng, a.Gamma, b.Gamma)
	}
	if chromosomeFieldEvolves("w_mean", options) {
		c.WMean = pick(rng, a.WMean, b.WMean)
	}
	if chromosomeFieldEvolves("w_momentum", options) {
		c.WMomentum = pick(rng, a.WMomentum, b.WMomentum)
	}
	if chromosomeFieldEvolves("w_breakout", options) {
		c.WBreakout = pick(rng, a.WBreakout, b.WBreakout)
	}
	if chromosomeFieldEvolves("dust_usd", options) {
		c.DustUSD = pick(rng, a.DustUSD, b.DustUSD)
	}
	if chromosomeFieldEvolves("rebalance_threshold", options) {
		c.RebalanceThreshold = pick(rng, a.RebalanceThreshold, b.RebalanceThreshold)
	}
	if chromosomeFieldEvolves("force_full_threshold", options) || chromosomeFieldEvolves("force_empty_threshold", options) {
		c.ForceFullThreshold, c.ForceEmptyThreshold = crossoverForceThresholdPair(a.ForceFullThreshold, a.ForceEmptyThreshold, b.ForceFullThreshold, b.ForceEmptyThreshold, rng)
	}
	if chromosomeFieldEvolves("wedge_delta_threshold", options) {
		c.WedgeDeltaThreshold = pick(rng, a.WedgeDeltaThreshold, b.WedgeDeltaThreshold)
	}
	if chromosomeFieldEvolves("wedge_vol_ratio_threshold", options) {
		c.WedgeVolRatioThreshold = pick(rng, a.WedgeVolRatioThreshold, b.WedgeVolRatioThreshold)
	}
	if chromosomeFieldEvolves("macro_bear_multiplier", options) {
		c.MacroBearMultiplier = pick(rng, a.MacroBearMultiplier, b.MacroBearMultiplier)
	}
	if chromosomeFieldEvolves("macro_bull_multiplier", options) {
		c.MacroBullMultiplier = pick(rng, a.MacroBullMultiplier, b.MacroBullMultiplier)
	}
	if chromosomeFieldEvolves("extra_deploy_pct", options) {
		c.ExtraDeployPct = pick(rng, a.ExtraDeployPct, b.ExtraDeployPct)
	}
	if chromosomeFieldEvolves("soft_release_months", options) {
		c.SoftReleaseMonths = int(pick(rng, float64(a.SoftReleaseMonths), float64(b.SoftReleaseMonths)))
	}
	if chromosomeFieldEvolves("soft_release_pct", options) {
		c.SoftReleasePct = pick(rng, a.SoftReleasePct, b.SoftReleasePct)
	}
	if chromosomeFieldEvolves("hard_release_max_pct", options) {
		c.HardReleaseMaxPct = pick(rng, a.HardReleaseMaxPct, b.HardReleaseMaxPct)
	}
	return normalizeChromosome(c, options)
}

func sampleRegionThreshold(rng RandomSource, limits [2]float64) (float64, bool) {
	if limits[1] <= limits[0] {
		return 0, false
	}
	return limits[0] + rng.Float64()*(limits[1]-limits[0]), true
}

func normalizeRegionThresholds(values []float64, limits [2]float64, maximum int) []float64 {
	if maximum <= 0 {
		return nil
	}
	minimum, upper := limits[0], limits[1]
	if minimum >= upper {
		return nil
	}
	seen := map[uint64]bool{}
	out := make([]float64, 0, len(values))
	for _, value := range values {
		if value < minimum || value > upper {
			continue
		}
		bits := math.Float64bits(value)
		if seen[bits] {
			continue
		}
		seen[bits] = true
		out = append(out, value)
	}
	sort.Float64s(out)
	if len(out) > maximum {
		out = out[:maximum]
	}
	return out
}

func mutateRegionThresholds(values []float64, limits [2]float64, maximum int, scale float64, rng RandomSource) []float64 {
	values = append([]float64(nil), values...)
	for i := range values {
		if rng.Float64() < 0.5 {
			values[i] += math.Round(rng.NormFloat64()*scale) * searchParameterStep
		}
	}
	if maximum > 0 && len(values) < maximum && rng.Float64() < 0.25 {
		if value, ok := sampleRegionThreshold(rng, limits); ok {
			values = append(values, value)
		}
	}
	if len(values) > 0 && rng.Float64() < 0.15 {
		values = append(values[:len(values)-1], values[len(values):]...)
	}
	return normalizeRegionThresholds(values, limits, maximum)
}

func marketRegionGlobalOptions(options GeneOptions) GeneOptions {
	options.MarketRegionEnabled = false
	options.DisableBeta = true
	options.EvolveGamma = false
	options.EnableWMean = false
	options.EnableWMomentum = false
	options.EnableWBreakout = false
	options.EvolveForceFullThreshold = false
	options.EvolveForceEmptyThreshold = false
	options.EvolveRebalanceThreshold = false
	return options
}

func sampleMarketRegionGlobal(rng RandomSource, options GeneOptions) quant.Chromosome {
	return sampleChromosomeWithOptions(rng, marketRegionGlobalOptions(options))
}

func mutateMarketRegionGlobal(c quant.Chromosome, prob float64, scale float64, rng RandomSource, options GeneOptions) quant.Chromosome {
	return mutateChromosomeWithOptions(c, prob, scale, rng, marketRegionGlobalOptions(options))
}

func crossoverMarketRegionGlobal(a quant.Chromosome, b quant.Chromosome, rng RandomSource, options GeneOptions) quant.Chromosome {
	return crossoverChromosomeWithOptions(a, b, rng, marketRegionGlobalOptions(options))
}

func combineMarketRegionChromosome(global quant.Chromosome, state quant.Chromosome) quant.Chromosome {
	c := global
	c.Gamma = state.Gamma
	c.WMean = state.WMean
	c.WMomentum = state.WMomentum
	c.WBreakout = state.WBreakout
	c.ForceFullThreshold = state.ForceFullThreshold
	c.ForceEmptyThreshold = state.ForceEmptyThreshold
	return c
}

// marketRegionStateChromosome deliberately retains only the six values that a
// market interval is allowed to select. Keeping the stored pack sparse makes
// it impossible for a state pack to smuggle a second Beta, reserve or legacy
// base-position setting into the execution path.
func marketRegionStateChromosome(c quant.Chromosome) quant.Chromosome {
	return quant.Chromosome{
		Gamma:               c.Gamma,
		WMean:               c.WMean,
		WMomentum:           c.WMomentum,
		WBreakout:           c.WBreakout,
		ForceFullThreshold:  c.ForceFullThreshold,
		ForceEmptyThreshold: c.ForceEmptyThreshold,
	}
}

func sampleMarketRegionState(rng RandomSource, global quant.Chromosome) quant.Chromosome {
	c := global
	c.Gamma = sampleRange(rng, "gamma")
	c.WMean = sampleRange(rng, "w_mean")
	c.WMomentum = sampleRange(rng, "w_momentum")
	c.WBreakout = sampleRange(rng, "w_breakout")
	c.ForceEmptyThreshold = sampleRange(rng, "force_empty_threshold")
	c.ForceFullThreshold = sampleRangeAtLeast(rng, "force_full_threshold", c.ForceEmptyThreshold)
	return marketRegionStateChromosome(c)
}

func mutateMarketRegionState(c quant.Chromosome, global quant.Chromosome, prob float64, scale float64, rng RandomSource) quant.Chromosome {
	c = combineMarketRegionChromosome(global, c)
	c.Gamma = mutateFloat(c.Gamma, "gamma", prob, scale, rng)
	c.WMean = mutateFloat(c.WMean, "w_mean", prob, scale, rng)
	c.WMomentum = mutateFloat(c.WMomentum, "w_momentum", prob, scale, rng)
	c.WBreakout = mutateFloat(c.WBreakout, "w_breakout", prob, scale, rng)
	c.ForceFullThreshold, c.ForceEmptyThreshold = mutateForceThresholdPair(c.ForceFullThreshold, c.ForceEmptyThreshold, prob, scale, rng)
	return marketRegionStateChromosome(c)
}

func crossoverMarketRegionState(a quant.Chromosome, b quant.Chromosome, global quant.Chromosome, rng RandomSource) quant.Chromosome {
	c := global
	c.Gamma = pick(rng, a.Gamma, b.Gamma)
	c.WMean = pick(rng, a.WMean, b.WMean)
	c.WMomentum = pick(rng, a.WMomentum, b.WMomentum)
	c.WBreakout = pick(rng, a.WBreakout, b.WBreakout)
	c.ForceFullThreshold, c.ForceEmptyThreshold = crossoverForceThresholdPair(a.ForceFullThreshold, a.ForceEmptyThreshold, b.ForceFullThreshold, b.ForceEmptyThreshold, rng)
	return marketRegionStateChromosome(c)
}

func normalizeChromosome(c quant.Chromosome, options GeneOptions) quant.Chromosome {
	options = NormalizeGeneOptions(options)
	if !options.EvolveRebalanceThreshold {
		c.RebalanceThreshold = 0
	}
	if options.MarketRegionEnabled {
		// These two mechanisms are intentionally absent from market-region
		// search and execution, rather than being frozen at an arbitrary seed.
		c.MicroReservePct = 0
		c.RebalanceThreshold = 0
	}
	if !options.EvolveForceFullThreshold && !options.MarketRegionEnabled {
		c.ForceFullThreshold = 1
	}
	if !options.EvolveForceEmptyThreshold && !options.MarketRegionEnabled {
		c.ForceEmptyThreshold = 0
	}
	if !options.EvolveGamma && !options.MarketRegionEnabled {
		c.Gamma = 0
	}
	if !options.EnableWMean && !options.MarketRegionEnabled {
		c.WMean = 0
	}
	if !options.EnableWMomentum && !options.MarketRegionEnabled {
		c.WMomentum = 0
	}
	if !options.EnableWBreakout && !options.MarketRegionEnabled {
		c.WBreakout = 0
	}
	if options.PositionStructure == sigmoiddca.PositionStructureFloatingOnly {
		c.MicroReservePct = 0
		c.RebalanceThreshold = 0
		c.MacroBearMultiplier = 1
		c.MacroBullMultiplier = 1
		c.ExtraDeployPct = 0
		c.SoftReleaseMonths = int(quant.HardBounds["soft_release_months"].Max)
		c.SoftReleasePct = 0
		c.HardReleaseMaxPct = 0
	}
	if options.FixedGene != nil && len(options.FixedParamKeys) > 0 {
		c = applyFixedChromosomeFields(c, *options.FixedGene, options.FixedParamKeys)
		c = constrainFixedForceThresholdPair(c, options.FixedParamKeys)
	}
	c = quantizeSearchChromosome(c, options.FixedParamKeys)
	if options.MarketRegionEnabled || options.PositionStructure == sigmoiddca.PositionStructureFloatingOnly {
		// quantizeSearchChromosome applies ordinary hard bounds; restore these
		// explicit disabled sentinels after that generic normalisation.
		c.MicroReservePct = 0
		c.RebalanceThreshold = 0
	}
	if options.DisableBeta || options.MarketRegionEnabled {
		c.Beta = 0
	}
	if options.DisableDustFilter {
		c.DustUSD = 0
	}
	if options.DisableWedgeMinimum {
		c.WedgeDeltaThreshold = 0
		c.WedgeVolRatioThreshold = 0
	}
	return c
}

func quantizeSearchChromosome(c quant.Chromosome, fixedKeys []string) quant.Chromosome {
	fixed := make(map[string]bool, len(fixedKeys))
	for _, key := range fixedKeys {
		fixed[key] = true
	}
	quantize := func(value float64, name string) float64 {
		if fixed[name] {
			return value
		}
		return quantizeSearchValue(value, quant.HardBounds[name])
	}
	c.MicroReservePct = quantize(c.MicroReservePct, "micro_reserve_pct")
	c.Beta = quantize(c.Beta, "beta")
	c.Gamma = quantize(c.Gamma, "gamma")
	c.WMean = quantize(c.WMean, "w_mean")
	c.WMomentum = quantize(c.WMomentum, "w_momentum")
	c.WBreakout = quantize(c.WBreakout, "w_breakout")
	c.DustUSD = quantize(c.DustUSD, "dust_usd")
	c.RebalanceThreshold = quantize(c.RebalanceThreshold, "rebalance_threshold")
	c.ForceFullThreshold = quantize(c.ForceFullThreshold, "force_full_threshold")
	c.ForceEmptyThreshold = quantize(c.ForceEmptyThreshold, "force_empty_threshold")
	c.WedgeDeltaThreshold = quantize(c.WedgeDeltaThreshold, "wedge_delta_threshold")
	c.WedgeVolRatioThreshold = quantize(c.WedgeVolRatioThreshold, "wedge_vol_ratio_threshold")
	c.MacroBearMultiplier = quantize(c.MacroBearMultiplier, "macro_bear_multiplier")
	c.MacroBullMultiplier = quantize(c.MacroBullMultiplier, "macro_bull_multiplier")
	c.ExtraDeployPct = quantize(c.ExtraDeployPct, "extra_deploy_pct")
	if !fixed["soft_release_months"] {
		c.SoftReleaseMonths = int(math.Round(quantizeSearchValue(float64(c.SoftReleaseMonths), quant.HardBounds["soft_release_months"])))
	}
	c.SoftReleasePct = quantize(c.SoftReleasePct, "soft_release_pct")
	c.HardReleaseMaxPct = quantize(c.HardReleaseMaxPct, "hard_release_max_pct")
	return quant.ClampChromosome(c)
}

func quantizeSearchValue(value float64, bound quant.Bound) float64 {
	minimum := math.Ceil(bound.Min/searchParameterStep) * searchParameterStep
	maximum := math.Floor(bound.Max/searchParameterStep) * searchParameterStep
	value = math.Round(value/searchParameterStep) * searchParameterStep
	if value < minimum {
		value = minimum
	}
	if value > maximum {
		value = maximum
	}
	return math.Round(value*100) / 100
}

// mutateForceThresholdPair preserves the structural chromosome relation while
// producing a new pair. It never generates an invalid full/empty ordering for
// ValidateChromosome to reject later.
func mutateForceThresholdPair(full float64, empty float64, prob float64, scale float64, rng RandomSource) (float64, float64) {
	empty = mutateFloat(empty, "force_empty_threshold", prob, scale, rng)
	full = mutateFloat(full, "force_full_threshold", prob, scale, rng)
	if full < empty {
		// The valid proposal space for full is [empty, max]. Keeping empty and
		// drawing directly from that lattice preserves the invariant.
		full = sampleRangeAtLeast(rng, "force_full_threshold", empty)
	}
	return full, empty
}

// crossoverForceThresholdPair chooses the empty threshold first, then only
// uses a parent full threshold that is valid for that choice. No invalid child
// chromosome is constructed and repaired afterwards.
func crossoverForceThresholdPair(fullA float64, emptyA float64, fullB float64, emptyB float64, rng RandomSource) (float64, float64) {
	if rng.Float64() < 0.5 {
		if rng.Float64() < 0.5 && fullB >= emptyA {
			return fullB, emptyA
		}
		return fullA, emptyA
	}
	if rng.Float64() < 0.5 && fullA >= emptyB {
		return fullA, emptyB
	}
	return fullB, emptyB
}

func constrainFixedForceThresholdPair(c quant.Chromosome, fixedKeys []string) quant.Chromosome {
	fullFixed, emptyFixed := false, false
	for _, key := range fixedKeys {
		fullFixed = fullFixed || key == "force_full_threshold"
		emptyFixed = emptyFixed || key == "force_empty_threshold"
	}
	if fullFixed && !emptyFixed && c.ForceEmptyThreshold > c.ForceFullThreshold {
		c.ForceEmptyThreshold = c.ForceFullThreshold
	}
	if emptyFixed && !fullFixed && c.ForceFullThreshold < c.ForceEmptyThreshold {
		c.ForceFullThreshold = c.ForceEmptyThreshold
	}
	return c
}

func clampRange(value float64, bound quant.Bound) float64 {
	if value < bound.Min {
		return bound.Min
	}
	if value > bound.Max {
		return bound.Max
	}
	return value
}

func applyFixedChromosomeFields(c quant.Chromosome, base quant.Chromosome, keys []string) quant.Chromosome {
	base = quant.ClampChromosome(base)
	for _, key := range keys {
		switch key {
		case "micro_reserve_pct":
			c.MicroReservePct = base.MicroReservePct
		case "beta":
			c.Beta = base.Beta
		case "gamma":
			c.Gamma = base.Gamma
		case "w_mean":
			c.WMean = base.WMean
		case "w_momentum":
			c.WMomentum = base.WMomentum
		case "w_breakout":
			c.WBreakout = base.WBreakout
		case "dust_usd":
			c.DustUSD = base.DustUSD
		case "rebalance_threshold":
			c.RebalanceThreshold = base.RebalanceThreshold
		case "force_full_threshold":
			c.ForceFullThreshold = base.ForceFullThreshold
		case "force_empty_threshold":
			c.ForceEmptyThreshold = base.ForceEmptyThreshold
		case "wedge_delta_threshold":
			c.WedgeDeltaThreshold = base.WedgeDeltaThreshold
		case "wedge_vol_ratio_threshold":
			c.WedgeVolRatioThreshold = base.WedgeVolRatioThreshold
		case "macro_bear_multiplier":
			c.MacroBearMultiplier = base.MacroBearMultiplier
		case "macro_bull_multiplier":
			c.MacroBullMultiplier = base.MacroBullMultiplier
		case "extra_deploy_pct":
			c.ExtraDeployPct = base.ExtraDeployPct
		case "soft_release_months":
			c.SoftReleaseMonths = base.SoftReleaseMonths
		case "soft_release_pct":
			c.SoftReleasePct = base.SoftReleasePct
		case "hard_release_max_pct":
			c.HardReleaseMaxPct = base.HardReleaseMaxPct
		}
	}
	return c
}

func (e SigmoidDCAEvolvable) Evaluate(ctx context.Context, g Gene, plan EvaluablePlan) (FitnessResult, error) {
	normalized := e.NormalizeGene(g, plan.GeneOptions)
	region, regionMode := isMarketRegionGene(normalized)
	if !regionMode {
		c := normalized.(quant.Chromosome)
		if err := quant.ValidateChromosome(c); err != nil {
			trace(plan, TraceModeDetailed, "strategy", "individual.evaluate.invalid_gene", "invalid chromosome rejected", map[string]any{
				"generation": plan.Generation,
				"individual": plan.Individual,
				"worker":     plan.Worker,
				"error":      err.Error(),
			})
			return FitnessResult{ScoreTotal: FatalFitnessScore, Fatal: true}, nil
		}
	}
	if len(plan.MultiMarkets) > 0 {
		return e.evaluateMultiMarket(ctx, normalized, region, regionMode, plan)
	}
	result := FitnessResult{}
	for i, window := range plan.Windows {
		if err := ctx.Err(); err != nil {
			return FitnessResult{}, err
		}
		trace(plan, TraceModeDetailed, "strategy", "window.evaluate.start", "evaluation window started", map[string]any{
			"generation": plan.Generation,
			"individual": plan.Individual,
			"worker":     plan.Worker,
			"window":     window.Label,
			"bars":       len(window.Bars),
		})
		c := quant.DefaultSeedChromosome
		var provider backtestcore.ParameterProvider
		if regionMode {
			var providerErr error
			if plan.ComputeStep != nil {
				plan.ComputeStep(marketRegionProviderUnits(region, window.Bars))
			}
			provider, providerErr = newMarketRegionProviderWithCache(region, window.Bars, plan.MarketRegionCache)
			if providerErr != nil {
				return FitnessResult{ScoreTotal: FatalFitnessScore, Fatal: true}, nil
			}
		} else {
			c = normalized.(quant.Chromosome)
		}
		path := runSigmoidDCAPathBacktestWithTraceAndMode(window.Bars, window.EvalStartMs, plan.Interval, plan.ExecutionMode, c, plan.Spawn, PathTraceConfig{
			Trace:         plan.Trace,
			Mode:          plan.TraceMode,
			TraceModeFunc: plan.TraceModeFunc,
			ComputeStep:   plan.ComputeStep,
			Generation:    plan.Generation,
			Individual:    plan.Individual,
			Worker:        plan.Worker,
			Window:        window.Label,
		}, plan.Costs, NormalizeGeneOptions(plan.GeneOptions).PositionStructure, plan.Pair, plan.LongTermFilter, provider)
		if path.Err != nil {
			trace(plan, TraceModeSummary, "strategy", "window.evaluate.failed", "backtest failed", map[string]any{"error": path.Err.Error(), "window": window.Label})
			return FitnessResult{}, fmt.Errorf("window %s backtest failed: %w", window.Label, path.Err)
		}
		metrics := path.Metrics
		baseline := plan.DCABaselines[i]
		alpha := metrics.ROI - baseline.ROI
		score := alpha - 1.5*math.Max(0, metrics.MaxDrawdown-baseline.MaxDrawdown) - plan.TradePenalty*float64(metrics.TradeCount)
		if metrics.MaxDrawdown >= 0.88 {
			score = FatalFitnessScore
			result.Fatal = true
		}
		result.Windows = append(result.Windows, quant.CrucibleResult{
			Window: window.Label,
			Score:  score,
			ROI:    metrics.ROI,
			MaxDD:  metrics.MaxDrawdown,
			Alpha:  alpha,
		})
		if metrics.MaxDrawdown > result.MaxDrawdown {
			result.MaxDrawdown = metrics.MaxDrawdown
		}
		if result.Fatal {
			result.ScoreTotal = FatalFitnessScore
			trace(plan, TraceModeDetailed, "strategy", "window.evaluate.fatal", "window triggered fatal fitness", map[string]any{
				"generation":      plan.Generation,
				"individual":      plan.Individual,
				"worker":          plan.Worker,
				"window":          window.Label,
				"score":           score,
				"roi":             metrics.ROI,
				"alpha":           alpha,
				"max_drawdown":    metrics.MaxDrawdown,
				"trade_count":     metrics.TradeCount,
				"trade_penalty":   plan.TradePenalty,
				"baseline_roi":    baseline.ROI,
				"baseline_max_dd": baseline.MaxDrawdown,
			})
			return result, nil
		}
		result.ScoreTotal += window.Weight * score
		trace(plan, TraceModeDetailed, "strategy", "window.evaluate.done", "evaluation window completed", map[string]any{
			"generation":      plan.Generation,
			"individual":      plan.Individual,
			"worker":          plan.Worker,
			"window":          window.Label,
			"score":           score,
			"weighted_score":  window.Weight * score,
			"roi":             metrics.ROI,
			"alpha":           alpha,
			"max_drawdown":    metrics.MaxDrawdown,
			"trade_count":     metrics.TradeCount,
			"trade_penalty":   plan.TradePenalty,
			"baseline_roi":    baseline.ROI,
			"baseline_max_dd": baseline.MaxDrawdown,
			"final_equity":    metrics.FinalEquity,
			"total_injected":  metrics.TotalInjected,
		})
	}
	return result, nil
}

// evaluateMultiMarket intentionally has no window result output.  A market is
// evaluated once over its complete supplied training period, then the direct
// sum of annualized log growth is used as the candidate score.
func (e SigmoidDCAEvolvable) evaluateMultiMarket(ctx context.Context, normalized Gene, region MarketRegionGene, regionMode bool, plan EvaluablePlan) (FitnessResult, error) {
	result := FitnessResult{}
	for _, market := range plan.MultiMarkets {
		if err := ctx.Err(); err != nil {
			return FitnessResult{}, err
		}
		c := quant.DefaultSeedChromosome
		var provider backtestcore.ParameterProvider
		if regionMode {
			if plan.ComputeStep != nil {
				plan.ComputeStep(marketRegionProviderUnits(region, market.Window.Bars))
			}
			var err error
			provider, err = newMarketRegionProviderWithCache(region, market.Window.Bars, market.MarketRegionCache)
			if err != nil {
				return FitnessResult{ScoreTotal: FatalFitnessScore, Fatal: true}, nil
			}
		} else {
			c = normalized.(quant.Chromosome)
		}
		path := runSigmoidDCAPathBacktestWithTraceAndMode(market.Window.Bars, market.Window.EvalStartMs, plan.Interval, plan.ExecutionMode, c, plan.Spawn, PathTraceConfig{
			Trace: plan.Trace, Mode: plan.TraceMode, TraceModeFunc: plan.TraceModeFunc, ComputeStep: plan.ComputeStep,
			Generation: plan.Generation, Individual: plan.Individual, Worker: plan.Worker, Window: "完整訓練期",
		}, plan.Costs, NormalizeGeneOptions(plan.GeneOptions).PositionStructure, market.Pair, plan.LongTermFilter, provider)
		if path.Err != nil {
			trace(plan, TraceModeSummary, "strategy", "market.evaluate.failed", "backtest failed", map[string]any{"error": path.Err.Error(), "market": market.InstrumentID})
			return FitnessResult{}, fmt.Errorf("market %s backtest failed: %w", market.InstrumentID, path.Err)
		}
		metrics := path.Metrics
		if metrics.MaxDrawdown > result.MaxDrawdown {
			result.MaxDrawdown = metrics.MaxDrawdown
		}
		years := float64(market.Window.Bars[len(market.Window.Bars)-1].OpenTime-market.Window.EvalStartMs) / float64(365*24*60*60*1000)
		if years <= 0 {
			return FitnessResult{}, fmt.Errorf("行情 %s 的可評估期間不足一年日", market.InstrumentID)
		}
		performance := MarketPerformance{
			InstrumentID: market.InstrumentID,
			Pair:         market.Pair,
			TotalReturn:  metrics.ROI,
			MaxDrawdown:  metrics.MaxDrawdown,
		}
		if 1+metrics.ROI <= 0 {
			result.Markets = append(result.Markets, performance)
			return FitnessResult{ScoreTotal: math.Inf(-1), MaxDrawdown: result.MaxDrawdown, Markets: result.Markets, Fatal: true}, nil
		}
		annualLogGrowth := math.Log1p(metrics.ROI) / years
		performance.AnnualizedReturn = math.Expm1(annualLogGrowth)
		result.Markets = append(result.Markets, performance)
		// The aggregate drawdown is the worst selected market.  The existing
		// strategy safety ceiling applies to that aggregate, so one unacceptable
		// market rejects the whole candidate.
		if result.MaxDrawdown >= 0.88 {
			result.ScoreTotal = FatalFitnessScore
			result.Fatal = true
			return result, nil
		}
		result.ScoreTotal += annualLogGrowth
	}
	return result, nil
}

func (SigmoidDCAEvolvable) DecodeElite(raw []byte) Gene {
	if len(raw) == 0 {
		return quant.DefaultSeedChromosome
	}
	if pack, ok := decodeMarketRegionParamPack(raw); ok {
		return pack.MarketRegion
	}
	var region MarketRegionGene
	if json.Unmarshal(raw, &region) == nil && region.SchemaVersion == MarketRegionSchemaVersion {
		return region
	}
	params := sigmoiddca.ParseParamsFromParamPack(raw)
	return params.Chromosome
}

func (e SigmoidDCAEvolvable) EncodeResult(g Gene, spawn *quant.SpawnPoint, options GeneOptions) ([]byte, error) {
	options = NormalizeGeneOptions(options)
	if region, ok := isMarketRegionGene(g); ok {
		normalized, err := normalizeMarketRegionGene(region, options)
		if err != nil {
			return nil, err
		}
		params := sigmoiddca.DefaultParams()
		params.PositionStructure = options.PositionStructure
		if spawn != nil {
			params.Spawn = *spawn
		}
		return json.Marshal(marketRegionParamPack{
			SchemaVersion:     MarketRegionSchemaVersion,
			Chromosome:        normalized.Global,
			Spawn:             params.Spawn,
			PositionStructure: params.PositionStructure,
			MarketRegion:      normalized,
		})
	}
	params := sigmoiddca.DefaultParams()
	params.Chromosome = e.NormalizeGene(g, options).(quant.Chromosome)
	if err := quant.ValidateChromosome(params.Chromosome); err != nil {
		return nil, err
	}
	params.PositionStructure = options.PositionStructure
	if spawn != nil {
		params.Spawn = *spawn
	}
	return json.Marshal(params)
}

func (SigmoidDCAEvolvable) Verify(ctx context.Context, g Gene, spawn *quant.SpawnPoint, bars []quant.Bar, _ float64, _ float64) (BacktestMetrics, error) {
	if err := ctx.Err(); err != nil {
		return BacktestMetrics{}, err
	}
	return RunSigmoidDCASingleBacktest(bars, firstEvalStart(bars), "1d", asChromosome(g), spawn), nil
}

func RunSigmoidDCASingleBacktest(bars []quant.Bar, evalStartMs int64, interval string, chromosome quant.Chromosome, spawn *quant.SpawnPoint) BacktestMetrics {
	return RunSigmoidDCAPathBacktest(bars, evalStartMs, interval, chromosome, spawn).Metrics
}

func RunSigmoidDCASingleBacktestWithMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint) BacktestMetrics {
	return RunSigmoidDCAPathBacktestWithMode(bars, evalStartMs, interval, executionMode, chromosome, spawn).Metrics
}

func RunSigmoidDCASingleBacktestWithModeAndCosts(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, costs quant.ExecutionCostConfig) BacktestMetrics {
	return RunSigmoidDCAPathBacktestWithModeAndCosts(bars, evalStartMs, interval, executionMode, chromosome, spawn, costs).Metrics
}

type PathTraceConfig struct {
	Trace         func(TraceEvent)
	Mode          TraceMode
	TraceModeFunc func() TraceMode
	ComputeStep   func(int64)
	Generation    int
	Individual    int
	Worker        int
	Window        string
}

func RunSigmoidDCASingleBacktestWithTrace(bars []quant.Bar, evalStartMs int64, interval string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig) BacktestMetrics {
	return RunSigmoidDCAPathBacktestWithTrace(bars, evalStartMs, interval, chromosome, spawn, traceCfg).Metrics
}

func RunSigmoidDCASingleBacktestWithTraceAndMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig) BacktestMetrics {
	return RunSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, traceCfg).Metrics
}

func RunSigmoidDCAPathBacktest(bars []quant.Bar, evalStartMs int64, interval string, chromosome quant.Chromosome, spawn *quant.SpawnPoint) SigmoidDCAPathResult {
	return RunSigmoidDCAPathBacktestWithTrace(bars, evalStartMs, interval, chromosome, spawn, PathTraceConfig{})
}

func RunSigmoidDCAPathBacktestWithMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint) SigmoidDCAPathResult {
	return RunSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, PathTraceConfig{})
}

func RunSigmoidDCAPathBacktestWithModeAndCosts(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, costs quant.ExecutionCostConfig) SigmoidDCAPathResult {
	return runSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, PathTraceConfig{}, costs, sigmoiddca.PositionStructureDualLayer, "BTCUSDT", backtestcore.LongTermFilterConfig{})
}

func RunSigmoidDCAPathBacktestWithModeCostsAndStructure(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, costs quant.ExecutionCostConfig, positionStructure string) SigmoidDCAPathResult {
	return RunSigmoidDCAPathBacktestForInstrument(bars, evalStartMs, "BTCUSDT", interval, executionMode, chromosome, spawn, costs, positionStructure)
}

func RunSigmoidDCAPathBacktestForInstrument(bars []quant.Bar, evalStartMs int64, symbol string, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, costs quant.ExecutionCostConfig, positionStructure string) SigmoidDCAPathResult {
	return runSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, PathTraceConfig{}, costs, positionStructure, symbol, backtestcore.LongTermFilterConfig{})
}

func RunSigmoidDCAPathBacktestWithTrace(bars []quant.Bar, evalStartMs int64, interval string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig) SigmoidDCAPathResult {
	return RunSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionModeCloseSameBar, chromosome, spawn, traceCfg)
}

func RunSigmoidDCAPathBacktestWithTraceAndMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig) SigmoidDCAPathResult {
	return runSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, traceCfg, quant.ExecutionCostConfig{}, sigmoiddca.PositionStructureDualLayer, "BTCUSDT", backtestcore.LongTermFilterConfig{})
}

func runSigmoidDCAPathBacktestWithTraceAndMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig, costs quant.ExecutionCostConfig, positionStructure string, symbol string, longTermFilter backtestcore.LongTermFilterConfig, provider ...backtestcore.ParameterProvider) SigmoidDCAPathResult {
	executionMode = normalizeBacktestExecutionMode(executionMode)
	if len(bars) == 0 || bars[0].Close <= 0 || executionMode == executionModePreclose10m {
		return SigmoidDCAPathResult{}
	}
	params := sigmoiddca.DefaultParams()
	params.Chromosome = quant.ClampChromosome(chromosome)
	params.PositionStructure = sigmoiddca.NormalizePositionStructure(positionStructure)
	if spawn != nil {
		params.Spawn = *spawn
	}
	hooks := backtestcore.Hooks{ComputeStep: traceCfg.ComputeStep}
	if traceCfg.Trace != nil {
		hooks.OnStep = func(event backtestcore.StepEvent) {
			if !TraceEnabled(activePathTraceMode(traceCfg), TraceModeFull) {
				return
			}
			tracePath(traceCfg, TraceModeFull, "strategy", "step.computed", "strategy step computed", map[string]any{
				"generation":      traceCfg.Generation,
				"individual":      traceCfg.Individual,
				"worker":          traceCfg.Worker,
				"window":          traceCfg.Window,
				"bar_index":       event.Index,
				"open_time":       event.Bar.OpenTime,
				"close":           event.Bar.Close,
				"execution_mode":  event.ExecutionMode,
				"total_equity":    event.TotalEquity,
				"usdt_balance":    event.Portfolio.USDTBalance,
				"dead_btc":        event.Portfolio.DeadBTC,
				"float_btc":       event.Portfolio.FloatBTC,
				"cold_sealed_btc": event.Portfolio.ColdSealedBTC,
				"intents":         len(event.Output.Intents),
				"lot_transfers":   len(event.Output.LotTransfers),
				"diagnostics":     event.Output.Diagnostics,
			})
		}
	}
	var parameterProvider backtestcore.ParameterProvider
	if len(provider) > 0 {
		parameterProvider = provider[0]
	}
	result, err := backtestcore.RunSigmoidDCA(backtestcore.SigmoidDCARequest{
		Spec: backtestcore.Spec{
			Symbol:               symbol,
			Interval:             interval,
			ExecutionMode:        executionMode,
			PositionStructure:    params.PositionStructure,
			StartTimeMs:          bars[0].OpenTime,
			EndTimeMs:            bars[len(bars)-1].OpenTime,
			EvaluationStartMs:    evalStartMs,
			EvaluationEndMs:      bars[len(bars)-1].OpenTime,
			InitialCapital:       params.Spawn.Policy.InitialUSDT,
			MonthlyContribution:  params.Spawn.Policy.MonthlyInjectUSDT,
			InitialAssetQuantity: params.Spawn.Policy.ColdSealedBTC,
			Costs:                costs,
			LongTermFilter:       longTermFilter,
		},
		Bars:              bars,
		Params:            params,
		ParameterProvider: parameterProvider,
		Hooks:             hooks,
	})
	if err != nil {
		return SigmoidDCAPathResult{Err: err}
	}
	nav := make([]float64, 0, len(result.Path))
	for _, point := range result.Path {
		nav = append(nav, point.TotalEquity)
	}
	metrics := BacktestMetrics{
		ROI:           result.TotalReturn,
		MaxDrawdown:   quant.MaxDrawdown(nav),
		FinalEquity:   result.FinalAssets,
		TotalInjected: result.TotalInjected,
		TradeCount:    result.TradeCount,
	}
	return SigmoidDCAPathResult{
		Metrics: metrics,
		NAV:     result.Path,
	}
}

func applyForceTargetThresholds(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, chromosome quant.Chromosome, modelTargetWeight float64) (quant.StrategyOutput, float64) {
	return backtestcore.ApplyForceTargetThresholds(output, portfolio, price, chromosome, modelTargetWeight)
}

func normalizeBacktestExecutionMode(mode string) string {
	switch mode {
	case executionModeCloseNextOpen, executionModePreclose10m:
		return mode
	default:
		return executionModeCloseSameBar
	}
}

func usesNextOpenExecution(mode string) bool {
	return normalizeBacktestExecutionMode(mode) == executionModeCloseNextOpen
}

func trace(plan EvaluablePlan, required TraceMode, source string, scope string, message string, fields map[string]any) {
	if plan.Trace == nil || !TraceEnabled(activePlanTraceMode(plan), required) {
		return
	}
	plan.Trace(TraceEvent{
		RequiredMode: required,
		Level:        "trace",
		Source:       source,
		Scope:        scope,
		Message:      message,
		Fields:       fields,
	})
}

func tracePath(traceCfg PathTraceConfig, required TraceMode, source string, scope string, message string, fields map[string]any) {
	if traceCfg.Trace == nil || !TraceEnabled(activePathTraceMode(traceCfg), required) {
		return
	}
	traceCfg.Trace(TraceEvent{
		RequiredMode: required,
		Level:        "trace",
		Source:       source,
		Scope:        scope,
		Message:      message,
		Fields:       fields,
	})
}

func activePlanTraceMode(plan EvaluablePlan) TraceMode {
	if plan.TraceModeFunc != nil {
		return NormalizeTraceMode(plan.TraceModeFunc())
	}
	return NormalizeTraceMode(plan.TraceMode)
}

func activePathTraceMode(traceCfg PathTraceConfig) TraceMode {
	if traceCfg.TraceModeFunc != nil {
		return NormalizeTraceMode(traceCfg.TraceModeFunc())
	}
	return NormalizeTraceMode(traceCfg.Mode)
}

func applyRebalanceThreshold(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, threshold float64) quant.StrategyOutput {
	return backtestcore.ApplyRebalanceThreshold(output, portfolio, price, threshold)
}

func rebalanceThresholdAllows(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, threshold float64) bool {
	return backtestcore.RebalanceThresholdAllows(output, portfolio, price, threshold)
}

func applyBacktestOutput(portfolio quant.PortfolioSnapshot, output quant.StrategyOutput, price float64) quant.PortfolioSnapshot {
	return applyBacktestOutputWithCosts(portfolio, output, price, quant.ExecutionCostConfig{})
}

func applyBacktestOutputWithCosts(portfolio quant.PortfolioSnapshot, output quant.StrategyOutput, price float64, costs quant.ExecutionCostConfig) quant.PortfolioSnapshot {
	updated, _ := backtestcore.ApplyStrategyOutput(portfolio, output, price, backtestcore.SimulatorConfig{Costs: costs})
	return updated
}

func asChromosome(g Gene) quant.Chromosome {
	if c, ok := g.(quant.Chromosome); ok {
		return quant.ClampChromosome(c)
	}
	return quant.DefaultSeedChromosome
}

func sampleRange(rng RandomSource, name string) float64 {
	minimum, maximum := searchLatticeBounds(quant.HardBounds[name])
	return float64(minimum+rng.Intn(maximum-minimum+1)) * searchParameterStep
}

func mutateFloat(v float64, name string, prob float64, scale float64, rng RandomSource) float64 {
	bound := quant.HardBounds[name]
	if rng.Float64() < prob {
		steps := int(math.Round(rng.NormFloat64() * math.Max(1, scale)))
		if steps == 0 {
			if rng.Float64() < 0.5 {
				steps = -1
			} else {
				steps = 1
			}
		}
		v = quantizeSearchValue(v, bound) + float64(steps)*searchParameterStep
	}
	return quantizeSearchValue(v, bound)
}

func sampleRangeAtLeast(rng RandomSource, name string, minimum float64) float64 {
	minStep, maxStep := searchLatticeBounds(quant.HardBounds[name])
	required := int(math.Ceil((minimum - 1e-9) / searchParameterStep))
	if required > minStep {
		minStep = required
	}
	if minStep > maxStep {
		return float64(maxStep) * searchParameterStep
	}
	return float64(minStep+rng.Intn(maxStep-minStep+1)) * searchParameterStep
}

func searchLatticeBounds(bound quant.Bound) (int, int) {
	minimum := int(math.Ceil(bound.Min/searchParameterStep - 1e-9))
	maximum := int(math.Floor(bound.Max/searchParameterStep + 1e-9))
	return minimum, maximum
}

func pick(rng RandomSource, a float64, b float64) float64 {
	if rng.Float64() < 0.5 {
		return a
	}
	return b
}

func writeQuantized(h interface{ Write([]byte) (int, error) }, v float64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(math.Round(v*1e6)))
	_, _ = h.Write(buf[:])
}

func firstEvalStart(bars []quant.Bar) int64 {
	if len(bars) == 0 {
		return 0
	}
	return bars[0].OpenTime
}
