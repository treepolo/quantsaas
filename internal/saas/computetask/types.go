package computetask

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	compute "quantsaas/internal/compute"
)

var (
	ErrNotFound           = errors.New("找不到計算任務")
	ErrAccessNotFound     = errors.New("找不到計算任務")
	ErrUnknownExecutor    = errors.New("找不到相容的計算執行器")
	ErrInvalidState       = errors.New("計算任務狀態不允許此操作")
	ErrDependencyPending  = errors.New("前置階段尚未完成")
	ErrVersionMismatch    = errors.New("計算任務版本已不相容")
	ErrSoftLimitConfirm   = errors.New("計算量超過建議上限，需要明確確認")
	ErrHardLimitExceeded  = errors.New("計算量超過系統硬上限")
	ErrServiceUnavailable = errors.New("計算任務服務尚未啟動")
)

type Options struct {
	Workers       int
	SoftItemLimit int
	HardItemLimit int
	LeaseDuration time.Duration
	PollInterval  time.Duration
}

func DefaultOptions() Options {
	return Options{
		Workers:       4,
		SoftItemLimit: 1000,
		HardItemLimit: 300000,
		LeaseDuration: 60 * time.Second,
		PollInterval:  250 * time.Millisecond,
	}
}

func (o Options) validate() error {
	if o.Workers < 1 || o.Workers > 64 {
		return fmt.Errorf("workers must be between 1 and 64")
	}
	if o.SoftItemLimit < 1 || o.HardItemLimit < o.SoftItemLimit {
		return fmt.Errorf("invalid compute task item limits")
	}
	if o.LeaseDuration <= 0 || o.PollInterval <= 0 {
		return fmt.Errorf("lease duration and poll interval must be positive")
	}
	return nil
}

type CreateSpec struct {
	Kind                string
	TaskType            string
	Title               string
	ExecutorType        string
	Settings            any
	ResearchSettingID   string
	ResearchSettingHash string
	ParentTaskID        *uint
	StageKey            string
	StageType           string
	StageOrder          int
	DependsOnTaskIDs    []uint
	RNG                 compute.RNGSpec
	Items               []compute.ManifestItemInput
}

type StageSpec struct {
	Key                string
	Type               string
	Order              int
	Title              string
	ExecutorType       string
	Settings           any
	DependsOnStageKeys []string
	RNG                compute.RNGSpec
	Items              []compute.ManifestItemInput
}

type CompositeSpec struct {
	TaskType            string
	Title               string
	Settings            any
	ResearchSettingID   string
	ResearchSettingHash string
	Stages              []StageSpec
}

type PlanPreview struct {
	PlanKey              string                     `json:"plan_key"`
	StageKey             string                     `json:"stage_key,omitempty"`
	StageType            string                     `json:"stage_type,omitempty"`
	StageOrder           int                        `json:"stage_order"`
	TaskSchemaVersion    string                     `json:"task_schema_version"`
	LifecycleVersion     string                     `json:"lifecycle_version"`
	Executor             compute.ExecutorDescriptor `json:"executor"`
	ManifestVersion      string                     `json:"manifest_version"`
	ManifestHash         string                     `json:"manifest_hash"`
	TotalItems           int                        `json:"total_items"`
	EstimatedUnits       int64                      `json:"estimated_units"`
	UnknownUnitItems     int                        `json:"unknown_unit_items"`
	CacheHitCount        int                        `json:"cache_hit_count"`
	NewItemCount         int                        `json:"new_item_count"`
	SoftItemLimit        int                        `json:"soft_item_limit"`
	HardItemLimit        int                        `json:"hard_item_limit"`
	RequiresConfirmation bool                       `json:"requires_confirmation"`
	EstimatedSeconds     *float64                   `json:"estimated_seconds,omitempty"`
}

type CompositePlanPreview struct {
	PlanKey              string        `json:"plan_key"`
	TaskSchemaVersion    string        `json:"task_schema_version"`
	LifecycleVersion     string        `json:"lifecycle_version"`
	ManifestVersion      string        `json:"manifest_version"`
	ManifestHash         string        `json:"manifest_hash"`
	Stages               []PlanPreview `json:"stages"`
	TotalItems           int           `json:"total_items"`
	EstimatedUnits       int64         `json:"estimated_units"`
	UnknownUnitItems     int           `json:"unknown_unit_items"`
	CacheHitCount        int           `json:"cache_hit_count"`
	NewItemCount         int           `json:"new_item_count"`
	SoftItemLimit        int           `json:"soft_item_limit"`
	HardItemLimit        int           `json:"hard_item_limit"`
	RequiresConfirmation bool          `json:"requires_confirmation"`
}

type LimitError struct {
	Cause   error
	Preview PlanPreview
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("%v：%d 個項目、%d 估算工作單位（建議上限 %d，硬上限 %d）", e.Cause, e.Preview.TotalItems, e.Preview.EstimatedUnits, e.Preview.SoftItemLimit, e.Preview.HardItemLimit)
}

func (e *LimitError) Unwrap() error { return e.Cause }

type CompositeLimitError struct {
	Cause   error
	Preview CompositePlanPreview
}

func (e *CompositeLimitError) Error() string {
	return fmt.Sprintf("%v：%d 個項目、%d 估算工作單位（建議上限 %d，硬上限 %d）", e.Cause, e.Preview.TotalItems, e.Preview.EstimatedUnits, e.Preview.SoftItemLimit, e.Preview.HardItemLimit)
}

func (e *CompositeLimitError) Unwrap() error { return e.Cause }

