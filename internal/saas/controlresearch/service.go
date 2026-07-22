package controlresearch

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"quantsaas/internal/backtestcore"
	compute "quantsaas/internal/compute"
	core "quantsaas/internal/controlresearch"
	robust "quantsaas/internal/robustness"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/computetask"
	parameterresearchsvc "quantsaas/internal/saas/parameterresearch"
	robustnesssvc "quantsaas/internal/saas/robustness"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db                *gorm.DB
	computeTasks      *computetask.Service
	parameterResearch *parameterresearchsvc.Service
	results           *backtestresult.Store
	taskSyncs         sync.Map
}

type preparedPlan struct {
	canonical     TaskCanonical
	canonicalRaw  []byte
	canonicalHash string
	taskKey       string
	batchKey      string
	batch         core.Batch
	composite     computetask.CompositeSpec
	preview       computetask.CompositePlanPreview
}

func NewService(db *gorm.DB, tasks *computetask.Service, parameterResearch *parameterresearchsvc.Service) *Service {
	if parameterResearch == nil {
		parameterResearch = parameterresearchsvc.NewService(db, tasks, nil)
	}
	return &Service{db: db, computeTasks: tasks, parameterResearch: parameterResearch, results: backtestresult.NewStore(db)}
}

func (s *Service) lockTaskSync(taskID uint) func() {
	value, _ := s.taskSyncs.LoadOrStore(taskID, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

func (s *Service) Preview(ctx context.Context, userID uint, req CreateRequest) (PlanResponse, error) {
	prepared, err := s.prepare(ctx, userID, req)
	if err != nil {
		return PlanResponse{}, err
	}
	return planResponse(prepared), nil
}

func (s *Service) Create(ctx context.Context, userID uint, req CreateRequest) (TaskDescriptor, error) {
	prepared, err := s.prepare(ctx, userID, req)
	if err != nil {
		return TaskDescriptor{}, err
	}
	if req.ExpectedPlanKey != "" && req.ExpectedPlanKey != prepared.preview.PlanKey {
		return TaskDescriptor{}, ErrPlanStale
	}
	batch, err := s.persistBatch(ctx, userID, prepared)
	if err != nil {
		return TaskDescriptor{}, err
	}
	tagsRaw, _ := compute.CanonicalJSON(cleanStrings(req.Tags))
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "隨機參數與無意義規則對照"
	}
	model := saasstore.ControlAnalysisTask{OwnerUserID: userID, TaskKey: prepared.taskKey, Name: name, Notes: strings.TrimSpace(req.Notes), Tags: tagsRaw, Status: "draft", SourceKind: prepared.canonical.SourceKind, SourceVersion: prepared.canonical.SourceVersion, SourceContentHash: prepared.canonical.SourceContentHash, RandomBatchID: batch.ID, RandomTargetCount: req.RandomCount, ShuffleSeed: req.ShuffleSeed, ShuffleTargetCount: req.ShuffleCount, ToggleEveryNBars: req.ToggleEveryNBars, RuleVersion: RuleVersion, StatisticsVersion: core.StatisticsVersion, ParameterSpaceHash: prepared.canonical.ParameterSpaceHash, ModelArtifactHash: prepared.canonical.ModelArtifactHash, PredictionSchemaHash: prepared.canonical.PredictionSchemaHash, DynamicPolicyHash: prepared.canonical.DynamicPolicyHash, CanonicalHash: prepared.canonicalHash, Canonical: prepared.canonicalRaw}
	if prepared.canonical.SourceGenomeID != 0 {
		model.SourceGenomeID = &prepared.canonical.SourceGenomeID
	}
	if prepared.canonical.CandidateID != 0 {
		model.CandidateID = &prepared.canonical.CandidateID
	}
	if prepared.canonical.ResearchConfigurationID != 0 {
		model.ResearchConfigurationID = &prepared.canonical.ResearchConfigurationID
	}
	created := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "task_key"}}, DoNothing: true}).Create(&model)
	if created.Error != nil {
		return TaskDescriptor{}, created.Error
	}
	if created.RowsAffected == 0 {
		if err := s.db.WithContext(ctx).Where("owner_user_id = ? AND task_key = ?", userID, prepared.taskKey).First(&model).Error; err != nil {
			return TaskDescriptor{}, err
		}
		return s.Get(ctx, userID, model.ID)
	}
	root, err := s.computeTasks.CreateComposite(ctx, userID, prepared.composite, req.ConfirmSoftLimit)
	if err != nil {
		_ = s.db.WithContext(context.Background()).Model(&model).Updates(map[string]any{"status": "failed"}).Error
		return TaskDescriptor{}, err
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Model(&model).Updates(map[string]any{"compute_task_id": root.ID, "status": "planned", "started_at": now}).Error; err != nil {
		return TaskDescriptor{}, err
	}
	model.ComputeTaskID = &root.ID
	if _, err := s.startNextStage(ctx, userID, model); err != nil {
		return TaskDescriptor{}, err
	}
	return s.Get(ctx, userID, model.ID)
}

