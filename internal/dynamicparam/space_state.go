package dynamicparam

import (
	"fmt"
	"math"
	"sort"
)

const SpaceStateVersion = "p09-space-state-v1"

const (
	SpaceDirectionUp        = "up"
	SpaceDirectionBalanced  = "balanced"
	SpaceDirectionDown      = "down"
	SpaceDirectionUncertain = "uncertain"
	SpaceMagnitudeLarge     = "large"
	SpaceMagnitudeSmall     = "small"
	SpaceMagnitudeUncertain = "uncertain"
)

type SpaceStateRules struct {
	SchemaVersion       string  `json:"schema_version"`
	DirectionConfidence float64 `json:"direction_confidence"`
	DirectionGap        float64 `json:"direction_gap"`
	DirectionHysteresis float64 `json:"direction_hysteresis"`
	MagnitudeConfidence float64 `json:"magnitude_confidence"`
	MagnitudeHysteresis float64 `json:"magnitude_hysteresis"`
	MinimumSupport      int     `json:"minimum_support"`
}

type SpaceHardState struct {
	SchemaVersion string `json:"schema_version"`
	Direction     string `json:"direction"`
	Magnitude     string `json:"magnitude"`
}

type SpaceRuleReport struct {
	SchemaVersion         string  `json:"schema_version"`
	DirectionNMI          float64 `json:"direction_nmi"`
	MagnitudeNMI          float64 `json:"magnitude_nmi"`
	DirectionSwitches     int     `json:"direction_switches"`
	MagnitudeSwitches     int     `json:"magnitude_switches"`
	DirectionCertainCount int     `json:"direction_certain_count"`
	MagnitudeCertainCount int     `json:"magnitude_certain_count"`
}

type SpaceStateEngine struct {
	rules     SpaceStateRules
	direction string
	magnitude string
}

func NewSpaceStateEngine(rules SpaceStateRules) (*SpaceStateEngine, error) {
	if err := ValidateSpaceStateRules(rules); err != nil {
		return nil, err
	}
	return &SpaceStateEngine{rules: rules, direction: SpaceDirectionUncertain, magnitude: SpaceMagnitudeUncertain}, nil
}

func ValidateSpaceStateRules(rules SpaceStateRules) error {
	if rules.SchemaVersion != SpaceStateVersion || rules.DirectionConfidence <= 1.0/3 || rules.DirectionConfidence >= 1 || rules.DirectionGap < 0 || rules.DirectionGap >= 1 || rules.DirectionHysteresis < 0 || rules.DirectionHysteresis > 1 || rules.MagnitudeConfidence <= 0.5 || rules.MagnitudeConfidence >= 1 || rules.MagnitudeHysteresis < 0 || rules.MagnitudeHysteresis > 1 || rules.MinimumSupport < 1 {
		return fmt.Errorf("invalid space-state rules")
	}
	return nil
}

func (engine *SpaceStateEngine) Step(regions SixRegionProbabilities) SpaceHardState {
	directionProbabilities := map[string]float64{
		SpaceDirectionUp:       regions.UpSmall + regions.UpLarge,
		SpaceDirectionBalanced: regions.BalancedSmall + regions.BalancedLarge,
		SpaceDirectionDown:     regions.DownSmall + regions.DownLarge,
	}
	large := regions.UpLarge + regions.BalancedLarge + regions.DownLarge
	engine.direction = nextSpaceDirection(engine.direction, directionProbabilities, engine.rules)
	engine.magnitude = nextSpaceMagnitude(engine.magnitude, large, engine.rules)
	return SpaceHardState{SchemaVersion: SpaceStateVersion, Direction: engine.direction, Magnitude: engine.magnitude}
}

func nextSpaceDirection(current string, probabilities map[string]float64, rules SpaceStateRules) string {
	if probability, ok := probabilities[current]; ok {
		second := secondLargest(probabilities, current)
		confidenceExit := 1.0/3 + rules.DirectionHysteresis*(rules.DirectionConfidence-1.0/3)
		gapExit := rules.DirectionHysteresis * rules.DirectionGap
		if probability >= confidenceExit && probability-second >= gapExit {
			return current
		}
	}
	winner, maximum, second := topTwo(probabilities)
	if maximum >= rules.DirectionConfidence && maximum-second >= rules.DirectionGap {
		return winner
	}
	return SpaceDirectionUncertain
}

func nextSpaceMagnitude(current string, large float64, rules SpaceStateRules) string {
	exit := 0.5 + rules.MagnitudeHysteresis*(rules.MagnitudeConfidence-0.5)
	if current == SpaceMagnitudeLarge && large >= exit {
		return current
	}
	if current == SpaceMagnitudeSmall && 1-large >= exit {
		return current
	}
	if large >= rules.MagnitudeConfidence {
		return SpaceMagnitudeLarge
	}
	if 1-large >= rules.MagnitudeConfidence {
		return SpaceMagnitudeSmall
	}
	return SpaceMagnitudeUncertain
}

