package parameterresearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	core "quantsaas/internal/parameterresearch"
	robust "quantsaas/internal/robustness"
	robustnesssvc "quantsaas/internal/saas/robustness"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) AnalyzeRun(ctx context.Context, userID, runID uint, req AnalysisRequest) (AnalysisDescriptor, error) {
	run, configuration, canonical, err := s.loadRun(ctx, userID, runID)
	if err != nil {
		return AnalysisDescriptor{}, err
	}
	if err := s.syncRun(ctx, userID, &run); err != nil {
		return AnalysisDescriptor{}, err
	}
	var rows []saasstore.ResearchEvaluationPoint
	if err := s.db.WithContext(ctx).Where("configuration_id = ? AND status = ? AND backtest_result_id IS NOT NULL", configuration.ID, "completed").Order("vector_hash ASC").Find(&rows).Error; err != nil {
		return AnalysisDescriptor{}, err
	}
	if len(rows) == 0 {
		return AnalysisDescriptor{}, robustnesssvc.ErrStudyNotReady
	}
	points := make([]robustnesssvc.ImportPoint, 0, len(rows))
	pointHashes := make([]string, 0, len(rows))
	for _, row := range rows {
		var coordinates []int
		var parameters map[string]float64
		if json.Unmarshal(row.Coordinates, &coordinates) != nil || json.Unmarshal(row.Parameters, &parameters) != nil {
			return AnalysisDescriptor{}, ErrInvalidRequest
		}
		points = append(points, robustnesssvc.ImportPoint{ID: row.VectorHash, Kind: robust.PointActual, Coordinates: coordinates, Parameters: parameters, BacktestResultID: *row.BacktestResultID, SourceStage: "p10", SamplingBatch: "saved"})
		pointHashes = append(pointHashes, row.VectorHash)
	}
	center, _ := pointForCoordinates(canonical.ParameterSpace, canonical.BaseCoordinates)
	study, err := s.robustness.Import(ctx, userID, robustnesssvc.ImportStudyRequest{Name: "M：" + configuration.ConfigHash[:12], ResearchSettingID: fmt.Sprintf("p10-configuration:%d", configuration.ID), ResearchSettingHash: configuration.ConfigHash, ParameterSpace: canonical.ParameterSpace, CenterPointKey: center.VectorHash, Points: points})
	if err != nil {
		return AnalysisDescriptor{}, err
	}
	analysis, err := s.robustness.Analyze(ctx, userID, study.ID, robustnesssvc.AnalyzeRequest{Metric: req.Metric, Radii: req.Radii})
	if err != nil {
		return AnalysisDescriptor{}, err
	}
	sort.Strings(pointHashes)
	pointRaw, _ := compute.CanonicalJSON(pointHashes)
	pointSetHash := compute.HashBytes(pointRaw)
	summaryRaw, _ := compute.CanonicalJSON(analysis.Result)
	identityRaw, _ := compute.CanonicalJSON(map[string]any{"configuration_hash": configuration.ConfigHash, "point_set_hash": pointSetHash, "j_content_hash": analysis.ContentHash, "version": core.AnalysisSnapshotVersion})
	snapshotKey := "p10-analysis:" + compute.HashBytes(identityRaw)
	model := saasstore.ResearchAnalysisSnapshot{ConfigurationID: configuration.ID, SnapshotKey: snapshotKey, SchemaVersion: core.AnalysisSnapshotVersion, PointSetHash: pointSetHash, MetricsVersion: robust.MetricsVersion, JAnalysisVersion: robust.AnalysisVersion, RobustnessStudyID: study.ID, RobustnessSnapshotID: analysis.ID, Completeness: analysisCompleteness(analysis.Result), ContentHash: compute.HashBytes(summaryRaw), Summary: summaryRaw}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "snapshot_key"}}, DoNothing: true}).Create(&model)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return tx.Where("snapshot_key = ?", snapshotKey).First(&model).Error
		}
		pointByHash := map[string]uint{}
		for _, row := range rows {
			pointByHash[row.VectorHash] = row.ID
		}
		for _, region := range analysis.Result.Regions {
			boundaryRaw, _ := compute.CanonicalJSON(map[string]any{"missing_coordinates": analysis.Result.MissingCoordinates})
			regionModel := saasstore.RobustRegion{AnalysisSnapshotID: model.ID, ComponentID: region.ID, Completeness: regionCompleteness(region), Boundary: boundaryRaw, Lineage: saasstore.JSONB(`[]`)}
			if err := tx.Create(&regionModel).Error; err != nil {
				return err
			}
			links := make([]saasstore.RobustRegionPoint, 0, len(region.PointIDs))
			for _, pointID := range region.PointIDs {
				if id := pointByHash[pointID]; id != 0 {
					links = append(links, saasstore.RobustRegionPoint{RegionID: regionModel.ID, PointID: id})
				}
			}
			if len(links) > 0 {
				if err := tx.Create(&links).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return AnalysisDescriptor{}, err
	}
	return AnalysisDescriptor{ID: model.ID, ConfigurationID: model.ConfigurationID, PointSetHash: model.PointSetHash, Completeness: model.Completeness, ContentHash: model.ContentHash, RobustnessStudyID: model.RobustnessStudyID, RobustnessSnapshotID: model.RobustnessSnapshotID, Result: summaryRaw}, nil
}

