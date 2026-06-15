package quant

import "math"

const (
	MicroSignalEMABars     = 21
	MicroSignalStdDevBars  = 21
	MicroVolRatioShortBars = 16
	MicroVolRatioLongBars  = 112
)

type MicroDecisionInput struct {
	Closes                 []float64
	Price                  float64
	TotalEquity            float64
	FloatBTC               float64
	SpendableUSDT          float64
	Signal                 float64
	WMean                  float64
	WMomentum              float64
	WBreakout              float64
	SigmaFloor             float64
	Beta                   float64
	Gamma                  float64
	MarketBetaMultiplier   float64
	VolatilityRatio        float64
	DustUSD                float64
	WedgeDeltaThreshold    float64
	WedgeVolRatioThreshold float64
	IsQuiet                bool
	AISignal               AISignalVector
	AIW1                   float64
	AIW2                   float64
	AIW3                   float64
	AIBeta                 float64
}

type MicroDecisionOutput struct {
	Action          string
	Signal          float64
	CurrentWeight   float64
	TargetWeight    float64
	DeltaWeight     float64
	TheoreticalUSD  float64
	OrderUSDT       float64
	Exponent        float64
	VolatilityRatio float64
	AISignalScalar  float64
	ForcedMinimum   bool
}

// ComputeMicroDecisionV4 implements the Sigmoid dynamic balance.
// Signal is the external market force, InventoryBias is the restoring spring,
// Beta controls spring stiffness, Gamma controls inventory feedback, and
// VolatilityRatio prevents quiet-market dust from becoming useless churn.
func ComputeMicroDecisionV4(input MicroDecisionInput) MicroDecisionOutput {
	price := input.Price
	if price <= 0 && len(input.Closes) > 0 {
		price = input.Closes[len(input.Closes)-1]
	}
	if price <= 0 || input.TotalEquity <= 0 {
		return MicroDecisionOutput{}
	}

	signal := input.Signal
	if len(input.Closes) > 0 {
		computed, ok := computeMicroSignal(input.Closes, input)
		if !ok {
			return MicroDecisionOutput{}
		}
		signal = computed
	}

	volatilityRatio := input.VolatilityRatio
	if volatilityRatio <= 0 {
		volatilityRatio = ComputeVolatilityRatio(input.Closes)
	}

	dust := input.DustUSD
	if dust <= 0 {
		dust = 10.1
	}

	marketBeta := input.MarketBetaMultiplier
	if marketBeta <= 0 {
		marketBeta = 1
	}

	currentWeight := ClipFloat64(input.FloatBTC*price/input.TotalEquity, 0, 1)
	effectiveBeta := math.Max(0.01, input.Beta*marketBeta)
	inventoryBias := currentWeight - 0.5
	aiSignal := input.AIW1*input.AISignal.SMarket +
		input.AIW2*input.AISignal.SNews +
		input.AIW3*input.AISignal.SSentiment

	exponent := effectiveBeta*signal + input.Gamma*inventoryBias + input.AIBeta*aiSignal
	targetWeight := ClipFloat64(1/(1+math.Exp(exponent)), 0, 1)
	deltaWeight := targetWeight - currentWeight
	theoreticalUSD := deltaWeight * input.TotalEquity

	input.VolatilityRatio = volatilityRatio
	orderUSD, forced := filterMicroOrder(theoreticalUSD, deltaWeight, input, dust)
	action := ""
	if orderUSD > 0 {
		action = ActionBuy
		orderUSD = math.Min(orderUSD, input.SpendableUSDT)
	} else if orderUSD < 0 {
		action = ActionSell
	}

	if math.Abs(orderUSD) < dust && !forced {
		orderUSD = 0
		action = ""
	}

	return MicroDecisionOutput{
		Action:          action,
		Signal:          signal,
		CurrentWeight:   currentWeight,
		TargetWeight:    targetWeight,
		DeltaWeight:     deltaWeight,
		TheoreticalUSD:  theoreticalUSD,
		OrderUSDT:       RoundToUSDT(orderUSD),
		Exponent:        exponent,
		VolatilityRatio: volatilityRatio,
		AISignalScalar:  aiSignal,
		ForcedMinimum:   forced,
	}
}

func computeMicroSignal(closes []float64, input MicroDecisionInput) (float64, bool) {
	if len(closes) < MicroSignalEMABars+1 {
		return 0, false
	}
	price := closes[len(closes)-1]
	if price <= 0 {
		return 0, false
	}

	ema := EMA(closes, MicroSignalEMABars)
	if ema <= 0 {
		return 0, false
	}

	logReturns := make([]float64, 0, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		if closes[i-1] <= 0 || closes[i] <= 0 {
			continue
		}
		logReturns = append(logReturns, math.Log(closes[i]/closes[i-1]))
	}

	sigmaFloor := input.SigmaFloor
	if sigmaFloor <= 0 {
		sigmaFloor = 0.001
	}
	sigma := math.Max(StdDev(logReturns, MicroSignalStdDevBars), sigmaFloor)
	if sigma <= 0 {
		return 0, false
	}

	meanReversion := (price/ema - 1) / sigma
	momentum := 0.0
	if len(closes) > MicroSignalEMABars && closes[len(closes)-1-MicroSignalEMABars] > 0 {
		momentum = math.Log(price / closes[len(closes)-1-MicroSignalEMABars])
	}
	breakout := 0.0
	high := rollingHigh(closes, MicroVolRatioLongBars)
	if high > 0 {
		breakout = price/high - 1
	}

	return input.WMean*meanReversion + input.WMomentum*momentum + input.WBreakout*breakout, true
}

func ComputeVolatilityRatio(closes []float64) float64 {
	if len(closes) < 2 {
		return 1
	}
	short := MAVAbsChange(closes, MicroVolRatioShortBars)
	long := MAVAbsChange(closes, MicroVolRatioLongBars)
	if short <= 0 || long <= 0 {
		return 1
	}
	return ClipFloat64(short/long, 0.1, 3.0)
}

func rollingHigh(values []float64, period int) float64 {
	window := tail(values, period)
	if len(window) == 0 {
		return 0
	}
	high := window[0]
	for _, v := range window[1:] {
		if v > high {
			high = v
		}
	}
	return high
}

func filterMicroOrder(theoreticalUSD, deltaWeight float64, input MicroDecisionInput, dust float64) (float64, bool) {
	absUSD := math.Abs(theoreticalUSD)
	if absUSD >= dust {
		return theoreticalUSD, false
	}
	if absUSD <= 0 || input.IsQuiet {
		return 0, false
	}

	wedgeDelta := input.WedgeDeltaThreshold
	if wedgeDelta <= 0 {
		wedgeDelta = 0.04
	}
	wedgeVol := input.WedgeVolRatioThreshold
	if wedgeVol <= 0 {
		wedgeVol = 1.6
	}

	breaksWedge := math.Abs(deltaWeight) >= wedgeDelta || input.VolatilityRatio >= wedgeVol
	if !breaksWedge {
		return 0, false
	}

	if theoreticalUSD > 0 {
		return dust, true
	}
	return -dust, true
}
