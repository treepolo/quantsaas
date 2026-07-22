package computetask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	compute "quantsaas/internal/compute"
	saasstore "quantsaas/internal/saas/store"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db        *gorm.DB
	registry  *Registry
	options   Options
	logger    *zap.Logger
	workerID  string
	rootCtx   context.Context
	stopRoot  context.CancelFunc
	started   atomic.Bool
	stopping  atomic.Bool
	sem       chan struct{}
	mu        sync.Mutex
	cancels   map[uint]map[uint]context.CancelFunc
	computeMu sync.RWMutex
	computes  map[uint]*liveComputeMonitor
	wg        sync.WaitGroup
}

func NewService(db *gorm.DB, registry *Registry, options Options, logger *zap.Logger) (*Service, error) {
	if db == nil {
		return nil, fmt.Errorf("database is required")
	}
	if registry == nil {
		registry = NewRegistry()
	}
	if err := options.validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	rootCtx, cancel := context.WithCancel(context.Background())
	return &Service{
		db: db, registry: registry, options: options, logger: logger,
		workerID: fmt.Sprintf("worker-%d", time.Now().UTC().UnixNano()),
		rootCtx:  rootCtx, stopRoot: cancel, sem: make(chan struct{}, options.Workers),
		cancels:  make(map[uint]map[uint]context.CancelFunc),
		computes: make(map[uint]*liveComputeMonitor),
	}, nil
}

func (s *Service) Limits() LimitsDescriptor {
	return LimitsDescriptor{SoftItemLimit: s.options.SoftItemLimit, HardItemLimit: s.options.HardItemLimit, Workers: s.options.Workers}
}

func (s *Service) Preview(ctx context.Context, userID uint, spec CreateSpec) (PlanPreview, error) {
	plan, err := s.buildPlan(ctx, userID, spec)
	if err != nil {
		return PlanPreview{}, err
	}
	caches, err := s.findValidCaches(ctx, s.db, userID, plan)
	if err != nil {
		return PlanPreview{}, err
	}
	return s.previewForPlan(plan, len(caches)), nil
}

// PreviewTask recalculates only cache availability for an already immutable
// manifest. It is safe for a stage confirmation screen and never starts work.
func (s *Service) PreviewTask(ctx context.Context, userID uint, taskID uint) (PlanPreview, error) {
	var task saasstore.ComputeTask
	if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PlanPreview{}, ErrAccessNotFound
		}
		return PlanPreview{}, err
	}
	if task.Kind == compute.TaskKindComposite {
		return PlanPreview{}, ErrInvalidState
	}
	plan, err := s.storedPlan(ctx, task)
	if err != nil {
		return PlanPreview{}, err
	}
	caches, err := s.findValidCaches(ctx, s.db, userID, plan)
	if err != nil {
		return PlanPreview{}, err
	}
	return s.previewForPlan(plan, len(caches)), nil
}

