package dynamicparam

import (
	"fmt"
	"math"
	"sort"

	"quantsaas/internal/quant"
)

const (
	ControlFixed      = "fixed"
	ControlGlobal     = "global"
	ControlContinuous = "continuous"
	ControlSixState   = "six_state"
)

type ContinuousTerm struct {
	Input     string  `json:"input"`
	Linear    float64 `json:"linear"`
	Quadratic float64 `json:"quadratic"`
}

type SixStateEffects struct {
	Direction   map[string]float64 `json:"direction,omitempty"`
	Volatility  map[string]float64 `json:"volatility,omitempty"`
	Interaction map[string]float64 `json:"interaction,omitempty"`
}

type ParameterControl struct {
	ParameterID string           `json:"parameter_id"`
	Mode        string           `json:"mode"`
	Lower       float64          `json:"lower"`
	Upper       float64          `json:"upper"`
	BaseValue   float64          `json:"base_value"`
	GlobalValue float64          `json:"global_value,omitempty"`
	BaseLogit   float64          `json:"base_logit,omitempty"`
	Terms       []ContinuousTerm `json:"terms,omitempty"`
	Effects     SixStateEffects  `json:"effects,omitempty"`
}

type DynamicPolicy struct {
	SchemaVersion string             `json:"schema_version"`
	Version       string             `json:"version"`
	Controls      []ParameterControl `json:"controls"`
	EvolveGamma   bool               `json:"evolve_gamma"`
}

type PolicyInput struct {
	Index          int
	TimeMs         int64
	State          StructureState
	Signals        map[string]*float64
	FallbackReason string
}

type ParameterVariable struct {
	StableID        string  `json:"stable_id"`
	ParameterID     string  `json:"parameter_id"`
	ControlMode     string  `json:"control_mode"`
	Role            string  `json:"role"`
	Lower           float64 `json:"lower"`
	Upper           float64 `json:"upper"`
	MinimumStep     float64 `json:"minimum_step"`
	DisplayDecimals int     `json:"display_decimals"`
	PredictionInput string  `json:"prediction_input,omitempty"`
}

type DynamicParameterSpaceSchema struct {
	SchemaVersion           string              `json:"schema_version"`
	ModelArtifactHash       string              `json:"model_artifact_hash"`
	PredictionSchemaVersion string              `json:"prediction_schema_version"`
	PolicyVersion           string              `json:"policy_version"`
	Variables               []ParameterVariable `json:"variables"`
	StructuralConstraints   []string            `json:"structural_constraints"`
}

func ValidatePolicy(policy DynamicPolicy) error {
	if policy.SchemaVersion != PolicySchemaVersion || policy.Version == "" {
		return fmt.Errorf("invalid dynamic policy version")
	}
	seen := map[string]bool{}
	for _, control := range policy.Controls {
		if seen[control.ParameterID] {
			return fmt.Errorf("duplicate parameter control %q", control.ParameterID)
		}
		seen[control.ParameterID] = true
		bound, ok := quant.HardBounds[control.ParameterID]
		if !ok || control.Lower != bound.Min || control.Upper != bound.Max || control.Lower >= control.Upper {
			return fmt.Errorf("parameter %q does not use the canonical legal range", control.ParameterID)
		}
		if control.BaseValue < control.Lower || control.BaseValue > control.Upper || !finite(control.BaseValue) {
			return fmt.Errorf("parameter %q has an invalid base value", control.ParameterID)
		}
		switch control.Mode {
		case ControlFixed:
		case ControlGlobal:
			if control.GlobalValue < control.Lower || control.GlobalValue > control.Upper || !finite(control.GlobalValue) {
				return fmt.Errorf("parameter %q has an invalid global value", control.ParameterID)
			}
		case ControlContinuous:
			inputs := map[string]bool{}
			for _, term := range control.Terms {
				if term.Input == "" || inputs[term.Input] || !finite(term.Linear) || !finite(term.Quadratic) {
					return fmt.Errorf("parameter %q has an invalid continuous term", control.ParameterID)
				}
				inputs[term.Input] = true
			}
		case ControlSixState:
			if err := validateSumToZero(control.Effects); err != nil {
				return fmt.Errorf("parameter %q: %w", control.ParameterID, err)
			}
		default:
			return fmt.Errorf("parameter %q has unsupported control mode %q", control.ParameterID, control.Mode)
		}
		if control.ParameterID == "gamma" && !policy.EvolveGamma && control.Mode != ControlFixed {
			return fmt.Errorf("Gamma cannot be enabled by a dynamic policy")
		}
	}
	return nil
}

