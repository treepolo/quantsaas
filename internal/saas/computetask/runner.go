package computetask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	compute "quantsaas/internal/compute"
	saasstore "quantsaas/internal/saas/store"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type claimedItem struct {
	task       saasstore.ComputeTask
	item       saasstore.ComputeTaskItem
	executor   Executor
	leaseOwner string
}

type cacheReservation struct {
	entry  saasstore.ComputeCacheEntry
	reused bool
	wait   bool
}

// Start recovers expired leases and starts the bounded database-backed worker
// loop. It is intentionally idempotent; one Service instance is started once.
func (s *Service) Start() error {
	if s.stopping.Load() {
		return ErrServiceUnavailable
	}
	if !s.started.CompareAndSwap(false, true) {
		return nil
	}
	if err := s.recoverExpired(s.rootCtx); err != nil {
		s.started.Store(false)
		return err
	}
	if err := s.validateRecoverableTasks(s.rootCtx); err != nil {
		s.started.Store(false)
		return err
	}
	s.wg.Add(1)
	go s.scheduler()
	return nil
}

// Shutdown stops accepting new local work, interrupts running executors and
// waits for their checkpoints to be persisted. Interrupted items return to
// pending instead of being recorded as a user cancellation.
func (s *Service) Shutdown(ctx context.Context) error {
	if !s.started.Load() {
		return nil
	}
	if s.stopping.CompareAndSwap(false, true) {
		s.stopRoot()
		s.cancelAllExecutions()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) scheduler() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.options.PollInterval)
	defer ticker.Stop()
	s.dispatch()
	for {
		select {
		case <-s.rootCtx.Done():
			return
		case <-ticker.C:
			if err := s.resolveBlockedDependencies(s.rootCtx); err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Warn("resolve blocked compute stages", zap.Error(err))
			}
			s.dispatch()
		}
	}
}

func (s *Service) dispatch() {
	for !s.stopping.Load() {
		select {
		case s.sem <- struct{}{}:
		case <-s.rootCtx.Done():
			return
		default:
			return
		}
		claim, err := s.claimNext(s.rootCtx)
		if err != nil {
			<-s.sem
			if !errors.Is(err, context.Canceled) {
				s.logger.Error("claim compute task item", zap.Error(err))
			}
			return
		}
		if claim == nil {
			<-s.sem
			return
		}
		s.wg.Add(1)
		go func(work *claimedItem) {
			defer s.wg.Done()
			defer func() { <-s.sem }()
			s.executeClaim(work)
		}(claim)
	}
}

func (s *Service) claimNext(ctx context.Context) (*claimedItem, error) {
	var claim *claimedItem
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&saasstore.ComputeTaskItem{}).
			Select("compute_task_items.*").
			Joins("JOIN compute_tasks ON compute_tasks.id = compute_task_items.compute_task_id").
			Where("compute_tasks.status IN ?", []string{compute.TaskStatusQueued, compute.TaskStatusRunning}).
			Where("compute_tasks.cancel_requested_at IS NULL").
			Where("(compute_task_items.status = ? AND (compute_task_items.lease_expires_at IS NULL OR compute_task_items.lease_expires_at <= ?)) OR (compute_task_items.status = ? AND (compute_task_items.lease_expires_at IS NULL OR compute_task_items.lease_expires_at <= ?))", compute.ItemStatusPending, now, compute.ItemStatusRunning, now).
			Where(`NOT EXISTS (
				SELECT 1 FROM compute_task_dependencies dependency
				JOIN compute_tasks prerequisite ON prerequisite.id = dependency.depends_on_task_id
				WHERE dependency.compute_task_id = compute_tasks.id
				AND prerequisite.status <> ?
			)`, compute.TaskStatusCompleted).
			Order("compute_tasks.created_at ASC, compute_tasks.stage_order ASC, compute_task_items.item_index ASC")
		if tx.Dialector.Name() == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED", Table: clause.Table{Name: "compute_task_items"}})
		}
		var item saasstore.ComputeTaskItem
		found := query.Limit(1).Find(&item)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected == 0 {
			return nil
		}
		var task saasstore.ComputeTask
		taskQuery := tx.Where("id = ?", item.ComputeTaskID)
		if tx.Dialector.Name() == "postgres" {
			taskQuery = taskQuery.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := taskQuery.First(&task).Error; err != nil {
			return err
		}
		executor, err := s.runtimeExecutor(task)
		if err != nil {
			return s.invalidateTaskTx(tx, &task, err.Error(), now)
		}
		canonicalInput, err := compute.CanonicalRawJSON(item.Input)
		if err != nil || compute.HashBytes(canonicalInput) != item.InputHash {
			return s.invalidateTaskTx(tx, &task, "manifest item input hash mismatch", now)
		}
		leaseOwner := fmt.Sprintf("%s:item-%d:%d", s.workerID, item.ID, now.UnixNano())
		leaseExpiresAt := now.Add(s.options.LeaseDuration)
		updates := map[string]any{
			"status":           compute.ItemStatusRunning,
			"lease_owner":      leaseOwner,
			"lease_expires_at": leaseExpiresAt,
			"failed_at":        nil,
			"cancelled_at":     nil,
			"error_message":    "",
		}
		if item.StartedAt == nil {
			updates["started_at"] = now
		}
		if err := tx.Model(&saasstore.ComputeTaskItem{}).Where("id = ?", item.ID).Updates(updates).Error; err != nil {
			return err
		}
		taskUpdates := map[string]any{"status": compute.TaskStatusRunning}
		if task.StartedAt == nil {
			taskUpdates["started_at"] = now
		}
		if err := tx.Model(&saasstore.ComputeTask{}).Where("id = ?", task.ID).Updates(taskUpdates).Error; err != nil {
			return err
		}
		item.Status = compute.ItemStatusRunning
		item.LeaseOwner = leaseOwner
		item.LeaseExpiresAt = &leaseExpiresAt
		if item.StartedAt == nil {
			item.StartedAt = &now
		}
		task.Status = compute.TaskStatusRunning
		if task.StartedAt == nil {
			task.StartedAt = &now
		}
		claim = &claimedItem{task: task, item: item, executor: executor, leaseOwner: leaseOwner}
		return nil
	})
	return claim, err
}

