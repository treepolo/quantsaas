package performancereport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	performancecore "quantsaas/internal/performance"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound     = errors.New("performance report not found")
	ErrInvalidState = errors.New("invalid performance report state")
	ErrIntegrity    = errors.New("performance report integrity check failed")
)

type Store struct {
	db *gorm.DB
}

type Reservation struct {
	Report   saasstore.PerformanceReport
	Created  bool
	Reusable bool
}

type LoadedReport struct {
	Report       saasstore.PerformanceReport
	Settings     ResolvedSettings
	SummaryModel *saasstore.PerformanceReportSummary
	Summary      *performancecore.Summary
	Manifest     *ChartManifest
	ChartModels  []saasstore.PerformanceReportChartBlock
	Charts       map[string]json.RawMessage
}

type IntegrityReport struct {
	Valid            bool   `json:"valid"`
	IdentityVerified bool   `json:"identity_verified"`
	SummaryVerified  bool   `json:"summary_verified"`
	ManifestVerified bool   `json:"manifest_verified"`
	ChartsVerified   bool   `json:"charts_verified"`
	ReportID         uint   `json:"report_id"`
	ContentHash      string `json:"content_hash"`
	ChartBlockCount  int    `json:"chart_block_count"`
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Reserve(ctx context.Context, identity Identity) (Reservation, error) {
	if identity.AnalysisKey == "" || identity.Snapshot.SettingsHash == "" || len(identity.SettingsJSON) == 0 {
		return Reservation{}, fmt.Errorf("invalid performance report identity")
	}
	activeKey := identity.AnalysisKey + "|" + performancecore.ReportSchemaVersion
	for attempt := 0; attempt < 3; attempt++ {
		candidate := saasstore.PerformanceReport{
			BacktestResultID:              identity.Snapshot.BacktestResultID,
			AnnualizationBacktestResultID: identity.Snapshot.AnnualizationBacktestResultID,
			AnalysisKey:                   identity.AnalysisKey, ActiveKey: &activeKey,
			AnalysisVersion: performancecore.AnalysisVersion, SchemaVersion: performancecore.ReportSchemaVersion,
			SettingsHash: identity.Snapshot.SettingsHash, Settings: saasstore.JSONB(identity.SettingsJSON),
			SourceResultContentHash:        identity.Snapshot.BacktestResultContentHash,
			AnnualizationResultContentHash: identity.Snapshot.AnnualizationResultContentHash,
			Status:                         saasstore.PerformanceReportStatusPending,
		}
		created := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "active_key"}}, DoNothing: true}).Create(&candidate)
		if created.Error != nil {
			return Reservation{}, created.Error
		}
		if created.RowsAffected == 1 {
			return Reservation{Report: candidate, Created: true}, nil
		}
		var existing saasstore.PerformanceReport
		if err := s.db.WithContext(ctx).Where("active_key = ?", activeKey).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return Reservation{}, err
		}
		if existing.AnalysisKey != identity.AnalysisKey || existing.SettingsHash != identity.Snapshot.SettingsHash ||
			existing.BacktestResultID != identity.Snapshot.BacktestResultID || existing.AnnualizationBacktestResultID != identity.Snapshot.AnnualizationBacktestResultID {
			return Reservation{}, fmt.Errorf("%w: active report identity mismatch", ErrIntegrity)
		}
		switch existing.Status {
		case saasstore.PerformanceReportStatusCompleted:
			report, verifyErr := s.Verify(ctx, existing.ID, true)
			if verifyErr == nil && report.Valid && report.ChartsVerified {
				return Reservation{Report: existing, Reusable: true}, nil
			}
			reason := "completed report failed integrity verification"
			if verifyErr != nil {
				reason += ": " + verifyErr.Error()
			}
			if err := s.invalidateActive(ctx, existing.ID, reason); err != nil {
				return Reservation{}, err
			}
		case saasstore.PerformanceReportStatusPending, saasstore.PerformanceReportStatusRunning:
			return Reservation{Report: existing}, nil
		default:
			if err := s.db.WithContext(ctx).Model(&saasstore.PerformanceReport{}).Where("id = ?", existing.ID).Update("active_key", nil).Error; err != nil {
				return Reservation{}, err
			}
		}
	}
	return Reservation{}, fmt.Errorf("could not reserve performance report")
}

