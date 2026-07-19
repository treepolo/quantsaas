package perturbation

import (
	"encoding/json"
	"errors"

	core "quantsaas/internal/perturbation"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/computetask"
)

const (
	GroupSchemaVersion     = "p13-perturbation-group-v1"
	VariantExecutorType    = "p13.perturbation.generate"
	VariantExecutorVersion = "p13-perturbation-generate-executor-v1"
	VariantResultVersion   = "p13-perturbation-generate-result-v1"
	VariantPlanVersion     = "p13-perturbation-variant-plan-v1"
	TestSchemaVersion      = "p13-perturbation-test-v1"
	SubjectSchemaVersion   = "p13-perturbation-subject-v1"
	RunExecutorType        = "p13.perturbation.backtest"
	RunExecutorVersion     = "p13-perturbation-backtest-executor-v1"
	RunResultVersion       = "p13-perturbation-backtest-result-v1"
)

var (
	ErrInvalidSource       = errors.New("invalid_source_bar")
	ErrUnsupportedSource   = errors.New("unsupported_source_kind")
	ErrStalePlan           = errors.New("stale_plan")
	ErrInvalidSeed         = errors.New("invalid_seed")
	ErrInvalidAlpha        = errors.New("invalid_alpha")
	ErrRecipeConflict      = errors.New("recipe_conflict")
	ErrContentMismatch     = errors.New("content_hash_mismatch")
	ErrNotFound            = errors.New("找不到擾動研究資源")
	ErrArchived            = errors.New("擾動研究已封存")
	ErrMissingVariant      = errors.New("missing_variant")
	ErrIncompatibleSubject = errors.New("incompatible_test_subject")
)

type SourceRequest struct {
	InstrumentID string `json:"instrument_id,omitempty"`
	VersionID    uint   `json:"version_id,omitempty"`
	Interval     string `json:"interval"`
	StartTimeMs  int64  `json:"start_time_ms"`
	EndTimeMs    int64  `json:"end_time_ms"`
}

type SourceDescriptor struct {
	InstrumentID            string `json:"instrument_id"`
	DataSource              string `json:"data_source"`
	Symbol                  string `json:"symbol"`
	DisplayName             string `json:"display_name"`
	Interval                string `json:"interval"`
	VersionID               uint   `json:"version_id,omitempty"`
	ContentHash             string `json:"content_hash,omitempty"`
	ArtifactKind            string `json:"artifact_kind"`
	HasPerturbationAncestor bool   `json:"has_perturbation_ancestor"`
	Immutable               bool   `json:"immutable"`
	AvailableStartTimeMs    int64  `json:"available_start_time_ms,omitempty"`
	AvailableEndTimeMs      int64  `json:"available_end_time_ms,omitempty"`
}

type GroupPlanRequest struct {
	Source SourceRequest `json:"source"`
}

type GroupPlan struct {
	SchemaVersion        string           `json:"schema_version"`
	Source               SourceDescriptor `json:"source"`
	ActualStartTimeMs    int64            `json:"actual_start_time_ms"`
	ActualEndTimeMs      int64            `json:"actual_end_time_ms"`
	BarCount             int              `json:"bar_count"`
	PreviousClosePresent bool             `json:"previous_close_present"`
	PreviousClose        float64          `json:"previous_close,omitempty"`
	SourceContentHash    string           `json:"source_content_hash"`
	EstimatedBytes       int64            `json:"estimated_bytes"`
	PlanHash             string           `json:"plan_hash"`
	SourceVersion        string           `json:"source_version"`
	WickWarning          bool             `json:"wick_warning"`
}

type CreateGroupRequest struct {
	PlanRequest GroupPlanRequest `json:"plan_request"`
	PlanHash    string           `json:"plan_hash"`
	Name        string           `json:"name"`
	Notes       string           `json:"notes"`
	Tags        []string         `json:"tags"`
}

type GroupDescriptor struct {
	ID               uint                `json:"id"`
	Name             string              `json:"name"`
	Notes            string              `json:"notes,omitempty"`
	Tags             []string            `json:"tags"`
	AlgorithmVersion string              `json:"algorithm_version"`
	Archived         bool                `json:"archived"`
	Snapshot         SnapshotDescriptor  `json:"snapshot"`
	Variants         []VariantDescriptor `json:"variants,omitempty"`
	CreatedAt        string              `json:"created_at"`
}

