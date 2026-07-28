package backtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"time"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/compute"
	"quantsaas/internal/marketversion"
	"quantsaas/internal/quant"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/ga"
	"quantsaas/internal/saas/marketdata"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/gorm"
)

const (
	SourceChampion  = "champion"
	SourceCandidate = "candidate"
	SourceCustom    = "custom"
	SourceBaseline  = "baseline"
)

var (
	ErrNotFound         = errors.New("找不到回測紀錄")
	ErrResultInProgress = errors.New("標準化回測結果仍在計算")
)

type Service struct {
	db            *gorm.DB
	instruments   *marketdata.InstrumentStore
	results       *backtestresult.Store
	runSigmoidDCA func(backtestcore.SigmoidDCARequest) (backtestcore.Result, error)
}

type CreateRequest struct {
	StrategyID            string            `json:"strategy_id"`
	InstanceID            uint              `json:"instance_id"`
	InstrumentID          string            `json:"instrument_id"`
	DataSource            string            `json:"data_source"`
	MarketDataVersionID   uint              `json:"market_data_version_id,omitempty"`
	MarketDataContentHash string            `json:"market_data_content_hash,omitempty"`
	ExecutionMode         string            `json:"execution_mode"`
	StartTimeMs           int64             `json:"start_time_ms"`
	EndTimeMs             int64             `json:"end_time_ms"`
	Pair                  string            `json:"pair"`
	Symbol                string            `json:"symbol"`
	Interval              string            `json:"interval"`
	Source                string            `json:"source"`
	CandidateID           uint              `json:"candidate_id"`
	GenomeID              uint              `json:"genome_id"`
	CustomParams          json.RawMessage   `json:"custom_params"`
	SpawnPoint            *quant.SpawnPoint `json:"spawn_point"`
	InitialCapital        *float64          `json:"initial_capital"`
	MonthlyDCA            *float64          `json:"monthly_dca"`
	FeeRate               *float64          `json:"fee_rate"`
	SpreadRate            *float64          `json:"spread_rate"`
	LongTermFilterEnabled *bool             `json:"long_term_filter_enabled"`
	LongTermFilterMonths  int               `json:"long_term_filter_months"`
}

type EquitySnapshot struct {
	Time                             string  `json:"time"`
	Price                            float64 `json:"price"`
	TotalAssets                      float64 `json:"total_assets"`
	Benchmark                        float64 `json:"benchmark"`
	StrategyChangePct                float64 `json:"strategy_change_pct"`
	BenchmarkChangePct               float64 `json:"benchmark_change_pct"`
	PracticalTargetWeight            float64 `json:"practical_target_weight"`
	PracticalTargetWeightChange      float64 `json:"practical_target_weight_change"`
	ModelTargetWeight                float64 `json:"model_target_weight"`
	ModelTargetWeightChange          float64 `json:"model_target_weight_change"`
	EmptyReferenceTargetWeight       float64 `json:"empty_reference_target_weight"`
	EmptyReferenceTargetWeightChange float64 `json:"empty_reference_target_weight_change"`
	Cash                             float64 `json:"cash"`
	AssetQuantity                    float64 `json:"asset_quantity"`
	ActualExposureWeight             float64 `json:"actual_exposure_weight"`
	IntradayExposureWeight           float64 `json:"intraday_exposure_weight,omitempty"`
	DailyReturn                      float64 `json:"daily_return"`
	PracticalTotalAssets             float64 `json:"practical_total_assets"`
	PracticalCash                    float64 `json:"practical_cash"`
	PracticalAssetQuantity           float64 `json:"practical_asset_quantity"`
	PracticalActualExposureWeight    float64 `json:"practical_actual_exposure_weight"`
	PracticalDailyReturn             float64 `json:"practical_daily_return"`
	LongTermFilterEnabled            bool    `json:"long_term_filter_enabled"`
	LongTermFilterReady              bool    `json:"long_term_filter_ready"`
	LongTermFilterRiskOff            bool    `json:"long_term_filter_risk_off"`
	LongTermFilterCurrentSMA         float64 `json:"long_term_filter_current_sma"`
	LongTermFilterPreviousSMA        float64 `json:"long_term_filter_previous_sma"`
	LongTermFilterSignal             string  `json:"long_term_filter_signal,omitempty"`
	LongTermFilterEvent              string  `json:"long_term_filter_event,omitempty"`
}

type WindowResult struct {
	Window            string  `json:"window"`
	Score             float64 `json:"score"`
	TotalReturn       float64 `json:"total_return"`
	BenchmarkReturn   float64 `json:"benchmark_return"`
	Alpha             float64 `json:"alpha"`
	MaxDrawdown       float64 `json:"max_drawdown"`
	BenchmarkDrawdown float64 `json:"benchmark_drawdown"`
}

type Response struct {
	ID                    uint               `json:"id"`
	Status                string             `json:"status"`
	BacktestResultID      uint               `json:"backtest_result_id,omitempty"`
	BacktestKey           string             `json:"backtest_key,omitempty"`
	ResultVersion         string             `json:"result_version,omitempty"`
	ResultContentHash     string             `json:"result_content_hash,omitempty"`
	ResultStatus          string             `json:"result_status,omitempty"`
	ReusedResult          bool               `json:"reused_result"`
	StrategyID            string             `json:"strategy_id"`
	Symbol                string             `json:"symbol"`
	InstrumentID          string             `json:"instrument_id"`
	DataSource            string             `json:"data_source"`
	ExecutionMode         string             `json:"execution_mode"`
	Interval              string             `json:"interval"`
	Source                string             `json:"source"`
	TotalReturn           float64            `json:"total_return"`
	Alpha                 float64            `json:"alpha"`
	MaxDrawdown           float64            `json:"max_drawdown"`
	FinalEquity           float64            `json:"final_equity"`
	Benchmark             float64            `json:"benchmark"`
	BenchmarkReturn       float64            `json:"benchmark_return"`
	BenchmarkMaxDrawdown  float64            `json:"benchmark_max_drawdown"`
	BenchmarkFinalEquity  float64            `json:"benchmark_final_equity"`
	FeeRate               float64            `json:"fee_rate"`
	SpreadRate            float64            `json:"spread_rate"`
	FeeCost               float64            `json:"fee_cost"`
	SlippageCost          float64            `json:"slippage_cost"`
	TotalExecutionCost    float64            `json:"total_execution_cost"`
	RebalanceThreshold    float64            `json:"rebalance_threshold"`
	ForceFullThreshold    float64            `json:"force_full_threshold"`
	ForceEmptyThreshold   float64            `json:"force_empty_threshold"`
	PositionStructure     string             `json:"position_structure"`
	TradeCount            int                `json:"trade_count"`
	LongTermFilterEnabled bool               `json:"long_term_filter_enabled"`
	LongTermFilterMonths  int                `json:"long_term_filter_months"`
	LongTermFilterVersion string             `json:"long_term_filter_version"`
	PracticalTotalReturn  float64            `json:"practical_total_return"`
	PracticalMaxDrawdown  float64            `json:"practical_max_drawdown"`
	PracticalFinalEquity  float64            `json:"practical_final_equity"`
	PracticalTradeCount   int                `json:"practical_trade_count"`
	WMean                 float64            `json:"w_mean"`
	WMomentum             float64            `json:"w_momentum"`
	WBreakout             float64            `json:"w_breakout"`
	NAV                   []EquitySnapshot   `json:"nav"`
	Windows               map[string]float64 `json:"windows"`
	WindowDetails         []WindowResult     `json:"window_details"`
	Error                 string             `json:"error,omitempty"`
	CreatedAt             string             `json:"created_at,omitempty"`
	FinishedAt            string             `json:"finished_at,omitempty"`
}

type StandardResultDescriptor struct {
	ID                 uint                         `json:"id"`
	Status             string                       `json:"status"`
	BacktestKey        string                       `json:"backtest_key"`
	ResultVersion      string                       `json:"result_version"`
	ResultContentHash  string                       `json:"result_content_hash,omitempty"`
	Spec               backtestresult.SpecSnapshot  `json:"spec"`
	Summary            *backtestresult.SummaryData  `json:"summary,omitempty"`
	Manifest           *backtestresult.PathManifest `json:"path_manifest,omitempty"`
	BacktestRunIDs     []uint                       `json:"backtest_run_ids"`
	PerformanceReports []PerformanceReportReference `json:"performance_reports"`
	KlineInverseRefs   []KlineInverseReference      `json:"kline_inverse_references,omitempty"`
	CreatedAt          string                       `json:"created_at"`
	CompletedAt        string                       `json:"completed_at,omitempty"`
}

type KlineInverseReference struct {
	StudyID      uint   `json:"study_id"`
	StudyName    string `json:"study_name"`
	PathID       uint   `json:"path_id"`
	EvaluationID uint   `json:"evaluation_id"`
	OutcomeState string `json:"outcome_state"`
	Permanent    bool   `json:"permanent"`
	BackLink     string `json:"back_link"`
}

type PerformanceReportReference struct {
	ID              uint            `json:"id"`
	Status          string          `json:"status"`
	AnalysisVersion string          `json:"analysis_version"`
	SchemaVersion   string          `json:"schema_version"`
	SettingsHash    string          `json:"settings_hash"`
	Settings        json.RawMessage `json:"settings"`
	ContentHash     string          `json:"content_hash,omitempty"`
	CreatedAt       string          `json:"created_at"`
}

type StandardPathBlockResponse struct {
	ResultID    uint                         `json:"result_id"`
	BlockIndex  int                          `json:"block_index"`
	ContentHash string                       `json:"content_hash"`
	Block       backtestresult.PathBlockData `json:"block"`
}

type storedResponseMetrics struct {
	Runner                string             `json:"runner,omitempty"`
	StrategyVersion       string             `json:"strategy_version,omitempty"`
	Alpha                 float64            `json:"alpha"`
	Benchmark             float64            `json:"benchmark"`
	BenchmarkReturn       float64            `json:"benchmark_return"`
	BenchmarkMaxDrawdown  float64            `json:"benchmark_max_drawdown"`
	BenchmarkFinalEquity  float64            `json:"benchmark_final_equity"`
	FeeRate               float64            `json:"fee_rate"`
	SpreadRate            float64            `json:"spread_rate"`
	FeeCost               float64            `json:"fee_cost"`
	SlippageCost          float64            `json:"slippage_cost"`
	TotalExecutionCost    float64            `json:"total_execution_cost"`
	RebalanceThreshold    float64            `json:"rebalance_threshold"`
	ForceFullThreshold    float64            `json:"force_full_threshold"`
	ForceEmptyThreshold   float64            `json:"force_empty_threshold"`
	PositionStructure     string             `json:"position_structure"`
	WMean                 float64            `json:"w_mean"`
	WMomentum             float64            `json:"w_momentum"`
	WBreakout             float64            `json:"w_breakout"`
	Windows               map[string]float64 `json:"windows"`
	WindowDetails         []WindowResult     `json:"window_details"`
	LongTermFilterEnabled bool               `json:"long_term_filter_enabled"`
	LongTermFilterMonths  int                `json:"long_term_filter_months"`
	LongTermFilterVersion string             `json:"long_term_filter_version"`
	PracticalTotalReturn  float64            `json:"practical_total_return"`
	PracticalMaxDrawdown  float64            `json:"practical_max_drawdown"`
	PracticalFinalEquity  float64            `json:"practical_final_equity"`
	PracticalTradeCount   int                `json:"practical_trade_count"`
}

