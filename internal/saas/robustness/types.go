package robustness

import (
	"encoding/json"
	"errors"

	core "quantsaas/internal/robustness"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/computetask"
)

const (
	StudySettingVersion   = "p08-study-setting-v1"
	StudySchemaVersion    = "p08-study-v1"
	PointSchemaVersion    = "p08-point-execution-v2"
	SnapshotSchemaVersion = "p08-snapshot-v1"
	PointExecutorType     = "p08.robustness.backtest-point"
	PointExecutorVersion  = "p08-point-executor-v2"
	PointResultVersion    = "p08-point-result-v2"
)

const (
	ModeOneDimensional   = "one_dimensional"
	ModeTwoDimensional   = "two_dimensional"
	ModeMultidimensional = "multidimensional"
	ModeImported         = "imported_evaluations"
)

var (
	ErrStudyNotFound  = errors.New("找不到參數穩健分析")
	ErrInvalidRequest = errors.New("參數穩健分析設定無效")
	ErrStudyNotReady  = errors.New("參數穩健分析尚無可用的實測點")
)

type BacktestSettings struct {
	InstrumentID          string   `json:"instrument_id"`
	DataSource            string   `json:"data_source"`
	MarketDataVersionID   uint     `json:"market_data_version_id,omitempty"`
	MarketDataContentHash string   `json:"market_data_content_hash,omitempty"`
	ExecutionMode         string   `json:"execution_mode"`
	StartTimeMs           int64    `json:"start_time_ms"`
	EndTimeMs             int64    `json:"end_time_ms"`
	Symbol                string   `json:"symbol"`
	Interval              string   `json:"interval"`
	InitialCapital        *float64 `json:"initial_capital,omitempty"`
	MonthlyDCA            *float64 `json:"monthly_dca,omitempty"`
	FeeRate               *float64 `json:"fee_rate,omitempty"`
	SpreadRate            *float64 `json:"spread_rate,omitempty"`
	LongTermFilterEnabled *bool    `json:"long_term_filter_enabled,omitempty"`
	LongTermFilterMonths  int      `json:"long_term_filter_months,omitempty"`
}

type CreateStudyRequest struct {
	Name             string             `json:"name"`
	Mode             string             `json:"mode"`
	GenomeID         uint               `json:"genome_id"`
	Axes             []string           `json:"axes"`
	Radius           int                `json:"radius"`
	Radii            []int              `json:"radii"`
	Metric           core.MetricName    `json:"metric"`
	CustomSteps      map[string]float64 `json:"custom_steps,omitempty"`
	SampleCount      int                `json:"sample_count,omitempty"`
	SampleOffset     int                `json:"sample_offset,omitempty"`
	ConfirmSoftLimit bool               `json:"confirm_soft_limit,omitempty"`
	Backtest         BacktestSettings   `json:"backtest"`
}

type StudySetting struct {
	Version            string             `json:"version"`
	Request            CreateStudyRequest `json:"request"`
	BaseParameterHash  string             `json:"base_parameter_hash"`
	ParameterSpaceHash string             `json:"parameter_space_hash"`
	SamplingVersion    string             `json:"sampling_version,omitempty"`
}

type PointExecutionInput struct {
	SchemaVersion string                 `json:"schema_version"`
	Backtest      backtest.CreateRequest `json:"backtest"`
}

type PointExecutionResult struct {
	SchemaVersion             string               `json:"schema_version"`
	BacktestResultID          uint                 `json:"backtest_result_id"`
	BacktestResultVersion     string               `json:"backtest_result_version"`
	BacktestResultContentHash string               `json:"backtest_result_content_hash"`
	Metrics                   core.RelativeMetrics `json:"metrics"`
	ReusedBacktest            bool                 `json:"reused_backtest"`
}

type CreateStudyResponse struct {
	Study   StudyDescriptor             `json:"study"`
	Preview computetask.PlanPreview     `json:"preview"`
	Task    *computetask.TaskDescriptor `json:"task,omitempty"`
}

type StudyDescriptor struct {
	ID                  uint                   `json:"id"`
	Name                string                 `json:"name"`
	Mode                string                 `json:"mode"`
	Status              string                 `json:"status"`
	StudyKey            string                 `json:"study_key"`
	SettingVersion      string                 `json:"setting_version"`
	SettingHash         string                 `json:"setting_hash"`
	Settings            json.RawMessage        `json:"settings,omitempty"`
	SpaceVersion        string                 `json:"space_version"`
	SpaceHash           string                 `json:"space_hash"`
	ParameterSpace      core.ParameterSpace    `json:"parameter_space"`
	CenterPointKey      string                 `json:"center_point_key"`
	SourceGenomeID      *uint                  `json:"source_genome_id,omitempty"`
	ComputeTaskID       *uint                  `json:"compute_task_id,omitempty"`
	ExpectedPointCount  int                    `json:"expected_point_count"`
	ActualPointCount    int                    `json:"actual_point_count"`
	PredictedPointCount int                    `json:"predicted_point_count"`
	Points              []core.EvaluationPoint `json:"points,omitempty"`
	LatestAnalysis      *AnalysisDescriptor    `json:"latest_analysis,omitempty"`
	CreatedAt           string                 `json:"created_at"`
	CompletedAt         string                 `json:"completed_at,omitempty"`
}

type AnalyzeRequest struct {
	Metric core.MetricName `json:"metric"`
	Radii  []int           `json:"radii"`
}

type AnalysisDescriptor struct {
	ID           uint                `json:"id"`
	AnalysisKey  string              `json:"analysis_key"`
	PointSetHash string              `json:"point_set_hash"`
	SettingsHash string              `json:"settings_hash"`
	Metric       core.MetricName     `json:"metric"`
	Radii        []int               `json:"radii"`
	ContentHash  string              `json:"content_hash"`
	Result       core.AnalysisResult `json:"result"`
	CreatedAt    string              `json:"created_at"`
}

type ImportPoint struct {
	ID                 string             `json:"id"`
	Kind               core.PointKind     `json:"kind"`
	Coordinates        []int              `json:"coordinates"`
	Parameters         map[string]float64 `json:"parameters"`
	BacktestResultID   uint               `json:"backtest_result_id,omitempty"`
	SourceStage        string             `json:"source_stage,omitempty"`
	SamplingBatch      string             `json:"sampling_batch,omitempty"`
	PredictionMetadata json.RawMessage    `json:"prediction_metadata,omitempty"`
}

type ImportStudyRequest struct {
	Name                string              `json:"name"`
	ResearchSettingID   string              `json:"research_setting_id"`
	ResearchSettingHash string              `json:"research_setting_hash"`
	ParameterSpace      core.ParameterSpace `json:"parameter_space"`
	CenterPointKey      string              `json:"center_point_key,omitempty"`
	Points              []ImportPoint       `json:"points"`
}
