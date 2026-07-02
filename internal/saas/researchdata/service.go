package researchdata

import (
	"context"
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
)

var (
	ErrInvalidDatasetRequest = errors.New("研究資料集設定不正確")
	ErrMissingPrimaryData    = errors.New("主商品缺少可用資料")
)

type Service struct {
	db         *gorm.DB
	marketData *marketdata.Service
}

type PreviewRequest struct {
	PrimaryInstrumentID string               `json:"primary_instrument_id"`
	PrimaryInterval     string               `json:"primary_interval"`
	Indicators          []IndicatorSelection `json:"indicators"`
	StartTimeMs         int64                `json:"start_time_ms"`
	EndTimeMs           int64                `json:"end_time_ms"`
	MissingPolicy       string               `json:"missing_policy"`
	IndicatorAlgorithm  string               `json:"indicator_algorithm"`
}

type IndicatorSelection struct {
	InstrumentID string `json:"instrument_id"`
	Interval     string `json:"interval"`
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
	Time  int64
	Close float64
}

func NewService(db *gorm.DB) *Service {
	return &Service{
		db:         db,
		marketData: marketdata.NewService(db, nil),
	}
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
			SearchBlockedReason: "主商品沒有可用資料，不能建立研究資料集。",
		}, nil
	}

	timeline := make([]int64, 0, len(primaryRows))
	for _, row := range primaryRows {
		timeline = append(timeline, row.Time)
	}
	primary.AlignedRows = len(timeline)
	primary.MissingRows = 0
	primary.FilledRows = 0
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
			warnings = append(warnings, fmt.Sprintf("%s 使用 %s，與主商品 %s 不同，將依缺值策略對齊。", instrument.DisplayName, selection.Interval, req.PrimaryInterval))
		}
		indicators = append(indicators, preview)
	}

	blocked := ""
	canSearch := true
	if len(req.Indicators) > 0 && strings.TrimSpace(req.IndicatorAlgorithm) == "" {
		canSearch = false
		blocked = "已建立參考指標資料集，但尚未啟用任何已確認的指標演算法，因此不能開始參數搜尋。"
	}
	for _, indicator := range indicators {
		if indicator.Error != "" {
			canSearch = false
			if blocked == "" {
				blocked = "參考指標資料有錯誤，不能開始參數搜尋。"
			}
			break
		}
	}

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
	for _, row := range rows {
		points = append(points, seriesPoint{Time: row.OpenTime, Close: row.Close})
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
		if _, ok := byTime[ts]; ok {
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
	switch policy {
	case MissingPolicyForwardFill:
		idx := sort.Search(len(rows), func(i int) bool { return rows[i].Time > ts })
		return idx > 0
	case MissingPolicyLinear:
		idx := sort.Search(len(rows), func(i int) bool { return rows[i].Time >= ts })
		if idx < len(rows) && rows[idx].Time == ts {
			return !math.IsNaN(rows[idx].Close)
		}
		return idx > 0 && idx < len(rows)
	default:
		return false
	}
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
