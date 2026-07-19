package computetask

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	compute "quantsaas/internal/compute"
	saasstore "quantsaas/internal/saas/store"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type integrationExecutor struct {
	mu             sync.Mutex
	calls          map[string]int
	failOnce       map[string]bool
	block          map[string]chan struct{}
	started        chan string
	resumedFrom    map[string]bool
	executionDelay time.Duration
}

func newIntegrationExecutor() *integrationExecutor {
	return &integrationExecutor{
		calls: make(map[string]int), failOnce: make(map[string]bool), block: make(map[string]chan struct{}),
		started: make(chan string, 32), resumedFrom: make(map[string]bool),
	}
}

func (e *integrationExecutor) Descriptor() compute.ExecutorDescriptor {
	return compute.ExecutorDescriptor{Type: "p05.integration", Version: "v1", ResultSchemaVersion: "result-v1"}
}

func (e *integrationExecutor) Execute(ctx context.Context, execution Execution) (json.RawMessage, error) {
	var input struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(execution.Input, &input); err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.calls[input.Value]++
	call := e.calls[input.Value]
	shouldFail := e.failOnce[input.Value] && call == 1
	block := e.block[input.Value]
	if len(execution.Checkpoint) > 0 && string(execution.Checkpoint) != "{}" {
		e.resumedFrom[input.Value] = true
	}
	delay := e.executionDelay
	e.mu.Unlock()
	if execution.Report != nil {
		_ = execution.Report(ctx, ProgressUpdate{Progress: 0.25, Checkpoint: json.RawMessage(`{"step":1}`), RNGPosition: 1})
	}
	select {
	case e.started <- input.Value:
	default:
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if shouldFail {
		return nil, fmt.Errorf("planned failure for %s", input.Value)
	}
	return json.RawMessage(fmt.Sprintf(`{"value":%q}`, input.Value)), nil
}

func (e *integrationExecutor) callCount(key string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[key]
}

func (e *integrationExecutor) resumed(key string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resumedFrom[key]
}

func TestComputeTaskRetryAndCacheReuse(t *testing.T) {
	db := openComputeIntegrationDB(t)
	user := createComputeIntegrationUser(t, db, "retry")
	executor := newIntegrationExecutor()
	executor.failOnce["b"] = true
	service := newComputeIntegrationService(t, db, executor, 3)
	ctx := context.Background()
	spec := integrationCreateSpec("retry-plan", "a", "b", "c")
	task, err := service.Create(ctx, user.ID, spec, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, user.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	partial := waitForTaskStatus(t, service, user.ID, task.ID, compute.TaskStatusPartial)
	if partial.ValidResultCount != 2 || partial.FailedCount != 1 || partial.Progress >= 1 {
		t.Fatalf("partial task counts = %+v", partial)
	}
	if _, err := service.Retry(ctx, user.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	completed := waitForTaskStatus(t, service, user.ID, task.ID, compute.TaskStatusCompleted)
	if completed.ValidResultCount != 3 || completed.FailedCount != 0 || completed.Progress != 1 {
		t.Fatalf("completed task counts = %+v", completed)
	}
	if executor.callCount("a") != 1 || executor.callCount("b") != 2 || executor.callCount("c") != 1 {
		t.Fatalf("unexpected retry calls: a=%d b=%d c=%d", executor.callCount("a"), executor.callCount("b"), executor.callCount("c"))
	}
	if !executor.resumed("b") {
		t.Fatal("failed item did not resume from its persisted checkpoint")
	}
	preview, err := service.Preview(ctx, user.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CacheHitCount != 3 || preview.NewItemCount != 0 {
		t.Fatalf("cache preview = %+v", preview)
	}
	reused, err := service.Create(ctx, user.ID, spec, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reused.Reused || reused.ID != task.ID {
		t.Fatalf("same immutable plan was not deduplicated: %+v", reused)
	}
	cacheOnlySpec := integrationCreateSpec("cache-only-plan", "a", "b", "c")
	cacheOnlySpec.Settings = map[string]any{"view": "different"}
	cacheOnly, err := service.Create(ctx, user.ID, cacheOnlySpec, true)
	if err != nil {
		t.Fatal(err)
	}
	if cacheOnly.Status != compute.TaskStatusCompleted || cacheOnly.CacheHitCount != 3 {
		t.Fatalf("cached task = %+v", cacheOnly)
	}
	storedItems, err := service.Items(ctx, user.ID, task.ID, ItemFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(storedItems[0].Result) != 0 {
		t.Fatal("item list unexpectedly included result payload")
	}
	itemsWithResults, err := service.Items(ctx, user.ID, task.ID, ItemFilter{Limit: 10, IncludeResult: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsWithResults[0].Result) == 0 {
		t.Fatal("explicit item result request omitted result payload")
	}
	summary, err := service.Get(ctx, user.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Settings) != 0 || len(summary.Manifest) != 0 {
		t.Fatal("task summary unexpectedly included immutable snapshot payload")
	}
	snapshot, err := service.Snapshot(ctx, user.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SettingsHash != summary.SettingsHash || snapshot.ManifestHash != summary.ManifestHash || len(snapshot.Settings) == 0 || len(snapshot.Manifest) == 0 {
		t.Fatalf("lazy task snapshot is incomplete: %+v", snapshot)
	}
	lookup, err := service.LookupCache(ctx, user.ID, storedItems[0].CacheKey)
	if err != nil || !lookup.Found {
		t.Fatalf("owner cache lookup = %+v, err=%v", lookup, err)
	}
	foreignLookup, err := service.LookupCache(ctx, user.ID+999, storedItems[0].CacheKey)
	if err != nil || foreignLookup.Found {
		t.Fatalf("cross-user cache lookup leaked result = %+v, err=%v", foreignLookup, err)
	}
	if storedItems[0].CacheEntryID == nil {
		t.Fatal("completed item has no cache entry")
	}
	cacheID := *storedItems[0].CacheEntryID
	if err := db.Model(&saasstore.ComputeCacheEntry{}).Where("id = ?", cacheID).
		Update("result", saasstore.JSONB(`{"tampered":true}`)).Error; err != nil {
		t.Fatal(err)
	}
	corruptLookup, err := service.LookupCache(ctx, user.ID, storedItems[0].CacheKey)
	if err != nil || corruptLookup.Found {
		t.Fatalf("corrupt cache lookup = %+v, err=%v", corruptLookup, err)
	}
	var cacheAfterLookup saasstore.ComputeCacheEntry
	if err := db.First(&cacheAfterLookup, cacheID).Error; err != nil {
		t.Fatal(err)
	}
	if cacheAfterLookup.Status != compute.CacheStatusCompleted {
		t.Fatalf("read-only lookup mutated corrupt cache status to %s", cacheAfterLookup.Status)
	}
}

func TestComputeTaskConcurrentDeduplication(t *testing.T) {
	db := openComputeIntegrationDB(t)
	user := createComputeIntegrationUser(t, db, "dedupe")
	executor := newIntegrationExecutor()
	release := make(chan struct{})
	executor.block["shared"] = release
	service := newComputeIntegrationService(t, db, executor, 2)
	ctx := context.Background()
	firstSpec := integrationCreateSpec("first-plan", "shared")
	secondSpec := integrationCreateSpec("second-plan", "shared")
	first, err := service.Create(ctx, user.ID, firstSpec, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(ctx, user.ID, secondSpec, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, user.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, user.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	waitForExecutorStart(t, executor.started, "shared")
	time.Sleep(100 * time.Millisecond)
	if executor.callCount("shared") != 1 {
		t.Fatalf("duplicate execution started %d times", executor.callCount("shared"))
	}
	close(release)
	waitForTaskStatus(t, service, user.ID, first.ID, compute.TaskStatusCompleted)
	waitForTaskStatus(t, service, user.ID, second.ID, compute.TaskStatusCompleted)
	if executor.callCount("shared") != 1 {
		t.Fatalf("shared cache executed %d times", executor.callCount("shared"))
	}
}

func TestComputeTaskCancellationPreservesResultsAndRetriesMissing(t *testing.T) {
	db := openComputeIntegrationDB(t)
	user := createComputeIntegrationUser(t, db, "cancel")
	executor := newIntegrationExecutor()
	release := make(chan struct{})
	executor.block["b"] = release
	service := newComputeIntegrationService(t, db, executor, 1)
	ctx := context.Background()
	task, err := service.Create(ctx, user.ID, integrationCreateSpec("cancel-plan", "a", "b", "c"), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartTask(ctx, user.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	waitForExecutorStart(t, executor.started, "a")
	waitForExecutorStart(t, executor.started, "b")
	cancelled, err := service.Cancel(ctx, user.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.ValidResultCount != 1 {
		t.Fatalf("completed result was not preserved at cancellation: %+v", cancelled)
	}
	cancelled = waitForTaskStatus(t, service, user.ID, task.ID, compute.TaskStatusCancelled)
	if cancelled.ValidResultCount != 1 || cancelled.CancelledCount != 2 || cancelled.Progress >= 1 {
		t.Fatalf("cancelled task counts = %+v", cancelled)
	}
	delete(executor.block, "b")
	close(release)
	if _, err := service.Retry(ctx, user.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, service, user.ID, task.ID, compute.TaskStatusCompleted)
	if executor.callCount("a") != 1 || executor.callCount("b") != 2 || executor.callCount("c") != 1 {
		t.Fatalf("cancel retry calls: a=%d b=%d c=%d", executor.callCount("a"), executor.callCount("b"), executor.callCount("c"))
	}
}

func TestComputeTaskRecoveryFinishesPersistedCancellation(t *testing.T) {
	db := openComputeIntegrationDB(t)
	user := createComputeIntegrationUser(t, db, "cancel-recovery")
	executor := newIntegrationExecutor()
	service := newComputeIntegrationService(t, db, executor, 1)
	ctx := context.Background()
	task, err := service.Create(ctx, user.ID, integrationCreateSpec("cancelled-before-restart", "a"), true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	future := now.Add(time.Hour)
	if err := db.Model(&saasstore.ComputeTask{}).Where("id = ?", task.ID).Updates(map[string]any{
		"status": compute.TaskStatusRunning, "started_at": now, "cancel_requested_at": now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&saasstore.ComputeTaskItem{}).Where("compute_task_id = ?", task.ID).Updates(map[string]any{
		"status": compute.ItemStatusRunning, "lease_owner": "stopped-worker", "lease_expires_at": future,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.recoverExpired(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.Get(ctx, user.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != compute.TaskStatusCancelled || recovered.CancelledCount != 1 || recovered.CancelledAt == "" {
		t.Fatalf("persisted cancellation did not converge after recovery: %+v", recovered)
	}
}

func TestCompositeStagesRequireExplicitStart(t *testing.T) {
	db := openComputeIntegrationDB(t)
	user := createComputeIntegrationUser(t, db, "composite")
	executor := newIntegrationExecutor()
	service := newComputeIntegrationService(t, db, executor, 2)
	ctx := context.Background()
	spec := CompositeSpec{
		TaskType: "research", Title: "研究任務", Settings: map[string]any{"schema": "v1"},
		Stages: []StageSpec{
			{Key: "search", Type: "search", Order: 1, Title: "搜尋", ExecutorType: executor.Descriptor().Type, Items: integrationItems("a")},
			{Key: "verify", Type: "verify", Order: 2, Title: "驗證", ExecutorType: executor.Descriptor().Type, DependsOnStageKeys: []string{"search"}, Items: integrationItems("b")},
		},
	}
	root, err := service.CreateComposite(ctx, user.ID, spec, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.ChildTaskIDs) != 2 || root.Status != compute.TaskStatusPlanned {
		t.Fatalf("composite root = %+v", root)
	}
	if _, err := service.StartTask(ctx, user.ID, root.ID); err != ErrInvalidState {
		t.Fatalf("composite root unexpectedly started every stage: %v", err)
	}
	if _, err := service.StartTask(ctx, user.ID, root.ChildTaskIDs[1]); err != ErrDependencyPending {
		t.Fatalf("dependent stage start error = %v", err)
	}
	if _, err := service.StartTask(ctx, user.ID, root.ChildTaskIDs[0]); err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, service, user.ID, root.ChildTaskIDs[0], compute.TaskStatusCompleted)
	rootAfterFirst := waitForTaskStatus(t, service, user.ID, root.ID, compute.TaskStatusPartial)
	if rootAfterFirst.Status != compute.TaskStatusPartial || executor.callCount("b") != 0 {
		t.Fatalf("future stage ran without confirmation: root=%+v b_calls=%d", rootAfterFirst, executor.callCount("b"))
	}
	if _, err := service.StartTask(ctx, user.ID, root.ChildTaskIDs[1]); err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, service, user.ID, root.ChildTaskIDs[1], compute.TaskStatusCompleted)
	waitForTaskStatus(t, service, user.ID, root.ID, compute.TaskStatusCompleted)
}

func TestComputeTaskRestartRecoversExpiredLeaseFromFixedManifest(t *testing.T) {
	db := openComputeIntegrationDB(t)
	user := createComputeIntegrationUser(t, db, "restart")
	executor := newIntegrationExecutor()
	registry := NewRegistry()
	if err := registry.Register(executor); err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions()
	options.Workers = 1
	options.PollInterval = 20 * time.Millisecond
	options.LeaseDuration = 300 * time.Millisecond
	service, err := NewService(db, registry, options, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	task, err := service.Create(ctx, user.ID, integrationCreateSpec("restart-plan", "resume"), true)
	if err != nil {
		t.Fatal(err)
	}
	var item saasstore.ComputeTaskItem
	if err := db.Where("compute_task_id = ?", task.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	expired := time.Now().UTC().Add(-time.Second)
	if err := db.Model(&item).Updates(map[string]any{
		"status": compute.ItemStatusRunning, "progress": 0.4, "attempt": 1,
		"checkpoint": saasstore.JSONB(`{"step":1}`), "checkpoint_hash": compute.HashBytes([]byte(`{"step":1}`)),
		"rng_position": 1, "lease_owner": "crashed-worker", "lease_expires_at": expired,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&saasstore.ComputeTask{}).Where("id = ?", task.ID).Updates(map[string]any{
		"status": compute.TaskStatusRunning, "started_at": expired,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = service.Shutdown(shutdownCtx)
	})
	completed := waitForTaskStatus(t, service, user.ID, task.ID, compute.TaskStatusCompleted)
	if completed.ManifestHash != task.ManifestHash || completed.PlanKey != task.PlanKey || !executor.resumed("resume") {
		t.Fatalf("restart changed manifest or lost checkpoint: before=%+v after=%+v resumed=%v", task, completed, executor.resumed("resume"))
	}
}

func integrationCreateSpec(taskType string, values ...string) CreateSpec {
	return CreateSpec{TaskType: taskType, Title: taskType, ExecutorType: "p05.integration", Settings: map[string]any{"schema": "v1"}, Items: integrationItems(values...)}
}

func integrationItems(values ...string) []compute.ManifestItemInput {
	items := make([]compute.ManifestItemInput, 0, len(values))
	for _, value := range values {
		items = append(items, compute.ManifestItemInput{Key: value, CacheKey: "value:" + value, Input: json.RawMessage(fmt.Sprintf(`{"value":%q}`, value)), EstimatedUnits: 1})
	}
	return items
}

func newComputeIntegrationService(t *testing.T, db *gorm.DB, executor *integrationExecutor, workers int) *Service {
	t.Helper()
	registry := NewRegistry()
	if err := registry.Register(executor); err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions()
	options.Workers = workers
	options.PollInterval = 20 * time.Millisecond
	options.LeaseDuration = 500 * time.Millisecond
	service, err := NewService(db, registry, options, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = service.Shutdown(shutdownCtx)
	})
	return service
}

func waitForExecutorStart(t *testing.T, started <-chan string, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case got := <-started:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("executor did not start %s", want)
		}
	}
}

func waitForTaskStatus(t *testing.T, service *Service, userID uint, taskID uint, want string) *TaskDescriptor {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		task, err := service.Get(context.Background(), userID, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == want {
			return task
		}
		time.Sleep(25 * time.Millisecond)
	}
	task, _ := service.Get(context.Background(), userID, taskID)
	t.Fatalf("task %d status = %+v, want %s", taskID, task, want)
	return nil
}

func createComputeIntegrationUser(t *testing.T, db *gorm.DB, suffix string) saasstore.User {
	t.Helper()
	user := saasstore.User{Email: fmt.Sprintf("p05-%s@example.test", suffix), PasswordHash: "test-only", Role: "user", Plan: "free", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func openComputeIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("p05_compute_%d", time.Now().UnixNano())
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
	if err := db.AutoMigrate(
		&saasstore.User{},
		&saasstore.ComputeTask{},
		&saasstore.ComputeCacheEntry{},
		&saasstore.ComputeTaskItem{},
		&saasstore.ComputeTaskDependency{},
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
