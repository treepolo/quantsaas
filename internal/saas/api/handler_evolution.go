package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

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
	c.JSON(http.StatusAccepted, task)
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

	var genes []saasstore.GeneRecord
	if err := h.db.WithContext(c.Request.Context()).
		Where("role IN ?", []string{saasstore.GeneRoleChallenger, saasstore.GeneRoleChampion}).
		Order("created_at DESC").
		Limit(50).
		Find(&genes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"current_task": h.service.CurrentTask(),
		"tasks":        tasks,
		"genes":        genes,
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
	c.JSON(http.StatusOK, promoted)
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
