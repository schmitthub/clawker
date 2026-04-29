# Task 08 — cmd/clawkerd/listener: EKU assertion, Session entry audit + tests

**Status**: pending
**Claimed by**: —
**Blocks**: —
**Blocked by**: none
**Parallel-safe**: yes (no other task touches `cmd/clawkerd/listener.go`)

## Findings covered

- **C5** — `cmd/clawkerd/listener.go:86` — server cert lacks ServerAuth EKU verification awareness. `internal/auth/agent_cert.go` was extended to add `ExtKeyUsageServerAuth`, but `pinPeerCNToCP` only validates CN. Go's TLS stack enforces client EKU when `ClientAuth=RequireAndVerifyClientCert` so mostly defended, but next refactor could drop ServerAuth and break CP→clawkerd dials.
- **S12** — `cmd/clawkerd/listener.go:122-124` — successful (CN=ContainerCP) Session call leaves no audit trail until first Command. TLS layer pins peer CN but handler runs no logging at gRPC entry.
- **T4** — `cmd/clawkerd/listener.go` Session server entry point has zero tests. No verification that unauthenticated/missing-CP-identity stream is rejected, or gRPC error mapping is correct.

## Decisions

1. **C5**: Add comment in listener.go around the cert config cross-referencing dual-EKU rationale in `internal/auth/agent_cert.go`. Plus add explicit ServerAuth EKU assertion in `pinPeerCNToCP` — assert peer cert.ExtKeyUsage contains ClientAuth (defensive: even if Go's TLS stack already enforces it, the assertion documents the dependency at the call site).

   Wait — re-read carefully. `pinPeerCNToCP` runs server-side on clawkerd, validating the CP's peer cert. The CP-side cert needs `ExtKeyUsageClientAuth` (it's the client). So the assertion to add is: peer cert (CP) must carry `ClientAuth`. The dual-EKU doc cross-ref points to why the agent leaf has BOTH ClientAuth and ServerAuth (clawkerd uses the same cert for outbound gRPC to CP AND inbound listener for CP→clawkerd dials).

2. **S12**: Add Info log with peer CN + thumbprint at runSession entry. Audit trail for every Session start.
3. **T4**: Add `cmd/clawkerd/listener_test.go` with in-process gRPC server (`bufconn`) + bad-CN client → expect `codes.Unauthenticated` (or whatever the actual rejection code is — verify in current code).

## Affected files

| File | Change |
|------|--------|
| `cmd/clawkerd/listener.go` | C5 + S12. Comment around L86 + EKU assertion in `pinPeerCNToCP`. Info audit log at `runSession` entry (L122-124 area). |
| `cmd/clawkerd/listener_test.go` | NEW — bufconn-based test for CN-pin and EKU rejection. |
| `cmd/clawkerd/CLAUDE.md` (if it exists) | Document the dual-EKU rationale + Session-entry audit log invariant. |

## Implementation plan

1. **Read `cmd/clawkerd/listener.go`** — note the TLS config setup, `pinPeerCNToCP` impl, and `runSession` signature.
2. **Read `internal/auth/agent_cert.go`** — confirm dual-EKU is present (`ExtKeyUsageClientAuth + ExtKeyUsageServerAuth`).
3. **Add comment + EKU assertion** in `pinPeerCNToCP`:
   ```go
   func pinPeerCNToCP(...) error {
       // ... existing CN check ...

       // Defense in depth: even though tls.Config.ClientAuth=RequireAndVerifyClientCert
       // enforces ClientAuth EKU at the TLS layer, assert it explicitly here so a future
       // refactor that loosens TLS config (or runs without verified client cert in tests)
       // still fails closed at the application layer.
       //
       // The agent leaf cert carries BOTH ClientAuth (for clawkerd→CP outbound) and
       // ServerAuth (for CP→clawkerd inbound, which this listener serves). See
       // internal/auth/agent_cert.go for the dual-EKU rationale.
       hasClientAuth := false
       for _, eku := range peerCert.ExtKeyUsage {
           if eku == x509.ExtKeyUsageClientAuth {
               hasClientAuth = true
               break
           }
       }
       if !hasClientAuth {
           return status.Error(codes.Unauthenticated, "peer cert missing ClientAuth EKU")
       }

       return nil
   }
   ```

