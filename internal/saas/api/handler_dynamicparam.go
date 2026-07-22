package api

import (
	"errors"
	"net/http"
	"strconv"

	"quantsaas/internal/saas/computetask"
	dynamicparamsvc "quantsaas/internal/saas/dynamicparam"

	"github.com/gin-gonic/gin"
)

type DynamicParameterHandler struct{ service *dynamicparamsvc.Service }

func NewDynamicParameterHandler(service *dynamicparamsvc.Service) *DynamicParameterHandler {
	return &DynamicParameterHandler{service: service}
}

func (h *DynamicParameterHandler) Preview(c *gin.Context) {
	var req dynamicparamsvc.CreateStudyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Preview(c.Request.Context(), currentUserID(c), req)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *DynamicParameterHandler) Create(c *gin.Context) {
	var req dynamicparamsvc.CreateStudyRequest
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

func (h *DynamicParameterHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	result, err := h.service.List(c.Request.Context(), currentUserID(c), limit)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *DynamicParameterHandler) Get(c *gin.Context) {
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

func (h *DynamicParameterHandler) Materialize(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req dynamicparamsvc.MaterializeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Materialize(c.Request.Context(), currentUserID(c), id, req)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (h *DynamicParameterHandler) PreviewMaterialize(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req dynamicparamsvc.MaterializeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.PreviewMaterialize(c.Request.Context(), currentUserID(c), id, req)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *DynamicParameterHandler) ReportBlock(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.ReportBlock(c.Request.Context(), currentUserID(c), id, c.Param("blockID"))
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *DynamicParameterHandler) respondError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, dynamicparamsvc.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, dynamicparamsvc.ErrInvalidRequest):
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
