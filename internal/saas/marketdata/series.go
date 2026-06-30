package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SeriesTypeTradableAsset = saasstore.ResearchSeriesTypeTradableAsset
	SeriesTypeIndicator     = saasstore.ResearchSeriesTypeIndicator
	SeriesTypeDerived       = saasstore.ResearchSeriesTypeDerived

	RevisionPolicyCurrentHistorical = "current_historical"
	RevisionPolicyPointInTime       = "point_in_time"
	RevisionPolicyUnknown           = "unknown"
)

var (
	ErrUnsupportedSeries     = errors.New("unsupported research series")
	ErrUnsupportedSeriesType = errors.New("unsupported research series type")
)

type ResearchSeries struct {
	ID                 string         `json:"id"`
	SeriesType         string         `json:"series_type"`
	Symbol             string         `json:"symbol,omitempty"`
	DisplayName        string         `json:"display_name"`
	DataSource         string         `json:"data_source"`
	SourceInstrumentID string         `json:"source_instrument_id,omitempty"`
	SupportedIntervals []string       `json:"supported_intervals,omitempty"`
	Frequency          string         `json:"frequency"`
	Unit               string         `json:"unit"`
	Currency           string         `json:"currency,omitempty"`
	Market             string         `json:"market"`
	RevisionPolicy     string         `json:"revision_policy"`
	Tradable           bool           `json:"tradable"`
	Enabled            bool           `json:"enabled"`
	SortOrder          int            `json:"sort_order"`
	Metadata           map[string]any `json:"metadata,omitempty"`
}

type UpsertSeriesRequest struct {
	ID                 string         `json:"id"`
	SeriesType         string         `json:"series_type"`
	Symbol             string         `json:"symbol"`
	DisplayName        string         `json:"display_name"`
	DataSource         string         `json:"data_source"`
	SourceInstrumentID string         `json:"source_instrument_id"`
	SupportedIntervals []string       `json:"supported_intervals"`
	Frequency          string         `json:"frequency"`
	Unit               string         `json:"unit"`
	Currency           string         `json:"currency"`
	Market             string         `json:"market"`
	RevisionPolicy     string         `json:"revision_policy"`
	Tradable           *bool          `json:"tradable"`
	SortOrder          int            `json:"sort_order"`
	Metadata           map[string]any `json:"metadata"`
}

type SeriesStore struct {
	db *gorm.DB
}

func NewSeriesStore(db *gorm.DB) *SeriesStore {
	return &SeriesStore{db: db}
}

func SeedResearchSeries(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return nil
	}
	instruments, err := NewInstrumentStore(db).Instruments(ctx)
	if err != nil {
		return err
	}
	store := NewSeriesStore(db)
	return store.SyncTradableAssets(ctx, instruments)
}

func (s *SeriesStore) Series(ctx context.Context) ([]ResearchSeries, error) {
	if s == nil || s.db == nil {
		return seededTradableAssetSeries(), nil
	}
	var records []saasstore.ResearchSeries
	if err := s.db.WithContext(ctx).
		Where("enabled = ?", true).
		Order("sort_order ASC, display_name ASC, id ASC").
		Find(&records).Error; err != nil {
		return nil, err
	}
	out := make([]ResearchSeries, 0, len(records))
	for _, record := range records {
		series, err := recordToSeries(record)
		if err != nil {
			return nil, err
		}
		out = append(out, series)
	}
	return out, nil
}

func (s *SeriesStore) Upsert(ctx context.Context, req UpsertSeriesRequest) (ResearchSeries, error) {
	if s == nil || s.db == nil {
		return ResearchSeries{}, fmt.Errorf("series store is unavailable")
	}
	series, err := normalizeUpsertSeries(req)
	if err != nil {
		return ResearchSeries{}, err
	}
	record, err := seriesToRecord(series)
	if err != nil {
		return ResearchSeries{}, err
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"series_type", "symbol", "display_name", "data_source", "source_instrument_id",
			"supported_intervals", "frequency", "unit", "currency", "market", "revision_policy",
			"tradable", "enabled", "sort_order", "metadata", "updated_at",
		}),
	}).Create(&record).Error; err != nil {
		return ResearchSeries{}, err
	}
	return recordToSeries(record)
}

