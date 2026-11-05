> Retained plan (rewritten 2026-10-27). Approved by operator. Branch `feat/hostproxy-behind-cp` (#604, stack #605). Steps 1-7 of the first design are DONE (CP authorization, clawkerd relay, eBPF lane removal, e2e, docs, `host_proxy.port` merge + migration). The REVERSE CONNECTION rework below: steps 1-5 DONE in code (unit tests pass), step 6 (restack, submit, e2e, bigdaddy UAT) open. Check source before reuse.

# Host proxy behind the CP

Source: Mythos probe `mem:security/probes/2026-10-26-sandbox-escape-probe` secondaries 1 (`/callback/register` binds any host loopback port) and 2 (`/git/credential` has no server-side policy gate).

## Goal

Agents cannot reach the host proxy. Every host action goes agent -> clawkerd loopback relay -> CP `AgentService` RPC (all authorization) -> host proxy. The host proxy keeps the egress check, redirect check, callback port bind, and browser open.

## Why the reverse connection

Linux UAT (bigdaddy, rootless, 2026-10-27): the CP dial to `host.docker.internal:<port>` (172.17.0.1) is refused. The host proxy binds host loopback, and on rootless Docker 172.17.0.1 is in the rootlesskit namespace. The CP admin port is host-published on 127.0.0.1, which works on macOS, rootful and rootless Linux. So the host proxy dials the CP.

## Operator rulings (2026-10-27)

- RPC: new service `api/hostproxy/v1` `HostProxyService.Connect` (bidi stream; the host proxy sends results, the CP sends requests; each carries an `id`; requests are a oneof of the AgentService host-action request messages). Served on the ADMIN listener (7443): ports follow trust lanes, and the host proxy is a CLI-CA host process. gRPC routes services by path; no gateway.
- Identity: own Hydra client `clawker-hostproxy` with its own ES256 key/JWK and its own scope, plus its own CLI-CA client leaf (CN `clawker-hostproxy`). `Connect` pins the client ID (interceptor) and the CN (handler).
- No TCP listener in the host proxy except the short-lived OAuth callback ports. Health = the daemon process is alive; it keeps reconnecting with backoff. `host_proxy.port` is dead: the unreleased migration removes `port`, `manager.port`, `daemon.port`.
- Single instance: the daemon holds an exclusive `flock` on its PID file for its whole life (before, only the port bind guarded it).
- The CP holds ONE stream. The newest replaces the old one; pending calls on the old one fail with `Unavailable`. No connected host proxy -> `Unavailable`.
- Prior rulings stay: grants from container labels (`dev.clawker.host_proxy`, `dev.clawker.git_https`), callback-session ownership in `controlplane/hostgateway`, e2e opens no real browser.

## Rework steps (tests first in each)

1. Proto + scope map (`api/hostproxy/v1`), Makefile proto inputs.
2. consts + `internal/auth`: host proxy signing key/JWK + client leaf; remove the server leaf; CP container mounts the host proxy JWK.
3. CP: Hydra registers the host proxy client; admin listener routes `HostProxyService` to its own auth interceptor (scope + client-ID pin); `hostgateway` holds the stream (CN pin, newest wins, pending map, deadlines); remove the HTTPS dial and `buildHostGateway`'s HTTP client.
4. Host proxy: action executor (old handlers without HTTP) + CP connector (dial 127.0.0.1:<admin port>, own token source, backoff); daemon flock; manager = PID liveness; `serve --port` removed; config `host_proxy.port` removed + migration.
5. e2e `TestFirewall_HostActionsBehindCP`: drop the port setup and the direct-dial probe; add a reconnect check after `controlplane down`/`up`.
6. Docs, AGENTS.md, Serena memories; restack, submit, `make clawker`, e2e, bigdaddy UAT.
