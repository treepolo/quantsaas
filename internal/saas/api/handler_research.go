package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/saas/marketdata"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ResearchStatusHandler struct {
	db *gorm.DB
}

func NewResearchStatusHandler(db *gorm.DB) *ResearchStatusHandler {
	return &ResearchStatusHandler{db: db}
}

func (h *ResearchStatusHandler) Status(c *gin.Context) {
	simulation := parsePositionSimulationQuery(c)
	items := make([]gin.H, 0, len(marketdata.Instruments()))
	for _, instrument := range marketdata.Instruments() {
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

	var champion saasstore.GeneRecord
	if err := h.db.WithContext(ctx).
		Where("strategy_id = ? AND instrument_id = ? AND data_source = ? AND interval = ? AND execution_mode = ? AND role = ?",
			sigmoiddca.StrategyID, instrument.ID, instrument.DataSource, "1d", marketdata.ExecutionModeCloseSameBar, saasstore.GeneRoleChampion).
		Order("activated_at DESC NULLS LAST, created_at DESC").
		First(&champion).Error; err != nil {
		base["status"] = "missing_champion"
		return base
	}

	var rows []saasstore.KLine
	if err := h.db.WithContext(ctx).
		Where("instrument_id = ? AND source = ? AND interval = ?", instrument.ID, instrument.DataSource, "1d").
		Order("open_time ASC").
		Find(&rows).Error; err != nil || len(rows) == 0 {
		base["status"] = "missing_data"
		if err != nil {
			base["error"] = err.Error()
		}
		return base
	}
	rows = completedDailyRows(instrument, rows, time.Now().UTC())
	if len(rows) == 0 {
		base["status"] = "missing_data"
		base["error"] = "no completed daily bars"
		return base
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
		Interval:   "1d",
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
	base["target_weight"] = output.Diagnostics["target_weight"]
	base["current_weight"] = output.Diagnostics["current_weight"]
	base["delta_weight"] = output.Diagnostics["delta_weight"]
	base["diagnostics"] = output.Diagnostics
	base["parameter_values"] = paramValues
	if summary, ok := simulateResearchPosition(rows, params, instrument.Symbol, simulation); ok {
		base["position_simulation"] = summary
	}
	return base
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
		latestNAV = cash + assetQty*row.Close
		latestTargetWeight = targetWeight
		latestMs = row.OpenTime
		points++
	}

	if !started || points == 0 {
		return nil, false
	}
	navChangePct := 0.0
	if previousNAV > 0 {
		navChangePct = latestNAV/previousNAV - 1
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
		"latest_target_weight":   latestTargetWeight,
		"previous_target_weight": previousTargetWeight,
		"target_weight_delta":    targetDelta,
		"cash_balance":           cash,
		"asset_quantity":         assetQty,
		"points":                 points,
	}, true
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
