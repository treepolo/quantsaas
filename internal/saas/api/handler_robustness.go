package api

import (
	"errors"
	"net/http"
	"strconv"

	"quantsaas/internal/saas/computetask"
	robustnesssvc "quantsaas/internal/saas/robustness"

	"github.com/gin-gonic/gin"
)

type RobustnessHandler struct{ service *robustnesssvc.Service }

func NewRobustnessHandler(service *robustnesssvc.Service) *RobustnessHandler {
	return &RobustnessHandler{service: service}
}

func (h *RobustnessHandler) Parameters(c *gin.Context) {
	id, err := strconv.ParseUint(c.Query("genome_id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "genome_id is required"})
		return
	}
	definitions, values, err := h.service.ParameterDefinitions(c.Request.Context(), uint(id))
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"definitions": definitions, "values": values})
}

func (h *RobustnessHandler) Preview(c *gin.Context) {
	var req robustnesssvc.CreateStudyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	preview, err := h.service.Preview(c.Request.Context(), currentUserID(c), req)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, preview)
}

func (h *RobustnessHandler) Create(c *gin.Context) {
	var req robustnesssvc.CreateStudyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Create(c.Request.Context(), currentUserID(c), req)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (h *RobustnessHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	rows, err := h.service.List(c.Request.Context(), currentUserID(c), limit)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (h *RobustnessHandler) Get(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.Get(c.Request.Context(), currentUserID(c), id)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *RobustnessHandler) Analyze(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req robustnesssvc.AnalyzeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Analyze(c.Request.Context(), currentUserID(c), id, req)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *RobustnessHandler) Import(c *gin.Context) {
	var req robustnesssvc.ImportStudyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Import(c.Request.Context(), currentUserID(c), req)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (h *RobustnessHandler) respondError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, robustnesssvc.ErrStudyNotFound):
		status = http.StatusNotFound
	case errors.Is(err, robustnesssvc.ErrInvalidRequest), errors.Is(err, robustnesssvc.ErrStudyNotReady):
		status = http.StatusBadRequest
	case errors.Is(err, computetask.ErrSoftLimitConfirm):
		status = http.StatusConflict
	case errors.Is(err, computetask.ErrHardLimitExceeded):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, computetask.ErrServiceUnavailable):
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
