package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultFredBaseURL = "https://api.stlouisfed.org"
	fredDateLayout     = "2006-01-02"
	fredVintageChunk   = 500
)

type FredClient struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

type fredObservationResponse struct {
	Observations []fredObservation `json:"observations"`
	ErrorCode    int               `json:"error_code,omitempty"`
	ErrorMessage string            `json:"error_message,omitempty"`
}

type fredObservation struct {
	RealtimeStart string `json:"realtime_start"`
	RealtimeEnd   string `json:"realtime_end"`
	Date          string `json:"date"`
	Value         string `json:"value"`
	Extra         map[string]string
}

type fredVintageDatesResponse struct {
	VintageDates []string `json:"vintage_dates"`
	Count        int      `json:"count"`
	Limit        int      `json:"limit"`
	Offset       int      `json:"offset"`
	ErrorCode    int      `json:"error_code,omitempty"`
	ErrorMessage string   `json:"error_message,omitempty"`
}

type FredObservationRow struct {
	Bar               BinanceKLine
	ObservationTimeMs int64
	RealtimeStartMs   int64
	RealtimeEndMs     int64
	AvailableAtMs     int64
}

func NewFredClient(baseURL string, apiKey string) *FredClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultFredBaseURL
	}
	if strings.TrimSpace(apiKey) == "" {
		apiKey = os.Getenv("FRED_API_KEY")
	}
	return &FredClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  strings.TrimSpace(apiKey),
		HTTPClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (c *FredClient) FetchObservations(ctx context.Context, seriesID string, startTimeMs int64, endTimeMs int64) ([]FredObservationRow, error) {
	seriesID = normalizeSymbol(seriesID)
	if seriesID == "" {
		return nil, ErrUnsupportedInstrument
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, fmt.Errorf("FRED_API_KEY is required")
	}
	vintageDates, err := c.fetchVintageDates(ctx, seriesID, startTimeMs)
	if err != nil {
		return nil, err
	}
	if len(vintageDates) == 0 {
		return nil, nil
	}
	byObservation := map[int64]FredObservationRow{}
	for start := 0; start < len(vintageDates); start += fredVintageChunk {
		end := start + fredVintageChunk
		if end > len(vintageDates) {
			end = len(vintageDates)
		}
		rows, err := c.fetchObservationVintages(ctx, seriesID, vintageDates[start:end], startTimeMs, endTimeMs)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			existing, ok := byObservation[row.ObservationTimeMs]
			if !ok || row.RealtimeStartMs < existing.RealtimeStartMs {
				byObservation[row.ObservationTimeMs] = row
			}
		}
	}
	rows := make([]FredObservationRow, 0, len(byObservation))
	for _, row := range byObservation {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Bar.OpenTime < rows[j].Bar.OpenTime })
	return rows, nil
}

func (c *FredClient) fetchVintageDates(ctx context.Context, seriesID string, startTimeMs int64) ([]string, error) {
	out := []string{}
	offset := 0
	for {
		endpoint, err := url.Parse(c.BaseURL + "/fred/series/vintagedates")
		if err != nil {
			return nil, err
		}
		query := endpoint.Query()
		query.Set("series_id", seriesID)
		query.Set("api_key", c.APIKey)
		query.Set("file_type", "json")
		query.Set("sort_order", "asc")
		query.Set("limit", "1000")
		query.Set("offset", strconv.Itoa(offset))
		if startTimeMs > 0 {
			query.Set("realtime_start", time.UnixMilli(startTimeMs).UTC().Format(fredDateLayout))
		}
		endpoint.RawQuery = query.Encode()

		var payload fredVintageDatesResponse
		if err := c.getJSON(ctx, endpoint.String(), &payload, "fred vintagedates"); err != nil {
			return nil, err
		}
		if payload.ErrorCode != 0 {
			return nil, fmt.Errorf("fred error %d: %s", payload.ErrorCode, payload.ErrorMessage)
		}
		out = append(out, payload.VintageDates...)
		offset += len(payload.VintageDates)
		if len(payload.VintageDates) == 0 || offset >= payload.Count {
			break
		}
	}
	return out, nil
}

