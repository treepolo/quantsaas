package computetask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
)

func (s *Service) StartTask(ctx context.Context, userID uint, taskID uint) (*TaskDescriptor, error) {
	if !s.started.Load() || s.stopping.Load() {
		return nil, ErrServiceUnavailable
	}
	var task saasstore.ComputeTask
	if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAccessNotFound
		}
		return nil, err
	}
	if task.Status == compute.TaskStatusCompleted || task.Status == compute.TaskStatusQueued || task.Status == compute.TaskStatusRunning {
		return s.Get(ctx, userID, taskID)
	}
	if task.Kind == compute.TaskKindComposite {
		return nil, ErrInvalidState
	}
	if err := s.validateStoredTask(ctx, task); err != nil {
		s.invalidateTask(ctx, task.ID, err)
		return nil, err
	}
	if task.Status != compute.TaskStatusPlanned && task.Status != compute.TaskStatusPartial {
		return nil, ErrInvalidState
	}
	if task.Kind == compute.TaskKindStage {
		ready, err := s.dependenciesCompleted(ctx, task.ID)
		if err != nil {
			return nil, err
		}
		if !ready {
			return nil, ErrDependencyPending
		}
	}
	var pending int64
	if err := s.db.WithContext(ctx).Model(&saasstore.ComputeTaskItem{}).
		Where("compute_task_id = ? AND status = ?", task.ID, compute.ItemStatusPending).Count(&pending).Error; err != nil {
		return nil, err
	}
	if pending == 0 {
		if task.Status == compute.TaskStatusPlanned && task.CacheHitCount == task.TotalItems {
			now := time.Now().UTC()
			if err := s.db.WithContext(ctx).Model(&saasstore.ComputeTask{}).Where("id = ? AND user_id = ? AND status = ?", task.ID, userID, compute.TaskStatusPlanned).Updates(map[string]any{
				"status": compute.TaskStatusCompleted, "started_at": now, "completed_at": now,
				"cancelled_at": nil, "cancel_requested_at": nil, "error_message": "",
				"attempt": gorm.Expr("attempt + 1"),
			}).Error; err != nil {
				return nil, err
			}
			if task.ParentTaskID != nil {
				if err := s.refreshCompositeTask(ctx, *task.ParentTaskID); err != nil {
					return nil, err
				}
			}
			return s.Get(ctx, userID, taskID)
		}
		return nil, ErrInvalidState
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Model(&saasstore.ComputeTask{}).Where("id = ? AND user_id = ?", task.ID, userID).Updates(map[string]any{
		"status": compute.TaskStatusQueued, "started_at": now, "completed_at": nil,
		"cancelled_at": nil, "cancel_requested_at": nil, "error_message": "",
		"attempt": gorm.Expr("attempt + 1"),
	}).Error; err != nil {
		return nil, err
	}
	if task.ParentTaskID != nil {
		if err := s.refreshCompositeTask(ctx, *task.ParentTaskID); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, userID, taskID)
}

func (s *Service) Cancel(ctx context.Context, userID uint, taskID uint) (*TaskDescriptor, error) {
	var task saasstore.ComputeTask
	if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAccessNotFound
		}
		return nil, err
	}
	if task.Status == compute.TaskStatusCompleted || task.Status == compute.TaskStatusInvalidated {
		return nil, ErrInvalidState
	}
	taskIDs := []uint{task.ID}
	if task.Kind == compute.TaskKindComposite {
		var childIDs []uint
		if err := s.db.WithContext(ctx).Model(&saasstore.ComputeTask{}).Where("parent_task_id = ? AND user_id = ?", task.ID, userID).
			Order("stage_order ASC, id ASC").Pluck("id", &childIDs).Error; err != nil {
			return nil, err
		}
		taskIDs = append(taskIDs, childIDs...)
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&saasstore.ComputeTask{}).Where("id IN ? AND status NOT IN ?", taskIDs, []string{compute.TaskStatusCompleted, compute.TaskStatusInvalidated}).Updates(map[string]any{
			"cancel_requested_at": now,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&saasstore.ComputeTaskItem{}).
			Where("compute_task_id IN ? AND status = ?", taskIDs, compute.ItemStatusPending).
			Updates(map[string]any{
				"status": compute.ItemStatusCancelled, "cancelled_at": now,
				"lease_owner": "", "lease_expires_at": nil,
			}).Error
	}); err != nil {
		return nil, err
	}
	s.cancelTaskExecutions(taskIDs)
	for _, id := range taskIDs {
		if id == task.ID && task.Kind == compute.TaskKindComposite {
			continue
		}
		if err := s.refreshAtomicTask(ctx, id); err != nil {
			return nil, err
		}
	}
	if task.Kind == compute.TaskKindComposite {
		if err := s.refreshCompositeTask(ctx, task.ID); err != nil {
			return nil, err
		}
	} else if task.ParentTaskID != nil {
		if err := s.refreshCompositeTask(ctx, *task.ParentTaskID); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, userID, taskID)
}

