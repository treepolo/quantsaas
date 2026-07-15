package dynamicparam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	core "quantsaas/internal/dynamicparam"
	"quantsaas/internal/quant"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/gorm"
)

const reportFormulaVersion = "p09-report-formula-v1"

type Service struct {
	db           *gorm.DB
	computeTasks *computetask.Service
}

func NewService(db *gorm.DB, tasks *computetask.Service) *Service {
	return &Service{db: db, computeTasks: tasks}
}

func (s *Service) Preview(ctx context.Context, userID uint, req CreateStudyRequest) (computetask.PlanPreview, error) {
	prepared, err := s.prepare(ctx, req)
	if err != nil {
		return computetask.PlanPreview{}, err
	}
	if s.computeTasks == nil {
		return computetask.PlanPreview{}, computetask.ErrServiceUnavailable
	}
	return s.computeTasks.Preview(ctx, userID, prepared.spec)
}

func (s *Service) Create(ctx context.Context, userID uint, req CreateStudyRequest) (CreateStudyResponse, error) {
	if userID == 0 || s.computeTasks == nil {
		return CreateStudyResponse{}, computetask.ErrServiceUnavailable
	}
	prepared, err := s.prepare(ctx, req)
	if err != nil {
		return CreateStudyResponse{}, err
	}
	preview, err := s.computeTasks.Preview(ctx, userID, prepared.spec)
	if err != nil {
		return CreateStudyResponse{}, err
	}
	studyKey := "p09-study:" + compute.HashBytes(prepared.settingRaw)
	var study saasstore.DynamicModelStudy
	find := s.db.WithContext(ctx).Where("owner_user_id = ? AND study_key = ?", userID, studyKey).First(&study)
	if find.Error == nil {
		descriptor, err := s.Get(ctx, userID, study.ID)
		if err != nil {
			return CreateStudyResponse{}, err
		}
		var task *computetask.TaskDescriptor
		if study.ComputeTaskID != nil {
			task, _ = s.computeTasks.Get(ctx, userID, *study.ComputeTaskID)
		}
		return CreateStudyResponse{Study: descriptor, Preview: preview, Task: task}, nil
	}
	if !errors.Is(find.Error, gorm.ErrRecordNotFound) {
		return CreateStudyResponse{}, find.Error
	}
	study = saasstore.DynamicModelStudy{
		OwnerUserID: userID, StudyKey: studyKey, Name: prepared.request.Name, Status: StudyStatusPlanned,
		Route: prepared.request.Route, InstrumentID: prepared.scope.InstrumentID, DataSource: prepared.scope.DataSource,
		Symbol: prepared.scope.Symbol, Interval: prepared.scope.Interval, ExecutionMode: prepared.request.ExecutionMode,
		TrainStartTimeMs: prepared.scope.StartTimeMs, TrainEndTimeMs: prepared.scope.EndTimeMs, DatasetHash: prepared.scope.DatasetHash,
		SettingVersion: StudySettingVersion, SettingHash: compute.HashBytes(prepared.settingRaw), Settings: saasstore.JSONB(prepared.settingRaw),
	}
	if err := s.db.WithContext(ctx).Create(&study).Error; err != nil {
		return CreateStudyResponse{}, err
	}
	task, err := s.computeTasks.Create(ctx, userID, prepared.spec, prepared.request.ConfirmSoftLimit)
	if err != nil {
		_ = s.db.WithContext(ctx).Delete(&study).Error
		return CreateStudyResponse{}, err
	}
	if err := s.db.WithContext(ctx).Model(&study).Updates(map[string]any{"compute_task_id": task.ID, "status": taskStudyStatus(task.Status, false)}).Error; err != nil {
		return CreateStudyResponse{}, err
	}
	study.ComputeTaskID = &task.ID
	study.Status = taskStudyStatus(task.Status, false)
	if task.Status == compute.TaskStatusCompleted {
		if err := s.syncTraining(ctx, &study, task); err != nil {
			return CreateStudyResponse{}, err
		}
	}
	descriptor, err := s.describe(ctx, study, true)
	return CreateStudyResponse{Study: descriptor, Preview: preview, Task: task}, err
}

type preparedStudy struct {
	request    CreateStudyRequest
	scope      MarketScope
	settingRaw []byte
	spec       computetask.CreateSpec
}