type preparedBacktest struct {
	req               CreateRequest
	params            sigmoiddca.Params
	bars              []quant.Bar
	costs             quant.ExecutionCostConfig
	coreSpec          backtestcore.Spec
	identity          backtestresult.Identity
	parameterProvider backtestcore.ParameterProvider
	marketRegionRaw   []byte
	marketRegionCache *ga.MarketRegionFeatureCache
}

type DynamicExecutionMetadata struct {
	ModelArtifactHash          string
	PredictionSchemaHash       string
	MaterializedPredictionHash string
	DynamicPolicyHash          string
	DynamicControlMode         string
	EffectiveParametersHash    string
	ParameterProvider          backtestcore.ParameterProvider
}

// StandardExecutionResult is the P02/P03 result of a domain-owned calculation.
// It intentionally does not create a user BacktestRun; callers such as P08 own
// their request/evaluation records and reference this immutable result directly.
type StandardExecutionResult struct {
	ID          uint
	BacktestKey string
	Version     string
	ContentHash string
	Summary     backtestresult.SummaryData
	Reused      bool
}

// GeneratedPathRequest lets research modules execute immutable synthetic OHLC
// through the same P02 runner and P03 result store. The caller owns the path;
// this service owns only execution semantics and standardized artifacts.
type GeneratedPathRequest struct {
	Backtest          CreateRequest
	Parameters        sigmoiddca.Params
	Bars              []quant.Bar
	EvaluationStartMs int64
	PathHash          string
}

type instanceConfig struct {
	InitialUSDT       float64 `json:"initial_usdt"`
	MonthlyInjectUSDT float64 `json:"monthly_inject_usdt"`
	ColdSealedBTC     float64 `json:"cold_sealed_btc"`
}

func NewService(db *gorm.DB) *Service {
	return &Service{
		db:            db,
		instruments:   marketdata.NewInstrumentStore(db),
		results:       backtestresult.NewStore(db),
		runSigmoidDCA: backtestcore.RunSigmoidDCA,
	}
}

func (s *Service) EnsureStandardResult(ctx context.Context, userID uint, req CreateRequest) (StandardExecutionResult, error) {
	req = s.normalizeRequest(ctx, req)
	if err := s.validateBasicRequest(ctx, req); err != nil {
		return StandardExecutionResult{}, err
	}
	if err := s.validateMarketDataVersionOwner(ctx, userID, req); err != nil {
		return StandardExecutionResult{}, err
	}
	prepared, err := s.prepare(ctx, userID, req)
	if err != nil {
		return StandardExecutionResult{}, err
	}
	return s.ensureStandardPrepared(ctx, prepared)
}

func (s *Service) EnsureDynamicStandardResult(ctx context.Context, userID uint, req CreateRequest, dynamic DynamicExecutionMetadata) (StandardExecutionResult, error) {
	if dynamic.ModelArtifactHash == "" || dynamic.PredictionSchemaHash == "" || dynamic.MaterializedPredictionHash == "" || dynamic.DynamicPolicyHash == "" || dynamic.DynamicControlMode == "" || dynamic.EffectiveParametersHash == "" || dynamic.ParameterProvider == nil {
		return StandardExecutionResult{}, fmt.Errorf("動態參數回測缺少完整不可變身分")
	}
	req = s.normalizeRequest(ctx, req)
	if err := s.validateBasicRequest(ctx, req); err != nil {
		return StandardExecutionResult{}, err
	}
	if err := s.validateMarketDataVersionOwner(ctx, userID, req); err != nil {
		return StandardExecutionResult{}, err
	}
	prepared, err := s.prepareWithDynamic(ctx, userID, req, &dynamic)
	if err != nil {
		return StandardExecutionResult{}, err
	}
	return s.ensureStandardPrepared(ctx, prepared)
}

func (s *Service) EnsureGeneratedPathStandardResult(ctx context.Context, userID uint, input GeneratedPathRequest) (StandardExecutionResult, error) {
	if len(input.Bars) < 2 || strings.TrimSpace(input.PathHash) == "" || input.EvaluationStartMs <= input.Bars[0].OpenTime || input.EvaluationStartMs > input.Bars[len(input.Bars)-1].OpenTime {
		return StandardExecutionResult{}, fmt.Errorf("合成路徑缺少完整 W＋H 身分")
	}
	req := s.normalizeRequest(ctx, input.Backtest)
	disabled := false
	req.LongTermFilterEnabled = &disabled
	req.LongTermFilterMonths = 0
	req.Source = SourceCustom
	req.StartTimeMs = input.Bars[0].OpenTime
	req.EndTimeMs = input.Bars[len(input.Bars)-1].OpenTime
	if err := s.validateBasicRequest(ctx, req); err != nil {
		return StandardExecutionResult{}, err
	}
	for index, bar := range input.Bars {
		if index > 0 && bar.OpenTime <= input.Bars[index-1].OpenTime {
			return StandardExecutionResult{}, fmt.Errorf("合成路徑日期必須嚴格遞增")
		}
		if bar.Open <= 0 || bar.High < math.Max(bar.Open, bar.Close) || bar.Low <= 0 || bar.Low > math.Min(bar.Open, bar.Close) || bar.Close <= 0 {
			return StandardExecutionResult{}, fmt.Errorf("合成路徑第 %d 根 OHLC 不合法", index)
		}
	}
	params := input.Parameters
	params.PositionStructure = sigmoiddca.NormalizePositionStructure(params.PositionStructure)
	params.Spawn.Policy.MonthlyInjectUSDT = 0
	params.Spawn.Policy.ColdSealedBTC = 0
	if req.InitialCapital != nil {
		params.Spawn.Policy.InitialUSDT = *req.InitialCapital
	}
	if err := normalizeSpawnPoint(&params.Spawn); err != nil {
		return StandardExecutionResult{}, err
	}
	costs := backtestCosts(req)
	coreSpec := backtestcore.Spec{
		Runner: backtestcore.RunnerSigmoidDCA, InstrumentID: req.InstrumentID, Symbol: req.Symbol,
		DataSource: req.DataSource, Interval: req.Interval, ExecutionMode: req.ExecutionMode,
		PositionStructure: params.PositionStructure, StartTimeMs: input.Bars[0].OpenTime,
		EndTimeMs: input.Bars[len(input.Bars)-1].OpenTime, EvaluationStartMs: input.EvaluationStartMs,
		EvaluationEndMs: input.Bars[len(input.Bars)-1].OpenTime, PrefixMode: backtestcore.PrefixModeHistoryOnly,
		InitialCapital: params.Spawn.Policy.InitialUSDT, MonthlyContribution: 0, InitialAssetQuantity: 0,
		Costs: costs, CoreVersion: backtestcore.CoreVersion,
	}
	identity, err := backtestresult.BuildIdentity(backtestresult.SpecInput{
		StrategyID: sigmoiddca.StrategyID, StrategyVersion: sigmoiddca.StrategyVersion,
		ParameterSchemaVersion: backtestresult.ParameterSchemaV1, Parameters: params,
		CoreSpec: coreSpec, DatasetVersion: "p12-generated-ohlc-v1", DatasetHash: input.PathHash,
	}, input.Bars)
	if err != nil {
		return StandardExecutionResult{}, err
	}
	coreSpec.DatasetHash = identity.Snapshot.DatasetHash
	return s.ensureStandardPrepared(ctx, preparedBacktest{req: req, params: params, bars: append([]quant.Bar(nil), input.Bars...), costs: costs, coreSpec: coreSpec, identity: identity})
}

func (s *Service) ensureStandardPrepared(ctx context.Context, prepared preparedBacktest) (StandardExecutionResult, error) {
	reservation, err := s.results.Reserve(ctx, prepared.identity)
	if err != nil {
		return StandardExecutionResult{}, err
	}
	if reservation.Reusable {
		return s.loadStandardExecution(ctx, reservation.Result.ID, true)
	}
	if !reservation.Created {
		return s.waitStandardExecution(ctx, reservation.Result.ID)
	}
	if err := s.results.MarkRunning(ctx, reservation.Result.ID); err != nil {
		return StandardExecutionResult{}, err
	}
	artifacts, err := s.executePrepared(ctx, prepared)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			_ = s.results.Cancel(context.Background(), reservation.Result.ID, err.Error())
		} else {
			_ = s.results.Fail(context.Background(), reservation.Result.ID, err)
		}
		return StandardExecutionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = s.results.Cancel(context.Background(), reservation.Result.ID, err.Error())
		return StandardExecutionResult{}, err
	}
	if err := s.results.Complete(ctx, reservation.Result.ID, artifacts); err != nil {
		_ = s.results.Fail(context.Background(), reservation.Result.ID, err)
		return StandardExecutionResult{}, err
	}
	if _, err := s.results.VerifyResult(ctx, reservation.Result.ID); err != nil {
		_ = s.results.Invalidate(context.Background(), reservation.Result.ID, err.Error())
		return StandardExecutionResult{}, err
	}
	return s.loadStandardExecution(ctx, reservation.Result.ID, false)
}

