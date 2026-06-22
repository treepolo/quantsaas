package ga

import (
	"math"
	"testing"

	"quantsaas/internal/quant"
)

func TestApplyBacktestOutputWithCosts(t *testing.T) {
	portfolio := quant.PortfolioSnapshot{USDTBalance: 1000}
	output := quant.StrategyOutput{
		Intents: []quant.TradeIntent{{
			Action:     quant.ActionBuy,
			AmountUSDT: 1000,
			LotType:    quant.LotTypeFloating,
		}},
	}

	portfolio = applyBacktestOutputWithCosts(portfolio, output, 100, quant.ExecutionCostConfig{
		FeeRate:    0.01,
		SpreadRate: 0.01,
	})

	wantQty := 1000 / (100 * 1.01 * 1.01)
	if math.Abs(portfolio.FloatBTC-wantQty) > 1e-9 {
		t.Fatalf("float asset = %.12f, want %.12f", portfolio.FloatBTC, wantQty)
	}
	if math.Abs(portfolio.USDTBalance) > 1e-9 {
		t.Fatalf("cash = %.12f, want 0", portfolio.USDTBalance)
	}

	portfolio = applyBacktestOutputWithCosts(portfolio, quant.StrategyOutput{
		Intents: []quant.TradeIntent{{
			Action:   quant.ActionSell,
			QtyAsset: portfolio.FloatBTC,
			LotType:  quant.LotTypeFloating,
		}},
	}, 100, quant.ExecutionCostConfig{FeeRate: 0.01, SpreadRate: 0.01})

	wantCash := wantQty * 100 * 0.99 * 0.99
	if math.Abs(portfolio.USDTBalance-wantCash) > 1e-9 {
		t.Fatalf("cash after sell = %.12f, want %.12f", portfolio.USDTBalance, wantCash)
	}
	if math.Abs(portfolio.FloatBTC) > 1e-9 {
		t.Fatalf("float asset after sell = %.12f, want 0", portfolio.FloatBTC)
	}
}
