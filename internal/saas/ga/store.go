package ga

import (
	"context"
	"encoding/json"
	"fmt"

	"quantsaas/internal/quant"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
)

type GormGenomeStore struct {
	db *gorm.DB
}

func NewGormGenomeStore(db *gorm.DB) *GormGenomeStore {
	return &GormGenomeStore{db: db}
}

func (s *GormGenomeStore) LoadEliteGenes(ctx context.Context, strategyID string, limit int) ([][]byte, error) {
	if limit <= 0 {
		limit = 16
	}
	var records []saasstore.GeneRecord
	if err := s.db.WithContext(ctx).
		Where("strategy_id = ? AND role IN ?", strategyID, []string{saasstore.GeneRoleChampion, saasstore.GeneRoleChallenger}).
		Order("CASE role WHEN 'champion' THEN 0 WHEN 'challenger' THEN 1 ELSE 2 END, score_total DESC, created_at DESC").
		Limit(limit).
		Find(&records).Error; err != nil {
		return nil, err
	}

	raw := make([][]byte, 0, len(records))
	for _, record := range records {
		raw = append(raw, []byte(record.ParamPack))
	}
	return raw, nil
}

func (s *GormGenomeStore) SaveChallenger(ctx context.Context, strategyID string, paramPack []byte, result FitnessResult) (uint, error) {
	windowScore, err := json.Marshal(result.Windows)
	if err != nil {
		return 0, err
	}
	record := saasstore.GeneRecord{
		StrategyID:  strategyID,
		Role:        saasstore.GeneRoleChallenger,
		ParamPack:   saasstore.JSONB(paramPack),
		ScoreTotal:  result.ScoreTotal,
		MaxDrawdown: result.MaxDrawdown,
		WindowScore: saasstore.JSONB(windowScore),
	}
	if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
		return 0, err
	}
	return record.ID, nil
}

func (s *GormGenomeStore) LoadKLines(ctx context.Context, symbol string, interval string) ([]quant.Bar, error) {
	var rows []saasstore.KLine
	if err := s.db.WithContext(ctx).
		Where("symbol = ? AND interval = ?", symbol, interval).
		Order("open_time ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no klines for %s %s", symbol, interval)
	}

	bars := make([]quant.Bar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, quant.Bar{
			OpenTime: row.OpenTime,
			Open:     row.Open,
			High:     row.High,
			Low:      row.Low,
			Close:    row.Close,
			Volume:   row.Volume,
		})
	}
	return bars, nil
}
