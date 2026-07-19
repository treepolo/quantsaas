package perturbation

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

	compute "quantsaas/internal/compute"
	"quantsaas/internal/marketversion"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPerturbationEndToEndAndIntegrity(t *testing.T) {
	db := openPerturbationIntegrationDB(t)
	ctx := context.Background()
	user := saasstore.User{Email: "p13@example.test", PasswordHash: "test-only", Role: "user", Plan: "free", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	intervals, _ := saasstore.NewJSONB([]string{"1d"})
	instrument := saasstore.ResearchInstrument{ID: "P13", Symbol: "P13", DisplayName: "P13", DataSource: "binance", SupportedIntervals: intervals, Market: "crypto", Enabled: true}
	if err := db.Create(&instrument).Error; err != nil {
		t.Fatal(err)
	}
	start := int64(1_700_000_000_000)
	bars := make([]saasstore.KLine, 0, 180)
	for index := 0; index < 180; index++ {
		center := 100 + .08*float64(index) + 3*math.Sin(float64(index)/9)
		open := center * (1 + .004*math.Sin(float64(index)/4))
		close := center * (1 + .006*math.Cos(float64(index)/7))
		bars = append(bars, saasstore.KLine{InstrumentID: "P13", Source: "binance", Symbol: "P13", Interval: "1d", OpenTime: start + int64(index)*86_400_000, Open: open, High: math.Max(open, close) * 1.018, Low: math.Min(open, close) * .982, Close: close, Volume: 1000 + float64(index)})
	}
	if err := db.CreateInBatches(&bars, 100).Error; err != nil {
		t.Fatal(err)
	}
	paramsRaw, _ := json.Marshal(sigmoiddca.DefaultParams())
	gene := saasstore.GeneRecord{StrategyID: sigmoiddca.StrategyID, InstrumentID: "P13", DataSource: "binance", Interval: "1d", ExecutionMode: saasstore.ExecutionModeCloseSameBar, Role: saasstore.GeneRoleChallenger, Name: "P13 base", ParamPack: paramsRaw, SearchConfig: saasstore.JSONB(`{}`), Tags: saasstore.JSONB(`[]`), WindowScore: saasstore.JSONB(`{}`)}
	if err := db.Create(&gene).Error; err != nil {
		t.Fatal(err)
	}

	registry := computetask.NewRegistry()
	for _, executor := range []computetask.Executor{NewVariantExecutor(db), NewRunExecutor(db, backtest.NewService(db))} {
		if err := registry.Register(executor); err != nil {
			t.Fatal(err)
		}
	}
	options := computetask.DefaultOptions()
	options.Workers, options.SoftItemLimit, options.HardItemLimit = 2, 1000, 10000
	options.PollInterval, options.LeaseDuration = 10*time.Millisecond, 5*time.Second
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
	service := NewService(db, tasks, nil)
	sources, err := service.Sources(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].AvailableStartTimeMs != bars[0].OpenTime || sources[0].AvailableEndTimeMs != bars[len(bars)-1].OpenTime {
		t.Fatalf("unexpected source bounds: %+v", sources)
	}

	groupRequest := GroupPlanRequest{Source: SourceRequest{InstrumentID: "P13", Interval: "1d", StartTimeMs: bars[1].OpenTime, EndTimeMs: bars[len(bars)-1].OpenTime}}
	groupPlan, err := service.PlanGroup(ctx, user.ID, groupRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !groupPlan.PreviousClosePresent || groupPlan.BarCount != len(bars)-1 {
		t.Fatalf("unexpected group plan: %+v", groupPlan)
	}
	group, err := service.CreateGroup(ctx, user.ID, CreateGroupRequest{PlanRequest: groupRequest, PlanHash: groupPlan.PlanHash, Name: "P13 integration"})
	if err != nil {
		t.Fatal(err)
	}
	reusedGroup, err := service.CreateGroup(ctx, user.ID, CreateGroupRequest{PlanRequest: groupRequest, PlanHash: groupPlan.PlanHash, Name: "P13 integration"})
	if err != nil || reusedGroup.ID != group.ID {
		t.Fatalf("exact group was not reused: group=%+v err=%v", reusedGroup, err)
	}
	distinctGroup, err := service.CreateGroup(ctx, user.ID, CreateGroupRequest{PlanRequest: groupRequest, PlanHash: groupPlan.PlanHash, Name: "P13 integration", Notes: "same display name, distinct research identity"})
	if err != nil || distinctGroup.ID == group.ID {
		t.Fatalf("same display name could not create a distinct group: group=%+v err=%v", distinctGroup, err)
	}
	var storedGroups []saasstore.PerturbationGroup
	if err := db.Where("id IN ?", []uint{group.ID, distinctGroup.ID}).Order("id ASC").Find(&storedGroups).Error; err != nil || len(storedGroups) != 2 || storedGroups[0].MarketSeriesID == storedGroups[1].MarketSeriesID || storedGroups[0].SourceSnapshotID != storedGroups[1].SourceSnapshotID {
		t.Fatalf("unexpected group storage isolation: groups=%+v err=%v", storedGroups, err)
	}

	variantInput := VariantPlanRequest{Seeds: []string{"42", "18446744073709551615"}, Alphas: []string{"0.0100", "0.03"}}
	variantPlan, err := service.PlanVariants(ctx, user.ID, group.ID, variantInput)
	if err != nil {
		t.Fatal(err)
	}
	if variantPlan.UniqueVariants != 4 || variantPlan.Alphas[0] != "0.01" {
		t.Fatalf("unexpected variant plan: %+v", variantPlan)
	}
	variantTask, err := service.StartVariants(ctx, user.ID, group.ID, StartVariantsRequest{PlanRequest: variantInput, PlanHash: variantPlan.PlanHash, ConfirmSoftLimit: true})
	if err != nil {
		t.Fatal(err)
	}
	drivePerturbationTask(t, tasks, user.ID, variantTask.Task.ID)
	variants, err := service.ListVariants(ctx, user.ID, group.ID, false)
	if err != nil || len(variants) != 4 {
		t.Fatalf("variants=%+v err=%v", variants, err)
	}
	for _, variant := range variants {
		if variant.Status != marketversion.VersionStatusCompleted || variant.IntegrityStatus != marketversion.IntegrityValid || variant.GeneratedContentHash == "" {
			t.Fatalf("invalid variant: %+v", variant)
		}
	}
	if reusable, err := service.PlanVariants(ctx, user.ID, group.ID, variantInput); err != nil || reusable.ExistingVariants != 4 || reusable.PendingVariants != 0 {
		t.Fatalf("variant reuse failed: %+v err=%v", reusable, err)
	}

	initial, monthly, fee, spread, filter := 10_000.0, 100.0, .001, .001, false
	testRequest := CreateTestRequest{Name: "P13 gene test", GroupID: group.ID, Subjects: []SubjectRequest{{SourceKind: "gene_record", SourceID: gene.ID}}, Backtest: BacktestSettings{ExecutionMode: saasstore.ExecutionModeCloseSameBar, StartTimeMs: group.Snapshot.StartTimeMs, EndTimeMs: group.Snapshot.EndTimeMs, InitialCapital: &initial, MonthlyDCA: &monthly, FeeRate: &fee, SpreadRate: &spread, LongTermFilterEnabled: &filter}}
	testPlan, err := service.PlanTest(ctx, user.ID, TestPlanRequest{CreateTestRequest: testRequest})
	if err != nil {
		t.Fatal(err)
	}
	testRecord, err := service.CreateTest(ctx, user.ID, StartTestRequest{CreateTestRequest: testRequest, PlanHash: testPlan.PlanHash})
	if err != nil {
		t.Fatal(err)
	}
	batchPlan, err := service.PlanBatch(ctx, user.ID, testRecord.ID, BatchPlanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if batchPlan.PlannedRuns != 5 {
		t.Fatalf("unexpected batch plan: %+v", batchPlan)
	}
	batch, err := service.StartBatch(ctx, user.ID, testRecord.ID, StartBatchRequest{PlanHash: batchPlan.PlanHash, ConfirmSoftLimit: true})
	if err != nil {
		t.Fatal(err)
	}
	drivePerturbationTask(t, tasks, user.ID, batch.Task.ID)
	if _, err := service.GetTest(ctx, user.ID, testRecord.ID, true); err != nil {
		t.Fatal(err)
	}
	snapshots, err := service.AnalysisSnapshots(ctx, user.ID, testRecord.ID)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshots=%+v err=%v", snapshots, err)
	}
	detail, err := service.GetAnalysisSnapshot(ctx, user.ID, testRecord.ID, snapshots[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Completeness != "complete" || detail.ValidCount != 5 || len(detail.Metrics) == 0 || len(detail.Qualifications) == 0 {
		t.Fatalf("incomplete analysis: %+v", detail)
	}

	var firstBar saasstore.MarketDataVersionBar
	if err := db.Where("version_id = ?", variants[0].OutputVersionID).Order("ordinal ASC").First(&firstBar).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&firstBar).Update("close", firstBar.Close*1.01).Error; err != nil {
		t.Fatal(err)
	}
	corrupt, err := service.VerifyVariant(ctx, user.ID, variants[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if corrupt.Status != marketversion.VersionStatusCorrupt || corrupt.IntegrityStatus != marketversion.IntegrityCorrupt {
		t.Fatalf("tamper was not quarantined: %+v", corrupt)
	}
	if _, err := service.PlanGroup(ctx, user.ID, GroupPlanRequest{Source: SourceRequest{VersionID: variants[1].OutputVersionID, Interval: "1d", StartTimeMs: group.Snapshot.StartTimeMs, EndTimeMs: group.Snapshot.EndTimeMs}}); err != ErrUnsupportedSource {
		t.Fatalf("L source recursion was accepted: %v", err)
	}
}

func drivePerturbationTask(t *testing.T, tasks *computetask.Service, userID, taskID uint) {
	t.Helper()
	if _, err := tasks.StartTask(context.Background(), userID, taskID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		task, err := tasks.Get(context.Background(), userID, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if compute.IsTerminal(task.Status) {
			if task.Status != compute.TaskStatusCompleted {
				t.Fatalf("task %d status=%s error=%s", taskID, task.Status, task.Error)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %d timed out", taskID)
}

func openPerturbationIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("p13_perturbation_%d", time.Now().UnixNano())
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
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
