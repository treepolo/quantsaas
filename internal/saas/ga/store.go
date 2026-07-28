package ga

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
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

func (s *GormGenomeStore) ReserveCandidates(ctx context.Context, scope GeneScope, searchConfig []byte, taskID uint, generation int, candidates []CandidateReservation) error {
	if len(candidates) == 0 {
		return nil
	}
	searchHash := candidateSearchHash(searchConfig)
	rows := make([]saasstore.GeneObservation, 0, len(candidates))
	for individual, candidate := range candidates {
		paramPack := candidate.ParamPack
		if !json.Valid(paramPack) {
			paramPack = []byte(`{}`)
		}
		rows = append(rows, saasstore.GeneObservation{
			StrategyID: scope.StrategyID, InstrumentID: scope.InstrumentID, DataSource: scope.DataSource,
			Interval: scope.Interval, ExecutionMode: scope.ExecutionMode, SearchHash: searchHash,
			TaskID: taskID, Generation: generation, Individual: individual,
			Fingerprint: fmt.Sprintf("%016x", candidate.Fingerprint), ParamPack: saasstore.JSONB(paramPack),
		})
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
			return err
		}
		points := aggregateGridPoints(taskID, candidates)
		if len(points) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "task_id"}, {Name: "parameter_key"}, {Name: "grid_step"}},
			DoUpdates: clause.Assignments(map[string]any{"count": gorm.Expr("gene_parameter_grid_points.count + EXCLUDED.count")}),
		}).Create(&points).Error
	})
}

func aggregateGridPoints(taskID uint, candidates []CandidateReservation) []saasstore.GeneParameterGridPoint {
	type gridPointKey struct {
		parameter string
		step      int
	}
	counts := map[gridPointKey]int64{}
	for _, candidate := range candidates {
		for _, chromosome := range candidateChromosomes(candidate.ParamPack) {
			for key, value := range chromosomeGridValues(chromosome) {
				step := int(math.Round(value / searchParameterStep))
				counts[gridPointKey{parameter: key, step: step}]++
			}
		}
	}
	points := make([]saasstore.GeneParameterGridPoint, 0, len(counts))
	for key, count := range counts {
		points = append(points, saasstore.GeneParameterGridPoint{TaskID: taskID, ParameterKey: key.parameter, GridStep: key.step, Count: count})
	}
	return points
}

func candidateChromosomes(raw []byte) []quant.Chromosome {
	var packed struct {
		Chromosome   quant.Chromosome `json:"sigmoid_dca_config"`
		MarketRegion struct {
			Packs []struct {
				Chromosome quant.Chromosome `json:"chromosome"`
			} `json:"packs"`
		} `json:"market_region"`
	}
	if json.Unmarshal(raw, &packed) != nil {
		return nil
	}
	if len(packed.MarketRegion.Packs) > 0 {
		out := make([]quant.Chromosome, 0, len(packed.MarketRegion.Packs))
		for _, pack := range packed.MarketRegion.Packs {
			out = append(out, pack.Chromosome)
		}
		return out
	}
	return []quant.Chromosome{packed.Chromosome}
}

func chromosomeGridValues(c quant.Chromosome) map[string]float64 {
	return map[string]float64{
		"micro_reserve_pct": c.MicroReservePct, "beta": c.Beta, "gamma": c.Gamma,
		"w_mean": c.WMean, "w_momentum": c.WMomentum, "w_breakout": c.WBreakout,
		"dust_usd": c.DustUSD, "rebalance_threshold": c.RebalanceThreshold,
		"force_full_threshold": c.ForceFullThreshold, "force_empty_threshold": c.ForceEmptyThreshold,
		"wedge_delta_threshold": c.WedgeDeltaThreshold, "wedge_vol_ratio_threshold": c.WedgeVolRatioThreshold,
		"macro_bear_multiplier": c.MacroBearMultiplier, "macro_bull_multiplier": c.MacroBullMultiplier,
		"extra_deploy_pct": c.ExtraDeployPct, "soft_release_months": float64(c.SoftReleaseMonths),
		"soft_release_pct": c.SoftReleasePct, "hard_release_max_pct": c.HardReleaseMaxPct,
	}
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
