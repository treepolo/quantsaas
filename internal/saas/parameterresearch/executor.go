package parameterresearch

import (
	"context"
	"encoding/json"
	"fmt"

	compute "quantsaas/internal/compute"
	core "quantsaas/internal/parameterresearch"
	"quantsaas/internal/saas/computetask"
)

type SurrogateExecutor struct{}

func NewSurrogateExecutor() *SurrogateExecutor { return &SurrogateExecutor{} }

func (e *SurrogateExecutor) Descriptor() compute.ExecutorDescriptor {
	return compute.ExecutorDescriptor{Type: SurrogateExecutorType, Version: SurrogateExecutorVersion, ResultSchemaVersion: SurrogateResultVersion}
}

func (e *SurrogateExecutor) Execute(ctx context.Context, execution computetask.Execution) (json.RawMessage, error) {
	var input SurrogateExecutionInput
	if err := json.Unmarshal(execution.Input, &input); err != nil || input.SchemaVersion != ConfigurationSchemaVersion {
		return nil, ErrInvalidRequest
	}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: .05}); err != nil {
			return nil, err
		}
	}
	artifact, err := core.TrainSurrogate(input.Examples, input.N0, input.Settings)
	if err != nil {
		return nil, err
	}
	artifactRaw, err := compute.CanonicalJSON(artifact)
	if err != nil {
		return nil, err
	}
	result := SurrogateExecutionResult{SchemaVersion: SurrogateResultVersion, Artifact: artifact, ContentHash: compute.HashBytes(artifactRaw)}
	raw, err := compute.CanonicalJSON(result)
	if err != nil {
		return nil, err
	}
	if execution.Report != nil {
		if err := execution.Report(ctx, computetask.ProgressUpdate{Progress: 1}); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

func (e *SurrogateExecutor) ValidateCachedResult(_ context.Context, _ uint, raw json.RawMessage) error {
	var result SurrogateExecutionResult
	if err := json.Unmarshal(raw, &result); err != nil || result.SchemaVersion != SurrogateResultVersion {
		return ErrInvalidRequest
	}
	artifact, err := compute.CanonicalJSON(result.Artifact)
	if err != nil || compute.HashBytes(artifact) != result.ContentHash {
		return fmt.Errorf("P10 代理模型快取內容 hash 不符")
	}
	return nil
}
