package perturbation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	compute "quantsaas/internal/compute"
	"quantsaas/internal/marketversion"
	core "quantsaas/internal/perturbation"
	"quantsaas/internal/quant"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/computetask"
	dynamicparamsvc "quantsaas/internal/saas/dynamicparam"
	parameterresearchsvc "quantsaas/internal/saas/parameterresearch"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) PlanBatch(ctx context.Context, userID, testID uint, request BatchPlanRequest) (BatchPlan, error) {
	var test saasstore.PerturbationTest
	if s.db.WithContext(ctx).Where("id=? AND owner_user_id=? AND archived_at IS NULL", testID, userID).First(&test).Error != nil {
		return BatchPlan{}, ErrNotFound
	}
	var subjects []saasstore.PerturbationTestSubject
	if err := s.db.WithContext(ctx).Where("test_id=?", test.ID).Order("ordinal ASC").Find(&subjects).Error; err != nil {
		return BatchPlan{}, err
	}
	if len(subjects) == 0 {
		return BatchPlan{}, ErrIncompatibleSubject
	}
	seedFilter, err := canonicalSeedFilter(request.Seeds)
	if err != nil {
		return BatchPlan{}, err
	}
	alphaFilter, err := canonicalAlphaFilter(request.Alphas)
	if err != nil {
		return BatchPlan{}, err
	}
	var variants []saasstore.PerturbationVariant
	q := s.db.WithContext(ctx).Where("group_id=? AND owner_user_id=? AND status=? AND integrity_status=? AND archived_at IS NULL", test.GroupID, userID, marketversion.VersionStatusCompleted, marketversion.IntegrityValid)
	if len(seedFilter) > 0 {
		q = q.Where("seed IN ?", seedFilter)
	}
	if len(alphaFilter) > 0 {
		q = q.Where("alpha IN ?", alphaFilter)
	}
	if err := q.Order("alpha ASC,seed ASC,id ASC").Find(&variants).Error; err != nil {
		return BatchPlan{}, err
	}
	found := map[string]bool{}
	variantIDs := make([]uint, 0, len(variants))
	recipes := make([]map[string]any, 0, len(variants))
	for _, variant := range variants {
		found[variant.Seed+"|"+variant.Alpha] = true
		variantIDs = append(variantIDs, variant.ID)
		recipes = append(recipes, map[string]any{"variant_id": variant.ID, "recipe_hash": variant.GenerationRecipeHash, "seed": variant.Seed, "alpha": variant.Alpha, "content_hash": variant.GeneratedContentHash})
	}
	missing := []VariantRecipe{}
	if len(seedFilter) > 0 && len(alphaFilter) > 0 {
		for _, seed := range seedFilter {
			for _, alpha := range alphaFilter {
				if !found[seed+"|"+alpha] {
					hash, _ := core.RecipeHash("missing", seed, alpha)
					missing = append(missing, VariantRecipe{Seed: seed, Alpha: alpha, RecipeHash: hash})
				}
			}
		}
	}
	if len(missing) > 0 {
		return BatchPlan{SchemaVersion: VariantPlanVersion, TestID: test.ID, Seeds: seedFilter, Alphas: alphaFilter, VariantIDs: variantIDs, SubjectCount: len(subjects), DatasetCount: 1 + len(variants), MissingVariants: missing}, ErrMissingVariant
	}
	planned := len(subjects) * (1 + len(variants))
	existing := 0
	for _, subject := range subjects {
		datasetHashes := []string{}
		var snapshot saasstore.PerturbationSourceSnapshot
		if s.db.WithContext(ctx).Joins("JOIN perturbation_groups ON perturbation_groups.source_snapshot_id = perturbation_source_snapshots.id").Where("perturbation_groups.id=?", test.GroupID).First(&snapshot).Error != nil {
			return BatchPlan{}, ErrNotFound
		}
		var sourceVersion saasstore.MarketDataVersion
		if s.db.WithContext(ctx).First(&sourceVersion, snapshot.SourceVersionID).Error != nil {
			return BatchPlan{}, ErrNotFound
		}
		datasetHashes = append(datasetHashes, sourceVersion.ContentHash)
		for _, variant := range variants {
			datasetHashes = append(datasetHashes, variant.GeneratedContentHash)
		}
		for _, datasetHash := range datasetHashes {
			specHash := runSpecHash(subject.SubjectHash, datasetHash, test.BacktestSettings)
			var count int64
			_ = s.db.WithContext(ctx).Model(&saasstore.PerturbationTestRun{}).Where("subject_id=? AND dataset_content_hash=? AND backtest_spec_hash=? AND status=?", subject.ID, datasetHash, specHash, saasstore.BacktestResultStatusCompleted).Count(&count).Error
			if count > 0 {
				existing++
			}
		}
	}
	manifest := map[string]any{"schema_version": TestSchemaVersion, "test_id": test.ID, "test_spec_hash": test.TestSpecHash, "subjects": subjectIDs(subjects), "source_baseline": true, "variants": recipes}
	manifestHash, _, err := core.CanonicalHash(manifest)
	if err != nil {
		return BatchPlan{}, err
	}
	planHash, _, err := core.CanonicalHash(map[string]any{"manifest_hash": manifestHash, "seeds": seedFilter, "alphas": alphaFilter})
	if err != nil {
		return BatchPlan{}, err
	}
	plan := BatchPlan{SchemaVersion: TestSchemaVersion, TestID: test.ID, PlanHash: "perturbation-batch-plan:v1:" + planHash, ManifestHash: "perturbation-manifest:v1:" + manifestHash, Seeds: seedFilter, Alphas: alphaFilter, VariantIDs: variantIDs, SubjectCount: len(subjects), DatasetCount: 1 + len(variants), PlannedRuns: planned, ExistingRuns: existing, PendingRuns: planned - existing}
	if s.computeTasks != nil {
		limits := s.computeTasks.Limits()
		plan.RequiresConfirmation = plan.PendingRuns > limits.SoftItemLimit
	}
	return plan, nil
}

