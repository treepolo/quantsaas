package controlresearch

import (
	"encoding/json"
	"errors"

	"quantsaas/internal/backtestcore"
	core "quantsaas/internal/controlresearch"
	performancecore "quantsaas/internal/performance"
	robust "quantsaas/internal/robustness"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/computetask"
	robustnesssvc "quantsaas/internal/saas/robustness"
)

const (
	TaskSchemaVersion       = "p11-control-task-v1"
	SnapshotSchemaVersion   = "p11-control-snapshot-v1"
	DetailSchemaVersion     = "p11-control-detail-v1"
	RuleVersion             = "p11-four-rules-v1"
	RangeVersion            = "p11-source-research-range-v1"
	ExecutorType            = "p11.control.target-runner"
	ExecutorVersion         = "p11-control-executor-v1"
	ExecutorResultVersion   = "p11-control-result-v1"
	ExecutionInputVersion   = "p11-control-input-v1"
	RandomRecordVersion     = "p11-random-parameter-v1"
	ComparisonSourceVersion = "p11-control-comparison-v1"
)

var (
	ErrInvalidRequest             = errors.New("P11 對照研究設定無效")
	ErrExtensionCountNotIncreased = errors.New("追加後的隨機參數或曝險打亂總數，至少要比目前數量增加一項")
	ErrNotFound                   = errors.New("找不到 P11 對照研究資源")
	ErrPlanStale                  = errors.New("對照研究計畫已過期，請重新預覽")
)

type CreateRequest struct {
	Name             string                         `json:"name"`
	Notes            string                         `json:"notes"`
	Tags             []string                       `json:"tags"`
	GenomeID         uint                           `json:"genome_id,omitempty"`
	CandidateID      uint                           `json:"candidate_id,omitempty"`
	Backtest         robustnesssvc.BacktestSettings `json:"backtest"`
	RandomSeed       int64                          `json:"random_seed"`
	RandomCount      int                            `json:"random_count"`
	ShuffleSeed      int64                          `json:"shuffle_seed"`
	ShuffleCount     int                            `json:"shuffle_count"`
	ToggleEveryNBars int                            `json:"toggle_every_n_bars"`
	ConfirmSoftLimit bool                           `json:"confirm_soft_limit,omitempty"`
	ExpectedPlanKey  string                         `json:"expected_plan_key,omitempty"`
}

type ExtendRequest struct {
	RandomCount      int  `json:"random_count"`
	ShuffleCount     int  `json:"shuffle_count"`
	ConfirmSoftLimit bool `json:"confirm_soft_limit"`
}

type UpdateMetadataRequest struct {
	Name     string   `json:"name"`
	Notes    string   `json:"notes"`
	Tags     []string `json:"tags"`
	Archived *bool    `json:"archived,omitempty"`
}

type TaskCanonical struct {
	SchemaVersion           string                 `json:"schema_version"`
	SourceKind              string                 `json:"source_kind"`
	SourceGenomeID          uint                   `json:"source_genome_id,omitempty"`
	CandidateID             uint                   `json:"candidate_id,omitempty"`
	ResearchConfigurationID uint                   `json:"research_configuration_id,omitempty"`
	SourceVersion           string                 `json:"source_version"`
	SourceContentHash       string                 `json:"source_content_hash"`
	ParameterSpace          robust.ParameterSpace  `json:"parameter_space"`
	ParameterSpaceHash      string                 `json:"parameter_space_hash"`
	FixedStructureHash      string                 `json:"fixed_structure_hash"`
	BaselineParameters      map[string]float64     `json:"baseline_parameters"`
	BaselineExecutorType    string                 `json:"baseline_executor_type"`
	BaselineInput           json.RawMessage        `json:"baseline_input"`
	Backtest                backtest.CreateRequest `json:"backtest"`
	ModelArtifactHash       string                 `json:"model_artifact_hash,omitempty"`
	PredictionSchemaHash    string                 `json:"prediction_schema_hash,omitempty"`
	DynamicPolicyHash       string                 `json:"dynamic_policy_hash,omitempty"`
	RandomSeed              int64                  `json:"random_seed"`
	ShuffleSeed             int64                  `json:"shuffle_seed"`
	ToggleEveryNBars        int                    `json:"toggle_every_n_bars"`
	RuleVersion             string                 `json:"rule_version"`
	StatisticsVersion       string                 `json:"statistics_version"`
}

