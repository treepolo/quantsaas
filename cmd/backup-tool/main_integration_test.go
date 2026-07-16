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

	dynamicSettings := saasstore.JSONB(fmt.Sprintf(`{"version":"p09-study-setting-v1","request":{"genome_id":%d}}`, gene.ID))
	dynamicStudy := saasstore.DynamicModelStudy{OwnerUserID: user.ID, StudyKey: "p09-study:backup", Name: "P09 backup study", Status: "completed", Route: "explainable_gam", InstrumentID: outputInstrumentID, DataSource: "generated", Symbol: outputInstrumentID, Interval: "1d", ExecutionMode: "close_same_bar", TrainStartTimeMs: versionBars[0].OpenTime, TrainEndTimeMs: versionBars[1].OpenTime, DatasetHash: "p09-dataset", SettingVersion: "p09-study-setting-v1", SettingHash: compute.HashBytes(dynamicSettings), Settings: dynamicSettings, ComputeTaskID: &computeRoot.ID, MaterializationTaskID: &computeRoot.ID, ArtifactSetHash: "p09-artifact-set", CompletedAt: &completedAt}
	if err := source.Create(&dynamicStudy).Error; err != nil {
		t.Fatal(err)
	}
	artifactPayload := saasstore.JSONB(`{"schema_version":"p09-model-artifact-v1"}`)
	dynamicArtifact := saasstore.DynamicModelArtifact{StudyID: dynamicStudy.ID, ArtifactKey: "horizon-20", SchemaVersion: "p09-model-artifact-v1", Route: "explainable_gam", Horizon: 20, TargetKind: "horizon_bundle", Lookback: 20, DatasetHash: dynamicStudy.DatasetHash, TrainingStartTimeMs: dynamicStudy.TrainStartTimeMs, TrainingEndTimeMs: dynamicStudy.TrainEndTimeMs, ContentHash: compute.HashBytes(artifactPayload), Payload: artifactPayload}
	if err := source.Create(&dynamicArtifact).Error; err != nil {
		t.Fatal(err)
	}
	predictionManifest := saasstore.JSONB(`[{"block_id":"oof-0000"}]`)
	dynamicPrediction := saasstore.DynamicPredictionSnapshot{StudyID: dynamicStudy.ID, SnapshotKey: "p09-oof:backup", SchemaVersion: "p09-prediction-v1", ArtifactSetHash: dynamicStudy.ArtifactSetHash, DatasetHash: dynamicStudy.DatasetHash, PredictionCount: 2, StartTimeMs: dynamicStudy.TrainStartTimeMs, EndTimeMs: dynamicStudy.TrainEndTimeMs, BlockManifestHash: compute.HashBytes(predictionManifest), BlockManifest: predictionManifest, ContentHash: "p09-prediction-hash"}
	if err := source.Create(&dynamicPrediction).Error; err != nil {
		t.Fatal(err)
	}
	policyPayload := saasstore.JSONB(`{"schema_version":"p09-dynamic-policy-v1"}`)
	spacePayload := saasstore.JSONB(`{"schema_version":"p09-dynamic-parameter-space-v1"}`)
	dynamicPolicy := saasstore.DynamicPolicyArtifact{OwnerUserID: user.ID, StudyID: dynamicStudy.ID, PolicyKey: "p09-policy:backup", SchemaVersion: "p09-dynamic-policy-v1", ArtifactSetHash: dynamicStudy.ArtifactSetHash, PredictionSnapshotID: dynamicPrediction.ID, ContentHash: compute.HashBytes(policyPayload), Payload: policyPayload, ParameterSpaceVersion: "p09-dynamic-parameter-space-v1", ParameterSpaceHash: compute.HashBytes(spacePayload), ParameterSpace: spacePayload}
	if err := source.Create(&dynamicPolicy).Error; err != nil {
		t.Fatal(err)
	}
	materialManifest := saasstore.JSONB(`[{"block_id":"daily-diagnostics"}]`)
	dynamicMaterialization := saasstore.DynamicMaterialization{OwnerUserID: user.ID, MaterializationKey: "p09-materialized:backup", SchemaVersion: "p09-prediction-v1", StudyID: dynamicStudy.ID, PredictionSnapshotID: dynamicPrediction.ID, PolicyArtifactID: dynamicPolicy.ID, ContentHash: "p09-materialized-hash", BlockManifestHash: compute.HashBytes(materialManifest), BlockManifest: materialManifest, BacktestResultID: &resultID, BacktestResultVersion: backtestresult.ResultSchemaVersion, BacktestResultContentHash: artifacts.ResultContentHash}
	if err := source.Create(&dynamicMaterialization).Error; err != nil {
		t.Fatal(err)
	}
	reportManifest := saasstore.JSONB(`[{"block_id":"model-validation"}]`)
	dynamicReport := saasstore.DynamicModelReportSnapshot{StudyID: dynamicStudy.ID, SnapshotKey: "p09-report:backup", SchemaVersion: "p09-model-report-v1", FormulaVersion: "p09-report-formula-v1", ArtifactSetHash: dynamicStudy.ArtifactSetHash, PredictionSnapshotID: dynamicPrediction.ID, PolicyArtifactID: &dynamicPolicy.ID, MaterializationID: &dynamicMaterialization.ID, ActualStartTimeMs: dynamicStudy.TrainStartTimeMs, ActualEndTimeMs: dynamicStudy.TrainEndTimeMs, Completeness: "complete", BlockManifestHash: compute.HashBytes(reportManifest), BlockManifest: reportManifest, ContentHash: "p09-report-hash"}
	if err := source.Create(&dynamicReport).Error; err != nil {
		t.Fatal(err)
	}
	dynamicBlockPayload := saasstore.JSONB(`[{"mean_loss":0.5}]`)
	dynamicBlock := saasstore.DynamicReportBlock{StudyID: dynamicStudy.ID, OwnerKind: "report_snapshot", OwnerID: dynamicReport.ID, BlockID: "model-validation", BlockKind: "model_validation", SchemaVersion: "p09-model-report-v1", FormulaVersion: "p09-report-formula-v1", BlockIndex: 0, StartTimeMs: dynamicStudy.TrainStartTimeMs, EndTimeMs: dynamicStudy.TrainEndTimeMs, PointCount: 1, ContentHash: compute.HashBytes(dynamicBlockPayload), Payload: dynamicBlockPayload}
	if err := source.Create(&dynamicBlock).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Model(&dynamicStudy).Updates(map[string]any{"prediction_snapshot_id": dynamicPrediction.ID, "policy_artifact_id": dynamicPolicy.ID, "materialization_id": dynamicMaterialization.ID, "report_snapshot_id": dynamicReport.ID}).Error; err != nil {
		t.Fatal(err)
	}

	configurationCanonical := saasstore.JSONB(fmt.Sprintf(`{"schema_version":"p10-research-configuration-v1","genome_id":%d,"parameter_space":{"schema_version":"p08-grid-v1","axes":[],"fixed":{}},"base_coordinates":[],"backtest":{},"dataset_hash":"p10-dataset"}`, gene.ID))
	configuration := saasstore.ResearchConfiguration{OwnerUserID: user.ID, ConfigHash: compute.HashBytes(configurationCanonical), SchemaVersion: "p10-research-configuration-v1", StrategyID: gene.StrategyID, InstrumentID: outputInstrumentID, DataSource: "generated", Symbol: outputInstrumentID, Interval: "1d", DatasetHash: "p10-dataset", StartTimeMs: versionBars[0].OpenTime, EndTimeMs: versionBars[1].OpenTime, ExecutionMode: "close_same_bar", ParameterSpaceVersion: "p08-grid-v1", ParameterSpaceHash: compute.HashBytes(robustnessSpace), ParameterSpace: robustnessSpace, DynamicMode: true, DynamicStudyID: &dynamicStudy.ID, DynamicPolicyID: &dynamicPolicy.ID, Canonical: configurationCanonical}
	if err := source.Create(&configuration).Error; err != nil {
		t.Fatal(err)
	}
	metadata := saasstore.ResearchConfigurationMetadata{ConfigurationID: configuration.ID, Name: "P10 backup research", Tags: saasstore.JSONB(`[]`)}
	if err := source.Create(&metadata).Error; err != nil {
		t.Fatal(err)
	}
	researchRun := saasstore.ResearchRun{OwnerUserID: user.ID, ConfigurationID: configuration.ID, RunKey: "p10-run:backup", SamplerVersion: "p10-sobol-v1", ExplorationStatus: "checkpoint", Status: compute.TaskStatusCompleted, StartedAt: &completedAt, CompletedAt: &completedAt}
	if err := source.Create(&researchRun).Error; err != nil {
		t.Fatal(err)
	}
	stage := saasstore.ResearchStage{RunID: researchRun.ID, Ordinal: 0, StageKey: "global-0", StageType: "global", ManifestHash: "p10-manifest", Manifest: saasstore.JSONB(`{"points":[]}`), ComputeTaskID: &computeRoot.ID, Status: compute.TaskStatusCompleted, RequestedCount: 1, UniqueCount: 1, CompletedCount: 1, CompletedAt: &completedAt}
	if err := source.Create(&stage).Error; err != nil {
		t.Fatal(err)
	}
	researchPoint := saasstore.ResearchEvaluationPoint{ConfigurationID: configuration.ID, VectorHash: "p10-vector", CoordinateKey: "0", Coordinates: saasstore.JSONB(`[0]`), Parameters: parameters, Legality: "legal", Status: "completed", BacktestResultID: &resultID, BacktestResultVersion: backtestresult.ResultSchemaVersion, BacktestResultContentHash: artifacts.ResultContentHash, MetricsVersion: "p08-relative-metrics-v1", MetricsHash: compute.HashBytes(metrics), Metrics: metrics, Qualified: true}
	if err := source.Create(&researchPoint).Error; err != nil {
		t.Fatal(err)
	}
	origin := saasstore.ResearchPointOrigin{PointID: researchPoint.ID, RunID: researchRun.ID, StageID: stage.ID, OriginKey: "sobol:0", OriginType: "sobol", Reason: saasstore.JSONB(`{}`)}
	if err := source.Create(&origin).Error; err != nil {
		t.Fatal(err)
	}
	researchAnalysis := saasstore.ResearchAnalysisSnapshot{ConfigurationID: configuration.ID, SnapshotKey: "p10-analysis:backup", SchemaVersion: "p10-analysis-v1", PointSetHash: "p10-point-set", MetricsVersion: "p08-relative-metrics-v1", JAnalysisVersion: "p08-analysis-v1", RobustnessStudyID: robustnessStudy.ID, RobustnessSnapshotID: robustnessSnapshot.ID, Completeness: "complete", ContentHash: "p10-analysis-hash", Summary: saasstore.JSONB(`{}`)}
	if err := source.Create(&researchAnalysis).Error; err != nil {
		t.Fatal(err)
	}
	region := saasstore.RobustRegion{AnalysisSnapshotID: researchAnalysis.ID, ComponentID: "region-a", Completeness: "complete", Boundary: saasstore.JSONB(`{}`), Lineage: saasstore.JSONB(`[]`)}
	if err := source.Create(&region).Error; err != nil {
		t.Fatal(err)
	}
	regionPoint := saasstore.RobustRegionPoint{RegionID: region.ID, PointID: researchPoint.ID}
	if err := source.Create(&regionPoint).Error; err != nil {
		t.Fatal(err)
	}
	adoptionUnit := saasstore.JSONB(`{"schema_version":"p10-dynamic-adoption-unit-v1"}`)
	candidate := saasstore.RobustCandidate{OwnerUserID: user.ID, ConfigurationID: configuration.ID, PointID: researchPoint.ID, AnalysisSnapshotID: &researchAnalysis.ID, RegionID: &region.ID, CandidateKey: "p10-candidate:backup", Version: "p10-candidate-v1", SourceKind: "analysis", Completeness: "complete", Roles: saasstore.JSONB(`["region_center"]`), AdoptionUnitHash: compute.HashBytes(adoptionUnit), AdoptionUnit: adoptionUnit, Name: "P10 backup candidate", Tags: saasstore.JSONB(`[]`), Lineage: saasstore.JSONB(`[]`)}
	if err := source.Create(&candidate).Error; err != nil {
		t.Fatal(err)
	}
	candidateAnalysis := saasstore.CandidateAnalysisLink{CandidateID: candidate.ID, AnalysisKind: "G", Version: "p10-analysis-link-v1", Status: "not_calculated", PartialSnapshot: saasstore.JSONB(`{}`)}
	if err := source.Create(&candidateAnalysis).Error; err != nil {
		t.Fatal(err)
	}
	candidateGene := saasstore.CandidateGeneLink{CandidateID: candidate.ID, GeneRecordID: gene.ID, CandidateVersion: candidate.Version, ImportedAt: completedAt, PromotionAudit: saasstore.JSONB(`[]`)}
	if err := source.Create(&candidateGene).Error; err != nil {
		t.Fatal(err)
	}
	researchSeries := saasstore.ResearchSeries{OwnerUserID: user.ID, SeriesKey: "p10-series:backup", Name: "P10 backup series", SchemaVersion: "p10-series-v1", CommonBackgroundHash: "p10-background", CommonBackground: saasstore.JSONB(`{}`), ChangedFactors: saasstore.JSONB(`[]`), CommonSchemaHash: configuration.ParameterSpaceHash, CommonSchema: configuration.ParameterSpace}
	if err := source.Create(&researchSeries).Error; err != nil {
		t.Fatal(err)
	}
	seriesMember := saasstore.ResearchSeriesMember{SeriesID: researchSeries.ID, ConfigurationID: configuration.ID, DisplayOrder: 0, FactorValues: saasstore.JSONB(`{}`)}
	if err := source.Create(&seriesMember).Error; err != nil {
		t.Fatal(err)
	}
	comparison := saasstore.ResearchComparisonSnapshot{SeriesID: researchSeries.ID, SnapshotKey: "p10-comparison:backup", SchemaVersion: "p10-comparison-v1", Eligibility: "descriptive_only", EligibilityReasons: saasstore.JSONB(`[]`), MemberHashes: saasstore.JSONB(`[]`), CommonManifestHash: "p10-common-manifest", CommonManifest: saasstore.JSONB(`[]`), Missing: saasstore.JSONB(`{}`), Differences: saasstore.JSONB(`[]`), ContentHash: "p10-comparison-hash"}
	if err := source.Create(&comparison).Error; err != nil {
		t.Fatal(err)
	}
	surrogate := saasstore.SurrogateModelSnapshot{OwnerUserID: user.ID, ConfigurationID: configuration.ID, RunID: researchRun.ID, SnapshotKey: "p10-surrogate:backup", SchemaVersion: "p10-surrogate-v1", TrainingPointSetHash: "p10-training", BatchFoldHash: "p10-fold", ModelSettings: saasstore.JSONB(`{}`), OOFMetrics: saasstore.JSONB(`{}`), ArtifactHash: "p10-surrogate-artifact", Artifact: saasstore.JSONB(`{}`), ContentHash: "p10-surrogate-content", Status: compute.TaskStatusCompleted, ComputeTaskID: &computeRoot.ID}
	if err := source.Create(&surrogate).Error; err != nil {
		t.Fatal(err)
	}
	proposal := saasstore.SurrogateProposal{SurrogateSnapshotID: surrogate.ID, VectorHash: "p10-proposal-vector", ProposalTypes: saasstore.JSONB(`["high_return"]`), Coordinates: saasstore.JSONB(`[1]`), Parameters: parameters, Predictions: saasstore.JSONB(`{}`), Uncertainty: saasstore.JSONB(`{}`), CandidatePoolHash: "p10-pool", ActualPointID: &researchPoint.ID, ActualError: saasstore.JSONB(`{}`)}
	if err := source.Create(&proposal).Error; err != nil {
		t.Fatal(err)
	}

	randomBatch := saasstore.RandomParameterBatch{OwnerUserID: user.ID, BatchKey: "p11-batch:backup", Seed: 42, TargetCount: 1, GeneratorVersion: "p11-discrete-uniform-v1", RangeVersion: "p11-source-research-range-v1", ParameterSpaceVersion: "p08-grid-v1", ParameterSpaceHash: configuration.ParameterSpaceHash, ParameterSpace: configuration.ParameterSpace, FixedStructureHash: candidate.AdoptionUnitHash, AttemptCount: 1, RejectReasons: saasstore.JSONB(`{}`), ContentHash: "p11-batch-content"}
	if err := source.Create(&randomBatch).Error; err != nil {
		t.Fatal(err)
	}
	randomRecord := saasstore.RandomParameterRecord{BatchID: randomBatch.ID, SequenceIndex: 0, Coordinates: saasstore.JSONB(`[0]`), Parameters: parameters, ContentHash: compute.HashBytes(parameters), BacktestResultID: &resultID, BacktestResultVersion: backtestresult.ResultSchemaVersion, BacktestContentHash: artifacts.ResultContentHash}
	if err := source.Create(&randomRecord).Error; err != nil {
		t.Fatal(err)
	}
	controlTask := saasstore.ControlAnalysisTask{OwnerUserID: user.ID, TaskKey: "p11-task:backup", Name: "P11 backup control", Tags: saasstore.JSONB(`[]`), Status: "completed", SourceKind: "m_candidate", CandidateID: &candidate.ID, ResearchConfigurationID: &configuration.ID, SourceVersion: candidate.Version, SourceContentHash: candidate.AdoptionUnitHash, RandomBatchID: randomBatch.ID, RandomTargetCount: 1, ShuffleSeed: 84, ShuffleTargetCount: 1, ToggleEveryNBars: 7, RuleVersion: "p11-four-rules-v1", StatisticsVersion: "p11-empirical-midrank-v1", ParameterSpaceHash: configuration.ParameterSpaceHash, CanonicalHash: "p11-canonical-hash", Canonical: saasstore.JSONB(`{"schema_version":"p11-control-task-v1"}`), ComputeTaskID: &computeRoot.ID, StartedAt: &completedAt, CompletedAt: &completedAt}
	if err := source.Create(&controlTask).Error; err != nil {
		t.Fatal(err)
	}
	controlEvaluation := saasstore.ControlEvaluation{TaskID: controlTask.ID, Kind: "baseline", SequenceIndex: 0, BacktestResultID: resultID, BacktestResultVersion: backtestresult.ResultSchemaVersion, BacktestResultContentHash: artifacts.ResultContentHash, PerformanceReportID: &performanceReportID, Summary: saasstore.JSONB(`{"roi":-0.01}`), SummaryHash: "p11-summary-hash"}
	if err := source.Create(&controlEvaluation).Error; err != nil {
		t.Fatal(err)
	}
	controlSnapshot := saasstore.ControlAnalysisSnapshot{TaskID: controlTask.ID, SnapshotKey: "p11-snapshot:backup", SchemaVersion: "p11-control-snapshot-v1", Completeness: "completed", StatisticsVersion: "p11-empirical-midrank-v1", RandomCompletedCount: 1, ShuffleCompletedCount: 1, RuleCompletedCount: 4, Summary: saasstore.JSONB(`{}`), DetailManifest: saasstore.JSONB(`[]`), ContentHash: "p11-snapshot-content"}
	if err := source.Create(&controlSnapshot).Error; err != nil {
		t.Fatal(err)
	}
	controlMember := saasstore.ControlSnapshotMember{SnapshotID: controlSnapshot.ID, EvaluationID: controlEvaluation.ID, RepresentativeRole: "baseline"}
	if err := source.Create(&controlMember).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Model(&controlTask).Update("latest_snapshot_id", controlSnapshot.ID).Error; err != nil {
		t.Fatal(err)
	}

	inverseStudy := saasstore.KlineInverseStudy{OwnerUserID: user.ID, StudyHash: "p12-study:backup", SchemaVersion: "p12-study-v1", Name: "P12 backup inverse", Tags: saasstore.JSONB(`[]`), Status: "completed", CurrentStage: "probe", SourceKind: "gene", SourceVersion: "gene-record-v1", SourceContentHash: compute.HashBytes(gene.ParamPack), SourceGenomeID: &gene.ID, ParameterHash: compute.HashBytes(parameters), InstrumentID: "BTCUSDT", DataSource: "binance", Symbol: "BTCUSDT", Interval: "1d", ExecutionMode: "close_same_bar", WarmupLength: 1, EvaluationLength: 2, EvaluationStartMs: bars[1].OpenTime, InitialCapital: 1000, FeeRate: .001, InitialBudget: 1, CellCount: 1, ParentCapacity: 1, RootSeed: 1212, BoundsHash: "p12-bounds", CalibrationSourceHash: "p12-calibration-source", CanonicalHash: "p12-canonical", Canonical: saasstore.JSONB(`{"schema_version":"p12-study-v1"}`), StartedAt: &completedAt, CompletedAt: &completedAt}
	if err := source.Create(&inverseStudy).Error; err != nil {
		t.Fatal(err)
	}
	inverseCalibration := saasstore.KlineInverseCalibration{StudyID: inverseStudy.ID, SchemaVersion: "p12-calibration-v1", SourceSnapshot: saasstore.JSONB(`{}`), SourceContentHash: "p12-calibration-source", ObservedBounds: saasstore.JSONB(`{}`), FinalBounds: saasstore.JSONB(`{}`), FeatureRanges: saasstore.JSONB(`{}`), Centers: saasstore.JSONB(`[]`), CalibrationSeed: 1212, CalibrationCount: 1, CellCount: 1, ParentCapacity: 1, GeneratorVersion: "p12-global-v1", FeatureVersion: "p12-features-v1", CVTVersion: "p12-cvt-v1", ContentHash: "p12-calibration-content"}
	if err := source.Create(&inverseCalibration).Error; err != nil {
		t.Fatal(err)
	}
	inverseBatch := saasstore.KlineInverseBatch{StudyID: inverseStudy.ID, Ordinal: 0, BatchKey: "p12-batch:backup", BatchType: "probe", SchemaVersion: "p12-batch-v1", ManifestHash: "p12-manifest", Manifest: saasstore.JSONB(`{}`), CompatibilityHash: "p12-compatible", Budget: 1, RNGStart: 0, RNGEnd: 1, CheckpointPosition: 1, CheckpointHash: "p12-checkpoint", Checkpoint: saasstore.JSONB(`{"next":1}`), ComputeTaskID: &computeRoot.ID, Status: "completed", CompletedCount: 1, StartedAt: &completedAt, CompletedAt: &completedAt}
	if err := source.Create(&inverseBatch).Error; err != nil {
		t.Fatal(err)
	}
	inversePath := saasstore.KlineInversePath{PathHash: "p12-path:backup", SchemaVersion: "p12-path-v1", CoordinateVersion: "p12-coordinate-v1", WarmupLength: 1, EvaluationLength: 2, StartTimeMs: bars[0].OpenTime, EvaluationStartMs: bars[1].OpenTime, EndTimeMs: bars[2].OpenTime, DatesHash: "p12-dates", CoordinatesHash: "p12-coordinates", OHLCContentHash: "p12-ohlc", Coordinates: saasstore.JSONB(`[{"g":0,"b":0,"u":0,"d":0}]`), OHLC: saasstore.JSONB(`[]`), Permanent: true, PermanentReason: "anchor"}
	if err := source.Create(&inversePath).Error; err != nil {
		t.Fatal(err)
	}
	inverseEvaluation := saasstore.KlineInverseEvaluation{StudyID: inverseStudy.ID, PathID: inversePath.ID, EvaluationKey: "p12-evaluation:backup", BatchID: inverseBatch.ID, SequenceIndex: 0, CellIndex: 0, Status: "completed", OutcomeState: "A+B", PassA: true, PassB: true, QRelative: .01, QAbsolute: .02, FeaturesVersion: "p12-features-v1", FeaturesHash: "p12-features", Features: saasstore.JSONB(`{}`), BacktestResultID: resultID, BacktestResultVersion: backtestresult.ResultSchemaVersion, BacktestResultContentHash: artifacts.ResultContentHash, Permanent: true}
	if err := source.Create(&inverseEvaluation).Error; err != nil {
		t.Fatal(err)
	}
	inverseLineage := saasstore.KlineInverseLineageEdge{StudyID: inverseStudy.ID, BatchID: inverseBatch.ID, SequenceIndex: 0, ChildPathID: inversePath.ID, RequestedOperation: "global", ActualOperation: "global", ChangedChannels: saasstore.JSONB(`[]`), RNGPosition: 0, VariationVersion: "p12-variation-v1"}
	if err := source.Create(&inverseLineage).Error; err != nil {
		t.Fatal(err)
	}
	inverseSnapshot := saasstore.KlineInverseArchiveSnapshot{StudyID: inverseStudy.ID, BatchID: inverseBatch.ID, SnapshotKey: "p12-snapshot:backup", SchemaVersion: "p12-snapshot-v1", SearchVersion: "p12-search-v1", StatisticsVersion: "p12-statistics-v1", EvaluatedCount: 1, TouchedCellCount: 1, ACellCount: 1, BCellCount: 1, PermanentPathCount: 1, LineageEdgeCount: 1, Summary: saasstore.JSONB(`{}`), ActiveParents: saasstore.JSONB(`[]`), CellSummary: saasstore.JSONB(`[]`), ContentHash: "p12-snapshot-content"}
	if err := source.Create(&inverseSnapshot).Error; err != nil {
		t.Fatal(err)
	}
	inverseProbe := saasstore.KlineInverseProbeBatch{StudyID: inverseStudy.ID, BatchID: inverseBatch.ID, AnchorPathID: inversePath.ID, CompatibilityHash: "p12-probe-compatible", ManifestHash: inverseBatch.ManifestHash, Manifest: inverseBatch.Manifest, Status: "completed"}
	if err := source.Create(&inverseProbe).Error; err != nil {
		t.Fatal(err)
	}
	inverseSource := saasstore.KlineInverseSourceLink{StudyID: inverseStudy.ID, SourceKind: "gene", SourceID: fmt.Sprint(gene.ID), SourceVersion: "gene-record-v1", SourceContentHash: compute.HashBytes(gene.ParamPack), BackLink: "/evolution"}
	if err := source.Create(&inverseSource).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Model(&inverseStudy).Update("current_snapshot_id", inverseSnapshot.ID).Error; err != nil {
		t.Fatal(err)
	}

	perturbationSnapshot := saasstore.PerturbationSourceSnapshot{OwnerUserID: user.ID, SourceContentHash: "p13-source-content", SchemaVersion: "p13-source-snapshot-v1", Status: marketversion.VersionStatusCompleted, SourceVersionID: sourceVersion.ID, OriginalInstrumentID: "BTCUSDT", OriginalDataSource: "binance", OriginalSymbol: "BTCUSDT", Interval: "1d", StartTimeMs: bars[0].OpenTime, EndTimeMs: bars[1].OpenTime, BarCount: 2, DirectLineage: saasstore.JSONB(`[]`), RecursiveLineage: saasstore.JSONB(`[]`), CompletedAt: &completedAt}
	if err := source.Create(&perturbationSnapshot).Error; err != nil {
		t.Fatal(err)
	}
	perturbationGroup := saasstore.PerturbationGroup{OwnerUserID: user.ID, GroupKey: "p13-group:backup", Name: "P13 backup", Tags: saasstore.JSONB(`[]`), SourceSnapshotID: perturbationSnapshot.ID, MarketSeriesID: series.ID, AlgorithmVersion: "p13-local-ohlc-v1"}
	if err := source.Create(&perturbationGroup).Error; err != nil {
		t.Fatal(err)
	}
	perturbationVariant := saasstore.PerturbationVariant{OwnerUserID: user.ID, GroupID: perturbationGroup.ID, SourceSnapshotID: perturbationSnapshot.ID, Seed: "42", Alpha: "0.01", GenerationRecipeHash: "p13-recipe:backup", OutputVersionID: outputVersion.ID, OutputInstrumentID: outputInstrumentID, GeneratedContentHash: marketHash, Status: marketversion.VersionStatusCompleted, IntegrityStatus: marketversion.IntegrityValid, BarCount: 2, ComputeTaskID: &computeRoot.ID, CompletedAt: &completedAt}
	if err := source.Create(&perturbationVariant).Error; err != nil {
		t.Fatal(err)
	}
	perturbationTest := saasstore.PerturbationTest{OwnerUserID: user.ID, GroupID: perturbationGroup.ID, TestSpecHash: "p13-test:backup", SchemaVersion: "p13-perturbation-test-v1", Name: "P13 test backup", Tags: saasstore.JSONB(`[]`), Status: "completed", BacktestSettings: saasstore.JSONB(`{}`), CompletedAt: &completedAt}
	if err := source.Create(&perturbationTest).Error; err != nil {
		t.Fatal(err)
	}
	perturbationSubject := saasstore.PerturbationTestSubject{TestID: perturbationTest.ID, Ordinal: 0, SourceKind: "gene_record", SourceID: gene.ID, SourceVersion: "gene-record-v1", SubjectHash: compute.HashBytes(gene.ParamPack), AdoptionUnit: gene.ParamPack, ExecutionInput: saasstore.JSONB(`{}`)}
	if err := source.Create(&perturbationSubject).Error; err != nil {
		t.Fatal(err)
	}
	perturbationBatch := saasstore.PerturbationTestBatch{TestID: perturbationTest.ID, Ordinal: 1, ManifestHash: "p13-manifest:backup", Manifest: saasstore.JSONB(`{}`), ComputeTaskID: &computeRoot.ID, Status: "completed", PlannedCount: 1, CompletedCount: 1, CompletedAt: &completedAt}
	if err := source.Create(&perturbationBatch).Error; err != nil {
		t.Fatal(err)
	}
	perturbationRun := saasstore.PerturbationTestRun{TestID: perturbationTest.ID, BatchID: perturbationBatch.ID, SubjectID: perturbationSubject.ID, DatasetVersionID: outputVersion.ID, DatasetContentHash: marketHash, Alpha: "0.01", Seed: "42", BacktestSpecHash: "p13-backtest-spec", BacktestResultID: &resultID, BacktestResultVersion: backtestresult.ResultSchemaVersion, BacktestResultContentHash: artifacts.ResultContentHash, Status: saasstore.BacktestResultStatusCompleted, Metrics: saasstore.JSONB(`{"relative":{"qualification":"qualified"}}`), MetricHash: "p13-metric", PerformanceReportID: &performanceReportID, CompletedAt: &completedAt}
	if err := source.Create(&perturbationRun).Error; err != nil {
		t.Fatal(err)
	}
	perturbationAnalysis := saasstore.PerturbationAnalysisSnapshot{TestID: perturbationTest.ID, SnapshotKey: "p13-analysis:backup", SchemaVersion: "p13-analysis-v1", AnalysisSetHash: "p13-analysis-set:backup", StatisticsVersion: "p13-statistics-v1", Completeness: "complete", IncludedBatches: saasstore.JSONB(`[1]`), PlannedCount: 1, ValidCount: 1, ContentHash: "p13-analysis-content", Summary: saasstore.JSONB(`{}`)}
	if err := source.Create(&perturbationAnalysis).Error; err != nil {
		t.Fatal(err)
	}
	perturbationMetric := saasstore.PerturbationMetricSummary{AnalysisSnapshotID: perturbationAnalysis.ID, SubjectID: perturbationSubject.ID, Alpha: "0.01", MetricKey: "log_final_nav_ratio", PlannedCount: 1, ValidCount: 1, Statistics: saasstore.JSONB(`{"available":true,"count":1}`), ContentHash: "p13-summary-content"}
	if err := source.Create(&perturbationMetric).Error; err != nil {
		t.Fatal(err)
	}
	perturbationQualification := saasstore.PerturbationQualificationSummary{AnalysisSnapshotID: perturbationAnalysis.ID, SubjectID: perturbationSubject.ID, Alpha: "0.01", ValidCount: 1, QualifiedCount: 1, ContentHash: "p13-qualification-content"}
	if err := source.Create(&perturbationQualification).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Model(&perturbationTest).Update("latest_snapshot_id", perturbationAnalysis.ID).Error; err != nil {
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
	if len(backup.DynamicModelStudies) != 1 || len(backup.DynamicModelArtifacts) != 1 || len(backup.DynamicPredictions) != 1 || len(backup.DynamicPolicies) != 1 || len(backup.DynamicMaterializations) != 1 || len(backup.DynamicReportSnapshots) != 1 || len(backup.DynamicReportBlocks) != 1 {
		t.Fatalf("incomplete P09 backup closure: studies=%d artifacts=%d predictions=%d policies=%d materializations=%d reports=%d blocks=%d", len(backup.DynamicModelStudies), len(backup.DynamicModelArtifacts), len(backup.DynamicPredictions), len(backup.DynamicPolicies), len(backup.DynamicMaterializations), len(backup.DynamicReportSnapshots), len(backup.DynamicReportBlocks))
	}
	if len(backup.ResearchConfigurations) != 1 || len(backup.ResearchRuns) != 1 || len(backup.ResearchPoints) != 1 || len(backup.RobustCandidates) != 1 || len(backup.ResearchComparisons) != 1 || len(backup.SurrogateSnapshots) != 1 || len(backup.SurrogateProposals) != 1 {
		t.Fatalf("incomplete P10 backup closure: configurations=%d runs=%d points=%d candidates=%d comparisons=%d surrogates=%d proposals=%d", len(backup.ResearchConfigurations), len(backup.ResearchRuns), len(backup.ResearchPoints), len(backup.RobustCandidates), len(backup.ResearchComparisons), len(backup.SurrogateSnapshots), len(backup.SurrogateProposals))
	}
	if len(backup.RandomParameterBatches) != 1 || len(backup.RandomParameterRecords) != 1 || len(backup.ControlAnalysisTasks) != 1 || len(backup.ControlEvaluations) != 1 || len(backup.ControlSnapshots) != 1 || len(backup.ControlSnapshotMembers) != 1 {
		t.Fatalf("incomplete P11 backup closure: batches=%d records=%d tasks=%d evaluations=%d snapshots=%d members=%d", len(backup.RandomParameterBatches), len(backup.RandomParameterRecords), len(backup.ControlAnalysisTasks), len(backup.ControlEvaluations), len(backup.ControlSnapshots), len(backup.ControlSnapshotMembers))
	}
	if len(backup.KlineInverseStudies) != 1 || len(backup.KlineInverseCalibrations) != 1 || len(backup.KlineInverseBatches) != 1 || len(backup.KlineInversePaths) != 1 || len(backup.KlineInverseEvaluations) != 1 || len(backup.KlineInverseLineage) != 1 || len(backup.KlineInverseSnapshots) != 1 || len(backup.KlineInverseProbes) != 1 || len(backup.KlineInverseSourceLinks) != 1 {
		t.Fatalf("incomplete P12 backup closure: studies=%d calibrations=%d batches=%d paths=%d evaluations=%d lineage=%d snapshots=%d probes=%d sources=%d", len(backup.KlineInverseStudies), len(backup.KlineInverseCalibrations), len(backup.KlineInverseBatches), len(backup.KlineInversePaths), len(backup.KlineInverseEvaluations), len(backup.KlineInverseLineage), len(backup.KlineInverseSnapshots), len(backup.KlineInverseProbes), len(backup.KlineInverseSourceLinks))
	}
	if len(backup.PerturbationSnapshots) != 1 || len(backup.PerturbationGroups) != 1 || len(backup.PerturbationVariants) != 1 || len(backup.PerturbationTests) != 1 || len(backup.PerturbationSubjects) != 1 || len(backup.PerturbationBatches) != 1 || len(backup.PerturbationRuns) != 1 || len(backup.PerturbationAnalyses) != 1 || len(backup.PerturbationMetrics) != 1 || len(backup.PerturbationQualifications) != 1 {
		t.Fatalf("incomplete P13 backup closure: snapshots=%d groups=%d variants=%d tests=%d subjects=%d batches=%d runs=%d analyses=%d metrics=%d qualifications=%d", len(backup.PerturbationSnapshots), len(backup.PerturbationGroups), len(backup.PerturbationVariants), len(backup.PerturbationTests), len(backup.PerturbationSubjects), len(backup.PerturbationBatches), len(backup.PerturbationRuns), len(backup.PerturbationAnalyses), len(backup.PerturbationMetrics), len(backup.PerturbationQualifications))
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
	var restoredPerturbationRun saasstore.PerturbationTestRun
	if err := target.First(&restoredPerturbationRun, perturbationRun.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restoredPerturbationRun.BacktestResultID == nil || *restoredPerturbationRun.BacktestResultID != resultID || restoredPerturbationRun.PerformanceReportID == nil || *restoredPerturbationRun.PerformanceReportID != performanceReportID || restoredPerturbationRun.DatasetVersionID != outputVersion.ID {
		t.Fatalf("restored P13 run = %+v", restoredPerturbationRun)
	}
	var restoredRobustnessPoint saasstore.RobustnessEvaluationPoint
	if err := target.First(&restoredRobustnessPoint, robustnessPoint.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restoredRobustnessPoint.StudyID != robustnessStudy.ID || restoredRobustnessPoint.BacktestResultID == nil || *restoredRobustnessPoint.BacktestResultID != resultID || restoredRobustnessPoint.MetricsHash != robustnessPoint.MetricsHash {
		t.Fatalf("restored P08 point = %+v", restoredRobustnessPoint)
	}
	var restoredDynamicMaterialization saasstore.DynamicMaterialization
	if err := target.First(&restoredDynamicMaterialization, dynamicMaterialization.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restoredDynamicMaterialization.ContentHash != dynamicMaterialization.ContentHash || restoredDynamicMaterialization.BacktestResultID == nil || *restoredDynamicMaterialization.BacktestResultID != resultID {
		t.Fatalf("restored P09 materialization = %+v", restoredDynamicMaterialization)
	}
	var restoredCandidate saasstore.RobustCandidate
	if err := target.First(&restoredCandidate, candidate.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restoredCandidate.AdoptionUnitHash != candidate.AdoptionUnitHash || restoredCandidate.PointID != researchPoint.ID {
		t.Fatalf("restored P10 candidate = %+v", restoredCandidate)
	}
	var restoredControlTask saasstore.ControlAnalysisTask
	if err := target.First(&restoredControlTask, controlTask.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restoredControlTask.CanonicalHash != controlTask.CanonicalHash || restoredControlTask.LatestSnapshotID == nil || *restoredControlTask.LatestSnapshotID != controlSnapshot.ID {
		t.Fatalf("restored P11 control task = %+v", restoredControlTask)
	}
	var restoredControlEvaluation saasstore.ControlEvaluation
	if err := target.First(&restoredControlEvaluation, controlEvaluation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restoredControlEvaluation.BacktestResultContentHash != artifacts.ResultContentHash || restoredControlEvaluation.PerformanceReportID == nil || *restoredControlEvaluation.PerformanceReportID != performanceReportID {
		t.Fatalf("restored P11 evaluation = %+v", restoredControlEvaluation)
	}
	var restoredInverseStudy saasstore.KlineInverseStudy
	if err := target.First(&restoredInverseStudy, inverseStudy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restoredInverseStudy.CanonicalHash != inverseStudy.CanonicalHash || restoredInverseStudy.CurrentSnapshotID == nil || *restoredInverseStudy.CurrentSnapshotID != inverseSnapshot.ID {
		t.Fatalf("restored P12 study = %+v", restoredInverseStudy)
	}
	var restoredInverseEvaluation saasstore.KlineInverseEvaluation
	if err := target.First(&restoredInverseEvaluation, inverseEvaluation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if restoredInverseEvaluation.PathID != inversePath.ID || restoredInverseEvaluation.BacktestResultContentHash != artifacts.ResultContentHash || !restoredInverseEvaluation.Permanent {
		t.Fatalf("restored P12 evaluation = %+v", restoredInverseEvaluation)
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
