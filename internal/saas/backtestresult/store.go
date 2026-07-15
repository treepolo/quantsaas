package backtestresult

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound         = errors.New("standardized backtest result not found")
	ErrInvalidState     = errors.New("invalid standardized backtest result state")
	ErrIntegrity        = errors.New("standardized backtest result integrity check failed")
	ErrResultReferenced = errors.New("standardized backtest result is referenced")
)

type Store struct {
	db *gorm.DB
}

type Reservation struct {
	Spec     saasstore.BacktestSpec
	Result   saasstore.BacktestResult
	Created  bool
	Reusable bool
}

type LoadedResult struct {
	Spec         saasstore.BacktestSpec
	Result       saasstore.BacktestResult
	SummaryModel *saasstore.BacktestResultSummary
	Summary      *SummaryData
	Manifest     *PathManifest
	BlockModels  []saasstore.BacktestPathBlock
	Blocks       []PathBlockData
}

type IntegrityReport struct {
	Valid            bool   `json:"valid"`
	SpecVerified     bool   `json:"spec_verified"`
	SummaryVerified  bool   `json:"summary_verified"`
	ManifestVerified bool   `json:"manifest_verified"`
	PathVerified     bool   `json:"path_verified"`
	SummaryOnly      bool   `json:"summary_only"`
	ResultID         uint   `json:"result_id"`
	ResultHash       string `json:"result_hash"`
	BlockCount       int    `json:"block_count"`
	PointCount       int    `json:"point_count"`
}

type ReferenceReport struct {
	BacktestRunIDs                    []uint `json:"backtest_run_ids"`
	PerformanceReportIDs              []uint `json:"performance_report_ids"`
	AnnualizationPerformanceReportIDs []uint `json:"annualization_performance_report_ids"`
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Reserve(ctx context.Context, identity Identity) (Reservation, error) {
	if identity.BacktestKey == "" || identity.SpecContentHash == "" || len(identity.SnapshotJSON) == 0 {
		return Reservation{}, fmt.Errorf("invalid backtest identity")
	}
	spec, err := s.ensureSpec(ctx, identity)
	if err != nil {
		return Reservation{}, err
	}
	activeKey := identity.BacktestKey + "|" + ResultSchemaVersion

	for attempt := 0; attempt < 3; attempt++ {
		candidate := saasstore.BacktestResult{
			BacktestSpecID: spec.ID,
			BacktestKey:    identity.BacktestKey,
			ActiveKey:      &activeKey,
			ResultVersion:  ResultSchemaVersion,
			Status:         saasstore.BacktestResultStatusPending,
			PathState:      saasstore.BacktestPathStatePending,
		}
		create := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "active_key"}},
			DoNothing: true,
		}).Create(&candidate)
		if create.Error != nil {
			return Reservation{}, create.Error
		}
		if create.RowsAffected == 1 {
			return Reservation{Spec: spec, Result: candidate, Created: true}, nil
		}

		var existing saasstore.BacktestResult
		if err := s.db.WithContext(ctx).Where("active_key = ?", activeKey).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return Reservation{}, err
		}
		if existing.BacktestSpecID != spec.ID || existing.BacktestKey != identity.BacktestKey || existing.ResultVersion != ResultSchemaVersion {
			return Reservation{}, fmt.Errorf("%w: active result identity mismatch", ErrIntegrity)
		}
		switch existing.Status {
		case saasstore.BacktestResultStatusCompleted:
			report, verifyErr := s.VerifyResult(ctx, existing.ID)
			if verifyErr == nil && report.Valid && report.PathVerified {
				return Reservation{Spec: spec, Result: existing, Reusable: true}, nil
			}
			reason := "completed result failed integrity verification"
			if verifyErr != nil {
				reason += ": " + verifyErr.Error()
			}
			if err := s.invalidateActive(ctx, existing.ID, reason); err != nil {
				return Reservation{}, err
			}
		case saasstore.BacktestResultStatusPending, saasstore.BacktestResultStatusRunning:
			return Reservation{Spec: spec, Result: existing}, nil
		default:
			if err := s.clearActiveKey(ctx, existing.ID); err != nil {
				return Reservation{}, err
			}
		}
	}
	return Reservation{}, fmt.Errorf("could not reserve standardized backtest result")
}

