package ga

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

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
	marketPerformance, err := json.Marshal(result.Markets)
	if err != nil {
		return 0, err
	}
	if !json.Valid(searchConfig) {
		searchConfig = []byte(`{}`)
	}
	record := saasstore.GeneRecord{
		StrategyID:        scope.StrategyID,
		InstrumentID:      scope.InstrumentID,
		DataSource:        scope.DataSource,
		Interval:          scope.Interval,
		ExecutionMode:     scope.ExecutionMode,
		Role:              saasstore.GeneRoleChallenger,
		Tags:              saasstore.JSONB(`[]`),
		SearchConfig:      saasstore.JSONB(searchConfig),
		ParamPack:         saasstore.JSONB(paramPack),
		ScoreTotal:        result.ScoreTotal,
		MaxDrawdown:       result.MaxDrawdown,
		WindowScore:       saasstore.JSONB(windowScore),
		MarketPerformance: saasstore.JSONB(marketPerformance),
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
		Where("evaluation_version = 0 OR evaluated = ?", true).
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

func (s *GormGenomeStore) LoadBestEvaluatedCandidate(ctx context.Context, scope GeneScope, searchConfig []byte) (EvaluatedCandidate, bool, error) {
	searchHash := candidateSearchHash(searchConfig)
	var row saasstore.GeneObservation
	err := s.db.WithContext(ctx).
		Where("strategy_id = ? AND instrument_id = ? AND data_source = ? AND interval = ? AND execution_mode = ? AND search_hash = ?",
			scope.StrategyID, scope.InstrumentID, scope.DataSource, scope.Interval, scope.ExecutionMode, searchHash).
		Where("evaluation_version = ? AND evaluated = ?", 1, true).
		Order("score_total DESC, id ASC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return EvaluatedCandidate{}, false, nil
	}
	if err != nil {
		return EvaluatedCandidate{}, false, err
	}
	fitness := FitnessResult{
		ScoreTotal: row.ScoreTotal, MaxDrawdown: row.MaxDrawdown, Fatal: row.Fatal,
	}
	_ = json.Unmarshal([]byte(row.WindowScore), &fitness.Windows)
	_ = json.Unmarshal([]byte(row.MarketPerformance), &fitness.Markets)
	return EvaluatedCandidate{ParamPack: append([]byte(nil), row.ParamPack...), Fitness: fitness}, true, nil
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
			EvaluationVersion: 1,
		})
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&rows, 200).Error; err != nil {
			return err
		}
		points := aggregateGridPoints(taskID, searchHash, candidates)
		if len(points) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "task_id"}, {Name: "parameter_key"}, {Name: "grid_step"}},
			DoUpdates: clause.Assignments(map[string]any{"count": gorm.Expr("gene_parameter_grid_points.count + EXCLUDED.count")}),
		}).CreateInBatches(&points, 1000).Error
	})
}

