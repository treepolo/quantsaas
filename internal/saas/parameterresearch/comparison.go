package parameterresearch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"quantsaas/internal/compute"
	saasstore "quantsaas/internal/saas/store"
)

var comparisonBlocks = map[string][]string{
	"candidate": {"parameter_snapshot", "lineage", "analysis_links"},
	"analysis":  {"research_context", "performance_landscape", "robust_regions", "candidate_frontier"},
	"series":    {"comparison_context", "common_manifest", "missing", "differences"},
}

func (s *Service) CandidateComparison(ctx context.Context, userID, candidateID uint) (ComparisonDescriptor, error) {
	var candidate saasstore.RobustCandidate
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", candidateID, userID).First(&candidate).Error; err != nil {
		return ComparisonDescriptor{}, ErrNotFound
	}
	snapshotID := uint(0)
	if candidate.AnalysisSnapshotID != nil {
		snapshotID = *candidate.AnalysisSnapshotID
	}
	name := strings.TrimSpace(candidate.Name)
	if name == "" {
		name = fmt.Sprintf("M 候選 #%d", candidate.ID)
	}
	return ComparisonDescriptor{SourceKind: "m_candidate", SourceID: candidate.ID, SourceVersion: candidate.Version, SnapshotID: snapshotID, ContentHash: candidate.AdoptionUnitHash, CanonicalSubject: fmt.Sprintf("m_candidate:%d:%s", candidate.ID, candidate.Version), DisplayName: name, SourceStatus: candidate.Completeness, Archived: candidate.ArchivedAt != nil, CreatedAt: candidate.CreatedAt.UTC().Format(timeFormat), SourceLink: fmt.Sprintf("/evolution?candidate=%d", candidate.ID), AvailableBlocks: comparisonBlocks["candidate"]}, nil
}

func (s *Service) AnalysisComparison(ctx context.Context, userID, snapshotID uint) (ComparisonDescriptor, error) {
	snapshot, configuration, metadata, err := s.loadAnalysisComparison(ctx, userID, snapshotID)
	if err != nil {
		return ComparisonDescriptor{}, err
	}
	name := strings.TrimSpace(metadata.Name)
	if name == "" {
		name = fmt.Sprintf("M 分析快照 #%d", snapshot.ID)
	}
	return ComparisonDescriptor{SourceKind: "m_analysis_snapshot", SourceID: snapshot.ConfigurationID, SourceVersion: ComparisonSourceVersion, SnapshotID: snapshot.ID, ContentHash: snapshot.ContentHash, DisplayName: name, SourceStatus: snapshot.Completeness, Archived: configuration.ArchivedAt != nil || metadata.ArchivedAt != nil, CreatedAt: snapshot.CreatedAt.UTC().Format(timeFormat), SourceLink: fmt.Sprintf("/evolution?configuration=%d&analysis=%d", configuration.ID, snapshot.ID), AvailableBlocks: comparisonBlocks["analysis"]}, nil
}

func (s *Service) SeriesComparison(ctx context.Context, userID, seriesID, snapshotID uint) (ComparisonDescriptor, error) {
	series, snapshot, err := s.loadSeriesComparison(ctx, userID, seriesID, snapshotID)
	if err != nil {
		return ComparisonDescriptor{}, err
	}
	return ComparisonDescriptor{SourceKind: "m_series_comparison_snapshot", SourceID: series.ID, SourceVersion: snapshot.SchemaVersion, SnapshotID: snapshot.ID, ContentHash: snapshot.ContentHash, DisplayName: series.Name, SourceStatus: snapshot.Eligibility, Archived: series.ArchivedAt != nil, CreatedAt: snapshot.CreatedAt.UTC().Format(timeFormat), SourceLink: fmt.Sprintf("/evolution?series=%d&comparison=%d", series.ID, snapshot.ID), AvailableBlocks: comparisonBlocks["series"]}, nil
}

