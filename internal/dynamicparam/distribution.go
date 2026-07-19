package dynamicparam

import (
	"context"
	"fmt"
	"math"
	"sort"
)

type LearnerModel struct {
	Route string    `json:"route"`
	GAM   *GAMModel `json:"gam,omitempty"`
	TCN   *TCNModel `json:"tcn,omitempty"`
}

type RegionRule struct {
	DirectionBoundary float64 `json:"direction_boundary"`
	MagnitudeBoundary float64 `json:"magnitude_boundary"`
}

type DistributionModel struct {
	SchemaVersion string                         `json:"schema_version"`
	ZeroType      LearnerModel                   `json:"zero_type"`
	BothMean      *LearnerModel                  `json:"both_mean,omitempty"`
	UpMean        *LearnerModel                  `json:"up_mean,omitempty"`
	DownMean      *LearnerModel                  `json:"down_mean,omitempty"`
	BothResiduals []BivariateLognormalComponent  `json:"both_residuals,omitempty"`
	UpResiduals   []UnivariateLognormalComponent `json:"up_residuals,omitempty"`
	DownResiduals []UnivariateLognormalComponent `json:"down_residuals,omitempty"`
	RegionRule    RegionRule                     `json:"region_rule"`
}

type LearnerConfig struct {
	Route string    `json:"route"`
	GAM   GAMConfig `json:"gam"`
	TCN   TCNConfig `json:"tcn"`
}

func TrainDistribution(features []FeaturePoint, targets []TargetPoint, config LearnerConfig, regionRule RegionRule) (DistributionModel, error) {
	return TrainDistributionContext(context.Background(), features, targets, config, regionRule)
}

func TrainDistributionContext(ctx context.Context, features []FeaturePoint, targets []TargetPoint, config LearnerConfig, regionRule RegionRule) (DistributionModel, error) {
	if err := ctx.Err(); err != nil {
		return DistributionModel{}, err
	}
	if regionRule.DirectionBoundary <= 0 || regionRule.DirectionBoundary >= 1 || regionRule.MagnitudeBoundary <= 0 {
		return DistributionModel{}, fmt.Errorf("invalid six-region boundaries")
	}
	zeroExamples := make([]SupervisedExample, 0)
	bothExamples := make([]SupervisedExample, 0)
	upExamples := make([]SupervisedExample, 0)
	downExamples := make([]SupervisedExample, 0)
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return DistributionModel{}, err
		}
		if target.Index < 0 || target.Index >= len(features) || !features[target.Index].Available || !target.Normalized {
			continue
		}
		class := zeroClass(target.NormalizedUp, target.NormalizedDown)
		oneHot := make([]float64, 4)
		oneHot[class] = 1
		zeroExamples = append(zeroExamples, SupervisedExample{Feature: features[target.Index], Target: oneHot})
		switch class {
		case 1:
			upExamples = append(upExamples, SupervisedExample{Feature: features[target.Index], Target: []float64{math.Log(target.NormalizedUp)}})
		case 2:
			downExamples = append(downExamples, SupervisedExample{Feature: features[target.Index], Target: []float64{math.Log(target.NormalizedDown)}})
		case 3:
			bothExamples = append(bothExamples, SupervisedExample{Feature: features[target.Index], Target: []float64{math.Log(target.NormalizedUp), math.Log(target.NormalizedDown)}})
		}
	}
	if len(zeroExamples) < 8 {
		return DistributionModel{}, fmt.Errorf("insufficient normalized samples for joint distribution")
	}
	zeroModel, err := trainLearner(ctx, zeroExamples, 4, LossSoftmax, config)
	if err != nil {
		return DistributionModel{}, err
	}
	model := DistributionModel{SchemaVersion: DistributionModelVersion, ZeroType: zeroModel, RegionRule: regionRule}
	if len(bothExamples) >= 4 {
		learner, trainErr := trainLearner(ctx, bothExamples, 2, LossMSE, config)
		if trainErr != nil {
			return DistributionModel{}, trainErr
		}
		model.BothMean = &learner
		model.BothResiduals = selectBivariateResidualMixture(bothExamples, learner, 3)
	}
	if len(upExamples) >= 3 {
		learner, trainErr := trainLearner(ctx, upExamples, 1, LossMSE, config)
		if trainErr != nil {
			return DistributionModel{}, trainErr
		}
		model.UpMean = &learner
		model.UpResiduals = selectUnivariateResidualMixture(upExamples, learner, 3)
	}
	if len(downExamples) >= 3 {
		learner, trainErr := trainLearner(ctx, downExamples, 1, LossMSE, config)
		if trainErr != nil {
			return DistributionModel{}, trainErr
		}
		model.DownMean = &learner
		model.DownResiduals = selectUnivariateResidualMixture(downExamples, learner, 3)
	}
	return model, nil
}

