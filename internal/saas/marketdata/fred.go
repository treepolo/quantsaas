package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultFredBaseURL = "https://api.stlouisfed.org"
	fredDateLayout     = "2006-01-02"
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
	Date  string `json:"date"`
	Value string `json:"value"`
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

func (c *FredClient) FetchObservations(ctx context.Context, seriesID string, startTimeMs int64, endTimeMs int64) ([]BinanceKLine, error) {
	seriesID = normalizeSymbol(seriesID)
	if seriesID == "" {
		return nil, ErrUnsupportedInstrument
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, fmt.Errorf("FRED_API_KEY is required")
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
	if startTimeMs > 0 {
		query.Set("observation_start", time.UnixMilli(startTimeMs).UTC().Format(fredDateLayout))
	}
	if endTimeMs > 0 {
		query.Set("observation_end", time.UnixMilli(endTimeMs).UTC().Format(fredDateLayout))
	}
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
		return nil, fmt.Errorf("fred observations status %d", resp.StatusCode)
	}
	var payload fredObservationResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.ErrorCode != 0 {
		return nil, fmt.Errorf("fred error %d: %s", payload.ErrorCode, payload.ErrorMessage)
	}
	rows := make([]BinanceKLine, 0, len(payload.Observations))
	for _, item := range payload.Observations {
		date, err := time.Parse(fredDateLayout, item.Date)
		if err != nil {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(item.Value), 64)
		if err != nil {
			continue
		}
		rows = append(rows, BinanceKLine{
			OpenTime: date.UTC().UnixMilli(),
			Open:     value,
			High:     value,
			Low:      value,
			Close:    value,
			Volume:   0,
		})
	}
	return rows, nil
}
