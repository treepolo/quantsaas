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
	DataSourceBinance = "binance"
	DataSourceYahoo   = "yahoo"
	DataSourceFRED    = "fred"

	InstrumentBTCUSDT = "BTCUSDT"

	ExecutionModeCloseSameBar  = "close_same_bar"
	ExecutionModeCloseNextOpen = "close_next_open"
	ExecutionModePreclose10m   = "preclose_10m"
)

var (
	ErrUnsupportedInstrument = errors.New("unsupported research instrument")
	ErrUnsupportedSource     = errors.New("unsupported market data source")
)

type ResearchInstrument struct {
	ID                  string           `json:"id"`
	Symbol              string           `json:"symbol"`
	DisplayName         string           `json:"display_name"`
	DataSource          string           `json:"data_source"`
	SupportedIntervals  []string         `json:"supported_intervals"`
	AvailableStartMs    map[string]int64 `json:"available_start_ms,omitempty"`
	Market              string           `json:"market"`
	SortOrder           int              `json:"sort_order"`
	Enabled             bool             `json:"enabled"`
	LastAutoUpdateAt    string           `json:"last_auto_update_at,omitempty"`
	LastAutoUpdateError string           `json:"last_auto_update_error,omitempty"`
}

type InstrumentStore struct {
	db *gorm.DB
}

type UpsertInstrumentRequest struct {
	ID                 string   `json:"id"`
	Symbol             string   `json:"symbol"`
	DisplayName        string   `json:"display_name"`
	DataSource         string   `json:"data_source"`
	SupportedIntervals []string `json:"supported_intervals"`
	Market             string   `json:"market"`
	SortOrder          int      `json:"sort_order"`
}

type ReorderInstrumentRequest struct {
	IDs []string `json:"ids"`
}

var defaultYahooIntervals = []string{"1d", "1h", "1m", "1w", "1M"}
var defaultBinanceIntervals = []string{"1d", "1h", "15m", "5m", "1m", "1s", "1w", "1M"}
var defaultFredIntervals = []string{"1d"}

var seededResearchInstruments = []ResearchInstrument{
	{ID: "TQQQ", Symbol: "TQQQ", DisplayName: "TQQQ 三倍做多納指 ETF", DataSource: DataSourceYahoo, SupportedIntervals: defaultYahooIntervals, Market: "us", SortOrder: 10, Enabled: true},
	{ID: "SQQQ", Symbol: "SQQQ", DisplayName: "SQQQ 三倍做空納指 ETF", DataSource: DataSourceYahoo, SupportedIntervals: defaultYahooIntervals, Market: "us", SortOrder: 20, Enabled: true},
	{ID: "SOXL", Symbol: "SOXL", DisplayName: "SOXL 三倍做多費半 ETF", DataSource: DataSourceYahoo, SupportedIntervals: defaultYahooIntervals, Market: "us", SortOrder: 30, Enabled: true},
	{ID: "SOXS", Symbol: "SOXS", DisplayName: "SOXS 三倍做空費半 ETF", DataSource: DataSourceYahoo, SupportedIntervals: defaultYahooIntervals, Market: "us", SortOrder: 40, Enabled: true},
	{ID: "TWII", Symbol: "^TWII", DisplayName: "台灣加權指數", DataSource: DataSourceYahoo, SupportedIntervals: defaultYahooIntervals, Market: "tw", SortOrder: 50, Enabled: true},
	{ID: "GSPC", Symbol: "^GSPC", DisplayName: "標普 500 指數", DataSource: DataSourceYahoo, SupportedIntervals: defaultYahooIntervals, Market: "us", SortOrder: 60, Enabled: true},
	{ID: "NDX", Symbol: "^NDX", DisplayName: "納斯達克 100 指數", DataSource: DataSourceYahoo, SupportedIntervals: defaultYahooIntervals, Market: "us", SortOrder: 70, Enabled: true},
	{ID: "SOX", Symbol: "^SOX", DisplayName: "費城半導體指數", DataSource: DataSourceYahoo, SupportedIntervals: defaultYahooIntervals, Market: "us", SortOrder: 80, Enabled: true},
	{ID: InstrumentBTCUSDT, Symbol: "BTCUSDT", DisplayName: "比特幣現貨", DataSource: DataSourceBinance, SupportedIntervals: defaultBinanceIntervals, Market: "crypto", SortOrder: 90, Enabled: true},
}

