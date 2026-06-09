# Infracerts Subpackage

Short-lived mTLS client certificate issuer for clawker infrastructure services. CP-side helper that signs leaves on demand from a CLI-provisioned intermediate CA.

## Trust chain

```
CLI root CA (auth.EnsureAuthMaterial — host-side, never enters CP)
  └── infra intermediate CA (CLI-minted, bind-mounted RO into CP)
        ├── envoy-otel-client    (minted at firewall.Stack.EnsureRunning/Reload, 1y TTL)
        ├── coredns-otel-client  (minted at firewall.Stack.EnsureRunning/Reload, 1y TTL)
        ├── cp-otel-client       (minted via otelcerts.Service.LoadTLSConfig, per-handshake, in-process)
        └── <future infra service>
```

The receiving party (today: `otel-collector`'s `otlp/infra` receiver) pins the **infra intermediate CA** as its `client_ca_file` — NOT the CLI root. `monitor init` resolves the bind-mount source from `consts.AuthInfraCACertPath()`. This is the agent-spoofing boundary: agent containers carry leaves signed directly by the CLI root (`auth.MintAgentCert`), so their chain does not validate against the intermediate even though it does validate against the root. Anchoring trust at the intermediate locks the trusted lane to leaves this subpackage minted, which is what stops agents from forging `service.name=clawker-cp`/`envoy`/`coredns` records onto the trusted forensic indices. Leaves still bundle the intermediate in their presented chain so the receiver completes validation in one hop without holding the intermediate in any additional truststore.

## API

```go
type Issuer struct { /* unexported */ }

func Load(certPath, keyPath string) (*Issuer, error)
func (i *Issuer) MintClient(serviceName string, ttl time.Duration) (chainPEM, keyPEM []byte, err error)
func (i *Issuer) IntermediatePEM() []byte
```

`Load` enforces `BasicConstraints.IsCA=true` on the intermediate so a misprovisioned leaf can't sneak in as a signer.

`MintClient` returns the leaf cert PEM followed by the intermediate cert PEM in one buffer (`chainPEM`), and the leaf private key separately (`keyPEM`). The leaf carries `KeyUsage=DigitalSignature`, `ExtKeyUsage=ClientAuth`, `CN=serviceName`, and `DNSNames=[serviceName]`.

## Lifetime

- **Intermediate**: provisioned once by the CLI at `auth.EnsureAuthMaterial` time (5-year TTL). Rotates when the CLI root rotates (`clawker auth rotate --force`).
- **Leaves**: 1-year TTL (same shape as MITM domain certs). Re-issued on every `firewall.Stack.EnsureRunning` / `Reload` — container restart cadence is the rotation cadence. No renewal goroutine.

## Why an intermediate (not direct CLI-mints-all)

- Lifecycle ownership: CP owns the infra plane; CP owns the runtime-minted infra certs.
- Adding a new infra service is a CP-side change. CLI does not learn about it.
- Cert provisioning cadence matches container start cadence — no host-side cert files for services that exist only inside the running CP.

## Imports

- **Uses**: stdlib `crypto/*`, `encoding/pem`, `math/big`. No internal/ imports.
- **Imported by**: `cmd/clawker-cp` (loads the intermediate at startup and hands it to `otelcerts.New`, which is then passed to `firewall.NewStack` as a `firewall.OtelCertProvisioner`), `internal/controlplane/otelcerts` (wraps `*Issuer` behind its `Issuer` interface and mints per-service material on demand). The firewall package does NOT import `infracerts` directly — it only sees the `OtelCertProvisioner` interface defined in `firewall/stack.go`. (`internal/auth` provisions the intermediate CA itself using x509 directly — it does not import this package.)

## Degraded mode

`*Issuer` reaches the firewall only indirectly: `cmd/clawker-cp` wraps it with `*otelcerts.Service` and hands the wrapper to `firewall.NewStack` behind the `firewall.OtelCertProvisioner` interface. A nil provisioner is tolerated by Stack — it skips the mTLS material bind-mounts and Envoy/CoreDNS run in **stdout-only degraded mode**: Envoy omits the OTel access-log sink AND the `otel_collector_als` cluster (sender-side gate on `als.MTLS` in `buildHTTPAccessLog` / `buildTCPAccessLog` / `installOtelALSCluster`); CoreDNS sees no `CLAWKER_COREDNS_OTEL_ENDPOINT` and installs `noopEmitter`. There is no plaintext OTLP fallback — infra services must never cross into the untrusted `otel-collector:4317` receiver lane reserved for agent containers. Stdout JSON sinks remain wired for `docker logs` triage; the OpenSearch ingestion path stays cold. The CP-side load failure emits `event=otelcerts_unavailable` with `step=infracerts_load` so operators can triage.
