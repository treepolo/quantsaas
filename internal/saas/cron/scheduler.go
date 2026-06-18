package cron

import (
	"context"
	"time"

	"quantsaas/internal/saas/instance"
	"quantsaas/internal/saas/marketdata"

	robfigcron "github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

type Scheduler struct {
	cron       *robfigcron.Cron
	manager    *instance.Manager
	marketData *marketdata.Service
	logger     *zap.Logger
}

func NewScheduler(manager *instance.Manager, marketData *marketdata.Service, logger *zap.Logger) *Scheduler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Scheduler{
		cron:       robfigcron.New(robfigcron.WithSeconds()),
		manager:    manager,
		marketData: marketData,
		logger:     logger,
	}
}

func (s *Scheduler) Start() error {
	_, err := s.cron.AddFunc("0 * * * * *", s.scanRunningInstances)
	if err != nil {
		return err
	}
	if s.marketData != nil {
		if _, err := s.cron.AddFunc("0 15 */6 * * *", s.updateDailyMarketData); err != nil {
			return err
		}
		go s.updateDailyMarketData()
	}
	s.cron.Start()
	return nil
}

func (s *Scheduler) Stop(ctx context.Context) {
	stopCtx := s.cron.Stop()
	select {
	case <-stopCtx.Done():
	case <-ctx.Done():
		s.logger.Warn("cron shutdown timed out", zap.Error(ctx.Err()))
	}
}

func (s *Scheduler) updateDailyMarketData() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	results, err := s.marketData.UpdateLatest(ctx)
	if err != nil {
		s.logger.Warn("market data update failed", zap.Error(err))
		return
	}
	for _, result := range results {
		if result.Error != "" {
			s.logger.Warn("market data interval update failed", zap.String("instrument_id", result.InstrumentID), zap.String("interval", result.Interval), zap.String("error", result.Error))
		}
	}
}

func (s *Scheduler) scanRunningInstances() {
	ctx := context.Background()
	ids, err := s.manager.RunningInstanceIDs(ctx)
	if err != nil {
		s.logger.Warn("list running instances failed", zap.Error(err))
		return
	}
	for _, id := range ids {
		id := id
		go func() {
			if err := s.manager.Tick(ctx, id); err != nil {
				s.logger.Warn("instance tick failed", zap.Uint("instance_id", id), zap.Error(err))
				_ = s.manager.MarkError(ctx, id, err)
			}
		}()
	}
}
