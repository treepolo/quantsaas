package emergency

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/saas/ga"
	"quantsaas/internal/saas/marketdata"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/gorm"
)

const BundleVersion = 1

type Bundle struct {
	Version       int             `json:"version"`
	CreatedAt     string          `json:"created_at"`
	StrategyID    string          `json:"strategy_id"`
	ParameterID   uint            `json:"parameter_id"`
	ParameterRole string          `json:"parameter_role"`
	InstrumentID  string          `json:"instrument_id"`
	Symbol        string          `json:"symbol"`
	DataSource    string          `json:"data_source"`
	Interval      string          `json:"interval"`
	ExecutionMode string          `json:"execution_mode"`
	ParamPack     json.RawMessage `json:"param_pack"`
	Bars          []Bar           `json:"bars"`
}

type Bar struct {
	Date       string  `json:"date"`
	OpenTimeMs int64   `json:"open_time_ms"`
	Open       float64 `json:"open"`
	High       float64 `json:"high"`
	Low        float64 `json:"low"`
	Close      float64 `json:"close"`
	Volume     float64 `json:"volume"`
	Manual     bool    `json:"manual,omitempty"`
}

type ManualPrice struct {
	Date       string  `json:"date"`
	OpenTimeMs int64   `json:"open_time_ms,omitempty"`
	Close      float64 `json:"close"`
	Source     string  `json:"source,omitempty"`
	CreatedAt  string  `json:"created_at,omitempty"`
}

type Result struct {
	GeneratedAt                       string             `json:"generated_at"`
	StrategyID                        string             `json:"strategy_id"`
	ParameterID                       uint               `json:"parameter_id"`
	InstrumentID                      string             `json:"instrument_id"`
	Symbol                            string             `json:"symbol"`
	DataSource                        string             `json:"data_source"`
	Interval                          string             `json:"interval"`
	ExecutionMode                     string             `json:"execution_mode"`
	LatestDate                        string             `json:"latest_date"`
	LatestTimeMs                      int64              `json:"latest_time_ms"`
	LatestTime                        string             `json:"latest_time"`
	LatestClose                       float64            `json:"latest_close"`
	MarketState                       string             `json:"market_state"`
	PositionStructure                 string             `json:"position_structure"`
	PracticalTargetWeight             float64            `json:"practical_target_weight"`
	PreviousPracticalTargetWeight     float64            `json:"previous_practical_target_weight"`
	PracticalTargetWeightChange       float64            `json:"practical_target_weight_change"`
	BaselineModelTargetWeight         float64            `json:"baseline_model_target_weight"`
	PreviousBaselineModelTargetWeight float64            `json:"previous_baseline_model_target_weight"`
	BaselineModelTargetWeightChange   float64            `json:"baseline_model_target_weight_change"`
	EmptyReferenceTargetWeight        float64            `json:"empty_reference_target_weight"`
	EmptyReferenceTargetWeightChange  float64            `json:"empty_reference_target_weight_change"`
	ModelNAV                          float64            `json:"model_nav"`
	ModelNAVChangePct                 float64            `json:"model_nav_change_pct"`
	BenchmarkNAV                      float64            `json:"benchmark_nav"`
	BenchmarkNAVChangePct             float64            `json:"benchmark_nav_change_pct"`
	Points                            int                `json:"points"`
	Diagnostics                       map[string]float64 `json:"diagnostics,omitempty"`
}

type ExportRequest struct {
	ParameterID uint
}