func init() {
	seededResearchInstruments = append(seededResearchInstruments,
		ResearchInstrument{ID: "UNRATE", Symbol: "UNRATE", DisplayName: "美國失業率", DataSource: DataSourceFRED, SupportedIntervals: defaultFredIntervals, Market: "macro", SortOrder: 200, Enabled: true},
		ResearchInstrument{ID: "SOFR", Symbol: "SOFR", DisplayName: "SOFR 擔保隔夜融資利率", DataSource: DataSourceFRED, SupportedIntervals: defaultFredIntervals, Market: "macro", SortOrder: 210, Enabled: true},
		ResearchInstrument{ID: "BAMLH0A0HYM2", Symbol: "BAMLH0A0HYM2", DisplayName: "美國高收益債信用利差", DataSource: DataSourceFRED, SupportedIntervals: defaultFredIntervals, Market: "macro", SortOrder: 220, Enabled: true},
	)
}

func Instruments() []ResearchInstrument {
	out := make([]ResearchInstrument, len(seededResearchInstruments))
	copy(out, seededResearchInstruments)
	return out
}

func NewInstrumentStore(db *gorm.DB) *InstrumentStore {
	return &InstrumentStore{db: db}
}

func SeedResearchInstruments(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return nil
	}
	records := make([]saasstore.ResearchInstrument, 0, len(seededResearchInstruments))
	for _, instrument := range seededResearchInstruments {
		record, err := instrumentToRecord(instrument)
		if err != nil {
			return err
		}
		records = append(records, record)
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"symbol", "display_name", "data_source", "supported_intervals", "market", "updated_at",
		}),
	}).Create(&records).Error
}

func (s *InstrumentStore) Instruments(ctx context.Context) ([]ResearchInstrument, error) {
	if s == nil || s.db == nil {
		return Instruments(), nil
	}
	var records []saasstore.ResearchInstrument
	if err := s.db.WithContext(ctx).
		Where("enabled = ?", true).
		Order("sort_order ASC, display_name ASC, id ASC").
		Find(&records).Error; err != nil {
		return nil, err
	}
	out := make([]ResearchInstrument, 0, len(records))
	for _, record := range records {
		instrument, err := recordToInstrument(record)
		if err != nil {
			return nil, err
		}
		out = append(out, instrument)
	}
	return out, nil
}

func (s *InstrumentStore) ResolveInstrument(ctx context.Context, instrumentID string, symbol string, source string) (ResearchInstrument, error) {
	instrumentID = normalizeInstrumentID(instrumentID)
	symbol = normalizeSymbol(symbol)
	source = normalizeSource(source)
	if instrumentID == "" && symbol == "" {
		instrumentID = InstrumentBTCUSDT
	}
	if s != nil && s.db != nil {
		var records []saasstore.ResearchInstrument
		query := s.db.WithContext(ctx).Where("enabled = ?", true)
		if instrumentID != "" {
			query = query.Where("id = ?", instrumentID)
		} else {
			query = query.Where("symbol = ?", symbol)
			if source != "" {
				query = query.Where("data_source = ?", source)
			}
		}
		if err := query.Order("sort_order ASC").Find(&records).Error; err != nil {
			return ResearchInstrument{}, err
		}
		if len(records) > 0 {
			return recordToInstrument(records[0])
		}
	}
	return ResolveInstrument(instrumentID, symbol, source)
}

