package api

import (
	"errors"
	"net/http"
	"strconv"

	core "quantsaas/internal/parameterresearch"
	"quantsaas/internal/saas/computetask"
	parameterresearchsvc "quantsaas/internal/saas/parameterresearch"
	robustnesssvc "quantsaas/internal/saas/robustness"

	"github.com/gin-gonic/gin"
)

type ParameterResearchHandler struct{ service *parameterresearchsvc.Service }

func NewParameterResearchHandler(service *parameterresearchsvc.Service) *ParameterResearchHandler {
	return &ParameterResearchHandler{service: service}
}

func (h *ParameterResearchHandler) CreateConfiguration(c *gin.Context) {
	var req parameterresearchsvc.CreateConfigurationRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "研究設定格式無效"})
		return
	}
	result, err := h.service.CreateConfiguration(c.Request.Context(), currentUserID(c), req)
	h.respond(c, http.StatusCreated, result, err)
}
func (h *ParameterResearchHandler) DynamicSpace(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.DynamicSpace(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) ListConfigurations(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	result, err := h.service.ListConfigurations(c.Request.Context(), currentUserID(c), limit)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) GetConfiguration(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.GetConfiguration(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) ListRuns(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.ListRuns(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) ArchiveConfiguration(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	err := h.service.ArchiveConfiguration(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, gin.H{"status": "archived"}, err)
}
func (h *ParameterResearchHandler) PlanRun(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req parameterresearchsvc.RunPlanRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "計畫格式無效"})
		return
	}
	result, err := h.service.PlanInitialRun(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) StartRun(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req parameterresearchsvc.StartRunRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "啟動格式無效"})
		return
	}
	result, err := h.service.StartRun(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, http.StatusCreated, result, err)
}
func (h *ParameterResearchHandler) GetRun(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.GetRun(c.Request.Context(), currentUserID(c), id, c.DefaultQuery("include_points", "true") != "false")
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) ListPoints(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "100"))
	result, err := h.service.ListPoints(c.Request.Context(), currentUserID(c), id, page, pageSize, c.Query("status"))
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) GetLandscape(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "2000"))
	result, err := h.service.GetLandscape(c.Request.Context(), currentUserID(c), id, c.Query("axis_x"), c.Query("axis_y"), c.DefaultQuery("metric", "performance_drawdown"), limit)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) PlanStage(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req parameterresearchsvc.StagePlanRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "階段計畫格式無效"})
		return
	}
	result, err := h.service.PlanNextStage(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) StartStage(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req parameterresearchsvc.StartStageRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "階段啟動格式無效"})
		return
	}
	result, err := h.service.StartStage(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, http.StatusCreated, result, err)
}
func (h *ParameterResearchHandler) PauseRun(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	err := h.service.PauseRun(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, gin.H{"status": "paused"}, err)
}
func (h *ParameterResearchHandler) CancelRun(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	err := h.service.CancelRun(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, gin.H{"status": "cancelled"}, err)
}
func (h *ParameterResearchHandler) Analyze(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req parameterresearchsvc.AnalysisRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "分析格式無效"})
		return
	}
	result, err := h.service.AnalyzeRun(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, http.StatusCreated, result, err)
}
func (h *ParameterResearchHandler) DeriveCandidates(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.DeriveCandidates(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusCreated, result, err)
}
func (h *ParameterResearchHandler) ManualCandidate(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.CreateManualCandidate(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusCreated, result, err)
}
func (h *ParameterResearchHandler) ListCandidates(c *gin.Context) {
	configurationID, _ := strconv.ParseUint(c.Query("configuration_id"), 10, 64)
	result, err := h.service.ListCandidates(c.Request.Context(), currentUserID(c), uint(configurationID))
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) GetCandidate(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.GetCandidate(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) CandidateComparison(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.CandidateComparison(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) CandidateComparisonBlock(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.CandidateComparisonBlock(c.Request.Context(), currentUserID(c), id, c.Param("blockID"), c.Query("content_hash"))
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) ArchiveCandidate(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.ArchiveCandidate(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) ExportCandidate(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.ExportCandidate(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) PromoteCandidate(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.PromoteCandidate(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) UpdateAnalysisLink(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req parameterresearchsvc.UpdateAnalysisLinkRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "分析狀態格式無效"})
		return
	}
	result, err := h.service.UpdateAnalysisLink(c.Request.Context(), currentUserID(c), id, c.Param("kind"), req)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) PlanSurrogate(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req parameterresearchsvc.SurrogatePlanRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "代理計畫格式無效"})
		return
	}
	result, err := h.service.PlanSurrogate(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) ListSurrogates(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.ListSurrogates(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) StartSurrogate(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req parameterresearchsvc.StartSurrogateRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "代理啟動格式無效"})
		return
	}
	result, err := h.service.StartSurrogate(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, http.StatusCreated, result, err)
}
func (h *ParameterResearchHandler) GetSurrogate(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.GetSurrogate(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) CreateProposals(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req parameterresearchsvc.ProposalRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "代理提案格式無效"})
		return
	}
	result, err := h.service.CreateProposals(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, http.StatusCreated, result, err)
}
func (h *ParameterResearchHandler) ListProposals(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.ListProposals(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) CreateSeries(c *gin.Context) {
	var req parameterresearchsvc.CreateSeriesRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "研究系列格式無效"})
		return
	}
	result, err := h.service.CreateSeries(c.Request.Context(), currentUserID(c), req)
	h.respond(c, http.StatusCreated, result, err)
}
func (h *ParameterResearchHandler) GetSeries(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.GetSeries(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) AnalysisComparison(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.AnalysisComparison(c.Request.Context(), currentUserID(c), id)
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) AnalysisComparisonBlock(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.AnalysisComparisonBlock(c.Request.Context(), currentUserID(c), id, c.Param("blockID"), c.Query("content_hash"))
	h.respond(c, http.StatusOK, result, err)
}
func (h *ParameterResearchHandler) SeriesComparison(c *gin.Context) {
	seriesID, ok := parseIDParam(c)
	if !ok {
		return
	}
	snapshotID, err := strconv.ParseUint(c.Param("snapshotID"), 10, 64)
	if err != nil || snapshotID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "comparison snapshot id 無效"})
		return
	}
	result, serviceErr := h.service.SeriesComparison(c.Request.Context(), currentUserID(c), seriesID, uint(snapshotID))
	h.respond(c, http.StatusOK, result, serviceErr)
}
func (h *ParameterResearchHandler) SeriesComparisonBlock(c *gin.Context) {
	seriesID, ok := parseIDParam(c)
	if !ok {
		return
	}
	snapshotID, err := strconv.ParseUint(c.Param("snapshotID"), 10, 64)
	if err != nil || snapshotID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "comparison snapshot id 無效"})
		return
	}
	result, serviceErr := h.service.SeriesComparisonBlock(c.Request.Context(), currentUserID(c), seriesID, uint(snapshotID), c.Param("blockID"), c.Query("content_hash"))
	h.respond(c, http.StatusOK, result, serviceErr)
}

func (h *ParameterResearchHandler) respond(c *gin.Context, success int, value any, err error) {
	if err == nil {
		c.JSON(success, value)
		return
	}
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, parameterresearchsvc.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, parameterresearchsvc.ErrInvalidRequest), errors.Is(err, core.ErrInvalidPlan), errors.Is(err, core.ErrSurrogateIneligible), errors.Is(err, robustnesssvc.ErrStudyNotReady):
		status = http.StatusBadRequest
	case errors.Is(err, parameterresearchsvc.ErrPlanStale), errors.Is(err, computetask.ErrSoftLimitConfirm):
		status = http.StatusConflict
	case errors.Is(err, computetask.ErrHardLimitExceeded):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, computetask.ErrServiceUnavailable):
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
