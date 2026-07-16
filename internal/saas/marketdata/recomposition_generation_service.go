package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	"quantsaas/internal/marketversion"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const generationTaskType = "p06.recomposition.generate"

func (s *Service) CreateRecompositionGeneration(
	ctx context.Context,
	userID uint,
	req RecompositionGenerationRequest,
	confirmSoftLimit bool,
) (RecompositionGenerationTask, error) {
	if s == nil || s.db == nil || s.computeTasks == nil || userID == 0 {
		return RecompositionGenerationTask{}, computetask.ErrServiceUnavailable
	}
	req.SchemaVersion = strings.TrimSpace(req.SchemaVersion)
	if req.SchemaVersion == "" {
		req.SchemaVersion = RecompositionGenerationRequestV1
	}
	req.PlanHash = strings.TrimSpace(req.PlanHash)
	req.SeriesName = strings.TrimSpace(req.SeriesName)
	req.Notes = strings.TrimSpace(req.Notes)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.SchemaVersion != RecompositionGenerationRequestV1 || req.PlanID == 0 || req.PlanHash == "" || req.SeriesName == "" || len(req.SeriesName) > 160 {
		return RecompositionGenerationTask{}, ErrInvalidRecomposition
	}
	var plan saasstore.RecompositionPlan
	if err := s.db.WithContext(ctx).
		Where("id = ? AND owner_user_id = ? AND plan_hash = ?", req.PlanID, userID, req.PlanHash).
		First(&plan).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return RecompositionGenerationTask{}, ErrRecompositionPlanNotFound
		}
		return RecompositionGenerationTask{}, err
	}
	if plan.Status != marketversion.VersionStatusCompleted || plan.TotalOutputBars <= 0 {
		return RecompositionGenerationTask{}, ErrStaleRecompositionPlan
	}
	if req.IdempotencyKey == "" {
		raw, _ := compute.CanonicalJSON(struct {
			PlanHash   string `json:"plan_hash"`
			SeriesName string `json:"series_name"`
		}{req.PlanHash, req.SeriesName})
		req.IdempotencyKey = "p06-generation:" + compute.HashBytes(raw)
	}
	if len(req.IdempotencyKey) > 128 {
		return RecompositionGenerationTask{}, ErrInvalidRecomposition
	}

	generation, err := s.reserveRecompositionGeneration(ctx, userID, req, plan)
	if err != nil {
		return RecompositionGenerationTask{}, err
	}
	spec, err := generationCompositeSpec(generation, plan)
	if err != nil {
		return RecompositionGenerationTask{}, err
	}
	preview, err := s.computeTasks.PreviewComposite(ctx, userID, spec)
	if err != nil {
		return RecompositionGenerationTask{}, err
	}
	if generation.ComputeTaskID != nil {
		task, getErr := s.computeTasks.Get(ctx, userID, *generation.ComputeTaskID)
		if getErr != nil {
			return RecompositionGenerationTask{}, getErr
		}
		result, resultErr := s.recompositionGenerationResult(ctx, generation.ID, userID)
		return RecompositionGenerationTask{Generation: result, Task: task, Preview: preview}, resultErr
	}
	task, err := s.computeTasks.CreateComposite(ctx, userID, spec, confirmSoftLimit)
	if err != nil {
		return RecompositionGenerationTask{}, err
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&saasstore.RecompositionGeneration{}).
			Where("id = ? AND owner_user_id = ? AND compute_task_id IS NULL", generation.ID, userID).
			Update("compute_task_id", task.ID).Error; err != nil {
			return err
		}
		return tx.Model(&saasstore.MarketDataVersion{}).
			Where("id = ? AND owner_user_id = ?", generation.OutputVersionID, userID).
			Update("compute_task_id", task.ID).Error
	}); err != nil {
		return RecompositionGenerationTask{}, err
	}
	generation.ComputeTaskID = &task.ID
	result, err := s.recompositionGenerationResult(ctx, generation.ID, userID)
	return RecompositionGenerationTask{Generation: result, Task: task, Preview: preview}, err
}

