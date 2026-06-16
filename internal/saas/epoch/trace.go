package epoch

import (
	"sync"
	"time"

	"quantsaas/internal/saas/ga"
)

const (
	TraceBufferLimit = 5000
)

type TraceEvent struct {
	ID      uint64         `json:"id"`
	Time    string         `json:"time"`
	Level   string         `json:"level"`
	Source  string         `json:"source"`
	Scope   string         `json:"scope"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

type TraceSnapshot struct {
	TaskID uint         `json:"task_id"`
	Mode   ga.TraceMode `json:"mode"`
	Events []TraceEvent `json:"events"`
}

type traceBuffer struct {
	mu     sync.Mutex
	nextID uint64
	limit  int
	events []TraceEvent
}

func newTraceBuffer(limit int) *traceBuffer {
	if limit <= 0 {
		limit = TraceBufferLimit
	}
	return &traceBuffer{limit: limit, events: make([]TraceEvent, 0, limit)}
}

func (b *traceBuffer) add(event ga.TraceEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	item := TraceEvent{
		ID:      b.nextID,
		Time:    time.Now().UTC().Format(time.RFC3339Nano),
		Level:   event.Level,
		Source:  event.Source,
		Scope:   event.Scope,
		Message: event.Message,
		Fields:  event.Fields,
	}
	b.events = append(b.events, item)
	if len(b.events) > b.limit {
		copy(b.events, b.events[len(b.events)-b.limit:])
		b.events = b.events[:b.limit]
	}
}

func (b *traceBuffer) snapshot(afterID uint64, limit int) []TraceEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	if limit <= 0 || limit > b.limit {
		limit = b.limit
	}
	out := make([]TraceEvent, 0, min(limit, len(b.events)))
	for _, event := range b.events {
		if event.ID > afterID {
			out = append(out, event)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}
