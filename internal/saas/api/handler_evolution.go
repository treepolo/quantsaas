package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/saas/config"
	"quantsaas/internal/saas/epoch"
	"quantsaas/internal/saas/ga"
	"quantsaas/internal/saas/marketdata"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type EvolutionHandler struct {
	appRole string
	db      *gorm.DB
	redis   *saasstore.RedisClient
	service *epoch.Service
}

func NewEvolutionHandler(appRole string, db *gorm.DB, redis *saasstore.RedisClient, service *epoch.Service) *EvolutionHandler {
	return &EvolutionHandler{appRole: appRole, db: db, redis: redis, service: service}
}

func (h *EvolutionHandler) CreateTask(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "此功能僅允許 lab/dev 模式"})
		return
	}
	var req epoch.CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	task, err := h.service.CreateAndRunTask(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, evolutionTaskResponse(*task))
}

func (h *EvolutionHandler) EstimateCompute(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "此功能僅允許 lab/dev 模式"})
		return
	}
	var req epoch.CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	estimate, err := h.service.EstimateCompute(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, estimate)
}

type parameterGridPointResponse struct {
	Value float64 `json:"value"`
	Count int64   `json:"count"`
}

type parameterGridAxisResponse struct {
	Key    string                       `json:"key"`
	Label  string                       `json:"label"`
	Status string                       `json:"status"`
	Min    float64                      `json:"min"`
	Max    float64                      `json:"max"`
	Points []parameterGridPointResponse `json:"points"`
}

