package epoch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/saas/ga"
	"quantsaas/internal/saas/marketdata"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	TaskStatusRunning   = "running"
	TaskStatusDone      = "completed"
	TaskStatusFailed    = "failed"
	TaskStatusCancelled = "cancelled"
)

type Service struct {
	db          *gorm.DB
	engine      *ga.EvolutionEngine
	instruments *marketdata.InstrumentStore
	logger      *zap.Logger
	mu          sync.Mutex
	currentTask *saasstore.EvolutionTask
	cancelFuncs map[uint]context.CancelFunc
	traceMu     sync.Mutex
	traces      map[uint]*traceBuffer
	traceModes  map[uint]ga.TraceMode
}

type CreateTaskRequest struct {
	StrategyID           string            `json:"strategy_id"`
	Pair                 string            `json:"pair"`
	InstrumentID         string            `json:"instrument_id"`
	DataSource           string            `json:"data_source"`
	ExecutionMode        string            `json:"execution_mode"`
	TrainStartMs         int64             `json:"train_start_ms"`
	TrainEndMs           int64             `json:"train_end_ms"`
	Interval             string            `json:"interval"`
	PopSize              int               `json:"pop_size"`
	MaxGenerations       int               `json:"max_generations"`
	SpawnMode            string            `json:"spawn_mode"`
	SpawnPoint           *quant.SpawnPoint `json:"spawn_point"`
	TestMode             bool              `json:"test_mode"`
	TraceMode            ga.TraceMode      `json:"trace_mode"`
	ContinuousMode       string            `json:"continuous_mode"`
	ContinuousIterations int               `json:"continuous_iterations"`
	ContinuousUnlimited  bool              `json:"continuous_unlimited"`
	StandardStartMs      int64             `json:"standard_start_ms"`
	StandardEndMs        int64             `json:"standard_end_ms"`
}

type standardizedChampion struct {
	GeneRecordID uint
	Score        float64
	ParamPack    []byte
}

func NewService(db *gorm.DB, engine *ga.EvolutionEngine, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		db:          db,
		engine:      engine,
		instruments: marketdata.NewInstrumentStore(db),
		logger:      logger,
		cancelFuncs: map[uint]context.CancelFunc{},
		traces:      map[uint]*traceBuffer{},
		traceModes:  map[uint]ga.TraceMode{},
	}
}

func (s *Service) CreateAndRunTask(ctx context.Context, req CreateTaskRequest) (*saasstore.EvolutionTask, error) {
	s.mu.Lock()
	if s.currentTask != nil {
		s.mu.Unlock()
		return nil, errors.New("已有進化任務正在執行")
	}

	req = s.normalizeRequest(ctx, req)
	if err := s.validateRequest(ctx, req); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	spawn, err := s.resolveSpawnPoint(ctx, req)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if err := validateSpawnPoint(spawn); err != nil {
		s.mu.Unlock()
		return nil, err
	}

	configRaw, err := json.Marshal(req)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	now := time.Now().UTC()
	task := &saasstore.EvolutionTask{
		StrategyID:    req.StrategyID,
		InstrumentID:  req.InstrumentID,
		DataSource:    req.DataSource,
		Interval:      req.Interval,
		ExecutionMode: req.ExecutionMode,
		TrainStartMs:  req.TrainStartMs,
		TrainEndMs:    req.TrainEndMs,
		Status:        TaskStatusRunning,
		Progress:      0,
		Config:        saasstore.JSONB(configRaw),
		StartedAt:     &now,
	}
	if err := s.db.WithContext(ctx).Create(task).Error; err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.currentTask = task
	runCtx, cancel := context.WithCancel(context.Background())
	s.cancelFuncs[task.ID] = cancel
	s.initTrace(task.ID, req.TraceMode)
	s.mu.Unlock()

	go s.runEpoch(runCtx, task.ID, req, spawn)
	return task, nil
}