type PlanResponse struct {
	PlanKey          string                           `json:"plan_key"`
	TaskKey          string                           `json:"task_key"`
	BatchKey         string                           `json:"batch_key"`
	RandomCount      int                              `json:"random_count"`
	ShuffleCount     int                              `json:"shuffle_count"`
	AttemptCount     int                              `json:"attempt_count"`
	RejectionCount   int                              `json:"rejection_count"`
	RejectReasons    map[string]int                   `json:"reject_reasons"`
	FixedDimensions  []string                         `json:"fixed_dimensions"`
	RandomDimensions []string                         `json:"random_dimensions"`
	SameStructure    bool                             `json:"same_structure"`
	Compute          computetask.CompositePlanPreview `json:"compute"`
}

type StageDescriptor struct {
	ID             uint    `json:"id"`
	Key            string  `json:"key"`
	Type           string  `json:"type"`
	Status         string  `json:"status"`
	CompletedCount int     `json:"completed_count"`
	TotalCount     int     `json:"total_count"`
	FailedCount    int     `json:"failed_count"`
	Progress       float64 `json:"progress"`
	Error          string  `json:"error,omitempty"`
}

type MetricSet struct {
	ROI                   float64                                      `json:"roi"`
	FinalEquity           float64                                      `json:"final_equity"`
	FinalNAVRatio         float64                                      `json:"final_nav_ratio"`
	LogFinalNAVRatio      float64                                      `json:"log_final_nav_ratio"`
	MaxDrawdown           float64                                      `json:"max_drawdown"`
	Sortino               *float64                                     `json:"sortino,omitempty"`
	LongestUnderwaterDays float64                                      `json:"longest_underwater_days"`
	TradeCount            int                                          `json:"trade_count"`
	ExposureDaysRatio     float64                                      `json:"exposure_days_ratio"`
	AverageExposure       float64                                      `json:"average_exposure"`
	FeeCost               float64                                      `json:"fee_cost"`
	SlippageCost          float64                                      `json:"slippage_cost"`
	ReturnDistributions   map[string]performancecore.DistributionStats `json:"return_distributions"`
}

type PercentileSet struct {
	LogFinalNAVRatio      float64  `json:"log_final_nav_ratio"`
	MaxDrawdown           float64  `json:"max_drawdown"`
	Sortino               *float64 `json:"sortino,omitempty"`
	LongestUnderwaterDays float64  `json:"longest_underwater_days"`
}

type DistributionSet struct {
	LogFinalNAVRatio      core.Distribution  `json:"log_final_nav_ratio"`
	MaxDrawdown           core.Distribution  `json:"max_drawdown"`
	Sortino               *core.Distribution `json:"sortino,omitempty"`
	LongestUnderwaterDays core.Distribution  `json:"longest_underwater_days"`
}

type RuleResult struct {
	EvaluationID     uint      `json:"evaluation_id"`
	RuleType         string    `json:"rule_type"`
	BacktestResultID uint      `json:"backtest_result_id"`
	Metrics          MetricSet `json:"metrics"`
}

type SnapshotSummary struct {
	SchemaVersion        string           `json:"schema_version"`
	BaselineEvaluationID uint             `json:"baseline_evaluation_id"`
	BaselineResultID     uint             `json:"baseline_result_id"`
	Baseline             MetricSet        `json:"baseline"`
	RandomDistribution   *DistributionSet `json:"random_distribution,omitempty"`
	RandomPercentiles    *PercentileSet   `json:"random_percentiles,omitempty"`
	ShuffleDistribution  *DistributionSet `json:"shuffle_distribution,omitempty"`
	ShufflePercentiles   *PercentileSet   `json:"shuffle_percentiles,omitempty"`
	Rules                []RuleResult     `json:"rules"`
	ConclusionLabels     []string         `json:"conclusion_labels"`
}

type SnapshotDescriptor struct {
	ID                    uint            `json:"id"`
	Completeness          string          `json:"completeness"`
	StatisticsVersion     string          `json:"statistics_version"`
	RandomCompletedCount  int             `json:"random_completed_count"`
	ShuffleCompletedCount int             `json:"shuffle_completed_count"`
	RuleCompletedCount    int             `json:"rule_completed_count"`
	FailedCount           int             `json:"failed_count"`
	CancelledCount        int             `json:"cancelled_count"`
	CacheHitCount         int             `json:"cache_hit_count"`
	ContentHash           string          `json:"content_hash"`
	Summary               SnapshotSummary `json:"summary"`
	CreatedAt             string          `json:"created_at"`
}

