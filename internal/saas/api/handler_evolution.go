package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/saas/config"
	"quantsaas/internal/saas/epoch"
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
		if err := tx.Where("id = ? AND role = ?", id, saasstore.GeneRoleChallenger).First(&promoted).Error; err != nil {
			return err
		}
		if err := tx.Model(&saasstore.GeneRecord{}).
			Where("strategy_id = ? AND role = ?", promoted.StrategyID, saasstore.GeneRoleChampion).
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
		MaxGenerations int `json:"max_generations"`
	}
	_ = json.Unmarshal([]byte(task.Config), &cfg)
	currentGeneration := 0
	if cfg.MaxGenerations > 0 {
		currentGeneration = int(math.Round(task.Progress * float64(cfg.MaxGenerations)))
	}
	var result struct {
		Fitness struct {
			ScoreTotal  float64 `json:"ScoreTotal"`
			MaxDrawdown float64 `json:"MaxDrawdown"`
		} `json:"Fitness"`
	}
	_ = json.Unmarshal([]byte(task.Result), &result)
	return gin.H{
		"id":                 task.ID,
		"status":             task.Status,
		"progress":           task.Progress,
		"current_generation": currentGeneration,
		"max_generations":    cfg.MaxGenerations,
		"best_score":         result.Fitness.ScoreTotal,
		"max_drawdown":       result.Fitness.MaxDrawdown,
		"created_at":         task.CreatedAt.Format(time.RFC3339),
		"started_at":         formatOptionalTime(task.StartedAt),
		"finished_at":        formatOptionalTime(task.FinishedAt),
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
		"id":           record.ID,
		"role":         record.Role,
		"created_at":   record.CreatedAt.Format(time.RFC3339),
		"score_total":  record.ScoreTotal,
		"max_drawdown": record.MaxDrawdown,
		"window_score": parseWindowScores(record.WindowScore),
	}
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
