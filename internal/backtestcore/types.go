package backtestcore

import (
	"fmt"
	"math"
	"strings"

	"quantsaas/internal/quant"
)

const CoreVersion = "p07-v1"

const (
	LongTermFilterVersion       = "p07-long-term-filter-v1"
	DefaultLongTermFilterMonths = 10
)

const (
	RunnerSigmoidDCA     = "sigmoid_dca"
	RunnerRule           = "rule"
	RunnerExposureReplay = "exposure_replay"
)

const (
	ExecutionModeCloseSameBar     = "close_same_bar"
	ExecutionModeCloseNextOpen    = "close_next_open"
	ExecutionModeOpenBuyCloseSell = "open_buy_close_sell"
)

const (
	PrefixModeExecute     = "execute"
	PrefixModeHistoryOnly = "history_only"
)

type Spec struct {
	Runner               string                    `json:"runner"`
	InstrumentID         string                    `json:"instrument_id,omitempty"`
	Symbol               string                    `json:"symbol"`
	DataSource           string                    `json:"data_source,omitempty"`
	Interval             string                    `json:"interval"`
	ExecutionMode        string                    `json:"execution_mode"`
	PositionStructure    string                    `json:"position_structure,omitempty"`
	StartTimeMs          int64                     `json:"start_time_ms"`
	EndTimeMs            int64                     `json:"end_time_ms"`
	EvaluationStartMs    int64                     `json:"evaluation_start_ms"`
	EvaluationEndMs      int64                     `json:"evaluation_end_ms"`
	EvaluationStartIndex int                       `json:"evaluation_start_index"`
	EvaluationEndIndex   int                       `json:"evaluation_end_index"`
	PrefixMode           string                    `json:"prefix_mode"`
	InitialCapital       float64                   `json:"initial_capital"`
	MonthlyContribution  float64                   `json:"monthly_contribution"`
	InitialAssetQuantity float64                   `json:"initial_asset_quantity,omitempty"`
	MinimumTradeUSD      float64                   `json:"minimum_trade_usd,omitempty"`
	MinimumAssetQuantity float64                   `json:"minimum_asset_quantity,omitempty"`
	Costs                quant.ExecutionCostConfig `json:"costs"`
	LongTermFilter       LongTermFilterConfig      `json:"long_term_filter"`
	DatasetHash          string                    `json:"dataset_hash,omitempty"`
	CoreVersion          string                    `json:"core_version"`
}

type LongTermFilterConfig struct {
	Enabled bool   `json:"enabled"`
	Months  int    `json:"months"`
	Version string `json:"version"`
}

type CostSummary struct {
	FeeCost      float64 `json:"fee_cost"`
	SlippageCost float64 `json:"slippage_cost"`
	TotalCost    float64 `json:"total_cost"`
}

func (s *CostSummary) Add(other CostSummary) {
	s.FeeCost += other.FeeCost
	s.SlippageCost += other.SlippageCost
	s.TotalCost += other.TotalCost
}

type TradeSummary struct {
	TradeCount   int         `json:"trade_count"`
	BuyCount     int         `json:"buy_count"`
	SellCount    int         `json:"sell_count"`
	BuyNotional  float64     `json:"buy_notional"`
	SellNotional float64     `json:"sell_notional"`
	Costs        CostSummary `json:"costs"`
}

func (s *TradeSummary) Add(other TradeSummary) {
	s.TradeCount += other.TradeCount
	s.BuyCount += other.BuyCount
	s.SellCount += other.SellCount
	s.BuyNotional += other.BuyNotional
	s.SellNotional += other.SellNotional
	s.Costs.Add(other.Costs)
}

type ParameterMetadata struct {
	StructureState   string `json:"structure_state,omitempty"`
	OccurrenceID     string `json:"occurrence_id,omitempty"`
	ModelVersion     string `json:"model_version,omitempty"`
	PolicyVersion    string `json:"policy_version,omitempty"`
	MaterializedHash string `json:"materialized_hash,omitempty"`
	FallbackEvent    string `json:"fallback_event,omitempty"`
}

type EffectiveParameters struct {
	Chromosome quant.Chromosome  `json:"chromosome"`
	Metadata   ParameterMetadata `json:"metadata,omitempty"`
}

type ParameterContext struct {
	Index      int
	Bar        quant.Bar
	Closes     []float64
	Timestamps []int64
}

type ParameterProvider func(ParameterContext) (EffectiveParameters, error)