func (s *Service) prepare(ctx context.Context, req CreateStudyRequest) (preparedStudy, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "動態參數模型研究"
	}
	req.Symbol = strings.ToUpper(strings.TrimSpace(req.Symbol))
	req.InstrumentID = strings.TrimSpace(req.InstrumentID)
	req.DataSource = strings.TrimSpace(req.DataSource)
	req.Interval = strings.TrimSpace(req.Interval)
	req.ExecutionMode = strings.TrimSpace(req.ExecutionMode)
	if req.GenomeID == 0 || req.InstrumentID == "" || req.DataSource == "" || req.Symbol == "" || req.Interval != "1d" || req.TrainStartTimeMs <= 0 || req.TrainEndTimeMs <= req.TrainStartTimeMs {
		return preparedStudy{}, ErrInvalidRequest
	}
	if req.Route != core.RouteExplainable && req.Route != core.RouteTCN {
		return preparedStudy{}, ErrInvalidRequest
	}
	if req.ExecutionMode == "" {
		req.ExecutionMode = saasstore.ExecutionModeCloseNextOpen
	}
	if req.ExecutionMode != saasstore.ExecutionModeCloseSameBar && req.ExecutionMode != saasstore.ExecutionModeCloseNextOpen {
		return preparedStudy{}, ErrInvalidRequest
	}
	if len(req.Lookbacks) == 0 {
		req.Lookbacks = []int{5, 10, 20, 40, 60, 120, 250, 500}
	}
	if req.Folds == 0 {
		req.Folds = 4
	}
	if req.MinimumTrain == 0 {
		req.MinimumTrain = 120
	}
	if req.ActivityKappa <= 0 {
		req.ActivityKappa = 20
	}
	if req.RegionRule.DirectionBoundary == 0 {
		req.RegionRule = core.RegionRule{DirectionBoundary: 0.2, MagnitudeBoundary: 1}
	}
	if req.Policy.SchemaVersion == "" {
		req.Policy = core.DynamicPolicy{SchemaVersion: core.PolicySchemaVersion, Version: "p09-fixed-policy-v1"}
	}
	if err := core.ValidatePolicy(req.Policy); err != nil {
		return preparedStudy{}, err
	}
	training := core.TrainingConfig{
		Route: req.Route, Lookbacks: req.Lookbacks, Folds: req.Folds, MinimumTrain: req.MinimumTrain,
		RegionRule: req.RegionRule, ActivityKappa: req.ActivityKappa,
		Learner: core.LearnerConfig{Route: req.Route, GAM: core.GAMConfig{Interactions: true, L1Penalty: 0.0001, L2Penalty: 0.0001, Epochs: 250, LearningRate: 0.02}, TCN: core.TCNConfig{Hidden: 4, KernelSize: 2, Dilations: []int{1, 2, 4, 8}, Epochs: 120, LearningRate: 0.01, L2Penalty: 0.0001}},
	}
	if err := core.ValidateTrainingConfig(training); err != nil {
		return preparedStudy{}, err
	}
	var gene saasstore.GeneRecord
	if err := s.db.WithContext(ctx).Where("id = ? AND strategy_id = ?", req.GenomeID, sigmoiddca.StrategyID).First(&gene).Error; err != nil {
		return preparedStudy{}, ErrInvalidRequest
	}
	base := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
	if err := quant.ValidateChromosome(base.Chromosome); err != nil {
		return preparedStudy{}, err
	}
	bars, err := queryBars(ctx, s.db, MarketScope{InstrumentID: req.InstrumentID, DataSource: req.DataSource, Symbol: req.Symbol, Interval: req.Interval, StartTimeMs: req.TrainStartTimeMs, EndTimeMs: req.TrainEndTimeMs})
	if err != nil {
		return preparedStudy{}, err
	}
	minimumRequired := req.MinimumTrain + core.HorizonTwentyDay + 2
	if len(bars) < minimumRequired {
		return preparedStudy{}, fmt.Errorf("P09 至少需要 %d 根日 K，實際只有 %d 根", minimumRequired, len(bars))
	}
	datasetHash, err := backtestresult.HashDataset(backtestresult.DatasetSchemaVersion, bars)
	if err != nil {
		return preparedStudy{}, err
	}
	scope := MarketScope{InstrumentID: req.InstrumentID, DataSource: req.DataSource, Symbol: req.Symbol, Interval: req.Interval, StartTimeMs: bars[0].OpenTime, EndTimeMs: bars[len(bars)-1].OpenTime, DatasetHash: datasetHash}
	paramRaw, err := compute.CanonicalJSON(base)
	if err != nil {
		return preparedStudy{}, err
	}
	settingRequest := req
	settingRequest.Name = ""
	settingRequest.ConfirmSoftLimit = false
	settingRequest.StateRules = core.StateRules{}
	setting := StudySetting{Version: StudySettingVersion, Request: settingRequest, DatasetHash: datasetHash, Training: training, BaseParameterHash: compute.HashBytes(paramRaw)}
	settingRaw, err := compute.CanonicalJSON(setting)
	if err != nil {
		return preparedStudy{}, err
	}
	items := make([]compute.ManifestItemInput, 0, 2)
	multiplier := int64(1)
	if req.Route == core.RouteTCN {
		multiplier = 4
	}
	for _, horizon := range []int{core.HorizonOneDay, core.HorizonTwentyDay} {
		input := TrainExecutionInput{SchemaVersion: TrainInputVersion, Horizon: horizon, Scope: scope, Training: training}
		raw, err := compute.CanonicalJSON(input)
		if err != nil {
			return preparedStudy{}, err
		}
		items = append(items, compute.ManifestItemInput{Key: fmt.Sprintf("horizon-%d", horizon), CacheKey: "p09-train:" + compute.HashBytes(raw), Input: raw, EstimatedUnits: int64(len(bars)*len(req.Lookbacks)) * multiplier})
	}
	spec := computetask.CreateSpec{Kind: compute.TaskKindAtomic, TaskType: "p09.dynamic-model.train", Title: req.Name, ExecutorType: TrainExecutorType, Settings: setting, ResearchSettingID: fmt.Sprintf("p09-genome:%d", req.GenomeID), ResearchSettingHash: compute.HashBytes(settingRaw), StageKey: "training", StageType: req.Route, Items: items}
	return preparedStudy{request: req, scope: scope, settingRaw: settingRaw, spec: spec}, nil
}