func (s *Service) waitStandardExecution(ctx context.Context, resultID uint) (StandardExecutionResult, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		loaded, err := s.results.Load(ctx, resultID, false)
		if err != nil {
			return StandardExecutionResult{}, err
		}
		switch loaded.Result.Status {
		case saasstore.BacktestResultStatusCompleted:
			return s.loadStandardExecution(ctx, resultID, true)
		case saasstore.BacktestResultStatusFailed, saasstore.BacktestResultStatusCancelled, saasstore.BacktestResultStatusInvalidated, saasstore.BacktestResultStatusArchived:
			return StandardExecutionResult{}, fmt.Errorf("標準化回測結果 %d 無法重用：%s %s", resultID, loaded.Result.Status, loaded.Result.ErrorMessage)
		}
		select {
		case <-ctx.Done():
			return StandardExecutionResult{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) loadStandardExecution(ctx context.Context, resultID uint, reused bool) (StandardExecutionResult, error) {
	if _, err := s.results.VerifyResult(ctx, resultID); err != nil {
		return StandardExecutionResult{}, err
	}
	loaded, err := s.results.Load(ctx, resultID, false)
	if err != nil {
		return StandardExecutionResult{}, err
	}
	if loaded.Summary == nil {
		return StandardExecutionResult{}, fmt.Errorf("標準化回測結果缺少摘要")
	}
	return StandardExecutionResult{
		ID: loaded.Result.ID, BacktestKey: loaded.Result.BacktestKey,
		Version: loaded.Result.ResultVersion, ContentHash: loaded.Result.ContentHash,
		Summary: *loaded.Summary, Reused: reused,
	}, nil
}

func (s *Service) Create(ctx context.Context, userID uint, req CreateRequest) (*Response, error) {
	req = s.normalizeRequest(ctx, req)
	if err := s.validateBasicRequest(ctx, req); err != nil {
		return nil, err
	}
	if err := s.validateMarketDataVersionOwner(ctx, userID, req); err != nil {
		return nil, err
	}

	requestRaw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	var instanceID *uint
	if req.InstanceID != 0 {
		instanceID = &req.InstanceID
	}
	now := time.Now().UTC()
	run := saasstore.BacktestRun{
		UserID:        userID,
		StrategyID:    req.StrategyID,
		InstanceID:    instanceID,
		InstrumentID:  req.InstrumentID,
		DataSource:    req.DataSource,
		ExecutionMode: req.ExecutionMode,
		StartTimeMs:   req.StartTimeMs,
		EndTimeMs:     req.EndTimeMs,
		Symbol:        req.Symbol,
		Interval:      req.Interval,
		Source:        req.Source,
		Status:        saasstore.BacktestStatusRunning,
		Request:       saasstore.JSONB(requestRaw),
		StartedAt:     &now,
	}
	if err := s.db.WithContext(ctx).Create(&run).Error; err != nil {
		return nil, err
	}
	var identity backtestresult.Identity
	var execute func() (backtestresult.Artifacts, error)
	if req.Source == SourceBaseline {
		prepared, prepareErr := s.prepareBaseline(ctx, req)
		if prepareErr != nil {
			s.finishRunFailure(ctx, run.ID, saasstore.BacktestStatusFailed, prepareErr.Error())
			return nil, prepareErr
		}
		identity = prepared.identity
		execute = func() (backtestresult.Artifacts, error) { return s.executeControlPrepared(prepared) }
	} else {
		prepared, prepareErr := s.prepare(ctx, userID, req)
		if prepareErr != nil {
			s.finishRunFailure(ctx, run.ID, saasstore.BacktestStatusFailed, prepareErr.Error())
			return nil, prepareErr
		}
		identity = prepared.identity
		execute = func() (backtestresult.Artifacts, error) { return s.executePrepared(ctx, prepared) }
	}

	reservation, err := s.results.Reserve(ctx, identity)
	if err != nil {
		s.finishRunFailure(ctx, run.ID, saasstore.BacktestStatusFailed, err.Error())
		return nil, err
	}
	resultID := reservation.Result.ID
	reused := !reservation.Created
	if err := s.db.WithContext(ctx).Model(&saasstore.BacktestRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"backtest_result_id": resultID,
		"reused_result":      reused,
	}).Error; err != nil {
		if reservation.Created {
			_ = s.results.Fail(ctx, resultID, err)
		}
		s.finishRunFailure(ctx, run.ID, saasstore.BacktestStatusFailed, err.Error())
		return nil, err
	}
	run.BacktestResultID = &resultID
	run.ReusedResult = reused

	if reservation.Reusable {
		if _, err := s.results.VerifyResult(ctx, resultID); err != nil {
			s.finishRunFailure(ctx, run.ID, saasstore.BacktestStatusFailed, err.Error())
			return nil, err
		}
		finished := time.Now().UTC()
		if err := s.finishLinkedRunsCompleted(ctx, resultID, finished); err != nil {
			return nil, err
		}
		run.Status = saasstore.BacktestStatusCompleted
		run.FinishedAt = &finished
		loaded, err := s.results.Load(ctx, resultID, true)
		if err != nil {
			return nil, err
		}
		return responseFromStored(run, loaded)
	}
	if !reservation.Created {
		return pendingResponse(run, reservation.Result), nil
	}

	if err := s.results.MarkRunning(ctx, resultID); err != nil {
		s.finishRunFailure(ctx, run.ID, saasstore.BacktestStatusFailed, err.Error())
		return nil, err
	}
	artifacts, err := execute()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			_ = s.results.Cancel(context.Background(), resultID, err.Error())
			s.finishLinkedRunsFailure(context.Background(), resultID, saasstore.BacktestStatusCancelled, err.Error())
		} else {
			s.finishOwnedResultFailure(ctx, resultID, err)
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = s.results.Cancel(context.Background(), resultID, err.Error())
		s.finishLinkedRunsFailure(context.Background(), resultID, saasstore.BacktestStatusCancelled, err.Error())
		return nil, err
	}
	if err := s.results.Complete(ctx, resultID, artifacts); err != nil {
		s.finishOwnedResultFailure(ctx, resultID, err)
		return nil, err
	}
	if _, err := s.results.VerifyResult(ctx, resultID); err != nil {
		_ = s.results.Invalidate(ctx, resultID, err.Error())
		s.finishLinkedRunsFailure(ctx, resultID, saasstore.BacktestStatusFailed, err.Error())
		return nil, err
	}
	finished := time.Now().UTC()
	if err := s.finishLinkedRunsCompleted(ctx, resultID, finished); err != nil {
		return nil, err
	}
	run.Status = saasstore.BacktestStatusCompleted
	run.FinishedAt = &finished
	loaded, err := s.results.Load(ctx, resultID, true)
	if err != nil {
		return nil, err
	}
	return responseFromStored(run, loaded)
}

func (s *Service) Get(ctx context.Context, userID uint, id uint) (*Response, error) {
	var run saasstore.BacktestRun
	if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if run.BacktestResultID != nil {
		loaded, err := s.results.Load(ctx, *run.BacktestResultID, true)
		if err != nil {
			return nil, err
		}
		switch loaded.Result.Status {
		case saasstore.BacktestResultStatusCompleted, saasstore.BacktestResultStatusInvalidated, saasstore.BacktestResultStatusArchived:
			if _, err := backtestresult.VerifyRecords(loaded.Spec, loaded.Result, loaded.SummaryModel, loaded.BlockModels); err != nil {
				return nil, err
			}
			if run.Status == saasstore.BacktestStatusRunning {
				finished := time.Now().UTC()
				if loaded.Result.CompletedAt != nil {
					finished = *loaded.Result.CompletedAt
				}
				if err := s.finishLinkedRunsCompleted(ctx, loaded.Result.ID, finished); err != nil {
					return nil, err
				}
				run.Status = saasstore.BacktestStatusCompleted
				run.FinishedAt = &finished
			}
			return responseFromStored(run, loaded)
		case saasstore.BacktestResultStatusFailed, saasstore.BacktestResultStatusCancelled:
			status := saasstore.BacktestStatusFailed
			if loaded.Result.Status == saasstore.BacktestResultStatusCancelled {
				status = saasstore.BacktestStatusCancelled
			}
			response := pendingResponse(run, loaded.Result)
			response.Status = status
			response.Error = loaded.Result.ErrorMessage
			return response, nil
		default:
			return pendingResponse(run, loaded.Result), nil
		}
	}
	if run.Status != saasstore.BacktestStatusCompleted {
		return &Response{
			ID:            run.ID,
			Status:        run.Status,
			StrategyID:    run.StrategyID,
			Symbol:        run.Symbol,
			InstrumentID:  run.InstrumentID,
			DataSource:    run.DataSource,
			ExecutionMode: run.ExecutionMode,
			Interval:      run.Interval,
			Source:        run.Source,
			Error:         run.ErrorMessage,
			CreatedAt:     run.CreatedAt.Format(time.RFC3339),
		}, nil
	}

	var response Response
	if err := json.Unmarshal([]byte(run.Result), &response); err != nil {
		return nil, err
	}
	response.ID = run.ID
	response.Status = run.Status
	response.CreatedAt = run.CreatedAt.Format(time.RFC3339)
	if run.FinishedAt != nil {
		response.FinishedAt = run.FinishedAt.Format(time.RFC3339)
	}
	return &response, nil
}

func (s *Service) prepare(ctx context.Context, userID uint, req CreateRequest) (preparedBacktest, error) {
	return s.prepareWithDynamic(ctx, userID, req, nil)
}

