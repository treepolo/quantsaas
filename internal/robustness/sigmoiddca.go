package robustness

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

type ParameterDefinition struct {
	Name        string        `json:"name"`
	Label       string        `json:"label"`
	Type        ParameterType `json:"type"`
	LegalMin    float64       `json:"legal_min"`
	LegalMax    float64       `json:"legal_max"`
	DefaultStep float64       `json:"default_step"`
	Active      bool          `json:"active"`
}

var parameterLabels = map[string]string{
	"micro_reserve_pct":         "資金保留比例",
	"beta":                      "訊號反應係數",
	"gamma":                     "倉位回饋係數",
	"w_mean":                    "均值回歸權重",
	"w_momentum":                "動能權重",
	"w_breakout":                "突破權重",
	"dust_usd":                  "最小有效調整金額",
	"rebalance_threshold":       "調倉門檻",
	"force_full_threshold":      "強制滿倉門檻",
	"force_empty_threshold":     "強制空倉門檻",
	"wedge_delta_threshold":     "小額調整突破門檻",
	"wedge_vol_ratio_threshold": "波動突破門檻",
	"macro_bear_multiplier":     "弱勢環境投入倍率",
	"macro_bull_multiplier":     "強勢環境投入倍率",
	"extra_deploy_pct":          "額外資金投入比例",
	"soft_release_months":       "長期部位持有月數",
	"soft_release_pct":          "長期部位單次轉用比例",
	"hard_release_max_pct":      "長期部位最大轉用比例",
}

var parameterOrder = []string{
	"micro_reserve_pct", "beta", "gamma", "w_mean", "w_momentum", "w_breakout", "dust_usd",
	"rebalance_threshold", "force_full_threshold", "force_empty_threshold", "wedge_delta_threshold",
	"wedge_vol_ratio_threshold", "macro_bear_multiplier", "macro_bull_multiplier", "extra_deploy_pct",
	"soft_release_months", "soft_release_pct", "hard_release_max_pct",
}

func SigmoidDCAParameterDefinitions(positionStructure string) []ParameterDefinition {
	floatingOnly := sigmoiddca.NormalizePositionStructure(positionStructure) == sigmoiddca.PositionStructureFloatingOnly
	macro := map[string]bool{
		"macro_bear_multiplier": true, "macro_bull_multiplier": true, "extra_deploy_pct": true,
		"soft_release_months": true, "soft_release_pct": true, "hard_release_max_pct": true,
	}
	result := make([]ParameterDefinition, 0, len(parameterOrder))
	for _, name := range parameterOrder {
		bound := quant.HardBounds[name]
		kind, step := ParameterFloat, 0.05
		if name == "soft_release_months" {
			kind, step = ParameterInt, 1
		}
		result = append(result, ParameterDefinition{
			Name: name, Label: parameterLabels[name], Type: kind,
			LegalMin: bound.Min, LegalMax: bound.Max, DefaultStep: step,
			Active: !(floatingOnly && macro[name]),
		})
	}
	return result
}

