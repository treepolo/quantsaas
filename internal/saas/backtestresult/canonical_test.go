package backtestresult

import (
	"testing"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/quant"
)

func TestBuildIdentityCanonicalizesParameterJSON(t *testing.T) {
	first := baseSpecInput()
	first.Parameters = map[string]any{"beta": 1.25, "gamma": 2.0}
	second := baseSpecInput()
	second.Parameters = map[string]any{"gamma": 2, "beta": 1.2500}

	left, err := BuildIdentity(first, testBars())
	if err != nil {
		t.Fatal(err)
	}
	right, err := BuildIdentity(second, testBars())
	if err != nil {
		t.Fatal(err)
	}
	if left.BacktestKey != right.BacktestKey {
		t.Fatalf("canonical keys differ: %s != %s", left.BacktestKey, right.BacktestKey)
	}
	if left.Snapshot.ParameterHash != right.Snapshot.ParameterHash {
		t.Fatalf("parameter hashes differ: %s != %s", left.Snapshot.ParameterHash, right.Snapshot.ParameterHash)
	}
}

func TestBacktestKeySeparatesAllOutputAffectingInputs(t *testing.T) {
	baseInput := baseSpecInput()
	baseBars := testBars()
	base, err := BuildIdentity(baseInput, baseBars)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*SpecInput, *[]quant.Bar)
	}{
		{name: "parameters", mutate: func(input *SpecInput, _ *[]quant.Bar) {
			input.Parameters = map[string]any{"beta": 1.5}
		}},
		{name: "dataset", mutate: func(_ *SpecInput, bars *[]quant.Bar) {
			(*bars)[1].Close += 0.01
		}},
		{name: "execution mode", mutate: func(input *SpecInput, _ *[]quant.Bar) {
			input.CoreSpec.ExecutionMode = backtestcore.ExecutionModeCloseNextOpen
		}},
		{name: "filter", mutate: func(input *SpecInput, _ *[]quant.Bar) {
			input.LongTermFilterVersion = "filter-v1"
			input.LongTermFilterSettings = map[string]any{"enabled": true, "months": 12}
		}},
		{name: "cost", mutate: func(input *SpecInput, _ *[]quant.Bar) {
			input.CoreSpec.Costs.FeeRate += 0.0001
		}},
		{name: "core version", mutate: func(input *SpecInput, _ *[]quant.Bar) {
			input.CoreSpec.CoreVersion = "p02-v2"
		}},
		{name: "strategy version", mutate: func(input *SpecInput, _ *[]quant.Bar) {
			input.StrategyVersion = "0.2.0"
		}},
		{name: "dynamic policy", mutate: func(input *SpecInput, _ *[]quant.Bar) {
			input.DynamicPolicyHash = "sha256:policy"
			input.DynamicControlMode = "daily"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseSpecInput()
			bars := append([]quant.Bar(nil), baseBars...)
			tt.mutate(&input, &bars)
			identity, err := BuildIdentity(input, bars)
			if err != nil {
				t.Fatal(err)
			}
			if identity.BacktestKey == base.BacktestKey {
				t.Fatalf("%s did not change backtest key", tt.name)
			}
		})
	}
}

func TestDatasetHashIncludesOHLCVAndOrdering(t *testing.T) {
	base, err := HashDataset(DatasetSchemaVersion, testBars())
	if err != nil {
		t.Fatal(err)
	}
	changed := testBars()
	changed[0].Volume++
	other, err := HashDataset(DatasetSchemaVersion, changed)
	if err != nil {
		t.Fatal(err)
	}
	if base == other {
		t.Fatal("volume change did not change dataset hash")
	}

	reversed := testBars()
	reversed[0], reversed[1] = reversed[1], reversed[0]
	if _, err := HashDataset(DatasetSchemaVersion, reversed); err == nil {
		t.Fatal("expected unordered bars to fail")
	}
}

func baseSpecInput() SpecInput {
	return SpecInput{
		StrategyID:             "sigmoid-dca-btc",
		StrategyVersion:        "0.1.0",
		ParameterSchemaVersion: ParameterSchemaV1,
		Parameters:             map[string]any{"beta": 1.25},
		DatasetVersion:         DatasetSchemaVersion,
		CoreSpec: backtestcore.Spec{
			Runner:               backtestcore.RunnerSigmoidDCA,
			InstrumentID:         "BTCUSDT",
			Symbol:               "BTCUSDT",
			DataSource:           "binance",
			Interval:             "1d",
			ExecutionMode:        backtestcore.ExecutionModeCloseSameBar,
			PositionStructure:    "floating_only",
			StartTimeMs:          1_700_000_000_000,
			EndTimeMs:            1_700_172_800_000,
			EvaluationStartMs:    1_700_000_000_000,
			EvaluationEndMs:      1_700_172_800_000,
			PrefixMode:           backtestcore.PrefixModeExecute,
			InitialCapital:       1000,
			MonthlyContribution:  100,
			InitialAssetQuantity: 0,
			Costs: quant.ExecutionCostConfig{
				FeeRate:    0.001,
				SpreadRate: 0.0005,
			},
			CoreVersion: backtestcore.CoreVersion,
		},
	}
}

func testBars() []quant.Bar {
	return []quant.Bar{
		{OpenTime: 1_700_000_000_000, Open: 100, High: 102, Low: 99, Close: 101, Volume: 10},
		{OpenTime: 1_700_086_400_000, Open: 101, High: 104, Low: 100, Close: 103, Volume: 12},
		{OpenTime: 1_700_172_800_000, Open: 103, High: 105, Low: 98, Close: 99, Volume: 15},
	}
}
