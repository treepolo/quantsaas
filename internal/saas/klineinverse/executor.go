package klineinverse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	compute "quantsaas/internal/compute"
	core "quantsaas/internal/klineinverse"
	parametercore "quantsaas/internal/parameterresearch"
	"quantsaas/internal/quant"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CalibrationExecutor struct {
	db *gorm.DB
}

func NewCalibrationExecutor(db *gorm.DB) *CalibrationExecutor {
	return &CalibrationExecutor{db: db}
}

func (e *CalibrationExecutor) Descriptor() compute.ExecutorDescriptor {
	return executorDescriptor(CalibrationExecutorType)
}

type calibrationCheckpoint struct {
	Next      int             `json:"next"`
	Behaviors []core.Behavior `json:"behaviors"`
}

func (e *CalibrationExecutor) Execute(ctx context.Context, execution computetask.Execution) (json.RawMessage, error) {
	if e == nil || e.db == nil {
		return nil, computetask.ErrServiceUnavailable
	}
	var input CalibrationExecutionInput
	if json.Unmarshal(execution.Input, &input) != nil || input.StudyID == 0 || input.BatchID == 0 {
		return nil, ErrInvalidRequest
	}
	study, canonical, batch, err := loadExecutionContext(ctx, e.db, execution.UserID, input.StudyID, input.BatchID, "calibration")
	if err != nil {
		return nil, err
	}
	checkpoint := calibrationCheckpoint{}
	if len(execution.Checkpoint) > 0 {
		_ = json.Unmarshal(execution.Checkpoint, &checkpoint)
	}
	if checkpoint.Next < 0 || checkpoint.Next > canonical.InitialBudget || len(checkpoint.Behaviors) != checkpoint.Next {
		checkpoint = calibrationCheckpoint{}
	}
	for sequence := checkpoint.Next; sequence < canonical.InitialBudget; sequence++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path, err := core.GenerateGlobal(canonical.RootSeed, sequence, canonical.Dates, canonical.WarmupLength, canonical.FinalBounds)
		if err != nil {
			return nil, err
		}
		behavior, err := core.Features(path)
		if err != nil {
			return nil, err
		}
		checkpoint.Behaviors = append(checkpoint.Behaviors, behavior)
		checkpoint.Next = sequence + 1
		if shouldReport(checkpoint.Next, canonical.InitialBudget) {
			if err := reportCalibration(ctx, e.db, execution, batch.ID, checkpoint, canonical.InitialBudget); err != nil {
				return nil, err
			}
		}
	}
	ranges, err := core.BuildFeatureRange(checkpoint.Behaviors)
	if err != nil {
		return nil, err
	}
	features := make([][20]float64, len(checkpoint.Behaviors))
	for index, behavior := range checkpoint.Behaviors {
		features[index] = core.NormalizeFeatures(behavior, ranges)
	}
	centers, err := core.CalibrateCVT(features, canonical.CellCount, canonical.RootSeed)
	if err != nil {
		return nil, err
	}
	sourceRaw, _ := compute.CanonicalJSON(canonical.CalibrationSources)
	observedRaw, _ := compute.CanonicalJSON(canonical.ObservedBounds)
	finalRaw, _ := compute.CanonicalJSON(canonical.FinalBounds)
	rangesRaw, _ := compute.CanonicalJSON(ranges)
	centersRaw, _ := compute.CanonicalJSON(centers)
	contentRaw, err := compute.CanonicalJSON(map[string]any{
		"schema_version":      CalibrationSchemaVersion,
		"study_hash":          study.StudyHash,
		"source_content_hash": canonical.CalibrationSourceHash,
		"ranges":              ranges,
		"centers":             centers,
		"count":               canonical.InitialBudget,
		"cell_count":          canonical.CellCount,
		"parent_capacity":     canonical.ParentCapacity,
		"versions":            map[string]string{"generator": core.CoordinateVersion, "feature": core.FeatureVersion, "cvt": core.CVTVersion},
	})
	if err != nil {
		return nil, err
	}
	calibration := saasstore.KlineInverseCalibration{
		StudyID: study.ID, SchemaVersion: CalibrationSchemaVersion, SourceSnapshot: sourceRaw,
		SourceContentHash: canonical.CalibrationSourceHash, ObservedBounds: observedRaw, FinalBounds: finalRaw,
		FeatureRanges: rangesRaw, Centers: centersRaw, CalibrationSeed: canonical.RootSeed,
		CalibrationCount: canonical.InitialBudget, CellCount: canonical.CellCount, ParentCapacity: canonical.ParentCapacity,
		GeneratorVersion: core.CoordinateVersion, FeatureVersion: core.FeatureVersion, CVTVersion: core.CVTVersion,
		ContentHash: compute.HashBytes(contentRaw),
	}
	now := time.Now().UTC()
	if err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "study_id"}}, DoNothing: true}).Create(&calibration).Error; err != nil {
			return err
		}
		if calibration.ID == 0 {
			if err := tx.Where("study_id = ?", study.ID).First(&calibration).Error; err != nil {
				return err
			}
		}
		if calibration.ContentHash != compute.HashBytes(contentRaw) {
			return ErrInvalidRequest
		}
		if err := tx.Model(&batch).Updates(map[string]any{
			"status": "completed", "completed_count": canonical.InitialBudget,
			"checkpoint_position": canonical.InitialBudget, "checkpoint": json.RawMessage(`{}`),
			"checkpoint_hash": "", "completed_at": now,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&study).Updates(map[string]any{"status": "waiting", "current_stage": "search", "error_message": ""}).Error
	}); err != nil {
		return nil, err
	}
	result := ExecutorResult{SchemaVersion: CalibrationResultVersion, StudyID: study.ID, BatchID: batch.ID, ContentHash: calibration.ContentHash}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: 1, RNGPosition: int64(canonical.InitialBudget)}); err != nil {
			return nil, err
		}
	}
	return compute.CanonicalJSON(result)
}