func (s *Service) reserveRecompositionGeneration(
	ctx context.Context,
	userID uint,
	req RecompositionGenerationRequest,
	plan saasstore.RecompositionPlan,
) (saasstore.RecompositionGeneration, error) {
	var existing saasstore.RecompositionGeneration
	err := s.db.WithContext(ctx).Where("owner_user_id = ? AND idempotency_key = ?", userID, req.IdempotencyKey).First(&existing).Error
	if err == nil {
		if existing.PlanID != plan.ID || existing.PlanHash != plan.PlanHash {
			return existing, ErrStaleRecompositionPlan
		}
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return existing, err
	}
	tags := normalizeGenerationTags(req.Tags)
	tagsRaw, err := saasstore.NewJSONB(tags)
	if err != nil {
		return existing, err
	}
	var perturbationAncestors int64
	sourceIDs := s.db.Model(&saasstore.RecompositionPlanSegment{}).Select("source_version_id").Where("plan_id = ?", plan.ID)
	if err := s.db.WithContext(ctx).Model(&saasstore.MarketDataVersion{}).
		Where("(id = ? OR id IN (?)) AND (artifact_kind = ? OR has_perturbation_ancestor = ?)", plan.CalendarVersionID, sourceIDs, marketversion.ArtifactKindLocalPerturbation, true).
		Count(&perturbationAncestors).Error; err != nil {
		return existing, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		var generation saasstore.RecompositionGeneration
		err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var series saasstore.MarketSeries
			find := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("owner_user_id = ? AND name = ?", userID, req.SeriesName).First(&series)
			if errors.Is(find.Error, gorm.ErrRecordNotFound) {
				series = saasstore.MarketSeries{OwnerUserID: userID, Name: req.SeriesName, Notes: req.Notes, Tags: tagsRaw}
				if err := tx.Create(&series).Error; err != nil {
					return err
				}
			} else if find.Error != nil {
				return find.Error
			} else {
				if series.ArchivedAt != nil {
					return ErrInvalidRecomposition
				}
				if err := tx.Model(&series).Updates(map[string]any{"notes": req.Notes, "tags": tagsRaw}).Error; err != nil {
					return err
				}
			}
			var maxVersion int
			if err := tx.Model(&saasstore.MarketDataVersion{}).
				Where("market_series_id = ?", series.ID).
				Select("COALESCE(MAX(version_number), 0)").Scan(&maxVersion).Error; err != nil {
				return err
			}
			versionNumber := maxVersion + 1
			version := saasstore.MarketDataVersion{
				OwnerUserID: userID, MarketSeriesID: &series.ID, VersionNumber: versionNumber,
				SchemaVersion: marketversion.VersionSchemaVersion, BarSchemaVersion: marketversion.BarSchemaVersion,
				ArtifactKind: marketversion.ArtifactKindSegmentRecomposition, GeneratorVersion: marketversion.RecompositionAlgorithm,
				PrecisionVersion: marketversion.PricePrecisionVersion, Status: marketversion.VersionStatusStaging,
				IntegrityStatus: marketversion.IntegrityPending, ContentHash: plan.ContentHash, PlanHash: plan.PlanHash,
				Plan: append(saasstore.JSONB(nil), plan.CanonicalPlan...), InstrumentID: "pending", DataSource: DataSourceGenerated,
				Symbol: "pending", Market: plan.TargetMarket, Timezone: plan.TargetTimezone, Interval: plan.Interval,
				CalendarID: fmt.Sprintf("version:%d", plan.CalendarVersionID), CalendarVersion: plan.CalendarVersion,
				CalendarHash: plan.CalendarHash, BarCount: plan.TotalOutputBars, StartTimeMs: plan.OutputStartTimeMs,
				EndTimeMs: plan.OutputEndTimeMs, HasPerturbationAncestor: perturbationAncestors > 0, InternalOnly: true, Published: false,
			}
			if err := tx.Create(&version).Error; err != nil {
				return err
			}
			instrumentID := fmt.Sprintf("MV%d", version.ID)
			if err := tx.Model(&version).Updates(map[string]any{"instrument_id": instrumentID, "symbol": instrumentID}).Error; err != nil {
				return err
			}
			version.InstrumentID, version.Symbol = instrumentID, instrumentID
			generation = saasstore.RecompositionGeneration{
				OwnerUserID: userID, IdempotencyKey: req.IdempotencyKey, PlanID: plan.ID, PlanHash: plan.PlanHash,
				MarketSeriesID: series.ID, VersionNumber: versionNumber, OutputVersionID: version.ID,
				Status: marketversion.VersionStatusStaging,
			}
			return tx.Create(&generation).Error
		})
		if err == nil {
			return generation, nil
		}
		if !isUniqueViolation(err) {
			return generation, err
		}
		if findErr := s.db.WithContext(ctx).Where("owner_user_id = ? AND idempotency_key = ?", userID, req.IdempotencyKey).First(&existing).Error; findErr == nil {
			return existing, nil
		}
	}
	return saasstore.RecompositionGeneration{}, err
}

