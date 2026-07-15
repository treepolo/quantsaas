package compute

import (
	"encoding/json"
	"testing"
)

func TestBuildCompositePlanIsStableAndFinite(t *testing.T) {
	descriptor := ExecutorDescriptor{Type: "test", Version: "v1", ResultSchemaVersion: "r1"}
	spec := CompositePlanSpec{
		TaskType: "research", Settings: map[string]any{"z": 1, "a": 2}, ResearchSettingHash: "research-v1",
		Stages: []CompositeStagePlanSpec{
			{Key: "verify", Type: "verification", Order: 20, Executor: descriptor, DependsOnStageKeys: []string{"search"}, Items: []ManifestItemInput{{Key: "b", CacheKey: "b", Input: json.RawMessage(`{"x":2}`)}}},
			{Key: "search", Type: "search", Order: 10, Executor: descriptor, Items: []ManifestItemInput{{Key: "a", CacheKey: "a", Input: json.RawMessage(`{"x":1}`)}}},
		},
	}
	first, err := BuildCompositePlan(spec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildCompositePlan(spec)
	if err != nil {
		t.Fatal(err)
	}
	if first.PlanKey != second.PlanKey || first.Snapshot.ManifestHash != second.Snapshot.ManifestHash {
		t.Fatal("composite identity is not stable")
	}
	if len(first.Manifest.Stages) != 2 || first.Manifest.Stages[0].Key != "search" || first.Manifest.Stages[1].DependsOnStageKeys[0] != "search" {
		t.Fatalf("unexpected stage manifest: %+v", first.Manifest.Stages)
	}
}

func TestBuildCompositePlanRejectsForwardDependency(t *testing.T) {
	descriptor := ExecutorDescriptor{Type: "test", Version: "v1", ResultSchemaVersion: "r1"}
	_, err := BuildCompositePlan(CompositePlanSpec{TaskType: "research", Stages: []CompositeStagePlanSpec{
		{Key: "first", Type: "first", Order: 1, Executor: descriptor, DependsOnStageKeys: []string{"second"}, Items: []ManifestItemInput{{Key: "a", CacheKey: "a", Input: json.RawMessage(`{}`)}}},
		{Key: "second", Type: "second", Order: 2, Executor: descriptor, Items: []ManifestItemInput{{Key: "b", CacheKey: "b", Input: json.RawMessage(`{}`)}}},
	}})
	if err == nil {
		t.Fatal("expected forward dependency error")
	}
}