func (s *Service) StartBatch(ctx context.Context, userID, testID uint, request StartBatchRequest) (BatchTask, error) {
	if s.computeTasks == nil {
		return BatchTask{}, computetask.ErrServiceUnavailable
	}
	plan, err := s.PlanBatch(ctx, userID, testID, BatchPlanRequest{Seeds: request.Seeds, Alphas: request.Alphas})
	if err != nil {
		return BatchTask{}, err
	}
	if plan.PlanHash != strings.TrimSpace(request.PlanHash) {
		return BatchTask{}, ErrStalePlan
	}
	var test saasstore.PerturbationTest
	var subjects []saasstore.PerturbationTestSubject
	var snapshot saasstore.PerturbationSourceSnapshot
	var variants []saasstore.PerturbationVariant
	if s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", testID, userID).First(&test).Error != nil {
		return BatchTask{}, ErrNotFound
	}
	_ = s.db.WithContext(ctx).Where("test_id=?", test.ID).Order("ordinal ASC").Find(&subjects).Error
	if s.db.WithContext(ctx).Joins("JOIN perturbation_groups ON perturbation_groups.source_snapshot_id=perturbation_source_snapshots.id").Where("perturbation_groups.id=?", test.GroupID).First(&snapshot).Error != nil {
		return BatchTask{}, ErrNotFound
	}
	if len(plan.VariantIDs) > 0 {
		_ = s.db.WithContext(ctx).Where("id IN ?", plan.VariantIDs).Order("alpha ASC,seed ASC,id ASC").Find(&variants).Error
	}
	manifestRaw, _ := compute.CanonicalJSON(map[string]any{"schema_version": TestSchemaVersion, "manifest_hash": plan.ManifestHash, "source_version_id": snapshot.SourceVersionID, "variant_ids": plan.VariantIDs, "subject_ids": subjectIDs(subjects), "planned_runs": plan.PlannedRuns})
	var batch saasstore.PerturbationTestBatch
	newRuns := []saasstore.PerturbationTestRun{}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked saasstore.PerturbationTest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, test.ID).Error; err != nil {
			return err
		}
		var maxOrdinal int
		_ = tx.Model(&saasstore.PerturbationTestBatch{}).Where("test_id=?", test.ID).Select("COALESCE(MAX(ordinal),0)").Scan(&maxOrdinal).Error
		batch = saasstore.PerturbationTestBatch{TestID: test.ID, Ordinal: maxOrdinal + 1, ManifestHash: plan.ManifestHash, Manifest: manifestRaw, Status: "running", PlannedCount: plan.PlannedRuns, MissingCount: plan.PendingRuns, CacheHitCount: plan.ExistingRuns}
		created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "test_id"}, {Name: "manifest_hash"}}, DoNothing: true}).Create(&batch)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			if err := tx.Where("test_id=? AND manifest_hash=?", test.ID, plan.ManifestHash).First(&batch).Error; err != nil {
				return err
			}
			return nil
		}
		datasets := []struct {
			versionID         uint
			hash, alpha, seed string
		}{{snapshot.SourceVersionID, snapshot.SourceContentHash, "0", ""}}
		for _, variant := range variants {
			datasets = append(datasets, struct {
				versionID         uint
				hash, alpha, seed string
			}{variant.OutputVersionID, variant.GeneratedContentHash, variant.Alpha, variant.Seed})
		}
		for _, subject := range subjects {
			for _, dataset := range datasets {
				specHash := runSpecHash(subject.SubjectHash, dataset.hash, test.BacktestSettings)
				run := saasstore.PerturbationTestRun{TestID: test.ID, BatchID: batch.ID, SubjectID: subject.ID, DatasetVersionID: dataset.versionID, DatasetContentHash: dataset.hash, Alpha: dataset.alpha, Seed: dataset.seed, BacktestSpecHash: specHash, Status: "pending", Metrics: saasstore.JSONB(`{}`)}
				created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "subject_id"}, {Name: "dataset_content_hash"}, {Name: "backtest_spec_hash"}}, DoNothing: true}).Create(&run)
				if created.Error != nil {
					return created.Error
				}
				if created.RowsAffected > 0 {
					newRuns = append(newRuns, run)
				}
			}
		}
		return tx.Model(&locked).Updates(map[string]any{"status": "running", "error_message": ""}).Error
	})
	if err != nil {
		return BatchTask{}, err
	}
	if len(newRuns) == 0 {
		now := time.Now().UTC()
		_ = s.db.WithContext(ctx).Model(&batch).Updates(map[string]any{"status": "completed", "completed_count": batch.PlannedCount, "missing_count": 0, "completed_at": now}).Error
		_ = s.createAnalysisSnapshot(ctx, userID, test.ID)
		return BatchTask{BatchID: batch.ID, Plan: plan}, nil
	}
	items := make([]compute.ManifestItemInput, 0, len(newRuns))
	for _, run := range newRuns {
		raw, _ := compute.CanonicalJSON(RunExecutionInput{SchemaVersion: TestSchemaVersion, RunID: run.ID})
		items = append(items, compute.ManifestItemInput{Key: fmt.Sprintf("run-%d", run.ID), CacheKey: fmt.Sprintf("perturbation-run:%d:%s", run.ID, run.BacktestSpecHash), Input: raw, EstimatedUnits: 1})
	}
	spec := computetask.CreateSpec{TaskType: "perturbation_backtest_batch", Title: "擾動行情批次回測", ExecutorType: RunExecutorType, Settings: map[string]any{"schema_version": TestSchemaVersion, "test_id": test.ID, "batch_id": batch.ID, "manifest_hash": plan.ManifestHash}, ResearchSettingID: fmt.Sprintf("perturbation-test:%d", test.ID), ResearchSettingHash: compute.HashBytes([]byte(test.TestSpecHash)), Items: items}
	preview, err := s.computeTasks.Preview(ctx, userID, spec)
	if err != nil {
		return BatchTask{}, err
	}
	task, err := s.computeTasks.Create(ctx, userID, spec, request.ConfirmSoftLimit)
	if err != nil {
		return BatchTask{}, err
	}
	_ = s.db.WithContext(ctx).Model(&batch).Update("compute_task_id", task.ID).Error
	candidateIDs := []uint{}
	for _, subject := range subjects {
		if subject.CandidateID != nil {
			candidateIDs = append(candidateIDs, *subject.CandidateID)
		}
	}
	if len(candidateIDs) > 0 {
		_ = s.db.WithContext(ctx).Model(&saasstore.CandidateAnalysisLink{}).Where("candidate_id IN ? AND analysis_kind=? AND version=?", candidateIDs, "L", candidateAnalysisVersion).Updates(map[string]any{"status": "running", "task_id": task.ID}).Error
	}
	return BatchTask{BatchID: batch.ID, Plan: plan, Task: task, Preview: preview}, nil
}

