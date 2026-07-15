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

type RecompositionPreviewExecutor struct {
	service *Service
}

func NewRecompositionPreviewExecutor(service *Service) *RecompositionPreviewExecutor {
	return &RecompositionPreviewExecutor{service: service}
}

func (e *RecompositionPreviewExecutor) Descriptor() compute.ExecutorDescriptor {
	return compute.ExecutorDescriptor{
		Type: RecompositionPreviewExecutorType, Version: RecompositionPreviewExecutorVersion,
		ResultSchemaVersion: RecompositionPreviewResultVersion,
	}
}

func (e *RecompositionPreviewExecutor) Execute(ctx context.Context, execution computetask.Execution) (json.RawMessage, error) {
	if e == nil || e.service == nil || e.service.db == nil {
		return nil, computetask.ErrServiceUnavailable
	}
	var input RecompositionPreviewExecutionInput
	if err := json.Unmarshal(execution.Input, &input); err != nil {
		return nil, fmt.Errorf("decode recomposition preview input: %w", err)
	}
	if input.SchemaVersion != RecompositionPreviewRequestVersion || input.RequestHash == "" || input.TotalOutputBars <= 0 {
		return nil, ErrInvalidRecomposition
	}
	if execution.Report != nil {
		_ = execution.Report(ctx, computetask.ProgressUpdate{Progress: 0.05})
	}
	preview, err := e.service.executeRecompositionPreview(ctx, execution.UserID, execution.TaskID, input, execution.Report)
	if err != nil {
		return nil, err
	}
	result := previewCacheResult{SchemaVersion: RecompositionPreviewResultVersion, PlanID: preview.PlanID, PlanHash: preview.PlanHash}
	raw, err := compute.CanonicalJSON(result)
	return json.RawMessage(raw), err
}

func (e *RecompositionPreviewExecutor) ValidateCachedResult(ctx context.Context, userID uint, raw json.RawMessage) error {
	if e == nil || e.service == nil || e.service.db == nil {
		return computetask.ErrServiceUnavailable
	}
	var cached previewCacheResult
	if err := json.Unmarshal(raw, &cached); err != nil {
		return err
	}
	if cached.SchemaVersion != RecompositionPreviewResultVersion || cached.PlanID == 0 || cached.PlanHash == "" {
		return ErrInvalidRecomposition
	}
	var plan saasstore.RecompositionPlan
	if err := e.service.db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND plan_hash = ? AND status = ?", cached.PlanID, userID, cached.PlanHash, marketversion.VersionStatusCompleted).First(&plan).Error; err != nil {
		return err
	}
	var count int64
	err := e.service.db.WithContext(ctx).Table("recomposition_plans AS plan").
		Joins("JOIN recomposition_plan_segments AS segment ON segment.plan_id = plan.id").
		Joins("JOIN market_data_versions AS source ON source.id = segment.source_version_id").
		Where("plan.id = ? AND plan.owner_user_id = ? AND plan.plan_hash = ? AND plan.status = ?", cached.PlanID, userID, cached.PlanHash, marketversion.VersionStatusCompleted).
		Where("source.status = ? AND source.integrity_status = ?", marketversion.VersionStatusCompleted, marketversion.IntegrityValid).
		Distinct("segment.id").Count(&count).Error
	if err != nil {
		return err
	}
	var previewBars int64
	if err := e.service.db.WithContext(ctx).Model(&saasstore.RecompositionPreviewBar{}).Where("plan_id = ?", plan.ID).Count(&previewBars).Error; err != nil {
		return err
	}
	if count != int64(plan.SegmentCount) || previewBars != int64(plan.TotalOutputBars) {
		return ErrStaleRecompositionPlan
	}
	return nil
}

