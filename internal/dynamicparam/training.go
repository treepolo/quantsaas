package dynamicparam

import (
	"context"
	"fmt"
	"math"
	"sort"

	"quantsaas/internal/quant"
)

type TrainingConfig struct {
	Route         string        `json:"route"`
	Lookbacks     []int         `json:"lookbacks"`
	Folds         int           `json:"folds"`
	MinimumTrain  int           `json:"minimum_train"`
	Learner       LearnerConfig `json:"learner"`
	RegionRule    RegionRule    `json:"region_rule"`
	ActivityKappa float64       `json:"activity_kappa"`
}

type FoldWindow struct {
	Fold            int `json:"fold"`
	TrainStart      int `json:"train_start"`
	TrainEnd        int `json:"train_end"`
	ValidationStart int `json:"validation_start"`
	ValidationEnd   int `json:"validation_end"`
	Purge           int `json:"purge"`
}

type TargetModel struct {
	TargetKind   string                 `json:"target_kind"`
	Lookback     int                    `json:"lookback"`
	Learner      *LearnerModel          `json:"learner,omitempty"`
	Distribution *DistributionModel     `json:"distribution,omitempty"`
	Calibrator   *ProbabilityCalibrator `json:"calibrator,omitempty"`
	Report       ModelReport            `json:"report"`
	OOF          []TargetOOFPoint       `json:"oof"`
}

type TargetOOFPoint struct {
	Index        int                     `json:"index"`
	TimeMs       int64                   `json:"time_ms"`
	Values       []float64               `json:"values,omitempty"`
	Distribution *JointDistribution      `json:"distribution,omitempty"`
	SixRegions   *SixRegionProbabilities `json:"six_regions,omitempty"`
}

type HorizonModel struct {
	Horizon          int                `json:"horizon"`
	Direction        TargetModel        `json:"direction"`
	Joint            TargetModel        `json:"joint"`
	Activity         TargetModel        `json:"activity"`
	ActivityScale    ActivityScaleModel `json:"activity_scale"`
	RegionRule       RegionRule         `json:"region_rule"`
	SpaceRules       SpaceStateRules    `json:"space_rules"`
	SpaceReport      SpaceRuleReport    `json:"space_report"`
	StructuralRules  *StateRules        `json:"structural_rules,omitempty"`
	StructuralReport *StateRuleReport   `json:"structural_report,omitempty"`
	OOF              []Prediction       `json:"oof"`
}

type TrainedSystem struct {
	SchemaVersion string       `json:"schema_version"`
	Route         string       `json:"route"`
	OneDay        HorizonModel `json:"one_day"`
	TwentyDay     HorizonModel `json:"twenty_day"`
}

func TrainSystem(bars []quant.Bar, config TrainingConfig) (TrainedSystem, error) {
	if err := ValidateTrainingConfig(config); err != nil {
		return TrainedSystem{}, err
	}
	one, err := TrainHorizon(bars, HorizonOneDay, config)
	if err != nil {
		return TrainedSystem{}, err
	}
	twenty, err := TrainHorizon(bars, HorizonTwentyDay, config)
	if err != nil {
		return TrainedSystem{}, err
	}
	return TrainedSystem{SchemaVersion: ModelArtifactVersion, Route: config.Route, OneDay: one, TwentyDay: twenty}, nil
}

func ValidateTrainingConfig(config TrainingConfig) error {
	if config.Route != RouteExplainable && config.Route != RouteTCN {
		return fmt.Errorf("unsupported model route")
	}
	if len(config.Lookbacks) == 0 || config.Folds < 2 || config.MinimumTrain < 20 {
		return fmt.Errorf("lookbacks, at least two folds and minimum training support are required")
	}
	allowed := map[int]bool{5: true, 10: true, 20: true, 40: true, 60: true, 120: true, 250: true, 500: true}
	for _, lookback := range config.Lookbacks {
		if !allowed[lookback] {
			return fmt.Errorf("lookback %d is outside the confirmed candidate set", lookback)
		}
	}
	if config.RegionRule.DirectionBoundary <= 0 || config.RegionRule.DirectionBoundary >= 1 || config.RegionRule.MagnitudeBoundary <= 0 {
		return fmt.Errorf("invalid six-region rule")
	}
	return nil
}

func TrainHorizon(bars []quant.Bar, horizon int, config TrainingConfig) (HorizonModel, error) {
	return TrainHorizonContext(context.Background(), bars, horizon, config)
}

