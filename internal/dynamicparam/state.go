package dynamicparam

import (
	"fmt"
	"math"
)

const (
	DirectionUp        = "up"
	DirectionDown      = "down"
	DirectionUncertain = "uncertain"
)

const (
	VolatilityLow  = "low"
	VolatilityHigh = "high"
)

type DirectionRule struct {
	EntryProbability float64 `json:"entry_probability"`
	HysteresisRatio  float64 `json:"hysteresis_ratio"`
}

type VolatilityCondition struct {
	Group          string  `json:"group"`
	Metric         string  `json:"metric"`
	EntryThreshold float64 `json:"entry_threshold"`
}

type VolatilityRule struct {
	Conditions      []VolatilityCondition `json:"conditions"`
	HysteresisRatio float64               `json:"hysteresis_ratio"`
}

type StateRules struct {
	SchemaVersion string         `json:"schema_version"`
	Direction     DirectionRule  `json:"direction"`
	Volatility    VolatilityRule `json:"volatility"`
	ActivityKappa float64        `json:"activity_kappa"`
}

type StructureState struct {
	SchemaVersion          string   `json:"schema_version"`
	Direction              string   `json:"direction"`
	Volatility             string   `json:"volatility"`
	StateType              string   `json:"state_type"`
	OccurrenceID           string   `json:"occurrence_id"`
	Occurrence             int      `json:"occurrence"`
	TrendDeviation         *float64 `json:"trend_deviation,omitempty"`
	EffectiveActivityScale *float64 `json:"effective_activity_scale,omitempty"`
}

type StateInput struct {
	TimeMs                 int64
	Close                  float64
	DirectionUpProbability float64
	HistoryActivityRatios  ActivityVector
	NormalActivityScale    float64
	ObservedActivityScale  float64
}

type StateEngine struct {
	rules        StateRules
	direction    string
	highVol      bool
	occurrence   int
	stateType    string
	occurrenceID string
	trend        occurrenceTrend
	stateScales  []float64
	startScale   float64
}

func NewStateEngine(rules StateRules) (*StateEngine, error) {
	if err := ValidateStateRules(rules); err != nil {
		return nil, err
	}
	return &StateEngine{rules: rules, direction: DirectionUncertain}, nil
}

func ValidateStateRules(rules StateRules) error {
	if rules.SchemaVersion != StateSchemaVersion {
		return fmt.Errorf("unsupported state schema version")
	}
	if rules.Direction.EntryProbability <= 0.5 || rules.Direction.EntryProbability >= 1 || rules.Direction.HysteresisRatio < 0 || rules.Direction.HysteresisRatio > 1 {
		return fmt.Errorf("invalid direction hysteresis rule")
	}
	if len(rules.Volatility.Conditions) == 0 || rules.Volatility.HysteresisRatio < 0 || rules.Volatility.HysteresisRatio > 1 {
		return fmt.Errorf("invalid volatility hysteresis rule")
	}
	groups := map[string]bool{}
	for _, condition := range rules.Volatility.Conditions {
		if condition.EntryThreshold <= 1 || !finite(condition.EntryThreshold) {
			return fmt.Errorf("volatility entry threshold must exceed one")
		}
		if groups[condition.Group] {
			return fmt.Errorf("volatility group %q is enabled more than once", condition.Group)
		}
		groups[condition.Group] = true
		switch condition.Group {
		case "overall_gap":
			if condition.Metric != "yang_zhang" && condition.Metric != "tr_mean" {
				return fmt.Errorf("invalid overall/gap metric")
			}
		case "intraday_range":
			if condition.Metric != "parkinson" && condition.Metric != "high_low_mean" {
				return fmt.Errorf("invalid intraday range metric")
			}
		case "instability":
			if condition.Metric != "tr_std_dev" && condition.Metric != "high_low_std_dev" {
				return fmt.Errorf("invalid instability metric")
			}
		default:
			return fmt.Errorf("unsupported volatility group %q", condition.Group)
		}
	}
	if rules.ActivityKappa <= 0 || !finite(rules.ActivityKappa) {
		return fmt.Errorf("activity kappa must be positive")
	}
	return nil
}

