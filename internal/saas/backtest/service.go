package backtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"quantsaas/internal/quant"
	"quantsaas/internal/saas/ga"
	"quantsaas/internal/saas/marketdata"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"gorm.io/gorm"
)

const (
	SourceChampion  = "champion"
	SourceCandidate = "candidate"
	SourceCustom    = "custom"
)

var ErrNotFound = errors.New("找不到回測紀錄")

type Service struct {
	db *gorm.DB
}

type CreateRequest struct {
	StrategyID    string            `json:"strategy_id"`
	InstanceID    uint              `json:"instance_id"`
	InstrumentID  string            `json:"instrument_id"`
	DataSource    string            `json:"data_source"`
	ExecutionMode string            `json:"execution_mode"`
	StartTimeMs   int64             `json:"start_time_ms"`
	EndTimeMs     int64             `json:"end_time_ms"`
	Pair          string            `json:"pair"`
	Symbol        string            `json:"symbol"`
	Interval      string            `json:"interval"`
	Source        string            `json:"source"`
	CandidateID   uint              `json:"candidate_id"`
	GenomeID      uint              `json:"genome_id"`
	CustomParams  json.RawMessage   `json:"custom_params"`
	SpawnPoint    *quant.SpawnPoint `json:"spawn_point"`
}

type EquitySnapshot struct {
	Time        string  `json:"time"`
	TotalAssets float64 `json:"total_assets"`
	Benchmark   float64 `json:"benchmark"`
}

type WindowResult struct {
	Window            string  `json:"window"`
	Score             float64 `json:"score"`
	TotalReturn       float64 `json:"total_return"`
	BenchmarkReturn   float64 `json:"benchmark_return"`
	Alpha             float64 `json:"alpha"`
	MaxDrawdown       float64 `json:"max_drawdown"`
	BenchmarkDrawdown float64 `json:"benchmark_drawdown"`
}

type Response struct {
	ID            uint               `json:"id"`
	Status        string             `json:"status"`
	StrategyID    string             `json:"strategy_id"`
	Symbol        string             `json:"symbol"`
	InstrumentID  string             `json:"instrument_id"`
	DataSource    string             `json:"data_source"`
	ExecutionMode string             `json:"execution_mode"`
	Interval      string             `json:"interval"`
	Source        string             `json:"source"`
	TotalReturn   float64            `json:"total_return"`
	Alpha         float64            `json:"alpha"`
	MaxDrawdown   float64            `json:"max_drawdown"`
	FinalEquity   float64            `json:"final_equity"`
	Benchmark     float64            `json:"benchmark"`
	NAV           []EquitySnapshot   `json:"nav"`
	Windows       map[string]float64 `json:"windows"`
	WindowDetails []WindowResult     `json:"window_details"`
	Error         string             `json:"error,omitempty"`
	CreatedAt     string             `json:"created_at,omitempty"`
	FinishedAt    string             `json:"finished_at,omitempty"`
}

