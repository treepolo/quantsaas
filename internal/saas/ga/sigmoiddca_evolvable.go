package ga

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/fnv"
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
	forceEmptyThreshold := sampleRange(rng, "force_empty_threshold")
	forceFullThreshold := forceEmptyThreshold + rng.Float64()*(1-forceEmptyThreshold)
	c := quant.Chromosome{
		MicroReservePct:        sampleRange(rng, "micro_reserve_pct"),
		Beta:                   sampleRange(rng, "beta"),
		Gamma:                  sampleRange(rng, "gamma"),
		WMean:                  sampleRange(rng, "w_mean"),
		WMomentum:              sampleRange(rng, "w_momentum"),
		WBreakout:              sampleRange(rng, "w_breakout"),
		DustUSD:                sampleRange(rng, "dust_usd"),
		RebalanceThreshold:     sampleRange(rng, "rebalance_threshold"),
		ForceFullThreshold:     forceFullThreshold,
		ForceEmptyThreshold:    forceEmptyThreshold,
		WedgeDeltaThreshold:    sampleRange(rng, "wedge_delta_threshold"),
		WedgeVolRatioThreshold: sampleRange(rng, "wedge_vol_ratio_threshold"),
		MacroBearMultiplier:    sampleRange(rng, "macro_bear_multiplier"),
		MacroBullMultiplier:    sampleRange(rng, "macro_bull_multiplier"),
		ExtraDeployPct:         sampleRange(rng, "extra_deploy_pct"),
		SoftReleaseMonths:      int(sampleRange(rng, "soft_release_months")),
		SoftReleasePct:         sampleRange(rng, "soft_release_pct"),
		HardReleaseMaxPct:      sampleRange(rng, "hard_release_max_pct"),
	}
	return quant.ClampChromosome(c)
}

func (SigmoidDCAEvolvable) Mutate(g Gene, prob float64, scale float64, rng RandomSource) Gene {
	c := asChromosome(g)
	c.MicroReservePct = mutateFloat(c.MicroReservePct, "micro_reserve_pct", prob, scale, rng)
	c.Beta = mutateFloat(c.Beta, "beta", prob, scale, rng)
	c.Gamma = mutateFloat(c.Gamma, "gamma", prob, scale, rng)
	c.WMean = mutateFloat(c.WMean, "w_mean", prob, scale, rng)
	c.WMomentum = mutateFloat(c.WMomentum, "w_momentum", prob, scale, rng)
	c.WBreakout = mutateFloat(c.WBreakout, "w_breakout", prob, scale, rng)
	c.DustUSD = mutateFloat(c.DustUSD, "dust_usd", prob, scale, rng)
	c.RebalanceThreshold = mutateFloat(c.RebalanceThreshold, "rebalance_threshold", prob, scale, rng)
	c.ForceFullThreshold = mutateFloat(c.ForceFullThreshold, "force_full_threshold", prob, scale, rng)
	c.ForceEmptyThreshold = mutateFloat(c.ForceEmptyThreshold, "force_empty_threshold", prob, scale, rng)
	c.WedgeDeltaThreshold = mutateFloat(c.WedgeDeltaThreshold, "wedge_delta_threshold", prob, scale, rng)
	c.WedgeVolRatioThreshold = mutateFloat(c.WedgeVolRatioThreshold, "wedge_vol_ratio_threshold", prob, scale, rng)
	c.MacroBearMultiplier = mutateFloat(c.MacroBearMultiplier, "macro_bear_multiplier", prob, scale, rng)
	c.MacroBullMultiplier = mutateFloat(c.MacroBullMultiplier, "macro_bull_multiplier", prob, scale, rng)
	c.ExtraDeployPct = mutateFloat(c.ExtraDeployPct, "extra_deploy_pct", prob, scale, rng)
	c.SoftReleaseMonths = int(math.Round(mutateFloat(float64(c.SoftReleaseMonths), "soft_release_months", prob, scale, rng)))
	c.SoftReleasePct = mutateFloat(c.SoftReleasePct, "soft_release_pct", prob, scale, rng)
	c.HardReleaseMaxPct = mutateFloat(c.HardReleaseMaxPct, "hard_release_max_pct", prob, scale, rng)
	return quant.ClampChromosome(c)
}

