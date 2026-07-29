package epoch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/marketversion"
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

	SearchInitialUSDT = 1000000
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
	computeMu   sync.Mutex
	computes    map[uint]*computeMonitor
}

type CreateTaskRequest struct {
	StrategyID                string                 `json:"strategy_id"`
	ResearchDatasetID         uint                   `json:"research_dataset_id"`
	Pair                      string                 `json:"pair"`
	InstrumentID              string                 `json:"instrument_id"`
	DataSource                string                 `json:"data_source"`
	ExecutionMode             string                 `json:"execution_mode"`
	TrainStartMs              int64                  `json:"train_start_ms"`
	TrainEndMs                int64                  `json:"train_end_ms"`
	Interval                  string                 `json:"interval"`
	PopSize                   int                    `json:"pop_size"`
	MaxGenerations            int                    `json:"max_generations"`
	SearchAlgorithm           string                 `json:"search_algorithm"`
	LayeredLocalPercent       int                    `json:"layered_local_percent"`
	SpawnMode                 string                 `json:"spawn_mode"`
	SpawnPoint                *quant.SpawnPoint      `json:"spawn_point"`
	InitialCapital            float64                `json:"initial_capital"`
	MonthlyDCA                *float64               `json:"monthly_dca"`
	EvolveRebalanceThreshold  bool                   `json:"evolve_rebalance_threshold"`
	EvolveForceFullThreshold  bool                   `json:"evolve_force_full_threshold"`
	EvolveForceEmptyThreshold bool                   `json:"evolve_force_empty_threshold"`
	EvolveGamma               bool                   `json:"evolve_gamma"`
	EnableWMean               *bool                  `json:"enable_w_mean"`
	EnableWMomentum           *bool                  `json:"enable_w_momentum"`
	EnableWBreakout           *bool                  `json:"enable_w_breakout"`
	PositionStructure         string                 `json:"position_structure"`
	TradePenalty              float64                `json:"trade_penalty"`
	FeeRate                   *float64               `json:"fee_rate"`
	SpreadRate                *float64               `json:"spread_rate"`
	LongTermFilterEnabled     *bool                  `json:"long_term_filter_enabled"`
	LongTermFilterMonths      int                    `json:"long_term_filter_months"`
	TestMode                  bool                   `json:"test_mode"`
	TraceMode                 ga.TraceMode           `json:"trace_mode"`
	ComputeMonitorEnabled     bool                   `json:"compute_monitor_enabled"`
	ContinuousMode            string                 `json:"continuous_mode"`
	ContinuousIterations      int                    `json:"continuous_iterations"`
	ContinuousUnlimited       bool                   `json:"continuous_unlimited"`
	StandardStartMs           int64                  `json:"standard_start_ms"`
	StandardEndMs             int64                  `json:"standard_end_ms"`
	SeedGeneID                uint                   `json:"seed_gene_id"`
	FixedParamKeys            []string               `json:"fixed_param_keys"`
	MarketRegionEnabled       bool                   `json:"market_region_enabled"`
	MarketRegionMaxThresholds int                    `json:"market_region_max_thresholds"`
	MultiMarketEnabled        bool                   `json:"multi_market_enabled"`
	MultiMarketInstrumentIDs  []string               `json:"multi_market_instrument_ids"`
	MultiMarketSelections     []MultiMarketSelection `json:"multi_market_selections"`
	seedParamPack             []byte
	fixedGene                 *quant.Chromosome
	multiMarkets              []ga.MarketScope
	searchCheckpoint          []byte
	resumeIteration           int
	autoSeedGene              bool
}

type MultiMarketSelection struct {
	InstrumentID string `json:"instrument_id"`
	UseAllData   bool   `json:"use_all_data"`
	StartTimeMs  int64  `json:"start_time_ms"`
	EndTimeMs    int64  `json:"end_time_ms"`
}

type ComputeEstimate struct {
	Enabled            bool  `json:"enabled"`
	UnitsPerIndividual int64 `json:"units_per_individual"`
	PlannedUnits       int64 `json:"planned_units"`
}

type computeMonitor struct {
	enabled            bool
	startedAt          time.Time
	unitsPerIndividual atomic.Int64
	plannedUnits       atomic.Int64
	computedUnits      atomic.Int64
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
	service := &Service{
		db:          db,
		engine:      engine,
		instruments: marketdata.NewInstrumentStore(db),
		logger:      logger,
		cancelFuncs: map[uint]context.CancelFunc{},
		traces:      map[uint]*traceBuffer{},
		traceModes:  map[uint]ga.TraceMode{},
		computes:    map[uint]*computeMonitor{},
	}
	if err := service.resumeInterruptedTasks(); err != nil {
		logger.Warn("failed to recover interrupted evolution tasks", zap.Error(err))
	}
	return service
}

// Evolution work and trace buffers are in memory. After a SaaS restart a row
// still marked running has no executing goroutine behind it, so leaving it
// running makes the lab permanently show zero progress and blocks new tasks.
func (s *Service) recoverInterruptedTasks() error {
	now := time.Now().UTC()
	return s.db.Model(&saasstore.EvolutionTask{}).
		Where("status = ?", TaskStatusRunning).
		Updates(map[string]any{
			"status":        TaskStatusFailed,
			"error_message": "服務重啟，原本執行中的演化任務已中斷；請重新建立任務。",
			"finished_at":   now,
		}).Error
}