func projectCandidateCompletion(ctx context.Context, db *gorm.DB, study saasstore.KlineInverseStudy, snapshot saasstore.KlineInverseArchiveSnapshot, cellCount int) error {
	if study.SourceCandidateID == nil {
		return nil
	}
	partial, _ := compute.CanonicalJSON(map[string]any{
		"study_id": study.ID, "snapshot_id": snapshot.ID, "status": "completed",
		"evaluated_count": snapshot.EvaluatedCount, "a_coverage": ratio(snapshot.ACellCount, cellCount),
		"b_coverage": ratio(snapshot.BCellCount, cellCount), "back_link": fmt.Sprintf("/kline-inverse?study=%d", study.ID),
	})
	result := db.WithContext(ctx).Model(&saasstore.CandidateAnalysisLink{}).
		Where("candidate_id = ? AND analysis_kind = ? AND version = ?", *study.SourceCandidateID, "C", parametercore.AnalysisLinkVersion).
		Updates(map[string]any{"status": "completed", "task_id": study.ID, "source_id": strconv.FormatUint(uint64(study.ID), 10), "source_version": SnapshotSchemaVersion, "source_content_hash": snapshot.ContentHash, "partial_snapshot": partial, "error_message": ""})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (e *CalibrationExecutor) ValidateCachedResult(ctx context.Context, userID uint, raw json.RawMessage) error {
	var result ExecutorResult
	if json.Unmarshal(raw, &result) != nil || result.SchemaVersion != CalibrationResultVersion || result.StudyID == 0 {
		return ErrInvalidRequest
	}
	var count int64
	err := e.db.WithContext(ctx).Model(&saasstore.KlineInverseCalibration{}).
		Joins("JOIN kline_inverse_studies ON kline_inverse_studies.id = kline_inverse_calibrations.study_id").
		Where("kline_inverse_calibrations.study_id = ? AND kline_inverse_calibrations.content_hash = ? AND kline_inverse_studies.owner_user_id = ?", result.StudyID, result.ContentHash, userID).Count(&count).Error
	if err != nil || count != 1 {
		return ErrNotFound
	}
	return nil
}

func reportCalibration(ctx context.Context, db *gorm.DB, execution computetask.Execution, batchID uint, checkpoint calibrationCheckpoint, total int) error {
	raw, err := compute.CanonicalJSON(checkpoint)
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).Model(&saasstore.KlineInverseBatch{}).Where("id = ?", batchID).Updates(map[string]any{
		"status": "running", "completed_count": checkpoint.Next, "checkpoint_position": checkpoint.Next,
		"checkpoint": raw, "checkpoint_hash": compute.HashBytes(raw),
	}).Error; err != nil {
		return err
	}
	if execution.Report != nil {
		return execution.Report(ctx, computetask.ProgressUpdate{Progress: float64(checkpoint.Next) / float64(total), Checkpoint: raw, RNGPosition: int64(checkpoint.Next)})
	}
	return nil
}

type SearchExecutor struct {
	db        *gorm.DB
	backtests *backtest.Service
}

func NewSearchExecutor(db *gorm.DB, backtests *backtest.Service) *SearchExecutor {
	if backtests == nil {
		backtests = backtest.NewService(db)
	}
	return &SearchExecutor{db: db, backtests: backtests}
}

func (e *SearchExecutor) Descriptor() compute.ExecutorDescriptor {
	return executorDescriptor(SearchExecutorType)
}

type searchCheckpoint struct {
	Next int `json:"next"`
}

type searchArchive map[int][]core.Candidate

