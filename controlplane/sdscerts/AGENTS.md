# sdscerts — dedicated Envoy→CP SDS client identity

Provisions the mTLS client material Envoy uses to dial the CP's on-demand
certificate SDS server (`controlplane/firewall/sds_server.go`). One leaf, one
lane: CN + DNS SAN = `consts.EnvoySDSClientName` (`envoy-sds-client`), signed
by the infra intermediate CA, written to
`consts.SDSClientsDir()/envoy/{client.pem,client.key,ca.pem}` (host twin:
`consts.HostFirewallSDSCertsDir()`, bind-mounted RO at `/etc/envoy/sds-tls`).

## Why not otelcerts

Both packages mint client leaves off the same infra intermediate, but the SDS
lane hands out CA-signed MITM certificates — a far more sensitive service than
log ingestion. The lanes are deliberately separate on every axis:

- **Identity**: the CP's SDS listener pins the `envoy-sds-client` SAN
  (`requireSDSClientSAN` in `internal/controlplane/cmd.go`, run from
  `tls.Config.VerifyConnection` so resumed sessions are covered too). The
  telemetry lane's `envoy-otel-client` leaf — valid under the same
  intermediate — is refused there by design.
- **Material + provisioning**: own directory (`sds-clients/`), own mount
  (`/etc/envoy/sds-tls`), own provisioner. A telemetry provisioning change
  cannot silently disable per-SNI minting.
- **Readiness gate**: the firewall stack's `sdsCertsReady` flag (and the
  `dev.clawker.firewall.sds_certs_ready` drift label) is independent of
  `infraCertsReady`.

## API

```go
type FileOwner struct { UID, GID int }
func New(issuer *infracerts.Issuer, destDir string, rootCABytes []byte, owner FileOwner) (*Service, error)
func (s *Service) EnsureEnvoyClient() (certPath, keyPath, caPath string, err error)
func NewCPProvisioner() (*Service, error) // CP wiring: infracerts.Load + SDSClientsDir + root CA
```

`*Service` satisfies `firewall.SDSCertProvisioner`. LANDMINE: box the concrete
return into the interface ONLY on the success arm — a typed-nil `*Service` in
the interface passes the stack's nil-guard and panics on dispatch (same rule
as `otelcerts.NewCPProvisioner`).

Degraded mode: construction or mint failure → `event=sds_certs_unavailable` /
`event=sds_client_certs_unavailable`, `sdsCertsReady` stays false, wildcard
MITM chains keep their static `[apex, *.apex]` certs (multi-label subdomains
fail client-side hostname verification, the pre-SDS behavior). CP stays up.

CP keeps ownership of the client directory. Its group is `consts.EnvoyGID`,
with mode `0750`. The group can read and traverse the directory; other users
have no access. Existing directory permissions are restricted on each call.

The certificate, private key, and CA files use mode `0600`, with ownership
set to `consts.EnvoyUID` and `consts.EnvoyGID`. Each temporary file receives
these permissions and ownership before atomic rename publishes it. A failure
returns an error and removes the temporary file. There is no fallback to
broader permissions.

The firewall stack sets the Envoy process UID and GID from these same constants
and mounts the client directory read-only. CP checks the certificate and key
pair before writing any file. E2E tests must verify access and rotation with
the actual CP and Envoy processes; file mode assertions alone do not prove it.

## Used by

`internal/controlplane/cmd.go` (`run()` constructs it, `buildEnforcement`
threads it into `firewall.NewStack`); the stack dispatches
`EnsureEnvoyClient` from `ensureConfigs` on every reload.
