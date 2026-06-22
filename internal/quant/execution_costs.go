package quant

import "math"

type ExecutionCostConfig struct {
	FeeRate    float64 `json:"fee_rate"`
	SpreadRate float64 `json:"spread_rate"`
}

func NormalizeExecutionCosts(costs ExecutionCostConfig) ExecutionCostConfig {
	if math.IsNaN(costs.FeeRate) || math.IsInf(costs.FeeRate, 0) || costs.FeeRate < 0 {
		costs.FeeRate = 0
	}
	if math.IsNaN(costs.SpreadRate) || math.IsInf(costs.SpreadRate, 0) || costs.SpreadRate < 0 {
		costs.SpreadRate = 0
	}
	return costs
}

func BuyQuantityForCash(cashBudget float64, price float64, costs ExecutionCostConfig) (float64, float64) {
	costs = NormalizeExecutionCosts(costs)
	if cashBudget <= 0 || price <= 0 {
		return 0, 0
	}
	fillPrice := price * (1 + costs.SpreadRate)
	denominator := fillPrice * (1 + costs.FeeRate)
	if denominator <= 0 {
		return 0, 0
	}
	qty := cashBudget / denominator
	spent := qty * denominator
	if spent > cashBudget && spent-cashBudget < 1e-9 {
		spent = cashBudget
	}
	return qty, spent
}

func SellProceedsForQuantity(qty float64, price float64, costs ExecutionCostConfig) float64 {
	costs = NormalizeExecutionCosts(costs)
	if qty <= 0 || price <= 0 {
		return 0
	}
	fillPrice := price * (1 - costs.SpreadRate)
	if fillPrice <= 0 {
		return 0
	}
	gross := qty * fillPrice
	return gross * (1 - costs.FeeRate)
}