type TaskDescriptor struct {
	ID                  uint                       `json:"id"`
	UserID              uint                       `json:"user_id"`
	ParentTaskID        *uint                      `json:"parent_task_id,omitempty"`
	Kind                string                     `json:"kind"`
	TaskType            string                     `json:"task_type"`
	Title               string                     `json:"title"`
	PlanKey             string                     `json:"plan_key"`
	TaskSchemaVersion   string                     `json:"task_schema_version"`
	LifecycleVersion    string                     `json:"lifecycle_version"`
	Executor            compute.ExecutorDescriptor `json:"executor"`
	SettingsHash        string                     `json:"settings_hash"`
	Settings            json.RawMessage            `json:"settings,omitempty"`
	ResearchSettingID   string                     `json:"research_setting_id,omitempty"`
	ResearchSettingHash string                     `json:"research_setting_hash,omitempty"`
	StageKey            string                     `json:"stage_key,omitempty"`
	StageType           string                     `json:"stage_type,omitempty"`
	StageOrder          int                        `json:"stage_order"`
	ManifestVersion     string                     `json:"manifest_version,omitempty"`
	ManifestHash        string                     `json:"manifest_hash,omitempty"`
	Manifest            json.RawMessage            `json:"manifest,omitempty"`
	TotalItems          int                        `json:"total_items"`
	EstimatedUnits      int64                      `json:"estimated_units"`
	UnknownUnitItems    int                        `json:"unknown_unit_items"`
	CacheHitCount       int                        `json:"cache_hit_count"`
	NewItemCount        int                        `json:"new_item_count"`
	ValidResultCount    int                        `json:"valid_result_count"`
	FailedCount         int                        `json:"failed_count"`
	MissingCount        int                        `json:"missing_count"`
	CancelledCount      int                        `json:"cancelled_count"`
	Progress            float64                    `json:"progress"`
	Status              string                     `json:"status"`
	Error               string                     `json:"error,omitempty"`
	Attempt             int                        `json:"attempt"`
	RNGAlgorithm        string                     `json:"rng_algorithm,omitempty"`
	RNGVersion          string                     `json:"rng_version,omitempty"`
	RootSeed            *int64                     `json:"root_seed,omitempty"`
	RNGPosition         int64                      `json:"rng_position"`
	CheckpointHash      string                     `json:"checkpoint_hash,omitempty"`
	DependencyTaskIDs   []uint                     `json:"dependency_task_ids"`
	ChildTaskIDs        []uint                     `json:"child_task_ids"`
	CreatedAt           string                     `json:"created_at"`
	UpdatedAt           string                     `json:"updated_at"`
	StartedAt           string                     `json:"started_at,omitempty"`
	CompletedAt         string                     `json:"completed_at,omitempty"`
	CancelledAt         string                     `json:"cancelled_at,omitempty"`
	CancelRequestedAt   string                     `json:"cancel_requested_at,omitempty"`
	Reused              bool                       `json:"reused"`
}

type ItemDescriptor struct {
	ID             uint            `json:"id"`
	TaskID         uint            `json:"task_id"`
	Index          int             `json:"index"`
	Key            string          `json:"key"`
	CacheKey       string          `json:"cache_key"`
	InputHash      string          `json:"input_hash"`
	EstimatedUnits int64           `json:"estimated_units"`
	Status         string          `json:"status"`
	Progress       float64         `json:"progress"`
	Attempt        int             `json:"attempt"`
	CacheEntryID   *uint           `json:"cache_entry_id,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	ResultHash     string          `json:"result_hash,omitempty"`
	Error          string          `json:"error,omitempty"`
	CheckpointHash string          `json:"checkpoint_hash,omitempty"`
	RNGPosition    int64           `json:"rng_position"`
	StartedAt      string          `json:"started_at,omitempty"`
	CompletedAt    string          `json:"completed_at,omitempty"`
	FailedAt       string          `json:"failed_at,omitempty"`
	CancelledAt    string          `json:"cancelled_at,omitempty"`
}

type ListFilter struct {
	Status       string
	ParentTaskID *uint
	RootOnly     bool
	Limit        int
	Offset       int
}

type ItemFilter struct {
	Status        string
	Limit         int
	Offset        int
	IncludeResult bool
}

type TaskSnapshot struct {
	ID                  uint            `json:"id"`
	PlanKey             string          `json:"plan_key"`
	TaskSchemaVersion   string          `json:"task_schema_version"`
	LifecycleVersion    string          `json:"lifecycle_version"`
	SettingsHash        string          `json:"settings_hash"`
	Settings            json.RawMessage `json:"settings"`
	ResearchSettingID   string          `json:"research_setting_id,omitempty"`
	ResearchSettingHash string          `json:"research_setting_hash,omitempty"`
	ManifestVersion     string          `json:"manifest_version"`
	ManifestHash        string          `json:"manifest_hash"`
	Manifest            json.RawMessage `json:"manifest"`
	CheckpointHash      string          `json:"checkpoint_hash,omitempty"`
	Checkpoint          json.RawMessage `json:"checkpoint,omitempty"`
}

type CacheLookup struct {
	CacheKey    string          `json:"cache_key"`
	Found       bool            `json:"found"`
	ContentHash string          `json:"content_hash,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	CompletedAt string          `json:"completed_at,omitempty"`
}

type LimitsDescriptor struct {
	SoftItemLimit int `json:"soft_item_limit"`
	HardItemLimit int `json:"hard_item_limit"`
	Workers       int `json:"workers"`
}
