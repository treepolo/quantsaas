package robustness

import "errors"

const (
	MetricsVersion      = "p08-relative-metrics-v1"
	GridVersion         = "p08-grid-v1"
	AnalysisVersion     = "p08-analysis-v1"
	ConnectivityVersion = "p08-axis-connectivity-v1"
	DistanceVersion     = "p08-grid-distance-v1"
	FrontierVersion     = "p08-pareto-v1"
	CenterVersion       = "p08-center-v1"
	SamplingVersion     = "p08-halton-sampling-v1"
)

var (
	ErrInvalidMetricInput = errors.New("相對績效指標輸入無效")
	ErrInvalidSchema      = errors.New("參數空間 schema 無效")
	ErrInvalidPoint       = errors.New("評估點無效")
)

type MetricName string

const (
	MetricLogFinalNAVRatio      MetricName = "log_final_nav_ratio"
	MetricDrawdownResidualRatio MetricName = "drawdown_residual_ratio"
	MetricLogDrawdownResidual   MetricName = "log_drawdown_residual_ratio"
	MetricPerformanceDrawdown   MetricName = "performance_drawdown_composite"
	MetricQualification         MetricName = "qualification"
)

type RelativeMetricInput struct {
	StrategyFinalNAV     float64 `json:"strategy_final_nav"`
	BenchmarkFinalNAV    float64 `json:"benchmark_final_nav"`
	StrategyMaxDrawdown  float64 `json:"strategy_max_drawdown"`
	BenchmarkMaxDrawdown float64 `json:"benchmark_max_drawdown"`
}

type RelativeMetrics struct {
	Version                  string  `json:"version"`
	FinalNAVRatio            float64 `json:"final_nav_ratio"`
	LogFinalNAVRatio         float64 `json:"log_final_nav_ratio"`
	DrawdownResidualRatio    float64 `json:"drawdown_residual_ratio"`
	LogDrawdownResidualRatio float64 `json:"log_drawdown_residual_ratio"`
	PerformanceDrawdown      float64 `json:"performance_drawdown_composite"`
	Qualified                bool    `json:"qualified"`
}

func (m RelativeMetrics) Value(name MetricName) float64 {
	switch name {
	case MetricDrawdownResidualRatio:
		return m.DrawdownResidualRatio
	case MetricLogDrawdownResidual:
		return m.LogDrawdownResidualRatio
	case MetricPerformanceDrawdown:
		return m.PerformanceDrawdown
	case MetricQualification:
		if m.Qualified {
			return 1
		}
		return 0
	default:
		return m.LogFinalNAVRatio
	}
}

func ValidMetric(name MetricName) bool {
	switch name {
	case MetricLogFinalNAVRatio, MetricDrawdownResidualRatio, MetricLogDrawdownResidual, MetricPerformanceDrawdown, MetricQualification:
		return true
	default:
		return false
	}
}

type ParameterType string

const (
	ParameterFloat ParameterType = "float"
	ParameterInt   ParameterType = "int"
)

// ParameterAxis uses explicit legal values. Connectivity always compares the
// index in this sequence, never the raw numeric distance.
type ParameterAxis struct {
	Name       string        `json:"name"`
	Label      string        `json:"label"`
	Type       ParameterType `json:"type"`
	Values     []float64     `json:"values"`
	LegalMin   float64       `json:"legal_min"`
	LegalMax   float64       `json:"legal_max"`
	Step       float64       `json:"step"`
	StudyStart int           `json:"study_start"`
	StudyEnd   int           `json:"study_end"`
}

type ParameterSpace struct {
	SchemaVersion       string             `json:"schema_version"`
	Axes                []ParameterAxis    `json:"axes"`
	Fixed               map[string]float64 `json:"fixed"`
	ExcludedCoordinates [][]int            `json:"excluded_coordinates,omitempty"`
}

type PointKind string

const (
	PointActual    PointKind = "actual"
	PointPredicted PointKind = "predicted"
	PointProposed  PointKind = "proposed"
)

type PointState string

