package performance

const (
	AnalysisVersion          = "p04-analysis-v1"
	ReportSchemaVersion      = "p04-report-v1"
	SummarySchemaVersion     = "p04-summary-v1"
	ChartSchemaVersion       = "p04-chart-v1"
	AggregationUTCVersion    = "utc-calendar-v1"
	DistributionStatsVersion = "population-moments-linear-quantiles-v1"
	SortinoFormulaVersion    = "annualized-observed-days-v1"
	BetaFormulaVersion       = "aligned-utc-endpoints-covariance-v1"
	AnnualizationVersion     = "cagr-365.2425-v1"
	DefaultHistogramBins     = 20
	MaximumHistogramBins     = 100
)

const (
	PeriodDaily   = "daily"
	PeriodWeekly  = "weekly"
	PeriodMonthly = "monthly"
)

const (
	ChartDistributionDaily   = "return_distribution_daily"
	ChartDistributionWeekly  = "return_distribution_weekly"
	ChartDistributionMonthly = "return_distribution_monthly"
	ChartReturnAccumulation  = "return_accumulation"
	ChartUnderwater          = "underwater"
	ChartExposure            = "exposure"
)

type Point struct {
	TimeMs         int64
	NAV            float64
	BenchmarkNAV   float64
	ActualExposure float64
}

type SeriesPoint struct {
	TimeMs int64   `json:"time_ms"`
	Value  float64 `json:"value"`
}

type Config struct {
	RiskFreeAnnualRate float64
	HistogramBins      int
}

type RelativePerformance struct {
	FinalNAVRatio                     float64  `json:"final_nav_ratio"`
	LogFinalNAVRatio                  float64  `json:"log_final_nav_ratio"`
	StrategyNoCashFlowAnnualized      *float64 `json:"strategy_no_cash_flow_annualized,omitempty"`
	BenchmarkNoCashFlowAnnualized     *float64 `json:"benchmark_no_cash_flow_annualized,omitempty"`
	NoCashFlowAnnualizedDifference    *float64 `json:"no_cash_flow_annualized_difference,omitempty"`
	AnnualizationFormulaVersion       string   `json:"annualization_formula_version"`
	AnnualizationUsesNoCashFlowResult bool     `json:"annualization_uses_no_cash_flow_result"`
}

type DistributionStats struct {
	Period         string             `json:"period"`
	Count          int                `json:"count"`
	Mean           float64            `json:"mean"`
	Median         float64            `json:"median"`
	StdDev         float64            `json:"std_dev"`
	Skewness       float64            `json:"skewness"`
	ExcessKurtosis float64            `json:"excess_kurtosis"`
	Minimum        float64            `json:"minimum"`
	Maximum        float64            `json:"maximum"`
	Quantiles      map[string]float64 `json:"quantiles"`
	StatsVersion   string             `json:"stats_version"`
}

type UnderwaterStats struct {
	LongestDays       float64 `json:"longest_days"`
	LongestPoints     int     `json:"longest_points"`
	StartedAtMs       int64   `json:"started_at_ms,omitempty"`
	RecoveredAtMs     int64   `json:"recovered_at_ms,omitempty"`
	RecoveryCompleted bool    `json:"recovery_completed"`
}

type SortinoStats struct {
	Value              *float64 `json:"value,omitempty"`
	RiskFreeAnnualRate float64  `json:"risk_free_annual_rate"`
	PeriodsPerYear     float64  `json:"periods_per_year"`
	ObservationCount   int      `json:"observation_count"`
	FormulaVersion     string   `json:"formula_version"`
	UnavailableReason  string   `json:"unavailable_reason,omitempty"`
}

type BetaStats struct {
	Value             *float64 `json:"value,omitempty"`
	ObservationCount  int      `json:"observation_count"`
	FormulaVersion    string   `json:"formula_version"`
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
}

type ExposureStats struct {
	ExposureDaysRatio        float64  `json:"exposure_days_ratio"`
	AverageActualExposure    float64  `json:"average_actual_exposure"`
	ExposureAdjustedReturn   *float64 `json:"exposure_adjusted_return,omitempty"`
	ExposureAdjustedReadable bool     `json:"exposure_adjusted_readable"`
}

type Summary struct {
	SchemaVersion      string                       `json:"schema_version"`
	AnalysisVersion    string                       `json:"analysis_version"`
	AggregationVersion string                       `json:"aggregation_version"`
	Relative           RelativePerformance          `json:"relative_performance"`
	Distributions      map[string]DistributionStats `json:"distributions"`
	LongestUnderwater  UnderwaterStats              `json:"longest_underwater"`
	Sortino            SortinoStats                 `json:"sortino"`
	Beta               BetaStats                    `json:"beta"`
	Exposure           ExposureStats                `json:"exposure"`
}

type HistogramBin struct {
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
	Count int     `json:"count"`
}

type DistributionChart struct {
	SchemaVersion string         `json:"schema_version"`
	Kind          string         `json:"kind"`
	Period        string         `json:"period"`
	Bins          []HistogramBin `json:"bins"`
}

type AccumulationPoint struct {
	TimeMs           int64   `json:"time_ms"`
	DailyReturn      float64 `json:"daily_return"`
	ArithmeticSum    float64 `json:"arithmetic_sum"`
	CompoundedReturn float64 `json:"compounded_return"`
}

type ReturnAccumulationChart struct {
	SchemaVersion string              `json:"schema_version"`
	Kind          string              `json:"kind"`
	Points        []AccumulationPoint `json:"points"`
}

type UnderwaterPoint struct {
	TimeMs         int64   `json:"time_ms"`
	Drawdown       float64 `json:"drawdown"`
	UnderwaterDays float64 `json:"underwater_days"`
}

type UnderwaterChart struct {
	SchemaVersion string            `json:"schema_version"`
	Kind          string            `json:"kind"`
	Points        []UnderwaterPoint `json:"points"`
}

type ExposurePoint struct {
	TimeMs               int64   `json:"time_ms"`
	ActualExposureWeight float64 `json:"actual_exposure_weight"`
}

type ExposureChart struct {
	SchemaVersion string          `json:"schema_version"`
	Kind          string          `json:"kind"`
	Points        []ExposurePoint `json:"points"`
}

type Charts struct {
	DailyDistribution   DistributionChart
	WeeklyDistribution  DistributionChart
	MonthlyDistribution DistributionChart
	Accumulation        ReturnAccumulationChart
	Underwater          UnderwaterChart
	Exposure            ExposureChart
}

type Result struct {
	Summary Summary
	Charts  Charts
}
