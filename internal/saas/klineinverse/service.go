package klineinverse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	core "quantsaas/internal/klineinverse"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/computetask"
	parameterresearchsvc "quantsaas/internal/saas/parameterresearch"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db                *gorm.DB
	computeTasks      *computetask.Service
	backtests         *backtest.Service
	results           *backtestresult.Store
	parameterResearch *parameterresearchsvc.Service
}

func NewService(db *gorm.DB, tasks *computetask.Service, backtests *backtest.Service, parameterResearch *parameterresearchsvc.Service) *Service {
	if backtests == nil {
		backtests = backtest.NewService(db)
	}
	if parameterResearch == nil {
		parameterResearch = parameterresearchsvc.NewService(db, tasks, nil)
	}
	return &Service{db: db, computeTasks: tasks, backtests: backtests, results: backtestresult.NewStore(db), parameterResearch: parameterResearch}
}

func (s *Service) CreateDraft(ctx context.Context, userID uint, request CreateDraftRequest) (StudyDescriptor, error) {
	canonical, err := s.resolveDraft(ctx, userID, request)
	if err != nil {
		return StudyDescriptor{}, err
	}
	canonicalRaw, err := compute.CanonicalJSON(canonical)
	if err != nil {
		return StudyDescriptor{}, err
	}
	canonicalHash := compute.HashBytes(canonicalRaw)
	ownerIdentity, _ := compute.CanonicalJSON(map[string]any{"owner_user_id": userID, "canonical_hash": canonicalHash})
	studyHash := "p12-study:" + compute.HashBytes(ownerIdentity)
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = "K 線樣貌反推研究"
	}
	tagsRaw, _ := compute.CanonicalJSON(cleanStrings(request.Tags))
	study := saasstore.KlineInverseStudy{
		OwnerUserID: userID, StudyHash: studyHash, SchemaVersion: StudySchemaVersion, Name: name,
		Notes: strings.TrimSpace(request.Notes), Tags: tagsRaw, Status: "draft", CurrentStage: "draft",
		SourceKind: canonical.SourceKind, SourceVersion: canonical.SourceVersion, SourceContentHash: canonical.SourceContentHash,
		ParameterHash: canonical.ParameterHash, InstrumentID: canonical.InstrumentID, DataSource: canonical.DataSource,
		Symbol: canonical.Symbol, Interval: canonical.Interval, ExecutionMode: canonical.ExecutionMode,
		WarmupLength: canonical.WarmupLength, EvaluationLength: canonical.EvaluationLength,
		EvaluationStartMs: canonical.EvaluationStartMs, InitialCapital: canonical.InitialCapital,
		FeeRate: canonical.FeeRate, SlippageRate: canonical.SlippageRate, InitialBudget: canonical.InitialBudget,
		CellCount: canonical.CellCount, ParentCapacity: canonical.ParentCapacity, RootSeed: canonical.RootSeed,
		BoundsHash: canonical.BoundsHash, CalibrationSourceHash: canonical.CalibrationSourceHash,
		CanonicalHash: canonicalHash, Canonical: canonicalRaw,
	}
	switch canonical.SourceKind {
	case "gene_record":
		study.SourceGenomeID = &canonical.SourceID
	case "m_candidate":
		study.SourceCandidateID = &canonical.SourceID
	case "backtest_result":
		study.SourceBacktestResultID = &canonical.SourceID
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "study_hash"}}, DoNothing: true}).Create(&study)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			return tx.Where("study_hash = ? AND owner_user_id = ?", studyHash, userID).First(&study).Error
		}
		for ordinal, batchType := range []string{"calibration", "search"} {
			manifest := map[string]any{"schema_version": BatchSchemaVersion, "study_hash": studyHash, "batch_type": batchType, "budget": canonical.InitialBudget, "rng_start": 0, "rng_end": canonical.InitialBudget}
			manifestRaw, _ := compute.CanonicalJSON(manifest)
			batch := saasstore.KlineInverseBatch{StudyID: study.ID, Ordinal: ordinal, BatchKey: fmt.Sprintf("p12-batch:%d:%s:0", study.ID, batchType), BatchType: batchType, SchemaVersion: BatchSchemaVersion, ManifestHash: compute.HashBytes(manifestRaw), Manifest: manifestRaw, CompatibilityHash: canonicalHash, Budget: canonical.InitialBudget, RNGStart: 0, RNGEnd: int64(canonical.InitialBudget), Status: "planned"}
			if err := tx.Create(&batch).Error; err != nil {
				return err
			}
		}
		link := saasstore.KlineInverseSourceLink{StudyID: study.ID, SourceKind: canonical.SourceKind, SourceID: strconv.FormatUint(uint64(canonical.SourceID), 10), SourceVersion: canonical.SourceVersion, SourceContentHash: canonical.SourceContentHash, BackLink: fmt.Sprintf("/kline-inverse?study=%d", study.ID)}
		return tx.Create(&link).Error
	})
	if err != nil {
		return StudyDescriptor{}, err
	}
	return s.Get(ctx, userID, study.ID)
}