func (model DistributionModel) Predict(feature FeaturePoint) (JointDistribution, SixRegionProbabilities, error) {
	if model.SchemaVersion != DistributionModelVersion {
		return JointDistribution{}, SixRegionProbabilities{}, fmt.Errorf("unsupported distribution model")
	}
	zero, err := model.ZeroType.Predict(feature)
	if err != nil || len(zero) != 4 {
		return JointDistribution{}, SixRegionProbabilities{}, fmt.Errorf("predict zero mass: %w", err)
	}
	distribution := JointDistribution{SchemaVersion: DistributionModelVersion, ZeroMass: ZeroMass{BothZero: zero[0], UpOnly: zero[1], DownOnly: zero[2], BothPositive: zero[3]}}
	if model.BothMean != nil {
		mean, predictErr := model.BothMean.Predict(feature)
		if predictErr != nil {
			return JointDistribution{}, SixRegionProbabilities{}, predictErr
		}
		for _, residual := range model.BothResiduals {
			component := residual
			component.MeanLogUp += mean[0]
			component.MeanLogDown += mean[1]
			distribution.BothPositive = append(distribution.BothPositive, component)
		}
	}
	if model.UpMean != nil {
		mean, predictErr := model.UpMean.Predict(feature)
		if predictErr != nil {
			return JointDistribution{}, SixRegionProbabilities{}, predictErr
		}
		for _, residual := range model.UpResiduals {
			component := residual
			component.MeanLog += mean[0]
			distribution.UpOnly = append(distribution.UpOnly, component)
		}
	}
	if model.DownMean != nil {
		mean, predictErr := model.DownMean.Predict(feature)
		if predictErr != nil {
			return JointDistribution{}, SixRegionProbabilities{}, predictErr
		}
		for _, residual := range model.DownResiduals {
			component := residual
			component.MeanLog += mean[0]
			distribution.DownOnly = append(distribution.DownOnly, component)
		}
	}
	regions := IntegrateSixRegions(distribution, model.RegionRule, 512)
	return distribution, regions, nil
}

func (model LearnerModel) Predict(feature FeaturePoint) ([]float64, error) {
	switch model.Route {
	case RouteExplainable:
		if model.GAM == nil {
			return nil, fmt.Errorf("missing explainable model")
		}
		return model.GAM.Predict(feature)
	case RouteTCN:
		if model.TCN == nil {
			return nil, fmt.Errorf("missing TCN model")
		}
		return model.TCN.Predict(feature)
	default:
		return nil, fmt.Errorf("unsupported model route %q", model.Route)
	}
}

func trainLearner(ctx context.Context, examples []SupervisedExample, outputs int, loss string, config LearnerConfig) (LearnerModel, error) {
	if err := ctx.Err(); err != nil {
		return LearnerModel{}, err
	}
	switch config.Route {
	case RouteExplainable:
		model, err := TrainGAMContext(ctx, examples, outputs, loss, config.GAM)
		if err != nil {
			return LearnerModel{}, err
		}
		return LearnerModel{Route: config.Route, GAM: &model}, nil
	case RouteTCN:
		model, err := TrainTCNContext(ctx, examples, outputs, loss, config.TCN)
		if err != nil {
			return LearnerModel{}, err
		}
		return LearnerModel{Route: config.Route, TCN: &model}, nil
	default:
		return LearnerModel{}, fmt.Errorf("unsupported model route %q", config.Route)
	}
}