func (s *Service) prepare(ctx context.Context, userID uint, req CreateRequest) (preparedPlan, error) {
	if userID == 0 || (req.GenomeID == 0) == (req.CandidateID == 0) || req.RandomCount < 1 || req.RandomCount > 10000 || req.ShuffleCount < 1 || req.ShuffleCount > 10000 || req.ToggleEveryNBars < 1 {
		return preparedPlan{}, ErrInvalidRequest
	}
	canonical, validator, err := s.resolveSource(ctx, userID, req)
	if err != nil {
		return preparedPlan{}, err
	}
	canonical.RandomSeed, canonical.ShuffleSeed, canonical.ToggleEveryNBars = req.RandomSeed, req.ShuffleSeed, req.ToggleEveryNBars
	canonical.RuleVersion, canonical.StatisticsVersion = RuleVersion, core.StatisticsVersion
	canonicalRaw, err := compute.CanonicalJSON(canonical)
	if err != nil {
		return preparedPlan{}, err
	}
	canonicalHash := compute.HashBytes(canonicalRaw)
	ownerRaw, _ := compute.CanonicalJSON(map[string]any{"owner_user_id": userID, "canonical_hash": canonicalHash})
	taskKey := "p11-task:" + compute.HashBytes(ownerRaw)
	batchIdentity, _ := compute.CanonicalJSON(map[string]any{"seed": req.RandomSeed, "generator_version": core.RandomGeneratorVersion, "range_version": RangeVersion, "parameter_space_hash": canonical.ParameterSpaceHash, "fixed_structure_hash": canonical.FixedStructureHash, "model_artifact_hash": canonical.ModelArtifactHash, "prediction_schema_hash": canonical.PredictionSchemaHash, "dynamic_policy_hash": canonical.DynamicPolicyHash})
	batchOwnerRaw, _ := compute.CanonicalJSON(map[string]any{"owner_user_id": userID, "batch_identity": json.RawMessage(batchIdentity)})
	batchKey := "p11-batch:" + compute.HashBytes(batchOwnerRaw)
	batch, err := core.GenerateDiscrete(canonical.ParameterSpace, req.RandomCount, req.RandomSeed, validator)
	if err != nil {
		return preparedPlan{}, err
	}
	composite, err := s.buildComposite(ctx, userID, taskKey, canonicalHash, canonical, batch, req.RandomCount, req.ShuffleCount)
	if err != nil {
		return preparedPlan{}, err
	}
	preview, err := s.computeTasks.PreviewComposite(ctx, userID, composite)
	if err != nil {
		return preparedPlan{}, err
	}
	return preparedPlan{canonical: canonical, canonicalRaw: canonicalRaw, canonicalHash: canonicalHash, taskKey: taskKey, batchKey: batchKey, batch: batch, composite: composite, preview: preview}, nil
}