func TrainHorizonContext(ctx context.Context, bars []quant.Bar, horizon int, config TrainingConfig) (HorizonModel, error) {
	if err := ctx.Err(); err != nil {
		return HorizonModel{}, err
	}
	targets, err := BuildTargets(bars, horizon)
	if err != nil {
		return HorizonModel{}, err
	}
	direction, err := selectAndFitTarget(ctx, bars, targets, TargetDirection, horizon, config)
	if err != nil {
		return HorizonModel{}, err
	}
	joint, err := selectAndFitTarget(ctx, bars, targets, TargetJointSpace, horizon, config)
	if err != nil {
		return HorizonModel{}, err
	}
	activity, err := selectAndFitTarget(ctx, bars, targets, TargetPathActivity, horizon, config)
	if err != nil {
		return HorizonModel{}, err
	}
	scaleFeatures, err := BuildFeaturePoints(bars, activity.Lookback)
	if err != nil {
		return HorizonModel{}, err
	}
	scale, err := TrainActivityScale(scaleFeatures, targets)
	if err != nil {
		return HorizonModel{}, err
	}
	oof := combineOOF(horizon, direction.OOF, joint.OOF, activity.OOF, scale, scaleFeatures)
	if err := ctx.Err(); err != nil {
		return HorizonModel{}, err
	}
	regionRule, spaceRules, spaceReport, err := SearchSpaceStateRules(oof, targets)
	if err != nil {
		return HorizonModel{}, err
	}
	joint.Distribution.RegionRule = regionRule
	spaceEngine, err := NewSpaceStateEngine(spaceRules)
	if err != nil {
		return HorizonModel{}, err
	}
	for index := range oof {
		if err := ctx.Err(); err != nil {
			return HorizonModel{}, err
		}
		oof[index].SixRegions = IntegrateSixRegions(oof[index].JointDistribution, regionRule, 512)
		oof[index].SpaceState = spaceEngine.Step(oof[index].SixRegions)
	}
	result := HorizonModel{Horizon: horizon, Direction: direction, Joint: joint, Activity: activity, ActivityScale: scale, RegionRule: regionRule, SpaceRules: spaceRules, SpaceReport: spaceReport, OOF: oof}
	if horizon == HorizonTwentyDay {
		if err := ctx.Err(); err != nil {
			return HorizonModel{}, err
		}
		activityKappa := config.ActivityKappa
		if activityKappa <= 0 {
			activityKappa = 20
		}
		rules, report, searchErr := SearchStructuralStateRules(oof, targets, scaleFeatures, activityKappa)
		if searchErr != nil {
			return HorizonModel{}, searchErr
		}
		result.StructuralRules, result.StructuralReport = &rules, &report
	}
	return result, nil
}

func (model HorizonModel) Predict(bars []quant.Bar) ([]Prediction, error) {
	featureCache := map[int][]FeaturePoint{}
	for _, lookback := range []int{model.Direction.Lookback, model.Joint.Lookback, model.Activity.Lookback} {
		if _, ok := featureCache[lookback]; ok {
			continue
		}
		features, err := BuildFeaturePoints(bars, lookback)
		if err != nil {
			return nil, err
		}
		featureCache[lookback] = features
	}
	result := make([]Prediction, 0, len(bars))
	spaceEngine, err := NewSpaceStateEngine(model.SpaceRules)
	if err != nil {
		return nil, err
	}
	for index := range bars {
		directionFeature := featureCache[model.Direction.Lookback][index]
		jointFeature := featureCache[model.Joint.Lookback][index]
		activityFeature := featureCache[model.Activity.Lookback][index]
		if !directionFeature.Available || !jointFeature.Available || !activityFeature.Available {
			continue
		}
		direction, err := model.Direction.Learner.Predict(directionFeature)
		if err != nil {
			return nil, err
		}
		if model.Direction.Calibrator == nil {
			return nil, fmt.Errorf("direction probability calibrator is missing")
		}
		direction[0], err = model.Direction.Calibrator.Predict(direction[0])
		if err != nil {
			return nil, err
		}
		distribution, regions, err := model.Joint.Distribution.Predict(jointFeature)
		if err != nil {
			return nil, err
		}
		activityRaw, err := model.Activity.Learner.Predict(activityFeature)
		if err != nil {
			return nil, err
		}
		activity := decodeActivityPrediction(activityRaw)
		normalScale, err := model.ActivityScale.PredictActivity(activity)
		if err != nil {
			return nil, err
		}
		result = append(result, Prediction{SchemaVersion: PredictionSchemaVersion, Index: index, TimeMs: bars[index].OpenTime, Horizon: model.Horizon, DirectionUpProbability: direction[0], JointDistribution: distribution, SixRegions: regions, SpaceState: spaceEngine.Step(regions), PathActivity: activity, NormalActivityScale: normalScale, Available: true})
	}
	return result, nil
}

