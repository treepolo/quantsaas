package perturbation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	"quantsaas/internal/marketversion"
	core "quantsaas/internal/perturbation"
	"quantsaas/internal/saas/computetask"
	"quantsaas/internal/saas/marketdata"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) PlanVariants(ctx context.Context, userID, groupID uint, req VariantPlanRequest) (VariantPlan, error) {
	group, snapshot, err := s.loadGroupSnapshot(ctx, userID, groupID)
	if err != nil {
		return VariantPlan{}, err
	}
	if group.ArchivedAt != nil {
		return VariantPlan{}, ErrArchived
	}
	seeds := req.Seeds
	if len(seeds) == 0 && req.SeedCount > 0 {
		seeds, err = generateSeeds(req.SeedCount)
		if err != nil {
			return VariantPlan{}, err
		}
	}
	seedSet := map[string]bool{}
	canonicalSeeds := []string{}
	for _, input := range seeds {
		seed, _, parseErr := core.ParseSeed(input)
		if parseErr != nil {
			return VariantPlan{}, ErrInvalidSeed
		}
		if !seedSet[seed] {
			seedSet[seed] = true
			canonicalSeeds = append(canonicalSeeds, seed)
		}
	}
	alphaSet := map[string]bool{}
	canonicalAlphas := []string{}
	for _, input := range req.Alphas {
		alpha, _, parseErr := core.ParseAlpha(input)
		if parseErr != nil {
			return VariantPlan{}, ErrInvalidAlpha
		}
		if !alphaSet[alpha] {
			alphaSet[alpha] = true
			canonicalAlphas = append(canonicalAlphas, alpha)
		}
	}
	if len(canonicalSeeds) == 0 || len(canonicalAlphas) == 0 {
		return VariantPlan{}, ErrInvalidSeed
	}
	sort.Slice(canonicalSeeds, func(i, j int) bool {
		_, a, _ := core.ParseSeed(canonicalSeeds[i])
		_, b, _ := core.ParseSeed(canonicalSeeds[j])
		return a < b
	})
	sort.Slice(canonicalAlphas, func(i, j int) bool {
		_, a, _ := core.ParseAlpha(canonicalAlphas[i])
		_, b, _ := core.ParseAlpha(canonicalAlphas[j])
		return a < b
	})
	recipes := make([]VariantRecipe, 0, len(canonicalSeeds)*len(canonicalAlphas))
	existing := 0
	for _, seed := range canonicalSeeds {
		for _, alpha := range canonicalAlphas {
			hash, _ := core.RecipeHash(snapshot.SourceContentHash, seed, alpha)
			recipe := VariantRecipe{Seed: seed, Alpha: alpha, RecipeHash: hash}
			var variant saasstore.PerturbationVariant
			findErr := s.db.WithContext(ctx).Where("owner_user_id=? AND generation_recipe_hash=?", userID, hash).First(&variant).Error
			if findErr == nil {
				recipe.VariantID = variant.ID
				if variant.Status == marketversion.VersionStatusCompleted && variant.IntegrityStatus == marketversion.IntegrityValid && s.verifyVariantRecord(ctx, variant) == nil {
					recipe.Reusable = true
					existing++
				}
			} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
				return VariantPlan{}, findErr
			}
			recipes = append(recipes, recipe)
		}
	}
	plan := VariantPlan{SchemaVersion: VariantPlanVersion, GroupID: group.ID, Seeds: canonicalSeeds, Alphas: canonicalAlphas, Recipes: recipes, UniqueVariants: len(recipes), ExistingVariants: existing, PendingVariants: len(recipes) - existing, TotalOutputBars: int64(snapshot.BarCount) * int64(len(recipes)-existing)}
	plan.EstimatedBytes = plan.TotalOutputBars * estimatedBytesPerBar
	hash, _, err := core.CanonicalHash(struct {
		SchemaVersion    string          `json:"schema_version"`
		GroupID          uint            `json:"group_id"`
		SnapshotHash     string          `json:"snapshot_hash"`
		AlgorithmVersion string          `json:"algorithm_version"`
		Recipes          []VariantRecipe `json:"recipes"`
	}{VariantPlanVersion, group.ID, snapshot.SourceContentHash, group.AlgorithmVersion, recipes})
	plan.PlanHash = "perturbation-variant-plan:v1:" + hash
	if s.computeTasks != nil {
		limits := s.computeTasks.Limits()
		plan.RequiresConfirmation = plan.PendingVariants > limits.SoftItemLimit || plan.TotalOutputBars > int64(limits.SoftItemLimit)
	}
	return plan, err
}

