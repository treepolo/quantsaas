package compute

import (
	"fmt"
	"sort"
	"strings"
)

// BuildCompositePlan validates a finite stage DAG and derives the immutable
// parent identity without creating a circular dependency between the parent
// key and its child stage keys. Each stage identity is first derived without a
// parent; the service then derives the runnable child key with the parent key.
func BuildCompositePlan(spec CompositePlanSpec) (CompositePlan, error) {
	taskType := strings.TrimSpace(spec.TaskType)
	if taskType == "" {
		return CompositePlan{}, fmt.Errorf("composite task type is required")
	}
	if len(spec.Stages) == 0 {
		return CompositePlan{}, fmt.Errorf("composite manifest requires at least one stage")
	}
	settings := spec.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	settingsJSON, err := CanonicalJSON(settings)
	if err != nil {
		return CompositePlan{}, fmt.Errorf("canonicalize composite settings: %w", err)
	}

	stages := append([]CompositeStagePlanSpec(nil), spec.Stages...)
	for index := range stages {
		stages[index].DependsOnStageKeys = append([]string(nil), stages[index].DependsOnStageKeys...)
		stages[index].Items = append([]ManifestItemInput(nil), stages[index].Items...)
	}
	sort.SliceStable(stages, func(i, j int) bool {
		if stages[i].Order == stages[j].Order {
			return strings.TrimSpace(stages[i].Key) < strings.TrimSpace(stages[j].Key)
		}
		return stages[i].Order < stages[j].Order
	})
	known := make(map[string]int, len(stages))
	orders := make(map[int]string, len(stages))
	for index := range stages {
		stages[index].Key = strings.TrimSpace(stages[index].Key)
		stages[index].Type = strings.TrimSpace(stages[index].Type)
		if stages[index].Key == "" || stages[index].Type == "" || stages[index].Order < 0 {
			return CompositePlan{}, fmt.Errorf("composite stage %d has invalid key, type or order", index)
		}
		if _, exists := known[stages[index].Key]; exists {
			return CompositePlan{}, fmt.Errorf("duplicate composite stage key %q", stages[index].Key)
		}
		if previous, exists := orders[stages[index].Order]; exists {
			return CompositePlan{}, fmt.Errorf("composite stages %q and %q share order %d", previous, stages[index].Key, stages[index].Order)
		}
		known[stages[index].Key] = index
		orders[stages[index].Order] = stages[index].Key
	}
	for index := range stages {
		seenDependency := make(map[string]struct{}, len(stages[index].DependsOnStageKeys))
		for dependencyIndex, rawDependency := range stages[index].DependsOnStageKeys {
			dependency := strings.TrimSpace(rawDependency)
			if dependency == "" || dependency == stages[index].Key {
				return CompositePlan{}, fmt.Errorf("invalid dependency for stage %q", stages[index].Key)
			}
			dependencyPosition, exists := known[dependency]
			if !exists {
				return CompositePlan{}, fmt.Errorf("unknown dependency %q for stage %q", dependency, stages[index].Key)
			}
			if stages[dependencyPosition].Order >= stages[index].Order {
				return CompositePlan{}, fmt.Errorf("stage %q must depend only on an earlier stage", stages[index].Key)
			}
			if _, duplicate := seenDependency[dependency]; duplicate {
				return CompositePlan{}, fmt.Errorf("duplicate dependency %q for stage %q", dependency, stages[index].Key)
			}
			seenDependency[dependency] = struct{}{}
			stages[index].DependsOnStageKeys[dependencyIndex] = dependency
		}
		sort.Strings(stages[index].DependsOnStageKeys)
	}

	manifest := CompositeManifest{SchemaVersion: ManifestSchemaVersion, Stages: make([]CompositeStageSnapshot, 0, len(stages))}
	stagePlans := make([]Plan, 0, len(stages))
	for _, stage := range stages {
		plan, err := BuildPlan(PlanSpec{
			TaskType: taskType + ":" + stage.Type, Executor: stage.Executor, Settings: stage.Settings,
			ResearchSettingHash: strings.TrimSpace(spec.ResearchSettingHash), StageKey: stage.Key,
			StageType: stage.Type, StageOrder: stage.Order, RNG: stage.RNG, Items: stage.Items,
		})
		if err != nil {
			return CompositePlan{}, fmt.Errorf("build composite stage %s: %w", stage.Key, err)
		}
		stagePlans = append(stagePlans, plan)
		manifest.Stages = append(manifest.Stages, CompositeStageSnapshot{
			Key: stage.Key, Type: stage.Type, Order: stage.Order, Executor: plan.Snapshot.Executor,
			SettingsHash: plan.Snapshot.SettingsHash, ManifestHash: plan.Snapshot.ManifestHash,
			StageIdentityKey: plan.PlanKey, DependsOnStageKeys: append([]string(nil), stage.DependsOnStageKeys...),
			TotalItems: plan.Manifest.TotalItems, EstimatedUnits: plan.Manifest.EstimatedUnits,
			UnknownUnitItems: plan.Manifest.UnknownUnitItems, RNG: stage.RNG,
		})
	}
	manifestJSON, err := CanonicalJSON(manifest)
	if err != nil {
		return CompositePlan{}, err
	}
	snapshot := CompositePlanSnapshot{
		SchemaVersion: TaskSchemaVersion, LifecycleVersion: LifecycleVersion, TaskType: taskType,
		SettingsHash: HashBytes(settingsJSON), ResearchSettingHash: strings.TrimSpace(spec.ResearchSettingHash),
		ManifestHash: HashBytes(manifestJSON),
	}
	snapshotJSON, err := CanonicalJSON(snapshot)
	if err != nil {
		return CompositePlan{}, err
	}
	return CompositePlan{
		Snapshot: snapshot, SnapshotJSON: snapshotJSON,
		PlanKey: "compute-composite:v1:" + hashHex(snapshotJSON), SettingsJSON: settingsJSON,
		Manifest: manifest, ManifestJSON: manifestJSON, StagePlans: stagePlans,
	}, nil
}