type SnapshotDescriptor struct {
	ID                   uint   `json:"id"`
	SourceContentHash    string `json:"source_content_hash"`
	SourceVersionID      uint   `json:"source_version_id"`
	OriginalInstrumentID string `json:"original_instrument_id"`
	OriginalDataSource   string `json:"original_data_source"`
	OriginalSymbol       string `json:"original_symbol"`
	Interval             string `json:"interval"`
	StartTimeMs          int64  `json:"start_time_ms"`
	EndTimeMs            int64  `json:"end_time_ms"`
	BarCount             int    `json:"bar_count"`
	Status               string `json:"status"`
}

type MetadataRequest struct {
	Name  string   `json:"name"`
	Notes string   `json:"notes"`
	Tags  []string `json:"tags"`
}

type VariantPlanRequest struct {
	Seeds     []string `json:"seeds"`
	SeedCount int      `json:"seed_count,omitempty"`
	Alphas    []string `json:"alphas"`
}

type VariantPlan struct {
	SchemaVersion        string          `json:"schema_version"`
	GroupID              uint            `json:"group_id"`
	PlanHash             string          `json:"plan_hash"`
	Seeds                []string        `json:"seeds"`
	Alphas               []string        `json:"alphas"`
	Recipes              []VariantRecipe `json:"recipes"`
	UniqueVariants       int             `json:"unique_variants"`
	ExistingVariants     int             `json:"existing_variants"`
	PendingVariants      int             `json:"pending_variants"`
	TotalOutputBars      int64           `json:"total_output_bars"`
	EstimatedBytes       int64           `json:"estimated_bytes"`
	EstimatedSeconds     *float64        `json:"estimated_seconds,omitempty"`
	RequiresConfirmation bool            `json:"requires_confirmation"`
}

type VariantRecipe struct {
	Seed       string `json:"seed"`
	Alpha      string `json:"alpha"`
	RecipeHash string `json:"recipe_hash"`
	VariantID  uint   `json:"variant_id,omitempty"`
	Reusable   bool   `json:"reusable"`
}

type StartVariantsRequest struct {
	PlanRequest      VariantPlanRequest `json:"plan_request"`
	PlanHash         string             `json:"plan_hash"`
	ConfirmSoftLimit bool               `json:"confirm_soft_limit"`
}

type VariantTask struct {
	Plan    VariantPlan                 `json:"plan"`
	Task    *computetask.TaskDescriptor `json:"task"`
	Preview computetask.PlanPreview     `json:"task_preview"`
}

type VariantDescriptor struct {
	ID                   uint                  `json:"id"`
	GroupID              uint                  `json:"group_id"`
	Seed                 string                `json:"seed"`
	Alpha                string                `json:"alpha"`
	RecipeHash           string                `json:"recipe_hash"`
	OutputVersionID      uint                  `json:"output_version_id"`
	OutputInstrumentID   string                `json:"output_instrument_id"`
	GeneratedContentHash string                `json:"generated_content_hash,omitempty"`
	Status               string                `json:"status"`
	IntegrityStatus      string                `json:"integrity_status"`
	BarCount             int                   `json:"bar_count"`
	Deviation            core.DeviationSummary `json:"deviation"`
	ComputeTaskID        *uint                 `json:"compute_task_id,omitempty"`
	Archived             bool                  `json:"archived"`
	ErrorCode            string                `json:"error_code,omitempty"`
	ErrorMessage         string                `json:"error_message,omitempty"`
	CreatedAt            string                `json:"created_at"`
}

type VariantExecutionInput struct {
	SchemaVersion string `json:"schema_version"`
	VariantID     uint   `json:"variant_id"`
	RecipeHash    string `json:"recipe_hash"`
}

type VariantExecutionResult struct {
	SchemaVersion string `json:"schema_version"`
	VariantID     uint   `json:"variant_id"`
	VersionID     uint   `json:"version_id"`
	ContentHash   string `json:"content_hash"`
}

