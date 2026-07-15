package compute

import "testing"

func TestLayeredKeysAreStableAndVersionSensitive(t *testing.T) {
	first, err := BuildEvaluationPointKey(EvaluationPointIdentity{
		ResearchSettingHash: "research-a", ParameterSpaceSchema: "space-v1",
		ParameterQuantization: "quant-v1", NormalizedParameterVector: map[string]any{"beta": 1.2, "gamma": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildEvaluationPointKey(EvaluationPointIdentity{
		ResearchSettingHash: "research-a", ParameterSpaceSchema: "space-v1",
		ParameterQuantization: "quant-v1", NormalizedParameterVector: map[string]any{"gamma": 2.0, "beta": 1.20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("equivalent evaluation identities differ: %s != %s", first, second)
	}
	changed, err := BuildEvaluationPointKey(EvaluationPointIdentity{
		ResearchSettingHash: "research-a", ParameterSpaceSchema: "space-v2",
		ParameterQuantization: "quant-v1", NormalizedParameterVector: map[string]any{"beta": 1.2, "gamma": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("parameter space version did not invalidate evaluation point key")
	}
}

func TestAnalysisKeyCanonicalizesSourceSet(t *testing.T) {
	left, err := BuildAnalysisKey(AnalysisIdentity{AnalysisType: "heatmap", AnalysisVersion: "v1", SourceKeys: []string{"b", "a"}, Settings: map[string]any{"bins": 20}})
	if err != nil {
		t.Fatal(err)
	}
	right, err := BuildAnalysisKey(AnalysisIdentity{AnalysisType: "heatmap", AnalysisVersion: "v1", SourceKeys: []string{"a", "b"}, Settings: map[string]any{"bins": 20.0}})
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("analysis source set ordering changed key: %s != %s", left, right)
	}
}