func ExportBundle(ctx context.Context, db *gorm.DB, req ExportRequest) (Bundle, error) {
	if db == nil {
		return Bundle{}, errors.New("database is required")
	}
	if req.ParameterID == 0 {
		return Bundle{}, errors.New("parameter id is required")
	}
	var record saasstore.GeneRecord
	if err := db.WithContext(ctx).First(&record, req.ParameterID).Error; err != nil {
		return Bundle{}, fmt.Errorf("load parameter %d: %w", req.ParameterID, err)
	}
	var rows []saasstore.KLine
	if err := db.WithContext(ctx).
		Where("instrument_id = ? AND source = ? AND interval = ?",
			record.InstrumentID, record.DataSource, record.Interval).
		Order("open_time ASC").
		Find(&rows).Error; err != nil {
		return Bundle{}, fmt.Errorf("load market data: %w", err)
	}
	if len(rows) == 0 {
		return Bundle{}, fmt.Errorf("no market data for %s %s", record.InstrumentID, record.Interval)
	}
	bars := make([]Bar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, barFromRow(row))
	}
	return Bundle{
		Version:       BundleVersion,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		StrategyID:    record.StrategyID,
		ParameterID:   record.ID,
		ParameterRole: record.Role,
		InstrumentID:  record.InstrumentID,
		Symbol:        rows[0].Symbol,
		DataSource:    record.DataSource,
		Interval:      record.Interval,
		ExecutionMode: marketdata.NormalizeExecutionMode(record.ExecutionMode),
		ParamPack:     json.RawMessage(record.ParamPack),
		Bars:          bars,
	}, nil
}

func LoadBundle(path string) (Bundle, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Bundle{}, err
	}
	var bundle Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return Bundle{}, err
	}
	return bundle, bundle.Validate()
}

func SaveBundle(path string, bundle Bundle) error {
	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func NewBar(openTimeMs int64, open float64, high float64, low float64, close float64, volume float64) Bar {
	return Bar{
		Date:       time.UnixMilli(openTimeMs).UTC().Format("2006-01-02"),
		OpenTimeMs: openTimeMs,
		Open:       open,
		High:       high,
		Low:        low,
		Close:      close,
		Volume:     volume,
	}
}

func LatestBarOpenTimeMs(bars []Bar) int64 {
	latest := int64(0)
	for _, bar := range bars {
		if bar.OpenTimeMs > latest {
			latest = bar.OpenTimeMs
		}
	}
	return latest
}

func MergeBars(base []Bar, incoming []Bar) []Bar {
	byDate := map[string]Bar{}
	for _, bar := range base {
		if bar.Close <= 0 {
			continue
		}
		date := barDate(bar)
		bar.Date = date
		byDate[date] = bar
	}
	for _, bar := range incoming {
		if bar.Close <= 0 {
			continue
		}
		date := barDate(bar)
		bar.Date = date
		byDate[date] = bar
	}
	out := make([]Bar, 0, len(byDate))
	for _, bar := range byDate {
		out = append(out, bar)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].OpenTimeMs < out[j].OpenTimeMs
	})
	return out
}

func (b Bundle) Validate() error {
	if b.Version == 0 {
		return errors.New("bundle version is required")
	}
	if b.StrategyID != sigmoiddca.StrategyID {
		return fmt.Errorf("unsupported strategy: %s", b.StrategyID)
	}
	if b.ParameterID == 0 {
		return errors.New("parameter id is required")
	}
	if strings.TrimSpace(b.InstrumentID) == "" || strings.TrimSpace(b.Symbol) == "" {
		return errors.New("instrument and symbol are required")
	}
	if len(b.ParamPack) == 0 || string(b.ParamPack) == "null" {
		return errors.New("param pack is required")
	}
	if len(b.Bars) == 0 {
		return errors.New("bars are required")
	}
	return nil
}

func LoadManualPrices(path string) ([]ManualPrice, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readManualPrices(file)
}

