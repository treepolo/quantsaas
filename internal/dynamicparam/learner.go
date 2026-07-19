package dynamicparam

import (
	"context"
	"fmt"
	"math"
)

const (
	LossMSE     = "mse"
	LossBinary  = "binary_cross_entropy"
	LossSoftmax = "softmax_cross_entropy"
)

type SupervisedExample struct {
	Feature FeaturePoint
	Target  []float64
}

type GAMConfig struct {
	Interactions bool    `json:"interactions"`
	L1Penalty    float64 `json:"l1_penalty"`
	L2Penalty    float64 `json:"l2_penalty"`
	Epochs       int     `json:"epochs"`
	LearningRate float64 `json:"learning_rate"`
}

type GAMModel struct {
	SchemaVersion string      `json:"schema_version"`
	Loss          string      `json:"loss"`
	Outputs       int         `json:"outputs"`
	Config        GAMConfig   `json:"config"`
	BasisNames    []string    `json:"basis_names"`
	Weights       [][]float64 `json:"weights"`
}

func TrainGAM(examples []SupervisedExample, outputs int, loss string, config GAMConfig) (GAMModel, error) {
	return TrainGAMContext(context.Background(), examples, outputs, loss, config)
}

func TrainGAMContext(ctx context.Context, examples []SupervisedExample, outputs int, loss string, config GAMConfig) (GAMModel, error) {
	if err := ctx.Err(); err != nil {
		return GAMModel{}, err
	}
	if err := validateExamples(examples, outputs, loss); err != nil {
		return GAMModel{}, err
	}
	if config.Epochs <= 0 {
		config.Epochs = 250
	}
	if config.LearningRate <= 0 {
		config.LearningRate = 0.02
	}
	model := GAMModel{SchemaVersion: "p09-explainable-gam-v1", Loss: loss, Outputs: outputs, Config: config}
	first, names, err := gamBasis(examples[0].Feature, config.Interactions)
	if err != nil {
		return GAMModel{}, err
	}
	model.BasisNames = names
	model.Weights = makeMatrix(outputs, len(first))
	for epoch := 0; epoch < config.Epochs; epoch++ {
		if err := ctx.Err(); err != nil {
			return GAMModel{}, err
		}
		for _, example := range examples {
			if err := ctx.Err(); err != nil {
				return GAMModel{}, err
			}
			basis, _, basisErr := gamBasis(example.Feature, config.Interactions)
			if basisErr != nil {
				return GAMModel{}, basisErr
			}
			raw := matrixVector(model.Weights, basis)
			prediction := activate(raw, loss)
			gradient := outputGradient(prediction, example.Target, loss)
			for output := range model.Weights {
				for index := range model.Weights[output] {
					regularization := config.L2Penalty * model.Weights[output][index]
					model.Weights[output][index] -= config.LearningRate * (gradient[output]*basis[index] + regularization)
					if index > 0 && config.L1Penalty > 0 {
						model.Weights[output][index] = softThreshold(model.Weights[output][index], config.LearningRate*config.L1Penalty)
					}
				}
			}
		}
	}
	return model, nil
}

func (model GAMModel) Predict(feature FeaturePoint) ([]float64, error) {
	if model.SchemaVersion != "p09-explainable-gam-v1" || len(model.Weights) != model.Outputs {
		return nil, fmt.Errorf("invalid GAM model")
	}
	basis, names, err := gamBasis(feature, model.Config.Interactions)
	if err != nil {
		return nil, err
	}
	if len(names) != len(model.BasisNames) || len(basis) != len(model.Weights[0]) {
		return nil, fmt.Errorf("GAM feature schema mismatch")
	}
	return activate(matrixVector(model.Weights, basis), model.Loss), nil
}

type TCNConfig struct {
	Hidden       int     `json:"hidden"`
	KernelSize   int     `json:"kernel_size"`
	Dilations    []int   `json:"dilations"`
	Epochs       int     `json:"epochs"`
	LearningRate float64 `json:"learning_rate"`
	L2Penalty    float64 `json:"l2_penalty"`
}

type TCNLayer struct {
	Dilation int           `json:"dilation"`
	Weights  [][][]float64 `json:"weights"`
	Bias     []float64     `json:"bias"`
}

