package klineinverse

import (
	"encoding/json"
	"errors"

	compute "quantsaas/internal/compute"
	core "quantsaas/internal/klineinverse"
	"quantsaas/internal/saas/computetask"
	"quantsaas/internal/strategies/sigmoiddca"
)

const (
	StudySchemaVersion         = "p12-study-v1"
	CalibrationSchemaVersion   = "p12-calibration-v1"
	BatchSchemaVersion         = "p12-batch-v1"
	PathSchemaVersion          = "p12-path-v1"
	EvaluationSchemaVersion    = "p12-evaluation-v1"
	SnapshotSchemaVersion      = "p12-archive-snapshot-v1"
	StatisticsVersion          = "p12-statistics-v1"
	CalibrationExecutorType    = "p12.kline-inverse.calibration"
	CalibrationExecutorVersion = "p12-calibration-executor-v1"
	CalibrationResultVersion   = "p12-calibration-result-v1"
	SearchExecutorType         = "p12.kline-inverse.search"
	SearchExecutorVersion      = "p12-search-executor-v1"
	SearchResultVersion        = "p12-search-result-v1"
)

var (
	ErrInvalidRequest = errors.New("P12 K 線樣貌反推設定無效")
	ErrNotFound       = errors.New("找不到 P12 K 線樣貌反推資源")
	ErrPlanStale      = errors.New("P12 計算計畫已過期，請重新預覽")
	ErrDynamicSource  = errors.New("P12 已確認規格未定義 K 動態模型如何套用合成行情")
)

type CalibrationSource struct {
	InstrumentID string `json:"instrument_id"`
	DataSource   string `json:"data_source"`
	Symbol       string `json:"symbol"`
	Interval     string `json:"interval"`
	StartTimeMs  int64  `json:"start_time_ms"`
	EndTimeMs    int64  `json:"end_time_ms"`
}

type CreateDraftRequest struct {
	Name               string              `json:"name"`
	Notes              string              `json:"notes"`
	Tags               []string            `json:"tags"`
	GenomeID           uint                `json:"genome_id,omitempty"`
	CandidateID        uint                `json:"candidate_id,omitempty"`
	BacktestResultID   uint                `json:"backtest_result_id,omitempty"`
	InstrumentID       string              `json:"instrument_id"`
	DataSource         string              `json:"data_source"`
	Symbol             string              `json:"symbol"`
	Interval           string              `json:"interval"`
	ExecutionMode      string              `json:"execution_mode"`
	EvaluationStartMs  int64               `json:"evaluation_start_ms"`
	EvaluationLength   int                 `json:"evaluation_length"`
	CalibrationSources []CalibrationSource `json:"calibration_sources"`
	FinalBounds        *core.Bounds        `json:"final_bounds,omitempty"`
	InitialCapital     float64             `json:"initial_capital"`
	FeeRate            float64             `json:"fee_rate"`
	SlippageRate       float64             `json:"slippage_rate"`
	InitialBudget      int                 `json:"initial_budget"`
	CellCount          int                 `json:"cell_count"`
	ParentCapacity     int                 `json:"parent_capacity"`
	RootSeed           int64               `json:"root_seed"`
	MutationAmplitude  float64             `json:"mutation_amplitude"`
}

type AvailabilityRequest struct {
	InstrumentID     string `form:"instrument_id" json:"instrument_id"`
	DataSource       string `form:"data_source" json:"data_source"`
	Symbol           string `form:"symbol" json:"symbol"`
	Interval         string `form:"interval" json:"interval"`
	EvaluationLength int    `form:"evaluation_length" json:"evaluation_length"`
}

type AvailabilityDescriptor struct {
	AvailableStartMs          int64 `json:"available_start_ms"`
	AvailableEndMs            int64 `json:"available_end_ms"`
	EarliestEvaluationStartMs int64 `json:"earliest_evaluation_start_ms"`
	LatestEvaluationStartMs   int64 `json:"latest_evaluation_start_ms"`
	WarmupLength              int   `json:"warmup_length"`
	EvaluationLength          int   `json:"evaluation_length"`
	BarCount                  int64 `json:"bar_count"`
}