func queryBars(ctx context.Context, db *gorm.DB, scope MarketScope) ([]quant.Bar, error) {
	var rows []saasstore.KLine
	query := db.WithContext(ctx).Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time >= ? AND open_time <= ?", scope.InstrumentID, scope.DataSource, scope.Symbol, scope.Interval, scope.StartTimeMs, scope.EndTimeMs).Order("open_time ASC").Find(&rows)
	if query.Error != nil {
		return nil, query.Error
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("P09 訓練區間沒有行情資料")
	}
	bars := make([]quant.Bar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, quant.Bar{OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	return bars, nil
}

func (s *Service) List(ctx context.Context, userID uint, limit int) ([]StudyDescriptor, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []saasstore.DynamicModelStudy
	if err := s.db.WithContext(ctx).Where("owner_user_id = ?", userID).Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]StudyDescriptor, 0, len(rows))
	for _, row := range rows {
		descriptor, err := s.describe(ctx, row, false)
		if err != nil {
			return nil, err
		}
		result = append(result, descriptor)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, userID, studyID uint) (StudyDescriptor, error) {
	var study saasstore.DynamicModelStudy
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", studyID, userID).First(&study).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return StudyDescriptor{}, ErrNotFound
		}
		return StudyDescriptor{}, err
	}
	if study.ComputeTaskID != nil && study.ArtifactSetHash == "" && s.computeTasks != nil {
		if task, err := s.computeTasks.Get(ctx, userID, *study.ComputeTaskID); err == nil {
			if err := s.syncTraining(ctx, &study, task); err != nil {
				return StudyDescriptor{}, err
			}
		}
	}
	if study.MaterializationTaskID != nil && study.MaterializationID == nil && s.computeTasks != nil {
		if task, err := s.computeTasks.Get(ctx, userID, *study.MaterializationTaskID); err == nil {
			if err := s.syncMaterialization(ctx, &study, task); err != nil {
				return StudyDescriptor{}, err
			}
		}
	}
	return s.describe(ctx, study, true)
}

func (s *Service) syncTraining(ctx context.Context, study *saasstore.DynamicModelStudy, task *computetask.TaskDescriptor) error {
	if study.ComputeTaskID == nil || *study.ComputeTaskID != task.ID {
		return ErrInvalidRequest
	}
	if task.Status != compute.TaskStatusCompleted {
		status := taskStudyStatus(task.Status, false)
		return s.db.WithContext(ctx).Model(study).Updates(map[string]any{"status": status, "error_message": task.Error}).Error
	}
	if study.ArtifactSetHash != "" {
		return nil
	}
	var items []saasstore.ComputeTaskItem
	if err := s.db.WithContext(ctx).Where("compute_task_id = ? AND status IN ?", task.ID, []string{compute.ItemStatusCompleted, compute.ItemStatusCached}).Order("item_index ASC").Find(&items).Error; err != nil {
		return err
	}
	if len(items) != 2 {
		return fmt.Errorf("P09 訓練結果不完整")
	}
	models := map[int]core.HorizonModel{}
	for _, item := range items {
		var result TrainExecutionResult
		if err := json.Unmarshal(item.Result, &result); err != nil || result.SchemaVersion != TrainResultVersion || result.DatasetHash != study.DatasetHash {
			return fmt.Errorf("P09 訓練結果格式無效")
		}
		models[result.Horizon] = result.Model
	}
	one, oneOK := models[core.HorizonOneDay]
	twenty, twentyOK := models[core.HorizonTwentyDay]
	if !oneOK || !twentyOK || twenty.StructuralRules == nil {
		return fmt.Errorf("P09 缺少完整模型或狀態規則")
	}
	var setting StudySetting
	if err := json.Unmarshal(study.Settings, &setting); err != nil {
		return err
	}
	var gene saasstore.GeneRecord
	if err := s.db.WithContext(ctx).First(&gene, setting.Request.GenomeID).Error; err != nil {
		return err
	}
	base := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked saasstore.DynamicModelStudy
		if err := tx.Where("id = ?", study.ID).First(&locked).Error; err != nil {
			return err
		}
		if locked.ArtifactSetHash != "" {
			*study = locked
			return nil
		}
		artifactRows, reports, err := persistModelArtifacts(tx, locked, one, twenty)
		if err != nil {
			return err
		}
		identities := make([]map[string]any, 0, len(artifactRows))
		for _, artifact := range artifactRows {
			identities = append(identities, map[string]any{"key": artifact.ArtifactKey, "hash": artifact.ContentHash})
		}
		identityRaw, _ := compute.CanonicalJSON(identities)
		artifactSetHash := compute.HashBytes(identityRaw)
		snapshot, err := persistPredictionSnapshot(tx, locked, artifactSetHash, one.OOF, twenty.OOF)
		if err != nil {
			return err
		}
		policyBundle := PolicyBundle{SchemaVersion: core.PolicySchemaVersion, StateRules: *twenty.StructuralRules, Policy: setting.Request.Policy, BaseChromosome: base.Chromosome, ModelVersion: core.ModelArtifactVersion}
		policyRaw, err := compute.CanonicalJSON(policyBundle)
		if err != nil {
			return err
		}
		policyHash := compute.HashBytes(policyRaw)
		space, err := core.BuildParameterSpace(setting.Request.Policy, artifactSetHash)
		if err != nil {
			return err
		}
		spaceRaw, _ := compute.CanonicalJSON(space)
		policy := saasstore.DynamicPolicyArtifact{OwnerUserID: locked.OwnerUserID, StudyID: locked.ID, PolicyKey: "p09-policy:" + policyHash, SchemaVersion: core.PolicySchemaVersion, ArtifactSetHash: artifactSetHash, PredictionSnapshotID: snapshot.ID, ContentHash: policyHash, Payload: policyRaw, ParameterSpaceVersion: core.ParameterSpaceVersion, ParameterSpaceHash: compute.HashBytes(spaceRaw), ParameterSpace: spaceRaw}
		if err := tx.Create(&policy).Error; err != nil {
			return err
		}
		reportSnapshot, err := persistModelReportSnapshot(tx, locked, artifactSetHash, snapshot, policy.ID, reports, one, twenty)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		updates := map[string]any{"artifact_set_hash": artifactSetHash, "prediction_snapshot_id": snapshot.ID, "policy_artifact_id": policy.ID, "report_snapshot_id": reportSnapshot.ID, "status": StudyStatusAwaitingMaterialization, "completed_at": now, "error_code": "", "error_message": ""}
		if err := tx.Model(&locked).Updates(updates).Error; err != nil {
			return err
		}
		locked.ArtifactSetHash, locked.PredictionSnapshotID, locked.PolicyArtifactID, locked.ReportSnapshotID = artifactSetHash, &snapshot.ID, &policy.ID, &reportSnapshot.ID
		locked.Status, locked.CompletedAt = StudyStatusAwaitingMaterialization, &now
		*study = locked
		return nil
	})
}

