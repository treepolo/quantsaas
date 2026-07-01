package marketdata

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"quantsaas/internal/quant"
	saasstore "quantsaas/internal/saas/store"
)

var ErrInvalidDatasetRequest = errors.New("invalid research dataset request")

type DatasetBuildRequest struct {
	TradableSeriesIDs  []string `json:"tradable_series_ids"`
	IndicatorSeriesIDs []string `json:"indicator_series_ids"`
	Interval           string   `json:"interval"`
	StartTimeMs        int64    `json:"start_time_ms"`
	EndTimeMs          int64    `json:"end_time_ms"`
	MaxRows            int      `json:"max_rows"`
}

type ResearchDataset struct {
	Interval    string              `json:"interval"`
	StartTimeMs int64               `json:"start_time_ms"`
	EndTimeMs   int64               `json:"end_time_ms"`
	Series      []DatasetSeriesInfo `json:"series"`
	Rows        []DatasetRow        `json:"rows"`
	Issues      []DatasetIssue      `json:"issues,omitempty"`
}

type DatasetSeriesInfo struct {
	ID              string `json:"id"`
	Role            string `json:"role"`
	SeriesType      string `json:"series_type"`
	DisplayName     string `json:"display_name"`
	DataSource      string `json:"data_source"`
	PointCount      int    `json:"point_count"`
	MissingCount    int    `json:"missing_count"`
	FirstObservedMs int64  `json:"first_observed_ms,omitempty"`
	LastObservedMs  int64  `json:"last_observed_ms,omitempty"`
}

type DatasetRow struct {
	ObservedAtMs    int64                   `json:"observed_at_ms"`
	DecisionTimeMs  int64                   `json:"decision_time_ms"`
	Values          map[string]DatasetValue `json:"values"`
	MissingSeriesID []string                `json:"missing_series_ids,omitempty"`
}

type DatasetValue struct {
	SeriesID      string  `json:"series_id"`
	ObservedAtMs  int64   `json:"observed_at_ms"`
	AvailableAtMs int64   `json:"available_at_ms"`
	Value         float64 `json:"value"`
	Open          float64 `json:"open,omitempty"`
	High          float64 `json:"high,omitempty"`
	Low           float64 `json:"low,omitempty"`
	Close         float64 `json:"close,omitempty"`
	Volume        float64 `json:"volume,omitempty"`
	Source        string  `json:"source"`
	LagMs         int64   `json:"lag_ms"`
}

