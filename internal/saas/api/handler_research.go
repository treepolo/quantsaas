package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/saas/ga"
	"quantsaas/internal/saas/marketdata"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ResearchStatusHandler struct {
	db          *gorm.DB
	instruments *marketdata.InstrumentStore
}

func NewResearchStatusHandler(db *gorm.DB) *ResearchStatusHandler {
	return &ResearchStatusHandler{db: db, instruments: marketdata.NewInstrumentStore(db)}
}

func (h *ResearchStatusHandler) Status(c *gin.Context) {
	simulation := parsePositionSimulationQuery(c)
	instruments, err := h.instruments.Instruments(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if requested := c.Query("instrument_id"); requested != "" {
		instrument, err := h.instruments.ResolveInstrument(c.Request.Context(), requested, "", "")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		instruments = []marketdata.ResearchInstrument{instrument}
	}
	items := make([]gin.H, 0, len(instruments))
	for _, instrument := range instruments {
		items = append(items, h.instrumentStatus(c.Request.Context(), instrument, simulation))
	}
	c.JSON(http.StatusOK, gin.H{
		"items":      items,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

type positionSimulationQuery struct {
	StartTimeMs    int64
	InitialCapital float64
	MonthlyDCA     float64
	FeeRate        float64
	SpreadRate     float64
}

func parsePositionSimulationQuery(c *gin.Context) positionSimulationQuery {
	return positionSimulationQuery{
		StartTimeMs:    parseInt64Query(c, "simulation_start_ms"),
		InitialCapital: parseFloatQuery(c, "simulation_initial_capital"),
		MonthlyDCA:     parseFloatQuery(c, "simulation_monthly_dca"),
		FeeRate:        parseFloatQuery(c, "simulation_fee_rate"),
		SpreadRate:     parseFloatQuery(c, "simulation_spread_rate"),
	}
}

func parseInt64Query(c *gin.Context, key string) int64 {
	value := c.Query(key)
	if value == "" {
		return 0
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func parseFloatQuery(c *gin.Context, key string) float64 {
	value := c.Query(key)
	if value == "" {
		return 0
	}
	parsed, _ := strconv.ParseFloat(value, 64)
	return parsed
}

func (h *ResearchStatusHandler) instrumentStatus(ctx context.Context, instrument marketdata.ResearchInstrument, simulation positionSimulationQuery) gin.H {
	base := gin.H{
		"instrument":     instrument,
		"instrument_id":  instrument.ID,
		"symbol":         instrument.Symbol,
		"data_source":    instrument.DataSource,
		"interval":       "1d",
		"execution_mode": marketdata.ExecutionModeCloseNextOpen,
	}
	states := make([]gin.H, 0, 2)
	for _, interval := range []string{"1d", "1w"} {
		if !instrumentSupportsInterval(instrument, interval) {
			continue
		}
		states = append(states, h.instrumentIntervalStatus(ctx, instrument, interval, simulation))
	}
	base["interval_states"] = states
	if len(states) == 0 {
		base["status"] = "missing_data"
		return base
	}
	primary := states[0]
	for _, state := range states {
		if state["interval"] == "1d" {
			primary = state
			break
		}
	}
	for key, value := range primary {
		base[key] = value
	}
	if primary["status"] == "ready" {
		if bars, ok := primary["_bars"].([]quant.Bar); ok {
			params, _ := primary["_params"].(sigmoiddca.Params)
			externalSignals, _ := primary["_external_signals"].(map[int64]float64)
			if summary, ok := simulateResearchPosition(bars, params, instrument.Symbol, simulation, externalSignals); ok {
				base["position_simulation"] = summary
			}
		}
	}
	delete(base, "_bars")
	delete(base, "_params")
	delete(base, "_external_signals")
	for _, state := range states {
		delete(state, "_bars")
		delete(state, "_params")
		delete(state, "_external_signals")
	}
	return base
}

func (h *ResearchStatusHandler) instrumentIntervalStatus(ctx context.Context, instrument marketdata.ResearchInstrument, interval string, simulation positionSimulationQuery) gin.H {
	base := gin.H{
		"interval":       interval,
		"execution_mode": marketdata.ExecutionModeCloseNextOpen,
	}
	champion, err := h.loadMarketStateChampion(ctx, instrument, interval)
	if err != nil {
		base["status"] = "missing_champion"
		return base
	}
	base["execution_mode"] = champion.ExecutionMode
	params := sigmoiddca.ParseParamsFromParamPack([]byte(champion.ParamPack))
	indicatorSeriesIDs := marketdata.NormalizeSeriesIDs(params.IndicatorSeriesIDs)

	dataset, err := marketdata.NewService(h.db, nil).BuildDataset(ctx, marketdata.DatasetBuildRequest{
		TradableSeriesIDs:  []string{instrument.ID},
		IndicatorSeriesIDs: indicatorSeriesIDs,
		Interval:           interval,
		StartTimeMs:        1,
		EndTimeMs:          time.Now().UTC().UnixMilli(),
	})
	if err != nil {
		base["status"] = "missing_data"
		base["error"] = err.Error()
		return base
	}
	dataset = datasetRowsAvailableAt(dataset, time.Now().UTC())
	bars, err := marketdata.PrimaryBarsFromDataset(dataset)
	if err != nil || len(bars) == 0 {
		base["status"] = "missing_data"
		if err != nil {
			base["error"] = err.Error()
		} else {
			base["error"] = "no completed bars"
		}
		return base
	}
	externalSignals := marketdata.ExternalSignalByTime(dataset)

	closes := make([]float64, 0, len(bars))
	timestamps := make([]int64, 0, len(bars))
	for _, bar := range bars {
		closes = append(closes, bar.Close)
		timestamps = append(timestamps, bar.OpenTime)
	}
	latest := bars[len(bars)-1]
	portfolio := quant.PortfolioSnapshot{
		USDTBalance: params.Spawn.Policy.InitialUSDT,
		TotalEquity: params.Spawn.Policy.InitialUSDT,
	}
	output := sigmoiddca.Step(quant.StrategyInput{
		Symbol:     instrument.Symbol,
		Interval:   interval,
		Closes:     closes,
		Timestamps: timestamps,
		Portfolio:  portfolio,
		Spawn:      params.Spawn,
		AISignal:   researchAISignal(externalSignals, latest.OpenTime),
	}, params)
	marketState := ""
	if raw, ok := output.RuntimeState["last_market_state"]; ok {
		marketState, _ = raw.(string)
	}
	paramValues := map[string]any{}
	rawParams, _ := json.Marshal(params.Chromosome)
	_ = json.Unmarshal(rawParams, &paramValues)

	base["status"] = "ready"
	base["champion"] = geneResponse(champion)
	base["latest_bar"] = gin.H{
		"open_time_ms": latest.OpenTime,
		"time":         time.UnixMilli(latest.OpenTime).UTC().Format(time.RFC3339),
		"close":        latest.Close,
		"completed":    true,
	}
	base["market_state"] = marketState
	base["diagnostics"] = output.Diagnostics
	base["parameter_values"] = paramValues
	base["indicator_series_ids"] = indicatorSeriesIDs
	base["_bars"] = bars
	base["_params"] = params
	base["_external_signals"] = externalSignals
	if model, ok := simulateResearchModel(bars, params, interval, champion.ExecutionMode, simulation, externalSignals); ok {
		base["model_simulation"] = model
		if latestTarget, ok := model["latest_practical_target_weight"]; ok {
			base["target_weight"] = latestTarget
		}
		if latestDelta, ok := model["latest_practical_target_weight_change"]; ok {
			base["delta_weight"] = latestDelta
		}
		base["empty_reference_target_weight"] = model["latest_empty_reference_target_weight"]
		base["empty_reference_target_weight_change"] = model["latest_empty_reference_target_weight_change"]
	} else {
		base["target_weight"] = output.Diagnostics["target_weight"]
		base["current_weight"] = output.Diagnostics["current_weight"]
		base["delta_weight"] = output.Diagnostics["delta_weight"]
	}
	return base
}

func (h *ResearchStatusHandler) loadMarketStateChampion(ctx context.Context, instrument marketdata.ResearchInstrument, interval string) (saasstore.GeneRecord, error) {
	var fallbackErr error
	for _, executionMode := range marketStateExecutionModePriority() {
		var champion saasstore.GeneRecord
		err := h.db.WithContext(ctx).
			Where("strategy_id = ? AND instrument_id = ? AND data_source = ? AND interval = ? AND execution_mode = ? AND role = ?",
				sigmoiddca.StrategyID, instrument.ID, instrument.DataSource, interval, executionMode, saasstore.GeneRoleChampion).
			Order("activated_at DESC NULLS LAST, created_at DESC").
			First(&champion).Error
		if err == nil {
			return champion, nil
		}
		if fallbackErr == nil || !errors.Is(err, gorm.ErrRecordNotFound) {
			fallbackErr = err
		}
	}
	if fallbackErr == nil {
		fallbackErr = gorm.ErrRecordNotFound
	}
	return saasstore.GeneRecord{}, fallbackErr
}

func marketStateExecutionModePriority() []string {
	return []string{
		marketdata.ExecutionModeCloseNextOpen,
		marketdata.ExecutionModeCloseSameBar,
	}
}

func instrumentSupportsInterval(instrument marketdata.ResearchInstrument, interval string) bool {
	for _, supported := range instrument.SupportedIntervals {
		if supported == interval {
			return true
		}
	}
	return false
}

func simulateResearchModel(bars []quant.Bar, params sigmoiddca.Params, interval string, executionMode string, settings positionSimulationQuery, externalSignals map[int64]float64) (gin.H, bool) {
	if len(bars) == 0 {
		return nil, false
	}
	spawn := params.Spawn
	if settings.InitialCapital > 0 {
		spawn.Policy.InitialUSDT = settings.InitialCapital
	}
	if settings.MonthlyDCA >= 0 {
		spawn.Policy.MonthlyInjectUSDT = settings.MonthlyDCA
	}
	executionMode = marketdata.NormalizeExecutionMode(executionMode)
	costs := researchCosts(settings)
	path := ga.RunSigmoidDCAPathBacktestWithModeCostsStructureAndSignals(bars, bars[0].OpenTime, interval, executionMode, params.Chromosome, &spawn, costs, params.PositionStructure, externalSignals)
	baseline := quant.SimulateGhostDCAFrom(bars, bars[0].OpenTime, quant.GhostDCAConfig{
		InitialUSDT:       spawn.Policy.InitialUSDT,
		MonthlyInjectUSDT: spawn.Policy.MonthlyInjectUSDT,
		UseOpenExecution:  executionMode == marketdata.ExecutionModeCloseNextOpen,
		Costs:             costs,
	})
	points := mergeResearchModelPoints(path.NAV, baseline)
	if len(points) == 0 {
		return nil, false
	}
	latest := points[len(points)-1]
	previous := gin.H{}
	if len(points) > 1 {
		previous = points[len(points)-2]
	}
	return gin.H{
		"start_time_ms":                               bars[0].OpenTime,
		"latest_time_ms":                              latest["time_ms"],
		"latest_time":                                 latest["time"],
		"initial_capital":                             spawn.Policy.InitialUSDT,
		"monthly_dca":                                 spawn.Policy.MonthlyInjectUSDT,
		"fee_rate":                                    costs.FeeRate,
		"spread_rate":                                 costs.SpreadRate,
		"rebalance_threshold":                         params.Chromosome.RebalanceThreshold,
		"force_full_threshold":                        params.Chromosome.ForceFullThreshold,
		"force_empty_threshold":                       params.Chromosome.ForceEmptyThreshold,
		"position_structure":                          params.PositionStructure,
		"indicator_series_ids":                        params.IndicatorSeriesIDs,
		"trade_count":                                 path.Metrics.TradeCount,
		"w_mean":                                      params.Chromosome.WMean,
		"w_momentum":                                  params.Chromosome.WMomentum,
		"w_breakout":                                  params.Chromosome.WBreakout,
		"latest_nav":                                  latest["model_nav"],
		"previous_nav":                                previous["model_nav"],
		"nav_change_pct":                              latest["model_nav_change_pct"],
		"latest_benchmark":                            latest["benchmark"],
		"benchmark_change_pct":                        latest["benchmark_change_pct"],
		"latest_practical_target_weight":              latest["practical_target_weight"],
		"previous_practical_target_weight":            previous["practical_target_weight"],
		"latest_practical_target_weight_change":       latest["practical_target_weight_change"],
		"latest_model_target_weight":                  latest["model_target_weight"],
		"previous_model_target_weight":                previous["model_target_weight"],
		"latest_model_target_weight_change":           latest["model_target_weight_change"],
		"latest_empty_reference_target_weight":        latest["empty_reference_target_weight"],
		"previous_empty_reference_target_weight":      previous["empty_reference_target_weight"],
		"latest_empty_reference_target_weight_change": latest["empty_reference_target_weight_change"],
		"points":                                      len(points),
		"chart_points":                                points,
	}, true
}

func researchCosts(settings positionSimulationQuery) quant.ExecutionCostConfig {
	return quant.NormalizeExecutionCosts(quant.ExecutionCostConfig{
		FeeRate:    settings.FeeRate,
		SpreadRate: settings.SpreadRate,
	})
}

func simulateResearchPosition(bars []quant.Bar, params sigmoiddca.Params, symbol string, settings positionSimulationQuery, externalSignals map[int64]float64) (gin.H, bool) {
	if settings.StartTimeMs <= 0 || settings.InitialCapital <= 0 || len(bars) == 0 {
		return nil, false
	}

	closes := make([]float64, 0, len(bars))
	timestamps := make([]int64, 0, len(bars))
	state := map[string]any{}
	started := false
	cash := 0.0
	assetQty := 0.0
	invested := 0.0
	lastYear := 0
	var lastMonth time.Month
	points := 0
	latestNAV := 0.0
	previousNAV := 0.0
	latestTargetWeight := 0.0
	previousTargetWeight := 0.0
	latestActualWeight := 0.0
	previousActualWeight := 0.0
	latestContribution := 0.0
	latestMs := int64(0)
	startedAtMs := int64(0)

	for _, bar := range bars {
		if bar.Close <= 0 {
			continue
		}
		closes = append(closes, bar.Close)
		timestamps = append(timestamps, bar.OpenTime)
		if !started && bar.OpenTime < settings.StartTimeMs {
			continue
		}
		year, month := time.UnixMilli(bar.OpenTime).UTC().Year(), time.UnixMilli(bar.OpenTime).UTC().Month()
		contribution := 0.0
		if !started {
			started = true
			cash = settings.InitialCapital
			invested = settings.InitialCapital
			lastYear = year
			lastMonth = month
			startedAtMs = bar.OpenTime
		} else if (year != lastYear || month != lastMonth) && settings.MonthlyDCA > 0 {
			cash += settings.MonthlyDCA
			invested += settings.MonthlyDCA
			contribution = settings.MonthlyDCA
			lastYear = year
			lastMonth = month
		}

		equityBefore := cash + assetQty*bar.Close
		output := sigmoiddca.Step(quant.StrategyInput{
			Symbol:     symbol,
			Interval:   "1d",
			Closes:     closes,
			Timestamps: timestamps,
			Portfolio: quant.PortfolioSnapshot{
				USDTBalance: cash,
				FloatBTC:    assetQty,
				TotalEquity: equityBefore,
			},
			RuntimeState: state,
			Spawn:        params.Spawn,
			AISignal:     researchAISignal(externalSignals, bar.OpenTime),
		}, params)
		state = output.RuntimeState
		targetWeight := currentWeight(assetQty, bar.Close, equityBefore)
		if raw, ok := output.Diagnostics["target_weight"]; ok {
			targetWeight = clamp01(raw)
		}
		targetValue := equityBefore * targetWeight
		currentValue := assetQty * bar.Close
		deltaValue := targetValue - currentValue
		if deltaValue > 0 {
			buyValue := math.Min(deltaValue, cash)
			assetQty += buyValue / bar.Close
			cash -= buyValue
		} else if deltaValue < 0 {
			sellQty := math.Min(-deltaValue/bar.Close, assetQty)
			assetQty -= sellQty
			cash += sellQty * bar.Close
		}

		previousNAV = latestNAV
		previousTargetWeight = latestTargetWeight
		previousActualWeight = latestActualWeight
		latestNAV = cash + assetQty*bar.Close
		latestTargetWeight = targetWeight
		latestActualWeight = currentWeight(assetQty, bar.Close, latestNAV)
		latestContribution = contribution
		latestMs = bar.OpenTime
		points++
	}

	if !started || points == 0 {
		return nil, false
	}
	navChangePct := 0.0
	if previousNAV > 0 {
		navChangePct = (latestNAV-latestContribution)/previousNAV - 1
	}
	targetDelta := 0.0
	if points > 1 {
		targetDelta = latestTargetWeight - previousTargetWeight
	}
	return gin.H{
		"start_time_ms":          startedAtMs,
		"latest_time_ms":         latestMs,
		"latest_time":            time.UnixMilli(latestMs).UTC().Format(time.RFC3339),
		"initial_capital":        settings.InitialCapital,
		"monthly_dca":            settings.MonthlyDCA,
		"invested_capital":       invested,
		"latest_nav":             latestNAV,
		"previous_nav":           previousNAV,
		"nav_change_pct":         navChangePct,
		"latest_contribution":    latestContribution,
		"latest_target_weight":   latestTargetWeight,
		"previous_target_weight": previousTargetWeight,
		"target_weight_delta":    targetDelta,
		"latest_actual_weight":   latestActualWeight,
		"previous_actual_weight": previousActualWeight,
		"cash_balance":           cash,
		"asset_quantity":         assetQty,
		"points":                 points,
	}, true
}

func mergeResearchModelPoints(strategy []ga.BacktestPoint, baseline quant.GhostDCAResult) []gin.H {
	byTime := make(map[int64]float64, len(baseline.Times))
	for i, ts := range baseline.Times {
		if i < len(baseline.NAV) {
			byTime[ts] = baseline.NAV[i]
		}
	}
	points := make([]gin.H, 0, len(strategy))
	previousModelNAV := 0.0
	previousBenchmark := 0.0
	for _, item := range strategy {
		benchmark, ok := byTime[item.TimeMs]
		if !ok {
			continue
		}
		points = append(points, gin.H{
			"time_ms":                              item.TimeMs,
			"time":                                 time.UnixMilli(item.TimeMs).UTC().Format(time.RFC3339),
			"price":                                item.Price,
			"model_nav":                            item.TotalEquity,
			"benchmark":                            benchmark,
			"model_nav_change_pct":                 pctChange(item.TotalEquity, previousModelNAV),
			"benchmark_change_pct":                 pctChange(benchmark, previousBenchmark),
			"practical_target_weight":              item.PracticalTargetWeight,
			"practical_target_weight_change":       item.PracticalTargetWeightChange,
			"model_target_weight":                  item.ModelTargetWeight,
			"model_target_weight_change":           item.ModelTargetWeightChange,
			"empty_reference_target_weight":        item.EmptyReferenceTargetWeight,
			"empty_reference_target_weight_change": item.EmptyReferenceTargetWeightChange,
		})
		previousModelNAV = item.TotalEquity
		previousBenchmark = benchmark
	}
	return points
}

func datasetRowsAvailableAt(dataset marketdata.ResearchDataset, now time.Time) marketdata.ResearchDataset {
	maxDecisionTimeMs := now.UTC().UnixMilli()
	rows := make([]marketdata.DatasetRow, 0, len(dataset.Rows))
	for _, row := range dataset.Rows {
		if row.DecisionTimeMs <= maxDecisionTimeMs {
			rows = append(rows, row)
		}
	}
	dataset.Rows = rows
	return dataset
}

func researchAISignal(externalSignals map[int64]float64, timeMs int64) quant.AISignalVector {
	if len(externalSignals) == 0 {
		return quant.AISignalVector{}
	}
	return quant.AISignalVector{SMarket: externalSignals[timeMs]}
}

func pctChange(current float64, previous float64) float64 {
	if previous <= 0 {
		return 0
	}
	return current/previous - 1
}

func currentWeight(assetQty float64, price float64, equity float64) float64 {
	if equity <= 0 || price <= 0 {
		return 0
	}
	return clamp01(assetQty * price / equity)
}

func clamp01(value float64) float64 {
	return math.Max(0, math.Min(1, value))
}
