package dynamicparam

import "math"

const StateRuleReportVersion = "p09-state-rule-report-v1"

type DirectionRuleDiagnostics struct {
	MutualInformation float64            `json:"mutual_information"`
	Coverage          float64            `json:"coverage"`
	HitRate           float64            `json:"hit_rate"`
	ActualUpRate      map[string]float64 `json:"actual_up_rate"`
	StateDurations    []int              `json:"state_durations"`
	Switches          int                `json:"switches"`
}

type VolatilityRuleDiagnostics struct {
	BalancedAccuracy float64 `json:"balanced_accuracy"`
	FalseExits       int     `json:"false_exits"`
	ExitDelay        int     `json:"exit_delay"`
	Switches         int     `json:"switches"`
	StateDurations   []int   `json:"state_durations"`
}

type StateRuleReport struct {
	SchemaVersion string                    `json:"schema_version"`
	Direction     DirectionRuleDiagnostics  `json:"direction"`
	Volatility    VolatilityRuleDiagnostics `json:"volatility"`
}

func SearchStructuralStateRules(oof []Prediction, targets []TargetPoint, features []FeaturePoint, activityKappa float64) (StateRules, StateRuleReport, error) {
	labels := make(map[int]TargetPoint, len(targets))
	for _, target := range targets {
		labels[target.Index] = target
	}
	minimumSupport := maxInt(5, len(oof)/50)
	bestDirection := DirectionRuleDiagnostics{MutualInformation: -1}
	bestDirectionRule := DirectionRule{}
	for _, entry := range []float64{0.55, 0.6, 0.65, 0.7, 0.75} {
		for _, rho := range []float64{0, 0.5, 1} {
			rule := DirectionRule{EntryProbability: entry, HysteresisRatio: rho}
			diagnostics := scoreEndpointDirection(oof, labels, rule, minimumSupport)
			if betterStateCandidate(diagnostics.MutualInformation, diagnostics.Switches, entry, bestDirection.MutualInformation, bestDirection.Switches, bestDirectionRule.EntryProbability) {
				bestDirection, bestDirectionRule = diagnostics, rule
			}
		}
	}
	type conditionChoice struct{ group, metric string }
	groups := [][]conditionChoice{
		{{"overall_gap", "yang_zhang"}, {"overall_gap", "tr_mean"}},
		{{"intraday_range", "parkinson"}, {"intraday_range", "high_low_mean"}},
		{{"instability", "tr_std_dev"}, {"instability", "high_low_std_dev"}},
	}
	bestVolatility := VolatilityRuleDiagnostics{BalancedAccuracy: -1}
	bestVolatilityRule := VolatilityRule{}
	for mask := 1; mask < 8; mask++ {
		choices := [][]conditionChoice{{}}
		for groupIndex, options := range groups {
			if mask&(1<<groupIndex) == 0 {
				continue
			}
			next := make([][]conditionChoice, 0, len(choices)*len(options))
			for _, choice := range choices {
				for _, option := range options {
					next = append(next, append(append([]conditionChoice{}, choice...), option))
				}
			}
			choices = next
		}
		for _, choice := range choices {
			for _, threshold := range []float64{1.1, 1.25, 1.5, 2} {
				for _, rho := range []float64{0, 0.5, 1} {
					conditions := make([]VolatilityCondition, 0, len(choice))
					for _, option := range choice {
						conditions = append(conditions, VolatilityCondition{Group: option.group, Metric: option.metric, EntryThreshold: threshold})
					}
					rule := VolatilityRule{Conditions: conditions, HysteresisRatio: rho}
					diagnostics := scoreVolatilityRule(oof, labels, features, rule)
					if diagnostics.BalancedAccuracy > bestVolatility.BalancedAccuracy+1e-12 || (math.Abs(diagnostics.BalancedAccuracy-bestVolatility.BalancedAccuracy) <= 1e-12 && (diagnostics.FalseExits < bestVolatility.FalseExits || (diagnostics.FalseExits == bestVolatility.FalseExits && diagnostics.ExitDelay < bestVolatility.ExitDelay))) {
						bestVolatility, bestVolatilityRule = diagnostics, rule
					}
				}
			}
		}
	}
	rules := StateRules{SchemaVersion: StateSchemaVersion, Direction: bestDirectionRule, Volatility: bestVolatilityRule, ActivityKappa: activityKappa}
	if err := ValidateStateRules(rules); err != nil {
		return StateRules{}, StateRuleReport{}, err
	}
	return rules, StateRuleReport{SchemaVersion: StateRuleReportVersion, Direction: bestDirection, Volatility: bestVolatility}, nil
}