func (s *Service) CurrentTask() *saasstore.EvolutionTask {
	s.mu.Lock()
	task := s.currentTask
	s.mu.Unlock()
	if task == nil {
		return nil
	}
	var latest saasstore.EvolutionTask
	if err := s.db.First(&latest, task.ID).Error; err != nil {
		return task
	}
	return &latest
}

func (s *Service) CancelTask(ctx context.Context, taskID uint) error {
	s.mu.Lock()
	cancel := s.cancelFuncs[taskID]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		return nil
	}
	now := time.Now().UTC()
	updates := map[string]any{
		"status":        TaskStatusCancelled,
		"error_message": "使用者已中止任務",
		"finished_at":   &now,
	}
	var task saasstore.EvolutionTask
	if err := s.db.WithContext(ctx).First(&task, taskID).Error; err == nil {
		var req CreateTaskRequest
		if err := json.Unmarshal([]byte(task.Config), &req); err == nil {
			if id, saveErr := s.saveCancelledBest(ctx, taskID, req); saveErr == nil && id > 0 {
				updates["error_message"] = fmt.Sprintf("使用者已中止任務，已保存目前最佳參數 #%d", id)
			} else if saveErr != nil {
				s.logger.Warn("failed to save cancelled best", zap.Error(saveErr))
			}
		}
	}
	return s.db.WithContext(ctx).
		Model(&saasstore.EvolutionTask{}).
		Where("id = ? AND status = ?", taskID, TaskStatusRunning).
		Updates(updates).Error
}

func (s *Service) runEpoch(ctx context.Context, taskID uint, req CreateTaskRequest, spawn *quant.SpawnPoint) {
	if req.ContinuousMode != "" {
		s.runContinuousEpochs(ctx, taskID, req, spawn)
		return
	}
	result, err := s.engine.RunEpoch(ctx, ga.EpochConfig{
		TaskID:             taskID,
		Pair:               req.Pair,
		InstrumentID:       req.InstrumentID,
		DataSource:         req.DataSource,
		ExecutionMode:      req.ExecutionMode,
		StartTimeMs:        req.TrainStartMs,
		EndTimeMs:          req.TrainEndMs,
		Interval:           req.Interval,
		PopSize:            req.PopSize,
		MaxGenerations:     req.MaxGenerations,
		SpawnMode:          req.SpawnMode,
		LotStepSize:        spawn.Risk.LotStep,
		LotMinQty:          spawn.Risk.LotMin,
		SpawnPointOverride: spawn,
		TraceMode:          req.TraceMode,
		TraceModeFunc:      s.traceModeGetter(taskID),
		OnTrace:            s.traceSink(taskID),
		OnProgress: func(progress ga.EpochProgress) {
			bestParamPack := json.RawMessage(progress.BestParamPack)
			if !json.Valid(bestParamPack) {
				bestParamPack = json.RawMessage(`null`)
			}
			raw, _ := json.Marshal(map[string]any{
				"current_generation":   progress.Generation + 1,
				"best_score":           progress.BestFitness,
				"max_drawdown":         progress.BestMaxDrawdown,
				"window_scores":        progress.BestWindows,
				"best_param_pack":      bestParamPack,
				"mutation_probability": progress.MutationProbability,
				"mutation_scale":       progress.MutationScale,
				"updated_at":           time.Now().UTC().Format(time.RFC3339),
			})
			_ = s.db.Model(&saasstore.EvolutionTask{}).
				Where("id = ?", taskID).
				Updates(map[string]any{
					"progress": float64(progress.Generation+1) / float64(max(1, req.MaxGenerations)),
					"result":   saasstore.JSONB(raw),
				}).Error
		},
	})

	finished := time.Now().UTC()
	updates := map[string]any{
		"finished_at": &finished,
	}
	if err != nil {
		s.logger.Warn("epoch failed", zap.Error(err))
		if errors.Is(err, context.Canceled) {
			updates["status"] = TaskStatusCancelled
			updates["error_message"] = "使用者已中止任務"
			if id, saveErr := s.saveCancelledBest(ctx, taskID, req); saveErr == nil && id > 0 {
				updates["error_message"] = fmt.Sprintf("使用者已中止任務，已保存目前最佳參數 #%d", id)
			} else if saveErr != nil {
				s.logger.Warn("failed to save cancelled best", zap.Error(saveErr))
			}
		} else {
			updates["status"] = TaskStatusFailed
			updates["error_message"] = err.Error()
		}
	} else {
		paramPack := json.RawMessage(result.ParamPack)
		if !json.Valid(paramPack) {
			paramPack = json.RawMessage(`null`)
		}
		raw, _ := json.Marshal(map[string]any{
			"current_generation":   req.MaxGenerations,
			"best_score":           result.Fitness.ScoreTotal,
			"max_drawdown":         result.Fitness.MaxDrawdown,
			"window_scores":        result.Fitness.Windows,
			"best_param_pack":      paramPack,
			"gene_record_id":       result.GeneRecordID,
			"mutation_probability": s.engine.MutationProbability,
			"mutation_scale":       s.engine.MutationScale,
			"updated_at":           finished.Format(time.RFC3339),
			"Fitness":              result.Fitness,
		})
		updates["status"] = TaskStatusDone
		updates["progress"] = 1.0
		updates["result"] = saasstore.JSONB(raw)
	}
	_ = s.db.Model(&saasstore.EvolutionTask{}).Where("id = ?", taskID).Updates(updates).Error

	s.mu.Lock()
	s.currentTask = nil
	delete(s.cancelFuncs, taskID)
	s.mu.Unlock()
}

