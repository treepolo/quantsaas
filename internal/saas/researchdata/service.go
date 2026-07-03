package researchdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"quantsaas/internal/saas/marketdata"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
)

const (
	MissingPolicyEmpty       = "empty"
	MissingPolicyForwardFill = "forward_fill"
	MissingPolicyLinear      = "linear"

	SeriesRolePrimary   = "primary"
	SeriesRoleIndicator = "indicator"
)

var (
	ErrInvalidDatasetRequest = errors.New("研究資料集設定不正確")
	ErrMissingPrimaryData    = errors.New("主商品缺少可用資料")
	ErrDatasetNotFound       = errors.New("找不到研究資料集")
)

type Service struct {
	db         *gorm.DB
	marketData *marketdata.Service
}

type DatasetRequest struct {
	Name                string               `json:"name"`
	Notes               string               `json:"notes"`
	PrimaryInstrumentID string               `json:"primary_instrument_id"`
	PrimaryInterval     string               `json:"primary_interval"`
	Indicators          []IndicatorSelection `json:"indicators"`
	StartTimeMs         int64                `json:"start_time_ms"`
	EndTimeMs           int64                `json:"end_time_ms"`
	MissingPolicy       string               `json:"missing_policy"`
	IndicatorAlgorithm  string               `json:"indicator_algorithm"`
}

type PreviewRequest = DatasetRequest

type IndicatorSelection struct {
	InstrumentID string `json:"instrument_id"`
	Interval     string `json:"interval"`
}

type DatasetResponse struct {
	ID                  uint            `json:"id"`
	Name                string          `json:"name"`
	Notes               string          `json:"notes"`
	Primary             DatasetSeries   `json:"primary"`
	Indicators          []DatasetSeries `json:"indicators"`
	StartTimeMs         int64           `json:"start_time_ms"`
	EndTimeMs           int64           `json:"end_time_ms"`
	MissingPolicy       string          `json:"missing_policy"`
	IndicatorAlgorithm  string          `json:"indicator_algorithm"`
	CanSearch           bool            `json:"can_search"`
	SearchBlockedReason string          `json:"search_blocked_reason,omitempty"`
	Warnings            []string        `json:"warnings,omitempty"`
	Preview             *PreviewResult  `json:"preview,omitempty"`
	CreatedAt           string          `json:"created_at"`
	UpdatedAt           string          `json:"updated_at"`
}

type DatasetSeries struct {
	InstrumentID string `json:"instrument_id"`
	Symbol       string `json:"symbol"`
	DisplayName  string `json:"display_name"`
	DataSource   string `json:"data_source"`
	Interval     string `json:"interval"`
	SortOrder    int    `json:"sort_order"`
}

type PreviewResult struct {
	Primary             SeriesPreview   `json:"primary"`
	Indicators          []SeriesPreview `json:"indicators"`
	MissingPolicy       string          `json:"missing_policy"`
	StartTimeMs         int64           `json:"start_time_ms"`
	EndTimeMs           int64           `json:"end_time_ms"`
	AlignedRows         int             `json:"aligned_rows"`
	ReferenceCount      int             `json:"reference_count"`
	CanSearch           bool            `json:"can_search"`
	SearchBlockedReason string          `json:"search_blocked_reason,omitempty"`
	Warnings            []string        `json:"warnings,omitempty"`
}

type SeriesPreview struct {
	InstrumentID       string `json:"instrument_id"`
	Symbol             string `json:"symbol"`
	DisplayName        string `json:"display_name"`
	DataSource         string `json:"data_source"`
	Interval           string `json:"interval"`
	RawRows            int    `json:"raw_rows"`
	AlignedRows        int    `json:"aligned_rows"`
	MissingRows        int    `json:"missing_rows"`
	FilledRows         int    `json:"filled_rows"`
	FirstDataTimeMs    int64  `json:"first_data_time_ms,omitempty"`
	LastDataTimeMs     int64  `json:"last_data_time_ms,omitempty"`
	FirstAlignedTimeMs int64  `json:"first_aligned_time_ms,omitempty"`
	LastAlignedTimeMs  int64  `json:"last_aligned_time_ms,omitempty"`
	Error              string `json:"error,omitempty"`
}

