package parameterresearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	dynamiccore "quantsaas/internal/dynamicparam"
	core "quantsaas/internal/parameterresearch"
	robust "quantsaas/internal/robustness"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/computetask"
	dynamicparamsvc "quantsaas/internal/saas/dynamicparam"
	robustnesssvc "quantsaas/internal/saas/robustness"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type stageManifest struct {
	SchemaVersion string              `json:"schema_version"`
	StageType     string              `json:"stage_type"`
	Global        *core.GlobalPlan    `json:"global,omitempty"`
	Points        []core.PlannedPoint `json:"points"`
}

func (s *Service) PlanInitialRun(ctx context.Context, userID, configurationID uint, req RunPlanRequest) (PlanDescriptor, error) {
	configuration, canonical, err := s.loadConfiguration(ctx, userID, configurationID)
	if err != nil {
		return PlanDescriptor{}, err
	}
	requested := req.RequestedSobol
	if requested == 0 {
		requested = core.InitialSobolCount(len(canonical.ParameterSpace.Axes))
	}
	validator, err := s.BuildPointValidator(ctx, userID, configurationID)
	if err != nil {
		return PlanDescriptor{}, err
	}
	global, err := core.PlanGlobalValidated(canonical.ParameterSpace, canonical.BaseCoordinates, requested, 0, nil, true, validator)
	if err != nil {
		return PlanDescriptor{}, err
	}
	return s.previewStage(ctx, userID, configuration, canonical, "global", global.Points, &global)
}

func (s *Service) PlanNextStage(ctx context.Context, userID, runID uint, req StagePlanRequest) (PlanDescriptor, error) {
	run, configuration, canonical, err := s.loadRun(ctx, userID, runID)
	if err != nil {
		return PlanDescriptor{}, err
	}
	if err := s.syncRun(ctx, userID, &run); err != nil {
		return PlanDescriptor{}, err
	}
	existing, err := s.existingPointHashes(ctx, configuration.ID)
	if err != nil {
		return PlanDescriptor{}, err
	}
	validator, err := s.BuildPointValidator(ctx, userID, configuration.ID)
	if err != nil {
		return PlanDescriptor{}, err
	}
	switch req.Kind {
	case "append_global":
		requested := req.RequestedSobol
		if requested == 0 {
			requested = core.InitialSobolCount(len(canonical.ParameterSpace.Axes)) << run.GlobalBatchCount
		}
		global, err := core.PlanGlobalValidated(canonical.ParameterSpace, canonical.BaseCoordinates, requested, run.NextSobolIndex, existing, false, validator)
		if err != nil {
			return PlanDescriptor{}, err
		}
		return s.previewStage(ctx, userID, configuration, canonical, "global", global.Points, &global)
	case "local_refinement":
		if req.CenterPointID == 0 || req.Radius < 1 {
			return PlanDescriptor{}, ErrInvalidRequest
		}
		var point saasstore.ResearchEvaluationPoint
		if err := s.db.WithContext(ctx).Where("id = ? AND configuration_id = ? AND status = ?", req.CenterPointID, configuration.ID, "completed").First(&point).Error; err != nil {
			return PlanDescriptor{}, ErrInvalidRequest
		}
		var coordinates []int
		if err := json.Unmarshal(point.Coordinates, &coordinates); err != nil {
			return PlanDescriptor{}, err
		}
		maximumPoints := 0
		if s.computeTasks != nil {
			maximumPoints = s.computeTasks.Limits().HardItemLimit
		}
		points, err := core.PlanLocalRefinementLimited(canonical.ParameterSpace, coordinates, req.Radius, existing, maximumPoints)
		if err != nil {
			return PlanDescriptor{}, err
		}
		points, _ = validPlannedPoints(points, validator)
		if len(points) == 0 {
			return PlanDescriptor{}, ErrInvalidRequest
		}
		return s.previewStage(ctx, userID, configuration, canonical, "local_refinement", points, nil)
	case "surrogate_proposals":
		if req.SurrogateID == 0 || len(req.ProposalIDs) == 0 {
			return PlanDescriptor{}, ErrInvalidRequest
		}
		var surrogate saasstore.SurrogateModelSnapshot
		if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND run_id = ? AND configuration_id = ? AND status = ?", req.SurrogateID, userID, run.ID, configuration.ID, "completed").First(&surrogate).Error; err != nil {
			return PlanDescriptor{}, ErrInvalidRequest
		}
		var proposals []saasstore.SurrogateProposal
		if err := s.db.WithContext(ctx).Where("surrogate_snapshot_id = ? AND id IN ?", surrogate.ID, req.ProposalIDs).Order("id ASC").Find(&proposals).Error; err != nil || len(proposals) != len(uniqueUint(req.ProposalIDs)) {
			return PlanDescriptor{}, ErrInvalidRequest
		}
		points := make([]core.PlannedPoint, 0, len(proposals))
		for _, proposal := range proposals {
			if proposal.ActualPointID != nil || existing[proposal.VectorHash] {
				continue
			}
			var coordinates []int
			var parameters map[string]float64
			if json.Unmarshal(proposal.Coordinates, &coordinates) != nil || json.Unmarshal(proposal.Parameters, &parameters) != nil {
				return PlanDescriptor{}, ErrInvalidRequest
			}
			points = append(points, core.PlannedPoint{Coordinates: coordinates, Parameters: parameters, VectorHash: proposal.VectorHash, OriginType: "surrogate_proposal", OriginKey: fmt.Sprintf("surrogate:%d:proposal:%d", surrogate.ID, proposal.ID)})
		}
		points, _ = validPlannedPoints(points, validator)
		if len(points) == 0 {
			return PlanDescriptor{}, ErrInvalidRequest
		}
		return s.previewStage(ctx, userID, configuration, canonical, "surrogate_proposals", points, nil)
	default:
		return PlanDescriptor{}, ErrInvalidRequest
	}
}