func analysisCompleteness(result robust.AnalysisResult) string {
	if len(result.MissingCoordinates) > 0 {
		return "partial"
	}
	for _, region := range result.Regions {
		for _, geometry := range region.Geometries {
			if !geometry.GuaranteedBoxExact || !geometry.AxisFailureExact {
				return "partial"
			}
		}
	}
	return "complete"
}
func regionCompleteness(region robust.ConnectedRegion) string {
	for _, geometry := range region.Geometries {
		if geometry.Completeness == "provisional" || !geometry.GuaranteedBoxExact || !geometry.AxisFailureExact {
			return "provisional"
		}
	}
	return "formal"
}

func (s *Service) DeriveCandidates(ctx context.Context, userID, snapshotID uint) ([]CandidateDescriptor, error) {
	var snapshot saasstore.ResearchAnalysisSnapshot
	if err := s.db.WithContext(ctx).First(&snapshot, snapshotID).Error; err != nil {
		return nil, ErrNotFound
	}
	var configuration saasstore.ResearchConfiguration
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", snapshot.ConfigurationID, userID).First(&configuration).Error; err != nil {
		return nil, ErrNotFound
	}
	var result robust.AnalysisResult
	if err := json.Unmarshal(snapshot.Summary, &result); err != nil {
		return nil, err
	}
	var points []saasstore.ResearchEvaluationPoint
	if err := s.db.WithContext(ctx).Where("configuration_id = ?", configuration.ID).Find(&points).Error; err != nil {
		return nil, err
	}
	pointByHash := map[string]saasstore.ResearchEvaluationPoint{}
	for _, point := range points {
		pointByHash[point.VectorHash] = point
	}
	var regions []saasstore.RobustRegion
	if err := s.db.WithContext(ctx).Where("analysis_snapshot_id = ?", snapshot.ID).Find(&regions).Error; err != nil {
		return nil, err
	}
	regionByComponent := map[string]saasstore.RobustRegion{}
	for _, region := range regions {
		regionByComponent[region.ComponentID] = region
	}
	for _, regionResult := range result.Regions {
		region := regionByComponent[regionResult.ID]
		for _, proposal := range regionResult.Proposals {
			point, ok := pointByHash[proposal.PointID]
			if !ok {
				continue
			}
			completeness := "formal"
			if proposal.Provisional {
				completeness = "provisional"
			}
			if _, err := s.persistCandidate(ctx, userID, configuration, point, &snapshot, &region, "derived", completeness, proposal.Roles); err != nil {
				return nil, err
			}
		}
	}
	return s.ListCandidates(ctx, userID, configuration.ID)
}

func (s *Service) CreateManualCandidate(ctx context.Context, userID, pointID uint) (CandidateDescriptor, error) {
	var point saasstore.ResearchEvaluationPoint
	if err := s.db.WithContext(ctx).Where("id = ? AND status = ?", pointID, "completed").First(&point).Error; err != nil {
		return CandidateDescriptor{}, ErrNotFound
	}
	var configuration saasstore.ResearchConfiguration
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", point.ConfigurationID, userID).First(&configuration).Error; err != nil {
		return CandidateDescriptor{}, ErrNotFound
	}
	return s.persistCandidate(ctx, userID, configuration, point, nil, nil, "manual", "manual", []string{"manual"})
}