func (s *Service) resolveSource(ctx context.Context, userID uint, req CreateRequest) (TaskCanonical, func(map[string]float64) error, error) {
	if req.CandidateID != 0 {
		var candidate saasstore.RobustCandidate
		if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND archived_at IS NULL", req.CandidateID, userID).First(&candidate).Error; err != nil {
			return TaskCanonical{}, nil, ErrNotFound
		}
		var configuration saasstore.ResearchConfiguration
		var point saasstore.ResearchEvaluationPoint
		if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", candidate.ConfigurationID, userID).First(&configuration).Error; err != nil {
			return TaskCanonical{}, nil, ErrNotFound
		}
		if err := s.db.WithContext(ctx).Where("id = ? AND configuration_id = ?", candidate.PointID, configuration.ID).First(&point).Error; err != nil {
			return TaskCanonical{}, nil, ErrNotFound
		}
		var space robust.ParameterSpace
		var parameters map[string]float64
		if json.Unmarshal(configuration.ParameterSpace, &space) != nil || json.Unmarshal(point.Parameters, &parameters) != nil {
			return TaskCanonical{}, nil, ErrInvalidRequest
		}
		execution, err := s.parameterResearch.BuildPointExecutionInput(ctx, userID, configuration.ID, parameters)
		if err != nil {
			return TaskCanonical{}, nil, err
		}
		validator, err := s.parameterResearch.BuildPointValidator(ctx, userID, configuration.ID)
		if err != nil {
			return TaskCanonical{}, nil, err
		}
		canonical := TaskCanonical{SchemaVersion: TaskSchemaVersion, SourceKind: "m_candidate", CandidateID: candidate.ID, ResearchConfigurationID: configuration.ID, SourceVersion: candidate.Version, SourceContentHash: candidate.AdoptionUnitHash, ParameterSpace: space, ParameterSpaceHash: configuration.ParameterSpaceHash, FixedStructureHash: candidate.AdoptionUnitHash, BaselineParameters: parameters, BaselineExecutorType: execution.ExecutorType, BaselineInput: execution.Input, Backtest: execution.Backtest}
		var config parameterresearchsvc.ConfigurationCanonical
		if json.Unmarshal(configuration.Canonical, &config) == nil && config.DynamicPackage != nil {
			canonical.ModelArtifactHash = config.DynamicPackage.ArtifactSetHash
			canonical.PredictionSchemaHash = config.DynamicPackage.PredictionSnapshotHash
			canonical.DynamicPolicyHash = config.DynamicPackage.BasePolicyHash
		}
		return canonical, validator, nil
	}
	var gene saasstore.GeneRecord
	if err := s.db.WithContext(ctx).Where("id = ? AND strategy_id = ?", req.GenomeID, sigmoiddca.StrategyID).First(&gene).Error; err != nil {
		return TaskCanonical{}, nil, ErrNotFound
	}
	params := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
	settings, err := normalizeBacktestSettings(req.Backtest, gene, params)
	if err != nil {
		return TaskCanonical{}, nil, err
	}
	space, err := fullParameterSpace(params, json.RawMessage(gene.SearchConfig))
	if err != nil {
		return TaskCanonical{}, nil, err
	}
	spaceRaw, _ := compute.CanonicalJSON(space)
	paramsRaw, _ := compute.CanonicalJSON(params)
	bt := backtestRequest(settings, paramsRaw)
	baselineInput, _ := compute.CanonicalJSON(robustnesssvc.PointExecutionInput{SchemaVersion: robustnesssvc.PointSchemaVersion, Backtest: bt})
	validator := func(values map[string]float64) error {
		_, err := robust.ChromosomeWithValues(params.Chromosome, values)
		return err
	}
	return TaskCanonical{SchemaVersion: TaskSchemaVersion, SourceKind: "gene_record", SourceGenomeID: gene.ID, SourceVersion: RandomRecordVersion, SourceContentHash: compute.HashBytes(paramsRaw), ParameterSpace: space, ParameterSpaceHash: compute.HashBytes(spaceRaw), FixedStructureHash: compute.HashBytes(gene.SearchConfig), BaselineParameters: robust.ChromosomeValues(params.Chromosome), BaselineExecutorType: robustnesssvc.PointExecutorType, BaselineInput: baselineInput, Backtest: bt}, validator, nil
}