// resumeInterruptedTasks reconstructs the in-memory runner after a service
// restart. Reservations are durable, so a restarted layered run skips every
// already evaluated fingerprint before it evaluates another candidate.
func (s *Service) resumeInterruptedTasks() error {
	var task saasstore.EvolutionTask
	err := s.db.Where("status = ?", TaskStatusRunning).Order("id ASC").First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var req CreateTaskRequest
	if err := json.Unmarshal([]byte(task.Config), &req); err != nil {
		return s.failInterruptedTask(task.ID, err)
	}
	var snapshot struct {
		SearchCheckpoint json.RawMessage `json:"search_checkpoint"`
		CurrentIteration int             `json:"current_iteration"`
	}
	_ = json.Unmarshal([]byte(task.Result), &snapshot)
	if req.SearchAlgorithm != ga.SearchAlgorithmGenetic {
		if len(snapshot.SearchCheckpoint) == 0 || string(snapshot.SearchCheckpoint) == "null" {
			return s.failInterruptedTask(task.ID, errors.New("舊版逐層搜尋任務沒有相容的搜尋檢查點，已安全終止以避免新舊演算法混算"))
		}
		req.searchCheckpoint = append([]byte(nil), snapshot.SearchCheckpoint...)
		req.resumeIteration = max(1, snapshot.CurrentIteration)
	}
	ctx := context.Background()
	req = s.normalizeRequest(ctx, req)
	if err := s.resolveMultiMarkets(ctx, &req); err != nil {
		return s.failInterruptedTask(task.ID, err)
	}
	var prepareErr error
	req, prepareErr = s.prepareSeedGene(ctx, req)
	if prepareErr != nil {
		return s.failInterruptedTask(task.ID, prepareErr)
	}
	spawn, prepareErr := s.resolveSpawnPoint(ctx, req)
	if prepareErr != nil {
		return s.failInterruptedTask(task.ID, prepareErr)
	}
	applySearchCapital(req, spawn)
	if prepareErr = validateSpawnPoint(spawn); prepareErr != nil {
		return s.failInterruptedTask(task.ID, prepareErr)
	}
	s.currentTask = &task
	runCtx, cancel := context.WithCancel(context.Background())
	s.cancelFuncs[task.ID] = cancel
	s.initTrace(task.ID, req.TraceMode)
	s.initComputeMonitor(task.ID, req.ComputeMonitorEnabled)
	go s.runEpoch(runCtx, task.ID, req, spawn)
	return nil
}

func (s *Service) failInterruptedTask(taskID uint, cause error) error {
	now := time.Now().UTC()
	if err := s.db.Model(&saasstore.EvolutionTask{}).Where("id = ?", taskID).Updates(map[string]any{
		"status": TaskStatusFailed, "error_message": cause.Error(), "finished_at": &now,
	}).Error; err != nil {
		return err
	}
	return cause
}

func (s *Service) CreateAndRunTask(ctx context.Context, req CreateTaskRequest) (*saasstore.EvolutionTask, error) {
	s.mu.Lock()
	if s.currentTask != nil {
		s.mu.Unlock()
		return nil, errors.New("已有進化任務正在執行")
	}

	req = s.normalizeRequest(ctx, req)
	var err error
	if err = s.resolveMultiMarkets(ctx, &req); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if err := s.validateRequest(ctx, req); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	req, err = s.prepareSeedGene(ctx, req)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	spawn, err := s.resolveSpawnPoint(ctx, req)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	applySearchCapital(req, spawn)
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
	s.initComputeMonitor(task.ID, req.ComputeMonitorEnabled)
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
		s.overlayComputeSnapshot(task)
		return task
	}
	s.overlayComputeSnapshot(&latest)
	return &latest
}

func (s *Service) EstimateCompute(ctx context.Context, req CreateTaskRequest) (ComputeEstimate, error) {
	req = s.normalizeRequest(ctx, req)
	var err error
	if err = s.resolveMultiMarkets(ctx, &req); err != nil {
		return ComputeEstimate{}, err
	}
	if err := s.validateRequest(ctx, req); err != nil {
		return ComputeEstimate{}, err
	}
	req, err = s.prepareSeedGene(ctx, req)
	if err != nil {
		return ComputeEstimate{}, err
	}
	spawn, err := s.resolveSpawnPoint(ctx, req)
	if err != nil {
		return ComputeEstimate{}, err
	}
	applySearchCapital(req, spawn)
	if err := validateSpawnPoint(spawn); err != nil {
		return ComputeEstimate{}, err
	}
	estimate, err := s.engine.EstimateComputePlan(ctx, s.epochConfig(req, spawn, 0))
	if err != nil {
		return ComputeEstimate{}, err
	}
	return ComputeEstimate{
		Enabled:            req.ComputeMonitorEnabled,
		UnitsPerIndividual: estimate.UnitsPerIndividual,
		PlannedUnits:       plannedComputeUnitsForRequest(req, estimate.PlannedUnits),
	}, nil
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
		TaskID:              taskID,
		Pair:                req.Pair,
		InstrumentID:        req.InstrumentID,
		DataSource:          req.DataSource,
		ExecutionMode:       req.ExecutionMode,
		StartTimeMs:         req.TrainStartMs,
		EndTimeMs:           req.TrainEndMs,
		Interval:            req.Interval,
		PopSize:             req.PopSize,
		MaxGenerations:      req.MaxGenerations,
		SearchAlgorithm:     req.SearchAlgorithm,
		LayeredLocalPercent: req.LayeredLocalPercent,
		SpawnMode:           req.SpawnMode,
		LotStepSize:         spawn.Risk.LotStep,
		LotMinQty:           spawn.Risk.LotMin,
		InitialCapital:      searchInitialCapital(req),
		MonthlyDCA:          searchMonthlyDCA(req),
		GeneOptions:         searchGeneOptions(req),
		Costs:               searchCosts(req),
		TradePenalty:        searchTradePenalty(req),
		LongTermFilter:      searchLongTermFilter(req),
		SpawnPointOverride:  spawn,
		TraceMode:           req.TraceMode,
		TraceModeFunc:       s.traceModeGetter(taskID),
		SeedGeneID:          req.SeedGeneID,
		SeedParamPack:       req.seedParamPack,
		SeedIsAutomatic:     req.autoSeedGene,
		OnTrace:             s.traceSink(taskID),
		OnComputePlan:       s.computePlanSetter(taskID, req),
		OnComputeStep:       s.computeStepCounter(taskID),
		SearchCheckpoint:    req.searchCheckpoint,
		OnSearchCheckpoint:  s.searchCheckpointWriter(taskID),
		OnSearchIdentity:    s.searchIdentityWriter(taskID),
		MultiMarketEnabled:  req.MultiMarketEnabled,
		MultiMarkets:        req.multiMarkets,
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
				"market_performance":   progress.BestMarkets,
				"best_param_pack":      bestParamPack,
				"mutation_probability": progress.MutationProbability,
				"mutation_scale":       progress.MutationScale,
				"search_axes":          progress.SearchAxes,
				"search_status":        progress.SearchStatus,
				"updated_at":           time.Now().UTC().Format(time.RFC3339),
			})
			raw = s.mergeExistingTaskResult(taskID, raw)
			raw = s.mergeComputeSnapshotJSON(taskID, raw)
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
		raw = s.mergeComputeSnapshotJSON(taskID, raw)
		updates["status"] = TaskStatusDone
		updates["progress"] = 1.0
		updates["result"] = saasstore.JSONB(raw)
	}
	_ = s.db.Model(&saasstore.EvolutionTask{}).Where("id = ?", taskID).Updates(updates).Error

	s.mu.Lock()
	s.currentTask = nil
	delete(s.cancelFuncs, taskID)
	s.mu.Unlock()
	s.deleteComputeMonitor(taskID)
}

