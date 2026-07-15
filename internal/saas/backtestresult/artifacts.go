package backtestresult

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"quantsaas/internal/backtestcore"
)

type SummaryOptions struct {
	Sortino *float64
	Beta    *float64
	Extra   any
}

type SummaryData struct {
	SchemaVersion           string                   `json:"schema_version"`
	ROI                     float64                  `json:"roi"`
	FinalEquity             float64                  `json:"final_equity"`
	MaxDrawdown             float64                  `json:"max_drawdown"`
	TradeCount              int                      `json:"trade_count"`
	ExposureDaysRatio       float64                  `json:"exposure_days_ratio"`
	AverageActualExposure   float64                  `json:"average_actual_exposure"`
	LongestUnderwaterDays   float64                  `json:"longest_underwater_days"`
	LongestUnderwaterPoints int                      `json:"longest_underwater_points"`
	Sortino                 *float64                 `json:"sortino,omitempty"`
	Beta                    *float64                 `json:"beta,omitempty"`
	Costs                   backtestcore.CostSummary `json:"costs"`
	TotalInjected           float64                  `json:"total_injected"`
	EvaluationInitial       float64                  `json:"evaluation_initial"`
	EvaluationStartMs       int64                    `json:"evaluation_start_ms"`
	EvaluationEndMs         int64                    `json:"evaluation_end_ms"`
	Extra                   json.RawMessage          `json:"extra,omitempty"`
}

type PathPoint struct {
	backtestcore.NAVPoint
	BenchmarkEquity      *float64 `json:"benchmark_equity,omitempty"`
	BenchmarkDailyReturn *float64 `json:"benchmark_daily_return,omitempty"`
}

type PathBlockData struct {
	SchemaVersion   string      `json:"schema_version"`
	BlockIndex      int         `json:"block_index"`
	StartPointIndex int         `json:"start_point_index"`
	EndPointIndex   int         `json:"end_point_index"`
	StartTimeMs     int64       `json:"start_time_ms"`
	EndTimeMs       int64       `json:"end_time_ms"`
	Points          []PathPoint `json:"points"`
}

type PathManifestEntry struct {
	BlockIndex      int    `json:"block_index"`
	StartPointIndex int    `json:"start_point_index"`
	EndPointIndex   int    `json:"end_point_index"`
	StartTimeMs     int64  `json:"start_time_ms"`
	EndTimeMs       int64  `json:"end_time_ms"`
	PointCount      int    `json:"point_count"`
	ContentHash     string `json:"content_hash"`
}

type PathManifest struct {
	SchemaVersion string              `json:"schema_version"`
	PointCount    int                 `json:"point_count"`
	BlockCount    int                 `json:"block_count"`
	Blocks        []PathManifestEntry `json:"blocks"`
}

type PathBlockArtifact struct {
	Data        PathBlockData
	PayloadJSON []byte
	ContentHash string
}

type Artifacts struct {
	Summary           SummaryData
	SummaryJSON       []byte
	SummaryHash       string
	Blocks            []PathBlockArtifact
	Manifest          PathManifest
	ManifestJSON      []byte
	ManifestHash      string
	ResultContentHash string
}

type resultContentEnvelope struct {
	ResultVersion string `json:"result_version"`
	SpecHash      string `json:"spec_hash"`
	SummaryHash   string `json:"summary_hash"`
	ManifestHash  string `json:"manifest_hash"`
	PointCount    int    `json:"point_count"`
	BlockCount    int    `json:"block_count"`
}

