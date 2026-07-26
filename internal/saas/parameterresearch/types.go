package parameterresearch

import (
	"encoding/json"
	"errors"

	dynamiccore "quantsaas/internal/dynamicparam"
	core "quantsaas/internal/parameterresearch"
	robust "quantsaas/internal/robustness"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/computetask"
	robustnesssvc "quantsaas/internal/saas/robustness"
)

const (
	ConfigurationSchemaVersion = "p10-research-configuration-v1"
	RunSchemaVersion           = "p10-research-run-v1"
	StageSchemaVersion         = "p10-research-stage-v1"
	SurrogateExecutorType      = "p10.research.surrogate"
	SurrogateExecutorVersion   = "p10-surrogate-executor-v1"
	SurrogateResultVersion     = "p10-surrogate-result-v1"
)

var (
	ErrInvalidRequest = errors.New("P10 參數研究設定無效")
	ErrNotFound       = errors.New("找不到 P10 參數研究資源")
	ErrPlanStale      = errors.New("研究計畫已過期，請重新預覽")
)

type DynamicReference struct {
	StudyID          uint `json:"study_id"`
	PolicyArtifactID uint `json:"policy_artifact_id"`
}

type GeometryReference struct {
	StudyID     uint   `json:"study_id"`
	ArtifactID  uint   `json:"artifact_id"`
	Horizon     int    `json:"horizon"`
	ContentHash string `json:"content_hash"`
}

type DynamicSpaceDescriptor struct {
	StudyID          uint                                    `json:"study_id"`
	PolicyArtifactID uint                                    `json:"policy_artifact_id"`
	Schema           dynamiccore.DynamicParameterSpaceSchema `json:"schema"`
	BaseValues       map[string]float64                      `json:"base_values"`
}

// PointExecutionInput is the stable M-owned adapter used by downstream H. It
// preserves K's frozen structure while allowing only M research dimensions to
// change.
type PointExecutionInput struct {
	ExecutorType string                 `json:"executor_type"`
	Input        json.RawMessage        `json:"input"`
	Backtest     backtest.CreateRequest `json:"backtest"`
	Dynamic      bool                   `json:"dynamic"`
}

type CreateConfigurationRequest struct {
	Name            string                         `json:"name"`
	Notes           string                         `json:"notes"`
	Tags            []string                       `json:"tags"`
	GenomeID        uint                           `json:"genome_id"`
	ParameterSpace  robust.ParameterSpace          `json:"parameter_space"`
	BaseCoordinates []int                          `json:"base_coordinates"`
	Backtest        robustnesssvc.BacktestSettings `json:"backtest"`
	Dynamic         *DynamicReference              `json:"dynamic,omitempty"`
	Geometry        *GeometryReference             `json:"geometry,omitempty"`
}

type ConfigurationCanonical struct {
	SchemaVersion   string                         `json:"schema_version"`
	GenomeID        uint                           `json:"genome_id"`
	ParameterSpace  robust.ParameterSpace          `json:"parameter_space"`
	BaseCoordinates []int                          `json:"base_coordinates"`
	Backtest        robustnesssvc.BacktestSettings `json:"backtest"`
	DatasetHash     string                         `json:"dataset_hash"`
	DynamicPackage  *DynamicPackageReference       `json:"dynamic_package,omitempty"`
	GeometryPackage *GeometryPackageReference      `json:"geometry_package,omitempty"`
}

type DynamicPackageReference struct {
	StudyID                uint   `json:"study_id"`
	PolicyArtifactID       uint   `json:"policy_artifact_id"`
	ArtifactSetHash        string `json:"artifact_set_hash"`
	PredictionSnapshotID   uint   `json:"prediction_snapshot_id"`
	PredictionSnapshotHash string `json:"prediction_snapshot_hash"`
	BasePolicyHash         string `json:"base_policy_hash"`
	ParameterSpaceVersion  string `json:"parameter_space_version"`
	ParameterSpaceHash     string `json:"parameter_space_hash"`
}

type GeometryPackageReference struct {
	StudyID       uint   `json:"study_id"`
	ArtifactID    uint   `json:"artifact_id"`
	Horizon       int    `json:"horizon"`
	DatasetHash   string `json:"dataset_hash"`
	ContentHash   string `json:"content_hash"`
	SchemaVersion string `json:"schema_version"`
}

