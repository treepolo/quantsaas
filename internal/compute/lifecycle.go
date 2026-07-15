package compute

func Progress(counts ItemCounts) float64 {
	if counts.Total <= 0 {
		return 0
	}
	value := float64(counts.Valid()) / float64(counts.Total)
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func DeriveTaskStatus(counts ItemCounts, started bool, cancelRequested bool) string {
	if cancelRequested {
		if counts.Running > 0 {
			return TaskStatusRunning
		}
		return TaskStatusCancelled
	}
	if counts.Total <= 0 {
		if started {
			return TaskStatusCompleted
		}
		return TaskStatusPlanned
	}
	if counts.Valid() == counts.Total {
		return TaskStatusCompleted
	}
	if counts.Running > 0 {
		return TaskStatusRunning
	}
	if !started {
		return TaskStatusPlanned
	}
	if counts.Pending > 0 {
		return TaskStatusQueued
	}
	if counts.Valid() > 0 {
		return TaskStatusPartial
	}
	if counts.Failed > 0 {
		return TaskStatusFailed
	}
	return TaskStatusCancelled
}

func DeriveCompositeStatus(statuses []string) string {
	if len(statuses) == 0 {
		return TaskStatusPlanned
	}
	completed, running, planned, failed, cancelled, partial, invalidated := 0, 0, 0, 0, 0, 0, 0
	for _, status := range statuses {
		switch status {
		case TaskStatusCompleted:
			completed++
		case TaskStatusRunning, TaskStatusQueued:
			running++
		case TaskStatusPlanned:
			planned++
		case TaskStatusFailed:
			failed++
		case TaskStatusCancelled:
			cancelled++
		case TaskStatusInvalidated:
			invalidated++
		default:
			partial++
		}
	}
	if invalidated > 0 {
		return TaskStatusInvalidated
	}
	if completed == len(statuses) {
		return TaskStatusCompleted
	}
	if running > 0 {
		return TaskStatusRunning
	}
	if planned == len(statuses) {
		return TaskStatusPlanned
	}
	if failed == len(statuses) {
		return TaskStatusFailed
	}
	if cancelled == len(statuses) {
		return TaskStatusCancelled
	}
	if completed > 0 || partial > 0 || planned > 0 || failed > 0 || cancelled > 0 {
		return TaskStatusPartial
	}
	return TaskStatusPlanned
}

func CanStart(status string) bool {
	return status == TaskStatusPlanned || status == TaskStatusPartial
}

func CanRetry(status string) bool {
	return status == TaskStatusFailed || status == TaskStatusCancelled || status == TaskStatusPartial
}

func IsTerminal(status string) bool {
	switch status {
	case TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled, TaskStatusInvalidated:
		return true
	default:
		return false
	}
}
