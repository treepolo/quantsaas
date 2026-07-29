package ga

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

const (
	executionModeCloseSameBar  = "close_same_bar"
	executionModeCloseNextOpen = "close_next_open"
	executionModePreclose10m   = "preclose_10m"
)

type SigmoidDCAEvolvable struct{}

type BacktestPoint = backtestcore.NAVPoint

type SigmoidDCAPathResult struct {
	Metrics BacktestMetrics
	NAV     []BacktestPoint
	Err     error
}

func NewSigmoidDCAEvolvable() SigmoidDCAEvolvable {
	return SigmoidDCAEvolvable{}
}

func NormalizeGeneOptions(options GeneOptions) GeneOptions {
	if options.PositionStructure == "" {
		options.PositionStructure = sigmoiddca.PositionStructureDualLayer
	}
	options.PositionStructure = sigmoiddca.NormalizePositionStructure(options.PositionStructure)
	if !options.EnableWMean && !options.EnableWMomentum && !options.EnableWBreakout {
		options.EnableWMean = true
		options.EnableWMomentum = true
		options.EnableWBreakout = true
	}
	options.FixedParamKeys = NormalizeFixedParamKeys(options.FixedParamKeys)
	return options
}

func NormalizeFixedParamKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if !isFixedParamKey(key) || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

func IsFixedParamKey(key string) bool {
	return isFixedParamKey(key)
}

func isFixedParamKey(key string) bool {
	_, ok := quant.HardBounds[key]
	return ok
}

func (SigmoidDCAEvolvable) StrategyID() string {
	return sigmoiddca.StrategyID
}

func (SigmoidDCAEvolvable) Sample(rng RandomSource) Gene {
	return sampleChromosome(rng, NormalizeGeneOptions(GeneOptions{
		EvolveRebalanceThreshold:  true,
		EvolveForceFullThreshold:  true,
		EvolveForceEmptyThreshold: true,
		EvolveGamma:               true,
		EnableWMean:               true,
		EnableWMomentum:           true,
		EnableWBreakout:           true,
		PositionStructure:         sigmoiddca.PositionStructureDualLayer,
	}))
}

func (SigmoidDCAEvolvable) Mutate(g Gene, prob float64, scale float64, rng RandomSource) Gene {
	return mutateChromosome(asChromosome(g), prob, scale, rng, NormalizeGeneOptions(GeneOptions{
		EvolveRebalanceThreshold:  true,
		EvolveForceFullThreshold:  true,
		EvolveForceEmptyThreshold: true,
		EvolveGamma:               true,
		EnableWMean:               true,
		EnableWMomentum:           true,
		EnableWBreakout:           true,
		PositionStructure:         sigmoiddca.PositionStructureDualLayer,
	}))
}

func (SigmoidDCAEvolvable) Crossover(p1 Gene, p2 Gene, rng RandomSource) Gene {
	return crossoverChromosome(asChromosome(p1), asChromosome(p2), rng, NormalizeGeneOptions(GeneOptions{
		EvolveRebalanceThreshold:  true,
		EvolveForceFullThreshold:  true,
		EvolveForceEmptyThreshold: true,
		EvolveGamma:               true,
		EnableWMean:               true,
		EnableWMomentum:           true,
		EnableWBreakout:           true,
		PositionStructure:         sigmoiddca.PositionStructureDualLayer,
	}))
}

func (SigmoidDCAEvolvable) Fingerprint(g Gene) uint64 {
	return fingerprintChromosome(asChromosome(g), NormalizeGeneOptions(GeneOptions{
		EvolveRebalanceThreshold:  true,
		EvolveForceFullThreshold:  true,
		EvolveForceEmptyThreshold: true,
		EvolveGamma:               true,
		EnableWMean:               true,
		EnableWMomentum:           true,
		EnableWBreakout:           true,
		PositionStructure:         sigmoiddca.PositionStructureDualLayer,
	}))
}

