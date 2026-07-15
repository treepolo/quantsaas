package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"quantsaas/internal/saas/computetask"

	"github.com/gin-gonic/gin"
)

type ComputeTaskHandler struct {
	service *computetask.Service
}

func NewComputeTaskHandler(service *computetask.Service) *ComputeTaskHandler {
	return &ComputeTaskHandler{service: service}
}

func (h *ComputeTaskHandler) Limits(c *gin.Context) {
	if h.service == nil {
		h.respondError(c, computetask.ErrServiceUnavailable)
		return
	}
	c.JSON(http.StatusOK, h.service.Limits())
}

func (h *ComputeTaskHandler) List(c *gin.Context) {
	parentID, ok := optionalUintQuery(c, "parent_task_id")
	if !ok {
		return
	}
	rows, err := h.service.List(c.Request.Context(), currentUserID(c), computetask.ListFilter{
		Status: c.Query("status"), ParentTaskID: parentID,
		RootOnly: c.Query("root_only") == "1" || strings.EqualFold(c.Query("root_only"), "true"),
		Limit:    parseIntQuery(c.Query("limit")), Offset: parseIntQuery(c.Query("offset")),
	})
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (h *ComputeTaskHandler) Get(c *gin.Context) {
	taskID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	task, err := h.service.Get(c.Request.Context(), currentUserID(c), taskID)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, task)
}

func (h *ComputeTaskHandler) Snapshot(c *gin.Context) {
	taskID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	snapshot, err := h.service.Snapshot(c.Request.Context(), currentUserID(c), taskID)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

func (h *ComputeTaskHandler) Preview(c *gin.Context) {
	taskID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	preview, err := h.service.PreviewTask(c.Request.Context(), currentUserID(c), taskID)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, preview)
}

func (h *ComputeTaskHandler) Items(c *gin.Context) {
	taskID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	items, err := h.service.Items(c.Request.Context(), currentUserID(c), taskID, computetask.ItemFilter{
		Status: c.Query("status"), Limit: parseIntQuery(c.Query("limit")), Offset: parseIntQuery(c.Query("offset")),
		IncludeResult: c.Query("include_result") == "1" || strings.EqualFold(c.Query("include_result"), "true"),
	})
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (h *ComputeTaskHandler) Start(c *gin.Context) {
	taskID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	task, err := h.service.StartTask(c.Request.Context(), currentUserID(c), taskID)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, task)
}

func (h *ComputeTaskHandler) Cancel(c *gin.Context) {
	taskID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	task, err := h.service.Cancel(c.Request.Context(), currentUserID(c), taskID)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, task)
}

func (h *ComputeTaskHandler) Retry(c *gin.Context) {
	taskID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	task, err := h.service.Retry(c.Request.Context(), currentUserID(c), taskID)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, task)
}

func (h *ComputeTaskHandler) CacheLookup(c *gin.Context) {
	var request struct {
		CacheKey string `json:"cache_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.LookupCache(c.Request.Context(), currentUserID(c), request.CacheKey)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *ComputeTaskHandler) respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, computetask.ErrAccessNotFound), errors.Is(err, computetask.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "找不到計算任務"})
	case errors.Is(err, computetask.ErrDependencyPending):
		c.JSON(http.StatusConflict, gin.H{"error": "前置階段尚未完成"})
	case errors.Is(err, computetask.ErrInvalidState):
		c.JSON(http.StatusConflict, gin.H{"error": "目前任務狀態不允許此操作"})
	case errors.Is(err, computetask.ErrVersionMismatch), errors.Is(err, computetask.ErrUnknownExecutor):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, computetask.ErrServiceUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
	case errors.Is(err, computetask.ErrHardLimitExceeded), errors.Is(err, computetask.ErrSoftLimitConfirm):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "計算任務操作失敗"})
	}
}

func optionalUintQuery(c *gin.Context, key string) (*uint, bool) {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return nil, true
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + key})
		return nil, false
	}
	result := uint(parsed)
	return &result, true
}

func parseIntQuery(value string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(value))
	return parsed
}
