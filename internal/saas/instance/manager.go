package instance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"quantsaas/internal/protocol"
	"quantsaas/internal/quant"
	"quantsaas/internal/saas/ga"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type KLineProvider interface {
	FetchKLines(ctx context.Context, inst saasstore.StrategyInstance) ([]quant.Bar, error)
}

type AgentSender interface {
	SendToAgent(userID uint, cmd protocol.TradeCommand) error
}

type Manager struct {
	db       *gorm.DB
	klines   KLineProvider
	sender   AgentSender
	logger   *zap.Logger
	strategy string
}

func NewManager(db *gorm.DB, klines KLineProvider, sender AgentSender, logger *zap.Logger) *Manager {
	if logger == nil {
		logger = zap.NewNop()
	}
	if klines == nil {
		klines = NewDBKLineProvider(db)
	}
	return &Manager{
		db:       db,
		klines:   klines,
		sender:   sender,
		logger:   logger,
		strategy: sigmoiddca.StrategyID,
	}
}

func (m *Manager) Start(ctx context.Context, instanceID uint) error {
	return m.db.WithContext(ctx).Model(&saasstore.StrategyInstance{}).
		Where("id = ? AND status <> ?", instanceID, "DELETED").
		Update("status", saasstore.InstanceStatusRunning).Error
}

func (m *Manager) Stop(ctx context.Context, instanceID uint) error {
	return m.db.WithContext(ctx).Model(&saasstore.StrategyInstance{}).
		Where("id = ? AND status = ?", instanceID, saasstore.InstanceStatusRunning).
		Update("status", saasstore.InstanceStatusStopped).Error
}

func (m *Manager) Delete(ctx context.Context, instanceID uint) error {
	return m.db.WithContext(ctx).Model(&saasstore.StrategyInstance{}).
		Where("id = ?", instanceID).
		Update("status", "DELETED").Error
}

func (m *Manager) MarkError(ctx context.Context, instanceID uint, cause error) error {
	m.logger.Warn("instance entered error state", zap.Uint("instance_id", instanceID), zap.Error(cause))
	return m.db.WithContext(ctx).Model(&saasstore.StrategyInstance{}).
		Where("id = ?", instanceID).
		Update("status", saasstore.InstanceStatusError).Error
}

func (m *Manager) RunningInstanceIDs(ctx context.Context) ([]uint, error) {
	var ids []uint
	err := m.db.WithContext(ctx).Model(&saasstore.StrategyInstance{}).
		Where("status = ?", saasstore.InstanceStatusRunning).
		Pluck("id", &ids).Error
	return ids, err
}

func (m *Manager) Tick(ctx context.Context, instanceID uint) error {
	var inst saasstore.StrategyInstance
	if err := m.db.WithContext(ctx).First(&inst, instanceID).Error; err != nil {
		return err
	}
	if inst.Status != saasstore.InstanceStatusRunning {
		return nil
	}

	bars, err := m.klines.FetchKLines(ctx, inst)
	if err != nil {
		return err
	}
	if len(bars) == 0 {
		return errors.New("no kline data")
	}
	latestBarTime := bars[len(bars)-1].OpenTime

	var portfolio saasstore.PortfolioState
	if err := m.db.WithContext(ctx).Where("instance_id = ?", inst.ID).First(&portfolio).Error; err != nil {
		return err
	}
	if latestBarTime <= portfolio.LastProcessedBarTime {
		return nil
	}

	var runtimeState saasstore.RuntimeState
	if err := m.db.WithContext(ctx).Where("instance_id = ?", inst.ID).First(&runtimeState).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			runtimeState = saasstore.RuntimeState{
				InstanceID: inst.ID,
				State:      saasstore.JSONB("{}"),
			}
		} else {
			return err
		}
	}

	closes := quant.ExtractCloses(bars)
	timestamps := quant.ExtractTimestamps(bars)
	params, err := m.loadStrategyParams(ctx, bars)
	if err != nil {
		return err
	}
	lots, err := m.loadLots(ctx, inst.ID)
	if err != nil {
		return err
	}

	output := sigmoiddca.Step(quant.StrategyInput{
		Symbol:     inst.Symbol,
		Interval:   "1d",
		Closes:     closes,
		Timestamps: timestamps,
		Portfolio: quant.PortfolioSnapshot{
			USDTBalance:   portfolio.USDTBalance,
			DeadBTC:       portfolio.DeadBTC,
			FloatBTC:      portfolio.FloatBTC,
			ColdSealedBTC: portfolio.ColdSealedBTC,
			TotalEquity:   portfolio.TotalEquity,
		},
		Lots:         lots,
		RuntimeState: decodeJSONBMap(runtimeState.State),
		Spawn:        params.Spawn,
	}, params)

	commands := make([]protocol.TradeCommand, 0, len(output.Intents))
	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := upsertRuntimeState(tx, inst.ID, output.RuntimeState); err != nil {
			return err
		}
		if err := m.applyLotTransfers(tx, &portfolio, output.LotTransfers); err != nil {
			return err
		}
		for _, intent := range output.Intents {
			cmd, err := commandFromIntent(inst.ID, latestBarTime, intent)
			if err != nil {
				return err
			}
			if err := createPendingExecution(tx, inst.ID, cmd); err != nil {
				return err
			}
			commands = append(commands, cmd)
		}
		return nil
	})
	if err != nil {
		return err
	}

	for _, cmd := range commands {
		if m.sender == nil {
			m.logger.Warn("agent sender unavailable; command left pending", zap.String("client_order_id", cmd.ClientOrderID))
			return nil
		}
		if err := m.sender.SendToAgent(inst.UserID, cmd); err != nil {
			m.logger.Warn("agent not connected; command left pending", zap.Uint("user_id", inst.UserID), zap.Error(err))
			return nil
		}
	}

	return m.db.WithContext(ctx).Model(&saasstore.PortfolioState{}).
		Where("id = ?", portfolio.ID).
		Update("last_processed_bar_time", latestBarTime).Error
}

