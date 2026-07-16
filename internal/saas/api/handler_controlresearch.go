package api

import (
	"errors"
	"net/http"
	"strconv"

	"quantsaas/internal/saas/computetask"
	controlresearchsvc "quantsaas/internal/saas/controlresearch"

	"github.com/gin-gonic/gin"
)

type ControlResearchHandler struct{ service *controlresearchsvc.Service }

func NewControlResearchHandler(service *controlresearchsvc.Service) *ControlResearchHandler {
	return &ControlResearchHandler{service: service}
}

func (h *ControlResearchHandler) Preview(c *gin.Context) {
	var request controlresearchsvc.CreateRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "對照研究設定格式無效"})
		return
	}
	result, err := h.service.Preview(c.Request.Context(), currentUserID(c), request)
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) Create(c *gin.Context) {
	var request controlresearchsvc.CreateRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "對照研究設定格式無效"})
		return
	}
	result, err := h.service.Create(c.Request.Context(), currentUserID(c), request)
	h.respond(c, http.StatusCreated, result, err)
}

func (h *ControlResearchHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	result, err := h.service.List(c.Request.Context(), currentUserID(c), limit)
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) Get(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.Get(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) StartNext(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.StartNext(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) Cancel(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.Cancel(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) Retry(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.Retry(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) PreviewExtension(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var request controlresearchsvc.ExtendRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "追加設定格式無效"})
		return
	}
	result, err := h.service.PreviewExtension(c.Request.Context(), currentUserID(c), id, request)
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) Extend(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var request controlresearchsvc.ExtendRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "追加設定格式無效"})
		return
	}
	result, err := h.service.Extend(c.Request.Context(), currentUserID(c), id, request)
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) Snapshots(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.Snapshots(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) RandomRecords(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	result, err := h.service.RandomRecords(c.Request.Context(), currentUserID(c), id, limit, offset)
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) Detail(c *gin.Context) {
	taskID, ok := parseIDParam(c)
	if !ok {
		return
	}
	snapshotID, err := strconv.ParseUint(c.Param("snapshotID"), 10, 64)
	if err != nil || snapshotID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "snapshot id 無效"})
		return
	}
	result, serviceErr := h.service.Detail(c.Request.Context(), currentUserID(c), taskID, uint(snapshotID))
	h.respond(c, http.StatusOK, result, serviceErr)
}

func (h *ControlResearchHandler) PathBlock(c *gin.Context) {
	evaluationID, err := strconv.ParseUint(c.Param("evaluationID"), 10, 64)
	blockIndex, blockErr := strconv.Atoi(c.DefaultQuery("block", "0"))
	if err != nil || evaluationID == 0 || blockErr != nil || blockIndex < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "明細路徑參數無效"})
		return
	}
	result, serviceErr := h.service.PathBlock(c.Request.Context(), currentUserID(c), uint(evaluationID), blockIndex)
	h.respond(c, http.StatusOK, result, serviceErr)
}

func (h *ControlResearchHandler) Comparison(c *gin.Context) {
	taskID, ok := parseIDParam(c)
	if !ok {
		return
	}
	snapshotID, err := strconv.ParseUint(c.Param("snapshotID"), 10, 64)
	if err != nil || snapshotID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "snapshot id 無效"})
		return
	}
	result, serviceErr := h.service.Comparison(c.Request.Context(), currentUserID(c), taskID, uint(snapshotID))
	h.respond(c, http.StatusOK, result, serviceErr)
}

func (h *ControlResearchHandler) UpdateMetadata(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var request controlresearchsvc.UpdateMetadataRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "中繼資料格式無效"})
		return
	}
	result, err := h.service.UpdateMetadata(c.Request.Context(), currentUserID(c), id, request)
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) DeleteImpact(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.DeleteImpact(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) DeleteTask(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.DeleteTask(c.Request.Context(), currentUserID(c), id, c.Query("confirm") == "true")
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) DeletePathDetails(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.DeletePathDetails(c.Request.Context(), currentUserID(c), id, c.Query("confirm") == "true")
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) DeleteUnusedBatch(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.DeleteUnusedBatch(c.Request.Context(), currentUserID(c), id, c.Query("confirm") == "true")
	h.respond(c, http.StatusOK, result, err)
}

func (h *ControlResearchHandler) respond(c *gin.Context, success int, value any, err error) {
	if err == nil {
		c.JSON(success, value)
		return
	}
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, controlresearchsvc.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, controlresearchsvc.ErrInvalidRequest):
		status = http.StatusBadRequest
	case errors.Is(err, controlresearchsvc.ErrPlanStale), errors.Is(err, computetask.ErrSoftLimitConfirm):
		status = http.StatusConflict
	case errors.Is(err, computetask.ErrHardLimitExceeded):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, computetask.ErrServiceUnavailable):
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