func (s *Service) Plan(ctx context.Context, userID, studyID uint) (PlanResponse, error) {
	study, canonical, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return PlanResponse{}, err
	}
	composite, err := s.initialComposite(ctx, study, canonical)
	if err != nil {
		return PlanResponse{}, err
	}
	preview, err := s.computeTasks.PreviewComposite(ctx, userID, composite)
	if err != nil {
		return PlanResponse{}, err
	}
	descriptor, err := s.descriptor(ctx, study, canonical)
	if err != nil {
		return PlanResponse{}, err
	}
	return PlanResponse{Study: descriptor, Plan: preview, FeatureCalculations: canonical.InitialBudget, BacktestEvaluations: canonical.InitialBudget, StorageEstimateBytes: int64(canonical.InitialBudget*(canonical.WarmupLength+canonical.EvaluationLength)) * 96}, nil
}

func (s *Service) Start(ctx context.Context, userID, studyID uint, request StartRequest) (StudyDescriptor, error) {
	study, canonical, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return StudyDescriptor{}, err
	}
	if study.Status != "draft" && study.Status != "waiting" {
		return StudyDescriptor{}, ErrInvalidRequest
	}
	composite, err := s.initialComposite(ctx, study, canonical)
	if err != nil {
		return StudyDescriptor{}, err
	}
	preview, err := s.computeTasks.PreviewComposite(ctx, userID, composite)
	if err != nil {
		return StudyDescriptor{}, err
	}
	if strings.TrimSpace(request.PlanKey) == "" || request.PlanKey != preview.PlanKey {
		return StudyDescriptor{}, ErrPlanStale
	}
	root, err := s.computeTasks.CreateComposite(ctx, userID, composite, request.ConfirmSoftLimit)
	if err != nil {
		return StudyDescriptor{}, err
	}
	var stages []saasstore.ComputeTask
	if err := s.db.WithContext(ctx).Where("parent_task_id = ?", root.ID).Order("stage_order ASC").Find(&stages).Error; err != nil {
		return StudyDescriptor{}, err
	}
	if len(stages) != 2 {
		return StudyDescriptor{}, fmt.Errorf("P12 初始任務階段不完整")
	}
	var batches []saasstore.KlineInverseBatch
	if err := s.db.WithContext(ctx).Where("study_id = ? AND ordinal IN ?", study.ID, []int{0, 1}).Order("ordinal ASC").Find(&batches).Error; err != nil {
		return StudyDescriptor{}, err
	}
	if len(batches) != 2 {
		return StudyDescriptor{}, ErrNotFound
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for index := range batches {
			if err := tx.Model(&batches[index]).Updates(map[string]any{"compute_task_id": stages[index].ID, "status": stages[index].Status}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&study).Updates(map[string]any{"status": "waiting", "current_stage": "calibration", "started_at": now}).Error
	}); err != nil {
		return StudyDescriptor{}, err
	}
	if stages[0].Status != compute.TaskStatusCompleted {
		if _, err := s.computeTasks.StartTask(ctx, userID, stages[0].ID); err != nil {
			return StudyDescriptor{}, err
		}
	}
	if canonical.SourceKind == "m_candidate" {
		s.updateCandidateLink(ctx, userID, study, "running", nil)
	}
	return s.Get(ctx, userID, study.ID)
}