type TaskDescriptor struct {
	ID                      uint                `json:"id"`
	Name                    string              `json:"name"`
	Notes                   string              `json:"notes"`
	Tags                    []string            `json:"tags"`
	Status                  string              `json:"status"`
	SourceKind              string              `json:"source_kind"`
	SourceGenomeID          *uint               `json:"source_genome_id,omitempty"`
	CandidateID             *uint               `json:"candidate_id,omitempty"`
	ResearchConfigurationID *uint               `json:"research_configuration_id,omitempty"`
	RandomBatchID           uint                `json:"random_batch_id"`
	RandomTargetCount       int                 `json:"random_target_count"`
	ShuffleTargetCount      int                 `json:"shuffle_target_count"`
	ToggleEveryNBars        int                 `json:"toggle_every_n_bars"`
	SameStructure           bool                `json:"same_structure"`
	ComputeTaskID           *uint               `json:"compute_task_id,omitempty"`
	Stages                  []StageDescriptor   `json:"stages"`
	LatestSnapshot          *SnapshotDescriptor `json:"latest_snapshot,omitempty"`
	Archived                bool                `json:"archived"`
	CreatedAt               string              `json:"created_at"`
	CompletedAt             string              `json:"completed_at,omitempty"`
}

type RandomRecordDescriptor struct {
	ID                    uint               `json:"id"`
	BatchID               uint               `json:"batch_id"`
	SequenceIndex         int                `json:"sequence_index"`
	Coordinates           []int              `json:"coordinates"`
	Parameters            map[string]float64 `json:"parameters"`
	ContentHash           string             `json:"content_hash"`
	BacktestResultID      *uint              `json:"backtest_result_id,omitempty"`
	BacktestResultVersion string             `json:"backtest_result_version,omitempty"`
	BacktestContentHash   string             `json:"backtest_content_hash,omitempty"`
}

type EvaluationDescriptor struct {
	ID                        uint      `json:"id"`
	Kind                      string    `json:"kind"`
	SequenceIndex             int       `json:"sequence_index"`
	RuleType                  string    `json:"rule_type,omitempty"`
	RandomParameterRecordID   *uint     `json:"random_parameter_record_id,omitempty"`
	BacktestResultID          uint      `json:"backtest_result_id"`
	BacktestResultVersion     string    `json:"backtest_result_version"`
	BacktestResultContentHash string    `json:"backtest_result_content_hash"`
	Metrics                   MetricSet `json:"metrics"`
	RepresentativeRole        string    `json:"representative_role,omitempty"`
}

type DetailDescriptor struct {
	SchemaVersion string                 `json:"schema_version"`
	TaskID        uint                   `json:"task_id"`
	SnapshotID    uint                   `json:"snapshot_id"`
	Evaluations   []EvaluationDescriptor `json:"evaluations"`
}

type PathBlockDescriptor struct {
	EvaluationID uint                         `json:"evaluation_id"`
	ResultID     uint                         `json:"result_id"`
	BlockIndex   int                          `json:"block_index"`
	ContentHash  string                       `json:"content_hash"`
	Block        backtestresult.PathBlockData `json:"block"`
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

type ExecutionInput struct {
	SchemaVersion string                   `json:"schema_version"`
	Kind          string                   `json:"kind"`
	SequenceIndex int                      `json:"sequence_index"`
	TaskKey       string                   `json:"task_key,omitempty"`
	Backtest      backtest.CreateRequest   `json:"backtest"`
	Rule          *backtestcore.RuleConfig `json:"rule,omitempty"`
	ShuffleSeed   int64                    `json:"shuffle_seed,omitempty"`
}

type ExecutionResult struct {
	SchemaVersion             string `json:"schema_version"`
	Kind                      string `json:"kind"`
	SequenceIndex             int    `json:"sequence_index"`
	RuleType                  string `json:"rule_type,omitempty"`
	BacktestResultID          uint   `json:"backtest_result_id"`
	BacktestResultVersion     string `json:"backtest_result_version"`
	BacktestResultContentHash string `json:"backtest_result_content_hash"`
	ReusedBacktest            bool   `json:"reused_backtest"`
}