4. **Add audit log at runSession entry** (S12):
   ```go
   func (s *clawkerdServer) runSession(stream clawkerdv1.ClawkerdService_SessionServer) error {
       peerCert, err := extractPeerCert(stream.Context())
       if err != nil { /* unauthenticated */ }
       thumbprint := sha256.Sum256(peerCert.Raw)

       s.log.Info().
           Str("peer_cn", peerCert.Subject.CommonName).
           Str("peer_thumbprint", hex.EncodeToString(thumbprint[:])).
           Str("event", "session_started").
           Msg("clawkerd: Session started")

       defer func() {
           s.log.Info().
               Str("peer_cn", peerCert.Subject.CommonName).
               Str("event", "session_ended").
               Dur("duration", time.Since(startedAt)).
               Msg("clawkerd: Session ended")
       }()

       // ... existing session loop ...
   }
   ```

5. **Write `listener_test.go`** with bufconn pattern:
   ```go
   func TestListener_RejectsBadCN(t *testing.T) {
       lis := bufconn.Listen(1 << 20)
       srv := newTestServer(t, lis)  // wires the real listener with a CP-CA root
       defer srv.Stop()

       client := newTestClient(t, lis, withWrongCN())  // client cert with CN=evil
       _, err := client.Session(ctx, ...)
       require.Error(t, err)
       require.Equal(t, codes.Unauthenticated, status.Code(err))
   }

   func TestListener_RejectsMissingClientAuthEKU(t *testing.T) {
       // Mint a cert with ServerAuth-only EKU (no ClientAuth).
       // Verify pinPeerCNToCP rejects.
   }

   func TestListener_AcceptsValidCN(t *testing.T) {
       client := newTestClient(t, lis, withCN(consts.ContainerCPCN))
       stream, err := client.Session(ctx, ...)
       require.NoError(t, err)
       // verify Welcome received
   }

   func TestRunSession_LogsAuditOnEntry(t *testing.T) {
       // Capture log output via test logger
       // Verify "session_started" event with peer_cn + peer_thumbprint
   }
   ```

## Test requirements

- `TestListener_RejectsBadCN` — bufconn + client cert with wrong CN → Unauthenticated.
- `TestListener_RejectsMissingClientAuthEKU` — cert with ServerAuth-only → rejected.
- `TestListener_AcceptsValidCN` — happy path.
- `TestRunSession_LogsAuditOnEntry` — capture logs; verify session_started event fired.
- `TestRunSession_LogsAuditOnExit` — verify session_ended event with duration.

## Verification

```bash
go build ./...
go vet ./cmd/clawkerd/...
go test ./cmd/clawkerd/... -race -v -run TestListener
go test ./cmd/clawkerd/... -race -v -run TestRunSession

# Confirm EKU check present
grep -n 'ExtKeyUsageClientAuth' cmd/clawkerd/listener.go

make test
```

## Dependencies

None. Independent. Parallel-safe with Task #7 (different file in same dir).

## Risks / gotchas

- **`pinPeerCNToCP` is on the server side** (clawkerd's listener). Validates the CP's peer cert. EKU to assert is `ClientAuth` (CP is the client). Agent leaf cert carries both ClientAuth + ServerAuth because the same cert flips roles depending on direction.
- **bufconn pattern**: standard Go gRPC test pattern. `google.golang.org/grpc/test/bufconn`. Simulates a network listener without binding ports.
- **CA setup in tests**: tests need to mint test certs signed by a test CA. Steal the pattern from existing `internal/auth/auth_test.go` or similar. Don't bind a real CA path.
- **Audit log volume**: Sessions are long-lived (server-streaming, agent's lifetime). Two log lines per Session is negligible.
- **defer + duration capture**: `time.Since(startedAt)` requires `startedAt` to be in scope at defer time — declare it before the deferred func.
- **`peer_thumbprint` field naming**: match the field name used elsewhere (e.g. `agentregistry` may use `thumbprint_hex`). Stay consistent.
- **Don't accidentally leak the cert** to log output — only the CN + thumbprint are safe.

## Reference reading

- `cmd/clawkerd/listener.go` (current implementation)
- `cmd/clawkerd/CLAUDE.md` (if it exists — clawkerd-runs-as-root context)
- `internal/auth/agent_cert.go` (dual-EKU mint code)
- `internal/auth/auth_test.go` (cert-minting test patterns)
- `internal/controlplane/grpc_mtls_agent_test.go` (sibling: in-process gRPC mTLS test pattern)
- Task #7 file (sibling clawkerd fix — both touch `cmd/clawkerd/`)

## Resolution

(Filled in on completion.)

- Commit SHA:
- Notes:
