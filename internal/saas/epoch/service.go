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
	TaskStatusDone    = "done"
	TaskStatusFailed  = "failed"
)

type Service struct {
	db          *gorm.DB
	engine      *ga.EvolutionEngine
	logger      *zap.Logger
	mu          sync.Mutex
	currentTask *saasstore.EvolutionTask
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
}

func NewService(db *gorm.DB, engine *ga.EvolutionEngine, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{db: db, engine: engine, logger: logger}
}

func (s *Service) CreateAndRunTask(ctx context.Context, req CreateTaskRequest) (*saasstore.EvolutionTask, error) {
	s.mu.Lock()
	if s.currentTask != nil {
		s.mu.Unlock()
		return nil, errors.New("已有進化任務正在執行")
	}

	req = normalizeRequest(req)
	spawn, err := s.resolveSpawnPoint(ctx, req)
	if err != nil {
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
	s.mu.Unlock()

	go s.runEpoch(task.ID, req, spawn)
	return task, nil
}

func (s *Service) CurrentTask() *saasstore.EvolutionTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentTask
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
		OnProgress: func(progress ga.EpochProgress) {
			_ = s.db.Model(&saasstore.EvolutionTask{}).
				Where("id = ?", taskID).
				Updates(map[string]any{
					"progress": float64(progress.Generation+1) / float64(max(1, req.MaxGenerations)),
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
		raw, _ := json.Marshal(result)
		updates["status"] = TaskStatusDone
		updates["progress"] = 1.0
		updates["result"] = saasstore.JSONB(raw)
	}
	_ = s.db.Model(&saasstore.EvolutionTask{}).Where("id = ?", taskID).Updates(updates).Error

	s.mu.Lock()
	s.currentTask = nil
	s.mu.Unlock()
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

func randomSpawnPoint() quant.SpawnPoint {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	spawn := *defaultSpawnPoint()
	spawn.Policy.InitialUSDT = 500 + rng.Float64()*4500
	spawn.Policy.MonthlyInjectUSDT = 50 + rng.Float64()*450
	spawn.Risk.MaxDrawdownPct = 0.50 + rng.Float64()*0.38
	return spawn
}
