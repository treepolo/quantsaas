package dynamicparam

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"quantsaas/internal/quant"
)

const GeometryModelSchemaVersion = "geometry-conditional-joint-v1"

// GeometryDensityFloor prevents an extreme but finite tail probability from
// underflowing to zero and incorrectly invalidating an otherwise usable fold.
const geometryDensityFloor = 1e-300

// GeometryRegionModel is one frozen conditional joint-density region. The
// coefficients operate on the two raw geometry inputs and are not a history
// lookup or a normalized feature transform.
type GeometryRegionModel struct {
	Weight              float64    `json:"weight"`
	AreaCoefficients    [3]float64 `json:"area_coefficients"`
	SlopeCoefficients   [3]float64 `json:"slope_coefficients"`
	AreaStdDev          float64    `json:"area_std_dev"`
	SlopeStdDev         float64    `json:"slope_std_dev"`
	AreaSlopeCovariance float64    `json:"area_slope_covariance"`
}

type GeometryDistribution struct {
	SchemaVersion string                 `json:"schema_version"`
	Regions       []GeometryRegionOutput `json:"regions"`
}

type GeometryRegionOutput struct {
	Probability         float64 `json:"probability"`
	AreaCenter          float64 `json:"area_center"`
	SlopeCenter         float64 `json:"slope_center"`
	AreaLower           float64 `json:"area_lower"`
	AreaUpper           float64 `json:"area_upper"`
	SlopeLower          float64 `json:"slope_lower"`
	SlopeUpper          float64 `json:"slope_upper"`
	AreaSlopeCovariance float64 `json:"area_slope_covariance"`
}

type GeometryModel struct {
	SchemaVersion string                   `json:"schema_version"`
	Horizon       int                      `json:"horizon"`
	Lookback      int                      `json:"lookback"`
	Regions       []GeometryRegionModel    `json:"regions"`
	Report        GeometryValidationReport `json:"report"`
}

type GeometryValidationReport struct {
	Regions            int     `json:"regions"`
	Samples            int     `json:"samples"`
	ValidationSamples  int     `json:"validation_samples"`
	JointNLL           float64 `json:"joint_nll"`
	BaselineJointNLL   float64 `json:"baseline_joint_nll"`
	AreaNLL            float64 `json:"area_nll"`
	SlopeNLL           float64 `json:"slope_nll"`
	JointBrier         float64 `json:"joint_brier"`
	CalibrationError   float64 `json:"calibration_error"`
	OutOfSample        bool    `json:"out_of_sample"`
	BaselineGatePassed bool    `json:"baseline_gate_passed"`
	Purge              int     `json:"purge"`
	WalkForwardVersion string  `json:"walk_forward_version"`
}

type GeometryCandidateReport struct {
	Lookback int                      `json:"lookback"`
	Regions  int                      `json:"regions"`
	Report   GeometryValidationReport `json:"report"`
}

type GeometryTrainingConfig struct {
	Lookbacks    []int `json:"lookbacks"`
	Folds        int   `json:"folds"`
	MinimumTrain int   `json:"minimum_train"`
}

type GeometryTrainingResult struct {
	SchemaVersion string                    `json:"schema_version"`
	Horizon       int                       `json:"horizon"`
	SelectedModel GeometryModel             `json:"selected_model"`
	Candidates    []GeometryCandidateReport `json:"candidates"`
}

type GeometryPrediction struct {
	SchemaVersion     string               `json:"schema_version"`
	Index             int                  `json:"index"`
	TimeMs            int64                `json:"time_ms"`
	Horizon           int                  `json:"horizon"`
	Feature           GeometryPoint        `json:"feature"`
	Distribution      GeometryDistribution `json:"distribution"`
	Available         bool                 `json:"available"`
	UnavailableReason string               `json:"unavailable_reason,omitempty"`
	ContentHash       string               `json:"content_hash"`
}