type BacktestSettings struct {
	StrategyID            string   `json:"strategy_id"`
	ExecutionMode         string   `json:"execution_mode"`
	StartTimeMs           int64    `json:"start_time_ms"`
	EndTimeMs             int64    `json:"end_time_ms"`
	InitialCapital        *float64 `json:"initial_capital,omitempty"`
	MonthlyDCA            *float64 `json:"monthly_dca,omitempty"`
	FeeRate               *float64 `json:"fee_rate,omitempty"`
	SpreadRate            *float64 `json:"spread_rate,omitempty"`
	LongTermFilterEnabled *bool    `json:"long_term_filter_enabled,omitempty"`
	LongTermFilterMonths  int      `json:"long_term_filter_months,omitempty"`
}

type SubjectRequest struct {
	SourceKind string `json:"source_kind"`
	SourceID   uint   `json:"source_id"`
}

type CreateTestRequest struct {
	Name     string           `json:"name"`
	Notes    string           `json:"notes"`
	Tags     []string         `json:"tags"`
	GroupID  uint             `json:"group_id"`
	Subjects []SubjectRequest `json:"subjects"`
	Backtest BacktestSettings `json:"backtest"`
}

type TestPlanRequest struct{ CreateTestRequest }

type TestPlan struct {
	SchemaVersion   string              `json:"schema_version"`
	PlanHash        string              `json:"plan_hash"`
	TestSpecHash    string              `json:"test_spec_hash"`
	GroupID         uint                `json:"group_id"`
	SubjectCount    int                 `json:"subject_count"`
	VariantCount    int                 `json:"variant_count"`
	PlannedRuns     int                 `json:"planned_runs"`
	CacheHits       int                 `json:"cache_hits"`
	PendingRuns     int                 `json:"pending_runs"`
	MissingVariants []VariantRecipe     `json:"missing_variants"`
	Subjects        []SubjectDescriptor `json:"subjects"`
}

type SubjectDescriptor struct {
	ID            uint            `json:"id,omitempty"`
	Ordinal       int             `json:"ordinal"`
	SourceKind    string          `json:"source_kind"`
	SourceID      uint            `json:"source_id"`
	SourceVersion string          `json:"source_version"`
	SubjectHash   string          `json:"subject_hash"`
	AdoptionUnit  json.RawMessage `json:"adoption_unit"`
	Dynamic       bool            `json:"dynamic"`
	CandidateID   *uint           `json:"candidate_id,omitempty"`
}

type StartTestRequest struct {
	CreateTestRequest
	PlanHash string `json:"plan_hash"`
}

type StartBatchRequest struct {
	Seeds            []string `json:"seeds,omitempty"`
	Alphas           []string `json:"alphas,omitempty"`
	PlanHash         string   `json:"plan_hash"`
	ConfirmSoftLimit bool     `json:"confirm_soft_limit"`
}

type BatchPlanRequest struct {
	Seeds  []string `json:"seeds,omitempty"`
	Alphas []string `json:"alphas,omitempty"`
}

type BatchPlan struct {
	SchemaVersion        string          `json:"schema_version"`
	TestID               uint            `json:"test_id"`
	PlanHash             string          `json:"plan_hash"`
	ManifestHash         string          `json:"manifest_hash"`
	Seeds                []string        `json:"seeds"`
	Alphas               []string        `json:"alphas"`
	VariantIDs           []uint          `json:"variant_ids"`
	SubjectCount         int             `json:"subject_count"`
	DatasetCount         int             `json:"dataset_count"`
	PlannedRuns          int             `json:"planned_runs"`
	ExistingRuns         int             `json:"existing_runs"`
	PendingRuns          int             `json:"pending_runs"`
	MissingVariants      []VariantRecipe `json:"missing_variants"`
	RequiresConfirmation bool            `json:"requires_confirmation"`
}

type BatchTask struct {
	BatchID uint                        `json:"batch_id"`
	Plan    BatchPlan                   `json:"plan"`
	Task    *computetask.TaskDescriptor `json:"task"`
	Preview computetask.PlanPreview     `json:"task_preview"`
}

