package computetask

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	compute "quantsaas/internal/compute"
)

type ProgressUpdate struct {
	Progress    float64
	Checkpoint  json.RawMessage
	RNGPosition int64
}

type Execution struct {
	UserID      uint
	TaskID      uint
	ItemID      uint
	ItemKey     string
	Input       json.RawMessage
	Checkpoint  json.RawMessage
	RNG         compute.RNGSpec
	RNGPosition int64
	Report      func(context.Context, ProgressUpdate) error
	CountUnits  func(int64)
	Heartbeat   func(string)
}

type Executor interface {
	Descriptor() compute.ExecutorDescriptor
	Execute(context.Context, Execution) (json.RawMessage, error)
}

type CachedResultValidator interface {
	ValidateCachedResult(context.Context, uint, json.RawMessage) error
}

type Registry struct {
	mu        sync.RWMutex
	executors map[string]Executor
}

func NewRegistry() *Registry {
	return &Registry{executors: make(map[string]Executor)}
}

func (r *Registry) Register(executor Executor) error {
	if executor == nil {
		return fmt.Errorf("executor is required")
	}
	descriptor := executor.Descriptor()
	if descriptor.Type == "" || descriptor.Version == "" || descriptor.ResultSchemaVersion == "" {
		return fmt.Errorf("executor descriptor is incomplete")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.executors[descriptor.Type]; exists {
		return fmt.Errorf("executor %s is already registered", descriptor.Type)
	}
	r.executors[descriptor.Type] = executor
	return nil
}

func (r *Registry) Get(executorType string) (Executor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	executor, ok := r.executors[executorType]
	return executor, ok
}