func (s *Store) MarkRunning(ctx context.Context, reportID uint) error {
	now := time.Now().UTC()
	update := s.db.WithContext(ctx).Model(&saasstore.PerformanceReport{}).
		Where("id = ? AND status = ?", reportID, saasstore.PerformanceReportStatusPending).
		Updates(map[string]any{"status": saasstore.PerformanceReportStatusRunning, "started_at": &now})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("%w: report %d cannot start", ErrInvalidState, reportID)
	}
	return nil
}

func (s *Store) Complete(ctx context.Context, reportID uint, identity Identity, artifacts Artifacts) error {
	if artifacts.SummaryHash == "" || artifacts.ManifestHash == "" || artifacts.ReportContentHash == "" {
		return fmt.Errorf("incomplete performance report artifacts")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var report saasstore.PerformanceReport
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&report, reportID).Error; err != nil {
			return err
		}
		if report.Status != saasstore.PerformanceReportStatusPending && report.Status != saasstore.PerformanceReportStatusRunning {
			return fmt.Errorf("%w: report %d cannot complete from %s", ErrInvalidState, reportID, report.Status)
		}
		if report.AnalysisKey != identity.AnalysisKey || report.SettingsHash != identity.Snapshot.SettingsHash {
			return fmt.Errorf("%w: report identity changed before completion", ErrIntegrity)
		}
		expectedHash, err := BuildReportContentHash(identity, artifacts.SummaryHash, artifacts.ManifestHash)
		if err != nil {
			return err
		}
		if expectedHash != artifacts.ReportContentHash {
			return fmt.Errorf("%w: report content hash mismatch before save", ErrIntegrity)
		}
		summary := summaryModel(reportID, artifacts)
		if err := tx.Create(&summary).Error; err != nil {
			return err
		}
		blocks := make([]saasstore.PerformanceReportChartBlock, 0, len(artifacts.Charts))
		for _, artifact := range artifacts.Charts {
			blocks = append(blocks, chartModel(reportID, artifact))
		}
		if err := tx.CreateInBatches(&blocks, 16).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		update := tx.Model(&saasstore.PerformanceReport{}).
			Where("id = ? AND status IN ?", reportID, []string{saasstore.PerformanceReportStatusPending, saasstore.PerformanceReportStatusRunning}).
			Updates(map[string]any{
				"status": saasstore.PerformanceReportStatusCompleted, "summary_hash": artifacts.SummaryHash,
				"chart_manifest": saasstore.JSONB(artifacts.ManifestJSON), "chart_manifest_hash": artifacts.ManifestHash,
				"content_hash": artifacts.ReportContentHash, "completed_at": &now, "error_message": "",
			})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return fmt.Errorf("%w: report %d changed while completing", ErrInvalidState, reportID)
		}
		return nil
	})
}

func (s *Store) Fail(ctx context.Context, reportID uint, cause error) error {
	message := "performance analysis failed"
	if cause != nil {
		message = cause.Error()
	}
	return s.markTerminal(ctx, reportID, saasstore.PerformanceReportStatusFailed, message)
}

func (s *Store) Cancel(ctx context.Context, reportID uint, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = "performance analysis cancelled"
	}
	return s.markTerminal(ctx, reportID, saasstore.PerformanceReportStatusCancelled, reason)
}

func (s *Store) Invalidate(ctx context.Context, reportID uint, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = "performance report invalidated"
	}
	return s.invalidateActive(ctx, reportID, reason)
}

func (s *Store) Archive(ctx context.Context, reportID uint) error {
	now := time.Now().UTC()
	update := s.db.WithContext(ctx).Model(&saasstore.PerformanceReport{}).
		Where("id = ? AND status IN ?", reportID, []string{saasstore.PerformanceReportStatusCompleted, saasstore.PerformanceReportStatusInvalidated}).
		Updates(map[string]any{"status": saasstore.PerformanceReportStatusArchived, "active_key": nil, "archived_at": &now})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("%w: report %d cannot be archived", ErrInvalidState, reportID)
	}
	return nil
}