func (s *Service) Create(ctx context.Context, userID uint, spec CreateSpec, confirmSoftLimit bool) (*TaskDescriptor, error) {
	if userID == 0 {
		return nil, ErrAccessNotFound
	}
	plan, err := s.buildPlan(ctx, userID, spec)
	if err != nil {
		return nil, err
	}
	caches, err := s.findValidCaches(ctx, s.db, userID, plan)
	if err != nil {
		return nil, err
	}
	preview := s.previewForPlan(plan, len(caches))
	if exceedsTaskLimit(preview.TotalItems, preview.EstimatedUnits, preview.HardItemLimit) {
		return nil, &LimitError{Cause: ErrHardLimitExceeded, Preview: preview}
	}
	if preview.RequiresConfirmation && !confirmSoftLimit {
		return nil, &LimitError{Cause: ErrSoftLimitConfirm, Preview: preview}
	}

	kind := strings.TrimSpace(spec.Kind)
	if kind == "" {
		kind = compute.TaskKindAtomic
		if spec.ParentTaskID != nil {
			kind = compute.TaskKindStage
		}
	}
	if kind != compute.TaskKindAtomic && kind != compute.TaskKindStage {
		return nil, fmt.Errorf("unsupported compute task kind %q", kind)
	}
	if kind == compute.TaskKindStage && spec.ParentTaskID == nil {
		return nil, fmt.Errorf("stage compute task requires a composite parent")
	}
	if kind == compute.TaskKindAtomic && (spec.ParentTaskID != nil || len(spec.DependsOnTaskIDs) > 0) {
		return nil, fmt.Errorf("atomic compute task cannot have a parent or stage dependencies")
	}
	title := strings.TrimSpace(spec.Title)
	if title == "" {
		title = strings.TrimSpace(spec.TaskType)
	}
	activeKey := fmt.Sprintf("%d|%s", userID, plan.PlanKey)
	now := time.Now().UTC()
	createdID := uint(0)
	reused := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if spec.ParentTaskID != nil {
			var parent saasstore.ComputeTask
			if err := tx.Where("id = ? AND user_id = ?", *spec.ParentTaskID, userID).First(&parent).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAccessNotFound
				}
				return err
			}
			if parent.Kind != compute.TaskKindComposite {
				return fmt.Errorf("parent task must be composite")
			}
		}
		candidate := saasstore.ComputeTask{
			UserID: userID, ParentTaskID: spec.ParentTaskID, Kind: kind,
			TaskType: strings.TrimSpace(spec.TaskType), Title: title,
			PlanKey: plan.PlanKey, ActiveKey: &activeKey,
			TaskSchemaVersion: compute.TaskSchemaVersion, LifecycleVersion: compute.LifecycleVersion,
			ExecutorType: plan.Snapshot.Executor.Type, ExecutorVersion: plan.Snapshot.Executor.Version,
			ResultSchemaVersion: plan.Snapshot.Executor.ResultSchemaVersion,
			SettingsHash:        plan.Snapshot.SettingsHash, Settings: saasstore.JSONB(plan.SettingsJSON),
			ResearchSettingID: strings.TrimSpace(spec.ResearchSettingID), ResearchSettingHash: strings.TrimSpace(spec.ResearchSettingHash),
			StageKey: plan.Snapshot.StageKey, StageType: plan.Snapshot.StageType, StageOrder: plan.Snapshot.StageOrder,
			ManifestVersion: compute.ManifestSchemaVersion, ManifestHash: plan.Snapshot.ManifestHash, Manifest: saasstore.JSONB(plan.ManifestJSON),
			TotalItems: plan.Manifest.TotalItems, EstimatedUnits: plan.Manifest.EstimatedUnits, UnknownUnitItems: plan.Manifest.UnknownUnitItems,
			ComputeMonitorEnabled: spec.ComputeMonitorEnabled,
			CacheHitCount:         len(caches), NewItemCount: plan.Manifest.TotalItems - len(caches),
			ValidResultCount: len(caches), MissingCount: plan.Manifest.TotalItems - len(caches),
			Progress:     compute.Progress(compute.ItemCounts{Total: plan.Manifest.TotalItems, Cached: len(caches)}),
			Status:       compute.TaskStatusPlanned,
			RNGAlgorithm: plan.Snapshot.RNG.Algorithm, RNGVersion: plan.Snapshot.RNG.Version, RootSeed: plan.Snapshot.RNG.RootSeed,
			Checkpoint: saasstore.JSONB(`{}`),
		}
		if len(caches) == plan.Manifest.TotalItems {
			candidate.Status = compute.TaskStatusCompleted
			candidate.Progress = 1
			candidate.CompletedAt = &now
			candidate.MissingCount = 0
		}
		created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "active_key"}}, DoNothing: true}).Create(&candidate)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			var existing saasstore.ComputeTask
			if err := tx.Where("active_key = ?", activeKey).First(&existing).Error; err != nil {
				return err
			}
			if existing.UserID != userID || existing.PlanKey != plan.PlanKey {
				return fmt.Errorf("active compute task identity mismatch")
			}
			createdID = existing.ID
			reused = true
			return nil
		}
		createdID = candidate.ID
		items := make([]saasstore.ComputeTaskItem, 0, len(plan.Manifest.Items))
		for _, item := range plan.Manifest.Items {
			model := saasstore.ComputeTaskItem{
				ComputeTaskID: candidate.ID, ItemIndex: item.Index, ItemKey: item.Key,
				BaseCacheKey: item.BaseCacheKey, CacheKey: item.ResolvedCacheKey,
				InputHash: item.InputHash, Input: saasstore.JSONB(item.Input), EstimatedUnits: item.EstimatedUnits,
				Status: compute.ItemStatusPending, Result: saasstore.JSONB(`{}`), Checkpoint: saasstore.JSONB(`{}`),
			}
			if cache, ok := caches[item.ResolvedCacheKey]; ok {
				model.Status = compute.ItemStatusCached
				model.Progress = 1
				model.CacheEntryID = &cache.ID
				model.Result = append(saasstore.JSONB(nil), cache.Result...)
				model.ResultHash = cache.ContentHash
				model.CompletedAt = cache.CompletedAt
			}
			items = append(items, model)
		}
		if err := tx.CreateInBatches(&items, 500).Error; err != nil {
			return err
		}
		if err := s.createDependencies(ctx, tx, userID, candidate.ID, spec.ParentTaskID, spec.DependsOnTaskIDs); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	descriptor, err := s.Get(ctx, userID, createdID)
	if descriptor != nil {
		descriptor.Reused = reused
	}
	return descriptor, err
}

