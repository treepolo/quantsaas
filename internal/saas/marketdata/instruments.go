package marketdata

import (
	"errors"
	"strings"
)

const (
	DataSourceBinance = "binance"
	DataSourceYahoo   = "yahoo"

	InstrumentBTCUSDT = "BTCUSDT"

	ExecutionModeCloseSameBar  = "close_same_bar"
	ExecutionModeCloseNextOpen = "close_next_open"
	ExecutionModePreclose10m   = "preclose_10m"
)

var (
	ErrUnsupportedInstrument = errors.New("unsupported research instrument")
	ErrUnsupportedSource     = errors.New("unsupported market data source")
)

type ResearchInstrument struct {
	ID                 string   `json:"id"`
	Symbol             string   `json:"symbol"`
	DisplayName        string   `json:"display_name"`
	DataSource         string   `json:"data_source"`
	SupportedIntervals []string `json:"supported_intervals"`
}

var researchInstruments = []ResearchInstrument{
	{ID: "TWII", Symbol: "^TWII", DisplayName: "台灣加權指數", DataSource: DataSourceYahoo, SupportedIntervals: []string{"1d"}},
	{ID: "GSPC", Symbol: "^GSPC", DisplayName: "標普 500 指數", DataSource: DataSourceYahoo, SupportedIntervals: []string{"1d"}},
	{ID: "NDX", Symbol: "^NDX", DisplayName: "納斯達克 100 指數", DataSource: DataSourceYahoo, SupportedIntervals: []string{"1d"}},
	{ID: "SOX", Symbol: "^SOX", DisplayName: "費城半導體指數", DataSource: DataSourceYahoo, SupportedIntervals: []string{"1d"}},
	{ID: "SOXL", Symbol: "SOXL", DisplayName: "SOXL 三倍做多費半 ETF", DataSource: DataSourceYahoo, SupportedIntervals: []string{"1d"}},
	{ID: InstrumentBTCUSDT, Symbol: "BTCUSDT", DisplayName: "比特幣現貨", DataSource: DataSourceBinance, SupportedIntervals: []string{"1d", "1h", "15m", "5m", "1m", "1s"}},
}

func Instruments() []ResearchInstrument {
	out := make([]ResearchInstrument, len(researchInstruments))
	copy(out, researchInstruments)
	return out
}

func ResolveInstrument(instrumentID string, symbol string, source string) (ResearchInstrument, error) {
	instrumentID = normalizeInstrumentID(instrumentID)
	symbol = normalizeSymbol(symbol)
	source = normalizeSource(source)
	if instrumentID == "" && symbol == "" {
		instrumentID = InstrumentBTCUSDT
	}
	for _, instrument := range researchInstruments {
		if instrumentID != "" && normalizeInstrumentID(instrument.ID) == instrumentID {
			return instrument, nil
		}
		if symbol != "" && normalizeSymbol(instrument.Symbol) == symbol {
			if source == "" || normalizeSource(instrument.DataSource) == source {
				return instrument, nil
			}
		}
	}
	return ResearchInstrument{}, ErrUnsupportedInstrument
}

func SupportedExecutionModes() []string {
	return []string{ExecutionModeCloseSameBar, ExecutionModeCloseNextOpen, ExecutionModePreclose10m}
}

func NormalizeExecutionMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return ExecutionModeCloseSameBar
	}
	return mode
}

func IsSupportedExecutionMode(mode string) bool {
	mode = NormalizeExecutionMode(mode)
	for _, supported := range SupportedExecutionModes() {
		if mode == supported {
			return true
		}
	}
	return false
}

func normalizeInstrumentID(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func normalizeSource(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
