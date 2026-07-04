package ga

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/fnv"
	"math"
	"strings"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

const (
	executionModeCloseSameBar  = "close_same_bar"
	executionModeCloseNextOpen = "close_next_open"
	executionModePreclose10m   = "preclose_10m"
)

type SigmoidDCAEvolvable struct{}

type BacktestPoint struct {
	TimeMs                           int64
	Price                            float64
	TotalEquity                      float64
	PracticalTargetWeight            float64
	PracticalTargetWeightChange      float64
	ModelTargetWeight                float64
	ModelTargetWeightChange          float64
	EmptyReferenceTargetWeight       float64
	EmptyReferenceTargetWeightChange float64
}

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
		}, plan.Costs, NormalizeGeneOptions(plan.GeneOptions).PositionStructure).Metrics
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
	return runSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, PathTraceConfig{}, costs, sigmoiddca.PositionStructureDualLayer)
}

func RunSigmoidDCAPathBacktestWithModeCostsAndStructure(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, costs quant.ExecutionCostConfig, positionStructure string) SigmoidDCAPathResult {
	return runSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, PathTraceConfig{}, costs, positionStructure)
}

func RunSigmoidDCAPathBacktestWithTrace(bars []quant.Bar, evalStartMs int64, interval string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig) SigmoidDCAPathResult {
	return RunSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionModeCloseSameBar, chromosome, spawn, traceCfg)
}

func RunSigmoidDCAPathBacktestWithTraceAndMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig) SigmoidDCAPathResult {
	return runSigmoidDCAPathBacktestWithTraceAndMode(bars, evalStartMs, interval, executionMode, chromosome, spawn, traceCfg, quant.ExecutionCostConfig{}, sigmoiddca.PositionStructureDualLayer)
}

