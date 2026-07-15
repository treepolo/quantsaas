package compute

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

type cacheKeySnapshot struct {
	SchemaVersion string             `json:"schema_version"`
	Executor      ExecutorDescriptor `json:"executor"`
	BaseCacheKey  string             `json:"base_cache_key"`
	InputHash     string             `json:"input_hash"`
}

func BuildPlan(spec PlanSpec) (Plan, error) {
	taskType := strings.TrimSpace(spec.TaskType)
	if taskType == "" {
		return Plan{}, fmt.Errorf("task type is required")
	}
	executor, err := normalizeExecutor(spec.Executor)
	if err != nil {
		return Plan{}, err
	}
	if len(spec.Items) == 0 {
		return Plan{}, fmt.Errorf("manifest requires at least one item")
	}
	if spec.StageOrder < 0 {
		return Plan{}, fmt.Errorf("stage order cannot be negative")
	}
	stageKey := strings.TrimSpace(spec.StageKey)
	if stageKey == "" {
		stageKey = "main"
	}
	stageType := strings.TrimSpace(spec.StageType)
	if stageType == "" {
		stageType = taskType
	}
	if (spec.RNG.Algorithm == "") != (spec.RNG.Version == "") {
		return Plan{}, fmt.Errorf("RNG algorithm and version must be provided together")
	}

	settings := spec.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	settingsJSON, err := CanonicalJSON(settings)
	if err != nil {
		return Plan{}, fmt.Errorf("canonicalize task settings: %w", err)
	}

	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Executor:      executor,
		TotalItems:    len(spec.Items),
		Items:         make([]ManifestItem, 0, len(spec.Items)),
	}
	seenKeys := make(map[string]struct{}, len(spec.Items))
	seenCacheKeys := make(map[string]struct{}, len(spec.Items))
	for index, item := range spec.Items {
		key := strings.TrimSpace(item.Key)
		baseCacheKey := strings.TrimSpace(item.CacheKey)
		if key == "" || baseCacheKey == "" {
			return Plan{}, fmt.Errorf("manifest item %d requires key and cache key", index)
		}
		if item.EstimatedUnits < 0 {
			return Plan{}, fmt.Errorf("manifest item %s has negative estimated units", key)
		}
		if _, exists := seenKeys[key]; exists {
			return Plan{}, fmt.Errorf("duplicate manifest item key %q", key)
		}
		seenKeys[key] = struct{}{}

		input := item.Input
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		canonicalInput, err := CanonicalRawJSON(input)
		if err != nil {
			return Plan{}, fmt.Errorf("canonicalize manifest item %s: %w", key, err)
		}
		inputHash := HashBytes(canonicalInput)
		cacheSnapshotJSON, err := CanonicalJSON(cacheKeySnapshot{
			SchemaVersion: CacheKeySchemaVersion,
			Executor:      executor,
			BaseCacheKey:  baseCacheKey,
			InputHash:     inputHash,
		})
		if err != nil {
			return Plan{}, err
		}
		resolvedCacheKey := "compute-cache:v1:" + hashHex(cacheSnapshotJSON)
		if _, exists := seenCacheKeys[resolvedCacheKey]; exists {
			return Plan{}, fmt.Errorf("duplicate resolved cache key for manifest item %q", key)
		}
		seenCacheKeys[resolvedCacheKey] = struct{}{}
		if item.EstimatedUnits == 0 {
			manifest.UnknownUnitItems++
		} else if manifest.EstimatedUnits > math.MaxInt64-item.EstimatedUnits {
			return Plan{}, fmt.Errorf("manifest estimated units overflow")
		} else {
			manifest.EstimatedUnits += item.EstimatedUnits
		}
		manifest.Items = append(manifest.Items, ManifestItem{
			Index: index, Key: key, BaseCacheKey: baseCacheKey,
			ResolvedCacheKey: resolvedCacheKey, InputHash: inputHash,
			Input: json.RawMessage(canonicalInput), EstimatedUnits: item.EstimatedUnits,
		})
	}
	manifestJSON, err := CanonicalJSON(manifest)
	if err != nil {
		return Plan{}, fmt.Errorf("canonicalize manifest: %w", err)
	}
	snapshot := PlanSnapshot{
		SchemaVersion: TaskSchemaVersion, LifecycleVersion: LifecycleVersion,
		TaskType: taskType, Executor: executor, SettingsHash: HashBytes(settingsJSON),
		ResearchSettingHash: strings.TrimSpace(spec.ResearchSettingHash),
		ParentPlanKey:       strings.TrimSpace(spec.ParentPlanKey), StageKey: stageKey,
		StageType: stageType, StageOrder: spec.StageOrder,
		ManifestHash: HashBytes(manifestJSON), RNG: spec.RNG,
	}
	snapshotJSON, err := CanonicalJSON(snapshot)
	if err != nil {
		return Plan{}, fmt.Errorf("canonicalize task plan: %w", err)
	}
	return Plan{
		Snapshot: snapshot, SnapshotJSON: snapshotJSON,
		PlanKey:      "compute-task:v1:" + hashHex(snapshotJSON),
		SettingsJSON: settingsJSON, Manifest: manifest, ManifestJSON: manifestJSON,
	}, nil
}

func normalizeExecutor(descriptor ExecutorDescriptor) (ExecutorDescriptor, error) {
	descriptor.Type = strings.TrimSpace(descriptor.Type)
	descriptor.Version = strings.TrimSpace(descriptor.Version)
	descriptor.ResultSchemaVersion = strings.TrimSpace(descriptor.ResultSchemaVersion)
	if descriptor.Type == "" || descriptor.Version == "" || descriptor.ResultSchemaVersion == "" {
		return ExecutorDescriptor{}, fmt.Errorf("executor type, version and result schema version are required")
	}
	return descriptor, nil
}

func CanonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return CanonicalRawJSON(raw)
}

func CanonicalRawJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values are not allowed")
		}
		return nil, err
	}
	normalized, err := normalizeNumbers(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func normalizeNumbers(value any) (any, error) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			normalized, err := normalizeNumbers(child)
			if err != nil {
				return nil, err
			}
			item[key] = normalized
		}
		return item, nil
	case []any:
		for index, child := range item {
			normalized, err := normalizeNumbers(child)
			if err != nil {
				return nil, err
			}
			item[index] = normalized
		}
		return item, nil
	case json.Number:
		if integer, err := strconv.ParseInt(item.String(), 10, 64); err == nil {
			return json.Number(strconv.FormatInt(integer, 10)), nil
		}
		number, err := strconv.ParseFloat(item.String(), 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("invalid JSON number %q", item.String())
		}
		if number == 0 {
			return json.Number("0"), nil
		}
		return json.Number(strconv.FormatFloat(number, 'g', -1, 64)), nil
	default:
		return value, nil
	}
}

func HashBytes(raw []byte) string {
	return "sha256:" + hashHex(raw)
}

func hashHex(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
