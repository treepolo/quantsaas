package researchdata

import (
	"testing"
	"time"
)

func TestAlignStatsMissingPolicies(t *testing.T) {
	timeline := []int64{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(),
		time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).UnixMilli(),
		time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC).UnixMilli(),
		time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC).UnixMilli(),
	}
	rows := []seriesPoint{
		{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), AvailableTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), Close: 1},
		{Time: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC).UnixMilli(), AvailableTime: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC).UnixMilli(), Close: 3},
	}

	aligned, missing, filled := alignStats(timeline, rows, MissingPolicyForwardFill)
	if aligned != 4 || missing != 0 || filled != 2 {
		t.Fatalf("forward_fill = aligned %d missing %d filled %d, want 4/0/2", aligned, missing, filled)
	}
}

func TestNormalizeMissingPolicy(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "", want: MissingPolicyForwardFill},
		{input: "unknown", want: MissingPolicyForwardFill},
		{input: "empty", want: MissingPolicyForwardFill},
		{input: MissingPolicyForwardFill, want: MissingPolicyForwardFill},
		{input: "linear", want: MissingPolicyForwardFill},
	}
	for _, tt := range tests {
		if got := normalizeMissingPolicy(tt.input); got != tt.want {
			t.Fatalf("normalizeMissingPolicy(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAlignStatsRespectsIndicatorAvailability(t *testing.T) {
	timeline := []int64{
		time.Date(2026, 1, 2, 21, 30, 0, 0, time.UTC).UnixMilli(),
		time.Date(2026, 1, 5, 21, 30, 0, 0, time.UTC).UnixMilli(),
		time.Date(2026, 1, 6, 21, 30, 0, 0, time.UTC).UnixMilli(),
	}
	rows := []seriesPoint{
		{
			Time:          time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).UnixMilli(),
			AvailableTime: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC).UnixMilli(),
			Close:         4.1,
		},
		{
			Time:          time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC).UnixMilli(),
			AvailableTime: time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC).UnixMilli(),
			Close:         4.2,
		},
	}

	aligned, missing, filled := alignStats(timeline, rows, MissingPolicyForwardFill)
	if aligned != 2 || missing != 1 || filled != 2 {
		t.Fatalf("availability forward_fill = aligned %d missing %d filled %d, want 2/1/2", aligned, missing, filled)
	}
}

func TestAlignStatsUsesReleasedIndicatorValueAcrossLaterTimeline(t *testing.T) {
	timeline := []int64{
		time.Date(2026, 1, 2, 21, 30, 0, 0, time.UTC).UnixMilli(),
		time.Date(2026, 1, 5, 21, 30, 0, 0, time.UTC).UnixMilli(),
		time.Date(2026, 1, 6, 21, 30, 0, 0, time.UTC).UnixMilli(),
	}
	rows := []seriesPoint{
		{
			Time:          time.Date(2025, 12, 1, 8, 0, 0, 0, time.UTC).UnixMilli(),
			AvailableTime: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC).UnixMilli(),
			Close:         4.1,
		},
	}

	aligned, missing, filled := alignStats(timeline, rows, MissingPolicyForwardFill)
	if aligned != 2 || missing != 1 || filled != 2 {
		t.Fatalf("released indicator alignment = aligned %d missing %d filled %d, want 2/1/2", aligned, missing, filled)
	}
}

func TestSummarizeAlignmentReportsActualAlignedWindow(t *testing.T) {
	timeline := []int64{
		time.Date(2026, 1, 2, 21, 30, 0, 0, time.UTC).UnixMilli(),
		time.Date(2026, 1, 5, 21, 30, 0, 0, time.UTC).UnixMilli(),
		time.Date(2026, 1, 6, 21, 30, 0, 0, time.UTC).UnixMilli(),
	}
	rows := []seriesPoint{
		{
			Time:          time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC).UnixMilli(),
			AvailableTime: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC).UnixMilli(),
			Close:         4.1,
		},
	}

	stats := summarizeAlignment(timeline, rows, MissingPolicyForwardFill)
	if stats.firstAlignedTime != timeline[1] || stats.lastAlignedTime != timeline[2] {
		t.Fatalf("aligned window = %d..%d, want %d..%d", stats.firstAlignedTime, stats.lastAlignedTime, timeline[1], timeline[2])
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