func (s *Service) runContinuousEpochs(ctx context.Context, taskID uint, req CreateTaskRequest, spawn *quant.SpawnPoint) {
	var champion *standardizedChampion
	var lastResult ga.EpochResult
	var lastErr error
	iteration := 0

	for {
		if err := ctx.Err(); err != nil {
			lastErr = err
			break
		}
		if !req.ContinuousUnlimited && iteration >= req.ContinuousIterations {
			break
		}
		iteration++
		cfg := s.epochConfig(req, spawn, taskID)
		cfg.RandomPopulation = req.ContinuousMode == "random"
		if req.ContinuousMode == "standardized_best" && champion != nil {
			cfg.SeedParamPack = champion.ParamPack
		}
		cfg.OnProgress = s.epochProgressUpdater(taskID, req, iteration)

		result, err := s.engine.RunEpoch(ctx, cfg)
		if err != nil {
			lastErr = err
			break
		}
		lastResult = result

		if req.ContinuousMode == "standardized_best" {
			nextChampion, err := s.refreshStandardizedChampion(ctx, req, result.GeneRecordID, champion)
			if err != nil {
				lastErr = err
				break
			}
			champion = nextChampion
			s.writeContinuousSnapshot(taskID, req, iteration, result, champion, false)
		} else {
			s.writeContinuousSnapshot(taskID, req, iteration, result, nil, false)
		}
	}

	finished := time.Now().UTC()
	updates := map[string]any{"finished_at": &finished}
	if lastErr != nil {
		s.logger.Warn("continuous epoch failed", zap.Error(lastErr))
		if errors.Is(lastErr, context.Canceled) {
			updates["status"] = TaskStatusCancelled
			updates["error_message"] = "使用者已中止任務"
			if id, saveErr := s.saveCancelledBest(ctx, taskID, req); saveErr == nil && id > 0 {
				updates["error_message"] = fmt.Sprintf("使用者已中止任務，已保存目前最佳參數 #%d", id)
			} else if saveErr != nil {
				s.logger.Warn("failed to save cancelled best", zap.Error(saveErr))
			}
		} else {
			updates["status"] = TaskStatusFailed
			updates["error_message"] = lastErr.Error()
		}
	} else {
		updates["status"] = TaskStatusDone
		updates["progress"] = 1.0
		s.writeContinuousSnapshot(taskID, req, iteration, lastResult, champion, true)
	}
	_ = s.db.Model(&saasstore.EvolutionTask{}).Where("id = ?", taskID).Updates(updates).Error

	s.mu.Lock()
	s.currentTask = nil
	delete(s.cancelFuncs, taskID)
	s.mu.Unlock()
}