func zeroClass(up, down float64) int {
	upPositive := up > 0
	downPositive := down > 0
	switch {
	case !upPositive && !downPositive:
		return 0
	case upPositive && !downPositive:
		return 1
	case !upPositive && downPositive:
		return 2
	default:
		return 3
	}
}

func selectUnivariateResidualMixture(examples []SupervisedExample, learner LearnerModel, maxComponents int) []UnivariateLognormalComponent {
	residuals := make([]float64, 0, len(examples))
	for _, example := range examples {
		prediction, err := learner.Predict(example.Feature)
		if err == nil {
			residuals = append(residuals, example.Target[0]-prediction[0])
		}
	}
	return fitUnivariateMixture(residuals, selectComponentCount1D(residuals, maxComponents))
}

func selectBivariateResidualMixture(examples []SupervisedExample, learner LearnerModel, maxComponents int) []BivariateLognormalComponent {
	residuals := make([][2]float64, 0, len(examples))
	for _, example := range examples {
		prediction, err := learner.Predict(example.Feature)
		if err == nil {
			residuals = append(residuals, [2]float64{example.Target[0] - prediction[0], example.Target[1] - prediction[1]})
		}
	}
	return fitBivariateMixture(residuals, selectComponentCount2D(residuals, maxComponents))
}

func selectComponentCount1D(values []float64, maximum int) int {
	if len(values) < 10 {
		return 1
	}
	trainEnd := len(values) * 4 / 5
	bestK, bestLoss, bestSE := 1, math.Inf(1), 0.0
	losses := map[int]float64{}
	for k := 1; k <= maximum && k*3 < trainEnd; k++ {
		mixture := fitUnivariateMixture(values[:trainEnd], k)
		observations := make([]float64, 0, len(values)-trainEnd)
		for _, value := range values[trainEnd:] {
			observations = append(observations, -math.Log(math.Max(1e-15, univariateDensity(value, mixture))))
		}
		mean, variance := meanVariance(observations)
		losses[k] = mean
		if mean < bestLoss {
			bestK, bestLoss = k, mean
			bestSE = math.Sqrt(variance / math.Max(1, float64(len(observations))))
		}
	}
	for k := 1; k <= bestK; k++ {
		if loss, ok := losses[k]; ok && loss <= bestLoss+bestSE {
			return k
		}
	}
	return bestK
}

func selectComponentCount2D(values [][2]float64, maximum int) int {
	if len(values) < 12 {
		return 1
	}
	trainEnd := len(values) * 4 / 5
	bestK, bestLoss, bestSE := 1, math.Inf(1), 0.0
	losses := map[int]float64{}
	for k := 1; k <= maximum && k*5 < trainEnd; k++ {
		mixture := fitBivariateMixture(values[:trainEnd], k)
		observations := make([]float64, 0, len(values)-trainEnd)
		for _, value := range values[trainEnd:] {
			observations = append(observations, -math.Log(math.Max(1e-15, bivariateDensity(value, mixture))))
		}
		mean, variance := meanVariance(observations)
		losses[k] = mean
		if mean < bestLoss {
			bestK, bestLoss = k, mean
			bestSE = math.Sqrt(variance / math.Max(1, float64(len(observations))))
		}
	}
	for k := 1; k <= bestK; k++ {
		if loss, ok := losses[k]; ok && loss <= bestLoss+bestSE {
			return k
		}
	}
	return bestK
}

func fitUnivariateMixture(values []float64, k int) []UnivariateLognormalComponent {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	centers := make([]float64, k)
	for index := range centers {
		centers[index] = sorted[(2*index+1)*len(sorted)/(2*k)]
	}
	assignments := make([]int, len(values))
	for iteration := 0; iteration < 30; iteration++ {
		counts := make([]int, k)
		sums := make([]float64, k)
		for index, value := range values {
			best := 0
			for candidate := 1; candidate < k; candidate++ {
				if math.Abs(value-centers[candidate]) < math.Abs(value-centers[best]) {
					best = candidate
				}
			}
			assignments[index] = best
			counts[best]++
			sums[best] += value
		}
		for index := range centers {
			if counts[index] > 0 {
				centers[index] = sums[index] / float64(counts[index])
			}
		}
	}
	result := make([]UnivariateLognormalComponent, k)
	for cluster := 0; cluster < k; cluster++ {
		clusterValues := make([]float64, 0)
		for index, value := range values {
			if assignments[index] == cluster {
				clusterValues = append(clusterValues, value)
			}
		}
		mean, variance := meanVariance(clusterValues)
		result[cluster] = UnivariateLognormalComponent{Weight: float64(len(clusterValues)) / float64(len(values)), MeanLog: mean, Variance: math.Max(variance, 1e-6)}
	}
	return result
}