func (e *SearchExecutor) Execute(ctx context.Context, execution computetask.Execution) (json.RawMessage, error) {
	if e == nil || e.db == nil || e.backtests == nil {
		return nil, computetask.ErrServiceUnavailable
	}
	var input SearchExecutionInput
	if json.Unmarshal(execution.Input, &input) != nil || input.StudyID == 0 || input.BatchID == 0 {
		return nil, ErrInvalidRequest
	}
	study, canonical, batch, err := loadExecutionContext(ctx, e.db, execution.UserID, input.StudyID, input.BatchID, "search")
	if err != nil {
		return nil, err
	}
	calibration, ranges, centers, err := loadCalibration(ctx, e.db, study.ID, canonical)
	if err != nil {
		return nil, err
	}
	archive, completed, cacheHits, errorCount, err := loadArchive(ctx, e.db, study.ID, batch.ID, canonical.ParentCapacity, canonical.FinalBounds)
	if err != nil {
		return nil, err
	}
	if batch.CompletedCount > completed {
		completed = batch.CompletedCount
	}
	checkpoint := searchCheckpoint{Next: int(batch.CheckpointPosition)}
	if checkpoint.Next < completed {
		checkpoint.Next = completed
	}
	if len(execution.Checkpoint) > 0 {
		var stored searchCheckpoint
		if json.Unmarshal(execution.Checkpoint, &stored) == nil && stored.Next > checkpoint.Next && stored.Next <= batch.Budget {
			checkpoint = stored
		}
	}
	manifest := batchManifest{}
	if len(batch.Manifest) > 0 {
		_ = json.Unmarshal(batch.Manifest, &manifest)
	}
	operations := operationSchedule(batch.Budget)
	if batch.BatchType == "probe" {
		operations = selectedOperationSchedule(batch.Budget, manifest.Operations)
	}
	for offset := checkpoint.Next; offset < batch.Budget; offset++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sequence := int(batch.RNGStart) + offset
		requested := operations[offset]
		path, variation, parents, err := buildBatchPath(ctx, e.db, study.ID, canonical, batch, manifest, archive, sequence, requested)
		if err != nil {
			errorCount++
			checkpoint.Next = offset + 1
			if shouldReport(checkpoint.Next, batch.Budget) {
				if reportErr := reportSearch(ctx, e.db, execution, batch.ID, checkpoint, completed, cacheHits, errorCount, batch.Budget); reportErr != nil {
					return nil, reportErr
				}
			}
			continue
		}
		evaluation, reused, err := e.evaluatePath(ctx, execution.UserID, study, batch, canonical, ranges, centers, sequence, path, variation, parents)
		if err != nil {
			errorCount++
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
		} else {
			completed++
			if reused {
				cacheHits++
			}
			candidate, err := candidateFromEvaluation(ctx, e.db, evaluation)
			if err != nil {
				return nil, err
			}
			selected, err := core.SelectPareto(append(archive[evaluation.CellIndex], candidate), canonical.ParentCapacity, canonical.FinalBounds)
			if err != nil {
				return nil, err
			}
			archive[evaluation.CellIndex] = selected
		}
		checkpoint.Next = offset + 1
		if shouldReport(checkpoint.Next, batch.Budget) {
			if err := reportSearch(ctx, e.db, execution, batch.ID, checkpoint, completed, cacheHits, errorCount, batch.Budget); err != nil {
				return nil, err
			}
		}
	}
	snapshot, err := buildSnapshot(ctx, e.db, study, batch, canonical, archive, completed, cacheHits, errorCount)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "batch_id"}}, DoNothing: true}).Create(&snapshot)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			var existing saasstore.KlineInverseArchiveSnapshot
			if lookupErr := tx.Where("batch_id = ?", batch.ID).First(&existing).Error; lookupErr != nil {
				return lookupErr
			}
			if existing.ContentHash != snapshot.ContentHash {
				return ErrInvalidRequest
			}
			snapshot = existing
		}
		if err := tx.Model(&batch).Updates(map[string]any{
			"status": "completed", "completed_count": completed, "cache_hit_count": cacheHits,
			"error_count": errorCount, "checkpoint_position": batch.Budget, "checkpoint": json.RawMessage(`{}`),
			"checkpoint_hash": "", "completed_at": now,
		}).Error; err != nil {
			return err
		}
		if batch.BatchType == "probe" {
			if err := tx.Model(&saasstore.KlineInverseProbeBatch{}).Where("batch_id = ?", batch.ID).Update("status", "completed").Error; err != nil {
				return err
			}
		}
		return tx.Model(&study).Updates(map[string]any{
			"status": "current_plan_completed", "current_stage": "completed", "current_snapshot_id": snapshot.ID,
			"completed_at": now, "error_message": "",
		}).Error
	}); err != nil {
		return nil, err
	}
	if err := projectCandidateCompletion(ctx, e.db, study, snapshot, canonical.CellCount); err != nil {
		return nil, err
	}
	result := ExecutorResult{SchemaVersion: SearchResultVersion, StudyID: study.ID, BatchID: batch.ID, SnapshotID: snapshot.ID, ContentHash: snapshot.ContentHash}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: 1, RNGPosition: batch.RNGEnd}); err != nil {
			return nil, err
		}
	}
	_ = calibration
	return compute.CanonicalJSON(result)
}

func (e *SearchExecutor) ValidateCachedResult(ctx context.Context, userID uint, raw json.RawMessage) error {
	var result ExecutorResult
	if json.Unmarshal(raw, &result) != nil || result.SchemaVersion != SearchResultVersion || result.SnapshotID == 0 {
		return ErrInvalidRequest
	}
	var count int64
	err := e.db.WithContext(ctx).Model(&saasstore.KlineInverseArchiveSnapshot{}).
		Joins("JOIN kline_inverse_studies ON kline_inverse_studies.id = kline_inverse_archive_snapshots.study_id").
		Where("kline_inverse_archive_snapshots.id = ? AND kline_inverse_archive_snapshots.content_hash = ? AND kline_inverse_studies.owner_user_id = ?", result.SnapshotID, result.ContentHash, userID).Count(&count).Error
	if err != nil || count != 1 {
		return ErrNotFound
	}
	return nil
}

func loadExecutionContext(ctx context.Context, db *gorm.DB, userID, studyID, batchID uint, batchType string) (saasstore.KlineInverseStudy, StudyCanonical, saasstore.KlineInverseBatch, error) {
	var study saasstore.KlineInverseStudy
	if err := db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", studyID, userID).First(&study).Error; err != nil {
		return study, StudyCanonical{}, saasstore.KlineInverseBatch{}, ErrNotFound
	}
	var canonical StudyCanonical
	if json.Unmarshal(study.Canonical, &canonical) != nil || canonical.SchemaVersion != StudySchemaVersion {
		return study, canonical, saasstore.KlineInverseBatch{}, ErrInvalidRequest
	}
	var batch saasstore.KlineInverseBatch
	if err := db.WithContext(ctx).Where("id = ? AND study_id = ?", batchID, studyID).First(&batch).Error; err != nil {
		return study, canonical, batch, ErrNotFound
	}
	if batchType == "calibration" && batch.BatchType != "calibration" {
		return study, canonical, batch, ErrNotFound
	}
	if batchType == "search" && !contains([]string{"search", "extension", "probe"}, batch.BatchType) {
		return study, canonical, batch, ErrNotFound
	}
	if batch.BatchType == "calibration" || batch.BatchType == "search" {
		if batch.CompatibilityHash != study.CanonicalHash {
			return study, canonical, batch, ErrInvalidRequest
		}
	} else {
		var manifest batchManifest
		if json.Unmarshal(batch.Manifest, &manifest) != nil || manifest.Compatibility["study"] != study.CanonicalHash || manifest.Compatibility["search"] != core.SearchVersion || manifest.Compatibility["variation"] != core.VariationVersion || manifest.Compatibility["rng"] != core.RNGVersion {
			return study, canonical, batch, ErrInvalidRequest
		}
		var calibration saasstore.KlineInverseCalibration
		if db.WithContext(ctx).Where("study_id = ?", study.ID).First(&calibration).Error != nil || manifest.Compatibility["calibration"] != calibration.ContentHash {
			return study, canonical, batch, ErrInvalidRequest
		}
	}
	return study, canonical, batch, nil
}