func (s *Service) epochConfig(req CreateTaskRequest, spawn *quant.SpawnPoint, taskID uint) ga.EpochConfig {
	return ga.EpochConfig{
		TaskID:             taskID,
		Pair:               req.Pair,
		InstrumentID:       req.InstrumentID,
		DataSource:         req.DataSource,
		ExecutionMode:      req.ExecutionMode,
		StartTimeMs:        req.TrainStartMs,
		EndTimeMs:          req.TrainEndMs,
		Interval:           req.Interval,
		PopSize:            req.PopSize,
		MaxGenerations:     req.MaxGenerations,
		SpawnMode:          req.SpawnMode,
		LotStepSize:        spawn.Risk.LotStep,
		LotMinQty:          spawn.Risk.LotMin,
		SpawnPointOverride: spawn,
		TraceMode:          req.TraceMode,
		TraceModeFunc:      s.traceModeGetter(taskID),
		OnTrace:            s.traceSink(taskID),
	}
}

func (s *Service) epochProgressUpdater(taskID uint, req CreateTaskRequest, iteration int) func(ga.EpochProgress) {
	return func(progress ga.EpochProgress) {
		bestParamPack := json.RawMessage(progress.BestParamPack)
		if !json.Valid(bestParamPack) {
			bestParamPack = json.RawMessage(`null`)
		}
		totalIterations := req.ContinuousIterations
		overallProgress := float64(progress.Generation+1) / float64(max(1, req.MaxGenerations))
		if req.ContinuousMode != "" && !req.ContinuousUnlimited {
			overallProgress = (float64(iteration-1) + overallProgress) / float64(max(1, totalIterations))
		}
		raw, _ := json.Marshal(map[string]any{
			"current_generation":    progress.Generation + 1,
			"best_score":            progress.BestFitness,
			"max_drawdown":          progress.BestMaxDrawdown,
			"window_scores":         progress.BestWindows,
			"best_param_pack":       bestParamPack,
			"mutation_probability":  progress.MutationProbability,
			"mutation_scale":        progress.MutationScale,
			"updated_at":            time.Now().UTC().Format(time.RFC3339),
			"continuous_mode":       req.ContinuousMode,
			"current_iteration":     iteration,
			"continuous_iterations": req.ContinuousIterations,
			"continuous_unlimited":  req.ContinuousUnlimited,
			"standard_start_ms":     req.StandardStartMs,
			"standard_end_ms":       req.StandardEndMs,
		})
		_ = s.db.Model(&saasstore.EvolutionTask{}).
			Where("id = ?", taskID).
			Updates(map[string]any{
				"progress": overallProgress,
				"result":   saasstore.JSONB(raw),
			}).Error
	}
}

func (s *Service) writeContinuousSnapshot(taskID uint, req CreateTaskRequest, iteration int, result ga.EpochResult, champion *standardizedChampion, final bool) {
	paramPack := json.RawMessage(result.ParamPack)
	if !json.Valid(paramPack) {
		paramPack = json.RawMessage(`null`)
	}
	payload := map[string]any{
		"current_generation":    req.MaxGenerations,
		"best_score":            result.Fitness.ScoreTotal,
		"max_drawdown":          result.Fitness.MaxDrawdown,
		"window_scores":         result.Fitness.Windows,
		"best_param_pack":       paramPack,
		"gene_record_id":        result.GeneRecordID,
		"mutation_probability":  s.engine.MutationProbability,
		"mutation_scale":        s.engine.MutationScale,
		"updated_at":            time.Now().UTC().Format(time.RFC3339),
		"Fitness":               result.Fitness,
		"continuous_mode":       req.ContinuousMode,
		"current_iteration":     iteration,
		"continuous_iterations": req.ContinuousIterations,
		"continuous_unlimited":  req.ContinuousUnlimited,
		"standard_start_ms":     req.StandardStartMs,
		"standard_end_ms":       req.StandardEndMs,
		"final":                 final,
	}
	if champion != nil {
		payload["standard_champion_gene_id"] = champion.GeneRecordID
		payload["standard_champion_score"] = champion.Score
	}
	raw, _ := json.Marshal(payload)
	_ = s.db.Model(&saasstore.EvolutionTask{}).
		Where("id = ?", taskID).
		Update("result", saasstore.JSONB(raw)).Error
}

