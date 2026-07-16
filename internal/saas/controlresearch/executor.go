package controlresearch

import (
	"context"
	"encoding/json"
	"fmt"

	"quantsaas/internal/backtestcore"
	compute "quantsaas/internal/compute"
	core "quantsaas/internal/controlresearch"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
)

type Executor struct {
	db        *gorm.DB
	backtests *backtest.Service
	results   *backtestresult.Store
}

func NewExecutor(db *gorm.DB, backtests *backtest.Service) *Executor {
	if backtests == nil {
		backtests = backtest.NewService(db)
	}
	return &Executor{db: db, backtests: backtests, results: backtestresult.NewStore(db)}
}

func (e *Executor) Descriptor() compute.ExecutorDescriptor {
	return compute.ExecutorDescriptor{Type: ExecutorType, Version: ExecutorVersion, ResultSchemaVersion: ExecutorResultVersion}
}

func (e *Executor) Execute(ctx context.Context, execution computetask.Execution) (json.RawMessage, error) {
	if e == nil || e.db == nil || e.backtests == nil {
		return nil, computetask.ErrServiceUnavailable
	}
	var input ExecutionInput
	if err := json.Unmarshal(execution.Input, &input); err != nil || input.SchemaVersion != ExecutionInputVersion || input.SequenceIndex < 0 {
		return nil, ErrInvalidRequest
	}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: .05}); err != nil {
			return nil, err
		}
	}
	var standard backtest.StandardExecutionResult
	var err error
	ruleType := ""
	switch input.Kind {
	case "rule":
		if input.Rule == nil {
			return nil, ErrInvalidRequest
		}
		ruleType = input.Rule.Type
		standard, err = e.backtests.EnsureRuleStandardResult(ctx, execution.UserID, input.Backtest, *input.Rule)
	case "shuffle":
		standard, err = e.executeShuffle(ctx, execution.UserID, input)
	default:
		return nil, ErrInvalidRequest
	}
	if err != nil {
		return nil, err
	}
	result := ExecutionResult{SchemaVersion: ExecutorResultVersion, Kind: input.Kind, SequenceIndex: input.SequenceIndex, RuleType: ruleType, BacktestResultID: standard.ID, BacktestResultVersion: standard.Version, BacktestResultContentHash: standard.ContentHash, ReusedBacktest: standard.Reused}
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

func (e *Executor) executeShuffle(ctx context.Context, userID uint, input ExecutionInput) (backtest.StandardExecutionResult, error) {
	var task saasstore.ControlAnalysisTask
	if err := e.db.WithContext(ctx).Where("owner_user_id = ? AND task_key = ?", userID, input.TaskKey).First(&task).Error; err != nil {
		return backtest.StandardExecutionResult{}, ErrNotFound
	}
	var baseline saasstore.ControlEvaluation
	if err := e.db.WithContext(ctx).Where("task_id = ? AND kind = ? AND sequence_index = 0", task.ID, "baseline").First(&baseline).Error; err != nil {
		return backtest.StandardExecutionResult{}, fmt.Errorf("正式參數回測尚未完成: %w", err)
	}
	if _, err := e.results.VerifyResult(ctx, baseline.BacktestResultID); err != nil {
		return backtest.StandardExecutionResult{}, err
	}
	loaded, err := e.results.Load(ctx, baseline.BacktestResultID, true)
	if err != nil {
		return backtest.StandardExecutionResult{}, err
	}
	values := make([]float64, 0)
	times := make([]int64, 0)
	for _, block := range loaded.Blocks {
		for _, point := range block.Points {
			times = append(times, point.TimeMs)
			values = append(values, point.ActualExposureWeight)
		}
	}
	shuffled, err := core.Shuffle(values, input.ShuffleSeed, input.SequenceIndex)
	if err != nil {
		return backtest.StandardExecutionResult{}, err
	}
	targets := make([]backtestcore.ExposureTarget, len(shuffled))
	for i := range shuffled {
		targets[i] = backtestcore.ExposureTarget{TimeMs: times[i], Weight: shuffled[i]}
	}
	permutationRaw, err := compute.CanonicalJSON(targets)
	if err != nil {
		return backtest.StandardExecutionResult{}, err
	}
	identity := backtest.ExposureReplayIdentity{SchemaVersion: ExecutionInputVersion, SourceResultID: baseline.BacktestResultID, SourceVersion: baseline.BacktestResultVersion, SourceContentHash: baseline.BacktestResultContentHash, ShuffleVersion: core.ShuffleVersion, ShuffleSeed: input.ShuffleSeed, SequenceIndex: input.SequenceIndex, PermutationHash: compute.HashBytes(permutationRaw)}
	return e.backtests.EnsureExposureReplayStandardResult(ctx, userID, input.Backtest, targets, identity)
}

func (e *Executor) ValidateCachedResult(ctx context.Context, _ uint, raw json.RawMessage) error {
	if e == nil || e.db == nil {
		return computetask.ErrServiceUnavailable
	}
	var result ExecutionResult
	if err := json.Unmarshal(raw, &result); err != nil || result.SchemaVersion != ExecutorResultVersion || result.BacktestResultID == 0 {
		return ErrInvalidRequest
	}
	var stored saasstore.BacktestResult
	if err := e.db.WithContext(ctx).Where("id = ? AND status = ?", result.BacktestResultID, saasstore.BacktestResultStatusCompleted).First(&stored).Error; err != nil {
		return err
	}
	if stored.ResultVersion != result.BacktestResultVersion || stored.ContentHash != result.BacktestResultContentHash {
		return ErrInvalidRequest
	}
	return nil
}