func (s *Service) StartVariants(ctx context.Context, userID, groupID uint, req StartVariantsRequest) (VariantTask, error) {
	if s.computeTasks == nil {
		return VariantTask{}, computetask.ErrServiceUnavailable
	}
	plan, err := s.PlanVariants(ctx, userID, groupID, req.PlanRequest)
	if err != nil {
		return VariantTask{}, err
	}
	if plan.PlanHash != strings.TrimSpace(req.PlanHash) {
		return VariantTask{}, ErrStalePlan
	}
	group, snapshot, err := s.loadGroupSnapshot(ctx, userID, groupID)
	if err != nil {
		return VariantTask{}, err
	}
	pending := []saasstore.PerturbationVariant{}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var maxVersion int
		_ = tx.Model(&saasstore.MarketDataVersion{}).Where("market_series_id=?", group.MarketSeriesID).Select("COALESCE(MAX(version_number),0)").Scan(&maxVersion).Error
		for _, recipe := range plan.Recipes {
			if recipe.Reusable {
				continue
			}
			var variant saasstore.PerturbationVariant
			if err := tx.Where("owner_user_id=? AND generation_recipe_hash=?", userID, recipe.RecipeHash).First(&variant).Error; err == nil {
				if variant.Status == marketversion.VersionStatusCorrupt {
					return ErrRecipeConflict
				}
				pending = append(pending, variant)
				continue
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			maxVersion++
			instrumentID := internalID("LVR", recipe.RecipeHash)
			instrument := saasstore.ResearchInstrument{ID: instrumentID, Symbol: instrumentID, DisplayName: fmt.Sprintf("擾動版本 %s × %s", recipe.Seed, recipe.Alpha), DataSource: marketdata.DataSourceGenerated, SupportedIntervals: mustJSON([]string{snapshot.Interval}), AvailableStartMs: mustJSON(map[string]int64{snapshot.Interval: snapshot.StartTimeMs}), Market: "research", SortOrder: 900001, Enabled: true, InternalOnly: true}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&instrument).Error; err != nil {
				return err
			}
			output := instrumentID
			version := saasstore.MarketDataVersion{OwnerUserID: userID, MarketSeriesID: &group.MarketSeriesID, VersionNumber: maxVersion, SchemaVersion: marketversion.VersionSchemaVersion, BarSchemaVersion: marketversion.BarSchemaVersion, ArtifactKind: marketversion.ArtifactKindLocalPerturbation, GeneratorVersion: core.AlgorithmVersion, PrecisionVersion: marketversion.PricePrecisionVersion, Status: marketversion.VersionStatusStaging, IntegrityStatus: marketversion.IntegrityPending, ContentHash: "", PlanHash: recipe.RecipeHash, Plan: mustJSON(recipe), InstrumentID: instrumentID, DataSource: marketdata.DataSourceGenerated, Symbol: instrumentID, Market: "research", Timezone: "UTC", Interval: snapshot.Interval, CalendarID: fmt.Sprintf("snapshot:%d", snapshot.ID), CalendarVersion: marketversion.CalendarFromVersionVersion, CalendarHash: snapshot.SourceContentHash, BarCount: snapshot.BarCount, StartTimeMs: snapshot.StartTimeMs, EndTimeMs: snapshot.EndTimeMs, PreviousClosePresent: snapshot.PreviousClosePresent, PreviousClose: snapshot.PreviousClose, HasPerturbationAncestor: true, InternalOnly: true, Published: false, OutputInstrumentID: &output}
			if err := tx.Create(&version).Error; err != nil {
				return err
			}
			variant = saasstore.PerturbationVariant{OwnerUserID: userID, GroupID: group.ID, SourceSnapshotID: snapshot.ID, Seed: recipe.Seed, Alpha: recipe.Alpha, GenerationRecipeHash: recipe.RecipeHash, OutputVersionID: version.ID, OutputInstrumentID: instrumentID, Status: marketversion.VersionStatusStaging, IntegrityStatus: marketversion.IntegrityPending, BarCount: snapshot.BarCount}
			if err := tx.Create(&variant).Error; err != nil {
				return err
			}
			if err := tx.Create(&saasstore.MarketDataVersionSource{VersionID: version.ID, SourceVersionID: snapshot.SourceVersionID, SourceOrder: 0, SourceRole: "perturbation_source", SourceHash: snapshot.SourceContentHash}).Error; err != nil {
				return err
			}
			pending = append(pending, variant)
		}
		return nil
	})
	if err != nil {
		return VariantTask{}, err
	}
	items := make([]compute.ManifestItemInput, 0, len(pending))
	for _, variant := range pending {
		input, _ := compute.CanonicalJSON(VariantExecutionInput{SchemaVersion: VariantPlanVersion, VariantID: variant.ID, RecipeHash: variant.GenerationRecipeHash})
		items = append(items, compute.ManifestItemInput{Key: fmt.Sprintf("variant-%d", variant.ID), CacheKey: "perturbation-variant:" + variant.GenerationRecipeHash, Input: input, EstimatedUnits: int64(snapshot.BarCount)})
	}
	spec := computetask.CreateSpec{TaskType: "perturbation_variant_generation", Title: "局部行情擾動版本產生", ExecutorType: VariantExecutorType, Settings: map[string]any{"schema_version": VariantPlanVersion, "group_id": group.ID, "plan_hash": plan.PlanHash}, ResearchSettingID: fmt.Sprintf("perturbation-group:%d", group.ID), ResearchSettingHash: compute.HashBytes([]byte(plan.PlanHash)), Items: items}
	preview, err := s.computeTasks.Preview(ctx, userID, spec)
	if err != nil {
		return VariantTask{}, err
	}
	task, err := s.computeTasks.Create(ctx, userID, spec, req.ConfirmSoftLimit)
	if err != nil {
		return VariantTask{}, err
	}
	if len(pending) > 0 {
		ids := make([]uint, 0, len(pending))
		for _, v := range pending {
			ids = append(ids, v.ID)
		}
		_ = s.db.WithContext(ctx).Model(&saasstore.PerturbationVariant{}).Where("id IN ? AND compute_task_id IS NULL", ids).Update("compute_task_id", task.ID).Error
		_ = s.db.WithContext(ctx).Model(&saasstore.MarketDataVersion{}).Where("id IN (?)", s.db.Model(&saasstore.PerturbationVariant{}).Select("output_version_id").Where("id IN ?", ids)).Update("compute_task_id", task.ID).Error
	}
	return VariantTask{Plan: plan, Task: task, Preview: preview}, nil
}

