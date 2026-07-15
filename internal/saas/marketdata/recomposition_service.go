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
)

var (
	ErrInvalidRecomposition      = errors.New("K 線片段重組設定不正確")
	ErrUnsupportedSourceKind     = errors.New("來源行情種類不支援片段重組")
	ErrStaleRecompositionPlan    = errors.New("來源行情已變更，請重新建立預覽")
	ErrRecompositionPlanNotFound = errors.New("找不到片段重組預覽")
)

const estimatedStoredBytesPerBar int64 = 224

func (s *Service) CreateRecompositionPreviewTask(ctx context.Context, userID uint, req RecompositionPreviewRequest, confirmSoftLimit bool) (RecompositionPreviewTask, error) {
	if s == nil || s.db == nil || s.computeTasks == nil {
		return RecompositionPreviewTask{}, computetask.ErrServiceUnavailable
	}
	input, err := s.prepareRecompositionPreview(ctx, userID, req)
	if err != nil {
		return RecompositionPreviewTask{}, err
	}
	raw, err := compute.CanonicalJSON(input)
	if err != nil {
		return RecompositionPreviewTask{}, err
	}
	spec := computetask.CreateSpec{
		TaskType: "market_recomposition_preview", Title: "K 線片段重組預覽",
		ExecutorType: RecompositionPreviewExecutorType,
		Settings: map[string]any{
			"schema_version":    RecompositionPreviewRequestVersion,
			"request_hash":      input.RequestHash,
			"total_read_bars":   input.TotalReadBars,
			"total_output_bars": input.TotalOutputBars,
		},
		ResearchSettingHash: compute.HashBytes([]byte(input.RequestHash)),
		Items: []compute.ManifestItemInput{{
			Key: "preview", CacheKey: "market-recomposition-preview:" + input.RequestHash,
			Input: json.RawMessage(raw), EstimatedUnits: int64(input.TotalReadBars + input.TotalOutputBars),
		}},
	}
	preview, err := s.computeTasks.Preview(ctx, userID, spec)
	if err != nil {
		return RecompositionPreviewTask{}, err
	}
	task, err := s.computeTasks.Create(ctx, userID, spec, confirmSoftLimit)
	if err != nil {
		return RecompositionPreviewTask{}, err
	}
	return RecompositionPreviewTask{
		Task: task, TaskPreview: preview, TotalReadBars: input.TotalReadBars, TotalOutputBars: input.TotalOutputBars,
		EstimatedBytes: int64(input.TotalOutputBars) * estimatedStoredBytesPerBar,
	}, nil
}

func (s *Service) prepareRecompositionPreview(ctx context.Context, userID uint, req RecompositionPreviewRequest) (RecompositionPreviewExecutionInput, error) {
	if userID == 0 {
		return RecompositionPreviewExecutionInput{}, ErrInvalidRecomposition
	}
	req.SchemaVersion = RecompositionPreviewRequestVersion
	req.Interval = normalizeInterval(req.Interval)
	req.CalendarInstrumentID = normalizeInstrumentID(req.CalendarInstrumentID)
	if req.Interval == "" || req.OutputStartTimeMs <= 0 || len(req.Segments) == 0 {
		return RecompositionPreviewExecutionInput{}, ErrInvalidRecomposition
	}
	resolved := make([]ResolvedRecompositionSource, 0, len(req.Segments))
	totalRead := 0
	totalOutput := 0
	seenItemIDs := map[string]bool{}
	for index, segment := range req.Segments {
		segment.ItemID = strings.TrimSpace(segment.ItemID)
		segment.SourceInstrumentID = normalizeInstrumentID(segment.SourceInstrumentID)
		if segment.ItemID == "" || seenItemIDs[segment.ItemID] || segment.StartTimeMs <= 0 || segment.EndTimeMs < segment.StartTimeMs || segment.RepeatCount <= 0 {
			return RecompositionPreviewExecutionInput{}, fmt.Errorf("%w: 第 %d 個片段不完整", ErrInvalidRecomposition, index+1)
		}
		seenItemIDs[segment.ItemID] = true
		resolvedSource, err := s.resolveRecompositionSource(ctx, userID, segment.SourceInstrumentID, segment.SourceVersionID, req.Interval)
		if err != nil {
			return RecompositionPreviewExecutionInput{}, fmt.Errorf("片段 %s: %w", segment.ItemID, err)
		}
		resolvedSource.Request = segment
		barCount, err := s.countSourceVersionRange(ctx, resolvedSource, segment.StartTimeMs, segment.EndTimeMs)
		if err != nil {
			return RecompositionPreviewExecutionInput{}, err
		}
		if barCount == 0 {
			return RecompositionPreviewExecutionInput{}, fmt.Errorf("%w: 片段 %s 沒有 K 線", ErrInvalidRecomposition, segment.ItemID)
		}
		resolvedSource.BarCount = barCount
		totalRead += barCount
		if barCount > int(^uint(0)>>1)/segment.RepeatCount || totalOutput > int(^uint(0)>>1)-barCount*segment.RepeatCount {
			return RecompositionPreviewExecutionInput{}, fmt.Errorf("%w: 輸出數量溢位", ErrInvalidRecomposition)
		}
		totalOutput += barCount * segment.RepeatCount
		resolved = append(resolved, resolvedSource)
		req.Segments[index] = segment
	}
	calendarSource, err := s.resolveCalendarSource(ctx, userID, req, resolved)
	if err != nil {
		return RecompositionPreviewExecutionInput{}, err
	}
	if err := s.validateCalendarCapacity(ctx, calendarSource, req.OutputStartTimeMs, totalOutput); err != nil {
		return RecompositionPreviewExecutionInput{}, err
	}
	input := RecompositionPreviewExecutionInput{
		SchemaVersion: RecompositionPreviewRequestVersion, Request: req, Sources: resolved,
		CalendarSource: calendarSource, TotalReadBars: totalRead, TotalOutputBars: totalOutput,
	}
	hashInput := input
	hashInput.RequestHash = ""
	raw, err := compute.CanonicalJSON(hashInput)
	if err != nil {
		return RecompositionPreviewExecutionInput{}, err
	}
	input.RequestHash = "recomposition-request:v1:" + compute.HashBytes(raw)
	return input, nil
}

