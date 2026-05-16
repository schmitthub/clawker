# Infracerts Subpackage

Short-lived mTLS client certificate issuer for clawker infrastructure services. CP-side helper that signs leaves on demand from a CLI-provisioned intermediate CA.

## Trust chain

```
CLI root CA (auth.EnsureAuthMaterial — host-side, never enters CP)
  └── infra intermediate CA (CLI-minted, bind-mounted RO into CP)
        ├── envoy-otel-client    (minted at firewall.Stack.EnsureRunning, 1y TTL)
        ├── coredns-otel-client  (minted at firewall.Stack.EnsureRunning, 1y TTL)
        └── <future infra service>
```

The receiving party (today: `otel-collector`'s `otlp/infra` receiver) trusts only the CLI root CA. Leaves include the intermediate cert bundled in the PEM chain they present, so chain building succeeds without the relying party needing the intermediate in its truststore.

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
- **Leaves**: 1-year TTL (same shape as MITM domain certs). Re-issued on every `firewall.Stack.EnsureRunning` — container restart cadence is the rotation cadence. No renewal goroutine.

## Why an intermediate (not direct CLI-mints-all)

- Lifecycle ownership: CP owns the infra plane; CP owns the runtime-minted infra certs.
- Adding a new infra service is a CP-side change. CLI does not learn about it.
- Cert provisioning cadence matches container start cadence — no host-side cert files for services that exist only inside the running CP.

## Imports

- **Uses**: stdlib `crypto/*`, `encoding/pem`, `math/big`. No internal/ imports.
- **Imported by**: `cmd/clawker-cp` (loads the intermediate at startup; passes the Issuer into `firewall.NewStack`), `internal/controlplane/firewall` (consumes via the `InfraIssuer` interface; tests pass a fake).

## Degraded mode

`*Issuer` is passed as `firewall.InfraIssuer`, an interface. A nil issuer is tolerated by Stack — it skips the mTLS material bind-mounts; sibling Envoy/CoreDNS containers fall back to plaintext OTLP push (preserving existing behavior). The CP-side load failure emits `event=infra_issuer_unavailable` so operators can triage.