func (s *Service) syncTest(ctx context.Context, userID, testID uint) error {
	if s.computeTasks == nil {
		return computetask.ErrServiceUnavailable
	}
	var test saasstore.PerturbationTest
	if s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", testID, userID).First(&test).Error != nil {
		return ErrNotFound
	}
	var batches []saasstore.PerturbationTestBatch
	if err := s.db.WithContext(ctx).Where("test_id=? AND compute_task_id IS NOT NULL AND status IN ?", test.ID, []string{"running", "partially_completed"}).Find(&batches).Error; err != nil {
		return err
	}
	for _, batch := range batches {
		task, err := s.computeTasks.Get(ctx, userID, *batch.ComputeTaskID)
		if err != nil {
			return err
		}
		if task.Status == compute.TaskStatusCompleted || task.Status == compute.TaskStatusFailed || task.Status == compute.TaskStatusCancelled {
			var completed, failed int64
			_ = s.db.WithContext(ctx).Model(&saasstore.PerturbationTestRun{}).Where("batch_id=? AND status=?", batch.ID, saasstore.BacktestResultStatusCompleted).Count(&completed).Error
			_ = s.db.WithContext(ctx).Model(&saasstore.PerturbationTestRun{}).Where("batch_id=? AND status IN ?", batch.ID, []string{saasstore.BacktestResultStatusFailed, saasstore.BacktestResultStatusCancelled}).Count(&failed).Error
			completedTotal := int(completed) + batch.CacheHitCount
			status := "partially_completed"
			if completedTotal >= batch.PlannedCount {
				status = "completed"
			} else if task.Status == compute.TaskStatusCancelled {
				status = "cancelled"
			} else if completed == 0 && failed > 0 {
				status = "failed"
			}
			now := time.Now().UTC()
			_ = s.db.WithContext(ctx).Model(&batch).Updates(map[string]any{"status": status, "completed_count": completedTotal, "failed_count": failed, "missing_count": maxInt(0, batch.PlannedCount-completedTotal-int(failed)), "completed_at": now, "error_message": task.Error}).Error
		}
	}
	var active int64
	_ = s.db.WithContext(ctx).Model(&saasstore.PerturbationTestBatch{}).Where("test_id=? AND status=?", test.ID, "running").Count(&active).Error
	if active == 0 {
		if err := s.createAnalysisSnapshot(ctx, userID, test.ID); err != nil {
			return err
		}
	}
	return nil
}