func BuildSummary(result backtestcore.Result, maxDrawdown float64, options SummaryOptions) (SummaryData, error) {
	if len(result.Path) == 0 {
		return SummaryData{}, fmt.Errorf("cannot summarize an empty backtest path")
	}
	for name, value := range map[string]float64{
		"roi":                result.TotalReturn,
		"final_equity":       result.FinalAssets,
		"max_drawdown":       maxDrawdown,
		"total_injected":     result.TotalInjected,
		"evaluation_initial": result.EvaluationInitial,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return SummaryData{}, fmt.Errorf("invalid summary %s", name)
		}
	}
	if err := validateOptionalMetric("sortino", options.Sortino); err != nil {
		return SummaryData{}, err
	}
	if err := validateOptionalMetric("beta", options.Beta); err != nil {
		return SummaryData{}, err
	}

	exposedDays := 0
	exposureTotal := 0.0
	for _, point := range result.Path {
		if math.IsNaN(point.ActualExposureWeight) || math.IsInf(point.ActualExposureWeight, 0) {
			return SummaryData{}, fmt.Errorf("invalid actual exposure at %d", point.TimeMs)
		}
		if point.ActualExposureWeight > 0 {
			exposedDays++
		}
		exposureTotal += point.ActualExposureWeight
	}
	underwaterDays, underwaterPoints := longestUnderwater(result.Path)
	extraJSON, err := canonicalOptionalJSON(options.Extra)
	if err != nil {
		return SummaryData{}, fmt.Errorf("canonicalize summary metadata: %w", err)
	}

	return SummaryData{
		SchemaVersion:           SummarySchemaVersion,
		ROI:                     result.TotalReturn,
		FinalEquity:             result.FinalAssets,
		MaxDrawdown:             maxDrawdown,
		TradeCount:              result.TradeCount,
		ExposureDaysRatio:       float64(exposedDays) / float64(len(result.Path)),
		AverageActualExposure:   exposureTotal / float64(len(result.Path)),
		LongestUnderwaterDays:   underwaterDays,
		LongestUnderwaterPoints: underwaterPoints,
		Sortino:                 cloneFloat(options.Sortino),
		Beta:                    cloneFloat(options.Beta),
		Costs:                   result.Costs,
		TotalInjected:           result.TotalInjected,
		EvaluationInitial:       result.EvaluationInitial,
		EvaluationStartMs:       result.EvaluationStartMs,
		EvaluationEndMs:         result.EvaluationEndMs,
		Extra:                   json.RawMessage(extraJSON),
	}, nil
}

func BuildArtifacts(specHash string, summary SummaryData, path []PathPoint, blockSize int) (Artifacts, error) {
	if specHash == "" {
		return Artifacts{}, fmt.Errorf("spec hash is required")
	}
	if len(path) == 0 {
		return Artifacts{}, fmt.Errorf("path cannot be empty")
	}
	if err := validatePath(path); err != nil {
		return Artifacts{}, err
	}
	if blockSize <= 0 {
		blockSize = DefaultPathBlockSize
	}
	summary.SchemaVersion = SummarySchemaVersion
	summaryJSON, err := canonicalJSON(summary)
	if err != nil {
		return Artifacts{}, fmt.Errorf("canonicalize result summary: %w", err)
	}
	artifacts := Artifacts{
		Summary:     summary,
		SummaryJSON: summaryJSON,
		SummaryHash: hashBytes(summaryJSON),
	}

	manifest := PathManifest{
		SchemaVersion: ManifestSchemaVersion,
		PointCount:    len(path),
		Blocks:        make([]PathManifestEntry, 0, (len(path)+blockSize-1)/blockSize),
	}
	for start := 0; start < len(path); start += blockSize {
		endExclusive := start + blockSize
		if endExclusive > len(path) {
			endExclusive = len(path)
		}
		blockIndex := len(artifacts.Blocks)
		blockPoints := append([]PathPoint(nil), path[start:endExclusive]...)
		data := PathBlockData{
			SchemaVersion:   PathSchemaVersion,
			BlockIndex:      blockIndex,
			StartPointIndex: start,
			EndPointIndex:   endExclusive - 1,
			StartTimeMs:     blockPoints[0].TimeMs,
			EndTimeMs:       blockPoints[len(blockPoints)-1].TimeMs,
			Points:          blockPoints,
		}
		payload, err := canonicalJSON(data)
		if err != nil {
			return Artifacts{}, fmt.Errorf("canonicalize path block %d: %w", blockIndex, err)
		}
		contentHash := hashBytes(payload)
		artifacts.Blocks = append(artifacts.Blocks, PathBlockArtifact{
			Data:        data,
			PayloadJSON: payload,
			ContentHash: contentHash,
		})
		manifest.Blocks = append(manifest.Blocks, PathManifestEntry{
			BlockIndex:      blockIndex,
			StartPointIndex: start,
			EndPointIndex:   endExclusive - 1,
			StartTimeMs:     data.StartTimeMs,
			EndTimeMs:       data.EndTimeMs,
			PointCount:      len(blockPoints),
			ContentHash:     contentHash,
		})
	}
	manifest.BlockCount = len(manifest.Blocks)
	manifestJSON, err := canonicalJSON(manifest)
	if err != nil {
		return Artifacts{}, fmt.Errorf("canonicalize path manifest: %w", err)
	}
	manifestHash := hashBytes(manifestJSON)
	resultHash, err := BuildResultContentHash(specHash, artifacts.SummaryHash, manifestHash, manifest.PointCount, manifest.BlockCount)
	if err != nil {
		return Artifacts{}, err
	}
	artifacts.Manifest = manifest
	artifacts.ManifestJSON = manifestJSON
	artifacts.ManifestHash = manifestHash
	artifacts.ResultContentHash = resultHash
	return artifacts, nil
}

