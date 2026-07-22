package controlresearch

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/computetask"
	parameterresearchsvc "quantsaas/internal/saas/parameterresearch"
	robustnesssvc "quantsaas/internal/saas/robustness"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestControlAnalysisRunsAllStagesAndPersistsImmutableEvidence(t *testing.T) {
	db := openControlResearchIntegrationDB(t)
	ctx := context.Background()
	user := saasstore.User{Email: "p11@example.test", PasswordHash: "test-only", Role: "user", Plan: "free", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	intervals, _ := saasstore.NewJSONB([]string{"1d"})
	if err := db.Create(&saasstore.ResearchInstrument{ID: "P11", Symbol: "P11", DisplayName: "P11", DataSource: "binance", SupportedIntervals: intervals, Market: "crypto", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	start := int64(1_700_000_000_000)
	bars := make([]saasstore.KLine, 0, 220)
	for index := 0; index < 220; index++ {
		center := 100 + .1*float64(index) + 3*math.Sin(float64(index)/8)
		open := center * (1 + .003*math.Sin(float64(index)/3))
		close := center * (1 + .005*math.Cos(float64(index)/5))
		bars = append(bars, saasstore.KLine{InstrumentID: "P11", Source: "binance", Symbol: "P11", Interval: "1d", OpenTime: start + int64(index)*86_400_000, Open: open, High: math.Max(open, close) * 1.02, Low: math.Min(open, close) * .98, Close: close, Volume: 1000 + float64(index)})
	}
	if err := db.CreateInBatches(&bars, 100).Error; err != nil {
		t.Fatal(err)
	}
	params := sigmoiddca.DefaultParams()
	paramRaw, _ := json.Marshal(params)
	gene := saasstore.GeneRecord{StrategyID: sigmoiddca.StrategyID, InstrumentID: "P11", DataSource: "binance", Interval: "1d", ExecutionMode: saasstore.ExecutionModeCloseSameBar, Role: saasstore.GeneRoleChallenger, Name: "P11 base", ParamPack: paramRaw, SearchConfig: saasstore.JSONB(`{}`), Tags: saasstore.JSONB(`[]`), WindowScore: saasstore.JSONB(`{}`)}
	if err := db.Create(&gene).Error; err != nil {
		t.Fatal(err)
	}

	registry := computetask.NewRegistry()
	backtests := backtest.NewService(db)
	for _, executor := range []computetask.Executor{robustnesssvc.NewPointExecutor(db), NewExecutor(db, backtests)} {
		if err := registry.Register(executor); err != nil {
			t.Fatal(err)
		}
	}
	options := computetask.DefaultOptions()
	options.Workers = 2
	options.SoftItemLimit = 100
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
	parameterResearch := parameterresearchsvc.NewService(db, tasks, robustnesssvc.NewService(db, tasks))
	service := NewService(db, tasks, parameterResearch)
	initial, monthly, fee, spread, filter := 10_000.0, 100.0, .001, .001, false
	request := CreateRequest{Name: "P11 control", GenomeID: gene.ID, RandomSeed: 42, RandomCount: 3, ShuffleSeed: 84, ShuffleCount: 3, ToggleEveryNBars: 7, ConfirmSoftLimit: true, Backtest: robustnesssvc.BacktestSettings{InstrumentID: "P11", DataSource: "binance", Symbol: "P11", Interval: "1d", ExecutionMode: saasstore.ExecutionModeCloseSameBar, StartTimeMs: bars[0].OpenTime, EndTimeMs: bars[len(bars)-1].OpenTime, InitialCapital: &initial, MonthlyDCA: &monthly, FeeRate: &fee, SpreadRate: &spread, LongTermFilterEnabled: &filter}}
	preview, err := service.Preview(ctx, user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if preview.RandomCount != 3 || preview.ShuffleCount != 3 || len(preview.Compute.Stages) != 4 || len(preview.RandomDimensions) == 0 {
		t.Fatalf("unexpected P11 preview: %+v", preview)
	}
	request.ExpectedPlanKey = preview.PlanKey
	task, err := service.Create(ctx, user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	task = driveControlTask(t, service, user.ID, task.ID)
	if task.Status != "completed" || task.LatestSnapshot == nil || task.LatestSnapshot.Completeness != "completed" {
		t.Fatalf("P11 task did not complete: %+v", task)
	}
	if task.LatestSnapshot.RandomCompletedCount != 3 || task.LatestSnapshot.ShuffleCompletedCount != 3 || task.LatestSnapshot.RuleCompletedCount != 4 || len(task.LatestSnapshot.Summary.Rules) != 4 {
		t.Fatalf("P11 evidence groups are incomplete: %+v", task.LatestSnapshot)
	}
	if len(task.LatestSnapshot.Summary.Baseline.ReturnDistributions) != 3 {
		t.Fatalf("P04 distribution semantics were not retained: %+v", task.LatestSnapshot.Summary.Baseline)
	}
	reopened, err := service.List(ctx, user.ID, 10)
	if err != nil || len(reopened) != 1 || reopened[0].ID != task.ID {
		t.Fatalf("P11 task cannot be reopened: %+v err=%v", reopened, err)
	}
	var evaluationBefore saasstore.ControlEvaluation
	if err := db.Where("task_id = ?", task.ID).Order("updated_at DESC").First(&evaluationBefore).Error; err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := service.Get(ctx, user.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	var evaluationAfter saasstore.ControlEvaluation
	if err := db.First(&evaluationAfter, evaluationBefore.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !evaluationAfter.UpdatedAt.Equal(evaluationBefore.UpdatedAt) {
		t.Fatalf("read-only polling rewrote an existing evaluation: before=%s after=%s", evaluationBefore.UpdatedAt, evaluationAfter.UpdatedAt)
	}
	reused, err := service.Create(ctx, user.ID, request)
	if err != nil || reused.ID != task.ID {
		t.Fatalf("same P11 identity was not reused: %+v err=%v", reused, err)
	}
	var geneCount int64
	if err := db.Model(&saasstore.GeneRecord{}).Count(&geneCount).Error; err != nil {
		t.Fatal(err)
	}
	if geneCount != 1 {
		t.Fatalf("random parameters polluted the gene library: %d", geneCount)
	}
	var runCount int64
	if err := db.Model(&saasstore.BacktestRun{}).Count(&runCount).Error; err != nil || runCount != 0 {
		t.Fatalf("P11 created UI backtest runs: %d err=%v", runCount, err)
	}
	records, err := service.RandomRecords(ctx, user.ID, task.RandomBatchID, 100, 0)
	if err != nil || len(records) != 3 {
		t.Fatalf("random records were not persisted: %+v err=%v", records, err)
	}
	extension, err := service.PreviewExtension(ctx, user.ID, task.ID, ExtendRequest{RandomCount: 5, ShuffleCount: 5})
	if err != nil || extension.Compute.NewItemCount != 4 || extension.Compute.CacheHitCount < 11 {
		t.Fatalf("append preview did not preserve prior work: %+v err=%v", extension, err)
	}
	comparison, err := service.Comparison(ctx, user.ID, task.ID, task.LatestSnapshot.ID)
	if err != nil || comparison.SourceKind != "control_analysis_snapshot" || comparison.ContentHash == "" {
		t.Fatalf("N comparison descriptor is incomplete: %+v err=%v", comparison, err)
	}
	impact, err := service.DeleteImpact(ctx, user.ID, task.ID)
	if err != nil || impact["hard_delete_allowed"] != true || impact["path_delete_allowed"] != true {
		t.Fatalf("P11 cleanup impact is incomplete: %+v err=%v", impact, err)
	}
	if _, err := service.DeleteTask(ctx, user.ID, task.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteUnusedBatch(ctx, user.ID, task.RandomBatchID, true); err != nil {
		t.Fatal(err)
	}
	var standardResultCount int64
	if err := db.Model(&saasstore.BacktestResult{}).Count(&standardResultCount).Error; err != nil || standardResultCount == 0 {
		t.Fatalf("task cleanup deleted shared standardized results: %d err=%v", standardResultCount, err)
	}
}

func driveControlTask(t *testing.T, service *Service, userID, taskID uint) TaskDescriptor {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		task, err := service.Get(context.Background(), userID, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == "completed" {
			return task
		}
		if task.Status == "failed" {
			t.Fatalf("P11 task failed: %+v", task)
		}
		if _, err := service.StartNext(context.Background(), userID, taskID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("P11 control task timed out")
	return TaskDescriptor{}
}

func openControlResearchIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("p11_control_research_%d", time.Now().UnixNano())
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	scoped, err := withControlResearchSearchPath(dsn, schema)
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
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func withControlResearchSearchPath(dsn, schema string) (string, error) {
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