func AppendManualPrice(path string, price ManualPrice) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("manual price path is required")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if price.CreatedAt == "" {
		price.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if price.Source == "" {
		price.Source = "manual"
	}
	raw, err := json.Marshal(price)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

func Calculate(bundle Bundle, manualPrices []ManualPrice) (Result, error) {
	if err := bundle.Validate(); err != nil {
		return Result{}, err
	}
	bars, err := mergedBars(bundle.Bars, manualPrices)
	if err != nil {
		return Result{}, err
	}
	if len(bars) == 0 {
		return Result{}, errors.New("no usable bars")
	}
	params := sigmoiddca.ParseParamsFromParamPack(bundle.ParamPack)
	spawn := params.Spawn
	normalizeSpawn(&spawn)
	quantBars := make([]quant.Bar, 0, len(bars))
	closes := make([]float64, 0, len(bars))
	timestamps := make([]int64, 0, len(bars))
	for _, bar := range bars {
		if bar.Close <= 0 {
			continue
		}
		quantBars = append(quantBars, quant.Bar{
			OpenTime: bar.OpenTimeMs,
			Open:     fallbackOHLC(bar.Open, bar.Close),
			High:     fallbackOHLC(bar.High, bar.Close),
			Low:      fallbackOHLC(bar.Low, bar.Close),
			Close:    bar.Close,
			Volume:   bar.Volume,
		})
		closes = append(closes, bar.Close)
		timestamps = append(timestamps, bar.OpenTimeMs)
	}
	if len(quantBars) == 0 {
		return Result{}, errors.New("no positive close prices")
	}
	executionMode := marketdata.NormalizeExecutionMode(bundle.ExecutionMode)
	positionStructure := sigmoiddca.NormalizePositionStructure(params.PositionStructure)
	path := ga.RunSigmoidDCAPathBacktestWithModeCostsAndStructure(quantBars, quantBars[0].OpenTime, bundle.Interval, executionMode, params.Chromosome, &spawn, quant.ExecutionCostConfig{}, positionStructure)
	if len(path.NAV) == 0 {
		return Result{}, errors.New("model simulation produced no points")
	}
	baseline := quant.SimulateGhostDCAFrom(quantBars, quantBars[0].OpenTime, quant.GhostDCAConfig{
		InitialUSDT:       spawn.Policy.InitialUSDT,
		MonthlyInjectUSDT: spawn.Policy.MonthlyInjectUSDT,
		UseOpenExecution:  executionMode == marketdata.ExecutionModeCloseNextOpen,
	})
	latest := path.NAV[len(path.NAV)-1]
	previous := ga.BacktestPoint{}
	if len(path.NAV) > 1 {
		previous = path.NAV[len(path.NAV)-2]
	}
	benchmarkLatest, benchmarkPrevious := alignedBenchmark(path.NAV, baseline)
	output := sigmoiddca.Step(quant.StrategyInput{
		Symbol:     bundle.Symbol,
		Interval:   bundle.Interval,
		Closes:     closes,
		Timestamps: timestamps,
		Portfolio: quant.PortfolioSnapshot{
			USDTBalance: spawn.Policy.InitialUSDT,
			TotalEquity: spawn.Policy.InitialUSDT,
		},
		Spawn: spawn,
	}, params)
	marketState := ""
	if raw, ok := output.RuntimeState["last_market_state"]; ok {
		marketState, _ = raw.(string)
	}
	latestBar := bars[len(bars)-1]
	return Result{
		GeneratedAt:                       time.Now().UTC().Format(time.RFC3339),
		StrategyID:                        bundle.StrategyID,
		ParameterID:                       bundle.ParameterID,
		InstrumentID:                      bundle.InstrumentID,
		Symbol:                            bundle.Symbol,
		DataSource:                        bundle.DataSource,
		Interval:                          bundle.Interval,
		ExecutionMode:                     executionMode,
		LatestDate:                        latestBar.Date,
		LatestTimeMs:                      latest.TimeMs,
		LatestTime:                        time.UnixMilli(latest.TimeMs).UTC().Format(time.RFC3339),
		LatestClose:                       latest.Price,
		MarketState:                       marketState,
		PositionStructure:                 positionStructure,
		PracticalTargetWeight:             cleanFloat(latest.PracticalTargetWeight),
		PreviousPracticalTargetWeight:     cleanFloat(previous.PracticalTargetWeight),
		PracticalTargetWeightChange:       cleanFloat(latest.PracticalTargetWeightChange),
		BaselineModelTargetWeight:         cleanFloat(latest.ModelTargetWeight),
		PreviousBaselineModelTargetWeight: cleanFloat(previous.ModelTargetWeight),
		BaselineModelTargetWeightChange:   cleanFloat(latest.ModelTargetWeightChange),
		EmptyReferenceTargetWeight:        cleanFloat(latest.EmptyReferenceTargetWeight),
		EmptyReferenceTargetWeightChange:  cleanFloat(latest.EmptyReferenceTargetWeightChange),
		ModelNAV:                          cleanFloat(latest.TotalEquity),
		ModelNAVChangePct:                 cleanFloat(pctChange(latest.TotalEquity, previous.TotalEquity)),
		BenchmarkNAV:                      cleanFloat(benchmarkLatest),
		BenchmarkNAVChangePct:             cleanFloat(pctChange(benchmarkLatest, benchmarkPrevious)),
		Points:                            len(path.NAV),
		Diagnostics:                       output.Diagnostics,
	}, nil
}

func RenderMarkdown(result Result) string {
	var b strings.Builder
	b.WriteString("# SOXL 緊急試算結果\n\n")
	b.WriteString(fmt.Sprintf("- 產生時間：%s\n", result.GeneratedAt))
	b.WriteString(fmt.Sprintf("- 參數 ID：#%d\n", result.ParameterID))
	b.WriteString(fmt.Sprintf("- 標的：%s (%s)\n", result.InstrumentID, result.Symbol))
	b.WriteString(fmt.Sprintf("- 週期：%s\n", result.Interval))
	b.WriteString(fmt.Sprintf("- 最新日期：%s\n", result.LatestDate))
	b.WriteString(fmt.Sprintf("- 最新收盤價：%.6f\n", result.LatestClose))
	b.WriteString(fmt.Sprintf("- 市場狀態：%s\n", displayMarketState(result.MarketState)))
	b.WriteString(fmt.Sprintf("- 倉位結構：%s\n", displayPositionStructure(result.PositionStructure)))
	b.WriteString(fmt.Sprintf("- 實務模型目標權重：%.4f%%\n", result.PracticalTargetWeight*100))
	b.WriteString(fmt.Sprintf("- 前一筆實務模型目標權重：%.4f%%\n", result.PreviousPracticalTargetWeight*100))
	b.WriteString(fmt.Sprintf("- 實務模型目標權重變化：%+.4f%%\n", result.PracticalTargetWeightChange*100))
	b.WriteString(fmt.Sprintf("- 基準模型目標權重：%.4f%%\n", result.BaselineModelTargetWeight*100))
	b.WriteString(fmt.Sprintf("- 前一筆基準模型目標權重：%.4f%%\n", result.PreviousBaselineModelTargetWeight*100))
	b.WriteString(fmt.Sprintf("- 基準模型目標權重變化：%+.4f%%\n", result.BaselineModelTargetWeightChange*100))
	b.WriteString(fmt.Sprintf("- 模型淨值：%.4f\n", result.ModelNAV))
	b.WriteString(fmt.Sprintf("- 模型淨值變化：%+.4f%%\n", result.ModelNAVChangePct*100))
	return b.String()
}

func readManualPrices(r io.Reader) ([]ManualPrice, error) {
	scanner := bufio.NewScanner(r)
	out := []ManualPrice{}
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var price ManualPrice
		if err := json.Unmarshal([]byte(text), &price); err != nil {
			return nil, fmt.Errorf("manual price line %d: %w", line, err)
		}
		out = append(out, price)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func mergedBars(base []Bar, manual []ManualPrice) ([]Bar, error) {
	byDate := map[string]Bar{}
	for _, bar := range base {
		if bar.Close <= 0 {
			continue
		}
		date := barDate(bar)
		bar.Date = date
		byDate[date] = bar
	}
	reference := base
	for _, price := range manual {
		if price.Close <= 0 {
			return nil, fmt.Errorf("manual close for %s must be positive", price.Date)
		}
		date := normalizeDate(price.Date)
		if date == "" {
			return nil, errors.New("manual date is required")
		}
		openTime := price.OpenTimeMs
		if openTime == 0 {
			openTime = inferOpenTimeMs(reference, date)
		}
		bar := Bar{
			Date:       date,
			OpenTimeMs: openTime,
			Open:       price.Close,
			High:       price.Close,
			Low:        price.Close,
			Close:      price.Close,
			Manual:     true,
		}
		byDate[date] = bar
		reference = append(reference, bar)
	}
	out := make([]Bar, 0, len(byDate))
	for _, bar := range byDate {
		out = append(out, bar)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].OpenTimeMs < out[j].OpenTimeMs
	})
	return out, nil
}

