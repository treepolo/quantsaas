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
	"time"

	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	DefaultBaseURL = "https://api.binance.com"
	DefaultSymbol  = "BTCUSDT"
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

type BinanceKLine struct {
	OpenTime int64
	Open     float64
	High     float64
	Low      float64
	Close    float64
	Volume   float64
}

type Service struct {
	db     *gorm.DB
	client *Client
	now    func() time.Time
}

type ImportRequest struct {
	Symbol      string `json:"symbol"`
	Interval    string `json:"interval"`
	StartTimeMs int64  `json:"start_time_ms"`
	EndTimeMs   int64  `json:"end_time_ms"`
}

type ImportResult struct {
	Symbol      string `json:"symbol"`
	Interval    string `json:"interval"`
	StartTimeMs int64  `json:"start_time_ms"`
	EndTimeMs   int64  `json:"end_time_ms"`
	FetchedBars int    `json:"fetched_bars"`
	StoredBars  int64  `json:"stored_bars"`
	FirstOpenMs int64  `json:"first_open_ms,omitempty"`
	LastOpenMs  int64  `json:"last_open_ms,omitempty"`
}

type DatasetSummary struct {
	Symbol      string `json:"symbol"`
	Interval    string `json:"interval"`
	Count       int64  `json:"count"`
	FirstOpenMs int64  `json:"first_open_ms,omitempty"`
	LastOpenMs  int64  `json:"last_open_ms,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

func NewService(db *gorm.DB, client *Client) *Service {
	if client == nil {
		client = NewClient(DefaultBaseURL)
	}
	return &Service{db: db, client: client, now: func() time.Time { return time.Now().UTC() }}
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

func SupportedIntervals() []string {
	return []string{"1d", "1h", "15m", "5m", "1m", "1s"}
}

func (s *Service) Import(ctx context.Context, req ImportRequest) (ImportResult, error) {
	req = s.normalizeImportRequest(req)
	if err := validateImportRequest(req); err != nil {
		return ImportResult{}, err
	}

	result := ImportResult{
		Symbol:      req.Symbol,
		Interval:    req.Interval,
		StartTimeMs: req.StartTimeMs,
		EndTimeMs:   req.EndTimeMs,
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
		stored, err := s.storeKLines(ctx, req.Symbol, req.Interval, rows)
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

	return result, nil
}

func (s *Service) Summaries(ctx context.Context, symbol string, intervals []string) ([]DatasetSummary, error) {
	symbol = normalizeSymbol(symbol)
	if symbol == "" {
		symbol = DefaultSymbol
	}
	if len(intervals) == 0 {
		intervals = SupportedIntervals()
	}
	for i, interval := range intervals {
		intervals[i] = normalizeInterval(interval)
	}

	rows := make([]DatasetSummary, 0, len(intervals))
	for _, interval := range intervals {
		if _, ok := intervalDurations[interval]; !ok {
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
			Where("symbol = ? AND interval = ?", symbol, interval).
			Scan(&summary).Error; err != nil {
			return nil, err
		}
		item := DatasetSummary{Symbol: symbol, Interval: interval, Count: summary.Count}
		if summary.FirstOpenMs != nil {
			item.FirstOpenMs = *summary.FirstOpenMs
		}
		if summary.LastOpenMs != nil {
			item.LastOpenMs = *summary.LastOpenMs
		}
		if summary.UpdatedAt != nil {
			item.UpdatedAt = summary.UpdatedAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, item)
	}
	return rows, nil
}

func (s *Service) normalizeImportRequest(req ImportRequest) ImportRequest {
	req.Symbol = normalizeSymbol(req.Symbol)
	if req.Symbol == "" {
		req.Symbol = DefaultSymbol
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
	_ = s.db.Model(&saasstore.KLine{}).
		Select("max(open_time) as last_open_ms").
		Where("symbol = ? AND interval = ?", symbol, interval).
		Scan(&latest).Error
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

func validateImportRequest(req ImportRequest) error {
	if _, ok := intervalDurations[req.Interval]; !ok {
		return ErrUnsupportedInterval
	}
	if req.StartTimeMs <= 0 || req.EndTimeMs <= 0 || req.StartTimeMs > req.EndTimeMs {
		return ErrInvalidRange
	}
	return nil
}

func (s *Service) storeKLines(ctx context.Context, symbol string, interval string, rows []BinanceKLine) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	now := s.now()
	records := make([]saasstore.KLine, 0, len(rows))
	for _, row := range rows {
		records = append(records, saasstore.KLine{
			CreatedAt: now,
			UpdatedAt: now,
			Symbol:    symbol,
			Interval:  interval,
			OpenTime:  row.OpenTime,
			Open:      row.Open,
			High:      row.High,
			Low:       row.Low,
			Close:     row.Close,
			Volume:    row.Volume,
		})
	}
	tx := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "symbol"}, {Name: "interval"}, {Name: "open_time"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"open", "high", "low", "close", "volume", "updated_at",
		}),
	}).CreateInBatches(records, 1000)
	return tx.RowsAffected, tx.Error
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
