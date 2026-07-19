package backtestcore

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

func TestRunSigmoidDCAStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	steps := 0
	params := sigmoiddca.DefaultParams()
	params.Spawn.Policy.InitialUSDT = 1000
	_, err := RunSigmoidDCA(SigmoidDCARequest{
		Context: ctx,
		Spec: Spec{
			Symbol:         "TEST",
			Interval:       "1d",
			ExecutionMode:  ExecutionModeCloseSameBar,
			InitialCapital: 1000,
		},
		Bars:   flatCoreBars(120),
		Params: params,
		Hooks: Hooks{ComputeStep: func(int64) {
			steps++
			if steps == 1 {
				cancel()
			}
		}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunSigmoidDCA error = %v, want context.Canceled", err)
	}
	if steps != 1 {
		t.Fatalf("compute steps = %d, want cancellation after first step", steps)
	}
}

func TestSimulatorTracksPostTradeStateAndCosts(t *testing.T) {
	simulator := NewSimulator(1000, 0, SimulatorConfig{
		Costs: quant.ExecutionCostConfig{FeeRate: 0.01, SpreadRate: 0.01},
	})
	buy := simulator.Execute(quant.StrategyOutput{Intents: []quant.TradeIntent{{
		Action:     quant.ActionBuy,
		AmountUSDT: 1000,
		LotType:    quant.LotTypeFloating,
	}}}, 100)
	if buy.TradeCount != 1 || buy.BuyCount != 1 || buy.SellCount != 0 {
		t.Fatalf("unexpected buy summary: %+v", buy)
	}
	portfolio := simulator.Portfolio(100)
	wantQty := 1000 / (100 * 1.01 * 1.01)
	coreAssertNear(t, "asset quantity", totalAssetQuantity(portfolio), wantQty)
	coreAssertNear(t, "cash", portfolio.USDTBalance, 0)
	coreAssertNear(t, "actual exposure", actualExposure(portfolio, 100), 1)
	coreAssertNear(t, "buy cost total", buy.Costs.TotalCost, buy.Costs.FeeCost+buy.Costs.SlippageCost)

	sell := simulator.Execute(quant.StrategyOutput{Intents: []quant.TradeIntent{{
		Action:   quant.ActionSell,
		QtyAsset: wantQty,
		LotType:  quant.LotTypeFloating,
	}}}, 100)
	if sell.TradeCount != 1 || sell.SellCount != 1 || sell.BuyCount != 0 {
		t.Fatalf("unexpected sell summary: %+v", sell)
	}
	portfolio = simulator.Portfolio(100)
	coreAssertNear(t, "asset after sell", totalAssetQuantity(portfolio), 0)
	coreAssertNear(t, "cash after sell", portfolio.USDTBalance, wantQty*100*0.99*0.99)
	coreAssertNear(t, "actual exposure after sell", actualExposure(portfolio, 100), 0)
}

func TestExposureReplaySupportsPartialExposure(t *testing.T) {
	bars := []quant.Bar{
		{OpenTime: day(2026, 1, 1), Open: 100, High: 100, Low: 100, Close: 100},
		{OpenTime: day(2026, 1, 2), Open: 110, High: 110, Low: 110, Close: 110},
	}
	result, err := RunExposureReplay(ExposureReplayRequest{
		Spec: Spec{
			Symbol:         "TEST",
			Interval:       "1d",
			ExecutionMode:  ExecutionModeCloseSameBar,
			InitialCapital: 1000,
		},
		Bars: bars,
		Targets: []ExposureTarget{
			{TimeMs: bars[0].OpenTime, Weight: 0.5},
			{TimeMs: bars[1].OpenTime, Weight: 0.5},
		},
	})
	if err != nil {
		t.Fatalf("RunExposureReplay error: %v", err)
	}
	if len(result.Path) != 2 {
		t.Fatalf("path points = %d, want 2", len(result.Path))
	}
	for _, point := range result.Path {
		coreAssertNear(t, "actual exposure", point.ActualExposureWeight, 0.5)
	}
	coreAssertNear(t, "final assets", result.FinalAssets, 1050)
	coreAssertNear(t, "total return", result.TotalReturn, 0.05)
	coreAssertNear(t, "daily return", result.Path[1].DailyReturn, 0.05)
	if result.TradeCount != 2 {
		t.Fatalf("trade count = %d, want 2", result.TradeCount)
	}
}

func TestExposureReplayCloseNextOpenDoesNotAnticipatePendingTarget(t *testing.T) {
	bars := []quant.Bar{
		{OpenTime: day(2026, 1, 1), Open: 100, High: 100, Low: 100, Close: 100},
		{OpenTime: day(2026, 1, 2), Open: 200, High: 200, Low: 200, Close: 200},
	}
	result, err := RunExposureReplay(ExposureReplayRequest{
		Spec: Spec{Symbol: "TEST", Interval: "1d", ExecutionMode: ExecutionModeCloseNextOpen, InitialCapital: 1000},
		Bars: bars,
		Targets: []ExposureTarget{
			{TimeMs: bars[0].OpenTime, Weight: 1},
			{TimeMs: bars[1].OpenTime, Weight: 0},
		},
	})
	if err != nil {
		t.Fatalf("RunExposureReplay error: %v", err)
	}
	coreAssertNear(t, "first-day actual exposure", result.Path[0].ActualExposureWeight, 0)
	coreAssertNear(t, "second-day actual exposure", result.Path[1].ActualExposureWeight, 1)
	coreAssertNear(t, "final assets", result.FinalAssets, 1000)
	if result.TradeCount != 1 {
		t.Fatalf("trade count = %d, want 1", result.TradeCount)
	}
}

func TestExposureReplayAppliesMonthlyContributionAsCashFlow(t *testing.T) {
	bars := []quant.Bar{
		{OpenTime: day(2026, 1, 31), Open: 100, High: 100, Low: 100, Close: 100},
		{OpenTime: day(2026, 2, 1), Open: 100, High: 100, Low: 100, Close: 100},
	}
	result, err := RunExposureReplay(ExposureReplayRequest{
		Spec: Spec{
			Symbol:              "TEST",
			Interval:            "1d",
			ExecutionMode:       ExecutionModeCloseSameBar,
			InitialCapital:      1000,
			MonthlyContribution: 100,
		},
		Bars: bars,
		Targets: []ExposureTarget{
			{TimeMs: bars[0].OpenTime, Weight: 0},
			{TimeMs: bars[1].OpenTime, Weight: 0},
		},
	})
	if err != nil {
		t.Fatalf("RunExposureReplay error: %v", err)
	}
	coreAssertNear(t, "final assets", result.FinalAssets, 1100)
	coreAssertNear(t, "total injected", result.TotalInjected, 1100)
	coreAssertNear(t, "cash", result.Path[1].Cash, 1100)
	coreAssertNear(t, "cash-flow-adjusted total return", result.TotalReturn, 0)
	if len(result.CashFlows) != 1 || result.CashFlows[0].Amount != 100 {
		t.Fatalf("cash flows = %+v, want one 100 contribution", result.CashFlows)
	}
}

func TestRuleOpenBuyCloseSellUsesIntradayExposure(t *testing.T) {
	bar := quant.Bar{OpenTime: day(2026, 1, 1), Open: 100, High: 110, Low: 100, Close: 110}
	result, err := RunRule(RuleRequest{
		Spec: Spec{Symbol: "TEST", Interval: "1d", InitialCapital: 1000},
		Bars: []quant.Bar{bar},
		Rule: RuleConfig{Type: RuleOpenBuyCloseSell},
	})
	if err != nil {
		t.Fatalf("RunRule error: %v", err)
	}
	point := result.Path[0]
	coreAssertNear(t, "end-of-day exposure", point.ActualExposureWeight, 0)
	coreAssertNear(t, "intraday exposure", point.IntradayExposureWeight, 1)
	coreAssertNear(t, "cash", point.Cash, 1100)
	coreAssertNear(t, "asset quantity", point.AssetQuantity, 0)
	coreAssertNear(t, "total return", result.TotalReturn, 0.1)
	if result.TradeCount != 2 || point.Trades.TradeCount != 2 {
		t.Fatalf("trade counts = result %d / point %d, want 2", result.TradeCount, point.Trades.TradeCount)
	}
}

func TestRuleDayParityTargetsAreDeterministic(t *testing.T) {
	bars := []quant.Bar{
		{OpenTime: day(2026, 1, 1), Open: 100, High: 100, Low: 100, Close: 100},
		{OpenTime: day(2026, 1, 2), Open: 100, High: 100, Low: 100, Close: 100},
	}
	result, err := RunRule(RuleRequest{
		Spec: Spec{Symbol: "TEST", Interval: "1d", ExecutionMode: ExecutionModeCloseSameBar, InitialCapital: 1000},
		Bars: bars,
		Rule: RuleConfig{Type: RuleOddBuyEvenSell},
	})
	if err != nil {
		t.Fatalf("RunRule error: %v", err)
	}
	coreAssertNear(t, "odd-day target", result.Path[0].PracticalTargetWeight, 1)
	coreAssertNear(t, "even-day target", result.Path[1].PracticalTargetWeight, 0)
	coreAssertNear(t, "odd-day actual", result.Path[0].ActualExposureWeight, 1)
	coreAssertNear(t, "even-day actual", result.Path[1].ActualExposureWeight, 0)
}

func TestSigmoidDynamicParametersAreValidatedAndTraceable(t *testing.T) {
	bars := flatCoreBars(115)
	params := sigmoiddca.DefaultParams()
	params.Spawn.Policy.InitialUSDT = 1000
	params.Spawn.Policy.MonthlyInjectUSDT = 0
	providerCalls := 0
	result, err := RunSigmoidDCA(SigmoidDCARequest{
		Spec: Spec{
			Symbol:            "TEST",
			Interval:          "1d",
			ExecutionMode:     ExecutionModeCloseSameBar,
			InitialCapital:    1000,
			EvaluationStartMs: bars[112].OpenTime,
		},
		Bars:   bars,
		Params: params,
		ParameterProvider: func(context ParameterContext) (EffectiveParameters, error) {
			providerCalls++
			chromosome := params.Chromosome
			chromosome.Beta = 2.5
			return EffectiveParameters{
				Chromosome: chromosome,
				Metadata: ParameterMetadata{
					StructureState: "stable",
					OccurrenceID:   "occ-1",
					ModelVersion:   "model-v1",
					PolicyVersion:  "policy-v1",
					FallbackEvent:  "none",
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("RunSigmoidDCA error: %v", err)
	}
	if providerCalls != len(bars) {
		t.Fatalf("provider calls = %d, want %d", providerCalls, len(bars))
	}
	if len(result.Path) != 3 {
		t.Fatalf("path points = %d, want 3", len(result.Path))
	}
	for _, point := range result.Path {
		if point.EffectiveParameters == nil {
			t.Fatal("missing effective parameter snapshot")
		}
		if point.EffectiveParameters.Metadata.ModelVersion != "model-v1" || point.EffectiveParameters.Metadata.OccurrenceID != "occ-1" {
			t.Fatalf("unexpected dynamic metadata: %+v", point.EffectiveParameters.Metadata)
		}
		coreAssertNear(t, "effective beta", point.EffectiveParameters.Chromosome.Beta, 2.5)
	}

	_, err = RunSigmoidDCA(SigmoidDCARequest{
		Spec:   Spec{Symbol: "TEST", Interval: "1d", InitialCapital: 1000},
		Bars:   bars[:1],
		Params: params,
		ParameterProvider: func(ParameterContext) (EffectiveParameters, error) {
			invalid := params.Chromosome
			invalid.Beta = 999
			return EffectiveParameters{Chromosome: invalid}, nil
		},
	})
	if err == nil {
		t.Fatal("expected out-of-range dynamic parameter error")
	}
}

func TestFixedDynamicProviderMatchesStaticP02Execution(t *testing.T) {
	bars := flatCoreBars(140)
	params := sigmoiddca.DefaultParams()
	params.Spawn.Policy.InitialUSDT = 1000
	params.Spawn.Policy.MonthlyInjectUSDT = 50
	spec := Spec{Symbol: "TEST", Interval: "1d", ExecutionMode: ExecutionModeCloseNextOpen, InitialCapital: 1000, MonthlyContribution: 50, EvaluationStartMs: bars[112].OpenTime}
	staticResult, err := RunSigmoidDCA(SigmoidDCARequest{Spec: spec, Bars: bars, Params: params})
	if err != nil {
		t.Fatal(err)
	}
	dynamicResult, err := RunSigmoidDCA(SigmoidDCARequest{Spec: spec, Bars: bars, Params: params, ParameterProvider: func(ParameterContext) (EffectiveParameters, error) {
		return EffectiveParameters{Chromosome: params.Chromosome, Metadata: ParameterMetadata{FallbackEvent: "fixed"}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	coreAssertNear(t, "fixed final assets", dynamicResult.FinalAssets, staticResult.FinalAssets)
	coreAssertNear(t, "fixed return", dynamicResult.TotalReturn, staticResult.TotalReturn)
	coreAssertNear(t, "fixed practical final assets", dynamicResult.PracticalFinalAssets, staticResult.PracticalFinalAssets)
	if dynamicResult.TradeCount != staticResult.TradeCount || len(dynamicResult.Path) != len(staticResult.Path) {
		t.Fatalf("fixed provider changed execution: static trades/path=%d/%d dynamic=%d/%d", staticResult.TradeCount, len(staticResult.Path), dynamicResult.TradeCount, len(dynamicResult.Path))
	}
	for index := range staticResult.Path {
		staticPoint, dynamicPoint := staticResult.Path[index], dynamicResult.Path[index]
		coreAssertNear(t, "fixed total equity", dynamicPoint.TotalEquity, staticPoint.TotalEquity)
		coreAssertNear(t, "fixed target weight", dynamicPoint.PracticalTargetWeight, staticPoint.PracticalTargetWeight)
		coreAssertNear(t, "fixed cash", dynamicPoint.Cash, staticPoint.Cash)
		coreAssertNear(t, "fixed quantity", dynamicPoint.AssetQuantity, staticPoint.AssetQuantity)
		if dynamicPoint.Trades != staticPoint.Trades || dynamicPoint.PracticalTrades != staticPoint.PracticalTrades {
			t.Fatalf("fixed provider changed trades at path index %d", index)
		}
	}
}

func TestSigmoidDynamicParameterProviderErrorRejectsRun(t *testing.T) {
	params := sigmoiddca.DefaultParams()
	_, err := RunSigmoidDCA(SigmoidDCARequest{
		Spec:   Spec{Symbol: "TEST", Interval: "1d", InitialCapital: 1000},
		Bars:   flatCoreBars(1),
		Params: params,
		ParameterProvider: func(ParameterContext) (EffectiveParameters, error) {
			return EffectiveParameters{}, errors.New("no causal snapshot")
		},
	})
	if err == nil {
		t.Fatal("expected provider error")
	}
}

func TestRunRejectsEvaluationRangeOutsideDataset(t *testing.T) {
	bars := flatCoreBars(2)
	_, err := RunExposureReplay(ExposureReplayRequest{
		Spec: Spec{
			Symbol:            "TEST",
			Interval:          "1d",
			InitialCapital:    1000,
			EvaluationStartMs: bars[len(bars)-1].OpenTime + int64(24*time.Hour/time.Millisecond),
		},
		Bars: bars,
	})
	if err == nil {
		t.Fatal("expected evaluation range error")
	}
}

func TestHistoryOnlyPrefixDoesNotTradeOrInjectCapital(t *testing.T) {
	bars := flatCoreBars(115)
	params := sigmoiddca.DefaultParams()
	params.Spawn.Policy.InitialUSDT = 1000
	params.Spawn.Policy.MonthlyInjectUSDT = 100
	params.PositionStructure = sigmoiddca.PositionStructureFloatingOnly
	result, err := RunSigmoidDCA(SigmoidDCARequest{
		Spec: Spec{
			Symbol:              "TEST",
			Interval:            "1d",
			ExecutionMode:       ExecutionModeCloseSameBar,
			PrefixMode:          PrefixModeHistoryOnly,
			InitialCapital:      1000,
			MonthlyContribution: 100,
			EvaluationStartMs:   bars[112].OpenTime,
		},
		Bars:   bars,
		Params: params,
	})
	if err != nil {
		t.Fatalf("RunSigmoidDCA error: %v", err)
	}
	if len(result.Path) != 3 {
		t.Fatalf("path points = %d, want 3", len(result.Path))
	}
	coreAssertNear(t, "total injected", result.TotalInjected, 1000)
	coreAssertNear(t, "final assets", result.FinalAssets, 1000)
	if result.Conditions.EvaluationStartIndex != 112 {
		t.Fatalf("evaluation start index = %d, want 112", result.Conditions.EvaluationStartIndex)
	}
	if result.TradeCount == 0 {
		t.Fatal("expected the first formal evaluation bars to use prefix history and produce a trade")
	}
}

func TestSigmoidPositionStructureReachesCoreUnchanged(t *testing.T) {
	bars := flatCoreBars(115)
	base := sigmoiddca.DefaultParams()
	base.Spawn.Policy.InitialUSDT = 1000
	base.Spawn.Policy.MonthlyInjectUSDT = 100

	run := func(positionStructure string) quant.PortfolioSnapshot {
		params := base
		params.PositionStructure = positionStructure
		latest := quant.PortfolioSnapshot{}
		_, err := RunSigmoidDCA(SigmoidDCARequest{
			Spec: Spec{
				Symbol:              "TEST",
				Interval:            "1d",
				ExecutionMode:       ExecutionModeCloseSameBar,
				PositionStructure:   positionStructure,
				InitialCapital:      1000,
				MonthlyContribution: 100,
			},
			Bars:   bars,
			Params: params,
			Hooks: Hooks{OnStep: func(event StepEvent) {
				latest = event.Portfolio
			}},
		})
		if err != nil {
			t.Fatalf("RunSigmoidDCA(%s) error: %v", positionStructure, err)
		}
		return latest
	}

	dual := run(sigmoiddca.PositionStructureDualLayer)
	floating := run(sigmoiddca.PositionStructureFloatingOnly)
	if dual.DeadBTC <= 0 {
		t.Fatalf("dual-layer dead asset = %f, want > 0", dual.DeadBTC)
	}
	coreAssertNear(t, "floating-only dead asset", floating.DeadBTC, 0)
}

func TestAlwaysExposedRuleRunsWithoutStrategyParameters(t *testing.T) {
	bars := flatCoreBars(40)
	result, err := RunRule(RuleRequest{
		Spec: Spec{
			Symbol:              "TEST",
			Interval:            "1d",
			ExecutionMode:       ExecutionModeCloseSameBar,
			InitialCapital:      1000,
			MonthlyContribution: 0,
		},
		Bars: bars,
		Rule: RuleConfig{Type: RuleAlwaysExposed},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Path) != len(bars) {
		t.Fatalf("path points = %d, want %d", len(result.Path), len(bars))
	}
	for index, point := range result.Path {
		if point.ActualExposureWeight < 0.999999 {
			t.Fatalf("point %d exposure = %.8f, want full exposure", index, point.ActualExposureWeight)
		}
	}
	coreAssertNear(t, "flat-market return", result.TotalReturn, 0)
}

func flatCoreBars(count int) []quant.Bar {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := make([]quant.Bar, count)
	for i := range bars {
		bars[i] = quant.Bar{
			OpenTime: start.AddDate(0, 0, i).UnixMilli(),
			Open:     100,
			High:     100,
			Low:      100,
			Close:    100,
		}
	}
	return bars
}

func day(year int, month time.Month, value int) int64 {
	return time.Date(year, month, value, 0, 0, 0, 0, time.UTC).UnixMilli()
}

func coreAssertNear(t *testing.T, name string, got float64, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %.15f, want %.15f", name, got, want)
	}
}
