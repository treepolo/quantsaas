package epoch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/saas/ga"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	TaskStatusRunning = "running"
	TaskStatusDone    = "completed"
	TaskStatusFailed  = "failed"
)

type Service struct {
	db          *gorm.DB
	engine      *ga.EvolutionEngine
	logger      *zap.Logger
	mu          sync.Mutex
	currentTask *saasstore.EvolutionTask
	traceMu     sync.Mutex
	traces      map[uint]*traceBuffer
	traceModes  map[uint]ga.TraceMode
}

type CreateTaskRequest struct {
	StrategyID     string            `json:"strategy_id"`
	Pair           string            `json:"pair"`
	Interval       string            `json:"interval"`
	PopSize        int               `json:"pop_size"`
	MaxGenerations int               `json:"max_generations"`
	SpawnMode      string            `json:"spawn_mode"`
	SpawnPoint     *quant.SpawnPoint `json:"spawn_point"`
	TestMode       bool              `json:"test_mode"`
	TraceMode      ga.TraceMode      `json:"trace_mode"`
}

func NewService(db *gorm.DB, engine *ga.EvolutionEngine, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		db:         db,
		engine:     engine,
		logger:     logger,
		traces:     map[uint]*traceBuffer{},
		traceModes: map[uint]ga.TraceMode{},
	}
}

func (s *Service) CreateAndRunTask(ctx context.Context, req CreateTaskRequest) (*saasstore.EvolutionTask, error) {
	s.mu.Lock()
	if s.currentTask != nil {
		s.mu.Unlock()
		return nil, errors.New("已有進化任務正在執行")
	}

	req = normalizeRequest(req)
	if err := validateRequest(req); err != nil {
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
		StrategyID: req.StrategyID,
		Status:     TaskStatusRunning,
		Progress:   0,
		Config:     saasstore.JSONB(configRaw),
		StartedAt:  &now,
	}
	if err := s.db.WithContext(ctx).Create(task).Error; err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.currentTask = task
	s.initTrace(task.ID, req.TraceMode)
	s.mu.Unlock()

	go s.runEpoch(task.ID, req, spawn)
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

func (s *Service) runEpoch(taskID uint, req CreateTaskRequest, spawn *quant.SpawnPoint) {
	ctx := context.Background()
	result, err := s.engine.RunEpoch(ctx, ga.EpochConfig{
		Pair:               req.Pair,
		Interval:           req.Interval,
		PopSize:            req.PopSize,
		MaxGenerations:     req.MaxGenerations,
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
		updates["status"] = TaskStatusFailed
		updates["error_message"] = err.Error()
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
	s.mu.Unlock()
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
		spawn, err := s.loadChampionSpawn(ctx, req.StrategyID)
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

func (s *Service) loadChampionSpawn(ctx context.Context, strategyID string) (*quant.SpawnPoint, error) {
	var record saasstore.GeneRecord
	if err := s.db.WithContext(ctx).
		Where("strategy_id = ? AND role = ?", strategyID, saasstore.GeneRoleChampion).
		Order("activated_at DESC NULLS LAST, created_at DESC").
		First(&record).Error; err != nil {
		return nil, err
	}
	params := sigmoiddca.ParseParamsFromParamPack([]byte(record.ParamPack))
	return &params.Spawn, nil
}

func normalizeRequest(req CreateTaskRequest) CreateTaskRequest {
	if req.StrategyID == "" {
		req.StrategyID = sigmoiddca.StrategyID
	}
	if req.Pair == "" {
		req.Pair = "BTCUSDT"
	}
	if req.Interval == "" {
		req.Interval = "1d"
	}
	if req.SpawnMode == "" {
		req.SpawnMode = "inherit"
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

func validateRequest(req CreateTaskRequest) error {
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
