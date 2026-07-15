package robustness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	core "quantsaas/internal/robustness"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db           *gorm.DB
	computeTasks *computetask.Service
}

func NewService(db *gorm.DB, tasks *computetask.Service) *Service {
	return &Service{db: db, computeTasks: tasks}
}

func (s *Service) SetComputeTasks(tasks *computetask.Service) { s.computeTasks = tasks }

func (s *Service) ParameterDefinitions(ctx context.Context, genomeID uint) ([]core.ParameterDefinition, map[string]float64, error) {
	params, _, err := s.loadGenomeParams(ctx, genomeID)
	if err != nil {
		return nil, nil, err
	}
	return core.SigmoidDCAParameterDefinitions(params.PositionStructure), core.ChromosomeValues(params.Chromosome), nil
}

func (s *Service) Preview(ctx context.Context, userID uint, req CreateStudyRequest) (computetask.PlanPreview, error) {
	_, spec, _, _, err := s.prepareStudy(ctx, req)
	if err != nil {
		return computetask.PlanPreview{}, err
	}
	if s.computeTasks == nil {
		return computetask.PlanPreview{}, computetask.ErrServiceUnavailable
	}
	return s.computeTasks.Preview(ctx, userID, spec)
}

func (s *Service) Create(ctx context.Context, userID uint, req CreateStudyRequest) (CreateStudyResponse, error) {
	if userID == 0 || s.computeTasks == nil {
		return CreateStudyResponse{}, computetask.ErrServiceUnavailable
	}
	prepared, spec, settingRaw, spaceRaw, err := s.prepareStudy(ctx, req)
	if err != nil {
		return CreateStudyResponse{}, err
	}
	preview, err := s.computeTasks.Preview(ctx, userID, spec)
	if err != nil {
		return CreateStudyResponse{}, err
	}
	studyKey := "p08-study:" + compute.HashBytes(settingRaw)
	var study saasstore.RobustnessStudy
	createdStudy := false
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
	settingHash := compute.HashBytes(settingRaw)
	spaceHash := compute.HashBytes(spaceRaw)
	genomeID := req.GenomeID
	study = saasstore.RobustnessStudy{
		OwnerUserID: userID, StudyKey: studyKey, Name: prepared.request.Name, Mode: prepared.request.Mode,
		Status: compute.TaskStatusPlanned, SettingVersion: StudySettingVersion, SettingHash: settingHash,
		Settings: saasstore.JSONB(settingRaw), SpaceVersion: core.GridVersion, SpaceHash: spaceHash,
		ParameterSpace: saasstore.JSONB(spaceRaw), CenterPointKey: prepared.centerPointKey,
		SourceGenomeID: &genomeID, ExpectedPointCount: len(prepared.points),
	}
	if err := s.db.WithContext(ctx).Create(&study).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			if err := s.db.WithContext(ctx).Where("owner_user_id = ? AND study_key = ?", userID, studyKey).First(&study).Error; err != nil {
				return CreateStudyResponse{}, err
			}
		} else {
			return CreateStudyResponse{}, err
		}
	} else {
		createdStudy = true
	}
	task, err := s.computeTasks.Create(ctx, userID, spec, req.ConfirmSoftLimit)
	if err != nil {
		if createdStudy {
			_ = s.db.WithContext(ctx).Delete(&study).Error
		}
		return CreateStudyResponse{}, err
	}
	if err := s.db.WithContext(ctx).Model(&study).Where("compute_task_id IS NULL").Update("compute_task_id", task.ID).Error; err != nil {
		return CreateStudyResponse{}, err
	}
	study.ComputeTaskID = &task.ID
	if task.Status == compute.TaskStatusCompleted {
		if err := s.syncStudy(ctx, &study, task); err != nil {
			return CreateStudyResponse{}, err
		}
	}
	descriptor, err := s.describe(ctx, study, true)
	return CreateStudyResponse{Study: descriptor, Preview: preview, Task: task}, err
}

type preparedStudy struct {
	request        CreateStudyRequest
	space          core.ParameterSpace
	points         []core.EvaluationPoint
	centerPointKey string
}