func generationCompositeSpec(generation saasstore.RecompositionGeneration, plan saasstore.RecompositionPlan) (computetask.CompositeSpec, error) {
	input := RecompositionGenerationExecutionInput{
		SchemaVersion: RecompositionGenerationRequestV1, GenerationID: generation.ID,
		PlanID: plan.ID, PlanHash: plan.PlanHash, OutputVersionID: generation.OutputVersionID,
	}
	raw, err := compute.CanonicalJSON(input)
	if err != nil {
		return computetask.CompositeSpec{}, err
	}
	item := func(stage string, units int64) []compute.ManifestItemInput {
		return []compute.ManifestItemInput{{
			Key: stage, CacheKey: fmt.Sprintf("p06-generation:%d:%s", generation.ID, stage),
			Input: json.RawMessage(raw), EstimatedUnits: units,
		}}
	}
	units := int64(plan.TotalOutputBars)
	return computetask.CompositeSpec{
		TaskType: generationTaskType, Title: "K 線片段重組行情正式產生",
		Settings: input, ResearchSettingID: fmt.Sprintf("recomposition-plan:%d", plan.ID), ResearchSettingHash: compute.HashBytes([]byte(plan.PlanHash)),
		Stages: []computetask.StageSpec{
			{Key: "expand", Type: "expand", Order: 1, Title: "重新展開完整 K 線", ExecutorType: RecompositionExpandExecutorType, Settings: input, Items: item("expand", units*2)},
			{Key: "calendar-audit", Type: "calendar-audit", Order: 2, Title: "稽核交易日曆與內容雜湊", ExecutorType: RecompositionAuditExecutorType, Settings: input, DependsOnStageKeys: []string{"expand"}, Items: item("calendar-audit", units)},
			{Key: "publish", Type: "publish", Order: 3, Title: "原子發布不可變版本", ExecutorType: RecompositionPublishExecutorType, Settings: input, DependsOnStageKeys: []string{"calendar-audit"}, Items: item("publish", units)},
		},
	}, nil
}

func (s *Service) RecompositionGeneration(ctx context.Context, userID, generationID uint) (RecompositionGenerationResult, error) {
	return s.recompositionGenerationResult(ctx, generationID, userID)
}

func (s *Service) recompositionGenerationResult(ctx context.Context, generationID, userID uint) (RecompositionGenerationResult, error) {
	var generation saasstore.RecompositionGeneration
	if err := s.db.WithContext(ctx).Preload("MarketSeries").Preload("OutputVersion").
		Where("id = ? AND owner_user_id = ?", generationID, userID).First(&generation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return RecompositionGenerationResult{}, ErrRecompositionPlanNotFound
		}
		return RecompositionGenerationResult{}, err
	}
	result := RecompositionGenerationResult{
		SchemaVersion: RecompositionGenerationResultV1, GenerationID: generation.ID,
		PlanID: generation.PlanID, PlanHash: generation.PlanHash, SeriesID: generation.MarketSeriesID,
		SeriesName: generation.MarketSeries.Name, VersionID: generation.OutputVersionID, VersionNumber: generation.VersionNumber,
		ContentHash: generation.OutputVersion.ContentHash, Status: generation.Status,
		IntegrityStatus: generation.OutputVersion.IntegrityStatus, Published: generation.OutputVersion.Published,
		ComputeTaskID: generation.ComputeTaskID,
	}
	if generation.OutputVersion.OutputInstrumentID != nil {
		result.OutputInstrumentID = *generation.OutputVersion.OutputInstrumentID
	}
	result.ExpandedAt = formatOptionalTime(generation.ExpandedAt)
	result.CalendarCheckedAt = formatOptionalTime(generation.CalendarCheckedAt)
	result.PublishedAt = formatOptionalTime(generation.PublishedAt)
	return result, nil
}

func normalizeGenerationTags(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate key")
}