func (s *Service) persistCandidate(ctx context.Context, userID uint, configuration saasstore.ResearchConfiguration, point saasstore.ResearchEvaluationPoint, snapshot *saasstore.ResearchAnalysisSnapshot, region *saasstore.RobustRegion, source, completeness string, roles []string) (CandidateDescriptor, error) {
	roles = cleanStrings(roles)
	identity := map[string]any{"configuration_id": configuration.ID, "point_id": point.ID, "snapshot_id": uint(0), "source": source, "version": core.CandidateVersion}
	if snapshot != nil {
		identity["snapshot_id"] = snapshot.ID
	}
	identityRaw, _ := compute.CanonicalJSON(identity)
	candidateKey := "p10-candidate:" + compute.HashBytes(identityRaw)
	adoptionRaw, adoptionHash, err := s.candidateAdoptionUnit(ctx, userID, configuration, point)
	if err != nil {
		return CandidateDescriptor{}, err
	}
	rolesRaw, _ := compute.CanonicalJSON(roles)
	model := saasstore.RobustCandidate{OwnerUserID: userID, ConfigurationID: configuration.ID, PointID: point.ID, CandidateKey: candidateKey, Version: core.CandidateVersion, SourceKind: source, Completeness: completeness, Roles: rolesRaw, AdoptionUnitHash: adoptionHash, AdoptionUnit: saasstore.JSONB(adoptionRaw), Tags: saasstore.JSONB(`[]`), Lineage: saasstore.JSONB(`[]`)}
	if snapshot != nil {
		model.AnalysisSnapshotID = &snapshot.ID
	}
	if region != nil {
		model.RegionID = &region.ID
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "candidate_key"}}, DoNothing: true}).Create(&model)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Where("candidate_key = ?", candidateKey).First(&model).Error; err != nil {
				return err
			}
			var oldRoles []string
			_ = json.Unmarshal(model.Roles, &oldRoles)
			rolesRaw, _ = compute.CanonicalJSON(cleanStrings(append(oldRoles, roles...)))
			if err := tx.Model(&model).Update("roles", rolesRaw).Error; err != nil {
				return err
			}
		}
		for _, kind := range []string{"G", "H", "L", "C"} {
			link := saasstore.CandidateAnalysisLink{CandidateID: model.ID, AnalysisKind: kind, Version: core.AnalysisLinkVersion, Status: "not_calculated", PartialSnapshot: saasstore.JSONB(`{}`)}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&link).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return CandidateDescriptor{}, err
	}
	return s.GetCandidate(ctx, userID, model.ID)
}

func (s *Service) candidateAdoptionUnit(ctx context.Context, userID uint, configuration saasstore.ResearchConfiguration, point saasstore.ResearchEvaluationPoint) (json.RawMessage, string, error) {
	var canonical ConfigurationCanonical
	if err := json.Unmarshal(configuration.Canonical, &canonical); err != nil {
		return nil, "", err
	}
	var parameters map[string]float64
	if err := json.Unmarshal(point.Parameters, &parameters); err != nil {
		return nil, "", err
	}
	var gene saasstore.GeneRecord
	if err := s.db.WithContext(ctx).First(&gene, canonical.GenomeID).Error; err != nil {
		return nil, "", err
	}
	base := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
	var unit any
	if canonical.DynamicPackage == nil {
		chromosome, err := robust.ChromosomeWithValues(base.Chromosome, parameters)
		if err != nil {
			return nil, "", err
		}
		base.Chromosome = chromosome
		unit = base
	} else {
		planned := core.PlannedPoint{Parameters: parameters}
		input, err := s.dynamicInputForPoint(ctx, userID, canonical, planned, base)
		if err != nil {
			return nil, "", err
		}
		unit = map[string]any{"schema_version": "p10-dynamic-adoption-unit-v1", "model_artifact_hash": canonical.DynamicPackage.ArtifactSetHash, "prediction_snapshot_hash": canonical.DynamicPackage.PredictionSnapshotHash, "policy_hash": input.PolicyHash, "policy_bundle": input.PolicyOverride, "base_parameters": base, "backtest": canonical.Backtest, "configuration_hash": configuration.ConfigHash}
	}
	raw, err := compute.CanonicalJSON(unit)
	if err != nil {
		return nil, "", err
	}
	return raw, compute.HashBytes(raw), nil
}

