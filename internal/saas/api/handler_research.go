package api

import (
	"context"
	"encoding/json"
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
}

func parsePositionSimulationQuery(c *gin.Context) positionSimulationQuery {
	return positionSimulationQuery{
		StartTimeMs:    parseInt64Query(c, "simulation_start_ms"),
		InitialCapital: parseFloatQuery(c, "simulation_initial_capital"),
		MonthlyDCA:     parseFloatQuery(c, "simulation_monthly_dca"),
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
		"execution_mode": marketdata.ExecutionModeCloseSameBar,
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
		if rows, ok := primary["_rows"].([]saasstore.KLine); ok {
			params, _ := primary["_params"].(sigmoiddca.Params)
			if summary, ok := simulateResearchPosition(rows, params, instrument.Symbol, simulation); ok {
				base["position_simulation"] = summary
			}
		}
	}
	delete(base, "_rows")
	delete(base, "_params")
	for _, state := range states {
		delete(state, "_rows")
		delete(state, "_params")
	}
	return base
}

func (h *ResearchStatusHandler) instrumentIntervalStatus(ctx context.Context, instrument marketdata.ResearchInstrument, interval string, simulation positionSimulationQuery) gin.H {
	base := gin.H{
		"interval":       interval,
		"execution_mode": marketdata.ExecutionModeCloseSameBar,
	}
	var champion saasstore.GeneRecord
	if err := h.db.WithContext(ctx).
		Where("strategy_id = ? AND instrument_id = ? AND data_source = ? AND interval = ? AND execution_mode = ? AND role = ?",
			sigmoiddca.StrategyID, instrument.ID, instrument.DataSource, interval, marketdata.ExecutionModeCloseSameBar, saasstore.GeneRoleChampion).
		Order("activated_at DESC NULLS LAST, created_at DESC").
		First(&champion).Error; err != nil {
		base["status"] = "missing_champion"
		return base
	}

	var rows []saasstore.KLine
	if err := h.db.WithContext(ctx).
		Where("instrument_id = ? AND source = ? AND interval = ?", instrument.ID, instrument.DataSource, interval).
		Order("open_time ASC").
		Find(&rows).Error; err != nil || len(rows) == 0 {
		base["status"] = "missing_data"
		if err != nil {
			base["error"] = err.Error()
		}
		return base
	}
	if interval == "1d" {
		rows = completedDailyRows(instrument, rows, time.Now().UTC())
		if len(rows) == 0 {
			base["status"] = "missing_data"
			base["error"] = "no completed daily bars"
			return base
		}
	}

	params := sigmoiddca.ParseParamsFromParamPack([]byte(champion.ParamPack))
	closes := make([]float64, 0, len(rows))
	timestamps := make([]int64, 0, len(rows))
	for _, row := range rows {
		closes = append(closes, row.Close)
		timestamps = append(timestamps, row.OpenTime)
	}
	latest := rows[len(rows)-1]
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
	base["_rows"] = rows
	base["_params"] = params
	if model, ok := simulateResearchModel(rows, params, interval, simulation); ok {
		base["model_simulation"] = model
		if latestTarget, ok := model["latest_model_target_weight"]; ok {
			base["target_weight"] = latestTarget
		}
		if latestDelta, ok := model["latest_model_target_weight_change"]; ok {
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

func instrumentSupportsInterval(instrument marketdata.ResearchInstrument, interval string) bool {
	for _, supported := range instrument.SupportedIntervals {
		if supported == interval {
			return true
		}
	}
	return false
}

func simulateResearchModel(rows []saasstore.KLine, params sigmoiddca.Params, interval string, settings positionSimulationQuery) (gin.H, bool) {
	if len(rows) == 0 {
		return nil, false
	}
	spawn := params.Spawn
	if settings.InitialCapital > 0 {
		spawn.Policy.InitialUSDT = settings.InitialCapital
	}
	if settings.MonthlyDCA >= 0 {
		spawn.Policy.MonthlyInjectUSDT = settings.MonthlyDCA
	}
	bars := barsFromRows(rows)
	path := ga.RunSigmoidDCAPathBacktestWithMode(bars, rows[0].OpenTime, interval, marketdata.ExecutionModeCloseSameBar, params.Chromosome, &spawn)
	baseline := quant.SimulateGhostDCAFrom(bars, rows[0].OpenTime, quant.GhostDCAConfig{
		InitialUSDT:       spawn.Policy.InitialUSDT,
		MonthlyInjectUSDT: spawn.Policy.MonthlyInjectUSDT,
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
		"start_time_ms":                               rows[0].OpenTime,
		"latest_time_ms":                              latest["time_ms"],
		"latest_time":                                 latest["time"],
		"initial_capital":                             spawn.Policy.InitialUSDT,
		"monthly_dca":                                 spawn.Policy.MonthlyInjectUSDT,
		"latest_nav":                                  latest["model_nav"],
		"previous_nav":                                previous["model_nav"],
		"nav_change_pct":                              latest["model_nav_change_pct"],
		"latest_benchmark":                            latest["benchmark"],
		"benchmark_change_pct":                        latest["benchmark_change_pct"],
		"latest_model_target_weight":                  latest["model_target_weight"],
		"previous_model_target_weight":                previous["model_target_weight"],
		"latest_model_target_weight_change":           latest["model_target_weight_change"],
		"latest_empty_reference_target_weight":        latest["empty_reference_target_weight"],
		"previous_empty_reference_target_weight":      previous["empty_reference_target_weight"],
		"latest_empty_reference_target_weight_change": latest["empty_reference_target_weight_change"],
		"points":       len(points),
		"chart_points": points,
	}, true
}

func simulateResearchPosition(rows []saasstore.KLine, params sigmoiddca.Params, symbol string, settings positionSimulationQuery) (gin.H, bool) {
	if settings.StartTimeMs <= 0 || settings.InitialCapital <= 0 || len(rows) == 0 {
		return nil, false
	}

	closes := make([]float64, 0, len(rows))
	timestamps := make([]int64, 0, len(rows))
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

	for _, row := range rows {
		if row.Close <= 0 {
			continue
		}
		closes = append(closes, row.Close)
		timestamps = append(timestamps, row.OpenTime)
		if !started && row.OpenTime < settings.StartTimeMs {
			continue
		}
		year, month := time.UnixMilli(row.OpenTime).UTC().Year(), time.UnixMilli(row.OpenTime).UTC().Month()
		contribution := 0.0
		if !started {
			started = true
			cash = settings.InitialCapital
			invested = settings.InitialCapital
			lastYear = year
			lastMonth = month
			startedAtMs = row.OpenTime
		} else if (year != lastYear || month != lastMonth) && settings.MonthlyDCA > 0 {
			cash += settings.MonthlyDCA
			invested += settings.MonthlyDCA
			contribution = settings.MonthlyDCA
			lastYear = year
			lastMonth = month
		}

		equityBefore := cash + assetQty*row.Close
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
		}, params)
		state = output.RuntimeState
		targetWeight := currentWeight(assetQty, row.Close, equityBefore)
		if raw, ok := output.Diagnostics["target_weight"]; ok {
			targetWeight = clamp01(raw)
		}
		targetValue := equityBefore * targetWeight
		currentValue := assetQty * row.Close
		deltaValue := targetValue - currentValue
		if deltaValue > 0 {
			buyValue := math.Min(deltaValue, cash)
			assetQty += buyValue / row.Close
			cash -= buyValue
		} else if deltaValue < 0 {
			sellQty := math.Min(-deltaValue/row.Close, assetQty)
			assetQty -= sellQty
			cash += sellQty * row.Close
		}

		previousNAV = latestNAV
		previousTargetWeight = latestTargetWeight
		previousActualWeight = latestActualWeight
		latestNAV = cash + assetQty*row.Close
		latestTargetWeight = targetWeight
		latestActualWeight = currentWeight(assetQty, row.Close, latestNAV)
		latestContribution = contribution
		latestMs = row.OpenTime
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

func barsFromRows(rows []saasstore.KLine) []quant.Bar {
	bars := make([]quant.Bar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, quant.Bar{
			OpenTime: row.OpenTime,
			Open:     row.Open,
			High:     row.High,
			Low:      row.Low,
			Close:    row.Close,
			Volume:   row.Volume,
		})
	}
	return bars
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

func completedDailyRows(instrument marketdata.ResearchInstrument, rows []saasstore.KLine, now time.Time) []saasstore.KLine {
	out := make([]saasstore.KLine, 0, len(rows))
	for _, row := range rows {
		if isCompletedDailyBar(instrument, row.OpenTime, now) {
			out = append(out, row)
		}
	}
	return out
}

func isCompletedDailyBar(instrument marketdata.ResearchInstrument, openTimeMs int64, now time.Time) bool {
	openTime := time.UnixMilli(openTimeMs)
	switch instrument.ID {
	case marketdata.InstrumentBTCUSDT:
		return !openTime.Add(24 * time.Hour).After(now)
	case "TWII":
		loc, err := time.LoadLocation("Asia/Taipei")
		if err != nil {
			loc = time.FixedZone("Asia/Taipei", 8*3600)
		}
		local := openTime.In(loc)
		closeAt := time.Date(local.Year(), local.Month(), local.Day(), 13, 30, 0, 0, loc)
		return !closeAt.UTC().After(now)
	default:
		loc, err := time.LoadLocation("America/New_York")
		if err != nil {
			loc = time.FixedZone("America/New_York", -5*3600)
		}
		local := openTime.In(loc)
		closeAt := time.Date(local.Year(), local.Month(), local.Day(), 16, 0, 0, 0, loc)
		return !closeAt.UTC().After(now)
	}
}