func (s *Service) prepareStudy(ctx context.Context, req CreateStudyRequest) (preparedStudy, computetask.CreateSpec, []byte, []byte, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Mode = strings.TrimSpace(req.Mode)
	if req.Name == "" {
		req.Name = "參數穩健區域分析"
	}
	if req.GenomeID == 0 || req.Radius < 1 || req.Radius > 100 || req.SampleOffset < 0 {
		return preparedStudy{}, computetask.CreateSpec{}, nil, nil, ErrInvalidRequest
	}
	if len(req.Radii) == 0 {
		req.Radii = []int{1, 2, 3, 5, 8, 13}
	}
	req.Radii = normalizeRadii(req.Radii)
	if len(req.Radii) == 0 {
		return preparedStudy{}, computetask.CreateSpec{}, nil, nil, ErrInvalidRequest
	}
	if req.Metric == "" {
		req.Metric = core.MetricLogFinalNAVRatio
	}
	if !core.ValidMetric(req.Metric) {
		return preparedStudy{}, computetask.CreateSpec{}, nil, nil, ErrInvalidRequest
	}
	axisCount := len(req.Axes)
	switch req.Mode {
	case ModeOneDimensional:
		if axisCount != 1 {
			return preparedStudy{}, computetask.CreateSpec{}, nil, nil, ErrInvalidRequest
		}
	case ModeTwoDimensional:
		if axisCount != 2 {
			return preparedStudy{}, computetask.CreateSpec{}, nil, nil, ErrInvalidRequest
		}
	case ModeMultidimensional:
		if axisCount < 2 || req.SampleCount < 1 {
			return preparedStudy{}, computetask.CreateSpec{}, nil, nil, ErrInvalidRequest
		}
	default:
		return preparedStudy{}, computetask.CreateSpec{}, nil, nil, ErrInvalidRequest
	}
	params, gene, err := s.loadGenomeParams(ctx, req.GenomeID)
	if err != nil {
		return preparedStudy{}, computetask.CreateSpec{}, nil, nil, err
	}
	space, err := core.BuildLocalSpace(params, req.Axes, req.Radius, req.CustomSteps)
	if err != nil {
		return preparedStudy{}, computetask.CreateSpec{}, nil, nil, err
	}
	centerCoordinates := make([]int, len(space.Axes))
	baseValues := core.ChromosomeValues(params.Chromosome)
	for dimension, axis := range space.Axes {
		center := baseValues[axis.Name]
		if axis.Type == core.ParameterFloat {
			center = math.Round(center*100) / 100
		}
		found := false
		for index, value := range axis.Values {
			if abs(value-center) < 1e-9 {
				centerCoordinates[dimension], found = index, true
				break
			}
		}
		if !found {
			return preparedStudy{}, computetask.CreateSpec{}, nil, nil, ErrInvalidRequest
		}
	}
	centerKey := core.CoordinateKey(centerCoordinates)
	var points []core.EvaluationPoint
	if req.Mode == ModeMultidimensional {
		centerParams := cloneFloatMap(space.Fixed)
		for dimension, axis := range space.Axes {
			centerParams[axis.Name] = axis.Values[centerCoordinates[dimension]]
		}
		points = []core.EvaluationPoint{{ID: centerKey, Kind: core.PointActual, State: core.PointUnknown, Coordinates: centerCoordinates, Parameters: centerParams}}
		if req.SampleCount > 1 {
			sampled, sampleErr := core.SampleNeighborhood(space, req.SampleCount, req.SampleOffset)
			if sampleErr != nil {
				return preparedStudy{}, computetask.CreateSpec{}, nil, nil, sampleErr
			}
			seen := map[string]bool{centerKey: true}
			for _, point := range sampled {
				if !seen[point.ID] && len(points) < req.SampleCount {
					seen[point.ID] = true
					points = append(points, point)
				}
			}
		}
	} else {
		points, err = core.Enumerate(space)
		if err != nil {
			return preparedStudy{}, computetask.CreateSpec{}, nil, nil, err
		}
	}
	spaceRaw, err := compute.CanonicalJSON(space)
	if err != nil {
		return preparedStudy{}, computetask.CreateSpec{}, nil, nil, err
	}
	paramRaw, _ := compute.CanonicalJSON(params)
	settingRequest := req
	settingRequest.Name = ""
	settingRequest.ConfirmSoftLimit = false
	setting := StudySetting{Version: StudySettingVersion, Request: settingRequest, BaseParameterHash: compute.HashBytes(paramRaw), ParameterSpaceHash: compute.HashBytes(spaceRaw)}
	if req.Mode == ModeMultidimensional {
		setting.SamplingVersion = core.SamplingVersion
	}
	settingRaw, err := compute.CanonicalJSON(setting)
	if err != nil {
		return preparedStudy{}, computetask.CreateSpec{}, nil, nil, err
	}
	items := make([]compute.ManifestItemInput, 0, len(points))
	for _, point := range points {
		chromosome, err := core.ChromosomeWithValues(params.Chromosome, point.Parameters)
		if err != nil {
			return preparedStudy{}, computetask.CreateSpec{}, nil, nil, err
		}
		pointParams := params
		pointParams.Chromosome = chromosome
		customRaw, err := compute.CanonicalJSON(pointParams)
		if err != nil {
			return preparedStudy{}, computetask.CreateSpec{}, nil, nil, err
		}
		bt := backtest.CreateRequest{
			StrategyID: sigmoiddca.StrategyID, InstrumentID: req.Backtest.InstrumentID, DataSource: req.Backtest.DataSource,
			MarketDataVersionID: req.Backtest.MarketDataVersionID, MarketDataContentHash: req.Backtest.MarketDataContentHash,
			ExecutionMode: req.Backtest.ExecutionMode, StartTimeMs: req.Backtest.StartTimeMs, EndTimeMs: req.Backtest.EndTimeMs,
			Symbol: req.Backtest.Symbol, Pair: req.Backtest.Symbol, Interval: req.Backtest.Interval,
			Source: backtest.SourceCustom, CustomParams: json.RawMessage(customRaw), InitialCapital: req.Backtest.InitialCapital,
			MonthlyDCA: req.Backtest.MonthlyDCA, FeeRate: req.Backtest.FeeRate, SpreadRate: req.Backtest.SpreadRate,
			LongTermFilterEnabled: req.Backtest.LongTermFilterEnabled, LongTermFilterMonths: req.Backtest.LongTermFilterMonths,
		}
		input := PointExecutionInput{SchemaVersion: PointSchemaVersion, Backtest: bt}
		inputRaw, err := compute.CanonicalJSON(input)
		if err != nil {
			return preparedStudy{}, computetask.CreateSpec{}, nil, nil, err
		}
		backtestRaw, _ := compute.CanonicalJSON(bt)
		items = append(items, compute.ManifestItemInput{Key: point.ID, CacheKey: "p08-backtest:" + compute.HashBytes(backtestRaw), Input: inputRaw, EstimatedUnits: 1})
	}
	spec := computetask.CreateSpec{
		Kind: compute.TaskKindAtomic, TaskType: "p08.robustness.scan", Title: req.Name,
		ExecutorType: PointExecutorType, Settings: setting,
		ResearchSettingID: fmt.Sprintf("p08-genome:%d", gene.ID), ResearchSettingHash: compute.HashBytes(settingRaw),
		StageKey: "scan", StageType: req.Mode, Items: items,
	}
	return preparedStudy{request: req, space: space, points: points, centerPointKey: centerKey}, spec, settingRaw, spaceRaw, nil
}