type instanceConfig struct {
	InitialUSDT       float64 `json:"initial_usdt"`
	MonthlyInjectUSDT float64 `json:"monthly_inject_usdt"`
	ColdSealedBTC     float64 `json:"cold_sealed_btc"`
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Create(ctx context.Context, userID uint, req CreateRequest) (*Response, error) {
	req = normalizeRequest(req)
	if err := validateBasicRequest(req); err != nil {
		return nil, err
	}

	requestRaw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	var instanceID *uint
	if req.InstanceID != 0 {
		instanceID = &req.InstanceID
	}
	now := time.Now().UTC()
	run := saasstore.BacktestRun{
		UserID:        userID,
		StrategyID:    req.StrategyID,
		InstanceID:    instanceID,
		InstrumentID:  req.InstrumentID,
		DataSource:    req.DataSource,
		ExecutionMode: req.ExecutionMode,
		StartTimeMs:   req.StartTimeMs,
		EndTimeMs:     req.EndTimeMs,
		Symbol:        req.Symbol,
		Interval:      req.Interval,
		Source:        req.Source,
		Status:        saasstore.BacktestStatusRunning,
		Request:       saasstore.JSONB(requestRaw),
		StartedAt:     &now,
	}
	if err := s.db.WithContext(ctx).Create(&run).Error; err != nil {
		return nil, err
	}

	response, err := s.execute(ctx, userID, run.ID, req)
	finished := time.Now().UTC()
	updates := map[string]any{"finished_at": &finished}
	if err != nil {
		updates["status"] = saasstore.BacktestStatusFailed
		updates["error_message"] = err.Error()
		_ = s.db.WithContext(ctx).Model(&saasstore.BacktestRun{}).Where("id = ?", run.ID).Updates(updates).Error
		return nil, err
	}

	response.ID = run.ID
	response.Status = saasstore.BacktestStatusCompleted
	response.CreatedAt = run.CreatedAt.Format(time.RFC3339)
	response.FinishedAt = finished.Format(time.RFC3339)
	resultRaw, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	updates["status"] = saasstore.BacktestStatusCompleted
	updates["result"] = saasstore.JSONB(resultRaw)
	if err := s.db.WithContext(ctx).Model(&saasstore.BacktestRun{}).Where("id = ?", run.ID).Updates(updates).Error; err != nil {
		return nil, err
	}
	return response, nil
}

func (s *Service) Get(ctx context.Context, userID uint, id uint) (*Response, error) {
	var run saasstore.BacktestRun
	if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if run.Status != saasstore.BacktestStatusCompleted {
		return &Response{
			ID:            run.ID,
			Status:        run.Status,
			StrategyID:    run.StrategyID,
			Symbol:        run.Symbol,
			InstrumentID:  run.InstrumentID,
			DataSource:    run.DataSource,
			ExecutionMode: run.ExecutionMode,
			Interval:      run.Interval,
			Source:        run.Source,
			Error:         run.ErrorMessage,
			CreatedAt:     run.CreatedAt.Format(time.RFC3339),
		}, nil
	}

	var response Response
	if err := json.Unmarshal([]byte(run.Result), &response); err != nil {
		return nil, err
	}
	response.ID = run.ID
	response.Status = run.Status
	response.CreatedAt = run.CreatedAt.Format(time.RFC3339)
	if run.FinishedAt != nil {
		response.FinishedAt = run.FinishedAt.Format(time.RFC3339)
	}
	return &response, nil
}

func (s *Service) execute(ctx context.Context, userID uint, runID uint, req CreateRequest) (*Response, error) {
	params, err := s.resolveParams(ctx, userID, req)
	if err != nil {
		return nil, err
	}
	spawn := params.Spawn
	if err := normalizeSpawnPoint(&spawn); err != nil {
		return nil, err
	}

	bars, err := s.loadBars(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("尚未匯入 %s %s 的 K 線資料", req.Symbol, req.Interval)
	}

	path := ga.RunSigmoidDCAPathBacktestWithMode(bars, bars[0].OpenTime, req.Interval, req.ExecutionMode, params.Chromosome, &spawn)
	baseline := quant.SimulateGhostDCAFrom(bars, bars[0].OpenTime, quant.GhostDCAConfig{
		InitialUSDT:       spawn.Policy.InitialUSDT,
		MonthlyInjectUSDT: spawn.Policy.MonthlyInjectUSDT,
		UseOpenExecution:  req.ExecutionMode == marketdata.ExecutionModeCloseNextOpen,
	})
	alpha := path.Metrics.ROI - baseline.ROI
	windows, windowDetails := scoreWindows(bars, req.Interval, req.ExecutionMode, params.Chromosome, &spawn)

	return &Response{
		ID:            runID,
		Status:        saasstore.BacktestStatusCompleted,
		StrategyID:    req.StrategyID,
		Symbol:        req.Symbol,
		InstrumentID:  req.InstrumentID,
		DataSource:    req.DataSource,
		ExecutionMode: req.ExecutionMode,
		Interval:      req.Interval,
		Source:        req.Source,
		TotalReturn:   path.Metrics.ROI,
		Alpha:         alpha,
		MaxDrawdown:   path.Metrics.MaxDrawdown,
		FinalEquity:   path.Metrics.FinalEquity,
		Benchmark:     baseline.FinalEquity,
		NAV:           mergeNAV(path.NAV, baseline),
		Windows:       windows,
		WindowDetails: windowDetails,
	}, nil
}

func (s *Service) resolveParams(ctx context.Context, userID uint, req CreateRequest) (sigmoiddca.Params, error) {
	params := sigmoiddca.DefaultParams()
	params.Chromosome = quant.DefaultSeedChromosome

	switch req.Source {
	case SourceChampion:
		record, err := s.loadLatestGene(ctx, req, saasstore.GeneRoleChampion)
		if err == nil {
			params = sigmoiddca.ParseParamsFromParamPack([]byte(record.ParamPack))
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return params, err
		}
	case SourceCandidate:
		id := req.CandidateID
		if id == 0 {
			id = req.GenomeID
		}
		if id == 0 {
			return params, errors.New("候選參數回測需要指定基因 ID")
		}
		record, err := s.loadGeneByID(ctx, id)
		if err != nil {
			return params, err
		}
		params = sigmoiddca.ParseParamsFromParamPack([]byte(record.ParamPack))
	case SourceCustom:
		parsed, err := parseCustomParams(req.CustomParams)
		if err != nil {
			return params, err
		}
		params = parsed
	default:
		return params, fmt.Errorf("不支援的回測來源: %s", req.Source)
	}

	if req.InstanceID != 0 {
		instance, err := s.loadInstance(ctx, userID, req.InstanceID)
		if err != nil {
			return params, err
		}
		params.Spawn = overlayInstanceSpawn(params.Spawn, instance)
	}
	if req.SpawnPoint != nil {
		params.Spawn = *req.SpawnPoint
	}
	params.Chromosome = quant.ClampChromosome(params.Chromosome)
	return params, nil
}

func (s *Service) loadLatestGene(ctx context.Context, req CreateRequest, role string) (saasstore.GeneRecord, error) {
	var record saasstore.GeneRecord
	err := s.db.WithContext(ctx).
		Where("strategy_id = ? AND instrument_id = ? AND data_source = ? AND interval = ? AND execution_mode = ? AND role = ?",
			req.StrategyID, req.InstrumentID, req.DataSource, req.Interval, req.ExecutionMode, role).
		Order("activated_at DESC NULLS LAST, created_at DESC").
		First(&record).Error
	return record, err
}

func (s *Service) loadGeneByID(ctx context.Context, id uint) (saasstore.GeneRecord, error) {
	var record saasstore.GeneRecord
	err := s.db.WithContext(ctx).
		Where("id = ? AND role IN ?", id, []string{saasstore.GeneRoleChallenger, saasstore.GeneRoleChampion, saasstore.GeneRoleRetired}).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return record, fmt.Errorf("找不到可回測的候選參數")
	}
	return record, err
}