type NAVPoint struct {
	TimeMs                           int64                `json:"time_ms"`
	Price                            float64              `json:"price"`
	TotalEquity                      float64              `json:"total_equity"`
	Cash                             float64              `json:"cash"`
	AssetQuantity                    float64              `json:"asset_quantity"`
	ActualExposureWeight             float64              `json:"actual_exposure_weight"`
	IntradayExposureWeight           float64              `json:"intraday_exposure_weight,omitempty"`
	DailyReturn                      float64              `json:"daily_return"`
	PracticalTotalEquity             float64              `json:"practical_total_equity"`
	PracticalCash                    float64              `json:"practical_cash"`
	PracticalAssetQuantity           float64              `json:"practical_asset_quantity"`
	PracticalActualExposureWeight    float64              `json:"practical_actual_exposure_weight"`
	PracticalDailyReturn             float64              `json:"practical_daily_return"`
	PracticalTargetWeight            float64              `json:"practical_target_weight"`
	PracticalTargetWeightChange      float64              `json:"practical_target_weight_change"`
	ModelTargetWeight                float64              `json:"model_target_weight"`
	ModelTargetWeightChange          float64              `json:"model_target_weight_change"`
	EmptyReferenceTargetWeight       float64              `json:"empty_reference_target_weight,omitempty"`
	EmptyReferenceTargetWeightChange float64              `json:"empty_reference_target_weight_change,omitempty"`
	Trades                           TradeSummary         `json:"trades"`
	PracticalTrades                  TradeSummary         `json:"practical_trades"`
	LongTermFilterEnabled            bool                 `json:"long_term_filter_enabled"`
	LongTermFilterReady              bool                 `json:"long_term_filter_ready"`
	LongTermFilterRiskOff            bool                 `json:"long_term_filter_risk_off"`
	LongTermFilterCurrentSMA         float64              `json:"long_term_filter_current_sma,omitempty"`
	LongTermFilterPreviousSMA        float64              `json:"long_term_filter_previous_sma,omitempty"`
	LongTermFilterSignal             string               `json:"long_term_filter_signal,omitempty"`
	LongTermFilterEvent              string               `json:"long_term_filter_event,omitempty"`
	EffectiveParameters              *EffectiveParameters `json:"effective_parameters,omitempty"`
}

type Result struct {
	Conditions           Spec                  `json:"conditions"`
	Path                 []NAVPoint            `json:"path"`
	FinalAssets          float64               `json:"final_assets"`
	TotalReturn          float64               `json:"total_return"`
	TradeCount           int                   `json:"trade_count"`
	Costs                CostSummary           `json:"costs"`
	TotalInjected        float64               `json:"total_injected"`
	EvaluationInitial    float64               `json:"evaluation_initial"`
	EvaluationStartMs    int64                 `json:"evaluation_start_ms"`
	EvaluationEndMs      int64                 `json:"evaluation_end_ms"`
	CashFlows            []quant.TimedCashFlow `json:"cash_flows,omitempty"`
	PracticalFinalAssets float64               `json:"practical_final_assets"`
	PracticalTotalReturn float64               `json:"practical_total_return"`
	PracticalTradeCount  int                   `json:"practical_trade_count"`
	PracticalCosts       CostSummary           `json:"practical_costs"`
}

type StepEvent struct {
	Index         int
	Bar           quant.Bar
	ExecutionMode string
	Portfolio     quant.PortfolioSnapshot
	Output        quant.StrategyOutput
	TotalEquity   float64
}

type Hooks struct {
	ComputeStep func(int64)
	OnStep      func(StepEvent)
}

