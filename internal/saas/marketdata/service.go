package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	DefaultBaseURL      = "https://api.binance.com"
	DefaultYahooBaseURL = "https://query1.finance.yahoo.com"
	DefaultSymbol       = "BTCUSDT"

	PriceAdjustmentLegacyUnknown = "legacy_unknown"
	PriceAdjustmentRawExchange   = "raw_exchange_v1"
	PriceAdjustmentYahooAdjusted = "yahoo_adjusted_ohlc_v1"
	PriceAdjustmentYahooIntraday = "yahoo_raw_intraday_v1"

	yahooUserAgent          = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0 Safari/537.36 QuantSaaS/0.1"
	yahooMinRequestInterval = 1200 * time.Millisecond
	yahooDailyReadyDelay    = 90 * time.Minute
)

var (
	ErrUnsupportedInterval = errors.New("不支援的資料週期")
	ErrInvalidRange        = errors.New("資料起訖時間不正確")
)

var intervalDurations = map[string]time.Duration{
	"1s":  time.Second,
	"1m":  time.Minute,
	"3m":  3 * time.Minute,
	"5m":  5 * time.Minute,
	"15m": 15 * time.Minute,
	"30m": 30 * time.Minute,
	"1h":  time.Hour,
	"2h":  2 * time.Hour,
	"4h":  4 * time.Hour,
	"6h":  6 * time.Hour,
	"8h":  8 * time.Hour,
	"12h": 12 * time.Hour,
	"1d":  24 * time.Hour,
	"3d":  3 * 24 * time.Hour,
	"1w":  7 * 24 * time.Hour,
	"1M":  31 * 24 * time.Hour,
}

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type YahooClient struct {
	BaseURL    string
	HTTPClient *http.Client
	mu         sync.Mutex
	lastAt     time.Time
}

type BinanceKLine struct {
	OpenTime int64
	Open     float64
	High     float64
	Low      float64
	Close    float64
	Volume   float64
}

type Service struct {
	db          *gorm.DB
	client      *Client
	yahooClient *YahooClient
	instruments *InstrumentStore
	series      *SeriesStore
	now         func() time.Time
}

type ImportRequest struct {
	InstrumentID             string `json:"instrument_id"`
	DataSource               string `json:"data_source"`
	Symbol                   string `json:"symbol"`
	Interval                 string `json:"interval"`
	StartTimeMs              int64  `json:"start_time_ms"`
	EndTimeMs                int64  `json:"end_time_ms"`
	IncludePrecloseSnapshots bool   `json:"include_preclose_snapshots"`
}

type ImportResult struct {
	InstrumentID          string `json:"instrument_id"`
	DataSource            string `json:"data_source"`
	Symbol                string `json:"symbol"`
	Interval              string `json:"interval"`
	StartTimeMs           int64  `json:"start_time_ms"`
	EndTimeMs             int64  `json:"end_time_ms"`
	FetchedBars           int    `json:"fetched_bars"`
	StoredBars            int64  `json:"stored_bars"`
	PrecloseSnapshotCount int64  `json:"preclose_snapshot_count"`
	FirstOpenMs           int64  `json:"first_open_ms,omitempty"`
	LastOpenMs            int64  `json:"last_open_ms,omitempty"`
	PriceAdjustment       string `json:"price_adjustment,omitempty"`
	PriceAdjustmentLabel  string `json:"price_adjustment_label,omitempty"`
}

type AutoUpdateResult struct {
	InstrumentID         string `json:"instrument_id"`
	DataSource           string `json:"data_source"`
	Symbol               string `json:"symbol"`
	Interval             string `json:"interval"`
	StoredBars           int64  `json:"stored_bars"`
	Skipped              bool   `json:"skipped,omitempty"`
	Reason               string `json:"reason,omitempty"`
	LastOpenMs           int64  `json:"last_open_ms,omitempty"`
	ExpectedLatestOpenMs int64  `json:"expected_latest_open_ms,omitempty"`
	Error                string `json:"error,omitempty"`
}

type AvailableStartResult struct {
	InstrumentID string            `json:"instrument_id"`
	DataSource   string            `json:"data_source"`
	Symbol       string            `json:"symbol"`
	Starts       map[string]int64  `json:"starts"`
	Errors       map[string]string `json:"errors,omitempty"`
}

type MaintenanceResult struct {
	InstrumentID string                     `json:"instrument_id"`
	DataSource   string                     `json:"data_source"`
	Symbol       string                     `json:"symbol"`
	Datasets     []MaintenanceDatasetResult `json:"datasets"`
	HasIssues    bool                       `json:"has_issues"`
	Error        string                     `json:"error,omitempty"`
}

type MaintenanceDatasetResult struct {
	Interval             string `json:"interval"`
	Count                int64  `json:"count"`
	ExpectedCount        int64  `json:"expected_count,omitempty"`
	InvalidOpenTimeCount int64  `json:"invalid_open_time_count"`
	NeedsFullReimport    bool   `json:"needs_full_reimport"`
	PriceAdjustment      string `json:"price_adjustment"`
	PriceAdjustmentLabel string `json:"price_adjustment_label"`
	ReimportedDaily      bool   `json:"reimported_daily,omitempty"`
	RebuiltFromDaily     bool   `json:"rebuilt_from_daily,omitempty"`
	StoredBars           int64  `json:"stored_bars,omitempty"`
	DeletedRows          int64  `json:"deleted_rows,omitempty"`
	FirstOpenMs          int64  `json:"first_open_ms,omitempty"`
	LastOpenMs           int64  `json:"last_open_ms,omitempty"`
	ExpectedFirstOpenMs  int64  `json:"expected_first_open_ms,omitempty"`
	ExpectedLastOpenMs   int64  `json:"expected_last_open_ms,omitempty"`
	Error                string `json:"error,omitempty"`
}

type DatasetSummary struct {
	InstrumentID           string `json:"instrument_id"`
	DataSource             string `json:"data_source"`
	Symbol                 string `json:"symbol"`
	Market                 string `json:"market"`
	Interval               string `json:"interval"`
	Count                  int64  `json:"count"`
	PrecloseSnapshotCount  int64  `json:"preclose_snapshot_count"`
	FirstPrecloseMs        int64  `json:"first_preclose_ms,omitempty"`
	LastPrecloseMs         int64  `json:"last_preclose_ms,omitempty"`
	FirstOpenMs            int64  `json:"first_open_ms,omitempty"`
	LastOpenMs             int64  `json:"last_open_ms,omitempty"`
	ExpectedLatestOpenMs   int64  `json:"expected_latest_open_ms,omitempty"`
	IsFresh                bool   `json:"is_fresh"`
	UpdatedAt              string `json:"updated_at,omitempty"`
	PriceAdjustment        string `json:"price_adjustment"`
	PriceAdjustmentLabel   string `json:"price_adjustment_label"`
	PriceAdjustmentNote    string `json:"price_adjustment_note"`
	NeedsFullReimport      bool   `json:"needs_full_reimport"`
	PriceMetadataUpdatedAt string `json:"price_metadata_updated_at,omitempty"`
}

func NewService(db *gorm.DB, client *Client) *Service {
	if client == nil {
		client = NewClient(DefaultBaseURL)
	}
	return &Service{db: db, client: client, yahooClient: NewYahooClient(DefaultYahooBaseURL), instruments: NewInstrumentStore(db), series: NewSeriesStore(db), now: func() time.Time { return time.Now().UTC() }}
}

func NewClient(baseURL string) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func NewYahooClient(baseURL string) *YahooClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultYahooBaseURL
	}
	return &YahooClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func SupportedIntervals() []string {
	return []string{"1d", "1h", "15m", "5m", "1m", "1s", "1w", "1M"}
}

func (s *Service) Instruments(ctx context.Context) ([]ResearchInstrument, error) {
	return s.instruments.Instruments(ctx)
}

func (s *Service) ResolveInstrument(ctx context.Context, instrumentID string, symbol string, source string) (ResearchInstrument, error) {
	return s.instruments.ResolveInstrument(ctx, instrumentID, symbol, source)
}

func (s *Service) UpsertInstrument(ctx context.Context, req UpsertInstrumentRequest) (ResearchInstrument, error) {
	instrument, err := s.instruments.Upsert(ctx, req)
	if err != nil {
		return ResearchInstrument{}, err
	}
	_ = s.series.SyncTradableAssets(ctx, []ResearchInstrument{instrument})
	result, err := s.RefreshAvailableStarts(ctx, instrument.ID)
	if err == nil && len(result.Starts) > 0 {
		instrument.AvailableStartMs = result.Starts
	}
	return instrument, nil
}

func (s *Service) DisableInstrument(ctx context.Context, id string) error {
	return s.instruments.Disable(ctx, id)
}

func (s *Service) ReorderInstruments(ctx context.Context, req ReorderInstrumentRequest) error {
	return s.instruments.Reorder(ctx, req)
}

func (s *Service) Series(ctx context.Context) ([]ResearchSeries, error) {
	return s.series.Series(ctx)
}

func (s *Service) UpsertSeries(ctx context.Context, req UpsertSeriesRequest) (ResearchSeries, error) {
	return s.series.Upsert(ctx, req)
}

func (s *Service) DisableSeries(ctx context.Context, id string) error {
	return s.series.Disable(ctx, id)
}