func (SigmoidDCAEvolvable) SampleWithOptions(rng RandomSource, options GeneOptions) Gene {
	return sampleChromosome(rng, options)
}

func (SigmoidDCAEvolvable) MutateWithOptions(g Gene, prob float64, scale float64, rng RandomSource, options GeneOptions) Gene {
	return mutateChromosome(asChromosome(g), prob, scale, rng, options)
}

func (SigmoidDCAEvolvable) CrossoverWithOptions(p1 Gene, p2 Gene, rng RandomSource, options GeneOptions) Gene {
	return crossoverChromosome(asChromosome(p1), asChromosome(p2), rng, options)
}

func (SigmoidDCAEvolvable) FingerprintWithOptions(g Gene, options GeneOptions) uint64 {
	return fingerprintChromosome(asChromosome(g), options)
}

func (SigmoidDCAEvolvable) NormalizeGene(g Gene, options GeneOptions) Gene {
	return normalizeChromosomeForOptions(asChromosome(g), options)
}

func applyFixedChromosomeFields(c quant.Chromosome, base quant.Chromosome, keys []string) quant.Chromosome {
	base = quant.ClampChromosome(base)
	for _, key := range keys {
		switch key {
		case "micro_reserve_pct":
			c.MicroReservePct = base.MicroReservePct
		case "beta":
			c.Beta = base.Beta
		case "gamma":
			c.Gamma = base.Gamma
		case "w_mean":
			c.WMean = base.WMean
		case "w_momentum":
			c.WMomentum = base.WMomentum
		case "w_breakout":
			c.WBreakout = base.WBreakout
		case "dust_usd":
			c.DustUSD = base.DustUSD
		case "rebalance_threshold":
			c.RebalanceThreshold = base.RebalanceThreshold
		case "force_full_threshold":
			c.ForceFullThreshold = base.ForceFullThreshold
		case "force_empty_threshold":
			c.ForceEmptyThreshold = base.ForceEmptyThreshold
		case "wedge_delta_threshold":
			c.WedgeDeltaThreshold = base.WedgeDeltaThreshold
		case "wedge_vol_ratio_threshold":
			c.WedgeVolRatioThreshold = base.WedgeVolRatioThreshold
		case "macro_bear_multiplier":
			c.MacroBearMultiplier = base.MacroBearMultiplier
		case "macro_bull_multiplier":
			c.MacroBullMultiplier = base.MacroBullMultiplier
		case "extra_deploy_pct":
			c.ExtraDeployPct = base.ExtraDeployPct
		case "soft_release_months":
			c.SoftReleaseMonths = base.SoftReleaseMonths
		case "soft_release_pct":
			c.SoftReleasePct = base.SoftReleasePct
		case "hard_release_max_pct":
			c.HardReleaseMaxPct = base.HardReleaseMaxPct
		}
	}
	return c
}