func (s *Service) executeRecompositionPreview(
	ctx context.Context,
	userID uint,
	taskID uint,
	input RecompositionPreviewExecutionInput,
	report func(context.Context, computetask.ProgressUpdate) error,
) (RecompositionPreviewResult, error) {
	versions := make(map[string]saasstore.MarketDataVersion)
	versionBars := make(map[uint][]marketversion.Bar)
	resolve := func(source ResolvedRecompositionSource) (saasstore.MarketDataVersion, []marketversion.Bar, error) {
		key := source.ContentHash
		if source.VersionID == 0 && source.Fingerprint != nil {
			key = fingerprintKey(*source.Fingerprint)
		}
		if version, ok := versions[key]; ok {
			return version, versionBars[version.ID], nil
		}
		var version saasstore.MarketDataVersion
		var bars []marketversion.Bar
		var err error
		if source.VersionID != 0 {
			version, bars, err = s.loadMarketVersionBars(ctx, userID, source.VersionID)
		} else {
			version, bars, err = s.ensureLiveSourceSnapshot(ctx, userID, source)
		}
		if err != nil {
			return version, nil, err
		}
		versions[key] = version
		versionBars[version.ID] = bars
		return version, bars, nil
	}

	resolvedVersions := make([]saasstore.MarketDataVersion, 0, len(input.Sources))
	resolvedBars := make([][]marketversion.Bar, 0, len(input.Sources))
	for index, source := range input.Sources {
		if err := ctx.Err(); err != nil {
			return RecompositionPreviewResult{}, err
		}
		version, bars, err := resolve(source)
		if err != nil {
			return RecompositionPreviewResult{}, err
		}
		resolvedVersions = append(resolvedVersions, version)
		resolvedBars = append(resolvedBars, bars)
		if report != nil {
			_ = report(ctx, computetask.ProgressUpdate{Progress: 0.1 + 0.3*float64(index+1)/float64(len(input.Sources)+1)})
		}
	}
	calendarVersion, calendarBars, err := resolve(input.CalendarSource)
	if err != nil {
		return RecompositionPreviewResult{}, err
	}
	calendarIdentity := versionIdentity(calendarVersion)
	outputSlots := calendarSlots(calendarBars, input.Request.OutputStartTimeMs, input.TotalOutputBars)
	if len(outputSlots) != input.TotalOutputBars || outputSlots[0] != input.Request.OutputStartTimeMs {
		return RecompositionPreviewResult{}, marketversion.ErrCalendarSlots
	}
	calendarHash, err := marketversion.HashCalendarSlots(calendarIdentity, outputSlots)
	if err != nil {
		return RecompositionPreviewResult{}, err
	}
	if report != nil {
		_ = report(ctx, computetask.ProgressUpdate{Progress: 0.45})
	}

	segments := make([]marketversion.SegmentPlan, 0, len(input.Sources))
	for index, source := range input.Sources {
		selected, previous, err := selectVersionRange(resolvedBars[index], source.Request.StartTimeMs, source.Request.EndTimeMs)
		if err != nil {
			return RecompositionPreviewResult{}, fmt.Errorf("片段 %s: %w", source.Request.ItemID, err)
		}
		segment := marketversion.SegmentPlan{
			ItemID: source.Request.ItemID, Order: index, Source: versionIdentity(resolvedVersions[index]),
			StartTimeMs: source.Request.StartTimeMs, EndTimeMs: source.Request.EndTimeMs,
			BarCount: len(selected), RepeatCount: source.Request.RepeatCount, FirstOpen: selected[0].Open, Bars: selected,
		}
		if previous != nil {
			segment.PreviousClosePresent = true
			segment.PreviousClose = previous.Close
			gap := selected[0].Open/previous.Close - 1
			segment.SourceGapRatio = &gap
		}
		segments = append(segments, segment)
	}
	plan := marketversion.GenerationPlan{
		SchemaVersion: marketversion.RecompositionPlanVersion, AlgorithmVersion: marketversion.RecompositionAlgorithm,
		PrecisionVersion: marketversion.PricePrecisionVersion, Interval: input.Request.Interval,
		TargetMarket: calendarVersion.Market, TargetTimezone: calendarVersion.Timezone,
		CalendarSource: calendarIdentity, CalendarVersion: marketversion.CalendarFromVersionVersion, CalendarHash: calendarHash,
		OutputStartTimeMs: input.Request.OutputStartTimeMs, TotalOutputBars: input.TotalOutputBars, Segments: segments,
	}
	normalizedPlan, canonicalPlan, planHash, err := marketversion.NormalizePlan(plan)
	if err != nil {
		return RecompositionPreviewResult{}, err
	}
	result, err := marketversion.Recompose(normalizedPlan, outputSlots)
	if err != nil {
		return RecompositionPreviewResult{}, err
	}
	if report != nil {
		_ = report(ctx, computetask.ProgressUpdate{Progress: 0.8})
	}
	preview, err := s.persistRecompositionPreview(ctx, userID, taskID, normalizedPlan, canonicalPlan, planHash, result, input.TotalReadBars, calendarVersion)
	if err != nil {
		return RecompositionPreviewResult{}, err
	}
	if report != nil {
		_ = report(ctx, computetask.ProgressUpdate{Progress: 1})
	}
	return preview, nil
}