func (s *Service) buildPlan(ctx context.Context, userID uint, spec CreateSpec) (compute.Plan, error) {
	_ = ctx
	_ = userID
	executor, ok := s.registry.Get(strings.TrimSpace(spec.ExecutorType))
	if !ok {
		return compute.Plan{}, ErrUnknownExecutor
	}
	parentPlanKey := ""
	if spec.ParentTaskID != nil {
		var parent saasstore.ComputeTask
		if err := s.db.Where("id = ? AND user_id = ?", *spec.ParentTaskID, userID).First(&parent).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return compute.Plan{}, ErrAccessNotFound
			}
			return compute.Plan{}, err
		}
		parentPlanKey = parent.PlanKey
	}
	return compute.BuildPlan(compute.PlanSpec{
		TaskType: spec.TaskType, Executor: executor.Descriptor(), Settings: spec.Settings,
		ResearchSettingHash: spec.ResearchSettingHash, ParentPlanKey: parentPlanKey,
		StageKey: spec.StageKey, StageType: spec.StageType, StageOrder: spec.StageOrder,
		RNG: spec.RNG, Items: spec.Items,
	})
}

func (s *Service) previewForPlan(plan compute.Plan, cacheHits int) PlanPreview {
	return PlanPreview{
		PlanKey: plan.PlanKey, TaskSchemaVersion: compute.TaskSchemaVersion, LifecycleVersion: compute.LifecycleVersion,
		StageKey: plan.Snapshot.StageKey, StageType: plan.Snapshot.StageType, StageOrder: plan.Snapshot.StageOrder,
		Executor: plan.Snapshot.Executor, ManifestVersion: compute.ManifestSchemaVersion, ManifestHash: plan.Snapshot.ManifestHash,
		TotalItems: plan.Manifest.TotalItems, EstimatedUnits: plan.Manifest.EstimatedUnits, UnknownUnitItems: plan.Manifest.UnknownUnitItems,
		CacheHitCount: cacheHits, NewItemCount: plan.Manifest.TotalItems - cacheHits,
		SoftItemLimit: s.options.SoftItemLimit, HardItemLimit: s.options.HardItemLimit,
		RequiresConfirmation: exceedsTaskLimit(plan.Manifest.TotalItems, plan.Manifest.EstimatedUnits, s.options.SoftItemLimit),
	}
}

func exceedsTaskLimit(items int, estimatedUnits int64, limit int) bool {
	return items > limit || estimatedUnits > int64(limit)
}