func (e SigmoidDCAEvolvable) Evaluate(ctx context.Context, g Gene, plan EvaluablePlan) (FitnessResult, error) {
	c := e.NormalizeGene(g, plan.GeneOptions).(quant.Chromosome)
	if err := quant.ValidateChromosome(c); err != nil {
		trace(plan, TraceModeDetailed, "strategy", "individual.evaluate.invalid_gene", "invalid chromosome rejected", map[string]any{
			"generation": plan.Generation,
			"individual": plan.Individual,
			"worker":     plan.Worker,
			"error":      err.Error(),
		})
		return FitnessResult{ScoreTotal: FatalFitnessScore, Fatal: true}, nil
	}
	if len(plan.MultiMarkets) > 0 {
		return e.evaluateMultiMarket(ctx, c, plan)
	}
	result := FitnessResult{}
	for i, window := range plan.Windows {
		if err := ctx.Err(); err != nil {
			return FitnessResult{}, err
		}
		trace(plan, TraceModeDetailed, "strategy", "window.evaluate.start", "evaluation window started", map[string]any{
			"generation": plan.Generation,
			"individual": plan.Individual,
			"worker":     plan.Worker,
			"window":     window.Label,
			"bars":       len(window.Bars),
		})
		path := runSigmoidDCAPathBacktestWithTraceAndMode(window.Bars, window.EvalStartMs, plan.Interval, plan.ExecutionMode, c, plan.Spawn, PathTraceConfig{
			Trace:         plan.Trace,
			Mode:          plan.TraceMode,
			TraceModeFunc: plan.TraceModeFunc,
			ComputeStep:   plan.ComputeStep,
			Generation:    plan.Generation,
			Individual:    plan.Individual,
			Worker:        plan.Worker,
			Window:        window.Label,
		}, plan.Costs, NormalizeGeneOptions(plan.GeneOptions).PositionStructure, plan.Pair, plan.LongTermFilter)
		if path.Err != nil {
			return FitnessResult{}, fmt.Errorf("window %s backtest failed: %w", window.Label, path.Err)
		}
		metrics := path.Metrics
		baseline := plan.DCABaselines[i]
		alpha := metrics.ROI - baseline.ROI
		score := alpha - 1.5*math.Max(0, metrics.MaxDrawdown-baseline.MaxDrawdown) - plan.TradePenalty*float64(metrics.TradeCount)
		if metrics.MaxDrawdown >= 0.88 {
			score = FatalFitnessScore
			result.Fatal = true
		}
		result.Windows = append(result.Windows, quant.CrucibleResult{
			Window: window.Label,
			Score:  score,
			ROI:    metrics.ROI,
			MaxDD:  metrics.MaxDrawdown,
			Alpha:  alpha,
		})
		if metrics.MaxDrawdown > result.MaxDrawdown {
			result.MaxDrawdown = metrics.MaxDrawdown
		}
		if result.Fatal {
			result.ScoreTotal = FatalFitnessScore
			trace(plan, TraceModeDetailed, "strategy", "window.evaluate.fatal", "window triggered fatal fitness", map[string]any{
				"generation":      plan.Generation,
				"individual":      plan.Individual,
				"worker":          plan.Worker,
				"window":          window.Label,
				"score":           score,
				"roi":             metrics.ROI,
				"alpha":           alpha,
				"max_drawdown":    metrics.MaxDrawdown,
				"trade_count":     metrics.TradeCount,
				"trade_penalty":   plan.TradePenalty,
				"baseline_roi":    baseline.ROI,
				"baseline_max_dd": baseline.MaxDrawdown,
			})
			return result, nil
		}
		result.ScoreTotal += window.Weight * score
		trace(plan, TraceModeDetailed, "strategy", "window.evaluate.done", "evaluation window completed", map[string]any{
			"generation":      plan.Generation,
			"individual":      plan.Individual,
			"worker":          plan.Worker,
			"window":          window.Label,
			"score":           score,
			"weighted_score":  window.Weight * score,
			"roi":             metrics.ROI,
			"alpha":           alpha,
			"max_drawdown":    metrics.MaxDrawdown,
			"trade_count":     metrics.TradeCount,
			"trade_penalty":   plan.TradePenalty,
			"baseline_roi":    baseline.ROI,
			"baseline_max_dd": baseline.MaxDrawdown,
			"final_equity":    metrics.FinalEquity,
			"total_injected":  metrics.TotalInjected,
		})
	}
	return result, nil
}