func (s *Service) SyncTradableAssetSeries(ctx context.Context) error {
	instruments, err := s.Instruments(ctx)
	if err != nil {
		return err
	}
	return s.series.SyncTradableAssets(ctx, instruments)
}

func (s *Service) Import(ctx context.Context, req ImportRequest) (ImportResult, error) {
	req = s.normalizeImportRequest(req)
	if err := s.validateImportRequest(ctx, req); err != nil {
		return ImportResult{}, err
	}
	existing := s.datasetBounds(ctx, req)

	result := ImportResult{
		InstrumentID:         req.InstrumentID,
		DataSource:           req.DataSource,
		Symbol:               req.Symbol,
		Interval:             req.Interval,
		StartTimeMs:          req.StartTimeMs,
		EndTimeMs:            req.EndTimeMs,
		PriceAdjustment:      currentPriceAdjustment(req.DataSource, req.Interval),
		PriceAdjustmentLabel: priceAdjustmentLabel(currentPriceAdjustment(req.DataSource, req.Interval)),
	}
	if req.DataSource == DataSourceYahoo {
		rows, err := s.yahooClient.FetchKLines(ctx, req.Symbol, req.Interval, req.StartTimeMs, req.EndTimeMs)
		if err != nil {
			return ImportResult{}, err
		}
		rows = normalizeYahooRowsForStorage(req, rows, s.now())
		if len(rows) > 0 && isFullCoverageImport(existing, req) {
			if err := s.deleteKLines(ctx, req.InstrumentID, req.DataSource, req.Symbol, req.Interval); err != nil {
				return ImportResult{}, err
			}
		}
		stored, err := s.storeKLines(ctx, req.InstrumentID, req.DataSource, req.Symbol, req.Interval, rows)
		if err != nil {
			return ImportResult{}, err
		}
		result.FetchedBars = len(rows)
		result.StoredBars = stored
		if len(rows) > 0 {
			result.FirstOpenMs = rows[0].OpenTime
			result.LastOpenMs = rows[len(rows)-1].OpenTime
		}
		adjustment, err := s.recordDatasetMetadata(ctx, req, result, existing)
		if err != nil {
			return ImportResult{}, err
		}
		result.PriceAdjustment = adjustment
		result.PriceAdjustmentLabel = priceAdjustmentLabel(adjustment)
		if req.IncludePrecloseSnapshots {
			count, err := s.importPrecloseSnapshots(ctx, req)
			if err != nil {
				return ImportResult{}, err
			}
			result.PrecloseSnapshotCount = count
		}
		return result, nil
	}

	cursor := req.StartTimeMs
	step := intervalDurations[req.Interval].Milliseconds()
	if step <= 0 {
		return ImportResult{}, ErrUnsupportedInterval
	}

	for cursor <= req.EndTimeMs {
		if err := ctx.Err(); err != nil {
			return ImportResult{}, err
		}
		rows, err := s.client.FetchKLines(ctx, req.Symbol, req.Interval, cursor, req.EndTimeMs, 1000)
		if err != nil {
			return ImportResult{}, err
		}
		if len(rows) == 0 {
			break
		}
		stored, err := s.storeKLines(ctx, req.InstrumentID, req.DataSource, req.Symbol, req.Interval, rows)
		if err != nil {
			return ImportResult{}, err
		}
		result.FetchedBars += len(rows)
		result.StoredBars += stored
		if result.FirstOpenMs == 0 {
			result.FirstOpenMs = rows[0].OpenTime
		}
		result.LastOpenMs = rows[len(rows)-1].OpenTime

		next := rows[len(rows)-1].OpenTime + step
		if next <= cursor {
			break
		}
		cursor = next
		if len(rows) < 1000 {
			break
		}
	}
	if req.IncludePrecloseSnapshots {
		count, err := s.importPrecloseSnapshots(ctx, req)
		if err != nil {
			return ImportResult{}, err
		}
		result.PrecloseSnapshotCount = count
	}
	adjustment, err := s.recordDatasetMetadata(ctx, req, result, existing)
	if err != nil {
		return ImportResult{}, err
	}
	result.PriceAdjustment = adjustment
	result.PriceAdjustmentLabel = priceAdjustmentLabel(adjustment)

	return result, nil
}

func (s *Service) Summaries(ctx context.Context, symbol string, intervals []string) ([]DatasetSummary, error) {
	instrument, err := s.instruments.ResolveInstrument(ctx, "", symbol, "")
	if err != nil {
		return nil, err
	}
	if len(intervals) == 0 {
		intervals = instrument.SupportedIntervals
	}
	for i, interval := range intervals {
		intervals[i] = normalizeInterval(interval)
	}
	metadata, err := s.datasetMetadataByInterval(ctx, instrument, intervals)
	if err != nil {
		return nil, err
	}

	rows := make([]DatasetSummary, 0, len(intervals))
	for _, interval := range intervals {
		if !instrumentSupportsInterval(instrument, interval) {
			continue
		}
		var summary struct {
			Count       int64
			FirstOpenMs *int64
			LastOpenMs  *int64
			UpdatedAt   *time.Time
		}
		if err := s.db.WithContext(ctx).
			Model(&saasstore.KLine{}).
			Select("count(*) as count, min(open_time) as first_open_ms, max(open_time) as last_open_ms, max(updated_at) as updated_at").
			Where("instrument_id = ? AND source = ? AND interval = ?", instrument.ID, instrument.DataSource, interval).
			Scan(&summary).Error; err != nil {
			return nil, err
		}
		item := DatasetSummary{InstrumentID: instrument.ID, DataSource: instrument.DataSource, Symbol: instrument.Symbol, Market: instrument.Market, Interval: interval, Count: summary.Count}
		if summary.FirstOpenMs != nil {
			item.FirstOpenMs = *summary.FirstOpenMs
		}
		if summary.LastOpenMs != nil {
			item.LastOpenMs = *summary.LastOpenMs
		}
		if summary.UpdatedAt != nil {
			item.UpdatedAt = summary.UpdatedAt.UTC().Format(time.RFC3339)
		}
		s.enrichPriceAdjustment(instrument, &item, metadata[interval])
		if interval == "1d" {
			var snapshotSummary struct {
				Count       int64
				FirstOpenMs *int64
				LastOpenMs  *int64
			}
			if err := s.db.WithContext(ctx).
				Model(&saasstore.DailyExecutionSnapshot{}).
				Select("count(*) as count, min(observed_at_ms) as first_open_ms, max(observed_at_ms) as last_open_ms").
				Where("instrument_id = ? AND data_source = ? AND snapshot_type = ?", instrument.ID, instrument.DataSource, ExecutionModePreclose10m).
				Scan(&snapshotSummary).Error; err != nil {
				return nil, err
			}
			item.PrecloseSnapshotCount = snapshotSummary.Count
			if snapshotSummary.FirstOpenMs != nil {
				item.FirstPrecloseMs = *snapshotSummary.FirstOpenMs
			}
			if snapshotSummary.LastOpenMs != nil {
				item.LastPrecloseMs = *snapshotSummary.LastOpenMs
			}
		}
		s.enrichFreshness(instrument, &item)
		rows = append(rows, item)
	}
	return rows, nil
}

type datasetBounds struct {
	Count       int64
	FirstOpenMs int64
	LastOpenMs  int64
}

func (s *Service) datasetBounds(ctx context.Context, req ImportRequest) datasetBounds {
	if s.db == nil {
		return datasetBounds{}
	}
	var summary struct {
		Count       int64
		FirstOpenMs *int64
		LastOpenMs  *int64
	}
	_ = s.db.WithContext(ctx).
		Model(&saasstore.KLine{}).
		Select("count(*) as count, min(open_time) as first_open_ms, max(open_time) as last_open_ms").
		Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ?", req.InstrumentID, req.DataSource, req.Symbol, req.Interval).
		Scan(&summary).Error
	out := datasetBounds{Count: summary.Count}
	if summary.FirstOpenMs != nil {
		out.FirstOpenMs = *summary.FirstOpenMs
	}
	if summary.LastOpenMs != nil {
		out.LastOpenMs = *summary.LastOpenMs
	}
	return out
}

func isFullCoverageImport(existing datasetBounds, req ImportRequest) bool {
	return existing.Count == 0 || existing.FirstOpenMs == 0 || req.StartTimeMs <= existing.FirstOpenMs
}

func (s *Service) deleteKLines(ctx context.Context, instrumentID string, source string, symbol string, interval string) error {
	_, err := s.deleteKLinesReturningRows(ctx, instrumentID, source, symbol, interval)
	return err
}

func (s *Service) deleteKLinesReturningRows(ctx context.Context, instrumentID string, source string, symbol string, interval string) (int64, error) {
	if s.db == nil {
		return 0, nil
	}
	tx := s.db.WithContext(ctx).
		Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ?", instrumentID, source, symbol, interval).
		Delete(&saasstore.KLine{})
	return tx.RowsAffected, tx.Error
}