func (s *Service) StartNextStage(ctx context.Context, userID, studyID uint) (StudyDescriptor, error) {
	study, _, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return StudyDescriptor{}, err
	}
	var batches []saasstore.KlineInverseBatch
	if err := s.db.WithContext(ctx).Where("study_id = ?", study.ID).Order("ordinal ASC").Find(&batches).Error; err != nil {
		return StudyDescriptor{}, err
	}
	for _, batch := range batches {
		if batch.ComputeTaskID == nil {
			continue
		}
		task, err := s.computeTasks.Get(ctx, userID, *batch.ComputeTaskID)
		if err != nil {
			return StudyDescriptor{}, err
		}
		if task.Status == compute.TaskStatusPlanned || task.Status == compute.TaskStatusPartial {
			if _, err := s.computeTasks.StartTask(ctx, userID, task.ID); err != nil {
				return StudyDescriptor{}, err
			}
			_ = s.db.WithContext(ctx).Model(&study).Updates(map[string]any{"status": "running", "current_stage": batch.BatchType}).Error
			return s.Get(ctx, userID, study.ID)
		}
	}
	return s.Get(ctx, userID, study.ID)
}

func (s *Service) CancelBatch(ctx context.Context, userID, studyID, batchID uint) (StudyDescriptor, error) {
	study, _, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return StudyDescriptor{}, err
	}
	var batch saasstore.KlineInverseBatch
	if err := s.db.WithContext(ctx).Where("id = ? AND study_id = ?", batchID, study.ID).First(&batch).Error; err != nil || batch.ComputeTaskID == nil {
		return StudyDescriptor{}, ErrNotFound
	}
	if _, err := s.computeTasks.Cancel(ctx, userID, *batch.ComputeTaskID); err != nil {
		return StudyDescriptor{}, err
	}
	_ = s.db.WithContext(ctx).Model(&study).Updates(map[string]any{"status": "cancelled", "current_stage": batch.BatchType}).Error
	if study.SourceCandidateID != nil {
		s.updateCandidateLink(ctx, userID, study, "cancelled", nil)
	}
	return s.Get(ctx, userID, study.ID)
}

func (s *Service) ResumeBatch(ctx context.Context, userID, studyID, batchID uint) (StudyDescriptor, error) {
	study, _, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return StudyDescriptor{}, err
	}
	var batch saasstore.KlineInverseBatch
	if err := s.db.WithContext(ctx).Where("id = ? AND study_id = ?", batchID, study.ID).First(&batch).Error; err != nil || batch.ComputeTaskID == nil {
		return StudyDescriptor{}, ErrNotFound
	}
	task, err := s.computeTasks.Retry(ctx, userID, *batch.ComputeTaskID)
	if err != nil {
		return StudyDescriptor{}, err
	}
	_ = s.db.WithContext(ctx).Model(&study).Updates(map[string]any{"status": "running", "current_stage": batch.BatchType}).Error
	_ = s.db.WithContext(ctx).Model(&batch).Updates(map[string]any{"status": task.Status, "error_message": ""}).Error
	return s.Get(ctx, userID, study.ID)
}

func (s *Service) Archive(ctx context.Context, userID, studyID uint) (StudyDescriptor, error) {
	study, _, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return StudyDescriptor{}, err
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Model(&study).Update("archived_at", now).Error; err != nil {
		return StudyDescriptor{}, err
	}
	return s.Get(ctx, userID, study.ID)
}