func (e SigmoidDCAEvolvable) evaluateMultiMarket(ctx context.Context, chromosome quant.Chromosome, plan EvaluablePlan) (FitnessResult, error) {
	result := FitnessResult{}
	for _, market := range plan.MultiMarkets {
		if err := ctx.Err(); err != nil {
			return FitnessResult{}, err
		}
		path := runSigmoidDCAPathBacktestWithTraceAndMode(
			market.Window.Bars,
			market.Window.EvalStartMs,
			market.Interval,
			plan.ExecutionMode,
			chromosome,
			plan.Spawn,
			PathTraceConfig{
				Trace:         plan.Trace,
				Mode:          plan.TraceMode,
				TraceModeFunc: plan.TraceModeFunc,
				ComputeStep:   plan.ComputeStep,
				Generation:    plan.Generation,
				Individual:    plan.Individual,
				Worker:        plan.Worker,
				Window:        market.InstrumentID,
			},
			plan.Costs,
			NormalizeGeneOptions(plan.GeneOptions).PositionStructure,
			market.Pair,
			plan.LongTermFilter,
		)
		if path.Err != nil {
			return FitnessResult{}, fmt.Errorf("market %s backtest failed: %w", market.InstrumentID, path.Err)
		}
		metrics := path.Metrics
		performance := MarketPerformance{
			InstrumentID: market.InstrumentID,
			Pair:         market.Pair,
			TotalReturn:  metrics.ROI,
			MaxDrawdown:  metrics.MaxDrawdown,
		}
		if metrics.MaxDrawdown > result.MaxDrawdown {
			result.MaxDrawdown = metrics.MaxDrawdown
		}
		if metrics.MaxDrawdown >= 0.88 {
			performance.FailureReason = "最大回撤觸及 88% 淘汰門檻"
			result.Markets = append(result.Markets, performance)
			result.Fatal = true
			result.FailureReason = fmt.Sprintf("%s：%s", market.InstrumentID, performance.FailureReason)
			result.ScoreTotal = FatalFitnessScore
			return result, nil
		}
		if len(market.Window.Bars) < 2 {
			return FitnessResult{}, fmt.Errorf("行情 %s 的資料不足", market.InstrumentID)
		}
		durationMs := market.Window.Bars[len(market.Window.Bars)-1].OpenTime - market.Window.EvalStartMs
		years := float64(durationMs) / (365.2425 * 24 * 60 * 60 * 1000)
		if years <= 0 {
			return FitnessResult{}, fmt.Errorf("行情 %s 的可評估期間無效", market.InstrumentID)
		}
		if 1+metrics.ROI <= 0 {
			performance.FailureReason = "年化成長倍數無法定義"
			result.Markets = append(result.Markets, performance)
			result.Fatal = true
			result.FailureReason = fmt.Sprintf("%s：%s", market.InstrumentID, performance.FailureReason)
			result.ScoreTotal = FatalFitnessScore
			return result, nil
		}
		scoreContribution, annualized, ok := multiMarketReturnScore(metrics.ROI, years)
		if !ok {
			performance.FailureReason = "年化報酬無法定義"
			result.Markets = append(result.Markets, performance)
			result.Fatal = true
			result.FailureReason = fmt.Sprintf("%s：%s", market.InstrumentID, performance.FailureReason)
			result.ScoreTotal = FatalFitnessScore
			return result, nil
		}
		performance.AnnualizedReturn = &annualized
		result.Markets = append(result.Markets, performance)
		result.ScoreTotal += scoreContribution
	}
	return result, nil
}

func multiMarketReturnScore(totalReturn float64, years float64) (score float64, annualized float64, ok bool) {
	if years <= 0 || 1+totalReturn <= 0 {
		return 0, 0, false
	}
	score = math.Log1p(totalReturn)
	annualized = math.Expm1(score / years)
	if math.IsNaN(score) || math.IsInf(score, 0) || math.IsNaN(annualized) || math.IsInf(annualized, 0) {
		return 0, 0, false
	}
	return score, annualized, true
}

func (SigmoidDCAEvolvable) DecodeElite(raw []byte) Gene {
	if len(raw) == 0 {
		return quant.DefaultSeedChromosome
	}
	params := sigmoiddca.ParseParamsFromParamPack(raw)
	return params.Chromosome
}

