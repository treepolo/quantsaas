package api

import (
	"errors"
	"net/http"
	"strconv"

	perturbationsvc "quantsaas/internal/saas/perturbation"

	"github.com/gin-gonic/gin"
)

type PerturbationHandler struct{ service *perturbationsvc.Service }

func NewPerturbationHandler(service *perturbationsvc.Service) *PerturbationHandler {
	return &PerturbationHandler{service: service}
}

func (h *PerturbationHandler) Sources(c *gin.Context) {
	items, err := h.service.Sources(c.Request.Context(), currentUserID(c))
	h.respond(c, items, err, http.StatusOK)
}
func (h *PerturbationHandler) PlanGroup(c *gin.Context) {
	var req perturbationsvc.GroupPlanRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "來源設定格式錯誤"})
		return
	}
	result, err := h.service.PlanGroup(c.Request.Context(), currentUserID(c), req)
	h.respond(c, result, err, http.StatusOK)
}
func (h *PerturbationHandler) CreateGroup(c *gin.Context) {
	var req perturbationsvc.CreateGroupRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "群組設定格式錯誤"})
		return
	}
	result, err := h.service.CreateGroup(c.Request.Context(), currentUserID(c), req)
	h.respond(c, result, err, http.StatusCreated)
}
func (h *PerturbationHandler) ListGroups(c *gin.Context) {
	items, err := h.service.ListGroups(c.Request.Context(), currentUserID(c), c.Query("include_archived") == "true")
	limit, offset := perturbationPagination(c)
	h.respond(c, gin.H{"items": pagePerturbationItems(items, limit, offset), "total": len(items), "limit": limit, "offset": offset}, err, http.StatusOK)
}
func (h *PerturbationHandler) GetGroup(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	result, err := h.service.GetGroup(c.Request.Context(), currentUserID(c), id, true)
	h.respond(c, result, err, http.StatusOK)
}
func (h *PerturbationHandler) UpdateGroup(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	var req perturbationsvc.MetadataRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "metadata 格式錯誤"})
		return
	}
	result, err := h.service.UpdateGroupMetadata(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, result, err, http.StatusOK)
}
func (h *PerturbationHandler) ArchiveGroup(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	err := h.service.ArchiveGroup(c.Request.Context(), currentUserID(c), id)
	h.respond(c, gin.H{"archived": err == nil}, err, http.StatusOK)
}
func (h *PerturbationHandler) PlanVariants(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	var req perturbationsvc.VariantPlanRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "版本設定格式錯誤"})
		return
	}
	result, err := h.service.PlanVariants(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, result, err, http.StatusOK)
}
func (h *PerturbationHandler) StartVariants(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	var req perturbationsvc.StartVariantsRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "版本設定格式錯誤"})
		return
	}
	result, err := h.service.StartVariants(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, result, err, http.StatusAccepted)
}
func (h *PerturbationHandler) ListVariants(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	items, err := h.service.ListVariants(c.Request.Context(), currentUserID(c), id, c.Query("include_archived") == "true")
	limit, offset := perturbationPagination(c)
	h.respond(c, gin.H{"items": pagePerturbationItems(items, limit, offset), "total": len(items), "limit": limit, "offset": offset}, err, http.StatusOK)
}
func (h *PerturbationHandler) GetVariant(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	result, err := h.service.GetVariant(c.Request.Context(), currentUserID(c), id)
	h.respond(c, result, err, http.StatusOK)
}
func (h *PerturbationHandler) VerifyVariant(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	result, err := h.service.VerifyVariant(c.Request.Context(), currentUserID(c), id)
	h.respond(c, result, err, http.StatusOK)
}
func (h *PerturbationHandler) PlanTest(c *gin.Context) {
	var req perturbationsvc.TestPlanRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "測試設定格式錯誤"})
		return
	}
	result, err := h.service.PlanTest(c.Request.Context(), currentUserID(c), req)
	h.respond(c, result, err, http.StatusOK)
}
func (h *PerturbationHandler) CreateTest(c *gin.Context) {
	var req perturbationsvc.StartTestRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "測試設定格式錯誤"})
		return
	}
	result, err := h.service.CreateTest(c.Request.Context(), currentUserID(c), req)
	h.respond(c, result, err, http.StatusCreated)
}
func (h *PerturbationHandler) ListTests(c *gin.Context) {
	items, err := h.service.ListTests(c.Request.Context(), currentUserID(c), c.Query("include_archived") == "true")
	limit, offset := perturbationPagination(c)
	h.respond(c, gin.H{"items": pagePerturbationItems(items, limit, offset), "total": len(items), "limit": limit, "offset": offset}, err, http.StatusOK)
}
func (h *PerturbationHandler) GetTest(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	result, err := h.service.GetTest(c.Request.Context(), currentUserID(c), id, true)
	h.respond(c, result, err, http.StatusOK)
}
func (h *PerturbationHandler) UpdateTest(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	var req perturbationsvc.MetadataRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "metadata 格式錯誤"})
		return
	}
	result, err := h.service.UpdateTestMetadata(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, result, err, http.StatusOK)
}
func (h *PerturbationHandler) ArchiveTest(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	err := h.service.ArchiveTest(c.Request.Context(), currentUserID(c), id)
	h.respond(c, gin.H{"archived": err == nil}, err, http.StatusOK)
}
func (h *PerturbationHandler) PlanBatch(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	var req perturbationsvc.BatchPlanRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "批次設定格式錯誤"})
		return
	}
	result, err := h.service.PlanBatch(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, result, err, http.StatusOK)
}
func (h *PerturbationHandler) StartBatch(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	var req perturbationsvc.StartBatchRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "批次設定格式錯誤"})
		return
	}
	result, err := h.service.StartBatch(c.Request.Context(), currentUserID(c), id, req)
	h.respond(c, result, err, http.StatusAccepted)
}
func (h *PerturbationHandler) Runs(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	limit, offset := perturbationPagination(c)
	items, err := h.service.Runs(c.Request.Context(), currentUserID(c), id, limit, offset)
	h.respond(c, gin.H{"items": items, "limit": limit, "offset": offset}, err, http.StatusOK)
}
func (h *PerturbationHandler) Snapshots(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	items, err := h.service.AnalysisSnapshots(c.Request.Context(), currentUserID(c), id)
	limit, offset := perturbationPagination(c)
	h.respond(c, gin.H{"items": pagePerturbationItems(items, limit, offset), "total": len(items), "limit": limit, "offset": offset}, err, http.StatusOK)
}
func (h *PerturbationHandler) Snapshot(c *gin.Context) {
	id, ok := parseUintPath(c, "id")
	if !ok {
		return
	}
	snapshotID, ok := parseUintPath(c, "snapshot_id")
	if !ok {
		return
	}
	result, err := h.service.GetAnalysisSnapshot(c.Request.Context(), currentUserID(c), id, snapshotID)
	h.respond(c, result, err, http.StatusOK)
}

