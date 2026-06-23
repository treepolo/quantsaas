package ga

import (
	"testing"

	"quantsaas/internal/quant"
)

func TestApplyRebalanceThresholdFiltersOnlyMicroOrders(t *testing.T) {
	output := quant.StrategyOutput{
		Intents: []quant.TradeIntent{
			{Action: quant.ActionBuy, Engine: quant.EngineMacro, AmountUSDT: 100, LotType: quant.LotTypeDeadStack},
			{Action: quant.ActionBuy, Engine: quant.EngineMicro, AmountUSDT: 50, LotType: quant.LotTypeFloating},
		},
		LotTransfers: []quant.LotTransfer{
			{FromLotType: quant.LotTypeDeadStack, ToLotType: quant.LotTypeFloating, Amount: 1},
		},
		Diagnostics: map[string]float64{"target_weight": 0.05},
	}
	portfolio := quant.PortfolioSnapshot{
		USDTBalance: 1000,
		FloatBTC:    0.04,
		TotalEquity: 1000,
	}

	filtered := applyRebalanceThreshold(output, portfolio, 1000, 0.02)

	if len(filtered.Intents) != 1 {
		t.Fatalf("intent count = %d, want 1", len(filtered.Intents))
	}
	if filtered.Intents[0].Engine != quant.EngineMacro {
		t.Fatalf("remaining intent engine = %s, want macro", filtered.Intents[0].Engine)
	}
	if len(filtered.LotTransfers) != 0 {
		t.Fatalf("lot transfers = %d, want 0", len(filtered.LotTransfers))
	}
}

func TestApplyRebalanceThresholdKeepsMicroOrderWhenGapPasses(t *testing.T) {
	output := quant.StrategyOutput{
		Intents:     []quant.TradeIntent{{Action: quant.ActionSell, Engine: quant.EngineMicro, QtyAsset: 0.02, LotType: quant.LotTypeFloating}},
		Diagnostics: map[string]float64{"target_weight": 0.05},
	}
	portfolio := quant.PortfolioSnapshot{
		USDTBalance: 1000,
		FloatBTC:    0.20,
		TotalEquity: 1000,
	}

	filtered := applyRebalanceThreshold(output, portfolio, 1000, 0.02)

	if len(filtered.Intents) != 1 {
		t.Fatalf("intent count = %d, want 1", len(filtered.Intents))
	}
	if filtered.Intents[0].Engine != quant.EngineMicro {
		t.Fatalf("remaining intent engine = %s, want micro", filtered.Intents[0].Engine)
	}
}
