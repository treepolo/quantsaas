package perturbation

const (
	AlgorithmVersion  = "p13-ohlc-log-tr-triangular-v1"
	HashDomain        = "quantsaas:L:ohlc-log-tr-triangular:v1"
	SnapshotSchema    = "p13-perturbation-source-v1"
	OutputSchema      = "p13-perturbation-output-v1"
	RecipeSchema      = "p13-perturbation-recipe-v1"
	StatisticsVersion = "p13-linear-quantile-population-v1"
	MetricVersion     = "p13-relative-performance-v1"
	AnalysisSchema    = "p13-perturbation-analysis-v1"
)

type SourceIdentity struct {
	InstrumentID string `json:"instrument_id"`
	DataSource   string `json:"data_source"`
	Symbol       string `json:"symbol"`
	Interval     string `json:"interval"`
}

type Bar struct {
	OpenTime int64   `json:"open_time"`
	Open     float64 `json:"open"`
	High     float64 `json:"high"`
	Low      float64 `json:"low"`
	Close    float64 `json:"close"`
	Volume   float64 `json:"volume"`
}

type Coordinates struct {
	Middle    float64
	Body      float64
	UpperWick float64
	LowerWick float64
}

type Deviation struct {
	Open  float64 `json:"open"`
	High  float64 `json:"high"`
	Low   float64 `json:"low"`
	Close float64 `json:"close"`
	Bar   float64 `json:"bar"`
}

type DeviationSummary struct {
	Median   float64 `json:"median"`
	P95      float64 `json:"p95"`
	Maximum  float64 `json:"maximum"`
	OpenMax  float64 `json:"open_max"`
	HighMax  float64 `json:"high_max"`
	LowMax   float64 `json:"low_max"`
	CloseMax float64 `json:"close_max"`
}

type Generated struct {
	Bars       []Bar            `json:"bars"`
	Deviations []Deviation      `json:"deviations"`
	Summary    DeviationSummary `json:"summary"`
}

type DescriptiveStats struct {
	Available bool    `json:"available"`
	Count     int     `json:"count"`
	Mean      float64 `json:"mean,omitempty"`
	Median    float64 `json:"median,omitempty"`
	StdDev    float64 `json:"std_dev,omitempty"`
	Minimum   float64 `json:"minimum,omitempty"`
	Maximum   float64 `json:"maximum,omitempty"`
	P05       float64 `json:"p05,omitempty"`
	P25       float64 `json:"p25,omitempty"`
	P75       float64 `json:"p75,omitempty"`
	P95       float64 `json:"p95,omitempty"`
}

type RelativeMetrics struct {
	FinalNAVRatio                *float64 `json:"final_nav_ratio,omitempty"`
	LogFinalNAVRatio             *float64 `json:"log_final_nav_ratio,omitempty"`
	DrawdownResidualRatio        *float64 `json:"drawdown_residual_ratio,omitempty"`
	LogDrawdownResidualRatio     *float64 `json:"log_drawdown_residual_ratio,omitempty"`
	PerformanceDrawdownComposite *float64 `json:"performance_drawdown_composite,omitempty"`
	Qualification                string   `json:"qualification"`
	UnavailableReason            string   `json:"unavailable_reason,omitempty"`
}

const (
	QualificationQualified      = "qualified"
	QualificationReturnFailed   = "return_failed_only"
	QualificationDrawdownFailed = "drawdown_failed_only"
	QualificationBothFailed     = "both_failed"
)
