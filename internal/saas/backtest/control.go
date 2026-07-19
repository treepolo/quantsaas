package backtest

import (
	"context"
	"fmt"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/quant"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/strategies/sigmoiddca"
)

const (
	ControlRuleStrategyVersion     = "p11-control-rule-v1"
	ControlExposureStrategyVersion = "p11-exposure-replay-v1"
	ControlParameterSchemaVersion  = "p11-control-input-v1"
)

type ExposureReplayIdentity struct {
	SchemaVersion     string `json:"schema_version"`
	SourceResultID    uint   `json:"source_result_id"`
	SourceVersion     string `json:"source_version"`
	SourceContentHash string `json:"source_content_hash"`
	ShuffleVersion    string `json:"shuffle_version"`
	ShuffleSeed       int64  `json:"shuffle_seed"`
	SequenceIndex     int    `json:"sequence_index"`
	PermutationHash   string `json:"permutation_hash"`
}

type controlPrepared struct {
	request  CreateRequest
	bars     []quant.Bar
	coreSpec backtestcore.Spec
	identity backtestresult.Identity
	run      func() (backtestcore.Result, error)
}

func (s *Service) prepareBaseline(ctx context.Context, req CreateRequest) (controlPrepared, error) {
	rule := backtestcore.RuleConfig{Type: backtestcore.RuleAlwaysExposed}
	prepared, err := s.prepareControl(ctx, req, backtestcore.RunnerRule, ControlRuleStrategyVersion, map[string]any{
		"schema_version": ControlParameterSchemaVersion,
		"analysis_mode":  "market_baseline",
		"rule":           rule,
	})
	if err != nil {
		return controlPrepared{}, err
	}
	prepared.run = func() (backtestcore.Result, error) {
		return backtestcore.RunRule(backtestcore.RuleRequest{Spec: prepared.coreSpec, Bars: prepared.bars, Rule: rule})
	}
	return prepared, nil
}

func (s *Service) EnsureRuleStandardResult(ctx context.Context, userID uint, req CreateRequest, rule backtestcore.RuleConfig) (StandardExecutionResult, error) {
	prepared, err := s.prepareControl(ctx, req, backtestcore.RunnerRule, ControlRuleStrategyVersion, map[string]any{"schema_version": ControlParameterSchemaVersion, "rule": rule})
	if err != nil {
		return StandardExecutionResult{}, err
	}
	prepared.run = func() (backtestcore.Result, error) {
		return backtestcore.RunRule(backtestcore.RuleRequest{Spec: prepared.coreSpec, Bars: prepared.bars, Rule: rule})
	}
	return s.ensureControlPrepared(ctx, userID, prepared)
}

func (s *Service) EnsureExposureReplayStandardResult(ctx context.Context, userID uint, req CreateRequest, targets []backtestcore.ExposureTarget, identity ExposureReplayIdentity) (StandardExecutionResult, error) {
	if identity.SchemaVersion == "" || identity.SourceResultID == 0 || identity.SourceContentHash == "" || identity.PermutationHash == "" || identity.SequenceIndex < 0 {
		return StandardExecutionResult{}, fmt.Errorf("曝險打散身分不完整")
	}
	prepared, err := s.prepareControl(ctx, req, backtestcore.RunnerExposureReplay, ControlExposureStrategyVersion, identity)
	if err != nil {
		return StandardExecutionResult{}, err
	}
	prepared.run = func() (backtestcore.Result, error) {
		return backtestcore.RunExposureReplay(backtestcore.ExposureReplayRequest{Spec: prepared.coreSpec, Bars: prepared.bars, Targets: targets})
	}
	return s.ensureControlPrepared(ctx, userID, prepared)
}

