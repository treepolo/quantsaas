package compute

import (
	"fmt"
	"sort"
	"strings"
)

const (
	CacheLayerBacktest   = "backtest"
	CacheLayerEvaluation = "evaluation"
	CacheLayerAnalysis   = "analysis"
)

type EvaluationPointIdentity struct {
	ResearchSettingHash       string `json:"research_setting_hash"`
	ParameterSpaceSchema      string `json:"parameter_space_schema"`
	ParameterQuantization     string `json:"parameter_quantization"`
	NormalizedParameterVector any    `json:"normalized_parameter_vector"`
}

type AnalysisIdentity struct {
	AnalysisType    string   `json:"analysis_type"`
	AnalysisVersion string   `json:"analysis_version"`
	SourceKeys      []string `json:"source_keys"`
	Settings        any      `json:"settings"`
}

type layeredKeySnapshot struct {
	SchemaVersion string   `json:"schema_version"`
	Layer         string   `json:"layer"`
	IdentityHash  string   `json:"identity_hash"`
	UpstreamKeys  []string `json:"upstream_keys,omitempty"`
}

// BuildLayeredKey derives a stable identity for one cache layer. Task IDs,
// titles, parent IDs and UI metadata are intentionally not accepted here;
// callers provide only result-changing identity and explicit upstream keys.
func BuildLayeredKey(layer string, identity any, upstreamKeys ...string) (string, error) {
	layer = strings.TrimSpace(layer)
	switch layer {
	case CacheLayerBacktest, CacheLayerEvaluation, CacheLayerAnalysis:
	default:
		return "", fmt.Errorf("unsupported compute cache layer %q", layer)
	}
	identityJSON, err := CanonicalJSON(identity)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s cache identity: %w", layer, err)
	}
	upstream := make([]string, 0, len(upstreamKeys))
	seen := make(map[string]struct{}, len(upstreamKeys))
	for _, raw := range upstreamKeys {
		key := strings.TrimSpace(raw)
		if key == "" {
			return "", fmt.Errorf("%s cache identity has an empty upstream key", layer)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		upstream = append(upstream, key)
	}
	sort.Strings(upstream)
	snapshotJSON, err := CanonicalJSON(layeredKeySnapshot{
		SchemaVersion: LayeredKeySchemaVersion, Layer: layer,
		IdentityHash: HashBytes(identityJSON), UpstreamKeys: upstream,
	})
	if err != nil {
		return "", err
	}
	return "compute-" + layer + ":v1:" + hashHex(snapshotJSON), nil
}

func BuildEvaluationPointKey(identity EvaluationPointIdentity) (string, error) {
	if strings.TrimSpace(identity.ResearchSettingHash) == "" || strings.TrimSpace(identity.ParameterSpaceSchema) == "" ||
		strings.TrimSpace(identity.ParameterQuantization) == "" || identity.NormalizedParameterVector == nil {
		return "", fmt.Errorf("evaluation point identity is incomplete")
	}
	return BuildLayeredKey(CacheLayerEvaluation, identity)
}

func BuildAnalysisKey(identity AnalysisIdentity) (string, error) {
	if strings.TrimSpace(identity.AnalysisType) == "" || strings.TrimSpace(identity.AnalysisVersion) == "" || len(identity.SourceKeys) == 0 {
		return "", fmt.Errorf("analysis identity is incomplete")
	}
	settings := identity.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	return BuildLayeredKey(CacheLayerAnalysis, struct {
		AnalysisType    string `json:"analysis_type"`
		AnalysisVersion string `json:"analysis_version"`
		Settings        any    `json:"settings"`
	}{identity.AnalysisType, identity.AnalysisVersion, settings}, identity.SourceKeys...)
}