func (s *Service) List(ctx context.Context, userID uint, includeArchived bool) ([]StudyDescriptor, error) {
	query := s.db.WithContext(ctx).Where("owner_user_id = ?", userID)
	if !includeArchived {
		query = query.Where("archived_at IS NULL")
	}
	var rows []saasstore.KlineInverseStudy
	if err := query.Order("updated_at DESC,id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]StudyDescriptor, 0, len(rows))
	for _, row := range rows {
		var canonical StudyCanonical
		if json.Unmarshal(row.Canonical, &canonical) != nil {
			continue
		}
		descriptor, err := s.descriptor(ctx, row, canonical)
		if err != nil {
			return nil, err
		}
		result = append(result, descriptor)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, userID, studyID uint) (StudyDescriptor, error) {
	study, canonical, err := s.loadStudy(ctx, userID, studyID)
	if err != nil {
		return StudyDescriptor{}, err
	}
	return s.descriptor(ctx, study, canonical)
}

func (s *Service) initialComposite(ctx context.Context, study saasstore.KlineInverseStudy, canonical StudyCanonical) (computetask.CompositeSpec, error) {
	var batches []saasstore.KlineInverseBatch
	if err := s.db.WithContext(ctx).Where("study_id = ? AND ordinal IN ?", study.ID, []int{0, 1}).Order("ordinal ASC").Find(&batches).Error; err != nil {
		return computetask.CompositeSpec{}, err
	}
	if len(batches) != 2 {
		return computetask.CompositeSpec{}, ErrNotFound
	}
	calInput, _ := compute.CanonicalJSON(CalibrationExecutionInput{StudyID: study.ID, BatchID: batches[0].ID})
	searchInput, _ := compute.CanonicalJSON(SearchExecutionInput{StudyID: study.ID, BatchID: batches[1].ID})
	seed := canonical.RootSeed
	return computetask.CompositeSpec{TaskType: "p12-kline-inverse", Title: study.Name, Settings: map[string]any{"study_id": study.ID, "study_hash": study.StudyHash}, ResearchSettingID: strconv.FormatUint(uint64(study.ID), 10), ResearchSettingHash: study.CanonicalHash, Stages: []computetask.StageSpec{
		{Key: "calibration", Type: "calibration", Order: 0, Title: "特徵空間校準", ExecutorType: CalibrationExecutorType, RNG: compute.RNGSpec{Algorithm: "counter", Version: core.RNGVersion, RootSeed: &seed}, Items: []compute.ManifestItemInput{{Key: "calibration", CacheKey: "p12-calibration:" + study.StudyHash, Input: calInput, EstimatedUnits: int64(canonical.InitialBudget)}}},
		{Key: "search", Type: "search", Order: 1, Title: "品質多樣性搜尋", ExecutorType: SearchExecutorType, DependsOnStageKeys: []string{"calibration"}, RNG: compute.RNGSpec{Algorithm: "counter", Version: core.RNGVersion, RootSeed: &seed}, Items: []compute.ManifestItemInput{{Key: "search", CacheKey: "p12-search:" + study.StudyHash + ":0", Input: searchInput, EstimatedUnits: int64(canonical.InitialBudget)}}},
	}}, nil
}

func (s *Service) resolveDraft(ctx context.Context, userID uint, request CreateDraftRequest) (StudyCanonical, error) {
	if userID == 0 || request.EvaluationStartMs <= 0 || request.EvaluationLength < 2 || request.EvaluationLength > 5000 || request.InitialBudget < 5 || request.InitialBudget > 100000 || request.InitialCapital <= 0 || request.FeeRate < 0 || request.SlippageRate < 0 || request.MutationAmplitude <= 0 || request.MutationAmplitude > 1 {
		return StudyCanonical{}, ErrInvalidRequest
	}
	sources := 0
	if request.GenomeID != 0 {
		sources++
	}
	if request.CandidateID != 0 {
		sources++
	}
	if request.BacktestResultID != 0 {
		sources++
	}
	if sources != 1 {
		return StudyCanonical{}, ErrInvalidRequest
	}
	params, kind, sourceID, version, contentHash, defaults, err := s.resolveParameters(ctx, userID, request)
	if err != nil {
		return StudyCanonical{}, err
	}
	request.InstrumentID = firstNonempty(request.InstrumentID, defaults.InstrumentID)
	request.DataSource = firstNonempty(request.DataSource, defaults.DataSource)
	request.Symbol = firstNonempty(request.Symbol, defaults.Symbol)
	request.Interval = firstNonempty(request.Interval, defaults.Interval)
	request.ExecutionMode = firstNonempty(request.ExecutionMode, defaults.ExecutionMode)
	if request.InstrumentID == "" || request.DataSource == "" || request.Symbol == "" || request.Interval == "" || !contains([]string{"close_same_bar", "close_next_open"}, request.ExecutionMode) {
		return StudyCanonical{}, ErrInvalidRequest
	}
	warmup := sigmoiddca.StrategyManifest().RequiredHistoryBars
	if warmup < 1 || sigmoiddca.StrategyManifest().RequiresVolume {
		return StudyCanonical{}, ErrInvalidRequest
	}
	dates, err := s.studyDates(ctx, request.InstrumentID, request.DataSource, request.Symbol, request.Interval, request.EvaluationStartMs, warmup, request.EvaluationLength)
	if err != nil {
		return StudyCanonical{}, err
	}
	if len(request.CalibrationSources) == 0 {
		request.CalibrationSources = []CalibrationSource{{InstrumentID: request.InstrumentID, DataSource: request.DataSource, Symbol: request.Symbol, Interval: request.Interval, StartTimeMs: dates[0], EndTimeMs: dates[len(dates)-1]}}
	}
	observed, sourceHash, err := s.calibrateBounds(ctx, request.CalibrationSources, request.Interval)
	if err != nil {
		return StudyCanonical{}, err
	}
	finalBounds := observed
	if request.FinalBounds != nil {
		finalBounds = *request.FinalBounds
	}
	if err := finalBounds.Validate(); err != nil {
		return StudyCanonical{}, err
	}
	if request.CellCount == 0 || request.ParentCapacity == 0 {
		autoK, autoP, _ := core.AutoKP(request.InitialBudget)
		if request.CellCount == 0 {
			request.CellCount = autoK
		}
		if request.ParentCapacity == 0 {
			request.ParentCapacity = autoP
		}
	}
	if request.CellCount < 1 || request.CellCount > request.InitialBudget || request.ParentCapacity < 1 {
		return StudyCanonical{}, ErrInvalidRequest
	}
	params.Spawn.Policy.InitialUSDT = request.InitialCapital
	params.Spawn.Policy.MonthlyInjectUSDT = 0
	params.Spawn.Policy.ColdSealedBTC = 0
	paramsRaw, _ := compute.CanonicalJSON(params)
	boundsRaw, _ := compute.CanonicalJSON(finalBounds)
	return StudyCanonical{SchemaVersion: StudySchemaVersion, SourceKind: kind, SourceID: sourceID, SourceVersion: version, SourceContentHash: contentHash, Parameters: params, ParameterHash: compute.HashBytes(paramsRaw), InstrumentID: request.InstrumentID, DataSource: request.DataSource, Symbol: request.Symbol, Interval: request.Interval, ExecutionMode: request.ExecutionMode, Dates: dates, WarmupLength: warmup, EvaluationLength: request.EvaluationLength, EvaluationStartMs: request.EvaluationStartMs, CalibrationSources: request.CalibrationSources, CalibrationSourceHash: sourceHash, ObservedBounds: observed, FinalBounds: finalBounds, BoundsHash: compute.HashBytes(boundsRaw), InitialCapital: request.InitialCapital, FeeRate: request.FeeRate, SlippageRate: request.SlippageRate, MonthlyContribution: 0, InitialBudget: request.InitialBudget, CellCount: request.CellCount, ParentCapacity: request.ParentCapacity, RootSeed: request.RootSeed, MutationAmplitude: request.MutationAmplitude, CoordinateVersion: core.CoordinateVersion, FeatureVersion: core.FeatureVersion, DistanceVersion: core.DistanceVersion, CVTVersion: core.CVTVersion, SearchVersion: core.SearchVersion, VariationVersion: core.VariationVersion, StateVersion: core.StateVersion, RNGVersion: core.RNGVersion}, nil
}

type sourceDefaults struct{ InstrumentID, DataSource, Symbol, Interval, ExecutionMode string }

func (s *Service) resolveParameters(ctx context.Context, userID uint, request CreateDraftRequest) (sigmoiddca.Params, string, uint, string, string, sourceDefaults, error) {
	if request.GenomeID != 0 {
		var gene saasstore.GeneRecord
		if err := s.db.WithContext(ctx).Where("id = ? AND strategy_id = ?", request.GenomeID, sigmoiddca.StrategyID).First(&gene).Error; err != nil {
			return sigmoiddca.Params{}, "", 0, "", "", sourceDefaults{}, ErrNotFound
		}
		params := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
		raw, _ := compute.CanonicalJSON(params)
		return params, "gene_record", gene.ID, "gene-record-v1", compute.HashBytes(raw), sourceDefaults{gene.InstrumentID, gene.DataSource, gene.InstrumentID, gene.Interval, gene.ExecutionMode}, nil
	}
	if request.CandidateID != 0 {
		var candidate saasstore.RobustCandidate
		var configuration saasstore.ResearchConfiguration
		var point saasstore.ResearchEvaluationPoint
		if s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", request.CandidateID, userID).First(&candidate).Error != nil {
			return sigmoiddca.Params{}, "", 0, "", "", sourceDefaults{}, ErrNotFound
		}
		if s.db.WithContext(ctx).First(&configuration, candidate.ConfigurationID).Error != nil || s.db.WithContext(ctx).First(&point, candidate.PointID).Error != nil {
			return sigmoiddca.Params{}, "", 0, "", "", sourceDefaults{}, ErrNotFound
		}
		var values map[string]float64
		if json.Unmarshal(point.Parameters, &values) != nil {
			return sigmoiddca.Params{}, "", 0, "", "", sourceDefaults{}, ErrInvalidRequest
		}
		execution, err := s.parameterResearch.BuildPointExecutionInput(ctx, userID, configuration.ID, values)
		if err != nil {
			return sigmoiddca.Params{}, "", 0, "", "", sourceDefaults{}, err
		}
		if execution.Dynamic {
			return sigmoiddca.Params{}, "", 0, "", "", sourceDefaults{}, ErrDynamicSource
		}
		params := sigmoiddca.ParseParamsFromParamPack(execution.Backtest.CustomParams)
		bt := execution.Backtest
		return params, "m_candidate", candidate.ID, candidate.Version, candidate.AdoptionUnitHash, sourceDefaults{bt.InstrumentID, bt.DataSource, bt.Symbol, bt.Interval, bt.ExecutionMode}, nil
	}
	var runCount int64
	if err := s.db.WithContext(ctx).Model(&saasstore.BacktestRun{}).Where("user_id = ? AND backtest_result_id = ?", userID, request.BacktestResultID).Count(&runCount).Error; err != nil || runCount == 0 {
		return sigmoiddca.Params{}, "", 0, "", "", sourceDefaults{}, ErrNotFound
	}
	loaded, err := s.results.Load(ctx, request.BacktestResultID, false)
	if err != nil {
		return sigmoiddca.Params{}, "", 0, "", "", sourceDefaults{}, err
	}
	identity, err := backtestresult.DecodeIdentity(loaded.Spec.Snapshot)
	if err != nil {
		return sigmoiddca.Params{}, "", 0, "", "", sourceDefaults{}, err
	}
	if identity.Snapshot.ModelArtifactHash != "" || identity.Snapshot.DynamicPolicyHash != "" {
		return sigmoiddca.Params{}, "", 0, "", "", sourceDefaults{}, ErrDynamicSource
	}
	var params sigmoiddca.Params
	if json.Unmarshal(identity.Snapshot.Parameters, &params) != nil {
		return sigmoiddca.Params{}, "", 0, "", "", sourceDefaults{}, ErrInvalidRequest
	}
	snapshot := identity.Snapshot
	return params, "backtest_result", request.BacktestResultID, snapshot.SchemaVersion, loaded.Result.ContentHash, sourceDefaults{snapshot.InstrumentID, snapshot.DataSource, snapshot.Symbol, snapshot.Interval, snapshot.ExecutionMode}, nil
}

func (s *Service) studyDates(ctx context.Context, instrument, source, symbol, interval string, start int64, w, h int) ([]int64, error) {
	var before, after []saasstore.KLine
	if err := s.db.WithContext(ctx).Where("instrument_id=? AND source=? AND symbol=? AND interval=? AND open_time < ?", instrument, source, symbol, interval, start).Order("open_time DESC").Limit(w).Find(&before).Error; err != nil {
		return nil, err
	}
	if len(before) != w {
		return nil, fmt.Errorf("H 起點前只有 %d 根，策略需要 %d 根暖身", len(before), w)
	}
	sort.Slice(before, func(i, j int) bool { return before[i].OpenTime < before[j].OpenTime })
	if err := s.db.WithContext(ctx).Where("instrument_id=? AND source=? AND symbol=? AND interval=? AND open_time >= ?", instrument, source, symbol, interval, start).Order("open_time ASC").Limit(h).Find(&after).Error; err != nil {
		return nil, err
	}
	if len(after) != h || after[0].OpenTime != start {
		return nil, fmt.Errorf("H 起點或有效交易日數不足")
	}
	dates := make([]int64, 0, w+h)
	for _, bar := range before {
		dates = append(dates, bar.OpenTime)
	}
	for _, bar := range after {
		dates = append(dates, bar.OpenTime)
	}
	return dates, nil
}

func (s *Service) calibrateBounds(ctx context.Context, sources []CalibrationSource, interval string) (core.Bounds, string, error) {
	var gValues, bValues, uValues, dValues []float64
	identity := []any{}
	for _, source := range sources {
		if source.Interval != interval || source.StartTimeMs <= 0 || source.EndTimeMs < source.StartTimeMs {
			return core.Bounds{}, "", ErrInvalidRequest
		}
		var rows []saasstore.KLine
		if err := s.db.WithContext(ctx).Where("instrument_id=? AND source=? AND symbol=? AND interval=? AND open_time>=? AND open_time<=?", source.InstrumentID, source.DataSource, source.Symbol, source.Interval, source.StartTimeMs, source.EndTimeMs).Order("open_time ASC").Find(&rows).Error; err != nil {
			return core.Bounds{}, "", err
		}
		if len(rows) == 0 {
			return core.Bounds{}, "", fmt.Errorf("校準資料沒有 K 線")
		}
		var previous saasstore.KLine
		hasPrevious := s.db.WithContext(ctx).Where("instrument_id=? AND source=? AND symbol=? AND interval=? AND open_time < ?", source.InstrumentID, source.DataSource, source.Symbol, source.Interval, rows[0].OpenTime).Order("open_time DESC").First(&previous).Error == nil
		for index, row := range rows {
			if row.Open <= 0 || row.Close <= 0 || row.High < math.Max(row.Open, row.Close) || row.Low <= 0 || row.Low > math.Min(row.Open, row.Close) {
				return core.Bounds{}, "", fmt.Errorf("校準資料含非法 OHLC")
			}
			bValues = append(bValues, math.Log(row.Close/row.Open))
			uValues = append(uValues, math.Log(row.High/math.Max(row.Open, row.Close)))
			dValues = append(dValues, math.Log(math.Min(row.Open, row.Close)/row.Low))
			if index > 0 {
				previous = rows[index-1]
				hasPrevious = true
			}
			if hasPrevious && previous.Close > 0 {
				gValues = append(gValues, math.Log(row.Open/previous.Close))
			}
		}
		bars := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			bars = append(bars, map[string]any{
				"open_time": row.OpenTime,
				"open":      row.Open,
				"high":      row.High,
				"low":       row.Low,
				"close":     row.Close,
			})
		}
		identity = append(identity, map[string]any{"source": source, "bars": bars})
	}
	if len(gValues) == 0 {
		return core.Bounds{}, "", fmt.Errorf("校準資料缺少可用前收")
	}
	bounds := core.Bounds{GMin: minFloat(gValues), GMax: maxFloat(gValues), BMin: minFloat(bValues), BMax: maxFloat(bValues), UMin: 0, UMax: maxFloat(uValues), DMin: 0, DMax: maxFloat(dValues)}
	if err := bounds.Validate(); err != nil {
		return core.Bounds{}, "", err
	}
	raw, _ := compute.CanonicalJSON(identity)
	return bounds, compute.HashBytes(raw), nil
}