func normalizeSpec(spec Spec, bars []quant.Bar, runner string) (Spec, error) {
	if len(bars) == 0 {
		return Spec{}, fmt.Errorf("回測 K 線不可為空")
	}
	spec.Runner = runner
	spec.Symbol = strings.TrimSpace(spec.Symbol)
	if spec.Symbol == "" {
		spec.Symbol = "BTCUSDT"
	}
	spec.Interval = strings.TrimSpace(spec.Interval)
	if spec.Interval == "" {
		spec.Interval = "1d"
	}
	switch spec.ExecutionMode {
	case "", ExecutionModeCloseSameBar:
		spec.ExecutionMode = ExecutionModeCloseSameBar
	case ExecutionModeCloseNextOpen, ExecutionModeOpenBuyCloseSell:
	default:
		return Spec{}, fmt.Errorf("不支援的 execution mode: %s", spec.ExecutionMode)
	}
	switch spec.PrefixMode {
	case "", PrefixModeExecute:
		spec.PrefixMode = PrefixModeExecute
	case PrefixModeHistoryOnly:
	default:
		return Spec{}, fmt.Errorf("不支援的歷史前綴模式: %s", spec.PrefixMode)
	}
	if spec.InitialCapital <= 0 || math.IsNaN(spec.InitialCapital) || math.IsInf(spec.InitialCapital, 0) {
		spec.InitialCapital = 1000
	}
	if spec.MonthlyContribution < 0 || math.IsNaN(spec.MonthlyContribution) || math.IsInf(spec.MonthlyContribution, 0) {
		return Spec{}, fmt.Errorf("每月投入不可為負數")
	}
	if spec.InitialAssetQuantity < 0 || math.IsNaN(spec.InitialAssetQuantity) || math.IsInf(spec.InitialAssetQuantity, 0) {
		return Spec{}, fmt.Errorf("期初資產數量不可為負數")
	}
	if spec.MinimumTradeUSD < 0 || math.IsNaN(spec.MinimumTradeUSD) || math.IsInf(spec.MinimumTradeUSD, 0) {
		return Spec{}, fmt.Errorf("最小交易額不可為負數")
	}
	if spec.MinimumAssetQuantity < 0 || math.IsNaN(spec.MinimumAssetQuantity) || math.IsInf(spec.MinimumAssetQuantity, 0) {
		return Spec{}, fmt.Errorf("最小資產數量不可為負數")
	}
	spec.Costs = quant.NormalizeExecutionCosts(spec.Costs)
	if spec.LongTermFilter.Enabled {
		if runner != RunnerSigmoidDCA {
			return Spec{}, fmt.Errorf("長週期風險濾網目前只支援實務策略回測")
		}
		if spec.Interval != "1d" {
			return Spec{}, fmt.Errorf("長週期風險濾網只支援日 K")
		}
		if spec.LongTermFilter.Months <= 0 {
			return Spec{}, fmt.Errorf("長週期風險濾網月線長度必須大於 0")
		}
		if strings.TrimSpace(spec.LongTermFilter.Version) == "" {
			spec.LongTermFilter.Version = LongTermFilterVersion
		}
	}
	spec.CoreVersion = CoreVersion
	if spec.StartTimeMs == 0 {
		spec.StartTimeMs = bars[0].OpenTime
	}
	if spec.EndTimeMs == 0 {
		spec.EndTimeMs = bars[len(bars)-1].OpenTime
	}
	if spec.StartTimeMs > spec.EndTimeMs {
		return Spec{}, fmt.Errorf("回測起點不可晚於終點")
	}
	if spec.StartTimeMs < bars[0].OpenTime || spec.EndTimeMs > bars[len(bars)-1].OpenTime {
		return Spec{}, fmt.Errorf("回測起訖必須落在輸入 K 線範圍內")
	}
	if spec.EvaluationStartMs == 0 {
		spec.EvaluationStartMs = spec.StartTimeMs
	}
	if spec.EvaluationEndMs == 0 {
		spec.EvaluationEndMs = spec.EndTimeMs
	}
	if spec.EvaluationStartMs > spec.EvaluationEndMs {
		return Spec{}, fmt.Errorf("正式評估起點不可晚於終點")
	}
	if spec.EvaluationStartMs < spec.StartTimeMs || spec.EvaluationEndMs > spec.EndTimeMs {
		return Spec{}, fmt.Errorf("正式評估區間必須落在回測起訖內")
	}
	previous := int64(0)
	for i, bar := range bars {
		if i > 0 && bar.OpenTime <= previous {
			return Spec{}, fmt.Errorf("K 線時間必須嚴格遞增")
		}
		previous = bar.OpenTime
	}
	return spec, nil
}

func isEvaluationBar(spec Spec, bar quant.Bar) bool {
	return bar.OpenTime >= spec.EvaluationStartMs && bar.OpenTime <= spec.EvaluationEndMs
}

func actualExposure(portfolio quant.PortfolioSnapshot, price float64) float64 {
	assetQuantity := portfolio.DeadBTC + portfolio.FloatBTC + portfolio.ColdSealedBTC
	equity := portfolio.USDTBalance + assetQuantity*price
	return quant.ActualExposureWeight(assetQuantity, price, equity)
}

func totalAssetQuantity(portfolio quant.PortfolioSnapshot) float64 {
	return portfolio.DeadBTC + portfolio.FloatBTC + portfolio.ColdSealedBTC
}
