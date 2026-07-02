package api

import (
	"errors"
	"net/http"
	"strconv"

	"quantsaas/internal/saas/config"
	"quantsaas/internal/saas/researchdata"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ResearchDataHandler struct {
	appRole string
	service *researchdata.Service
}

func NewResearchDataHandler(appRole string, db *gorm.DB) *ResearchDataHandler {
	return &ResearchDataHandler{
		appRole: appRole,
		service: researchdata.NewService(db),
	}
}

func (h *ResearchDataHandler) Preview(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	var req researchdata.PreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Preview(c.Request.Context(), req)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, researchdata.ErrInvalidDatasetRequest) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *ResearchDataHandler) List(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	rows, err := h.service.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"datasets": rows})
}

func (h *ResearchDataHandler) Get(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	id, ok := researchDatasetID(c)
	if !ok {
		return
	}
	result, err := h.service.Get(c.Request.Context(), id, c.Query("preview") == "1" || c.Query("preview") == "true")
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *ResearchDataHandler) Create(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	var req researchdata.DatasetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Create(c.Request.Context(), req)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (h *ResearchDataHandler) Update(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	id, ok := researchDatasetID(c)
	if !ok {
		return
	}
	var req researchdata.DatasetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Update(c.Request.Context(), id, req)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *ResearchDataHandler) Delete(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "lab/dev only"})
		return
	}
	id, ok := researchDatasetID(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted", "id": id})
}

func (h *ResearchDataHandler) respondError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, researchdata.ErrInvalidDatasetRequest) {
		status = http.StatusBadRequest
	}
	if errors.Is(err, researchdata.ErrDatasetNotFound) {
		status = http.StatusNotFound
	}
	c.JSON(status, gin.H{"error": err.Error()})
}

func (h *ResearchDataHandler) canUseLab() bool {
	return h.appRole == config.AppRoleLab || h.appRole == config.AppRoleDev
}

func researchDatasetID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dataset id"})
		return 0, false
	}
	return uint(id), true
}