func (s *Service) loadStudy(ctx context.Context, userID, studyID uint) (saasstore.KlineInverseStudy, StudyCanonical, error) {
	var study saasstore.KlineInverseStudy
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", studyID, userID).First(&study).Error; err != nil {
		return study, StudyCanonical{}, ErrNotFound
	}
	var canonical StudyCanonical
	if json.Unmarshal(study.Canonical, &canonical) != nil || canonical.SchemaVersion != StudySchemaVersion {
		return study, StudyCanonical{}, ErrInvalidRequest
	}
	return study, canonical, nil
}

func (s *Service) descriptor(ctx context.Context, study saasstore.KlineInverseStudy, canonical StudyCanonical) (StudyDescriptor, error) {
	var batches []saasstore.KlineInverseBatch
	if err := s.db.WithContext(ctx).Where("study_id=?", study.ID).Order("ordinal ASC").Find(&batches).Error; err != nil {
		return StudyDescriptor{}, err
	}
	descriptors := make([]BatchDescriptor, 0, len(batches))
	for _, batch := range batches {
		descriptors = append(descriptors, BatchDescriptor{ID: batch.ID, Ordinal: batch.Ordinal, BatchType: batch.BatchType, Budget: batch.Budget, Status: batch.Status, CompletedCount: batch.CompletedCount, CacheHitCount: batch.CacheHitCount, ErrorCount: batch.ErrorCount, RNGStart: batch.RNGStart, RNGEnd: batch.RNGEnd, CheckpointPosition: batch.CheckpointPosition, ComputeTaskID: batch.ComputeTaskID, ManifestHash: batch.ManifestHash, CompatibilityHash: batch.CompatibilityHash, ErrorMessage: batch.ErrorMessage})
	}
	tags := []string{}
	_ = json.Unmarshal(study.Tags, &tags)
	return StudyDescriptor{ID: study.ID, Name: study.Name, Notes: study.Notes, Tags: tags, Status: study.Status, CurrentStage: study.CurrentStage, StudyHash: study.StudyHash, SourceKind: study.SourceKind, SourceID: canonical.SourceID, SourceVersion: study.SourceVersion, SourceContentHash: study.SourceContentHash, InstrumentID: study.InstrumentID, DataSource: study.DataSource, Symbol: study.Symbol, Interval: study.Interval, ExecutionMode: study.ExecutionMode, WarmupLength: study.WarmupLength, EvaluationLength: study.EvaluationLength, EvaluationStartMs: study.EvaluationStartMs, InitialBudget: study.InitialBudget, CellCount: study.CellCount, ParentCapacity: study.ParentCapacity, RootSeed: study.RootSeed, ObservedBounds: canonical.ObservedBounds, FinalBounds: canonical.FinalBounds, Canonical: canonical, CurrentSnapshotID: study.CurrentSnapshotID, Archived: study.ArchivedAt != nil, Batches: descriptors, CreatedAt: study.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: study.UpdatedAt.UTC().Format(time.RFC3339)}, nil
}

