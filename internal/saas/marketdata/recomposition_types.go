package marketdata

import (
	"encoding/json"

	"quantsaas/internal/marketversion"
	"quantsaas/internal/saas/computetask"
)

const (
	RecompositionPreviewExecutorType    = "p06.recomposition.preview"
	RecompositionPreviewExecutorVersion = "p06-preview-executor-v1"
	RecompositionPreviewResultVersion   = "p06-preview-result-v1"
	RecompositionPreviewRequestVersion  = "p06-preview-request-v1"
	RecompositionExpandExecutorType     = "p06.recomposition.expand"
	RecompositionAuditExecutorType      = "p06.recomposition.calendar-audit"
	RecompositionPublishExecutorType    = "p06.recomposition.publish"
	RecompositionGenerationExecutorV1   = "p06-generation-executor-v1"
	RecompositionGenerationResultV1     = "p06-generation-result-v1"
	RecompositionGenerationRequestV1    = "p06-generation-request-v1"
)

type RecompositionSegmentRequest struct {
	ItemID             string `json:"item_id"`
	SourceInstrumentID string `json:"source_instrument_id,omitempty"`
	SourceVersionID    uint   `json:"source_version_id,omitempty"`
	StartTimeMs        int64  `json:"start_time_ms"`
	EndTimeMs          int64  `json:"end_time_ms"`
	RepeatCount        int    `json:"repeat_count"`
}

type RecompositionPreviewRequest struct {
	SchemaVersion           string                        `json:"schema_version,omitempty"`
	Segments                []RecompositionSegmentRequest `json:"segments"`
	Interval                string                        `json:"interval"`
	CalendarInstrumentID    string                        `json:"calendar_instrument_id,omitempty"`
	CalendarSourceVersionID uint                          `json:"calendar_source_version_id,omitempty"`
	OutputStartTimeMs       int64                         `json:"output_start_time_ms"`
}

type DatasetFingerprint struct {
	InstrumentID string `json:"instrument_id"`
	DataSource   string `json:"data_source"`
	Symbol       string `json:"symbol"`
	Interval     string `json:"interval"`
	Count        int64  `json:"count"`
	FirstOpenMs  int64  `json:"first_open_ms"`
	LastOpenMs   int64  `json:"last_open_ms"`
	UpdatedAtMs  int64  `json:"updated_at_ms"`
}

type ResolvedRecompositionSource struct {
	Request      RecompositionSegmentRequest `json:"request"`
	Instrument   ResearchInstrument          `json:"instrument"`
	VersionID    uint                        `json:"version_id,omitempty"`
	ContentHash  string                      `json:"content_hash,omitempty"`
	ArtifactKind string                      `json:"artifact_kind"`
	Fingerprint  *DatasetFingerprint         `json:"fingerprint,omitempty"`
	BarCount     int                         `json:"bar_count"`
}

type RecompositionPreviewExecutionInput struct {
	SchemaVersion   string                        `json:"schema_version"`
	Request         RecompositionPreviewRequest   `json:"request"`
	Sources         []ResolvedRecompositionSource `json:"sources"`
	CalendarSource  ResolvedRecompositionSource   `json:"calendar_source"`
	TotalReadBars   int                           `json:"total_read_bars"`
	TotalOutputBars int                           `json:"total_output_bars"`
	RequestHash     string                        `json:"request_hash"`
}

type RecompositionPreviewTask struct {
	Task            *computetask.TaskDescriptor `json:"task"`
	TaskPreview     computetask.PlanPreview     `json:"task_preview"`
	TotalReadBars   int                         `json:"total_read_bars"`
	TotalOutputBars int                         `json:"total_output_bars"`
	EstimatedBytes  int64                       `json:"estimated_bytes"`
}

type RecompositionPreviewResult struct {
	SchemaVersion      string                          `json:"schema_version"`
	PlanID             uint                            `json:"plan_id"`
	PlanHash           string                          `json:"plan_hash"`
	ContentHash        string                          `json:"content_hash"`
	Interval           string                          `json:"interval"`
	TargetMarket       string                          `json:"target_market"`
	TargetTimezone     string                          `json:"target_timezone"`
	CalendarVersionID  uint                            `json:"calendar_version_id"`
	CalendarHash       string                          `json:"calendar_hash"`
	OutputStartTimeMs  int64                           `json:"output_start_time_ms"`
	OutputEndTimeMs    int64                           `json:"output_end_time_ms"`
	SegmentCount       int                             `json:"segment_count"`
	InstanceCount      int                             `json:"instance_count"`
	TotalOutputBars    int                             `json:"total_output_bars"`
	EstimatedReadBars  int                             `json:"estimated_read_bars"`
	EstimatedWriteBars int                             `json:"estimated_write_bars"`
	EstimatedBytes     int64                           `json:"estimated_bytes"`
	AnchorWarningCount int                             `json:"anchor_warning_count"`
	Instances          []marketversion.SegmentInstance `json:"instances"`
}

