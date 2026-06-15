package quant

const (
	ActionBuy  = "BUY"
	ActionSell = "SELL"

	EngineMacro = "MACRO"
	EngineMicro = "MICRO"

	LotTypeDeadStack  = "DEAD_STACK"
	LotTypeFloating   = "FLOATING"
	LotTypeColdSealed = "COLD_SEALED"
)

type Bar struct {
	OpenTime int64
	Open     float64
	High     float64
	Low      float64
	Close    float64
	Volume   float64
}

type AISignalVector struct {
	SMarket    float64 `json:"s_market"`
	SNews      float64 `json:"s_news"`
	SSentiment float64 `json:"s_sentiment"`
}

type PortfolioSnapshot struct {
	USDTBalance   float64
	DeadBTC       float64
	FloatBTC      float64
	ColdSealedBTC float64
	TotalEquity   float64
}

type CapitalPolicy struct {
	InitialUSDT       float64 `json:"initial_usdt"`
	MonthlyInjectUSDT float64 `json:"monthly_inject_usdt"`
	ColdSealedBTC     float64 `json:"cold_sealed_btc"`
}

type RiskBounds struct {
	MaxDrawdownPct float64 `json:"max_drawdown_pct"`
	FeeRate        float64 `json:"fee_rate"`
	LotStep        float64 `json:"lot_step"`
	LotMin         float64 `json:"lot_min"`
}

type SpawnPoint struct {
	Policy CapitalPolicy `json:"policy"`
	Risk   RiskBounds    `json:"risk"`
}

type StrategyInput struct {
	Symbol               string
	Interval             string
	Closes               []float64
	Timestamps           []int64
	Portfolio            PortfolioSnapshot
	Lots                 []SpotLot
	RuntimeState         map[string]any
	Params               map[string]float64
	Spawn                SpawnPoint
	AISignal             AISignalVector
	LastProcessedBarTime int64
}

type TradeIntent struct {
	Action     string
	Engine     string
	Symbol     string
	AmountUSDT float64
	QtyAsset   float64
	LotType    string
	Reason     string
}

type LotTransfer struct {
	FromLotType string
	ToLotType   string
	Amount      float64
	Reason      string
}

type StrategyOutput struct {
	Intents      []TradeIntent
	LotTransfers []LotTransfer
	RuntimeState map[string]any
	Diagnostics  map[string]float64
}
