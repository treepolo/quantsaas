package marketdata

import (
	"context"
	"errors"
	"strings"

	"quantsaas/internal/marketversion"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
)

const ArtifactKindReferenceIndicator = "reference_indicator"

type MarketChartSource struct {
	Instrument      ResearchInstrument `json:"instrument"`
	VersionID       uint               `json:"version_id,omitempty"`
	VersionNumber   int                `json:"version_number,omitempty"`
	ContentHash     string             `json:"content_hash,omitempty"`
	ArtifactKind    string             `json:"artifact_kind"`
	DisplayName     string             `json:"display_name"`
	SeriesName      string             `json:"series_name,omitempty"`
	Interval        string             `json:"interval"`
	StartTimeMs     int64              `json:"start_time_ms"`
	EndTimeMs       int64              `json:"end_time_ms"`
	BarCount        int64              `json:"bar_count"`
	Immutable       bool               `json:"immutable"`
	IntegrityStatus string             `json:"integrity_status,omitempty"`
	CanBacktest     bool               `json:"can_backtest"`
}

func (s *Service) MarketChartSources(ctx context.Context, userID uint) ([]MarketChartSource, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("行情資料服務尚未就緒")
	}
	instruments, err := s.instruments.Instruments(ctx)
	if err != nil {
		return nil, err
	}
	type liveBounds struct {
		InstrumentID string
		Source       string
		Symbol       string
		Interval     string
		BarCount     int64
		StartTimeMs  int64
		EndTimeMs    int64
	}
	instrumentIDs := make([]string, 0, len(instruments))
	for _, instrument := range instruments {
		instrumentIDs = append(instrumentIDs, instrument.ID)
	}
	liveRows := make([]liveBounds, 0)
	if len(instrumentIDs) > 0 {
		if err := s.db.WithContext(ctx).Model(&saasstore.KLine{}).
			Select("instrument_id, source, symbol, interval, COUNT(*) AS bar_count, MIN(open_time) AS start_time_ms, MAX(open_time) AS end_time_ms").
			Where("instrument_id IN ?", instrumentIDs).
			Group("instrument_id, source, symbol, interval").
			Scan(&liveRows).Error; err != nil {
			return nil, err
		}
	}
	liveByIdentity := make(map[string]liveBounds, len(liveRows))
	for _, row := range liveRows {
		liveByIdentity[marketChartIdentity(row.InstrumentID, row.Source, row.Symbol, row.Interval)] = row
	}
	generatedKinds := map[string]string{}
	if len(instrumentIDs) > 0 {
		var metadata []saasstore.DatasetMetadata
		if err := s.db.WithContext(ctx).
			Where("instrument_id IN ? AND price_adjustment = ?", instrumentIDs, PriceAdjustmentGeneratedDailyLeverage).
			Find(&metadata).Error; err != nil {
			return nil, err
		}
		for _, item := range metadata {
			generatedKinds[marketChartIdentity(item.InstrumentID, item.DataSource, item.Symbol, item.Interval)] = marketversion.ArtifactKindDailyLeverage
		}
	}
	result := make([]MarketChartSource, 0, len(instruments))
	for _, instrument := range instruments {
		for _, interval := range instrument.SupportedIntervals {
			identity := marketChartIdentity(instrument.ID, instrument.DataSource, instrument.Symbol, interval)
			bounds, ok := liveByIdentity[identity]
			if !ok || bounds.BarCount == 0 {
				continue
			}
			kind := marketversion.ArtifactKindSourceSnapshot
			if instrument.DataSource == DataSourceFRED {
				kind = ArtifactKindReferenceIndicator
			} else if generatedKind := generatedKinds[identity]; generatedKind != "" {
				kind = generatedKind
			}
			result = append(result, MarketChartSource{
				Instrument: instrument, ArtifactKind: kind, DisplayName: instrument.DisplayName, Interval: interval,
				StartTimeMs: bounds.StartTimeMs, EndTimeMs: bounds.EndTimeMs, BarCount: bounds.BarCount,
				CanBacktest: kind != ArtifactKindReferenceIndicator,
			})
		}
	}

	var versions []saasstore.MarketDataVersion
	if err := s.db.WithContext(ctx).
		Where("owner_user_id=? AND artifact_kind <> ? AND status=? AND integrity_status=? AND published=true AND archived_at IS NULL",
			userID, marketversion.ArtifactKindSourceSnapshot, marketversion.VersionStatusCompleted, marketversion.IntegrityValid).
		Order("created_at DESC,id DESC").Find(&versions).Error; err != nil {
		return nil, err
	}
	seriesNames := map[uint]string{}
	for _, version := range versions {
		if version.MarketSeriesID != nil {
			seriesNames[*version.MarketSeriesID] = ""
		}
	}
	if len(seriesNames) > 0 {
		ids := make([]uint, 0, len(seriesNames))
		for id := range seriesNames {
			ids = append(ids, id)
		}
		var series []saasstore.MarketSeries
		if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&series).Error; err != nil {
			return nil, err
		}
		for _, item := range series {
			seriesNames[item.ID] = item.Name
		}
	}
	for _, version := range versions {
		instrumentID := version.InstrumentID
		if version.OutputInstrumentID != nil {
			instrumentID = *version.OutputInstrumentID
		}
		instrument, err := s.instruments.ResolveInstrument(ctx, instrumentID, version.Symbol, version.DataSource)
		if err != nil {
			if errors.Is(err, ErrUnsupportedInstrument) {
				// 商品停用後不可再建立新工作，但既有不可變版本仍可能留在資料庫。
				// 略過這筆已退出使用的版本，不讓它拖垮所有仍可用的行情來源。
				continue
			}
			return nil, err
		}
		seriesName := ""
		if version.MarketSeriesID != nil {
			seriesName = strings.TrimSpace(seriesNames[*version.MarketSeriesID])
		}
		displayName := instrument.DisplayName
		if seriesName != "" {
			displayName = seriesName + " · " + instrument.DisplayName
		}
		result = append(result, MarketChartSource{
			Instrument: instrument, VersionID: version.ID, VersionNumber: version.VersionNumber,
			ContentHash: version.ContentHash, ArtifactKind: version.ArtifactKind, DisplayName: displayName,
			SeriesName: seriesName, Interval: version.Interval, StartTimeMs: version.StartTimeMs,
			EndTimeMs: version.EndTimeMs, BarCount: int64(version.BarCount), Immutable: true,
			IntegrityStatus: version.IntegrityStatus, CanBacktest: true,
		})
	}
	return result, nil
}