func (s *Service) runContinuousEpochs(ctx context.Context, taskID uint, req CreateTaskRequest, spawn *quant.SpawnPoint) {
	var champion *standardizedChampion
	var lastResult ga.EpochResult
	var lastErr error
	iteration := 0
	if req.resumeIteration > 0 {
		iteration = req.resumeIteration - 1
	}
	reservedFingerprints := make(map[uint64]bool)

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
		if req.resumeIteration > 0 && iteration == req.resumeIteration {
			cfg.SearchCheckpoint = req.searchCheckpoint
		}
		cfg.ReservedFingerprints = reservedFingerprints
		cfg.RandomPopulation = req.ContinuousMode == "random"
		cfg.UseInitialSeedOnly = req.ContinuousMode == "initial_seed"
		if req.ContinuousMode == "standardized_best" && champion != nil {
			cfg.SeedParamPack = champion.ParamPack
			cfg.SeedIsAutomatic = false
		}
		cfg.OnProgress = s.epochProgressUpdater(taskID, req, iteration)

		result, err := s.engine.RunEpoch(ctx, cfg)
		if err != nil {
			lastErr = err
			break
		}
		lastResult = result
		req.searchCheckpoint = nil
		req.resumeIteration = 0

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
	s.deleteComputeMonitor(taskID)
}