func normalizeBacktestSettings(settings robustnesssvc.BacktestSettings, gene saasstore.GeneRecord, params sigmoiddca.Params) (robustnesssvc.BacktestSettings, error) {
	var searchConfig map[string]any
	_ = json.Unmarshal(gene.SearchConfig, &searchConfig)
	if settings.InstrumentID == "" {
		settings.InstrumentID = gene.InstrumentID
	}
	if settings.DataSource == "" {
		settings.DataSource = gene.DataSource
	}
	if settings.Symbol == "" {
		settings.Symbol = gene.InstrumentID
	}
	if settings.Interval == "" {
		settings.Interval = gene.Interval
	}
	if settings.ExecutionMode == "" {
		settings.ExecutionMode = gene.ExecutionMode
	}
	if settings.InitialCapital == nil {
		value := params.Spawn.Policy.InitialUSDT
		settings.InitialCapital = &value
	}
	if settings.MonthlyDCA == nil {
		value := params.Spawn.Policy.MonthlyInjectUSDT
		settings.MonthlyDCA = &value
	}
	if settings.FeeRate == nil {
		value := params.Spawn.Risk.FeeRate
		if configured, ok := searchConfig["fee_rate"].(float64); ok {
			value = configured
		}
		settings.FeeRate = &value
	}
	if settings.SpreadRate == nil {
		value := 0.0
		if configured, ok := searchConfig["spread_rate"].(float64); ok {
			value = configured
		}
		settings.SpreadRate = &value
	}
	if settings.LongTermFilterEnabled == nil {
		value := true
		if configured, ok := searchConfig["long_term_filter_enabled"].(bool); ok {
			value = configured
		}
		settings.LongTermFilterEnabled = &value
	}
	if settings.LongTermFilterMonths == 0 {
		if configured, ok := searchConfig["long_term_filter_months"].(float64); ok {
			settings.LongTermFilterMonths = int(configured)
		}
	}
	if settings.LongTermFilterMonths == 0 {
		settings.LongTermFilterMonths = backtestcore.DefaultLongTermFilterMonths
	}
	if settings.StartTimeMs <= 0 || settings.EndTimeMs <= settings.StartTimeMs || settings.InstrumentID == "" || settings.DataSource == "" || settings.Symbol == "" || settings.Interval == "" {
		return settings, ErrInvalidRequest
	}
	return settings, nil
}

func fullParameterSpace(params sigmoiddca.Params, searchRaw json.RawMessage) (robust.ParameterSpace, error) {
	values := robust.ChromosomeValues(params.Chromosome)
	space := robust.ParameterSpace{SchemaVersion: robust.GridVersion, Fixed: map[string]float64{}}
	var search map[string]any
	_ = json.Unmarshal(searchRaw, &search)
	disabled := map[string]bool{}
	for field, name := range map[string]string{"enable_w_mean": "w_mean", "enable_w_momentum": "w_momentum", "enable_w_breakout": "w_breakout"} {
		if value, ok := search[field].(bool); ok && !value {
			disabled[name] = true
		}
	}
	for field, name := range map[string]string{"evolve_gamma": "gamma", "evolve_rebalance_threshold": "rebalance_threshold", "evolve_force_full_threshold": "force_full_threshold", "evolve_force_empty_threshold": "force_empty_threshold"} {
		if value, ok := search[field].(bool); !ok || !value {
			disabled[name] = true
		}
	}
	for _, definition := range robust.SigmoidDCAParameterDefinitions(params.PositionStructure) {
		if !definition.Active || disabled[definition.Name] {
			space.Fixed[definition.Name] = values[definition.Name]
			continue
		}
		axisValues := completeAxis(definition.LegalMin, definition.LegalMax, definition.DefaultStep, definition.Type)
		space.Axes = append(space.Axes, robust.ParameterAxis{Name: definition.Name, Label: definition.Label, Type: definition.Type, Values: axisValues, LegalMin: definition.LegalMin, LegalMax: definition.LegalMax, Step: definition.DefaultStep, StudyStart: 0, StudyEnd: len(axisValues) - 1})
	}
	if len(space.Axes) == 0 || robust.ValidateSpace(space) != nil {
		return space, ErrInvalidRequest
	}
	return space, nil
}

