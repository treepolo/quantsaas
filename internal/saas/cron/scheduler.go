package cron

import (
	"context"

	"quantsaas/internal/saas/instance"

	robfigcron "github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

type Scheduler struct {
	cron    *robfigcron.Cron
	manager *instance.Manager
	logger  *zap.Logger
}

func NewScheduler(manager *instance.Manager, logger *zap.Logger) *Scheduler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Scheduler{
		cron:    robfigcron.New(robfigcron.WithSeconds()),
		manager: manager,
		logger:  logger,
	}
}

func (s *Scheduler) Start() error {
	_, err := s.cron.AddFunc("0 * * * * *", s.scanRunningInstances)
	if err != nil {
		return err
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