func (s *Service) saveCancelledBest(ctx context.Context, taskID uint, req CreateTaskRequest) (uint, error) {
	saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = saveCtx
	var task saasstore.EvolutionTask
	if err := s.db.WithContext(ctx).First(&task, taskID).Error; err != nil {
		return 0, err
	}
	var result struct {
		BestScore     float64                `json:"best_score"`
		MaxDrawdown   float64                `json:"max_drawdown"`
		WindowScores  []quant.CrucibleResult `json:"window_scores"`
		BestParamPack json.RawMessage        `json:"best_param_pack"`
		GeneRecordID  uint                   `json:"gene_record_id"`
	}
	if err := json.Unmarshal([]byte(task.Result), &result); err != nil {
		return 0, nil
	}
	if result.GeneRecordID > 0 || !json.Valid(result.BestParamPack) || string(result.BestParamPack) == "null" {
		return 0, nil
	}
	searchConfig := map[string]any{
		"strategy_id":          req.StrategyID,
		"symbol":               req.Pair,
		"instrument_id":        req.InstrumentID,
		"data_source":          req.DataSource,
		"interval":             req.Interval,
		"execution_mode":       req.ExecutionMode,
		"train_start_ms":       req.TrainStartMs,
		"train_end_ms":         req.TrainEndMs,
		"spawn_mode":           req.SpawnMode,
		"population":           req.PopSize,
		"generations":          req.MaxGenerations,
		"source":               "cancelled_task",
		"cancelled_task_id":    taskID,
		"continuous_mode":      req.ContinuousMode,
		"standard_start_ms":    req.StandardStartMs,
		"standard_end_ms":      req.StandardEndMs,
		"continuous_unlimited": req.ContinuousUnlimited,
	}
	configRaw, _ := json.Marshal(searchConfig)
	windowScore, _ := json.Marshal(result.WindowScores)
	record := saasstore.GeneRecord{
		StrategyID:    req.StrategyID,
		InstrumentID:  req.InstrumentID,
		DataSource:    req.DataSource,
		Interval:      req.Interval,
		ExecutionMode: req.ExecutionMode,
		Role:          saasstore.GeneRoleChallenger,
		Tags:          saasstore.JSONB(`["中止保存"]`),
		SearchConfig:  saasstore.JSONB(configRaw),
		ParamPack:     saasstore.JSONB(result.BestParamPack),
		ScoreTotal:    result.BestScore,
		MaxDrawdown:   result.MaxDrawdown,
		WindowScore:   saasstore.JSONB(windowScore),
	}
	if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
		return 0, err
	}
	raw, _ := json.Marshal(map[string]any{
		"current_generation":   0,
		"best_score":           result.BestScore,
		"max_drawdown":         result.MaxDrawdown,
		"window_scores":        result.WindowScores,
		"best_param_pack":      result.BestParamPack,
		"gene_record_id":       record.ID,
		"updated_at":           time.Now().UTC().Format(time.RFC3339),
		"cancelled_saved_best": true,
	})
	_ = s.db.WithContext(ctx).Model(&saasstore.EvolutionTask{}).Where("id = ?", taskID).Update("result", saasstore.JSONB(raw)).Error
	return record.ID, nil
}

