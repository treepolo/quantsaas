package api

import (
	"errors"
	"net/http"
	"strings"

	"quantsaas/internal/saas/config"
	"quantsaas/internal/saas/marketdata"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type MarketDataHandler struct {
	appRole string
	service *marketdata.Service
}

func NewMarketDataHandler(appRole string, db *gorm.DB) *MarketDataHandler {
	return &MarketDataHandler{
		appRole: appRole,
		service: marketdata.NewService(db, nil),
	}
}

func (h *MarketDataHandler) Instruments(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	instruments, err := h.service.Instruments(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"instruments":     instruments,
		"execution_modes": marketdata.SupportedExecutionModes(),
	})
}

func (h *MarketDataHandler) UpsertInstrument(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	var req marketdata.UpsertInstrumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	instrument, err := h.service.UpsertInstrument(c.Request.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, marketdata.ErrUnsupportedInstrument) || errors.Is(err, marketdata.ErrUnsupportedSource) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, instrument)
}

func (h *MarketDataHandler) DeleteInstrument(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	if err := h.service.DisableInstrument(c.Request.Context(), c.Param("id")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, marketdata.ErrUnsupportedInstrument) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *MarketDataHandler) ReorderInstruments(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	var req marketdata.ReorderInstrumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.ReorderInstruments(c.Request.Context(), req); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, marketdata.ErrUnsupportedInstrument) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "reordered"})
}

func (h *MarketDataHandler) Series(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	rows, err := h.service.Series(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"series": rows,
		"series_types": []string{
			marketdata.SeriesTypeTradableAsset,
			marketdata.SeriesTypeIndicator,
			marketdata.SeriesTypeDerived,
		},
	})
}

func (h *MarketDataHandler) UpsertSeries(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	var req marketdata.UpsertSeriesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	series, err := h.service.UpsertSeries(c.Request.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, marketdata.ErrUnsupportedSeries) || errors.Is(err, marketdata.ErrUnsupportedSeriesType) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, series)
}

func (h *MarketDataHandler) DeleteSeries(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	if err := h.service.DisableSeries(c.Request.Context(), c.Param("id")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, marketdata.ErrUnsupportedSeries) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *MarketDataHandler) SyncTradableAssetSeries(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	if err := h.service.SyncTradableAssetSeries(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "synced"})
}

func (h *MarketDataHandler) DatasetPreview(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	var req marketdata.DatasetBuildRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.MaxRows == 0 {
		req.MaxRows = 500
	}
	result, err := h.service.BuildDataset(c.Request.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, marketdata.ErrInvalidDatasetRequest) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *MarketDataHandler) RefreshInstrumentStarts(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	result, err := h.service.RefreshAvailableStarts(c.Request.Context(), c.Param("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, marketdata.ErrUnsupportedInstrument) || errors.Is(err, marketdata.ErrUnsupportedSource) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *MarketDataHandler) RefreshAllInstrumentStarts(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	results, err := h.service.RefreshAllAvailableStarts(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

func (h *MarketDataHandler) Status(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	symbol := strings.TrimSpace(c.Query("symbol"))
	instrumentID := strings.TrimSpace(c.Query("instrument_id"))
	instrument, err := h.service.ResolveInstrument(c.Request.Context(), instrumentID, symbol, c.Query("data_source"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var intervals []string
	if raw := strings.TrimSpace(c.Query("intervals")); raw != "" {
		intervals = strings.Split(raw, ",")
	}
	rows, err := h.service.Summaries(c.Request.Context(), instrument.Symbol, intervals)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"instrument":          instrument,
		"symbol":              instrument.Symbol,
		"instrument_id":       instrument.ID,
		"data_source":         instrument.DataSource,
		"supported_intervals": instrument.SupportedIntervals,
		"datasets":            rows,
	})
}

func (h *MarketDataHandler) Overview(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	rows, err := h.service.AllSummaries(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func (h *MarketDataHandler) UpdateLatest(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	rows, err := h.service.UpdateLatest(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": rows})
}

func (h *MarketDataHandler) AuditMaintenance(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	rows, err := h.service.AuditMaintenance(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": rows})
}

func (h *MarketDataHandler) RepairMaintenance(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	rows, err := h.service.RepairMaintenance(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": rows})
}

func (h *MarketDataHandler) Import(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	var req marketdata.ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Import(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, marketdata.ErrUnsupportedInterval), errors.Is(err, marketdata.ErrInvalidRange):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *MarketDataHandler) canUseLab() bool {
	return h.appRole == config.AppRoleLab || h.appRole == config.AppRoleDev
}