func (s *Store) Load(ctx context.Context, reportID uint, withCharts bool) (LoadedReport, error) {
	var report saasstore.PerformanceReport
	if err := s.db.WithContext(ctx).First(&report, reportID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return LoadedReport{}, ErrNotFound
		}
		return LoadedReport{}, err
	}
	var settings ResolvedSettings
	if err := json.Unmarshal(report.Settings, &settings); err != nil {
		return LoadedReport{}, fmt.Errorf("decode performance settings: %w", err)
	}
	loaded := LoadedReport{Report: report, Settings: settings}
	if report.SummaryHash != "" {
		var model saasstore.PerformanceReportSummary
		if err := s.db.WithContext(ctx).Where("performance_report_id = ?", reportID).First(&model).Error; err != nil {
			return LoadedReport{}, err
		}
		summary, err := DecodeSummary(model.Payload)
		if err != nil {
			return LoadedReport{}, err
		}
		loaded.SummaryModel = &model
		loaded.Summary = &summary
	}
	if len(report.ChartManifest) > 0 && string(report.ChartManifest) != "{}" {
		manifest, err := DecodeChartManifest(report.ChartManifest)
		if err != nil {
			return LoadedReport{}, err
		}
		loaded.Manifest = &manifest
	}
	if withCharts && loaded.Manifest != nil {
		if err := s.db.WithContext(ctx).Where("performance_report_id = ?", reportID).Order("id ASC").Find(&loaded.ChartModels).Error; err != nil {
			return LoadedReport{}, err
		}
		loaded.Charts = make(map[string]json.RawMessage, len(loaded.ChartModels))
		for _, model := range loaded.ChartModels {
			loaded.Charts[model.Kind] = append(json.RawMessage(nil), model.Payload...)
		}
	}
	return loaded, nil
}

func (s *Store) LoadChart(ctx context.Context, reportID uint, kind string) (json.RawMessage, saasstore.PerformanceReportChartBlock, error) {
	loaded, err := s.Load(ctx, reportID, false)
	if err != nil {
		return nil, saasstore.PerformanceReportChartBlock{}, err
	}
	if loaded.Manifest == nil {
		return nil, saasstore.PerformanceReportChartBlock{}, ErrNotFound
	}
	var entry *ChartManifestEntry
	for index := range loaded.Manifest.Blocks {
		if loaded.Manifest.Blocks[index].Kind == kind {
			entry = &loaded.Manifest.Blocks[index]
			break
		}
	}
	if entry == nil {
		return nil, saasstore.PerformanceReportChartBlock{}, ErrNotFound
	}
	var model saasstore.PerformanceReportChartBlock
	if err := s.db.WithContext(ctx).Where("performance_report_id = ? AND kind = ?", reportID, kind).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, model, ErrNotFound
		}
		return nil, model, err
	}
	canonical, err := canonicalRawJSON(model.Payload)
	if err != nil || hashBytes(canonical) != model.ContentHash || model.ContentHash != entry.ContentHash || model.PointCount != entry.PointCount {
		return nil, model, fmt.Errorf("%w: chart %s content mismatch", ErrIntegrity, kind)
	}
	return append(json.RawMessage(nil), model.Payload...), model, nil
}

func (s *Store) ListForResult(ctx context.Context, resultID uint) ([]saasstore.PerformanceReport, error) {
	var reports []saasstore.PerformanceReport
	if err := s.db.WithContext(ctx).Where("backtest_result_id = ?", resultID).Order("created_at DESC, id DESC").Find(&reports).Error; err != nil {
		return nil, err
	}
	return reports, nil
}

func (s *Store) Verify(ctx context.Context, reportID uint, verifyCharts bool) (IntegrityReport, error) {
	loaded, err := s.Load(ctx, reportID, verifyCharts)
	if err != nil {
		return IntegrityReport{}, err
	}
	return verifyRecords(s.db.WithContext(ctx), loaded, verifyCharts)
}