func completeAxis(minimum, maximum, step float64, kind robust.ParameterType) []float64 {
	count := int(math.Floor((maximum-minimum)/step+1e-9)) + 1
	values := make([]float64, 0, count+1)
	for i := 0; i < count; i++ {
		value := minimum + float64(i)*step
		if kind == robust.ParameterInt {
			value = math.Round(value)
		} else {
			value = math.Round(value*1e8) / 1e8
		}
		values = append(values, value)
	}
	if values[len(values)-1] < maximum-1e-9 {
		values = append(values, maximum)
	}
	return values
}

func backtestRequest(settings robustnesssvc.BacktestSettings, custom json.RawMessage) backtest.CreateRequest {
	return backtest.CreateRequest{StrategyID: sigmoiddca.StrategyID, InstrumentID: settings.InstrumentID, DataSource: settings.DataSource, MarketDataVersionID: settings.MarketDataVersionID, MarketDataContentHash: settings.MarketDataContentHash, ExecutionMode: settings.ExecutionMode, StartTimeMs: settings.StartTimeMs, EndTimeMs: settings.EndTimeMs, Symbol: settings.Symbol, Pair: settings.Symbol, Interval: settings.Interval, Source: backtest.SourceCustom, CustomParams: custom, InitialCapital: settings.InitialCapital, MonthlyDCA: settings.MonthlyDCA, FeeRate: settings.FeeRate, SpreadRate: settings.SpreadRate, LongTermFilterEnabled: settings.LongTermFilterEnabled, LongTermFilterMonths: settings.LongTermFilterMonths}
}