func (s *Service) ListCandidates(ctx context.Context, userID, configurationID uint) ([]CandidateDescriptor, error) {
	var rows []saasstore.RobustCandidate
	query := s.db.WithContext(ctx).Where("owner_user_id = ?", userID)
	if configurationID != 0 {
		query = query.Where("configuration_id = ?", configurationID)
	}
	if err := query.Order("created_at DESC,id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]CandidateDescriptor, 0, len(rows))
	for _, row := range rows {
		descriptor, err := s.describeCandidate(ctx, row)
		if err != nil {
			return nil, err
		}
		result = append(result, descriptor)
	}
	return result, nil
}
func (s *Service) GetCandidate(ctx context.Context, userID, id uint) (CandidateDescriptor, error) {
	var row saasstore.RobustCandidate
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, userID).First(&row).Error; err != nil {
		return CandidateDescriptor{}, ErrNotFound
	}
	return s.describeCandidate(ctx, row)
}
func (s *Service) ArchiveCandidate(ctx context.Context, userID, id uint) (CandidateDescriptor, error) {
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&saasstore.RobustCandidate{}).Where("id = ? AND owner_user_id = ?", id, userID).Update("archived_at", now)
	if result.Error != nil {
		return CandidateDescriptor{}, result.Error
	}
	if result.RowsAffected == 0 {
		return CandidateDescriptor{}, ErrNotFound
	}
	return s.GetCandidate(ctx, userID, id)
}
func (s *Service) describeCandidate(ctx context.Context, row saasstore.RobustCandidate) (CandidateDescriptor, error) {
	var roles, tags []string
	_ = json.Unmarshal(row.Roles, &roles)
	_ = json.Unmarshal(row.Tags, &tags)
	descriptor := CandidateDescriptor{ID: row.ID, ConfigurationID: row.ConfigurationID, PointID: row.PointID, AnalysisSnapshotID: row.AnalysisSnapshotID, RegionID: row.RegionID, SourceKind: row.SourceKind, Completeness: row.Completeness, Roles: roles, AdoptionUnitHash: row.AdoptionUnitHash, Name: row.Name, Notes: row.Notes, Tags: tags, Archived: row.ArchivedAt != nil}
	var geneLink saasstore.CandidateGeneLink
	if err := s.db.WithContext(ctx).Where("candidate_id = ?", row.ID).First(&geneLink).Error; err == nil {
		descriptor.GeneRecordID = &geneLink.GeneRecordID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return descriptor, err
	}
	var links []saasstore.CandidateAnalysisLink
	if err := s.db.WithContext(ctx).Where("candidate_id = ?", row.ID).Order("analysis_kind ASC").Find(&links).Error; err != nil {
		return descriptor, err
	}
	for _, link := range links {
		descriptor.AnalysisLinks = append(descriptor.AnalysisLinks, AnalysisLinkDescriptor{Kind: link.AnalysisKind, Version: link.Version, Status: link.Status, TaskID: link.TaskID, SourceID: link.SourceID, SourceVersion: link.SourceVersion, SourceContentHash: link.SourceContentHash, PartialSnapshot: append(json.RawMessage(nil), link.PartialSnapshot...), ErrorMessage: link.ErrorMessage})
	}
	return descriptor, nil
}

func (s *Service) UpdateAnalysisLink(ctx context.Context, userID, candidateID uint, kind string, req UpdateAnalysisLinkRequest) (CandidateDescriptor, error) {
	kind = strings.ToUpper(strings.TrimSpace(kind))
	if !contains([]string{"G", "H", "L", "C"}, kind) || !contains([]string{"not_calculated", "running", "partially_completed", "completed", "failed", "cancelled", "not_applicable"}, req.Status) {
		return CandidateDescriptor{}, ErrInvalidRequest
	}
	var candidate saasstore.RobustCandidate
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", candidateID, userID).First(&candidate).Error; err != nil {
		return CandidateDescriptor{}, ErrNotFound
	}
	partial := saasstore.JSONB(`{}`)
	if len(req.PartialSnapshot) > 0 {
		canonical, err := compute.CanonicalRawJSON(req.PartialSnapshot)
		if err != nil {
			return CandidateDescriptor{}, ErrInvalidRequest
		}
		partial = canonical
	}
	updates := map[string]any{"status": req.Status, "task_id": req.TaskID, "source_id": strings.TrimSpace(req.SourceID), "source_version": strings.TrimSpace(req.SourceVersion), "source_content_hash": strings.TrimSpace(req.SourceContentHash), "partial_snapshot": partial, "error_message": strings.TrimSpace(req.ErrorMessage)}
	result := s.db.WithContext(ctx).Model(&saasstore.CandidateAnalysisLink{}).Where("candidate_id = ? AND analysis_kind = ? AND version = ?", candidate.ID, kind, core.AnalysisLinkVersion).Updates(updates)
	if result.Error != nil {
		return CandidateDescriptor{}, result.Error
	}
	if result.RowsAffected == 0 {
		return CandidateDescriptor{}, ErrNotFound
	}
	return s.GetCandidate(ctx, userID, candidate.ID)
}