func verifyRecords(db *gorm.DB, loaded LoadedReport, verifyCharts bool) (IntegrityReport, error) {
	report := loaded.Report
	integrity := IntegrityReport{ReportID: report.ID, ContentHash: report.ContentHash}
	var source, annualization saasstore.BacktestResult
	if err := db.First(&source, report.BacktestResultID).Error; err != nil {
		return integrity, integrityError("source result missing: %v", err)
	}
	if err := db.First(&annualization, report.AnnualizationBacktestResultID).Error; err != nil {
		return integrity, integrityError("annualization result missing: %v", err)
	}
	identity, err := BuildIdentity(IdentitySnapshot{
		BacktestResultID: report.BacktestResultID, BacktestResultVersion: source.ResultVersion,
		BacktestResultContentHash:          report.SourceResultContentHash,
		AnnualizationBacktestResultID:      report.AnnualizationBacktestResultID,
		AnnualizationBacktestResultVersion: annualization.ResultVersion,
		AnnualizationResultContentHash:     report.AnnualizationResultContentHash,
		Settings:                           loaded.Settings,
	})
	if err != nil || identity.AnalysisKey != report.AnalysisKey || identity.Snapshot.SettingsHash != report.SettingsHash ||
		source.ContentHash != report.SourceResultContentHash || annualization.ContentHash != report.AnnualizationResultContentHash ||
		report.AnalysisVersion != performancecore.AnalysisVersion || report.SchemaVersion != performancecore.ReportSchemaVersion {
		return integrity, integrityError("report identity mismatch")
	}
	integrity.IdentityVerified = true

	if report.Status == saasstore.PerformanceReportStatusPending || report.Status == saasstore.PerformanceReportStatusRunning ||
		report.Status == saasstore.PerformanceReportStatusFailed || report.Status == saasstore.PerformanceReportStatusCancelled {
		if (report.Status == saasstore.PerformanceReportStatusPending || report.Status == saasstore.PerformanceReportStatusRunning) && report.ActiveKey == nil {
			return integrity, integrityError("active report is missing active key")
		}
		if (report.Status == saasstore.PerformanceReportStatusFailed || report.Status == saasstore.PerformanceReportStatusCancelled) && report.ActiveKey != nil {
			return integrity, integrityError("terminal incomplete report still has active key")
		}
		if report.SummaryHash != "" || report.ContentHash != "" {
			return integrity, integrityError("incomplete report unexpectedly has immutable content")
		}
		integrity.Valid = true
		return integrity, nil
	}
	if report.Status != saasstore.PerformanceReportStatusCompleted && report.Status != saasstore.PerformanceReportStatusInvalidated && report.Status != saasstore.PerformanceReportStatusArchived {
		return integrity, integrityError("unknown report status %q", report.Status)
	}
	if report.Status == saasstore.PerformanceReportStatusCompleted && report.ActiveKey == nil {
		return integrity, integrityError("completed report is missing active key")
	}
	if report.Status != saasstore.PerformanceReportStatusCompleted && report.ActiveKey != nil {
		return integrity, integrityError("inactive report still has active key")
	}
	if loaded.SummaryModel == nil || loaded.Summary == nil {
		return integrity, integrityError("completed report is missing summary")
	}
	summaryJSON, err := canonicalJSON(*loaded.Summary)
	if err != nil || hashBytes(summaryJSON) != loaded.SummaryModel.ContentHash || loaded.SummaryModel.ContentHash != report.SummaryHash || !summaryColumnsMatch(*loaded.Summary, *loaded.SummaryModel) {
		return integrity, integrityError("summary content mismatch")
	}
	integrity.SummaryVerified = true
	if loaded.Manifest == nil {
		return integrity, integrityError("completed report is missing chart manifest")
	}
	manifestJSON, err := canonicalJSON(*loaded.Manifest)
	if err != nil || hashBytes(manifestJSON) != report.ChartManifestHash || loaded.Manifest.BlockCount != len(loaded.Manifest.Blocks) {
		return integrity, integrityError("chart manifest mismatch")
	}
	seen := map[string]bool{}
	for _, entry := range loaded.Manifest.Blocks {
		if entry.Kind == "" || entry.SchemaVersion != performancecore.ChartSchemaVersion || entry.ContentHash == "" || entry.PointCount < 0 || seen[entry.Kind] {
			return integrity, integrityError("invalid chart manifest entry %q", entry.Kind)
		}
		seen[entry.Kind] = true
	}
	integrity.ManifestVerified = true
	expectedContentHash, err := BuildReportContentHash(identity, report.SummaryHash, report.ChartManifestHash)
	if err != nil || expectedContentHash != report.ContentHash {
		return integrity, integrityError("report content hash mismatch")
	}
	if !verifyCharts {
		integrity.Valid = true
		return integrity, nil
	}
	if len(loaded.ChartModels) != loaded.Manifest.BlockCount {
		return integrity, integrityError("chart block count mismatch")
	}
	models := make(map[string]saasstore.PerformanceReportChartBlock, len(loaded.ChartModels))
	for _, model := range loaded.ChartModels {
		models[model.Kind] = model
	}
	for _, entry := range loaded.Manifest.Blocks {
		model, ok := models[entry.Kind]
		if !ok || model.PerformanceReportID != report.ID || model.SchemaVersion != entry.SchemaVersion || model.ContentHash != entry.ContentHash || model.PointCount != entry.PointCount {
			return integrity, integrityError("chart %s metadata mismatch", entry.Kind)
		}
		canonical, err := canonicalRawJSON(model.Payload)
		if err != nil || hashBytes(canonical) != model.ContentHash {
			return integrity, integrityError("chart %s content mismatch", entry.Kind)
		}
	}
	integrity.ChartBlockCount = len(loaded.ChartModels)
	integrity.ChartsVerified = true
	integrity.Valid = true
	return integrity, nil
}

