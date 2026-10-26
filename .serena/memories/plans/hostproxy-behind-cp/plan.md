> Retained plan (2026-10-26). Approved by operator. Status: NOT STARTED. Branch `feat/hostproxy-behind-cp` exists locally from `chore/adversarial-host-listen` (#599) with no commits. Check source before reuse.

# Host proxy behind the CP

Source: Mythos probe `mem:security/probes/2026-10-26-sandbox-escape-probe` secondaries 1 (`/callback/register` binds any host loopback port) and 2 (`/git/credential` has no server-side policy gate; `store`/`erase` mutate host credential store).

## Goal

Agents cannot reach the host proxy directly. Every host action goes agent -> clawkerd loopback relay -> CP `AgentService` RPC (per-container authorization, audit with agent identity) -> host proxy (mTLS, accepts only clawker-CA client certs).

## Facts that drive the design

- The host proxy cannot identify the calling container: on Docker Desktop, traffic to `host.docker.internal` is NATed. Only the CP has agent identity (mTLS + peer IP + registry).
- `forward_https` (`GitHTTPSEnabled`) is only a container-side gate (`internal/workspace/git.go:27`, `container_start.go:513`). `server.go` registers `POST /git/credential` with no condition, so a container with `forward_https: false` can still `curl` it.
- The CP cannot read `clawker.yaml`. #593 pushes `enable_host_proxy` in `FirewallEnableRequest` to the firewall `EnrollmentStore`. That is firewall-only and fails the rule "never gate CP behavior on firewall.enable".
- Prior art: #596 `clawkerd/otlp_relay.go` (loopback HTTP -> `AgentService.ExportTelemetry` over the clawkerd bootstrap leaf, no bearer token) and `controlplane/agent/export_handler.go` (identity interceptor + registry thumbprint check).
- The host proxy listens on host loopback only (`server.go` Start). The eBPF host-proxy lane is `common.h` ~L906 (`host_proxy_ip`, `host_proxy_port`).
- Container scripts: `internal/hostproxy/internals/host-open.sh`, `git-credential-clawker.sh`, `cmd/callback-forwarder`.

## Operator rulings

- D1 policy source: container LABELS written by the CLI at create (`enable_host_proxy`, `forward_https`). The CP reads them through the identity interceptor. Live apply from #593 is lost; changes take effect at the next create.
- D2 placement: new layer just below the release branch #600. `gh stack add` only adds on top. Procedure: commit on `feat/hostproxy-behind-cp`, `git rebase --onto feat/hostproxy-behind-cp chore/adversarial-host-listen release/mythos-hardening`, `gh stack unstack`, `gh stack init` with all 10 branches bottom to top, `gh stack submit --auto`, then mark the new PR ready. Existing PRs survive.
- D3: e2e must not open a real browser on the host. Check how `TestFirewall_HostProxyReachable` wires the host proxy. If there is no safe path, the callback and open-url allow paths become CP-handler unit tests; e2e covers deny paths and git.
- Callback design B: register carries the auth URL; the server egress-checks it, requires `redirect_uri` = `http://localhost|127.0.0.1:<port>` with that port, port >= 1024, TTL <= 5 min, opens the browser itself, closes the listener after first capture.
- git `store`/`erase` stay allowed when `forward_https` is on (policy grant).
- Native Linux reachability (CP and host proxy on a loopback-only listener) is a FOLLOW-UP: no Linux host available (bigdaddy offline).

## Steps (tests first in each)

1. Proto: `AgentService` RPCs `OpenURL`, `GitCredential`, `RegisterCallback`, `GetCallbackData`, `DeleteCallback`. Public scope like `ExportTelemetry`.
2. CP host-proxy domain package (not under firewall): peer -> container + thumbprint; policy from labels; `enable_host_proxy` off -> PermissionDenied; `GitCredential` needs `forward_https`; `OpenURL` and `RegisterCallback` egress-checked against the CP rule store when the firewall is on; callback rules above; callback sessions owned by the registering container. Forward to the host proxy.
3. Host proxy: mTLS listener, client cert chained to the clawker CA (CP for actions, CLI for health/status); register-and-open as one action; one-shot listener.
4. clawkerd: loopback relay serving the current HTTP paths, mapped to the RPCs; `CLAWKER_HOST_PROXY` points at it. `host-open.sh` uses the single register-and-open call.
5. Remove the eBPF host-proxy lane (BPF C fields and branch) and #593's `host_proxy` enrollment plumbing (`FirewallEnableRequest.host_proxy`, `EnrollmentStore` field). Run `make clawker` after BPF edits.
6. e2e (single `-run` each; stop live agents first; `clawker controlplane up` after): direct agent -> `host.docker.internal:<port>` denied; `enable_host_proxy: false` -> 403; `forward_https: false` -> git 403, on -> get works; callback owned by agent A, agent B 403; open/url to denied host 403.
7. Docs and memory: `docs/threat-model.mdx`, `docs/security.mdx`, `docs/credentials.mdx`, hostproxy and clawkerd `AGENTS.md`, Serena `runtime/core`, `controlplane/core`, `controlplane/firewall/core`, firewall-uat skill host-proxy rows.
