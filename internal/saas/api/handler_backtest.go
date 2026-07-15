package api

import (
	"errors"
	"net/http"
	"strconv"

	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/config"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type BacktestHandler struct {
	appRole string
	service *backtest.Service
}

func NewBacktestHandler(appRole string, db *gorm.DB) *BacktestHandler {
	return &BacktestHandler{
		appRole: appRole,
		service: backtest.NewService(db),
	}
}

func (h *BacktestHandler) Create(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "此功能僅允許 lab/dev 模式"})
		return
	}
	var req backtest.CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	response, err := h.service.Create(c.Request.Context(), currentUserID(c), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, response)
}

func (h *BacktestHandler) Get(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	response, err := h.service.Get(c.Request.Context(), currentUserID(c), uint(id))
	if err != nil {
		if errors.Is(err, backtest.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "backtest not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, response)
}

func (h *BacktestHandler) GetStandardResult(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	response, err := h.service.GetStandardResult(c.Request.Context(), currentUserID(c), uint(id))
	if err != nil {
		if errors.Is(err, backtest.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "backtest result not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, response)
}

func (h *BacktestHandler) GetStandardPathBlock(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	blockIndex := 0
	if raw := c.Query("block_index"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid block_index"})
			return
		}
		blockIndex = parsed
	}
	response, err := h.service.GetStandardPathBlock(c.Request.Context(), currentUserID(c), uint(id), blockIndex)
	if err != nil {
		if errors.Is(err, backtest.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "backtest path block not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, response)
}

func (h *BacktestHandler) VerifyStandardResult(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	response, err := h.service.VerifyStandardResult(c.Request.Context(), currentUserID(c), uint(id))
	if err != nil {
		if errors.Is(err, backtest.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "backtest result not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, response)
}

func (h *BacktestHandler) canUseLab() bool {
	return h.appRole == config.AppRoleLab || h.appRole == config.AppRoleDev
}