func (s *Service) buildComposite(ctx context.Context, userID uint, taskKey, canonicalHash string, canonical TaskCanonical, batch core.Batch, randomCount, shuffleCount int) (computetask.CompositeSpec, error) {
	baselineBacktestRaw, _ := compute.CanonicalJSON(canonical.Backtest)
	baselineCacheKey := "p08-backtest:" + compute.HashBytes(baselineBacktestRaw)
	if canonical.BaselineExecutorType != robustnesssvc.PointExecutorType {
		baselineCacheKey = "p10-dynamic-backtest:" + compute.HashBytes(canonical.BaselineInput)
	}
	baseline := compute.ManifestItemInput{Key: "baseline", CacheKey: baselineCacheKey, Input: canonical.BaselineInput, EstimatedUnits: 1}
	randomItems := make([]compute.ManifestItemInput, 0, randomCount)
	for _, sample := range batch.Samples[:randomCount] {
		var execution parameterresearchsvc.PointExecutionInput
		var err error
		if canonical.ResearchConfigurationID != 0 {
			execution, err = s.parameterResearch.BuildPointExecutionInput(ctx, userID, canonical.ResearchConfigurationID, sample.Parameters)
		} else {
			var gene saasstore.GeneRecord
			if err = s.db.WithContext(ctx).First(&gene, canonical.SourceGenomeID).Error; err == nil {
				params := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
				params.Chromosome, err = robust.ChromosomeWithValues(params.Chromosome, sample.Parameters)
				if err == nil {
					raw, _ := compute.CanonicalJSON(params)
					bt := canonical.Backtest
					bt.CustomParams = raw
					executionInput, _ := compute.CanonicalJSON(robustnesssvc.PointExecutionInput{SchemaVersion: robustnesssvc.PointSchemaVersion, Backtest: bt})
					execution = parameterresearchsvc.PointExecutionInput{ExecutorType: robustnesssvc.PointExecutorType, Input: executionInput, Backtest: bt}
				}
			}
		}
		if err != nil {
			return computetask.CompositeSpec{}, err
		}
		randomItems = append(randomItems, compute.ManifestItemInput{Key: fmt.Sprintf("random:%06d", sample.Index), CacheKey: "p11-random:" + compute.HashBytes(execution.Input), Input: execution.Input, EstimatedUnits: 1})
	}
	rules := []backtestcore.RuleConfig{{Type: backtestcore.RuleOddBuyEvenSell}, {Type: backtestcore.RuleEvenBuyOddSell}, {Type: backtestcore.RuleFixedDayToggle, ToggleEveryNBars: canonical.ToggleEveryNBars, StartWithExposure: true}, {Type: backtestcore.RuleOpenBuyCloseSell}}
	ruleItems := make([]compute.ManifestItemInput, 0, len(rules))
	for index, rule := range rules {
		input := ExecutionInput{SchemaVersion: ExecutionInputVersion, Kind: "rule", SequenceIndex: index, Backtest: canonical.Backtest, Rule: &rule}
		raw, _ := compute.CanonicalJSON(input)
		ruleItems = append(ruleItems, compute.ManifestItemInput{Key: fmt.Sprintf("rule:%s", rule.Type), CacheKey: "p11-rule:" + compute.HashBytes(raw), Input: raw, EstimatedUnits: 1})
	}
	shuffleItems := make([]compute.ManifestItemInput, 0, shuffleCount)
	for index := 0; index < shuffleCount; index++ {
		input := ExecutionInput{SchemaVersion: ExecutionInputVersion, Kind: "shuffle", SequenceIndex: index, TaskKey: taskKey, Backtest: canonical.Backtest, ShuffleSeed: canonical.ShuffleSeed}
		raw, _ := compute.CanonicalJSON(input)
		shuffleItems = append(shuffleItems, compute.ManifestItemInput{Key: fmt.Sprintf("shuffle:%06d", index), CacheKey: "p11-shuffle:" + compute.HashBytes(raw), Input: raw, EstimatedUnits: 1})
	}
	settings := map[string]any{"schema_version": TaskSchemaVersion, "task_key": taskKey, "canonical_hash": canonicalHash, "random_count": randomCount, "shuffle_count": shuffleCount}
	return computetask.CompositeSpec{TaskType: "p11.control-analysis", Title: "隨機參數與無意義規則對照", Settings: settings, ResearchSettingID: taskKey, ResearchSettingHash: canonicalHash, Stages: []computetask.StageSpec{
		{Key: "baseline", Type: "baseline", Order: 0, Title: "正式參數回測", ExecutorType: canonical.BaselineExecutorType, Settings: settings, Items: []compute.ManifestItemInput{baseline}},
		{Key: "random", Type: "random_parameters", Order: 1, Title: "隨機參數對照", ExecutorType: canonical.BaselineExecutorType, Settings: settings, DependsOnStageKeys: []string{"baseline"}, RNG: computeRNG(canonical.RandomSeed), Items: randomItems},
		{Key: "rules", Type: "meaningless_rules", Order: 2, Title: "固定無意義規則", ExecutorType: ExecutorType, Settings: settings, DependsOnStageKeys: []string{"baseline"}, Items: ruleItems},
		{Key: "shuffle", Type: "exposure_shuffle", Order: 3, Title: "曝險序列打散", ExecutorType: ExecutorType, Settings: settings, DependsOnStageKeys: []string{"baseline"}, RNG: computeRNG(canonical.ShuffleSeed), Items: shuffleItems},
	}}, nil
}