func BuildResultContentHash(specHash string, summaryHash string, manifestHash string, pointCount int, blockCount int) (string, error) {
	envelope := resultContentEnvelope{
		ResultVersion: ResultSchemaVersion,
		SpecHash:      specHash,
		SummaryHash:   summaryHash,
		ManifestHash:  manifestHash,
		PointCount:    pointCount,
		BlockCount:    blockCount,
	}
	raw, err := canonicalJSON(envelope)
	if err != nil {
		return "", fmt.Errorf("canonicalize result content envelope: %w", err)
	}
	return hashBytes(raw), nil
}

func DecodeSummary(raw []byte) (SummaryData, error) {
	var summary SummaryData
	if err := json.Unmarshal(raw, &summary); err != nil {
		return SummaryData{}, err
	}
	return summary, nil
}

func DecodeManifest(raw []byte) (PathManifest, error) {
	var manifest PathManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return PathManifest{}, err
	}
	return manifest, nil
}

func DecodePathBlock(raw []byte) (PathBlockData, error) {
	var block PathBlockData
	if err := json.Unmarshal(raw, &block); err != nil {
		return PathBlockData{}, err
	}
	return block, nil
}

func longestUnderwater(path []backtestcore.NAVPoint) (float64, int) {
	if len(path) == 0 {
		return 0, 0
	}
	peak := path[0].TotalEquity
	underwaterStart := int64(0)
	underwaterPoints := 0
	longestDuration := int64(0)
	longestPoints := 0
	for _, point := range path[1:] {
		if point.TotalEquity >= peak {
			if underwaterStart != 0 {
				duration := point.TimeMs - underwaterStart
				if duration > longestDuration {
					longestDuration = duration
				}
				if underwaterPoints > longestPoints {
					longestPoints = underwaterPoints
				}
			}
			peak = point.TotalEquity
			underwaterStart = 0
			underwaterPoints = 0
			continue
		}
		if underwaterStart == 0 {
			underwaterStart = point.TimeMs
		}
		underwaterPoints++
		if underwaterPoints > longestPoints {
			longestPoints = underwaterPoints
		}
	}
	if underwaterStart != 0 {
		duration := path[len(path)-1].TimeMs - underwaterStart
		if duration > longestDuration {
			longestDuration = duration
		}
	}
	return float64(longestDuration) / float64((24 * time.Hour).Milliseconds()), longestPoints
}

func validateOptionalMetric(name string, value *float64) error {
	if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0)) {
		return fmt.Errorf("invalid %s", name)
	}
	return nil
}

func validatePath(path []PathPoint) error {
	previousTime := int64(0)
	for index, point := range path {
		if index > 0 && point.TimeMs <= previousTime {
			return fmt.Errorf("path timestamps must be strictly increasing")
		}
		previousTime = point.TimeMs
		for name, value := range map[string]float64{
			"price": point.Price, "total_equity": point.TotalEquity,
			"cash": point.Cash, "asset_quantity": point.AssetQuantity,
			"actual_exposure_weight": point.ActualExposureWeight,
			"daily_return":           point.DailyReturn,
		} {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("path point %d has invalid %s", index, name)
			}
		}
		if point.ActualExposureWeight < -1e-12 || point.ActualExposureWeight > 1+1e-12 {
			return fmt.Errorf("path point %d actual exposure is outside [0,1]", index)
		}
		if point.BenchmarkEquity != nil && (math.IsNaN(*point.BenchmarkEquity) || math.IsInf(*point.BenchmarkEquity, 0)) {
			return fmt.Errorf("path point %d has invalid benchmark equity", index)
		}
		if point.BenchmarkDailyReturn != nil && (math.IsNaN(*point.BenchmarkDailyReturn) || math.IsInf(*point.BenchmarkDailyReturn, 0)) {
			return fmt.Errorf("path point %d has invalid benchmark return", index)
		}
	}
	return nil
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
