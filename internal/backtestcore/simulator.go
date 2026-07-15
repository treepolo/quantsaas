package backtestcore

import (
	"math"

	"quantsaas/internal/quant"
)

type SimulatorConfig struct {
	Costs           quant.ExecutionCostConfig
	MinimumTradeUSD float64
	MinimumAssetQty float64
}

type Simulator struct {
	portfolio quant.PortfolioSnapshot
	config    SimulatorConfig
}

func NewSimulator(initialCash float64, initialColdAsset float64, config SimulatorConfig) *Simulator {
	config.Costs = quant.NormalizeExecutionCosts(config.Costs)
	if config.MinimumTradeUSD < 0 || math.IsNaN(config.MinimumTradeUSD) || math.IsInf(config.MinimumTradeUSD, 0) {
		config.MinimumTradeUSD = 0
	}
	if config.MinimumAssetQty < 0 || math.IsNaN(config.MinimumAssetQty) || math.IsInf(config.MinimumAssetQty, 0) {
		config.MinimumAssetQty = 0
	}
	return &Simulator{
		portfolio: quant.PortfolioSnapshot{
			USDTBalance:   initialCash,
			ColdSealedBTC: initialColdAsset,
		},
		config: config,
	}
}

func ApplyStrategyOutput(portfolio quant.PortfolioSnapshot, output quant.StrategyOutput, price float64, config SimulatorConfig) (quant.PortfolioSnapshot, TradeSummary) {
	simulator := NewSimulator(0, 0, config)
	simulator.portfolio = portfolio
	summary := simulator.Execute(output, price)
	return simulator.Portfolio(price), summary
}

func (s *Simulator) Portfolio(price float64) quant.PortfolioSnapshot {
	portfolio := s.portfolio
	portfolio.TotalEquity = portfolio.USDTBalance + totalAssetQuantity(portfolio)*price
	return portfolio
}

func (s *Simulator) Contribute(amount float64) {
	if amount > 0 && !math.IsNaN(amount) && !math.IsInf(amount, 0) {
		s.portfolio.USDTBalance += amount
	}
}

func (s *Simulator) Execute(output quant.StrategyOutput, price float64) TradeSummary {
	summary := TradeSummary{}
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return summary
	}
	for _, transfer := range output.LotTransfers {
		if transfer.FromLotType != quant.LotTypeDeadStack || transfer.ToLotType != quant.LotTypeFloating || transfer.Amount <= 0 {
			continue
		}
		amount := math.Min(transfer.Amount, s.portfolio.DeadBTC)
		s.portfolio.DeadBTC -= amount
		s.portfolio.FloatBTC += amount
	}
	for _, intent := range output.Intents {
		switch {
		case intent.Action == quant.ActionBuy && intent.AmountUSDT > 0:
			requested := math.Min(intent.AmountUSDT, s.portfolio.USDTBalance)
			if requested <= s.config.MinimumTradeUSD {
				continue
			}
			qty, spent := quant.BuyQuantityForCash(requested, price, s.config.Costs)
			if qty <= 0 || spent <= 0 || qty < s.config.MinimumAssetQty {
				continue
			}
			s.portfolio.USDTBalance -= spent
			if intent.LotType == quant.LotTypeDeadStack {
				s.portfolio.DeadBTC += qty
			} else {
				s.portfolio.FloatBTC += qty
			}
			fillPrice := price * (1 + s.config.Costs.SpreadRate)
			fillNotional := qty * fillPrice
			costs := CostSummary{
				FeeCost:      fillNotional * s.config.Costs.FeeRate,
				SlippageCost: qty * (fillPrice - price),
			}
			costs.TotalCost = costs.FeeCost + costs.SlippageCost
			summary.TradeCount++
			summary.BuyCount++
			summary.BuyNotional += qty * price
			summary.Costs.Add(costs)
		case intent.Action == quant.ActionSell && intent.QtyAsset > 0:
			qty := math.Min(intent.QtyAsset, s.portfolio.FloatBTC)
			if qty <= 0 || qty < s.config.MinimumAssetQty || qty*price <= s.config.MinimumTradeUSD {
				continue
			}
			proceeds := quant.SellProceedsForQuantity(qty, price, s.config.Costs)
			if proceeds <= 0 {
				continue
			}
			s.portfolio.FloatBTC -= qty
			s.portfolio.USDTBalance += proceeds
			fillPrice := price * (1 - s.config.Costs.SpreadRate)
			fillNotional := qty * fillPrice
			costs := CostSummary{
				FeeCost:      fillNotional * s.config.Costs.FeeRate,
				SlippageCost: qty * (price - fillPrice),
			}
			costs.TotalCost = costs.FeeCost + costs.SlippageCost
			summary.TradeCount++
			summary.SellCount++
			summary.SellNotional += qty * price
			summary.Costs.Add(costs)
		}
	}
	return summary
}

