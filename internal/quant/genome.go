package quant

import "fmt"

type Chromosome struct {
	MicroReservePct        float64 `json:"micro_reserve_pct"`
	Beta                   float64 `json:"beta"`
	Gamma                  float64 `json:"gamma"`
	WMean                  float64 `json:"w_mean"`
	WMomentum              float64 `json:"w_momentum"`
	WBreakout              float64 `json:"w_breakout"`
	ExternalSignalWeight   float64 `json:"external_signal_weight"`
	DustUSD                float64 `json:"dust_usd"`
	RebalanceThreshold     float64 `json:"rebalance_threshold"`
	ForceFullThreshold     float64 `json:"force_full_threshold"`
	ForceEmptyThreshold    float64 `json:"force_empty_threshold"`
	WedgeDeltaThreshold    float64 `json:"wedge_delta_threshold"`
	WedgeVolRatioThreshold float64 `json:"wedge_vol_ratio_threshold"`
	MacroBearMultiplier    float64 `json:"macro_bear_multiplier"`
	MacroBullMultiplier    float64 `json:"macro_bull_multiplier"`
	ExtraDeployPct         float64 `json:"extra_deploy_pct"`
	SoftReleaseMonths      int     `json:"soft_release_months"`
	SoftReleasePct         float64 `json:"soft_release_pct"`
	HardReleaseMaxPct      float64 `json:"hard_release_max_pct"`
}

type Bound struct {
	Min float64
	Max float64
}

var HardBounds = map[string]Bound{
	"micro_reserve_pct":         {Min: 0.10, Max: 0.45},
	"beta":                      {Min: 0.20, Max: 8.00},
	"gamma":                     {Min: 0.00, Max: 5.00},
	"w_mean":                    {Min: -3.00, Max: 3.00},
	"w_momentum":                {Min: -3.00, Max: 3.00},
	"w_breakout":                {Min: -3.00, Max: 3.00},
	"external_signal_weight":    {Min: -3.00, Max: 3.00},
	"dust_usd":                  {Min: 5.00, Max: 25.00},
	"rebalance_threshold":       {Min: 0.00, Max: 1.00},
	"force_full_threshold":      {Min: 0.00, Max: 1.00},
	"force_empty_threshold":     {Min: 0.00, Max: 1.00},
	"wedge_delta_threshold":     {Min: 0.01, Max: 0.15},
	"wedge_vol_ratio_threshold": {Min: 1.10, Max: 2.50},
	"macro_bear_multiplier":     {Min: 1.00, Max: 2.50},
	"macro_bull_multiplier":     {Min: 0.20, Max: 1.00},
	"extra_deploy_pct":          {Min: 0.00, Max: 0.60},
	"soft_release_months":       {Min: 3, Max: 36},
	"soft_release_pct":          {Min: 0.00, Max: 0.25},
	"hard_release_max_pct":      {Min: 0.00, Max: 0.50},
}

var DefaultSeedChromosome = Chromosome{
	MicroReservePct:        0.25,
	Beta:                   2.00,
	Gamma:                  1.00,
	WMean:                  1.00,
	WMomentum:              -0.50,
	WBreakout:              0.75,
	ExternalSignalWeight:   0.00,
	DustUSD:                10.10,
	RebalanceThreshold:     0.00,
	ForceFullThreshold:     1.00,
	ForceEmptyThreshold:    0.00,
	WedgeDeltaThreshold:    0.04,
	WedgeVolRatioThreshold: 1.60,
	MacroBearMultiplier:    1.40,
	MacroBullMultiplier:    0.60,
	ExtraDeployPct:         0.20,
	SoftReleaseMonths:      12,
	SoftReleasePct:         0.08,
	HardReleaseMaxPct:      0.15,
}

var GeneSteps = map[string]float64{
	"micro_reserve_pct":         0.01,
	"beta":                      0.10,
	"gamma":                     0.10,
	"w_mean":                    0.10,
	"w_momentum":                0.10,
	"w_breakout":                0.10,
	"external_signal_weight":    0.10,
	"dust_usd":                  0.50,
	"rebalance_threshold":       0.005,
	"force_full_threshold":      0.01,
	"force_empty_threshold":     0.01,
	"wedge_delta_threshold":     0.005,
	"wedge_vol_ratio_threshold": 0.05,
	"macro_bear_multiplier":     0.05,
	"macro_bull_multiplier":     0.05,
	"extra_deploy_pct":          0.02,
	"soft_release_months":       1,
	"soft_release_pct":          0.01,
	"hard_release_max_pct":      0.02,
}

func ClampChromosome(c Chromosome) Chromosome {
	c.MicroReservePct = clampByName(c.MicroReservePct, "micro_reserve_pct")
	c.Beta = clampByName(c.Beta, "beta")
	c.Gamma = clampByName(c.Gamma, "gamma")
	c.WMean = clampByName(c.WMean, "w_mean")
	c.WMomentum = clampByName(c.WMomentum, "w_momentum")
	c.WBreakout = clampByName(c.WBreakout, "w_breakout")
	c.ExternalSignalWeight = clampByName(c.ExternalSignalWeight, "external_signal_weight")
	c.DustUSD = clampByName(c.DustUSD, "dust_usd")
	c.RebalanceThreshold = clampByName(c.RebalanceThreshold, "rebalance_threshold")
	c.ForceFullThreshold = clampByName(c.ForceFullThreshold, "force_full_threshold")
	c.ForceEmptyThreshold = clampByName(c.ForceEmptyThreshold, "force_empty_threshold")
	c.WedgeDeltaThreshold = clampByName(c.WedgeDeltaThreshold, "wedge_delta_threshold")
	c.WedgeVolRatioThreshold = clampByName(c.WedgeVolRatioThreshold, "wedge_vol_ratio_threshold")
	c.MacroBearMultiplier = clampByName(c.MacroBearMultiplier, "macro_bear_multiplier")
	c.MacroBullMultiplier = clampByName(c.MacroBullMultiplier, "macro_bull_multiplier")
	c.ExtraDeployPct = clampByName(c.ExtraDeployPct, "extra_deploy_pct")
	c.SoftReleaseMonths = int(clampByName(float64(c.SoftReleaseMonths), "soft_release_months"))
	c.SoftReleasePct = clampByName(c.SoftReleasePct, "soft_release_pct")
	c.HardReleaseMaxPct = clampByName(c.HardReleaseMaxPct, "hard_release_max_pct")

	if c.MacroBearMultiplier < c.MacroBullMultiplier {
		c.MacroBearMultiplier = c.MacroBullMultiplier
	}
	return c
}

func ValidateChromosome(c Chromosome) error {
	c = ClampChromosome(c)
	if c.DustUSD <= 0 {
		return fmt.Errorf("dust_usd must be positive")
	}
	if c.MacroBearMultiplier < c.MacroBullMultiplier {
		return fmt.Errorf("macro_bear_multiplier must be >= macro_bull_multiplier")
	}
	if c.ForceFullThreshold < c.ForceEmptyThreshold {
		return fmt.Errorf("滿倉閾值低於空倉閾值，參數無效")
	}
	return nil
}

func clampByName(v float64, name string) float64 {
	bound := HardBounds[name]
	return ClipFloat64(v, bound.Min, bound.Max)
}