func (s *Service) prepareWithDynamic(ctx context.Context, userID uint, req CreateRequest, dynamic *DynamicExecutionMetadata) (preparedBacktest, error) {
	params, err := s.resolveParams(ctx, userID, req)
	if err != nil {
		return preparedBacktest{}, err
	}
	spawn := params.Spawn
	if err := normalizeSpawnPoint(&spawn); err != nil {
		return preparedBacktest{}, err
	}

	bars, err := s.loadBars(ctx, req)
	if err != nil {
		return preparedBacktest{}, err
	}
	if len(bars) == 0 {
		return preparedBacktest{}, fmt.Errorf("尚未匯入 %s %s 的 K 線資料", req.Symbol, req.Interval)
	}

	costs := backtestCosts(req)
	params.Spawn = spawn
	params.PositionStructure = sigmoiddca.NormalizePositionStructure(params.PositionStructure)
	coreSpec := backtestcore.Spec{
		Runner:               backtestcore.RunnerSigmoidDCA,
		InstrumentID:         req.InstrumentID,
		Symbol:               req.Symbol,
		DataSource:           req.DataSource,
		Interval:             req.Interval,
		ExecutionMode:        req.ExecutionMode,
		PositionStructure:    params.PositionStructure,
		StartTimeMs:          bars[0].OpenTime,
		EndTimeMs:            bars[len(bars)-1].OpenTime,
		EvaluationStartMs:    bars[0].OpenTime,
		EvaluationEndMs:      bars[len(bars)-1].OpenTime,
		PrefixMode:           backtestcore.PrefixModeExecute,
		InitialCapital:       spawn.Policy.InitialUSDT,
		MonthlyContribution:  spawn.Policy.MonthlyInjectUSDT,
		InitialAssetQuantity: spawn.Policy.ColdSealedBTC,
		Costs:                costs,
		LongTermFilter:       longTermFilterConfig(req),
		CoreVersion:          backtestcore.CoreVersion,
	}
	var modelArtifactHash string
	var predictionSchemaHash string
	var materializedPredictionHash string
	var dynamicPolicyHash string
	var dynamicControlMode string
	var effectiveParametersHash string
	var parameterProvider backtestcore.ParameterProvider
	var marketRegionRaw []byte
	marketRegionCache := ga.NewMarketRegionFeatureCache()
	if dynamic != nil {
		modelArtifactHash = dynamic.ModelArtifactHash
		predictionSchemaHash = dynamic.PredictionSchemaHash
		materializedPredictionHash = dynamic.MaterializedPredictionHash
		dynamicPolicyHash = dynamic.DynamicPolicyHash
		dynamicControlMode = dynamic.DynamicControlMode
		effectiveParametersHash = dynamic.EffectiveParametersHash
		parameterProvider = dynamic.ParameterProvider
	} else {
		provider, raw, handled, providerErr := s.resolveMarketRegionProvider(ctx, req, bars, marketRegionCache)
		if providerErr != nil {
			return preparedBacktest{}, providerErr
		}
		if handled {
			parameterProvider = provider
			effectiveParametersHash = compute.HashBytes(raw)
			marketRegionRaw = raw
		}
	}
	identity, err := backtestresult.BuildIdentity(backtestresult.SpecInput{
		StrategyID:                 req.StrategyID,
		StrategyVersion:            sigmoiddca.StrategyVersion,
		ParameterSchemaVersion:     backtestresult.ParameterSchemaV1,
		Parameters:                 params,
		CoreSpec:                   coreSpec,
		DatasetVersion:             backtestresult.DatasetSchemaVersion,
		LongTermFilterVersion:      coreSpec.LongTermFilter.Version,
		LongTermFilterSettings:     coreSpec.LongTermFilter,
		ModelArtifactHash:          modelArtifactHash,
		PredictionSchemaHash:       predictionSchemaHash,
		MaterializedPredictionHash: materializedPredictionHash,
		DynamicPolicyHash:          dynamicPolicyHash,
		DynamicControlMode:         dynamicControlMode,
		EffectiveParametersHash:    effectiveParametersHash,
	}, bars)
	if err != nil {
		return preparedBacktest{}, err
	}
	coreSpec.DatasetHash = identity.Snapshot.DatasetHash
	prepared := preparedBacktest{req: req, params: params, bars: bars, costs: costs, coreSpec: coreSpec, identity: identity, parameterProvider: parameterProvider, marketRegionRaw: marketRegionRaw, marketRegionCache: marketRegionCache}
	return prepared, nil
}

func (s *Service) executePrepared(ctx context.Context, prepared preparedBacktest) (backtestresult.Artifacts, error) {
	path, err := s.runSigmoidDCA(backtestcore.SigmoidDCARequest{
		Context:           ctx,
		Spec:              prepared.coreSpec,
		Bars:              prepared.bars,
		Params:            prepared.params,
		ParameterProvider: prepared.parameterProvider,
	})
	if err != nil {
		return backtestresult.Artifacts{}, err
	}
	pathMaxDrawdown := maxDrawdown(path.Path)
	practicalMaxDrawdown := maxDrawdown(practicalPath(path.Path))
	spawn := prepared.params.Spawn
	baseline := quant.SimulateGhostDCAFrom(prepared.bars, prepared.coreSpec.EvaluationStartMs, quant.GhostDCAConfig{
		InitialUSDT:       spawn.Policy.InitialUSDT,
		MonthlyInjectUSDT: spawn.Policy.MonthlyInjectUSDT,
		UseOpenExecution:  prepared.req.ExecutionMode == marketdata.ExecutionModeCloseNextOpen,
		Costs:             prepared.costs,
	})
	alpha := path.TotalReturn - baseline.ROI
	windows := map[string]float64{}
	windowDetails := []WindowResult{}
	if prepared.coreSpec.PrefixMode != backtestcore.PrefixModeHistoryOnly {
		windows, windowDetails, err = scoreWindows(ctx, prepared.bars, prepared.req.Symbol, prepared.req.Interval, prepared.req.ExecutionMode, prepared.params.Chromosome, &spawn, prepared.costs, prepared.params.PositionStructure, prepared.coreSpec.LongTermFilter, prepared.marketRegionRaw, prepared.marketRegionCache)
		if err != nil {
			return backtestresult.Artifacts{}, err
		}
	}
	metrics := storedResponseMetrics{
		Alpha:                 alpha,
		Benchmark:             baseline.FinalEquity,
		BenchmarkReturn:       baseline.ROI,
		BenchmarkMaxDrawdown:  baseline.MaxDrawdown,
		BenchmarkFinalEquity:  baseline.FinalEquity,
		FeeRate:               prepared.costs.FeeRate,
		SpreadRate:            prepared.costs.SpreadRate,
		FeeCost:               path.Costs.FeeCost,
		SlippageCost:          path.Costs.SlippageCost,
		TotalExecutionCost:    path.Costs.TotalCost,
		RebalanceThreshold:    prepared.params.Chromosome.RebalanceThreshold,
		ForceFullThreshold:    prepared.params.Chromosome.ForceFullThreshold,
		ForceEmptyThreshold:   prepared.params.Chromosome.ForceEmptyThreshold,
		PositionStructure:     prepared.params.PositionStructure,
		WMean:                 prepared.params.Chromosome.WMean,
		WMomentum:             prepared.params.Chromosome.WMomentum,
		WBreakout:             prepared.params.Chromosome.WBreakout,
		Windows:               windows,
		WindowDetails:         windowDetails,
		LongTermFilterEnabled: prepared.coreSpec.LongTermFilter.Enabled,
		LongTermFilterMonths:  prepared.coreSpec.LongTermFilter.Months,
		LongTermFilterVersion: prepared.coreSpec.LongTermFilter.Version,
		PracticalTotalReturn:  path.PracticalTotalReturn,
		PracticalMaxDrawdown:  practicalMaxDrawdown,
		PracticalFinalEquity:  path.PracticalFinalAssets,
		PracticalTradeCount:   path.PracticalTradeCount,
	}
	summary, err := backtestresult.BuildSummary(path, pathMaxDrawdown, backtestresult.SummaryOptions{Extra: metrics})
	if err != nil {
		return backtestresult.Artifacts{}, err
	}
	standardPath := standardizedPath(path.Path, baseline)
	return backtestresult.BuildArtifacts(prepared.identity.SpecContentHash, summary, standardPath, backtestresult.DefaultPathBlockSize)
}

func (s *Service) GetStandardResult(ctx context.Context, userID uint, resultID uint) (*StandardResultDescriptor, error) {
	runIDs, err := s.authorizedRunIDs(ctx, userID, resultID)
	if err != nil {
		return nil, err
	}
	if _, err := s.results.VerifyMetadata(ctx, resultID); err != nil {
		return nil, err
	}
	loaded, err := s.results.Load(ctx, resultID, false)
	if err != nil {
		return nil, err
	}
	identity, err := backtestresult.DecodeIdentity([]byte(loaded.Spec.Snapshot))
	if err != nil {
		return nil, err
	}
	var reportModels []saasstore.PerformanceReport
	if err := s.db.WithContext(ctx).Where("backtest_result_id = ?", resultID).Order("created_at DESC, id DESC").Find(&reportModels).Error; err != nil {
		return nil, err
	}
	reportReferences := make([]PerformanceReportReference, 0, len(reportModels))
	for _, report := range reportModels {
		reportReferences = append(reportReferences, PerformanceReportReference{
			ID: report.ID, Status: report.Status, AnalysisVersion: report.AnalysisVersion,
			SchemaVersion: report.SchemaVersion, SettingsHash: report.SettingsHash,
			Settings: append(json.RawMessage(nil), report.Settings...), ContentHash: report.ContentHash,
			CreatedAt: report.CreatedAt.Format(time.RFC3339),
		})
	}
	var klineRows []struct {
		StudyID      uint
		StudyName    string
		PathID       uint
		EvaluationID uint
		OutcomeState string
		Permanent    bool
	}
	if err := s.db.WithContext(ctx).Table("kline_inverse_evaluations").
		Select("kline_inverse_studies.id AS study_id, kline_inverse_studies.name AS study_name, kline_inverse_evaluations.path_id, kline_inverse_evaluations.id AS evaluation_id, kline_inverse_evaluations.outcome_state, kline_inverse_evaluations.permanent").
		Joins("JOIN kline_inverse_studies ON kline_inverse_studies.id = kline_inverse_evaluations.study_id").
		Where("kline_inverse_studies.owner_user_id = ? AND kline_inverse_evaluations.backtest_result_id = ?", userID, resultID).
		Order("kline_inverse_evaluations.id ASC").Scan(&klineRows).Error; err != nil {
		return nil, err
	}
	klineReferences := make([]KlineInverseReference, 0, len(klineRows))
	for _, row := range klineRows {
		klineReferences = append(klineReferences, KlineInverseReference{StudyID: row.StudyID, StudyName: row.StudyName, PathID: row.PathID, EvaluationID: row.EvaluationID, OutcomeState: row.OutcomeState, Permanent: row.Permanent, BackLink: fmt.Sprintf("/kline-inverse?study=%d", row.StudyID)})
	}
	descriptor := &StandardResultDescriptor{
		ID:                 loaded.Result.ID,
		Status:             loaded.Result.Status,
		BacktestKey:        loaded.Result.BacktestKey,
		ResultVersion:      loaded.Result.ResultVersion,
		ResultContentHash:  loaded.Result.ContentHash,
		Spec:               identity.Snapshot,
		Summary:            loaded.Summary,
		Manifest:           loaded.Manifest,
		BacktestRunIDs:     runIDs,
		PerformanceReports: reportReferences,
		KlineInverseRefs:   klineReferences,
		CreatedAt:          loaded.Result.CreatedAt.Format(time.RFC3339),
	}
	if loaded.Result.CompletedAt != nil {
		descriptor.CompletedAt = loaded.Result.CompletedAt.Format(time.RFC3339)
	}
	return descriptor, nil
}

