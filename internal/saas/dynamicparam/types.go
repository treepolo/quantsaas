package dynamicparam

import (
	"encoding/json"
	"errors"

	core "quantsaas/internal/dynamicparam"
	"quantsaas/internal/quant"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/computetask"
)

const (
	StudySettingVersion        = "p09-study-setting-v1"
	StudySchemaVersion         = "p09-study-v1"
	TrainInputVersion          = "p09-train-input-v1"
	TrainExecutorType          = "p09-model-training"
	TrainExecutorVersion       = "p09-model-training-v1"
	TrainResultVersion         = "p09-model-training-result-v1"
	MaterializeInputVersion    = "p09-materialize-input-v1"
	MaterializeExecutorType    = "p09-materialize-and-backtest"
	MaterializeExecutorVersion = "p09-materialize-and-backtest-v1"
	MaterializeResultVersion   = "p09-materialize-result-v1"
)

const (
	StudyStatusPlanned                 = "planned"
	StudyStatusTraining                = "training"
	StudyStatusAwaitingMaterialization = "awaiting_materialization"
	StudyStatusMaterializing           = "materializing"
	StudyStatusCompleted               = "completed"
	StudyStatusPartial                 = "partial"
	StudyStatusFailed                  = "failed"
	StudyStatusCancelled               = "cancelled"
)

var (
	ErrInvalidRequest = errors.New("P09 動態模型研究設定無效")
	ErrNotFound       = errors.New("找不到 P09 動態模型研究")
)

type CreateStudyRequest struct {
	Name                  string             `json:"name"`
	GenomeID              uint               `json:"genome_id"`
	Route                 string             `json:"route"`
	Lookbacks             []int              `json:"lookbacks"`
	Folds                 int                `json:"folds"`
	MinimumTrain          int                `json:"minimum_train"`
	InstrumentID          string             `json:"instrument_id"`
	DataSource            string             `json:"data_source"`
	Symbol                string             `json:"symbol"`
	Interval              string             `json:"interval"`
	ExecutionMode         string             `json:"execution_mode"`
	TrainStartTimeMs      int64              `json:"train_start_time_ms"`
	TrainEndTimeMs        int64              `json:"train_end_time_ms"`
	StateRules            core.StateRules    `json:"state_rules"`
	Policy                core.DynamicPolicy `json:"policy"`
	RegionRule            core.RegionRule    `json:"region_rule"`
	LongTermFilterEnabled bool               `json:"long_term_filter_enabled"`
	LongTermFilterMonths  int                `json:"long_term_filter_months"`
	ActivityKappa         float64            `json:"activity_kappa"`
	ConfirmSoftLimit      bool               `json:"confirm_soft_limit"`
}

type StudySetting struct {
	Version           string              `json:"version"`
	Request           CreateStudyRequest  `json:"request"`
	DatasetHash       string              `json:"dataset_hash"`
	Training          core.TrainingConfig `json:"training"`
	BaseParameterHash string              `json:"base_parameter_hash"`
}

type MarketScope struct {
	InstrumentID          string `json:"instrument_id"`
	DataSource            string `json:"data_source"`
	Symbol                string `json:"symbol"`
	Interval              string `json:"interval"`
	MarketDataVersionID   uint   `json:"market_data_version_id,omitempty"`
	MarketDataContentHash string `json:"market_data_content_hash,omitempty"`
	StartTimeMs           int64  `json:"start_time_ms"`
	EndTimeMs             int64  `json:"end_time_ms"`
	DatasetHash           string `json:"dataset_hash"`
}

type TrainExecutionInput struct {
	SchemaVersion string              `json:"schema_version"`
	Horizon       int                 `json:"horizon"`
	Scope         MarketScope         `json:"scope"`
	Training      core.TrainingConfig `json:"training"`
}

type TrainExecutionResult struct {
	SchemaVersion string            `json:"schema_version"`
	Horizon       int               `json:"horizon"`
	DatasetHash   string            `json:"dataset_hash"`
	Model         core.HorizonModel `json:"model"`
	ContentHash   string            `json:"content_hash"`
}

