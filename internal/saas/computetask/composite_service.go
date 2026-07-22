package computetask

import (
	"context"
	"fmt"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type preparedCompositeStage struct {
	spec    StageSpec
	plan    compute.Plan
	caches  map[string]saasstore.ComputeCacheEntry
	preview PlanPreview
}

type preparedComposite struct {
	plan    compute.CompositePlan
	stages  []preparedCompositeStage
	preview CompositePlanPreview
}

func (s *Service) PreviewComposite(ctx context.Context, userID uint, spec CompositeSpec) (CompositePlanPreview, error) {
	prepared, err := s.prepareComposite(ctx, userID, spec)
	if err != nil {
		return CompositePlanPreview{}, err
	}
	return prepared.preview, nil
}

func (s *Service) CreateComposite(ctx context.Context, userID uint, spec CompositeSpec, confirmSoftLimit bool) (*TaskDescriptor, error) {
	if userID == 0 {
		return nil, ErrAccessNotFound
	}
	prepared, err := s.prepareComposite(ctx, userID, spec)
	if err != nil {
		return nil, err
	}
	if exceedsTaskLimit(prepared.preview.TotalItems, prepared.preview.EstimatedUnits, prepared.preview.HardItemLimit) {
		return nil, &CompositeLimitError{Cause: ErrHardLimitExceeded, Preview: prepared.preview}
	}
	if prepared.preview.RequiresConfirmation && !confirmSoftLimit {
		return nil, &CompositeLimitError{Cause: ErrSoftLimitConfirm, Preview: prepared.preview}
	}
	title := strings.TrimSpace(spec.Title)
	if title == "" {
		title = strings.TrimSpace(spec.TaskType)
	}
	activeKey := fmt.Sprintf("%d|%s", userID, prepared.plan.PlanKey)
	rootID := uint(0)
	reused := false
	now := time.Now().UTC()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		statuses := make([]string, 0, len(prepared.stages))
		for _, stage := range prepared.stages {
			status := compute.TaskStatusPlanned
			if len(stage.caches) == stage.plan.Manifest.TotalItems {
				status = compute.TaskStatusCompleted
			}
			statuses = append(statuses, status)
		}
		rootStatus := compute.DeriveCompositeStatus(statuses)
		root := saasstore.ComputeTask{
			UserID: userID, Kind: compute.TaskKindComposite, TaskType: strings.TrimSpace(spec.TaskType), Title: title,
			PlanKey: prepared.plan.PlanKey, ActiveKey: &activeKey,
			TaskSchemaVersion: compute.TaskSchemaVersion, LifecycleVersion: compute.LifecycleVersion,
			SettingsHash: prepared.plan.Snapshot.SettingsHash, Settings: saasstore.JSONB(prepared.plan.SettingsJSON),
			ResearchSettingID: strings.TrimSpace(spec.ResearchSettingID), ResearchSettingHash: strings.TrimSpace(spec.ResearchSettingHash),
			ManifestVersion: compute.ManifestSchemaVersion, ManifestHash: prepared.plan.Snapshot.ManifestHash,
			Manifest:   saasstore.JSONB(prepared.plan.ManifestJSON),
			TotalItems: prepared.preview.TotalItems, EstimatedUnits: prepared.preview.EstimatedUnits,
			UnknownUnitItems: prepared.preview.UnknownUnitItems, CacheHitCount: prepared.preview.CacheHitCount,
			NewItemCount: prepared.preview.NewItemCount, ValidResultCount: prepared.preview.CacheHitCount,
			MissingCount: prepared.preview.TotalItems - prepared.preview.CacheHitCount,
			Progress:     float64(prepared.preview.CacheHitCount) / float64(prepared.preview.TotalItems),
			Status:       rootStatus, Checkpoint: saasstore.JSONB(`{}`),
		}
		if rootStatus == compute.TaskStatusCompleted {
			root.CompletedAt = &now
			root.Progress = 1
			root.MissingCount = 0
		}
		created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "active_key"}}, DoNothing: true}).Create(&root)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			var existing saasstore.ComputeTask
			if err := tx.Where("active_key = ?", activeKey).First(&existing).Error; err != nil {
				return err
			}
			if existing.UserID != userID || existing.PlanKey != prepared.plan.PlanKey || existing.Kind != compute.TaskKindComposite {
				return fmt.Errorf("active composite compute task identity mismatch")
			}
			rootID = existing.ID
			reused = true
			return nil
		}
		rootID = root.ID
		stageIDs := make(map[string]uint, len(prepared.stages))
		for _, stage := range prepared.stages {
			stageID, err := s.createCompositeStageTx(tx, userID, root.ID, spec, stage, now)
			if err != nil {
				return err
			}
			stageIDs[stage.spec.Key] = stageID
		}
		for _, stage := range prepared.stages {
			for _, dependencyKey := range stage.spec.DependsOnStageKeys {
				dependencyID, exists := stageIDs[dependencyKey]
				if !exists {
					return fmt.Errorf("missing stage dependency %q", dependencyKey)
				}
				if err := tx.Create(&saasstore.ComputeTaskDependency{ComputeTaskID: stageIDs[stage.spec.Key], DependsOnTaskID: dependencyID}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	descriptor, err := s.Get(ctx, userID, rootID)
	if descriptor != nil {
		descriptor.Reused = reused
	}
	return descriptor, err
}

func (s *Service) prepareComposite(ctx context.Context, userID uint, spec CompositeSpec) (preparedComposite, error) {
	stageSpecs := make([]compute.CompositeStagePlanSpec, 0, len(spec.Stages))
	byKey := make(map[string]StageSpec, len(spec.Stages))
	for _, rawStage := range spec.Stages {
		stage := rawStage
		stage.Key = strings.TrimSpace(stage.Key)
		stage.Type = strings.TrimSpace(stage.Type)
		stage.ExecutorType = strings.TrimSpace(stage.ExecutorType)
		executor, ok := s.registry.Get(stage.ExecutorType)
		if !ok {
			return preparedComposite{}, ErrUnknownExecutor
		}
		stage.DependsOnStageKeys = trimStrings(stage.DependsOnStageKeys)
		byKey[stage.Key] = stage
		stageSpecs = append(stageSpecs, compute.CompositeStagePlanSpec{
			Key: stage.Key, Type: stage.Type, Order: stage.Order, Executor: executor.Descriptor(),
			Settings: stage.Settings, DependsOnStageKeys: stage.DependsOnStageKeys,
			RNG: stage.RNG, Items: stage.Items,
		})
	}
	plan, err := compute.BuildCompositePlan(compute.CompositePlanSpec{
		TaskType: spec.TaskType, Settings: spec.Settings,
		ResearchSettingHash: spec.ResearchSettingHash, Stages: stageSpecs,
	})
	if err != nil {
		return preparedComposite{}, err
	}
	prepared := preparedComposite{plan: plan, stages: make([]preparedCompositeStage, 0, len(plan.Manifest.Stages))}
	preview := CompositePlanPreview{
		PlanKey: plan.PlanKey, TaskSchemaVersion: compute.TaskSchemaVersion, LifecycleVersion: compute.LifecycleVersion,
		ManifestVersion: compute.ManifestSchemaVersion, ManifestHash: plan.Snapshot.ManifestHash,
		SoftItemLimit: s.options.SoftItemLimit, HardItemLimit: s.options.HardItemLimit,
		Stages: make([]PlanPreview, 0, len(plan.Manifest.Stages)),
	}
	for _, snapshot := range plan.Manifest.Stages {
		stage := byKey[snapshot.Key]
		executor, _ := s.registry.Get(stage.ExecutorType)
		stagePlan, err := compute.BuildPlan(compute.PlanSpec{
			TaskType: strings.TrimSpace(spec.TaskType) + ":" + stage.Type, Executor: executor.Descriptor(), Settings: stage.Settings,
			ResearchSettingHash: spec.ResearchSettingHash, ParentPlanKey: plan.PlanKey,
			StageKey: stage.Key, StageType: stage.Type, StageOrder: stage.Order, RNG: stage.RNG, Items: stage.Items,
		})
		if err != nil {
			return preparedComposite{}, err
		}
		caches, err := s.findValidCaches(ctx, s.db, userID, stagePlan)
		if err != nil {
			return preparedComposite{}, err
		}
		stagePreview := s.previewForPlan(stagePlan, len(caches))
		prepared.stages = append(prepared.stages, preparedCompositeStage{spec: stage, plan: stagePlan, caches: caches, preview: stagePreview})
		preview.Stages = append(preview.Stages, stagePreview)
		preview.TotalItems += stagePreview.TotalItems
		preview.EstimatedUnits += stagePreview.EstimatedUnits
		preview.UnknownUnitItems += stagePreview.UnknownUnitItems
		preview.CacheHitCount += stagePreview.CacheHitCount
		preview.NewItemCount += stagePreview.NewItemCount
	}
	preview.RequiresConfirmation = exceedsTaskLimit(preview.TotalItems, preview.EstimatedUnits, preview.SoftItemLimit)
	prepared.preview = preview
	return prepared, nil
}

func (s *Service) createCompositeStageTx(tx *gorm.DB, userID uint, parentID uint, composite CompositeSpec, prepared preparedCompositeStage, now time.Time) (uint, error) {
	activeKey := fmt.Sprintf("%d|%s", userID, prepared.plan.PlanKey)
	title := strings.TrimSpace(prepared.spec.Title)
	if title == "" {
		title = prepared.spec.Type
	}
	status := compute.TaskStatusPlanned
	progress := compute.Progress(compute.ItemCounts{Total: prepared.plan.Manifest.TotalItems, Cached: len(prepared.caches)})
	stage := saasstore.ComputeTask{
		UserID: userID, ParentTaskID: &parentID, Kind: compute.TaskKindStage,
		TaskType: prepared.plan.Snapshot.TaskType, Title: title, PlanKey: prepared.plan.PlanKey, ActiveKey: &activeKey,
		TaskSchemaVersion: compute.TaskSchemaVersion, LifecycleVersion: compute.LifecycleVersion,
		ExecutorType: prepared.plan.Snapshot.Executor.Type, ExecutorVersion: prepared.plan.Snapshot.Executor.Version,
		ResultSchemaVersion: prepared.plan.Snapshot.Executor.ResultSchemaVersion,
		SettingsHash:        prepared.plan.Snapshot.SettingsHash, Settings: saasstore.JSONB(prepared.plan.SettingsJSON),
		ResearchSettingID: strings.TrimSpace(composite.ResearchSettingID), ResearchSettingHash: strings.TrimSpace(composite.ResearchSettingHash),
		StageKey: prepared.plan.Snapshot.StageKey, StageType: prepared.plan.Snapshot.StageType, StageOrder: prepared.plan.Snapshot.StageOrder,
		ManifestVersion: compute.ManifestSchemaVersion, ManifestHash: prepared.plan.Snapshot.ManifestHash, Manifest: saasstore.JSONB(prepared.plan.ManifestJSON),
		TotalItems: prepared.plan.Manifest.TotalItems, EstimatedUnits: prepared.plan.Manifest.EstimatedUnits,
		UnknownUnitItems: prepared.plan.Manifest.UnknownUnitItems, CacheHitCount: len(prepared.caches),
		NewItemCount: prepared.plan.Manifest.TotalItems - len(prepared.caches), ValidResultCount: len(prepared.caches),
		MissingCount: prepared.plan.Manifest.TotalItems - len(prepared.caches), Progress: progress, Status: status,
		RNGAlgorithm: prepared.plan.Snapshot.RNG.Algorithm, RNGVersion: prepared.plan.Snapshot.RNG.Version,
		RootSeed: prepared.plan.Snapshot.RNG.RootSeed, Checkpoint: saasstore.JSONB(`{}`),
	}
	if err := tx.Create(&stage).Error; err != nil {
		return 0, err
	}
	items := make([]saasstore.ComputeTaskItem, 0, len(prepared.plan.Manifest.Items))
	for _, item := range prepared.plan.Manifest.Items {
		model := saasstore.ComputeTaskItem{
			ComputeTaskID: stage.ID, ItemIndex: item.Index, ItemKey: item.Key,
			BaseCacheKey: item.BaseCacheKey, CacheKey: item.ResolvedCacheKey,
			InputHash: item.InputHash, Input: saasstore.JSONB(item.Input), EstimatedUnits: item.EstimatedUnits,
			Status: compute.ItemStatusPending, Result: saasstore.JSONB(`{}`), Checkpoint: saasstore.JSONB(`{}`),
		}
		if cache, ok := prepared.caches[item.ResolvedCacheKey]; ok {
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
		return 0, err
	}
	return stage.ID, nil
}

func trimStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, strings.TrimSpace(value))
	}
	return result
}
