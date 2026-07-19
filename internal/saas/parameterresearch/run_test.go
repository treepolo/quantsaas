package parameterresearch

import "testing"

func TestPreserveUserControlledRunStatus(t *testing.T) {
	tests := []struct {
		stored  string
		derived string
		want    string
	}{
		{stored: "paused", derived: "cancelled", want: "paused"},
		{stored: "cancelled", derived: "partial", want: "cancelled"},
		{stored: "running", derived: "completed", want: "completed"},
	}
	for _, test := range tests {
		got := preserveUserControlledRunStatus(test.stored, test.derived)
		if got != test.want {
			t.Fatalf("stored=%s derived=%s: got %s, want %s", test.stored, test.derived, got, test.want)
		}
	}
}
