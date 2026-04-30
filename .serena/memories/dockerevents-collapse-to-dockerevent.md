# dockerevents: collapse to single `DockerEvent` type

**Goal:** drop every per-action / per-resource Go type in `internal/controlplane/dockerevents` and publish a single `DockerEvent` envelope wrapping moby's `events.Message`. Subscribers filter on `ev.Type` + `ev.Action`. Drift-safe — method bodies derive from the embedded Message; no parallel state.

**Current (broken) state on branch `feat/clawkerd-commands` (commits `dc3a01a5`, `95d53f40`, `b8c7b0b7`):** dockerevents publishes 12 typed container events (`ContainerCreated/Started/Restarted/Paused/Unpaused/Died/Stopped/Killed/OOM/Destroyed/Renamed`) + 4 network events (`NetworkCreated/Connected/Disconnected/Destroyed`) plus base `ContainerEvent`/`NetworkEvent`. Each per-action type is a wrapper that adds nothing but a distinct `reflect.TypeOf` for routing. The user (correctly) called out that this is parallel vocabulary — moby already has the discriminator (`Type`+`Action`), and inventing Go types per action creates drift surface.

The right shape: one `DockerEvent { events.Message }` type. Methods on it are pure projections of the embedded Message — `EventName()` is `fmt.Sprintf("docker.%s.%s", Type, Action)`, `OccurredAt()` is `time.Unix(0, TimeNano)`, `MarshalZerologObject` dumps Type/Action/Actor.ID/Actor.Attributes/Scope verbatim. Drift impossible.

## The exact code (drop-in for `internal/controlplane/dockerevents/events.go`)

Replace the entire file with:

```go
// Package dockerevents subscribes to moby's container/network event
// stream and republishes it on the Overseer bus as a single typed
// envelope, DockerEvent, wrapping moby's events.Message verbatim.
//
// One topic. Subscribers receive every container + network event the
// daemon emits and filter on ev.Type + ev.Action. There is NO Go
// type per moby action — the action vocabulary lives where it
// belongs (moby's events.Action) and is filtered at the consumer
// boundary, not invented in our type system.
//
// Drift safety: the methods on DockerEvent are pure projections of
// the embedded events.Message. New moby actions render verbatim
// through EventName / MarshalZerologObject without code change. New
// event types (volume, image, plugin) likewise pass through if the
// feeder's stream filter is widened.
package dockerevents

import (
	"fmt"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/rs/zerolog"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
)

// DockerEvent is the bus envelope for any docker daemon event the
// feeder republishes. Embeds events.Message verbatim. Implements
// overseer.Event via three pure projections of the embedded fields —
// no parallel schema, no engine-key vs label flattening at the
// producer side. Subscribers reach through to Type, Action,
// Actor.ID, Actor.Attributes, TimeNano directly.
type DockerEvent struct {
	events.Message
}

// EventName renders the canonical "docker.<type>.<action>" string
// (e.g. "docker.container.start", "docker.network.connect"). Used
// by NewLoggerHook for the log-line message and the `event` field;
// also used by structured-log filters in Loki / Grafana.
func (e DockerEvent) EventName() string {
	return fmt.Sprintf("docker.%s.%s", e.Type, e.Action)
}

// OccurredAt converts moby's nanosecond timestamp to time.Time. Uses
// TimeNano for sub-second precision; e.Time is seconds-only.
func (e DockerEvent) OccurredAt() time.Time {
	return time.Unix(0, e.TimeNano)
}

// MarshalZerologObject dumps the moby Message as structured log
// fields. Actor.Attributes is folded out as one `actor_attr.<k>`
// field per entry so operators can label-filter on individual
// attribute keys in Loki without a JSON parser. Type and Action are
// flat fields so subscribers' Loki queries pin on them directly.
func (e DockerEvent) MarshalZerologObject(z *zerolog.Event) {
	z.Str("type", string(e.Type)).
		Str("action", string(e.Action)).
		Str("actor_id", e.Actor.ID).
		Str("scope", e.Scope)
	for k, v := range e.Actor.Attributes {
		z.Str("actor_attr."+k, v)
	}
}

// ApplyTo projects moby's container actions onto Overseer's
// ContainerView status enum. Network events have no State
// projection in v1 — Overseer doesn't track network edges in its
// worldview. Volume/image events also no-op (no consumer).
//
// The (Type, Action) → status switch lives here as the single
// place that does the worldview-level coarsening; the source events
// preserve full moby fidelity.
func (e DockerEvent) ApplyTo(s *overseer.State) {
	if e.Type != events.ContainerEventType {
		return
	}
	switch e.Action {
	case events.ActionStart, events.ActionRestart, events.ActionUnPause:
		view := s.Containers[e.Actor.ID]
		view.ID = e.Actor.ID
		if name := e.Actor.Attributes["name"]; name != "" {
			view.Name = name
		}
		view.Status = overseer.ContainerStatusRunning
		view.Labels = stripEngineKeys(e.Actor.Attributes,
			"image", "name", "exitCode", "signal", "oldName", "execDuration")
		view.UpdatedAt = e.OccurredAt()
		s.Containers[e.Actor.ID] = view

	case events.ActionDie, events.ActionStop, events.ActionKill, events.ActionOOM:
		view := s.Containers[e.Actor.ID]
		view.ID = e.Actor.ID
		view.Status = overseer.ContainerStatusStopped
		view.UpdatedAt = e.OccurredAt()
		s.Containers[e.Actor.ID] = view

	case events.ActionDestroy:
		// moby fires `destroy` for `docker rm` (verified vs live
		// stream — zero `container/remove` actions observed).
		// `events.ActionRemove` exists in the shared Action vocabulary
		// but is image-only (`docker rmi`) and never reaches this
		// switch for container events. ApplyTo is a projection, not
		// the wire vocabulary, so it MUST NOT branch on ActionRemove.
		delete(s.Containers, e.Actor.ID)

	case events.ActionRename:
		view := s.Containers[e.Actor.ID]
		view.ID = e.Actor.ID
		view.Name = e.Actor.Attributes["name"]
		view.UpdatedAt = e.OccurredAt()
		s.Containers[e.Actor.ID] = view

	// Created / Paused / Unpaused-as-pure-edge and any unrecognised
	// action: pure pub/sub, no State change.
	}
}

// stripEngineKeys returns a copy of attrs with the listed engine-set
// keys removed. Engine-set keys live alongside user labels in
// Actor.Attributes; State.ContainerView.Labels should hold only
// true labels.
func stripEngineKeys(attrs map[string]string, keys ...string) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	skip := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		skip[k] = struct{}{}
	}
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		if _, drop := skip[k]; drop {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
```