func (s *Service) recordDatasetMetadata(ctx context.Context, req ImportRequest, result ImportResult, existing datasetBounds) (string, error) {
	current := currentPriceAdjustment(req.DataSource, req.Interval)
	if s.db == nil || result.FetchedBars == 0 {
		return current, nil
	}
	fullCoverage := isFullCoverageImport(existing, req)
	if !fullCoverage {
		var existingMeta saasstore.DatasetMetadata
		err := s.db.WithContext(ctx).
			Where("instrument_id = ? AND data_source = ? AND symbol = ? AND interval = ?", req.InstrumentID, req.DataSource, req.Symbol, req.Interval).
			First(&existingMeta).Error
		if err == nil && existingMeta.PriceAdjustment == current && existingMeta.FullCoverage {
			existingMeta.ImportedEndMs = maxInt64(existingMeta.ImportedEndMs, result.LastOpenMs)
			return current, s.db.WithContext(ctx).Save(&existingMeta).Error
		}
		if err == nil {
			return existingMeta.PriceAdjustment, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return current, err
		}
		current = PriceAdjustmentLegacyUnknown
	}
	row := saasstore.DatasetMetadata{
		InstrumentID:    req.InstrumentID,
		DataSource:      req.DataSource,
		Symbol:          req.Symbol,
		Interval:        req.Interval,
		PriceAdjustment: current,
		ImportedStartMs: result.FirstOpenMs,
		ImportedEndMs:   result.LastOpenMs,
		FullCoverage:    fullCoverage,
	}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "instrument_id"},
			{Name: "data_source"},
			{Name: "symbol"},
			{Name: "interval"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"price_adjustment", "imported_start_ms", "imported_end_ms", "full_coverage", "updated_at"}),
	}).Create(&row).Error
	return current, err
}

func (s *Service) datasetMetadataByInterval(ctx context.Context, instrument ResearchInstrument, intervals []string) (map[string]saasstore.DatasetMetadata, error) {
	out := map[string]saasstore.DatasetMetadata{}
	if s.db == nil || len(intervals) == 0 {
		return out, nil
	}
	var rows []saasstore.DatasetMetadata
	if err := s.db.WithContext(ctx).
		Where("instrument_id = ? AND data_source = ? AND symbol = ? AND interval IN ?", instrument.ID, instrument.DataSource, instrument.Symbol, intervals).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.Interval] = row
	}
	return out, nil
}

func (s *Service) loadKLines(ctx context.Context, instrument ResearchInstrument, interval string) ([]saasstore.KLine, error) {
	if s.db == nil {
		return nil, nil
	}
	var rows []saasstore.KLine
	err := s.db.WithContext(ctx).
		Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ?", instrument.ID, instrument.DataSource, instrument.Symbol, interval).
		Order("open_time ASC").
		Find(&rows).Error
	return rows, err
}

func (s *Service) AllSummaries(ctx context.Context) ([]InstrumentSummary, error) {
	instruments, err := s.Instruments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]InstrumentSummary, 0, len(instruments))
	for _, instrument := range instruments {
		rows, err := s.Summaries(ctx, instrument.Symbol, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, InstrumentSummary{Instrument: instrument, Datasets: rows})
	}
	return out, nil
}

type InstrumentSummary struct {
	Instrument ResearchInstrument `json:"instrument"`
	Datasets   []DatasetSummary   `json:"datasets"`
}

func (s *Service) UpdateLatest(ctx context.Context) ([]AutoUpdateResult, error) {
	instruments, err := s.Instruments(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]AutoUpdateResult, 0)
	now := s.now()
	for _, instrument := range instruments {
		var instrumentErr string
		for _, interval := range autoUpdateIntervals(instrument.SupportedIntervals) {
			latestOpenMs := s.latestDatasetOpenMs(instrument, interval)
			expectedOpenMs := expectedLatestOpenMs(instrument, interval, now)
			item := AutoUpdateResult{
				InstrumentID:         instrument.ID,
				DataSource:           instrument.DataSource,
				Symbol:               instrument.Symbol,
				Interval:             interval,
				LastOpenMs:           latestOpenMs,
				ExpectedLatestOpenMs: expectedOpenMs,
			}
			if shouldSkipLatestUpdate(latestOpenMs, expectedOpenMs) {
				item.Skipped = true
				item.Reason = "already_fresh"
				results = append(results, item)
				continue
			}
			req := ImportRequest{
				InstrumentID: instrument.ID,
				DataSource:   instrument.DataSource,
				Symbol:       instrument.Symbol,
				Interval:     interval,
				StartTimeMs:  latestOnlyUpdateStartFromLatest(latestOpenMs, interval, now),
				EndTimeMs:    now.UnixMilli(),
			}
			if req.StartTimeMs > req.EndTimeMs {
				item.Skipped = true
				item.Reason = "invalid_range"
				results = append(results, item)
				continue
			}
			imported, err := s.Import(ctx, req)
			if err != nil {
				item.Error = err.Error()
				instrumentErr = err.Error()
			} else {
				item.StoredBars = imported.StoredBars
				if imported.LastOpenMs > 0 {
					item.LastOpenMs = imported.LastOpenMs
				}
			}
			results = append(results, item)
		}
		s.recordAutoUpdate(ctx, instrument.ID, instrumentErr)
	}
	return results, nil
}

func (s *Service) RefreshAvailableStarts(ctx context.Context, instrumentID string) (AvailableStartResult, error) {
	instrument, err := s.instruments.ResolveInstrument(ctx, instrumentID, "", "")
	if err != nil {
		return AvailableStartResult{}, err
	}
	result := AvailableStartResult{
		InstrumentID: instrument.ID,
		DataSource:   instrument.DataSource,
		Symbol:       instrument.Symbol,
		Starts:       map[string]int64{},
		Errors:       map[string]string{},
	}
	for interval, start := range instrument.AvailableStartMs {
		if start > 0 {
			result.Starts[normalizeInterval(interval)] = start
		}
	}
	for _, rawInterval := range instrument.SupportedIntervals {
		interval := normalizeInterval(rawInterval)
		if interval == "" {
			continue
		}
		start, err := s.detectAvailableStart(ctx, instrument, interval)
		if err != nil {
			result.Errors[interval] = err.Error()
			continue
		}
		if start > 0 {
			result.Starts[interval] = start
		}
	}
	if len(result.Starts) > 0 && s.db != nil {
		if err := s.db.WithContext(ctx).
			Model(&saasstore.ResearchInstrument{}).
			Where("id = ?", instrument.ID).
			Updates(map[string]any{
				"available_start_ms": availableStartMsJSON(result.Starts),
				"updated_at":         s.now(),
			}).Error; err != nil {
			return result, err
		}
	}
	if len(result.Errors) == 0 {
		result.Errors = nil
	}
	return result, nil
}