func (s *Service) Retry(ctx context.Context, userID uint, taskID uint) (*TaskDescriptor, error) {
	if !s.started.Load() || s.stopping.Load() {
		return nil, ErrServiceUnavailable
	}
	var root saasstore.ComputeTask
	if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", taskID, userID).First(&root).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAccessNotFound
		}
		return nil, err
	}
	if !compute.CanRetry(root.Status) {
		return nil, ErrInvalidState
	}
	if root.Kind == compute.TaskKindStage {
		ready, err := s.dependenciesCompleted(ctx, root.ID)
		if err != nil {
			return nil, err
		}
		if !ready {
			return nil, ErrDependencyPending
		}
	}
	tasks := []saasstore.ComputeTask{root}
	if root.Kind == compute.TaskKindComposite {
		tasks = nil
		if err := s.db.WithContext(ctx).Where("parent_task_id = ? AND user_id = ?", root.ID, userID).
			Order("stage_order ASC, id ASC").Find(&tasks).Error; err != nil {
			return nil, err
		}
	}
	for _, task := range tasks {
		if task.Status == compute.TaskStatusCompleted {
			continue
		}
		if err := s.validateStoredTask(ctx, task); err != nil {
			s.invalidateTask(ctx, task.ID, err)
			return nil, err
		}
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, task := range tasks {
			if task.Status == compute.TaskStatusCompleted {
				continue
			}
			if err := tx.Model(&saasstore.ComputeTaskItem{}).
				Where("compute_task_id = ? AND status IN ?", task.ID, []string{compute.ItemStatusFailed, compute.ItemStatusCancelled}).
				Updates(map[string]any{
					"status": compute.ItemStatusPending, "failed_at": nil, "cancelled_at": nil,
					"completed_at": nil, "error_message": "", "lease_owner": "", "lease_expires_at": nil,
				}).Error; err != nil {
				return err
			}
			var pending int64
			if err := tx.Model(&saasstore.ComputeTaskItem{}).Where("compute_task_id = ? AND status = ?", task.ID, compute.ItemStatusPending).Count(&pending).Error; err != nil {
				return err
			}
			if pending > 0 {
				nextStatus := compute.TaskStatusQueued
				startedAt := any(now)
				if root.Kind == compute.TaskKindComposite {
					nextStatus = compute.TaskStatusPlanned
					startedAt = nil
				}
				if err := tx.Model(&saasstore.ComputeTask{}).Where("id = ?", task.ID).Updates(map[string]any{
					"status": nextStatus, "started_at": startedAt, "completed_at": nil,
					"cancelled_at": nil, "cancel_requested_at": nil, "error_message": "",
					"attempt": gorm.Expr("attempt + 1"),
				}).Error; err != nil {
					return err
				}
			}
		}
		if root.Kind == compute.TaskKindComposite {
			return tx.Model(&saasstore.ComputeTask{}).Where("id = ?", root.ID).Updates(map[string]any{
				"status": compute.TaskStatusPlanned, "started_at": nil, "completed_at": nil,
				"cancelled_at": nil, "cancel_requested_at": nil, "error_message": "",
				"attempt": gorm.Expr("attempt + 1"),
			}).Error
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if root.Kind == compute.TaskKindComposite {
		if err := s.refreshCompositeTask(ctx, root.ID); err != nil {
			return nil, err
		}
	} else if root.ParentTaskID != nil {
		if err := s.refreshCompositeTask(ctx, *root.ParentTaskID); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, userID, taskID)
}

// LookupCache is deliberately read-only. It never queues work and does not
// repair or invalidate a corrupt entry as a side effect of a UI lookup.
func (s *Service) LookupCache(ctx context.Context, userID uint, cacheKey string) (CacheLookup, error) {
	cacheKey = strings.TrimSpace(cacheKey)
	if userID == 0 || cacheKey == "" {
		return CacheLookup{}, ErrAccessNotFound
	}
	var entry saasstore.ComputeCacheEntry
	if err := s.db.WithContext(ctx).Where("owner_user_id = ? AND cache_key = ? AND status = ?", userID, cacheKey, compute.CacheStatusCompleted).
		Order("completed_at DESC, id DESC").First(&entry).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CacheLookup{CacheKey: cacheKey, Found: false}, nil
		}
		return CacheLookup{}, err
	}
	executor, ok := s.registry.Get(entry.ExecutorType)
	if !ok || executor.Descriptor().Version != entry.ExecutorVersion || executor.Descriptor().ResultSchemaVersion != entry.ResultSchemaVersion ||
		!s.validCompletedCache(ctx, userID, entry, executor) {
		return CacheLookup{CacheKey: cacheKey, Found: false}, nil
	}
	result := CacheLookup{CacheKey: cacheKey, Found: true, ContentHash: entry.ContentHash, Result: append(json.RawMessage(nil), entry.Result...)}
	if entry.CompletedAt != nil {
		result.CompletedAt = entry.CompletedAt.UTC().Format(time.RFC3339)
	}
	return result, nil
}