func (s *Service) createDependencies(ctx context.Context, tx *gorm.DB, userID uint, taskID uint, parentTaskID *uint, dependencyIDs []uint) error {
	seen := make(map[uint]struct{}, len(dependencyIDs))
	rows := make([]saasstore.ComputeTaskDependency, 0, len(dependencyIDs))
	for _, dependencyID := range dependencyIDs {
		if dependencyID == 0 || dependencyID == taskID {
			return fmt.Errorf("invalid compute task dependency")
		}
		if _, exists := seen[dependencyID]; exists {
			continue
		}
		seen[dependencyID] = struct{}{}
		var dependency saasstore.ComputeTask
		if err := tx.WithContext(ctx).Select("id", "parent_task_id").Where("id = ? AND user_id = ?", dependencyID, userID).Take(&dependency).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAccessNotFound
			}
			return err
		}
		if parentTaskID == nil || dependency.ParentTaskID == nil || *dependency.ParentTaskID != *parentTaskID {
			return fmt.Errorf("stage dependencies must belong to the same composite task")
		}
		rows = append(rows, saasstore.ComputeTaskDependency{ComputeTaskID: taskID, DependsOnTaskID: dependencyID})
	}
	if len(rows) > 0 {
		return tx.WithContext(ctx).Create(&rows).Error
	}
	return nil
}

func (s *Service) findValidCaches(ctx context.Context, db *gorm.DB, userID uint, plan compute.Plan) (map[string]saasstore.ComputeCacheEntry, error) {
	result := make(map[string]saasstore.ComputeCacheEntry)
	executor, ok := s.registry.Get(plan.Snapshot.Executor.Type)
	if !ok {
		return nil, ErrUnknownExecutor
	}
	keys := make([]string, 0, len(plan.Manifest.Items))
	for _, item := range plan.Manifest.Items {
		keys = append(keys, item.ResolvedCacheKey)
	}
	for start := 0; start < len(keys); start += 1000 {
		end := min(start+1000, len(keys))
		var rows []saasstore.ComputeCacheEntry
		if err := db.WithContext(ctx).
			Where("owner_user_id = ? AND cache_key IN ? AND status = ?", userID, keys[start:end], compute.CacheStatusCompleted).
			Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.SchemaVersion != compute.CacheEntrySchemaVersion ||
				row.ExecutorType != plan.Snapshot.Executor.Type || row.ExecutorVersion != plan.Snapshot.Executor.Version ||
				row.ResultSchemaVersion != plan.Snapshot.Executor.ResultSchemaVersion {
				continue
			}
			if !s.validCompletedCache(ctx, userID, row, executor) {
				continue
			}
			result[row.CacheKey] = row
		}
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, userID uint, taskID uint) (*TaskDescriptor, error) {
	var task saasstore.ComputeTask
	if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAccessNotFound
		}
		return nil, err
	}
	descriptor, err := s.taskDescriptor(ctx, task)
	if err != nil {
		return nil, err
	}
	s.overlayLiveCompute(descriptor)
	return descriptor, nil
}

func (s *Service) overlayLiveCompute(descriptor *TaskDescriptor) {
	if descriptor == nil || !descriptor.ComputeMonitorEnabled {
		return
	}
	s.computeMu.RLock()
	monitor := s.computes[descriptor.ID]
	s.computeMu.RUnlock()
	if monitor == nil {
		return
	}
	descriptor.ComputedUnits, descriptor.PlannedComputeUnits, descriptor.ComputeUnitsPerSec, descriptor.ComputeRemainingSec, descriptor.ComputeCurrentStage, descriptor.ComputeLastHeartbeat = monitor.snapshot()
}

func (s *Service) Snapshot(ctx context.Context, userID uint, taskID uint) (*TaskSnapshot, error) {
	var task saasstore.ComputeTask
	if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAccessNotFound
		}
		return nil, err
	}
	return &TaskSnapshot{
		ID: task.ID, PlanKey: task.PlanKey,
		TaskSchemaVersion: task.TaskSchemaVersion, LifecycleVersion: task.LifecycleVersion,
		SettingsHash: task.SettingsHash, Settings: append(json.RawMessage(nil), task.Settings...),
		ResearchSettingID: task.ResearchSettingID, ResearchSettingHash: task.ResearchSettingHash,
		ManifestVersion: task.ManifestVersion, ManifestHash: task.ManifestHash,
		Manifest:       append(json.RawMessage(nil), task.Manifest...),
		CheckpointHash: task.CheckpointHash, Checkpoint: append(json.RawMessage(nil), task.Checkpoint...),
	}, nil
}

