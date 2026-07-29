package ga

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/quant"
)

const (
	SingleMarketScoringVersion = "ga-crucible-fitness-v1"
	MultiMarketScoringVersion  = "multi-market-log-annualized-v2"
)

type DatasetIdentity struct {
	InstrumentID string `json:"instrument_id"`
	DataSource   string `json:"data_source"`
	Pair         string `json:"pair"`
	Interval     string `json:"interval"`
	StartTimeMs  int64  `json:"start_time_ms"`
	EndTimeMs    int64  `json:"end_time_ms"`
	ContentHash  string `json:"content_hash"`
	BarCount     int    `json:"bar_count"`
}

type SearchIdentity struct {
	SchemaVersion  string                            `json:"schema_version"`
	StrategyID     string                            `json:"strategy_id"`
	CoreVersion    string                            `json:"core_version"`
	ScoringVersion string                            `json:"scoring_version"`
	ExecutionMode  string                            `json:"execution_mode"`
	Datasets       []DatasetIdentity                 `json:"datasets"`
	Spawn          quant.SpawnPoint                  `json:"spawn"`
	Costs          quant.ExecutionCostConfig         `json:"costs"`
	TradePenalty   float64                           `json:"trade_penalty"`
	LongTermFilter backtestcore.LongTermFilterConfig `json:"long_term_filter"`
	ParameterAxes  []SearchParameterAxis             `json:"parameter_axes"`
}

type SearchParameterAxis struct {
	Key      string         `json:"key"`
	Kind     string         `json:"kind"`
	Minimum  float64        `json:"minimum"`
	Maximum  float64        `json:"maximum"`
	Step     float64        `json:"step"`
	State    ParameterState `json:"state"`
	Value    float64        `json:"value"`
	GridSize int            `json:"grid_size"`
}

func BuildSearchIdentity(strategyID string, plan EvaluablePlan) SearchIdentity {
	datasets := append([]DatasetIdentity(nil), plan.Datasets...)
	sort.Slice(datasets, func(i, j int) bool {
		if datasets[i].InstrumentID != datasets[j].InstrumentID {
			return datasets[i].InstrumentID < datasets[j].InstrumentID
		}
		if datasets[i].DataSource != datasets[j].DataSource {
			return datasets[i].DataSource < datasets[j].DataSource
		}
		if datasets[i].Interval != datasets[j].Interval {
			return datasets[i].Interval < datasets[j].Interval
		}
		if datasets[i].StartTimeMs != datasets[j].StartTimeMs {
			return datasets[i].StartTimeMs < datasets[j].StartTimeMs
		}
		return datasets[i].EndTimeMs < datasets[j].EndTimeMs
	})
	scoringVersion := SingleMarketScoringVersion
	if len(plan.MultiMarkets) > 0 {
		scoringVersion = MultiMarketScoringVersion
	}
	spawn := quant.SpawnPoint{}
	if plan.Spawn != nil {
		spawn = *plan.Spawn
	}
	axes := ParameterAxes(plan.GeneOptions)
	semanticAxes := make([]SearchParameterAxis, 0, len(axes))
	for _, axis := range axes {
		semanticAxes = append(semanticAxes, SearchParameterAxis{
			Key:      axis.Key,
			Kind:     axis.Kind,
			Minimum:  axis.Minimum,
			Maximum:  axis.Maximum,
			Step:     axis.Step,
			State:    axis.State,
			Value:    axis.Value,
			GridSize: axis.GridSize,
		})
	}
	return SearchIdentity{
		SchemaVersion:  CoreCandidateSchemaVersion,
		StrategyID:     strategyID,
		CoreVersion:    backtestcore.CoreVersion,
		ScoringVersion: scoringVersion,
		ExecutionMode:  plan.ExecutionMode,
		Datasets:       datasets,
		Spawn:          spawn,
		Costs:          quant.NormalizeExecutionCosts(plan.Costs),
		TradePenalty:   math.Max(0, plan.TradePenalty),
		LongTermFilter: plan.LongTermFilter,
		ParameterAxes:  semanticAxes,
	}
}

func (identity SearchIdentity) CanonicalJSON() []byte {
	raw, err := json.Marshal(identity)
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}

func (identity SearchIdentity) Hash() string {
	sum := sha256.Sum256(identity.CanonicalJSON())
	return hex.EncodeToString(sum[:])
}

func BuildDatasetIdentity(scope DatasetScope, bars []quant.Bar) DatasetIdentity {
	identity := DatasetIdentity{
		InstrumentID: scope.InstrumentID,
		DataSource:   scope.DataSource,
		Pair:         scope.Symbol,
		Interval:     scope.Interval,
		BarCount:     len(bars),
	}
	if len(bars) > 0 {
		identity.StartTimeMs = bars[0].OpenTime
		identity.EndTimeMs = bars[len(bars)-1].OpenTime
	}
	hash := sha256.New()
	var buffer [8]byte
	for _, bar := range bars {
		binary.LittleEndian.PutUint64(buffer[:], uint64(bar.OpenTime))
		_, _ = hash.Write(buffer[:])
		for _, value := range []float64{bar.Open, bar.High, bar.Low, bar.Close, bar.Volume} {
			binary.LittleEndian.PutUint64(buffer[:], math.Float64bits(value))
			_, _ = hash.Write(buffer[:])
		}
	}
	identity.ContentHash = hex.EncodeToString(hash.Sum(nil))
	return identity
}