func (s *Service) CandidateComparisonBlock(ctx context.Context, userID, candidateID uint, blockID, expectedHash string) (ComparisonBlockDescriptor, error) {
	descriptor, err := s.CandidateComparison(ctx, userID, candidateID)
	if err != nil {
		return ComparisonBlockDescriptor{}, err
	}
	if expectedHash != "" && expectedHash != descriptor.ContentHash {
		return ComparisonBlockDescriptor{}, ErrPlanStale
	}
	var candidate saasstore.RobustCandidate
	_ = s.db.WithContext(ctx).First(&candidate, candidateID).Error
	var payload any
	switch blockID {
	case "parameter_snapshot":
		payload = map[string]any{"version": candidate.Version, "adoption_unit_hash": candidate.AdoptionUnitHash, "adoption_unit": json.RawMessage(candidate.AdoptionUnit), "roles": json.RawMessage(candidate.Roles), "configuration_id": candidate.ConfigurationID, "point_id": candidate.PointID}
	case "lineage":
		payload = map[string]any{"lineage": json.RawMessage(candidate.Lineage), "analysis_snapshot_id": candidate.AnalysisSnapshotID, "region_id": candidate.RegionID}
	case "analysis_links":
		var links []saasstore.CandidateAnalysisLink
		if err := s.db.WithContext(ctx).Where("candidate_id = ?", candidate.ID).Order("analysis_kind ASC, version ASC").Find(&links).Error; err != nil {
			return ComparisonBlockDescriptor{}, err
		}
		payload = links
	default:
		return ComparisonBlockDescriptor{}, ErrInvalidRequest
	}
	return makeComparisonBlock("candidate", candidate.ID, blockID, descriptor, candidate.Version, "", payload), nil
}

func (s *Service) AnalysisComparisonBlock(ctx context.Context, userID, snapshotID uint, blockID, expectedHash string) (ComparisonBlockDescriptor, error) {
	descriptor, err := s.AnalysisComparison(ctx, userID, snapshotID)
	if err != nil {
		return ComparisonBlockDescriptor{}, err
	}
	if expectedHash != "" && expectedHash != descriptor.ContentHash {
		return ComparisonBlockDescriptor{}, ErrPlanStale
	}
	snapshot, configuration, metadata, err := s.loadAnalysisComparison(ctx, userID, snapshotID)
	if err != nil {
		return ComparisonBlockDescriptor{}, err
	}
	var payload any
	switch blockID {
	case "research_context":
		payload = map[string]any{"configuration_id": configuration.ID, "name": metadata.Name, "configuration_hash": configuration.ConfigHash, "dataset_hash": configuration.DatasetHash, "parameter_space_hash": configuration.ParameterSpaceHash, "strategy_id": configuration.StrategyID, "instrument_id": configuration.InstrumentID, "data_source": configuration.DataSource, "interval": configuration.Interval, "start_time_ms": configuration.StartTimeMs, "end_time_ms": configuration.EndTimeMs, "execution_mode": configuration.ExecutionMode, "dynamic_mode": configuration.DynamicMode}
	case "performance_landscape":
		payload = json.RawMessage(snapshot.Summary)
	case "robust_regions":
		var regions []saasstore.RobustRegion
		if err := s.db.WithContext(ctx).Where("analysis_snapshot_id = ?", snapshot.ID).Order("component_id ASC").Find(&regions).Error; err != nil {
			return ComparisonBlockDescriptor{}, err
		}
		payload = regions
	case "candidate_frontier":
		var candidates []saasstore.RobustCandidate
		if err := s.db.WithContext(ctx).Where("analysis_snapshot_id = ?", snapshot.ID).Order("id ASC").Find(&candidates).Error; err != nil {
			return ComparisonBlockDescriptor{}, err
		}
		payload = candidates
	default:
		return ComparisonBlockDescriptor{}, ErrInvalidRequest
	}
	return makeComparisonBlock("analysis", snapshot.ID, blockID, descriptor, snapshot.SchemaVersion, snapshot.JAnalysisVersion, payload), nil
}