type TCNModel struct {
	SchemaVersion string      `json:"schema_version"`
	Loss          string      `json:"loss"`
	Outputs       int         `json:"outputs"`
	Config        TCNConfig   `json:"config"`
	Layers        []TCNLayer  `json:"layers"`
	Head          [][]float64 `json:"head"`
	HeadBias      []float64   `json:"head_bias"`
}

func TrainTCN(examples []SupervisedExample, outputs int, loss string, config TCNConfig) (TCNModel, error) {
	return TrainTCNContext(context.Background(), examples, outputs, loss, config)
}

func TrainTCNContext(ctx context.Context, examples []SupervisedExample, outputs int, loss string, config TCNConfig) (TCNModel, error) {
	if err := ctx.Err(); err != nil {
		return TCNModel{}, err
	}
	if err := validateExamples(examples, outputs, loss); err != nil {
		return TCNModel{}, err
	}
	if config.Hidden <= 0 {
		config.Hidden = 4
	}
	if config.KernelSize == 0 {
		config.KernelSize = 2
	}
	if config.KernelSize != 2 {
		return TCNModel{}, fmt.Errorf("P09 TCN currently requires kernel size 2")
	}
	if len(config.Dilations) == 0 {
		config.Dilations = []int{1, 2, 4, 8}
	}
	for index, dilation := range config.Dilations {
		if dilation != 1<<index {
			return TCNModel{}, fmt.Errorf("TCN dilations must be 1,2,4,8,...")
		}
	}
	if config.Epochs <= 0 {
		config.Epochs = 120
	}
	if config.LearningRate <= 0 {
		config.LearningRate = 0.01
	}
	model := newTCNModel(outputs, loss, config)
	for epoch := 0; epoch < config.Epochs; epoch++ {
		if err := ctx.Err(); err != nil {
			return TCNModel{}, err
		}
		for _, example := range examples {
			if err := ctx.Err(); err != nil {
				return TCNModel{}, err
			}
			if err := model.trainExample(example); err != nil {
				return TCNModel{}, err
			}
		}
	}
	return model, nil
}

func (model TCNModel) Predict(feature FeaturePoint) ([]float64, error) {
	if model.SchemaVersion != "p09-causal-tcn-v1" || len(feature.RawSequence) == 0 {
		return nil, fmt.Errorf("invalid TCN model or causal sequence")
	}
	_, raw, err := model.forward(feature.RawSequence)
	if err != nil {
		return nil, err
	}
	return activate(raw, model.Loss), nil
}

func newTCNModel(outputs int, loss string, config TCNConfig) TCNModel {
	model := TCNModel{SchemaVersion: "p09-causal-tcn-v1", Loss: loss, Outputs: outputs, Config: config}
	inputChannels := 4
	seedIndex := 1
	for _, dilation := range config.Dilations {
		layer := TCNLayer{Dilation: dilation, Weights: make([][][]float64, config.Hidden), Bias: make([]float64, config.Hidden)}
		for output := 0; output < config.Hidden; output++ {
			layer.Weights[output] = make([][]float64, inputChannels)
			for input := 0; input < inputChannels; input++ {
				layer.Weights[output][input] = []float64{deterministicWeight(seedIndex), deterministicWeight(seedIndex + 1)}
				seedIndex += 2
			}
		}
		model.Layers = append(model.Layers, layer)
		inputChannels = config.Hidden
	}
	model.Head = makeMatrix(outputs, config.Hidden)
	model.HeadBias = make([]float64, outputs)
	for output := range model.Head {
		for hidden := range model.Head[output] {
			model.Head[output][hidden] = deterministicWeight(seedIndex)
			seedIndex++
		}
	}
	return model
}

