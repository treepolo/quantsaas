package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	"quantsaas/internal/marketversion"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	generationStageExpand  = "expand"
	generationStageAudit   = "calendar-audit"
	generationStagePublish = "publish"
)

type RecompositionGenerationExecutor struct {
	service *Service
	stage   string
}

func NewRecompositionExpandExecutor(service *Service) *RecompositionGenerationExecutor {
	return &RecompositionGenerationExecutor{service: service, stage: generationStageExpand}
}

func NewRecompositionAuditExecutor(service *Service) *RecompositionGenerationExecutor {
	return &RecompositionGenerationExecutor{service: service, stage: generationStageAudit}
}

func NewRecompositionPublishExecutor(service *Service) *RecompositionGenerationExecutor {
	return &RecompositionGenerationExecutor{service: service, stage: generationStagePublish}
}

func (e *RecompositionGenerationExecutor) Descriptor() compute.ExecutorDescriptor {
	executorType := RecompositionExpandExecutorType
	switch e.stage {
	case generationStageAudit:
		executorType = RecompositionAuditExecutorType
	case generationStagePublish:
		executorType = RecompositionPublishExecutorType
	}
	return compute.ExecutorDescriptor{Type: executorType, Version: RecompositionGenerationExecutorV1, ResultSchemaVersion: RecompositionGenerationResultV1}
}

func (e *RecompositionGenerationExecutor) Execute(ctx context.Context, execution computetask.Execution) (json.RawMessage, error) {
	if e == nil || e.service == nil || e.service.db == nil {
		return nil, computetask.ErrServiceUnavailable
	}
	var input RecompositionGenerationExecutionInput
	if err := json.Unmarshal(execution.Input, &input); err != nil {
		return nil, err
	}
	if err := validateGenerationExecutionInput(input); err != nil {
		return nil, err
	}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: 0.02}); err != nil {
			return nil, err
		}
	}
	var contentHash string
	var err error
	switch e.stage {
	case generationStageExpand:
		contentHash, err = e.service.expandRecompositionGeneration(ctx, execution.UserID, input, execution.Report)
	case generationStageAudit:
		contentHash, err = e.service.auditRecompositionGeneration(ctx, execution.UserID, input, execution.Report)
	case generationStagePublish:
		contentHash, err = e.service.publishRecompositionGeneration(ctx, execution.UserID, input, execution.Report)
	default:
		err = ErrInvalidRecomposition
	}
	if err != nil {
		return nil, err
	}
	result := generationCacheResult{
		SchemaVersion: RecompositionGenerationResultV1, GenerationID: input.GenerationID,
		VersionID: input.OutputVersionID, Stage: e.stage, ContentHash: contentHash,
	}
	raw, err := compute.CanonicalJSON(result)
	return json.RawMessage(raw), err
}

func (e *RecompositionGenerationExecutor) ValidateCachedResult(ctx context.Context, userID uint, raw json.RawMessage) error {
	var result generationCacheResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.SchemaVersion != RecompositionGenerationResultV1 || result.GenerationID == 0 || result.VersionID == 0 || result.Stage != e.stage {
		return ErrInvalidRecomposition
	}
	var row saasstore.RecompositionGeneration
	if err := e.service.db.WithContext(ctx).Preload("OutputVersion").
		Where("id = ? AND owner_user_id = ? AND output_version_id = ?", result.GenerationID, userID, result.VersionID).
		First(&row).Error; err != nil {
		return err
	}
	if result.ContentHash == "" || row.OutputVersion.ContentHash != result.ContentHash {
		return ErrStaleRecompositionPlan
	}
	switch e.stage {
	case generationStageExpand:
		if row.ExpandedAt == nil {
			return ErrStaleRecompositionPlan
		}
	case generationStageAudit:
		if row.CalendarCheckedAt == nil || row.OutputVersion.IntegrityStatus != marketversion.IntegrityValid {
			return ErrStaleRecompositionPlan
		}
	case generationStagePublish:
		if row.PublishedAt == nil || !row.OutputVersion.Published || row.Status != marketversion.VersionStatusCompleted {
			return ErrStaleRecompositionPlan
		}
	}
	return nil
}

func validateGenerationExecutionInput(input RecompositionGenerationExecutionInput) error {
	if input.SchemaVersion != RecompositionGenerationRequestV1 || input.GenerationID == 0 || input.PlanID == 0 ||
		input.OutputVersionID == 0 || strings.TrimSpace(input.PlanHash) == "" {
		return ErrInvalidRecomposition
	}
	return nil
}

