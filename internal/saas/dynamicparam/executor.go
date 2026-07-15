package dynamicparam

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	compute "quantsaas/internal/compute"
	core "quantsaas/internal/dynamicparam"
	"quantsaas/internal/quant"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
)

type TrainExecutor struct{ db *gorm.DB }

func NewTrainExecutor(db *gorm.DB) *TrainExecutor { return &TrainExecutor{db: db} }

func (executor *TrainExecutor) Descriptor() compute.ExecutorDescriptor {
	return compute.ExecutorDescriptor{Type: TrainExecutorType, Version: TrainExecutorVersion, ResultSchemaVersion: TrainResultVersion}
}

func (executor *TrainExecutor) Execute(ctx context.Context, execution computetask.Execution) (json.RawMessage, error) {
	if executor == nil || executor.db == nil {
		return nil, computetask.ErrServiceUnavailable
	}
	var input TrainExecutionInput
	if err := json.Unmarshal(execution.Input, &input); err != nil {
		return nil, err
	}
	if input.SchemaVersion != TrainInputVersion || (input.Horizon != core.HorizonOneDay && input.Horizon != core.HorizonTwentyDay) {
		return nil, ErrInvalidRequest
	}
	bars, err := loadScopedBars(ctx, executor.db, input.Scope)
	if err != nil {
		return nil, err
	}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: 0.05}); err != nil {
			return nil, err
		}
	}
	model, err := core.TrainHorizon(bars, input.Horizon, input.Training)
	if err != nil {
		return nil, err
	}
	result := TrainExecutionResult{SchemaVersion: TrainResultVersion, Horizon: input.Horizon, DatasetHash: input.Scope.DatasetHash, Model: model}
	identity, err := compute.CanonicalJSON(struct {
		SchemaVersion string            `json:"schema_version"`
		Horizon       int               `json:"horizon"`
		DatasetHash   string            `json:"dataset_hash"`
		Model         core.HorizonModel `json:"model"`
	}{result.SchemaVersion, result.Horizon, result.DatasetHash, result.Model})
	if err != nil {
		return nil, err
	}
	result.ContentHash = compute.HashBytes(identity)
	raw, err := compute.CanonicalJSON(result)
	if err != nil {
		return nil, err
	}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: 1}); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

func (executor *TrainExecutor) ValidateCachedResult(ctx context.Context, userID uint, raw json.RawMessage) error {
	_ = ctx
	_ = userID
	var result TrainExecutionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.SchemaVersion != TrainResultVersion || result.ContentHash == "" || result.DatasetHash == "" {
		return ErrInvalidRequest
	}
	identity, err := compute.CanonicalJSON(struct {
		SchemaVersion string            `json:"schema_version"`
		Horizon       int               `json:"horizon"`
		DatasetHash   string            `json:"dataset_hash"`
		Model         core.HorizonModel `json:"model"`
	}{result.SchemaVersion, result.Horizon, result.DatasetHash, result.Model})
	if err != nil {
		return err
	}
	if compute.HashBytes(identity) != result.ContentHash {
		return ErrInvalidRequest
	}
	return nil
}

