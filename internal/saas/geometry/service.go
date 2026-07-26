package geometry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	compute "quantsaas/internal/compute"
	core "quantsaas/internal/dynamicparam"
	"quantsaas/internal/quant"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"
)

var ErrInvalidRequest = errors.New("走勢幾何模型設定無效")
var ErrNotFound = errors.New("找不到走勢幾何模型研究")

type Service struct {
	db    *gorm.DB
	tasks *computetask.Service
}

func NewService(db *gorm.DB, tasks *computetask.Service) *Service {
	return &Service{db: db, tasks: tasks}
}

func (s *Service) prepare(ctx context.Context, req CreateRequest) (CreateRequest, MarketScope, []byte, computetask.CreateSpec, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "走勢幾何預測模型"
	}
	req.Interval = strings.TrimSpace(req.Interval)
	if req.Interval == "" {
		req.Interval = "1d"
	}
	if req.Interval != "1d" || req.InstrumentID == "" || req.DataSource == "" || req.Symbol == "" || req.TrainStartTimeMs <= 0 || req.TrainEndTimeMs <= req.TrainStartTimeMs {
		return CreateRequest{}, MarketScope{}, nil, computetask.CreateSpec{}, ErrInvalidRequest
	}
	if len(req.Lookbacks) == 0 {
		req.Lookbacks = []int{5, 10, 20, 40, 60, 120, 250, 500}
	}
	req.Lookbacks = uniqueLookbacks(req.Lookbacks)
	if len(req.Lookbacks) == 0 {
		return CreateRequest{}, MarketScope{}, nil, computetask.CreateSpec{}, ErrInvalidRequest
	}
	if req.Folds < 2 {
		req.Folds = 4
	}
	if req.MinimumTrain < 8 {
		req.MinimumTrain = 8
	}
	var rows []saasstore.KLine
	if err := s.db.WithContext(ctx).Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time >= ? AND open_time <= ?", req.InstrumentID, req.DataSource, req.Symbol, req.Interval, req.TrainStartTimeMs, req.TrainEndTimeMs).Order("open_time ASC").Find(&rows).Error; err != nil {
		return CreateRequest{}, MarketScope{}, nil, computetask.CreateSpec{}, err
	}
	bars := make([]quant.Bar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, quant.Bar{OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	if len(bars) < req.MinimumTrain+core.HorizonTwentyDay+2 {
		return CreateRequest{}, MarketScope{}, nil, computetask.CreateSpec{}, fmt.Errorf("幾何模型至少需要更多日 K")
	}
	datasetHash, err := backtestresult.HashDataset(backtestresult.DatasetSchemaVersion, bars)
	if err != nil {
		return CreateRequest{}, MarketScope{}, nil, computetask.CreateSpec{}, err
	}
	scope := MarketScope{InstrumentID: req.InstrumentID, DataSource: req.DataSource, Symbol: req.Symbol, Interval: req.Interval, StartTimeMs: bars[0].OpenTime, EndTimeMs: bars[len(bars)-1].OpenTime, DatasetHash: datasetHash}
	setting := StudySetting{Version: StudySettingVersion, Request: req, DatasetHash: datasetHash}
	settingRaw, err := compute.CanonicalJSON(setting)
	if err != nil {
		return CreateRequest{}, MarketScope{}, nil, computetask.CreateSpec{}, err
	}
	config := core.GeometryTrainingConfig{Lookbacks: req.Lookbacks, Folds: req.Folds, MinimumTrain: req.MinimumTrain}
	items := []compute.ManifestItemInput{}
	for _, horizon := range []int{core.HorizonOneDay, core.HorizonTwentyDay} {
		inputRaw, _ := compute.CanonicalJSON(TrainInput{SchemaVersion: TrainInputVersion, Horizon: horizon, Scope: scope, Config: config})
		items = append(items, compute.ManifestItemInput{Key: fmt.Sprintf("horizon-%d", horizon), CacheKey: "geometry-train:" + compute.HashBytes(inputRaw), Input: inputRaw, EstimatedUnits: int64(len(bars) * len(req.Lookbacks) * 3)})
	}
	spec := computetask.CreateSpec{Kind: compute.TaskKindAtomic, TaskType: "geometry-model.train", Title: req.Name, ExecutorType: TrainExecutorType, Settings: setting, ResearchSettingID: "geometry:" + req.Symbol, ResearchSettingHash: compute.HashBytes(settingRaw), StageKey: "training", StageType: "trend-geometry", ComputeMonitorEnabled: req.ComputeMonitorEnabled == nil || *req.ComputeMonitorEnabled, Items: items}
	return req, scope, settingRaw, spec, nil
}

func (s *Service) Preview(ctx context.Context, userID uint, req CreateRequest) (computetask.PlanPreview, error) {
	if s.tasks == nil {
		return computetask.PlanPreview{}, computetask.ErrServiceUnavailable
	}
	_, _, _, spec, err := s.prepare(ctx, req)
	if err != nil {
		return computetask.PlanPreview{}, err
	}
	return s.tasks.Preview(ctx, userID, spec)
}
func (s *Service) Create(ctx context.Context, userID uint, req CreateRequest) (CreateResponse, error) {
	if userID == 0 || s.tasks == nil {
		return CreateResponse{}, computetask.ErrServiceUnavailable
	}
	prepared, scope, settingRaw, spec, err := s.prepare(ctx, req)
	if err != nil {
		return CreateResponse{}, err
	}
	preview, err := s.tasks.Preview(ctx, userID, spec)
	if err != nil {
		return CreateResponse{}, err
	}
	key := "geometry-study:" + compute.HashBytes(settingRaw)
	var study saasstore.GeometryModelStudy
	if find := s.db.WithContext(ctx).Where("owner_user_id = ? AND study_key = ?", userID, key).First(&study); find.Error == nil {
		if study.Status == StudyStatusFailed || study.Status == StudyStatusCancelled {
			// A failed/cancelled run is historical state, not a reusable study.
			// Recreate its compute task with the current executor/cache version
			// while keeping the old task and study identity auditable.
			task, createErr := s.tasks.Create(ctx, userID, spec, prepared.ConfirmSoftLimit)
			if createErr != nil {
				return CreateResponse{}, createErr
			}
			if updateErr := s.db.WithContext(ctx).Model(&study).Updates(map[string]any{
				"compute_task_id": task.ID,
				"status":          taskStatus(task.Status),
				"error_message":   "",
				"completed_at":    nil,
			}).Error; updateErr != nil {
				return CreateResponse{}, updateErr
			}
			study.ComputeTaskID = &task.ID
			study.Status = taskStatus(task.Status)
			study.ErrorMessage = ""
			study.CompletedAt = nil
			return CreateResponse{Study: s.describe(ctx, study), Preview: preview, Task: task}, nil
		}
		return CreateResponse{Study: s.describe(ctx, study), Preview: preview}, nil
	} else if !errors.Is(find.Error, gorm.ErrRecordNotFound) {
		return CreateResponse{}, find.Error
	}
	study = saasstore.GeometryModelStudy{OwnerUserID: userID, StudyKey: key, Name: prepared.Name, Status: StudyStatusPlanned, InstrumentID: scope.InstrumentID, DataSource: scope.DataSource, Symbol: scope.Symbol, Interval: scope.Interval, TrainStartTimeMs: scope.StartTimeMs, TrainEndTimeMs: scope.EndTimeMs, DatasetHash: scope.DatasetHash, SettingVersion: StudySettingVersion, SettingHash: compute.HashBytes(settingRaw), Settings: saasstore.JSONB(settingRaw)}
	if err := s.db.WithContext(ctx).Create(&study).Error; err != nil {
		return CreateResponse{}, err
	}
	task, err := s.tasks.Create(ctx, userID, spec, prepared.ConfirmSoftLimit)
	if err != nil {
		return CreateResponse{}, err
	}
	s.db.WithContext(ctx).Model(&study).Updates(map[string]any{"compute_task_id": task.ID, "status": taskStatus(task.Status)})
	study.ComputeTaskID = &task.ID
	study.Status = taskStatus(task.Status)
	if task.Status == compute.TaskStatusCompleted {
		if err := s.sync(ctx, &study, task); err != nil {
			return CreateResponse{}, err
		}
	}
	return CreateResponse{Study: s.describe(ctx, study), Preview: preview, Task: task}, nil
}
func (s *Service) List(ctx context.Context, userID uint, limit int) ([]StudyDescriptor, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rows []saasstore.GeometryModelStudy
	if err := s.db.WithContext(ctx).Where("owner_user_id = ?", userID).Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]StudyDescriptor, 0, len(rows))
	for _, row := range rows {
		result = append(result, s.describe(ctx, row))
	}
	return result, nil
}