type MaterializeExecutionInput struct {
	SchemaVersion          string                 `json:"schema_version"`
	StudyID                uint                   `json:"study_id"`
	BasePolicyArtifactID   uint                   `json:"base_policy_artifact_id,omitempty"`
	ArtifactSetHash        string                 `json:"artifact_set_hash"`
	PredictionSnapshotHash string                 `json:"prediction_snapshot_hash"`
	PolicyHash             string                 `json:"policy_hash"`
	PolicyOverride         *PolicyBundle          `json:"policy_override,omitempty"`
	Scope                  MarketScope            `json:"scope"`
	Backtest               backtest.CreateRequest `json:"backtest"`
}

type PolicyBundle struct {
	SchemaVersion  string             `json:"schema_version"`
	StateRules     core.StateRules    `json:"state_rules"`
	Policy         core.DynamicPolicy `json:"policy"`
	BaseChromosome quant.Chromosome   `json:"base_chromosome"`
	ModelVersion   string             `json:"model_version"`
}

type PredictionBlockPayload struct {
	SchemaVersion string            `json:"schema_version"`
	OneDay        []core.Prediction `json:"one_day"`
	TwentyDay     []core.Prediction `json:"twenty_day"`
}

type MaterializeExecutionResult struct {
	SchemaVersion             string                `json:"schema_version"`
	Materialized              core.MaterializedPath `json:"materialized"`
	ContentHash               string                `json:"content_hash"`
	BacktestResultID          uint                  `json:"backtest_result_id"`
	BacktestResultVersion     string                `json:"backtest_result_version"`
	BacktestResultContentHash string                `json:"backtest_result_content_hash"`
}

type StudyDescriptor struct {
	ID                    uint                  `json:"id"`
	Name                  string                `json:"name"`
	Status                string                `json:"status"`
	Route                 string                `json:"route"`
	StudyKey              string                `json:"study_key"`
	SettingHash           string                `json:"setting_hash"`
	DatasetHash           string                `json:"dataset_hash"`
	ComputeTaskID         *uint                 `json:"compute_task_id,omitempty"`
	MaterializationTaskID *uint                 `json:"materialization_task_id,omitempty"`
	ArtifactSetHash       string                `json:"artifact_set_hash,omitempty"`
	PredictionSnapshotID  *uint                 `json:"prediction_snapshot_id,omitempty"`
	PolicyArtifactID      *uint                 `json:"policy_artifact_id,omitempty"`
	MaterializationID     *uint                 `json:"materialization_id,omitempty"`
	Reports               []core.ModelReport    `json:"reports,omitempty"`
	Comparison            *ComparisonDescriptor `json:"comparison,omitempty"`
	ErrorMessage          string                `json:"error_message,omitempty"`
	CreatedAt             string                `json:"created_at"`
	CompletedAt           string                `json:"completed_at,omitempty"`
}

type ComparisonDescriptor struct {
	SourceKind      string   `json:"source_kind"`
	SourceID        uint     `json:"source_id"`
	SourceVersion   string   `json:"source_version"`
	SnapshotID      uint     `json:"snapshot_id"`
	ContentHash     string   `json:"content_hash"`
	DisplayName     string   `json:"display_name"`
	SourceStatus    string   `json:"source_status"`
	Archived        bool     `json:"archived"`
	SourceLink      string   `json:"source_link"`
	AvailableBlocks []string `json:"available_blocks"`
}

type PreviewResponse struct {
	Training any `json:"training"`
}

type CreateStudyResponse struct {
	Study   StudyDescriptor             `json:"study"`
	Preview computetask.PlanPreview     `json:"preview"`
	Task    *computetask.TaskDescriptor `json:"task,omitempty"`
}

type MaterializeRequest struct {
	ConfirmSoftLimit bool `json:"confirm_soft_limit"`
}

type MaterializeResponse struct {
	Study StudyDescriptor             `json:"study"`
	Task  *computetask.TaskDescriptor `json:"task"`
}

type ReportBlockDescriptor struct {
	BlockID        string          `json:"block_id"`
	BlockKind      string          `json:"block_kind"`
	SchemaVersion  string          `json:"schema_version"`
	FormulaVersion string          `json:"formula_version"`
	ContentHash    string          `json:"content_hash"`
	PointCount     int             `json:"point_count"`
	Payload        json.RawMessage `json:"payload,omitempty"`
}