type StudyCanonical struct {
	SchemaVersion         string              `json:"schema_version"`
	SourceKind            string              `json:"source_kind"`
	SourceID              uint                `json:"source_id"`
	SourceVersion         string              `json:"source_version"`
	SourceContentHash     string              `json:"source_content_hash"`
	Parameters            sigmoiddca.Params   `json:"parameters"`
	ParameterHash         string              `json:"parameter_hash"`
	InstrumentID          string              `json:"instrument_id"`
	DataSource            string              `json:"data_source"`
	Symbol                string              `json:"symbol"`
	Interval              string              `json:"interval"`
	ExecutionMode         string              `json:"execution_mode"`
	Dates                 []int64             `json:"dates"`
	WarmupLength          int                 `json:"warmup_length"`
	EvaluationLength      int                 `json:"evaluation_length"`
	EvaluationStartMs     int64               `json:"evaluation_start_ms"`
	CalibrationSources    []CalibrationSource `json:"calibration_sources"`
	CalibrationSourceHash string              `json:"calibration_source_hash"`
	ObservedBounds        core.Bounds         `json:"observed_bounds"`
	FinalBounds           core.Bounds         `json:"final_bounds"`
	BoundsHash            string              `json:"bounds_hash"`
	InitialCapital        float64             `json:"initial_capital"`
	FeeRate               float64             `json:"fee_rate"`
	SlippageRate          float64             `json:"slippage_rate"`
	MonthlyContribution   float64             `json:"monthly_contribution"`
	InitialBudget         int                 `json:"initial_budget"`
	CellCount             int                 `json:"cell_count"`
	ParentCapacity        int                 `json:"parent_capacity"`
	RootSeed              int64               `json:"root_seed"`
	MutationAmplitude     float64             `json:"mutation_amplitude"`
	CoordinateVersion     string              `json:"coordinate_version"`
	FeatureVersion        string              `json:"feature_version"`
	DistanceVersion       string              `json:"distance_version"`
	CVTVersion            string              `json:"cvt_version"`
	SearchVersion         string              `json:"search_version"`
	VariationVersion      string              `json:"variation_version"`
	StateVersion          string              `json:"state_version"`
	RNGVersion            string              `json:"rng_version"`
}

type StudyDescriptor struct {
	ID                uint              `json:"id"`
	Name              string            `json:"name"`
	Notes             string            `json:"notes"`
	Tags              []string          `json:"tags"`
	Status            string            `json:"status"`
	CurrentStage      string            `json:"current_stage"`
	StudyHash         string            `json:"study_hash"`
	SourceKind        string            `json:"source_kind"`
	SourceID          uint              `json:"source_id"`
	SourceVersion     string            `json:"source_version"`
	SourceContentHash string            `json:"source_content_hash"`
	InstrumentID      string            `json:"instrument_id"`
	DataSource        string            `json:"data_source"`
	Symbol            string            `json:"symbol"`
	Interval          string            `json:"interval"`
	ExecutionMode     string            `json:"execution_mode"`
	WarmupLength      int               `json:"warmup_length"`
	EvaluationLength  int               `json:"evaluation_length"`
	EvaluationStartMs int64             `json:"evaluation_start_ms"`
	InitialBudget     int               `json:"initial_budget"`
	CellCount         int               `json:"cell_count"`
	ParentCapacity    int               `json:"parent_capacity"`
	RootSeed          int64             `json:"root_seed"`
	ObservedBounds    core.Bounds       `json:"observed_bounds"`
	FinalBounds       core.Bounds       `json:"final_bounds"`
	Canonical         StudyCanonical    `json:"canonical"`
	CurrentSnapshotID *uint             `json:"current_snapshot_id,omitempty"`
	Archived          bool              `json:"archived"`
	Batches           []BatchDescriptor `json:"batches,omitempty"`
	CreatedAt         string            `json:"created_at"`
	UpdatedAt         string            `json:"updated_at"`
}

type BatchDescriptor struct {
	ID                 uint   `json:"id"`
	Ordinal            int    `json:"ordinal"`
	BatchType          string `json:"batch_type"`
	Budget             int    `json:"budget"`
	Status             string `json:"status"`
	CompletedCount     int    `json:"completed_count"`
	CacheHitCount      int    `json:"cache_hit_count"`
	ErrorCount         int    `json:"error_count"`
	RNGStart           int64  `json:"rng_start"`
	RNGEnd             int64  `json:"rng_end"`
	CheckpointPosition int64  `json:"checkpoint_position"`
	ComputeTaskID      *uint  `json:"compute_task_id,omitempty"`
	ManifestHash       string `json:"manifest_hash"`
	CompatibilityHash  string `json:"compatibility_hash"`
	ErrorMessage       string `json:"error_message,omitempty"`
}

