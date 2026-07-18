package parameterresearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	compute "quantsaas/internal/compute"
	dynamiccore "quantsaas/internal/dynamicparam"
	"quantsaas/internal/quant"
	robust "quantsaas/internal/robustness"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/computetask"
	dynamicparamsvc "quantsaas/internal/saas/dynamicparam"
	robustnesssvc "quantsaas/internal/saas/robustness"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestStaticResearchRunsStandardBacktestsAndPromotesCandidate(t *testing.T) {
	db := openParameterResearchIntegrationDB(t)
	ctx := context.Background()
	user := saasstore.User{Email: "p10@example.test", PasswordHash: "test-only", Role: "user", Plan: "free", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	intervals, _ := saasstore.NewJSONB([]string{"1d"})
	if err := db.Create(&saasstore.ResearchInstrument{ID: "P10", Symbol: "P10", DisplayName: "P10", DataSource: "binance", SupportedIntervals: intervals, Market: "crypto", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	start := int64(1_700_000_000_000)
	bars := make([]saasstore.KLine, 0, 260)
	for index := 0; index < 260; index++ {
		center := 100 + .12*float64(index) + 4*math.Sin(float64(index)/7)
		open := center * (1 + .004*math.Sin(float64(index)/3))
		close := center * (1 + .006*math.Cos(float64(index)/5))
		bars = append(bars, saasstore.KLine{InstrumentID: "P10", Source: "binance", Symbol: "P10", Interval: "1d", OpenTime: start + int64(index)*86_400_000, Open: open, High: math.Max(open, close) * 1.02, Low: math.Min(open, close) * .98, Close: close, Volume: 1000 + float64(index)})
	}
	if err := db.CreateInBatches(&bars, 100).Error; err != nil {
		t.Fatal(err)
	}
	params := sigmoiddca.DefaultParams()
	paramRaw, _ := json.Marshal(params)
	gene := saasstore.GeneRecord{StrategyID: sigmoiddca.StrategyID, InstrumentID: "P10", DataSource: "binance", Interval: "1d", ExecutionMode: saasstore.ExecutionModeCloseSameBar, Role: saasstore.GeneRoleChallenger, Name: "P10 base", ParamPack: paramRaw, SearchConfig: saasstore.JSONB(`{}`), Tags: saasstore.JSONB(`[]`), WindowScore: saasstore.JSONB(`{}`)}
	if err := db.Create(&gene).Error; err != nil {
		t.Fatal(err)
	}

	registry := computetask.NewRegistry()
	if err := registry.Register(robustnesssvc.NewPointExecutor(db)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(NewSurrogateExecutor()); err != nil {
		t.Fatal(err)
	}
	for _, executor := range []computetask.Executor{dynamicparamsvc.NewTrainExecutor(db), dynamicparamsvc.NewMaterializeExecutor(db, backtest.NewService(db))} {
		if err := registry.Register(executor); err != nil {
			t.Fatal(err)
		}
	}
	options := computetask.DefaultOptions()
	options.Workers = 2
	options.SoftItemLimit = 200
	options.HardItemLimit = 1000
	options.PollInterval = 10 * time.Millisecond
	options.LeaseDuration = 5 * time.Second
	tasks, err := computetask.NewService(db, registry, options, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tasks.Shutdown(shutdown)
	})
	service := NewService(db, tasks, robustnesssvc.NewService(db, tasks))

	space, err := robust.BuildLocalSpace(params, []string{"beta"}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	initial, monthly, fee, spread, filter := 10_000.0, 100.0, .001, .001, false
	request := CreateConfigurationRequest{Name: "P10 static", GenomeID: gene.ID, ParameterSpace: space, BaseCoordinates: []int{1}, Backtest: robustnesssvc.BacktestSettings{InstrumentID: "P10", DataSource: "binance", Symbol: "P10", Interval: "1d", ExecutionMode: saasstore.ExecutionModeCloseSameBar, StartTimeMs: bars[0].OpenTime, EndTimeMs: bars[len(bars)-1].OpenTime, InitialCapital: &initial, MonthlyDCA: &monthly, FeeRate: &fee, SpreadRate: &spread, LongTermFilterEnabled: &filter}}
	configuration, err := service.CreateConfiguration(ctx, user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := service.CreateConfiguration(ctx, user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID != configuration.ID || reused.ConfigHash != configuration.ConfigHash {
		t.Fatalf("immutable configuration was not reused: %+v %+v", configuration, reused)
	}
	plan, err := service.PlanInitialRun(ctx, user.ID, configuration.ID, RunPlanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Global == nil || plan.Global.Mode != "full_enumeration" || len(plan.Points) != 3 {
		t.Fatalf("unexpected initial plan: %+v", plan)
	}
	run, err := service.StartRun(ctx, user.ID, configuration.ID, StartRunRequest{Plan: RunPlanRequest{}, PlanKey: plan.PlanKey, IdempotencyKey: "p10-integration", ConfirmSoftLimit: true})
	if err != nil {
		t.Fatal(err)
	}
	run = waitParameterResearchRun(t, service, user.ID, run.ID)
	if run.Status != compute.TaskStatusCompleted || len(run.Points) != 3 {
		t.Fatalf("research run did not complete: %+v", run)
	}
	for _, point := range run.Points {
		if point.BacktestResultID == nil || point.BacktestResultContentHash == "" || point.Metrics == nil {
			t.Fatalf("point is not backed by a standard result: %+v", point)
		}
	}
	var userRuns int64
	if err := db.Model(&saasstore.BacktestRun{}).Count(&userRuns).Error; err != nil {
		t.Fatal(err)
	}
	if userRuns != 0 {
		t.Fatalf("P10 created %d UI backtest runs", userRuns)
	}

	reopened, err := service.ListRuns(ctx, user.ID, configuration.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened) != 1 || reopened[0].ID != run.ID {
		t.Fatalf("run cannot be reopened: %+v", reopened)
	}
	page, err := service.ListPoints(ctx, user.ID, run.ID, 1, 2, "completed")
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Items) != 2 || page.TotalPages != 2 {
		t.Fatalf("unexpected point page: %+v", page)
	}

	analysis, err := service.AnalyzeRun(ctx, user.ID, run.ID, AnalysisRequest{Metric: robust.MetricLogFinalNAVRatio, Radii: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if analysis.ContentHash == "" || analysis.RobustnessSnapshotID == 0 {
		t.Fatalf("analysis was not persisted: %+v", analysis)
	}
	candidate, err := service.CreateManualCandidate(ctx, user.ID, run.Points[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidate.AnalysisLinks) != 4 {
		t.Fatalf("candidate does not have G/H/L/C slots: %+v", candidate)
	}
	seenKinds := map[string]bool{}
	for _, link := range candidate.AnalysisLinks {
		seenKinds[link.Kind] = true
	}
	for _, kind := range []string{"G", "H", "L", "C"} {
		if !seenKinds[kind] {
			t.Fatalf("candidate is missing %s analysis link: %+v", kind, candidate.AnalysisLinks)
		}
	}
	if seenKinds["E"] || configuration.DatasetHash == "" {
		t.Fatalf("E must remain an immutable dataset identity, not an analysis link: dataset=%q links=%+v", configuration.DatasetHash, candidate.AnalysisLinks)
	}
	statuses := []string{"not_calculated", "running", "partially_completed", "completed", "failed", "cancelled", "not_applicable"}
	for _, kind := range []string{"G", "H", "L", "C"} {
		for _, status := range statuses {
			updated, updateErr := service.UpdateAnalysisLink(ctx, user.ID, candidate.ID, kind, UpdateAnalysisLinkRequest{Status: status, SourceID: "p14-" + strings.ToLower(kind), SourceVersion: "p14-fixed-v1", SourceContentHash: "p14-fixed-hash-" + strings.ToLower(kind), PartialSnapshot: json.RawMessage(`{"checkpoint":1}`)})
			if updateErr != nil {
				t.Fatalf("%s status %s failed: %v", kind, status, updateErr)
			}
			for _, link := range updated.AnalysisLinks {
				if link.Kind == kind && link.Status != status {
					t.Fatalf("%s status=%s, want %s", kind, link.Status, status)
				}
			}
		}
		if _, updateErr := service.UpdateAnalysisLink(ctx, user.ID, candidate.ID, kind, UpdateAnalysisLinkRequest{Status: "completed", SourceID: "p14-" + strings.ToLower(kind), SourceVersion: "p14-fixed-v1", SourceContentHash: "p14-fixed-hash-" + strings.ToLower(kind), PartialSnapshot: json.RawMessage(`{"checkpoint":2}`)}); updateErr != nil {
			t.Fatal(updateErr)
		}
	}
	candidateComparison, err := service.CandidateComparison(ctx, user.ID, candidate.ID)
	if err != nil || candidateComparison.ContentHash != candidate.AdoptionUnitHash || candidateComparison.SnapshotID != 0 {
		t.Fatalf("candidate comparison identity is not fixed: %+v err=%v", candidateComparison, err)
	}
	analysisComparison, err := service.AnalysisComparison(ctx, user.ID, analysis.ID)
	if err != nil || analysisComparison.ContentHash != analysis.ContentHash || analysisComparison.SnapshotID != analysis.ID {
		t.Fatalf("analysis comparison identity is not fixed: %+v err=%v", analysisComparison, err)
	}
	landscapeBlock, err := service.AnalysisComparisonBlock(ctx, user.ID, analysis.ID, "performance_landscape", analysis.ContentHash)
	if err != nil || landscapeBlock.ContentHash == "" || len(landscapeBlock.Payload) == 0 {
		t.Fatalf("analysis comparison block is unavailable: %+v err=%v", landscapeBlock, err)
	}
	if _, err := service.AnalysisComparisonBlock(ctx, user.ID, analysis.ID, "performance_landscape", "stale-hash"); !errors.Is(err, ErrPlanStale) {
		t.Fatalf("stale comparison hash was accepted: %v", err)
	}
	exported, err := service.ExportCandidate(ctx, user.ID, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	reexported, err := service.ExportCandidate(ctx, user.ID, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exported.GeneRecordID == nil || reexported.GeneRecordID == nil || *exported.GeneRecordID != *reexported.GeneRecordID {
		t.Fatalf("library export was not idempotent: %+v %+v", exported, reexported)
	}
	promoted, err := service.PromoteCandidate(ctx, user.ID, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	var promotedGene saasstore.GeneRecord
	if err := db.First(&promotedGene, *promoted.GeneRecordID).Error; err != nil {
		t.Fatal(err)
	}
	if promotedGene.Role != saasstore.GeneRoleChampion {
		t.Fatalf("candidate was not promoted: %+v", promotedGene)
	}

	dynamicPolicy := dynamiccore.DynamicPolicy{SchemaVersion: dynamiccore.PolicySchemaVersion, Version: "p10-global-policy-v1", Controls: []dynamiccore.ParameterControl{{ParameterID: "beta", Mode: dynamiccore.ControlGlobal, Lower: quant.HardBounds["beta"].Min, Upper: quant.HardBounds["beta"].Max, BaseValue: params.Chromosome.Beta, GlobalValue: params.Chromosome.Beta}}}
	dynamicService := dynamicparamsvc.NewService(db, tasks)
	dynamicCreated, err := dynamicService.Create(ctx, user.ID, dynamicparamsvc.CreateStudyRequest{Name: "P10 dynamic source", GenomeID: gene.ID, Route: dynamiccore.RouteExplainable, Lookbacks: []int{5}, Folds: 2, MinimumTrain: 30, InstrumentID: "P10", DataSource: "binance", Symbol: "P10", Interval: "1d", ExecutionMode: saasstore.ExecutionModeCloseSameBar, TrainStartTimeMs: bars[0].OpenTime, TrainEndTimeMs: bars[len(bars)-1].OpenTime, ActivityKappa: 20, RegionRule: dynamiccore.RegionRule{DirectionBoundary: .2, MagnitudeBoundary: 1}, Policy: dynamicPolicy, LongTermFilterEnabled: false, ConfirmSoftLimit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.StartTask(ctx, user.ID, dynamicCreated.Task.ID); err != nil {
		t.Fatal(err)
	}
	dynamicTask := waitParameterResearchTask(t, tasks, user.ID, dynamicCreated.Task.ID)
	if dynamicTask.Status != compute.TaskStatusCompleted {
		t.Fatalf("P09 source training failed: %+v", dynamicTask)
	}
	dynamicStudy, err := dynamicService.Get(ctx, user.ID, dynamicCreated.Study.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dynamicStudy.PolicyArtifactID == nil {
		t.Fatalf("dynamic policy was not persisted: %+v", dynamicStudy)
	}
	dynamicSpace, err := service.DynamicSpace(ctx, user.ID, *dynamicStudy.PolicyArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dynamicSpace.Schema.Variables) != 1 {
		t.Fatalf("unexpected K parameter space: %+v", dynamicSpace)
	}
	variable := dynamicSpace.Schema.Variables[0]
	step := variable.MinimumStep
	center := dynamicSpace.BaseValues[variable.StableID]
	values := []float64{center - step, center, center + step}
	dynamicResearchSpace := robust.ParameterSpace{SchemaVersion: robust.GridVersion, Axes: []robust.ParameterAxis{{Name: variable.StableID, Label: "動態政策全域值", Type: robust.ParameterFloat, Values: values, LegalMin: variable.Lower, LegalMax: variable.Upper, Step: step, StudyStart: 0, StudyEnd: 2}}, Fixed: map[string]float64{}}
	dynamicRequest := request
	dynamicRequest.Name = "P10 dynamic"
	dynamicRequest.ParameterSpace = dynamicResearchSpace
	dynamicRequest.BaseCoordinates = []int{1}
	dynamicRequest.Dynamic = &DynamicReference{StudyID: dynamicStudy.ID, PolicyArtifactID: *dynamicStudy.PolicyArtifactID}
	dynamicConfiguration, err := service.CreateConfiguration(ctx, user.ID, dynamicRequest)
	if err != nil {
		t.Fatal(err)
	}
	dynamicPlan, err := service.PlanInitialRun(ctx, user.ID, dynamicConfiguration.ID, RunPlanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	dynamicRun, err := service.StartRun(ctx, user.ID, dynamicConfiguration.ID, StartRunRequest{Plan: RunPlanRequest{}, PlanKey: dynamicPlan.PlanKey, IdempotencyKey: "p10-dynamic-integration", ConfirmSoftLimit: true})
	if err != nil {
		t.Fatal(err)
	}
	dynamicRun = waitParameterResearchRun(t, service, user.ID, dynamicRun.ID)
	if dynamicRun.Status != compute.TaskStatusCompleted || len(dynamicRun.Points) != 3 {
		t.Fatalf("K dynamic research did not complete: %+v", dynamicRun)
	}
	dynamicCandidate, err := service.CreateManualCandidate(ctx, user.ID, dynamicRun.Points[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var storedCandidate saasstore.RobustCandidate
	if err := db.First(&storedCandidate, dynamicCandidate.ID).Error; err != nil {
		t.Fatal(err)
	}
	var adoption map[string]any
	if json.Unmarshal(storedCandidate.AdoptionUnit, &adoption) != nil || adoption["model_artifact_hash"] == "" || adoption["policy_bundle"] == nil {
		t.Fatalf("dynamic adoption unit is incomplete: %s", storedCandidate.AdoptionUnit)
	}
	series, err := service.CreateSeries(ctx, user.ID, CreateSeriesRequest{Name: "P14 fixed comparison", ConfigurationIDs: []uint{configuration.ID, dynamicConfiguration.ID}, ChangedFactors: []string{"dynamic_mode"}})
	if err != nil {
		t.Fatal(err)
	}
	seriesComparison, err := service.SeriesComparison(ctx, user.ID, series.ID, series.SnapshotID)
	if err != nil || seriesComparison.ContentHash != series.ContentHash || seriesComparison.SnapshotID != series.SnapshotID {
		t.Fatalf("series comparison identity is not fixed: %+v err=%v", seriesComparison, err)
	}
	seriesBlock, err := service.SeriesComparisonBlock(ctx, user.ID, series.ID, series.SnapshotID, "comparison_context", series.ContentHash)
	if err != nil || seriesBlock.ContentHash == "" || len(seriesBlock.Payload) == 0 {
		t.Fatalf("series comparison block is unavailable: %+v err=%v", seriesBlock, err)
	}
}

func waitParameterResearchTask(t *testing.T, tasks *computetask.Service, userID, taskID uint) *computetask.TaskDescriptor {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		task, err := tasks.Get(context.Background(), userID, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if compute.IsTerminal(task.Status) || task.Status == compute.TaskStatusPartial {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("P10 dependency task timed out")
	return nil
}

func waitParameterResearchRun(t *testing.T, service *Service, userID, runID uint) RunDescriptor {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		run, err := service.GetRun(context.Background(), userID, runID, true)
		if err != nil {
			t.Fatal(err)
		}
		if compute.IsTerminal(run.Status) || run.Status == compute.TaskStatusPartial {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("P10 research run timed out")
	return RunDescriptor{}
}

func openParameterResearchIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("p10_parameter_research_%d", time.Now().UnixNano())
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	scoped, err := withParameterResearchSearchPath(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(postgres.Open(scoped), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := saasstore.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	verifyResearchSeriesIndexUpgrade(t, db)
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

type legacyResearchSeriesIndex struct {
	SeriesKey string `gorm:"size:128;not null;uniqueIndex:idx_research_series_owner_key"`
}

func (legacyResearchSeriesIndex) TableName() string { return "parameter_research_series" }

func verifyResearchSeriesIndexUpgrade(t *testing.T, db *gorm.DB) {
	t.Helper()
	const indexName = "idx_research_series_owner_key"
	if err := db.Migrator().DropIndex(&saasstore.ResearchSeries{}, indexName); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().CreateIndex(&legacyResearchSeriesIndex{}, indexName); err != nil {
		t.Fatal(err)
	}
	if err := saasstore.AutoMigrate(db); err != nil {
		t.Fatalf("upgrade legacy research series index: %v", err)
	}
	indexes, err := db.Migrator().GetIndexes(&saasstore.ResearchSeries{})
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range indexes {
		if index.Name() != indexName {
			continue
		}
		unique, _ := index.Unique()
		columns := map[string]bool{}
		for _, column := range index.Columns() {
			columns[column] = true
		}
		if unique && len(columns) == 2 && columns["owner_user_id"] && columns["series_key"] {
			return
		}
	}
	t.Fatalf("%s was not upgraded to unique(owner_user_id, series_key)", indexName)
}

func withParameterResearchSearchPath(dsn, schema string) (string, error) {
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