func (s *Service) updateCandidateLink(ctx context.Context, userID uint, study saasstore.KlineInverseStudy, status string, snapshot *saasstore.KlineInverseArchiveSnapshot) {
	if study.SourceCandidateID == nil {
		return
	}
	partial := map[string]any{"study_id": study.ID, "status": status}
	sourceHash := study.CanonicalHash
	if snapshot != nil {
		partial["snapshot_id"] = snapshot.ID
		partial["evaluated_count"] = snapshot.EvaluatedCount
		partial["a_coverage"] = ratio(snapshot.ACellCount, study.CellCount)
		partial["b_coverage"] = ratio(snapshot.BCellCount, study.CellCount)
		sourceHash = snapshot.ContentHash
	}
	raw, _ := compute.CanonicalJSON(partial)
	_, _ = s.parameterResearch.UpdateAnalysisLink(ctx, userID, *study.SourceCandidateID, "C", parameterresearchsvc.UpdateAnalysisLinkRequest{Status: status, TaskID: &study.ID, SourceID: strconv.FormatUint(uint64(study.ID), 10), SourceVersion: SnapshotSchemaVersion, SourceContentHash: sourceHash, PartialSnapshot: raw})
}

func cleanStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func minFloat(values []float64) float64 {
	result := values[0]
	for _, value := range values[1:] {
		result = math.Min(result, value)
	}
	return result
}
func maxFloat(values []float64) float64 {
	result := values[0]
	for _, value := range values[1:] {
		result = math.Max(result, value)
	}
	return result
}
func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