func loadCalibration(ctx context.Context, db *gorm.DB, studyID uint, canonical StudyCanonical) (saasstore.KlineInverseCalibration, core.FeatureRange, []core.Center, error) {
	var calibration saasstore.KlineInverseCalibration
	if err := db.WithContext(ctx).Where("study_id = ?", studyID).First(&calibration).Error; err != nil {
		return calibration, core.FeatureRange{}, nil, fmt.Errorf("特徵空間校準尚未完成: %w", err)
	}
	if calibration.CalibrationCount != canonical.InitialBudget || calibration.CellCount != canonical.CellCount || calibration.SourceContentHash != canonical.CalibrationSourceHash || calibration.FeatureVersion != core.FeatureVersion || calibration.CVTVersion != core.CVTVersion {
		return calibration, core.FeatureRange{}, nil, ErrInvalidRequest
	}
	var ranges core.FeatureRange
	var centers []core.Center
	if json.Unmarshal(calibration.FeatureRanges, &ranges) != nil || json.Unmarshal(calibration.Centers, &centers) != nil || len(centers) != canonical.CellCount {
		return calibration, ranges, nil, ErrInvalidRequest
	}
	return calibration, ranges, centers, nil
}

func operationSchedule(n int) []string {
	quotas := core.OperationQuotas(n)
	result := make([]string, 0, n)
	remaining := n
	for remaining > 0 {
		for _, operation := range core.OperationOrder {
			if quotas[operation] > 0 {
				result = append(result, operation)
				quotas[operation]--
				remaining--
			}
		}
	}
	return result
}

func selectedOperationSchedule(n int, operations []string) []string {
	if len(operations) == 0 {
		return operationSchedule(n)
	}
	result := make([]string, n)
	for index := range result {
		result[index] = operations[index%len(operations)]
	}
	return result
}

func buildBatchPath(ctx context.Context, db *gorm.DB, studyID uint, canonical StudyCanonical, batch saasstore.KlineInverseBatch, manifest batchManifest, archive searchArchive, sequence int, requested string) (core.Path, core.Variation, []core.Candidate, error) {
	if batch.BatchType != "probe" {
		return buildSearchPath(canonical, archive, sequence, requested)
	}
	var anchorEvaluation saasstore.KlineInverseEvaluation
	if err := db.WithContext(ctx).Where("study_id = ? AND path_id = ? AND permanent = ? AND pass_a = ?", studyID, manifest.AnchorPathID, true, true).First(&anchorEvaluation).Error; err != nil {
		return core.Path{}, core.Variation{}, nil, ErrNotFound
	}
	anchor, err := candidateFromEvaluation(ctx, db, anchorEvaluation)
	if err != nil {
		return core.Path{}, core.Variation{}, nil, err
	}
	parents := []core.Candidate{anchor}
	selected := selectParents(canonical.RootSeed, sequence, archive)
	for _, candidate := range selected {
		if candidate.ID != anchor.ID {
			parents = append(parents, candidate)
			break
		}
	}
	path, variation, err := core.ProbeVary(canonical.RootSeed, sequence, requested, parents, canonical.FinalBounds, manifest.Amplitude, manifest.Scope, manifest.MinLength, manifest.MaxLength)
	return path, variation, parents, err
}

func buildSearchPath(canonical StudyCanonical, archive searchArchive, sequence int, requested string) (core.Path, core.Variation, []core.Candidate, error) {
	parents := selectParents(canonical.RootSeed, sequence, archive)
	if requested == core.OperationGlobal || len(parents) == 0 {
		path, err := core.GenerateGlobal(canonical.RootSeed, sequence, canonical.Dates, canonical.WarmupLength, canonical.FinalBounds)
		return path, core.Variation{Operation: requested, ActualOperation: core.OperationGlobal, Amplitude: canonical.MutationAmplitude}, parents, err
	}
	path, variation, err := core.Vary(canonical.RootSeed, sequence, requested, parents, canonical.FinalBounds, canonical.MutationAmplitude)
	return path, variation, parents, err
}

func selectParents(seed int64, sequence int, archive searchArchive) []core.Candidate {
	all := make([]core.Candidate, 0)
	for _, candidates := range archive {
		all = append(all, candidates...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Hash < all[j].Hash })
	if len(all) == 0 {
		return nil
	}
	first := int(core.DeterministicUnit(seed, sequence, 500000) * float64(len(all)))
	result := []core.Candidate{all[first]}
	if len(all) > 1 {
		second := int(core.DeterministicUnit(seed, sequence, 500001) * float64(len(all)-1))
		if second >= first {
			second++
		}
		result = append(result, all[second])
	}
	return result
}