type PlanResponse struct {
	Study                StudyDescriptor                  `json:"study"`
	Plan                 computetask.CompositePlanPreview `json:"plan"`
	FeatureCalculations  int                              `json:"feature_calculations"`
	BacktestEvaluations  int                              `json:"backtest_evaluations"`
	StorageEstimateBytes int64                            `json:"storage_estimate_bytes"`
}

type StartRequest struct {
	PlanKey          string `json:"plan_key"`
	ConfirmSoftLimit bool   `json:"confirm_soft_limit"`
}

type ExtensionPlanRequest struct {
	AdditionalBudget int `json:"additional_budget"`
}

type ProbePlanRequest struct {
	AnchorPathID uint     `json:"anchor_path_id"`
	Budget       int      `json:"budget"`
	Operations   []string `json:"operations"`
	Scope        string   `json:"scope"`
	MinLength    int      `json:"min_length"`
	MaxLength    int      `json:"max_length"`
	Amplitude    float64  `json:"amplitude"`
}

type BatchPlanResponse struct {
	BatchID             uint                    `json:"batch_id"`
	BatchType           string                  `json:"batch_type"`
	Plan                computetask.PlanPreview `json:"plan"`
	ManifestHash        string                  `json:"manifest_hash"`
	CompatibilityHash   string                  `json:"compatibility_hash"`
	BacktestEvaluations int                     `json:"backtest_evaluations"`
}

type BatchStartRequest struct {
	BatchID          uint   `json:"batch_id"`
	PlanKey          string `json:"plan_key"`
	ConfirmSoftLimit bool   `json:"confirm_soft_limit"`
}

type CalibrationExecutionInput struct {
	StudyID uint `json:"study_id"`
	BatchID uint `json:"batch_id"`
}
type SearchExecutionInput struct {
	StudyID uint `json:"study_id"`
	BatchID uint `json:"batch_id"`
}

type ExecutorResult struct {
	SchemaVersion string `json:"schema_version"`
	StudyID       uint   `json:"study_id"`
	BatchID       uint   `json:"batch_id"`
	SnapshotID    uint   `json:"snapshot_id,omitempty"`
	ContentHash   string `json:"content_hash"`
}

type Overview struct {
	StudyID             uint                          `json:"study_id"`
	SnapshotID          uint                          `json:"snapshot_id"`
	Status              string                        `json:"status"`
	EvaluatedCount      int                           `json:"evaluated_count"`
	TouchedCellCount    int                           `json:"touched_cell_count"`
	SearchReach         float64                       `json:"search_reach"`
	ACellCount          int                           `json:"a_cell_count"`
	BCellCount          int                           `json:"b_cell_count"`
	ACoverage           float64                       `json:"a_coverage"`
	BCoverage           float64                       `json:"b_coverage"`
	ACellPerTouched     float64                       `json:"a_cell_per_touched"`
	BCellPerTouched     float64                       `json:"b_cell_per_touched"`
	StateCounts         map[string]int                `json:"state_counts"`
	FeatureStatistics   map[string]map[string]float64 `json:"feature_statistics"`
	DistanceStatistics  map[string]map[string]float64 `json:"distance_statistics"`
	QRelativeStatistics map[string]float64            `json:"q_relative_statistics"`
	QAbsoluteStatistics map[string]float64            `json:"q_absolute_statistics"`
	PermanentPathCount  int                           `json:"permanent_path_count"`
	LineageEdgeCount    int                           `json:"lineage_edge_count"`
	CacheHitCount       int                           `json:"cache_hit_count"`
	ErrorCount          int                           `json:"error_count"`
}

type CellSummary struct {
	CellIndex             int         `json:"cell_index"`
	EvaluationCount       int         `json:"evaluation_count"`
	ACount                int         `json:"a_count"`
	BCount                int         `json:"b_count"`
	ActiveParetoCount     int         `json:"active_pareto_count"`
	BestQRelative         float64     `json:"best_q_relative"`
	MedianQRelative       float64     `json:"median_q_relative"`
	BestQAbsolute         float64     `json:"best_q_absolute"`
	MedianQAbsolute       float64     `json:"median_q_absolute"`
	MedianNearestDistance float64     `json:"median_nearest_distance"`
	Features              [20]float64 `json:"features"`
}

type MapResponse struct {
	StudyID    uint          `json:"study_id"`
	SnapshotID uint          `json:"snapshot_id"`
	AxisX      string        `json:"axis_x"`
	AxisY      string        `json:"axis_y"`
	Target     string        `json:"target"`
	Color      string        `json:"color"`
	Cells      []CellSummary `json:"cells"`
	Points     []MapPoint    `json:"points"`
}