func runSigmoidDCAPathBacktestWithTraceAndMode(bars []quant.Bar, evalStartMs int64, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint, traceCfg PathTraceConfig, costs quant.ExecutionCostConfig, positionStructure string) SigmoidDCAPathResult {
	costs = quant.NormalizeExecutionCosts(costs)
	executionMode = normalizeBacktestExecutionMode(executionMode)
	if len(bars) == 0 || bars[0].Close <= 0 || executionMode == executionModePreclose10m {
		return SigmoidDCAPathResult{}
	}
	if interval == "" {
		interval = "1d"
	}

	params := sigmoiddca.DefaultParams()
	params.Chromosome = quant.ClampChromosome(chromosome)
	params.PositionStructure = sigmoiddca.NormalizePositionStructure(positionStructure)
	if spawn != nil {
		params.Spawn = *spawn
	}

	portfolio := quant.PortfolioSnapshot{USDTBalance: params.Spawn.Policy.InitialUSDT}
	if portfolio.USDTBalance <= 0 {
		portfolio.USDTBalance = 1000
	}
	portfolio.ColdSealedBTC = params.Spawn.Policy.ColdSealedBTC
	evalInjected := 0.0
	evalFlows := make([]quant.TimedCashFlow, 0)
	state := map[string]any{}
	nav := make([]float64, 0, len(bars))
	points := make([]BacktestPoint, 0, len(bars))
	closes := make([]float64, 0, len(bars))
	timestamps := make([]int64, 0, len(bars))
	lastYear, lastMonth := barYearMonth(bars[0])
	evalInitial := 0.0
	actualEvalStart := int64(0)
	pendingOutput := quant.StrategyOutput{}
	hasPendingOutput := false
	prevModelTargetWeight := 0.0
	prevPracticalTargetWeight := 0.0
	prevEmptyReferenceTargetWeight := 0.0
	adoptedPracticalTargetWeight := 0.0
	hasAdoptedPracticalTargetWeight := false
	hasPrevTargetWeight := false
	tradeCount := 0

	for i, bar := range bars {
		if bar.Close <= 0 {
			continue
		}
		if traceCfg.ComputeStep != nil {
			traceCfg.ComputeStep(1)
		}
		year, month := barYearMonth(bar)
		if i > 0 && (year != lastYear || month != lastMonth) && params.Spawn.Policy.MonthlyInjectUSDT > 0 {
			portfolio.USDTBalance += params.Spawn.Policy.MonthlyInjectUSDT
			if bar.OpenTime > evalStartMs {
				evalInjected += params.Spawn.Policy.MonthlyInjectUSDT
				evalFlows = append(evalFlows, quant.TimedCashFlow{TimeMs: bar.OpenTime, Amount: params.Spawn.Policy.MonthlyInjectUSDT})
			}
			lastYear, lastMonth = year, month
		}
		if usesNextOpenExecution(executionMode) && hasPendingOutput {
			if bar.Open <= 0 {
				return SigmoidDCAPathResult{}
			}
			if bar.OpenTime >= evalStartMs {
				tradeCount += countTradeIntents(pendingOutput)
			}
			portfolio = applyBacktestOutputWithCosts(portfolio, pendingOutput, bar.Open, costs)
			hasPendingOutput = false
		}

		closes = append(closes, bar.Close)
		timestamps = append(timestamps, bar.OpenTime)
		portfolio.TotalEquity = portfolio.USDTBalance +
			(portfolio.DeadBTC+portfolio.FloatBTC+portfolio.ColdSealedBTC)*bar.Close
		output := sigmoiddca.Step(quant.StrategyInput{
			Symbol:       "BTCUSDT",
			Interval:     interval,
			Closes:       closes,
			Timestamps:   timestamps,
			Portfolio:    portfolio,
			RuntimeState: state,
			Spawn:        params.Spawn,
		}, params)
		rawModelTargetWeight := diagnosticValue(output.Diagnostics, "target_weight")
		modelTargetWeight := totalTargetWeight(portfolio, bar.Close, portfolio.TotalEquity, rawModelTargetWeight)
		practicalModelTargetWeight := modelTargetWeight
		output, practicalModelTargetWeight = applyForceTargetThresholds(output, portfolio, bar.Close, params.Chromosome, modelTargetWeight)
		rebalanceAllowed := rebalanceThresholdAllows(output, portfolio, bar.Close, params.Chromosome.RebalanceThreshold)
		if !hasAdoptedPracticalTargetWeight || rebalanceAllowed {
			adoptedPracticalTargetWeight = practicalModelTargetWeight
			hasAdoptedPracticalTargetWeight = true
		}
		output = applyRebalanceThreshold(output, portfolio, bar.Close, params.Chromosome.RebalanceThreshold)
		emptyReferenceOutput := sigmoiddca.Step(quant.StrategyInput{
			Symbol:     "BTCUSDT",
			Interval:   interval,
			Closes:     closes,
			Timestamps: timestamps,
			Portfolio: quant.PortfolioSnapshot{
				USDTBalance: portfolio.TotalEquity,
				TotalEquity: portfolio.TotalEquity,
			},
			RuntimeState: map[string]any{},
			Spawn:        params.Spawn,
		}, params)
		rawEmptyReferenceTargetWeight := diagnosticValue(emptyReferenceOutput.Diagnostics, "target_weight")
		emptyReferenceTargetWeight := totalTargetWeight(quant.PortfolioSnapshot{
			USDTBalance: portfolio.TotalEquity,
			TotalEquity: portfolio.TotalEquity,
		}, bar.Close, portfolio.TotalEquity, rawEmptyReferenceTargetWeight)
		_ = emptyReferenceOutput
		state = output.RuntimeState
		if usesNextOpenExecution(executionMode) {
			pendingOutput = output
			hasPendingOutput = true
		} else {
			if bar.OpenTime >= evalStartMs {
				tradeCount += countTradeIntents(output)
			}
			portfolio = applyBacktestOutputWithCosts(portfolio, output, bar.Close, costs)
		}

		equity := portfolio.USDTBalance + (portfolio.DeadBTC+portfolio.FloatBTC+portfolio.ColdSealedBTC)*bar.Close
		practicalTargetWeight := adoptedPracticalTargetWeight
		if TraceEnabled(activePathTraceMode(traceCfg), TraceModeFull) {
			tracePath(traceCfg, TraceModeFull, "strategy", "step.computed", "strategy step computed", map[string]any{
				"generation":      traceCfg.Generation,
				"individual":      traceCfg.Individual,
				"worker":          traceCfg.Worker,
				"window":          traceCfg.Window,
				"bar_index":       i,
				"open_time":       bar.OpenTime,
				"close":           bar.Close,
				"execution_mode":  executionMode,
				"total_equity":    equity,
				"usdt_balance":    portfolio.USDTBalance,
				"dead_btc":        portfolio.DeadBTC,
				"float_btc":       portfolio.FloatBTC,
				"cold_sealed_btc": portfolio.ColdSealedBTC,
				"intents":         len(output.Intents),
				"lot_transfers":   len(output.LotTransfers),
				"diagnostics":     output.Diagnostics,
			})
		}
		if bar.OpenTime >= evalStartMs {
			if len(nav) == 0 {
				evalInitial = equity
				actualEvalStart = bar.OpenTime
			}
			practicalTargetWeightChange := 0.0
			modelTargetWeightChange := 0.0
			emptyReferenceTargetWeightChange := 0.0
			if hasPrevTargetWeight {
				practicalTargetWeightChange = practicalTargetWeight - prevPracticalTargetWeight
				modelTargetWeightChange = modelTargetWeight - prevModelTargetWeight
				emptyReferenceTargetWeightChange = emptyReferenceTargetWeight - prevEmptyReferenceTargetWeight
			}
			nav = append(nav, equity)
			points = append(points, BacktestPoint{
				TimeMs:                           bar.OpenTime,
				Price:                            bar.Close,
				TotalEquity:                      equity,
				PracticalTargetWeight:            practicalTargetWeight,
				PracticalTargetWeightChange:      practicalTargetWeightChange,
				ModelTargetWeight:                modelTargetWeight,
				ModelTargetWeightChange:          modelTargetWeightChange,
				EmptyReferenceTargetWeight:       emptyReferenceTargetWeight,
				EmptyReferenceTargetWeightChange: emptyReferenceTargetWeightChange,
			})
			prevPracticalTargetWeight = practicalTargetWeight
			prevModelTargetWeight = modelTargetWeight
			prevEmptyReferenceTargetWeight = emptyReferenceTargetWeight
			hasPrevTargetWeight = true
		}
	}

	final := 0.0
	if len(nav) > 0 {
		final = nav[len(nav)-1]
	}
	metrics := BacktestMetrics{
		ROI:           quant.ModifiedDietzROI(evalInitial, final, evalFlows, actualEvalStart, lastBacktestPointTime(points)),
		MaxDrawdown:   quant.MaxDrawdown(nav),
		FinalEquity:   final,
		TotalInjected: evalInitial + evalInjected,
		TradeCount:    tradeCount,
	}
	return SigmoidDCAPathResult{
		Metrics: metrics,
		NAV:     points,
	}
}

