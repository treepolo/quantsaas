package computetask

import (
	"quantsaas/internal/saas/store"
	"sync/atomic"
	"time"
)

type liveComputeMonitor struct {
	planned       int64
	started       time.Time
	units         atomic.Int64
	stage         atomic.Value
	lastHeartbeat atomic.Int64
}

func newLiveComputeMonitor(planned int64) *liveComputeMonitor {
	monitor := &liveComputeMonitor{planned: planned, started: time.Now().UTC()}
	monitor.stage.Store("執行中")
	monitor.touch("")
	return monitor
}

func (m *liveComputeMonitor) snapshot() (int64, int64, float64, float64, string, string) {
	if m == nil {
		return 0, 0, 0, 0, "", ""
	}
	computed := m.units.Load()
	elapsed := time.Since(m.started).Seconds()
	rate := 0.0
	if elapsed > 0 {
		rate = float64(computed) / elapsed
	}
	remaining := 0.0
	if rate > 0 && m.planned > computed {
		remaining = float64(m.planned-computed) / rate
	}
	stage, _ := m.stage.Load().(string)
	last := time.Unix(0, m.lastHeartbeat.Load()).UTC().Format(time.RFC3339)
	return computed, m.planned, rate, remaining, stage, last
}

func (m *liveComputeMonitor) touch(stage string) {
	if m == nil {
		return
	}
	if stage != "" {
		m.stage.Store(stage)
	}
	m.lastHeartbeat.Store(time.Now().UTC().UnixNano())
}

func (s *Service) ensureLiveCompute(task store.ComputeTask) {
	if !task.ComputeMonitorEnabled {
		return
	}
	s.computeMu.Lock()
	if s.computes[task.ID] == nil {
		s.computes[task.ID] = newLiveComputeMonitor(task.EstimatedUnits)
	}
	s.computeMu.Unlock()
}

func (s *Service) countLiveCompute(taskID uint) func(int64) {
	s.computeMu.RLock()
	monitor := s.computes[taskID]
	s.computeMu.RUnlock()
	if monitor == nil {
		return func(int64) {}
	}
	return func(delta int64) {
		monitor.touch("")
		if delta > 0 {
			monitor.units.Add(delta)
		}
	}
}

func (s *Service) heartbeatLiveCompute(taskID uint, stage string) {
	s.computeMu.RLock()
	monitor := s.computes[taskID]
	s.computeMu.RUnlock()
	if monitor != nil {
		monitor.touch(stage)
	}
}

func (s *Service) clearLiveCompute(taskID uint) {
	s.computeMu.Lock()
	delete(s.computes, taskID)
	s.computeMu.Unlock()
}