func (s *Service) generationRecords(ctx context.Context, userID uint, input RecompositionGenerationExecutionInput) (saasstore.RecompositionGeneration, saasstore.RecompositionPlan, saasstore.MarketDataVersion, error) {
	var generation saasstore.RecompositionGeneration
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND plan_id = ? AND plan_hash = ? AND output_version_id = ?",
		input.GenerationID, userID, input.PlanID, input.PlanHash, input.OutputVersionID).First(&generation).Error; err != nil {
		return generation, saasstore.RecompositionPlan{}, saasstore.MarketDataVersion{}, err
	}
	var plan saasstore.RecompositionPlan
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND plan_hash = ? AND status = ?",
		input.PlanID, userID, input.PlanHash, marketversion.VersionStatusCompleted).First(&plan).Error; err != nil {
		return generation, plan, saasstore.MarketDataVersion{}, err
	}
	var version saasstore.MarketDataVersion
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND plan_hash = ?", input.OutputVersionID, userID, input.PlanHash).First(&version).Error; err != nil {
		return generation, plan, version, err
	}
	return generation, plan, version, nil
}

func (s *Service) expandRecompositionGeneration(
	ctx context.Context,
	userID uint,
	input RecompositionGenerationExecutionInput,
	report func(context.Context, computetask.ProgressUpdate) error,
) (string, error) {
	generation, planRecord, version, err := s.generationRecords(ctx, userID, input)
	if err != nil {
		return "", err
	}
	if generation.ExpandedAt != nil {
		var count int64
		if err := s.db.WithContext(ctx).Model(&saasstore.MarketDataVersionBar{}).Where("version_id = ?", version.ID).Count(&count).Error; err == nil && count == int64(planRecord.TotalOutputBars) {
			return version.ContentHash, nil
		}
	}
	if generation.PublishedAt != nil || version.Published || version.Status != marketversion.VersionStatusStaging {
		return "", ErrStaleRecompositionPlan
	}
	var plan marketversion.GenerationPlan
	if err := json.Unmarshal(planRecord.CanonicalPlan, &plan); err != nil {
		return "", err
	}
	if len(plan.Segments) != planRecord.SegmentCount {
		return "", ErrStaleRecompositionPlan
	}
	for index := range plan.Segments {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		sourceVersion, sourceBars, err := s.loadMarketVersionBars(ctx, userID, plan.Segments[index].Source.VersionID)
		if err != nil {
			return "", err
		}
		if sourceVersion.ContentHash != plan.Segments[index].Source.ContentHash {
			return "", ErrStaleRecompositionPlan
		}
		selected, _, err := selectVersionRange(sourceBars, plan.Segments[index].StartTimeMs, plan.Segments[index].EndTimeMs)
		if err != nil {
			return "", err
		}
		plan.Segments[index].Bars = selected
		if report != nil {
			if err := report(ctx, computetask.ProgressUpdate{Progress: 0.1 + 0.35*float64(index+1)/float64(len(plan.Segments))}); err != nil {
				return "", err
			}
		}
	}
	calendarVersion, calendarBars, err := s.loadMarketVersionBars(ctx, userID, plan.CalendarSource.VersionID)
	if err != nil {
		return "", err
	}
	if calendarVersion.ContentHash != plan.CalendarSource.ContentHash {
		return "", ErrStaleRecompositionPlan
	}
	slots := calendarSlots(calendarBars, plan.OutputStartTimeMs, plan.TotalOutputBars)
	calendarHash, err := marketversion.HashCalendarSlots(plan.CalendarSource, slots)
	if err != nil || calendarHash != plan.CalendarHash {
		return "", ErrStaleRecompositionPlan
	}
	normalized, _, planHash, err := marketversion.NormalizePlan(plan)
	if err != nil || planHash != planRecord.PlanHash {
		return "", ErrStaleRecompositionPlan
	}
	result, err := marketversion.Recompose(normalized, slots)
	if err != nil {
		return "", err
	}
	if result.ContentHash != planRecord.ContentHash || len(result.Bars) != planRecord.TotalOutputBars {
		return "", ErrStaleRecompositionPlan
	}
	if report != nil {
		if err := report(ctx, computetask.ProgressUpdate{Progress: 0.7}); err != nil {
			return "", err
		}
	}
	now := time.Now().UTC()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked saasstore.MarketDataVersion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_user_id = ?", version.ID, userID).First(&locked).Error; err != nil {
			return err
		}
		if locked.Status != marketversion.VersionStatusStaging || locked.Published || locked.IntegrityStatus == marketversion.IntegrityValid {
			return ErrStaleRecompositionPlan
		}
		for _, model := range []any{&saasstore.MarketDataVersionBar{}, &saasstore.MarketDataVersionSource{}, &saasstore.RecompositionSegmentInstance{}, &saasstore.RecompositionBarLineage{}} {
			if err := tx.Where("version_id = ?", version.ID).Delete(model).Error; err != nil {
				return err
			}
		}
		bars := make([]saasstore.MarketDataVersionBar, 0, len(result.Bars))
		lineage := make([]saasstore.RecompositionBarLineage, 0, len(result.Lineage))
		for index, bar := range result.Bars {
			bars = append(bars, saasstore.MarketDataVersionBar{VersionID: version.ID, Ordinal: bar.Ordinal, OpenTime: bar.OpenTime, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume})
			origin := result.Lineage[index]
			lineage = append(lineage, saasstore.RecompositionBarLineage{
				VersionID: version.ID, OutputOrdinal: origin.OutputOrdinal, OutputOpenTime: origin.OutputOpenTime,
				SegmentInstanceKey: origin.SegmentInstanceID, SourceVersionID: origin.SourceVersionID,
				SourceContentHash: origin.SourceContentHash, SourceOrdinal: origin.SourceOrdinal, SourceOpenTime: origin.SourceOpenTime,
			})
		}
		if err := tx.CreateInBatches(&bars, 1000).Error; err != nil {
			return err
		}
		if err := tx.CreateInBatches(&lineage, 1000).Error; err != nil {
			return err
		}
		instances := make([]saasstore.RecompositionSegmentInstance, 0, len(result.Instances))
		for _, instance := range result.Instances {
			instances = append(instances, saasstore.RecompositionSegmentInstance{
				VersionID: version.ID, InstanceKey: instance.InstanceID, SegmentItemID: instance.SegmentItemID,
				InstanceOrder: instance.Order, RepeatOrdinal: instance.RepeatOrdinal, SourceVersionID: instance.SourceVersionID,
				SourceContentHash: instance.SourceContentHash, SourceStartTimeMs: instance.SourceStartTimeMs, SourceEndTimeMs: instance.SourceEndTimeMs,
				OutputStartOrdinal: instance.OutputStartOrdinal, OutputEndOrdinal: instance.OutputEndOrdinal,
				OutputStartTimeMs: instance.OutputStartTimeMs, OutputEndTimeMs: instance.OutputEndTimeMs,
				ScaleMultiplier: instance.ScaleMultiplier, SourceGapRatio: instance.SourceGapRatio,
				ActualGapRatio: instance.ActualGapRatio, AnchorMissing: instance.AnchorMissing, AnchorValue: instance.AnchorValue,
			})
		}
		if err := tx.Create(&instances).Error; err != nil {
			return err
		}
		seenSources := map[uint]bool{}
		sources := make([]saasstore.MarketDataVersionSource, 0, len(plan.Segments)+1)
		for _, segment := range plan.Segments {
			if seenSources[segment.Source.VersionID] {
				continue
			}
			seenSources[segment.Source.VersionID] = true
			sources = append(sources, saasstore.MarketDataVersionSource{VersionID: version.ID, SourceVersionID: segment.Source.VersionID, SourceOrder: len(sources), SourceRole: "segment", SourceHash: segment.Source.ContentHash})
		}
		if !seenSources[plan.CalendarSource.VersionID] {
			sources = append(sources, saasstore.MarketDataVersionSource{VersionID: version.ID, SourceVersionID: plan.CalendarSource.VersionID, SourceOrder: len(sources), SourceRole: "calendar", SourceHash: plan.CalendarSource.ContentHash})
		}
		if err := tx.Create(&sources).Error; err != nil {
			return err
		}
		if err := tx.Model(&saasstore.MarketDataVersion{}).Where("id = ?", version.ID).
			Updates(map[string]any{"content_hash": result.ContentHash, "bar_count": len(result.Bars), "start_time_ms": result.Bars[0].OpenTime, "end_time_ms": result.Bars[len(result.Bars)-1].OpenTime}).Error; err != nil {
			return err
		}
		return tx.Model(&saasstore.RecompositionGeneration{}).Where("id = ?", generation.ID).Update("expanded_at", now).Error
	})
	if err != nil {
		return "", err
	}
	if report != nil {
		if err := report(ctx, computetask.ProgressUpdate{Progress: 1}); err != nil {
			return "", err
		}
	}
	return result.ContentHash, nil
}