func (s *Service) executeClaim(claim *claimedItem) {
	ctx, cancel := context.WithCancel(s.rootCtx)
	s.registerCancel(claim.task.ID, claim.item.ID, cancel)
	defer func() {
		cancel()
		s.unregisterCancel(claim.task.ID, claim.item.ID)
	}()

	reservation, err := s.reserveCache(ctx, claim)
	if err != nil {
		s.finishWithError(claim, nil, err)
		return
	}
	if reservation.wait {
		if err := s.releaseWaitingItem(ctx, claim, reservation.entry.LeaseExpiresAt); err != nil {
			s.logger.Warn("release duplicate compute item", zap.Uint("item_id", claim.item.ID), zap.Error(err))
		}
		return
	}
	if reservation.reused {
		if err := s.refreshTaskTree(context.Background(), claim.task.ID); err != nil {
			s.logger.Warn("refresh cached compute item", zap.Uint("item_id", claim.item.ID), zap.Error(err))
		}
		return
	}

	cacheEntry := reservation.entry
	heartbeatDone := make(chan struct{})
	var heartbeatWG sync.WaitGroup
	heartbeatWG.Add(1)
	go func() {
		defer heartbeatWG.Done()
		s.heartbeat(ctx, cancel, claim, cacheEntry.ID, heartbeatDone)
	}()
	// Keep both leases alive until the terminal database write finishes. Stopping
	// the heartbeat immediately after Execute returns leaves a lease-expiry gap
	// while completeExecution is waiting for a busy database; a waiting item can
	// otherwise take over the cache key and repeat the same computation.
	defer func() {
		close(heartbeatDone)
		heartbeatWG.Wait()
	}()
	report := func(reportCtx context.Context, update ProgressUpdate) error {
		return s.reportProgress(reportCtx, claim, cacheEntry.ID, update)
	}
	execution := Execution{
		UserID: claim.task.UserID, TaskID: claim.task.ID, ItemID: claim.item.ID,
		ItemKey: claim.item.ItemKey, Input: append(json.RawMessage(nil), claim.item.Input...),
		Checkpoint:  append(json.RawMessage(nil), claim.item.Checkpoint...),
		RNG:         compute.RNGSpec{Algorithm: claim.task.RNGAlgorithm, Version: claim.task.RNGVersion, RootSeed: claim.task.RootSeed},
		RNGPosition: claim.item.RNGPosition,
		Report:      report,
	}
	result, executeErr := safeExecute(ctx, claim.executor, execution)
	if executeErr != nil {
		s.finishWithError(claim, &cacheEntry, executeErr)
		return
	}
	if err := s.completeExecution(context.Background(), claim, cacheEntry, result); err != nil {
		s.finishWithError(claim, &cacheEntry, err)
	}
}

func safeExecute(ctx context.Context, executor Executor, execution Execution) (result json.RawMessage, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("compute executor panic: %v\n%s", recovered, debug.Stack())
		}
	}()
	return executor.Execute(ctx, execution)
}