func (s *Store) MarkRunning(ctx context.Context, resultID uint) error {
	now := time.Now().UTC()
	update := s.db.WithContext(ctx).Model(&saasstore.BacktestResult{}).
		Where("id = ? AND status = ?", resultID, saasstore.BacktestResultStatusPending).
		Updates(map[string]any{
			"status":     saasstore.BacktestResultStatusRunning,
			"started_at": &now,
		})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("%w: result %d cannot start", ErrInvalidState, resultID)
	}
	return nil
}

func (s *Store) Complete(ctx context.Context, resultID uint, artifacts Artifacts) error {
	if artifacts.SummaryHash == "" || artifacts.ManifestHash == "" || artifacts.ResultContentHash == "" {
		return fmt.Errorf("incomplete standardized result artifacts")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var result saasstore.BacktestResult
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&result, resultID).Error; err != nil {
			return err
		}
		if result.Status != saasstore.BacktestResultStatusRunning && result.Status != saasstore.BacktestResultStatusPending {
			return fmt.Errorf("%w: result %d cannot complete from %s", ErrInvalidState, resultID, result.Status)
		}
		var spec saasstore.BacktestSpec
		if err := tx.First(&spec, result.BacktestSpecID).Error; err != nil {
			return err
		}
		expectedHash, err := BuildResultContentHash(spec.ContentHash, artifacts.SummaryHash, artifacts.ManifestHash, artifacts.Manifest.PointCount, artifacts.Manifest.BlockCount)
		if err != nil {
			return err
		}
		if expectedHash != artifacts.ResultContentHash {
			return fmt.Errorf("%w: result content hash mismatch before save", ErrIntegrity)
		}

		summary := summaryModel(resultID, artifacts)
		if err := tx.Create(&summary).Error; err != nil {
			return err
		}
		blocks := make([]saasstore.BacktestPathBlock, 0, len(artifacts.Blocks))
		for _, artifact := range artifacts.Blocks {
			blocks = append(blocks, pathBlockModel(resultID, artifact))
		}
		if len(blocks) > 0 {
			if err := tx.CreateInBatches(&blocks, 32).Error; err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		update := tx.Model(&saasstore.BacktestResult{}).
			Where("id = ? AND status IN ?", resultID, []string{saasstore.BacktestResultStatusPending, saasstore.BacktestResultStatusRunning}).
			Updates(map[string]any{
				"status":             saasstore.BacktestResultStatusCompleted,
				"summary_hash":       artifacts.SummaryHash,
				"path_manifest":      saasstore.JSONB(artifacts.ManifestJSON),
				"path_manifest_hash": artifacts.ManifestHash,
				"content_hash":       artifacts.ResultContentHash,
				"path_block_count":   artifacts.Manifest.BlockCount,
				"path_point_count":   artifacts.Manifest.PointCount,
				"path_state":         saasstore.BacktestPathStateAvailable,
				"completed_at":       &now,
				"error_message":      "",
			})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return fmt.Errorf("%w: result %d changed while completing", ErrInvalidState, resultID)
		}
		return nil
	})
}

func (s *Store) Fail(ctx context.Context, resultID uint, cause error) error {
	message := "backtest failed"
	if cause != nil {
		message = cause.Error()
	}
	return s.markTerminal(ctx, resultID, saasstore.BacktestResultStatusFailed, message)
}

func (s *Store) Cancel(ctx context.Context, resultID uint, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = "backtest cancelled"
	}
	return s.markTerminal(ctx, resultID, saasstore.BacktestResultStatusCancelled, reason)
}

func (s *Store) Invalidate(ctx context.Context, resultID uint, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = "result invalidated"
	}
	return s.invalidateActive(ctx, resultID, reason)
}

func (s *Store) Archive(ctx context.Context, resultID uint) error {
	now := time.Now().UTC()
	update := s.db.WithContext(ctx).Model(&saasstore.BacktestResult{}).
		Where("id = ? AND status IN ?", resultID, []string{saasstore.BacktestResultStatusCompleted, saasstore.BacktestResultStatusInvalidated}).
		Updates(map[string]any{
			"status":      saasstore.BacktestResultStatusArchived,
			"active_key":  nil,
			"archived_at": &now,
		})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("%w: result %d cannot be archived", ErrInvalidState, resultID)
	}
	return nil
}

