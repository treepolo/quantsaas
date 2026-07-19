package controlresearch

import (
	"encoding/json"
	"testing"
	"time"

	saasstore "quantsaas/internal/saas/store"
)

func TestSnapshotDescriptorNormalizesLegacyNullCollections(t *testing.T) {
	row := saasstore.ControlAnalysisSnapshot{
		ID: 1, Completeness: "partially_completed", StatisticsVersion: "test", ContentHash: "hash", CreatedAt: time.Unix(1, 0),
		Summary: saasstore.JSONB(`{"schema_version":"p11-control-snapshot-v1","baseline_evaluation_id":1,"baseline_result_id":1,"baseline":{},"rules":null,"conclusion_labels":null}`),
	}
	descriptor, err := (&Service{}).snapshotDescriptorFrom(row)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Summary.Rules == nil || descriptor.Summary.ConclusionLabels == nil {
		t.Fatalf("legacy null collections were not normalized: %+v", descriptor.Summary)
	}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	summary := payload["summary"].(map[string]any)
	if summary["rules"] == nil || summary["conclusion_labels"] == nil {
		t.Fatalf("API payload still exposes null collections: %s", raw)
	}
}