// GetParameterGrid returns the compact, server-aggregated search coverage for
// one task. Its response is bounded by the legal 0.05 lattice, not by the
// number of candidates, so rendering it never requires a raw candidate dump.
func (h *EvolutionHandler) GetParameterGrid(c *gin.Context) {
	taskID, err := strconv.ParseUint(c.Param("taskID"), 10, 64)
	if err != nil || taskID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "無效的任務編號"})
		return
	}
	var task saasstore.EvolutionTask
	if err := h.db.WithContext(c.Request.Context()).First(&task, uint(taskID)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "找不到任務"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var observation saasstore.GeneObservation
	_ = h.db.WithContext(c.Request.Context()).Where("task_id = ?", task.ID).Order("id DESC").First(&observation).Error
	var resultIdentity struct {
		SearchHash string `json:"search_hash"`
	}
	_ = json.Unmarshal([]byte(task.Result), &resultIdentity)
	query := h.db.WithContext(c.Request.Context()).Model(&saasstore.GeneParameterGridPoint{})
	if observation.SearchHash != "" {
		query = query.Where("search_hash = ?", observation.SearchHash)
	} else if resultIdentity.SearchHash != "" {
		query = query.Where("search_hash = ?", resultIdentity.SearchHash)
	} else {
		query = query.Where("task_id = ?", task.ID)
	}
	var rows []saasstore.GeneParameterGridPoint
	if err := query.
		Select("MIN(id) AS id, MIN(task_id) AS task_id, search_hash, parameter_key, grid_step, MAX(grid_value) AS grid_value, SUM(count) AS count").
		Group("search_hash, parameter_key, grid_step").
		Order("parameter_key ASC, grid_step ASC").
		Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	pointsByKey := map[string][]parameterGridPointResponse{}
	for _, row := range rows {
		value := row.GridValue
		if value == 0 && !ga.IsMarketThresholdGridKey(row.ParameterKey) {
			value = ga.GridStoredValue(row.ParameterKey, row.GridStep)
		}
		pointsByKey[row.ParameterKey] = append(pointsByKey[row.ParameterKey], parameterGridPointResponse{
			Value: value,
			Count: row.Count,
		})
	}
	var gridConfig struct {
		PositionStructure         string  `json:"position_structure"`
		MarketRegionEnabled       bool    `json:"market_region_enabled"`
		MarketRegionMaxThresholds int     `json:"market_region_max_thresholds"`
		EvolveRebalanceThreshold  bool    `json:"evolve_rebalance_threshold"`
		EvolveForceFullThreshold  bool    `json:"evolve_force_full_threshold"`
		EvolveForceEmptyThreshold bool    `json:"evolve_force_empty_threshold"`
		EvolveGamma               bool    `json:"evolve_gamma"`
		EnableWMean               *bool   `json:"enable_w_mean"`
		EnableWMomentum           *bool   `json:"enable_w_momentum"`
		EnableWBreakout           *bool   `json:"enable_w_breakout"`
		FeeRate                   float64 `json:"fee_rate"`
		SpreadRate                float64 `json:"spread_rate"`
	}
	_ = json.Unmarshal([]byte(task.Config), &gridConfig)
	type axisDefinition struct{ key, label, status string }
	statusFor := func(key string) string {
		if gridConfig.MarketRegionEnabled {
			switch key {
			case "micro_reserve_pct", "beta", "rebalance_threshold":
				return "停用"
			}
		}
		if key == "dust_usd" && gridConfig.FeeRate == 0 && gridConfig.SpreadRate == 0 {
			return "停用"
		}
		if gridConfig.PositionStructure == sigmoiddca.PositionStructureFloatingOnly {
			switch key {
			case "micro_reserve_pct", "rebalance_threshold", "macro_bear_multiplier", "macro_bull_multiplier", "extra_deploy_pct", "soft_release_months", "soft_release_pct", "hard_release_max_pct", "wedge_delta_threshold", "wedge_vol_ratio_threshold":
				return "停用"
			}
		}
		switch key {
		case "rebalance_threshold":
			if !gridConfig.EvolveRebalanceThreshold {
				return "固定中性化"
			}
		case "gamma":
			if !gridConfig.MarketRegionEnabled && !gridConfig.EvolveGamma {
				return "固定中性化"
			}
		case "force_full_threshold":
			if !gridConfig.MarketRegionEnabled && !gridConfig.EvolveForceFullThreshold {
				return "固定中性化"
			}
		case "force_empty_threshold":
			if !gridConfig.MarketRegionEnabled && !gridConfig.EvolveForceEmptyThreshold {
				return "固定中性化"
			}
		case "w_mean":
			if !gridConfig.MarketRegionEnabled && gridConfig.EnableWMean != nil && !*gridConfig.EnableWMean {
				return "固定中性化"
			}
		case "w_momentum":
			if !gridConfig.MarketRegionEnabled && gridConfig.EnableWMomentum != nil && !*gridConfig.EnableWMomentum {
				return "固定中性化"
			}
		case "w_breakout":
			if !gridConfig.MarketRegionEnabled && gridConfig.EnableWBreakout != nil && !*gridConfig.EnableWBreakout {
				return "固定中性化"
			}
		}
		return "演化中"
	}
	definitions := []axisDefinition{
		{"micro_reserve_pct", "微觀保留比例", ""}, {"beta", "Beta", ""}, {"gamma", "Gamma", ""},
		{"w_mean", "均值權重", ""}, {"w_momentum", "動能權重", ""}, {"w_breakout", "突破權重", ""},
		{"dust_usd", "最小交易金額", ""}, {"rebalance_threshold", "再平衡門檻", ""},
		{"force_full_threshold", "強制滿倉門檻", ""}, {"force_empty_threshold", "強制空倉門檻", ""},
		{"wedge_delta_threshold", "舊微觀最小單權重觸發", ""}, {"wedge_vol_ratio_threshold", "舊微觀最小單波動觸發", ""},
		{"macro_bear_multiplier", "偏空倍率", ""}, {"macro_bull_multiplier", "偏多倍率", ""},
		{"extra_deploy_pct", "額外投入比例", ""}, {"soft_release_months", "緩釋月數", ""},
		{"soft_release_pct", "緩釋比例", ""}, {"hard_release_max_pct", "硬釋放上限", ""},
	}
	if gridConfig.MarketRegionEnabled {
		for _, id := range ga.MarketRegionFeatureIDs {
			definitions = append(definitions, axisDefinition{"market_region." + id + ".window", id + " 窗口", "演化中"})
			for index := 1; index <= gridConfig.MarketRegionMaxThresholds; index++ {
				definitions = append(definitions, axisDefinition{fmt.Sprintf("market_region.%s.threshold_%d", id, index), fmt.Sprintf("%s 判定值 %d", id, index), "演化中"})
			}
		}
	}
	defined := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		defined[definition.key] = true
	}
	for key := range pointsByKey {
		if defined[key] {
			continue
		}
		label := key
		if strings.HasPrefix(key, "market_region.state_") {
			label = strings.ReplaceAll(strings.TrimPrefix(key, "market_region."), ".", " · ")
		}
		definitions = append(definitions, axisDefinition{key, label, "演化中"})
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].key < definitions[j].key })
	axisEvolves := func(key string) bool {
		if strings.HasPrefix(key, "market_region.") {
			return true
		}
		if gridConfig.MarketRegionEnabled {
			switch key {
			case "gamma", "w_mean", "w_momentum", "w_breakout", "force_full_threshold", "force_empty_threshold":
				return true
			default:
				return false
			}
		}
		if key == "dust_usd" && gridConfig.FeeRate == 0 && gridConfig.SpreadRate == 0 {
			return false
		}
		if gridConfig.PositionStructure == sigmoiddca.PositionStructureFloatingOnly {
			switch key {
			case "micro_reserve_pct", "rebalance_threshold", "macro_bear_multiplier", "macro_bull_multiplier", "extra_deploy_pct", "soft_release_months", "soft_release_pct", "hard_release_max_pct", "wedge_delta_threshold", "wedge_vol_ratio_threshold":
				return false
			}
		}
		switch key {
		case "rebalance_threshold":
			return gridConfig.EvolveRebalanceThreshold
		case "gamma":
			return gridConfig.EvolveGamma
		case "force_full_threshold":
			return gridConfig.EvolveForceFullThreshold
		case "force_empty_threshold":
			return gridConfig.EvolveForceEmptyThreshold
		case "w_mean":
			return gridConfig.EnableWMean == nil || *gridConfig.EnableWMean
		case "w_momentum":
			return gridConfig.EnableWMomentum == nil || *gridConfig.EnableWMomentum
		case "w_breakout":
			return gridConfig.EnableWBreakout == nil || *gridConfig.EnableWBreakout
		}
		return true
	}
	axes := make([]parameterGridAxisResponse, 0, len(definitions))
	for _, definition := range definitions {
		boundKey := definition.key
		if strings.HasPrefix(boundKey, "market_region.state_") {
			if separator := strings.LastIndex(boundKey, "."); separator >= 0 {
				boundKey = boundKey[separator+1:]
			}
		}
		bound := quant.HardBounds[boundKey]
		points := pointsByKey[definition.key]
		if points == nil {
			points = []parameterGridPointResponse{}
		}
		minimum, maximum := bound.Min, bound.Max
		if strings.HasPrefix(definition.key, "market_region.") {
			if len(points) > 0 {
				minimum, maximum = points[0].Value, points[len(points)-1].Value
			}
			if strings.HasSuffix(definition.key, ".window") {
				minimum, maximum = 2, math.Max(2, maximum)
			}
		}
		status := definition.status
		if status == "" {
			status = statusFor(definition.key)
		}
		if !axisEvolves(definition.key) {
			status = "停用"
		}
		axes = append(axes, parameterGridAxisResponse{Key: definition.key, Label: definition.label, Status: status, Min: minimum, Max: maximum, Points: points})
	}
	c.JSON(http.StatusOK, gin.H{"task_id": task.ID, "axes": axes, "grid_point_count": len(rows)})
}