func fitBivariateMixture(values [][2]float64, k int) []BivariateLognormalComponent {
	if len(values) == 0 {
		return nil
	}
	centers := make([][2]float64, k)
	for index := range centers {
		centers[index] = values[(2*index+1)*len(values)/(2*k)]
	}
	assignments := make([]int, len(values))
	for iteration := 0; iteration < 30; iteration++ {
		counts := make([]int, k)
		sums := make([][2]float64, k)
		for index, value := range values {
			best := 0
			for candidate := 1; candidate < k; candidate++ {
				if squaredDistance(value, centers[candidate]) < squaredDistance(value, centers[best]) {
					best = candidate
				}
			}
			assignments[index] = best
			counts[best]++
			sums[best][0] += value[0]
			sums[best][1] += value[1]
		}
		for index := range centers {
			if counts[index] > 0 {
				centers[index] = [2]float64{sums[index][0] / float64(counts[index]), sums[index][1] / float64(counts[index])}
			}
		}
	}
	result := make([]BivariateLognormalComponent, k)
	for cluster := 0; cluster < k; cluster++ {
		clusterValues := make([][2]float64, 0)
		for index, value := range values {
			if assignments[index] == cluster {
				clusterValues = append(clusterValues, value)
			}
		}
		component := covarianceComponent(clusterValues)
		component.Weight = float64(len(clusterValues)) / float64(len(values))
		result[cluster] = component
	}
	return result
}

func covarianceComponent(values [][2]float64) BivariateLognormalComponent {
	if len(values) == 0 {
		return BivariateLognormalComponent{VarLogUp: 1e-6, VarLogDown: 1e-6}
	}
	meanUp, meanDown := 0.0, 0.0
	for _, value := range values {
		meanUp += value[0]
		meanDown += value[1]
	}
	meanUp /= float64(len(values))
	meanDown /= float64(len(values))
	varUp, varDown, covariance := 0.0, 0.0, 0.0
	for _, value := range values {
		up := value[0] - meanUp
		down := value[1] - meanDown
		varUp += up * up
		varDown += down * down
		covariance += up * down
	}
	denominator := math.Max(1, float64(len(values)-1))
	varUp, varDown, covariance = varUp/denominator, varDown/denominator, covariance/denominator
	varUp, varDown = math.Max(varUp, 1e-6), math.Max(varDown, 1e-6)
	limit := 0.999 * math.Sqrt(varUp*varDown)
	covariance = math.Max(-limit, math.Min(limit, covariance))
	return BivariateLognormalComponent{MeanLogUp: meanUp, MeanLogDown: meanDown, VarLogUp: varUp, VarLogDown: varDown, Covariance: covariance}
}

func univariateDensity(value float64, mixture []UnivariateLognormalComponent) float64 {
	total := 0.0
	for _, component := range mixture {
		variance := math.Max(component.Variance, 1e-9)
		delta := value - component.MeanLog
		total += component.Weight * math.Exp(-delta*delta/(2*variance)) / math.Sqrt(2*math.Pi*variance)
	}
	return total
}

func bivariateDensity(value [2]float64, mixture []BivariateLognormalComponent) float64 {
	total := 0.0
	for _, component := range mixture {
		determinant := component.VarLogUp*component.VarLogDown - component.Covariance*component.Covariance
		if determinant <= 0 {
			continue
		}
		x, y := value[0]-component.MeanLogUp, value[1]-component.MeanLogDown
		quadratic := (component.VarLogDown*x*x - 2*component.Covariance*x*y + component.VarLogUp*y*y) / determinant
		total += component.Weight * math.Exp(-quadratic/2) / (2 * math.Pi * math.Sqrt(determinant))
	}
	return total
}

