package ga

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/fnv"
	"math"

	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

type SigmoidDCAEvolvable struct{}

func NewSigmoidDCAEvolvable() SigmoidDCAEvolvable {
	return SigmoidDCAEvolvable{}
}

func (SigmoidDCAEvolvable) StrategyID() string {
	return sigmoiddca.StrategyID
}

func (SigmoidDCAEvolvable) Sample(rng RandomSource) Gene {
	c := quant.Chromosome{
		MicroReservePct:        sampleRange(rng, "micro_reserve_pct"),
		Beta:                   sampleRange(rng, "beta"),
		Gamma:                  sampleRange(rng, "gamma"),
		WMean:                  sampleRange(rng, "w_mean"),
		WMomentum:              sampleRange(rng, "w_momentum"),
		WBreakout:              sampleRange(rng, "w_breakout"),
		DustUSD:                sampleRange(rng, "dust_usd"),
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

func (e SigmoidDCAEvolvable) Evaluate(ctx context.Context, g Gene, plan EvaluablePlan) (FitnessResult, error) {
	c := asChromosome(g)
	result := FitnessResult{}
	for i, window := range plan.Windows {
		if err := ctx.Err(); err != nil {
			return FitnessResult{}, err
		}
		metrics := RunSigmoidDCASingleBacktest(window.Bars, window.EvalStartMs, c, plan.Spawn)
		baseline := plan.DCABaselines[i]
		alpha := metrics.ROI - baseline.ROI
		score := alpha - 1.5*math.Max(0, metrics.MaxDrawdown-baseline.MaxDrawdown)
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
			return result, nil
		}
		result.ScoreTotal += window.Weight * score
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

func (SigmoidDCAEvolvable) EncodeResult(g Gene, spawn *quant.SpawnPoint) ([]byte, error) {
	params := sigmoiddca.DefaultParams()
	params.Chromosome = quant.ClampChromosome(asChromosome(g))
	if spawn != nil {
		params.Spawn = *spawn
	}
	return json.Marshal(params)
}

func (SigmoidDCAEvolvable) Verify(ctx context.Context, g Gene, spawn *quant.SpawnPoint, bars []quant.Bar, _ float64, _ float64) (BacktestMetrics, error) {
	if err := ctx.Err(); err != nil {
		return BacktestMetrics{}, err
	}
	return RunSigmoidDCASingleBacktest(bars, firstEvalStart(bars), asChromosome(g), spawn), nil
}

func RunSigmoidDCASingleBacktest(bars []quant.Bar, evalStartMs int64, chromosome quant.Chromosome, spawn *quant.SpawnPoint) BacktestMetrics {
	if len(bars) == 0 || bars[0].Close <= 0 {
		return BacktestMetrics{}
	}

	params := sigmoiddca.DefaultParams()
	params.Chromosome = quant.ClampChromosome(chromosome)
	if spawn != nil {
		params.Spawn = *spawn
	}

	portfolio := quant.PortfolioSnapshot{USDTBalance: params.Spawn.Policy.InitialUSDT}
	if portfolio.USDTBalance <= 0 {
		portfolio.USDTBalance = 1000
	}
	totalInjected := portfolio.USDTBalance
	state := map[string]any{}
	var nav []float64
	lastYear, lastMonth := barYearMonth(bars[0])

	for i, bar := range bars {
		if bar.Close <= 0 {
			continue
		}
		year, month := barYearMonth(bar)
		if i > 0 && (year != lastYear || month != lastMonth) && params.Spawn.Policy.MonthlyInjectUSDT > 0 {
			portfolio.USDTBalance += params.Spawn.Policy.MonthlyInjectUSDT
			totalInjected += params.Spawn.Policy.MonthlyInjectUSDT
			lastYear, lastMonth = year, month
		}

		closes := make([]float64, 0, i+1)
		timestamps := make([]int64, 0, i+1)
		for _, b := range bars[:i+1] {
			closes = append(closes, b.Close)
			timestamps = append(timestamps, b.OpenTime)
		}
		portfolio.TotalEquity = portfolio.USDTBalance +
			(portfolio.DeadBTC+portfolio.FloatBTC+portfolio.ColdSealedBTC)*bar.Close
		output := sigmoiddca.Step(quant.StrategyInput{
			Symbol:       "BTCUSDT",
			Interval:     "1d",
			Closes:       closes,
			Timestamps:   timestamps,
			Portfolio:    portfolio,
			RuntimeState: state,
			Spawn:        params.Spawn,
		}, params)
		state = output.RuntimeState
		portfolio = applyBacktestOutput(portfolio, output, bar.Close)

		equity := portfolio.USDTBalance + (portfolio.DeadBTC+portfolio.FloatBTC+portfolio.ColdSealedBTC)*bar.Close
		if bar.OpenTime >= evalStartMs {
			nav = append(nav, equity)
		}
	}

	final := 0.0
	if len(nav) > 0 {
		final = nav[len(nav)-1]
	}
	roi := 0.0
	if totalInjected > 0 {
		roi = final/totalInjected - 1
	}
	return BacktestMetrics{
		ROI:           roi,
		MaxDrawdown:   quant.MaxDrawdown(nav),
		FinalEquity:   final,
		TotalInjected: totalInjected,
	}
}

func applyBacktestOutput(portfolio quant.PortfolioSnapshot, output quant.StrategyOutput, price float64) quant.PortfolioSnapshot {
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
			qty := amount / price
			portfolio.USDTBalance -= amount
			if intent.LotType == quant.LotTypeDeadStack {
				portfolio.DeadBTC += qty
			} else {
				portfolio.FloatBTC += qty
			}
		case intent.Action == quant.ActionSell && intent.QtyAsset > 0:
			qty := math.Min(intent.QtyAsset, portfolio.FloatBTC)
			portfolio.FloatBTC -= qty
			portfolio.USDTBalance += qty * price
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

func barYearMonth(bar quant.Bar) (int, int) {
	const monthMs = int64(30 * 24 * 60 * 60 * 1000)
	return int(bar.OpenTime / (12 * monthMs)), int((bar.OpenTime/monthMs)%12 + 1)
}