func (c *FredClient) fetchObservationVintages(ctx context.Context, seriesID string, vintageDates []string, startTimeMs int64, endTimeMs int64) ([]FredObservationRow, error) {
	if len(vintageDates) == 0 {
		return nil, nil
	}
	endpoint, err := url.Parse(c.BaseURL + "/fred/series/observations")
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("series_id", seriesID)
	query.Set("api_key", c.APIKey)
	query.Set("file_type", "json")
	query.Set("sort_order", "asc")
	query.Set("limit", "100000")
	query.Set("output_type", "3")
	query.Set("vintage_dates", strings.Join(vintageDates, ","))
	if startTimeMs > 0 {
		query.Set("observation_start", time.UnixMilli(startTimeMs).UTC().Format(fredDateLayout))
	}
	if endTimeMs > 0 {
		query.Set("observation_end", time.UnixMilli(endTimeMs).UTC().Format(fredDateLayout))
	}
	endpoint.RawQuery = query.Encode()

	var payload fredObservationResponse
	if err := c.getJSON(ctx, endpoint.String(), &payload, "fred observations"); err != nil {
		return nil, err
	}
	if payload.ErrorCode != 0 {
		return nil, fmt.Errorf("fred error %d: %s", payload.ErrorCode, payload.ErrorMessage)
	}
	rows := make([]FredObservationRow, 0, len(payload.Observations))
	for _, item := range payload.Observations {
		row, ok := fredInitialObservationRow(seriesID, item, vintageDates)
		if ok {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (c *FredClient) getJSON(ctx context.Context, endpoint string, out any, label string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if len(strings.TrimSpace(string(body))) > 0 {
			return fmt.Errorf("%s status %d: %s", label, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("%s status %d", label, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func fredInitialObservationRow(seriesID string, item fredObservation, vintageDates []string) (FredObservationRow, bool) {
	date, err := time.Parse(fredDateLayout, item.Date)
	if err != nil {
		return FredObservationRow{}, false
	}
	valueText := ""
	vintageText := ""
	for _, vintage := range vintageDates {
		key := fredVintageValueKey(seriesID, vintage)
		if value, ok := item.Extra[key]; ok {
			valueText = value
			vintageText = vintage
			break
		}
		if value, ok := findFredVintageValueBySuffix(item.Extra, vintage); ok {
			valueText = value
			vintageText = vintage
			break
		}
	}
	if vintageText == "" {
		return FredObservationRow{}, false
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(valueText), 64)
	if err != nil {
		return FredObservationRow{}, false
	}
	realtimeStart, err := time.Parse(fredDateLayout, vintageText)
	if err != nil {
		return FredObservationRow{}, false
	}
	observationTimeMs := date.UTC().UnixMilli()
	realtimeStartMs := realtimeStart.UTC().UnixMilli()
	return FredObservationRow{
		Bar: BinanceKLine{
			OpenTime: observationTimeMs,
			Open:     value,
			High:     value,
			Low:      value,
			Close:    value,
			Volume:   0,
		},
		ObservationTimeMs: observationTimeMs,
		RealtimeStartMs:   realtimeStartMs,
		RealtimeEndMs:     realtimeStartMs,
		AvailableAtMs:     realtimeStart.UTC().AddDate(0, 0, 1).UnixMilli(),
	}, true
}

func fredVintageValueKey(seriesID string, vintage string) string {
	return seriesID + "_" + strings.ReplaceAll(vintage, "-", "")
}

func findFredVintageValueBySuffix(values map[string]string, vintage string) (string, bool) {
	suffix := "_" + strings.ReplaceAll(vintage, "-", "")
	for key, value := range values {
		if strings.HasSuffix(key, suffix) {
			return value, true
		}
	}
	return "", false
}

func (o *fredObservation) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	type baseObservation struct {
		RealtimeStart string `json:"realtime_start"`
		RealtimeEnd   string `json:"realtime_end"`
		Date          string `json:"date"`
		Value         string `json:"value"`
	}
	var base baseObservation
	if err := json.Unmarshal(data, &base); err != nil {
		return err
	}
	o.RealtimeStart = base.RealtimeStart
	o.RealtimeEnd = base.RealtimeEnd
	o.Date = base.Date
	o.Value = base.Value
	o.Extra = map[string]string{}
	for key, value := range raw {
		switch key {
		case "realtime_start", "realtime_end", "date", "value":
			continue
		default:
			var text string
			if err := json.Unmarshal(value, &text); err == nil {
				o.Extra[key] = text
			}
		}
	}
	return nil
}