type seriesPoint struct {
	Time          int64
	AvailableTime int64
	Close         float64
}

type resolvedIndicator struct {
	Instrument marketdata.ResearchInstrument
	Interval   string
}

func NewService(db *gorm.DB) *Service {
	return &Service{
		db:         db,
		marketData: marketdata.NewService(db, nil),
	}
}

func (s *Service) List(ctx context.Context) ([]DatasetResponse, error) {
	var rows []saasstore.ResearchDataset
	if err := s.db.WithContext(ctx).Order("updated_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]DatasetResponse, 0, len(rows))
	for _, row := range rows {
		item, err := s.responseForRecord(ctx, row, false)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, id uint, includePreview bool) (DatasetResponse, error) {
	var row saasstore.ResearchDataset
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DatasetResponse{}, ErrDatasetNotFound
		}
		return DatasetResponse{}, err
	}
	return s.responseForRecord(ctx, row, includePreview)
}

func (s *Service) Create(ctx context.Context, req DatasetRequest) (DatasetResponse, error) {
	normalized, primary, indicators, err := s.normalizeRequest(ctx, req)
	if err != nil {
		return DatasetResponse{}, err
	}
	if strings.TrimSpace(normalized.Name) == "" {
		normalized.Name = defaultDatasetName(primary, normalized.PrimaryInterval, len(indicators))
	}

	var created saasstore.ResearchDataset
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		config, _ := saasstore.NewJSONB(map[string]any{})
		created = saasstore.ResearchDataset{
			Name:                normalized.Name,
			Notes:               normalized.Notes,
			PrimaryInstrumentID: primary.ID,
			PrimaryDataSource:   primary.DataSource,
			PrimarySymbol:       primary.Symbol,
			PrimaryInterval:     normalized.PrimaryInterval,
			StartTimeMs:         normalized.StartTimeMs,
			EndTimeMs:           normalized.EndTimeMs,
			MissingPolicy:       normalized.MissingPolicy,
			IndicatorAlgorithm:  normalized.IndicatorAlgorithm,
			Config:              config,
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		return replaceSeries(ctx, tx, created.ID, primary, normalized.PrimaryInterval, indicators)
	})
	if err != nil {
		return DatasetResponse{}, err
	}
	return s.Get(ctx, created.ID, true)
}

func (s *Service) Update(ctx context.Context, id uint, req DatasetRequest) (DatasetResponse, error) {
	normalized, primary, indicators, err := s.normalizeRequest(ctx, req)
	if err != nil {
		return DatasetResponse{}, err
	}
	if strings.TrimSpace(normalized.Name) == "" {
		normalized.Name = defaultDatasetName(primary, normalized.PrimaryInterval, len(indicators))
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing saasstore.ResearchDataset
		if err := tx.First(&existing, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrDatasetNotFound
			}
			return err
		}
		updates := map[string]any{
			"name":                  normalized.Name,
			"notes":                 normalized.Notes,
			"primary_instrument_id": primary.ID,
			"primary_data_source":   primary.DataSource,
			"primary_symbol":        primary.Symbol,
			"primary_interval":      normalized.PrimaryInterval,
			"start_time_ms":         normalized.StartTimeMs,
			"end_time_ms":           normalized.EndTimeMs,
			"missing_policy":        normalized.MissingPolicy,
			"indicator_algorithm":   normalized.IndicatorAlgorithm,
		}
		if err := tx.Model(&existing).Updates(updates).Error; err != nil {
			return err
		}
		return replaceSeries(ctx, tx, id, primary, normalized.PrimaryInterval, indicators)
	})
	if err != nil {
		return DatasetResponse{}, err
	}
	return s.Get(ctx, id, true)
}