func scoreEndpointDirection(oof []Prediction, labels map[int]TargetPoint, rule DirectionRule, minimumSupport int) DirectionRuleDiagnostics {
	current, last := DirectionUncertain, ""
	predicted, actual := []string{}, []string{}
	counts, upCounts := map[string]int{}, map[string]int{}
	hits, certain, switches, run := 0, 0, 0, 0
	durations := []int{}
	for _, prediction := range oof {
		target, ok := labels[prediction.Index]
		if !ok {
			continue
		}
		current = nextDirection(current, prediction.DirectionUpProbability, rule)
		truth := DirectionDown
		if target.DirectionUp {
			truth = DirectionUp
		}
		predicted, actual = append(predicted, current), append(actual, truth)
		counts[current]++
		if target.DirectionUp {
			upCounts[current]++
		}
		if current != DirectionUncertain {
			certain++
			if current == truth {
				hits++
			}
		}
		if last == "" || current == last {
			run++
		} else {
			durations = append(durations, run)
			run = 1
			switches++
		}
		last = current
	}
	if run > 0 {
		durations = append(durations, run)
	}
	mi := normalizedMutualInformation(predicted, actual)
	if certain < minimumSupport {
		mi = -1
	}
	rates := map[string]float64{}
	for state, count := range counts {
		if count > 0 {
			rates[state] = float64(upCounts[state]) / float64(count)
		}
	}
	coverage, hitRate := 0.0, 0.0
	if len(predicted) > 0 {
		coverage = float64(certain) / float64(len(predicted))
	}
	if certain > 0 {
		hitRate = float64(hits) / float64(certain)
	}
	return DirectionRuleDiagnostics{MutualInformation: mi, Coverage: coverage, HitRate: hitRate, ActualUpRate: rates, StateDurations: durations, Switches: switches}
}

func scoreVolatilityRule(oof []Prediction, labels map[int]TargetPoint, features []FeaturePoint, rule VolatilityRule) VolatilityRuleDiagnostics {
	current, last := false, false
	first := true
	tp, tn, fp, fn := 0, 0, 0, 0
	falseExits, exitDelay, switches, run := 0, 0, 0, 0
	durations := []int{}
	for _, prediction := range oof {
		target, ok := labels[prediction.Index]
		if !ok || prediction.Index < 0 || prediction.Index >= len(features) || !features[prediction.Index].Available {
			continue
		}
		truthHigh := target.Normalized && target.NormalizedUp+target.NormalizedDown > 1
		current = nextVolatility(current, features[prediction.Index].HistoryRatio, rule)
		if current && truthHigh {
			tp++
		}
		if current && !truthHigh {
			fp++
		}
		if !current && truthHigh {
			fn++
		}
		if !current && !truthHigh {
			tn++
		}
		if !first && current != last {
			durations = append(durations, run)
			run = 0
			switches++
			if !current && truthHigh {
				falseExits++
			}
		}
		if current && !truthHigh {
			exitDelay++
		}
		run++
		last = current
		first = false
	}
	if run > 0 {
		durations = append(durations, run)
	}
	tpr, tnr := 0.0, 0.0
	if tp+fn > 0 {
		tpr = float64(tp) / float64(tp+fn)
	}
	if tn+fp > 0 {
		tnr = float64(tn) / float64(tn+fp)
	}
	return VolatilityRuleDiagnostics{BalancedAccuracy: (tpr + tnr) / 2, FalseExits: falseExits, ExitDelay: exitDelay, Switches: switches, StateDurations: durations}
}