func (s *Service) List(ctx context.Context, userID uint, limit int) ([]StudyDescriptor, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []saasstore.RobustnessStudy
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
	var study saasstore.RobustnessStudy
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", studyID, userID).First(&study).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return StudyDescriptor{}, ErrStudyNotFound
		}
		return StudyDescriptor{}, err
	}
	if study.ComputeTaskID != nil && s.computeTasks != nil {
		if task, err := s.computeTasks.Get(ctx, userID, *study.ComputeTaskID); err == nil {
			if err := s.syncStudy(ctx, &study, task); err != nil {
				return StudyDescriptor{}, err
			}
		}
	}
	return s.describe(ctx, study, true)
}

func (s *Service) syncStudy(ctx context.Context, study *saasstore.RobustnessStudy, task *computetask.TaskDescriptor) error {
	if study.ComputeTaskID == nil || *study.ComputeTaskID != task.ID {
		return ErrInvalidRequest
	}
	var items []saasstore.ComputeTaskItem
	if err := s.db.WithContext(ctx).Where("compute_task_id = ? AND status IN ? AND result_hash <> ''", task.ID, []string{compute.ItemStatusCompleted, compute.ItemStatusCached}).Order("item_index ASC").Find(&items).Error; err != nil {
		return err
	}
	var space core.ParameterSpace
	if err := json.Unmarshal(study.ParameterSpace, &space); err != nil {
		return err
	}
	for _, item := range items {
		var result PointExecutionResult
		if err := json.Unmarshal(item.Result, &result); err != nil || result.SchemaVersion != PointResultVersion {
			return fmt.Errorf("P08 點結果格式無效")
		}
		manifestPoint, err := pointFromManifestKey(space, item.ItemKey)
		if err != nil {
			return err
		}
		coordinateRaw, _ := compute.CanonicalJSON(manifestPoint.Coordinates)
		parameterRaw, _ := compute.CanonicalJSON(manifestPoint.Parameters)
		metricsRaw, _ := compute.CanonicalJSON(result.Metrics)
		resultID := result.BacktestResultID
		point := saasstore.RobustnessEvaluationPoint{
			StudyID: study.ID, PointKey: manifestPoint.ID, Kind: string(core.PointActual), State: pointState(result.Metrics),
			CoordinateHash: compute.HashBytes(coordinateRaw), Coordinates: coordinateRaw,
			ParameterHash: compute.HashBytes(parameterRaw), Parameters: parameterRaw,
			BacktestResultID: &resultID, BacktestResultVersion: result.BacktestResultVersion,
			BacktestResultContentHash: result.BacktestResultContentHash,
			MetricsVersion:            core.MetricsVersion, MetricsHash: compute.HashBytes(metricsRaw), Metrics: metricsRaw,
		}
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "study_id"}, {Name: "point_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"kind", "state", "coordinate_hash", "coordinates", "parameter_hash", "parameters", "backtest_result_id", "backtest_result_version", "backtest_result_content_hash", "metrics_version", "metrics_hash", "metrics", "updated_at"}),
		}).Create(&point).Error; err != nil {
			return err
		}
	}
	status := task.Status
	completedAt := study.CompletedAt
	if task.Status == compute.TaskStatusCompleted {
		now := time.Now().UTC()
		completedAt = &now
	}
	var actualCount int64
	if err := s.db.WithContext(ctx).Model(&saasstore.RobustnessEvaluationPoint{}).Where("study_id = ? AND kind = ?", study.ID, core.PointActual).Count(&actualCount).Error; err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Model(study).Updates(map[string]any{"status": status, "actual_point_count": int(actualCount), "completed_at": completedAt}).Error; err != nil {
		return err
	}
	study.Status, study.ActualPointCount, study.CompletedAt = status, int(actualCount), completedAt
	return nil
}