func (model *TCNModel) trainExample(example SupervisedExample) error {
	activations, raw, err := model.forward(example.Feature.RawSequence)
	if err != nil {
		return err
	}
	prediction := activate(raw, model.Loss)
	gradient := outputGradient(prediction, example.Target, model.Loss)
	last := activations[len(activations)-1]
	lastIndex := len(last) - 1
	deltas := make([][][]float64, len(model.Layers))
	for layerIndex, activation := range activations[1:] {
		deltas[layerIndex] = makeMatrix(len(activation), len(activation[0]))
	}
	for hidden := 0; hidden < model.Config.Hidden; hidden++ {
		for output := 0; output < model.Outputs; output++ {
			deltas[len(deltas)-1][lastIndex][hidden] += gradient[output] * model.Head[output][hidden]
			headGradient := gradient[output]*last[lastIndex][hidden] + model.Config.L2Penalty*model.Head[output][hidden]
			model.Head[output][hidden] -= model.Config.LearningRate * clipped(headGradient)
		}
	}
	for output := range model.HeadBias {
		model.HeadBias[output] -= model.Config.LearningRate * clipped(gradient[output])
	}
	for layerIndex := len(model.Layers) - 1; layerIndex >= 0; layerIndex-- {
		layer := &model.Layers[layerIndex]
		current := activations[layerIndex+1]
		previous := activations[layerIndex]
		previousDelta := makeMatrix(len(previous), len(previous[0]))
		weightGradients := makeTensorLike(layer.Weights)
		biasGradients := make([]float64, len(layer.Bias))
		for timeIndex := len(current) - 1; timeIndex >= 0; timeIndex-- {
			for output := range layer.Weights {
				deltaZ := deltas[layerIndex][timeIndex][output] * (1 - current[timeIndex][output]*current[timeIndex][output])
				biasGradients[output] += deltaZ
				for input := range layer.Weights[output] {
					weightGradients[output][input][0] += deltaZ * previous[timeIndex][input]
					previousDelta[timeIndex][input] += deltaZ * layer.Weights[output][input][0]
					past := timeIndex - layer.Dilation
					if past >= 0 {
						weightGradients[output][input][1] += deltaZ * previous[past][input]
						previousDelta[past][input] += deltaZ * layer.Weights[output][input][1]
					}
				}
			}
		}
		for output := range layer.Weights {
			layer.Bias[output] -= model.Config.LearningRate * clipped(biasGradients[output])
			for input := range layer.Weights[output] {
				for kernel := range layer.Weights[output][input] {
					gradientValue := weightGradients[output][input][kernel] + model.Config.L2Penalty*layer.Weights[output][input][kernel]
					layer.Weights[output][input][kernel] -= model.Config.LearningRate * clipped(gradientValue)
				}
			}
		}
		if layerIndex > 0 {
			deltas[layerIndex-1] = previousDelta
		}
	}
	return nil
}

func (model TCNModel) forward(sequence [][]float64) ([][][]float64, []float64, error) {
	if len(sequence) == 0 {
		return nil, nil, fmt.Errorf("TCN input sequence is empty")
	}
	for _, row := range sequence {
		if len(row) != 4 {
			return nil, nil, fmt.Errorf("TCN input must have four OHLC channels")
		}
	}
	activations := make([][][]float64, 1, len(model.Layers)+1)
	activations[0] = cloneMatrix(sequence)
	previous := activations[0]
	for _, layer := range model.Layers {
		current := makeMatrix(len(previous), len(layer.Weights))
		for timeIndex := range previous {
			for output := range layer.Weights {
				value := layer.Bias[output]
				for input := range layer.Weights[output] {
					value += layer.Weights[output][input][0] * previous[timeIndex][input]
					if past := timeIndex - layer.Dilation; past >= 0 {
						value += layer.Weights[output][input][1] * previous[past][input]
					}
				}
				current[timeIndex][output] = math.Tanh(value)
			}
		}
		activations = append(activations, current)
		previous = current
	}
	last := previous[len(previous)-1]
	raw := make([]float64, model.Outputs)
	for output := range raw {
		raw[output] = model.HeadBias[output]
		for hidden := range last {
			raw[output] += model.Head[output][hidden] * last[hidden]
		}
	}
	return activations, raw, nil
}