func topTwo(probabilities map[string]float64) (string, float64, float64) {
	keys := []string{SpaceDirectionBalanced, SpaceDirectionDown, SpaceDirectionUp}
	sort.Strings(keys)
	winner, maximum, second := keys[0], -1.0, -1.0
	for _, key := range keys {
		value := probabilities[key]
		if value > maximum {
			winner, maximum, second = key, value, maximum
		} else if value > second {
			second = value
		}
	}
	return winner, maximum, second
}

func secondLargest(probabilities map[string]float64, excluded string) float64 {
	result := 0.0
	for key, value := range probabilities {
		if key != excluded && value > result {
			result = value
		}
	}
	return result
}

func SearchSpaceStateRules(oof []Prediction, targets []TargetPoint) (RegionRule, SpaceStateRules, SpaceRuleReport, error) {
	byIndex := make(map[int]TargetPoint, len(targets))
	totals := make([]float64, 0, len(targets))
	for _, target := range targets {
		if target.Normalized {
			byIndex[target.Index] = target
			if target.NormalizedUp+target.NormalizedDown > 0 {
				totals = append(totals, target.NormalizedUp+target.NormalizedDown)
			}
		}
	}
	if len(oof) < 20 || len(totals) < 20 {
		return RegionRule{}, SpaceStateRules{}, SpaceRuleReport{}, fmt.Errorf("insufficient OOF support for space-state search")
	}
	sort.Float64s(totals)
	minimumSupport := maxInt(5, len(oof)/50)
	type directionCandidate struct {
		boundary, confidence, gap, hysteresis, nmi float64
		switches, support                          int
	}
	bestDirection := directionCandidate{nmi: -1}
	for _, boundary := range []float64{0.1, 0.2, 0.3, 0.4} {
		for _, confidence := range []float64{0.45, 0.55, 0.65, 0.75} {
			for _, gap := range []float64{0.05, 0.15, 0.25} {
				for _, hysteresis := range []float64{0, 0.5, 1} {
					candidate := scoreDirectionRule(oof, byIndex, boundary, confidence, gap, hysteresis, minimumSupport)
					if betterStateCandidate(candidate.nmi, candidate.switches, confidence+gap, bestDirection.nmi, bestDirection.switches, bestDirection.confidence+bestDirection.gap) {
						bestDirection = candidate
					}
				}
			}
		}
	}
	type magnitudeCandidate struct {
		boundary, confidence, hysteresis, nmi float64
		switches, support                     int
	}
	bestMagnitude := magnitudeCandidate{nmi: -1}
	for _, quantile := range []float64{0.35, 0.5, 0.65} {
		boundary := quantileSorted(totals, quantile)
		for _, confidence := range []float64{0.55, 0.65, 0.75, 0.85} {
			for _, hysteresis := range []float64{0, 0.5, 1} {
				candidate := scoreMagnitudeRule(oof, byIndex, boundary, confidence, hysteresis, minimumSupport)
				if betterStateCandidate(candidate.nmi, candidate.switches, confidence, bestMagnitude.nmi, bestMagnitude.switches, bestMagnitude.confidence) {
					bestMagnitude = candidate
				}
			}
		}
	}
	if bestDirection.nmi < 0 || bestMagnitude.nmi < 0 {
		return RegionRule{}, SpaceStateRules{}, SpaceRuleReport{}, fmt.Errorf("no space-state rule met minimum support")
	}
	rule := RegionRule{DirectionBoundary: bestDirection.boundary, MagnitudeBoundary: bestMagnitude.boundary}
	rules := SpaceStateRules{SchemaVersion: SpaceStateVersion, DirectionConfidence: bestDirection.confidence, DirectionGap: bestDirection.gap, DirectionHysteresis: bestDirection.hysteresis, MagnitudeConfidence: bestMagnitude.confidence, MagnitudeHysteresis: bestMagnitude.hysteresis, MinimumSupport: minimumSupport}
	report := SpaceRuleReport{SchemaVersion: SpaceStateVersion, DirectionNMI: bestDirection.nmi, MagnitudeNMI: bestMagnitude.nmi, DirectionSwitches: bestDirection.switches, MagnitudeSwitches: bestMagnitude.switches, DirectionCertainCount: bestDirection.support, MagnitudeCertainCount: bestMagnitude.support}
	return rule, rules, report, nil
}