func validPlannedPoints(points []core.PlannedPoint, validator func(map[string]float64) error) ([]core.PlannedPoint, int) {
	if validator == nil {
		return points, 0
	}
	valid := make([]core.PlannedPoint, 0, len(points))
	rejected := 0
	for _, point := range points {
		if validator(point.Parameters) != nil {
			rejected++
			continue
		}
		valid = append(valid, point)
	}
	return valid, rejected
}

func (s *Service) previewStage(ctx context.Context, userID uint, configuration saasstore.ResearchConfiguration, canonical ConfigurationCanonical, stageType string, points []core.PlannedPoint, global *core.GlobalPlan) (PlanDescriptor, error) {
	if s.computeTasks == nil {
		return PlanDescriptor{}, computetask.ErrServiceUnavailable
	}
	spec, manifest, err := s.stageComputeSpec(ctx, userID, configuration, canonical, stageType, points, global, 0)
	if err != nil {
		return PlanDescriptor{}, err
	}
	preview, err := s.computeTasks.Preview(ctx, userID, spec)
	if err != nil {
		return PlanDescriptor{}, err
	}
	return PlanDescriptor{PlanKey: preview.PlanKey, ManifestHash: manifestHash(manifest), StageType: stageType, Global: global, Points: points, Compute: preview, NextSobolIndex: func() int64 {
		if global != nil {
			return global.NextSobolIndex
		}
		return 0
	}()}, nil
}

func (s *Service) StartRun(ctx context.Context, userID, configurationID uint, req StartRunRequest) (RunDescriptor, error) {
	if strings.TrimSpace(req.PlanKey) == "" || strings.TrimSpace(req.IdempotencyKey) == "" {
		return RunDescriptor{}, ErrInvalidRequest
	}
	configuration, canonical, err := s.loadConfiguration(ctx, userID, configurationID)
	if err != nil {
		return RunDescriptor{}, err
	}
	plan, err := s.PlanInitialRun(ctx, userID, configurationID, req.Plan)
	if err != nil {
		return RunDescriptor{}, err
	}
	if plan.PlanKey != req.PlanKey {
		return RunDescriptor{}, ErrPlanStale
	}
	runIdentity, _ := compute.CanonicalJSON(map[string]any{"configuration_hash": configuration.ConfigHash, "idempotency_key": strings.TrimSpace(req.IdempotencyKey), "version": RunSchemaVersion})
	runKey := "p10-run:" + compute.HashBytes(runIdentity)
	var existing saasstore.ResearchRun
	if err := s.db.WithContext(ctx).Where("owner_user_id = ? AND run_key = ?", userID, runKey).First(&existing).Error; err == nil {
		return s.GetRun(ctx, userID, existing.ID, true)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return RunDescriptor{}, err
	}
	spec, manifest, err := s.stageComputeSpec(ctx, userID, configuration, canonical, "global", plan.Points, plan.Global, 0)
	if err != nil {
		return RunDescriptor{}, err
	}
	task, err := s.computeTasks.Create(ctx, userID, spec, req.ConfirmSoftLimit)
	if err != nil {
		return RunDescriptor{}, err
	}
	now := time.Now().UTC()
	run := saasstore.ResearchRun{OwnerUserID: userID, ConfigurationID: configuration.ID, RunKey: runKey, SamplerVersion: core.SamplerVersion, RootSeed: req.Plan.RootSeed, NextSobolIndex: plan.NextSobolIndex, ExplorationStatus: "running", Status: "running", StartedAt: &now}
	manifestRaw, _ := compute.CanonicalJSON(manifest)
	stage := saasstore.ResearchStage{Ordinal: 0, StageKey: "global-0", StageType: "global", ManifestHash: manifestHash(manifest), Manifest: manifestRaw, ComputeTaskID: &task.ID, Status: task.Status, RequestedCount: plan.Global.RequestedSobol, UniqueCount: len(plan.Points), CacheHitCount: task.CacheHitCount, MissingCount: len(plan.Points) - task.CacheHitCount, SobolStartIndex: plan.Global.SobolStartIndex, SobolEndIndex: plan.Global.SobolEndIndex}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		stage.RunID = run.ID
		return tx.Create(&stage).Error
	})
	if err != nil {
		return RunDescriptor{}, err
	}
	if task.Status != compute.TaskStatusCompleted {
		if _, err := s.computeTasks.StartTask(ctx, userID, task.ID); err != nil {
			return RunDescriptor{}, err
		}
	}
	return s.GetRun(ctx, userID, run.ID, true)
}