func (s *Service) auditRecompositionGeneration(
	ctx context.Context,
	userID uint,
	input RecompositionGenerationExecutionInput,
	report func(context.Context, computetask.ProgressUpdate) error,
) (string, error) {
	generation, plan, version, err := s.generationRecords(ctx, userID, input)
	if err != nil {
		return "", err
	}
	if generation.ExpandedAt == nil || version.Published || version.Status != marketversion.VersionStatusStaging {
		return "", ErrStaleRecompositionPlan
	}
	if generation.CalendarCheckedAt != nil && version.IntegrityStatus == marketversion.IntegrityValid {
		return version.ContentHash, nil
	}
	bars, lineage, err := s.loadVersionContentForAudit(ctx, version)
	if err != nil {
		return "", err
	}
	if report != nil {
		if err := report(ctx, computetask.ProgressUpdate{Progress: 0.4}); err != nil {
			return "", err
		}
	}
	calendarVersion, calendarBars, err := s.loadMarketVersionBars(ctx, userID, plan.CalendarVersionID)
	if err != nil {
		return "", err
	}
	slots := calendarSlots(calendarBars, plan.OutputStartTimeMs, plan.TotalOutputBars)
	identity := versionIdentity(calendarVersion)
	calendarHash, err := marketversion.HashCalendarSlots(identity, slots)
	if err != nil || calendarHash != plan.CalendarHash || len(slots) != len(bars) {
		return "", ErrStaleRecompositionPlan
	}
	for index := range bars {
		if bars[index].OpenTime != slots[index] {
			return "", ErrStaleRecompositionPlan
		}
	}
	contentHash, err := marketversion.HashRecompositionContent(bars, lineage)
	if err != nil || contentHash != plan.ContentHash || contentHash != version.ContentHash {
		return "", ErrStaleRecompositionPlan
	}
	now := time.Now().UTC()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&saasstore.MarketDataVersion{}).Where("id = ? AND status = ? AND published = false", version.ID, marketversion.VersionStatusStaging).
			Update("integrity_status", marketversion.IntegrityValid).Error; err != nil {
			return err
		}
		return tx.Model(&saasstore.RecompositionGeneration{}).Where("id = ?", generation.ID).Update("calendar_checked_at", now).Error
	})
	if err != nil {
		return "", err
	}
	if report != nil {
		if err := report(ctx, computetask.ProgressUpdate{Progress: 1}); err != nil {
			return "", err
		}
	}
	return contentHash, nil
}