func (SigmoidDCAEvolvable) Crossover(p1 Gene, p2 Gene, rng RandomSource) Gene {
	a := asChromosome(p1)
	b := asChromosome(p2)
	c := quant.Chromosome{}
	c.MicroReservePct = pick(rng, a.MicroReservePct, b.MicroReservePct)
	c.Beta = pick(rng, a.Beta, b.Beta)
	c.Gamma = pick(rng, a.Gamma, b.Gamma)
	c.WMean = pick(rng, a.WMean, b.WMean)
	c.WMomentum = pick(rng, a.WMomentum, b.WMomentum)
	c.WBreakout = pick(rng, a.WBreakout, b.WBreakout)
	c.DustUSD = pick(rng, a.DustUSD, b.DustUSD)
	c.RebalanceThreshold = pick(rng, a.RebalanceThreshold, b.RebalanceThreshold)
	c.ForceFullThreshold = pick(rng, a.ForceFullThreshold, b.ForceFullThreshold)
	c.ForceEmptyThreshold = pick(rng, a.ForceEmptyThreshold, b.ForceEmptyThreshold)
	c.WedgeDeltaThreshold = pick(rng, a.WedgeDeltaThreshold, b.WedgeDeltaThreshold)
	c.WedgeVolRatioThreshold = pick(rng, a.WedgeVolRatioThreshold, b.WedgeVolRatioThreshold)
	c.MacroBearMultiplier = pick(rng, a.MacroBearMultiplier, b.MacroBearMultiplier)
	c.MacroBullMultiplier = pick(rng, a.MacroBullMultiplier, b.MacroBullMultiplier)
	c.ExtraDeployPct = pick(rng, a.ExtraDeployPct, b.ExtraDeployPct)
	c.SoftReleaseMonths = int(pick(rng, float64(a.SoftReleaseMonths), float64(b.SoftReleaseMonths)))
	c.SoftReleasePct = pick(rng, a.SoftReleasePct, b.SoftReleasePct)
	c.HardReleaseMaxPct = pick(rng, a.HardReleaseMaxPct, b.HardReleaseMaxPct)
	return quant.ClampChromosome(c)
}

func (SigmoidDCAEvolvable) Fingerprint(g Gene) uint64 {
	c := asChromosome(g)
	h := fnv.New64a()
	writeQuantized(h, c.MicroReservePct)
	writeQuantized(h, c.Beta)
	writeQuantized(h, c.Gamma)
	writeQuantized(h, c.WMean)
	writeQuantized(h, c.WMomentum)
	writeQuantized(h, c.WBreakout)
	writeQuantized(h, c.DustUSD)
	writeQuantized(h, c.RebalanceThreshold)
	writeQuantized(h, c.ForceFullThreshold)
	writeQuantized(h, c.ForceEmptyThreshold)
	writeQuantized(h, c.WedgeDeltaThreshold)
	writeQuantized(h, c.WedgeVolRatioThreshold)
	writeQuantized(h, c.MacroBearMultiplier)
	writeQuantized(h, c.MacroBullMultiplier)
	writeQuantized(h, c.ExtraDeployPct)
	writeQuantized(h, float64(c.SoftReleaseMonths))
	writeQuantized(h, c.SoftReleasePct)
	writeQuantized(h, c.HardReleaseMaxPct)
	return h.Sum64()
}