func (e SigmoidDCAEvolvable) EncodeResult(g Gene, spawn *quant.SpawnPoint, options GeneOptions) ([]byte, error) {
	options = NormalizeGeneOptions(options)
	params := sigmoiddca.DefaultParams()
	params.Chromosome = e.NormalizeGene(g, options).(quant.Chromosome)
	if err := quant.ValidateChromosome(params.Chromosome); err != nil {
		return nil, err
	}
	params.PositionStructure = options.PositionStructure
	if spawn != nil {
		params.Spawn = *spawn
	}
	return json.Marshal(params)
}

func (SigmoidDCAEvolvable) Verify(ctx context.Context, g Gene, spawn *quant.SpawnPoint, bars []quant.Bar, _ float64, _ float64) (BacktestMetrics, error) {
	if err := ctx.Err(); err != nil {
		return BacktestMetrics{}, err
	}
	return RunSigmoidDCASingleBacktest(bars, firstEvalStart(bars), "1d", asChromosome(g), spawn), nil
}

func RunSigmoidDCASingleBacktest(bars []quant.Bar, evalStartMs int64, interval string, chromosome quant.Chromosome, spawn *quant.SpawnPoint) BacktestMetrics {
	return RunSigmoidDCAPathBacktest(bars, evalStartMs, interval, chromosome, spawn).Metrics
}

func RunSigmoidDCASingleBacktestWithMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint) BacktestMetrics {
	return RunSigmoidDCAPathBacktestWithMode(bars, evalStartMs, interval, executionMode, chromosome, spawn).Metrics
}

func RunSigmoidDCASingleBacktestWithModeAndCosts(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, costs quant.ExecutionCostConfig) BacktestMetrics {
	return RunSigmoidDCAPathBacktestWithModeAndCosts(bars, evalStartMs, interval, executionMode, chromosome, spawn, costs).Metrics
}

type PathTraceConfig struct {
	Trace         func(TraceEvent)
	Mode          TraceMode
	TraceModeFunc func() TraceMode
	ComputeStep   func(int64)
	Generation    int
	Individual    int
	Worker        int
	Window        string
}

func RunSigmoidDCASingleBacktestWithTrace(bars []quant.Bar, evalStartMs int64, interval string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig) BacktestMetrics {
	return RunSigmoidDCAPathBacktestWithTrace(bars, evalStartMs, interval, chromosome, spawn, traceCfg).Metrics
}

func RunSigmoidDCASingleBacktestWithTraceAndMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig) BacktestMetrics {
	return RunSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, traceCfg).Metrics
}

func RunSigmoidDCAPathBacktest(bars []quant.Bar, evalStartMs int64, interval string, chromosome quant.Chromosome, spawn *quant.SpawnPoint) SigmoidDCAPathResult {
	return RunSigmoidDCAPathBacktestWithTrace(bars, evalStartMs, interval, chromosome, spawn, PathTraceConfig{})
}

func RunSigmoidDCAPathBacktestWithMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint) SigmoidDCAPathResult {
	return RunSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, PathTraceConfig{})
}

func RunSigmoidDCAPathBacktestWithModeAndCosts(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, costs quant.ExecutionCostConfig) SigmoidDCAPathResult {
	return runSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, PathTraceConfig{}, costs, sigmoiddca.PositionStructureDualLayer, "BTCUSDT", backtestcore.LongTermFilterConfig{})
}

func RunSigmoidDCAPathBacktestWithModeCostsAndStructure(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, costs quant.ExecutionCostConfig, positionStructure string) SigmoidDCAPathResult {
	return RunSigmoidDCAPathBacktestForInstrument(bars, evalStartMs, "BTCUSDT", interval, executionMode, chromosome, spawn, costs, positionStructure)
}

func RunSigmoidDCAPathBacktestForInstrument(bars []quant.Bar, evalStartMs int64, symbol string, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, costs quant.ExecutionCostConfig, positionStructure string) SigmoidDCAPathResult {
	return runSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, PathTraceConfig{}, costs, positionStructure, symbol, backtestcore.LongTermFilterConfig{})
}