func BuildPurgedFolds(total, folds, minimumTrain, purge int) ([]FoldWindow, error) {
	if total <= minimumTrain+purge+folds || folds < 2 || minimumTrain < 1 || purge < 1 {
		return nil, fmt.Errorf("insufficient samples for purged walk-forward")
	}
	remaining := total - minimumTrain - purge
	block := remaining / folds
	if block < 1 {
		return nil, fmt.Errorf("walk-forward validation block is empty")
	}
	result := make([]FoldWindow, 0, folds)
	for fold := 0; fold < folds; fold++ {
		validationStart := minimumTrain + purge + fold*block
		validationEnd := validationStart + block - 1
		if fold == folds-1 {
			validationEnd = total - 1
		}
		result = append(result, FoldWindow{Fold: fold, TrainStart: 0, TrainEnd: validationStart - purge - 1, ValidationStart: validationStart, ValidationEnd: validationEnd, Purge: purge})
	}
	return result, nil
}

func selectAndFitTarget(ctx context.Context, bars []quant.Bar, targets []TargetPoint, targetKind string, horizon int, config TrainingConfig) (TargetModel, error) {
	type candidate struct {
		lookback int
		report   ModelReport
	}
	candidates := make([]candidate, 0, len(config.Lookbacks))
	for _, lookback := range uniqueSorted(config.Lookbacks) {
		if err := ctx.Err(); err != nil {
			return TargetModel{}, err
		}
		features, err := BuildFeaturePoints(bars, lookback)
		if err != nil {
			return TargetModel{}, err
		}
		report, err := validateCandidate(ctx, features, targets, targetKind, horizon, config)
		if err != nil {
			if ctx.Err() != nil {
				return TargetModel{}, ctx.Err()
			}
			continue
		}
		candidates = append(candidates, candidate{lookback: lookback, report: report})
	}
	if len(candidates) == 0 {
		return TargetModel{}, fmt.Errorf("no %s candidate had sufficient causal samples", targetKind)
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.report.MeanLoss < best.report.MeanLoss {
			best = candidate
		}
	}
	selected := best
	for _, candidate := range candidates {
		if candidate.lookback < selected.lookback && candidate.report.MeanLoss <= best.report.MeanLoss+best.report.StandardError {
			selected = candidate
		}
	}
	features, err := BuildFeaturePoints(bars, selected.lookback)
	if err != nil {
		return TargetModel{}, err
	}
	examples := targetExamples(features, targets, targetKind)
	model := TargetModel{TargetKind: targetKind, Lookback: selected.lookback, Report: selected.report}
	model.OOF, err = materializeTargetOOF(ctx, features, targets, targetKind, horizon, config)
	if err != nil {
		return TargetModel{}, err
	}
	switch targetKind {
	case TargetDirection:
		learner, trainErr := trainLearner(ctx, examples, 1, LossBinary, config.Learner)
		if trainErr != nil {
			return TargetModel{}, trainErr
		}
		model.Learner = &learner
		probabilities, labels := learnerCalibrationSamples(learner, examples)
		calibrator, calibrationErr := FitProbabilityCalibrator(probabilities, labels, 10)
		if calibrationErr != nil {
			return TargetModel{}, calibrationErr
		}
		model.Calibrator = &calibrator
	case TargetPathActivity:
		learner, trainErr := trainLearner(ctx, examples, 6, LossMSE, config.Learner)
		if trainErr != nil {
			return TargetModel{}, trainErr
		}
		model.Learner = &learner
	case TargetJointSpace:
		distribution, trainErr := TrainDistributionContext(ctx, features, targets, config.Learner, config.RegionRule)
		if trainErr != nil {
			return TargetModel{}, trainErr
		}
		model.Distribution = &distribution
	default:
		return TargetModel{}, fmt.Errorf("unsupported prediction target")
	}
	return model, nil
}

