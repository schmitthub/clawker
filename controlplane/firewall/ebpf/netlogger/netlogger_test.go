package netlogger

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	mobycontainer "github.com/moby/moby/api/types/container"
	mobyevents "github.com/moby/moby/api/types/events"
	mobyclient "github.com/moby/moby/client"

	"github.com/schmitthub/clawker/controlplane/dockerevents"
	ebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/controlplane/pubsub"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// testTopics builds the enroll + evict topics a Service consumes, closing
// both on test cleanup so the per-subscriber drain goroutines exit.
func testTopics(t *testing.T) (*pubsub.Topic[ebpf.EBPFContainerEnrolled], *pubsub.Topic[dockerevents.DockerEvent]) {
	t.Helper()
	enrolled, err := pubsub.NewTopic[ebpf.EBPFContainerEnrolled](logger.Nop())
	if err != nil {
		t.Fatalf("new enroll topic: %v", err)
	}
	evict, err := pubsub.NewTopic[dockerevents.DockerEvent](logger.Nop())
	if err != nil {
		t.Fatalf("new evict topic: %v", err)
	}
	t.Cleanup(func() {
		_ = enrolled.Close()
		_ = evict.Close()
	})
	return enrolled, evict
}

// publishEnrolled is the test-side producer mirroring what
// firewall.Handler.publishEnrolled does on the wire.
func publishEnrolled(top *pubsub.Topic[ebpf.EBPFContainerEnrolled], payload ebpf.EBPFContainerEnrolled) bool {
	return top.Publish(pubsub.Event[ebpf.EBPFContainerEnrolled]{
		Timestamp: time.Now().UnixNano(),
		Source:    "test",
		Payload:   payload,
	})
}

// publishDockerEvent is the test-side producer for the evict topic.
func publishDockerEvent(top *pubsub.Topic[dockerevents.DockerEvent], payload dockerevents.DockerEvent) bool {
	return top.Publish(pubsub.Event[dockerevents.DockerEvent]{
		Timestamp: time.Now().UnixNano(),
		Source:    "test",
		Payload:   payload,
	})
}

type fakeInspecter struct {
	calls atomic.Int32
	out   map[string]mobyclient.ContainerInspectResult
	err   error
}

func (f *fakeInspecter) ContainerInspect(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
	f.calls.Add(1)
	if f.err != nil {
		return mobyclient.ContainerInspectResult{}, f.err
	}
	res, ok := f.out[id]
	if !ok {
		return mobyclient.ContainerInspectResult{}, errors.New("inspect: not found")
	}
	return res, nil
}

func inspectFor(id, agent, project string) mobyclient.ContainerInspectResult {
	return mobyclient.ContainerInspectResult{
		Container: mobycontainer.InspectResponse{
			ID: id,
			Config: &mobycontainer.Config{Labels: map[string]string{
				"dev.clawker.agent":   agent,
				"dev.clawker.project": project,
			}},
		},
	}
}

// TestService_New_RequiredDeps locks the constructor's validation
// surface so a future caller can't accidentally land a nil dep and
// see a panic deep in the run loop.
func TestService_New_RequiredDeps(t *testing.T) {
	enrolled, evict := testTopics(t)
	insp := &fakeInspecter{}
	cfg := configmocks.NewBlankConfig()
	cases := []struct {
		name   string
		mutate func(d *Deps)
	}{
		{"nil mgr", func(d *Deps) { d.Mgr = nil }},
		{"nil enrolled topic", func(d *Deps) { d.EnrolledTopic = nil }},
		{"nil evict topic", func(d *Deps) { d.EvictTopic = nil }},
		{"nil docker", func(d *Deps) { d.Docker = nil }},
		{"nil cfg", func(d *Deps) { d.Cfg = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Deps{
				Mgr:           &ebpf.Manager{},
				EnrolledTopic: enrolled,
				EvictTopic:    evict,
				Docker:        insp,
				Cfg:           cfg,
			}
			tc.mutate(&d)
			if _, err := New(d); err == nil {
				t.Fatalf("expected error on %s", tc.name)
			}
		})
	}
}