func IntegrateSixRegions(distribution JointDistribution, rule RegionRule, samples int) SixRegionProbabilities {
	if samples < 32 {
		samples = 32
	}
	result := SixRegionProbabilities{BalancedSmall: distribution.ZeroMass.BothZero}
	for _, component := range distribution.UpOnly {
		for index := 1; index <= samples; index++ {
			up := math.Exp(component.MeanLog + math.Sqrt(component.Variance)*inverseNormal(halton(index, 2)))
			weight := distribution.ZeroMass.UpOnly * component.Weight / float64(samples)
			if up >= rule.MagnitudeBoundary {
				result.UpLarge += weight
			} else {
				result.UpSmall += weight
			}
		}
	}
	for _, component := range distribution.DownOnly {
		for index := 1; index <= samples; index++ {
			down := math.Exp(component.MeanLog + math.Sqrt(component.Variance)*inverseNormal(halton(index, 3)))
			weight := distribution.ZeroMass.DownOnly * component.Weight / float64(samples)
			if down >= rule.MagnitudeBoundary {
				result.DownLarge += weight
			} else {
				result.DownSmall += weight
			}
		}
	}
	for _, component := range distribution.BothPositive {
		standardUp := math.Sqrt(component.VarLogUp)
		correlation := component.Covariance / math.Sqrt(component.VarLogUp*component.VarLogDown)
		correlation = math.Max(-0.999, math.Min(0.999, correlation))
		for index := 1; index <= samples; index++ {
			z1 := inverseNormal(halton(index, 2))
			z2 := inverseNormal(halton(index, 3))
			up := math.Exp(component.MeanLogUp + standardUp*z1)
			down := math.Exp(component.MeanLogDown + math.Sqrt(component.VarLogDown)*(correlation*z1+math.Sqrt(1-correlation*correlation)*z2))
			addRegion(&result, up, down, distribution.ZeroMass.BothPositive*component.Weight/float64(samples), rule)
		}
	}
	normalizeRegions(&result)
	return result
}

func addRegion(result *SixRegionProbabilities, up, down, weight float64, rule RegionRule) {
	magnitude := up + down
	direction := (up - down) / magnitude
	large := magnitude >= rule.MagnitudeBoundary
	switch {
	case direction > rule.DirectionBoundary && large:
		result.UpLarge += weight
	case direction > rule.DirectionBoundary:
		result.UpSmall += weight
	case direction < -rule.DirectionBoundary && large:
		result.DownLarge += weight
	case direction < -rule.DirectionBoundary:
		result.DownSmall += weight
	case large:
		result.BalancedLarge += weight
	default:
		result.BalancedSmall += weight
	}
}

func normalizeRegions(result *SixRegionProbabilities) {
	total := result.UpSmall + result.UpLarge + result.BalancedSmall + result.BalancedLarge + result.DownSmall + result.DownLarge
	if total <= 0 {
		return
	}
	result.UpSmall /= total
	result.UpLarge /= total
	result.BalancedSmall /= total
	result.BalancedLarge /= total
	result.DownSmall /= total
	result.DownLarge /= total
}

func squaredDistance(left, right [2]float64) float64 {
	up, down := left[0]-right[0], left[1]-right[1]
	return up*up + down*down
}

func halton(index, base int) float64 {
	result, fraction := 0.0, 1.0/float64(base)
	for index > 0 {
		result += fraction * float64(index%base)
		index /= base
		fraction /= float64(base)
	}
	return math.Max(1e-12, math.Min(1-1e-12, result))
}

// inverseNormal is Acklam's deterministic approximation of the standard normal quantile.
func inverseNormal(probability float64) float64 {
	a := [...]float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02, 1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := [...]float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02, 6.680131188771972e+01, -1.328068155288572e+01}
	c := [...]float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00, -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	d := [...]float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00, 3.754408661907416e+00}
	if probability < 0.02425 {
		q := math.Sqrt(-2 * math.Log(probability))
		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	}
	if probability > 1-0.02425 {
		q := math.Sqrt(-2 * math.Log(1-probability))
		return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	}
	q := probability - 0.5
	r := q * q
	return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q / (((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
}