## Files to change

### 1. `internal/controlplane/dockerevents/events.go` — REPLACE entirely with the snippet above

Drop everything else. No `ContainerEvent`, no `NetworkEvent`, no per-action wrappers, no per-action `MarshalZerologObject` overrides, no per-action `ApplyTo`.

### 2. `internal/controlplane/dockerevents/dispatch.go`

Currently switches on `ev.Action` and publishes per-action types. Collapse to a single publish:

```go
func (f *Feeder) dispatchContainer(ev events.Message) {
	id := ev.Actor.ID
	if id == "" {
		return
	}

	managed := f.isManaged(ev.Actor.Attributes)
	if !managed && !f.containers[id] {
		return
	}

	// Update managed-set membership based on action; destroy/remove
	// drop, everything else mark-or-track. parseExitCode audit
	// retained for actions that carry exitCode so a moby contract
	// change still surfaces in logs.
	switch ev.Action {
	case events.ActionDestroy, events.ActionRemove:
		delete(f.containers, id)
	default:
		f.containers[id] = true
		if ev.Action == events.ActionDie || ev.Action == events.ActionStop {
			_ = f.parseExitCode(ev.Actor.Attributes["exitCode"], id)
		}
	}

	f.publishDockerEvent(DockerEvent{Message: ev}, id)
}

func (f *Feeder) dispatchNetwork(ctx context.Context, ev events.Message) {
	netID := ev.Actor.ID
	if netID == "" {
		return
	}

	switch ev.Action {
	case events.ActionCreate:
		f.tryNetworkInspect(ctx, netID)
	case events.ActionDestroy, events.ActionRemove:
		delete(f.networksNeedRecheck, netID)
		delete(f.networks, netID)
	case events.ActionConnect, events.ActionDisconnect:
		if f.networksNeedRecheck[netID] {
			f.tryNetworkInspect(ctx, netID)
		}
	}

	if !f.networks[netID] {
		return
	}
	// Connect/Disconnect with no managed container: drop the edge
	// (no container anchor for the worldview to attribute it to).
	if ev.Action == events.ActionConnect || ev.Action == events.ActionDisconnect {
		ctrID := ev.Actor.Attributes["container"]
		if ctrID == "" || !f.containers[ctrID] {
			return
		}
	}

	f.publishDockerEvent(DockerEvent{Message: ev}, ev.Actor.Attributes["container"])
}
```

Rename `publishContainerEvent` / `publishNetworkEvent` → single `publishDockerEvent(ev DockerEvent, ctxID string)` that takes a context-id (container or network) for the drop-log.

### 3. `internal/controlplane/dockerevents/reconcile.go`

Currently fabricates per-action types from `containerEventFromState`. Collapse:

```go
func (f *Feeder) reconcile(ctx context.Context) error {
	// ... (containers/networks list as today) ...

	for _, c := range containers.Items {
		f.containers[c.ID] = true
		action := containerActionFromState(c.State)
		if action == "" {
			continue // StateCreated, StateRestarting — no synthetic publish
		}
		envelope := DockerEvent{Message: events.Message{
			Type:     events.ContainerEventType,
			Action:   action,
			Actor:    events.Actor{ID: c.ID, Attributes: containerAttributesFromSummary(c)},
			Scope:    "local",
			Time:     now.Unix(),
			TimeNano: now.UnixNano(),
		}}
		f.publishDockerEvent(envelope, c.ID)
	}

	for _, c := range containers.Items {
		// ... network edges: same pattern, build a DockerEvent with
		//     Type=NetworkEventType, Action=ActionConnect, Actor.ID=netID,
		//     Actor.Attributes["container"]=c.ID
	}
}
```

`containerActionFromState` and `containerAttributesFromSummary` already exist in `dispatch.go` — leave them; reconcile reuses them.

`containerEventFromState` (returns `overseer.Event`) — DELETE. Reconcile builds the envelope inline.

### 4. `internal/controlplane/agentdial/subscribe.go`

Currently has 3 typed subscriptions (`ContainerStarted`, `ContainerRestarted`, `ContainerUnpaused`) with a generic `runningEvent` constraint and 3 goroutines. Collapse to 1 subscription with predicate:

```go
func Subscribe(ctx context.Context, dialer *Dialer, bus *overseer.Overseer, log *logger.Logger) func() {
	if log == nil {
		log = logger.Nop()
	}
	sub, ok := overseer.SubscribeFiltered(bus, "agentdial", func(ev dockerevents.DockerEvent) bool {
		if ev.Type != events.ContainerEventType {
			return false
		}
		switch ev.Action {
		case events.ActionStart, events.ActionRestart, events.ActionUnPause:
			return ev.Actor.Attributes[consts.LabelPurpose] == consts.PurposeAgent
		}
		return false
	})
	if !ok {
		log.Warn().Msg("agentdial: bus closed before subscribe; consumer not started")
		return func() {}
	}

	done := runConsumer(ctx, sub.C, dialer, log)
	return func() { sub.Unsubscribe(); <-done }
}
```

Drop `runningEvent` interface, drop `agentPurposeFilter` generic, drop `containerID` generic. `runConsumer` and `drainOnce` lose generic constraints — they take `<-chan dockerevents.DockerEvent`. The dialer is called with `dialer.DialAgent(ctx, ev.Actor.ID)`.

`import "github.com/moby/moby/api/types/events"` is required for the action constants.

### 5. `internal/controlplane/agentregistry/subscribe.go`

Currently subscribes to `ContainerDestroyed`. Replace with filtered DockerEvent subscription:

```go
func Subscribe(ctx context.Context, reg Registry, bus *overseer.Overseer, log *logger.Logger) func() {
	if log == nil {
		log = logger.Nop()
	}
	sub, ok := overseer.SubscribeFiltered(bus, "agentregistry", func(ev dockerevents.DockerEvent) bool {
		return ev.Type == events.ContainerEventType && ev.Action == events.ActionDestroy
	})
	if !ok {
		log.Warn().Msg("agentregistry: bus closed before subscribe; consumer not started")
		return func() {}
	}

	done := runConsumer(ctx, sub.C, reg, log)
	return func() { sub.Unsubscribe(); <-done }
}
```

`runConsumer` / `drainOnce` / `handleEvent` take `<-chan dockerevents.DockerEvent` (or single `dockerevents.DockerEvent` in handleEvent). EvictByContainerID is called with `ev.Actor.ID`.

`import "github.com/moby/moby/api/types/events"` required.

### 6. Tests

#### `internal/controlplane/dockerevents/dispatch_test.go`

Replace per-action subscription assertions with single-type subscription + Action filter. Pattern:

```go
sub, ok := overseer.Subscribe[DockerEvent](bus, "test")
require.True(t, ok)
defer sub.Unsubscribe()

f.dispatch(ctx, events.Message{Type: events.ContainerEventType, Action: events.ActionStart, ...})

ev := <-sub.C
require.Equal(t, events.ContainerEventType, ev.Type)
require.Equal(t, events.ActionStart, ev.Action)
```

Tests to keep:
- ActionCreate publishes a DockerEvent BUT does NOT flip Status to running (was the original bug — keep this assertion against the new ApplyTo)
- ActionRestart publishes an event (with Action=restart, distinguishable from ActionStart)
- ActionOOM publishes its own event (no collapse with die/stop)
- ActionDie carries exit_code in actor_attr.exitCode

`TestDispatch_NetworkConnectDisconnect_BothManaged`, `TestDispatch_NetworkCreate_PublishesForManaged`, `TestDispatch_NetworkDestroy_PublishesAndDropsManagedSet` — same shape: single `Subscribe[DockerEvent]`, assert Type=NetworkEventType + Action.

