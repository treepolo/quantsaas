package backtestcore

import (
	"fmt"
	"math"
	"time"

	"quantsaas/internal/quant"
)

const (
	RuleAlwaysExposed    = "always_exposed"
	RuleOddBuyEvenSell   = "odd_buy_even_sell"
	RuleEvenBuyOddSell   = "even_buy_odd_sell"
	RuleFixedDayToggle   = "fixed_day_toggle"
	RuleOpenBuyCloseSell = "open_buy_close_sell"
)

type RuleConfig struct {
	Type              string `json:"type"`
	ToggleEveryNBars  int    `json:"toggle_every_n_bars,omitempty"`
	StartWithExposure bool   `json:"start_with_exposure,omitempty"`
}

type RuleRequest struct {
	Spec Spec
	Bars []quant.Bar
	Rule RuleConfig
}

type ExposureTarget struct {
	TimeMs int64   `json:"time_ms"`
	Weight float64 `json:"weight"`
}

type ExposureReplayRequest struct {
	Spec    Spec
	Bars    []quant.Bar
	Targets []ExposureTarget
}

type targetProvider func(index int, bar quant.Bar) (float64, error)

func RunRule(request RuleRequest) (Result, error) {
	if request.Rule.Type == RuleOpenBuyCloseSell {
		request.Spec.ExecutionMode = ExecutionModeOpenBuyCloseSell
	}
	provider, err := ruleTargetProvider(request.Rule)
	if err != nil {
		return Result{}, err
	}
	return runTargets(request.Spec, request.Bars, RunnerRule, provider)
}

func RunExposureReplay(request ExposureReplayRequest) (Result, error) {
	if request.Spec.ExecutionMode == ExecutionModeOpenBuyCloseSell {
		return Result{}, fmt.Errorf("曝險序列重放不支援每天開盤買、收盤賣模式")
	}
	targets := make(map[int64]float64, len(request.Targets))
	for _, target := range request.Targets {
		if target.TimeMs == 0 {
			return Result{}, fmt.Errorf("曝險序列時間不可為 0")
		}
		if math.IsNaN(target.Weight) || math.IsInf(target.Weight, 0) || target.Weight < 0 || target.Weight > 1 {
			return Result{}, fmt.Errorf("%d 的目標曝險必須介於 0 到 1", target.TimeMs)
		}
		if _, exists := targets[target.TimeMs]; exists {
			return Result{}, fmt.Errorf("曝險序列包含重複時間 %d", target.TimeMs)
		}
		targets[target.TimeMs] = target.Weight
	}
	provider := func(_ int, bar quant.Bar) (float64, error) {
		target, ok := targets[bar.OpenTime]
		if !ok {
			return 0, fmt.Errorf("曝險序列缺少 %d 的目標", bar.OpenTime)
		}
		return target, nil
	}
	return runTargets(request.Spec, request.Bars, RunnerExposureReplay, provider)
}

func ruleTargetProvider(rule RuleConfig) (targetProvider, error) {
	switch rule.Type {
	case RuleAlwaysExposed:
		return func(_ int, _ quant.Bar) (float64, error) { return 1, nil }, nil
	case RuleOddBuyEvenSell:
		return func(_ int, bar quant.Bar) (float64, error) {
			if time.UnixMilli(bar.OpenTime).UTC().Day()%2 == 1 {
				return 1, nil
			}
			return 0, nil
		}, nil
	case RuleEvenBuyOddSell:
		return func(_ int, bar quant.Bar) (float64, error) {
			if time.UnixMilli(bar.OpenTime).UTC().Day()%2 == 0 {
				return 1, nil
			}
			return 0, nil
		}, nil
	case RuleFixedDayToggle:
		if rule.ToggleEveryNBars <= 0 {
			return nil, fmt.Errorf("固定 N 日切換規則的 N 必須大於 0")
		}
		return func(index int, _ quant.Bar) (float64, error) {
			exposed := (index/rule.ToggleEveryNBars)%2 == 0
			if !rule.StartWithExposure {
				exposed = !exposed
			}
			if exposed {
				return 1, nil
			}
			return 0, nil
		}, nil
	case RuleOpenBuyCloseSell:
		return func(_ int, _ quant.Bar) (float64, error) { return 0, nil }, nil
	default:
		return nil, fmt.Errorf("不支援的無意義規則: %s", rule.Type)
	}
}

