package backtestresult

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/quant"
)

const (
	BacktestKeyVersion    = "p03-key-v1"
	SpecSchemaVersion     = "p03-spec-v1"
	ResultSchemaVersion   = "p03-result-v1"
	SummarySchemaVersion  = "p03-summary-v1"
	PathSchemaVersion     = "p03-path-v1"
	ManifestSchemaVersion = "p03-manifest-v1"
	DatasetSchemaVersion  = "ohlcv-v1"
	ParameterSchemaV1     = "sigmoid-dca-params-v1"
	DefaultPathBlockSize  = 256
)

type SpecInput struct {
	StrategyID                 string
	StrategyVersion            string
	ParameterSchemaVersion     string
	Parameters                 any
	CoreSpec                   backtestcore.Spec
	DatasetVersion             string
	DatasetHash                string
	LongTermFilterVersion      string
	LongTermFilterSettings     any
	ModelArtifactHash          string
	PredictionSchemaHash       string
	MaterializedPredictionHash string
	DynamicPolicyHash          string
	DynamicControlMode         string
	EffectiveParametersHash    string
}

// SpecSnapshot is the canonical, immutable identity payload. Request/task
// metadata such as a candidate ID is intentionally excluded when the resolved
// parameter snapshot is identical.
type SpecSnapshot struct {
	SchemaVersion              string          `json:"schema_version"`
	BacktestKeyVersion         string          `json:"backtest_key_version"`
	Runner                     string          `json:"runner"`
	StrategyID                 string          `json:"strategy_id"`
	StrategyVersion            string          `json:"strategy_version"`
	ParameterSchemaVersion     string          `json:"parameter_schema_version"`
	Parameters                 json.RawMessage `json:"parameters"`
	ParameterHash              string          `json:"parameter_hash"`
	InstrumentID               string          `json:"instrument_id"`
	Symbol                     string          `json:"symbol"`
	DataSource                 string          `json:"data_source"`
	Interval                   string          `json:"interval"`
	ExecutionMode              string          `json:"execution_mode"`
	PositionStructure          string          `json:"position_structure"`
	StartTimeMs                int64           `json:"start_time_ms"`
	EndTimeMs                  int64           `json:"end_time_ms"`
	EvaluationStartMs          int64           `json:"evaluation_start_ms"`
	EvaluationEndMs            int64           `json:"evaluation_end_ms"`
	PrefixMode                 string          `json:"prefix_mode"`
	InitialCapital             float64         `json:"initial_capital"`
	MonthlyContribution        float64         `json:"monthly_contribution"`
	InitialAssetQuantity       float64         `json:"initial_asset_quantity"`
	MinimumTradeUSD            float64         `json:"minimum_trade_usd"`
	MinimumAssetQuantity       float64         `json:"minimum_asset_quantity"`
	FeeRate                    float64         `json:"fee_rate"`
	SlippageRate               float64         `json:"slippage_rate"`
	DatasetVersion             string          `json:"dataset_version"`
	DatasetHash                string          `json:"dataset_hash"`
	CoreVersion                string          `json:"core_version"`
	LongTermFilterVersion      string          `json:"long_term_filter_version,omitempty"`
	LongTermFilterSettings     json.RawMessage `json:"long_term_filter_settings,omitempty"`
	ModelArtifactHash          string          `json:"model_artifact_hash,omitempty"`
	PredictionSchemaHash       string          `json:"prediction_schema_hash,omitempty"`
	MaterializedPredictionHash string          `json:"materialized_prediction_hash,omitempty"`
	DynamicPolicyHash          string          `json:"dynamic_policy_hash,omitempty"`
	DynamicControlMode         string          `json:"dynamic_control_mode,omitempty"`
	EffectiveParametersHash    string          `json:"effective_parameters_hash,omitempty"`
}

type Identity struct {
	Snapshot        SpecSnapshot
	SnapshotJSON    []byte
	BacktestKey     string
	SpecContentHash string
}

type datasetSnapshot struct {
	SchemaVersion string      `json:"schema_version"`
	Bars          []quant.Bar `json:"bars"`
}

