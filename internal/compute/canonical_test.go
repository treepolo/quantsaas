package compute

import (
	"encoding/json"
	"testing"
)

func TestBuildPlanIsCanonicalAndVersioned(t *testing.T) {
	descriptor := ExecutorDescriptor{Type: "test.executor", Version: "v1", ResultSchemaVersion: "result-v1"}
	left, err := BuildPlan(PlanSpec{
		TaskType: "scan", Executor: descriptor,
		Settings: map[string]any{"beta": 2, "alpha": 1},
		Items:    []ManifestItemInput{{Key: "point-1", CacheKey: "point:1", Input: json.RawMessage(`{"y":2,"x":1}`), EstimatedUnits: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := BuildPlan(PlanSpec{
		TaskType: "scan", Executor: descriptor,
		Settings: map[string]any{"alpha": 1.0, "beta": 2.0},
		Items:    []ManifestItemInput{{Key: "point-1", CacheKey: "point:1", Input: json.RawMessage(`{"x":1.0,"y":2.0}`), EstimatedUnits: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if left.PlanKey != right.PlanKey || left.Manifest.Items[0].ResolvedCacheKey != right.Manifest.Items[0].ResolvedCacheKey {
		t.Fatalf("canonical keys differ: %s / %s", left.PlanKey, right.PlanKey)
	}
	if left.Snapshot.SchemaVersion != TaskSchemaVersion || left.Manifest.SchemaVersion != ManifestSchemaVersion {
		t.Fatalf("unexpected versions: %+v %+v", left.Snapshot, left.Manifest)
	}
	if left.Manifest.TotalItems != 1 || left.Manifest.EstimatedUnits != 10 || left.Manifest.UnknownUnitItems != 0 {
		t.Fatalf("unexpected manifest totals: %+v", left.Manifest)
	}
}

func TestBuildPlanInvalidatesOnExecutorSettingsOrInputVersion(t *testing.T) {
	base := PlanSpec{
		TaskType: "scan",
		Executor: ExecutorDescriptor{Type: "test.executor", Version: "v1", ResultSchemaVersion: "result-v1"},
		Settings: map[string]any{"radius": 1},
		Items:    []ManifestItemInput{{Key: "point", CacheKey: "point", Input: json.RawMessage(`{"value":1}`)}},
	}
	first, err := BuildPlan(base)
	if err != nil {
		t.Fatal(err)
	}
	changedExecutor := base
	changedExecutor.Executor.Version = "v2"
	second, err := BuildPlan(changedExecutor)
	if err != nil {
		t.Fatal(err)
	}
	changedSettings := base
	changedSettings.Settings = map[string]any{"radius": 2}
	third, err := BuildPlan(changedSettings)
	if err != nil {
		t.Fatal(err)
	}
	changedInput := base
	changedInput.Items = []ManifestItemInput{{Key: "point", CacheKey: "point", Input: json.RawMessage(`{"value":2}`)}}
	fourth, err := BuildPlan(changedInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.PlanKey == second.PlanKey || first.PlanKey == third.PlanKey || first.PlanKey == fourth.PlanKey {
		t.Fatal("version, settings or input change did not invalidate task plan")
	}
	if first.Manifest.Items[0].ResolvedCacheKey == second.Manifest.Items[0].ResolvedCacheKey ||
		first.Manifest.Items[0].ResolvedCacheKey == fourth.Manifest.Items[0].ResolvedCacheKey {
		t.Fatal("executor or input change did not invalidate cache key")
	}
}

func TestBuildPlanRejectsUnboundedOrDuplicateManifest(t *testing.T) {
	descriptor := ExecutorDescriptor{Type: "test.executor", Version: "v1", ResultSchemaVersion: "result-v1"}
	if _, err := BuildPlan(PlanSpec{TaskType: "scan", Executor: descriptor}); err == nil {
		t.Fatal("empty manifest was accepted")
	}
	_, err := BuildPlan(PlanSpec{TaskType: "scan", Executor: descriptor, Items: []ManifestItemInput{
		{Key: "duplicate", CacheKey: "a", Input: json.RawMessage(`{}`)},
		{Key: "duplicate", CacheKey: "b", Input: json.RawMessage(`{}`)},
	}})
	if err == nil {
		t.Fatal("duplicate item key was accepted")
	}
	_, err = BuildPlan(PlanSpec{TaskType: "scan", Executor: descriptor, Items: []ManifestItemInput{
		{Key: "one", CacheKey: "same", Input: json.RawMessage(`{}`)},
		{Key: "two", CacheKey: "same", Input: json.RawMessage(`{}`)},
	}})
	if err == nil {
		t.Fatal("duplicate cache identity was accepted")
	}
}
