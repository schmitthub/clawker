# Repository Structure

Reference map of the clawker repo. Lazy-loaded from root `CLAUDE.md`.

```
├── api/
│   ├── admin/v1/              # AdminService protobuf (CLI → CP gRPC)
│   └── agent/v1/              # AgentService protobuf (Register RPC for clawkerd→CP identity binding)
├── cmd/
│   ├── clawker/               # Main CLI binary
│   ├── clawker-cp/            # Control plane daemon (PID 1 in CP container)
│   ├── clawker-generate/      # Code generation helper
│   ├── clawkerd/              # Per-container agent daemon (Linux)
│   ├── coredns-clawker/       # Custom CoreDNS with dnsbpf plugin (Linux)
│   └── gen-docs/              # CLI doc generator
├── internal/
│   ├── auth/                  # CLI-side auth material + CP dial helpers
│   ├── build/                 # Build-time metadata (leaf, stdlib only)
│   ├── bundler/               # Dockerfile generation, semver resolution, npm registry
│   ├── clawker/               # Main application lifecycle
│   ├── clawkerd/              # Embedded clawkerd binary (go:embed)
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
│   ├── controlplane/          # CP daemon: Ory auth, AdminService, agent watcher
│   │   ├── agent/             # Unified agent surface: Dialer, Registry, Register handler, IdentityInterceptor, events
│   │   ├── adminclient/       # CLI-side AdminService gRPC dial (mTLS + OAuth2)
│   │   ├── cpboot/            # Host-side CP lifecycle (EnsureRunning/Stop), embedded clawker-cp + ebpf-manager binaries
│   │   ├── dockerevents/      # Docker events feeder + typed envelope
│   │   ├── firewall/          # Firewall: Handler (13 RPCs), Stack, Envoy+CoreDNS, eBPF
│   │   │   └── ebpf/          # eBPF loader + Manager
│   │   │       └── netlogger/ # Per-decision egress event emitter (ringbuf → enrich → OTLP)
│   │   ├── infracerts/        # CLI-root CA material (long-lived) for CP→clawkerd dial
│   │   ├── otelcerts/         # Short-lived infra leaves for trusted OTLP (CP/Envoy/CoreDNS/netlogger)
│   │   ├── overseer/          # Typed event bus + worldview state
│   │   └── mocks/
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
│   ├── e2e/                   # E2E integration tests
│   └── whail/                 # Whail BuildKit integration tests
├── scripts/                   # install.sh, install-hooks.sh, check-claude-freshness.sh, etc.
└── templates/                 # clawker.yaml scaffolding
```
