package robustness

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
	core "quantsaas/internal/robustness"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestStudyRunsThroughComputeCacheAndPersistsAnalysis(t *testing.T) {
	db := openRobustnessIntegrationDB(t)
	ctx := context.Background()
	user := saasstore.User{Email: "p08@example.test", PasswordHash: "test-only", Role: "user", Plan: "free", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	intervals, _ := saasstore.NewJSONB([]string{"1d"})
	if err := db.Create(&saasstore.ResearchInstrument{ID: "P08", Symbol: "P08", DisplayName: "P08", DataSource: "binance", SupportedIntervals: intervals, Market: "crypto", SortOrder: 1, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	start := int64(1_700_000_000_000)
	bars := make([]saasstore.KLine, 0, 260)
	for index := 0; index < 260; index++ {
		price := 100 + float64(index)*0.08 + float64(index%19)
		bars = append(bars, saasstore.KLine{InstrumentID: "P08", Source: "binance", Symbol: "P08", Interval: "1d", OpenTime: start + int64(index)*86_400_000, Open: price, High: price * 1.02, Low: price * 0.98, Close: price * (1 + 0.003*float64((index%5)-2)), Volume: 1000})
	}
	if err := db.CreateInBatches(&bars, 100).Error; err != nil {
		t.Fatal(err)
	}
	params := sigmoiddca.DefaultParams()
	paramRaw, _ := json.Marshal(params)
	gene := saasstore.GeneRecord{StrategyID: sigmoiddca.StrategyID, InstrumentID: "P08", DataSource: "binance", Interval: "1d", ExecutionMode: "close_same_bar", Role: saasstore.GeneRoleChallenger, Name: "P08 center", ParamPack: paramRaw, SearchConfig: saasstore.JSONB(`{}`), Tags: saasstore.JSONB(`[]`), WindowScore: saasstore.JSONB(`{}`)}
	if err := db.Create(&gene).Error; err != nil {
		t.Fatal(err)
	}
	registry := computetask.NewRegistry()
	if err := registry.Register(NewPointExecutor(db)); err != nil {
		t.Fatal(err)
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
	service := NewService(db, tasks)
	initial := 10_000.0
	monthly := 100.0
	fee := 0.001
	spread := 0.001
	filter := false
	request := CreateStudyRequest{Name: "Beta 一維穩健性", Mode: ModeOneDimensional, GenomeID: gene.ID, Axes: []string{"beta"}, Radius: 1, Radii: []int{1}, Metric: core.MetricLogFinalNAVRatio, Backtest: BacktestSettings{InstrumentID: "P08", DataSource: "binance", ExecutionMode: "close_same_bar", Symbol: "P08", Interval: "1d", InitialCapital: &initial, MonthlyDCA: &monthly, FeeRate: &fee, SpreadRate: &spread, LongTermFilterEnabled: &filter}}
	created, err := service.Create(ctx, user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Preview.TotalItems != 3 || created.Task == nil || created.Task.Status != compute.TaskStatusPlanned {
		t.Fatalf("unexpected study preview/task: %+v", created)
	}
	if _, err := tasks.StartTask(ctx, user.ID, created.Task.ID); err != nil {
		t.Fatal(err)
	}
	task := waitRobustnessTask(t, tasks, user.ID, created.Task.ID)
	if task.Status != compute.TaskStatusCompleted || task.ValidResultCount != 3 {
		t.Fatalf("task did not complete: %+v", task)
	}
	study, err := service.Get(ctx, user.ID, created.Study.ID)
	if err != nil {
		t.Fatal(err)
	}
	if study.ActualPointCount != 3 || len(study.Points) != 3 || study.Status != compute.TaskStatusCompleted {
		t.Fatalf("study not synchronized: %+v", study)
	}
	analysis, err := service.Analyze(ctx, user.ID, study.ID, AnalyzeRequest{Metric: core.MetricLogFinalNAVRatio, Radii: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if analysis.ContentHash == "" || analysis.Result.ObservedPointSetHash == "" || len(analysis.Result.Scales) != 1 {
		t.Fatalf("analysis was not persisted: %+v", analysis)
	}
	second, err := service.Analyze(ctx, user.ID, study.ID, AnalyzeRequest{Metric: core.MetricLogFinalNAVRatio, Radii: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != analysis.ID {
		t.Fatalf("same immutable analysis was not reused: %d != %d", second.ID, analysis.ID)
	}
	var runCount int64
	if err := db.Model(&saasstore.BacktestRun{}).Count(&runCount).Error; err != nil {
		t.Fatal(err)
	}
	if runCount != 0 {
		t.Fatalf("P08 executor created %d user backtest runs", runCount)
	}
	twoDimensional := request
	twoDimensional.Name = "Gamma × Beta 二維穩健性"
	twoDimensional.Mode = ModeTwoDimensional
	twoDimensional.Axes = []string{"gamma", "beta"}
	twoDimensional.Radii = []int{1, 2}
	twoDPreview, err := service.Preview(ctx, user.ID, twoDimensional)
	if err != nil {
		t.Fatal(err)
	}
	if twoDPreview.TotalItems != 9 || twoDPreview.CacheHitCount != 3 || twoDPreview.NewItemCount != 6 {
		t.Fatalf("overlapping one/two dimensional points did not reuse P05 cache: %+v", twoDPreview)
	}
	twoDCreated, err := service.Create(ctx, user.ID, twoDimensional)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.StartTask(ctx, user.ID, twoDCreated.Task.ID); err != nil {
		t.Fatal(err)
	}
	twoDTask := waitRobustnessTask(t, tasks, user.ID, twoDCreated.Task.ID)
	if twoDTask.Status != compute.TaskStatusCompleted || twoDTask.CacheHitCount != 3 {
		t.Fatalf("two dimensional task did not preserve cache hits: %+v", twoDTask)
	}
	twoDStudy, err := service.Get(ctx, user.ID, twoDCreated.Study.ID)
	if err != nil {
		t.Fatal(err)
	}
	if twoDStudy.ActualPointCount != 9 || len(twoDStudy.Points) != 9 {
		t.Fatalf("cached outputs were not remapped to the two-dimensional manifest: %+v", twoDStudy)
	}
	for _, point := range twoDStudy.Points {
		if len(point.Coordinates) != 2 || len(point.Parameters) == 0 {
			t.Fatalf("invalid two-dimensional point restored from manifest: %+v", point)
		}
	}
	imported, err := service.Import(ctx, user.ID, ImportStudyRequest{Name: "M points", ResearchSettingID: "m-setting-1", ResearchSettingHash: "sha256:m", ParameterSpace: study.ParameterSpace, CenterPointKey: study.Points[0].ID, Points: []ImportPoint{{Kind: core.PointActual, Coordinates: study.Points[0].Coordinates, Parameters: study.Points[0].Parameters, BacktestResultID: study.Points[0].BacktestResultID}, {ID: "prediction", Kind: core.PointPredicted, Coordinates: study.Points[1].Coordinates, Parameters: study.Points[1].Parameters, PredictionMetadata: json.RawMessage(`{"model_version":"rf-v1"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if imported.ActualPointCount != 1 || imported.PredictedPointCount != 1 {
		t.Fatalf("imported point classes were mixed: %+v", imported)
	}
}

func waitRobustnessTask(t *testing.T, tasks *computetask.Service, userID, taskID uint) *computetask.TaskDescriptor {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
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
	t.Fatal("P08 compute task timed out")
	return nil
}

func openRobustnessIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("p08_robustness_%d", time.Now().UnixNano())
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	scoped, err := withRobustnessSearchPath(dsn, schema)
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
func withRobustnessSearchPath(dsn, schema string) (string, error) {
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
