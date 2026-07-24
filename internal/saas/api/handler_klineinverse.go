package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"quantsaas/internal/saas/computetask"
	klineinversesvc "quantsaas/internal/saas/klineinverse"
	"quantsaas/internal/saas/performancereport"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type KlineInverseHandler struct {
	service     *klineinversesvc.Service
	performance *performancereport.Service
}

func NewKlineInverseHandler(service *klineinversesvc.Service, db *gorm.DB) *KlineInverseHandler {
	return &KlineInverseHandler{service: service, performance: performancereport.NewService(db)}
}

func (h *KlineInverseHandler) CreateDraft(c *gin.Context) {
	var request klineinversesvc.CreateDraftRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "K 線樣貌反推設定格式無效"})
		return
	}
	result, err := h.service.CreateDraft(c.Request.Context(), currentUserID(c), request)
	h.respond(c, http.StatusCreated, result, err)
}

func (h *KlineInverseHandler) Availability(c *gin.Context) {
	var request klineinversesvc.AvailabilityRequest
	if c.ShouldBindQuery(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "K 線可用範圍查詢格式無效"})
		return
	}
	result, err := h.service.Availability(c.Request.Context(), request)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) List(c *gin.Context) {
	result, err := h.service.List(c.Request.Context(), currentUserID(c), c.Query("include_archived") == "true")
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) Get(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	result, err := h.service.Get(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) Plan(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	result, err := h.service.Plan(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) Start(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	var request klineinversesvc.StartRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "啟動設定格式無效"})
		return
	}
	result, err := h.service.Start(c.Request.Context(), currentUserID(c), id, request)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) Archive(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	result, err := h.service.Archive(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) StartNext(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	result, err := h.service.StartNextStage(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) Cancel(c *gin.Context) {
	studyID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	batchID, ok := parseUintPath(c, "batchId")
	if !ok {
		return
	}
	result, err := h.service.CancelBatch(c.Request.Context(), currentUserID(c), studyID, batchID)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) Resume(c *gin.Context) {
	studyID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	batchID, ok := parseUintPath(c, "batchId")
	if !ok {
		return
	}
	result, err := h.service.ResumeBatch(c.Request.Context(), currentUserID(c), studyID, batchID)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) PlanExtension(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	var request klineinversesvc.ExtensionPlanRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "追加預算格式無效"})
		return
	}
	result, err := h.service.PlanExtension(c.Request.Context(), currentUserID(c), id, request)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) StartExtension(c *gin.Context) { h.startAppend(c) }

func (h *KlineInverseHandler) PlanProbe(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	var request klineinversesvc.ProbePlanRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "錨點探測格式無效"})
		return
	}
	result, err := h.service.PlanProbe(c.Request.Context(), currentUserID(c), id, request)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) StartProbe(c *gin.Context) { h.startAppend(c) }

func (h *KlineInverseHandler) startAppend(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	var request klineinversesvc.BatchStartRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "批次啟動格式無效"})
		return
	}
	result, err := h.service.StartAppendBatch(c.Request.Context(), currentUserID(c), id, request)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) Overview(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	result, err := h.service.Overview(c.Request.Context(), currentUserID(c), id, queryUint(c, "snapshot_id"))
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) Map(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	result, err := h.service.Map(c.Request.Context(), currentUserID(c), id, queryUint(c, "snapshot_id"), c.DefaultQuery("axis_x", "w_mean_return"), c.DefaultQuery("axis_y", "h_mean_return"), c.DefaultQuery("target", "A"), c.DefaultQuery("color", "target_count"))
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) Paths(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))
	query := klineinversesvc.PathQuery{Page: page, PageSize: pageSize, State: c.Query("state"), Target: c.Query("target")}
	if raw := c.Query("cell_index"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cell_index 無效"})
			return
		}
		query.CellIndex = &value
	}
	if raw := c.Query("permanent"); raw != "" {
		value := raw == "true"
		query.Permanent = &value
	}
	result, err := h.service.Paths(c.Request.Context(), currentUserID(c), id, query)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) Path(c *gin.Context) {
	studyID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	pathID, ok := parseUintPath(c, "pathId")
	if !ok {
		return
	}
	result, err := h.service.Path(c.Request.Context(), currentUserID(c), studyID, pathID)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) ChartSeries(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	result, err := h.service.ChartSeries(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) Lineage(c *gin.Context) {
	studyID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	pathID, ok := parseUintPath(c, "pathId")
	if !ok {
		return
	}
	result, err := h.service.Lineage(c.Request.Context(), currentUserID(c), studyID, pathID)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) Boundary(c *gin.Context) {
	studyID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	pathID, ok := parseUintPath(c, "pathId")
	if !ok {
		return
	}
	batchIDs := []uint{}
	for _, raw := range strings.Split(c.Query("batch_ids"), ",") {
		if raw == "" {
			continue
		}
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || value == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "batch_ids 無效"})
			return
		}
		batchIDs = append(batchIDs, uint(value))
	}
	result, err := h.service.Boundary(c.Request.Context(), currentUserID(c), studyID, pathID, batchIDs)
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) Comparison(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	result, err := h.service.Comparison(c.Request.Context(), currentUserID(c), id, queryUint(c, "snapshot_id"))
	h.respond(c, http.StatusOK, result, err)
}

func (h *KlineInverseHandler) CreatePerformanceReport(c *gin.Context) {
	studyID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	pathID, ok := parseUintPath(c, "pathId")
	if !ok {
		return
	}
	path, err := h.service.Path(c.Request.Context(), currentUserID(c), studyID, pathID)
	if err != nil || path.PermanentReason == "" {
		h.respond(c, http.StatusOK, nil, klineinversesvc.ErrNotFound)
		return
	}
	var request performancereport.CreateRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "報酬分析設定格式無效"})
		return
	}
	report, err := h.performance.Create(c.Request.Context(), currentUserID(c), path.BacktestResultID, request)
	status := http.StatusCreated
	if report != nil && report.Reused {
		status = http.StatusOK
	}
	if err != nil {
		handlePerformanceError(c, err)
		return
	}
	c.JSON(status, report)
}

func (h *KlineInverseHandler) respond(c *gin.Context, success int, value any, err error) {
	if err == nil {
		c.JSON(success, value)
		return
	}
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, klineinversesvc.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, klineinversesvc.ErrInvalidRequest), errors.Is(err, klineinversesvc.ErrDynamicSource):
		status = http.StatusBadRequest
	case errors.Is(err, klineinversesvc.ErrPlanStale), errors.Is(err, computetask.ErrSoftLimitConfirm):
		status = http.StatusConflict
	case errors.Is(err, computetask.ErrHardLimitExceeded):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, computetask.ErrServiceUnavailable):
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{"error": err.Error()})
}

func queryUint(c *gin.Context, name string) uint {
	value, _ := strconv.ParseUint(c.Query(name), 10, 64)
	return uint(value)
}