func pointFromManifestKey(space core.ParameterSpace, key string) (core.EvaluationPoint, error) {
	parts := strings.Split(strings.TrimSpace(key), ":")
	if len(parts) != len(space.Axes) {
		return core.EvaluationPoint{}, ErrInvalidRequest
	}
	coordinates := make([]int, len(parts))
	parameters := cloneFloatMap(space.Fixed)
	for dimension, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < space.Axes[dimension].StudyStart || value > space.Axes[dimension].StudyEnd {
			return core.EvaluationPoint{}, ErrInvalidRequest
		}
		coordinates[dimension] = value
		parameters[space.Axes[dimension].Name] = space.Axes[dimension].Values[value]
	}
	if isCoordinateExcluded(space, coordinates) {
		return core.EvaluationPoint{}, ErrInvalidRequest
	}
	return core.EvaluationPoint{ID: core.CoordinateKey(coordinates), Kind: core.PointActual, State: core.PointUnknown, Coordinates: coordinates, Parameters: parameters}, nil
}

func (s *Service) Analyze(ctx context.Context, userID, studyID uint, req AnalyzeRequest) (AnalysisDescriptor, error) {
	study, err := s.Get(ctx, userID, studyID)
	if err != nil {
		return AnalysisDescriptor{}, err
	}
	if req.Metric == "" {
		req.Metric = core.MetricLogFinalNAVRatio
	}
	if !core.ValidMetric(req.Metric) {
		return AnalysisDescriptor{}, ErrInvalidRequest
	}
	req.Radii = normalizeRadii(req.Radii)
	if len(req.Radii) == 0 {
		req.Radii = []int{1, 2, 3, 5, 8, 13}
	}
	if len(study.Points) == 0 {
		return AnalysisDescriptor{}, ErrStudyNotReady
	}
	result, err := core.Analyze(study.ParameterSpace, study.Points, study.CenterPointKey, req.Radii, req.Metric)
	if err != nil {
		return AnalysisDescriptor{}, err
	}
	radiiRaw, _ := compute.CanonicalJSON(req.Radii)
	identityRaw, _ := compute.CanonicalJSON(struct {
		StudyID      uint            `json:"study_id"`
		PointSetHash string          `json:"point_set_hash"`
		Metric       core.MetricName `json:"metric"`
		RadiiHash    string          `json:"radii_hash"`
		Version      string          `json:"version"`
	}{study.ID, result.ObservedPointSetHash, req.Metric, compute.HashBytes(radiiRaw), core.AnalysisVersion})
	analysisKey := "p08-analysis:" + compute.HashBytes(identityRaw)
	var existing saasstore.RobustnessAnalysisSnapshot
	if err := s.db.WithContext(ctx).Where("study_id = ? AND analysis_key = ?", study.ID, analysisKey).First(&existing).Error; err == nil {
		return decodeAnalysis(existing)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return AnalysisDescriptor{}, err
	}
	payloadRaw, _ := compute.CanonicalJSON(result)
	model := saasstore.RobustnessAnalysisSnapshot{
		StudyID: study.ID, AnalysisKey: analysisKey, AnalysisVersion: core.AnalysisVersion,
		ConnectivityVersion: core.ConnectivityVersion, DistanceVersion: core.DistanceVersion,
		FrontierVersion: core.FrontierVersion, CenterVersion: core.CenterVersion,
		PointSetHash: result.ObservedPointSetHash, SettingsHash: study.SettingHash,
		Metric: string(req.Metric), Radii: radiiRaw, Payload: payloadRaw, ContentHash: compute.HashBytes(payloadRaw),
	}
	if err := s.db.WithContext(ctx).Create(&model).Error; err != nil {
		return AnalysisDescriptor{}, err
	}
	return decodeAnalysis(model)
}

