package parameterresearch

type ComparisonContext struct {
	InstrumentID        string `json:"instrument_id"`
	DatasetHash         string `json:"dataset_hash"`
	Interval            string `json:"interval"`
	StartTimeMs         int64  `json:"start_time_ms"`
	EndTimeMs           int64  `json:"end_time_ms"`
	StrategyVersion     string `json:"strategy_version"`
	BacktestCoreVersion string `json:"backtest_core_version"`
	ExecutionMode       string `json:"execution_mode"`
	InitialCapitalHash  string `json:"initial_capital_hash"`
	CashFlowHash        string `json:"cash_flow_hash"`
	CostHash            string `json:"cost_hash"`
	BenchmarkVersion    string `json:"benchmark_version"`
	MetricVersion       string `json:"metric_version"`
	ParameterSchemaHash string `json:"parameter_schema_hash"`
	ResultSchemaVersion string `json:"result_schema_version"`
	PointSetHash        string `json:"point_set_hash"`
}

type ComparisonEligibility struct {
	Level   string   `json:"level"`
	Reasons []string `json:"reasons"`
}

func CompareEligibility(members []ComparisonContext) ComparisonEligibility {
	if len(members) < 2 {
		return ComparisonEligibility{Level: "incompatible", Reasons: []string{"至少需要兩份研究設定"}}
	}
	base := members[0]
	contextMatched, schemaMatched, pointSetMatched := true, true, true
	reasons := []string{}
	for _, member := range members[1:] {
		if member.ResultSchemaVersion != base.ResultSchemaVersion || member.BacktestCoreVersion != base.BacktestCoreVersion || member.MetricVersion != base.MetricVersion {
			return ComparisonEligibility{Level: "incompatible", Reasons: []string{"回測核心、指標或結果 schema 版本不相容"}}
		}
		if member.InstrumentID != base.InstrumentID || member.DatasetHash != base.DatasetHash || member.Interval != base.Interval || member.StartTimeMs != base.StartTimeMs || member.EndTimeMs != base.EndTimeMs || member.StrategyVersion != base.StrategyVersion || member.ExecutionMode != base.ExecutionMode || member.InitialCapitalHash != base.InitialCapitalHash || member.CashFlowHash != base.CashFlowHash || member.CostHash != base.CostHash || member.BenchmarkVersion != base.BenchmarkVersion {
			contextMatched = false
		}
		if member.ParameterSchemaHash != base.ParameterSchemaHash {
			schemaMatched = false
		}
		if member.PointSetHash == "" || member.PointSetHash != base.PointSetHash {
			pointSetMatched = false
		}
	}
	if !contextMatched {
		return ComparisonEligibility{Level: "descriptive_only", Reasons: []string{"商品、資料期間、基準或實際回測背景不同"}}
	}
	if !schemaMatched {
		return ComparisonEligibility{Level: "context_matched_unpaired", Reasons: []string{"共同背景相同，但參數 schema 不相容，不能逐點配對"}}
	}
	if !pointSetMatched {
		reasons = append(reasons, "共同背景與參數 schema 相同，但評估點集合尚未配對")
		return ComparisonEligibility{Level: "context_matched_unpaired", Reasons: reasons}
	}
	return ComparisonEligibility{Level: "paired_direct", Reasons: []string{"共同背景、參數 schema 與標準化向量 manifest 完全一致"}}
}
