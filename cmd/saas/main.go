package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"quantsaas/internal/saas/api"
	"quantsaas/internal/saas/auth"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/computetask"
	"quantsaas/internal/saas/config"
	controlresearchsvc "quantsaas/internal/saas/controlresearch"
	saascron "quantsaas/internal/saas/cron"
	dynamicparamsvc "quantsaas/internal/saas/dynamicparam"
	"quantsaas/internal/saas/epoch"
	"quantsaas/internal/saas/ga"
	"quantsaas/internal/saas/instance"
	"quantsaas/internal/saas/marketdata"
	parameterresearchsvc "quantsaas/internal/saas/parameterresearch"
	robustnesssvc "quantsaas/internal/saas/robustness"
	"quantsaas/internal/saas/store"
	"quantsaas/internal/saas/ws"

	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	cfg, err := config.Load("config.yaml")
	if err != nil {
		logger.Fatal("load config failed", zap.Error(err))
	}

	db, err := store.NewDB(cfg.Database)
	if err != nil {
		logger.Fatal("init db failed", zap.Error(err))
	}
	sqlDB, _ := db.DB.DB()
	defer sqlDB.Close()

	ctx := context.Background()
	if err := marketdata.SeedResearchInstruments(ctx, db.DB); err != nil {
		logger.Fatal("seed research instruments failed", zap.Error(err))
	}
	redisClient, err := store.NewRedis(ctx, cfg.Redis)
	if err != nil {
		logger.Fatal("init redis failed", zap.Error(err))
	}
	defer redisClient.Close()

	authService, err := auth.NewService(cfg.JWT)
	if err != nil {
		logger.Fatal("init auth failed", zap.Error(err))
	}

	hub := ws.NewHub(db.DB, authService, logger)
	instanceManager := instance.NewManager(db.DB, nil, hub, logger)
	marketDataService := marketdata.NewService(db.DB, nil)
	genomeStore := ga.NewGormGenomeStore(db.DB)
	evolutionEngine := ga.NewEvolutionEngine(ga.NewSigmoidDCAEvolvable(), genomeStore)
	epochService := epoch.NewService(db.DB, evolutionEngine, logger)
	evolutionHandler := api.NewEvolutionHandler(cfg.AppRole, db.DB, redisClient, epochService)
	computeRegistry := computetask.NewRegistry()
	backtestService := backtest.NewService(db.DB)
	for _, executor := range []computetask.Executor{
		robustnesssvc.NewPointExecutor(db.DB),
		dynamicparamsvc.NewTrainExecutor(db.DB),
		dynamicparamsvc.NewMaterializeExecutor(db.DB, backtestService),
		controlresearchsvc.NewExecutor(db.DB, backtestService),
		parameterresearchsvc.NewSurrogateExecutor(),
		marketdata.NewRecompositionPreviewExecutor(marketDataService),
		marketdata.NewRecompositionExpandExecutor(marketDataService),
		marketdata.NewRecompositionAuditExecutor(marketDataService),
		marketdata.NewRecompositionPublishExecutor(marketDataService),
	} {
		if err := computeRegistry.Register(executor); err != nil {
			logger.Fatal("register market-data compute executor failed", zap.Error(err))
		}
	}
	computeOptions := computetask.Options{
		Workers: cfg.Compute.Workers, SoftItemLimit: cfg.Compute.SoftItemLimit,
		HardItemLimit: cfg.Compute.HardItemLimit,
		LeaseDuration: time.Duration(cfg.Compute.LeaseSeconds) * time.Second,
		PollInterval:  time.Duration(cfg.Compute.PollMilliseconds) * time.Millisecond,
	}
	computeTasks, err := computetask.NewService(db.DB, computeRegistry, computeOptions, logger)
	if err != nil {
		logger.Fatal("init compute task service failed", zap.Error(err))
	}
	marketDataService.SetComputeTasks(computeTasks)
	robustnessStudies := robustnesssvc.NewService(db.DB, computeTasks)
	dynamicParameterStudies := dynamicparamsvc.NewService(db.DB, computeTasks)
	parameterResearch := parameterresearchsvc.NewService(db.DB, computeTasks, robustnessStudies)
	controlResearch := controlresearchsvc.NewService(db.DB, computeTasks, parameterResearch)
	if err := computeTasks.Start(); err != nil {
		logger.Fatal("start compute task service failed", zap.Error(err))
	}

	router := api.NewRouter(api.RouterDeps{
		Config:            cfg,
		DB:                db.DB,
		Redis:             redisClient,
		Auth:              authService,
		InstanceManager:   instanceManager,
		EpochService:      epochService,
		EvolutionHandler:  evolutionHandler,
		ComputeTasks:      computeTasks,
		MarketData:        marketDataService,
		Robustness:        robustnessStudies,
		DynamicParameters: dynamicParameterStudies,
		ParameterResearch: parameterResearch,
		ControlResearch:   controlResearch,
		AgentStatus:       hub,
		WSHandler:         hub.HandleConnection,
	})

	scheduler := saascron.NewScheduler(instanceManager, marketDataService, logger)
	if err := scheduler.Start(); err != nil {
		logger.Fatal("start cron failed", zap.Error(err))
	}

	server := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      router,
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeoutSeconds) * time.Second,
	}

	go func() {
		logger.Info("SaaS server listening", zap.String("addr", cfg.Server.Addr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("http server failed", zap.Error(err))
		}
	}()

	waitForShutdown(logger, server, scheduler, computeTasks, hub)
}

func waitForShutdown(logger *zap.Logger, server *http.Server, scheduler *saascron.Scheduler, computeTasks *computetask.Service, hub *ws.Hub) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	scheduler.Stop(ctx)
	if err := computeTasks.Shutdown(ctx); err != nil {
		logger.Warn("compute task shutdown failed", zap.Error(err))
	}
	hub.CloseAll()
	if err := server.Shutdown(ctx); err != nil {
		logger.Warn("http shutdown failed", zap.Error(err))
	}
}