func ApplyPolicy(base quant.Chromosome, policy DynamicPolicy, input PolicyInput) (EffectiveSnapshot, error) {
	if err := ValidatePolicy(policy); err != nil {
		return EffectiveSnapshot{}, err
	}
	result := base
	contributions := make(map[string]Contribution, len(policy.Controls))
	fallbacks := make([]string, 0)
	for _, control := range policy.Controls {
		value := control.BaseValue
		contribution := Contribution{Mode: control.Mode, BaseValue: control.BaseValue, Terms: map[string]float64{}}
		switch control.Mode {
		case ControlFixed:
			value = control.BaseValue
		case ControlGlobal:
			value = control.GlobalValue
		case ControlContinuous:
			q := control.BaseLogit
			available := true
			for _, term := range control.Terms {
				signal, ok := input.Signals[term.Input]
				if !ok || signal == nil || !finite(*signal) || *signal < -1 || *signal > 1 {
					available = false
					break
				}
				termValue := term.Linear*(*signal) + term.Quadratic*(*signal)*(*signal)
				q += termValue
				contribution.Terms[term.Input] = termValue
			}
			if available {
				value = boundedSigmoid(control.Lower, control.Upper, q)
			} else {
				value = control.BaseValue
				fallbacks = append(fallbacks, control.ParameterID+":prediction_unavailable")
			}
		case ControlSixState:
			q := control.BaseLogit
			direction := control.Effects.Direction[input.State.Direction]
			volatility := control.Effects.Volatility[input.State.Volatility]
			interaction := control.Effects.Interaction[input.State.Direction+"_"+input.State.Volatility]
			q += direction + volatility + interaction
			contribution.Terms["direction"] = direction
			contribution.Terms["volatility"] = volatility
			contribution.Terms["interaction"] = interaction
			value = boundedSigmoid(control.Lower, control.Upper, q)
		}
		if err := setChromosomeValue(&result, control.ParameterID, value); err != nil {
			return EffectiveSnapshot{}, err
		}
		contribution.FinalValue = value
		contributions[control.ParameterID] = contribution
	}
	if err := validateEffectiveChromosome(result); err != nil {
		return EffectiveSnapshot{}, fmt.Errorf("daily effective parameters violate structural constraints: %w", err)
	}
	return EffectiveSnapshot{
		SchemaVersion: EffectiveParameterVersion, Index: input.Index, TimeMs: input.TimeMs, State: input.State,
		Chromosome: result, Contributions: contributions, FallbackEvents: fallbacks,
	}, nil
}

func BuildParameterSpace(policy DynamicPolicy, artifactHash string) (DynamicParameterSpaceSchema, error) {
	if err := ValidatePolicy(policy); err != nil {
		return DynamicParameterSpaceSchema{}, err
	}
	variables := make([]ParameterVariable, 0)
	for _, control := range policy.Controls {
		switch control.Mode {
		case ControlGlobal:
			variables = append(variables, parameterVariable(control, "global", ""))
		case ControlContinuous:
			variables = append(variables, parameterVariable(control, "base_logit", ""))
			for _, term := range control.Terms {
				variables = append(variables,
					parameterVariable(control, "linear", term.Input),
					parameterVariable(control, "quadratic", term.Input),
				)
			}
		case ControlSixState:
			for _, role := range []string{"direction", "volatility", "interaction"} {
				variables = append(variables, parameterVariable(control, role, ""))
			}
		}
	}
	sort.Slice(variables, func(i, j int) bool { return variables[i].StableID < variables[j].StableID })
	return DynamicParameterSpaceSchema{
		SchemaVersion: ParameterSpaceVersion, ModelArtifactHash: artifactHash,
		PredictionSchemaVersion: PredictionSchemaVersion, PolicyVersion: policy.Version, Variables: variables,
		StructuralConstraints: []string{"ForceFullThreshold >= ForceEmptyThreshold", "MacroBearMultiplier >= MacroBullMultiplier"},
	}, nil
}

func parameterVariable(control ParameterControl, role string, input string) ParameterVariable {
	id := control.ParameterID + ":" + control.Mode + ":" + role
	if input != "" {
		id += ":" + input
	}
	lower, upper, step := control.Lower, control.Upper, (control.Upper-control.Lower)/100
	if role != "global" {
		lower, upper, step = -8, 8, 0.05
	}
	return ParameterVariable{StableID: id, ParameterID: control.ParameterID, ControlMode: control.Mode, Role: role, Lower: lower, Upper: upper, MinimumStep: step, DisplayDecimals: 2, PredictionInput: input}
}