func loadScopedBars(ctx context.Context, db *gorm.DB, scope MarketScope) ([]quant.Bar, error) {
	if scope.InstrumentID == "" || scope.DataSource == "" || scope.Symbol == "" || scope.Interval == "" || scope.StartTimeMs <= 0 || scope.EndTimeMs < scope.StartTimeMs || scope.DatasetHash == "" {
		return nil, ErrInvalidRequest
	}
	var rows []saasstore.KLine
	if err := db.WithContext(ctx).Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time >= ? AND open_time <= ?", scope.InstrumentID, scope.DataSource, scope.Symbol, scope.Interval, scope.StartTimeMs, scope.EndTimeMs).Order("open_time ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("P09 訓練區間沒有行情資料")
	}
	bars := make([]quant.Bar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, quant.Bar{OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	hash, err := backtestresult.HashDataset(backtestresult.DatasetSchemaVersion, bars)
	if err != nil {
		return nil, err
	}
	if hash != scope.DatasetHash {
		return nil, fmt.Errorf("P09 dataset hash 已改變")
	}
	return bars, nil
}

type MaterializeExecutor struct {
	db       *gorm.DB
	backtest *backtest.Service
}

func NewMaterializeExecutor(db *gorm.DB, backtestService *backtest.Service) *MaterializeExecutor {
	return &MaterializeExecutor{db: db, backtest: backtestService}
}

func (executor *MaterializeExecutor) Descriptor() compute.ExecutorDescriptor {
	return compute.ExecutorDescriptor{Type: MaterializeExecutorType, Version: MaterializeExecutorVersion, ResultSchemaVersion: MaterializeResultVersion}
}

func (executor *MaterializeExecutor) Execute(ctx context.Context, execution computetask.Execution) (json.RawMessage, error) {
	if executor == nil || executor.db == nil || executor.backtest == nil {
		return nil, computetask.ErrServiceUnavailable
	}
	var input MaterializeExecutionInput
	if err := json.Unmarshal(execution.Input, &input); err != nil {
		return nil, err
	}
	if input.SchemaVersion != MaterializeInputVersion || input.StudyID == 0 || input.ArtifactSetHash == "" || input.PredictionSnapshotHash == "" || input.PolicyHash == "" {
		return nil, ErrInvalidRequest
	}
	var study saasstore.DynamicModelStudy
	if err := executor.db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND artifact_set_hash = ?", input.StudyID, execution.UserID, input.ArtifactSetHash).First(&study).Error; err != nil {
		return nil, ErrNotFound
	}
	var artifacts []saasstore.DynamicModelArtifact
	if err := executor.db.WithContext(ctx).Where("study_id = ? AND target_kind = ?", study.ID, "horizon_bundle").Order("horizon ASC").Find(&artifacts).Error; err != nil {
		return nil, err
	}
	if len(artifacts) != 2 {
		return nil, fmt.Errorf("P09 模型組不完整")
	}
	models := map[int]core.HorizonModel{}
	for _, artifact := range artifacts {
		var model core.HorizonModel
		if err := json.Unmarshal(artifact.Payload, &model); err != nil {
			return nil, err
		}
		canonical, err := compute.CanonicalJSON(model)
		if err != nil {
			return nil, err
		}
		if compute.HashBytes(canonical) != artifact.ContentHash {
			return nil, fmt.Errorf("P09 模型 artifact hash 不符")
		}
		models[artifact.Horizon] = model
	}
	_, oneOK := models[core.HorizonOneDay]
	twenty, twentyOK := models[core.HorizonTwentyDay]
	if !oneOK || !twentyOK || twenty.StructuralRules == nil {
		return nil, fmt.Errorf("P09 缺少 1 日／20 日模型或結構規則")
	}
	var snapshot saasstore.DynamicPredictionSnapshot
	if err := executor.db.WithContext(ctx).Where("study_id = ? AND content_hash = ?", study.ID, input.PredictionSnapshotHash).First(&snapshot).Error; err != nil {
		return nil, ErrNotFound
	}
	var blocks []saasstore.DynamicReportBlock
	if err := executor.db.WithContext(ctx).Where("owner_kind = ? AND owner_id = ? AND block_kind = ?", "prediction_snapshot", snapshot.ID, "oof_predictions").Order("block_index ASC").Find(&blocks).Error; err != nil {
		return nil, err
	}
	oneOOF, twentyOOF := []core.Prediction{}, []core.Prediction{}
	for _, block := range blocks {
		var payload PredictionBlockPayload
		if err := json.Unmarshal(block.Payload, &payload); err != nil {
			return nil, err
		}
		canonical, err := compute.CanonicalJSON(payload)
		if err != nil {
			return nil, err
		}
		if compute.HashBytes(canonical) != block.ContentHash {
			return nil, fmt.Errorf("P09 預測區塊 hash 不符")
		}
		oneOOF = append(oneOOF, payload.OneDay...)
		twentyOOF = append(twentyOOF, payload.TwentyDay...)
	}
	if len(oneOOF) == 0 || len(twentyOOF) == 0 {
		return nil, fmt.Errorf("P09 OOF 預測不完整")
	}
	var policyRow saasstore.DynamicPolicyArtifact
	policyQuery := executor.db.WithContext(ctx).Where("study_id = ? AND owner_user_id = ?", study.ID, execution.UserID)
	if input.PolicyOverride == nil {
		policyQuery = policyQuery.Where("content_hash = ?", input.PolicyHash)
	} else if input.BasePolicyArtifactID != 0 {
		policyQuery = policyQuery.Where("id = ?", input.BasePolicyArtifactID)
	} else {
		return nil, ErrInvalidRequest
	}
	if err := policyQuery.First(&policyRow).Error; err != nil {
		return nil, ErrNotFound
	}
	var policy PolicyBundle
	if input.PolicyOverride != nil {
		policy = *input.PolicyOverride
	} else if err := json.Unmarshal(policyRow.Payload, &policy); err != nil {
		return nil, err
	}
	canonicalPolicy, err := compute.CanonicalJSON(policy)
	if err != nil {
		return nil, err
	}
	if compute.HashBytes(canonicalPolicy) != input.PolicyHash {
		return nil, fmt.Errorf("P09 動態政策 hash 不符")
	}
	if policy.ModelVersion == "" || policyRow.ArtifactSetHash != input.ArtifactSetHash || policyRow.PredictionSnapshotID != snapshot.ID {
		return nil, fmt.Errorf("P09 動態政策來源版本不相容")
	}
	if err := core.ValidatePolicy(policy.Policy); err != nil {
		return nil, err
	}
	if err := core.ValidateStateRules(policy.StateRules); err != nil {
		return nil, err
	}
	if policyUsesPredictions(policy.Policy) && !allPredictionTargetsValidated(models) {
		return nil, fmt.Errorf("P09 模型尚未全部通過 baseline gate，不能供動態持倉使用")
	}
	bars, err := loadScopedBars(ctx, executor.db, input.Scope)
	if err != nil {
		return nil, err
	}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: 0.1}); err != nil {
			return nil, err
		}
	}
	path, err := core.Materialize(bars, oneOOF, twentyOOF, core.MaterializationConfig{
		SchemaVersion: core.PredictionSchemaVersion, ActivityLookback: twenty.Activity.Lookback, ActivityScale: twenty.ActivityScale,
		StateRules: policy.StateRules, Policy: policy.Policy, BaseChromosome: policy.BaseChromosome,
		ModelArtifactHash: input.ArtifactSetHash, PredictionHash: input.PredictionSnapshotHash, PolicyHash: input.PolicyHash,
	})
	if err != nil {
		return nil, err
	}
	materializedRaw, err := compute.CanonicalJSON(path)
	if err != nil {
		return nil, err
	}
	contentHash := compute.HashBytes(materializedRaw)
	effective := make([]core.EffectiveSnapshot, 0, len(path.Diagnostics))
	for _, diagnostic := range path.Diagnostics {
		effective = append(effective, diagnostic.Effective)
	}
	effectiveRaw, err := compute.CanonicalJSON(effective)
	if err != nil {
		return nil, err
	}
	provider, err := core.ParameterProvider(path, policy.ModelVersion, policy.Policy.Version)
	if err != nil {
		return nil, err
	}
	standard, err := executor.backtest.EnsureDynamicStandardResult(ctx, execution.UserID, input.Backtest, backtest.DynamicExecutionMetadata{
		ModelArtifactHash: input.ArtifactSetHash, PredictionSchemaHash: input.PredictionSnapshotHash,
		MaterializedPredictionHash: contentHash, DynamicPolicyHash: input.PolicyHash,
		DynamicControlMode: policyControlMode(policy.Policy), EffectiveParametersHash: compute.HashBytes(effectiveRaw), ParameterProvider: provider,
	})
	if err != nil {
		return nil, err
	}
	result := MaterializeExecutionResult{SchemaVersion: MaterializeResultVersion, Materialized: path, ContentHash: contentHash, BacktestResultID: standard.ID, BacktestResultVersion: standard.Version, BacktestResultContentHash: standard.ContentHash}
	raw, err := compute.CanonicalJSON(result)
	if err != nil {
		return nil, err
	}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: 1}); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

