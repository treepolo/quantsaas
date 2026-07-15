package api

import (
	"errors"
	"net/http"
	"strconv"

	"quantsaas/internal/saas/performancereport"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type PerformanceReportHandler struct {
	service *performancereport.Service
}

func NewPerformanceReportHandler(db *gorm.DB) *PerformanceReportHandler {
	return &PerformanceReportHandler{service: performancereport.NewService(db)}
}

func (h *PerformanceReportHandler) Create(c *gin.Context) {
	resultID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	var request performancereport.CreateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	report, err := h.service.Create(c.Request.Context(), currentUserID(c), resultID, request)
	if err != nil {
		handlePerformanceError(c, err)
		return
	}
	status := http.StatusCreated
	if report.Reused {
		status = http.StatusOK
	}
	c.JSON(status, report)
}

func (h *PerformanceReportHandler) ListForResult(c *gin.Context) {
	resultID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	reports, err := h.service.ListForResult(c.Request.Context(), currentUserID(c), resultID)
	if err != nil {
		handlePerformanceError(c, err)
		return
	}
	c.JSON(http.StatusOK, reports)
}

func (h *PerformanceReportHandler) Get(c *gin.Context) {
	reportID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	report, err := h.service.Get(c.Request.Context(), currentUserID(c), reportID)
	if err != nil {
		handlePerformanceError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

func (h *PerformanceReportHandler) GetChart(c *gin.Context) {
	reportID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	chart, err := h.service.GetChart(c.Request.Context(), currentUserID(c), reportID, c.Param("kind"))
	if err != nil {
		handlePerformanceError(c, err)
		return
	}
	c.JSON(http.StatusOK, chart)
}

func (h *PerformanceReportHandler) Verify(c *gin.Context) {
	reportID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	report, err := h.service.Verify(c.Request.Context(), currentUserID(c), reportID)
	if err != nil {
		handlePerformanceError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

func (h *PerformanceReportHandler) LatestForGenome(c *gin.Context) {
	genomeID, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	response, err := h.service.LatestForGenome(c.Request.Context(), currentUserID(c), genomeID)
	if err != nil {
		handlePerformanceError(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func parseUintPath(c *gin.Context, name string) (uint, bool) {
	value, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || value == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return 0, false
	}
	return uint(value), true
}

func handlePerformanceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, performancereport.ErrAccessNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "找不到報酬分析或來源回測"})
	case errors.Is(err, performancereport.ErrInProgress):
		c.JSON(http.StatusConflict, gin.H{"error": "相同設定的報酬分析仍在計算，請稍後重試"})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	}
}
