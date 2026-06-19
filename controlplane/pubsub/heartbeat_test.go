package pubsub

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

// decodeLines parses each non-empty newline-delimited JSON object the logger
// wrote into the buffer.
func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("unmarshal log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// waitFor polls cond until true or the deadline, so the test never sleeps on a
// fixed duration that flakes under load.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestStatsHeartbeat_SamplesEverySourceEachTick(t *testing.T) {
	var buf threadSafeBuffer
	log := logger.NewWriter(&buf)

	var topicCalls, worldviewCalls atomic.Int64
	topicSrc := NewTopicStatsSource("docker", func() Stats {
		topicCalls.Add(1)
		return Stats{
			Subscribers:         3,
			QueueCapacity:       2048,
			PublishedTotal:      10,
			DroppedTotal:        1,
			PanicsRecovered:     2,
			HookPanicsRecovered: 4,
		}
	})
	worldviewSrc := NewWorldviewStatsSource("agent", func() int {
		worldviewCalls.Add(1)
		return 7
	})

	hb := NewStatsHeartbeat(log, time.Millisecond, topicSrc, worldviewSrc)
	ctx, cancel := context.WithCancel(context.Background())
	hb.Start(ctx)

	// Both sources must be sampled at least once before we assert field shape.
	waitFor(t, func() bool { return topicCalls.Load() > 0 && worldviewCalls.Load() > 0 })
	cancel()

	lines := decodeLines(t, buf.buf())
	var sawTopic, sawWorldview bool
	for _, l := range lines {
		switch l["message"] {
		case logMsgStatsHeartbeat:
			sawTopic = true
			// Exact field fidelity with the pre-extraction cmd.go log line.
			assertField(t, l, logFieldTopic, "docker")
			assertNum(t, l, logFieldSubscribers, 3)
			assertNum(t, l, logFieldQueueCapacity, 2048)
			assertNum(t, l, logFieldPublishedTotal, 10)
			assertNum(t, l, logFieldDroppedTotal, 1)
			assertNum(t, l, logFieldPanicsRecovered, 2)
			assertNum(t, l, logFieldHookPanics, 4)
		case logMsgWorldviewHeartbeat:
			sawWorldview = true
			assertField(t, l, logFieldTopic, "agent")
			assertNum(t, l, logFieldAgentWorldview, 7)
		}
	}
	if !sawTopic {
		t.Errorf("no %q line emitted; lines=%v", logMsgStatsHeartbeat, lines)
	}
	if !sawWorldview {
		t.Errorf("no %q line emitted; lines=%v", logMsgWorldviewHeartbeat, lines)
	}
}

func TestStatsHeartbeat_RecoversPanickingSource(t *testing.T) {
	var buf threadSafeBuffer
	log := logger.NewWriter(&buf)

	panicSrc := sourceFunc(func(*logger.Logger) { panic("boom") })

	hb := NewStatsHeartbeat(log, time.Millisecond, panicSrc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hb.Start(ctx)

	// The recover must audit the panic with the exact event token cmd.go used,
	// rather than letting the goroutine die unobserved.
	waitFor(t, func() bool {
		for _, l := range decodeLines(t, buf.buf()) {
			if l["message"] == logMsgHeartbeatPanic &&
				l[logFieldHeartbeatEvent] == eventStatsHeartbeatPanic {
				return true
			}
		}
		return false
	})
}

func TestStatsHeartbeat_StopsOnContextCancel(t *testing.T) {
	var calls atomic.Int64
	src := sourceFunc(func(*logger.Logger) { calls.Add(1) })

	hb := NewStatsHeartbeat(logger.Nop(), time.Millisecond, src)
	ctx, cancel := context.WithCancel(context.Background())
	hb.Start(ctx)

	waitFor(t, func() bool { return calls.Load() > 0 })
	cancel()

	// After cancel the loop must stop sampling. Observe a quiescent count.
	waitFor(t, func() bool {
		before := calls.Load()
		time.Sleep(20 * time.Millisecond)
		return calls.Load() == before
	})
}

func TestNewStatsHeartbeat_NonPositiveIntervalFallsBack(t *testing.T) {
	hb := NewStatsHeartbeat(logger.Nop(), 0)
	if hb.interval != DefaultStatsInterval {
		t.Fatalf("interval = %v, want fallback %v", hb.interval, DefaultStatsInterval)
	}
	hb = NewStatsHeartbeat(logger.Nop(), -1)
	if hb.interval != DefaultStatsInterval {
		t.Fatalf("interval = %v, want fallback %v", hb.interval, DefaultStatsInterval)
	}
}

// sourceFunc adapts a bare func to a StatsSource for tests.
type sourceFunc func(*logger.Logger)

func (f sourceFunc) LogSnapshot(log *logger.Logger) { f(log) }

// threadSafeBuffer is a bytes.Buffer guarded for concurrent writes (the
// heartbeat goroutine writes while the test reads).
type threadSafeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *threadSafeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *threadSafeBuffer) buf() *bytes.Buffer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.NewBuffer(s.b.Bytes())
}

func assertField(t *testing.T, l map[string]any, key, want string) {
	t.Helper()
	if got, _ := l[key].(string); got != want {
		t.Errorf("field %q = %v, want %q", key, l[key], want)
	}
}

func assertNum(t *testing.T, l map[string]any, key string, want float64) {
	t.Helper()
	if got, _ := l[key].(float64); got != want {
		t.Errorf("field %q = %v, want %v", key, l[key], want)
	}
}