func (s *GormGenomeStore) UpdateCandidateResults(ctx context.Context, searchConfig []byte, results []CandidateEvaluation) error {
	if len(results) == 0 {
		return nil
	}
	searchHash := candidateSearchHash(searchConfig)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		const batchSize = 250
		for start := 0; start < len(results); start += batchSize {
			end := min(len(results), start+batchSize)
			placeholders := make([]string, 0, end-start)
			args := make([]any, 0, (end-start)*6+1)
			for _, result := range results[start:end] {
				windowScore, _ := json.Marshal(result.Fitness.Windows)
				marketPerformance, _ := json.Marshal(result.Fitness.Markets)
				placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?)")
				args = append(args, fmt.Sprintf("%016x", result.Fingerprint), result.Fitness.ScoreTotal, result.Fitness.MaxDrawdown, result.Fitness.Fatal, string(windowScore), string(marketPerformance))
			}
			args = append(args, searchHash)
			statement := `UPDATE gene_observations AS observation
				SET score_total = evaluated.score_total,
					max_drawdown = evaluated.max_drawdown,
					fatal = evaluated.fatal,
					window_score = evaluated.window_score::jsonb,
					market_performance = evaluated.market_performance::jsonb,
					evaluated = TRUE
				FROM (VALUES ` + strings.Join(placeholders, ",") + `) AS evaluated(fingerprint, score_total, max_drawdown, fatal, window_score, market_performance)
				WHERE observation.search_hash = ? AND observation.fingerprint = evaluated.fingerprint`
			if err := tx.Exec(statement, args...).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func aggregateGridPoints(taskID uint, searchHash string, candidates []CandidateReservation) []saasstore.GeneParameterGridPoint {
	type gridPointKey struct {
		parameter string
		step      int64
	}
	counts := map[gridPointKey]int64{}
	for _, candidate := range candidates {
		for key, value := range candidateGridValues(candidate.ParamPack) {
			for _, point := range value {
				step := gridStoredStep(key, point)
				counts[gridPointKey{parameter: key, step: step}]++
			}
		}
	}
	points := make([]saasstore.GeneParameterGridPoint, 0, len(counts))
	for key, count := range counts {
		points = append(points, saasstore.GeneParameterGridPoint{TaskID: taskID, SearchHash: searchHash, ParameterKey: key.parameter, GridStep: key.step, GridValue: gridStoredValueForInsert(key.parameter, key.step), Count: count})
	}
	return points
}

func IsMarketThresholdGridKey(key string) bool {
	return strings.HasPrefix(key, "market_region.") && strings.Contains(key, ".threshold_")
}

func gridStoredStep(key string, value float64) int64 {
	if IsMarketThresholdGridKey(key) {
		// Raw market values have no common numeric scale. Their IEEE-754 bits are
		// a reversible, collision-free identity for the exact calculated value;
		// multiplying by a fixed scale overflowed large geometry values.
		return int64(math.Float64bits(value))
	}
	return int64(math.Round(value / searchParameterStep))
}

func GridStoredValue(key string, step int64) float64 {
	if IsMarketThresholdGridKey(key) {
		return math.Float64frombits(uint64(step))
	}
	return float64(step) * searchParameterStep
}

func gridStoredValueForInsert(key string, step int64) float64 {
	return GridStoredValue(key, step)
}

func candidateGridValues(raw []byte) map[string][]float64 {
	values := map[string][]float64{}
	var packed struct {
		MarketRegion struct {
			Global   quant.Chromosome      `json:"global"`
			Features []MarketRegionFeature `json:"features"`
			Packs    []MarketRegionPack    `json:"packs"`
		} `json:"market_region"`
	}
	if json.Unmarshal(raw, &packed) != nil {
		return values
	}
	if len(packed.MarketRegion.Features) == 0 {
		for _, chromosome := range candidateChromosomes(raw) {
			for key, value := range chromosomeGridValues(chromosome) {
				values[key] = append(values[key], value)
			}
		}
		return values
	}
	// A market-region candidate only evolves the per-state six values plus its
	// market windows and decision values.  Do not write fixed global values to
	// the coverage store: showing them as axes made the grid imply that disabled
	// mechanisms were being searched.
	for _, pack := range packed.MarketRegion.Packs {
		digest := sha256.Sum256([]byte(pack.Key))
		statePrefix := fmt.Sprintf("market_region.state_%x.", digest[:4])
		for _, key := range []string{"gamma", "w_mean", "w_momentum", "w_breakout", "force_full_threshold", "force_empty_threshold"} {
			stateKey := statePrefix + key
			values[stateKey] = append(values[stateKey], chromosomeGridValues(pack.Chromosome)[key])
		}
	}
	for _, feature := range packed.MarketRegion.Features {
		values["market_region."+feature.ID+".window"] = append(values["market_region."+feature.ID+".window"], float64(feature.Window))
		for index, threshold := range feature.Thresholds {
			key := fmt.Sprintf("market_region.%s.threshold_%d", feature.ID, index+1)
			values[key] = append(values[key], threshold)
		}
	}
	return values
}

func candidateChromosomes(raw []byte) []quant.Chromosome {
	var packed struct {
		Chromosome   quant.Chromosome `json:"sigmoid_dca_config"`
		MarketRegion struct {
			Global quant.Chromosome `json:"global"`
			Packs  []struct {
				Chromosome quant.Chromosome `json:"chromosome"`
			} `json:"packs"`
		} `json:"market_region"`
	}
	if json.Unmarshal(raw, &packed) != nil {
		return nil
	}
	if len(packed.MarketRegion.Packs) > 0 {
		global := packed.MarketRegion.Global
		if global == (quant.Chromosome{}) {
			global = packed.MarketRegion.Packs[0].Chromosome
		}
		out := make([]quant.Chromosome, 0, len(packed.MarketRegion.Packs)+1)
		out = append(out, global)
		for _, pack := range packed.MarketRegion.Packs {
			out = append(out, combineMarketRegionChromosome(global, pack.Chromosome))
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
