package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"quantsaas/internal/saas/computetask"
	geometry "quantsaas/internal/saas/geometry"
)

type GeometryHandler struct{ service *geometry.Service }

func NewGeometryHandler(service *geometry.Service) *GeometryHandler {
	return &GeometryHandler{service: service}
}
func (h *GeometryHandler) Preview(c *gin.Context) {
	var req geometry.CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Preview(c.Request.Context(), currentUserID(c), req)
	if err != nil {
		geometryError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
func (h *GeometryHandler) Create(c *gin.Context) {
	var req geometry.CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Create(c.Request.Context(), currentUserID(c), req)
	if err != nil {
		geometryError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}
func (h *GeometryHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	result, err := h.service.List(c.Request.Context(), currentUserID(c), limit)
	if err != nil {
		geometryError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
func (h *GeometryHandler) Get(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.Get(c.Request.Context(), currentUserID(c), id)
	if err != nil {
		geometryError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *GeometryHandler) Artifacts(c *gin.Context) {
	result, err := h.service.CompatibleArtifacts(c.Request.Context(), currentUserID(c), c.Query("instrument_id"), c.Query("data_source"), c.Query("symbol"), c.Query("interval"), c.Query("dataset_hash"), queryInt(c, "horizon"))
	if err != nil {
		geometryError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func queryInt(c *gin.Context, key string) int { value, _ := strconv.Atoi(c.Query(key)); return value }
func geometryError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, geometry.ErrInvalidRequest):
		status = http.StatusBadRequest
	case errors.Is(err, geometry.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, computetask.ErrSoftLimitConfirm):
		status = http.StatusConflict
	case errors.Is(err, computetask.ErrHardLimitExceeded):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, computetask.ErrServiceUnavailable):
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