func (s *Store) References(ctx context.Context, resultID uint) (ReferenceReport, error) {
	var ids []uint
	if err := s.db.WithContext(ctx).Model(&saasstore.BacktestRun{}).
		Where("backtest_result_id = ?", resultID).
		Order("id ASC").Pluck("id", &ids).Error; err != nil {
		return ReferenceReport{}, err
	}
	var reportIDs []uint
	if err := s.db.WithContext(ctx).Model(&saasstore.PerformanceReport{}).
		Where("backtest_result_id = ?", resultID).Order("id ASC").Pluck("id", &reportIDs).Error; err != nil {
		return ReferenceReport{}, err
	}
	var annualizationReportIDs []uint
	if err := s.db.WithContext(ctx).Model(&saasstore.PerformanceReport{}).
		Where("annualization_backtest_result_id = ?", resultID).Order("id ASC").Pluck("id", &annualizationReportIDs).Error; err != nil {
		return ReferenceReport{}, err
	}
	return ReferenceReport{BacktestRunIDs: ids, PerformanceReportIDs: reportIDs, AnnualizationPerformanceReportIDs: annualizationReportIDs}, nil
}

func (s *Store) DeleteFailed(ctx context.Context, resultID uint) (ReferenceReport, error) {
	references, err := s.References(ctx, resultID)
	if err != nil {
		return ReferenceReport{}, err
	}
	if referenceCount(references) > 0 {
		return references, fmt.Errorf("%w: %d references", ErrResultReferenced, referenceCount(references))
	}
	deleted := s.db.WithContext(ctx).Where("id = ? AND status IN ?", resultID,
		[]string{saasstore.BacktestResultStatusFailed, saasstore.BacktestResultStatusCancelled}).
		Delete(&saasstore.BacktestResult{})
	if deleted.Error != nil {
		return references, deleted.Error
	}
	if deleted.RowsAffected != 1 {
		return references, fmt.Errorf("%w: result %d is not removable", ErrInvalidState, resultID)
	}
	return references, nil
}

func (s *Store) DeletePathDetail(ctx context.Context, resultID uint, allowReferenced bool) (ReferenceReport, error) {
	references, err := s.References(ctx, resultID)
	if err != nil {
		return ReferenceReport{}, err
	}
	if referenceCount(references) > 0 && !allowReferenced {
		return references, fmt.Errorf("%w: %d references", ErrResultReferenced, referenceCount(references))
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var result saasstore.BacktestResult
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&result, resultID).Error; err != nil {
			return err
		}
		if result.PathState != saasstore.BacktestPathStateAvailable {
			return fmt.Errorf("%w: result %d has no removable path", ErrInvalidState, resultID)
		}
		if err := tx.Where("backtest_result_id = ?", resultID).Delete(&saasstore.BacktestPathBlock{}).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		return tx.Model(&saasstore.BacktestResult{}).Where("id = ?", resultID).Updates(map[string]any{
			"status":          saasstore.BacktestResultStatusArchived,
			"active_key":      nil,
			"path_state":      saasstore.BacktestPathStateDeleted,
			"path_deleted_at": &now,
			"archived_at":     &now,
		}).Error
	})
	return references, err
}

func referenceCount(report ReferenceReport) int {
	return len(report.BacktestRunIDs) + len(report.PerformanceReportIDs) + len(report.AnnualizationPerformanceReportIDs)
}

