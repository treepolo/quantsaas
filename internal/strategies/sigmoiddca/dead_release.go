package sigmoiddca

import (
	"math"
	"time"

	"quantsaas/internal/quant"
)

func computeDeadRelease(input quant.StrategyInput, params Params, market quant.MarketState, micro quant.MicroDecisionOutput) []quant.LotTransfer {
	price := latestClose(input)
	if price <= 0 || micro.Action != quant.ActionSell || micro.OrderUSDT >= 0 {
		return nil
	}

	targetSellBTC := math.Abs(micro.OrderUSDT) / price
	if targetSellBTC <= 0 {
		return nil
	}

	var transfers []quant.LotTransfer
	now := time.UnixMilli(latestTimestamp(input)).UTC()
	c := params.Chromosome

	if market.State == quant.MarketStateBullTrend || market.State == quant.MarketStateShock {
		_, released := quant.SoftRelease(input.Lots, now, c.SoftReleaseMonths, c.SoftReleasePct, targetSellBTC)
		if released == 0 && len(input.Lots) == 0 {
			released = math.Min(input.Portfolio.DeadBTC*c.SoftReleasePct, targetSellBTC)
		}
		if released > 0 {
			transfers = append(transfers, quant.LotTransfer{
				FromLotType: quant.LotTypeDeadStack,
				ToLotType:   quant.LotTypeFloating,
				Amount:      released,
				Reason:      "soft release before micro sell",
			})
		}
	}

	availableFloat := input.Portfolio.FloatBTC
	for _, transfer := range transfers {
		availableFloat += transfer.Amount
	}
	if availableFloat >= targetSellBTC {
		return transfers
	}

	required := targetSellBTC - availableFloat
	_, released := quant.HardRelease(input.Lots, required, c.HardReleaseMaxPct)
	if released == 0 && len(input.Lots) == 0 {
		released = math.Min(input.Portfolio.DeadBTC*c.HardReleaseMaxPct, required)
	}
	if released > 0 {
		transfers = append(transfers, quant.LotTransfer{
			FromLotType: quant.LotTypeDeadStack,
			ToLotType:   quant.LotTypeFloating,
			Amount:      released,
			Reason:      "hard release to cover micro sell",
		})
	}

	return transfers
}
