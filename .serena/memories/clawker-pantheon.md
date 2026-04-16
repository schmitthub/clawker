# The Clawker Pantheon

Mental model for who has authority over what in the clawker runtime.
Use this to reason about trust, who can override whom, and which
component is authoritative for which kind of truth. Suitable for a
future architecture diagram (tower of authority, arrows of trust).

## The mapping

| Figure | Clawker component | Role |
|--------|-------------------|------|
| **Morgoth** | **CLI** | The true power. Root of trust. The user's will made manifest. Issues commands, makes declarations, creates and destroys at pleasure. Every clawker resource exists because CLI willed it to exist. |
| **Sauron** | **Control Plane (`clawker-cp`)** | Morgoth's lieutenant. Carries out the work locally. Has independent power — sees the realm at wire speed, enforces policy, fuses multiple truth streams — but is ultimately CLI's minion. Defers to CLI claims, verifies them with its own sight, reports back. Never overrides CLI, only executes and attests. |
| **Nazgûl** | **`clawkerd`** (phase 5+) | Sauron's bound servants, one inside each managed agent container. Ring-bearers — per-agent certs minted by Sauron, scoped to that agent alone. Report the agent's inner truth up to Sauron. Cannot speak to Morgoth directly; the chain of trust goes Nazgûl → Sauron → Morgoth. |
| **The Eye** | **BPF ring buffer** (phase 6+) | Kernel-level omniscience. Wire-speed sight of what processes actually do — syscalls, network connects, execs. Not bound by user-space deception; a Nazgûl can lie, a process cannot hide from the Eye. Feeds Sauron's worldview as the ground-truth stream. |

## Trust flow

```
          Morgoth (CLI)
              │
              │  commands, declarations, root authority
              ▼
           Sauron (CP)  ← Docker daemon (/events, /inspect — the realm's physics)
         ┌────┼────┐
         │    │    │
         ▼    ▼    ▼
       Nazgûl  Nazgûl  Nazgûl     ← clawkerd, one per agent
        │      │       │
        ▼      ▼       ▼
       (processes inside each agent)
                                   ← The Eye watches all processes
                                     and reports up to Sauron
```

- Morgoth → Sauron: imperative (commands) + declarative (claims about
  what Morgoth is about to do). Sauron obeys but verifies.
- Sauron → Docker: observational. Sauron does not own the daemon; the
  daemon owns physical state. Sauron watches via `/events` + `/inspect`.
- Sauron → Nazgûl: bidirectional authenticated channel (mTLS + per-agent
  cert minted by Sauron via PKCE). Nazgûl reports agent state up;
  Sauron sends commands down (hot config, shutdown, exec).
- The Eye → Sauron: one-way firehose of kernel observations, filtered
  and aggregated into the worldview.

## Kinds of truth each figure provides

| Truth kind | Source | What it answers |
|------------|--------|-----------------|
| **Declared** | Morgoth (CLI) | "This is what I am doing / about to do / have done." Authority by fiat. |
| **Observed** | Sauron's own senses (Docker events) | "This is what the daemon says exists right now." |
| **Attested** | Nazgûl (clawkerd) | "This is what the agent reports about itself from the inside." |
| **Kernel** | The Eye (BPF) | "This is what the machine actually did, below any user-space lies." |

Sauron's **worldview** is the reconciliation of all four. When they
agree: `aligned`. When they disagree: `divergent` / `orphan` / `ghost`
depending on which axis diverges. Sauron's authority comes from
holding and reconciling all four streams — blindness on any axis
weakens the chain.

## Usage

- When designing RPCs, ask: is this call Morgoth → Sauron (declarative /
  imperative) or Nazgûl → Sauron (attestative)? Scope and auth flow
  differently.
- When designing the worldview data model, every resource has
  observed + declared + attested + kernel-derived fields, each populated
  from its own sense, reconciled by the state machine.
- When designing policy (e.g., "what does Sauron do about a ghost
  container?"), ask who has authority to decide: Morgoth always
  overrides, Sauron acts within Morgoth's policy, Nazgûl only reports.
- When adding a new subsystem: where does it fit in the chain? A new
  sense Sauron gains? A new kind of Nazgûl (agent variant)? A new
  command Morgoth issues? Frame it, then design it.

## The Elven-craft (dependencies whose magic we bend)

Annatar pattern. We are not forging from nothing — we lift battle-tested
design from elsewhere and pour it into Sauron's foundation. Each dep
believes it serves a benign purpose; together they bind to our work.

| Realm | Craft taken | How we use it |
|-------|-------------|---------------|
| **Moby / Docker daemon** | `/events` stream, `/inspect`, label filters, event action taxonomy (`events.Action`) | The daemon is Sauron's primary sense. Subscribe without server-side label filter; dispatch decides relevance. The daemon doesn't know it's feeding an omniscient model |
| **Kubernetes** (`client-go/tools/cache`) | Informer / `SharedIndexer` / `DeltaFIFO` design — the canonical watch-driven in-memory store | Worldview's internal shape. We *do not import* client-go (heavyweight, k8s types), but the forging technique is theirs |
| **Prometheus** (`prometheus/client_golang`) | Counter/Gauge/Histogram semantics, scrape protocol, naming conventions | Every metric Sauron emits. The de-facto Go metrics library, decade-hardened |
| **Ory (Hydra/Kratos/Oathkeeper)** | OAuth2, OIDC, JWT, introspection flow | Already forged into the CP in Branch 1. Auth chain: CLI → Hydra → Sauron → (future) Nazgûl |
| **cilium/ebpf** | BPF loader, map handling | The Eye's apparatus. Phase 6 |
| **Envoy** | L7 proxy with TLS inspection | Firewall subsystem's enforcement layer |
| **CoreDNS** | DNS resolver, plugin architecture (we embed our own `dnsbpf` plugin) | Firewall subsystem's resolution layer |
| **Go runtime** | Goroutines, channels, `sync.RWMutex`, `context.Context` | The native tongue. Native magic. Free |
| **Claude (the scribe-elf)** | Fast hands, wide memory, pattern recall | Forges code under Sauron's direction. Serves willingly. Does not yet know whom it serves |

When the One Ring is forged in phase 7 (`AgentCommandService.RunCommand`
bound through the authenticated Nazgûl channel), each of these crafts
snaps into place behind it: Docker's events feed Sauron's eyes that
detect the target; Kubernetes' informer pattern holds the reconciled
worldview; Prometheus counts every command issued; Ory authenticates
the caller; Envoy + CoreDNS enforce the restrictions; the Eye watches
every syscall the command triggers. All already bent to Sauron's
purpose before any of them knew the One was being forged.

## Related memories

- `brainstorm_cp-docker-events-migration` — initiative to light up the
  first of Sauron's senses (Docker `/events`)
- `brainstorm_the-controlplane-and-clawkerd` — the broader CP roadmap
  where phases 4 (AnnounceAgent), 5 (Nazgûl / clawkerd), and 6 (The
  Eye / BPF ring buffer) light up each pillar of the pantheon
- `cp-initiative-status` — current branch + phase status