func RunSigmoidDCAPathBacktestWithTrace(bars []quant.Bar, evalStartMs int64, interval string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig) SigmoidDCAPathResult {
	return RunSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionModeCloseSameBar, chromosome, spawn, traceCfg)
}

func RunSigmoidDCAPathBacktestWithTraceAndMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig) SigmoidDCAPathResult {
	return runSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, traceCfg, quant.ExecutionCostConfig{}, sigmoiddca.PositionStructureDualLayer, "BTCUSDT", backtestcore.LongTermFilterConfig{})
}

func runSigmoidDCAPathBacktestWithTraceAndMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig, costs quant.ExecutionCostConfig, positionStructure string, symbol string, longTermFilter backtestcore.LongTermFilterConfig) SigmoidDCAPathResult {
	executionMode = normalizeBacktestExecutionMode(executionMode)
	if len(bars) == 0 {
		return SigmoidDCAPathResult{Err: fmt.Errorf("回測 K 線不可為空")}
	}
	if bars[0].Close <= 0 {
		return SigmoidDCAPathResult{Err: fmt.Errorf("回測價格必須大於 0")}
	}
	if executionMode == executionModePreclose10m {
		return SigmoidDCAPathResult{Err: fmt.Errorf("收盤前模式缺少歷史快照")}
	}
	params := sigmoiddca.DefaultParams()
	params.Chromosome = quant.ClampChromosome(chromosome)
	params.PositionStructure = sigmoiddca.NormalizePositionStructure(positionStructure)
	if spawn != nil {
		params.Spawn = *spawn
	}
	hooks := backtestcore.Hooks{ComputeStep: traceCfg.ComputeStep}
	if traceCfg.Trace != nil {
		hooks.OnStep = func(event backtestcore.StepEvent) {
			if !TraceEnabled(activePathTraceMode(traceCfg), TraceModeFull) {
				return
			}
			tracePath(traceCfg, TraceModeFull, "strategy", "step.computed", "strategy step computed", map[string]any{
				"generation":      traceCfg.Generation,
				"individual":      traceCfg.Individual,
				"worker":          traceCfg.Worker,
				"window":          traceCfg.Window,
				"bar_index":       event.Index,
				"open_time":       event.Bar.OpenTime,
				"close":           event.Bar.Close,
				"execution_mode":  event.ExecutionMode,
				"total_equity":    event.TotalEquity,
				"usdt_balance":    event.Portfolio.USDTBalance,
				"dead_btc":        event.Portfolio.DeadBTC,
				"float_btc":       event.Portfolio.FloatBTC,
				"cold_sealed_btc": event.Portfolio.ColdSealedBTC,
				"intents":         len(event.Output.Intents),
				"lot_transfers":   len(event.Output.LotTransfers),
				"diagnostics":     event.Output.Diagnostics,
			})
		}
	}
	result, err := backtestcore.RunSigmoidDCA(backtestcore.SigmoidDCARequest{
		Spec: backtestcore.Spec{
			Symbol:               symbol,
			Interval:             interval,
			ExecutionMode:        executionMode,
			PositionStructure:    params.PositionStructure,
			StartTimeMs:          bars[0].OpenTime,
			EndTimeMs:            bars[len(bars)-1].OpenTime,
			EvaluationStartMs:    evalStartMs,
			EvaluationEndMs:      bars[len(bars)-1].OpenTime,
			InitialCapital:       params.Spawn.Policy.InitialUSDT,
			MonthlyContribution:  params.Spawn.Policy.MonthlyInjectUSDT,
			InitialAssetQuantity: params.Spawn.Policy.ColdSealedBTC,
			Costs:                costs,
			LongTermFilter:       longTermFilter,
		},
		Bars:   bars,
		Params: params,
		Hooks:  hooks,
	})
	if err != nil {
		return SigmoidDCAPathResult{Err: err}
	}
	nav := make([]float64, 0, len(result.Path))
	for _, point := range result.Path {
		nav = append(nav, point.TotalEquity)
	}
	metrics := BacktestMetrics{
		ROI:           result.TotalReturn,
		MaxDrawdown:   quant.MaxDrawdown(nav),
		FinalEquity:   result.FinalAssets,
		TotalInjected: result.TotalInjected,
		TradeCount:    result.TradeCount,
	}
	return SigmoidDCAPathResult{
		Metrics: metrics,
		NAV:     result.Path,
	}
}