func (s *Service) RefreshAllAvailableStarts(ctx context.Context) ([]AvailableStartResult, error) {
	instruments, err := s.Instruments(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]AvailableStartResult, 0, len(instruments))
	for _, instrument := range instruments {
		result, err := s.RefreshAvailableStarts(ctx, instrument.ID)
		if err != nil {
			results = append(results, AvailableStartResult{
				InstrumentID: instrument.ID,
				DataSource:   instrument.DataSource,
				Symbol:       instrument.Symbol,
				Starts:       map[string]int64{},
				Errors:       map[string]string{"_": err.Error()},
			})
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Service) AuditMaintenance(ctx context.Context, instrumentID string) ([]MaintenanceResult, error) {
	instruments, err := s.maintenanceInstruments(ctx, instrumentID)
	if err != nil {
		return nil, err
	}
	results := make([]MaintenanceResult, 0, len(instruments))
	for _, instrument := range instruments {
		result, err := s.auditInstrumentMaintenance(ctx, instrument)
		if err != nil {
			results = append(results, MaintenanceResult{
				InstrumentID: instrument.ID,
				DataSource:   instrument.DataSource,
				Symbol:       instrument.Symbol,
				Error:        err.Error(),
				HasIssues:    true,
			})
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Service) RepairMaintenance(ctx context.Context, instrumentID string) ([]MaintenanceResult, error) {
	instruments, err := s.maintenanceInstruments(ctx, instrumentID)
	if err != nil {
		return nil, err
	}
	results := make([]MaintenanceResult, 0, len(instruments))
	for _, instrument := range instruments {
		result, err := s.repairInstrumentMaintenance(ctx, instrument)
		if err != nil {
			results = append(results, MaintenanceResult{
				InstrumentID: instrument.ID,
				DataSource:   instrument.DataSource,
				Symbol:       instrument.Symbol,
				Error:        err.Error(),
				HasIssues:    true,
			})
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Service) maintenanceInstruments(ctx context.Context, instrumentID string) ([]ResearchInstrument, error) {
	if strings.TrimSpace(instrumentID) != "" {
		instrument, err := s.instruments.ResolveInstrument(ctx, instrumentID, "", "")
		if err != nil {
			return nil, err
		}
		return []ResearchInstrument{instrument}, nil
	}
	return s.Instruments(ctx)
}

func (s *Service) auditInstrumentMaintenance(ctx context.Context, instrument ResearchInstrument) (MaintenanceResult, error) {
	intervals := maintenanceIntervals(instrument)
	metadata, err := s.datasetMetadataByInterval(ctx, instrument, intervals)
	if err != nil {
		return MaintenanceResult{}, err
	}
	out := MaintenanceResult{InstrumentID: instrument.ID, DataSource: instrument.DataSource, Symbol: instrument.Symbol}
	for _, interval := range intervals {
		item, err := s.auditMaintenanceDataset(ctx, instrument, interval, metadata[interval])
		if err != nil {
			item = MaintenanceDatasetResult{Interval: interval, Error: err.Error()}
		}
		if maintenanceDatasetHasIssues(item) {
			out.HasIssues = true
		}
		out.Datasets = append(out.Datasets, item)
	}
	return out, nil
}

func (s *Service) repairInstrumentMaintenance(ctx context.Context, instrument ResearchInstrument) (MaintenanceResult, error) {
	before, err := s.auditInstrumentMaintenance(ctx, instrument)
	if err != nil {
		return MaintenanceResult{}, err
	}
	out := MaintenanceResult{InstrumentID: instrument.ID, DataSource: instrument.DataSource, Symbol: instrument.Symbol}
	dailyReport := maintenanceDatasetByInterval(before.Datasets, "1d")
	if instrumentSupportsInterval(instrument, "1d") && (dailyReport.Count == 0 || dailyReport.InvalidOpenTimeCount > 0 || dailyReport.NeedsFullReimport) {
		repaired, err := s.reimportDailyForMaintenance(ctx, instrument)
		if err != nil {
			repaired = dailyReport
			repaired.Error = err.Error()
		}
		out.Datasets = append(out.Datasets, repaired)
	} else if instrumentSupportsInterval(instrument, "1d") {
		out.Datasets = append(out.Datasets, dailyReport)
	}
	if instrumentSupportsInterval(instrument, "1w") {
		item, err := s.rebuildAggregateForMaintenance(ctx, instrument, "1w")
		if err != nil {
			item = maintenanceDatasetByInterval(before.Datasets, "1w")
			item.Error = err.Error()
		}
		out.Datasets = append(out.Datasets, item)
	}
	if instrumentSupportsInterval(instrument, "1M") {
		item, err := s.rebuildAggregateForMaintenance(ctx, instrument, "1M")
		if err != nil {
			item = maintenanceDatasetByInterval(before.Datasets, "1M")
			item.Error = err.Error()
		}
		out.Datasets = append(out.Datasets, item)
	}
	for _, item := range out.Datasets {
		if maintenanceDatasetHasIssues(item) || item.ReimportedDaily || item.RebuiltFromDaily {
			out.HasIssues = true
			break
		}
	}
	return out, nil
}

func (s *Service) detectAvailableStart(ctx context.Context, instrument ResearchInstrument, interval string) (int64, error) {
	end := s.now().UnixMilli()
	switch instrument.DataSource {
	case DataSourceYahoo:
		interval = normalizeInterval(interval)
		if interval == "1w" || interval == "1M" {
			return s.detectYahooAggregateAvailableStart(ctx, instrument, interval, end)
		}
		var lastErr error
		for _, start := range yahooAvailableStartProbeStarts(interval, s.now()) {
			rows, err := s.yahooClient.FetchKLines(ctx, instrument.Symbol, interval, start, end)
			if err != nil {
				lastErr = err
				continue
			}
			rows = normalizeYahooRowsForStorage(ImportRequest{
				InstrumentID: instrument.ID,
				DataSource:   instrument.DataSource,
				Symbol:       instrument.Symbol,
				Interval:     interval,
				StartTimeMs:  start,
				EndTimeMs:    end,
			}, rows, s.now())
			if first := firstRowOpenTime(rows); first > 0 {
				return first, nil
			}
		}
		if lastErr != nil {
			return 0, lastErr
		}
		return 0, nil
	case DataSourceBinance:
		rows, err := s.client.FetchKLines(ctx, instrument.Symbol, interval, 0, end, 1)
		if err != nil {
			return 0, err
		}
		return firstRowOpenTime(rows), nil
	default:
		return 0, ErrUnsupportedSource
	}
}

func (s *Service) detectYahooAggregateAvailableStart(ctx context.Context, instrument ResearchInstrument, interval string, end int64) (int64, error) {
	var lastErr error
	for _, start := range yahooAvailableStartProbeStarts("1d", s.now()) {
		rows, err := s.yahooClient.FetchKLines(ctx, instrument.Symbol, "1d", start, end)
		if err != nil {
			lastErr = err
			continue
		}
		rows = normalizeYahooRowsForStorage(ImportRequest{
			InstrumentID: instrument.ID,
			DataSource:   instrument.DataSource,
			Symbol:       instrument.Symbol,
			Interval:     "1d",
			StartTimeMs:  start,
			EndTimeMs:    end,
		}, rows, s.now())
		first := firstRowOpenTime(rows)
		if first <= 0 {
			continue
		}
		period, ok := aggregateYahooPeriod(instrument.Symbol, first, interval)
		if !ok {
			return first, nil
		}
		return period.StartMs, nil
	}
	if lastErr != nil {
		return 0, lastErr
	}
	return 0, nil
}

func yahooAvailableStartProbeStarts(interval string, now time.Time) []int64 {
	switch normalizeInterval(interval) {
	case "1m":
		return []int64{
			now.AddDate(0, 0, -7).UnixMilli(),
			now.AddDate(0, 0, -5).UnixMilli(),
			now.AddDate(0, 0, -2).UnixMilli(),
		}
	case "1h":
		return []int64{
			now.AddDate(-1, 0, 0).UnixMilli(),
			now.AddDate(0, -6, 0).UnixMilli(),
			now.AddDate(0, -2, 0).UnixMilli(),
			now.AddDate(0, 0, -30).UnixMilli(),
		}
	default:
		return []int64{0}
	}
}

func firstRowOpenTime(rows []BinanceKLine) int64 {
	if len(rows) == 0 {
		return 0
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].OpenTime < rows[j].OpenTime })
	for _, row := range rows {
		if row.OpenTime > 0 && row.Close > 0 {
			return row.OpenTime
		}
	}
	return 0
}

func maintenanceIntervals(instrument ResearchInstrument) []string {
	out := make([]string, 0, 3)
	for _, interval := range []string{"1d", "1w", "1M"} {
		if instrumentSupportsInterval(instrument, interval) {
			out = append(out, interval)
		}
	}
	return out
}

func maintenanceDatasetByInterval(items []MaintenanceDatasetResult, interval string) MaintenanceDatasetResult {
	for _, item := range items {
		if item.Interval == interval {
			return item
		}
	}
	return MaintenanceDatasetResult{Interval: interval}
}

func maintenanceDatasetHasIssues(item MaintenanceDatasetResult) bool {
	return item.Error != "" || item.InvalidOpenTimeCount > 0 || item.NeedsFullReimport || (item.ExpectedCount > 0 && item.Count != item.ExpectedCount) || (item.ExpectedLastOpenMs > 0 && item.LastOpenMs != item.ExpectedLastOpenMs)
}

func (s *Service) auditMaintenanceDataset(ctx context.Context, instrument ResearchInstrument, interval string, metadata saasstore.DatasetMetadata) (MaintenanceDatasetResult, error) {
	rows, err := s.loadKLines(ctx, instrument, interval)
	if err != nil {
		return MaintenanceDatasetResult{}, err
	}
	item := MaintenanceDatasetResult{
		Interval:             interval,
		Count:                int64(len(rows)),
		PriceAdjustment:      metadata.PriceAdjustment,
		PriceAdjustmentLabel: priceAdjustmentLabel(metadata.PriceAdjustment),
		NeedsFullReimport:    len(rows) > 0 && currentPriceAdjustment(instrument.DataSource, interval) != metadata.PriceAdjustment,
	}
	if item.PriceAdjustment == "" {
		if len(rows) == 0 {
			item.PriceAdjustment = currentPriceAdjustment(instrument.DataSource, interval)
		} else {
			item.PriceAdjustment = PriceAdjustmentLegacyUnknown
			item.NeedsFullReimport = true
		}
		item.PriceAdjustmentLabel = priceAdjustmentLabel(item.PriceAdjustment)
	}
	for _, row := range rows {
		if row.OpenTime > 0 {
			if item.FirstOpenMs == 0 || row.OpenTime < item.FirstOpenMs {
				item.FirstOpenMs = row.OpenTime
			}
			if row.OpenTime > item.LastOpenMs {
				item.LastOpenMs = row.OpenTime
			}
		}
		if !isCanonicalKLineOpenTime(instrument, interval, row.OpenTime) {
			item.InvalidOpenTimeCount++
		}
	}
	if interval == "1w" || interval == "1M" {
		dailyRows, err := s.loadKLines(ctx, instrument, "1d")
		if err != nil {
			return item, err
		}
		expected := aggregateRowsForInstrument(instrument, kLineRowsToBars(dailyRows), interval, s.now().UnixMilli())
		item.ExpectedCount = int64(len(expected))
		if len(expected) > 0 {
			item.ExpectedFirstOpenMs = expected[0].OpenTime
			item.ExpectedLastOpenMs = expected[len(expected)-1].OpenTime
		}
	}
	return item, nil
}

func (s *Service) reimportDailyForMaintenance(ctx context.Context, instrument ResearchInstrument) (MaintenanceDatasetResult, error) {
	start := instrument.AvailableStartMs["1d"]
	if start <= 0 {
		bounds := s.datasetBounds(ctx, ImportRequest{InstrumentID: instrument.ID, DataSource: instrument.DataSource, Symbol: instrument.Symbol, Interval: "1d"})
		start = bounds.FirstOpenMs
	}
	if start <= 0 {
		start = s.latestOnlyUpdateStart(instrument, "1d")
	}
	imported, err := s.Import(ctx, ImportRequest{
		InstrumentID: instrument.ID,
		DataSource:   instrument.DataSource,
		Symbol:       instrument.Symbol,
		Interval:     "1d",
		StartTimeMs:  start,
		EndTimeMs:    s.now().UnixMilli(),
	})
	if err != nil {
		return MaintenanceDatasetResult{Interval: "1d"}, err
	}
	metadata, err := s.datasetMetadataByInterval(ctx, instrument, []string{"1d"})
	if err != nil {
		return MaintenanceDatasetResult{}, err
	}
	item, err := s.auditMaintenanceDataset(ctx, instrument, "1d", metadata["1d"])
	if err != nil {
		return item, err
	}
	item.ReimportedDaily = true
	item.StoredBars = imported.StoredBars
	return item, nil
}

func (s *Service) rebuildAggregateForMaintenance(ctx context.Context, instrument ResearchInstrument, interval string) (MaintenanceDatasetResult, error) {
	dailyRows, err := s.loadKLines(ctx, instrument, "1d")
	if err != nil {
		return MaintenanceDatasetResult{Interval: interval}, err
	}
	rows := aggregateRowsForInstrument(instrument, kLineRowsToBars(dailyRows), interval, s.now().UnixMilli())
	deleted, err := s.deleteKLinesReturningRows(ctx, instrument.ID, instrument.DataSource, instrument.Symbol, interval)
	if err != nil {
		return MaintenanceDatasetResult{Interval: interval}, err
	}
	stored, err := s.storeKLines(ctx, instrument.ID, instrument.DataSource, instrument.Symbol, interval, rows)
	if err != nil {
		return MaintenanceDatasetResult{Interval: interval}, err
	}
	result := ImportResult{
		InstrumentID: instrument.ID,
		DataSource:   instrument.DataSource,
		Symbol:       instrument.Symbol,
		Interval:     interval,
		FetchedBars:  len(rows),
		StoredBars:   stored,
	}
	if len(rows) > 0 {
		result.FirstOpenMs = rows[0].OpenTime
		result.LastOpenMs = rows[len(rows)-1].OpenTime
	}
	if _, err := s.recordDatasetMetadata(ctx, ImportRequest{
		InstrumentID: instrument.ID,
		DataSource:   instrument.DataSource,
		Symbol:       instrument.Symbol,
		Interval:     interval,
		StartTimeMs:  result.FirstOpenMs,
		EndTimeMs:    s.now().UnixMilli(),
	}, result, datasetBounds{}); err != nil {
		return MaintenanceDatasetResult{Interval: interval}, err
	}
	metadata, err := s.datasetMetadataByInterval(ctx, instrument, []string{interval})
	if err != nil {
		return MaintenanceDatasetResult{Interval: interval}, err
	}
	item, err := s.auditMaintenanceDataset(ctx, instrument, interval, metadata[interval])
	if err != nil {
		return item, err
	}
	item.RebuiltFromDaily = true
	item.StoredBars = stored
	item.DeletedRows = deleted
	return item, nil
}

func (s *Service) normalizeImportRequest(req ImportRequest) ImportRequest {
	instrument, err := s.instruments.ResolveInstrument(context.Background(), req.InstrumentID, req.Symbol, req.DataSource)
	if err == nil {
		req.InstrumentID = instrument.ID
		req.Symbol = instrument.Symbol
		req.DataSource = instrument.DataSource
	} else {
		req.Symbol = normalizeSymbol(req.Symbol)
		req.DataSource = normalizeSource(req.DataSource)
		if req.Symbol == "" {
			req.Symbol = DefaultSymbol
			req.InstrumentID = InstrumentBTCUSDT
			req.DataSource = DataSourceBinance
		}
	}
	req.Interval = normalizeInterval(req.Interval)
	if req.Interval == "" {
		req.Interval = "1d"
	}
	if req.EndTimeMs == 0 {
		req.EndTimeMs = s.now().UnixMilli()
	}
	if req.StartTimeMs == 0 {
		req.StartTimeMs = s.defaultStartTime(req.Symbol, req.Interval)
	}
	return req
}

func (s *Service) defaultStartTime(symbol string, interval string) int64 {
	var latest struct {
		LastOpenMs *int64
	}
	if s.db != nil {
		_ = s.db.Model(&saasstore.KLine{}).
			Select("max(open_time) as last_open_ms").
			Where("symbol = ? AND interval = ?", symbol, interval).
			Scan(&latest).Error
	}
	if latest.LastOpenMs != nil {
		if d, ok := intervalDurations[interval]; ok {
			return *latest.LastOpenMs + d.Milliseconds()
		}
	}
	switch interval {
	case "1s":
		return s.now().Add(-24 * time.Hour).UnixMilli()
	case "1m", "3m", "5m", "15m", "30m":
		return s.now().AddDate(0, 0, -90).UnixMilli()
	case "1h", "2h", "4h", "6h", "8h", "12h":
		return s.now().AddDate(-2, 0, 0).UnixMilli()
	}
	if symbol == DefaultSymbol {
		return time.Date(2017, 8, 17, 0, 0, 0, 0, time.UTC).UnixMilli()
	}
	return s.now().AddDate(-10, 0, 0).UnixMilli()
}

func autoUpdateIntervals(supported []string) []string {
	allowed := map[string]bool{"1d": true, "1w": true, "1M": true}
	out := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, raw := range supported {
		interval := normalizeInterval(raw)
		if !allowed[interval] || seen[interval] {
			continue
		}
		seen[interval] = true
		out = append(out, interval)
	}
	return out
}

func (s *Service) latestOnlyUpdateStart(instrument ResearchInstrument, interval string) int64 {
	return latestOnlyUpdateStartFromLatest(s.latestDatasetOpenMs(instrument, interval), interval, s.now())
}

func (s *Service) latestDatasetOpenMs(instrument ResearchInstrument, interval string) int64 {
	var latest struct {
		LastOpenMs *int64
	}
	if s.db != nil {
		_ = s.db.Model(&saasstore.KLine{}).
			Select("max(open_time) as last_open_ms").
			Where("instrument_id = ? AND source = ? AND interval = ?", instrument.ID, instrument.DataSource, interval).
			Scan(&latest).Error
	}
	if latest.LastOpenMs != nil {
		return *latest.LastOpenMs
	}
	return 0
}

func latestOnlyUpdateStartFromLatest(latestOpenMs int64, interval string, now time.Time) int64 {
	if latestOpenMs > 0 {
		if d, ok := intervalDurations[interval]; ok {
			return latestOpenMs + d.Milliseconds()
		}
	}
	switch interval {
	case "1d":
		return now.AddDate(0, 0, -10).UnixMilli()
	case "1w":
		return now.AddDate(0, 0, -70).UnixMilli()
	case "1M":
		return now.AddDate(0, -8, 0).UnixMilli()
	default:
		return now.AddDate(0, 0, -10).UnixMilli()
	}
}

func shouldSkipLatestUpdate(latestOpenMs int64, expectedLatestOpenMs int64) bool {
	return latestOpenMs > 0 && expectedLatestOpenMs > 0 && latestOpenMs >= expectedLatestOpenMs
}

func (s *Service) recordAutoUpdate(ctx context.Context, instrumentID string, errMessage string) {
	if s.db == nil {
		return
	}
	updates := map[string]any{
		"last_auto_update_at":    s.now(),
		"last_auto_update_error": errMessage,
	}
	_ = s.db.WithContext(ctx).Model(&saasstore.ResearchInstrument{}).Where("id = ?", instrumentID).Updates(updates).Error
}

func (s *Service) enrichFreshness(instrument ResearchInstrument, item *DatasetSummary) {
	expected := expectedLatestOpenMs(instrument, item.Interval, s.now())
	item.ExpectedLatestOpenMs = expected
	if expected == 0 {
		item.IsFresh = item.Count > 0
		return
	}
	item.IsFresh = item.Count > 0 && item.LastOpenMs >= expected
}

func (s *Service) enrichPriceAdjustment(instrument ResearchInstrument, item *DatasetSummary, metadata saasstore.DatasetMetadata) {
	adjustment := metadata.PriceAdjustment
	if adjustment == "" {
		if item.Count == 0 {
			adjustment = currentPriceAdjustment(instrument.DataSource, item.Interval)
		} else {
			adjustment = PriceAdjustmentLegacyUnknown
		}
	}
	label, note := priceAdjustmentText(adjustment)
	item.PriceAdjustment = adjustment
	item.PriceAdjustmentLabel = label
	item.PriceAdjustmentNote = note
	item.NeedsFullReimport = item.Count > 0 && currentPriceAdjustment(instrument.DataSource, item.Interval) != adjustment
	if !metadata.UpdatedAt.IsZero() {
		item.PriceMetadataUpdatedAt = metadata.UpdatedAt.UTC().Format(time.RFC3339)
	}
}

func currentPriceAdjustment(source string, interval string) string {
	if normalizeSource(source) != DataSourceYahoo {
		return PriceAdjustmentRawExchange
	}
	switch normalizeInterval(interval) {
	case "1d", "1w", "1M":
		return PriceAdjustmentYahooAdjusted
	default:
		return PriceAdjustmentYahooIntraday
	}
}

func priceAdjustmentLabel(value string) string {
	label, _ := priceAdjustmentText(value)
	return label
}

func priceAdjustmentText(value string) (string, string) {
	switch value {
	case PriceAdjustmentYahooAdjusted:
		return "Yahoo 調整後價格", "使用 Yahoo adjclose 調整 OHLC，並回推修正大型公司行動斷層。"
	case PriceAdjustmentYahooIntraday:
		return "Yahoo 日內原始價格", "日內資料不做股息或拆分回推，適合短週期觀察。"
	case PriceAdjustmentRawExchange:
		return "交易所原始價格", "使用資料來源提供的原始 K 線價格。"
	default:
		return "舊口徑或未知", "這批資料是在口徑記錄機制建立前匯入，完整重匯後才會標成新版口徑。"
	}
}

func maxInt64(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func normalizeYahooRowsForStorage(req ImportRequest, rows []BinanceKLine, now time.Time) []BinanceKLine {
	if req.Interval != "1d" || len(rows) == 0 {
		return rows
	}
	out := make([]BinanceKLine, 0, len(rows))
	for _, row := range rows {
		if row.OpenTime != marketDailyOpenMs(req.InstrumentID, req.Symbol, row.OpenTime) {
			continue
		}
		if !isCompletedMarketDailyRow(req.InstrumentID, req.Symbol, row.OpenTime, now) {
			continue
		}
		out = append(out, row)
	}
	return out
}

func expectedLatestOpenMs(instrument ResearchInstrument, interval string, now time.Time) int64 {
	interval = normalizeInterval(interval)
	switch interval {
	case "1d":
		return expectedDailyOpenMs(instrument, now)
	case "1w":
		return expectedWeeklyOpenMs(now)
	case "1M":
		return expectedMonthlyOpenMs(now)
	}
	duration, ok := intervalDurations[interval]
	if !ok || duration <= 0 {
		return 0
	}
	return now.Add(-duration).Truncate(duration).UnixMilli()
}

func expectedDailyOpenMs(instrument ResearchInstrument, now time.Time) int64 {
	if instrument.Market == "crypto" || instrument.ID == InstrumentBTCUSDT {
		utc := now.UTC()
		date := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
		if utc.Before(date.Add(24 * time.Hour)) {
			date = date.AddDate(0, 0, -1)
		}
		return date.UnixMilli()
	}
	loc, closeHour, closeMinute := precloseSchedule(instrument.ID)
	localNow := now.In(loc)
	closeAt := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), closeHour, closeMinute, 0, 0, loc)
	target := localNow
	if localNow.Before(closeAt.Add(90 * time.Minute)) {
		target = target.AddDate(0, 0, -1)
	}
	target = previousBusinessDay(target)
	return marketDailyOpenAt(instrument.ID, instrument.Symbol, target).UnixMilli()
}

func previousBusinessDay(value time.Time) time.Time {
	for value.Weekday() == time.Saturday || value.Weekday() == time.Sunday {
		value = value.AddDate(0, 0, -1)
	}
	return value
}

func expectedWeeklyOpenMs(now time.Time) int64 {
	utc := now.UTC()
	weekday := int(utc.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	thisMonday := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(weekday - 1))
	return thisMonday.AddDate(0, 0, -7).UnixMilli()
}

func expectedMonthlyOpenMs(now time.Time) int64 {
	utc := now.UTC()
	thisMonth := time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
	return thisMonth.AddDate(0, -1, 0).UnixMilli()
}

func (s *Service) validateImportRequest(ctx context.Context, req ImportRequest) error {
	if _, ok := intervalDurations[req.Interval]; !ok {
		return ErrUnsupportedInterval
	}
	instrument, err := s.instruments.ResolveInstrument(ctx, req.InstrumentID, req.Symbol, req.DataSource)
	if err != nil {
		return err
	}
	if req.DataSource != instrument.DataSource {
		return ErrUnsupportedSource
	}
	if !instrumentSupportsInterval(instrument, req.Interval) {
		return ErrUnsupportedInterval
	}
	if req.StartTimeMs <= 0 || req.EndTimeMs <= 0 || req.StartTimeMs > req.EndTimeMs {
		return ErrInvalidRange
	}
	return nil
}

func (s *Service) storeKLines(ctx context.Context, instrumentID string, source string, symbol string, interval string, rows []BinanceKLine) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	now := s.now()
	records := make([]saasstore.KLine, 0, len(rows))
	for _, row := range rows {
		records = append(records, saasstore.KLine{
			CreatedAt:    now,
			UpdatedAt:    now,
			InstrumentID: instrumentID,
			Source:       source,
			Symbol:       symbol,
			Interval:     interval,
			OpenTime:     row.OpenTime,
			Open:         row.Open,
			High:         row.High,
			Low:          row.Low,
			Close:        row.Close,
			Volume:       row.Volume,
		})
	}
	tx := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "symbol"}, {Name: "interval"}, {Name: "open_time"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"instrument_id", "source", "open", "high", "low", "close", "volume", "updated_at",
		}),
	}).CreateInBatches(records, 1000)
	return tx.RowsAffected, tx.Error
}