func (engine *StateEngine) Step(input StateInput) (StructureState, error) {
	if engine == nil {
		return StructureState{}, fmt.Errorf("state engine is nil")
	}
	if input.TimeMs <= 0 || input.Close <= 0 || !finite(input.Close) || input.DirectionUpProbability < 0 || input.DirectionUpProbability > 1 || !finite(input.DirectionUpProbability) {
		return StructureState{}, fmt.Errorf("invalid state input")
	}
	trendDeviation := engine.trend.Deviation(math.Log(input.Close))
	newDirection := nextDirection(engine.direction, input.DirectionUpProbability, engine.rules.Direction)
	newHighVol := nextVolatility(engine.highVol, input.HistoryActivityRatios, engine.rules.Volatility)
	newType := stateType(newDirection, newHighVol)
	if newType != engine.stateType {
		engine.occurrence++
		engine.stateType = newType
		engine.occurrenceID = fmt.Sprintf("occ-%06d-%d", engine.occurrence, input.TimeMs)
		engine.trend = occurrenceTrend{}
		engine.stateScales = nil
		engine.startScale = input.NormalActivityScale
		trendDeviation = nil
	}
	engine.direction = newDirection
	engine.highVol = newHighVol
	effectiveScale := blendedActivityScale(engine.startScale, engine.stateScales, engine.rules.ActivityKappa)
	state := StructureState{
		SchemaVersion: StateSchemaVersion, Direction: newDirection, Volatility: VolatilityLow,
		StateType: newType, OccurrenceID: engine.occurrenceID, Occurrence: engine.occurrence,
		TrendDeviation: trendDeviation, EffectiveActivityScale: effectiveScale,
	}
	if newHighVol {
		state.Volatility = VolatilityHigh
	}
	engine.trend.Add(math.Log(input.Close))
	if input.ObservedActivityScale > 0 && finite(input.ObservedActivityScale) {
		engine.stateScales = append(engine.stateScales, input.ObservedActivityScale)
	}
	return state, nil
}

func nextDirection(current string, probability float64, rule DirectionRule) string {
	exit := 0.5 + rule.HysteresisRatio*(rule.EntryProbability-0.5)
	if current == DirectionUp && probability >= exit {
		return DirectionUp
	}
	if current == DirectionDown && probability <= 1-exit {
		return DirectionDown
	}
	if probability >= rule.EntryProbability {
		return DirectionUp
	}
	if probability <= 1-rule.EntryProbability {
		return DirectionDown
	}
	return DirectionUncertain
}

func nextVolatility(currentHigh bool, ratios ActivityVector, rule VolatilityRule) bool {
	if currentHigh {
		for _, condition := range rule.Conditions {
			exit := 1 + rule.HysteresisRatio*(condition.EntryThreshold-1)
			if activityMetric(ratios, condition.Metric) >= exit {
				return true
			}
		}
		return false
	}
	for _, condition := range rule.Conditions {
		if activityMetric(ratios, condition.Metric) > condition.EntryThreshold {
			return true
		}
	}
	return false
}

func activityMetric(value ActivityVector, metric string) float64 {
	switch metric {
	case "tr_mean":
		return value.TRMean
	case "tr_std_dev":
		return value.TRStdDev
	case "high_low_mean":
		return value.HighLowMean
	case "high_low_std_dev":
		return value.HighLowStdDev
	case "parkinson":
		return value.Parkinson
	case "yang_zhang":
		return value.YangZhang
	default:
		return math.NaN()
	}
}

func stateType(direction string, highVol bool) string {
	volatility := VolatilityLow
	if highVol {
		volatility = VolatilityHigh
	}
	return direction + "_" + volatility
}

func blendedActivityScale(start float64, stateValues []float64, kappa float64) *float64 {
	if start <= 0 || !finite(start) {
		return nil
	}
	state, ok := positiveMedian(stateValues)
	if !ok {
		value := start
		return &value
	}
	n := float64(len(stateValues))
	value := math.Pow(start, kappa/(kappa+n)) * math.Pow(state, n/(kappa+n))
	if !finite(value) || value <= 0 {
		return nil
	}
	return &value
}

type occurrenceTrend struct {
	count     int
	sumX      float64
	sumY      float64
	sumXX     float64
	sumXY     float64
	residuals []float64
}

func (trend *occurrenceTrend) Deviation(logClose float64) *float64 {
	if trend.count < 3 {
		return nil
	}
	denominator := float64(trend.count)*trend.sumXX - trend.sumX*trend.sumX
	if math.Abs(denominator) < 1e-12 {
		return nil
	}
	beta := (float64(trend.count)*trend.sumXY - trend.sumX*trend.sumY) / denominator
	alpha := (trend.sumY - beta*trend.sumX) / float64(trend.count)
	_, variance := meanVariance(trend.residuals)
	standardDeviation := math.Sqrt(variance)
	if standardDeviation <= 0 || !finite(standardDeviation) {
		return nil
	}
	x := float64(trend.count)
	value := (logClose - (alpha + beta*x)) / standardDeviation
	if !finite(value) {
		return nil
	}
	return &value
}

func (trend *occurrenceTrend) Add(logClose float64) {
	x := float64(trend.count)
	if trend.count >= 2 {
		denominator := float64(trend.count)*trend.sumXX - trend.sumX*trend.sumX
		if math.Abs(denominator) >= 1e-12 {
			beta := (float64(trend.count)*trend.sumXY - trend.sumX*trend.sumY) / denominator
			alpha := (trend.sumY - beta*trend.sumX) / float64(trend.count)
			trend.residuals = append(trend.residuals, logClose-(alpha+beta*x))
		}
	}
	trend.count++
	trend.sumX += x
	trend.sumY += logClose
	trend.sumXX += x * x
	trend.sumXY += x * logClose
}