// MapPoint 是每筆評估的精簡投影資料；OHLC 僅在使用者選取結果後才按需讀取。
type MapPoint struct {
	EvaluationID uint        `json:"evaluation_id"`
	PathID       uint        `json:"path_id"`
	OutcomeState string      `json:"outcome_state"`
	PassA        bool        `json:"pass_a"`
	PassB        bool        `json:"pass_b"`
	QRelative    float64     `json:"q_rel"`
	QAbsolute    float64     `json:"q_abs"`
	X            float64     `json:"x"`
	Y            float64     `json:"y"`
}

type PathSummary struct {
	ID               uint    `json:"id"`
	PathHash         string  `json:"path_hash"`
	EvaluationID     uint    `json:"evaluation_id"`
	CellIndex        int     `json:"cell_index"`
	OutcomeState     string  `json:"outcome_state"`
	PassA            bool    `json:"pass_a"`
	PassB            bool    `json:"pass_b"`
	QRelative        float64 `json:"q_rel"`
	QAbsolute        float64 `json:"q_abs"`
	BacktestResultID uint    `json:"backtest_result_id"`
	PermanentReason  string  `json:"permanent_reason"`
}

type PathPage struct {
	Items      []PathSummary `json:"items"`
	Page       int           `json:"page"`
	PageSize   int           `json:"page_size"`
	Total      int64         `json:"total"`
	TotalPages int           `json:"total_pages"`
}

type PathDetail struct {
	PathSummary
	WarmupLength         int             `json:"warmup_length"`
	EvaluationLength     int             `json:"evaluation_length"`
	Coordinates          json.RawMessage `json:"coordinates"`
	OHLC                 json.RawMessage `json:"ohlc"`
	Features             json.RawMessage `json:"features"`
	PerformanceReportIDs []uint          `json:"performance_report_ids"`
}

type LineageEdgeDescriptor struct {
	ID                 uint     `json:"id"`
	ChildPathID        uint     `json:"child_path_id"`
	ParentPathID       *uint    `json:"parent_path_id,omitempty"`
	RequestedOperation string   `json:"requested_operation"`
	ActualOperation    string   `json:"actual_operation"`
	ChangedStart       int      `json:"changed_start"`
	ChangedLength      int      `json:"changed_length"`
	ChangedChannels    []string `json:"changed_channels"`
	Amplitude          float64  `json:"amplitude"`
	BatchID            uint     `json:"batch_id"`
}

type BoundaryPoint struct {
	ChildPathID   uint          `json:"child_path_id"`
	Operation     string        `json:"operation"`
	Distance      core.Distance `json:"distance"`
	QRelative     float64       `json:"q_rel"`
	QAbsolute     float64       `json:"q_abs"`
	State         string        `json:"state"`
	ChangedStart  int           `json:"changed_start"`
	ChangedLength int           `json:"changed_length"`
	Amplitude     float64       `json:"amplitude"`
	BatchID       uint          `json:"batch_id"`
}
type PassStep struct {
	Radius float64 `json:"radius"`
	Passed int     `json:"passed"`
	Total  int     `json:"total"`
	Rate   float64 `json:"rate"`
}
type BoundaryResponse struct {
	Anchor          PathSummary     `json:"anchor"`
	Points          []BoundaryPoint `json:"points"`
	NearestFailureA *float64        `json:"nearest_failure_a,omitempty"`
	NearestFailureB *float64        `json:"nearest_failure_b,omitempty"`
	PassCurveA      []PassStep      `json:"pass_curve_a"`
	PassCurveB      []PassStep      `json:"pass_curve_b"`
}

type ComparisonDescriptor struct {
	StudyID         uint     `json:"study_id"`
	SnapshotID      uint     `json:"snapshot_id"`
	SnapshotVersion string   `json:"snapshot_version"`
	ContentHash     string   `json:"content_hash"`
	SourceKind      string   `json:"source_kind"`
	SourceID        uint     `json:"source_id"`
	ParameterHash   string   `json:"parameter_hash"`
	LazyBlocks      []string `json:"lazy_blocks"`
	ReadOnly        bool     `json:"read_only"`
}

func executorDescriptor(kind string) compute.ExecutorDescriptor {
	if kind == CalibrationExecutorType {
		return compute.ExecutorDescriptor{Type: kind, Version: CalibrationExecutorVersion, ResultSchemaVersion: CalibrationResultVersion}
	}
	return compute.ExecutorDescriptor{Type: kind, Version: SearchExecutorVersion, ResultSchemaVersion: SearchResultVersion}
}