func (s *Service) GetStandardPathBlock(ctx context.Context, userID uint, resultID uint, blockIndex int) (*StandardPathBlockResponse, error) {
	if blockIndex < 0 {
		return nil, fmt.Errorf("block index cannot be negative")
	}
	if _, err := s.authorizedRunIDs(ctx, userID, resultID); err != nil {
		return nil, err
	}
	block, model, err := s.results.LoadBlock(ctx, resultID, blockIndex)
	if err != nil {
		if errors.Is(err, backtestresult.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &StandardPathBlockResponse{
		ResultID:    resultID,
		BlockIndex:  blockIndex,
		ContentHash: model.ContentHash,
		Block:       block,
	}, nil
}

func (s *Service) VerifyStandardResult(ctx context.Context, userID uint, resultID uint) (backtestresult.IntegrityReport, error) {
	if _, err := s.authorizedRunIDs(ctx, userID, resultID); err != nil {
		return backtestresult.IntegrityReport{}, err
	}
	return s.results.VerifyResult(ctx, resultID)
}

// EnsureNoCashFlowResult returns the same immutable backtest input with only
// monthly cash contributions set to zero. It reuses the regular P02 runner and
// P03 persistence path; analysis code never simulates NAV itself.
func (s *Service) EnsureNoCashFlowResult(ctx context.Context, userID uint, sourceResultID uint) (backtestresult.LoadedResult, error) {
	if _, err := s.authorizedRunIDs(ctx, userID, sourceResultID); err != nil {
		return backtestresult.LoadedResult{}, err
	}
	if _, err := s.results.VerifyResult(ctx, sourceResultID); err != nil {
		return backtestresult.LoadedResult{}, err
	}
	source, err := s.results.Load(ctx, sourceResultID, true)
	if err != nil {
		return backtestresult.LoadedResult{}, err
	}
	identity, err := backtestresult.DecodeIdentity([]byte(source.Spec.Snapshot))
	if err != nil {
		return backtestresult.LoadedResult{}, err
	}
	if identity.Snapshot.MonthlyContribution == 0 {
		return source, nil
	}
	if identity.Snapshot.Runner != backtestcore.RunnerSigmoidDCA || identity.Snapshot.StrategyID != sigmoiddca.StrategyID {
		return backtestresult.LoadedResult{}, fmt.Errorf("目前無法重建此 runner 的無現金流版本: %s", identity.Snapshot.Runner)
	}
	if identity.Snapshot.StrategyVersion != sigmoiddca.StrategyVersion || identity.Snapshot.CoreVersion != backtestcore.CoreVersion {
		return backtestresult.LoadedResult{}, fmt.Errorf("無現金流重跑需要相容的策略與回測核心版本")
	}
	if identity.Snapshot.ModelArtifactHash != "" || identity.Snapshot.MaterializedPredictionHash != "" || identity.Snapshot.DynamicPolicyHash != "" {
		return backtestresult.LoadedResult{}, fmt.Errorf("動態參數結果需要由原研究模組提供可重放的無現金流參數序列")
	}

	var params sigmoiddca.Params
	if err := json.Unmarshal(identity.Snapshot.Parameters, &params); err != nil {
		return backtestresult.LoadedResult{}, fmt.Errorf("解碼凍結參數失敗: %w", err)
	}
	params.Spawn.Policy.MonthlyInjectUSDT = 0
	params.PositionStructure = sigmoiddca.NormalizePositionStructure(params.PositionStructure)
	if err := normalizeSpawnPoint(&params.Spawn); err != nil {
		return backtestresult.LoadedResult{}, err
	}

	bars, err := s.loadBarsForSnapshot(ctx, identity.Snapshot)
	if err != nil {
		return backtestresult.LoadedResult{}, err
	}
	datasetHash, err := backtestresult.HashDataset(identity.Snapshot.DatasetVersion, bars)
	if err != nil {
		return backtestresult.LoadedResult{}, err
	}
	if datasetHash != identity.Snapshot.DatasetHash {
		return backtestresult.LoadedResult{}, fmt.Errorf("無現金流重跑資料集內容與原結果不一致")
	}
	coreSpec := coreSpecFromSnapshot(identity.Snapshot)
	coreSpec.MonthlyContribution = 0
	noCashFlowIdentity, err := backtestresult.BuildIdentity(backtestresult.SpecInput{
		StrategyID:                 identity.Snapshot.StrategyID,
		StrategyVersion:            identity.Snapshot.StrategyVersion,
		ParameterSchemaVersion:     identity.Snapshot.ParameterSchemaVersion,
		Parameters:                 params,
		CoreSpec:                   coreSpec,
		DatasetVersion:             identity.Snapshot.DatasetVersion,
		DatasetHash:                identity.Snapshot.DatasetHash,
		LongTermFilterVersion:      identity.Snapshot.LongTermFilterVersion,
		LongTermFilterSettings:     identity.Snapshot.LongTermFilterSettings,
		ModelArtifactHash:          identity.Snapshot.ModelArtifactHash,
		PredictionSchemaHash:       identity.Snapshot.PredictionSchemaHash,
		MaterializedPredictionHash: identity.Snapshot.MaterializedPredictionHash,
		DynamicPolicyHash:          identity.Snapshot.DynamicPolicyHash,
		DynamicControlMode:         identity.Snapshot.DynamicControlMode,
		EffectiveParametersHash:    identity.Snapshot.EffectiveParametersHash,
	}, bars)
	if err != nil {
		return backtestresult.LoadedResult{}, err
	}
	reservation, err := s.results.Reserve(ctx, noCashFlowIdentity)
	if err != nil {
		return backtestresult.LoadedResult{}, err
	}
	if reservation.Reusable {
		return s.results.Load(ctx, reservation.Result.ID, true)
	}
	if !reservation.Created {
		return backtestresult.LoadedResult{}, ErrResultInProgress
	}
	if err := s.results.MarkRunning(ctx, reservation.Result.ID); err != nil {
		return backtestresult.LoadedResult{}, err
	}
	prepared := preparedBacktest{
		req: CreateRequest{
			StrategyID: identity.Snapshot.StrategyID, InstrumentID: identity.Snapshot.InstrumentID,
			DataSource: identity.Snapshot.DataSource, ExecutionMode: identity.Snapshot.ExecutionMode,
			Symbol: identity.Snapshot.Symbol, Interval: identity.Snapshot.Interval,
		},
		params: params, bars: bars,
		costs: coreSpec.Costs, coreSpec: coreSpec, identity: noCashFlowIdentity,
	}
	artifacts, err := s.executePrepared(ctx, prepared)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			_ = s.results.Cancel(context.Background(), reservation.Result.ID, err.Error())
		} else {
			_ = s.results.Fail(ctx, reservation.Result.ID, err)
		}
		return backtestresult.LoadedResult{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = s.results.Cancel(context.Background(), reservation.Result.ID, err.Error())
		return backtestresult.LoadedResult{}, err
	}
	if err := s.results.Complete(ctx, reservation.Result.ID, artifacts); err != nil {
		_ = s.results.Fail(ctx, reservation.Result.ID, err)
		return backtestresult.LoadedResult{}, err
	}
	if _, err := s.results.VerifyResult(ctx, reservation.Result.ID); err != nil {
		_ = s.results.Invalidate(ctx, reservation.Result.ID, err.Error())
		return backtestresult.LoadedResult{}, err
	}
	return s.results.Load(ctx, reservation.Result.ID, true)
}

func (s *Service) loadBarsForSnapshot(ctx context.Context, snapshot backtestresult.SpecSnapshot) ([]quant.Bar, error) {
	var rows []saasstore.KLine
	err := s.db.WithContext(ctx).
		Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ? AND open_time >= ? AND open_time <= ?",
			snapshot.InstrumentID, snapshot.DataSource, snapshot.Symbol, snapshot.Interval, snapshot.StartTimeMs, snapshot.EndTimeMs).
		Order("open_time ASC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("找不到原回測資料集，無法建立無現金流版本")
	}
	bars := make([]quant.Bar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, quant.Bar{OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	return bars, nil
}

func coreSpecFromSnapshot(snapshot backtestresult.SpecSnapshot) backtestcore.Spec {
	filter := backtestcore.LongTermFilterConfig{}
	if len(snapshot.LongTermFilterSettings) > 0 {
		_ = json.Unmarshal(snapshot.LongTermFilterSettings, &filter)
	}
	return backtestcore.Spec{
		Runner: snapshot.Runner, InstrumentID: snapshot.InstrumentID, Symbol: snapshot.Symbol,
		DataSource: snapshot.DataSource, Interval: snapshot.Interval, ExecutionMode: snapshot.ExecutionMode,
		PositionStructure: snapshot.PositionStructure, StartTimeMs: snapshot.StartTimeMs, EndTimeMs: snapshot.EndTimeMs,
		EvaluationStartMs: snapshot.EvaluationStartMs, EvaluationEndMs: snapshot.EvaluationEndMs,
		PrefixMode: snapshot.PrefixMode, InitialCapital: snapshot.InitialCapital,
		MonthlyContribution: snapshot.MonthlyContribution, InitialAssetQuantity: snapshot.InitialAssetQuantity,
		MinimumTradeUSD: snapshot.MinimumTradeUSD, MinimumAssetQuantity: snapshot.MinimumAssetQuantity,
		Costs:          quant.ExecutionCostConfig{FeeRate: snapshot.FeeRate, SpreadRate: snapshot.SlippageRate},
		LongTermFilter: filter,
		DatasetHash:    snapshot.DatasetHash, CoreVersion: snapshot.CoreVersion,
	}
}

func responseFromStored(run saasstore.BacktestRun, loaded backtestresult.LoadedResult) (*Response, error) {
	if loaded.Summary == nil {
		return nil, fmt.Errorf("standardized result %d has no summary", loaded.Result.ID)
	}
	identity, err := backtestresult.DecodeIdentity([]byte(loaded.Spec.Snapshot))
	if err != nil {
		return nil, err
	}
	metrics := storedResponseMetrics{}
	if len(loaded.Summary.Extra) > 0 {
		if err := json.Unmarshal(loaded.Summary.Extra, &metrics); err != nil {
			return nil, fmt.Errorf("decode stored response metrics: %w", err)
		}
	}
	if metrics.Windows == nil {
		metrics.Windows = map[string]float64{}
	}
	if metrics.PracticalFinalEquity == 0 && loaded.Summary.FinalEquity != 0 {
		metrics.PracticalFinalEquity = loaded.Summary.FinalEquity
		metrics.PracticalTotalReturn = loaded.Summary.ROI
		metrics.PracticalMaxDrawdown = loaded.Summary.MaxDrawdown
		metrics.PracticalTradeCount = loaded.Summary.TradeCount
	}
	status := run.Status
	if status == saasstore.BacktestStatusRunning {
		status = saasstore.BacktestStatusCompleted
	}
	response := &Response{
		ID:                    run.ID,
		Status:                status,
		BacktestResultID:      loaded.Result.ID,
		BacktestKey:           loaded.Result.BacktestKey,
		ResultVersion:         loaded.Result.ResultVersion,
		ResultContentHash:     loaded.Result.ContentHash,
		ResultStatus:          loaded.Result.Status,
		ReusedResult:          run.ReusedResult,
		StrategyID:            identity.Snapshot.StrategyID,
		Symbol:                identity.Snapshot.Symbol,
		InstrumentID:          identity.Snapshot.InstrumentID,
		DataSource:            identity.Snapshot.DataSource,
		ExecutionMode:         identity.Snapshot.ExecutionMode,
		Interval:              identity.Snapshot.Interval,
		Source:                run.Source,
		TotalReturn:           metrics.PracticalTotalReturn,
		Alpha:                 metrics.Alpha,
		MaxDrawdown:           loaded.Summary.MaxDrawdown,
		FinalEquity:           loaded.Summary.FinalEquity,
		Benchmark:             metrics.Benchmark,
		BenchmarkReturn:       metrics.BenchmarkReturn,
		BenchmarkMaxDrawdown:  metrics.BenchmarkMaxDrawdown,
		BenchmarkFinalEquity:  metrics.BenchmarkFinalEquity,
		FeeRate:               metrics.FeeRate,
		SpreadRate:            metrics.SpreadRate,
		FeeCost:               metrics.FeeCost,
		SlippageCost:          metrics.SlippageCost,
		TotalExecutionCost:    metrics.TotalExecutionCost,
		RebalanceThreshold:    metrics.RebalanceThreshold,
		ForceFullThreshold:    metrics.ForceFullThreshold,
		ForceEmptyThreshold:   metrics.ForceEmptyThreshold,
		PositionStructure:     metrics.PositionStructure,
		TradeCount:            loaded.Summary.TradeCount,
		LongTermFilterEnabled: metrics.LongTermFilterEnabled,
		LongTermFilterMonths:  metrics.LongTermFilterMonths,
		LongTermFilterVersion: metrics.LongTermFilterVersion,
		PracticalTotalReturn:  metrics.PracticalTotalReturn,
		PracticalMaxDrawdown:  metrics.PracticalMaxDrawdown,
		PracticalFinalEquity:  metrics.PracticalFinalEquity,
		PracticalTradeCount:   metrics.PracticalTradeCount,
		WMean:                 metrics.WMean,
		WMomentum:             metrics.WMomentum,
		WBreakout:             metrics.WBreakout,
		NAV:                   equitySnapshotsFromStandardPath(loaded.Path()),
		Windows:               metrics.Windows,
		WindowDetails:         metrics.WindowDetails,
		Error:                 run.ErrorMessage,
		CreatedAt:             run.CreatedAt.Format(time.RFC3339),
	}
	if run.FinishedAt != nil {
		response.FinishedAt = run.FinishedAt.Format(time.RFC3339)
	}
	return response, nil
}

func pendingResponse(run saasstore.BacktestRun, result saasstore.BacktestResult) *Response {
	var req CreateRequest
	_ = json.Unmarshal([]byte(run.Request), &req)
	filter := longTermFilterConfig(req)
	return &Response{
		ID:                    run.ID,
		Status:                saasstore.BacktestStatusRunning,
		BacktestResultID:      result.ID,
		BacktestKey:           result.BacktestKey,
		ResultVersion:         result.ResultVersion,
		ResultContentHash:     result.ContentHash,
		ResultStatus:          result.Status,
		ReusedResult:          run.ReusedResult,
		StrategyID:            run.StrategyID,
		Symbol:                run.Symbol,
		InstrumentID:          run.InstrumentID,
		DataSource:            run.DataSource,
		ExecutionMode:         run.ExecutionMode,
		Interval:              run.Interval,
		Source:                run.Source,
		LongTermFilterEnabled: filter.Enabled,
		LongTermFilterMonths:  filter.Months,
		LongTermFilterVersion: filter.Version,
		NAV:                   []EquitySnapshot{},
		Windows:               map[string]float64{},
		CreatedAt:             run.CreatedAt.Format(time.RFC3339),
	}
}

func standardizedPath(strategy []backtestcore.NAVPoint, baseline quant.GhostDCAResult) []backtestresult.PathPoint {
	type benchmarkPoint struct {
		equity      float64
		dailyReturn float64
	}
	byTime := make(map[int64]benchmarkPoint, len(baseline.Times))
	previous := 0.0
	for index, timestamp := range baseline.Times {
		if index >= len(baseline.NAV) {
			continue
		}
		equity := baseline.NAV[index]
		byTime[timestamp] = benchmarkPoint{equity: equity, dailyReturn: pctChange(equity, previous)}
		previous = equity
	}
	points := make([]backtestresult.PathPoint, 0, len(strategy))
	for _, point := range strategy {
		stored := backtestresult.PathPoint{NAVPoint: point}
		if benchmark, ok := byTime[point.TimeMs]; ok {
			equity := benchmark.equity
			dailyReturn := benchmark.dailyReturn
			stored.BenchmarkEquity = &equity
			stored.BenchmarkDailyReturn = &dailyReturn
		}
		points = append(points, stored)
	}
	return points
}

func equitySnapshotsFromStandardPath(path []backtestresult.PathPoint) []EquitySnapshot {
	points := make([]EquitySnapshot, 0, len(path))
	previousStrategy := 0.0
	previousBenchmark := 0.0
	for _, item := range path {
		benchmark := 0.0
		if item.BenchmarkEquity != nil {
			benchmark = *item.BenchmarkEquity
		}
		practicalTotalEquity := item.PracticalTotalEquity
		practicalCash := item.PracticalCash
		practicalAssetQuantity := item.PracticalAssetQuantity
		practicalActualExposure := item.PracticalActualExposureWeight
		practicalDailyReturn := item.PracticalDailyReturn
		if practicalTotalEquity == 0 && item.TotalEquity != 0 {
			practicalTotalEquity = item.TotalEquity
			practicalCash = item.Cash
			practicalAssetQuantity = item.AssetQuantity
			practicalActualExposure = item.ActualExposureWeight
			practicalDailyReturn = item.DailyReturn
		}
		points = append(points, EquitySnapshot{
			Time:                             time.UnixMilli(item.TimeMs).UTC().Format(time.RFC3339),
			Price:                            item.Price,
			TotalAssets:                      item.TotalEquity,
			Benchmark:                        benchmark,
			StrategyChangePct:                pctChange(item.TotalEquity, previousStrategy),
			BenchmarkChangePct:               pctChange(benchmark, previousBenchmark),
			PracticalTargetWeight:            item.PracticalTargetWeight,
			PracticalTargetWeightChange:      item.PracticalTargetWeightChange,
			ModelTargetWeight:                item.ModelTargetWeight,
			ModelTargetWeightChange:          item.ModelTargetWeightChange,
			EmptyReferenceTargetWeight:       item.EmptyReferenceTargetWeight,
			EmptyReferenceTargetWeightChange: item.EmptyReferenceTargetWeightChange,
			Cash:                             item.Cash,
			AssetQuantity:                    item.AssetQuantity,
			ActualExposureWeight:             item.ActualExposureWeight,
			IntradayExposureWeight:           item.IntradayExposureWeight,
			DailyReturn:                      item.DailyReturn,
			PracticalTotalAssets:             practicalTotalEquity,
			PracticalCash:                    practicalCash,
			PracticalAssetQuantity:           practicalAssetQuantity,
			PracticalActualExposureWeight:    practicalActualExposure,
			PracticalDailyReturn:             practicalDailyReturn,
			LongTermFilterEnabled:            item.LongTermFilterEnabled,
			LongTermFilterReady:              item.LongTermFilterReady,
			LongTermFilterRiskOff:            item.LongTermFilterRiskOff,
			LongTermFilterCurrentSMA:         item.LongTermFilterCurrentSMA,
			LongTermFilterPreviousSMA:        item.LongTermFilterPreviousSMA,
			LongTermFilterSignal:             item.LongTermFilterSignal,
			LongTermFilterEvent:              item.LongTermFilterEvent,
		})
		previousStrategy = item.TotalEquity
		if item.BenchmarkEquity != nil {
			previousBenchmark = benchmark
		}
	}
	return points
}

func (s *Service) finishRunFailure(ctx context.Context, runID uint, status string, message string) {
	finished := time.Now().UTC()
	_ = s.db.WithContext(ctx).Model(&saasstore.BacktestRun{}).Where("id = ?", runID).Updates(map[string]any{
		"status":        status,
		"error_message": message,
		"finished_at":   &finished,
	}).Error
}

func (s *Service) finishOwnedResultFailure(ctx context.Context, resultID uint, cause error) {
	operationContext := ctx
	if ctx.Err() != nil {
		operationContext = context.Background()
	}
	status := saasstore.BacktestStatusFailed
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status = saasstore.BacktestStatusCancelled
		_ = s.results.Cancel(operationContext, resultID, cause.Error())
	} else {
		_ = s.results.Fail(operationContext, resultID, cause)
	}
	s.finishLinkedRunsFailure(operationContext, resultID, status, cause.Error())
}

func (s *Service) finishLinkedRunsFailure(ctx context.Context, resultID uint, status string, message string) {
	finished := time.Now().UTC()
	_ = s.db.WithContext(ctx).Model(&saasstore.BacktestRun{}).
		Where("backtest_result_id = ? AND status = ?", resultID, saasstore.BacktestStatusRunning).
		Updates(map[string]any{
			"status":        status,
			"error_message": message,
			"finished_at":   &finished,
		}).Error
}

func (s *Service) finishLinkedRunsCompleted(ctx context.Context, resultID uint, finished time.Time) error {
	return s.db.WithContext(ctx).Model(&saasstore.BacktestRun{}).
		Where("backtest_result_id = ? AND status = ?", resultID, saasstore.BacktestStatusRunning).
		Updates(map[string]any{
			"status":        saasstore.BacktestStatusCompleted,
			"result":        saasstore.JSONB("{}"),
			"error_message": "",
			"finished_at":   &finished,
		}).Error
}

func (s *Service) authorizedRunIDs(ctx context.Context, userID uint, resultID uint) ([]uint, error) {
	var ids []uint
	if err := s.db.WithContext(ctx).Model(&saasstore.BacktestRun{}).
		Where("user_id = ? AND backtest_result_id = ?", userID, resultID).
		Order("id ASC").Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		var count int64
		if err := s.db.WithContext(ctx).Model(&saasstore.KlineInverseEvaluation{}).
			Joins("JOIN kline_inverse_studies ON kline_inverse_studies.id = kline_inverse_evaluations.study_id").
			Where("kline_inverse_studies.owner_user_id = ? AND kline_inverse_evaluations.backtest_result_id = ? AND kline_inverse_evaluations.permanent = ?", userID, resultID, true).
			Count(&count).Error; err != nil {
			return nil, err
		}
		if count == 0 {
			return nil, ErrNotFound
		}
	}
	return ids, nil
}