func (m *Manager) loadStrategyParams(ctx context.Context, bars []quant.Bar) (sigmoiddca.Params, error) {
	var record saasstore.GeneRecord
	err := m.db.WithContext(ctx).
		Where("strategy_id = ? AND role = ?", m.strategy, saasstore.GeneRoleChampion).
		Order("activated_at DESC NULLS LAST, created_at DESC").
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return sigmoiddca.DefaultParams(), nil
	}
	if err != nil {
		return sigmoiddca.Params{}, err
	}
	if params, handled, resolveErr := ga.ResolveMarketRegionParams([]byte(record.ParamPack), bars); handled {
		return params, resolveErr
	}
	return sigmoiddca.ParseParamsFromParamPack([]byte(record.ParamPack)), nil
}

func (m *Manager) loadLots(ctx context.Context, instanceID uint) ([]quant.SpotLot, error) {
	var rows []saasstore.SpotLot
	if err := m.db.WithContext(ctx).Where("instance_id = ?", instanceID).Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	lots := make([]quant.SpotLot, 0, len(rows))
	for _, row := range rows {
		lots = append(lots, quant.SpotLot{
			ID:           row.ID,
			LotType:      row.LotType,
			Amount:       row.Amount,
			CostPrice:    row.CostPrice,
			CreatedAt:    row.CreatedAt,
			IsColdSealed: row.IsColdSealed,
		})
	}
	return lots, nil
}

func (m *Manager) applyLotTransfers(tx *gorm.DB, portfolio *saasstore.PortfolioState, transfers []quant.LotTransfer) error {
	for _, transfer := range transfers {
		if transfer.FromLotType != quant.LotTypeDeadStack || transfer.ToLotType != quant.LotTypeFloating || transfer.Amount <= 0 {
			continue
		}
		amount := min(transfer.Amount, portfolio.DeadBTC)
		portfolio.DeadBTC -= amount
		portfolio.FloatBTC += amount
		if err := tx.Model(portfolio).Updates(map[string]any{
			"dead_btc":  portfolio.DeadBTC,
			"float_btc": portfolio.FloatBTC,
		}).Error; err != nil {
			return err
		}
		payload, _ := json.Marshal(transfer)
		audit := saasstore.AuditLog{
			InstanceID: &portfolio.InstanceID,
			EventType:  "lot_transfer",
			Payload:    saasstore.JSONB(payload),
		}
		if err := tx.Create(&audit).Error; err != nil {
			return err
		}
	}
	return nil
}

func upsertRuntimeState(tx *gorm.DB, instanceID uint, state map[string]any) error {
	raw, _ := json.Marshal(state)
	row := saasstore.RuntimeState{
		InstanceID: instanceID,
		State:      saasstore.JSONB(raw),
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "instance_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"state", "updated_at"}),
	}).Create(&row).Error
}

func createPendingExecution(tx *gorm.DB, instanceID uint, cmd protocol.TradeCommand) error {
	raw, _ := json.Marshal(cmd)
	row := saasstore.SpotExecution{
		InstanceID:    instanceID,
		ClientOrderID: cmd.ClientOrderID,
		Status:        saasstore.ExecutionStatusPending,
		Request:       saasstore.JSONB(raw),
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "client_order_id"}},
		DoNothing: true,
	}).Create(&row).Error
}

func commandFromIntent(instanceID uint, barTime int64, intent quant.TradeIntent) (protocol.TradeCommand, error) {
	if intent.Action == "" {
		return protocol.TradeCommand{}, errors.New("empty intent action")
	}
	cmd := protocol.TradeCommand{
		ClientOrderID: fmt.Sprintf("inst%d-%s-%d", instanceID, intent.Engine, barTime),
		Action:        intent.Action,
		Engine:        intent.Engine,
		Symbol:        intent.Symbol,
		LotType:       intent.LotType,
	}
	if intent.AmountUSDT > 0 {
		cmd.AmountUSDT = strconv.FormatFloat(intent.AmountUSDT, 'f', 2, 64)
	}
	if intent.QtyAsset > 0 {
		cmd.QtyAsset = strconv.FormatFloat(intent.QtyAsset, 'f', 6, 64)
	}
	return cmd, nil
}

func decodeJSONBMap(raw saasstore.JSONB) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

type DBKLineProvider struct {
	db *gorm.DB
}

func NewDBKLineProvider(db *gorm.DB) *DBKLineProvider {
	return &DBKLineProvider{db: db}
}

func (p *DBKLineProvider) FetchKLines(ctx context.Context, inst saasstore.StrategyInstance) ([]quant.Bar, error) {
	var rows []saasstore.KLine
	if err := p.db.WithContext(ctx).
		Where("symbol = ?", inst.Symbol).
		Order("open_time DESC").
		Limit(2500).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("no klines found")
	}
	bars := make([]quant.Bar, len(rows))
	for i, row := range rows {
		target := len(rows) - 1 - i
		bars[target] = quant.Bar{
			OpenTime: row.OpenTime,
			Open:     row.Open,
			High:     row.High,
			Low:      row.Low,
			Close:    row.Close,
			Volume:   row.Volume,
		}
	}
	return bars, nil
}
