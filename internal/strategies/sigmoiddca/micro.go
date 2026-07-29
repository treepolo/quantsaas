package sigmoiddca

import (
	"math"

	"quantsaas/internal/quant"
)

func computeMicroIntent(input quant.StrategyInput, params Params, market quant.MarketState, totalEquity, spendableUSDT float64) (quant.TradeIntent, quant.MicroDecisionOutput) {
	c := params.Chromosome
	price := latestClose(input)

	decision := quant.ComputeMicroDecisionV4(quant.MicroDecisionInput{
		Closes:                 input.Closes,
		Price:                  price,
		TotalEquity:            totalEquity,
		FloatBTC:               input.Portfolio.FloatBTC,
		SpendableUSDT:          spendableUSDT,
		WMean:                  c.WMean,
		WMomentum:              c.WMomentum,
		WBreakout:              c.WBreakout,
		SigmaFloor:             0.001,
		Beta:                   c.Beta,
		Gamma:                  c.Gamma,
		MarketBetaMultiplier:   market.BetaMultiplier,
		VolatilityRatio:        market.VolatilityRatio,
		DustUSD:                c.DustUSD,
		WedgeDeltaThreshold:    c.WedgeDeltaThreshold,
		WedgeVolRatioThreshold: c.WedgeVolRatioThreshold,
		IsQuiet:                market.IsQuiet,
		DisableDustFilter:      params.DisableMinimumTrade || params.FloatingOnly(),
		AISignal:               input.AISignal,
	})

	if decision.Action == "" || decision.OrderUSDT == 0 {
		return quant.TradeIntent{}, decision
	}

	intent := quant.TradeIntent{
		Action:  decision.Action,
		Engine:  quant.EngineMicro,
		Symbol:  input.Symbol,
		LotType: quant.LotTypeFloating,
		Reason:  "sigmoid dynamic balance",
	}
	if decision.Action == quant.ActionBuy {
		intent.AmountUSDT = math.Abs(decision.OrderUSDT)
	} else {
		intent.QtyAsset = math.Abs(decision.OrderUSDT) / price
	}
	return intent, decision
}