func validateSumToZero(effects SixStateEffects) error {
	for name, values := range map[string]map[string]float64{"direction": effects.Direction, "volatility": effects.Volatility, "interaction": effects.Interaction} {
		if len(values) == 0 {
			continue
		}
		sum := 0.0
		for _, value := range values {
			if !finite(value) {
				return fmt.Errorf("%s effect is not finite", name)
			}
			sum += value
		}
		if math.Abs(sum) > 1e-9 {
			return fmt.Errorf("%s effects must sum to zero", name)
		}
	}
	return nil
}

func boundedSigmoid(lower, upper, q float64) float64 {
	if q >= 0 {
		exp := math.Exp(-q)
		return lower + (upper-lower)/(1+exp)
	}
	exp := math.Exp(q)
	return lower + (upper-lower)*exp/(1+exp)
}

func setChromosomeValue(chromosome *quant.Chromosome, name string, value float64) error {
	switch name {
	case "micro_reserve_pct":
		chromosome.MicroReservePct = value
	case "beta":
		chromosome.Beta = value
	case "gamma":
		chromosome.Gamma = value
	case "w_mean":
		chromosome.WMean = value
	case "w_momentum":
		chromosome.WMomentum = value
	case "w_breakout":
		chromosome.WBreakout = value
	case "dust_usd":
		chromosome.DustUSD = value
	case "rebalance_threshold":
		chromosome.RebalanceThreshold = value
	case "force_full_threshold":
		chromosome.ForceFullThreshold = value
	case "force_empty_threshold":
		chromosome.ForceEmptyThreshold = value
	case "wedge_delta_threshold":
		chromosome.WedgeDeltaThreshold = value
	case "wedge_vol_ratio_threshold":
		chromosome.WedgeVolRatioThreshold = value
	case "macro_bear_multiplier":
		chromosome.MacroBearMultiplier = value
	case "macro_bull_multiplier":
		chromosome.MacroBullMultiplier = value
	case "extra_deploy_pct":
		chromosome.ExtraDeployPct = value
	case "soft_release_months":
		chromosome.SoftReleaseMonths = int(math.Round(value))
	case "soft_release_pct":
		chromosome.SoftReleasePct = value
	case "hard_release_max_pct":
		chromosome.HardReleaseMaxPct = value
	default:
		return fmt.Errorf("unsupported dynamic parameter %q", name)
	}
	return nil
}

func validateEffectiveChromosome(chromosome quant.Chromosome) error {
	values := map[string]float64{
		"micro_reserve_pct": chromosome.MicroReservePct, "beta": chromosome.Beta, "gamma": chromosome.Gamma,
		"w_mean": chromosome.WMean, "w_momentum": chromosome.WMomentum, "w_breakout": chromosome.WBreakout,
		"dust_usd": chromosome.DustUSD, "rebalance_threshold": chromosome.RebalanceThreshold,
		"force_full_threshold": chromosome.ForceFullThreshold, "force_empty_threshold": chromosome.ForceEmptyThreshold,
		"wedge_delta_threshold": chromosome.WedgeDeltaThreshold, "wedge_vol_ratio_threshold": chromosome.WedgeVolRatioThreshold,
		"macro_bear_multiplier": chromosome.MacroBearMultiplier, "macro_bull_multiplier": chromosome.MacroBullMultiplier,
		"extra_deploy_pct": chromosome.ExtraDeployPct, "soft_release_months": float64(chromosome.SoftReleaseMonths),
		"soft_release_pct": chromosome.SoftReleasePct, "hard_release_max_pct": chromosome.HardReleaseMaxPct,
	}
	for name, value := range values {
		bound := quant.HardBounds[name]
		if !finite(value) || value < bound.Min || value > bound.Max {
			return fmt.Errorf("%s is outside the legal range", name)
		}
	}
	if chromosome.ForceFullThreshold < chromosome.ForceEmptyThreshold {
		return fmt.Errorf("force_full_threshold must be >= force_empty_threshold")
	}
	if chromosome.MacroBearMultiplier < chromosome.MacroBullMultiplier {
		return fmt.Errorf("macro_bear_multiplier must be >= macro_bull_multiplier")
	}
	return nil
}