func persistModelArtifacts(tx *gorm.DB, study saasstore.DynamicModelStudy, one, twenty core.HorizonModel) ([]saasstore.DynamicModelArtifact, []core.ModelReport, error) {
	models := []core.HorizonModel{one, twenty}
	rows := make([]saasstore.DynamicModelArtifact, 0, 8)
	reports := make([]core.ModelReport, 0, 6)
	for _, model := range models {
		bundle := model
		bundle.OOF = nil
		bundleRaw, err := compute.CanonicalJSON(bundle)
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, saasstore.DynamicModelArtifact{StudyID: study.ID, ArtifactKey: fmt.Sprintf("horizon-%d", model.Horizon), SchemaVersion: core.ModelArtifactVersion, Route: study.Route, Horizon: model.Horizon, TargetKind: "horizon_bundle", Lookback: model.Activity.Lookback, DatasetHash: study.DatasetHash, TrainingStartTimeMs: study.TrainStartTimeMs, TrainingEndTimeMs: study.TrainEndTimeMs, ContentHash: compute.HashBytes(bundleRaw), Payload: bundleRaw})
		for _, target := range []core.TargetModel{model.Direction, model.Joint, model.Activity} {
			artifact := target
			artifact.OOF = nil
			raw, err := compute.CanonicalJSON(artifact)
			if err != nil {
				return nil, nil, err
			}
			hash := compute.HashBytes(raw)
			rows = append(rows, saasstore.DynamicModelArtifact{StudyID: study.ID, ArtifactKey: fmt.Sprintf("horizon-%d-%s", model.Horizon, target.TargetKind), SchemaVersion: core.ModelArtifactVersion, Route: study.Route, Horizon: model.Horizon, TargetKind: target.TargetKind, Lookback: target.Lookback, DatasetHash: study.DatasetHash, TrainingStartTimeMs: study.TrainStartTimeMs, TrainingEndTimeMs: study.TrainEndTimeMs, ContentHash: hash, Payload: raw})
			report := target.Report
			report.ArtifactHash = hash
			report.ContentHash = ""
			reportRaw, _ := compute.CanonicalJSON(report)
			report.ContentHash = compute.HashBytes(reportRaw)
			reports = append(reports, report)
		}
	}
	if err := tx.Create(&rows).Error; err != nil {
		return nil, nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ArtifactKey < rows[j].ArtifactKey })
	return rows, reports, nil
}

