package quant

import (
	"fmt"
	"math"
	"time"
)

type MacroDecisionInput struct {
	Symbol              string
	CurrentTimeMs       int64
	LastMacroYearMonth  string
	USDTBalance         float64
	SpendableUSDT       float64
	ReserveFloor        float64
	MicroReserveBuffer  float64
	MonthlyInjectUSDT   float64
	DustUSD             float64
	Market              MarketState
	MacroBearMultiplier float64
	MacroBullMultiplier float64
	ExtraDeployPct      float64
}

type MacroDecisionOutput struct {
	ShouldBuy      bool
	Action         string
	Engine         string
	Symbol         string
	AmountUSDT     float64
	LotType        string
	YearMonth      string
	RegimeMultiple float64
	Reason         string
}

func ComputeMacroDecision(input MacroDecisionInput) MacroDecisionOutput {
	dust := input.DustUSD
	if dust <= 0 {
		dust = 10.1
	}

	yearMonth := yearMonthFromMs(input.CurrentTimeMs)
	if yearMonth == "" || yearMonth == input.LastMacroYearMonth {
		return MacroDecisionOutput{YearMonth: yearMonth}
	}

	multiplier := macroRegimeMultiplier(input)
	extraDeploy := math.Max(0, input.USDTBalance-input.ReserveFloor-input.MicroReserveBuffer) *
		ClipFloat64(input.ExtraDeployPct, 0, 0.60)
	budget := input.MonthlyInjectUSDT*multiplier + extraDeploy
	amount := math.Min(budget, input.SpendableUSDT)
	amount = RoundToUSDT(amount)
	if amount < dust {
		return MacroDecisionOutput{
			YearMonth:      yearMonth,
			RegimeMultiple: multiplier,
			Reason:         "macro budget below dust threshold",
		}
	}

	return MacroDecisionOutput{
		ShouldBuy:      true,
		Action:         ActionBuy,
		Engine:         EngineMacro,
		Symbol:         input.Symbol,
		AmountUSDT:     amount,
		LotType:        LotTypeDeadStack,
		YearMonth:      yearMonth,
		RegimeMultiple: multiplier,
		Reason:         fmt.Sprintf("monthly macro dca in %s", input.Market.State),
	}
}

func macroRegimeMultiplier(input MacroDecisionInput) float64 {
	bear := input.MacroBearMultiplier
	if bear <= 0 {
		bear = 1.4
	}
	bull := input.MacroBullMultiplier
	if bull <= 0 {
		bull = 0.6
	}

	switch input.Market.State {
	case MarketStateBullTrend:
		return ClipFloat64(bull, 0.2, 1.0)
	case MarketStateBearTrend:
		return ClipFloat64(bear, 1.0, 2.5)
	case MarketStateShock:
		if input.Market.DrawdownRatio < -0.20 {
			return 1.8
		}
		return 1.0
	default:
		return 1.0
	}
}

func yearMonthFromMs(ms int64) string {
	if ms <= 0 {
		return ""
	}
	t := time.UnixMilli(ms).UTC()
	return fmt.Sprintf("%04d-%02d", t.Year(), int(t.Month()))
}