func BuildIdentity(input SpecInput, bars []quant.Bar) (Identity, error) {
	if strings.TrimSpace(input.StrategyID) == "" {
		return Identity{}, fmt.Errorf("strategy id is required")
	}
	if strings.TrimSpace(input.StrategyVersion) == "" {
		return Identity{}, fmt.Errorf("strategy version is required")
	}
	if input.Parameters == nil {
		return Identity{}, fmt.Errorf("parameter snapshot is required")
	}

	parameterJSON, err := canonicalJSON(input.Parameters)
	if err != nil {
		return Identity{}, fmt.Errorf("canonicalize parameters: %w", err)
	}
	filterJSON, err := canonicalOptionalJSON(input.LongTermFilterSettings)
	if err != nil {
		return Identity{}, fmt.Errorf("canonicalize long-term filter: %w", err)
	}

	spec := input.CoreSpec
	if strings.TrimSpace(spec.Runner) == "" {
		spec.Runner = backtestcore.RunnerSigmoidDCA
	}
	if strings.TrimSpace(spec.PrefixMode) == "" {
		spec.PrefixMode = backtestcore.PrefixModeExecute
	}
	if strings.TrimSpace(spec.CoreVersion) == "" {
		spec.CoreVersion = backtestcore.CoreVersion
	}
	if spec.EvaluationStartMs == 0 {
		spec.EvaluationStartMs = spec.StartTimeMs
	}
	if spec.EvaluationEndMs == 0 {
		spec.EvaluationEndMs = spec.EndTimeMs
	}
	spec.Costs = quant.NormalizeExecutionCosts(spec.Costs)
	if err := validateResolvedSpec(spec); err != nil {
		return Identity{}, err
	}

	datasetVersion := strings.TrimSpace(input.DatasetVersion)
	if datasetVersion == "" {
		datasetVersion = DatasetSchemaVersion
	}
	datasetHash := strings.TrimSpace(input.DatasetHash)
	if datasetHash == "" {
		datasetHash, err = HashDataset(datasetVersion, bars)
		if err != nil {
			return Identity{}, err
		}
	}

	parameterSchema := strings.TrimSpace(input.ParameterSchemaVersion)
	if parameterSchema == "" {
		parameterSchema = ParameterSchemaV1
	}
	snapshot := SpecSnapshot{
		SchemaVersion:              SpecSchemaVersion,
		BacktestKeyVersion:         BacktestKeyVersion,
		Runner:                     spec.Runner,
		StrategyID:                 strings.TrimSpace(input.StrategyID),
		StrategyVersion:            strings.TrimSpace(input.StrategyVersion),
		ParameterSchemaVersion:     parameterSchema,
		Parameters:                 json.RawMessage(parameterJSON),
		ParameterHash:              hashBytes(parameterJSON),
		InstrumentID:               strings.TrimSpace(spec.InstrumentID),
		Symbol:                     strings.TrimSpace(spec.Symbol),
		DataSource:                 strings.TrimSpace(spec.DataSource),
		Interval:                   strings.TrimSpace(spec.Interval),
		ExecutionMode:              strings.TrimSpace(spec.ExecutionMode),
		PositionStructure:          strings.TrimSpace(spec.PositionStructure),
		StartTimeMs:                spec.StartTimeMs,
		EndTimeMs:                  spec.EndTimeMs,
		EvaluationStartMs:          spec.EvaluationStartMs,
		EvaluationEndMs:            spec.EvaluationEndMs,
		PrefixMode:                 spec.PrefixMode,
		InitialCapital:             spec.InitialCapital,
		MonthlyContribution:        spec.MonthlyContribution,
		InitialAssetQuantity:       spec.InitialAssetQuantity,
		MinimumTradeUSD:            spec.MinimumTradeUSD,
		MinimumAssetQuantity:       spec.MinimumAssetQuantity,
		FeeRate:                    spec.Costs.FeeRate,
		SlippageRate:               spec.Costs.SpreadRate,
		DatasetVersion:             datasetVersion,
		DatasetHash:                datasetHash,
		CoreVersion:                strings.TrimSpace(spec.CoreVersion),
		LongTermFilterVersion:      strings.TrimSpace(input.LongTermFilterVersion),
		LongTermFilterSettings:     json.RawMessage(filterJSON),
		ModelArtifactHash:          strings.TrimSpace(input.ModelArtifactHash),
		PredictionSchemaHash:       strings.TrimSpace(input.PredictionSchemaHash),
		MaterializedPredictionHash: strings.TrimSpace(input.MaterializedPredictionHash),
		DynamicPolicyHash:          strings.TrimSpace(input.DynamicPolicyHash),
		DynamicControlMode:         strings.TrimSpace(input.DynamicControlMode),
		EffectiveParametersHash:    strings.TrimSpace(input.EffectiveParametersHash),
	}
	return IdentityFromSnapshot(snapshot)
}