func (s *Service) loadGroupSnapshot(ctx context.Context, userID, groupID uint) (saasstore.PerturbationGroup, saasstore.PerturbationSourceSnapshot, error) {
	var group saasstore.PerturbationGroup
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", groupID, userID).First(&group).Error; err != nil {
		return group, saasstore.PerturbationSourceSnapshot{}, ErrNotFound
	}
	var snapshot saasstore.PerturbationSourceSnapshot
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=? AND status=?", group.SourceSnapshotID, userID, marketversion.VersionStatusCompleted).First(&snapshot).Error; err != nil {
		return group, snapshot, ErrNotFound
	}
	return group, snapshot, nil
}

func (s *Service) ListVariants(ctx context.Context, userID, groupID uint, includeArchived bool) ([]VariantDescriptor, error) {
	if _, _, err := s.loadGroupSnapshot(ctx, userID, groupID); err != nil {
		return nil, err
	}
	var rows []saasstore.PerturbationVariant
	q := s.db.WithContext(ctx).Where("owner_user_id=? AND group_id=?", userID, groupID)
	if !includeArchived {
		q = q.Where("archived_at IS NULL")
	}
	if err := q.Order("alpha ASC,seed ASC,id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]VariantDescriptor, 0, len(rows))
	for _, row := range rows {
		out = append(out, variantDescriptor(row))
	}
	return out, nil
}
func (s *Service) GetVariant(ctx context.Context, userID, variantID uint) (VariantDescriptor, error) {
	var row saasstore.PerturbationVariant
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", variantID, userID).First(&row).Error; err != nil {
		return VariantDescriptor{}, ErrNotFound
	}
	return variantDescriptor(row), nil
}
func variantDescriptor(row saasstore.PerturbationVariant) VariantDescriptor {
	return VariantDescriptor{ID: row.ID, GroupID: row.GroupID, Seed: row.Seed, Alpha: row.Alpha, RecipeHash: row.GenerationRecipeHash, OutputVersionID: row.OutputVersionID, OutputInstrumentID: row.OutputInstrumentID, GeneratedContentHash: row.GeneratedContentHash, Status: row.Status, IntegrityStatus: row.IntegrityStatus, BarCount: row.BarCount, Deviation: core.DeviationSummary{Median: row.DeviationMedian, P95: row.DeviationP95, Maximum: row.DeviationMaximum, OpenMax: row.DeviationOpenMax, HighMax: row.DeviationHighMax, LowMax: row.DeviationLowMax, CloseMax: row.DeviationCloseMax}, ComputeTaskID: row.ComputeTaskID, Archived: row.ArchivedAt != nil, ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)}
}

