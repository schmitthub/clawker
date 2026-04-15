# Firewall Egress Rule Reconciler — PRD

## Problem

The CLI allows users to add, update, and delete egress rules in flight via
`clawker firewall {add,remove,update}`. Each mutation requires regenerating
Envoy and CoreDNS config files and restarting both containers. When multiple
mutations arrive in rapid succession, restarts collide — the stack is
mid-restart when the next regenerate/restart cycle fires, causing failures.

Additionally, a teardown signal (`clawker firewall down`) can race with
in-flight reconciles, potentially skipping security-relevant rule mutations.

## Solution

A single-goroutine FIFO action queue inside the CP that processes egress
rule mutations and teardown requests in order. Consecutive identical actions
are coalesced (e.g. three rapid-fire rule changes produce one reconcile
cycle), but different action types always execute in the order they were
received.

## Scope

### In Scope

- Coalescing consecutive egress rule mutations into minimal restart cycles.
- FIFO ordering between different action types (reconcile before teardown).
- Regenerating `envoy.yaml` and `Corefile` from `egress-rules.yaml`.
- Restarting Envoy and CoreDNS containers after config regeneration.
- Tearing down the firewall stack and clearing eBPF pins.
- Cleaning up generated config files on teardown.

### Out of Scope

- The egress rule store itself (already exists, handles concurrent writes).
- The config generation logic (already exists).
- Generic/reusable queue abstraction (extract later if needed).
- CLI-side confirmation that rules are applied (respond ACCEPTED on write).
- Rollback on failed restart.

## Queue Semantics

### FIFO with Same-Type Coalescing

The queue processes actions in the order received. When an action is
dequeued, consecutive actions of the same type are drained and coalesced
into a single execution. When a different action type is encountered, the
current action executes and the new action type becomes the current action.

### Action Types

| Action    | Trigger                        | Effect                                     |
|-----------|--------------------------------|--------------------------------------------|
| Reconcile | Any egress rule CRUD operation | Read store, regenerate configs, restart stack |
| Teardown  | `clawker firewall down`        | Stop stack, clear eBPF pins, remove configs  |

### Coalescing Behavior

| Queue Contents     | Execution                          |
|--------------------|------------------------------------|
| [R]                | 1 reconcile                        |
| [R, R, R]          | 1 reconcile                        |
| [R, R, T]          | 1 reconcile, 1 teardown            |
| [R, R, R, T, R]    | 1 reconcile, 1 teardown            |
| [T]                | 1 teardown                         |
| [R, T, R]          | 1 reconcile, 1 teardown            |

Teardown terminates the reconciler loop. Any actions queued after a
teardown are never processed. This is correct because the stack no longer
exists.

### Why FIFO, Not Priority

This is an ephemeral development system. Users expect mutations to take
effect in order. A user who removes an untrusted domain and then shuts
down the firewall expects that domain to be gone from running config
before the stack shuts down. Priority-based resolution that skips
reconciles in favor of teardown creates silent security drift — the
removed rule never takes effect in the running config, and stale
generated config files persist on the bind mount.

The cost of a redundant reconcile cycle before teardown is ~200ms of
container restart that is about to be torn down anyway. The cost of
skipping it is silent security drift in a system whose entire purpose
is egress control.

## Write Path

1. gRPC handler receives `AddRule`, `RemoveRule`, or `UpdateRule`.
2. Handler writes to the egress rule store (existing, concurrency-safe).
3. Handler calls `reconciler.Notify()`.
4. Handler returns `ACCEPTED` to CLI immediately.

`ACCEPTED` means the rule is persisted. It does not mean the rule is
active in the running stack. The reconciler will apply it asynchronously.

## Reconcile Path

1. Goroutine dequeues a `Reconcile` action.
2. Drains any consecutive `Reconcile` actions (coalesces).
3. Reads current rule set from store.
4. Regenerates `envoy.yaml` and `Corefile`.
5. Restarts Envoy and CoreDNS containers.
6. Returns to queue — if actions arrived during steps 3–5, processes next.

### Timing Example