const (
	PointQualified   PointState = "qualified"
	PointUnqualified PointState = "unqualified"
	PointUnknown     PointState = "unknown"
)

type EvaluationPoint struct {
	ID               string             `json:"id"`
	Kind             PointKind          `json:"kind"`
	State            PointState         `json:"state"`
	Coordinates      []int              `json:"coordinates"`
	Parameters       map[string]float64 `json:"parameters"`
	Metrics          *RelativeMetrics   `json:"metrics,omitempty"`
	BacktestResultID uint               `json:"backtest_result_id,omitempty"`
	SourceStage      string             `json:"source_stage,omitempty"`
	SamplingBatch    string             `json:"sampling_batch,omitempty"`
}

type ScaleStatistic struct {
	Radius             int      `json:"radius"`
	ExpectedPoints     int      `json:"expected_points"`
	ObservedPoints     int      `json:"observed_points"`
	UnknownPoints      int      `json:"unknown_points"`
	QualifiedPoints    int      `json:"qualified_points"`
	QualificationRatio float64  `json:"qualification_ratio"`
	AreaRatio          float64  `json:"area_ratio"`
	Mean               float64  `json:"mean"`
	Median             float64  `json:"median"`
	StandardDeviation  float64  `json:"standard_deviation"`
	CenterToMean       *float64 `json:"center_to_mean,omitempty"`
	CenterToMedian     *float64 `json:"center_to_median,omitempty"`
	Complete           bool     `json:"complete"`
}

type StopReason string

const (
	StopObservedFailure  StopReason = "observed_failure"
	StopUnknownGap       StopReason = "unknown_gap"
	StopResearchBoundary StopReason = "research_boundary"
	StopLegalBoundary    StopReason = "legal_boundary"
)

type DirectionTolerance struct {
	Axis      string     `json:"axis"`
	Direction string     `json:"direction"`
	Steps     int        `json:"steps"`
	Stop      StopReason `json:"stop_reason"`
}

type PointGeometry struct {
	PointID                string               `json:"point_id"`
	Directions             []DirectionTolerance `json:"directions"`
	AxisFailureDepth       int                  `json:"axis_failure_depth"`
	AxisFailureExact       bool                 `json:"axis_failure_exact"`
	GuaranteedBoxRadius    int                  `json:"guaranteed_box_radius"`
	GuaranteedBoxExact     bool                 `json:"guaranteed_box_exact"`
	NeighborhoodQuality    float64              `json:"neighborhood_quality"`
	NeighborhoodStability  float64              `json:"neighborhood_stability"`
	NeighborhoodDispersion float64              `json:"neighborhood_dispersion"`
	MedoidCost             int                  `json:"medoid_cost"`
	Completeness           string               `json:"completeness"`
	TruncationReasons      []string             `json:"truncation_reasons,omitempty"`
}

type ConnectedRegion struct {
	ID          string          `json:"id"`
	PointIDs    []string        `json:"point_ids"`
	Geometries  []PointGeometry `json:"geometries"`
	FrontierIDs []string        `json:"frontier_ids"`
	CenterIDs   []string        `json:"center_ids"`
	Proposals   []RoleProposal  `json:"proposals"`
}

type RoleProposal struct {
	PointID     string   `json:"point_id"`
	Roles       []string `json:"roles"`
	Provisional bool     `json:"provisional"`
}

type AnalysisResult struct {
	AnalysisVersion      string            `json:"analysis_version"`
	ConnectivityVersion  string            `json:"connectivity_version"`
	DistanceVersion      string            `json:"distance_version"`
	FrontierVersion      string            `json:"frontier_version"`
	CenterVersion        string            `json:"center_version"`
	Metric               MetricName        `json:"metric"`
	CenterPointID        string            `json:"center_point_id,omitempty"`
	Points               []EvaluationPoint `json:"points"`
	Scales               []ScaleStatistic  `json:"scales"`
	Regions              []ConnectedRegion `json:"regions"`
	MissingCoordinates   [][]int           `json:"missing_coordinates"`
	ObservedPointSetHash string            `json:"observed_point_set_hash,omitempty"`
}