func (s *Service) loadInstance(ctx context.Context, userID uint, id uint) (saasstore.StrategyInstance, error) {
	var instance saasstore.StrategyInstance
	err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&instance).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return instance, fmt.Errorf("找不到策略實例")
	}
	return instance, err
}

func (s *Service) loadBars(ctx context.Context, req CreateRequest) ([]quant.Bar, error) {
	var rows []saasstore.KLine
	query := s.db.WithContext(ctx).
		Where("symbol = ? AND interval = ? AND instrument_id = ? AND source = ?", req.Symbol, req.Interval, req.InstrumentID, req.DataSource)
	if req.StartTimeMs > 0 {
		query = query.Where("open_time >= ?", req.StartTimeMs)
	}
	if req.EndTimeMs > 0 {
		query = query.Where("open_time <= ?", req.EndTimeMs)
	}
	if err := query.Order("open_time ASC").Find(&rows).Error; err != nil {
		return nil, err
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

func normalizeRequest(req CreateRequest) CreateRequest {
	if req.StrategyID == "" {
		req.StrategyID = sigmoiddca.StrategyID
	}
	if req.Symbol == "" {
		req.Symbol = req.Pair
	}
	instrument, err := marketdata.ResolveInstrument(req.InstrumentID, req.Symbol, req.DataSource)
	if err == nil {
		req.InstrumentID = instrument.ID
		req.Symbol = instrument.Symbol
		req.DataSource = instrument.DataSource
	}
	if req.Symbol == "" {
		req.Symbol = marketdata.DefaultSymbol
		req.InstrumentID = marketdata.InstrumentBTCUSDT
		req.DataSource = marketdata.DataSourceBinance
	}
	req.Symbol = strings.ToUpper(strings.TrimSpace(req.Symbol))
	if req.Interval == "" {
		req.Interval = "1d"
	}
	req.ExecutionMode = marketdata.NormalizeExecutionMode(req.ExecutionMode)
	if req.Source == "" {
		req.Source = SourceChampion
	}
	req.Source = strings.ToLower(strings.TrimSpace(req.Source))
	return req
}

func validateBasicRequest(req CreateRequest) error {
	instrument, err := marketdata.ResolveInstrument(req.InstrumentID, req.Symbol, req.DataSource)
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
		return errors.New("收盤前 10 分鐘模式需要歷史快照回測路徑，目前尚未開放，不能用日 K 假裝回測")
	}
	if req.StartTimeMs > 0 && req.EndTimeMs > 0 && req.StartTimeMs > req.EndTimeMs {
		return errors.New("start_time_ms must be earlier than end_time_ms")
	}
	if req.StrategyID != sigmoiddca.StrategyID {
		return fmt.Errorf("尚不支援的策略: %s", req.StrategyID)
	}
	switch req.Source {
	case SourceChampion, SourceCandidate, SourceCustom:
	default:
		return fmt.Errorf("不支援的回測來源: %s", req.Source)
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

func parseCustomParams(raw json.RawMessage) (sigmoiddca.Params, error) {
	params := sigmoiddca.DefaultParams()
	if len(raw) == 0 || string(raw) == "null" {
		return params, errors.New("自訂參數不可為空")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return params, fmt.Errorf("自訂參數 JSON 格式不正確")
	}
	if _, ok := envelope["sigmoid_dca_config"]; ok {
		if err := json.Unmarshal(raw, &params); err != nil {
			return params, fmt.Errorf("自訂參數內容不正確")
		}
		params.Chromosome = quant.ClampChromosome(params.Chromosome)
		return params, nil
	}
	var chromosome quant.Chromosome
	if err := json.Unmarshal(raw, &chromosome); err != nil {
		return params, fmt.Errorf("自訂參數內容不正確")
	}
	params.Chromosome = quant.ClampChromosome(chromosome)
	return params, nil
}

func overlayInstanceSpawn(spawn quant.SpawnPoint, instance saasstore.StrategyInstance) quant.SpawnPoint {
	var cfg instanceConfig
	if err := json.Unmarshal([]byte(instance.Config), &cfg); err != nil {
		return spawn
	}
	if cfg.InitialUSDT > 0 {
		spawn.Policy.InitialUSDT = cfg.InitialUSDT
	}
	if cfg.MonthlyInjectUSDT > 0 {
		spawn.Policy.MonthlyInjectUSDT = cfg.MonthlyInjectUSDT
	}
	if cfg.ColdSealedBTC > 0 {
		spawn.Policy.ColdSealedBTC = cfg.ColdSealedBTC
	}
	return spawn
}

func normalizeSpawnPoint(spawn *quant.SpawnPoint) error {
	if spawn.Policy.InitialUSDT <= 0 {
		spawn.Policy.InitialUSDT = 1000
	}
	if spawn.Policy.MonthlyInjectUSDT < 0 {
		return errors.New("月度投入不可為負數")
	}
	if spawn.Policy.MonthlyInjectUSDT == 0 {
		spawn.Policy.MonthlyInjectUSDT = 100
	}
	if spawn.Policy.ColdSealedBTC < 0 {
		return errors.New("封存資產不可為負數")
	}
	if spawn.Risk.MaxDrawdownPct <= 0 {
		spawn.Risk.MaxDrawdownPct = 0.88
	}
	if spawn.Risk.LotStep <= 0 {
		spawn.Risk.LotStep = 0.000001
	}
	if spawn.Risk.LotMin <= 0 {
		spawn.Risk.LotMin = 0.00001
	}
	return nil
}

func scoreWindows(bars []quant.Bar, interval string, executionMode string, chromosome quant.Chromosome, spawn *quant.SpawnPoint) (map[string]float64, []WindowResult) {
	windows := quant.BuildCrucibleWindows(bars, 1200)
	scores := make(map[string]float64, len(windows))
	details := make([]WindowResult, 0, len(windows))
	for _, window := range windows {
		metrics := ga.RunSigmoidDCASingleBacktestWithMode(window.Bars, window.EvalStartMs, interval, executionMode, chromosome, spawn)
		baseline := quant.SimulateGhostDCAFrom(window.Bars, window.EvalStartMs, quant.GhostDCAConfig{
			InitialUSDT:       spawn.Policy.InitialUSDT,
			MonthlyInjectUSDT: spawn.Policy.MonthlyInjectUSDT,
			UseOpenExecution:  executionMode == marketdata.ExecutionModeCloseNextOpen,
		})
		alpha := metrics.ROI - baseline.ROI
		score := alpha - 1.5*math.Max(0, metrics.MaxDrawdown-baseline.MaxDrawdown)
		if metrics.MaxDrawdown >= 0.88 {
			score = ga.FatalFitnessScore
		}
		scores[window.Label] = score
		details = append(details, WindowResult{
			Window:            window.Label,
			Score:             score,
			TotalReturn:       metrics.ROI,
			BenchmarkReturn:   baseline.ROI,
			Alpha:             alpha,
			MaxDrawdown:       metrics.MaxDrawdown,
			BenchmarkDrawdown: baseline.MaxDrawdown,
		})
	}
	return scores, details
}

func mergeNAV(strategy []ga.BacktestPoint, baseline quant.GhostDCAResult) []EquitySnapshot {
	byTime := make(map[int64]float64, len(baseline.Times))
	for i, ts := range baseline.Times {
		if i < len(baseline.NAV) {
			byTime[ts] = baseline.NAV[i]
		}
	}
	points := make([]EquitySnapshot, 0, len(strategy))
	for _, item := range strategy {
		benchmark, ok := byTime[item.TimeMs]
		if !ok {
			continue
		}
		points = append(points, EquitySnapshot{
			Time:        time.UnixMilli(item.TimeMs).UTC().Format(time.RFC3339),
			TotalAssets: item.TotalEquity,
			Benchmark:   benchmark,
		})
	}
	return points
}