func (s *Store) Load(ctx context.Context, resultID uint, withPath bool) (LoadedResult, error) {
	var result saasstore.BacktestResult
	if err := s.db.WithContext(ctx).First(&result, resultID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return LoadedResult{}, ErrNotFound
		}
		return LoadedResult{}, err
	}
	var spec saasstore.BacktestSpec
	if err := s.db.WithContext(ctx).First(&spec, result.BacktestSpecID).Error; err != nil {
		return LoadedResult{}, err
	}
	loaded := LoadedResult{Spec: spec, Result: result}
	if result.SummaryHash != "" {
		var summaryModel saasstore.BacktestResultSummary
		if err := s.db.WithContext(ctx).Where("backtest_result_id = ?", resultID).First(&summaryModel).Error; err != nil {
			return LoadedResult{}, err
		}
		summary, err := DecodeSummary([]byte(summaryModel.Payload))
		if err != nil {
			return LoadedResult{}, fmt.Errorf("decode result summary: %w", err)
		}
		loaded.SummaryModel = &summaryModel
		loaded.Summary = &summary
	}
	if len(result.PathManifest) > 0 && string(result.PathManifest) != "{}" {
		manifest, err := DecodeManifest([]byte(result.PathManifest))
		if err != nil {
			return LoadedResult{}, fmt.Errorf("decode path manifest: %w", err)
		}
		loaded.Manifest = &manifest
	}
	if withPath && result.PathState == saasstore.BacktestPathStateAvailable {
		if err := s.db.WithContext(ctx).Where("backtest_result_id = ?", resultID).
			Order("block_index ASC").Find(&loaded.BlockModels).Error; err != nil {
			return LoadedResult{}, err
		}
		loaded.Blocks = make([]PathBlockData, 0, len(loaded.BlockModels))
		for _, model := range loaded.BlockModels {
			block, err := DecodePathBlock([]byte(model.Payload))
			if err != nil {
				return LoadedResult{}, fmt.Errorf("decode path block %d: %w", model.BlockIndex, err)
			}
			loaded.Blocks = append(loaded.Blocks, block)
		}
	}
	return loaded, nil
}

func (s *Store) LoadBlock(ctx context.Context, resultID uint, blockIndex int) (PathBlockData, saasstore.BacktestPathBlock, error) {
	var result saasstore.BacktestResult
	if err := s.db.WithContext(ctx).First(&result, resultID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PathBlockData{}, saasstore.BacktestPathBlock{}, ErrNotFound
		}
		return PathBlockData{}, saasstore.BacktestPathBlock{}, err
	}
	if result.PathState != saasstore.BacktestPathStateAvailable {
		return PathBlockData{}, saasstore.BacktestPathBlock{}, ErrNotFound
	}
	manifest, err := DecodeManifest([]byte(result.PathManifest))
	if err != nil || blockIndex < 0 || blockIndex >= len(manifest.Blocks) {
		if err == nil {
			err = fmt.Errorf("block index is outside the manifest")
		}
		return PathBlockData{}, saasstore.BacktestPathBlock{}, fmt.Errorf("%w: %v", ErrIntegrity, err)
	}
	entry := manifest.Blocks[blockIndex]
	var model saasstore.BacktestPathBlock
	err = s.db.WithContext(ctx).
		Where("backtest_result_id = ? AND block_index = ?", resultID, blockIndex).
		First(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PathBlockData{}, model, ErrNotFound
		}
		return PathBlockData{}, model, err
	}
	block, err := DecodePathBlock([]byte(model.Payload))
	if err != nil {
		return PathBlockData{}, model, err
	}
	if model.ContentHash != entry.ContentHash || model.StartPointIndex != entry.StartPointIndex ||
		model.EndPointIndex != entry.EndPointIndex || model.StartTimeMs != entry.StartTimeMs ||
		model.EndTimeMs != entry.EndTimeMs || model.PointCount != entry.PointCount {
		return PathBlockData{}, model, fmt.Errorf("%w: block metadata does not match manifest", ErrIntegrity)
	}
	canonical, err := canonicalJSON(block)
	if err != nil || hashBytes(canonical) != model.ContentHash {
		if err == nil {
			err = fmt.Errorf("block content hash mismatch")
		}
		return PathBlockData{}, model, fmt.Errorf("%w: %v", ErrIntegrity, err)
	}
	if err := validatePath(block.Points); err != nil || len(block.Points) == 0 ||
		block.Points[0].TimeMs != entry.StartTimeMs || block.Points[len(block.Points)-1].TimeMs != entry.EndTimeMs {
		if err == nil {
			err = fmt.Errorf("block point time range mismatch")
		}
		return PathBlockData{}, model, fmt.Errorf("%w: %v", ErrIntegrity, err)
	}
	return block, model, nil
}