func (s *Service) VerifyVariant(ctx context.Context, userID, variantID uint) (VariantDescriptor, error) {
	var variant saasstore.PerturbationVariant
	if err := s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", variantID, userID).First(&variant).Error; err != nil {
		return VariantDescriptor{}, ErrNotFound
	}
	if err := s.verifyVariantRecord(ctx, variant); err != nil {
		_ = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if updateErr := tx.Model(&saasstore.PerturbationVariant{}).Where("id=?", variant.ID).Updates(map[string]any{"status": marketversion.VersionStatusCorrupt, "integrity_status": marketversion.IntegrityCorrupt, "error_code": "content_hash_mismatch", "error_message": err.Error()}).Error; updateErr != nil {
				return updateErr
			}
			return tx.Model(&saasstore.MarketDataVersion{}).Where("id=?", variant.OutputVersionID).Updates(map[string]any{"status": marketversion.VersionStatusCorrupt, "integrity_status": marketversion.IntegrityCorrupt, "published": false, "error_code": "content_hash_mismatch", "error_message": err.Error()}).Error
		})
		return s.GetVariant(ctx, userID, variantID)
	}
	return s.GetVariant(ctx, userID, variantID)
}
func (s *Service) verifyVariantRecord(ctx context.Context, variant saasstore.PerturbationVariant) error {
	if variant.Status != marketversion.VersionStatusCompleted || variant.IntegrityStatus != marketversion.IntegrityValid || variant.GeneratedContentHash == "" {
		return ErrContentMismatch
	}
	var version saasstore.MarketDataVersion
	if err := s.db.WithContext(ctx).First(&version, variant.OutputVersionID).Error; err != nil {
		return err
	}
	var rows []saasstore.MarketDataVersionBar
	if err := s.db.WithContext(ctx).Where("version_id=?", version.ID).Order("ordinal ASC").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) != variant.BarCount || len(rows) != version.BarCount {
		return ErrContentMismatch
	}
	identity := core.SourceIdentity{InstrumentID: version.InstrumentID, DataSource: version.DataSource, Symbol: version.Symbol, Interval: version.Interval}
	hash, err := core.GeneratedContentHash(identity, versionRows(rows))
	if err != nil {
		return err
	}
	if hash != variant.GeneratedContentHash || hash != version.ContentHash {
		return ErrContentMismatch
	}
	return nil
}