func diagnosticValue(values map[string]float64, key string) float64 {
	if values == nil {
		return 0
	}
	value := values[key]
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func applyForceTargetThresholds(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, chromosome quant.Chromosome, modelTargetWeight float64) (quant.StrategyOutput, float64) {
	forcedTarget := modelTargetWeight
	switch {
	case chromosome.ForceFullThreshold < 1 && modelTargetWeight >= chromosome.ForceFullThreshold:
		forcedTarget = 1
	case chromosome.ForceEmptyThreshold > 0 && modelTargetWeight <= chromosome.ForceEmptyThreshold:
		forcedTarget = 0
	default:
		return output, modelTargetWeight
	}
	return forceTotalTargetOutput(output, portfolio, price, forcedTarget, chromosome.DustUSD), forcedTarget
}

func forceTotalTargetOutput(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, targetTotalWeight float64, dustUSD float64) quant.StrategyOutput {
	if price <= 0 {
		return output
	}
	totalEquity := portfolio.TotalEquity
	if totalEquity <= 0 {
		totalEquity = portfolio.USDTBalance + (portfolio.DeadBTC+portfolio.FloatBTC+portfolio.ColdSealedBTC)*price
	}
	if totalEquity <= 0 {
		return output
	}
	targetTotalWeight = quant.ClipFloat64(targetTotalWeight, 0, 1)
	currentAssetValue := (portfolio.DeadBTC + portfolio.FloatBTC + portfolio.ColdSealedBTC) * price
	targetAssetValue := totalEquity * targetTotalWeight
	deltaValue := targetAssetValue - currentAssetValue
	dust := dustUSD
	if dust <= 0 {
		dust = 10.1
	}

	forced := output
	forced.Intents = make([]quant.TradeIntent, 0, 1)
	forced.LotTransfers = nil
	if forced.Diagnostics == nil {
		forced.Diagnostics = map[string]float64{}
	}
	targetFloatingWeight := targetFloatingWeightForTotal(portfolio, price, totalEquity, targetTotalWeight)
	forced.Diagnostics["target_weight"] = targetFloatingWeight
	forced.Diagnostics["delta_weight"] = targetFloatingWeight - floatingWeight(portfolio, price, totalEquity)

	switch {
	case deltaValue > dust:
		amount := math.Min(deltaValue, portfolio.USDTBalance)
		if amount > dust {
			forced.Intents = append(forced.Intents, quant.TradeIntent{
				Action:     quant.ActionBuy,
				Engine:     quant.EngineMicro,
				AmountUSDT: amount,
				LotType:    quant.LotTypeFloating,
				Reason:     "forced practical target",
			})
		}
	case deltaValue < -dust:
		sellQty := math.Min(-deltaValue/price, portfolio.DeadBTC+portfolio.FloatBTC)
		if sellQty > portfolio.FloatBTC && portfolio.DeadBTC > 0 {
			forced.LotTransfers = append(forced.LotTransfers, quant.LotTransfer{
				FromLotType: quant.LotTypeDeadStack,
				ToLotType:   quant.LotTypeFloating,
				Amount:      math.Min(portfolio.DeadBTC, sellQty-portfolio.FloatBTC),
				Reason:      "forced practical target release",
			})
		}
		if sellQty*price > dust {
			forced.Intents = append(forced.Intents, quant.TradeIntent{
				Action:   quant.ActionSell,
				Engine:   quant.EngineMicro,
				QtyAsset: sellQty,
				LotType:  quant.LotTypeFloating,
				Reason:   "forced practical target",
			})
		}
	}
	return forced
}

func targetFloatingWeightForTotal(portfolio quant.PortfolioSnapshot, price float64, totalEquity float64, targetTotalWeight float64) float64 {
	if price <= 0 || totalEquity <= 0 {
		return 0
	}
	nonFloatingWeight := (portfolio.DeadBTC + portfolio.ColdSealedBTC) * price / totalEquity
	return quant.ClipFloat64(targetTotalWeight-nonFloatingWeight, 0, 1)
}

func countTradeIntents(output quant.StrategyOutput) int {
	count := 0
	for _, intent := range output.Intents {
		if intent.Action == quant.ActionBuy && intent.AmountUSDT > 0 {
			count++
		}
		if intent.Action == quant.ActionSell && intent.QtyAsset > 0 {
			count++
		}
	}
	return count
}

func floatingWeight(portfolio quant.PortfolioSnapshot, price float64, totalEquity float64) float64 {
	if price <= 0 {
		return 0
	}
	if totalEquity <= 0 {
		totalEquity = portfolio.TotalEquity
	}
	if totalEquity <= 0 {
		totalEquity = portfolio.USDTBalance + (portfolio.DeadBTC+portfolio.FloatBTC+portfolio.ColdSealedBTC)*price
	}
	if totalEquity <= 0 {
		return 0
	}
	return quant.ClipFloat64(portfolio.FloatBTC*price/totalEquity, 0, 1)
}

func totalAssetWeight(portfolio quant.PortfolioSnapshot, price float64, totalEquity float64) float64 {
	if price <= 0 {
		return 0
	}
	if totalEquity <= 0 {
		totalEquity = portfolio.TotalEquity
	}
	if totalEquity <= 0 {
		totalEquity = portfolio.USDTBalance + (portfolio.DeadBTC+portfolio.FloatBTC+portfolio.ColdSealedBTC)*price
	}
	if totalEquity <= 0 {
		return 0
	}
	totalAsset := portfolio.DeadBTC + portfolio.FloatBTC + portfolio.ColdSealedBTC
	return quant.ClipFloat64(totalAsset*price/totalEquity, 0, 1)
}

func totalTargetWeight(portfolio quant.PortfolioSnapshot, price float64, totalEquity float64, targetFloatingWeight float64) float64 {
	if price <= 0 {
		return 0
	}
	if totalEquity <= 0 {
		totalEquity = portfolio.TotalEquity
	}
	if totalEquity <= 0 {
		totalEquity = portfolio.USDTBalance + (portfolio.DeadBTC+portfolio.FloatBTC+portfolio.ColdSealedBTC)*price
	}
	if totalEquity <= 0 {
		return 0
	}
	nonFloatingWeight := (portfolio.DeadBTC + portfolio.ColdSealedBTC) * price / totalEquity
	return quant.ClipFloat64(nonFloatingWeight+quant.ClipFloat64(targetFloatingWeight, 0, 1), 0, 1)
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

func lastBacktestPointTime(points []BacktestPoint) int64 {
	if len(points) == 0 {
		return 0
	}
	return points[len(points)-1].TimeMs
}

func applyRebalanceThreshold(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, threshold float64) quant.StrategyOutput {
	if rebalanceThresholdAllows(output, portfolio, price, threshold) {
		return output
	}

	filtered := output
	filtered.Intents = make([]quant.TradeIntent, 0, len(output.Intents))
	for _, intent := range output.Intents {
		if intent.Engine == quant.EngineMicro {
			continue
		}
		filtered.Intents = append(filtered.Intents, intent)
	}
	filtered.LotTransfers = nil
	return filtered
}

func rebalanceThresholdAllows(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, threshold float64) bool {
	if threshold <= 0 || price <= 0 {
		return true
	}
	targetWeight := diagnosticValue(output.Diagnostics, "target_weight")
	totalEquity := portfolio.TotalEquity
	if totalEquity <= 0 {
		totalEquity = portfolio.USDTBalance + (portfolio.DeadBTC+portfolio.FloatBTC+portfolio.ColdSealedBTC)*price
	}
	if totalEquity <= 0 {
		return true
	}
	currentWeight := floatingWeight(portfolio, price, totalEquity)
	return math.Abs(targetWeight-currentWeight) >= threshold
}

func applyBacktestOutput(portfolio quant.PortfolioSnapshot, output quant.StrategyOutput, price float64) quant.PortfolioSnapshot {
	return applyBacktestOutputWithCosts(portfolio, output, price, quant.ExecutionCostConfig{})
}

func applyBacktestOutputWithCosts(portfolio quant.PortfolioSnapshot, output quant.StrategyOutput, price float64, costs quant.ExecutionCostConfig) quant.PortfolioSnapshot {
	for _, transfer := range output.LotTransfers {
		if transfer.FromLotType == quant.LotTypeDeadStack && transfer.ToLotType == quant.LotTypeFloating {
			amount := math.Min(transfer.Amount, portfolio.DeadBTC)
			portfolio.DeadBTC -= amount
			portfolio.FloatBTC += amount
		}
	}
	for _, intent := range output.Intents {
		switch {
		case intent.Action == quant.ActionBuy && intent.AmountUSDT > 0 && price > 0:
			amount := math.Min(intent.AmountUSDT, portfolio.USDTBalance)
			qty, spent := quant.BuyQuantityForCash(amount, price, costs)
			if qty <= 0 || spent <= 0 {
				continue
			}
			portfolio.USDTBalance -= spent
			if intent.LotType == quant.LotTypeDeadStack {
				portfolio.DeadBTC += qty
			} else {
				portfolio.FloatBTC += qty
			}
		case intent.Action == quant.ActionSell && intent.QtyAsset > 0:
			qty := math.Min(intent.QtyAsset, portfolio.FloatBTC)
			proceeds := quant.SellProceedsForQuantity(qty, price, costs)
			if qty <= 0 || proceeds <= 0 {
				continue
			}
			portfolio.FloatBTC -= qty
			portfolio.USDTBalance += proceeds
		}
	}
	return portfolio
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

func barYearMonth(bar quant.Bar) (int, time.Month) {
	t := time.UnixMilli(bar.OpenTime).UTC()
	return t.Year(), t.Month()
}
