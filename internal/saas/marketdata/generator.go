package marketdata

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
)

var (
	ErrInvalidGenerateRequest = errors.New("行情產生參數不正確")
	ErrNoSourceRows           = errors.New("選定區間沒有可用母行情資料")
)

type GenerateLeveragedRequest struct {
	SourceInstrumentID string  `json:"source_instrument_id"`
	SourceInterval     string  `json:"source_interval"`
	StartTimeMs        int64   `json:"start_time_ms"`
	EndTimeMs          int64   `json:"end_time_ms"`
	Multiplier         float64 `json:"multiplier"`
	TargetInstrumentID string  `json:"target_instrument_id"`
	TargetSymbol       string  `json:"target_symbol"`
	TargetDisplayName  string  `json:"target_display_name"`
}

type GenerateLeveragedResult struct {
	Instrument           ResearchInstrument `json:"instrument"`
	SourceInstrumentID   string             `json:"source_instrument_id"`
	SourceDataSource     string             `json:"source_data_source"`
	SourceSymbol         string             `json:"source_symbol"`
	Interval             string             `json:"interval"`
	Multiplier           float64            `json:"multiplier"`
	GeneratedBars        int                `json:"generated_bars"`
	StoredBars           int64              `json:"stored_bars"`
	FirstOpenMs          int64              `json:"first_open_ms"`
	LastOpenMs           int64              `json:"last_open_ms"`
	UsedFallbackBaseline bool               `json:"used_fallback_baseline"`
	PriceAdjustment      string             `json:"price_adjustment"`
	PriceAdjustmentLabel string             `json:"price_adjustment_label"`
}

func (s *Service) GenerateLeveraged(ctx context.Context, req GenerateLeveragedRequest) (GenerateLeveragedResult, error) {
	if s == nil || s.db == nil {
		return GenerateLeveragedResult{}, fmt.Errorf("market data service is unavailable")
	}
	req.SourceInstrumentID = normalizeInstrumentID(req.SourceInstrumentID)
	req.SourceInterval = normalizeInterval(req.SourceInterval)
	req.TargetInstrumentID = normalizeInstrumentID(req.TargetInstrumentID)
	req.TargetSymbol = normalizeSymbol(req.TargetSymbol)
	req.TargetDisplayName = strings.TrimSpace(req.TargetDisplayName)
	if req.SourceInterval == "" {
		req.SourceInterval = "1d"
	}
	if err := validateGenerateLeveragedRequest(req); err != nil {
		return GenerateLeveragedResult{}, err
	}
	sourceInstrument, err := s.instruments.ResolveInstrument(ctx, req.SourceInstrumentID, "", "")
	if err != nil {
		return GenerateLeveragedResult{}, err
	}
	if req.TargetInstrumentID == sourceInstrument.ID || req.TargetSymbol == sourceInstrument.Symbol {
		return GenerateLeveragedResult{}, ErrInvalidGenerateRequest
	}
	if existing, err := s.instruments.ResolveInstrument(ctx, req.TargetInstrumentID, "", ""); err == nil && existing.DataSource != DataSourceGenerated {
		return GenerateLeveragedResult{}, ErrInvalidGenerateRequest
	}
	if existing, err := s.instruments.ResolveInstrument(ctx, "", req.TargetSymbol, ""); err == nil && existing.ID != req.TargetInstrumentID {
		return GenerateLeveragedResult{}, ErrInvalidGenerateRequest
	}
	if !instrumentSupportsInterval(sourceInstrument, req.SourceInterval) {
		return GenerateLeveragedResult{}, ErrUnsupportedInterval
	}
	sourceRows, previousClose, err := s.loadSourceRowsForGeneration(ctx, sourceInstrument, req.SourceInterval, req.StartTimeMs, req.EndTimeMs)
	if err != nil {
		return GenerateLeveragedResult{}, err
	}
	generatedRows, usedFallback, err := buildDailyLeveragedRows(sourceRows, previousClose, req.Multiplier)
	if err != nil {
		return GenerateLeveragedResult{}, err
	}
	targetReq := UpsertInstrumentRequest{
		ID:                 req.TargetInstrumentID,
		Symbol:             req.TargetSymbol,
		DisplayName:        req.TargetDisplayName,
		DataSource:         DataSourceGenerated,
		SupportedIntervals: []string{req.SourceInterval},
		Market:             sourceInstrument.Market,
		SortOrder:          1000,
	}
	var result GenerateLeveragedResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txSvc := *s
		txSvc.db = tx
		txSvc.instruments = NewInstrumentStore(tx)
		targetInstrument, err := txSvc.instruments.Upsert(ctx, targetReq)
		if err != nil {
			return err
		}
		targetInstrument.AvailableStartMs = map[string]int64{req.SourceInterval: generatedRows[0].OpenTime}
		if err := tx.Model(&saasstore.ResearchInstrument{}).
			Where("id = ?", targetInstrument.ID).
			Update("available_start_ms", availableStartMsJSON(targetInstrument.AvailableStartMs)).Error; err != nil {
			return err
		}
		existing := txSvc.datasetBounds(ctx, ImportRequest{
			InstrumentID: targetInstrument.ID,
			DataSource:   targetInstrument.DataSource,
			Symbol:       targetInstrument.Symbol,
			Interval:     req.SourceInterval,
		})
		if _, err := txSvc.deleteKLinesReturningRows(ctx, targetInstrument.ID, targetInstrument.DataSource, targetInstrument.Symbol, req.SourceInterval); err != nil {
			return err
		}
		stored, err := txSvc.storeKLines(ctx, targetInstrument.ID, targetInstrument.DataSource, targetInstrument.Symbol, req.SourceInterval, generatedRows)
		if err != nil {
			return err
		}
		importResult := ImportResult{
			InstrumentID: targetInstrument.ID,
			DataSource:   targetInstrument.DataSource,
			Symbol:       targetInstrument.Symbol,
			Interval:     req.SourceInterval,
			StartTimeMs:  req.StartTimeMs,
			EndTimeMs:    req.EndTimeMs,
			FetchedBars:  len(generatedRows),
			StoredBars:   stored,
			FirstOpenMs:  generatedRows[0].OpenTime,
			LastOpenMs:   generatedRows[len(generatedRows)-1].OpenTime,
		}
		adjustment, err := txSvc.recordDatasetMetadata(ctx, ImportRequest{
			InstrumentID: targetInstrument.ID,
			DataSource:   targetInstrument.DataSource,
			Symbol:       targetInstrument.Symbol,
			Interval:     req.SourceInterval,
			StartTimeMs:  req.StartTimeMs,
			EndTimeMs:    req.EndTimeMs,
		}, importResult, existing)
		if err != nil {
			return err
		}
		result = GenerateLeveragedResult{
			Instrument:           targetInstrument,
			SourceInstrumentID:   sourceInstrument.ID,
			SourceDataSource:     sourceInstrument.DataSource,
			SourceSymbol:         sourceInstrument.Symbol,
			Interval:             req.SourceInterval,
			Multiplier:           req.Multiplier,
			GeneratedBars:        len(generatedRows),
			StoredBars:           stored,
			FirstOpenMs:          generatedRows[0].OpenTime,
			LastOpenMs:           generatedRows[len(generatedRows)-1].OpenTime,
			UsedFallbackBaseline: usedFallback,
			PriceAdjustment:      adjustment,
			PriceAdjustmentLabel: priceAdjustmentLabel(adjustment),
		}
		return nil
	})
	if err != nil {
		return GenerateLeveragedResult{}, err
	}
	return result, nil
}

