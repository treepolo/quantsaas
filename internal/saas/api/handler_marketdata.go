package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"quantsaas/internal/saas/computetask"
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
	return NewMarketDataHandlerWithService(appRole, marketdata.NewService(db, nil))
}

func NewMarketDataHandlerWithService(appRole string, service *marketdata.Service) *MarketDataHandler {
	return &MarketDataHandler{
		appRole: appRole,
		service: service,
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

func (h *MarketDataHandler) GenerateLeveraged(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	var req marketdata.GenerateLeveragedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.GenerateLeveraged(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, marketdata.ErrInvalidGenerateRequest),
			errors.Is(err, marketdata.ErrInvalidRange),
			errors.Is(err, marketdata.ErrUnsupportedInterval),
			errors.Is(err, marketdata.ErrUnsupportedInstrument),
			errors.Is(err, marketdata.ErrNoSourceRows):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *MarketDataHandler) RecompositionSources(c *gin.Context) {
	rows, err := h.service.RecompositionSources(c.Request.Context(), currentUserID(c))
	if err != nil {
		h.respondRecompositionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func (h *MarketDataHandler) MarketChartSources(c *gin.Context) {
	rows, err := h.service.MarketChartSources(c.Request.Context(), currentUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func (h *MarketDataHandler) MarketChartBars(c *gin.Context) {
	versionID, _ := strconv.ParseUint(strings.TrimSpace(c.Query("version_id")), 10, 64)
	startMs, _ := strconv.ParseInt(strings.TrimSpace(c.Query("start_time_ms")), 10, 64)
	endMs, _ := strconv.ParseInt(strings.TrimSpace(c.Query("end_time_ms")), 10, 64)
	limit, _ := strconv.Atoi(strings.TrimSpace(c.Query("limit")))
	rows, err := h.service.MarketChartBars(c.Request.Context(), currentUserID(c), c.Query("instrument_id"), uint(versionID), c.Query("interval"), startMs, endMs, limit)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rows": rows})
}

func (h *MarketDataHandler) RecompositionSourceBars(c *gin.Context) {
	versionID, _ := strconv.ParseUint(strings.TrimSpace(c.Query("version_id")), 10, 64)
	startMs, _ := strconv.ParseInt(strings.TrimSpace(c.Query("start_time_ms")), 10, 64)
	endMs, _ := strconv.ParseInt(strings.TrimSpace(c.Query("end_time_ms")), 10, 64)
	limit, _ := strconv.Atoi(strings.TrimSpace(c.Query("limit")))
	rows, err := h.service.RecompositionSourceBars(
		c.Request.Context(), currentUserID(c), c.Query("instrument_id"), uint(versionID), c.Query("interval"), startMs, endMs, limit,
	)
	if err != nil {
		h.respondRecompositionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"rows": rows})
}

func (h *MarketDataHandler) CreateRecompositionPreview(c *gin.Context) {
	var body struct {
		Request          marketdata.RecompositionPreviewRequest `json:"request"`
		ConfirmSoftLimit bool                                   `json:"confirm_soft_limit"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.CreateRecompositionPreviewTask(c.Request.Context(), currentUserID(c), body.Request, body.ConfirmSoftLimit)
	if err != nil {
		h.respondRecompositionError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (h *MarketDataHandler) GetRecompositionPlan(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.GetRecompositionPlan(c.Request.Context(), currentUserID(c), id)
	if err != nil {
		h.respondRecompositionError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *MarketDataHandler) RecompositionPreviewBars(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	result, err := h.service.RecompositionPreviewBars(c.Request.Context(), currentUserID(c), id, limit, offset)
	if err != nil {
		h.respondRecompositionError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *MarketDataHandler) CreateRecompositionGeneration(c *gin.Context) {
	var body struct {
		Request          marketdata.RecompositionGenerationRequest `json:"request"`
		ConfirmSoftLimit bool                                      `json:"confirm_soft_limit"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.CreateRecompositionGeneration(c.Request.Context(), currentUserID(c), body.Request, body.ConfirmSoftLimit)
	if err != nil {
		h.respondRecompositionError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (h *MarketDataHandler) GetRecompositionGeneration(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.RecompositionGeneration(c.Request.Context(), currentUserID(c), id)
	if err != nil {
		h.respondRecompositionError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *MarketDataHandler) MarketSeries(c *gin.Context) {
	includeArchived := strings.EqualFold(c.Query("include_archived"), "true")
	result, err := h.service.MarketSeries(c.Request.Context(), currentUserID(c), includeArchived)
	if err != nil {
		h.respondRecompositionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": result})
}

func (h *MarketDataHandler) ArchiveMarketVersion(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	if err := h.service.ArchiveMarketVersion(c.Request.Context(), currentUserID(c), id); err != nil {
		h.respondRecompositionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "archived"})
}

func (h *MarketDataHandler) ArchiveMarketSeries(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	if err := h.service.ArchiveMarketSeries(c.Request.Context(), currentUserID(c), id); err != nil {
		h.respondRecompositionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "archived"})
}

func (h *MarketDataHandler) respondRecompositionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, marketdata.ErrInvalidRecomposition), errors.Is(err, marketdata.ErrUnsupportedInterval),
		errors.Is(err, marketdata.ErrUnsupportedInstrument), errors.Is(err, marketdata.ErrUnsupportedSourceKind):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, marketdata.ErrRecompositionPlanNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "找不到指定的重組資料"})
	case errors.Is(err, marketdata.ErrStaleRecompositionPlan), errors.Is(err, computetask.ErrInvalidState), errors.Is(err, computetask.ErrDependencyPending):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, computetask.ErrHardLimitExceeded), errors.Is(err, computetask.ErrSoftLimitConfirm):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	case errors.Is(err, computetask.ErrServiceUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (h *MarketDataHandler) canUseLab() bool {
	return h.appRole == config.AppRoleLab || h.appRole == config.AppRoleDev
}