func (s *Service) StartStage(ctx context.Context, userID, runID uint, req StartStageRequest) (RunDescriptor, error) {
	plan, err := s.PlanNextStage(ctx, userID, runID, req.Plan)
	if err != nil {
		return RunDescriptor{}, err
	}
	if plan.PlanKey != req.PlanKey {
		return RunDescriptor{}, ErrPlanStale
	}
	run, configuration, canonical, err := s.loadRun(ctx, userID, runID)
	if err != nil {
		return RunDescriptor{}, err
	}
	var ordinal int64
	s.db.WithContext(ctx).Model(&saasstore.ResearchStage{}).Where("run_id = ?", run.ID).Count(&ordinal)
	spec, manifest, err := s.stageComputeSpec(ctx, userID, configuration, canonical, plan.StageType, plan.Points, plan.Global, int(ordinal))
	if err != nil {
		return RunDescriptor{}, err
	}
	task, err := s.computeTasks.Create(ctx, userID, spec, req.ConfirmSoftLimit)
	if err != nil {
		return RunDescriptor{}, err
	}
	manifestRaw, _ := compute.CanonicalJSON(manifest)
	stage := saasstore.ResearchStage{RunID: run.ID, Ordinal: int(ordinal), StageKey: fmt.Sprintf("%s-%d", plan.StageType, ordinal), StageType: plan.StageType, ManifestHash: manifestHash(manifest), Manifest: manifestRaw, ComputeTaskID: &task.ID, Status: task.Status, RequestedCount: len(plan.Points), UniqueCount: len(plan.Points), CacheHitCount: task.CacheHitCount, MissingCount: len(plan.Points) - task.CacheHitCount}
	if plan.Global != nil {
		stage.RequestedCount = plan.Global.RequestedSobol
		stage.SobolStartIndex = plan.Global.SobolStartIndex
		stage.SobolEndIndex = plan.Global.SobolEndIndex
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked saasstore.ResearchRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_user_id = ?", run.ID, userID).First(&locked).Error; err != nil {
			return err
		}
		if plan.Global != nil && locked.NextSobolIndex != plan.Global.SobolStartIndex {
			return ErrPlanStale
		}
		updates := map[string]any{"status": "running", "exploration_status": "running", "paused_at": nil}
		if plan.Global != nil {
			updates["next_sobol_index"] = plan.Global.NextSobolIndex
		}
		if err := tx.Model(&locked).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Create(&stage).Error
	})
	if err != nil {
		return RunDescriptor{}, err
	}
	if task.Status != compute.TaskStatusCompleted {
		if _, err := s.computeTasks.StartTask(ctx, userID, task.ID); err != nil {
			return RunDescriptor{}, err
		}
	}
	return s.GetRun(ctx, userID, run.ID, true)
}

func (s *Service) stageComputeSpec(ctx context.Context, userID uint, configuration saasstore.ResearchConfiguration, canonical ConfigurationCanonical, stageType string, points []core.PlannedPoint, global *core.GlobalPlan, ordinal int) (computetask.CreateSpec, stageManifest, error) {
	if len(points) == 0 {
		return computetask.CreateSpec{}, stageManifest{}, ErrInvalidRequest
	}
	manifest := stageManifest{SchemaVersion: StageSchemaVersion, StageType: stageType, Global: global, Points: points}
	items := make([]compute.ManifestItemInput, 0, len(points))
	var gene saasstore.GeneRecord
	if err := s.db.WithContext(ctx).Where("id = ? AND strategy_id = ?", canonical.GenomeID, sigmoiddca.StrategyID).First(&gene).Error; err != nil {
		return computetask.CreateSpec{}, manifest, ErrInvalidRequest
	}
	base := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
	for _, point := range points {
		var input any
		var cacheKey string
		if canonical.DynamicPackage == nil {
			chromosome, err := robust.ChromosomeWithValues(base.Chromosome, point.Parameters)
			if err != nil {
				return computetask.CreateSpec{}, manifest, err
			}
			pointParams := base
			pointParams.Chromosome = chromosome
			customRaw, err := compute.CanonicalJSON(pointParams)
			if err != nil {
				return computetask.CreateSpec{}, manifest, err
			}
			bt := backtestRequest(canonical.Backtest, json.RawMessage(customRaw))
			value := robustnesssvc.PointExecutionInput{SchemaVersion: robustnesssvc.PointSchemaVersion, Backtest: bt}
			input = value
			raw, _ := compute.CanonicalJSON(bt)
			cacheKey = "p08-backtest:" + compute.HashBytes(raw)
		} else {
			value, err := s.dynamicInputForPoint(ctx, userID, canonical, point, base)
			if err != nil {
				return computetask.CreateSpec{}, manifest, err
			}
			input = value
			raw, _ := compute.CanonicalJSON(value)
			cacheKey = "p10-dynamic-backtest:" + compute.HashBytes(raw)
		}
		inputRaw, err := compute.CanonicalJSON(input)
		if err != nil {
			return computetask.CreateSpec{}, manifest, err
		}
		items = append(items, compute.ManifestItemInput{Key: point.VectorHash, CacheKey: cacheKey, Input: inputRaw, EstimatedUnits: 1})
	}
	executorType := robustnesssvc.PointExecutorType
	if canonical.DynamicPackage != nil {
		executorType = dynamicparamsvc.MaterializeExecutorType
	}
	settings := map[string]any{"schema_version": StageSchemaVersion, "configuration_id": configuration.ID, "configuration_hash": configuration.ConfigHash, "stage_type": stageType, "manifest_hash": manifestHash(manifest)}
	spec := computetask.CreateSpec{Kind: compute.TaskKindAtomic, TaskType: "p10.parameter-research." + stageType, Title: "參數研究：" + stageType, ExecutorType: executorType, Settings: settings, ResearchSettingID: fmt.Sprintf("p10-configuration:%d", configuration.ID), ResearchSettingHash: configuration.ConfigHash, StageKey: fmt.Sprintf("%s-%d", stageType, ordinal), StageType: stageType, StageOrder: ordinal, Items: items}
	return spec, manifest, nil
}