func (h *PerturbationHandler) respond(c *gin.Context, value any, err error, success int) {
	if err == nil {
		c.JSON(success, value)
		return
	}
	status := http.StatusBadRequest
	code := "invalid_request"
	switch {
	case errors.Is(err, perturbationsvc.ErrNotFound):
		status = http.StatusNotFound
		code = "not_found"
	case errors.Is(err, perturbationsvc.ErrStalePlan):
		status = http.StatusConflict
		code = "stale_plan"
	case errors.Is(err, perturbationsvc.ErrRecipeConflict):
		status = http.StatusConflict
		code = "recipe_conflict"
	case errors.Is(err, perturbationsvc.ErrMissingVariant):
		status = http.StatusConflict
		code = "missing_variant"
	case errors.Is(err, perturbationsvc.ErrContentMismatch):
		status = http.StatusConflict
		code = "content_hash_mismatch"
	case errors.Is(err, perturbationsvc.ErrUnsupportedSource):
		code = "unsupported_source_kind"
	case errors.Is(err, perturbationsvc.ErrInvalidSeed):
		code = "invalid_seed"
	case errors.Is(err, perturbationsvc.ErrInvalidAlpha):
		code = "invalid_alpha"
	case errors.Is(err, perturbationsvc.ErrIncompatibleSubject):
		code = "incompatible_test_subject"
	}
	c.JSON(status, gin.H{"code": code, "error": err.Error()})
}

func perturbationPagination(c *gin.Context) (int, int) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func pagePerturbationItems[T any](items []T, limit, offset int) []T {
	if offset >= len(items) {
		return []T{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