func (s *Service) reserveCache(ctx context.Context, claim *claimedItem) (cacheReservation, error) {
	activeKey := fmt.Sprintf("%d|%s", claim.task.UserID, claim.item.CacheKey)
	for attempt := 0; attempt < 4; attempt++ {
		var reservation cacheReservation
		retry := false
		now := time.Now().UTC()
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var existing saasstore.ComputeCacheEntry
			query := tx.Where("active_key = ?", activeKey)
			if tx.Dialector.Name() == "postgres" {
				query = query.Clauses(clause.Locking{Strength: "UPDATE"})
			}
			err := query.First(&existing).Error
			if err == nil {
				if existing.Status == compute.CacheStatusCompleted && s.validCompletedCache(ctx, claim.task.UserID, existing, claim.executor) {
					updates := map[string]any{
						"status": compute.ItemStatusCached, "progress": 1,
						"cache_entry_id": existing.ID, "result": existing.Result,
						"result_hash": existing.ContentHash, "completed_at": existing.CompletedAt,
						"lease_owner": "", "lease_expires_at": nil, "error_message": "",
					}
					updated := tx.Model(&saasstore.ComputeTaskItem{}).
						Where("id = ? AND lease_owner = ? AND status = ?", claim.item.ID, claim.leaseOwner, compute.ItemStatusRunning).
						Updates(updates)
					if updated.Error != nil {
						return updated.Error
					}
					if updated.RowsAffected != 1 {
						return ErrInvalidState
					}
					reservation = cacheReservation{entry: existing, reused: true}
					return nil
				}
				compatible := existing.SchemaVersion == compute.CacheEntrySchemaVersion &&
					existing.ExecutorType == claim.task.ExecutorType && existing.ExecutorVersion == claim.task.ExecutorVersion &&
					existing.ResultSchemaVersion == claim.task.ResultSchemaVersion && existing.InputHash == claim.item.InputHash
				if existing.Status == compute.CacheStatusRunning && compatible {
					if existing.LeaseExpiresAt != nil && existing.LeaseExpiresAt.After(now) && existing.LeaseOwner != claim.leaseOwner {
						reservation = cacheReservation{entry: existing, wait: true}
						return nil
					}
					leaseExpiresAt := now.Add(s.options.LeaseDuration)
					updates := map[string]any{
						"lease_owner": claim.leaseOwner, "lease_expires_at": leaseExpiresAt,
						"source_task_item_id": claim.item.ID, "attempt": gorm.Expr("attempt + 1"),
						"error_message": "",
					}
					if err := tx.Model(&saasstore.ComputeCacheEntry{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
						return err
					}
					if err := tx.Model(&saasstore.ComputeTaskItem{}).Where("id = ? AND lease_owner = ?", claim.item.ID, claim.leaseOwner).
						Updates(map[string]any{"cache_entry_id": existing.ID, "attempt": gorm.Expr("attempt + 1")}).Error; err != nil {
						return err
					}
					existing.LeaseOwner = claim.leaseOwner
					existing.LeaseExpiresAt = &leaseExpiresAt
					reservation = cacheReservation{entry: existing}
					return nil
				}
				invalidatedAt := now
				if err := tx.Model(&saasstore.ComputeCacheEntry{}).Where("id = ?", existing.ID).Updates(map[string]any{
					"status": compute.CacheStatusInvalidated, "active_key": nil,
					"invalidated_at": invalidatedAt, "lease_owner": "", "lease_expires_at": nil,
					"error_message": "cache identity or content validation failed",
				}).Error; err != nil {
					return err
				}
				retry = true
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			leaseExpiresAt := now.Add(s.options.LeaseDuration)
			sourceItemID := claim.item.ID
			candidate := saasstore.ComputeCacheEntry{
				OwnerUserID: claim.task.UserID, CacheKey: claim.item.CacheKey, ActiveKey: &activeKey,
				SchemaVersion: compute.CacheEntrySchemaVersion,
				ExecutorType:  claim.task.ExecutorType, ExecutorVersion: claim.task.ExecutorVersion,
				ResultSchemaVersion: claim.task.ResultSchemaVersion, InputHash: claim.item.InputHash,
				Status: compute.CacheStatusRunning, Result: saasstore.JSONB(`{}`), SourceTaskItemID: &sourceItemID,
				Attempt: 1, LeaseOwner: claim.leaseOwner, LeaseExpiresAt: &leaseExpiresAt,
			}
			created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "active_key"}}, DoNothing: true}).Create(&candidate)
			if created.Error != nil {
				return created.Error
			}
			if created.RowsAffected == 0 {
				retry = true
				return nil
			}
			if err := tx.Model(&saasstore.ComputeTaskItem{}).Where("id = ? AND lease_owner = ?", claim.item.ID, claim.leaseOwner).
				Updates(map[string]any{"cache_entry_id": candidate.ID, "attempt": gorm.Expr("attempt + 1")}).Error; err != nil {
				return err
			}
			reservation = cacheReservation{entry: candidate}
			return nil
		})
		if err != nil {
			return cacheReservation{}, err
		}
		if retry {
			continue
		}
		return reservation, nil
	}
	return cacheReservation{}, fmt.Errorf("reserve compute cache after concurrent conflicts: %w", ErrInvalidState)
}