func backtestRequest(settings robustnesssvc.BacktestSettings, custom json.RawMessage) backtest.CreateRequest {
	return backtest.CreateRequest{StrategyID: sigmoiddca.StrategyID, InstrumentID: settings.InstrumentID, DataSource: settings.DataSource, MarketDataVersionID: settings.MarketDataVersionID, MarketDataContentHash: settings.MarketDataContentHash, ExecutionMode: settings.ExecutionMode, StartTimeMs: settings.StartTimeMs, EndTimeMs: settings.EndTimeMs, Symbol: settings.Symbol, Pair: settings.Symbol, Interval: settings.Interval, Source: backtest.SourceCustom, CustomParams: custom, InitialCapital: settings.InitialCapital, MonthlyDCA: settings.MonthlyDCA, FeeRate: settings.FeeRate, SpreadRate: settings.SpreadRate, LongTermFilterEnabled: settings.LongTermFilterEnabled, LongTermFilterMonths: settings.LongTermFilterMonths}
}

// BuildPointExecutionInput exposes M's exact static/K execution adapter to H
// without copying dynamic-policy materialization rules into the control system.
func (s *Service) BuildPointExecutionInput(ctx context.Context, userID, configurationID uint, parameters map[string]float64) (PointExecutionInput, error) {
	_, canonical, err := s.loadConfiguration(ctx, userID, configurationID)
	if err != nil {
		return PointExecutionInput{}, err
	}
	var gene saasstore.GeneRecord
	if err := s.db.WithContext(ctx).Where("id = ? AND strategy_id = ?", canonical.GenomeID, sigmoiddca.StrategyID).First(&gene).Error; err != nil {
		return PointExecutionInput{}, ErrInvalidRequest
	}
	base := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
	point := core.PlannedPoint{Parameters: parameters}
	if canonical.DynamicPackage == nil {
		chromosome, err := robust.ChromosomeWithValues(base.Chromosome, parameters)
		if err != nil {
			return PointExecutionInput{}, err
		}
		base.Chromosome = chromosome
		customRaw, err := compute.CanonicalJSON(base)
		if err != nil {
			return PointExecutionInput{}, err
		}
		bt := backtestRequest(canonical.Backtest, customRaw)
		inputRaw, err := compute.CanonicalJSON(robustnesssvc.PointExecutionInput{SchemaVersion: robustnesssvc.PointSchemaVersion, Backtest: bt})
		return PointExecutionInput{ExecutorType: robustnesssvc.PointExecutorType, Input: inputRaw, Backtest: bt}, err
	}
	dynamicInput, err := s.dynamicInputForPoint(ctx, userID, canonical, point, base)
	if err != nil {
		return PointExecutionInput{}, err
	}
	inputRaw, err := compute.CanonicalJSON(dynamicInput)
	return PointExecutionInput{ExecutorType: dynamicparamsvc.MaterializeExecutorType, Input: inputRaw, Backtest: dynamicInput.Backtest, Dynamic: true}, err
}

// BuildPointValidator loads M/K structure once and returns a pure validator
// suitable for deterministic random sampling. The returned closure performs no
// database, network, file or time access.
func (s *Service) BuildPointValidator(ctx context.Context, userID, configurationID uint) (func(map[string]float64) error, error) {
	_, canonical, err := s.loadConfiguration(ctx, userID, configurationID)
	if err != nil {
		return nil, err
	}
	var gene saasstore.GeneRecord
	if err := s.db.WithContext(ctx).Where("id = ? AND strategy_id = ?", canonical.GenomeID, sigmoiddca.StrategyID).First(&gene).Error; err != nil {
		return nil, ErrInvalidRequest
	}
	base := sigmoiddca.ParseParamsFromParamPack(gene.ParamPack)
	if canonical.DynamicPackage == nil {
		return func(parameters map[string]float64) error {
			_, err := robust.ChromosomeWithValues(base.Chromosome, parameters)
			return err
		}, nil
	}
	var policyRow saasstore.DynamicPolicyArtifact
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", canonical.DynamicPackage.PolicyArtifactID, userID).First(&policyRow).Error; err != nil {
		return nil, ErrInvalidRequest
	}
	var bundle dynamicparamsvc.PolicyBundle
	var schema dynamiccore.DynamicParameterSpaceSchema
	if err := json.Unmarshal(policyRow.Payload, &bundle); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(policyRow.ParameterSpace, &schema); err != nil {
		return nil, err
	}
	baseRaw, err := compute.CanonicalJSON(bundle)
	if err != nil {
		return nil, err
	}
	variables := map[string]dynamiccore.ParameterVariable{}
	for _, variable := range schema.Variables {
		variables[variable.StableID] = variable
	}
	return func(parameters map[string]float64) error {
		var candidate dynamicparamsvc.PolicyBundle
		if err := json.Unmarshal(baseRaw, &candidate); err != nil {
			return err
		}
		for stableID, value := range parameters {
			variable, ok := variables[stableID]
			if !ok {
				continue
			}
			if !finite(value) || value < variable.Lower-1e-9 || value > variable.Upper+1e-9 {
				return ErrInvalidRequest
			}
			if err := applyDynamicVariable(&candidate.Policy, variable, value); err != nil {
				return err
			}
		}
		return dynamiccore.ValidatePolicy(candidate.Policy)
	}, nil
}

