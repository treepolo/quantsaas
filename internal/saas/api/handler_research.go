package api

import (
	"context"
	"encoding/json"
	"net/http"
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
	items := make([]gin.H, 0, len(marketdata.Instruments()))
	for _, instrument := range marketdata.Instruments() {
		items = append(items, h.instrumentStatus(c.Request.Context(), instrument))
	}
	c.JSON(http.StatusOK, gin.H{
		"items":      items,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *ResearchStatusHandler) instrumentStatus(ctx context.Context, instrument marketdata.ResearchInstrument) gin.H {
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
	return base
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