func (e *SearchExecutor) evaluatePath(ctx context.Context, userID uint, study saasstore.KlineInverseStudy, batch saasstore.KlineInverseBatch, canonical StudyCanonical, ranges core.FeatureRange, centers []core.Center, sequence int, path core.Path, variation core.Variation, parents []core.Candidate) (saasstore.KlineInverseEvaluation, bool, error) {
	pathHash, err := core.Hash(path)
	if err != nil {
		return saasstore.KlineInverseEvaluation{}, false, err
	}
	behavior, err := core.Features(path)
	if err != nil {
		return saasstore.KlineInverseEvaluation{}, false, err
	}
	features := core.FeatureVector(behavior)
	featuresRaw, _ := compute.CanonicalJSON(behavior)
	cell := core.AssignCell(core.NormalizeFeatures(behavior, ranges), centers)
	ohlc, err := core.Reconstruct(path, 100)
	if err != nil {
		return saasstore.KlineInverseEvaluation{}, false, err
	}
	pathRow, err := persistPath(ctx, e.db, path, ohlc, pathHash)
	if err != nil {
		return saasstore.KlineInverseEvaluation{}, false, err
	}
	if err := persistLineage(ctx, e.db, study.ID, batch.ID, sequence, pathRow.ID, variation, parents); err != nil {
		return saasstore.KlineInverseEvaluation{}, false, err
	}
	evaluationKeyRaw, _ := compute.CanonicalJSON(map[string]any{"study_hash": study.StudyHash, "path_hash": pathHash})
	evaluationKey := "p12-evaluation:" + compute.HashBytes(evaluationKeyRaw)
	var existing saasstore.KlineInverseEvaluation
	if err := e.db.WithContext(ctx).Where("study_id = ? AND evaluation_key = ?", study.ID, evaluationKey).First(&existing).Error; err == nil && existing.Status == "completed" {
		if err := preservePermanence(ctx, e.db, study.ID, pathRow.ID, existing.ID, parents, existing.PassA); err != nil {
			return existing, true, err
		}
		return existing, true, nil
	}
	recordFailure := func(errorType string, cause error) error {
		failure := saasstore.KlineInverseEvaluation{
			StudyID: study.ID, PathID: pathRow.ID, EvaluationKey: evaluationKey, BatchID: batch.ID,
			SequenceIndex: sequence, CellIndex: cell, Status: "failed", OutcomeState: "error",
			FeaturesVersion: core.FeatureVersion, FeaturesHash: compute.HashBytes(featuresRaw), Features: featuresRaw,
			BacktestResultVersion: "unavailable", ErrorType: errorType, ErrorMessage: cause.Error(),
		}
		if existing.ID == 0 {
			return e.db.WithContext(ctx).Create(&failure).Error
		}
		return e.db.WithContext(ctx).Model(&existing).Updates(map[string]any{
			"batch_id": batch.ID, "sequence_index": sequence, "cell_index": cell,
			"status": "failed", "outcome_state": "error", "pass_a": false, "pass_b": false,
			"q_relative": 0, "q_absolute": 0, "features_version": core.FeatureVersion,
			"features_hash": compute.HashBytes(featuresRaw), "features": featuresRaw,
			"backtest_result_id": 0, "backtest_result_version": "unavailable",
			"backtest_result_content_hash": "", "reused_backtest": false, "permanent": false,
			"error_type": errorType, "error_message": cause.Error(),
		}).Error
	}
	bars := make([]quant.Bar, len(ohlc))
	for index, bar := range ohlc {
		bars[index] = quant.Bar{OpenTime: bar.TimeMs, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: 0}
	}
	initial, fee, spread, monthly := canonical.InitialCapital, canonical.FeeRate, canonical.SlippageRate, 0.0
	request := backtest.CreateRequest{
		StrategyID: sigmoiddca.StrategyID, InstrumentID: canonical.InstrumentID, DataSource: canonical.DataSource,
		ExecutionMode: canonical.ExecutionMode, Pair: canonical.Symbol, Symbol: canonical.Symbol, Interval: canonical.Interval,
		InitialCapital: &initial, MonthlyDCA: &monthly, FeeRate: &fee, SpreadRate: &spread,
	}
	standard, err := e.backtests.EnsureGeneratedPathStandardResult(ctx, userID, backtest.GeneratedPathRequest{
		Backtest: request, Parameters: canonical.Parameters, Bars: bars,
		EvaluationStartMs: canonical.EvaluationStartMs, PathHash: pathHash,
	})
	if err != nil {
		if persistErr := recordFailure("backtest", err); persistErr != nil {
			return saasstore.KlineInverseEvaluation{}, false, fmt.Errorf("%v；保存失敗評估時發生錯誤: %w", err, persistErr)
		}
		return saasstore.KlineInverseEvaluation{}, false, err
	}
	var extra struct {
		BenchmarkFinalEquity float64 `json:"benchmark_final_equity"`
	}
	if json.Unmarshal(standard.Summary.Extra, &extra) != nil || extra.BenchmarkFinalEquity <= 0 {
		failureErr := fmt.Errorf("標準回測摘要缺少 DCA 終值")
		if persistErr := recordFailure("benchmark_summary", failureErr); persistErr != nil {
			return saasstore.KlineInverseEvaluation{}, standard.Reused, fmt.Errorf("%v；保存失敗評估時發生錯誤: %w", failureErr, persistErr)
		}
		return saasstore.KlineInverseEvaluation{}, standard.Reused, failureErr
	}
	outcome, err := core.Classify(canonical.InitialCapital, standard.Summary.FinalEquity, extra.BenchmarkFinalEquity)
	if err != nil {
		if persistErr := recordFailure("classification", err); persistErr != nil {
			return saasstore.KlineInverseEvaluation{}, standard.Reused, fmt.Errorf("%v；保存失敗評估時發生錯誤: %w", err, persistErr)
		}
		return saasstore.KlineInverseEvaluation{}, standard.Reused, err
	}
	evaluation := saasstore.KlineInverseEvaluation{
		StudyID: study.ID, PathID: pathRow.ID, EvaluationKey: evaluationKey, BatchID: batch.ID,
		SequenceIndex: sequence, CellIndex: cell, Status: "completed", OutcomeState: outcome.State,
		PassA: outcome.PassA, PassB: outcome.PassB, QRelative: outcome.QRelative, QAbsolute: outcome.QAbsolute,
		FeaturesVersion: core.FeatureVersion, FeaturesHash: compute.HashBytes(featuresRaw), Features: featuresRaw,
		BacktestResultID: standard.ID, BacktestResultVersion: standard.Version,
		BacktestResultContentHash: standard.ContentHash, ReusedBacktest: standard.Reused,
		Permanent: outcome.PassA,
	}
	_ = features
	if existing.ID != 0 {
		if err := e.db.WithContext(ctx).Delete(&existing).Error; err != nil {
			return evaluation, standard.Reused, err
		}
	}
	if err := e.db.WithContext(ctx).Create(&evaluation).Error; err != nil {
		if lookupErr := e.db.WithContext(ctx).Where("study_id = ? AND evaluation_key = ?", study.ID, evaluationKey).First(&evaluation).Error; lookupErr != nil {
			return evaluation, standard.Reused, err
		}
	}
	if err := preservePermanence(ctx, e.db, study.ID, pathRow.ID, evaluation.ID, parents, outcome.PassA); err != nil {
		return evaluation, standard.Reused, err
	}
	return evaluation, standard.Reused, nil
}

