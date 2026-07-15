package dynamicparam

import (
	"encoding/json"

	"quantsaas/internal/quant"
)

const (
	TargetSchemaVersion       = "p09-target-v1"
	FeatureSchemaVersion      = "p09-causal-ohlc-v1"
	PredictionSchemaVersion   = "p09-prediction-v1"
	StateSchemaVersion        = "p09-structure-state-v1"
	PolicySchemaVersion       = "p09-dynamic-policy-v1"
	ParameterSpaceVersion     = "p09-dynamic-parameter-space-v1"
	ModelArtifactVersion      = "p09-model-artifact-v1"
	ModelReportVersion        = "p09-model-report-v1"
	ComparisonAdapterVersion  = "p09-comparison-adapter-v1"
	WalkForwardVersion        = "p09-purged-expanding-v1"
	DistributionModelVersion  = "p09-zero-lognormal-mixture-v1"
	ActivityScaleVersion      = "p09-activity-scale-v1"
	TrendDeviationVersion     = "p09-occurrence-trend-v1"
	EffectiveParameterVersion = "p09-effective-parameter-v1"
)

const (
	HorizonOneDay    = 1
	HorizonTwentyDay = 20
)

const (
	RouteExplainable = "explainable_gam"
	RouteTCN         = "causal_tcn"
)

const (
	TargetDirection    = "direction"
	TargetJointSpace   = "joint_space"
	TargetPathActivity = "path_activity"
)

type TargetPoint struct {
	SchemaVersion  string         `json:"schema_version"`
	Index          int            `json:"index"`
	TimeMs         int64          `json:"time_ms"`
	Horizon        int            `json:"horizon"`
	DirectionUp    bool           `json:"direction_up"`
	UpSpace        float64        `json:"up_space"`
	DownSpace      float64        `json:"down_space"`
	TotalSpace     float64        `json:"total_space"`
	CausalMedian   float64        `json:"causal_median,omitempty"`
	NormalizedUp   float64        `json:"normalized_up,omitempty"`
	NormalizedDown float64        `json:"normalized_down,omitempty"`
	Normalized     bool           `json:"normalized"`
	FutureActivity ActivityVector `json:"future_activity"`
}

type ActivityVector struct {
	TRMean        float64 `json:"tr_mean"`
	TRStdDev      float64 `json:"tr_std_dev"`
	HighLowMean   float64 `json:"high_low_mean"`
	HighLowStdDev float64 `json:"high_low_std_dev"`
	Parkinson     float64 `json:"parkinson"`
	YangZhang     float64 `json:"yang_zhang"`
}

type FeaturePoint struct {
	SchemaVersion     string         `json:"schema_version"`
	Index             int            `json:"index"`
	TimeMs            int64          `json:"time_ms"`
	Lookback          int            `json:"lookback"`
	Activity          ActivityVector `json:"activity"`
	HistoryRatio      ActivityVector `json:"history_ratio"`
	RawSequence       [][]float64    `json:"raw_sequence,omitempty"`
	Available         bool           `json:"available"`
	UnavailableReason string         `json:"unavailable_reason,omitempty"`
}

type ZeroMass struct {
	BothZero     float64 `json:"both_zero"`
	UpOnly       float64 `json:"up_only"`
	DownOnly     float64 `json:"down_only"`
	BothPositive float64 `json:"both_positive"`
}

type BivariateLognormalComponent struct {
	Weight      float64 `json:"weight"`
	MeanLogUp   float64 `json:"mean_log_up"`
	MeanLogDown float64 `json:"mean_log_down"`
	VarLogUp    float64 `json:"var_log_up"`
	VarLogDown  float64 `json:"var_log_down"`
	Covariance  float64 `json:"covariance"`
}

type UnivariateLognormalComponent struct {
	Weight   float64 `json:"weight"`
	MeanLog  float64 `json:"mean_log"`
	Variance float64 `json:"variance"`
}

type JointDistribution struct {
	SchemaVersion string                         `json:"schema_version"`
	ZeroMass      ZeroMass                       `json:"zero_mass"`
	BothPositive  []BivariateLognormalComponent  `json:"both_positive"`
	UpOnly        []UnivariateLognormalComponent `json:"up_only"`
	DownOnly      []UnivariateLognormalComponent `json:"down_only"`
}

type SixRegionProbabilities struct {
	UpSmall       float64 `json:"up_small"`
	UpLarge       float64 `json:"up_large"`
	BalancedSmall float64 `json:"balanced_small"`
	BalancedLarge float64 `json:"balanced_large"`
	DownSmall     float64 `json:"down_small"`
	DownLarge     float64 `json:"down_large"`
}