func canonicalSeedFilter(values []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		canonical, _, err := core.ParseSeed(v)
		if err != nil {
			return nil, ErrInvalidSeed
		}
		if !seen[canonical] {
			seen[canonical] = true
			out = append(out, canonical)
		}
	}
	sort.Strings(out)
	return out, nil
}
func canonicalAlphaFilter(values []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		canonical, _, err := core.ParseAlpha(v)
		if err != nil {
			return nil, ErrInvalidAlpha
		}
		if !seen[canonical] {
			seen[canonical] = true
			out = append(out, canonical)
		}
	}
	sort.Strings(out)
	return out, nil
}
func subjectIDs(subjects []saasstore.PerturbationTestSubject) []uint {
	out := make([]uint, 0, len(subjects))
	for _, s := range subjects {
		out = append(out, s.ID)
	}
	return out
}
func runSpecHash(subjectHash, datasetHash string, settings saasstore.JSONB) string {
	hash, _, _ := core.CanonicalHash(map[string]any{"subject_hash": subjectHash, "dataset_hash": datasetHash, "backtest_settings": json.RawMessage(settings), "metric_version": core.MetricVersion})
	return "perturbation-run-spec:v1:" + hash
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type RunExecutor struct {
	db        *gorm.DB
	backtests *backtest.Service
	dynamic   *dynamicparamsvc.MaterializeExecutor
}

func NewRunExecutor(db *gorm.DB, backtests *backtest.Service) *RunExecutor {
	if backtests == nil {
		backtests = backtest.NewService(db)
	}
	return &RunExecutor{db: db, backtests: backtests, dynamic: dynamicparamsvc.NewMaterializeExecutor(db, backtests)}
}
func (e *RunExecutor) Descriptor() compute.ExecutorDescriptor {
	return compute.ExecutorDescriptor{Type: RunExecutorType, Version: RunExecutorVersion, ResultSchemaVersion: RunResultVersion}
}
func (e *RunExecutor) Execute(ctx context.Context, execution computetask.Execution) (json.RawMessage, error) {
	var input RunExecutionInput
	if json.Unmarshal(execution.Input, &input) != nil || input.SchemaVersion != TestSchemaVersion {
		return nil, ErrIncompatibleSubject
	}
	var run saasstore.PerturbationTestRun
	var subject saasstore.PerturbationTestSubject
	var version saasstore.MarketDataVersion
	if e.db.WithContext(ctx).Joins("JOIN perturbation_tests ON perturbation_tests.id=perturbation_test_runs.test_id").Where("perturbation_test_runs.id=? AND perturbation_tests.owner_user_id=?", input.RunID, execution.UserID).First(&run).Error != nil || e.db.WithContext(ctx).First(&subject, run.SubjectID).Error != nil || e.db.WithContext(ctx).Where("id=? AND status=? AND integrity_status=? AND published=?", run.DatasetVersionID, marketversion.VersionStatusCompleted, marketversion.IntegrityValid, true).First(&version).Error != nil {
		return nil, ErrNotFound
	}
	var pointInput parameterresearchsvc.PointExecutionInput
	if json.Unmarshal(subject.ExecutionInput, &pointInput) != nil {
		return nil, ErrIncompatibleSubject
	}
	var rows []saasstore.MarketDataVersionBar
	if err := e.db.WithContext(ctx).Where("version_id=?", version.ID).Order("ordinal ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	_ = rows
	pointInput.Backtest.InstrumentID = version.InstrumentID
	pointInput.Backtest.DataSource = version.DataSource
	pointInput.Backtest.Symbol = version.Symbol
	pointInput.Backtest.Pair = version.Symbol
	pointInput.Backtest.Interval = version.Interval
	pointInput.Backtest.MarketDataVersionID = version.ID
	pointInput.Backtest.MarketDataContentHash = version.ContentHash
	var standard backtest.StandardExecutionResult
	if !subject.Dynamic {
		result, err := e.backtests.EnsureStandardResult(ctx, execution.UserID, pointInput.Backtest)
		if err != nil {
			e.failRun(run, err)
			return nil, err
		}
		standard = result
	} else {
		var dynamicInput dynamicparamsvc.MaterializeExecutionInput
		if json.Unmarshal(pointInput.Input, &dynamicInput) != nil {
			return nil, ErrIncompatibleSubject
		}
		datasetHash, err := versionDatasetHash(ctx, e.db, version.ID)
		if err != nil {
			return nil, err
		}
		dynamicInput.Scope = dynamicparamsvc.MarketScope{InstrumentID: version.InstrumentID, DataSource: version.DataSource, Symbol: version.Symbol, Interval: version.Interval, MarketDataVersionID: version.ID, MarketDataContentHash: version.ContentHash, StartTimeMs: pointInput.Backtest.StartTimeMs, EndTimeMs: pointInput.Backtest.EndTimeMs, DatasetHash: datasetHash}
		dynamicInput.Backtest = pointInput.Backtest
		raw, _ := compute.CanonicalJSON(dynamicInput)
		nested := execution
		nested.Input = raw
		resultRaw, err := e.dynamic.Execute(ctx, nested)
		if err != nil {
			e.failRun(run, err)
			return nil, err
		}
		var result dynamicparamsvc.MaterializeExecutionResult
		if json.Unmarshal(resultRaw, &result) != nil {
			return nil, ErrIncompatibleSubject
		}
		var summary saasstore.BacktestResultSummary
		if e.db.WithContext(ctx).Where("backtest_result_id=?", result.BacktestResultID).First(&summary).Error != nil {
			return nil, ErrNotFound
		}
		standard = backtest.StandardExecutionResult{ID: result.BacktestResultID, Version: result.BacktestResultVersion, ContentHash: result.BacktestResultContentHash, Summary: summaryFromRow(summary)}
	}
	metrics := relativeMetrics(standard.Summary)
	metricsRaw, _ := compute.CanonicalJSON(metrics)
	metricsHash := compute.HashBytes(metricsRaw)
	now := time.Now().UTC()
	if err := e.db.WithContext(ctx).Model(&run).Updates(map[string]any{"status": saasstore.BacktestResultStatusCompleted, "backtest_result_id": standard.ID, "backtest_result_version": standard.Version, "backtest_result_content_hash": standard.ContentHash, "reused_result": standard.Reused, "metrics": metricsRaw, "metric_hash": metricsHash, "completed_at": now, "error_code": "", "error_message": ""}).Error; err != nil {
		return nil, err
	}
	return compute.CanonicalJSON(RunExecutionResult{SchemaVersion: RunResultVersion, RunID: run.ID, BacktestResultID: standard.ID, ContentHash: standard.ContentHash})
}
func (e *RunExecutor) failRun(run saasstore.PerturbationTestRun, err error) {
	status := saasstore.BacktestResultStatusFailed
	if errors.Is(err, context.Canceled) {
		status = saasstore.BacktestResultStatusCancelled
	}
	_ = e.db.WithContext(context.Background()).Model(&run).Updates(map[string]any{"status": status, "error_code": "backtest_failed", "error_message": err.Error()}).Error
}
func (e *RunExecutor) ValidateCachedResult(ctx context.Context, userID uint, raw json.RawMessage) error {
	var result RunExecutionResult
	if json.Unmarshal(raw, &result) != nil {
		return ErrContentMismatch
	}
	var count int64
	err := e.db.WithContext(ctx).Model(&saasstore.PerturbationTestRun{}).Joins("JOIN perturbation_tests ON perturbation_tests.id=perturbation_test_runs.test_id").Where("perturbation_test_runs.id=? AND perturbation_test_runs.backtest_result_id=? AND perturbation_test_runs.backtest_result_content_hash=? AND perturbation_test_runs.status=? AND perturbation_tests.owner_user_id=?", result.RunID, result.BacktestResultID, result.ContentHash, saasstore.BacktestResultStatusCompleted, userID).Count(&count).Error
	if err != nil || count != 1 {
		return ErrContentMismatch
	}
	return nil
}

func versionDatasetHash(ctx context.Context, db *gorm.DB, versionID uint) (string, error) {
	var rows []saasstore.MarketDataVersionBar
	if err := db.WithContext(ctx).Where("version_id=?", versionID).Order("ordinal ASC").Find(&rows).Error; err != nil {
		return "", err
	}
	bars := make([]quant.Bar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, quant.Bar{OpenTime: row.OpenTime, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	return backtestresult.HashDataset(backtestresult.DatasetSchemaVersion, bars)
}
func summaryFromRow(row saasstore.BacktestResultSummary) backtestresult.SummaryData {
	var summary backtestresult.SummaryData
	if json.Unmarshal(row.Payload, &summary) == nil {
		return summary
	}
	return backtestresult.SummaryData{SchemaVersion: row.SchemaVersion, ROI: row.ROI, FinalEquity: row.FinalEquity, MaxDrawdown: row.MaxDrawdown, TradeCount: row.TradeCount, ExposureDaysRatio: row.ExposureDaysRatio, AverageActualExposure: row.AverageActualExposure, LongestUnderwaterDays: row.LongestUnderwaterDays, LongestUnderwaterPoints: row.LongestUnderwaterPoints, Sortino: row.Sortino, Beta: row.Beta}
}

type runMetrics struct {
	ParameterFinalNAV    float64              `json:"parameter_final_nav"`
	DCAFinalNAV          float64              `json:"dca_final_nav"`
	ParameterTotalReturn float64              `json:"parameter_total_return"`
	DCATotalReturn       float64              `json:"dca_total_return"`
	ParameterMaxDrawdown float64              `json:"parameter_max_drawdown"`
	DCAMaxDrawdown       float64              `json:"dca_max_drawdown"`
	Relative             core.RelativeMetrics `json:"relative"`
}

func relativeMetrics(summary backtestresult.SummaryData) runMetrics {
	var extra struct {
		BenchmarkFinalEquity float64 `json:"benchmark_final_equity"`
		BenchmarkReturn      float64 `json:"benchmark_return"`
		BenchmarkMaxDrawdown float64 `json:"benchmark_max_drawdown"`
	}
	_ = json.Unmarshal(summary.Extra, &extra)
	return runMetrics{ParameterFinalNAV: summary.FinalEquity, DCAFinalNAV: extra.BenchmarkFinalEquity, ParameterTotalReturn: summary.ROI, DCATotalReturn: extra.BenchmarkReturn, ParameterMaxDrawdown: summary.MaxDrawdown, DCAMaxDrawdown: extra.BenchmarkMaxDrawdown, Relative: core.RelativePerformance(summary.FinalEquity, extra.BenchmarkFinalEquity, summary.ROI, extra.BenchmarkReturn, summary.MaxDrawdown, extra.BenchmarkMaxDrawdown)}
}

func (s *Service) Runs(ctx context.Context, userID, testID uint, limit, offset int) ([]RunDescriptor, error) {
	if _, err := s.GetTest(ctx, userID, testID, false); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var rows []saasstore.PerturbationTestRun
	if err := s.db.WithContext(ctx).Where("test_id=?", testID).Order("subject_id ASC,alpha ASC,seed ASC,id ASC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]RunDescriptor, 0, len(rows))
	for _, row := range rows {
		out = append(out, runDescriptor(row))
	}
	return out, nil
}
func runDescriptor(row saasstore.PerturbationTestRun) RunDescriptor {
	return RunDescriptor{ID: row.ID, TestID: row.TestID, BatchID: row.BatchID, SubjectID: row.SubjectID, DatasetVersionID: row.DatasetVersionID, DatasetContentHash: row.DatasetContentHash, Alpha: row.Alpha, Seed: row.Seed, Status: row.Status, BacktestResultID: row.BacktestResultID, BacktestResultContentHash: row.BacktestResultContentHash, Reused: row.ReusedResult, Metrics: append(json.RawMessage(nil), row.Metrics...), PerformanceReportID: row.PerformanceReportID, ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage}
}

func (s *Service) createAnalysisSnapshot(ctx context.Context, userID, testID uint) error {
	var test saasstore.PerturbationTest
	if s.db.WithContext(ctx).Where("id=? AND owner_user_id=?", testID, userID).First(&test).Error != nil {
		return ErrNotFound
	}
	var runs []saasstore.PerturbationTestRun
	if err := s.db.WithContext(ctx).Where("test_id=?", test.ID).Order("subject_id ASC,alpha ASC,seed ASC,id ASC").Find(&runs).Error; err != nil {
		return err
	}
	if len(runs) == 0 {
		return nil
	}
	var batches []saasstore.PerturbationTestBatch
	_ = s.db.WithContext(ctx).Where("test_id=?", test.ID).Order("ordinal ASC").Find(&batches).Error
	planned := 0
	batchIDs := []uint{}
	for _, batch := range batches {
		planned += batch.PlannedCount
		batchIDs = append(batchIDs, batch.ID)
	}
	valid, failed := 0, 0
	for _, run := range runs {
		if run.Status == saasstore.BacktestResultStatusCompleted {
			valid++
		} else if run.Status == saasstore.BacktestResultStatusFailed || run.Status == saasstore.BacktestResultStatusCancelled {
			failed++
		}
	}
	missing := maxInt(0, planned-valid-failed)
	completeness := "partial"
	if missing == 0 && failed == 0 {
		completeness = "complete"
	}
	setIdentity := []map[string]any{}
	for _, run := range runs {
		setIdentity = append(setIdentity, map[string]any{"id": run.ID, "status": run.Status, "result_hash": run.BacktestResultContentHash, "metric_hash": run.MetricHash})
	}
	setHash, _, _ := core.CanonicalHash(map[string]any{"runs": setIdentity, "planned": planned, "statistics_version": core.StatisticsVersion})
	analysisSetHash := "perturbation-analysis-set:v1:" + setHash
	var existing saasstore.PerturbationAnalysisSnapshot
	if s.db.WithContext(ctx).Where("analysis_set_hash=?", analysisSetHash).First(&existing).Error == nil {
		return nil
	}
	type groupKey struct {
		subject uint
		alpha   string
	}
	values := map[groupKey]map[string][]float64{}
	qualifications := map[groupKey]map[string]int{}
	counts := map[groupKey]struct{ planned, failed, missing int }{}
	baselines := map[uint]runMetrics{}
	for _, run := range runs {
		if run.Alpha == "0" && run.Status == saasstore.BacktestResultStatusCompleted {
			var metrics runMetrics
			if json.Unmarshal(run.Metrics, &metrics) == nil {
				baselines[run.SubjectID] = metrics
			}
		}
	}
	for _, run := range runs {
		key := groupKey{run.SubjectID, run.Alpha}
		count := counts[key]
		count.planned++
		if run.Status != saasstore.BacktestResultStatusCompleted {
			if run.Status == saasstore.BacktestResultStatusFailed || run.Status == saasstore.BacktestResultStatusCancelled {
				count.failed++
			} else {
				count.missing++
			}
			counts[key] = count
			continue
		}
		var metrics runMetrics
		if json.Unmarshal(run.Metrics, &metrics) != nil {
			count.failed++
			counts[key] = count
			continue
		}
		if values[key] == nil {
			values[key] = map[string][]float64{}
		}
		appendMetric := func(name string, value *float64) {
			if value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) {
				values[key][name] = append(values[key][name], *value)
			}
		}
		appendMetric("parameter_total_return", &metrics.ParameterTotalReturn)
		appendMetric("parameter_max_drawdown", &metrics.ParameterMaxDrawdown)
		appendMetric("final_nav_ratio", metrics.Relative.FinalNAVRatio)
		appendMetric("log_final_nav_ratio", metrics.Relative.LogFinalNAVRatio)
		appendMetric("drawdown_residual_ratio", metrics.Relative.DrawdownResidualRatio)
		appendMetric("log_drawdown_residual_ratio", metrics.Relative.LogDrawdownResidualRatio)
		appendMetric("performance_drawdown_composite", metrics.Relative.PerformanceDrawdownComposite)
		if base, ok := baselines[run.SubjectID]; ok && run.Alpha != "0" {
			addDelta := func(name string, value, baseline *float64) {
				if value != nil && baseline != nil {
					delta := *value - *baseline
					absolute := math.Abs(delta)
					appendMetric("signed_delta_"+name, &delta)
					appendMetric("absolute_delta_"+name, &absolute)
				}
			}
			addDelta("log_final_nav_ratio", metrics.Relative.LogFinalNAVRatio, base.Relative.LogFinalNAVRatio)
			addDelta("performance_drawdown_composite", metrics.Relative.PerformanceDrawdownComposite, base.Relative.PerformanceDrawdownComposite)
		}
		if qualifications[key] == nil {
			qualifications[key] = map[string]int{}
		}
		qualifications[key][metrics.Relative.Qualification]++
		counts[key] = count
	}
	summaryRaw, _ := compute.CanonicalJSON(map[string]any{"schema_version": core.AnalysisSchema, "test_id": test.ID, "batch_ids": batchIDs, "planned_count": planned, "valid_count": valid, "failed_count": failed, "missing_count": missing, "completeness": completeness})
	contentHash := compute.HashBytes(summaryRaw)
	snapshot := saasstore.PerturbationAnalysisSnapshot{TestID: test.ID, SnapshotKey: "perturbation-analysis:v1:" + contentHash, SchemaVersion: core.AnalysisSchema, AnalysisSetHash: analysisSetHash, StatisticsVersion: core.StatisticsVersion, Completeness: completeness, IncludedBatches: mustJSON(batchIDs), PlannedCount: planned, ValidCount: valid, FailedCount: failed, MissingCount: missing, ContentHash: contentHash, Summary: summaryRaw}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&snapshot).Error; err != nil {
			return err
		}
		keys := make([]groupKey, 0, len(counts))
		for key := range counts {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].subject == keys[j].subject {
				return keys[i].alpha < keys[j].alpha
			}
			return keys[i].subject < keys[j].subject
		})
		for _, key := range keys {
			count := counts[key]
			metricNames := make([]string, 0, len(values[key]))
			for name := range values[key] {
				metricNames = append(metricNames, name)
			}
			sort.Strings(metricNames)
			for _, name := range metricNames {
				stats := core.Describe(values[key][name])
				raw, _ := compute.CanonicalJSON(stats)
				row := saasstore.PerturbationMetricSummary{AnalysisSnapshotID: snapshot.ID, SubjectID: key.subject, Alpha: key.alpha, MetricKey: name, PlannedCount: count.planned, ValidCount: stats.Count, FailedCount: count.failed, MissingCount: count.missing, Statistics: raw, ContentHash: compute.HashBytes(raw)}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			}
			q := qualifications[key]
			qRaw, _ := compute.CanonicalJSON(q)
			qrow := saasstore.PerturbationQualificationSummary{AnalysisSnapshotID: snapshot.ID, SubjectID: key.subject, Alpha: key.alpha, ValidCount: q[core.QualificationQualified] + q[core.QualificationReturnFailed] + q[core.QualificationDrawdownFailed] + q[core.QualificationBothFailed], QualifiedCount: q[core.QualificationQualified], ReturnFailedCount: q[core.QualificationReturnFailed], DrawdownFailedCount: q[core.QualificationDrawdownFailed], BothFailedCount: q[core.QualificationBothFailed], ContentHash: compute.HashBytes(qRaw)}
			if err := tx.Create(&qrow).Error; err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		status := "partially_completed"
		if completeness == "complete" {
			status = "completed"
		}
		if err := tx.Model(&test).Updates(map[string]any{"latest_snapshot_id": snapshot.ID, "status": status, "completed_at": now}).Error; err != nil {
			return err
		}
		return projectCandidateLinks(tx, test, snapshot, status)
	})
}