func (s *Service) dynamicInputForPoint(ctx context.Context, userID uint, canonical ConfigurationCanonical, point core.PlannedPoint, base sigmoiddca.Params) (dynamicparamsvc.MaterializeExecutionInput, error) {
	ref := canonical.DynamicPackage
	var policyRow saasstore.DynamicPolicyArtifact
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", ref.PolicyArtifactID, userID).First(&policyRow).Error; err != nil {
		return dynamicparamsvc.MaterializeExecutionInput{}, ErrInvalidRequest
	}
	var bundle dynamicparamsvc.PolicyBundle
	if err := json.Unmarshal(policyRow.Payload, &bundle); err != nil {
		return dynamicparamsvc.MaterializeExecutionInput{}, err
	}
	var schema dynamiccore.DynamicParameterSpaceSchema
	if err := json.Unmarshal(policyRow.ParameterSpace, &schema); err != nil {
		return dynamicparamsvc.MaterializeExecutionInput{}, err
	}
	variables := map[string]dynamiccore.ParameterVariable{}
	for _, variable := range schema.Variables {
		variables[variable.StableID] = variable
	}
	for stableID, value := range point.Parameters {
		variable, ok := variables[stableID]
		if !ok {
			continue
		}
		if !finite(value) || value < variable.Lower-1e-9 || value > variable.Upper+1e-9 {
			return dynamicparamsvc.MaterializeExecutionInput{}, ErrInvalidRequest
		}
		if err := applyDynamicVariable(&bundle.Policy, variable, value); err != nil {
			return dynamicparamsvc.MaterializeExecutionInput{}, err
		}
	}
	if err := dynamiccore.ValidatePolicy(bundle.Policy); err != nil {
		return dynamicparamsvc.MaterializeExecutionInput{}, err
	}
	bundle.BaseChromosome = base.Chromosome
	bundleRaw, _ := compute.CanonicalJSON(bundle)
	policyHash := compute.HashBytes(bundleRaw)
	bt := backtestRequest(canonical.Backtest, json.RawMessage(nil))
	geneRaw, _ := compute.CanonicalJSON(base)
	bt.CustomParams = geneRaw
	return dynamicparamsvc.MaterializeExecutionInput{SchemaVersion: dynamicparamsvc.MaterializeInputVersion, StudyID: ref.StudyID, BasePolicyArtifactID: ref.PolicyArtifactID, ArtifactSetHash: ref.ArtifactSetHash, PredictionSnapshotHash: ref.PredictionSnapshotHash, PolicyHash: policyHash, PolicyOverride: &bundle, Scope: dynamicparamsvc.MarketScope{InstrumentID: canonical.Backtest.InstrumentID, DataSource: canonical.Backtest.DataSource, Symbol: canonical.Backtest.Symbol, Interval: canonical.Backtest.Interval, StartTimeMs: canonical.Backtest.StartTimeMs, EndTimeMs: canonical.Backtest.EndTimeMs, DatasetHash: canonical.DatasetHash}, Backtest: bt}, nil
}

