package klineinverse

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	compute "quantsaas/internal/compute"
	core "quantsaas/internal/klineinverse"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type batchManifest struct {
	SchemaVersion string            `json:"schema_version"`
	StudyHash     string            `json:"study_hash"`
	BatchType     string            `json:"batch_type"`
	Budget        int               `json:"budget"`
	RNGStart      int64             `json:"rng_start"`
	RNGEnd        int64             `json:"rng_end"`
	AnchorPathID  uint              `json:"anchor_path_id,omitempty"`
	Operations    []string          `json:"operations,omitempty"`
	Scope         string            `json:"scope,omitempty"`
	MinLength     int               `json:"min_length,omitempty"`
	MaxLength     int               `json:"max_length,omitempty"`
	Amplitude     float64           `json:"amplitude,omitempty"`
	Compatibility map[string]string `json:"compatibility"`
}

func (s *Service) PlanExtension(ctx context.Context, userID, studyID uint, request ExtensionPlanRequest) (BatchPlanResponse, error) {
	study, canonical, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return BatchPlanResponse{}, err
	}
	if request.AdditionalBudget < 5 || request.AdditionalBudget > 100000 || study.ArchivedAt != nil {
		return BatchPlanResponse{}, ErrInvalidRequest
	}
	return s.planAppendBatch(ctx, userID, study, canonical, "extension", request.AdditionalBudget, ProbePlanRequest{})
}

func (s *Service) PlanProbe(ctx context.Context, userID, studyID uint, request ProbePlanRequest) (BatchPlanResponse, error) {
	study, canonical, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return BatchPlanResponse{}, err
	}
	if request.AnchorPathID == 0 || request.Budget < 1 || request.Budget > 100000 || request.Amplitude <= 0 || request.Amplitude > 1 || request.MinLength < 1 || request.MaxLength < request.MinLength || request.MaxLength > canonical.WarmupLength+canonical.EvaluationLength || study.ArchivedAt != nil {
		return BatchPlanResponse{}, ErrInvalidRequest
	}
	if request.Scope != "W" && request.Scope != "H" && request.Scope != "both" {
		return BatchPlanResponse{}, ErrInvalidRequest
	}
	request.Operations = cleanStrings(request.Operations)
	if len(request.Operations) == 0 {
		return BatchPlanResponse{}, ErrInvalidRequest
	}
	for _, operation := range request.Operations {
		if !contains(core.OperationOrder, operation) || operation == core.OperationGlobal {
			return BatchPlanResponse{}, ErrInvalidRequest
		}
	}
	var anchor saasstore.KlineInverseEvaluation
	if err := s.db.WithContext(ctx).Where("study_id = ? AND path_id = ? AND permanent = ? AND pass_a = ?", study.ID, request.AnchorPathID, true, true).First(&anchor).Error; err != nil {
		return BatchPlanResponse{}, ErrNotFound
	}
	return s.planAppendBatch(ctx, userID, study, canonical, "probe", request.Budget, request)
}

func (s *Service) planAppendBatch(ctx context.Context, userID uint, study saasstore.KlineInverseStudy, canonical StudyCanonical, batchType string, budget int, probe ProbePlanRequest) (BatchPlanResponse, error) {
	var calibration saasstore.KlineInverseCalibration
	if err := s.db.WithContext(ctx).Where("study_id = ?", study.ID).First(&calibration).Error; err != nil {
		return BatchPlanResponse{}, fmt.Errorf("特徵空間校準尚未完成: %w", err)
	}
	tx := s.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return BatchPlanResponse{}, tx.Error
	}
	defer tx.Rollback()
	// A study-row lock serializes append planning so ordinal and counter-RNG
	// ranges cannot overlap when requests arrive concurrently.
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&saasstore.KlineInverseStudy{}, study.ID).Error; err != nil {
		return BatchPlanResponse{}, err
	}
	var ordinal int
	var rngStart int64
	if err := tx.Model(&saasstore.KlineInverseBatch{}).Where("study_id = ?", study.ID).Select("COALESCE(MAX(ordinal), -1) + 1").Scan(&ordinal).Error; err != nil {
		return BatchPlanResponse{}, err
	}
	if err := tx.Model(&saasstore.KlineInverseBatch{}).Where("study_id = ?", study.ID).Select("COALESCE(MAX(rng_end), 0)").Scan(&rngStart).Error; err != nil {
		return BatchPlanResponse{}, err
	}
	manifest := batchManifest{
		SchemaVersion: BatchSchemaVersion, StudyHash: study.StudyHash, BatchType: batchType,
		Budget: budget, RNGStart: rngStart, RNGEnd: rngStart + int64(budget),
		AnchorPathID: probe.AnchorPathID, Operations: probe.Operations, Scope: probe.Scope,
		MinLength: probe.MinLength, MaxLength: probe.MaxLength, Amplitude: probe.Amplitude,
		Compatibility: map[string]string{
			"study": study.CanonicalHash, "calibration": calibration.ContentHash,
			"search": core.SearchVersion, "variation": core.VariationVersion, "rng": core.RNGVersion,
		},
	}
	manifestRaw, err := compute.CanonicalJSON(manifest)
	if err != nil {
		return BatchPlanResponse{}, err
	}
	manifestHash := compute.HashBytes(manifestRaw)
	compatibilityRaw, _ := compute.CanonicalJSON(manifest.Compatibility)
	batchKey := "p12-batch:" + study.StudyHash + ":" + batchType + ":" + manifestHash
	batch := saasstore.KlineInverseBatch{
		StudyID: study.ID, Ordinal: ordinal, BatchKey: batchKey, BatchType: batchType,
		SchemaVersion: BatchSchemaVersion, ManifestHash: manifestHash, Manifest: manifestRaw,
		CompatibilityHash: compute.HashBytes(compatibilityRaw),
		Budget:            budget, RNGStart: rngStart, RNGEnd: rngStart + int64(budget), Status: "planned",
	}
	created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "batch_key"}}, DoNothing: true}).Create(&batch)
	if created.Error != nil {
		return BatchPlanResponse{}, created.Error
	}
	if created.RowsAffected == 0 {
		if err := tx.Where("batch_key = ? AND study_id = ?", batchKey, study.ID).First(&batch).Error; err != nil {
			return BatchPlanResponse{}, err
		}
	}
	if batchType == "probe" {
		probeCompatibilityRaw, _ := compute.CanonicalJSON(map[string]any{"study_hash": study.StudyHash, "calibration_hash": calibration.ContentHash, "anchor": probe.AnchorPathID, "scope": probe.Scope, "operations": probe.Operations, "min_length": probe.MinLength, "max_length": probe.MaxLength, "amplitude": probe.Amplitude})
		probeRow := saasstore.KlineInverseProbeBatch{StudyID: study.ID, BatchID: batch.ID, AnchorPathID: probe.AnchorPathID, CompatibilityHash: compute.HashBytes(probeCompatibilityRaw), ManifestHash: manifestHash, Manifest: manifestRaw, Status: "planned"}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "batch_id"}}, DoNothing: true}).Create(&probeRow).Error; err != nil {
			return BatchPlanResponse{}, err
		}
	}
	if err := tx.Commit().Error; err != nil {
		return BatchPlanResponse{}, err
	}
	spec, err := s.batchComputeSpec(study, batch)
	if err != nil {
		return BatchPlanResponse{}, err
	}
	preview, err := s.computeTasks.Preview(ctx, userID, spec)
	if err != nil {
		return BatchPlanResponse{}, err
	}
	return BatchPlanResponse{BatchID: batch.ID, BatchType: batchType, Plan: preview, ManifestHash: manifestHash, CompatibilityHash: batch.CompatibilityHash, BacktestEvaluations: budget}, nil
}