func persistPath(ctx context.Context, db *gorm.DB, path core.Path, ohlc []core.OHLC, pathHash string) (saasstore.KlineInversePath, error) {
	datesRaw, _ := compute.CanonicalJSON(path.Dates)
	coordinatesRaw, _ := compute.CanonicalJSON(path.Coordinates)
	ohlcRaw, _ := compute.CanonicalJSON(ohlc)
	row := saasstore.KlineInversePath{
		PathHash: pathHash, SchemaVersion: PathSchemaVersion, CoordinateVersion: core.CoordinateVersion,
		WarmupLength: path.WarmupLength, EvaluationLength: path.EvaluationLength,
		StartTimeMs: path.Dates[0], EvaluationStartMs: path.Dates[path.WarmupLength], EndTimeMs: path.Dates[len(path.Dates)-1],
		DatesHash: compute.HashBytes(datesRaw), CoordinatesHash: compute.HashBytes(coordinatesRaw), OHLCContentHash: compute.HashBytes(ohlcRaw),
		Coordinates: coordinatesRaw, OHLC: ohlcRaw,
	}
	created := db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "path_hash"}}, DoNothing: true}).Create(&row)
	if created.Error != nil {
		return row, created.Error
	}
	if created.RowsAffected == 0 {
		if err := db.WithContext(ctx).Where("path_hash = ?", pathHash).First(&row).Error; err != nil {
			return row, err
		}
		if row.CoordinatesHash != compute.HashBytes(coordinatesRaw) || row.OHLCContentHash != compute.HashBytes(ohlcRaw) {
			return row, ErrInvalidRequest
		}
	}
	return row, nil
}