func (s *Service) ensureLiveSourceSnapshot(ctx context.Context, userID uint, source ResolvedRecompositionSource) (saasstore.MarketDataVersion, []marketversion.Bar, error) {
	if source.Fingerprint == nil {
		return saasstore.MarketDataVersion{}, nil, ErrInvalidRecomposition
	}
	current, err := s.datasetFingerprint(ctx, source.Instrument, source.Fingerprint.Interval)
	if err != nil {
		return saasstore.MarketDataVersion{}, nil, err
	}
	if current != *source.Fingerprint {
		return saasstore.MarketDataVersion{}, nil, ErrStaleRecompositionPlan
	}
	var rows []saasstore.KLine
	if err := s.db.WithContext(ctx).
		Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ?", source.Instrument.ID, source.Instrument.DataSource, source.Instrument.Symbol, source.Fingerprint.Interval).
		Order("open_time ASC").Find(&rows).Error; err != nil {
		return saasstore.MarketDataVersion{}, nil, err
	}
	bars := make([]marketversion.Bar, 0, len(rows))
	for index, row := range rows {
		bars = append(bars, marketversion.Bar{Ordinal: index, OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	if err := marketversion.ValidateBars(bars, true); err != nil {
		return saasstore.MarketDataVersion{}, nil, err
	}
	timezone := marketTimezone(source.Instrument.Market)
	calendarID := fmt.Sprintf("actual-slots:%s:%s", source.Instrument.ID, source.Fingerprint.Interval)
	snapshot := marketversion.SourceSnapshot{
		SchemaVersion: marketversion.VersionSchemaVersion, InstrumentID: source.Instrument.ID,
		DataSource: source.Instrument.DataSource, Symbol: source.Instrument.Symbol, Market: source.Instrument.Market,
		Timezone: timezone, Interval: source.Fingerprint.Interval, ArtifactKind: source.ArtifactKind,
		CalendarID: calendarID, CalendarVersion: marketversion.CalendarFromVersionVersion, Bars: bars,
	}
	contentHash, err := marketversion.HashSourceSnapshot(snapshot)
	if err != nil {
		return saasstore.MarketDataVersion{}, nil, err
	}
	snapshotKey := fmt.Sprintf("%d|%s", userID, contentHash)
	planRaw, err := compute.CanonicalJSON(struct {
		SchemaVersion string             `json:"schema_version"`
		Fingerprint   DatasetFingerprint `json:"fingerprint"`
	}{marketversion.VersionSchemaVersion, *source.Fingerprint})
	if err != nil {
		return saasstore.MarketDataVersion{}, nil, err
	}
	var version saasstore.MarketDataVersion
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		candidate := saasstore.MarketDataVersion{
			OwnerUserID: userID, SnapshotKey: &snapshotKey,
			SchemaVersion: marketversion.VersionSchemaVersion, BarSchemaVersion: marketversion.BarSchemaVersion,
			ArtifactKind: source.ArtifactKind, GeneratorVersion: "p06-source-snapshot-v1", PrecisionVersion: marketversion.PricePrecisionVersion,
			Status: marketversion.VersionStatusCompleted, IntegrityStatus: marketversion.IntegrityValid,
			ContentHash: contentHash, PlanHash: fingerprintKey(*source.Fingerprint), Plan: saasstore.JSONB(planRaw),
			InstrumentID: source.Instrument.ID, DataSource: source.Instrument.DataSource, Symbol: source.Instrument.Symbol,
			Market: source.Instrument.Market, Timezone: timezone, Interval: source.Fingerprint.Interval,
			CalendarID: calendarID, CalendarVersion: marketversion.CalendarFromVersionVersion, CalendarHash: contentHash,
			BarCount: len(bars), StartTimeMs: bars[0].OpenTime, EndTimeMs: bars[len(bars)-1].OpenTime,
			InternalOnly: true, Published: false,
		}
		now := time.Now().UTC()
		candidate.CompletedAt = &now
		created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "snapshot_key"}}, DoNothing: true}).Create(&candidate)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			if err := tx.Where("snapshot_key = ?", snapshotKey).First(&version).Error; err != nil {
				return err
			}
			if version.ContentHash != contentHash || version.Status != marketversion.VersionStatusCompleted || version.IntegrityStatus != marketversion.IntegrityValid {
				return ErrStaleRecompositionPlan
			}
			return nil
		}
		version = candidate
		models := make([]saasstore.MarketDataVersionBar, 0, len(bars))
		for _, bar := range bars {
			models = append(models, saasstore.MarketDataVersionBar{
				VersionID: version.ID, Ordinal: bar.Ordinal, OpenTime: bar.OpenTime,
				Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume,
			})
		}
		return tx.CreateInBatches(&models, 1000).Error
	})
	if err != nil {
		return saasstore.MarketDataVersion{}, nil, err
	}
	return version, bars, nil
}