type ConfigurationDescriptor struct {
	ID                    uint                      `json:"id"`
	Name                  string                    `json:"name"`
	Notes                 string                    `json:"notes"`
	Tags                  []string                  `json:"tags"`
	ConfigHash            string                    `json:"config_hash"`
	SchemaVersion         string                    `json:"schema_version"`
	InstrumentID          string                    `json:"instrument_id"`
	DataSource            string                    `json:"data_source"`
	Symbol                string                    `json:"symbol"`
	Interval              string                    `json:"interval"`
	DatasetHash           string                    `json:"dataset_hash"`
	StartTimeMs           int64                     `json:"start_time_ms"`
	EndTimeMs             int64                     `json:"end_time_ms"`
	ExecutionMode         string                    `json:"execution_mode"`
	ParameterSpaceVersion string                    `json:"parameter_space_version"`
	ParameterSpaceHash    string                    `json:"parameter_space_hash"`
	ParameterSpace        robust.ParameterSpace     `json:"parameter_space"`
	BaseCoordinates       []int                     `json:"base_coordinates"`
	DynamicMode           bool                      `json:"dynamic_mode"`
	DynamicPackage        *DynamicPackageReference  `json:"dynamic_package,omitempty"`
	GeometryPackage       *GeometryPackageReference `json:"geometry_package,omitempty"`
	Archived              bool                      `json:"archived"`
	CreatedAt             string                    `json:"created_at"`
}

type RunPlanRequest struct {
	RequestedSobol int   `json:"requested_sobol"`
	RootSeed       int64 `json:"root_seed"`
}

type StagePlanRequest struct {
	Kind           string `json:"kind"`
	RequestedSobol int    `json:"requested_sobol,omitempty"`
	CenterPointID  uint   `json:"center_point_id,omitempty"`
	Radius         int    `json:"radius,omitempty"`
	SurrogateID    uint   `json:"surrogate_id,omitempty"`
	ProposalIDs    []uint `json:"proposal_ids,omitempty"`
}

type PointPageDescriptor struct {
	Items      []PointDescriptor `json:"items"`
	Page       int               `json:"page"`
	PageSize   int               `json:"page_size"`
	Total      int64             `json:"total"`
	TotalPages int               `json:"total_pages"`
}

type LandscapeDescriptor struct {
	ConfigurationID uint              `json:"configuration_id"`
	AxisX           string            `json:"axis_x"`
	AxisY           string            `json:"axis_y"`
	Metric          string            `json:"metric"`
	Points          []PointDescriptor `json:"points"`
	Truncated       bool              `json:"truncated"`
}

type PlanDescriptor struct {
	PlanKey        string                  `json:"plan_key"`
	ManifestHash   string                  `json:"manifest_hash"`
	StageType      string                  `json:"stage_type"`
	Global         *core.GlobalPlan        `json:"global,omitempty"`
	Points         []core.PlannedPoint     `json:"points"`
	Compute        computetask.PlanPreview `json:"compute"`
	NextSobolIndex int64                   `json:"next_sobol_index"`
}

type StartRunRequest struct {
	Plan             RunPlanRequest `json:"plan"`
	PlanKey          string         `json:"plan_key"`
	IdempotencyKey   string         `json:"idempotency_key"`
	ConfirmSoftLimit bool           `json:"confirm_soft_limit"`
}

type StartStageRequest struct {
	Plan             StagePlanRequest `json:"plan"`
	PlanKey          string           `json:"plan_key"`
	ConfirmSoftLimit bool             `json:"confirm_soft_limit"`
}

type PointDescriptor struct {
	ID                        uint                    `json:"id"`
	VectorHash                string                  `json:"vector_hash"`
	Coordinates               []int                   `json:"coordinates"`
	Parameters                map[string]float64      `json:"parameters"`
	Status                    string                  `json:"status"`
	Qualified                 bool                    `json:"qualified"`
	BacktestResultID          *uint                   `json:"backtest_result_id,omitempty"`
	BacktestResultContentHash string                  `json:"backtest_result_content_hash,omitempty"`
	Metrics                   *robust.RelativeMetrics `json:"metrics,omitempty"`
}