func persistLineage(ctx context.Context, db *gorm.DB, studyID, batchID uint, sequence int, childPathID uint, variation core.Variation, parents []core.Candidate) error {
	channelsRaw, _ := compute.CanonicalJSON(variation.Channels)
	if len(parents) == 0 {
		var existing int64
		if err := db.WithContext(ctx).Model(&saasstore.KlineInverseLineageEdge{}).
			Where("study_id = ? AND batch_id = ? AND sequence_index = ? AND child_path_id = ? AND parent_path_id IS NULL AND parent_ordinal = ?", studyID, batchID, sequence, childPathID, 0).
			Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return nil
		}
		row := saasstore.KlineInverseLineageEdge{StudyID: studyID, BatchID: batchID, SequenceIndex: sequence, ChildPathID: childPathID, ParentOrdinal: 0, RequestedOperation: variation.Operation, ActualOperation: variation.ActualOperation, Amplitude: variation.Amplitude, ChangedStart: variation.StartIndex, ChangedLength: variation.Length, ChangedChannels: channelsRaw, RNGPosition: int64(sequence), VariationVersion: core.VariationVersion}
		return db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	}
	for ordinal, parent := range parents {
		parentID, err := strconv.ParseUint(parent.ID, 10, 64)
		if err != nil {
			return err
		}
		value := uint(parentID)
		row := saasstore.KlineInverseLineageEdge{StudyID: studyID, BatchID: batchID, SequenceIndex: sequence, ChildPathID: childPathID, ParentPathID: &value, ParentOrdinal: ordinal, RequestedOperation: variation.Operation, ActualOperation: variation.ActualOperation, Amplitude: variation.Amplitude, ChangedStart: variation.StartIndex, ChangedLength: variation.Length, ChangedChannels: channelsRaw, RNGPosition: int64(sequence), VariationVersion: core.VariationVersion}
		if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func preservePermanence(ctx context.Context, db *gorm.DB, studyID, pathID, evaluationID uint, parents []core.Candidate, success bool) error {
	parentIDs := make([]uint, 0, len(parents))
	parentIsAnchor := false
	for _, parent := range parents {
		value, _ := strconv.ParseUint(parent.ID, 10, 64)
		if value == 0 {
			continue
		}
		parentIDs = append(parentIDs, uint(value))
		var count int64
		_ = db.WithContext(ctx).Model(&saasstore.KlineInverseEvaluation{}).Where("study_id = ? AND path_id = ? AND pass_a = ?", studyID, uint(value), true).Count(&count).Error
		parentIsAnchor = parentIsAnchor || count > 0
	}
	if !success && !parentIsAnchor {
		return nil
	}
	reason := "anchor"
	if !success {
		reason = "direct_anchor_child"
	}
	if err := db.WithContext(ctx).Model(&saasstore.KlineInversePath{}).Where("id = ?", pathID).Updates(map[string]any{"permanent": true, "permanent_reason": reason}).Error; err != nil {
		return err
	}
	if err := db.WithContext(ctx).Model(&saasstore.KlineInverseEvaluation{}).Where("id = ?", evaluationID).Update("permanent", true).Error; err != nil {
		return err
	}
	if success {
		return markAncestorsPermanent(ctx, db, studyID, parentIDs)
	}
	return nil
}

func markAncestorsPermanent(ctx context.Context, db *gorm.DB, studyID uint, seeds []uint) error {
	queue := append([]uint(nil), seeds...)
	seen := map[uint]bool{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == 0 || seen[current] {
			continue
		}
		seen[current] = true
		if err := db.WithContext(ctx).Model(&saasstore.KlineInversePath{}).Where("id = ?", current).Updates(map[string]any{"permanent": true, "permanent_reason": "successful_ancestor"}).Error; err != nil {
			return err
		}
		if err := db.WithContext(ctx).Model(&saasstore.KlineInverseEvaluation{}).Where("study_id = ? AND path_id = ?", studyID, current).Update("permanent", true).Error; err != nil {
			return err
		}
		var edges []saasstore.KlineInverseLineageEdge
		if err := db.WithContext(ctx).Where("study_id = ? AND child_path_id = ? AND parent_path_id IS NOT NULL", studyID, current).Find(&edges).Error; err != nil {
			return err
		}
		for _, edge := range edges {
			if edge.ParentPathID != nil {
				queue = append(queue, *edge.ParentPathID)
			}
		}
	}
	return nil
}

func loadArchive(ctx context.Context, db *gorm.DB, studyID, batchID uint, capacity int, bounds core.Bounds) (searchArchive, int, int, int, error) {
	var rows []saasstore.KlineInverseEvaluation
	if err := db.WithContext(ctx).Where("study_id = ? AND status = ?", studyID, "completed").Order("sequence_index ASC").Find(&rows).Error; err != nil {
		return nil, 0, 0, 0, err
	}
	archive := searchArchive{}
	cacheHits := 0
	completed := 0
	for _, row := range rows {
		candidate, err := candidateFromEvaluation(ctx, db, row)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		selected, err := core.SelectPareto(append(archive[row.CellIndex], candidate), capacity, bounds)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		archive[row.CellIndex] = selected
		if row.BatchID == batchID {
			completed++
		}
		if row.BatchID == batchID && row.ReusedBacktest {
			cacheHits++
		}
	}
	var batch saasstore.KlineInverseBatch
	_ = db.WithContext(ctx).First(&batch, batchID).Error
	return archive, completed, cacheHits, batch.ErrorCount, nil
}

func candidateFromEvaluation(ctx context.Context, db *gorm.DB, evaluation saasstore.KlineInverseEvaluation) (core.Candidate, error) {
	var row saasstore.KlineInversePath
	if err := db.WithContext(ctx).First(&row, evaluation.PathID).Error; err != nil {
		return core.Candidate{}, err
	}
	var coordinates []core.Coordinate
	if json.Unmarshal(row.Coordinates, &coordinates) != nil {
		return core.Candidate{}, ErrInvalidRequest
	}
	dates := make([]int64, len(coordinates))
	var ohlc []core.OHLC
	if json.Unmarshal(row.OHLC, &ohlc) != nil || len(ohlc) != len(coordinates) {
		return core.Candidate{}, ErrInvalidRequest
	}
	for index := range ohlc {
		dates[index] = ohlc[index].TimeMs
	}
	path := core.Path{WarmupLength: row.WarmupLength, EvaluationLength: row.EvaluationLength, Dates: dates, Coordinates: coordinates}
	return core.Candidate{ID: strconv.FormatUint(uint64(row.ID), 10), Hash: row.PathHash, Path: path, QRelative: evaluation.QRelative, QAbsolute: evaluation.QAbsolute}, nil
}

func reportSearch(ctx context.Context, db *gorm.DB, execution computetask.Execution, batchID uint, checkpoint searchCheckpoint, completed, cacheHits, errorCount, total int) error {
	raw, _ := compute.CanonicalJSON(checkpoint)
	if err := db.WithContext(ctx).Model(&saasstore.KlineInverseBatch{}).Where("id = ?", batchID).Updates(map[string]any{
		"status": "running", "completed_count": completed, "cache_hit_count": cacheHits, "error_count": errorCount,
		"checkpoint_position": checkpoint.Next, "checkpoint": raw, "checkpoint_hash": compute.HashBytes(raw),
	}).Error; err != nil {
		return err
	}
	if execution.Report != nil {
		return execution.Report(ctx, computetask.ProgressUpdate{Progress: float64(checkpoint.Next) / float64(total), Checkpoint: raw, RNGPosition: int64(checkpoint.Next)})
	}
	return nil
}

func buildSnapshot(ctx context.Context, db *gorm.DB, study saasstore.KlineInverseStudy, batch saasstore.KlineInverseBatch, canonical StudyCanonical, archive searchArchive, completed, cacheHits, errorCount int) (saasstore.KlineInverseArchiveSnapshot, error) {
	var evaluations []saasstore.KlineInverseEvaluation
	if err := db.WithContext(ctx).Where("study_id = ? AND status = ?", study.ID, "completed").Find(&evaluations).Error; err != nil {
		return saasstore.KlineInverseArchiveSnapshot{}, err
	}
	stateCounts := map[string]int{}
	qRel, qAbs := []float64{}, []float64{}
	aCells, bCells, touched := map[int]bool{}, map[int]bool{}, map[int]bool{}
	for _, evaluation := range evaluations {
		stateCounts[evaluation.OutcomeState]++
		qRel = append(qRel, evaluation.QRelative)
		qAbs = append(qAbs, evaluation.QAbsolute)
		touched[evaluation.CellIndex] = true
		if evaluation.PassA {
			aCells[evaluation.CellIndex] = true
		}
		if evaluation.PassB {
			bCells[evaluation.CellIndex] = true
		}
	}
	active := map[string][]uint{}
	for cell, candidates := range archive {
		for _, candidate := range candidates {
			id, _ := strconv.ParseUint(candidate.ID, 10, 64)
			active[strconv.Itoa(cell)] = append(active[strconv.Itoa(cell)], uint(id))
		}
	}
	var permanentCount, edgeCount int64
	_ = db.WithContext(ctx).Model(&saasstore.KlineInverseEvaluation{}).Where("study_id = ? AND permanent = ?", study.ID, true).Count(&permanentCount).Error
	_ = db.WithContext(ctx).Model(&saasstore.KlineInverseLineageEdge{}).Where("study_id = ?", study.ID).Count(&edgeCount).Error
	overview := Overview{
		StudyID: study.ID, Status: "completed", EvaluatedCount: len(evaluations), TouchedCellCount: len(touched),
		SearchReach: ratio(len(touched), canonical.CellCount), ACellCount: len(aCells), BCellCount: len(bCells),
		ACoverage: ratio(len(aCells), canonical.CellCount), BCoverage: ratio(len(bCells), canonical.CellCount),
		ACellPerTouched: ratio(len(aCells), len(touched)), BCellPerTouched: ratio(len(bCells), len(touched)),
		StateCounts: stateCounts, QRelativeStatistics: core.Quantiles(qRel), QAbsoluteStatistics: core.Quantiles(qAbs),
		PermanentPathCount: int(permanentCount), LineageEdgeCount: int(edgeCount), CacheHitCount: cacheHits, ErrorCount: errorCount,
		FeatureStatistics: map[string]map[string]float64{}, DistanceStatistics: map[string]map[string]float64{},
	}
	var calibration saasstore.KlineInverseCalibration
	if err := db.WithContext(ctx).Where("study_id = ?", study.ID).First(&calibration).Error; err != nil {
		return saasstore.KlineInverseArchiveSnapshot{}, err
	}
	var centers []core.Center
	if json.Unmarshal(calibration.Centers, &centers) != nil {
		return saasstore.KlineInverseArchiveSnapshot{}, ErrInvalidRequest
	}
	featureValues := make([][]float64, len(core.FeatureNames))
	for _, evaluation := range evaluations {
		var behavior core.Behavior
		if json.Unmarshal(evaluation.Features, &behavior) != nil {
			continue
		}
		vector := core.FeatureVector(behavior)
		for index, value := range vector {
			featureValues[index] = append(featureValues[index], value)
		}
	}
	for index, name := range core.FeatureNames {
		overview.FeatureStatistics[name] = core.Quantiles(featureValues[index])
	}
	distanceValues, err := activeDistanceStatistics(archive, canonical.FinalBounds)
	if err != nil {
		return saasstore.KlineInverseArchiveSnapshot{}, err
	}
	overview.DistanceStatistics = distanceValues
	cellSummaries, err := buildCellSummaries(evaluations, archive, centers)
	if err != nil {
		return saasstore.KlineInverseArchiveSnapshot{}, err
	}
	summaryRaw, _ := compute.CanonicalJSON(overview)
	activeRaw, _ := compute.CanonicalJSON(active)
	cellsRaw, _ := compute.CanonicalJSON(cellSummaries)
	contentRaw, _ := compute.CanonicalJSON(map[string]any{"study_hash": study.StudyHash, "batch_manifest_hash": batch.ManifestHash, "summary": overview, "active": active, "cells": cellSummaries})
	contentHash := compute.HashBytes(contentRaw)
	return saasstore.KlineInverseArchiveSnapshot{
		StudyID: study.ID, BatchID: batch.ID, SnapshotKey: "p12-snapshot:" + contentHash,
		SchemaVersion: SnapshotSchemaVersion, SearchVersion: core.SearchVersion, StatisticsVersion: StatisticsVersion,
		EvaluatedCount: len(evaluations), TouchedCellCount: len(touched), ACellCount: len(aCells), BCellCount: len(bCells),
		PermanentPathCount: int(permanentCount), LineageEdgeCount: int(edgeCount), Summary: summaryRaw,
		ActiveParents: activeRaw, CellSummary: cellsRaw, ContentHash: contentHash,
	}, nil
}

func buildCellSummaries(evaluations []saasstore.KlineInverseEvaluation, archive searchArchive, centers []core.Center) ([]CellSummary, error) {
	byCell := map[int][]saasstore.KlineInverseEvaluation{}
	for _, evaluation := range evaluations {
		byCell[evaluation.CellIndex] = append(byCell[evaluation.CellIndex], evaluation)
	}
	cells := make([]int, 0, len(byCell))
	for cell := range byCell {
		cells = append(cells, cell)
	}
	sort.Ints(cells)
	result := make([]CellSummary, 0, len(cells))
	for _, cell := range cells {
		rows := byCell[cell]
		qRel, qAbs := []float64{}, []float64{}
		aCount, bCount := 0, 0
		for _, row := range rows {
			qRel = append(qRel, row.QRelative)
			qAbs = append(qAbs, row.QAbsolute)
			if row.PassA {
				aCount++
			}
			if row.PassB {
				bCount++
			}
		}
		relStats, absStats := core.Quantiles(qRel), core.Quantiles(qAbs)
		summary := CellSummary{CellIndex: cell, EvaluationCount: len(rows), ACount: aCount, BCount: bCount, ActiveParetoCount: len(archive[cell]), BestQRelative: relStats["max"], MedianQRelative: relStats["median"], BestQAbsolute: absStats["max"], MedianQAbsolute: absStats["median"]}
		if cell >= 0 && cell < len(centers) {
			summary.Features = [20]float64(centers[cell])
		}
		result = append(result, summary)
	}
	return result, nil
}

func activeDistanceStatistics(archive searchArchive, bounds core.Bounds) (map[string]map[string]float64, error) {
	all := []core.Candidate{}
	for _, candidates := range archive {
		all = append(all, candidates...)
	}
	warmup, evaluation, total := []float64{}, []float64{}, []float64{}
	for index, candidate := range all {
		nearest := core.Distance{Warmup: math.Inf(1), Evaluation: math.Inf(1), Total: math.Inf(1)}
		for otherIndex, other := range all {
			if index == otherIndex {
				continue
			}
			distance, err := core.PathDistance(candidate.Path, other.Path, bounds)
			if err != nil {
				return nil, err
			}
			nearest.Warmup = math.Min(nearest.Warmup, distance.Warmup)
			nearest.Evaluation = math.Min(nearest.Evaluation, distance.Evaluation)
			nearest.Total = math.Min(nearest.Total, distance.Total)
		}
		if len(all) > 1 {
			warmup = append(warmup, nearest.Warmup)
			evaluation = append(evaluation, nearest.Evaluation)
			total = append(total, nearest.Total)
		}
	}
	return map[string]map[string]float64{"d_w": core.Quantiles(warmup), "d_h": core.Quantiles(evaluation), "d_total": core.Quantiles(total)}, nil
}

func shouldReport(current, total int) bool {
	if current == total {
		return true
	}
	step := int(math.Max(1, float64(total/20)))
	return current%step == 0
}
