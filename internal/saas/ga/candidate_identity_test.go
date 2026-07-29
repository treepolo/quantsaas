package ga

import (
	"sync"
	"testing"

	saasstore "quantsaas/internal/saas/store"

	"gorm.io/gorm/schema"
)

func TestCanonicalCandidateIdentityUsesFullParamPack(t *testing.T) {
	first := canonicalCandidateIdentity([]byte(`{"beta":1.00,"gamma":2.00}`))
	second := canonicalCandidateIdentity([]byte(`{"beta":1.05,"gamma":2.00}`))
	if len(first) != 64 {
		t.Fatalf("identity length = %d, want SHA-256 hex", len(first))
	}
	if first == second {
		t.Fatal("different effective parameter packs produced the same identity")
	}
	if first != canonicalCandidateIdentity([]byte(`{"beta":1.00,"gamma":2.00}`)) {
		t.Fatal("same parameter pack produced a different identity")
	}
}

func TestCandidateEvaluationModelHasAtomicIdentityConstraint(t *testing.T) {
	parsed, err := schema.Parse(&saasstore.GeneCandidateEvaluation{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse model schema: %v", err)
	}
	index, ok := parsed.ParseIndexes()["idx_gene_candidate_identity"]
	if !ok {
		t.Fatal("missing durable candidate identity index")
	}
	if index.Class != "UNIQUE" {
		t.Fatalf("candidate identity index class = %q, want UNIQUE", index.Class)
	}
	got := make([]string, 0, len(index.Fields))
	for _, field := range index.Fields {
		got = append(got, field.DBName)
	}
	want := []string{"schema_version", "search_hash", "fingerprint"}
	if len(got) != len(want) {
		t.Fatalf("identity columns = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("identity columns = %v, want %v", got, want)
		}
	}
}

func TestGridCoverageModelKeepsLegacyTableWritable(t *testing.T) {
	parsed, err := schema.Parse(&saasstore.GeneParameterGridPoint{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse grid model schema: %v", err)
	}
	for _, name := range []string{"ParameterState", "GridIndex", "Generation", "LastTaskID", "LastGeneration"} {
		field := parsed.LookUpField(name)
		if field == nil {
			t.Fatalf("missing current grid field %s", name)
		}
		if field.NotNull {
			t.Fatalf("%s must remain nullable while legacy rows exist", name)
		}
	}
	for _, name := range []string{"TaskID", "GridStep"} {
		field := parsed.LookUpField(name)
		if field == nil || !field.NotNull {
			t.Fatalf("legacy compatibility field %s must remain writable and NOT NULL", name)
		}
	}
	index, ok := parsed.ParseIndexes()["idx_gene_parameter_grid_unique"]
	if !ok || index.Class != "UNIQUE" {
		t.Fatal("legacy grid uniqueness index is no longer represented by the model")
	}
}