func (s *Store) VerifyResult(ctx context.Context, resultID uint) (IntegrityReport, error) {
	loaded, err := s.Load(ctx, resultID, true)
	if err != nil {
		return IntegrityReport{}, err
	}
	return verifyRecords(loaded.Spec, loaded.Result, loaded.SummaryModel, loaded.BlockModels, true)
}

func (s *Store) VerifyMetadata(ctx context.Context, resultID uint) (IntegrityReport, error) {
	loaded, err := s.Load(ctx, resultID, false)
	if err != nil {
		return IntegrityReport{}, err
	}
	return verifyRecords(loaded.Spec, loaded.Result, loaded.SummaryModel, nil, false)
}

func VerifyRecords(spec saasstore.BacktestSpec, result saasstore.BacktestResult, summaryModel *saasstore.BacktestResultSummary, blockModels []saasstore.BacktestPathBlock) (IntegrityReport, error) {
	return verifyRecords(spec, result, summaryModel, blockModels, true)
}

func verifyRecords(spec saasstore.BacktestSpec, result saasstore.BacktestResult, summaryModel *saasstore.BacktestResultSummary, blockModels []saasstore.BacktestPathBlock, verifyPath bool) (IntegrityReport, error) {
	report := IntegrityReport{ResultID: result.ID, ResultHash: result.ContentHash, BlockCount: result.PathBlockCount, PointCount: result.PathPointCount}
	identity, err := DecodeIdentity([]byte(spec.Snapshot))
	if err != nil {
		return report, integrityError("invalid spec snapshot: %v", err)
	}
	if identity.BacktestKey != spec.BacktestKey || identity.SpecContentHash != spec.ContentHash {
		return report, integrityError("spec hash or backtest key mismatch")
	}
	if identity.Snapshot.SchemaVersion != spec.SchemaVersion ||
		identity.Snapshot.StrategyID != spec.StrategyID || identity.Snapshot.StrategyVersion != spec.StrategyVersion ||
		identity.Snapshot.InstrumentID != spec.InstrumentID || identity.Snapshot.Symbol != spec.Symbol ||
		identity.Snapshot.DataSource != spec.DataSource || identity.Snapshot.Interval != spec.Interval ||
		identity.Snapshot.ExecutionMode != spec.ExecutionMode || identity.Snapshot.StartTimeMs != spec.StartTimeMs ||
		identity.Snapshot.EndTimeMs != spec.EndTimeMs || identity.Snapshot.ParameterHash != spec.ParameterHash ||
		identity.Snapshot.DatasetHash != spec.DatasetHash || identity.Snapshot.CoreVersion != spec.CoreVersion ||
		identity.Snapshot.PositionStructure != spec.PositionStructure {
		return report, integrityError("indexed spec fields do not match immutable snapshot")
	}
	if result.BacktestSpecID != spec.ID || result.BacktestKey != spec.BacktestKey || result.ResultVersion != ResultSchemaVersion {
		return report, integrityError("result does not reference the expected spec/version")
	}
	report.SpecVerified = true

	if result.Status == saasstore.BacktestResultStatusPending || result.Status == saasstore.BacktestResultStatusRunning ||
		result.Status == saasstore.BacktestResultStatusFailed || result.Status == saasstore.BacktestResultStatusCancelled {
		if (result.Status == saasstore.BacktestResultStatusPending || result.Status == saasstore.BacktestResultStatusRunning) && result.ActiveKey == nil {
			return report, integrityError("active result is missing active key")
		}
		if (result.Status == saasstore.BacktestResultStatusFailed || result.Status == saasstore.BacktestResultStatusCancelled) && result.ActiveKey != nil {
			return report, integrityError("terminal incomplete result still has active key")
		}
		if result.SummaryHash != "" || result.ContentHash != "" || result.PathBlockCount != 0 || result.PathPointCount != 0 {
			return report, integrityError("incomplete result unexpectedly has immutable output content")
		}
		report.Valid = true
		return report, nil
	}
	if result.Status != saasstore.BacktestResultStatusCompleted && result.Status != saasstore.BacktestResultStatusInvalidated && result.Status != saasstore.BacktestResultStatusArchived {
		return report, integrityError("unknown result status %q", result.Status)
	}
	if result.Status == saasstore.BacktestResultStatusCompleted && result.ActiveKey == nil {
		return report, integrityError("completed current result is missing active key")
	}
	if (result.Status == saasstore.BacktestResultStatusInvalidated || result.Status == saasstore.BacktestResultStatusArchived) && result.ActiveKey != nil {
		return report, integrityError("inactive result still has active key")
	}
	if summaryModel == nil {
		return report, integrityError("completed result is missing summary")
	}
	if summaryModel.BacktestResultID != result.ID || summaryModel.SchemaVersion != SummarySchemaVersion {
		return report, integrityError("summary identity mismatch")
	}
	summary, err := DecodeSummary([]byte(summaryModel.Payload))
	if err != nil {
		return report, integrityError("invalid summary payload: %v", err)
	}
	summaryJSON, err := canonicalJSON(summary)
	if err != nil || hashBytes(summaryJSON) != summaryModel.ContentHash || summaryModel.ContentHash != result.SummaryHash {
		return report, integrityError("summary content hash mismatch")
	}
	if !summaryColumnsMatch(summary, *summaryModel) {
		return report, integrityError("summary query columns do not match immutable payload")
	}
	report.SummaryVerified = true

	manifest, err := DecodeManifest([]byte(result.PathManifest))
	if err != nil {
		return report, integrityError("invalid path manifest: %v", err)
	}
	manifestJSON, err := canonicalJSON(manifest)
	if err != nil || hashBytes(manifestJSON) != result.PathManifestHash {
		return report, integrityError("path manifest hash mismatch")
	}
	if manifest.SchemaVersion != ManifestSchemaVersion || manifest.BlockCount != result.PathBlockCount ||
		manifest.PointCount != result.PathPointCount || len(manifest.Blocks) != manifest.BlockCount {
		return report, integrityError("path manifest totals mismatch")
	}
	if err := validateManifestOrder(manifest); err != nil {
		return report, integrityError("invalid path manifest order: %v", err)
	}
	report.ManifestVerified = true

	resultHash, err := BuildResultContentHash(spec.ContentHash, summaryModel.ContentHash, result.PathManifestHash, manifest.PointCount, manifest.BlockCount)
	if err != nil || resultHash != result.ContentHash {
		return report, integrityError("result content hash mismatch")
	}
	if !verifyPath {
		report.SummaryOnly = result.PathState == saasstore.BacktestPathStateDeleted
		report.Valid = true
		return report, nil
	}
	if result.PathState == saasstore.BacktestPathStateDeleted {
		if len(blockModels) != 0 {
			return report, integrityError("deleted path still has blocks")
		}
		report.SummaryOnly = true
		report.Valid = true
		return report, nil
	}
	if result.PathState != saasstore.BacktestPathStateAvailable {
		return report, integrityError("completed result has invalid path state %q", result.PathState)
	}
	if len(blockModels) != manifest.BlockCount {
		return report, integrityError("path block count mismatch")
	}
	for index, model := range blockModels {
		entry := manifest.Blocks[index]
		if model.BacktestResultID != result.ID || model.BlockIndex != index || model.BlockIndex != entry.BlockIndex ||
			model.SchemaVersion != PathSchemaVersion || model.StartPointIndex != entry.StartPointIndex ||
			model.EndPointIndex != entry.EndPointIndex || model.StartTimeMs != entry.StartTimeMs ||
			model.EndTimeMs != entry.EndTimeMs || model.PointCount != entry.PointCount || model.ContentHash != entry.ContentHash {
			return report, integrityError("path block %d metadata mismatch", index)
		}
		block, err := DecodePathBlock([]byte(model.Payload))
		if err != nil {
			return report, integrityError("invalid path block %d: %v", index, err)
		}
		blockJSON, err := canonicalJSON(block)
		if err != nil || hashBytes(blockJSON) != model.ContentHash {
			return report, integrityError("path block %d content hash mismatch", index)
		}
		if block.BlockIndex != index || block.StartPointIndex != entry.StartPointIndex || block.EndPointIndex != entry.EndPointIndex ||
			block.StartTimeMs != entry.StartTimeMs || block.EndTimeMs != entry.EndTimeMs || len(block.Points) != entry.PointCount {
			return report, integrityError("path block %d payload metadata mismatch", index)
		}
		if err := validatePath(block.Points); err != nil {
			return report, integrityError("path block %d semantic validation failed: %v", index, err)
		}
		if len(block.Points) == 0 || block.Points[0].TimeMs != entry.StartTimeMs || block.Points[len(block.Points)-1].TimeMs != entry.EndTimeMs {
			return report, integrityError("path block %d point time range mismatch", index)
		}
	}
	report.PathVerified = true
	report.Valid = true
	return report, nil
}

