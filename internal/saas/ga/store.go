package ga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"quantsaas/internal/quant"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

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
		Where("candidate_schema_version = ? AND search_hash = ?", CoreCandidateSchemaVersion, scope.SearchHash).
		Order("score_total DESC, CASE role WHEN 'champion' THEN 0 WHEN 'challenger' THEN 1 ELSE 2 END, created_at DESC").
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
		StrategyID:             scope.StrategyID,
		InstrumentID:           scope.InstrumentID,
		DataSource:             scope.DataSource,
		Interval:               scope.Interval,
		ExecutionMode:          scope.ExecutionMode,
		CandidateSchemaVersion: CoreCandidateSchemaVersion,
		SearchHash:             scope.SearchHash,
		Role:                   saasstore.GeneRoleChallenger,
		Tags:                   saasstore.JSONB(`[]`),
		SearchConfig:           saasstore.JSONB(searchConfig),
		ParamPack:              saasstore.JSONB(paramPack),
		ScoreTotal:             result.ScoreTotal,
		MaxDrawdown:            result.MaxDrawdown,
		WindowScore:            saasstore.JSONB(windowScore),
		MarketPerformance:      saasstore.JSONB(marketPerformance),
	}
	if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
		return 0, err
	}
	return record.ID, nil
}

func (s *GormGenomeStore) ReserveCandidates(ctx context.Context, scope GeneScope, taskID uint, generation int, candidates []CandidateReservation) ([]CandidateReservationOutcome, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	outcomes := make([]CandidateReservationOutcome, 0, len(candidates))
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, candidate := range candidates {
			if err := ctx.Err(); err != nil {
				return err
			}
			now := time.Now().UTC()
			fingerprint := candidate.Identity
			if fingerprint == "" {
				fingerprint = fmt.Sprintf("%016x", candidate.Fingerprint)
			}
			paramPack := candidate.ParamPack
			if !json.Valid(paramPack) {
				paramPack = []byte(`{}`)
			}
			row := saasstore.GeneCandidateEvaluation{
				SchemaVersion:     CoreCandidateSchemaVersion,
				SearchHash:        scope.SearchHash,
				Fingerprint:       fingerprint,
				Status:            saasstore.GeneCandidateStatusReserved,
				TaskID:            taskID,
				Generation:        generation,
				Individual:        candidate.Individual,
				AttemptCount:      1,
				ParamPack:         saasstore.JSONB(paramPack),
				WindowScore:       saasstore.JSONB(`[]`),
				MarketPerformance: saasstore.JSONB(`[]`),
				ReservedAt:        &now,
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
			var stored saasstore.GeneCandidateEvaluation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("schema_version = ? AND search_hash = ? AND fingerprint = ?", CoreCandidateSchemaVersion, scope.SearchHash, fingerprint).
				First(&stored).Error; err != nil {
				return err
			}
			outcome := CandidateReservationOutcome{Fingerprint: candidate.Fingerprint, Identity: fingerprint}
			switch stored.Status {
			case saasstore.GeneCandidateStatusCompleted:
				outcome.Completed = true
				outcome.Fitness = candidateFitness(stored)
			case saasstore.GeneCandidateStatusFailed, saasstore.GeneCandidateStatusInterrupted:
				updates := map[string]any{
					"status":         saasstore.GeneCandidateStatusReserved,
					"task_id":        taskID,
					"generation":     generation,
					"individual":     candidate.Individual,
					"attempt_count":  gorm.Expr("attempt_count + 1"),
					"param_pack":     saasstore.JSONB(paramPack),
					"failure_reason": "",
					"reserved_at":    &now,
					"completed_at":   nil,
					"score_total":    nil,
					"max_drawdown":   nil,
				}
				result := tx.Model(&saasstore.GeneCandidateEvaluation{}).
					Where("id = ? AND status IN ?", stored.ID, []string{saasstore.GeneCandidateStatusFailed, saasstore.GeneCandidateStatusInterrupted}).
					Updates(updates)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected == 1 {
					outcome.Reserved = true
					outcome.ReservationID = stored.ID
				}
			case saasstore.GeneCandidateStatusReserved:
				if stored.TaskID == taskID {
					outcome.Reserved = true
					outcome.ReservationID = stored.ID
				}
			}
			outcomes = append(outcomes, outcome)
		}
		return nil
	})
	return outcomes, err
}

