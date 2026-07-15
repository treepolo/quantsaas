package compute

import "encoding/json"

const (
	TaskSchemaVersion       = "p05-task-v1"
	ManifestSchemaVersion   = "p05-manifest-v1"
	CacheKeySchemaVersion   = "p05-cache-key-v1"
	CacheEntrySchemaVersion = "p05-cache-entry-v1"
	LifecycleVersion        = "p05-lifecycle-v1"
	LayeredKeySchemaVersion = "p05-layered-key-v1"
)

const (
	TaskKindAtomic    = "atomic"
	TaskKindComposite = "composite"
	TaskKindStage     = "stage"
)

const (
	TaskStatusPlanned     = "planned"
	TaskStatusQueued      = "queued"
	TaskStatusRunning     = "running"
	TaskStatusPartial     = "partial"
	TaskStatusCompleted   = "completed"
	TaskStatusFailed      = "failed"
	TaskStatusCancelled   = "cancelled"
	TaskStatusInvalidated = "invalidated"
)

const (
	ItemStatusPending   = "pending"
	ItemStatusRunning   = "running"
	ItemStatusCompleted = "completed"
	ItemStatusCached    = "cached"
	ItemStatusFailed    = "failed"
	ItemStatusCancelled = "cancelled"
)

const (
	CacheStatusRunning     = "running"
	CacheStatusCompleted   = "completed"
	CacheStatusFailed      = "failed"
	CacheStatusInvalidated = "invalidated"
	CacheStatusArchived    = "archived"
)

type ExecutorDescriptor struct {
	Type                string `json:"type"`
	Version             string `json:"version"`
	ResultSchemaVersion string `json:"result_schema_version"`
}

type RNGSpec struct {
	Algorithm string `json:"algorithm,omitempty"`
	Version   string `json:"version,omitempty"`
	RootSeed  *int64 `json:"root_seed,omitempty"`
}

type ManifestItemInput struct {
	Key            string          `json:"key"`
	CacheKey       string          `json:"cache_key"`
	Input          json.RawMessage `json:"input"`
	EstimatedUnits int64           `json:"estimated_units,omitempty"`
}

type ManifestItem struct {
	Index            int             `json:"index"`
	Key              string          `json:"key"`
	BaseCacheKey     string          `json:"base_cache_key"`
	ResolvedCacheKey string          `json:"resolved_cache_key"`
	InputHash        string          `json:"input_hash"`
	Input            json.RawMessage `json:"input"`
	EstimatedUnits   int64           `json:"estimated_units,omitempty"`
}

type Manifest struct {
	SchemaVersion    string             `json:"schema_version"`
	Executor         ExecutorDescriptor `json:"executor"`
	TotalItems       int                `json:"total_items"`
	EstimatedUnits   int64              `json:"estimated_units"`
	UnknownUnitItems int                `json:"unknown_unit_items"`
	Items            []ManifestItem     `json:"items"`
}

type PlanSpec struct {
	TaskType            string
	Executor            ExecutorDescriptor
	Settings            any
	ResearchSettingHash string
	ParentPlanKey       string
	StageKey            string
	StageType           string
	StageOrder          int
	RNG                 RNGSpec
	Items               []ManifestItemInput
}

type PlanSnapshot struct {
	SchemaVersion       string             `json:"schema_version"`
	LifecycleVersion    string             `json:"lifecycle_version"`
	TaskType            string             `json:"task_type"`
	Executor            ExecutorDescriptor `json:"executor"`
	SettingsHash        string             `json:"settings_hash"`
	ResearchSettingHash string             `json:"research_setting_hash,omitempty"`
	ParentPlanKey       string             `json:"parent_plan_key,omitempty"`
	StageKey            string             `json:"stage_key"`
	StageType           string             `json:"stage_type"`
	StageOrder          int                `json:"stage_order"`
	ManifestHash        string             `json:"manifest_hash"`
	RNG                 RNGSpec            `json:"rng,omitempty"`
}

type Plan struct {
	Snapshot     PlanSnapshot
	SnapshotJSON []byte
	PlanKey      string
	SettingsJSON []byte
	Manifest     Manifest
	ManifestJSON []byte
}

type ItemCounts struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Cached    int `json:"cached"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
}

type CompositeStagePlanSpec struct {
	Key                string
	Type               string
	Order              int
	Executor           ExecutorDescriptor
	Settings           any
	DependsOnStageKeys []string
	RNG                RNGSpec
	Items              []ManifestItemInput
}

type CompositePlanSpec struct {
	TaskType            string
	Settings            any
	ResearchSettingHash string
	Stages              []CompositeStagePlanSpec
}

type CompositeStageSnapshot struct {
	Key                string             `json:"key"`
	Type               string             `json:"type"`
	Order              int                `json:"order"`
	Executor           ExecutorDescriptor `json:"executor"`
	SettingsHash       string             `json:"settings_hash"`
	ManifestHash       string             `json:"manifest_hash"`
	StageIdentityKey   string             `json:"stage_identity_key"`
	DependsOnStageKeys []string           `json:"depends_on_stage_keys"`
	TotalItems         int                `json:"total_items"`
	EstimatedUnits     int64              `json:"estimated_units"`
	UnknownUnitItems   int                `json:"unknown_unit_items"`
	RNG                RNGSpec            `json:"rng,omitempty"`
}

type CompositeManifest struct {
	SchemaVersion string                   `json:"schema_version"`
	Stages        []CompositeStageSnapshot `json:"stages"`
}

type CompositePlanSnapshot struct {
	SchemaVersion       string `json:"schema_version"`
	LifecycleVersion    string `json:"lifecycle_version"`
	TaskType            string `json:"task_type"`
	SettingsHash        string `json:"settings_hash"`
	ResearchSettingHash string `json:"research_setting_hash,omitempty"`
	ManifestHash        string `json:"manifest_hash"`
}

type CompositePlan struct {
	Snapshot     CompositePlanSnapshot
	SnapshotJSON []byte
	PlanKey      string
	SettingsJSON []byte
	Manifest     CompositeManifest
	ManifestJSON []byte
	StagePlans   []Plan
}

func (c ItemCounts) Valid() int {
	return c.Completed + c.Cached
}

func (c ItemCounts) Missing() int {
	missing := c.Total - c.Valid() - c.Failed
	if missing < 0 {
		return 0
	}
	return missing
}