func (s *Service) validCompletedCache(ctx context.Context, userID uint, entry saasstore.ComputeCacheEntry, executor Executor) bool {
	if entry.Status != compute.CacheStatusCompleted || entry.SchemaVersion != compute.CacheEntrySchemaVersion || entry.CompletedAt == nil {
		return false
	}
	canonical, err := compute.CanonicalRawJSON(entry.Result)
	if err != nil || compute.HashBytes(canonical) != entry.ContentHash {
		return false
	}
	if validator, ok := executor.(CachedResultValidator); ok {
		return validator.ValidateCachedResult(ctx, userID, json.RawMessage(canonical)) == nil
	}
	return true
}

func (s *Service) releaseWaitingItem(ctx context.Context, claim *claimedItem, cacheLeaseExpiresAt *time.Time) error {
	recheckAt := time.Now().UTC().Add(max(s.options.PollInterval*4, 50*time.Millisecond))
	if cacheLeaseExpiresAt != nil && cacheLeaseExpiresAt.Before(recheckAt) {
		recheckAt = *cacheLeaseExpiresAt
	}
	result := s.db.WithContext(ctx).Model(&saasstore.ComputeTaskItem{}).
		Where("id = ? AND lease_owner = ? AND status = ?", claim.item.ID, claim.leaseOwner, compute.ItemStatusRunning).
		Updates(map[string]any{"status": compute.ItemStatusPending, "lease_owner": "", "lease_expires_at": recheckAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrInvalidState
	}
	return s.refreshTaskTree(ctx, claim.task.ID)
}

func (s *Service) heartbeat(ctx context.Context, cancel context.CancelFunc, claim *claimedItem, cacheEntryID uint, done <-chan struct{}) {
	interval := s.options.LeaseDuration / 3
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			now := time.Now().UTC()
			expiresAt := now.Add(s.options.LeaseDuration)
			itemResult := s.db.WithContext(ctx).Model(&saasstore.ComputeTaskItem{}).
				Where("id = ? AND lease_owner = ? AND status = ?", claim.item.ID, claim.leaseOwner, compute.ItemStatusRunning).
				Update("lease_expires_at", expiresAt)
			if itemResult.Error != nil || itemResult.RowsAffected != 1 {
				cancel()
				return
			}
			cacheResult := s.db.WithContext(ctx).Model(&saasstore.ComputeCacheEntry{}).
				Where("id = ? AND lease_owner = ? AND status = ?", cacheEntryID, claim.leaseOwner, compute.CacheStatusRunning).
				Update("lease_expires_at", expiresAt)
			if cacheResult.Error != nil || cacheResult.RowsAffected != 1 {
				cancel()
				return
			}
			var state struct {
				CancelRequestedAt *time.Time
			}
			if err := s.db.WithContext(ctx).Model(&saasstore.ComputeTask{}).Select("cancel_requested_at").Where("id = ?", claim.task.ID).
				Take(&state).Error; err != nil || state.CancelRequestedAt != nil {
				cancel()
				return
			}
		}
	}
}

func (s *Service) reportProgress(ctx context.Context, claim *claimedItem, cacheEntryID uint, update ProgressUpdate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if update.Progress < 0 || update.Progress > 1 {
		return fmt.Errorf("progress must be between 0 and 1")
	}
	var checkpoint json.RawMessage
	checkpointHash := ""
	if len(update.Checkpoint) > 0 {
		canonical, err := compute.CanonicalRawJSON(update.Checkpoint)
		if err != nil {
			return fmt.Errorf("canonicalize compute checkpoint: %w", err)
		}
		checkpoint = canonical
		checkpointHash = compute.HashBytes(canonical)
	}
	expiresAt := time.Now().UTC().Add(s.options.LeaseDuration)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		itemUpdates := map[string]any{
			"progress": update.Progress, "rng_position": update.RNGPosition,
			"lease_expires_at": expiresAt,
		}
		if len(checkpoint) > 0 {
			itemUpdates["checkpoint"] = saasstore.JSONB(checkpoint)
			itemUpdates["checkpoint_hash"] = checkpointHash
		}
		updated := tx.Model(&saasstore.ComputeTaskItem{}).
			Where("id = ? AND lease_owner = ? AND status = ?", claim.item.ID, claim.leaseOwner, compute.ItemStatusRunning).
			Updates(itemUpdates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrInvalidState
		}
		cacheUpdated := tx.Model(&saasstore.ComputeCacheEntry{}).
			Where("id = ? AND lease_owner = ? AND status = ?", cacheEntryID, claim.leaseOwner, compute.CacheStatusRunning).
			Update("lease_expires_at", expiresAt)
		if cacheUpdated.Error != nil {
			return cacheUpdated.Error
		}
		if cacheUpdated.RowsAffected != 1 {
			return ErrInvalidState
		}
		taskUpdates := map[string]any{
			"rng_position": update.RNGPosition,
			"progress":     gorm.Expr("(SELECT COALESCE(SUM(progress), 0) / NULLIF(COUNT(*), 0) FROM compute_task_items WHERE compute_task_id = ?)", claim.task.ID),
		}
		if len(checkpoint) > 0 {
			taskUpdates["checkpoint"] = saasstore.JSONB(checkpoint)
			taskUpdates["checkpoint_hash"] = checkpointHash
		}
		return tx.Model(&saasstore.ComputeTask{}).Where("id = ?", claim.task.ID).Updates(taskUpdates).Error
	})
}