func (s *Store) markTerminal(ctx context.Context, reportID uint, status string, message string) error {
	now := time.Now().UTC()
	update := s.db.WithContext(ctx).Model(&saasstore.PerformanceReport{}).
		Where("id = ? AND status IN ?", reportID, []string{saasstore.PerformanceReportStatusPending, saasstore.PerformanceReportStatusRunning}).
		Updates(map[string]any{"status": status, "active_key": nil, "error_message": message, "completed_at": &now})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("%w: report %d cannot transition to %s", ErrInvalidState, reportID, status)
	}
	return nil
}

func (s *Store) invalidateActive(ctx context.Context, reportID uint, reason string) error {
	now := time.Now().UTC()
	update := s.db.WithContext(ctx).Model(&saasstore.PerformanceReport{}).
		Where("id = ? AND status = ?", reportID, saasstore.PerformanceReportStatusCompleted).
		Updates(map[string]any{"status": saasstore.PerformanceReportStatusInvalidated, "active_key": nil, "invalidated_at": &now, "invalidation_reason": reason})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("%w: report %d cannot be invalidated", ErrInvalidState, reportID)
	}
	return nil
}

func summaryModel(reportID uint, artifacts Artifacts) saasstore.PerformanceReportSummary {
	summary := artifacts.Summary
	return saasstore.PerformanceReportSummary{
		PerformanceReportID: reportID, SchemaVersion: performancecore.SummarySchemaVersion,
		ContentHash: artifacts.SummaryHash, FinalNAVRatio: summary.Relative.FinalNAVRatio,
		LogFinalNAVRatio:               summary.Relative.LogFinalNAVRatio,
		StrategyNoCashFlowAnnualized:   cloneFloat(summary.Relative.StrategyNoCashFlowAnnualized),
		BenchmarkNoCashFlowAnnualized:  cloneFloat(summary.Relative.BenchmarkNoCashFlowAnnualized),
		NoCashFlowAnnualizedDifference: cloneFloat(summary.Relative.NoCashFlowAnnualizedDifference),
		Sortino:                        cloneFloat(summary.Sortino.Value), Beta: cloneFloat(summary.Beta.Value),
		LongestUnderwaterDays:  summary.LongestUnderwater.LongestDays,
		ExposureDaysRatio:      summary.Exposure.ExposureDaysRatio,
		AverageActualExposure:  summary.Exposure.AverageActualExposure,
		ExposureAdjustedReturn: cloneFloat(summary.Exposure.ExposureAdjustedReturn),
		Payload:                saasstore.JSONB(artifacts.SummaryJSON),
	}
}

func chartModel(reportID uint, artifact ChartArtifact) saasstore.PerformanceReportChartBlock {
	return saasstore.PerformanceReportChartBlock{
		PerformanceReportID: reportID, Kind: artifact.Kind, SchemaVersion: artifact.SchemaVersion,
		ContentHash: artifact.ContentHash, PointCount: artifact.PointCount, Payload: saasstore.JSONB(artifact.PayloadJSON),
	}
}

func summaryColumnsMatch(summary performancecore.Summary, model saasstore.PerformanceReportSummary) bool {
	return nearlyEqual(summary.Relative.FinalNAVRatio, model.FinalNAVRatio) && nearlyEqual(summary.Relative.LogFinalNAVRatio, model.LogFinalNAVRatio) &&
		optionalEqual(summary.Relative.StrategyNoCashFlowAnnualized, model.StrategyNoCashFlowAnnualized) &&
		optionalEqual(summary.Relative.BenchmarkNoCashFlowAnnualized, model.BenchmarkNoCashFlowAnnualized) &&
		optionalEqual(summary.Relative.NoCashFlowAnnualizedDifference, model.NoCashFlowAnnualizedDifference) &&
		optionalEqual(summary.Sortino.Value, model.Sortino) && optionalEqual(summary.Beta.Value, model.Beta) &&
		nearlyEqual(summary.LongestUnderwater.LongestDays, model.LongestUnderwaterDays) &&
		nearlyEqual(summary.Exposure.ExposureDaysRatio, model.ExposureDaysRatio) &&
		nearlyEqual(summary.Exposure.AverageActualExposure, model.AverageActualExposure) &&
		optionalEqual(summary.Exposure.ExposureAdjustedReturn, model.ExposureAdjustedReturn)
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func optionalEqual(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return nearlyEqual(*left, *right)
}

func nearlyEqual(left, right float64) bool {
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= 1e-12*scale
}

func integrityError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrIntegrity, fmt.Sprintf(format, args...))
}