func (s *Service) refreshStandardizedChampion(ctx context.Context, req CreateTaskRequest, newGeneID uint, current *standardizedChampion) (*standardizedChampion, error) {
	if current == nil {
		var records []saasstore.GeneRecord
		if err := s.db.WithContext(ctx).
			Where("strategy_id = ? AND instrument_id = ? AND data_source = ? AND interval = ? AND execution_mode = ? AND role IN ?",
				req.StrategyID, req.InstrumentID, req.DataSource, req.Interval, req.ExecutionMode,
				[]string{saasstore.GeneRoleChallenger, saasstore.GeneRoleChampion, saasstore.GeneRoleRetired}).
			Order("created_at DESC").
			Find(&records).Error; err != nil {
			return nil, err
		}
		var best *standardizedChampion
		for _, record := range records {
			candidate, err := s.evaluateStandardizedRecord(ctx, req, record)
			if err != nil {
				return nil, err
			}
			if best == nil || candidate.Score > best.Score {
				best = candidate
			}
		}
		return best, nil
	}
	var record saasstore.GeneRecord
	if err := s.db.WithContext(ctx).First(&record, newGeneID).Error; err != nil {
		return current, err
	}
	candidate, err := s.evaluateStandardizedRecord(ctx, req, record)
	if err != nil {
		return current, err
	}
	if candidate.Score > current.Score {
		return candidate, nil
	}
	return current, nil
}

func (s *Service) evaluateStandardizedRecord(ctx context.Context, req CreateTaskRequest, record saasstore.GeneRecord) (*standardizedChampion, error) {
	params := sigmoiddca.ParseParamsFromParamPack([]byte(record.ParamPack))
	spawn := params.Spawn
	if err := validateSpawnPoint(&spawn); err != nil {
		return nil, err
	}
	fitness, err := s.engine.EvaluateParamPack(ctx, ga.EpochConfig{
		Pair:               req.Pair,
		InstrumentID:       req.InstrumentID,
		DataSource:         req.DataSource,
		ExecutionMode:      req.ExecutionMode,
		StartTimeMs:        req.StandardStartMs,
		EndTimeMs:          req.StandardEndMs,
		Interval:           req.Interval,
		PopSize:            req.PopSize,
		MaxGenerations:     req.MaxGenerations,
		SpawnMode:          req.SpawnMode,
		LotStepSize:        spawn.Risk.LotStep,
		LotMinQty:          spawn.Risk.LotMin,
		SpawnPointOverride: &spawn,
		TraceMode:          ga.TraceModeSummary,
	}, []byte(record.ParamPack))
	if err != nil {
		return nil, err
	}
	return &standardizedChampion{GeneRecordID: record.ID, Score: fitness.ScoreTotal, ParamPack: []byte(record.ParamPack)}, nil
}

func (s *Service) TraceSnapshot(taskID uint, afterID uint64, limit int) TraceSnapshot {
	s.traceMu.Lock()
	buffer := s.traces[taskID]
	mode := s.traceModes[taskID]
	s.traceMu.Unlock()
	if buffer == nil {
		return TraceSnapshot{TaskID: taskID, Mode: ga.TraceModeOff, Events: []TraceEvent{}}
	}
	return TraceSnapshot{
		TaskID: taskID,
		Mode:   mode,
		Events: buffer.snapshot(afterID, limit),
	}
}

func (s *Service) SetTraceMode(taskID uint, mode ga.TraceMode) ga.TraceMode {
	mode = ga.NormalizeTraceMode(mode)
	s.traceMu.Lock()
	defer s.traceMu.Unlock()
	if s.traces[taskID] == nil {
		s.traces[taskID] = newTraceBuffer(TraceBufferLimit)
	}
	s.traceModes[taskID] = mode
	return mode
}

func (s *Service) initTrace(taskID uint, mode ga.TraceMode) {
	mode = ga.NormalizeTraceMode(mode)
	if mode == ga.TraceModeOff {
		mode = ga.TraceModeDetailed
	}
	s.traceMu.Lock()
	defer s.traceMu.Unlock()
	s.traces[taskID] = newTraceBuffer(TraceBufferLimit)
	s.traceModes[taskID] = mode
}

func (s *Service) traceSink(taskID uint) func(ga.TraceEvent) {
	return func(event ga.TraceEvent) {
		s.traceMu.Lock()
		mode := s.traceModes[taskID]
		buffer := s.traces[taskID]
		s.traceMu.Unlock()
		if buffer == nil || !ga.TraceEnabled(mode, event.RequiredMode) {
			return
		}
		buffer.add(event)
	}
}