func (s *Service) StartAppendBatch(ctx context.Context, userID, studyID uint, request BatchStartRequest) (StudyDescriptor, error) {
	study, _, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return StudyDescriptor{}, err
	}
	var batch saasstore.KlineInverseBatch
	if err := s.db.WithContext(ctx).Where("id = ? AND study_id = ? AND batch_type IN ?", request.BatchID, study.ID, []string{"extension", "probe"}).First(&batch).Error; err != nil {
		return StudyDescriptor{}, ErrNotFound
	}
	if batch.Status != "planned" || batch.ComputeTaskID != nil || strings.TrimSpace(request.PlanKey) == "" {
		return StudyDescriptor{}, ErrInvalidRequest
	}
	spec, err := s.batchComputeSpec(study, batch)
	if err != nil {
		return StudyDescriptor{}, err
	}
	preview, err := s.computeTasks.Preview(ctx, userID, spec)
	if err != nil {
		return StudyDescriptor{}, err
	}
	if preview.PlanKey != request.PlanKey {
		return StudyDescriptor{}, ErrPlanStale
	}
	task, err := s.computeTasks.Create(ctx, userID, spec, request.ConfirmSoftLimit)
	if err != nil {
		return StudyDescriptor{}, err
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&batch).Updates(map[string]any{"compute_task_id": task.ID, "status": task.Status}).Error; err != nil {
			return err
		}
		if batch.BatchType == "probe" {
			if err := tx.Model(&saasstore.KlineInverseProbeBatch{}).Where("batch_id = ?", batch.ID).Update("status", task.Status).Error; err != nil {
				return err
			}
		}
		return tx.Model(&study).Updates(map[string]any{"status": "waiting", "current_stage": batch.BatchType}).Error
	}); err != nil {
		return StudyDescriptor{}, err
	}
	if task.Status != compute.TaskStatusCompleted {
		if _, err := s.computeTasks.StartTask(ctx, userID, task.ID); err != nil {
			return StudyDescriptor{}, err
		}
	}
	return s.Get(ctx, userID, study.ID)
}

func (s *Service) batchComputeSpec(study saasstore.KlineInverseStudy, batch saasstore.KlineInverseBatch) (computetask.CreateSpec, error) {
	input, err := compute.CanonicalJSON(SearchExecutionInput{StudyID: study.ID, BatchID: batch.ID})
	if err != nil {
		return computetask.CreateSpec{}, err
	}
	seed := study.RootSeed
	return computetask.CreateSpec{
		TaskType: "p12-kline-inverse-" + batch.BatchType, Title: study.Name + " · " + batch.BatchType,
		ExecutorType: SearchExecutorType, Settings: map[string]any{"study_id": study.ID, "batch_id": batch.ID, "manifest_hash": batch.ManifestHash},
		ResearchSettingID: strconv.FormatUint(uint64(study.ID), 10), ResearchSettingHash: study.CanonicalHash,
		RNG:   compute.RNGSpec{Algorithm: "counter", Version: core.RNGVersion, RootSeed: &seed},
		Items: []compute.ManifestItemInput{{Key: batch.BatchType, CacheKey: "p12-search-batch:" + batch.ManifestHash, Input: input, EstimatedUnits: int64(batch.Budget)}},
	}, nil
}