func (s *Service) importPrecloseSnapshots(ctx context.Context, req ImportRequest) (int64, error) {
	instrument, err := s.instruments.ResolveInstrument(ctx, req.InstrumentID, req.Symbol, req.DataSource)
	if err != nil {
		return 0, err
	}
	startTime := req.StartTimeMs
	if req.DataSource == DataSourceYahoo {
		limitStart := s.now().AddDate(0, 0, -59).UnixMilli()
		if startTime < limitStart {
			startTime = limitStart
		}
	}
	var rows []BinanceKLine
	if req.DataSource == DataSourceYahoo {
		rows, err = s.yahooClient.FetchChartRows(ctx, instrument.Symbol, "5m", startTime, req.EndTimeMs)
	} else {
		rows, err = s.fetchBinanceKLines(ctx, instrument.Symbol, "5m", startTime, req.EndTimeMs)
	}
	if err != nil {
		return 0, err
	}
	snapshots, err := buildPrecloseSnapshots(instrument, rows, s.now())
	if err != nil {
		return 0, err
	}
	if len(snapshots) == 0 {
		return 0, nil
	}
	tx := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "instrument_id"},
			{Name: "data_source"},
			{Name: "trade_date_ms"},
			{Name: "snapshot_type"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"symbol", "price", "volume", "observed_at_ms", "updated_at",
		}),
	}).CreateInBatches(snapshots, 1000)
	return tx.RowsAffected, tx.Error
}

