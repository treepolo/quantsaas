package controlresearch

import (
	"encoding/json"
	"strings"
	"testing"

	saasstore "quantsaas/internal/saas/store"
)

func TestSnapshotAtLeastAsCompleteRejectsRegression(t *testing.T) {
	current := saasstore.ControlAnalysisSnapshot{
		Completeness:          "partially_completed",
		RandomCompletedCount:  100,
		RuleCompletedCount:    4,
		ShuffleCompletedCount: 40,
	}
	older := current
	older.ShuffleCompletedCount = 26
	if snapshotAtLeastAsComplete(older, current) {
		t.Fatal("older snapshot must not replace newer progress")
	}
	newer := current
	newer.ShuffleCompletedCount = 69
	if !snapshotAtLeastAsComplete(newer, current) {
		t.Fatal("newer snapshot should advance progress")
	}
	complete := newer
	complete.Completeness = "completed"
	complete.ShuffleCompletedCount = 100
	if !snapshotAtLeastAsComplete(complete, current) {
		t.Fatal("completed snapshot should replace partial progress")
	}
	if snapshotAtLeastAsComplete(current, complete) {
		t.Fatal("partial snapshot must not replace a completed snapshot")
	}
}

func TestEvaluationIdentityForItem(t *testing.T) {
	random := saasstore.ComputeTaskItem{ItemKey: "random:000042"}
	kind, index, err := evaluationIdentityForItem("random", random)
	if err != nil || kind != "random" || index != 42 {
		t.Fatalf("random identity = %s:%d err=%v", kind, index, err)
	}
	shuffle := saasstore.ComputeTaskItem{Result: saasstore.JSONB(`{"schema_version":"p11-control-result-v1","kind":"shuffle","sequence_index":40,"backtest_result_id":1}`)}
	kind, index, err = evaluationIdentityForItem("shuffle", shuffle)
	if err != nil || kind != "shuffle" || index != 40 {
		t.Fatalf("shuffle identity = %s:%d err=%v", kind, index, err)
	}
}

func TestRepresentativeRolesFitStoredFieldForSingleResult(t *testing.T) {
	metrics, err := json.Marshal(MetricSet{LogFinalNAVRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	roles := representativeRoles([]saasstore.ControlEvaluation{{ID: 1, Kind: "shuffle", Summary: metrics}})
	if roles[1] != "shuffle_median" {
		t.Fatalf("single representative role = %q", roles[1])
	}
	if len(roles[1]) > 32 {
		t.Fatalf("representative role exceeds stored field: %q", roles[1])
	}
}

func TestConclusionLabelIncludesAllAvailableComparisonMetrics(t *testing.T) {
	sortino := 73.8
	label := conclusionLabel("曝險順序打亂分佈", PercentileSet{LogFinalNAVRatio: 50.4, MaxDrawdown: 96.0, Sortino: &sortino})
	for _, expected := range []string{"評估對象於曝險順序打亂分佈", "報酬第 50.4 百分位", "最大回撤第 96.0 百分位", "Sortino 第 73.8 百分位"} {
		if !strings.Contains(label, expected) {
			t.Fatalf("conclusion label %q is missing %q", label, expected)
		}
	}
}
