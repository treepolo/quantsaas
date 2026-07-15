package backtestcore

import (
	"fmt"
	"math"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

type SigmoidDCARequest struct {
	Spec              Spec
	Bars              []quant.Bar
	Params            sigmoiddca.Params
	ParameterProvider ParameterProvider
	Hooks             Hooks
}

func RunSigmoidDCA(request SigmoidDCARequest) (Result, error) {
	spec, err := normalizeSpec(request.Spec, request.Bars, RunnerSigmoidDCA)
	if err != nil {
		return Result{}, err
	}
	if spec.ExecutionMode == ExecutionModeOpenBuyCloseSell {
		return Result{}, fmt.Errorf("SigmoidDCA runner 不支援每天開盤買、收盤賣模式")
	}
	params := request.Params
	params.Chromosome = quant.ClampChromosome(params.Chromosome)
	if err := quant.ValidateChromosome(params.Chromosome); err != nil {
		return Result{}, err
	}
	params.PositionStructure = sigmoiddca.NormalizePositionStructure(spec.PositionStructure)
	spec.PositionStructure = params.PositionStructure
	params.Spawn.Policy.InitialUSDT = spec.InitialCapital
	params.Spawn.Policy.MonthlyInjectUSDT = spec.MonthlyContribution
	params.Spawn.Policy.ColdSealedBTC = spec.InitialAssetQuantity

	simulator := NewSimulator(spec.InitialCapital, spec.InitialAssetQuantity, SimulatorConfig{
		Costs:           spec.Costs,
		MinimumTradeUSD: spec.MinimumTradeUSD,
		MinimumAssetQty: spec.MinimumAssetQuantity,
	})
	filteredSimulator := NewSimulator(spec.InitialCapital, spec.InitialAssetQuantity, SimulatorConfig{
		Costs:           spec.Costs,
		MinimumTradeUSD: spec.MinimumTradeUSD,
		MinimumAssetQty: spec.MinimumAssetQuantity,
	})
	longTermFilter := NewLongTermFilter(spec.LongTermFilter)
	state := map[string]any{}
	closes := make([]float64, 0, len(request.Bars))
	timestamps := make([]int64, 0, len(request.Bars))
	points := make([]NAVPoint, 0, len(request.Bars))
	evalFlows := make([]quant.TimedCashFlow, 0)
	evalInjected := 0.0
	evalInitial := 0.0
	actualEvalStart := int64(0)
	pendingOutput := quant.StrategyOutput{}
	hasPendingOutput := false
	pendingPracticalAdjustment := false
	pendingFilterSignal := ""
	filterRiskOff := false
	filterDiverged := false
	filterObservation := LongTermFilterObservation{}
	prevModelTargetWeight := 0.0
	prevPracticalTargetWeight := 0.0
	prevEmptyReferenceTargetWeight := 0.0
	adoptedPracticalTargetWeight := 0.0
	hasAdoptedPracticalTargetWeight := false
	hasPrevTargetWeight := false
	tradeCount := 0
	costSummary := CostSummary{}
	practicalTradeCount := 0
	practicalCostSummary := CostSummary{}
	lastYear, lastMonth := barYearMonth(request.Bars[0])
	runStarted := false
	historyOnlyStarted := false

	for i, bar := range request.Bars {
		if bar.OpenTime < spec.StartTimeMs {
			continue
		}
		if bar.OpenTime > spec.EndTimeMs || bar.OpenTime > spec.EvaluationEndMs {
			break
		}
		if bar.Close <= 0 || math.IsNaN(bar.Close) || math.IsInf(bar.Close, 0) {
			continue
		}
		if !runStarted {
			lastYear, lastMonth = barYearMonth(bar)
			runStarted = true
		}
		if spec.PrefixMode == PrefixModeHistoryOnly && bar.OpenTime < spec.EvaluationStartMs {
			closes = append(closes, bar.Close)
			timestamps = append(timestamps, bar.OpenTime)
			filterObservation = longTermFilter.Observe(i, request.Bars)
			if filterObservation.Signal != "" {
				pendingFilterSignal = filterObservation.Signal
			}
			continue
		}
		if spec.PrefixMode == PrefixModeHistoryOnly && !historyOnlyStarted {
			lastYear, lastMonth = barYearMonth(bar)
			historyOnlyStarted = true
		}
		if request.Hooks.ComputeStep != nil {
			request.Hooks.ComputeStep(1)
		}

		year, month := barYearMonth(bar)
		contributedThisBar := false
		if i > 0 && (year != lastYear || month != lastMonth) && spec.MonthlyContribution > 0 {
			simulator.Contribute(spec.MonthlyContribution)
			filteredSimulator.Contribute(spec.MonthlyContribution)
			contributedThisBar = true
			if bar.OpenTime > spec.EvaluationStartMs {
				evalInjected += spec.MonthlyContribution
				evalFlows = append(evalFlows, quant.TimedCashFlow{TimeMs: bar.OpenTime, Amount: spec.MonthlyContribution})
			}
			lastYear, lastMonth = year, month
		}

		pointTrades := TradeSummary{}
		pointPracticalTrades := TradeSummary{}
		filterEvent := ""
		if spec.ExecutionMode == ExecutionModeCloseNextOpen && (hasPendingOutput || pendingFilterSignal != "" || (filterDiverged && !filterRiskOff && contributedThisBar)) {
			if bar.Open <= 0 || math.IsNaN(bar.Open) || math.IsInf(bar.Open, 0) {
				return Result{}, fmt.Errorf("%d 的開盤價無效，無法執行前一根 K 線訊號", bar.OpenTime)
			}
			practicalExecuted := TradeSummary{}
			if hasPendingOutput {
				practicalExecuted = simulator.Execute(pendingOutput, bar.Open)
			}
			practicalExecutionWeight := actualExposure(simulator.Portfolio(bar.Open), bar.Open)
			filteredExecuted := TradeSummary{}
			switch pendingFilterSignal {
			case LongTermFilterSignalEnter:
				filterRiskOff = true
				filterDiverged = true
				filterEvent = LongTermFilterSignalEnter
				filteredExecuted = filteredSimulator.LiquidateAll(bar.Open)
			case LongTermFilterSignalExit:
				filterRiskOff = false
				filterDiverged = true
				filterEvent = LongTermFilterSignalExit
				filteredExecuted = filteredSimulator.RebalanceToExposure(practicalExecutionWeight, bar.Open)
			default:
				if !filterRiskOff && (hasPendingOutput || contributedThisBar) {
					if filterDiverged {
						if pendingPracticalAdjustment || contributedThisBar {
							filteredExecuted = filteredSimulator.RebalanceToExposure(practicalExecutionWeight, bar.Open)
						}
					} else {
						filteredExecuted = filteredSimulator.Execute(pendingOutput, bar.Open)
					}
				}
			}
			if isEvaluationBar(spec, bar) {
				pointTrades.Add(filteredExecuted)
				tradeCount += filteredExecuted.TradeCount
				costSummary.Add(filteredExecuted.Costs)
				pointPracticalTrades.Add(practicalExecuted)
				practicalTradeCount += practicalExecuted.TradeCount
				practicalCostSummary.Add(practicalExecuted.Costs)
			}
			hasPendingOutput = false
			pendingFilterSignal = ""
		}

		closes = append(closes, bar.Close)
		timestamps = append(timestamps, bar.OpenTime)
		portfolio := simulator.Portfolio(bar.Close)
		effectiveParams := params
		var effectiveSnapshot *EffectiveParameters
		if request.ParameterProvider != nil {
			effective, providerErr := request.ParameterProvider(ParameterContext{
				Index:      i,
				Bar:        bar,
				Closes:     append([]float64(nil), closes...),
				Timestamps: append([]int64(nil), timestamps...),
			})
			if providerErr != nil {
				return Result{}, fmt.Errorf("%d 的每日有效參數不可用: %w", bar.OpenTime, providerErr)
			}
			if validationErr := validateEffectiveChromosome(effective.Chromosome); validationErr != nil {
				return Result{}, fmt.Errorf("%d 的每日有效參數無效: %w", bar.OpenTime, validationErr)
			}
			effectiveParams.Chromosome = effective.Chromosome
			effectiveCopy := effective
			effectiveSnapshot = &effectiveCopy
		}

		strategyInput := quant.StrategyInput{
			Symbol:       spec.Symbol,
			Interval:     spec.Interval,
			Closes:       closes,
			Timestamps:   timestamps,
			Portfolio:    portfolio,
			RuntimeState: state,
			Params:       chromosomeParameterMap(effectiveParams.Chromosome),
			Spawn:        effectiveParams.Spawn,
		}
		output := sigmoiddca.Step(strategyInput, effectiveParams)
		rawModelTargetWeight := diagnosticValue(output.Diagnostics, "target_weight")
		modelTargetWeight := totalTargetWeight(portfolio, bar.Close, portfolio.TotalEquity, rawModelTargetWeight)
		practicalModelTargetWeight := modelTargetWeight
		output, practicalModelTargetWeight = ApplyForceTargetThresholds(output, portfolio, bar.Close, effectiveParams.Chromosome, modelTargetWeight)
		rebalanceAllowed := RebalanceThresholdAllows(output, portfolio, bar.Close, effectiveParams.Chromosome.RebalanceThreshold)
		output = ApplyRebalanceThreshold(output, portfolio, bar.Close, effectiveParams.Chromosome.RebalanceThreshold)
		if !hasAdoptedPracticalTargetWeight {
			adoptedPracticalTargetWeight = totalAssetWeight(portfolio, bar.Close, portfolio.TotalEquity)
			hasAdoptedPracticalTargetWeight = true
		}
		if rebalanceAllowed && shouldAdoptPracticalTarget(output, portfolio, bar.Close, practicalModelTargetWeight) {
			adoptedPracticalTargetWeight = practicalModelTargetWeight
		}

		emptyPortfolio := quant.PortfolioSnapshot{
			USDTBalance: portfolio.TotalEquity,
			TotalEquity: portfolio.TotalEquity,
		}
		emptyReferenceOutput := sigmoiddca.Step(quant.StrategyInput{
			Symbol:     spec.Symbol,
			Interval:   spec.Interval,
			Closes:     closes,
			Timestamps: timestamps,
			Portfolio:  emptyPortfolio,
			Params:     chromosomeParameterMap(effectiveParams.Chromosome),
			Spawn:      effectiveParams.Spawn,
		}, effectiveParams)
		emptyReferenceTargetWeight := totalTargetWeight(
			emptyPortfolio,
			bar.Close,
			portfolio.TotalEquity,
			diagnosticValue(emptyReferenceOutput.Diagnostics, "target_weight"),
		)
		state = output.RuntimeState
		filterObservation = longTermFilter.Observe(i, request.Bars)

		if spec.ExecutionMode == ExecutionModeCloseNextOpen {
			pendingOutput = output
			hasPendingOutput = true
			pendingPracticalAdjustment = hasAnyExecutableAdjustment(output)
			if filterObservation.Signal != "" {
				pendingFilterSignal = filterObservation.Signal
			}
		} else {
			practicalExecuted := simulator.Execute(output, bar.Close)
			practicalExecutionWeight := actualExposure(simulator.Portfolio(bar.Close), bar.Close)
			filteredExecuted := TradeSummary{}
			switch filterObservation.Signal {
			case LongTermFilterSignalEnter:
				filterRiskOff = true
				filterDiverged = true
				filterEvent = LongTermFilterSignalEnter
				filteredExecuted = filteredSimulator.LiquidateAll(bar.Close)
			case LongTermFilterSignalExit:
				filterRiskOff = false
				filterDiverged = true
				filterEvent = LongTermFilterSignalExit
				filteredExecuted = filteredSimulator.RebalanceToExposure(practicalExecutionWeight, bar.Close)
			default:
				if !filterRiskOff {
					if filterDiverged {
						if hasAnyExecutableAdjustment(output) || contributedThisBar {
							filteredExecuted = filteredSimulator.RebalanceToExposure(practicalExecutionWeight, bar.Close)
						}
					} else {
						filteredExecuted = filteredSimulator.Execute(output, bar.Close)
					}
				}
			}
			if isEvaluationBar(spec, bar) {
				pointTrades.Add(filteredExecuted)
				tradeCount += filteredExecuted.TradeCount
				costSummary.Add(filteredExecuted.Costs)
				pointPracticalTrades.Add(practicalExecuted)
				practicalTradeCount += practicalExecuted.TradeCount
				practicalCostSummary.Add(practicalExecuted.Costs)
			}
		}

		portfolio = simulator.Portfolio(bar.Close)
		filteredPortfolio := filteredSimulator.Portfolio(bar.Close)
		if request.Hooks.OnStep != nil {
			request.Hooks.OnStep(StepEvent{
				Index:         i,
				Bar:           bar,
				ExecutionMode: spec.ExecutionMode,
				Portfolio:     portfolio,
				Output:        output,
				TotalEquity:   portfolio.TotalEquity,
			})
		}
		if !isEvaluationBar(spec, bar) {
			continue
		}
		if len(points) == 0 {
			evalInitial = filteredPortfolio.TotalEquity
			actualEvalStart = bar.OpenTime
			spec.EvaluationStartIndex = i
		}
		spec.EvaluationEndIndex = i
		practicalTargetWeight := adoptedPracticalTargetWeight
		practicalTargetWeightChange := 0.0
		modelTargetWeightChange := 0.0
		emptyReferenceTargetWeightChange := 0.0
		if hasPrevTargetWeight {
			practicalTargetWeightChange = practicalTargetWeight - prevPracticalTargetWeight
			modelTargetWeightChange = modelTargetWeight - prevModelTargetWeight
			emptyReferenceTargetWeightChange = emptyReferenceTargetWeight - prevEmptyReferenceTargetWeight
		}
		dailyReturn := 0.0
		if len(points) > 0 && points[len(points)-1].TotalEquity > 0 {
			dailyReturn = filteredPortfolio.TotalEquity/points[len(points)-1].TotalEquity - 1
		}
		practicalDailyReturn := 0.0
		if len(points) > 0 && points[len(points)-1].PracticalTotalEquity > 0 {
			practicalDailyReturn = portfolio.TotalEquity/points[len(points)-1].PracticalTotalEquity - 1
		}
		points = append(points, NAVPoint{
			TimeMs:                           bar.OpenTime,
			Price:                            bar.Close,
			TotalEquity:                      filteredPortfolio.TotalEquity,
			Cash:                             filteredPortfolio.USDTBalance,
			AssetQuantity:                    totalAssetQuantity(filteredPortfolio),
			ActualExposureWeight:             actualExposure(filteredPortfolio, bar.Close),
			DailyReturn:                      dailyReturn,
			PracticalTotalEquity:             portfolio.TotalEquity,
			PracticalCash:                    portfolio.USDTBalance,
			PracticalAssetQuantity:           totalAssetQuantity(portfolio),
			PracticalActualExposureWeight:    actualExposure(portfolio, bar.Close),
			PracticalDailyReturn:             practicalDailyReturn,
			PracticalTargetWeight:            practicalTargetWeight,
			PracticalTargetWeightChange:      practicalTargetWeightChange,
			ModelTargetWeight:                modelTargetWeight,
			ModelTargetWeightChange:          modelTargetWeightChange,
			EmptyReferenceTargetWeight:       emptyReferenceTargetWeight,
			EmptyReferenceTargetWeightChange: emptyReferenceTargetWeightChange,
			Trades:                           pointTrades,
			PracticalTrades:                  pointPracticalTrades,
			LongTermFilterEnabled:            spec.LongTermFilter.Enabled,
			LongTermFilterReady:              filterObservation.Ready,
			LongTermFilterRiskOff:            filterRiskOff,
			LongTermFilterCurrentSMA:         filterObservation.CurrentSMA,
			LongTermFilterPreviousSMA:        filterObservation.PreviousSMA,
			LongTermFilterSignal:             filterObservation.Signal,
			LongTermFilterEvent:              filterEvent,
			EffectiveParameters:              effectiveSnapshot,
		})
		prevPracticalTargetWeight = practicalTargetWeight
		prevModelTargetWeight = modelTargetWeight
		prevEmptyReferenceTargetWeight = emptyReferenceTargetWeight
		hasPrevTargetWeight = true
	}

	if len(points) == 0 {
		return Result{}, fmt.Errorf("正式評估區間沒有有效 K 線")
	}
	return finishResult(spec, points, evalInitial, evalInjected, actualEvalStart, evalFlows, tradeCount, costSummary, practicalTradeCount, practicalCostSummary), nil
}

func finishResult(spec Spec, points []NAVPoint, evalInitial float64, evalInjected float64, actualEvalStart int64, evalFlows []quant.TimedCashFlow, tradeCount int, costs CostSummary, practicalTradeCount int, practicalCosts CostSummary) Result {
	finalAssets := 0.0
	evaluationEnd := int64(0)
	if len(points) > 0 {
		finalAssets = points[len(points)-1].TotalEquity
		evaluationEnd = points[len(points)-1].TimeMs
	}
	totalReturn := quant.ModifiedDietzROI(evalInitial, finalAssets, evalFlows, actualEvalStart, evaluationEnd)
	if len(points) == 1 && evalInitial > 0 && len(evalFlows) == 0 {
		totalReturn = finalAssets/evalInitial - 1
	}
	practicalFinalAssets := points[len(points)-1].PracticalTotalEquity
	practicalInitial := points[0].PracticalTotalEquity
	practicalReturn := quant.ModifiedDietzROI(practicalInitial, practicalFinalAssets, evalFlows, actualEvalStart, evaluationEnd)
	if len(points) == 1 && practicalInitial > 0 && len(evalFlows) == 0 {
		practicalReturn = practicalFinalAssets/practicalInitial - 1
	}
	return Result{
		Conditions:           spec,
		Path:                 points,
		FinalAssets:          finalAssets,
		TotalReturn:          totalReturn,
		TradeCount:           tradeCount,
		Costs:                costs,
		TotalInjected:        evalInitial + evalInjected,
		EvaluationInitial:    evalInitial,
		EvaluationStartMs:    actualEvalStart,
		EvaluationEndMs:      evaluationEnd,
		CashFlows:            evalFlows,
		PracticalFinalAssets: practicalFinalAssets,
		PracticalTotalReturn: practicalReturn,
		PracticalTradeCount:  practicalTradeCount,
		PracticalCosts:       practicalCosts,
	}
}

func validateEffectiveChromosome(c quant.Chromosome) error {
	values := map[string]float64{
		"micro_reserve_pct":         c.MicroReservePct,
		"beta":                      c.Beta,
		"gamma":                     c.Gamma,
		"w_mean":                    c.WMean,
		"w_momentum":                c.WMomentum,
		"w_breakout":                c.WBreakout,
		"dust_usd":                  c.DustUSD,
		"rebalance_threshold":       c.RebalanceThreshold,
		"force_full_threshold":      c.ForceFullThreshold,
		"force_empty_threshold":     c.ForceEmptyThreshold,
		"wedge_delta_threshold":     c.WedgeDeltaThreshold,
		"wedge_vol_ratio_threshold": c.WedgeVolRatioThreshold,
		"macro_bear_multiplier":     c.MacroBearMultiplier,
		"macro_bull_multiplier":     c.MacroBullMultiplier,
		"extra_deploy_pct":          c.ExtraDeployPct,
		"soft_release_months":       float64(c.SoftReleaseMonths),
		"soft_release_pct":          c.SoftReleasePct,
		"hard_release_max_pct":      c.HardReleaseMaxPct,
	}
	for name, value := range values {
		bound := quant.HardBounds[name]
		if math.IsNaN(value) || math.IsInf(value, 0) || value < bound.Min || value > bound.Max {
			return fmt.Errorf("%s 超出允許範圍 [%g, %g]", name, bound.Min, bound.Max)
		}
	}
	if c.ForceFullThreshold < c.ForceEmptyThreshold {
		return fmt.Errorf("滿倉閾值低於空倉閾值，參數無效")
	}
	if c.MacroBearMultiplier < c.MacroBullMultiplier {
		return fmt.Errorf("macro_bear_multiplier 必須大於或等於 macro_bull_multiplier")
	}
	return nil
}

func chromosomeParameterMap(c quant.Chromosome) map[string]float64 {
	return map[string]float64{
		"micro_reserve_pct":         c.MicroReservePct,
		"beta":                      c.Beta,
		"gamma":                     c.Gamma,
		"w_mean":                    c.WMean,
		"w_momentum":                c.WMomentum,
		"w_breakout":                c.WBreakout,
		"dust_usd":                  c.DustUSD,
		"rebalance_threshold":       c.RebalanceThreshold,
		"force_full_threshold":      c.ForceFullThreshold,
		"force_empty_threshold":     c.ForceEmptyThreshold,
		"wedge_delta_threshold":     c.WedgeDeltaThreshold,
		"wedge_vol_ratio_threshold": c.WedgeVolRatioThreshold,
		"macro_bear_multiplier":     c.MacroBearMultiplier,
		"macro_bull_multiplier":     c.MacroBullMultiplier,
		"extra_deploy_pct":          c.ExtraDeployPct,
		"soft_release_months":       float64(c.SoftReleaseMonths),
		"soft_release_pct":          c.SoftReleasePct,
		"hard_release_max_pct":      c.HardReleaseMaxPct,
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

func ApplyForceTargetThresholds(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, chromosome quant.Chromosome, modelTargetWeight float64) (quant.StrategyOutput, float64) {
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
		totalEquity = portfolio.USDTBalance + totalAssetQuantity(portfolio)*price
	}
	if totalEquity <= 0 {
		return output
	}
	targetTotalWeight = quant.ClipFloat64(targetTotalWeight, 0, 1)
	currentAssetValue := totalAssetQuantity(portfolio) * price
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

func floatingWeight(portfolio quant.PortfolioSnapshot, price float64, totalEquity float64) float64 {
	if price <= 0 {
		return 0
	}
	if totalEquity <= 0 {
		totalEquity = portfolio.TotalEquity
	}
	if totalEquity <= 0 {
		totalEquity = portfolio.USDTBalance + totalAssetQuantity(portfolio)*price
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
		totalEquity = portfolio.USDTBalance + totalAssetQuantity(portfolio)*price
	}
	if totalEquity <= 0 {
		return 0
	}
	return quant.ClipFloat64(totalAssetQuantity(portfolio)*price/totalEquity, 0, 1)
}

func totalTargetWeight(portfolio quant.PortfolioSnapshot, price float64, totalEquity float64, targetFloatingWeight float64) float64 {
	if price <= 0 {
		return 0
	}
	if totalEquity <= 0 {
		totalEquity = portfolio.TotalEquity
	}
	if totalEquity <= 0 {
		totalEquity = portfolio.USDTBalance + totalAssetQuantity(portfolio)*price
	}
	if totalEquity <= 0 {
		return 0
	}
	nonFloatingWeight := (portfolio.DeadBTC + portfolio.ColdSealedBTC) * price / totalEquity
	return quant.ClipFloat64(nonFloatingWeight+quant.ClipFloat64(targetFloatingWeight, 0, 1), 0, 1)
}

func shouldAdoptPracticalTarget(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, targetWeight float64) bool {
	if hasExecutablePracticalAdjustment(output) {
		return true
	}
	currentWeight := totalAssetWeight(portfolio, price, portfolio.TotalEquity)
	return math.Abs(currentWeight-targetWeight) <= 1e-9
}

func hasExecutablePracticalAdjustment(output quant.StrategyOutput) bool {
	for _, intent := range output.Intents {
		if intent.Engine != quant.EngineMicro {
			continue
		}
		if intent.Action == quant.ActionBuy && intent.AmountUSDT > 0 {
			return true
		}
		if intent.Action == quant.ActionSell && intent.QtyAsset > 0 {
			return true
		}
	}
	return len(output.LotTransfers) > 0
}

func hasAnyExecutableAdjustment(output quant.StrategyOutput) bool {
	for _, intent := range output.Intents {
		if intent.Action == quant.ActionBuy && intent.AmountUSDT > 0 {
			return true
		}
		if intent.Action == quant.ActionSell && intent.QtyAsset > 0 {
			return true
		}
	}
	return len(output.LotTransfers) > 0
}

func ApplyRebalanceThreshold(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, threshold float64) quant.StrategyOutput {
	if RebalanceThresholdAllows(output, portfolio, price, threshold) {
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

func RebalanceThresholdAllows(output quant.StrategyOutput, portfolio quant.PortfolioSnapshot, price float64, threshold float64) bool {
	if threshold <= 0 || price <= 0 {
		return true
	}
	targetWeight := diagnosticValue(output.Diagnostics, "target_weight")
	totalEquity := portfolio.TotalEquity
	if totalEquity <= 0 {
		totalEquity = portfolio.USDTBalance + totalAssetQuantity(portfolio)*price
	}
	if totalEquity <= 0 {
		return true
	}
	currentWeight := floatingWeight(portfolio, price, totalEquity)
	return math.Abs(targetWeight-currentWeight) >= threshold
}

func barYearMonth(bar quant.Bar) (int, time.Month) {
	t := time.UnixMilli(bar.OpenTime).UTC()
	return t.Year(), t.Month()
}
