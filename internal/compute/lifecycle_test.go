package compute

import "testing"

func TestDeriveTaskStatusNeverTreatsPartialOrCancelledWorkAsComplete(t *testing.T) {
	cases := []struct {
		name      string
		counts    ItemCounts
		started   bool
		cancelled bool
		want      string
	}{
		{"planned", ItemCounts{Total: 2, Pending: 2}, false, false, TaskStatusPlanned},
		{"queued", ItemCounts{Total: 2, Pending: 2}, true, false, TaskStatusQueued},
		{"running", ItemCounts{Total: 2, Running: 1, Pending: 1}, true, false, TaskStatusRunning},
		{"completed", ItemCounts{Total: 2, Completed: 1, Cached: 1}, true, false, TaskStatusCompleted},
		{"partial", ItemCounts{Total: 2, Completed: 1, Failed: 1}, true, false, TaskStatusPartial},
		{"failed", ItemCounts{Total: 2, Failed: 2}, true, false, TaskStatusFailed},
		{"cancelling", ItemCounts{Total: 2, Running: 1, Cancelled: 1}, true, true, TaskStatusRunning},
		{"cancelled", ItemCounts{Total: 2, Completed: 1, Cancelled: 1}, true, true, TaskStatusCancelled},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := DeriveTaskStatus(test.counts, test.started, test.cancelled); got != test.want {
				t.Fatalf("status = %s, want %s", got, test.want)
			}
		})
	}
}

func TestCompositeStatusPreservesCompletedStages(t *testing.T) {
	if got := DeriveCompositeStatus([]string{TaskStatusCompleted, TaskStatusPlanned}); got != TaskStatusPartial {
		t.Fatalf("composite status = %s", got)
	}
	if got := DeriveCompositeStatus([]string{TaskStatusCompleted, TaskStatusCompleted}); got != TaskStatusCompleted {
		t.Fatalf("completed composite status = %s", got)
	}
	if got := DeriveCompositeStatus([]string{TaskStatusCompleted, TaskStatusCancelled}); got != TaskStatusPartial {
		t.Fatalf("cancelled child destroyed completed stage: %s", got)
	}
}