func (s *InstrumentStore) Upsert(ctx context.Context, req UpsertInstrumentRequest) (ResearchInstrument, error) {
	if s == nil || s.db == nil {
		return ResearchInstrument{}, fmt.Errorf("instrument store is unavailable")
	}
	instrument, err := normalizeUpsertInstrument(req)
	if err != nil {
		return ResearchInstrument{}, err
	}
	record, err := instrumentToRecord(instrument)
	if err != nil {
		return ResearchInstrument{}, err
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"symbol", "display_name", "data_source", "supported_intervals", "market", "sort_order", "enabled", "updated_at",
		}),
	}).Create(&record).Error; err != nil {
		return ResearchInstrument{}, err
	}
	return recordToInstrument(record)
}

func (s *InstrumentStore) Disable(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("instrument store is unavailable")
	}
	id = normalizeInstrumentID(id)
	if id == "" {
		return ErrUnsupportedInstrument
	}
	return s.db.WithContext(ctx).
		Model(&saasstore.ResearchInstrument{}).
		Where("id = ?", id).
		Updates(map[string]any{"enabled": false}).Error
}

func (s *InstrumentStore) Reorder(ctx context.Context, req ReorderInstrumentRequest) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("instrument store is unavailable")
	}
	ids := make([]string, 0, len(req.IDs))
	seen := map[string]bool{}
	for _, raw := range req.IDs {
		id := normalizeInstrumentID(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return ErrUnsupportedInstrument
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for index, id := range ids {
			result := tx.Model(&saasstore.ResearchInstrument{}).
				Where("id = ? AND enabled = ?", id, true).
				Update("sort_order", (index+1)*10)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return ErrUnsupportedInstrument
			}
		}
		return nil
	})
}

func ResolveInstrument(instrumentID string, symbol string, source string) (ResearchInstrument, error) {
	instrumentID = normalizeInstrumentID(instrumentID)
	symbol = normalizeSymbol(symbol)
	source = normalizeSource(source)
	if instrumentID == "" && symbol == "" {
		instrumentID = InstrumentBTCUSDT
	}
	for _, instrument := range seededResearchInstruments {
		if instrumentID != "" && normalizeInstrumentID(instrument.ID) == instrumentID {
			return instrument, nil
		}
		if symbol != "" && normalizeSymbol(instrument.Symbol) == symbol {
			if source == "" || normalizeSource(instrument.DataSource) == source {
				return instrument, nil
			}
		}
	}
	return ResearchInstrument{}, ErrUnsupportedInstrument
}

func normalizeUpsertInstrument(req UpsertInstrumentRequest) (ResearchInstrument, error) {
	source := normalizeSource(req.DataSource)
	if source == "" {
		source = DataSourceYahoo
	}
	if source != DataSourceYahoo && source != DataSourceBinance && source != DataSourceFRED {
		return ResearchInstrument{}, ErrUnsupportedSource
	}
	symbol := normalizeSymbol(req.Symbol)
	if symbol == "" {
		return ResearchInstrument{}, ErrUnsupportedInstrument
	}
	id := normalizeInstrumentID(req.ID)
	if id == "" {
		id = strings.TrimPrefix(symbol, "^")
	}
	if id == "" {
		return ResearchInstrument{}, ErrUnsupportedInstrument
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = symbol
	}
	intervals := normalizeIntervals(req.SupportedIntervals)
	if len(intervals) == 0 {
		if source == DataSourceBinance {
			intervals = defaultBinanceIntervals
		} else if source == DataSourceFRED {
			intervals = defaultFredIntervals
		} else {
			intervals = defaultYahooIntervals
		}
	}
	market := strings.ToLower(strings.TrimSpace(req.Market))
	if market == "" {
		market = inferMarket(id, symbol, source)
	}
	sortOrder := req.SortOrder
	if sortOrder == 0 {
		sortOrder = 1000
	}
	return ResearchInstrument{
		ID:                 id,
		Symbol:             symbol,
		DisplayName:        displayName,
		DataSource:         source,
		SupportedIntervals: intervals,
		Market:             market,
		SortOrder:          sortOrder,
		Enabled:            true,
	}, nil
}

