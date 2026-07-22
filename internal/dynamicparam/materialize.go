package dynamicparam

import (
	"fmt"
	"math"
	"sort"

	"quantsaas/internal/quant"
)

type MaterializationConfig struct {
	SchemaVersion     string             `json:"schema_version"`
	ActivityLookback  int                `json:"activity_lookback"`
	ActivityScale     ActivityScaleModel `json:"activity_scale"`
	StateRules        StateRules         `json:"state_rules"`
	Policy            DynamicPolicy      `json:"policy"`
	BaseChromosome    quant.Chromosome   `json:"base_chromosome"`
	ModelArtifactHash string             `json:"model_artifact_hash"`
	PredictionHash    string             `json:"prediction_hash"`
	PolicyHash        string             `json:"policy_hash"`
	WorkCounter       func(int64)        `json:"-"`
	Heartbeat         func(string)       `json:"-"`
}

type DailyDiagnostic struct {
	SchemaVersion string            `json:"schema_version"`
	Index         int               `json:"index"`
	TimeMs        int64             `json:"time_ms"`
	OneDay        *Prediction       `json:"one_day,omitempty"`
	TwentyDay     *Prediction       `json:"twenty_day,omitempty"`
	State         StructureState    `json:"state"`
	Effective     EffectiveSnapshot `json:"effective"`
}

type MaterializedPath struct {
	SchemaVersion     string            `json:"schema_version"`
	ModelArtifactHash string            `json:"model_artifact_hash"`
	PredictionHash    string            `json:"prediction_hash"`
	PolicyHash        string            `json:"policy_hash"`
	Diagnostics       []DailyDiagnostic `json:"diagnostics"`
}

func Materialize(bars []quant.Bar, oneDay, twentyDay []Prediction, config MaterializationConfig) (MaterializedPath, error) {
	if config.SchemaVersion != PredictionSchemaVersion || config.ModelArtifactHash == "" || config.PolicyHash == "" {
		return MaterializedPath{}, fmt.Errorf("invalid materialization identity")
	}
	if err := validateEffectiveChromosome(config.BaseChromosome); err != nil {
		return MaterializedPath{}, err
	}
	if err := ValidatePolicy(config.Policy); err != nil {
		return MaterializedPath{}, err
	}
	engine, err := NewStateEngine(config.StateRules)
	if err != nil {
		return MaterializedPath{}, err
	}
	features, err := BuildFeaturePoints(bars, config.ActivityLookback)
	if err != nil {
		return MaterializedPath{}, err
	}
	oneByIndex, twentyByIndex := predictionsByIndex(oneDay), predictionsByIndex(twentyDay)
	result := MaterializedPath{SchemaVersion: PredictionSchemaVersion, ModelArtifactHash: config.ModelArtifactHash, PredictionHash: config.PredictionHash, PolicyHash: config.PolicyHash, Diagnostics: make([]DailyDiagnostic, 0, len(bars))}
	for index, bar := range bars {
		if config.Heartbeat != nil {
			config.Heartbeat("每日診斷：計算第 " + fmt.Sprintf("%d/%d", index+1, len(bars)))
		}
		one, oneOK := oneByIndex[index]
		twenty, twentyOK := twentyByIndex[index]
		featureAvailable := index < len(features) && features[index].Available
		if !oneOK || !twentyOK || !featureAvailable {
			effective := fallbackSnapshot(index, bar.OpenTime, config.BaseChromosome, "prediction_or_feature_unavailable")
			result.Diagnostics = append(result.Diagnostics, DailyDiagnostic{SchemaVersion: EffectiveParameterVersion, Index: index, TimeMs: bar.OpenTime, Effective: effective})
			continue
		}
		observedActivityScale, scaleErr := config.ActivityScale.Predict(features[index])
		if scaleErr != nil {
			effective := fallbackSnapshot(index, bar.OpenTime, config.BaseChromosome, "observed_activity_scale_unavailable")
			result.Diagnostics = append(result.Diagnostics, DailyDiagnostic{SchemaVersion: EffectiveParameterVersion, Index: index, TimeMs: bar.OpenTime, OneDay: &one, TwentyDay: &twenty, Effective: effective})
			continue
		}
		state, stateErr := engine.Step(StateInput{
			TimeMs: bar.OpenTime, Close: bar.Close, DirectionUpProbability: twenty.DirectionUpProbability,
			HistoryActivityRatios: features[index].HistoryRatio, NormalActivityScale: twenty.NormalActivityScale,
			ObservedActivityScale: observedActivityScale,
		})
		if stateErr != nil {
			return MaterializedPath{}, stateErr
		}
		signals := buildPolicySignals(one, twenty, features[index], state)
		effective, policyErr := ApplyPolicy(config.BaseChromosome, config.Policy, PolicyInput{Index: index, TimeMs: bar.OpenTime, State: state, Signals: signals})
		if policyErr != nil {
			return MaterializedPath{}, policyErr
		}
		oneCopy, twentyCopy := one, twenty
		result.Diagnostics = append(result.Diagnostics, DailyDiagnostic{SchemaVersion: EffectiveParameterVersion, Index: index, TimeMs: bar.OpenTime, OneDay: &oneCopy, TwentyDay: &twentyCopy, State: state, Effective: effective})
		if config.WorkCounter != nil {
			config.WorkCounter(1)
		}
	}
	return result, nil
}