func (loaded LoadedResult) Path() []PathPoint {
	count := 0
	for _, block := range loaded.Blocks {
		count += len(block.Points)
	}
	path := make([]PathPoint, 0, count)
	for _, block := range loaded.Blocks {
		path = append(path, block.Points...)
	}
	return path
}

func (s *Store) ensureSpec(ctx context.Context, identity Identity) (saasstore.BacktestSpec, error) {
	model := specModel(identity)
	create := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "backtest_key"}},
		DoNothing: true,
	}).Create(&model)
	if create.Error != nil {
		return saasstore.BacktestSpec{}, create.Error
	}
	var stored saasstore.BacktestSpec
	if err := s.db.WithContext(ctx).Where("backtest_key = ?", identity.BacktestKey).First(&stored).Error; err != nil {
		return saasstore.BacktestSpec{}, err
	}
	if stored.ContentHash != identity.SpecContentHash {
		return saasstore.BacktestSpec{}, fmt.Errorf("%w: existing spec hash mismatch", ErrIntegrity)
	}
	verified, err := DecodeIdentity([]byte(stored.Snapshot))
	if err != nil || verified.BacktestKey != identity.BacktestKey {
		return saasstore.BacktestSpec{}, fmt.Errorf("%w: existing spec payload mismatch", ErrIntegrity)
	}
	return stored, nil
}

