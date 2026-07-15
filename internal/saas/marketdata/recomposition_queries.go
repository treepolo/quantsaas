package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"quantsaas/internal/marketversion"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
)

func (s *Service) RecompositionSources(ctx context.Context, userID uint) ([]RecompositionSource, error) {
	instruments, err := s.instruments.Instruments(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]RecompositionSource, 0, len(instruments))
	for _, instrument := range instruments {
		if instrument.DataSource == DataSourceFRED {
			continue
		}
		var version saasstore.MarketDataVersion
		err := s.db.WithContext(ctx).
			Where("output_instrument_id = ? AND owner_user_id = ? AND status = ? AND integrity_status = ?", instrument.ID, userID, marketversion.VersionStatusCompleted, marketversion.IntegrityValid).
			Order("version_number DESC, id DESC").First(&version).Error
		if err == nil {
			if version.ArchivedAt != nil {
				continue
			}
			result = append(result, RecompositionSource{
				Instrument: instrument, VersionID: version.ID, ContentHash: version.ContentHash,
				ArtifactKind: version.ArtifactKind, Immutable: true, IntegrityStatus: version.IntegrityStatus,
			})
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		artifactKind := marketversion.ArtifactKindSourceSnapshot
		if instrument.DataSource == DataSourceGenerated {
			var metadata saasstore.DatasetMetadata
			if err := s.db.WithContext(ctx).Where("instrument_id = ? AND data_source = ?", instrument.ID, instrument.DataSource).
				Order("updated_at DESC").First(&metadata).Error; err != nil || metadata.PriceAdjustment != PriceAdjustmentGeneratedDailyLeverage {
				continue
			}
			artifactKind = marketversion.ArtifactKindDailyLeverage
		}
		result = append(result, RecompositionSource{Instrument: instrument, ArtifactKind: artifactKind})
	}
	return result, nil
}

func (s *Service) RecompositionSourceBars(ctx context.Context, userID uint, instrumentID string, versionID uint, interval string, startMs, endMs int64, limit int) ([]marketversion.Bar, error) {
	if limit <= 0 || limit > 5000 {
		limit = 2000
	}
	interval = normalizeInterval(interval)
	if startMs <= 0 || endMs < startMs || interval == "" {
		return nil, ErrInvalidRecomposition
	}
	if versionID != 0 {
		version, err := s.loadAccessibleMarketVersion(ctx, userID, versionID)
		if err != nil {
			return nil, err
		}
		if version.Interval != interval {
			return nil, ErrUnsupportedInterval
		}
		var rows []saasstore.MarketDataVersionBar
		if err := s.db.WithContext(ctx).Where("version_id = ? AND open_time BETWEEN ? AND ?", version.ID, startMs, endMs).
			Order("open_time ASC").Limit(limit).Find(&rows).Error; err != nil {
			return nil, err
		}
		return versionRowsToBars(rows), nil
	}
	instrument, err := s.instruments.ResolveInstrument(ctx, instrumentID, "", "")
	if err != nil {
		return nil, err
	}
	if instrument.DataSource == DataSourceFRED || !instrumentSupportsInterval(instrument, interval) {
		return nil, ErrUnsupportedSourceKind
	}
	var rows []saasstore.KLine
	if err := s.db.WithContext(ctx).
		Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time BETWEEN ? AND ?", instrument.ID, instrument.DataSource, instrument.Symbol, interval, startMs, endMs).
		Order("open_time ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]marketversion.Bar, 0, len(rows))
	for index, row := range rows {
		result = append(result, marketversion.Bar{Ordinal: index, OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	return result, nil
}

func (s *Service) GetRecompositionPlan(ctx context.Context, userID uint, planID uint) (RecompositionPlanDetail, error) {
	var record saasstore.RecompositionPlan
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", planID, userID).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return RecompositionPlanDetail{}, ErrRecompositionPlanNotFound
		}
		return RecompositionPlanDetail{}, err
	}
	preview, err := s.recompositionPreviewResult(record)
	if err != nil {
		return RecompositionPlanDetail{}, err
	}
	var rows []saasstore.RecompositionPlanSegment
	if err := s.db.WithContext(ctx).Where("plan_id = ?", record.ID).Order("segment_order ASC").Find(&rows).Error; err != nil {
		return RecompositionPlanDetail{}, err
	}
	segments := make([]RecompositionPlanSegmentDetail, 0, len(rows))
	for _, row := range rows {
		var version saasstore.MarketDataVersion
		if err := s.db.WithContext(ctx).First(&version, row.SourceVersionID).Error; err != nil {
			return RecompositionPlanDetail{}, err
		}
		displayName := version.Symbol
		if version.OutputInstrumentID != nil {
			if instrument, err := s.instruments.ResolveInstrument(ctx, *version.OutputInstrumentID, "", ""); err == nil {
				displayName = instrument.DisplayName
			}
		} else if instrument, err := s.instruments.ResolveInstrument(ctx, version.InstrumentID, "", ""); err == nil {
			displayName = instrument.DisplayName
		}
		segments = append(segments, RecompositionPlanSegmentDetail{
			ItemID: row.ItemID, Order: row.SegmentOrder, SourceVersionID: row.SourceVersionID,
			SourceInstrumentID: version.InstrumentID, SourceSymbol: version.Symbol, SourceDisplayName: displayName,
			SourceContentHash: row.SourceContentHash, StartTimeMs: row.SourceStartTimeMs, EndTimeMs: row.SourceEndTimeMs,
			BarCount: row.BarCount, RepeatCount: row.RepeatCount, PreviousClosePresent: row.PreviousClosePresent,
			PreviousClose: row.PreviousClose, FirstOpen: row.FirstOpen, SourceGapRatio: row.SourceGapRatio,
		})
	}
	return RecompositionPlanDetail{RecompositionPreviewResult: preview, Segments: segments, CreatedAt: record.CreatedAt.UTC().Format(timeLayout)}, nil
}

func (s *Service) RecompositionPreviewBars(ctx context.Context, userID uint, planID uint, limit, offset int) (VersionBarPage, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	var plan saasstore.RecompositionPlan
	if err := s.db.WithContext(ctx).Select("id", "total_output_bars").Where("id = ? AND owner_user_id = ?", planID, userID).First(&plan).Error; err != nil {
		return VersionBarPage{}, ErrRecompositionPlanNotFound
	}
	var rows []saasstore.RecompositionPreviewBar
	if err := s.db.WithContext(ctx).Where("plan_id = ?", planID).Order("ordinal ASC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return VersionBarPage{}, err
	}
	bars := make([]marketversion.Bar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, marketversion.Bar{Ordinal: row.Ordinal, OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	return VersionBarPage{Rows: bars, Total: int64(plan.TotalOutputBars), Limit: limit, Offset: offset}, nil
}

func (s *Service) CachedPreviewPlanFromTaskItem(raw json.RawMessage) (uint, string, error) {
	var cached previewCacheResult
	if err := json.Unmarshal(raw, &cached); err != nil {
		return 0, "", err
	}
	if cached.SchemaVersion != RecompositionPreviewResultVersion || cached.PlanID == 0 || strings.TrimSpace(cached.PlanHash) == "" {
		return 0, "", ErrInvalidRecomposition
	}
	return cached.PlanID, cached.PlanHash, nil
}

func versionRowsToBars(rows []saasstore.MarketDataVersionBar) []marketversion.Bar {
	result := make([]marketversion.Bar, 0, len(rows))
	for _, row := range rows {
		result = append(result, marketversion.Bar{Ordinal: row.Ordinal, OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	return result
}

const timeLayout = "2006-01-02T15:04:05Z07:00"