func (executor *MaterializeExecutor) ValidateCachedResult(ctx context.Context, userID uint, raw json.RawMessage) error {
	_ = ctx
	_ = userID
	var result MaterializeExecutionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.SchemaVersion != MaterializeResultVersion || result.ContentHash == "" || result.BacktestResultID == 0 {
		return ErrInvalidRequest
	}
	materialized, err := compute.CanonicalJSON(result.Materialized)
	if err != nil {
		return err
	}
	if compute.HashBytes(materialized) != result.ContentHash {
		return ErrInvalidRequest
	}
	return nil
}

func policyControlMode(policy core.DynamicPolicy) string {
	modes := make([]string, 0, len(policy.Controls))
	seen := map[string]bool{}
	for _, control := range policy.Controls {
		if !seen[control.Mode] {
			seen[control.Mode] = true
			modes = append(modes, control.Mode)
		}
	}
	if len(modes) == 0 {
		return core.ControlFixed
	}
	sort.Strings(modes)
	return strings.Join(modes, "+")
}

func policyUsesPredictions(policy core.DynamicPolicy) bool {
	for _, control := range policy.Controls {
		if control.Mode == core.ControlContinuous || control.Mode == core.ControlSixState {
			return true
		}
	}
	return false
}

func allPredictionTargetsValidated(models map[int]core.HorizonModel) bool {
	for _, horizon := range []int{core.HorizonOneDay, core.HorizonTwentyDay} {
		model, ok := models[horizon]
		if !ok || !model.Direction.Report.BaselineGatePassed || !model.Joint.Report.BaselineGatePassed || !model.Activity.Report.BaselineGatePassed {
			return false
		}
	}
	return true
}