func applyDynamicVariable(policy *dynamiccore.DynamicPolicy, variable dynamiccore.ParameterVariable, value float64) error {
	for i := range policy.Controls {
		control := &policy.Controls[i]
		if control.ParameterID != variable.ParameterID || control.Mode != variable.ControlMode {
			continue
		}
		switch variable.Role {
		case "global":
			control.GlobalValue = value
		case "base_logit":
			control.BaseLogit = value
		case "linear", "quadratic":
			found := false
			for j := range control.Terms {
				if control.Terms[j].Input == variable.PredictionInput {
					if variable.Role == "linear" {
						control.Terms[j].Linear = value
					} else {
						control.Terms[j].Quadratic = value
					}
					found = true
					break
				}
			}
			if !found {
				return ErrInvalidRequest
			}
		case "direction", "volatility", "interaction":
			var target map[string]float64
			switch variable.Role {
			case "direction":
				target = control.Effects.Direction
			case "volatility":
				target = control.Effects.Volatility
			default:
				target = control.Effects.Interaction
			}
			keys := make([]string, 0, len(target))
			for key := range target {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			if len(keys) < 2 {
				return ErrInvalidRequest
			}
			for _, key := range keys {
				target[key] = 0
			}
			target[keys[0]] = value
			target[keys[len(keys)-1]] = -value
		default:
			return ErrInvalidRequest
		}
		return nil
	}
	return ErrInvalidRequest
}

func (s *Service) GetRun(ctx context.Context, userID, runID uint, includePoints bool) (RunDescriptor, error) {
	run, _, _, err := s.loadRun(ctx, userID, runID)
	if err != nil {
		return RunDescriptor{}, err
	}
	if err := s.syncRun(ctx, userID, &run); err != nil {
		return RunDescriptor{}, err
	}
	return s.describeRun(ctx, run, includePoints)
}

func (s *Service) syncRun(ctx context.Context, userID uint, run *saasstore.ResearchRun) error {
	var stages []saasstore.ResearchStage
	if err := s.db.WithContext(ctx).Where("run_id = ?", run.ID).Order("ordinal ASC").Find(&stages).Error; err != nil {
		return err
	}
	globalBatches := 0
	globalPoints := map[uint]bool{}
	statuses := []string{}
	for i := range stages {
		stage := &stages[i]
		if stage.ComputeTaskID == nil {
			continue
		}
		task, err := s.computeTasks.Get(ctx, userID, *stage.ComputeTaskID)
		if err != nil {
			return err
		}
		if err := s.syncStage(ctx, *run, stage, task); err != nil {
			return err
		}
		statuses = append(statuses, task.Status)
		if stage.StageType == "global" && task.Status == compute.TaskStatusCompleted {
			globalBatches++
		}
		var origins []saasstore.ResearchPointOrigin
		if err := s.db.WithContext(ctx).Where("run_id = ? AND stage_id = ?", run.ID, stage.ID).Find(&origins).Error; err != nil {
			return err
		}
		if stage.StageType == "global" {
			for _, origin := range origins {
				globalPoints[origin.PointID] = true
			}
		}
	}
	status := compute.DeriveCompositeStatus(statuses)
	if status == compute.TaskStatusPlanned {
		status = "waiting"
	}
	exploration := run.ExplorationStatus
	if status == compute.TaskStatusCompleted {
		exploration = "checkpoint"
	}
	updates := map[string]any{"status": status, "exploration_status": exploration, "global_batch_count": globalBatches, "global_unique_point_count": len(globalPoints)}
	if err := s.db.WithContext(ctx).Model(run).Updates(updates).Error; err != nil {
		return err
	}
	run.Status, run.ExplorationStatus, run.GlobalBatchCount, run.GlobalUniquePointCount = status, exploration, globalBatches, len(globalPoints)
	return nil
}

func (s *Service) syncStage(ctx context.Context, run saasstore.ResearchRun, stage *saasstore.ResearchStage, task *computetask.TaskDescriptor) error {
	var manifest stageManifest
	if err := json.Unmarshal(stage.Manifest, &manifest); err != nil {
		return err
	}
	pointByHash := map[string]core.PlannedPoint{}
	for _, point := range manifest.Points {
		pointByHash[point.VectorHash] = point
	}
	var items []saasstore.ComputeTaskItem
	if err := s.db.WithContext(ctx).Where("compute_task_id = ? AND status IN ?", task.ID, []string{compute.ItemStatusCompleted, compute.ItemStatusCached}).Find(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
		planned, ok := pointByHash[item.ItemKey]
		if !ok {
			return fmt.Errorf("P10 任務點不在固定 manifest")
		}
		metrics, resultID, resultVersion, resultHash, err := s.decodePointResult(ctx, run.ConfigurationID, json.RawMessage(item.Result))
		if err != nil {
			return err
		}
		coordinatesRaw, _ := compute.CanonicalJSON(planned.Coordinates)
		parametersRaw, _ := compute.CanonicalJSON(planned.Parameters)
		metricsRaw, _ := compute.CanonicalJSON(metrics)
		point := saasstore.ResearchEvaluationPoint{ConfigurationID: run.ConfigurationID, VectorHash: planned.VectorHash, CoordinateKey: coordinateKey(planned.Coordinates), Coordinates: coordinatesRaw, Parameters: parametersRaw, Legality: "legal", Status: "completed", BacktestResultID: &resultID, BacktestResultVersion: resultVersion, BacktestResultContentHash: resultHash, MetricsVersion: metrics.Version, MetricsHash: compute.HashBytes(metricsRaw), Metrics: metricsRaw, Qualified: metrics.Qualified}
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "configuration_id"}, {Name: "vector_hash"}}, DoUpdates: clause.AssignmentColumns([]string{"status", "backtest_result_id", "backtest_result_version", "backtest_result_content_hash", "metrics_version", "metrics_hash", "metrics", "qualified", "updated_at"})}).Create(&point).Error; err != nil {
			return err
		}
		if point.ID == 0 {
			if err := s.db.WithContext(ctx).Where("configuration_id = ? AND vector_hash = ?", run.ConfigurationID, planned.VectorHash).First(&point).Error; err != nil {
				return err
			}
		}
		reasonRaw, _ := compute.CanonicalJSON(map[string]any{"origin": planned.OriginType, "origin_key": planned.OriginKey})
		origin := saasstore.ResearchPointOrigin{PointID: point.ID, RunID: run.ID, StageID: stage.ID, OriginKey: planned.OriginKey, OriginType: planned.OriginType, SobolIndex: planned.SobolIndex, Reason: reasonRaw}
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&origin).Error; err != nil {
			return err
		}
		if planned.OriginType == "surrogate_proposal" {
			if err := s.db.WithContext(ctx).Model(&saasstore.SurrogateProposal{}).Where("surrogate_snapshot_id IN (SELECT id FROM surrogate_model_snapshots WHERE run_id = ?) AND vector_hash = ?", run.ID, point.VectorHash).Update("actual_point_id", point.ID).Error; err != nil {
				return err
			}
		}
	}
	updates := map[string]any{"status": task.Status, "cache_hit_count": task.CacheHitCount, "completed_count": task.ValidResultCount, "failed_count": task.FailedCount, "missing_count": task.MissingCount, "error_message": task.Error}
	if task.Status == compute.TaskStatusCompleted {
		updates["completed_at"] = time.Now().UTC()
	}
	if err := s.db.WithContext(ctx).Model(stage).Updates(updates).Error; err != nil {
		return err
	}
	stage.Status, stage.CompletedCount, stage.FailedCount, stage.MissingCount = task.Status, task.ValidResultCount, task.FailedCount, task.MissingCount
	return nil
}

func (s *Service) decodePointResult(ctx context.Context, configurationID uint, raw json.RawMessage) (robust.RelativeMetrics, uint, string, string, error) {
	var configuration saasstore.ResearchConfiguration
	if err := s.db.WithContext(ctx).First(&configuration, configurationID).Error; err != nil {
		return robust.RelativeMetrics{}, 0, "", "", err
	}
	if !configuration.DynamicMode {
		var result robustnesssvc.PointExecutionResult
		if err := json.Unmarshal(raw, &result); err != nil || result.SchemaVersion != robustnesssvc.PointResultVersion {
			return robust.RelativeMetrics{}, 0, "", "", ErrInvalidRequest
		}
		return result.Metrics, result.BacktestResultID, result.BacktestResultVersion, result.BacktestResultContentHash, nil
	}
	var result dynamicparamsvc.MaterializeExecutionResult
	if err := json.Unmarshal(raw, &result); err != nil || result.SchemaVersion != dynamicparamsvc.MaterializeResultVersion {
		return robust.RelativeMetrics{}, 0, "", "", ErrInvalidRequest
	}
	metrics, err := s.metricsForResult(ctx, result.BacktestResultID)
	return metrics, result.BacktestResultID, result.BacktestResultVersion, result.BacktestResultContentHash, err
}

