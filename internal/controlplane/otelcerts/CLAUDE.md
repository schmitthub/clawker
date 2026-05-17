# OTelCerts Subpackage

Mints and provisions short-lived mTLS client material for the trusted
OTLP/infra lane. CP-level helper; not part of the firewall package.

## Trust model

```
CLI root CA (provisioned by `clawker auth` — server-side trust anchor)
  └── infra intermediate CA (CLI-minted, bind-mounted RO into CP)
        ├── envoy-otel-client    (leaf, written to disk, bind-mounted into envoy)
        ├── coredns-otel-client  (leaf, written to disk, bind-mounted into coredns)
        └── cp-otel-client       (leaf, in-process via tls.Config)
```

The `otel-collector`'s `otlp/infra` receiver's `client_ca_file` is the
**infra intermediate CA**. Agent containers carry leaves signed
directly by the CLI root with no path through the intermediate, so
their handshake fails the receiver's chain validation. The CLI root CA
remains the server-side trust anchor used by clients to verify the
otel-collector's server cert.

This is the security boundary on the trusted forensic indices: agents
cannot reach `clawker-cp` / `clawker-envoy` / `clawker-coredns` even
when they hold a valid CLI-root-signed leaf, because their chain does
not pass through the infra intermediate.

## API

```go
type Issuer interface {
    MintClient(serviceName string, ttl time.Duration) (chainPEM, keyPEM []byte, err error)
}

type Service struct { /* unexported */ }

func New(issuer Issuer, destDir string, rootCABytes []byte, log *logger.Logger) (*Service, error)

// SetLogger replaces the Service's logger. Wiring exists because
// main.go constructs the Service before logger.New (the *tls.Config
// it returns is consumed by logger.New itself). Closures read s.log
// lazily at handshake time, so post-construction SetLogger is safe.
func (s *Service) SetLogger(log *logger.Logger)

// EnsureClient writes <destDir>/<svc>/{client.pem,client.key,ca.pem}
// atomically (tmp + rename). Used by sibling containers
// (envoy, coredns) via bind-mount.
//
// `svc` is the short directory tag (e.g. "envoy", "coredns"); the
// leaf CN passed to the issuer is `<svc>-otel-client` internally.
// The suffix is load-bearing for the otel-collector's authz config
// — pinned by TestEnsureClient_WritesAllThreeFiles.
func (s *Service) EnsureClient(svc string) (certPath, keyPath, caPath string, err error)

// LoadTLSConfig returns a *tls.Config with a GetClientCertificate
// hook that re-mints per TLS handshake. Used in-process by the CP
// OTLP exporter — leaf material never lands on disk.
//
// Only callable by code running inside the CP process: the closure
// holds the Service reference, which is not transportable across
// process boundaries. Sibling containers (envoy, coredns) MUST use
// EnsureClient instead and bind-mount the disk material.
func (s *Service) LoadTLSConfig(svc string) (*tls.Config, error)
```

`*infracerts.Issuer` satisfies `Issuer`. Wiring in
`cmd/clawker-cp/main.go` passes the concrete `*infracerts.Issuer` once
it's loaded.

`New` returns an error for nil issuer, empty destDir, empty rootCA
bytes, or rootCA bytes that contain no parseable PEM certificates
(parsed once at construction; failure surfaces at startup, not first
handshake).

### Degraded-mode signaling

Degraded mode propagates via **plain interface-typed nil**, NOT a
typed-nil `(*Service)(nil)`. Callers in `cmd/clawker-cp/main.go`
declare:

```go
var (
    otelCertsSvc *otelcerts.Service          // concrete handle for SetLogger
    otelCerts    fwhandler.OtelCertProvisioner // interface, plain nil on failure
)
```

and only assign into `otelCerts` on the success path. Boxing a typed-
nil `*Service` into the interface would pass downstream `s.otelCerts
== nil` checks while still dispatching `EnsureClient`/`LoadTLSConfig`
on a nil receiver — turning the intended degraded mode into a panic
that strands eBPF (see `internal/controlplane/CLAUDE.md`, "CP crashing
is a security incident"). The two-variable pattern is enforced by an
inline comment in `main.go`.

On the in-process exporter side, the CP wiring sets `OtelOptions =
nil` (entire struct, not just TLSConfig) on any failure so the logger
stays file+stderr only — never half-init mTLS with no creds, never
plaintext-fall-back to the untrusted lane.

## File permissions (Docker bind-mount UID traversal)

- Per-svc dir: `0o755`
- All three files: `0o644`

The directory mode is load-bearing for non-root in-container readers
(Envoy distroless runs UID 101). Docker bind-mounts preserve host
inode perms; a `0o700` dir blocks traversal even when the file would
be readable. `0o644` on the key is constrained by the same UID rule.
See commit 07b73371.

## Lifetime / rotation

- **Leaves**: 1-year TTL (matches MITM domain certs).
- **Disk path (EnsureClient)**: re-issued on every
  `firewall.Stack.EnsureRunning` / `Reload`. Container restart cadence
  is the rotation cadence; no renewal goroutine.
- **In-process path (LoadTLSConfig)**: re-issued on every TLS
  handshake via `GetClientCertificate`. Matches the CoreDNS plugin
  rotation pattern.
- **Pair-check on every mint**: `tls.X509KeyPair` round-trip rejects
  malformed/mismatched output so a buggy issuer can't half-overwrite a
  prior-good pair into a broken state.

## v1 limitation: issuer key rotation

`LoadTLSConfig`'s `GetClientCertificate` closure holds the `*Service`
reference, which holds the `Issuer` reference loaded at CP startup. A
`clawker auth rotate` that replaces the intermediate's private key is
NOT picked up at runtime — the closure keeps signing with the stale
signer until CP restart. Document this; do not fix in v1.

## Health gating vs. exporter buffering

`infraCertsReady`-style health gating from the firewall stack does not
apply to the in-process consumer. The OTLP gRPC exporter buffers and
retries internally per `logger/CLAUDE.md` "OTEL Resilience", so an
isolated mint failure produces a transient buffered-and-dropped state
rather than a hard CP outage.

A per-handshake mint failure is otherwise invisible (the OTel SDK
routes it as a generic "otel sdk error" line). `LoadTLSConfig` emits
`event=otelcerts_handshake_mint_failed` with `svc` field so operators
can triage trusted-lane handshake failures from the structured log
surface. Pair-check failure (issuer returns mismatched cert/key)
emits `event=otelcerts_handshake_pair_invalid`. Both require
`SetLogger` to have been called post-construction; `main.go` wires
this immediately after `logger.New`.

## Imports

- **Uses**: stdlib `crypto/*`, `internal/logger`. No internal/
  controlplane/firewall imports (this is the layering boundary; see
  `feedback_no_layering_violations.md`).
- **Imported by**: `cmd/clawker-cp` (constructs the Service; wires it
  into firewall.NewStack AND uses LoadTLSConfig for the CP's own OTel
  exporter), `internal/controlplane/firewall` (consumes via the
  package-local `OtelCertProvisioner` interface; tests pass a fake).

## Why this lives outside the firewall package

The previous home (`firewall/stack.go::ensureInfraClientCerts`) was a
layering violation: the firewall is one of three consumers of OTel
client material, not the owner. The CP's own OTLP exporter is the
third consumer; placing the mint logic in the firewall package forces
that consumer to either reach across packages (untenable) or
reimplement the mint+validate+rotate logic (worse).

See `feedback_no_layering_violations.md` for the broader rule.
