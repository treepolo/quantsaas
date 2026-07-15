package parameterresearch

import (
	"errors"
	"math"
	"sort"
)

var ErrSurrogateIneligible = errors.New("代理模型至少需要兩個完整全域批次且累積達 2*N0 個實測 Sobol 點")

type SurrogateExample struct {
	Coordinates      []int   `json:"coordinates"`
	Batch            string  `json:"batch"`
	LogFinalNAVRatio float64 `json:"log_final_nav_ratio"`
	LogDrawdownRatio float64 `json:"log_drawdown_residual_ratio"`
}

type ForestSettings struct {
	Trees             int    `json:"trees"`
	MaximumDepth      int    `json:"maximum_depth"`
	MinimumLeaf       int    `json:"minimum_leaf"`
	FeatureSampleMode string `json:"feature_sample_mode"`
	Bootstrap         bool   `json:"bootstrap"`
	Seed              uint64 `json:"seed"`
	Version           string `json:"version"`
}

func DefaultForestSettings(seed uint64) ForestSettings {
	return ForestSettings{Trees: 64, MaximumDepth: 8, MinimumLeaf: 3, FeatureSampleMode: "sqrt", Bootstrap: true, Seed: seed, Version: SurrogateVersion}
}

type RegressionTree struct {
	Feature   int             `json:"feature,omitempty"`
	Threshold float64         `json:"threshold,omitempty"`
	Value     float64         `json:"value"`
	Left      *RegressionTree `json:"left,omitempty"`
	Right     *RegressionTree `json:"right,omitempty"`
}

type RandomForest struct {
	Settings     ForestSettings   `json:"settings"`
	FeatureCount int              `json:"feature_count"`
	Trees        []RegressionTree `json:"trees"`
}

type TargetValidation struct {
	MAE                 float64 `json:"mae"`
	RMSE                float64 `json:"rmse"`
	MedianAbsoluteError float64 `json:"median_absolute_error"`
	P90AbsoluteError    float64 `json:"p90_absolute_error"`
	RankCorrelation     float64 `json:"rank_correlation"`
	BaselineMAE         float64 `json:"baseline_mae"`
	ResidualP90         float64 `json:"residual_p90"`
	IntervalCoverage    float64 `json:"interval_coverage"`
	CanGuide            bool    `json:"can_guide"`
}

type SurrogateArtifact struct {
	SchemaVersion string           `json:"schema_version"`
	Settings      ForestSettings   `json:"settings"`
	BatchNames    []string         `json:"batch_names"`
	ReturnModel   RandomForest     `json:"return_model"`
	DrawdownModel RandomForest     `json:"drawdown_model"`
	ReturnOOF     TargetValidation `json:"return_oof"`
	DrawdownOOF   TargetValidation `json:"drawdown_oof"`
}

func TrainSurrogate(examples []SurrogateExample, n0 int, settings ForestSettings) (SurrogateArtifact, error) {
	if len(examples) < 2*n0 || n0 < 1 {
		return SurrogateArtifact{}, ErrSurrogateIneligible
	}
	batchSet := map[string]bool{}
	featureCount := 0
	for _, example := range examples {
		if example.Batch == "" || len(example.Coordinates) == 0 || !finiteFloat(example.LogFinalNAVRatio) || !finiteFloat(example.LogDrawdownRatio) {
			return SurrogateArtifact{}, ErrInvalidPlan
		}
		batchSet[example.Batch] = true
		if featureCount == 0 {
			featureCount = len(example.Coordinates)
		}
		if len(example.Coordinates) != featureCount {
			return SurrogateArtifact{}, ErrInvalidPlan
		}
	}
	if len(batchSet) < 2 {
		return SurrogateArtifact{}, ErrSurrogateIneligible
	}
	batches := make([]string, 0, len(batchSet))
	for batch := range batchSet {
		batches = append(batches, batch)
	}
	sort.Strings(batches)
	if settings.Trees < 8 || settings.MaximumDepth < 1 || settings.MinimumLeaf < 1 || settings.Version != SurrogateVersion {
		return SurrogateArtifact{}, ErrInvalidPlan
	}
	returnPredictions, returnActual, returnBaseline := []float64{}, []float64{}, []float64{}
	drawdownPredictions, drawdownActual, drawdownBaseline := []float64{}, []float64{}, []float64{}
	for fold, heldOut := range batches {
		train, validate := splitBatch(examples, heldOut)
		if len(train) < settings.MinimumLeaf*2 || len(validate) == 0 {
			return SurrogateArtifact{}, ErrSurrogateIneligible
		}
		returnForest := trainForest(train, func(e SurrogateExample) float64 { return e.LogFinalNAVRatio }, withSeed(settings, settings.Seed+uint64(fold*2+1)))
		drawdownForest := trainForest(train, func(e SurrogateExample) float64 { return e.LogDrawdownRatio }, withSeed(settings, settings.Seed+uint64(fold*2+2)))
		returnMedian := medianTargets(train, func(e SurrogateExample) float64 { return e.LogFinalNAVRatio })
		drawdownMedian := medianTargets(train, func(e SurrogateExample) float64 { return e.LogDrawdownRatio })
		for _, example := range validate {
			prediction, _ := returnForest.Predict(example.Coordinates)
			returnPredictions = append(returnPredictions, prediction)
			returnActual = append(returnActual, example.LogFinalNAVRatio)
			returnBaseline = append(returnBaseline, returnMedian)
			prediction, _ = drawdownForest.Predict(example.Coordinates)
			drawdownPredictions = append(drawdownPredictions, prediction)
			drawdownActual = append(drawdownActual, example.LogDrawdownRatio)
			drawdownBaseline = append(drawdownBaseline, drawdownMedian)
		}
	}
	returnValidation := validatePredictions(returnPredictions, returnActual, returnBaseline)
	drawdownValidation := validatePredictions(drawdownPredictions, drawdownActual, drawdownBaseline)
	returnModel := trainForest(examples, func(e SurrogateExample) float64 { return e.LogFinalNAVRatio }, withSeed(settings, settings.Seed+1000001))
	drawdownModel := trainForest(examples, func(e SurrogateExample) float64 { return e.LogDrawdownRatio }, withSeed(settings, settings.Seed+2000003))
	return SurrogateArtifact{SchemaVersion: SurrogateVersion, Settings: settings, BatchNames: batches, ReturnModel: returnModel, DrawdownModel: drawdownModel, ReturnOOF: returnValidation, DrawdownOOF: drawdownValidation}, nil
}

