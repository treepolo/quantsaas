package klineinverse

import (
	"errors"
	"fmt"
	"math"
)

const (
	CoordinateVersion = "p12-gbud-v1"
	FeatureVersion    = "p12-behavior-20-v1"
	DistanceVersion   = "p12-multiresolution-distance-v1"
	CVTVersion        = "p12-cvt-farthest-lloyd-v1"
	SearchVersion     = "p12-mome-v1"
	VariationVersion  = "p12-five-operations-v1"
	StateVersion      = "p12-ab-state-v1"
	RNGVersion        = "p12-counter-splitmix64-v1"
)

var ErrInvalidPath = errors.New("K 線生成路徑無效")

type Coordinate struct {
	G float64 `json:"g"`
	B float64 `json:"b"`
	U float64 `json:"u"`
	D float64 `json:"d"`
}

type Bounds struct {
	GMin float64 `json:"g_min"`
	GMax float64 `json:"g_max"`
	BMin float64 `json:"b_min"`
	BMax float64 `json:"b_max"`
	UMin float64 `json:"u_min"`
	UMax float64 `json:"u_max"`
	DMin float64 `json:"d_min"`
	DMax float64 `json:"d_max"`
}

type Path struct {
	WarmupLength     int          `json:"warmup_length"`
	EvaluationLength int          `json:"evaluation_length"`
	Dates            []int64      `json:"dates"`
	Coordinates      []Coordinate `json:"coordinates"`
}

type OHLC struct {
	TimeMs int64   `json:"time_ms"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
}

type SegmentFeatures struct {
	MeanReturn            float64 `json:"mean_return"`
	TrendEfficiency       float64 `json:"trend_efficiency"`
	DrawdownShape         float64 `json:"drawdown_shape"`
	RunupShape            float64 `json:"runup_shape"`
	DirectionReversal     float64 `json:"direction_reversal"`
	MeanTR                float64 `json:"mean_tr"`
	ActivityConcentration float64 `json:"activity_concentration"`
	GapShare              float64 `json:"gap_share"`
	WickShare             float64 `json:"wick_share"`
	WickAsymmetry         float64 `json:"wick_asymmetry"`
}

type Behavior struct {
	Warmup     SegmentFeatures `json:"warmup"`
	Evaluation SegmentFeatures `json:"evaluation"`
}

type Distance struct {
	Warmup     float64 `json:"d_w"`
	Evaluation float64 `json:"d_h"`
	Total      float64 `json:"d_total"`
}

type FeatureRange struct {
	Min [20]float64 `json:"min"`
	Max [20]float64 `json:"max"`
}

type Outcome struct {
	QRelative float64 `json:"q_rel"`
	QAbsolute float64 `json:"q_abs"`
	State     string  `json:"state"`
	PassA     bool    `json:"pass_a"`
	PassB     bool    `json:"pass_b"`
}

const (
	StateAB                  = "a_and_b"
	StateAOnly               = "a_only"
	StatePositiveButBelowDCA = "positive_but_below_dca"
	StateNeither             = "neither"
)

func (b Bounds) Validate() error {
	values := []float64{b.GMin, b.GMax, b.BMin, b.BMax, b.UMin, b.UMax, b.DMin, b.DMax}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%w：邊界包含非有限數值", ErrInvalidPath)
		}
	}
	if b.GMin > b.GMax || b.BMin > b.BMax || b.UMin < 0 || b.DMin < 0 || b.UMin > b.UMax || b.DMin > b.DMax {
		return fmt.Errorf("%w：邊界上下限不合法", ErrInvalidPath)
	}
	return nil
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