func (s *Service) resolveCalendarSource(ctx context.Context, userID uint, req RecompositionPreviewRequest, sources []ResolvedRecompositionSource) (ResolvedRecompositionSource, error) {
	if req.CalendarSourceVersionID != 0 || req.CalendarInstrumentID != "" {
		return s.resolveRecompositionSource(ctx, userID, req.CalendarInstrumentID, req.CalendarSourceVersionID, req.Interval)
	}
	if len(sources) == 0 {
		return ResolvedRecompositionSource{}, ErrInvalidRecomposition
	}
	return sources[0], nil
}

func (s *Service) resolveRecompositionSource(ctx context.Context, userID uint, instrumentID string, versionID uint, interval string) (ResolvedRecompositionSource, error) {
	if versionID != 0 {
		version, err := s.loadAccessibleMarketVersion(ctx, userID, versionID)
		if err != nil {
			return ResolvedRecompositionSource{}, err
		}
		if version.Interval != interval {
			return ResolvedRecompositionSource{}, ErrUnsupportedInterval
		}
		instrument := ResearchInstrument{
			ID: version.InstrumentID, Symbol: version.Symbol, DisplayName: version.Symbol, DataSource: version.DataSource,
			SupportedIntervals: []string{version.Interval}, Market: version.Market, Enabled: true,
		}
		if version.OutputInstrumentID != nil {
			if resolved, err := s.instruments.ResolveInstrument(ctx, *version.OutputInstrumentID, "", ""); err == nil {
				instrument = resolved
			}
		}
		return ResolvedRecompositionSource{Instrument: instrument, VersionID: version.ID, ContentHash: version.ContentHash, ArtifactKind: version.ArtifactKind}, nil
	}
	instrument, err := s.instruments.ResolveInstrument(ctx, instrumentID, "", "")
	if err != nil {
		return ResolvedRecompositionSource{}, err
	}
	if !instrumentSupportsInterval(instrument, interval) {
		return ResolvedRecompositionSource{}, ErrUnsupportedInterval
	}
	if instrument.DataSource == DataSourceFRED {
		return ResolvedRecompositionSource{}, ErrUnsupportedSourceKind
	}
	var immutable saasstore.MarketDataVersion
	err = s.db.WithContext(ctx).
		Where("output_instrument_id = ? AND owner_user_id = ? AND status = ? AND integrity_status = ?", instrument.ID, userID, marketversion.VersionStatusCompleted, marketversion.IntegrityValid).
		Order("version_number DESC, id DESC").First(&immutable).Error
	if err == nil {
		return ResolvedRecompositionSource{Instrument: instrument, VersionID: immutable.ID, ContentHash: immutable.ContentHash, ArtifactKind: immutable.ArtifactKind}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return ResolvedRecompositionSource{}, err
	}
	artifactKind, err := s.liveArtifactKind(ctx, instrument, interval)
	if err != nil {
		return ResolvedRecompositionSource{}, err
	}
	fingerprint, err := s.datasetFingerprint(ctx, instrument, interval)
	if err != nil {
		return ResolvedRecompositionSource{}, err
	}
	return ResolvedRecompositionSource{Instrument: instrument, ArtifactKind: artifactKind, Fingerprint: &fingerprint}, nil
}