// TestService_Enroll_HydratesLabelCache exercises the
// EBPFContainerEnrolled → ContainerInspect → LabelCache.AddOrUpdate
// chain. This is the load-bearing test for the wiring: the rest of
// the package's tests fake the cache directly, but Service.Start is
// the seam that proves the bus subscription, inspect lookup, and
// cache mutate compose end-to-end without an explicit backfill.
func TestService_Enroll_HydratesLabelCache(t *testing.T) {
	enrolled, evict := testTopics(t)
	insp := &fakeInspecter{out: map[string]mobyclient.ContainerInspectResult{
		"cid-abc": inspectFor("cid-abc", "agent-x", "proj-y"),
	}}
	svc := newTestService(t, Deps{
		EnrolledTopic: enrolled,
		EvictTopic:    evict,
		Docker:        insp,
		Cfg:           configmocks.NewBlankConfig(),
	})

	if !publishEnrolled(enrolled, ebpf.EBPFContainerEnrolled{
		CgroupID:    7,
		ContainerID: "cid-abc",
		At:          time.Now(),
	}) {
		t.Fatalf("Publish returned false")
	}

	if !eventuallyTrue(2*time.Second, func() bool {
		_, _, _, ok := svc.cache.Lookup(7)
		return ok
	}) {
		t.Fatalf("cache did not hydrate within deadline")
	}
	cid, agent, project, ok := svc.cache.Lookup(7)
	if !ok || cid != "cid-abc" || agent != "agent-x" || project != "proj-y" {
		t.Fatalf("cache populated wrong: cid=%q agent=%q project=%q ok=%v", cid, agent, project, ok)
	}
	if got := insp.calls.Load(); got != 1 {
		t.Errorf("ContainerInspect calls = %d; want 1 (one per enroll)", got)
	}
}

// TestService_Evict_OnDockerDieRemovesCacheEntry pins the eviction
// half of the LabelCache lifecycle. The subscription must drop the
// entry on container/die so a reused cgroup_id can't return stale
// labels.
func TestService_Evict_OnDockerDieRemovesCacheEntry(t *testing.T) {
	enrolled, evict := testTopics(t)
	insp := &fakeInspecter{out: map[string]mobyclient.ContainerInspectResult{
		"cid-abc": inspectFor("cid-abc", "agent-x", "proj-y"),
	}}
	svc := newTestService(t, Deps{
		EnrolledTopic: enrolled,
		EvictTopic:    evict,
		Docker:        insp,
		Cfg:           configmocks.NewBlankConfig(),
	})

	publishEnrolled(enrolled, ebpf.EBPFContainerEnrolled{CgroupID: 7, ContainerID: "cid-abc", At: time.Now()})
	if !eventuallyTrue(2*time.Second, func() bool { _, _, _, ok := svc.cache.Lookup(7); return ok }) {
		t.Fatalf("cache did not hydrate before evict")
	}
	publishDockerEvent(evict, dockerevents.DockerEvent{Message: mobyevents.Message{
		Type:     mobyevents.ContainerEventType,
		Action:   mobyevents.ActionDie,
		Actor:    mobyevents.Actor{ID: "cid-abc"},
		TimeNano: time.Now().UnixNano(),
	}})
	if !eventuallyTrue(2*time.Second, func() bool { _, _, _, ok := svc.cache.Lookup(7); return !ok }) {
		t.Fatalf("cache did not evict after container/die")
	}
}

