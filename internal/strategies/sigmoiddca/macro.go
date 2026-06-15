package sigmoiddca

import "quantsaas/internal/quant"

func computeMacroIntent(input quant.StrategyInput, state RuntimeState, params Params, market quant.MarketState, spendableUSDT, reserveFloor float64) (quant.TradeIntent, string, map[string]float64) {
	c := params.Chromosome
	decision := quant.ComputeMacroDecision(quant.MacroDecisionInput{
		Symbol:              input.Symbol,
		CurrentTimeMs:       latestTimestamp(input),
		LastMacroYearMonth:  state.LastMacroYearMonth,
		USDTBalance:         input.Portfolio.USDTBalance,
		SpendableUSDT:       spendableUSDT,
		ReserveFloor:        reserveFloor,
		MonthlyInjectUSDT:   params.Spawn.Policy.MonthlyInjectUSDT,
		DustUSD:             c.DustUSD,
		Market:              market,
		MacroBearMultiplier: c.MacroBearMultiplier,
		MacroBullMultiplier: c.MacroBullMultiplier,
		ExtraDeployPct:      c.ExtraDeployPct,
	})

	diag := map[string]float64{
		"macro_regime_multiplier": decision.RegimeMultiple,
	}
	if !decision.ShouldBuy {
		return quant.TradeIntent{}, decision.YearMonth, diag
	}

	return quant.TradeIntent{
		Action:     decision.Action,
		Engine:     decision.Engine,
		Symbol:     decision.Symbol,
		AmountUSDT: decision.AmountUSDT,
		LotType:    decision.LotType,
		Reason:     decision.Reason,
	}, decision.YearMonth, diag
}