`TestStripEngineKeys` stays.

`TestLogEventReceived_*` stays (those test the receive-side audit log, not the bus shape).

#### `internal/controlplane/dockerevents/feeder_test.go`

`TestReconcile_PublishesNetworkConnected` — rename/keep, assert `Subscribe[DockerEvent]` receives Type=NetworkEventType, Action=ActionConnect, Actor.ID=net1, Actor.Attributes["container"]=ctr1.

`TestContainerEventFromState` — DELETE. The function it tests is gone.

#### `internal/controlplane/agentdial/subscribe_test.go`

`mkContainerEvent` helper stays but returns `dockerevents.DockerEvent` (not `dockerevents.ContainerEvent`):

```go
func mkContainerEvent(action events.Action, id string, labels map[string]string) dockerevents.DockerEvent {
	attrs := map[string]string{"name": id, "image": "alpine"}
	for k, v := range labels { attrs[k] = v }
	return dockerevents.DockerEvent{Message: events.Message{
		Type:     events.ContainerEventType,
		Action:   action,
		Actor:    events.Actor{ID: id, Attributes: attrs},
		TimeNano: time.Now().UnixNano(),
	}}
}
```

Drop `mkStarted`, `mkRestarted`, `mkUnpaused` per-action helpers — call sites use `mkContainerEvent(events.ActionStart, ...)` directly.

Keep all 5 tests:
- `TestSubscribe_DialsOnPurposeAgentContainerStarted`
- `TestSubscribe_DialsOnPurposeAgentContainerRestarted`
- `TestSubscribe_DialsOnPurposeAgentContainerUnpaused`
- `TestSubscribe_IgnoresNonAgentContainerStarted`
- `TestSubscribe_CancelStopsConsumer`
- `TestSubscribe_NilLogTolerated`

#### `internal/controlplane/agentregistry/subscribe_test.go`

`mkContainerEvent` helper returns `dockerevents.DockerEvent`. `mkDestroyed`, `mkStarted`, `mkDied` become wrappers on top of `mkContainerEvent(events.ActionDestroy, id)` / `mkContainerEvent(events.ActionStart, id)` / `mkContainerEvent(events.ActionDie, id)` — or just inline `mkContainerEvent` at every call site (simpler).

Subscribe-storm test (`TestSubscribe_PanicStormTerminatesAtThreshold`) — publishes are now of `DockerEvent` with Action=ActionDestroy. Update.

### 7. Docs

- `internal/controlplane/overseer/CLAUDE.md` — replace per-event-type list with: "dockerevents.DockerEvent (single envelope wrapping moby's events.Message). ApplyTo on DockerEvent projects (Type, Action) onto State.Containers status."
- `CLAUDE.md` (root) line 102 — replace dockerevents description: `# Docker events feeder + reconcile + single typed envelope ('DockerEvent') wrapping moby's events.Message verbatim. Subscribers filter on ev.Type + ev.Action. Drift-safe — no parallel Go vocabulary on top of moby's actions.`
- `.claude/docs/ARCHITECTURE.md`, `.claude/docs/KEY-CONCEPTS.md` — strike per-event-type lists; replace with single DockerEvent reference.
- `internal/controlplane/agentdial/CLAUDE.md`, `internal/controlplane/agentregistry/CLAUDE.md` — update event type references.

## Why this is the right shape

User repeatedly directed: "we are just taking docker container events and publishing them to overseer." `events.Message` IS the contract. Wrapping it in 16 per-action types reintroduces the parallel-vocabulary failure mode that started this whole refactor. One envelope, projections derived from the embedded fields, drift impossible.

Subscribers expressing intent at the Action level (predicate filter) is the same coupling shape they already have today — agentdial knows it cares about start/restart/unpause, agentregistry knows it cares about destroy. The Action vocabulary lives in `events.Action`, not in our type system.

## Validation

After the refactor:

```bash
go build ./...
go test ./internal/controlplane/...
make test
```

Manual sanity (CP container running):

```bash
docker create --label dev.clawker.purpose=agent --label dev.clawker.managed=true alpine sleep 1
# expect Loki: event="docker.container.create" type=container action=create
docker rm <id>
# expect Loki: event="docker.container.destroy" type=container action=destroy
# NO event="docker.container.start" between them
```

## Branch state

Work on `feat/clawkerd-commands`. Current head: `b8c7b0b7`. Three prior commits (`dc3a01a5`, `95d53f40`, `b8c7b0b7`) build up the wrong shape; this collapse commit replaces it. Do NOT revert — do a forward-fix commit `refactor(dockerevents): collapse to single DockerEvent envelope` so review history shows the wrong-shape → right-shape evolution.
