package robustness

import (
	"context"
	"encoding/json"
	"fmt"

	compute "quantsaas/internal/compute"
	core "quantsaas/internal/robustness"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
)

type PointExecutor struct {
	db        *gorm.DB
	backtests *backtest.Service
	slots     chan struct{}
}

// Keep one full backtest resident at a time for a robustness task. Each point
// materializes bars and a complete result path; allowing several points to do
// that concurrently can exhaust the SaaS container even when the task item
// count itself is modest.
const defaultPointConcurrency = 1

func NewPointExecutor(db *gorm.DB) *PointExecutor {
	return &PointExecutor{db: db, backtests: backtest.NewService(db), slots: make(chan struct{}, defaultPointConcurrency)}
}

func (e *PointExecutor) Descriptor() compute.ExecutorDescriptor {
	return compute.ExecutorDescriptor{Type: PointExecutorType, Version: PointExecutorVersion, ResultSchemaVersion: PointResultVersion}
}

func (e *PointExecutor) Execute(ctx context.Context, execution computetask.Execution) (json.RawMessage, error) {
	if e == nil || e.db == nil || e.backtests == nil {
		return nil, computetask.ErrServiceUnavailable
	}
	var input PointExecutionInput
	if err := json.Unmarshal(execution.Input, &input); err != nil {
		return nil, fmt.Errorf("解碼 P08 格點輸入失敗: %w", err)
	}
	if input.SchemaVersion != PointSchemaVersion {
		return nil, ErrInvalidRequest
	}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: 0.05}); err != nil {
			return nil, err
		}
	}
	result, err := e.backtests.EnsureStandardResult(ctx, execution.UserID, input.Backtest)
	if err != nil {
		return nil, err
	}
	var extra struct {
		BenchmarkFinalEquity float64 `json:"benchmark_final_equity"`
		BenchmarkMaxDrawdown float64 `json:"benchmark_max_drawdown"`
	}
	if err := json.Unmarshal(result.Summary.Extra, &extra); err != nil {
		return nil, fmt.Errorf("標準回測摘要缺少基準資料: %w", err)
	}
	metrics, err := core.ComputeRelativeMetrics(core.RelativeMetricInput{
		StrategyFinalNAV: result.Summary.FinalEquity, BenchmarkFinalNAV: extra.BenchmarkFinalEquity,
		StrategyMaxDrawdown: result.Summary.MaxDrawdown, BenchmarkMaxDrawdown: extra.BenchmarkMaxDrawdown,
	})
	if err != nil {
		return nil, err
	}
	output := PointExecutionResult{
		SchemaVersion:    PointResultVersion,
		BacktestResultID: result.ID, BacktestResultVersion: result.Version,
		BacktestResultContentHash: result.ContentHash, Metrics: metrics, ReusedBacktest: result.Reused,
	}
	raw, err := compute.CanonicalJSON(output)
	if err != nil {
		return nil, err
	}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: 1}); err != nil {
			return nil, err
		}
	}
	return json.RawMessage(raw), nil
}

func (e *PointExecutor) ValidateCachedResult(ctx context.Context, userID uint, raw json.RawMessage) error {
	_ = userID
	if e == nil || e.db == nil {
		return computetask.ErrServiceUnavailable
	}
	var result PointExecutionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.SchemaVersion != PointResultVersion || result.BacktestResultID == 0 || result.Metrics.Version != core.MetricsVersion {
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