func (s *Service) traceModeGetter(taskID uint) func() ga.TraceMode {
	return func() ga.TraceMode {
		s.traceMu.Lock()
		defer s.traceMu.Unlock()
		return s.traceModes[taskID]
	}
}

func (s *Service) resolveSpawnPoint(ctx context.Context, req CreateTaskRequest) (*quant.SpawnPoint, error) {
	switch req.SpawnMode {
	case "inherit":
		spawn, err := s.loadChampionSpawn(ctx, req)
		if err == nil {
			return spawn, nil
		}
		return defaultSpawnPoint(), nil
	case "random_once":
		spawn := randomSpawnPoint()
		return &spawn, nil
	case "manual":
		if req.SpawnPoint == nil {
			return nil, errors.New("manual spawn_mode 需要 spawn_point")
		}
		return req.SpawnPoint, nil
	default:
		return nil, fmt.Errorf("不支援的 spawn_mode: %s", req.SpawnMode)
	}
}

func (s *Service) loadChampionSpawn(ctx context.Context, req CreateTaskRequest) (*quant.SpawnPoint, error) {
	var record saasstore.GeneRecord
	if err := s.db.WithContext(ctx).
		Where("strategy_id = ? AND instrument_id = ? AND data_source = ? AND interval = ? AND execution_mode = ? AND role = ?",
			req.StrategyID, req.InstrumentID, req.DataSource, req.Interval, req.ExecutionMode, saasstore.GeneRoleChampion).
		Order("activated_at DESC NULLS LAST, created_at DESC").
		First(&record).Error; err != nil {
		return nil, err
	}
	params := sigmoiddca.ParseParamsFromParamPack([]byte(record.ParamPack))
	return &params.Spawn, nil
}

func (s *Service) normalizeRequest(ctx context.Context, req CreateTaskRequest) CreateTaskRequest {
	if req.StrategyID == "" {
		req.StrategyID = sigmoiddca.StrategyID
	}
	if req.Pair == "" {
		req.Pair = marketdata.DefaultSymbol
	}
	instrument, err := s.instruments.ResolveInstrument(ctx, req.InstrumentID, req.Pair, req.DataSource)
	if err == nil {
		req.InstrumentID = instrument.ID
		req.Pair = instrument.Symbol
		req.DataSource = instrument.DataSource
	}
	if req.Interval == "" {
		req.Interval = "1d"
	}
	if strings.TrimSpace(req.ExecutionMode) == "" {
		req.ExecutionMode = marketdata.ExecutionModeCloseNextOpen
	} else {
		req.ExecutionMode = marketdata.NormalizeExecutionMode(req.ExecutionMode)
	}
	if req.SpawnMode == "" {
		req.SpawnMode = "inherit"
	}
	req.ContinuousMode = strings.ToLower(strings.TrimSpace(req.ContinuousMode))
	if req.ContinuousMode != "" && req.ContinuousIterations == 0 && !req.ContinuousUnlimited {
		req.ContinuousIterations = 2
	}
	req.TraceMode = ga.NormalizeTraceMode(req.TraceMode)
	if req.TraceMode == ga.TraceModeOff {
		req.TraceMode = ga.TraceModeDetailed
	}
	if req.TestMode {
		req.PopSize = 10
		req.MaxGenerations = 3
	}
	if req.PopSize == 0 {
		req.PopSize = 300
	}
	if req.MaxGenerations == 0 {
		req.MaxGenerations = 25
	}
	return req
}