// TestService_Evict_IgnoresNonDieActions guards the filter:
// container/start must not evict (that would wipe the entry just
// hydrated by the corresponding EBPFContainerEnrolled), and other
// event types must not match at all.
func TestService_Evict_IgnoresNonDieActions(t *testing.T) {
	enrolled, evict := testTopics(t)
	insp := &fakeInspecter{out: map[string]mobyclient.ContainerInspectResult{
		"cid-abc": inspectFor("cid-abc", "agent-x", "proj-y"),
	}}
	svc := newTestService(t, Deps{
		EnrolledTopic: enrolled,
		EvictTopic:    evict,
		Docker:        insp,
		Cfg:           configmocks.NewBlankConfig(),
	})

	publishEnrolled(enrolled, ebpf.EBPFContainerEnrolled{CgroupID: 7, ContainerID: "cid-abc", At: time.Now()})
	eventuallyTrue(2*time.Second, func() bool { _, _, _, ok := svc.cache.Lookup(7); return ok })

	// Non-die actions must not evict.
	publishDockerEvent(evict, dockerevents.DockerEvent{Message: mobyevents.Message{
		Type: mobyevents.ContainerEventType, Action: mobyevents.ActionStart,
		Actor: mobyevents.Actor{ID: "cid-abc"}, TimeNano: time.Now().UnixNano(),
	}})
	publishDockerEvent(evict, dockerevents.DockerEvent{Message: mobyevents.Message{
		Type: mobyevents.NetworkEventType, Action: mobyevents.ActionDisconnect,
		Actor: mobyevents.Actor{ID: "cid-abc"}, TimeNano: time.Now().UnixNano(),
	}})

	// Give the topics a moment to dispatch both events before
	// asserting non-eviction; without this the assertion might
	// race the dispatch and pass for the wrong reason.
	time.Sleep(50 * time.Millisecond)
	if _, _, _, ok := svc.cache.Lookup(7); !ok {
		t.Fatalf("cache lost binding to a non-die action")
	}
}

// TestService_InspectFailureIsLogged_NotFatal asserts the degraded
// path: docker daemon hiccup at enroll time MUST NOT panic and MUST
// leave the cache without an entry (the next emit lands with empty
// attribution per the strict directive).
func TestService_InspectFailureIsLogged_NotFatal(t *testing.T) {
	enrolled, evict := testTopics(t)
	insp := &fakeInspecter{err: errors.New("docker daemon unreachable")}
	svc := newTestService(t, Deps{
		EnrolledTopic: enrolled,
		EvictTopic:    evict,
		Docker:        insp,
		Cfg:           configmocks.NewBlankConfig(),
	})

	publishEnrolled(enrolled, ebpf.EBPFContainerEnrolled{CgroupID: 7, ContainerID: "cid-abc", At: time.Now()})
	// The subscriber goroutine processes asynchronously; wait for
	// the inspect call to land before asserting cache stayed empty.
	if !eventuallyTrue(2*time.Second, func() bool { return insp.calls.Load() == 1 }) {
		t.Fatalf("inspect call did not fire")
	}
	if _, _, _, ok := svc.cache.Lookup(7); ok {
		t.Fatalf("cache should remain empty when inspect fails")
	}
}

// newTestService builds a Service without invoking Start. Real
// Start opens a ringbuf.NewReader against a *ebpf.Map, which would
// require CAP_BPF that the dev container lacks. Instead we wire the
// bus subscription path manually and rely on the bus to drive cache
// hydration / eviction. The reader + processor + reverse-DNS
// goroutines are not exercised here; their behavior is covered by
// the targeted reader / processor / reverse-DNS tests.
func newTestService(t *testing.T, d Deps) *Service {
	t.Helper()
	d.Mgr = &ebpf.Manager{} // never dereferenced — Start is not called
	if d.Log == nil {
		d.Log = logger.Nop()
	}
	if d.QueueBuffer == 0 {
		d.QueueBuffer = 64
	}
	cache := NewLabelCache(d.Log)
	svc := &Service{
		deps:    d,
		cache:   cache,
		revDNS:  NewReverseDNSMapWithWalk(func(func(uint32)) error { return nil }, nil, d.Log),
		metrics: NewMetrics(),
		sink:    nopSink{},
		queue:   make(chan []byte, d.QueueBuffer),
	}
	// Subscription teardown is owned by the topics: testTopics closes
	// both on cleanup, which drains the per-subscriber goroutines.
	svc.subscribeBus()
	return svc
}

func eventuallyTrue(timeout time.Duration, predicate func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return predicate()
}
