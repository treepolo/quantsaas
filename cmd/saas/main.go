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
	"quantsaas/internal/saas/config"
	saascron "quantsaas/internal/saas/cron"
	"quantsaas/internal/saas/epoch"
	"quantsaas/internal/saas/ga"
	"quantsaas/internal/saas/instance"
	"quantsaas/internal/saas/marketdata"
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

	router := api.NewRouter(api.RouterDeps{
		Config:           cfg,
		DB:               db.DB,
		Redis:            redisClient,
		Auth:             authService,
		InstanceManager:  instanceManager,
		EpochService:     epochService,
		EvolutionHandler: evolutionHandler,
		AgentStatus:      hub,
		WSHandler:        hub.HandleConnection,
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

	waitForShutdown(logger, server, scheduler, hub)
}

func waitForShutdown(logger *zap.Logger, server *http.Server, scheduler *saascron.Scheduler, hub *ws.Hub) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	scheduler.Stop(ctx)
	hub.CloseAll()
	if err := server.Shutdown(ctx); err != nil {
		logger.Warn("http shutdown failed", zap.Error(err))
	}
}
