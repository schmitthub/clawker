# Repository Structure

Reference map of the clawker repo. Lazy-loaded from root `CLAUDE.md`.

```
├── api/
│   ├── admin/v1/              # AdminService protobuf (CLI → CP gRPC)
│   ├── agent/v1/              # AgentService protobuf (Register RPC for clawkerd→CP identity binding)
│   └── clawkerd/v1/           # ClawkerdService protobuf (Session RPC for CP→clawkerd dispatch)
├── cmd/
│   ├── clawker/               # Main CLI binary
│   ├── clawkercp/            # Control plane daemon (PID 1 in CP container)
│   ├── clawkerd/              # Thin agent-daemon entrypoint (Linux): os.Exit(clawkerd.Main())
│   ├── coredns-clawker/       # Custom CoreDNS with dnsbpf plugin (Linux)
│   └── gen-docs/              # CLI doc generator
├── clawkerd/                  # Per-container agent daemon (package clawkerd: listener/session/spawn/register/...); embed in clawkerd/embed (clawkerdembed.Binary)
├── controlplane/              # CP domains + infra (top-level; orchestrator lives in internal/controlplane)
│   ├── adminclient/           # CLI-side AdminService gRPC dial (mTLS + OAuth2)
│   ├── agent/                 # Unified agent surface: Dialer, Registry, Register handler, IdentityInterceptor, AgentEvent + state repo
│   ├── auth/                  # Typed agent/project identity primitives (ProjectSlug, AgentName)
│   ├── dockerevents/          # Docker events feeder + typed DockerEvent envelope + container state repo
│   ├── firewall/              # Firewall: Handler (13 RPCs), Stack, Envoy+CoreDNS, rules store
│   │   └── ebpf/              # eBPF loader + Manager
│   │       └── netlogger/     # Per-decision egress event emitter (ringbuf → enrich → OTLP)
│   ├── infracerts/            # CLI-root CA material (long-lived) for CP→clawkerd dial
│   ├── manager/               # Host-side CP lifecycle (EnsureRunning/Stop/CPRunning), embedded clawkercp + ebpf-manager binaries
│   ├── otel/                  # CP-side OTel logger provider factory (trusted-infra OTLP)
│   ├── otelcerts/             # Short-lived infra leaves for trusted OTLP (CP/Envoy/CoreDNS/netlogger)
│   ├── pubsub/                # Generic typed pub/sub pipe: Topic[T], Event[T] (dumb, stateless, panic-recovered delivery)
│   ├── server/                # AdminService composition (NewAdminServer) + authz + AgentService listener wiring
│   ├── subprocess/            # Ory subprocess lifecycle manager (start, health, crash, shutdown)
│   └── mocks/
├── internal/
│   ├── auth/                  # CLI-side auth material + CP dial helpers
│   ├── build/                 # Build-time metadata (leaf, stdlib only)
│   ├── bundler/               # Dockerfile generation, semver resolution, npm registry
│   ├── clawker/               # Main application lifecycle
│   ├── clawkerd/              # clawkerd daemon entrypoint package (Main/run in cmd.go); embed lives at top-level clawkerd/embed
│   ├── cmd/                   # Cobra commands
│   │   ├── factory/           # Factory constructor
│   │   ├── settings/          # Settings commands
│   │   ├── skill/             # Skill plugin management
│   │   └── project/edit/      # Project edit subcommand
│   ├── cmdutil/               # Factory struct, error types, arg validators
│   ├── config/                # Store[T] config engine (see internal/config/CLAUDE.md)
│   │   └── storeui/           # Domain adapters for storeui
│   ├── consts/                # Cross-package constants
│   ├── containerfs/           # Host Claude config preparation
│   ├── controlplane/          # CP daemon orchestrator (cmd.go): constructs pub/sub topics + domain state repos, wires handlers, runs startup/drain
│   ├── dnsbpf/                # CoreDNS plugin for BPF dns_cache
│   ├── docker/                # Docker middleware (wraps pkg/whail + bundler)
│   ├── docs/                  # CLI doc generation
│   ├── git/                   # Git operations, worktree management (leaf)
│   ├── hostproxy/             # Host proxy for container-to-host communication
│   ├── iostreams/             # I/O streams, colors, styles, spinners, layout
│   ├── keyring/               # Credential storage
│   ├── logger/                # Struct-based zerolog; Factory noun
│   ├── monitor/               # Monitoring stack templates
│   ├── project/               # Project registration
│   ├── prompter/              # Interactive prompts
│   ├── signals/               # OS signal utilities (leaf)
│   ├── socketbridge/          # SSH/GPG agent forwarding via muxrpc
│   ├── storage/               # Multi-file YAML store
│   ├── storeui/               # Generic TUI for Store[T] editing
│   ├── term/                  # Terminal capabilities (sole x/term gateway)
│   ├── testenv/               # Unified test environment (test-only)
│   ├── text/                  # Pure text utilities (leaf)
│   ├── tui/                   # BubbleTea TUI layer
│   ├── update/                # Background update checker
│   └── workspace/             # Bind vs Snapshot strategies
├── pkg/whail/                 # Reusable Docker engine with label-based isolation
├── test/
│   ├── adversarial/           # Adversarial C2 harness (Go server + SQLite, exposed via ngrok)
│   ├── e2e/                   # E2E integration tests
│   └── whail/                 # Whail BuildKit integration tests
└── scripts/                   # install.sh, install-hooks.sh, check-claude-freshness.sh, etc.
```