func (s *Service) resolveParams(ctx context.Context, userID uint, req CreateRequest) (sigmoiddca.Params, error) {
	params := sigmoiddca.DefaultParams()
	params.Chromosome = quant.DefaultSeedChromosome

	switch req.Source {
	case SourceChampion:
		record, err := s.loadLatestGene(ctx, req, saasstore.GeneRoleChampion)
		if err == nil {
			params = sigmoiddca.ParseParamsFromParamPack([]byte(record.ParamPack))
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return params, err
		}
	case SourceCandidate:
		id := req.CandidateID
		if id == 0 {
			id = req.GenomeID
		}
		if id == 0 {
			return params, errors.New("候選參數回測需要指定基因 ID")
		}
		record, err := s.loadGeneByID(ctx, id)
		if err != nil {
			return params, err
		}
		params = sigmoiddca.ParseParamsFromParamPack([]byte(record.ParamPack))
	case SourceCustom:
		parsed, err := parseCustomParams(req.CustomParams)
		if err != nil {
			return params, err
		}
		params = parsed
	default:
		return params, fmt.Errorf("不支援的回測來源: %s", req.Source)
	}

	if req.InstanceID != 0 {
		instance, err := s.loadInstance(ctx, userID, req.InstanceID)
		if err != nil {
			return params, err
		}
		params.Spawn = overlayInstanceSpawn(params.Spawn, instance)
	}
	if req.SpawnPoint != nil {
		params.Spawn = *req.SpawnPoint
	}
	if req.InitialCapital != nil {
		params.Spawn.Policy.InitialUSDT = *req.InitialCapital
	}
	if req.MonthlyDCA != nil {
		params.Spawn.Policy.MonthlyInjectUSDT = *req.MonthlyDCA
	}
	params.Chromosome = quant.ClampChromosome(params.Chromosome)
	if err := quant.ValidateChromosome(params.Chromosome); err != nil {
		return params, err
	}
	return params, nil
}