func (s *Service) ExportCandidate(ctx context.Context, userID, candidateID uint) (CandidateDescriptor, error) {
	var candidate saasstore.RobustCandidate
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", candidateID, userID).First(&candidate).Error; err != nil {
		return CandidateDescriptor{}, ErrNotFound
	}
	var existing saasstore.CandidateGeneLink
	if err := s.db.WithContext(ctx).Where("candidate_id = ?", candidate.ID).First(&existing).Error; err == nil {
		return s.GetCandidate(ctx, userID, candidate.ID)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return CandidateDescriptor{}, err
	}
	var configuration saasstore.ResearchConfiguration
	if err := s.db.WithContext(ctx).First(&configuration, candidate.ConfigurationID).Error; err != nil {
		return CandidateDescriptor{}, err
	}
	var point saasstore.ResearchEvaluationPoint
	if err := s.db.WithContext(ctx).First(&point, candidate.PointID).Error; err != nil {
		return CandidateDescriptor{}, err
	}
	searchRaw, _ := compute.CanonicalJSON(map[string]any{"source": "parameter_research", "candidate_id": candidate.ID, "candidate_version": candidate.Version, "configuration_hash": configuration.ConfigHash, "adoption_unit_hash": candidate.AdoptionUnitHash})
	score, maxDrawdown := 0.0, 0.0
	var metrics robust.RelativeMetrics
	if json.Unmarshal(point.Metrics, &metrics) == nil {
		score = metrics.PerformanceDrawdown
	}
	if point.BacktestResultID != nil {
		var summary saasstore.BacktestResultSummary
		if s.db.WithContext(ctx).Where("backtest_result_id = ?", *point.BacktestResultID).First(&summary).Error == nil {
			maxDrawdown = summary.MaxDrawdown
		}
	}
	gene := saasstore.GeneRecord{StrategyID: configuration.StrategyID, InstrumentID: configuration.InstrumentID, DataSource: configuration.DataSource, Interval: configuration.Interval, ExecutionMode: configuration.ExecutionMode, Role: saasstore.GeneRoleChallenger, Name: candidate.Name, Notes: candidate.Notes, Tags: candidate.Tags, SearchConfig: searchRaw, ParamPack: candidate.AdoptionUnit, ScoreTotal: score, MaxDrawdown: maxDrawdown, WindowScore: saasstore.JSONB(`{}`)}
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&gene).Error; err != nil {
			return err
		}
		link := saasstore.CandidateGeneLink{CandidateID: candidate.ID, GeneRecordID: gene.ID, CandidateVersion: candidate.Version, ImportedAt: now, PromotionAudit: saasstore.JSONB(`[]`)}
		return tx.Create(&link).Error
	})
	if err != nil {
		return CandidateDescriptor{}, err
	}
	return s.GetCandidate(ctx, userID, candidate.ID)
}

func (s *Service) PromoteCandidate(ctx context.Context, userID, candidateID uint) (CandidateDescriptor, error) {
	if _, err := s.ExportCandidate(ctx, userID, candidateID); err != nil {
		return CandidateDescriptor{}, err
	}
	var candidate saasstore.RobustCandidate
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", candidateID, userID).First(&candidate).Error; err != nil {
		return CandidateDescriptor{}, ErrNotFound
	}
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var link saasstore.CandidateGeneLink
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("candidate_id = ?", candidate.ID).First(&link).Error; err != nil {
			return err
		}
		var gene saasstore.GeneRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND role IN ?", link.GeneRecordID, []string{saasstore.GeneRoleChallenger, saasstore.GeneRoleRetired, saasstore.GeneRoleChampion}).First(&gene).Error; err != nil {
			return err
		}
		if err := tx.Model(&saasstore.GeneRecord{}).Where("strategy_id = ? AND instrument_id = ? AND data_source = ? AND interval = ? AND execution_mode = ? AND role = ? AND id <> ?", gene.StrategyID, gene.InstrumentID, gene.DataSource, gene.Interval, gene.ExecutionMode, saasstore.GeneRoleChampion, gene.ID).Update("role", saasstore.GeneRoleRetired).Error; err != nil {
			return err
		}
		if err := tx.Model(&gene).Updates(map[string]any{"role": saasstore.GeneRoleChampion, "activated_at": now}).Error; err != nil {
			return err
		}
		var audit []map[string]any
		_ = json.Unmarshal(link.PromotionAudit, &audit)
		audit = append(audit, map[string]any{"promoted_at": now.UTC().Format(time.RFC3339), "gene_record_id": gene.ID})
		auditRaw, _ := compute.CanonicalJSON(audit)
		return tx.Model(&link).Updates(map[string]any{"last_promoted_at": now, "promotion_audit": auditRaw}).Error
	})
	if err != nil {
		return CandidateDescriptor{}, err
	}
	return s.GetCandidate(ctx, userID, candidate.ID)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