func (s *Service) completeExecution(ctx context.Context, claim *claimedItem, cacheEntry saasstore.ComputeCacheEntry, rawResult json.RawMessage) error {
	if len(rawResult) == 0 {
		return fmt.Errorf("compute executor returned an empty result")
	}
	canonical, err := compute.CanonicalRawJSON(rawResult)
	if err != nil {
		return fmt.Errorf("canonicalize compute result: %w", err)
	}
	contentHash := compute.HashBytes(canonical)
	now := time.Now().UTC()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cacheUpdated := tx.Model(&saasstore.ComputeCacheEntry{}).
			Where("id = ? AND lease_owner = ? AND status = ?", cacheEntry.ID, claim.leaseOwner, compute.CacheStatusRunning).
			Updates(map[string]any{
				"status": compute.CacheStatusCompleted, "result": saasstore.JSONB(canonical),
				"content_hash": contentHash, "completed_at": now, "error_message": "",
				"lease_owner": "", "lease_expires_at": nil, "source_task_item_id": claim.item.ID,
			})
		if cacheUpdated.Error != nil {
			return cacheUpdated.Error
		}
		if cacheUpdated.RowsAffected != 1 {
			return fmt.Errorf("compute cache lease lost: %w", ErrInvalidState)
		}
		itemUpdated := tx.Model(&saasstore.ComputeTaskItem{}).
			Where("id = ? AND lease_owner = ? AND status = ?", claim.item.ID, claim.leaseOwner, compute.ItemStatusRunning).
			Updates(map[string]any{
				"status": compute.ItemStatusCompleted, "progress": 1,
				"cache_entry_id": cacheEntry.ID, "result": saasstore.JSONB(canonical),
				"result_hash": contentHash, "completed_at": now, "error_message": "",
				"lease_owner": "", "lease_expires_at": nil,
			})
		if itemUpdated.Error != nil {
			return itemUpdated.Error
		}
		if itemUpdated.RowsAffected != 1 {
			return fmt.Errorf("compute task item lease lost: %w", ErrInvalidState)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.refreshTaskTree(ctx, claim.task.ID)
}

func (s *Service) finishWithError(claim *claimedItem, cacheEntry *saasstore.ComputeCacheEntry, executionErr error) {
	ctx := context.Background()
	message := strings.TrimSpace(executionErr.Error())
	if message == "" {
		message = "compute executor failed"
	}
	var task saasstore.ComputeTask
	_ = s.db.WithContext(ctx).Select("id", "cancel_requested_at").First(&task, claim.task.ID).Error
	userCancelled := task.CancelRequestedAt != nil
	shutdown := s.stopping.Load() && !userCancelled
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if cacheEntry != nil && cacheEntry.ID != 0 {
			cacheUpdates := map[string]any{"lease_owner": "", "lease_expires_at": nil}
			if shutdown {
				cacheUpdates["lease_expires_at"] = now
			} else {
				cacheUpdates["status"] = compute.CacheStatusFailed
				cacheUpdates["active_key"] = nil
				cacheUpdates["error_message"] = message
			}
			if err := tx.Model(&saasstore.ComputeCacheEntry{}).
				Where("id = ? AND lease_owner = ? AND status = ?", cacheEntry.ID, claim.leaseOwner, compute.CacheStatusRunning).
				Updates(cacheUpdates).Error; err != nil {
				return err
			}
		}
		itemUpdates := map[string]any{"lease_owner": "", "lease_expires_at": nil}
		switch {
		case shutdown:
			itemUpdates["status"] = compute.ItemStatusPending
			itemUpdates["error_message"] = ""
		case userCancelled || errors.Is(executionErr, context.Canceled):
			itemUpdates["status"] = compute.ItemStatusCancelled
			itemUpdates["cancelled_at"] = now
			itemUpdates["error_message"] = ""
		default:
			itemUpdates["status"] = compute.ItemStatusFailed
			itemUpdates["failed_at"] = now
			itemUpdates["error_message"] = message
		}
		return tx.Model(&saasstore.ComputeTaskItem{}).
			Where("id = ? AND lease_owner = ? AND status = ?", claim.item.ID, claim.leaseOwner, compute.ItemStatusRunning).
			Updates(itemUpdates).Error
	})
	if err != nil {
		s.logger.Error("finish failed compute item", zap.Uint("item_id", claim.item.ID), zap.Error(err))
		return
	}
	if err := s.refreshTaskTree(ctx, claim.task.ID); err != nil {
		s.logger.Warn("refresh failed compute item", zap.Uint("item_id", claim.item.ID), zap.Error(err))
	}
}