type VariantExecutor struct{ db *gorm.DB }

func NewVariantExecutor(db *gorm.DB) *VariantExecutor { return &VariantExecutor{db: db} }
func (e *VariantExecutor) Descriptor() compute.ExecutorDescriptor {
	return compute.ExecutorDescriptor{Type: VariantExecutorType, Version: VariantExecutorVersion, ResultSchemaVersion: VariantResultVersion}
}
func (e *VariantExecutor) Execute(ctx context.Context, execution computetask.Execution) (json.RawMessage, error) {
	var input VariantExecutionInput
	if json.Unmarshal(execution.Input, &input) != nil || input.SchemaVersion != VariantPlanVersion || input.VariantID == 0 {
		return nil, ErrRecipeConflict
	}
	var variant saasstore.PerturbationVariant
	var snapshot saasstore.PerturbationSourceSnapshot
	var output saasstore.MarketDataVersion
	if e.db.WithContext(ctx).Where("id=? AND owner_user_id=? AND generation_recipe_hash=?", input.VariantID, execution.UserID, input.RecipeHash).First(&variant).Error != nil {
		return nil, ErrNotFound
	}
	if variant.Status == marketversion.VersionStatusCompleted && variant.IntegrityStatus == marketversion.IntegrityValid {
		return compute.CanonicalJSON(VariantExecutionResult{SchemaVersion: VariantResultVersion, VariantID: variant.ID, VersionID: variant.OutputVersionID, ContentHash: variant.GeneratedContentHash})
	}
	if e.db.WithContext(ctx).First(&snapshot, variant.SourceSnapshotID).Error != nil || e.db.WithContext(ctx).First(&output, variant.OutputVersionID).Error != nil {
		return nil, ErrNotFound
	}
	var sourceRows []saasstore.MarketDataVersionBar
	if err := e.db.WithContext(ctx).Where("version_id=?", snapshot.SourceVersionID).Order("ordinal ASC").Find(&sourceRows).Error; err != nil {
		return nil, err
	}
	if len(sourceRows) != snapshot.BarCount {
		return nil, ErrContentMismatch
	}
	_, seed, err := core.ParseSeed(variant.Seed)
	if err != nil {
		return nil, ErrInvalidSeed
	}
	_, alpha, err := core.ParseAlpha(variant.Alpha)
	if err != nil {
		return nil, ErrInvalidAlpha
	}
	identity := core.SourceIdentity{InstrumentID: snapshot.OriginalInstrumentID, DataSource: snapshot.OriginalDataSource, Symbol: snapshot.OriginalSymbol, Interval: snapshot.Interval}
	var previous *float64
	if snapshot.PreviousClosePresent {
		value := snapshot.PreviousClose
		previous = &value
	}
	if err := e.db.WithContext(ctx).Model(&variant).Updates(map[string]any{"status": marketversion.VersionStatusStaging, "error_code": "", "error_message": ""}).Error; err != nil {
		return nil, err
	}
	if execution.Report != nil {
		_ = execution.Report(ctx, computetask.ProgressUpdate{Progress: .05})
	}
	generated, err := core.Generate(identity, versionRows(sourceRows), previous, seed, alpha)
	if err != nil {
		e.failVariant(variant, err)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		e.cancelVariant(variant, err)
		return nil, err
	}
	target := core.SourceIdentity{InstrumentID: output.InstrumentID, DataSource: output.DataSource, Symbol: output.Symbol, Interval: output.Interval}
	contentHash, err := core.GeneratedContentHash(target, generated.Bars)
	if err != nil {
		e.failVariant(variant, err)
		return nil, err
	}
	now := time.Now().UTC()
	err = e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked saasstore.PerturbationVariant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, variant.ID).Error; err != nil {
			return err
		}
		if locked.Status == marketversion.VersionStatusCompleted {
			return nil
		}
		if err := tx.Where("version_id=?", locked.OutputVersionID).Delete(&saasstore.MarketDataVersionBar{}).Error; err != nil {
			return err
		}
		rows := make([]saasstore.MarketDataVersionBar, 0, len(generated.Bars))
		for index, bar := range generated.Bars {
			rows = append(rows, saasstore.MarketDataVersionBar{VersionID: locked.OutputVersionID, Ordinal: index, OpenTime: bar.OpenTime, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume})
		}
		if err := tx.CreateInBatches(&rows, 1000).Error; err != nil {
			return err
		}
		summary := generated.Summary
		if err := tx.Model(&saasstore.MarketDataVersion{}).Where("id=?", locked.OutputVersionID).Updates(map[string]any{"status": marketversion.VersionStatusCompleted, "integrity_status": marketversion.IntegrityValid, "content_hash": contentHash, "published": true, "completed_at": now, "error_code": "", "error_message": ""}).Error; err != nil {
			return err
		}
		return tx.Model(&locked).Updates(map[string]any{"status": marketversion.VersionStatusCompleted, "integrity_status": marketversion.IntegrityValid, "generated_content_hash": contentHash, "deviation_median": summary.Median, "deviation_p95": summary.P95, "deviation_maximum": summary.Maximum, "deviation_open_max": summary.OpenMax, "deviation_high_max": summary.HighMax, "deviation_low_max": summary.LowMax, "deviation_close_max": summary.CloseMax, "completed_at": now, "error_code": "", "error_message": ""}).Error
	})
	if err != nil {
		e.failVariant(variant, err)
		return nil, err
	}
	if execution.Report != nil {
		_ = execution.Report(ctx, computetask.ProgressUpdate{Progress: 1})
	}
	return compute.CanonicalJSON(VariantExecutionResult{SchemaVersion: VariantResultVersion, VariantID: variant.ID, VersionID: variant.OutputVersionID, ContentHash: contentHash})
}
func (e *VariantExecutor) failVariant(v saasstore.PerturbationVariant, err error) {
	_ = e.db.WithContext(context.Background()).Model(&saasstore.PerturbationVariant{}).Where("id=?", v.ID).Updates(map[string]any{"status": marketversion.VersionStatusFailed, "error_code": "generation_failed", "error_message": err.Error()}).Error
}
func (e *VariantExecutor) cancelVariant(v saasstore.PerturbationVariant, err error) {
	_ = e.db.WithContext(context.Background()).Model(&saasstore.PerturbationVariant{}).Where("id=?", v.ID).Updates(map[string]any{"status": marketversion.VersionStatusCancelled, "error_code": "cancelled", "error_message": err.Error()}).Error
}
func (e *VariantExecutor) ValidateCachedResult(ctx context.Context, userID uint, raw json.RawMessage) error {
	var result VariantExecutionResult
	if json.Unmarshal(raw, &result) != nil || result.SchemaVersion != VariantResultVersion {
		return ErrContentMismatch
	}
	var variant saasstore.PerturbationVariant
	if e.db.WithContext(ctx).Where("id=? AND owner_user_id=? AND output_version_id=? AND generated_content_hash=? AND status=? AND integrity_status=?", result.VariantID, userID, result.VersionID, result.ContentHash, marketversion.VersionStatusCompleted, marketversion.IntegrityValid).First(&variant).Error != nil {
		return ErrContentMismatch
	}
	return nil
}
