package researchdata

import "testing"

func TestAlignStatsMissingPolicies(t *testing.T) {
	timeline := []int64{10, 20, 30, 40}
	rows := []seriesPoint{
		{Time: 10, Close: 1},
		{Time: 30, Close: 3},
	}

	aligned, missing, filled := alignStats(timeline, rows, MissingPolicyEmpty)
	if aligned != 2 || missing != 2 || filled != 0 {
		t.Fatalf("empty = aligned %d missing %d filled %d, want 2/2/0", aligned, missing, filled)
	}

	aligned, missing, filled = alignStats(timeline, rows, MissingPolicyForwardFill)
	if aligned != 4 || missing != 0 || filled != 2 {
		t.Fatalf("forward_fill = aligned %d missing %d filled %d, want 4/0/2", aligned, missing, filled)
	}

	aligned, missing, filled = alignStats(timeline, rows, MissingPolicyLinear)
	if aligned != 3 || missing != 1 || filled != 1 {
		t.Fatalf("linear = aligned %d missing %d filled %d, want 3/1/1", aligned, missing, filled)
	}
}

func TestNormalizeMissingPolicy(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "", want: MissingPolicyEmpty},
		{input: "unknown", want: MissingPolicyEmpty},
		{input: MissingPolicyForwardFill, want: MissingPolicyForwardFill},
		{input: MissingPolicyLinear, want: MissingPolicyLinear},
	}
	for _, tt := range tests {
		if got := normalizeMissingPolicy(tt.input); got != tt.want {
			t.Fatalf("normalizeMissingPolicy(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSearchReadinessRequiresConfirmedAlgorithmForIndicators(t *testing.T) {
	canSearch, reason := searchReadiness(0, "", nil)
	if !canSearch || reason != "" {
		t.Fatalf("zero indicator readiness = %v %q, want true empty reason", canSearch, reason)
	}

	canSearch, reason = searchReadiness(1, "", nil)
	if canSearch || reason == "" {
		t.Fatalf("indicator without algorithm readiness = %v %q, want blocked", canSearch, reason)
	}

	canSearch, reason = searchReadiness(1, "confirmed-experiment", nil)
	if !canSearch || reason != "" {
		t.Fatalf("indicator with algorithm readiness = %v %q, want true empty reason", canSearch, reason)
	}
}
