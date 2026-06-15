package api

import (
	"errors"
	"net/http"
	"strings"

	"quantsaas/internal/saas/config"
	"quantsaas/internal/saas/marketdata"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type MarketDataHandler struct {
	appRole string
	service *marketdata.Service
}

func NewMarketDataHandler(appRole string, db *gorm.DB) *MarketDataHandler {
	return &MarketDataHandler{
		appRole: appRole,
		service: marketdata.NewService(db, nil),
	}
}

func (h *MarketDataHandler) Status(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "此路由僅允許 lab/dev 模式"})
		return
	}
	symbol := c.DefaultQuery("symbol", marketdata.DefaultSymbol)
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		symbol = marketdata.DefaultSymbol
	}
	var intervals []string
	if raw := strings.TrimSpace(c.Query("intervals")); raw != "" {
		intervals = strings.Split(raw, ",")
	}
	rows, err := h.service.Summaries(c.Request.Context(), symbol, intervals)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"symbol":              symbol,
		"supported_intervals": marketdata.SupportedIntervals(),
		"datasets":            rows,
	})
}

func (h *MarketDataHandler) Import(c *gin.Context) {
	if !h.canUseLab() {
		c.JSON(http.StatusForbidden, gin.H{"error": "此路由僅允許 lab/dev 模式"})
		return
	}
	var req marketdata.ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Import(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, marketdata.ErrUnsupportedInterval), errors.Is(err, marketdata.ErrInvalidRange):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *MarketDataHandler) canUseLab() bool {
	return h.appRole == config.AppRoleLab || h.appRole == config.AppRoleDev
}