func (s *Store) markTerminal(ctx context.Context, resultID uint, status string, message string) error {
	now := time.Now().UTC()
	update := s.db.WithContext(ctx).Model(&saasstore.BacktestResult{}).
		Where("id = ? AND status IN ?", resultID, []string{saasstore.BacktestResultStatusPending, saasstore.BacktestResultStatusRunning}).
		Updates(map[string]any{
			"status":        status,
			"active_key":    nil,
			"error_message": message,
			"completed_at":  &now,
		})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("%w: result %d cannot transition to %s", ErrInvalidState, resultID, status)
	}
	return nil
}

func (s *Store) invalidateActive(ctx context.Context, resultID uint, reason string) error {
	now := time.Now().UTC()
	update := s.db.WithContext(ctx).Model(&saasstore.BacktestResult{}).
		Where("id = ? AND status = ?", resultID, saasstore.BacktestResultStatusCompleted).
		Updates(map[string]any{
			"status":              saasstore.BacktestResultStatusInvalidated,
			"active_key":          nil,
			"invalidated_at":      &now,
			"invalidation_reason": reason,
		})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("%w: result %d cannot be invalidated", ErrInvalidState, resultID)
	}
	return nil
}

func (s *Store) clearActiveKey(ctx context.Context, resultID uint) error {
	return s.db.WithContext(ctx).Model(&saasstore.BacktestResult{}).Where("id = ?", resultID).Update("active_key", nil).Error
}

func specModel(identity Identity) saasstore.BacktestSpec {
	snapshot := identity.Snapshot
	return saasstore.BacktestSpec{
		BacktestKey:       identity.BacktestKey,
		SchemaVersion:     snapshot.SchemaVersion,
		ContentHash:       identity.SpecContentHash,
		StrategyID:        snapshot.StrategyID,
		StrategyVersion:   snapshot.StrategyVersion,
		InstrumentID:      snapshot.InstrumentID,
		Symbol:            snapshot.Symbol,
		DataSource:        snapshot.DataSource,
		Interval:          snapshot.Interval,
		ExecutionMode:     snapshot.ExecutionMode,
		StartTimeMs:       snapshot.StartTimeMs,
		EndTimeMs:         snapshot.EndTimeMs,
		ParameterHash:     snapshot.ParameterHash,
		DatasetHash:       snapshot.DatasetHash,
		CoreVersion:       snapshot.CoreVersion,
		PositionStructure: snapshot.PositionStructure,
		Snapshot:          saasstore.JSONB(identity.SnapshotJSON),
	}
}