func projectCandidateLinks(tx *gorm.DB, test saasstore.PerturbationTest, snapshot saasstore.PerturbationAnalysisSnapshot, status string) error {
	var subjects []saasstore.PerturbationTestSubject
	if err := tx.Where("test_id=? AND candidate_id IS NOT NULL", test.ID).Find(&subjects).Error; err != nil {
		return err
	}
	for _, subject := range subjects {
		partial := mustJSON(map[string]any{"test_id": test.ID, "subject_id": subject.ID, "analysis_snapshot_id": snapshot.ID, "status": status, "content_hash": snapshot.ContentHash, "back_link": fmt.Sprintf("/generator?mode=perturbation&test=%d", test.ID)})
		linkStatus := "partially_completed"
		if status == "completed" {
			linkStatus = "completed"
		}
		if err := tx.Model(&saasstore.CandidateAnalysisLink{}).Where("candidate_id=? AND analysis_kind=? AND version=?", *subject.CandidateID, "L", candidateAnalysisVersion).Updates(map[string]any{"status": linkStatus, "source_id": strconv.FormatUint(uint64(snapshot.ID), 10), "source_version": core.AnalysisSchema, "source_content_hash": snapshot.ContentHash, "partial_snapshot": partial, "error_message": ""}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) AnalysisSnapshots(ctx context.Context, userID, testID uint) ([]AnalysisSnapshotDescriptor, error) {
	if _, err := s.GetTest(ctx, userID, testID, false); err != nil {
		return nil, err
	}
	var rows []saasstore.PerturbationAnalysisSnapshot
	if err := s.db.WithContext(ctx).Where("test_id=?", testID).Order("created_at DESC,id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]AnalysisSnapshotDescriptor, 0, len(rows))
	for _, row := range rows {
		out = append(out, analysisDescriptor(row, false))
	}
	return out, nil
}
func (s *Service) GetAnalysisSnapshot(ctx context.Context, userID, testID, snapshotID uint) (AnalysisSnapshotDescriptor, error) {
	var row saasstore.PerturbationAnalysisSnapshot
	if s.db.WithContext(ctx).Joins("JOIN perturbation_tests ON perturbation_tests.id=perturbation_analysis_snapshots.test_id").Where("perturbation_analysis_snapshots.id=? AND perturbation_analysis_snapshots.test_id=? AND perturbation_tests.owner_user_id=?", snapshotID, testID, userID).First(&row).Error != nil {
		return AnalysisSnapshotDescriptor{}, ErrNotFound
	}
	out := analysisDescriptor(row, true)
	var metrics []saasstore.PerturbationMetricSummary
	_ = s.db.WithContext(ctx).Where("analysis_snapshot_id=?", row.ID).Order("subject_id ASC,alpha ASC,metric_key ASC").Find(&metrics).Error
	for _, metric := range metrics {
		var stats core.DescriptiveStats
		_ = json.Unmarshal(metric.Statistics, &stats)
		out.Metrics = append(out.Metrics, MetricSummaryDescriptor{SubjectID: metric.SubjectID, Alpha: metric.Alpha, MetricKey: metric.MetricKey, PlannedCount: metric.PlannedCount, ValidCount: metric.ValidCount, FailedCount: metric.FailedCount, MissingCount: metric.MissingCount, Statistics: stats})
	}
	var quals []saasstore.PerturbationQualificationSummary
	_ = s.db.WithContext(ctx).Where("analysis_snapshot_id=?", row.ID).Order("subject_id ASC,alpha ASC").Find(&quals).Error
	for _, q := range quals {
		out.Qualifications = append(out.Qualifications, QualificationSummaryDescriptor{SubjectID: q.SubjectID, Alpha: q.Alpha, ValidCount: q.ValidCount, Qualified: q.QualifiedCount, ReturnFailedOnly: q.ReturnFailedCount, DrawdownFailedOnly: q.DrawdownFailedCount, BothFailed: q.BothFailedCount})
	}
	return out, nil
}
func analysisDescriptor(row saasstore.PerturbationAnalysisSnapshot, details bool) AnalysisSnapshotDescriptor {
	out := AnalysisSnapshotDescriptor{ID: row.ID, TestID: row.TestID, SnapshotKey: row.SnapshotKey, AnalysisSetHash: row.AnalysisSetHash, StatisticsVersion: row.StatisticsVersion, Completeness: row.Completeness, PlannedCount: row.PlannedCount, ValidCount: row.ValidCount, FailedCount: row.FailedCount, MissingCount: row.MissingCount, ContentHash: row.ContentHash, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)}
	if details {
		out.Summary = append(json.RawMessage(nil), row.Summary...)
	}
	return out
}