func (s *Service) List(ctx context.Context, userID uint, filter ListFilter) ([]TaskDescriptor, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := s.db.WithContext(ctx).Where("user_id = ?", userID)
	if strings.TrimSpace(filter.Status) != "" {
		query = query.Where("status = ?", strings.TrimSpace(filter.Status))
	}
	if filter.ParentTaskID != nil {
		query = query.Where("parent_task_id = ?", *filter.ParentTaskID)
	} else if filter.RootOnly {
		query = query.Where("parent_task_id IS NULL")
	}
	var rows []saasstore.ComputeTask
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Offset(filter.Offset).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []TaskDescriptor{}, nil
	}
	taskIDs := make([]uint, 0, len(rows))
	dependencyIDs := make(map[uint][]uint, len(rows))
	childIDs := make(map[uint][]uint, len(rows))
	for _, row := range rows {
		taskIDs = append(taskIDs, row.ID)
		dependencyIDs[row.ID] = []uint{}
		childIDs[row.ID] = []uint{}
	}
	type dependencyRelation struct {
		ComputeTaskID   uint
		DependsOnTaskID uint
	}
	var dependencies []dependencyRelation
	if err := s.db.WithContext(ctx).Table("compute_task_dependencies AS dependency").
		Select("dependency.compute_task_id, dependency.depends_on_task_id").
		Joins("JOIN compute_tasks prerequisite ON prerequisite.id = dependency.depends_on_task_id").
		Where("dependency.compute_task_id IN ? AND prerequisite.user_id = ?", taskIDs, userID).
		Order("dependency.compute_task_id ASC, dependency.depends_on_task_id ASC").Scan(&dependencies).Error; err != nil {
		return nil, err
	}
	for _, dependency := range dependencies {
		dependencyIDs[dependency.ComputeTaskID] = append(dependencyIDs[dependency.ComputeTaskID], dependency.DependsOnTaskID)
	}
	type childRelation struct {
		ID           uint
		ParentTaskID *uint
	}
	var children []childRelation
	if err := s.db.WithContext(ctx).Model(&saasstore.ComputeTask{}).
		Select("id, parent_task_id").Where("parent_task_id IN ? AND user_id = ?", taskIDs, userID).
		Order("parent_task_id ASC, stage_order ASC, id ASC").Scan(&children).Error; err != nil {
		return nil, err
	}
	for _, child := range children {
		if child.ParentTaskID != nil {
			childIDs[*child.ParentTaskID] = append(childIDs[*child.ParentTaskID], child.ID)
		}
	}
	items := make([]TaskDescriptor, 0, len(rows))
	for _, row := range rows {
		descriptor := taskDescriptorWithRelations(row, dependencyIDs[row.ID], childIDs[row.ID])
		s.overlayLiveCompute(descriptor)
		items = append(items, *descriptor)
	}
	return items, nil
}

func (s *Service) Items(ctx context.Context, userID uint, taskID uint, filter ItemFilter) ([]ItemDescriptor, error) {
	if _, err := s.Get(ctx, userID, taskID); err != nil {
		return nil, err
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := s.db.WithContext(ctx).Where("compute_task_id = ?", taskID)
	if strings.TrimSpace(filter.Status) != "" {
		query = query.Where("status = ?", strings.TrimSpace(filter.Status))
	}
	var rows []saasstore.ComputeTaskItem
	if err := query.Order("item_index ASC, id ASC").Limit(limit).Offset(filter.Offset).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]ItemDescriptor, 0, len(rows))
	for _, row := range rows {
		result = append(result, itemDescriptor(row, filter.IncludeResult))
	}
	return result, nil
}

func (s *Service) taskDescriptor(ctx context.Context, task saasstore.ComputeTask) (*TaskDescriptor, error) {
	var dependencyIDs []uint
	if err := s.db.WithContext(ctx).Table("compute_task_dependencies AS dependency").
		Joins("JOIN compute_tasks prerequisite ON prerequisite.id = dependency.depends_on_task_id").
		Where("dependency.compute_task_id = ? AND prerequisite.user_id = ?", task.ID, task.UserID).
		Order("dependency.depends_on_task_id ASC").Pluck("dependency.depends_on_task_id", &dependencyIDs).Error; err != nil {
		return nil, err
	}
	var childIDs []uint
	if err := s.db.WithContext(ctx).Model(&saasstore.ComputeTask{}).
		Where("parent_task_id = ? AND user_id = ?", task.ID, task.UserID).Order("stage_order ASC, id ASC").Pluck("id", &childIDs).Error; err != nil {
		return nil, err
	}
	return taskDescriptorWithRelations(task, dependencyIDs, childIDs), nil
}