func (s *Service) Import(ctx context.Context, userID uint, req ImportStudyRequest) (StudyDescriptor, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.ResearchSettingID = strings.TrimSpace(req.ResearchSettingID)
	req.ResearchSettingHash = strings.TrimSpace(req.ResearchSettingHash)
	if userID == 0 || req.ResearchSettingID == "" || req.ResearchSettingHash == "" || len(req.Points) == 0 || core.ValidateSpace(req.ParameterSpace) != nil {
		return StudyDescriptor{}, ErrInvalidRequest
	}
	if req.Name == "" {
		req.Name = "研究評估點穩健分析"
	}
	spaceRaw, _ := compute.CanonicalJSON(req.ParameterSpace)
	settingRaw, _ := compute.CanonicalJSON(struct {
		Version             string `json:"version"`
		ResearchSettingID   string `json:"research_setting_id"`
		ResearchSettingHash string `json:"research_setting_hash"`
		ParameterSpaceHash  string `json:"parameter_space_hash"`
	}{StudySettingVersion, req.ResearchSettingID, req.ResearchSettingHash, compute.HashBytes(spaceRaw)})
	studyKey := "p08-import:" + compute.HashBytes(settingRaw)
	var study saasstore.RobustnessStudy
	err := s.db.WithContext(ctx).Where("owner_user_id = ? AND study_key = ?", userID, studyKey).First(&study).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		study = saasstore.RobustnessStudy{OwnerUserID: userID, StudyKey: studyKey, Name: req.Name, Mode: ModeImported, Status: compute.TaskStatusCompleted, SettingVersion: StudySettingVersion, SettingHash: compute.HashBytes(settingRaw), Settings: settingRaw, SpaceVersion: core.GridVersion, SpaceHash: compute.HashBytes(spaceRaw), ParameterSpace: spaceRaw, CenterPointKey: req.CenterPointKey, ExpectedPointCount: len(req.Points)}
		if err = s.db.WithContext(ctx).Create(&study).Error; err != nil {
			return StudyDescriptor{}, err
		}
	} else if err != nil {
		return StudyDescriptor{}, err
	}
	for _, input := range req.Points {
		if len(input.Coordinates) != len(req.ParameterSpace.Axes) || isCoordinateExcluded(req.ParameterSpace, input.Coordinates) || !parametersMatchCoordinate(req.ParameterSpace, input.Coordinates, input.Parameters) {
			return StudyDescriptor{}, ErrInvalidRequest
		}
		pointKey := input.ID
		if pointKey == "" {
			pointKey = core.CoordinateKey(input.Coordinates)
		}
		coordinateRaw, _ := compute.CanonicalJSON(input.Coordinates)
		parameterRaw, _ := compute.CanonicalJSON(input.Parameters)
		model := saasstore.RobustnessEvaluationPoint{StudyID: study.ID, PointKey: pointKey, Kind: string(input.Kind), State: string(core.PointUnknown), CoordinateHash: compute.HashBytes(coordinateRaw), Coordinates: coordinateRaw, ParameterHash: compute.HashBytes(parameterRaw), Parameters: parameterRaw, SourceStage: input.SourceStage, SamplingBatch: input.SamplingBatch, PredictionMetadata: saasstore.JSONB(`{}`)}
		if input.Kind == "" {
			model.Kind = string(core.PointActual)
		}
		if core.PointKind(model.Kind) == core.PointActual {
			if input.BacktestResultID == 0 {
				return StudyDescriptor{}, ErrInvalidRequest
			}
			metrics, result, metricErr := s.metricsForStoredResult(ctx, input.BacktestResultID)
			if metricErr != nil {
				return StudyDescriptor{}, metricErr
			}
			metricsRaw, _ := compute.CanonicalJSON(metrics)
			resultID := result.ID
			model.BacktestResultID = &resultID
			model.BacktestResultVersion = result.ResultVersion
			model.BacktestResultContentHash = result.ContentHash
			model.MetricsVersion = core.MetricsVersion
			model.MetricsHash = compute.HashBytes(metricsRaw)
			model.Metrics = metricsRaw
			model.State = pointState(metrics)
		} else {
			if model.Kind != string(core.PointPredicted) && model.Kind != string(core.PointProposed) {
				return StudyDescriptor{}, ErrInvalidRequest
			}
			if len(input.PredictionMetadata) > 0 {
				canonical, canonicalErr := compute.CanonicalRawJSON(input.PredictionMetadata)
				if canonicalErr != nil {
					return StudyDescriptor{}, canonicalErr
				}
				model.PredictionMetadata = canonical
			}
		}
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "study_id"}, {Name: "point_key"}}, DoUpdates: clause.AssignmentColumns([]string{"kind", "state", "coordinate_hash", "coordinates", "parameter_hash", "parameters", "backtest_result_id", "backtest_result_version", "backtest_result_content_hash", "metrics_version", "metrics_hash", "metrics", "source_stage", "sampling_batch", "prediction_metadata", "updated_at"})}).Create(&model).Error; err != nil {
			return StudyDescriptor{}, err
		}
	}
	var actual, predicted, total int64
	s.db.WithContext(ctx).Model(&saasstore.RobustnessEvaluationPoint{}).Where("study_id = ? AND kind = ?", study.ID, core.PointActual).Count(&actual)
	s.db.WithContext(ctx).Model(&saasstore.RobustnessEvaluationPoint{}).Where("study_id = ? AND kind IN ?", study.ID, []core.PointKind{core.PointPredicted, core.PointProposed}).Count(&predicted)
	s.db.WithContext(ctx).Model(&saasstore.RobustnessEvaluationPoint{}).Where("study_id = ?", study.ID).Count(&total)
	if study.CenterPointKey == "" {
		var first saasstore.RobustnessEvaluationPoint
		if err := s.db.WithContext(ctx).Where("study_id = ? AND kind = ?", study.ID, core.PointActual).Order("point_key ASC").First(&first).Error; err == nil {
			study.CenterPointKey = first.PointKey
		}
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Model(&study).Updates(map[string]any{"name": req.Name, "status": compute.TaskStatusCompleted, "expected_point_count": int(total), "actual_point_count": int(actual), "predicted_point_count": int(predicted), "center_point_key": study.CenterPointKey, "completed_at": now}).Error; err != nil {
		return StudyDescriptor{}, err
	}
	study.Name = req.Name
	study.Status = compute.TaskStatusCompleted
	study.ExpectedPointCount = int(total)
	study.ActualPointCount = int(actual)
	study.PredictedPointCount = int(predicted)
	study.CompletedAt = &now
	return s.describe(ctx, study, true)
}