func persistPredictionSnapshot(tx *gorm.DB, study saasstore.DynamicModelStudy, artifactSetHash string, one, twenty []core.Prediction) (saasstore.DynamicPredictionSnapshot, error) {
	count := len(one) + len(twenty)
	if count == 0 {
		return saasstore.DynamicPredictionSnapshot{}, fmt.Errorf("P09 沒有 OOF 預測")
	}
	start, end := study.TrainEndTimeMs, study.TrainStartTimeMs
	for _, predictions := range [][]core.Prediction{one, twenty} {
		for _, prediction := range predictions {
			if prediction.TimeMs < start {
				start = prediction.TimeMs
			}
			if prediction.TimeMs > end {
				end = prediction.TimeMs
			}
		}
	}
	snapshot := saasstore.DynamicPredictionSnapshot{StudyID: study.ID, SnapshotKey: "pending", SchemaVersion: core.PredictionSchemaVersion, ArtifactSetHash: artifactSetHash, DatasetHash: study.DatasetHash, PredictionCount: count, StartTimeMs: start, EndTimeMs: end, BlockManifestHash: "pending", BlockManifest: saasstore.JSONB(`[]`), ContentHash: "pending"}
	if err := tx.Create(&snapshot).Error; err != nil {
		return snapshot, err
	}
	manifest := make([]map[string]any, 0)
	blockIndex := 0
	for oneOffset, twentyOffset := 0, 0; oneOffset < len(one) || twentyOffset < len(twenty); blockIndex++ {
		oneEnd, twentyEnd := minInt(oneOffset+256, len(one)), minInt(twentyOffset+256, len(twenty))
		payload := PredictionBlockPayload{SchemaVersion: core.PredictionSchemaVersion, OneDay: append([]core.Prediction(nil), one[oneOffset:oneEnd]...), TwentyDay: append([]core.Prediction(nil), twenty[twentyOffset:twentyEnd]...)}
		raw, _ := compute.CanonicalJSON(payload)
		blockStart, blockEnd := int64(0), int64(0)
		for _, predictions := range [][]core.Prediction{payload.OneDay, payload.TwentyDay} {
			for _, prediction := range predictions {
				if blockStart == 0 || prediction.TimeMs < blockStart {
					blockStart = prediction.TimeMs
				}
				if prediction.TimeMs > blockEnd {
					blockEnd = prediction.TimeMs
				}
			}
		}
		block := saasstore.DynamicReportBlock{StudyID: study.ID, OwnerKind: "prediction_snapshot", OwnerID: snapshot.ID, BlockID: fmt.Sprintf("oof-%04d", blockIndex), BlockKind: "oof_predictions", SchemaVersion: core.PredictionSchemaVersion, FormulaVersion: core.PredictionSchemaVersion, BlockIndex: blockIndex, StartTimeMs: blockStart, EndTimeMs: blockEnd, PointCount: len(payload.OneDay) + len(payload.TwentyDay), ContentHash: compute.HashBytes(raw), Payload: raw}
		if err := tx.Create(&block).Error; err != nil {
			return snapshot, err
		}
		manifest = append(manifest, map[string]any{"block_id": block.BlockID, "content_hash": block.ContentHash, "point_count": block.PointCount})
		oneOffset, twentyOffset = oneEnd, twentyEnd
	}
	manifestRaw, _ := compute.CanonicalJSON(manifest)
	identityRaw, _ := compute.CanonicalJSON(map[string]any{"schema_version": core.PredictionSchemaVersion, "artifact_set_hash": artifactSetHash, "dataset_hash": study.DatasetHash, "manifest_hash": compute.HashBytes(manifestRaw)})
	snapshot.SnapshotKey = "p09-oof:" + compute.HashBytes(identityRaw)
	snapshot.BlockManifest, snapshot.BlockManifestHash, snapshot.ContentHash = manifestRaw, compute.HashBytes(manifestRaw), compute.HashBytes(identityRaw)
	if err := tx.Model(&snapshot).Updates(map[string]any{"snapshot_key": snapshot.SnapshotKey, "block_manifest": snapshot.BlockManifest, "block_manifest_hash": snapshot.BlockManifestHash, "content_hash": snapshot.ContentHash}).Error; err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func persistModelReportSnapshot(tx *gorm.DB, study saasstore.DynamicModelStudy, artifactSetHash string, prediction saasstore.DynamicPredictionSnapshot, policyID uint, reports []core.ModelReport, one, twenty core.HorizonModel) (saasstore.DynamicModelReportSnapshot, error) {
	snapshot := saasstore.DynamicModelReportSnapshot{StudyID: study.ID, SnapshotKey: "pending", SchemaVersion: core.ModelReportVersion, FormulaVersion: reportFormulaVersion, ArtifactSetHash: artifactSetHash, PredictionSnapshotID: prediction.ID, PolicyArtifactID: &policyID, ActualStartTimeMs: prediction.StartTimeMs, ActualEndTimeMs: prediction.EndTimeMs, Completeness: "model_ready", BlockManifestHash: "pending", BlockManifest: saasstore.JSONB(`[]`), ContentHash: "pending"}
	if err := tx.Create(&snapshot).Error; err != nil {
		return snapshot, err
	}
	payloads := []struct {
		id, kind, schema string
		value            any
		points           int
	}{
		{"model-validation", "model_validation", core.ModelReportVersion, reports, len(reports)},
		{"calibration", "calibration_reliability", core.CalibrationVersion, reports, len(reports)},
		{"space-state", "space_state", core.SpaceStateVersion, []core.SpaceRuleReport{one.SpaceReport, twenty.SpaceReport}, 2},
		{"structure-state", "structure_state", core.StateRuleReportVersion, twenty.StructuralReport, 1},
	}
	manifest := make([]map[string]any, 0, len(payloads))
	for index, payload := range payloads {
		raw, _ := compute.CanonicalJSON(payload.value)
		block := saasstore.DynamicReportBlock{StudyID: study.ID, OwnerKind: "report_snapshot", OwnerID: snapshot.ID, BlockID: payload.id, BlockKind: payload.kind, SchemaVersion: payload.schema, FormulaVersion: reportFormulaVersion, BlockIndex: index, StartTimeMs: prediction.StartTimeMs, EndTimeMs: prediction.EndTimeMs, PointCount: payload.points, ContentHash: compute.HashBytes(raw), Payload: raw}
		if err := tx.Create(&block).Error; err != nil {
			return snapshot, err
		}
		manifest = append(manifest, map[string]any{"block_id": block.BlockID, "block_kind": block.BlockKind, "content_hash": block.ContentHash})
	}
	manifestRaw, _ := compute.CanonicalJSON(manifest)
	identityRaw, _ := compute.CanonicalJSON(map[string]any{"artifact_set_hash": artifactSetHash, "prediction_hash": prediction.ContentHash, "policy_id": policyID, "manifest_hash": compute.HashBytes(manifestRaw)})
	snapshot.SnapshotKey = "p09-report:" + compute.HashBytes(identityRaw)
	snapshot.BlockManifest, snapshot.BlockManifestHash, snapshot.ContentHash = manifestRaw, compute.HashBytes(manifestRaw), compute.HashBytes(identityRaw)
	if err := tx.Model(&snapshot).Updates(map[string]any{"snapshot_key": snapshot.SnapshotKey, "block_manifest": snapshot.BlockManifest, "block_manifest_hash": snapshot.BlockManifestHash, "content_hash": snapshot.ContentHash}).Error; err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (s *Service) Materialize(ctx context.Context, userID, studyID uint, req MaterializeRequest) (MaterializeResponse, error) {
	if s.computeTasks == nil {
		return MaterializeResponse{}, computetask.ErrServiceUnavailable
	}
	descriptor, err := s.Get(ctx, userID, studyID)
	if err != nil {
		return MaterializeResponse{}, err
	}
	if descriptor.Status != StudyStatusAwaitingMaterialization || descriptor.PredictionSnapshotID == nil || descriptor.PolicyArtifactID == nil {
		return MaterializeResponse{}, ErrInvalidRequest
	}
	var study saasstore.DynamicModelStudy
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", studyID, userID).First(&study).Error; err != nil {
		return MaterializeResponse{}, err
	}
	var setting StudySetting
	if err := json.Unmarshal(study.Settings, &setting); err != nil {
		return MaterializeResponse{}, err
	}
	var prediction saasstore.DynamicPredictionSnapshot
	if err := s.db.WithContext(ctx).First(&prediction, *study.PredictionSnapshotID).Error; err != nil {
		return MaterializeResponse{}, err
	}
	var policy saasstore.DynamicPolicyArtifact
	if err := s.db.WithContext(ctx).First(&policy, *study.PolicyArtifactID).Error; err != nil {
		return MaterializeResponse{}, err
	}
	var gene saasstore.GeneRecord
	if err := s.db.WithContext(ctx).First(&gene, setting.Request.GenomeID).Error; err != nil {
		return MaterializeResponse{}, err
	}
	backtestRequest := backtest.CreateRequest{StrategyID: sigmoiddca.StrategyID, InstrumentID: study.InstrumentID, DataSource: study.DataSource, ExecutionMode: study.ExecutionMode, StartTimeMs: study.TrainStartTimeMs, EndTimeMs: study.TrainEndTimeMs, Symbol: study.Symbol, Pair: study.Symbol, Interval: study.Interval, Source: backtest.SourceCustom, CustomParams: json.RawMessage(gene.ParamPack), LongTermFilterEnabled: &setting.Request.LongTermFilterEnabled, LongTermFilterMonths: setting.Request.LongTermFilterMonths}
	input := MaterializeExecutionInput{SchemaVersion: MaterializeInputVersion, StudyID: study.ID, ArtifactSetHash: study.ArtifactSetHash, PredictionSnapshotHash: prediction.ContentHash, PolicyHash: policy.ContentHash, Scope: MarketScope{InstrumentID: study.InstrumentID, DataSource: study.DataSource, Symbol: study.Symbol, Interval: study.Interval, StartTimeMs: study.TrainStartTimeMs, EndTimeMs: study.TrainEndTimeMs, DatasetHash: study.DatasetHash}, Backtest: backtestRequest}
	raw, _ := compute.CanonicalJSON(input)
	spec := computetask.CreateSpec{Kind: compute.TaskKindAtomic, TaskType: "p09.dynamic-model.materialize", Title: study.Name + "：物化與回測", ExecutorType: MaterializeExecutorType, Settings: input, ResearchSettingID: fmt.Sprintf("p09-study:%d", study.ID), ResearchSettingHash: study.SettingHash, StageKey: "materialization", StageType: "dynamic-backtest", Items: []compute.ManifestItemInput{{Key: "materialize", CacheKey: "p09-materialize:" + compute.HashBytes(raw), Input: raw, EstimatedUnits: int64(prediction.PredictionCount)}}}
	task, err := s.computeTasks.Create(ctx, userID, spec, req.ConfirmSoftLimit)
	if err != nil {
		return MaterializeResponse{}, err
	}
	if err := s.db.WithContext(ctx).Model(&study).Updates(map[string]any{"materialization_task_id": task.ID, "status": taskStudyStatus(task.Status, true)}).Error; err != nil {
		return MaterializeResponse{}, err
	}
	study.MaterializationTaskID, study.Status = &task.ID, taskStudyStatus(task.Status, true)
	if task.Status == compute.TaskStatusCompleted {
		if err := s.syncMaterialization(ctx, &study, task); err != nil {
			return MaterializeResponse{}, err
		}
	}
	updated, err := s.describe(ctx, study, true)
	return MaterializeResponse{Study: updated, Task: task}, err
}

func (s *Service) syncMaterialization(ctx context.Context, study *saasstore.DynamicModelStudy, task *computetask.TaskDescriptor) error {
	if study.MaterializationTaskID == nil || *study.MaterializationTaskID != task.ID {
		return ErrInvalidRequest
	}
	if task.Status != compute.TaskStatusCompleted {
		return s.db.WithContext(ctx).Model(study).Updates(map[string]any{"status": taskStudyStatus(task.Status, true), "error_message": task.Error}).Error
	}
	if study.MaterializationID != nil {
		return nil
	}
	var item saasstore.ComputeTaskItem
	if err := s.db.WithContext(ctx).Where("compute_task_id = ? AND status IN ?", task.ID, []string{compute.ItemStatusCompleted, compute.ItemStatusCached}).First(&item).Error; err != nil {
		return err
	}
	var result MaterializeExecutionResult
	if err := json.Unmarshal(item.Result, &result); err != nil || result.SchemaVersion != MaterializeResultVersion {
		return fmt.Errorf("P09 物化結果格式無效")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked saasstore.DynamicModelStudy
		if err := tx.First(&locked, study.ID).Error; err != nil {
			return err
		}
		if locked.MaterializationID != nil {
			*study = locked
			return nil
		}
		var prediction saasstore.DynamicPredictionSnapshot
		if err := tx.First(&prediction, *locked.PredictionSnapshotID).Error; err != nil {
			return err
		}
		var policy saasstore.DynamicPolicyArtifact
		if err := tx.First(&policy, *locked.PolicyArtifactID).Error; err != nil {
			return err
		}
		pathRaw, _ := compute.CanonicalJSON(result.Materialized)
		manifest := []map[string]any{{"block_id": "daily-diagnostics", "content_hash": compute.HashBytes(pathRaw), "point_count": len(result.Materialized.Diagnostics)}}
		manifestRaw, _ := compute.CanonicalJSON(manifest)
		resultID := result.BacktestResultID
		materialization := saasstore.DynamicMaterialization{OwnerUserID: locked.OwnerUserID, MaterializationKey: "p09-materialized:" + result.ContentHash, SchemaVersion: core.PredictionSchemaVersion, StudyID: locked.ID, PredictionSnapshotID: prediction.ID, PolicyArtifactID: policy.ID, ContentHash: result.ContentHash, BlockManifestHash: compute.HashBytes(manifestRaw), BlockManifest: manifestRaw, BacktestResultID: &resultID, BacktestResultVersion: result.BacktestResultVersion, BacktestResultContentHash: result.BacktestResultContentHash}
		if err := tx.Create(&materialization).Error; err != nil {
			return err
		}
		block := saasstore.DynamicReportBlock{StudyID: locked.ID, OwnerKind: "materialization", OwnerID: materialization.ID, BlockID: "daily-diagnostics", BlockKind: "daily_diagnostics", SchemaVersion: core.EffectiveParameterVersion, FormulaVersion: reportFormulaVersion, BlockIndex: 0, StartTimeMs: locked.TrainStartTimeMs, EndTimeMs: locked.TrainEndTimeMs, PointCount: len(result.Materialized.Diagnostics), ContentHash: compute.HashBytes(pathRaw), Payload: pathRaw}
		if err := tx.Create(&block).Error; err != nil {
			return err
		}
		completedReport, err := persistCompletedReportSnapshot(tx, locked, prediction, policy, materialization, block)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&locked).Updates(map[string]any{"materialization_id": materialization.ID, "report_snapshot_id": completedReport.ID, "status": StudyStatusCompleted, "completed_at": now, "error_code": "", "error_message": ""}).Error; err != nil {
			return err
		}
		locked.MaterializationID = &materialization.ID
		locked.ReportSnapshotID = &completedReport.ID
		locked.Status = StudyStatusCompleted
		locked.CompletedAt = &now
		*study = locked
		return nil
	})
}