func (s *Service) metricsForResult(ctx context.Context, resultID uint) (robust.RelativeMetrics, error) {
	var summary saasstore.BacktestResultSummary
	if err := s.db.WithContext(ctx).Where("backtest_result_id = ?", resultID).First(&summary).Error; err != nil {
		return robust.RelativeMetrics{}, err
	}
	var payload struct {
		Extra json.RawMessage `json:"extra"`
	}
	if err := json.Unmarshal(summary.Payload, &payload); err != nil {
		return robust.RelativeMetrics{}, err
	}
	var benchmark struct {
		Final    float64 `json:"benchmark_final_equity"`
		Drawdown float64 `json:"benchmark_max_drawdown"`
	}
	if err := json.Unmarshal(payload.Extra, &benchmark); err != nil {
		return robust.RelativeMetrics{}, err
	}
	return robust.ComputeRelativeMetrics(robust.RelativeMetricInput{StrategyFinalNAV: summary.FinalEquity, BenchmarkFinalNAV: benchmark.Final, StrategyMaxDrawdown: summary.MaxDrawdown, BenchmarkMaxDrawdown: benchmark.Drawdown})
}

func (s *Service) PauseRun(ctx context.Context, userID, runID uint) error {
	return s.stopRun(ctx, userID, runID, "paused")
}
func (s *Service) CancelRun(ctx context.Context, userID, runID uint) error {
	return s.stopRun(ctx, userID, runID, "cancelled")
}
func (s *Service) stopRun(ctx context.Context, userID, runID uint, status string) error {
	run, _, _, err := s.loadRun(ctx, userID, runID)
	if err != nil {
		return err
	}
	var stage saasstore.ResearchStage
	if err := s.db.WithContext(ctx).Where("run_id = ? AND compute_task_id IS NOT NULL", run.ID).Order("ordinal DESC").First(&stage).Error; err == nil && stage.ComputeTaskID != nil {
		_, _ = s.computeTasks.Cancel(ctx, userID, *stage.ComputeTaskID)
	}
	now := time.Now().UTC()
	updates := map[string]any{"status": status, "exploration_status": status}
	if status == "paused" {
		updates["paused_at"] = now
	} else {
		updates["cancelled_at"] = now
	}
	return s.db.WithContext(ctx).Model(&run).Updates(updates).Error
}

func (s *Service) loadConfiguration(ctx context.Context, userID, id uint) (saasstore.ResearchConfiguration, ConfigurationCanonical, error) {
	var row saasstore.ResearchConfiguration
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, userID).First(&row).Error; err != nil {
		return row, ConfigurationCanonical{}, ErrNotFound
	}
	var canonical ConfigurationCanonical
	if err := json.Unmarshal(row.Canonical, &canonical); err != nil {
		return row, canonical, err
	}
	return row, canonical, nil
}
func (s *Service) loadRun(ctx context.Context, userID, id uint) (saasstore.ResearchRun, saasstore.ResearchConfiguration, ConfigurationCanonical, error) {
	var run saasstore.ResearchRun
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, userID).First(&run).Error; err != nil {
		return run, saasstore.ResearchConfiguration{}, ConfigurationCanonical{}, ErrNotFound
	}
	configuration, canonical, err := s.loadConfiguration(ctx, userID, run.ConfigurationID)
	return run, configuration, canonical, err
}
func (s *Service) existingPointHashes(ctx context.Context, configurationID uint) (map[string]bool, error) {
	var rows []saasstore.ResearchEvaluationPoint
	if err := s.db.WithContext(ctx).Select("vector_hash").Where("configuration_id = ?", configurationID).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := map[string]bool{}
	for _, row := range rows {
		result[row.VectorHash] = true
	}
	return result, nil
}

func (s *Service) ListRuns(ctx context.Context, userID, configurationID uint) ([]RunDescriptor, error) {
	if _, _, err := s.loadConfiguration(ctx, userID, configurationID); err != nil {
		return nil, err
	}
	var rows []saasstore.ResearchRun
	if err := s.db.WithContext(ctx).Where("configuration_id = ? AND owner_user_id = ?", configurationID, userID).Order("created_at DESC,id DESC").Limit(100).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]RunDescriptor, 0, len(rows))
	for i := range rows {
		if err := s.syncRun(ctx, userID, &rows[i]); err != nil {
			return nil, err
		}
		descriptor, err := s.describeRun(ctx, rows[i], false)
		if err != nil {
			return nil, err
		}
		result = append(result, descriptor)
	}
	return result, nil
}