type StageDescriptor struct {
	ID              uint   `json:"id"`
	Ordinal         int    `json:"ordinal"`
	StageKey        string `json:"stage_key"`
	StageType       string `json:"stage_type"`
	ManifestHash    string `json:"manifest_hash"`
	ComputeTaskID   *uint  `json:"compute_task_id,omitempty"`
	Status          string `json:"status"`
	RequestedCount  int    `json:"requested_count"`
	UniqueCount     int    `json:"unique_count"`
	CacheHitCount   int    `json:"cache_hit_count"`
	CompletedCount  int    `json:"completed_count"`
	FailedCount     int    `json:"failed_count"`
	MissingCount    int    `json:"missing_count"`
	SobolStartIndex int64  `json:"sobol_start_index"`
	SobolEndIndex   int64  `json:"sobol_end_index"`
	ErrorMessage    string `json:"error_message,omitempty"`
}

type RunDescriptor struct {
	ID                     uint              `json:"id"`
	ConfigurationID        uint              `json:"configuration_id"`
	RunKey                 string            `json:"run_key"`
	SamplerVersion         string            `json:"sampler_version"`
	NextSobolIndex         int64             `json:"next_sobol_index"`
	GlobalUniquePointCount int               `json:"global_unique_point_count"`
	GlobalBatchCount       int               `json:"global_batch_count"`
	ExplorationStatus      string            `json:"exploration_status"`
	Status                 string            `json:"status"`
	Stages                 []StageDescriptor `json:"stages"`
	Points                 []PointDescriptor `json:"points,omitempty"`
	CreatedAt              string            `json:"created_at"`
}

type AnalysisRequest struct {
	Metric robust.MetricName `json:"metric"`
	Radii  []int             `json:"radii"`
}

type AnalysisDescriptor struct {
	ID                   uint            `json:"id"`
	ConfigurationID      uint            `json:"configuration_id"`
	PointSetHash         string          `json:"point_set_hash"`
	Completeness         string          `json:"completeness"`
	ContentHash          string          `json:"content_hash"`
	RobustnessStudyID    uint            `json:"robustness_study_id"`
	RobustnessSnapshotID uint            `json:"robustness_snapshot_id"`
	Result               json.RawMessage `json:"result"`
}

type CandidateDescriptor struct {
	ID                 uint                     `json:"id"`
	ConfigurationID    uint                     `json:"configuration_id"`
	PointID            uint                     `json:"point_id"`
	AnalysisSnapshotID *uint                    `json:"analysis_snapshot_id,omitempty"`
	RegionID           *uint                    `json:"region_id,omitempty"`
	SourceKind         string                   `json:"source_kind"`
	Completeness       string                   `json:"completeness"`
	Roles              []string                 `json:"roles"`
	AdoptionUnitHash   string                   `json:"adoption_unit_hash"`
	Name               string                   `json:"name"`
	Notes              string                   `json:"notes"`
	Tags               []string                 `json:"tags"`
	Archived           bool                     `json:"archived"`
	GeneRecordID       *uint                    `json:"gene_record_id,omitempty"`
	AnalysisLinks      []AnalysisLinkDescriptor `json:"analysis_links"`
}

type AnalysisLinkDescriptor struct {
	Kind              string          `json:"kind"`
	Version           string          `json:"version"`
	Status            string          `json:"status"`
	TaskID            *uint           `json:"task_id,omitempty"`
	SourceID          string          `json:"source_id,omitempty"`
	SourceVersion     string          `json:"source_version,omitempty"`
	SourceContentHash string          `json:"source_content_hash,omitempty"`
	PartialSnapshot   json.RawMessage `json:"partial_snapshot,omitempty"`
	ErrorMessage      string          `json:"error_message,omitempty"`
}

type UpdateAnalysisLinkRequest struct {
	Status            string          `json:"status"`
	TaskID            *uint           `json:"task_id,omitempty"`
	SourceID          string          `json:"source_id,omitempty"`
	SourceVersion     string          `json:"source_version,omitempty"`
	SourceContentHash string          `json:"source_content_hash,omitempty"`
	PartialSnapshot   json.RawMessage `json:"partial_snapshot,omitempty"`
	ErrorMessage      string          `json:"error_message,omitempty"`
}