func (s *Service) metricsForStoredResult(ctx context.Context, resultID uint) (core.RelativeMetrics, saasstore.BacktestResult, error) {
	var result saasstore.BacktestResult
	if err := s.db.WithContext(ctx).Where("id = ? AND status = ?", resultID, saasstore.BacktestResultStatusCompleted).First(&result).Error; err != nil {
		return core.RelativeMetrics{}, result, ErrInvalidRequest
	}
	var summary saasstore.BacktestResultSummary
	if err := s.db.WithContext(ctx).Where("backtest_result_id = ?", resultID).First(&summary).Error; err != nil {
		return core.RelativeMetrics{}, result, err
	}
	var payload struct {
		BenchmarkFinalEquity float64 `json:"benchmark_final_equity"`
		BenchmarkMaxDrawdown float64 `json:"benchmark_max_drawdown"`
	}
	var summaryData struct {
		Extra json.RawMessage `json:"extra"`
	}
	if err := json.Unmarshal(summary.Payload, &summaryData); err != nil {
		return core.RelativeMetrics{}, result, err
	}
	if err := json.Unmarshal(summaryData.Extra, &payload); err != nil {
		return core.RelativeMetrics{}, result, err
	}
	metrics, err := core.ComputeRelativeMetrics(core.RelativeMetricInput{StrategyFinalNAV: summary.FinalEquity, BenchmarkFinalNAV: payload.BenchmarkFinalEquity, StrategyMaxDrawdown: summary.MaxDrawdown, BenchmarkMaxDrawdown: payload.BenchmarkMaxDrawdown})
	return metrics, result, err
}