func (s *Simulator) RebalanceToExposure(targetWeight float64, price float64) TradeSummary {
	if price <= 0 || math.IsNaN(targetWeight) || math.IsInf(targetWeight, 0) {
		return TradeSummary{}
	}
	targetWeight = quant.ClipFloat64(targetWeight, 0, 1)
	portfolio := s.Portfolio(price)
	if portfolio.TotalEquity <= 0 {
		return TradeSummary{}
	}
	currentAssetValue := totalAssetQuantity(portfolio) * price
	targetAssetValue := portfolio.TotalEquity * targetWeight
	delta := targetAssetValue - currentAssetValue
	if math.Abs(delta) <= s.config.MinimumTradeUSD {
		return TradeSummary{}
	}
	output := quant.StrategyOutput{}
	if delta > 0 {
		output.Intents = []quant.TradeIntent{{
			Action:     quant.ActionBuy,
			Engine:     quant.EngineMicro,
			AmountUSDT: math.Min(delta, portfolio.USDTBalance),
			LotType:    quant.LotTypeFloating,
		}}
	} else {
		return s.sellAnyAsset(math.Min(-delta/price, totalAssetQuantity(portfolio)), price, true)
	}
	return s.Execute(output, price)
}

// LiquidateAll is reserved for an outer portfolio overlay such as the P07
// risk filter. It liquidates every lot, including otherwise sealed holdings,
// without changing strategy state or lot semantics in the practical model.
func (s *Simulator) LiquidateAll(price float64) TradeSummary {
	return s.sellAnyAsset(totalAssetQuantity(s.Portfolio(price)), price, false)
}

func (s *Simulator) sellAnyAsset(qty float64, price float64, respectMinimum bool) TradeSummary {
	if price <= 0 || qty <= 0 || math.IsNaN(qty) || math.IsInf(qty, 0) {
		return TradeSummary{}
	}
	available := totalAssetQuantity(s.portfolio)
	qty = math.Min(qty, available)
	if respectMinimum && (qty < s.config.MinimumAssetQty || qty*price <= s.config.MinimumTradeUSD) {
		return TradeSummary{}
	}
	proceeds := quant.SellProceedsForQuantity(qty, price, s.config.Costs)
	if proceeds <= 0 {
		return TradeSummary{}
	}
	remaining := qty
	fromFloating := math.Min(remaining, s.portfolio.FloatBTC)
	s.portfolio.FloatBTC -= fromFloating
	remaining -= fromFloating
	fromDead := math.Min(remaining, s.portfolio.DeadBTC)
	s.portfolio.DeadBTC -= fromDead
	remaining -= fromDead
	fromSealed := math.Min(remaining, s.portfolio.ColdSealedBTC)
	s.portfolio.ColdSealedBTC -= fromSealed
	s.portfolio.USDTBalance += proceeds
	fillPrice := price * (1 - s.config.Costs.SpreadRate)
	fillNotional := qty * fillPrice
	costs := CostSummary{
		FeeCost:      fillNotional * s.config.Costs.FeeRate,
		SlippageCost: qty * (price - fillPrice),
	}
	costs.TotalCost = costs.FeeCost + costs.SlippageCost
	return TradeSummary{
		TradeCount:   1,
		SellCount:    1,
		SellNotional: qty * price,
		Costs:        costs,
	}
}