func marketChartIdentity(instrumentID, source, symbol, interval string) string {
	return strings.Join([]string{instrumentID, source, symbol, interval}, "\x00")
}

func (s *Service) MarketChartBars(ctx context.Context, userID uint, instrumentID string, versionID uint, interval string, startMs, endMs int64, limit int) ([]marketversion.Bar, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	interval = normalizeInterval(interval)
	if startMs <= 0 || endMs < startMs || interval == "" {
		return nil, ErrInvalidRange
	}
	if versionID != 0 {
		var version saasstore.MarketDataVersion
		if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=? AND status=? AND integrity_status=? AND published=true AND archived_at IS NULL",
			versionID, userID, marketversion.VersionStatusCompleted, marketversion.IntegrityValid).First(&version).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, errors.New("找不到可查看的行情版本")
			}
			return nil, err
		}
		if version.Interval != interval {
			return nil, ErrUnsupportedInterval
		}
		var rows []saasstore.MarketDataVersionBar
		if err := s.db.WithContext(ctx).Where("version_id=? AND open_time BETWEEN ? AND ?", version.ID, startMs, endMs).Order("ordinal ASC").Limit(limit).Find(&rows).Error; err != nil {
			return nil, err
		}
		return versionRowsToBars(rows), nil
	}
	instrument, err := s.instruments.ResolveInstrument(ctx, instrumentID, "", "")
	if err != nil {
		return nil, err
	}
	if !instrumentSupportsInterval(instrument, interval) {
		return nil, ErrUnsupportedInterval
	}
	var rows []saasstore.KLine
	if err := s.db.WithContext(ctx).Where("instrument_id=? AND source=? AND symbol=? AND interval=? AND open_time BETWEEN ? AND ?", instrument.ID, instrument.DataSource, instrument.Symbol, interval, startMs, endMs).
		Order("open_time ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]marketversion.Bar, 0, len(rows))
	for index, row := range rows {
		result = append(result, marketversion.Bar{Ordinal: index, OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	return result, nil
}
