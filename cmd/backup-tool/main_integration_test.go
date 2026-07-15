package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"quantsaas/internal/backtestcore"
	"quantsaas/internal/compute"
	"quantsaas/internal/marketversion"
	performancecore "quantsaas/internal/performance"
	"quantsaas/internal/quant"
	"quantsaas/internal/saas/backtestresult"
	"quantsaas/internal/saas/computetask"
	"quantsaas/internal/saas/performancereport"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestIncrementalBackupRestoresStandardizedResultGraph(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	source := openBackupIntegrationDB(t, dsn, "p03_backup_source")
	target := openBackupIntegrationDB(t, dsn, "p03_backup_target")
	ctx := context.Background()

	user := saasstore.User{ID: 91, Email: "backup-p03@example.test", PasswordHash: "test-only", Role: "user", Plan: "free", Status: "active"}
	if err := source.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	targetUser := user
	if err := target.Create(&targetUser).Error; err != nil {
		t.Fatal(err)
	}

	bars := []quant.Bar{
		{OpenTime: 1_700_000_000_000, Open: 100, High: 102, Low: 99, Close: 101, Volume: 10},
		{OpenTime: 1_700_086_400_000, Open: 101, High: 104, Low: 100, Close: 103, Volume: 12},
		{OpenTime: 1_700_172_800_000, Open: 103, High: 105, Low: 98, Close: 99, Volume: 15},
	}
	coreSpec := backtestcore.Spec{
		Runner: backtestcore.RunnerSigmoidDCA, InstrumentID: "BTCUSDT", Symbol: "BTCUSDT",
		DataSource: "binance", Interval: "1d", ExecutionMode: backtestcore.ExecutionModeCloseSameBar,
		PositionStructure: "floating_only", StartTimeMs: bars[0].OpenTime, EndTimeMs: bars[2].OpenTime,
		EvaluationStartMs: bars[0].OpenTime, EvaluationEndMs: bars[2].OpenTime,
		PrefixMode: backtestcore.PrefixModeExecute, InitialCapital: 1000, CoreVersion: backtestcore.CoreVersion,
	}
	identity, err := backtestresult.BuildIdentity(backtestresult.SpecInput{
		StrategyID: "sigmoid-dca-btc", StrategyVersion: "0.1.0",
		ParameterSchemaVersion: backtestresult.ParameterSchemaV1,
		Parameters:             map[string]any{"beta": 1.25}, CoreSpec: coreSpec,
	}, bars)
	if err != nil {
		t.Fatal(err)
	}
	resultStore := backtestresult.NewStore(source)
	reservation, err := resultStore.Reserve(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if !reservation.Created {
		t.Fatal("expected a new result reservation")
	}
	if err := resultStore.MarkRunning(ctx, reservation.Result.ID); err != nil {
		t.Fatal(err)
	}
	// The spec predates the incremental boundary; completing the result after
	// the boundary must still pull that spec into the backup dependency closure.
	since := time.Now().UTC()
	time.Sleep(2 * time.Millisecond)
	path := []backtestcore.NAVPoint{
		{TimeMs: bars[0].OpenTime, Price: 101, TotalEquity: 1000, Cash: 1000, ActualExposureWeight: 0},
		{TimeMs: bars[1].OpenTime, Price: 103, TotalEquity: 1010, Cash: 505, AssetQuantity: 4.9029, ActualExposureWeight: 0.5, DailyReturn: 0.01},
		{TimeMs: bars[2].OpenTime, Price: 99, TotalEquity: 990, Cash: 495, AssetQuantity: 5, ActualExposureWeight: 0.5, DailyReturn: -0.01980198},
	}
	coreResult := backtestcore.Result{
		Path: path, FinalAssets: 990, TotalReturn: -0.01, TradeCount: 1,
		TotalInjected: 1000, EvaluationInitial: 1000,
		EvaluationStartMs: bars[0].OpenTime, EvaluationEndMs: bars[2].OpenTime,
	}
	summary, err := backtestresult.BuildSummary(coreResult, 0.01980198, backtestresult.SummaryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	standardPath := make([]backtestresult.PathPoint, 0, len(path))
	for _, point := range path {
		standardPath = append(standardPath, backtestresult.PathPoint{NAVPoint: point})
	}
	artifacts, err := backtestresult.BuildArtifacts(identity.SpecContentHash, summary, standardPath, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := resultStore.Complete(ctx, reservation.Result.ID, artifacts); err != nil {
		t.Fatal(err)
	}
	resultID := reservation.Result.ID
	run := saasstore.BacktestRun{
		UserID: user.ID, StrategyID: "sigmoid-dca-btc", InstrumentID: "BTCUSDT", DataSource: "binance",
		ExecutionMode: "close_same_bar", Symbol: "BTCUSDT", Interval: "1d", Source: "custom",
		Status: saasstore.BacktestStatusCompleted, BacktestResultID: &resultID,
		Request: saasstore.JSONB(`{"source":"custom"}`), Result: saasstore.JSONB(`{}`),
	}
	if err := source.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	performanceIdentity, err := performancereport.BuildIdentity(performancereport.IdentitySnapshot{
		BacktestResultID: resultID, BacktestResultVersion: backtestresult.ResultSchemaVersion, BacktestResultContentHash: artifacts.ResultContentHash,
		AnnualizationBacktestResultID: resultID, AnnualizationBacktestResultVersion: backtestresult.ResultSchemaVersion, AnnualizationResultContentHash: artifacts.ResultContentHash,
		Settings: performancereport.ResolvedSettings{},
	})
	if err != nil {
		t.Fatal(err)
	}
	performanceResult, err := performancecore.Analyze([]performancecore.Point{
		{TimeMs: bars[0].OpenTime, NAV: 1000, BenchmarkNAV: 1000, ActualExposure: 0},
		{TimeMs: bars[1].OpenTime, NAV: 1010, BenchmarkNAV: 1005, ActualExposure: 0.5},
		{TimeMs: bars[2].OpenTime, NAV: 990, BenchmarkNAV: 995, ActualExposure: 0.5},
	}, nil, nil, performancecore.Config{})
	if err != nil {
		t.Fatal(err)
	}
	performanceArtifacts, err := performancereport.BuildArtifacts(performanceIdentity, performanceResult)
	if err != nil {
		t.Fatal(err)
	}
	performanceStore := performancereport.NewStore(source)
	performanceReservation, err := performanceStore.Reserve(ctx, performanceIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if !performanceReservation.Created {
		t.Fatal("expected a new performance report reservation")
	}
	if err := performanceStore.MarkRunning(ctx, performanceReservation.Report.ID); err != nil {
		t.Fatal(err)
	}
	if err := performanceStore.Complete(ctx, performanceReservation.Report.ID, performanceIdentity, performanceArtifacts); err != nil {
		t.Fatal(err)
	}
	performanceReportID := performanceReservation.Report.ID

	computeRegistry := computetask.NewRegistry()
	computeExecutor := backupComputeExecutor{}
	if err := computeRegistry.Register(computeExecutor); err != nil {
		t.Fatal(err)
	}
	computeService, err := computetask.NewService(source, computeRegistry, computetask.DefaultOptions(), nil)
	if err != nil {
		t.Fatal(err)
	}
	computeRoot, err := computeService.CreateComposite(ctx, user.ID, computetask.CompositeSpec{
		TaskType: "backup-research", Title: "備份研究", Settings: map[string]any{"version": 1},
		Stages: []computetask.StageSpec{
			{Key: "scan", Type: "scan", Order: 1, ExecutorType: computeExecutor.Descriptor().Type, Items: []compute.ManifestItemInput{{Key: "point-a", CacheKey: "point:a", Input: json.RawMessage(`{"point":"a"}`), EstimatedUnits: 1}}},
			{Key: "verify", Type: "verify", Order: 2, ExecutorType: computeExecutor.Descriptor().Type, DependsOnStageKeys: []string{"scan"}, Items: []compute.ManifestItemInput{{Key: "point-b", CacheKey: "point:b", Input: json.RawMessage(`{"point":"b"}`), EstimatedUnits: 1}}},
		},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	var computeStages []saasstore.ComputeTask
	if err := source.Where("parent_task_id = ?", computeRoot.ID).Order("stage_order ASC").Find(&computeStages).Error; err != nil {
		t.Fatal(err)
	}
	var cachedItem saasstore.ComputeTaskItem
	if err := source.Where("compute_task_id = ?", computeStages[0].ID).First(&cachedItem).Error; err != nil {
		t.Fatal(err)
	}
	cacheResult, err := compute.CanonicalRawJSON([]byte(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	cacheHash := compute.HashBytes(cacheResult)
	cacheActiveKey := fmt.Sprintf("%d|%s", user.ID, cachedItem.CacheKey)
	completedAt := time.Now().UTC()
	cache := saasstore.ComputeCacheEntry{
		OwnerUserID: user.ID, CacheKey: cachedItem.CacheKey, ActiveKey: &cacheActiveKey,
		SchemaVersion: compute.CacheEntrySchemaVersion, ExecutorType: computeExecutor.Descriptor().Type,
		ExecutorVersion: computeExecutor.Descriptor().Version, ResultSchemaVersion: computeExecutor.Descriptor().ResultSchemaVersion,
		InputHash: cachedItem.InputHash, Status: compute.CacheStatusCompleted, Result: saasstore.JSONB(cacheResult),
		ContentHash: cacheHash, SourceTaskItemID: &cachedItem.ID, CompletedAt: &completedAt,
	}
	if err := source.Create(&cache).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Model(&cachedItem).Updates(map[string]any{
		"status": compute.ItemStatusCached, "progress": 1, "cache_entry_id": cache.ID,
		"result": saasstore.JSONB(cacheResult), "result_hash": cacheHash, "completed_at": completedAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	series := saasstore.MarketSeries{OwnerUserID: user.ID, Name: "backup market series", Tags: saasstore.JSONB(`[]`)}
	if err := source.Create(&series).Error; err != nil {
		t.Fatal(err)
	}
	sourceVersion := saasstore.MarketDataVersion{
		OwnerUserID: user.ID, SchemaVersion: marketversion.VersionSchemaVersion, BarSchemaVersion: marketversion.BarSchemaVersion,
		ArtifactKind: marketversion.ArtifactKindSourceSnapshot, GeneratorVersion: "backup-source-v1", PrecisionVersion: marketversion.PricePrecisionVersion,
		Status: marketversion.VersionStatusCompleted, IntegrityStatus: marketversion.IntegrityValid, ContentHash: "source-content", PlanHash: "source-plan", Plan: saasstore.JSONB(`{}`),
		InstrumentID: "BTCUSDT", DataSource: "binance", Symbol: "BTCUSDT", Market: "crypto", Timezone: "UTC", Interval: "1d",
		CalendarID: "source-calendar", CalendarVersion: marketversion.CalendarFromVersionVersion, CalendarHash: "source-calendar-hash", BarCount: 2,
		StartTimeMs: bars[0].OpenTime, EndTimeMs: bars[1].OpenTime, InternalOnly: true, CompletedAt: &completedAt,
	}
	if err := source.Create(&sourceVersion).Error; err != nil {
		t.Fatal(err)
	}
	versionBars := []marketversion.Bar{
		{Ordinal: 0, OpenTime: bars[0].OpenTime, Open: 100, High: 110, Low: 95, Close: 105, Volume: 10},
		{Ordinal: 1, OpenTime: bars[1].OpenTime, Open: 106, High: 112, Low: 101, Close: 108, Volume: 11},
	}
	versionLineage := []marketversion.BarLineage{
		{OutputOrdinal: 0, OutputOpenTime: versionBars[0].OpenTime, SegmentInstanceID: "segment-a:1", SourceVersionID: sourceVersion.ID, SourceContentHash: sourceVersion.ContentHash, SourceOrdinal: 0, SourceOpenTime: versionBars[0].OpenTime},
		{OutputOrdinal: 1, OutputOpenTime: versionBars[1].OpenTime, SegmentInstanceID: "segment-a:1", SourceVersionID: sourceVersion.ID, SourceContentHash: sourceVersion.ContentHash, SourceOrdinal: 1, SourceOpenTime: versionBars[1].OpenTime},
	}
	marketHash, err := marketversion.HashRecompositionContent(versionBars, versionLineage)
	if err != nil {
		t.Fatal(err)
	}
	outputInstrumentID := "MV_BACKUP"
	outputVersion := saasstore.MarketDataVersion{
		OwnerUserID: user.ID, MarketSeriesID: &series.ID, VersionNumber: 1, SchemaVersion: marketversion.VersionSchemaVersion, BarSchemaVersion: marketversion.BarSchemaVersion,
		ArtifactKind: marketversion.ArtifactKindSegmentRecomposition, GeneratorVersion: marketversion.RecompositionAlgorithm, PrecisionVersion: marketversion.PricePrecisionVersion,
		Status: marketversion.VersionStatusCompleted, IntegrityStatus: marketversion.IntegrityValid, ContentHash: marketHash, PlanHash: "backup-market-plan", Plan: saasstore.JSONB(`{}`),
		InstrumentID: outputInstrumentID, DataSource: "generated", Symbol: outputInstrumentID, Market: "crypto", Timezone: "UTC", Interval: "1d",
		CalendarID: "backup-calendar", CalendarVersion: marketversion.CalendarFromVersionVersion, CalendarHash: "backup-calendar-hash", BarCount: len(versionBars),
		StartTimeMs: versionBars[0].OpenTime, EndTimeMs: versionBars[1].OpenTime, Published: true, OutputInstrumentID: &outputInstrumentID, ComputeTaskID: &computeRoot.ID, CompletedAt: &completedAt,
	}
	if err := source.Create(&outputVersion).Error; err != nil {
		t.Fatal(err)
	}
	for _, bar := range versionBars {
		if err := source.Create(&saasstore.MarketDataVersionBar{VersionID: outputVersion.ID, Ordinal: bar.Ordinal, OpenTime: bar.OpenTime, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, lineage := range versionLineage {
		if err := source.Create(&saasstore.RecompositionBarLineage{VersionID: outputVersion.ID, OutputOrdinal: lineage.OutputOrdinal, OutputOpenTime: lineage.OutputOpenTime, SegmentInstanceKey: lineage.SegmentInstanceID, SourceVersionID: lineage.SourceVersionID, SourceContentHash: lineage.SourceContentHash, SourceOrdinal: lineage.SourceOrdinal, SourceOpenTime: lineage.SourceOpenTime}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := source.Create(&saasstore.MarketDataVersionSource{VersionID: outputVersion.ID, SourceVersionID: sourceVersion.ID, SourceOrder: 0, SourceRole: "segment", SourceHash: sourceVersion.ContentHash}).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&saasstore.RecompositionSegmentInstance{VersionID: outputVersion.ID, InstanceKey: "segment-a:1", SegmentItemID: "segment-a", InstanceOrder: 0, RepeatOrdinal: 1, SourceVersionID: sourceVersion.ID, SourceContentHash: sourceVersion.ContentHash, SourceStartTimeMs: versionBars[0].OpenTime, SourceEndTimeMs: versionBars[1].OpenTime, OutputStartOrdinal: 0, OutputEndOrdinal: 1, OutputStartTimeMs: versionBars[0].OpenTime, OutputEndTimeMs: versionBars[1].OpenTime, ScaleMultiplier: 1, ActualGapRatio: 0, AnchorValue: 100}).Error; err != nil {
		t.Fatal(err)
	}
	plan := saasstore.RecompositionPlan{OwnerUserID: user.ID, PlanHash: outputVersion.PlanHash, SchemaVersion: marketversion.RecompositionPlanVersion, AlgorithmVersion: marketversion.RecompositionAlgorithm, PrecisionVersion: marketversion.PricePrecisionVersion, Status: marketversion.VersionStatusCompleted, Interval: "1d", TargetMarket: "crypto", TargetTimezone: "UTC", CalendarVersionID: sourceVersion.ID, CalendarVersion: marketversion.CalendarFromVersionVersion, CalendarHash: outputVersion.CalendarHash, OutputStartTimeMs: versionBars[0].OpenTime, OutputEndTimeMs: versionBars[1].OpenTime, SegmentCount: 1, InstanceCount: 1, TotalOutputBars: 2, EstimatedReadBars: 2, EstimatedWriteBars: 2, EstimatedBytes: 448, ContentHash: marketHash, CanonicalPlan: saasstore.JSONB(`{}`), Instances: saasstore.JSONB(`[]`), PreviewTaskID: &computeRoot.ID, CompletedAt: &completedAt}
	if err := source.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&saasstore.RecompositionPlanSegment{PlanID: plan.ID, ItemID: "segment-a", SegmentOrder: 0, SourceVersionID: sourceVersion.ID, SourceContentHash: sourceVersion.ContentHash, SourceStartTimeMs: versionBars[0].OpenTime, SourceEndTimeMs: versionBars[1].OpenTime, BarCount: 2, RepeatCount: 1, PreviousClosePresent: false, FirstOpen: 100}).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&saasstore.RecompositionGeneration{OwnerUserID: user.ID, IdempotencyKey: "backup-generation", PlanID: plan.ID, PlanHash: plan.PlanHash, MarketSeriesID: series.ID, VersionNumber: 1, OutputVersionID: outputVersion.ID, ComputeTaskID: &computeRoot.ID, Status: marketversion.VersionStatusCompleted, ExpandedAt: &completedAt, CalendarCheckedAt: &completedAt, PublishedAt: &completedAt}).Error; err != nil {
		t.Fatal(err)
	}
	outputIntervals, _ := saasstore.NewJSONB([]string{"1d"})
	if err := source.Create(&saasstore.ResearchInstrument{ID: outputInstrumentID, Symbol: outputInstrumentID, DisplayName: "backup market v1", DataSource: "generated", SupportedIntervals: outputIntervals, Market: "crypto", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	for _, bar := range versionBars {
		if err := source.Create(&saasstore.KLine{InstrumentID: outputInstrumentID, Source: "generated", Symbol: outputInstrumentID, Interval: "1d", OpenTime: bar.OpenTime, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume}).Error; err != nil {
			t.Fatal(err)
		}
	}

	gene := saasstore.GeneRecord{
		StrategyID: "sigmoid-dca-btc", InstrumentID: "BTCUSDT", DataSource: "binance", Interval: "1d",
		ExecutionMode: "close_same_bar", Role: "candidate", Name: "P08 backup source",
		Tags: saasstore.JSONB(`[]`), SearchConfig: saasstore.JSONB(`{}`), ParamPack: saasstore.JSONB(`{"beta":1.25}`), WindowScore: saasstore.JSONB(`{}`),
	}
	if err := source.Create(&gene).Error; err != nil {
		t.Fatal(err)
	}
	robustnessSettings := saasstore.JSONB(`{"version":"p08-study-setting-v1"}`)
	robustnessSpace := saasstore.JSONB(`{"schema_version":"p08-grid-v1","axes":[{"name":"beta","label":"訊號反應係數","type":"float","values":[1.2,1.25,1.3],"legal_min":0.1,"legal_max":10,"step":0.05,"study_start":0,"study_end":2}],"fixed":{}}`)
	robustnessStudy := saasstore.RobustnessStudy{
		OwnerUserID: user.ID, StudyKey: "p08-study:backup", Name: "P08 backup study", Mode: "one_dimensional", Status: compute.TaskStatusCompleted,
		SettingVersion: "p08-study-setting-v1", SettingHash: compute.HashBytes(robustnessSettings), Settings: robustnessSettings,
		SpaceVersion: "p08-grid-v1", SpaceHash: compute.HashBytes(robustnessSpace), ParameterSpace: robustnessSpace,
		CenterPointKey: "1", SourceGenomeID: &gene.ID, ComputeTaskID: &computeRoot.ID, ExpectedPointCount: 1, ActualPointCount: 1, CompletedAt: &completedAt,
	}
	if err := source.Create(&robustnessStudy).Error; err != nil {
		t.Fatal(err)
	}
	coordinates := saasstore.JSONB(`[1]`)
	parameters := saasstore.JSONB(`{"beta":1.25}`)
	metrics := saasstore.JSONB(`{"version":"p08-relative-metrics-v1","final_nav_ratio":1.01,"log_final_nav_ratio":0.00995,"drawdown_residual_ratio":1.1,"log_drawdown_residual_ratio":0.09531,"performance_drawdown_composite":0.010945,"qualified":true}`)
	robustnessPoint := saasstore.RobustnessEvaluationPoint{
		StudyID: robustnessStudy.ID, PointKey: "1", Kind: "actual", State: "qualified",
		CoordinateHash: compute.HashBytes(coordinates), Coordinates: coordinates, ParameterHash: compute.HashBytes(parameters), Parameters: parameters,
		BacktestResultID: &resultID, BacktestResultVersion: backtestresult.ResultSchemaVersion, BacktestResultContentHash: artifacts.ResultContentHash,
		MetricsVersion: "p08-relative-metrics-v1", MetricsHash: compute.HashBytes(metrics), Metrics: metrics, PredictionMetadata: saasstore.JSONB(`{}`),
	}
	if err := source.Create(&robustnessPoint).Error; err != nil {
		t.Fatal(err)
	}
	analysisPayload := saasstore.JSONB(`{"analysis_version":"p08-analysis-v1","points":[],"scales":[],"regions":[],"missing_coordinates":[]}`)
	robustnessSnapshot := saasstore.RobustnessAnalysisSnapshot{
		StudyID: robustnessStudy.ID, AnalysisKey: "p08-analysis:backup", AnalysisVersion: "p08-analysis-v1",
		ConnectivityVersion: "p08-axis-connectivity-v1", DistanceVersion: "p08-grid-distance-v1", FrontierVersion: "p08-pareto-v1", CenterVersion: "p08-center-v1",
		PointSetHash: "backup-point-set", SettingsHash: robustnessStudy.SettingHash, Metric: "log_final_nav_ratio", Radii: saasstore.JSONB(`[1,2,3]`),
		Payload: analysisPayload, ContentHash: compute.HashBytes(analysisPayload),
	}
	if err := source.Create(&robustnessSnapshot).Error; err != nil {
		t.Fatal(err)
	}

	backup, err := buildIncrementalBackup(source, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(backup.BacktestSpecs) != 1 || len(backup.BacktestResults) != 1 || len(backup.BacktestSummaries) != 1 || len(backup.BacktestPathBlocks) != 2 || len(backup.BacktestRuns) != 1 ||
		len(backup.PerformanceReports) != 1 || len(backup.PerformanceSummaries) != 1 || len(backup.PerformanceCharts) != 6 {
		t.Fatalf("incomplete backup closure: specs=%d results=%d summaries=%d blocks=%d runs=%d reports=%d report_summaries=%d report_charts=%d",
			len(backup.BacktestSpecs), len(backup.BacktestResults), len(backup.BacktestSummaries), len(backup.BacktestPathBlocks), len(backup.BacktestRuns),
			len(backup.PerformanceReports), len(backup.PerformanceSummaries), len(backup.PerformanceCharts))
	}
	if len(backup.ComputeTasks) != 3 || len(backup.ComputeTaskItems) != 2 || len(backup.ComputeDependencies) != 1 || len(backup.ComputeCacheEntries) != 1 {
		t.Fatalf("incomplete compute backup closure: tasks=%d items=%d dependencies=%d caches=%d", len(backup.ComputeTasks), len(backup.ComputeTaskItems), len(backup.ComputeDependencies), len(backup.ComputeCacheEntries))
	}
	if backup.Version != backupVersion || len(backup.RobustnessStudies) != 1 || len(backup.RobustnessPoints) != 1 || len(backup.RobustnessSnapshots) != 1 || len(backup.GeneRecords) != 1 {
		t.Fatalf("incomplete P08 backup closure: version=%d studies=%d points=%d snapshots=%d genes=%d", backup.Version, len(backup.RobustnessStudies), len(backup.RobustnessPoints), len(backup.RobustnessSnapshots), len(backup.GeneRecords))
	}
	if len(backup.MarketSeries) != 1 || len(backup.MarketDataVersions) != 2 || len(backup.MarketVersionBars) != 2 ||
		len(backup.MarketVersionSources) != 1 || len(backup.RecompositionPlans) != 1 || len(backup.RecompositionSegments) != 1 ||
		len(backup.RecompositionGenerations) != 1 || len(backup.RecompositionInstances) != 1 || len(backup.RecompositionLineage) != 2 {
		t.Fatalf("incomplete market-version closure: series=%d versions=%d bars=%d sources=%d plans=%d segments=%d generations=%d instances=%d lineage=%d",
			len(backup.MarketSeries), len(backup.MarketDataVersions), len(backup.MarketVersionBars), len(backup.MarketVersionSources),
			len(backup.RecompositionPlans), len(backup.RecompositionSegments), len(backup.RecompositionGenerations), len(backup.RecompositionInstances), len(backup.RecompositionLineage))
	}
	raw, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	var restoredPayload incrementalBackup
	if err := json.Unmarshal(raw, &restoredPayload); err != nil {
		t.Fatal(err)
	}
	if err := applyIncrementalBackup(target, restoredPayload); err != nil {
		t.Fatal(err)
	}

	restoredStore := backtestresult.NewStore(target)
	report, err := restoredStore.VerifyResult(ctx, resultID)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid || !report.PathVerified || report.ResultHash != artifacts.ResultContentHash {
		t.Fatalf("restored integrity report = %+v", report)
	}
	var restoredRun saasstore.BacktestRun
	if err := target.First(&restoredRun, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restoredRun.BacktestResultID == nil || *restoredRun.BacktestResultID != resultID {
		t.Fatalf("restored run reference = %v, want %d", restoredRun.BacktestResultID, resultID)
	}
	restoredPerformance, err := performancereport.NewStore(target).Verify(ctx, performanceReportID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !restoredPerformance.Valid || !restoredPerformance.ChartsVerified || restoredPerformance.ContentHash != performanceArtifacts.ReportContentHash {
		t.Fatalf("restored performance integrity report = %+v", restoredPerformance)
	}
	var restoredComputeItem saasstore.ComputeTaskItem
	if err := target.First(&restoredComputeItem, cachedItem.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restoredComputeItem.CacheEntryID == nil || *restoredComputeItem.CacheEntryID != cache.ID || restoredComputeItem.ResultHash != cacheHash {
		t.Fatalf("restored compute item/cache reference = %+v", restoredComputeItem)
	}
	var restoredMarketVersion saasstore.MarketDataVersion
	if err := target.First(&restoredMarketVersion, outputVersion.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restoredMarketVersion.ContentHash != marketHash || !restoredMarketVersion.Published {
		t.Fatalf("restored market version = %+v", restoredMarketVersion)
	}
	var restoredRobustnessPoint saasstore.RobustnessEvaluationPoint
	if err := target.First(&restoredRobustnessPoint, robustnessPoint.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restoredRobustnessPoint.StudyID != robustnessStudy.ID || restoredRobustnessPoint.BacktestResultID == nil || *restoredRobustnessPoint.BacktestResultID != resultID || restoredRobustnessPoint.MetricsHash != robustnessPoint.MetricsHash {
		t.Fatalf("restored P08 point = %+v", restoredRobustnessPoint)
	}
}

type backupComputeExecutor struct{}

func (backupComputeExecutor) Descriptor() compute.ExecutorDescriptor {
	return compute.ExecutorDescriptor{Type: "p05.backup-test", Version: "v1", ResultSchemaVersion: "result-v1"}
}

func (backupComputeExecutor) Execute(context.Context, computetask.Execution) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func openBackupIntegrationDB(t *testing.T, dsn string, prefix string) *gorm.DB {
	t.Helper()
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	scopedDSN, err := backupDSNWithSearchPath(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(postgres.Open(scopedDSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := saasstore.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func backupDSNWithSearchPath(dsn string, schema string) (string, error) {
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			return "", err
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String(), nil
	}
	return strings.TrimSpace(dsn) + " search_path=" + schema, nil
}