func (s *SeriesStore) Disable(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("series store is unavailable")
	}
	id = normalizeSeriesID(id)
	if id == "" {
		return ErrUnsupportedSeries
	}
	return s.db.WithContext(ctx).
		Model(&saasstore.ResearchSeries{}).
		Where("id = ?", id).
		Updates(map[string]any{"enabled": false}).Error
}

func (s *SeriesStore) SyncTradableAssets(ctx context.Context, instruments []ResearchInstrument) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("series store is unavailable")
	}
	records := make([]saasstore.ResearchSeries, 0, len(instruments))
	for _, instrument := range instruments {
		if !instrument.Enabled {
			continue
		}
		record, err := seriesToRecord(seriesFromInstrument(instrument))
		if err != nil {
			return err
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"series_type", "symbol", "display_name", "data_source", "source_instrument_id",
			"supported_intervals", "frequency", "unit", "currency", "market", "revision_policy",
			"tradable", "sort_order", "metadata", "updated_at",
		}),
	}).Create(&records).Error
}

func seededTradableAssetSeries() []ResearchSeries {
	instruments := Instruments()
	out := make([]ResearchSeries, 0, len(instruments))
	for _, instrument := range instruments {
		out = append(out, seriesFromInstrument(instrument))
	}
	return out
}

func seriesFromInstrument(instrument ResearchInstrument) ResearchSeries {
	id := normalizeInstrumentID(instrument.ID)
	source := normalizeSource(instrument.DataSource)
	metadata := map[string]any{
		"source":               "research_instrument",
		"source_instrument_id": id,
	}
	return ResearchSeries{
		ID:                 id,
		SeriesType:         SeriesTypeTradableAsset,
		Symbol:             normalizeSymbol(instrument.Symbol),
		DisplayName:        strings.TrimSpace(instrument.DisplayName),
		DataSource:         source,
		SourceInstrumentID: id,
		SupportedIntervals: normalizeIntervals(instrument.SupportedIntervals),
		Frequency:          "ohlcv",
		Unit:               "adjusted_price",
		Currency:           inferSeriesCurrency(instrument),
		Market:             strings.ToLower(strings.TrimSpace(instrument.Market)),
		RevisionPolicy:     RevisionPolicyCurrentHistorical,
		Tradable:           true,
		Enabled:            true,
		SortOrder:          instrument.SortOrder,
		Metadata:           metadata,
	}
}

func normalizeUpsertSeries(req UpsertSeriesRequest) (ResearchSeries, error) {
	seriesType := normalizeSeriesType(req.SeriesType)
	if seriesType == "" {
		seriesType = SeriesTypeIndicator
	}
	if !isSupportedSeriesType(seriesType) {
		return ResearchSeries{}, ErrUnsupportedSeriesType
	}
	id := normalizeSeriesID(req.ID)
	symbol := normalizeSymbol(req.Symbol)
	if id == "" {
		if symbol != "" {
			id = strings.TrimPrefix(symbol, "^")
		} else {
			id = normalizeSeriesID(req.DisplayName)
		}
	}
	if id == "" {
		return ResearchSeries{}, ErrUnsupportedSeries
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = firstNonEmpty(symbol, id)
	}
	source := normalizeSource(req.DataSource)
	if source == "" {
		source = "manual"
	}
	frequency := strings.TrimSpace(req.Frequency)
	if frequency == "" {
		frequency = "1d"
	}
	unit := strings.TrimSpace(req.Unit)
	if unit == "" {
		if seriesType == SeriesTypeTradableAsset {
			unit = "adjusted_price"
		} else {
			unit = "value"
		}
	}
	revisionPolicy := strings.TrimSpace(req.RevisionPolicy)
	if revisionPolicy == "" {
		revisionPolicy = RevisionPolicyCurrentHistorical
	}
	tradable := seriesType == SeriesTypeTradableAsset
	if req.Tradable != nil {
		tradable = *req.Tradable
	}
	market := strings.ToLower(strings.TrimSpace(req.Market))
	if market == "" {
		market = "global"
	}
	sortOrder := req.SortOrder
	if sortOrder == 0 {
		sortOrder = 1000
	}
	metadata := req.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	sourceInstrumentID := normalizeInstrumentID(req.SourceInstrumentID)
	return ResearchSeries{
		ID:                 id,
		SeriesType:         seriesType,
		Symbol:             symbol,
		DisplayName:        displayName,
		DataSource:         source,
		SourceInstrumentID: sourceInstrumentID,
		SupportedIntervals: normalizeIntervals(req.SupportedIntervals),
		Frequency:          frequency,
		Unit:               unit,
		Currency:           strings.ToUpper(strings.TrimSpace(req.Currency)),
		Market:             market,
		RevisionPolicy:     revisionPolicy,
		Tradable:           tradable,
		Enabled:            true,
		SortOrder:          sortOrder,
		Metadata:           metadata,
	}, nil
}