func (s *Service) dependenciesCompleted(ctx context.Context, taskID uint) (bool, error) {
	var incomplete int64
	err := s.db.WithContext(ctx).Table("compute_task_dependencies AS dependency").
		Joins("JOIN compute_tasks prerequisite ON prerequisite.id = dependency.depends_on_task_id").
		Where("dependency.compute_task_id = ? AND prerequisite.status <> ?", taskID, compute.TaskStatusCompleted).
		Count(&incomplete).Error
	return incomplete == 0, err
}

func (s *Service) validateStoredTask(ctx context.Context, task saasstore.ComputeTask) error {
	_, err := s.storedPlan(ctx, task)
	return err
}

func (s *Service) storedPlan(ctx context.Context, task saasstore.ComputeTask) (compute.Plan, error) {
	executor, err := s.runtimeExecutor(task)
	if err != nil {
		return compute.Plan{}, err
	}
	canonicalSettings, err := compute.CanonicalRawJSON(task.Settings)
	if err != nil || compute.HashBytes(canonicalSettings) != task.SettingsHash {
		return compute.Plan{}, fmt.Errorf("task settings hash mismatch: %w", ErrVersionMismatch)
	}
	canonicalManifest, err := compute.CanonicalRawJSON(task.Manifest)
	if err != nil || compute.HashBytes(canonicalManifest) != task.ManifestHash {
		return compute.Plan{}, fmt.Errorf("task manifest hash mismatch: %w", ErrVersionMismatch)
	}
	var manifest compute.Manifest
	if err := json.Unmarshal(canonicalManifest, &manifest); err != nil {
		return compute.Plan{}, fmt.Errorf("decode task manifest: %w", ErrVersionMismatch)
	}
	if manifest.SchemaVersion != compute.ManifestSchemaVersion || manifest.TotalItems != task.TotalItems || len(manifest.Items) != task.TotalItems || manifest.Executor != executor.Descriptor() {
		return compute.Plan{}, ErrVersionMismatch
	}
	parentPlanKey := ""
	if task.ParentTaskID != nil {
		if err := s.db.WithContext(ctx).Model(&saasstore.ComputeTask{}).Where("id = ? AND user_id = ?", *task.ParentTaskID, task.UserID).
			Pluck("plan_key", &parentPlanKey).Error; err != nil {
			return compute.Plan{}, err
		}
	}
	inputs := make([]compute.ManifestItemInput, 0, len(manifest.Items))
	for _, item := range manifest.Items {
		inputs = append(inputs, compute.ManifestItemInput{Key: item.Key, CacheKey: item.BaseCacheKey, Input: item.Input, EstimatedUnits: item.EstimatedUnits})
	}
	rebuilt, err := compute.BuildPlan(compute.PlanSpec{
		TaskType: task.TaskType, Executor: executor.Descriptor(), Settings: json.RawMessage(canonicalSettings),
		ResearchSettingHash: task.ResearchSettingHash, ParentPlanKey: parentPlanKey,
		StageKey: task.StageKey, StageType: task.StageType, StageOrder: task.StageOrder,
		RNG: compute.RNGSpec{Algorithm: task.RNGAlgorithm, Version: task.RNGVersion, RootSeed: task.RootSeed}, Items: inputs,
	})
	if err != nil || rebuilt.PlanKey != task.PlanKey || rebuilt.Snapshot.ManifestHash != task.ManifestHash {
		return compute.Plan{}, fmt.Errorf("task plan identity mismatch: %w", ErrVersionMismatch)
	}
	var storedItems []saasstore.ComputeTaskItem
	if err := s.db.WithContext(ctx).Where("compute_task_id = ?", task.ID).Order("item_index ASC").Find(&storedItems).Error; err != nil {
		return compute.Plan{}, err
	}
	if len(storedItems) != len(manifest.Items) {
		return compute.Plan{}, fmt.Errorf("task item count mismatch: %w", ErrVersionMismatch)
	}
	for index, item := range storedItems {
		expected := manifest.Items[index]
		if item.ItemIndex != expected.Index || item.ItemKey != expected.Key || item.BaseCacheKey != expected.BaseCacheKey ||
			item.CacheKey != expected.ResolvedCacheKey || item.InputHash != expected.InputHash || item.EstimatedUnits != expected.EstimatedUnits {
			return compute.Plan{}, fmt.Errorf("task item %d identity mismatch: %w", index, ErrVersionMismatch)
		}
		canonicalInput, err := compute.CanonicalRawJSON(item.Input)
		if err != nil || compute.HashBytes(canonicalInput) != expected.InputHash {
			return compute.Plan{}, fmt.Errorf("task item %d input mismatch: %w", index, ErrVersionMismatch)
		}
	}
	return rebuilt, nil
}

func (s *Service) invalidateTask(ctx context.Context, taskID uint, cause error) {
	now := time.Now().UTC()
	_ = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task saasstore.ComputeTask
		if err := tx.First(&task, taskID).Error; err != nil {
			return err
		}
		return s.invalidateTaskTx(tx, &task, cause.Error(), now)
	})
}