func materializeTargetOOF(ctx context.Context, features []FeaturePoint, targets []TargetPoint, targetKind string, horizon int, config TrainingConfig) ([]TargetOOFPoint, error) {
	examples := targetExamples(features, targets, targetKind)
	folds, err := BuildPurgedFolds(len(examples), config.Folds, config.MinimumTrain, horizon)
	if err != nil {
		return nil, err
	}
	result := make([]TargetOOFPoint, 0)
	for _, fold := range folds {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		train := examples[fold.TrainStart : fold.TrainEnd+1]
		validation := examples[fold.ValidationStart : fold.ValidationEnd+1]
		switch targetKind {
		case TargetDirection:
			learner, trainErr := trainLearner(ctx, train, 1, LossBinary, config.Learner)
			if trainErr != nil {
				return nil, trainErr
			}
			trainProbabilities, trainLabels := learnerCalibrationSamples(learner, train)
			calibrator, calibrationErr := FitProbabilityCalibrator(trainProbabilities, trainLabels, 10)
			if calibrationErr != nil {
				return nil, calibrationErr
			}
			for _, example := range validation {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				values, predictErr := learner.Predict(example.Feature)
				if predictErr != nil {
					return nil, predictErr
				}
				values[0], predictErr = calibrator.Predict(values[0])
				if predictErr != nil {
					return nil, predictErr
				}
				result = append(result, TargetOOFPoint{Index: example.Feature.Index, TimeMs: example.Feature.TimeMs, Values: values})
			}
		case TargetPathActivity:
			learner, trainErr := trainLearner(ctx, train, 6, LossMSE, config.Learner)
			if trainErr != nil {
				return nil, trainErr
			}
			for _, example := range validation {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				values, predictErr := learner.Predict(example.Feature)
				if predictErr != nil {
					return nil, predictErr
				}
				result = append(result, TargetOOFPoint{Index: example.Feature.Index, TimeMs: example.Feature.TimeMs, Values: values})
			}
		case TargetJointSpace:
			trainTargets := examplesToTargets(train, targets)
			distributionModel, trainErr := TrainDistributionContext(ctx, features, trainTargets, config.Learner, config.RegionRule)
			if trainErr != nil {
				return nil, trainErr
			}
			for _, example := range validation {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				distribution, regions, predictErr := distributionModel.Predict(example.Feature)
				if predictErr != nil {
					return nil, predictErr
				}
				result = append(result, TargetOOFPoint{Index: example.Feature.Index, TimeMs: example.Feature.TimeMs, Distribution: &distribution, SixRegions: &regions})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result, nil
}

func combineOOF(horizon int, direction, joint, activity []TargetOOFPoint, scale ActivityScaleModel, features []FeaturePoint) []Prediction {
	type parts struct{ direction, joint, activity *TargetOOFPoint }
	byIndex := map[int]*parts{}
	attach := func(points []TargetOOFPoint, kind string) {
		for index := range points {
			point := &points[index]
			entry := byIndex[point.Index]
			if entry == nil {
				entry = &parts{}
				byIndex[point.Index] = entry
			}
			switch kind {
			case TargetDirection:
				entry.direction = point
			case TargetJointSpace:
				entry.joint = point
			case TargetPathActivity:
				entry.activity = point
			}
		}
	}
	attach(direction, TargetDirection)
	attach(joint, TargetJointSpace)
	attach(activity, TargetPathActivity)
	indices := make([]int, 0, len(byIndex))
	for index, entry := range byIndex {
		if entry.direction != nil && entry.joint != nil && entry.activity != nil {
			indices = append(indices, index)
		}
	}
	sort.Ints(indices)
	result := make([]Prediction, 0, len(indices))
	for _, index := range indices {
		entry := byIndex[index]
		values := entry.activity.Values
		activityValue := decodeActivityPrediction(values)
		normalScale, _ := scale.PredictActivity(activityValue)
		result = append(result, Prediction{SchemaVersion: PredictionSchemaVersion, Index: index, TimeMs: entry.direction.TimeMs, Horizon: horizon, DirectionUpProbability: entry.direction.Values[0], JointDistribution: *entry.joint.Distribution, SixRegions: *entry.joint.SixRegions, PathActivity: activityValue, NormalActivityScale: normalScale, Available: true})
	}
	return result
}

func decodeActivityPrediction(values []float64) ActivityVector {
	decode := func(value float64) float64 { return math.Max(1e-12, math.Expm1(value)) }
	return ActivityVector{TRMean: decode(values[0]), TRStdDev: decode(values[1]), HighLowMean: decode(values[2]), HighLowStdDev: decode(values[3]), Parkinson: decode(values[4]), YangZhang: decode(values[5])}
}

func validateCandidate(ctx context.Context, features []FeaturePoint, targets []TargetPoint, targetKind string, horizon int, config TrainingConfig) (ModelReport, error) {
	examples := targetExamples(features, targets, targetKind)
	folds, err := BuildPurgedFolds(len(examples), config.Folds, config.MinimumTrain, horizon)
	if err != nil {
		return ModelReport{}, err
	}
	report := ModelReport{SchemaVersion: ModelReportVersion, Route: config.Route, Horizon: horizon, TargetKind: targetKind, WalkForwardVersion: WalkForwardVersion}
	losses, baselines := make([]float64, 0), make([]float64, 0)
	marginalUp, marginalDown, zeroBrier, regionBrier, reliability := []float64{}, []float64{}, []float64{}, []float64{}, []float64{}
	allCriteriaPassed := true
	for _, fold := range folds {
		if err := ctx.Err(); err != nil {
			return ModelReport{}, err
		}
		train := examples[fold.TrainStart : fold.TrainEnd+1]
		validation := examples[fold.ValidationStart : fold.ValidationEnd+1]
		evaluation, foldErr := fitAndScoreFold(ctx, features, targets, train, validation, targetKind, config)
		if foldErr != nil {
			return ModelReport{}, foldErr
		}
		report.Folds = append(report.Folds, ValidationFold{Fold: fold.Fold, TrainStartIndex: fold.TrainStart, TrainEndIndex: fold.TrainEnd, ValidationStartIndex: fold.ValidationStart, ValidationEndIndex: fold.ValidationEnd, Purge: horizon, Loss: evaluation.Loss, BaselineLoss: evaluation.BaselineLoss, CalibrationError: evaluation.CalibrationError, BaselineCalibrationError: evaluation.BaselineCalibrationError, MarginalUpError: evaluation.MarginalUpError, BaselineMarginalUpError: evaluation.BaselineMarginalUpError, MarginalDownError: evaluation.MarginalDownError, BaselineMarginalDownError: evaluation.BaselineMarginalDownError, ZeroTypeBrier: evaluation.ZeroTypeBrier, BaselineZeroTypeBrier: evaluation.BaselineZeroTypeBrier, SixRegionBrier: evaluation.SixRegionBrier, BaselineSixRegionBrier: evaluation.BaselineSixRegionBrier, CriteriaPassed: evaluation.CriteriaPassed, Support: len(validation)})
		losses = append(losses, evaluation.Loss)
		baselines = append(baselines, evaluation.BaselineLoss)
		marginalUp = append(marginalUp, evaluation.MarginalUpError)
		marginalDown = append(marginalDown, evaluation.MarginalDownError)
		zeroBrier = append(zeroBrier, evaluation.ZeroTypeBrier)
		regionBrier = append(regionBrier, evaluation.SixRegionBrier)
		reliability = append(reliability, evaluation.CalibrationError)
		allCriteriaPassed = allCriteriaPassed && evaluation.CriteriaPassed
	}
	report.MeanLoss, report.StandardError = meanAndSE(losses)
	report.MeanBaselineLoss, _ = meanAndSE(baselines)
	report.MeanMarginalUpError, _ = meanAndSE(marginalUp)
	report.MeanMarginalDownError, _ = meanAndSE(marginalDown)
	report.MeanZeroTypeBrier, _ = meanAndSE(zeroBrier)
	report.MeanSixRegionBrier, _ = meanAndSE(regionBrier)
	report.MeanReliabilityError, _ = meanAndSE(reliability)
	report.BaselineGatePassed = report.MeanLoss < report.MeanBaselineLoss && allCriteriaPassed
	report.PredictiveStatus = "validated"
	if !report.BaselineGatePassed {
		report.PredictiveStatus = "not_proven_predictive"
	}
	if targetKind == TargetDirection {
		oof, materializeErr := materializeTargetOOF(ctx, features, targets, targetKind, horizon, config)
		if ctx.Err() != nil {
			return ModelReport{}, ctx.Err()
		}
		if materializeErr == nil {
			labels := targetLabelsByIndex(targets)
			probabilities, actual := make([]float64, 0, len(oof)), make([]float64, 0, len(oof))
			for _, point := range oof {
				if label, ok := labels[point.Index]; ok && len(point.Values) == 1 {
					probabilities, actual = append(probabilities, point.Values[0]), append(actual, label)
				}
			}
			report.Reliability = BuildReliabilityBins(probabilities, actual, 10)
		}
	}
	return report, nil
}

type foldEvaluation struct {
	Loss, BaselineLoss                           float64
	CalibrationError, BaselineCalibrationError   float64
	MarginalUpError, BaselineMarginalUpError     float64
	MarginalDownError, BaselineMarginalDownError float64
	ZeroTypeBrier, BaselineZeroTypeBrier         float64
	SixRegionBrier, BaselineSixRegionBrier       float64
	CriteriaPassed                               bool
}

func fitAndScoreFold(ctx context.Context, features []FeaturePoint, targets []TargetPoint, train, validation []SupervisedExample, targetKind string, config TrainingConfig) (foldEvaluation, error) {
	if err := ctx.Err(); err != nil {
		return foldEvaluation{}, err
	}
	switch targetKind {
	case TargetDirection:
		model, err := trainLearner(ctx, train, 1, LossBinary, config.Learner)
		if err != nil {
			return foldEvaluation{}, err
		}
		prevalence := 0.0
		for _, example := range train {
			prevalence += example.Target[0]
		}
		prevalence /= float64(len(train))
		trainProbabilities, trainLabels := learnerCalibrationSamples(model, train)
		calibrator, err := FitProbabilityCalibrator(trainProbabilities, trainLabels, 10)
		if err != nil {
			return foldEvaluation{}, err
		}
		loss, baseline, brier, baselineBrier := 0.0, 0.0, 0.0, 0.0
		for _, example := range validation {
			prediction, predictErr := model.Predict(example.Feature)
			if predictErr != nil {
				return foldEvaluation{}, predictErr
			}
			prediction[0], predictErr = calibrator.Predict(prediction[0])
			if predictErr != nil {
				return foldEvaluation{}, predictErr
			}
			y := example.Target[0]
			loss += binaryLogLoss(prediction[0], y)
			baseline += binaryLogLoss(prevalence, y)
			brier += (prediction[0] - y) * (prediction[0] - y)
			baselineBrier += (prevalence - y) * (prevalence - y)
		}
		denominator := float64(len(validation))
		result := foldEvaluation{Loss: loss / denominator, BaselineLoss: baseline / denominator, CalibrationError: brier / denominator, BaselineCalibrationError: baselineBrier / denominator}
		result.CriteriaPassed = result.CalibrationError <= result.BaselineCalibrationError
		return result, nil
	case TargetPathActivity:
		model, err := trainLearner(ctx, train, 6, LossMSE, config.Learner)
		if err != nil {
			return foldEvaluation{}, err
		}
		means := make([]float64, 6)
		for _, example := range train {
			for i, v := range example.Target {
				means[i] += v
			}
		}
		for i := range means {
			means[i] /= float64(len(train))
		}
		loss, baseline := 0.0, 0.0
		for _, example := range validation {
			prediction, predictErr := model.Predict(example.Feature)
			if predictErr != nil {
				return foldEvaluation{}, predictErr
			}
			loss += mse(prediction, example.Target)
			baseline += mse(means, example.Target)
		}
		denominator := float64(len(validation))
		return foldEvaluation{Loss: loss / denominator, BaselineLoss: baseline / denominator, CriteriaPassed: loss <= baseline}, nil
	case TargetJointSpace:
		trainTargets := examplesToTargets(train, targets)
		model, err := TrainDistributionContext(ctx, features, trainTargets, config.Learner, config.RegionRule)
		if err != nil {
			return foldEvaluation{}, err
		}
		baselineDistribution := unconditionalJointDistribution(trainTargets)
		loss, baseline := 0.0, 0.0
		upError, baseUpError, downError, baseDownError := 0.0, 0.0, 0.0, 0.0
		zeroBrier, baseZeroBrier, regionBrier, baseRegionBrier := 0.0, 0.0, 0.0, 0.0
		zeroPredictions, zeroBaselines, zeroLabels := [][]float64{}, [][]float64{}, []int{}
		regionPredictions, regionBaselines, regionLabels := [][]float64{}, [][]float64{}, []int{}
		for _, example := range validation {
			target, ok := exampleTarget(example, targets)
			if !ok {
				continue
			}
			distribution, _, predictErr := model.Predict(example.Feature)
			if predictErr != nil {
				return foldEvaluation{}, predictErr
			}
			loss += distributionNLL(distribution, target)
			baseline += distributionNLL(baselineDistribution, target)
			predictedUp, predictedDown := distributionMarginalMeans(distribution)
			baseUp, baseDown := distributionMarginalMeans(baselineDistribution)
			upError += math.Abs(predictedUp - target.NormalizedUp)
			baseUpError += math.Abs(baseUp - target.NormalizedUp)
			downError += math.Abs(predictedDown - target.NormalizedDown)
			baseDownError += math.Abs(baseDown - target.NormalizedDown)
			probabilities := zeroProbabilities(distribution)
			baselineProbabilities := zeroProbabilities(baselineDistribution)
			class := zeroClass(target.NormalizedUp, target.NormalizedDown)
			for index, probability := range probabilities {
				actual := 0.0
				if index == class {
					actual = 1
				}
				zeroBrier += (probability - actual) * (probability - actual) / 4
				baseZeroBrier += (baselineProbabilities[index] - actual) * (baselineProbabilities[index] - actual) / 4
			}
			zeroPredictions = append(zeroPredictions, probabilities)
			zeroBaselines = append(zeroBaselines, baselineProbabilities)
			zeroLabels = append(zeroLabels, class)
			regions := IntegrateSixRegions(distribution, config.RegionRule, 256)
			baselineRegions := IntegrateSixRegions(baselineDistribution, config.RegionRule, 256)
			regionValues := regionProbabilities(regions)
			baselineRegionValues := regionProbabilities(baselineRegions)
			regionClass := actualRegionClass(target, config.RegionRule)
			for index, probability := range regionValues {
				actual := 0.0
				if index == regionClass {
					actual = 1
				}
				regionBrier += (probability - actual) * (probability - actual) / 6
				baseRegionBrier += (baselineRegionValues[index] - actual) * (baselineRegionValues[index] - actual) / 6
			}
			regionPredictions = append(regionPredictions, regionValues)
			regionBaselines = append(regionBaselines, baselineRegionValues)
			regionLabels = append(regionLabels, regionClass)
		}
		denominator := float64(len(validation))
		reliability := 0.5 * (multiclassReliabilityError(zeroPredictions, zeroLabels, 10) + multiclassReliabilityError(regionPredictions, regionLabels, 10))
		baseReliability := 0.5 * (multiclassReliabilityError(zeroBaselines, zeroLabels, 10) + multiclassReliabilityError(regionBaselines, regionLabels, 10))
		result := foldEvaluation{Loss: loss / denominator, BaselineLoss: baseline / denominator, CalibrationError: reliability, BaselineCalibrationError: baseReliability, MarginalUpError: upError / denominator, BaselineMarginalUpError: baseUpError / denominator, MarginalDownError: downError / denominator, BaselineMarginalDownError: baseDownError / denominator, ZeroTypeBrier: zeroBrier / denominator, BaselineZeroTypeBrier: baseZeroBrier / denominator, SixRegionBrier: regionBrier / denominator, BaselineSixRegionBrier: baseRegionBrier / denominator}
		result.CriteriaPassed = result.MarginalUpError <= result.BaselineMarginalUpError && result.MarginalDownError <= result.BaselineMarginalDownError && result.ZeroTypeBrier <= result.BaselineZeroTypeBrier && result.SixRegionBrier <= result.BaselineSixRegionBrier && result.CalibrationError <= result.BaselineCalibrationError
		return result, nil
	default:
		return foldEvaluation{}, fmt.Errorf("unsupported target kind")
	}
}

func unconditionalJointDistribution(targets []TargetPoint) JointDistribution {
	counts := [4]float64{1, 1, 1, 1}
	upLogs, downLogs := []float64{}, []float64{}
	bothLogs := [][2]float64{}
	for _, target := range targets {
		if !target.Normalized {
			continue
		}
		class := zeroClass(target.NormalizedUp, target.NormalizedDown)
		counts[class]++
		switch class {
		case 1:
			upLogs = append(upLogs, math.Log(target.NormalizedUp))
		case 2:
			downLogs = append(downLogs, math.Log(target.NormalizedDown))
		case 3:
			bothLogs = append(bothLogs, [2]float64{math.Log(target.NormalizedUp), math.Log(target.NormalizedDown)})
		}
	}
	total := counts[0] + counts[1] + counts[2] + counts[3]
	return JointDistribution{
		SchemaVersion: DistributionModelVersion,
		ZeroMass:      ZeroMass{BothZero: counts[0] / total, UpOnly: counts[1] / total, DownOnly: counts[2] / total, BothPositive: counts[3] / total},
		UpOnly:        fitUnivariateMixture(upLogs, selectComponentCount1D(upLogs, 3)),
		DownOnly:      fitUnivariateMixture(downLogs, selectComponentCount1D(downLogs, 3)),
		BothPositive:  fitBivariateMixture(bothLogs, selectComponentCount2D(bothLogs, 3)),
	}
}

func distributionMarginalMeans(distribution JointDistribution) (float64, float64) {
	up, down := 0.0, 0.0
	for _, component := range distribution.UpOnly {
		up += distribution.ZeroMass.UpOnly * component.Weight * math.Exp(component.MeanLog+component.Variance/2)
	}
	for _, component := range distribution.DownOnly {
		down += distribution.ZeroMass.DownOnly * component.Weight * math.Exp(component.MeanLog+component.Variance/2)
	}
	for _, component := range distribution.BothPositive {
		up += distribution.ZeroMass.BothPositive * component.Weight * math.Exp(component.MeanLogUp+component.VarLogUp/2)
		down += distribution.ZeroMass.BothPositive * component.Weight * math.Exp(component.MeanLogDown+component.VarLogDown/2)
	}
	return up, down
}

func zeroProbabilities(distribution JointDistribution) []float64 {
	return []float64{distribution.ZeroMass.BothZero, distribution.ZeroMass.UpOnly, distribution.ZeroMass.DownOnly, distribution.ZeroMass.BothPositive}
}

func regionProbabilities(regions SixRegionProbabilities) []float64 {
	return []float64{regions.UpSmall, regions.UpLarge, regions.BalancedSmall, regions.BalancedLarge, regions.DownSmall, regions.DownLarge}
}

func actualRegionClass(target TargetPoint, rule RegionRule) int {
	direction := actualSpaceDirection(target, rule.DirectionBoundary)
	large := target.NormalizedUp+target.NormalizedDown >= rule.MagnitudeBoundary
	switch direction {
	case SpaceDirectionUp:
		if large {
			return 1
		}
		return 0
	case SpaceDirectionDown:
		if large {
			return 5
		}
		return 4
	default:
		if large {
			return 3
		}
		return 2
	}
}

func multiclassReliabilityError(probabilities [][]float64, labels []int, binCount int) float64 {
	if len(probabilities) != len(labels) || len(labels) == 0 {
		return math.Inf(1)
	}
	classes := len(probabilities[0])
	total := 0.0
	for class := 0; class < classes; class++ {
		counts := make([]int, binCount)
		forecast := make([]float64, binCount)
		observed := make([]float64, binCount)
		for index, values := range probabilities {
			if len(values) != classes {
				continue
			}
			value := math.Max(0, math.Min(1, values[class]))
			bin := int(math.Min(float64(binCount-1), math.Floor(value*float64(binCount))))
			counts[bin]++
			forecast[bin] += value
			if labels[index] == class {
				observed[bin]++
			}
		}
		for bin, count := range counts {
			if count == 0 {
				continue
			}
			weight := float64(count) / float64(len(labels))
			total += weight * math.Abs(forecast[bin]/float64(count)-observed[bin]/float64(count)) / float64(classes)
		}
	}
	return total
}

func learnerCalibrationSamples(model LearnerModel, examples []SupervisedExample) ([]float64, []float64) {
	probabilities := make([]float64, 0, len(examples))
	labels := make([]float64, 0, len(examples))
	for _, example := range examples {
		prediction, err := model.Predict(example.Feature)
		if err == nil && len(prediction) == 1 && len(example.Target) == 1 {
			probabilities = append(probabilities, prediction[0])
			labels = append(labels, example.Target[0])
		}
	}
	return probabilities, labels
}

func targetLabelsByIndex(targets []TargetPoint) map[int]float64 {
	result := make(map[int]float64, len(targets))
	for _, target := range targets {
		if target.DirectionUp {
			result[target.Index] = 1
		} else {
			result[target.Index] = 0
		}
	}
	return result
}

func targetExamples(features []FeaturePoint, targets []TargetPoint, targetKind string) []SupervisedExample {
	result := make([]SupervisedExample, 0, len(targets))
	for _, target := range targets {
		if target.Index < 0 || target.Index >= len(features) || !features[target.Index].Available {
			continue
		}
		var output []float64
		switch targetKind {
		case TargetDirection:
			if target.DirectionUp {
				output = []float64{1}
			} else {
				output = []float64{0}
			}
		case TargetJointSpace:
			if !target.Normalized {
				continue
			}
			output = []float64{target.NormalizedUp, target.NormalizedDown}
		case TargetPathActivity:
			output = activityTrainingTarget(target.FutureActivity)
		}
		result = append(result, SupervisedExample{Feature: features[target.Index], Target: output})
	}
	return result
}

func activityTrainingTarget(value ActivityVector) []float64 {
	return []float64{math.Log1p(value.TRMean), math.Log1p(value.TRStdDev), math.Log1p(value.HighLowMean), math.Log1p(value.HighLowStdDev), math.Log1p(value.Parkinson), math.Log1p(value.YangZhang)}
}

func examplesToTargets(examples []SupervisedExample, all []TargetPoint) []TargetPoint {
	result := make([]TargetPoint, 0, len(examples))
	for _, example := range examples {
		if target, ok := exampleTarget(example, all); ok {
			result = append(result, target)
		}
	}
	return result
}
func exampleTarget(example SupervisedExample, all []TargetPoint) (TargetPoint, bool) {
	for _, target := range all {
		if target.Index == example.Feature.Index {
			return target, true
		}
	}
	return TargetPoint{}, false
}

func distributionNLL(distribution JointDistribution, target TargetPoint) float64 {
	class := zeroClass(target.NormalizedUp, target.NormalizedDown)
	mass := []float64{distribution.ZeroMass.BothZero, distribution.ZeroMass.UpOnly, distribution.ZeroMass.DownOnly, distribution.ZeroMass.BothPositive}[class]
	density := 1.0
	switch class {
	case 1:
		density = univariateDensity(math.Log(target.NormalizedUp), distribution.UpOnly) / target.NormalizedUp
	case 2:
		density = univariateDensity(math.Log(target.NormalizedDown), distribution.DownOnly) / target.NormalizedDown
	case 3:
		density = bivariateDensity([2]float64{math.Log(target.NormalizedUp), math.Log(target.NormalizedDown)}, distribution.BothPositive) / (target.NormalizedUp * target.NormalizedDown)
	}
	return -math.Log(math.Max(1e-15, mass*density))
}

func binaryLogLoss(probability, actual float64) float64 {
	probability = math.Max(1e-12, math.Min(1-1e-12, probability))
	return -(actual*math.Log(probability) + (1-actual)*math.Log(1-probability))
}
func mse(left, right []float64) float64 {
	total := 0.0
	for index := range left {
		delta := left[index] - right[index]
		total += delta * delta
	}
	return total / float64(len(left))
}
func meanAndSE(values []float64) (float64, float64) {
	mean, variance := meanVariance(values)
	return mean, math.Sqrt(variance / math.Max(1, float64(len(values))))
}
func uniqueSorted(values []int) []int {
	seen := map[int]bool{}
	result := make([]int, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Ints(result)
	return result
}