type AnalysisSnapshotDescriptor struct {
	ID                uint                             `json:"id"`
	TestID            uint                             `json:"test_id"`
	SnapshotKey       string                           `json:"snapshot_key"`
	AnalysisSetHash   string                           `json:"analysis_set_hash"`
	StatisticsVersion string                           `json:"statistics_version"`
	Completeness      string                           `json:"completeness"`
	PlannedCount      int                              `json:"planned_count"`
	ValidCount        int                              `json:"valid_count"`
	FailedCount       int                              `json:"failed_count"`
	MissingCount      int                              `json:"missing_count"`
	ContentHash       string                           `json:"content_hash"`
	Summary           json.RawMessage                  `json:"summary,omitempty"`
	Metrics           []MetricSummaryDescriptor        `json:"metrics,omitempty"`
	Qualifications    []QualificationSummaryDescriptor `json:"qualifications,omitempty"`
	CreatedAt         string                           `json:"created_at"`
}

type MetricSummaryDescriptor struct {
	SubjectID    uint                  `json:"subject_id"`
	Alpha        string                `json:"alpha"`
	MetricKey    string                `json:"metric_key"`
	PlannedCount int                   `json:"planned_count"`
	ValidCount   int                   `json:"valid_count"`
	FailedCount  int                   `json:"failed_count"`
	MissingCount int                   `json:"missing_count"`
	Statistics   core.DescriptiveStats `json:"statistics"`
}

type QualificationSummaryDescriptor struct {
	SubjectID          uint   `json:"subject_id"`
	Alpha              string `json:"alpha"`
	ValidCount         int    `json:"valid_count"`
	Qualified          int    `json:"qualified"`
	ReturnFailedOnly   int    `json:"return_failed_only"`
	DrawdownFailedOnly int    `json:"drawdown_failed_only"`
	BothFailed         int    `json:"both_failed"`
}

type TestDescriptor struct {
	ID               uint                `json:"id"`
	GroupID          uint                `json:"group_id"`
	Name             string              `json:"name"`
	Notes            string              `json:"notes,omitempty"`
	Tags             []string            `json:"tags"`
	Status           string              `json:"status"`
	TestSpecHash     string              `json:"test_spec_hash"`
	Backtest         BacktestSettings    `json:"backtest"`
	Subjects         []SubjectDescriptor `json:"subjects,omitempty"`
	LatestSnapshotID *uint               `json:"latest_snapshot_id,omitempty"`
	Archived         bool                `json:"archived"`
	CreatedAt        string              `json:"created_at"`
}

type RunExecutionInput struct {
	SchemaVersion string `json:"schema_version"`
	RunID         uint   `json:"run_id"`
}

type RunExecutionResult struct {
	SchemaVersion    string `json:"schema_version"`
	RunID            uint   `json:"run_id"`
	BacktestResultID uint   `json:"backtest_result_id"`
	ContentHash      string `json:"content_hash"`
}

type RunDescriptor struct {
	ID                        uint            `json:"id"`
	TestID                    uint            `json:"test_id"`
	BatchID                   uint            `json:"batch_id"`
	SubjectID                 uint            `json:"subject_id"`
	DatasetVersionID          uint            `json:"dataset_version_id"`
	DatasetContentHash        string          `json:"dataset_content_hash"`
	Alpha                     string          `json:"alpha"`
	Seed                      string          `json:"seed"`
	Status                    string          `json:"status"`
	BacktestResultID          *uint           `json:"backtest_result_id,omitempty"`
	BacktestResultContentHash string          `json:"backtest_result_content_hash,omitempty"`
	Reused                    bool            `json:"reused"`
	Metrics                   json.RawMessage `json:"metrics,omitempty"`
	PerformanceReportID       *uint           `json:"performance_report_id,omitempty"`
	ErrorCode                 string          `json:"error_code,omitempty"`
	ErrorMessage              string          `json:"error_message,omitempty"`
}

type RunInputSnapshot struct {
	Backtest      backtest.CreateRequest `json:"backtest"`
	Dynamic       bool                   `json:"dynamic"`
	ExecutorType  string                 `json:"executor_type,omitempty"`
	ExecutorInput json.RawMessage        `json:"executor_input,omitempty"`
}