func taskDescriptorWithRelations(task saasstore.ComputeTask, dependencyIDs, childIDs []uint) *TaskDescriptor {
	descriptor := &TaskDescriptor{
		ID: task.ID, UserID: task.UserID, ParentTaskID: task.ParentTaskID, Kind: task.Kind,
		TaskType: task.TaskType, Title: task.Title, PlanKey: task.PlanKey,
		TaskSchemaVersion: task.TaskSchemaVersion, LifecycleVersion: task.LifecycleVersion,
		Executor:          compute.ExecutorDescriptor{Type: task.ExecutorType, Version: task.ExecutorVersion, ResultSchemaVersion: task.ResultSchemaVersion},
		SettingsHash:      task.SettingsHash,
		ResearchSettingID: task.ResearchSettingID, ResearchSettingHash: task.ResearchSettingHash,
		StageKey: task.StageKey, StageType: task.StageType, StageOrder: task.StageOrder,
		ManifestVersion: task.ManifestVersion, ManifestHash: task.ManifestHash,
		TotalItems: task.TotalItems, EstimatedUnits: task.EstimatedUnits, UnknownUnitItems: task.UnknownUnitItems, ComputeMonitorEnabled: task.ComputeMonitorEnabled,
		CacheHitCount: task.CacheHitCount, NewItemCount: task.NewItemCount,
		ValidResultCount: task.ValidResultCount, FailedCount: task.FailedCount,
		MissingCount: task.MissingCount, CancelledCount: task.CancelledCount,
		Progress: task.Progress, Status: task.Status, Error: task.ErrorMessage, Attempt: task.Attempt,
		RNGAlgorithm: task.RNGAlgorithm, RNGVersion: task.RNGVersion, RootSeed: task.RootSeed,
		RNGPosition: task.RNGPosition, CheckpointHash: task.CheckpointHash,
		DependencyTaskIDs: dependencyIDs, ChildTaskIDs: childIDs,
		CreatedAt: task.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: task.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if task.StartedAt != nil {
		descriptor.StartedAt = task.StartedAt.UTC().Format(time.RFC3339)
	}
	if task.CompletedAt != nil {
		descriptor.CompletedAt = task.CompletedAt.UTC().Format(time.RFC3339)
	}
	if task.CancelledAt != nil {
		descriptor.CancelledAt = task.CancelledAt.UTC().Format(time.RFC3339)
	}
	if task.CancelRequestedAt != nil {
		descriptor.CancelRequestedAt = task.CancelRequestedAt.UTC().Format(time.RFC3339)
	}
	return descriptor
}

func itemDescriptor(item saasstore.ComputeTaskItem, includeResult bool) ItemDescriptor {
	descriptor := ItemDescriptor{
		ID: item.ID, TaskID: item.ComputeTaskID, Index: item.ItemIndex, Key: item.ItemKey,
		CacheKey: item.CacheKey, InputHash: item.InputHash, EstimatedUnits: item.EstimatedUnits,
		Status: item.Status, Progress: item.Progress, Attempt: item.Attempt, CacheEntryID: item.CacheEntryID,
		ResultHash: item.ResultHash, Error: item.ErrorMessage,
		CheckpointHash: item.CheckpointHash, RNGPosition: item.RNGPosition,
	}
	if includeResult && item.ResultHash != "" {
		descriptor.Result = append(json.RawMessage(nil), item.Result...)
	}
	if item.StartedAt != nil {
		descriptor.StartedAt = item.StartedAt.UTC().Format(time.RFC3339)
	}
	if item.CompletedAt != nil {
		descriptor.CompletedAt = item.CompletedAt.UTC().Format(time.RFC3339)
	}
	if item.FailedAt != nil {
		descriptor.FailedAt = item.FailedAt.UTC().Format(time.RFC3339)
	}
	if item.CancelledAt != nil {
		descriptor.CancelledAt = item.CancelledAt.UTC().Format(time.RFC3339)
	}
	return descriptor
}
