package performancereport

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

	performancecore "quantsaas/internal/performance"
)

const (
	AnalysisKeyVersion = "p04-key-v1"
	SettingsVersion    = "p04-settings-v1"
)

type BetaBenchmarkSettings struct {
	InstrumentID   string `json:"instrument_id"`
	Symbol         string `json:"symbol"`
	DataSource     string `json:"data_source"`
	Interval       string `json:"interval"`
	StartTimeMs    int64  `json:"start_time_ms"`
	EndTimeMs      int64  `json:"end_time_ms"`
	DatasetVersion string `json:"dataset_version"`
	DatasetHash    string `json:"dataset_hash"`
}

type ResolvedSettings struct {
	SchemaVersion        string                 `json:"schema_version"`
	AggregationVersion   string                 `json:"aggregation_version"`
	StatisticsVersion    string                 `json:"statistics_version"`
	SortinoVersion       string                 `json:"sortino_version"`
	BetaVersion          string                 `json:"beta_version"`
	AnnualizationVersion string                 `json:"annualization_version"`
	RiskFreeAnnualRate   float64                `json:"risk_free_annual_rate"`
	HistogramBins        int                    `json:"histogram_bins"`
	BetaBenchmark        *BetaBenchmarkSettings `json:"beta_benchmark,omitempty"`
}

type IdentitySnapshot struct {
	KeyVersion                         string           `json:"key_version"`
	ReportSchemaVersion                string           `json:"report_schema_version"`
	AnalysisVersion                    string           `json:"analysis_version"`
	BacktestResultID                   uint             `json:"backtest_result_id"`
	BacktestResultVersion              string           `json:"backtest_result_version"`
	BacktestResultContentHash          string           `json:"backtest_result_content_hash"`
	AnnualizationBacktestResultID      uint             `json:"annualization_backtest_result_id"`
	AnnualizationBacktestResultVersion string           `json:"annualization_backtest_result_version"`
	AnnualizationResultContentHash     string           `json:"annualization_result_content_hash"`
	Settings                           ResolvedSettings `json:"settings"`
	SettingsHash                       string           `json:"settings_hash"`
}

type Identity struct {
	Snapshot     IdentitySnapshot
	SnapshotJSON []byte
	AnalysisKey  string
	SettingsJSON []byte
}

func BuildIdentity(snapshot IdentitySnapshot) (Identity, error) {
	snapshot.KeyVersion = AnalysisKeyVersion
	snapshot.ReportSchemaVersion = performancecore.ReportSchemaVersion
	snapshot.AnalysisVersion = performancecore.AnalysisVersion
	if snapshot.BacktestResultID == 0 || snapshot.AnnualizationBacktestResultID == 0 {
		return Identity{}, fmt.Errorf("source and annualization result IDs are required")
	}
	for name, value := range map[string]string{
		"source result version":             snapshot.BacktestResultVersion,
		"source result content hash":        snapshot.BacktestResultContentHash,
		"annualization result version":      snapshot.AnnualizationBacktestResultVersion,
		"annualization result content hash": snapshot.AnnualizationResultContentHash,
	} {
		if strings.TrimSpace(value) == "" {
			return Identity{}, fmt.Errorf("%s is required", name)
		}
	}
	snapshot.Settings = normalizeSettings(snapshot.Settings)
	settingsJSON, err := canonicalJSON(snapshot.Settings)
	if err != nil {
		return Identity{}, fmt.Errorf("canonicalize performance settings: %w", err)
	}
	snapshot.SettingsHash = hashBytes(settingsJSON)
	snapshotJSON, err := canonicalJSON(snapshot)
	if err != nil {
		return Identity{}, fmt.Errorf("canonicalize performance identity: %w", err)
	}
	digest := sha256.Sum256(snapshotJSON)
	return Identity{
		Snapshot: snapshot, SnapshotJSON: snapshotJSON,
		AnalysisKey: "pr:v1:" + hex.EncodeToString(digest[:]), SettingsJSON: settingsJSON,
	}, nil
}

func DecodeIdentity(raw []byte) (Identity, error) {
	var snapshot IdentitySnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return Identity{}, err
	}
	if snapshot.KeyVersion != AnalysisKeyVersion || snapshot.ReportSchemaVersion != performancecore.ReportSchemaVersion || snapshot.AnalysisVersion != performancecore.AnalysisVersion {
		return Identity{}, fmt.Errorf("unsupported performance report identity version")
	}
	expectedSettingsHash := snapshot.SettingsHash
	identity, err := BuildIdentity(snapshot)
	if err != nil {
		return Identity{}, err
	}
	if expectedSettingsHash != identity.Snapshot.SettingsHash {
		return Identity{}, fmt.Errorf("performance settings hash mismatch")
	}
	return identity, nil
}

func normalizeSettings(settings ResolvedSettings) ResolvedSettings {
	settings.SchemaVersion = SettingsVersion
	settings.AggregationVersion = performancecore.AggregationUTCVersion
	settings.StatisticsVersion = performancecore.DistributionStatsVersion
	settings.SortinoVersion = performancecore.SortinoFormulaVersion
	settings.BetaVersion = performancecore.BetaFormulaVersion
	settings.AnnualizationVersion = performancecore.AnnualizationVersion
	if settings.HistogramBins == 0 {
		settings.HistogramBins = performancecore.DefaultHistogramBins
	}
	return settings
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
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values are not allowed")
		}
		return nil, err
	}
	normalized, err := normalizeNumbers(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func normalizeNumbers(value any) (any, error) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			normalized, err := normalizeNumbers(child)
			if err != nil {
				return nil, err
			}
			item[key] = normalized
		}
		return item, nil
	case []any:
		for index, child := range item {
			normalized, err := normalizeNumbers(child)
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
		number, err := strconv.ParseFloat(item.String(), 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("invalid JSON number %q", item.String())
		}
		if number == 0 {
			return json.Number("0"), nil
		}
		return json.Number(strconv.FormatFloat(number, 'g', -1, 64)), nil
	default:
		return value, nil
	}
}

func hashBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}