func (s *Service) ListPoints(ctx context.Context, userID, runID uint, page, pageSize int, status string) (PointPageDescriptor, error) {
	run, _, _, err := s.loadRun(ctx, userID, runID)
	if err != nil {
		return PointPageDescriptor{}, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 500 {
		pageSize = 100
	}
	query := s.db.WithContext(ctx).Model(&saasstore.ResearchEvaluationPoint{}).Where("configuration_id = ?", run.ConfigurationID)
	if strings.TrimSpace(status) != "" {
		query = query.Where("status = ?", strings.TrimSpace(status))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PointPageDescriptor{}, err
	}
	var rows []saasstore.ResearchEvaluationPoint
	if err := query.Order("id ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return PointPageDescriptor{}, err
	}
	items := make([]PointDescriptor, 0, len(rows))
	for _, row := range rows {
		item, err := decodePoint(row)
		if err != nil {
			return PointPageDescriptor{}, err
		}
		items = append(items, item)
	}
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	return PointPageDescriptor{Items: items, Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages}, nil
}

func (s *Service) GetLandscape(ctx context.Context, userID, runID uint, axisX, axisY, metric string, limit int) (LandscapeDescriptor, error) {
	run, _, canonical, err := s.loadRun(ctx, userID, runID)
	if err != nil {
		return LandscapeDescriptor{}, err
	}
	axisNames := map[string]bool{}
	for _, axis := range canonical.ParameterSpace.Axes {
		axisNames[axis.Name] = true
	}
	if !axisNames[axisX] || !axisNames[axisY] || axisX == axisY {
		return LandscapeDescriptor{}, ErrInvalidRequest
	}
	if limit < 1 || limit > 5000 {
		limit = 2000
	}
	var total int64
	base := s.db.WithContext(ctx).Model(&saasstore.ResearchEvaluationPoint{}).Where("configuration_id = ? AND status = ?", run.ConfigurationID, "completed")
	if err := base.Count(&total).Error; err != nil {
		return LandscapeDescriptor{}, err
	}
	var rows []saasstore.ResearchEvaluationPoint
	if err := base.Order("id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return LandscapeDescriptor{}, err
	}
	items := make([]PointDescriptor, 0, len(rows))
	for _, row := range rows {
		item, err := decodePoint(row)
		if err != nil {
			return LandscapeDescriptor{}, err
		}
		items = append(items, item)
	}
	return LandscapeDescriptor{ConfigurationID: run.ConfigurationID, AxisX: axisX, AxisY: axisY, Metric: metric, Points: items, Truncated: total > int64(len(items))}, nil
}

func uniqueUint(values []uint) []uint {
	seen := map[uint]bool{}
	result := make([]uint, 0, len(values))
	for _, value := range values {
		if value != 0 && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
func (s *Service) describeRun(ctx context.Context, run saasstore.ResearchRun, includePoints bool) (RunDescriptor, error) {
	descriptor := RunDescriptor{ID: run.ID, ConfigurationID: run.ConfigurationID, RunKey: run.RunKey, SamplerVersion: run.SamplerVersion, NextSobolIndex: run.NextSobolIndex, GlobalUniquePointCount: run.GlobalUniquePointCount, GlobalBatchCount: run.GlobalBatchCount, ExplorationStatus: run.ExplorationStatus, Status: run.Status, CreatedAt: run.CreatedAt.UTC().Format(time.RFC3339)}
	var stages []saasstore.ResearchStage
	if err := s.db.WithContext(ctx).Where("run_id = ?", run.ID).Order("ordinal ASC").Find(&stages).Error; err != nil {
		return descriptor, err
	}
	for _, stage := range stages {
		descriptor.Stages = append(descriptor.Stages, StageDescriptor{ID: stage.ID, Ordinal: stage.Ordinal, StageKey: stage.StageKey, StageType: stage.StageType, ManifestHash: stage.ManifestHash, ComputeTaskID: stage.ComputeTaskID, Status: stage.Status, RequestedCount: stage.RequestedCount, UniqueCount: stage.UniqueCount, CacheHitCount: stage.CacheHitCount, CompletedCount: stage.CompletedCount, FailedCount: stage.FailedCount, MissingCount: stage.MissingCount, SobolStartIndex: stage.SobolStartIndex, SobolEndIndex: stage.SobolEndIndex, ErrorMessage: stage.ErrorMessage})
	}
	if includePoints {
		var points []saasstore.ResearchEvaluationPoint
		if err := s.db.WithContext(ctx).Where("configuration_id = ?", run.ConfigurationID).Order("id ASC").Limit(1000).Find(&points).Error; err != nil {
			return descriptor, err
		}
		for _, point := range points {
			decoded, err := decodePoint(point)
			if err != nil {
				return descriptor, err
			}
			descriptor.Points = append(descriptor.Points, decoded)
		}
	}
	return descriptor, nil
}

func decodePoint(point saasstore.ResearchEvaluationPoint) (PointDescriptor, error) {
	var coordinates []int
	var parameters map[string]float64
	if err := json.Unmarshal(point.Coordinates, &coordinates); err != nil {
		return PointDescriptor{}, err
	}
	if err := json.Unmarshal(point.Parameters, &parameters); err != nil {
		return PointDescriptor{}, err
	}
	descriptor := PointDescriptor{ID: point.ID, VectorHash: point.VectorHash, Coordinates: coordinates, Parameters: parameters, Status: point.Status, Qualified: point.Qualified, BacktestResultID: point.BacktestResultID, BacktestResultContentHash: point.BacktestResultContentHash}
	if point.MetricsHash != "" {
		var metrics robust.RelativeMetrics
		if err := json.Unmarshal(point.Metrics, &metrics); err != nil {
			return descriptor, err
		}
		descriptor.Metrics = &metrics
	}
	return descriptor, nil
}
func manifestHash(manifest stageManifest) string {
	raw, _ := compute.CanonicalJSON(manifest)
	return compute.HashBytes(raw)
}
func coordinateKey(coordinates []int) string {
	parts := make([]string, len(coordinates))
	for i, value := range coordinates {
		parts[i] = fmt.Sprint(value)
	}
	return strings.Join(parts, ":")
}