func validateGenerateLeveragedRequest(req GenerateLeveragedRequest) error {
	if req.SourceInstrumentID == "" || req.TargetInstrumentID == "" || req.TargetSymbol == "" {
		return ErrInvalidGenerateRequest
	}
	if len(req.TargetInstrumentID) > 32 || len(req.TargetSymbol) > 32 {
		return ErrInvalidGenerateRequest
	}
	if _, ok := intervalDurations[req.SourceInterval]; !ok {
		return ErrUnsupportedInterval
	}
	if req.StartTimeMs <= 0 || req.EndTimeMs <= 0 || req.StartTimeMs > req.EndTimeMs {
		return ErrInvalidRange
	}
	if req.Multiplier <= 0 || math.IsNaN(req.Multiplier) || math.IsInf(req.Multiplier, 0) {
		return ErrInvalidGenerateRequest
	}
	return nil
}

func (s *Service) loadSourceRowsForGeneration(ctx context.Context, instrument ResearchInstrument, interval string, startMs int64, endMs int64) ([]BinanceKLine, *float64, error) {
	var rows []saasstore.KLine
	if err := s.db.WithContext(ctx).
		Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time BETWEEN ? AND ?", instrument.ID, instrument.DataSource, instrument.Symbol, interval, startMs, endMs).
		Order("open_time ASC").
		Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 {
		return nil, nil, ErrNoSourceRows
	}
	var previous saasstore.KLine
	err := s.db.WithContext(ctx).
		Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time < ?", instrument.ID, instrument.DataSource, instrument.Symbol, interval, rows[0].OpenTime).
		Order("open_time DESC").
		First(&previous).Error
	var previousClose *float64
	if err == nil {
		value := previous.Close
		previousClose = &value
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}
	return kLineRowsToBars(rows), previousClose, nil
}

func buildDailyLeveragedRows(sourceRows []BinanceKLine, previousClose *float64, multiplier float64) ([]BinanceKLine, bool, error) {
	if len(sourceRows) == 0 {
		return nil, false, ErrNoSourceRows
	}
	out := make([]BinanceKLine, 0, len(sourceRows))
	prevChildClose := 1.0
	prevSourceClose := 0.0
	usedFallback := previousClose == nil
	if previousClose != nil {
		prevSourceClose = *previousClose
	}
	for index, row := range sourceRows {
		if row.Open <= 0 || row.Close <= 0 {
			return nil, usedFallback, ErrInvalidGenerateRequest
		}
		childOpen := 0.0
		childClose := 0.0
		if index == 0 && usedFallback {
			childOpen = 1
			childClose = prevChildClose * (1 + multiplier*(row.Close/row.Open-1))
		} else {
			if prevSourceClose <= 0 {
				return nil, usedFallback, ErrInvalidGenerateRequest
			}
			childOpen = prevChildClose * (1 + multiplier*(row.Open/prevSourceClose-1))
			childClose = prevChildClose * (1 + multiplier*(row.Close/prevSourceClose-1))
		}
		if childOpen <= 0 || childClose <= 0 || math.IsNaN(childOpen) || math.IsNaN(childClose) || math.IsInf(childOpen, 0) || math.IsInf(childClose, 0) {
			return nil, usedFallback, fmt.Errorf("%w: 倍率後價格小於等於 0", ErrInvalidGenerateRequest)
		}
		out = append(out, BinanceKLine{
			OpenTime: row.OpenTime,
			Open:     childOpen,
			High:     math.Max(childOpen, childClose),
			Low:      math.Min(childOpen, childClose),
			Close:    childClose,
			Volume:   0,
		})
		prevSourceClose = row.Close
		prevChildClose = childClose
	}
	return out, usedFallback, nil
}
