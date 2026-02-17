package whailtest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/schmitthub/clawker/pkg/whail"
)

// RecordedBuildEvent pairs a build progress event with its timing delay.
// Delay represents the wall-clock time since the previous event (or since
// recording started for the first event).
type RecordedBuildEvent struct {
	// DelayMs is the delay in milliseconds since the previous event.
	// Stored as int64 for clean JSON (avoids float precision issues).
	DelayMs int64                    `json:"delay_ms"`
	Event   whail.BuildProgressEvent `json:"event"`
}

// Delay returns the delay as a time.Duration.
func (e RecordedBuildEvent) Delay() time.Duration {
	return time.Duration(e.DelayMs) * time.Millisecond
}

// RecordedBuildScenario is a named sequence of timed build events that can be
// serialized to/from JSON for deterministic replay without Docker.
type RecordedBuildScenario struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Events      []RecordedBuildEvent `json:"events"`
}

// FlatEvents extracts just the BuildProgressEvents without timing information.
func (s *RecordedBuildScenario) FlatEvents() []whail.BuildProgressEvent {
	events := make([]whail.BuildProgressEvent, len(s.Events))
	for i, re := range s.Events {
		events[i] = re.Event
	}
	return events
}

// LoadRecordedScenario reads a RecordedBuildScenario from a JSON file.
func LoadRecordedScenario(path string) (*RecordedBuildScenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read recorded scenario %s: %w", path, err)
	}
	var scenario RecordedBuildScenario
	if err := json.Unmarshal(data, &scenario); err != nil {
		return nil, fmt.Errorf("parse recorded scenario %s: %w", path, err)
	}
	return &scenario, nil
}

// LoadRecordedScenarioFromBytes parses a RecordedBuildScenario from JSON bytes.
func LoadRecordedScenarioFromBytes(data []byte) (*RecordedBuildScenario, error) {
	var scenario RecordedBuildScenario
	if err := json.Unmarshal(data, &scenario); err != nil {
		return nil, fmt.Errorf("parse recorded scenario: %w", err)
	}
	return &scenario, nil
}

// SaveRecordedScenario writes a RecordedBuildScenario to a JSON file.
// Creates parent directories as needed.
func SaveRecordedScenario(path string, scenario *RecordedBuildScenario) error {
	data, err := json.MarshalIndent(scenario, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal recorded scenario: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write recorded scenario %s: %w", path, err)
	}
	return nil
}

// RecordedScenarioFromEvents converts a flat event slice into a RecordedBuildScenario
// with uniform delays. Useful for generating synthetic recordings from Go test scenarios.
func RecordedScenarioFromEvents(name, desc string, events []whail.BuildProgressEvent, delay time.Duration) *RecordedBuildScenario {
	delayMs := delay.Milliseconds()
	recorded := make([]RecordedBuildEvent, len(events))
	for i, e := range events {
		recorded[i] = RecordedBuildEvent{
			DelayMs: delayMs,
			Event:   e,
		}
	}
	return &RecordedBuildScenario{
		Name:        name,
		Description: desc,
		Events:      recorded,
	}
}

// RecordedScenarioFromEventsWithTiming converts a flat event slice into a
// RecordedBuildScenario with per-event timing based on the event type:
//   - Internal steps: internalDelay
//   - Running status: runningDelay
//   - Log lines: logDelay
//   - Complete/Cached/Error: completeDelay
//   - Other: completeDelay
func RecordedScenarioFromEventsWithTiming(
	name, desc string,
	events []whail.BuildProgressEvent,
	internalDelay, runningDelay, logDelay, completeDelay time.Duration,
) *RecordedBuildScenario {
	recorded := make([]RecordedBuildEvent, len(events))
	for i, e := range events {
		var d time.Duration
		switch {
		case whail.IsInternalStep(e.StepName):
			d = internalDelay
		case e.LogLine != "" && e.Status == 0:
			d = logDelay
		case e.Status == whail.BuildStepRunning:
			d = runningDelay
		default:
			d = completeDelay
		}
		recorded[i] = RecordedBuildEvent{
			DelayMs: d.Milliseconds(),
			Event:   e,
		}
	}
	return &RecordedBuildScenario{
		Name:        name,
		Description: desc,
		Events:      recorded,
	}
}

// EventRecorder wraps a BuildProgressFunc callback, capturing each event with
// wall-clock timing. Use this to record event sequences from real Docker builds
// for later replay.
type EventRecorder struct {
	name  string
	desc  string
	inner whail.BuildProgressFunc

	mu      sync.Mutex
	events  []RecordedBuildEvent
	lastAt  time.Time
	started bool
}

// NewEventRecorder creates a recorder that forwards events to inner (which may be nil).
func NewEventRecorder(name, desc string, inner whail.BuildProgressFunc) *EventRecorder {
	return &EventRecorder{
		name:  name,
		desc:  desc,
		inner: inner,
	}
}

// OnProgress returns a BuildProgressFunc suitable for use as a callback.
// Each invocation records the event and its delay since the previous event.
func (r *EventRecorder) OnProgress() whail.BuildProgressFunc {
	return func(event whail.BuildProgressEvent) {
		r.mu.Lock()
		now := time.Now()
		var delayMs int64
		if r.started {
			delayMs = max(now.Sub(r.lastAt).Milliseconds(), 0)
		}
		r.lastAt = now
		r.started = true
		r.events = append(r.events, RecordedBuildEvent{
			DelayMs: delayMs,
			Event:   event,
		})
		r.mu.Unlock()

		if r.inner != nil {
			r.inner(event)
		}
	}
}

// Scenario returns the captured events as a RecordedBuildScenario.
// Call after the build completes.
func (r *EventRecorder) Scenario() *RecordedBuildScenario {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := make([]RecordedBuildEvent, len(r.events))
	copy(events, r.events)
	return &RecordedBuildScenario{
		Name:        r.name,
		Description: r.desc,
		Events:      events,
	}
}