func (SigmoidDCAEvolvable) NormalizeGene(g Gene, options GeneOptions) Gene {
	c := asChromosome(g)
	options = NormalizeGeneOptions(options)
	if !options.EvolveRebalanceThreshold {
		c.RebalanceThreshold = 0
	}
	if !options.EvolveForceFullThreshold {
		c.ForceFullThreshold = 1
	}
	if !options.EvolveForceEmptyThreshold {
		c.ForceEmptyThreshold = 0
	}
	if !options.EvolveGamma {
		c.Gamma = 0
	}
	if !options.EnableWMean {
		c.WMean = 0
	}
	if !options.EnableWMomentum {
		c.WMomentum = 0
	}
	if !options.EnableWBreakout {
		c.WBreakout = 0
	}
	if options.PositionStructure == sigmoiddca.PositionStructureFloatingOnly {
		c.MacroBearMultiplier = 1
		c.MacroBullMultiplier = 1
		c.ExtraDeployPct = 0
		c.SoftReleaseMonths = int(quant.HardBounds["soft_release_months"].Max)
		c.SoftReleasePct = 0
		c.HardReleaseMaxPct = 0
	}
	if options.FixedGene != nil && len(options.FixedParamKeys) > 0 {
		c = applyFixedChromosomeFields(c, *options.FixedGene, options.FixedParamKeys)
	}
	return quant.ClampChromosome(c)
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
		metrics := runSigmoidDCAPathBacktestWithTraceAndMode(window.Bars, window.EvalStartMs, plan.Interval, plan.ExecutionMode, c, plan.Spawn, PathTraceConfig{
			Trace:         plan.Trace,
			Mode:          plan.TraceMode,
			TraceModeFunc: plan.TraceModeFunc,
			ComputeStep:   plan.ComputeStep,
			Generation:    plan.Generation,
			Individual:    plan.Individual,
			Worker:        plan.Worker,
			Window:        window.Label,
		}, plan.Costs, NormalizeGeneOptions(plan.GeneOptions).PositionStructure, plan.Pair).Metrics
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
	return runSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, PathTraceConfig{}, costs, sigmoiddca.PositionStructureDualLayer, "BTCUSDT")
}

func RunSigmoidDCAPathBacktestWithModeCostsAndStructure(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, costs quant.ExecutionCostConfig, positionStructure string) SigmoidDCAPathResult {
	return RunSigmoidDCAPathBacktestForInstrument(bars, evalStartMs, "BTCUSDT", interval, executionMode, chromosome, spawn, costs, positionStructure)
}

func RunSigmoidDCAPathBacktestForInstrument(bars []quant.Bar, evalStartMs int64, symbol string, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, costs quant.ExecutionCostConfig, positionStructure string) SigmoidDCAPathResult {
	return runSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, PathTraceConfig{}, costs, positionStructure, symbol)
}

func RunSigmoidDCAPathBacktestWithTrace(bars []quant.Bar, evalStartMs int64, interval string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig) SigmoidDCAPathResult {
	return RunSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionModeCloseSameBar, chromosome, spawn, traceCfg)
}

func RunSigmoidDCAPathBacktestWithTraceAndMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig) SigmoidDCAPathResult {
	return runSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, traceCfg, quant.ExecutionCostConfig{}, sigmoiddca.PositionStructureDualLayer, "BTCUSDT")
}

func runSigmoidDCAPathBacktestWithTraceAndMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig, costs quant.ExecutionCostConfig, positionStructure string, symbol string) SigmoidDCAPathResult {
	executionMode = normalizeBacktestExecutionMode(executionMode)
	if len(bars) == 0 || bars[0].Close <= 0 || executionMode == executionModePreclose10m {
		return SigmoidDCAPathResult{}
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
		},
		Bars:   bars,
		Params: params,
		Hooks:  hooks,
	})
	if err != nil {
		return SigmoidDCAPathResult{}
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

func sampleRange(rng RandomSource, name string) float64 {
	bound := quant.HardBounds[name]
	return bound.Min + rng.Float64()*(bound.Max-bound.Min)
}

func mutateFloat(v float64, name string, prob float64, scale float64, rng RandomSource) float64 {
	if rng.Float64() >= prob {
		return v
	}
	step := quant.GeneSteps[name]
	if step == 0 {
		step = 0.01
	}
	return v + rng.NormFloat64()*step*scale
}

func pick(rng RandomSource, a float64, b float64) float64 {
	if rng.Float64() < 0.5 {
		return a
	}
	return b
}

func writeQuantized(h interface{ Write([]byte) (int, error) }, v float64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(math.Round(v*1e6)))
	_, _ = h.Write(buf[:])
}

func firstEvalStart(bars []quant.Bar) int64 {
	if len(bars) == 0 {
		return 0
	}
	return bars[0].OpenTime
}