func (s *Service) Delete(ctx context.Context, id uint) error {
	result := s.db.WithContext(ctx).Delete(&saasstore.ResearchDataset{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrDatasetNotFound
	}
	return nil
}

func (s *Service) Preview(ctx context.Context, req PreviewRequest) (PreviewResult, error) {
	req.MissingPolicy = normalizeMissingPolicy(req.MissingPolicy)
	if strings.TrimSpace(req.PrimaryInstrumentID) == "" || strings.TrimSpace(req.PrimaryInterval) == "" {
		return PreviewResult{}, fmt.Errorf("%w: 主商品與資料週期必填", ErrInvalidDatasetRequest)
	}
	if req.StartTimeMs <= 0 || req.EndTimeMs <= 0 || req.StartTimeMs > req.EndTimeMs {
		return PreviewResult{}, fmt.Errorf("%w: 資料起訖時間不正確", ErrInvalidDatasetRequest)
	}

	primaryInstrument, err := s.marketData.ResolveInstrument(ctx, req.PrimaryInstrumentID, "", "")
	if err != nil {
		return PreviewResult{}, err
	}
	primaryRows, err := s.loadSeries(ctx, primaryInstrument, req.PrimaryInterval, req.StartTimeMs, req.EndTimeMs)
	if err != nil {
		return PreviewResult{}, err
	}
	primary := previewForRawSeries(primaryInstrument, req.PrimaryInterval, primaryRows)
	if len(primaryRows) == 0 {
		primary.Error = ErrMissingPrimaryData.Error()
		return PreviewResult{
			Primary:             primary,
			MissingPolicy:       req.MissingPolicy,
			StartTimeMs:         req.StartTimeMs,
			EndTimeMs:           req.EndTimeMs,
			ReferenceCount:      len(req.Indicators),
			CanSearch:           false,
			SearchBlockedReason: "主商品沒有可用資料，不能建立可搜尋的研究資料集",
		}, nil
	}

	timeline := make([]int64, 0, len(primaryRows))
	for _, row := range primaryRows {
		timeline = append(timeline, row.Time)
	}
	primary.AlignedRows = len(timeline)
	primary.FirstAlignedTimeMs = timeline[0]
	primary.LastAlignedTimeMs = timeline[len(timeline)-1]

	warnings := []string{}
	indicators := make([]SeriesPreview, 0, len(req.Indicators))
	for _, selection := range req.Indicators {
		selection.Interval = strings.TrimSpace(selection.Interval)
		instrumentID := strings.TrimSpace(selection.InstrumentID)
		if instrumentID == "" || selection.Interval == "" {
			indicators = append(indicators, SeriesPreview{Error: "參考指標與週期必填"})
			continue
		}
		instrument, err := s.marketData.ResolveInstrument(ctx, instrumentID, "", "")
		if err != nil {
			indicators = append(indicators, SeriesPreview{
				InstrumentID: instrumentID,
				Interval:     selection.Interval,
				Error:        err.Error(),
			})
			continue
		}
		rows, err := s.loadSeries(ctx, instrument, selection.Interval, req.StartTimeMs, req.EndTimeMs)
		if err != nil {
			indicators = append(indicators, SeriesPreview{
				InstrumentID: instrument.ID,
				Symbol:       instrument.Symbol,
				DisplayName:  instrument.DisplayName,
				DataSource:   instrument.DataSource,
				Interval:     selection.Interval,
				Error:        err.Error(),
			})
			continue
		}
		preview := previewForRawSeries(instrument, selection.Interval, rows)
		preview.AlignedRows, preview.MissingRows, preview.FilledRows = alignStats(timeline, rows, req.MissingPolicy)
		if preview.AlignedRows > 0 {
			preview.FirstAlignedTimeMs = timeline[0]
			preview.LastAlignedTimeMs = timeline[len(timeline)-1]
		}
		if selection.Interval != req.PrimaryInterval {
			warnings = append(warnings, fmt.Sprintf("%s 使用 %s，與主商品 %s 不同，會依缺值策略對齊", instrument.DisplayName, selection.Interval, req.PrimaryInterval))
		}
		indicators = append(indicators, preview)
	}

	canSearch, blocked := searchReadiness(len(req.Indicators), req.IndicatorAlgorithm, indicators)
	return PreviewResult{
		Primary:             primary,
		Indicators:          indicators,
		MissingPolicy:       req.MissingPolicy,
		StartTimeMs:         req.StartTimeMs,
		EndTimeMs:           req.EndTimeMs,
		AlignedRows:         len(timeline),
		ReferenceCount:      len(req.Indicators),
		CanSearch:           canSearch,
		SearchBlockedReason: blocked,
		Warnings:            warnings,
	}, nil
}

func (s *Service) normalizeRequest(ctx context.Context, req DatasetRequest) (DatasetRequest, marketdata.ResearchInstrument, []resolvedIndicator, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Notes = strings.TrimSpace(req.Notes)
	req.PrimaryInstrumentID = strings.TrimSpace(req.PrimaryInstrumentID)
	req.PrimaryInterval = strings.TrimSpace(req.PrimaryInterval)
	req.MissingPolicy = normalizeMissingPolicy(req.MissingPolicy)
	req.IndicatorAlgorithm = strings.TrimSpace(req.IndicatorAlgorithm)
	if req.PrimaryInstrumentID == "" || req.PrimaryInterval == "" {
		return req, marketdata.ResearchInstrument{}, nil, fmt.Errorf("%w: 主商品與資料週期必填", ErrInvalidDatasetRequest)
	}
	if req.StartTimeMs <= 0 || req.EndTimeMs <= 0 || req.StartTimeMs > req.EndTimeMs {
		return req, marketdata.ResearchInstrument{}, nil, fmt.Errorf("%w: 資料起訖時間不正確", ErrInvalidDatasetRequest)
	}
	primary, err := s.marketData.ResolveInstrument(ctx, req.PrimaryInstrumentID, "", "")
	if err != nil {
		return req, marketdata.ResearchInstrument{}, nil, err
	}
	indicators := make([]resolvedIndicator, 0, len(req.Indicators))
	seen := map[string]bool{}
	for index, selection := range req.Indicators {
		selection.InstrumentID = strings.TrimSpace(selection.InstrumentID)
		selection.Interval = strings.TrimSpace(selection.Interval)
		if selection.InstrumentID == "" || selection.Interval == "" {
			return req, marketdata.ResearchInstrument{}, nil, fmt.Errorf("%w: 第 %d 個參考指標不完整", ErrInvalidDatasetRequest, index+1)
		}
		if selection.InstrumentID == primary.ID {
			return req, marketdata.ResearchInstrument{}, nil, fmt.Errorf("%w: 參考指標不能與主商品相同", ErrInvalidDatasetRequest)
		}
		key := selection.InstrumentID + "|" + selection.Interval
		if seen[key] {
			return req, marketdata.ResearchInstrument{}, nil, fmt.Errorf("%w: 參考指標重複", ErrInvalidDatasetRequest)
		}
		seen[key] = true
		instrument, err := s.marketData.ResolveInstrument(ctx, selection.InstrumentID, "", "")
		if err != nil {
			return req, marketdata.ResearchInstrument{}, nil, err
		}
		req.Indicators[index] = selection
		indicators = append(indicators, resolvedIndicator{Instrument: instrument, Interval: selection.Interval})
	}
	return req, primary, indicators, nil
}

func (s *Service) responseForRecord(ctx context.Context, row saasstore.ResearchDataset, includePreview bool) (DatasetResponse, error) {
	var seriesRows []saasstore.ResearchDatasetSeries
	if err := s.db.WithContext(ctx).
		Where("dataset_id = ?", row.ID).
		Order("role ASC, sort_order ASC, id ASC").
		Find(&seriesRows).Error; err != nil {
		return DatasetResponse{}, err
	}
	response := DatasetResponse{
		ID:                 row.ID,
		Name:               row.Name,
		Notes:              row.Notes,
		StartTimeMs:        row.StartTimeMs,
		EndTimeMs:          row.EndTimeMs,
		MissingPolicy:      normalizeMissingPolicy(row.MissingPolicy),
		IndicatorAlgorithm: row.IndicatorAlgorithm,
		CreatedAt:          row.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:          row.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	for _, item := range seriesRows {
		series := DatasetSeries{
			InstrumentID: item.InstrumentID,
			Symbol:       item.Symbol,
			DisplayName:  item.Symbol,
			DataSource:   item.DataSource,
			Interval:     item.Interval,
			SortOrder:    item.SortOrder,
		}
		if instrument, err := s.marketData.ResolveInstrument(ctx, item.InstrumentID, "", ""); err == nil {
			series.DisplayName = instrument.DisplayName
		}
		if item.Role == SeriesRolePrimary {
			response.Primary = series
		} else {
			response.Indicators = append(response.Indicators, series)
		}
	}
	if response.Primary.InstrumentID == "" {
		response.Primary = DatasetSeries{
			InstrumentID: row.PrimaryInstrumentID,
			Symbol:       row.PrimarySymbol,
			DisplayName:  row.PrimarySymbol,
			DataSource:   row.PrimaryDataSource,
			Interval:     row.PrimaryInterval,
		}
	}
	response.CanSearch, response.SearchBlockedReason = searchReadiness(len(response.Indicators), response.IndicatorAlgorithm, nil)
	if includePreview {
		preview, err := s.Preview(ctx, response.toRequest())
		if err != nil {
			return response, nil
		}
		response.Preview = &preview
		response.CanSearch = preview.CanSearch
		response.SearchBlockedReason = preview.SearchBlockedReason
		response.Warnings = preview.Warnings
	}
	return response, nil
}

func (d DatasetResponse) toRequest() DatasetRequest {
	indicators := make([]IndicatorSelection, 0, len(d.Indicators))
	for _, item := range d.Indicators {
		indicators = append(indicators, IndicatorSelection{InstrumentID: item.InstrumentID, Interval: item.Interval})
	}
	return DatasetRequest{
		Name:                d.Name,
		Notes:               d.Notes,
		PrimaryInstrumentID: d.Primary.InstrumentID,
		PrimaryInterval:     d.Primary.Interval,
		Indicators:          indicators,
		StartTimeMs:         d.StartTimeMs,
		EndTimeMs:           d.EndTimeMs,
		MissingPolicy:       d.MissingPolicy,
		IndicatorAlgorithm:  d.IndicatorAlgorithm,
	}
}

func replaceSeries(ctx context.Context, tx *gorm.DB, datasetID uint, primary marketdata.ResearchInstrument, primaryInterval string, indicators []resolvedIndicator) error {
	if err := tx.WithContext(ctx).Where("dataset_id = ?", datasetID).Delete(&saasstore.ResearchDatasetSeries{}).Error; err != nil {
		return err
	}
	rows := []saasstore.ResearchDatasetSeries{{
		DatasetID:    datasetID,
		Role:         SeriesRolePrimary,
		SortOrder:    0,
		InstrumentID: primary.ID,
		DataSource:   primary.DataSource,
		Symbol:       primary.Symbol,
		Interval:     primaryInterval,
	}}
	for index, indicator := range indicators {
		rows = append(rows, saasstore.ResearchDatasetSeries{
			DatasetID:    datasetID,
			Role:         SeriesRoleIndicator,
			SortOrder:    index + 1,
			InstrumentID: indicator.Instrument.ID,
			DataSource:   indicator.Instrument.DataSource,
			Symbol:       indicator.Instrument.Symbol,
			Interval:     indicator.Interval,
		})
	}
	return tx.WithContext(ctx).Create(&rows).Error
}

func (s *Service) loadSeries(ctx context.Context, instrument marketdata.ResearchInstrument, interval string, startMs int64, endMs int64) ([]seriesPoint, error) {
	if s.db == nil {
		return nil, nil
	}
	var rows []saasstore.KLine
	if err := s.db.WithContext(ctx).
		Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time BETWEEN ? AND ?", instrument.ID, instrument.DataSource, instrument.Symbol, interval, startMs, endMs).
		Order("open_time ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	points := make([]seriesPoint, 0, len(rows))
	availability := map[int64]int64{}
	if instrument.DataSource == marketdata.DataSourceFRED && len(rows) > 0 {
		var metadata []saasstore.KLineObservationMetadata
		if err := s.db.WithContext(ctx).
			Where("source = ? AND symbol = ? AND interval = ? AND open_time BETWEEN ? AND ?", instrument.DataSource, instrument.Symbol, interval, startMs, endMs).
			Find(&metadata).Error; err != nil {
			return nil, err
		}
		for _, item := range metadata {
			if item.AvailabilityRule == marketdata.FredAvailabilityRuleReleasePlusOneDay && item.AvailableAtMs > 0 {
				availability[item.OpenTime] = item.AvailableAtMs
			}
		}
	}
	for _, row := range rows {
		availableAt := row.OpenTime
		if instrument.DataSource == marketdata.DataSourceFRED {
			availableAt = availability[row.OpenTime]
		}
		points = append(points, seriesPoint{Time: row.OpenTime, AvailableTime: availableAt, Close: row.Close})
	}
	return points, nil
}

func previewForRawSeries(instrument marketdata.ResearchInstrument, interval string, rows []seriesPoint) SeriesPreview {
	preview := SeriesPreview{
		InstrumentID: instrument.ID,
		Symbol:       instrument.Symbol,
		DisplayName:  instrument.DisplayName,
		DataSource:   instrument.DataSource,
		Interval:     interval,
		RawRows:      len(rows),
	}
	if len(rows) > 0 {
		preview.FirstDataTimeMs = rows[0].Time
		preview.LastDataTimeMs = rows[len(rows)-1].Time
	}
	return preview
}

func alignStats(timeline []int64, rows []seriesPoint, policy string) (aligned int, missing int, filled int) {
	if len(timeline) == 0 {
		return 0, 0, 0
	}
	if len(rows) == 0 {
		return 0, len(timeline), 0
	}
	byTime := make(map[int64]seriesPoint, len(rows))
	for _, row := range rows {
		byTime[row.Time] = row
	}
	for _, ts := range timeline {
		if row, ok := byTime[ts]; ok && pointAvailableAt(row, ts) {
			aligned++
			continue
		}
		if valueAvailableAt(ts, rows, policy) {
			aligned++
			filled++
		} else {
			missing++
		}
	}
	return aligned, missing, filled
}

func valueAvailableAt(ts int64, rows []seriesPoint, policy string) bool {
	normalized := normalizeSeriesPoints(rows)
	switch policy {
	case MissingPolicyForwardFill:
		idx := sort.Search(len(normalized), func(i int) bool { return normalized[i].Time > ts })
		for i := idx - 1; i >= 0; i-- {
			if pointAvailableAt(normalized[i], ts) {
				return true
			}
		}
		return false
	case MissingPolicyLinear:
		idx := sort.Search(len(normalized), func(i int) bool { return normalized[i].Time >= ts })
		if idx < len(normalized) && normalized[idx].Time == ts {
			return pointAvailableAt(normalized[idx], ts) && !math.IsNaN(normalized[idx].Close)
		}
		if idx <= 0 || idx >= len(normalized) {
			return false
		}
		return pointAvailableAt(normalized[idx-1], ts) && pointAvailableAt(normalized[idx], ts)
	default:
		return false
	}
}

func normalizeSeriesPoints(rows []seriesPoint) []seriesPoint {
	if len(rows) == 0 {
		return nil
	}
	out := append([]seriesPoint(nil), rows...)
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

func pointAvailableAt(row seriesPoint, ts int64) bool {
	return row.AvailableTime > 0 && row.AvailableTime <= ts
}

func normalizeMissingPolicy(value string) string {
	switch strings.TrimSpace(value) {
	case MissingPolicyForwardFill:
		return MissingPolicyForwardFill
	case MissingPolicyLinear:
		return MissingPolicyLinear
	default:
		return MissingPolicyEmpty
	}
}

func searchReadiness(indicatorCount int, algorithm string, indicators []SeriesPreview) (bool, string) {
	for _, indicator := range indicators {
		if indicator.Error != "" {
			return false, "參考指標資料有錯誤，不能開始參數搜尋"
		}
	}
	if indicatorCount > 0 && strings.TrimSpace(algorithm) == "" {
		return false, "此資料集含參考指標，但尚未指定已確認的指標演算法，因此不能開始參數搜尋"
	}
	return true, ""
}

func defaultDatasetName(primary marketdata.ResearchInstrument, interval string, indicatorCount int) string {
	if indicatorCount == 0 {
		return fmt.Sprintf("%s %s 單商品資料集", primary.DisplayName, interval)
	}
	return fmt.Sprintf("%s %s + %d 個參考指標", primary.DisplayName, interval, indicatorCount)
}

func marshalConfig(v any) saasstore.JSONB {
	raw, err := json.Marshal(v)
	if err != nil {
		return saasstore.JSONB(`{}`)
	}
	return saasstore.JSONB(raw)
}