func (s *Service) SeriesComparisonBlock(ctx context.Context, userID, seriesID, snapshotID uint, blockID, expectedHash string) (ComparisonBlockDescriptor, error) {
	descriptor, err := s.SeriesComparison(ctx, userID, seriesID, snapshotID)
	if err != nil {
		return ComparisonBlockDescriptor{}, err
	}
	if expectedHash != "" && expectedHash != descriptor.ContentHash {
		return ComparisonBlockDescriptor{}, ErrPlanStale
	}
	series, snapshot, err := s.loadSeriesComparison(ctx, userID, seriesID, snapshotID)
	if err != nil {
		return ComparisonBlockDescriptor{}, err
	}
	var payload any
	switch blockID {
	case "comparison_context":
		payload = map[string]any{"series_id": series.ID, "eligibility": snapshot.Eligibility, "eligibility_reasons": json.RawMessage(snapshot.EligibilityReasons), "member_hashes": json.RawMessage(snapshot.MemberHashes), "common_background_hash": series.CommonBackgroundHash, "common_schema_hash": series.CommonSchemaHash}
	case "common_manifest":
		payload = json.RawMessage(snapshot.CommonManifest)
	case "missing":
		payload = json.RawMessage(snapshot.Missing)
	case "differences":
		payload = json.RawMessage(snapshot.Differences)
	default:
		return ComparisonBlockDescriptor{}, ErrInvalidRequest
	}
	return makeComparisonBlock("series", snapshot.ID, blockID, descriptor, snapshot.SchemaVersion, "", payload), nil
}

func (s *Service) loadAnalysisComparison(ctx context.Context, userID, snapshotID uint) (saasstore.ResearchAnalysisSnapshot, saasstore.ResearchConfiguration, saasstore.ResearchConfigurationMetadata, error) {
	var snapshot saasstore.ResearchAnalysisSnapshot
	if err := s.db.WithContext(ctx).First(&snapshot, snapshotID).Error; err != nil {
		return snapshot, saasstore.ResearchConfiguration{}, saasstore.ResearchConfigurationMetadata{}, ErrNotFound
	}
	var configuration saasstore.ResearchConfiguration
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", snapshot.ConfigurationID, userID).First(&configuration).Error; err != nil {
		return snapshot, configuration, saasstore.ResearchConfigurationMetadata{}, ErrNotFound
	}
	var metadata saasstore.ResearchConfigurationMetadata
	_ = s.db.WithContext(ctx).Where("configuration_id = ?", configuration.ID).First(&metadata).Error
	return snapshot, configuration, metadata, nil
}

func (s *Service) loadSeriesComparison(ctx context.Context, userID, seriesID, snapshotID uint) (saasstore.ResearchSeries, saasstore.ResearchComparisonSnapshot, error) {
	var series saasstore.ResearchSeries
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", seriesID, userID).First(&series).Error; err != nil {
		return series, saasstore.ResearchComparisonSnapshot{}, ErrNotFound
	}
	var snapshot saasstore.ResearchComparisonSnapshot
	if err := s.db.WithContext(ctx).Where("id = ? AND series_id = ?", snapshotID, series.ID).First(&snapshot).Error; err != nil {
		return series, snapshot, ErrNotFound
	}
	return series, snapshot, nil
}

func makeComparisonBlock(kind string, id uint, blockID string, source ComparisonDescriptor, schemaVersion, formulaVersion string, payload any) ComparisonBlockDescriptor {
	raw, _ := compute.CanonicalJSON(payload)
	contextRaw, _ := compute.CanonicalJSON(map[string]any{"source_kind": source.SourceKind, "source_id": source.SourceID, "source_version": source.SourceVersion, "snapshot_id": source.SnapshotID, "source_content_hash": source.ContentHash})
	return ComparisonBlockDescriptor{BlockID: blockID, BlockKind: kind + "." + blockID, SchemaID: "quantsaas." + kind + "." + blockID, SchemaVersion: schemaVersion, FormulaVersion: formulaVersion, Axes: []string{}, ContextFingerprint: contextRaw, ContentHash: compute.HashBytes(raw), Availability: "available", PayloadLocator: fmt.Sprintf("%s:%d:%s", kind, id, blockID), Payload: raw}
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"