func (s *Service) loadVersionContentForAudit(ctx context.Context, version saasstore.MarketDataVersion) ([]marketversion.Bar, []marketversion.BarLineage, error) {
	var barRows []saasstore.MarketDataVersionBar
	if err := s.db.WithContext(ctx).Where("version_id = ?", version.ID).Order("ordinal ASC").Find(&barRows).Error; err != nil {
		return nil, nil, err
	}
	var lineageRows []saasstore.RecompositionBarLineage
	if err := s.db.WithContext(ctx).Where("version_id = ?", version.ID).Order("output_ordinal ASC").Find(&lineageRows).Error; err != nil {
		return nil, nil, err
	}
	if len(barRows) != version.BarCount || len(lineageRows) != version.BarCount {
		return nil, nil, ErrStaleRecompositionPlan
	}
	bars := make([]marketversion.Bar, 0, len(barRows))
	lineage := make([]marketversion.BarLineage, 0, len(lineageRows))
	for index, row := range barRows {
		bars = append(bars, marketversion.Bar{Ordinal: row.Ordinal, OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
		origin := lineageRows[index]
		lineage = append(lineage, marketversion.BarLineage{
			OutputOrdinal: origin.OutputOrdinal, OutputOpenTime: origin.OutputOpenTime,
			SegmentInstanceID: origin.SegmentInstanceKey, SourceVersionID: origin.SourceVersionID,
			SourceContentHash: origin.SourceContentHash, SourceOrdinal: origin.SourceOrdinal, SourceOpenTime: origin.SourceOpenTime,
		})
	}
	return bars, lineage, nil
}

func (s *Service) publishRecompositionGeneration(
	ctx context.Context,
	userID uint,
	input RecompositionGenerationExecutionInput,
	report func(context.Context, computetask.ProgressUpdate) error,
) (string, error) {
	generation, _, version, err := s.generationRecords(ctx, userID, input)
	if err != nil {
		return "", err
	}
	if generation.PublishedAt != nil && version.Published && version.Status == marketversion.VersionStatusCompleted {
		return version.ContentHash, nil
	}
	if generation.CalendarCheckedAt == nil || generation.ExpandedAt == nil || version.IntegrityStatus != marketversion.IntegrityValid ||
		version.Status != marketversion.VersionStatusStaging || version.Published {
		return "", ErrStaleRecompositionPlan
	}
	var series saasstore.MarketSeries
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND archived_at IS NULL", generation.MarketSeriesID, userID).First(&series).Error; err != nil {
		return "", err
	}
	var barRows []saasstore.MarketDataVersionBar
	if err := s.db.WithContext(ctx).Where("version_id = ?", version.ID).Order("ordinal ASC").Find(&barRows).Error; err != nil {
		return "", err
	}
	if len(barRows) != version.BarCount {
		return "", ErrStaleRecompositionPlan
	}
	if report != nil {
		if err := report(ctx, computetask.ProgressUpdate{Progress: 0.25}); err != nil {
			return "", err
		}
	}
	intervals, _ := saasstore.NewJSONB([]string{version.Interval})
	starts, _ := saasstore.NewJSONB(map[string]int64{version.Interval: version.StartTimeMs})
	instrumentID := version.InstrumentID
	displayName := fmt.Sprintf("%s v%d", series.Name, version.VersionNumber)
	now := time.Now().UTC()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked saasstore.MarketDataVersion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_user_id = ?", version.ID, userID).First(&locked).Error; err != nil {
			return err
		}
		if locked.Status != marketversion.VersionStatusStaging || locked.IntegrityStatus != marketversion.IntegrityValid || locked.Published {
			return ErrStaleRecompositionPlan
		}
		instrument := saasstore.ResearchInstrument{
			ID: instrumentID, Symbol: version.Symbol, DisplayName: displayName, DataSource: DataSourceGenerated,
			SupportedIntervals: intervals, AvailableStartMs: starts, Market: version.Market, SortOrder: 1000, Enabled: true,
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"display_name", "supported_intervals", "available_start_ms", "enabled", "updated_at"})}).Create(&instrument).Error; err != nil {
			return err
		}
		klines := make([]saasstore.KLine, 0, len(barRows))
		for _, bar := range barRows {
			klines = append(klines, saasstore.KLine{
				InstrumentID: instrumentID, Source: DataSourceGenerated, Symbol: version.Symbol, Interval: version.Interval,
				OpenTime: bar.OpenTime, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume,
			})
		}
		if err := tx.CreateInBatches(&klines, 1000).Error; err != nil {
			return err
		}
		metadata := saasstore.DatasetMetadata{
			InstrumentID: instrumentID, DataSource: DataSourceGenerated, Symbol: version.Symbol, Interval: version.Interval,
			PriceAdjustment: PriceAdjustmentSegmentRecomposition, ImportedStartMs: version.StartTimeMs,
			ImportedEndMs: version.EndTimeMs, FullCoverage: true,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "instrument_id"}, {Name: "data_source"}, {Name: "symbol"}, {Name: "interval"}},
			DoUpdates: clause.AssignmentColumns([]string{"price_adjustment", "imported_start_ms", "imported_end_ms", "full_coverage", "updated_at"}),
		}).Create(&metadata).Error; err != nil {
			return err
		}
		if err := tx.Model(&saasstore.MarketDataVersion{}).Where("id = ?", version.ID).Updates(map[string]any{
			"status": marketversion.VersionStatusCompleted, "published": true, "internal_only": false,
			"output_instrument_id": instrumentID, "completed_at": now,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&saasstore.RecompositionGeneration{}).Where("id = ?", generation.ID).Updates(map[string]any{
			"status": marketversion.VersionStatusCompleted, "published_at": now,
		}).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return "", ErrStaleRecompositionPlan
		}
		return "", err
	}
	if report != nil {
		if err := report(ctx, computetask.ProgressUpdate{Progress: 1}); err != nil {
			return "", err
		}
	}
	return version.ContentHash, nil
}