func (s *Service) validateRequest(ctx context.Context, req CreateTaskRequest) error {
	instrument, err := s.instruments.ResolveInstrument(ctx, req.InstrumentID, req.Pair, req.DataSource)
	if err != nil {
		return err
	}
	if req.DataSource != instrument.DataSource {
		return fmt.Errorf("unsupported data source: %s", req.DataSource)
	}
	if !supportsInterval(instrument.SupportedIntervals, req.Interval) {
		return fmt.Errorf("unsupported interval for %s: %s", instrument.ID, req.Interval)
	}
	if !marketdata.IsSupportedExecutionMode(req.ExecutionMode) {
		return fmt.Errorf("unsupported execution mode: %s", req.ExecutionMode)
	}
	if req.ExecutionMode == marketdata.ExecutionModePreclose10m {
		return errors.New("收盤前 10 分鐘模式需要歷史快照搜尋路徑，目前尚未開放，不能用日 K 假裝參數搜尋")
	}
	if req.TrainStartMs > 0 && req.TrainEndMs > 0 && req.TrainStartMs > req.TrainEndMs {
		return errors.New("train_start_ms must be earlier than train_end_ms")
	}
	switch req.ContinuousMode {
	case "", "standardized_best", "random":
	default:
		return fmt.Errorf("unsupported continuous_mode: %s", req.ContinuousMode)
	}
	if req.ContinuousMode != "" {
		if !req.ContinuousUnlimited && (req.ContinuousIterations < 1 || req.ContinuousIterations > 100) {
			return errors.New("continuous_iterations must be between 1 and 100")
		}
		if req.ContinuousMode == "standardized_best" {
			if req.StandardStartMs == 0 || req.StandardEndMs == 0 {
				return errors.New("standard_start_ms and standard_end_ms are required")
			}
			if req.StandardStartMs > req.StandardEndMs {
				return errors.New("standard_start_ms must be earlier than standard_end_ms")
			}
		}
	}
	if req.StrategyID != sigmoiddca.StrategyID {
		return fmt.Errorf("尚不支援的策略: %s", req.StrategyID)
	}
	switch req.SpawnMode {
	case "inherit", "random_once", "manual":
	default:
		return fmt.Errorf("不支援的 spawn_mode: %s", req.SpawnMode)
	}
	if req.TestMode {
		if req.PopSize != 10 || req.MaxGenerations != 3 {
			return errors.New("test_mode 必須使用 Pop=10、Gen=3")
		}
		return nil
	}
	if req.PopSize < 10 || req.PopSize > 500 {
		return errors.New("pop_size 必須介於 10 到 500")
	}
	if req.MaxGenerations < 5 || req.MaxGenerations > 50 {
		return errors.New("max_generations 必須介於 5 到 50")
	}
	return nil
}

func supportsInterval(supported []string, interval string) bool {
	for _, item := range supported {
		if item == interval {
			return true
		}
	}
	return false
}

func defaultSpawnPoint() *quant.SpawnPoint {
	return &quant.SpawnPoint{
		Policy: quant.CapitalPolicy{
			InitialUSDT:       1000,
			MonthlyInjectUSDT: 100,
		},
		Risk: quant.RiskBounds{
			MaxDrawdownPct: 0.88,
			FeeRate:        0.001,
			LotStep:        0.000001,
			LotMin:         0.00001,
		},
	}
}

func validateSpawnPoint(spawn *quant.SpawnPoint) error {
	if spawn == nil {
		return errors.New("spawn_point 不可為空")
	}
	if spawn.Policy.InitialUSDT <= 0 {
		return errors.New("初始資金必須大於 0")
	}
	if spawn.Policy.MonthlyInjectUSDT < 0 {
		return errors.New("月度投入不可為負數")
	}
	if spawn.Policy.ColdSealedBTC < 0 {
		return errors.New("封存資產不可為負數")
	}
	if spawn.Risk.MaxDrawdownPct <= 0 || spawn.Risk.MaxDrawdownPct > 0.88 {
		return errors.New("最大回撤邊界必須介於 0 到 0.88")
	}
	if spawn.Risk.LotStep <= 0 || spawn.Risk.LotMin <= 0 {
		return errors.New("下單精度與最小數量必須大於 0")
	}
	return nil
}

func randomSpawnPoint() quant.SpawnPoint {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	spawn := *defaultSpawnPoint()
	spawn.Policy.InitialUSDT = 500 + rng.Float64()*4500
	spawn.Policy.MonthlyInjectUSDT = 50 + rng.Float64()*450
	spawn.Risk.MaxDrawdownPct = 0.50 + rng.Float64()*0.38
	return spawn
}