func buildPrecloseSnapshots(instrument ResearchInstrument, rows []BinanceKLine, now time.Time) ([]saasstore.DailyExecutionSnapshot, error) {
	loc, closeHour, closeMinute := precloseSchedule(instrument.ID)
	byDate := map[string]BinanceKLine{}
	for _, row := range rows {
		if row.Close <= 0 {
			continue
		}
		local := time.UnixMilli(row.OpenTime).In(loc)
		target := time.Date(local.Year(), local.Month(), local.Day(), closeHour, closeMinute, 0, 0, loc).Add(-10 * time.Minute)
		if local.After(target.Add(15*time.Minute)) || local.Before(target.Add(-20*time.Minute)) {
			continue
		}
		key := local.Format("2006-01-02")
		current, ok := byDate[key]
		if !ok || absDuration(time.UnixMilli(row.OpenTime).In(loc).Sub(target)) < absDuration(time.UnixMilli(current.OpenTime).In(loc).Sub(target)) {
			byDate[key] = row
		}
	}
	out := make([]saasstore.DailyExecutionSnapshot, 0, len(byDate))
	for _, row := range byDate {
		local := time.UnixMilli(row.OpenTime).In(loc)
		tradeDate := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).UTC()
		out = append(out, saasstore.DailyExecutionSnapshot{
			CreatedAt:    now,
			UpdatedAt:    now,
			InstrumentID: instrument.ID,
			DataSource:   instrument.DataSource,
			Symbol:       instrument.Symbol,
			TradeDateMs:  tradeDate.UnixMilli(),
			SnapshotType: ExecutionModePreclose10m,
			Price:        row.Close,
			Volume:       row.Volume,
			ObservedAtMs: row.OpenTime,
		})
	}
	return out, nil
}

func marketDailyOpenMs(instrumentID string, symbol string, openTimeMs int64) int64 {
	return marketDailyOpenAt(instrumentID, symbol, time.UnixMilli(openTimeMs)).UnixMilli()
}

func marketDailyOpenAt(instrumentID string, symbol string, value time.Time) time.Time {
	switch instrumentID {
	case InstrumentBTCUSDT:
		utc := value.UTC()
		return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	}
	if isTaiwanInstrument(instrumentID, symbol) {
		loc, err := time.LoadLocation("Asia/Taipei")
		if err != nil {
			loc = time.FixedZone("Asia/Taipei", 8*3600)
		}
		local := value.In(loc)
		return time.Date(local.Year(), local.Month(), local.Day(), 9, 0, 0, 0, loc).UTC()
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.FixedZone("America/New_York", -5*3600)
	}
	local := value.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 9, 30, 0, 0, loc).UTC()
}

func isCompletedMarketDailyRow(instrumentID string, symbol string, openTimeMs int64, now time.Time) bool {
	if now.IsZero() {
		return true
	}
	readyAt := marketDailyCloseAt(instrumentID, symbol, time.UnixMilli(openTimeMs)).Add(yahooDailyReadyDelay)
	return !readyAt.After(now.UTC())
}

