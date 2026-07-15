package performancereport

import (
	"encoding/json"
	"fmt"

	performancecore "quantsaas/internal/performance"
)

type ChartManifestEntry struct {
	Kind          string `json:"kind"`
	SchemaVersion string `json:"schema_version"`
	ContentHash   string `json:"content_hash"`
	PointCount    int    `json:"point_count"`
}

type ChartManifest struct {
	SchemaVersion string               `json:"schema_version"`
	BlockCount    int                  `json:"block_count"`
	Blocks        []ChartManifestEntry `json:"blocks"`
}

type ChartArtifact struct {
	Kind          string
	SchemaVersion string
	PayloadJSON   []byte
	ContentHash   string
	PointCount    int
}

type Artifacts struct {
	Summary           performancecore.Summary
	SummaryJSON       []byte
	SummaryHash       string
	Charts            []ChartArtifact
	Manifest          ChartManifest
	ManifestJSON      []byte
	ManifestHash      string
	ReportContentHash string
}

type reportContentEnvelope struct {
	ReportSchemaVersion            string `json:"report_schema_version"`
	AnalysisVersion                string `json:"analysis_version"`
	SourceResultContentHash        string `json:"source_result_content_hash"`
	AnnualizationResultContentHash string `json:"annualization_result_content_hash"`
	SettingsHash                   string `json:"settings_hash"`
	SummaryHash                    string `json:"summary_hash"`
	ChartManifestHash              string `json:"chart_manifest_hash"`
}

func BuildArtifacts(identity Identity, result performancecore.Result) (Artifacts, error) {
	if identity.AnalysisKey == "" || identity.Snapshot.SettingsHash == "" {
		return Artifacts{}, fmt.Errorf("performance report identity is required")
	}
	result.Summary.SchemaVersion = performancecore.SummarySchemaVersion
	result.Summary.AnalysisVersion = performancecore.AnalysisVersion
	result.Summary.AggregationVersion = performancecore.AggregationUTCVersion
	summaryJSON, err := canonicalJSON(result.Summary)
	if err != nil {
		return Artifacts{}, err
	}
	artifacts := Artifacts{Summary: result.Summary, SummaryJSON: summaryJSON, SummaryHash: hashBytes(summaryJSON)}
	chartInputs := []struct {
		kind  string
		value any
		count int
	}{
		{performancecore.ChartDistributionDaily, result.Charts.DailyDistribution, len(result.Charts.DailyDistribution.Bins)},
		{performancecore.ChartDistributionWeekly, result.Charts.WeeklyDistribution, len(result.Charts.WeeklyDistribution.Bins)},
		{performancecore.ChartDistributionMonthly, result.Charts.MonthlyDistribution, len(result.Charts.MonthlyDistribution.Bins)},
		{performancecore.ChartReturnAccumulation, result.Charts.Accumulation, len(result.Charts.Accumulation.Points)},
		{performancecore.ChartUnderwater, result.Charts.Underwater, len(result.Charts.Underwater.Points)},
		{performancecore.ChartExposure, result.Charts.Exposure, len(result.Charts.Exposure.Points)},
	}
	manifest := ChartManifest{SchemaVersion: performancecore.ChartSchemaVersion, Blocks: make([]ChartManifestEntry, 0, len(chartInputs))}
	for _, input := range chartInputs {
		payload, err := canonicalJSON(input.value)
		if err != nil {
			return Artifacts{}, fmt.Errorf("canonicalize chart %s: %w", input.kind, err)
		}
		contentHash := hashBytes(payload)
		artifacts.Charts = append(artifacts.Charts, ChartArtifact{Kind: input.kind, SchemaVersion: performancecore.ChartSchemaVersion, PayloadJSON: payload, ContentHash: contentHash, PointCount: input.count})
		manifest.Blocks = append(manifest.Blocks, ChartManifestEntry{Kind: input.kind, SchemaVersion: performancecore.ChartSchemaVersion, ContentHash: contentHash, PointCount: input.count})
	}
	manifest.BlockCount = len(manifest.Blocks)
	manifestJSON, err := canonicalJSON(manifest)
	if err != nil {
		return Artifacts{}, err
	}
	artifacts.Manifest = manifest
	artifacts.ManifestJSON = manifestJSON
	artifacts.ManifestHash = hashBytes(manifestJSON)
	artifacts.ReportContentHash, err = BuildReportContentHash(identity, artifacts.SummaryHash, artifacts.ManifestHash)
	if err != nil {
		return Artifacts{}, err
	}
	return artifacts, nil
}

func BuildReportContentHash(identity Identity, summaryHash string, manifestHash string) (string, error) {
	payload, err := canonicalJSON(reportContentEnvelope{
		ReportSchemaVersion:            performancecore.ReportSchemaVersion,
		AnalysisVersion:                performancecore.AnalysisVersion,
		SourceResultContentHash:        identity.Snapshot.BacktestResultContentHash,
		AnnualizationResultContentHash: identity.Snapshot.AnnualizationResultContentHash,
		SettingsHash:                   identity.Snapshot.SettingsHash,
		SummaryHash:                    summaryHash,
		ChartManifestHash:              manifestHash,
	})
	if err != nil {
		return "", err
	}
	return hashBytes(payload), nil
}

func DecodeSummary(raw []byte) (performancecore.Summary, error) {
	var summary performancecore.Summary
	if err := json.Unmarshal(raw, &summary); err != nil {
		return performancecore.Summary{}, err
	}
	return summary, nil
}

func DecodeChartManifest(raw []byte) (ChartManifest, error) {
	var manifest ChartManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ChartManifest{}, err
	}
	return manifest, nil
}
