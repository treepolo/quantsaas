package backtestcore

import (
	"testing"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/strategies/sigmoiddca"
)

func TestLongTermFilterUsesCompletedMonthsAndRetainsStateOnEqualSMA(t *testing.T) {
	bars := []quant.Bar{
		dailyBar("2024-01-30", 100), dailyBar("2024-02-01", 100),
		dailyBar("2024-02-28", 90), dailyBar("2024-03-01", 90),
		dailyBar("2024-03-28", 90), dailyBar("2024-04-01", 90),
		dailyBar("2024-04-30", 120),
	}
	filter := NewLongTermFilter(LongTermFilterConfig{Enabled: true, Months: 1, Version: LongTermFilterVersion})

	var observations []LongTermFilterObservation
	for index := range bars {
		observations = append(observations, filter.Observe(index, bars))
	}
	if observations[0].Ready {
		t.Fatal("第一個完成月份不應已有前月均線可比較")
	}
	if observations[2].Signal != LongTermFilterSignalEnter || !observations[2].RiskOff {
		t.Fatalf("二月下彎訊號 = %+v，預期進入濾網", observations[2])
	}
	if observations[4].Signal != "" || !observations[4].RiskOff {
		t.Fatalf("三月均線相等時應維持濾網狀態，得到 %+v", observations[4])
	}
	if observations[6].Signal != "" {
		t.Fatalf("最後一個未完成月份不得產生訊號，得到 %+v", observations[6])
	}
}

func TestSigmoidDCALongTermFilterExecutionTiming(t *testing.T) {
	bars := []quant.Bar{
		dailyBar("2024-01-30", 120), dailyBar("2024-02-01", 115),
		dailyBar("2024-02-28", 90), dailyBar("2024-03-01", 90),
		dailyBar("2024-03-28", 95), dailyBar("2024-04-01", 95),
	}
	for index := range bars {
		bars[index].Open = bars[index].Close
	}
	params := sigmoiddca.DefaultParams()

	sameBar, err := RunSigmoidDCA(SigmoidDCARequest{
		Spec: filterTestSpec(bars, ExecutionModeCloseSameBar), Bars: bars, Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sameBar.Path[2].LongTermFilterEvent != LongTermFilterSignalEnter || sameBar.Path[2].AssetQuantity != 0 {
		t.Fatalf("收盤同根應在二月底平倉，得到 %+v", sameBar.Path[2])
	}
	if sameBar.Path[2].PracticalAssetQuantity <= 0 {
		t.Fatal("濾網觸發不得改寫實務模型持倉")
	}
	if sameBar.Path[4].LongTermFilterEvent != LongTermFilterSignalExit || sameBar.Path[4].AssetQuantity <= 0 {
		t.Fatalf("收盤同根應在三月底解除並依實務模型重建，得到 %+v", sameBar.Path[4])
	}

	nextOpen, err := RunSigmoidDCA(SigmoidDCARequest{
		Spec: filterTestSpec(bars, ExecutionModeCloseNextOpen), Bars: bars, Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}
	if nextOpen.Path[2].LongTermFilterRiskOff || nextOpen.Path[2].LongTermFilterEvent != "" {
		t.Fatal("隔日開盤模式不得在二月底提前套用濾網")
	}
	if nextOpen.Path[3].LongTermFilterEvent != LongTermFilterSignalEnter || nextOpen.Path[3].AssetQuantity != 0 {
		t.Fatalf("隔日開盤應在三月第一根平倉，得到 %+v", nextOpen.Path[3])
	}
	if nextOpen.Path[4].LongTermFilterEvent != "" || !nextOpen.Path[4].LongTermFilterRiskOff {
		t.Fatal("三月底的解除訊號不得提前改變隔日開盤持倉")
	}
	if nextOpen.Path[5].LongTermFilterEvent != LongTermFilterSignalExit || nextOpen.Path[5].AssetQuantity <= 0 {
		t.Fatalf("隔日開盤應在四月第一根重新建倉，得到 %+v", nextOpen.Path[5])
	}
}

func filterTestSpec(bars []quant.Bar, executionMode string) Spec {
	return Spec{
		Symbol: "TEST", Interval: "1d", ExecutionMode: executionMode,
		StartTimeMs: bars[0].OpenTime, EndTimeMs: bars[len(bars)-1].OpenTime,
		EvaluationStartMs: bars[0].OpenTime, EvaluationEndMs: bars[len(bars)-1].OpenTime,
		InitialCapital: 1000, InitialAssetQuantity: 1,
		LongTermFilter: LongTermFilterConfig{Enabled: true, Months: 1, Version: LongTermFilterVersion},
	}
}

func dailyBar(date string, close float64) quant.Bar {
	timestamp, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return quant.Bar{OpenTime: timestamp.UnixMilli(), Open: close, High: close, Low: close, Close: close}
}