func (forest RandomForest) Predict(coordinates []int) (float64, float64) {
	if len(coordinates) != forest.FeatureCount || len(forest.Trees) == 0 {
		return math.NaN(), math.NaN()
	}
	values := make([]float64, len(forest.Trees))
	mean := 0.0
	for i := range forest.Trees {
		values[i] = forest.Trees[i].predict(coordinates)
		mean += values[i]
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, value := range values {
		difference := value - mean
		variance += difference * difference
	}
	return mean, math.Sqrt(variance / float64(len(values)))
}

func (tree RegressionTree) predict(coordinates []int) float64 {
	current := &tree
	for current.Left != nil && current.Right != nil {
		if float64(coordinates[current.Feature]) <= current.Threshold {
			current = current.Left
		} else {
			current = current.Right
		}
	}
	return current.Value
}

type trainingRow struct {
	x []int
	y float64
}

func trainForest(examples []SurrogateExample, target func(SurrogateExample) float64, settings ForestSettings) RandomForest {
	rng := newXorShift(settings.Seed)
	rows := make([]trainingRow, len(examples))
	for i, example := range examples {
		rows[i] = trainingRow{x: append([]int(nil), example.Coordinates...), y: target(example)}
	}
	forest := RandomForest{Settings: settings, FeatureCount: len(rows[0].x), Trees: make([]RegressionTree, settings.Trees)}
	for treeIndex := range forest.Trees {
		sample := rows
		if settings.Bootstrap {
			sample = make([]trainingRow, len(rows))
			for i := range sample {
				sample[i] = rows[rng.intn(len(rows))]
			}
		}
		forest.Trees[treeIndex] = buildTree(sample, 0, settings, rng)
	}
	return forest
}

func buildTree(rows []trainingRow, depth int, settings ForestSettings, rng *xorShift) RegressionTree {
	value := meanRows(rows)
	node := RegressionTree{Value: value}
	if depth >= settings.MaximumDepth || len(rows) < settings.MinimumLeaf*2 || rowVariance(rows, value) < 1e-15 {
		return node
	}
	featureCount := len(rows[0].x)
	candidateCount := int(math.Sqrt(float64(featureCount)))
	if candidateCount < 1 {
		candidateCount = 1
	}
	features := rng.permutation(featureCount)[:candidateCount]
	bestLoss := math.Inf(1)
	bestFeature, bestThreshold := -1, 0.0
	for _, feature := range features {
		values := make([]int, 0, len(rows))
		seen := map[int]bool{}
		for _, row := range rows {
			if !seen[row.x[feature]] {
				seen[row.x[feature]] = true
				values = append(values, row.x[feature])
			}
		}
		sort.Ints(values)
		for i := 0; i+1 < len(values); i++ {
			threshold := (float64(values[i]) + float64(values[i+1])) / 2
			left, right := splitRows(rows, feature, threshold)
			if len(left) < settings.MinimumLeaf || len(right) < settings.MinimumLeaf {
				continue
			}
			loss := sumSquared(left) + sumSquared(right)
			if loss < bestLoss-1e-15 || (math.Abs(loss-bestLoss) <= 1e-15 && (feature < bestFeature || bestFeature < 0)) {
				bestLoss, bestFeature, bestThreshold = loss, feature, threshold
			}
		}
	}
	if bestFeature < 0 {
		return node
	}
	left, right := splitRows(rows, bestFeature, bestThreshold)
	node.Feature, node.Threshold = bestFeature, bestThreshold
	leftNode, rightNode := buildTree(left, depth+1, settings, rng), buildTree(right, depth+1, settings, rng)
	node.Left, node.Right = &leftNode, &rightNode
	return node
}

type ProposalPrediction struct {
	Coordinates        []int   `json:"coordinates"`
	ReturnMean         float64 `json:"return_mean"`
	ReturnDispersion   float64 `json:"return_dispersion"`
	ReturnLower        float64 `json:"return_lower"`
	ReturnUpper        float64 `json:"return_upper"`
	DrawdownMean       float64 `json:"drawdown_mean"`
	DrawdownDispersion float64 `json:"drawdown_dispersion"`
	DrawdownLower      float64 `json:"drawdown_lower"`
	DrawdownUpper      float64 `json:"drawdown_upper"`
}

func ScoreProposalPool(artifact SurrogateArtifact, pool [][]int) []ProposalPrediction {
	result := make([]ProposalPrediction, 0, len(pool))
	for _, coordinate := range pool {
		returnMean, returnDispersion := artifact.ReturnModel.Predict(coordinate)
		drawdownMean, drawdownDispersion := artifact.DrawdownModel.Predict(coordinate)
		result = append(result, ProposalPrediction{Coordinates: append([]int(nil), coordinate...), ReturnMean: returnMean, ReturnDispersion: returnDispersion, ReturnLower: returnMean - artifact.ReturnOOF.ResidualP90, ReturnUpper: returnMean + artifact.ReturnOOF.ResidualP90, DrawdownMean: drawdownMean, DrawdownDispersion: drawdownDispersion, DrawdownLower: drawdownMean - artifact.DrawdownOOF.ResidualP90, DrawdownUpper: drawdownMean + artifact.DrawdownOOF.ResidualP90})
	}
	return result
}

func SelectProposals(kind string, scored []ProposalPrediction, count int, artifact SurrogateArtifact) ([]ProposalPrediction, error) {
	if count < 1 || len(scored) == 0 {
		return nil, ErrInvalidPlan
	}
	selected := append([]ProposalPrediction(nil), scored...)
	switch kind {
	case "conservative_performance":
		if !artifact.ReturnOOF.CanGuide || !artifact.DrawdownOOF.CanGuide {
			return nil, errors.New("兩個代理目標都通過 OOF 基準前，不能產生保守績效提案")
		}
		selected = paretoProposal(selected, func(p ProposalPrediction) (float64, float64) { return p.ReturnLower, p.DrawdownLower })
		sort.SliceStable(selected, func(i, j int) bool {
			if selected[i].ReturnLower == selected[j].ReturnLower {
				return selected[i].DrawdownLower > selected[j].DrawdownLower
			}
			return selected[i].ReturnLower > selected[j].ReturnLower
		})
	case "qualification_boundary":
		filtered := selected[:0]
		for _, point := range selected {
			if (point.ReturnLower <= 0 && point.ReturnUpper >= 0) || (point.DrawdownLower <= 0 && point.DrawdownUpper >= 0) {
				filtered = append(filtered, point)
			}
		}
		selected = filtered
		sort.SliceStable(selected, func(i, j int) bool { return boundaryDistance(selected[i]) < boundaryDistance(selected[j]) })
	case "model_uncertainty":
		selected = paretoProposal(selected, func(p ProposalPrediction) (float64, float64) { return p.ReturnDispersion, p.DrawdownDispersion })
		sort.SliceStable(selected, func(i, j int) bool {
			if selected[i].ReturnDispersion == selected[j].ReturnDispersion {
				return selected[i].DrawdownDispersion > selected[j].DrawdownDispersion
			}
			return selected[i].ReturnDispersion > selected[j].ReturnDispersion
		})
	case "pure_coverage":
		// Input order is the continued Sobol sequence and must remain untouched.
	default:
		return nil, ErrInvalidPlan
	}
	if len(selected) > count {
		selected = selected[:count]
	}
	return selected, nil
}

func splitBatch(examples []SurrogateExample, heldOut string) ([]SurrogateExample, []SurrogateExample) {
	train, validate := []SurrogateExample{}, []SurrogateExample{}
	for _, example := range examples {
		if example.Batch == heldOut {
			validate = append(validate, example)
		} else {
			train = append(train, example)
		}
	}
	return train, validate
}

func validatePredictions(predicted, actual, baseline []float64) TargetValidation {
	errors, baselineErrors := make([]float64, len(actual)), make([]float64, len(actual))
	squared, covered := 0.0, 0
	for i := range actual {
		errors[i] = math.Abs(predicted[i] - actual[i])
		baselineErrors[i] = math.Abs(baseline[i] - actual[i])
		squared += (predicted[i] - actual[i]) * (predicted[i] - actual[i])
	}
	sort.Float64s(errors)
	p90 := percentile(errors, 0.9)
	for i := range actual {
		if math.Abs(predicted[i]-actual[i]) <= p90 {
			covered++
		}
	}
	mae, baselineMAE := meanSlice(errors), meanSlice(baselineErrors)
	return TargetValidation{MAE: mae, RMSE: math.Sqrt(squared / float64(len(actual))), MedianAbsoluteError: percentile(errors, 0.5), P90AbsoluteError: p90, RankCorrelation: spearman(predicted, actual), BaselineMAE: baselineMAE, ResidualP90: p90, IntervalCoverage: float64(covered) / float64(len(actual)), CanGuide: mae < baselineMAE}
}

func paretoProposal(points []ProposalPrediction, objectives func(ProposalPrediction) (float64, float64)) []ProposalPrediction {
	result := make([]ProposalPrediction, 0, len(points))
	for i, point := range points {
		a1, a2 := objectives(point)
		dominated := false
		for j, other := range points {
			if i == j {
				continue
			}
			b1, b2 := objectives(other)
			if b1 >= a1 && b2 >= a2 && (b1 > a1 || b2 > a2) {
				dominated = true
				break
			}
		}
		if !dominated {
			result = append(result, point)
		}
	}
	return result
}

func boundaryDistance(point ProposalPrediction) float64 {
	return math.Min(math.Abs(point.ReturnMean), math.Abs(point.DrawdownMean))
}

func splitRows(rows []trainingRow, feature int, threshold float64) ([]trainingRow, []trainingRow) {
	left, right := []trainingRow{}, []trainingRow{}
	for _, row := range rows {
		if float64(row.x[feature]) <= threshold {
			left = append(left, row)
		} else {
			right = append(right, row)
		}
	}
	return left, right
}

func meanRows(rows []trainingRow) float64 {
	sum := 0.0
	for _, row := range rows {
		sum += row.y
	}
	return sum / float64(len(rows))
}
func rowVariance(rows []trainingRow, mean float64) float64 {
	value := 0.0
	for _, row := range rows {
		d := row.y - mean
		value += d * d
	}
	return value / float64(len(rows))
}
func sumSquared(rows []trainingRow) float64 {
	mean := meanRows(rows)
	total := 0.0
	for _, row := range rows {
		d := row.y - mean
		total += d * d
	}
	return total
}
func medianTargets(rows []SurrogateExample, target func(SurrogateExample) float64) float64 {
	values := make([]float64, len(rows))
	for i, row := range rows {
		values[i] = target(row)
	}
	sort.Float64s(values)
	return percentile(values, .5)
}
func meanSlice(values []float64) float64 {
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}
func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	position := q * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}
func finiteFloat(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func withSeed(settings ForestSettings, seed uint64) ForestSettings {
	settings.Seed = seed
	return settings
}

func spearman(left, right []float64) float64 {
	if len(left) != len(right) || len(left) < 2 {
		return 0
	}
	a, b := ranks(left), ranks(right)
	ma, mb := meanSlice(a), meanSlice(b)
	numerator, da, db := 0.0, 0.0, 0.0
	for i := range a {
		x, y := a[i]-ma, b[i]-mb
		numerator += x * y
		da += x * x
		db += y * y
	}
	if da == 0 || db == 0 {
		return 0
	}
	return numerator / math.Sqrt(da*db)
}
func ranks(values []float64) []float64 {
	indices := make([]int, len(values))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool { return values[indices[i]] < values[indices[j]] })
	result := make([]float64, len(values))
	for start := 0; start < len(indices); {
		end := start + 1
		for end < len(indices) && values[indices[end]] == values[indices[start]] {
			end++
		}
		rank := (float64(start)+float64(end-1))/2 + 1
		for i := start; i < end; i++ {
			result[indices[i]] = rank
		}
		start = end
	}
	return result
}

type xorShift struct{ state uint64 }

func newXorShift(seed uint64) *xorShift {
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return &xorShift{state: seed}
}
func (r *xorShift) next() uint64 {
	x := r.state
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	r.state = x
	return x
}
func (r *xorShift) intn(n int) int { return int(r.next() % uint64(n)) }
func (r *xorShift) permutation(n int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	for i := n - 1; i > 0; i-- {
		j := r.intn(i + 1)
		p[i], p[j] = p[j], p[i]
	}
	return p
}
