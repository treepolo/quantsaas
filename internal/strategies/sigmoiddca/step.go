package sigmoiddca

import (
	"math"

	"quantsaas/internal/quant"
)

const (
	// RequiredHistoryBars is the finite amount of history needed before the
	// first evaluated bar. Research callers consume this metadata instead of
	// duplicating or guessing the strategy's indicator windows.
	RequiredHistoryBars = quant.MicroVolRatioLongBars
	minStrategyBars     = RequiredHistoryBars + 1
)

func Step(input quant.StrategyInput, params Params) quant.StrategyOutput {
	state := DecodeRuntimeState(input.RuntimeState)
	if len(input.Closes) < minStrategyBars || len(input.Timestamps) == 0 {
		return outputWithState(state)
	}

	params.Chromosome = quant.ClampChromosome(params.Chromosome)
	price := latestClose(input)
	if price <= 0 {
		return outputWithState(state)
	}

	totalEquity := input.Portfolio.TotalEquity
	if totalEquity <= 0 {
		totalEquity = input.Portfolio.USDTBalance +
			(input.Portfolio.DeadBTC+input.Portfolio.FloatBTC+input.Portfolio.ColdSealedBTC)*price
	}
	reserveFloor := totalEquity * params.Chromosome.MicroReservePct
	if params.FloatingOnly() {
		reserveFloor = 0
	}
	spendableUSDT := math.Max(0, input.Portfolio.USDTBalance-reserveFloor)

	market := quant.ComputeMarketState(input.Closes)
	microIntent, microDecision := computeMicroIntent(input, params, market, totalEquity, spendableUSDT)
	macroIntent := quant.TradeIntent{}
	macroYearMonth := ""
	macroDiag := map[string]float64{}
	var transfers []quant.LotTransfer
	if !params.FloatingOnly() {
		macroIntent, macroYearMonth, macroDiag = computeMacroIntent(input, state, params, market, spendableUSDT, reserveFloor)
		transfers = computeDeadRelease(input, params, market, microDecision)
	}

	intents := make([]quant.TradeIntent, 0, 2)
	if macroIntent.Action != "" {
		intents = append(intents, macroIntent)
	}
	if microIntent.Action != "" {
		intents = append(intents, microIntent)
	}

	nextState := RuntimeState{
		LastProcessedBarTime: latestTimestamp(input),
		LastMacroYearMonth:   state.LastMacroYearMonth,
		LastMarketState:      market.State,
	}
	if macroYearMonth != "" {
		nextState.LastMacroYearMonth = macroYearMonth
	}

	diagnostics := map[string]float64{
		"total_equity":       totalEquity,
		"reserve_floor":      reserveFloor,
		"spendable_usdt":     spendableUSDT,
		"current_weight":     microDecision.CurrentWeight,
		"target_weight":      microDecision.TargetWeight,
		"delta_weight":       microDecision.DeltaWeight,
		"signal":             microDecision.Signal,
		"volatility_ratio":   microDecision.VolatilityRatio,
		"market_beta":        market.BetaMultiplier,
		"market_trend_slope": market.TrendSlope,
		"market_drawdown":    market.DrawdownRatio,
	}
	for k, v := range macroDiag {
		diagnostics[k] = v
	}

	return quant.StrategyOutput{
		Intents:      intents,
		LotTransfers: transfers,
		RuntimeState: EncodeRuntimeState(nextState),
		Diagnostics:  diagnostics,
	}
}

func outputWithState(state RuntimeState) quant.StrategyOutput {
	return quant.StrategyOutput{
		RuntimeState: EncodeRuntimeState(state),
		Diagnostics:  map[string]float64{},
	}
}

func latestClose(input quant.StrategyInput) float64 {
	if len(input.Closes) == 0 {
		return 0
	}
	return input.Closes[len(input.Closes)-1]
}

func latestTimestamp(input quant.StrategyInput) int64 {
	if len(input.Timestamps) == 0 {
		return 0
	}
	return input.Timestamps[len(input.Timestamps)-1]
}