func normalizeIntervals(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func inferMarket(id string, symbol string, source string) string {
	if source == DataSourceFRED {
		return "macro"
	}
	if source == DataSourceBinance || strings.Contains(symbol, "USDT") {
		return "crypto"
	}
	if strings.HasPrefix(symbol, "^TW") || id == "TWII" {
		return "tw"
	}
	return "us"
}

func instrumentToRecord(instrument ResearchInstrument) (saasstore.ResearchInstrument, error) {
	intervals, err := saasstore.NewJSONB(instrument.SupportedIntervals)
	if err != nil {
		return saasstore.ResearchInstrument{}, err
	}
	return saasstore.ResearchInstrument{
		ID:                  normalizeInstrumentID(instrument.ID),
		Symbol:              normalizeSymbol(instrument.Symbol),
		DisplayName:         displayNameForKnownInstrument(instrument),
		DataSource:          normalizeSource(instrument.DataSource),
		SupportedIntervals:  intervals,
		AvailableStartMs:    availableStartMsJSON(instrument.AvailableStartMs),
		Market:              strings.ToLower(strings.TrimSpace(instrument.Market)),
		SortOrder:           instrument.SortOrder,
		Enabled:             instrument.Enabled,
		LastAutoUpdateError: instrument.LastAutoUpdateError,
	}, nil
}

func displayNameForKnownInstrument(instrument ResearchInstrument) string {
	switch normalizeInstrumentID(instrument.ID) {
	case "TQQQ":
		return "TQQQ 三倍做多納指 ETF"
	case "SQQQ":
		return "SQQQ 三倍做空納指 ETF"
	case "SOXL":
		return "SOXL 三倍做多費半 ETF"
	case "SOXS":
		return "SOXS 三倍做空費半 ETF"
	case "TWII":
		return "台灣加權指數"
	case "GSPC":
		return "標普 500 指數"
	case "NDX":
		return "納斯達克 100 指數"
	case "SOX":
		return "費城半導體指數"
	case InstrumentBTCUSDT:
		return "比特幣現貨"
	default:
		return strings.TrimSpace(instrument.DisplayName)
	}
}

func recordToInstrument(record saasstore.ResearchInstrument) (ResearchInstrument, error) {
	var intervals []string
	if len(record.SupportedIntervals) > 0 {
		if err := json.Unmarshal(record.SupportedIntervals, &intervals); err != nil {
			return ResearchInstrument{}, err
		}
	}
	availableStartMs := map[string]int64{}
	if len(record.AvailableStartMs) > 0 {
		_ = json.Unmarshal(record.AvailableStartMs, &availableStartMs)
	}
	instrument := ResearchInstrument{
		ID:                  record.ID,
		Symbol:              record.Symbol,
		DisplayName:         record.DisplayName,
		DataSource:          record.DataSource,
		SupportedIntervals:  normalizeIntervals(intervals),
		AvailableStartMs:    normalizeAvailableStartMs(availableStartMs),
		Market:              record.Market,
		SortOrder:           record.SortOrder,
		Enabled:             record.Enabled,
		LastAutoUpdateError: record.LastAutoUpdateError,
	}
	if record.LastAutoUpdateAt != nil {
		instrument.LastAutoUpdateAt = record.LastAutoUpdateAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return instrument, nil
}

func availableStartMsJSON(values map[string]int64) saasstore.JSONB {
	normalized := normalizeAvailableStartMs(values)
	raw, err := saasstore.NewJSONB(normalized)
	if err != nil {
		return saasstore.JSONB([]byte(`{}`))
	}
	return raw
}

func normalizeAvailableStartMs(values map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for interval, value := range values {
		interval = normalizeInterval(interval)
		if interval == "" || value <= 0 {
			continue
		}
		out[interval] = value
	}
	return out
}

func SupportedExecutionModes() []string {
	return []string{ExecutionModeCloseSameBar, ExecutionModeCloseNextOpen, ExecutionModePreclose10m}
}

func NormalizeExecutionMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return ExecutionModeCloseSameBar
	}
	return mode
}

func IsSupportedExecutionMode(mode string) bool {
	mode = NormalizeExecutionMode(mode)
	for _, supported := range SupportedExecutionModes() {
		if mode == supported {
			return true
		}
	}
	return false
}

func normalizeInstrumentID(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func normalizeSource(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