func (s *Service) resolveMarketRegionProvider(ctx context.Context, req CreateRequest, bars []quant.Bar, cache *ga.MarketRegionFeatureCache) (backtestcore.ParameterProvider, []byte, bool, error) {
	var raw []byte
	switch req.Source {
	case SourceChampion:
		record, err := s.loadLatestGene(ctx, req, saasstore.GeneRoleChampion)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, false, nil
		}
		if err != nil {
			return nil, nil, false, err
		}
		raw = []byte(record.ParamPack)
	case SourceCandidate:
		id := req.CandidateID
		if id == 0 {
			id = req.GenomeID
		}
		if id == 0 {
			return nil, nil, false, nil
		}
		record, err := s.loadGeneByID(ctx, id)
		if err != nil {
			return nil, nil, false, err
		}
		raw = []byte(record.ParamPack)
	case SourceCustom:
		raw = []byte(req.CustomParams)
	default:
		return nil, nil, false, nil
	}
	provider, handled, err := ga.MarketRegionParameterProviderWithCache(raw, bars, cache)
	return provider, raw, handled, err
}

func (s *Service) loadLatestGene(ctx context.Context, req CreateRequest, role string) (saasstore.GeneRecord, error) {
	var record saasstore.GeneRecord
	err := s.db.WithContext(ctx).
		Where("strategy_id = ? AND instrument_id = ? AND data_source = ? AND interval = ? AND execution_mode = ? AND role = ?",
			req.StrategyID, req.InstrumentID, req.DataSource, req.Interval, req.ExecutionMode, role).
		Order("activated_at DESC NULLS LAST, created_at DESC").
		First(&record).Error
	return record, err
}

func (s *Service) loadGeneByID(ctx context.Context, id uint) (saasstore.GeneRecord, error) {
	var record saasstore.GeneRecord
	err := s.db.WithContext(ctx).
		Where("id = ? AND role IN ?", id, []string{saasstore.GeneRoleChallenger, saasstore.GeneRoleChampion, saasstore.GeneRoleRetired}).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return record, fmt.Errorf("找不到可回測的候選參數")
	}
	return record, err
}

func (s *Service) loadInstance(ctx context.Context, userID uint, id uint) (saasstore.StrategyInstance, error) {
	var instance saasstore.StrategyInstance
	err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&instance).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return instance, fmt.Errorf("找不到策略實例")
	}
	return instance, err
}

func (s *Service) loadBars(ctx context.Context, req CreateRequest) ([]quant.Bar, error) {
	if req.MarketDataVersionID != 0 {
		var rows []saasstore.MarketDataVersionBar
		query := s.db.WithContext(ctx).Where("version_id = ?", req.MarketDataVersionID)
		if req.StartTimeMs > 0 {
			query = query.Where("open_time >= ?", req.StartTimeMs)
		}
		if req.EndTimeMs > 0 {
			query = query.Where("open_time <= ?", req.EndTimeMs)
		}
		if err := query.Order("ordinal ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		bars := make([]quant.Bar, 0, len(rows))
		for _, row := range rows {
			bars = append(bars, quant.Bar{OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
		}
		return bars, nil
	}
	var rows []saasstore.KLine
	query := s.db.WithContext(ctx).
		Where("symbol = ? AND interval = ? AND instrument_id = ? AND source = ?", req.Symbol, req.Interval, req.InstrumentID, req.DataSource)
	if req.StartTimeMs > 0 {
		query = query.Where("open_time >= ?", req.StartTimeMs)
	}
	if req.EndTimeMs > 0 {
		query = query.Where("open_time <= ?", req.EndTimeMs)
	}
	if err := query.Order("open_time ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	bars := make([]quant.Bar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, quant.Bar{
			OpenTime: row.OpenTime,
			Open:     row.Open,
			High:     row.High,
			Low:      row.Low,
			Close:    row.Close,
			Volume:   row.Volume,
		})
	}
	return bars, nil
}

func (s *Service) normalizeRequest(ctx context.Context, req CreateRequest) CreateRequest {
	if req.StrategyID == "" {
		req.StrategyID = sigmoiddca.StrategyID
	}
	if req.Symbol == "" {
		req.Symbol = req.Pair
	}
	instrument, err := s.instruments.ResolveInstrument(ctx, req.InstrumentID, req.Symbol, req.DataSource)
	if err == nil {
		req.InstrumentID = instrument.ID
		req.Symbol = instrument.Symbol
		req.DataSource = instrument.DataSource
	}
	if req.Symbol == "" {
		req.Symbol = marketdata.DefaultSymbol
		req.InstrumentID = marketdata.InstrumentBTCUSDT
		req.DataSource = marketdata.DataSourceBinance
	}
	req.Symbol = strings.ToUpper(strings.TrimSpace(req.Symbol))
	if req.Interval == "" {
		req.Interval = "1d"
	}
	if strings.TrimSpace(req.ExecutionMode) == "" {
		req.ExecutionMode = marketdata.ExecutionModeCloseNextOpen
	} else {
		req.ExecutionMode = marketdata.NormalizeExecutionMode(req.ExecutionMode)
	}
	if req.Source == "" {
		req.Source = SourceChampion
	}
	req.Source = strings.ToLower(strings.TrimSpace(req.Source))
	if req.Source == SourceBaseline {
		disabled := false
		req.LongTermFilterEnabled = &disabled
		req.LongTermFilterMonths = 0
	}
	if req.LongTermFilterEnabled == nil {
		enabled := true
		req.LongTermFilterEnabled = &enabled
	}
	if req.LongTermFilterMonths == 0 {
		req.LongTermFilterMonths = backtestcore.DefaultLongTermFilterMonths
	}
	return req
}

func (s *Service) validateBasicRequest(ctx context.Context, req CreateRequest) error {
	instrument, err := s.instruments.ResolveInstrument(ctx, req.InstrumentID, req.Symbol, req.DataSource)
	if err != nil {
		return err
	}
	if req.DataSource != instrument.DataSource {
		return fmt.Errorf("unsupported data source: %s", req.DataSource)
	}
	if !supportsInterval(instrument.SupportedIntervals, req.Interval) {
		return fmt.Errorf("unsupported interval for %s: %s", instrument.ID, req.Interval)
	}
	if req.MarketDataVersionID != 0 {
		var count int64
		err := s.db.WithContext(ctx).Model(&saasstore.MarketDataVersion{}).Where(
			"id = ? AND output_instrument_id = ? AND interval = ? AND content_hash = ? AND status = ? AND integrity_status = ? AND published = true",
			req.MarketDataVersionID, instrument.ID, req.Interval, strings.TrimSpace(req.MarketDataContentHash),
			marketversion.VersionStatusCompleted, marketversion.IntegrityValid,
		).Count(&count).Error
		if err != nil {
			return err
		}
		if count != 1 || strings.TrimSpace(req.MarketDataContentHash) == "" {
			return errors.New("行情版本不存在、未發布或內容雜湊不符")
		}
	}
	if !marketdata.IsSupportedExecutionMode(req.ExecutionMode) {
		return fmt.Errorf("unsupported execution mode: %s", req.ExecutionMode)
	}
	if req.ExecutionMode == marketdata.ExecutionModePreclose10m {
		return errors.New("收盤前 10 分鐘模式需要歷史快照回測路徑，目前尚未開放，不能用日 K 假裝回測")
	}
	if req.LongTermFilterEnabled != nil && *req.LongTermFilterEnabled {
		if req.Interval != "1d" {
			return errors.New("長週期風險濾網只支援日 K，請改用日 K 或關閉濾網")
		}
		if req.LongTermFilterMonths <= 0 {
			return errors.New("N 月線長度必須大於 0")
		}
	}
	if req.StartTimeMs > 0 && req.EndTimeMs > 0 && req.StartTimeMs > req.EndTimeMs {
		return errors.New("start_time_ms must be earlier than end_time_ms")
	}
	if err := validateCostRate("fee_rate", req.FeeRate); err != nil {
		return err
	}
	if err := validateCostRate("spread_rate", req.SpreadRate); err != nil {
		return err
	}
	if req.StrategyID != sigmoiddca.StrategyID {
		return fmt.Errorf("尚不支援的策略: %s", req.StrategyID)
	}
	switch req.Source {
	case SourceChampion, SourceCandidate, SourceCustom, SourceBaseline:
	default:
		return fmt.Errorf("不支援的回測來源: %s", req.Source)
	}
	return nil
}

func (s *Service) validateMarketDataVersionOwner(ctx context.Context, userID uint, req CreateRequest) error {
	if req.MarketDataVersionID == 0 {
		return nil
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&saasstore.MarketDataVersion{}).
		Where("id = ? AND owner_user_id = ?", req.MarketDataVersionID, userID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return errors.New("無權使用這份行情版本")
	}
	return nil
}

func validateCostRate(name string, value *float64) error {
	if value == nil {
		return nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
		return fmt.Errorf("%s must be zero or positive", name)
	}
	if *value > 0.2 {
		return fmt.Errorf("%s is too large", name)
	}
	return nil
}

func supportsInterval(supported []string, interval string) bool {
	for _, item := range supported {
		if item == interval {
			return true
		}
	}
	return false
}

func parseCustomParams(raw json.RawMessage) (sigmoiddca.Params, error) {
	params := sigmoiddca.DefaultParams()
	params.PositionStructure = sigmoiddca.PositionStructureFloatingOnly
	if len(raw) == 0 || string(raw) == "null" {
		return params, errors.New("自訂參數不可為空")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return params, fmt.Errorf("自訂參數 JSON 格式不正確")
	}
	if _, ok := envelope["sigmoid_dca_config"]; ok {
		if err := json.Unmarshal(raw, &params); err != nil {
			return params, fmt.Errorf("自訂參數內容不正確")
		}
		params.Chromosome = quant.ClampChromosome(params.Chromosome)
		params.PositionStructure = sigmoiddca.NormalizePositionStructure(params.PositionStructure)
		if err := quant.ValidateChromosome(params.Chromosome); err != nil {
			return params, err
		}
		return params, nil
	}
	chromosome := quant.DefaultSeedChromosome
	if err := json.Unmarshal(raw, &chromosome); err != nil {
		return params, fmt.Errorf("自訂參數內容不正確")
	}
	params.Chromosome = quant.ClampChromosome(chromosome)
	if err := quant.ValidateChromosome(params.Chromosome); err != nil {
		return params, err
	}
	return params, nil
}

func overlayInstanceSpawn(spawn quant.SpawnPoint, instance saasstore.StrategyInstance) quant.SpawnPoint {
	var cfg instanceConfig
	if err := json.Unmarshal([]byte(instance.Config), &cfg); err != nil {
		return spawn
	}
	if cfg.InitialUSDT > 0 {
		spawn.Policy.InitialUSDT = cfg.InitialUSDT
	}
	if cfg.MonthlyInjectUSDT > 0 {
		spawn.Policy.MonthlyInjectUSDT = cfg.MonthlyInjectUSDT
	}
	if cfg.ColdSealedBTC > 0 {
		spawn.Policy.ColdSealedBTC = cfg.ColdSealedBTC
	}
	return spawn
}

func normalizeSpawnPoint(spawn *quant.SpawnPoint) error {
	if spawn.Policy.InitialUSDT <= 0 {
		spawn.Policy.InitialUSDT = 1000
	}
	if spawn.Policy.MonthlyInjectUSDT < 0 {
		return errors.New("月度投入不可為負數")
	}
	if spawn.Policy.ColdSealedBTC < 0 {
		return errors.New("封存資產不可為負數")
	}
	if spawn.Risk.MaxDrawdownPct <= 0 {
		spawn.Risk.MaxDrawdownPct = 0.88
	}
	if spawn.Risk.LotStep <= 0 {
		spawn.Risk.LotStep = 0.000001
	}
	if spawn.Risk.LotMin <= 0 {
		spawn.Risk.LotMin = 0.00001
	}
	return nil
}

func backtestCosts(req CreateRequest) quant.ExecutionCostConfig {
	costs := quant.ExecutionCostConfig{}
	if req.FeeRate != nil {
		costs.FeeRate = *req.FeeRate
	}
	if req.SpreadRate != nil {
		costs.SpreadRate = *req.SpreadRate
	}
	return quant.NormalizeExecutionCosts(costs)
}

func longTermFilterConfig(req CreateRequest) backtestcore.LongTermFilterConfig {
	enabled := req.LongTermFilterEnabled != nil && *req.LongTermFilterEnabled
	return backtestcore.LongTermFilterConfig{
		Enabled: enabled,
		Months:  req.LongTermFilterMonths,
		Version: backtestcore.LongTermFilterVersion,
	}
}

func practicalPath(points []backtestcore.NAVPoint) []backtestcore.NAVPoint {
	result := make([]backtestcore.NAVPoint, len(points))
	for index, point := range points {
		point.TotalEquity = point.PracticalTotalEquity
		point.Cash = point.PracticalCash
		point.AssetQuantity = point.PracticalAssetQuantity
		point.ActualExposureWeight = point.PracticalActualExposureWeight
		point.DailyReturn = point.PracticalDailyReturn
		point.Trades = point.PracticalTrades
		result[index] = point
	}
	return result
}

func scoreWindows(ctx context.Context, bars []quant.Bar, symbol string, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, costs quant.ExecutionCostConfig, positionStructure string, longTermFilter backtestcore.LongTermFilterConfig, marketRegionRaw []byte, marketRegionCache *ga.MarketRegionFeatureCache) (map[string]float64, []WindowResult, error) {
	windows := quant.BuildCrucibleWindows(bars, 1200)
	scores := make(map[string]float64, len(windows))
	type windowOutcome struct {
		detail WindowResult
		err    error
	}
	outcomes := make([]windowOutcome, len(windows))
	workers := min(runtime.NumCPU(), len(windows))
	if workers < 1 {
		workers = 1
	}
	tasks := make(chan int, len(windows))
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range tasks {
				outcomes[index] = scoreWindow(ctx, windows[index], symbol, interval, executionMode, chromosome, spawn, costs, positionStructure, longTermFilter, marketRegionRaw, marketRegionCache)
			}
		}()
	}
	for index := range windows {
		tasks <- index
	}
	close(tasks)
	group.Wait()
	details := make([]WindowResult, 0, len(windows))
	for index, window := range windows {
		outcome := outcomes[index]
		if outcome.err != nil {
			return nil, nil, outcome.err
		}
		scores[window.Label] = outcome.detail.Score
		details = append(details, outcome.detail)
	}
	return scores, details, nil
}