func isCoordinateExcluded(space core.ParameterSpace, coordinate []int) bool {
	for _, excluded := range space.ExcludedCoordinates {
		if len(excluded) != len(coordinate) {
			continue
		}
		same := true
		for i := range coordinate {
			if coordinate[i] != excluded[i] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	for i, value := range coordinate {
		if value < space.Axes[i].StudyStart || value > space.Axes[i].StudyEnd {
			return true
		}
	}
	return false
}
func parametersMatchCoordinate(space core.ParameterSpace, coordinate []int, parameters map[string]float64) bool {
	if len(parameters) == 0 {
		return false
	}
	for name, value := range space.Fixed {
		if abs(parameters[name]-value) > 1e-9 {
			return false
		}
	}
	for i, axis := range space.Axes {
		if abs(parameters[axis.Name]-axis.Values[coordinate[i]]) > 1e-9 {
			return false
		}
	}
	return true
}

func (s *Service) describe(ctx context.Context, study saasstore.RobustnessStudy, includeDetails bool) (StudyDescriptor, error) {
	var space core.ParameterSpace
	if err := json.Unmarshal(study.ParameterSpace, &space); err != nil {
		return StudyDescriptor{}, err
	}
	descriptor := StudyDescriptor{ID: study.ID, Name: study.Name, Mode: study.Mode, Status: study.Status, StudyKey: study.StudyKey, SettingVersion: study.SettingVersion, SettingHash: study.SettingHash, SpaceVersion: study.SpaceVersion, SpaceHash: study.SpaceHash, ParameterSpace: space, CenterPointKey: study.CenterPointKey, SourceGenomeID: study.SourceGenomeID, ComputeTaskID: study.ComputeTaskID, ExpectedPointCount: study.ExpectedPointCount, ActualPointCount: study.ActualPointCount, PredictedPointCount: study.PredictedPointCount, CreatedAt: study.CreatedAt.UTC().Format(time.RFC3339)}
	if includeDetails {
		descriptor.Settings = append(json.RawMessage(nil), study.Settings...)
		var models []saasstore.RobustnessEvaluationPoint
		if err := s.db.WithContext(ctx).Where("study_id = ?", study.ID).Order("point_key ASC").Limit(100000).Find(&models).Error; err != nil {
			return descriptor, err
		}
		descriptor.Points = make([]core.EvaluationPoint, 0, len(models))
		for _, model := range models {
			point, err := decodePoint(model)
			if err != nil {
				return descriptor, err
			}
			descriptor.Points = append(descriptor.Points, point)
		}
		var snapshot saasstore.RobustnessAnalysisSnapshot
		if err := s.db.WithContext(ctx).Where("study_id = ?", study.ID).Order("created_at DESC,id DESC").First(&snapshot).Error; err == nil {
			analysis, err := decodeAnalysis(snapshot)
			if err != nil {
				return descriptor, err
			}
			descriptor.LatestAnalysis = &analysis
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return descriptor, err
		}
	}
	if study.CompletedAt != nil {
		descriptor.CompletedAt = study.CompletedAt.UTC().Format(time.RFC3339)
	}
	return descriptor, nil
}

func decodePoint(model saasstore.RobustnessEvaluationPoint) (core.EvaluationPoint, error) {
	var coordinates []int
	var parameters map[string]float64
	if err := json.Unmarshal(model.Coordinates, &coordinates); err != nil {
		return core.EvaluationPoint{}, err
	}
	if err := json.Unmarshal(model.Parameters, &parameters); err != nil {
		return core.EvaluationPoint{}, err
	}
	point := core.EvaluationPoint{ID: model.PointKey, Kind: core.PointKind(model.Kind), State: core.PointState(model.State), Coordinates: coordinates, Parameters: parameters, SourceStage: model.SourceStage, SamplingBatch: model.SamplingBatch}
	if model.BacktestResultID != nil {
		point.BacktestResultID = *model.BacktestResultID
	}
	if model.MetricsHash != "" {
		var metrics core.RelativeMetrics
		if err := json.Unmarshal(model.Metrics, &metrics); err != nil {
			return point, err
		}
		point.Metrics = &metrics
	}
	return point, nil
}

func decodeAnalysis(model saasstore.RobustnessAnalysisSnapshot) (AnalysisDescriptor, error) {
	var radii []int
	var result core.AnalysisResult
	if err := json.Unmarshal(model.Radii, &radii); err != nil {
		return AnalysisDescriptor{}, err
	}
	if err := json.Unmarshal(model.Payload, &result); err != nil {
		return AnalysisDescriptor{}, err
	}
	return AnalysisDescriptor{ID: model.ID, AnalysisKey: model.AnalysisKey, PointSetHash: model.PointSetHash, SettingsHash: model.SettingsHash, Metric: core.MetricName(model.Metric), Radii: radii, ContentHash: model.ContentHash, Result: result, CreatedAt: model.CreatedAt.UTC().Format(time.RFC3339)}, nil
}

func (s *Service) loadGenomeParams(ctx context.Context, id uint) (sigmoiddca.Params, saasstore.GeneRecord, error) {
	var gene saasstore.GeneRecord
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&gene).Error; err != nil {
		return sigmoiddca.Params{}, gene, ErrInvalidRequest
	}
	params := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
	return params, gene, nil
}

func normalizeRadii(values []int) []int {
	seen := map[int]bool{}
	result := []int{}
	for _, v := range values {
		if v > 0 && v <= 100 && !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	sort.Ints(result)
	return result
}
func pointState(metrics core.RelativeMetrics) string {
	if metrics.Qualified {
		return string(core.PointQualified)
	}
	return string(core.PointUnqualified)
}
func cloneFloatMap(values map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(values))
	for k, v := range values {
		result[k] = v
	}
	return result
}
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