func (s *Service) CompatibleArtifacts(ctx context.Context, userID uint, instrumentID, dataSource, symbol, interval, datasetHash string, horizon int) ([]CompatibleArtifact, error) {
	query := s.db.WithContext(ctx).Where("owner_user_id = ? AND status = ? AND artifact_set_hash <> ''", userID, StudyStatusCompleted)
	if instrumentID != "" {
		query = query.Where("instrument_id = ?", instrumentID)
	}
	if dataSource != "" {
		query = query.Where("data_source = ?", dataSource)
	}
	if symbol != "" {
		query = query.Where("symbol = ?", symbol)
	}
	if interval != "" {
		query = query.Where("interval = ?", interval)
	}
	if datasetHash != "" {
		query = query.Where("dataset_hash = ?", datasetHash)
	}
	var studies []saasstore.GeometryModelStudy
	if err := query.Order("completed_at DESC").Find(&studies).Error; err != nil {
		return nil, err
	}
	result := make([]CompatibleArtifact, 0)
	for _, study := range studies {
		artifactQuery := s.db.WithContext(ctx).Where("study_id = ?", study.ID)
		if horizon > 0 {
			artifactQuery = artifactQuery.Where("horizon = ?", horizon)
		}
		var artifacts []saasstore.GeometryModelArtifact
		if err := artifactQuery.Order("horizon ASC").Find(&artifacts).Error; err != nil {
			return nil, err
		}
		for _, artifact := range artifacts {
			result = append(result, CompatibleArtifact{StudyID: study.ID, StudyName: study.Name, ArtifactID: artifact.ID, Horizon: artifact.Horizon, Lookback: artifact.Lookback, InstrumentID: study.InstrumentID, DataSource: study.DataSource, Symbol: study.Symbol, Interval: study.Interval, DatasetHash: study.DatasetHash, SchemaVersion: artifact.SchemaVersion, ContentHash: artifact.ContentHash, Status: study.Status})
		}
	}
	return result, nil
}
func (s *Service) Get(ctx context.Context, userID, id uint) (StudyDescriptor, error) {
	var study saasstore.GeometryModelStudy
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, userID).First(&study).Error; err != nil {
		return StudyDescriptor{}, ErrNotFound
	}
	if study.ComputeTaskID != nil {
		if task, err := s.tasks.Get(ctx, userID, *study.ComputeTaskID); err == nil && task.Status != compute.TaskStatusRunning && task.Status != compute.TaskStatusQueued {
			if study.ArtifactSetHash == "" {
				_ = s.sync(ctx, &study, task)
			}
		}
	}
	return s.describe(ctx, study), nil
}
func (s *Service) sync(ctx context.Context, study *saasstore.GeometryModelStudy, task *computetask.TaskDescriptor) error {
	if task.Status != compute.TaskStatusCompleted {
		return s.db.WithContext(ctx).Model(study).Updates(map[string]any{"status": taskStatus(task.Status), "error_message": task.Error}).Error
	}
	var items []saasstore.ComputeTaskItem
	if err := s.db.WithContext(ctx).Where("compute_task_id = ? AND status IN ?", task.ID, []string{compute.ItemStatusCompleted, compute.ItemStatusCached}).Order("item_index ASC").Find(&items).Error; err != nil {
		return err
	}
	if len(items) != 2 {
		return fmt.Errorf("幾何模型訓練結果不完整")
	}
	var results []TrainResult
	for _, item := range items {
		var result TrainResult
		if err := json.Unmarshal(item.Result, &result); err != nil {
			return err
		}
		results = append(results, result)
	}
	identity := []map[string]any{}
	for _, result := range results {
		modelRaw, _ := compute.CanonicalJSON(result.Training)
		hash := compute.HashBytes(modelRaw)
		artifact := saasstore.GeometryModelArtifact{StudyID: study.ID, ArtifactKey: fmt.Sprintf("horizon-%d", result.Horizon), SchemaVersion: core.GeometryModelSchemaVersion, Horizon: result.Horizon, Lookback: result.Training.SelectedModel.Lookback, DatasetHash: study.DatasetHash, TrainingStartTimeMs: study.TrainStartTimeMs, TrainingEndTimeMs: study.TrainEndTimeMs, ContentHash: hash, Payload: modelRaw}
		if err := s.db.WithContext(ctx).Create(&artifact).Error; err != nil {
			return err
		}
		identity = append(identity, map[string]any{"horizon": result.Horizon, "hash": hash})
	}
	snapshotPayload := SnapshotPayload{SchemaVersion: core.GeometryModelSchemaVersion}
	for _, result := range results {
		if result.Horizon == core.HorizonOneDay {
			snapshotPayload.OneDay = result.Predictions
		} else {
			snapshotPayload.TwentyDay = result.Predictions
		}
	}
	payloadRaw, _ := compute.CanonicalJSON(snapshotPayload)
	snapshotHash := compute.HashBytes(payloadRaw)
	snapshot := saasstore.GeometryPredictionSnapshot{StudyID: study.ID, SchemaVersion: core.GeometryModelSchemaVersion, ArtifactSetHash: compute.HashBytes(mustCanonical(identity)), DatasetHash: study.DatasetHash, ContentHash: snapshotHash, Payload: payloadRaw}
	if err := s.db.WithContext(ctx).Create(&snapshot).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	artifactSetHash := compute.HashBytes(mustCanonical(identity))
	if err := s.db.WithContext(ctx).Model(study).Updates(map[string]any{"artifact_set_hash": artifactSetHash, "prediction_id": snapshot.ID, "status": StudyStatusCompleted, "completed_at": now, "error_message": ""}).Error; err != nil {
		return err
	}
	study.ArtifactSetHash, study.PredictionID, study.Status, study.CompletedAt = artifactSetHash, &snapshot.ID, StudyStatusCompleted, &now
	return nil
}
func (s *Service) describe(ctx context.Context, study saasstore.GeometryModelStudy) StudyDescriptor {
	descriptor := StudyDescriptor{ID: study.ID, Name: study.Name, Status: study.Status, StudyKey: study.StudyKey, SettingHash: study.SettingHash, DatasetHash: study.DatasetHash, ComputeTaskID: study.ComputeTaskID, ArtifactSetHash: study.ArtifactSetHash, PredictionID: study.PredictionID, CreatedAt: study.CreatedAt.UTC().Format(time.RFC3339), ErrorMessage: study.ErrorMessage, InstrumentID: study.InstrumentID, DataSource: study.DataSource, Symbol: study.Symbol, Interval: study.Interval}
	if study.CompletedAt != nil {
		descriptor.CompletedAt = study.CompletedAt.UTC().Format(time.RFC3339)
	}
	if study.ArtifactSetHash != "" {
		var rows []saasstore.GeometryModelArtifact
		if err := s.db.WithContext(ctx).Where("study_id = ?", study.ID).Order("horizon ASC").Find(&rows).Error; err == nil {
			for _, row := range rows {
				var training core.GeometryTrainingResult
				if json.Unmarshal(row.Payload, &training) == nil {
					descriptor.Artifacts = append(descriptor.Artifacts, ArtifactDescriptor{ID: row.ID, Horizon: row.Horizon, Lookback: row.Lookback, ContentHash: row.ContentHash, Report: training.SelectedModel.Report, Model: training.SelectedModel, Training: training})
				}
			}
		}
		var snapshot saasstore.GeometryPredictionSnapshot
		if study.PredictionID != nil && s.db.WithContext(ctx).First(&snapshot, *study.PredictionID).Error == nil {
			var payload SnapshotPayload
			if json.Unmarshal(snapshot.Payload, &payload) == nil {
				descriptor.Predictions = &payload
			}
		}
	}
	return descriptor
}
func taskStatus(status string) string {
	switch status {
	case compute.TaskStatusPlanned:
		return StudyStatusPlanned
	case compute.TaskStatusRunning:
		return StudyStatusTraining
	case compute.TaskStatusCompleted:
		return StudyStatusCompleted
	case compute.TaskStatusCancelled:
		return StudyStatusCancelled
	default:
		return StudyStatusFailed
	}
}
func mustCanonical(value any) []byte { raw, _ := compute.CanonicalJSON(value); return raw }