func (s *Service) liveArtifactKind(ctx context.Context, instrument ResearchInstrument, interval string) (string, error) {
	if instrument.DataSource != DataSourceGenerated {
		return marketversion.ArtifactKindSourceSnapshot, nil
	}
	var metadata saasstore.DatasetMetadata
	err := s.db.WithContext(ctx).Where("instrument_id = ? AND data_source = ? AND symbol = ? AND interval = ?", instrument.ID, instrument.DataSource, instrument.Symbol, interval).First(&metadata).Error
	if err != nil {
		return "", ErrUnsupportedSourceKind
	}
	if metadata.PriceAdjustment == PriceAdjustmentGeneratedDailyLeverage {
		return marketversion.ArtifactKindDailyLeverage, nil
	}
	return "", ErrUnsupportedSourceKind
}

func (s *Service) loadAccessibleMarketVersion(ctx context.Context, userID uint, versionID uint) (saasstore.MarketDataVersion, error) {
	var version saasstore.MarketDataVersion
	err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", versionID, userID).First(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return version, ErrUnsupportedSourceKind
	}
	if err != nil {
		return version, err
	}
	if version.Status != marketversion.VersionStatusCompleted || version.IntegrityStatus != marketversion.IntegrityValid || version.ArchivedAt != nil && version.InternalOnly {
		return version, ErrUnsupportedSourceKind
	}
	return version, nil
}

func (s *Service) datasetFingerprint(ctx context.Context, instrument ResearchInstrument, interval string) (DatasetFingerprint, error) {
	var aggregate struct {
		Count       int64
		FirstOpenMs int64
		LastOpenMs  int64
		UpdatedAt   *time.Time
	}
	err := s.db.WithContext(ctx).Model(&saasstore.KLine{}).
		Select("COUNT(*) AS count, COALESCE(MIN(open_time), 0) AS first_open_ms, COALESCE(MAX(open_time), 0) AS last_open_ms, MAX(updated_at) AS updated_at").
		Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ?", instrument.ID, instrument.DataSource, instrument.Symbol, interval).
		Scan(&aggregate).Error
	if err != nil {
		return DatasetFingerprint{}, err
	}
	if aggregate.Count == 0 {
		return DatasetFingerprint{}, ErrNoSourceRows
	}
	fingerprint := DatasetFingerprint{
		InstrumentID: instrument.ID, DataSource: instrument.DataSource, Symbol: instrument.Symbol, Interval: interval,
		Count: aggregate.Count, FirstOpenMs: aggregate.FirstOpenMs, LastOpenMs: aggregate.LastOpenMs,
	}
	if aggregate.UpdatedAt != nil {
		fingerprint.UpdatedAtMs = aggregate.UpdatedAt.UTC().UnixMilli()
	}
	return fingerprint, nil
}

func (s *Service) countSourceVersionRange(ctx context.Context, source ResolvedRecompositionSource, startMs, endMs int64) (int, error) {
	var count int64
	query := s.db.WithContext(ctx)
	if source.VersionID != 0 {
		query = query.Model(&saasstore.MarketDataVersionBar{}).Where("version_id = ? AND open_time BETWEEN ? AND ?", source.VersionID, startMs, endMs)
	} else {
		query = query.Model(&saasstore.KLine{}).Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time BETWEEN ? AND ?",
			source.Instrument.ID, source.Instrument.DataSource, source.Instrument.Symbol, source.RequestedInterval(), startMs, endMs)
	}
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	if count > int64(^uint(0)>>1) {
		return 0, ErrInvalidRecomposition
	}
	return int(count), nil
}

func (source ResolvedRecompositionSource) RequestedInterval() string {
	if source.Request.SourceVersionID != 0 && source.VersionID != 0 {
		return source.Instrument.SupportedIntervals[0]
	}
	if source.Fingerprint != nil {
		return source.Fingerprint.Interval
	}
	if len(source.Instrument.SupportedIntervals) > 0 {
		return source.Instrument.SupportedIntervals[0]
	}
	return ""
}

func (s *Service) validateCalendarCapacity(ctx context.Context, source ResolvedRecompositionSource, startMs int64, count int) error {
	if count <= 0 {
		return marketversion.ErrCalendarSlots
	}
	var slots []int64
	query := s.db.WithContext(ctx)
	if source.VersionID != 0 {
		query = query.Model(&saasstore.MarketDataVersionBar{}).Where("version_id = ? AND open_time >= ?", source.VersionID, startMs)
	} else {
		query = query.Model(&saasstore.KLine{}).Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time >= ?",
			source.Instrument.ID, source.Instrument.DataSource, source.Instrument.Symbol, source.RequestedInterval(), startMs)
	}
	if err := query.Order("open_time ASC").Limit(count).Pluck("open_time", &slots).Error; err != nil {
		return err
	}
	if len(slots) != count || slots[0] != startMs {
		return marketversion.ErrCalendarSlots
	}
	return nil
}