func gamBasis(feature FeaturePoint, interactions bool) ([]float64, []string, error) {
	if !feature.Available {
		return nil, nil, fmt.Errorf("causal feature point is unavailable")
	}
	raw := []float64{feature.HistoryRatio.TRMean, feature.HistoryRatio.TRStdDev, feature.HistoryRatio.HighLowMean, feature.HistoryRatio.HighLowStdDev, feature.HistoryRatio.Parkinson, feature.HistoryRatio.YangZhang}
	names := []string{"tr_mean", "tr_std_dev", "high_low_mean", "high_low_std_dev", "parkinson", "yang_zhang"}
	values := make([]float64, len(raw))
	for index, value := range raw {
		if value <= 0 || !finite(value) {
			return nil, nil, fmt.Errorf("invalid dimensionless activity feature")
		}
		values[index] = math.Tanh(math.Log(value))
	}
	basis := []float64{1}
	basisNames := []string{"intercept"}
	for index, value := range values {
		basis = append(basis, value, value*value)
		basisNames = append(basisNames, names[index], names[index]+"^2")
	}
	if interactions {
		for left := 0; left < len(values); left++ {
			for right := left + 1; right < len(values); right++ {
				basis = append(basis, values[left]*values[right])
				basisNames = append(basisNames, names[left]+":"+names[right])
			}
		}
	}
	return basis, basisNames, nil
}

func validateExamples(examples []SupervisedExample, outputs int, loss string) error {
	if len(examples) == 0 || outputs <= 0 {
		return fmt.Errorf("training examples and outputs are required")
	}
	if loss != LossMSE && loss != LossBinary && loss != LossSoftmax {
		return fmt.Errorf("unsupported training loss %q", loss)
	}
	if loss == LossBinary && outputs != 1 {
		return fmt.Errorf("binary loss requires one output")
	}
	for _, example := range examples {
		if !example.Feature.Available || len(example.Target) != outputs {
			return fmt.Errorf("invalid supervised example")
		}
		for _, value := range example.Target {
			if !finite(value) {
				return fmt.Errorf("training target is not finite")
			}
		}
	}
	return nil
}

func activate(raw []float64, loss string) []float64 {
	result := append([]float64(nil), raw...)
	switch loss {
	case LossBinary:
		result[0] = sigmoid(raw[0])
	case LossSoftmax:
		maximum := raw[0]
		for _, value := range raw[1:] {
			maximum = math.Max(maximum, value)
		}
		total := 0.0
		for index := range result {
			result[index] = math.Exp(raw[index] - maximum)
			total += result[index]
		}
		for index := range result {
			result[index] /= total
		}
	}
	return result
}

func outputGradient(prediction, target []float64, loss string) []float64 {
	result := make([]float64, len(prediction))
	for index := range result {
		result[index] = prediction[index] - target[index]
		if loss == LossMSE {
			result[index] *= 2 / float64(len(result))
		}
	}
	return result
}

func matrixVector(matrix [][]float64, vector []float64) []float64 {
	result := make([]float64, len(matrix))
	for row := range matrix {
		for column := range matrix[row] {
			result[row] += matrix[row][column] * vector[column]
		}
	}
	return result
}

func makeMatrix(rows, columns int) [][]float64 {
	result := make([][]float64, rows)
	for row := range result {
		result[row] = make([]float64, columns)
	}
	return result
}

func cloneMatrix(input [][]float64) [][]float64 {
	result := make([][]float64, len(input))
	for index := range input {
		result[index] = append([]float64(nil), input[index]...)
	}
	return result
}

func makeTensorLike(input [][][]float64) [][][]float64 {
	result := make([][][]float64, len(input))
	for first := range input {
		result[first] = make([][]float64, len(input[first]))
		for second := range input[first] {
			result[first][second] = make([]float64, len(input[first][second]))
		}
	}
	return result
}

func softThreshold(value, threshold float64) float64 {
	if value > threshold {
		return value - threshold
	}
	if value < -threshold {
		return value + threshold
	}
	return 0
}

func deterministicWeight(index int) float64 {
	return 0.08 * math.Sin(float64(index)*1.618033988749895)
}

func sigmoid(value float64) float64 {
	if value >= 0 {
		exponential := math.Exp(-value)
		return 1 / (1 + exponential)
	}
	exponential := math.Exp(value)
	return exponential / (1 + exponential)
}

func clipped(value float64) float64 {
	if value > 5 {
		return 5
	}
	if value < -5 {
		return -5
	}
	return value
}
