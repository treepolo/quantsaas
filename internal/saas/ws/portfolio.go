package ws

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"quantsaas/internal/protocol"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
)

func (h *Hub) processDeltaReport(userID uint, report protocol.DeltaReport) error {
	if report.ClientOrderID == "" {
		return h.updateInitialBalances(userID, report.Balances)
	}

	return h.db.Transaction(func(tx *gorm.DB) error {
		var execution saasstore.SpotExecution
		if err := tx.Where("client_order_id = ? AND status = ?", report.ClientOrderID, saasstore.ExecutionStatusPending).
			First(&execution).Error; err != nil {
			return err
		}

		var cmd protocol.TradeCommand
		_ = json.Unmarshal(execution.Request, &cmd)
		status := saasstore.ExecutionStatusFilled
		if report.Execution != nil && strings.EqualFold(report.Execution.Status, "failed") {
			status = saasstore.ExecutionStatusFailed
		}

		now := time.Now().UTC()
		if err := tx.Model(&execution).Updates(map[string]any{
			"status":       status,
			"response":     mustJSONB(report),
			"completed_at": &now,
		}).Error; err != nil {
			return err
		}
		if status == saasstore.ExecutionStatusFailed {
			return h.writeAudit(tx, execution.InstanceID, "delta_report_failed", report)
		}

		if err := h.applyExecution(tx, execution.InstanceID, cmd, report); err != nil {
			return err
		}
		if err := h.updateBalancesForInstance(tx, execution.InstanceID, cmd.Symbol, report.Balances, report.Execution); err != nil {
			return err
		}
		return h.writeAudit(tx, execution.InstanceID, "delta_report_filled", report)
	})
}

func (h *Hub) applyExecution(tx *gorm.DB, instanceID uint, cmd protocol.TradeCommand, report protocol.DeltaReport) error {
	if report.Execution == nil {
		return nil
	}
	qty := parseFloat(report.Execution.FilledQty)
	price := parseFloat(report.Execution.FilledPrice)
	fee := parseFloat(report.Execution.Fee)
	if qty <= 0 && cmd.QtyAsset != "" {
		qty = parseFloat(cmd.QtyAsset)
	}
	if price <= 0 && cmd.AmountUSDT != "" && qty > 0 {
		price = parseFloat(cmd.AmountUSDT) / qty
	}

	record := saasstore.TradeRecord{
		InstanceID:    instanceID,
		ClientOrderID: cmd.ClientOrderID,
		Action:        cmd.Action,
		Engine:        cmd.Engine,
		Symbol:        cmd.Symbol,
		LotType:       cmd.LotType,
		FilledQty:     qty,
		FilledPrice:   price,
		Fee:           fee,
		ExecutedAt:    time.Now().UTC(),
		RawPayload:    mustJSONB(report),
	}
	if err := tx.Create(&record).Error; err != nil {
		return err
	}

	var portfolio saasstore.PortfolioState
	if err := tx.Where("instance_id = ?", instanceID).First(&portfolio).Error; err != nil {
		return err
	}
	if cmd.Action == "BUY" {
		if cmd.LotType == saasstore.LotTypeDeadStack {
			portfolio.DeadBTC += qty
		} else {
			portfolio.FloatBTC += qty
		}
		if cmd.AmountUSDT != "" {
			portfolio.USDTBalance -= parseFloat(cmd.AmountUSDT)
		}
	} else if cmd.Action == "SELL" {
		portfolio.FloatBTC -= min(qty, portfolio.FloatBTC)
		portfolio.USDTBalance += qty * price
	}
	if price > 0 {
		portfolio.TotalEquity = portfolio.USDTBalance + (portfolio.DeadBTC+portfolio.FloatBTC+portfolio.ColdSealedBTC)*price
	}
	return tx.Save(&portfolio).Error
}

func (h *Hub) updateInitialBalances(userID uint, balances []protocol.Balance) error {
	var instances []saasstore.StrategyInstance
	if err := h.db.Where("user_id = ?", userID).Find(&instances).Error; err != nil {
		return err
	}
	return h.db.Transaction(func(tx *gorm.DB) error {
		for _, inst := range instances {
			if err := h.updateBalancesForInstance(tx, inst.ID, inst.Symbol, balances, nil); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		return nil
	})
}

func (h *Hub) updateBalancesForInstance(tx *gorm.DB, instanceID uint, symbol string, balances []protocol.Balance, execution *protocol.Execution) error {
	var portfolio saasstore.PortfolioState
	if err := tx.Where("instance_id = ?", instanceID).First(&portfolio).Error; err != nil {
		return err
	}
	if usdt, ok := findBalance(balances, "USDT"); ok {
		portfolio.USDTBalance = usdt
	}
	price := 0.0
	if execution != nil {
		price = parseFloat(execution.FilledPrice)
	}
	if price > 0 {
		portfolio.TotalEquity = portfolio.USDTBalance + (portfolio.DeadBTC+portfolio.FloatBTC+portfolio.ColdSealedBTC)*price
	}
	return tx.Save(&portfolio).Error
}

func (h *Hub) writeAudit(tx *gorm.DB, instanceID uint, eventType string, payload any) error {
	raw, _ := json.Marshal(payload)
	audit := saasstore.AuditLog{
		InstanceID: &instanceID,
		EventType:  eventType,
		Payload:    saasstore.JSONB(raw),
	}
	return tx.Create(&audit).Error
}

func findBalance(balances []protocol.Balance, asset string) (float64, bool) {
	for _, balance := range balances {
		if strings.EqualFold(balance.Asset, asset) {
			return parseFloat(balance.Available) + parseFloat(balance.Frozen), true
		}
	}
	return 0, false
}

func parseFloat(v string) float64 {
	parsed, _ := strconv.ParseFloat(v, 64)
	return parsed
}

func mustJSONB(v any) saasstore.JSONB {
	raw, _ := json.Marshal(v)
	return saasstore.JSONB(raw)
}