func (h *EvolutionHandler) ListTasks(c *gin.Context) {
	if false && h.service != nil && h.service.CurrentTask() == nil {
		now := time.Now().UTC()
		if err := h.db.WithContext(c.Request.Context()).
			Model(&saasstore.EvolutionTask{}).
			Where("status = ?", epoch.TaskStatusRunning).
			Updates(map[string]any{
				"status":        epoch.TaskStatusFailed,
				"error_message": "服務曾重啟或任務中斷，請重新建立任務",
				"finished_at":   &now,
			}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	var tasks []saasstore.EvolutionTask
	if err := h.db.WithContext(c.Request.Context()).
		Order("created_at DESC").
		Limit(50).
		Find(&tasks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var latestChallenger *saasstore.GeneRecord
	var challenger saasstore.GeneRecord
	if err := h.db.WithContext(c.Request.Context()).
		Where("role = ?", saasstore.GeneRoleChallenger).
		Order("created_at DESC").
		First(&challenger).Error; err == nil {
		latestChallenger = &challenger
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var championRecord *saasstore.GeneRecord
	var champion saasstore.GeneRecord
	if err := h.db.WithContext(c.Request.Context()).
		Where("role = ?", saasstore.GeneRoleChampion).
		Order("activated_at DESC NULLS LAST, created_at DESC").
		First(&champion).Error; err == nil {
		championRecord = &champion
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var current any
	if h.service != nil {
		if task := h.service.CurrentTask(); task != nil {
			current = evolutionTaskResponse(*task)
		}
	}
	taskResponses := make([]gin.H, 0, len(tasks))
	for _, task := range tasks {
		taskResponses = append(taskResponses, evolutionTaskResponse(task))
	}

	c.JSON(http.StatusOK, gin.H{
		"current_task":      current,
		"running":           current != nil,
		"tasks":             taskResponses,
		"latest_challenger": genePtrResponse(latestChallenger),
		"champion":          genePtrResponse(championRecord),
		"window_summaries":  activeWindowSummary(latestChallenger, championRecord),
	})
}

func (h *EvolutionHandler) GetTrace(c *gin.Context) {
	if h.service == nil {
		c.JSON(http.StatusOK, gin.H{"task_id": 0, "mode": ga.TraceModeOff, "events": []any{}})
		return
	}
	taskID, ok := parseUintParam(c, "taskID")
	if !ok {
		return
	}
	afterID, _ := strconv.ParseUint(c.DefaultQuery("after_id", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "500"))
	if limit <= 0 || limit > epoch.TraceBufferLimit {
		limit = 500
	}
	c.JSON(http.StatusOK, h.service.TraceSnapshot(uint(taskID), afterID, limit))
}

func (h *EvolutionHandler) SetTraceMode(c *gin.Context) {
	if h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "trace service unavailable"})
		return
	}
	taskID, ok := parseUintParam(c, "taskID")
	if !ok {
		return
	}
	var req struct {
		TraceMode ga.TraceMode `json:"trace_mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	mode := h.service.SetTraceMode(uint(taskID), req.TraceMode)
	c.JSON(http.StatusOK, gin.H{"task_id": taskID, "mode": mode})
}

func (h *EvolutionHandler) CancelTask(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "此功能僅允許 lab/dev 模式"})
		return
	}
	if h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "evolution service unavailable"})
		return
	}
	taskID, ok := parseUintParam(c, "taskID")
	if !ok {
		return
	}
	if err := h.service.CancelTask(c.Request.Context(), uint(taskID)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": epoch.TaskStatusCancelled, "task_id": taskID})
}

func (h *EvolutionHandler) Promote(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "此功能僅允許 lab/dev 模式"})
		return
	}
	id, err := strconv.ParseUint(c.Param("taskID"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var promoted saasstore.GeneRecord
	err = h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND role IN ?", id, []string{saasstore.GeneRoleChallenger, saasstore.GeneRoleRetired}).First(&promoted).Error; err != nil {
			return err
		}
		if err := tx.Model(&saasstore.GeneRecord{}).
			Where("strategy_id = ? AND instrument_id = ? AND data_source = ? AND interval = ? AND execution_mode = ? AND role = ?",
				promoted.StrategyID, promoted.InstrumentID, promoted.DataSource, promoted.Interval, promoted.ExecutionMode, saasstore.GeneRoleChampion).
			Update("role", saasstore.GeneRoleRetired).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		return tx.Model(&promoted).Updates(map[string]any{
			"role":         saasstore.GeneRoleChampion,
			"activated_at": &now,
		}).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.redis != nil {
		_ = h.redis.Del(context.Background(), championCacheKey(promoted.StrategyID))
	}
	c.JSON(http.StatusOK, gin.H{"status": "promoted", "genome": geneResponse(promoted)})
}

func (h *EvolutionHandler) UpdateGenome(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "此功能僅允許 lab/dev 模式"})
		return
	}
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var req struct {
		Name  *string  `json:"name"`
		Notes *string  `json:"notes"`
		Tags  []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = strings.TrimSpace(*req.Name)
	}
	if req.Notes != nil {
		updates["notes"] = strings.TrimSpace(*req.Notes)
	}
	if req.Tags != nil {
		raw, err := json.Marshal(cleanTags(req.Tags))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		updates["tags"] = saasstore.JSONB(raw)
	}
	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "沒有可更新的欄位"})
		return
	}
	var record saasstore.GeneRecord
	if err := h.db.WithContext(c.Request.Context()).First(&record, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "找不到參數"})
		return
	}
	if err := h.db.WithContext(c.Request.Context()).Model(&record).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.db.WithContext(c.Request.Context()).First(&record, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.redis != nil && record.Role == saasstore.GeneRoleChampion {
		_ = h.redis.Del(context.Background(), championCacheKey(record.StrategyID))
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated", "genome": geneResponse(record)})
}

func (h *EvolutionHandler) DeleteGenome(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "此功能僅允許 lab/dev 模式"})
		return
	}
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	var record saasstore.GeneRecord
	if err := h.db.WithContext(c.Request.Context()).First(&record, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "找不到參數"})
		return
	}
	if err := h.db.WithContext(c.Request.Context()).Delete(&record).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.redis != nil && record.Role == saasstore.GeneRoleChampion {
		_ = h.redis.Del(context.Background(), championCacheKey(record.StrategyID))
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted", "id": id})
}

func (h *EvolutionHandler) GetChampion(c *gin.Context) {
	strategyID := c.Query("strategy_id")
	if strategyID == "" {
		strategyID = sigmoiddca.StrategyID
	}
	key := championCacheKey(strategyID)
	if h.redis != nil {
		if cached, err := h.redis.Get(c.Request.Context(), key); err == nil && cached != "" {
			c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(cached))
			return
		}
	}

	var record saasstore.GeneRecord
	if err := h.db.WithContext(c.Request.Context()).
		Scopes(geneScopeFromQuery(c)).
		Where("strategy_id = ? AND role = ?", strategyID, saasstore.GeneRoleChampion).
		Order("activated_at DESC NULLS LAST, created_at DESC").
		First(&record).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "champion not found"})
		return
	}
	raw, _ := json.Marshal(record)
	if h.redis != nil {
		_ = h.redis.Set(c.Request.Context(), key, string(raw), 10*time.Minute)
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
}

func (h *EvolutionHandler) canUseLab() bool {
	return h.appRole == config.AppRoleLab || h.appRole == config.AppRoleDev
}

func championCacheKey(strategyID string) string {
	return "champion:" + strategyID
}

func evolutionTaskResponse(task saasstore.EvolutionTask) gin.H {
	var cfg struct {
		Pair                      string                       `json:"pair"`
		ResearchDatasetID         uint                         `json:"research_dataset_id"`
		InstrumentID              string                       `json:"instrument_id"`
		DataSource                string                       `json:"data_source"`
		ExecutionMode             string                       `json:"execution_mode"`
		TrainStartMs              int64                        `json:"train_start_ms"`
		TrainEndMs                int64                        `json:"train_end_ms"`
		Interval                  string                       `json:"interval"`
		PopSize                   int                          `json:"pop_size"`
		MaxGenerations            int                          `json:"max_generations"`
		SearchAlgorithm           string                       `json:"search_algorithm"`
		LayeredLocalPercent       int                          `json:"layered_local_percent"`
		InitialCapital            float64                      `json:"initial_capital"`
		MonthlyDCA                float64                      `json:"monthly_dca"`
		EvolveRebalanceThreshold  bool                         `json:"evolve_rebalance_threshold"`
		EvolveForceFullThreshold  bool                         `json:"evolve_force_full_threshold"`
		EvolveForceEmptyThreshold bool                         `json:"evolve_force_empty_threshold"`
		EvolveGamma               bool                         `json:"evolve_gamma"`
		EnableWMean               bool                         `json:"enable_w_mean"`
		EnableWMomentum           bool                         `json:"enable_w_momentum"`
		EnableWBreakout           bool                         `json:"enable_w_breakout"`
		PositionStructure         string                       `json:"position_structure"`
		TradePenalty              float64                      `json:"trade_penalty"`
		FeeRate                   float64                      `json:"fee_rate"`
		SpreadRate                float64                      `json:"spread_rate"`
		LongTermFilterEnabled     bool                         `json:"long_term_filter_enabled"`
		LongTermFilterMonths      int                          `json:"long_term_filter_months"`
		SpawnMode                 string                       `json:"spawn_mode"`
		TestMode                  bool                         `json:"test_mode"`
		TraceMode                 string                       `json:"trace_mode"`
		ComputeMonitorEnabled     bool                         `json:"compute_monitor_enabled"`
		ContinuousMode            string                       `json:"continuous_mode"`
		ContinuousIterations      int                          `json:"continuous_iterations"`
		ContinuousUnlimited       bool                         `json:"continuous_unlimited"`
		StandardStartMs           int64                        `json:"standard_start_ms"`
		StandardEndMs             int64                        `json:"standard_end_ms"`
		SeedGeneID                uint                         `json:"seed_gene_id"`
		FixedParamKeys            []string                     `json:"fixed_param_keys"`
		MarketRegionEnabled       bool                         `json:"market_region_enabled"`
		MarketRegionMaxThresholds int                          `json:"market_region_max_thresholds"`
		MultiMarketEnabled        bool                         `json:"multi_market_enabled"`
		MultiMarketInstrumentIDs  []string                     `json:"multi_market_instrument_ids"`
		MultiMarketSelections     []epoch.MultiMarketSelection `json:"multi_market_selections"`
	}
	_ = json.Unmarshal([]byte(task.Config), &cfg)
	currentGeneration := 0
	if cfg.MaxGenerations > 0 {
		currentGeneration = int(math.Round(task.Progress * float64(cfg.MaxGenerations)))
	}
	var result struct {
		CurrentGeneration      int                     `json:"current_generation"`
		BestScore              float64                 `json:"best_score"`
		MaxDrawdown            float64                 `json:"max_drawdown"`
		MutationProbability    float64                 `json:"mutation_probability"`
		MutationScale          float64                 `json:"mutation_scale"`
		UpdatedAt              string                  `json:"updated_at"`
		WindowScores           []quant.CrucibleResult  `json:"window_scores"`
		MarketPerformance      []ga.MarketPerformance  `json:"market_performance"`
		BestParamPack          json.RawMessage         `json:"best_param_pack"`
		GeneRecordID           uint                    `json:"gene_record_id"`
		ContinuousMode         string                  `json:"continuous_mode"`
		CurrentIteration       int                     `json:"current_iteration"`
		ContinuousIterations   int                     `json:"continuous_iterations"`
		ContinuousUnlimited    bool                    `json:"continuous_unlimited"`
		StandardStartMs        int64                   `json:"standard_start_ms"`
		StandardEndMs          int64                   `json:"standard_end_ms"`
		StandardChampionGeneID uint                    `json:"standard_champion_gene_id"`
		StandardChampionScore  float64                 `json:"standard_champion_score"`
		ComputeMonitorEnabled  bool                    `json:"compute_monitor_enabled"`
		ComputedUnits          int64                   `json:"computed_units"`
		PlannedComputeUnits    int64                   `json:"planned_compute_units"`
		UnitsPerIndividual     int64                   `json:"units_per_individual"`
		ComputeUnitsPerSec     float64                 `json:"compute_units_per_sec"`
		ComputeRemainingSec    float64                 `json:"compute_remaining_sec"`
		ComputeStartedAt       string                  `json:"compute_started_at"`
		ComputeUpdatedAt       string                  `json:"compute_updated_at"`
		SearchAxes             []ga.LayeredAxisStatus  `json:"search_axes"`
		SearchStatus           *ga.LayeredSearchStatus `json:"search_status"`
		Fitness                struct {
			ScoreTotal  float64 `json:"ScoreTotal"`
			MaxDrawdown float64 `json:"MaxDrawdown"`
		} `json:"Fitness"`
	}
	_ = json.Unmarshal([]byte(task.Result), &result)
	if result.CurrentGeneration > 0 {
		currentGeneration = result.CurrentGeneration
	}
	bestScore := result.BestScore
	if bestScore == 0 {
		bestScore = result.Fitness.ScoreTotal
	}
	var bestScoreResponse any
	if result.CurrentGeneration > 0 || result.GeneRecordID > 0 || (len(result.BestParamPack) > 0 && string(result.BestParamPack) != "null") {
		bestScoreResponse = bestScore
	}
	maxDrawdown := result.MaxDrawdown
	if maxDrawdown == 0 {
		maxDrawdown = result.Fitness.MaxDrawdown
	}
	continuousMode := firstNonEmpty(result.ContinuousMode, cfg.ContinuousMode)
	continuousIterations := firstNonZeroInt(result.ContinuousIterations, cfg.ContinuousIterations)
	continuousUnlimited := result.ContinuousUnlimited || cfg.ContinuousUnlimited
	batchesPerIteration := cfg.MaxGenerations + 1
	totalEvaluations := currentGeneration * cfg.PopSize
	totalPlannedEvaluations := cfg.PopSize * batchesPerIteration
	if task.Status == epoch.TaskStatusDone {
		totalEvaluations = totalPlannedEvaluations
	}
	if continuousMode != "" {
		if result.CurrentIteration > 0 {
			totalEvaluations = ((result.CurrentIteration-1)*batchesPerIteration + currentGeneration) * cfg.PopSize
		}
		if continuousUnlimited {
			totalPlannedEvaluations = 0
		} else {
			totalPlannedEvaluations = cfg.PopSize * batchesPerIteration * continuousIterations
			if task.Status == epoch.TaskStatusDone {
				totalEvaluations = totalPlannedEvaluations
			}
		}
	}
	return gin.H{
		"id":                           task.ID,
		"strategy_id":                  task.StrategyID,
		"research_dataset_id":          cfg.ResearchDatasetID,
		"status":                       task.Status,
		"progress":                     task.Progress,
		"current_generation":           currentGeneration,
		"max_generations":              cfg.MaxGenerations,
		"pop_size":                     cfg.PopSize,
		"search_algorithm":             cfg.SearchAlgorithm,
		"layered_local_percent":        cfg.LayeredLocalPercent,
		"pair":                         cfg.Pair,
		"instrument_id":                firstNonEmpty(task.InstrumentID, cfg.InstrumentID),
		"data_source":                  firstNonEmpty(task.DataSource, cfg.DataSource),
		"execution_mode":               firstNonEmpty(task.ExecutionMode, cfg.ExecutionMode),
		"train_start_ms":               firstNonZero(task.TrainStartMs, cfg.TrainStartMs),
		"train_end_ms":                 firstNonZero(task.TrainEndMs, cfg.TrainEndMs),
		"initial_capital":              cfg.InitialCapital,
		"monthly_dca":                  cfg.MonthlyDCA,
		"evolve_rebalance_threshold":   cfg.EvolveRebalanceThreshold,
		"evolve_force_full_threshold":  cfg.EvolveForceFullThreshold,
		"evolve_force_empty_threshold": cfg.EvolveForceEmptyThreshold,
		"evolve_gamma":                 cfg.EvolveGamma,
		"enable_w_mean":                cfg.EnableWMean,
		"enable_w_momentum":            cfg.EnableWMomentum,
		"enable_w_breakout":            cfg.EnableWBreakout,
		"position_structure":           cfg.PositionStructure,
		"trade_penalty":                cfg.TradePenalty,
		"fee_rate":                     cfg.FeeRate,
		"spread_rate":                  cfg.SpreadRate,
		"long_term_filter_enabled":     cfg.LongTermFilterEnabled,
		"long_term_filter_months":      cfg.LongTermFilterMonths,
		"interval":                     cfg.Interval,
		"spawn_mode":                   cfg.SpawnMode,
		"test_mode":                    cfg.TestMode,
		"trace_mode":                   cfg.TraceMode,
		"compute_monitor_enabled":      result.ComputeMonitorEnabled || cfg.ComputeMonitorEnabled,
		"continuous_mode":              continuousMode,
		"current_iteration":            result.CurrentIteration,
		"continuous_iterations":        continuousIterations,
		"continuous_unlimited":         continuousUnlimited,
		"standard_start_ms":            firstNonZero(result.StandardStartMs, cfg.StandardStartMs),
		"standard_end_ms":              firstNonZero(result.StandardEndMs, cfg.StandardEndMs),
		"standard_champion_gene_id":    result.StandardChampionGeneID,
		"standard_champion_score":      result.StandardChampionScore,
		"seed_gene_id":                 cfg.SeedGeneID,
		"fixed_param_keys":             cfg.FixedParamKeys,
		"market_region_enabled":        cfg.MarketRegionEnabled,
		"market_region_max_thresholds": cfg.MarketRegionMaxThresholds,
		"multi_market_enabled":         cfg.MultiMarketEnabled,
		"multi_market_instrument_ids":  cfg.MultiMarketInstrumentIDs,
		"multi_market_selections":      cfg.MultiMarketSelections,
		"best_score":                   bestScoreResponse,
		"max_drawdown":                 maxDrawdown,
		"window_score":                 crucibleScores(result.WindowScores),
		"market_performance":           result.MarketPerformance,
		"search_axes":                  result.SearchAxes,
		"search_status":                result.SearchStatus,
		"best_param_pack":              parseRawJSON(result.BestParamPack),
		"gene_record_id":               result.GeneRecordID,
		"mutation_probability":         result.MutationProbability,
		"mutation_scale":               result.MutationScale,
		"evaluated_individuals":        totalEvaluations,
		"planned_evaluations":          totalPlannedEvaluations,
		"computed_units":               result.ComputedUnits,
		"planned_compute_units":        result.PlannedComputeUnits,
		"units_per_individual":         result.UnitsPerIndividual,
		"compute_units_per_sec":        result.ComputeUnitsPerSec,
		"compute_remaining_sec":        result.ComputeRemainingSec,
		"compute_started_at":           result.ComputeStartedAt,
		"compute_updated_at":           result.ComputeUpdatedAt,
		"monitor_updated_at":           result.UpdatedAt,
		"error":                        task.ErrorMessage,
		"created_at":                   task.CreatedAt.Format(time.RFC3339),
		"started_at":                   formatOptionalTime(task.StartedAt),
		"finished_at":                  formatOptionalTime(task.FinishedAt),
	}
}

func genePtrResponse(record *saasstore.GeneRecord) any {
	if record == nil {
		return nil
	}
	return geneResponse(*record)
}

func geneResponse(record saasstore.GeneRecord) gin.H {
	return gin.H{
		"id":                 record.ID,
		"role":               record.Role,
		"strategy_id":        record.StrategyID,
		"instrument_id":      record.InstrumentID,
		"data_source":        record.DataSource,
		"interval":           record.Interval,
		"execution_mode":     record.ExecutionMode,
		"name":               record.Name,
		"notes":              record.Notes,
		"tags":               parseStringSlice(record.Tags),
		"search_config":      parseRawJSON(json.RawMessage(record.SearchConfig)),
		"created_at":         record.CreatedAt.Format(time.RFC3339),
		"activated_at":       formatOptionalTime(record.ActivatedAt),
		"score_total":        record.ScoreTotal,
		"max_drawdown":       record.MaxDrawdown,
		"window_score":       parseWindowScores(record.WindowScore),
		"market_performance": parseMarketPerformance(record.MarketPerformance),
		"param_pack":         parseRawJSON(json.RawMessage(record.ParamPack)),
	}
}

func cleanTags(tags []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
		if len(out) >= 24 {
			break
		}
	}
	return out
}

func parseStringSlice(raw saasstore.JSONB) []string {
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return []string{}
	}
	return cleanTags(tags)
}

func geneScopeFromQuery(c *gin.Context) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		instrumentID := strings.TrimSpace(c.Query("instrument_id"))
		if instrumentID != "" {
			db = db.Where("instrument_id = ?", strings.ToUpper(instrumentID))
		}
		dataSource := strings.TrimSpace(c.Query("data_source"))
		if dataSource != "" {
			db = db.Where("data_source = ?", strings.ToLower(dataSource))
		}
		interval := strings.TrimSpace(c.Query("interval"))
		if interval != "" {
			db = db.Where("interval = ?", interval)
		}
		executionMode := marketdata.NormalizeExecutionMode(c.Query("execution_mode"))
		if c.Query("execution_mode") != "" {
			db = db.Where("execution_mode = ?", executionMode)
		}
		return db
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonZeroInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func parseWindowScores(raw saasstore.JSONB) map[string]float64 {
	var windows []quant.CrucibleResult
	if err := json.Unmarshal([]byte(raw), &windows); err != nil {
		return map[string]float64{}
	}
	out := make(map[string]float64, len(windows))
	for _, window := range windows {
		out[window.Window] = window.Score
	}
	return out
}

func parseMarketPerformance(raw saasstore.JSONB) []ga.MarketPerformance {
	var markets []ga.MarketPerformance
	if err := json.Unmarshal([]byte(raw), &markets); err != nil {
		return []ga.MarketPerformance{}
	}
	return markets
}

func activeWindowSummary(challenger *saasstore.GeneRecord, champion *saasstore.GeneRecord) map[string]float64 {
	if challenger != nil {
		return parseWindowScores(challenger.WindowScore)
	}
	if champion != nil {
		return parseWindowScores(champion.WindowScore)
	}
	return map[string]float64{}
}

func formatOptionalTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339)
}

func parseRawJSON(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func crucibleScores(windows []quant.CrucibleResult) map[string]float64 {
	out := make(map[string]float64, len(windows))
	for _, window := range windows {
		out[window.Window] = window.Score
	}
	return out
}

func parseUintParam(c *gin.Context, name string) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return 0, false
	}
	return id, true
}
