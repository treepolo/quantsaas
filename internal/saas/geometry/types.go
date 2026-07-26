package geometry

import (
	"encoding/json"

	core "quantsaas/internal/dynamicparam"
	"quantsaas/internal/saas/computetask"
)

const (
	StudySettingVersion  = "geometry-study-setting-v2"
	StudyStatusPlanned   = "planned"
	StudyStatusTraining  = "training"
	StudyStatusCompleted = "completed"
	StudyStatusFailed    = "failed"
	StudyStatusCancelled = "cancelled"
	TrainInputVersion    = "geometry-train-input-v1"
	TrainExecutorType    = "geometry-model-training"
	TrainExecutorVersion = "geometry-model-training-v3"
	TrainResultVersion   = "geometry-model-training-result-v2"
)

type CreateRequest struct {
	Name                  string `json:"name"`
	InstrumentID          string `json:"instrument_id"`
	DataSource            string `json:"data_source"`
	Symbol                string `json:"symbol"`
	Interval              string `json:"interval"`
	TrainStartTimeMs      int64  `json:"train_start_time_ms"`
	TrainEndTimeMs        int64  `json:"train_end_time_ms"`
	Lookbacks             []int  `json:"lookbacks"`
	Folds                 int    `json:"folds"`
	MinimumTrain          int    `json:"minimum_train"`
	ComputeMonitorEnabled *bool  `json:"compute_monitor_enabled,omitempty"`
	ConfirmSoftLimit      bool   `json:"confirm_soft_limit"`
}

type StudySetting struct {
	Version     string        `json:"version"`
	Request     CreateRequest `json:"request"`
	DatasetHash string        `json:"dataset_hash"`
}
type MarketScope struct {
	InstrumentID string `json:"instrument_id"`
	DataSource   string `json:"data_source"`
	Symbol       string `json:"symbol"`
	Interval     string `json:"interval"`
	StartTimeMs  int64  `json:"start_time_ms"`
	EndTimeMs    int64  `json:"end_time_ms"`
	DatasetHash  string `json:"dataset_hash"`
}
type TrainInput struct {
	SchemaVersion string                      `json:"schema_version"`
	Horizon       int                         `json:"horizon"`
	Scope         MarketScope                 `json:"scope"`
	Config        core.GeometryTrainingConfig `json:"config"`
}
type TrainResult struct {
	SchemaVersion string                      `json:"schema_version"`
	Horizon       int                         `json:"horizon"`
	DatasetHash   string                      `json:"dataset_hash"`
	Training      core.GeometryTrainingResult `json:"training"`
	Predictions   []core.GeometryPrediction   `json:"predictions"`
	ContentHash   string                      `json:"content_hash"`
}
type SnapshotPayload struct {
	SchemaVersion string                    `json:"schema_version"`
	OneDay        []core.GeometryPrediction `json:"one_day"`
	TwentyDay     []core.GeometryPrediction `json:"twenty_day"`
}

type StudyDescriptor struct {
	ID              uint                 `json:"id"`
	Name            string               `json:"name"`
	Status          string               `json:"status"`
	StudyKey        string               `json:"study_key"`
	SettingHash     string               `json:"setting_hash"`
	DatasetHash     string               `json:"dataset_hash"`
	ComputeTaskID   *uint                `json:"compute_task_id,omitempty"`
	ArtifactSetHash string               `json:"artifact_set_hash,omitempty"`
	PredictionID    *uint                `json:"prediction_id,omitempty"`
	CreatedAt       string               `json:"created_at"`
	CompletedAt     string               `json:"completed_at,omitempty"`
	ErrorMessage    string               `json:"error_message,omitempty"`
	Artifacts       []ArtifactDescriptor `json:"artifacts,omitempty"`
	Predictions     *SnapshotPayload     `json:"predictions,omitempty"`
	InstrumentID    string               `json:"instrument_id"`
	DataSource      string               `json:"data_source"`
	Symbol          string               `json:"symbol"`
	Interval        string               `json:"interval"`
}
type ArtifactDescriptor struct {
	ID          uint                          `json:"id"`
	Horizon     int                           `json:"horizon"`
	Lookback    int                           `json:"lookback"`
	ContentHash string                        `json:"content_hash"`
	Report      core.GeometryValidationReport `json:"report"`
	Model       core.GeometryModel            `json:"model,omitempty"`
	Training    core.GeometryTrainingResult   `json:"training"`
}

type CompatibleArtifact struct {
	StudyID       uint   `json:"study_id"`
	StudyName     string `json:"study_name"`
	ArtifactID    uint   `json:"artifact_id"`
	Horizon       int    `json:"horizon"`
	Lookback      int    `json:"lookback"`
	InstrumentID  string `json:"instrument_id"`
	DataSource    string `json:"data_source"`
	Symbol        string `json:"symbol"`
	Interval      string `json:"interval"`
	DatasetHash   string `json:"dataset_hash"`
	SchemaVersion string `json:"schema_version"`
	ContentHash   string `json:"content_hash"`
	Status        string `json:"status"`
}
type CreateResponse struct {
	Study   StudyDescriptor             `json:"study"`
	Preview computetask.PlanPreview     `json:"preview"`
	Task    *computetask.TaskDescriptor `json:"task,omitempty"`
}
type ReportResponse struct {
	Study    StudyDescriptor `json:"study"`
	Snapshot json.RawMessage `json:"snapshot,omitempty"`
}