func persistCompletedReportSnapshot(tx *gorm.DB, study saasstore.DynamicModelStudy, prediction saasstore.DynamicPredictionSnapshot, policy saasstore.DynamicPolicyArtifact, materialization saasstore.DynamicMaterialization, daily saasstore.DynamicReportBlock) (saasstore.DynamicModelReportSnapshot, error) {
	if study.ReportSnapshotID == nil {
		return saasstore.DynamicModelReportSnapshot{}, fmt.Errorf("P09 缺少模型報告快照")
	}
	var sourceBlocks []saasstore.DynamicReportBlock
	if err := tx.Where("owner_kind = ? AND owner_id = ?", "report_snapshot", *study.ReportSnapshotID).Order("block_index ASC").Find(&sourceBlocks).Error; err != nil {
		return saasstore.DynamicModelReportSnapshot{}, err
	}
	snapshot := saasstore.DynamicModelReportSnapshot{StudyID: study.ID, SnapshotKey: "pending-complete", SchemaVersion: core.ModelReportVersion, FormulaVersion: reportFormulaVersion, ArtifactSetHash: study.ArtifactSetHash, PredictionSnapshotID: prediction.ID, PolicyArtifactID: &policy.ID, MaterializationID: &materialization.ID, ActualStartTimeMs: prediction.StartTimeMs, ActualEndTimeMs: prediction.EndTimeMs, Completeness: "complete", BlockManifestHash: "pending", BlockManifest: saasstore.JSONB(`[]`), ContentHash: "pending"}
	if err := tx.Create(&snapshot).Error; err != nil {
		return snapshot, err
	}
	manifest := make([]map[string]any, 0, len(sourceBlocks)+1)
	for index, source := range sourceBlocks {
		copy := source
		copy.ID = 0
		copy.CreatedAt = time.Time{}
		copy.OwnerID = snapshot.ID
		copy.BlockIndex = index
		if err := tx.Create(&copy).Error; err != nil {
			return snapshot, err
		}
		manifest = append(manifest, map[string]any{"block_id": copy.BlockID, "block_kind": copy.BlockKind, "content_hash": copy.ContentHash, "point_count": copy.PointCount})
	}
	dailyCopy := daily
	dailyCopy.ID = 0
	dailyCopy.CreatedAt = time.Time{}
	dailyCopy.OwnerKind = "report_snapshot"
	dailyCopy.OwnerID = snapshot.ID
	dailyCopy.BlockIndex = len(sourceBlocks)
	if err := tx.Create(&dailyCopy).Error; err != nil {
		return snapshot, err
	}
	manifest = append(manifest, map[string]any{"block_id": dailyCopy.BlockID, "block_kind": dailyCopy.BlockKind, "content_hash": dailyCopy.ContentHash, "point_count": dailyCopy.PointCount})
	manifestRaw, _ := compute.CanonicalJSON(manifest)
	identityRaw, _ := compute.CanonicalJSON(map[string]any{"artifact_set_hash": study.ArtifactSetHash, "prediction_hash": prediction.ContentHash, "policy_hash": policy.ContentHash, "materialization_hash": materialization.ContentHash, "manifest_hash": compute.HashBytes(manifestRaw)})
	snapshot.SnapshotKey = "p09-report:" + compute.HashBytes(identityRaw)
	snapshot.BlockManifest = manifestRaw
	snapshot.BlockManifestHash = compute.HashBytes(manifestRaw)
	snapshot.ContentHash = compute.HashBytes(identityRaw)
	if err := tx.Model(&snapshot).Updates(map[string]any{"snapshot_key": snapshot.SnapshotKey, "block_manifest": snapshot.BlockManifest, "block_manifest_hash": snapshot.BlockManifestHash, "content_hash": snapshot.ContentHash}).Error; err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (s *Service) ReportBlock(ctx context.Context, userID, studyID uint, blockID string) (ReportBlockDescriptor, error) {
	var study saasstore.DynamicModelStudy
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", studyID, userID).First(&study).Error; err != nil {
		return ReportBlockDescriptor{}, ErrNotFound
	}
	var block saasstore.DynamicReportBlock
	query := s.db.WithContext(ctx).Where("study_id = ? AND block_id = ?", study.ID, blockID)
	if study.ReportSnapshotID != nil {
		query = query.Where("(owner_kind = ? AND owner_id = ?) OR owner_kind = ?", "report_snapshot", *study.ReportSnapshotID, "materialization")
	}
	if err := query.Order("id DESC").First(&block).Error; err != nil {
		return ReportBlockDescriptor{}, ErrNotFound
	}
	return ReportBlockDescriptor{BlockID: block.BlockID, BlockKind: block.BlockKind, SchemaVersion: block.SchemaVersion, FormulaVersion: block.FormulaVersion, ContentHash: block.ContentHash, PointCount: block.PointCount, Payload: json.RawMessage(block.Payload)}, nil
}