func (s *Service) prepareControl(ctx context.Context, req CreateRequest, runner, strategyVersion string, parameters any) (controlPrepared, error) {
	req = s.normalizeRequest(ctx, req)
	if err := s.validateBasicRequest(ctx, req); err != nil {
		return controlPrepared{}, err
	}
	bars, err := s.loadBars(ctx, req)
	if err != nil {
		return controlPrepared{}, err
	}
	if len(bars) == 0 {
		return controlPrepared{}, fmt.Errorf("研究區間沒有行情資料")
	}
	initial := 10000.0
	monthly := 0.0
	if req.InitialCapital != nil {
		initial = *req.InitialCapital
	}
	if req.MonthlyDCA != nil {
		monthly = *req.MonthlyDCA
	}
	if initial <= 0 || monthly < 0 {
		return controlPrepared{}, fmt.Errorf("對照回測資金設定無效")
	}
	costs := backtestCosts(req)
	coreSpec := backtestcore.Spec{Runner: runner, InstrumentID: req.InstrumentID, Symbol: req.Symbol, DataSource: req.DataSource, Interval: req.Interval, ExecutionMode: req.ExecutionMode, PositionStructure: "control", StartTimeMs: bars[0].OpenTime, EndTimeMs: bars[len(bars)-1].OpenTime, EvaluationStartMs: bars[0].OpenTime, EvaluationEndMs: bars[len(bars)-1].OpenTime, PrefixMode: backtestcore.PrefixModeExecute, InitialCapital: initial, MonthlyContribution: monthly, Costs: costs, CoreVersion: backtestcore.CoreVersion}
	identity, err := backtestresult.BuildIdentity(backtestresult.SpecInput{StrategyID: sigmoiddca.StrategyID, StrategyVersion: strategyVersion, ParameterSchemaVersion: ControlParameterSchemaVersion, Parameters: parameters, CoreSpec: coreSpec, DatasetVersion: backtestresult.DatasetSchemaVersion}, bars)
	if err != nil {
		return controlPrepared{}, err
	}
	coreSpec.DatasetHash = identity.Snapshot.DatasetHash
	return controlPrepared{request: req, bars: bars, coreSpec: coreSpec, identity: identity}, nil
}

func (s *Service) ensureControlPrepared(ctx context.Context, _ uint, prepared controlPrepared) (StandardExecutionResult, error) {
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
	artifacts, err := s.executeControlPrepared(prepared)
	if err != nil {
		_ = s.results.Fail(context.Background(), reservation.Result.ID, err)
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

func (s *Service) executeControlPrepared(prepared controlPrepared) (backtestresult.Artifacts, error) {
	path, err := prepared.run()
	if err != nil {
		return backtestresult.Artifacts{}, err
	}
	baseline := quant.SimulateGhostDCAFrom(prepared.bars, prepared.bars[0].OpenTime, quant.GhostDCAConfig{InitialUSDT: prepared.coreSpec.InitialCapital, MonthlyInjectUSDT: prepared.coreSpec.MonthlyContribution, UseOpenExecution: prepared.request.ExecutionMode == backtestcore.ExecutionModeCloseNextOpen, Costs: prepared.coreSpec.Costs})
	extra := storedResponseMetrics{
		Runner:               prepared.coreSpec.Runner,
		StrategyVersion:      prepared.identity.Snapshot.StrategyVersion,
		Alpha:                path.TotalReturn - baseline.ROI,
		Benchmark:            baseline.FinalEquity,
		BenchmarkReturn:      baseline.ROI,
		BenchmarkMaxDrawdown: baseline.MaxDrawdown,
		BenchmarkFinalEquity: baseline.FinalEquity,
		FeeRate:              prepared.coreSpec.Costs.FeeRate,
		SpreadRate:           prepared.coreSpec.Costs.SpreadRate,
		FeeCost:              path.Costs.FeeCost,
		SlippageCost:         path.Costs.SlippageCost,
		TotalExecutionCost:   path.Costs.TotalCost,
		PositionStructure:    "market_baseline",
		Windows:              map[string]float64{},
		WindowDetails:        []WindowResult{},
		PracticalTotalReturn: path.PracticalTotalReturn,
		PracticalMaxDrawdown: maxDrawdown(path.Path),
		PracticalFinalEquity: path.PracticalFinalAssets,
		PracticalTradeCount:  path.PracticalTradeCount,
	}
	summary, err := backtestresult.BuildSummary(path, maxDrawdown(path.Path), backtestresult.SummaryOptions{Extra: extra})
	if err != nil {
		return backtestresult.Artifacts{}, err
	}
	return backtestresult.BuildArtifacts(prepared.identity.SpecContentHash, summary, standardizedPath(path.Path, baseline), backtestresult.DefaultPathBlockSize)
}