func IdentityFromSnapshot(snapshot SpecSnapshot) (Identity, error) {
	if snapshot.SchemaVersion != SpecSchemaVersion || snapshot.BacktestKeyVersion != BacktestKeyVersion {
		return Identity{}, fmt.Errorf("unsupported backtest spec/key version: %s/%s", snapshot.SchemaVersion, snapshot.BacktestKeyVersion)
	}
	parameterJSON, err := canonicalRawJSON(snapshot.Parameters)
	if err != nil {
		return Identity{}, fmt.Errorf("canonicalize parameter snapshot: %w", err)
	}
	if hashBytes(parameterJSON) != snapshot.ParameterHash {
		return Identity{}, fmt.Errorf("parameter snapshot hash mismatch")
	}
	snapshot.Parameters = json.RawMessage(parameterJSON)
	if len(snapshot.LongTermFilterSettings) > 0 {
		filterJSON, err := canonicalRawJSON(snapshot.LongTermFilterSettings)
		if err != nil {
			return Identity{}, fmt.Errorf("canonicalize long-term filter snapshot: %w", err)
		}
		snapshot.LongTermFilterSettings = json.RawMessage(filterJSON)
	}
	snapshotJSON, err := canonicalJSON(snapshot)
	if err != nil {
		return Identity{}, fmt.Errorf("canonicalize backtest spec: %w", err)
	}
	digest := sha256.Sum256(snapshotJSON)
	hexDigest := hex.EncodeToString(digest[:])
	return Identity{
		Snapshot:        snapshot,
		SnapshotJSON:    snapshotJSON,
		BacktestKey:     "bt:v1:" + hexDigest,
		SpecContentHash: "sha256:" + hexDigest,
	}, nil
}

func DecodeIdentity(raw []byte) (Identity, error) {
	var snapshot SpecSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return Identity{}, fmt.Errorf("decode backtest spec: %w", err)
	}
	return IdentityFromSnapshot(snapshot)
}

func HashDataset(version string, bars []quant.Bar) (string, error) {
	if len(bars) == 0 {
		return "", fmt.Errorf("dataset bars cannot be empty")
	}
	previous := int64(0)
	for i, bar := range bars {
		if i > 0 && bar.OpenTime <= previous {
			return "", fmt.Errorf("dataset bars must be strictly ordered")
		}
		previous = bar.OpenTime
		for name, value := range map[string]float64{
			"open": bar.Open, "high": bar.High, "low": bar.Low,
			"close": bar.Close, "volume": bar.Volume,
		} {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return "", fmt.Errorf("dataset bar %d has invalid %s", i, name)
			}
		}
	}
	raw, err := canonicalJSON(datasetSnapshot{SchemaVersion: version, Bars: bars})
	if err != nil {
		return "", fmt.Errorf("canonicalize dataset: %w", err)
	}
	return hashBytes(raw), nil
}

func validateResolvedSpec(spec backtestcore.Spec) error {
	for name, value := range map[string]string{
		"runner": spec.Runner, "instrument_id": spec.InstrumentID,
		"symbol": spec.Symbol, "data_source": spec.DataSource,
		"interval": spec.Interval, "execution_mode": spec.ExecutionMode,
		"position_structure": spec.PositionStructure, "core_version": spec.CoreVersion,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if spec.StartTimeMs <= 0 || spec.EndTimeMs < spec.StartTimeMs {
		return fmt.Errorf("invalid backtest time range")
	}
	if spec.EvaluationStartMs < spec.StartTimeMs || spec.EvaluationEndMs > spec.EndTimeMs || spec.EvaluationStartMs > spec.EvaluationEndMs {
		return fmt.Errorf("invalid evaluation time range")
	}
	for name, value := range map[string]float64{
		"initial_capital":        spec.InitialCapital,
		"monthly_contribution":   spec.MonthlyContribution,
		"initial_asset_quantity": spec.InitialAssetQuantity,
		"minimum_trade_usd":      spec.MinimumTradeUSD,
		"minimum_asset_quantity": spec.MinimumAssetQuantity,
		"fee_rate":               spec.Costs.FeeRate,
		"slippage_rate":          spec.Costs.SpreadRate,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("invalid %s", name)
		}
	}
	if spec.InitialCapital <= 0 {
		return fmt.Errorf("initial capital must be positive")
	}
	return nil
}

func canonicalOptionalJSON(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return canonicalJSON(value)
}

func canonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return canonicalRawJSON(raw)
}

func canonicalRawJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	normalized, err := normalizeJSONNumbers(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("multiple JSON values are not allowed")
}

func normalizeJSONNumbers(value any) (any, error) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			normalized, err := normalizeJSONNumbers(child)
			if err != nil {
				return nil, err
			}
			item[key] = normalized
		}
		return item, nil
	case []any:
		for index, child := range item {
			normalized, err := normalizeJSONNumbers(child)
			if err != nil {
				return nil, err
			}
			item[index] = normalized
		}
		return item, nil
	case json.Number:
		if integer, err := strconv.ParseInt(item.String(), 10, 64); err == nil {
			return json.Number(strconv.FormatInt(integer, 10)), nil
		}
		value, err := strconv.ParseFloat(item.String(), 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("invalid JSON number %q", item.String())
		}
		if value == 0 {
			return json.Number("0"), nil
		}
		return json.Number(strconv.FormatFloat(value, 'g', -1, 64)), nil
	default:
		return value, nil
	}
}

func hashBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}
