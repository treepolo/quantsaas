package dynamicparam

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
	core "quantsaas/internal/dynamicparam"
	"quantsaas/internal/quant"
	"quantsaas/internal/saas/backtest"
	"quantsaas/internal/saas/computetask"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestP09TrainingMaterializationAndImmutableReports(t *testing.T) {
	db := openDynamicIntegrationDB(t)
	ctx := context.Background()
	user := saasstore.User{Email: "p09@example.test", PasswordHash: "test-only", Role: "user", Plan: "free", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	intervals, _ := saasstore.NewJSONB([]string{"1d"})
	if err := db.Create(&saasstore.ResearchInstrument{ID: "P09", Symbol: "P09", DisplayName: "P09", DataSource: "binance", SupportedIntervals: intervals, Market: "crypto", SortOrder: 1, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	start := int64(1_500_000_000_000)
	bars := make([]saasstore.KLine, 0, 190)
	for index := 0; index < 190; index++ {
		center := 100 + 0.12*float64(index) + 4*math.Sin(float64(index)/7)
		open := center * (1 + 0.004*math.Sin(float64(index)/3))
		close := center * (1 + 0.006*math.Cos(float64(index)/5))
		bars = append(bars, saasstore.KLine{InstrumentID: "P09", Source: "binance", Symbol: "P09", Interval: "1d", OpenTime: start + int64(index)*86_400_000, Open: open, High: math.Max(open, close) * 1.02, Low: math.Min(open, close) * 0.98, Close: close, Volume: 1000 + float64(index)})
	}
	if err := db.CreateInBatches(&bars, 100).Error; err != nil {
		t.Fatal(err)
	}
	params := sigmoiddca.DefaultParams()
	params.Spawn.Policy.InitialUSDT = 10_000
	params.Spawn.Policy.MonthlyInjectUSDT = 100
	paramRaw, _ := json.Marshal(params)
	gene := saasstore.GeneRecord{StrategyID: sigmoiddca.StrategyID, InstrumentID: "P09", DataSource: "binance", Interval: "1d", ExecutionMode: "close_next_open", Role: saasstore.GeneRoleChallenger, Name: "P09 base", ParamPack: paramRaw, SearchConfig: saasstore.JSONB(`{}`), Tags: saasstore.JSONB(`[]`), WindowScore: saasstore.JSONB(`{}`)}
	if err := db.Create(&gene).Error; err != nil {
		t.Fatal(err)
	}
	backtestService := backtest.NewService(db)
	registry := computetask.NewRegistry()
	for _, executor := range []computetask.Executor{NewTrainExecutor(db), NewMaterializeExecutor(db, backtestService)} {
		if err := registry.Register(executor); err != nil {
			t.Fatal(err)
		}
	}
	options := computetask.DefaultOptions()
	options.Workers = 2
	options.SoftItemLimit = 10_000
	options.HardItemLimit = 100_000
	options.PollInterval = 10 * time.Millisecond
	options.LeaseDuration = 10 * time.Second
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
	request := CreateStudyRequest{Name: "P09 fixed parity", GenomeID: gene.ID, Route: core.RouteExplainable, Lookbacks: []int{5}, Folds: 2, MinimumTrain: 30, InstrumentID: "P09", DataSource: "binance", Symbol: "P09", Interval: "1d", ExecutionMode: "close_next_open", TrainStartTimeMs: bars[0].OpenTime, TrainEndTimeMs: bars[len(bars)-1].OpenTime, ActivityKappa: 20, RegionRule: core.RegionRule{DirectionBoundary: 0.2, MagnitudeBoundary: 1}, Policy: core.DynamicPolicy{SchemaVersion: core.PolicySchemaVersion, Version: "p09-test-fixed-v1", Controls: []core.ParameterControl{{ParameterID: "beta", Mode: core.ControlFixed, Lower: quant.HardBounds["beta"].Min, Upper: quant.HardBounds["beta"].Max, BaseValue: params.Chromosome.Beta}}}, LongTermFilterEnabled: false}
	created, err := service.Create(ctx, user.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Task == nil || created.Preview.TotalItems != 2 {
		t.Fatalf("unexpected P09 create response: %+v", created)
	}
	task := created.Task
	if _, err := tasks.StartTask(ctx, user.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	training := waitDynamicTask(t, tasks, user.ID, task.ID)
	if training.Status != compute.TaskStatusCompleted {
		t.Fatalf("training failed: %+v", training)
	}
	study, err := service.Get(ctx, user.ID, created.Study.ID)
	if err != nil {
		t.Fatal(err)
	}
	if study.Status != StudyStatusAwaitingMaterialization || study.ArtifactSetHash == "" || study.PredictionSnapshotID == nil || study.PolicyArtifactID == nil || study.Comparison == nil || len(study.Reports) != 6 {
		t.Fatalf("training artifacts incomplete: %+v", study)
	}
	block, err := service.ReportBlock(ctx, user.ID, study.ID, "model-validation")
	if err != nil {
		t.Fatal(err)
	}
	if block.ContentHash == "" || block.PointCount != 6 {
		t.Fatalf("invalid immutable report block: %+v", block)
	}
	materialize, err := service.Materialize(ctx, user.ID, study.ID, MaterializeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	materializeTask := materialize.Task
	if _, err := tasks.StartTask(ctx, user.ID, materializeTask.ID); err != nil {
		t.Fatal(err)
	}
	completed := waitDynamicTask(t, tasks, user.ID, materializeTask.ID)
	if completed.Status != compute.TaskStatusCompleted {
		t.Fatalf("materialization failed: %+v", completed)
	}
	study, err = service.Get(ctx, user.ID, study.ID)
	if err != nil {
		t.Fatal(err)
	}
	if study.Status != StudyStatusCompleted || study.MaterializationID == nil {
		t.Fatalf("materialization was not persisted: %+v", study)
	}
	daily, err := service.ReportBlock(ctx, user.ID, study.ID, "daily-diagnostics")
	if err != nil {
		t.Fatal(err)
	}
	if daily.ContentHash == "" || daily.PointCount == 0 {
		t.Fatalf("daily diagnostics missing: %+v", daily)
	}
	var materialized saasstore.DynamicMaterialization
	if err := db.First(&materialized, *study.MaterializationID).Error; err != nil {
		t.Fatal(err)
	}
	if materialized.BacktestResultID == nil || materialized.BacktestResultContentHash == "" {
		t.Fatalf("standard backtest reference missing: %+v", materialized)
	}
}

func waitDynamicTask(t *testing.T, tasks *computetask.Service, userID, taskID uint) *computetask.TaskDescriptor {
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
	t.Fatal("P09 compute task timed out")
	return nil
}

func openDynamicIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("p09_dynamic_%d", time.Now().UnixNano())
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	scoped, err := dynamicSearchPath(dsn, schema)
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

func dynamicSearchPath(dsn, schema string) (string, error) {
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