func runTargets(rawSpec Spec, bars []quant.Bar, runner string, provider targetProvider) (Result, error) {
	spec, err := normalizeSpec(rawSpec, bars, runner)
	if err != nil {
		return Result{}, err
	}
	if runner != RunnerRule && spec.ExecutionMode == ExecutionModeOpenBuyCloseSell {
		return Result{}, fmt.Errorf("只有規則 runner 支援每天開盤買、收盤賣模式")
	}
	simulator := NewSimulator(spec.InitialCapital, spec.InitialAssetQuantity, SimulatorConfig{
		Costs:           spec.Costs,
		MinimumTradeUSD: spec.MinimumTradeUSD,
		MinimumAssetQty: spec.MinimumAssetQuantity,
	})
	points := make([]NAVPoint, 0, len(bars))
	evalFlows := make([]quant.TimedCashFlow, 0)
	evalInjected := 0.0
	evalInitial := 0.0
	actualEvalStart := int64(0)
	evaluationInitialized := false
	tradeCount := 0
	costSummary := CostSummary{}
	pendingTarget := 0.0
	hasPendingTarget := false
	previousTarget := 0.0
	hasPreviousTarget := false
	lastYear, lastMonth := barYearMonth(bars[0])
	runStarted := false
	historyOnlyStarted := false
	targetIndex := 0

	for i, bar := range bars {
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
			continue
		}
		if spec.PrefixMode == PrefixModeHistoryOnly && !historyOnlyStarted {
			lastYear, lastMonth = barYearMonth(bar)
			historyOnlyStarted = true
		}

		year, month := barYearMonth(bar)
		if i > 0 && (year != lastYear || month != lastMonth) && spec.MonthlyContribution > 0 {
			simulator.Contribute(spec.MonthlyContribution)
			if bar.OpenTime > spec.EvaluationStartMs {
				evalInjected += spec.MonthlyContribution
				evalFlows = append(evalFlows, quant.TimedCashFlow{TimeMs: bar.OpenTime, Amount: spec.MonthlyContribution})
			}
			lastYear, lastMonth = year, month
		}

		pointTrades := TradeSummary{}
		intradayExposure := 0.0
		if isEvaluationBar(spec, bar) && !evaluationInitialized {
			initialPrice := bar.Close
			if spec.ExecutionMode == ExecutionModeCloseNextOpen || spec.ExecutionMode == ExecutionModeOpenBuyCloseSell {
				initialPrice = bar.Open
			}
			if initialPrice <= 0 {
				return Result{}, fmt.Errorf("%d 的正式評估起始價格無效", bar.OpenTime)
			}
			evalInitial = simulator.Portfolio(initialPrice).TotalEquity
			actualEvalStart = bar.OpenTime
			evaluationInitialized = true
		}
		if spec.ExecutionMode == ExecutionModeCloseNextOpen && hasPendingTarget {
			if bar.Open <= 0 || math.IsNaN(bar.Open) || math.IsInf(bar.Open, 0) {
				return Result{}, fmt.Errorf("%d 的開盤價無效，無法執行前一根 K 線目標", bar.OpenTime)
			}
			executed := simulator.RebalanceToExposure(pendingTarget, bar.Open)
			if isEvaluationBar(spec, bar) {
				pointTrades.Add(executed)
				tradeCount += executed.TradeCount
				costSummary.Add(executed.Costs)
			}
			hasPendingTarget = false
		}

		target, targetErr := provider(targetIndex, bar)
		if targetErr != nil {
			return Result{}, targetErr
		}
		targetIndex++
		if math.IsNaN(target) || math.IsInf(target, 0) || target < 0 || target > 1 {
			return Result{}, fmt.Errorf("%d 的目標曝險必須介於 0 到 1", bar.OpenTime)
		}

		switch spec.ExecutionMode {
		case ExecutionModeCloseNextOpen:
			pendingTarget = target
			hasPendingTarget = true
		case ExecutionModeOpenBuyCloseSell:
			if bar.Open <= 0 || math.IsNaN(bar.Open) || math.IsInf(bar.Open, 0) {
				return Result{}, fmt.Errorf("%d 的開盤價無效，無法執行開盤買入", bar.OpenTime)
			}
			openTrade := simulator.RebalanceToExposure(1, bar.Open)
			intradayExposure = actualExposure(simulator.Portfolio(bar.Open), bar.Open)
			closeTrade := simulator.RebalanceToExposure(0, bar.Close)
			if isEvaluationBar(spec, bar) {
				pointTrades.Add(openTrade)
				pointTrades.Add(closeTrade)
				tradeCount += openTrade.TradeCount + closeTrade.TradeCount
				costSummary.Add(openTrade.Costs)
				costSummary.Add(closeTrade.Costs)
			}
			target = 0
		default:
			executed := simulator.RebalanceToExposure(target, bar.Close)
			if isEvaluationBar(spec, bar) {
				pointTrades.Add(executed)
				tradeCount += executed.TradeCount
				costSummary.Add(executed.Costs)
			}
		}

		if !isEvaluationBar(spec, bar) {
			continue
		}
		portfolio := simulator.Portfolio(bar.Close)
		if len(points) == 0 {
			spec.EvaluationStartIndex = i
		}
		spec.EvaluationEndIndex = i
		targetChange := 0.0
		if hasPreviousTarget {
			targetChange = target - previousTarget
		}
		dailyReturn := 0.0
		if len(points) > 0 && points[len(points)-1].TotalEquity > 0 {
			dailyReturn = portfolio.TotalEquity/points[len(points)-1].TotalEquity - 1
		}
		points = append(points, NAVPoint{
			TimeMs:                        bar.OpenTime,
			Price:                         bar.Close,
			TotalEquity:                   portfolio.TotalEquity,
			Cash:                          portfolio.USDTBalance,
			AssetQuantity:                 totalAssetQuantity(portfolio),
			ActualExposureWeight:          actualExposure(portfolio, bar.Close),
			IntradayExposureWeight:        intradayExposure,
			DailyReturn:                   dailyReturn,
			PracticalTotalEquity:          portfolio.TotalEquity,
			PracticalCash:                 portfolio.USDTBalance,
			PracticalAssetQuantity:        totalAssetQuantity(portfolio),
			PracticalActualExposureWeight: actualExposure(portfolio, bar.Close),
			PracticalDailyReturn:          dailyReturn,
			PracticalTargetWeight:         target,
			PracticalTargetWeightChange:   targetChange,
			ModelTargetWeight:             target,
			ModelTargetWeightChange:       targetChange,
			Trades:                        pointTrades,
			PracticalTrades:               pointTrades,
		})
		previousTarget = target
		hasPreviousTarget = true
	}

	if len(points) == 0 {
		return Result{}, fmt.Errorf("正式評估區間沒有有效 K 線")
	}
	return finishResult(spec, points, evalInitial, evalInjected, actualEvalStart, evalFlows, tradeCount, costSummary, tradeCount, costSummary), nil
}