func scoreWindow(ctx context.Context, window quant.CrucibleWindow, symbol string, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, costs quant.ExecutionCostConfig, positionStructure string, longTermFilter backtestcore.LongTermFilterConfig, marketRegionRaw []byte, marketRegionCache *ga.MarketRegionFeatureCache) (outcome struct {
	detail WindowResult
	err    error
}) {
	if err := ctx.Err(); err != nil {
		outcome.err = err
		return outcome
	}
	params := sigmoiddca.DefaultParams()
	params.Chromosome = chromosome
	params.Spawn = *spawn
	params.PositionStructure = positionStructure
	var provider backtestcore.ParameterProvider
	if len(marketRegionRaw) > 0 {
		var handled bool
		provider, handled, outcome.err = ga.MarketRegionParameterProviderWithCache(marketRegionRaw, window.Bars, marketRegionCache)
		if outcome.err != nil || !handled {
			if outcome.err == nil {
				outcome.err = errors.New("market region parameter pack cannot be loaded")
			}
			return outcome
		}
	}
	path, err := backtestcore.RunSigmoidDCA(backtestcore.SigmoidDCARequest{
		Context: ctx,
		Spec: backtestcore.Spec{
			Symbol:               symbol,
			Interval:             interval,
			ExecutionMode:        executionMode,
			PositionStructure:    positionStructure,
			StartTimeMs:          window.Bars[0].OpenTime,
			EndTimeMs:            window.Bars[len(window.Bars)-1].OpenTime,
			EvaluationStartMs:    window.EvalStartMs,
			EvaluationEndMs:      window.Bars[len(window.Bars)-1].OpenTime,
			InitialCapital:       spawn.Policy.InitialUSDT,
			MonthlyContribution:  spawn.Policy.MonthlyInjectUSDT,
			InitialAssetQuantity: spawn.Policy.ColdSealedBTC,
			Costs:                costs,
			LongTermFilter:       longTermFilter,
		},
		Bars:              window.Bars,
		Params:            params,
		ParameterProvider: provider,
	})
	if err != nil {
		outcome.err = err
		return outcome
	}
	pathMaxDrawdown := maxDrawdown(path.Path)
	baseline := quant.SimulateGhostDCAFrom(window.Bars, window.EvalStartMs, quant.GhostDCAConfig{
		InitialUSDT:       spawn.Policy.InitialUSDT,
		MonthlyInjectUSDT: spawn.Policy.MonthlyInjectUSDT,
		UseOpenExecution:  executionMode == marketdata.ExecutionModeCloseNextOpen,
		Costs:             costs,
	})
	alpha := path.TotalReturn - baseline.ROI
	score := alpha - 1.5*math.Max(0, pathMaxDrawdown-baseline.MaxDrawdown)
	if pathMaxDrawdown >= 0.88 {
		score = ga.FatalFitnessScore
	}
	outcome.detail = WindowResult{
		Window:            window.Label,
		Score:             score,
		TotalReturn:       path.TotalReturn,
		BenchmarkReturn:   baseline.ROI,
		Alpha:             alpha,
		MaxDrawdown:       pathMaxDrawdown,
		BenchmarkDrawdown: baseline.MaxDrawdown,
	}
	return outcome
}

func mergeNAV(strategy []ga.BacktestPoint, baseline quant.GhostDCAResult) []EquitySnapshot {
	byTime := make(map[int64]float64, len(baseline.Times))
	for i, ts := range baseline.Times {
		if i < len(baseline.NAV) {
			byTime[ts] = baseline.NAV[i]
		}
	}
	points := make([]EquitySnapshot, 0, len(strategy))
	previousStrategy := 0.0
	previousBenchmark := 0.0
	for _, item := range strategy {
		benchmark, ok := byTime[item.TimeMs]
		if !ok {
			continue
		}
		strategyChange := pctChange(item.TotalEquity, previousStrategy)
		benchmarkChange := pctChange(benchmark, previousBenchmark)
		points = append(points, EquitySnapshot{
			Time:                             time.UnixMilli(item.TimeMs).UTC().Format(time.RFC3339),
			Price:                            item.Price,
			TotalAssets:                      item.TotalEquity,
			Benchmark:                        benchmark,
			StrategyChangePct:                strategyChange,
			BenchmarkChangePct:               benchmarkChange,
			PracticalTargetWeight:            item.PracticalTargetWeight,
			PracticalTargetWeightChange:      item.PracticalTargetWeightChange,
			ModelTargetWeight:                item.ModelTargetWeight,
			ModelTargetWeightChange:          item.ModelTargetWeightChange,
			EmptyReferenceTargetWeight:       item.EmptyReferenceTargetWeight,
			EmptyReferenceTargetWeightChange: item.EmptyReferenceTargetWeightChange,
			Cash:                             item.Cash,
			AssetQuantity:                    item.AssetQuantity,
			ActualExposureWeight:             item.ActualExposureWeight,
			IntradayExposureWeight:           item.IntradayExposureWeight,
			DailyReturn:                      item.DailyReturn,
		})
		previousStrategy = item.TotalEquity
		previousBenchmark = benchmark
	}
	return points
}

func maxDrawdown(points []backtestcore.NAVPoint) float64 {
	nav := make([]float64, 0, len(points))
	for _, point := range points {
		nav = append(nav, point.TotalEquity)
	}
	return quant.MaxDrawdown(nav)
}

func pctChange(current float64, previous float64) float64 {
	if previous <= 0 {
		return 0
	}
	return current/previous - 1
}