type DatasetIssue struct {
	SeriesID string `json:"series_id,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type datasetSeriesData struct {
	Series ResearchSeries
	Role   string
	Values []DatasetValue
}

func (s *Service) BuildDataset(ctx context.Context, req DatasetBuildRequest) (ResearchDataset, error) {
	req = normalizeDatasetRequest(req)
	if err := validateDatasetRequest(req); err != nil {
		return ResearchDataset{}, err
	}
	if s == nil || s.db == nil {
		return ResearchDataset{}, fmt.Errorf("market data service is unavailable")
	}

	tradables, indicators, err := s.loadDatasetSeries(ctx, req)
	if err != nil {
		return ResearchDataset{}, err
	}
	if len(tradables) == 0 {
		return ResearchDataset{}, fmt.Errorf("%w: at least one tradable series is required", ErrInvalidDatasetRequest)
	}

	primary, err := s.loadTradableSeriesValues(ctx, tradables[0], req.Interval, req.StartTimeMs, req.EndTimeMs)
	if err != nil {
		return ResearchDataset{}, err
	}
	primary.Role = "primary_tradable"
	if len(primary.Values) == 0 {
		return ResearchDataset{}, fmt.Errorf("%w: primary tradable series has no data", ErrInvalidDatasetRequest)
	}
	maxDecisionTime := primary.Values[len(primary.Values)-1].AvailableAtMs

	all := []datasetSeriesData{primary}
	for _, series := range tradables[1:] {
		data, err := s.loadTradableSeriesValues(ctx, series, req.Interval, req.StartTimeMs, req.EndTimeMs)
		if err != nil {
			return ResearchDataset{}, err
		}
		data.Role = "tradable_asset"
		all = append(all, data)
	}
	for _, series := range indicators {
		data, err := s.loadPointSeriesValues(ctx, series, req.EndTimeMs, maxDecisionTime)
		if err != nil {
			return ResearchDataset{}, err
		}
		data.Role = "indicator"
		all = append(all, data)
	}

	result := assembleDataset(req, all)
	return result, nil
}

func (s *Service) BuildDatasetBars(ctx context.Context, req DatasetBuildRequest) ([]quant.Bar, ResearchDataset, error) {
	dataset, err := s.BuildDataset(ctx, req)
	if err != nil {
		return nil, ResearchDataset{}, err
	}
	bars, err := PrimaryBarsFromDataset(dataset)
	if err != nil {
		return nil, dataset, err
	}
	return bars, dataset, nil
}

func PrimaryBarsFromDataset(dataset ResearchDataset) ([]quant.Bar, error) {
	primaryID := primarySeriesID(dataset)
	if primaryID == "" {
		return nil, fmt.Errorf("%w: primary tradable series is missing", ErrInvalidDatasetRequest)
	}
	bars := make([]quant.Bar, 0, len(dataset.Rows))
	for _, row := range dataset.Rows {
		value, ok := row.Values[primaryID]
		if !ok {
			return nil, fmt.Errorf("%w: primary tradable value is missing at %d", ErrInvalidDatasetRequest, row.ObservedAtMs)
		}
		bar, err := datasetValueToBar(row.ObservedAtMs, value)
		if err != nil {
			return nil, err
		}
		bars = append(bars, bar)
	}
	return bars, nil
}

func ExternalSignalByTime(dataset ResearchDataset) map[int64]float64 {
	indicatorIDs := indicatorSeriesIDs(dataset)
	if len(indicatorIDs) == 0 || len(dataset.Rows) == 0 {
		return nil
	}
	history := make(map[string][]float64, len(indicatorIDs))
	out := map[int64]float64{}
	for _, row := range dataset.Rows {
		sum := 0.0
		count := 0
		for _, id := range indicatorIDs {
			value, ok := row.Values[id]
			if !ok {
				continue
			}
			scalar, ok := datasetScalarValue(value)
			if !ok {
				continue
			}
			history[id] = append(history[id], scalar)
			if z, ok := rollingZScore(history[id], 252); ok {
				sum += z
				count++
			}
		}
		if count > 0 {
			out[row.ObservedAtMs] = sum / float64(count)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func indicatorSeriesIDs(dataset ResearchDataset) []string {
	out := []string{}
	for _, info := range dataset.Series {
		if info.Role == "indicator" || info.SeriesType == SeriesTypeIndicator || info.SeriesType == SeriesTypeDerived {
			out = append(out, info.ID)
		}
	}
	return out
}

func datasetScalarValue(value DatasetValue) (float64, bool) {
	scalar := value.Value
	if scalar == 0 && value.Close != 0 {
		scalar = value.Close
	}
	if math.IsNaN(scalar) || math.IsInf(scalar, 0) {
		return 0, false
	}
	return scalar, true
}

func rollingZScore(values []float64, window int) (float64, bool) {
	if len(values) < 2 {
		return 0, false
	}
	if window <= 1 || window > len(values) {
		window = len(values)
	}
	recent := values[len(values)-window:]
	mean := 0.0
	for _, value := range recent {
		mean += value
	}
	mean /= float64(len(recent))
	variance := 0.0
	for _, value := range recent {
		delta := value - mean
		variance += delta * delta
	}
	std := math.Sqrt(variance / float64(len(recent)))
	if std <= 0 || math.IsNaN(std) || math.IsInf(std, 0) {
		return 0, false
	}
	return (recent[len(recent)-1] - mean) / std, true
}

func primarySeriesID(dataset ResearchDataset) string {
	for _, info := range dataset.Series {
		if info.Role == "primary_tradable" {
			return info.ID
		}
	}
	if len(dataset.Series) > 0 && dataset.Series[0].SeriesType == SeriesTypeTradableAsset {
		return dataset.Series[0].ID
	}
	return ""
}

func datasetValueToBar(observedAtMs int64, value DatasetValue) (quant.Bar, error) {
	closePrice := value.Close
	if closePrice <= 0 {
		closePrice = value.Value
	}
	if closePrice <= 0 {
		return quant.Bar{}, fmt.Errorf("%w: primary tradable close price is invalid at %d", ErrInvalidDatasetRequest, observedAtMs)
	}
	open := value.Open
	if open <= 0 {
		open = closePrice
	}
	high := value.High
	if high <= 0 {
		high = closePrice
	}
	low := value.Low
	if low <= 0 {
		low = closePrice
	}
	return quant.Bar{
		OpenTime: observedAtMs,
		Open:     open,
		High:     high,
		Low:      low,
		Close:    closePrice,
		Volume:   value.Volume,
	}, nil
}

func normalizeDatasetRequest(req DatasetBuildRequest) DatasetBuildRequest {
	req.Interval = normalizeInterval(req.Interval)
	if req.Interval == "" {
		req.Interval = "1d"
	}
	req.TradableSeriesIDs = normalizeSeriesIDs(req.TradableSeriesIDs)
	req.IndicatorSeriesIDs = normalizeSeriesIDs(req.IndicatorSeriesIDs)
	return req
}

func validateDatasetRequest(req DatasetBuildRequest) error {
	if len(req.TradableSeriesIDs) == 0 {
		return fmt.Errorf("%w: tradable_series_ids is required", ErrInvalidDatasetRequest)
	}
	if req.StartTimeMs <= 0 || req.EndTimeMs <= 0 || req.EndTimeMs < req.StartTimeMs {
		return fmt.Errorf("%w: invalid time range", ErrInvalidDatasetRequest)
	}
	return nil
}

func normalizeSeriesIDs(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		id := normalizeSeriesID(value)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func NormalizeSeriesIDs(values []string) []string {
	return normalizeSeriesIDs(values)
}

func (s *Service) loadDatasetSeries(ctx context.Context, req DatasetBuildRequest) ([]ResearchSeries, []ResearchSeries, error) {
	ids := append([]string{}, req.TradableSeriesIDs...)
	ids = append(ids, req.IndicatorSeriesIDs...)
	records := []saasstore.ResearchSeries{}
	if err := s.db.WithContext(ctx).
		Where("enabled = ? AND id IN ?", true, ids).
		Find(&records).Error; err != nil {
		return nil, nil, err
	}
	byID := map[string]ResearchSeries{}
	for _, record := range records {
		series, err := recordToSeries(record)
		if err != nil {
			return nil, nil, err
		}
		byID[series.ID] = series
	}

	tradables := make([]ResearchSeries, 0, len(req.TradableSeriesIDs))
	for _, id := range req.TradableSeriesIDs {
		series, ok := byID[id]
		if !ok {
			return nil, nil, fmt.Errorf("%w: series not found: %s", ErrInvalidDatasetRequest, id)
		}
		if series.SeriesType != SeriesTypeTradableAsset || !series.Tradable {
			return nil, nil, fmt.Errorf("%w: series is not tradable: %s", ErrInvalidDatasetRequest, id)
		}
		tradables = append(tradables, series)
	}

	indicators := make([]ResearchSeries, 0, len(req.IndicatorSeriesIDs))
	for _, id := range req.IndicatorSeriesIDs {
		series, ok := byID[id]
		if !ok {
			return nil, nil, fmt.Errorf("%w: series not found: %s", ErrInvalidDatasetRequest, id)
		}
		if series.SeriesType == SeriesTypeTradableAsset && series.Tradable {
			return nil, nil, fmt.Errorf("%w: tradable series cannot be used as indicator: %s", ErrInvalidDatasetRequest, id)
		}
		indicators = append(indicators, series)
	}
	return tradables, indicators, nil
}

func (s *Service) loadTradableSeriesValues(ctx context.Context, series ResearchSeries, interval string, startMs int64, endMs int64) (datasetSeriesData, error) {
	instrumentID := series.SourceInstrumentID
	if instrumentID == "" {
		instrumentID = series.ID
	}
	var rows []saasstore.KLine
	err := s.db.WithContext(ctx).
		Where("instrument_id = ? AND source = ? AND interval = ? AND open_time BETWEEN ? AND ?", instrumentID, series.DataSource, interval, startMs, endMs).
		Order("open_time ASC").
		Find(&rows).Error
	if err != nil {
		return datasetSeriesData{}, err
	}
	values := make([]DatasetValue, 0, len(rows))
	for _, row := range rows {
		availableAt := klineAvailableAtMs(series, interval, row.OpenTime)
		values = append(values, DatasetValue{
			SeriesID:      series.ID,
			ObservedAtMs:  row.OpenTime,
			AvailableAtMs: availableAt,
			Value:         row.Close,
			Open:          row.Open,
			High:          row.High,
			Low:           row.Low,
			Close:         row.Close,
			Volume:        row.Volume,
			Source:        "kline",
		})
	}
	return datasetSeriesData{Series: series, Values: values}, nil
}

func (s *Service) loadPointSeriesValues(ctx context.Context, series ResearchSeries, endObservedMs int64, maxDecisionMs int64) (datasetSeriesData, error) {
	var rows []saasstore.SeriesPoint
	err := s.db.WithContext(ctx).
		Where("series_id = ? AND observed_at_ms <= ? AND available_at_ms <= ?", series.ID, endObservedMs, maxDecisionMs).
		Order("available_at_ms ASC, observed_at_ms ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return datasetSeriesData{}, err
	}
	values := make([]DatasetValue, 0, len(rows))
	for _, row := range rows {
		values = append(values, DatasetValue{
			SeriesID:      series.ID,
			ObservedAtMs:  row.ObservedAtMs,
			AvailableAtMs: row.AvailableAtMs,
			Value:         row.Value,
			Open:          row.Open,
			High:          row.High,
			Low:           row.Low,
			Close:         row.Close,
			Volume:        row.Volume,
			Source:        "series_point",
		})
	}
	return datasetSeriesData{Series: series, Values: values}, nil
}

func assembleDataset(req DatasetBuildRequest, seriesData []datasetSeriesData) ResearchDataset {
	result := ResearchDataset{
		Interval:    req.Interval,
		StartTimeMs: req.StartTimeMs,
		EndTimeMs:   req.EndTimeMs,
		Series:      make([]DatasetSeriesInfo, 0, len(seriesData)),
		Rows:        []DatasetRow{},
	}
	if len(seriesData) == 0 || len(seriesData[0].Values) == 0 {
		result.Issues = append(result.Issues, DatasetIssue{Code: "empty_primary_series", Message: "primary tradable series has no values"})
		return result
	}

	indexes := make([]map[int64]DatasetValue, len(seriesData))
	for i, data := range seriesData {
		indexes[i] = map[int64]DatasetValue{}
		for _, value := range data.Values {
			indexes[i][value.ObservedAtMs] = value
		}
	}

	for _, primaryValue := range seriesData[0].Values {
		row := DatasetRow{
			ObservedAtMs:   primaryValue.ObservedAtMs,
			DecisionTimeMs: primaryValue.AvailableAtMs,
			Values:         map[string]DatasetValue{},
		}
		for i, data := range seriesData {
			value, ok := datasetValueForRow(data, indexes[i], primaryValue.ObservedAtMs, primaryValue.AvailableAtMs)
			if !ok {
				row.MissingSeriesID = append(row.MissingSeriesID, data.Series.ID)
				continue
			}
			value.LagMs = primaryValue.AvailableAtMs - value.AvailableAtMs
			row.Values[data.Series.ID] = value
		}
		result.Rows = append(result.Rows, row)
	}

	if req.MaxRows > 0 && len(result.Rows) > req.MaxRows {
		result.Rows = result.Rows[len(result.Rows)-req.MaxRows:]
	}

	for _, data := range seriesData {
		info := DatasetSeriesInfo{
			ID:          data.Series.ID,
			Role:        data.Role,
			SeriesType:  data.Series.SeriesType,
			DisplayName: data.Series.DisplayName,
			DataSource:  data.Series.DataSource,
			PointCount:  len(data.Values),
		}
		if len(data.Values) > 0 {
			info.FirstObservedMs = data.Values[0].ObservedAtMs
			info.LastObservedMs = data.Values[len(data.Values)-1].ObservedAtMs
		}
		for _, row := range result.Rows {
			if _, ok := row.Values[data.Series.ID]; !ok {
				info.MissingCount++
			}
		}
		if info.MissingCount > 0 {
			result.Issues = append(result.Issues, DatasetIssue{
				SeriesID: data.Series.ID,
				Code:     "missing_values",
				Message:  fmt.Sprintf("%s missing %d rows in preview", data.Series.ID, info.MissingCount),
			})
		}
		result.Series = append(result.Series, info)
	}
	return result
}

func datasetValueForRow(data datasetSeriesData, exactIndex map[int64]DatasetValue, observedAtMs int64, decisionTimeMs int64) (DatasetValue, bool) {
	if data.Series.SeriesType == SeriesTypeTradableAsset && data.Series.Tradable {
		value, ok := exactIndex[observedAtMs]
		if !ok || value.AvailableAtMs > decisionTimeMs {
			return DatasetValue{}, false
		}
		return value, true
	}
	var latest DatasetValue
	ok := false
	for _, value := range data.Values {
		if value.AvailableAtMs > decisionTimeMs {
			break
		}
		if value.ObservedAtMs > decisionTimeMs {
			continue
		}
		latest = value
		ok = true
	}
	return latest, ok
}

func klineAvailableAtMs(series ResearchSeries, interval string, openTimeMs int64) int64 {
	instrument := ResearchInstrument{
		ID:         firstNonEmpty(series.SourceInstrumentID, series.ID),
		Symbol:     series.Symbol,
		DataSource: series.DataSource,
		Market:     series.Market,
	}
	switch normalizeInterval(interval) {
	case "1d":
		return marketDailyCloseAt(instrument.ID, instrument.Symbol, time.UnixMilli(openTimeMs)).UnixMilli()
	case "1w", "1M":
		if period, ok := aggregatePeriodForInstrument(instrument, openTimeMs, interval); ok {
			return period.EndMs
		}
	}
	if duration, ok := intervalDurations[normalizeInterval(interval)]; ok {
		return openTimeMs + duration.Milliseconds()
	}
	return openTimeMs
}
