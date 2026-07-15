package marketversion

const (
	VersionSchemaVersion       = "p06-market-version-v1"
	BarSchemaVersion           = "p06-market-bar-v1"
	LineageSchemaVersion       = "p06-market-lineage-v1"
	RecompositionPlanVersion   = "p06-recomposition-plan-v1"
	RecompositionAlgorithm     = "p06-segment-recomposition-v1"
	CalendarFromVersionVersion = "p06-version-calendar-v1"
	PricePrecisionVersion      = "p06-price-decimal-10-v1"
)

const (
	ArtifactKindSourceSnapshot       = "source_snapshot"
	ArtifactKindDailyLeverage        = "daily_leverage"
	ArtifactKindSegmentRecomposition = "segment_recomposition"
	ArtifactKindLocalPerturbation    = "local_perturbation"
)

const (
	VersionStatusStaging   = "staging"
	VersionStatusCompleted = "completed"
	VersionStatusFailed    = "failed"
	VersionStatusCancelled = "cancelled"
	VersionStatusCorrupt   = "corrupt"
)

const (
	IntegrityPending = "pending"
	IntegrityValid   = "valid"
	IntegrityCorrupt = "corrupt"
)

type Bar struct {
	Ordinal  int     `json:"ordinal"`
	OpenTime int64   `json:"open_time"`
	Open     float64 `json:"open"`
	High     float64 `json:"high"`
	Low      float64 `json:"low"`
	Close    float64 `json:"close"`
	Volume   float64 `json:"volume"`
}

type VersionIdentity struct {
	VersionID       uint   `json:"version_id"`
	ContentHash     string `json:"content_hash"`
	ArtifactKind    string `json:"artifact_kind"`
	InstrumentID    string `json:"instrument_id"`
	DataSource      string `json:"data_source"`
	Symbol          string `json:"symbol"`
	Market          string `json:"market"`
	Timezone        string `json:"timezone"`
	Interval        string `json:"interval"`
	CalendarID      string `json:"calendar_id"`
	CalendarVersion string `json:"calendar_version"`
}

type SegmentPlan struct {
	ItemID               string          `json:"item_id"`
	Order                int             `json:"order"`
	Source               VersionIdentity `json:"source"`
	StartTimeMs          int64           `json:"start_time_ms"`
	EndTimeMs            int64           `json:"end_time_ms"`
	BarCount             int             `json:"bar_count"`
	RepeatCount          int             `json:"repeat_count"`
	PreviousClosePresent bool            `json:"previous_close_present"`
	PreviousClose        float64         `json:"previous_close,omitempty"`
	FirstOpen            float64         `json:"first_open"`
	SourceGapRatio       *float64        `json:"source_gap_ratio,omitempty"`
	Bars                 []Bar           `json:"-"`
}

type GenerationPlan struct {
	SchemaVersion     string          `json:"schema_version"`
	AlgorithmVersion  string          `json:"algorithm_version"`
	PrecisionVersion  string          `json:"precision_version"`
	Interval          string          `json:"interval"`
	TargetMarket      string          `json:"target_market"`
	TargetTimezone    string          `json:"target_timezone"`
	CalendarSource    VersionIdentity `json:"calendar_source"`
	CalendarVersion   string          `json:"calendar_version"`
	CalendarHash      string          `json:"calendar_hash"`
	OutputStartTimeMs int64           `json:"output_start_time_ms"`
	TotalOutputBars   int             `json:"total_output_bars"`
	Segments          []SegmentPlan   `json:"segments"`
}

type SegmentInstance struct {
	InstanceID         string   `json:"instance_id"`
	SegmentItemID      string   `json:"segment_item_id"`
	Order              int      `json:"order"`
	RepeatOrdinal      int      `json:"repeat_ordinal"`
	SourceVersionID    uint     `json:"source_version_id"`
	SourceContentHash  string   `json:"source_content_hash"`
	SourceStartTimeMs  int64    `json:"source_start_time_ms"`
	SourceEndTimeMs    int64    `json:"source_end_time_ms"`
	OutputStartOrdinal int      `json:"output_start_ordinal"`
	OutputEndOrdinal   int      `json:"output_end_ordinal"`
	OutputStartTimeMs  int64    `json:"output_start_time_ms"`
	OutputEndTimeMs    int64    `json:"output_end_time_ms"`
	ScaleMultiplier    float64  `json:"scale_multiplier"`
	SourceGapRatio     *float64 `json:"source_gap_ratio,omitempty"`
	ActualGapRatio     float64  `json:"actual_gap_ratio"`
	AnchorMissing      bool     `json:"anchor_missing"`
	AnchorValue        float64  `json:"anchor_value"`
}

type BarLineage struct {
	OutputOrdinal     int    `json:"output_ordinal"`
	OutputOpenTime    int64  `json:"output_open_time"`
	SegmentInstanceID string `json:"segment_instance_id"`
	SourceVersionID   uint   `json:"source_version_id"`
	SourceContentHash string `json:"source_content_hash"`
	SourceOrdinal     int    `json:"source_ordinal"`
	SourceOpenTime    int64  `json:"source_open_time"`
}

type RecompositionResult struct {
	Bars           []Bar             `json:"bars"`
	Instances      []SegmentInstance `json:"instances"`
	Lineage        []BarLineage      `json:"lineage"`
	ContentHash    string            `json:"content_hash"`
	AnchorWarnings int               `json:"anchor_warnings"`
}
