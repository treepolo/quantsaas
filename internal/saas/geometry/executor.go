package geometry

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
	compute "quantsaas/internal/compute"
	core "quantsaas/internal/dynamicparam"
	"quantsaas/internal/quant"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"
)

type Executor struct{ db *gorm.DB }

func NewExecutor(db *gorm.DB) *Executor { return &Executor{db: db} }
func (e *Executor) Descriptor() compute.ExecutorDescriptor {
	return compute.ExecutorDescriptor{Type: TrainExecutorType, Version: TrainExecutorVersion, ResultSchemaVersion: TrainResultVersion}
}

func (e *Executor) Execute(ctx context.Context, execution computetask.Execution) (json.RawMessage, error) {
	var input TrainInput
	if err := json.Unmarshal(execution.Input, &input); err != nil {
		return nil, err
	}
	if input.SchemaVersion != TrainInputVersion || (input.Horizon != core.HorizonOneDay && input.Horizon != core.HorizonTwentyDay) {
		return nil, ErrInvalidRequest
	}
	bars, err := loadBars(ctx, e.db, input.Scope)
	if err != nil {
		return nil, err
	}
	input.Config.Lookbacks = uniqueLookbacks(input.Config.Lookbacks)
	input.Config.MinimumTrain = maxGeometry(input.Config.MinimumTrain, 8)
	plannedUnits := int64(len(bars) * len(input.Config.Lookbacks) * 3)
	if execution.Heartbeat != nil {
		execution.Heartbeat("幾何模型訓練")
	}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: 0.05}); err != nil {
			return nil, err
		}
	}
	training, err := core.SelectGeometryModel(bars, input.Horizon, input.Config)
	if err != nil {
		return nil, err
	}
	if execution.CountUnits != nil {
		execution.CountUnits(plannedUnits / 2)
	}
	if execution.Heartbeat != nil {
		execution.Heartbeat("幾何預測")
	}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: 0.8}); err != nil {
			return nil, err
		}
	}
	predictions, err := core.PredictGeometryModel(training.SelectedModel, bars)
	if err != nil {
		return nil, err
	}
	if execution.CountUnits != nil {
		execution.CountUnits(plannedUnits - plannedUnits/2)
	}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: 1}); err != nil {
			return nil, err
		}
	}
	result := TrainResult{SchemaVersion: TrainResultVersion, Horizon: input.Horizon, DatasetHash: input.Scope.DatasetHash, Training: training, Predictions: predictions}
	identity, err := compute.CanonicalJSON(struct {
		SchemaVersion string                      `json:"schema_version"`
		Horizon       int                         `json:"horizon"`
		DatasetHash   string                      `json:"dataset_hash"`
		Training      core.GeometryTrainingResult `json:"training"`
	}{result.SchemaVersion, result.Horizon, result.DatasetHash, result.Training})
	if err != nil {
		return nil, err
	}
	result.ContentHash = compute.HashBytes(identity)
	return compute.CanonicalJSON(result)
}

func (e *Executor) ValidateCachedResult(ctx context.Context, userID uint, raw json.RawMessage) error {
	_ = ctx
	_ = userID
	var result TrainResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.SchemaVersion != TrainResultVersion || result.ContentHash == "" {
		return ErrInvalidRequest
	}
	return nil
}

func loadBars(ctx context.Context, db *gorm.DB, scope MarketScope) ([]quant.Bar, error) {
	if db == nil || scope.InstrumentID == "" || scope.DataSource == "" || scope.Symbol == "" || scope.Interval != "1d" || scope.DatasetHash == "" {
		return nil, ErrInvalidRequest
	}
	var rows []saasstore.KLine
	if err := db.WithContext(ctx).Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time >= ? AND open_time <= ?", scope.InstrumentID, scope.DataSource, scope.Symbol, scope.Interval, scope.StartTimeMs, scope.EndTimeMs).Order("open_time ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	bars := make([]quant.Bar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, quant.Bar{OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("沒有幾何模型行情資料")
	}
	hash, err := backtestresult.HashDataset(backtestresult.DatasetSchemaVersion, bars)
	if err != nil {
		return nil, err
	}
	if hash != scope.DatasetHash {
		return nil, fmt.Errorf("幾何模型 dataset hash 已改變")
	}
	return bars, nil
}
func uniqueLookbacks(values []int) []int {
	allowed := map[int]bool{5: true, 10: true, 20: true, 40: true, 60: true, 120: true, 250: true, 500: true}
	seen := map[int]bool{}
	result := []int{}
	for _, value := range values {
		if allowed[value] && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
func maxGeometry(a, b int) int {
	if a > b {
		return a
	}
	return b
}