func (s *Service) describe(ctx context.Context, study saasstore.DynamicModelStudy, details bool) (StudyDescriptor, error) {
	descriptor := StudyDescriptor{ID: study.ID, Name: study.Name, Status: study.Status, Route: study.Route, StudyKey: study.StudyKey, SettingHash: study.SettingHash, DatasetHash: study.DatasetHash, ComputeTaskID: study.ComputeTaskID, MaterializationTaskID: study.MaterializationTaskID, ArtifactSetHash: study.ArtifactSetHash, PredictionSnapshotID: study.PredictionSnapshotID, PolicyArtifactID: study.PolicyArtifactID, MaterializationID: study.MaterializationID, ErrorMessage: study.ErrorMessage, CreatedAt: study.CreatedAt.UTC().Format(time.RFC3339)}
	if study.CompletedAt != nil {
		descriptor.CompletedAt = study.CompletedAt.UTC().Format(time.RFC3339)
	}
	if details && study.ArtifactSetHash != "" {
		var artifacts []saasstore.DynamicModelArtifact
		if err := s.db.WithContext(ctx).Where("study_id = ? AND target_kind <> ?", study.ID, "horizon_bundle").Order("horizon,target_kind").Find(&artifacts).Error; err != nil {
			return descriptor, err
		}
		for _, artifact := range artifacts {
			var target core.TargetModel
			if json.Unmarshal(artifact.Payload, &target) == nil {
				report := target.Report
				report.ArtifactHash = artifact.ContentHash
				report.ContentHash = ""
				raw, _ := compute.CanonicalJSON(report)
				report.ContentHash = compute.HashBytes(raw)
				descriptor.Reports = append(descriptor.Reports, report)
			}
		}
	}
	if study.ReportSnapshotID != nil {
		var snapshot saasstore.DynamicModelReportSnapshot
		if err := s.db.WithContext(ctx).First(&snapshot, *study.ReportSnapshotID).Error; err == nil {
			blocks := []string{"model-validation", "calibration", "space-state", "structure-state"}
			if snapshot.Completeness == "complete" {
				blocks = append(blocks, "daily-diagnostics")
			}
			descriptor.Comparison = &ComparisonDescriptor{SourceKind: "dynamic_model_report", SourceID: study.ID, SourceVersion: snapshot.SchemaVersion, SnapshotID: snapshot.ID, ContentHash: snapshot.ContentHash, DisplayName: study.Name, SourceStatus: study.Status, Archived: study.ArchivedAt != nil, SourceLink: fmt.Sprintf("/research/dynamic-parameters/%d", study.ID), AvailableBlocks: blocks}
		}
	}
	return descriptor, nil
}

func taskStudyStatus(status string, materialization bool) string {
	switch status {
	case compute.TaskStatusPlanned:
		if materialization {
			return StudyStatusAwaitingMaterialization
		}
		return StudyStatusPlanned
	case compute.TaskStatusRunning:
		if materialization {
			return StudyStatusMaterializing
		}
		return StudyStatusTraining
	case compute.TaskStatusCompleted:
		if materialization {
			return StudyStatusCompleted
		}
		return StudyStatusAwaitingMaterialization
	case compute.TaskStatusCancelled:
		return StudyStatusCancelled
	case compute.TaskStatusPartial:
		return StudyStatusPartial
	default:
		return StudyStatusFailed
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