func summaryModel(resultID uint, artifacts Artifacts) saasstore.BacktestResultSummary {
	summary := artifacts.Summary
	return saasstore.BacktestResultSummary{
		BacktestResultID:        resultID,
		SchemaVersion:           SummarySchemaVersion,
		ContentHash:             artifacts.SummaryHash,
		ROI:                     summary.ROI,
		FinalEquity:             summary.FinalEquity,
		MaxDrawdown:             summary.MaxDrawdown,
		TradeCount:              summary.TradeCount,
		ExposureDaysRatio:       summary.ExposureDaysRatio,
		AverageActualExposure:   summary.AverageActualExposure,
		LongestUnderwaterDays:   summary.LongestUnderwaterDays,
		LongestUnderwaterPoints: summary.LongestUnderwaterPoints,
		Sortino:                 cloneFloat(summary.Sortino),
		Beta:                    cloneFloat(summary.Beta),
		Payload:                 saasstore.JSONB(artifacts.SummaryJSON),
	}
}

func pathBlockModel(resultID uint, artifact PathBlockArtifact) saasstore.BacktestPathBlock {
	data := artifact.Data
	return saasstore.BacktestPathBlock{
		BacktestResultID: resultID,
		BlockIndex:       data.BlockIndex,
		SchemaVersion:    PathSchemaVersion,
		StartPointIndex:  data.StartPointIndex,
		EndPointIndex:    data.EndPointIndex,
		StartTimeMs:      data.StartTimeMs,
		EndTimeMs:        data.EndTimeMs,
		PointCount:       len(data.Points),
		ContentHash:      artifact.ContentHash,
		Payload:          saasstore.JSONB(artifact.PayloadJSON),
	}
}

func validateManifestOrder(manifest PathManifest) error {
	nextPoint := 0
	previousEndTime := int64(0)
	for index, entry := range manifest.Blocks {
		if entry.BlockIndex != index || entry.StartPointIndex != nextPoint || entry.EndPointIndex < entry.StartPointIndex {
			return fmt.Errorf("block %d is not contiguous", index)
		}
		if entry.PointCount != entry.EndPointIndex-entry.StartPointIndex+1 {
			return fmt.Errorf("block %d point count mismatch", index)
		}
		if entry.StartTimeMs > entry.EndTimeMs || strings.TrimSpace(entry.ContentHash) == "" {
			return fmt.Errorf("block %d metadata is invalid", index)
		}
		if index > 0 && entry.StartTimeMs <= previousEndTime {
			return fmt.Errorf("block %d time range overlaps the previous block", index)
		}
		previousEndTime = entry.EndTimeMs
		nextPoint = entry.EndPointIndex + 1
	}
	if nextPoint != manifest.PointCount {
		return fmt.Errorf("manifest point range is incomplete")
	}
	return nil
}

func summaryColumnsMatch(summary SummaryData, model saasstore.BacktestResultSummary) bool {
	return nearlyEqual(summary.ROI, model.ROI) && nearlyEqual(summary.FinalEquity, model.FinalEquity) &&
		nearlyEqual(summary.MaxDrawdown, model.MaxDrawdown) && summary.TradeCount == model.TradeCount &&
		nearlyEqual(summary.ExposureDaysRatio, model.ExposureDaysRatio) &&
		nearlyEqual(summary.AverageActualExposure, model.AverageActualExposure) &&
		nearlyEqual(summary.LongestUnderwaterDays, model.LongestUnderwaterDays) &&
		summary.LongestUnderwaterPoints == model.LongestUnderwaterPoints &&
		optionalFloatEqual(summary.Sortino, model.Sortino) && optionalFloatEqual(summary.Beta, model.Beta)
}

func optionalFloatEqual(left *float64, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return nearlyEqual(*left, *right)
}

func nearlyEqual(left float64, right float64) bool {
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= 1e-12*scale
}

func integrityError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrIntegrity, fmt.Sprintf(format, args...))
}
