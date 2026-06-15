package sigmoiddca

import "encoding/json"

type RuntimeState struct {
	LastProcessedBarTime int64  `json:"last_processed_bar_time"`
	LastMacroYearMonth   string `json:"last_macro_year_month"`
	LastMarketState      string `json:"last_market_state"`
}

func DecodeRuntimeState(raw map[string]any) RuntimeState {
	if len(raw) == 0 {
		return RuntimeState{}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return RuntimeState{}
	}
	var state RuntimeState
	if err := json.Unmarshal(encoded, &state); err != nil {
		return RuntimeState{}
	}
	return state
}

func EncodeRuntimeState(state RuntimeState) map[string]any {
	encoded, err := json.Marshal(state)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return map[string]any{}
	}
	return out
}