type SurrogateExecutionInput struct {
	SchemaVersion string                  `json:"schema_version"`
	N0            int                     `json:"n0"`
	Settings      core.ForestSettings     `json:"settings"`
	Examples      []core.SurrogateExample `json:"examples"`
}

type SurrogateExecutionResult struct {
	SchemaVersion string                 `json:"schema_version"`
	Artifact      core.SurrogateArtifact `json:"artifact"`
	ContentHash   string                 `json:"content_hash"`
}

type SurrogatePlanRequest struct {
	Seed uint64 `json:"seed"`
}
type StartSurrogateRequest struct {
	Seed             uint64 `json:"seed"`
	PlanKey          string `json:"plan_key"`
	ConfirmSoftLimit bool   `json:"confirm_soft_limit"`
}
type ProposalRequest struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type SurrogateDescriptor struct {
	ID                   uint                    `json:"id"`
	ConfigurationID      uint                    `json:"configuration_id"`
	RunID                uint                    `json:"run_id"`
	Status               string                  `json:"status"`
	ComputeTaskID        *uint                   `json:"compute_task_id,omitempty"`
	TrainingPointSetHash string                  `json:"training_point_set_hash"`
	CanGuideReturn       bool                    `json:"can_guide_return"`
	CanGuideDrawdown     bool                    `json:"can_guide_drawdown"`
	CanGuideConservative bool                    `json:"can_guide_conservative"`
	ContentHash          string                  `json:"content_hash,omitempty"`
	Artifact             *core.SurrogateArtifact `json:"artifact,omitempty"`
}

type ProposalDescriptor struct {
	ID            uint                    `json:"id"`
	Types         []string                `json:"types"`
	VectorHash    string                  `json:"vector_hash"`
	Coordinates   []int                   `json:"coordinates"`
	Parameters    map[string]float64      `json:"parameters"`
	Prediction    core.ProposalPrediction `json:"prediction"`
	ActualPointID *uint                   `json:"actual_point_id,omitempty"`
}

type CreateSeriesRequest struct {
	Name             string            `json:"name"`
	ConfigurationIDs []uint            `json:"configuration_ids"`
	ChangedFactors   []string          `json:"changed_factors"`
	FactorValues     []json.RawMessage `json:"factor_values"`
}

type SeriesDescriptor struct {
	ID               uint                       `json:"id"`
	Name             string                     `json:"name"`
	ConfigurationIDs []uint                     `json:"configuration_ids"`
	Eligibility      core.ComparisonEligibility `json:"eligibility"`
	SnapshotID       uint                       `json:"snapshot_id"`
	ContentHash      string                     `json:"content_hash"`
}

const ComparisonSourceVersion = "p14-m-comparison-source-v1"

type ComparisonDescriptor struct {
	SourceKind       string   `json:"source_kind"`
	SourceID         uint     `json:"source_id"`
	SourceVersion    string   `json:"source_version"`
	SnapshotID       uint     `json:"snapshot_id"`
	ContentHash      string   `json:"content_hash"`
	CanonicalSubject string   `json:"canonical_subject_ref,omitempty"`
	DisplayName      string   `json:"display_name"`
	SourceStatus     string   `json:"source_status"`
	Archived         bool     `json:"archived"`
	CreatedAt        string   `json:"created_at"`
	SourceLink       string   `json:"source_link"`
	AvailableBlocks  []string `json:"available_blocks"`
}

type ComparisonBlockDescriptor struct {
	BlockID            string          `json:"block_id"`
	BlockKind          string          `json:"block_kind"`
	SchemaID           string          `json:"schema_id"`
	SchemaVersion      string          `json:"schema_version"`
	FormulaVersion     string          `json:"formula_version,omitempty"`
	Unit               string          `json:"unit,omitempty"`
	Axes               []string        `json:"axes"`
	ContextFingerprint json.RawMessage `json:"context_fingerprint"`
	ContentHash        string          `json:"content_hash"`
	Availability       string          `json:"availability"`
	PayloadLocator     string          `json:"payload_locator"`
	Payload            json.RawMessage `json:"payload"`
}