func computeRNG(seed int64) compute.RNGSpec {
	return compute.RNGSpec{Algorithm: "splitmix64", Version: "p11-v1", RootSeed: &seed}
}

func (s *Service) persistBatch(ctx context.Context, userID uint, prepared preparedPlan) (saasstore.RandomParameterBatch, error) {
	spaceRaw, _ := compute.CanonicalJSON(prepared.canonical.ParameterSpace)
	reasonsRaw, _ := compute.CanonicalJSON(prepared.batch.RejectReasons)
	contentRaw, _ := compute.CanonicalJSON(prepared.batch)
	batch := saasstore.RandomParameterBatch{OwnerUserID: userID, BatchKey: prepared.batchKey, Seed: prepared.batch.Seed, TargetCount: len(prepared.batch.Samples), GeneratorVersion: core.RandomGeneratorVersion, RangeVersion: RangeVersion, ParameterSpaceVersion: prepared.canonical.ParameterSpace.SchemaVersion, ParameterSpaceHash: prepared.canonical.ParameterSpaceHash, ParameterSpace: spaceRaw, FixedStructureHash: prepared.canonical.FixedStructureHash, ModelArtifactHash: prepared.canonical.ModelArtifactHash, PredictionSchemaHash: prepared.canonical.PredictionSchemaHash, DynamicPolicyHash: prepared.canonical.DynamicPolicyHash, AttemptCount: prepared.batch.AttemptCount, RejectionCount: prepared.batch.RejectionCount, RejectReasons: reasonsRaw, ContentHash: compute.HashBytes(contentRaw)}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "batch_key"}}, DoNothing: true}).Create(&batch)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Where("batch_key = ? AND owner_user_id = ?", prepared.batchKey, userID).First(&batch).Error; err != nil {
				return err
			}
			if batch.ParameterSpaceHash != prepared.canonical.ParameterSpaceHash || batch.FixedStructureHash != prepared.canonical.FixedStructureHash {
				return ErrInvalidRequest
			}
			if err := tx.Model(&batch).Updates(map[string]any{"target_count": len(prepared.batch.Samples), "attempt_count": prepared.batch.AttemptCount, "rejection_count": prepared.batch.RejectionCount, "reject_reasons": reasonsRaw, "content_hash": compute.HashBytes(contentRaw)}).Error; err != nil {
				return err
			}
		}
		for _, sample := range prepared.batch.Samples {
			coordinatesRaw, _ := compute.CanonicalJSON(sample.Coordinates)
			parametersRaw, _ := compute.CanonicalJSON(sample.Parameters)
			record := saasstore.RandomParameterRecord{BatchID: batch.ID, SequenceIndex: sample.Index, Coordinates: coordinatesRaw, Parameters: parametersRaw, ContentHash: compute.HashBytes(parametersRaw)}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return batch, err
}

func planResponse(prepared preparedPlan) PlanResponse {
	fixed := make([]string, 0, len(prepared.canonical.ParameterSpace.Fixed))
	for name := range prepared.canonical.ParameterSpace.Fixed {
		fixed = append(fixed, name)
	}
	sort.Strings(fixed)
	random := make([]string, len(prepared.canonical.ParameterSpace.Axes))
	for i, axis := range prepared.canonical.ParameterSpace.Axes {
		random[i] = axis.Name
	}
	return PlanResponse{PlanKey: prepared.preview.PlanKey, TaskKey: prepared.taskKey, BatchKey: prepared.batchKey, RandomCount: len(prepared.batch.Samples), ShuffleCount: len(prepared.composite.Stages[3].Items), AttemptCount: prepared.batch.AttemptCount, RejectionCount: prepared.batch.RejectionCount, RejectReasons: prepared.batch.RejectReasons, FixedDimensions: fixed, RandomDimensions: random, SameStructure: prepared.canonical.ResearchConfigurationID != 0, Compute: prepared.preview}
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