func (s *Service) loadMarketVersionBars(ctx context.Context, userID uint, versionID uint) (saasstore.MarketDataVersion, []marketversion.Bar, error) {
	version, err := s.loadAccessibleMarketVersion(ctx, userID, versionID)
	if err != nil {
		return version, nil, err
	}
	var rows []saasstore.MarketDataVersionBar
	if err := s.db.WithContext(ctx).Where("version_id = ?", versionID).Order("ordinal ASC").Find(&rows).Error; err != nil {
		return version, nil, err
	}
	if len(rows) != version.BarCount {
		return version, nil, ErrStaleRecompositionPlan
	}
	bars := make([]marketversion.Bar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, marketversion.Bar{Ordinal: row.Ordinal, OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	if err := marketversion.ValidateBars(bars, true); err != nil {
		return version, nil, err
	}
	return version, bars, nil
}

func (s *Service) persistRecompositionPreview(
	ctx context.Context,
	userID uint,
	taskID uint,
	plan marketversion.GenerationPlan,
	canonicalPlan []byte,
	planHash string,
	result marketversion.RecompositionResult,
	readBars int,
	calendarVersion saasstore.MarketDataVersion,
) (RecompositionPreviewResult, error) {
	var record saasstore.RecompositionPlan
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		find := tx.Where("owner_user_id = ? AND plan_hash = ?", userID, planHash).First(&record)
		if find.Error == nil {
			if record.Status != marketversion.VersionStatusCompleted || record.ContentHash != result.ContentHash {
				return ErrStaleRecompositionPlan
			}
			return nil
		}
		if !errors.Is(find.Error, gorm.ErrRecordNotFound) {
			return find.Error
		}
		instancesRaw, err := compute.CanonicalJSON(result.Instances)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		record = saasstore.RecompositionPlan{
			OwnerUserID: userID, PlanHash: planHash, SchemaVersion: plan.SchemaVersion,
			AlgorithmVersion: plan.AlgorithmVersion, PrecisionVersion: plan.PrecisionVersion,
			Status: marketversion.VersionStatusCompleted, Interval: plan.Interval,
			TargetMarket: plan.TargetMarket, TargetTimezone: plan.TargetTimezone,
			CalendarVersionID: calendarVersion.ID, CalendarVersion: plan.CalendarVersion, CalendarHash: plan.CalendarHash,
			OutputStartTimeMs: result.Bars[0].OpenTime, OutputEndTimeMs: result.Bars[len(result.Bars)-1].OpenTime,
			SegmentCount: len(plan.Segments), InstanceCount: len(result.Instances), TotalOutputBars: len(result.Bars),
			EstimatedReadBars: readBars, EstimatedWriteBars: len(result.Bars),
			EstimatedBytes:     int64(len(result.Bars)) * estimatedStoredBytesPerBar,
			AnchorWarningCount: result.AnchorWarnings, ContentHash: result.ContentHash,
			CanonicalPlan: saasstore.JSONB(canonicalPlan), Instances: saasstore.JSONB(instancesRaw),
			PreviewTaskID: &taskID, CompletedAt: &now,
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		segmentRows := make([]saasstore.RecompositionPlanSegment, 0, len(plan.Segments))
		for _, segment := range plan.Segments {
			segmentRows = append(segmentRows, saasstore.RecompositionPlanSegment{
				PlanID: record.ID, ItemID: segment.ItemID, SegmentOrder: segment.Order,
				SourceVersionID: segment.Source.VersionID, SourceContentHash: segment.Source.ContentHash,
				SourceStartTimeMs: segment.StartTimeMs, SourceEndTimeMs: segment.EndTimeMs,
				BarCount: segment.BarCount, RepeatCount: segment.RepeatCount,
				PreviousClosePresent: segment.PreviousClosePresent, PreviousClose: segment.PreviousClose,
				FirstOpen: segment.FirstOpen, SourceGapRatio: segment.SourceGapRatio,
			})
		}
		if err := tx.Create(&segmentRows).Error; err != nil {
			return err
		}
		previewRows := make([]saasstore.RecompositionPreviewBar, 0, len(result.Bars))
		for index, bar := range result.Bars {
			lineage := result.Lineage[index]
			previewRows = append(previewRows, saasstore.RecompositionPreviewBar{
				PlanID: record.ID, Ordinal: bar.Ordinal, OpenTime: bar.OpenTime,
				Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume,
				SegmentInstanceID: lineage.SegmentInstanceID, SourceVersionID: lineage.SourceVersionID,
				SourceOrdinal: lineage.SourceOrdinal, SourceOpenTime: lineage.SourceOpenTime,
			})
		}
		return tx.CreateInBatches(&previewRows, 1000).Error
	})
	if err != nil {
		return RecompositionPreviewResult{}, err
	}
	return s.recompositionPreviewResult(record)
}

func (s *Service) recompositionPreviewResult(record saasstore.RecompositionPlan) (RecompositionPreviewResult, error) {
	var instances []marketversion.SegmentInstance
	if err := json.Unmarshal(record.Instances, &instances); err != nil {
		return RecompositionPreviewResult{}, err
	}
	return RecompositionPreviewResult{
		SchemaVersion: RecompositionPreviewResultVersion, PlanID: record.ID, PlanHash: record.PlanHash,
		ContentHash: record.ContentHash, Interval: record.Interval, TargetMarket: record.TargetMarket,
		TargetTimezone: record.TargetTimezone, CalendarVersionID: record.CalendarVersionID,
		CalendarHash: record.CalendarHash, OutputStartTimeMs: record.OutputStartTimeMs,
		OutputEndTimeMs: record.OutputEndTimeMs, SegmentCount: record.SegmentCount,
		InstanceCount: record.InstanceCount, TotalOutputBars: record.TotalOutputBars,
		EstimatedReadBars: record.EstimatedReadBars, EstimatedWriteBars: record.EstimatedWriteBars,
		EstimatedBytes: record.EstimatedBytes, AnchorWarningCount: record.AnchorWarningCount, Instances: instances,
	}, nil
}

func versionIdentity(version saasstore.MarketDataVersion) marketversion.VersionIdentity {
	return marketversion.VersionIdentity{
		VersionID: version.ID, ContentHash: version.ContentHash, ArtifactKind: version.ArtifactKind,
		InstrumentID: version.InstrumentID, DataSource: version.DataSource, Symbol: version.Symbol,
		Market: version.Market, Timezone: version.Timezone, Interval: version.Interval,
		CalendarID: version.CalendarID, CalendarVersion: version.CalendarVersion,
	}
}

func calendarSlots(bars []marketversion.Bar, startMs int64, count int) []int64 {
	result := make([]int64, 0, count)
	for _, bar := range bars {
		if bar.OpenTime < startMs {
			continue
		}
		result = append(result, bar.OpenTime)
		if len(result) == count {
			break
		}
	}
	return result
}

func selectVersionRange(bars []marketversion.Bar, startMs, endMs int64) ([]marketversion.Bar, *marketversion.Bar, error) {
	selected := make([]marketversion.Bar, 0)
	var previous *marketversion.Bar
	for _, bar := range bars {
		if bar.OpenTime < startMs {
			copy := bar
			previous = &copy
			continue
		}
		if bar.OpenTime > endMs {
			break
		}
		selected = append(selected, bar)
	}
	if len(selected) == 0 || selected[0].OpenTime != startMs || selected[len(selected)-1].OpenTime != endMs {
		return nil, nil, ErrInvalidRecomposition
	}
	return selected, previous, nil
}

func fingerprintKey(value DatasetFingerprint) string {
	raw, _ := compute.CanonicalJSON(value)
	return "market-dataset-fingerprint:v1:" + compute.HashBytes(raw)
}

func marketTimezone(market string) string {
	switch strings.ToLower(strings.TrimSpace(market)) {
	case "tw":
		return "Asia/Taipei"
	case "us":
		return "America/New_York"
	default:
		return "UTC"
	}
}