type RecompositionGenerationRequest struct {
	SchemaVersion  string   `json:"schema_version,omitempty"`
	PlanID         uint     `json:"plan_id"`
	PlanHash       string   `json:"plan_hash"`
	SeriesName     string   `json:"series_name"`
	Notes          string   `json:"notes,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
}

type RecompositionGenerationExecutionInput struct {
	SchemaVersion   string `json:"schema_version"`
	GenerationID    uint   `json:"generation_id"`
	PlanID          uint   `json:"plan_id"`
	PlanHash        string `json:"plan_hash"`
	OutputVersionID uint   `json:"output_version_id"`
}

type RecompositionGenerationResult struct {
	SchemaVersion      string `json:"schema_version"`
	GenerationID       uint   `json:"generation_id"`
	PlanID             uint   `json:"plan_id"`
	PlanHash           string `json:"plan_hash"`
	SeriesID           uint   `json:"series_id"`
	SeriesName         string `json:"series_name"`
	VersionID          uint   `json:"version_id"`
	VersionNumber      int    `json:"version_number"`
	OutputInstrumentID string `json:"output_instrument_id,omitempty"`
	ContentHash        string `json:"content_hash,omitempty"`
	Status             string `json:"status"`
	IntegrityStatus    string `json:"integrity_status"`
	Published          bool   `json:"published"`
	ComputeTaskID      *uint  `json:"compute_task_id,omitempty"`
	ExpandedAt         string `json:"expanded_at,omitempty"`
	CalendarCheckedAt  string `json:"calendar_checked_at,omitempty"`
	PublishedAt        string `json:"published_at,omitempty"`
}

type RecompositionGenerationTask struct {
	Generation RecompositionGenerationResult    `json:"generation"`
	Task       *computetask.TaskDescriptor      `json:"task"`
	Preview    computetask.CompositePlanPreview `json:"task_preview"`
}

type generationCacheResult struct {
	SchemaVersion string `json:"schema_version"`
	GenerationID  uint   `json:"generation_id"`
	VersionID     uint   `json:"version_id"`
	Stage         string `json:"stage"`
	ContentHash   string `json:"content_hash,omitempty"`
}

type MarketSeriesResult struct {
	ID        uint                  `json:"id"`
	Name      string                `json:"name"`
	Notes     string                `json:"notes,omitempty"`
	Tags      []string              `json:"tags"`
	Archived  bool                  `json:"archived"`
	Versions  []MarketVersionResult `json:"versions"`
	CreatedAt string                `json:"created_at"`
}

type MarketVersionResult struct {
	ID              uint   `json:"id"`
	VersionNumber   int    `json:"version_number"`
	ArtifactKind    string `json:"artifact_kind"`
	ContentHash     string `json:"content_hash"`
	PlanHash        string `json:"plan_hash"`
	InstrumentID    string `json:"instrument_id"`
	Interval        string `json:"interval"`
	BarCount        int    `json:"bar_count"`
	StartTimeMs     int64  `json:"start_time_ms"`
	EndTimeMs       int64  `json:"end_time_ms"`
	Status          string `json:"status"`
	IntegrityStatus string `json:"integrity_status"`
	Published       bool   `json:"published"`
	Archived        bool   `json:"archived"`
	CreatedAt       string `json:"created_at"`
}

type RecompositionPlanDetail struct {
	RecompositionPreviewResult
	Segments  []RecompositionPlanSegmentDetail `json:"segments"`
	CreatedAt string                           `json:"created_at"`
}

type RecompositionPlanSegmentDetail struct {
	ItemID               string   `json:"item_id"`
	Order                int      `json:"order"`
	SourceVersionID      uint     `json:"source_version_id"`
	SourceInstrumentID   string   `json:"source_instrument_id"`
	SourceSymbol         string   `json:"source_symbol"`
	SourceDisplayName    string   `json:"source_display_name"`
	SourceContentHash    string   `json:"source_content_hash"`
	StartTimeMs          int64    `json:"start_time_ms"`
	EndTimeMs            int64    `json:"end_time_ms"`
	BarCount             int      `json:"bar_count"`
	RepeatCount          int      `json:"repeat_count"`
	PreviousClosePresent bool     `json:"previous_close_present"`
	PreviousClose        float64  `json:"previous_close,omitempty"`
	FirstOpen            float64  `json:"first_open"`
	SourceGapRatio       *float64 `json:"source_gap_ratio,omitempty"`
}

type VersionBarPage struct {
	Rows   []marketversion.Bar `json:"rows"`
	Total  int64               `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

type RecompositionSource struct {
	Instrument      ResearchInstrument `json:"instrument"`
	VersionID       uint               `json:"version_id,omitempty"`
	ContentHash     string             `json:"content_hash,omitempty"`
	ArtifactKind    string             `json:"artifact_kind"`
	Immutable       bool               `json:"immutable"`
	IntegrityStatus string             `json:"integrity_status,omitempty"`
	Archived        bool               `json:"archived"`
}

type previewCacheResult struct {
	SchemaVersion string `json:"schema_version"`
	PlanID        uint   `json:"plan_id"`
	PlanHash      string `json:"plan_hash"`
}

func marshalCanonical(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	return json.RawMessage(raw), err
}