func marketDailyCloseAt(instrumentID string, symbol string, value time.Time) time.Time {
	switch instrumentID {
	case InstrumentBTCUSDT:
		openAt := marketDailyOpenAt(instrumentID, symbol, value)
		return openAt.Add(24 * time.Hour)
	}
	if isTaiwanInstrument(instrumentID, symbol) {
		loc, err := time.LoadLocation("Asia/Taipei")
		if err != nil {
			loc = time.FixedZone("Asia/Taipei", 8*3600)
		}
		local := value.In(loc)
		return time.Date(local.Year(), local.Month(), local.Day(), 13, 30, 0, 0, loc).UTC()
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.FixedZone("America/New_York", -5*3600)
	}
	local := value.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 16, 0, 0, 0, loc).UTC()
}

func isTaiwanInstrument(instrumentID string, symbol string) bool {
	id := strings.ToUpper(strings.TrimSpace(instrumentID))
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	return id == "TWII" || strings.HasSuffix(id, ".TW") || strings.HasSuffix(sym, ".TW")
}

func precloseSchedule(instrumentID string) (*time.Location, int, int) {
	switch instrumentID {
	case InstrumentBTCUSDT:
		return time.UTC, 0, 0
	}
	if isTaiwanInstrument(instrumentID, "") {
		loc, err := time.LoadLocation("Asia/Taipei")
		if err != nil {
			return time.FixedZone("Asia/Taipei", 8*3600), 13, 30
		}
		return loc, 13, 30
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.FixedZone("America/New_York", -5*3600), 16, 0
	}
	return loc, 16, 0
}

func (s *Service) fetchBinanceKLines(ctx context.Context, symbol string, interval string, startTimeMs int64, endTimeMs int64) ([]BinanceKLine, error) {
	step := intervalDurations[interval].Milliseconds()
	if step <= 0 {
		return nil, ErrUnsupportedInterval
	}
	out := make([]BinanceKLine, 0)
	cursor := startTimeMs
	for cursor <= endTimeMs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, err := s.client.FetchKLines(ctx, symbol, interval, cursor, endTimeMs, 1000)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		out = append(out, rows...)
		next := rows[len(rows)-1].OpenTime + step
		if next <= cursor || len(rows) < 1000 {
			break
		}
		cursor = next
	}
	return out, nil
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func instrumentSupportsInterval(instrument ResearchInstrument, interval string) bool {
	for _, supported := range instrument.SupportedIntervals {
		if supported == interval {
			return true
		}
	}
	return false
}

func (c *Client) FetchKLines(ctx context.Context, symbol string, interval string, startTime int64, endTime int64, limit int) ([]BinanceKLine, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	endpoint, err := url.Parse(c.BaseURL + "/api/v3/klines")
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("symbol", symbol)
	query.Set("interval", interval)
	query.Set("startTime", strconv.FormatInt(startTime, 10))
	query.Set("endTime", strconv.FormatInt(endTime, 10))
	query.Set("limit", strconv.Itoa(limit))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("binance klines status %d", resp.StatusCode)
	}
	var raw [][]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	rows := make([]BinanceKLine, 0, len(raw))
	for _, item := range raw {
		row, err := parseKLineRow(item)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (c *YahooClient) FetchKLines(ctx context.Context, symbol string, interval string, startTime int64, endTime int64) ([]BinanceKLine, error) {
	switch interval {
	case "1d", "1h", "1m", "1w", "1M":
	default:
		return nil, ErrUnsupportedInterval
	}
	switch normalizeInterval(interval) {
	case "1w", "1M":
		rows, err := c.FetchChartRows(ctx, symbol, "1d", startTime, endTime)
		if err != nil {
			return nil, err
		}
		return aggregateYahooDailyRows(symbol, rows, interval, endTime), nil
	}
	return c.FetchChartRows(ctx, symbol, interval, startTime, endTime)
}

func (c *YahooClient) FetchChartRows(ctx context.Context, symbol string, interval string, startTime int64, endTime int64) ([]BinanceKLine, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, time.Duration(attempt)*1500*time.Millisecond); err != nil {
				return nil, err
			}
		}
		for _, baseURL := range c.chartBaseURLs() {
			rows, retryable, err := c.fetchChart(ctx, baseURL, symbol, interval, startTime, endTime)
			if err == nil {
				return rows, nil
			}
			lastErr = err
			if !retryable {
				return nil, err
			}
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("yahoo chart request failed")
}

func (c *YahooClient) fetchChart(ctx context.Context, baseURL string, symbol string, interval string, startTime int64, endTime int64) ([]BinanceKLine, bool, error) {
	if err := c.waitTurn(ctx); err != nil {
		return nil, false, err
	}
	endpoint, err := url.Parse(strings.TrimRight(baseURL, "/") + "/v8/finance/chart/" + url.PathEscape(symbol))
	if err != nil {
		return nil, false, err
	}
	query := endpoint.Query()
	query.Set("period1", strconv.FormatInt(startTime/1000, 10))
	query.Set("period2", strconv.FormatInt(endTime/1000, 10))
	query.Set("interval", yahooChartInterval(interval))
	query.Set("events", "history")
	query.Set("includeAdjustedClose", "true")
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", yahooUserAgent)
	req.Header.Set("Accept", "application/json,text/plain,*/*")
	req.Header.Set("Accept-Language", "zh-TW,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Referer", "https://finance.yahoo.com/")
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, isRetryableYahooStatus(resp.StatusCode), fmt.Errorf("yahoo chart status %d", resp.StatusCode)
	}
	var raw yahooChartResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, false, err
	}
	if raw.Chart.Error != nil {
		return nil, false, fmt.Errorf("yahoo chart error: %s", raw.Chart.Error.Description)
	}
	if len(raw.Chart.Result) == 0 {
		return nil, false, nil
	}
	result := raw.Chart.Result[0]
	if len(result.Indicators.Quote) == 0 {
		return nil, false, nil
	}
	quote := result.Indicators.Quote[0]
	var adjustedClose []*float64
	if len(result.Indicators.AdjClose) > 0 {
		adjustedClose = result.Indicators.AdjClose[0].AdjClose
	}
	rows := make([]BinanceKLine, 0, len(result.Timestamp))
	for i, ts := range result.Timestamp {
		open, ok := yahooFloatAt(quote.Open, i)
		if !ok {
			continue
		}
		high, ok := yahooFloatAt(quote.High, i)
		if !ok {
			continue
		}
		low, ok := yahooFloatAt(quote.Low, i)
		if !ok {
			continue
		}
		closePrice, ok := yahooFloatAt(quote.Close, i)
		if !ok {
			continue
		}
		if adjClose, ok := yahooFloatAt(adjustedClose, i); ok && closePrice != 0 {
			factor := adjClose / closePrice
			open *= factor
			high *= factor
			low *= factor
			closePrice = adjClose
		}
		open, high, low, closePrice = repairYahooOHLC(open, high, low, closePrice)
		volume := 0.0
		if i < len(quote.Volume) && quote.Volume[i] != nil {
			volume = float64(*quote.Volume[i])
		}
		rows = append(rows, BinanceKLine{
			OpenTime: ts * 1000,
			Open:     open,
			High:     high,
			Low:      low,
			Close:    closePrice,
			Volume:   volume,
		})
	}
	if shouldBackAdjustYahooDiscontinuities(interval) {
		backAdjustLargeYahooDiscontinuities(rows)
	}
	return rows, false, nil
}

func (c *YahooClient) chartBaseURLs() []string {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultYahooBaseURL
	}
	out := []string{base}
	if strings.Contains(base, "query1.finance.yahoo.com") {
		out = append(out, strings.Replace(base, "query1.finance.yahoo.com", "query2.finance.yahoo.com", 1))
	}
	return uniqueStrings(out)
}

func (c *YahooClient) waitTurn(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.lastAt.IsZero() {
		wait := c.lastAt.Add(yahooMinRequestInterval).Sub(time.Now())
		if wait > 0 {
			if err := sleepWithContext(ctx, wait); err != nil {
				return err
			}
		}
	}
	c.lastAt = time.Now()
	return nil
}

func isRetryableYahooStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func repairYahooOHLC(open float64, high float64, low float64, closePrice float64) (float64, float64, float64, float64) {
	if closePrice <= 0 {
		return open, high, low, closePrice
	}
	if open <= 0 {
		open = closePrice
	}
	if high <= 0 {
		high = math.Max(open, closePrice)
	}
	if low <= 0 {
		low = math.Min(open, closePrice)
	}
	high = math.Max(high, math.Max(open, closePrice))
	low = math.Min(low, math.Min(open, closePrice))
	return open, high, low, closePrice
}

