package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	compute "quantsaas/internal/compute"
	"quantsaas/internal/marketversion"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRecompositionPreviewToAtomicPublish(t *testing.T) {
	db := openRecompositionIntegrationDB(t)
	ctx := context.Background()
	user := saasstore.User{Email: "p06@example.test", PasswordHash: "test-only", Role: "user", Plan: "free", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	intervals, _ := saasstore.NewJSONB([]string{"1d"})
	for index, id := range []string{"SOURCE_A", "SOURCE_B"} {
		instrument := saasstore.ResearchInstrument{ID: id, Symbol: id, DisplayName: id, DataSource: DataSourceBinance, SupportedIntervals: intervals, Market: "crypto", SortOrder: index + 1, Enabled: true}
		if err := db.Create(&instrument).Error; err != nil {
			t.Fatal(err)
		}
		rows := make([]saasstore.KLine, 0, 12)
		for day := 0; day < 12; day++ {
			base := 100.0 + float64(index*100+day*3)
			rows = append(rows, saasstore.KLine{
				InstrumentID: id, Source: DataSourceBinance, Symbol: id, Interval: "1d",
				OpenTime: 1_700_000_000_000 + int64(day)*86_400_000,
				Open:     base, High: base + 8, Low: base - 5, Close: base + 3, Volume: 1000 + float64(day),
			})
		}
		if err := db.Create(&rows).Error; err != nil {
			t.Fatal(err)
		}
	}

	marketService := NewService(db, nil)
	registry := computetask.NewRegistry()
	for _, executor := range []computetask.Executor{
		NewRecompositionPreviewExecutor(marketService), NewRecompositionExpandExecutor(marketService),
		NewRecompositionAuditExecutor(marketService), NewRecompositionPublishExecutor(marketService),
	} {
		if err := registry.Register(executor); err != nil {
			t.Fatal(err)
		}
	}
	options := computetask.DefaultOptions()
	options.Workers = 1
	options.SoftItemLimit = 10_000
	options.HardItemLimit = 100_000
	options.PollInterval = 10 * time.Millisecond
	options.LeaseDuration = 5 * time.Second
	tasks, err := computetask.NewService(db, registry, options, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	marketService.SetComputeTasks(tasks)
	if err := tasks.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tasks.Shutdown(shutdownCtx)
	})

	start := int64(1_700_000_000_000)
	preview, err := marketService.CreateRecompositionPreviewTask(ctx, user.ID, RecompositionPreviewRequest{
		Segments: []RecompositionSegmentRequest{
			{ItemID: "a", SourceInstrumentID: "SOURCE_A", StartTimeMs: start + 86_400_000, EndTimeMs: start + 3*86_400_000, RepeatCount: 1},
			{ItemID: "b", SourceInstrumentID: "SOURCE_B", StartTimeMs: start + 2*86_400_000, EndTimeMs: start + 4*86_400_000, RepeatCount: 2},
		},
		Interval: "1d", CalendarInstrumentID: "SOURCE_A", OutputStartTimeMs: start,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.StartTask(ctx, user.ID, preview.Task.ID); err != nil {
		t.Fatal(err)
	}
	previewTask := waitForComputeTask(t, tasks, user.ID, preview.Task.ID)
	if previewTask.Status != compute.TaskStatusCompleted {
		t.Fatalf("preview task status = %s, error=%s", previewTask.Status, previewTask.Error)
	}
	items, err := tasks.Items(ctx, user.ID, preview.Task.ID, computetask.ItemFilter{IncludeResult: true})
	if err != nil || len(items) != 1 {
		t.Fatalf("preview items=%d err=%v", len(items), err)
	}
	var cached previewCacheResult
	raw, _ := json.Marshal(items[0].Result)
	if err := json.Unmarshal(raw, &cached); err != nil || cached.PlanID == 0 {
		t.Fatalf("preview result=%s err=%v", raw, err)
	}
	plan, err := marketService.GetRecompositionPlan(ctx, user.ID, cached.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TotalOutputBars != 9 || plan.InstanceCount != 3 {
		t.Fatalf("preview plan bars=%d instances=%d", plan.TotalOutputBars, plan.InstanceCount)
	}

	generationTask, err := marketService.CreateRecompositionGeneration(ctx, user.ID, RecompositionGenerationRequest{
		PlanID: plan.PlanID, PlanHash: plan.PlanHash, SeriesName: "integration series", IdempotencyKey: "integration-generation-1",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	root, err := tasks.Get(ctx, user.ID, generationTask.Task.ID)
	if err != nil || len(root.ChildTaskIDs) != 3 {
		t.Fatalf("generation root=%+v err=%v", root, err)
	}
	for _, stageID := range root.ChildTaskIDs {
		if _, err := tasks.StartTask(ctx, user.ID, stageID); err != nil {
			t.Fatal(err)
		}
		stage := waitForComputeTask(t, tasks, user.ID, stageID)
		if stage.Status != compute.TaskStatusCompleted {
			t.Fatalf("stage %s status=%s error=%s", stage.StageKey, stage.Status, stage.Error)
		}
	}
	generation, err := marketService.RecompositionGeneration(ctx, user.ID, generationTask.Generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if !generation.Published || generation.Status != marketversion.VersionStatusCompleted || generation.IntegrityStatus != marketversion.IntegrityValid || generation.OutputInstrumentID == "" {
		t.Fatalf("generation=%+v", generation)
	}
	var versionBars, lineage, publishedBars int64
	if err := db.Model(&saasstore.MarketDataVersionBar{}).Where("version_id = ?", generation.VersionID).Count(&versionBars).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&saasstore.RecompositionBarLineage{}).Where("version_id = ?", generation.VersionID).Count(&lineage).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&saasstore.KLine{}).Where("instrument_id = ?", generation.OutputInstrumentID).Count(&publishedBars).Error; err != nil {
		t.Fatal(err)
	}
	if versionBars != 9 || lineage != 9 || publishedBars != 9 {
		t.Fatalf("stored version=%d lineage=%d published=%d", versionBars, lineage, publishedBars)
	}
}

func waitForComputeTask(t *testing.T, service *computetask.Service, userID, taskID uint) *computetask.TaskDescriptor {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		task, err := service.Get(context.Background(), userID, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if compute.IsTerminal(task.Status) {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("compute task timed out")
	return nil
}

func openRecompositionIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("p06_recomposition_%d", time.Now().UnixNano())
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
