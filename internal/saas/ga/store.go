package ga

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"

	"quantsaas/internal/quant"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GormGenomeStore struct {
	db *gorm.DB
}

func NewGormGenomeStore(db *gorm.DB) *GormGenomeStore {
	return &GormGenomeStore{db: db}
}

func (s *GormGenomeStore) LoadEliteGenes(ctx context.Context, scope GeneScope, limit int) ([][]byte, error) {
	if limit <= 0 {
		limit = 16
	}
	var records []saasstore.GeneRecord
	if err := s.db.WithContext(ctx).
		Where("strategy_id = ? AND instrument_id = ? AND data_source = ? AND interval = ? AND execution_mode = ? AND role IN ?",
			scope.StrategyID, scope.InstrumentID, scope.DataSource, scope.Interval, scope.ExecutionMode,
			[]string{saasstore.GeneRoleChampion, saasstore.GeneRoleChallenger}).
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

func (s *GormGenomeStore) SaveChallenger(ctx context.Context, scope GeneScope, paramPack []byte, result FitnessResult, searchConfig []byte) (uint, error) {
	windowScore, err := json.Marshal(result.Windows)
	if err != nil {
		return 0, err
	}
	if !json.Valid(searchConfig) {
		searchConfig = []byte(`{}`)
	}
	record := saasstore.GeneRecord{
		StrategyID:    scope.StrategyID,
		InstrumentID:  scope.InstrumentID,
		DataSource:    scope.DataSource,
		Interval:      scope.Interval,
		ExecutionMode: scope.ExecutionMode,
		Role:          saasstore.GeneRoleChallenger,
		Tags:          saasstore.JSONB(`[]`),
		SearchConfig:  saasstore.JSONB(searchConfig),
		ParamPack:     saasstore.JSONB(paramPack),
		ScoreTotal:    result.ScoreTotal,
		MaxDrawdown:   result.MaxDrawdown,
		WindowScore:   saasstore.JSONB(windowScore),
	}
	if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
		return 0, err
	}
	return record.ID, nil
}

func (s *GormGenomeStore) LoadReservedFingerprints(ctx context.Context, scope GeneScope, searchConfig []byte) (map[uint64]bool, error) {
	searchHash := candidateSearchHash(searchConfig)
	var rows []saasstore.GeneObservation
	if err := s.db.WithContext(ctx).
		Where("strategy_id = ? AND instrument_id = ? AND data_source = ? AND interval = ? AND execution_mode = ? AND search_hash = ?",
			scope.StrategyID, scope.InstrumentID, scope.DataSource, scope.Interval, scope.ExecutionMode, searchHash).
		Select("fingerprint").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	reserved := make(map[uint64]bool, len(rows))
	for _, row := range rows {
		fingerprint, err := strconv.ParseUint(row.Fingerprint, 16, 64)
		if err == nil {
			reserved[fingerprint] = true
		}
	}
	return reserved, nil
}

func (s *GormGenomeStore) ReserveFingerprints(ctx context.Context, scope GeneScope, searchConfig []byte, taskID uint, generation int, fingerprints []uint64) error {
	if len(fingerprints) == 0 {
		return nil
	}
	searchHash := candidateSearchHash(searchConfig)
	rows := make([]saasstore.GeneObservation, 0, len(fingerprints))
	for individual, fingerprint := range fingerprints {
		rows = append(rows, saasstore.GeneObservation{
			StrategyID: scope.StrategyID, InstrumentID: scope.InstrumentID, DataSource: scope.DataSource,
			Interval: scope.Interval, ExecutionMode: scope.ExecutionMode, SearchHash: searchHash,
			TaskID: taskID, Generation: generation, Individual: individual,
			Fingerprint: fmt.Sprintf("%016x", fingerprint), ParamPack: saasstore.JSONB(`{}`),
		})
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

func candidateSearchHash(searchConfig []byte) string {
	digest := sha256.Sum256(searchConfig)
	return fmt.Sprintf("%x", digest)
}

func (s *GormGenomeStore) LoadKLines(ctx context.Context, scope DatasetScope) ([]quant.Bar, error) {
	var rows []saasstore.KLine
	query := s.db.WithContext(ctx).
		Where("symbol = ? AND interval = ?", scope.Symbol, scope.Interval)
	if scope.InstrumentID != "" {
		query = query.Where("instrument_id = ?", scope.InstrumentID)
	}
	if scope.DataSource != "" {
		query = query.Where("source = ?", scope.DataSource)
	}
	if scope.StartTimeMs > 0 {
		query = query.Where("open_time >= ?", scope.StartTimeMs)
	}
	if scope.EndTimeMs > 0 {
		query = query.Where("open_time <= ?", scope.EndTimeMs)
	}
	if err := query.Order("open_time ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no klines for %s %s", scope.Symbol, scope.Interval)
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