func seriesToRecord(series ResearchSeries) (saasstore.ResearchSeries, error) {
	intervals, err := saasstore.NewJSONB(normalizeIntervals(series.SupportedIntervals))
	if err != nil {
		return saasstore.ResearchSeries{}, err
	}
	metadata := series.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, err := saasstore.NewJSONB(metadata)
	if err != nil {
		return saasstore.ResearchSeries{}, err
	}
	return saasstore.ResearchSeries{
		ID:                 normalizeSeriesID(series.ID),
		SeriesType:         normalizeSeriesType(series.SeriesType),
		Symbol:             normalizeSymbol(series.Symbol),
		DisplayName:        strings.TrimSpace(series.DisplayName),
		DataSource:         normalizeSource(series.DataSource),
		SourceInstrumentID: normalizeInstrumentID(series.SourceInstrumentID),
		SupportedIntervals: intervals,
		Frequency:          strings.TrimSpace(series.Frequency),
		Unit:               strings.TrimSpace(series.Unit),
		Currency:           strings.ToUpper(strings.TrimSpace(series.Currency)),
		Market:             strings.ToLower(strings.TrimSpace(series.Market)),
		RevisionPolicy:     strings.TrimSpace(series.RevisionPolicy),
		Tradable:           series.Tradable,
		Enabled:            series.Enabled,
		SortOrder:          series.SortOrder,
		Metadata:           metadataJSON,
	}, nil
}

func recordToSeries(record saasstore.ResearchSeries) (ResearchSeries, error) {
	var intervals []string
	if len(record.SupportedIntervals) > 0 {
		if err := json.Unmarshal(record.SupportedIntervals, &intervals); err != nil {
			return ResearchSeries{}, err
		}
	}
	metadata := map[string]any{}
	if len(record.Metadata) > 0 {
		_ = json.Unmarshal(record.Metadata, &metadata)
	}
	return ResearchSeries{
		ID:                 record.ID,
		SeriesType:         record.SeriesType,
		Symbol:             record.Symbol,
		DisplayName:        record.DisplayName,
		DataSource:         record.DataSource,
		SourceInstrumentID: record.SourceInstrumentID,
		SupportedIntervals: normalizeIntervals(intervals),
		Frequency:          record.Frequency,
		Unit:               record.Unit,
		Currency:           record.Currency,
		Market:             record.Market,
		RevisionPolicy:     record.RevisionPolicy,
		Tradable:           record.Tradable,
		Enabled:            record.Enabled,
		SortOrder:          record.SortOrder,
		Metadata:           metadata,
	}, nil
}

func normalizeSeriesID(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func normalizeSeriesType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isSupportedSeriesType(value string) bool {
	switch value {
	case SeriesTypeTradableAsset, SeriesTypeIndicator, SeriesTypeDerived:
		return true
	default:
		return false
	}
}

func inferSeriesCurrency(instrument ResearchInstrument) string {
	if instrument.DataSource == DataSourceBinance || instrument.Market == "crypto" {
		return "USDT"
	}
	if instrument.Market == "tw" {
		return "TWD"
	}
	return "USD"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