func PredictGeometryModel(model GeometryModel, bars []quant.Bar) ([]GeometryPrediction, error) {
	features, err := BuildGeometryFeatures(bars, model.Lookback)
	if err != nil {
		return nil, err
	}
	result := make([]GeometryPrediction, 0, len(features))
	for _, feature := range features {
		prediction := GeometryPrediction{SchemaVersion: GeometryModelSchemaVersion, Index: feature.Index, TimeMs: feature.TimeMs, Horizon: model.Horizon, Feature: feature}
		if !feature.Available {
			prediction.UnavailableReason = feature.UnavailableReason
			prediction.ContentHash = hashGeometryPrediction(prediction)
			result = append(result, prediction)
			continue
		}
		distribution, predictErr := model.Predict(feature)
		if predictErr != nil {
			return nil, predictErr
		}
		prediction.Distribution, prediction.Available = distribution, true
		prediction.ContentHash = hashGeometryPrediction(prediction)
		result = append(result, prediction)
	}
	return result, nil
}

func hashGeometryPrediction(prediction GeometryPrediction) string {
	type hashInput struct {
		SchemaVersion     string               `json:"schema_version"`
		Index             int                  `json:"index"`
		TimeMs            int64                `json:"time_ms"`
		Horizon           int                  `json:"horizon"`
		Feature           GeometryPoint        `json:"feature"`
		Distribution      GeometryDistribution `json:"distribution"`
		Available         bool                 `json:"available"`
		UnavailableReason string               `json:"unavailable_reason,omitempty"`
	}
	raw, _ := json.Marshal(hashInput{prediction.SchemaVersion, prediction.Index, prediction.TimeMs, prediction.Horizon, prediction.Feature, prediction.Distribution, prediction.Available, prediction.UnavailableReason})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// SelectGeometryModel compares lookback and one-to-three-region candidates on
// the same purged expanding walk-forward folds. The final selected model is
// fitted only after candidate comparison has finished.
func SelectGeometryModel(bars []quant.Bar, horizon int, config GeometryTrainingConfig) (GeometryTrainingResult, error) {
	if horizon != HorizonOneDay && horizon != HorizonTwentyDay {
		return GeometryTrainingResult{}, fmt.Errorf("unsupported geometry horizon")
	}
	if len(config.Lookbacks) == 0 || config.Folds < 2 || config.MinimumTrain < 1 {
		return GeometryTrainingResult{}, fmt.Errorf("invalid geometry training configuration")
	}
	type candidate struct {
		lookback, regions                 int
		loss, baselineLoss, standardError float64
	}
	allReports := make([]GeometryCandidateReport, 0)
	best := candidate{loss: math.Inf(1)}
	for _, lookback := range uniqueSorted(config.Lookbacks) {
		samples, err := BuildGeometrySamples(bars, lookback, horizon)
		if err != nil {
			return GeometryTrainingResult{}, err
		}
		if len(samples) == 0 {
			continue
		}
		folds, err := BuildPurgedFolds(len(samples), config.Folds, config.MinimumTrain, horizon)
		if err != nil {
			continue
		}
		for regions := 1; regions <= 3; regions++ {
			losses := make([]float64, 0, len(folds))
			baselineLosses := make([]float64, 0, len(folds))
			calibrationErrors := make([]float64, 0, len(folds))
			validationSamples := 0
			for _, fold := range folds {
				train := samples[fold.TrainStart : fold.TrainEnd+1]
				validation := samples[fold.ValidationStart : fold.ValidationEnd+1]
				model, trainErr := TrainGeometryModel(train, horizon, lookback, regions)
				if trainErr != nil {
					losses = nil
					break
				}
				loss, scoreErr := GeometryJointNLL(model, validation)
				if scoreErr != nil {
					losses = nil
					break
				}
				baselineModel, baselineErr := trainGeometryBaseline(train, horizon, lookback)
				if baselineErr != nil {
					losses = nil
					break
				}
				baselineLoss, baselineScoreErr := GeometryJointNLL(baselineModel, validation)
				if baselineScoreErr != nil {
					losses = nil
					break
				}
				losses = append(losses, loss)
				baselineLosses = append(baselineLosses, baselineLoss)
				calibrationErrors = append(calibrationErrors, geometryCalibrationError(model, validation))
				validationSamples += len(validation)
			}
			if len(losses) != len(folds) || len(baselineLosses) != len(folds) {
				continue
			}
			mean, meanBaseline, meanCalibration, variance := meanGeometry(losses), meanGeometry(baselineLosses), meanGeometry(calibrationErrors), 0.0
			for _, loss := range losses {
				variance += (loss - mean) * (loss - mean)
			}
			variance /= math.Max(1, float64(len(losses)-1))
			standardError := math.Sqrt(variance / math.Max(1, float64(len(losses))))
			report := GeometryCandidateReport{Lookback: lookback, Regions: regions, Report: GeometryValidationReport{Regions: regions, Samples: len(samples), ValidationSamples: validationSamples, JointNLL: mean, BaselineJointNLL: meanBaseline, AreaNLL: mean, SlopeNLL: mean, CalibrationError: meanCalibration, OutOfSample: true, BaselineGatePassed: mean < meanBaseline, Purge: horizon, WalkForwardVersion: WalkForwardVersion}}
			allReports = append(allReports, report)
			if mean < best.loss {
				best = candidate{lookback: lookback, regions: regions, loss: mean, baselineLoss: meanBaseline, standardError: standardError}
			}
		}
	}
	if math.IsInf(best.loss, 1) {
		return GeometryTrainingResult{}, fmt.Errorf("no geometry candidate had sufficient purged samples")
	}
	// Prefer the simplest candidate within one standard error of the best.
	for _, report := range allReports {
		if report.Report.JointNLL <= best.loss+best.standardError && (report.Regions < best.regions || (report.Regions == best.regions && report.Lookback < best.lookback)) {
			best.regions, best.lookback = report.Regions, report.Lookback
		}
	}
	finalSamples, err := BuildGeometrySamples(bars, best.lookback, horizon)
	if err != nil {
		return GeometryTrainingResult{}, err
	}
	model, err := TrainGeometryModel(finalSamples, horizon, best.lookback, best.regions)
	if err != nil {
		return GeometryTrainingResult{}, err
	}
	for _, candidateReport := range allReports {
		if candidateReport.Lookback == best.lookback && candidateReport.Regions == best.regions {
			model.Report = candidateReport.Report
			break
		}
	}
	return GeometryTrainingResult{SchemaVersion: GeometryModelSchemaVersion, Horizon: horizon, SelectedModel: model, Candidates: allReports}, nil
}

// TrainGeometryModel fits a deterministic conditional joint density on raw
// area and raw slope. Region weights are frozen after training and are never
// inferred by looking up similar historical windows.
func trainGeometryBaseline(samples []GeometrySample, horizon, lookback int) (GeometryModel, error) {
	if len(samples) < 2 {
		return GeometryModel{}, fmt.Errorf("insufficient geometry baseline samples")
	}
	areas, slopes := make([]float64, 0, len(samples)), make([]float64, 0, len(samples))
	for _, sample := range samples {
		if !sample.Feature.Available || !sample.Target.Available || sample.Target.CoverageArea < 0 {
			return GeometryModel{}, fmt.Errorf("geometry baseline samples contain unavailable or invalid rows")
		}
		areas = append(areas, sample.Target.CoverageArea)
		slopes = append(slopes, sample.Target.TrendSlope)
	}
	areaMean, slopeMean := meanGeometry(areas), meanGeometry(slopes)
	areaStd, slopeStd := geometryStdDev(areas, areaMean), geometryStdDev(slopes, slopeMean)
	if areaStd <= 0 || slopeStd <= 0 {
		return GeometryModel{}, fmt.Errorf("geometry baseline has degenerate variance")
	}
	areaResiduals, slopeResiduals := make([]float64, len(areas)), make([]float64, len(slopes))
	for index := range areas {
		areaResiduals[index], slopeResiduals[index] = areas[index]-areaMean, slopes[index]-slopeMean
	}
	return GeometryModel{SchemaVersion: GeometryModelSchemaVersion, Horizon: horizon, Lookback: lookback, Regions: []GeometryRegionModel{{Weight: 1, AreaCoefficients: [3]float64{areaMean, 0, 0}, SlopeCoefficients: [3]float64{slopeMean, 0, 0}, AreaStdDev: areaStd, SlopeStdDev: slopeStd, AreaSlopeCovariance: geometryCovariance(areaResiduals, slopeResiduals)}}}, nil
}

func geometryStdDev(values []float64, mean float64) float64 {
	if len(values) < 2 {
		return 0
	}
	variance := 0.0
	for _, value := range values {
		variance += (value - mean) * (value - mean)
	}
	return math.Sqrt(variance / float64(len(values)-1))
}

func geometryCovariance(left, right []float64) float64 {
	if len(left) != len(right) || len(left) < 2 {
		return 0
	}
	leftMean, rightMean := meanGeometry(left), meanGeometry(right)
	value := 0.0
	for index := range left {
		value += (left[index] - leftMean) * (right[index] - rightMean)
	}
	return value / float64(len(left)-1)
}

func TrainGeometryModel(samples []GeometrySample, horizon, lookback, regions int) (GeometryModel, error) {
	if regions < 1 || regions > 3 {
		return GeometryModel{}, fmt.Errorf("geometry regions must be 1, 2 or 3")
	}
	if len(samples) < geometryMaxInt(8, regions*4) {
		return GeometryModel{}, fmt.Errorf("insufficient geometry samples")
	}
	for _, sample := range samples {
		if !sample.Feature.Available || !sample.Target.Available || sample.Target.CoverageArea < 0 {
			return GeometryModel{}, fmt.Errorf("geometry samples contain unavailable or invalid rows")
		}
	}
	assignments := geometryClusters(samples, regions)
	models := make([]GeometryRegionModel, regions)
	for region := 0; region < regions; region++ {
		rows := make([]GeometrySample, 0)
		for index, sample := range samples {
			if assignments[index] == region {
				rows = append(rows, sample)
			}
		}
		if len(rows) == 0 {
			return GeometryModel{}, fmt.Errorf("empty geometry region")
		}
		area, err := fitGeometryResponse(rows, true)
		if err != nil {
			return GeometryModel{}, err
		}
		slope, err := fitGeometryResponse(rows, false)
		if err != nil {
			return GeometryModel{}, err
		}
		covariance := geometryResidualCovariance(rows, area, slope)
		models[region] = GeometryRegionModel{
			Weight: float64(len(rows)) / float64(len(samples)), AreaCoefficients: area,
			SlopeCoefficients: slope, AreaStdDev: covariance.areaStd,
			SlopeStdDev: covariance.slopeStd, AreaSlopeCovariance: covariance.cov,
		}
	}
	model := GeometryModel{SchemaVersion: GeometryModelSchemaVersion, Horizon: horizon, Lookback: lookback, Regions: models}
	report, err := validateGeometryModel(model, samples)
	if err != nil {
		return GeometryModel{}, err
	}
	model.Report = report
	return model, nil
}

func (model GeometryModel) Predict(feature GeometryPoint) (GeometryDistribution, error) {
	if model.SchemaVersion != GeometryModelSchemaVersion || !feature.Available || len(model.Regions) < 1 || len(model.Regions) > 3 {
		return GeometryDistribution{}, fmt.Errorf("invalid geometry model or feature")
	}
	outputs := make([]GeometryRegionOutput, 0, len(model.Regions))
	for _, region := range model.Regions {
		area := geometryLinear(region.AreaCoefficients, feature)
		slope := geometryLinear(region.SlopeCoefficients, feature)
		areaStd := math.Max(region.AreaStdDev, 1e-9)
		slopeStd := math.Max(region.SlopeStdDev, 1e-9)
		outputs = append(outputs, GeometryRegionOutput{
			Probability: region.Weight, AreaCenter: math.Max(0, area), SlopeCenter: slope,
			AreaLower: math.Max(0, area-2*areaStd), AreaUpper: math.Max(0, area+2*areaStd),
			SlopeLower: slope - 2*slopeStd, SlopeUpper: slope + 2*slopeStd,
			AreaSlopeCovariance: region.AreaSlopeCovariance,
		})
	}
	normalizeGeometryProbabilities(outputs)
	return GeometryDistribution{SchemaVersion: GeometryModelSchemaVersion, Regions: outputs}, nil
}

func GeometryJointNLL(model GeometryModel, samples []GeometrySample) (float64, error) {
	if len(samples) == 0 {
		return 0, fmt.Errorf("empty geometry validation set")
	}
	total := 0.0
	for _, sample := range samples {
		if _, err := model.Predict(sample.Feature); err != nil {
			return 0, err
		}
		density := 0.0
		for _, region := range model.Regions {
			density += region.Weight * geometryTruncatedBivariateDensity(sample.Target.CoverageArea, sample.Target.TrendSlope, region, sample.Feature)
		}
		if math.IsNaN(density) || math.IsInf(density, 0) {
			return 0, fmt.Errorf("invalid geometry density")
		}
		if density < geometryDensityFloor {
			density = geometryDensityFloor
		}
		total -= math.Log(density)
	}
	return total / float64(len(samples)), nil
}

func validateGeometryModel(model GeometryModel, samples []GeometrySample) (GeometryValidationReport, error) {
	nll, err := GeometryJointNLL(model, samples)
	if err != nil {
		return GeometryValidationReport{}, err
	}
	areaNLL, slopeNLL := 0.0, 0.0
	for _, sample := range samples {
		areaDensity, slopeDensity := geometryAreaDensity(model, sample.Target.CoverageArea, sample.Feature), geometrySlopeDensity(model, sample.Target.TrendSlope, sample.Feature)
		if math.IsNaN(areaDensity) || math.IsInf(areaDensity, 0) || math.IsNaN(slopeDensity) || math.IsInf(slopeDensity, 0) {
			return GeometryValidationReport{}, fmt.Errorf("invalid geometry marginal density")
		}
		areaDensity = math.Max(areaDensity, geometryDensityFloor)
		slopeDensity = math.Max(slopeDensity, geometryDensityFloor)
		areaNLL -= math.Log(areaDensity)
		slopeNLL -= math.Log(slopeDensity)
	}
	calibration := geometryCalibrationError(model, samples)
	count := float64(len(samples))
	return GeometryValidationReport{Regions: len(model.Regions), Samples: len(samples), JointNLL: nll, AreaNLL: areaNLL / count, SlopeNLL: slopeNLL / count, CalibrationError: calibration, Purge: model.Horizon, WalkForwardVersion: WalkForwardVersion}, nil
}

func geometryAreaDensity(model GeometryModel, value float64, feature GeometryPoint) float64 {
	total := 0.0
	for _, region := range model.Regions {
		total += region.Weight * geometryTruncatedNormalDensity(value, geometryLinear(region.AreaCoefficients, feature), math.Max(region.AreaStdDev, 1e-9))
	}
	return total
}
func geometrySlopeDensity(model GeometryModel, value float64, feature GeometryPoint) float64 {
	total := 0.0
	for _, region := range model.Regions {
		total += region.Weight * normalDensity(value, geometryLinear(region.SlopeCoefficients, feature), math.Max(region.SlopeStdDev, 1e-9))
	}
	return total
}
func geometryTruncatedNormalDensity(value, mean, std float64) float64 {
	if value < 0 {
		return 0
	}
	normalization := normalCDF(mean / std)
	return normalDensity(value, mean, std) / math.Max(normalization, 1e-12)
}
func normalDensity(value, mean, std float64) float64 {
	z := (value - mean) / std
	return math.Exp(-0.5*z*z) / (std * math.Sqrt(2*math.Pi))
}
func geometryAreaCDF(value, mean, std float64) float64 {
	if value < 0 {
		return 0
	}
	denominator := math.Max(normalCDF(mean/std), 1e-12)
	return math.Max(0, math.Min(1, (normalCDF((value-mean)/std)-normalCDF(-mean/std))/denominator))
}
func geometrySlopeCDF(value, mean, std float64) float64 { return normalCDF((value - mean) / std) }
func geometryCalibrationError(model GeometryModel, samples []GeometrySample) float64 {
	const bins = 10
	areaCounts, slopeCounts := make([]int, bins), make([]int, bins)
	for _, sample := range samples {
		areaP, slopeP := 0.0, 0.0
		for _, region := range model.Regions {
			areaP += region.Weight * geometryAreaCDF(sample.Target.CoverageArea, geometryLinear(region.AreaCoefficients, sample.Feature), math.Max(region.AreaStdDev, 1e-9))
			slopeP += region.Weight * geometrySlopeCDF(sample.Target.TrendSlope, geometryLinear(region.SlopeCoefficients, sample.Feature), math.Max(region.SlopeStdDev, 1e-9))
		}
		ai := int(math.Min(bins-1, math.Floor(areaP*bins)))
		si := int(math.Min(bins-1, math.Floor(slopeP*bins)))
		areaCounts[ai]++
		slopeCounts[si]++
	}
	errorValue := 0.0
	count := float64(len(samples))
	for _, value := range areaCounts {
		errorValue += math.Abs(float64(value)/count - 1/float64(bins))
	}
	for _, value := range slopeCounts {
		errorValue += math.Abs(float64(value)/count - 1/float64(bins))
	}
	return errorValue / (2 * bins)
}

func fitGeometryResponse(samples []GeometrySample, area bool) ([3]float64, error) {
	var result [3]float64
	var normal [3][3]float64
	var target [3]float64
	for _, sample := range samples {
		x := geometryInput(sample.Feature)
		y := sample.Target.TrendSlope
		if area {
			y = sample.Target.CoverageArea
		}
		for row := 0; row < 3; row++ {
			for column := 0; column < 3; column++ {
				normal[row][column] += x[row] * x[column]
			}
			target[row] += x[row] * y
		}
	}
	for index := 0; index < 3; index++ {
		normal[index][index] += 1e-9
	}
	result, ok := solve3(normal, target)
	if !ok {
		return result, fmt.Errorf("geometry regression is singular")
	}
	return result, nil
}

func geometryClusters(samples []GeometrySample, regions int) []int {
	assignments := make([]int, len(samples))
	centers := make([][2]float64, regions)
	sorted := append([]GeometrySample(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Target.CoverageArea < sorted[j].Target.CoverageArea })
	for index := range centers {
		centers[index] = [2]float64{sorted[(2*index+1)*len(sorted)/(2*regions)].Target.CoverageArea, sorted[(2*index+1)*len(sorted)/(2*regions)].Target.TrendSlope}
	}
	for iteration := 0; iteration < 20; iteration++ {
		counts := make([]int, regions)
		sums := make([][2]float64, regions)
		for index, sample := range samples {
			value := [2]float64{sample.Target.CoverageArea, sample.Target.TrendSlope}
			best := 0
			for candidate := 1; candidate < regions; candidate++ {
				if geometryDistance(value, centers[candidate]) < geometryDistance(value, centers[best]) {
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
	return assignments
}

func geometryResidualCovariance(samples []GeometrySample, area, slope [3]float64) (result struct{ areaStd, slopeStd, cov float64 }) {
	var areaValues, slopeValues []float64
	for _, sample := range samples {
		a := sample.Target.CoverageArea - geometryLinear(area, sample.Feature)
		s := sample.Target.TrendSlope - geometryLinear(slope, sample.Feature)
		areaValues = append(areaValues, a)
		slopeValues = append(slopeValues, s)
	}
	meanA, meanS := meanGeometry(areaValues), meanGeometry(slopeValues)
	denominator := math.Max(1, float64(len(samples)-1))
	va, vs, cov := 0.0, 0.0, 0.0
	for index := range areaValues {
		da, ds := areaValues[index]-meanA, slopeValues[index]-meanS
		va += da * da
		vs += ds * ds
		cov += da * ds
	}
	result.areaStd, result.slopeStd, result.cov = math.Sqrt(math.Max(va/denominator, 1e-9)), math.Sqrt(math.Max(vs/denominator, 1e-9)), cov/denominator
	limit := 0.999 * result.areaStd * result.slopeStd
	result.cov = math.Max(-limit, math.Min(limit, result.cov))
	return result
}

func geometryTruncatedBivariateDensity(area, slope float64, region GeometryRegionModel, feature GeometryPoint) float64 {
	if area < 0 {
		return 0
	}
	meanA, meanS := geometryLinear(region.AreaCoefficients, feature), geometryLinear(region.SlopeCoefficients, feature)
	sa, ss := math.Max(region.AreaStdDev, 1e-9), math.Max(region.SlopeStdDev, 1e-9)
	covariance := math.Max(-0.999*sa*ss, math.Min(0.999*sa*ss, region.AreaSlopeCovariance))
	determinant := sa*sa*ss*ss - covariance*covariance
	if determinant <= 0 {
		return 0
	}
	da, ds := area-meanA, slope-meanS
	quadratic := (ss*ss*da*da - 2*covariance*da*ds + sa*sa*ds*ds) / determinant
	base := math.Exp(-quadratic/2) / (2 * math.Pi * math.Sqrt(determinant))
	normalization := normalCDF(meanA / sa)
	return base / math.Max(normalization, 1e-12)
}

func geometryLinear(coeff [3]float64, feature GeometryPoint) float64 {
	return coeff[0] + coeff[1]*feature.CoverageArea + coeff[2]*feature.TrendSlope
}
func geometryInput(feature GeometryPoint) [3]float64 {
	return [3]float64{1, feature.CoverageArea, feature.TrendSlope}
}
func geometryDistance(left, right [2]float64) float64 {
	a, b := left[0]-right[0], left[1]-right[1]
	return a*a + b*b
}
func meanGeometry(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / math.Max(1, float64(len(values)))
}
func normalizeGeometryProbabilities(values []GeometryRegionOutput) {
	total := 0.0
	for _, value := range values {
		total += value.Probability
	}
	if total <= 0 {
		return
	}
	for index := range values {
		values[index].Probability /= total
	}
}
func normalCDF(value float64) float64 { return 0.5 * (1 + math.Erf(value/math.Sqrt2)) }
func geometryMaxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func solve3(matrix [3][3]float64, values [3]float64) ([3]float64, bool) {
	for pivot := 0; pivot < 3; pivot++ {
		best := pivot
		for row := pivot + 1; row < 3; row++ {
			if math.Abs(matrix[row][pivot]) > math.Abs(matrix[best][pivot]) {
				best = row
			}
		}
		if math.Abs(matrix[best][pivot]) < 1e-12 {
			return [3]float64{}, false
		}
		matrix[pivot], matrix[best] = matrix[best], matrix[pivot]
		values[pivot], values[best] = values[best], values[pivot]
		divisor := matrix[pivot][pivot]
		for column := pivot; column < 3; column++ {
			matrix[pivot][column] /= divisor
		}
		values[pivot] /= divisor
		for row := 0; row < 3; row++ {
			if row == pivot {
				continue
			}
			factor := matrix[row][pivot]
			for column := pivot; column < 3; column++ {
				matrix[row][column] -= factor * matrix[pivot][column]
			}
			values[row] -= factor * values[pivot]
		}
	}
	return values, true
}
