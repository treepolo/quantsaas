package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

	yahooUserAgent          = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0 Safari/537.36 QuantSaaS/0.1"
	yahooMinRequestInterval = 1200 * time.Millisecond
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
}

type AutoUpdateResult struct {
	InstrumentID string `json:"instrument_id"`
	DataSource   string `json:"data_source"`
	Symbol       string `json:"symbol"`
	Interval     string `json:"interval"`
	StoredBars   int64  `json:"stored_bars"`
	Error        string `json:"error,omitempty"`
}

type DatasetSummary struct {
	InstrumentID          string `json:"instrument_id"`
	DataSource            string `json:"data_source"`
	Symbol                string `json:"symbol"`
	Market                string `json:"market"`
	Interval              string `json:"interval"`
	Count                 int64  `json:"count"`
	PrecloseSnapshotCount int64  `json:"preclose_snapshot_count"`
	FirstPrecloseMs       int64  `json:"first_preclose_ms,omitempty"`
	LastPrecloseMs        int64  `json:"last_preclose_ms,omitempty"`
	FirstOpenMs           int64  `json:"first_open_ms,omitempty"`
	LastOpenMs            int64  `json:"last_open_ms,omitempty"`
	ExpectedLatestOpenMs  int64  `json:"expected_latest_open_ms,omitempty"`
	IsFresh               bool   `json:"is_fresh"`
	UpdatedAt             string `json:"updated_at,omitempty"`
}

func NewService(db *gorm.DB, client *Client) *Service {
	if client == nil {
		client = NewClient(DefaultBaseURL)
	}
	return &Service{db: db, client: client, yahooClient: NewYahooClient(DefaultYahooBaseURL), instruments: NewInstrumentStore(db), now: func() time.Time { return time.Now().UTC() }}
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
	return s.instruments.Upsert(ctx, req)
}

func (s *Service) DisableInstrument(ctx context.Context, id string) error {
	return s.instruments.Disable(ctx, id)
}

func (s *Service) Import(ctx context.Context, req ImportRequest) (ImportResult, error) {
	req = s.normalizeImportRequest(req)
	if err := s.validateImportRequest(ctx, req); err != nil {
		return ImportResult{}, err
	}

	result := ImportResult{
		InstrumentID: req.InstrumentID,
		DataSource:   req.DataSource,
		Symbol:       req.Symbol,
		Interval:     req.Interval,
		StartTimeMs:  req.StartTimeMs,
		EndTimeMs:    req.EndTimeMs,
	}
	if req.DataSource == DataSourceYahoo {
		rows, err := s.yahooClient.FetchKLines(ctx, req.Symbol, req.Interval, req.StartTimeMs, req.EndTimeMs)
		if err != nil {
			return ImportResult{}, err
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
	for _, instrument := range instruments {
		var instrumentErr string
		for _, interval := range instrument.SupportedIntervals {
			req := ImportRequest{
				InstrumentID: instrument.ID,
				DataSource:   instrument.DataSource,
				Symbol:       instrument.Symbol,
				Interval:     interval,
				StartTimeMs:  s.latestUpdateStart(instrument, interval),
				EndTimeMs:    s.now().UnixMilli(),
			}
			item := AutoUpdateResult{InstrumentID: instrument.ID, DataSource: instrument.DataSource, Symbol: instrument.Symbol, Interval: interval}
			if req.StartTimeMs > req.EndTimeMs {
				results = append(results, item)
				continue
			}
			imported, err := s.Import(ctx, req)
			if err != nil {
				item.Error = err.Error()
				instrumentErr = err.Error()
			} else {
				item.StoredBars = imported.StoredBars
			}
			results = append(results, item)
		}
		s.recordAutoUpdate(ctx, instrument.ID, instrumentErr)
	}
	return results, nil
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

func (s *Service) latestUpdateStart(instrument ResearchInstrument, interval string) int64 {
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
		if d, ok := intervalDurations[interval]; ok {
			return *latest.LastOpenMs + d.Milliseconds()
		}
	}
	now := s.now()
	switch interval {
	case "1s":
		return now.Add(-24 * time.Hour).UnixMilli()
	case "1m":
		return now.AddDate(0, 0, -7).UnixMilli()
	case "5m", "15m", "30m":
		return now.AddDate(0, 0, -14).UnixMilli()
	case "1h":
		return now.AddDate(0, -2, 0).UnixMilli()
	case "1w":
		return now.AddDate(-2, 0, 0).UnixMilli()
	case "1M":
		return now.AddDate(-5, 0, 0).UnixMilli()
	default:
		return now.AddDate(0, 0, -45).UnixMilli()
	}
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
	return time.Date(target.Year(), target.Month(), target.Day(), 0, 0, 0, 0, loc).UTC().UnixMilli()
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

func precloseSchedule(instrumentID string) (*time.Location, int, int) {
	switch instrumentID {
	case InstrumentBTCUSDT:
		return time.UTC, 0, 0
	case "TWII":
		loc, err := time.LoadLocation("Asia/Taipei")
		if err != nil {
			return time.FixedZone("Asia/Taipei", 8*3600), 13, 30
		}
		return loc, 13, 30
	default:
		loc, err := time.LoadLocation("America/New_York")
		if err != nil {
			return time.FixedZone("America/New_York", -5*3600), 16, 0
		}
		return loc, 16, 0
	}
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