```
t=0ms   CLI: firewall add example.com    → store write → Notify(Reconcile)
t=5ms   CLI: firewall add foo.dev        → store write → Notify(Reconcile)
t=10ms  CLI: firewall remove bar.io      → store write → Notify(Reconcile)
t=11ms  Reconciler wakes, drains 2 extra Reconcile signals
t=12ms  Reads store (has example.com, foo.dev, no bar.io)
t=15ms  Regenerates envoy.yaml + Corefile
t=80ms  Restarts Envoy + CoreDNS
t=80ms  No pending actions → blocks
Result: 3 mutations, 1 restart
```

### Teardown-After-Mutation Example

```
t=0ms   CLI: firewall remove evil.com    → store write → Notify(Reconcile)
t=50ms  CLI: firewall down               → Notify(Teardown)
t=51ms  Reconciler wakes, sees Reconcile
t=52ms  Drains: next action is Teardown (different type), stops draining
t=53ms  Reconcile executes: regenerates configs without evil.com, restarts
t=120ms Teardown executes: stops stack, clears eBPF, removes config files
t=121ms Loop exits
Result: evil.com removed from running config before stack destroyed
```

## Teardown Path

1. Goroutine dequeues a `Teardown` action.
2. Drains any consecutive `Teardown` actions (coalesces, unlikely but safe).
3. Stops Envoy and CoreDNS containers.
4. Clears all eBPF pins.
5. Removes generated config files (`envoy.yaml`, `Corefile`).
6. Loop exits — reconciler is dead.

A new `EgressReconciler` instance is created if the environment is
restarted. The init sequence reads the egress rule store and generates
fresh configs.

## API Surface

### gRPC (ControlPlane service)

```protobuf
rpc AddEgressRule(AddEgressRuleRequest) returns (EgressRuleResponse);
rpc RemoveEgressRule(RemoveEgressRuleRequest) returns (EgressRuleResponse);
rpc UpdateEgressRule(UpdateEgressRuleRequest) returns (EgressRuleResponse);
rpc ListEgressRules(ListEgressRulesRequest) returns (ListEgressRulesResponse);
rpc FirewallDown(FirewallDownRequest) returns (FirewallDownResponse);
```

`ListEgressRules` is a pure read — does not trigger reconciliation.

### Internal (Go)

```go
type EgressReconciler struct { ... }

func NewEgressReconciler(store, generator, stack, ebpf, logger) *EgressReconciler
func (r *EgressReconciler) Notify()          // queues Reconcile action
func (r *EgressReconciler) NotifyTeardown()  // queues Teardown action
func (r *EgressReconciler) Close()           // graceful shutdown, blocks until done
```

## Error Handling

| Failure                  | Behavior                                         |
|--------------------------|--------------------------------------------------|
| Store read fails         | Log error, continue loop (next Notify retries)   |
| Config generation fails  | Log error, continue loop                         |
| Envoy restart fails      | Log error, continue loop                         |
| CoreDNS restart fails    | Log error, continue loop                         |
| Stack stop fails         | Log error, still attempt eBPF clear and config removal |
| eBPF clear fails         | Log error, still attempt config removal          |

All reconcile failures are recoverable by the next cycle. Teardown
failures are best-effort — each step is attempted regardless of prior
step failures.

## Shutdown

`Close()` closes the action channel, causing the goroutine to exit after
completing any in-flight action. `Close()` blocks until the goroutine has
exited. Called during CP graceful shutdown.

If teardown has not been explicitly requested, `Close()` does not tear
down the firewall stack — it only stops the reconciler. This allows the
CP to restart without disrupting a running firewall.

## Testing

- **Coalescing**: mock store, generator, stack. Send N `Reconcile` signals,
  assert generator and stack called once with final rule set.
- **FIFO ordering**: send `Reconcile` then `Teardown`, assert reconcile
  runs before teardown.
- **Teardown terminates**: send `Teardown` then `Reconcile`, assert only
  teardown runs.
- **Mid-reconcile mutation**: mock stack restart with a sleep, send
  additional `Reconcile` during sleep, assert second cycle runs with
  updated state.
- **Race**: send signals from multiple goroutines, run with `-race`,
  assert no data races.

## Future Considerations

- If a second queue emerges with identical FIFO + coalescing mechanics,
  extract the queue primitive into a shared internal package.
- If CLI needs confirmation that rules are live (not just accepted),
  publish a `firewall.converged` event to the broker after successful
  reconcile. CLI subscribes via `WatchEnv`.
- Rollback: if reconcile fails, restore last known good configs. Not
  needed for local dev — user can re-run the command.