type Prediction struct {
	SchemaVersion          string                 `json:"schema_version"`
	Index                  int                    `json:"index"`
	TimeMs                 int64                  `json:"time_ms"`
	Horizon                int                    `json:"horizon"`
	DirectionUpProbability float64                `json:"direction_up_probability"`
	JointDistribution      JointDistribution      `json:"joint_distribution"`
	SixRegions             SixRegionProbabilities `json:"six_regions"`
	SpaceState             SpaceHardState         `json:"space_state"`
	PathActivity           ActivityVector         `json:"path_activity"`
	NormalActivityScale    float64                `json:"normal_activity_scale,omitempty"`
	Available              bool                   `json:"available"`
	UnavailableReason      string                 `json:"unavailable_reason,omitempty"`
}

type MaterializedPrediction struct {
	SchemaVersion string       `json:"schema_version"`
	ArtifactHash  string       `json:"artifact_hash"`
	DatasetHash   string       `json:"dataset_hash"`
	Predictions   []Prediction `json:"predictions"`
}

type ModelArtifact struct {
	SchemaVersion        string          `json:"schema_version"`
	Route                string          `json:"route"`
	Horizon              int             `json:"horizon"`
	Lookback             int             `json:"lookback"`
	TargetKind           string          `json:"target_kind"`
	FeatureSchemaVersion string          `json:"feature_schema_version"`
	DistributionVersion  string          `json:"distribution_version"`
	DatasetHash          string          `json:"dataset_hash"`
	TrainStartMs         int64           `json:"train_start_ms"`
	TrainEndMs           int64           `json:"train_end_ms"`
	Parameters           json.RawMessage `json:"parameters"`
	ContentHash          string          `json:"content_hash"`
}

type ValidationFold struct {
	Fold                      int     `json:"fold"`
	TrainStartIndex           int     `json:"train_start_index"`
	TrainEndIndex             int     `json:"train_end_index"`
	ValidationStartIndex      int     `json:"validation_start_index"`
	ValidationEndIndex        int     `json:"validation_end_index"`
	Purge                     int     `json:"purge"`
	Loss                      float64 `json:"loss"`
	BaselineLoss              float64 `json:"baseline_loss"`
	CalibrationError          float64 `json:"calibration_error"`
	BaselineCalibrationError  float64 `json:"baseline_calibration_error"`
	MarginalUpError           float64 `json:"marginal_up_error,omitempty"`
	BaselineMarginalUpError   float64 `json:"baseline_marginal_up_error,omitempty"`
	MarginalDownError         float64 `json:"marginal_down_error,omitempty"`
	BaselineMarginalDownError float64 `json:"baseline_marginal_down_error,omitempty"`
	ZeroTypeBrier             float64 `json:"zero_type_brier,omitempty"`
	BaselineZeroTypeBrier     float64 `json:"baseline_zero_type_brier,omitempty"`
	SixRegionBrier            float64 `json:"six_region_brier,omitempty"`
	BaselineSixRegionBrier    float64 `json:"baseline_six_region_brier,omitempty"`
	CriteriaPassed            bool    `json:"criteria_passed"`
	Support                   int     `json:"support"`
}

type ModelReport struct {
	SchemaVersion         string           `json:"schema_version"`
	ArtifactHash          string           `json:"artifact_hash"`
	Route                 string           `json:"route"`
	Horizon               int              `json:"horizon"`
	TargetKind            string           `json:"target_kind"`
	WalkForwardVersion    string           `json:"walk_forward_version"`
	Folds                 []ValidationFold `json:"folds"`
	MeanLoss              float64          `json:"mean_loss"`
	StandardError         float64          `json:"standard_error"`
	MeanBaselineLoss      float64          `json:"mean_baseline_loss"`
	MeanMarginalUpError   float64          `json:"mean_marginal_up_error,omitempty"`
	MeanMarginalDownError float64          `json:"mean_marginal_down_error,omitempty"`
	MeanZeroTypeBrier     float64          `json:"mean_zero_type_brier,omitempty"`
	MeanSixRegionBrier    float64          `json:"mean_six_region_brier,omitempty"`
	MeanReliabilityError  float64          `json:"mean_reliability_error,omitempty"`
	BaselineGatePassed    bool             `json:"baseline_gate_passed"`
	PredictiveStatus      string           `json:"predictive_status"`
	Reliability           []CalibrationBin `json:"reliability,omitempty"`
	ContentHash           string           `json:"content_hash"`
}

type EffectiveSnapshot struct {
	SchemaVersion  string                  `json:"schema_version"`
	Index          int                     `json:"index"`
	TimeMs         int64                   `json:"time_ms"`
	State          StructureState          `json:"state"`
	Chromosome     quant.Chromosome        `json:"chromosome"`
	Contributions  map[string]Contribution `json:"contributions"`
	FallbackEvents []string                `json:"fallback_events,omitempty"`
}

type Contribution struct {
	Mode       string             `json:"mode"`
	BaseValue  float64            `json:"base_value"`
	Terms      map[string]float64 `json:"terms,omitempty"`
	FinalValue float64            `json:"final_value"`
}
