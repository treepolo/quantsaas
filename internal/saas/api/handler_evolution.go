package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
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

func (h *EvolutionHandler) ListTasks(c *gin.Context) {
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
		Where("candidate_schema_version = ? AND role = ?", ga.CoreCandidateSchemaVersion, saasstore.GeneRoleChallenger).
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
	c.JSON(http.StatusOK, gin.H{"status": epoch.TaskStatusCancelling, "task_id": taskID})
}

func (h *EvolutionHandler) GetGridCoverage(c *gin.Context) {
	taskID, ok := parseUintParam(c, "taskID")
	if !ok {
		return
	}
	var task saasstore.EvolutionTask
	if err := h.db.WithContext(c.Request.Context()).First(&task, uint(taskID)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "找不到演化任務"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if task.SearchHash == "" {
		c.JSON(http.StatusOK, gin.H{
			"task_id":     task.ID,
			"search_hash": "",
			"axes":        []any{},
		})
		return
	}
	var rows []saasstore.GeneParameterGridPoint
	if err := h.db.WithContext(c.Request.Context()).
		Model(&saasstore.GeneParameterGridPoint{}).
		Select("parameter_key, parameter_state, grid_index, grid_value, SUM(count) AS count, MAX(last_generation) AS last_generation").
		Where("search_hash = ?", task.SearchHash).
		Group("parameter_key, parameter_state, grid_index, grid_value").
		Order("parameter_key ASC, grid_value ASC").
		Limit(10000).
		Scan(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var gridConfig struct {
		EvolveRebalanceThreshold  bool     `json:"evolve_rebalance_threshold"`
		EvolveForceFullThreshold  bool     `json:"evolve_force_full_threshold"`
		EvolveForceEmptyThreshold bool     `json:"evolve_force_empty_threshold"`
		EvolveGamma               bool     `json:"evolve_gamma"`
		EnableWMean               bool     `json:"enable_w_mean"`
		EnableWMomentum           bool     `json:"enable_w_momentum"`
		EnableWBreakout           bool     `json:"enable_w_breakout"`
		PositionStructure         string   `json:"position_structure"`
		FixedParamKeys            []string `json:"fixed_param_keys"`
		FeeRate                   float64  `json:"fee_rate"`
		SpreadRate                float64  `json:"spread_rate"`
	}
	_ = json.Unmarshal([]byte(task.Config), &gridConfig)
	positionStructure := sigmoiddca.NormalizePositionStructure(gridConfig.PositionStructure)
	metadata := ga.ParameterAxes(ga.GeneOptions{
		EvolveRebalanceThreshold:  gridConfig.EvolveRebalanceThreshold,
		EvolveForceFullThreshold:  gridConfig.EvolveForceFullThreshold,
		EvolveForceEmptyThreshold: gridConfig.EvolveForceEmptyThreshold,
		EvolveGamma:               gridConfig.EvolveGamma,
		EnableWMean:               gridConfig.EnableWMean,
		EnableWMomentum:           gridConfig.EnableWMomentum,
		EnableWBreakout:           gridConfig.EnableWBreakout,
		PositionStructure:         positionStructure,
		DisableMinimumTrade:       positionStructure == sigmoiddca.PositionStructureFloatingOnly || (gridConfig.FeeRate == 0 && gridConfig.SpreadRate == 0),
		FixedParamKeys:            gridConfig.FixedParamKeys,
	})
	byKey := make(map[string]ga.ParameterAxis, len(metadata))
	for _, axis := range metadata {
		byKey[axis.Key] = axis
	}
	type point struct {
		Value float64 `json:"value"`
		Count int64   `json:"count"`
	}
	type axisResponse struct {
		Key            string  `json:"key"`
		Label          string  `json:"label"`
		Kind           string  `json:"kind"`
		State          string  `json:"state"`
		Minimum        float64 `json:"minimum"`
		Maximum        float64 `json:"maximum"`
		Step           float64 `json:"step"`
		GridSize       int     `json:"grid_size"`
		TotalCount     int64   `json:"total_count"`
		LastGeneration int     `json:"last_generation"`
		Points         []point `json:"points"`
	}
	order := make([]string, 0)
	axes := make(map[string]*axisResponse)
	for _, row := range rows {
		axis := axes[row.ParameterKey]
		if axis == nil {
			meta := byKey[row.ParameterKey]
			axis = &axisResponse{
				Key:      row.ParameterKey,
				Label:    meta.Label,
				Kind:     meta.Kind,
				State:    row.ParameterState,
				Minimum:  meta.Minimum,
				Maximum:  meta.Maximum,
				Step:     meta.Step,
				GridSize: meta.GridSize,
				Points:   []point{},
			}
			axes[row.ParameterKey] = axis
			order = append(order, row.ParameterKey)
		}
		axis.Points = append(axis.Points, point{Value: row.GridValue, Count: row.Count})
		axis.TotalCount += row.Count
		if row.LastGeneration > axis.LastGeneration {
			axis.LastGeneration = row.LastGeneration
		}
	}
	result := make([]axisResponse, 0, len(order))
	for _, key := range order {
		result = append(result, *axes[key])
	}
	type generationCount struct {
		Generation int   `json:"generation"`
		Count      int64 `json:"count"`
	}
	var generations []generationCount
	if err := h.db.WithContext(c.Request.Context()).
		Model(&saasstore.GeneParameterGridPoint{}).
		Select("generation, SUM(count) AS count").
		Where("search_hash = ?", task.SearchHash).
		Group("generation").
		Order("generation ASC").
		Scan(&generations).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"task_id":     task.ID,
		"search_hash": task.SearchHash,
		"axes":        result,
		"generations": generations,
	})
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
		if err := tx.Where("id = ? AND candidate_schema_version = ? AND role IN ?",
			id, ga.CoreCandidateSchemaVersion, []string{saasstore.GeneRoleChallenger, saasstore.GeneRoleRetired}).First(&promoted).Error; err != nil {
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
		MultiMarketEnabled        bool                         `json:"multi_market_enabled"`
		MultiMarketSelections     []epoch.MultiMarketSelection `json:"multi_market_selections"`
		GridCoverageEnabled       bool                         `json:"grid_coverage_enabled"`
	}
	_ = json.Unmarshal([]byte(task.Config), &cfg)
	currentGeneration := 0
	if cfg.MaxGenerations > 0 {
		currentGeneration = int(math.Round(task.Progress * float64(cfg.MaxGenerations)))
	}
	var result struct {
		CurrentGeneration      int                    `json:"current_generation"`
		BestValid              bool                   `json:"best_valid"`
		BestScore              *float64               `json:"best_score"`
		MaxDrawdown            *float64               `json:"max_drawdown"`
		MutationProbability    float64                `json:"mutation_probability"`
		MutationScale          float64                `json:"mutation_scale"`
		UpdatedAt              string                 `json:"updated_at"`
		WindowScores           []quant.CrucibleResult `json:"window_scores"`
		MarketPerformance      []ga.MarketPerformance `json:"market_performance"`
		BestParamPack          json.RawMessage        `json:"best_param_pack"`
		GeneRecordID           uint                   `json:"gene_record_id"`
		ContinuousMode         string                 `json:"continuous_mode"`
		CurrentIteration       int                    `json:"current_iteration"`
		ContinuousIterations   int                    `json:"continuous_iterations"`
		ContinuousUnlimited    bool                   `json:"continuous_unlimited"`
		StandardStartMs        int64                  `json:"standard_start_ms"`
		StandardEndMs          int64                  `json:"standard_end_ms"`
		StandardChampionGeneID uint                   `json:"standard_champion_gene_id"`
		StandardChampionScore  float64                `json:"standard_champion_score"`
		ComputeMonitorEnabled  bool                   `json:"compute_monitor_enabled"`
		ComputedUnits          int64                  `json:"computed_units"`
		PlannedComputeUnits    int64                  `json:"planned_compute_units"`
		UnitsPerIndividual     int64                  `json:"units_per_individual"`
		ComputeUnitsPerSec     float64                `json:"compute_units_per_sec"`
		ComputeRemainingSec    float64                `json:"compute_remaining_sec"`
		ComputeStartedAt       string                 `json:"compute_started_at"`
		ComputeUpdatedAt       string                 `json:"compute_updated_at"`
		EvaluatedCount         int64                  `json:"evaluated_count"`
		ValidCount             int64                  `json:"valid_count"`
		SkippedCount           int64                  `json:"skipped_count"`
		FailedCount            int64                  `json:"failed_count"`
		Fitness                struct {
			ScoreTotal  float64 `json:"ScoreTotal"`
			MaxDrawdown float64 `json:"MaxDrawdown"`
		} `json:"Fitness"`
	}
	_ = json.Unmarshal([]byte(task.Result), &result)
	if result.CurrentGeneration > 0 {
		currentGeneration = result.CurrentGeneration
	}
	continuousMode := firstNonEmpty(result.ContinuousMode, cfg.ContinuousMode)
	continuousIterations := firstNonZeroInt(result.ContinuousIterations, cfg.ContinuousIterations)
	continuousUnlimited := result.ContinuousUnlimited || cfg.ContinuousUnlimited
	totalPlannedEvaluations := cfg.PopSize * cfg.MaxGenerations
	if continuousMode != "" {
		if continuousUnlimited {
			totalPlannedEvaluations = 0
		} else {
			totalPlannedEvaluations = cfg.PopSize * cfg.MaxGenerations * continuousIterations
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
		"multi_market_enabled":         cfg.MultiMarketEnabled,
		"multi_market_selections":      cfg.MultiMarketSelections,
		"grid_coverage_enabled":        cfg.GridCoverageEnabled,
		"search_hash":                  task.SearchHash,
		"best_valid":                   result.BestValid,
		"best_score":                   nullableMetric(result.BestValid, result.BestScore),
		"max_drawdown":                 nullableMetric(result.BestValid, result.MaxDrawdown),
		"window_score":                 crucibleScores(result.WindowScores),
		"market_performance":           result.MarketPerformance,
		"best_param_pack":              parseRawJSON(result.BestParamPack),
		"gene_record_id":               result.GeneRecordID,
		"mutation_probability":         result.MutationProbability,
		"mutation_scale":               result.MutationScale,
		"evaluated_individuals":        result.EvaluatedCount,
		"evaluated_count":              result.EvaluatedCount,
		"valid_count":                  result.ValidCount,
		"skipped_count":                result.SkippedCount,
		"failed_count":                 result.FailedCount,
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
		"id":                       record.ID,
		"role":                     record.Role,
		"strategy_id":              record.StrategyID,
		"candidate_schema_version": record.CandidateSchemaVersion,
		"search_hash":              record.SearchHash,
		"instrument_id":            record.InstrumentID,
		"data_source":              record.DataSource,
		"interval":                 record.Interval,
		"execution_mode":           record.ExecutionMode,
		"name":                     record.Name,
		"notes":                    record.Notes,
		"tags":                     parseStringSlice(record.Tags),
		"search_config":            parseRawJSON(json.RawMessage(record.SearchConfig)),
		"created_at":               record.CreatedAt.Format(time.RFC3339),
		"activated_at":             formatOptionalTime(record.ActivatedAt),
		"score_total":              record.ScoreTotal,
		"max_drawdown":             record.MaxDrawdown,
		"window_score":             parseWindowScores(record.WindowScore),
		"market_performance":       parseRawJSON(json.RawMessage(record.MarketPerformance)),
		"param_pack":               parseRawJSON(json.RawMessage(record.ParamPack)),
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

func nullableMetric(valid bool, value *float64) any {
	if !valid || value == nil {
		return nil
	}
	return *value
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