func (s *Service) epochConfig(req CreateTaskRequest, spawn *quant.SpawnPoint, taskID uint) ga.EpochConfig {
	return ga.EpochConfig{
		TaskID:              taskID,
		Pair:                req.Pair,
		InstrumentID:        req.InstrumentID,
		DataSource:          req.DataSource,
		ExecutionMode:       req.ExecutionMode,
		StartTimeMs:         req.TrainStartMs,
		EndTimeMs:           req.TrainEndMs,
		Interval:            req.Interval,
		PopSize:             req.PopSize,
		MaxGenerations:      req.MaxGenerations,
		SearchAlgorithm:     req.SearchAlgorithm,
		LayeredLocalPercent: req.LayeredLocalPercent,
		SpawnMode:           req.SpawnMode,
		LotStepSize:         spawn.Risk.LotStep,
		LotMinQty:           spawn.Risk.LotMin,
		InitialCapital:      searchInitialCapital(req),
		MonthlyDCA:          searchMonthlyDCA(req),
		GeneOptions:         searchGeneOptions(req),
		Costs:               searchCosts(req),
		TradePenalty:        searchTradePenalty(req),
		LongTermFilter:      searchLongTermFilter(req),
		SpawnPointOverride:  spawn,
		TraceMode:           req.TraceMode,
		TraceModeFunc:       s.traceModeGetter(taskID),
		SeedGeneID:          req.SeedGeneID,
		SeedParamPack:       req.seedParamPack,
		SeedIsAutomatic:     req.autoSeedGene,
		OnTrace:             s.traceSink(taskID),
		OnComputePlan:       s.computePlanSetter(taskID, req),
		OnComputeStep:       s.computeStepCounter(taskID),
		SearchCheckpoint:    req.searchCheckpoint,
		OnSearchCheckpoint:  s.searchCheckpointWriter(taskID),
		OnSearchIdentity:    s.searchIdentityWriter(taskID),
		MultiMarketEnabled:  req.MultiMarketEnabled,
		MultiMarkets:        req.multiMarkets,
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
			"market_performance":    progress.BestMarkets,
			"best_param_pack":       bestParamPack,
			"mutation_probability":  progress.MutationProbability,
			"mutation_scale":        progress.MutationScale,
			"search_axes":           progress.SearchAxes,
			"search_status":         progress.SearchStatus,
			"updated_at":            time.Now().UTC().Format(time.RFC3339),
			"continuous_mode":       req.ContinuousMode,
			"current_iteration":     iteration,
			"continuous_iterations": req.ContinuousIterations,
			"continuous_unlimited":  req.ContinuousUnlimited,
			"standard_start_ms":     req.StandardStartMs,
			"standard_end_ms":       req.StandardEndMs,
		})
		raw = s.mergeExistingTaskResult(taskID, raw)
		raw = s.mergeComputeSnapshotJSON(taskID, raw)
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
		"market_performance":    result.Fitness.Markets,
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
	raw = s.mergeComputeSnapshotJSON(taskID, raw)
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
		BestScore         float64                `json:"best_score"`
		MaxDrawdown       float64                `json:"max_drawdown"`
		WindowScores      []quant.CrucibleResult `json:"window_scores"`
		MarketPerformance []ga.MarketPerformance `json:"market_performance"`
		BestParamPack     json.RawMessage        `json:"best_param_pack"`
		GeneRecordID      uint                   `json:"gene_record_id"`
	}
	if err := json.Unmarshal([]byte(task.Result), &result); err != nil {
		return 0, nil
	}
	if result.GeneRecordID > 0 || !json.Valid(result.BestParamPack) || string(result.BestParamPack) == "null" {
		return 0, nil
	}
	searchConfig := map[string]any{
		"strategy_id":                  req.StrategyID,
		"symbol":                       req.Pair,
		"instrument_id":                req.InstrumentID,
		"data_source":                  req.DataSource,
		"interval":                     req.Interval,
		"execution_mode":               req.ExecutionMode,
		"train_start_ms":               req.TrainStartMs,
		"train_end_ms":                 req.TrainEndMs,
		"initial_capital":              searchInitialCapital(req),
		"monthly_dca":                  searchMonthlyDCA(req),
		"gene_options":                 searchGeneOptions(req),
		"evolve_rebalance_threshold":   req.EvolveRebalanceThreshold,
		"evolve_force_full_threshold":  req.EvolveForceFullThreshold,
		"evolve_force_empty_threshold": req.EvolveForceEmptyThreshold,
		"evolve_gamma":                 req.EvolveGamma,
		"enable_w_mean":                boolValue(req.EnableWMean),
		"enable_w_momentum":            boolValue(req.EnableWMomentum),
		"enable_w_breakout":            boolValue(req.EnableWBreakout),
		"position_structure":           normalizedPositionStructure(req.PositionStructure),
		"trade_penalty":                searchTradePenalty(req),
		"long_term_filter_enabled":     boolValue(req.LongTermFilterEnabled),
		"long_term_filter_months":      req.LongTermFilterMonths,
		"long_term_filter_version":     backtestcore.LongTermFilterVersion,
		"fee_rate":                     searchCosts(req).FeeRate,
		"spread_rate":                  searchCosts(req).SpreadRate,
		"spawn_mode":                   req.SpawnMode,
		"population":                   req.PopSize,
		"generations":                  req.MaxGenerations,
		"source":                       "cancelled_task",
		"cancelled_task_id":            taskID,
		"continuous_mode":              req.ContinuousMode,
		"standard_start_ms":            req.StandardStartMs,
		"standard_end_ms":              req.StandardEndMs,
		"continuous_unlimited":         req.ContinuousUnlimited,
		"seed_gene_id":                 req.SeedGeneID,
		"fixed_param_keys":             req.FixedParamKeys,
		"multi_market_enabled":         req.MultiMarketEnabled,
		"multi_market_instrument_ids":  req.MultiMarketInstrumentIDs,
		"multi_markets":                req.multiMarkets,
	}
	configRaw, _ := json.Marshal(searchConfig)
	windowScore, _ := json.Marshal(result.WindowScores)
	marketPerformance, _ := json.Marshal(result.MarketPerformance)
	record := saasstore.GeneRecord{
		StrategyID:        req.StrategyID,
		InstrumentID:      req.InstrumentID,
		DataSource:        req.DataSource,
		Interval:          req.Interval,
		ExecutionMode:     req.ExecutionMode,
		Role:              saasstore.GeneRoleChallenger,
		Tags:              saasstore.JSONB(`["中止保存"]`),
		SearchConfig:      saasstore.JSONB(configRaw),
		ParamPack:         saasstore.JSONB(result.BestParamPack),
		ScoreTotal:        result.BestScore,
		MaxDrawdown:       result.MaxDrawdown,
		WindowScore:       saasstore.JSONB(windowScore),
		MarketPerformance: saasstore.JSONB(marketPerformance),
	}
	if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
		return 0, err
	}
	raw, _ := json.Marshal(map[string]any{
		"current_generation":   0,
		"best_score":           result.BestScore,
		"max_drawdown":         result.MaxDrawdown,
		"window_scores":        result.WindowScores,
		"market_performance":   result.MarketPerformance,
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
	applySearchCapital(req, &spawn)
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
		InitialCapital:     searchInitialCapital(req),
		MonthlyDCA:         searchMonthlyDCA(req),
		GeneOptions:        searchGeneOptions(req),
		Costs:              searchCosts(req),
		TradePenalty:       searchTradePenalty(req),
		LongTermFilter:     searchLongTermFilter(req),
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

func (s *Service) initComputeMonitor(taskID uint, enabled bool) {
	s.computeMu.Lock()
	defer s.computeMu.Unlock()
	if !enabled {
		delete(s.computes, taskID)
		return
	}
	s.computes[taskID] = &computeMonitor{
		enabled:   true,
		startedAt: time.Now().UTC(),
	}
}

func (s *Service) deleteComputeMonitor(taskID uint) {
	s.computeMu.Lock()
	defer s.computeMu.Unlock()
	delete(s.computes, taskID)
}

func (s *Service) computeMonitor(taskID uint) *computeMonitor {
	s.computeMu.Lock()
	defer s.computeMu.Unlock()
	return s.computes[taskID]
}

func (s *Service) computePlanSetter(taskID uint, req CreateTaskRequest) func(ga.ComputePlan) {
	if !req.ComputeMonitorEnabled {
		return nil
	}
	return func(plan ga.ComputePlan) {
		monitor := s.computeMonitor(taskID)
		if monitor == nil || !monitor.enabled {
			return
		}
		monitor.unitsPerIndividual.Store(plan.UnitsPerIndividual)
		monitor.plannedUnits.Store(plannedComputeUnitsForRequest(req, plan.PlannedUnits))
	}
}

func (s *Service) computeStepCounter(taskID uint) func(int64) {
	monitor := s.computeMonitor(taskID)
	if monitor == nil || !monitor.enabled {
		return nil
	}
	return func(delta int64) {
		if delta > 0 {
			monitor.computedUnits.Add(delta)
		}
	}
}

func plannedComputeUnitsForRequest(req CreateTaskRequest, epochPlanned int64) int64 {
	if epochPlanned <= 0 {
		return 0
	}
	if req.ContinuousMode == "" {
		return epochPlanned
	}
	if req.ContinuousUnlimited {
		return 0
	}
	return epochPlanned * int64(max(1, req.ContinuousIterations))
}

func (s *Service) overlayComputeSnapshot(task *saasstore.EvolutionTask) {
	if task == nil {
		return
	}
	task.Result = saasstore.JSONB(s.mergeComputeSnapshotJSON(task.ID, []byte(task.Result)))
}

func (s *Service) mergeExistingTaskResult(taskID uint, raw []byte) []byte {
	existing := map[string]any{}
	var task saasstore.EvolutionTask
	if err := s.db.Select("result").First(&task, taskID).Error; err == nil && len(task.Result) > 0 {
		_ = json.Unmarshal([]byte(task.Result), &existing)
	}
	incoming := map[string]any{}
	if err := json.Unmarshal(raw, &incoming); err != nil {
		return raw
	}
	for key, value := range incoming {
		existing[key] = value
	}
	merged, err := json.Marshal(existing)
	if err != nil {
		return raw
	}
	return merged
}

func (s *Service) searchCheckpointWriter(taskID uint) func([]byte) {
	return func(checkpoint []byte) {
		if !json.Valid(checkpoint) {
			return
		}
		raw, _ := json.Marshal(map[string]any{
			"search_checkpoint": json.RawMessage(checkpoint),
			"updated_at":        time.Now().UTC().Format(time.RFC3339),
		})
		raw = s.mergeExistingTaskResult(taskID, raw)
		_ = s.db.Model(&saasstore.EvolutionTask{}).
			Where("id = ? AND status = ?", taskID, TaskStatusRunning).
			Update("result", saasstore.JSONB(raw)).Error
	}
}

func (s *Service) searchIdentityWriter(taskID uint) func(string) {
	return func(searchHash string) {
		if strings.TrimSpace(searchHash) == "" {
			return
		}
		raw, _ := json.Marshal(map[string]any{
			"search_hash": searchHash,
			"updated_at":  time.Now().UTC().Format(time.RFC3339),
		})
		raw = s.mergeExistingTaskResult(taskID, raw)
		_ = s.db.Model(&saasstore.EvolutionTask{}).
			Where("id = ? AND status = ?", taskID, TaskStatusRunning).
			Update("result", saasstore.JSONB(raw)).Error
	}
}

func (s *Service) mergeComputeSnapshotJSON(taskID uint, raw []byte) []byte {
	fields := s.computeSnapshotFields(taskID)
	if len(fields) == 0 {
		return raw
	}
	payload := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	for key, value := range fields {
		payload[key] = value
	}
	merged, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return merged
}

func (s *Service) computeSnapshotFields(taskID uint) map[string]any {
	monitor := s.computeMonitor(taskID)
	if monitor == nil || !monitor.enabled {
		return nil
	}
	now := time.Now().UTC()
	computed := monitor.computedUnits.Load()
	planned := monitor.plannedUnits.Load()
	elapsed := now.Sub(monitor.startedAt).Seconds()
	rate := 0.0
	if elapsed > 0 {
		rate = float64(computed) / elapsed
	}
	remainingSeconds := 0.0
	if planned > computed && rate > 0 {
		remainingSeconds = float64(planned-computed) / rate
	}
	return map[string]any{
		"compute_monitor_enabled": true,
		"computed_units":          computed,
		"planned_compute_units":   planned,
		"units_per_individual":    monitor.unitsPerIndividual.Load(),
		"compute_units_per_sec":   rate,
		"compute_remaining_sec":   remainingSeconds,
		"compute_started_at":      monitor.startedAt.Format(time.RFC3339),
		"compute_updated_at":      now.Format(time.RFC3339),
	}
}

func (s *Service) initTrace(taskID uint, mode ga.TraceMode) {
	mode = ga.NormalizeTraceMode(mode)
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

func (s *Service) prepareSeedGene(ctx context.Context, req CreateTaskRequest) (CreateTaskRequest, error) {
	if req.SeedGeneID == 0 {
		// The deterministic layered search must have a meaningful centre even
		// when the form did not manually select a saved gene.  Reuse the best
		// compatible completed candidate; continuous runs then replace this
		// centre with their own newly found best result on the next iteration.
		if req.SearchAlgorithm == "" || req.SearchAlgorithm == ga.SearchAlgorithmLayeredGrid {
			var records []saasstore.GeneRecord
			if err := s.db.WithContext(ctx).
				Where("strategy_id = ? AND instrument_id = ? AND data_source = ? AND interval = ? AND execution_mode = ? AND role IN ?",
					req.StrategyID, req.InstrumentID, req.DataSource, req.Interval, req.ExecutionMode,
					[]string{saasstore.GeneRoleChampion, saasstore.GeneRoleChallenger, saasstore.GeneRoleRetired}).
				Order("score_total DESC, created_at DESC").Limit(500).Find(&records).Error; err == nil {
				var record *saasstore.GeneRecord
				for index := range records {
					if geneRecordMatchesMarketScope(records[index], req) {
						record = &records[index]
						break
					}
				}
				if record == nil {
					return req, nil
				}
				req.SeedGeneID = record.ID
				req.seedParamPack = []byte(record.ParamPack)
				req.autoSeedGene = true
				params := sigmoiddca.ParseParamsFromParamPack(req.seedParamPack)
				fixed := params.Chromosome
				req.fixedGene = &fixed
			}
		}
		return req, nil
	}
	var record saasstore.GeneRecord
	if err := s.db.WithContext(ctx).First(&record, req.SeedGeneID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return req, fmt.Errorf("seed_gene_id #%d not found", req.SeedGeneID)
		}
		return req, err
	}
	if record.StrategyID != req.StrategyID ||
		record.InstrumentID != req.InstrumentID ||
		record.DataSource != req.DataSource ||
		record.Interval != req.Interval ||
		record.ExecutionMode != req.ExecutionMode {
		return req, fmt.Errorf("seed_gene_id #%d scope does not match this search", req.SeedGeneID)
	}
	if !geneRecordMatchesMarketScope(record, req) {
		return req, fmt.Errorf("seed_gene_id #%d multi-market scope does not match this search", req.SeedGeneID)
	}
	switch record.Role {
	case saasstore.GeneRoleChallenger, saasstore.GeneRoleChampion, saasstore.GeneRoleRetired:
	default:
		return req, fmt.Errorf("seed_gene_id #%d has unsupported role %s", req.SeedGeneID, record.Role)
	}
	req.seedParamPack = []byte(record.ParamPack)
	params := sigmoiddca.ParseParamsFromParamPack(req.seedParamPack)
	fixed := params.Chromosome
	req.fixedGene = &fixed
	return req, nil
}

func geneRecordMatchesMarketScope(record saasstore.GeneRecord, req CreateTaskRequest) bool {
	if !req.MultiMarketEnabled {
		return true
	}
	var config struct {
		MultiMarkets []ga.MarketScope `json:"multi_markets"`
	}
	if err := json.Unmarshal([]byte(record.SearchConfig), &config); err != nil {
		return false
	}
	if len(config.MultiMarkets) != len(req.multiMarkets) {
		return false
	}
	for index := range config.MultiMarkets {
		saved := config.MultiMarkets[index]
		current := req.multiMarkets[index]
		if saved.InstrumentID != current.InstrumentID ||
			saved.Pair != current.Pair ||
			saved.DataSource != current.DataSource ||
			saved.StartTimeMs != current.StartTimeMs ||
			saved.EndTimeMs != current.EndTimeMs {
			return false
		}
	}
	return true
}

func (s *Service) normalizeRequest(ctx context.Context, req CreateTaskRequest) CreateTaskRequest {
	if req.MultiMarketEnabled {
		// The single-market research dataset belongs only to single-market mode.
		// Multi-market evaluation is defined exclusively by its selections.
		req.ResearchDatasetID = 0
	}
	if req.ResearchDatasetID > 0 {
		if dataset, err := s.loadResearchDataset(ctx, req.ResearchDatasetID); err == nil {
			req.InstrumentID = dataset.primary.InstrumentID
			req.Pair = dataset.primary.Symbol
			req.DataSource = dataset.primary.DataSource
			req.Interval = dataset.primary.Interval
			req.TrainStartMs = dataset.record.StartTimeMs
			req.TrainEndMs = dataset.record.EndTimeMs
		}
	}
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
	if req.SearchAlgorithm != ga.SearchAlgorithmGenetic {
		req.SearchAlgorithm = ga.SearchAlgorithmLayeredGrid
	}
	if req.LayeredLocalPercent == 0 {
		req.LayeredLocalPercent = 70
	}
	if req.LayeredLocalPercent < 0 {
		req.LayeredLocalPercent = 0
	}
	if req.LayeredLocalPercent > 100 {
		req.LayeredLocalPercent = 100
	}
	req.InitialCapital = SearchInitialUSDT
	if req.MonthlyDCA == nil {
		zero := 0.0
		req.MonthlyDCA = &zero
	}
	if req.EnableWMean == nil {
		value := true
		req.EnableWMean = &value
	}
	if req.LongTermFilterEnabled == nil {
		value := true
		req.LongTermFilterEnabled = &value
	}
	if req.LongTermFilterMonths == 0 {
		req.LongTermFilterMonths = backtestcore.DefaultLongTermFilterMonths
	}
	if req.EnableWMomentum == nil {
		value := true
		req.EnableWMomentum = &value
	}
	if req.EnableWBreakout == nil {
		value := true
		req.EnableWBreakout = &value
	}
	req.PositionStructure = normalizedPositionStructure(req.PositionStructure)
	// Persist the effective candidate space, not merely the unchecked form
	// values.  Market-region search owns the state-controlled parameters and
	// explicitly removes global Beta, reserve and rebalancing from execution.
	if req.MarketRegionEnabled {
		req.EvolveRebalanceThreshold = false
		req.EvolveGamma = false
		req.EvolveForceFullThreshold = false
		req.EvolveForceEmptyThreshold = false
		// Every market-region parameter pack owns all three signal weights.
		// Keep the effective flags enabled so request validation, persisted
		// configuration and compute estimation describe that actual search space.
		mean, momentum, breakout := true, true, true
		req.EnableWMean, req.EnableWMomentum, req.EnableWBreakout = &mean, &momentum, &breakout
	}
	req.ContinuousMode = strings.ToLower(strings.TrimSpace(req.ContinuousMode))
	if req.ContinuousMode != "" && req.ContinuousIterations == 0 && !req.ContinuousUnlimited {
		req.ContinuousIterations = 2
	}
	req.TraceMode = ga.NormalizeTraceMode(req.TraceMode)
	req.FixedParamKeys = ga.NormalizeFixedParamKeys(req.FixedParamKeys)
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
	if req.MarketRegionEnabled && req.MarketRegionMaxThresholds < 0 {
		return errors.New("market_region_max_thresholds must not be negative")
	}
	if req.ResearchDatasetID > 0 {
		dataset, err := s.loadResearchDataset(ctx, req.ResearchDatasetID)
		if err != nil {
			return err
		}
		if len(dataset.indicators) > 0 && strings.TrimSpace(dataset.record.IndicatorAlgorithm) == "" {
			return errors.New("此研究資料集含參考指標，但尚未指定已確認的指標演算法，因此不能開始參數搜尋")
		}
	}
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
	if boolValue(req.LongTermFilterEnabled) {
		if req.Interval != "1d" {
			return errors.New("長週期風險濾網只支援日 K，請改用日 K 或關閉濾網")
		}
		if req.LongTermFilterMonths <= 0 {
			return errors.New("N 月線長度必須大於 0")
		}
	}
	if req.TrainStartMs > 0 && req.TrainEndMs > 0 && req.TrainStartMs > req.TrainEndMs {
		return errors.New("train_start_ms must be earlier than train_end_ms")
	}
	if err := validateCostRate("fee_rate", req.FeeRate); err != nil {
		return err
	}
	if err := validateCostRate("spread_rate", req.SpreadRate); err != nil {
		return err
	}
	if req.InitialCapital != SearchInitialUSDT {
		return fmt.Errorf("initial_capital must be %.0f", float64(SearchInitialUSDT))
	}
	if req.MonthlyDCA != nil && *req.MonthlyDCA < 0 {
		return errors.New("monthly_dca must be zero or positive")
	}
	if !req.MarketRegionEnabled && !boolValue(req.EnableWMean) && !boolValue(req.EnableWMomentum) && !boolValue(req.EnableWBreakout) {
		return errors.New("at least one signal must be enabled")
	}
	if req.TradePenalty < 0 {
		return errors.New("trade_penalty must be zero or positive")
	}
	if req.TradePenalty > 1 {
		return errors.New("trade_penalty is too large")
	}
	if len(req.FixedParamKeys) > 0 && req.SeedGeneID == 0 {
		return errors.New("seed_gene_id is required when fixed_param_keys is not empty")
	}
	switch req.ContinuousMode {
	case "", "standardized_best", "random", "initial_seed":
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

// resolveMultiMarkets makes the selected collection canonical before the task
// is persisted.  The first canonical item is used only for legacy task fields;
// the complete collection is the actual evaluation and reservation identity.
func (s *Service) resolveMultiMarkets(ctx context.Context, req *CreateTaskRequest) error {
	if !req.MultiMarketEnabled {
		req.multiMarkets = nil
		return nil
	}
	selections := append([]MultiMarketSelection(nil), req.MultiMarketSelections...)
	if len(selections) == 0 {
		// Backward compatibility for tasks created before per-market dates were
		// added: an id-only selection means that market's complete data range.
		for _, id := range req.MultiMarketInstrumentIDs {
			selections = append(selections, MultiMarketSelection{InstrumentID: id, UseAllData: true})
		}
	}
	seen := make(map[string]bool, len(selections))
	unique := make([]MultiMarketSelection, 0, len(selections))
	for _, selection := range selections {
		selection.InstrumentID = strings.TrimSpace(selection.InstrumentID)
		if selection.InstrumentID != "" && !seen[selection.InstrumentID] {
			seen[selection.InstrumentID] = true
			unique = append(unique, selection)
		}
	}
	if len(unique) < 2 {
		return errors.New("採用多行情時至少要選擇兩張行情")
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i].InstrumentID < unique[j].InstrumentID })
	markets := make([]ga.MarketScope, 0, len(unique))
	missing := make([]string, 0)
	for index, selection := range unique {
		instrument, err := s.instruments.ResolveInstrument(ctx, selection.InstrumentID, "", "")
		if err != nil {
			return err
		}
		if !supportsInterval(instrument.SupportedIntervals, req.Interval) {
			return fmt.Errorf("行情 %s 不支援週期 %s", instrument.DisplayName, req.Interval)
		}
		baseQuery := s.db.WithContext(ctx).Model(&saasstore.KLine{}).
			Where("instrument_id = ? AND source = ? AND symbol = ? AND interval = ?", instrument.ID, instrument.DataSource, instrument.Symbol, req.Interval)
		var available struct {
			StartTimeMs int64
			EndTimeMs   int64
		}
		var availableCount int64
		if err := baseQuery.Select("MIN(open_time) AS start_time_ms, MAX(open_time) AS end_time_ms").Scan(&available).Error; err != nil {
			return err
		}
		if err := baseQuery.Count(&availableCount).Error; err != nil {
			return err
		}
		query := baseQuery
		if !selection.UseAllData && (selection.StartTimeMs == 0 || selection.EndTimeMs == 0) {
			return fmt.Errorf("行情 %s 未使用全部資料時必須設定開始與結束日期", instrument.DisplayName)
		}
		if !selection.UseAllData && selection.StartTimeMs > 0 && selection.EndTimeMs > 0 && selection.StartTimeMs > selection.EndTimeMs {
			return fmt.Errorf("行情 %s 的開始日期不可晚於結束日期", instrument.DisplayName)
		}
		if !selection.UseAllData && selection.StartTimeMs > 0 {
			query = query.Where("open_time >= ?", selection.StartTimeMs)
		}
		if !selection.UseAllData && selection.EndTimeMs > 0 {
			query = query.Where("open_time <= ?", selection.EndTimeMs)
		}
		var bounds struct {
			StartTimeMs int64
			EndTimeMs   int64
		}
		if err := query.Select("MIN(open_time) AS start_time_ms, MAX(open_time) AS end_time_ms").Scan(&bounds).Error; err != nil {
			return err
		}
		var count int64
		if err := query.Count(&count).Error; err != nil {
			return err
		}
		if count == 0 || availableCount == 0 {
			missing = append(missing, instrument.DisplayName)
		}
		if !selection.UseAllData && availableCount > 0 {
			if utcDay(selection.StartTimeMs) < utcDay(available.StartTimeMs) || utcDay(selection.EndTimeMs) > utcDay(available.EndTimeMs) {
				return fmt.Errorf("行情 %s 的設定日期超出可用資料範圍 %s ～ %s",
					instrument.DisplayName,
					time.UnixMilli(available.StartTimeMs).UTC().Format("2006-01-02"),
					time.UnixMilli(available.EndTimeMs).UTC().Format("2006-01-02"))
			}
		}
		if selection.UseAllData {
			selection.StartTimeMs, selection.EndTimeMs = available.StartTimeMs, available.EndTimeMs
		}
		unique[index] = selection
		markets = append(markets, ga.MarketScope{InstrumentID: instrument.ID, Pair: instrument.Symbol, DataSource: instrument.DataSource, StartTimeMs: selection.StartTimeMs, EndTimeMs: selection.EndTimeMs})
	}
	if len(missing) > 0 {
		return fmt.Errorf("下列行情在指定訓練期與週期缺少資料：%s", strings.Join(missing, "、"))
	}
	req.MultiMarketSelections = unique
	req.MultiMarketInstrumentIDs = make([]string, 0, len(unique))
	for _, selection := range unique {
		req.MultiMarketInstrumentIDs = append(req.MultiMarketInstrumentIDs, selection.InstrumentID)
	}
	req.multiMarkets = markets
	// Make old task fields deterministic and prevent the primary picker from
	// splitting durable reservations for the identical market collection.
	req.InstrumentID, req.Pair, req.DataSource = markets[0].InstrumentID, markets[0].Pair, markets[0].DataSource
	return nil
}

func utcDay(value int64) int64 {
	if value <= 0 {
		return 0
	}
	return time.UnixMilli(value).UTC().Truncate(24 * time.Hour).Unix()
}

type taskResearchDataset struct {
	record     saasstore.ResearchDataset
	primary    saasstore.ResearchDatasetSeries
	indicators []saasstore.ResearchDatasetSeries
}

func (s *Service) loadResearchDataset(ctx context.Context, id uint) (taskResearchDataset, error) {
	var record saasstore.ResearchDataset
	if err := s.db.WithContext(ctx).First(&record, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return taskResearchDataset{}, fmt.Errorf("找不到研究資料集 #%d", id)
		}
		return taskResearchDataset{}, err
	}
	var rows []saasstore.ResearchDatasetSeries
	if err := s.db.WithContext(ctx).
		Where("dataset_id = ?", id).
		Order("role ASC, sort_order ASC, id ASC").
		Find(&rows).Error; err != nil {
		return taskResearchDataset{}, err
	}
	out := taskResearchDataset{record: record}
	for _, row := range rows {
		if row.Role == "primary" {
			out.primary = row
		} else {
			out.indicators = append(out.indicators, row)
		}
	}
	if out.primary.InstrumentID == "" {
		out.primary = saasstore.ResearchDatasetSeries{
			InstrumentID:          record.PrimaryInstrumentID,
			DataSource:            record.PrimaryDataSource,
			Symbol:                record.PrimarySymbol,
			Interval:              record.PrimaryInterval,
			MarketDataVersionID:   record.PrimaryMarketDataVersionID,
			MarketDataContentHash: record.PrimaryMarketDataContentHash,
		}
	}
	allSeries := append([]saasstore.ResearchDatasetSeries{out.primary}, out.indicators...)
	for _, series := range allSeries {
		if series.MarketDataVersionID == nil {
			continue
		}
		var version saasstore.MarketDataVersion
		if err := s.db.WithContext(ctx).Where(
			"id = ? AND output_instrument_id = ? AND interval = ? AND content_hash = ? AND status = ? AND integrity_status = ? AND published = true",
			*series.MarketDataVersionID, series.InstrumentID, series.Interval, series.MarketDataContentHash,
			marketversion.VersionStatusCompleted, marketversion.IntegrityValid,
		).First(&version).Error; err != nil {
			return taskResearchDataset{}, fmt.Errorf("研究資料集引用的行情版本已失效: %s %s", series.InstrumentID, series.Interval)
		}
	}
	return out, nil
}

func searchCosts(req CreateTaskRequest) quant.ExecutionCostConfig {
	costs := quant.ExecutionCostConfig{}
	if req.FeeRate != nil {
		costs.FeeRate = *req.FeeRate
	}
	if req.SpreadRate != nil {
		costs.SpreadRate = *req.SpreadRate
	}
	return quant.NormalizeExecutionCosts(costs)
}

func searchLongTermFilter(req CreateTaskRequest) backtestcore.LongTermFilterConfig {
	return backtestcore.LongTermFilterConfig{
		Enabled: boolValue(req.LongTermFilterEnabled),
		Months:  req.LongTermFilterMonths,
		Version: backtestcore.LongTermFilterVersion,
	}
}

func searchInitialCapital(req CreateTaskRequest) float64 {
	if req.InitialCapital > 0 {
		return req.InitialCapital
	}
	return SearchInitialUSDT
}

func searchMonthlyDCA(req CreateTaskRequest) float64 {
	if req.MonthlyDCA != nil && *req.MonthlyDCA > 0 {
		return *req.MonthlyDCA
	}
	return 0
}

func searchGeneOptions(req CreateTaskRequest) ga.GeneOptions {
	costs := searchCosts(req)
	return ga.GeneOptions{
		EvolveRebalanceThreshold:  req.EvolveRebalanceThreshold,
		EvolveForceFullThreshold:  req.EvolveForceFullThreshold,
		EvolveForceEmptyThreshold: req.EvolveForceEmptyThreshold,
		EvolveGamma:               req.EvolveGamma,
		EnableWMean:               boolValue(req.EnableWMean),
		EnableWMomentum:           boolValue(req.EnableWMomentum),
		EnableWBreakout:           boolValue(req.EnableWBreakout),
		PositionStructure:         normalizedPositionStructure(req.PositionStructure),
		FixedParamKeys:            req.FixedParamKeys,
		MarketRegionEnabled:       req.MarketRegionEnabled,
		MarketRegionMaxThresholds: req.MarketRegionMaxThresholds,
		DisableBeta:               req.MarketRegionEnabled,
		DisableDustFilter:         costs.FeeRate == 0 && costs.SpreadRate == 0,
		DisableWedgeMinimum:       normalizedPositionStructure(req.PositionStructure) == sigmoiddca.PositionStructureFloatingOnly,
		FixedGene:                 req.fixedGene,
	}
}

func searchTradePenalty(req CreateTaskRequest) float64 {
	if req.TradePenalty > 0 {
		return req.TradePenalty
	}
	return 0
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func normalizedPositionStructure(value string) string {
	if strings.TrimSpace(value) == "" {
		return sigmoiddca.PositionStructureFloatingOnly
	}
	return sigmoiddca.NormalizePositionStructure(value)
}

func applySearchCapital(req CreateTaskRequest, spawn *quant.SpawnPoint) {
	if spawn == nil {
		return
	}
	spawn.Policy.InitialUSDT = searchInitialCapital(req)
	spawn.Policy.MonthlyInjectUSDT = searchMonthlyDCA(req)
}

func validateCostRate(name string, value *float64) error {
	if value == nil {
		return nil
	}
	if *value < 0 {
		return fmt.Errorf("%s must be zero or positive", name)
	}
	if *value > 0.2 {
		return fmt.Errorf("%s is too large", name)
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
			InitialUSDT:       SearchInitialUSDT,
			MonthlyInjectUSDT: 0,
		},
		Risk: quant.RiskBounds{
			MaxDrawdownPct: 0.88,
			FeeRate:        0,
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
	spawn.Risk.MaxDrawdownPct = 0.50 + rng.Float64()*0.38
	return spawn
}