func applyForceTargetThresholds(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, chromosome quant.Chromosome, modelTargetWeight float64) (quant.StrategyOutput, float64) {
	return backtestcore.ApplyForceTargetThresholds(output, portfolio, price, chromosome, modelTargetWeight)
}

func normalizeBacktestExecutionMode(mode string) string {
	switch mode {
	case executionModeCloseNextOpen, executionModePreclose10m:
		return mode
	default:
		return executionModeCloseSameBar
	}
}

func usesNextOpenExecution(mode string) bool {
	return normalizeBacktestExecutionMode(mode) == executionModeCloseNextOpen
}

func trace(plan EvaluablePlan, required TraceMode, source string, scope string, message string, fields map[string]any) {
	if plan.Trace == nil || !TraceEnabled(activePlanTraceMode(plan), required) {
		return
	}
	plan.Trace(TraceEvent{
		RequiredMode: required,
		Level:        "trace",
		Source:       source,
		Scope:        scope,
		Message:      message,
		Fields:       fields,
	})
}

func tracePath(traceCfg PathTraceConfig, required TraceMode, source string, scope string, message string, fields map[string]any) {
	if traceCfg.Trace == nil || !TraceEnabled(activePathTraceMode(traceCfg), required) {
		return
	}
	traceCfg.Trace(TraceEvent{
		RequiredMode: required,
		Level:        "trace",
		Source:       source,
		Scope:        scope,
		Message:      message,
		Fields:       fields,
	})
}

func activePlanTraceMode(plan EvaluablePlan) TraceMode {
	if plan.TraceModeFunc != nil {
		return NormalizeTraceMode(plan.TraceModeFunc())
	}
	return NormalizeTraceMode(plan.TraceMode)
}

func activePathTraceMode(traceCfg PathTraceConfig) TraceMode {
	if traceCfg.TraceModeFunc != nil {
		return NormalizeTraceMode(traceCfg.TraceModeFunc())
	}
	return NormalizeTraceMode(traceCfg.Mode)
}

func applyRebalanceThreshold(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, threshold float64) quant.StrategyOutput {
	return backtestcore.ApplyRebalanceThreshold(output, portfolio, price, threshold)
}

func rebalanceThresholdAllows(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, threshold float64) bool {
	return backtestcore.RebalanceThresholdAllows(output, portfolio, price, threshold)
}

func applyBacktestOutput(portfolio quant.PortfolioSnapshot, output quant.StrategyOutput, price float64) quant.PortfolioSnapshot {
	return applyBacktestOutputWithCosts(portfolio, output, price, quant.ExecutionCostConfig{})
}

func applyBacktestOutputWithCosts(portfolio quant.PortfolioSnapshot, output quant.StrategyOutput, price float64, costs quant.ExecutionCostConfig) quant.PortfolioSnapshot {
	updated, _ := backtestcore.ApplyStrategyOutput(portfolio, output, price, backtestcore.SimulatorConfig{Costs: costs})
	return updated
}

func asChromosome(g Gene) quant.Chromosome {
	if c, ok := g.(quant.Chromosome); ok {
		return quant.ClampChromosome(c)
	}
	return quant.DefaultSeedChromosome
}

func firstEvalStart(bars []quant.Bar) int64 {
	if len(bars) == 0 {
		return 0
	}
	return bars[0].OpenTime
}
