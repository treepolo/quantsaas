package api

import (
	"errors"
	"net/http"

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

func (h *ResearchDataHandler) canUseLab() bool {
	return h.appRole == config.AppRoleLab || h.appRole == config.AppRoleDev
}