func barFromRow(row saasstore.KLine) Bar {
	return Bar{
		Date:       time.UnixMilli(row.OpenTime).UTC().Format("2006-01-02"),
		OpenTimeMs: row.OpenTime,
		Open:       row.Open,
		High:       row.High,
		Low:        row.Low,
		Close:      row.Close,
		Volume:     row.Volume,
	}
}

func displayPositionStructure(value string) string {
	switch sigmoiddca.NormalizePositionStructure(value) {
	case sigmoiddca.PositionStructureFloatingOnly:
		return "純浮動模型"
	default:
		return "雙層模型"
	}
}

func barDate(bar Bar) string {
	if date := normalizeDate(bar.Date); date != "" {
		return date
	}
	return time.UnixMilli(bar.OpenTimeMs).UTC().Format("2006-01-02")
}

func normalizeDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.Format("2006-01-02")
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC().Format("2006-01-02")
	}
	return value
}

func inferOpenTimeMs(reference []Bar, date string) int64 {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return time.Now().UTC().UnixMilli()
	}
	hour, min, sec := 0, 0, 0
	if len(reference) > 0 {
		sort.Slice(reference, func(i, j int) bool {
			return reference[i].OpenTimeMs < reference[j].OpenTimeMs
		})
		last := time.UnixMilli(reference[len(reference)-1].OpenTimeMs).UTC()
		hour, min, sec = last.Clock()
	}
	return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), hour, min, sec, 0, time.UTC).UnixMilli()
}