func (s *GormGenomeStore) CompleteCandidateEvaluations(ctx context.Context, evaluations []CandidateEvaluation) error {
	if len(evaluations) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, evaluation := range evaluations {
			if evaluation.ReservationID == 0 {
				continue
			}
			now := time.Now().UTC()
			updates := map[string]any{
				"completed_at": &now,
			}
			if evaluation.Error != "" {
				updates["status"] = saasstore.GeneCandidateStatusFailed
				updates["failure_reason"] = evaluation.Error
				updates["score_total"] = nil
				updates["max_drawdown"] = nil
			} else {
				windowScore, _ := json.Marshal(evaluation.Fitness.Windows)
				marketPerformance, _ := json.Marshal(evaluation.Fitness.Markets)
				score := evaluation.Fitness.ScoreTotal
				maxDrawdown := evaluation.Fitness.MaxDrawdown
				updates["status"] = saasstore.GeneCandidateStatusCompleted
				updates["score_total"] = &score
				updates["max_drawdown"] = &maxDrawdown
				updates["fatal"] = evaluation.Fitness.Fatal
				updates["failure_reason"] = evaluation.Fitness.FailureReason
				updates["window_score"] = saasstore.JSONB(windowScore)
				updates["market_performance"] = saasstore.JSONB(marketPerformance)
			}
			result := tx.Model(&saasstore.GeneCandidateEvaluation{}).
				Where("id = ? AND status = ?", evaluation.ReservationID, saasstore.GeneCandidateStatusReserved).
				Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("candidate reservation #%d is no longer active", evaluation.ReservationID)
			}
		}
		return nil
	})
}

func (s *GormGenomeStore) RecordGridCoverage(ctx context.Context, scope GeneScope, taskID uint, generation int, axes []ParameterAxis, candidates []CandidateReservation) error {
	if len(axes) == 0 || len(candidates) == 0 {
		return nil
	}
	type pointKey struct {
		parameter string
		state     string
		index     int64
		valueBits uint64
	}
	counts := map[pointKey]int64{}
	for _, candidate := range candidates {
		params := sigmoiddca.ParseParamsFromParamPack(candidate.ParamPack)
		for _, axis := range axes {
			value := chromosomeValue(params.Chromosome, axis.Key)
			index := gridCoordinate(axis.Key, axis.Kind, value)
			if axis.State != ParameterStateEvolving {
				index = int64(math.Float64bits(value))
			}
			counts[pointKey{
				parameter: axis.Key,
				state:     string(axis.State),
				index:     index,
				valueBits: math.Float64bits(value),
			}]++
		}
	}
	rows := make([]saasstore.GeneParameterGridPoint, 0, len(counts))
	for key, count := range counts {
		rows = append(rows, saasstore.GeneParameterGridPoint{
			SearchHash:     scope.SearchHash,
			ParameterKey:   key.parameter,
			ParameterState: key.state,
			GridIndex:      key.index,
			Generation:     generation,
			GridValue:      math.Float64frombits(key.valueBits),
			Count:          count,
			LastTaskID:     taskID,
			LastGeneration: generation,
			TaskID:         taskID,
			GridStep:       legacyGridStep(generation, key.index),
		})
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "search_hash"},
			{Name: "parameter_key"},
			{Name: "parameter_state"},
			{Name: "grid_index"},
			{Name: "generation"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"count":           gorm.Expr("gene_parameter_grid_points.count + EXCLUDED.count"),
			"grid_value":      gorm.Expr("EXCLUDED.grid_value"),
			"last_task_id":    taskID,
			"last_generation": generation,
			"updated_at":      time.Now().UTC(),
		}),
	}).CreateInBatches(&rows, 500).Error
}

func legacyGridStep(generation int, gridIndex int64) int64 {
	return gridIndex + int64(generation)*1_000_000
}

func candidateFitness(row saasstore.GeneCandidateEvaluation) FitnessResult {
	result := FitnessResult{Fatal: row.Fatal, FailureReason: row.FailureReason}
	if row.ScoreTotal != nil {
		result.ScoreTotal = *row.ScoreTotal
	}
	if row.MaxDrawdown != nil {
		result.MaxDrawdown = *row.MaxDrawdown
	}
	_ = json.Unmarshal([]byte(row.WindowScore), &result.Windows)
	_ = json.Unmarshal([]byte(row.MarketPerformance), &result.Markets)
	return result
}

func (s *GormGenomeStore) MarkInterruptedCandidates(ctx context.Context, taskIDs []uint, reason string) error {
	if len(taskIDs) == 0 {
		return nil
	}
	if reason == "" {
		reason = "服務重啟前評估未完成"
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&saasstore.GeneCandidateEvaluation{}).
		Where("task_id IN ? AND status = ?", taskIDs, saasstore.GeneCandidateStatusReserved).
		Updates(map[string]any{
			"status":         saasstore.GeneCandidateStatusInterrupted,
			"failure_reason": reason,
			"completed_at":   &now,
		}).Error
}

func parseCandidateFingerprint(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("empty fingerprint")
	}
	return strconv.ParseUint(value, 16, 64)
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