func (s *Service) recoverExpired(ctx context.Context) error {
	now := time.Now().UTC()
	var cancelledTasks []saasstore.ComputeTask
	if err := s.db.WithContext(ctx).Where("kind <> ? AND cancel_requested_at IS NOT NULL AND status IN ?", compute.TaskKindComposite, []string{compute.TaskStatusQueued, compute.TaskStatusRunning}).Find(&cancelledTasks).Error; err != nil {
		return err
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(cancelledTasks) > 0 {
			taskIDs := make([]uint, 0, len(cancelledTasks))
			for _, task := range cancelledTasks {
				taskIDs = append(taskIDs, task.ID)
			}
			if err := tx.Model(&saasstore.ComputeTaskItem{}).
				Where("compute_task_id IN ? AND status IN ?", taskIDs, []string{compute.ItemStatusPending, compute.ItemStatusRunning}).
				Updates(map[string]any{"status": compute.ItemStatusCancelled, "cancelled_at": now, "lease_owner": "", "lease_expires_at": nil}).Error; err != nil {
				return err
			}
			if err := tx.Model(&saasstore.ComputeCacheEntry{}).
				Where("status = ? AND source_task_item_id IN (SELECT id FROM compute_task_items WHERE compute_task_id IN ?)", compute.CacheStatusRunning, taskIDs).
				Updates(map[string]any{"status": compute.CacheStatusFailed, "active_key": nil, "error_message": "cancelled", "lease_owner": "", "lease_expires_at": nil}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&saasstore.ComputeTaskItem{}).
			Where("status = ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", compute.ItemStatusRunning, now).
			Updates(map[string]any{"status": compute.ItemStatusPending, "lease_owner": "", "lease_expires_at": nil}).Error; err != nil {
			return err
		}
		if err := tx.Model(&saasstore.ComputeCacheEntry{}).
			Where("status = ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", compute.CacheStatusRunning, now).
			Updates(map[string]any{"lease_owner": "", "lease_expires_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&saasstore.ComputeTask{}).
			Where("status = ? AND NOT EXISTS (SELECT 1 FROM compute_task_items WHERE compute_task_items.compute_task_id = compute_tasks.id AND compute_task_items.status = ? AND compute_task_items.lease_expires_at > ?)", compute.TaskStatusRunning, compute.ItemStatusRunning, now).
			Update("status", compute.TaskStatusQueued).Error
	})
	if err != nil {
		return err
	}
	for _, task := range cancelledTasks {
		if err := s.refreshAtomicTask(ctx, task.ID); err != nil {
			return err
		}
		if task.ParentTaskID != nil {
			if err := s.refreshCompositeTask(ctx, *task.ParentTaskID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) validateRecoverableTasks(ctx context.Context) error {
	var tasks []saasstore.ComputeTask
	if err := s.db.WithContext(ctx).Where("kind <> ? AND status IN ?", compute.TaskKindComposite, []string{compute.TaskStatusQueued, compute.TaskStatusRunning}).Find(&tasks).Error; err != nil {
		return err
	}
	for _, task := range tasks {
		if err := s.validateStoredTask(ctx, task); err != nil {
			s.invalidateTask(ctx, task.ID, err)
			if task.ParentTaskID != nil {
				if refreshErr := s.refreshCompositeTask(ctx, *task.ParentTaskID); refreshErr != nil {
					return refreshErr
				}
			}
		}
	}
	return nil
}

func (s *Service) runtimeExecutor(task saasstore.ComputeTask) (Executor, error) {
	if task.TaskSchemaVersion != compute.TaskSchemaVersion || task.LifecycleVersion != compute.LifecycleVersion || task.ManifestVersion != compute.ManifestSchemaVersion {
		return nil, ErrVersionMismatch
	}
	executor, ok := s.registry.Get(task.ExecutorType)
	if !ok {
		return nil, ErrUnknownExecutor
	}
	descriptor := executor.Descriptor()
	if descriptor.Type != task.ExecutorType || descriptor.Version != task.ExecutorVersion || descriptor.ResultSchemaVersion != task.ResultSchemaVersion {
		return nil, ErrVersionMismatch
	}
	return executor, nil
}

func (s *Service) invalidateTaskTx(tx *gorm.DB, task *saasstore.ComputeTask, reason string, now time.Time) error {
	if err := tx.Model(&saasstore.ComputeTaskItem{}).Where("compute_task_id = ? AND status IN ?", task.ID, []string{compute.ItemStatusPending, compute.ItemStatusRunning}).Updates(map[string]any{
		"status": compute.ItemStatusFailed, "failed_at": now, "error_message": reason,
		"lease_owner": "", "lease_expires_at": nil,
	}).Error; err != nil {
		return err
	}
	return tx.Model(&saasstore.ComputeTask{}).Where("id = ?", task.ID).Updates(map[string]any{
		"status": compute.TaskStatusInvalidated, "active_key": nil, "invalidated_at": now, "error_message": reason,
	}).Error
}

func (s *Service) registerCancel(taskID uint, itemID uint, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancels[taskID] == nil {
		s.cancels[taskID] = make(map[uint]context.CancelFunc)
	}
	s.cancels[taskID][itemID] = cancel
}

func (s *Service) unregisterCancel(taskID uint, itemID uint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels[taskID], itemID)
	if len(s.cancels[taskID]) == 0 {
		delete(s.cancels, taskID)
	}
}

func (s *Service) cancelTaskExecutions(taskIDs []uint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, taskID := range taskIDs {
		for _, cancel := range s.cancels[taskID] {
			cancel()
		}
	}
}

func (s *Service) cancelAllExecutions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, items := range s.cancels {
		for _, cancel := range items {
			cancel()
		}
	}
}

type itemAggregate struct {
	Status   string
	Count    int
	Progress float64
}

func (s *Service) refreshTaskTree(ctx context.Context, taskID uint) error {
	var relation saasstore.ComputeTask
	if err := s.db.WithContext(ctx).Select("id", "parent_task_id").Where("id = ?", taskID).Take(&relation).Error; err != nil {
		return err
	}
	if err := s.refreshAtomicTask(ctx, taskID); err != nil {
		return err
	}
	if relation.ParentTaskID != nil {
		return s.refreshCompositeTask(ctx, *relation.ParentTaskID)
	}
	return nil
}

func (s *Service) refreshAtomicTask(ctx context.Context, taskID uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task saasstore.ComputeTask
		if err := tx.Where("id = ?", taskID).First(&task).Error; err != nil {
			return err
		}
		if task.Kind == compute.TaskKindComposite {
			return nil
		}
		var rows []itemAggregate
		if err := tx.Model(&saasstore.ComputeTaskItem{}).
			Select("status, COUNT(*) AS count, COALESCE(SUM(progress), 0) AS progress").
			Where("compute_task_id = ?", taskID).Group("status").Scan(&rows).Error; err != nil {
			return err
		}
		counts := compute.ItemCounts{Total: task.TotalItems}
		progressSum := 0.0
		lastError := ""
		for _, row := range rows {
			progressSum += row.Progress
			switch row.Status {
			case compute.ItemStatusPending:
				counts.Pending += row.Count
			case compute.ItemStatusRunning:
				counts.Running += row.Count
			case compute.ItemStatusCompleted:
				counts.Completed += row.Count
			case compute.ItemStatusCached:
				counts.Cached += row.Count
			case compute.ItemStatusFailed:
				counts.Failed += row.Count
			case compute.ItemStatusCancelled:
				counts.Cancelled += row.Count
			}
		}
		if counts.Failed > 0 {
			_ = tx.Model(&saasstore.ComputeTaskItem{}).Where("compute_task_id = ? AND status = ?", taskID, compute.ItemStatusFailed).
				Order("item_index ASC").Limit(1).Pluck("error_message", &lastError).Error
		}
		status := compute.DeriveTaskStatus(counts, task.StartedAt != nil, task.CancelRequestedAt != nil)
		progress := 0.0
		if counts.Total > 0 {
			progress = progressSum / float64(counts.Total)
			if progress > 1 {
				progress = 1
			}
		}
		now := time.Now().UTC()
		updates := map[string]any{
			"valid_result_count": counts.Valid(), "failed_count": counts.Failed,
			"missing_count": counts.Missing(), "cancelled_count": counts.Cancelled,
			"cache_hit_count": counts.Cached, "new_item_count": counts.Total - counts.Cached,
			"progress": progress, "status": status, "error_message": lastError,
		}
		if status == compute.TaskStatusCompleted || status == compute.TaskStatusFailed || status == compute.TaskStatusPartial || status == compute.TaskStatusCancelled || status == compute.TaskStatusInvalidated {
			updates["completed_at"] = now
		} else {
			updates["completed_at"] = nil
		}
		if status == compute.TaskStatusCancelled {
			updates["cancelled_at"] = now
		}
		return tx.Model(&saasstore.ComputeTask{}).Where("id = ?", taskID).Updates(updates).Error
	})
}

func (s *Service) refreshCompositeTask(ctx context.Context, taskID uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var root saasstore.ComputeTask
		if err := tx.Where("id = ? AND kind = ?", taskID, compute.TaskKindComposite).First(&root).Error; err != nil {
			return err
		}
		var children []saasstore.ComputeTask
		if err := tx.Where("parent_task_id = ?", taskID).Order("stage_order ASC, id ASC").Find(&children).Error; err != nil {
			return err
		}
		statuses := make([]string, 0, len(children))
		totalItems, estimatedUnits, unknownUnits := 0, int64(0), 0
		cacheHits, newItems, valid, failed, missing, cancelled := 0, 0, 0, 0, 0, 0
		weightedProgress := 0.0
		activeChildren := 0
		started := root.StartedAt != nil
		for _, child := range children {
			statuses = append(statuses, child.Status)
			totalItems += child.TotalItems
			estimatedUnits += child.EstimatedUnits
			unknownUnits += child.UnknownUnitItems
			cacheHits += child.CacheHitCount
			newItems += child.NewItemCount
			valid += child.ValidResultCount
			failed += child.FailedCount
			missing += child.MissingCount
			cancelled += child.CancelledCount
			weightedProgress += child.Progress * float64(child.TotalItems)
			if child.Status == compute.TaskStatusQueued || child.Status == compute.TaskStatusRunning {
				activeChildren++
			}
			if child.StartedAt != nil {
				started = true
			}
		}
		status := compute.DeriveCompositeStatus(statuses)
		if root.CancelRequestedAt != nil {
			if activeChildren > 0 {
				status = compute.TaskStatusRunning
			} else {
				status = compute.TaskStatusCancelled
			}
		}
		progress := 0.0
		if totalItems > 0 {
			progress = weightedProgress / float64(totalItems)
		}
		now := time.Now().UTC()
		updates := map[string]any{
			"total_items": totalItems, "estimated_units": estimatedUnits, "unknown_unit_items": unknownUnits,
			"cache_hit_count": cacheHits, "new_item_count": newItems,
			"valid_result_count": valid, "failed_count": failed, "missing_count": missing,
			"cancelled_count": cancelled, "progress": progress, "status": status,
		}
		if started && root.StartedAt == nil {
			updates["started_at"] = now
		}
		if compute.IsTerminal(status) || status == compute.TaskStatusPartial {
			updates["completed_at"] = now
		} else {
			updates["completed_at"] = nil
		}
		if status == compute.TaskStatusCancelled {
			updates["cancelled_at"] = now
		}
		return tx.Model(&saasstore.ComputeTask{}).Where("id = ?", taskID).Updates(updates).Error
	})
}