func scoreDirectionRule(oof []Prediction, targets map[int]TargetPoint, boundary, confidence, gap, hysteresis float64, minimumSupport int) struct {
	boundary, confidence, gap, hysteresis, nmi float64
	switches, support                          int
} {
	rules := SpaceStateRules{SchemaVersion: SpaceStateVersion, DirectionConfidence: confidence, DirectionGap: gap, DirectionHysteresis: hysteresis, MagnitudeConfidence: 0.6, MagnitudeHysteresis: 0.5, MinimumSupport: minimumSupport}
	engine, _ := NewSpaceStateEngine(rules)
	predicted, actual := []string{}, []string{}
	last, switches, support := "", 0, 0
	for _, prediction := range oof {
		target, ok := targets[prediction.Index]
		if !ok {
			continue
		}
		regions := IntegrateSixRegions(prediction.JointDistribution, RegionRule{DirectionBoundary: boundary, MagnitudeBoundary: 1}, 256)
		state := engine.Step(regions).Direction
		truth := actualSpaceDirection(target, boundary)
		predicted, actual = append(predicted, state), append(actual, truth)
		if state != SpaceDirectionUncertain {
			support++
		}
		if last != "" && state != last {
			switches++
		}
		last = state
	}
	nmi := normalizedMutualInformation(predicted, actual)
	if support < minimumSupport {
		nmi = -1
	}
	return struct {
		boundary, confidence, gap, hysteresis, nmi float64
		switches, support                          int
	}{boundary, confidence, gap, hysteresis, nmi, switches, support}
}

func scoreMagnitudeRule(oof []Prediction, targets map[int]TargetPoint, boundary, confidence, hysteresis float64, minimumSupport int) struct {
	boundary, confidence, hysteresis, nmi float64
	switches, support                     int
} {
	rules := SpaceStateRules{SchemaVersion: SpaceStateVersion, DirectionConfidence: 0.5, DirectionGap: 0.1, DirectionHysteresis: 0.5, MagnitudeConfidence: confidence, MagnitudeHysteresis: hysteresis, MinimumSupport: minimumSupport}
	engine, _ := NewSpaceStateEngine(rules)
	predicted, actual := []string{}, []string{}
	last, switches, support := "", 0, 0
	for _, prediction := range oof {
		target, ok := targets[prediction.Index]
		if !ok {
			continue
		}
		regions := IntegrateSixRegions(prediction.JointDistribution, RegionRule{DirectionBoundary: 0.2, MagnitudeBoundary: boundary}, 256)
		state := engine.Step(regions).Magnitude
		truth := SpaceMagnitudeSmall
		if target.NormalizedUp+target.NormalizedDown >= boundary {
			truth = SpaceMagnitudeLarge
		}
		predicted, actual = append(predicted, state), append(actual, truth)
		if state != SpaceMagnitudeUncertain {
			support++
		}
		if last != "" && state != last {
			switches++
		}
		last = state
	}
	nmi := normalizedMutualInformation(predicted, actual)
	if support < minimumSupport {
		nmi = -1
	}
	return struct {
		boundary, confidence, hysteresis, nmi float64
		switches, support                     int
	}{boundary, confidence, hysteresis, nmi, switches, support}
}

func actualSpaceDirection(target TargetPoint, boundary float64) string {
	total := target.NormalizedUp + target.NormalizedDown
	if total <= 0 {
		return SpaceDirectionBalanced
	}
	asymmetry := (target.NormalizedUp - target.NormalizedDown) / total
	if asymmetry > boundary {
		return SpaceDirectionUp
	}
	if asymmetry < -boundary {
		return SpaceDirectionDown
	}
	return SpaceDirectionBalanced
}

func normalizedMutualInformation(predicted, actual []string) float64 {
	if len(predicted) != len(actual) || len(actual) == 0 {
		return 0
	}
	joint := map[string]int{}
	px, py := map[string]int{}, map[string]int{}
	for index := range actual {
		joint[predicted[index]+"\x00"+actual[index]]++
		px[predicted[index]]++
		py[actual[index]]++
	}
	n := float64(len(actual))
	mi := 0.0
	for key, count := range joint {
		separator := 0
		for separator < len(key) && key[separator] != 0 {
			separator++
		}
		x, y := key[:separator], key[separator+1:]
		p := float64(count) / n
		mi += p * math.Log(p/(float64(px[x])/n*float64(py[y])/n))
	}
	entropy := func(counts map[string]int) float64 {
		value := 0.0
		for _, count := range counts {
			p := float64(count) / n
			value -= p * math.Log(p)
		}
		return value
	}
	denominator := math.Sqrt(entropy(px) * entropy(py))
	if denominator <= 0 {
		return 0
	}
	return mi / denominator
}

func betterStateCandidate(score float64, switches int, simplicity float64, bestScore float64, bestSwitches int, bestSimplicity float64) bool {
	if score > bestScore+1e-12 {
		return true
	}
	if math.Abs(score-bestScore) <= 1e-12 && (switches < bestSwitches || (switches == bestSwitches && simplicity < bestSimplicity)) {
		return true
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