func buildPolicySignals(one, twenty Prediction, feature FeaturePoint, state StructureState) map[string]*float64 {
	result := map[string]*float64{}
	put := func(name string, value float64) {
		if finite(value) && value >= -1 && value <= 1 {
			copy := value
			result[name] = &copy
		}
	}
	put("direction_1d", 2*one.DirectionUpProbability-1)
	put("direction_20d", 2*twenty.DirectionUpProbability-1)
	addSpaceStateSignals := func(prefix string, state SpaceHardState) {
		switch state.Direction {
		case SpaceDirectionUp:
			put(prefix+"_direction", 1)
		case SpaceDirectionBalanced:
			put(prefix+"_direction", 0)
		case SpaceDirectionDown:
			put(prefix+"_direction", -1)
		}
		switch state.Magnitude {
		case SpaceMagnitudeLarge:
			put(prefix+"_magnitude", 1)
		case SpaceMagnitudeSmall:
			put(prefix+"_magnitude", -1)
		}
	}
	addSpaceStateSignals("space_1d", one.SpaceState)
	addSpaceStateSignals("space_20d", twenty.SpaceState)
	addRegionSignals := func(prefix string, regions SixRegionProbabilities) {
		put(prefix+"_up_small", 2*regions.UpSmall-1)
		put(prefix+"_up_large", 2*regions.UpLarge-1)
		put(prefix+"_balanced_small", 2*regions.BalancedSmall-1)
		put(prefix+"_balanced_large", 2*regions.BalancedLarge-1)
		put(prefix+"_down_small", 2*regions.DownSmall-1)
		put(prefix+"_down_large", 2*regions.DownLarge-1)
	}
	addRegionSignals("region_1d", one.SixRegions)
	addRegionSignals("region_20d", twenty.SixRegions)
	addActivitySignals := func(prefix string, predicted ActivityVector) {
		current := feature.Activity
		ratio := feature.HistoryRatio
		values := []struct {
			name                      string
			predicted, current, ratio float64
		}{
			{"tr_mean", predicted.TRMean, current.TRMean, ratio.TRMean}, {"tr_std_dev", predicted.TRStdDev, current.TRStdDev, ratio.TRStdDev},
			{"high_low_mean", predicted.HighLowMean, current.HighLowMean, ratio.HighLowMean}, {"high_low_std_dev", predicted.HighLowStdDev, current.HighLowStdDev, ratio.HighLowStdDev},
			{"parkinson", predicted.Parkinson, current.Parkinson, ratio.Parkinson}, {"yang_zhang", predicted.YangZhang, current.YangZhang, ratio.YangZhang},
		}
		for _, value := range values {
			median := value.current / value.ratio
			if median > 0 && value.predicted > 0 {
				put(prefix+"_"+value.name, math.Tanh(math.Log(value.predicted/median)))
			}
		}
	}
	addActivitySignals("activity_1d", one.PathActivity)
	addActivitySignals("activity_20d", twenty.PathActivity)
	if state.TrendDeviation != nil {
		put("occurrence_trend_deviation", math.Tanh(*state.TrendDeviation))
	}
	return result
}

func fallbackSnapshot(index int, timeMs int64, chromosome quant.Chromosome, reason string) EffectiveSnapshot {
	return EffectiveSnapshot{SchemaVersion: EffectiveParameterVersion, Index: index, TimeMs: timeMs, Chromosome: chromosome, Contributions: map[string]Contribution{}, FallbackEvents: []string{reason}}
}

func predictionsByIndex(predictions []Prediction) map[int]Prediction {
	result := make(map[int]Prediction, len(predictions))
	for _, prediction := range predictions {
		if prediction.Available {
			result[prediction.Index] = prediction
		}
	}
	return result
}

func (path MaterializedPath) SortedDiagnostics() []DailyDiagnostic {
	result := append([]DailyDiagnostic(nil), path.Diagnostics...)
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result
}