func BuildLocalSpace(params sigmoiddca.Params, axisNames []string, radius int, customSteps map[string]float64) (ParameterSpace, error) {
	if radius < 1 || len(axisNames) == 0 {
		return ParameterSpace{}, ErrInvalidSchema
	}
	definitions := SigmoidDCAParameterDefinitions(params.PositionStructure)
	byName := make(map[string]ParameterDefinition, len(definitions))
	for _, definition := range definitions {
		byName[definition.Name] = definition
	}
	fixed := ChromosomeValues(params.Chromosome)
	space := ParameterSpace{SchemaVersion: GridVersion, Fixed: fixed}
	seen := map[string]bool{}
	for _, rawName := range axisNames {
		name := strings.TrimSpace(rawName)
		definition, exists := byName[name]
		if !exists || !definition.Active || seen[name] {
			return ParameterSpace{}, fmt.Errorf("%w: unsupported research dimension %s", ErrInvalidSchema, name)
		}
		seen[name] = true
		step := definition.DefaultStep
		if customSteps != nil && customSteps[name] > 0 {
			step = customSteps[name]
		}
		center := fixed[name]
		if definition.Type == ParameterFloat {
			center = math.Round(center*100) / 100
		}
		values := AxisValues(center, definition.LegalMin, definition.LegalMax, step, radius, definition.Type)
		if len(values) == 0 {
			return ParameterSpace{}, ErrInvalidSchema
		}
		delete(space.Fixed, name)
		space.Axes = append(space.Axes, ParameterAxis{
			Name: name, Label: definition.Label, Type: definition.Type, Values: values,
			LegalMin: definition.LegalMin, LegalMax: definition.LegalMax, Step: step,
			StudyStart: 0, StudyEnd: len(values) - 1,
		})
	}
	points, err := Enumerate(space)
	if err != nil {
		return ParameterSpace{}, err
	}
	for _, point := range points {
		chromosome, err := ChromosomeWithValues(params.Chromosome, point.Parameters)
		if err != nil || quant.ValidateChromosome(chromosome) != nil {
			space.ExcludedCoordinates = append(space.ExcludedCoordinates, append([]int(nil), point.Coordinates...))
		}
	}
	sort.Slice(space.ExcludedCoordinates, func(i, j int) bool {
		return CoordinateKey(space.ExcludedCoordinates[i]) < CoordinateKey(space.ExcludedCoordinates[j])
	})
	return space, ValidateSpace(space)
}

func ChromosomeValues(c quant.Chromosome) map[string]float64 {
	return map[string]float64{
		"micro_reserve_pct": c.MicroReservePct, "beta": c.Beta, "gamma": c.Gamma,
		"w_mean": c.WMean, "w_momentum": c.WMomentum, "w_breakout": c.WBreakout,
		"dust_usd": c.DustUSD, "rebalance_threshold": c.RebalanceThreshold,
		"force_full_threshold": c.ForceFullThreshold, "force_empty_threshold": c.ForceEmptyThreshold,
		"wedge_delta_threshold": c.WedgeDeltaThreshold, "wedge_vol_ratio_threshold": c.WedgeVolRatioThreshold,
		"macro_bear_multiplier": c.MacroBearMultiplier, "macro_bull_multiplier": c.MacroBullMultiplier,
		"extra_deploy_pct": c.ExtraDeployPct, "soft_release_months": float64(c.SoftReleaseMonths),
		"soft_release_pct": c.SoftReleasePct, "hard_release_max_pct": c.HardReleaseMaxPct,
	}
}

func ChromosomeWithValues(base quant.Chromosome, values map[string]float64) (quant.Chromosome, error) {
	for name, value := range values {
		bound, exists := quant.HardBounds[name]
		if !exists || math.IsNaN(value) || math.IsInf(value, 0) || value < bound.Min-1e-9 || value > bound.Max+1e-9 {
			return base, fmt.Errorf("%w: %s", ErrInvalidPoint, name)
		}
		switch name {
		case "micro_reserve_pct":
			base.MicroReservePct = value
		case "beta":
			base.Beta = value
		case "gamma":
			base.Gamma = value
		case "w_mean":
			base.WMean = value
		case "w_momentum":
			base.WMomentum = value
		case "w_breakout":
			base.WBreakout = value
		case "dust_usd":
			base.DustUSD = value
		case "rebalance_threshold":
			base.RebalanceThreshold = value
		case "force_full_threshold":
			base.ForceFullThreshold = value
		case "force_empty_threshold":
			base.ForceEmptyThreshold = value
		case "wedge_delta_threshold":
			base.WedgeDeltaThreshold = value
		case "wedge_vol_ratio_threshold":
			base.WedgeVolRatioThreshold = value
		case "macro_bear_multiplier":
			base.MacroBearMultiplier = value
		case "macro_bull_multiplier":
			base.MacroBullMultiplier = value
		case "extra_deploy_pct":
			base.ExtraDeployPct = value
		case "soft_release_months":
			if math.Abs(value-math.Round(value)) > 1e-9 {
				return base, ErrInvalidPoint
			}
			base.SoftReleaseMonths = int(math.Round(value))
		case "soft_release_pct":
			base.SoftReleasePct = value
		case "hard_release_max_pct":
			base.HardReleaseMaxPct = value
		}
	}
	if err := quant.ValidateChromosome(base); err != nil {
		return base, err
	}
	return base, nil
}