func alignedBenchmark(points []ga.BacktestPoint, baseline quant.GhostDCAResult) (float64, float64) {
	byTime := make(map[int64]float64, len(baseline.Times))
	for i, ts := range baseline.Times {
		if i < len(baseline.NAV) {
			byTime[ts] = baseline.NAV[i]
		}
	}
	latest := 0.0
	previous := 0.0
	for _, point := range points {
		value, ok := byTime[point.TimeMs]
		if !ok {
			continue
		}
		previous = latest
		latest = value
	}
	return latest, previous
}

func normalizeSpawn(spawn *quant.SpawnPoint) {
	if spawn.Policy.InitialUSDT <= 0 {
		spawn.Policy.InitialUSDT = 1000
	}
	if spawn.Policy.MonthlyInjectUSDT < 0 {
		spawn.Policy.MonthlyInjectUSDT = 0
	}
	if spawn.Risk.MaxDrawdownPct <= 0 {
		spawn.Risk.MaxDrawdownPct = 0.88
	}
	if spawn.Risk.LotStep <= 0 {
		spawn.Risk.LotStep = 0.000001
	}
	if spawn.Risk.LotMin <= 0 {
		spawn.Risk.LotMin = 0.00001
	}
}

func fallbackOHLC(value float64, close float64) float64 {
	if value > 0 {
		return value
	}
	return close
}

func pctChange(current float64, previous float64) float64 {
	if previous <= 0 {
		return 0
	}
	return current/previous - 1
}

func cleanFloat(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func displayMarketState(value string) string {
	switch value {
	case "BULL_TREND":
		return "趨勢牛"
	case "BEAR_TREND":
		return "趨勢熊"
	case "QUIET":
		return "安靜態"
	case "SHOCK":
		return "衝擊態"
	case "":
		return "未判定"
	default:
		return value
	}
}