func (s *Service) resolveBlockedDependencies(ctx context.Context) error {
	var blockedIDs []uint
	err := s.db.WithContext(ctx).Model(&saasstore.ComputeTask{}).
		Distinct("compute_tasks.id").
		Joins("JOIN compute_task_dependencies dependency ON dependency.compute_task_id = compute_tasks.id").
		Joins("JOIN compute_tasks prerequisite ON prerequisite.id = dependency.depends_on_task_id").
		Where("compute_tasks.status = ?", compute.TaskStatusQueued).
		Where("prerequisite.status IN ?", []string{compute.TaskStatusFailed, compute.TaskStatusPartial, compute.TaskStatusCancelled, compute.TaskStatusInvalidated}).
		Pluck("compute_tasks.id", &blockedIDs).Error
	if err != nil {
		return err
	}
	for _, taskID := range blockedIDs {
		now := time.Now().UTC()
		if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&saasstore.ComputeTaskItem{}).
				Where("compute_task_id = ? AND status = ?", taskID, compute.ItemStatusPending).
				Updates(map[string]any{"status": compute.ItemStatusFailed, "failed_at": now, "error_message": ErrDependencyPending.Error()}).Error; err != nil {
				return err
			}
			return tx.Model(&saasstore.ComputeTask{}).Where("id = ? AND status = ?", taskID, compute.TaskStatusQueued).
				Updates(map[string]any{"status": compute.TaskStatusFailed, "completed_at": now, "error_message": ErrDependencyPending.Error()}).Error
		}); err != nil {
			return err
		}
		if err := s.refreshTaskTree(ctx, taskID); err != nil {
			return err
		}
	}
	return nil
}