func aggregateYahooDailyRows(symbol string, rows []BinanceKLine, interval string, endTimeMs int64) []BinanceKLine {
	rows = filterYahooRegularDailyRows(symbol, rows)
	if len(rows) == 0 {
		return nil
	}
	interval = normalizeInterval(interval)
	sort.Slice(rows, func(i, j int) bool { return rows[i].OpenTime < rows[j].OpenTime })
	out := make([]BinanceKLine, 0)
	currentKey := ""
	var currentEndMs int64
	var current BinanceKLine
	for _, row := range rows {
		period, ok := aggregateYahooPeriod(symbol, row.OpenTime, interval)
		if !ok {
			continue
		}
		if currentKey == "" || period.Key != currentKey {
			if currentKey != "" {
				if isCompletedAggregatePeriod(currentEndMs, endTimeMs) {
					out = append(out, current)
				}
			}
			currentKey = period.Key
			currentEndMs = period.EndMs
			current = row
			current.OpenTime = period.StartMs
			continue
		}
		current.High = math.Max(current.High, row.High)
		current.Low = math.Min(current.Low, row.Low)
		current.Close = row.Close
		current.Volume += row.Volume
	}
	if currentKey != "" {
		if isCompletedAggregatePeriod(currentEndMs, endTimeMs) {
			out = append(out, current)
		}
	}
	return out
}

func aggregateRowsForInstrument(instrument ResearchInstrument, rows []BinanceKLine, interval string, endTimeMs int64) []BinanceKLine {
	if len(rows) == 0 {
		return nil
	}
	interval = normalizeInterval(interval)
	sort.Slice(rows, func(i, j int) bool { return rows[i].OpenTime < rows[j].OpenTime })
	out := make([]BinanceKLine, 0)
	currentKey := ""
	var currentEndMs int64
	var current BinanceKLine
	for _, row := range rows {
		if !isCanonicalKLineOpenTime(instrument, "1d", row.OpenTime) {
			continue
		}
		period, ok := aggregatePeriodForInstrument(instrument, row.OpenTime, interval)
		if !ok {
			continue
		}
		if currentKey == "" || period.Key != currentKey {
			if currentKey != "" && isCompletedAggregatePeriod(currentEndMs, endTimeMs) {
				out = append(out, current)
			}
			currentKey = period.Key
			currentEndMs = period.EndMs
			current = row
			current.OpenTime = period.StartMs
			continue
		}
		current.High = math.Max(current.High, row.High)
		current.Low = math.Min(current.Low, row.Low)
		current.Close = row.Close
		current.Volume += row.Volume
	}
	if currentKey != "" && isCompletedAggregatePeriod(currentEndMs, endTimeMs) {
		out = append(out, current)
	}
	return out
}

func isCompletedAggregatePeriod(periodEndMs int64, endTimeMs int64) bool {
	if periodEndMs <= 0 || endTimeMs <= 0 {
		return true
	}
	return periodEndMs <= endTimeMs
}

func filterYahooRegularDailyRows(symbol string, rows []BinanceKLine) []BinanceKLine {
	out := make([]BinanceKLine, 0, len(rows))
	for _, row := range rows {
		if row.OpenTime != marketDailyOpenMs("", symbol, row.OpenTime) {
			continue
		}
		out = append(out, row)
	}
	return out
}

type yahooAggregatePeriod struct {
	Key     string
	StartMs int64
	EndMs   int64
}

func aggregateYahooPeriod(symbol string, openTimeMs int64, interval string) (yahooAggregatePeriod, bool) {
	loc := marketLocationForSymbol(symbol)
	local := time.UnixMilli(openTimeMs).In(loc)
	switch interval {
	case "1w":
		offset := (int(local.Weekday()) + 6) % 7
		start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -offset)
		end := start.AddDate(0, 0, 7)
		return yahooAggregatePeriod{Key: start.Format("2006-01-02"), StartMs: start.UnixMilli(), EndMs: end.UnixMilli()}, true
	case "1M":
		start := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
		end := start.AddDate(0, 1, 0)
		return yahooAggregatePeriod{Key: start.Format("2006-01"), StartMs: start.UnixMilli(), EndMs: end.UnixMilli()}, true
	default:
		return yahooAggregatePeriod{}, false
	}
}

func aggregatePeriodForInstrument(instrument ResearchInstrument, openTimeMs int64, interval string) (yahooAggregatePeriod, bool) {
	loc := marketLocationForInstrument(instrument)
	local := time.UnixMilli(openTimeMs).In(loc)
	switch normalizeInterval(interval) {
	case "1w":
		offset := (int(local.Weekday()) + 6) % 7
		start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -offset)
		end := start.AddDate(0, 0, 7)
		return yahooAggregatePeriod{Key: start.Format("2006-01-02"), StartMs: start.UnixMilli(), EndMs: end.UnixMilli()}, true
	case "1M":
		start := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
		end := start.AddDate(0, 1, 0)
		return yahooAggregatePeriod{Key: start.Format("2006-01"), StartMs: start.UnixMilli(), EndMs: end.UnixMilli()}, true
	default:
		return yahooAggregatePeriod{}, false
	}
}

func isCanonicalKLineOpenTime(instrument ResearchInstrument, interval string, openTimeMs int64) bool {
	if openTimeMs <= 0 {
		return false
	}
	switch normalizeInterval(interval) {
	case "1d":
		return openTimeMs == marketDailyOpenMs(instrument.ID, instrument.Symbol, openTimeMs)
	case "1w", "1M":
		period, ok := aggregatePeriodForInstrument(instrument, openTimeMs, interval)
		return ok && openTimeMs == period.StartMs
	default:
		return true
	}
}

func kLineRowsToBars(rows []saasstore.KLine) []BinanceKLine {
	out := make([]BinanceKLine, 0, len(rows))
	for _, row := range rows {
		out = append(out, BinanceKLine{
			OpenTime: row.OpenTime,
			Open:     row.Open,
			High:     row.High,
			Low:      row.Low,
			Close:    row.Close,
			Volume:   row.Volume,
		})
	}
	return out
}

func marketLocationForSymbol(symbol string) *time.Location {
	if isTaiwanInstrument("", symbol) {
		loc, err := time.LoadLocation("Asia/Taipei")
		if err == nil {
			return loc
		}
		return time.FixedZone("Asia/Taipei", 8*3600)
	}
	loc, err := time.LoadLocation("America/New_York")
	if err == nil {
		return loc
	}
	return time.FixedZone("America/New_York", -5*3600)
}

func marketLocationForInstrument(instrument ResearchInstrument) *time.Location {
	if instrument.Market == "crypto" || instrument.DataSource == DataSourceBinance || instrument.ID == InstrumentBTCUSDT {
		return time.UTC
	}
	return marketLocationForSymbol(instrument.Symbol)
}

func yahooChartInterval(interval string) string {
	switch normalizeInterval(interval) {
	case "1w":
		return "1wk"
	case "1M":
		return "1mo"
	default:
		return normalizeInterval(interval)
	}
}

func shouldBackAdjustYahooDiscontinuities(interval string) bool {
	switch normalizeInterval(interval) {
	case "1d", "1w", "1M":
		return true
	default:
		return false
	}
}

func backAdjustLargeYahooDiscontinuities(rows []BinanceKLine) {
	for i := 1; i < len(rows); i++ {
		prevClose := rows[i-1].Close
		currentClose := rows[i].Close
		if prevClose <= 0 || currentClose <= 0 {
			continue
		}
		ratio := currentClose / prevClose
		if ratio >= 0.35 && ratio <= 2.85 {
			continue
		}
		for j := 0; j < i; j++ {
			rows[j].Open *= ratio
			rows[j].High *= ratio
			rows[j].Low *= ratio
			rows[j].Close *= ratio
		}
	}
}

type yahooChartResponse struct {
	Chart struct {
		Result []struct {
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open   []*float64 `json:"open"`
					High   []*float64 `json:"high"`
					Low    []*float64 `json:"low"`
					Close  []*float64 `json:"close"`
					Volume []*int64   `json:"volume"`
				} `json:"quote"`
				AdjClose []struct {
					AdjClose []*float64 `json:"adjclose"`
				} `json:"adjclose"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct {
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

func yahooFloatAt(values []*float64, index int) (float64, bool) {
	if index >= len(values) || values[index] == nil {
		return 0, false
	}
	return *values[index], true
}

func parseKLineRow(item []any) (BinanceKLine, error) {
	if len(item) < 6 {
		return BinanceKLine{}, errors.New("binance kline row too short")
	}
	openTime, err := numberToInt64(item[0])
	if err != nil {
		return BinanceKLine{}, err
	}
	open, err := stringNumberToFloat(item[1])
	if err != nil {
		return BinanceKLine{}, err
	}
	high, err := stringNumberToFloat(item[2])
	if err != nil {
		return BinanceKLine{}, err
	}
	low, err := stringNumberToFloat(item[3])
	if err != nil {
		return BinanceKLine{}, err
	}
	closePrice, err := stringNumberToFloat(item[4])
	if err != nil {
		return BinanceKLine{}, err
	}
	volume, err := stringNumberToFloat(item[5])
	if err != nil {
		return BinanceKLine{}, err
	}
	return BinanceKLine{
		OpenTime: openTime,
		Open:     open,
		High:     high,
		Low:      low,
		Close:    closePrice,
		Volume:   volume,
	}, nil
}

func normalizeSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

func normalizeInterval(interval string) string {
	return strings.TrimSpace(interval)
}

func numberToInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, fmt.Errorf("unexpected integer value %T", value)
	}
}

func stringNumberToFloat(value any) (float64, error) {
	switch typed := value.(type) {
	case string:
		return strconv.ParseFloat(typed, 64)
	case float64:
		return typed, nil
	default:
		return 0, fmt.Errorf("unexpected decimal value %T", value)
	}
}
